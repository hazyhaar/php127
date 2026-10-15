package vm

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func (vm *VM) valueType(v types.Value) string {
	v = types.Deref(vm.Zone, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		return "null"
	case types.TypeLong:
		return "int"
	case types.TypeDouble:
		return "float"
	case types.TypeString:
		return "string"
	case types.TypeTrue:
		return "true"
	case types.TypeFalse:
		return "false"
	case types.TypeArray:
		return "array"
	case types.TypeObject:
		return vm.className(types.ObjectInfo(vm.Zone, v).ClassID)
	case types.TypeResource:
		if cid, ok := opaqueResourceClassID(v.Extra); ok {
			return vm.className(cid)
		}
		return "resource"
	default:
		return "resource"
	}
}

// Le test exact precede toute coercition, notamment dans les unions scalaires.
func (vm *VM) matchesType(v types.Value, name string, scope, called uint32) bool {
	if strings.Contains(name, "|") {
		for part := range strings.SplitSeq(name, "|") {
			if vm.matchesType(v, part, scope, called) {
				return true
			}
		}
		return false
	}
	if strings.Contains(name, "&") {
		for part := range strings.SplitSeq(name, "&") {
			if !vm.matchesType(v, part, scope, called) {
				return false
			}
		}
		return true
	}
	switch strings.ToLower(name) {
	case "", "mixed":
		return true
	case "never", "void":
		return false
	case "null":
		return v.IsNull()
	case "true":
		return v.Type == types.TypeTrue
	case "false":
		return v.Type == types.TypeFalse
	case "bool":
		return v.Type == types.TypeTrue || v.Type == types.TypeFalse
	case "int":
		return v.Type == types.TypeLong
	case "float":
		return v.Type == types.TypeDouble
	case "string":
		return v.Type == types.TypeString
	case "array":
		return v.Type == types.TypeArray
	case "object":
		if v.Type == types.TypeResource {
			_, ok := opaqueResourceClassID(v.Extra)
			return ok
		}
		return v.Type == types.TypeObject
	case "iterable":
		if v.Type == types.TypeArray {
			return true
		}
		name = "Traversable"
	case "callable":
		_, ok := vm.callback(v)
		return ok
	}
	var objClass uint32
	if v.Type == types.TypeObject {
		objClass = types.ObjectInfo(vm.Zone, v).ClassID
	} else if cid, ok := opaqueResourceClassID(v.Extra); v.Type == types.TypeResource && ok {
		objClass = cid
	} else {
		return false
	}
	want, ok := vm.resolveName(name)
	switch strings.ToLower(name) {
	case "self":
		want, ok = scope, scope != 0
	case "parent":
		want = vm.parentOf(scope)
		ok = want != 0
	case "static":
		want, ok = called, called != 0
	}
	return ok && vm.isInstance(objClass, want)
}

func (vm *VM) coerceType(value *types.Value, name string, strict bool, scope, called uint32) bool {
	v := types.Deref(vm.Zone, *value)
	if vm.matchesType(v, name, scope, called) {
		return true
	}
	parts := strings.Split(strings.ToLower(name), "|")
	has := func(want string) bool {
		return slices.Contains(parts, want)
	}
	replace := func(next types.Value) bool {
		if value.Type == types.TypeReference {
			types.AssignRef(vm.Zone, *value, next)
			types.ReleaseValue(vm.Zone, &next)
			return true
		}
		types.ReleaseValue(vm.Zone, value)
		*value = next
		return true
	}
	if v.Type == types.TypeLong && has("float") {
		return replace(types.NewDouble(float64(v.ToLong())))
	}
	if strict || v.IsNull() || v.IsUndef() || v.Type == types.TypeArray || v.Type == types.TypeObject {
		return false
	}
	var number float64
	numeric := false
	switch v.Type {
	case types.TypeLong:
		number, numeric = float64(v.ToLong()), true
	case types.TypeDouble:
		number, numeric = v.ToDouble(), true
	case types.TypeTrue:
		number, numeric = 1, true
	case types.TypeFalse:
		numeric = true
	case types.TypeString:
		var err error
		number, err = strconv.ParseFloat(strings.TrimSpace(types.StringData(vm.Zone, v)), 64)
		numeric = err == nil && !math.IsInf(number, 0) && !math.IsNaN(number)
	}
	if numeric {
		if v.Type == types.TypeString && has("int") {
			if integer, err := strconv.ParseInt(strings.TrimSpace(types.StringData(vm.Zone, v)), 10, 64); err == nil {
				return replace(types.NewLong(integer))
			}
		}
		preferFloat := v.Type == types.TypeString && has("float") && strings.ContainsAny(types.StringData(vm.Zone, v), ".eE")
		if has("int") && !preferFloat && number >= -0x1p63 && number < 0x1p63 {
			if number != math.Trunc(number) && vm.errorReporting&8192 != 0 {
				from := "float " + strconv.FormatFloat(number, 'g', -1, 64)
				if v.Type == types.TypeString {
					from = "float-string \"" + types.StringData(vm.Zone, v) + "\""
				}
				vm.raiseDiag(eDeprecated, diagDeprecate, "Implicit conversion from "+from+" to int loses precision", vm.line)
			}
			return replace(types.NewLong(int64(number)))
		}
		if has("float") {
			return replace(types.NewDouble(number))
		}
	}
	if has("string") {
		s, owned := vm.stringify(v)
		if !owned {
			s = types.RetainValue(vm.Zone, s)
		}
		return replace(s)
	}
	if has("bool") {
		return replace(types.NewBool(types.ToBoolZ(vm.Zone, v)))
	}
	return false
}

func (vm *VM) checkArgs(fn *bytecode.OpArray, args []types.Value, scope, called uint32) bool {
	if len(args) == 0 || len(fn.Params) == 0 {
		return true
	}
	strict := len(vm.frames) > 0 && vm.frame().fn.StrictTypes
	for i := range args {
		pidx := i
		if pidx >= len(fn.Params) {
			pidx = len(fn.Params) - 1
		}
		if pidx < 0 || i >= len(fn.Params) && !fn.Params[pidx].Variadic {
			break
		}
		p := fn.Params[pidx]
		if !p.ByRef && args[i].Type == types.TypeReference {
			v := vmRetain(vm.Zone, types.Deref(vm.Zone, args[i]))
			vmRelease(vm.Zone, &args[i])
			args[i] = v
		}
		if p.TypeName == "" || args[i].IsUndef() {
			continue
		}
		name := p.TypeName
		if p.HasDefault && p.Default.IsNull() {
			name += "|null"
		}
		if vm.coerceType(&args[i], name, strict, scope, called) {
			continue
		}
		given := vm.valueType(args[i])
		label := fn.Name
		if scope != 0 {
			label = vm.className(scope) + "::" + label
		}
		vm.releaseArgs(args)
		vm.raiseBuiltin(bytecode.ClassTypeError, fmt.Sprintf("%s(): Argument #%d ($%s) must be of type %s, %s given, called in %s on line %d", label, i+1, p.Name, name, given, vm.file(), vm.line), 0)
		return false
	}
	return true
}

func (vm *VM) checkProperty(v *types.Value, p bytecode.PropInfo) bool {
	given := vm.valueType(*v)
	if vm.coerceType(v, p.TypeName, vm.frame().fn.StrictTypes, p.ClassID, p.ClassID) {
		return true
	}
	vm.raiseBuiltin(bytecode.ClassTypeError, "Cannot assign "+given+" to property "+vm.className(p.ClassID)+"::$"+p.Name+" of type "+p.TypeName, 0)
	return false
}
