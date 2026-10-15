package vm

import (
	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
	"strings"
)

func (vm *VM) globalSnapshot() types.Value {
	arr, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature(err.Error())
		return arr
	}
	put := func(name string, value types.Value) {
		if value.IsUndef() || name == "GLOBALS" || strings.HasPrefix(name, "\x00") {
			return
		}
		key, err := types.NewStringLiteral(vm.Zone, name)
		if err != nil {
			vm.fatalFeature(err.Error())
			return
		}
		err = types.ArraySet(vm.Zone, &arr, key, types.Deref(vm.Zone, value), nil)
		types.ReleaseValue(vm.Zone, &key)
		if err != nil {
			vm.fatalFeature(err.Error())
		}
	}
	for name, value := range vm.globals {
		put(name, value)
	}
	root := &vm.frames[0]
	for i, name := range root.fn.CVNames {
		put(name, vm.slots[root.cvBase+i])
	}
	return arr
}

func (vm *VM) opGlobal(op bytecode.Opcode) {
	var value types.Value
	if op == bytecode.OpGlobalSet {
		value = vm.pop()
	}
	key := vm.pop()
	s, owned := vm.stringify(key)
	name := types.StringData(vm.Zone, s)
	if owned {
		types.ReleaseValue(vm.Zone, &s)
	}
	types.ReleaseValue(vm.Zone, &key)
	if vm.stop || vm.control {
		types.ReleaseValue(vm.Zone, &value)
		return
	}
	var slot *types.Value
	root := &vm.frames[0]
	for i, n := range root.fn.CVNames {
		if n == name {
			slot = &vm.slots[root.cvBase+i]
			break
		}
	}
	current := vm.globals[name]
	if slot != nil {
		current = *slot
	}
	switch op {
	case bytecode.OpGlobalGet:
		v := types.Deref(vm.Zone, current)
		if v.IsUndef() {
			v = types.NewNull()
		}
		vm.push(types.RetainValue(vm.Zone, v))
	case bytecode.OpGlobalIsset:
		v := types.Deref(vm.Zone, current)
		vm.push(types.NewBool(!v.IsUndef() && !v.IsNull()))
	case bytecode.OpGlobalSet:
		if current.Type == types.TypeReference {
			types.AssignRef(vm.Zone, current, value)
		} else {
			types.ReleaseValue(vm.Zone, &current)
			current = types.RetainValue(vm.Zone, value)
		}
		if slot != nil {
			*slot = current
		} else {
			vm.globals[name] = current
		}
		vm.push(value)
	case bytecode.OpGlobalUnset:
		types.ReleaseValue(vm.Zone, &current)
		if slot != nil {
			*slot = types.NewUndef()
		} else {
			delete(vm.globals, name)
		}
	}
	vm.bump()
}
