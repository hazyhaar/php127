package zone

import (
	"errors"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"testing/synctest"
	"unsafe"
)

func TestAllocAlignmentAndAddressing(t *testing.T) {
	z := New(256)
	var next uint32
	for _, size := range []uint32{1, 7, 8, 9, 17, 32} {
		offset, err := z.Alloc(size)
		if err != nil || offset != next || offset%8 != 0 {
			t.Fatalf("Alloc(%d) = %d, %v; want %d", size, offset, err, next)
		}
		data, err := z.Bytes(offset, size)
		if err != nil || len(data) != int(size) || cap(data) != len(data) {
			t.Fatalf("Bytes(%d, %d) = len %d cap %d, %v", offset, size, len(data), cap(data), err)
		}
		if uintptr(unsafe.Pointer(&data[0]))%8 != 0 {
			t.Fatal("unaligned backing memory")
		}
		data[0] = byte(size)
		for i := range data {
			got, err := z.Offset(&data[i])
			if err != nil || got != offset+uint32(i) {
				t.Fatalf("Offset = %d, %v", got, err)
			}
		}
		next += (size + 7) &^ 7
	}
}

func TestAllocBounds(t *testing.T) {
	z := New(17)
	for _, size := range []uint32{0, 18, ^uint32(0), ^uint32(0) - 6} {
		if _, err := z.Alloc(size); err == nil {
			t.Fatalf("Alloc(%d) unexpectedly succeeded", size)
		}
	}
	if offset, err := z.Alloc(9); err != nil || offset != 0 {
		t.Fatalf("Alloc(9) = %d, %v", offset, err)
	}
	if _, err := z.Alloc(1); !errors.Is(err, ErrExhausted) {
		t.Fatalf("exhaustion = %v", err)
	}
	for _, span := range [][2]uint32{{16, 1}, {0, 17}, {^uint32(0), 2}, {8, ^uint32(0)}} {
		if _, err := z.Bytes(span[0], span[1]); !errors.Is(err, ErrInvalidOffset) {
			t.Fatalf("Bytes(%v) = %v", span, err)
		}
	}
	var foreign byte
	for _, ptr := range []*byte{nil, &foreign} {
		if _, err := z.Offset(ptr); !errors.Is(err, ErrInvalidOffset) {
			t.Fatalf("Offset(foreign) = %v", err)
		}
	}
	var empty Zone
	if _, err := empty.Alloc(1); !errors.Is(err, ErrExhausted) {
		t.Fatalf("zero zone allocation = %v", err)
	}
	empty.Reset(nil)
	if _, err := New(0).Alloc(1); !errors.Is(err, ErrExhausted) {
		t.Fatalf("zero capacity allocation = %v", err)
	}
}

func TestOffsetRejectsUnallocatedMemory(t *testing.T) {
	z := New(64)
	if _, err := z.Alloc(16); err != nil {
		t.Fatal(err)
	}
	if offset, err := z.Offset(&z.memory[15]); err != nil || offset != 15 {
		t.Fatalf("last allocated byte = %d, %v; want 15, nil", offset, err)
	}
	for _, offset := range []uint32{16, 17, 63} {
		if _, err := z.Offset(&z.memory[offset]); !errors.Is(err, ErrInvalidOffset) {
			t.Errorf("Offset at %d = %v; want ErrInvalidOffset", offset, err)
		}
	}
}

func TestResetPreservesMemoryAfterGoexit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		z := New(16)
		if _, err := z.Alloc(16); err != nil {
			t.Fatal(err)
		}
		z.memory[0] = 42
		z.RegisterCleanup(CleanupItem{Target: 1})
		z.RegisterCleanup(CleanupItem{Target: 2})
		var got []uint32
		done := make(chan struct{})
		go func() {
			defer close(done)
			z.Reset(func(item CleanupItem) {
				got = append(got, item.Target)
				runtime.Goexit()
			})
		}()
		<-done
		if z.cursor != 16 || z.cleanups.Len() != 1 {
			t.Errorf("after Goexit: cursor=%d pending=%d; want 16, 1", z.cursor, z.cleanups.Len())
		}
		if _, err := z.Alloc(1); !errors.Is(err, ErrExhausted) {
			t.Errorf("allocation after Goexit = %v; want ErrExhausted", err)
		}
		z.Reset(func(item CleanupItem) {
			got = append(got, item.Target)
			data, err := z.Bytes(0, 16)
			if err != nil || data[0] != 42 {
				t.Errorf("pending cleanup lost live memory: %v", err)
			}
		})
		if !slices.Equal(got, []uint32{2, 1}) {
			t.Errorf("cleanups = %v; want [2 1]", got)
		}
		if z.cursor != 0 || z.cleanups.Len() != 0 {
			t.Errorf("after resumed Reset: cursor=%d pending=%d; want 0, 0", z.cursor, z.cleanups.Len())
		}
		if offset, err := z.Alloc(16); err != nil || offset != 0 {
			t.Errorf("reuse after resumed Reset = %d, %v; want 0, nil", offset, err)
		}
	})
}

