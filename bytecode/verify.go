package bytecode

import (
	"fmt"
	"math"
)

type VerifyError struct {
	PC  int
	Msg string
}

func (e *VerifyError) Error() string {
	if e.PC < 0 {
		return "bytecode: " + e.Msg
	}
	return fmt.Sprintf("bytecode: pc %d: %s", e.PC, e.Msg)
}

type edge struct {
	pc    int
	depth int
}

func Verify(fn *OpArray) error {
	if fn == nil {
		return &VerifyError{PC: -1, Msg: "fonction absente"}
	}
	if fn.NumCVs < 0 || fn.MaxStack < 0 {
		return &VerifyError{PC: -1, Msg: "dimension de cadre négative"}
	}
	if fn.NumCVs > math.MaxInt32 {
		return &VerifyError{PC: -1, Msg: "nombre de variables hors borne"}
	}
	n := len(fn.Code)
	for pc, in := range fn.Code {
		if err := checkOperands(fn, pc, in); err != nil {
			return err
		}
	}
	for i, e := range fn.TryTable {
		if e.StartPC > e.EndPC || int(e.EndPC) > n {
			return &VerifyError{PC: -1, Msg: fmt.Sprintf("intervalle try %d hors code", i)}
		}
		for _, c := range e.CatchTargets {
			if int(c.PC) >= n {
				return &VerifyError{PC: -1, Msg: "cible catch hors code"}
			}
			if c.CV >= 0 && int(c.CV) >= fn.NumCVs {
				return &VerifyError{PC: -1, Msg: "variable catch hors cadre"}
			}
		}
		if e.FinallyPC != NoPC && int(e.FinallyPC) >= n {
			return &VerifyError{PC: -1, Msg: "cible finally hors code"}
		}
		if e.FinallyEndPC != NoPC && int(e.FinallyEndPC) > n {
			return &VerifyError{PC: -1, Msg: "fin de finally hors code"}
		}
	}
	if n == 0 {
		return &VerifyError{PC: 0, Msg: "chemin sans terminaison"}
	}
	depthAt := make([]int, n)
	for i := range depthAt {
		depthAt[i] = -1
	}
	queue := make([]edge, 1, max(n, 8))
	queue[0] = edge{0, 0}
	var edgeBuf [8]edge
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		if cur.pc < 0 || cur.pc >= n {
			return &VerifyError{PC: cur.pc, Msg: "cible de saut hors code"}
		}
		if cur.depth < 0 {
			return &VerifyError{PC: cur.pc, Msg: "sous-dépassement de pile"}
		}
		if cur.depth > fn.MaxStack {
			return &VerifyError{PC: cur.pc, Msg: "dépassement de MaxStack"}
		}
		if depthAt[cur.pc] >= 0 {
			if depthAt[cur.pc] != cur.depth {
				return &VerifyError{PC: cur.pc, Msg: "déséquilibre de pile"}
			}
			continue
		}
		depthAt[cur.pc] = cur.depth
		edges, term, err := successors(fn, cur.pc, cur.depth, edgeBuf[:0])
		if err != nil {
			return err
		}
		if !term && len(edges) == 0 {
			return &VerifyError{PC: cur.pc, Msg: "chemin sans terminaison"}
		}
		for _, e := range edges {
			if e.pc < 0 || e.pc >= n {
				return &VerifyError{PC: cur.pc, Msg: "cible de saut hors code"}
			}
			if e.depth < 0 {
				return &VerifyError{PC: cur.pc, Msg: "sous-dépassement de pile"}
			}
			queue = append(queue, e)
		}
	}
	return nil
}

