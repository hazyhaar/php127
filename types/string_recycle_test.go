package types

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

func TestStringDoubleRelease(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "linked"}[linked], func(t *testing.T) {
			z := zone.New(160)
			guard := stringValue(t, z, "guard", 0)
			first := stringValue(t, z, "first", 0)
			victim := stringValue(t, z, "victim", 0)
			last := stringValue(t, z, "last", 0)
			firstOffset, victimOffset := first.DataSlot, victim.DataSlot
			if linked {
				ReleaseString(z, &first)
			}
			stale := victim // Deliberately not retained: exercise caller misuse.
			ReleaseString(z, &victim)
			raw, err := z.Bytes(0, 128)
			if err != nil {
				t.Fatal(err)
			}
			before := bytes.Clone(raw)
			func() {
				defer func() {
					if got := recover(); got != "types: invalid string header" {
						t.Fatalf("double release panic = %v", got)
					}
				}()
				ReleaseString(z, &stale)
			}()
			if !bytes.Equal(raw, before) {
				t.Fatal("double release changed memory")
			}
			want := []uint32{victimOffset, 128}
			if linked {
				want = []uint32{victimOffset, firstOffset, 128}
			}
			for _, offset := range want {
				v := stringValue(t, z, "reuse", 0)
				if v.DataSlot != offset || v.DataSlot%8 != 0 {
					t.Fatalf("reused offset = %d, want %d", v.DataSlot, offset)
				}
			}
			assertString(t, z, guard, "guard")
			assertString(t, z, last, "last")
		})
	}
}

func TestStringRecycleVariableSizes(t *testing.T) {
	z := zone.New(2 << 20)
	rng := rand.New(rand.NewPCG(1, 3))
	text := strings.Repeat("x", 60000)
	var previous Value
	for i := range 5000 {
		length := 1 + rng.IntN(len(text))
		current, err := NewString(z, text[:length], 0)
		if err != nil {
			t.Fatalf("iteration %d length %d: %v", i, length, err)
		}
		assertString(t, z, current, text[:length])
		if previous.Type == TypeString {
			assertString(t, z, previous, text[:previous.Scalar])
			ReleaseString(z, &previous)
		}
		previous = current
	}
	ReleaseString(z, &previous)
}

func TestStringRecycleEndurance(t *testing.T) {
	z := zone.New(4096)
	text := strings.Repeat("x", 64)
	want := text + text
	for i := range 100000 {
		v := stringValue(t, z, text, 0)
		if err := Append(z, &v, v); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if StringData(z, v) != want {
			t.Fatalf("iteration %d: self append corrupted", i)
		}
		ReleaseString(z, &v)
	}
	if n := testing.AllocsPerRun(1000, func() {
		v, err := NewString(z, text, 0)
		if err != nil {
			panic(err)
		}
		if err := Append(z, &v, v); err != nil {
			panic(err)
		}
		ReleaseString(z, &v)
	}); n != 0 {
		t.Fatalf("allocations = %g", n)
	}
}

func TestStringImmediateFlagsAndHash(t *testing.T) {
	z := zone.New(256)
	v := stringValue(t, z, "abc", 0)
	a := RetainString(z, v)
	if v.Flags&FlagOwnedMutable != 0 || a.Flags&FlagOwnedMutable != 0 {
		t.Fatal("ownership leaked into immediate flags")
	}
	if StringInfo(z, v).Flags&uint32(FlagOwnedMutable) == 0 {
		t.Fatal("header lost ownership")
	}
	if StringHash(z, v) != uint64(193485963)|1<<63 || StringHash(z, emptyString)&(1<<63) == 0 {
		t.Fatal("hash high bit absent")
	}
	if err := Append(z, &v, a); err != nil {
		t.Fatal(err)
	}
	if v.Flags&FlagOwnedMutable != 0 {
		t.Fatal("copy leaked ownership")
	}
	assertString(t, z, a, "abc")
}

func TestStringZeroRawHash(t *testing.T) {
	// Base-33 digits encode the additive inverse of the seed after 13 steps.
	var text [13]byte
	power := uint64(1)
	for range text {
		power *= 33
	}
	remainder := uint64(5381) * power
	remainder = -remainder
	for i := len(text) - 1; i >= 0; i-- {
		text[i] = byte(remainder % 33)
		remainder /= 33
	}
	rawHash := uint64(5381)
	for _, c := range text {
		rawHash = rawHash*33 + uint64(c)
	}
	if rawHash != 0 {
		t.Fatalf("test input hash = %d", rawHash)
	}
	z := zone.New(64)
	v := stringValue(t, z, string(text[:]), 0)
	if got := StringHash(z, v); got != 1<<63 || StringInfo(z, v).Hash != got {
		t.Fatalf("zero raw hash not cached: %d", got)
	}
}

func TestStringReleaseLastOwner(t *testing.T) {
	z := zone.New(64)
	v := stringValue(t, z, "abc", 0)
	alias := RetainString(z, v)
	guard := stringValue(t, z, "guard", 0)
	oldOffset := v.DataSlot
	ReleaseString(z, &v)
	assertString(t, z, alias, "abc")
	if _, err := NewString(z, "new", 0); err != zone.ErrExhausted {
		t.Fatalf("live alias recycled: %v", err)
	}
	ReleaseString(z, &alias)
	reused := stringValue(t, z, "new", 0)
	if reused.DataSlot != oldOffset {
		t.Fatal("last owner did not recycle storage")
	}
	assertString(t, z, guard, "guard")
}

func BenchmarkStringRecycle(b *testing.B) {
	z := zone.New(4096)
	text := strings.Repeat("x", 64)
	b.ReportAllocs()
	for b.Loop() {
		v, err := NewString(z, text, 0)
		if err != nil {
			b.Fatal(err)
		}
		if err := Append(z, &v, v); err != nil {
			b.Fatal(err)
		}
		ReleaseString(z, &v)
	}
}
