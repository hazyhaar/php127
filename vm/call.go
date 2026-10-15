package vm

import (
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func (vm *VM) opInit(op bytecode.Opcode, in bytecode.Instr) {
	fr := vm.frame()
	fn := fr.fn
	spec := callSpec{flags: in.Flags, line: in.Line, icPC: -1}
	switch op {
	case bytecode.OpInitCall:
		if in.Flags&bytecode.FlagClosure != 0 {
			obj := vm.pop()
			spec.kind = callClosure
			spec.obj = obj
			spec.hasObj = true
		} else {
			spec.kind = callFunc
			spec.funcIndex = int(in.Op1)
			spec.icPC = int32(fr.pc)
			if in.Flags&bytecode.FlagNative != 0 {
				spec.method = fn.Strings[in.Op1]
			}
		}
	case bytecode.OpInitMethodCall:
		spec.kind = callMethod
		spec.method, _ = vm.resolvePropName(fr, in.Op1)
		if in.Flags&bytecode.FlagUseThis != 0 {
			if !vm.frame().hasThis {
				vm.raiseBuiltin(bytecode.ClassError, "Using $this when not in object context", 0)
				return
			}
			spec.obj = types.RetainValue(vm.Zone, vm.frame().this)
			spec.hasObj = true
			spec.classID = types.ObjectInfo(vm.Zone, spec.obj).ClassID
		} else {
			obj := vm.pop()
			spec.obj = obj
			spec.hasObj = true
			if types.Deref(vm.Zone, obj).Type != types.TypeObject {
				types.ReleaseValue(vm.Zone, &obj)
				vm.raiseBuiltin(bytecode.ClassError, "Call to a member function "+spec.method+"() on non-object", 0)
				return
			}
			spec.classID = types.ObjectInfo(vm.Zone, types.Deref(vm.Zone, obj)).ClassID
		}
		if in.Flags&bytecode.FlagParent != 0 {
			spec.classID = vm.parentOf(vm.frame().scopeClass)
		}
	case bytecode.OpInitStaticMethodCall:
		spec.kind = callStatic
		spec.method, _ = vm.resolvePropName(fr, in.Op1)
		spec.classID = vm.resolveCallClass(in)
		if in.Flags&bytecode.FlagUseThis != 0 && vm.frame().hasThis {
			spec.obj = types.RetainValue(vm.Zone, vm.frame().this)
			spec.hasObj = true
		}
	case bytecode.OpInitNew:
		spec.kind = callNew
		spec.classID = in.Op1
		// new self, new parent et new static désignent la classe de portée, sa parente ou la classe appelée du cadre courant.
		switch {
		case in.Flags&bytecode.FlagSelf != 0:
			spec.classID = fr.scopeClass
		case in.Flags&bytecode.FlagParent != 0:
			spec.classID = vm.parentOf(fr.scopeClass)
		case in.Flags&bytecode.FlagStatic != 0:
			spec.classID = fr.calledClass
		}
		spec.method = "__construct"
	}
	if vm.stop {
		return
	}
	vm.pending = append(vm.pending, spec)
	vm.bump()
}

func (vm *VM) resolveCallClass(in bytecode.Instr) uint32 {
	fr := vm.frame()
	switch {
	case in.Flags&bytecode.FlagParent != 0:
		return vm.parentOf(fr.scopeClass)
	case in.Flags&bytecode.FlagSelf != 0:
		return fr.scopeClass
	case in.Flags&bytecode.FlagStatic != 0:
		return fr.calledClass
	default:
		return in.Op2
	}
}

func (vm *VM) parentOf(id uint32) uint32 {
	if c := vm.classByID(id); c != nil {
		return c.ParentID
	}
	return bytecode.ClassNone
}

func (vm *VM) allocArgs() []types.Value {
	if n := len(vm.argPool); n > 0 {
		a := vm.argPool[n-1]
		vm.argPool = vm.argPool[:n-1]
		return a[:0]
	}
	return make([]types.Value, 0, 8)
}

func (vm *VM) opSend(byRef bool) {
	if len(vm.pending) == 0 {
		vm.fatalFeature("envoi d'argument")
		return
	}
	v := vm.pop()
	if byRef && v.Type != types.TypeReference {
		r, err := types.NewReference(vm.Zone, v)
		types.ReleaseValue(vm.Zone, &v)
		if err != nil {
			vm.fatalFeature("référence")
			return
		}
		v = r
	}
	top := &vm.pending[len(vm.pending)-1]
	if top.args == nil {
		top.args = vm.allocArgs()
		top.pooledArgs = true
	}
	top.args = append(top.args, v)
	vm.bump()
}

func (vm *VM) opSendNamed(idx uint32) {
	if len(vm.pending) == 0 {
		vm.fatalFeature("argument nommé")
		return
	}
	v := vm.pop()
	name := vm.frame().fn.Strings[idx]
	top := &vm.pending[len(vm.pending)-1]
	top.named = append(top.named, namedArg{name: name, val: v})
	vm.bump()
}

func (vm *VM) opSendUnpack() {
	if len(vm.pending) == 0 {
		vm.fatalFeature("dépaquetage")
		return
	}
	arr := vm.pop()
	top := &vm.pending[len(vm.pending)-1]
	if top.args == nil {
		top.args = vm.allocArgs()
		top.pooledArgs = true
	}
	types.ArrayForEach(vm.Zone, arr, func(_, v types.Value) bool {
		top.args = append(top.args, types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)))
		return true
	})
	types.ReleaseValue(vm.Zone, &arr)
	vm.bump()
}

