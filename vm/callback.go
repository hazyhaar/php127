package vm

import (
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func (vm *VM) callback(v types.Value) (callSpec, bool) {
	v = types.Deref(vm.Zone, v)
	spec := callSpec{icPC: -1}
	switch v.Type {
	case types.TypeString:
		name := types.StringData(vm.Zone, v)
		if class, method, found := strings.Cut(name, "::"); found {
			spec.kind, spec.method = callStatic, method
			var ok bool
			spec.classID, ok = vm.resolveName(class)
			if !ok {
				return spec, false
			}
		} else {
			spec.kind, spec.method, spec.flags = callFunc, name, bytecode.FlagNative
			for i, fn := range vm.Program.Funcs {
				if fn != nil && !bytecode.AnonymousFunc(fn.Name) && strings.EqualFold(fn.Name, name) {
					spec.funcIndex, spec.flags = i, 0
					return spec, true
				}
			}
			_, ok := lookupNative(name)
			return spec, ok || intrinsicExists(name)
		}
	case types.TypeObject:
		spec.obj, spec.hasObj = v, true
		spec.classID = types.ObjectInfo(vm.Zone, v).ClassID
		if spec.classID == bytecode.ClassClosure {
			spec.kind = callClosure
			return spec, true
		}
		spec.kind, spec.method = callMethod, "__invoke"
	case types.TypeArray:
		if types.ArrayLen(vm.Zone, v) != 2 {
			return spec, false
		}
		target, foundTarget, err := types.ArrayGet(vm.Zone, v, types.NewLong(0), nil)
		if err != nil || !foundTarget {
			return spec, false
		}
		method, foundMethod, err := types.ArrayGet(vm.Zone, v, types.NewLong(1), nil)
		if err != nil || !foundMethod {
			return spec, false
		}
		if method.Type != types.TypeString {
			return spec, false
		}
		spec.method = types.StringData(vm.Zone, method)
		target = types.Deref(vm.Zone, target)
		if target.Type == types.TypeObject {
			spec.kind, spec.obj, spec.hasObj = callMethod, target, true
			spec.classID = types.ObjectInfo(vm.Zone, target).ClassID
		} else if target.Type == types.TypeString {
			spec.kind = callStatic
			var ok bool
			spec.classID, ok = vm.resolveName(types.StringData(vm.Zone, target))
			if !ok {
				return spec, false
			}
		} else {
			return spec, false
		}
	default:
		return spec, false
	}
	m, def, ok := vm.findMethod(spec.classID, spec.method)
	return spec, ok && vm.visible(m.Visibility, def) && (spec.kind != callStatic || m.Static)
}

func (vm *VM) callbackIntrinsic(spec callSpec, line uint32) bool {
	name := strings.ToLower(spec.method)
	if name != "is_callable" && name != "call_user_func" && name != "call_user_func_array" {
		return false
	}
	if len(spec.args) == 0 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, name+"() expects at least 1 argument, 0 given", 0)
		return true
	}
	call, ok := vm.callback(spec.args[0])
	if name == "is_callable" {
		vm.push(types.NewBool(ok))
		vm.bump()
		return true
	}
	if !ok {
		vm.raiseBuiltin(bytecode.ClassTypeError, name+"(): Argument #1 ($callback) must be a valid callback", 0)
		return true
	}
	if name == "call_user_func_array" {
		if len(spec.args) != 2 || types.Deref(vm.Zone, spec.args[1]).Type != types.TypeArray {
			vm.raiseBuiltin(bytecode.ClassTypeError, name+"(): Argument #2 ($args) must be of type array", 0)
			return true
		}
		types.ArrayForEach(vm.Zone, types.Deref(vm.Zone, spec.args[1]), func(k, v types.Value) bool {
			v = types.RetainValue(vm.Zone, v)
			if k.Type == types.TypeString {
				call.named = append(call.named, namedArg{name: types.StringData(vm.Zone, k), val: v})
			} else {
				call.args = append(call.args, v)
			}
			return true
		})
	} else {
		for _, v := range spec.args[1:] {
			call.args = append(call.args, types.RetainValue(vm.Zone, v))
		}
	}
	if call.hasObj {
		call.obj = types.RetainValue(vm.Zone, call.obj)
	}
	switch call.kind {
	case callFunc:
		vm.doFunc(call, line)
	case callClosure:
		vm.doClosure(call, line)
	default:
		vm.doMethod(call, line)
	}
	return true
}

