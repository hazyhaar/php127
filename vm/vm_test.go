package vm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/mysqli"
	"code.hazyhaar.fr/devhoros/php127/parser"
	"code.hazyhaar.fr/devhoros/php127/sapi"
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

// phpResult exécute /usr/bin/php -n sur un script, depuis le répertoire du script, et rend sortie standard, sortie d'erreur et code de sortie.
func phpResult(t *testing.T, path string) (string, string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", path)
	cmd.Dir = filepath.Dir(path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// vmResult compile un script depuis son chemin absolu et l'exécute sur une zone fraîche, ou sur la zone fournie pour rejouer le chemin de VM recyclée.
func vmResult(t *testing.T, path string, z *zone.Zone) (string, string, int) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.CompileSource(string(src), path)
	if err != nil {
		t.Fatalf("compilation de %s: %v", path, err)
	}
	if z == nil {
		z = zone.New(zone.DefaultCapacity)
	}
	var out, errOut bytes.Buffer
	code := Execute(z, &out, &errOut, prog, NewContext())
	return out.String(), errOut.String(), code
}

// writeTree écrit un arbre de fichiers PHP dans un répertoire temporaire et rend le chemin absolu du fichier principal.
func writeTree(t *testing.T, files map[string]string, main string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, main)
}

// parityTree compare au caractère près la VM et PHP 8.3 sur un programme de plusieurs fichiers, puis rejoue la VM sur une zone recyclée.
func parityTree(t *testing.T, files map[string]string, main string) string {
	t.Helper()
	path := writeTree(t, files, main)
	wantOut, wantErr, wantCode := phpResult(t, path)
	gotOut, gotErr, gotCode := vmResult(t, path, nil)
	if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
		t.Fatalf("stdout got %q\nwant %q\nstderr got %q want %q\nexit %d want %d", gotOut, wantOut, gotErr, wantErr, gotCode, wantCode)
	}
	z := zone.New(zone.DefaultCapacity)
	for run := range 2 {
		z.Reset(nil)
		gotOut, _, gotCode = vmResult(t, path, z)
		if gotOut != wantOut || gotCode != wantCode {
			t.Fatalf("zone recyclée, passe %d: stdout got %q want %q, exit %d want %d", run, gotOut, wantOut, gotCode, wantCode)
		}
	}
	return wantOut
}

func TestLot71RequireOnceChainClassesTraitsInterfaces(t *testing.T) {
	files := map[string]string{
		"iface.php": `<?php
interface Shape { const SIDES = 0; public function area(): float; }
interface Named extends Shape { public function name(): string; }
`,
		"trait.php": `<?php
require_once __DIR__ . '/iface.php';
trait Counts { public static $count = 0; public function bump(): int { return ++static::$count; } }
trait Describes {
	use Counts;
	public $tag = 'd';
	public function describe(): string {
		$this->bump();
		return $this->name() . ' area=' . $this->area() . ' sides=' . static::SIDES . ' tag=' . $this->tag;
	}
}
`,
		"base.php": `<?php
require_once __DIR__ . '/trait.php';
abstract class Base implements Named {
	use Describes;
	protected $w;
	public function __construct($w) { $this->w = $w; }
	public function name(): string { return static::class; }
}
`,
		"child.php": `<?php
require_once __DIR__ . '/base.php';
class Square extends Base {
	const SIDES = 4;
	public $extra = 'x';
	public function area(): float { return $this->w * $this->w; }
}
$fromInclude = new Square(3);
echo $fromInclude->describe(), "\n";
`,
		"main.php": `<?php
var_dump(require_once __DIR__ . '/child.php');
var_dump(require_once __DIR__ . '/child.php');
class Cube extends Square {
	public function area(): float { return parent::area() * 6; }
}
$s = new Square(2);
$c = new Cube(1);
echo $s->describe(), "\n", $c->describe(), "\n";
var_dump($s instanceof Shape, $s instanceof Named, $s instanceof Base, $c instanceof Square, $c instanceof Shape);
var_dump(Square::$count, Base::SIDES, Square::SIDES, Cube::SIDES, $c->extra, $c->tag);
var_dump(get_parent_class($c), get_parent_class('Square'), is_subclass_of($c, 'Base'), is_a('Cube', 'Named', true));
var_dump(class_exists('Square'), class_exists('Base'), interface_exists('Named'), trait_exists('Describes'), class_exists('Describes'));
var_dump(method_exists('Cube', 'describe'), method_exists($s, 'bump'), get_object_vars($s));
var_dump(array_slice(get_declared_classes(), -3), array_slice(get_declared_interfaces(), -2), get_declared_traits());
var_dump($fromInclude->area(), isset($fromInclude));
`,
	}
	parityTree(t, files, "main.php")
}

func TestLot71AutoloaderAndClassAlias(t *testing.T) {
	files := map[string]string{
		"lib/Compat_Impl.php": `<?php
class Compat_Impl {
	const VERSION = '1.0';
	public static function hi() { return 'hi'; }
	public function who() { return static::class; }
}
class_alias('Compat_Impl', 'Compat');
`,
		"lib/Parent_A.php": `<?php
abstract class Parent_A { public $p = 'parent'; abstract public function kind(); public function hello() { return 'hello ' . $this->kind(); } }
`,
		"lib/Child_A.php": `<?php
class Child_A extends Parent_A { public $c = 'child'; public function kind() { return 'child'; } }
`,
		"lib/Thrower.php": `<?php
throw new RuntimeException('autoload refuse');
`,
		"main.php": `<?php
spl_autoload_register(function ($class) {
	echo "autoload($class)\n";
	$file = __DIR__ . '/lib/' . $class . '.php';
	if (file_exists($file)) {
		require_once $file;
	}
});
var_dump(class_exists('Compat_Impl', false));
var_dump(class_exists('Compat_Impl'));
echo Compat_Impl::hi(), ' ', Compat::VERSION, "\n";
$x = new Compat();
echo get_class($x), ' ', $x->who(), "\n";
var_dump($x instanceof Compat, $x instanceof Compat_Impl, is_a($x, 'Compat'), class_exists('compat'));
var_dump(class_alias('Compat_Impl', 'Compat'));
var_dump(class_alias('Nowhere_Z', 'Alias_Z'));
var_dump(class_alias('Nowhere_Z', 'Alias_Z', false));
$k = new Child_A();
echo $k->hello(), ' ', $k->p, ' ', $k->c, "\n";
var_dump($k instanceof Parent_A, get_parent_class($k));
var_dump(class_exists('Missing_X'), interface_exists('Missing_I'), trait_exists('Missing_T'));
spl_autoload_call('Missing_Call');
try {
	new Missing_Y();
} catch (Error $e) {
	echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
	Missing_S::run();
} catch (Error $e) {
	echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
	echo Missing_C::K;
} catch (Error $e) {
	echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
	new Thrower();
} catch (RuntimeException $e) {
	echo 'relance: ', $e->getMessage(), "\n";
}
var_dump($x instanceof Missing_W);
`,
	}
	parityTree(t, files, "main.php")
}

func TestLot71IncludeMissingFile(t *testing.T) {
	files := map[string]string{
		"main.php": `<?php
$r = include 'absent.php';
var_dump($r);
$r = include_once __DIR__ . '/absent_once.php';
var_dump($r);
try {
	require 'absent_req.php';
} catch (Error $e) {
	echo get_class($e), ': ', $e->getMessage(), "\n";
}
echo "suite\n";
require_once 'absent_fatal.php';
echo "jamais\n";
`,
	}
	parityTree(t, files, "main.php")
}

// TestLot71SilencedIncludeMissingFile vérifie le résultat false et la poursuite du script ; la suppression des Warning par @ n'est pas comparée, le compilateur effaçant l'opérateur @ (constat consigné au rapport du lot 7.1).
func TestLot71SilencedIncludeMissingFile(t *testing.T) {
	path := writeTree(t, map[string]string{"main.php": `<?php
$r = @include 'absent.php';
var_dump($r);
$s = @include_once 'absent.php';
var_dump($s);
echo "suite\n";
`}, "main.php")
	wantOut, _, wantCode := phpResult(t, path)
	gotOut, _, gotCode := vmResult(t, path, nil)
	if wantOut != "bool(false)\nbool(false)\nsuite\n" || wantCode != 0 {
		t.Fatalf("référence PHP inattendue: %q exit %d", wantOut, wantCode)
	}
	if gotCode != 0 || !strings.HasSuffix(gotOut, "bool(false)\nsuite\n") || strings.Count(gotOut, "bool(false)") != 2 {
		t.Fatalf("VM: %q exit %d", gotOut, gotCode)
	}
}

func TestLot71GlobalsAcrossIncludes(t *testing.T) {
	files := map[string]string{
		"version.php": `<?php
$wp_version = '6.9-alpha';
$wp_db_version = 60421;
`,
		"rootvars.php": `<?php
$root_var = 5;
$counter = ($counter ?? 0) + 1;
`,
		"declares_global.php": `<?php
global $from_file;
$from_file = 'set in file';
$local_only = 'local';
`,
		"nested.php": `<?php
require __DIR__ . '/rootvars.php';
$nested_level = 'nested';
`,
		"main.php": `<?php
function load_version() {
	global $wp_version;
	require __DIR__ . '/version.php';
	return $wp_db_version;
}
var_dump(load_version());
var_dump($wp_version, isset($wp_db_version));
function show() { global $wp_version; return $wp_version; }
var_dump(show(), $GLOBALS['wp_version']);
require __DIR__ . '/rootvars.php';
require __DIR__ . '/rootvars.php';
function root() { global $root_var, $counter; return [$root_var, $counter]; }
var_dump(root(), $GLOBALS['root_var']);
function in_function() {
	include __DIR__ . '/declares_global.php';
	return [$from_file, $local_only];
}
var_dump(in_function(), $from_file, isset($local_only));
include __DIR__ . '/nested.php';
var_dump($nested_level, $counter);
function nested_global() { global $nested_level; return $nested_level; }
var_dump(nested_global());
`,
	}
	parityTree(t, files, "main.php")
}

func TestLot71RegisterShutdownFunction(t *testing.T) {
	t.Run("exit", func(t *testing.T) {
		parityTree(t, map[string]string{"main.php": `<?php
class S { public static function m($v) { echo "static $v\n"; } }
function report() { global $kept; echo "global=$kept\n"; }
register_shutdown_function(function ($a, $b) {
	echo "first $a $b\n";
	register_shutdown_function(function () { echo "nested\n"; });
}, 1, 2);
register_shutdown_function('printf', "%s-%s\n", 'x', 'y');
register_shutdown_function(['S', 'm'], 'v');
register_shutdown_function('report');
$kept = 'kept';
var_dump(register_shutdown_function(function () { echo "last\n"; }));
try {
	register_shutdown_function('no_such_function');
} catch (TypeError $e) {
	echo $e->getMessage(), "\n";
}
function leave() { echo "body\n"; exit(3); }
leave();
echo "jamais\n";
`}, "main.php")
	})
	t.Run("fin normale", func(t *testing.T) {
		parityTree(t, map[string]string{"main.php": `<?php
register_shutdown_function(function () { echo "after main\n"; });
echo "main\n";
`}, "main.php")
	})
	t.Run("exit dans un rappel", func(t *testing.T) {
		parityTree(t, map[string]string{"main.php": `<?php
register_shutdown_function(function () { echo "one\n"; exit(7); });
register_shutdown_function(function () { echo "two\n"; });
echo "main\n";
`}, "main.php")
	})
	t.Run("exception non rattrapée", func(t *testing.T) {
		parityTree(t, map[string]string{"main.php": `<?php
register_shutdown_function(function () { echo "shutdown runs\n"; });
throw new LogicException('boom');
`}, "main.php")
	})
}

func TestLot71FunctionRedeclarationAndLateParent(t *testing.T) {
	t.Run("fonction conditionnelle", func(t *testing.T) {
		parityTree(t, map[string]string{
			"compat.php": `<?php
if (!function_exists('strlen')) { function strlen($s) { return -1; } }
if (!function_exists('compat_helper')) { function compat_helper() { return 'first'; } }
function uses_helper() { return compat_helper() . ' ' . strlen('abc'); }
`,
			"compat2.php": `<?php
if (!function_exists('compat_helper')) { function compat_helper() { return 'second'; } }
function uses_helper2() { return compat_helper(); }
`,
			"main.php": `<?php
require __DIR__ . '/compat.php';
require __DIR__ . '/compat2.php';
echo uses_helper(), ' ', uses_helper2(), "\n";
var_dump(array_values(array_filter(get_defined_functions()['user'], fn ($f) => str_starts_with($f, 'uses_') || str_starts_with($f, 'compat_'))));
`,
		}, "main.php")
	})
	t.Run("redéclaration fatale", func(t *testing.T) {
		parityTree(t, map[string]string{
			"f1.php":   "<?php\nfunction twice() {}\n",
			"f2.php":   "<?php\nfunction twice() {}\n",
			"main.php": "<?php\nrequire 'f1.php';\necho \"between\\n\";\nrequire 'f2.php';\necho \"jamais\\n\";\n",
		}, "main.php")
	})
	t.Run("parent absent", func(t *testing.T) {
		parityTree(t, map[string]string{
			"orphan.php": "<?php\nclass Orphan extends Nobody {}\n",
			"main.php": `<?php
try {
	require __DIR__ . '/orphan.php';
} catch (Error $e) {
	echo get_class($e), ': ', $e->getMessage(), ' @', basename($e->getFile()), ':', $e->getLine(), "\n";
}
var_dump(class_exists('Orphan', false));
`,
		}, "main.php")
	})
	t.Run("parent déclaré par un require ultérieur du même fichier", func(t *testing.T) {
		parityTree(t, map[string]string{
			"parent.php": "<?php\nclass LaterParent { public $v = 'p'; public function who() { return 'parent'; } }\n",
			"child.php":  "<?php\nrequire_once __DIR__ . '/parent.php';\nclass LaterChild extends LaterParent { public $w = 'c'; }\n$o = new LaterChild();\necho $o->who(), $o->v, $o->w, \"\\n\";\n",
			"main.php":   "<?php\nrequire __DIR__ . '/child.php';\nvar_dump(new LaterChild() instanceof LaterParent);\n",
		}, "main.php")
	})
}

func TestLot71ParentCallForwardsThisAndEvalDeclarations(t *testing.T) {
	parityTree(t, map[string]string{
		"a.php": `<?php
class FwdA {
	public $w = 2;
	public function area() { return $this->w * 3; }
	public static function s() { return isset($this) ? 'this' : 'nothis'; }
}
`,
		"main.php": `<?php
require __DIR__ . '/a.php';
class FwdB extends FwdA {
	public function __construct() { $this->w = 5; }
	public function area() { return parent::area() * 6 . FwdA::s() . self::s() . static::s(); }
	public function viaName() { return FwdA::area(); }
}
$b = new FwdB();
echo $b->area(), ' ', $b->viaName(), "\n";
eval('class EvalDeclared extends FwdA { public function area() { return "eval " . parent::area(); } }');
echo (new EvalDeclared())->area(), "\n";
try {
	eval('class EvalOrphan extends EvalNobody {}');
} catch (Error $e) {
	echo get_class($e), ': ', $e->getMessage(), "\n";
}
var_dump(class_exists('EvalDeclared'), class_exists('EvalOrphan', false));
`,
	}, "main.php")
}

