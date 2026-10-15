package bytecode

type Opcode uint16

const (
	OpNop Opcode = iota
	OpConst
	OpPop
	OpDup
	OpEcho
	OpPrint

	OpLoadCV
	OpStoreCV
	OpAssignCV
	OpAssignRefCV
	OpUnsetCV
	OpIssetCV
	OpEmptyCV
	OpMakeRef
	OpDeref

	OpAdd
	OpSub
	OpMul
	OpDiv
	OpMod
	OpPow
	OpSL
	OpSR
	OpConcat
	OpBWOr
	OpBWAnd
	OpBWXor
	OpBWNot
	OpBoolNot
	OpBoolXor
	OpUnaryPlus
	OpUnaryMinus
	OpCast

	OpIsIdentical
	OpIsNotIdentical
	OpIsEqual
	OpIsNotEqual
	OpIsSmaller
	OpIsSmallerOrEqual
	OpSpaceship

	OpPreIncCV
	OpPreDecCV
	OpPostIncCV
	OpPostDecCV

	OpJmp
	OpJmpZ
	OpJmpNZ
	OpJmpNull
	OpSwitchLong
	OpSwitchString
	OpMatch
	OpReturn
	OpExit

	OpInitArray
	OpAddArrayElement
	OpAppendArrayElement
	OpFetchDimR
	OpFetchDimW
	OpFetchDimIs
	OpAssignDim
	OpAssignDimOp
	OpUnsetDim
	OpIssetDim
	OpEmptyDim
	OpFeResetR
	OpFeResetRW
	OpFeFetchR
	OpFeFetchRW
	OpFeFree

	OpInitCall
	OpInitMethodCall
	OpInitStaticMethodCall
	OpInitNew
	OpSendVal
	OpSendRef
	OpSendNamed
	OpSendUnpack
	OpDoCall
	OpBindClosure

	OpFetchPropR
	OpFetchPropW
	OpAssignProp
	OpAssignPropOp
	OpUnsetProp
	OpIssetProp
	OpEmptyProp
	OpFetchStaticPropR
	OpAssignStaticProp
	OpFetchClassConst
	OpInstanceof
	OpClone

	OpThrow
	OpEnterCatch
	OpEnterFinally
	OpLeaveFinally
	OpFastCallFinally
	OpFastRetFinally
	OpStaticCheck
	OpBindStatic
	OpEval
	OpInclude
	OpBindGlobal
	OpGlobalGet
	OpGlobalSet
	OpGlobalUnset
	OpGlobalIsset
	OpYield
	OpYieldFrom
)

func (op Opcode) String() string {
	if int(op) < len(opcodeNames) {
		return opcodeNames[op]
	}
	return "Op?"
}

