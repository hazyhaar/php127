package bytecode

import (
	"io"
	"slices"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type Instr struct {
	Op     Opcode
	Flags  uint16
	Op1    uint32
	Op2    uint32
	Result uint32
	Line   uint32
}

type Visibility uint8

const (
	VisPublic Visibility = iota
	VisProtected
	VisPrivate
)

const (
	ClassAbstract uint16 = 1 << iota
	ClassFinal
	ClassReadonly
)

type AttributeArg struct {
	Name  string
	Value *Constant
}

type AttributeProto struct {
	Name string
	Args []AttributeArg
}

type ParamInfo struct {
	DefaultConst     *Constant
	Name             string
	ByRef            bool
	TypeName         string
	Promoted         bool
	HasDefault       bool
	Default          types.Value
	DefaultString    string
	HasDefaultString bool
	Variadic         bool
	Attributes       []AttributeProto
}

type UseVar struct {
	CV    uint32
	ByRef bool
}

type CatchTarget struct {
	Classes []uint32
	CV      int32
	PC      uint32
}

type TryCatchEntry struct {
	StartPC      uint32
	EndPC        uint32
	CatchTargets []CatchTarget
	FinallyPC    uint32
	FinallyEndPC uint32
}

type CaseKind uint8

const (
	CaseLong CaseKind = iota
	CaseString
)

type JumpCase struct {
	Kind   CaseKind
	Index  uint32
	Target uint32
}

type JumpTable struct {
	Cases   []JumpCase
	Default uint32
}

type PropIC struct {
	ClassID uint32
	SlotIdx uint32
	Fast    bool
}

type CallIC struct {
	ProgFuncsLen int
	FuncIdx      int32
	Kind         uint8
	NativeNoRef  bool
	NativeSync   bool
	Native       func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error)
}

type OpArray struct {
	ReturnType        string
	IsGenerator       bool
	Name              string
	Filename          string
	StrictTypes       bool
	ReturnByRef       bool
	UsesVarArgs       bool
	HasGlobalsCV      bool
	HasDynamicCV      bool
	HasTypedParams    bool
	CVMetaResolved    bool
	FastCall          bool
	NeedsSuperglobals bool
	HasStaticVars     bool
	GlobalsCV         uint32
	NumCVs            int
	CVNames           []string
	SuperglobalCVs    []uint32
	MaxStack          int
	Params            []ParamInfo
	Consts            []types.Value
	Strings           []string
	Code              []Instr
	TryTable          []TryCatchEntry
	Tables            []JumpTable
	Aux               []uint32
	Uses              []UseVar
	Attributes        []AttributeProto
	FuncID            uint32
}

func IsSuperglobalName(name string) bool {
	switch name {
	case "_SERVER", "_GET", "_POST", "_COOKIE", "_REQUEST", "_SESSION", "_ENV", "_FILES":
		return true
	default:
		return false
	}
}

