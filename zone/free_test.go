package zone

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func TestFreeReuseAndReset(t *testing.T) {
	for _, span := range []uint32{8, 16, 24, 32, 40, 48, 56, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072, 262144, 524288, 1048576, 2097152, 4194304, 8388608, 16777216, 33554432, 67108864} {
		size := span - 7
		if span > 64 {
			size = span/2 + 1
		}
		z := New(2*span + 8)
		a, err := z.Alloc(size)
		if err != nil {
			t.Fatal(err)
		}
		b, err := z.Alloc(size)
		if err != nil {
			t.Fatal(err)
		}
		if a != 0 || b != span || z.cursor != 2*span {
			t.Fatalf("size=%d offsets=%d,%d cursor=%d span=%d", size, a, b, z.cursor, span)
		}
		if _, err := z.Alloc(8); err != nil {
			t.Fatal(err)
		}
		if err := z.Free(a, size); err != nil {
			t.Fatal(err)
		}
		if err := z.Free(b, size); err != nil {
			t.Fatal(err)
		}
		for _, want := range []uint32{b, a} {
			got, err := z.Alloc(span)
			if err != nil || got != want {
				t.Fatalf("size=%d got=%d want=%d err=%v", size, got, want, err)
			}
		}
		if err := z.Free(b, size); err != nil {
			t.Fatal(err)
		}
		z.Reset(nil)
		if got, err := z.Alloc(size); err != nil || got != 0 {
			t.Fatalf("stale list after reset: %d %v", got, err)
		}
		if _, err := z.Alloc(span); err != nil {
			t.Fatal(err)
		}
		if _, err := z.Alloc(8); err != nil {
			t.Fatal(err)
		}
		if _, err := z.Alloc(size); !errors.Is(err, ErrExhausted) {
			t.Fatalf("reset retained a free-list entry: %v", err)
		}
	}
}

func TestFreeGeometricTop(t *testing.T) {
	for _, size := range []uint32{65, 129, 65537, 131073, 262144, 262145} {
		z := New(1 << 20)
		offset, err := z.Alloc(size)
		if err != nil {
			t.Fatal(err)
		}
		if err := z.Free(offset, size); err != nil || z.cursor != 0 {
			t.Fatalf("size=%d cursor=%d err=%v", size, z.cursor, err)
		}
	}
}

func TestSizeClassBounds(t *testing.T) {
	for _, tc := range []struct {
		size  uint32
		class int
		span  uint64
	}{
		{64, 7, 64}, {65, 8, 128}, {128, 8, 128}, {129, 9, 256},
		{262144, 19, 262144}, {262145, 20, 524288},
		{4 << 20, 23, 4 << 20}, {4<<20 + 1, 24, 8 << 20},
		{64 << 20, 27, 64 << 20}, {64<<20 + 1, -1, 64<<20 + 8},
		{math.MaxUint32, -1, 1 << 32},
	} {
		class, span := sizeClass(tc.size)
		if class != tc.class || span != tc.span {
			t.Errorf("size=%d class=%d span=%d, want %d,%d", tc.size, class, span, tc.class, tc.span)
		}
	}
	z := New(2*(4<<20+8) + 8)
	if _, err := z.Alloc(4<<20 + 1); err != nil {
		t.Fatal(err)
	}
	if _, err := z.Alloc(8); err != nil {
		t.Fatal(err)
	}
	if err := z.Free(0, 4<<20+1); err != nil {
		t.Fatal(err)
	}
	if got, err := z.Alloc(4<<20 + 1); err != nil || got != 0 {
		t.Fatalf("large non-top block not recycled: %d %v", got, err)
	}
}

