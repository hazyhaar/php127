package vm

import (
	"fmt"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/ext"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func opaqueResourceClassID(extra uint16) (uint32, bool) {
	switch extra {
	case ext.ResourceKindGD:
		return bytecode.ClassGdImage, true
	case ext.ResourceKindFinfo:
		return bytecode.ClassFinfo, true
	case ext.ResourceKindCurl:
		return bytecode.ClassCurlHandle, true
	case ext.ResourceKindXML:
		return bytecode.ClassXMLParser, true
	}
	return 0, false
}

func (vm *VM) classByID(id uint32) *bytecode.ClassProto {
	if vm.Program != nil {
		if idx := int(id) - int(bytecode.ClassUser); idx >= 0 && idx < len(vm.Program.Classes) {
			if c := vm.Program.Classes[idx]; c != nil && c.ID == id {
				return c
			}
		}
		if vm.classIndex != nil {
			if c, ok := vm.classIndex[id]; ok {
				return c
			}
			return builtinProto(id)
		}
		for _, c := range vm.Program.Classes {
			if c != nil && c.ID == id {
				return c
			}
		}
		for _, c := range vm.Program.Traits {
			if c != nil && c.ID == id {
				return c
			}
		}
	}
	return builtinProto(id)
}

// displayClassName rend le nom de classe tel que l'affichent var_dump et les traces de PHP : le nom interne d'une
// classe anonyme s'y arrête à son octet nul.
func displayClassName(name string) string {
	if i := strings.IndexByte(name, 0); i >= 0 {
		return name[:i]
	}
	return name
}

func (vm *VM) className(id uint32) string {
	if name, ok := bytecode.BuiltinClassName(id); ok {
		return name
	}
	if c := vm.classByID(id); c != nil {
		return c.Name
	}
	return "class"
}

func (vm *VM) findMethod(classID uint32, name string) (bytecode.MethodProto, uint32, bool) {
	seen := 0
	for classID != bytecode.ClassNone && seen < 64 {
		seen++
		c := vm.classByID(classID)
		if c == nil {
			return bytecode.MethodProto{}, 0, false
		}
		for _, m := range c.Methods {
			if strings.EqualFold(m.Name, name) && m.Func != nil {
				if m.ClassID == 0 {
					m.ClassID = c.ID
				}
				return m, m.ClassID, true
			}
		}
		if c.ParentID == bytecode.ClassNone || c.ParentID == classID {
			return bytecode.MethodProto{}, 0, false
		}
		classID = c.ParentID
	}
	return bytecode.MethodProto{}, 0, false
}

func (vm *VM) hasMethod(classID uint32, name string) bool {
	_, _, ok := vm.findMethod(classID, name)
	return ok
}

func (vm *VM) visible(vis bytecode.Visibility, def uint32) bool {
	if vis == bytecode.VisPublic {
		return true
	}
	scope := uint32(bytecode.ClassNone)
	if len(vm.frames) > 0 {
		scope = vm.frame().scopeClass
	}
	if vis == bytecode.VisPrivate {
		return scope == def
	}
	return vm.related(scope, def)
}

func (vm *VM) related(a, b uint32) bool {
	if a == 0 || b == 0 {
		return false
	}
	return vm.inherits(a, b) || vm.inherits(b, a) || a == b
}

func (vm *VM) inherits(id, ancestor uint32) bool {
	seen := 0
	for id != 0 && id != bytecode.ClassNone && seen < 64 {
		if id == ancestor {
			return true
		}
		c := vm.classByID(id)
		if c == nil {
			return false
		}
		id = c.ParentID
		seen++
	}
	return false
}

func (vm *VM) visName(v bytecode.Visibility) string {
	switch v {
	case bytecode.VisPrivate:
		return "private"
	case bytecode.VisProtected:
		return "protected"
	default:
		return "public"
	}
}

func (vm *VM) instancePropCount(classID uint32) uint32 {
	if c := vm.classByID(classID); c != nil && classID >= bytecode.ClassUser && c.PropsResolved {
		return c.InstancePropCount
	}
	if vm.isExceptionFamily(classID) {
		extra := uint32(0)
		if c := vm.classByID(classID); c != nil && classID >= bytecode.ClassUser {
			extra = countOwnProps(c)
		}
		return 4 + extra
	}
	n := uint32(0)
	var chain [64]uint32
	nChain := 0
	id := classID
	for id != 0 && id != bytecode.ClassNone && nChain < 64 {
		chain[nChain] = id
		nChain++
		c := vm.classByID(id)
		if c == nil {
			break
		}
		id = c.ParentID
	}
	for i := nChain - 1; i >= 0; i-- {
		c := vm.classByID(chain[i])
		if c == nil {
			continue
		}
		for _, p := range c.Props {
			if !p.Static {
				n++
			}
		}
	}
	return n
}

func countOwnProps(c *bytecode.ClassProto) uint32 {
	n := uint32(0)
	for _, p := range c.Props {
		if !p.Static {
			n++
		}
	}
	return n
}

func (vm *VM) propIndex(classID uint32, name string, scope uint32) (uint32, bytecode.PropInfo, bool) {
	if classID >= bytecode.ClassUser {
		if c := vm.classByID(classID); c != nil && c.PropsResolved {
			var found bytecode.PropInfo
			hasFound := false
			for i := range c.ResolvedProps {
				p := c.ResolvedProps[i]
				if p.Name == name {
					if p.Visibility == bytecode.VisPrivate {
						if p.ClassID == scope {
							return p.Index, p, true
						}
						if p.ClassID != classID {
							continue
						}
					}
					found = p
					hasFound = true
				}
			}
			if hasFound {
				return found.Index, found, true
			}
			return 0, bytecode.PropInfo{}, false
		}
	}
	base := uint32(0)
	if vm.isExceptionFamily(classID) && classID >= bytecode.ClassUser {
		base = 4
	}
	var chain [64]*bytecode.ClassProto
	nChain := 0
	id := classID
	for id != 0 && id != bytecode.ClassNone && nChain < 64 {
		c := vm.classByID(id)
		if c == nil {
			break
		}
		chain[nChain] = c
		nChain++
		id = c.ParentID
	}
	index := base
	var found bytecode.PropInfo
	ok := false
	foundIndex := uint32(0)
	for i := nChain - 1; i >= 0; i-- {
		c := chain[i]
		for _, p := range c.Props {
			if p.Static {
				continue
			}
			if p.Name == name {
				if p.Visibility == bytecode.VisPrivate {
					if c.ID == scope {
						p.Index = index
						p.ClassID = c.ID
						return index, p, true
					}
					if c.ID != classID {
						index++
						continue
					}
				}
				found = p
				found.Index = index
				if found.ClassID == 0 {
					found.ClassID = c.ID
				}
				foundIndex = index
				ok = true
			}
			index++
		}
	}
	if !ok {
		return 0, bytecode.PropInfo{}, false
	}
	return foundIndex, found, true
}

