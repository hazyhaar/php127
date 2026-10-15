package vm

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func (vm *VM) opConst(in bytecode.Instr) {
	fn := vm.frame().fn
	if in.Flags&bytecode.FlagNamedConst != 0 {
		name := fn.Strings[in.Op1]
		v := vm.lookupConst(name)
		if vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &v)
			return
		}
		vm.push(v)
		vm.bump()
		return
	}
	if in.Flags&bytecode.FlagStringConst != 0 {
		vm.push(vm.materialize(fn, in.Op1))
		vm.bump()
		return
	}
	vm.push(types.RetainValue(vm.Zone, fn.Consts[in.Op1]))
	vm.bump()
}

func (vm *VM) materialize(fn *bytecode.OpArray, idx uint32) types.Value {
	isMain := vm.Program != nil && fn == vm.Program.Main && vm.Context == &vm.defaultCtx
	if isMain {
		if int(idx) < len(vm.mainStrs) && vm.mainStrs[idx].Type == types.TypeString {
			return types.RetainValue(vm.Zone, vm.mainStrs[idx])
		}
	} else if vm.Context != nil {
		if cache := vm.Context.strs[fn]; int(idx) < len(cache) && cache[idx].Type == types.TypeString {
			return types.RetainValue(vm.Zone, cache[idx])
		}
	}
	v, err := types.NewStringLiteral(vm.Zone, fn.Strings[idx])
	if err != nil {
		vm.fatalFeature("chaîne")
		return types.NewNull()
	}
	if isMain {
		if len(vm.mainStrs) < len(fn.Strings) {
			if cap(vm.mainStrs) >= len(fn.Strings) {
				vm.mainStrs = vm.mainStrs[:len(fn.Strings)]
			} else {
				next := make([]types.Value, len(fn.Strings))
				copy(next, vm.mainStrs)
				vm.mainStrs = next
			}
		}
		if vm.mainStrs[idx].Type != types.TypeString {
			vm.mainStrs[idx] = types.RetainValue(vm.Zone, v)
		}
	} else if vm.Context != nil {
		if vm.Context.strs == nil {
			vm.Context.strs = make(map[*bytecode.OpArray][]types.Value, 8)
		}
		cache := vm.Context.strs[fn]
		if int(idx) >= len(cache) {
			if cap(cache) >= len(fn.Strings) {
				cache = cache[:len(fn.Strings)]
			} else {
				next := make([]types.Value, len(fn.Strings))
				copy(next, cache)
				cache = next
			}
			vm.Context.strs[fn] = cache
		}
		if cache[idx].Type != types.TypeString {
			cache[idx] = types.RetainValue(vm.Zone, v)
		}
	}
	return v
}

func isSuperglobal(name string) bool {
	switch name {
	case "_SERVER", "_GET", "_POST", "_COOKIE", "_REQUEST", "_SESSION", "_ENV", "_FILES":
		return true
	default:
		return false
	}
}

func (vm *VM) superglobalRef(name string) types.Value {
	if ref, ok := vm.globals[name]; ok && ref.Type == types.TypeReference {
		return ref
	}
	initial := vm.globals[name]
	if initial.IsUndef() {
		arr, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("superglobale")
			return types.NewNull()
		}
		initial = arr
	} else {
		initial = vmRetain(vm.Zone, types.Deref(vm.Zone, initial))
	}
	r, err := types.NewReference(vm.Zone, initial)
	types.ReleaseValue(vm.Zone, &initial)
	if err != nil {
		vm.fatalFeature("superglobale")
		return types.NewNull()
	}
	if old, ok := vm.globals[name]; ok {
		vmRelease(vm.Zone, &old)
	}
	vm.globals[name] = r
	return r
}

func (vm *VM) findCVIndex(fn *bytecode.OpArray, name string) (uint32, bool) {
	for i, n := range fn.CVNames {
		if n == name {
			return uint32(i), true
		}
	}
	return 0, false
}

func (vm *VM) readVarByName(name string) (types.Value, bool) {
	fr := vm.frame()
	if name == "GLOBALS" {
		return vm.globalSnapshot(), true
	}
	if isSuperglobal(name) {
		ref := vm.superglobalRef(name)
		return vmRetain(vm.Zone, types.Deref(vm.Zone, ref)), true
	}
	if idx, ok := vm.findCVIndex(fr.fn, name); ok {
		slot := vm.slots[fr.cvBase+int(idx)]
		if !slot.IsUndef() {
			return vmRetain(vm.Zone, types.Deref(vm.Zone, slot)), true
		}
	}
	if fr.dynamicLocals != nil {
		if v, ok := fr.dynamicLocals[name]; ok && !v.IsUndef() {
			return vmRetain(vm.Zone, types.Deref(vm.Zone, v)), true
		}
	}
	if len(vm.frames) == 1 {
		if v, ok := vm.globals[name]; ok && !v.IsUndef() {
			return vmRetain(vm.Zone, types.Deref(vm.Zone, v)), true
		}
	}
	return types.NewNull(), false
}

func (vm *VM) resolveDynamicCVName(idx uint32) (string, bool) {
	fr := vm.frame()
	if int(idx) >= len(fr.fn.CVNames) {
		return "", false
	}
	raw := fr.fn.CVNames[idx]
	if !strings.HasPrefix(raw, "$") {
		return raw, false
	}
	inner := raw[1:]
	val, _ := vm.readVarByName(inner)
	s, owned := vm.stringify(val)
	name := types.StringData(vm.Zone, s)
	if owned {
		vmRelease(vm.Zone, &s)
	}
	vmRelease(vm.Zone, &val)
	return name, true
}

