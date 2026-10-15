package vm

import (
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func (vm *VM) enumCases(c *bytecode.ClassProto) []types.Value {
	if cases, ok := vm.enums[c.ID]; ok {
		return cases
	}
	cases := make([]types.Value, 0, len(c.Cases))
	for _, entry := range c.Cases {
		obj, err := types.NewObject(vm.Zone, c.ID, uint32(len(c.Props)), false)
		if err != nil {
			vm.fatalFeature(err.Error())
			break
		}
		name := vm.constantValue(&bytecode.Constant{Text: entry.Name, IsString: true})
		types.ObjectSet(vm.Zone, obj, 0, name)
		types.ReleaseValue(vm.Zone, &name)
		if entry.Value != nil {
			v := vm.constantValue(entry.Value)
			types.ObjectSet(vm.Zone, obj, 1, v)
			types.ReleaseValue(vm.Zone, &v)
		}
		cases = append(cases, obj)
	}
	if vm.enums == nil {
		vm.enums = make(map[uint32][]types.Value)
	}
	vm.enums[c.ID] = cases
	return cases
}

func (vm *VM) enumMethod(spec callSpec) bool {
	c := vm.classByID(spec.classID)
	if c == nil || c.Kind != bytecode.KindEnum {
		return false
	}
	name := strings.ToLower(spec.method)
	if name != "cases" && (c.BackingType == "" || name != "from" && name != "tryfrom") {
		return false
	}
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	cases := vm.enumCases(c)
	if vm.stop {
		return true
	}
	if name == "cases" {
		v, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature(err.Error())
			return true
		}
		for _, obj := range cases {
			if err := types.ArrayAppend(vm.Zone, &v, obj); err != nil {
				types.ReleaseValue(vm.Zone, &v)
				vm.fatalFeature(err.Error())
				return true
			}
		}
		vm.push(v)
		vm.bump()
		return true
	}
	if len(spec.args) != 1 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, c.Name+"::"+spec.method+"() expects exactly 1 argument", 0)
		return true
	}
	v := types.RetainValue(vm.Zone, types.Deref(vm.Zone, spec.args[0]))
	defer types.ReleaseValue(vm.Zone, &v)
	if !vm.coerceType(&v, c.BackingType, vm.frame().fn.StrictTypes, c.ID, c.ID) {
		vm.raiseBuiltin(bytecode.ClassTypeError, c.Name+"::"+spec.method+"(): Argument #1 ($value) must be of type "+c.BackingType+", "+vm.valueType(spec.args[0])+" given", 0)
		return true
	}
	for _, obj := range cases {
		backing := types.ObjectGet(vm.Zone, obj, 1)
		match := v.Type == backing.Type && v.Scalar == backing.Scalar
		if v.Type == types.TypeString && backing.Type == types.TypeString {
			match = types.StringData(vm.Zone, v) == types.StringData(vm.Zone, backing)
		}
		if match {
			vm.push(types.RetainValue(vm.Zone, obj))
			vm.bump()
			return true
		}
	}
	if name == "tryfrom" {
		vm.push(types.NewNull())
		vm.bump()
		return true
	}
	s, own := vm.stringify(v)
	text := types.StringData(vm.Zone, s)
	if own {
		types.ReleaseValue(vm.Zone, &s)
	}
	if v.Type == types.TypeString {
		text = "\"" + text + "\""
	}
	vm.raiseBuiltin(bytecode.ClassValueError, text+" is not a valid backing value for enum "+c.Name, 0)
	return true
}
