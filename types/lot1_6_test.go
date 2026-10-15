package types

import (
	"encoding/binary"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

func TestDjbx33aParityAgainstGCC(t *testing.T) {
	cPath, err := filepath.Abs(filepath.Join("c_src", "djbx33a.c"))
	if err != nil {
		t.Fatal(err)
	}
	driverPath := filepath.Join(t.TempDir(), "driver.c")
	binPath := filepath.Join(t.TempDir(), "driver")
	driverCode := `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

uint64_t djbx33a(const uint8_t *str, int len);

int main(void) {
    uint32_t len;
    while (fread(&len, sizeof(len), 1, stdin) == 1) {
        uint8_t *buf = len ? (uint8_t *)malloc(len) : NULL;
        if (len && fread(buf, 1, len, stdin) != len) {
            free(buf);
            return 2;
        }
        uint64_t h = djbx33a(buf ? buf : (const uint8_t *)"", (int)len);
        fwrite(&h, sizeof(h), 1, stdout);
        free(buf);
    }
    return 0;
}
`
	if err := writeDriverAndCompile(t, cPath, driverPath, binPath, driverCode); err != nil {
		t.Fatal(err)
	}

	var inputs [][]byte
	for length := 0; length <= 64; length++ {
		buf := make([]byte, length)
		for i := range buf {
			buf[i] = byte((i*37 + length*13 + 0xa5) & 0xff)
		}
		inputs = append(inputs, buf)
	}
	for _, entry := range archtimeInternedTable {
		inputs = append(inputs, []byte(entry.text))
	}

	var stdinBuf []byte
	for _, in := range inputs {
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(len(in)))
		stdinBuf = append(stdinBuf, hdr[:]...)
		stdinBuf = append(stdinBuf, in...)
	}

	cmd := exec.CommandContext(t.Context(), binPath)
	cmd.Stdin = strings.NewReader(string(stdinBuf))
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != len(inputs)*8 {
		t.Fatalf("gcc oracle failed: err=%v len=%d", err, len(out))
	}

	z := zone.New(65536)
	for i, in := range inputs {
		want := binary.LittleEndian.Uint64(out[i*8 : (i+1)*8])
		got := Djbx33a(in, len(in))
		if got != want {
			t.Fatalf("input %d (len=%d): Djbx33a=%#x gcc=%#x", i, len(in), got, want)
		}
		z.Reset(nil)
		v := stringValue(t, z, string(in), 0)
		if sh := StringHash(z, v); sh != want {
			t.Fatalf("input %d: StringHash=%#x want=%#x", i, sh, want)
		}
	}
}

func writeDriverAndCompile(t *testing.T, cPath, driverPath, binPath, code string) error {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "gcc", "-O2", "-std=c99", "-Wall", "-Wextra", "-Werror", "-x", "c", cPath, "-x", "c", "-", "-o", binPath)
	cmd.Stdin = strings.NewReader(code)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gcc compile failed: %v\n%s", err, out)
	}
	return nil
}

func TestArchtimeProcessZoneAndNewStringLiteral(t *testing.T) {
	z := zone.New(1024)
	for i := range 256 {
		s := string([]byte{byte(i)})
		v, err := NewStringLiteral(z, s)
		if err != nil {
			t.Fatal(err)
		}
		if v != byteTable[i] || v.Flags&FlagZoneRequest != 0 || z.LiveBytes() != 0 {
			t.Fatalf("byte %d allocated or mismatched: %+v live=%d", i, v, z.LiveBytes())
		}
	}
	empty, err := NewStringLiteral(z, "")
	if err != nil || empty != emptyString || empty != InternedEmpty || z.LiveBytes() != 0 {
		t.Fatalf("empty literal: %+v err=%v live=%d", empty, err, z.LiveBytes())
	}
	for _, pair := range []struct {
		v    Value
		want string
	}{
		{InternedEmpty, ""},
		{InternedZero, "0"},
		{InternedOne, "1"},
		{InternedNull, "NULL"},
		{InternedBoolean, "boolean"},
		{InternedInteger, "integer"},
		{InternedDouble, "double"},
		{InternedString, "string"},
		{InternedArray, "array"},
		{InternedObject, "object"},
		{InternedResource, "resource"},
		{InternedStdClass, "stdClass"},
		{InternedTrue, "true"},
		{InternedFalse, "false"},
	} {
		if got := StringData(z, pair.v); got != pair.want {
			t.Fatalf("got=%q want=%q", got, pair.want)
		}
		h := StringInfo(z, pair.v)
		if h.Hash != Djbx33a([]byte(pair.want), len(pair.want)) {
			t.Fatalf("%q hash=%#x", pair.want, h.Hash)
		}
	}
}

func TestArrayKeyHashCacheO1(t *testing.T) {
	z := zone.New(65536)
	a := arrayValue(t, z)
	k := stringValue(t, z, strings.Repeat("long-key-", 100), 0)
	if StringInfo(z, k).Hash != 0 {
		t.Fatal("unexpected initial hash")
	}
	arraySet(t, z, &a, k, NewLong(42))
	cached := StringInfo(z, k).Hash
	if cached == 0 {
		t.Fatal("ArraySet did not populate inserted key hash")
	}
	// Corrupting the bytes in-place proves arrayKeyHash reads hdr.Hash in O(1) without rescanning bytes.
	hdr, data := stringStorage(z, k)
	if hdr.Hash != cached {
		t.Fatal("missing header hash")
	}
	if got := arrayKeyHash(z, k); got != cached {
		t.Fatalf("got=%#x want=%#x", got, cached)
	}
	_ = data
	ReleaseString(z, &k)
	ReleaseArray(z, &a)
	assertZoneEmpty(t, z)
}

