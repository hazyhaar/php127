// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/vm: Machine Virtuelle Flatstack127 à zéro allocation

package vm

import (
	"fmt"
	"io"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/builtins"
	"code.hazyhaar.fr/devhoros/php127/pdo"
	"code.hazyhaar.fr/devhoros/php127/security"
	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"github.com/hazyhaar/c2pkg/c2blueteam"
	"github.com/hazyhaar/c2pkg/c2mem"
	"github.com/hazyhaar/c2pkg/c2ordered_swiss"
	"github.com/hazyhaar/c2pkg/c2pcre"
	"github.com/hazyhaar/c2pkg/c2strsimd"
)

type Opcode uint8

const (
	OpNop Opcode = iota
	OpLoadConst
	OpLoadConstStr
	OpLoadVar
	OpStoreVar
	OpDup
	OpPop

	// Arithmétique & Math
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpMod
	OpPow
	OpNeg
	OpNot

	// Comparaisons
	OpEq
	OpNotEq
	OpIdentical
	OpNotIdentical
	OpLt
	OpLte
	OpGt
	OpGte
	OpSpaceship

	// Bitwise & Décalages
	OpBitAnd
	OpBitOr
	OpBitXor
	OpBitNot
	OpShiftLeft
	OpShiftRight

	// Chaînes & SIMD
	OpConcat
	OpStrLen
	OpStrPos
	OpSubstr

	// Tableaux & Itération
	OpArrayInit
	OpArraySet
	OpArrayGet
	OpArrayPop
	OpArrayLen
	OpArrayIterInit
	OpArrayIterNext

	// Contrôle de flux
	OpJmp
	OpJmpZ
	OpJmpNZ
	OpSwitch

	// Fonctions & Frames
	OpCall
	OpCallDynamic
	OpRetFrame
	OpGetArg
	OpThrow

	// Modèle Objet & Classes
	OpNewObject
	OpGetProp
	OpSetProp
	OpCallMethod
	OpCallStatic
	OpGetConst
	OpInstanceOf

	// Buffers de sortie SPL
	OpObStart
	OpObGetClean
	OpObGetContents
	OpObEndFlush

	// Primitives Standard & Builtins
	OpBuiltinCall

	// Sécurité & BlueTeam
	OpPregMatch
	OpBlueteamEntropy
	OpBlueteamClassify
	OpSecurityCheck

	// E/S & Retour
	OpEcho
	OpPrint
	OpReturn

	// Opérateur de silence @
	OpSilenceBegin
	OpSilenceEnd
)

type Instruction struct {
	Op    Opcode
	A     uint16
	B     uint16
	C     uint16
	Const types.Value
}

type ClassDef struct {
	Name         string
	Parent       string
	Implements   []string
	Methods      map[string]uint16 // method name lowercase -> function index in BytecodeProgram.Functions
	Constants    map[string]types.Value
	PropNames    []string
	PropOffsets  map[string]int
	PropDefaults []types.Value
}

type ExceptionHandler struct {
	StartPC   int
	EndPC     int
	CatchPC   int
	FinallyPC int
	CatchType string
	CatchVar  uint16
}

type BytecodeProgram struct {
	Instructions      []Instruction
	Constants         []types.Value
	StringPool        []string
	Functions         []BytecodeProgram
	Classes           []ClassDef
	ClassMap          map[string]uint16
	ExceptionHandlers []ExceptionHandler
	NumVars           uint16
}

type CallFrame struct {
	ReturnPC      int
	BaseSP        int
	BaseVar       int
	NumArgs       uint16
	IsConstructor bool
	Prog          *BytecodeProgram
}

type ObjectInstance struct {
	ClassIdx  uint16
	ClassName string
	Props     []types.Value
	PropsBuf  [32]types.Value
}

type FiberState uint8

const (
	FiberInit FiberState = iota
	FiberRunning
	FiberSuspended
	FiberTerminated
)

type FiberInstance struct {
	State       FiberState
	Callable    types.Value
	SavedStack  []types.Value
	SavedSP     int
	SavedVars   []types.Value
	SavedFrames []CallFrame
	SavedFP     int
	SavedProg   *BytecodeProgram
	SavedPC     int
	ReturnVal   types.Value
}

type VM struct {
	Stack         []types.Value
	StackBuf      [1024]types.Value
	SP            int
	Vars          []types.Value
	VarsBuf       [512]types.Value
	Frames        []CallFrame
	FramesBuf     [64]CallFrame
	FP            int
	Functions     []BytecodeProgram
	Classes       []ClassDef
	ClassMap      map[string]uint16
	Objects       []ObjectInstance
	ObjectsBuf    [128]ObjectInstance
	NextObject    int
	Fibers        map[int64]*FiberInstance
	NextFiber     int64
	CurrentFiber  int64
	Arena         c2mem.C2_arena_t
	ArenaBuf      [256 * 1024]byte
	Cleanups      [64]c2mem.C2_cleanup_entry_t
	SwissTables   [16]c2ordered_swiss.Swiss_table_t
	SwissCtrl     [16][128]byte
	SwissSlots    [16][128]uint32
	SwissElems    [16][64]c2ordered_swiss.Swiss_element_t
	SwissValues   [16][64]types.Value
	NextTable     int
	Output        io.Writer
	FormatBuf     [128]byte
	Guard         *security.Guard
	OutputBuffers [][]byte
	Streams       *streams.StreamManager
	DB            *pdo.MemoryDB
	SilenceLevel  int
}

func NewVM(out io.Writer) *VM {
	vm := &VM{
		Output:  out,
		Guard:   security.NewGuard(security.ModeEnforce),
		Streams: streams.NewStreamManager(nil),
		DB:      pdo.DefaultDB,
	}
	vm.Reset()
	return vm
}

func (vm *VM) Reset() {
	vm.SP = 0
	vm.FP = 0
	vm.Stack = vm.StackBuf[:]
	vm.Vars = vm.VarsBuf[:]
	vm.Frames = vm.FramesBuf[:]
	vm.Objects = vm.ObjectsBuf[:]
	if vm.Fibers == nil {
		vm.Fibers = make(map[int64]*FiberInstance)
	} else {
		for k := range vm.Fibers {
			delete(vm.Fibers, k)
		}
	}
	vm.NextFiber = 1
	vm.CurrentFiber = 0
	if vm.Streams == nil {
		vm.Streams = streams.NewStreamManager(nil)
	}
	if vm.DB == nil {
		vm.DB = pdo.DefaultDB
	}
	for i := range vm.Vars {
		vm.Vars[i] = types.NewNull()
	}
	c2mem.C2_arena_init(&vm.Arena, vm.ArenaBuf[:], uint32(len(vm.ArenaBuf)), vm.Cleanups[:], uint32(len(vm.Cleanups)))
	vm.NextTable = 0
	vm.NextObject = 0
	for i := range vm.Objects {
		vm.Objects[i] = ObjectInstance{}
	}
	vm.OutputBuffers = vm.OutputBuffers[:0]
	vm.SilenceLevel = 0
}