func (vm *VM) opLoadCV(in bytecode.Instr) {
	fr := vm.frame()
	if in.Flags&(bytecode.FlagUseThis|bytecode.FlagGlobal) == 0 && int(in.Op1) < len(fr.fn.CVNames) && strings.HasPrefix(fr.fn.CVNames[in.Op1], "$") {
		targetName, _ := vm.resolveDynamicCVName(in.Op1)
		if vm.stop || vm.control {
			return
		}
		fr = vm.frame()
		if targetIdx, ok := vm.findCVIndex(fr.fn, targetName); ok {
			in.Op1 = targetIdx
		} else {
			if in.Flags&bytecode.FlagLoadRef != 0 {
				if fr.dynamicLocals == nil {
					fr.dynamicLocals = make(map[string]types.Value)
				}
				cur := fr.dynamicLocals[targetName]
				if cur.Type != types.TypeReference {
					initVal := cur
					if initVal.IsUndef() {
						initVal = types.NewNull()
					}
					r, err := types.NewReference(vm.Zone, initVal)
					vmRelease(vm.Zone, &cur)
					if err != nil {
						vm.fatalFeature("référence")
						return
					}
					cur = r
					fr.dynamicLocals[targetName] = cur
				}
				vm.push(types.RetainReference(vm.Zone, cur))
				vm.bump()
				return
			}
			v, ok := vm.readVarByName(targetName)
			if !ok {
				vm.push(types.NewNull())
			} else {
				vm.push(v)
			}
			vm.bump()
			return
		}
	}
	if in.Flags == 0 {
		if fr.fn.HasGlobalsCV && in.Op1 == fr.fn.GlobalsCV || !fr.fn.CVMetaResolved && int(in.Op1) < len(fr.fn.CVNames) && fr.fn.CVNames[in.Op1] == "GLOBALS" {
			vm.push(vm.globalSnapshot())
			vm.bump()
			return
		}
		slot := vm.slots[fr.cvBase+int(in.Op1)]
		if slot.Type <= types.TypeDouble {
			if slot.Type == types.TypeUndef {
				if fr.dynamicLocals != nil && int(in.Op1) < len(fr.fn.CVNames) {
					if dv, ok := fr.dynamicLocals[fr.fn.CVNames[in.Op1]]; ok && !dv.IsUndef() {
						vm.push(vmRetain(vm.Zone, types.Deref(vm.Zone, dv)))
						vm.bump()
						return
					}
				}
				vm.push(types.NewNull())
			} else {
				vm.push(slot)
			}
			vm.bump()
			return
		}
		vm.push(vmRetain(vm.Zone, types.Deref(vm.Zone, slot)))
		vm.bump()
		return
	}
	if in.Flags&bytecode.FlagUseThis != 0 {
		if !fr.hasThis {
			vm.raiseBuiltin(bytecode.ClassError, "Using $this when not in object context", 0)
			return
		}
		vm.push(types.RetainValue(vm.Zone, fr.this))
		vm.bump()
		return
	}
	if in.Flags&bytecode.FlagGlobal != 0 {
		name := fr.fn.Strings[in.Op1]
		vm.push(vm.loadGlobal(name))
		vm.bump()
		return
	}
	if in.Flags&bytecode.FlagLoadRef != 0 {
		slot := vm.cvPtr(in.Op1)
		if slot.Type != types.TypeReference {
			old := *slot
			initVal := old
			if initVal.IsUndef() {
				initVal = types.NewNull()
			}
			r, err := types.NewReference(vm.Zone, initVal)
			if err != nil {
				vm.fatalFeature("référence")
				return
			}
			*slot = r
			types.ReleaseValue(vm.Zone, &old)
		}
		cur := *vm.cvPtr(in.Op1)
		vm.push(types.RetainReference(vm.Zone, cur))
		vm.bump()
		return
	}
	slot := vm.slots[fr.cvBase+int(in.Op1)]
	if slot.IsUndef() {
		vm.push(types.NewNull())
		vm.bump()
		return
	}
	vm.push(vmRetain(vm.Zone, types.Deref(vm.Zone, slot)))
	vm.bump()
}

func (vm *VM) loadGlobal(name string) types.Value {
	if isSuperglobal(name) {
		ref := vm.superglobalRef(name)
		return vmRetain(vm.Zone, types.Deref(vm.Zone, ref))
	}
	if v, ok := vm.globals[name]; ok {
		return vmRetain(vm.Zone, v)
	}
	arr, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("superglobale")
		return types.NewNull()
	}
	vm.globals[name] = arr
	return types.RetainValue(vm.Zone, arr)
}

func (vm *VM) bindGlobal(idx uint32) {
	name := vm.frame().fn.CVNames[idx]
	root := &vm.frames[0]
	var ref types.Value
	for i, n := range root.fn.CVNames {
		if n != name {
			continue
		}
		slot := &vm.slots[root.cvBase+i]
		if slot.Type != types.TypeReference {
			old := *slot
			v := old
			if v.IsUndef() {
				v = types.NewNull()
			}
			r, err := types.NewReference(vm.Zone, v)
			if err != nil {
				vm.fatalFeature("reference globale")
				return
			}
			*slot = r
			vmRelease(vm.Zone, &old)
		}
		ref = rOrSlot(&vm.slots[vm.frames[0].cvBase+i])
		break
	}
	if ref.Type != types.TypeReference {
		ref = vm.globals[name]
		if ref.Type != types.TypeReference {
			initial := ref
			if initial.IsUndef() {
				initial = types.NewNull()
			}
			r, err := types.NewReference(vm.Zone, initial)
			if err != nil {
				vm.fatalFeature("reference globale")
				return
			}
			vm.globals[name] = r
			vmRelease(vm.Zone, &ref)
			ref = r
		}
	}
	owned := types.RetainReference(vm.Zone, ref)
	slot := vm.cvPtr(idx)
	old := *slot
	*slot = owned
	vmRelease(vm.Zone, &old)
	vm.bump()
}

func rOrSlot(p *types.Value) types.Value { return *p }

func (vm *VM) lookupConst(name string) types.Value {
	v, ok := vm.findConst(name)
	if !ok {
		vm.raiseBuiltin(bytecode.ClassError, "Undefined constant \""+name+"\"", 0)
	}
	return v
}