func (fn *OpArray) ResolveCVMeta() {
	if fn == nil {
		return
	}
	fn.GlobalsCV = NoCV
	fn.HasGlobalsCV = false
	fn.HasDynamicCV = false
	fn.HasTypedParams = false
	hasRefOrVarParam := false
	for _, p := range fn.Params {
		if p.TypeName != "" {
			fn.HasTypedParams = true
		}
		if p.ByRef || p.Variadic || p.HasDefault || p.HasDefaultString || p.DefaultConst != nil {
			hasRefOrVarParam = true
		}
	}
	fn.SuperglobalCVs = fn.SuperglobalCVs[:0]
	for i, name := range fn.CVNames {
		if name == "GLOBALS" {
			fn.GlobalsCV = uint32(i)
			fn.HasGlobalsCV = true
		} else if IsSuperglobalName(name) {
			fn.SuperglobalCVs = append(fn.SuperglobalCVs, uint32(i))
		} else if strings.HasPrefix(name, "$") {
			fn.HasDynamicCV = true
		}
	}
	fn.UsesVarArgs = false
	fn.HasStaticVars = false
	hasGlobalOp := false
	for _, in := range fn.Code {
		if in.Op == OpStaticCheck || in.Op == OpBindStatic {
			fn.HasStaticVars = true
		}
		if in.Op == OpEval || in.Op == OpInclude {
			fn.UsesVarArgs = true
			hasGlobalOp = true
		}
		if in.Op == OpGlobalGet || in.Op == OpGlobalSet || in.Op == OpGlobalUnset || in.Op == OpGlobalIsset || in.Op == OpBindGlobal || (in.Op == OpLoadCV && in.Flags&FlagGlobal != 0) {
			hasGlobalOp = true
		}
		if in.Op == OpInitCall {
			if in.Flags&FlagClosure != 0 {
				fn.UsesVarArgs = true
			}
			if in.Flags&FlagNative != 0 && int(in.Op1) < len(fn.Strings) {
				switch strings.ToLower(strings.TrimPrefix(fn.Strings[in.Op1], `\`)) {
				case "func_num_args", "func_get_arg", "func_get_args", "call_user_func", "call_user_func_array":
					fn.UsesVarArgs = true
				case "extract", "compact":
					fn.HasDynamicCV = true
				case "session_start", "session_reset", "session_unset", "session_decode", "session_destroy", "session_write_close", "session_commit", "session_encode":
					hasGlobalOp = true
				}
			}
		}
	}
	fn.NeedsSuperglobals = fn.HasGlobalsCV || len(fn.SuperglobalCVs) > 0 || hasGlobalOp
	fn.FastCall = !fn.HasTypedParams && !fn.UsesVarArgs && !fn.IsGenerator && len(fn.SuperglobalCVs) == 0 && !hasRefOrVarParam && !fn.HasDynamicCV
	fn.CVMetaResolved = true
}

type PropInfo struct {
	Initial    *Constant
	ClassID    uint32
	Name       string
	Visibility Visibility
	Readonly   bool
	Static     bool
	Promoted   bool
	TypeName   string
	Index      uint32
	HasDefault bool
	Default    types.Value
	Attributes []AttributeProto
}

type MethodProto struct {
	Name       string
	Visibility Visibility
	Static     bool
	Abstract   bool
	Final      bool
	Func       *OpArray
	ClassID    uint32
	Attributes []AttributeProto
}

type ClassConst struct {
	Initial    *Constant
	Name       string
	Visibility Visibility
	Final      bool
	Value      types.Value
	Text       string
	IsString   bool
	Attributes []AttributeProto
}

// Constant ne contient aucun handle de zone de requete.
type Constant struct {
	Value    types.Value
	Text     string
	IsString bool
	IsArray  bool
	Items    []ConstantItem
}

type ConstantItem struct {
	Key   *Constant
	Value Constant
}

type ClassKind uint8

const (
	KindClass ClassKind = iota
	KindInterface
	KindTrait
	KindEnum
)

type ClassProto struct {
	BackingType       string
	Cases             []EnumCase
	Name              string
	ID                uint32
	Kind              ClassKind
	Flags             uint16
	ParentID          uint32
	ParentName        string
	Interfaces        []string
	Traits            []string
	Props             []PropInfo
	ResolvedProps     []PropInfo
	InstancePropCount uint32
	PropsResolved     bool
	HasDestructor     bool
	Methods           []MethodProto
	Constants         []ClassConst
	Attributes        []AttributeProto
}

type EnumCase struct {
	Name       string
	Value      *Constant
	Attributes []AttributeProto
}

type ConstDecl struct {
	Name     string
	Value    types.Value
	Text     string
	IsString bool
}

type Program struct {
	Main              *OpArray
	Funcs             []*OpArray
	Classes           []*ClassProto
	Traits            []*ClassProto
	Constants         []ConstDecl
	NeedsSuperglobals bool
}

func BuiltinClassID(name string) (uint32, bool) {
	switch strings.ToLower(strings.TrimPrefix(name, `\`)) {
	case "throwable":
		return ClassThrowable, true
	case "exception":
		return ClassException, true
	case "error":
		return ClassError, true
	case "typeerror":
		return ClassTypeError, true
	case "valueerror":
		return ClassValueError, true
	case "arithmeticerror":
		return ClassArithmeticError, true
	case "divisionbyzeroerror":
		return ClassDivisionByZeroError, true
	case "unhandledmatcherror":
		return ClassUnhandledMatchError, true
	case "argumentcounterror":
		return ClassArgumentCountError, true
	case "closure":
		return ClassClosure, true
	case "unitenum":
		return ClassUnitEnum, true
	case "backedenum":
		return ClassBackedEnum, true
	case "generator":
		return ClassGenerator, true
	case "traversable":
		return ClassTraversable, true
	case "iterator":
		return ClassIterator, true
	case "iteratoraggregate":
		return ClassIteratorAggregate, true
	case "seekableiterator":
		return ClassSeekableIterator, true
	case "countable":
		return ClassCountable, true
	case "arrayaccess":
		return ClassArrayAccess, true
	case "runtimeexception":
		return ClassRuntimeException, true
	case "logicexception":
		return ClassLogicException, true
	case "badfunctioncallexception":
		return ClassBadFunctionCallException, true
	case "badmethodcallexception":
		return ClassBadMethodCallException, true
	case "domainexception":
		return ClassDomainException, true
	case "invalidargumentexception":
		return ClassInvalidArgumentException, true
	case "lengthexception":
		return ClassLengthException, true
	case "outofboundsexception":
		return ClassOutOfBoundsException, true
	case "outofrangeexception":
		return ClassOutOfRangeException, true
	case "overflowexception":
		return ClassOverflowException, true
	case "rangeexception":
		return ClassRangeException, true
	case "underflowexception":
		return ClassUnderflowException, true
	case "unexpectedvalueexception":
		return ClassUnexpectedValueException, true
	case "fiber":
		return ClassFiber, true
	case "fibererror":
		return ClassFiberError, true
	case "spldoublylinkedlist":
		return ClassSplDoublyLinkedList, true
	case "splstack":
		return ClassSplStack, true
	case "splqueue":
		return ClassSplQueue, true
	case "splfixedarray":
		return ClassSplFixedArray, true
	case "splobjectstorage":
		return ClassSplObjectStorage, true
	case "arrayobject":
		return ClassArrayObject, true
	case "arrayiterator":
		return ClassArrayIterator, true
	case "stdclass":
		return ClassStdClass, true
	case "pdo":
		return ClassPDO, true
	case "pdostatement":
		return ClassPDOStatement, true
	case "pdoexception":
		return ClassPDOException, true
	case "datetime":
		return ClassDateTime, true
	case "datetimeimmutable":
		return ClassDateTimeImmutable, true
	case "datetimezone":
		return ClassDateTimeZone, true
	case "dateinterval":
		return ClassDateInterval, true
	case "dateperiod":
		return ClassDatePeriod, true
	case "datetimeinterface":
		return ClassDateTimeInterface, true
	case "dateerror":
		return ClassDateError, true
	case "dateobjecterror":
		return ClassDateObjectError, true
	case "daterangeerror":
		return ClassDateRangeError, true
	case "dateexception":
		return ClassDateException, true
	case "dateinvalidtimezoneexception":
		return ClassDateInvalidTimeZoneException, true
	case "dateinvalidoperationexception":
		return ClassDateInvalidOperationException, true
	case "datemalformedstringexception":
		return ClassDateMalformedStringException, true
	case "datemalformedintervalstringexception":
		return ClassDateMalformedIntervalStringException, true
	case "reflectionexception":
		return ClassReflectionException, true
	case "reflectionclass":
		return ClassReflectionClass, true
	case "reflectionobject":
		return ClassReflectionObject, true
	case "reflectionmethod":
		return ClassReflectionMethod, true
	case "reflectionfunction":
		return ClassReflectionFunction, true
	case "reflectionproperty":
		return ClassReflectionProperty, true
	case "reflectionparameter":
		return ClassReflectionParameter, true
	case "reflectionclassconstant":
		return ClassReflectionClassConstant, true
	case "reflectionenum":
		return ClassReflectionEnum, true
	case "reflectionenumunitcase":
		return ClassReflectionEnumUnitCase, true
	case "reflectionenumbackedcase":
		return ClassReflectionEnumBackedCase, true
	case "reflectionnamedtype":
		return ClassReflectionNamedType, true
	case "reflectionuniontype":
		return ClassReflectionUnionType, true
	case "reflectionintersectiontype":
		return ClassReflectionIntersectionType, true
	case "reflectionattribute":
		return ClassReflectionAttribute, true
	case "reflector":
		return ClassReflector, true
	case "reflectiontype":
		return ClassReflectionType, true
	case "reflectionfunctionabstract":
		return ClassReflectionFunctionAbstract, true
	case "jsonserializable":
		return ClassJsonSerializable, true
	case "stringable":
		return ClassStringable, true
	default:
		return 0, false
	}
}

func BuiltinClassName(id uint32) (string, bool) {
	switch id {
	case ClassThrowable:
		return "Throwable", true
	case ClassException:
		return "Exception", true
	case ClassError:
		return "Error", true
	case ClassTypeError:
		return "TypeError", true
	case ClassValueError:
		return "ValueError", true
	case ClassArithmeticError:
		return "ArithmeticError", true
	case ClassDivisionByZeroError:
		return "DivisionByZeroError", true
	case ClassUnhandledMatchError:
		return "UnhandledMatchError", true
	case ClassArgumentCountError:
		return "ArgumentCountError", true
	case ClassClosure:
		return "Closure", true
	case ClassUnitEnum:
		return "UnitEnum", true
	case ClassBackedEnum:
		return "BackedEnum", true
	case ClassGenerator:
		return "Generator", true
	case ClassTraversable:
		return "Traversable", true
	case ClassIterator:
		return "Iterator", true
	case ClassIteratorAggregate:
		return "IteratorAggregate", true
	case ClassSeekableIterator:
		return "SeekableIterator", true
	case ClassCountable:
		return "Countable", true
	case ClassArrayAccess:
		return "ArrayAccess", true
	case ClassRuntimeException:
		return "RuntimeException", true
	case ClassLogicException:
		return "LogicException", true
	case ClassBadFunctionCallException:
		return "BadFunctionCallException", true
	case ClassBadMethodCallException:
		return "BadMethodCallException", true
	case ClassDomainException:
		return "DomainException", true
	case ClassInvalidArgumentException:
		return "InvalidArgumentException", true
	case ClassLengthException:
		return "LengthException", true
	case ClassOutOfBoundsException:
		return "OutOfBoundsException", true
	case ClassOutOfRangeException:
		return "OutOfRangeException", true
	case ClassOverflowException:
		return "OverflowException", true
	case ClassRangeException:
		return "RangeException", true
	case ClassUnderflowException:
		return "UnderflowException", true
	case ClassUnexpectedValueException:
		return "UnexpectedValueException", true
	case ClassJsonSerializable:
		return "JsonSerializable", true
	case ClassStringable:
		return "Stringable", true
	case ClassFiber:
		return "Fiber", true
	case ClassFiberError:
		return "FiberError", true
	case ClassSplDoublyLinkedList:
		return "SplDoublyLinkedList", true
	case ClassSplStack:
		return "SplStack", true
	case ClassSplQueue:
		return "SplQueue", true
	case ClassSplFixedArray:
		return "SplFixedArray", true
	case ClassSplObjectStorage:
		return "SplObjectStorage", true
	case ClassArrayObject:
		return "ArrayObject", true
	case ClassArrayIterator:
		return "ArrayIterator", true
	case ClassStdClass:
		return "stdClass", true
	case ClassPDO:
		return "PDO", true
	case ClassPDOStatement:
		return "PDOStatement", true
	case ClassPDOException:
		return "PDOException", true
	case ClassDateTime:
		return "DateTime", true
	case ClassDateTimeImmutable:
		return "DateTimeImmutable", true
	case ClassDateTimeZone:
		return "DateTimeZone", true
	case ClassDateInterval:
		return "DateInterval", true
	case ClassDatePeriod:
		return "DatePeriod", true
	case ClassDateTimeInterface:
		return "DateTimeInterface", true
	case ClassDateError:
		return "DateError", true
	case ClassDateObjectError:
		return "DateObjectError", true
	case ClassDateRangeError:
		return "DateRangeError", true
	case ClassDateException:
		return "DateException", true
	case ClassDateInvalidTimeZoneException:
		return "DateInvalidTimeZoneException", true
	case ClassDateInvalidOperationException:
		return "DateInvalidOperationException", true
	case ClassDateMalformedStringException:
		return "DateMalformedStringException", true
	case ClassDateMalformedIntervalStringException:
		return "DateMalformedIntervalStringException", true
	case ClassReflectionException:
		return "ReflectionException", true
	case ClassReflectionClass:
		return "ReflectionClass", true
	case ClassReflectionObject:
		return "ReflectionObject", true
	case ClassReflectionMethod:
		return "ReflectionMethod", true
	case ClassReflectionFunction:
		return "ReflectionFunction", true
	case ClassReflectionProperty:
		return "ReflectionProperty", true
	case ClassReflectionParameter:
		return "ReflectionParameter", true
	case ClassReflectionClassConstant:
		return "ReflectionClassConstant", true
	case ClassReflectionEnum:
		return "ReflectionEnum", true
	case ClassReflectionEnumUnitCase:
		return "ReflectionEnumUnitCase", true
	case ClassReflectionEnumBackedCase:
		return "ReflectionEnumBackedCase", true
	case ClassReflectionNamedType:
		return "ReflectionNamedType", true
	case ClassReflectionUnionType:
		return "ReflectionUnionType", true
	case ClassReflectionIntersectionType:
		return "ReflectionIntersectionType", true
	case ClassReflectionAttribute:
		return "ReflectionAttribute", true
	case ClassReflector:
		return "Reflector", true
	case ClassReflectionType:
		return "ReflectionType", true
	case ClassReflectionFunctionAbstract:
		return "ReflectionFunctionAbstract", true
	default:
		return "", false
	}
}

func (p *Program) classByName(name string) *ClassProto {
	if p == nil {
		return nil
	}
	clean := strings.TrimPrefix(name, `\`)
	for _, c := range p.Classes {
		if c != nil && strings.EqualFold(strings.TrimPrefix(c.Name, `\`), clean) {
			return c
		}
	}
	for _, c := range p.Traits {
		if c != nil && strings.EqualFold(strings.TrimPrefix(c.Name, `\`), clean) {
			return c
		}
	}
	return nil
}

func Link(p *Program) error {
	if p == nil {
		return &VerifyError{PC: -1, Msg: "programme absent"}
	}
	p.NeedsSuperglobals = false
	nextFuncID := uint32(1)
	if p.Main != nil {
		p.Main.FuncID = nextFuncID
		nextFuncID++
		p.Main.ResolveCVMeta()
		if p.Main.NeedsSuperglobals {
			p.NeedsSuperglobals = true
		}
	}
	for _, fn := range p.Funcs {
		if fn != nil {
			fn.FuncID = nextFuncID
			nextFuncID++
			fn.ResolveCVMeta()
			if fn.NeedsSuperglobals {
				p.NeedsSuperglobals = true
			}
		}
	}
	for i := 0; i < len(p.Classes); i++ {
		c := p.Classes[i]
		if c == nil {
			continue
		}
		if c.ParentName != "" && c.ParentID == ClassNone {
			if id, ok := BuiltinClassID(c.ParentName); ok {
				c.ParentID = id
			} else if parent := p.classByName(c.ParentName); parent != nil {
				c.ParentID = parent.ID
			} else {
				ext := &ClassProto{
					Name: strings.TrimPrefix(c.ParentName, `\`),
					ID:   ClassUser + uint32(len(p.Classes)+len(p.Traits)),
				}
				p.Classes = append(p.Classes, ext)
				c.ParentID = ext.ID
			}
		}
		if err := flattenTraits(p, c); err != nil {
			return err
		}
		if err := checkInterfaces(p, c); err != nil {
			return err
		}
		for _, m := range c.Methods {
			if m.Func != nil {
				m.Func.FuncID = nextFuncID
				nextFuncID++
				m.Func.ResolveCVMeta()
				if m.Func.NeedsSuperglobals {
					p.NeedsSuperglobals = true
				}
			}
		}
	}
	for _, t := range p.Traits {
		if t == nil {
			continue
		}
		for _, m := range t.Methods {
			if m.Func != nil {
				m.Func.FuncID = nextFuncID
				nextFuncID++
				m.Func.ResolveCVMeta()
				if m.Func.NeedsSuperglobals {
					p.NeedsSuperglobals = true
				}
			}
		}
	}
	for _, c := range p.Classes {
		if c != nil {
			resolveClassMeta(p, c)
		}
	}
	return nil
}

func isBuiltinExceptionOrError(id uint32) bool {
	switch id {
	case ClassException, ClassError, ClassTypeError, ClassValueError, ClassArithmeticError,
		ClassDivisionByZeroError, ClassUnhandledMatchError, ClassArgumentCountError,
		ClassRuntimeException, ClassLogicException, ClassBadFunctionCallException,
		ClassBadMethodCallException, ClassDomainException, ClassInvalidArgumentException,
		ClassLengthException, ClassOutOfBoundsException, ClassOutOfRangeException,
		ClassOverflowException, ClassRangeException, ClassUnderflowException,
		ClassUnexpectedValueException, ClassFiberError, ClassPDOException,
		ClassDateError, ClassDateObjectError, ClassDateRangeError, ClassDateException,
		ClassDateInvalidTimeZoneException, ClassDateInvalidOperationException,
		ClassDateMalformedStringException, ClassDateMalformedIntervalStringException,
		ClassReflectionException:
		return true
	default:
		return false
	}
}

func resolveClassMeta(p *Program, c *ClassProto) {
	var chain [64]*ClassProto
	nChain := 0
	cur := c
	isExc := false
	for cur != nil && nChain < 64 {
		chain[nChain] = cur
		nChain++
		if isBuiltinExceptionOrError(cur.ID) {
			isExc = true
		}
		if cur.ParentID == 0 || cur.ParentID == ClassNone || cur.ParentID == cur.ID {
			break
		}
		if isBuiltinExceptionOrError(cur.ParentID) {
			isExc = true
			break
		}
		var next *ClassProto
		for _, cand := range p.Classes {
			if cand != nil && cand.ID == cur.ParentID {
				next = cand
				break
			}
		}
		cur = next
	}
	base := uint32(0)
	if isExc && c.ID >= ClassUser {
		base = 4
	}
	index := base
	c.ResolvedProps = c.ResolvedProps[:0]
	for i := nChain - 1; i >= 0; i-- {
		cl := chain[i]
		for _, prop := range cl.Props {
			if prop.Static {
				continue
			}
			replaced := false
			for j := range c.ResolvedProps {
				if c.ResolvedProps[j].Name == prop.Name && c.ResolvedProps[j].Visibility != VisPrivate {
					prop.Index = c.ResolvedProps[j].Index
					c.ResolvedProps[j] = prop
					replaced = true
					break
				}
			}
			if !replaced {
				prop.Index = index
				index++
				c.ResolvedProps = append(c.ResolvedProps, prop)
			}
		}
	}
	c.InstancePropCount = index
	c.HasDestructor = false
	for i := 0; i < nChain; i++ {
		cl := chain[i]
		for _, m := range cl.Methods {
			if strings.EqualFold(m.Name, "__destruct") && m.Func != nil {
				c.HasDestructor = true
				break
			}
		}
		if c.HasDestructor {
			break
		}
	}
	c.PropsResolved = true
}

func flattenTraits(p *Program, c *ClassProto) error {
	for _, name := range c.Traits {
		trait := p.classByName(name)
		if trait == nil {
			continue
		}
		if trait.Kind != KindTrait && !isTraitSlice(p, trait) {
			continue
		}
		for _, m := range trait.Methods {
			if _, ok := methodLocal(c, m.Name); ok {
				continue
			}
			copied := m
			copied.ClassID = c.ID
			c.Methods = append(c.Methods, copied)
		}
		base := uint32(len(c.Props))
		for _, prop := range trait.Props {
			if _, ok := propLocal(c, prop.Name); ok {
				continue
			}
			prop.Index = base
			base++
			c.Props = append(c.Props, prop)
		}
	}
	return nil
}

func isTraitSlice(p *Program, c *ClassProto) bool {
	return slices.Contains(p.Traits, c)
}

func methodLocal(c *ClassProto, name string) (MethodProto, bool) {
	for _, m := range c.Methods {
		if strings.EqualFold(m.Name, name) {
			return m, true
		}
	}
	return MethodProto{}, false
}

func propLocal(c *ClassProto, name string) (PropInfo, bool) {
	for _, prop := range c.Props {
		if prop.Name == name {
			return prop, true
		}
	}
	return PropInfo{}, false
}

func checkInterfaces(p *Program, c *ClassProto) error {
	if c.Kind != KindClass || c.Flags&ClassAbstract != 0 {
		return nil
	}
	for _, name := range c.Interfaces {
		iface := p.classByName(name)
		if iface == nil || iface.Kind != KindInterface {
			continue
		}
		for _, m := range iface.Methods {
			if _, ok := methodOn(p, c, m.Name); !ok {
				return &VerifyError{PC: -1, Msg: "méthode d'interface absente: " + c.Name + "::" + m.Name}
			}
		}
	}
	return nil
}

func methodOn(p *Program, c *ClassProto, name string) (MethodProto, bool) {
	for c != nil {
		if m, ok := methodLocal(c, name); ok {
			return m, true
		}
		if len(c.Traits) > 0 {
			for _, tname := range c.Traits {
				if p.classByName(tname) == nil {
					return MethodProto{}, true
				}
			}
		}
		if c.ParentID == ClassNone {
			return MethodProto{}, false
		}
		if _, ok := BuiltinClassID(c.ParentName); ok || c.ParentID < ClassUser {
			return MethodProto{}, true
		}
		parent := (*ClassProto)(nil)
		for _, cand := range p.Classes {
			if cand != nil && cand.ID == c.ParentID {
				parent = cand
				break
			}
		}
		if parent != nil && len(parent.Methods) == 0 && len(parent.Props) == 0 && parent.ParentName == "" {
			return MethodProto{}, true
		}
		c = parent
	}
	return MethodProto{}, false
}