func (vm *VM) classAllowsDynamicProperties(cid uint32) bool {
	if cid == bytecode.ClassStdClass || cid < bytecode.ClassUser {
		return true
	}
	seen := 0
	for id := cid; id != 0 && id != bytecode.ClassNone && seen < 64; seen++ {
		c := vm.classByID(id)
		if c == nil {
			break
		}
		for _, attr := range c.Attributes {
			aname := strings.TrimPrefix(attr.Name, `\`)
			if strings.EqualFold(aname, "AllowDynamicProperties") {
				return true
			}
		}
		id = c.ParentID
	}
	return false
}

// emitDynamicPropDeprecation signale la création d'une propriété dynamique ; le résultat vrai signale que le gestionnaire d'erreurs a interrompu l'instruction.
func (vm *VM) emitDynamicPropDeprecation(cid uint32, name string) bool {
	if !vm.classAllowsDynamicProperties(cid) {
		return vm.raiseDiag(eDeprecated, diagDeprecate, "Creation of dynamic property "+vm.className(cid)+"::$"+name+" is deprecated", vm.line)
	}
	return false
}

func (vm *VM) inMagic(slot uint32, name string) bool {
	if vm.magicGuard == nil {
		return false
	}
	return vm.magicGuard[magicKey{slot: slot, name: name}]
}

func (vm *VM) enterMagic(slot uint32, name string) {
	if vm.magicGuard == nil {
		vm.magicGuard = make(map[magicKey]bool)
	}
	vm.magicGuard[magicKey{slot: slot, name: name}] = true
}

func (vm *VM) leaveMagic(slot uint32, name string) {
	if vm.magicGuard != nil {
		delete(vm.magicGuard, magicKey{slot: slot, name: name})
	}
}

func (vm *VM) callMagicGet(d types.Value, cid uint32, m bytecode.MethodProto, name string) (types.Value, bool) {
	nameVal, err := types.NewStringLiteral(vm.Zone, name)
	if err != nil {
		vm.fatalFeature("chaîne")
		return types.NewNull(), true
	}
	vm.enterMagic(d.DataSlot, name)
	ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, []types.Value{nameVal}, cid, m.ClassID, vm.line)
	vm.leaveMagic(d.DataSlot, name)
	if threw || vm.stop {
		return types.NewNull(), true
	}
	vm.control = false
	return ret, false
}

func (vm *VM) callMagicSet(d types.Value, cid uint32, m bytecode.MethodProto, name string, val types.Value) bool {
	nameVal, err := types.NewStringLiteral(vm.Zone, name)
	if err != nil {
		vm.fatalFeature("chaîne")
		return true
	}
	argVal := vmRetain(vm.Zone, types.Deref(vm.Zone, val))
	vm.enterMagic(d.DataSlot, name)
	ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, []types.Value{nameVal, argVal}, cid, m.ClassID, vm.line)
	vm.leaveMagic(d.DataSlot, name)
	if !threw && !vm.stop {
		vmRelease(vm.Zone, &ret)
		vm.control = false
	}
	return threw || vm.stop
}

func (vm *VM) initProps(classID uint32, obj types.Value) {
	if classID >= bytecode.ClassUser {
		if c := vm.classByID(classID); c != nil && c.PropsResolved {
			if c.InstancePropCount >= 4 && len(c.ResolvedProps) > 0 && c.ResolvedProps[0].Index == 4 || (len(c.ResolvedProps) == 0 && c.InstancePropCount == 4) {
				vm.setExceptionProps(obj, "", 0, vm.file(), vm.line)
			}
			for i := range c.ResolvedProps {
				p := c.ResolvedProps[i]
				v := vm.propDefault(p)
				types.ObjectSet(vm.Zone, obj, p.Index, v)
				vmRelease(vm.Zone, &v)
			}
			return
		}
	}
	if vm.isExceptionFamily(classID) {
		vm.setExceptionProps(obj, "", 0, vm.file(), vm.line)
	}
	var chain [64]*bytecode.ClassProto
	nChain := 0
	for c := vm.classByID(classID); c != nil && nChain < 64; c = vm.classByID(c.ParentID) {
		chain[nChain] = c
		nChain++
	}
	idx := uint32(0)
	if vm.isExceptionFamily(classID) {
		idx = 4
	}
	for i := nChain - 1; i >= 0; i-- {
		c := chain[i]
		for _, p := range c.Props {
			if p.Static {
				continue
			}
			v := vm.propDefault(p)
			types.ObjectSet(vm.Zone, obj, idx, v)
			vmRelease(vm.Zone, &v)
			idx++
		}
	}
}

func (vm *VM) resolvePropName(fr *frame, nameIdx uint32) (string, bool) {
	raw := fr.fn.Strings[nameIdx]
	if idxStr, ok := strings.CutPrefix(raw, "\x00cv:"); ok {
		cvIdx, err := strconv.Atoi(idxStr)
		if err == nil && fr.cvBase+cvIdx < len(vm.slots) {
			v := vm.slots[fr.cvBase+cvIdx]
			if v.IsUndef() {
				cvName := ""
				if cvIdx < len(fr.fn.CVNames) {
					cvName = fr.fn.CVNames[cvIdx]
				}
				vm.reportDiag(eWarning, diagWarning, "Undefined variable $"+cvName, vm.file(), vm.line)
				return "", true
			}
			ks, own := vm.stringify(v)
			s := ""
			if ks.Type == types.TypeString {
				s = types.StringData(vm.Zone, ks)
			}
			if own {
				types.ReleaseValue(vm.Zone, &ks)
			}
			return s, true
		}
	}
	return raw, false
}

func (vm *VM) opFetchProp(op bytecode.Opcode, nameIdx uint32, flags uint16) {
	if flags&bytecode.FlagFuncArg != 0 && vm.funcArgByRef() {
		op = bytecode.OpFetchPropW
	}
	obj := vm.pop()
	if vm.stop {
		vmRelease(vm.Zone, &obj)
		return
	}
	fr := vm.frame()
	name, dynName := vm.resolvePropName(fr, nameIdx)
	pcache := vm.propCache(fr.fn)
	if !dynName && op == bytecode.OpFetchPropR && obj.Type == types.TypeObject && fr.pc < len(pcache) {
		ic := pcache[fr.pc]
		if ic.Fast && types.ObjectInfo(vm.Zone, obj).ClassID == ic.ClassID {
			cur := types.ObjectGet(vm.Zone, obj, ic.SlotIdx)
			if !cur.IsUndef() && cur.Type != types.TypeReference {
				res := vmRetain(vm.Zone, cur)
				vmRelease(vm.Zone, &obj)
				if vm.stop || vm.control {
					vmRelease(vm.Zone, &res)
					return
				}
				vm.push(res)
				vm.bump()
				return
			}
		}
	}
	d := types.Deref(vm.Zone, obj)
	if d.Type != types.TypeObject {
		tname := vm.typeNameForDiag(d)
		vmRelease(vm.Zone, &obj)
		if flags&bytecode.FlagIsset != 0 || op == bytecode.OpFetchDimIs {
			vm.push(types.NewNull())
			vm.bump()
			return
		}
		if op == bytecode.OpFetchPropW {
			vm.raiseBuiltin(bytecode.ClassError, fmt.Sprintf("Attempt to modify property %q on %s", name, tname), 0)
			return
		}
		if vm.raiseDiag(eWarning, diagWarning, fmt.Sprintf("Attempt to read property %q on %s", name, tname), vm.line) {
			return
		}
		vm.push(types.NewNull())
		vm.bump()
		return
	}
	cid := types.ObjectInfo(vm.Zone, d).ClassID
	idx, info, ok := vm.propIndex(cid, name, fr.scopeClass)
	if ok && !vm.visible(info.Visibility, info.ClassID) {
		if op != bytecode.OpFetchPropW && !vm.inMagic(d.DataSlot, name) {
			if m, _, mOK := vm.findMethod(cid, "__get"); mOK {
				ret, threw := vm.callMagicGet(d, cid, m, name)
				vmRelease(vm.Zone, &obj)
				if !threw && !vm.stop && !vm.control {
					vm.push(ret)
					vm.bump()
				} else {
					vmRelease(vm.Zone, &ret)
				}
				return
			}
		}
		vmRelease(vm.Zone, &obj)
		vm.raiseBuiltin(bytecode.ClassError, "Cannot access "+vm.visName(info.Visibility)+" property "+vm.className(cid)+"::$"+name, 0)
		return
	}
	if !ok {
		if op == bytecode.OpFetchPropW {
			if _, exists := vm.dynamicPropGet(d, name); !exists {
				if vm.emitDynamicPropDeprecation(cid, name) {
					vmRelease(vm.Zone, &obj)
					return
				}
			}
			if ref, dynOK := vm.dynamicPropRef(d, name); dynOK {
				vmRelease(vm.Zone, &obj)
				if !vm.stop && !vm.control {
					vm.push(ref)
					vm.bump()
				} else {
					vmRelease(vm.Zone, &ref)
				}
				return
			}
		}
		if v, dynOK := vm.dynamicPropGet(d, name); dynOK {
			vmRelease(vm.Zone, &obj)
			if !vm.stop && !vm.control {
				vm.push(v)
				vm.bump()
			} else {
				vmRelease(vm.Zone, &v)
			}
			return
		}
		if !vm.inMagic(d.DataSlot, name) {
			if m, _, mOK := vm.findMethod(cid, "__get"); mOK {
				ret, threw := vm.callMagicGet(d, cid, m, name)
				vmRelease(vm.Zone, &obj)
				if !threw && !vm.stop && !vm.control {
					vm.push(ret)
					vm.bump()
				} else {
					vmRelease(vm.Zone, &ret)
				}
				return
			}
		}
		vmRelease(vm.Zone, &obj)
		if flags&bytecode.FlagIsset == 0 && vm.raiseDiag(eWarning, diagWarning, "Undefined property: "+vm.className(cid)+"::$"+name, vm.line) {
			return
		}
		vm.push(types.NewNull())
		vm.bump()
		return
	}
	cur := types.ObjectGet(vm.Zone, d, idx)
	if cur.IsUndef() && op != bytecode.OpFetchPropW {
		if !vm.inMagic(d.DataSlot, name) {
			if m, _, mOK := vm.findMethod(cid, "__get"); mOK {
				ret, threw := vm.callMagicGet(d, cid, m, name)
				vmRelease(vm.Zone, &obj)
				if !threw && !vm.stop && !vm.control {
					vm.push(ret)
					vm.bump()
				} else {
					vmRelease(vm.Zone, &ret)
				}
				return
			}
		}
		if info.TypeName != "" {
			vmRelease(vm.Zone, &obj)
			vm.raiseBuiltin(bytecode.ClassError, "Typed property "+vm.className(info.ClassID)+"::$"+name+" must not be accessed before initialization", 0)
			return
		}
		vmRelease(vm.Zone, &obj)
		if flags&bytecode.FlagIsset == 0 && vm.raiseDiag(eWarning, diagWarning, "Undefined property: "+vm.className(cid)+"::$"+name, vm.line) {
			return
		}
		vm.push(types.NewNull())
		vm.bump()
		return
	}
	if !dynName && op == bytecode.OpFetchPropR && !cur.IsUndef() && !info.Static && info.Visibility == bytecode.VisPublic && !info.Readonly && info.TypeName == "" {
		if c := vm.ensurePropCache(fr.fn); fr.pc < len(c) {
			c[fr.pc] = bytecode.PropIC{ClassID: cid, SlotIdx: idx, Fast: true}
		}
	}
	if op == bytecode.OpFetchPropW {
		var res types.Value
		if cur.Type == types.TypeReference {
			res = types.RetainReference(vm.Zone, cur)
		} else {
			initVal := cur
			if initVal.IsUndef() {
				initVal = types.NewNull()
			}
			r, err := types.NewReference(vm.Zone, initVal)
			if err != nil {
				vmRelease(vm.Zone, &obj)
				vm.fatalFeature("référence")
				return
			}
			types.ObjectSet(vm.Zone, d, idx, r)
			res = types.RetainReference(vm.Zone, r)
			types.ReleaseValue(vm.Zone, &r)
		}
		vmRelease(vm.Zone, &obj)
		if vm.stop || vm.control {
			vmRelease(vm.Zone, &res)
			return
		}
		vm.push(res)
		vm.bump()
		return
	}
	res := vmRetain(vm.Zone, types.Deref(vm.Zone, cur))
	vmRelease(vm.Zone, &obj)
	if vm.stop || vm.control {
		vmRelease(vm.Zone, &res)
		return
	}
	vm.push(res)
	vm.bump()
}

func (vm *VM) opAssignProp(op bytecode.Opcode, in bytecode.Instr) {
	val := vm.pop()
	obj := vm.pop()
	if vm.stop {
		vmRelease(vm.Zone, &val)
		vmRelease(vm.Zone, &obj)
		return
	}
	fr := vm.frame()
	name, dynName := vm.resolvePropName(fr, in.Op1)
	pcache := vm.propCache(fr.fn)
	if !dynName && obj.Type == types.TypeObject && fr.pc < len(pcache) {
		ic := pcache[fr.pc]
		if ic.Fast && types.ObjectInfo(vm.Zone, obj).ClassID == ic.ClassID {
			cur := types.ObjectGet(vm.Zone, obj, ic.SlotIdx)
			if !cur.IsUndef() && cur.Type != types.TypeReference {
				if op == bytecode.OpAssignPropOp {
					opCode := bytecode.Opcode(in.Op2)
					if opCode == bytecode.OpAdd && cur.Type == types.TypeLong && val.Type == types.TypeLong {
						val = types.Add(cur, val)
					} else if opCode == bytecode.OpSub && cur.Type == types.TypeLong && val.Type == types.TypeLong {
						val = types.Sub(cur, val)
					} else if opCode == bytecode.OpMul && cur.Type == types.TypeLong && val.Type == types.TypeLong {
						val = types.Mul(cur, val)
					} else {
						val = vm.applyOp(opCode, cur, val)
						if vm.stop || vm.control {
							vmRelease(vm.Zone, &val)
							vmRelease(vm.Zone, &obj)
							return
						}
					}
				}
				types.ObjectSet(vm.Zone, obj, ic.SlotIdx, val)
				vmRelease(vm.Zone, &obj)
				if vm.stop || vm.control {
					vmRelease(vm.Zone, &val)
					return
				}
				if in.Flags&bytecode.FlagDiscard == 0 {
					vm.push(val)
				} else {
					vmRelease(vm.Zone, &val)
				}
				vm.bump()
				return
			}
		}
	}
	d := types.Deref(vm.Zone, obj)
	if d.Type != types.TypeObject {
		tname := vm.typeNameForDiag(d)
		vmRelease(vm.Zone, &val)
		vmRelease(vm.Zone, &obj)
		vm.raiseBuiltin(bytecode.ClassError, fmt.Sprintf("Attempt to assign property %q on %s", name, tname), 0)
		return
	}
	cid := types.ObjectInfo(vm.Zone, d).ClassID
	idx, info, ok := vm.propIndex(cid, name, fr.scopeClass)
	if ok && !vm.visible(info.Visibility, info.ClassID) {
		if op == bytecode.OpAssignProp && !vm.inMagic(d.DataSlot, name) {
			if m, _, mOK := vm.findMethod(cid, "__set"); mOK {
				threw := vm.callMagicSet(d, cid, m, name, val)
				vmRelease(vm.Zone, &obj)
				if !threw && !vm.stop && !vm.control {
					if in.Flags&bytecode.FlagDiscard == 0 {
						vm.push(val)
					} else {
						vmRelease(vm.Zone, &val)
					}
					vm.bump()
				} else {
					vmRelease(vm.Zone, &val)
				}
				return
			}
		}
		vmRelease(vm.Zone, &val)
		vmRelease(vm.Zone, &obj)
		vm.raiseBuiltin(bytecode.ClassError, "Cannot access "+vm.visName(info.Visibility)+" property "+vm.className(cid)+"::$"+name, 0)
		return
	}
	if !ok {
		if op == bytecode.OpAssignPropOp {
			cur, dynOK := vm.dynamicPropGet(d, name)
			if !dynOK && !vm.inMagic(d.DataSlot, name) {
				if m, _, mOK := vm.findMethod(cid, "__get"); mOK {
					var threw bool
					cur, threw = vm.callMagicGet(d, cid, m, name)
					if threw || vm.stop || vm.control {
						vmRelease(vm.Zone, &cur)
						vmRelease(vm.Zone, &val)
						vmRelease(vm.Zone, &obj)
						return
					}
					dynOK = true
				}
			}
			if !dynOK {
				if vm.raiseDiag(eWarning, diagWarning, "Undefined property: "+vm.className(cid)+"::$"+name, vm.line) {
					vmRelease(vm.Zone, &val)
					vmRelease(vm.Zone, &obj)
					return
				}
				cur = types.NewNull()
			}
			val = vm.applyOp(bytecode.Opcode(in.Op2), cur, val)
			vmRelease(vm.Zone, &cur)
			if vm.stop || vm.control {
				vmRelease(vm.Zone, &val)
				vmRelease(vm.Zone, &obj)
				return
			}
		}
		if existing, exists := vm.dynamicPropGet(d, name); exists {
			vmRelease(vm.Zone, &existing)
			vm.dynamicPropSet(d, name, val)
			vmRelease(vm.Zone, &obj)
			if !vm.stop && !vm.control {
				if in.Flags&bytecode.FlagDiscard == 0 {
					vm.push(val)
				} else {
					vmRelease(vm.Zone, &val)
				}
				vm.bump()
			} else {
				vmRelease(vm.Zone, &val)
			}
			return
		}
		if !vm.inMagic(d.DataSlot, name) {
			if m, _, mOK := vm.findMethod(cid, "__set"); mOK {
				threw := vm.callMagicSet(d, cid, m, name, val)
				vmRelease(vm.Zone, &obj)
				if !threw && !vm.stop && !vm.control {
					if in.Flags&bytecode.FlagDiscard == 0 {
						vm.push(val)
					} else {
						vmRelease(vm.Zone, &val)
					}
					vm.bump()
				} else {
					vmRelease(vm.Zone, &val)
				}
				return
			}
		}
		if vm.emitDynamicPropDeprecation(cid, name) {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &obj)
			return
		}
		if vm.dynamicPropSet(d, name, val) {
			vmRelease(vm.Zone, &obj)
			if !vm.stop && !vm.control {
				if in.Flags&bytecode.FlagDiscard == 0 {
					vm.push(val)
				} else {
					vmRelease(vm.Zone, &val)
				}
				vm.bump()
			} else {
				vmRelease(vm.Zone, &val)
			}
			return
		}
		vmRelease(vm.Zone, &val)
		vmRelease(vm.Zone, &obj)
		vm.raiseBuiltin(bytecode.ClassError, "Undefined property "+name, 0)
		return
	}
	cur := types.ObjectGet(vm.Zone, d, idx)
	if cur.IsUndef() && op == bytecode.OpAssignProp && !vm.inMagic(d.DataSlot, name) {
		if m, _, mOK := vm.findMethod(cid, "__set"); mOK {
			threw := vm.callMagicSet(d, cid, m, name, val)
			vmRelease(vm.Zone, &obj)
			if !threw && !vm.stop && !vm.control {
				if in.Flags&bytecode.FlagDiscard == 0 {
					vm.push(val)
				} else {
					vmRelease(vm.Zone, &val)
				}
				vm.bump()
			} else {
				vmRelease(vm.Zone, &val)
			}
			return
		}
	}
	if op == bytecode.OpAssignPropOp {
		if cur.IsUndef() {
			if vm.raiseDiag(eWarning, diagWarning, "Undefined property: "+vm.className(cid)+"::$"+name, vm.line) {
				vmRelease(vm.Zone, &val)
				vmRelease(vm.Zone, &obj)
				return
			}
			cur = types.NewNull()
		}
		val = vm.applyOp(bytecode.Opcode(in.Op2), cur, val)
		if vm.stop || vm.control {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &obj)
			return
		}
	}
	if info.Readonly {
		if !cur.IsUndef() {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &obj)
			vm.raiseBuiltin(bytecode.ClassError, "Cannot modify readonly property "+displayClassName(vm.className(info.ClassID))+"::$"+name, 0)
			return
		}
	}
	if info.TypeName != "" && !vm.checkProperty(&val, info) {
		vmRelease(vm.Zone, &val)
		vmRelease(vm.Zone, &obj)
		return
	}
	if !dynName && !info.Static && info.Visibility == bytecode.VisPublic && !info.Readonly && info.TypeName == "" {
		if c := vm.ensurePropCache(fr.fn); fr.pc < len(c) {
			c[fr.pc] = bytecode.PropIC{ClassID: cid, SlotIdx: idx, Fast: true}
		}
	}
	types.ObjectSet(vm.Zone, d, idx, val)
	vmRelease(vm.Zone, &obj)
	if vm.stop || vm.control {
		vmRelease(vm.Zone, &val)
		return
	}
	if in.Flags&bytecode.FlagDiscard == 0 {
		vm.push(val)
	} else {
		vmRelease(vm.Zone, &val)
	}
	vm.bump()
}

func (vm *VM) opPropQuery(op bytecode.Opcode, nameIdx uint32) {
	obj := vm.pop()
	if vm.stop {
		vmRelease(vm.Zone, &obj)
		return
	}
	d := types.Deref(vm.Zone, obj)
	if d.Type != types.TypeObject {
		vmRelease(vm.Zone, &obj)
		if op == bytecode.OpUnsetProp {
			vm.bump()
			return
		}
		vm.push(types.NewBool(op == bytecode.OpEmptyProp))
		vm.bump()
		return
	}
	cid := types.ObjectInfo(vm.Zone, d).ClassID
	name, _ := vm.resolvePropName(vm.frame(), nameIdx)
	idx, info, ok := vm.propIndex(cid, name, vm.frame().scopeClass)
	accessible := ok && vm.visible(info.Visibility, info.ClassID)
	if accessible {
		cur := types.ObjectGet(vm.Zone, d, idx)
		if !cur.IsUndef() {
			if op == bytecode.OpUnsetProp {
				empty := types.NewUndef()
				types.ObjectSet(vm.Zone, d, idx, empty)
				vmRelease(vm.Zone, &obj)
				vm.bump()
				return
			}
			if op == bytecode.OpEmptyProp {
				vm.push(types.NewBool(!types.ToBoolZ(vm.Zone, cur)))
			} else {
				vm.push(types.NewBool(isSetValue(vm.Zone, cur)))
			}
			vmRelease(vm.Zone, &obj)
			vm.bump()
			return
		}
	}
	if !ok {
		if res, dynOK := vm.dynamicPropQuery(op, d, name); dynOK {
			vmRelease(vm.Zone, &obj)
			if op != bytecode.OpUnsetProp {
				vm.push(types.NewBool(res))
			}
			vm.bump()
			return
		}
	}
	if !vm.inMagic(d.DataSlot, name) {
		if op == bytecode.OpUnsetProp {
			if m, _, mOK := vm.findMethod(cid, "__unset"); mOK {
				nameVal, err := types.NewStringLiteral(vm.Zone, name)
				if err != nil {
					vmRelease(vm.Zone, &obj)
					vm.fatalFeature("chaîne")
					return
				}
				vm.enterMagic(d.DataSlot, name)
				ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, []types.Value{nameVal}, cid, m.ClassID, vm.line)
				vm.leaveMagic(d.DataSlot, name)
				vmRelease(vm.Zone, &obj)
				if !threw && !vm.stop {
					vmRelease(vm.Zone, &ret)
					vm.control = false
					vm.bump()
				}
				return
			}
		} else {
			if m, _, mOK := vm.findMethod(cid, "__isset"); mOK {
				nameVal, err := types.NewStringLiteral(vm.Zone, name)
				if err != nil {
					vmRelease(vm.Zone, &obj)
					vm.fatalFeature("chaîne")
					return
				}
				vm.enterMagic(d.DataSlot, name)
				ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, []types.Value{nameVal}, cid, m.ClassID, vm.line)
				vm.leaveMagic(d.DataSlot, name)
				if threw || vm.stop {
					vmRelease(vm.Zone, &obj)
					return
				}
				vm.control = false
				issetRes := types.ToBoolZ(vm.Zone, ret)
				vmRelease(vm.Zone, &ret)
				if !issetRes {
					vmRelease(vm.Zone, &obj)
					vm.push(types.NewBool(op == bytecode.OpEmptyProp))
					vm.bump()
					return
				}
				if op == bytecode.OpIssetProp {
					vmRelease(vm.Zone, &obj)
					vm.push(types.NewBool(true))
					vm.bump()
					return
				}
				if gm, _, gmOK := vm.findMethod(cid, "__get"); gmOK {
					gret, gthrew := vm.callMagicGet(d, cid, gm, name)
					vmRelease(vm.Zone, &obj)
					if gthrew || vm.stop {
						vmRelease(vm.Zone, &gret)
						return
					}
					emptyRes := !types.ToBoolZ(vm.Zone, gret)
					vmRelease(vm.Zone, &gret)
					vm.push(types.NewBool(emptyRes))
					vm.bump()
					return
				}
				vmRelease(vm.Zone, &obj)
				vm.push(types.NewBool(false))
				vm.bump()
				return
			}
		}
	}
	if op == bytecode.OpUnsetProp {
		if ok && !accessible {
			vmRelease(vm.Zone, &obj)
			vm.raiseBuiltin(bytecode.ClassError, "Cannot access "+vm.visName(info.Visibility)+" property "+vm.className(cid)+"::$"+name, 0)
			return
		}
		vmRelease(vm.Zone, &obj)
		vm.bump()
		return
	}
	vmRelease(vm.Zone, &obj)
	vm.push(types.NewBool(op == bytecode.OpEmptyProp))
	vm.bump()
}

func (vm *VM) staticSlots(classID uint32) []types.Value {
	c := vm.classByID(classID)
	n := 0
	if c != nil {
		for _, p := range c.Props {
			if p.Static {
				n++
			}
		}
	}
	slots := vm.statics[classID]
	if len(slots) < n {
		next := make([]types.Value, n)
		copy(next, slots)
		i := 0
		for _, p := range c.Props {
			if p.Static {
				if i >= len(slots) {
					next[i] = vm.propDefault(p)
				}
				i++
			}
		}
		if vm.statics == nil {
			vm.statics = make(map[uint32][]types.Value)
		}
		vm.statics[classID] = next
		slots = next
	}
	return slots
}

func (vm *VM) staticIndex(classID uint32, name string) (uint32, bool) {
	c := vm.classByID(classID)
	if c == nil {
		return 0, false
	}
	i := uint32(0)
	for _, p := range c.Props {
		if !p.Static {
			continue
		}
		if p.Name == name {
			return i, true
		}
		i++
	}
	if c.ParentID != 0 {
		return vm.staticIndex(c.ParentID, name)
	}
	return 0, false
}

func (vm *VM) opStaticProp(assign bool, in bytecode.Instr) {
	name := vm.frame().fn.Strings[in.Op1]
	classID := vm.resolveCallClass(in)
	var info bytecode.PropInfo
	for c := vm.classByID(classID); c != nil; c = vm.classByID(c.ParentID) {
		found := false
		for _, p := range c.Props {
			if p.Static && p.Name == name {
				info = p
				classID = c.ID
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	idx, ok := vm.staticIndex(classID, name)
	if !ok {
		if assign {
			v := vm.pop()
			types.ReleaseValue(vm.Zone, &v)
		}
		vm.raiseBuiltin(bytecode.ClassError, "Access to undeclared static property", 0)
		return
	}
	slots := vm.staticSlots(classID)
	if assign {
		v := vm.pop()
		if !vm.checkProperty(&v, info) {
			types.ReleaseValue(vm.Zone, &v)
			return
		}
		if int(idx) < len(slots) {
			types.ReleaseValue(vm.Zone, &slots[idx])
			slots[idx] = v
			vm.push(types.RetainValue(vm.Zone, v))
		}
		vm.bump()
		return
	}
	if int(idx) >= len(slots) || slots[idx].IsUndef() {
		if info.TypeName != "" {
			vm.raiseBuiltin(bytecode.ClassError, "Typed static property "+vm.className(classID)+"::$"+name+" must not be accessed before initialization", 0)
			return
		}
		vm.push(types.NewNull())
	} else {
		vm.push(types.RetainValue(vm.Zone, slots[idx]))
	}
	vm.bump()
}

func (vm *VM) opClassConst(in bytecode.Instr) {
	name := vm.frame().fn.Strings[in.Op1]
	if in.Flags&bytecode.FlagClassMagic != 0 || name == "class" {
		id := vm.frame().calledClass
		if in.Flags&bytecode.FlagSelf != 0 {
			id = vm.frame().scopeClass
		}
		s, err := types.NewStringLiteral(vm.Zone, vm.className(id))
		if err != nil {
			vm.fatalFeature("constante de classe")
			return
		}
		vm.push(s)
		vm.bump()
		return
	}
	classID := in.Op2
	if in.Flags&bytecode.FlagStatic != 0 {
		classID = vm.frame().calledClass
	}
	if in.Flags&bytecode.FlagSelf != 0 {
		classID = vm.frame().scopeClass
	}
	if in.Flags&bytecode.FlagParent != 0 {
		classID = vm.parentOf(vm.frame().scopeClass)
	}
	if value, ok := vm.lookupClassConst(classID, name); ok {
		if vm.stop || vm.control {
			// L'évaluation différée de la constante a levé une exception.
			vmRelease(vm.Zone, &value)
			return
		}
		vm.push(value)
		vm.bump()
		return
	}
	vm.raiseBuiltin(bytecode.ClassError, "Undefined class constant "+name, 0)
}

func (vm *VM) lookupClassConst(classID uint32, name string) (types.Value, bool) {
	start := classID
	if c := vm.classByID(classID); c != nil && c.Kind == bytecode.KindEnum {
		cases := vm.enumCases(c)
		for i, entry := range c.Cases {
			if entry.Name == name {
				return types.RetainValue(vm.Zone, cases[i]), true
			}
		}
	}
	seen := 0
	for classID != 0 && classID != bytecode.ClassNone && seen < 64 {
		if v, ok := vm.lookupNativeClassConst(classID, name); ok {
			return v, true
		}
		c := vm.classByID(classID)
		if c == nil {
			return types.Value{}, false
		}
		for _, cst := range c.Constants {
			if cst.Name == name {
				if cst.Initial != nil {
					return vm.constantValue(cst.Initial), true
				}
				if cst.IsString {
					return vm.constantValue(&bytecode.Constant{Text: cst.Text, IsString: true}), true
				}
				return types.RetainValue(vm.Zone, cst.Value), true
			}
		}
		classID = c.ParentID
		seen++
	}
	for id, depth := start, 0; id >= bytecode.ClassUser && depth < 64; depth++ {
		c := vm.classByID(id)
		if c == nil {
			break
		}
		if v, ok := vm.interfaceConst(c.Interfaces, name, 0); ok {
			return v, true
		}
		if c.ParentID == id {
			break
		}
		id = c.ParentID
	}
	return types.Value{}, false
}

// interfaceConst cherche une constante héritée d'une interface utilisateur, y compris par les interfaces des classes parentes.
func (vm *VM) interfaceConst(names []string, name string, depth int) (types.Value, bool) {
	if depth > 32 {
		return types.Value{}, false
	}
	for _, iname := range names {
		iid, ok := vm.resolveName(iname)
		if !ok || iid < bytecode.ClassUser {
			continue
		}
		ic := vm.classByID(iid)
		if ic == nil {
			continue
		}
		for _, cst := range ic.Constants {
			if cst.Name == name {
				if cst.Initial != nil {
					return vm.constantValue(cst.Initial), true
				}
				if cst.IsString {
					return vm.constantValue(&bytecode.Constant{Text: cst.Text, IsString: true}), true
				}
				return types.RetainValue(vm.Zone, cst.Value), true
			}
		}
		if v, ok := vm.interfaceConst(ic.Interfaces, name, depth+1); ok {
			return v, true
		}
	}
	return types.Value{}, false
}

func (vm *VM) opInstanceof(classID uint32) {
	v := vm.pop()
	if vm.stop {
		return
	}
	defer types.ReleaseValue(vm.Zone, &v)
	d := types.Deref(vm.Zone, v)
	if d.Type == types.TypeResource {
		cid, ok := opaqueResourceClassID(d.Extra)
		vm.push(types.NewBool(ok && cid == vm.aliasTarget(classID)))
		vm.bump()
		return
	}
	if d.Type != types.TypeObject {
		vm.push(types.NewBool(false))
		vm.bump()
		return
	}
	id := types.ObjectInfo(vm.Zone, d).ClassID
	vm.push(types.NewBool(vm.isInstance(id, classID)))
	vm.bump()
}

func (vm *VM) isInstance(objClass, want uint32) bool {
	if objClass == want {
		return true
	}
	want = vm.aliasTarget(want)
	if objClass == want {
		return true
	}
	if want == bytecode.ClassThrowable {
		return vm.isExceptionFamily(objClass) || objClass == bytecode.ClassThrowable
	}
	seen := 0
	id := objClass
	for id != 0 && id != bytecode.ClassNone && seen < 64 {
		if id == want {
			return true
		}
		c := vm.classByID(id)
		if c == nil {
			return false
		}
		for _, name := range c.Interfaces {
			if iid, ok := vm.resolveName(name); ok && (iid == want || vm.inherits(iid, want) || vm.interfaceExtends(iid, want, 0)) {
				return true
			}
		}
		id = c.ParentID
		seen++
	}
	return false
}

// interfaceExtends parcourt les interfaces parentes d'une interface utilisateur, déclarées par extends dans Interfaces.
func (vm *VM) interfaceExtends(iid, want uint32, depth int) bool {
	if depth > 32 || iid < bytecode.ClassUser {
		return false
	}
	c := vm.classByID(iid)
	if c == nil || c.Kind != bytecode.KindInterface {
		return false
	}
	for _, name := range c.Interfaces {
		pid, ok := vm.resolveName(name)
		if !ok {
			continue
		}
		if pid == want || vm.interfaceExtends(pid, want, depth+1) {
			return true
		}
	}
	return false
}

func (vm *VM) resolveName(name string) (uint32, bool) {
	name = strings.TrimPrefix(name, `\`)
	if id, ok := bytecode.BuiltinClassID(name); ok {
		return id, true
	}
	if vm.Program == nil {
		return 0, false
	}
	if len(vm.classAliases) > 0 {
		if id, ok := vm.classAliases[strings.ToLower(name)]; ok {
			return id, true
		}
	}
	if vm.classNames != nil {
		id, ok := vm.classNames[strings.ToLower(name)]
		return id, ok
	}
	for _, c := range vm.Program.Classes {
		if c != nil && strings.EqualFold(c.Name, name) {
			return c.ID, true
		}
	}
	for _, c := range vm.Program.Traits {
		if c != nil && strings.EqualFold(c.Name, name) {
			return c.ID, true
		}
	}
	return 0, false
}

// aliasTarget remplace l'identifiant d'une classe non déclarée dont le nom est un alias de class_alias par celui de la classe visée.
func (vm *VM) aliasTarget(id uint32) uint32 {
	if len(vm.classAliases) == 0 || id < bytecode.ClassUser {
		return id
	}
	c := vm.classByID(id)
	if c == nil {
		return id
	}
	if target, ok := vm.classAliases[strings.ToLower(c.Name)]; ok {
		return target
	}
	return id
}

// classOperand résout un opérande de classe de bytecode vers une classe déclarée, en passant par les alias, les déclarations en attente puis l'autoloader SPL.
func (vm *VM) classOperand(id uint32) (uint32, bool) {
	if _, builtin := bytecode.BuiltinClassName(id); builtin {
		return id, true
	}
	c := vm.classByID(id)
	if c == nil {
		return id, false
	}
	if vm.classDeclared(c) {
		return id, true
	}
	if target, ok := vm.declaredClassByName(c.Name, true); ok {
		return target.ID, true
	}
	return id, false
}

// dynamicClassOperand résout la classe désignée à l'exécution par la variable compilée cv du cadre courant : un
// objet désigne sa classe, une chaîne une classe déclarée, autochargée au besoin. Pour instanceof (lenient), une
// classe introuvable rend 0 sans autoload ni erreur, comme ZEND_INSTANCEOF. Un refus rend le message de l'Error de
// PHP, que l'appelant lève après avoir dépilé ses opérandes ; une exception de l'autoloader laisse vm.control posé.
func (vm *VM) dynamicClassOperand(cv uint32, lenient bool) (uint32, string) {
	fr := vm.frame()
	v := types.Deref(vm.Zone, vm.slots[fr.cvBase+int(cv)])
	switch v.Type {
	case types.TypeObject:
		return types.ObjectInfo(vm.Zone, v).ClassID, ""
	case types.TypeResource:
		if cid, ok := opaqueResourceClassID(v.Extra); ok {
			return cid, ""
		}
	case types.TypeString:
		name := strings.Clone(strings.TrimPrefix(types.StringData(vm.Zone, v), `\`))
		if id, ok := bytecode.BuiltinClassID(name); ok {
			return id, ""
		}
		c, ok := vm.declaredClassByName(name, !lenient)
		if ok {
			return c.ID, ""
		}
		if lenient || vm.stop || vm.control {
			return 0, ""
		}
		return 0, `Class "` + name + `" not found`
	}
	return 0, "Class name must be a valid object or a string"
}

// dynamicClassName empile le résultat de $x::class : le nom de la classe d'un objet ; toute autre valeur lève la
// TypeError de PHP.
func (vm *VM) dynamicClassName(cv uint32) {
	fr := vm.frame()
	v := types.Deref(vm.Zone, vm.slots[fr.cvBase+int(cv)])
	var cid uint32
	if v.Type == types.TypeObject {
		cid = types.ObjectInfo(vm.Zone, v).ClassID
	} else if rc, ok := opaqueResourceClassID(v.Extra); v.Type == types.TypeResource && ok {
		cid = rc
	} else {
		vm.raiseBuiltin(bytecode.ClassTypeError, `Cannot use "::class" on `+vm.operandTypeName(v), 0)
		return
	}
	s, err := types.NewStringLiteral(vm.Zone, vm.className(cid))
	if err != nil {
		vm.fatalFeature("constante de classe")
		return
	}
	vm.push(s)
	vm.bump()
}

func (vm *VM) raiseClassNotFound(id uint32) {
	vm.raiseBuiltin(bytecode.ClassError, `Class "`+vm.className(id)+`" not found`, 0)
}

// classIsPlaceholder reconnaît le prototype vide que le compilateur crée pour un nom de classe seulement référencé.
func classIsPlaceholder(c *bytecode.ClassProto) bool {
	return c.Kind == bytecode.KindClass && c.Flags == 0 && c.ParentName == "" && len(c.Interfaces) == 0 && len(c.Traits) == 0 &&
		len(c.Props) == 0 && len(c.Methods) == 0 && len(c.Constants) == 0 && len(c.Attributes) == 0 && len(c.Cases) == 0
}

// classDeclared distingue une classe déclarée d'un nom seulement référencé : état explicite pour les classes liées dynamiquement, sinon classement du programme principal.
func (vm *VM) classDeclared(c *bytecode.ClassProto) bool {
	if c == nil {
		return false
	}
	if c.ID < bytecode.ClassUser {
		return true
	}
	if _, builtin := bytecode.BuiltinClassName(c.ID); builtin {
		return true
	}
	if st, ok := vm.classState[c.ID]; ok {
		return st
	}
	if vm.classState == nil {
		vm.classState = make(map[uint32]bool)
	}
	declared := vm.mainDeclares(c)
	if declared && !classIsPlaceholder(c) {
		// État provisoire contre une chaîne d'héritage cyclique pendant l'examen des ancêtres.
		vm.classState[c.ID] = true
		if vm.mainNeedsLateBinding(c) {
			declared = false
			vm.pendingClasses = append(vm.pendingClasses, vm.mainPendingEntry(c))
		}
	}
	vm.classState[c.ID] = declared
	return declared
}

// mainNeedsLateBinding signale une classe du programme principal dont le parent ou un trait n'est pas encore déclaré, typiquement fourni par un fichier inclus plus tard ou par l'autoloader : PHP ne la déclare qu'à l'exécution de son instruction, la VM la met en attente de liaison.
func (vm *VM) mainNeedsLateBinding(c *bytecode.ClassProto) bool {
	if c.ParentName != "" {
		if _, builtin := bytecode.BuiltinClassID(c.ParentName); !builtin {
			p := vm.classByID(c.ParentID)
			if c.ParentID == bytecode.ClassNone || p == nil || !strings.EqualFold(strings.TrimPrefix(p.Name, `\`), strings.TrimPrefix(c.ParentName, `\`)) {
				if _, ok := vm.declaredClassByName(c.ParentName, false); !ok {
					return true
				}
			} else if !vm.classDeclared(p) {
				return true
			}
		}
	}
	for _, t := range c.Traits {
		if _, ok := vm.declaredClassByName(t, false); !ok {
			return true
		}
	}
	// PHP déclare à l'exécution de son instruction une classe dont une interface n'est pas encore connue : un fichier inclus ou l'autoloader peut la fournir avant.
	for _, iname := range c.Interfaces {
		if !vm.interfaceReady(iname) {
			return true
		}
	}
	return false
}

// mainPendingEntry situe une classe du programme principal mise en attente : fichier principal et ligne de sa déclaration relue.
func (vm *VM) mainPendingEntry(c *bytecode.ClassProto) pendingClass {
	// Une classe anonyme n'est déclarée qu'à l'exécution de son new class, jamais au niveau du fichier.
	entry := pendingClass{id: c.ID, topLevel: c.Flags&bytecode.ClassAnonymous == 0}
	if vm.Program != nil && vm.Program.Main != nil {
		entry.file = vm.Program.Main.Filename
	}
	if !vm.mainDeclDone {
		vm.mainDeclDone = true
		vm.mainDecl = mainFileDeclarations(vm.Program.Main)
	}
	if vm.mainDecl != nil {
		if site, ok := vm.mainDecl.classes[strings.ToLower(strings.TrimPrefix(c.Name, `\`))]; ok {
			entry.line = site.line
			entry.topLevel = site.topLevel
		}
	}
	return entry
}

// mainDeclares classe un prototype du programme principal : non vide, ou d'identifiant inférieur à celui d'une déclaration non vide (le compilateur alloue les déclarations avant les références), ou nommé par une déclaration du fichier principal relu.
func (vm *VM) mainDeclares(c *bytecode.ClassProto) bool {
	if !classIsPlaceholder(c) {
		return true
	}
	if vm.Program == nil {
		return false
	}
	for _, list := range [][]*bytecode.ClassProto{vm.Program.Classes, vm.Program.Traits} {
		for _, other := range list {
			if other != nil && other.ID > c.ID && !classIsPlaceholder(other) {
				return true
			}
		}
	}
	if !vm.mainDeclDone {
		vm.mainDeclDone = true
		vm.mainDecl = mainFileDeclarations(vm.Program.Main)
	}
	if vm.mainDecl == nil {
		return true
	}
	_, ok := vm.mainDecl.classes[strings.ToLower(c.Name)]
	return ok
}

// declaredClassByName cherche une classe déclarée par son nom ou un alias ; autoload déclenche les déclarations en attente puis la chaîne spl_autoload_register.
func (vm *VM) declaredClassByName(name string, autoload bool) (*bytecode.ClassProto, bool) {
	name = strings.TrimPrefix(name, `\`)
	if name == "" {
		return nil, false
	}
	look := func() (*bytecode.ClassProto, bool) {
		id, ok := vm.resolveName(name)
		if !ok {
			return nil, false
		}
		if _, builtin := bytecode.BuiltinClassName(id); builtin {
			if bp := builtinProto(id); bp != nil {
				return bp, true
			}
			return &bytecode.ClassProto{ID: id, Name: name}, true
		}
		c := vm.classByID(id)
		if c == nil || !vm.classDeclared(c) {
			return nil, false
		}
		return c, true
	}
	if c, ok := look(); ok {
		return c, true
	}
	if !autoload || vm.stop {
		return nil, false
	}
	if vm.bindPendingNamed(name) {
		if c, ok := look(); ok {
			return c, true
		}
	}
	if vm.autoloadClass(name) {
		return look()
	}
	return nil, false
}

func (vm *VM) isExceptionFamily(id uint32) bool {
	return vm.inherits(id, bytecode.ClassException) || vm.inherits(id, bytecode.ClassError) || id == bytecode.ClassException || id == bytecode.ClassError
}

func (vm *VM) opClone() {
	v := vm.pop()
	if vm.stop {
		return
	}
	d := types.Deref(vm.Zone, v)
	if d.Type != types.TypeObject {
		types.ReleaseValue(vm.Zone, &v)
		vm.raiseBuiltin(bytecode.ClassError, "__clone method called on non-object", 0)
		return
	}
	info := types.ObjectInfo(vm.Zone, d)
	obj, err := types.NewObject(vm.Zone, info.ClassID, info.PropCount, vm.hasMethod(info.ClassID, "__destruct"))
	if err != nil {
		types.ReleaseValue(vm.Zone, &v)
		vm.fatalFeature("clone")
		return
	}
	for i := uint32(0); i < info.PropCount; i++ {
		types.ObjectSet(vm.Zone, obj, i, types.ObjectGet(vm.Zone, d, i))
	}
	if info.DynOffset != types.EmptySlot {
		dynArr := types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: info.DynOffset}
		types.ArrayForEach(vm.Zone, dynArr, func(k, dv types.Value) bool {
			if k.Type == types.TypeString {
				vm.dynamicPropSet(obj, types.StringData(vm.Zone, k), dv)
			}
			return true
		})
	}
	types.ReleaseValue(vm.Zone, &v)
	if m, _, ok := vm.findMethod(info.ClassID, "__clone"); ok {
		ret, threw := vm.invokeOpArray(m.Func, obj, true, nil, info.ClassID, m.ClassID, vm.line)
		if threw || vm.stop {
			types.ReleaseValue(vm.Zone, &obj)
			return
		}
		types.ReleaseValue(vm.Zone, &ret)
	}
	vm.push(obj)
	vm.bump()
}

func builtinProto(id uint32) *bytecode.ClassProto {
	switch id {
	case bytecode.ClassThrowable:
		return &bytecode.ClassProto{Name: "Throwable", ID: bytecode.ClassThrowable, Kind: bytecode.KindInterface}
	case bytecode.ClassException:
		return &bytecode.ClassProto{Name: "Exception", ID: bytecode.ClassException, ParentID: bytecode.ClassThrowable, Interfaces: []string{"Throwable"}}
	case bytecode.ClassError:
		return &bytecode.ClassProto{Name: "Error", ID: bytecode.ClassError, ParentID: bytecode.ClassThrowable, Interfaces: []string{"Throwable"}}
	case bytecode.ClassTypeError:
		return &bytecode.ClassProto{Name: "TypeError", ID: bytecode.ClassTypeError, ParentID: bytecode.ClassError}
	case bytecode.ClassValueError:
		return &bytecode.ClassProto{Name: "ValueError", ID: bytecode.ClassValueError, ParentID: bytecode.ClassError}
	case bytecode.ClassArithmeticError:
		return &bytecode.ClassProto{Name: "ArithmeticError", ID: bytecode.ClassArithmeticError, ParentID: bytecode.ClassError}
	case bytecode.ClassDivisionByZeroError:
		return &bytecode.ClassProto{Name: "DivisionByZeroError", ID: bytecode.ClassDivisionByZeroError, ParentID: bytecode.ClassArithmeticError}
	case bytecode.ClassUnhandledMatchError:
		return &bytecode.ClassProto{Name: "UnhandledMatchError", ID: bytecode.ClassUnhandledMatchError, ParentID: bytecode.ClassError}
	case bytecode.ClassArgumentCountError:
		return &bytecode.ClassProto{Name: "ArgumentCountError", ID: bytecode.ClassArgumentCountError, ParentID: bytecode.ClassTypeError}
	case bytecode.ClassClosure:
		return &bytecode.ClassProto{Name: "Closure", ID: bytecode.ClassClosure}
	case bytecode.ClassGenerator:
		return &bytecode.ClassProto{Name: "Generator", ID: id, Interfaces: []string{"Traversable", "Iterator"}}
	case bytecode.ClassTraversable:
		return &bytecode.ClassProto{Name: "Traversable", ID: id, Kind: bytecode.KindInterface}
	case bytecode.ClassIterator:
		return &bytecode.ClassProto{Name: "Iterator", ID: id, ParentID: bytecode.ClassTraversable, Kind: bytecode.KindInterface, Interfaces: []string{"Traversable"}}
	case bytecode.ClassIteratorAggregate:
		return &bytecode.ClassProto{Name: "IteratorAggregate", ID: id, ParentID: bytecode.ClassTraversable, Kind: bytecode.KindInterface, Interfaces: []string{"Traversable"}}
	case bytecode.ClassSeekableIterator:
		return &bytecode.ClassProto{Name: "SeekableIterator", ID: id, ParentID: bytecode.ClassIterator, Kind: bytecode.KindInterface, Interfaces: []string{"Iterator", "Traversable"}}
	case bytecode.ClassCountable:
		return &bytecode.ClassProto{Name: "Countable", ID: id, Kind: bytecode.KindInterface}
	case bytecode.ClassArrayAccess:
		return &bytecode.ClassProto{Name: "ArrayAccess", ID: id, Kind: bytecode.KindInterface}
	case bytecode.ClassUnitEnum:
		return &bytecode.ClassProto{Name: "UnitEnum", ID: id, Kind: bytecode.KindInterface}
	case bytecode.ClassBackedEnum:
		return &bytecode.ClassProto{Name: "BackedEnum", ID: id, ParentID: bytecode.ClassUnitEnum, Kind: bytecode.KindInterface}
	case bytecode.ClassRuntimeException:
		return &bytecode.ClassProto{Name: "RuntimeException", ID: id, ParentID: bytecode.ClassException}
	case bytecode.ClassLogicException:
		return &bytecode.ClassProto{Name: "LogicException", ID: id, ParentID: bytecode.ClassException}
	case bytecode.ClassBadFunctionCallException:
		return &bytecode.ClassProto{Name: "BadFunctionCallException", ID: id, ParentID: bytecode.ClassLogicException}
	case bytecode.ClassBadMethodCallException:
		return &bytecode.ClassProto{Name: "BadMethodCallException", ID: id, ParentID: bytecode.ClassBadFunctionCallException}
	case bytecode.ClassDomainException:
		return &bytecode.ClassProto{Name: "DomainException", ID: id, ParentID: bytecode.ClassLogicException}
	case bytecode.ClassInvalidArgumentException:
		return &bytecode.ClassProto{Name: "InvalidArgumentException", ID: id, ParentID: bytecode.ClassLogicException}
	case bytecode.ClassLengthException:
		return &bytecode.ClassProto{Name: "LengthException", ID: id, ParentID: bytecode.ClassLogicException}
	case bytecode.ClassOutOfBoundsException:
		return &bytecode.ClassProto{Name: "OutOfBoundsException", ID: id, ParentID: bytecode.ClassRuntimeException}
	case bytecode.ClassOutOfRangeException:
		return &bytecode.ClassProto{Name: "OutOfRangeException", ID: id, ParentID: bytecode.ClassLogicException}
	case bytecode.ClassOverflowException:
		return &bytecode.ClassProto{Name: "OverflowException", ID: id, ParentID: bytecode.ClassRuntimeException}
	case bytecode.ClassRangeException:
		return &bytecode.ClassProto{Name: "RangeException", ID: id, ParentID: bytecode.ClassRuntimeException}
	case bytecode.ClassUnderflowException:
		return &bytecode.ClassProto{Name: "UnderflowException", ID: id, ParentID: bytecode.ClassRuntimeException}
	case bytecode.ClassUnexpectedValueException:
		return &bytecode.ClassProto{Name: "UnexpectedValueException", ID: id, ParentID: bytecode.ClassRuntimeException}
	case bytecode.ClassFiber:
		return &bytecode.ClassProto{Name: "Fiber", ID: id}
	case bytecode.ClassFiberError:
		return &bytecode.ClassProto{Name: "FiberError", ID: id, ParentID: bytecode.ClassError}
	case bytecode.ClassSplDoublyLinkedList:
		return &bytecode.ClassProto{Name: "SplDoublyLinkedList", ID: id, Interfaces: []string{"Iterator", "Traversable", "Countable", "ArrayAccess"}}
	case bytecode.ClassSplStack:
		return &bytecode.ClassProto{Name: "SplStack", ID: id, ParentID: bytecode.ClassSplDoublyLinkedList, Interfaces: []string{"Iterator", "Traversable", "Countable", "ArrayAccess"}}
	case bytecode.ClassSplQueue:
		return &bytecode.ClassProto{Name: "SplQueue", ID: id, ParentID: bytecode.ClassSplDoublyLinkedList, Interfaces: []string{"Iterator", "Traversable", "Countable", "ArrayAccess"}}
	case bytecode.ClassSplFixedArray:
		return &bytecode.ClassProto{Name: "SplFixedArray", ID: id, Interfaces: []string{"Iterator", "Traversable", "Countable", "ArrayAccess"}}
	case bytecode.ClassSplObjectStorage:
		return &bytecode.ClassProto{Name: "SplObjectStorage", ID: id, Interfaces: []string{"Iterator", "Traversable", "Countable", "ArrayAccess"}}
	case bytecode.ClassArrayObject:
		return &bytecode.ClassProto{Name: "ArrayObject", ID: id, Interfaces: []string{"IteratorAggregate", "Traversable", "Countable", "ArrayAccess"}}
	case bytecode.ClassArrayIterator:
		return &bytecode.ClassProto{Name: "ArrayIterator", ID: id, Interfaces: []string{"SeekableIterator", "Iterator", "Traversable", "Countable", "ArrayAccess"}}
	case bytecode.ClassStdClass:
		return &bytecode.ClassProto{Name: "stdClass", ID: id}
	case bytecode.ClassPDO:
		return &bytecode.ClassProto{Name: "PDO", ID: id}
	case bytecode.ClassPDOStatement:
		return &bytecode.ClassProto{Name: "PDOStatement", ID: id, Interfaces: []string{"IteratorAggregate", "Traversable"}}
	case bytecode.ClassPDOException:
		return &bytecode.ClassProto{Name: "PDOException", ID: id, ParentID: bytecode.ClassRuntimeException}
	case bytecode.ClassDateTimeInterface:
		return &bytecode.ClassProto{Name: "DateTimeInterface", ID: id, Kind: bytecode.KindInterface}
	case bytecode.ClassDateTime:
		return &bytecode.ClassProto{Name: "DateTime", ID: id, Interfaces: []string{"DateTimeInterface"}}
	case bytecode.ClassDateTimeImmutable:
		return &bytecode.ClassProto{Name: "DateTimeImmutable", ID: id, Interfaces: []string{"DateTimeInterface"}}
	case bytecode.ClassDateTimeZone:
		return &bytecode.ClassProto{Name: "DateTimeZone", ID: id}
	case bytecode.ClassDateInterval:
		return &bytecode.ClassProto{Name: "DateInterval", ID: id}
	case bytecode.ClassDatePeriod:
		return &bytecode.ClassProto{Name: "DatePeriod", ID: id, Interfaces: []string{"IteratorAggregate", "Traversable"}}
	case bytecode.ClassDateError:
		return &bytecode.ClassProto{Name: "DateError", ID: id, ParentID: bytecode.ClassError}
	case bytecode.ClassDateObjectError:
		return &bytecode.ClassProto{Name: "DateObjectError", ID: id, ParentID: bytecode.ClassDateError}
	case bytecode.ClassDateRangeError:
		return &bytecode.ClassProto{Name: "DateRangeError", ID: id, ParentID: bytecode.ClassDateError}
	case bytecode.ClassDateException:
		return &bytecode.ClassProto{Name: "DateException", ID: id, ParentID: bytecode.ClassException}
	case bytecode.ClassDateInvalidTimeZoneException:
		return &bytecode.ClassProto{Name: "DateInvalidTimeZoneException", ID: id, ParentID: bytecode.ClassDateException}
	case bytecode.ClassDateInvalidOperationException:
		return &bytecode.ClassProto{Name: "DateInvalidOperationException", ID: id, ParentID: bytecode.ClassDateException}
	case bytecode.ClassDateMalformedStringException:
		return &bytecode.ClassProto{Name: "DateMalformedStringException", ID: id, ParentID: bytecode.ClassDateException}
	case bytecode.ClassDateMalformedIntervalStringException:
		return &bytecode.ClassProto{Name: "DateMalformedIntervalStringException", ID: id, ParentID: bytecode.ClassDateException}
	case bytecode.ClassReflector:
		return &bytecode.ClassProto{Name: "Reflector", ID: id, Kind: bytecode.KindInterface}
	case bytecode.ClassReflectionException:
		return &bytecode.ClassProto{Name: "ReflectionException", ID: id, ParentID: bytecode.ClassException}
	case bytecode.ClassReflectionClass:
		return &bytecode.ClassProto{Name: "ReflectionClass", ID: id, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionObject:
		return &bytecode.ClassProto{Name: "ReflectionObject", ID: id, ParentID: bytecode.ClassReflectionClass, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionFunctionAbstract:
		return &bytecode.ClassProto{Name: "ReflectionFunctionAbstract", ID: id, Flags: bytecode.ClassAbstract, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionFunction:
		return &bytecode.ClassProto{Name: "ReflectionFunction", ID: id, ParentID: bytecode.ClassReflectionFunctionAbstract, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionMethod:
		return &bytecode.ClassProto{Name: "ReflectionMethod", ID: id, ParentID: bytecode.ClassReflectionFunctionAbstract, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionProperty:
		return &bytecode.ClassProto{Name: "ReflectionProperty", ID: id, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionParameter:
		return &bytecode.ClassProto{Name: "ReflectionParameter", ID: id, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionClassConstant:
		return &bytecode.ClassProto{Name: "ReflectionClassConstant", ID: id, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionEnum:
		return &bytecode.ClassProto{Name: "ReflectionEnum", ID: id, ParentID: bytecode.ClassReflectionClass, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionEnumUnitCase:
		return &bytecode.ClassProto{Name: "ReflectionEnumUnitCase", ID: id, ParentID: bytecode.ClassReflectionClassConstant, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionEnumBackedCase:
		return &bytecode.ClassProto{Name: "ReflectionEnumBackedCase", ID: id, ParentID: bytecode.ClassReflectionEnumUnitCase, Interfaces: []string{"Reflector"}}
	case bytecode.ClassReflectionType:
		return &bytecode.ClassProto{Name: "ReflectionType", ID: id, Flags: bytecode.ClassAbstract}
	case bytecode.ClassReflectionNamedType:
		return &bytecode.ClassProto{Name: "ReflectionNamedType", ID: id, ParentID: bytecode.ClassReflectionType}
	case bytecode.ClassReflectionUnionType:
		return &bytecode.ClassProto{Name: "ReflectionUnionType", ID: id, ParentID: bytecode.ClassReflectionType}
	case bytecode.ClassReflectionIntersectionType:
		return &bytecode.ClassProto{Name: "ReflectionIntersectionType", ID: id, ParentID: bytecode.ClassReflectionType}
	case bytecode.ClassReflectionAttribute:
		return &bytecode.ClassProto{Name: "ReflectionAttribute", ID: id, Interfaces: []string{"Reflector"}}
	case bytecode.ClassJsonSerializable:
		return &bytecode.ClassProto{Name: "JsonSerializable", ID: id, Kind: bytecode.KindInterface}
	case bytecode.ClassStringable:
		return &bytecode.ClassProto{Name: "Stringable", ID: id, Kind: bytecode.KindInterface}
	case bytecode.ClassMysqli:
		return &bytecode.ClassProto{Name: "mysqli", ID: id}
	case bytecode.ClassMysqliResult:
		return &bytecode.ClassProto{Name: "mysqli_result", ID: id, Interfaces: []string{"IteratorAggregate", "Traversable"}}
	case bytecode.ClassMysqliStmt:
		return &bytecode.ClassProto{Name: "mysqli_stmt", ID: id}
	case bytecode.ClassMysqliDriver:
		return &bytecode.ClassProto{Name: "mysqli_driver", ID: id, Flags: bytecode.ClassFinal}
	case bytecode.ClassMysqliSqlException:
		return &bytecode.ClassProto{Name: "mysqli_sql_exception", ID: id, ParentID: bytecode.ClassRuntimeException, Flags: bytecode.ClassFinal}
	case bytecode.ClassGdImage:
		return &bytecode.ClassProto{Name: "GdImage", ID: id, Flags: bytecode.ClassFinal}
	case bytecode.ClassFinfo:
		return &bytecode.ClassProto{Name: "finfo", ID: id}
	case bytecode.ClassCurlHandle:
		return &bytecode.ClassProto{Name: "CurlHandle", ID: id, Flags: bytecode.ClassFinal}
	case bytecode.ClassXMLParser:
		return &bytecode.ClassProto{Name: "XMLParser", ID: id, Flags: bytecode.ClassFinal}
	default:
		return nil
	}
}
