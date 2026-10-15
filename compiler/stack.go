package compiler

import (
	bc "code.hazyhaar.fr/devhoros/php127/bytecode"
	"fmt"
)

// Les gestionnaires partent de la pile vide restauree par la machine virtuelle.
func stackSize(fn *bc.OpArray) error {
	n := len(fn.Code)
	type edge struct{ pc, depth int }
	queue := make([]edge, 1, max(n, 8))
	queue[0] = edge{0, 0}
	depths := make([]int, n)
	for i := range depths {
		depths[i] = -1
	}
	for _, entry := range fn.TryTable {
		for _, c := range entry.CatchTargets {
			queue = append(queue, edge{int(c.PC), 0})
		}
		if entry.FinallyPC != bc.NoPC {
			queue = append(queue, edge{int(entry.FinallyPC), 0})
		}
	}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		if cur.pc < 0 || cur.pc >= n || cur.depth < 0 {
			return fmt.Errorf("compiler: pile ou cible invalide a %d", cur.pc)
		}
		if d := depths[cur.pc]; d >= 0 {
			if d != cur.depth {
				return fmt.Errorf("compiler: pile desequilibree a %d: %d/%d", cur.pc, d, cur.depth)
			}
			continue
		}
		depths[cur.pc] = cur.depth
		fn.MaxStack = max(fn.MaxStack, cur.depth)
		in := fn.Code[cur.pc]
		d := cur.depth
		switch in.Op {
		case bc.OpEval, bc.OpInclude, bc.OpBindGlobal, bc.OpAssignStaticProp:
		case bc.OpFetchStaticPropR:
			d++
		case bc.OpGlobalGet, bc.OpGlobalIsset, bc.OpYieldFrom:
		case bc.OpYield:
			if in.Flags&bc.FlagHasKey != 0 {
				d--
			}
		case bc.OpGlobalSet, bc.OpGlobalUnset:
			d--
		case bc.OpStaticCheck:
			queue = append(queue, edge{int(in.Op2), d})
		case bc.OpBindStatic:
			d--
		case bc.OpReturn, bc.OpExit, bc.OpThrow, bc.OpLeaveFinally:
			continue
		case bc.OpJmp:
			queue = append(queue, edge{int(in.Op1), d})
			continue
		case bc.OpJmpZ, bc.OpJmpNZ:
			d--
			queue = append(queue, edge{int(in.Op1), d})
		case bc.OpJmpNull:
			queue = append(queue, edge{int(in.Op1), d - 1})
		case bc.OpMatch:
			for _, c := range fn.Tables[in.Op1].Cases {
				queue = append(queue, edge{int(c.Target), d - 1})
			}
			if target := fn.Tables[in.Op1].Default; target != bc.NoPC {
				queue = append(queue, edge{int(target), d - 1})
			}
			continue
		case bc.OpFeResetR, bc.OpFeFetchR:
			queue = append(queue, edge{int(in.Op1), d - 1})
		case bc.OpFeResetRW:
			queue = append(queue, edge{int(in.Result), d})
		case bc.OpFeFetchRW:
			queue = append(queue, edge{int(in.Op1), d})
		case bc.OpConst, bc.OpLoadCV, bc.OpInitArray, bc.OpDup, bc.OpIssetCV, bc.OpEmptyCV, bc.OpDoCall, bc.OpFetchClassConst:
			d++
		case bc.OpPreIncCV, bc.OpPostIncCV, bc.OpPreDecCV, bc.OpPostDecCV:
			if in.Flags&bc.FlagDiscard == 0 {
				d++
			}
		case bc.OpPop, bc.OpEcho, bc.OpStoreCV, bc.OpSendVal, bc.OpSendRef, bc.OpSendNamed, bc.OpSendUnpack, bc.OpInitMethodCall:
			d--
		case bc.OpAdd, bc.OpSub, bc.OpMul, bc.OpDiv, bc.OpMod, bc.OpPow, bc.OpSL, bc.OpSR, bc.OpConcat, bc.OpBWOr, bc.OpBWAnd, bc.OpBWXor, bc.OpBoolXor, bc.OpIsIdentical, bc.OpIsNotIdentical, bc.OpIsEqual, bc.OpIsNotEqual, bc.OpIsSmaller, bc.OpIsSmallerOrEqual, bc.OpSpaceship, bc.OpFetchDimR, bc.OpFetchDimW, bc.OpFetchDimIs, bc.OpIssetDim, bc.OpEmptyDim, bc.OpUnsetDim:
			d--
		case bc.OpUnsetProp:
			d--
		case bc.OpAssignProp, bc.OpAssignPropOp:
			d--
			if in.Flags&bc.FlagDiscard != 0 {
				d--
			}
		case bc.OpAppendArrayElement:
			if in.Flags&bc.FlagAssignDimCV != 0 {
				if in.Flags&bc.FlagDiscard != 0 {
					d--
				}
			} else {
				d--
				if in.Flags&bc.FlagDiscard != 0 {
					d--
				}
			}
		case bc.OpAddArrayElement:
			d -= 2
		case bc.OpAssignDim, bc.OpAssignDimOp:
			if in.Flags&bc.FlagAssignDimCV != 0 {
				d--
				if in.Flags&bc.FlagDiscard != 0 {
					d--
				}
			} else {
				d -= 2
				if in.Flags&bc.FlagDiscard != 0 {
					d--
				}
			}
		case bc.OpBindClosure:
			d += 1 - int(in.Op2)
		case bc.OpInitCall:
			if in.Flags&bc.FlagClosure != 0 {
				d--
			}
		case bc.OpNop, bc.OpAssignCV, bc.OpAssignRefCV, bc.OpUnsetCV, bc.OpPrint, bc.OpCast, bc.OpBoolNot, bc.OpBWNot, bc.OpUnaryPlus, bc.OpUnaryMinus, bc.OpClone, bc.OpInitNew, bc.OpEnterCatch, bc.OpEnterFinally, bc.OpFeFree, bc.OpInitStaticMethodCall, bc.OpFetchPropR, bc.OpFetchPropW, bc.OpIssetProp, bc.OpEmptyProp, bc.OpInstanceof:
		default:
			return fmt.Errorf("compiler: effet de pile absent pour %s", in.Op)
		}
		queue = append(queue, edge{cur.pc + 1, d})
	}
	return nil
}
