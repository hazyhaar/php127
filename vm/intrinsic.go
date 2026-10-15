package vm

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/spl"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func intrinsicExists(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(name, `\`)) {
	case "error_reporting", "func_num_args", "func_get_arg", "func_get_args",
		"define", "defined", "constant", "function_exists",
		"class_exists", "interface_exists", "trait_exists", "enum_exists",
		"is_callable", "call_user_func", "call_user_func_array",
		"compact", "extract", "get_class", "get_called_class",
		"array_map", "array_filter", "array_reduce", "array_walk", "array_walk_recursive",
		"usort", "uasort", "uksort", "preg_replace_callback",
		"ob_start", "ob_get_clean", "ob_get_contents", "ob_end_clean", "ob_end_flush",
		"ob_get_flush", "ob_clean", "ob_flush", "ob_get_length", "ob_get_level", "flush",
		"set_error_handler", "restore_error_handler",
		"set_exception_handler", "restore_exception_handler",
		"trigger_error", "user_error", "error_log", "debug_backtrace",
		"spl_autoload_register", "spl_autoload_unregister", "spl_autoload_functions",
		"gc_collect_cycles", "memory_get_usage", "memory_get_peak_usage", "ini_get", "ini_set",
		"method_exists", "property_exists", "is_a", "is_subclass_of",
		"get_parent_class", "get_class_methods", "get_object_vars",
		"is_iterable", "is_countable", "iterator_to_array", "iterator_count",
		"assert", "filter_input":
		return true
	}
	return false
}

