package vm

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/datetime"
	"code.hazyhaar.fr/devhoros/php127/ext"
	"code.hazyhaar.fr/devhoros/php127/spl"
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/streams"
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
		"assert", "filter_input",
		"register_shutdown_function", "class_alias", "spl_autoload_call",
		"ini_alter", "ini_restore", "set_include_path", "get_include_path",
		"extension_loaded", "get_loaded_extensions", "get_defined_constants",
		"get_declared_classes", "get_declared_interfaces", "get_declared_traits",
		"get_defined_functions", "get_defined_vars", "ini_get_all",
		"is_resource", "get_resource_type", "get_class_vars", "gc_enabled", "gc_enable", "gc_disable",
		"fastcgi_finish_request", "litespeed_finish_request",
		"xml_parse", "curl_exec":
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
				if vm.raiseDiag(eDeprecated, diagDeprecate, "strlen(): Passing null to parameter #1 ($string) of type string is deprecated", line) {
					return true
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
		"gc_collect_cycles", "memory_get_usage", "memory_get_peak_usage",
		"array_map", "array_filter", "array_reduce", "array_walk", "array_walk_recursive",
		"usort", "uasort", "uksort", "preg_replace_callback",
		"assert", "filter_input", "xml_parse", "curl_exec":
		return vm.intrinsicExtended(name, spec, line)
	case "register_shutdown_function", "class_alias", "spl_autoload_call",
		"ini_get", "ini_set", "ini_alter", "ini_restore", "set_include_path", "get_include_path",
		"extension_loaded", "get_loaded_extensions", "get_defined_constants",
		"get_declared_classes", "get_declared_interfaces", "get_declared_traits",
		"get_defined_functions", "get_defined_vars", "ini_get_all":
		return vm.intrinsicRuntime(name, spec, line)
	case "is_resource", "get_resource_type", "get_class_vars", "gc_enabled", "gc_enable", "gc_disable",
		"fastcgi_finish_request", "litespeed_finish_request":
		return vm.intrinsicHost(name, spec, line)
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
				if vm.raiseDiag(eWarning, diagWarning, "Constant "+key+" already defined", line) {
					return true
				}
			} else {
				if vm.constCache == nil {
					vm.constCache = make(map[string]types.Value)
				}
				key = strings.Clone(key)
				vm.constCache[key] = types.RetainValue(vm.Zone, types.Deref(vm.Zone, spec.args[1]))
				vm.userConsts = append(vm.userConsts, key)
			}
			result = types.NewBool(!exists)
		case "function_exists":
			_, found := lookupNative(key)
			found = found || intrinsicExists(key)
			for _, fn := range vm.Program.Funcs {
				if fn != nil && !bytecode.AnonymousFunc(fn.Name) && strings.EqualFold(fn.Name, key) {
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
			if id, ok := bytecode.BuiltinClassID(key); ok {
				if proto := builtinProto(id); proto != nil {
					found = proto.Kind == kind
				}
			} else {
				autoload := len(spec.args) < 2 || types.ToBoolZ(vm.Zone, spec.args[1])
				cl, ok := vm.declaredClassByName(key, autoload)
				if vm.stop || vm.control {
					return true
				}
				found = ok && (cl.Kind == kind || name == "class_exists" && cl.Kind == bytecode.KindEnum)
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
		if d.Type == types.TypeObject {
			classID = types.ObjectInfo(vm.Zone, d).ClassID
		} else if cid, ok := opaqueResourceClassID(d.Extra); d.Type == types.TypeResource && ok {
			classID = cid
		} else {
			vm.raiseBuiltin(bytecode.ClassTypeError, "get_class(): Argument #1 ($object) must be of type object", 0, traceItem{file: vm.file(), line: line, name: "get_class"})
			return true
		}
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
	if d.Type == types.TypeResource {
		if cid, ok := opaqueResourceClassID(d.Extra); ok {
			return cid, d, true
		}
	}
	if allowString && d.Type == types.TypeString {
		s := strings.TrimPrefix(types.StringData(vm.Zone, d), `\`)
		if id, ok := bytecode.BuiltinClassID(s); ok {
			return id, types.Value{}, true
		}
		if c, ok := vm.declaredClassByName(s, true); ok {
			return c.ID, types.Value{}, true
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
		if vm.stop || vm.control {
			return true
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
		if vm.stop || vm.control {
			return true
		}
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
		if vm.stop || vm.control {
			return true
		}
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
		if vm.stop || vm.control {
			return true
		}
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
		if vm.stop || vm.control {
			return true
		}
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
		out, ok := vm.collectTraversable(d, preserve)
		if !ok {
			if !vm.stop && !vm.control {
				vm.raiseBuiltin(bytecode.ClassTypeError, "iterator_to_array(): Argument #1 ($iterator) must be of type Traversable|array", 0)
			}
			return true
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
	case "xml_parse":
		return vm.intrinsicXMLParse(spec, line)
	case "curl_exec":
		return vm.intrinsicCurlExec(spec, line)
	}
	return false
}

func (vm *VM) intrinsicCurlExec(spec callSpec, line uint32) bool {
	if len(spec.args) != 1 {
		return false
	}
	handleVal := types.Deref(vm.Zone, spec.args[0])
	if !ext.HasActiveCurlCallbacks(vm.Zone, handleVal) {
		return false
	}
	step, err := ext.StepCurlExec(vm.Zone, vm.Out, handleVal)
	defer types.ReleaseValue(vm.Zone, &step.HeaderFunc)
	defer types.ReleaseValue(vm.Zone, &step.WriteFunc)
	if err != nil {
		vm.raiseNativeError(err, "curl_exec", []types.Value{handleVal}, line)
		return true
	}
	if !step.OK {
		vm.push(types.NewBool(false))
		vm.bump()
		return true
	}
	if !step.HeaderFunc.IsUndef() {
		call, ok := vm.callback(step.HeaderFunc)
		if !ok {
			ext.SetCurlWriteError(vm.Zone, handleVal)
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		for _, hdrLine := range step.HeaderLines {
			hs, herr := types.NewStringLiteral(vm.Zone, hdrLine)
			if herr != nil {
				vm.fatalFeature("curl_exec")
				return true
			}
			ret, threw := vm.invokeCallback(call, []types.Value{types.RetainValue(vm.Zone, handleVal), hs}, line)
			n := vm.toLong(ret)
			types.ReleaseValue(vm.Zone, &ret)
			if threw || vm.stop || vm.control {
				return true
			}
			if n != int64(len(hdrLine)) {
				ext.SetCurlWriteError(vm.Zone, handleVal)
				vm.push(types.NewBool(false))
				vm.bump()
				return true
			}
		}
	}
	if !step.WriteFunc.IsUndef() {
		if step.Body != "" {
			call, ok := vm.callback(step.WriteFunc)
			if !ok {
				ext.SetCurlWriteError(vm.Zone, handleVal)
				vm.push(types.NewBool(false))
				vm.bump()
				return true
			}
			bs, berr := types.NewStringLiteral(vm.Zone, step.Body)
			if berr != nil {
				vm.fatalFeature("curl_exec")
				return true
			}
			ret, threw := vm.invokeCallback(call, []types.Value{types.RetainValue(vm.Zone, handleVal), bs}, line)
			n := vm.toLong(ret)
			types.ReleaseValue(vm.Zone, &ret)
			if threw || vm.stop || vm.control {
				return true
			}
			if n != int64(len(step.Body)) {
				ext.SetCurlWriteError(vm.Zone, handleVal)
				vm.push(types.NewBool(false))
				vm.bump()
				return true
			}
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true
	}
	if step.ReturnString {
		s, serr := types.NewStringLiteral(vm.Zone, step.FullOut)
		if serr != nil {
			vm.fatalFeature("curl_exec")
			return true
		}
		vm.push(s)
		vm.bump()
		return true
	}
	vm.push(types.NewBool(true))
	vm.bump()
	return true
}

func (vm *VM) intrinsicXMLParse(spec callSpec, line uint32) bool {
	if len(spec.args) < 2 || len(spec.args) > 3 {
		return false
	}
	parserVal := types.Deref(vm.Zone, spec.args[0])
	if !ext.HasActiveXMLCallbacks(vm.Zone, parserVal) {
		return false
	}
	ds, down := vm.stringify(spec.args[1])
	if vm.stop || vm.control {
		if down {
			types.ReleaseValue(vm.Zone, &ds)
		}
		return true
	}
	data := ""
	if ds.Type == types.TypeString {
		data = strings.Clone(types.StringData(vm.Zone, ds))
	}
	if down {
		types.ReleaseValue(vm.Zone, &ds)
	}
	isFinal := true
	if len(spec.args) >= 3 {
		fv := types.Deref(vm.Zone, spec.args[2])
		if !fv.IsNull() && !fv.IsUndef() {
			isFinal = types.ToBoolZ(vm.Zone, fv)
		}
	}
	events, status := ext.StepXMLParse(vm.Zone, parserVal, data, isFinal)
	for i := range events {
		ev := &events[i]
		var call callSpec
		var ok bool
		if ev.Object.Type == types.TypeObject && ev.Handler.Type == types.TypeString {
			call = callSpec{
				kind:    callMethod,
				method:  types.StringData(vm.Zone, ev.Handler),
				obj:     ev.Object,
				hasObj:  true,
				classID: types.ObjectInfo(vm.Zone, ev.Object).ClassID,
			}
			ok = true
		} else {
			call, ok = vm.callback(ev.Handler)
		}
		if !ok {
			ext.ReleaseXMLEvent(vm.Zone, ev)
			continue
		}
		args := ev.Args
		ev.Args = nil
		ret, threw := vm.invokeCallback(call, args, line)
		types.ReleaseValue(vm.Zone, &ret)
		ext.ReleaseXMLEvent(vm.Zone, ev)
		if threw || vm.stop || vm.control {
			for k := i + 1; k < len(events); k++ {
				ext.ReleaseXMLEvent(vm.Zone, &events[k])
			}
			return true
		}
	}
	vm.push(types.NewLong(int64(status)))
	vm.bump()
	return true
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
			isRef := v.Type == types.TypeReference
			if recursive && dv.Type == types.TypeArray {
				sub, subOK := walkArray(dv)
				if !subOK {
					aborted = true
					return false
				}
				if isRef {
					// Le sous-tableau désigné par une référence se modifie dans sa cellule, visible de l'autre détenteur.
					types.AssignRef(vm.Zone, v, sub)
					_ = types.ArraySet(vm.Zone, &rebuilt, k, v, nil)
				} else {
					_ = types.ArraySet(vm.Zone, &rebuilt, k, sub, nil)
				}
				types.ReleaseValue(vm.Zone, &sub)
				return true
			}
			// Un élément déjà lié par référence est transmis par sa propre cellule, comme ZVAL_MAKE_REF en PHP.
			var vRef types.Value
			if isRef {
				vRef = types.RetainReference(vm.Zone, v)
			} else {
				var rerr error
				vRef, rerr = types.NewReference(vm.Zone, dv)
				if rerr != nil {
					aborted = true
					return false
				}
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
			if isRef {
				_ = types.ArraySet(vm.Zone, &rebuilt, k, vRef, nil)
			} else {
				_ = types.ArraySet(vm.Zone, &rebuilt, k, types.Deref(vm.Zone, vRef), nil)
			}
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
	// Une cellule de référence reste dans l'élément trié, comme les buckets que zend_hash_sort déplace sans les copier.
	types.ArrayForEach(vm.Zone, arr, func(k, v types.Value) bool {
		items = append(items, kvPair{
			k: types.RetainValue(vm.Zone, k),
			v: types.RetainValue(vm.Zone, v),
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
			argA = types.RetainValue(vm.Zone, types.Deref(vm.Zone, a.v))
			argB = types.RetainValue(vm.Zone, types.Deref(vm.Zone, b.v))
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

func (vm *VM) valueTainted(v types.Value) bool {
	d := types.Deref(vm.Zone, v)
	if (v.Flags|d.Flags)&types.FlagTainted != 0 {
		return true
	}
	return d.Type == types.TypeString && types.Flags(types.StringInfo(vm.Zone, d).Flags)&types.FlagTainted != 0
}

func (vm *VM) intrinsicPregReplaceCallback(spec callSpec, line uint32) bool {
	if len(spec.args) < 3 || len(spec.args) > 6 {
		vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("preg_replace_callback() expects between 3 and 6 arguments, %d given", len(spec.args)), 0)
		return true
	}
	// Le motif et le sujet survivent aux rappels, qui allouent et libèrent dans l'arène : copie avant libération.
	ps, pown := vm.stringify(spec.args[0])
	pat := ""
	if ps.Type == types.TypeString {
		pat = strings.Clone(types.StringData(vm.Zone, ps))
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
		subj = strings.Clone(types.StringData(vm.Zone, ss))
	}
	subjTainted := vm.valueTainted(spec.args[2]) || vm.valueTainted(ss)
	if sown {
		types.ReleaseValue(vm.Zone, &ss)
	}
	limit := -1
	if len(spec.args) >= 4 {
		limit = int(vm.toLong(spec.args[3]))
	}
	var flags int64
	if len(spec.args) >= 6 {
		flags = vm.toLong(spec.args[5])
	}
	offCap := flags&256 != 0
	nullUn := flags&512 != 0
	locs, names, ok := ext.PCREFindAll(vm.Zone, pat, subj, limit)
	if !ok {
		if len(spec.args) >= 5 && spec.args[4].Type == types.TypeReference {
			types.AssignRef(vm.Zone, spec.args[4], types.NewLong(0))
		}
		vm.push(types.NewNull())
		vm.bump()
		return true
	}
	var out strings.Builder
	last := 0
	var count int64
	outTainted := subjTainted
	for _, loc := range locs {
		if loc[0] < last {
			continue
		}
		out.WriteString(subj[last:loc[0]])
		last = loc[1]
		mArr, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("preg_replace_callback")
			return true
		}
		nGroups := len(loc) / 2
		lastMatched := 0
		if nullUn {
			lastMatched = nGroups - 1
		} else {
			for g := nGroups - 1; g >= 0; g-- {
				if loc[2*g] >= 0 {
					lastMatched = g
					break
				}
			}
		}
		for g := 0; g <= lastMatched; g++ {
			sIdx, eIdx := loc[2*g], loc[2*g+1]
			var sv types.Value
			if sIdx < 0 || eIdx < 0 {
				if nullUn {
					sv = types.NewNull()
				} else {
					sv, _ = types.NewStringLiteral(vm.Zone, "")
					if subjTainted {
						sv.Flags |= types.FlagTainted
					}
				}
			} else {
				sv, _ = types.NewStringLiteral(vm.Zone, subj[sIdx:eIdx])
				if subjTainted {
					sv.Flags |= types.FlagTainted
				}
			}
			capVal := sv
			if offCap {
				off := int64(-1)
				if sIdx >= 0 {
					off = int64(sIdx)
				}
				pair, perr := types.NewArray(vm.Zone)
				if perr != nil {
					types.ReleaseValue(vm.Zone, &sv)
					types.ReleaseValue(vm.Zone, &mArr)
					vm.fatalFeature("preg_replace_callback")
					return true
				}
				_ = types.ArrayAppend(vm.Zone, &pair, sv)
				_ = types.ArrayAppend(vm.Zone, &pair, types.NewLong(off))
				types.ReleaseValue(vm.Zone, &sv)
				capVal = pair
			}
			if g > 0 && g < len(names) && names[g] != "" {
				nk, _ := types.NewStringLiteral(vm.Zone, names[g])
				_ = types.ArraySet(vm.Zone, &mArr, nk, capVal, nil)
				types.ReleaseValue(vm.Zone, &nk)
			}
			_ = types.ArraySet(vm.Zone, &mArr, types.NewLong(int64(g)), capVal, nil)
			types.ReleaseValue(vm.Zone, &capVal)
		}
		ret, threw := vm.invokeCallback(call, []types.Value{mArr}, line)
		if threw || vm.stop || vm.control {
			types.ReleaseValue(vm.Zone, &ret)
			return true
		}
		if vm.valueTainted(ret) {
			outTainted = true
		}
		// stringify peut rendre ret lui-même sans propriété : la chaîne se lit avant de libérer ret.
		rs, rown := vm.stringify(ret)
		if vm.valueTainted(rs) {
			outTainted = true
		}
		if rs.Type == types.TypeString {
			out.WriteString(types.StringData(vm.Zone, rs))
		}
		if rown {
			types.ReleaseValue(vm.Zone, &rs)
		}
		types.ReleaseValue(vm.Zone, &ret)
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
	if outTainted {
		resStr.Flags |= types.FlagTainted
	}
	vm.push(resStr)
	vm.bump()
	return true
}

// iniDefaults reprend les valeurs de PHP 8.3 CLI lancé sans fichier ini (php -n), relevées le 2026-10-15 par ini_get_all, complétées des directives mbstring. et iconv. des extensions que la VM déclare ; une directive absente de la table n'existe pas pour ini_get.
var iniDefaults = map[string]string{
	"SMTP": "localhost", "allow_url_fopen": "1", "allow_url_include": "0", "arg_separator.input": "&",
	"arg_separator.output": "&", "assert.active": "1", "assert.bail": "0", "assert.callback": "", "assert.exception": "1",
	"assert.warning": "1", "auto_append_file": "", "auto_detect_line_endings": "0", "auto_globals_jit": "1",
	"auto_prepend_file": "", "browscap": "", "date.default_latitude": "31.7667", "date.default_longitude": "35.2333",
	"date.sunrise_zenith": "90.833333", "date.sunset_zenith": "90.833333", "date.timezone": "UTC",
	"default_charset": "UTF-8", "default_mimetype": "text/html", "default_socket_timeout": "60", "disable_classes": "",
	"disable_functions": "", "display_errors": "1", "display_startup_errors": "1", "doc_root": "", "docref_ext": "",
	"docref_root": "", "enable_dl": "1", "enable_post_data_reading": "1", "error_append_string": "", "error_log": "",
	"error_log_mode": "0644", "error_prepend_string": "", "error_reporting": "", "expose_php": "1",
	"extension_dir": "/usr/lib/php/20230831", "fiber.stack_size": "", "file_uploads": "1", "filter.default": "unsafe_raw",
	"filter.default_flags": "", "from": "", "hard_timeout": "2", "highlight.comment": "#FF8000",
	"highlight.default": "#0000BB", "highlight.html": "#000000", "highlight.keyword": "#007700",
	"highlight.string": "#DD0000", "html_errors": "0", "iconv.input_encoding": "", "iconv.internal_encoding": "",
	"iconv.output_encoding": "", "ignore_repeated_errors": "0", "ignore_repeated_source": "0", "ignore_user_abort": "0",
	"implicit_flush": "1", "include_path": ".:/usr/share/php", "input_encoding": "", "internal_encoding": "",
	"log_errors": "0", "mail.add_x_header": "0", "mail.force_extra_parameters": "", "mail.log": "",
	"mail.mixed_lf_and_crlf": "0", "max_execution_time": "0", "max_file_uploads": "20", "max_input_nesting_level": "64",
	"max_input_time": "-1", "max_input_vars": "1000", "max_multipart_body_parts": "-1", "mbstring.detect_order": "",
	"mbstring.encoding_translation": "0", "mbstring.http_input": "", "mbstring.http_output": "",
	"mbstring.internal_encoding": "", "mbstring.language": "neutral", "mbstring.regex_retry_limit": "1000000",
	"mbstring.regex_stack_limit": "100000", "mbstring.strict_detection": "0", "mbstring.substitute_character": "",
	"memory_limit": "128M", "open_basedir": "", "openssl.cafile": "", "openssl.capath": "", "output_buffering": "0",
	"output_encoding": "", "output_handler": "", "pcre.backtrack_limit": "1000000", "pcre.jit": "1",
	"pcre.recursion_limit": "100000", "post_max_size": "8M", "precision": "14", "realpath_cache_size": "4096K",
	"realpath_cache_ttl": "120", "register_argc_argv": "1", "report_memleaks": "1", "report_zend_debug": "0",
	"request_order": "", "sendmail_from": "", "sendmail_path": "/usr/sbin/sendmail -t -i", "serialize_precision": "-1",
	"session.auto_start": "0", "session.cache_expire": "180", "session.cache_limiter": "nocache",
	"session.cookie_domain": "", "session.cookie_httponly": "0", "session.cookie_lifetime": "0",
	"session.cookie_path": "/", "session.cookie_samesite": "", "session.cookie_secure": "0", "session.gc_divisor": "100",
	"session.gc_maxlifetime": "1440", "session.gc_probability": "1", "session.lazy_write": "1",
	"session.name": "PHPSESSID", "session.referer_check": "", "session.save_handler": "files",
	"session.save_path": "/var/lib/php/sessions", "session.serialize_handler": "php",
	"session.sid_bits_per_character": "4", "session.sid_length": "32", "session.trans_sid_hosts": "",
	"session.trans_sid_tags": "a=href,area=href,frame=src,form=", "session.upload_progress.cleanup": "1",
	"session.upload_progress.enabled": "1", "session.upload_progress.freq": "1%", "session.upload_progress.min_freq": "1",
	"session.upload_progress.name": "PHP_SESSION_UPLOAD_PROGRESS", "session.upload_progress.prefix": "upload_progress_",
	"session.use_cookies": "1", "session.use_only_cookies": "1", "session.use_strict_mode": "0",
	"session.use_trans_sid": "0", "short_open_tag": "1", "smtp_port": "25", "sys_temp_dir": "",
	"syslog.facility": "LOG_USER", "syslog.filter": "no-ctrl", "syslog.ident": "php", "unserialize_callback_func": "",
	"unserialize_max_depth": "4096", "upload_max_filesize": "2M", "upload_tmp_dir": "", "url_rewriter.hosts": "",
	"url_rewriter.tags": "form=", "user_agent": "", "user_dir": "", "user_ini.cache_ttl": "300",
	"user_ini.filename": ".user.ini", "variables_order": "EGPCS", "xmlrpc_error_number": "0", "xmlrpc_errors": "0",
	"zend.assertions": "1", "zend.detect_unicode": "1", "zend.enable_gc": "1", "zend.exception_ignore_args": "0",
	"zend.exception_string_param_max_len": "15", "zend.max_allowed_stack_size": "0", "zend.multibyte": "0",
	"zend.reserved_stack_size": "0", "zend.script_encoding": "", "zend.signal_check": "0", "zlib.output_compression": "0",
	"zlib.output_compression_level": "-1", "zlib.output_handler": "",
}

// iniAccess reprend, pour les directives de iniDefaults, les droits d'accès de PHP 8.3 (php -n, relevés le 2026-10-15 par ini_get_all) qui diffèrent de PHP_INI_ALL (7) : INI_USER vaut 1, INI_PERDIR 2, INI_SYSTEM 4. ini_set refuse une directive sans le bit INI_USER, ini_get_all publie la valeur.
var iniAccess = map[string]int64{
	"allow_url_fopen": 4, "allow_url_include": 4, "arg_separator.input": 6, "auto_append_file": 6, "auto_globals_jit": 6,
	"auto_prepend_file": 6, "browscap": 4, "disable_classes": 4, "disable_functions": 4, "doc_root": 4, "enable_dl": 4,
	"enable_post_data_reading": 6, "expose_php": 4, "extension_dir": 4, "file_uploads": 4, "filter.default": 6,
	"filter.default_flags": 6, "hard_timeout": 4, "mail.add_x_header": 6, "mail.force_extra_parameters": 6, "mail.log": 6,
	"mail.mixed_lf_and_crlf": 6, "max_file_uploads": 6, "max_input_nesting_level": 6, "max_input_time": 6,
	"max_input_vars": 6, "max_multipart_body_parts": 6, "openssl.cafile": 2, "openssl.capath": 2, "output_buffering": 6,
	"output_handler": 6, "post_max_size": 6, "realpath_cache_size": 4, "realpath_cache_ttl": 4, "register_argc_argv": 6,
	"request_order": 6, "sendmail_path": 4, "session.auto_start": 2, "session.upload_progress.cleanup": 2,
	"session.upload_progress.enabled": 2, "session.upload_progress.freq": 2, "session.upload_progress.min_freq": 2,
	"session.upload_progress.name": 2, "session.upload_progress.prefix": 2, "short_open_tag": 6, "sys_temp_dir": 4,
	"syslog.facility": 4, "syslog.ident": 4, "upload_max_filesize": 6, "upload_tmp_dir": 4, "user_dir": 4,
	"user_ini.cache_ttl": 4, "user_ini.filename": 4, "variables_order": 6, "xmlrpc_errors": 4,
	"zend.max_allowed_stack_size": 4, "zend.multibyte": 2, "zend.reserved_stack_size": 4, "zend.signal_check": 4,
}

// iniExtension rattache une directive à l'extension qui la déclare, d'après ini_get_all(extension) sur php -n ; les directives absentes relèvent de Core, sauf les préfixes mbstring. et iconv. rattachés à leur extension.
var iniExtension = map[string]string{
	"assert.active": "standard", "assert.bail": "standard", "assert.callback": "standard", "assert.exception": "standard",
	"assert.warning": "standard", "auto_detect_line_endings": "standard", "date.default_latitude": "date",
	"date.default_longitude": "date", "date.sunrise_zenith": "date", "date.sunset_zenith": "date",
	"date.timezone": "date", "default_socket_timeout": "standard", "filter.default": "filter",
	"filter.default_flags": "filter", "from": "standard", "openssl.cafile": "openssl", "openssl.capath": "openssl",
	"pcre.backtrack_limit": "pcre", "pcre.jit": "pcre", "pcre.recursion_limit": "pcre", "session.auto_start": "session",
	"session.cache_expire": "session", "session.cache_limiter": "session", "session.cookie_domain": "session",
	"session.cookie_httponly": "session", "session.cookie_lifetime": "session", "session.cookie_path": "session",
	"session.cookie_samesite": "session", "session.cookie_secure": "session", "session.gc_divisor": "session",
	"session.gc_maxlifetime": "session", "session.gc_probability": "session", "session.lazy_write": "session",
	"session.name": "session", "session.referer_check": "session", "session.save_handler": "session",
	"session.save_path": "session", "session.serialize_handler": "session", "session.sid_bits_per_character": "session",
	"session.sid_length": "session", "session.trans_sid_hosts": "standard", "session.trans_sid_tags": "standard",
	"session.upload_progress.cleanup": "session", "session.upload_progress.enabled": "session",
	"session.upload_progress.freq": "session", "session.upload_progress.min_freq": "session",
	"session.upload_progress.name": "session", "session.upload_progress.prefix": "session",
	"session.use_cookies": "session", "session.use_only_cookies": "session", "session.use_strict_mode": "session",
	"session.use_trans_sid": "session", "unserialize_max_depth": "standard", "url_rewriter.hosts": "standard",
	"url_rewriter.tags": "standard", "user_agent": "standard", "zlib.output_compression": "zlib",
	"zlib.output_compression_level": "zlib", "zlib.output_handler": "zlib",
}

// iniNullDefault liste les directives dont la valeur de démarrage est NULL : ini_get les rend vides, ini_get_all les publie à null tant que la requête ne les a pas écrites.
var iniNullDefault = map[string]bool{
	"assert.callback": true, "browscap": true, "fiber.stack_size": true, "filter.default_flags": true, "from": true,
	"mail.force_extra_parameters": true, "mail.log": true, "openssl.cafile": true, "openssl.capath": true,
	"output_handler": true, "sendmail_from": true, "zend.script_encoding": true,
}

// iniAccessOf rend les droits d'accès d'une directive.
func iniAccessOf(key string) int64 {
	if a, ok := iniAccess[key]; ok {
		return a
	}
	return 7
}

// iniExtensionOf rend le nom en minuscules de l'extension qui déclare une directive.
func iniExtensionOf(key string) string {
	if e, ok := iniExtension[key]; ok {
		return e
	}
	if prefix, _, ok := strings.Cut(key, "."); ok && (prefix == "mbstring" || prefix == "iconv") {
		return prefix
	}
	return "core"
}

// loadedExtensions énumère les extensions dont la VM fournit les fonctions représentatives, vérifiées par function_exists le 2026-10-15.
var loadedExtensions = []string{"Core", "date", "pcre", "zlib", "ctype", "filter", "hash", "iconv", "json", "mbstring", "random", "Reflection", "SPL", "session", "standard", "PDO", "pdo_sqlite"}

// probedExtensions associe à une extension une fonction représentative : l'extension n'est déclarée chargée que si la VM fournit cette fonction, pour ne jamais annoncer une extension dont les appels échoueraient.
var probedExtensions = []struct{ name, probe string }{
	{"mysqli", "mysqli_connect"},
	{"openssl", "openssl_encrypt"},
	{"fileinfo", "finfo_open"},
	{"tokenizer", "token_get_all"},
	{"xml", "xml_parser_create"},
	{"gd", "gd_info"},
	{"curl", "curl_init"},
}

// loadedExtensionList rend les extensions déclarées chargées : la liste fixe, puis chaque extension sondée dont la fonction représentative existe.
func loadedExtensionList() []string {
	out := slices.Clone(loadedExtensions)
	for _, e := range probedExtensions {
		if _, ok := lookupNative(e.probe); ok {
			out = append(out, e.name)
		}
	}
	return out
}

const initialErrorReporting = 32767

// iniValue lit une directive de la table de requête, à défaut sa valeur par défaut.
func (vm *VM) iniValue(key string) string {
	if key == "error_reporting" {
		if _, set := vm.ini[key]; set || vm.errorReporting != initialErrorReporting {
			return strconv.FormatInt(vm.errorReporting, 10)
		}
	}
	if v, ok := ext.IniGet(vm.Zone, key); ok {
		return v
	}
	if v, ok := vm.ini[key]; ok {
		return v
	}
	return iniDefaults[key]
}

// iniMaster rend la valeur maîtresse d'une directive : celle de la ligne de commande (-d), à défaut la valeur par défaut.
func (vm *VM) iniMaster(key string) (string, bool) {
	if vm.Context != nil {
		if v, ok := vm.Context.INIOverrides[key]; ok && iniKnown(key) {
			return v, true
		}
	}
	return iniDefaults[key], false
}

// applyINIOverrides pose, au début d'une requête, les directives de la ligne de commande connues de la VM dans
// la table de requête, l'état des extensions, le fuseau par défaut et le niveau de rapport d'erreurs ; une
// directive inconnue est ignorée comme le fait ini_get de PHP.
func (vm *VM) applyINIOverrides() {
	if vm.Context == nil || len(vm.Context.INIOverrides) == 0 {
		return
	}
	for _, key := range slices.Sorted(maps.Keys(vm.Context.INIOverrides)) {
		if !iniKnown(key) {
			continue
		}
		vm.setMasterDirective(key, vm.Context.INIOverrides[key])
	}
}

// setMasterDirective inscrit une valeur maîtresse dans la table de requête et dans les états qui en dépendent.
func (vm *VM) setMasterDirective(key, value string) {
	if key == "date.timezone" && vm.Zone != nil {
		datetime.DefaultTimezoneSet(vm.Zone, value)
	}
	ext.IniSet(vm.Zone, key, value)
	if vm.ini == nil {
		vm.ini = make(map[string]string)
	}
	vm.ini[key] = value
	if key == "error_reporting" {
		if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			vm.errorReporting = n
		} else {
			vm.errorReporting = 0
		}
	}
}

func iniKnown(key string) bool {
	_, ok := iniDefaults[key]
	return ok
}

// Niveaux d'erreur de PHP (E_ERROR consigné par les arrêts fatals) et masque que l'opérateur @ laisse actif (E_ERROR, E_PARSE, E_CORE_ERROR, E_COMPILE_ERROR, E_USER_ERROR, E_RECOVERABLE_ERROR).
const (
	eError        int64 = 1
	eWarning      int64 = 2
	eNotice       int64 = 8
	eDeprecated   int64 = 8192
	silenceMask   int64 = 4437
	diagWarning         = "Warning"
	diagNotice          = "Notice"
	diagDeprecate       = "Deprecated"
)

// silenced indique qu'une région @ est ouverte dans un cadre actif : comme error_reporting en PHP, la suppression vaut pour les fonctions appelées et les fichiers inclus depuis l'expression.
func (vm *VM) silenced() bool {
	for i := range vm.frames {
		if vm.frames[i].silence > 0 {
			return true
		}
	}
	return false
}

// effectiveErrorReporting rend le niveau error_reporting observable, réduit au masque des erreurs fatales pendant une région @.
func (vm *VM) effectiveErrorReporting() int64 {
	if vm.silenced() {
		return vm.errorReporting & silenceMask
	}
	return vm.errorReporting
}

// reportDiag consigne un diagnostic non fatal pour error_get_last puis l'affiche si error_reporting, éventuellement réduit par @, l'autorise.
func (vm *VM) reportDiag(typ int64, label, msg, file string, line uint32) {
	if vm.Zone != nil {
		stdlib.RecordError(vm.Zone, typ, msg, file, int64(line))
	}
	if vm.effectiveErrorReporting()&typ != 0 && vm.Out != nil {
		fmt.Fprintf(vm.Out, "\n%s: %s in %s on line %d\n", label, msg, file, line)
	}
}

// raiseDiag signale un diagnostic non fatal : gestionnaire de set_error_handler d'abord, appelé même sous @ comme en PHP, sinon consignation et affichage selon error_reporting. Le résultat vrai signale que le gestionnaire a levé une exception ou arrêté la requête : l'appelant abandonne alors l'instruction en cours.
func (vm *VM) raiseDiag(typ int64, label, msg string, line uint32) bool {
	if len(vm.errorHandlers) > 0 {
		top := vm.errorHandlers[len(vm.errorHandlers)-1]
		if !top.IsNull() {
			if call, ok := vm.callback(top); ok {
				mVal, _ := types.NewStringLiteral(vm.Zone, msg)
				fVal, _ := types.NewStringLiteral(vm.Zone, vm.file())
				ret, threw := vm.invokeCallback(call, []types.Value{types.NewLong(typ), mVal, fVal, types.NewLong(int64(line))}, line)
				vm.line = line
				handled := ret.Type != types.TypeFalse
				types.ReleaseValue(vm.Zone, &ret)
				if threw || handled {
					return threw || vm.stop || vm.control
				}
			}
		}
	}
	vm.reportDiag(typ, label, msg, vm.file(), line)
	return vm.stop || vm.control
}

// emitWarning signale un E_WARNING par raiseDiag ; les appelants testent vm.stop et vm.control après l'appel.
func (vm *VM) emitWarning(msg string, line uint32) {
	vm.raiseDiag(eWarning, diagWarning, msg, line)
}

// iniString convertit l'argument string|int|float|bool|null d'ini_set en chaîne comme PHP.
func (vm *VM) iniString(v types.Value) string {
	d := types.Deref(vm.Zone, v)
	switch d.Type {
	case types.TypeNull, types.TypeUndef, types.TypeFalse:
		return ""
	case types.TypeTrue:
		return "1"
	}
	s, owned := vm.stringify(d)
	out := strings.Clone(types.StringData(vm.Zone, s))
	if owned {
		types.ReleaseValue(vm.Zone, &s)
	}
	return out
}

func (vm *VM) pushString(s string) bool {
	v, err := types.NewStringLiteral(vm.Zone, s)
	if err != nil {
		vm.fatalFeature("chaîne")
		return true
	}
	vm.push(v)
	vm.bump()
	return true
}

func (vm *VM) stringArray(list []string) (types.Value, bool) {
	arr, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("tableau")
		return types.Value{}, false
	}
	for _, s := range list {
		v, err := types.NewStringLiteral(vm.Zone, s)
		if err != nil {
			types.ReleaseValue(vm.Zone, &arr)
			vm.fatalFeature("chaîne")
			return types.Value{}, false
		}
		_ = types.ArrayAppend(vm.Zone, &arr, v)
		types.ReleaseValue(vm.Zone, &v)
	}
	return arr, true
}

func (vm *VM) pushStrings(list []string) bool {
	if arr, ok := vm.stringArray(list); ok {
		vm.push(arr)
		vm.bump()
	}
	return true
}

func (vm *VM) arityError(name string, got, minArgs, maxArgs int) bool {
	if got >= minArgs && got <= maxArgs {
		return false
	}
	how, n := "exactly", minArgs
	if minArgs != maxArgs {
		how = "at least"
		if got > maxArgs {
			how, n = "at most", maxArgs
		}
	}
	word := "arguments"
	if n == 1 {
		word = "argument"
	}
	vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s() expects %s %d %s, %d given", name, how, n, word, got), 0)
	return true
}

// builtinClassIDs énumère les identifiants réservés aux classes internes de la VM, dans l'ordre de bytecode.
func builtinClassIDs() []uint32 {
	var ids []uint32
	for id := uint32(1); id < bytecode.ClassUser; id++ {
		ids = append(ids, id)
	}
	ids = append(ids, bytecode.ClassFiber, bytecode.ClassFiberError)
	for id := bytecode.ClassSplDoublyLinkedList; id <= bytecode.ClassArrayIterator; id++ {
		ids = append(ids, id)
	}
	ids = append(ids, bytecode.ClassStdClass, bytecode.ClassPDO, bytecode.ClassPDOStatement, bytecode.ClassPDOException)
	for id := bytecode.ClassDateTime; id <= bytecode.ClassDateMalformedIntervalStringException; id++ {
		ids = append(ids, id)
	}
	for id := bytecode.ClassReflectionException; id <= bytecode.ClassReflectionFunctionAbstract; id++ {
		ids = append(ids, id)
	}
	ids = append(ids, bytecode.ClassGdImage, bytecode.ClassFinfo, bytecode.ClassCurlHandle, bytecode.ClassXMLParser)
	var out []uint32
	for _, id := range ids {
		if _, ok := bytecode.BuiltinClassName(id); ok {
			out = append(out, id)
		}
	}
	return out
}

func builtinIsInterface(id uint32) bool {
	if p := builtinProto(id); p != nil {
		return p.Kind == bytecode.KindInterface
	}
	switch id {
	case bytecode.ClassThrowable, bytecode.ClassUnitEnum, bytecode.ClassBackedEnum, bytecode.ClassTraversable,
		bytecode.ClassIterator, bytecode.ClassIteratorAggregate, bytecode.ClassSeekableIterator, bytecode.ClassCountable,
		bytecode.ClassArrayAccess, bytecode.ClassJsonSerializable, bytecode.ClassStringable, bytecode.ClassDateTimeInterface,
		bytecode.ClassReflector:
		return true
	}
	return false
}

// declaredNames rend, dans l'ordre de déclaration, les noms des classes utilisateur déclarées dont le genre satisfait keep ; with alias ajoute les alias en minuscules.
func (vm *VM) declaredNames(keep func(bytecode.ClassKind) bool, withAliases bool) []string {
	var names []string
	if vm.Program == nil {
		return names
	}
	late := make(map[uint32]bool, len(vm.declOrder))
	for _, e := range vm.declOrder {
		if e.alias == "" {
			late[e.id] = true
		}
	}
	var main []*bytecode.ClassProto
	for _, list := range [][]*bytecode.ClassProto{vm.Program.Classes, vm.Program.Traits} {
		for _, c := range list {
			if c != nil && !late[c.ID] && vm.classDeclared(c) && keep(c.Kind) {
				main = append(main, c)
			}
		}
	}
	slices.SortFunc(main, func(a, b *bytecode.ClassProto) int { return int(a.ID) - int(b.ID) })
	for _, c := range main {
		names = append(names, c.Name)
	}
	for _, e := range vm.declOrder {
		if e.alias != "" {
			if withAliases {
				names = append(names, e.alias)
			}
			continue
		}
		if c := vm.classByID(e.id); c != nil && vm.classDeclared(c) && keep(c.Kind) {
			names = append(names, c.Name)
		}
	}
	return names
}

// intrinsicRuntime regroupe les primitives de cycle de vie, d'alias de classe, de configuration et d'introspection.
// intrinsicHost sert les fonctions qui interrogent l'état de la requête tenu par la VM : validité et genre d'une
// ressource, propriétés par défaut d'une classe vues depuis la portée courante, état du collecteur de cycles et
// fin de réponse anticipée des SAPI FastCGI et LiteSpeed.
func (vm *VM) intrinsicHost(name string, spec callSpec, line uint32) bool {
	switch name {
	case "is_resource":
		if vm.arityError(name, len(spec.args), 1, 1) {
			return true
		}
		_, open := vm.resourceType(types.Deref(vm.Zone, spec.args[0]))
		vm.push(types.NewBool(open))
	case "get_resource_type":
		if vm.arityError(name, len(spec.args), 1, 1) {
			return true
		}
		d := types.Deref(vm.Zone, spec.args[0])
		_, isOpaque := opaqueResourceClassID(d.Extra)
		if d.Type != types.TypeResource || isOpaque {
			vm.raiseBuiltin(bytecode.ClassTypeError, "get_resource_type(): Argument #1 ($resource) must be of type resource, "+vm.operandTypeName(d)+" given", 0)
			return true
		}
		kind, open := vm.resourceType(d)
		if !open {
			kind = "Unknown"
		}
		s, err := types.NewStringLiteral(vm.Zone, kind)
		if err != nil {
			vm.fatalFeature("chaîne")
			return true
		}
		vm.push(s)
	case "get_class_vars":
		if vm.arityError(name, len(spec.args), 1, 1) {
			return true
		}
		return vm.intrinsicGetClassVars(spec)
	case "gc_enabled":
		if vm.arityError(name, len(spec.args), 0, 0) {
			return true
		}
		vm.push(types.NewBool(!vm.gcDisabled))
	case "gc_enable", "gc_disable":
		if vm.arityError(name, len(spec.args), 0, 0) {
			return true
		}
		vm.gcDisabled = name == "gc_disable"
		vm.push(types.NewNull())
	default:
		if vm.arityError(name, len(spec.args), 0, 0) {
			return true
		}
		vm.push(types.NewBool(true))
	}
	vm.bump()
	return true
}

// resourceType rend le nom de genre qu'affiche get_resource_type et la validité d'une ressource. Un flux est
// valide tant que feof l'accepte, ce qui n'a aucun effet sur le flux ; un répertoire, que l'API des flux
// n'interroge pas sans effet, est tenu pour valide.
func (vm *VM) resourceType(v types.Value) (string, bool) {
	if v.Type != types.TypeResource {
		return "", false
	}
	if _, isOpaque := opaqueResourceClassID(v.Extra); isOpaque {
		return "", false
	}
	switch v.Extra {
	case streams.ResourceKindStream:
		feof, ok := streams.Lookup("feof")
		if !ok {
			return "stream", true
		}
		res, err := feof(vm.Zone, io.Discard, []types.Value{v})
		vmRelease(vm.Zone, &res)
		return "stream", err == nil
	case streams.ResourceKindDir:
		return "stream", true
	case streams.ResourceKindContext:
		return "stream-context", true
	}
	return "Unknown", true
}

// intrinsicGetClassVars rend, comme get_class_vars, les valeurs par défaut des propriétés d'une classe visibles
// depuis la portée courante : propriétés d'instance puis statiques, celles de la classe avant celles héritées.
func (vm *VM) intrinsicGetClassVars(spec callSpec) bool {
	s, owned := vm.stringify(spec.args[0])
	if vm.stop || vm.control {
		return true
	}
	cname := strings.Clone(types.StringData(vm.Zone, s))
	if owned {
		types.ReleaseValue(vm.Zone, &s)
	}
	var c *bytecode.ClassProto
	if id, ok := bytecode.BuiltinClassID(strings.TrimPrefix(cname, `\`)); ok {
		c = builtinProto(id)
		if c == nil {
			c = &bytecode.ClassProto{ID: id, Name: cname}
		}
	} else {
		found, ok := vm.declaredClassByName(cname, true)
		if vm.stop || vm.control {
			return true
		}
		if !ok {
			vm.raiseBuiltin(bytecode.ClassTypeError, "get_class_vars(): Argument #1 ($class) must be a valid class name, "+cname+" given", 0)
			return true
		}
		c = found
	}
	arr, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("get_class_vars")
		return true
	}
	for _, static := range [2]bool{false, true} {
		seen := map[string]bool{}
		for cur, depth := c, 0; cur != nil && depth < 64; cur, depth = vm.classByID(cur.ParentID), depth+1 {
			for _, p := range cur.Props {
				if seen[p.Name] {
					continue
				}
				seen[p.Name] = true
				if p.Static != static || !vm.visible(p.Visibility, p.ClassID) {
					continue
				}
				v := vm.propDefault(p)
				if vm.stop || vm.control {
					vmRelease(vm.Zone, &v)
					vmRelease(vm.Zone, &arr)
					return true
				}
				if v.IsUndef() {
					v = types.NewNull()
				}
				k, kerr := types.NewStringLiteral(vm.Zone, p.Name)
				if kerr == nil {
					_ = types.ArraySet(vm.Zone, &arr, k, v, nil)
					types.ReleaseValue(vm.Zone, &k)
				}
				vmRelease(vm.Zone, &v)
			}
			if cur.ParentID == 0 || cur.ParentID == bytecode.ClassNone {
				break
			}
		}
	}
	vm.push(arr)
	vm.bump()
	return true
}

func (vm *VM) intrinsicRuntime(name string, spec callSpec, line uint32) bool {
	argString := func(i int) string {
		s, owned := vm.stringify(spec.args[i])
		out := strings.Clone(types.StringData(vm.Zone, s))
		if owned {
			types.ReleaseValue(vm.Zone, &s)
		}
		return out
	}
	switch name {
	case "register_shutdown_function":
		if vm.arityError(name, len(spec.args), 1, 1<<30) {
			return true
		}
		cb := types.Deref(vm.Zone, spec.args[0])
		if _, ok := vm.callback(cb); !ok {
			msg := "register_shutdown_function(): Argument #1 ($callback) must be a valid callback"
			if cb.Type == types.TypeString && !strings.Contains(types.StringData(vm.Zone, cb), "::") {
				msg += `, function "` + types.StringData(vm.Zone, cb) + `" not found or invalid function name`
			}
			vm.raiseBuiltin(bytecode.ClassTypeError, msg, 0)
			return true
		}
		entry := shutdownCall{cb: types.RetainValue(vm.Zone, cb)}
		for _, a := range spec.args[1:] {
			entry.args = append(entry.args, types.RetainValue(vm.Zone, types.Deref(vm.Zone, a)))
		}
		vm.shutdownFuncs = append(vm.shutdownFuncs, entry)
		vm.push(types.NewNull())
		vm.bump()
		return true

	case "spl_autoload_call":
		if vm.arityError(name, len(spec.args), 1, 1) {
			return true
		}
		class := strings.TrimPrefix(argString(0), `\`)
		if _, ok := vm.declaredClassByName(class, false); !ok {
			vm.autoloadClass(class)
		}
		if vm.stop || vm.control {
			return true
		}
		vm.push(types.NewNull())
		vm.bump()
		return true

	case "class_alias":
		if vm.arityError(name, len(spec.args), 2, 3) {
			return true
		}
		orig := strings.TrimPrefix(argString(0), `\`)
		alias := strings.TrimPrefix(argString(1), `\`)
		autoload := len(spec.args) < 3 || types.ToBoolZ(vm.Zone, spec.args[2])
		var target uint32
		if id, ok := bytecode.BuiltinClassID(orig); ok {
			target = id
		} else {
			c, ok := vm.declaredClassByName(orig, autoload)
			if vm.stop || vm.control {
				return true
			}
			if !ok {
				vm.emitWarning(`Class "`+orig+`" not found`, line)
				if vm.stop || vm.control {
					return true
				}
				vm.push(types.NewBool(false))
				vm.bump()
				return true
			}
			target = c.ID
		}
		_, builtin := bytecode.BuiltinClassID(alias)
		_, inUse := vm.declaredClassByName(alias, false)
		if builtin || inUse || vm.pendingNamed(alias) {
			vm.emitWarning("Cannot declare class "+alias+", because the name is already in use", line)
			if vm.stop || vm.control {
				return true
			}
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		if vm.classAliases == nil {
			vm.classAliases = make(map[string]uint32)
		}
		vm.classAliases[strings.ToLower(alias)] = target
		vm.declOrder = append(vm.declOrder, declEntry{id: target, alias: strings.ToLower(alias)})
		if len(vm.pendingClasses) > 0 {
			vm.cloneProgram()
			vm.bindPendingAll()
		}
		vm.push(types.NewBool(true))
		vm.bump()
		return true

	case "ini_get":
		if vm.arityError(name, len(spec.args), 1, 1) {
			return true
		}
		key := argString(0)
		if !iniKnown(key) {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		return vm.pushString(vm.iniValue(key))

	case "ini_set", "ini_alter":
		if vm.arityError(name, len(spec.args), 2, 2) {
			return true
		}
		key := argString(0)
		if !iniKnown(key) || iniAccessOf(key)&1 == 0 {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		value := vm.iniString(spec.args[1])
		old := vm.iniValue(key)
		if key == "date.timezone" && !vm.setIniTimezone(old, value, line) {
			if vm.stop || vm.control {
				return true
			}
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		if warning, handled := ext.IniSet(vm.Zone, key, value); handled {
			if warning != "" {
				if vm.emitWarning("ini_set(): "+warning, line); vm.stop || vm.control {
					return true
				}
			}
			if strings.HasPrefix(key, "iconv.") {
				return vm.pushString(old)
			}
		}
		if vm.ini == nil {
			vm.ini = make(map[string]string)
		}
		vm.ini[key] = value
		if key == "error_reporting" {
			if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				vm.errorReporting = n
			} else {
				vm.errorReporting = 0
			}
		}
		return vm.pushString(old)

	case "ini_restore":
		if vm.arityError(name, len(spec.args), 1, 1) {
			return true
		}
		key := argString(0)
		master, fromCLI := vm.iniMaster(key)
		if fromCLI {
			vm.setMasterDirective(key, master)
			vm.push(types.NewNull())
			vm.bump()
			return true
		}
		if key == "date.timezone" {
			if _, set := vm.ini[key]; set {
				vm.setIniTimezone(vm.iniValue(key), master, line)
			}
		}
		if _, set := vm.ini[key]; set || strings.HasPrefix(key, "iconv.") {
			ext.IniSet(vm.Zone, key, master)
		}
		delete(vm.ini, key)
		if key == "error_reporting" {
			vm.errorReporting = initialErrorReporting
		}
		vm.push(types.NewNull())
		vm.bump()
		return true

	case "set_include_path":
		if vm.arityError(name, len(spec.args), 1, 1) {
			return true
		}
		value := argString(0)
		old := vm.iniValue("include_path")
		if value == "" {
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
		if vm.ini == nil {
			vm.ini = make(map[string]string)
		}
		vm.ini["include_path"] = value
		return vm.pushString(old)

	case "get_include_path":
		if vm.arityError(name, len(spec.args), 0, 0) {
			return true
		}
		return vm.pushString(vm.iniValue("include_path"))

	case "extension_loaded":
		if vm.arityError(name, len(spec.args), 1, 1) {
			return true
		}
		ext := argString(0)
		found := slices.ContainsFunc(loadedExtensionList(), func(e string) bool { return strings.EqualFold(e, ext) })
		vm.push(types.NewBool(found))
		vm.bump()
		return true

	case "get_loaded_extensions":
		if vm.arityError(name, len(spec.args), 0, 1) {
			return true
		}
		if len(spec.args) == 1 && types.ToBoolZ(vm.Zone, spec.args[0]) {
			return vm.pushStrings(nil)
		}
		return vm.pushStrings(loadedExtensionList())

	case "ini_get_all":
		if vm.arityError(name, len(spec.args), 0, 2) {
			return true
		}
		ext := ""
		if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
			ext = argString(0)
		}
		details := len(spec.args) < 2 || types.ToBoolZ(vm.Zone, spec.args[1])
		return vm.iniGetAll(ext, details, line)

	case "get_declared_classes", "get_declared_interfaces", "get_declared_traits":
		if vm.arityError(name, len(spec.args), 0, 0) {
			return true
		}
		var names []string
		switch name {
		case "get_declared_classes":
			for _, id := range builtinClassIDs() {
				if !builtinIsInterface(id) {
					n, _ := bytecode.BuiltinClassName(id)
					names = append(names, n)
				}
			}
			names = append(names, vm.declaredNames(func(k bytecode.ClassKind) bool { return k == bytecode.KindClass || k == bytecode.KindEnum }, true)...)
		case "get_declared_interfaces":
			for _, id := range builtinClassIDs() {
				if builtinIsInterface(id) {
					n, _ := bytecode.BuiltinClassName(id)
					names = append(names, n)
				}
			}
			names = append(names, vm.declaredNames(func(k bytecode.ClassKind) bool { return k == bytecode.KindInterface }, false)...)
		default:
			names = vm.declaredNames(func(k bytecode.ClassKind) bool { return k == bytecode.KindTrait }, false)
		}
		return vm.pushStrings(names)

	case "get_defined_functions":
		if vm.arityError(name, len(spec.args), 0, 1) {
			return true
		}
		var user []string
		for _, fn := range vm.Program.Funcs {
			if fn != nil && !bytecode.AnonymousFunc(fn.Name) {
				user = append(user, strings.ToLower(fn.Name))
			}
		}
		result, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("get_defined_functions")
			return true
		}
		for _, part := range []struct {
			key  string
			list []string
		}{{"internal", intrinsicNames()}, {"user", user}} {
			list, ok := vm.stringArray(part.list)
			if !ok {
				types.ReleaseValue(vm.Zone, &result)
				return true
			}
			k, _ := types.NewStringLiteral(vm.Zone, part.key)
			_ = types.ArraySet(vm.Zone, &result, k, list, nil)
			types.ReleaseValue(vm.Zone, &k)
			types.ReleaseValue(vm.Zone, &list)
		}
		vm.push(result)
		vm.bump()
		return true

	case "get_defined_constants":
		if vm.arityError(name, len(spec.args), 0, 1) {
			return true
		}
		categorize := len(spec.args) == 1 && types.ToBoolZ(vm.Zone, spec.args[0])
		user, err := types.NewArray(vm.Zone)
		if err != nil {
			vm.fatalFeature("get_defined_constants")
			return true
		}
		for _, key := range vm.userConsts {
			if v, ok := vm.constCache[key]; ok {
				k, _ := types.NewStringLiteral(vm.Zone, key)
				_ = types.ArraySet(vm.Zone, &user, k, v, nil)
				types.ReleaseValue(vm.Zone, &k)
			}
		}
		if !categorize {
			all, err := types.NewArray(vm.Zone)
			if err != nil {
				types.ReleaseValue(vm.Zone, &user)
				vm.fatalFeature("get_defined_constants")
				return true
			}
			for _, table := range extConstTables() {
				keys := slices.Sorted(maps.Keys(table))
				for _, key := range keys {
					k, _ := types.NewStringLiteral(vm.Zone, key)
					_ = types.ArraySet(vm.Zone, &all, k, table[key], nil)
					types.ReleaseValue(vm.Zone, &k)
				}
			}
			types.ArrayForEach(vm.Zone, user, func(k, v types.Value) bool {
				_ = types.ArraySet(vm.Zone, &all, k, v, nil)
				return true
			})
			types.ReleaseValue(vm.Zone, &user)
			vm.push(all)
			vm.bump()
			return true
		}
		result, err := types.NewArray(vm.Zone)
		if err != nil {
			types.ReleaseValue(vm.Zone, &user)
			vm.fatalFeature("get_defined_constants")
			return true
		}
		if types.ArrayLen(vm.Zone, user) > 0 {
			k, _ := types.NewStringLiteral(vm.Zone, "user")
			_ = types.ArraySet(vm.Zone, &result, k, user, nil)
			types.ReleaseValue(vm.Zone, &k)
		}
		types.ReleaseValue(vm.Zone, &user)
		vm.push(result)
		vm.bump()
		return true

	case "get_defined_vars":
		if vm.arityError(name, len(spec.args), 0, 0) {
			return true
		}
		return vm.definedVars()
	}
	return false
}

// setIniTimezone valide un fuseau écrit par ini_set ou ini_restore et le propage au fuseau par défaut de datetime, sauf si date_default_timezone_set a déjà choisi un autre fuseau que la directive courante old ; un fuseau invalide émet le Warning de PHP et rend false.
func (vm *VM) setIniTimezone(old, tz string, line uint32) bool {
	if vm.Zone == nil {
		return true
	}
	cur := datetime.DefaultTimezoneGet(vm.Zone)
	if !datetime.DefaultTimezoneSet(vm.Zone, tz) {
		vm.emitWarning("ini_set(): Invalid date.timezone value '"+tz+"', using '"+old+"' instead", line)
		return false
	}
	if cur != old {
		datetime.DefaultTimezoneSet(vm.Zone, cur)
	}
	return true
}

// iniGetAll construit le résultat de ini_get_all depuis la table de requête : toutes les directives triées par nom, ou celles d'une extension désignée par son nom en minuscules ; details donne les valeurs globale et locale et les droits d'accès.
func (vm *VM) iniGetAll(ext string, details bool, line uint32) bool {
	if ext != "" {
		known := ext == "core"
		for _, e := range loadedExtensionList() {
			if strings.ToLower(e) == ext {
				known = true
			}
		}
		if !known {
			vm.emitWarning(`ini_get_all(): Extension "`+ext+`" cannot be found`, line)
			if vm.stop || vm.control {
				return true
			}
			vm.push(types.NewBool(false))
			vm.bump()
			return true
		}
	}
	result, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("ini_get_all")
		return true
	}
	str := func(s string) types.Value {
		v, err := types.NewStringLiteral(vm.Zone, s)
		if err != nil {
			vm.fatalFeature("ini_get_all")
		}
		return v
	}
	set := func(arr *types.Value, key string, v types.Value) {
		k := str(key)
		_ = types.ArraySet(vm.Zone, arr, k, v, nil)
		types.ReleaseValue(vm.Zone, &k)
		types.ReleaseValue(vm.Zone, &v)
	}
	for _, key := range slices.Sorted(maps.Keys(iniDefaults)) {
		if ext != "" && iniExtensionOf(key) != ext {
			continue
		}
		if !details {
			if _, written := vm.ini[key]; !written && iniNullDefault[key] {
				set(&result, key, types.NewNull())
			} else {
				set(&result, key, str(vm.iniValue(key)))
			}
			continue
		}
		entry, err := types.NewArray(vm.Zone)
		if err != nil {
			types.ReleaseValue(vm.Zone, &result)
			vm.fatalFeature("ini_get_all")
			return true
		}
		global := types.NewNull()
		if master, fromCLI := vm.iniMaster(key); fromCLI || !iniNullDefault[key] {
			global = str(master)
		}
		local := types.NewNull()
		if _, written := vm.ini[key]; written || !iniNullDefault[key] {
			local = str(vm.iniValue(key))
		}
		set(&entry, "global_value", global)
		set(&entry, "local_value", local)
		set(&entry, "access", types.NewLong(iniAccessOf(key)))
		set(&result, key, entry)
	}
	vm.push(result)
	vm.bump()
	return true
}

// pendingNamed indique qu'un nom est réservé par une classe compilée en attente de son parent ou de ses traits.
func (vm *VM) pendingNamed(name string) bool {
	for _, p := range vm.pendingClasses {
		if c := vm.classByID(p.id); c != nil && strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

// intrinsicNames rend les fonctions internes portées par la VM elle-même ; les fonctions natives de stdlib, ext et streams n'exposent pas d'énumération.
func intrinsicNames() []string {
	return []string{
		"error_reporting", "func_num_args", "func_get_arg", "func_get_args", "define", "defined", "constant",
		"function_exists", "class_exists", "interface_exists", "trait_exists", "enum_exists", "is_callable",
		"call_user_func", "call_user_func_array", "compact", "extract", "get_class", "get_called_class",
		"spl_autoload_register", "spl_autoload_unregister", "spl_autoload_functions", "spl_autoload_call",
		"register_shutdown_function", "class_alias", "ini_get", "ini_set", "ini_alter", "ini_restore",
		"set_include_path", "get_include_path", "extension_loaded", "get_loaded_extensions",
		"get_defined_constants", "get_declared_classes", "get_declared_interfaces", "get_declared_traits",
		"get_defined_functions", "get_defined_vars", "ini_get_all",
	}
}

// definedVars construit le tableau de get_defined_vars pour la portée courante ; un fichier inclus voit la portée de son cadre propriétaire.
func (vm *VM) definedVars() bool {
	idx := len(vm.frames) - 1
	if sc := vm.frame().includeScope; sc > 0 {
		idx = sc - 1
	}
	fr := &vm.frames[idx]
	arr, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("get_defined_vars")
		return true
	}
	put := func(name string, v types.Value) {
		d := types.Deref(vm.Zone, v)
		if d.IsUndef() {
			return
		}
		k, err := types.NewStringLiteral(vm.Zone, name)
		if err != nil {
			return
		}
		_ = types.ArraySet(vm.Zone, &arr, k, d, nil)
		types.ReleaseValue(vm.Zone, &k)
	}
	if idx == 0 {
		for _, sg := range []string{"_GET", "_POST", "_COOKIE", "_FILES", "_SERVER"} {
			if ref := vm.superglobalRef(sg); ref.Type == types.TypeReference {
				put(sg, ref)
			}
		}
	}
	for i, name := range fr.fn.CVNames {
		if name == "" || name == "this" || name == "GLOBALS" || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "$") || isSuperglobal(name) {
			continue
		}
		put(name, vm.slots[fr.cvBase+i])
	}
	extra := fr.dynamicLocals
	if idx == 0 {
		extra = vm.globals
	}
	for _, name := range slices.Sorted(maps.Keys(extra)) {
		if slices.Contains(fr.fn.CVNames, name) || strings.ContainsRune(name, 0) || isSuperglobal(name) || name == "GLOBALS" {
			continue
		}
		put(name, extra[name])
	}
	vm.push(arr)
	vm.bump()
	return true
}

// collectTraversable parcourt un objet Traversable (Iterator, IteratorAggregate, générateur) et rend ses
// éléments dans un tableau, clés conservées si preserve ; faux si l'objet n'est pas parcourable.
func (vm *VM) collectTraversable(d types.Value, preserve bool) (types.Value, bool) {
	out, err := types.NewArray(vm.Zone)
	if err != nil {
		vm.fatalFeature("tableau")
		return types.Value{}, false
	}
	var slot iterState
	if !vm.prepareObjectIter(&slot, d) {
		types.ReleaseValue(vm.Zone, &out)
		return types.Value{}, false
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
		return types.Value{}, false
	}
	return out, true
}