func (vm *VM) opDoCall() {
	if len(vm.pending) == 0 {
		vm.fatalFeature("appel")
		return
	}
	spec := vm.pending[len(vm.pending)-1]
	vm.pending = vm.pending[:len(vm.pending)-1]
	origArgs := spec.args
	pooled := spec.pooledArgs
	line := vm.line
	if spec.line != 0 {
		line = spec.line
	}
	switch spec.kind {
	case callNew:
		vm.doNew(spec, line)
	case callClosure:
		vm.doClosure(spec, line)
	case callFunc:
		vm.doFunc(spec, line)
	default:
		vm.doMethod(spec, line)
	}
	if pooled && origArgs != nil {
		clear(origArgs)
		vm.argPool = append(vm.argPool, origArgs[:0])
	}
}

func nativeNeedsSync(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(name, `\`)) {
	case "session_write_close", "session_commit", "session_encode",
		"session_start", "session_reset", "session_unset", "session_decode", "session_destroy",
		"stream_wrapper_register", "stream_register_wrapper":
		return true
	default:
		return false
	}
}

func isPotentialIntrinsic(name string) bool {
	clean := strings.ToLower(strings.TrimPrefix(name, `\`))
	return intrinsicExists(clean) || clean == "count" || clean == "sizeof" || clean == "var_dump" || clean == "strlen" || clean == "array_key_exists" || clean == "key_exists"
}

func (vm *VM) doFunc(spec callSpec, line uint32) {
	if spec.flags&bytecode.FlagNative != 0 {
		funcsLen := 0
		if vm.Program != nil {
			funcsLen = len(vm.Program.Funcs)
		}
		fr := vm.frame()
		ccache := vm.callCache(fr.fn)
		if spec.icPC >= 0 && int(spec.icPC) < len(ccache) {
			ic := ccache[spec.icPC]
			if ic.Kind != 0 && ic.ProgFuncsLen == funcsLen {
				if ic.Kind == 1 {
					spec.flags &^= bytecode.FlagNative
					spec.funcIndex = int(ic.FuncIdx)
				} else if ic.Kind == 2 && len(spec.named) == 0 {
					var argBuf [8]types.Value
					var args []types.Value
					if len(spec.args) <= len(argBuf) {
						args = argBuf[:len(spec.args)]
					} else {
						args = make([]types.Value, len(spec.args))
					}
					if ic.NativeNoRef {
						for i, arg := range spec.args {
							if arg.Type == types.TypeReference {
								args[i] = types.Deref(vm.Zone, arg)
							} else {
								args[i] = arg
							}
						}
					} else {
						for i, arg := range spec.args {
							if bytecode.NativeArgIsRef(spec.method, i) && arg.Type == types.TypeReference {
								vm.separateRefArray(arg)
								args[i] = arg
							} else {
								args[i] = types.Deref(vm.Zone, arg)
							}
						}
					}
					if ic.NativeSync {
						vm.preNativeCallSync(spec.method)
					}
					val, err := ic.Native(vm.Zone, vm.Out, args)
					if err != nil {
						vmRelease(vm.Zone, &val)
						vm.raiseNativeError(err, spec.method, args, line)
						vm.releaseArgs(spec.args)
						return
					}
					if ic.NativeSync {
						vm.postNativeCallSync(spec.method)
					}
					vm.releaseArgs(spec.args)
					vm.push(val)
					vm.bump()
					return
				}
			}
		}
		if spec.flags&bytecode.FlagNative != 0 {
			for i, fn := range vm.Program.Funcs {
				if fn != nil && !bytecode.AnonymousFunc(fn.Name) && strings.EqualFold(fn.Name, spec.method) {
					spec.flags &^= bytecode.FlagNative
					spec.funcIndex = i
					if spec.icPC >= 0 {
						if c := vm.ensureCallCache(fr.fn); int(spec.icPC) < len(c) {
							c[spec.icPC] = bytecode.CallIC{
								ProgFuncsLen: funcsLen,
								FuncIdx:      int32(i),
								Kind:         1,
							}
						}
					}
					break
				}
			}
		}
	}
	if spec.flags&bytecode.FlagNative != 0 {
		vm.doNative(spec, line)
		return
	}
	if vm.Program == nil || spec.funcIndex < 0 || spec.funcIndex >= len(vm.Program.Funcs) {
		vm.releaseArgs(spec.args)
		vm.raiseBuiltin(bytecode.ClassError, "Call to undefined function", 0)
		return
	}
	fn := vm.Program.Funcs[spec.funcIndex]
	if len(spec.named) > 0 && !vm.bindNamed(&spec, fn) {
		return
	}
	if fn.FastCall && len(spec.args) == len(fn.Params) {
		vm.bump()
		if !vm.pushFastFrame(fn, 0, 0, types.Value{}, false, line, true) {
			vm.releaseArgs(spec.args)
			return
		}
		fr := vm.frame()
		for i, v := range spec.args {
			slot := &vm.slots[fr.cvBase+i]
			if v.Type <= types.TypeDouble {
				*slot = v
			} else if v.Type == types.TypeReference {
				*slot = vmRetain(vm.Zone, types.Deref(vm.Zone, v))
				vmRelease(vm.Zone, &v)
			} else {
				*slot = v
			}
		}
		return
	}
	val, threw := vm.invokeOpArray(fn, types.Value{}, false, spec.args, 0, 0, line)
	if vm.stop || threw {
		return
	}
	vm.control = false
	vm.push(val)
	vm.bump()
}

// doNative exécute l'appel d'une fonction native désignée par son nom : intrinsèque de la VM, intrinsèque
// d'extension, error_reporting, puis fonction des bibliothèques natives. Le résultat est empilé et le compteur
// ordinal avancé, comme pour toute instruction d'appel.
func (vm *VM) doNative(spec callSpec, line uint32) {
	if len(spec.named) > 0 {
		for _, arg := range spec.named {
			v := arg.val
			vmRelease(vm.Zone, &v)
		}
		vm.releaseArgs(spec.args)
		vm.fatalFeature("arguments nommes de fonction native")
		return
	}
	if vm.intrinsic(spec, line) {
		vm.releaseArgs(spec.args)
		return
	}
	if vm.extIntrinsic(spec, line) {
		return
	}
	if strings.EqualFold(spec.method, "error_reporting") {
		if len(spec.args) > 1 {
			vm.releaseArgs(spec.args)
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, "error_reporting() expects at most 1 argument, "+strconv.Itoa(len(spec.args))+" given", 0)
			return
		}
		old := vm.effectiveErrorReporting()
		if len(spec.args) == 1 {
			arg := types.Deref(vm.Zone, spec.args[0])
			if arg.Type != types.TypeNull {
				vm.errorReporting = vm.toLong(arg)
			}
		}
		vm.releaseArgs(spec.args)
		vm.push(types.NewLong(old))
		vm.bump()
		return
	}
	native, ok := lookupNative(spec.method)
	if !ok {
		vm.releaseArgs(spec.args)
		vm.raiseBuiltin(bytecode.ClassError, "Call to undefined function "+spec.method+"()", 0)
		return
	}
	if spec.icPC >= 0 && !isPotentialIntrinsic(spec.method) {
		fr := vm.frame()
		if c := vm.ensureCallCache(fr.fn); int(spec.icPC) < len(c) {
			funcsLen := 0
			if vm.Program != nil {
				funcsLen = len(vm.Program.Funcs)
			}
			c[spec.icPC] = bytecode.CallIC{
				ProgFuncsLen: funcsLen,
				FuncIdx:      -1,
				Kind:         2,
				NativeNoRef:  !bytecode.NativeHasRefArgs(spec.method),
				NativeSync:   nativeNeedsSync(spec.method),
				Native:       native,
			}
		}
	}
	var argBuf [8]types.Value
	var args []types.Value
	if len(spec.args) <= len(argBuf) {
		args = argBuf[:len(spec.args)]
	} else {
		args = make([]types.Value, len(spec.args))
	}
	for i, arg := range spec.args {
		if bytecode.NativeArgIsRef(spec.method, i) && arg.Type == types.TypeReference {
			vm.separateRefArray(arg)
			args[i] = arg
		} else {
			args[i] = types.Deref(vm.Zone, arg)
		}
	}
	vm.preNativeCallSync(spec.method)
	val, err := native(vm.Zone, vm.Out, args)
	if err != nil {
		vmRelease(vm.Zone, &val)
		vm.raiseNativeError(err, spec.method, args, line)
		vm.releaseArgs(spec.args)
		return
	}
	vm.postNativeCallSync(spec.method)
	vm.releaseArgs(spec.args)
	vm.push(val)
	vm.bump()
}

// separateRefArray rend exclusif le tableau désigné par une référence transmise à une fonction native, comme la
// séparation de PHP avant l'écriture d'un paramètre &$array : un tableau partagé par copie sur écriture est
// dupliqué dans l'arène, curseur interne compris, et la référence reçoit la copie ; l'autre détenteur garde
// l'original intact.
func (vm *VM) separateRefArray(ref types.Value) {
	target := types.Deref(vm.Zone, ref)
	if target.Type != types.TypeArray || types.ArrayInfo(vm.Zone, target).RefCount <= 1 {
		return
	}
	// SeparateArray suit la référence et remplace la cible de sa cellule par la copie, tableau vide compris.
	cell := ref
	_ = types.SeparateArray(vm.Zone, &cell)
}

func (vm *VM) traceArgs(args []types.Value) string {
	var text strings.Builder
	for i, arg := range args {
		if i > 0 {
			text.WriteString(", ")
		}
		// Un paramètre par référence se décrit par sa valeur ; la trace n'exécute jamais de code utilisateur.
		arg = types.Deref(vm.Zone, arg)
		switch arg.Type {
		case types.TypeNull, types.TypeUndef:
			text.WriteString("NULL")
		case types.TypeString:
			s := types.StringData(vm.Zone, arg)
			text.WriteByte('\'')
			for _, b := range []byte(s[:min(len(s), 15)]) {
				if b < 32 || b > 126 {
					text.WriteString("\\x")
					const hex = "0123456789ABCDEF"
					text.WriteByte(hex[b>>4])
					text.WriteByte(hex[b&15])
				} else {
					text.WriteByte(b)
				}
			}
			if len(s) > 15 {
				text.WriteString("...")
			}
			text.WriteByte('\'')
		case types.TypeArray:
			text.WriteString("Array")
		case types.TypeObject:
			text.WriteString("Object(" + displayClassName(vm.className(types.ObjectInfo(vm.Zone, arg).ClassID)) + ")")
		case types.TypeFalse, types.TypeTrue:
			text.WriteString(strconv.FormatBool(arg.Type == types.TypeTrue))
		case types.TypeLong:
			text.WriteString(strconv.FormatInt(int64(arg.Scalar), 10))
		case types.TypeDouble:
			text.WriteString(formatFloat(arg.ToDouble()))
		case types.TypeResource:
			text.WriteString("Resource id #" + strconv.FormatUint(uint64(arg.DataSlot), 10))
		default:
			text.WriteString("Unknown")
		}
	}
	return text.String()
}

func (vm *VM) doMethod(spec callSpec, line uint32) {
	if vm.generatorMethod(spec) {
		return
	}
	if vm.enumMethod(spec) {
		return
	}
	called := spec.classID
	if spec.kind == callMethod && spec.hasObj {
		called = types.ObjectInfo(vm.Zone, types.Deref(vm.Zone, spec.obj)).ClassID
	}
	if spec.flags&bytecode.FlagParent != 0 || spec.flags&bytecode.FlagSelf != 0 || spec.flags&bytecode.FlagStatic != 0 {
		if spec.kind == callStatic && spec.flags&bytecode.FlagParent == 0 && spec.flags&bytecode.FlagSelf == 0 {
			called = vm.frame().calledClass
		} else if spec.kind == callStatic {
			called = vm.frame().calledClass
		}
	}
	if spec.kind == callStatic && spec.flags&(bytecode.FlagParent|bytecode.FlagSelf|bytecode.FlagStatic) != 0 {
		called = vm.frame().calledClass
	}
	if spec.kind == callStatic && spec.flags&(bytecode.FlagParent|bytecode.FlagSelf|bytecode.FlagStatic) == 0 {
		called = spec.classID
	}
	lookup := spec.classID
	if m, def, ok := vm.findMethod(lookup, spec.method); ok {
		if !vm.bindNamed(&spec, m.Func) {
			vm.releaseSpecObj(spec)
			return
		}
		if vm.visible(m.Visibility, def) {
			scope := def
			val, threw := vm.invokeOpArray(m.Func, spec.obj, spec.hasObj, spec.args, called, scope, line)
			vm.releaseSpecObj(spec)
			if vm.stop || threw {
				return
			}
			vm.control = false
			vm.push(val)
			vm.bump()
			return
		}
		if !vm.tryMagicCall(spec, lookup, called, line) {
			vm.releaseArgs(spec.args)
			vm.releaseSpecObj(spec)
			vm.raiseBuiltin(bytecode.ClassError, "Call to "+vm.visName(m.Visibility)+" method "+vm.className(def)+"::"+spec.method+"()", 0)
		}
		return
	}
	if vm.nativeClassMethod(spec, called, line) {
		return
	}
	if vm.callNative(spec, called, line) {
		return
	}
	if vm.tryMagicCall(spec, lookup, called, line) {
		return
	}
	vm.releaseArgs(spec.args)
	vm.releaseSpecObj(spec)
	vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method "+vm.className(lookup)+"::"+spec.method+"()", 0)
}

func (vm *VM) tryMagicCall(spec callSpec, lookup, called, line uint32) bool {
	magicName := "__call"
	if spec.kind == callStatic {
		magicName = "__callStatic"
	}
	m, def, ok := vm.findMethod(lookup, magicName)
	if !ok {
		return false
	}
	nameVal, err := types.NewStringLiteral(vm.Zone, spec.method)
	if err != nil {
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
		vm.fatalFeature("chaîne")
		return true
	}
	argsArr, err := types.NewArray(vm.Zone)
	if err != nil {
		types.ReleaseValue(vm.Zone, &nameVal)
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
		vm.fatalFeature("tableau")
		return true
	}
	for _, a := range spec.args {
		_ = types.ArrayAppend(vm.Zone, &argsArr, types.Deref(vm.Zone, a))
	}
	vm.releaseArgs(spec.args)
	for _, na := range spec.named {
		k, kerr := types.NewStringLiteral(vm.Zone, na.name)
		if kerr == nil {
			_ = types.ArraySet(vm.Zone, &argsArr, k, types.Deref(vm.Zone, na.val), nil)
			types.ReleaseValue(vm.Zone, &k)
		}
		v := na.val
		vmRelease(vm.Zone, &v)
	}
	var val types.Value
	var threw bool
	if spec.kind == callStatic {
		val, threw = vm.invokeOpArray(m.Func, types.Value{}, false, []types.Value{nameVal, argsArr}, called, def, line)
	} else {
		val, threw = vm.invokeOpArray(m.Func, spec.obj, spec.hasObj, []types.Value{nameVal, argsArr}, called, def, line)
	}
	vm.releaseSpecObj(spec)
	if vm.stop || threw {
		return true
	}
	vm.control = false
	vm.push(val)
	vm.bump()
	return true
}

func (vm *VM) doNew(spec callSpec, line uint32) {
	classID := spec.classID
	if vm.doNativeNew(classID, spec, line) {
		return
	}
	c := vm.classByID(classID)
	if c != nil && c.Kind == bytecode.KindEnum {
		vm.releaseArgs(spec.args)
		vm.raiseBuiltin(bytecode.ClassError, "Cannot instantiate enum "+c.Name, 0)
		return
	}
	if c == nil && classID >= bytecode.ClassUser {
		vm.releaseArgs(spec.args)
		vm.raiseBuiltin(bytecode.ClassError, "Class not found", 0)
		return
	}
	var props uint32
	var hasDtor bool
	if c != nil && c.PropsResolved {
		props = c.InstancePropCount
		hasDtor = c.HasDestructor
	} else {
		props = vm.instancePropCount(classID)
		hasDtor = vm.hasMethod(classID, "__destruct")
	}
	obj, err := types.NewObject(vm.Zone, classID, props, hasDtor)
	if err != nil {
		vm.releaseArgs(spec.args)
		vm.fatalFeature("objet")
		return
	}
	vm.initProps(classID, obj)
	if vm.stop || vm.control {
		// Une valeur initiale différée a levé une exception : l'instanciation est abandonnée.
		vm.releaseArgs(spec.args)
		types.ReleaseValue(vm.Zone, &obj)
		return
	}
	if m, _, ok := vm.findMethod(classID, "__construct"); ok {
		if !vm.bindNamed(&spec, m.Func) {
			types.ReleaseValue(vm.Zone, &obj)
			return
		}
		ret, threw := vm.invokeOpArray(m.Func, obj, true, spec.args, classID, m.ClassID, line)
		if threw || vm.stop {
			types.ReleaseValue(vm.Zone, &obj)
			return
		}
		types.ReleaseValue(vm.Zone, &ret)
		vm.control = false
	} else if vm.isExceptionFamily(classID) || classID == bytecode.ClassException || classID == bytecode.ClassError {
		vm.nativeConstruct(obj, spec.args, line)
	} else {
		vm.releaseArgs(spec.args)
	}
	if vm.stop {
		types.ReleaseValue(vm.Zone, &obj)
		return
	}
	vm.push(obj)
	vm.bump()
}

func (vm *VM) doClosure(spec callSpec, line uint32) {
	obj := types.Deref(vm.Zone, spec.obj)
	if obj.Type != types.TypeObject || types.ObjectInfo(vm.Zone, obj).ClassID != bytecode.ClassClosure {
		call, ok := vm.callback(obj)
		if ok {
			call.args, call.named = spec.args, spec.named
			if call.hasObj {
				call.obj = types.RetainValue(vm.Zone, call.obj)
			}
			types.ReleaseValue(vm.Zone, &spec.obj)
			if call.kind == callFunc {
				vm.doFunc(call, line)
			} else {
				vm.doMethod(call, line)
			}
			return
		}
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
		vm.raiseBuiltin(bytecode.ClassError, "Value is not callable", 0)
		return
	}
	idx := int(types.ObjectGet(vm.Zone, obj, 0).ToLong())
	if idx < 0 || idx >= len(vm.closures) || !vm.closures[idx].live {
		vm.releaseArgs(spec.args)
		types.ReleaseValue(vm.Zone, &spec.obj)
		vm.fatalFeature("fermeture")
		return
	}
	data := vm.closures[idx]
	if !vm.bindNamed(&spec, data.fn) {
		types.ReleaseValue(vm.Zone, &spec.obj)
		return
	}
	val, threw := vm.invokeClosure(data, spec.args, line)
	types.ReleaseValue(vm.Zone, &spec.obj)
	if vm.stop || threw {
		return
	}
	vm.control = false
	vm.push(val)
	vm.bump()
}

func (vm *VM) invokeClosure(data closureData, args []types.Value, line uint32) (types.Value, bool) {
	if !vm.checkArgs(data.fn, args, 0, 0) {
		return types.NewUndef(), true
	}
	if !vm.pushFrame(data.fn, 0, 0, types.Value{}, false, line) {
		vm.releaseArgs(args)
		return types.NewUndef(), true
	}
	fr := vm.frame()
	for i, u := range data.fn.Uses {
		if i >= len(data.uses) {
			break
		}
		vm.slots[fr.cvBase+int(u.CV)] = types.RetainValue(vm.Zone, data.uses[i])
	}
	depth := len(vm.frames)
	vm.bindParams(data.fn, args)
	if vm.stop || vm.hasThrow || len(vm.frames) < depth {
		return vm.takeReturn()
	}
	if data.fn.IsGenerator {
		return vm.newGenerator(), false
	}
	base := len(vm.frames) - 1
	vm.execFrames(base)
	return vm.takeReturn()
}

func (vm *VM) invokeOpArray(fn *bytecode.OpArray, this types.Value, hasThis bool, args []types.Value, called, scope uint32, callLine uint32) (types.Value, bool) {
	if (fn.HasTypedParams || !fn.CVMetaResolved) && !vm.checkArgs(fn, args, scope, called) {
		return types.NewUndef(), true
	}
	if fn.FastCall {
		if !vm.pushFastFrame(fn, called, scope, this, hasThis, callLine, false) {
			vm.releaseArgs(args)
			return types.NewUndef(), true
		}
	} else if !vm.pushFrame(fn, called, scope, this, hasThis, callLine) {
		vm.releaseArgs(args)
		return types.NewUndef(), true
	}
	depth := len(vm.frames)
	vm.bindParams(fn, args)
	if vm.stop || vm.hasThrow || len(vm.frames) < depth {
		return vm.takeReturn()
	}
	if fn.IsGenerator {
		return vm.newGenerator(), false
	}
	base := len(vm.frames) - 1
	vm.execFrames(base)
	return vm.takeReturn()
}

func (vm *VM) takeReturn() (types.Value, bool) {
	if vm.stop {
		return types.NewUndef(), true
	}
	if vm.hasThrow {
		obj := vm.thrown
		vm.hasThrow = false
		vm.thrown = types.Value{}
		if len(vm.frames) == 0 {
			vm.uncaught(obj)
			return types.NewUndef(), true
		}
		vm.throwAt(vm.frame().pc, obj)
		return types.NewUndef(), true
	}
	if vm.hasRet {
		v := vm.retVal
		vm.hasRet = false
		vm.retVal = types.Value{}
		return v, false
	}
	return types.NewNull(), false
}

func (vm *VM) bindParams(fn *bytecode.OpArray, args []types.Value) {
	fr := vm.frame()
	if fn.FastCall && len(args) == len(fn.Params) {
		for i, v := range args {
			slot := &vm.slots[fr.cvBase+i]
			if v.Type <= types.TypeDouble {
				*slot = v
			} else if v.Type == types.TypeReference {
				*slot = vmRetain(vm.Zone, types.Deref(vm.Zone, v))
				vmRelease(vm.Zone, &v)
			} else {
				*slot = v
			}
		}
		return
	}
	if (fn.UsesVarArgs || !fn.CVMetaResolved) && len(args) > 0 {
		fr.args = make([]types.Value, len(args))
		for i, arg := range args {
			if i >= len(fn.Params) || fn.Params[i].Variadic {
				fr.args[i] = vmRetain(vm.Zone, types.Deref(vm.Zone, arg))
			}
		}
	}
	pos := 0
	for i, p := range fn.Params {
		if i >= fn.NumCVs {
			break
		}
		slot := &vm.slots[fr.cvBase+i]
		if p.Variadic {
			arr, err := types.NewArray(vm.Zone)
			if err != nil {
				vm.fatalFeature("tableau")
				return
			}
			for pos < len(args) {
				_ = types.ArrayAppend(vm.Zone, &arr, args[pos])
				vmRelease(vm.Zone, &args[pos])
				pos++
			}
			if slot.Type != types.TypeUndef {
				vmRelease(vm.Zone, slot)
			}
			*slot = arr
			continue
		}
		if pos < len(args) && args[pos].Type != types.TypeUndef {
			v := args[pos]
			pos++
			if p.ByRef {
				if slot.Type != types.TypeUndef {
					vmRelease(vm.Zone, slot)
				}
				*slot = v
				continue
			}
			if v.Type <= types.TypeDouble && slot.Type <= types.TypeDouble {
				*slot = v
				continue
			}
			if v.Type != types.TypeReference && slot.Type <= types.TypeDouble {
				*slot = v
				continue
			}
			owned := vmRetain(vm.Zone, types.Deref(vm.Zone, v))
			vmRelease(vm.Zone, &v)
			if slot.Type != types.TypeUndef {
				vmRelease(vm.Zone, slot)
			}
			*slot = owned
			continue
		}
		if pos < len(args) {
			pos++
		}
		if p.DefaultConst != nil {
			if slot.Type != types.TypeUndef {
				vmRelease(vm.Zone, slot)
			}
			*slot = vm.constantValue(p.DefaultConst)
			if vm.stop || vm.control {
				return
			}
			continue
		}
		if p.HasDefaultString {
			s, err := types.NewStringLiteral(vm.Zone, p.DefaultString)
			if err != nil {
				vm.fatalFeature("argument par défaut")
				return
			}
			if slot.Type != types.TypeUndef {
				vmRelease(vm.Zone, slot)
			}
			*slot = s
			continue
		}
		if p.HasDefault {
			if slot.Type != types.TypeUndef {
				vmRelease(vm.Zone, slot)
			}
			*slot = vmRetain(vm.Zone, p.Default)
			continue
		}
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, "Too few arguments to function "+fn.Name+"(), "+strconv.Itoa(len(args))+" passed", 0)
		return
	}
	for pos < len(args) {
		vmRelease(vm.Zone, &args[pos])
		pos++
	}
}

func (vm *VM) bindNamed(spec *callSpec, fn *bytecode.OpArray) bool {
	for i, arg := range spec.named {
		idx := -1
		for j, p := range fn.Params {
			if p.Name == arg.name {
				idx = j
				break
			}
		}
		message := ""
		if idx < 0 {
			message = "Unknown named parameter $" + arg.name
		} else if idx < len(spec.args) && spec.args[idx].Type != types.TypeUndef {
			message = "Named parameter $" + arg.name + " overwrites previous argument"
		}
		if message != "" {
			vm.releaseArgs(spec.args)
			for _, remaining := range spec.named[i:] {
				v := remaining.val
				vmRelease(vm.Zone, &v)
			}
			vm.raiseBuiltin(bytecode.ClassError, message, 0)
			return false
		}
		for len(spec.args) <= idx {
			spec.args = append(spec.args, types.NewUndef())
		}
		spec.args[idx] = arg.val
	}
	spec.named = nil
	return true
}

func (vm *VM) releaseArgs(args []types.Value) {
	for i := range args {
		vmRelease(vm.Zone, &args[i])
	}
}

func (vm *VM) releaseSpecObj(spec callSpec) {
	if spec.hasObj {
		types.ReleaseValue(vm.Zone, &spec.obj)
	}
}

func (vm *VM) callNative(spec callSpec, called uint32, line uint32) bool {
	obj := types.Deref(vm.Zone, spec.obj)
	if obj.Type != types.TypeObject {
		return false
	}
	id := types.ObjectInfo(vm.Zone, obj).ClassID
	if !vm.isExceptionFamily(id) && id != bytecode.ClassException && id != bytecode.ClassError {
		return false
	}
	switch spec.method {
	case "getMessage":
		vm.releaseArgs(spec.args)
		vm.push(types.RetainValue(vm.Zone, types.ObjectGet(vm.Zone, obj, 0)))
		vm.releaseSpecObj(spec)
		vm.bump()
		return true
	case "getCode":
		vm.releaseArgs(spec.args)
		vm.push(types.ObjectGet(vm.Zone, obj, 1))
		vm.releaseSpecObj(spec)
		vm.bump()
		return true
	case "getFile":
		vm.releaseArgs(spec.args)
		vm.push(types.RetainValue(vm.Zone, types.ObjectGet(vm.Zone, obj, 2)))
		vm.releaseSpecObj(spec)
		vm.bump()
		return true
	case "getLine":
		vm.push(types.ObjectGet(vm.Zone, obj, 3))
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
		vm.bump()
		return true
	case "__construct":
		vm.nativeConstruct(obj, spec.args, line)
		vm.releaseSpecObj(spec)
		if !vm.stop {
			vm.push(types.NewNull())
			vm.bump()
		}
		return true
	default:
		return false
	}
}

func (vm *VM) nativeConstruct(obj types.Value, args []types.Value, line uint32) {
	msg := ""
	code := int64(0)
	if len(args) > 0 {
		s, own := vm.stringify(args[0])
		if s.Type == types.TypeString {
			msg = types.StringData(vm.Zone, s)
		}
		if own {
			types.ReleaseValue(vm.Zone, &s)
		}
	}
	if len(args) > 1 {
		code = vm.toLong(args[1])
	}
	vm.releaseArgs(args)
	vm.setExceptionProps(obj, msg, code, vm.file(), line)
}

func (vm *VM) setExceptionProps(obj types.Value, msg string, code int64, file string, line uint32) {
	ms, err := types.NewStringLiteral(vm.Zone, msg)
	if err != nil {
		vm.fatalFeature("exception")
		return
	}
	types.ObjectSet(vm.Zone, obj, 0, ms)
	types.ReleaseValue(vm.Zone, &ms)
	types.ObjectSet(vm.Zone, obj, 1, types.NewLong(code))
	fs, err := types.NewStringLiteral(vm.Zone, file)
	if err != nil {
		vm.fatalFeature("exception")
		return
	}
	types.ObjectSet(vm.Zone, obj, 2, fs)
	types.ReleaseValue(vm.Zone, &fs)
	types.ObjectSet(vm.Zone, obj, 3, types.NewLong(int64(line)))
}

func (vm *VM) opBindClosure(in bytecode.Instr) {
	if vm.Program == nil || int(in.Op1) >= len(vm.Program.Funcs) {
		vm.fatalFeature("fermeture")
		return
	}
	fn := vm.Program.Funcs[in.Op1]
	// Chaque fermeture possedant des variables statiques locales a besoin d'une identite distincte.
	if fn.HasStaticVars || !fn.CVMetaResolved {
		closureFn := *fn
		fn = &closureFn
	}
	n := int(in.Op2)
	var uses []types.Value
	if n > 0 {
		uses = make([]types.Value, n)
		for i := n - 1; i >= 0; i-- {
			uses[i] = vm.pop()
		}
	}
	obj, err := types.NewObject(vm.Zone, bytecode.ClassClosure, 1, true)
	if err != nil {
		for i := range uses {
			types.ReleaseValue(vm.Zone, &uses[i])
		}
		vm.fatalFeature("fermeture")
		return
	}
	idx := len(vm.closures)
	vm.closures = append(vm.closures, closureData{fn: fn, uses: uses, live: true})
	types.ObjectSet(vm.Zone, obj, 0, types.NewLong(int64(idx)))
	vm.push(obj)
	vm.bump()
}

func (vm *VM) releaseClosure(i int) {
	if i < 0 || i >= len(vm.closures) || !vm.closures[i].live {
		return
	}
	for j := range vm.closures[i].uses {
		types.ReleaseValue(vm.Zone, &vm.closures[i].uses[j])
	}
	vm.closures[i].live = false
	vm.closures[i].uses = nil
}

func (vm *VM) runDestructor(obj types.Value) {
	if obj.Type != types.TypeObject {
		return
	}
	info := types.ObjectInfo(vm.Zone, obj)
	if info.ClassID == bytecode.ClassGenerator {
		g := vm.generatorOf(obj)
		if g != nil {
			vm.closeGenerator(g)
		}
		return
	}
	if info.ClassID == bytecode.ClassClosure {
		if info.PropCount > 0 {
			idx := int(types.ObjectGet(vm.Zone, obj, 0).ToLong())
			vm.releaseClosure(idx)
		}
		return
	}
	m, _, ok := vm.findMethod(info.ClassID, "__destruct")
	if !ok || m.Func == nil {
		return
	}
	savedControl := vm.control
	savedLine := vm.line
	ret, threw := vm.invokeOpArray(m.Func, obj, true, nil, info.ClassID, m.ClassID, vm.line)
	types.ReleaseValue(vm.Zone, &ret)
	if !threw && !vm.stop {
		vm.control = savedControl
		vm.line = savedLine
	}
}

// funcArgByRef indique si l'argument en cours d'évaluation pour l'appel en attente le plus récent se transmet
// par référence, d'après la signature de l'appelé résolue une fois par appel.
func (vm *VM) funcArgByRef() bool {
	if len(vm.pending) == 0 {
		return false
	}
	spec := &vm.pending[len(vm.pending)-1]
	if !spec.refResolved {
		vm.resolveArgRefs(spec)
	}
	idx := len(spec.args)
	if spec.refNative != "" {
		return bytecode.NativeArgIsRef(spec.refNative, idx)
	}
	return paramByRef(spec.refParams, idx)
}

func paramByRef(params []bytecode.ParamInfo, idx int) bool {
	if idx < len(params) {
		return params[idx].ByRef
	}
	if n := len(params); n > 0 && params[n-1].Variadic {
		return params[n-1].ByRef
	}
	return false
}

// resolveArgRefs lit la signature de l'appelé d'un appel en attente : fonction utilisateur ou native, fermeture
// ou chaîne et tableau appelables, méthode, méthode statique ou constructeur.
func (vm *VM) resolveArgRefs(spec *callSpec) {
	spec.refResolved = true
	switch spec.kind {
	case callFunc:
		if spec.flags&bytecode.FlagNative == 0 {
			if spec.funcIndex >= 0 && spec.funcIndex < len(vm.Program.Funcs) && vm.Program.Funcs[spec.funcIndex] != nil {
				spec.refParams = vm.Program.Funcs[spec.funcIndex].Params
			}
			return
		}
		if fr := vm.frame(); spec.icPC >= 0 {
			if c := vm.callCache(fr.fn); int(spec.icPC) < len(c) {
				ic := c[spec.icPC]
				if ic.Kind == 2 && ic.NativeNoRef && ic.ProgFuncsLen == len(vm.Program.Funcs) {
					return
				}
			}
		}
		if i := vm.userFuncIndex(spec.method); i >= 0 && vm.Program.Funcs[i] != nil {
			spec.refParams = vm.Program.Funcs[i].Params
			return
		}
		if bytecode.NativeHasRefArgs(spec.method) {
			spec.refNative = spec.method
		}
	case callClosure:
		d := types.Deref(vm.Zone, spec.obj)
		if d.Type == types.TypeObject && types.ObjectInfo(vm.Zone, d).ClassID == bytecode.ClassClosure {
			idx := int(types.ObjectGet(vm.Zone, d, 0).ToLong())
			if idx >= 0 && idx < len(vm.closures) && vm.closures[idx].live && vm.closures[idx].fn != nil {
				spec.refParams = vm.closures[idx].fn.Params
			}
			return
		}
		if inner, ok := vm.callback(spec.obj); ok && inner.kind != callClosure {
			vm.resolveArgRefs(&inner)
			spec.refParams, spec.refNative = inner.refParams, inner.refNative
		}
	case callMethod, callStatic:
		if m, _, ok := vm.findMethod(spec.classID, spec.method); ok && m.Func != nil {
			spec.refParams = m.Func.Params
		}
	case callNew:
		if m, _, ok := vm.findMethod(spec.classID, "__construct"); ok && m.Func != nil {
			spec.refParams = m.Func.Params
		}
	}
}