func checkOperands(fn *OpArray, pc int, in Instr) error {
	cv := func(idx uint32) error {
		if idx == NoCV {
			return nil
		}
		if int(idx) >= fn.NumCVs {
			return &VerifyError{PC: pc, Msg: "indice de variable hors cadre"}
		}
		return nil
	}
	jump := func(target uint32) error {
		if int(target) >= len(fn.Code) {
			return &VerifyError{PC: pc, Msg: "cible de saut hors code"}
		}
		return nil
	}
	switch in.Op {
	case OpYield, OpYieldFrom:
		if !fn.IsGenerator {
			return &VerifyError{PC: pc, Msg: "suspension hors generateur"}
		}
	case OpFetchStaticPropR, OpAssignStaticProp, OpFetchClassConst, OpFetchPropR, OpFetchPropW, OpAssignProp, OpAssignPropOp, OpUnsetProp, OpIssetProp, OpEmptyProp:
		if int(in.Op1) >= len(fn.Strings) {
			return &VerifyError{PC: pc, Msg: "nom de membre hors table"}
		}
		if in.Op == OpAssignPropOp && !validAssignOp(Opcode(in.Op2)) {
			return &VerifyError{PC: pc, Msg: "opérateur d'affectation invalide"}
		}
	case OpStaticCheck:
		if err := cv(in.Op1); err != nil {
			return err
		}
		return jump(in.Op2)
	case OpBindStatic, OpBindGlobal:
		return cv(in.Op1)
	case OpInclude:
		if int(in.Op1) >= len(fn.Strings) {
			return &VerifyError{PC: pc, Msg: "inclusion hors table"}
		}
	case OpLoadCV, OpStoreCV, OpAssignCV, OpUnsetCV, OpIssetCV, OpEmptyCV, OpPreIncCV, OpPreDecCV, OpPostIncCV, OpPostDecCV:
		return cv(in.Op1)
	case OpAssignDim, OpAssignDimOp, OpAppendArrayElement:
		if in.Flags&FlagAssignDimCV != 0 {
			if err := cv(in.Op1); err != nil {
				return err
			}
		}
		if in.Op == OpAssignDimOp {
			op := Opcode(in.Op1)
			if in.Flags&FlagAssignDimCV != 0 {
				op = Opcode(in.Op2)
			}
			if !validAssignOp(op) {
				return &VerifyError{PC: pc, Msg: "opérateur d'affectation invalide"}
			}
		}
	case OpAssignRefCV:
		if err := cv(in.Op1); err != nil {
			return err
		}
		return cv(in.Op2)
	case OpConst:
		if in.Flags&FlagStringConst != 0 || in.Flags&FlagNamedConst != 0 {
			if int(in.Op1) >= len(fn.Strings) {
				return &VerifyError{PC: pc, Msg: "constante chaîne hors table"}
			}
			return nil
		}
		if int(in.Op1) >= len(fn.Consts) {
			return &VerifyError{PC: pc, Msg: "constante hors table"}
		}
	case OpJmp, OpJmpZ, OpJmpNZ, OpJmpNull:
		return jump(in.Op1)
	case OpSwitchLong, OpSwitchString, OpMatch:
		if int(in.Op1) >= len(fn.Tables) {
			return &VerifyError{PC: pc, Msg: "table de saut hors borne"}
		}
		table := fn.Tables[in.Op1]
		for _, c := range table.Cases {
			if err := jump(c.Target); err != nil {
				return err
			}
		}
		if table.Default != NoPC {
			if err := jump(table.Default); err != nil {
				return err
			}
		}
	case OpFeResetR, OpFeFetchR:
		if err := jump(in.Op1); err != nil {
			return err
		}
		if in.Flags&FlagHasKey != 0 {
			if pc >= len(fn.Aux) {
				return &VerifyError{PC: pc, Msg: "opérande auxiliaire absent"}
			}
			if err := cv(fn.Aux[pc]); err != nil {
				return err
			}
		}
		if in.Op == OpFeFetchR {
			if err := cv(in.Result); err != nil {
				return err
			}
		}
	case OpFeResetRW:
		if err := cv(in.Op2); err != nil {
			return err
		}
		return jump(in.Result)
	case OpFeFetchRW:
		if err := jump(in.Op1); err != nil {
			return err
		}
		if err := cv(in.Result); err != nil {
			return err
		}
		if in.Flags&FlagHasKey != 0 {
			if pc >= len(fn.Aux) {
				return &VerifyError{PC: pc, Msg: "opérande auxiliaire absent"}
			}
			return cv(fn.Aux[pc])
		}
	case OpEnterCatch:
		return cv(in.Op1)
	case OpFastCallFinally:
		if err := jump(in.Op1); err != nil {
			return err
		}
		return jump(in.Op2)
	case OpInitCall:
		if in.Flags&FlagNative != 0 && int(in.Op1) >= len(fn.Strings) {
			return &VerifyError{PC: pc, Msg: "fonction native hors table"}
		}
		if in.Flags&FlagClosure == 0 && int(in.Op1) < 0 {
			return &VerifyError{PC: pc, Msg: "fonction hors programme"}
		}
	}
	return nil
}