func TestLot71IniAndIntrospection(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
var_dump(ini_get('memory_limit'), ini_get('max_execution_time'), ini_get('default_charset'), ini_get('pcre.backtrack_limit'));
var_dump(ini_get('include_path'), ini_get('open_basedir'), ini_get('zlib.output_compression'), ini_get('arg_separator.output'));
var_dump(ini_get('no.such.directive'), ini_set('no.such.directive', '1'), ini_set('disable_functions', 'x'));
var_dump(ini_set('memory_limit', '256M'), ini_get('memory_limit'), ini_alter('memory_limit', '512M'), ini_get('memory_limit'));
ini_restore('memory_limit');
var_dump(ini_get('memory_limit'), ini_set('display_errors', false), ini_get('display_errors'));
var_dump(set_include_path('/opt/a:/opt/b'), get_include_path(), ini_get('include_path'));
var_dump(extension_loaded('Core'), extension_loaded('standard'), extension_loaded('SPL'), extension_loaded('json'), extension_loaded('pcre'), extension_loaded('no_such_ext'));
var_dump(in_array('standard', get_loaded_extensions(), true), in_array('Core', get_loaded_extensions(), true));
class Decl {} interface DeclI {} trait DeclT {} enum DeclE {}
class_alias('Decl', 'DeclAlias');
var_dump(array_slice(get_declared_classes(), -3), array_slice(get_declared_interfaces(), -1), get_declared_traits());
define('LOT71_A', 1);
const LOT71_B = 'b';
var_dump(get_defined_constants(true)['user'], get_defined_constants()['LOT71_B']);
function lot71_f() {}
function Lot71_G() {}
var_dump(get_defined_functions()['user']);
function vars($a, $b = 2) { $c = [$a, $b]; $unset; return get_defined_vars(); }
var_dump(vars(1));
`}, "main.php")
}

// wordpressSrc rend le chemin absolu de la copie de WordPress embarquée dans testdata.
func wordpressSrc(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "testdata", "wordpress", "src"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wp-includes", "class-wp-hook.php")); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("copie WordPress absente (%v)", err)
		}
		t.Fatalf("copie WordPress absente: %v", err)
	}
	return dir
}

func TestLot71WordPressSodiumCompatAndWPHook(t *testing.T) {
	wp := wordpressSrc(t)
	out := parityTree(t, map[string]string{"main.php": `<?php
require_once '` + wp + `/wp-includes/sodium_compat/autoload.php';
var_dump(class_exists('ParagonIE_Sodium_Compat', false), class_exists('SodiumException', false));
var_dump(ParagonIE_Sodium_Core_Util::bin2hex("ab"), ParagonIE_Sodium_Core_Util::strlen("abc"));
require_once '` + wp + `/wp-includes/plugin.php';
var_dump(require_once '` + wp + `/wp-includes/class-wp-hook.php');
$hook = new WP_Hook();
$hook->add_filter('the_title', function ($v) { return $v . '!'; }, 10, 1);
$hook->add_filter('the_title', 'strtoupper', 5, 1);
var_dump($hook->has_filters(), $hook->apply_filters('title', array('title')));
var_dump($hook instanceof ArrayAccess, $hook instanceof Iterator, isset($hook[10]), count($hook[5]));
add_filter('greet', function ($s) { return "hello $s"; });
echo apply_filters('greet', 'world'), "\n";
add_action('lot71', function () { echo "action ran\n"; });
do_action('lot71');
var_dump(did_action('lot71'), has_filter('greet'), isset($GLOBALS['wp_filter']['greet']), $GLOBALS['wp_filter']['greet'] instanceof WP_Hook);
`}, "main.php")
	if !strings.Contains(out, `string(6) "TITLE!"`) || !strings.Contains(out, "hello world\naction ran\n") {
		t.Fatalf("référence PHP inattendue: %q", out)
	}
}

// TestLot71WordPressSettingsIncludeChain rejoue les inclusions de wp-settings.php qui précèdent require_wp_db(), sans l'appel wp_check_php_mysql_versions() dont l'issue dépend de l'extension mysqli.
func TestLot71WordPressSettingsIncludeChain(t *testing.T) {
	wp := wordpressSrc(t)
	out := parityTree(t, map[string]string{"main.php": `<?php
define('ABSPATH', '` + wp + `/');
define('WPINC', 'wp-includes');
global $wp_version, $wp_db_version, $required_php_version;
require ABSPATH . WPINC . '/version.php';
require ABSPATH . WPINC . '/compat-utf8.php';
require ABSPATH . WPINC . '/compat.php';
require ABSPATH . WPINC . '/load.php';
foreach (['class-wp-paused-extensions-storage.php', 'class-wp-exception.php', 'class-wp-fatal-error-handler.php', 'class-wp-recovery-mode-cookie-service.php', 'class-wp-recovery-mode-key-service.php', 'class-wp-recovery-mode-link-service.php', 'class-wp-recovery-mode-email-service.php', 'class-wp-recovery-mode.php', 'error-protection.php', 'default-constants.php'] as $f) {
	require ABSPATH . WPINC . '/' . $f;
}
require_once ABSPATH . WPINC . '/plugin.php';
foreach (['class-wp-list-util.php', 'class-wp-token-map.php', 'utf8.php', 'formatting.php', 'meta.php', 'functions.php', 'class-wp-admin-notice-collector.php', 'class-wp-meta-query.php', 'class-wp-matchesmapregex.php', 'class-wp.php', 'class-wp-error.php', 'pomo/mo.php', 'l10n/class-wp-translation-controller.php', 'l10n/class-wp-translations.php', 'l10n/class-wp-translation-file.php', 'l10n/class-wp-translation-file-mo.php', 'l10n/class-wp-translation-file-php.php'] as $f) {
	require ABSPATH . WPINC . '/' . $f;
}
var_dump($wp_version, $wp_db_version, $required_php_version);
var_dump(class_exists('WP_Error'), class_exists('WP_Recovery_Mode'), class_exists('MO'), class_exists('POMO_Reader'), class_exists('WP_Translation_File_MO'));
var_dump(wp_parse_args(['a' => 1], ['a' => 0, 'b' => 2]));
$e = new WP_Error('code', 'message', ['d' => 1]);
var_dump($e->get_error_message(), $e->get_error_codes(), is_wp_error($e), $e instanceof WP_Error);
$l = new WP_List_Util([['k' => 2], ['k' => 1]]);
var_dump($l->pluck('k'));
var_dump(wp_is_numeric_array([1, 2]), absint(-5), trailingslashit('/a'), untrailingslashit('/a/'));
$mo = new MO();
var_dump($mo instanceof Gettext_Translations, $mo instanceof Translations);
`}, "main.php")
	if !strings.Contains(out, `string(7) "message"`) {
		t.Fatalf("référence PHP inattendue: %q", out)
	}
}

// TestReprise7NamespacesAcrossAutoloadedFiles compare à PHP des classes, interfaces, traits et exceptions déclarés dans des espaces de noms et chargés par un autoloader PSR-4 : alias de use, use groupé, new static, catch par alias, types, ::class.
func TestReprise7NamespacesAcrossAutoloadedFiles(t *testing.T) {
	parityTree(t, map[string]string{
		"src/Acme/Shapes/Shape.php": `<?php
namespace Acme\Shapes;
interface Shape { const SIDES = 0; public function area(): float; }
`,
		"src/Acme/Util/Describes.php": `<?php
namespace Acme\Util;
trait Describes { public function describe(): string { return static::class . ' area=' . $this->area(); } }
`,
		"src/Acme/Shapes/Polygon.php": `<?php
namespace Acme\Shapes;
use Acme\Util\Describes;
abstract class Polygon implements Shape, \Countable {
	use Describes;
	public function count(): int { return static::SIDES; }
}
`,
		"src/Acme/Shapes/Exception/Invalid.php": `<?php
namespace Acme\Shapes\Exception;
class Invalid extends \InvalidArgumentException {}
`,
		"src/Acme/Shapes/Square.php": `<?php
namespace Acme\Shapes;
use Acme\Shapes\Exception\Invalid as InvalidShape;
final class Square extends Polygon {
	const SIDES = 4;
	public function __construct(private float $w) { if ($w < 0) { throw new InvalidShape('negative side'); } }
	public function area(): float { return $this->w ** 2; }
	public static function unit(): static { return new static(1); }
	public function withSide(float $w): self { return new self($w); }
}
`,
		"main.php": `<?php
namespace App;
use Acme\Shapes\{Square, Shape};
use Acme\Shapes\Exception\Invalid;
spl_autoload_register(function ($c) {
	echo "autoload $c\n";
	$f = __DIR__ . '/src/' . str_replace('\\', '/', $c) . '.php';
	if (is_file($f)) {
		require $f;
	}
});
$s = Square::unit();
echo get_class($s), ' ', $s->describe(), ' ', count($s), ' ', Square::SIDES, ' ', Shape::SIDES, "\n";
var_dump($s instanceof Shape, $s instanceof \Countable, $s instanceof \Acme\Shapes\Polygon, Square::class, get_parent_class($s));
try {
	new Square(-1);
} catch (Invalid $e) {
	echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
	new \Acme\Shapes\Missing();
} catch (\Error $e) {
	echo $e->getMessage(), "\n";
}
function area(Shape $s): float { return $s->area(); }
echo area($s->withSide(3)), "\n";
var_dump(class_exists('Acme\Shapes\Square', false), class_exists('Square', false), interface_exists('\Acme\Shapes\Shape'), __NAMESPACE__);
`,
	}, "main.php")
}

// TestReprise7WordPressRequestsNamespacedClasses exerce les vraies classes WpOrg\Requests de WordPress : autoloader de la bibliothèque, ArrayAccess d'un dictionnaire, hiérarchie d'exceptions HTTP, alias PSR-0 dépréciés.
func TestReprise7WordPressRequestsNamespacedClasses(t *testing.T) {
	wp := wordpressSrc(t)
	out := parityTree(t, map[string]string{"main.php": `<?php
require_once '` + wp + `/wp-includes/Requests/src/Autoload.php';
WpOrg\Requests\Autoload::register();
use WpOrg\Requests\Utility\CaseInsensitiveDictionary;
use WpOrg\Requests\Exception\Http\Status404;
$d = new CaseInsensitiveDictionary(['Content-Type' => 'text/html']);
var_dump($d['content-type'], isset($d['CONTENT-TYPE']), $d instanceof ArrayAccess, $d instanceof IteratorAggregate, get_class($d));
$d['X-Test'] = 'v';
foreach ($d->getAll() as $k => $v) {
	echo "$k=$v\n";
}
echo WpOrg\Requests\Exception\Http::get_class(404), "\n";
try {
	throw new Status404();
} catch (WpOrg\Requests\Exception $e) {
	echo get_class($e), ' ', $e->getCode(), ' ', $e->getMessage(), ' ', var_export($e instanceof Exception, true), "\n";
}
try {
	$d[] = 'x';
} catch (\WpOrg\Requests\Exception $e) {
	echo get_class($e), ': ', $e->getMessage(), ' ', $e->getType(), "\n";
}
var_dump(class_exists('WpOrg\Requests\Exception\Http\Status404', false), class_exists('Requests_Exception'));
`}, "main.php")
	if !strings.Contains(out, `WpOrg\Requests\Exception\Http\Status404 404 404 Not Found true`) {
		t.Fatalf("référence PHP inattendue: %q", out)
	}
}

// TestReprise7SilenceOperator compare à PHP la suppression par @ : include absent, propriété indéfinie, propagation aux fonctions appelées, gestionnaire d'erreurs appelé avec error_reporting réduit, error_get_last, remise à zéro de la région par un catch.
func TestReprise7SilenceOperator(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
function warn_inside() { $o = new stdClass; return $o->missing; }
function thrower() { throw new RuntimeException('t'); }
$r = @include 'absent.php';
var_dump($r);
$s = @include_once __DIR__ . '/absent_once.php';
var_dump($s);
$o = new stdClass;
echo @$o->nope, "|\n";
var_dump(@warn_inside());
var_dump(error_get_last()['message']);
var_dump(error_reporting(), @error_reporting());
set_error_handler(function ($no, $msg) { echo "handler($no, ", error_reporting(), "): $msg\n"; return false; });
echo @$o->viaHandler, "|\n";
restore_error_handler();
try {
	$x = @thrower();
} catch (RuntimeException $e) {
	echo "caught ", $e->getMessage(), "\n";
}
echo $o->afterCatch, "|\n";
$r = include 'absent2.php';
var_dump($r);
`}, "main.php")
}

// TestReprise7LateInheritanceThreeLevels compare à PHP des chaînes d'héritage GP -> P -> C liées à travers require_once et l'autoloader, y compris une classe du programme principal dont le parent est autochargé.
func TestReprise7LateInheritanceThreeLevels(t *testing.T) {
	t.Run("parent requis puis enfants du programme principal", func(t *testing.T) {
		parityTree(t, map[string]string{
			"gp.php": "<?php\nclass GP { public $g = 'g'; const K = 'gpk'; public function hello() { return 'gp-hello'; } public static function st() { return static::class; } }\n",
			"main.php": `<?php
require_once __DIR__ . '/gp.php';
class P extends GP { public $p = 'p'; }
class C extends P { public $c = 'c'; }
$o = new C();
var_dump($o->hello(), $o->g, $o->p, $o->c, C::K, C::st(), $o instanceof GP, get_object_vars($o), get_parent_class('P'));
`,
		}, "main.php")
	})
	t.Run("parent et enfant inclus, grand-parent autochargé", func(t *testing.T) {
		parityTree(t, map[string]string{
			"GP.php": "<?php\nclass GP { public $g = 'g'; const K = 'gpk'; public function hello() { return 'gp-hello'; } }\n",
			"pc.php": "<?php\nclass P extends GP { public $p = 'p'; public function hello() { return 'p>' . parent::hello(); } }\nclass C extends P { public $c = 'c'; }\n",
			"main.php": `<?php
spl_autoload_register(function ($c) { echo "autoload $c\n"; if (is_file(__DIR__ . "/$c.php")) require __DIR__ . "/$c.php"; });
require __DIR__ . '/pc.php';
$o = new C();
var_dump($o->hello(), $o->g, $o->p, $o->c, C::K, $o instanceof GP, get_object_vars($o));
`,
		}, "main.php")
	})
	t.Run("quatre niveaux sur trois fichiers", func(t *testing.T) {
		parityTree(t, map[string]string{
			"ggp.php": "<?php\ninterface Greets { const IK = 'ik'; public function hello(); }\nabstract class GGP implements Greets { public $gg = 'gg'; public function __destruct() {} }\n",
			"gp.php":  "<?php\nrequire_once __DIR__ . '/ggp.php';\nclass GP extends GGP { public $g = 'g'; const K = 'gpk'; public function hello() { return 'gp-hello'; } }\n",
			"p.php":   "<?php\nrequire_once __DIR__ . '/gp.php';\nclass P extends GP { public $p = 'p'; protected $shadow = 'p-shadow'; }\n",
			"main.php": `<?php
require __DIR__ . '/p.php';
class C extends P { public $c = 'c'; public function shadow() { return $this->shadow; } }
class D extends C {}
$o = new D();
var_dump($o->hello(), $o->gg, $o->g, $o->p, $o->c, $o->shadow(), D::K, D::IK, $o instanceof Greets, $o instanceof GGP, is_subclass_of($o, 'GP'), get_object_vars($o));
`,
		}, "main.php")
	})
	t.Run("grand-parent autochargé pour le programme principal", func(t *testing.T) {
		parityTree(t, map[string]string{
			"GP.php": "<?php\necho \"loading GP\\n\";\nclass GP { public $g = 'g'; const K = 'gpk'; public function hello() { return 'gp-hello'; } }\n",
			"main.php": `<?php
spl_autoload_register(function ($c) { echo "autoload $c\n"; if (is_file(__DIR__ . "/$c.php")) require __DIR__ . "/$c.php"; });
echo "start\n";
class P extends GP { public $p = 'p'; }
class C extends P { public $c = 'c'; }
$o = new C();
var_dump($o->hello(), $o->g, $o->p, $o->c, C::K, $o instanceof GP, get_object_vars($o));
`,
		}, "main.php")
	})
}

// TestReprise7ClassRedeclarationAndExternalInterface compare à PHP l'erreur fatale de redéclaration inconditionnelle (classe, interface, trait, alias) et la liaison d'une classe incluse dont l'interface est autochargée.
func TestReprise7ClassRedeclarationAndExternalInterface(t *testing.T) {
	cases := map[string]map[string]string{
		"classe": {
			"a.php":    "<?php\necho \"a\\n\";\nclass Dup {}\n",
			"g.php":    "<?php\nif (!class_exists('Dup')) { class Dup {} }\necho \"guarded\\n\";\n",
			"b.php":    "<?php\n\nclass Dup {}\necho \"b\\n\";\n",
			"main.php": "<?php\nrequire __DIR__ . '/a.php';\nrequire __DIR__ . '/g.php';\nrequire __DIR__ . '/b.php';\necho \"jamais\\n\";\n",
		},
		"interface": {
			"i1.php":   "<?php\ninterface IDup {}\n",
			"i2.php":   "<?php\ninterface IDup {}\n",
			"main.php": "<?php\nrequire __DIR__ . '/i1.php';\nrequire __DIR__ . '/i2.php';\n",
		},
		"trait": {
			"t1.php":   "<?php\ntrait TDup {}\n",
			"main.php": "<?php\nrequire __DIR__ . '/t1.php';\nrequire __DIR__ . '/t1.php';\n",
		},
		"alias": {
			"al.php":   "<?php\nclass AliasDup {}\n",
			"main.php": "<?php\nclass_alias('stdClass', 'AliasDup');\necho \"x\\n\";\nrequire __DIR__ . '/al.php';\n",
		},
		"programme principal": {
			"b.php":    "<?php\n\nclass Dup {}\n",
			"main.php": "<?php\nclass Dup {}\nrequire __DIR__ . '/b.php';\n",
		},
		"interface autochargée": {
			"Shape.php": "<?php\necho \"loading Shape\\n\";\ninterface Shape extends Countable { const SIDES = 3; public function area(); }\n",
			"tri.php": `<?php
class Tri implements Shape, Serializable {
	public function area() { return 1.5; }
	public function count(): int { return 3; }
	public function serialize() { return ''; }
	public function unserialize($d) {}
	public function __serialize(): array { return []; }
	public function __unserialize(array $d): void {}
}
echo "tri declared\n";
`,
			"main.php": `<?php
spl_autoload_register(function ($c) { echo "autoload $c\n"; if (is_file(__DIR__ . "/$c.php")) require __DIR__ . "/$c.php"; });
require __DIR__ . '/tri.php';
$t = new Tri();
var_dump($t instanceof Shape, $t instanceof Countable, is_a($t, 'Shape'), is_subclass_of($t, 'Shape'), Tri::SIDES, count($t), interface_exists('Shape', false));
`,
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) { parityTree(t, files, "main.php") })
	}
}

// TestReprise7NewSelfParentStatic compare à PHP new self, new parent et new static, qui créaient un objet de classe inconnue.
func TestReprise7NewSelfParentStatic(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
class A { public static function m() { return new self(); } public static function s() { return new static(); } }
class B extends A { public static function p() { return new parent(); } }
var_dump(get_class(A::m()), get_class(B::m()), get_class(B::s()), get_class(B::p()));
`}, "main.php")
}

// TestReprise7ByRefNativeOutputs compare à PHP les sorties par référence de natives ajoutées au lot 7, appelées depuis un script compilé.
func TestReprise7ByRefNativeOutputs(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
for ($i = 0; $i < 2; $i++) {
	unset($strong);
	$bytes = openssl_random_pseudo_bytes(8, $strong);
	var_dump(strlen($bytes), $strong);
}
$fp = fopen(__FILE__, 'r');
$wb = 'unset';
var_dump(flock($fp, LOCK_SH, $wb), $wb);
fclose($fp);
$fp = @fsockopen('127.0.0.1', 1, $errno, $errstr, 1.0);
var_dump($fp, $errno, $errstr);
$png = base64_decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==');
$info = 'unset';
$size = getimagesizefromstring($png, $info);
var_dump($size[0], $size['mime'], $info);
$ct = openssl_encrypt('secret', 'aes-128-gcm', str_repeat('k', 16), 0, str_repeat('i', 12), $tag);
var_dump(strlen($tag), openssl_decrypt($ct, 'aes-128-gcm', str_repeat('k', 16), 0, str_repeat('i', 12), $tag));
`}, "main.php")
}

