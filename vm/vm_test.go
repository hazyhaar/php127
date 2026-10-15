package vm

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type asm struct {
	fn *bytecode.OpArray
}

func newFn(name string, ncv, stack int) *asm {
	return &asm{fn: &bytecode.OpArray{Name: name, NumCVs: ncv, MaxStack: stack}}
}

func (a *asm) emit(op bytecode.Opcode, flags uint16, op1, op2, result, line uint32) int {
	a.fn.Code = append(a.fn.Code, bytecode.Instr{Op: op, Flags: flags, Op1: op1, Op2: op2, Result: result, Line: line})
	return len(a.fn.Code) - 1
}

func (a *asm) long(n int64, line uint32) {
	idx := uint32(len(a.fn.Consts))
	a.fn.Consts = append(a.fn.Consts, types.NewLong(n))
	a.emit(bytecode.OpConst, 0, idx, 0, 0, line)
}

func (a *asm) str(s string, line uint32) {
	idx := uint32(len(a.fn.Strings))
	a.fn.Strings = append(a.fn.Strings, s)
	a.emit(bytecode.OpConst, bytecode.FlagStringConst, idx, 0, 0, line)
}

func (a *asm) ret(line uint32) {
	a.emit(bytecode.OpReturn, bytecode.FlagReturnVoid, 0, 0, 0, line)
}

func (a *asm) patch(at int, val uint32) {
	a.fn.Code[at].Op1 = val
}

func fillNames(prog *bytecode.Program, path string) {
	set := func(fn *bytecode.OpArray) {
		if fn != nil && fn.Filename == "" {
			fn.Filename = path
		}
	}
	set(prog.Main)
	for _, fn := range prog.Funcs {
		set(fn)
	}
	for _, c := range prog.Classes {
		if c == nil {
			continue
		}
		for i := range c.Methods {
			set(c.Methods[i].Func)
		}
	}
	for _, c := range prog.Traits {
		if c == nil {
			continue
		}
		for i := range c.Methods {
			set(c.Methods[i].Func)
		}
	}
}

func parity(t *testing.T, src string, prog *bytecode.Program) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "t.php")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fillNames(prog, path)
	cmd := exec.Command("/usr/bin/php", "-n", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	z := zone.New(zone.DefaultCapacity)
	var gotOut, gotErr bytes.Buffer
	got := Execute(z, &gotOut, &gotErr, prog, NewContext())
	if got != code || gotOut.String() != stdout.String() || gotErr.String() != stderr.String() {
		t.Fatalf("stdout got %q want %q\nstderr got %q want %q\nexit %d want %d", gotOut.String(), stdout.String(), gotErr.String(), stderr.String(), got, code)
	}
}

