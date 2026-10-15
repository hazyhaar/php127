package vm

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/datetime"
	"code.hazyhaar.fr/devhoros/php127/ext"
	"code.hazyhaar.fr/devhoros/php127/fiber"
	"code.hazyhaar.fr/devhoros/php127/filter"
	"code.hazyhaar.fr/devhoros/php127/mysqli"
	"code.hazyhaar.fr/devhoros/php127/pdo"
	"code.hazyhaar.fr/devhoros/php127/reflection"
	"code.hazyhaar.fr/devhoros/php127/sapi"
	"code.hazyhaar.fr/devhoros/php127/session"
	"code.hazyhaar.fr/devhoros/php127/spl"
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func init() {
	registerBuiltinClassNames()
	compiler.NativeByValue = func(name string) bool {
		_, ok := lookupNative(name)
		return ok && !intrinsicExists(strings.ToLower(strings.TrimPrefix(name, `\`))) && !bytecode.NativeHasRefArgs(name)
	}
}

func vmRetain(z *zone.Zone, v types.Value) types.Value {
	if v.Type <= types.TypeDouble || v.Type == types.TypeResource {
		return v
	}
	return types.RetainValue(z, v)
}

func vmRelease(z *zone.Zone, v *types.Value) {
	if v.Type <= types.TypeDouble || v.Type == types.TypeResource {
		*v = types.Value{}
		return
	}
	types.ReleaseValue(z, v)
}

func registerBuiltinClassNames() {
	defer func() { _ = recover() }()
	if types.IsProcessZoneFrozen() {
		return
	}
	spl.EnsureClassNames()
	fiber.EnsureClassNames()
	datetime.EnsureClassNames()
	reflection.EnsureClassNames()
	mysqli.EnsureClassNames()
	ids := []uint32{
		bytecode.ClassException,
		bytecode.ClassError,
		bytecode.ClassTypeError,
		bytecode.ClassValueError,
		bytecode.ClassArithmeticError,
		bytecode.ClassDivisionByZeroError,
		bytecode.ClassUnhandledMatchError,
		bytecode.ClassArgumentCountError,
		bytecode.ClassClosure,
		bytecode.ClassGenerator,
		bytecode.ClassRuntimeException,
		bytecode.ClassLogicException,
		bytecode.ClassBadFunctionCallException,
		bytecode.ClassBadMethodCallException,
		bytecode.ClassDomainException,
		bytecode.ClassInvalidArgumentException,
		bytecode.ClassLengthException,
		bytecode.ClassOutOfBoundsException,
		bytecode.ClassOutOfRangeException,
		bytecode.ClassOverflowException,
		bytecode.ClassRangeException,
		bytecode.ClassUnderflowException,
		bytecode.ClassUnexpectedValueException,
		bytecode.ClassFiber,
		bytecode.ClassFiberError,
		bytecode.ClassSplDoublyLinkedList,
		bytecode.ClassSplStack,
		bytecode.ClassSplQueue,
		bytecode.ClassSplFixedArray,
		bytecode.ClassSplObjectStorage,
		bytecode.ClassArrayObject,
		bytecode.ClassArrayIterator,
		bytecode.ClassStdClass,
		bytecode.ClassPDO,
		bytecode.ClassPDOStatement,
		bytecode.ClassPDOException,
		bytecode.ClassDateTime,
		bytecode.ClassDateTimeImmutable,
		bytecode.ClassDateTimeZone,
		bytecode.ClassDateInterval,
		bytecode.ClassDatePeriod,
		bytecode.ClassDateTimeInterface,
		bytecode.ClassDateError,
		bytecode.ClassDateObjectError,
		bytecode.ClassDateRangeError,
		bytecode.ClassDateException,
		bytecode.ClassDateInvalidTimeZoneException,
		bytecode.ClassDateInvalidOperationException,
		bytecode.ClassDateMalformedStringException,
		bytecode.ClassDateMalformedIntervalStringException,
		bytecode.ClassReflectionException,
		bytecode.ClassReflectionClass,
		bytecode.ClassReflectionObject,
		bytecode.ClassReflectionMethod,
		bytecode.ClassReflectionFunction,
		bytecode.ClassReflectionProperty,
		bytecode.ClassReflectionParameter,
		bytecode.ClassReflectionClassConstant,
		bytecode.ClassReflectionEnum,
		bytecode.ClassReflectionEnumUnitCase,
		bytecode.ClassReflectionEnumBackedCase,
		bytecode.ClassReflectionNamedType,
		bytecode.ClassReflectionUnionType,
		bytecode.ClassReflectionIntersectionType,
		bytecode.ClassReflectionAttribute,
		bytecode.ClassReflector,
		bytecode.ClassReflectionType,
		bytecode.ClassReflectionFunctionAbstract,
		bytecode.ClassJsonSerializable,
		bytecode.ClassStringable,
		bytecode.ClassMysqli,
		bytecode.ClassMysqliResult,
		bytecode.ClassMysqliStmt,
		bytecode.ClassMysqliDriver,
		bytecode.ClassMysqliSqlException,
	}
	for _, id := range ids {
		if name, ok := bytecode.BuiltinClassName(id); ok {
			safeRegisterObjectClass(id, name)
		}
	}
}

func safeRegisterObjectClass(id uint32, name string) {
	defer func() { _ = recover() }()
	if !types.IsProcessZoneFrozen() {
		types.RegisterObjectClassName(id, name)
	}
}

func lookupNative(name string) (stdlib.NativeFunc, bool) {
	clean := strings.TrimPrefix(name, `\`)
	if fn, ok := stdlib.Lookup(clean); ok {
		return fn, true
	}
	if fn, ok := ext.Lookup(clean); ok {
		return fn, true
	}
	if fn, ok := streams.Lookup(clean); ok {
		return fn, true
	}
	if fn, ok := sapi.Lookup(clean); ok {
		return fn, true
	}
	if fn, ok := datetime.Lookup(clean); ok {
		return fn, true
	}
	if fn, ok := session.Lookup(clean); ok {
		return fn, true
	}
	if fn, ok := filter.Lookup(clean); ok {
		return fn, true
	}
	if fn, ok := mysqli.Lookup(clean); ok {
		return fn, true
	}
	switch strings.ToLower(clean) {
	case "spl_object_id":
		return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
			if len(args) != 1 {
				return types.Value{}, spl.SPLException{
					Class:   "ArgumentCountError",
					Message: fmt.Sprintf("spl_object_id() expects exactly 1 argument, %d given", len(args)),
				}
			}
			return spl.SplObjectID(z, args[0])
		}, true
	case "spl_object_hash":
		return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
			if len(args) != 1 {
				return types.Value{}, spl.SPLException{
					Class:   "ArgumentCountError",
					Message: fmt.Sprintf("spl_object_hash() expects exactly 1 argument, %d given", len(args)),
				}
			}
			return spl.SplObjectHash(z, args[0])
		}, true
	case "spl_classes":
		return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
			if len(args) != 0 {
				return types.Value{}, spl.SPLException{
					Class:   "ArgumentCountError",
					Message: fmt.Sprintf("spl_classes() expects exactly 0 arguments, %d given", len(args)),
				}
			}
			return spl.SplClasses(z)
		}, true
	}
	return nil, false
}

// mysqliClass reconnaît les classes servies par le paquet mysqli.
func mysqliClass(id uint32) bool {
	return id >= bytecode.ClassMysqli && id <= bytecode.ClassMysqliSqlException
}

// mysqliMethodArgIsRef indique les arguments d'une méthode mysqli transmis par référence : variables liées de bind_param (à partir du deuxième) et de bind_result (toutes).
func mysqliMethodArgIsRef(classID uint32, method string, idx int) bool {
	if classID != bytecode.ClassMysqliStmt {
		return false
	}
	return bytecode.NativeMethodArgIsRef(method, idx)
}

func (vm *VM) raiseNativeError(err error, funcName string, args []types.Value, line uint32) {
	if err == nil || vm.stop {
		return
	}
	tr := traceItem{file: vm.file(), line: line, name: funcName, args: vm.traceArgs(args)}
	if pe, ok := errors.AsType[pdo.PDOException](err); ok {
		obj, objErr := pdo.NewPDOExceptionObject(vm.Zone, pe)
		if objErr != nil {
			vm.fatalFeature("PDOException")
			return
		}
		fs, _ := types.NewStringLiteral(vm.Zone, vm.file())
		types.ObjectSet(vm.Zone, obj, 2, fs)
		types.ReleaseValue(vm.Zone, &fs)
		types.ObjectSet(vm.Zone, obj, 3, types.NewLong(int64(line)))
		pc := 0
		if len(vm.frames) > 0 {
			pc = vm.frame().pc
		}
		vm.excTrace = []traceItem{tr}
		vm.throwAt(pc, obj)
		return
	}
	if me, ok := errors.AsType[mysqli.SQLException](err); ok {
		obj, objErr := mysqli.NewSQLExceptionObject(vm.Zone, me)
		if objErr != nil {
			vm.fatalFeature("mysqli_sql_exception")
			return
		}
		fs, _ := types.NewStringLiteral(vm.Zone, vm.file())
		types.ObjectSet(vm.Zone, obj, 2, fs)
		types.ReleaseValue(vm.Zone, &fs)
		types.ObjectSet(vm.Zone, obj, 3, types.NewLong(int64(line)))
		pc := 0
		if len(vm.frames) > 0 {
			pc = vm.frame().pc
		}
		vm.excTrace = []traceItem{tr}
		vm.throwAt(pc, obj)
		return
	}
	if te, ok := errors.AsType[fiber.ThrownException](err); ok && !te.Exception.IsUndef() {
		pc := 0
		if len(vm.frames) > 0 {
			pc = vm.frame().pc
		}
		vm.excTrace = []traceItem{tr}
		vm.throwAt(pc, te.Exception)
		return
	}
	if fe, ok := errors.AsType[fiber.FiberError](err); ok {
		vm.raiseBuiltin(bytecode.ClassFiberError, fe.Message, 0, tr)
		return
	}
	if se, ok := errors.AsType[spl.SPLException](err); ok {
		classID, found := bytecode.BuiltinClassID(se.Class)
		if !found {
			classID = bytecode.ClassRuntimeException
		}
		vm.raiseBuiltin(classID, se.Message, 0, tr)
		return
	}
	if de, ok := errors.AsType[datetime.DateError](err); ok {
		classID, found := bytecode.BuiltinClassID(de.Class)
		if !found {
			classID = bytecode.ClassDateException
		}
		vm.raiseBuiltin(classID, de.Message, 0, tr)
		return
	}
	if re, ok := errors.AsType[reflection.ReflectionException](err); ok {
		vm.raiseBuiltin(bytecode.ClassReflectionException, re.Message, 0, tr)
		return
	}
	if rte, ok := errors.AsType[reflection.TypeError](err); ok {
		vm.raiseBuiltin(bytecode.ClassTypeError, rte.Message, 0, tr)
		return
	}
	if rve, ok := errors.AsType[reflection.ValueError](err); ok {
		vm.raiseBuiltin(bytecode.ClassValueError, rve.Message, 0, tr)
		return
	}
	if rer, ok := errors.AsType[reflection.Error](err); ok {
		vm.raiseBuiltin(bytecode.ClassError, rer.Message, 0, tr)
		return
	}
	if sse, ok := errors.AsType[session.SessionError](err); ok {
		classID, found := bytecode.BuiltinClassID(sse.Class)
		if !found {
			classID = bytecode.ClassError
		}
		vm.raiseBuiltin(classID, sse.Message, 0, tr)
		return
	}
	if fle, ok := errors.AsType[filter.FilterError](err); ok {
		classID, found := bytecode.BuiltinClassID(fle.Class)
		if !found {
			classID = bytecode.ClassError
		}
		vm.raiseBuiltin(classID, fle.Message, 0, tr)
		return
	}
	cls := stdlib.ExceptionClass(err)
	if cls == "" {
		cls = ext.ExceptionClass(err)
	}
	if cls == "" {
		cls = streams.ExceptionClass(err)
	}
	if cls == "" {
		cls = sapi.ExceptionClass(err)
	}
	if cls == "" {
		cls = pdo.ExceptionClass(err)
	}
	if cls == "" {
		cls = mysqli.ExceptionClass(err)
	}
	classID, ok := bytecode.BuiltinClassID(cls)
	if !ok {
		classID = bytecode.ClassError
	}
	vm.raiseBuiltin(classID, err.Error(), 0, tr)
}

func (vm *VM) lookupExtConst(name string) (types.Value, bool) {
	switch name {
	case "SORT_REGULAR", "COUNT_NORMAL":
		return types.NewLong(0), true
	case "SORT_NUMERIC", "COUNT_RECURSIVE", "CASE_UPPER":
		return types.NewLong(1), true
	case "SORT_STRING":
		return types.NewLong(2), true
	case "SORT_DESC":
		return types.NewLong(3), true
	case "SORT_ASC":
		return types.NewLong(4), true
	case "SORT_NATURAL":
		return types.NewLong(spl.SORT_NATURAL), true
	case "SORT_FLAG_CASE":
		return types.NewLong(spl.SORT_FLAG_CASE), true
	case "CASE_LOWER":
		return types.NewLong(0), true
	case "PHP_SESSION_DISABLED":
		return types.NewLong(session.PHP_SESSION_DISABLED), true
	case "PHP_SESSION_NONE":
		return types.NewLong(session.PHP_SESSION_NONE), true
	case "PHP_SESSION_ACTIVE":
		return types.NewLong(session.PHP_SESSION_ACTIVE), true
	case "JSON_HEX_TAG":
		return types.NewLong(1), true
	case "JSON_HEX_AMP":
		return types.NewLong(2), true
	case "JSON_HEX_APOS":
		return types.NewLong(4), true
	case "JSON_HEX_QUOT":
		return types.NewLong(8), true
	case "JSON_FORCE_OBJECT":
		return types.NewLong(16), true
	case "JSON_UNESCAPED_SLASHES":
		return types.NewLong(64), true
	case "JSON_PRETTY_PRINT":
		return types.NewLong(128), true
	case "JSON_UNESCAPED_UNICODE":
		return types.NewLong(256), true
	case "JSON_THROW_ON_ERROR":
		return types.NewLong(4194304), true
	case "DIRECTORY_SEPARATOR":
		v, err := types.NewStringLiteral(vm.Zone, "/")
		if err != nil {
			vm.fatalFeature("constante")
		}
		return v, true
	case "PATH_SEPARATOR":
		v, err := types.NewStringLiteral(vm.Zone, ":")
		if err != nil {
			vm.fatalFeature("constante")
		}
		return v, true
	}
	if iv, ok := filter.Constants()[name]; ok {
		return types.NewLong(iv), true
	}
	for _, table := range extConstTables() {
		if v, ok := table[name]; ok {
			if v.Type == types.TypeString {
				s, err := types.NewStringLiteral(vm.Zone, types.StringData(vm.Zone, v))
				if err != nil {
					vm.fatalFeature("constante")
					return types.NewNull(), false
				}
				if vm.constCache == nil {
					vm.constCache = make(map[string]types.Value)
				}
				vm.constCache[name] = types.RetainValue(vm.Zone, s)
				return s, true
			}
			return types.RetainValue(vm.Zone, v), true
		}
	}
	return types.Value{}, false
}

// extConstTables énumère, dans l'ordre de priorité de la résolution, les tables de constantes globales exportées par les paquets natifs.
func extConstTables() []map[string]types.Value {
	return []map[string]types.Value{ext.Constants(), streams.Constants(), sapi.Constants(), stdlib.Constants(), mysqli.Constants()}
}

func (vm *VM) lookupNativeClassConst(classID uint32, name string) (types.Value, bool) {
	if classID == bytecode.ClassPDO || vm.inherits(classID, bytecode.ClassPDO) {
		if name == "ERR_NONE" {
			v, err := types.NewStringLiteral(vm.Zone, pdo.ERR_NONE)
			if err != nil {
				vm.fatalFeature("constante de classe")
				return types.NewNull(), false
			}
			return v, true
		}
		if v, ok := pdo.LookupClassConstant("PDO", name); ok {
			return types.RetainValue(vm.Zone, v), true
		}
	}
	if classID == bytecode.ClassSplDoublyLinkedList || vm.inherits(classID, bytecode.ClassSplDoublyLinkedList) {
		switch name {
		case "IT_MODE_FIFO":
			return types.NewLong(spl.IT_MODE_FIFO), true
		case "IT_MODE_LIFO":
			return types.NewLong(spl.IT_MODE_LIFO), true
		case "IT_MODE_KEEP":
			return types.NewLong(spl.IT_MODE_KEEP), true
		case "IT_MODE_DELETE":
			return types.NewLong(spl.IT_MODE_DELETE), true
		}
	}
	if classID == bytecode.ClassArrayObject || classID == bytecode.ClassArrayIterator ||
		vm.inherits(classID, bytecode.ClassArrayObject) || vm.inherits(classID, bytecode.ClassArrayIterator) {
		switch name {
		case "STD_PROP_LIST":
			return types.NewLong(spl.STD_PROP_LIST), true
		case "ARRAY_AS_PROPS":
			return types.NewLong(spl.ARRAY_AS_PROPS), true
		}
	}
	if classID == bytecode.ClassDateTime || classID == bytecode.ClassDateTimeImmutable ||
		classID == bytecode.ClassDateTimeInterface || vm.inherits(classID, bytecode.ClassDateTime) ||
		vm.inherits(classID, bytecode.ClassDateTimeImmutable) {
		var s string
		switch name {
		case "ATOM":
			s = datetime.ATOM
		case "COOKIE":
			s = datetime.COOKIE
		case "ISO8601":
			s = datetime.ISO8601
		case "ISO8601_EXPANDED":
			s = datetime.ISO8601_EXPANDED
		case "RFC822":
			s = datetime.RFC822
		case "RFC850":
			s = datetime.RFC850
		case "RFC1036":
			s = datetime.RFC1036
		case "RFC1123":
			s = datetime.RFC1123
		case "RFC7231":
			s = datetime.RFC7231
		case "RFC2822":
			s = datetime.RFC2822
		case "RFC3339":
			s = datetime.RFC3339
		case "RFC3339_EXTENDED":
			s = datetime.RFC3339_EXTENDED
		case "RSS":
			s = datetime.RSS
		case "W3C":
			s = datetime.W3C
		}
		if s != "" {
			v, err := types.NewStringLiteral(vm.Zone, s)
			if err != nil {
				vm.fatalFeature("constante de classe")
				return types.NewNull(), false
			}
			return v, true
		}
	}
	if classID == bytecode.ClassDateTimeZone || vm.inherits(classID, bytecode.ClassDateTimeZone) {
		switch name {
		case "AFRICA":
			return types.NewLong(1), true
		case "AMERICA":
			return types.NewLong(2), true
		case "ANTARCTICA":
			return types.NewLong(4), true
		case "ARCTIC":
			return types.NewLong(8), true
		case "ASIA":
			return types.NewLong(16), true
		case "ATLANTIC":
			return types.NewLong(32), true
		case "AUSTRALIA":
			return types.NewLong(64), true
		case "EUROPE":
			return types.NewLong(128), true
		case "INDIAN":
			return types.NewLong(256), true
		case "PACIFIC":
			return types.NewLong(512), true
		case "UTC":
			return types.NewLong(1024), true
		case "ALL":
			return types.NewLong(2047), true
		case "ALL_WITH_BC":
			return types.NewLong(4095), true
		case "PER_COUNTRY":
			return types.NewLong(4096), true
		}
	}
	if classID == bytecode.ClassDatePeriod || vm.inherits(classID, bytecode.ClassDatePeriod) {
		switch name {
		case "EXCLUDE_START_DATE":
			return types.NewLong(datetime.EXCLUDE_START_DATE), true
		case "INCLUDE_END_DATE":
			return types.NewLong(datetime.INCLUDE_END_DATE), true
		}
	}
	if classID == bytecode.ClassReflectionClass || classID == bytecode.ClassReflectionObject ||
		classID == bytecode.ClassReflectionEnum || vm.inherits(classID, bytecode.ClassReflectionClass) {
		switch name {
		case "IS_IMPLICIT_ABSTRACT":
			return types.NewLong(reflection.ClassIsImplicitAbstract), true
		case "IS_EXPLICIT_ABSTRACT":
			return types.NewLong(reflection.ClassIsExplicitAbstract), true
		case "IS_FINAL":
			return types.NewLong(reflection.ClassIsFinal), true
		case "IS_READONLY":
			return types.NewLong(reflection.ClassIsReadOnly), true
		}
	}
	if classID == bytecode.ClassReflectionMethod || vm.inherits(classID, bytecode.ClassReflectionMethod) {
		switch name {
		case "IS_PUBLIC":
			return types.NewLong(reflection.MethodIsPublic), true
		case "IS_PROTECTED":
			return types.NewLong(reflection.MethodIsProtected), true
		case "IS_PRIVATE":
			return types.NewLong(reflection.MethodIsPrivate), true
		case "IS_STATIC":
			return types.NewLong(reflection.MethodIsStatic), true
		case "IS_FINAL":
			return types.NewLong(reflection.MethodIsFinal), true
		case "IS_ABSTRACT":
			return types.NewLong(reflection.MethodIsAbstract), true
		}
	}
	if classID == bytecode.ClassReflectionProperty || vm.inherits(classID, bytecode.ClassReflectionProperty) {
		switch name {
		case "IS_PUBLIC":
			return types.NewLong(reflection.PropertyIsPublic), true
		case "IS_PROTECTED":
			return types.NewLong(reflection.PropertyIsProtected), true
		case "IS_PRIVATE":
			return types.NewLong(reflection.PropertyIsPrivate), true
		case "IS_STATIC":
			return types.NewLong(reflection.PropertyIsStatic), true
		case "IS_READONLY":
			return types.NewLong(reflection.PropertyIsReadOnly), true
		}
	}
	if classID == bytecode.ClassReflectionClassConstant || classID == bytecode.ClassReflectionEnumUnitCase ||
		classID == bytecode.ClassReflectionEnumBackedCase || vm.inherits(classID, bytecode.ClassReflectionClassConstant) {
		switch name {
		case "IS_PUBLIC":
			return types.NewLong(reflection.ConstantIsPublic), true
		case "IS_PROTECTED":
			return types.NewLong(reflection.ConstantIsProtected), true
		case "IS_PRIVATE":
			return types.NewLong(reflection.ConstantIsPrivate), true
		case "IS_FINAL":
			return types.NewLong(reflection.ConstantIsFinal), true
		}
	}
	if classID == bytecode.ClassReflectionAttribute || vm.inherits(classID, bytecode.ClassReflectionAttribute) {
		if name == "IS_INSTANCEOF" {
			return types.NewLong(reflection.AttributeIsInstanceOf), true
		}
	}
	return types.Value{}, false
}

func (vm *VM) extIntrinsic(spec callSpec, line uint32) bool {
	name := strings.ToLower(strings.TrimPrefix(spec.method, `\`))
	if name == "var_dump" && len(spec.args) >= 1 && vm.hasEnumValue(spec.args) {
		vm.varDumpEnumIntrinsic(spec)
		return true
	}
	if name != "count" && name != "sizeof" {
		return false
	}
	if len(spec.args) < 1 || len(spec.args) > 2 {
		return false
	}
	d := types.Deref(vm.Zone, spec.args[0])
	if d.Type != types.TypeObject {
		return false
	}
	classID := types.ObjectInfo(vm.Zone, d).ClassID
	if !vm.isInstance(classID, bytecode.ClassCountable) {
		return false
	}
	defer vm.releaseArgs(spec.args)
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		dll, err := spl.DLLFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, name, spec.args, line)
			return true
		}
		vm.push(types.NewLong(int64(dll.Count())))
		vm.bump()
		return true
	case bytecode.ClassSplFixedArray:
		fa, err := spl.FixedArrayFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, name, spec.args, line)
			return true
		}
		vm.push(types.NewLong(int64(fa.Count())))
		vm.bump()
		return true
	case bytecode.ClassSplObjectStorage:
		os, err := spl.ObjectStorageFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, name, spec.args, line)
			return true
		}
		vm.push(types.NewLong(int64(os.Count())))
		vm.bump()
		return true
	case bytecode.ClassArrayObject:
		ao, err := spl.ArrayObjectFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, name, spec.args, line)
			return true
		}
		vm.push(types.NewLong(int64(ao.Count(vm.Zone))))
		vm.bump()
		return true
	case bytecode.ClassArrayIterator:
		ai, err := spl.ArrayIteratorFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, name, spec.args, line)
			return true
		}
		vm.push(types.NewLong(int64(ai.Count(vm.Zone))))
		vm.bump()
		return true
	default:
		if m, _, ok := vm.findMethod(classID, "count"); ok {
			ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, nil, classID, m.ClassID, line)
			if threw || vm.stop {
				return true
			}
			vm.control = false
			n := vm.toLong(ret)
			types.ReleaseValue(vm.Zone, &ret)
			vm.push(types.NewLong(n))
			vm.bump()
			return true
		}
		return false
	}
}

func (vm *VM) doNativeNew(classID uint32, spec callSpec, line uint32) bool {
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		defer vm.releaseArgs(spec.args)
		if len(spec.args) != 0 {
			cname, _ := bytecode.BuiltinClassName(classID)
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::__construct() expects exactly 0 arguments, %d given", cname, len(spec.args)), 0)
			return true
		}
		var obj types.Value
		var err error
		switch classID {
		case bytecode.ClassSplStack:
			obj, _, err = spl.NewSplStack(vm.Zone)
		case bytecode.ClassSplQueue:
			obj, _, err = spl.NewSplQueue(vm.Zone)
		default:
			obj, _, err = spl.NewSplDoublyLinkedList(vm.Zone)
		}
		if err != nil {
			vm.raiseNativeError(err, "__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassSplFixedArray:
		defer vm.releaseArgs(spec.args)
		if len(spec.args) > 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplFixedArray::__construct() expects at most 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		size := 0
		if len(spec.args) == 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
			size = int(vm.toLong(spec.args[0]))
		}
		obj, _, err := spl.NewSplFixedArray(vm.Zone, size)
		if err != nil {
			vm.raiseNativeError(err, "SplFixedArray::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassSplObjectStorage:
		defer vm.releaseArgs(spec.args)
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::__construct() expects exactly 0 arguments, %d given", len(spec.args)), 0)
			return true
		}
		obj, _, err := spl.NewSplObjectStorage(vm.Zone)
		if err != nil {
			vm.raiseNativeError(err, "SplObjectStorage::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassArrayObject:
		defer vm.releaseArgs(spec.args)
		if len(spec.args) > 3 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::__construct() expects at most 3 arguments, %d given", len(spec.args)), 0)
			return true
		}
		initial := types.NewNull()
		if len(spec.args) >= 1 {
			initial = spec.args[0]
		}
		flags := int64(0)
		if len(spec.args) >= 2 {
			flags = vm.toLong(spec.args[1])
		}
		iterClass := "ArrayIterator"
		if len(spec.args) >= 3 {
			s, own := vm.stringify(spec.args[2])
			iterClass = types.StringData(vm.Zone, s)
			if own {
				types.ReleaseValue(vm.Zone, &s)
			}
		}
		obj, _, err := spl.NewArrayObject(vm.Zone, initial, flags, iterClass)
		if err != nil {
			vm.raiseNativeError(err, "ArrayObject::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassArrayIterator:
		defer vm.releaseArgs(spec.args)
		if len(spec.args) > 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::__construct() expects at most 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		initial := types.NewNull()
		if len(spec.args) >= 1 {
			initial = spec.args[0]
		}
		flags := int64(0)
		if len(spec.args) >= 2 {
			flags = vm.toLong(spec.args[1])
		}
		obj, _, err := spl.NewArrayIterator(vm.Zone, initial, flags)
		if err != nil {
			vm.raiseNativeError(err, "ArrayIterator::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassMysqli, bytecode.ClassMysqliStmt, bytecode.ClassMysqliDriver:
		defer vm.releaseArgs(spec.args)
		name, _ := bytecode.BuiltinClassName(classID)
		obj, err := mysqli.Construct(vm.Zone, vm.Out, name, vm.derefArgs(spec.args))
		if err != nil {
			vm.raiseNativeError(err, name+"::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassMysqliResult:
		defer vm.releaseArgs(spec.args)
		vm.raiseBuiltin(bytecode.ClassError, "Cannot directly construct mysqli_result, use mysqli_query() or mysqli::query() instead", 0)
		return true

	case bytecode.ClassPDO:
		defer vm.releaseArgs(spec.args)
		obj, err := pdo.NewPDO(vm.Zone, spec.args...)
		if err != nil {
			vm.raiseNativeError(err, "PDO::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassFiber:
		defer vm.releaseArgs(spec.args)
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("Fiber::__construct() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		call, ok := vm.callback(spec.args[0])
		if !ok {
			vm.raiseBuiltin(bytecode.ClassTypeError, "Fiber::__construct(): Argument #1 ($callback) must be a valid callback", 0)
			return true
		}
		if call.hasObj {
			call.obj = types.RetainValue(vm.Zone, call.obj)
		}
		cb := vm.makeFiberCallback(call, line)
		obj, f, err := fiber.NewObject(vm.Zone, cb)
		if err != nil {
			if call.hasObj {
				types.ReleaseValue(vm.Zone, &call.obj)
			}
			vm.raiseNativeError(err, "Fiber::__construct", spec.args, line)
			return true
		}
		if call.hasObj {
			if vm.fiberCallbacks == nil {
				vm.fiberCallbacks = make(map[*fiber.Fiber]types.Value)
			}
			vm.fiberCallbacks[f] = call.obj
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassDateTime, bytecode.ClassDateTimeImmutable, bytecode.ClassDateTimeZone,
		bytecode.ClassDateInterval, bytecode.ClassDatePeriod:
		return vm.doDateTimeNew(classID, spec, line)

	case bytecode.ClassReflectionClass, bytecode.ClassReflectionObject, bytecode.ClassReflectionMethod,
		bytecode.ClassReflectionFunction, bytecode.ClassReflectionProperty, bytecode.ClassReflectionParameter,
		bytecode.ClassReflectionClassConstant, bytecode.ClassReflectionEnum,
		bytecode.ClassReflectionEnumUnitCase, bytecode.ClassReflectionEnumBackedCase:
		return vm.doReflectionNew(classID, spec, line)
	}
	return false
}

type fiberStackSnap struct {
	frames    []frame
	tailSlots []types.Value
	used      int
	pending   []callSpec
	control   bool
	line      uint32
	retVal    types.Value
	hasRet    bool
	thrown    types.Value
	hasThrow  bool
	excTrace  []traceItem
}

func (vm *VM) rootCVCount() int {
	if len(vm.frames) == 0 || vm.frames[0].fn == nil {
		return 0
	}
	return max(vm.frames[0].fn.NumCVs, 0)
}

func (vm *VM) saveStackSnap() fiberStackSnap {
	rootCVs := vm.rootCVCount()
	var tail []types.Value
	if vm.used > rootCVs {
		tail = make([]types.Value, vm.used-rootCVs)
		copy(tail, vm.slots[rootCVs:vm.used])
		clear(vm.slots[rootCVs:vm.used])
	}
	frames := make([]frame, len(vm.frames))
	copy(frames, vm.frames)
	var pending []callSpec
	if len(vm.pending) > 0 {
		pending = make([]callSpec, len(vm.pending))
		copy(pending, vm.pending)
	}
	snap := fiberStackSnap{
		frames:    frames,
		tailSlots: tail,
		used:      vm.used,
		pending:   pending,
		control:   vm.control,
		line:      vm.line,
		retVal:    vm.retVal,
		hasRet:    vm.hasRet,
		thrown:    vm.thrown,
		hasThrow:  vm.hasThrow,
		excTrace:  vm.excTrace,
	}
	if len(vm.frames) > 0 {
		root := vm.frames[0]
		root.evalBase = rootCVs
		root.evalTop = rootCVs
		root.span = rootCVs
		vm.frames = []frame{root}
	}
	vm.used = rootCVs
	vm.pending = nil
	vm.control = false
	vm.retVal = types.Value{}
	vm.hasRet = false
	vm.thrown = types.Value{}
	vm.hasThrow = false
	vm.excTrace = nil
	return snap
}

func (vm *VM) restoreStackSnap(snap fiberStackSnap) {
	rootCVs := vm.rootCVCount()
	if snap.used > len(vm.slots) {
		next := make([]types.Value, snap.used)
		copy(next, vm.slots[:rootCVs])
		vm.slots = next
	}
	clear(vm.slots[rootCVs:])
	if len(snap.tailSlots) > 0 {
		copy(vm.slots[rootCVs:rootCVs+len(snap.tailSlots)], snap.tailSlots)
	}
	vm.used = snap.used
	vm.frames = snap.frames
	vm.pending = snap.pending
	vm.control = snap.control
	vm.line = snap.line
	vm.retVal = snap.retVal
	vm.hasRet = snap.hasRet
	vm.thrown = snap.thrown
	vm.hasThrow = snap.hasThrow
	vm.excTrace = snap.excTrace
}

func (vm *VM) releaseStackSnap(snap *fiberStackSnap) {
	rootCVs := vm.rootCVCount()
	saved := vm.saveStackSnap()
	vm.restoreStackSnap(*snap)
	for len(vm.frames) > 1 {
		vm.releaseFrameValues()
		vm.popFrame()
	}
	for _, sp := range vm.pending {
		vm.releaseArgs(sp.args)
		vm.releaseSpecObj(sp)
	}
	vm.pending = nil
	if vm.hasRet {
		types.ReleaseValue(vm.Zone, &vm.retVal)
		vm.hasRet = false
	}
	if vm.hasThrow {
		types.ReleaseValue(vm.Zone, &vm.thrown)
		vm.hasThrow = false
	}
	clear(vm.slots[rootCVs:])
	vm.restoreStackSnap(saved)
	*snap = fiberStackSnap{}
}

func (vm *VM) makeFiberCallback(call callSpec, line uint32) fiber.Callback {
	return func(z *zone.Zone, args []types.Value) (types.Value, error) {
		inv := call
		inv.args = make([]types.Value, len(args))
		for i, a := range args {
			inv.args[i] = types.RetainValue(z, a)
		}
		if inv.hasObj {
			inv.obj = types.RetainValue(z, inv.obj)
		}
		var ret types.Value
		var threw bool
		switch inv.kind {
		case callClosure:
			obj := types.Deref(z, inv.obj)
			idx := int(types.ObjectGet(z, obj, 0).ToLong())
			data := vm.closures[idx]
			ret, threw = vm.invokeClosure(data, inv.args, line)
			types.ReleaseValue(z, &inv.obj)
		case callFunc:
			if inv.flags&bytecode.FlagNative == 0 {
				fn := vm.Program.Funcs[inv.funcIndex]
				ret, threw = vm.invokeOpArray(fn, types.Value{}, false, inv.args, 0, 0, line)
			} else if isPotentialIntrinsic(inv.method) {
				ret, threw = vm.invokeNativeNested(inv, line)
			} else if native, ok := lookupNative(inv.method); ok {
				dargs := make([]types.Value, len(inv.args))
				for i, a := range inv.args {
					dargs[i] = types.Deref(z, a)
				}
				val, err := native(z, vm.Out, dargs)
				vm.releaseArgs(inv.args)
				if err != nil {
					if len(vm.fiberCallerStack) > 0 {
						caller := vm.fiberCallerStack[len(vm.fiberCallerStack)-1]
						vm.fiberCallerStack = vm.fiberCallerStack[:len(vm.fiberCallerStack)-1]
						vm.restoreStackSnap(caller)
					}
					return types.NewNull(), err
				}
				ret = val
			} else {
				vm.releaseArgs(inv.args)
			}
		default:
			if m, _, ok := vm.findMethod(inv.classID, inv.method); ok {
				ret, threw = vm.invokeOpArray(m.Func, inv.obj, inv.hasObj, inv.args, inv.classID, m.ClassID, line)
			} else {
				vm.releaseArgs(inv.args)
				vm.releaseSpecObj(inv)
			}
		}
		var uncaughtObj types.Value
		if vm.hasThrow {
			uncaughtObj = vm.thrown
			vm.hasThrow = false
			vm.thrown = types.Value{}
			threw = true
		}
		for len(vm.frames) > 1 {
			vm.releaseFrameValues()
			vm.popFrame()
		}
		if len(vm.fiberCallerStack) > 0 {
			caller := vm.fiberCallerStack[len(vm.fiberCallerStack)-1]
			vm.fiberCallerStack = vm.fiberCallerStack[:len(vm.fiberCallerStack)-1]
			vm.restoreStackSnap(caller)
		}
		if threw {
			if !uncaughtObj.IsUndef() {
				return types.NewNull(), fiber.ThrownException{Exception: uncaughtObj, Message: "Uncaught exception in fiber"}
			}
			return types.NewNull(), fiber.FiberError{Message: "Uncaught exception in fiber"}
		}
		defer types.ReleaseValue(z, &ret)
		return ret, nil
	}
}

func (vm *VM) nativeClassMethod(spec callSpec, called uint32, line uint32) bool {
	if !spec.hasObj {
		return vm.nativeStaticMethod(spec, called, line)
	}
	obj := types.Deref(vm.Zone, spec.obj)
	if obj.Type != types.TypeObject {
		return false
	}
	classID := types.ObjectInfo(vm.Zone, obj).ClassID
	switch classID {
	case bytecode.ClassPDO:
		vm.callPDOInstance(obj, spec, line)
		return true
	case bytecode.ClassPDOStatement:
		vm.callPDOStmtInstance(obj, spec, line)
		return true
	case bytecode.ClassMysqli, bytecode.ClassMysqliResult, bytecode.ClassMysqliStmt:
		vm.callMysqliInstance(obj, classID, spec, line)
		return true
	case bytecode.ClassFiber:
		vm.callFiberInstance(obj, spec, line)
		return true
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		vm.callDLLInstance(obj, classID, spec, line)
		return true
	case bytecode.ClassSplFixedArray:
		vm.callFixedArrayInstance(obj, spec, line)
		return true
	case bytecode.ClassSplObjectStorage:
		vm.callObjectStorageInstance(obj, spec, line)
		return true
	case bytecode.ClassArrayObject:
		vm.callArrayObjectInstance(obj, spec, line)
		return true
	case bytecode.ClassArrayIterator:
		vm.callArrayIteratorInstance(obj, spec, line)
		return true
	case bytecode.ClassDateTime, bytecode.ClassDateTimeImmutable, bytecode.ClassDateTimeZone,
		bytecode.ClassDateInterval, bytecode.ClassDatePeriod:
		vm.callDateTimeInstance(obj, classID, spec, line)
		return true
	case bytecode.ClassReflectionClass, bytecode.ClassReflectionObject, bytecode.ClassReflectionMethod,
		bytecode.ClassReflectionFunction, bytecode.ClassReflectionProperty, bytecode.ClassReflectionParameter,
		bytecode.ClassReflectionClassConstant, bytecode.ClassReflectionEnum,
		bytecode.ClassReflectionEnumUnitCase, bytecode.ClassReflectionEnumBackedCase,
		bytecode.ClassReflectionNamedType, bytecode.ClassReflectionUnionType,
		bytecode.ClassReflectionIntersectionType, bytecode.ClassReflectionAttribute:
		vm.callReflectionInstance(obj, classID, spec, line)
		return true
	}
	return false
}

func (vm *VM) nativeStaticMethod(spec callSpec, called uint32, line uint32) bool {
	classID := spec.classID
	if classID == 0 {
		classID = called
	}
	switch classID {
	case bytecode.ClassDateTime, bytecode.ClassDateTimeImmutable, bytecode.ClassDateTimeZone, bytecode.ClassDateInterval:
		return vm.callDateTimeStatic(classID, spec, line)
	case bytecode.ClassPDO:
		if fn, ok := pdo.LookupStaticMethod("PDO", spec.method); ok {
			defer vm.releaseArgs(spec.args)
			val, err := fn(vm.Zone, vm.Out, spec.args)
			if err != nil {
				vm.raiseNativeError(err, "PDO::"+spec.method, spec.args, line)
				return true
			}
			vm.push(val)
			vm.bump()
			return true
		}
	case bytecode.ClassSplFixedArray:
		if strings.EqualFold(spec.method, "fromArray") {
			defer vm.releaseArgs(spec.args)
			if len(spec.args) < 1 || len(spec.args) > 2 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplFixedArray::fromArray() expects at least 1 argument, %d given", len(spec.args)), 0)
				return true
			}
			preserve := true
			if len(spec.args) == 2 {
				preserve = types.ToBoolZ(vm.Zone, spec.args[1])
			}
			obj, _, err := spl.FixedArrayFromArray(vm.Zone, spec.args[0], preserve)
			if err != nil {
				vm.raiseNativeError(err, "SplFixedArray::fromArray", spec.args, line)
				return true
			}
			vm.push(obj)
			vm.bump()
			return true
		}
	case bytecode.ClassFiber:
		switch strings.ToLower(spec.method) {
		case "suspend":
			if len(spec.args) > 1 {
				vm.releaseArgs(spec.args)
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("Fiber::suspend() expects at most 1 argument, %d given", len(spec.args)), 0)
				return true
			}
			val := types.NewNull()
			if len(spec.args) == 1 {
				val = types.RetainValue(vm.Zone, types.Deref(vm.Zone, spec.args[0]))
			}
			vm.releaseArgs(spec.args)
			cur := fiber.GetCurrent(vm.Zone)
			if cur == nil {
				types.ReleaseValue(vm.Zone, &val)
				vm.raiseNativeError(fiber.FiberError{Message: "Cannot suspend outside of a fiber"}, "Fiber::suspend", nil, line)
				return true
			}
			if vm.fiberSuspended == nil {
				vm.fiberSuspended = make(map[*fiber.Fiber]fiberStackSnap)
			}
			vm.fiberSuspended[cur] = vm.saveStackSnap()
			if len(vm.fiberCallerStack) > 0 {
				caller := vm.fiberCallerStack[len(vm.fiberCallerStack)-1]
				vm.fiberCallerStack = vm.fiberCallerStack[:len(vm.fiberCallerStack)-1]
				vm.restoreStackSnap(caller)
			}
			resumed, err := fiber.Suspend(vm.Zone, val)
			types.ReleaseValue(vm.Zone, &val)
			if err != nil {
				vm.raiseNativeError(err, "Fiber::suspend", nil, line)
				return true
			}
			vm.push(types.RetainValue(vm.Zone, resumed))
			vm.bump()
			return true
		case "getcurrent":
			defer vm.releaseArgs(spec.args)
			if len(spec.args) != 0 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("Fiber::getCurrent() expects exactly 0 arguments, %d given", len(spec.args)), 0)
				return true
			}
			curObj := fiber.GetCurrentObject(vm.Zone)
			vm.push(types.RetainValue(vm.Zone, curObj))
			vm.bump()
			return true
		}
	}
	return false
}

func (vm *VM) callPDOInstance(obj types.Value, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	val, err := pdo.CallPDOMethod(vm.Zone, obj, spec.method, spec.args...)
	if err != nil {
		vm.raiseNativeError(err, "PDO::"+spec.method, spec.args, line)
		return
	}
	vm.push(val)
	vm.bump()
}

func (vm *VM) callPDOStmtInstance(obj types.Value, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	val, err := pdo.CallStmtMethod(vm.Zone, obj, spec.method, spec.args...)
	if err != nil {
		vm.raiseNativeError(err, "PDOStatement::"+spec.method, spec.args, line)
		return
	}
	vm.push(val)
	vm.bump()
}

// callMysqliInstance délègue une méthode d'instance mysqli au paquet ; les variables liées de bind_param et bind_result restent des références, les autres arguments sont déréférencés.
func (vm *VM) callMysqliInstance(obj types.Value, classID uint32, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	args := make([]types.Value, len(spec.args))
	for i, a := range spec.args {
		if mysqliMethodArgIsRef(classID, spec.method, i) && a.Type == types.TypeReference {
			args[i] = a
		} else {
			args[i] = types.Deref(vm.Zone, a)
		}
	}
	name, _ := bytecode.BuiltinClassName(classID)
	val, err := mysqli.CallMethod(vm.Zone, vm.Out, obj, spec.method, args)
	if err != nil {
		vmRelease(vm.Zone, &val)
		vm.raiseNativeError(err, name+"->"+spec.method, spec.args, line)
		return
	}
	vm.push(val)
	vm.bump()
}

// derefArgs rend une copie déréférencée des arguments, sans transfert de propriété.
func (vm *VM) derefArgs(args []types.Value) []types.Value {
	out := make([]types.Value, len(args))
	for i, a := range args {
		out[i] = types.Deref(vm.Zone, a)
	}
	return out
}

// mysqliPropGet lit une propriété native d'un objet mysqli ; une erreur du paquet est levée en exception et la lecture rend null.
func (vm *VM) mysqliPropGet(obj types.Value, name string) (types.Value, bool) {
	v, found, err := mysqli.GetProperty(vm.Zone, obj, name)
	if err != nil {
		vmRelease(vm.Zone, &v)
		vm.raiseNativeError(err, name, nil, vm.line)
		return types.NewNull(), true
	}
	return v, found
}

func (vm *VM) callFiberInstance(obj types.Value, spec callSpec, line uint32) {
	f, err := fiber.FromObject(vm.Zone, obj)
	if err != nil {
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
		vm.raiseNativeError(err, "Fiber::"+spec.method, spec.args, line)
		return
	}
	method := strings.ToLower(spec.method)
	switch method {
	case "isstarted", "issuspended", "isrunning", "isterminated":
		defer vm.releaseArgs(spec.args)
		defer vm.releaseSpecObj(spec)
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("Fiber::%s() expects exactly 0 arguments, %d given", spec.method, len(spec.args)), 0)
			return
		}
		var b bool
		switch method {
		case "isstarted":
			b = f.IsStarted()
		case "issuspended":
			b = f.IsSuspended()
		case "isrunning":
			b = f.IsRunning()
		case "isterminated":
			b = f.IsTerminated()
		}
		vm.push(types.NewBool(b))
		vm.bump()
	case "getreturn":
		defer vm.releaseArgs(spec.args)
		defer vm.releaseSpecObj(spec)
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("Fiber::getReturn() expects exactly 0 arguments, %d given", len(spec.args)), 0)
			return
		}
		ret, err := f.GetReturn(vm.Zone)
		if err != nil {
			vm.raiseNativeError(err, "Fiber::getReturn", spec.args, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, ret))
		vm.bump()
	case "start":
		args := make([]types.Value, len(spec.args))
		for i, a := range spec.args {
			args[i] = types.RetainValue(vm.Zone, types.Deref(vm.Zone, a))
		}
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
		defer vm.releaseArgs(args)
		if f.State() != fiber.StateUnstarted {
			vm.raiseNativeError(fiber.FiberError{Message: "Cannot start a fiber that has already been started"}, "Fiber::start", nil, line)
			return
		}
		vm.fiberCallerStack = append(vm.fiberCallerStack, vm.saveStackSnap())
		val, err := f.Start(vm.Zone, args...)
		if err != nil {
			vm.raiseNativeError(err, "Fiber::start", nil, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, val))
		vm.bump()
	case "resume":
		var arg types.Value
		if len(spec.args) > 1 {
			vm.releaseArgs(spec.args)
			vm.releaseSpecObj(spec)
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("Fiber::resume() expects at most 1 argument, %d given", len(spec.args)), 0)
			return
		}
		if len(spec.args) == 1 {
			arg = types.RetainValue(vm.Zone, types.Deref(vm.Zone, spec.args[0]))
		} else {
			arg = types.NewNull()
		}
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
		defer types.ReleaseValue(vm.Zone, &arg)
		if f.State() != fiber.StateSuspended {
			vm.raiseNativeError(fiber.FiberError{Message: "Cannot resume a fiber that is not suspended"}, "Fiber::resume", nil, line)
			return
		}
		vm.fiberCallerStack = append(vm.fiberCallerStack, vm.saveStackSnap())
		if snap, ok := vm.fiberSuspended[f]; ok {
			delete(vm.fiberSuspended, f)
			vm.restoreStackSnap(snap)
		}
		val, err := f.Resume(vm.Zone, arg)
		if err != nil {
			vm.raiseNativeError(err, "Fiber::resume", nil, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, val))
		vm.bump()
	case "throw":
		if len(spec.args) != 1 {
			vm.releaseArgs(spec.args)
			vm.releaseSpecObj(spec)
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("Fiber::throw() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		exc := types.Deref(vm.Zone, spec.args[0])
		if exc.Type != types.TypeObject || !vm.isInstance(types.ObjectInfo(vm.Zone, exc).ClassID, bytecode.ClassThrowable) {
			vm.releaseArgs(spec.args)
			vm.releaseSpecObj(spec)
			vm.raiseBuiltin(bytecode.ClassTypeError, "Fiber::throw(): Argument #1 ($exception) must be of type Throwable", 0)
			return
		}
		retainedExc := types.RetainValue(vm.Zone, exc)
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
		if f.State() != fiber.StateSuspended {
			types.ReleaseValue(vm.Zone, &retainedExc)
			vm.raiseNativeError(fiber.FiberError{Message: "Cannot resume a fiber that is not suspended"}, "Fiber::throw", nil, line)
			return
		}
		vm.fiberCallerStack = append(vm.fiberCallerStack, vm.saveStackSnap())
		if snap, ok := vm.fiberSuspended[f]; ok {
			delete(vm.fiberSuspended, f)
			vm.restoreStackSnap(snap)
		}
		val, err := f.ThrowValue(vm.Zone, retainedExc)
		if err != nil {
			vm.raiseNativeError(err, "Fiber::throw", nil, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, val))
		vm.bump()
	default:
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
		vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method Fiber::"+spec.method+"()", 0)
	}
}

func (vm *VM) callDLLInstance(obj types.Value, classID uint32, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	dll, err := spl.DLLFromObject(vm.Zone, obj)
	if err != nil {
		vm.raiseNativeError(err, spec.method, spec.args, line)
		return
	}
	cname, _ := bytecode.BuiltinClassName(classID)
	switch strings.ToLower(spec.method) {
	case "__construct":
		vm.push(types.NewNull())
		vm.bump()
	case "push", "enqueue":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::%s() expects exactly 1 argument, %d given", cname, spec.method, len(spec.args)), 0)
			return
		}
		dll.Push(vm.Zone, spec.args[0])
		vm.push(types.NewNull())
		vm.bump()
	case "pop":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::pop() expects exactly 0 arguments, %d given", cname, len(spec.args)), 0)
			return
		}
		v, err := dll.Pop(vm.Zone)
		if err != nil {
			vm.raiseNativeError(err, cname+"::pop", spec.args, line)
			return
		}
		vm.push(v)
		vm.bump()
	case "shift", "dequeue":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::%s() expects exactly 0 arguments, %d given", cname, spec.method, len(spec.args)), 0)
			return
		}
		v, err := dll.Shift(vm.Zone)
		if err != nil {
			vm.raiseNativeError(err, cname+"::"+spec.method, spec.args, line)
			return
		}
		vm.push(v)
		vm.bump()
	case "unshift":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::unshift() expects exactly 1 argument, %d given", cname, len(spec.args)), 0)
			return
		}
		dll.Unshift(vm.Zone, spec.args[0])
		vm.push(types.NewNull())
		vm.bump()
	case "top":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::top() expects exactly 0 arguments, %d given", cname, len(spec.args)), 0)
			return
		}
		v, err := dll.Top()
		if err != nil {
			vm.raiseNativeError(err, cname+"::top", spec.args, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, v))
		vm.bump()
	case "bottom":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::bottom() expects exactly 0 arguments, %d given", cname, len(spec.args)), 0)
			return
		}
		v, err := dll.Bottom()
		if err != nil {
			vm.raiseNativeError(err, cname+"::bottom", spec.args, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, v))
		vm.bump()
	case "count":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::count() expects exactly 0 arguments, %d given", cname, len(spec.args)), 0)
			return
		}
		vm.push(types.NewLong(int64(dll.Count())))
		vm.bump()
	case "isempty":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::isEmpty() expects exactly 0 arguments, %d given", cname, len(spec.args)), 0)
			return
		}
		vm.push(types.NewBool(dll.IsEmpty()))
		vm.bump()
	case "setiteratormode":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::setIteratorMode() expects exactly 1 argument, %d given", cname, len(spec.args)), 0)
			return
		}
		m, err := dll.SetIteratorMode(vm.toLong(spec.args[0]))
		if err != nil {
			vm.raiseNativeError(err, cname+"::setIteratorMode", spec.args, line)
			return
		}
		vm.push(types.NewLong(m))
		vm.bump()
	case "getiteratormode":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::getIteratorMode() expects exactly 0 arguments, %d given", cname, len(spec.args)), 0)
			return
		}
		vm.push(types.NewLong(dll.GetIteratorMode()))
		vm.bump()
	case "offsetexists":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::offsetExists() expects exactly 1 argument, %d given", cname, len(spec.args)), 0)
			return
		}
		vm.push(types.NewBool(dll.OffsetExists(int(vm.toLong(spec.args[0])))))
		vm.bump()
	case "offsetget":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::offsetGet() expects exactly 1 argument, %d given", cname, len(spec.args)), 0)
			return
		}
		v, err := dll.OffsetGet(int(vm.toLong(spec.args[0])))
		if err != nil {
			vm.raiseNativeError(err, cname+"::offsetGet", spec.args, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, v))
		vm.bump()
	case "offsetset":
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::offsetSet() expects exactly 2 arguments, %d given", cname, len(spec.args)), 0)
			return
		}
		if err := dll.OffsetSetValue(vm.Zone, spec.args[0], spec.args[1]); err != nil {
			vm.raiseNativeError(err, cname+"::offsetSet", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "offsetunset":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::offsetUnset() expects exactly 1 argument, %d given", cname, len(spec.args)), 0)
			return
		}
		if err := dll.OffsetUnset(vm.Zone, int(vm.toLong(spec.args[0]))); err != nil {
			vm.raiseNativeError(err, cname+"::offsetUnset", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "add":
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::add() expects exactly 2 arguments, %d given", cname, len(spec.args)), 0)
			return
		}
		if err := dll.Add(vm.Zone, int(vm.toLong(spec.args[0])), spec.args[1]); err != nil {
			vm.raiseNativeError(err, cname+"::add", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "rewind":
		dll.Rewind()
		vm.push(types.NewNull())
		vm.bump()
	case "valid":
		vm.push(types.NewBool(dll.Valid()))
		vm.bump()
	case "current":
		vm.push(types.RetainValue(vm.Zone, dll.Current()))
		vm.bump()
	case "key":
		vm.push(types.NewLong(int64(dll.Key())))
		vm.bump()
	case "next":
		dll.Next(vm.Zone)
		vm.push(types.NewNull())
		vm.bump()
	case "prev":
		dll.Prev(vm.Zone)
		vm.push(types.NewNull())
		vm.bump()
	default:
		vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method "+cname+"::"+spec.method+"()", 0)
	}
}

func (vm *VM) callFixedArrayInstance(obj types.Value, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	fa, err := spl.FixedArrayFromObject(vm.Zone, obj)
	if err != nil {
		vm.raiseNativeError(err, "SplFixedArray::"+spec.method, spec.args, line)
		return
	}
	switch strings.ToLower(spec.method) {
	case "__construct":
		size := 0
		if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
			size = int(vm.toLong(spec.args[0]))
		}
		if _, err := fa.SetSize(vm.Zone, size); err != nil {
			vm.raiseNativeError(err, "SplFixedArray::__construct", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "count", "getsize":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplFixedArray::%s() expects exactly 0 arguments, %d given", spec.method, len(spec.args)), 0)
			return
		}
		vm.push(types.NewLong(int64(fa.Count())))
		vm.bump()
	case "setsize":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplFixedArray::setSize() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		ok, err := fa.SetSize(vm.Zone, int(vm.toLong(spec.args[0])))
		if err != nil {
			vm.raiseNativeError(err, "SplFixedArray::setSize", spec.args, line)
			return
		}
		vm.push(types.NewBool(ok))
		vm.bump()
	case "toarray":
		if len(spec.args) != 0 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplFixedArray::toArray() expects exactly 0 arguments, %d given", len(spec.args)), 0)
			return
		}
		arr, err := fa.ToArray(vm.Zone)
		if err != nil {
			vm.raiseNativeError(err, "SplFixedArray::toArray", spec.args, line)
			return
		}
		vm.push(arr)
		vm.bump()
	case "offsetexists":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplFixedArray::offsetExists() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		vm.push(types.NewBool(fa.OffsetExists(int(vm.toLong(spec.args[0])))))
		vm.bump()
	case "offsetget":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplFixedArray::offsetGet() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		v, err := fa.OffsetGet(int(vm.toLong(spec.args[0])))
		if err != nil {
			vm.raiseNativeError(err, "SplFixedArray::offsetGet", spec.args, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, v))
		vm.bump()
	case "offsetset":
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplFixedArray::offsetSet() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return
		}
		if types.Deref(vm.Zone, spec.args[0]).IsNull() {
			vm.raiseBuiltin(bytecode.ClassRuntimeException, "Index invalid or out of range", 0)
			return
		}
		if err := fa.OffsetSet(vm.Zone, int(vm.toLong(spec.args[0])), spec.args[1]); err != nil {
			vm.raiseNativeError(err, "SplFixedArray::offsetSet", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "offsetunset":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplFixedArray::offsetUnset() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		if err := fa.OffsetUnset(vm.Zone, int(vm.toLong(spec.args[0]))); err != nil {
			vm.raiseNativeError(err, "SplFixedArray::offsetUnset", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "rewind":
		fa.Rewind()
		vm.push(types.NewNull())
		vm.bump()
	case "valid":
		vm.push(types.NewBool(fa.Valid()))
		vm.bump()
	case "current":
		vm.push(types.RetainValue(vm.Zone, fa.Current()))
		vm.bump()
	case "key":
		vm.push(types.NewLong(int64(fa.Key())))
		vm.bump()
	case "next":
		fa.Next()
		vm.push(types.NewNull())
		vm.bump()
	default:
		vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method SplFixedArray::"+spec.method+"()", 0)
	}
}

func (vm *VM) callObjectStorageInstance(obj types.Value, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	os, err := spl.ObjectStorageFromObject(vm.Zone, obj)
	if err != nil {
		vm.raiseNativeError(err, "SplObjectStorage::"+spec.method, spec.args, line)
		return
	}
	switch strings.ToLower(spec.method) {
	case "__construct":
		vm.push(types.NewNull())
		vm.bump()
	case "attach", "offsetset":
		if len(spec.args) < 1 || len(spec.args) > 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::%s() expects at least 1 argument, %d given", spec.method, len(spec.args)), 0)
			return
		}
		info := types.NewNull()
		if len(spec.args) == 2 {
			info = spec.args[1]
		}
		if err := os.Attach(vm.Zone, spec.args[0], info); err != nil {
			vm.raiseNativeError(err, "SplObjectStorage::"+spec.method, spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "detach", "offsetunset":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::%s() expects exactly 1 argument, %d given", spec.method, len(spec.args)), 0)
			return
		}
		if err := os.Detach(vm.Zone, spec.args[0]); err != nil {
			vm.raiseNativeError(err, "SplObjectStorage::"+spec.method, spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "contains", "offsetexists":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::%s() expects exactly 1 argument, %d given", spec.method, len(spec.args)), 0)
			return
		}
		ok, err := os.Contains(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "SplObjectStorage::"+spec.method, spec.args, line)
			return
		}
		vm.push(types.NewBool(ok))
		vm.bump()
	case "offsetget":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::offsetGet() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		v, err := os.OffsetGet(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "SplObjectStorage::offsetGet", spec.args, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, v))
		vm.bump()
	case "addall":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::addAll() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		other, err := spl.ObjectStorageFromObject(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "SplObjectStorage::addAll", spec.args, line)
			return
		}
		vm.push(types.NewLong(int64(os.AddAll(vm.Zone, other))))
		vm.bump()
	case "removeall":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::removeAll() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		other, err := spl.ObjectStorageFromObject(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "SplObjectStorage::removeAll", spec.args, line)
			return
		}
		vm.push(types.NewLong(int64(os.RemoveAll(vm.Zone, other))))
		vm.bump()
	case "removeallexcept":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::removeAllExcept() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		other, err := spl.ObjectStorageFromObject(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "SplObjectStorage::removeAllExcept", spec.args, line)
			return
		}
		vm.push(types.NewLong(int64(os.RemoveAllExcept(vm.Zone, other))))
		vm.bump()
	case "count":
		vm.push(types.NewLong(int64(os.Count())))
		vm.bump()
	case "getinfo":
		vm.push(types.RetainValue(vm.Zone, os.GetInfo()))
		vm.bump()
	case "setinfo":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::setInfo() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		os.SetInfo(vm.Zone, spec.args[0])
		vm.push(types.NewNull())
		vm.bump()
	case "gethash":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("SplObjectStorage::getHash() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		h, err := os.GetHash(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "SplObjectStorage::getHash", spec.args, line)
			return
		}
		vm.push(h)
		vm.bump()
	case "rewind":
		os.Rewind()
		vm.push(types.NewNull())
		vm.bump()
	case "valid":
		vm.push(types.NewBool(os.Valid()))
		vm.bump()
	case "current":
		vm.push(types.RetainValue(vm.Zone, os.Current()))
		vm.bump()
	case "key":
		vm.push(types.NewLong(int64(os.Key())))
		vm.bump()
	case "next":
		os.Next()
		vm.push(types.NewNull())
		vm.bump()
	default:
		vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method SplObjectStorage::"+spec.method+"()", 0)
	}
}

func (vm *VM) makeSPLComparator(cbVal types.Value, fnName string, line uint32) (spl.Comparator, bool) {
	call, ok := vm.callback(cbVal)
	if !ok {
		vm.raiseBuiltin(bytecode.ClassTypeError, fnName+"(): Argument #1 ($callback) must be a valid callback", 0)
		return nil, false
	}
	return func(z *zone.Zone, a, b types.Value) (int, error) {
		inv := call
		inv.args = []types.Value{types.RetainValue(z, a), types.RetainValue(z, b)}
		if inv.hasObj {
			inv.obj = types.RetainValue(z, inv.obj)
		}
		var ret types.Value
		var threw bool
		switch inv.kind {
		case callClosure:
			obj := types.Deref(z, inv.obj)
			idx := int(types.ObjectGet(z, obj, 0).ToLong())
			data := vm.closures[idx]
			ret, threw = vm.invokeClosure(data, inv.args, line)
			types.ReleaseValue(z, &inv.obj)
		case callFunc:
			if inv.flags&bytecode.FlagNative == 0 {
				fn := vm.Program.Funcs[inv.funcIndex]
				ret, threw = vm.invokeOpArray(fn, types.Value{}, false, inv.args, 0, 0, line)
			} else if isPotentialIntrinsic(inv.method) {
				ret, threw = vm.invokeNativeNested(inv, line)
			} else if native, found := lookupNative(inv.method); found {
				dargs := []types.Value{types.Deref(z, a), types.Deref(z, b)}
				vm.releaseArgs(inv.args)
				val, err := native(z, vm.Out, dargs)
				if err != nil {
					return 0, err
				}
				ret = val
			} else {
				vm.releaseArgs(inv.args)
			}
		default:
			if m, _, found := vm.findMethod(inv.classID, inv.method); found {
				ret, threw = vm.invokeOpArray(m.Func, inv.obj, inv.hasObj, inv.args, inv.classID, m.ClassID, line)
			} else {
				vm.releaseArgs(inv.args)
				vm.releaseSpecObj(inv)
			}
		}
		if threw || vm.stop {
			return 0, errors.New("comparator threw")
		}
		vm.control = false
		cmp := int(vm.toLong(ret))
		types.ReleaseValue(z, &ret)
		return cmp, nil
	}, true
}

func (vm *VM) callArrayObjectInstance(obj types.Value, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	ao, err := spl.ArrayObjectFromObject(vm.Zone, obj)
	if err != nil {
		vm.raiseNativeError(err, "ArrayObject::"+spec.method, spec.args, line)
		return
	}
	switch strings.ToLower(spec.method) {
	case "__construct":
		vm.push(types.NewNull())
		vm.bump()
	case "append":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::append() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		if err := ao.Append(vm.Zone, spec.args[0]); err != nil {
			vm.raiseNativeError(err, "ArrayObject::append", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "count":
		vm.push(types.NewLong(int64(ao.Count(vm.Zone))))
		vm.bump()
	case "getflags":
		vm.push(types.NewLong(ao.GetFlags()))
		vm.bump()
	case "setflags":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::setFlags() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		ao.SetFlags(vm.toLong(spec.args[0]))
		vm.push(types.NewNull())
		vm.bump()
	case "getiteratorclass":
		s, err := types.NewString(vm.Zone, ao.GetIteratorClass(), 0)
		if err != nil {
			vm.fatalFeature("chaîne")
			return
		}
		vm.push(s)
		vm.bump()
	case "setiteratorclass":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::setIteratorClass() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		s, own := vm.stringify(spec.args[0])
		ao.SetIteratorClass(types.StringData(vm.Zone, s))
		if own {
			types.ReleaseValue(vm.Zone, &s)
		}
		vm.push(types.NewNull())
		vm.bump()
	case "getarraycopy":
		arr, err := ao.GetArrayCopy(vm.Zone)
		if err != nil {
			vm.raiseNativeError(err, "ArrayObject::getArrayCopy", spec.args, line)
			return
		}
		vm.push(arr)
		vm.bump()
	case "exchangearray":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::exchangeArray() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		old, err := ao.ExchangeArray(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "ArrayObject::exchangeArray", spec.args, line)
			return
		}
		vm.push(old)
		vm.bump()
	case "getiterator":
		itObj, _, err := ao.GetIterator(vm.Zone)
		if err != nil {
			vm.raiseNativeError(err, "ArrayObject::getIterator", spec.args, line)
			return
		}
		vm.push(itObj)
		vm.bump()
	case "offsetexists":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::offsetExists() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		ok, err := ao.OffsetExists(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "ArrayObject::offsetExists", spec.args, line)
			return
		}
		vm.push(types.NewBool(ok))
		vm.bump()
	case "offsetget":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::offsetGet() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		v, err := ao.OffsetGet(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "ArrayObject::offsetGet", spec.args, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)))
		vm.bump()
	case "offsetset":
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::offsetSet() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return
		}
		if err := ao.OffsetSet(vm.Zone, spec.args[0], spec.args[1]); err != nil {
			vm.raiseNativeError(err, "ArrayObject::offsetSet", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "offsetunset":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::offsetUnset() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		if err := ao.OffsetUnset(vm.Zone, spec.args[0]); err != nil {
			vm.raiseNativeError(err, "ArrayObject::offsetUnset", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "asort":
		flags := int64(spl.SORT_REGULAR)
		if len(spec.args) >= 1 {
			flags = vm.toLong(spec.args[0])
		}
		if err := ao.Asort(vm.Zone, flags); err != nil {
			vm.raiseNativeError(err, "ArrayObject::asort", spec.args, line)
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "ksort":
		flags := int64(spl.SORT_REGULAR)
		if len(spec.args) >= 1 {
			flags = vm.toLong(spec.args[0])
		}
		if err := ao.Ksort(vm.Zone, flags); err != nil {
			vm.raiseNativeError(err, "ArrayObject::ksort", spec.args, line)
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "natsort":
		if err := ao.Natsort(vm.Zone); err != nil {
			vm.raiseNativeError(err, "ArrayObject::natsort", spec.args, line)
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "natcasesort":
		if err := ao.Natcasesort(vm.Zone); err != nil {
			vm.raiseNativeError(err, "ArrayObject::natcasesort", spec.args, line)
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "uasort":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::uasort() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		cmp, ok := vm.makeSPLComparator(spec.args[0], "ArrayObject::uasort", line)
		if !ok {
			return
		}
		if err := ao.Uasort(vm.Zone, cmp); err != nil {
			if !vm.stop && !vm.control {
				vm.raiseNativeError(err, "ArrayObject::uasort", spec.args, line)
			}
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "uksort":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayObject::uksort() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		cmp, ok := vm.makeSPLComparator(spec.args[0], "ArrayObject::uksort", line)
		if !ok {
			return
		}
		if err := ao.Uksort(vm.Zone, cmp); err != nil {
			if !vm.stop && !vm.control {
				vm.raiseNativeError(err, "ArrayObject::uksort", spec.args, line)
			}
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	default:
		vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method ArrayObject::"+spec.method+"()", 0)
	}
}

func (vm *VM) callArrayIteratorInstance(obj types.Value, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	ai, err := spl.ArrayIteratorFromObject(vm.Zone, obj)
	if err != nil {
		vm.raiseNativeError(err, "ArrayIterator::"+spec.method, spec.args, line)
		return
	}
	switch strings.ToLower(spec.method) {
	case "__construct":
		vm.push(types.NewNull())
		vm.bump()
	case "append":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::append() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		if err := ai.Append(vm.Zone, spec.args[0]); err != nil {
			vm.raiseNativeError(err, "ArrayIterator::append", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "count":
		vm.push(types.NewLong(int64(ai.Count(vm.Zone))))
		vm.bump()
	case "getflags":
		vm.push(types.NewLong(ai.GetFlags()))
		vm.bump()
	case "setflags":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::setFlags() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		ai.SetFlags(vm.toLong(spec.args[0]))
		vm.push(types.NewNull())
		vm.bump()
	case "getarraycopy":
		arr, err := ai.GetArrayCopy(vm.Zone)
		if err != nil {
			vm.raiseNativeError(err, "ArrayIterator::getArrayCopy", spec.args, line)
			return
		}
		vm.push(arr)
		vm.bump()
	case "offsetexists":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::offsetExists() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		ok, err := ai.OffsetExists(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "ArrayIterator::offsetExists", spec.args, line)
			return
		}
		vm.push(types.NewBool(ok))
		vm.bump()
	case "offsetget":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::offsetGet() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		v, err := ai.OffsetGet(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "ArrayIterator::offsetGet", spec.args, line)
			return
		}
		vm.push(types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)))
		vm.bump()
	case "offsetset":
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::offsetSet() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return
		}
		if err := ai.OffsetSet(vm.Zone, spec.args[0], spec.args[1]); err != nil {
			vm.raiseNativeError(err, "ArrayIterator::offsetSet", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "offsetunset":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::offsetUnset() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		if err := ai.OffsetUnset(vm.Zone, spec.args[0]); err != nil {
			vm.raiseNativeError(err, "ArrayIterator::offsetUnset", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "rewind":
		ai.Rewind(vm.Zone)
		vm.push(types.NewNull())
		vm.bump()
	case "valid":
		vm.push(types.NewBool(ai.Valid(vm.Zone)))
		vm.bump()
	case "current":
		vm.push(types.RetainValue(vm.Zone, types.Deref(vm.Zone, ai.Current(vm.Zone))))
		vm.bump()
	case "key":
		vm.push(types.RetainValue(vm.Zone, ai.Key(vm.Zone)))
		vm.bump()
	case "next":
		ai.Next(vm.Zone)
		vm.push(types.NewNull())
		vm.bump()
	case "seek":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::seek() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		if err := ai.Seek(vm.Zone, int(vm.toLong(spec.args[0]))); err != nil {
			vm.raiseNativeError(err, "ArrayIterator::seek", spec.args, line)
			return
		}
		vm.push(types.NewNull())
		vm.bump()
	case "asort":
		flags := int64(spl.SORT_REGULAR)
		if len(spec.args) >= 1 {
			flags = vm.toLong(spec.args[0])
		}
		if err := ai.Asort(vm.Zone, flags); err != nil {
			vm.raiseNativeError(err, "ArrayIterator::asort", spec.args, line)
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "ksort":
		flags := int64(spl.SORT_REGULAR)
		if len(spec.args) >= 1 {
			flags = vm.toLong(spec.args[0])
		}
		if err := ai.Ksort(vm.Zone, flags); err != nil {
			vm.raiseNativeError(err, "ArrayIterator::ksort", spec.args, line)
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "natsort":
		if err := ai.Natsort(vm.Zone); err != nil {
			vm.raiseNativeError(err, "ArrayIterator::natsort", spec.args, line)
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "natcasesort":
		if err := ai.Natcasesort(vm.Zone); err != nil {
			vm.raiseNativeError(err, "ArrayIterator::natcasesort", spec.args, line)
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "uasort":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::uasort() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		cmp, ok := vm.makeSPLComparator(spec.args[0], "ArrayIterator::uasort", line)
		if !ok {
			return
		}
		if err := ai.Uasort(vm.Zone, cmp); err != nil {
			if !vm.stop && !vm.control {
				vm.raiseNativeError(err, "ArrayIterator::uasort", spec.args, line)
			}
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	case "uksort":
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ArrayIterator::uksort() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return
		}
		cmp, ok := vm.makeSPLComparator(spec.args[0], "ArrayIterator::uksort", line)
		if !ok {
			return
		}
		if err := ai.Uksort(vm.Zone, cmp); err != nil {
			if !vm.stop && !vm.control {
				vm.raiseNativeError(err, "ArrayIterator::uksort", spec.args, line)
			}
			return
		}
		vm.push(types.NewBool(true))
		vm.bump()
	default:
		vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method ArrayIterator::"+spec.method+"()", 0)
	}
}

func (vm *VM) objectOffsetGet(obj, key types.Value) (types.Value, bool) {
	d := types.Deref(vm.Zone, obj)
	if d.Type != types.TypeObject {
		return types.Value{}, false
	}
	classID := types.ObjectInfo(vm.Zone, d).ClassID
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		dll, err := spl.DLLFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		v, err := dll.OffsetGet(int(vm.toLong(key)))
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		return types.RetainValue(vm.Zone, v), true
	case bytecode.ClassSplFixedArray:
		fa, err := spl.FixedArrayFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		v, err := fa.OffsetGet(int(vm.toLong(key)))
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		return types.RetainValue(vm.Zone, v), true
	case bytecode.ClassSplObjectStorage:
		os, err := spl.ObjectStorageFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		v, err := os.OffsetGet(vm.Zone, key)
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		return types.RetainValue(vm.Zone, v), true
	case bytecode.ClassArrayObject:
		ao, err := spl.ArrayObjectFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		v, err := ao.OffsetGet(vm.Zone, key)
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		return types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)), true
	case bytecode.ClassArrayIterator:
		ai, err := spl.ArrayIteratorFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		v, err := ai.OffsetGet(vm.Zone, key)
		if err != nil {
			vm.raiseNativeError(err, "offsetGet", nil, vm.line)
			return types.NewNull(), true
		}
		return types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)), true
	default:
		if vm.isInstance(classID, bytecode.ClassArrayAccess) {
			if m, _, ok := vm.findMethod(classID, "offsetGet"); ok {
				ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, []types.Value{types.RetainValue(vm.Zone, key)}, classID, m.ClassID, vm.line)
				if threw || vm.stop {
					return types.NewNull(), true
				}
				vm.control = false
				return ret, true
			}
		}
		return types.Value{}, false
	}
}

func (vm *VM) objectOffsetSet(obj, key, val types.Value) bool {
	d := types.Deref(vm.Zone, obj)
	if d.Type != types.TypeObject {
		return false
	}
	classID := types.ObjectInfo(vm.Zone, d).ClassID
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		dll, err := spl.DLLFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
			return true
		}
		if err := dll.OffsetSetValue(vm.Zone, key, val); err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
		}
		return true
	case bytecode.ClassSplFixedArray:
		fa, err := spl.FixedArrayFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
			return true
		}
		if types.Deref(vm.Zone, key).IsNull() || key.IsUndef() {
			vm.raiseBuiltin(bytecode.ClassRuntimeException, "Index invalid or out of range", 0)
			return true
		}
		if err := fa.OffsetSet(vm.Zone, int(vm.toLong(key)), val); err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
		}
		return true
	case bytecode.ClassSplObjectStorage:
		os, err := spl.ObjectStorageFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
			return true
		}
		if err := os.OffsetSet(vm.Zone, key, val); err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
		}
		return true
	case bytecode.ClassArrayObject:
		ao, err := spl.ArrayObjectFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
			return true
		}
		if err := ao.OffsetSet(vm.Zone, key, val); err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
		}
		return true
	case bytecode.ClassArrayIterator:
		ai, err := spl.ArrayIteratorFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
			return true
		}
		if err := ai.OffsetSet(vm.Zone, key, val); err != nil {
			vm.raiseNativeError(err, "offsetSet", nil, vm.line)
		}
		return true
	default:
		if vm.isInstance(classID, bytecode.ClassArrayAccess) {
			if m, _, ok := vm.findMethod(classID, "offsetSet"); ok {
				ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, []types.Value{types.RetainValue(vm.Zone, key), types.RetainValue(vm.Zone, val)}, classID, m.ClassID, vm.line)
				if !threw && !vm.stop {
					types.ReleaseValue(vm.Zone, &ret)
					vm.control = false
				}
				return true
			}
		}
		return false
	}
}

func (vm *VM) objectOffsetExists(obj, key types.Value) (bool, bool) {
	d := types.Deref(vm.Zone, obj)
	if d.Type != types.TypeObject {
		return false, false
	}
	classID := types.ObjectInfo(vm.Zone, d).ClassID
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		dll, err := spl.DLLFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetExists", nil, vm.line)
			return false, true
		}
		return dll.OffsetExists(int(vm.toLong(key))), true
	case bytecode.ClassSplFixedArray:
		fa, err := spl.FixedArrayFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetExists", nil, vm.line)
			return false, true
		}
		return fa.OffsetExists(int(vm.toLong(key))), true
	case bytecode.ClassSplObjectStorage:
		os, err := spl.ObjectStorageFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetExists", nil, vm.line)
			return false, true
		}
		ok, err := os.OffsetExists(vm.Zone, key)
		if err != nil {
			vm.raiseNativeError(err, "offsetExists", nil, vm.line)
			return false, true
		}
		return ok, true
	case bytecode.ClassArrayObject:
		ao, err := spl.ArrayObjectFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetExists", nil, vm.line)
			return false, true
		}
		ok, err := ao.OffsetExists(vm.Zone, key)
		if err != nil {
			vm.raiseNativeError(err, "offsetExists", nil, vm.line)
			return false, true
		}
		return ok, true
	case bytecode.ClassArrayIterator:
		ai, err := spl.ArrayIteratorFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetExists", nil, vm.line)
			return false, true
		}
		ok, err := ai.OffsetExists(vm.Zone, key)
		if err != nil {
			vm.raiseNativeError(err, "offsetExists", nil, vm.line)
			return false, true
		}
		return ok, true
	default:
		if vm.isInstance(classID, bytecode.ClassArrayAccess) {
			if m, _, ok := vm.findMethod(classID, "offsetExists"); ok {
				ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, []types.Value{types.RetainValue(vm.Zone, key)}, classID, m.ClassID, vm.line)
				if threw || vm.stop {
					return false, true
				}
				vm.control = false
				b := types.ToBoolZ(vm.Zone, ret)
				types.ReleaseValue(vm.Zone, &ret)
				return b, true
			}
		}
		return false, false
	}
}

func (vm *VM) objectOffsetUnset(obj, key types.Value) bool {
	d := types.Deref(vm.Zone, obj)
	if d.Type != types.TypeObject {
		return false
	}
	classID := types.ObjectInfo(vm.Zone, d).ClassID
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		dll, err := spl.DLLFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
			return true
		}
		if err := dll.OffsetUnset(vm.Zone, int(vm.toLong(key))); err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
		}
		return true
	case bytecode.ClassSplFixedArray:
		fa, err := spl.FixedArrayFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
			return true
		}
		if err := fa.OffsetUnset(vm.Zone, int(vm.toLong(key))); err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
		}
		return true
	case bytecode.ClassSplObjectStorage:
		os, err := spl.ObjectStorageFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
			return true
		}
		if err := os.OffsetUnset(vm.Zone, key); err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
		}
		return true
	case bytecode.ClassArrayObject:
		ao, err := spl.ArrayObjectFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
			return true
		}
		if err := ao.OffsetUnset(vm.Zone, key); err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
		}
		return true
	case bytecode.ClassArrayIterator:
		ai, err := spl.ArrayIteratorFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
			return true
		}
		if err := ai.OffsetUnset(vm.Zone, key); err != nil {
			vm.raiseNativeError(err, "offsetUnset", nil, vm.line)
		}
		return true
	default:
		if vm.isInstance(classID, bytecode.ClassArrayAccess) {
			if m, _, ok := vm.findMethod(classID, "offsetUnset"); ok {
				ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, []types.Value{types.RetainValue(vm.Zone, key)}, classID, m.ClassID, vm.line)
				if !threw && !vm.stop {
					types.ReleaseValue(vm.Zone, &ret)
					vm.control = false
				}
				return true
			}
		}
		return false
	}
}

func (vm *VM) prepareObjectIter(slot *iterState, obj types.Value) bool {
	d := types.Deref(vm.Zone, obj)
	if d.Type != types.TypeObject {
		return false
	}
	classID := types.ObjectInfo(vm.Zone, d).ClassID
	if classID == bytecode.ClassPDOStatement {
		slot.index = 0
		k := int64(0)
		for {
			row, err := pdo.CallStmtMethod(vm.Zone, d, "fetch")
			if err != nil {
				vm.raiseNativeError(err, "PDOStatement::fetch", nil, vm.line)
				return true
			}
			if row.Type == types.TypeFalse {
				break
			}
			slot.keys = append(slot.keys, types.NewLong(k))
			slot.vals = append(slot.vals, row)
			k++
		}
		slot.active = len(slot.keys) > 0
		return true
	}
	if classID == bytecode.ClassArrayObject {
		ao, err := spl.ArrayObjectFromObject(vm.Zone, d)
		if err != nil {
			vm.raiseNativeError(err, "ArrayObject::getIterator", nil, vm.line)
			return true
		}
		itObj, _, err := ao.GetIterator(vm.Zone)
		if err != nil {
			vm.raiseNativeError(err, "ArrayObject::getIterator", nil, vm.line)
			return true
		}
		slot.iterObj = itObj
		slot.index = 0
		vm.iterRewind(slot.iterObj)
		if vm.stop || vm.control {
			return true
		}
		slot.active = vm.iterValid(slot.iterObj)
		return true
	}
	if vm.isInstance(classID, bytecode.ClassIteratorAggregate) {
		if m, _, ok := vm.findMethod(classID, "getIterator"); ok {
			itObj, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, d), true, nil, classID, m.ClassID, vm.line)
			if threw || vm.stop {
				return true
			}
			vm.control = false
			slot.iterObj = itObj
			slot.index = 0
			vm.iterRewind(slot.iterObj)
			if vm.stop || vm.control {
				return true
			}
			slot.active = vm.iterValid(slot.iterObj)
			return true
		}
	}
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue,
		bytecode.ClassSplFixedArray, bytecode.ClassSplObjectStorage, bytecode.ClassArrayIterator,
		bytecode.ClassDatePeriod:
		slot.iterObj = types.RetainValue(vm.Zone, d)
		slot.index = 0
		vm.iterRewind(slot.iterObj)
		if vm.stop || vm.control {
			return true
		}
		slot.active = vm.iterValid(slot.iterObj)
		return true
	default:
		if vm.isInstance(classID, bytecode.ClassIterator) {
			slot.iterObj = types.RetainValue(vm.Zone, d)
			slot.index = 0
			vm.iterRewind(slot.iterObj)
			if vm.stop || vm.control {
				return true
			}
			slot.active = vm.iterValid(slot.iterObj)
			return true
		}
	}
	return false
}

// startedGenerator rend le générateur porté par obj, démarré jusqu'à son premier yield ; nil si obj n'est pas
// un générateur ou si son démarrage a levé une exception.
func (vm *VM) startedGenerator(obj types.Value) (*generator, bool) {
	g := vm.generatorOf(obj)
	if g == nil {
		return nil, false
	}
	if !g.started && !vm.resumeGenerator(g, types.NewNull(), false) {
		return nil, true
	}
	return g, true
}

func (vm *VM) iterRewind(obj types.Value) {
	if g, isGen := vm.startedGenerator(obj); isGen {
		if g != nil {
			vm.rewindGenerator(g)
		}
		return
	}
	classID := types.ObjectInfo(vm.Zone, obj).ClassID
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		if dll, err := spl.DLLFromObject(vm.Zone, obj); err == nil {
			dll.Rewind()
		}
	case bytecode.ClassSplFixedArray:
		if fa, err := spl.FixedArrayFromObject(vm.Zone, obj); err == nil {
			fa.Rewind()
		}
	case bytecode.ClassSplObjectStorage:
		if os, err := spl.ObjectStorageFromObject(vm.Zone, obj); err == nil {
			os.Rewind()
		}
	case bytecode.ClassArrayIterator:
		if ai, err := spl.ArrayIteratorFromObject(vm.Zone, obj); err == nil {
			ai.Rewind(vm.Zone)
		}
	case bytecode.ClassDatePeriod:
		if p, err := datetime.PeriodFromObject(vm.Zone, obj); err == nil {
			p.Rewind()
		}
	default:
		if m, _, ok := vm.findMethod(classID, "rewind"); ok {
			ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, obj), true, nil, classID, m.ClassID, vm.line)
			if !threw && !vm.stop {
				types.ReleaseValue(vm.Zone, &ret)
				vm.control = false
			}
		}
	}
}

func (vm *VM) iterValid(obj types.Value) bool {
	if g, isGen := vm.startedGenerator(obj); isGen {
		return g != nil && !g.done
	}
	classID := types.ObjectInfo(vm.Zone, obj).ClassID
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		if dll, err := spl.DLLFromObject(vm.Zone, obj); err == nil {
			return dll.Valid()
		}
	case bytecode.ClassSplFixedArray:
		if fa, err := spl.FixedArrayFromObject(vm.Zone, obj); err == nil {
			return fa.Valid()
		}
	case bytecode.ClassSplObjectStorage:
		if os, err := spl.ObjectStorageFromObject(vm.Zone, obj); err == nil {
			return os.Valid()
		}
	case bytecode.ClassArrayIterator:
		if ai, err := spl.ArrayIteratorFromObject(vm.Zone, obj); err == nil {
			return ai.Valid(vm.Zone)
		}
	case bytecode.ClassDatePeriod:
		if p, err := datetime.PeriodFromObject(vm.Zone, obj); err == nil {
			return p.Valid()
		}
	default:
		if m, _, ok := vm.findMethod(classID, "valid"); ok {
			ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, obj), true, nil, classID, m.ClassID, vm.line)
			if threw || vm.stop {
				return false
			}
			vm.control = false
			b := types.ToBoolZ(vm.Zone, ret)
			types.ReleaseValue(vm.Zone, &ret)
			return b
		}
	}
	return false
}

func (vm *VM) iterCurrent(obj types.Value) types.Value {
	if g, isGen := vm.startedGenerator(obj); isGen {
		if g == nil || g.done {
			return types.NewNull()
		}
		return types.RetainValue(vm.Zone, g.value)
	}
	classID := types.ObjectInfo(vm.Zone, obj).ClassID
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		if dll, err := spl.DLLFromObject(vm.Zone, obj); err == nil {
			return types.RetainValue(vm.Zone, dll.Current())
		}
	case bytecode.ClassSplFixedArray:
		if fa, err := spl.FixedArrayFromObject(vm.Zone, obj); err == nil {
			return types.RetainValue(vm.Zone, fa.Current())
		}
	case bytecode.ClassSplObjectStorage:
		if os, err := spl.ObjectStorageFromObject(vm.Zone, obj); err == nil {
			return types.RetainValue(vm.Zone, os.Current())
		}
	case bytecode.ClassArrayIterator:
		if ai, err := spl.ArrayIteratorFromObject(vm.Zone, obj); err == nil {
			return types.RetainValue(vm.Zone, types.Deref(vm.Zone, ai.Current(vm.Zone)))
		}
	case bytecode.ClassDatePeriod:
		if p, err := datetime.PeriodFromObject(vm.Zone, obj); err == nil {
			return types.RetainValue(vm.Zone, p.Current())
		}
	default:
		if m, _, ok := vm.findMethod(classID, "current"); ok {
			ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, obj), true, nil, classID, m.ClassID, vm.line)
			if threw || vm.stop {
				return types.NewNull()
			}
			vm.control = false
			return ret
		}
	}
	return types.NewNull()
}

func (vm *VM) iterKey(obj types.Value) types.Value {
	if g, isGen := vm.startedGenerator(obj); isGen {
		if g == nil || g.done {
			return types.NewNull()
		}
		return types.RetainValue(vm.Zone, g.key)
	}
	classID := types.ObjectInfo(vm.Zone, obj).ClassID
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		if dll, err := spl.DLLFromObject(vm.Zone, obj); err == nil {
			return types.NewLong(int64(dll.Key()))
		}
	case bytecode.ClassSplFixedArray:
		if fa, err := spl.FixedArrayFromObject(vm.Zone, obj); err == nil {
			return types.NewLong(int64(fa.Key()))
		}
	case bytecode.ClassSplObjectStorage:
		if os, err := spl.ObjectStorageFromObject(vm.Zone, obj); err == nil {
			return types.NewLong(int64(os.Key()))
		}
	case bytecode.ClassArrayIterator:
		if ai, err := spl.ArrayIteratorFromObject(vm.Zone, obj); err == nil {
			return types.RetainValue(vm.Zone, ai.Key(vm.Zone))
		}
	case bytecode.ClassDatePeriod:
		if p, err := datetime.PeriodFromObject(vm.Zone, obj); err == nil {
			return types.NewLong(int64(p.Key()))
		}
	default:
		if m, _, ok := vm.findMethod(classID, "key"); ok {
			ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, obj), true, nil, classID, m.ClassID, vm.line)
			if threw || vm.stop {
				return types.NewNull()
			}
			vm.control = false
			return ret
		}
	}
	return types.NewNull()
}

func (vm *VM) iterNext(obj types.Value) {
	if g, isGen := vm.startedGenerator(obj); isGen {
		if g != nil && !g.done {
			vm.resumeGenerator(g, types.NewNull(), false)
		}
		return
	}
	classID := types.ObjectInfo(vm.Zone, obj).ClassID
	switch classID {
	case bytecode.ClassSplDoublyLinkedList, bytecode.ClassSplStack, bytecode.ClassSplQueue:
		if dll, err := spl.DLLFromObject(vm.Zone, obj); err == nil {
			dll.Next(vm.Zone)
		}
	case bytecode.ClassSplFixedArray:
		if fa, err := spl.FixedArrayFromObject(vm.Zone, obj); err == nil {
			fa.Next()
		}
	case bytecode.ClassSplObjectStorage:
		if os, err := spl.ObjectStorageFromObject(vm.Zone, obj); err == nil {
			os.Next()
		}
	case bytecode.ClassArrayIterator:
		if ai, err := spl.ArrayIteratorFromObject(vm.Zone, obj); err == nil {
			ai.Next(vm.Zone)
		}
	case bytecode.ClassDatePeriod:
		if p, err := datetime.PeriodFromObject(vm.Zone, obj); err == nil {
			p.Next()
		}
	default:
		if m, _, ok := vm.findMethod(classID, "next"); ok {
			ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, obj), true, nil, classID, m.ClassID, vm.line)
			if !threw && !vm.stop {
				types.ReleaseValue(vm.Zone, &ret)
				vm.control = false
			}
		}
	}
}

func (vm *VM) dynamicPropGet(obj types.Value, name string) (types.Value, bool) {
	classID := types.ObjectInfo(vm.Zone, obj).ClassID
	if mysqliClass(classID) && classID != bytecode.ClassMysqliSqlException {
		if v, ok := vm.mysqliPropGet(obj, name); ok {
			return v, true
		}
	}
	if classID == bytecode.ClassDateInterval {
		if iv, err := datetime.IntervalFromObject(vm.Zone, obj); err == nil {
			if v, perr := iv.GetProperty(vm.Zone, name); perr == nil {
				return v, true
			}
		}
	}
	if classID == bytecode.ClassArrayObject {
		if ao, err := spl.ArrayObjectFromObject(vm.Zone, obj); err == nil && ao.GetFlags()&spl.ARRAY_AS_PROPS != 0 {
			k, err := types.NewStringLiteral(vm.Zone, name)
			if err != nil {
				return types.NewNull(), true
			}
			defer types.ReleaseValue(vm.Zone, &k)
			v, err := ao.OffsetGet(vm.Zone, k)
			if err != nil {
				return types.NewNull(), true
			}
			return types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)), true
		}
	}
	if v, ok := pdo.ObjectGetProperty(vm.Zone, obj, name); ok {
		return types.RetainValue(vm.Zone, types.Deref(vm.Zone, v)), true
	}
	return types.Value{}, false
}

func (vm *VM) dynamicPropSet(obj types.Value, name string, val types.Value) bool {
	classID := types.ObjectInfo(vm.Zone, obj).ClassID
	if mysqliClass(classID) && classID != bytecode.ClassMysqliSqlException {
		if err := mysqli.SetProperty(vm.Zone, obj, name, types.Deref(vm.Zone, val)); err != nil {
			vm.raiseNativeError(err, name, nil, vm.line)
		}
		return true
	}
	if classID == bytecode.ClassDateInterval {
		if iv, err := datetime.IntervalFromObject(vm.Zone, obj); err == nil {
			if perr := iv.SetProperty(vm.Zone, name, val); perr == nil {
				return true
			}
		}
	}
	if classID == bytecode.ClassArrayObject {
		if ao, err := spl.ArrayObjectFromObject(vm.Zone, obj); err == nil && ao.GetFlags()&spl.ARRAY_AS_PROPS != 0 {
			k, err := types.NewStringLiteral(vm.Zone, name)
			if err == nil {
				_ = ao.OffsetSet(vm.Zone, k, val)
				types.ReleaseValue(vm.Zone, &k)
			}
			return true
		}
	}
	off, ok := vm.Zone.ObjectAddress(obj.DataSlot, obj.Extra)
	if !ok {
		return false
	}
	raw, err := vm.Zone.Bytes(off, uint32(unsafe.Sizeof(types.ObjectHeader{})))
	if err != nil {
		return false
	}
	h := (*types.ObjectHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	var dynArr types.Value
	if h.DynOffset == types.EmptySlot {
		dynArr, err = types.NewArray(vm.Zone)
		if err != nil {
			return false
		}
	} else {
		dynArr = types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: h.DynOffset}
	}
	k, err := types.NewStringLiteral(vm.Zone, name)
	if err == nil {
		_ = types.ArraySet(vm.Zone, &dynArr, k, types.Deref(vm.Zone, val), nil)
		types.ReleaseValue(vm.Zone, &k)
	}
	raw, err = vm.Zone.Bytes(off, uint32(unsafe.Sizeof(types.ObjectHeader{})))
	if err == nil {
		h = (*types.ObjectHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
		h.DynOffset = dynArr.DataSlot
	}
	return true
}

func (vm *VM) dynamicPropRef(obj types.Value, name string) (types.Value, bool) {
	off, ok := vm.Zone.ObjectAddress(obj.DataSlot, obj.Extra)
	if !ok {
		return types.Value{}, false
	}
	raw, err := vm.Zone.Bytes(off, uint32(unsafe.Sizeof(types.ObjectHeader{})))
	if err != nil {
		return types.Value{}, false
	}
	h := (*types.ObjectHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	var dynArr types.Value
	if h.DynOffset == types.EmptySlot {
		dynArr, err = types.NewArray(vm.Zone)
		if err != nil {
			return types.Value{}, false
		}
	} else {
		dynArr = types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: h.DynOffset}
	}
	k, err := types.NewStringLiteral(vm.Zone, name)
	if err != nil {
		return types.Value{}, false
	}
	ref, err := types.ArrayReference(vm.Zone, &dynArr, k, nil)
	types.ReleaseValue(vm.Zone, &k)
	if err != nil {
		return types.Value{}, false
	}
	raw, err = vm.Zone.Bytes(off, uint32(unsafe.Sizeof(types.ObjectHeader{})))
	if err == nil {
		h = (*types.ObjectHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
		h.DynOffset = dynArr.DataSlot
	}
	return ref, true
}

func (vm *VM) dynamicPropQuery(op bytecode.Opcode, obj types.Value, name string) (bool, bool) {
	classID := types.ObjectInfo(vm.Zone, obj).ClassID
	if mysqliClass(classID) && classID != bytecode.ClassMysqliSqlException && op != bytecode.OpUnsetProp {
		if v, ok := vm.mysqliPropGet(obj, name); ok {
			defer vmRelease(vm.Zone, &v)
			if op == bytecode.OpEmptyProp {
				return !types.ToBoolZ(vm.Zone, v), true
			}
			return isSetValue(vm.Zone, v), true
		}
	}
	if classID == bytecode.ClassDateInterval {
		if iv, err := datetime.IntervalFromObject(vm.Zone, obj); err == nil {
			if v, perr := iv.GetProperty(vm.Zone, name); perr == nil {
				if op == bytecode.OpUnsetProp {
					return false, true
				}
				if op == bytecode.OpEmptyProp {
					return !types.ToBoolZ(vm.Zone, v), true
				}
				return !v.IsNull(), true
			}
		}
	}
	if classID == bytecode.ClassArrayObject {
		if ao, err := spl.ArrayObjectFromObject(vm.Zone, obj); err == nil && ao.GetFlags()&spl.ARRAY_AS_PROPS != 0 {
			k, err := types.NewStringLiteral(vm.Zone, name)
			if err != nil {
				return false, true
			}
			defer types.ReleaseValue(vm.Zone, &k)
			if op == bytecode.OpUnsetProp {
				_ = ao.OffsetUnset(vm.Zone, k)
				return false, true
			}
			exists, _ := ao.OffsetExists(vm.Zone, k)
			if !exists {
				return op == bytecode.OpEmptyProp, true
			}
			v, _ := ao.OffsetGet(vm.Zone, k)
			if op == bytecode.OpEmptyProp {
				return !types.ToBoolZ(vm.Zone, v), true
			}
			return isSetValue(vm.Zone, v), true
		}
	}
	arr, hasDyn := pdo.ObjectDynamicArray(vm.Zone, obj)
	if !hasDyn {
		return false, false
	}
	v, found := pdo.ObjectGetProperty(vm.Zone, obj, name)
	if !found {
		return false, false
	}
	if op == bytecode.OpUnsetProp {
		k, err := types.NewStringLiteral(vm.Zone, name)
		if err == nil {
			_ = types.ArrayUnset(vm.Zone, &arr, k, nil)
			types.ReleaseValue(vm.Zone, &k)
		}
		return false, true
	}
	if op == bytecode.OpEmptyProp {
		return !types.ToBoolZ(vm.Zone, v), true
	}
	return isSetValue(vm.Zone, v), true
}