func (vm *VM) findConst(name string) (types.Value, bool) {
	if vm.constCache != nil {
		if v, ok := vm.constCache[name]; ok {
			return types.RetainValue(vm.Zone, v), true
		}
	}
	switch name {
	case "PHP_INT_MAX":
		return types.NewLong(math.MaxInt64), true
	case "PHP_INT_MIN":
		return types.NewLong(math.MinInt64), true
	case "PHP_INT_SIZE":
		return types.NewLong(8), true
	case "PHP_EOL":
		v, err := types.NewStringLiteral(vm.Zone, "\n")
		if err != nil {
			vm.fatalFeature("constante PHP_EOL")
		}
		return v, true
	case "DIRECTORY_SEPARATOR":
		v, err := types.NewStringLiteral(vm.Zone, "/")
		if err != nil {
			vm.fatalFeature("constante DIRECTORY_SEPARATOR")
		}
		return v, true
	case "PATH_SEPARATOR":
		v, err := types.NewStringLiteral(vm.Zone, ":")
		if err != nil {
			vm.fatalFeature("constante PATH_SEPARATOR")
		}
		return v, true
	case "PHP_VERSION":
		v, err := types.NewStringLiteral(vm.Zone, "8.3.0")
		if err != nil {
			vm.fatalFeature("constante PHP_VERSION")
		}
		return v, true
	case "PHP_SAPI":
		v, err := types.NewStringLiteral(vm.Zone, "cli")
		if err != nil {
			vm.fatalFeature("constante PHP_SAPI")
		}
		return v, true
	case "PHP_MAJOR_VERSION":
		return types.NewLong(8), true
	case "PHP_MINOR_VERSION":
		return types.NewLong(3), true
	case "PHP_RELEASE_VERSION":
		return types.NewLong(0), true
	case "PHP_VERSION_ID":
		return types.NewLong(80300), true
	case "PASSWORD_DEFAULT", "PASSWORD_BCRYPT":
		v, err := types.NewStringLiteral(vm.Zone, "2y")
		if err != nil {
			vm.fatalFeature("constante PASSWORD_DEFAULT")
		}
		return v, true
	case "PASSWORD_ARGON2I":
		v, err := types.NewStringLiteral(vm.Zone, "argon2i")
		if err != nil {
			vm.fatalFeature("constante PASSWORD_ARGON2I")
		}
		return v, true
	case "PASSWORD_ARGON2ID":
		v, err := types.NewStringLiteral(vm.Zone, "argon2id")
		if err != nil {
			vm.fatalFeature("constante PASSWORD_ARGON2ID")
		}
		return v, true
	case "STR_PAD_LEFT", "SORT_REGULAR", "COUNT_NORMAL", "EXTR_OVERWRITE", "CASE_LOWER":
		return types.NewLong(0), true
	case "STR_PAD_RIGHT", "E_ERROR", "SORT_NUMERIC", "ARRAY_FILTER_USE_BOTH", "COUNT_RECURSIVE", "EXTR_SKIP", "CASE_UPPER":
		return types.NewLong(1), true
	case "STR_PAD_BOTH", "E_WARNING", "SORT_STRING", "ARRAY_FILTER_USE_KEY":
		return types.NewLong(2), true
	case "SORT_DESC":
		return types.NewLong(3), true
	case "E_PARSE", "SORT_ASC":
		return types.NewLong(4), true
	case "SORT_LOCALE_STRING":
		return types.NewLong(5), true
	case "SORT_NATURAL":
		return types.NewLong(6), true
	case "E_NOTICE", "SORT_FLAG_CASE":
		return types.NewLong(8), true
	case "E_CORE_ERROR":
		return types.NewLong(16), true
	case "E_CORE_WARNING":
		return types.NewLong(32), true
	case "E_COMPILE_ERROR":
		return types.NewLong(64), true
	case "E_COMPILE_WARNING":
		return types.NewLong(128), true
	case "E_USER_ERROR":
		return types.NewLong(256), true
	case "E_USER_WARNING":
		return types.NewLong(512), true
	case "E_USER_NOTICE":
		return types.NewLong(1024), true
	case "E_STRICT":
		return types.NewLong(2048), true
	case "E_RECOVERABLE_ERROR":
		return types.NewLong(4096), true
	case "E_DEPRECATED":
		return types.NewLong(8192), true
	case "E_USER_DEPRECATED":
		return types.NewLong(16384), true
	case "E_ALL":
		return types.NewLong(32767), true
	case "M_PI":
		return types.NewDouble(math.Pi), true
	case "INF":
		return types.NewDouble(math.Inf(1)), true
	case "NAN":
		return types.NewDouble(math.NaN()), true
	}
	if v, ok := vm.lookupExtConst(name); ok {
		return v, true
	}
	if vm.Program != nil {
		for _, c := range vm.Program.Constants {
			if c.Name != name {
				continue
			}
			var v types.Value
			var err error
			if c.IsString {
				v, err = types.NewStringLiteral(vm.Zone, c.Text)
			} else {
				v = types.RetainValue(vm.Zone, c.Value)
			}
			if err != nil {
				vm.fatalFeature("constante")
				return types.NewNull(), false
			}
			if vm.constCache == nil {
				vm.constCache = make(map[string]types.Value)
			}
			vm.constCache[name] = types.RetainValue(vm.Zone, v)
			return v, true
		}
	}
	return types.NewNull(), false
}

func (vm *VM) opStore(idx uint32, push bool, flags ...uint16) {
	v := vm.pop()
	if vm.stop {
		vmRelease(vm.Zone, &v)
		return
	}
	var fl uint16
	if len(flags) > 0 {
		fl = flags[0]
	}
	fr := vm.frame()
	if int(idx) < len(fr.fn.CVNames) && strings.HasPrefix(fr.fn.CVNames[idx], "$") {
		targetName, _ := vm.resolveDynamicCVName(idx)
		if vm.stop || vm.control {
			vmRelease(vm.Zone, &v)
			return
		}
		fr = vm.frame()
		if targetIdx, ok := vm.findCVIndex(fr.fn, targetName); ok {
			idx = targetIdx
		} else {
			if fr.dynamicLocals == nil {
				fr.dynamicLocals = make(map[string]types.Value)
			}
			owned := vmRetain(vm.Zone, types.Deref(vm.Zone, v))
			old := fr.dynamicLocals[targetName]
			fr.dynamicLocals[targetName] = owned
			if len(vm.frames) == 1 {
				oldG := vm.globals[targetName]
				vm.globals[targetName] = vmRetain(vm.Zone, owned)
				vmRelease(vm.Zone, &oldG)
			}
			vmRelease(vm.Zone, &v)
			vmRelease(vm.Zone, &old)
			if vm.stop || vm.control {
				return
			}
			if push {
				vm.push(vmRetain(vm.Zone, owned))
			}
			vm.bump()
			return
		}
	}
	if fl&bytecode.FlagLoadRef != 0 {
		if v.Type != types.TypeReference {
			r, err := types.NewReference(vm.Zone, v)
			vmRelease(vm.Zone, &v)
			if err != nil {
				vm.fatalFeature("référence")
				return
			}
			v = r
		}
		slot := vm.cvPtr(idx)
		old := *slot
		*slot = v
		vmRelease(vm.Zone, &old)
		if vm.stop || vm.control {
			return
		}
		if push {
			cur := vm.slots[vm.frame().cvBase+int(idx)]
			vm.push(vmRetain(vm.Zone, types.Deref(vm.Zone, cur)))
		}
		vm.bump()
		return
	}
	slot := &vm.slots[fr.cvBase+int(idx)]
	if v.Type <= types.TypeDouble && slot.Type <= types.TypeDouble {
		*slot = v
		if push {
			vm.push(v)
		}
		vm.bump()
		return
	}
	vm.writeCV(idx, v, true)
	if vm.stop || vm.control {
		return
	}
	if push {
		slot := vm.slots[vm.frame().cvBase+int(idx)]
		vm.push(vmRetain(vm.Zone, types.Deref(vm.Zone, slot)))
	}
	vm.bump()
}