// TestReprise7IniGetAllAndTimezone compare à PHP ini_get_all, les droits d'accès qui gouvernent ini_set et la propagation de date.timezone au fuseau par défaut.
func TestReprise7IniGetAllAndTimezone(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
var_dump(date_default_timezone_get());
var_dump(ini_set('date.timezone', 'Europe/Paris'), ini_get('date.timezone'), date_default_timezone_get(), date('e', 0));
var_dump(ini_set('date.timezone', 'Foo/Bar'), ini_get('date.timezone'), date_default_timezone_get());
ini_restore('date.timezone');
var_dump(ini_get('date.timezone'), date_default_timezone_get());
date_default_timezone_set('Asia/Tokyo');
var_dump(ini_set('date.timezone', 'America/New_York'), date_default_timezone_get());
var_dump(ini_get_all('date', false));
var_dump(ini_get_all('nope'));
var_dump(ini_set('sendmail_path', 'x'), ini_set('max_input_time', '5'), ini_set('arg_separator.input', ';'));
$a = ini_get_all();
var_dump($a['memory_limit'], $a['allow_url_fopen']['access'], $a['session.auto_start']['access'], $a['filter.default_flags']);
ini_set('memory_limit', '256M');
var_dump(ini_get_all(null, false)['memory_limit'], ini_get_all()['memory_limit'], ini_get_all('core', false)['memory_limit']);
var_dump(ENT_QUOTES, get_defined_constants()['ENT_QUOTES'], defined('ENT_HTML5'));
`}, "main.php")
}

// TestReprise7ExtensionsDeclaredByProbe vérifie qu'extension_loaded et get_loaded_extensions déclarent mysqli et openssl, et ne déclarent une extension sondée que si sa fonction représentative existe.
func TestReprise7ExtensionsDeclaredByProbe(t *testing.T) {
	path := writeTree(t, map[string]string{"main.php": `<?php
foreach (['mysqli' => 'mysqli_connect', 'openssl' => 'openssl_encrypt', 'fileinfo' => 'finfo_open', 'tokenizer' => 'token_get_all', 'xml' => 'xml_parser_create'] as $ext => $probe) {
	echo $ext, ' ', var_export(extension_loaded($ext), true), ' ', var_export(function_exists($probe), true), ' ', var_export(in_array($ext, get_loaded_extensions(), true), true), "\n";
}
var_dump(is_array(ini_get_all('mysqli')), MYSQLI_ASSOC, defined('MYSQLI_REPORT_STRICT'));
`}, "main.php")
	out, _, code := vmResult(t, path, nil)
	if code != 0 {
		t.Fatalf("code %d: %q", code, out)
	}
	lines := strings.Split(out, "\n")
	for _, want := range []string{"mysqli true true true", "openssl true true true"} {
		if !slices.Contains(lines, want) {
			t.Fatalf("ligne %q absente de %q", want, out)
		}
	}
	for _, line := range lines[:5] {
		f := strings.Fields(line)
		if len(f) != 4 || f[1] != f[2] || f[1] != f[3] {
			t.Fatalf("extension déclarée sans sa fonction représentative, ou l'inverse : %q", line)
		}
	}
	if !strings.HasSuffix(out, "bool(true)\nint(1)\nbool(true)\n") {
		t.Fatalf("constantes ou ini_get_all mysqli: %q", out)
	}
}

const reprise7MysqliScript = `<?php
var_dump(extension_loaded('mysqli'), function_exists('mysqli_stmt_bind_param'), MYSQLI_ASSOC, ENT_QUOTES);
mysqli_report(MYSQLI_REPORT_OFF);
$db = mysqli_connect('sqlite:' . DB_PATH);
var_dump($db instanceof mysqli, mysqli_connect_errno());
var_dump(mysqli_query($db, 'CREATE TABLE IF NOT EXISTS posts (id INTEGER PRIMARY KEY, title TEXT)'), mysqli_query($db, 'DELETE FROM posts'));
$st = mysqli_prepare($db, 'INSERT INTO posts (id, title) VALUES (?, ?)');
mysqli_stmt_bind_param($st, 'is', $id, $title);
foreach ([[1, 'un'], [2, 'deux'], [3, 'trois']] as [$id, $title]) {
	var_dump(mysqli_stmt_execute($st));
}
foreach ([4, 5] as $k) {
	$s2 = mysqli_prepare($db, 'INSERT INTO posts (id, title) VALUES (?, ?)');
	mysqli_stmt_bind_param($s2, 'is', $n, $t);
	$n = $k;
	$t = "cache$k";
	mysqli_stmt_execute($s2);
	mysqli_stmt_close($s2);
}
$res = mysqli_query($db, 'SELECT id, title FROM posts ORDER BY id');
var_dump($res instanceof mysqli_result, mysqli_num_rows($res), $res->num_rows);
while ($row = mysqli_fetch_object($res)) {
	echo $row->id, ':', $row->title, ' ', isset($row->title) ? 'set' : 'unset', "\n";
}
mysqli_free_result($res);
$st = $db->prepare('SELECT title FROM posts WHERE id = ?');
$want = 2;
$st->bind_param('i', $want);
$st->bind_result($out);
$st->execute();
$st->fetch();
echo "id $want => $out\n";
$st->free_result();
$want = 5;
$st->execute();
$st->fetch();
echo "id $want => $out\n";
var_dump($db->errno, $db->error, isset($db->errno), $db->connect_errno);
var_dump(mysqli_query($db, 'SELECT * FROM missing_table'), mysqli_errno($db) !== 0);
mysqli_report(MYSQLI_REPORT_ERROR | MYSQLI_REPORT_STRICT);
try {
	mysqli_query($db, 'SELECT * FROM missing_table');
} catch (mysqli_sql_exception $e) {
	var_dump(get_class($e), $e instanceof RuntimeException, $e->getCode() !== 0, $e->getMessage() !== '', basename($e->getFile()), $e->getLine());
}
$drv = new mysqli_driver();
var_dump($drv->report_mode === (MYSQLI_REPORT_ERROR | MYSQLI_REPORT_STRICT));
$o = new mysqli('sqlite:' . DB_PATH);
var_dump($o->connect_errno, $o->query('SELECT COUNT(*) AS n FROM posts')->fetch_assoc());
`

const reprise7MysqliWant = `bool(true)
bool(true)
int(1)
int(3)
bool(true)
int(0)
bool(true)
bool(true)
bool(true)
bool(true)
bool(true)
bool(true)
int(5)
int(5)
1:un set
2:deux set
3:trois set
4:cache4 set
5:cache5 set
id 2 => deux
id 5 => cache5
int(0)
string(0) ""
bool(true)
int(0)
bool(false)
bool(true)
string(20) "mysqli_sql_exception"
bool(true)
bool(true)
bool(true)
string(8) "main.php"
int(43)
bool(true)
int(0)
array(1) {
  ["n"]=>
  string(1) "5"
}
`

// TestReprise7MysqliFlowFromCompiledScript exécute depuis un script compilé le flux mysqli de wpdb sur SQLite : variables liées par référence (procédural, chemin mis en cache et méthodes), mysqli_fetch_object, propriétés natives, mysqli_sql_exception, mysqli_driver ; la fin de requête ferme les connexions de la zone.
func TestReprise7MysqliFlowFromCompiledScript(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "wp.sqlite")
	src := strings.Replace(reprise7MysqliScript, "<?php\n", "<?php\nconst DB_PATH = '"+db+"';\n", 1)
	path := writeTree(t, map[string]string{"main.php": src}, "main.php")
	z := zone.New(zone.DefaultCapacity)
	for run := range 2 {
		out, _, code := vmResult(t, path, z)
		if code != 0 || out != reprise7MysqliWant {
			t.Fatalf("passe %d, code %d\n%s", run, code, out)
		}
		if n := mysqli.OpenConnCount(z); n != 0 {
			t.Fatalf("passe %d : %d connexions mysqli ouvertes après la fin de requête", run, n)
		}
		z.Reset(nil)
	}
}

// TestReprise7ExecuteRestoresSharedProgram vérifie qu'une même VM exécutée deux fois par sa méthode Execute retrouve le programme partagé d'origine : sans restauration, la seconde requête voit les classes incluses par la première.
func TestReprise7ExecuteRestoresSharedProgram(t *testing.T) {
	path := writeTree(t, map[string]string{
		"lib.php":  "<?php\nclass Lib { public function hi() { return 'hi'; } }\nfunction lib_f() { return 'f'; }\n",
		"main.php": "<?php\nvar_dump(class_exists('Lib', false));\nrequire_once __DIR__ . '/lib.php';\necho (new Lib())->hi(), lib_f(), \"\\n\";\n",
	}, "main.php")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.CompileSource(string(src), path)
	if err != nil {
		t.Fatal(err)
	}
	z := zone.New(zone.DefaultCapacity)
	var out bytes.Buffer
	v := New(z, &out, ioDiscard{}, prog, nil)
	for run := range 2 {
		out.Reset()
		if code := v.Execute(); code != 0 || out.String() != "bool(false)\nhif\n" {
			t.Fatalf("passe %d, code %d : %q", run, code, out.String())
		}
		if v.Program != prog {
			t.Fatalf("passe %d : la VM garde la copie du programme après la fin de requête", run)
		}
	}
}

// TestReprise7PanicStillFinishesRequest provoque une panique Go au milieu d'un script et vérifie que les rappels d'arrêt s'exécutent et que la fin de requête ferme la connexion mysqli et vide les nettoyages de la zone.
func TestReprise7PanicStillFinishesRequest(t *testing.T) {
	dir := t.TempDir()
	src := "<?php\nregister_shutdown_function(function () { echo \"shutdown\\n\"; });\n$db = mysqli_connect('sqlite:" + filepath.Join(dir, "p.sqlite") + "');\necho \"before\\n\";\n"
	prog, err := compiler.CompileSource(src, filepath.Join(dir, "main.php"))
	if err != nil {
		t.Fatal(err)
	}
	// Une instruction OpConst dont l'opérande dépasse la table des constantes fait paniquer la boucle d'exécution juste avant le retour final.
	code := prog.Main.Code
	last := code[len(code)-1]
	prog.Main.Code = append(code[:len(code)-1:len(code)-1], bytecode.Instr{Op: bytecode.OpConst, Op1: 1 << 20, Line: last.Line}, last)
	prog.Main.Aux = append(prog.Main.Aux, bytecode.NoCV)
	z := zone.New(zone.DefaultCapacity)
	var out bytes.Buffer
	if got := Execute(z, &out, ioDiscard{}, prog, nil); got != 255 {
		t.Fatalf("code %d : %q", got, out.String())
	}
	if !strings.Contains(out.String(), "before\n") || !strings.Contains(out.String(), "internal error") || !strings.HasSuffix(out.String(), "shutdown\n") {
		t.Fatalf("sortie après panique : %q", out.String())
	}
	if n := mysqli.OpenConnCount(z); n != 0 {
		t.Fatalf("%d connexions mysqli ouvertes après la panique", n)
	}
	if n := z.PendingCleanups(); n != 0 {
		t.Fatalf("%d nettoyages de zone en attente après la panique", n)
	}
}

// programShape résume l'état observable d'un programme partagé : tranches, prototypes, membres et métadonnées résolues.
func programShape(p *bytecode.Program) string {
	var b strings.Builder
	fmt.Fprintf(&b, "funcs=%d classes=%d traits=%d main=%d/%d\n", len(p.Funcs), len(p.Classes), len(p.Traits), len(p.Main.Code), p.Main.FuncID)
	for _, list := range [][]*bytecode.ClassProto{p.Classes, p.Traits} {
		for _, c := range list {
			fmt.Fprintf(&b, "%p %s id=%d parent=%d/%s m=%d p=%d rp=%d n=%d r=%v i=%v t=%v\n", c, c.Name, c.ID, c.ParentID, c.ParentName, len(c.Methods), len(c.Props), len(c.ResolvedProps), c.InstancePropCount, c.PropsResolved, c.Interfaces, c.Traits)
		}
	}
	for _, fn := range p.Funcs {
		if fn != nil {
			fmt.Fprintf(&b, "%s %d %d\n", fn.Name, fn.FuncID, len(fn.Code))
		}
	}
	return b.String()
}

// TestReprise7ConcurrentDynamicRequireSharedProgram exécute un même *bytecode.Program depuis huit goroutines dont chacune inclut dynamiquement des classes, des traits, des interfaces et des classes liées tardivement, sur des zones recyclées ; la sortie reste celle de PHP et le programme partagé reste inchangé.
func TestReprise7ConcurrentDynamicRequireSharedProgram(t *testing.T) {
	path := writeTree(t, map[string]string{
		"ggp.php":  "<?php\ninterface Greets { const IK = 'ik'; public function hello(); }\nabstract class GGP implements Greets { public $gg = 'gg'; }\n",
		"gp.php":   "<?php\nrequire_once __DIR__ . '/ggp.php';\ntrait Tags { public $tag = 't'; public function tagged() { return static::class . '#' . $this->tag; } }\nclass GP extends GGP { use Tags; public $g = 'g'; public function hello() { return 'gp-hello'; } }\n",
		"p.php":    "<?php\nrequire_once __DIR__ . '/gp.php';\nclass P extends GP { public $p = 'p'; }\n",
		"Auto.php": "<?php\nnamespace Ns;\nclass Auto extends \\P { public function hello() { return 'auto>' . parent::hello(); } }\n",
		"main.php": `<?php
spl_autoload_register(function ($c) { $f = __DIR__ . '/' . basename(str_replace('\\', '/', $c)) . '.php'; if (is_file($f)) require $f; });
require_once __DIR__ . '/p.php';
class C extends P { public $c = 'c'; }
$o = new C();
$a = new Ns\Auto();
echo $o->hello(), ' ', $o->tagged(), ' ', $a->hello(), ' ', $a->tagged(), ' ', C::IK, "\n";
var_dump($o instanceof Greets, $a instanceof GGP, get_object_vars($o), count(get_object_vars($a)));
`,
	}, "main.php")
	want, _, wantCode := phpResult(t, path)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.CompileSource(string(src), path)
	if err != nil {
		t.Fatal(err)
	}
	before := programShape(prog)
	var wg sync.WaitGroup
	errc := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			z := zone.New(zone.DefaultCapacity)
			for run := range 10 {
				var out bytes.Buffer
				code := Execute(z, &out, ioDiscard{}, prog, nil)
				z.Reset(nil)
				if code != wantCode || out.String() != want {
					errc <- fmt.Sprintf("passe %d code %d: %q", run, code, out.String())
					return
				}
			}
		})
	}
	wg.Wait()
	close(errc)
	for msg := range errc {
		t.Fatalf("écart à PHP %q : %s", want, msg)
	}
	if after := programShape(prog); after != before {
		t.Fatalf("le programme partagé a changé\navant:\n%s\naprès:\n%s", before, after)
	}
}

// TestReprise7PerZoneStateResetBetweenRequests vérifie sur une zone recyclée que les états par zone de stdlib (dernière erreur, chemin d'inclusion) et d'ext (encodage interne mbstring) ne survivent pas à la requête : la fin de requête dispatche leurs nettoyages.
func TestReprise7PerZoneStateResetBetweenRequests(t *testing.T) {
	first := writeTree(t, map[string]string{"main.php": `<?php
$o = new stdClass;
echo @$o->missing;
var_dump(mb_internal_encoding('ISO-8859-1'), mb_internal_encoding(), error_get_last()['message']);
`}, "main.php")
	second := writeTree(t, map[string]string{"main.php": `<?php
var_dump(mb_internal_encoding(), error_get_last());
`}, "main.php")
	z := zone.New(zone.DefaultCapacity)
	out, _, code := vmResult(t, first, z)
	if code != 0 || out != "bool(true)\nstring(10) \"ISO-8859-1\"\nstring(38) \"Undefined property: stdClass::$missing\"\n" {
		t.Fatalf("première requête, code %d : %q", code, out)
	}
	z.Reset(nil)
	out, _, code = vmResult(t, second, z)
	if code != 0 || out != "string(5) \"UTF-8\"\nNULL\n" {
		t.Fatalf("seconde requête sur la zone recyclée, code %d : %q", code, out)
	}
}

// TestWordPressBootAndQuery exécute wp-settings.php de la copie WordPress non modifiée sur une base SQLite atteinte par mysqli et wpdb,
// crée les tables d'options et d'articles, puis écrit et relit par l'API WordPress ; une seconde requête rejoue le tout sur la zone recyclée.
func TestWordPressBootAndQuery(t *testing.T) {
	wp := wordpressSrc(t)
	scriptFor := func(dbPath string) string {
		return writeTree(t, map[string]string{"main.php": `<?php
define('ABSPATH', '` + wp + `/');
define('DB_NAME', 'wp');
define('DB_USER', 'root');
define('DB_PASSWORD', '');
define('DB_HOST', 'sqlite:` + dbPath + `');
define('WP_INSTALLING', true);
$table_prefix = 'wp_';
$_SERVER['HTTP_HOST'] = 'localhost';
$_SERVER['REQUEST_URI'] = '/';
require ABSPATH . 'wp-settings.php';
echo "settings termine\n";
var_dump(is_string($wp_version) && $wp_version !== '', $wpdb instanceof wpdb, $wpdb->options, $wpdb->posts);
var_dump($wpdb->query("CREATE TABLE IF NOT EXISTS {$wpdb->options} (
	option_id bigint(20) unsigned NOT NULL auto_increment,
	option_name varchar(191) NOT NULL default '',
	option_value longtext NOT NULL,
	autoload varchar(20) NOT NULL default 'yes',
	PRIMARY KEY  (option_id),
	UNIQUE KEY option_name (option_name),
	KEY autoload (autoload)
) DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_520_ci"), $wpdb->last_error);
var_dump($wpdb->query("CREATE TABLE IF NOT EXISTS {$wpdb->posts} (
	ID bigint(20) unsigned NOT NULL auto_increment,
	post_author bigint(20) unsigned NOT NULL default '0',
	post_date datetime NOT NULL default '0000-00-00 00:00:00',
	post_title text NOT NULL,
	post_content longtext NOT NULL,
	post_status varchar(20) NOT NULL default 'publish',
	post_type varchar(20) NOT NULL default 'post',
	PRIMARY KEY  (ID),
	KEY type_status_date (post_type,post_status,post_date,ID)
) DEFAULT CHARACTER SET utf8mb4"), $wpdb->last_error);
delete_option('php127_probe');
var_dump(add_option('php127_probe', array('a' => 1, 'b' => 'deux')));
$o = get_option('php127_probe');
var_dump($o['b']);
var_dump(update_option('php127_probe', 'trois'));
wp_cache_flush();
var_dump(get_option('php127_probe'));
var_dump($wpdb->get_var($wpdb->prepare("SELECT option_value FROM {$wpdb->options} WHERE option_name = %s", 'php127_probe')));
var_dump($wpdb->insert($wpdb->posts, array('post_title' => 'Bonjour', 'post_content' => 'Corps', 'post_date' => '2026-10-15 10:00:00')));
$id = $wpdb->insert_id;
$row = $wpdb->get_row($wpdb->prepare("SELECT post_title, post_status FROM {$wpdb->posts} WHERE ID = %d", $id));
var_dump($row->post_title, $row->post_status);
add_filter('php127_titre', function ($t) { return strtoupper($t); });
var_dump(apply_filters('php127_titre', $row->post_title));
add_action('php127_action', function ($n) { echo "action $n\n"; });
do_action('php127_action', 7);
var_dump(did_action('php127_action'), did_action('init'));
`}, "main.php")
	}
	want := "settings termine\n" +
		"bool(true)\nbool(true)\nstring(10) \"wp_options\"\nstring(8) \"wp_posts\"\n" +
		"bool(true)\nstring(0) \"\"\nbool(true)\nstring(0) \"\"\n" +
		"bool(true)\nstring(4) \"deux\"\nbool(true)\nstring(5) \"trois\"\nstring(5) \"trois\"\n" +
		"int(1)\nstring(7) \"Bonjour\"\nstring(7) \"publish\"\nstring(7) \"BONJOUR\"\naction 7\nint(1)\nint(1)\n"
	// Cache froid à la première requête : les patrons compilés par un autre test ne faussent pas la mesure.
	compiledIncludeMu.Lock()
	clear(compiledIncludeCache)
	compiledIncludeMu.Unlock()
	z := zone.New(64 << 20)
	var durations [2]time.Duration
	var templates map[string]*compiledInclude
	for run := range 2 {
		if run > 0 {
			z.Reset(nil)
		}
		// Chaque requête part d'une base neuve : WordPress refuse une base où seules les tables du test existent déjà.
		path := scriptFor(filepath.Join(t.TempDir(), "wp.sqlite"))
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		prog, err := compiler.CompileSource(string(src), path)
		if err != nil {
			t.Fatal(err)
		}
		// Le contexte CLI construit $_SERVER (PHP_SELF, SCRIPT_FILENAME) comme php en ligne de commande.
		ctx := NewContext()
		ctx.CLIArgv = []string{path}
		var stdout, stderr bytes.Buffer
		start := time.Now()
		code := Execute(z, &stdout, &stderr, prog, ctx)
		durations[run] = time.Since(start)
		out, errOut := stdout.String(), stderr.String()
		if code != 0 || out != want || errOut != "" {
			t.Fatalf("requête %d : code %d, stderr %q\nstdout %q\nattendu %q", run, code, errOut, out, want)
		}
		compiledIncludeMu.RLock()
		snapshot := maps.Clone(compiledIncludeCache)
		compiledIncludeMu.RUnlock()
		if run == 0 {
			templates = snapshot
			continue
		}
		// La seconde requête lie des clones des patrons de la première : aucun fichier de WordPress n'est recompilé.
		if len(snapshot) != len(templates) {
			t.Fatalf("patrons : %d après la requête 2, %d après la requête 1", len(snapshot), len(templates))
		}
		for name, tmpl := range templates {
			if snapshot[name] != tmpl {
				t.Fatalf("%s recompilé à la requête 2", name)
			}
		}
	}
	root, err := filepath.EvalSymlinks(wp)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"wp-settings.php", "wp-includes/plugin.php", "wp-includes/class-wpdb.php"} {
		if templates[filepath.Join(root, rel)] == nil {
			t.Fatalf("%s absent du cache des programmes compilés", filepath.Join(root, rel))
		}
	}
	// Coût comparé, sur les mêmes fichiers, du clone d'un patron et de l'analyse suivie de la compilation qu'il évite.
	var cloneTotal, compileTotal time.Duration
	for name, tmpl := range templates {
		start := time.Now()
		_ = cloneCompiledInclude(tmpl.prog)
		cloneTotal += time.Since(start)
		src, err := readSourceFile(name)
		if err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		file, err := parser.Parse(src, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := compiler.Compile(file); err != nil {
			t.Fatal(err)
		}
		compileTotal += time.Since(start)
	}
	t.Logf("requête 1 (cache froid) %v, requête 2 (cache chaud) %v ; %d fichiers : clone %v (%v par fichier), analyse et compilation %v",
		durations[0], durations[1], len(templates), cloneTotal, cloneTotal/time.Duration(len(templates)), compileTotal)
	if cloneTotal >= compileTotal {
		t.Fatalf("clone %v, analyse et compilation %v : le cache n'économise rien", cloneTotal, compileTotal)
	}
}

// TestReprise8IssetGlobalAndCasts compare à PHP les verrous levés pour WordPress : isset sur une globale jamais
// affectée mais partagée par un fichier inclus, (array) sur un objet, (string) dans un foreach, sprintf
// positionnel, compteur de str_replace et str_ireplace, écriture imbriquée sur null, json_last_error.
func TestReprise8IssetGlobalAndCasts(t *testing.T) {
	parityTree(t, map[string]string{
		"settings.php": `<?php
function require_db() {
	global $db;
	if (isset($db)) { echo "deja\n"; return; }
	$db = new stdClass;
	echo "cree\n";
}
require_db();
require_db();
var_dump(isset($db), $db instanceof stdClass);
var_dump(isset($jamais), array_key_exists('jamais', $GLOBALS));
`,
		"main.php": `<?php
require __DIR__ . '/settings.php';
class P { private $a = 1; protected $b = 2; public $c = 3; public $d; public int $e; }
$p = new P; $p->f = 4;
var_dump(array_map('bin2hex', array_keys((array) $p)), count((array) function () {}), (array) new ArrayObject([5]));
function files($m) { $t = []; $t[] = $m; foreach ($t as $x) { $x = (string) $x; } return $x; }
for ($i = 0; $i < 3; $i++) { var_dump(files('a' . $i)); }
printf("[%2\$s %1\$05d %s %1\$s]\n", 7, 'x');
$n = 9; var_dump(str_replace(['a', 'n'], ['1', '2'], ['banana', 'x'], $n), $n);
$n = 9; var_dump(str_ireplace('A', 'z', 'aAa', $n), $n);
$s = '%0%0%0d'; $c = 1; while ($c) { $s = str_replace(['%0', '%'], '', $s, $c); } var_dump($s);
function feature($p, $f) { global $features; $features[$p][$f] = true; }
feature('post', 'title'); feature('post', 'editor');
$h = null; $h['x']['y'] = 1;
var_dump($features, $h);
var_dump(json_decode('{bad'), json_last_error(), json_last_error_msg(), json_decode('[1]'), json_last_error(), JSON_ERROR_NONE);
var_dump(INI_USER, INI_PERDIR, INI_SYSTEM, INI_ALL);
`}, "main.php")
}

// TestReprise8DeferredConstants compare à PHP les expressions constantes que la compilation ne peut résoudre :
// constante globale, constante d'une classe d'un autre fichier, new dans une valeur par défaut, erreur levée.
func TestReprise8DeferredConstants(t *testing.T) {
	parityTree(t, map[string]string{
		"base.php": `<?php
class Base { const X = 3; const PREFIX = 'wp_'; }
`,
		"main.php": `<?php
const K = 5;
define('D', 7);
require __DIR__ . '/base.php';
class B extends Base { const Y = Base::X + 1; const Z = K * 2; const T = self::PREFIX . 'options'; public $p = D; public $q = [K, 'k' => D]; }
function f($a = K, $b = D, $c = Base::X, $d = PHP_INT_SIZE, $e = E_ALL, $g = [K, 'k' => D], $o = new ArrayObject([1, 2])) {
	var_dump($a, $b, $c, $d, $e, $g, count($o));
}
f();
var_dump(B::Y, B::Z, B::T, (new B)->p, (new B)->q);
class E { const Q = NOPE; }
try { echo E::Q; } catch (Error $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
function h($x = NOPE3) { return $x; }
try { h(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump(h(1), function_exists('{constant}'));
`}, "main.php")
}

// TestReprise8IncludedInterfaceMissing compare à PHP le refus d'une classe incluse dont l'interface reste
// introuvable après l'autoloader, sans second appel de l'autoloader.
func TestReprise8IncludedInterfaceMissing(t *testing.T) {
	parityTree(t, map[string]string{
		"lost.php": `<?php
class Lost implements MissingIface {}
`,
		"main.php": `<?php
spl_autoload_register(function ($c) { echo "autoload $c\n"; });
try { require __DIR__ . '/lost.php'; } catch (Error $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
var_dump(class_exists('Lost', false));
`}, "main.php")
}

// TestReprise8IncludedInterfaceMethodMissing compare à PHP l'erreur fatale d'une classe incluse qui omet une
// méthode d'une interface autochargée, et la dernière erreur lue par une fonction d'arrêt.
func TestReprise8IncludedInterfaceMethodMissing(t *testing.T) {
	parityTree(t, map[string]string{
		"need.php": `<?php
interface Need { public function run(); public function stop(); }
`,
		"bad.php": `<?php
class Bad implements Need { public function run() {} }
`,
		"main.php": `<?php
register_shutdown_function(function () { $e = error_get_last(); var_dump($e['type'], $e['message']); });
spl_autoload_register(function ($c) { echo "autoload $c\n"; require __DIR__ . '/need.php'; });
require __DIR__ . '/bad.php';
echo "jamais\n";
`}, "main.php")
}

// TestReprise8UncaughtRecordedForShutdown compare à PHP la dernière erreur d'une exception non interceptée.
func TestReprise8UncaughtRecordedForShutdown(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
register_shutdown_function(function () { var_dump(error_get_last()); });
function f() { throw new RuntimeException('boom'); }
f();
`}, "main.php")
}

// TestReprise8DiagnosticsThroughHandler compare à PHP les diagnostics de lecture : clé absente, offset de
// chaîne, conversions de flottants et décrément de null passent par @, set_error_handler et error_get_last.
func TestReprise8DiagnosticsThroughHandler(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
$a = []; echo $a['x']; echo $a['5'], $a[-3], $a[null], $a[true];
$s = 'ab'; echo $s[5];
$z = null; $z--;
$arr = [1, 2]; [$p, $q, $r] = $arr;
$n = null; @$n['x']; var_dump(error_get_last()['message']);
@$a['k']; var_dump(error_get_last()['message']);
set_error_handler(function ($t, $m) { echo "H[$t]: $m\n"; return true; });
echo $a['y']; echo $s[9];
$f2 = 2.5; $arr[$f2] ?? 0; echo $arr[1.7], "\n";
$y = null; $y--;
$b['k']['j'] = 1; $c = $b['k']['z'] ?? 4;
echo isset($a['w']) ? 1 : 0, empty($a['w']) ? 1 : 0, "\n";
`}, "main.php")
}

// TestReprise8DynamicByRefCalls compare à PHP la transmission par référence décidée à l'exécution : appel par
// nom ou par variable, fermeture, méthode, méthode statique, constructeur, fonction déclarée dans un fichier inclus.
func TestReprise8DynamicByRefCalls(t *testing.T) {
	parityTree(t, map[string]string{
		"lib.php": `<?php
function lib_parse($s, &$out) { $out = explode(',', $s); }
`,
		"main.php": `<?php
require __DIR__ . '/lib.php';
lib_parse('a,b', $parts); var_dump($parts);
function inc(&$x, $d = 1) { $x += $d; }
$a = 1; $f = 'inc'; $f($a); $g = 'INC'; $g($a, 5); var_dump($a);
$c = function (&$y) { $y .= '!'; }; $s = 'a'; $c($s); var_dump($s);
$arr = ['k' => 1]; $f($arr['k']); var_dump($arr);
class K { static function m(&$z) { $z = 'm'; } function n(&$z) { $z = 'n'; } function __construct(&$c = null) { $c = 'ctor'; } }
$m = 'K::m'; $q = 0; $m($q); var_dump($q);
$cb = [new K, 'n']; $cb($q); var_dump($q);
$o = new K($cv); var_dump($cv);
$o->n($nn); var_dump($nn);
K::m($sm); var_dump($sm);
$arr2 = []; $o->n($arr2['x']['y']); var_dump($arr2);
$std = new stdClass; $o->n($std->p); var_dump($std);
$sort = 'sort'; $l = [3, 1]; $sort($l); var_dump($l);
$u = []; echo strlen($u['missing'] ?? 'z'), strlen($s), count($l), "\n";
`}, "main.php")
}

// TestReprise8ArraySpreadAndReferences compare à PHP le déploiement dans un littéral de tableau, le parcours
// des générateurs par iterator_to_array, les éléments par référence et call_user_func_array avec références.
func TestReprise8ArraySpreadAndReferences(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
$a = [1, 2];
echo json_encode([0, ...$a, 3]), json_encode([...['x' => 1, 7], ...[5], ...['x' => 2]]), "\n";
function gen() { yield 'k' => 1; yield 2; return 3; }
echo json_encode([...gen(), ...new ArrayIterator([9])]), "\n";
echo json_encode(iterator_to_array(gen())), json_encode(iterator_to_array(gen(), false)), iterator_count(gen()), "\n";
$g = gen(); foreach ($g as $k => $v) { echo "$k=$v "; } echo $g->getReturn(), "\n";
$v = 1; $r = [&$v]; $r[0] = 5; var_dump($v);
$w = ['a' => 1]; $rr = ['k' => &$w['a']]; $rr['k'] = 9; var_dump($w);
function inc2(&$x) { $x++; } $b = 1; call_user_func_array('inc2', [&$b]); var_dump($b);
class S { public $list = []; function __construct() { $this->register([&$this]); } function register($l) { var_dump($l[0] === $this); } }
new S;
`}, "main.php")
}

// TestReprise8IniSyncExtensions compare à la sortie de PHP 8.3.6 (avec mbstring et iconv, absents de php -n)
// la synchronisation de la table INI et des réglages d'extension : ini_set de mbstring.internal_encoding change
// l'encodage effectif, iconv_set_encoding se relit par ini_get et ini_get_all, ini_restore revient à UTF-8.
func TestReprise8IniSyncExtensions(t *testing.T) {
	path := writeTree(t, map[string]string{"main.php": `<?php
var_dump(ini_get("mbstring.internal_encoding"), ini_set("mbstring.internal_encoding","ISO-8859-1"), mb_internal_encoding(), mb_strlen("é"), ini_get("mbstring.internal_encoding"));
var_dump(ini_get("iconv.internal_encoding"), iconv_set_encoding("internal_encoding","ISO-8859-1"), ini_get("iconv.internal_encoding"), ini_set("iconv.input_encoding","ASCII"), iconv_get_encoding("input_encoding"));
ini_restore("mbstring.internal_encoding");
var_dump(mb_internal_encoding(), mb_strlen("é"));
var_dump(ini_get_all("iconv", false));
`}, "main.php")
	want := "string(0) \"\"\nstring(0) \"\"\nstring(10) \"ISO-8859-1\"\nint(2)\nstring(10) \"ISO-8859-1\"\nstring(0) \"\"\nbool(true)\nstring(10) \"ISO-8859-1\"\nstring(0) \"\"\nstring(5) \"ASCII\"\nstring(5) \"UTF-8\"\nint(1)\narray(3) {\n  [\"iconv.input_encoding\"]=>\n  string(5) \"ASCII\"\n  [\"iconv.internal_encoding\"]=>\n  string(10) \"ISO-8859-1\"\n  [\"iconv.output_encoding\"]=>\n  string(0) \"\"\n}\n"
	z := zone.New(zone.DefaultCapacity)
	for run := range 2 {
		z.Reset(nil)
		got, _, code := vmResult(t, path, z)
		if code != 0 || got != want {
			t.Fatalf("passe %d : code %d\nobtenu %q\nattendu %q", run, code, got, want)
		}
	}
}

// TestReprise8IssetSharedUndefAndGlobal compare à PHP deux mécanismes distincts : isset sur une variable qu'un
// fichier inclus nomme sans l'affecter (référence partagée vers une valeur indéfinie), et global $x qui crée
// l'entrée nulle de $GLOBALS.
func TestReprise8IssetSharedUndefAndGlobal(t *testing.T) {
	parityTree(t, map[string]string{
		"inc.php": `<?php
if (false) { $y = 1; }
var_dump(isset($y));
`,
		"main.php": `<?php
function f() { require __DIR__ . '/inc.php'; var_dump(isset($y), isset($y) ? 1 : 0); }
f();
require __DIR__ . '/inc.php';
var_dump(isset($y));
function g() { global $nv; }
g();
var_dump(array_key_exists('nv', $GLOBALS), isset($GLOBALS['nv']), array_key_exists('y', $GLOBALS));
function h() { global $y; var_dump(isset($y), $y); }
h();
var_dump(array_key_exists('y', $GLOBALS), $GLOBALS['y']);
`}, "main.php")
}

// TestIncludeCompiledProgramCacheInvalidation vérifie qu'un fichier inclus modifié sur disque, par sa taille ou
// par sa seule date de modification, est recompilé et que la requête suivante exécute le nouveau code ; un
// fichier inchangé est servi par le même patron.
func TestIncludeCompiledProgramCacheInvalidation(t *testing.T) {
	path := writeTree(t, map[string]string{
		"main.php": `<?php
var_dump(include __DIR__ . '/inc.php');
var_dump(inc_version());
`,
		"inc.php": `<?php
function inc_version() { return "v1"; }
return "r1";
`,
	}, "main.php")
	inc, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(path), "inc.php"))
	if err != nil {
		t.Fatal(err)
	}
	template := func() *compiledInclude {
		compiledIncludeMu.RLock()
		defer compiledIncludeMu.RUnlock()
		return compiledIncludeCache[inc]
	}
	z := zone.New(zone.DefaultCapacity)
	runAndCompare := func(step string) *compiledInclude {
		t.Helper()
		wantOut, _, wantCode := phpResult(t, path)
		z.Reset(nil)
		gotOut, _, gotCode := vmResult(t, path, z)
		if gotOut != wantOut || gotCode != wantCode {
			t.Fatalf("%s : stdout %q, attendu %q ; code %d, attendu %d", step, gotOut, wantOut, gotCode, wantCode)
		}
		tmpl := template()
		if tmpl == nil {
			t.Fatalf("%s : %s absent du cache", step, inc)
		}
		return tmpl
	}
	first := runAndCompare("première requête")
	if again := runAndCompare("fichier inchangé"); again != first {
		t.Fatal("fichier inchangé recompilé")
	}

	// Taille modifiée.
	if err := os.WriteFile(inc, []byte("<?php\nfunction inc_version() { return \"version-2\"; }\nreturn \"retour-2\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := runAndCompare("taille modifiée")
	if second == first {
		t.Fatal("taille modifiée : patron périmé servi")
	}

	// Même taille, contenu et date de modification différents.
	info, err := os.Stat(inc)
	if err != nil {
		t.Fatal(err)
	}
	same := "<?php\nfunction inc_version() { return \"version-3\"; }\nreturn \"retour-3\";\n"
	if int64(len(same)) != info.Size() {
		t.Fatalf("taille %d, attendu %d", len(same), info.Size())
	}
	if err := os.WriteFile(inc, []byte(same), 0o644); err != nil {
		t.Fatal(err)
	}
	later := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(inc, later, later); err != nil {
		t.Fatal(err)
	}
	third := runAndCompare("date de modification seule")
	if third == second {
		t.Fatal("date de modification modifiée : patron périmé servi")
	}
}

// TestIncludeCompiledProgramCloneIsolation lie le même fichier inclus dans des requêtes qui allouent des
// identifiants de classes et de fonctions différents : chaque inclusion lie un clone du patron, dont les
// opérandes de classe, les cibles de capture, les appels de fonctions et les constantes différées sont réécrits
// sans altérer le patron ni la requête suivante.
func TestIncludeCompiledProgramCloneIsolation(t *testing.T) {
	dir := filepath.Dir(writeTree(t, map[string]string{
		"lib.php": `<?php
const LIB_SEP = '/';
interface Shape { public function area(): int; }
trait Named { public function name(): string { return static::class . LIB_SEP . self::KIND; } }
class LibError extends RuntimeException {}
class Square implements Shape {
	use Named;
	const KIND = 'carre' . LIB_SEP . PHP_INT_SIZE;
	public array $tags = [LIB_SEP => self::KIND];
	public function __construct(private int $side = 2 * PHP_INT_SIZE) {}
	public function area(): int { return lib_square($this->side); }
}
function lib_square(int $n): int { return $n * $n; }
function lib_fail(string $why = 'raison' . LIB_SEP) { throw new LibError($why); }
function lib_report(): string {
	$s = new Square(3);
	try {
		lib_fail();
	} catch (LibError $e) {
		$caught = get_class($e) . ':' . $e->getMessage();
	}
	$f = function (Shape $x) { return $x->area() + 1; };
	return implode(' ', [$s->name(), $s->area(), $f($s), (new Square)->area(), $caught, json_encode($s->tags),
		$s instanceof Shape ? 'shape' : 'non', Square::KIND]);
}
return 'lib';
`,
		"a.php": `<?php
var_dump(require __DIR__ . '/lib.php');
echo lib_report(), "\n";
`,
		"b.php": `<?php
class Avant1 {}
class Avant2 extends Avant1 {}
function avant_un() {}
function avant_deux() {}
$x = new Avant2;
var_dump(require __DIR__ . '/lib.php');
echo lib_report(), "\n";
var_dump($x instanceof Avant1, new Square instanceof Shape);
`,
	}, "a.php"))
	z := zone.New(zone.DefaultCapacity)
	for _, main := range []string{"a.php", "b.php", "a.php", "b.php"} {
		path := filepath.Join(dir, main)
		wantOut, _, wantCode := phpResult(t, path)
		z.Reset(nil)
		gotOut, _, gotCode := vmResult(t, path, z)
		if gotOut != wantOut || gotCode != wantCode {
			t.Fatalf("%s : stdout %q, attendu %q ; code %d, attendu %d", main, gotOut, wantOut, gotCode, wantCode)
		}
	}
	lib, err := filepath.EvalSymlinks(filepath.Join(dir, "lib.php"))
	if err != nil {
		t.Fatal(err)
	}
	compiledIncludeMu.RLock()
	tmpl := compiledIncludeCache[lib]
	compiledIncludeMu.RUnlock()
	if tmpl == nil {
		t.Fatal("lib.php absent du cache")
	}
	// Un patron jamais lié garde les identifiants de fonction attribués par le compilateur, que linkDynamic réattribue sur chaque clone.
	fresh, err := compiler.CompileSource(mustRead(t, lib), lib)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Funcs) != len(tmpl.prog.Funcs) {
		t.Fatalf("patron : %d fonctions, compilation neuve %d", len(tmpl.prog.Funcs), len(fresh.Funcs))
	}
	for i, fn := range tmpl.prog.Funcs {
		if fn.FuncID != fresh.Funcs[i].FuncID || !slices.Equal(fn.Code, fresh.Funcs[i].Code) || !slices.Equal(fn.Strings, fresh.Funcs[i].Strings) {
			t.Fatalf("fonction %d (%s) du patron altérée par une liaison", i, fn.Name)
		}
	}
	for i, c := range tmpl.prog.Classes {
		fc := fresh.Classes[i]
		if c.ID != fc.ID || c.ParentID != fc.ParentID || len(c.Methods) != len(fc.Methods) || len(c.Props) != len(fc.Props) {
			t.Fatalf("classe %s du patron altérée par une liaison", c.Name)
		}
		for j, m := range c.Methods {
			if m.ClassID != fc.Methods[j].ClassID || !slices.Equal(m.Func.Code, fc.Methods[j].Func.Code) {
				t.Fatalf("méthode %s::%s du patron altérée par une liaison", c.Name, m.Name)
			}
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestCloneCompiledIncludeSharesNothingMutable mute un clone comme linkDynamic et la liaison des classes le font,
// puis vérifie que le patron et un second clone n'en voient rien, et que les thunks des constantes différées du
// clone désignent ses propres OpArray.
func TestCloneCompiledIncludeSharesNothingMutable(t *testing.T) {
	src := `<?php
const K = 'k';
trait T { public function t() { return 1; } }
class A { use T; const C = K . 'c'; public $p = K; public function m($x = K . 'm') { try { return 1; } catch (A $e) { return 2; } } }
function f($y = K . 'f') { return new A; }
`
	tmpl, err := compiler.CompileSource(src, "/clone.php")
	if err != nil {
		t.Fatal(err)
	}
	reference, err := compiler.CompileSource(src, "/clone.php")
	if err != nil {
		t.Fatal(err)
	}
	cl := cloneCompiledInclude(tmpl)
	opArrays := func(p *bytecode.Program) map[*bytecode.OpArray]bool {
		set := map[*bytecode.OpArray]bool{p.Main: true}
		for _, fn := range p.Funcs {
			set[fn] = true
		}
		for _, c := range append(slices.Clone(p.Classes), p.Traits...) {
			for _, m := range c.Methods {
				set[m.Func] = true
			}
		}
		return set
	}
	inTemplate, inClone := opArrays(tmpl), opArrays(cl)
	for fn := range inClone {
		if inTemplate[fn] {
			t.Fatalf("OpArray %s partagé avec le patron", fn.Name)
		}
	}
	var a *bytecode.ClassProto
	for _, c := range cl.Classes {
		if c.Name == "A" {
			a = c
		}
	}
	if a == nil {
		t.Fatal("classe A absente du clone")
	}
	thunks := 0
	check := func(k *bytecode.Constant, where string) {
		if k != nil && k.Thunk != nil {
			thunks++
			if !inClone[k.Thunk] {
				t.Fatalf("%s : thunk hors du clone", where)
			}
		}
	}
	for _, cc := range a.Constants {
		check(cc.Initial, "constante "+cc.Name)
	}
	for _, p := range a.Props {
		check(p.Initial, "propriété "+p.Name)
	}
	for _, m := range a.Methods {
		for _, p := range m.Func.Params {
			check(p.DefaultConst, "paramètre de "+m.Name)
		}
	}
	for _, fn := range cl.Funcs {
		for _, p := range fn.Params {
			check(p.DefaultConst, "paramètre de "+fn.Name)
		}
	}
	if thunks == 0 {
		t.Fatal("aucune constante différée dans le programme d'essai")
	}
	// Mutations de linkDynamic, bindClass et flattenTraitsInto sur le clone.
	for _, fn := range append(slices.Clone(cl.Funcs), cl.Main) {
		fn.FuncID += 100
		for i := range fn.Code {
			fn.Code[i].Op1 += 7
		}
		fn.Strings = append(fn.Strings, "ajout")
		for i := range fn.TryTable {
			for k := range fn.TryTable[i].CatchTargets {
				for j := range fn.TryTable[i].CatchTargets[k].Classes {
					fn.TryTable[i].CatchTargets[k].Classes[j] += 7
				}
			}
		}
	}
	for _, c := range append(slices.Clone(cl.Classes), cl.Traits...) {
		c.ID += 7
		c.ParentID += 7
		for i := range c.Props {
			c.Props[i].ClassID += 7
		}
		for i := range c.ResolvedProps {
			c.ResolvedProps[i].ClassID += 7
		}
		for i := range c.Methods {
			c.Methods[i].ClassID += 7
			if fn := c.Methods[i].Func; fn != nil && !slices.Contains(cl.Funcs, fn) {
				for j := range fn.Code {
					fn.Code[j].Op2 += 7
				}
			}
		}
		c.Methods = append(c.Methods, bytecode.MethodProto{Name: "ajout"})
		c.Props = append(c.Props, bytecode.PropInfo{Name: "ajout"})
	}
	for _, pair := range [][2]*bytecode.Program{{tmpl, reference}, {cloneCompiledInclude(tmpl), reference}} {
		got, want := pair[0], pair[1]
		if !reflect.DeepEqual(got.Main.Code, want.Main.Code) || got.Main.FuncID != want.Main.FuncID {
			t.Fatal("programme principal altéré par les mutations du clone")
		}
		for i := range want.Funcs {
			g, w := got.Funcs[i], want.Funcs[i]
			if g.FuncID != w.FuncID || !reflect.DeepEqual(g.Code, w.Code) || !reflect.DeepEqual(g.Strings, w.Strings) || !reflect.DeepEqual(g.TryTable, w.TryTable) {
				t.Fatalf("fonction %s altérée par les mutations du clone", w.Name)
			}
		}
		for i := range want.Classes {
			g, w := got.Classes[i], want.Classes[i]
			if g.ID != w.ID || g.ParentID != w.ParentID || len(g.Methods) != len(w.Methods) || len(g.Props) != len(w.Props) {
				t.Fatalf("classe %s altérée par les mutations du clone", w.Name)
			}
			for j := range w.Methods {
				if g.Methods[j].ClassID != w.Methods[j].ClassID || !reflect.DeepEqual(g.Methods[j].Func.Code, w.Methods[j].Func.Code) || !reflect.DeepEqual(g.Methods[j].Func.TryTable, w.Methods[j].Func.TryTable) {
					t.Fatalf("méthode %s::%s altérée par les mutations du clone", w.Name, w.Methods[j].Name)
				}
			}
			for j := range w.Props {
				if g.Props[j].ClassID != w.Props[j].ClassID {
					t.Fatalf("propriété %s::%s altérée par les mutations du clone", w.Name, w.Props[j].Name)
				}
			}
		}
	}
}

// TestMainProgramClassInterfaceChecks applique aux classes du programme principal le contrôle des fichiers
// inclus : interface introuvable après autoload, méthode abstraite ou d'interface sans corps, interface fournie
// par un fichier inclus ou déclarée plus bas dans le fichier. Les cas comparés à PHP placent la déclaration
// fautive en dernière instruction, l'erreur étant tranchée au retour du programme principal.
func TestMainProgramClassInterfaceChecks(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"interface-sans-corps": {"main.php": `<?php
interface I { function m(); }
class D implements I {}
echo "jamais\n";
`},
		"abstraite-heritee": {"main.php": `<?php
abstract class B { abstract function f(); }
echo "jamais\n";
class E extends B {}
`},
		"interface-introuvable": {"main.php": `<?php
echo "avant\n";
class C implements Missing {}
`},
		"parent-introuvable": {"main.php": `<?php
echo "avant\n";
class C extends Missing {}
`},
		"interface-autochargee-incomplete": {"main.php": `<?php
spl_autoload_register(function ($n) { echo "charge $n\n"; eval("interface $n { function m(); }"); });
echo "avant\n";
class C implements Missing {}
`},
		"interface-autochargee": {"main.php": `<?php
spl_autoload_register(function ($n) { echo "charge $n\n"; eval("interface $n { function m(); }"); });
class C implements Missing { function m() { return 3; } }
var_dump((new C)->m(), new C instanceof Missing);
`},
		"interface-incluse": {
			"i.php": `<?php interface I { function m(); }`,
			"main.php": `<?php
require __DIR__ . '/i.php';
class C implements I { function m() { return 7; } }
var_dump((new C)->m(), new C instanceof I, get_class(new C));
`},
		"interface-declaree-plus-bas": {"main.php": `<?php
class C implements I { function m() { return 1; } }
interface I { function m(); }
var_dump((new C)->m());
`},
	} {
		t.Run(name, func(t *testing.T) {
			parityTree(t, files, "main.php")
		})
	}
}

// TestCallbackInvokesVMIntrinsics compare à PHP les rappels qui désignent par leur nom un intrinsèque de la VM :
// array_map, array_filter, call_user_func, call_user_func_array, usort, fonction d'arrêt, et l'exception levée
// par un intrinsèque appelé en rappel.
func TestCallbackInvokesVMIntrinsics(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
ini_set('memory_limit', '256M');
var_dump(array_map('ini_get', ['memory_limit']));
var_dump(array_filter(['strlen', 'nonexistent_fn'], 'function_exists'));
var_dump(call_user_func('ini_get', 'memory_limit'));
var_dump(call_user_func_array('ini_get', ['memory_limit']));
var_dump(array_map('error_reporting', [E_ALL]));
var_dump(call_user_func_array('call_user_func', ['strtoupper', 'abc']));
function twice($x) { return $x * 2; }
var_dump(call_user_func('call_user_func', 'twice', 21));
var_dump(array_map('get_class', [new ArrayObject([]), new stdClass]));
var_dump(array_map('is_callable', ['twice', 'nope']));
var_dump(array_map('defined', ['E_ALL', 'NOPE_CONST']));
$o = [new stdClass, new ArrayObject([])];
var_dump(array_map('count', [[1, 2], [3]]));
try { array_map('constant', ['NOPE_CONST']); } catch (Error $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
register_shutdown_function('ini_get', 'memory_limit');
register_shutdown_function(function () { echo "fin\n"; });
`}, "main.php")
}

// TestPfsockopenByRefErrorOutputs vérifie, par le chemin compilateur puis VM, que pfsockopen écrit ses deux
// paramètres de sortie dans la portée appelante, comme PHP.
func TestPfsockopenByRefErrorOutputs(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
$errno = 999; $errstr = "unchanged";
$r = @pfsockopen("unix:///nonexistent/php127.sock", -1, $errno, $errstr, 0.01);
var_dump($r, $errno !== 999 && $errno !== 0, $errstr !== "unchanged" && $errstr !== "");
function viaFunction() { $n = 7; $s = 'x'; @pfsockopen("unix:///nonexistent/php127.sock", -1, $n, $s, 0.01); return [$n !== 7, $s !== 'x']; }
var_dump(viaFunction());
`}, "main.php")
}

// TestClassDeclarationFailsBeforeSubsequentStatements compare à PHP l'arrêt au point exact d'une déclaration de
// classe dont une dépendance manque, sans toucher aux liaisons tardives que PHP accepte.
func TestClassDeclarationFailsBeforeSubsequentStatements(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"interface-absente": {"main.php": "<?php\necho \"before\\n\";\nclass C implements Missing {}\necho \"after\\n\";\n"},
		"parent-absent":     {"main.php": "<?php\necho \"before\\n\";\nclass C extends Missing {}\necho \"after\\n\";\n"},
		"parent-inclus-trop-tard": {
			"b.php":    "<?php class B2 {}\n",
			"main.php": "<?php\necho \"a\\n\";\nclass A extends B2 {}\nrequire __DIR__ . '/b.php';\necho \"after\\n\";\n",
		},
		"parent-inclus-avant": {
			"b.php":    "<?php class B2 {}\n",
			"main.php": "<?php\nrequire __DIR__ . '/b.php';\nclass A extends B2 {}\nvar_dump(new A instanceof B2);\n",
		},
		"conditionnelle":     {"main.php": "<?php\necho \"a\\n\";\nif (true) { class A extends Nope {} }\necho \"after\\n\";\n"},
		"autochargee":        {"main.php": "<?php\nspl_autoload_register(function ($c) { echo \"auto $c\\n\"; eval(\"class $c {}\"); });\necho \"a\\n\";\nclass A extends Lazy {}\necho \"after\\n\";\nvar_dump(new A instanceof Lazy);\n"},
		"parent-plus-bas":    {"main.php": "<?php\necho \"a\\n\";\nclass A extends B {}\nclass B {}\nvar_dump(new A instanceof B);\n"},
		"interface-plus-bas": {"main.php": "<?php\necho \"a\\n\";\nclass A implements I {}\ninterface I {}\nvar_dump(new A instanceof I);\n"},
		"rattrapee":          {"main.php": "<?php\nfunction f() { class Inner extends Absent {} }\ntry { f(); } catch (Error $e) { echo $e->getMessage(), \"\\n\"; }\necho \"after\\n\";\n"},
		"fichier-inclus": {
			"bad.php":  "<?php\necho \"in\\n\";\nclass Bad implements Gone {}\necho \"jamais\\n\";\n",
			"main.php": "<?php\ntry { require __DIR__ . '/bad.php'; } catch (Error $e) { echo $e->getMessage(), \"\\n\"; }\necho \"after\\n\";\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			parityTree(t, files, "main.php")
		})
	}
}