func (vm *VM) Push(v types.Value) {
	if vm.SP >= len(vm.Stack) {
		newStack := make([]types.Value, len(vm.Stack)*2)
		copy(newStack, vm.Stack)
		vm.Stack = newStack
	}
	vm.Stack[vm.SP] = v
	vm.SP++
}

func (vm *VM) Pop() types.Value {
	if vm.SP > 0 {
		vm.SP--
		return vm.Stack[vm.SP]
	}
	return types.NewNull()
}

func (vm *VM) Peek() types.Value {
	if vm.SP > 0 {
		return vm.Stack[vm.SP-1]
	}
	return types.NewNull()
}

func (vm *VM) Execute(prog *BytecodeProgram) (types.Value, error) {
	pc := 0
	currentProg := prog
	if len(prog.Functions) > 0 {
		vm.Functions = prog.Functions
	}
	if len(prog.Classes) > 0 {
		vm.Classes = prog.Classes
		vm.ClassMap = prog.ClassMap
	}

	for {
		if pc >= len(currentProg.Instructions) {
			if vm.FP > 0 {
				// Implicit return from function
				vm.FP--
				frame := vm.Frames[vm.FP]
				pc = frame.ReturnPC
				if frame.Prog != nil {
					currentProg = frame.Prog
				} else {
					currentProg = prog
				}
				continue
			}
			break
		}

		ins := currentProg.Instructions[pc]
		switch ins.Op {
		case OpNop:
			pc++

		case OpLoadConst:
			vm.Push(ins.Const)
			pc++

		case OpLoadConstStr:
			if int(ins.A) < len(currentProg.StringPool) {
				vm.Push(types.NewString(currentProg.StringPool[ins.A]))
			} else {
				vm.Push(types.NewString(""))
			}
			pc++

		case OpLoadVar:
			base := 0
			if vm.FP > 0 {
				base = vm.Frames[vm.FP-1].BaseVar
			}
			slot := base + int(ins.A)
			if slot < len(vm.Vars) {
				vm.Push(vm.Vars[slot])
			} else {
				vm.Push(types.NewNull())
			}
			pc++

		case OpStoreVar:
			val := vm.Pop()
			base := 0
			if vm.FP > 0 {
				base = vm.Frames[vm.FP-1].BaseVar
			}
			slot := base + int(ins.A)
			if slot < len(vm.Vars) {
				vm.Vars[slot] = val
			}
			pc++

		case OpDup:
			vm.Push(vm.Peek())
			pc++

		case OpPop:
			_ = vm.Pop()
			pc++

		// Arithmétique & Math
		case OpAdd:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.Add(a, b))
			pc++

		case OpSub:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.Sub(a, b))
			pc++

		case OpMul:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.Mul(a, b))
			pc++

		case OpDiv:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.Div(a, b))
			pc++

		case OpMod:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.Mod(a, b))
			pc++

		case OpPow:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.Pow(a, b))
			pc++

		case OpNeg:
			a := vm.Pop()
			vm.Push(types.Neg(a))
			pc++

		case OpNot:
			a := vm.Pop()
			vm.Push(types.Not(a))
			pc++

		// Comparaisons
		case OpEq:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.NewBool(types.Equal(a, b)))
			pc++

		case OpNotEq:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.NewBool(!types.Equal(a, b)))
			pc++

		case OpIdentical:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.NewBool(types.Identical(a, b)))
			pc++

		case OpNotIdentical:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.NewBool(!types.Identical(a, b)))
			pc++

		case OpLt:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.NewBool(types.Lt(a, b)))
			pc++

		case OpLte:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.NewBool(types.Lte(a, b)))
			pc++

		case OpGt:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.NewBool(types.Gt(a, b)))
			pc++

		case OpGte:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.NewBool(types.Gte(a, b)))
			pc++

		case OpSpaceship:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.NewLong(int64(types.Compare(a, b))))
			pc++

		// Bitwise
		case OpBitAnd:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.BitAnd(a, b))
			pc++

		case OpBitOr:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.BitOr(a, b))
			pc++

		case OpBitXor:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.BitXor(a, b))
			pc++

		case OpBitNot:
			a := vm.Pop()
			vm.Push(types.BitNot(a))
			pc++

		case OpShiftLeft:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.ShiftLeft(a, b))
			pc++

		case OpShiftRight:
			b := vm.Pop()
			a := vm.Pop()
			vm.Push(types.ShiftRight(a, b))
			pc++

		// Chaînes
		case OpConcat:
			b := vm.Pop()
			a := vm.Pop()
			aBytes := a.ToStringBuffered(vm.FormatBuf[:])
			aLen := len(aBytes)
			// Copy aBytes to temp buffer so b.ToStringBuffered does not overwrite if using FormatBuf
			var tempA [64]byte
			if aLen <= len(tempA) {
				copy(tempA[:], aBytes)
				aBytes = tempA[:aLen]
			} else {
				aBytes = append([]byte(nil), aBytes...)
			}
			bBytes := b.ToStringBuffered(vm.FormatBuf[:])
			bLen := len(bBytes)
			totalLen := uint32(aLen + bLen)

			offset := c2mem.C2_arena_alloc(&vm.Arena, totalLen)
			var resVal types.Value
			if offset >= 0 && offset+int(totalLen) <= len(vm.ArenaBuf) {
				copy(vm.ArenaBuf[offset:], aBytes)
				copy(vm.ArenaBuf[offset+aLen:], bBytes)
				resVal = types.NewStringBytes(vm.ArenaBuf[offset : offset+int(totalLen)])
			} else {
				res := make([]byte, totalLen)
				copy(res, aBytes)
				copy(res[aLen:], bBytes)
				resVal = types.NewStringBytes(res)
			}
			vm.Push(resVal.CombineTaint(a, b))
			pc++

		case OpStrLen:
			a := vm.Pop()
			if a.Type == types.TypeString {
				b := a.Bytes()
				ulen := c2strsimd.C2_mb_strlen_utf8(b, uint64(len(b)))
				vm.Push(types.NewLong(int64(ulen)))
			} else {
				vm.Push(types.NewLong(0))
			}
			pc++

		case OpStrPos:
			needle := vm.Pop()
			haystack := vm.Pop()
			pos := haystack.Pos(needle)
			if pos >= 0 {
				vm.Push(types.NewLong(pos))
			} else {
				vm.Push(types.NewBool(false))
			}
			pc++

		case OpSubstr:
			length := vm.Pop().ToLong()
			start := vm.Pop().ToLong()
			str := vm.Pop()
			vm.Push(types.Substr(str, start, length))
			pc++

		// Tableaux
		case OpArrayInit:
			if vm.NextTable < len(vm.SwissTables) {
				tIdx := vm.NextTable
				vm.NextTable++
				ht := &vm.SwissTables[tIdx]
				c2ordered_swiss.Swiss_init(
					ht,
					vm.SwissCtrl[tIdx][:],
					vm.SwissSlots[tIdx][:],
					vm.SwissElems[tIdx][:],
					64,
					64,
				)
				val := types.Value{
					Type: types.TypeArray,
					Data: int64(tIdx),
				}
				vm.Push(val)
			} else {
				vm.Push(types.NewNull())
			}
			pc++

		case OpArraySet:
			val := vm.Pop()
			key := vm.Pop()
			arr := vm.Pop()
			if arr.Type == types.TypeArray && int(arr.Data) < len(vm.SwissTables) {
				tIdx := int(arr.Data)
				ht := &vm.SwissTables[tIdx]
				k := key.ToLong()
				if c2ordered_swiss.Swiss_insert_packed_int(ht, k, uint32(val.Type), k) {
					if k >= 0 && k < 64 {
						vm.SwissValues[tIdx][k] = val
					}
				}
				arr.Len = ht.N_elements
			}
			pc++

		case OpArrayGet:
			key := vm.Pop()
			arr := vm.Pop()
			if arr.Type == types.TypeArray && int(arr.Data) < len(vm.SwissTables) {
				tIdx := int(arr.Data)
				ht := &vm.SwissTables[tIdx]
				k := key.ToLong()
				idx := c2ordered_swiss.Swiss_find_int(ht, k)
				if idx >= 0 && k >= 0 && k < 64 {
					resVal := vm.SwissValues[tIdx][k]
					vm.Push(resVal)
				} else {
					vm.Push(types.NewNull())
				}
			} else {
				vm.Push(types.NewNull())
			}
			pc++

		case OpArrayPop:
			arr := vm.Pop()
			if arr.Type == types.TypeArray && int(arr.Data) < len(vm.SwissTables) {
				ht := &vm.SwissTables[arr.Data]
				ok := c2ordered_swiss.Swiss_pop(ht)
				vm.Push(types.NewBool(ok))
			} else {
				vm.Push(types.NewBool(false))
			}
			pc++

		case OpArrayLen:
			arr := vm.Pop()
			if arr.Type == types.TypeArray && int(arr.Data) < len(vm.SwissTables) {
				ht := &vm.SwissTables[arr.Data]
				vm.Push(types.NewLong(int64(ht.N_elements)))
			} else {
				vm.Push(types.NewLong(0))
			}
			pc++

		case OpArrayIterInit:
			// Initialise un index d'itération sur la pile (index 0)
			vm.Push(types.NewLong(0))
			pc++

		case OpArrayIterNext:
			// Stack: [array, iterIndex] -> Si iterIndex < array.Len: push iterIndex+1, push key, push val, pc++
			// Sinon: pop iterIndex, pop array, jump to target (ins.A)
			iterVal := vm.Pop()
			arr := vm.Peek() // laisse arr sur la pile pour l'itération suivante
			idx := iterVal.ToLong()
			if arr.Type == types.TypeArray && int(arr.Data) < len(vm.SwissTables) {
				tIdx := int(arr.Data)
				ht := &vm.SwissTables[tIdx]
				if uint32(idx) < ht.N_elements && int(idx) < len(ht.Entries) && ht.Entries[idx].Val.Type_ != 0 {
					elem := ht.Entries[idx]
					k := elem.Int_key
					// Remet le prochain index
					vm.Push(types.NewLong(idx + 1))
					// Push key
					vm.Push(types.NewLong(k))
					// Push value
					var val types.Value
					if k >= 0 && k < 64 {
						val = vm.SwissValues[tIdx][k]
					}
					vm.Push(val)
					pc++
				} else {
					// Fin de boucle : dépile le tableau et saute
					_ = vm.Pop() // pop arr
					pc = int(ins.A)
				}
			} else {
				_ = vm.Pop() // pop arr
				pc = int(ins.A)
			}

		// Contrôle de flux
		case OpJmp:
			pc = int(ins.A)

		case OpJmpZ:
			cond := vm.Pop()
			if !cond.ToBool() {
				pc = int(ins.A)
			} else {
				pc++
			}

		case OpJmpNZ:
			cond := vm.Pop()
			if cond.ToBool() {
				pc = int(ins.A)
			} else {
				pc++
			}

		case OpSwitch:
			// ins.A = nombre de cases, les instructions suivantes ou Const contiennent les mappings
			// Pour un switch rapide, dispatch sur comparaison directe
			val := vm.Pop()
			matchFound := false
			_ = val
			_ = matchFound
			pc++

		// Fonctions & Frames
		case OpCall:
			funcIdx := int(ins.A)
			numArgs := ins.B
			if funcIdx < len(vm.Functions) {
				calleeProg := &vm.Functions[funcIdx]
				if vm.FP >= len(vm.Frames) {
					newFrames := make([]CallFrame, len(vm.Frames)*2)
					copy(newFrames, vm.Frames)
					vm.Frames = newFrames
				}
				baseVar := (vm.FP + 1) * 32
				if baseVar+32 >= len(vm.Vars) {
					newVars := make([]types.Value, baseVar+64)
					copy(newVars, vm.Vars)
					vm.Vars = newVars
				}
				frame := CallFrame{
					ReturnPC: pc + 1,
					BaseSP:   vm.SP - int(numArgs),
					BaseVar:  baseVar,
					NumArgs:  numArgs,
					Prog:     currentProg,
				}
				vm.Frames[vm.FP] = frame
				vm.FP++
				currentProg = calleeProg
				pc = 0
			} else {
				return types.NewNull(), fmt.Errorf("fonction #%d introuvable", funcIdx)
			}

		case OpCallDynamic:
			numArgs := ins.A
			callee := vm.Pop()
			funcIdx := -1

			if callee.Type == types.TypeLong {
				funcIdx = int(callee.ToLong())
			} else if callee.Type == types.TypeObject && int(callee.Data) < len(vm.Objects) {
				obj := &vm.Objects[callee.Data]
				cls := vm.Classes[obj.ClassIdx]
				if mIdx, ok := cls.Methods["__invoke"]; ok {
					funcIdx = int(mIdx)
				}
			}

			if funcIdx >= 0 && funcIdx < len(vm.Functions) {
				calleeProg := &vm.Functions[funcIdx]
				if vm.FP >= len(vm.Frames) {
					newFrames := make([]CallFrame, len(vm.Frames)*2)
					copy(newFrames, vm.Frames)
					vm.Frames = newFrames
				}
				baseVar := (vm.FP + 1) * 32
				if baseVar+32 >= len(vm.Vars) {
					newVars := make([]types.Value, baseVar+64)
					copy(newVars, vm.Vars)
					vm.Vars = newVars
				}
				frame := CallFrame{
					ReturnPC: pc + 1,
					BaseSP:   vm.SP - int(numArgs),
					BaseVar:  baseVar,
					NumArgs:  numArgs,
					Prog:     currentProg,
				}
				vm.Frames[vm.FP] = frame
				vm.FP++
				if callee.Type == types.TypeObject {
					vm.Vars[frame.BaseVar] = callee
				}
				currentProg = calleeProg
				pc = 0
			} else {
				vm.SP -= int(numArgs)
				vm.Push(types.NewNull())
				pc++
			}

		case OpGetArg:
			argIdx := int(ins.A)
			if vm.FP > 0 {
				frame := vm.Frames[vm.FP-1]
				if argIdx < int(frame.NumArgs) {
					val := vm.Stack[frame.BaseSP+argIdx]
					vm.Push(val)
				} else {
					vm.Push(types.NewNull())
				}
			} else {
				vm.Push(types.NewNull())
			}
			pc++

		case OpRetFrame:
			var retVal types.Value = types.NewNull()
			if vm.SP > 0 {
				retVal = vm.Pop()
			}
			if vm.FP > 0 {
				vm.FP--
				frame := vm.Frames[vm.FP]
				// Nettoie la pile d'arguments
				vm.SP = frame.BaseSP
				// Si constructeur, la valeur résultante de 'new' est l'instance $this
				if frame.IsConstructor {
					retVal = vm.Vars[frame.BaseVar]
				}
				// Remet la valeur de retour sur la pile appelante
				vm.Push(retVal)
				pc = frame.ReturnPC
				if frame.Prog != nil {
					currentProg = frame.Prog
				} else {
					currentProg = prog
				}
			} else {
				return retVal, nil
			}

		case OpThrow:
			excVal := vm.Pop()
			handled := false

			for {
				base := 0
				if vm.FP > 0 {
					base = vm.Frames[vm.FP-1].BaseVar
				}

				for _, h := range currentProg.ExceptionHandlers {
					if pc >= h.StartPC && pc < h.EndPC {
						matched := false
						if excVal.Type == types.TypeObject && int(excVal.Data) < len(vm.Objects) {
							obj := &vm.Objects[excVal.Data]
							currIdx := int(obj.ClassIdx)
							depth := 0
							for currIdx < len(vm.Classes) && depth < 32 {
								depth++
								cls := vm.Classes[currIdx]
								if h.CatchType == "" || strings.EqualFold(h.CatchType, "Throwable") || strings.EqualFold(cls.Name, h.CatchType) || strings.EqualFold(cls.Parent, h.CatchType) {
									matched = true
									break
								}
								if strings.EqualFold(h.CatchType, "Exception") && (strings.EqualFold(cls.Name, "Exception") || strings.EqualFold(cls.Parent, "Exception") || strings.Contains(strings.ToLower(cls.Name), "exception")) {
									matched = true
									break
								}
								if strings.EqualFold(h.CatchType, "Error") && (strings.EqualFold(cls.Name, "Error") || strings.EqualFold(cls.Parent, "Error") || strings.Contains(strings.ToLower(cls.Name), "error")) {
									matched = true
									break
								}
								for _, iface := range cls.Implements {
									if strings.EqualFold(iface, h.CatchType) {
										matched = true
										break
									}
								}
								if matched || cls.Parent == "" {
									break
								}
								if pIdx, ok := vm.ClassMap[strings.ToLower(cls.Parent)]; ok {
									currIdx = int(pIdx)
								} else {
									break
								}
							}
						} else if h.CatchType == "" || strings.EqualFold(h.CatchType, "Throwable") {
							matched = true
						}

						if matched {
							if vm.FP > 0 {
								vm.SP = vm.Frames[vm.FP-1].BaseSP
							} else {
								vm.SP = 0
							}
							slot := base + int(h.CatchVar)
							if slot < len(vm.Vars) {
								vm.Vars[slot] = excVal
							}
							pc = h.CatchPC
							handled = true
							break
						}
					}
				}

				if handled {
					break
				}

				// Unwind frame
				if vm.FP > 0 {
					vm.FP--
					frame := vm.Frames[vm.FP]
					vm.SP = frame.BaseSP
					pc = frame.ReturnPC
					if frame.Prog != nil {
						currentProg = frame.Prog
					} else {
						currentProg = prog
					}
				} else {
					msg := excVal.String()
					if excVal.Type == types.TypeObject && int(excVal.Data) < len(vm.Objects) {
						obj := &vm.Objects[excVal.Data]
						if obj.Props[0].Type == types.TypeString {
							msg = obj.Props[0].String()
						}
						return types.NewNull(), fmt.Errorf("Uncaught %s: %s", obj.ClassName, msg)
					}
					return types.NewNull(), fmt.Errorf("Uncaught Exception: %s", msg)
				}
			}
			if handled {
				continue
			}

		// Modèle Objet & Classes
		case OpNewObject:
			classIdx := int(ins.A)
			numArgs := ins.B
			if classIdx < len(vm.Classes) {
				if vm.NextObject >= len(vm.Objects) {
					newObjects := make([]ObjectInstance, len(vm.Objects)*2)
					copy(newObjects, vm.Objects)
					vm.Objects = newObjects
				}
				cls := &vm.Classes[classIdx]
				objIdx := vm.NextObject
				vm.NextObject++
				obj := &vm.Objects[objIdx]
				obj.ClassIdx = uint16(classIdx)
				obj.ClassName = cls.Name
				if len(cls.PropDefaults) > len(obj.PropsBuf) {
					obj.Props = make([]types.Value, len(cls.PropDefaults))
				} else {
					obj.Props = obj.PropsBuf[:len(cls.PropDefaults)]
				}
				for pIdx, defVal := range cls.PropDefaults {
					obj.Props[pIdx] = defVal
				}
				objVal := types.Value{Type: types.TypeObject, Data: int64(objIdx)}

				// Instanciation spéciale PDO
				if cls.Name == "PDO" {
					dsnStr := ""
					userStr := ""
					passStr := ""
					if numArgs >= 1 {
						dsnStr = vm.Stack[vm.SP-int(numArgs)].String()
					}
					if numArgs >= 2 {
						userStr = vm.Stack[vm.SP-int(numArgs)+1].String()
					}
					if numArgs >= 3 {
						passStr = vm.Stack[vm.SP-int(numArgs)+2].String()
					}
					p, _ := pdo.NewPDO(dsnStr, userStr, passStr, nil)
					if p != nil {
						pId := pdo.RegisterPDOInstance(p)
						obj.Props[0] = types.NewLong(pId)
					}
					vm.SP -= int(numArgs)
					vm.Push(objVal)
					pc++
					continue
				}

				// Si constructeur présent
				if ctorIdx, ok := cls.Methods["__construct"]; ok && int(ctorIdx) < len(vm.Functions) {
					calleeProg := &vm.Functions[ctorIdx]
					if vm.FP >= len(vm.Frames) {
						newFrames := make([]CallFrame, len(vm.Frames)*2)
						copy(newFrames, vm.Frames)
						vm.Frames = newFrames
					}
					baseVar := (vm.FP + 1) * 32
					if baseVar+32 >= len(vm.Vars) {
						newVars := make([]types.Value, baseVar+64)
						copy(newVars, vm.Vars)
						vm.Vars = newVars
					}
					frame := CallFrame{
						ReturnPC:      pc + 1,
						BaseSP:        vm.SP - int(numArgs),
						BaseVar:       baseVar,
						NumArgs:       numArgs,
						IsConstructor: true,
						Prog:          currentProg,
					}
					vm.Frames[vm.FP] = frame
					vm.FP++
					// $this dans slot 0 de la frame
					vm.Vars[frame.BaseVar] = objVal
					currentProg = calleeProg
					pc = 0
				} else {
					// Pas de constructeur : dépiler les arguments s'il y en avait
					vm.SP -= int(numArgs)
					vm.Push(objVal)
					pc++
				}
			} else {
				vm.Push(types.NewNull())
				pc++
			}

		case OpGetProp:
			propSlot := int(ins.A)
			target := vm.Pop()
			if target.Type == types.TypeObject && int(target.Data) < len(vm.Objects) {
				obj := &vm.Objects[target.Data]
				if propSlot < len(obj.Props) {
					vm.Push(obj.Props[propSlot])
				} else {
					vm.Push(types.NewNull())
				}
			} else {
				vm.Push(types.NewNull())
			}
			pc++

		case OpSetProp:
			propSlot := int(ins.A)
			val := vm.Pop()
			target := vm.Pop()
			if target.Type == types.TypeObject && int(target.Data) < len(vm.Objects) {
				obj := &vm.Objects[target.Data]
				if propSlot >= len(obj.Props) {
					newProps := make([]types.Value, propSlot+1)
					copy(newProps, obj.Props)
					obj.Props = newProps
				}
				obj.Props[propSlot] = val
			}
			pc++

		case OpCallMethod:
			funcIdx := int(ins.A)
			numArgs := ins.B
			isNullsafe := ins.C == 1

			// Target object is at vm.Stack[vm.SP - int(numArgs) - 1]
			targetIdx := vm.SP - int(numArgs) - 1
			if targetIdx >= 0 {
				target := vm.Stack[targetIdx]
				if target.Type == types.TypeObject && int(target.Data) < len(vm.Objects) {
					obj := &vm.Objects[target.Data]
					if vm.isNativeClass(obj.ClassName) {
						resVal := vm.dispatchNativeMethod(obj, funcIdx, targetIdx, int(numArgs))
						vm.SP = targetIdx
						vm.Push(resVal)
						pc++
						continue
					}
					if funcIdx < len(vm.Functions) {
						calleeProg := &vm.Functions[funcIdx]
						frame := CallFrame{
							ReturnPC: pc + 1,
							BaseSP:   targetIdx,
							BaseVar:  (vm.FP + 1) * 32,
							NumArgs:  numArgs + 1,
							Prog:     currentProg,
						}
						vm.Frames[vm.FP] = frame
						vm.FP++
						// $this dans slot 0
						vm.Vars[frame.BaseVar] = target
						currentProg = calleeProg
						pc = 0
						continue
					}
				}
				if isNullsafe {
					vm.SP = targetIdx
					vm.Push(types.NewNull())
					pc++
				} else {
					vm.SP = targetIdx
					vm.Push(types.NewNull())
					pc++
				}
			} else {
				vm.Push(types.NewNull())
				pc++
			}

		case OpCallStatic:
			funcIdx := int(ins.A)
			numArgs := ins.B
			if funcIdx < len(vm.Functions) {
				calleeProg := &vm.Functions[funcIdx]
				frame := CallFrame{
					ReturnPC: pc + 1,
					BaseSP:   vm.SP - int(numArgs),
					BaseVar:  (vm.FP + 1) * 32,
					NumArgs:  numArgs,
					Prog:     currentProg,
				}
				vm.Frames[vm.FP] = frame
				vm.FP++
				currentProg = calleeProg
				pc = 0
			} else {
				vm.Push(types.NewNull())
				pc++
			}

		case OpGetConst:
			clsIdx := int(ins.A)
			constStrIdx := int(ins.B)
			res := types.NewNull()
			if clsIdx < len(vm.Classes) && constStrIdx < len(currentProg.StringPool) {
				cls := &vm.Classes[clsIdx]
				cName := currentProg.StringPool[constStrIdx]
				if cls.Constants != nil {
					if val, ok := cls.Constants[cName]; ok {
						res = val
					}
				}
			}
			vm.Push(res)
			pc++

		case OpInstanceOf:
			strIdx := int(ins.A)
			target := vm.Pop()
			matched := false
			if target.Type == types.TypeObject && int(target.Data) < len(vm.Objects) && strIdx < len(currentProg.StringPool) {
				obj := &vm.Objects[target.Data]
				targetClsName := currentProg.StringPool[strIdx]
				currIdx := int(obj.ClassIdx)
				for currIdx < len(vm.Classes) {
					cls := vm.Classes[currIdx]
					if strings.EqualFold(cls.Name, targetClsName) || strings.EqualFold(cls.Parent, targetClsName) {
						matched = true
						break
					}
					for _, iface := range cls.Implements {
						if strings.EqualFold(iface, targetClsName) {
							matched = true
							break
						}
					}
					if matched || cls.Parent == "" {
						break
					}
					if pIdx, ok := vm.ClassMap[strings.ToLower(cls.Parent)]; ok {
						currIdx = int(pIdx)
					} else {
						break
					}
				}
			}
			vm.Push(types.NewBool(matched))
			pc++

		// Buffers de sortie SPL
		case OpObStart:
			vm.OutputBuffers = append(vm.OutputBuffers, make([]byte, 0, 256))
			pc++

		case OpObGetClean:
			if len(vm.OutputBuffers) > 0 {
				top := vm.OutputBuffers[len(vm.OutputBuffers)-1]
				vm.OutputBuffers = vm.OutputBuffers[:len(vm.OutputBuffers)-1]
				vm.Push(types.NewString(string(top)))
			} else {
				vm.Push(types.NewBool(false))
			}
			pc++

		case OpObGetContents:
			if len(vm.OutputBuffers) > 0 {
				top := vm.OutputBuffers[len(vm.OutputBuffers)-1]
				vm.Push(types.NewString(string(top)))
			} else {
				vm.Push(types.NewBool(false))
			}
			pc++

		case OpObEndFlush:
			if len(vm.OutputBuffers) > 0 {
				top := vm.OutputBuffers[len(vm.OutputBuffers)-1]
				vm.OutputBuffers = vm.OutputBuffers[:len(vm.OutputBuffers)-1]
				if len(vm.OutputBuffers) > 0 {
					vm.OutputBuffers[len(vm.OutputBuffers)-1] = append(vm.OutputBuffers[len(vm.OutputBuffers)-1], top...)
				} else if vm.Output != nil {
					vm.Output.Write(top)
				}
			}
			pc++

		case OpBuiltinCall:
			builtinId := ins.A
			numArgs := int(ins.B)
			var builtinBuf [16]types.Value
			n := numArgs
			if n > 16 {
				n = 16
			}
			for i := numArgs - 1; i >= 0; i-- {
				val := vm.Pop()
				if i < 16 {
					builtinBuf[i] = val
				}
			}
			args := builtinBuf[:n]
			var res types.Value = types.NewNull()

			switch builtinId {
			case 1: // json_encode
				if len(args) >= 1 {
					res = builtins.JsonEncode(args[0])
				}
			case 2: // json_decode
				if len(args) >= 1 {
					assoc := false
					if len(args) >= 2 {
						assoc = args[1].ToBool()
					}
					res = builtins.JsonDecode(args[0], assoc)
				}
			case 3: // md5
				if len(args) >= 1 {
					res = builtins.Md5(args[0])
				}
			case 4: // sha256
				if len(args) >= 1 {
					res = builtins.Sha256(args[0])
				}
			case 5: // hash
				if len(args) >= 2 {
					res = builtins.Hash(args[0], args[1])
				}
			case 6: // hash_hmac
				if len(args) >= 3 {
					res = builtins.HashHmac(args[0], args[1], args[2])
				}
			case 7: // random_bytes
				if len(args) >= 1 {
					res = builtins.RandomBytes(args[0])
				}
			case 8: // bin2hex
				if len(args) >= 1 {
					res = builtins.Bin2Hex(args[0])
				}
			case 9: // hex2bin
				if len(args) >= 1 {
					res = builtins.Hex2Bin(args[0])
				}
			case 10: // time
				res = builtins.Time()
			case 11: // microtime
				asFloat := false
				if len(args) >= 1 {
					asFloat = args[0].ToBool()
				}
				res = builtins.Microtime(asFloat)
			case 12: // date
				if len(args) >= 1 {
					ts := types.NewNull()
					if len(args) >= 2 {
						ts = args[1]
					}
					res = builtins.Date(args[0], ts)
				}
			case 13: // strtotime
				if len(args) >= 1 {
					res = builtins.Strtotime(args[0])
				}
			case 14: // trim
				if len(args) >= 1 {
					mask := types.NewNull()
					if len(args) >= 2 {
						mask = args[1]
					}
					res = builtins.Trim(args[0], mask)
				}
			case 15: // ltrim
				if len(args) >= 1 {
					mask := types.NewNull()
					if len(args) >= 2 {
						mask = args[1]
					}
					res = builtins.Ltrim(args[0], mask)
				}
			case 16: // rtrim
				if len(args) >= 1 {
					mask := types.NewNull()
					if len(args) >= 2 {
						mask = args[1]
					}
					res = builtins.Rtrim(args[0], mask)
				}
			case 17: // strtolower
				if len(args) >= 1 {
					res = builtins.Strtolower(args[0])
				}
			case 18: // strtoupper
				if len(args) >= 1 {
					res = builtins.Strtoupper(args[0])
				}
			case 19: // str_replace
				if len(args) >= 3 {
					res = builtins.StrReplace(args[0], args[1], args[2])
				}
			case 20: // str_repeat
				if len(args) >= 2 {
					res = builtins.StrRepeat(args[0], args[1])
				}
			case 21: // is_callable
				if len(args) >= 1 {
					res = builtins.IsCallable(args[0])
				}
			case 30: // file_get_contents
				if len(args) >= 1 && vm.Streams != nil {
					res = vm.Streams.FileGetContents(args[0])
				}
			case 31: // file_put_contents
				if len(args) >= 2 && vm.Streams != nil {
					res = vm.Streams.FilePutContents(args[0], args[1])
				}
			case 32: // file_exists
				if len(args) >= 1 && vm.Streams != nil {
					res = vm.Streams.FileExists(args[0])
				}
			case 33: // dirname
				if len(args) >= 1 {
					res = streams.Dirname(args[0])
				}
			case 34: // basename
				if len(args) >= 1 {
					res = streams.Basename(args[0])
				}
			case 35: // pdo_exec
				if len(args) >= 1 && vm.DB != nil {
					n, _ := vm.DB.Exec(args[0].String(), args[1:]...)
					res = types.NewLong(n)
				}
			case 36: // pdo_query
				if len(args) >= 1 && vm.DB != nil {
					rows, _ := vm.DB.Query(args[0].String(), args[1:]...)
					res = types.NewLong(int64(len(rows)))
				}
			case 37: // pdo_last_id
				if vm.DB != nil {
					res = types.NewLong(vm.DB.LastInsertId())
				}
			case 40: // fiber_create
				if len(args) >= 1 {
					fId := vm.NextFiber
					vm.NextFiber++
					vm.Fibers[fId] = &FiberInstance{
						State:    FiberInit,
						Callable: args[0],
					}
					res = types.Value{Type: types.TypeObject, Data: fId}
				}
			case 41: // fiber_start
				if len(args) >= 1 && args[0].Type == types.TypeObject {
					fId := args[0].Data
					if fib, ok := vm.Fibers[fId]; ok && fib.State == FiberInit {
						fib.State = FiberRunning
						funcIdx := -1
						if fib.Callable.Type == types.TypeLong {
							funcIdx = int(fib.Callable.ToLong())
						} else if fib.Callable.Type == types.TypeObject && int(fib.Callable.Data) < len(vm.Objects) {
							obj := &vm.Objects[fib.Callable.Data]
							cls := vm.Classes[obj.ClassIdx]
							if mIdx, ok := cls.Methods["__invoke"]; ok {
								funcIdx = int(mIdx)
							}
						}
						if funcIdx >= 0 && funcIdx < len(vm.Functions) {
							calleeProg := &vm.Functions[funcIdx]
							fiberVM := NewVM(vm.Output)
							fiberVM.Functions = vm.Functions
							fiberVM.Classes = vm.Classes
							fiberVM.ClassMap = vm.ClassMap
							fiberVM.DB = vm.DB
							fiberVM.Streams = vm.Streams
							ret, _ := fiberVM.Execute(calleeProg)
							fib.State = FiberTerminated
							fib.ReturnVal = ret
							res = ret
						}
					}
				}
			case 42: // fiber_suspend
				if len(args) >= 1 {
					res = args[0]
				}
			case 43: // fiber_resume
				if len(args) >= 1 && args[0].Type == types.TypeObject {
					fId := args[0].Data
					if fib, ok := vm.Fibers[fId]; ok {
						if len(args) >= 2 {
							res = args[1]
						} else {
							res = fib.ReturnVal
						}
					}
				}
			case 44: // fiber_is_suspended
				if len(args) >= 1 && args[0].Type == types.TypeObject {
					if fib, ok := vm.Fibers[args[0].Data]; ok {
						res = types.NewBool(fib.State == FiberSuspended)
					} else {
						res = types.NewBool(false)
					}
				}
			case 45: // fiber_is_running
				if len(args) >= 1 && args[0].Type == types.TypeObject {
					if fib, ok := vm.Fibers[args[0].Data]; ok {
						res = types.NewBool(fib.State == FiberRunning)
					} else {
						res = types.NewBool(false)
					}
				}
			case 46: // fiber_is_terminated
				if len(args) >= 1 && args[0].Type == types.TypeObject {
					if fib, ok := vm.Fibers[args[0].Data]; ok {
						res = types.NewBool(fib.State == FiberTerminated)
					} else {
						res = types.NewBool(false)
					}
				}
			case 50: // getenv
				if len(args) >= 1 {
					res = builtins.Getenv(args[0])
				}
			case 51: // putenv
				if len(args) >= 1 {
					res = builtins.Putenv(args[0])
				}
			case 52: // parse_dotenv
				if len(args) >= 1 {
					parsed := builtins.ParseDotenv(args[0].String())
					res = types.NewLong(int64(len(parsed)))
				}
			case 53: // uuid_v4
				res = builtins.UuidV4()
			case 54: // uuid_v7
				res = builtins.UuidV7()
			case 55: // ulid
				res = builtins.Ulid()
			case 56: // monolog_log
				if len(args) >= 2 {
					name := types.NewString("app")
					if len(args) >= 3 {
						name = args[2]
					}
					res = builtins.LogMessage(args[0], args[1], name)
				}
			case 57: // curl_init
				url := types.NewNull()
				if len(args) >= 1 {
					url = args[0]
				}
				res = builtins.CurlInit(url)
			case 58: // curl_setopt
				if len(args) >= 3 {
					res = builtins.CurlSetopt(args[0], args[1], args[2])
				}
			case 59: // curl_exec
				if len(args) >= 1 {
					res = builtins.CurlExec(args[0])
				}
			case 60: // curl_getinfo
				if len(args) >= 2 {
					res = builtins.CurlGetinfo(args[0], args[1])
				}
			case 61: // curl_close
				if len(args) >= 1 {
					res = builtins.CurlClose(args[0])
				}
			case 62: // curl_error
				if len(args) >= 1 {
					res = builtins.CurlError(args[0])
				}
			case 63: // curl_errno
				if len(args) >= 1 {
					res = builtins.CurlErrno(args[0])
				}
			}

			vm.Push(res)
			pc++

		// Sécurité & BlueTeam
		case OpPregMatch:
			txtVal := vm.Pop()
			patVal := vm.Pop()
			pat := patVal.Bytes()
			txt := txtVal.Bytes()

			var re c2pcre.C2_regex_t
			c2pcre.C2_pcre_compile(pat, uint32(len(pat)), &re)
			var m c2pcre.C2_match_t
			matched := c2pcre.C2_pcre_exec(&re, txt, uint32(len(txt)), &m)
			vm.Push(types.NewLong(int64(matched)))
			pc++

		case OpBlueteamEntropy:
			val := vm.Pop()
			b := val.Bytes()
			entropyQ8 := c2blueteam.C2bt_calc_entropy_8_8(b, uint64(len(b)))
			f := float64(entropyQ8) / 256.0
			vm.Push(types.NewDouble(f))
			pc++

		case OpBlueteamClassify:
			val := vm.Pop()
			b := val.Bytes()
			var entropyQ8 uint32
			var charMask uint32
			pClass := c2blueteam.C2bt_classify_payload(b, uint64(len(b)), &entropyQ8, &charMask)
			vm.Push(types.NewLong(int64(pClass)))
			pc++

		case OpSecurityCheck:
			val := vm.Pop()
			sinkNameVal := vm.Pop()
			if vm.Guard != nil {
				if err := vm.Guard.CheckSink(sinkNameVal.String(), val); err != nil {
					return types.NewNull(), err
				}
			}
			pc++

		// E/S & Sortie
		case OpEcho, OpPrint:
			val := vm.Pop()
			buf := val.ToStringBuffered(vm.FormatBuf[:])
			if vm.Guard != nil {
				if err := vm.Guard.EgressInspect(buf); err != nil {
					return types.NewNull(), err
				}
			}
			if len(vm.OutputBuffers) > 0 {
				vm.OutputBuffers[len(vm.OutputBuffers)-1] = append(vm.OutputBuffers[len(vm.OutputBuffers)-1], buf...)
			} else if vm.Output != nil {
				vm.Output.Write(buf)
			}
			if ins.Op == OpPrint {
				vm.Push(types.NewLong(1))
			}
			pc++

		case OpSilenceBegin:
			vm.SilenceLevel++
			pc++

		case OpSilenceEnd:
			if vm.SilenceLevel > 0 {
				vm.SilenceLevel--
			}
			pc++

		case OpReturn:
			if vm.SP > 0 {
				return vm.Pop(), nil
			}
			return types.NewNull(), nil

		default:
			return types.NewNull(), fmt.Errorf("opcode inconnu: %d", ins.Op)
		}
	}

	if vm.SP > 0 {
		return vm.Pop(), nil
	}
	return types.NewNull(), nil
}