func TestCleanupLIFO(t *testing.T) {
	var registry CleanupRegistry
	items := []CleanupItem{
		{Kind: CleanupDestructor, Target: 1, Extra: 11},
		{Kind: CleanupStreamLock, Target: 2, Extra: 22},
		{Kind: CleanupTransaction, Target: 3, Extra: 33},
	}
	for _, item := range items {
		registry.RegisterCleanup(item)
	}
	var got []CleanupItem
	registry.RunCleanups(func(item CleanupItem) { got = append(got, item) })
	slices.Reverse(items)
	if !slices.Equal(got, items) {
		t.Fatalf("cleanups = %v, want %v", got, items)
	}
	registry.RunCleanups(nil)
	itemType := reflect.TypeFor[CleanupItem]()
	for field := range itemType.Fields() {
		kind := field.Type.Kind()
		if kind != reflect.Uint8 && kind != reflect.Uint32 {
			t.Fatalf("cleanup field %s is not pointer-free", field.Name)
		}
	}
}

func TestResetReusesMemoryAfterCleanups(t *testing.T) {
	z := New(16)
	offset, err := z.Alloc(16)
	if err != nil {
		t.Fatal(err)
	}
	data, err := z.Bytes(offset, 16)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 42
	z.RegisterCleanup(CleanupItem{Target: offset})
	calls := 0
	z.Reset(func(item CleanupItem) {
		calls++
		live, err := z.Bytes(item.Target, 16)
		if err != nil || live[0] != 42 {
			t.Errorf("memory invalidated before cleanup: %v", err)
		}
		if _, err := z.Alloc(1); !errors.Is(err, ErrExhausted) {
			t.Errorf("cursor reset before cleanup: %v", err)
		}
	})
	if calls != 1 {
		t.Fatalf("cleanup calls = %d", calls)
	}
	if _, err := z.Bytes(offset, 1); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("reset memory still accessible: %v", err)
	}
	if _, err := z.Offset(&data[0]); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("reset pointer still accessible: %v", err)
	}
	again, err := z.Alloc(16)
	if err != nil || again != offset {
		t.Fatalf("reuse = %d, %v", again, err)
	}
	reused, err := z.Bytes(again, 16)
	if err != nil || &reused[0] != &data[0] {
		t.Fatalf("backing memory not reused: %v", err)
	}
	z.Reset(nil)
}

func TestCleanupRegistrationDuringDrain(t *testing.T) {
	var registry CleanupRegistry
	registry.RegisterCleanup(CleanupItem{Target: 1})
	registry.RegisterCleanup(CleanupItem{Target: 2})
	var got []uint32
	registry.RunCleanups(func(item CleanupItem) {
		got = append(got, item.Target)
		if item.Target == 2 {
			registry.RegisterCleanup(CleanupItem{Target: 3})
		}
	})
	if !slices.Equal(got, []uint32{2, 3, 1}) {
		t.Fatalf("cleanups = %v", got)
	}
}

func TestResetDrainsAfterPanic(t *testing.T) {
	z := New(8)
	if _, err := z.Alloc(8); err != nil {
		t.Fatal(err)
	}
	for i := uint32(1); i <= 3; i++ {
		z.RegisterCleanup(CleanupItem{Target: i})
	}
	var got []uint32
	func() {
		defer func() {
			if value := recover(); value != "first" {
				t.Errorf("panic = %v, want first", value)
			}
		}()
		z.Reset(func(item CleanupItem) {
			got = append(got, item.Target)
			if item.Target == 3 {
				panic("first")
			}
			if item.Target == 2 {
				panic("second")
			}
		})
	}()
	if !slices.Equal(got, []uint32{3, 2, 1}) {
		t.Fatalf("cleanups = %v", got)
	}
	if offset, err := z.Alloc(8); err != nil || offset != 0 {
		t.Fatalf("reset after panic = %d, %v", offset, err)
	}
	z.Reset(nil)
}

func TestResetRejectsNilAndReentrancy(t *testing.T) {
	z := New(8)
	if _, err := z.Alloc(8); err != nil {
		t.Fatal(err)
	}
	z.RegisterCleanup(CleanupItem{Target: 1})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("nil dispatch did not panic")
			}
		}()
		z.Reset(nil)
	}()
	calls := 0
	z.Reset(func(CleanupItem) {
		calls++
		for _, run := range []func(){func() { z.Reset(nil) }, func() { z.RunCleanups(nil) }} {
			func() {
				defer func() {
					if recover() == nil {
						t.Error("recursive cleanup did not panic")
					}
				}()
				run()
			}()
		}
		if _, err := z.Alloc(1); !errors.Is(err, ErrExhausted) {
			t.Errorf("reentrant Reset released live memory: %v", err)
		}
	})
	if calls != 1 {
		t.Fatalf("nil dispatch lost cleanup: %d calls", calls)
	}
}

func TestAllocNoHeapAllocations(t *testing.T) {
	z := New(8192)
	if got := testing.AllocsPerRun(100, func() {
		z.Reset(nil)
		for range 1024 {
			if _, err := z.Alloc(8); err != nil {
				panic(err)
			}
		}
	}); got != 0 {
		t.Fatalf("heap allocations per 1024 allocations = %g", got)
	}
}

func BenchmarkAlloc(b *testing.B) {
	z := New(DefaultCapacity)
	b.ReportAllocs()
	var used uint32
	for b.Loop() {
		if used == DefaultCapacity {
			z.Reset(nil)
			used = 0
		}
		if _, err := z.Alloc(8); err != nil {
			b.Fatal(err)
		}
		used += 8
	}
}
