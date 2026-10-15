package vm

import (
	"fmt"
	"maps"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// PromotedObject décrit un objet global promu à l'amorçage (ARCHTIME) dont les propriétés ont été internées
// dans la zone de processus. À chaque requête, un descripteur d'objet léger est instancié dans la Zone courante
// et ses propriétés pointent directement vers les valeurs gelées de processZone.
type PromotedObject struct {
	ClassName string
	Class     *bytecode.ClassProto
	PropSlots []types.Value
	DynProps  map[string]types.Value
}

// ArchtimeImage regroupe les variables globales et constantes promues dans la zone de processus et gelées avant
// le démarrage des workers de requête (ARCHTIME). Les requêtes lisent ces valeurs sans copie et déclenchent une
// séparation CoW vers leur propre Zone en cas de mutation.
type ArchtimeImage struct {
	Globals   map[string]types.Value
	Objects   map[string]PromotedObject
	Constants map[string]types.Value
}

// SnapshotBootstrap exécute prog dans une arène d'amorçage temporaire, promeut toutes les variables globales
// (hors superglobales) et constantes utilisateur dans la zone de processus, puis gèle la zone de processus.
func SnapshotBootstrap(prog *bytecode.Program, processCap uint32) (*ArchtimeImage, error) {
	if processCap > 0 {
		types.ReserveProcessZone(processCap)
	}
	bootZone := zone.New(max(processCap, 16<<20))
	var outBuf, errBuf strings.Builder
	v := New(bootZone, &outBuf, &errBuf, prog, nil)
	v.resetInlineCachesIfProgramChanged()
	v.runMain()
	if v.stop && v.exit != 0 {
		return nil, fmt.Errorf("vm: bootstrap exited with code %d (stdout=%q stderr=%q)", v.exit, outBuf.String(), errBuf.String())
	}

	img := &ArchtimeImage{
		Globals:   make(map[string]types.Value),
		Objects:   make(map[string]PromotedObject),
		Constants: make(map[string]types.Value),
	}

	promoteGlobal := func(name string, val types.Value) error {
		if name == "" || name == "GLOBALS" || isSuperglobal(name) || strings.HasPrefix(name, "\x00") {
			return nil
		}
		d := types.Deref(bootZone, val)
		if d.IsUndef() {
			return nil
		}
		if d.Type <= types.TypeArray {
			iv, err := types.InternProcessValue(bootZone, d)
			if err != nil {
				return err
			}
			img.Globals[strings.Clone(name)] = iv
			return nil
		}
		if d.Type == types.TypeObject {
			info := types.ObjectInfo(bootZone, d)
			if info.ClassID == bytecode.ClassClosure || info.ClassID == bytecode.ClassGenerator {
				return nil
			}
			c := v.classByID(info.ClassID)
			if c == nil {
				return nil
			}
			slots := make([]types.Value, info.PropCount)
			for i := uint32(0); i < info.PropCount; i++ {
				pv := types.Deref(bootZone, types.ObjectGet(bootZone, d, i))
				if pv.IsUndef() {
					slots[i] = types.NewUndef()
					continue
				}
				if pv.Type > types.TypeArray {
					return nil
				}
				iv, err := types.InternProcessValue(bootZone, pv)
				if err != nil {
					return err
				}
				slots[i] = iv
			}
			var dyn map[string]types.Value
			if info.DynOffset != types.EmptySlot {
				dyn = make(map[string]types.Value)
				dynArr := types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: info.DynOffset}
				okDyn := true
				var dynErr error
				types.ArrayForEach(bootZone, dynArr, func(k, dv types.Value) bool {
					if k.Type != types.TypeString {
						return true
					}
					dv = types.Deref(bootZone, dv)
					if dv.Type > types.TypeArray {
						okDyn = false
						return false
					}
					iv, err := types.InternProcessValue(bootZone, dv)
					if err != nil {
						dynErr = err
						return false
					}
					dyn[strings.Clone(types.StringData(bootZone, k))] = iv
					return true
				})
				if dynErr != nil {
					return dynErr
				}
				if !okDyn {
					return nil
				}
			}
			cloner := progCloner{fns: make(map[*bytecode.OpArray]*bytecode.OpArray)}
			img.Objects[strings.Clone(name)] = PromotedObject{
				ClassName: strings.Clone(c.Name),
				Class:     cloner.class(c),
				PropSlots: slots,
				DynProps:  dyn,
			}
		}
		return nil
	}

	for name, val := range v.globals {
		if err := promoteGlobal(name, val); err != nil {
			return nil, err
		}
	}
	if len(v.frames) > 0 {
		root := &v.frames[0]
		for i, name := range root.fn.CVNames {
			if err := promoteGlobal(name, v.slots[root.cvBase+i]); err != nil {
				return nil, err
			}
		}
	}

	for _, name := range v.userConsts {
		if val, ok := v.constCache[name]; ok {
			d := types.Deref(bootZone, val)
			if !d.IsUndef() && d.Type <= types.TypeArray {
				iv, err := types.InternProcessValue(bootZone, d)
				if err != nil {
					return nil, err
				}
				img.Constants[strings.Clone(name)] = iv
			}
		}
	}
	if prog != nil {
		for _, c := range prog.Constants {
			if _, exists := img.Constants[c.Name]; exists {
				continue
			}
			var val types.Value
			var err error
			if c.IsString {
				val, err = types.InternProcessString(c.Text)
			} else {
				val, err = types.InternProcessValue(bootZone, c.Value)
			}
			if err != nil {
				return nil, err
			}
			img.Constants[strings.Clone(c.Name)] = val
		}
	}

	v.finishRequest()
	bootZone.Reset(nil)
	types.FreezeProcessZone()
	return img, nil
}