// TestAnonymousClassExtendsAndMethods compare à PHP les classes anonymes : parent déclaré dans un autre fichier
// inclus, constructeur relayé par parent::__construct, propriétés privées, interfaces, classe anonyme déclarée
// dans une fonction d'un fichier inclus (cas de wp_sanitize_html_kses), imbrication et identité de classe.
func TestAnonymousClassExtendsAndMethods(t *testing.T) {
	parityTree(t, map[string]string{
		"processor.php": `<?php
class Tag_Processor {
	protected $html;
	private $seen = 0;
	public function __construct($html) { $this->html = $html; }
	public function next() { $this->seen++; return $this->seen <= 2; }
	public function html() { return $this->html; }
}
interface Sanitizer { public function sanitize(); }
`,
		"kses.php": `<?php
function sanitize_html($content, $level) {
	$processor = new class($content, $level) extends Tag_Processor implements Sanitizer {
		private $level;
		public function __construct($html, $level) { parent::__construct($html); $this->level = $level; }
		public function sanitize() {
			$n = 0;
			while ($this->next()) { $n++; }
			return strtoupper($this->html()) . ":" . $this->level . ":" . $n;
		}
	};
	return $processor;
}
`,
		"main.php": `<?php
require __DIR__ . '/processor.php';
require __DIR__ . '/kses.php';
$p = sanitize_html('<b>x</b>', 'post');
var_dump($p->sanitize(), $p instanceof Tag_Processor, $p instanceof Sanitizer);
$q = sanitize_html('y', 'data');
var_dump(get_class($p) === get_class($q), strtok(get_class($p), "\0"), (new ReflectionClass($p))->isAnonymous());
$plain = new class { public $v = 3; public function m() { return new class { public function n() { return 'imbriquee'; } }; } };
var_dump($plain->v, $plain->m()->n(), strtok(get_class($plain), "\0"));
interface Shape { public function area(): float; }
$shape = new class(2.0) implements Shape { private float $side; public function __construct(float $side) { $this->side = $side; } public function area(): float { return $this->side ** 2; } };
var_dump($shape->area(), strtok(get_class($shape), "\0"));
var_dump($p);
`}, "main.php")
	t.Run("constructeur à propriétés promues", func(t *testing.T) {
		parityTree(t, map[string]string{"main.php": `<?php
$o = new class(42, 'ok') { public function __construct(public int $a, private string $b) {} public function b() { return $this->b; } };
var_dump($o->a, $o->b(), $o);
$r = new class(7) { public function __construct(public readonly int $v) {} };
var_dump($r->v, (new \ReflectionProperty($o, 'b'))->isPromoted());
`}, "main.php")
	})
}

