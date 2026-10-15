package bytecode

import (
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func insn(op Opcode, op1, op2, result, line uint32) Instr {
	return Instr{Op: op, Op1: op1, Op2: op2, Result: result, Line: line}
}

func fnWith(code []Instr, ncv, stack int) *OpArray {
	return &OpArray{Name: "f", Filename: "t.php", NumCVs: ncv, MaxStack: stack, Code: code, Consts: []types.Value{types.NewLong(0), types.NewLong(1)}}
}

func TestVerifyGeneratorAndStaticOperands(t *testing.T) {
	for _, op := range []Opcode{OpYield, OpYieldFrom} {
		t.Run(op.String(), func(t *testing.T) {
			fn := fnWith([]Instr{{Op: OpConst}, {Op: op}, {Op: OpReturn}}, 0, 1)
			if err := Verify(fn); err == nil {
				t.Fatal("suspension hors generateur acceptee")
			}
			fn.IsGenerator = true
			if err := Verify(fn); err != nil {
				t.Fatal(err)
			}
		})
	}
	fn := fnWith([]Instr{{Op: OpFetchStaticPropR}, {Op: OpReturn}}, 0, 1)
	if err := Verify(fn); err == nil {
		t.Fatal("nom statique hors table accepte")
	}
	fn.Strings = []string{"x"}
	if err := Verify(fn); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAcceptsBalancedPrograms(t *testing.T) {
	loop := fnWith([]Instr{
		insn(OpJmp, 2, 0, 0, 1),
		insn(OpNop, 0, 0, 0, 1),
		insn(OpConst, 0, 0, 0, 1),
		insn(OpJmpNZ, 1, 0, 0, 1),
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 0, 1)
	if err := Verify(loop); err != nil {
		t.Fatalf("boucle: %v", err)
	}
	branch := fnWith([]Instr{
		insn(OpConst, 0, 0, 0, 1),
		insn(OpJmpZ, 4, 0, 0, 1),
		insn(OpConst, 1, 0, 0, 1),
		insn(OpJmp, 5, 0, 0, 1),
		insn(OpConst, 1, 0, 0, 1),
		insn(OpPop, 0, 0, 0, 1),
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 0, 1)
	if err := Verify(branch); err != nil {
		t.Fatalf("alternative: %v", err)
	}
	call := fnWith([]Instr{
		insn(OpInitCall, 0, 0, 0, 1),
		insn(OpDoCall, 0, 0, 0, 1),
		insn(OpPop, 0, 0, 0, 1),
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 0, 1)
	if err := Verify(call); err != nil {
		t.Fatalf("appel: %v", err)
	}
	tried := fnWith([]Instr{
		insn(OpNop, 0, 0, 0, 1),
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
		insn(OpEnterFinally, 0, 0, 0, 1),
		insn(OpLeaveFinally, 0, 0, 0, 1),
	}, 0, 0)
	tried.TryTable = []TryCatchEntry{{StartPC: 0, EndPC: 2, FinallyPC: 2, FinallyEndPC: 4}}
	if err := Verify(tried); err != nil {
		t.Fatalf("finally: %v", err)
	}
	caught := fnWith([]Instr{
		insn(OpConst, 1, 0, 0, 1),
		insn(OpThrow, 0, 0, 0, 1),
		insn(OpEnterCatch, NoCV, 0, 0, 1),
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 0, 1)
	caught.TryTable = []TryCatchEntry{{
		StartPC: 0, EndPC: 2, FinallyPC: NoPC, FinallyEndPC: NoPC,
		CatchTargets: []CatchTarget{{Classes: []uint32{ClassException}, CV: -1, PC: 2}},
	}}
	if err := Verify(caught); err != nil {
		t.Fatalf("catch: %v", err)
	}
}

func TestVerifyRejectsImbalanceAndBounds(t *testing.T) {
	branch := fnWith([]Instr{
		insn(OpConst, 1, 0, 0, 1),
		insn(OpJmpZ, 4, 0, 0, 1),
		insn(OpConst, 1, 0, 0, 1),
		insn(OpJmp, 5, 0, 0, 1),
		insn(OpNop, 0, 0, 0, 1),
		insn(OpPop, 0, 0, 0, 1),
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 0, 2)
	if err := Verify(branch); err == nil {
		t.Fatal("alternative déséquilibrée acceptée")
	}
	loop := fnWith([]Instr{
		insn(OpNop, 0, 0, 0, 1),
		insn(OpConst, 1, 0, 0, 1),
		insn(OpJmp, 0, 0, 0, 1),
	}, 0, 2)
	if err := Verify(loop); err == nil {
		t.Fatal("boucle déséquilibrée acceptée")
	}
	under := fnWith([]Instr{
		insn(OpPop, 0, 0, 0, 1),
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 0, 1)
	if err := Verify(under); err == nil {
		t.Fatal("sous-dépassement OpPop accepté")
	}
	add := fnWith([]Instr{
		insn(OpAdd, 0, 0, 0, 1),
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 0, 1)
	if err := Verify(add); err == nil {
		t.Fatal("sous-dépassement OpAdd accepté")
	}
	cv := fnWith([]Instr{
		insn(OpLoadCV, 5, 0, 0, 1),
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 1, 1)
	if err := Verify(cv); err == nil {
		t.Fatal("indice de variable hors cadre accepté")
	}
	jmp := fnWith([]Instr{
		insn(OpJmp, 9, 0, 0, 1),
	}, 0, 0)
	if err := Verify(jmp); err == nil {
		t.Fatal("saut hors code accepté")
	}
	badFinally := fnWith([]Instr{
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 0, 0)
	badFinally.TryTable = []TryCatchEntry{{StartPC: 0, EndPC: 1, FinallyPC: 8, FinallyEndPC: NoPC}}
	if err := Verify(badFinally); err == nil {
		t.Fatal("finally hors code accepté")
	}
	badAssignDimOp := fnWith([]Instr{
		insn(OpConst, 0, 0, 0, 1),
		insn(OpConst, 1, 0, 0, 1),
		{Op: OpAssignDimOp, Flags: FlagAssignDimCV | FlagDiscard, Op1: 0, Op2: uint32(OpReturn), Line: 1},
		{Op: OpReturn, Flags: FlagReturnVoid, Line: 1},
	}, 1, 2)
	if err := Verify(badAssignDimOp); err == nil {
		t.Fatal("OpAssignDimOp avec opérateur invalide accepté")
	}
}