func (vm *VM) writeCV(idx uint32, v types.Value, through bool) {
	slot := vm.cvPtr(idx)
	if through && slot.Type == types.TypeReference {
		types.AssignRef(vm.Zone, *slot, v)
		vmRelease(vm.Zone, &v)
		return
	}
	owned := vmRetain(vm.Zone, types.Deref(vm.Zone, v))
	old := *slot
	*slot = owned
	vmRelease(vm.Zone, &v)
	vmRelease(vm.Zone, &old)
}

func (vm *VM) opAssignRef(dest, src uint32) {
	if dest == src {
		slot := vm.cvPtr(dest)
		if slot.Type != types.TypeReference {
			old := *slot
			r, err := types.NewReference(vm.Zone, old)
			if err != nil {
				vm.fatalFeature("référence")
				return
			}
			*slot = r
			types.ReleaseValue(vm.Zone, &old)
		}
		if !vm.stop && !vm.control {
			vm.bump()
		}
		return
	}
	source := vm.cvPtr(src)
	if source.Type != types.TypeReference {
		old := *source
		r, err := types.NewReference(vm.Zone, old)
		if err != nil {
			vm.fatalFeature("référence")
			return
		}
		*source = r
		types.ReleaseValue(vm.Zone, &old)
	}
	ref := types.RetainReference(vm.Zone, *vm.cvPtr(src))
	d := vm.cvPtr(dest)
	oldDest := *d
	*d = ref
	types.ReleaseValue(vm.Zone, &oldDest)
	if !vm.stop && !vm.control {
		vm.bump()
	}
}

func (vm *VM) issetCV(idx uint32) bool {
	fr := vm.frame()
	if int(idx) < len(fr.fn.CVNames) && strings.HasPrefix(fr.fn.CVNames[idx], "$") {
		targetName, _ := vm.resolveDynamicCVName(idx)
		if vm.stop || vm.control {
			return false
		}
		fr = vm.frame()
		if targetIdx, ok := vm.findCVIndex(fr.fn, targetName); ok {
			idx = targetIdx
		} else {
			v, ok := vm.readVarByName(targetName)
			if !ok {
				return false
			}
			notNull := !v.IsNull() && !v.IsUndef()
			vmRelease(vm.Zone, &v)
			return notNull
		}
	}
	if fr.fn.HasGlobalsCV && idx == fr.fn.GlobalsCV || (!fr.fn.CVMetaResolved && int(idx) < len(fr.fn.CVNames) && fr.fn.CVNames[idx] == "GLOBALS") {
		return true
	}
	slot := vm.slots[fr.cvBase+int(idx)]
	if slot.IsUndef() {
		if fr.dynamicLocals != nil && int(idx) < len(fr.fn.CVNames) {
			if dv, ok := fr.dynamicLocals[fr.fn.CVNames[idx]]; ok && !dv.IsUndef() {
				return !types.Deref(vm.Zone, dv).IsNull()
			}
		}
		return false
	}
	return !types.Deref(vm.Zone, slot).IsNull()
}

func (vm *VM) opMakeRef() {
	v := vm.pop()
	if v.Type == types.TypeReference {
		vm.push(v)
		vm.bump()
		return
	}
	r, err := types.NewReference(vm.Zone, v)
	types.ReleaseValue(vm.Zone, &v)
	if err != nil {
		vm.fatalFeature("référence")
		return
	}
	vm.push(r)
	vm.bump()
}

func (vm *VM) echo(v types.Value) {
	if vm.stop {
		return
	}
	v = types.Deref(vm.Zone, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull, types.TypeFalse:
		return
	case types.TypeTrue:
		_, _ = vm.Out.Write([]byte{'1'})
	case types.TypeLong:
		_, _ = vm.Out.Write([]byte(strconv.FormatInt(int64(v.Scalar), 10)))
	case types.TypeDouble:
		_, _ = vm.Out.Write([]byte(formatFloat(v.ToDouble())))
	case types.TypeString:
		_, _ = vm.Out.Write([]byte(types.StringData(vm.Zone, v)))
	case types.TypeArray:
		_, _ = vm.Out.Write([]byte("Array"))
	case types.TypeObject:
		s, own := vm.stringify(v)
		if !vm.stop && !vm.control && s.Type == types.TypeString {
			_, _ = vm.Out.Write([]byte(types.StringData(vm.Zone, s)))
		}
		if own {
			types.ReleaseValue(vm.Zone, &s)
		}
	default:
		vm.fatalFeature("echo")
	}
}

func formatFloat(f float64) string {
	if math.IsNaN(f) {
		return "NAN"
	}
	if math.IsInf(f, 1) {
		return "INF"
	}
	if math.IsInf(f, -1) {
		return "-INF"
	}
	return strconv.FormatFloat(f, 'G', 14, 64)
}

func (vm *VM) num(v types.Value) types.Value {
	taint := v.Flags & types.FlagTainted
	v = types.Deref(vm.Zone, v)
	taint |= v.Flags & types.FlagTainted
	var out types.Value
	switch v.Type {
	case types.TypeLong, types.TypeDouble:
		out = v
	case types.TypeTrue:
		out = types.NewLong(1)
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		out = types.NewLong(0)
	case types.TypeString:
		out = parseNum(types.StringData(vm.Zone, v))
	default:
		vm.raiseBuiltin(bytecode.ClassTypeError, "Unsupported operand types", 0)
		return types.NewLong(0)
	}
	out.Flags |= taint
	return out
}