func (vm *VM) invokeCallback(call callSpec, args []types.Value, line uint32) (types.Value, bool) {
	inv := call
	inv.args = args
	if inv.hasObj {
		inv.obj = types.RetainValue(vm.Zone, inv.obj)
	}
	switch inv.kind {
	case callClosure:
		obj := types.Deref(vm.Zone, inv.obj)
		idx := int(types.ObjectGet(vm.Zone, obj, 0).ToLong())
		if idx < 0 || idx >= len(vm.closures) || !vm.closures[idx].live {
			vm.releaseArgs(inv.args)
			types.ReleaseValue(vm.Zone, &inv.obj)
			vm.fatalFeature("fermeture")
			return types.NewNull(), true
		}
		data := vm.closures[idx]
		ret, threw := vm.invokeClosure(data, inv.args, line)
		types.ReleaseValue(vm.Zone, &inv.obj)
		if !threw && !vm.stop {
			vm.control = false
		}
		return ret, threw || vm.stop
	case callFunc:
		if inv.flags&bytecode.FlagNative == 0 {
			fn := vm.Program.Funcs[inv.funcIndex]
			ret, threw := vm.invokeOpArray(fn, types.Value{}, false, inv.args, 0, 0, line)
			if !threw && !vm.stop {
				vm.control = false
			}
			return ret, threw || vm.stop
		}
		if isPotentialIntrinsic(inv.method) {
			return vm.invokeNativeNested(inv, line)
		}
		if native, ok := lookupNative(inv.method); ok {
			dargs := make([]types.Value, len(inv.args))
			for i, a := range inv.args {
				if bytecode.NativeArgIsRef(inv.method, i) && a.Type == types.TypeReference {
					vm.separateRefArray(a)
					dargs[i] = a
				} else {
					dargs[i] = types.Deref(vm.Zone, a)
				}
			}
			vm.preNativeCallSync(inv.method)
			val, err := native(vm.Zone, vm.Out, dargs)
			if err != nil {
				vmRelease(vm.Zone, &val)
				vm.raiseNativeError(err, inv.method, dargs, line)
				vm.releaseArgs(inv.args)
				return types.NewNull(), true
			}
			vm.postNativeCallSync(inv.method)
			vm.releaseArgs(inv.args)
			return val, vm.stop
		}
		vm.releaseArgs(inv.args)
		return types.NewNull(), true
	default:
		if m, def, ok := vm.findMethod(inv.classID, inv.method); ok {
			called := inv.classID
			if inv.kind == callMethod && inv.hasObj {
				called = types.ObjectInfo(vm.Zone, types.Deref(vm.Zone, inv.obj)).ClassID
			}
			ret, threw := vm.invokeOpArray(m.Func, inv.obj, inv.hasObj, inv.args, called, def, line)
			vm.releaseSpecObj(inv)
			if !threw && !vm.stop {
				vm.control = false
			}
			return ret, threw || vm.stop
		}
		vm.releaseArgs(inv.args)
		vm.releaseSpecObj(inv)
		return types.NewNull(), true
	}
}

// invokeNativeNested exécute depuis un rappel une fonction native que doNative seul sait servir (intrinsèque de la
// VM ou d'extension, error_reporting). doNative opère comme une instruction d'appel : il empile son résultat et
// avance le compteur ordinal du cadre courant. Un cadre relais sans code, au nom de la fonction et au fichier de
// l'appelant, reçoit donc ce résultat et ce pas ; un cadre en ligne empilé par l'intrinsèque (call_user_func
// d'une fonction utilisateur) s'exécute au-dessus de lui. Une exception remonte comme pour invokeOpArray.
func (vm *VM) invokeNativeNested(inv callSpec, line uint32) (types.Value, bool) {
	inv.icPC = -1
	inv.flags |= bytecode.FlagNative
	// Le nom peut venir de l'arène et finir dans une entrée de trace qui survit à l'instruction.
	relay := &bytecode.OpArray{Name: strings.Clone(inv.method), MaxStack: 2, CVMetaResolved: true}
	if len(vm.frames) > 0 && vm.frame().fn != nil {
		relay.Filename = vm.frame().fn.Filename
	}
	if !vm.pushFrame(relay, 0, 0, types.Value{}, false, line) {
		vm.releaseArgs(inv.args)
		return types.NewUndef(), true
	}
	depth := len(vm.frames)
	vm.doNative(inv, line)
	if len(vm.frames) > depth && !vm.stop {
		vm.execFrames(depth)
	}
	if vm.stop {
		return types.NewUndef(), true
	}
	if len(vm.frames) == depth && !vm.hasThrow {
		fr := vm.frame()
		val := types.NewNull()
		if fr.evalTop > fr.evalBase {
			val = vm.pop()
		}
		vm.releaseFrameValues()
		vm.popFrame()
		vm.control = false
		return val, false
	}
	return vm.takeReturn()
}
