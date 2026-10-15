package vm

import (
	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func (vm *VM) constantValue(c *bytecode.Constant) types.Value {
	if c.IsString {
		v, err := types.NewStringLiteral(vm.Zone, c.Text)
		if err != nil {
			vm.fatalFeature(err.Error())
		}
		return v
	}
	if !c.IsArray {
		return types.RetainValue(vm.Zone, c.Value)
	}
	v, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature(err.Error())
		return v
	}
	for _, item := range c.Items {
		x := vm.constantValue(&item.Value)
		if item.Key == nil {
			err = types.ArrayAppend(vm.Zone, &v, x)
		} else {
			key := vm.constantValue(item.Key)
			err = types.ArraySet(vm.Zone, &v, key, x, nil)
			types.ReleaseValue(vm.Zone, &key)
		}
		types.ReleaseValue(vm.Zone, &x)
		if err != nil {
			types.ReleaseValue(vm.Zone, &v)
			vm.fatalFeature(err.Error())
			break
		}
	}
	return v
}

func (vm *VM) propDefault(p bytecode.PropInfo) types.Value {
	if p.Initial != nil {
		return vm.constantValue(p.Initial)
	}
	if p.HasDefault {
		return types.RetainValue(vm.Zone, p.Default)
	}
	if p.TypeName != "" {
		return types.NewUndef()
	}
	return types.NewNull()
}