func TestFreeClearsReferenceCount(t *testing.T) {
	z := New(64)
	off, err := z.Alloc(32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := z.Alloc(8); err != nil {
		t.Fatal(err)
	}
	raw, err := z.Bytes(off, 4)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(raw, 123)
	if err := z.Free(off, 32); err != nil {
		t.Fatal(err)
	}
	raw, err = z.Bytes(off, 4)
	if err != nil || binary.LittleEndian.Uint32(raw) != 0 {
		t.Fatalf("reference count not cleared: %v %v", raw, err)
	}
}

func TestFreeBoundsAndTop(t *testing.T) {
	z := New(64)
	if _, err := z.Alloc(32); err != nil {
		t.Fatal(err)
	}
	for _, span := range [][2]uint32{{0, 0}, {1, 8}, {32, 8}, {24, 9}, {0, math.MaxUint32}, {math.MaxUint32 - 7, 8}} {
		if err := z.Free(span[0], span[1]); err == nil {
			t.Fatalf("accepted %v", span)
		}
	}
	if z.cursor != 32 {
		t.Fatal("invalid free changed cursor")
	}
	if err := z.Free(24, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := z.Bytes(24, 1); !errors.Is(err, ErrInvalidOffset) {
		t.Fatal("top not reclaimed")
	}
	if got, err := z.Alloc(8); err != nil || got != 24 {
		t.Fatalf("top reuse: %d %v", got, err)
	}
	var empty Zone
	if _, err := empty.Alloc(8); !errors.Is(err, ErrExhausted) {
		t.Fatal(err)
	}
}

func TestPendingCleanups(t *testing.T) {
	z := New(8)
	if z.PendingCleanups() != 0 {
		t.Fatal("initial pending")
	}
	z.RegisterCleanup(CleanupItem{})
	z.RegisterCleanup(CleanupItem{})
	if z.PendingCleanups() != 2 {
		t.Fatal("registered pending")
	}
	want := 1
	z.Reset(func(CleanupItem) {
		if z.PendingCleanups() != want {
			t.Fatal("dispatch pending")
		}
		want--
	})
	if z.PendingCleanups() != 0 {
		t.Fatal("remaining pending")
	}
}

func TestObjectSlotDistribution(t *testing.T) {
	z := New(96)
	for round := range 2 {
		for want := range uint32(3) {
			index, generation, err := z.NewObjectHandle(0)
			if err != nil || index != want || generation != uint16(round+1) {
				t.Fatalf("round=%d index=%d generation=%d err=%v", round, index, generation, err)
			}
			z.FreeObjectHandle(index, generation)
		}
	}
	z.Reset(nil)
	for want := range uint32(3) {
		index, generation, err := z.NewObjectHandle(0)
		if err != nil || index != want || generation != 3 {
			t.Fatalf("reset index=%d gen=%d err=%v", index, generation, err)
		}
	}
}

func TestResetClearsTailLink(t *testing.T) {
	z := New(96)
	var generations [3]uint16
	for i := range uint32(3) {
		index, generation, err := z.NewObjectHandle(0)
		if err != nil || index != i {
			t.Fatalf("initial handle=%d err=%v", index, err)
		}
		generations[i] = generation
	}
	// Free the highest index first so Reset's future tail points to index zero.
	for _, i := range []uint32{2, 0, 1} {
		z.FreeObjectHandle(i, generations[i])
	}
	if z.objects[2].next != 0 {
		t.Fatal("missing stale tail link")
	}
	z.Reset(nil)
	var live [3]bool
	count := 0
	for {
		index, _, err := z.NewObjectHandle(0)
		if err == ErrExhausted {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if index >= uint32(len(live)) || live[index] {
			t.Fatalf("live index %d handed out again", index)
		}
		live[index] = true
		count++
	}
	if count != len(live) {
		t.Fatalf("allocated %d slots want %d", count, len(live))
	}
}

func TestObjectAddressFreed(t *testing.T) {
	z := New(32)
	off, err := z.Alloc(32)
	if err != nil {
		t.Fatal(err)
	}
	index, generation, err := z.NewObjectHandle(off)
	if err != nil {
		t.Fatal(err)
	}
	if address, ok := z.ObjectAddress(index, generation); !ok || address != off {
		t.Fatal("live handle rejected")
	}
	z.FreeObjectHandle(index, generation)
	if _, ok := z.ObjectAddress(index, generation); ok {
		t.Fatal("freed handle reported as valid")
	}
	if err := z.Free(off, 32); err != nil {
		t.Fatal(err)
	}
}

func TestLiveBytes(t *testing.T) {
	var empty Zone
	if empty.LiveBytes() != 0 || empty.BufferedRoots() != 0 {
		t.Fatal("zero zone")
	}
	z := New(8192)
	for _, size := range []uint32{1, 17, 32, 65, 129, 513} {
		off, err := z.Alloc(size)
		if err != nil {
			t.Fatal(err)
		}
		guard, err := z.Alloc(8)
		if err != nil {
			t.Fatal(err)
		}
		_, span := sizeClass(size)
		if z.LiveBytes() != span+8 {
			t.Fatalf("size=%d live=%d", size, z.LiveBytes())
		}
		if err := z.Free(off, size); err != nil {
			t.Fatal(err)
		}
		if z.LiveBytes() != 8 {
			t.Fatal("free list not subtracted")
		}
		if err := z.Free(guard, 8); err != nil {
			t.Fatal(err)
		}
		if z.LiveBytes() != 0 {
			t.Fatal("top free not subtracted")
		}
		z.Reset(nil)
	}
	if n := testing.AllocsPerRun(100, func() { z.LiveBytes(); z.BufferedRoots() }); n != 0 {
		t.Fatalf("inspection allocs=%g", n)
	}
}

func TestZoneNewPreallocationCapAndObjectGrowth(t *testing.T) {
	z := New(2 << 20)
	if cap(z.cycles.Roots) != 10000 || cap(z.cycles.Work) != 10000 || len(z.objects) != 4096 {
		t.Fatalf("caps: roots=%d work=%d objects=%d", cap(z.cycles.Roots), cap(z.cycles.Work), len(z.objects))
	}
	for i := range uint32(5000) {
		idx, gen, err := z.NewObjectHandle(0)
		if err != nil || idx != i || gen != 1 {
			t.Fatalf("i=%d idx=%d gen=%d err=%v", i, idx, gen, err)
		}
	}
	if len(z.objects) < 5000 || uint32(len(z.objects)) > (2<<20)/32 {
		t.Fatalf("objects len=%d", len(z.objects))
	}
}

func TestResetIsO1WithObjects(t *testing.T) {
	z := New(2 << 20)
	var handles [1024][2]uint32
	for i := range uint32(1024) {
		idx, gen, err := z.NewObjectHandle(i * 32)
		if err != nil || idx != i {
			t.Fatalf("alloc handle %d: idx=%d err=%v", i, idx, err)
		}
		handles[i] = [2]uint32{idx, uint32(gen)}
	}
	z.Reset(nil)
	if z.objectUsed != 0 || z.epoch != 1 {
		t.Fatalf("after Reset: objectUsed=%d epoch=%d, want 0, 1", z.objectUsed, z.epoch)
	}
	for i, h := range handles {
		if _, ok := z.ObjectAddress(h[0], uint16(h[1])); ok {
			t.Fatalf("handle %d remained valid across Reset", i)
		}
	}
	idx, gen, err := z.NewObjectHandle(64)
	if err != nil || idx != 0 || gen != 2 {
		t.Fatalf("post-Reset NewObjectHandle: idx=%d gen=%d err=%v", idx, gen, err)
	}
	if addr, ok := z.ObjectAddress(idx, gen); !ok || addr != 64 {
		t.Fatalf("post-Reset ObjectAddress: addr=%d ok=%v", addr, ok)
	}
}