// TestDynamicClassNewStaticCallConstPropInstanceof compare à PHP la classe désignée à l'exécution par une variable,
// une propriété, un élément de tableau ou un objet : new, appel statique, constante, propriété statique, instanceof,
// ::class, et les erreurs de PHP pour une valeur invalide ou une classe introuvable.
func TestDynamicClassNewStaticCallConstPropInstanceof(t *testing.T) {
	parityTree(t, map[string]string{
		"lazy.php": `<?php class Lazy { const X = 'lazy'; }`,
		"main.php": `<?php
class K { const V = 'cv'; public static $s = 'sp'; static function sm($a) { return "sm$a"; } function __construct($x = 0) { $this->x = $x; } public $x; }
class Child extends K {}
$cls = 'K';
var_dump(get_class(new $cls()), (new $cls(5))->x, $cls::sm(1), $cls::V, $cls::$s);
$o = new K;
var_dump($o instanceof $cls, $o::V, $o::sm(2), get_class(new $o));
$m = 'Missing';
var_dump($o instanceof $m);
$c2 = 'Child';
var_dump(new $c2 instanceof $cls, $o instanceof $c2);
class H { public $c = 'K'; public $arr = ['Child']; function f() { return new $this->c(); } function g() { return new $this->arr[0]; } }
var_dump(get_class((new H)->f()), get_class((new H)->g()));
$list = ['K']; var_dump(get_class(new $list[0]));
$cls::$s = 'sp2'; var_dump(K::$s, isset($cls::$s));
$cls::$s .= '+'; var_dump(K::$s);
var_dump($o::class);
$ns = '\\K'; var_dump(get_class(new $ns));
spl_autoload_register(function ($c) { echo "autoload $c\n"; if ($c === 'Lazy') require __DIR__ . '/lazy.php'; });
$auto = 'Auto1'; var_dump($o instanceof $auto);
$lazy = 'Lazy'; var_dump($lazy::X);
foreach ([fn() => (function () { $i = 5; return new $i; })(), fn() => (function () { $i = 5; return $i::sm(); })(),
	fn() => (function () { $i = 5; return $i::V; })(), fn() => (function () { $i = 5; return (new K) instanceof $i; })(),
	fn() => (function () { $i = 'Nope'; return $i::sm(); })(), fn() => (function () { $i = 'Nope'; return new $i; })(),
	fn() => (function () { $i = 'Nope'; return $i::$s; })(), fn() => (function () { $i = 'K'; return $i::class; })()] as $n => $f) {
	try { var_dump($f()); } catch (Throwable $e) { echo $n, ' ', get_class($e), ': ', $e->getMessage(), "\n"; }
}
`}, "main.php")
}