func TestParityScalarControlAndCalls(t *testing.T) {
	t.Run("arith", func(t *testing.T) {
		m := newFn("main", 0, 4)
		m.long(2, 1)
		m.long(3, 1)
		m.emit(bytecode.OpMul, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.emit(bytecode.OpAdd, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php echo 1+2*3;", &bytecode.Program{Main: m.fn})
	})
	t.Run("assign", func(t *testing.T) {
		m := newFn("main", 1, 4)
		m.long(4, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.emit(bytecode.OpAdd, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php $a=4; $a=$a+1; echo $a;", &bytecode.Program{Main: m.fn})
	})
	t.Run("concat", func(t *testing.T) {
		m := newFn("main", 0, 2)
		m.str("a", 1)
		m.str("b", 1)
		m.emit(bytecode.OpConcat, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, `<?php echo "a"."b";`, &bytecode.Program{Main: m.fn})
	})
	t.Run("compare", func(t *testing.T) {
		m := newFn("main", 0, 2)
		m.long(1, 1)
		m.long(1, 1)
		m.emit(bytecode.OpIsIdentical, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.long(2, 1)
		m.emit(bytecode.OpIsIdentical, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.long(1, 1)
		m.emit(bytecode.OpIsEqual, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php echo (1===1); echo (1===2); echo (1==1);", &bytecode.Program{Main: m.fn})
	})
	t.Run("spaceship", func(t *testing.T) {
		m := newFn("main", 0, 2)
		m.long(1, 1)
		m.long(2, 1)
		m.emit(bytecode.OpSpaceship, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php echo 1<=>2;", &bytecode.Program{Main: m.fn})
	})
	t.Run("inc", func(t *testing.T) {
		m := newFn("main", 1, 2)
		m.long(1, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpPreIncCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpPostIncCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php $a=1; echo ++$a; echo $a++;", &bytecode.Program{Main: m.fn})
	})
	t.Run("ifelse", func(t *testing.T) {
		m := newFn("main", 1, 2)
		m.long(0, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		jz := m.emit(bytecode.OpJmpZ, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		jmp := m.emit(bytecode.OpJmp, 0, 0, 0, 0, 1)
		m.patch(jz, uint32(len(m.fn.Code)))
		m.long(2, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.patch(jmp, uint32(len(m.fn.Code)))
		m.ret(1)
		parity(t, "<?php $a=0; if($a){echo 1;}else{echo 2;}", &bytecode.Program{Main: m.fn})
	})
	t.Run("loop", func(t *testing.T) {
		m := newFn("main", 2, 4)
		m.long(0, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.long(0, 1)
		m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
		head := len(m.fn.Code)
		m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
		m.long(10, 1)
		m.emit(bytecode.OpIsSmaller, 0, 0, 0, 0, 1)
		jz := m.emit(bytecode.OpJmpZ, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpAdd, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpPostIncCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpJmp, 0, uint32(head), 0, 0, 1)
		m.patch(jz, uint32(len(m.fn.Code)))
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php $s=0; for($i=0;$i<10;$i++){$s+=$i;} echo $s;", &bytecode.Program{Main: m.fn})
	})
	t.Run("coalesce", func(t *testing.T) {
		m := newFn("main", 1, 2)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		j := m.emit(bytecode.OpJmpNull, 0, 0, 0, 0, 1)
		end := m.emit(bytecode.OpJmp, 0, 0, 0, 0, 1)
		m.patch(j, uint32(len(m.fn.Code)))
		m.str("z", 1)
		m.patch(end, uint32(len(m.fn.Code)))
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, `<?php $a=null; echo $a??"z";`, &bytecode.Program{Main: m.fn})
	})
	t.Run("print", func(t *testing.T) {
		m := newFn("main", 0, 2)
		m.long(1, 1)
		m.emit(bytecode.OpPrint, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php echo print 1;", &bytecode.Program{Main: m.fn})
	})
	t.Run("exit", func(t *testing.T) {
		m := newFn("main", 0, 1)
		m.str("a", 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.long(3, 1)
		m.emit(bytecode.OpExit, 0, 0, 0, 0, 1)
		parity(t, `<?php echo "a"; exit(3);`, &bytecode.Program{Main: m.fn})
	})
	t.Run("bool", func(t *testing.T) {
		m := newFn("main", 0, 1)
		m.long(0, 1)
		m.emit(bytecode.OpBoolNot, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.emit(bytecode.OpBoolNot, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php echo !0; echo !1;", &bytecode.Program{Main: m.fn})
	})
	t.Run("cast", func(t *testing.T) {
		m := newFn("main", 0, 1)
		m.str("12", 1)
		m.emit(bytecode.OpCast, 0, bytecode.CastInt, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, `<?php echo (int)"12";`, &bytecode.Program{Main: m.fn})
	})
	t.Run("isset", func(t *testing.T) {
		m := newFn("main", 1, 2)
		m.long(0, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpIssetCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEmptyCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php $a=0; echo isset($a)?1:0; echo empty($a)?1:0;", &bytecode.Program{Main: m.fn})
	})
	t.Run("ref", func(t *testing.T) {
		m := newFn("main", 2, 2)
		m.long(1, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpAssignRefCV, 0, 1, 0, 0, 1)
		m.long(2, 1)
		m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php $a=1; $b=&$a; $b=2; echo $a;", &bytecode.Program{Main: m.fn})
	})
}

func TestParityWideFrame(t *testing.T) {
	f := newFn("f", 200, 2)
	for i := range 200 {
		f.long(int64(i), 1)
		f.emit(bytecode.OpStoreCV, 0, uint32(i), 0, 0, 1)
	}
	f.long(0, 1)
	for i := range 200 {
		f.emit(bytecode.OpLoadCV, 0, uint32(i), 0, 0, 1)
		f.emit(bytecode.OpAdd, 0, 0, 0, 0, 1)
	}
	f.emit(bytecode.OpReturn, 0, 0, 0, 0, 1)
	if err := bytecode.Verify(f.fn); err != nil {
		t.Fatal(err)
	}
	m := newFn("main", 2, 4)
	m.long(0, 1)
	m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
	m.long(0, 1)
	m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
	head := len(m.fn.Code)
	m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
	m.long(500, 1)
	m.emit(bytecode.OpIsSmaller, 0, 0, 0, 0, 1)
	jz := m.emit(bytecode.OpJmpZ, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpInitCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpAdd, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpPostIncCV, 0, 1, 0, 0, 1)
	m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpJmp, 0, uint32(head), 0, 0, 1)
	m.patch(jz, uint32(len(m.fn.Code)))
	m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
	m.ret(1)
	parity(t, "<?php function f(){$s=0; for($i=0;$i<200;$i++){$s+=$i;} return $s;} $n=0; for($i=0;$i<500;$i++){$n+=f();} echo $n;", &bytecode.Program{Main: m.fn, Funcs: []*bytecode.OpArray{f.fn}})
}

func TestParityFinallyAndErrors(t *testing.T) {
	t.Run("finally-return", func(t *testing.T) {
		f := newFn("f", 0, 1)
		f.long(1, 1)
		f.emit(bytecode.OpReturn, 0, 0, 0, 0, 1)
		f.str("F", 1)
		f.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		f.emit(bytecode.OpLeaveFinally, 0, 0, 0, 0, 1)
		f.fn.TryTable = []bytecode.TryCatchEntry{{StartPC: 0, EndPC: 2, FinallyPC: 2, FinallyEndPC: 5}}
		m := callEcho(f.fn)
		parity(t, `<?php function f(){ try { return 1; } finally { echo "F"; } } echo f();`, &bytecode.Program{Main: m, Funcs: []*bytecode.OpArray{f.fn}})
	})
	t.Run("finally-catch-return", func(t *testing.T) {
		f := newFn("f", 1, 2)
		f.emit(bytecode.OpInitNew, 0, bytecode.ClassException, 0, 0, 1)
		f.str("x", 1)
		f.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
		f.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		f.emit(bytecode.OpThrow, 0, 0, 0, 0, 1)
		f.emit(bytecode.OpEnterCatch, 0, 0, 0, 0, 1)
		f.long(3, 1)
		f.emit(bytecode.OpReturn, 0, 0, 0, 0, 1)
		f.str("F", 1)
		f.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		f.emit(bytecode.OpLeaveFinally, 0, 0, 0, 0, 1)
		f.fn.TryTable = []bytecode.TryCatchEntry{{
			StartPC: 0, EndPC: 5, FinallyPC: 8, FinallyEndPC: 11,
			CatchTargets: []bytecode.CatchTarget{{Classes: []uint32{bytecode.ClassException}, CV: 0, PC: 5}},
		}}
		m := callEcho(f.fn)
		parity(t, `<?php function f(){ try { throw new Exception("x"); } catch (Exception $e) { return 3; } finally { echo "F"; } } echo f();`, &bytecode.Program{Main: m, Funcs: []*bytecode.OpArray{f.fn}})
	})
	t.Run("nested-finally", func(t *testing.T) {
		f := newFn("f", 0, 1)
		f.long(1, 1)
		f.emit(bytecode.OpReturn, 0, 0, 0, 0, 1)
		f.str("I", 1)
		f.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		f.emit(bytecode.OpLeaveFinally, 0, 0, 0, 0, 1)
		f.str("O", 1)
		f.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		f.emit(bytecode.OpLeaveFinally, 0, 0, 0, 0, 1)
		f.fn.TryTable = []bytecode.TryCatchEntry{
			{StartPC: 0, EndPC: 2, FinallyPC: 2, FinallyEndPC: 5},
			{StartPC: 0, EndPC: 5, FinallyPC: 5, FinallyEndPC: 8},
		}
		m := callEcho(f.fn)
		parity(t, `<?php function f(){ try { try { return 1; } finally { echo "I"; } } finally { echo "O"; } } echo f();`, &bytecode.Program{Main: m, Funcs: []*bytecode.OpArray{f.fn}})
	})
	t.Run("finally-override", func(t *testing.T) {
		f := newFn("f", 0, 1)
		f.long(1, 1)
		f.emit(bytecode.OpReturn, 0, 0, 0, 0, 1)
		f.long(2, 1)
		f.emit(bytecode.OpReturn, 0, 0, 0, 0, 1)
		f.fn.TryTable = []bytecode.TryCatchEntry{{StartPC: 0, EndPC: 2, FinallyPC: 2, FinallyEndPC: 4}}
		m := callEcho(f.fn)
		parity(t, `<?php function f(){ try { return 1; } finally { return 2; } } echo f();`, &bytecode.Program{Main: m, Funcs: []*bytecode.OpArray{f.fn}})
	})
	t.Run("div-caught", func(t *testing.T) {
		m := newFn("main", 1, 2)
		m.long(1, 1)
		m.long(0, 1)
		m.emit(bytecode.OpDiv, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEnterCatch, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.fn.Strings = append(m.fn.Strings, "getMessage")
		m.emit(bytecode.OpInitMethodCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		m.fn.TryTable = []bytecode.TryCatchEntry{{
			StartPC: 0, EndPC: 4, FinallyPC: bytecode.NoPC,
			CatchTargets: []bytecode.CatchTarget{{Classes: []uint32{bytecode.ClassDivisionByZeroError}, CV: 0, PC: 4}},
		}}
		parity(t, `<?php try { echo 1/0; } catch (DivisionByZeroError $e) { echo $e->getMessage(); }`, &bytecode.Program{Main: m.fn})
	})
	t.Run("div-uncaught", func(t *testing.T) {
		m := newFn("main", 0, 2)
		m.long(1, 2)
		m.long(0, 2)
		m.emit(bytecode.OpDiv, 0, 0, 0, 0, 2)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 2)
		m.ret(2)
		parity(t, "<?php\n1/0;\n", &bytecode.Program{Main: m.fn})
	})
	t.Run("shift-caught", func(t *testing.T) {
		m := newFn("main", 1, 2)
		m.long(1, 1)
		m.long(-2, 1)
		m.emit(bytecode.OpSL, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEnterCatch, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.fn.Strings = append(m.fn.Strings, "getMessage")
		m.emit(bytecode.OpInitMethodCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		m.fn.TryTable = []bytecode.TryCatchEntry{{
			StartPC: 0, EndPC: 4, FinallyPC: bytecode.NoPC,
			CatchTargets: []bytecode.CatchTarget{{Classes: []uint32{bytecode.ClassArithmeticError}, CV: 0, PC: 4}},
		}}
		parity(t, `<?php try { echo 1<<-2; } catch (ArithmeticError $e) { echo $e->getMessage(); }`, &bytecode.Program{Main: m.fn})
	})
	t.Run("shift-uncaught", func(t *testing.T) {
		m := newFn("main", 0, 2)
		m.long(1, 2)
		m.long(-2, 2)
		m.emit(bytecode.OpSL, 0, 0, 0, 0, 2)
		m.ret(2)
		parity(t, "<?php\n1 << -2;\n", &bytecode.Program{Main: m.fn})
	})
	t.Run("mod-caught", func(t *testing.T) {
		m := newFn("main", 1, 2)
		m.long(1, 1)
		m.long(0, 1)
		m.emit(bytecode.OpMod, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEnterCatch, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.fn.Strings = append(m.fn.Strings, "getMessage")
		m.emit(bytecode.OpInitMethodCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		m.fn.TryTable = []bytecode.TryCatchEntry{{
			StartPC: 0, EndPC: 3, FinallyPC: bytecode.NoPC,
			CatchTargets: []bytecode.CatchTarget{{Classes: []uint32{bytecode.ClassDivisionByZeroError}, CV: 0, PC: 3}},
		}}
		parity(t, `<?php try { 1%0; } catch (DivisionByZeroError $e) { echo $e->getMessage(); }`, &bytecode.Program{Main: m.fn})
	})
	t.Run("union-catch", func(t *testing.T) {
		m := newFn("main", 1, 2)
		m.long(1, 1)
		m.long(0, 1)
		m.emit(bytecode.OpDiv, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEnterCatch, 0, 0, 0, 0, 1)
		m.str("c", 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		m.fn.TryTable = []bytecode.TryCatchEntry{{
			StartPC: 0, EndPC: 4, FinallyPC: bytecode.NoPC,
			CatchTargets: []bytecode.CatchTarget{{Classes: []uint32{bytecode.ClassTypeError, bytecode.ClassDivisionByZeroError}, CV: 0, PC: 4}},
		}}
		parity(t, `<?php try { 1/0; } catch (TypeError|DivisionByZeroError $e) { echo "c"; }`, &bytecode.Program{Main: m.fn})
	})
	t.Run("uncaught-function", func(t *testing.T) {
		f := newFn("f", 0, 2)
		f.long(1, 3)
		f.long(0, 3)
		f.emit(bytecode.OpDiv, 0, 0, 0, 0, 3)
		f.emit(bytecode.OpReturn, 0, 0, 0, 0, 3)
		m := newFn("main", 0, 1)
		m.emit(bytecode.OpInitCall, 0, 0, 0, 0, 5)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 5)
		m.emit(bytecode.OpPop, 0, 0, 0, 0, 5)
		m.ret(5)
		parity(t, "<?php\nfunction f() {\n    return 1/0;\n}\nf();\n", &bytecode.Program{Main: m.fn, Funcs: []*bytecode.OpArray{f.fn}})
	})
}

func callEcho(fn *bytecode.OpArray) *bytecode.OpArray {
	m := newFn("main", 0, 2)
	m.emit(bytecode.OpInitCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
	m.ret(1)
	return m.fn
}

func TestParityArraysClassesAndClosures(t *testing.T) {
	t.Run("cow", func(t *testing.T) {
		m := newFn("main", 2, 4)
		m.emit(bytecode.OpInitArray, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.emit(bytecode.OpAppendArrayElement, 0, 0, 0, 0, 1)
		m.long(2, 1)
		m.emit(bytecode.OpAppendArrayElement, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
		m.long(0, 1)
		m.long(9, 1)
		m.emit(bytecode.OpAssignDim, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.long(0, 1)
		m.emit(bytecode.OpFetchDimR, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
		m.long(0, 1)
		m.emit(bytecode.OpFetchDimR, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php $a=[1,2]; $b=$a; $b[0]=9; echo $a[0], $b[0];", &bytecode.Program{Main: m.fn})
	})
	t.Run("foreach-value", func(t *testing.T) {
		m := newFn("main", 3, 4)
		m.emit(bytecode.OpInitArray, 0, 0, 0, 0, 1)
		for _, n := range []int64{1, 2, 3} {
			m.long(n, 1)
			m.emit(bytecode.OpAppendArrayElement, 0, 0, 0, 0, 1)
		}
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.long(0, 1)
		m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		reset := m.emit(bytecode.OpFeResetR, 0, 0, 0, 0, 1)
		loop := len(m.fn.Code)
		fetch := m.emit(bytecode.OpFeFetchR, 0, 0, 0, 2, 1)
		m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 2, 0, 0, 1)
		m.emit(bytecode.OpAdd, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpJmp, 0, uint32(loop), 0, 0, 1)
		end := uint32(len(m.fn.Code))
		m.patch(reset, end)
		m.fn.Code[fetch].Op1 = end
		m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php $a=[1,2,3]; $s=0; foreach($a as $v){ $s+=$v; } echo $s;", &bytecode.Program{Main: m.fn})
	})
	t.Run("foreach-ref", func(t *testing.T) {
		m := newFn("main", 2, 4)
		m.emit(bytecode.OpInitArray, 0, 0, 0, 0, 1)
		for _, n := range []int64{1, 2, 3} {
			m.long(n, 1)
			m.emit(bytecode.OpAppendArrayElement, 0, 0, 0, 0, 1)
		}
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		reset := m.emit(bytecode.OpFeResetRW, 0, 0, 0, 0, 1)
		loop := len(m.fn.Code)
		fetch := m.emit(bytecode.OpFeFetchRW, 0, 0, 0, 1, 1)
		m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
		m.long(2, 1)
		m.emit(bytecode.OpMul, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpJmp, 0, uint32(loop), 0, 0, 1)
		end := uint32(len(m.fn.Code))
		m.fn.Code[reset].Op1 = 0
		m.fn.Code[reset].Op2 = 0
		m.fn.Code[reset].Result = end
		m.fn.Code[fetch].Op1 = end
		m.fn.Code[fetch].Op2 = 0
		m.emit(bytecode.OpUnsetCV, 0, 1, 0, 0, 1)
		for i, sep := range []string{"", ",", ","} {
			if sep != "" {
				m.str(sep, 1)
				m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
			}
			m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
			m.long(int64(i), 1)
			m.emit(bytecode.OpFetchDimR, 0, 0, 0, 0, 1)
			m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		}
		m.ret(1)
		parity(t, `<?php $a=[1,2,3]; foreach($a as &$v){ $v=$v*2; } unset($v); echo $a[0], ",", $a[1], ",", $a[2];`, &bytecode.Program{Main: m.fn})
	})
	t.Run("unset-dim", func(t *testing.T) {
		m := newFn("main", 1, 3)
		m.emit(bytecode.OpInitArray, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.emit(bytecode.OpAppendArrayElement, 0, 0, 0, 0, 1)
		m.long(2, 1)
		m.emit(bytecode.OpAppendArrayElement, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.long(0, 1)
		m.emit(bytecode.OpUnsetDim, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.long(0, 1)
		m.emit(bytecode.OpIssetDim, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.emit(bytecode.OpFetchDimR, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, "<?php $a=[1,2]; unset($a[0]); echo isset($a[0]); echo $a[1];", &bytecode.Program{Main: m.fn})
	})
	t.Run("classes", func(t *testing.T) {
		ca := newFn("__construct", 0, 1)
		ca.str("A", 1)
		ca.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		ca.ret(1)
		cb := newFn("__construct", 0, 2)
		cb.fn.Strings = append(cb.fn.Strings, "__construct")
		cb.emit(bytecode.OpInitStaticMethodCall, bytecode.FlagParent|bytecode.FlagUseThis, 0, 0, 0, 1)
		cb.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		cb.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
		cb.str("B", 1)
		cb.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		cb.ret(1)
		who := newFn("who", 0, 1)
		who.fn.Strings = append(who.fn.Strings, "class")
		who.emit(bytecode.OpFetchClassConst, bytecode.FlagClassMagic, 0, 0, 0, 1)
		who.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		who.ret(1)
		prog := &bytecode.Program{
			Classes: []*bytecode.ClassProto{
				{Name: "A", ID: 64, Methods: []bytecode.MethodProto{
					{Name: "__construct", Func: ca.fn, ClassID: 64, Visibility: bytecode.VisPublic},
					{Name: "who", Static: true, Func: who.fn, ClassID: 64, Visibility: bytecode.VisPublic},
				}},
				{Name: "B", ID: 65, ParentID: 64, ParentName: "A", Methods: []bytecode.MethodProto{
					{Name: "__construct", Func: cb.fn, ClassID: 65, Visibility: bytecode.VisPublic},
				}},
			},
		}
		m := newFn("main", 1, 2)
		m.emit(bytecode.OpInitNew, 0, 65, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
		m.fn.Strings = append(m.fn.Strings, "who")
		m.emit(bytecode.OpInitStaticMethodCall, 0, 0, 65, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		prog.Main = m.fn
		parity(t, `<?php class A { function __construct(){ echo "A"; } static function who(){ echo static::class; } } class B extends A { function __construct(){ parent::__construct(); echo "B"; } } new B; echo B::who();`, prog)
	})
	t.Run("destruct-return", func(t *testing.T) {
		d := newFn("__destruct", 0, 1)
		d.str("D", 1)
		d.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		d.ret(1)
		f := newFn("f", 1, 1)
		f.emit(bytecode.OpInitNew, 0, 70, 0, 0, 1)
		f.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		f.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		f.ret(1)
		m := newFn("main", 0, 1)
		m.emit(bytecode.OpInitCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
		m.str("X", 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		prog := &bytecode.Program{
			Main:  m.fn,
			Funcs: []*bytecode.OpArray{f.fn},
			Classes: []*bytecode.ClassProto{{
				Name: "A", ID: 70,
				Methods: []bytecode.MethodProto{{Name: "__destruct", Func: d.fn, ClassID: 70, Visibility: bytecode.VisPublic}},
			}},
		}
		parity(t, `<?php class A { function __destruct(){ echo "D"; } } function f(){ $a=new A; } f(); echo "X";`, prog)
	})
	t.Run("destruct-shutdown", func(t *testing.T) {
		d := newFn("__destruct", 0, 1)
		d.str("D", 1)
		d.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		d.ret(1)
		m := newFn("main", 1, 1)
		m.emit(bytecode.OpInitNew, 0, 71, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.str("X", 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		prog := &bytecode.Program{
			Main: m.fn,
			Classes: []*bytecode.ClassProto{{
				Name: "A", ID: 71,
				Methods: []bytecode.MethodProto{{Name: "__destruct", Func: d.fn, ClassID: 71, Visibility: bytecode.VisPublic}},
			}},
		}
		parity(t, `<?php class A { function __destruct(){ echo "D"; } } $a=new A; echo "X";`, prog)
	})
	t.Run("user-exception", func(t *testing.T) {
		m := newFn("main", 1, 3)
		m.emit(bytecode.OpInitNew, 0, 72, 0, 0, 1)
		m.str("hi", 1)
		m.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
		m.long(7, 1)
		m.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpThrow, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEnterCatch, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.fn.Strings = append(m.fn.Strings, "getMessage")
		m.emit(bytecode.OpInitMethodCall, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.fn.Strings = append(m.fn.Strings, "getCode")
		m.emit(bytecode.OpInitMethodCall, 0, 2, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		m.fn.TryTable = []bytecode.TryCatchEntry{{
			StartPC: 0, EndPC: 7, FinallyPC: bytecode.NoPC,
			CatchTargets: []bytecode.CatchTarget{{Classes: []uint32{bytecode.ClassException}, CV: 0, PC: 7}},
		}}
		prog := &bytecode.Program{
			Main: m.fn,
			Classes: []*bytecode.ClassProto{{
				Name: "MyEx", ID: 72, ParentID: bytecode.ClassException, ParentName: "Exception",
			}},
		}
		parity(t, `<?php class MyEx extends Exception {} try { throw new MyEx("hi", 7); } catch (Exception $e) { echo $e->getMessage(), $e->getCode(); }`, prog)
	})
	t.Run("instanceof", func(t *testing.T) {
		m := newFn("main", 1, 1)
		m.emit(bytecode.OpInitNew, 0, 73, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpInstanceof, 0, 73, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		prog := &bytecode.Program{
			Main:    m.fn,
			Classes: []*bytecode.ClassProto{{Name: "A", ID: 73}},
		}
		parity(t, "<?php class A {} $a=new A; echo $a instanceof A ? 1 : 0;", prog)
	})
	t.Run("trait", func(t *testing.T) {
		ping := newFn("ping", 0, 1)
		ping.str("T", 1)
		ping.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		ping.ret(1)
		prog := &bytecode.Program{
			Traits: []*bytecode.ClassProto{{
				Name: "T", ID: 80, Kind: bytecode.KindTrait,
				Methods: []bytecode.MethodProto{{Name: "ping", Func: ping.fn, ClassID: 80, Visibility: bytecode.VisPublic}},
			}},
			Classes: []*bytecode.ClassProto{{
				Name: "A", ID: 81, Traits: []string{"T"},
			}},
		}
		if err := bytecode.Link(prog); err != nil {
			t.Fatal(err)
		}
		m := newFn("main", 1, 2)
		m.emit(bytecode.OpInitNew, 0, 81, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.fn.Strings = append(m.fn.Strings, "ping")
		m.emit(bytecode.OpInitMethodCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		prog.Main = m.fn
		parity(t, `<?php trait T { function ping(){ echo "T"; } } class A { use T; } echo (new A)->ping();`, prog)
	})
	t.Run("closure", func(t *testing.T) {
		body := newFn("{closure}", 1, 1)
		body.fn.Uses = []bytecode.UseVar{{CV: 0}}
		body.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		body.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		body.ret(1)
		bodyRef := newFn("{closure}", 1, 1)
		bodyRef.fn.Uses = []bytecode.UseVar{{CV: 0, ByRef: true}}
		bodyRef.long(2, 1)
		bodyRef.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		bodyRef.ret(1)
		m := newFn("main", 2, 2)
		m.long(1, 1)
		m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpBindClosure, 0, 0, 1, 0, 1)
		m.emit(bytecode.OpInitCall, bytecode.FlagClosure, 0, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
		m.long(1, 1)
		m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, bytecode.FlagLoadRef, 1, 0, 0, 1)
		m.emit(bytecode.OpBindClosure, 0, 1, 1, 0, 1)
		m.emit(bytecode.OpInitCall, bytecode.FlagClosure, 0, 0, 0, 1)
		m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
		m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.ret(1)
		parity(t, `<?php $x=1; $f=function() use ($x) { echo $x; }; $f(); $y=1; $g=function() use (&$y) { $y=2; }; $g(); echo $y;`, &bytecode.Program{Main: m.fn, Funcs: []*bytecode.OpArray{body.fn, bodyRef.fn}})
	})
	t.Run("match", func(t *testing.T) {
		m := newFn("main", 0, 2)
		m.long(1, 1)
		m.fn.Consts = append(m.fn.Consts, types.NewLong(1))
		arm := uint32(2)
		m.fn.Tables = []bytecode.JumpTable{{
			Cases:   []bytecode.JumpCase{{Kind: bytecode.CaseLong, Index: 0, Target: arm}},
			Default: 5,
		}}
		m.emit(bytecode.OpMatch, 0, 0, 0, 0, 1)
		m.str("a", 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		jmp := m.emit(bytecode.OpJmp, 0, 0, 0, 0, 1)
		m.str("b", 1)
		m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
		m.patch(jmp, uint32(len(m.fn.Code)))
		m.ret(1)
		m.fn.Tables[0].Cases[0].Target = 2
		m.fn.Tables[0].Default = 5
		parity(t, `<?php echo match(1){1=>"a", default=>"b"};`, &bytecode.Program{Main: m.fn})
	})
}

func TestSharedProgramRace(t *testing.T) {
	m := newFn("main", 0, 2)
	m.long(2, 1)
	m.long(3, 1)
	m.emit(bytecode.OpAdd, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
	m.ret(1)
	prog := &bytecode.Program{Main: m.fn}
	before := append([]bytecode.Instr(nil), m.fn.Code...)
	var wg sync.WaitGroup
	errc := make(chan string, 32)
	for range 32 {
		wg.Go(func() {
			z := zone.New(zone.DefaultCapacity)
			var buf bytes.Buffer
			code := Execute(z, &buf, ioDiscard{}, prog, NewContext())
			if code != 0 || buf.String() != "5" {
				errc <- buf.String()
			}
		})
	}
	wg.Wait()
	close(errc)
	for msg := range errc {
		t.Fatalf("course ou écart: %q", msg)
	}
	if !reflect.DeepEqual(before, m.fn.Code) {
		t.Fatal("le programme partagé a été modifié")
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func TestConcurrentExecuteSameProgramNoRace(t *testing.T) {
	fn := newFn("double", 1, 2)
	fn.fn.Params = []bytecode.ParamInfo{{Name: "x"}}
	fn.fn.CVNames = []string{"x"}
	fn.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
	fn.long(2, 1)
	fn.emit(bytecode.OpMul, 0, 0, 0, 0, 1)
	fn.emit(bytecode.OpReturn, 0, 0, 0, 0, 1)

	m := newFn("main", 2, 4)
	m.fn.CVNames = []string{"o", "i"}
	m.fn.Strings = []string{"x", "substr", "double"}
	m.emit(bytecode.OpInitNew, 0, 100, 0, 0, 1)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
	m.long(0, 1)
	m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
	loop := len(m.fn.Code)
	m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
	m.long(10, 1)
	m.emit(bytecode.OpIsSmaller, 0, 0, 0, 0, 1)
	jz := m.emit(bytecode.OpJmpZ, 0, 0, 0, 0, 1)
	// $o->x += double($i)
	m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpInitCall, bytecode.FlagNative, 2, 0, 0, 1)
	m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
	m.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpAssignPropOp, bytecode.FlagDiscard, 0, uint32(bytecode.OpAdd), 0, 1)
	// substr("abcd", 1, 2)
	m.emit(bytecode.OpInitCall, bytecode.FlagNative, 1, 0, 0, 1)
	m.str("abcd", 1)
	m.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
	m.long(1, 1)
	m.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
	m.long(2, 1)
	m.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpPostIncCV, bytecode.FlagDiscard, 1, 0, 0, 1)
	m.emit(bytecode.OpJmp, 0, uint32(loop), 0, 0, 1)
	m.patch(jz, uint32(len(m.fn.Code)))
	m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpFetchPropR, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
	m.ret(1)

	prog := &bytecode.Program{
		Main:  m.fn,
		Funcs: []*bytecode.OpArray{fn.fn},
		Classes: []*bytecode.ClassProto{{
			Name: "Box", ID: 100,
			Props: []bytecode.PropInfo{{Name: "x", Visibility: bytecode.VisPublic, HasDefault: true, Default: types.NewLong(0), ClassID: 100}},
		}},
	}
	if err := bytecode.Link(prog); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errc := make(chan string, 16)
	for range 4 {
		wg.Go(func() {
			z := zone.New(zone.DefaultCapacity)
			for range 20 {
				var out bytes.Buffer
				code := Execute(z, &out, ioDiscard{}, prog, nil)
				z.Reset(nil)
				if code != 0 || out.String() != "90" {
					errc <- out.String()
					return
				}
			}
		})
	}
	wg.Wait()
	close(errc)
	for msg := range errc {
		t.Fatalf("sortie inattendue: %q", msg)
	}
}

func TestSourceCacheInvalidationOnModify(t *testing.T) {
	dir := t.TempDir()
	incPath := filepath.Join(dir, "inc.php")
	if err := os.WriteFile(incPath, []byte("<?php echo 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newFn("main", 0, 2)
	m.fn.Strings = []string{"include"}
	m.str(incPath, 1)
	m.emit(bytecode.OpInclude, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
	m.ret(1)
	prog := &bytecode.Program{Main: m.fn}
	if err := bytecode.Link(prog); err != nil {
		t.Fatal(err)
	}
	z := zone.New(zone.DefaultCapacity)
	var out1 bytes.Buffer
	if code := Execute(z, &out1, ioDiscard{}, prog, nil); code != 0 || out1.String() != "1" {
		t.Fatalf("première inclusion: code=%d out=%q", code, out1.String())
	}
	z.Reset(nil)
	if err := os.WriteFile(incPath, []byte("<?php echo 22;"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out2 bytes.Buffer
	if code := Execute(z, &out2, ioDiscard{}, prog, nil); code != 0 || out2.String() != "22" {
		t.Fatalf("seconde inclusion après modification: code=%d out=%q, attendu \"22\"", code, out2.String())
	}
}

func TestVague3FastPathsAndInlineFrame(t *testing.T) {
	// Teste FlagDiscard sur PreInc/PostInc/PreDec/PostDec, la fusion LoadCV+Cmp+JmpZ et inlineFrame (retour et exception).
	callee := newFn("step", 2, 2)
	callee.fn.Params = []bytecode.ParamInfo{{Name: "a"}, {Name: "b"}}
	callee.fn.CVNames = []string{"a", "b"}
	callee.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
	callee.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
	callee.emit(bytecode.OpAdd, 0, 0, 0, 0, 1)
	callee.emit(bytecode.OpReturn, 0, 0, 0, 0, 1)

	thrower := newFn("boom", 1, 2)
	thrower.fn.Params = []bytecode.ParamInfo{{Name: "v"}}
	thrower.fn.CVNames = []string{"v"}
	thrower.emit(bytecode.OpInitNew, 0, bytecode.ClassException, 0, 0, 1)
	thrower.str("err_inline", 1)
	thrower.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
	thrower.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	thrower.emit(bytecode.OpThrow, 0, 0, 0, 0, 1)
	thrower.ret(1)

	m := newFn("main", 3, 3)
	m.fn.CVNames = []string{"acc", "i", "e"}
	m.long(0, 1)
	m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
	m.long(0, 1)
	m.emit(bytecode.OpStoreCV, 0, 1, 0, 0, 1)
	m.emit(bytecode.OpPreIncCV, bytecode.FlagDiscard, 1, 0, 0, 1)
	m.emit(bytecode.OpPostIncCV, bytecode.FlagDiscard, 1, 0, 0, 1)
	m.emit(bytecode.OpPreDecCV, bytecode.FlagDiscard, 1, 0, 0, 1)
	m.emit(bytecode.OpPostDecCV, bytecode.FlagDiscard, 1, 0, 0, 1)
	loop := len(m.fn.Code)
	m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
	m.long(5, 1)
	m.emit(bytecode.OpIsSmaller, 0, 0, 0, 0, 1)
	jz := m.emit(bytecode.OpJmpZ, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpInitCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpLoadCV, 0, 1, 0, 0, 1)
	m.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpPostIncCV, bytecode.FlagDiscard, 1, 0, 0, 1)
	m.emit(bytecode.OpJmp, 0, uint32(loop), 0, 0, 1)
	m.patch(jz, uint32(len(m.fn.Code)))

	// Appel inline qui lève une exception attrapée par un try/catch dans main
	tryStart := uint32(len(m.fn.Code))
	m.emit(bytecode.OpNop, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpInitCall, 0, 1, 0, 0, 1)
	m.long(1, 1)
	m.emit(bytecode.OpSendVal, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
	afterTry := m.emit(bytecode.OpJmp, 0, 0, 0, 0, 1)
	tryEnd := uint32(len(m.fn.Code))
	catchPC := uint32(len(m.fn.Code))
	m.emit(bytecode.OpEnterCatch, 0, 2, 0, 0, 1)
	m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
	m.long(100, 1)
	m.emit(bytecode.OpAdd, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
	m.patch(afterTry, uint32(len(m.fn.Code)))
	m.fn.TryTable = []bytecode.TryCatchEntry{{
		StartPC:      tryStart,
		EndPC:        tryEnd,
		FinallyPC:    bytecode.NoPC,
		FinallyEndPC: bytecode.NoPC,
		CatchTargets: []bytecode.CatchTarget{{Classes: []uint32{bytecode.ClassException}, PC: catchPC, CV: 2}},
	}}
	m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpEcho, 0, 0, 0, 0, 1)
	m.ret(1)

	prog := &bytecode.Program{Main: m.fn, Funcs: []*bytecode.OpArray{callee.fn, thrower.fn}}
	if err := bytecode.Link(prog); err != nil {
		t.Fatal(err)
	}
	if err := bytecode.Verify(prog.Main); err != nil {
		t.Fatal(err)
	}
	z := zone.New(zone.DefaultCapacity)
	var out bytes.Buffer
	if code := Execute(z, &out, ioDiscard{}, prog, nil); code != 0 || out.String() != "110" {
		t.Fatalf("got code=%d out=%q, want 0 and \"110\"", code, out.String())
	}
}

func TestMainPropICActive(t *testing.T) {
	c := &bytecode.ClassProto{
		ID:    bytecode.ClassUser,
		Name:  "Point",
		Props: []bytecode.PropInfo{{Name: "x", Visibility: bytecode.VisPublic, Default: types.NewLong(0)}},
	}
	m := newFn("main", 2, 2)
	m.fn.CVNames = []string{"p", "i"}
	m.emit(bytecode.OpInitNew, 0, bytecode.ClassUser, 0, 0, 1)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpStoreCV, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 2)
	m.long(42, 2)
	pIdx := uint32(len(m.fn.Strings))
	m.fn.Strings = append(m.fn.Strings, "x")
	m.emit(bytecode.OpAssignProp, bytecode.FlagDiscard, pIdx, 0, 0, 2)
	m.emit(bytecode.OpLoadCV, 0, 0, 0, 0, 3)
	m.emit(bytecode.OpFetchPropR, 0, pIdx, 0, 0, 3)
	m.emit(bytecode.OpEcho, 0, 0, 0, 0, 3)
	m.ret(4)

	prog := &bytecode.Program{Main: m.fn, Classes: []*bytecode.ClassProto{c}}
	if err := bytecode.Link(prog); err != nil {
		t.Fatal(err)
	}
	if prog.Main.FuncID == 0 {
		t.Fatal("Main.FuncID must not be 0 after Link")
	}
	z := zone.New(zone.DefaultCapacity)
	var out bytes.Buffer
	inst := New(z, &out, ioDiscard{}, prog, nil)
	if code := inst.Execute(); code != 0 || out.String() != "42" {
		t.Fatalf("got code=%d out=%q", code, out.String())
	}
	if int(prog.Main.FuncID) >= len(inst.propCaches) || len(inst.propCaches[prog.Main.FuncID]) == 0 {
		t.Fatalf("expected propCaches[Main.FuncID=%d] to be populated", prog.Main.FuncID)
	}
}

func TestSmallSlotsDtorRealloc(t *testing.T) {
	dtor := newFn("__destruct", 40, 2)
	dtor.fn.CVNames = make([]string, 40)
	for i := range dtor.fn.CVNames {
		dtor.fn.CVNames[i] = "v"
	}
	dtor.long(1, 1)
	dtor.emit(bytecode.OpStoreCV, 0, 39, 0, 0, 1)
	dtor.ret(1)

	cl := &bytecode.ClassProto{
		ID:      bytecode.ClassUser,
		Name:    "BigDtor",
		Methods: []bytecode.MethodProto{{Name: "__destruct", Visibility: bytecode.VisPublic, Func: dtor.fn}},
	}
	f := newFn("f", 20, 2)
	f.fn.CVNames = make([]string, 20)
	for i := range 20 {
		f.emit(bytecode.OpInitNew, 0, bytecode.ClassUser, 0, 0, 1)
		f.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
		f.emit(bytecode.OpStoreCV, 0, uint32(i), 0, 0, 1)
	}
	f.ret(1)

	m := newFn("main", 1, 2)
	m.emit(bytecode.OpInitCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpPop, 0, 0, 0, 0, 1)
	m.emit(bytecode.OpInitCall, 0, 0, 0, 0, 2)
	m.emit(bytecode.OpDoCall, 0, 0, 0, 0, 2)
	m.emit(bytecode.OpPop, 0, 0, 0, 0, 2)
	m.ret(3)

	prog := &bytecode.Program{Main: m.fn, Funcs: []*bytecode.OpArray{f.fn}, Classes: []*bytecode.ClassProto{cl}}
	if err := bytecode.Link(prog); err != nil {
		t.Fatal(err)
	}
	z := zone.New(zone.DefaultCapacity)
	inst := New(z, ioDiscard{}, ioDiscard{}, prog, nil)
	inst.slots = make([]types.Value, 64)
	if code := inst.Execute(); code != 0 {
		t.Fatalf("got code=%d", code)
	}
}


