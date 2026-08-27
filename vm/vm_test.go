// SPDX-License-Identifier: Apache-2.0 OR MIT

package vm

import (
	"bytes"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func TestVMArithmeticAndEcho(t *testing.T) {
	var out bytes.Buffer
	vm := NewVM(&out)

	// PHP: $a = 10; $b = 20; echo $a + $b;
	prog := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewLong(10)},
			{Op: OpStoreVar, A: 0}, // $a = 10
			{Op: OpLoadConst, Const: types.NewLong(20)},
			{Op: OpStoreVar, A: 1}, // $b = 20
			{Op: OpLoadVar, A: 0},
			{Op: OpLoadVar, A: 1},
			{Op: OpAdd},
			{Op: OpEcho},
			{Op: OpReturn},
		},
	}

	_, err := vm.Execute(prog)
	if err != nil {
		t.Fatalf("vm execute failed: %v", err)
	}

	if out.String() != "30" {
		t.Fatalf("echo attendu \"30\", got %q", out.String())
	}
}

func TestVMLoopCounter(t *testing.T) {
	vm := NewVM(nil)

	// PHP: $i = 0; $sum = 0; while ($i < 10) { $sum += $i; $i++; } return $sum;
	// Instructions:
	// 0: $i = 0
	// 1: $sum = 0
	// 2: Loop condition: $i == 10 ?
	//    $diff = 10 - $i; if ($diff <= 0) jmp 11
	// 3: $sum = $sum + $i
	// 4: $i = $i + 1
	// 5: jmp 2
	// 6: return $sum
	prog := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewLong(0)},
			{Op: OpStoreVar, A: 0}, // $i = 0
			{Op: OpLoadConst, Const: types.NewLong(0)},
			{Op: OpStoreVar, A: 1}, // $sum = 0

			// 4: loop header: check if 10 - $i == 0
			{Op: OpLoadConst, Const: types.NewLong(10)}, // 4
			{Op: OpLoadVar, A: 0},                       // 5
			{Op: OpSub},                                 // 6
			{Op: OpJmpZ, A: 17},                         // 7: if 10 - $i == 0 jump to end (17)

			// 8: loop body ($sum = $sum + $i)
			{Op: OpLoadVar, A: 1},  // 8
			{Op: OpLoadVar, A: 0},  // 9
			{Op: OpAdd},            // 10
			{Op: OpStoreVar, A: 1}, // 11

			// 12: $i = $i + 1
			{Op: OpLoadVar, A: 0},                      // 12
			{Op: OpLoadConst, Const: types.NewLong(1)}, // 13
			{Op: OpAdd},            // 14
			{Op: OpStoreVar, A: 0}, // 15

			{Op: OpJmp, A: 4}, // 16: goto loop header (4)

			// 17: return $sum
			{Op: OpLoadVar, A: 1}, // 17
			{Op: OpReturn},        // 18
		},
	}

	res, err := vm.Execute(prog)
	if err != nil {
		t.Fatalf("vm loop failed: %v", err)
	}

	// 0+1+2+3+4+5+6+7+8+9 = 45
	if res.ToLong() != 45 {
		t.Fatalf("somme boucle attendue 45, got %d", res.ToLong())
	}
}

func TestVMSwissTableAndPCRE(t *testing.T) {
	vm := NewVM(nil)

	// PHP: $arr = []; $arr[0] = "token_42"; return preg_match('/\\d+/', $arr[0]);
	prog := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpArrayInit},
			{Op: OpStoreVar, A: 0}, // $arr

			// $arr[0] = "token_42"
			{Op: OpLoadVar, A: 0},
			{Op: OpLoadConst, Const: types.NewLong(0)},
			{Op: OpLoadConst, Const: types.NewString("token_42")},
			{Op: OpArraySet},

			// preg_match('/\\d+/', $arr[0])
			{Op: OpLoadConst, Const: types.NewString("\\d+")},
			{Op: OpLoadVar, A: 0},
			{Op: OpLoadConst, Const: types.NewLong(0)},
			{Op: OpArrayGet},
			{Op: OpPregMatch},
			{Op: OpReturn},
		},
	}

	res, err := vm.Execute(prog)
	if err != nil {
		t.Fatalf("vm swiss/pcre failed: %v", err)
	}

	if res.ToLong() != 1 {
		t.Fatalf("preg_match attendu 1, got %d", res.ToLong())
	}
}

func TestVMZeroAllocExecution(t *testing.T) {
	vm := NewVM(nil)
	prog := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewLong(100)},
			{Op: OpLoadConst, Const: types.NewLong(200)},
			{Op: OpAdd},
			{Op: OpLoadConst, Const: types.NewLong(50)},
			{Op: OpSub},
			{Op: OpReturn},
		},
	}

	allocs := testing.AllocsPerRun(1000, func() {
		vm.Reset()
		_, _ = vm.Execute(prog)
	})
	if allocs != 0 {
		t.Fatalf("VM Allocs/op = %.2f, attendu 0", allocs)
	}
}