// TestArrayUnionOperator compare à PHP l'union de tableaux par + et += : clés chaînes et entières, ordre,
// partage du tableau gauche, cibles composées (variable, élément, propriété, propriété statique) et TypeError
// des opérandes mixtes.
func TestArrayUnionOperator(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
$a = ['a' => 1, 5 => 'x']; $b = ['a' => 2, 'b' => 3, 5 => 'y', 6 => 'z'];
var_dump($a + $b, $b + $a, $a + [], [] + $b);
$keep = $a; $a += $b; var_dump($a, $keep);
$data = ['user_pass' => 'p']; $user_login = 'admin'; $data = $data + compact('user_login'); var_dump($data);
$c = ['k' => ['x' => 1]]; $c['k'] += ['y' => 2]; var_dump($c);
class O { public $p = [1]; public static $s = ['s' => 1]; }
$o = new O; $o->p += [5 => 2, 0 => 9]; O::$s += ['t' => 2]; var_dump($o->p, O::$s);
$r = ['n' => 1]; $ref = &$r; $ref += ['m' => 2]; var_dump($r);
$self = [1, 2]; $self += $self; var_dump($self);
foreach ([fn() => [] + 1, fn() => 1 + [], fn() => [] + null, fn() => [] + new stdClass, fn() => [] + 1.5, fn() => [] + "a", fn() => [] - [], fn() => [1] * 2, fn() => [] % 2] as $f) {
	try { var_dump($f()); } catch (Throwable $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
}
$x = [1]; try { $x += 5; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($x);
`}, "main.php")
}

// TestNullCoalesceAssignNoWarning compare à PHP l'affectation ??= sur variable, clé absente, clé imbriquée,
// propriété absente et propriété statique : aucun diagnostic, ni à l'écran ni dans set_error_handler.
func TestNullCoalesceAssignNoWarning(t *testing.T) {
	out := parityTree(t, map[string]string{"main.php": `<?php
set_error_handler(function ($t, $m) { echo "H: $m\n"; return true; });
$x ??= 'a'; $x ??= 'ignored';
$arr = []; $arr['missing'] ??= 'b'; $arr['missing'] ??= 'ignored'; $arr['m2']['n'] ??= 'c';
$query_vars = ['p' => 1]; foreach (['p', 'name', 'page_id'] as $key) { $query_vars[$key] ??= ''; }
class Q { public static $cache; public $dyn = null; }
$q = new Q; $q->dyn ??= 'd'; Q::$cache ??= []; Q::$cache['k'] ??= 'e';
$i = 0; $keys = ['a', 'b']; $hits = []; $hits[$keys[$i++]] ??= 'once';
var_dump($x, $arr, $query_vars, $q->dyn, Q::$cache, $hits, $i);
var_dump(error_get_last());
`}, "main.php")
	if strings.Contains(out, "H: ") {
		t.Fatalf("diagnostic émis : %q", out)
	}
}

// TestExceptionTraceWithByRefArrayArg compare à PHP la trace d'une exception levée dans une fonction dont un
// paramètre par référence désigne un tableau, rattrapée puis non rattrapée.
func TestExceptionTraceWithByRefArrayArg(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
class Txt { function __toString(): string { throw new Exception('jamais appelé'); } }
function f(&$arr, $o, $n, $d, $s) { throw new Exception('boom'); }
function g(array &$list) { f($list, new Txt, 3, 1.5, 'chaine-longue-de-plus-de-quinze'); }
$a = [1, 2];
try { g($a); } catch (Exception $e) { echo $e->getMessage(), ' ', $e->getLine(), "\n"; }
$b = ['k' => [1]];
g($b);
`}, "main.php")
}

// TestHostIntrinsics compare à PHP is_resource, get_resource_type, get_class_vars et l'état du collecteur de
// cycles, puis vérifie les fonctions de fin de réponse anticipée, absentes de PHP en ligne de commande.
func TestHostIntrinsics(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
$f = fopen('php://memory', 'r+');
var_dump(is_resource($f), get_resource_type($f)); fclose($f); var_dump(is_resource($f), get_resource_type($f), is_resource(5), is_resource(null));
$c = stream_context_create(); var_dump(get_resource_type($c), is_resource($c));
try { get_resource_type(5); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
class P { public $pa = 'p'; private $pp = 'pp'; protected $pr = 'pr'; public static $ps = 'ps'; }
class V extends P { const K = 5; public $a = 1; protected $b = 2; private $c = 3; public static $s = 4; public $n; public int $t; public $arr = [1]; public $k = self::K; }
V::$s = 9;
var_dump(get_class_vars('V'), get_class_vars('stdClass'));
class W extends V { function f() { return get_class_vars('V'); } function g() { return get_class_vars('P'); } }
var_dump((new W)->f(), (new W)->g());
try { get_class_vars('Missing'); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(gc_enabled(), gc_disable(), gc_enabled(), gc_enable(), gc_enabled());
`}, "main.php")
	path := writeTree(t, map[string]string{"main.php": `<?php
var_dump(fastcgi_finish_request(), litespeed_finish_request(), function_exists('fastcgi_finish_request'));
`}, "main.php")
	out, errOut, code := vmResult(t, path, nil)
	if want := "bool(true)\nbool(true)\nbool(true)\n"; out != want || errOut != "" || code != 0 {
		t.Fatalf("stdout %q stderr %q code %d, attendu %q", out, errOut, code, want)
	}
}

// TestExecuteReleasesZoneAfterRequest vérifie qu'après Execute la table des VM actives ne retient plus la zone,
// que la VM au repos ne la référence pas, qu'elle est réutilisée par la requête suivante sur la même zone, et que
// son entrée disparaît une fois la zone collectée.
func TestExecuteReleasesZoneAfterRequest(t *testing.T) {
	prog, err := compiler.CompileSource("<?php class D { function __destruct() { echo 'd'; } } $d = new D; echo 1;", "/zone.php")
	if err != nil {
		t.Fatal(err)
	}
	z := zone.New(1 << 20)
	key := weak.Make(z)
	var out bytes.Buffer
	if code := Execute(z, &out, nil, prog, nil); code != 0 || out.String() != "1d" {
		t.Fatalf("code %d, stdout %q", code, out.String())
	}
	if _, ok := zoneVM.Load(z); ok {
		t.Fatal("zoneVM retient la zone après la requête")
	}
	raw, ok := idleVM.Load(key)
	if !ok {
		t.Fatal("aucune VM au repos pour la zone")
	}
	first := raw.(*VM)
	if first.Zone != nil || first.Out != nil {
		t.Fatal("la VM au repos référence encore sa zone ou sa sortie")
	}
	z.Reset(nil)
	out.Reset()
	if code := Execute(z, &out, nil, prog, nil); code != 0 || out.String() != "1d" {
		t.Fatalf("seconde requête : code %d, stdout %q", code, out.String())
	}
	if raw, _ := idleVM.Load(key); raw != first {
		t.Fatal("la seconde requête n'a pas réutilisé la VM de la zone")
	}
	z = nil
	for i := 0; i < 200; i++ {
		runtime.GC()
		if _, ok := idleVM.Load(key); !ok {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("zone collectée %v, entrée de la VM au repos toujours présente", key.Value() == nil)
}

// TestNativeByRefArraySeparation compare à PHP les fonctions natives qui modifient leur tableau par référence
// (tri, empilement, défilement, curseur interne) appliquées à une copie : l'original partagé reste intact, comme
// le curseur de WP_Hook::apply_filters entre deux appels sur le même crochet.
func TestNativeByRefArraySeparation(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
$s = [3, 1, 2]; $u = $s; sort($u); $r = $s; rsort($r); $w = $s; array_pop($w); var_dump($s, $u, $r, $w);
$a = [3, 1, 2]; $b = $a; array_push($b, 9); sort($b); array_shift($b); array_unshift($b, 0); var_dump($a, $b);
$c = ['k' => 2, 'j' => 1]; $d = $c; ksort($d); asort($c); var_dump(array_keys($c), array_keys($d));
$e = [1, 2, 3]; $f = $e; array_pop($f); array_splice($f, 0, 1); var_dump($e, $f);
$p = [1, 2, 3]; $q = $p; end($q); var_dump(current($p), current($q)); reset($q); next($q); var_dump(key($p), key($q));
$list = [10, 20];
for ($k = 0; $k < 2; $k++) { $it = $list; do { echo current($it), ' '; } while (false !== next($it)); echo "|\n"; }
class Hook {
	public $priorities = [10, 20]; public $iterations = [];
	function run() { $n = 0; $this->iterations[$n] = $this->priorities; $out = ''; do { $out .= current($this->iterations[$n]) . ','; } while (false !== next($this->iterations[$n])); unset($this->iterations[$n]); return $out; }
}
$h = new Hook; var_dump($h->run(), $h->run(), current($h->priorities));
function f2($v) { return !$v; }
$cb = ['f2']; var_dump(array_map('f2', [true, false]));
`}, "main.php")
	t.Run("tableau vide partagé", func(t *testing.T) {
		parityTree(t, map[string]string{"main.php": `<?php
class K { const E = []; public $p = []; public static $s = []; }
$c = K::E; array_push($c, 1); var_dump(K::E, $c);
$o = new K; $q = $o->p; array_unshift($q, 'z'); var_dump($o->p, $q);
$r = K::$s; array_push($r, 'r'); array_unshift($r, 'l'); var_dump(K::$s, $r);
function mk() { static $s = []; return $s; }
$m = mk(); array_push($m, 2); var_dump(mk(), $m);
$e1 = []; $e2 = $e1; array_push($e2, 3); array_unshift($e1, 4); var_dump($e1, $e2);
$g = array_slice([1], 1); $h = $g; array_push($h, 5); var_dump($g, $h);
$v1 = []; $v2 = $v1; next($v2); unset($v2); $v1[] = 1; $v1[] = 2; var_dump(current($v1), key($v1));
$v1 = []; $v2 = $v1; end($v2); unset($v2); $v1[] = 1; var_dump(current($v1), key($v1));
$v1 = []; $v2 = $v1; prev($v2); unset($v2); $v1[] = 1; var_dump(current($v1));
class H { public $p = []; public $it = [];
	function run() { $this->it[0] = $this->p; end($this->it[0]); unset($this->it[0]); $this->p[] = 'a'; return current($this->p); } }
$hk = new H; var_dump($hk->run());
`}, "main.php")
	})
}

// TestPregReplaceCallbackReturnedString compare à PHP preg_replace_callback quand le rappel rend une chaîne qu'il
// vient de construire ou son argument, exerce PREG_OFFSET_CAPTURE et PREG_UNMATCHED_AS_NULL, et vérifie la
// conservation de FlagTainted face à eval() sous BlockTaintedSinks.
func TestPregReplaceCallbackReturnedString(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
function cb($m) { return "[" . $m[0] . "]"; }
var_dump(preg_replace_callback("~<p>~", "cb", "<p>a"));
var_dump(preg_replace_callback("~<p>~", fn($m) => "x", "<p>a"));
var_dump(preg_replace_callback("~<p>~", fn($m) => $m[0], "<p>a"));
var_dump(preg_replace_callback("~(<[^>]*(>|$)|>)~", fn($m) => "[" . $m[0] . "]", "<p>a</p><b"));
$subject = str_repeat("<i>", 2) . "t";
var_dump(preg_replace_callback("~<(\\w)>~", fn($m) => str_repeat($m[1], 40), $subject, -1, $n), $n, $subject);
var_dump(preg_replace_callback("/(a)(b)?/", function ($m) { var_dump($m); return "X"; }, "a", -1, $c1, PREG_OFFSET_CAPTURE));
var_dump(preg_replace_callback("/(a)(b)?/", function ($m) { var_dump($m); return "X"; }, "a", -1, $c2, PREG_UNMATCHED_AS_NULL));
var_dump(preg_replace_callback("/(a)(b)?/", function ($m) { var_dump($m); return "X"; }, "a", -1, $c3, PREG_OFFSET_CAPTURE | PREG_UNMATCHED_AS_NULL));
var_dump(preg_replace_callback("/(a)(b)?(c)/", function ($m) { var_dump($m); return "Y"; }, "ac", -1, $c4, PREG_OFFSET_CAPTURE));
`}, "main.php")

	// Conservation de FlagTainted : sujet marqué sans correspondance, capture transmise au rappel, et retour marqué du rappel.
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"no_match_preserves_taint", `<?php parse_str("in=echo+'pwn';", $q); $r = preg_replace_callback('~NEVER_MATCH~', fn($m) => $m[0], $q['in']); eval($r);`},
		{"capture_preserves_taint", `<?php parse_str("in=echo+'pwn';", $q); preg_replace_callback('~.+~', function ($m) { eval($m[0]); return ''; }, $q['in']);`},
		{"callback_return_taint", `<?php parse_str("in=echo+'pwn';", $q); $r = preg_replace_callback('~x~', fn($m) => $q['in'], 'x'); eval($r);`},
		{"idn_to_ascii_info_result_taint", `<?php parse_str("in=echo+'pwn';", $q); idn_to_ascii($q['in'], 0, INTL_IDNA_VARIANT_UTS46, $info); eval($info['result']);`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			z := zone.New(1 << 20)
			defer z.Reset(nil)
			prog, err := compiler.CompileSource(tc.src, "/taint_preg_cb.php")
			if err != nil {
				t.Fatal(err)
			}
			ctx := NewContext()
			ctx.BlockTaintedSinks = true
			var stdout, stderr bytes.Buffer
			code := Execute(z, &stdout, &stderr, prog, ctx)
			if code != 255 || ctx.TaintedSinks != 1 || !strings.Contains(stdout.String(), "Tainted input rejected at OpEval") || strings.Contains(stdout.String(), "pwn") {
				t.Fatalf("code=%d sinks=%d stdout=%q stderr=%q ; refus OpEval attendu", code, ctx.TaintedSinks, stdout.String(), stderr.String())
			}
		})
	}
}

// TestUsortUasortUksortPreservesElementReferences compare à PHP les tris par rappel et array_walk sur un tableau dont
// des éléments sont liés par référence : la cellule de référence suit l'élément trié, le comparateur reçoit la
// valeur, et une écriture ultérieure dans la variable liée reste visible dans le tableau.
func TestUsortUasortUksortPreservesElementReferences(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
$x = 2; $y = 7;
$a = ['x' => &$x, 'y' => 1];
uasort($a, fn($l, $r) => $l <=> $r);
$x = 9;
var_dump($a['x'], array_keys($a));
$a['x'] = 11; var_dump($x);
$u = [&$y, 3, 5];
usort($u, fn($l, $r) => $l <=> $r);
$y = 0;
var_dump($u);
$z = 4;
$k = ['b' => &$z, 'a' => 8];
uksort($k, fn($l, $r) => strcmp($l, $r));
$z = 40;
var_dump($k);
$k['b'] = 41;
var_dump($z);
$cmp = 0; $v1 = 'b'; $v2 = 'a'; $s = [&$v1, &$v2];
usort($s, function ($l, $r) use (&$cmp) { $cmp++; return strcmp($l, $r); });
$v1 = 'B'; var_dump($s, $cmp > 0);
$w = 1; $walk = ['p' => &$w, 'q' => 2];
array_walk($walk, function (&$v, $key) { $v *= 10; });
var_dump($w, $walk);
$w = 3; var_dump($walk['p']);
$sub = ['m' => 1]; $rec = ['s' => &$sub, 't' => [2]];
array_walk_recursive($rec, function (&$v) { $v += 100; });
var_dump($sub, $rec);
`}, "main.php")
}