func parseNum(text string) types.Value {
	text = strings.TrimLeft(text, " \t\n\r\v\f")
	if text == "" {
		return types.NewLong(0)
	}
	i := 0
	if text[0] == '+' || text[0] == '-' {
		i++
	}
	if i >= len(text) {
		return types.NewLong(0)
	}
	dot := false
	exp := false
	digits := 0
	end := len(text)
	for j := i; j < len(text); j++ {
		c := text[j]
		if c >= '0' && c <= '9' {
			digits++
			continue
		}
		if c == '.' && !dot && !exp {
			dot = true
			continue
		}
		if (c == 'e' || c == 'E') && !exp && digits > 0 {
			k := j + 1
			if k < len(text) && (text[k] == '+' || text[k] == '-') {
				k++
			}
			if k < len(text) && text[k] >= '0' && text[k] <= '9' {
				exp = true
				j = k
				continue
			}
		}
		end = j
		break
	}
	text = text[:end]
	if digits == 0 || text == "+" || text == "-" || text == "." || text == "+." || text == "-." {
		return types.NewLong(0)
	}
	if !dot && !exp {
		n, err := strconv.ParseInt(text, 10, 64)
		if err == nil {
			return types.NewLong(n)
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return types.NewLong(0)
	}
	return types.NewDouble(f)
}

func (vm *VM) toLong(v types.Value) int64 {
	n := vm.num(v)
	if n.Type == types.TypeDouble {
		return n.ToLong()
	}
	if n.Type == types.TypeLong {
		return int64(n.Scalar)
	}
	return 0
}

func (vm *VM) toIntWithDeprecation(v types.Value) int64 {
	v = types.Deref(vm.Zone, v)
	if v.Type == types.TypeDouble {
		f := v.ToDouble()
		if !math.IsNaN(f) && !math.IsInf(f, 0) && math.Trunc(f) != f {
			if vm.errorReporting&8192 != 0 && vm.Out != nil {
				file, line := vm.currentLoc()
				_, _ = fmt.Fprintf(vm.Out, "\nDeprecated: Implicit conversion from float %s to int loses precision in %s on line %d\n", strconv.FormatFloat(f, 'g', -1, 64), file, line)
			}
		}
		return types.FloatToInt(f)
	}
	return vm.toLong(v)
}

func (vm *VM) stringify(v types.Value) (types.Value, bool) {
	taint := v.Flags & types.FlagTainted
	v = types.Deref(vm.Zone, v)
	taint |= v.Flags & types.FlagTainted
	switch v.Type {
	case types.TypeString:
		v.Flags |= taint
		return v, false
	case types.TypeUndef, types.TypeNull, types.TypeFalse:
		s, err := types.NewStringLiteral(vm.Zone, "")
		if err != nil {
			vm.fatalFeature("chaîne")
			return types.NewNull(), false
		}
		s.Flags |= taint
		return s, true
	case types.TypeTrue:
		s, err := types.NewStringLiteral(vm.Zone, "1")
		if err != nil {
			vm.fatalFeature("chaîne")
			return types.NewNull(), false
		}
		s.Flags |= taint
		return s, true
	case types.TypeLong:
		s, err := types.FormatLong(vm.Zone, int64(v.Scalar))
		if err != nil {
			vm.fatalFeature("chaîne")
			return types.NewNull(), false
		}
		s.Flags |= taint
		return s, true
	case types.TypeDouble:
		s, err := types.NewStringLiteral(vm.Zone, formatFloat(v.ToDouble()))
		if err != nil {
			vm.fatalFeature("chaîne")
			return types.NewNull(), false
		}
		s.Flags |= taint
		return s, true
	case types.TypeObject:
		cid := types.ObjectInfo(vm.Zone, v).ClassID
		if m, _, ok := vm.findMethod(cid, "__toString"); ok {
			savedControl := vm.control
			ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, v), true, nil, cid, m.ClassID, vm.line)
			if threw || vm.stop {
				return types.NewNull(), false
			}
			vm.control = savedControl
			retD := types.Deref(vm.Zone, ret)
			if retD.Type != types.TypeString {
				types.ReleaseValue(vm.Zone, &ret)
				vm.raiseBuiltin(bytecode.ClassTypeError, vm.className(cid)+"::__toString(): Return value must be of type string", 0)
				return types.NewNull(), false
			}
			retD.Flags |= taint
			if ret.Type == types.TypeReference {
				s := types.RetainValue(vm.Zone, retD)
				types.ReleaseValue(vm.Zone, &ret)
				return s, true
			}
			return retD, true
		}
		vm.raiseBuiltin(bytecode.ClassError, "Object of class "+vm.className(cid)+" could not be converted to string", 0)
		return types.NewNull(), false
	default:
		vm.raiseBuiltin(bytecode.ClassError, "conversion en chaîne impossible", 0)
		return types.NewNull(), false
	}
}