func (vm *VM) registerPromotedClass(proto *bytecode.ClassProto, replace *bytecode.ClassProto) uint32 {
	vm.cloneProgram()
	cloner := progCloner{fns: make(map[*bytecode.OpArray]*bytecode.OpArray)}
	cp := cloner.class(proto)
	oldID := cp.ID
	var id uint32
	if replace != nil {
		id = replace.ID
	} else {
		id = vm.allocClassID()
	}
	cp.ID = id
	mapID := func(cid uint32) uint32 {
		if cid == oldID {
			return id
		}
		return cid
	}
	for i := range cp.Props {
		cp.Props[i].ClassID = mapID(cp.Props[i].ClassID)
	}
	for i := range cp.ResolvedProps {
		cp.ResolvedProps[i].ClassID = mapID(cp.ResolvedProps[i].ClassID)
	}
	for i := range cp.Methods {
		cp.Methods[i].ClassID = mapID(cp.Methods[i].ClassID)
		fn := cp.Methods[i].Func
		if fn == nil {
			continue
		}
		fn.FuncID = vm.allocFuncID()
		for j := range fn.Code {
			op := &fn.Code[j]
			if _, dynamic := bytecode.ClassOperandCV(*op); dynamic {
				continue
			}
			switch op.Op {
			case bytecode.OpInitNew, bytecode.OpInstanceof:
				op.Op1 = mapID(op.Op1)
			case bytecode.OpInitStaticMethodCall, bytecode.OpFetchClassConst, bytecode.OpFetchStaticPropR, bytecode.OpAssignStaticProp:
				op.Op2 = mapID(op.Op2)
			}
		}
	}
	if replace != nil {
		vm.replaceClass(replace, cp)
	} else {
		vm.Program.Classes = append(vm.Program.Classes, cp)
	}
	vm.indexClass(cp)
	vm.ownedClasses[cp] = true
	vm.classState[id] = true
	return id
}

func (vm *VM) applyArchtimeImage() {
	if vm.Context == nil || vm.Context.ArchtimeImage == nil {
		return
	}
	img := vm.Context.ArchtimeImage
	maps.Copy(vm.globals, img.Globals)
	for name, pobj := range img.Objects {
		cid, ok := vm.resolveName(pobj.ClassName)
		if ok && pobj.Class != nil {
			if ex := vm.classByID(cid); ex != nil && classIsPlaceholder(ex) {
				cid = vm.registerPromotedClass(pobj.Class, ex)
			}
		} else if !ok && pobj.Class != nil {
			cid = vm.registerPromotedClass(pobj.Class, nil)
			ok = true
		}
		if !ok {
			continue
		}
		obj, err := types.NewObject(vm.Zone, cid, uint32(len(pobj.PropSlots)), vm.hasMethod(cid, "__destruct"))
		if err != nil {
			continue
		}
		for i, pv := range pobj.PropSlots {
			if !pv.IsUndef() {
				types.ObjectSet(vm.Zone, obj, uint32(i), pv)
			}
		}
		for k, dv := range pobj.DynProps {
			vm.dynamicPropSet(obj, k, dv)
		}
		vm.globals[name] = obj
	}
}

func (vm *VM) seedRootGlobals() {
	if vm.Context == nil || vm.Context.ArchtimeImage == nil || len(vm.frames) != 1 {
		return
	}
	img := vm.Context.ArchtimeImage
	root := &vm.frames[0]
	for i, name := range root.fn.CVNames {
		_, inGlobals := img.Globals[name]
		_, inObjects := img.Objects[name]
		if !inGlobals && !inObjects {
			continue
		}
		if val, ok := vm.globals[name]; ok {
			slot := &vm.slots[root.cvBase+i]
			if slot.IsUndef() {
				*slot = val
				delete(vm.globals, name)
			}
		}
	}
}