var opcodeNames = [...]string{
	OpYield: "OpYield", OpYieldFrom: "OpYieldFrom",
	OpGlobalGet: "OpGlobalGet", OpGlobalSet: "OpGlobalSet", OpGlobalUnset: "OpGlobalUnset", OpGlobalIsset: "OpGlobalIsset",
	OpBindGlobal: "OpBindGlobal",
	OpEval:       "OpEval", OpInclude: "OpInclude",
	OpStaticCheck: "OpStaticCheck", OpBindStatic: "OpBindStatic",
	OpNop: "OpNop", OpConst: "OpConst", OpPop: "OpPop", OpDup: "OpDup", OpEcho: "OpEcho", OpPrint: "OpPrint",
	OpLoadCV: "OpLoadCV", OpStoreCV: "OpStoreCV", OpAssignCV: "OpAssignCV", OpAssignRefCV: "OpAssignRefCV",
	OpUnsetCV: "OpUnsetCV", OpIssetCV: "OpIssetCV", OpEmptyCV: "OpEmptyCV", OpMakeRef: "OpMakeRef", OpDeref: "OpDeref",
	OpAdd: "OpAdd", OpSub: "OpSub", OpMul: "OpMul", OpDiv: "OpDiv", OpMod: "OpMod", OpPow: "OpPow",
	OpSL: "OpSL", OpSR: "OpSR", OpConcat: "OpConcat", OpBWOr: "OpBWOr", OpBWAnd: "OpBWAnd", OpBWXor: "OpBWXor",
	OpBWNot: "OpBWNot", OpBoolNot: "OpBoolNot", OpBoolXor: "OpBoolXor", OpUnaryPlus: "OpUnaryPlus",
	OpUnaryMinus: "OpUnaryMinus", OpCast: "OpCast",
	OpIsIdentical: "OpIsIdentical", OpIsNotIdentical: "OpIsNotIdentical", OpIsEqual: "OpIsEqual",
	OpIsNotEqual: "OpIsNotEqual", OpIsSmaller: "OpIsSmaller", OpIsSmallerOrEqual: "OpIsSmallerOrEqual",
	OpSpaceship: "OpSpaceship",
	OpPreIncCV:  "OpPreIncCV", OpPreDecCV: "OpPreDecCV", OpPostIncCV: "OpPostIncCV", OpPostDecCV: "OpPostDecCV",
	OpJmp: "OpJmp", OpJmpZ: "OpJmpZ", OpJmpNZ: "OpJmpNZ", OpJmpNull: "OpJmpNull",
	OpSwitchLong: "OpSwitchLong", OpSwitchString: "OpSwitchString", OpMatch: "OpMatch", OpReturn: "OpReturn", OpExit: "OpExit",
	OpInitArray: "OpInitArray", OpAddArrayElement: "OpAddArrayElement", OpAppendArrayElement: "OpAppendArrayElement",
	OpFetchDimR: "OpFetchDimR", OpFetchDimW: "OpFetchDimW", OpFetchDimIs: "OpFetchDimIs",
	OpAssignDim: "OpAssignDim", OpAssignDimOp: "OpAssignDimOp", OpUnsetDim: "OpUnsetDim",
	OpIssetDim: "OpIssetDim", OpEmptyDim: "OpEmptyDim",
	OpFeResetR: "OpFeResetR", OpFeResetRW: "OpFeResetRW", OpFeFetchR: "OpFeFetchR", OpFeFetchRW: "OpFeFetchRW", OpFeFree: "OpFeFree",
	OpInitCall: "OpInitCall", OpInitMethodCall: "OpInitMethodCall", OpInitStaticMethodCall: "OpInitStaticMethodCall",
	OpInitNew: "OpInitNew", OpSendVal: "OpSendVal", OpSendRef: "OpSendRef", OpSendNamed: "OpSendNamed",
	OpSendUnpack: "OpSendUnpack", OpDoCall: "OpDoCall", OpBindClosure: "OpBindClosure",
	OpFetchPropR: "OpFetchPropR", OpFetchPropW: "OpFetchPropW", OpAssignProp: "OpAssignProp", OpAssignPropOp: "OpAssignPropOp",
	OpUnsetProp: "OpUnsetProp", OpIssetProp: "OpIssetProp", OpEmptyProp: "OpEmptyProp",
	OpFetchStaticPropR: "OpFetchStaticPropR", OpAssignStaticProp: "OpAssignStaticProp",
	OpFetchClassConst: "OpFetchClassConst", OpInstanceof: "OpInstanceof", OpClone: "OpClone",
	OpThrow: "OpThrow", OpEnterCatch: "OpEnterCatch", OpEnterFinally: "OpEnterFinally",
	OpLeaveFinally: "OpLeaveFinally", OpFastCallFinally: "OpFastCallFinally", OpFastRetFinally: "OpFastRetFinally",
}

const (
	CastInt uint32 = iota
	CastFloat
	CastString
	CastBool
	CastArray
	CastObject
	CastUnset
)

const (
	FlagStringConst uint16 = 1 << 0
	FlagNamedConst  uint16 = 1 << 1
	FlagReturnVoid  uint16 = 1 << 0
	FlagExitVoid    uint16 = 1 << 0
	FlagHasKey      uint16 = 1 << 0
	// FlagDiscard omet l'empilement de la valeur produite par une affectation en instruction.
	FlagDiscard uint16 = 1 << 0
	// FlagAssignDimCV cible directement la variable compilee Op1 sans empiler le tableau.
	FlagAssignDimCV uint16 = 1 << 2
	FlagGlobal      uint16 = 1 << 2
	FlagLoadRef     uint16 = 1 << 3
	FlagUseThis     uint16 = 1 << 0
	FlagParent      uint16 = 1 << 1
	FlagSelf        uint16 = 1 << 2
	FlagStatic      uint16 = 1 << 3
	FlagClosure     uint16 = 1 << 4
	FlagNative      uint16 = 1 << 7
	FlagPopObject   uint16 = 1 << 5
	FlagClassMagic  uint16 = 1 << 6
	FlagIsset       uint16 = 1 << 8
)