func successors(fn *OpArray, pc, depth int, dst []edge) ([]edge, bool, error) {
	in := fn.Code[pc]
	fall := func(delta int) ([]edge, bool, error) {
		if depth+delta < 0 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return append(dst, edge{pc + 1, depth + delta}), false, nil
	}
	jumpOnly := func(target int, next int) ([]edge, bool, error) {
		if next < 0 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		edges := append(dst, edge{target, next})
		return redirect(fn, pc, edges), false, nil
	}
	switch in.Op {
	case OpStaticCheck:
		return append(dst, edge{pc + 1, depth}, edge{int(in.Op2), depth}), false, nil
	case OpBindStatic:
		return fall(-1)
	case OpEval, OpInclude, OpGlobalGet, OpGlobalIsset, OpYieldFrom:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-depassement de pile"}
		}
		return fall(0)
	case OpYield:
		need := 1
		if in.Flags&FlagHasKey != 0 {
			need++
		}
		if !fn.IsGenerator || depth < need {
			return nil, false, &VerifyError{PC: pc, Msg: "yield hors generateur ou pile insuffisante"}
		}
		return fall(1 - need)
	case OpNop, OpBindGlobal, OpEnterCatch, OpEnterFinally, OpIssetCV, OpEmptyCV, OpUnsetCV, OpAssignRefCV:
		delta := 0
		if in.Op == OpIssetCV || in.Op == OpEmptyCV {
			delta = 1
		}
		return fall(delta)
	case OpConst, OpLoadCV, OpInitArray, OpFetchStaticPropR, OpFetchClassConst:
		return fall(1)
	case OpPreIncCV, OpPreDecCV, OpPostIncCV, OpPostDecCV:
		if in.Flags&FlagDiscard != 0 {
			return fall(0)
		}
		return fall(1)
	case OpPop, OpEcho, OpStoreCV, OpUnsetProp, OpSendVal, OpSendRef, OpSendNamed, OpSendUnpack, OpGlobalUnset:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return fall(-1)
	case OpGlobalSet:
		if depth < 2 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-depassement de pile"}
		}
		return fall(-1)
	case OpUnsetDim:
		if depth < 2 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return fall(-1)
	case OpFeFree:
		return fall(0)
	case OpInitMethodCall:
		if in.Flags&FlagUseThis != 0 {
			return fall(0)
		}
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return fall(-1)
	case OpDup:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return fall(1)
	case OpPrint, OpAssignCV, OpMakeRef, OpDeref, OpCast, OpBWNot, OpBoolNot, OpUnaryPlus, OpUnaryMinus,
		OpFetchPropR, OpFetchPropW, OpIssetProp, OpEmptyProp, OpInstanceof, OpClone, OpAssignStaticProp:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return fall(0)
	case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpPow, OpSL, OpSR, OpConcat, OpBWOr, OpBWAnd, OpBWXor, OpBoolXor,
		OpIsIdentical, OpIsNotIdentical, OpIsEqual, OpIsNotEqual, OpIsSmaller, OpIsSmallerOrEqual, OpSpaceship,
		OpFetchDimR, OpFetchDimW, OpFetchDimIs, OpIssetDim, OpEmptyDim:
		if depth < 2 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return fall(-1)
	case OpAssignProp, OpAssignPropOp:
		if depth < 2 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		if in.Flags&FlagDiscard != 0 {
			return fall(-2)
		}
		return fall(-1)
	case OpAppendArrayElement:
		need := 2
		if in.Flags&FlagAssignDimCV != 0 {
			need = 1
		}
		if depth < need {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		pushed := 1
		if in.Flags&FlagDiscard != 0 {
			pushed = 0
		}
		return fall(pushed - need)
	case OpAddArrayElement:
		if depth < 3 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return fall(-2)
	case OpAssignDim, OpAssignDimOp:
		need := 3
		if in.Flags&FlagAssignDimCV != 0 {
			need = 2
		}
		if depth < need {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		pushed := 1
		if in.Flags&FlagDiscard != 0 {
			pushed = 0
		}
		return fall(pushed - need)
	case OpBindClosure:
		if depth < int(in.Op2) {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return fall(1 - int(in.Op2))
	case OpInitCall, OpInitStaticMethodCall, OpInitNew:
		if in.Op == OpInitCall && in.Flags&FlagClosure != 0 {
			if depth < 1 {
				return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
			}
			return fall(-1)
		}
		return fall(0)
	case OpDoCall:
		return fall(1)
	case OpJmp:
		return jumpOnly(int(in.Op1), depth)
	case OpJmpZ, OpJmpNZ:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		next := depth - 1
		edges := append(dst, edge{pc + 1, next}, edge{int(in.Op1), next})
		return redirect(fn, pc, edges), false, nil
	case OpJmpNull:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		edges := append(dst, edge{pc + 1, depth}, edge{int(in.Op1), depth - 1})
		return redirect(fn, pc, edges), false, nil
	case OpSwitchLong, OpSwitchString, OpMatch:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		table := fn.Tables[in.Op1]
		next := depth - 1
		edges := dst
		for _, c := range table.Cases {
			edges = append(edges, edge{int(c.Target), next})
		}
		if table.Default != NoPC {
			edges = append(edges, edge{int(table.Default), next})
			return redirect(fn, pc, edges), false, nil
		}
		if in.Op == OpMatch {
			return redirect(fn, pc, edges), true, nil
		}
		edges = append(edges, edge{pc + 1, next})
		return redirect(fn, pc, edges), false, nil
	case OpReturn:
		pop := 1
		if in.Flags&FlagReturnVoid != 0 {
			pop = 0
		}
		if depth < pop {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		if fpc, ok := innermostFinally(fn, pc); ok {
			return append(dst, edge{fpc, depth - pop}), false, nil
		}
		return nil, true, nil
	case OpExit:
		pop := 1
		if in.Flags&FlagExitVoid != 0 {
			pop = 0
		}
		if depth < pop {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		if fpc, ok := innermostFinally(fn, pc); ok {
			return append(dst, edge{fpc, depth - pop}), false, nil
		}
		return nil, true, nil
	case OpThrow:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		edges := dst
		if e, ok := enclosingTry(fn, pc); ok {
			for _, c := range e.CatchTargets {
				edges = append(edges, edge{int(c.PC), depth - 1})
			}
			if e.FinallyPC != NoPC {
				edges = append(edges, edge{int(e.FinallyPC), depth - 1})
			}
		}
		if len(edges) == 0 {
			return nil, true, nil
		}
		return edges, true, nil
	case OpFeResetR:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return append(dst, edge{pc + 1, depth}, edge{int(in.Op1), depth - 1}), false, nil
	case OpFeResetRW:
		return append(dst, edge{pc + 1, depth}, edge{int(in.Result), depth}), false, nil
	case OpFeFetchR:
		if depth < 1 {
			return nil, false, &VerifyError{PC: pc, Msg: "sous-dépassement de pile"}
		}
		return append(dst, edge{pc + 1, depth}, edge{int(in.Op1), depth - 1}), false, nil
	case OpFeFetchRW:
		return append(dst, edge{pc + 1, depth}, edge{int(in.Op1), depth}), false, nil
	case OpLeaveFinally, OpFastRetFinally:
		return nil, true, nil
	case OpFastCallFinally:
		return append(dst, edge{int(in.Op1), depth}, edge{int(in.Op2), depth}), false, nil
	default:
		return nil, false, &VerifyError{PC: pc, Msg: "opcode non vérifié: " + in.Op.String()}
	}
}

func redirect(fn *OpArray, pc int, edges []edge) []edge {
	if len(fn.TryTable) == 0 {
		return edges
	}
	fpc, ok := innermostFinally(fn, pc)
	if !ok {
		return edges
	}
	e := mustEnclosing(fn, pc)
	for i, ed := range edges {
		if crossesFinally(e, ed.pc) && ed.pc != fpc {
			edges[i] = edge{fpc, ed.depth}
		}
	}
	return edges
}

func crossesFinally(e TryCatchEntry, target int) bool {
	if e.FinallyPC == NoPC {
		return false
	}
	if target >= int(e.FinallyPC) && (e.FinallyEndPC == NoPC || target < int(e.FinallyEndPC)) {
		return false
	}
	hi := max(int(e.FinallyPC), int(e.EndPC))
	return target < int(e.StartPC) || target >= hi
}

func mustEnclosing(fn *OpArray, pc int) TryCatchEntry {
	e, _ := enclosingTry(fn, pc)
	return e
}

func enclosingTry(fn *OpArray, pc int) (TryCatchEntry, bool) {
	best := -1
	bestSpan := int(^uint(0) >> 1)
	for i, e := range fn.TryTable {
		if int(e.StartPC) <= pc && pc < int(e.EndPC) {
			span := int(e.EndPC) - int(e.StartPC)
			if span < bestSpan {
				best = i
				bestSpan = span
			}
		}
	}
	if best < 0 {
		return TryCatchEntry{}, false
	}
	return fn.TryTable[best], true
}

func innermostFinally(fn *OpArray, pc int) (int, bool) {
	best := -1
	bestSpan := int(^uint(0) >> 1)
	for _, e := range fn.TryTable {
		if e.FinallyPC == NoPC {
			continue
		}
		hi := max(int(e.FinallyPC), int(e.EndPC))
		if int(e.StartPC) <= pc && pc < hi {
			span := hi - int(e.StartPC)
			if span < bestSpan {
				best = int(e.FinallyPC)
				bestSpan = span
			}
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

func validAssignOp(op Opcode) bool {
	return op == OpAdd || op == OpSub || op == OpMul || op == OpConcat
}