func (vm *VM) opBinary(op bytecode.Opcode) {
	r := vm.pop()
	l := vm.pop()
	if vm.stop {
		vmRelease(vm.Zone, &r)
		vmRelease(vm.Zone, &l)
		return
	}
	if (l.Type == types.TypeLong || l.Type == types.TypeDouble) && (r.Type == types.TypeLong || r.Type == types.TypeDouble) {
		switch op {
		case bytecode.OpAdd:
			vm.push(types.Add(l, r))
			vm.bump()
			return
		case bytecode.OpSub:
			vm.push(types.Sub(l, r))
			vm.bump()
			return
		case bytecode.OpMul:
			vm.push(types.Mul(l, r))
			vm.bump()
			return
		}
		if l.Type == types.TypeLong && r.Type == types.TypeLong {
			switch op {
			case bytecode.OpBWOr:
				vm.push(types.NewLong(int64(l.Scalar) | int64(r.Scalar)))
				vm.bump()
				return
			case bytecode.OpBWAnd:
				vm.push(types.NewLong(int64(l.Scalar) & int64(r.Scalar)))
				vm.bump()
				return
			case bytecode.OpBWXor:
				vm.push(types.NewLong(int64(l.Scalar) ^ int64(r.Scalar)))
				vm.bump()
				return
			case bytecode.OpSL:
				if n := int64(r.Scalar); n >= 0 && n < 64 {
					vm.push(types.NewLong(int64(l.Scalar << (r.Scalar & 63))))
					vm.bump()
					return
				}
			case bytecode.OpSR:
				if n := int64(r.Scalar); n >= 0 && n < 64 {
					vm.push(types.NewLong(int64(l.Scalar) >> (r.Scalar & 63)))
					vm.bump()
					return
				}
			}
		}
	}
	switch op {
	case bytecode.OpAdd:
		vm.push(types.Add(vm.num(l), vm.num(r)))
	case bytecode.OpSub:
		vm.push(types.Sub(vm.num(l), vm.num(r)))
	case bytecode.OpMul:
		vm.push(types.Mul(vm.num(l), vm.num(r)))
	case bytecode.OpDiv:
		res, err := types.Div(vm.num(l), vm.num(r))
		if err != nil {
			vmRelease(vm.Zone, &r)
			vmRelease(vm.Zone, &l)
			vm.raiseBuiltin(bytecode.ClassDivisionByZeroError, "Division by zero", 0)
			return
		}
		vm.push(res)
	case bytecode.OpMod:
		vm.opMod(l, r)
		if vm.stop {
			vmRelease(vm.Zone, &r)
			vmRelease(vm.Zone, &l)
			return
		}
	case bytecode.OpPow:
		vm.push(vm.pow(l, r))
	case bytecode.OpSL:
		vm.shift(l, r, true)
		if vm.stop {
			vmRelease(vm.Zone, &r)
			vmRelease(vm.Zone, &l)
			return
		}
	case bytecode.OpSR:
		vm.shift(l, r, false)
		if vm.stop {
			vmRelease(vm.Zone, &r)
			vmRelease(vm.Zone, &l)
			return
		}
	case bytecode.OpConcat:
		vm.opConcat(l, r)
		if vm.stop {
			vmRelease(vm.Zone, &r)
			vmRelease(vm.Zone, &l)
			return
		}
	case bytecode.OpBWOr:
		li := vm.toIntWithDeprecation(l)
		ri := vm.toIntWithDeprecation(r)
		vm.push(types.NewLong(li | ri))
	case bytecode.OpBWAnd:
		li := vm.toIntWithDeprecation(l)
		ri := vm.toIntWithDeprecation(r)
		vm.push(types.NewLong(li & ri))
	case bytecode.OpBWXor:
		li := vm.toIntWithDeprecation(l)
		ri := vm.toIntWithDeprecation(r)
		vm.push(types.NewLong(li ^ ri))
	case bytecode.OpBoolXor:
		vm.push(types.NewBool(types.ToBoolZ(vm.Zone, l) != types.ToBoolZ(vm.Zone, r)))
	}
	vmRelease(vm.Zone, &r)
	vmRelease(vm.Zone, &l)
	if !vm.stop {
		vm.bump()
	}
}

func (vm *VM) opMod(l, r types.Value) {
	a := vm.toIntWithDeprecation(l)
	b := vm.toIntWithDeprecation(r)
	if b == 0 {
		vm.raiseBuiltin(bytecode.ClassDivisionByZeroError, "Modulo by zero", 0)
		return
	}
	if a == math.MinInt64 && b == -1 {
		vm.push(types.NewLong(0))
		return
	}
	vm.push(types.NewLong(a % b))
}

func (vm *VM) pow(l, r types.Value) types.Value {
	ln, rn := vm.num(l), vm.num(r)
	if ln.Type == types.TypeLong && rn.Type == types.TypeLong {
		exp := int64(rn.Scalar)
		base := int64(ln.Scalar)
		if exp == 0 {
			return types.NewLong(1)
		}
		if exp > 0 {
			if base == 1 {
				return types.NewLong(1)
			}
			if base == -1 {
				if exp%2 == 0 {
					return types.NewLong(1)
				}
				return types.NewLong(-1)
			}
			if base == 0 {
				return types.NewLong(0)
			}
			if base == -2 && exp == 63 {
				return types.NewLong(math.MinInt64)
			}
		}
		if exp >= 0 && exp < 63 {
			acc := int64(1)
			ok := true
			for range exp {
				if base != 0 && (acc > math.MaxInt64/base && base > 0 || base < 0 && acc < math.MinInt64/base) {
					ok = false
					break
				}
				acc *= base
			}
			if ok {
				return types.NewLong(acc)
			}
		}
	}
	return types.NewDouble(math.Pow(ln.ToDouble(), rn.ToDouble()))
}

func (vm *VM) shift(l, r types.Value, left bool) {
	a := vm.toIntWithDeprecation(l)
	n := vm.toIntWithDeprecation(r)
	if n < 0 {
		vm.raiseBuiltin(bytecode.ClassArithmeticError, "Bit shift by negative number", 0)
		return
	}
	if n >= 64 {
		if !left && a < 0 {
			vm.push(types.NewLong(-1))
		} else {
			vm.push(types.NewLong(0))
		}
		return
	}
	if left {
		vm.push(types.NewLong(int64(uint64(a) << uint(n))))
		return
	}
	vm.push(types.NewLong(a >> n))
}

func (vm *VM) opConcat(l, r types.Value) {
	if l.Type == types.TypeString && r.Type == types.TypeLong {
		out, err := types.ConcatStringLong(vm.Zone, l, int64(r.Scalar))
		if err != nil {
			vm.fatalFeature("concaténation")
			return
		}
		vm.push(out)
		return
	}
	ls, lown := vm.stringify(l)
	rs, rown := vm.stringify(r)
	if vm.stop {
		if lown {
			types.ReleaseValue(vm.Zone, &ls)
		}
		if rown {
			types.ReleaseValue(vm.Zone, &rs)
		}
		return
	}
	out, err := types.Concat(vm.Zone, ls, rs)
	if lown {
		types.ReleaseValue(vm.Zone, &ls)
	}
	if rown {
		types.ReleaseValue(vm.Zone, &rs)
	}
	if err != nil {
		vm.fatalFeature("concaténation")
		return
	}
	vm.push(out)
}

