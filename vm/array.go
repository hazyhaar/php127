package vm

import (
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func (vm *VM) validStringOffsetForIsset(kd types.Value) (int64, bool) {
	if kd.Type == types.TypeLong {
		return kd.ToLong(), true
	}
	if kd.Type == types.TypeString {
		s := strings.TrimSpace(types.StringData(vm.Zone, kd))
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func (vm *VM) currentLoc() (string, uint32) {
	return vm.file(), vm.line
}

func (vm *VM) typeNameForDiag(v types.Value) string {
	return scalarOffsetTypeName(v.Type)
}

func (vm *VM) deprecatedFloatKey(w types.ArrayWarning) error {
	vm.raiseDiag(eDeprecated, diagDeprecate, "Implicit conversion from float "+strconv.FormatFloat(w.Float, 'g', -1, 64)+" to int loses precision", vm.line)
	return nil
}

func scalarOffsetTypeName(t types.Type) string {
	switch t {
	case types.TypeFalse:
		return "false"
	case types.TypeTrue:
		return "true"
	case types.TypeLong:
		return "int"
	case types.TypeDouble:
		return "float"
	case types.TypeString:
		return "string"
	case types.TypeArray:
		return "array"
	default:
		return "null"
	}
}

func (vm *VM) opInitArray() {
	arr, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("tableau")
		return
	}
	vm.push(arr)
	vm.bump()
}

func (vm *VM) opAddElem(keyed bool, in bytecode.Instr) {
	if !keyed && in.Flags&bytecode.FlagAssignDimCV != 0 {
		val := vm.pop()
		if vm.stop {
			vmRelease(vm.Zone, &val)
			return
		}
		fr := vm.frame()
		slot := &vm.slots[fr.cvBase+int(in.Op1)]
		d := types.Deref(vm.Zone, *slot)
		if d.Type == types.TypeUndef || d.Type == types.TypeNull || d.Type == types.TypeFalse {
			newArr, err := types.NewArray(vm.Zone)
			if err != nil {
				vmRelease(vm.Zone, &val)
				vm.fatalFeature("tableau")
				return
			}
			if slot.Type == types.TypeReference {
				types.AssignRef(vm.Zone, *slot, newArr)
				types.ReleaseValue(vm.Zone, &newArr)
			} else {
				vmRelease(vm.Zone, slot)
				*slot = newArr
			}
			slot = &vm.slots[vm.frame().cvBase+int(in.Op1)]
			d = types.Deref(vm.Zone, *slot)
		}
		if d.Type == types.TypeObject {
			ok := vm.objectOffsetSet(d, types.NewNull(), val)
			if !ok || vm.stop || vm.control {
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
		if d.Type == types.TypeString {
			vmRelease(vm.Zone, &val)
			vm.raiseBuiltin(bytecode.ClassError, "[] operator not supported for strings", 0)
			return
		}
		if d.Type != types.TypeArray {
			vmRelease(vm.Zone, &val)
			vm.raiseBuiltin(bytecode.ClassError, "Cannot use a scalar value as an array", 0)
			return
		}
		err := types.ArrayAppend(vm.Zone, slot, val)
		if err != nil {
			vmRelease(vm.Zone, &val)
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
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
	if in.Flags&bytecode.FlagUnpack != 0 {
		vm.opUnpackIntoArray()
		return
	}
	val := vm.pop()
	var key types.Value
	if keyed {
		key = vm.pop()
	}
	arr := vm.pop()
	if vm.stop {
		return
	}
	d := types.Deref(vm.Zone, arr)
	if d.Type == types.TypeUndef || d.Type == types.TypeNull || d.Type == types.TypeFalse {
		newArr, err := types.NewArray(vm.Zone)
		if err != nil {
			if keyed {
				vmRelease(vm.Zone, &key)
			}
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &arr)
			vm.fatalFeature("tableau")
			return
		}
		vmRelease(vm.Zone, &arr)
		arr = newArr
		d = arr
	}
	if d.Type == types.TypeObject {
		k := key
		if !keyed {
			k = types.NewNull()
		}
		ok := vm.objectOffsetSet(d, k, val)
		if keyed {
			vmRelease(vm.Zone, &key)
		}
		vmRelease(vm.Zone, &val)
		if !ok || vm.stop || vm.control {
			vmRelease(vm.Zone, &arr)
			return
		}
		vm.push(arr)
		vm.bump()
		return
	}
	if d.Type == types.TypeString {
		if keyed {
			vmRelease(vm.Zone, &key)
		}
		vmRelease(vm.Zone, &val)
		vmRelease(vm.Zone, &arr)
		vm.raiseBuiltin(bytecode.ClassError, "[] operator not supported for strings", 0)
		return
	}
	if d.Type != types.TypeArray {
		if keyed {
			vmRelease(vm.Zone, &key)
		}
		vmRelease(vm.Zone, &val)
		vmRelease(vm.Zone, &arr)
		vm.raiseBuiltin(bytecode.ClassError, "Cannot use a scalar value as an array", 0)
		return
	}
	var err error
	if keyed {
		err = types.ArraySet(vm.Zone, &arr, key, val, vm.deprecatedFloatKey)
		vmRelease(vm.Zone, &key)
	} else {
		err = types.ArrayAppend(vm.Zone, &arr, val)
	}
	vmRelease(vm.Zone, &val)
	if err != nil {
		vmRelease(vm.Zone, &arr)
		vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
		return
	}
	if vm.stop || vm.control {
		vmRelease(vm.Zone, &arr)
		return
	}
	vm.push(arr)
	vm.bump()
}

func (vm *VM) opFetchDim(op bytecode.Opcode, flags uint16) {
	if flags&bytecode.FlagFuncArg != 0 && vm.funcArgByRef() {
		op = bytecode.OpFetchDimW
	}
	key := vm.pop()
	arr := vm.pop()
	if vm.stop {
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		return
	}
	if op == bytecode.OpFetchDimR && arr.Type == types.TypeArray {
		val, ok, err := types.ArrayGet(vm.Zone, arr, key, vm.deprecatedFloatKey)
		if err != nil {
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
		if !ok || val.IsUndef() {
			if flags&bytecode.FlagDimWrite == 0 && vm.warnUndefinedKey(key) {
				vmRelease(vm.Zone, &key)
				vmRelease(vm.Zone, &arr)
				return
			}
			vm.push(types.NewNull())
		} else {
			vm.push(vmRetain(vm.Zone, types.Deref(vm.Zone, val)))
		}
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		vm.bump()
		return
	}
	d := types.Deref(vm.Zone, arr)
	if d.Type == types.TypeObject {
		if op == bytecode.OpFetchDimIs {
			exists, ok := vm.objectOffsetExists(d, key)
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			if !ok || vm.stop || vm.control {
				return
			}
			vm.push(types.NewBool(exists))
			vm.bump()
			return
		}
		val, ok := vm.objectOffsetGet(d, key)
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		if !ok || vm.stop || vm.control {
			return
		}
		if op == bytecode.OpFetchDimW {
			ref, err := types.NewReference(vm.Zone, val)
			vmRelease(vm.Zone, &val)
			if err != nil {
				vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
				return
			}
			vm.push(ref)
			vm.bump()
			return
		}
		vm.push(val)
		vm.bump()
		return
	}
	if d.Type == types.TypeString {
		if op == bytecode.OpFetchDimW {
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			vm.raiseBuiltin(bytecode.ClassError, "Cannot create references to/from string offsets", 0)
			return
		}
		kd := types.Deref(vm.Zone, key)
		if op == bytecode.OpFetchDimIs {
			idx, ok := vm.validStringOffsetForIsset(kd)
			vmRelease(vm.Zone, &key)
			if !ok {
				vmRelease(vm.Zone, &arr)
				vm.push(types.NewNull())
				vm.bump()
				return
			}
			n := int64(len(types.StringData(vm.Zone, d)))
			i := idx
			if i < 0 {
				i += n
			}
			if i < 0 || i >= n {
				vmRelease(vm.Zone, &arr)
				vm.push(types.NewNull())
				vm.bump()
				return
			}
			ch, err := types.ByteAt(vm.Zone, d, idx, func(types.StringWarning) error { return nil })
			vmRelease(vm.Zone, &arr)
			if err != nil {
				vm.push(types.NewNull())
			} else {
				vm.push(ch)
			}
			vm.bump()
			return
		}
		if op == bytecode.OpFetchDimR && (kd.Type == types.TypeDouble || kd.Type == types.TypeTrue || kd.Type == types.TypeFalse || kd.Type == types.TypeNull) {
			if vm.emitWarning("String offset cast occurred", vm.line); vm.stop || vm.control {
				vmRelease(vm.Zone, &key)
				vmRelease(vm.Zone, &arr)
				return
			}
		}
		idx := vm.toLong(kd)
		vmRelease(vm.Zone, &key)
		if vm.stop || vm.control {
			vmRelease(vm.Zone, &arr)
			return
		}
		ch, err := types.ByteAt(vm.Zone, d, idx, func(w types.StringWarning) error {
			var buf [96]byte
			vm.emitWarning(string(w.AppendMessage(buf[:0])), vm.line)
			return nil
		})
		vmRelease(vm.Zone, &arr)
		if err != nil {
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
		vm.push(ch)
		vm.bump()
		return
	}
	if op == bytecode.OpFetchDimW {
		if d.Type == types.TypeUndef || d.Type == types.TypeNull || d.Type == types.TypeFalse {
			newArr, err := types.NewArray(vm.Zone)
			if err != nil {
				vmRelease(vm.Zone, &key)
				vmRelease(vm.Zone, &arr)
				vm.fatalFeature("tableau")
				return
			}
			if arr.Type == types.TypeReference {
				types.AssignRef(vm.Zone, arr, newArr)
				types.ReleaseValue(vm.Zone, &newArr)
			} else {
				vmRelease(vm.Zone, &arr)
				arr = newArr
			}
		} else if d.Type != types.TypeArray {
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			vm.raiseBuiltin(bytecode.ClassError, "Cannot use a scalar value as an array", 0)
			return
		}
		ref, err := types.ArrayReference(vm.Zone, &arr, key, vm.deprecatedFloatKey)
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		if err != nil {
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
		vm.push(ref)
		vm.bump()
		return
	}
	if op == bytecode.OpFetchDimIs {
		if d.Type != types.TypeArray {
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			vm.push(types.NewNull())
			vm.bump()
			return
		}
		val, ok, err := types.ArrayGet(vm.Zone, arr, key, vm.deprecatedFloatKey)
		if err != nil {
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
		if !ok || val.IsUndef() {
			vm.push(types.NewNull())
		} else {
			vm.push(vmRetain(vm.Zone, types.Deref(vm.Zone, val)))
		}
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		vm.bump()
		return
	}
	if d.Type != types.TypeArray {
		autoVivified := flags&bytecode.FlagDimWrite != 0 && (d.Type == types.TypeNull || d.Type == types.TypeUndef || d.Type == types.TypeFalse)
		if !autoVivified {
			vm.emitWarning("Trying to access array offset on "+scalarOffsetTypeName(d.Type), vm.line)
			if vm.stop || vm.control {
				vmRelease(vm.Zone, &key)
				vmRelease(vm.Zone, &arr)
				return
			}
		}
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		vm.push(types.NewNull())
		vm.bump()
		return
	}
	val, ok, err := types.ArrayGet(vm.Zone, arr, key, vm.deprecatedFloatKey)
	if err != nil {
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
		return
	}
	if !ok || val.IsUndef() {
		if flags&bytecode.FlagDimWrite == 0 && vm.warnUndefinedKey(key) {
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			return
		}
		vm.push(types.NewNull())
	} else {
		vm.push(vmRetain(vm.Zone, types.Deref(vm.Zone, val)))
	}
	vmRelease(vm.Zone, &key)
	vmRelease(vm.Zone, &arr)
	vm.bump()
}

// warnUndefinedKey signale par raiseDiag la lecture d'une clé absente, sous la forme normalisée de PHP :
// entier nu, chaîne entre guillemets. Le résultat vrai signale que le gestionnaire a interrompu l'instruction.
func (vm *VM) warnUndefinedKey(key types.Value) bool {
	nk, err := types.NormalizeArrayKey(vm.Zone, key, nil)
	if err != nil {
		return false
	}
	msg := "Undefined array key "
	if nk.Type == types.TypeLong {
		msg += strconv.FormatInt(nk.ToLong(), 10)
	} else {
		msg += `"` + types.StringData(vm.Zone, nk) + `"`
	}
	return vm.raiseDiag(eWarning, diagWarning, msg, vm.line)
}

func (vm *VM) opAssignDim(op bytecode.Opcode, in bytecode.Instr) {
	if in.Flags&bytecode.FlagAssignDimCV != 0 {
		val := vm.pop()
		key := vm.pop()
		if vm.stop {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &key)
			return
		}
		fr := vm.frame()
		slot := &vm.slots[fr.cvBase+int(in.Op1)]
		d := types.Deref(vm.Zone, *slot)
		if d.Type == types.TypeUndef || d.Type == types.TypeNull || d.Type == types.TypeFalse {
			newArr, err := types.NewArray(vm.Zone)
			if err != nil {
				vmRelease(vm.Zone, &val)
				vmRelease(vm.Zone, &key)
				vm.fatalFeature("tableau")
				return
			}
			if slot.Type == types.TypeReference {
				types.AssignRef(vm.Zone, *slot, newArr)
				types.ReleaseValue(vm.Zone, &newArr)
			} else {
				vmRelease(vm.Zone, slot)
				*slot = newArr
			}
			slot = &vm.slots[vm.frame().cvBase+int(in.Op1)]
			d = types.Deref(vm.Zone, *slot)
		}
		if d.Type == types.TypeObject {
			if op == bytecode.OpAssignDimOp {
				cur, ok := vm.objectOffsetGet(d, key)
				if !ok || vm.stop || vm.control {
					vmRelease(vm.Zone, &val)
					vmRelease(vm.Zone, &key)
					return
				}
				val = vm.applyOp(bytecode.Opcode(in.Op2), cur, val)
				vmRelease(vm.Zone, &cur)
				if vm.stop || vm.control {
					vmRelease(vm.Zone, &val)
					vmRelease(vm.Zone, &key)
					return
				}
			}
			ok := vm.objectOffsetSet(d, key, val)
			vmRelease(vm.Zone, &key)
			if !ok || vm.stop || vm.control {
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
		if d.Type == types.TypeString {
			if op == bytecode.OpAssignDimOp {
				vmRelease(vm.Zone, &val)
				vmRelease(vm.Zone, &key)
				vm.raiseBuiltin(bytecode.ClassError, "Cannot use assign-op operators with string offsets", 0)
				return
			}
			idx := vm.toLong(key)
			vmRelease(vm.Zone, &key)
			if vm.stop || vm.control {
				vmRelease(vm.Zone, &val)
				return
			}
			repl, replOwned := vm.stringify(val)
			vmRelease(vm.Zone, &val)
			if vm.stop || vm.control {
				if replOwned {
					vmRelease(vm.Zone, &repl)
				}
				return
			}
			slot = &vm.slots[vm.frame().cvBase+int(in.Op1)]
			var err error
			if slot.Type == types.TypeReference {
				s := types.RetainValue(vm.Zone, types.Deref(vm.Zone, *slot))
				err = types.SetByteAt(vm.Zone, &s, idx, repl, func(types.StringWarning) error { return nil })
				if err == nil {
					types.AssignRef(vm.Zone, *slot, s)
				}
				types.ReleaseValue(vm.Zone, &s)
			} else {
				err = types.SetByteAt(vm.Zone, slot, idx, repl, func(types.StringWarning) error { return nil })
			}
			if err != nil {
				if replOwned {
					vmRelease(vm.Zone, &repl)
				}
				vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
				return
			}
			if in.Flags&bytecode.FlagDiscard == 0 {
				firstByte, _ := types.NewStringLiteral(vm.Zone, types.StringData(vm.Zone, repl)[:1])
				vm.push(firstByte)
			}
			if replOwned {
				vmRelease(vm.Zone, &repl)
			}
			vm.bump()
			return
		}
		if d.Type != types.TypeArray {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &key)
			vm.raiseBuiltin(bytecode.ClassError, "Cannot use a scalar value as an array", 0)
			return
		}
		if op == bytecode.OpAssignDimOp {
			cur, ok, err := types.ArrayGet(vm.Zone, d, key, vm.deprecatedFloatKey)
			if err != nil {
				vmRelease(vm.Zone, &val)
				vmRelease(vm.Zone, &key)
				vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
				return
			}
			if !ok {
				cur = types.NewNull()
			}
			curOwned := vmRetain(vm.Zone, types.Deref(vm.Zone, cur))
			val = vm.applyOp(bytecode.Opcode(in.Op2), curOwned, val)
			vmRelease(vm.Zone, &curOwned)
			if vm.stop || vm.control {
				vmRelease(vm.Zone, &val)
				vmRelease(vm.Zone, &key)
				return
			}
		}
		slot = &vm.slots[vm.frame().cvBase+int(in.Op1)]
		err := types.ArraySet(vm.Zone, slot, key, val, vm.deprecatedFloatKey)
		vmRelease(vm.Zone, &key)
		if err != nil {
			vmRelease(vm.Zone, &val)
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
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
	operator := in.Op1
	val := vm.pop()
	key := vm.pop()
	arr := vm.pop()
	if vm.stop {
		return
	}
	d := types.Deref(vm.Zone, arr)
	if d.Type == types.TypeUndef || d.Type == types.TypeNull || d.Type == types.TypeFalse {
		newArr, err := types.NewArray(vm.Zone)
		if err != nil {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			vm.fatalFeature("tableau")
			return
		}
		vmRelease(vm.Zone, &arr)
		arr = newArr
		d = arr
	}
	if d.Type == types.TypeObject {
		if op == bytecode.OpAssignDimOp {
			cur, ok := vm.objectOffsetGet(d, key)
			if !ok || vm.stop || vm.control {
				vmRelease(vm.Zone, &val)
				vmRelease(vm.Zone, &key)
				vmRelease(vm.Zone, &arr)
				return
			}
			val = vm.applyOp(bytecode.Opcode(operator), cur, val)
			vmRelease(vm.Zone, &cur)
			if vm.stop || vm.control {
				vmRelease(vm.Zone, &val)
				vmRelease(vm.Zone, &key)
				vmRelease(vm.Zone, &arr)
				return
			}
		}
		ok := vm.objectOffsetSet(d, key, val)
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &val)
		if !ok || vm.stop || vm.control {
			vmRelease(vm.Zone, &arr)
			return
		}
		vm.push(arr)
		vm.bump()
		return
	}
	if d.Type == types.TypeString {
		if op == bytecode.OpAssignDimOp {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			vm.raiseBuiltin(bytecode.ClassError, "Cannot use assign-op operators with string offsets", 0)
			return
		}
		idx := vm.toLong(key)
		vmRelease(vm.Zone, &key)
		if vm.stop || vm.control {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &arr)
			return
		}
		repl, replOwned := vm.stringify(val)
		vmRelease(vm.Zone, &val)
		if vm.stop || vm.control {
			if replOwned {
				vmRelease(vm.Zone, &repl)
			}
			vmRelease(vm.Zone, &arr)
			return
		}
		err := types.SetByteAt(vm.Zone, &arr, idx, repl, func(types.StringWarning) error { return nil })
		if replOwned {
			vmRelease(vm.Zone, &repl)
		}
		if err != nil {
			vmRelease(vm.Zone, &arr)
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
		vm.push(arr)
		vm.bump()
		return
	}
	if d.Type != types.TypeArray {
		vmRelease(vm.Zone, &val)
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		vm.raiseBuiltin(bytecode.ClassError, "Cannot use a scalar value as an array", 0)
		return
	}
	if op == bytecode.OpAssignDimOp {
		cur, ok, err := types.ArrayGet(vm.Zone, arr, key, vm.deprecatedFloatKey)
		if err != nil {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
		if !ok {
			cur = types.NewNull()
		}
		val = vm.applyOp(bytecode.Opcode(operator), cur, val)
		if vm.stop || vm.control {
			vmRelease(vm.Zone, &val)
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			return
		}
	}
	err := types.ArraySet(vm.Zone, &arr, key, val, vm.deprecatedFloatKey)
	vmRelease(vm.Zone, &key)
	vmRelease(vm.Zone, &val)
	if err != nil {
		vmRelease(vm.Zone, &arr)
		vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
		return
	}
	if vm.stop || vm.control {
		vmRelease(vm.Zone, &arr)
		return
	}
	vm.push(arr)
	vm.bump()
}

func (vm *VM) applyOp(op bytecode.Opcode, cur, val types.Value) types.Value {
	if res, handled := vm.arrayOperands(op, cur, val); handled {
		vmRelease(vm.Zone, &val)
		if vm.stop || vm.control {
			return types.NewNull()
		}
		return res
	}
	switch op {
	case bytecode.OpAdd:
		res := types.Add(vm.num(cur), vm.num(val))
		vmRelease(vm.Zone, &val)
		return res
	case bytecode.OpSub:
		res := types.Sub(vm.num(cur), vm.num(val))
		vmRelease(vm.Zone, &val)
		return res
	case bytecode.OpMul:
		res := types.Mul(vm.num(cur), vm.num(val))
		vmRelease(vm.Zone, &val)
		return res
	case bytecode.OpConcat:
		ls, lown := vm.stringify(cur)
		rs, rown := vm.stringify(val)
		if vm.stop || vm.control {
			if lown {
				types.ReleaseValue(vm.Zone, &ls)
			}
			if rown {
				types.ReleaseValue(vm.Zone, &rs)
			}
			vmRelease(vm.Zone, &val)
			return types.NewNull()
		}
		out, err := types.Concat(vm.Zone, ls, rs)
		if lown {
			types.ReleaseValue(vm.Zone, &ls)
		}
		if rown {
			types.ReleaseValue(vm.Zone, &rs)
		}
		vmRelease(vm.Zone, &val)
		if err != nil {
			vm.fatalFeature("concaténation")
			return types.NewNull()
		}
		return out
	default:
		vmRelease(vm.Zone, &val)
		vm.fatalFeature("opérateur d'affectation " + op.String())
		return types.NewNull()
	}
}

func (vm *VM) opUnsetDim() {
	key := vm.pop()
	arr := vm.pop()
	if vm.stop {
		return
	}
	if d := types.Deref(vm.Zone, arr); d.Type == types.TypeObject {
		ok := vm.objectOffsetUnset(d, key)
		types.ReleaseValue(vm.Zone, &key)
		if !ok || vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &arr)
			return
		}
		vm.push(arr)
		vm.bump()
		return
	}
	err := types.ArrayUnset(vm.Zone, &arr, key, vm.deprecatedFloatKey)
	types.ReleaseValue(vm.Zone, &key)
	if err != nil {
		types.ReleaseValue(vm.Zone, &arr)
		vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
		return
	}
	vm.push(arr)
	vm.bump()
}

func (vm *VM) opIssetDim(empty bool) {
	key := vm.pop()
	arr := vm.pop()
	if vm.stop {
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		return
	}
	d := types.Deref(vm.Zone, arr)
	if d.Type == types.TypeObject {
		exists, ok := vm.objectOffsetExists(d, key)
		if !ok || vm.stop || vm.control {
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			return
		}
		if empty {
			if !exists {
				vmRelease(vm.Zone, &key)
				vmRelease(vm.Zone, &arr)
				vm.push(types.NewBool(true))
				vm.bump()
				return
			}
			val, ok := vm.objectOffsetGet(d, key)
			vmRelease(vm.Zone, &key)
			vmRelease(vm.Zone, &arr)
			if !ok || vm.stop || vm.control {
				return
			}
			res := !types.ToBoolZ(vm.Zone, val)
			vmRelease(vm.Zone, &val)
			vm.push(types.NewBool(res))
			vm.bump()
			return
		}
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		vm.push(types.NewBool(exists))
		vm.bump()
		return
	}
	if d.Type == types.TypeString {
		idx, ok := vm.validStringOffsetForIsset(types.Deref(vm.Zone, key))
		vmRelease(vm.Zone, &key)
		if !ok {
			vmRelease(vm.Zone, &arr)
			vm.push(types.NewBool(empty))
			vm.bump()
			return
		}
		n := int64(len(types.StringData(vm.Zone, d)))
		if idx < 0 {
			idx += n
		}
		if idx < 0 || idx >= n {
			vmRelease(vm.Zone, &arr)
			vm.push(types.NewBool(empty))
			vm.bump()
			return
		}
		if empty {
			ch := types.StringData(vm.Zone, d)[idx]
			vmRelease(vm.Zone, &arr)
			vm.push(types.NewBool(ch == '0'))
			vm.bump()
			return
		}
		vmRelease(vm.Zone, &arr)
		vm.push(types.NewBool(true))
		vm.bump()
		return
	}
	if d.Type != types.TypeArray {
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		vm.push(types.NewBool(empty))
		vm.bump()
		return
	}
	if empty {
		val, ok, err := types.ArrayGet(vm.Zone, arr, key, vm.deprecatedFloatKey)
		vmRelease(vm.Zone, &key)
		vmRelease(vm.Zone, &arr)
		if err != nil {
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
		vm.push(types.NewBool(!ok || !types.ToBoolZ(vm.Zone, val)))
		vm.bump()
		return
	}
	ok, err := types.ArrayIsset(vm.Zone, arr, key, vm.deprecatedFloatKey)
	vmRelease(vm.Zone, &key)
	vmRelease(vm.Zone, &arr)
	if err != nil {
		vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
		return
	}
	vm.push(types.NewBool(ok))
	vm.bump()
}

func (vm *VM) opForeach(op bytecode.Opcode, in bytecode.Instr) {
	fr := vm.frame()
	switch op {
	case bytecode.OpFeResetR:
		arr := vm.pop()
		if vm.stop {
			return
		}
		slot := vm.iterSlot(fr, in.Op2)
		vm.freeIter(slot)
		if g := vm.generatorOf(arr); g != nil {
			slot.gen, slot.active = g, true
			if !vm.rewindGenerator(g) {
				types.ReleaseValue(vm.Zone, &arr)
				return
			}
			if g.done {
				types.ReleaseValue(vm.Zone, &arr)
				vm.jump(int(in.Op1))
				return
			}
			vm.push(arr)
			vm.bump()
			return
		}
		if vm.prepareObjectIter(slot, arr) {
			if vm.stop || vm.control {
				types.ReleaseValue(vm.Zone, &arr)
				return
			}
			if !slot.active {
				types.ReleaseValue(vm.Zone, &arr)
				vm.freeIter(slot)
				vm.jump(int(in.Op1))
				return
			}
			vm.push(arr)
			vm.bump()
			return
		}
		vm.snapshot(slot, arr, false, -1)
		if len(slot.keys) == 0 {
			types.ReleaseValue(vm.Zone, &arr)
			vm.jump(int(in.Op1))
			return
		}
		vm.push(arr)
		vm.bump()
	case bytecode.OpFeResetRW:
		slot := vm.iterSlot(fr, in.Op1)
		vm.freeIter(slot)
		arr := vm.slots[fr.cvBase+int(in.Op2)]
		if types.ArrayLen(vm.Zone, arr) == 0 {
			vm.jump(int(in.Result))
			return
		}
		slot.byRef = true
		slot.arrayCV = int(in.Op2)
		slot.index = 0
		slot.active = true
		vm.bump()
	case bytecode.OpFeFetchR:
		slot := vm.iterSlot(fr, in.Op2)
		if g := slot.gen; g != nil {
			if slot.index > 0 && !vm.resumeGenerator(g, types.NewNull(), false) {
				return
			}
			fr = vm.frame()
			slot = vm.iterSlot(fr, in.Op2)
			if g.done {
				arr := vm.pop()
				types.ReleaseValue(vm.Zone, &arr)
				vm.freeIter(slot)
				vm.jump(int(in.Op1))
				return
			}
			vm.storeCVMove(in.Result, types.RetainValue(vm.Zone, g.value))
			fr = vm.frame()
			if in.Flags&bytecode.FlagHasKey != 0 {
				vm.storeCVMove(fr.fn.Aux[fr.pc], types.RetainValue(vm.Zone, g.key))
				fr = vm.frame()
			}
			slot = vm.iterSlot(fr, in.Op2)
			slot.index++
			vm.bump()
			return
		}
		if slot.iterObj.Type == types.TypeObject {
			if slot.index > 0 {
				vm.iterNext(slot.iterObj)
				if vm.stop || vm.control {
					return
				}
				valid := vm.iterValid(slot.iterObj)
				if vm.stop || vm.control {
					return
				}
				fr = vm.frame()
				slot = vm.iterSlot(fr, in.Op2)
				if !valid {
					arr := vm.pop()
					types.ReleaseValue(vm.Zone, &arr)
					vm.freeIter(slot)
					vm.jump(int(in.Op1))
					return
				}
			}
			cur := vm.iterCurrent(slot.iterObj)
			if vm.stop || vm.control {
				types.ReleaseValue(vm.Zone, &cur)
				return
			}
			var key types.Value
			hasKey := in.Flags&bytecode.FlagHasKey != 0
			if hasKey {
				key = vm.iterKey(slot.iterObj)
				if vm.stop || vm.control {
					types.ReleaseValue(vm.Zone, &cur)
					types.ReleaseValue(vm.Zone, &key)
					return
				}
			}
			vm.storeCVMove(in.Result, cur)
			fr = vm.frame()
			if hasKey && fr.pc < len(fr.fn.Aux) {
				vm.storeCVMove(fr.fn.Aux[fr.pc], key)
				fr = vm.frame()
			}
			slot = vm.iterSlot(fr, in.Op2)
			slot.index++
			vm.bump()
			return
		}
		if !slot.active || slot.index >= len(slot.keys) {
			arr := vm.pop()
			types.ReleaseValue(vm.Zone, &arr)
			vm.freeIter(slot)
			vm.jump(int(in.Op1))
			return
		}
		idx := slot.index
		val := types.RetainValue(vm.Zone, slot.vals[idx])
		hasKey := in.Flags&bytecode.FlagHasKey != 0 && fr.pc < len(fr.fn.Aux)
		var key types.Value
		if hasKey {
			key = types.RetainValue(vm.Zone, slot.keys[idx])
		}
		vm.storeCVMove(in.Result, val)
		fr = vm.frame()
		if hasKey {
			vm.storeCVMove(fr.fn.Aux[fr.pc], key)
			fr = vm.frame()
		}
		slot = vm.iterSlot(fr, in.Op2)
		slot.index++
		vm.bump()
	case bytecode.OpFeFetchRW:
		slot := vm.iterSlot(fr, in.Op2)
		arr := &vm.slots[fr.cvBase+slot.arrayCV]
		key, nextIdx, ok := types.ArrayBucketAt(vm.Zone, *arr, uint32(slot.index))
		if !slot.active || !ok {
			vm.freeIter(slot)
			vm.jump(int(in.Op1))
			return
		}
		keyOwned := types.RetainValue(vm.Zone, key)
		ref, err := types.ArrayReference(vm.Zone, arr, keyOwned, vm.deprecatedFloatKey)
		if err != nil {
			types.ReleaseValue(vm.Zone, &keyOwned)
			vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
			return
		}
		vm.storeCVMove(in.Result, ref)
		fr = vm.frame()
		if in.Flags&bytecode.FlagHasKey != 0 && fr.pc < len(fr.fn.Aux) {
			vm.storeCVMove(fr.fn.Aux[fr.pc], keyOwned)
			fr = vm.frame()
		} else {
			types.ReleaseValue(vm.Zone, &keyOwned)
		}
		slot = vm.iterSlot(fr, in.Op2)
		slot.index = int(nextIdx)
		vm.bump()
	case bytecode.OpFeFree:
		slot := vm.iterSlot(fr, in.Op2)
		vm.freeIter(slot)
		vm.bump()
	}
}

func (vm *VM) iterSlot(fr *frame, idx uint32) *iterState {
	for int(idx) >= len(fr.iters) {
		fr.iters = append(fr.iters, iterState{})
	}
	return &fr.iters[idx]
}

func (vm *VM) snapshot(slot *iterState, arr types.Value, byRef bool, arrayCV int) {
	slot.byRef = byRef
	slot.arrayCV = arrayCV
	slot.index = 0
	slot.active = true
	d := types.Deref(vm.Zone, arr)
	if d.Type == types.TypeObject {
		info := types.ObjectInfo(vm.Zone, d)
		if c := vm.classByID(info.ClassID); c != nil && c.PropsResolved {
			for i := range c.ResolvedProps {
				p := c.ResolvedProps[i]
				if !vm.visible(p.Visibility, p.ClassID) {
					continue
				}
				val := types.ObjectGet(vm.Zone, d, p.Index)
				if val.IsUndef() {
					continue
				}
				ks, err := types.NewStringLiteral(vm.Zone, p.Name)
				if err != nil {
					continue
				}
				slot.keys = append(slot.keys, ks)
				slot.vals = append(slot.vals, types.RetainValue(vm.Zone, types.Deref(vm.Zone, val)))
			}
		}
		if info.DynOffset != types.EmptySlot {
			dynArr := types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: info.DynOffset}
			types.ArrayForEach(vm.Zone, dynArr, func(k, v types.Value) bool {
				ks, own := vm.stringify(k)
				if !own {
					ks = types.RetainValue(vm.Zone, ks)
				}
				slot.keys = append(slot.keys, ks)
				slot.vals = append(slot.vals, types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)))
				return true
			})
		}
		return
	}
	if d.Type != types.TypeArray {
		return
	}
	types.ArrayForEach(vm.Zone, arr, func(k, v types.Value) bool {
		slot.keys = append(slot.keys, types.RetainValue(vm.Zone, k))
		if !byRef {
			slot.vals = append(slot.vals, types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)))
		}
		return true
	})
}

func (vm *VM) freeIter(slot *iterState) {
	if slot == nil {
		return
	}
	for i := range slot.keys {
		types.ReleaseValue(vm.Zone, &slot.keys[i])
	}
	for i := range slot.vals {
		types.ReleaseValue(vm.Zone, &slot.vals[i])
	}
	if slot.iterObj.Type != types.TypeUndef {
		types.ReleaseValue(vm.Zone, &slot.iterObj)
		slot.iterObj = types.Value{}
	}
	slot.keys = nil
	slot.gen = nil
	slot.vals = nil
	slot.active = false
	slot.index = 0
}

func (vm *VM) storeCVMove(idx uint32, v types.Value) {
	if idx == bytecode.NoCV {
		types.ReleaseValue(vm.Zone, &v)
		return
	}
	slot := vm.cvPtr(idx)
	old := *slot
	*slot = v
	types.ReleaseValue(vm.Zone, &old)
}

// opUnpackIntoArray déploie dans le tableau littéral en construction un tableau ou un Traversable : clés
// entières renumérotées, clés chaînes écrasant la valeur précédente comme en PHP 8.1.
func (vm *VM) opUnpackIntoArray() {
	src := vm.pop()
	arr := vm.pop()
	if vm.stop {
		vmRelease(vm.Zone, &src)
		vmRelease(vm.Zone, &arr)
		return
	}
	d := types.Deref(vm.Zone, src)
	items := d
	owned := false
	if d.Type == types.TypeObject {
		collected, ok := vm.collectTraversable(d, true)
		if !ok {
			vmRelease(vm.Zone, &src)
			vmRelease(vm.Zone, &arr)
			if !vm.stop && !vm.control {
				vm.raiseBuiltin(bytecode.ClassError, "Only arrays and Traversables can be unpacked", 0)
			}
			return
		}
		items, owned = collected, true
	} else if d.Type != types.TypeArray {
		vmRelease(vm.Zone, &src)
		vmRelease(vm.Zone, &arr)
		vm.raiseBuiltin(bytecode.ClassError, "Only arrays and Traversables can be unpacked", 0)
		return
	}
	var err error
	types.ArrayForEach(vm.Zone, items, func(k, v types.Value) bool {
		v = types.Deref(vm.Zone, v)
		if k.Type == types.TypeString {
			err = types.ArraySet(vm.Zone, &arr, k, v, nil)
		} else {
			err = types.ArrayAppend(vm.Zone, &arr, v)
		}
		return err == nil
	})
	if owned {
		types.ReleaseValue(vm.Zone, &items)
	}
	vmRelease(vm.Zone, &src)
	if err != nil {
		vmRelease(vm.Zone, &arr)
		vm.raiseBuiltin(bytecode.ClassError, err.Error(), 0)
		return
	}
	vm.push(arr)
	vm.bump()
}