func TestVMBlueteamAndSecurityGuard(t *testing.T) {
	vm := NewVM(nil)

	// Test blueteam_entropy opcode
	progEntropy := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewString("The quick brown fox jumps over the lazy dog.")},
			{Op: OpBlueteamEntropy},
			{Op: OpReturn},
		},
	}
	resEntropy, err := vm.Execute(progEntropy)
	if err != nil {
		t.Fatalf("entropy opcode failed: %v", err)
	}
	f := resEntropy.ToDouble()
	if f < 3.0 || f > 5.0 {
		t.Fatalf("Entropie inattendue: %f", f)
	}

	// Test OpSecurityCheck : Rejet d'une commande système malveillante
	vm.Reset()
	progSecViolation := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewString("exec")},
			{Op: OpLoadConst, Const: types.NewString("rm -rf /; bash -i").WithTaint(types.FlagTaintedUserInput)},
			{Op: OpSecurityCheck},
			{Op: OpReturn},
		},
	}
	_, errSec := vm.Execute(progSecViolation)
	if errSec == nil {
		t.Fatalf("OpSecurityCheck aurait dû interdire l'exécution de bash injecté")
	}
}

func TestVMComparisonsAndBitwise(t *testing.T) {
	vm := NewVM(nil)

	// Test Comparaisons: 10 < 20, 10 == 10, 10 <=> 20
	prog := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewLong(10)},
			{Op: OpLoadConst, Const: types.NewLong(20)},
			{Op: OpLt}, // 10 < 20 -> true

			{Op: OpLoadConst, Const: types.NewLong(10)},
			{Op: OpLoadConst, Const: types.NewLong(10)},
			{Op: OpEq}, // 10 == 10 -> true

			{Op: OpLoadConst, Const: types.NewLong(10)},
			{Op: OpLoadConst, Const: types.NewLong(20)},
			{Op: OpSpaceship}, // 10 <=> 20 -> -1

			{Op: OpLoadConst, Const: types.NewLong(0b1100)},
			{Op: OpLoadConst, Const: types.NewLong(0b1010)},
			{Op: OpBitAnd}, // 0b1000 = 8

			{Op: OpReturn},
		},
	}

	resBitAnd, err := vm.Execute(prog)
	if err != nil {
		t.Fatalf("VM comparison & bitwise failed: %v", err)
	}
	if resBitAnd.ToLong() != 8 {
		t.Fatalf("BitAnd result got %d, want 8", resBitAnd.ToLong())
	}
	resSpaceship := vm.Pop()
	if resSpaceship.ToLong() != -1 {
		t.Fatalf("Spaceship result got %d, want -1", resSpaceship.ToLong())
	}
	resEq := vm.Pop()
	if !resEq.ToBool() {
		t.Fatalf("Eq result got false, want true")
	}
	resLt := vm.Pop()
	if !resLt.ToBool() {
		t.Fatalf("Lt result got false, want true")
	}
}

func TestVMFunctionCallFrame(t *testing.T) {
	vm := NewVM(nil)

	// Function #0: add3($x, $y, $z) { return $x + $y + $z; }
	funcAdd3 := BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpGetArg, A: 0}, // $x
			{Op: OpGetArg, A: 1}, // $y
			{Op: OpAdd},
			{Op: OpGetArg, A: 2}, // $z
			{Op: OpAdd},
			{Op: OpRetFrame},
		},
	}

	// Main program: add3(10, 20, 30) -> return 60
	prog := &BytecodeProgram{
		Functions: []BytecodeProgram{funcAdd3},
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewLong(10)},
			{Op: OpLoadConst, Const: types.NewLong(20)},
			{Op: OpLoadConst, Const: types.NewLong(30)},
			{Op: OpCall, A: 0, B: 3}, // call func#0 with 3 args
			{Op: OpReturn},
		},
	}

	res, err := vm.Execute(prog)
	if err != nil {
		t.Fatalf("VM function call failed: %v", err)
	}
	if res.ToLong() != 60 {
		t.Fatalf("add3(10, 20, 30) got %d, want 60", res.ToLong())
	}
}

