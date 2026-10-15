package types

import (
	"fmt"
	"os/exec"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

func objectValue(t testing.TB, z *zone.Zone, n uint32) Value {
	t.Helper()
	v, err := NewObject(z, 1, n, false)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func auditRef(t testing.TB, z *zone.Zone, v Value) Value {
	t.Helper()
	r, err := NewReference(z, v)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func assertZoneEmpty(t testing.TB, z *zone.Zone) {
	t.Helper()
	if live, roots := z.LiveBytes(), z.BufferedRoots(); live != 0 || roots != 0 {
		t.Fatalf("remaining live bytes=%d roots=%d", live, roots)
	}
}

func TestAuditCountParity(t *testing.T) {
	x20, y20, k20 := strings.Repeat("x", 20), strings.Repeat("y", 20), strings.Repeat("k", 20)
	cases := []struct {
		name, php string
		build     func(*testing.T, *zone.Zone) func()
	}{
		{"self_two_strings", `class A{public $a;public $b;public $c;} $a=new A;$a->a=$a;$a->b=str_repeat("x",20);$a->c=str_repeat("y",20);unset($a);`, func(t *testing.T, z *zone.Zone) func() {
			a, s1, s2 := objectValue(t, z, 3), stringValue(t, z, x20, 0), stringValue(t, z, y20, 0)
			ObjectSet(z, a, 0, a)
			ObjectSet(z, a, 1, s1)
			ObjectSet(z, a, 2, s2)
			ReleaseString(z, &s1)
			ReleaseString(z, &s2)
			ReleaseObject(z, &a)
			return nil
		}},
		{"self_shared_string", `class A{public $a;public $b;public $c;} $a=new A;$a->a=$a;$s=str_repeat("x",20);$a->b=$s;$a->c=$s;unset($s,$a);`, func(t *testing.T, z *zone.Zone) func() {
			a, s := objectValue(t, z, 3), stringValue(t, z, x20, 0)
			ObjectSet(z, a, 0, a)
			ObjectSet(z, a, 1, s)
			ObjectSet(z, a, 2, s)
			ReleaseString(z, &s)
			ReleaseObject(z, &a)
			return nil
		}},
		{"self_string_held_outside", `class A{public $a;public $b;} $a=new A;$a->a=$a;$s=str_repeat("x",20);$a->b=$s;unset($a);`, func(t *testing.T, z *zone.Zone) func() {
			a, s := objectValue(t, z, 2), stringValue(t, z, x20, 0)
			ObjectSet(z, a, 0, a)
			ObjectSet(z, a, 1, s)
			ReleaseObject(z, &a)
			return func() {
				if StringInfo(z, s).RefCount != 1 {
					t.Fatal("outside string owner lost")
				}
				ReleaseString(z, &s)
			}
		}},
		{"array_ref_cycle_string_value", `$a=[str_repeat("x",20)];$a[1]=&$a;unset($a);`, func(t *testing.T, z *zone.Zone) func() {
			a, s := arrayValue(t, z), stringValue(t, z, x20, 0)
			arraySet(t, z, &a, NewLong(0), s)
			ReleaseString(z, &s)
			r := auditRef(t, z, a)
			ReleaseArray(z, &a)
			arraySet(t, z, &r, NewLong(1), r)
			ReleaseReference(z, &r)
			return nil
		}},
		{"array_ref_cycle_string_key", `$k=str_repeat("k",20);$a=[$k=>1];unset($k);$a[1]=&$a;unset($a);`, func(t *testing.T, z *zone.Zone) func() {
			a, k := arrayValue(t, z), stringValue(t, z, k20, 0)
			arraySet(t, z, &a, k, NewLong(1))
			ReleaseString(z, &k)
			r := auditRef(t, z, a)
			ReleaseArray(z, &a)
			arraySet(t, z, &r, NewLong(1), r)
			ReleaseReference(z, &r)
			return nil
		}},
		{"self_array_two_strings", `class A{public $a;public $b;} $a=new A;$a->a=$a;$a->b=[str_repeat("x",20),str_repeat("y",20)];unset($a);`, func(t *testing.T, z *zone.Zone) func() {
			a, b, s1, s2 := objectValue(t, z, 2), arrayValue(t, z), stringValue(t, z, x20, 0), stringValue(t, z, y20, 0)
			arraySet(t, z, &b, NewLong(0), s1)
			arraySet(t, z, &b, NewLong(1), s2)
			ReleaseString(z, &s1)
			ReleaseString(z, &s2)
			ObjectSet(z, a, 0, a)
			ObjectSet(z, a, 1, b)
			ReleaseArray(z, &b)
			ReleaseObject(z, &a)
			return nil
		}},
		{"self_ref_scalar", `class A{public $a;public $b;} $a=new A;$a->a=$a;$x=1;$a->b=&$x;unset($x,$a);`, func(t *testing.T, z *zone.Zone) func() {
			a, r := objectValue(t, z, 2), auditRef(t, z, NewLong(1))
			ObjectSet(z, a, 0, a)
			ObjectSet(z, a, 1, r)
			ReleaseReference(z, &r)
			ReleaseObject(z, &a)
			return nil
		}},
		{"self_ref_string", `class A{public $a;public $b;} $a=new A;$a->a=$a;$x=str_repeat("x",20);$a->b=&$x;unset($x,$a);`, func(t *testing.T, z *zone.Zone) func() {
			a, s := objectValue(t, z, 2), stringValue(t, z, x20, 0)
			r := auditRef(t, z, s)
			ReleaseString(z, &s)
			ObjectSet(z, a, 0, a)
			ObjectSet(z, a, 1, r)
			ReleaseReference(z, &r)
			ReleaseObject(z, &a)
			return nil
		}},
		{"self_via_ref_cell", `class A{public $a;} $a=new A;$a->a=&$a;unset($a);`, func(t *testing.T, z *zone.Zone) func() {
			o := objectValue(t, z, 1)
			r := auditRef(t, z, o)
			ReleaseObject(z, &o)
			ObjectSet(z, Deref(z, r), 0, r)
			ReleaseReference(z, &r)
			return nil
		}},
		{"self_nested_object_string", `class A{public $a;public $b;} $a=new A;$a->a=$a;$c=new A;$c->a=str_repeat("x",20);$a->b=$c;unset($c,$a);`, func(t *testing.T, z *zone.Zone) func() {
			a, c, s := objectValue(t, z, 2), objectValue(t, z, 2), stringValue(t, z, x20, 0)
			ObjectSet(z, c, 0, s)
			ReleaseString(z, &s)
			ObjectSet(z, a, 0, a)
			ObjectSet(z, a, 1, c)
			ReleaseObject(z, &c)
			ReleaseObject(z, &a)
			return nil
		}},
		{"two_cycles_one_live", `class A{public $a;} $a=new A;$a->a=$a;$b=new A;$b->a=$b;unset($a);$k=$b;unset($b);`, func(t *testing.T, z *zone.Zone) func() {
			a, b := objectValue(t, z, 1), objectValue(t, z, 1)
			ObjectSet(z, a, 0, a)
			ObjectSet(z, b, 0, b)
			ReleaseObject(z, &a)
			k := RetainObject(z, b)
			ReleaseObject(z, &b)
			return func() {
				ReleaseObject(z, &k)
				if n := CollectCycles(z); n != 1 {
					t.Fatalf("second collection=%d", n)
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			z := zone.New(1 << 16)
			after := tc.build(t, z)
			checkArrayPHP(t, tc.php+`echo gc_collect_cycles();`, fmt.Sprint(CollectCycles(z)))
			if z.BufferedRoots() != 0 {
				t.Fatal("roots retained after collection")
			}
			if after != nil {
				after()
			}
			assertZoneEmpty(t, z)
		})
	}
}

func TestAuditRootRemovedOnFree(t *testing.T) {
	for _, movedType := range []Type{TypeArray, TypeObject, TypeReference} {
		t.Run(fmt.Sprint(movedType), func(t *testing.T) {
			z := zone.New(8192)
			a := arrayValue(t, z)
			var moved Value
			switch movedType {
			case TypeArray:
				moved = arrayValue(t, z)
			case TypeObject:
				moved = objectValue(t, z, 0)
			case TypeReference:
				moved = auditRef(t, z, a)
			}
			first := objectValue(t, z, 0)
			for _, v := range []Value{first, a, moved} {
				x := RetainValue(z, v)
				ReleaseValue(z, &x)
			}
			if z.BufferedRoots() != 3 {
				t.Fatalf("roots=%d", z.BufferedRoots())
			}
			ReleaseObject(z, &first)
			ReleaseValue(z, &moved)
			ReleaseArray(z, &a)
			assertZoneEmpty(t, z)
		})
	}
}

func TestAuditRootNoDuplicate(t *testing.T) {
	z := zone.New(8192)
	a := arrayValue(t, z)
	o := objectValue(t, z, 0)
	r := auditRef(t, z, a)
	for _, v := range []Value{a, o, r} {
		for range 3 {
			x := RetainValue(z, v)
			ReleaseValue(z, &x)
		}
	}
	if z.BufferedRoots() != 3 {
		t.Fatalf("duplicate roots=%d", z.BufferedRoots())
	}
	ReleaseReference(z, &r)
	ReleaseObject(z, &o)
	ReleaseArray(z, &a)
	assertZoneEmpty(t, z)
}

func TestAuditArrayOnlyRoot(t *testing.T) {
	z := zone.New(8192)
	o, b := objectValue(t, z, 1), arrayValue(t, z)
	arraySet(t, z, &b, NewLong(0), o)
	ObjectSet(z, o, 0, b)
	ReleaseObject(z, &o)
	first := CollectCycles(z)
	ReleaseArray(z, &b)
	second := CollectCycles(z)
	checkArrayPHP(t, `$o=new stdClass;$b=[$o];$o->x=$b;unset($o);echo gc_collect_cycles(),",";unset($b);echo gc_collect_cycles();`, fmt.Sprintf("%d,%d", first, second))
	assertZoneEmpty(t, z)
}

func TestAuditArrayCoWRoot(t *testing.T) {
	z := zone.New(8192)
	o, a := objectValue(t, z, 1), arrayValue(t, z)
	arraySet(t, z, &a, NewLong(0), o)
	ObjectSet(z, o, 0, a)
	ReleaseObject(z, &o)
	if CollectCycles(z) != 0 || ArrayInfo(z, a).RefCount != 2 {
		t.Fatal("expected live cycle with two array owners")
	}
	old := a // Borrow the old identity to inspect the cycle, not an additional owner.
	arraySet(t, z, &a, NewLong(1), NewLong(7))
	if ArrayInfo(z, old).RefCount != 1 || z.BufferedRoots() != 1 {
		t.Fatalf("CoW failed to buffer old array: roots=%d", z.BufferedRoots())
	}
	ReleaseArray(z, &a)
	checkArrayPHP(t, `$o=new stdClass;$a=[$o];$o->a=$a;unset($o);gc_collect_cycles();$a[1]=7;unset($a);echo gc_collect_cycles();`, fmt.Sprint(CollectCycles(z)))
	assertZoneEmpty(t, z)
}

func TestAuditResetClearsRoots(t *testing.T) {
	z := zone.New(8192)
	a := arrayValue(t, z)
	x := RetainArray(z, a)
	ReleaseArray(z, &x)
	if z.BufferedRoots() != 1 {
		t.Fatal("missing root before reset")
	}
	z.Reset(nil)
	assertZoneEmpty(t, z)
}

func TestAuditRetiredAcrossReset(t *testing.T) {
	z := zone.New(64)
	for range 2 * 65535 {
		v := objectValue(t, z, 0)
		ReleaseObject(z, &v)
	}
	z.Reset(nil)
	if _, err := NewObject(z, 1, 0, false); err != zone.ErrExhausted {
		t.Fatalf("retired slots reused: %v", err)
	}
	assertZoneEmpty(t, z)
}

func TestAuditHandleFailureNoLeak(t *testing.T) {
	z := zone.New(64)
	for range 2 * 65535 {
		v := objectValue(t, z, 0)
		ReleaseObject(z, &v)
	}
	if _, err := NewObject(z, 1, 1, false); err != zone.ErrExhausted {
		t.Fatalf("handle failure=%v", err)
	}
	assertZoneEmpty(t, z)
}

func TestAuditPropertyRelease(t *testing.T) {
	z := zone.New(8192)
	s := stringValue(t, z, "property-owned-string", 0)
	o := objectValue(t, z, 2)
	ObjectSet(z, o, 1, s)
	ReleaseObject(z, &o)
	if StringInfo(z, s).RefCount != 1 {
		t.Fatal("property owner not released")
	}
	ReleaseString(z, &s)
	assertZoneEmpty(t, z)
}

func TestAuditRecycledPropertyBlock(t *testing.T) {
	z := zone.New(8192)
	keep := stringValue(t, z, "k", 0)
	s1, s2 := stringValue(t, z, "AAAAAAAAAAAAAAAAAAA", 0), stringValue(t, z, "BBBBBBBBBBBBBBBBBBB", 0)
	guard := stringValue(t, z, "g", 0)
	ReleaseString(z, &s1)
	ReleaseString(z, &s2)
	o := objectValue(t, z, 3)
	for i := range uint32(3) {
		if v := ObjectGet(z, o, i); !v.IsUndef() {
			t.Fatalf("property %d not cleared: %+v", i, v)
		}
	}
	ReleaseObject(z, &o)
	ReleaseString(z, &guard)
	ReleaseString(z, &keep)
	assertZoneEmpty(t, z)
}

func TestAuditPropertyWriteThroughReference(t *testing.T) {
	z := zone.New(8192)
	o := objectValue(t, z, 1)
	r := auditRef(t, z, NewLong(1))
	ObjectSet(z, o, 0, r)
	ObjectSet(z, o, 0, NewLong(5))
	checkArrayPHP(t, `class A{public $p;} $o=new A;$x=1;$o->p=&$x;$o->p=5;enc($x);`, fmt.Sprintf("i:%d;", Deref(z, r).ToLong()))
	ReleaseReference(z, &r)
	ReleaseObject(z, &o)
	assertZoneEmpty(t, z)
}

func TestAuditAssignRefSelf(t *testing.T) {
	z := zone.New(4096)
	s := stringValue(t, z, strings.Repeat("x", 20), 0)
	r := auditRef(t, z, s)
	ReleaseString(z, &s)
	AssignRef(z, r, Deref(z, r))
	if StringData(z, Deref(z, r)) != strings.Repeat("x", 20) {
		t.Fatal("self assignment lost string")
	}
	ReleaseReference(z, &r)
	assertZoneEmpty(t, z)
}

func TestAuditNoNestedCell(t *testing.T) {
	z := zone.New(8192)
	a := arrayValue(t, z)
	arraySet(t, z, &a, NewLong(0), NewLong(1))
	r, err := ArrayReference(z, &a, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	ReleaseReference(z, &r)
	b := RetainArray(z, a)
	r, err = ArrayReference(z, &a, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if Deref(z, r).Type == TypeReference {
		t.Fatal("nested reference")
	}
	AssignRef(z, r, NewLong(5))
	x, ok, err := ArrayGet(z, b, NewLong(0), nil)
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	if Deref(z, x).ToLong() != 1 {
		t.Fatal("CoW did not separate cell")
	}
	ReleaseReference(z, &r)
	ReleaseArray(z, &a)
	ReleaseArray(z, &b)
	assertZoneEmpty(t, z)
}

func TestAuditSharedCellReference(t *testing.T) {
	z := zone.New(8192)
	a := arrayValue(t, z)
	arraySet(t, z, &a, NewLong(0), NewLong(1))
	r, err := ArrayReference(z, &a, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	b := RetainArray(z, a)
	r2, err := ArrayReference(z, &b, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	AssignRef(z, r2, NewLong(7))
	var got string
	for _, v := range []Value{a, b} {
		x, ok, err := ArrayGet(z, v, NewLong(0), nil)
		if err != nil || !ok {
			t.Fatalf("get: %v %v", ok, err)
		}
		got += fmt.Sprintf("i:%d;", Deref(z, x).ToLong())
	}
	got += fmt.Sprintf("i:%d;", Deref(z, r).ToLong())
	checkArrayPHP(t, `$a=[1];$r=&$a[0];$b=$a;$r2=&$b[0];$r2=7;enc($a[0]);enc($b[0]);enc($r);`, got)
	ReleaseReference(z, &r)
	ReleaseReference(z, &r2)
	ReleaseArray(z, &a)
	ReleaseArray(z, &b)
	assertZoneEmpty(t, z)
}

func TestAuditRootsAtBound(t *testing.T) {
	z := zone.New(1 << 16)
	o := objectValue(t, z, 0)
	held := make([]Value, 0, (1<<16)/24)
	for {
		r, err := NewReference(z, o)
		if err == zone.ErrExhausted {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, r)
	}
	// Measure the first fill: AllocsPerRun's warm-up would hide slice growth.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for _, r := range held {
		x := RetainReference(z, r)
		ReleaseReference(z, &x)
	}
	n := CollectCycles(z)
	runtime.ReadMemStats(&after)
	if n != 0 {
		t.Fatalf("live cells collected: %d", n)
	}
	if after.TotalAlloc != before.TotalAlloc || after.Mallocs != before.Mallocs {
		t.Fatalf("root bound: %d B, %d allocs", after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
	}
	for i := range held {
		ReleaseReference(z, &held[i])
	}
	ReleaseObject(z, &o)
	assertZoneEmpty(t, z)
}

func TestAuditRootSlotEncoding(t *testing.T) {
	t.Run("compact_in_place", func(t *testing.T) {
		z := zone.New(32768)
		a := arrayValue(t, z)
		// Nonconsecutive integer keys force hash storage before filling it.
		for i := range int64(8) {
			arraySet(t, z, &a, NewLong(100+i), NewLong(i))
		}
		h := arrayHeader(z, a)
		for i := int64(8); i < int64(h.Cap); i++ {
			arraySet(t, z, &a, NewLong(100+i), NewLong(i))
		}
		capacity, offset := h.Cap, h.DataOffset
		for i := int64(0); i < int64(capacity/2); i++ {
			if err := ArrayUnset(z, &a, NewLong(100+i), nil); err != nil {
				t.Fatal(err)
			}
		}
		b, _ := arrayData(z, h)
		if h.Kind != ArrayHash || h.Used != h.Cap || h.Len > h.Cap/2 || !b[0].Val.IsUndef() {
			t.Fatalf("compaction precondition: %+v", h)
		}
		const slot = 1<<27 | 37
		gcSetRootSlot(z, a, slot)
		arraySet(t, z, &a, NewLong(999), NewLong(42))
		h = arrayHeader(z, a)
		if h.Cap != capacity || h.DataOffset != offset || h.Used != capacity/2+1 {
			t.Fatalf("not in-place compaction: %+v", h)
		}
		if got := gcRootSlot(z, a); got != slot {
			t.Fatalf("root slot after compact=%d want=%d", got, slot)
		}
		gcSetRootSlot(z, a, 0)
		ReleaseArray(z, &a)
		assertZoneEmpty(t, z)
	})
	z := zone.New(32768)
	a := arrayValue(t, z)
	r := auditRef(t, z, a)
	o := objectValue(t, z, 0)
	for _, v := range []Value{a, r, o} {
		for _, slot := range []uint32{0, 31, 32, 1<<21 - 1, 1 << 21, 1<<28 - 1} {
			gcSetRootSlot(z, v, slot)
			for _, flags := range []uint32{Purple | Buffered, Grey | Buffered, Black, White} {
				gcSetFlags(z, v, flags)
				if gcRootSlot(z, v) != slot || gcFlags(z, v) != flags {
					t.Fatalf("type=%d slot=%d flags=%d", v.Type, slot, flags)
				}
			}
		}
		gcSetFlags(z, v, Black)
		gcSetRootSlot(z, v, 0)
	}
	ReleaseReference(z, &r)
	// Exercise the high bits independently of a multi-million-node allocation.
	gcSetRootSlot(z, a, 1<<27|37)
	for i := range int64(16) {
		arraySet(t, z, &a, NewLong(i), NewLong(i))
	}
	for i := range int64(12) {
		if err := ArrayUnset(z, &a, NewLong(i), nil); err != nil {
			t.Fatal(err)
		}
	}
	arraySet(t, z, &a, NewLong(30), NewLong(30))
	if gcRootSlot(z, a) != 1<<27|37 {
		t.Fatal("growth or compaction lost root high bits")
	}
	gcSetRootSlot(z, a, 0)
	ReleaseArray(z, &a)
	ReleaseObject(z, &o)
	assertZoneEmpty(t, z)
}

func TestAuditUnbufferLinear(t *testing.T) {
	if productionTiming(t) {
		return
	}
	assertLinearCPUWithBase(t, 2000, func(n int) time.Duration {
		z := zone.New(24 << 20)
		values := make([]Value, n)
		for i := range values {
			values[i] = arrayValue(t, z)
			x := RetainArray(z, values[i])
			ReleaseArray(z, &x)
		}
		if z.BufferedRoots() != len(values) {
			t.Fatal("root count")
		}
		start := threadCPU(t)
		for i := range values {
			ReleaseArray(z, &values[i])
		}
		elapsed := threadCPU(t) - start
		assertZoneEmpty(t, z)
		return elapsed
	})
}

func threadCPU(t *testing.T) time.Duration {
	t.Helper()
	var stamp syscall.Timespec
	// Linux CLOCK_THREAD_CPUTIME_ID provides nanosecond accounting; getrusage
	// may only update on scheduler ticks and reports zero for these short runs.
	const clockThreadCPU = 3
	if _, _, errno := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clockThreadCPU, uintptr(unsafe.Pointer(&stamp)), 0); errno != 0 {
		t.Fatal(errno)
	}
	return time.Duration(stamp.Nano())
}

func assertLinearCPU(t *testing.T, measure func(int) time.Duration) {
	t.Helper()
	assertLinearCPUWithBase(t, 10000, measure)
}

func assertLinearCPUWithBase(t *testing.T, base int, measure func(int) time.Duration) {
	t.Helper()
	// Thread CPU excludes descheduling. Compare paired measurements, not
	// independent minima taken under potentially different memory contention.
	// The lower envelope rejects persistent superlinear growth, not a slow pair.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ratio := float64(1<<63 - 1)
	for round := range 7 {
		var small, large time.Duration
		if round%2 == 0 {
			small = measure(base)
			large = measure(4 * base)
		} else {
			large = measure(4 * base)
			small = measure(base)
		}
		if small <= 0 {
			t.Fatal("thread CPU clock did not advance")
		}
		pair := float64(large) / float64(small)
		ratio = min(ratio, pair)
		t.Logf("thread CPU pair %d: N=%d %v; 4N=%d %v; ratio=%.3f", round, base, small, 4*base, large, pair)
	}
	if ratio >= 10 {
		t.Fatalf("nonlinear growth: ratio %.3f >= 10 (quadratic expectation: 16)", ratio)
	}
}

func TestAuditReferenceNonCollectable(t *testing.T) {
	z := zone.New(8192)
	s := stringValue(t, z, "reference-owned string", 0)
	for _, v := range []Value{{}, NewNull(), NewBool(false), NewBool(true), NewLong(1), NewDouble(1.5), s} {
		r := auditRef(t, z, v)
		alias := RetainReference(z, r)
		ReleaseReference(z, &alias)
		if z.BufferedRoots() != 0 {
			t.Errorf("non-collectable target %v buffered", v.Type)
		}
		ReleaseReference(z, &r)
	}
	ReleaseString(z, &s)
	assertZoneEmpty(t, z)
}

func productionTiming(t *testing.T) bool {
	t.Helper()
	// The complexity measurement applies to production, not race instrumentation.
	// A race run still executes this oracle in a non-instrumented child, never skips it.
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("missing build settings")
	}
	for _, setting := range info.Settings {
		if setting.Key == "-race" && setting.Value == "true" {
			out, err := exec.CommandContext(t.Context(), "go", "test", "-race=false", "-run", "^"+t.Name()+"$", "-count=1", "-v", ".").CombinedOutput()
			if err != nil {
				t.Fatalf("production timing: %v\n%s", err, out)
			}
			t.Logf("production timing:\n%s", out)
			return true
		}
	}
	return false
}

func TestAuditForeachReferenceRelease(t *testing.T) {
	if productionTiming(t) {
		return
	}
	assertLinearCPU(t, func(n int) time.Duration {
		z := zone.New(16 << 20)
		a := arrayValue(t, z)
		for i := range int64(n) {
			arraySet(t, z, &a, NewLong(i), NewLong(i))
		}
		start := threadCPU(t)
		if err := ArrayForEachReference(z, &a, func(Value, Value) bool { return true }); err != nil {
			t.Fatal(err)
		}
		ReleaseArray(z, &a)
		elapsed := threadCPU(t) - start
		assertZoneEmpty(t, z)
		return elapsed
	})
}

func TestReferencePHP(t *testing.T) {
	z := zone.New(8192)
	a, err := NewReference(z, NewLong(1))
	if err != nil {
		t.Fatal(err)
	}
	b := RetainReference(z, a)
	AssignRef(z, b, NewLong(2))
	ReleaseReference(z, &a)
	checkArrayPHP(t, `$a=1;$b=&$a;$b=2;unset($a);enc($b);`, fmt.Sprintf("i:%d;", Deref(z, b).ToLong()))
	ReleaseReference(z, &b)
	a = arrayValue(t, z)
	arraySet(t, z, &a, NewLong(0), NewLong(1))
	arraySet(t, z, &a, NewLong(1), NewLong(2))
	r, err := ArrayReference(z, &a, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	b = RetainArray(z, a)
	arraySet(t, z, &b, NewLong(1), NewLong(3))
	AssignRef(z, r, NewLong(99))
	x, _, err := ArrayGet(z, a, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	y, _, err := ArrayGet(z, b, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkArrayPHP(t, `$a=[1,2];$r=&$a[0];$b=$a;$b[1]=3;$r=99;enc($a[0]);enc($b[0]);`, fmt.Sprintf("i:%d;i:%d;", Deref(z, x).ToLong(), Deref(z, y).ToLong()))
	ReleaseReference(z, &r)
	ReleaseArray(z, &a)
	ReleaseArray(z, &b)
	if n := CollectCycles(z); n != 0 {
		t.Fatalf("acyclic collection=%d", n)
	}
}

func TestObjectGenerationAndLayout(t *testing.T) {
	if unsafe.Sizeof(RefCell{}) != 24 || unsafe.Offsetof(RefCell{}.RefCount) != 0 || unsafe.Offsetof(ObjectHeader{}.RefCount) != 0 {
		t.Fatal("layout")
	}
	var check func(reflect.Type)
	check = func(typ reflect.Type) {
		if typ.Kind() == reflect.Struct {
			for f := range typ.Fields() {
				check(f.Type)
			}
			return
		}
		if typ.Kind() < reflect.Int || typ.Kind() > reflect.Uint64 {
			t.Fatalf("pointer-bearing field: %v", typ)
		}
	}
	check(reflect.TypeFor[RefCell]())
	check(reflect.TypeFor[ObjectHeader]())
	z := zone.New(4096)
	a := objectValue(t, z, 2)
	stale := a
	ObjectSet(z, a, 0, NewLong(42))
	ReleaseObject(z, &a)
	b := objectValue(t, z, 2)
	defer ReleaseObject(z, &b)
	if stale.DataSlot == b.DataSlot || b.Extra != 1 {
		t.Fatalf("unused slot not preferred: %+v %+v", stale, b)
	}
	defer func() {
		if recover() != "types: stale or released object handle" {
			t.Fatal("stale handle accepted")
		}
	}()
	ObjectGet(z, stale, 0)
}

func TestObjectCleanupRegistration(t *testing.T) {
	z := zone.New(4096)
	v, err := NewObject(z, 17, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if z.PendingCleanups() != 1 {
		t.Fatal("missing cleanup")
	}
	z.RunCleanups(func(item zone.CleanupItem) {
		if item != (zone.CleanupItem{Kind: zone.CleanupDestructor, Target: v.DataSlot, Extra: uint32(v.Extra)}) {
			t.Fatalf("cleanup=%+v", item)
		}
		if ObjectInfo(z, v).ClassID != 17 {
			t.Fatal("object not live during cleanup")
		}
	})
	ReleaseObject(z, &v)
}

func TestCyclesPHP(t *testing.T) {
	for _, mode := range []string{"objects", "mixed", "reference", "diamond"} {
		t.Run(mode, func(t *testing.T) {
			z := zone.New(16384)
			var program string
			var live Value
			switch mode {
			case "reference":
				a := arrayValue(t, z)
				r, err := NewReference(z, a)
				if err != nil {
					t.Fatal(err)
				}
				ReleaseArray(z, &a)
				if err := ArrayAppend(z, &r, r); err != nil {
					t.Fatal(err)
				}
				ReleaseReference(z, &r)
				program = `$a=[];$a[0]=&$a;unset($a);`
			case "mixed":
				a := objectValue(t, z, 1)
				b := arrayValue(t, z)
				arraySet(t, z, &b, NewLong(0), a)
				ObjectSet(z, a, 0, b)
				ReleaseObject(z, &a)
				ReleaseArray(z, &b)
				program = `$a=new stdClass;$b=[$a];$a->x=$b;unset($a,$b);`
			case "objects":
				a, b := objectValue(t, z, 1), objectValue(t, z, 1)
				ObjectSet(z, a, 0, b)
				ObjectSet(z, b, 0, a)
				ReleaseObject(z, &a)
				ReleaseObject(z, &b)
				program = `$a=new stdClass;$b=new stdClass;$a->x=$b;$b->x=$a;unset($a,$b);`
			case "diamond":
				a, b, c, d := objectValue(t, z, 2), objectValue(t, z, 1), objectValue(t, z, 1), objectValue(t, z, 1)
				ObjectSet(z, a, 0, b)
				ObjectSet(z, a, 1, c)
				ObjectSet(z, b, 0, d)
				ObjectSet(z, c, 0, d)
				ObjectSet(z, d, 0, a)
				live = RetainObject(z, d)
				ReleaseObject(z, &a)
				ReleaseObject(z, &b)
				ReleaseObject(z, &c)
				ReleaseObject(z, &d)
				program = `$a=new stdClass;$b=new stdClass;$c=new stdClass;$d=new stdClass;$a->b=$b;$a->c=$c;$b->d=$d;$c->d=$d;$d->a=$a;$live=$d;unset($a,$b,$c,$d);`
			}
			n := CollectCycles(z)
			checkArrayPHP(t, program+`echo gc_collect_cycles();`, fmt.Sprint(n))
			if mode == "diamond" {
				if n != 0 || ObjectInfo(z, live).RefCount != 3 {
					t.Fatalf("live diamond count=%d header=%+v", n, ObjectInfo(z, live))
				}
				ReleaseObject(z, &live)
				if n := CollectCycles(z); n != 4 {
					t.Fatalf("dead diamond=%d", n)
				}
			}
			if n := CollectCycles(z); n != 0 {
				t.Fatalf("second collection=%d", n)
			}
		})
	}
}

func cycleExercise(z *zone.Zone) {
	a, err := NewObject(z, 1, 1, false)
	if err != nil {
		panic(err)
	}
	b, err := NewArray(z)
	if err != nil {
		panic(err)
	}
	if err := ArrayAppend(z, &b, a); err != nil {
		panic(err)
	}
	ObjectSet(z, a, 0, b)
	ReleaseObject(z, &a)
	ReleaseArray(z, &b)
	if CollectCycles(z) != 2 {
		panic("cycle not collected")
	}
}

func TestCyclesEnduranceZeroHeap(t *testing.T) {
	z := zone.New(4096)
	for range 10000 {
		cycleExercise(z)
	}
	if n := testing.AllocsPerRun(10000, func() { cycleExercise(z) }); n != 0 {
		t.Fatalf("allocs=%g", n)
	}
}

func BenchmarkCycles(b *testing.B) {
	z := zone.New(4096)
	cycleExercise(z)
	b.ReportAllocs()
	for b.Loop() {
		cycleExercise(z)
	}
}

func TestReferenceUnsharedCoW(t *testing.T) {
	z := zone.New(4096)
	a := arrayValue(t, z)
	arraySet(t, z, &a, NewLong(0), NewLong(1))
	r, err := ArrayReference(z, &a, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	ReleaseReference(z, &r)
	b := RetainArray(z, a)
	arraySet(t, z, &b, NewLong(0), NewLong(2))
	x, _, err := ArrayGet(z, a, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	y, _, err := ArrayGet(z, b, NewLong(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkArrayPHP(t, `$a=[1];$r=&$a[0];unset($r);$b=$a;$b[0]=2;enc($a[0]);enc($b[0]);`, fmt.Sprintf("i:%d;i:%d;", Deref(z, x).ToLong(), Deref(z, y).ToLong()))
	ReleaseArray(z, &a)
	ReleaseArray(z, &b)
}

func TestCyclesStringPHP(t *testing.T) {
	z := zone.New(4096)
	a := objectValue(t, z, 2)
	s := stringValue(t, z, "request-owned string", 0)
	ObjectSet(z, a, 0, a)
	ObjectSet(z, a, 1, s)
	ReleaseString(z, &s)
	ReleaseObject(z, &a)
	checkArrayPHP(t, `$a=new stdClass;$a->self=$a;$a->s=str_repeat('x',20);unset($a);echo gc_collect_cycles();`, fmt.Sprint(CollectCycles(z)))
}

func TestReferenceIterationPHP(t *testing.T) {
	z := zone.New(8192)
	a := arrayValue(t, z)
	for i := range int64(3) {
		arraySet(t, z, &a, NewLong(i), NewLong(i))
	}
	var last Value
	if err := ArrayForEachReference(z, &a, func(k, r Value) bool {
		AssignRef(z, r, NewLong(Deref(z, r).ToLong()+10))
		ReleaseValue(z, &last)
		last = RetainReference(z, r)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	AssignRef(z, last, NewLong(99))
	var got string
	for i := range int64(3) {
		v, _, err := ArrayGet(z, a, NewLong(i), nil)
		if err != nil {
			t.Fatal(err)
		}
		got += fmt.Sprintf("i:%d;", Deref(z, v).ToLong())
	}
	checkArrayPHP(t, `$a=[0,1,2];foreach($a as &$r){$r+=10;}$r=99;foreach($a as $v){enc($v);}`, got)
	ReleaseReference(z, &last)
	ReleaseArray(z, &a)
}

func TestObjectGenerationExhaustion(t *testing.T) {
	z := zone.New(32)
	a := objectValue(t, z, 0)
	stale := a
	ReleaseObject(z, &a)
	for range 65534 {
		a = objectValue(t, z, 0)
		ReleaseObject(z, &a)
	}
	if _, err := NewObject(z, 1, 0, false); err != zone.ErrExhausted {
		t.Fatalf("generation wrap allowed: %v", err)
	}
	defer func() {
		if recover() != staleObject {
			t.Fatal("generation wrap resurrected a stale handle")
		}
	}()
	ObjectInfo(z, stale)
}

func TestCyclesExternalReference(t *testing.T) {
	z := zone.New(8192)
	a, b := objectValue(t, z, 1), objectValue(t, z, 1)
	r, err := NewReference(z, a)
	if err != nil {
		t.Fatal(err)
	}
	ObjectSet(z, a, 0, b)
	ObjectSet(z, b, 0, r)
	ReleaseObject(z, &a)
	ReleaseObject(z, &b)
	if n := CollectCycles(z); n != 0 {
		t.Fatalf("external cell lost: %d", n)
	}
	a = Deref(z, r)
	if ObjectInfo(z, a).RefCount != 1 {
		t.Fatal("target count not restored")
	}
	ReleaseReference(z, &r)
	if n := CollectCycles(z); n != 2 {
		t.Fatalf("dead reference cycle=%d", n)
	}
}

func TestCyclesSharedReferenceAndLiveChild(t *testing.T) {
	z := zone.New(8192)
	a, b, live := objectValue(t, z, 2), objectValue(t, z, 1), objectValue(t, z, 0)
	r, err := NewReference(z, a)
	if err != nil {
		t.Fatal(err)
	}
	ObjectSet(z, a, 0, b)
	ObjectSet(z, a, 1, live)
	ObjectSet(z, b, 0, r)
	ReleaseReference(z, &r)
	ReleaseObject(z, &a)
	ReleaseObject(z, &b)
	if n := CollectCycles(z); n != 2 {
		t.Fatalf("dead parents=%d", n)
	}
	if ObjectInfo(z, live).RefCount != 1 {
		t.Fatal("dead edge not removed from live child")
	}
	ReleaseObject(z, &live)
}

func TestObjectAndReferenceAllocationFailure(t *testing.T) {
	z := zone.New(32)
	for range 3 {
		if _, err := NewObject(z, 1, 1, false); err != zone.ErrExhausted {
			t.Fatalf("object failure=%v", err)
		}
	}
	if off, err := z.Alloc(32); err != nil || off != 0 {
		t.Fatalf("leaked header: %d %v", off, err)
	}
	if err := z.Free(0, 32); err != nil {
		t.Fatal(err)
	}
	z = zone.New(48)
	s := stringValue(t, z, "owned", 0)
	if _, err := NewReference(z, s); err != zone.ErrExhausted {
		t.Fatalf("reference failure=%v", err)
	}
	if StringInfo(z, s).RefCount != 1 {
		t.Fatal("failed reference changed initial owner")
	}
	ReleaseString(z, &s)
}

func TestReferenceIterationSupportsStructuralMutation(t *testing.T) {
	z := zone.New(65536)
	a := arrayValue(t, z)
	arraySet(t, z, &a, NewLong(0), NewLong(1))
	var visited []int64
	err := ArrayForEachReference(z, &a, func(k, r Value) bool {
		v := Deref(z, r).ToLong()
		visited = append(visited, v)
		if k.ToLong() == 0 {
			for i := int64(1); i <= 12; i++ {
				arraySet(t, z, &a, NewLong(i), NewLong(i+1))
			}
			if err := ArrayUnset(z, &a, NewLong(2), nil); err != nil {
				t.Fatal(err)
			}
		}
		AssignRef(z, r, NewLong(v*10))
		return true
	})
	if err != nil {
		t.Fatalf("structural mutation failed: %v", err)
	}
	if len(visited) != 12 {
		t.Fatalf("visited=%v; want 12 elements", visited)
	}
	v0, ok, err := ArrayGet(z, a, NewLong(0), nil)
	if err != nil || !ok || Deref(z, v0).ToLong() != 10 {
		t.Fatalf("v0=%+v ok=%v err=%v", v0, ok, err)
	}
	v12, ok, err := ArrayGet(z, a, NewLong(12), nil)
	if err != nil || !ok || Deref(z, v12).ToLong() != 130 {
		t.Fatalf("v12=%+v ok=%v err=%v", v12, ok, err)
	}
	ReleaseArray(z, &a)
	if n := CollectCycles(z); n != 0 {
		t.Fatalf("leaked iterator cell: %d", n)
	}
	assertZoneEmpty(t, z)
}

func TestObjectHeaderAndResetInvalidation(t *testing.T) {
	z := zone.New(4096)
	a := objectValue(t, z, 0)
	h := objectHeader(z, a)
	for _, field := range []string{"generation", "count"} {
		func() {
			if field == "generation" {
				h.Generation++
			} else {
				h.RefCount = 0
			}
			defer func() {
				if recover() != staleObject {
					t.Errorf("invalid %s accepted", field)
				}
				h.Generation, h.RefCount = a.Extra, 1
			}()
			ObjectInfo(z, a)
		}()
	}
	z.Reset(nil)
	b := objectValue(t, z, 0)
	defer ReleaseObject(z, &b)
	defer func() {
		if recover() != staleObject {
			t.Fatal("reset resurrected handle")
		}
	}()
	ObjectInfo(z, a)
}