const (
	NoPC uint32 = ^uint32(0)
	NoCV uint32 = ^uint32(0)
)

const (
	ClassThrowable uint32 = 1 + iota
	ClassException
	ClassError
	ClassTypeError
	ClassValueError
	ClassArithmeticError
	ClassDivisionByZeroError
	ClassUnhandledMatchError
	ClassArgumentCountError
	ClassClosure
	ClassUnitEnum
	ClassBackedEnum
	ClassGenerator
	ClassTraversable
	ClassIterator
	ClassIteratorAggregate
	ClassSeekableIterator
	ClassCountable
	ClassArrayAccess
	ClassRuntimeException
	ClassLogicException
	ClassBadFunctionCallException
	ClassBadMethodCallException
	ClassDomainException
	ClassInvalidArgumentException
	ClassLengthException
	ClassOutOfBoundsException
	ClassOutOfRangeException
	ClassOverflowException
	ClassRangeException
	ClassUnderflowException
	ClassUnexpectedValueException
	ClassJsonSerializable
	ClassStringable
)

const (
	ClassFiber               uint32 = 5201
	ClassFiberError          uint32 = 5202
	ClassSplDoublyLinkedList uint32 = 5301
	ClassSplStack            uint32 = 5302
	ClassSplQueue            uint32 = 5303
	ClassSplFixedArray       uint32 = 5304
	ClassSplObjectStorage    uint32 = 5305
	ClassArrayObject         uint32 = 5306
	ClassArrayIterator       uint32 = 5307
	ClassStdClass            uint32 = 0x5c1a55
	ClassPDO                              uint32 = 0x50444f01
	ClassPDOStatement                     uint32 = 0x50444f02
	ClassPDOException                     uint32 = 0x50444f03
	ClassDateTime                         uint32 = 5401
	ClassDateTimeImmutable                uint32 = 5402
	ClassDateTimeZone                     uint32 = 5403
	ClassDateInterval                     uint32 = 5404
	ClassDatePeriod                       uint32 = 5405
	ClassDateTimeInterface                uint32 = 5406
	ClassDateError                        uint32 = 5407
	ClassDateObjectError                  uint32 = 5408
	ClassDateRangeError                   uint32 = 5409
	ClassDateException                    uint32 = 5410
	ClassDateInvalidTimeZoneException     uint32 = 5411
	ClassDateInvalidOperationException    uint32 = 5412
	ClassDateMalformedStringException     uint32 = 5413
	ClassDateMalformedIntervalStringException uint32 = 5414
	ClassReflectionException              uint32 = 5501
	ClassReflectionClass                  uint32 = 5502
	ClassReflectionObject                 uint32 = 5503
	ClassReflectionMethod                 uint32 = 5504
	ClassReflectionFunction               uint32 = 5505
	ClassReflectionProperty               uint32 = 5506
	ClassReflectionParameter              uint32 = 5507
	ClassReflectionClassConstant          uint32 = 5508
	ClassReflectionEnum                   uint32 = 5509
	ClassReflectionEnumUnitCase           uint32 = 5510
	ClassReflectionEnumBackedCase         uint32 = 5511
	ClassReflectionNamedType              uint32 = 5512
	ClassReflectionUnionType              uint32 = 5513
	ClassReflectionIntersectionType       uint32 = 5514
	ClassReflectionAttribute              uint32 = 5515
	ClassReflector                        uint32 = 5516
	ClassReflectionType                   uint32 = 5517
	ClassReflectionFunctionAbstract       uint32 = 5518
)

const ClassUser uint32 = 64

const ClassNone uint32 = 0