func TestVMForeachIteration(t *testing.T) {
	vm := NewVM(nil)

	// PHP: $arr = [10, 20, 30]; $sum = 0; foreach ($arr as $k => $v) { $sum += $v; } return $sum;
	prog := &BytecodeProgram{
		Instructions: []Instruction{
			// 0: init array
			{Op: OpArrayInit},
			{Op: OpStoreVar, A: 0}, // $arr

			// 2: $arr[0] = 10
			{Op: OpLoadVar, A: 0},
			{Op: OpLoadConst, Const: types.NewLong(0)},
			{Op: OpLoadConst, Const: types.NewLong(10)},
			{Op: OpArraySet},

			// 6: $arr[1] = 20
			{Op: OpLoadVar, A: 0},
			{Op: OpLoadConst, Const: types.NewLong(1)},
			{Op: OpLoadConst, Const: types.NewLong(20)},
			{Op: OpArraySet},

			// 10: $arr[2] = 30
			{Op: OpLoadVar, A: 0},
			{Op: OpLoadConst, Const: types.NewLong(2)},
			{Op: OpLoadConst, Const: types.NewLong(30)},
			{Op: OpArraySet},

			// 14: $sum = 0
			{Op: OpLoadConst, Const: types.NewLong(0)},
			{Op: OpStoreVar, A: 1}, // $sum

			// 16: Foreach setup: Push array, IterInit
			{Op: OpLoadVar, A: 0},
			{Op: OpArrayIterInit},

			// 18: IterNext (jump to 26 when done)
			{Op: OpArrayIterNext, A: 26},

			// 19: Loop body: Stack has [..., key, val]
			{Op: OpStoreVar, A: 3}, // $v = val
			{Op: OpStoreVar, A: 2}, // $k = key

			// 21: $sum += $v
			{Op: OpLoadVar, A: 1},
			{Op: OpLoadVar, A: 3},
			{Op: OpAdd},
			{Op: OpStoreVar, A: 1},

			// 25: goto IterNext (18)
			{Op: OpJmp, A: 18},

			// 26: return $sum
			{Op: OpLoadVar, A: 1},
			{Op: OpReturn},
		},
	}

	res, err := vm.Execute(prog)
	if err != nil {
		t.Fatalf("VM foreach failed: %v", err)
	}
	if res.ToLong() != 60 {
		t.Fatalf("foreach sum got %d, want 60 (10+20+30)", res.ToLong())
	}
}

func TestVMSubstrAndStrings(t *testing.T) {
	vm := NewVM(nil)

	// PHP: $s = "Flatstack127"; return substr($s, 4, 5); -> "stack"
	prog := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewString("Flatstack127")},
			{Op: OpLoadConst, Const: types.NewLong(4)},
			{Op: OpLoadConst, Const: types.NewLong(5)},
			{Op: OpSubstr},
			{Op: OpReturn},
		},
	}

	res, err := vm.Execute(prog)
	if err != nil {
		t.Fatalf("VM substr failed: %v", err)
	}
	if res.String() != "stack" {
		t.Fatalf("substr(\"Flatstack127\", 4, 5) got %q, want \"stack\"", res.String())
	}
}

func TestVMZeroAllocOnLoopsAndCalls(t *testing.T) {
	vm := NewVM(nil)

	funcAdd := BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpGetArg, A: 0},
			{Op: OpGetArg, A: 1},
			{Op: OpAdd},
			{Op: OpRetFrame},
		},
	}

	prog := &BytecodeProgram{
		Functions: []BytecodeProgram{funcAdd},
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewLong(0)},
			{Op: OpStoreVar, A: 0}, // $i = 0

			// 2: header (100 - $i)
			{Op: OpLoadConst, Const: types.NewLong(100)},
			{Op: OpLoadVar, A: 0},
			{Op: OpSub},
			{Op: OpJmpZ, A: 14},

			// 6: call funcAdd($i, 1)
			{Op: OpLoadVar, A: 0},
			{Op: OpLoadConst, Const: types.NewLong(1)},
			{Op: OpCall, A: 0, B: 2},
			{Op: OpStoreVar, A: 0},

			// 12: loop
			{Op: OpJmp, A: 2},

			// 14: return $i
			{Op: OpLoadVar, A: 0},
			{Op: OpReturn},
		},
	}

	allocs := testing.AllocsPerRun(1000, func() {
		vm.Reset()
		_, _ = vm.Execute(prog)
	})
	if allocs != 0 {
		t.Fatalf("VM Loop+Call Allocs/op = %.2f, invariant dur attendu = 0", allocs)
	}
}

func TestVMTaintPreservationInSwissTables(t *testing.T) {
	vm := NewVM(nil)

	// $arr[0] = $taintedString
	taintedStr := types.NewString("tainted_payload").WithTaint(types.FlagTaintedUserInput)
	prog := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpArrayInit},
			{Op: OpStoreVar, A: 0},
			{Op: OpLoadVar, A: 0},
			{Op: OpLoadConst, Const: types.NewLong(0)},
			{Op: OpLoadConst, Const: taintedStr},
			{Op: OpArraySet},

			// Read back
			{Op: OpLoadVar, A: 0},
			{Op: OpLoadConst, Const: types.NewLong(0)},
			{Op: OpArrayGet},
			{Op: OpReturn},
		},
	}

	res, err := vm.Execute(prog)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.IsTainted() {
		t.Fatalf("Retrieved array value should preserve FlagTaintedUserInput, got flags %d", res.Flags)
	}
	if res.String() != "tainted_payload" {
		t.Fatalf("Retrieved array value string got %q, want %q", res.String(), "tainted_payload")
	}
}

func TestVMSequentialConcatZeroAlloc(t *testing.T) {
	short := &BytecodeProgram{
		Instructions: []Instruction{
			{Op: OpLoadConst, Const: types.NewString("hello ")},
			{Op: OpLoadConst, Const: types.NewString("world")},
			{Op: OpConcat},
			{Op: OpReturn},
		},
	}
	vm := NewVM(nil)
	allocs := testing.AllocsPerRun(1000, func() {
		vm.Reset()
		_, _ = vm.Execute(short)
	})
	if allocs != 0 {
		t.Fatalf("VM Concat AllocsPerRun = %.2f, want 0", allocs)
	}
}