func (vm *VM) opUnary(op bytecode.Opcode, cast uint32) {
	v := vm.pop()
	if vm.stop {
		return
	}
	switch op {
	case bytecode.OpBWNot:
		vm.push(types.NewLong(^vm.toIntWithDeprecation(v)))
	case bytecode.OpBoolNot:
		vm.push(types.NewBool(!types.ToBoolZ(vm.Zone, v)))
	case bytecode.OpUnaryPlus:
		vm.push(vm.num(v))
	case bytecode.OpUnaryMinus:
		n := vm.num(v)
		vm.push(types.Sub(types.NewLong(0), n))
	case bytecode.OpCast:
		vm.cast(v, cast)
	}
	vmRelease(vm.Zone, &v)
	if !vm.stop {
		vm.bump()
	}
}

func (vm *VM) cast(v types.Value, kind uint32) {
	switch kind {
	case bytecode.CastInt:
		vm.push(types.NewLong(vm.toLong(v)))
	case bytecode.CastFloat:
		vm.push(types.NewDouble(vm.num(v).ToDouble()))
	case bytecode.CastBool:
		vm.push(types.NewBool(types.ToBoolZ(vm.Zone, v)))
	case bytecode.CastString:
		s, _ := vm.stringify(v)
		if !vm.stop {
			vm.push(types.RetainValue(vm.Zone, s))
			types.ReleaseValue(vm.Zone, &s)
		}
	case bytecode.CastUnset:
		vm.push(types.NewNull())
	case bytecode.CastArray:
		d := types.Deref(vm.Zone, v)
		if d.Type == types.TypeArray {
			vm.push(types.RetainValue(vm.Zone, d))
			return
		}
		arr, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("tableau")
			return
		}
		if d.Type == types.TypeNull || d.Type == types.TypeUndef {
			vm.push(arr)
			return
		}
		if err := types.ArrayAppend(vm.Zone, &arr, d); err != nil {
			vm.fatalFeature("tableau")
			return
		}
		vm.push(arr)
	case bytecode.CastObject:
		d := types.Deref(vm.Zone, v)
		if d.Type == types.TypeObject {
			vm.push(types.RetainValue(vm.Zone, d))
			return
		}
		obj, err := types.NewObject(vm.Zone, bytecode.ClassStdClass, 0, false)
		if err != nil {
			vm.fatalFeature("conversion en objet")
			return
		}
		if d.Type == types.TypeNull || d.Type == types.TypeUndef {
			vm.push(obj)
			return
		}
		if d.Type == types.TypeArray {
			types.ArrayForEach(vm.Zone, d, func(k, val types.Value) bool {
				ks, own := vm.stringify(k)
				keyStr := types.StringData(vm.Zone, ks)
				if own {
					types.ReleaseValue(vm.Zone, &ks)
				}
				_ = vm.dynamicPropSet(obj, keyStr, types.Deref(vm.Zone, val))
				return true
			})
			vm.push(obj)
			return
		}
		_ = vm.dynamicPropSet(obj, "scalar", d)
		vm.push(obj)
	default:
		vm.fatalFeature("conversion")
	}
}

func (vm *VM) opCmp(op bytecode.Opcode) {
	r := vm.pop()
	l := vm.pop()
	if vm.stop {
		return
	}
	if l.Type == types.TypeLong && r.Type == types.TypeLong {
		switch op {
		case bytecode.OpIsSmaller:
			vm.push(types.NewBool(int64(l.Scalar) < int64(r.Scalar)))
		case bytecode.OpIsSmallerOrEqual:
			vm.push(types.NewBool(int64(l.Scalar) <= int64(r.Scalar)))
		case bytecode.OpIsEqual, bytecode.OpIsIdentical:
			vm.push(types.NewBool(l.Scalar == r.Scalar))
		case bytecode.OpIsNotEqual, bytecode.OpIsNotIdentical:
			vm.push(types.NewBool(l.Scalar != r.Scalar))
		case bytecode.OpSpaceship:
			a, b := int64(l.Scalar), int64(r.Scalar)
			res := int64(0)
			if a < b {
				res = -1
			} else if a > b {
				res = 1
			}
			vm.push(types.NewLong(res))
		}
		vm.bump()
		return
	}
	if (l.Type == types.TypeLong || l.Type == types.TypeDouble) && (r.Type == types.TypeLong || r.Type == types.TypeDouble) {
		lf, rf := l.ToDouble(), r.ToDouble()
		switch op {
		case bytecode.OpIsSmaller:
			vm.push(types.NewBool(lf < rf))
			vm.bump()
			return
		case bytecode.OpIsSmallerOrEqual:
			vm.push(types.NewBool(lf <= rf))
			vm.bump()
			return
		case bytecode.OpIsEqual:
			vm.push(types.NewBool(lf == rf))
			vm.bump()
			return
		case bytecode.OpIsNotEqual:
			vm.push(types.NewBool(lf != rf))
			vm.bump()
			return
		}
	}
	switch op {
	case bytecode.OpIsIdentical:
		vm.push(types.NewBool(types.IdenticalZ(vm.Zone, l, r)))
	case bytecode.OpIsNotIdentical:
		vm.push(types.NewBool(!types.IdenticalZ(vm.Zone, l, r)))
	case bytecode.OpIsEqual:
		vm.push(types.NewBool(types.EqualZ(vm.Zone, l, r)))
	case bytecode.OpIsNotEqual:
		vm.push(types.NewBool(!types.EqualZ(vm.Zone, l, r)))
	case bytecode.OpIsSmaller:
		vm.push(types.NewBool(vm.cmp(l, r) < 0))
	case bytecode.OpIsSmallerOrEqual:
		vm.push(types.NewBool(vm.cmp(l, r) <= 0))
	case bytecode.OpSpaceship:
		vm.push(types.NewLong(int64(vm.cmp(l, r))))
	}
	vmRelease(vm.Zone, &r)
	vmRelease(vm.Zone, &l)
	if !vm.stop {
		vm.bump()
	}
}