// TestWordPressFullInstallAndQuery exécute l'installation complète de WordPress sur SQLite sans contournement :
// tables de dbDelta, options et rôles, création et authentification d'un utilisateur dont le condensat bcrypt est
// relu en base, article à blocs enregistré à travers le kses de l'API HTML (classe anonyme dérivée de
// WP_HTML_Tag_Processor), boucle de WP_Query avec le filtre the_content et parse_blocks. Une seconde requête, après
// z.Reset(nil), rouvre la même base : un second make_db_current_silent() réussit sur les tables peuplées, et
// l'option, l'utilisateur et l'article de la première requête sont relus. Aucune erreur ni aucun avertissement sur
// stderr.
func TestWordPressFullInstallAndQuery(t *testing.T) {
	wp := wordpressSrc(t)
	dbPath := filepath.Join(t.TempDir(), "wp.sqlite")
	header := `<?php
define('ABSPATH', '` + wp + `/');
define('DB_NAME', 'wp');
define('DB_USER', 'root');
define('DB_PASSWORD', '');
define('DB_HOST', 'sqlite:` + dbPath + `');
define('WP_INSTALLING', true);
$table_prefix = 'wp_';
$_SERVER['HTTP_HOST'] = 'localhost';
$_SERVER['REQUEST_URI'] = '/';
require ABSPATH . 'wp-settings.php';
require_once ABSPATH . 'wp-admin/includes/upgrade.php';
make_db_current_silent();
var_dump($wpdb->last_error);
$tables = $wpdb->get_col('SHOW TABLES');
sort($tables);
echo implode(',', $tables), "\n";
`
	loop := `$query = new WP_Query(['post_type' => 'post', 'post_status' => 'publish']);
var_dump($query->found_posts);
while ($query->have_posts()) {
	$query->the_post();
	var_dump(get_the_title(), apply_filters('the_content', get_the_content()), get_permalink());
}
wp_reset_postdata();
`
	install := header + `populate_options();
populate_roles();
var_dump($wpdb->last_error, get_option('blogname'), get_role('administrator') instanceof WP_Role);
$user_id = wp_create_user('admin', 'secretpass123', 'admin@example.com');
var_dump(is_int($user_id) && $user_id > 0);
// WordPress 6.8 et suivants préfixent de $wp le condensat bcrypt d'un HMAC-SHA384 du mot de passe.
$hash = $wpdb->get_var($wpdb->prepare("SELECT user_pass FROM {$wpdb->users} WHERE ID = %d", $user_id));
$bcrypt = substr($hash, 3);
var_dump(substr($hash, 0, 10), strlen($hash), strlen($bcrypt));
var_dump(password_verify(base64_encode(hash_hmac('sha384', 'secretpass123', 'wp-sha384', true)), $bcrypt), wp_check_password('secretpass123', $hash, $user_id));
var_dump(password_verify('rasmuslerdorf', '$2a$07$usesomesillystringfore2uDLvp1Ii2e./U9C8sBjqp8I90dH6hi'), password_verify('rasmuslerdorF', '$2a$07$usesomesillystringfore2uDLvp1Ii2e./U9C8sBjqp8I90dH6hi'));
$user = wp_authenticate('admin', 'secretpass123');
var_dump($user instanceof WP_User, $user->user_login, is_wp_error(wp_authenticate('admin', 'mauvais')));
// Le filtre de l'API HTML est actif avant wp_insert_post : l'enregistrement traverse wp_sanitize_html_kses.
$html_api = 0;
add_filter('wp_kses_force_legacy_parser', function ($legacy) use (&$html_api) { $html_api++; return false; }, 99);
$post_id = wp_insert_post(['post_title' => 'Article Horos', 'post_content' => '<!-- wp:paragraph --><p onclick="x">Bonjour <b>monde</b> !<script>y</script></p><!-- /wp:paragraph -->', 'post_status' => 'publish', 'post_author' => $user_id]);
var_dump(is_int($post_id) && $post_id > 0, $wpdb->last_error, $html_api > 0, get_post_field('post_content', $post_id, 'raw'));
` + loop + `var_dump(wp_kses_post('<p onclick="x">a <script>c</script><a href="javascript:alert(1)">l</a></p>'));
var_dump(wp_kses('<div class="c"><img src="x.png" onerror="y"></div>', array('div' => array('class' => true))));
$blocks = parse_blocks('<!-- wp:paragraph --><p>Bonjour</p><!-- /wp:paragraph -->');
var_dump(count($blocks), $blocks[0]['blockName']);
`
	reread := header + `var_dump(get_option('blogname'), get_role('administrator') instanceof WP_Role);
$user = wp_authenticate('admin', 'secretpass123');
var_dump($user instanceof WP_User, $user->user_login, $user->user_email, is_wp_error(wp_authenticate('admin', 'mauvais')));
` + loop + `var_dump((int) $user->ID);
`
	tables := "string(0) \"\"\n" +
		"wp_commentmeta,wp_comments,wp_links,wp_options,wp_postmeta,wp_posts,wp_term_relationships,wp_term_taxonomy,wp_termmeta,wp_terms,wp_usermeta,wp_users\n"
	post := "int(1)\n" +
		"string(13) \"Article Horos\"\nstring(56) \"<p class=\"wp-block-paragraph\">Bonjour <b>monde</b> !</p>\"\nstring(21) \"http://localhost/?p=1\"\n"
	wantInstall := tables +
		"string(0) \"\"\nstring(7) \"My Site\"\nbool(true)\n" +
		"bool(true)\n" +
		"string(10) \"$wp$2y$10$\"\nint(63)\nint(60)\n" +
		"bool(true)\nbool(true)\n" +
		"bool(true)\nbool(false)\n" +
		"bool(true)\nstring(5) \"admin\"\nbool(true)\n" +
		"bool(true)\nstring(0) \"\"\nbool(true)\n" +
		"string(72) \"<!-- wp:paragraph --><p>Bonjour <b>monde</b> !</p><!-- /wp:paragraph -->\"\n" +
		post +
		"string(33) \"<p>a <a href=\"alert(1)\">l</a></p>\"\nstring(21) \"<div class=\"c\"></div>\"\n" +
		"int(1)\nstring(14) \"core/paragraph\"\n"
	wantReread := tables +
		"string(7) \"My Site\"\nbool(true)\n" +
		"bool(true)\nstring(5) \"admin\"\nstring(17) \"admin@example.com\"\nbool(true)\n" +
		post +
		"int(1)\n"
	z := zone.New(64 << 20)
	for run, step := range []struct{ src, want string }{{install, wantInstall}, {reread, wantReread}} {
		if run > 0 {
			z.Reset(nil)
		}
		path := writeTree(t, map[string]string{"main.php": step.src}, "main.php")
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		prog, err := compiler.CompileSource(string(src), path)
		if err != nil {
			t.Fatal(err)
		}
		ctx := NewContext()
		ctx.CLIArgv = []string{path}
		var stdout, stderr bytes.Buffer
		code := Execute(z, &stdout, &stderr, prog, ctx)
		if out, errOut := stdout.String(), stderr.String(); code != 0 || out != step.want || errOut != "" {
			t.Fatalf("requête %d : code %d, stderr %q\nstdout %q\nattendu %q", run, code, errOut, out, step.want)
		}
	}
}

func TestUserStreamWrapperRegisterOpenReadWriteSeekFlushClose(t *testing.T) {
	parityTree(t, map[string]string{"main.php": `<?php
class MemStream {
	public $context;
	public static array $store = [];
	public static array $events = [];
	private string $key = '';
	private int $pos = 0;

	public function stream_open(string $path, string $mode, int $options, ?string &$opened_path): bool {
		$this->key = substr($path, strpos($path, '://') + 3);
		if ($this->key === 'deny') {
			return false;
		}
		if (str_contains($mode, 'w')) {
			self::$store[$this->key] = '';
			$this->pos = 0;
		} elseif (str_contains($mode, 'a')) {
			self::$store[$this->key] = self::$store[$this->key] ?? '';
			$this->pos = strlen(self::$store[$this->key]);
		} else {
			if (!isset(self::$store[$this->key])) {
				return false;
			}
			$this->pos = 0;
		}
		self::$events[] = 'open:' . $this->key . ':' . (is_resource($this->context) ? 'ctx' : 'noctx');
		return true;
	}

	public function stream_read(int $count): string|false {
		$buf = self::$store[$this->key] ?? '';
		$chunk = substr($buf, $this->pos, $count);
		$this->pos += strlen($chunk);
		return $chunk;
	}

	public function stream_write(string $data): int {
		$buf = self::$store[$this->key] ?? '';
		$before = substr($buf, 0, $this->pos);
		$after = substr($buf, $this->pos + strlen($data));
		self::$store[$this->key] = $before . $data . $after;
		$this->pos += strlen($data);
		return strlen($data);
	}

	public function stream_eof(): bool {
		$buf = self::$store[$this->key] ?? '';
		return $this->pos >= strlen($buf);
	}

	public function stream_tell(): int {
		return $this->pos;
	}

	public function stream_seek(int $offset, int $whence = SEEK_SET): bool {
		$len = strlen(self::$store[$this->key] ?? '');
		if ($whence === SEEK_SET) {
			$target = $offset;
		} elseif ($whence === SEEK_CUR) {
			$target = $this->pos + $offset;
		} elseif ($whence === SEEK_END) {
			$target = $len + $offset;
		} else {
			return false;
		}
		if ($target < 0) {
			return false;
		}
		$this->pos = $target;
		return true;
	}

	public function stream_flush(): bool {
		self::$events[] = 'flush:' . $this->key;
		return true;
	}

	public function stream_close(): void {
		self::$events[] = 'close:' . $this->key;
	}

	public function stream_stat(): array|false {
		return false;
	}
}

var_dump(stream_wrapper_register('mem', 'MemStream'));
$ctx = stream_context_create(['mem' => ['tag' => 'v1']]);
$fp = fopen('mem://doc', 'w+', false, $ctx);
var_dump(is_resource($fp));
var_dump(fwrite($fp, 'abcdef'));
var_dump(fflush($fp));
var_dump(fseek($fp, 2, SEEK_SET), ftell($fp));
var_dump(fread($fp, 2), ftell($fp), feof($fp));
var_dump(fread($fp, 10), feof($fp));
var_dump(rewind($fp), ftell($fp), fread($fp, 6));
var_dump(fclose($fp));
$fp2 = fopen('mem://noctx', 'w+');
var_dump(fclose($fp2));
var_dump(file_put_contents('mem://quick', 'hello world', 0, $ctx));
var_dump(file_get_contents('mem://quick', false, $ctx));
var_dump(@fopen('mem://deny', 'r'));
var_dump(MemStream::$events);
`}, "main.php")
}