func (vm *VM) intrinsic(spec callSpec, line uint32) bool {
	if vm.callbackIntrinsic(spec, line) {
		return true
	}
	name := strings.ToLower(strings.TrimPrefix(spec.method, `\`))
	switch name {
	case "strlen":
		if len(spec.args) == 1 && !vm.frame().fn.StrictTypes {
			d := types.Deref(vm.Zone, spec.args[0])
			if d.IsNull() || d.IsUndef() {
				if vm.errorReporting&8192 != 0 && vm.Out != nil {
					fmt.Fprintf(vm.Out, "\nDeprecated: strlen(): Passing null to parameter #1 ($string) of type string is deprecated in %s on line %d\n", vm.file(), line)
				}
				vm.push(types.NewLong(0))
				vm.bump()
				return true
			}
		}
		return false
	case "array_key_exists", "key_exists":
		if len(spec.args) == 2 {
			k := types.Deref(vm.Zone, spec.args[0])
			if k.Type == types.TypeDouble {
				n := k.ToLong()
				if k.ToDouble() != float64(n) {
					vm.deprecatedFloatKey(types.ArrayWarning{Float: k.ToDouble()})
				}
			}
		}
		return false
	case "compact":
		return vm.intrinsicCompact(spec, line)
	case "extract":
		return vm.intrinsicExtract(spec, line)
	case "get_class":
		return vm.intrinsicGetClass(spec, line)
	case "get_called_class", "get_parent_class", "method_exists", "property_exists",
		"is_a", "is_subclass_of", "get_class_methods", "get_object_vars",
		"is_iterable", "is_countable", "iterator_to_array", "iterator_count",
		"ob_start", "ob_get_clean", "ob_get_contents", "ob_end_clean", "ob_end_flush",
		"ob_get_flush", "ob_clean", "ob_flush", "ob_get_length", "ob_get_level", "flush",
		"set_error_handler", "restore_error_handler",
		"set_exception_handler", "restore_exception_handler",
		"trigger_error", "user_error", "error_log", "debug_backtrace",
		"spl_autoload_register", "spl_autoload_unregister", "spl_autoload_functions",
		"gc_collect_cycles", "memory_get_usage", "memory_get_peak_usage", "ini_get", "ini_set",
		"array_map", "array_filter", "array_reduce", "array_walk", "array_walk_recursive",
		"usort", "uasort", "uksort", "preg_replace_callback",
		"assert", "filter_input":
		return vm.intrinsicExtended(name, spec, line)
	}
	minArgs, maxArgs := 0, 0
	switch name {
	case "func_num_args", "func_get_args":
	case "func_get_arg", "defined", "constant", "function_exists":
		minArgs, maxArgs = 1, 1
	case "define":
		minArgs, maxArgs = 2, 2
	case "class_exists", "interface_exists", "trait_exists", "enum_exists":
		minArgs, maxArgs = 1, 2
	default:
		return false
	}
	fail := func(class uint32, msg string) {
		vm.raiseBuiltin(class, msg, 0, traceItem{file: vm.file(), line: line, name: name, args: vm.traceArgs(spec.args)})
	}
	if len(spec.args) < minArgs || len(spec.args) > maxArgs {
		how, n := "exactly", minArgs
		if minArgs != maxArgs {
			how = "at least"
			if len(spec.args) > maxArgs {
				how, n = "at most", maxArgs
			}
		}
		word := "arguments"
		if n == 1 {
			word = "argument"
		}
		fail(bytecode.ClassArgumentCountError, fmt.Sprintf("%s() expects %s %d %s, %d given", name, how, n, word, len(spec.args)))
		return true
	}
	var result types.Value
	if strings.HasPrefix(name, "func_") {
		fr := vm.frame()
		if len(vm.frames) == 1 {
			fail(bytecode.ClassError, name+"() must be called from a function context")
			return true
		}
		argument := func(i int) types.Value {
			if i < len(fr.fn.Params) && !fr.fn.Params[i].Variadic {
				v := types.Deref(vm.Zone, vm.slots[fr.cvBase+i])
				if v.IsUndef() {
					return types.NewNull()
				}
				return v
			}
			return fr.args[i]
		}
		switch name {
		case "func_num_args":
			result = types.NewLong(int64(len(fr.args)))
		case "func_get_arg":
			pos := vm.toLong(spec.args[0])
			if pos < 0 {
				fail(bytecode.ClassValueError, "func_get_arg(): Argument #1 ($position) must be greater than or equal to 0")
				return true
			}
			if pos >= int64(len(fr.args)) {
				fail(bytecode.ClassValueError, "func_get_arg(): Argument #1 ($position) must be less than the number of the arguments passed to the currently executed function")
				return true
			}
			result = types.RetainValue(vm.Zone, argument(int(pos)))
		case "func_get_args":
			var err error
			result, err = types.NewArray(vm.Zone)
			if err != nil {
				vm.fatalFeature("tableau d'arguments")
				return true
			}
			for i := range fr.args {
				if err := types.ArrayAppend(vm.Zone, &result, argument(i)); err != nil {
					types.ReleaseValue(vm.Zone, &result)
					vm.fatalFeature(err.Error())
					return true
				}
			}
		}
	} else {
		s, owned := vm.stringify(spec.args[0])
		key := types.StringData(vm.Zone, s)
		if owned {
			types.ReleaseValue(vm.Zone, &s)
		}
		if vm.stop || vm.control {
			return true
		}
		switch name {
		case "constant":
			result = vm.lookupConst(key)
		case "defined":
			v, ok := vm.findConst(key)
			types.ReleaseValue(vm.Zone, &v)
			result = types.NewBool(ok)
		case "define":
			v, exists := vm.findConst(key)
			types.ReleaseValue(vm.Zone, &v)
			if exists {
				if vm.errorReporting&2 != 0 {
					fmt.Fprintf(vm.Out, "\nWarning: Constant %s already defined in %s on line %d\n", key, vm.file(), line)
				}
			} else {
				if vm.constCache == nil {
					vm.constCache = make(map[string]types.Value)
				}
				vm.constCache[key] = types.RetainValue(vm.Zone, types.Deref(vm.Zone, spec.args[1]))
			}
			result = types.NewBool(!exists)
		case "function_exists":
			_, found := lookupNative(key)
			found = found || intrinsicExists(key)
			for _, fn := range vm.Program.Funcs {
				if strings.EqualFold(fn.Name, key) {
					found = true
					break
				}
			}
			result = types.NewBool(found)
		default:
			found := false
			kind := bytecode.KindClass
			if name == "interface_exists" {
				kind = bytecode.KindInterface
			}
			if name == "trait_exists" {
				kind = bytecode.KindTrait
			}
			if name == "enum_exists" {
				kind = bytecode.KindEnum
			}
			for _, cl := range append(append([]*bytecode.ClassProto(nil), vm.Program.Classes...), vm.Program.Traits...) {
				if (cl.Kind == kind || name == "class_exists" && cl.Kind == bytecode.KindEnum) && strings.EqualFold(cl.Name, key) {
					found = true
					break
				}
			}
			if id, ok := bytecode.BuiltinClassID(key); ok {
				if proto := builtinProto(id); proto != nil {
					found = proto.Kind == kind
				}
			}
			result = types.NewBool(found)
		}
	}
	if vm.stop || vm.control {
		types.ReleaseValue(vm.Zone, &result)
		return true
	}
	vm.push(result)
	vm.bump()
	return true
}

func (vm *VM) intrinsicCompact(spec callSpec, line uint32) bool {
	if len(spec.args) < 1 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, "compact() expects at least 1 argument, 0 given", 0, traceItem{file: vm.file(), line: line, name: "compact"})
		return true
	}
	result, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("compact")
		return true
	}
	var collect func(arg types.Value)
	collect = func(arg types.Value) {
		if vm.stop || vm.control {
			return
		}
		d := types.Deref(vm.Zone, arg)
		if d.Type == types.TypeArray {
			types.ArrayForEach(vm.Zone, d, func(_, v types.Value) bool {
				collect(v)
				return !vm.stop && !vm.control
			})
			return
		}
		if d.Type != types.TypeString {
			return
		}
		varName := types.StringData(vm.Zone, d)
		if varName == "this" {
			return
		}
		val, ok := vm.readVarByName(varName)
		if !ok {
			return
		}
		k, kerr := types.NewStringLiteral(vm.Zone, varName)
		if kerr == nil {
			_ = types.ArraySet(vm.Zone, &result, k, val, nil)
			types.ReleaseValue(vm.Zone, &k)
		}
		types.ReleaseValue(vm.Zone, &val)
	}
	for _, arg := range spec.args {
		collect(arg)
	}
	if vm.stop || vm.control {
		types.ReleaseValue(vm.Zone, &result)
		return true
	}
	vm.push(result)
	vm.bump()
	return true
}

func (vm *VM) intrinsicExtract(spec callSpec, line uint32) bool {
	if len(spec.args) < 1 || len(spec.args) > 3 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("extract() expects between 1 and 3 arguments, %d given", len(spec.args)), 0, traceItem{file: vm.file(), line: line, name: "extract"})
		return true
	}
	arr := types.Deref(vm.Zone, spec.args[0])
	if arr.Type != types.TypeArray {
		vm.raiseBuiltin(bytecode.ClassTypeError, "extract(): Argument #1 ($array) must be of type array", 0, traceItem{file: vm.file(), line: line, name: "extract"})
		return true
	}
	var count int64
	types.ArrayForEach(vm.Zone, arr, func(k, v types.Value) bool {
		if k.Type != types.TypeString {
			return true
		}
		varName := types.StringData(vm.Zone, k)
		if varName == "" || varName == "this" || (varName[0] >= '0' && varName[0] <= '9') {
			return true
		}
		fr := vm.frame()
		val := vmRetain(vm.Zone, types.Deref(vm.Zone, v))
		if idx, ok := vm.findCVIndex(fr.fn, varName); ok {
			vm.writeCV(idx, val, true)
		} else {
			if fr.dynamicLocals == nil {
				fr.dynamicLocals = make(map[string]types.Value)
			}
			old := fr.dynamicLocals[varName]
			fr.dynamicLocals[varName] = val
			if len(vm.frames) == 1 {
				oldG := vm.globals[varName]
				vm.globals[varName] = vmRetain(vm.Zone, val)
				vmRelease(vm.Zone, &oldG)
			}
			vmRelease(vm.Zone, &old)
		}
		count++
		return !vm.stop && !vm.control
	})
	if vm.stop || vm.control {
		return true
	}
	vm.push(types.NewLong(count))
	vm.bump()
	return true
}

func (vm *VM) intrinsicGetClass(spec callSpec, line uint32) bool {
	if len(spec.args) > 1 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("get_class() expects at most 1 argument, %d given", len(spec.args)), 0, traceItem{file: vm.file(), line: line, name: "get_class"})
		return true
	}
	var classID uint32
	if len(spec.args) == 0 {
		fr := vm.frame()
		if fr.hasThis && fr.this.Type == types.TypeObject {
			classID = types.ObjectInfo(vm.Zone, fr.this).ClassID
		} else if fr.scopeClass != 0 {
			classID = fr.scopeClass
		} else {
			vm.raiseBuiltin(bytecode.ClassError, "get_class() without arguments must be called from inside a class", 0, traceItem{file: vm.file(), line: line, name: "get_class"})
			return true
		}
	} else {
		d := types.Deref(vm.Zone, spec.args[0])
		if d.Type != types.TypeObject {
			vm.raiseBuiltin(bytecode.ClassTypeError, "get_class(): Argument #1 ($object) must be of type object", 0, traceItem{file: vm.file(), line: line, name: "get_class"})
			return true
		}
		classID = types.ObjectInfo(vm.Zone, d).ClassID
	}
	s, err := types.NewStringLiteral(vm.Zone, vm.className(classID))
	if err != nil {
		vm.fatalFeature("get_class")
		return true
	}
	vm.push(s)
	vm.bump()
	return true
}

func (vm *VM) replaceArrayArg(target types.Value, replacement types.Value) {
	if target.Type == types.TypeReference {
		types.AssignRef(vm.Zone, target, replacement)
		types.ReleaseValue(vm.Zone, &replacement)
		return
	}
	dst := types.Deref(vm.Zone, target)
	if dst.Type != types.TypeArray || replacement.Type != types.TypeArray || dst.DataSlot == replacement.DataSlot {
		types.ReleaseValue(vm.Zone, &replacement)
		return
	}
	hdrSize := uint32(unsafe.Sizeof(types.ArrayHeader{}))
	dstBytes, err := vm.Zone.Bytes(dst.DataSlot, hdrSize)
	if err != nil {
		types.ReleaseValue(vm.Zone, &replacement)
		return
	}
	srcBytes, err := vm.Zone.Bytes(replacement.DataSlot, hdrSize)
	if err != nil {
		types.ReleaseValue(vm.Zone, &replacement)
		return
	}
	dstHdr := (*types.ArrayHeader)(unsafe.Pointer(&dstBytes[0]))
	srcHdr := (*types.ArrayHeader)(unsafe.Pointer(&srcBytes[0]))
	dstRef := dstHdr.RefCount
	srcRef := srcHdr.RefCount
	*dstHdr, *srcHdr = *srcHdr, *dstHdr
	dstHdr.RefCount = dstRef
	srcHdr.RefCount = srcRef
	types.ReleaseValue(vm.Zone, &replacement)
}

func (vm *VM) resolveClassOrObj(v types.Value, allowString bool) (uint32, types.Value, bool) {
	d := types.Deref(vm.Zone, v)
	if d.Type == types.TypeObject {
		return types.ObjectInfo(vm.Zone, d).ClassID, d, true
	}
	if allowString && d.Type == types.TypeString {
		s := strings.TrimPrefix(types.StringData(vm.Zone, d), `\`)
		if cid, ok := vm.resolveName(s); ok {
			return cid, types.Value{}, true
		}
	}
	return 0, types.Value{}, false
}

func (vm *VM) builtinMethodExists(cid uint32, method string) bool {
	m := strings.ToLower(method)
	if vm.isExceptionFamily(cid) || cid == bytecode.ClassException || cid == bytecode.ClassError || cid == bytecode.ClassThrowable {
		switch m {
		case "__construct", "getmessage", "getcode", "getfile", "getline", "gettrace", "getprevious", "gettraceasstring", "__tostring":
			return true
		}
	}
	switch cid {
	case bytecode.ClassArrayObject:
		switch m {
		case "__construct", "append", "count", "getflags", "setflags", "getiteratorclass", "setiteratorclass",
			"getarraycopy", "exchangearray", "getiterator", "offsetexists", "offsetget", "offsetset", "offsetunset",
			"asort", "ksort", "natsort", "natcasesort", "uasort", "uksort":
			return true
		}
	case bytecode.ClassArrayIterator:
		switch m {
		case "__construct", "append", "count", "getflags", "setflags", "getarraycopy",
			"offsetexists", "offsetget", "offsetset", "offsetunset",
			"rewind", "valid", "current", "key", "next", "seek",
			"asort", "ksort", "natsort", "natcasesort", "uasort", "uksort":
			return true
		}
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		switch m {
		case "__construct", "push", "pop", "shift", "unshift", "top", "bottom", "count", "isempty",
			"setiteratormode", "getiteratormode", "offsetexists", "offsetget", "offsetset", "offsetunset",
			"add", "rewind", "valid", "current", "key", "next", "prev", "enqueue", "dequeue":
			return true
		}
	case bytecode.ClassSplFixedArray:
		switch m {
		case "__construct", "count", "getsize", "setsize", "toarray", "fromarray",
			"offsetexists", "offsetget", "offsetset", "offsetunset",
			"rewind", "valid", "current", "key", "next":
			return true
		}
	case bytecode.ClassSplObjectStorage:
		switch m {
		case "__construct", "attach", "detach", "contains", "addall", "removeall", "removeallexcept",
			"count", "getinfo", "setinfo", "gethash",
			"offsetexists", "offsetget", "offsetset", "offsetunset",
			"rewind", "valid", "current", "key", "next":
			return true
		}
	case bytecode.ClassClosure:
		return m == "__invoke" || m == "bind" || m == "bindto" || m == "call" || m == "fromcallable"
	case bytecode.ClassGenerator:
		switch m {
		case "current", "key", "next", "rewind", "send", "throw", "valid", "getreturn":
			return true
		}
	}
	return false
}

func (vm *VM) intrinsicExtended(name string, spec callSpec, line uint32) bool {
	switch name {
	case "ob_start":
		if vm.origOut == nil {
			vm.origOut = vm.Out
		}
		buf := new(bytes.Buffer)
		vm.obStack = append(vm.obStack, buf)
		vm.Out = buf
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "ob_get_contents":
		if len(vm.obStack) == 0 {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		top := vm.obStack[len(vm.obStack)-1]
		s, err := types.NewStringLiteral(vm.Zone, top.String())
		if err != nil {
			vm.fatalFeature("ob_get_contents")
			return true
		}
		vm.push(s)
		vm.bump()
		return true

	case "ob_get_length":
		if len(vm.obStack) == 0 {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		top := vm.obStack[len(vm.obStack)-1]
		vm.push(types.NewLong(int64(top.Len())))
		vm.bump()
		return true

	case "ob_get_level":
		vm.push(types.NewLong(int64(len(vm.obStack))))
		vm.bump()
		return true

	case "ob_clean":
		if len(vm.obStack) == 0 {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		vm.obStack[len(vm.obStack)-1].Reset()
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "ob_flush":
		if len(vm.obStack) == 0 {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		top := vm.obStack[len(vm.obStack)-1]
		parent := vm.origOut
		if len(vm.obStack) >= 2 {
			parent = vm.obStack[len(vm.obStack)-2]
		}
		if parent != nil && top.Len() > 0 {
			_, _ = parent.Write(top.Bytes())
		}
		top.Reset()
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "ob_end_clean":
		if len(vm.obStack) == 0 {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		vm.obStack = vm.obStack[:len(vm.obStack)-1]
		if len(vm.obStack) > 0 {
			vm.Out = vm.obStack[len(vm.obStack)-1]
		} else {
			vm.Out = vm.origOut
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "ob_end_flush":
		if len(vm.obStack) == 0 {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		top := vm.obStack[len(vm.obStack)-1]
		vm.obStack = vm.obStack[:len(vm.obStack)-1]
		if len(vm.obStack) > 0 {
			vm.Out = vm.obStack[len(vm.obStack)-1]
		} else {
			vm.Out = vm.origOut
		}
		if vm.Out != nil && top.Len() > 0 {
			_, _ = vm.Out.Write(top.Bytes())
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "ob_get_clean":
		if len(vm.obStack) == 0 {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		top := vm.obStack[len(vm.obStack)-1]
		vm.obStack = vm.obStack[:len(vm.obStack)-1]
		if len(vm.obStack) > 0 {
			vm.Out = vm.obStack[len(vm.obStack)-1]
		} else {
			vm.Out = vm.origOut
		}
		s, err := types.NewStringLiteral(vm.Zone, top.String())
		if err != nil {
			vm.fatalFeature("ob_get_clean")
			return true
		}
		vm.push(s)
		vm.bump()
		return true

	case "ob_get_flush":
		if len(vm.obStack) == 0 {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		top := vm.obStack[len(vm.obStack)-1]
		vm.obStack = vm.obStack[:len(vm.obStack)-1]
		if len(vm.obStack) > 0 {
			vm.Out = vm.obStack[len(vm.obStack)-1]
		} else {
			vm.Out = vm.origOut
		}
		text := top.String()
		if vm.Out != nil && len(text) > 0 {
			_, _ = vm.Out.Write([]byte(text))
		}
		s, err := types.NewStringLiteral(vm.Zone, text)
		if err != nil {
			vm.fatalFeature("ob_get_flush")
			return true
		}
		vm.push(s)
		vm.bump()
		return true

	case "flush":
		vm.push(types.NewNull())
		vm.bump()
		return true

	case "set_error_handler":
		if len(spec.args) < 1 || len(spec.args) > 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("set_error_handler() expects between 1 and 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		prev := types.NewNull()
		if len(vm.errorHandlers) > 0 {
			prev = types.RetainValue(vm.Zone, vm.errorHandlers[len(vm.errorHandlers)-1])
		}
		cb := types.Deref(vm.Zone, spec.args[0])
		if cb.IsNull() {
			vm.errorHandlers = append(vm.errorHandlers, types.NewNull())
		} else {
			if _, ok := vm.callback(cb); !ok {
				types.ReleaseValue(vm.Zone, &prev)
				vm.raiseBuiltin(bytecode.ClassTypeError, "set_error_handler(): Argument #1 ($callback) must be a valid callback or null", 0)
				return true
			}
			vm.errorHandlers = append(vm.errorHandlers, types.RetainValue(vm.Zone, cb))
		}
		vm.push(prev)
		vm.bump()
		return true

	case "restore_error_handler":
		if len(vm.errorHandlers) > 0 {
			last := vm.errorHandlers[len(vm.errorHandlers)-1]
			types.ReleaseValue(vm.Zone, &last)
			vm.errorHandlers = vm.errorHandlers[:len(vm.errorHandlers)-1]
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "set_exception_handler":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("set_exception_handler() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		prev := types.NewNull()
		if len(vm.exceptionHandlers) > 0 {
			prev = types.RetainValue(vm.Zone, vm.exceptionHandlers[len(vm.exceptionHandlers)-1])
		}
		cb := types.Deref(vm.Zone, spec.args[0])
		if cb.IsNull() {
			vm.exceptionHandlers = append(vm.exceptionHandlers, types.NewNull())
		} else {
			if _, ok := vm.callback(cb); !ok {
				types.ReleaseValue(vm.Zone, &prev)
				vm.raiseBuiltin(bytecode.ClassTypeError, "set_exception_handler(): Argument #1 ($callback) must be a valid callback or null", 0)
				return true
			}
			vm.exceptionHandlers = append(vm.exceptionHandlers, types.RetainValue(vm.Zone, cb))
		}
		vm.push(prev)
		vm.bump()
		return true

	case "restore_exception_handler":
		if len(vm.exceptionHandlers) > 0 {
			last := vm.exceptionHandlers[len(vm.exceptionHandlers)-1]
			types.ReleaseValue(vm.Zone, &last)
			vm.exceptionHandlers = vm.exceptionHandlers[:len(vm.exceptionHandlers)-1]
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "trigger_error", "user_error":
		if len(spec.args) < 1 || len(spec.args) > 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s() expects between 1 and 2 arguments, %d given", name, len(spec.args)), 0)
			return true
		}
		ms, mown := vm.stringify(spec.args[0])
		msg := ""
		if ms.Type == types.TypeString {
			msg = types.StringData(vm.Zone, ms)
		}
		if mown {
			types.ReleaseValue(vm.Zone, &ms)
		}
		if vm.stop || vm.control {
			return true
		}
		level := int64(1024)
		if len(spec.args) == 2 {
			level = vm.toLong(spec.args[1])
		}
		if len(vm.errorHandlers) > 0 {
			top := vm.errorHandlers[len(vm.errorHandlers)-1]
			if !top.IsNull() {
				if call, ok := vm.callback(top); ok {
					mVal, _ := types.NewStringLiteral(vm.Zone, msg)
					fVal, _ := types.NewStringLiteral(vm.Zone, vm.file())
					ret, threw := vm.invokeCallback(call, []types.Value{types.NewLong(level), mVal, fVal, types.NewLong(int64(line))}, line)
					if threw {
						types.ReleaseValue(vm.Zone, &ret)
						return true
					}
					handled := ret.Type != types.TypeFalse
					types.ReleaseValue(vm.Zone, &ret)
					if handled {
						vm.push(types.NewBool(true))
						vm.bump()
						return true
					}
				}
			}
		}
		if vm.errorReporting&level != 0 && vm.Out != nil {
			label := "Notice"
			switch level {
			case 512:
				label = "Warning"
			case 16384:
				label = "Deprecated"
			case 256:
				label = "Fatal error"
			}
			fmt.Fprintf(vm.Out, "\n%s: %s in %s on line %d\n", label, msg, vm.file(), line)
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "error_log":
		if len(spec.args) < 1 || len(spec.args) > 4 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("error_log() expects between 1 and 4 arguments, %d given", len(spec.args)), 0)
			return true
		}
		ms, mown := vm.stringify(spec.args[0])
		msg := ""
		if ms.Type == types.TypeString {
			msg = types.StringData(vm.Zone, ms)
		}
		if mown {
			types.ReleaseValue(vm.Zone, &ms)
		}
		msgType := int64(0)
		if len(spec.args) >= 2 {
			msgType = vm.toLong(spec.args[1])
		}
		if msgType == 3 && len(spec.args) >= 3 {
			ds, down := vm.stringify(spec.args[2])
			dest := ""
			if ds.Type == types.TypeString {
				dest = types.StringData(vm.Zone, ds)
			}
			if down {
				types.ReleaseValue(vm.Zone, &ds)
			}
			if dest != "" && dest != "/dev/null" {
				if f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
					_, _ = f.WriteString(msg)
					_ = f.Close()
				}
			}
		} else if vm.Err != nil {
			_, _ = fmt.Fprintln(vm.Err, msg)
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "debug_backtrace":
		arr, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("debug_backtrace")
			return true
		}
		for i, fr := range slices.Backward(vm.frames) {

			item, ierr := types.NewArray(vm.Zone)
			if ierr != nil {
				break
			}
			fnName := name
			lineNo := line
			fileName := vm.file()
			if i < len(vm.frames)-1 {
				if fr.fn != nil && fr.fn.Name != "" {
					fnName = fr.fn.Name
				}
				lineNo = vm.frames[i+1].callLine
				if fr.fn != nil && fr.fn.Filename != "" {
					fileName = fr.fn.Filename
				}
			}
			if k, kerr := types.NewStringLiteral(vm.Zone, "file"); kerr == nil {
				v, _ := types.NewStringLiteral(vm.Zone, fileName)
				_ = types.ArraySet(vm.Zone, &item, k, v, nil)
				types.ReleaseValue(vm.Zone, &v)
				types.ReleaseValue(vm.Zone, &k)
			}
			if k, kerr := types.NewStringLiteral(vm.Zone, "line"); kerr == nil {
				_ = types.ArraySet(vm.Zone, &item, k, types.NewLong(int64(lineNo)), nil)
				types.ReleaseValue(vm.Zone, &k)
			}
			if k, kerr := types.NewStringLiteral(vm.Zone, "function"); kerr == nil {
				v, _ := types.NewStringLiteral(vm.Zone, fnName)
				_ = types.ArraySet(vm.Zone, &item, k, v, nil)
				types.ReleaseValue(vm.Zone, &v)
				types.ReleaseValue(vm.Zone, &k)
			}
			_ = types.ArrayAppend(vm.Zone, &arr, item)
			types.ReleaseValue(vm.Zone, &item)
		}
		vm.push(arr)
		vm.bump()
		return true

	case "spl_autoload_register":
		if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
			cb := types.Deref(vm.Zone, spec.args[0])
			if _, ok := vm.callback(cb); !ok {
				vm.raiseBuiltin(bytecode.ClassTypeError, "spl_autoload_register(): Argument #1 ($callback) must be a valid callback", 0)
				return true
			}
			vm.autoloaders = append(vm.autoloaders, types.RetainValue(vm.Zone, cb))
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "spl_autoload_unregister":
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "spl_autoload_functions":
		arr, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("spl_autoload_functions")
			return true
		}
		for _, cb := range vm.autoloaders {
			_ = types.ArrayAppend(vm.Zone, &arr, cb)
		}
		vm.push(arr)
		vm.bump()
		return true

	case "gc_collect_cycles":
		n := types.CollectCycles(vm.Zone)
		vm.push(types.NewLong(int64(n)))
		vm.bump()
		return true

	case "memory_get_usage", "memory_get_peak_usage":
		used := int64(4096)
		if vm.Zone != nil && vm.Zone.LiveBytes() > 0 {
			used = int64(vm.Zone.LiveBytes())
		}
		vm.push(types.NewLong(used))
		vm.bump()
		return true

	case "ini_get":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ini_get() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		ks, kown := vm.stringify(spec.args[0])
		opt := ""
		if ks.Type == types.TypeString {
			opt = types.StringData(vm.Zone, ks)
		}
		if kown {
			types.ReleaseValue(vm.Zone, &ks)
		}
		val := ""
		switch strings.ToLower(opt) {
		case "memory_limit":
			val = "128M"
		case "error_reporting":
			val = strconv.FormatInt(vm.errorReporting, 10)
		case "display_errors":
			val = "1"
		case "default_charset":
			val = "UTF-8"
		case "max_execution_time":
			val = "0"
		case "date.timezone":
			val = "UTC"
		}
		s, err := types.NewStringLiteral(vm.Zone, val)
		if err != nil {
			vm.fatalFeature("ini_get")
			return true
		}
		vm.push(s)
		vm.bump()
		return true

	case "ini_set":
		s, err := types.NewStringLiteral(vm.Zone, "")
		if err != nil {
			vm.fatalFeature("ini_set")
			return true
		}
		vm.push(s)
		vm.bump()
		return true

	case "assert":
		if len(spec.args) < 1 || len(spec.args) > 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("assert() expects between 1 and 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		if !types.ToBoolZ(vm.Zone, spec.args[0]) {
			if len(spec.args) >= 2 {
				d := types.Deref(vm.Zone, spec.args[1])
				if d.Type == types.TypeObject {
					vm.throwAt(vm.frame().pc, types.RetainValue(vm.Zone, d))
					return true
				}
				ms, mown := vm.stringify(d)
				msg := "assert(false)"
				if ms.Type == types.TypeString {
					msg = types.StringData(vm.Zone, ms)
				}
				if mown {
					types.ReleaseValue(vm.Zone, &ms)
				}
				vm.raiseBuiltin(bytecode.ClassError, msg, 0)
				return true
			}
			vm.raiseBuiltin(bytecode.ClassError, "assert(false)", 0)
			return true
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "filter_input":
		if len(spec.args) < 2 || len(spec.args) > 4 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("filter_input() expects between 2 and 4 arguments, %d given", len(spec.args)), 0)
			return true
		}
		inputType := vm.toLong(spec.args[0])
		var sgName string
		switch inputType {
		case 0:
			sgName = "_POST"
		case 1:
			sgName = "_GET"
		case 2:
			sgName = "_COOKIE"
		case 4:
			sgName = "_ENV"
		case 5:
			sgName = "_SERVER"
		default:
			vm.push(types.NewNull())
			vm.bump()
			return true
		}
		ks, kown := vm.stringify(spec.args[1])
		if vm.stop || vm.control {
			if kown {
				types.ReleaseValue(vm.Zone, &ks)
			}
			return true
		}
		ref := vm.superglobalRef(sgName)
		arr := types.Deref(vm.Zone, ref)
		var res types.Value
		found := false
		if arr.Type == types.TypeArray {
			if v, ok, _ := types.ArrayGet(vm.Zone, arr, ks, nil); ok {
				res = types.RetainValue(vm.Zone, types.Deref(vm.Zone, v))
				found = true
			}
		}
		if kown {
			types.ReleaseValue(vm.Zone, &ks)
		}
		if !found {
			vm.push(types.NewNull())
			vm.bump()
			return true
		}
		vm.push(res)
		vm.bump()
		return true

	case "get_called_class":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("get_called_class() expects exactly 0 arguments, %d given", len(spec.args)), 0)
			return true
		}
		fr := vm.frame()
		cid := fr.calledClass
		if cid == 0 {
			cid = fr.scopeClass
		}
		if cid == 0 {
			vm.raiseBuiltin(bytecode.ClassError, "get_called_class() must be called from within a class", 0)
			return true
		}
		s, err := types.NewStringLiteral(vm.Zone, vm.className(cid))
		if err != nil {
			vm.fatalFeature("get_called_class")
			return true
		}
		vm.push(s)
		vm.bump()
		return true

	case "get_parent_class":
		if len(spec.args) > 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("get_parent_class() expects at most 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		var cid uint32
		var ok bool
		if len(spec.args) == 0 {
			cid = vm.frame().scopeClass
			ok = cid != 0
		} else {
			cid, _, ok = vm.resolveClassOrObj(spec.args[0], true)
		}
		if !ok {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		c := vm.classByID(cid)
		if c == nil || c.ParentID == 0 || c.ParentID == bytecode.ClassNone || c.ParentID == bytecode.ClassThrowable {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		s, err := types.NewStringLiteral(vm.Zone, vm.className(c.ParentID))
		if err != nil {
			vm.fatalFeature("get_parent_class")
			return true
		}
		vm.push(s)
		vm.bump()
		return true

	case "method_exists":
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("method_exists() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		cid, _, ok := vm.resolveClassOrObj(spec.args[0], true)
		ms, mown := vm.stringify(spec.args[1])
		mname := ""
		if ms.Type == types.TypeString {
			mname = types.StringData(vm.Zone, ms)
		}
		if mown {
			types.ReleaseValue(vm.Zone, &ms)
		}
		found := ok && (vm.hasMethod(cid, mname) || vm.builtinMethodExists(cid, mname))
		vm.push(types.NewBool(found))
		vm.bump()
		return true

	case "property_exists":
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("property_exists() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		cid, obj, ok := vm.resolveClassOrObj(spec.args[0], true)
		ps, pown := vm.stringify(spec.args[1])
		pname := ""
		if ps.Type == types.TypeString {
			pname = types.StringData(vm.Zone, ps)
		}
		if pown {
			types.ReleaseValue(vm.Zone, &ps)
		}
		found := false
		if ok {
			if _, _, pok := vm.propIndex(cid, pname, cid); pok {
				found = true
			} else if _, sok := vm.staticIndex(cid, pname); sok {
				found = true
			} else if vm.isExceptionFamily(cid) || cid == bytecode.ClassException || cid == bytecode.ClassError {
				switch pname {
				case "message", "code", "file", "line":
					found = true
				}
			}
			if !found && obj.Type == types.TypeObject {
				if v, dynOK := vm.dynamicPropGet(obj, pname); dynOK {
					types.ReleaseValue(vm.Zone, &v)
					found = true
				}
			}
		}
		vm.push(types.NewBool(found))
		vm.bump()
		return true

	case "is_a", "is_subclass_of":
		if len(spec.args) < 2 || len(spec.args) > 3 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s() expects between 2 and 3 arguments, %d given", name, len(spec.args)), 0)
			return true
		}
		allowString := name == "is_subclass_of"
		if len(spec.args) == 3 {
			allowString = types.ToBoolZ(vm.Zone, spec.args[2])
		}
		cid, _, ok := vm.resolveClassOrObj(spec.args[0], allowString)
		ts, town := vm.stringify(spec.args[1])
		tname := ""
		if ts.Type == types.TypeString {
			tname = strings.TrimPrefix(types.StringData(vm.Zone, ts), `\`)
		}
		if town {
			types.ReleaseValue(vm.Zone, &ts)
		}
		targetID, tok := vm.resolveName(tname)
		res := false
		if ok && tok {
			if name == "is_subclass_of" {
				res = cid != targetID && vm.isInstance(cid, targetID)
			} else {
				res = vm.isInstance(cid, targetID)
			}
		}
		vm.push(types.NewBool(res))
		vm.bump()
		return true

	case "get_class_methods":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("get_class_methods() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		cid, _, ok := vm.resolveClassOrObj(spec.args[0], true)
		arr, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("get_class_methods")
			return true
		}
		if !ok {
			vm.push(arr)
			vm.bump()
			return true
		}
		seen := make(map[string]bool)
		var names []string
		for id := cid; id != 0 && id != bytecode.ClassNone; {
			c := vm.classByID(id)
			if c == nil {
				break
			}
			for _, m := range c.Methods {
				low := strings.ToLower(m.Name)
				if !seen[low] && vm.visible(m.Visibility, c.ID) {
					seen[low] = true
					names = append(names, m.Name)
				}
			}
			id = c.ParentID
		}
		if vm.isExceptionFamily(cid) || cid == bytecode.ClassException || cid == bytecode.ClassError {
			for _, bm := range []string{"__construct", "getMessage", "getCode", "getFile", "getLine", "getTrace", "getPrevious", "getTraceAsString", "__toString"} {
				low := strings.ToLower(bm)
				if !seen[low] {
					seen[low] = true
					names = append(names, bm)
				}
			}
		}
		for _, nm := range names {
			if sv, serr := types.NewStringLiteral(vm.Zone, nm); serr == nil {
				_ = types.ArrayAppend(vm.Zone, &arr, sv)
				types.ReleaseValue(vm.Zone, &sv)
			}
		}
		vm.push(arr)
		vm.bump()
		return true

	case "get_object_vars":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("get_object_vars() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		d := types.Deref(vm.Zone, spec.args[0])
		if d.Type != types.TypeObject {
			vm.raiseBuiltin(bytecode.ClassTypeError, "get_object_vars(): Argument #1 ($object) must be of type object", 0)
			return true
		}
		info := types.ObjectInfo(vm.Zone, d)
		if info.ClassID == bytecode.ClassArrayObject {
			if ao, err := spl.ArrayObjectFromObject(vm.Zone, d); err == nil {
				if copyArr, cerr := ao.GetArrayCopy(vm.Zone); cerr == nil {
					vm.push(copyArr)
					vm.bump()
					return true
				}
			}
		}
		arr, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("get_object_vars")
			return true
		}
		if c := vm.classByID(info.ClassID); c != nil && c.PropsResolved {
			for _, p := range c.ResolvedProps {
				if p.Static || !vm.visible(p.Visibility, p.ClassID) {
					continue
				}
				pv := types.ObjectGet(vm.Zone, d, p.Index)
				if pv.IsUndef() {
					continue
				}
				if k, kerr := types.NewStringLiteral(vm.Zone, p.Name); kerr == nil {
					_ = types.ArraySet(vm.Zone, &arr, k, types.Deref(vm.Zone, pv), nil)
					types.ReleaseValue(vm.Zone, &k)
				}
			}
		}
		if info.DynOffset != types.EmptySlot {
			dynArr := types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: info.DynOffset}
			types.ArrayForEach(vm.Zone, dynArr, func(k, dv types.Value) bool {
				_ = types.ArraySet(vm.Zone, &arr, k, types.Deref(vm.Zone, dv), nil)
				return true
			})
		}
		vm.push(arr)
		vm.bump()
		return true

	case "is_iterable":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("is_iterable() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		d := types.Deref(vm.Zone, spec.args[0])
		res := d.Type == types.TypeArray || (d.Type == types.TypeObject && vm.isInstance(types.ObjectInfo(vm.Zone, d).ClassID, bytecode.ClassTraversable))
		vm.push(types.NewBool(res))
		vm.bump()
		return true

	case "is_countable":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("is_countable() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		d := types.Deref(vm.Zone, spec.args[0])
		res := d.Type == types.TypeArray || (d.Type == types.TypeObject && vm.isInstance(types.ObjectInfo(vm.Zone, d).ClassID, bytecode.ClassCountable))
		vm.push(types.NewBool(res))
		vm.bump()
		return true

	case "iterator_to_array":
		if len(spec.args) < 1 || len(spec.args) > 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("iterator_to_array() expects between 1 and 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		preserve := true
		if len(spec.args) == 2 {
			preserve = types.ToBoolZ(vm.Zone, spec.args[1])
		}
		d := types.Deref(vm.Zone, spec.args[0])
		if d.Type == types.TypeArray {
			if preserve {
				vm.push(types.RetainValue(vm.Zone, d))
				vm.bump()
				return true
			}
			out, err := types.NewArray(vm.Zone)
			if err != nil {
				vm.fatalFeature("iterator_to_array")
				return true
			}
			types.ArrayForEach(vm.Zone, d, func(_, v types.Value) bool {
				_ = types.ArrayAppend(vm.Zone, &out, types.Deref(vm.Zone, v))
				return true
			})
			vm.push(out)
			vm.bump()
			return true
		}
		if d.Type != types.TypeObject {
			vm.raiseBuiltin(bytecode.ClassTypeError, "iterator_to_array(): Argument #1 ($iterator) must be of type Traversable|array", 0)
			return true
		}
		out, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("iterator_to_array")
			return true
		}
		var slot iterState
		if !vm.prepareObjectIter(&slot, d) {
			types.ReleaseValue(vm.Zone, &out)
			vm.raiseBuiltin(bytecode.ClassTypeError, "iterator_to_array(): Argument #1 ($iterator) must be of type Traversable|array", 0)
			return true
		}
		if slot.iterObj.Type == types.TypeObject {
			for slot.active && !vm.stop && !vm.control {
				v := vm.iterCurrent(slot.iterObj)
				if vm.stop || vm.control {
					types.ReleaseValue(vm.Zone, &v)
					break
				}
				if preserve {
					k := vm.iterKey(slot.iterObj)
					if !vm.stop && !vm.control {
						_ = types.ArraySet(vm.Zone, &out, k, types.Deref(vm.Zone, v), vm.deprecatedFloatKey)
					}
					types.ReleaseValue(vm.Zone, &k)
				} else {
					_ = types.ArrayAppend(vm.Zone, &out, types.Deref(vm.Zone, v))
				}
				types.ReleaseValue(vm.Zone, &v)
				if vm.stop || vm.control {
					break
				}
				vm.iterNext(slot.iterObj)
				if vm.stop || vm.control {
					break
				}
				slot.active = vm.iterValid(slot.iterObj)
			}
			types.ReleaseValue(vm.Zone, &slot.iterObj)
		} else {
			for i := range slot.vals {
				if preserve {
					_ = types.ArraySet(vm.Zone, &out, slot.keys[i], slot.vals[i], vm.deprecatedFloatKey)
				} else {
					_ = types.ArrayAppend(vm.Zone, &out, slot.vals[i])
				}
				types.ReleaseValue(vm.Zone, &slot.keys[i])
				types.ReleaseValue(vm.Zone, &slot.vals[i])
			}
		}
		if vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &out)
			return true
		}
		vm.push(out)
		vm.bump()
		return true

	case "iterator_count":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("iterator_count() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		d := types.Deref(vm.Zone, spec.args[0])
		if d.Type == types.TypeArray {
			vm.push(types.NewLong(int64(types.ArrayLen(vm.Zone, d))))
			vm.bump()
			return true
		}
		if d.Type != types.TypeObject {
			vm.raiseBuiltin(bytecode.ClassTypeError, "iterator_count(): Argument #1 ($iterator) must be of type Traversable|array", 0)
			return true
		}
		var slot iterState
		if !vm.prepareObjectIter(&slot, d) {
			vm.raiseBuiltin(bytecode.ClassTypeError, "iterator_count(): Argument #1 ($iterator) must be of type Traversable|array", 0)
			return true
		}
		var count int64
		if slot.iterObj.Type == types.TypeObject {
			for slot.active && !vm.stop && !vm.control {
				count++
				vm.iterNext(slot.iterObj)
				if vm.stop || vm.control {
					break
				}
				slot.active = vm.iterValid(slot.iterObj)
			}
			types.ReleaseValue(vm.Zone, &slot.iterObj)
		} else {
			count = int64(len(slot.vals))
			for i := range slot.vals {
				types.ReleaseValue(vm.Zone, &slot.keys[i])
				types.ReleaseValue(vm.Zone, &slot.vals[i])
			}
		}
		if vm.stop || vm.control {
			return true
		}
		vm.push(types.NewLong(count))
		vm.bump()
		return true

	case "array_map":
		return vm.intrinsicArrayMap(spec, line)
	case "array_filter":
		return vm.intrinsicArrayFilter(spec, line)
	case "array_reduce":
		return vm.intrinsicArrayReduce(spec, line)
	case "array_walk", "array_walk_recursive":
		return vm.intrinsicArrayWalk(name == "array_walk_recursive", spec, line)
	case "usort", "uasort", "uksort":
		return vm.intrinsicUsort(name, spec, line)
	case "preg_replace_callback":
		return vm.intrinsicPregReplaceCallback(spec, line)
	}
	return false
}

func (vm *VM) intrinsicArrayMap(spec callSpec, line uint32) bool {
	if len(spec.args) < 2 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("array_map() expects at least 2 arguments, %d given", len(spec.args)), 0)
		return true
	}
	cbVal := types.Deref(vm.Zone, spec.args[0])
	hasCB := !cbVal.IsNull()
	var call callSpec
	if hasCB {
		var ok bool
		call, ok = vm.callback(cbVal)
		if !ok {
			vm.raiseBuiltin(bytecode.ClassTypeError, "array_map(): Argument #1 ($callback) must be a valid callback or null", 0)
			return true
		}
	}
	for i := 1; i < len(spec.args); i++ {
		if types.Deref(vm.Zone, spec.args[i]).Type != types.TypeArray {
			vm.raiseBuiltin(bytecode.ClassTypeError, fmt.Sprintf("array_map(): Argument #%d must be of type array", i+1), 0)
			return true
		}
	}
	out, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("array_map")
		return true
	}
	if len(spec.args) == 2 {
		arr := types.Deref(vm.Zone, spec.args[1])
		if !hasCB {
			types.ArrayForEach(vm.Zone, arr, func(k, v types.Value) bool {
				_ = types.ArraySet(vm.Zone, &out, k, types.Deref(vm.Zone, v), nil)
				return true
			})
			vm.push(out)
			vm.bump()
			return true
		}
		types.ArrayForEach(vm.Zone, arr, func(k, v types.Value) bool {
			ret, threw := vm.invokeCallback(call, []types.Value{types.RetainValue(vm.Zone, types.Deref(vm.Zone, v))}, line)
			if threw || vm.stop || vm.control {
				types.ReleaseValue(vm.Zone, &ret)
				return false
			}
			_ = types.ArraySet(vm.Zone, &out, k, types.Deref(vm.Zone, ret), nil)
			types.ReleaseValue(vm.Zone, &ret)
			return true
		})
		if vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &out)
			return true
		}
		vm.push(out)
		vm.bump()
		return true
	}
	nArrays := len(spec.args) - 1
	slicesList := make([][]types.Value, nArrays)
	maxLen := 0
	for i := range nArrays {
		arr := types.Deref(vm.Zone, spec.args[i+1])
		var vals []types.Value
		types.ArrayForEach(vm.Zone, arr, func(_, v types.Value) bool {
			vals = append(vals, types.Deref(vm.Zone, v))
			return true
		})
		slicesList[i] = vals
		if len(vals) > maxLen {
			maxLen = len(vals)
		}
	}
	for idx := range maxLen {
		if !hasCB {
			row, rerr := types.NewArray(vm.Zone)
			if rerr != nil {
				break
			}
			for i := range nArrays {
				elem := types.NewNull()
				if idx < len(slicesList[i]) {
					elem = slicesList[i][idx]
				}
				_ = types.ArrayAppend(vm.Zone, &row, elem)
			}
			_ = types.ArrayAppend(vm.Zone, &out, row)
			types.ReleaseValue(vm.Zone, &row)
			continue
		}
		cbArgs := make([]types.Value, nArrays)
		for i := range nArrays {
			if idx < len(slicesList[i]) {
				cbArgs[i] = types.RetainValue(vm.Zone, slicesList[i][idx])
			} else {
				cbArgs[i] = types.NewNull()
			}
		}
		ret, threw := vm.invokeCallback(call, cbArgs, line)
		if threw || vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &ret)
			types.ReleaseValue(vm.Zone, &out)
			return true
		}
		_ = types.ArrayAppend(vm.Zone, &out, types.Deref(vm.Zone, ret))
		types.ReleaseValue(vm.Zone, &ret)
	}
	vm.push(out)
	vm.bump()
	return true
}

func (vm *VM) intrinsicArrayFilter(spec callSpec, line uint32) bool {
	if len(spec.args) < 1 || len(spec.args) > 3 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("array_filter() expects between 1 and 3 arguments, %d given", len(spec.args)), 0)
		return true
	}
	arr := types.Deref(vm.Zone, spec.args[0])
	if arr.Type != types.TypeArray {
		vm.raiseBuiltin(bytecode.ClassTypeError, "array_filter(): Argument #1 ($array) must be of type array", 0)
		return true
	}
	hasCB := len(spec.args) >= 2 && !types.Deref(vm.Zone, spec.args[1]).IsNull()
	var call callSpec
	if hasCB {
		var ok bool
		call, ok = vm.callback(spec.args[1])
		if !ok {
			vm.raiseBuiltin(bytecode.ClassTypeError, "array_filter(): Argument #2 ($callback) must be a valid callback or null", 0)
			return true
		}
	}
	mode := int64(0)
	if len(spec.args) == 3 {
		mode = vm.toLong(spec.args[2])
	}
	out, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("array_filter")
		return true
	}
	types.ArrayForEach(vm.Zone, arr, func(k, v types.Value) bool {
		dv := types.Deref(vm.Zone, v)
		if !hasCB {
			if types.ToBoolZ(vm.Zone, dv) {
				_ = types.ArraySet(vm.Zone, &out, k, dv, nil)
			}
			return true
		}
		var cbArgs []types.Value
		switch mode {
		case 2: // ARRAY_FILTER_USE_KEY
			cbArgs = []types.Value{types.RetainValue(vm.Zone, k)}
		case 1: // ARRAY_FILTER_USE_BOTH
			cbArgs = []types.Value{types.RetainValue(vm.Zone, dv), types.RetainValue(vm.Zone, k)}
		default:
			cbArgs = []types.Value{types.RetainValue(vm.Zone, dv)}
		}
		ret, threw := vm.invokeCallback(call, cbArgs, line)
		if threw || vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &ret)
			return false
		}
		keep := types.ToBoolZ(vm.Zone, ret)
		types.ReleaseValue(vm.Zone, &ret)
		if keep {
			_ = types.ArraySet(vm.Zone, &out, k, dv, nil)
		}
		return true
	})
	if vm.stop || vm.control {
		types.ReleaseValue(vm.Zone, &out)
		return true
	}
	vm.push(out)
	vm.bump()
	return true
}

func (vm *VM) intrinsicArrayReduce(spec callSpec, line uint32) bool {
	if len(spec.args) < 2 || len(spec.args) > 3 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("array_reduce() expects between 2 and 3 arguments, %d given", len(spec.args)), 0)
		return true
	}
	arr := types.Deref(vm.Zone, spec.args[0])
	if arr.Type != types.TypeArray {
		vm.raiseBuiltin(bytecode.ClassTypeError, "array_reduce(): Argument #1 ($array) must be of type array", 0)
		return true
	}
	call, ok := vm.callback(spec.args[1])
	if !ok {
		vm.raiseBuiltin(bytecode.ClassTypeError, "array_reduce(): Argument #2 ($callback) must be a valid callback", 0)
		return true
	}
	carry := types.NewNull()
	if len(spec.args) == 3 {
		carry = types.RetainValue(vm.Zone, types.Deref(vm.Zone, spec.args[2]))
	}
	types.ArrayForEach(vm.Zone, arr, func(_, v types.Value) bool {
		dv := types.Deref(vm.Zone, v)
		ret, threw := vm.invokeCallback(call, []types.Value{carry, types.RetainValue(vm.Zone, dv)}, line)
		if threw || vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &ret)
			carry = types.NewNull()
			return false
		}
		carry = ret
		return true
	})
	if vm.stop || vm.control {
		types.ReleaseValue(vm.Zone, &carry)
		return true
	}
	vm.push(carry)
	vm.bump()
	return true
}

func (vm *VM) intrinsicArrayWalk(recursive bool, spec callSpec, line uint32) bool {
	fname := "array_walk"
	if recursive {
		fname = "array_walk_recursive"
	}
	if len(spec.args) < 2 || len(spec.args) > 3 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s() expects between 2 and 3 arguments, %d given", fname, len(spec.args)), 0)
		return true
	}
	arr := types.Deref(vm.Zone, spec.args[0])
	if arr.Type != types.TypeArray {
		vm.raiseBuiltin(bytecode.ClassTypeError, fname+"(): Argument #1 ($array) must be of type array", 0)
		return true
	}
	call, ok := vm.callback(spec.args[1])
	if !ok {
		vm.raiseBuiltin(bytecode.ClassTypeError, fname+"(): Argument #2 ($callback) must be a valid callback", 0)
		return true
	}
	hasExtra := len(spec.args) == 3
	var extra types.Value
	if hasExtra {
		extra = types.Deref(vm.Zone, spec.args[2])
	}
	var walkArray func(in types.Value) (types.Value, bool)
	walkArray = func(in types.Value) (types.Value, bool) {
		rebuilt, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature(fname)
			return types.NewNull(), false
		}
		aborted := false
		types.ArrayForEach(vm.Zone, in, func(k, v types.Value) bool {
			dv := types.Deref(vm.Zone, v)
			if recursive && dv.Type == types.TypeArray {
				sub, subOK := walkArray(dv)
				if !subOK {
					aborted = true
					return false
				}
				_ = types.ArraySet(vm.Zone, &rebuilt, k, sub, nil)
				types.ReleaseValue(vm.Zone, &sub)
				return true
			}
			vRef, rerr := types.NewReference(vm.Zone, dv)
			if rerr != nil {
				aborted = true
				return false
			}
			cbArgs := []types.Value{types.RetainReference(vm.Zone, vRef), types.RetainValue(vm.Zone, k)}
			if hasExtra {
				cbArgs = append(cbArgs, types.RetainValue(vm.Zone, extra))
			}
			ret, threw := vm.invokeCallback(call, cbArgs, line)
			types.ReleaseValue(vm.Zone, &ret)
			if threw || vm.stop || vm.control {
				types.ReleaseValue(vm.Zone, &vRef)
				aborted = true
				return false
			}
			updated := types.Deref(vm.Zone, vRef)
			_ = types.ArraySet(vm.Zone, &rebuilt, k, updated, nil)
			types.ReleaseValue(vm.Zone, &vRef)
			return true
		})
		if aborted {
			types.ReleaseValue(vm.Zone, &rebuilt)
			return types.NewNull(), false
		}
		return rebuilt, true
	}
	rebuilt, walkOK := walkArray(arr)
	if !walkOK || vm.stop || vm.control {
		types.ReleaseValue(vm.Zone, &rebuilt)
		return true
	}
	vm.replaceArrayArg(spec.args[0], rebuilt)
	vm.push(types.NewBool(true))
	vm.bump()
	return true
}

func (vm *VM) intrinsicUsort(name string, spec callSpec, line uint32) bool {
	if len(spec.args) != 2 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s() expects exactly 2 arguments, %d given", name, len(spec.args)), 0)
		return true
	}
	arr := types.Deref(vm.Zone, spec.args[0])
	if arr.Type != types.TypeArray {
		vm.raiseBuiltin(bytecode.ClassTypeError, name+"(): Argument #1 ($array) must be of type array", 0)
		return true
	}
	call, ok := vm.callback(spec.args[1])
	if !ok {
		vm.raiseBuiltin(bytecode.ClassTypeError, name+"(): Argument #2 ($callback) must be a valid callback", 0)
		return true
	}
	type kvPair struct {
		k types.Value
		v types.Value
	}
	var items []kvPair
	types.ArrayForEach(vm.Zone, arr, func(k, v types.Value) bool {
		items = append(items, kvPair{
			k: types.RetainValue(vm.Zone, k),
			v: types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)),
		})
		return true
	})
	aborted := false
	slices.SortStableFunc(items, func(a, b kvPair) int {
		if aborted || vm.stop || vm.control {
			return 0
		}
		var argA, argB types.Value
		if name == "uksort" {
			argA = types.RetainValue(vm.Zone, a.k)
			argB = types.RetainValue(vm.Zone, b.k)
		} else {
			argA = types.RetainValue(vm.Zone, a.v)
			argB = types.RetainValue(vm.Zone, b.v)
		}
		ret, threw := vm.invokeCallback(call, []types.Value{argA, argB}, line)
		if threw || vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &ret)
			aborted = true
			return 0
		}
		c := int(vm.toLong(ret))
		types.ReleaseValue(vm.Zone, &ret)
		return c
	})
	rebuilt, err := types.NewArray(vm.Zone)
	if err != nil {
		for i := range items {
			types.ReleaseValue(vm.Zone, &items[i].k)
			types.ReleaseValue(vm.Zone, &items[i].v)
		}
		vm.fatalFeature(name)
		return true
	}
	for i := range items {
		if !aborted {
			if name == "usort" {
				_ = types.ArrayAppend(vm.Zone, &rebuilt, items[i].v)
			} else {
				_ = types.ArraySet(vm.Zone, &rebuilt, items[i].k, items[i].v, nil)
			}
		}
		types.ReleaseValue(vm.Zone, &items[i].k)
		types.ReleaseValue(vm.Zone, &items[i].v)
	}
	if aborted || vm.stop || vm.control {
		types.ReleaseValue(vm.Zone, &rebuilt)
		return true
	}
	vm.replaceArrayArg(spec.args[0], rebuilt)
	vm.push(types.NewBool(true))
	vm.bump()
	return true
}

func parsePHPRegex(pattern string) (*regexp.Regexp, bool) {
	if len(pattern) < 2 {
		return nil, false
	}
	delim := pattern[0]
	closeDelim := delim
	switch delim {
	case '(':
		closeDelim = ')'
	case '[':
		closeDelim = ']'
	case '{':
		closeDelim = '}'
	case '<':
		closeDelim = '>'
	}
	end := -1
	for i := len(pattern) - 1; i >= 1; i-- {
		if pattern[i] == closeDelim && (i == 1 || pattern[i-1] != '\\') {
			end = i
			break
		}
	}
	if end < 1 {
		return nil, false
	}
	body := pattern[1:end]
	flags := pattern[end+1:]
	var prefix strings.Builder
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case 'i', 'm', 's':
			prefix.WriteString(string(flags[i]))
		}
	}
	if prefix.String() != "" {
		body = "(?" + prefix.String() + ")" + body
	}
	re, err := regexp.Compile(body)
	if err != nil {
		return nil, false
	}
	return re, true
}

func (vm *VM) intrinsicPregReplaceCallback(spec callSpec, line uint32) bool {
	if len(spec.args) < 3 || len(spec.args) > 6 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("preg_replace_callback() expects between 3 and 6 arguments, %d given", len(spec.args)), 0)
		return true
	}
	ps, pown := vm.stringify(spec.args[0])
	pat := ""
	if ps.Type == types.TypeString {
		pat = types.StringData(vm.Zone, ps)
	}
	if pown {
		types.ReleaseValue(vm.Zone, &ps)
	}
	call, ok := vm.callback(spec.args[1])
	if !ok {
		vm.raiseBuiltin(bytecode.ClassTypeError, "preg_replace_callback(): Argument #2 ($callback) must be a valid callback", 0)
		return true
	}
	ss, sown := vm.stringify(spec.args[2])
	subj := ""
	if ss.Type == types.TypeString {
		subj = types.StringData(vm.Zone, ss)
	}
	if sown {
		types.ReleaseValue(vm.Zone, &ss)
	}
	limit := -1
	if len(spec.args) >= 4 {
		limit = int(vm.toLong(spec.args[3]))
	}
	re, ok := parsePHPRegex(pat)
	if !ok {
		vm.push(types.NewNull())
		vm.bump()
		return true
	}
	locs := re.FindAllStringSubmatchIndex(subj, limit)
	names := re.SubexpNames()
	var out strings.Builder
	last := 0
	var count int64
	for _, loc := range locs {
		out.WriteString(subj[last:loc[0]])
		last = loc[1]
		mArr, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("preg_replace_callback")
			return true
		}
		nGroups := len(loc) / 2
		for g := range nGroups {
			sIdx, eIdx := loc[2*g], loc[2*g+1]
			part := ""
			if sIdx >= 0 && eIdx >= 0 {
				part = subj[sIdx:eIdx]
			}
			sv, _ := types.NewStringLiteral(vm.Zone, part)
			if g < len(names) && names[g] != "" {
				nk, _ := types.NewStringLiteral(vm.Zone, names[g])
				_ = types.ArraySet(vm.Zone, &mArr, nk, sv, nil)
				types.ReleaseValue(vm.Zone, &nk)
			}
			_ = types.ArraySet(vm.Zone, &mArr, types.NewLong(int64(g)), sv, nil)
			types.ReleaseValue(vm.Zone, &sv)
		}
		ret, threw := vm.invokeCallback(call, []types.Value{mArr}, line)
		if threw || vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &ret)
			return true
		}
		rs, rown := vm.stringify(ret)
		types.ReleaseValue(vm.Zone, &ret)
		if rs.Type == types.TypeString {
			out.WriteString(types.StringData(vm.Zone, rs))
		}
		if rown {
			types.ReleaseValue(vm.Zone, &rs)
		}
		count++
	}
	out.WriteString(subj[last:])
	if len(spec.args) >= 5 && spec.args[4].Type == types.TypeReference {
		types.AssignRef(vm.Zone, spec.args[4], types.NewLong(count))
	}
	resStr, err := types.NewStringLiteral(vm.Zone, out.String())
	if err != nil {
		vm.fatalFeature("preg_replace_callback")
		return true
	}
	vm.push(resStr)
	vm.bump()
	return true
}