func TestObjectClassNameLockFree(t *testing.T) {
	RegisterObjectClassName(77, "LockFreeClass")
	RegisterObjectClassName(100000, "SparseLockFreeClass")
	synctest.Test(t, func(t *testing.T) {
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				z := zone.New(4096)
				o1, err := NewObject(z, 77, 0, false)
				if err != nil {
					t.Error(err)
					return
				}
				o2, err := NewObject(z, 100000, 0, false)
				if err != nil {
					t.Error(err)
					return
				}
				for range 100 {
					if objectClassName(z, o1) != "LockFreeClass" || objectClassName(z, o2) != "SparseLockFreeClass" {
						t.Error("unexpected class name")
						return
					}
				}
				ReleaseObject(z, &o1)
				ReleaseObject(z, &o2)
			})
		}
		wg.Wait()
	})
}

func TestObjectComparisonsAndArrayIdenticalDeref(t *testing.T) {
	z := zone.New(16384)
	o1, err := NewObject(z, 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	o2, err := NewObject(z, 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	o3, err := NewObject(z, 2, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	ObjectSet(z, o1, 0, NewLong(10))
	ObjectSet(z, o1, 1, byteTable['a'])
	ObjectSet(z, o2, 0, stringValue(t, z, "10", 0))
	ObjectSet(z, o2, 1, byteTable['a'])
	ObjectSet(z, o3, 0, NewLong(10))
	ObjectSet(z, o3, 1, byteTable['a'])

	if !ToBoolZ(z, o1) {
		t.Fatal("object must be truthy")
	}
	if !EqualZ(z, o1, NewBool(true)) || EqualZ(z, o1, NewBool(false)) || EqualZ(z, o1, NewNull()) || EqualZ(z, o1, NewLong(1)) {
		t.Fatal("object scalar/null equality mismatch")
	}
	if !EqualZ(z, o1, o1) || !EqualZ(z, o1, o2) || EqualZ(z, o1, o3) {
		t.Fatal("object equality mismatch")
	}
	ObjectSet(z, o2, 0, NewLong(11))
	if EqualZ(z, o1, o2) {
		t.Fatal("different property value compared equal")
	}

	// ArrayIdentical with TypeReference inside bucket values
	a, b := arrayValue(t, z), arrayValue(t, z)
	r, err := NewReference(z, NewLong(7))
	if err != nil {
		t.Fatal(err)
	}
	arraySet(t, z, &a, NewLong(0), r)
	arraySet(t, z, &b, NewLong(0), NewLong(7))
	if !ArrayIdentical(z, a, b) {
		t.Fatal("ArrayIdentical did not dereference bucket reference cell")
	}
	ReleaseReference(z, &r)
	ReleaseArray(z, &a)
	ReleaseArray(z, &b)
	ReleaseObject(z, &o1)
	ReleaseObject(z, &o2)
	ReleaseObject(z, &o3)
}

func TestObjectDestructorLifecycle(t *testing.T) {
	var events []uint32
	SetObjectDestructor(func(z *zone.Zone, v Value) {
		events = append(events, ObjectInfo(z, v).ClassID)
	})
	defer SetObjectDestructor(nil)

	z := zone.New(16384)
	// 1. Immediate release calls destructor immediately and not again at Reset
	o1, err := NewObject(z, 101, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	ReleaseObject(z, &o1)
	if !slices.Equal(events, []uint32{101}) {
		t.Fatalf("events after ReleaseObject = %v; want [101]", events)
	}

	// 2. Cycle collection calls destructor during CollectCycles and not again at Reset
	o2, err := NewObject(z, 102, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	ObjectSet(z, o2, 0, o2)
	ReleaseObject(z, &o2)
	if !slices.Equal(events, []uint32{101}) {
		t.Fatalf("events before CollectCycles = %v", events)
	}
	if n := CollectCycles(z); n != 1 {
		t.Fatalf("collected=%d", n)
	}
	if !slices.Equal(events, []uint32{101, 102}) {
		t.Fatalf("events after CollectCycles = %v; want [101 102]", events)
	}

	// 3. Still-live object at Reset calls destructor during Reset cleanup
	o3, err := NewObject(z, 103, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = o3
	z.Reset(func(item zone.CleanupItem) {
		DispatchObjectDestructor(z, item)
	})
	if !slices.Equal(events, []uint32{101, 102, 103}) {
		t.Fatalf("events after Reset = %v; want [101 102 103]", events)
	}
}

func TestGCAutoThresholdAt10000Roots(t *testing.T) {
	z := zone.New(2 << 20)
	for range GCThreshold {
		o, err := NewObject(z, 1, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		ObjectSet(z, o, 0, o)
		ReleaseObject(z, &o)
	}
	if z.BufferedRoots() != GCThreshold {
		t.Fatalf("roots at threshold=%d; want %d", z.BufferedRoots(), GCThreshold)
	}
	// The 10001st candidate root triggers CollectCycles automatically!
	o, err := NewObject(z, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	ObjectSet(z, o, 0, o)
	ReleaseObject(z, &o)
	if z.BufferedRoots() != 1 {
		t.Fatalf("roots after auto-collection=%d; want 1", z.BufferedRoots())
	}
	if n := CollectCycles(z); n != 1 {
		t.Fatalf("remaining cycle=%d", n)
	}
	assertZoneEmpty(t, z)
}