func (vm *VM) cmp(a, b types.Value) int {
	a = types.Deref(vm.Zone, a)
	b = types.Deref(vm.Zone, b)
	if a.Type == types.TypeString && b.Type == types.TypeString {
		return types.CompareStringsPHP(vm.Zone, a, b)
	}
	if (a.Type == types.TypeNull || a.Type == types.TypeUndef) && b.Type == types.TypeString {
		if len(types.StringData(vm.Zone, b)) == 0 {
			return 0
		}
		return -1
	}
	if a.Type == types.TypeString && (b.Type == types.TypeNull || b.Type == types.TypeUndef) {
		if len(types.StringData(vm.Zone, a)) == 0 {
			return 0
		}
		return 1
	}
	if a.Type <= types.TypeTrue || b.Type <= types.TypeTrue {
		x, y := types.ToBoolZ(vm.Zone, a), types.ToBoolZ(vm.Zone, b)
		if x == y {
			return 0
		}
		if !x {
			return -1
		}
		return 1
	}
	if a.Type <= types.TypeDouble && b.Type <= types.TypeDouble {
		return types.CompareScalaires(a, b)
	}
	if types.EqualZ(vm.Zone, a, b) {
		return 0
	}
	if a.Type == types.TypeString && (b.Type == types.TypeLong || b.Type == types.TypeDouble) {
		if isNumericStringStrict(types.StringData(vm.Zone, a)) {
			return types.CompareScalaires(vm.num(a), b)
		}
		bs, own := vm.stringify(b)
		res := types.CompareStringsPHP(vm.Zone, a, bs)
		if own {
			types.ReleaseValue(vm.Zone, &bs)
		}
		return res
	}
	if (a.Type == types.TypeLong || a.Type == types.TypeDouble) && b.Type == types.TypeString {
		if isNumericStringStrict(types.StringData(vm.Zone, b)) {
			return types.CompareScalaires(a, vm.num(b))
		}
		as, own := vm.stringify(a)
		res := types.CompareStringsPHP(vm.Zone, as, b)
		if own {
			types.ReleaseValue(vm.Zone, &as)
		}
		return res
	}
	an, bn := vm.num(a), vm.num(b)
	if an.Type <= types.TypeDouble && bn.Type <= types.TypeDouble {
		return types.CompareScalaires(an, bn)
	}
	return 1
}

func isNumericStringStrict(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	if i >= len(s) {
		return false
	}
	dot := false
	exp := false
	digits := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			digits++
			continue
		}
		if c == '.' && !dot && !exp {
			dot = true
			continue
		}
		if (c == 'e' || c == 'E') && !exp && digits > 0 {
			i++
			if i < len(s) && (s[i] == '+' || s[i] == '-') {
				i++
			}
			if i >= len(s) || s[i] < '0' || s[i] > '9' {
				return false
			}
			exp = true
			continue
		}
		return false
	}
	return digits > 0
}

func incrementAlphaString(s string) string {
	if s == "" {
		return "1"
	}
	b := []byte(s)
	carry := true
	var prefix byte
	for i := len(b) - 1; i >= 0 && carry; i-- {
		c := b[i]
		if (c >= 'a' && c <= 'y') || (c >= 'A' && c <= 'Y') || (c >= '0' && c <= '8') {
			b[i] = c + 1
			carry = false
		} else if c == 'z' {
			b[i] = 'a'
			prefix = 'a'
		} else if c == 'Z' {
			b[i] = 'A'
			prefix = 'A'
		} else if c == '9' {
			b[i] = '0'
			prefix = '1'
		} else {
			carry = false
		}
	}
	if carry && prefix != 0 {
		out := make([]byte, len(b)+1)
		out[0] = prefix
		copy(out[1:], b)
		return string(out)
	}
	return string(b)
}

func (vm *VM) stepIncDec(cur types.Value, isInc bool) types.Value {
	cur = types.Deref(vm.Zone, cur)
	switch cur.Type {
	case types.TypeNull, types.TypeUndef:
		if isInc {
			return types.NewLong(1)
		}
		if vm.errorReporting&2 != 0 && vm.Out != nil {
			file, line := vm.currentLoc()
			_, _ = fmt.Fprintf(vm.Out, "\nWarning: Decrement on type null has no effect, this will change in the next major version of PHP in %s on line %d\n", file, line)
		}
		return types.NewNull()
	case types.TypeTrue, types.TypeFalse:
		return cur
	case types.TypeString:
		s := types.StringData(vm.Zone, cur)
		if isNumericStringStrict(s) {
			n := parseNum(s)
			if isInc {
				return types.Add(n, types.NewLong(1))
			}
			return types.Sub(n, types.NewLong(1))
		}
		if isInc {
			sv, err := types.NewStringLiteral(vm.Zone, incrementAlphaString(s))
			if err != nil {
				return types.NewNull()
			}
			return sv
		}
		return vmRetain(vm.Zone, cur)
	default:
		if isInc {
			return types.Add(vm.num(cur), types.NewLong(1))
		}
		return types.Sub(vm.num(cur), types.NewLong(1))
	}
}

func (vm *VM) opInc(op bytecode.Opcode, idx uint32, flags uint16) {
	discard := flags&bytecode.FlagDiscard != 0
	fr := vm.frame()
	slotPtr := &vm.slots[fr.cvBase+int(idx)]
	if slotPtr.Type == types.TypeLong {
		val := int64(slotPtr.Scalar)
		if val != math.MaxInt64 && val != math.MinInt64 {
			switch op {
			case bytecode.OpPreIncCV:
				slotPtr.Scalar = uint64(val + 1)
				if !discard {
					vm.push(*slotPtr)
				}
			case bytecode.OpPostIncCV:
				old := *slotPtr
				slotPtr.Scalar = uint64(val + 1)
				if !discard {
					vm.push(old)
				}
			case bytecode.OpPreDecCV:
				slotPtr.Scalar = uint64(val - 1)
				if !discard {
					vm.push(*slotPtr)
				}
			default:
				old := *slotPtr
				slotPtr.Scalar = uint64(val - 1)
				if !discard {
					vm.push(old)
				}
			}
			vm.bump()
			return
		}
	}
	slot := *slotPtr
	cur := types.Deref(vm.Zone, slot)
	isInc := op == bytecode.OpPreIncCV || op == bytecode.OpPostIncCV
	if discard {
		next := vm.stepIncDec(cur, isInc)
		vm.writeCV(idx, next, true)
		vm.bump()
		return
	}
	old := vmRetain(vm.Zone, cur)
	if old.IsUndef() {
		old = types.NewNull()
	}
	next := vm.stepIncDec(cur, isInc)
	vm.writeCV(idx, next, true)
	switch op {
	case bytecode.OpPreIncCV, bytecode.OpPreDecCV:
		vm.push(vmRetain(vm.Zone, types.Deref(vm.Zone, vm.slots[vm.frame().cvBase+int(idx)])))
		vmRelease(vm.Zone, &old)
	default:
		vm.push(old)
	}
	vm.bump()
}