func TestArchtimeWordPressKsesAndHTML5EntitiesSnapshot(t *testing.T) {
	wp := wordpressSrc(t)
	types.ResetProcessZone()
	defer types.ResetProcessZone()

	bootSrc := `<?php
define('ABSPATH', '` + wp + `/');
define('WPINC', 'wp-includes');
function wp_Trigger_Stub() {}
function add_action($t, $c, $p = 10, $a = 1) {}
function add_filter($t, $c, $p = 10, $a = 1) {}
function apply_filters($t, $v) { return $v; }
function _deprecated_function($f, $v, $r = '') {}
function _deprecated_argument($f, $v, $m = '') {}
require ABSPATH . 'wp-includes/class-wp-token-map.php';
require ABSPATH . 'wp-includes/kses.php';
require ABSPATH . 'wp-includes/html-api/html5-named-character-references.php';
`
	bootProg, err := compiler.CompileSource(bootSrc, filepath.Join(wp, "archtime_boot.php"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := SnapshotBootstrap(bootProg, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := img.Globals["allowedposttags"]; !ok {
		t.Fatal("allowedposttags absent de ArchtimeImage.Globals")
	}
	if _, ok := img.Objects["html5_named_character_references"]; !ok {
		t.Fatal("html5_named_character_references absent de ArchtimeImage.Objects")
	}
	if _, ok := img.Constants["CUSTOM_TAGS"]; !ok {
		t.Fatal("CUSTOM_TAGS absent de ArchtimeImage.Constants")
	}

	readSrc := `<?php
$len1 = 0;
$tok1 = $html5_named_character_references->read_token('&amp;', 1, $len1);
$len2 = 0;
$tok2 = $html5_named_character_references->read_token('&mdash;', 1, $len2);
var_dump(
	defined('CUSTOM_TAGS'),
	CUSTOM_TAGS,
	isset($allowedposttags['a']['href']),
	$allowedposttags['a']['href'],
	isset($allowedposttags['table']['align']),
	count($allowedposttags) > 50,
	$html5_named_character_references instanceof WP_Token_Map,
	$tok1,
	$len1,
	$tok2 === "\xE2\x80\x94",
	$len2
);
`
	readProg, err := compiler.CompileSource(readSrc, "read.php")
	if err != nil {
		t.Fatal(err)
	}
	z := zone.New(4 << 20)
	ctx := NewContext()
	ctx.ArchtimeImage = img
	var out, errBuf bytes.Buffer
	if code := Execute(z, &out, &errBuf, readProg, ctx); code != 0 || errBuf.Len() != 0 {
		t.Fatalf("read request failed: code=%d stderr=%q stdout=%q", code, errBuf.String(), out.String())
	}
	wantRead := "bool(true)\nbool(false)\nbool(true)\nbool(true)\nbool(true)\nbool(true)\nbool(true)\nstring(1) \"&\"\nint(4)\nbool(true)\nint(6)\n"
	if out.String() != wantRead {
		t.Fatalf("read stdout = %q, want %q", out.String(), wantRead)
	}

	cowSrc := `<?php
$allowedposttags['a']['href'] = false;
$allowedposttags['custom_tag'] = ['id' => true];
var_dump($allowedposttags['a']['href'], isset($allowedposttags['custom_tag']));
`
	cowProg, err := compiler.CompileSource(cowSrc, "cow.php")
	if err != nil {
		t.Fatal(err)
	}
	z.Reset(nil)
	out.Reset()
	errBuf.Reset()
	if code := Execute(z, &out, &errBuf, cowProg, ctx); code != 0 || errBuf.Len() != 0 {
		t.Fatalf("cow request failed: code=%d stderr=%q", code, errBuf.String())
	}
	if out.String() != "bool(false)\nbool(true)\n" {
		t.Fatalf("cow stdout = %q", out.String())
	}

	z.Reset(nil)
	out.Reset()
	errBuf.Reset()
	if code := Execute(z, &out, &errBuf, readProg, ctx); code != 0 || errBuf.Len() != 0 {
		t.Fatalf("post-cow read request failed: code=%d stderr=%q", code, errBuf.String())
	}
	if out.String() != wantRead {
		t.Fatalf("processZone mutated after CoW: got %q, want %q", out.String(), wantRead)
	}
}

func TestWordPressImageEditorGDAndFileinfo(t *testing.T) {
	wp := wordpressSrc(t)
	tmpDir := t.TempDir()
	path := writeTree(t, map[string]string{"main.php": `<?php
define('ABSPATH', '` + wp + `/');
define('WPINC', 'wp-includes');
define('FS_CHMOD_FILE', 0644);
define('FS_CHMOD_DIR', 0755);

function __($text, $domain = 'default') { return $text; }
function _x($text, $context, $domain = 'default') { return $text; }
function apply_filters($tag, $value) { return $value; }
function do_action($tag, ...$args) {}
function wp_raise_memory_limit($context = 'image') { return '256M'; }
function wp_is_stream($path) { return str_contains($path, '://'); }
function wp_mkdir_p($target) { return is_dir($target) || mkdir($target, 0755, true); }
function wp_basename($path, $suffix = '') { return basename($path, $suffix); }
function trailingslashit($s) { return rtrim($s, '/\\') . '/'; }
function untrailingslashit($s) { return rtrim($s, '/\\'); }
function wp_get_mime_types() {
	return [
		'jpg|jpeg|jpe' => 'image/jpeg',
		'gif'          => 'image/gif',
		'png'          => 'image/png',
	];
}
function get_allowed_mime_types($user = null) { return wp_get_mime_types(); }
function wp_check_filetype($filename, $mimes = null) {
	$mimes = $mimes ?: wp_get_mime_types();
	foreach ($mimes as $ext_preg => $mime_match) {
		if (preg_match('!\.(' . $ext_preg . ')$!i', $filename, $ext_matches)) {
			return ['ext' => $ext_matches[1], 'type' => $mime_match];
		}
	}
	return ['ext' => false, 'type' => false];
}
function wp_get_default_extension_for_mime_type($mime_type) {
	$extensions = explode('|', (string) array_search($mime_type, wp_get_mime_types(), true));
	return $extensions[0] ?: false;
}
function wp_get_image_editor_output_format($filename, $mime_type) { return []; }
function wp_exif_frac2dec($str) { return 0; }
function wp_getimagesize($filename, ?array &$image_info = null) {
	return @getimagesize($filename);
}
function wp_read_image_metadata($file) { return false; }
function wp_filesize($path) { return is_file($path) ? (int) filesize($path) : 0; }
function wp_imagecreatetruecolor($width, $height) {
	$img = imagecreatetruecolor($width, $height);
	if (is_gd_image($img) && function_exists('imagealphablending') && function_exists('imagesavealpha')) {
		imagealphablending($img, false);
		imagesavealpha($img, true);
	}
	return $img;
}
function image_resize_dimensions($orig_w, $orig_h, $dest_w, $dest_h, $crop = false) {
	if ($orig_w <= 0 || $orig_h <= 0) {
		return false;
	}
	if ($dest_w <= 0 && $dest_h <= 0) {
		return false;
	}
	$dst_w = $dest_w > 0 ? min($dest_w, $orig_w) : (int) round($orig_w * ($dest_h / $orig_h));
	$dst_h = $dest_h > 0 ? min($dest_h, $orig_h) : (int) round($orig_h * ($dest_w / $orig_w));
	return [0, 0, 0, 0, (int) $dst_w, (int) $dst_h, (int) $orig_w, (int) $orig_h];
}
function is_gd_image($image) {
	return $image instanceof GdImage || (is_resource($image) && get_resource_type($image) === 'gd');
}

require_once ABSPATH . 'wp-includes/class-wp-error.php';
function is_wp_error($thing) { return $thing instanceof WP_Error; }
require_once ABSPATH . 'wp-includes/class-wp-image-editor.php';
require_once ABSPATH . 'wp-includes/class-wp-image-editor-gd.php';

$dir = '` + tmpDir + `';
$srcPng = $dir . '/source.png';
$im = imagecreatetruecolor(120, 80);
var_dump($im instanceof GdImage, is_object($im), is_resource($im), get_class($im), get_debug_type($im));
$bg = imagecolorallocate($im, 20, 40, 200);
$fg = imagecolorallocate($im, 240, 180, 30);
imagefilledrectangle($im, 0, 0, 119, 79, $bg);
imagefilledrectangle($im, 10, 10, 50, 40, $fg);
var_dump(imagepng($im, $srcPng));
imagedestroy($im);

$fi = finfo_open(FILEINFO_MIME_TYPE);
var_dump($fi instanceof finfo, is_object($fi), is_resource($fi), get_class($fi), get_debug_type($fi));
var_dump(finfo_file($fi, $srcPng));
var_dump(finfo_buffer($fi, file_get_contents($srcPng)));
var_dump(finfo_close($fi));
var_dump(mime_content_type($srcPng));

var_dump(WP_Image_Editor_GD::test());
$ed = new WP_Image_Editor_GD($srcPng);
var_dump($ed->load());
var_dump($ed->get_size());
var_dump($ed->resize(60, 40, false));
var_dump($ed->get_size());
var_dump($ed->rotate(90));
var_dump($ed->get_size());
var_dump($ed->flip(true, false));

$dstJpg = $dir . '/thumb.jpg';
$saved = $ed->save($dstJpg, 'image/jpeg');
var_dump(is_array($saved), $saved['width'], $saved['height'], $saved['mime-type'], is_file($saved['path']));
var_dump(mime_content_type($saved['path']));
var_dump(imagecreatetruecolor(9000, 9000));
$hugePngHdr = "\x89PNG\r\n\x1a\n" . pack('N', 13) . "IHDR" . pack('NN', 10000, 10000) . "\x08\x06\x00\x00\x00";
var_dump(imagecreatefromstring($hugePngHdr));
`}, "main.php")
	z := zone.New(16 << 20)
	out, errOut, code := vmResult(t, path, z)
	want := "bool(true)\nbool(true)\nbool(false)\nstring(7) \"GdImage\"\nstring(7) \"GdImage\"\nbool(true)\n" +
		"bool(true)\nbool(true)\nbool(false)\nstring(5) \"finfo\"\nstring(5) \"finfo\"\n" +
		"string(9) \"image/png\"\nstring(9) \"image/png\"\nbool(true)\nstring(9) \"image/png\"\n" +
		"bool(true)\nbool(true)\n" +
		"array(2) {\n  [\"width\"]=>\n  int(120)\n  [\"height\"]=>\n  int(80)\n}\n" +
		"bool(true)\n" +
		"array(2) {\n  [\"width\"]=>\n  int(60)\n  [\"height\"]=>\n  int(40)\n}\n" +
		"bool(true)\n" +
		"array(2) {\n  [\"width\"]=>\n  int(40)\n  [\"height\"]=>\n  int(60)\n}\n" +
		"bool(true)\n" +
		"bool(true)\nint(40)\nint(60)\nstring(10) \"image/jpeg\"\nbool(true)\n" +
		"string(10) \"image/jpeg\"\nbool(false)\nbool(false)\n"
	if code != 0 || errOut != "" || out != want {
		t.Fatalf("code=%d stderr=%q\ngot:\n%s\nwant:\n%s", code, errOut, out, want)
	}
}

func TestWordPressPclZipExtract(t *testing.T) {
	wp := wordpressSrc(t)
	tmpDir := t.TempDir()
	path := writeTree(t, map[string]string{"main.php": `<?php
define('ABSPATH', '` + wp + `/');
function mbstring_binary_safe_encoding($reset = false) {
	static $encodings = [];
	static $overloaded = null;
	if (is_null($overloaded)) {
		$overloaded = function_exists('mb_internal_encoding');
	}
	if (!$overloaded) { return; }
	if (!$reset) {
		$encoding = mb_internal_encoding();
		array_push($encodings, $encoding);
		mb_internal_encoding('ISO-8859-1');
	}
	if ($reset && $encodings) {
		$encoding = array_pop($encodings);
		mb_internal_encoding($encoding);
	}
}
function reset_mbstring_encoding() { mbstring_binary_safe_encoding(true); }

require_once ABSPATH . 'wp-admin/includes/class-pclzip.php';

$base = '` + tmpDir + `';
$srcDir = $base . '/src-plugin';
mkdir($srcDir, 0755, true);
file_put_contents($srcDir . '/plugin.php', "<?php\n// Plugin Name: Horos Test\n");
file_put_contents($srcDir . '/readme.txt', str_repeat("Horos ZIP test payload line.\n", 20));

$zipPath = $base . '/plugin.zip';
$archive = new PclZip($zipPath);
$created = $archive->create([$srcDir . '/plugin.php', $srcDir . '/readme.txt'], PCLZIP_OPT_REMOVE_PATH, $srcDir);
var_dump(is_array($created), count($created), is_file($zipPath));

$list = $archive->listContent();
var_dump(is_array($list), count($list), $list[0]['stored_filename'], $list[1]['stored_filename']);

$destDir = $base . '/extracted';
mkdir($destDir, 0755, true);
$extracted = $archive->extract(PCLZIP_OPT_PATH, $destDir);
var_dump(is_array($extracted), count($extracted));
var_dump(
	file_get_contents($destDir . '/plugin.php') === file_get_contents($srcDir . '/plugin.php'),
	file_get_contents($destDir . '/readme.txt') === file_get_contents($srcDir . '/readme.txt')
);
`}, "main.php")
	z := zone.New(16 << 20)
	out, errOut, code := vmResult(t, path, z)
	want := "bool(true)\nint(2)\nbool(true)\n" +
		"bool(true)\nint(2)\nstring(10) \"plugin.php\"\nstring(10) \"readme.txt\"\n" +
		"bool(true)\nint(2)\n" +
		"bool(true)\nbool(true)\n"
	if code != 0 || errOut != "" || out != want {
		t.Fatalf("code=%d stderr=%q\ngot:\n%s\nwant:\n%s", code, errOut, out, want)
	}
}

func TestWordPressIXRMessageAndXMLParse(t *testing.T) {
	wp := wordpressSrc(t)
	path := writeTree(t, map[string]string{"main.php": `<?php
define('ABSPATH', '` + wp + `/');
define('WPINC', 'wp-includes');
function apply_filters($tag, $value) { return $value; }
require_once ABSPATH . 'wp-includes/IXR/class-IXR-value.php';
require_once ABSPATH . 'wp-includes/IXR/class-IXR-date.php';
require_once ABSPATH . 'wp-includes/IXR/class-IXR-base64.php';
require_once ABSPATH . 'wp-includes/IXR/class-IXR-error.php';
require_once ABSPATH . 'wp-includes/IXR/class-IXR-message.php';

$xml = '<?xml version="1.0"?>
<methodCall>
  <methodName>wp.newPost</methodName>
  <params>
    <param><value><int>1</int></value></param>
    <param><value><string>admin</string></value></param>
    <param><value><struct>
      <member><name>post_title</name><value><string>Hello XML-RPC</string></value></member>
      <member><name>post_status</name><value><string>publish</string></value></member>
    </struct></value></param>
  </params>
</methodCall>';

$msg = new IXR_Message($xml);
var_dump($msg->parse());
var_dump($msg->messageType, $msg->methodName, count($msg->params));
var_dump($msg->params[0], $msg->params[1], $msg->params[2]['post_title'], $msg->params[2]['post_status']);

$p = xml_parser_create('UTF-8');
var_dump($p instanceof XMLParser, is_object($p), is_resource($p), get_class($p), get_debug_type($p));
$vals = [];
$idx = [];
var_dump(xml_parse_into_struct($p, '<feed><entry id="42"><title>Horos</title></entry></feed>', $vals, $idx));
var_dump(count($vals) > 0, isset($idx['ENTRY']), isset($idx['TITLE']));
var_dump(xml_parser_free($p));

$p2 = xml_parser_create('UTF-8');
$v2 = [];
var_dump(xml_parse_into_struct($p2, '<root a="1" a="2"/>', $v2), xml_get_error_code($p2) === XML_ERROR_DUPLICATE_ATTRIBUTE);
xml_parser_free($p2);

$p3 = xml_parser_create('UTF-8');
$events = [];
xml_set_element_handler(
	$p3,
	function ($parser, $name, $attrs) use (&$events) {
		$events[] = "open:$name";
		if ($name === 'ROOT') {
			xml_set_element_handler($parser, null, null);
			xml_parser_free($parser);
		}
	},
	function ($parser, $name) use (&$events) {
		$events[] = "close:$name";
	}
);
var_dump(xml_parse($p3, '<root><child/></root>', true), count($events));
`}, "main.php")
	z := zone.New(16 << 20)
	out, errOut, code := vmResult(t, path, z)
	want := "bool(true)\n" +
		"string(10) \"methodCall\"\nstring(10) \"wp.newPost\"\nint(3)\n" +
		"int(1)\nstring(5) \"admin\"\nstring(13) \"Hello XML-RPC\"\nstring(7) \"publish\"\n" +
		"bool(true)\nbool(true)\nbool(false)\nstring(9) \"XMLParser\"\nstring(9) \"XMLParser\"\n" +
		"int(1)\nbool(true)\nbool(true)\nbool(true)\nbool(true)\n" +
		"int(0)\nbool(true)\n" +
		"int(1)\nint(4)\n"
	if code != 0 || errOut != "" || out != want {
		t.Fatalf("code=%d stderr=%q\ngot:\n%s\nwant:\n%s", code, errOut, out, want)
	}
}

func TestWordPressSAPIHTTPInputFilesAndCurl(t *testing.T) {
	wp := wordpressSrc(t)
	uploadDestDir := t.TempDir()
	serverSrc := `<?php
$uri = $_SERVER['REQUEST_URI'] ?? '/';
$method = $_SERVER['REQUEST_METHOD'] ?? 'GET';

if ($uri === '/wp-json/wp/v2/posts' && $method === 'GET') {
	header('Content-Type: application/json; charset=UTF-8');
	header('X-WP-Total: 1');
	http_response_code(200);
	echo json_encode([['id' => 1, 'title' => ['rendered' => 'Bonjour SAPI']]]);
	return;
}

if ($uri === '/wp-json/wp/v2/posts' && $method === 'POST') {
	$raw = file_get_contents('php://input');
	$data = json_decode($raw, true);
	setcookie('wordpress_logged_in_horos', 'admin|token', 0, '/');
	header('Content-Type: application/json; charset=UTF-8');
	http_response_code(201);
	echo json_encode([
		'id'     => 2,
		'title'  => $data['title'] ?? '',
		'status' => $data['status'] ?? 'draft',
		'auth'   => $_SERVER['HTTP_AUTHORIZATION'] ?? '',
	]);
	return;
}

if ($uri === '/wp-admin/async-upload.php' && $method === 'POST') {
	header('Content-Type: application/json; charset=UTF-8');
	$f = $_FILES['async-upload'] ?? null;
	if (!$f || !is_uploaded_file($f['tmp_name'])) {
		http_response_code(400);
		echo json_encode(['error' => 'missing upload', 'files' => $_FILES, 'post' => $_POST, 'ct' => $_SERVER['CONTENT_TYPE'] ?? '']);
		return;
	}
	$mime = mime_content_type($f['tmp_name']);
	$dest = '` + uploadDestDir + `/' . basename($f['name']);
	$moved = move_uploaded_file($f['tmp_name'], $dest);
	echo json_encode([
		'moved'   => $moved,
		'name'    => $f['name'],
		'size'    => $f['size'],
		'mime'    => $mime,
		'caption' => $_POST['caption'] ?? '',
		'content' => file_get_contents($dest),
	]);
	return;
}

http_response_code(404);
echo "Not Found";
`
	serverProg, err := compiler.CompileSource(serverSrc, "wp_sapi_index.php")
	if err != nil {
		t.Fatal(err)
	}
	runner := sapi.NewProgramRunner(serverProg, func(z *zone.Zone, prog *bytecode.Program, _ *sapi.RequestContext, out io.Writer) error {
		var errBuf bytes.Buffer
		if code := Execute(z, out, &errBuf, prog, NewContext()); code != 0 {
			return fmt.Errorf("exit %d: %s", code, errBuf.String())
		}
		return nil
	})
	srv := sapi.NewServer(runner, sapi.Options{
		ZoneCapacity: 8 << 20,
		MaxWorkers:   2,
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// 1. Test client PHP utilisant ext/curl (GET + POST JSON avec php://input + WpOrg\Requests\Transport\Curl) contre le serveur SAPI.
	clientSrc := `<?php
define('ABSPATH', '` + wp + `/');
$base = '` + ts.URL + `';

$ch = curl_init($base . '/wp-json/wp/v2/posts');
var_dump($ch instanceof CurlHandle, is_object($ch), is_resource($ch), get_class($ch), get_debug_type($ch));
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
$body = curl_exec($ch);
$code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
$ct = curl_getinfo($ch, CURLINFO_CONTENT_TYPE);
curl_close($ch);
$posts = json_decode($body, true);
var_dump($code, str_contains($ct, 'application/json'), $posts[0]['id'], $posts[0]['title']['rendered']);

$ch2 = curl_init();
curl_setopt_array($ch2, [
	CURLOPT_URL            => $base . '/wp-json/wp/v2/posts',
	CURLOPT_POST           => true,
	CURLOPT_POSTFIELDS     => json_encode(['title' => 'Créé via REST', 'status' => 'publish']),
	CURLOPT_HTTPHEADER     => ['Content-Type: application/json', 'Authorization: Bearer wp-jwt-token'],
	CURLOPT_RETURNTRANSFER => true,
	CURLOPT_HEADER         => true,
]);
$rawResp = curl_exec($ch2);
$hdrSize = curl_getinfo($ch2, CURLINFO_HEADER_SIZE);
$code2 = curl_getinfo($ch2, CURLINFO_RESPONSE_CODE);
curl_close($ch2);
$respHdrs = substr($rawResp, 0, $hdrSize);
$respBody = substr($rawResp, $hdrSize);
$created = json_decode($respBody, true);
var_dump(
	$code2,
	str_contains($respHdrs, 'wordpress_logged_in_horos='),
	$created['id'],
	$created['title'],
	$created['status'],
	$created['auth']
);

require_once ABSPATH . 'wp-includes/Requests/src/Autoload.php';
WpOrg\Requests\Autoload::register();
$transport = new WpOrg\Requests\Transport\Curl();
$rawTransportResp = $transport->request(
	$base . '/wp-json/wp/v2/posts',
	['Accept' => 'application/json'],
	'',
	[
		'type'             => WpOrg\Requests\Requests::GET,
		'timeout'          => 5,
		'connect_timeout'  => 5,
		'useragent'        => 'WordPress/Horos127',
		'follow_redirects' => true,
		'blocking'         => true,
		'filename'         => false,
		'auth'             => false,
		'proxy'            => false,
		'cookies'          => false,
		'max_bytes'        => false,
		'idn'              => false,
		'verify'           => false,
		'verifyname'       => false,
		'data_format'      => 'query',
		'hooks'            => new WpOrg\Requests\Hooks(),
		'protocol_version' => 1.1,
	]
);
var_dump(
	str_contains($rawTransportResp, 'HTTP/1.1 200'),
	str_contains($rawTransportResp, 'X-Wp-Total: 1') || str_contains($rawTransportResp, 'X-WP-Total: 1'),
	str_contains($rawTransportResp, 'Bonjour SAPI')
);
`
	clientPath := writeTree(t, map[string]string{"client.php": clientSrc}, "client.php")
	z := zone.New(16 << 20)
	out, errOut, code := vmResult(t, clientPath, z)
	want := "bool(true)\nbool(true)\nbool(false)\nstring(10) \"CurlHandle\"\nstring(10) \"CurlHandle\"\n" +
		"int(200)\nbool(true)\nint(1)\nstring(12) \"Bonjour SAPI\"\n" +
		"int(201)\nbool(true)\nint(2)\nstring(15) \"Créé via REST\"\nstring(7) \"publish\"\nstring(19) \"Bearer wp-jwt-token\"\n" +
		"bool(true)\nbool(true)\nbool(true)\n"
	if code != 0 || errOut != "" || out != want {
		t.Fatalf("cURL client failed: code=%d stderr=%q\ngot:\n%s\nwant:\n%s", code, errOut, out, want)
	}

	// 2. Test d'envoi multipart/form-data ($_FILES + is_uploaded_file + move_uploaded_file + finfo).
	var mpBuf bytes.Buffer
	mw := multipart.NewWriter(&mpBuf)
	_ = mw.WriteField("caption", "Logo Horos")
	fw, err := mw.CreateFormFile("async-upload", "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("Contenu téléversé via SAPI multipart.\n"))
	_ = mw.Close()

	resp, err := http.Post(ts.URL+"/wp-admin/async-upload.php", mw.FormDataContentType(), &mpBuf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("multipart status=%d body=%s", resp.StatusCode, string(respBytes))
	}
	wantUploadJSON := `{"moved":true,"name":"hello.txt","size":41,"mime":"text\/plain","caption":"Logo Horos","content":"Contenu t\u00e9l\u00e9vers\u00e9 via SAPI multipart.\n"}`
	if string(respBytes) != wantUploadJSON {
		t.Fatalf("multipart response = %s, want %s", string(respBytes), wantUploadJSON)
	}
}