func (vm *VM) isNativeClass(name string) bool {
	return strings.EqualFold(name, "PDO") || strings.EqualFold(name, "PDOStatement")
}

func (vm *VM) allocObjectIdxByName(name string) int {
	clsLower := strings.ToLower(name)
	clsIdx := 0
	if idx, ok := vm.ClassMap[clsLower]; ok {
		clsIdx = int(idx)
	}
	if vm.NextObject >= len(vm.Objects) {
		newObjects := make([]ObjectInstance, len(vm.Objects)*2)
		copy(newObjects, vm.Objects)
		vm.Objects = newObjects
	}
	objIdx := vm.NextObject
	vm.NextObject++
	obj := &vm.Objects[objIdx]
	obj.ClassIdx = uint16(clsIdx)
	obj.ClassName = name
	if clsIdx < len(vm.Classes) {
		cls := &vm.Classes[clsIdx]
		if len(cls.PropDefaults) > len(obj.PropsBuf) {
			obj.Props = make([]types.Value, len(cls.PropDefaults))
		} else {
			obj.Props = obj.PropsBuf[:len(cls.PropDefaults)]
		}
		for pIdx, defVal := range cls.PropDefaults {
			obj.Props[pIdx] = defVal
		}
	}
	return objIdx
}

func (vm *VM) newArrayFromRow(row pdo.TableRow) types.Value {
	if vm.NextTable < len(vm.SwissTables) {
		tIdx := vm.NextTable
		vm.NextTable++
		ht := &vm.SwissTables[tIdx]
		c2ordered_swiss.Swiss_init(
			ht,
			vm.SwissCtrl[tIdx][:],
			vm.SwissSlots[tIdx][:],
			vm.SwissElems[tIdx][:],
			64,
			64,
		)
		idx := int64(0)
		for _, v := range row {
			if idx < 64 {
				c2ordered_swiss.Swiss_insert_packed_int(ht, idx, uint32(v.Type), idx)
				vm.SwissValues[tIdx][idx] = v
				idx++
			}
		}
		return types.Value{
			Type: types.TypeArray,
			Data: int64(tIdx),
			Len:  ht.N_elements,
		}
	}
	return types.NewNull()
}

func (vm *VM) newArrayFromRows(rows []pdo.TableRow) types.Value {
	if vm.NextTable < len(vm.SwissTables) {
		tIdx := vm.NextTable
		vm.NextTable++
		ht := &vm.SwissTables[tIdx]
		c2ordered_swiss.Swiss_init(
			ht,
			vm.SwissCtrl[tIdx][:],
			vm.SwissSlots[tIdx][:],
			vm.SwissElems[tIdx][:],
			64,
			64,
		)
		for i, r := range rows {
			if i < 64 {
				rArr := vm.newArrayFromRow(r)
				c2ordered_swiss.Swiss_insert_packed_int(ht, int64(i), uint32(rArr.Type), int64(i))
				vm.SwissValues[tIdx][i] = rArr
			}
		}
		return types.Value{
			Type: types.TypeArray,
			Data: int64(tIdx),
			Len:  ht.N_elements,
		}
	}
	return types.NewNull()
}

func (vm *VM) dispatchNativeMethod(obj *ObjectInstance, funcIdx int, targetIdx int, numArgs int) types.Value {
	args := make([]types.Value, numArgs)
	for i := 0; i < numArgs; i++ {
		args[i] = vm.Stack[targetIdx+1+i]
	}

	cls := vm.Classes[obj.ClassIdx]
	var methodName string
	for mName, fIdx := range cls.Methods {
		if int(fIdx) == funcIdx {
			methodName = mName
			break
		}
	}

	if strings.EqualFold(obj.ClassName, "PDO") {
		pId := obj.Props[0].ToLong()
		p, ok := pdo.GetPDOInstance(pId)
		if !ok || p == nil {
			return types.NewNull()
		}

		switch strings.ToLower(methodName) {
		case "exec":
			if len(args) > 0 {
				n, _ := p.Exec(args[0].String(), args[1:]...)
				return types.NewLong(n)
			}
		case "query":
			if len(args) > 0 {
				stmt, err := p.Query(args[0].String(), args[1:]...)
				if err != nil || stmt == nil {
					return types.NewBool(false)
				}
				sId := pdo.RegisterStmtInstance(stmt)
				stmtObjIdx := vm.allocObjectIdxByName("PDOStatement")
				vm.Objects[stmtObjIdx].Props[0] = types.NewLong(sId)
				return types.Value{Type: types.TypeObject, Data: int64(stmtObjIdx)}
			}
		case "prepare":
			if len(args) > 0 {
				stmt, err := p.Prepare(args[0].String())
				if err != nil || stmt == nil {
					return types.NewBool(false)
				}
				sId := pdo.RegisterStmtInstance(stmt)
				stmtObjIdx := vm.allocObjectIdxByName("PDOStatement")
				vm.Objects[stmtObjIdx].Props[0] = types.NewLong(sId)
				return types.Value{Type: types.TypeObject, Data: int64(stmtObjIdx)}
			}
		case "begintransaction":
			_ = p.BeginTransaction()
			return types.NewBool(true)
		case "commit":
			_ = p.Commit()
			return types.NewBool(true)
		case "rollback":
			_ = p.RollBack()
			return types.NewBool(true)
		case "intransaction":
			return types.NewBool(p.InTransaction())
		case "lastinsertid":
			return types.NewLong(p.LastInsertId())
		case "setattribute":
			if len(args) >= 2 {
				p.SetAttribute(int(args[0].ToLong()), args[1])
				return types.NewBool(true)
			}
		case "getattribute":
			if len(args) >= 1 {
				return p.GetAttribute(int(args[0].ToLong()))
			}
		}
	}

	if strings.EqualFold(obj.ClassName, "PDOStatement") {
		sId := obj.Props[0].ToLong()
		stmt, ok := pdo.GetStmtInstance(sId)
		if !ok || stmt == nil {
			return types.NewNull()
		}

		switch strings.ToLower(methodName) {
		case "execute":
			var execArgs []types.Value
			if len(args) > 0 {
				if args[0].Type == types.TypeArray {
					tIdx := int(args[0].Data)
					if tIdx < len(vm.SwissTables) {
						ht := &vm.SwissTables[tIdx]
						for i := 0; i < int(ht.N_elements); i++ {
							execArgs = append(execArgs, vm.SwissValues[tIdx][i])
						}
					}
				} else {
					execArgs = args
				}
			}
			ok, _ := stmt.Execute(execArgs)
			return types.NewBool(ok)

		case "fetch":
			row, found := stmt.Fetch()
			if !found {
				return types.NewBool(false)
			}
			return vm.newArrayFromRow(row)

		case "fetchall":
			rows := stmt.FetchAll()
			return vm.newArrayFromRows(rows)

		case "fetchcolumn":
			col := 0
			if len(args) > 0 {
				col = int(args[0].ToLong())
			}
			val, found := stmt.FetchColumn(col)
			if !found {
				return types.NewBool(false)
			}
			return val

		case "rowcount":
			return types.NewLong(int64(stmt.RowCount()))

		case "bindvalue":
			if len(args) >= 2 {
				stmt.BindValue(args[0].String(), args[1])
				return types.NewBool(true)
			}

		case "closecursor":
			stmt.CloseCursor()
			return types.NewBool(true)
		}
	}

	return types.NewNull()
}
