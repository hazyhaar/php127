package types

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os/exec"
	"strings"
	"testing"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

func stringValue(t testing.TB, z *zone.Zone, text string, reserve uint32) Value {
	t.Helper()
	v, err := NewString(z, text, reserve)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func assertString(t testing.TB, z *zone.Zone, v Value, want string) {
	t.Helper()
	h := StringInfo(z, v)
	if got := StringData(z, v); got != want || v.Scalar != uint64(len(want)) || h.Len != uint32(len(want)) {
		t.Fatalf("value=%+v header=%+v data=%q want=%q", v, h, got, want)
	}
	_, data := stringStorage(z, v)
	if data[h.Len] != 0 || h.Cap%8 != 0 || v.DataSlot%8 != 0 || v.Extra != 0 || v.Type != TypeString || uint32(v.Flags) != h.Flags&^uint32(FlagOwnedMutable) {
		t.Fatalf("invalid string layout: %+v %+v", v, h)
	}
}

func TestStringLayoutAndReset(t *testing.T) {
	if unsafe.Sizeof(StringHeader{}) != 24 || unsafe.Alignof(StringHeader{}) != 8 || unsafe.Offsetof(StringHeader{}.Hash) != 16 {
		t.Fatal("header layout")
	}
	z := zone.New(256)
	for _, text := range []string{"", "a\x00b", "1234567", "12345678"} {
		v := stringValue(t, z, text, 0)
		assertString(t, z, v, text)
		if StringInfo(z, v).RefCount != 1 || v.Flags&FlagOwnedMutable != 0 || StringInfo(z, v).Flags&uint32(FlagOwnedMutable) == 0 || Strlen(v) != uint64(len(text)) {
			t.Fatal("metadata")
		}
		StringHash(z, v)
	}
	z.Reset(nil)
	v := stringValue(t, z, "", 16)
	assertString(t, z, v, "")
	if StringInfo(z, v).Hash != 0 {
		t.Fatal("stale hash after reset")
	}
}

func TestStringDirtyNUL(t *testing.T) {
	for _, op := range []string{"New", "Append", "SetByteAt"} {
		t.Run(op, func(t *testing.T) {
			z := zone.New(256)
			offset, err := z.Alloc(256)
			if err != nil {
				t.Fatal(err)
			}
			data, err := z.Bytes(offset, 256)
			if err != nil {
				t.Fatal(err)
			}
			for i := range data {
				data[i] = 0x5a
			}
			z.Reset(nil)
			v := stringValue(t, z, "abc", 32)
			want := "abc"
			c := stringValue(t, z, "!", 0)
			switch op {
			case "Append":
				err = Append(z, &v, c)
				want = "abc!"
			case "SetByteAt":
				err = SetByteAt(z, &v, 5, c, nil)
				want = "abc  !"
			}
			if err != nil {
				t.Fatal(err)
			}
			assertString(t, z, v, want)
		})
	}
}

func TestStringAppendEndurance(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	v := stringValue(t, z, "", 0)
	c := stringValue(t, z, "x", 0)
	for i := range 100000 {
		if err := Append(z, &v, c); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	assertString(t, z, v, strings.Repeat("x", 100000))
	used, err := z.Alloc(8)
	if err != nil || used > 4*100000 {
		t.Fatalf("used=%d err=%v", used, err)
	}
	t.Logf("100000 appended bytes consume %d Zone bytes", used)
}

func TestStringByteAtNoZoneAllocation(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	var octets [256]byte
	for i := range octets {
		octets[i] = byte(i)
	}
	v := stringValue(t, z, string(octets[:]), 0)
	before, err := z.Alloc(8)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 100000 {
		index := i % 256
		got, err := ByteAt(z, v, int64(index), nil)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if text := StringData(z, got); len(text) != 1 || text[0] != byte(index) {
			t.Fatalf("read %d: %q", i, text)
		}
	}
	after, err := z.Alloc(8)
	if err != nil || after != before+8 {
		t.Fatalf("cursor before=%d after=%d err=%v", before+8, after, err)
	}
}

func TestStringLongMinWhitespace(t *testing.T) {
	z := zone.New(1 << 16)
	var program, want strings.Builder
	for _, digits := range []string{"9223372036854775807", "9223372036854775808", "9223372036854775809"} {
		for _, sign := range []string{"", "+", "-"} {
			for _, leading := range []string{"", " \t", "000"} {
				for _, trailing := range []string{"", " ", "\t", "\n", "\r", "\v", "\f", " \t\n", "x", "\x00"} {
					a, b := sign+leading+digits+trailing, sign+digits
					if leading == " \t" {
						a = leading + sign + digits + trailing
					}
					x, y := stringValue(t, z, a, 0), stringValue(t, z, b, 0)
					fmt.Fprintf(&program, "echo (%s<=>%s),\"\\n\";", phpString(a), phpString(b))
					fmt.Fprintln(&want, CompareStringsPHP(z, x, y))
				}
			}
		}
	}
	checkStringPHP(t, program.String(), want.String())
}

func TestStringByteAtInterned(t *testing.T) {
	for i := range 256 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			z := zone.New(40)
			text := string([]byte{byte(i)})
			source := stringValue(t, z, text, 0)
			if _, err := z.Alloc(8); err != nil {
				t.Fatal(err)
			}
			v, err := ByteAt(z, source, -1, nil)
			if err != nil {
				t.Fatal(err)
			}
			empty, err := ByteAt(z, source, 1, func(StringWarning) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			assertString(t, z, empty, "")
			z.Reset(nil)
			assertString(t, z, v, text)
			h := StringInfo(z, v)
			if h.RefCount != math.MaxUint32 || v.Flags != FlagImmutable|FlagInterned || StringHash(z, v) != (5381*33+uint64(i))|1<<63 {
				t.Fatalf("not frozen: %+v %+v", v, h)
			}
			alias := RetainString(z, v)
			ReleaseString(z, &alias)
			for _, op := range []string{"Append", "SetByteAt"} {
				z.Reset(nil)
				copyValue := v
				if op == "Append" {
					err = Append(z, &copyValue, v)
				} else {
					err = SetByteAt(z, &copyValue, 1, v, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				assertString(t, z, copyValue, text+text)
				assertString(t, z, v, text)
				if StringInfo(z, v) != h || copyValue.Flags&FlagZoneRequest == 0 {
					t.Fatal("process storage changed")
				}
			}
		})
	}
}

func TestStringTwentyDigits(t *testing.T) {
	z := zone.New(4096)
	var program, want strings.Builder
	for _, n := range []int{18, 19, 20} {
		for _, sign := range []string{"", "-"} {
			for _, suffix := range []string{"e-" + fmt.Sprint(n), ".0e-" + fmt.Sprint(n)} {
				a, b := sign+"1"+strings.Repeat("0", n)+suffix, sign+"1"
				x, y := stringValue(t, z, a, 0), stringValue(t, z, b, 0)
				fmt.Fprintf(&program, "echo (%s<=>%s),\"\\n\";", phpString(a), phpString(b))
				fmt.Fprintln(&want, CompareStringsPHP(z, x, y))
			}
		}
	}
	checkStringPHP(t, program.String(), want.String())
}

func TestStringZoneValueOffset(t *testing.T) {
	z := zone.New(256)
	for _, size := range []uint32{1, 7, 8, 9, 17, 32} {
		offset, err := z.Alloc(size)
		if err != nil {
			t.Fatal(err)
		}
		v := Value{DataSlot: offset}
		data, err := z.Bytes(v.DataSlot, size)
		if err != nil || len(data) != int(size) || cap(data) != len(data) {
			t.Fatalf("value offset=%d size=%d error=%v", v.DataSlot, size, err)
		}
		got, err := z.Offset(&data[0])
		if err != nil || got != v.DataSlot {
			t.Fatalf("offset round trip=%d error=%v", got, err)
		}
	}
}

func TestStringTaintedOperations(t *testing.T) {
	for _, op := range []string{"Concat", "Substr", "AppendOwned", "AppendCopy", "AppendSelf", "ByteAt", "SetByteAt", "SetByteAtCopy"} {
		for mask := range 4 {
			t.Run(fmt.Sprintf("%s/%d", op, mask), func(t *testing.T) {
				z := zone.New(1024)
				reserve := uint32(32)
				if op == "AppendCopy" || op == "AppendSelf" {
					reserve = 0
				}
				a := stringValue(t, z, "abcdefg", reserve)
				b := stringValue(t, z, "!", 0)
				ah, _ := stringStorage(z, a)
				bh, _ := stringStorage(z, b)
				// Only headers are marked: immediate flags are not authoritative.
				if mask&1 != 0 {
					ah.Flags |= uint32(FlagTainted)
				}
				if mask&2 != 0 {
					bh.Flags |= uint32(FlagTainted)
				}
				wantTainted := mask != 0
				want := "abcdefg!"
				var got Value
				var err error
				switch op {
				case "ByteAt":
					got, err = ByteAt(z, a, 0, nil)
					want, wantTainted = "a", mask&1 != 0
				case "SetByteAt", "SetByteAtCopy":
					if op == "SetByteAtCopy" {
						RetainString(z, a)
					}
					err = SetByteAt(z, &a, 0, b, nil)
					got, want = a, "!bcdefg"
				case "Concat":
					got, err = Concat(z, a, b)
				case "Substr":
					got, err = Substr(z, a, 1, 3)
					want, wantTainted = "bcd", mask&1 != 0
				case "AppendSelf":
					err = Append(z, &a, a)
					got, want, wantTainted = a, "abcdefgabcdefg", mask&1 != 0
				default:
					err = Append(z, &a, b)
					got = a
				}
				if err != nil {
					t.Fatal(err)
				}
				assertString(t, z, got, want)
				if marked := StringInfo(z, got).Flags&uint32(FlagTainted) != 0; marked != wantTainted {
					t.Fatalf("tainted = %v, want %v", marked, wantTainted)
				}
			})
		}
	}
}

func TestStringAppendZoneOffsetCollision(t *testing.T) {
	suffix := byteTable['q']
	z := zone.New(suffix.DataSlot + 128)
	for left := suffix.DataSlot; left > 0; {
		n := min(left, 64)
		if _, err := z.Alloc(n); err != nil {
			t.Fatal(err)
		}
		left -= n
	}
	v := stringValue(t, z, "AB", 0)
	if v.DataSlot != suffix.DataSlot {
		t.Fatal("offsets do not collide")
	}
	if err := Append(z, &v, suffix); err != nil {
		t.Fatal(err)
	}
	assertString(t, z, v, "ABq")
}

func TestStringTaintedByteProvenance(t *testing.T) {
	z := zone.New(4096)
	v := stringValue(t, z, "q", 0)
	h, _ := stringStorage(z, v)
	h.Flags |= uint32(FlagTainted)
	for _, index := range []int64{0, 9} {
		got, err := ByteAt(z, v, index, func(StringWarning) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if got.Flags&FlagTainted == 0 {
			t.Fatal("byte lost taint")
		}
		if StringInfo(z, byteTable['q']).Flags&uint32(FlagTainted) != 0 || StringInfo(z, emptyString).Flags&uint32(FlagTainted) != 0 {
			t.Fatal("shared bytes contaminated")
		}
		x, err := Concat(z, got, byteTable['!'])
		if err != nil {
			t.Fatal(err)
		}
		if StringInfo(z, x).Flags&uint32(FlagTainted) == 0 {
			t.Fatal("concat lost provenance")
		}
		x, err = Substr(z, got, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if StringInfo(z, x).Flags&uint32(FlagTainted) == 0 {
			t.Fatal("substr lost provenance")
		}
		x = got
		if err := Append(z, &x, byteTable['!']); err != nil {
			t.Fatal(err)
		}
		if StringInfo(z, x).Flags&uint32(FlagTainted) == 0 {
			t.Fatal("writable copy lost provenance")
		}
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if _, err := ByteAt(z, v, 0, nil); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatal(allocs)
	}
}

func TestStringM21(t *testing.T) {
	t.Run("exact_capacity", func(t *testing.T) {
		z := zone.New(128)
		v := stringValue(t, z, "abcdef", 0)
		old := v.DataSlot
		if StringInfo(z, v).Cap != 8 {
			t.Fatal("expected capacity equal to final length plus NUL")
		}
		if err := Append(z, &v, byteTable['!']); err != nil {
			t.Fatal(err)
		}
		assertString(t, z, v, "abcdef!")
		if v.DataSlot != old {
			t.Fatal("exact capacity unnecessarily copied")
		}
	})
	for _, mode := range []string{"owned", "shared", "unowned", "immutable", "interned", "full"} {
		t.Run(mode, func(t *testing.T) {
			z := zone.New(512)
			reserve := uint32(32)
			if mode == "full" {
				reserve = 0
			}
			v := stringValue(t, z, "abcdefg", reserve)
			if mode == "interned" {
				var err error
				v, err = NewInternedString(z, "abcdefg")
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "shared" {
				RetainString(z, v)
			}
			if mode == "unowned" || mode == "immutable" {
				h, _ := stringStorage(z, v)
				if mode == "unowned" {
					h.Flags &^= uint32(FlagOwnedMutable)
				} else {
					h.Flags |= uint32(FlagImmutable)
				}
				v.Flags = Flags(h.Flags) &^ FlagOwnedMutable
			}
			old := v
			oldHash := StringHash(z, v)
			before := StringInfo(z, v)
			suffix := stringValue(t, z, "!", 0)
			if err := Append(z, &v, suffix); err != nil {
				t.Fatal(err)
			}
			assertString(t, z, v, "abcdefg!")
			if (v.DataSlot == old.DataSlot) != (mode == "owned") {
				t.Fatal("M21 allocation decision")
			}
			if StringInfo(z, v).Hash != 0 || StringHash(z, v) == oldHash {
				t.Fatal("hash not invalidated")
			}
			if mode == "shared" || mode == "interned" {
				raw, err := z.Bytes(old.DataSlot, 24+before.Cap)
				if err != nil || string(raw[24:31]) != "abcdefg" || raw[31] != 0 {
					t.Fatal("old buffer changed")
				}
				h := *(*StringHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
				wantRefs := before.RefCount - 1
				if mode == "interned" {
					wantRefs = math.MaxUint32
				}
				if h.RefCount != wantRefs || h.Hash != oldHash {
					t.Fatalf("old header=%+v", h)
				}
			} else if mode != "owned" {
				if reused, err := z.Alloc(stringHeaderSize + before.Cap); err != nil || reused != old.DataSlot {
					t.Fatalf("old unique buffer not recycled: %d %v", reused, err)
				}
			}
		})
	}
}

func TestStringAliasingAndReferences(t *testing.T) {
	z := zone.New(1024)
	v := stringValue(t, z, "abc", 32)
	if err := Append(z, &v, v); err != nil {
		t.Fatal(err)
	}
	assertString(t, z, v, "abcabc")
	alias := RetainString(z, v)
	if err := SetByteAt(z, &v, -1, alias, func(StringWarning) error { return nil }); err != nil {
		t.Fatal(err)
	}
	assertString(t, z, alias, "abcabc")
	assertString(t, z, v, "abcaba")
	if StringInfo(z, alias).RefCount != 1 {
		t.Fatal("reference not released")
	}
	ReleaseString(z, &alias)
	if alias != NewUndef() {
		t.Fatal("released value remains live")
	}
	i, err := NewInternedString(z, "literal")
	if err != nil {
		t.Fatal(err)
	}
	before := StringInfo(z, i)
	copyValue := RetainString(z, i)
	ReleaseString(z, &copyValue)
	if StringInfo(z, i) != before {
		t.Fatal("interned refcount changed")
	}
}

func TestStringFailureAtomicity(t *testing.T) {
	z := zone.New(64)
	v := stringValue(t, z, "1234567", 0)
	suffix := stringValue(t, z, "!", 0)
	StringHash(z, v)
	old, header := v, StringInfo(z, v)
	for _, op := range []func() error{
		func() error { return Append(z, &v, suffix) },
		func() error { return SetByteAt(z, &v, 7, suffix, nil) },
	} {
		if err := op(); !errors.Is(err, zone.ErrExhausted) {
			t.Fatalf("error=%v", err)
		}
		if v != old || StringInfo(z, v) != header {
			t.Fatal("failed mutation changed value")
		}
		assertString(t, z, v, "1234567")
	}
	if _, err := NewString(z, "", math.MaxUint32); !errors.Is(err, ErrStringTooLarge) {
		t.Fatalf("capacity overflow: %v", err)
	}
	if err := SetByteAt(z, &v, math.MaxInt64, suffix, nil); !errors.Is(err, ErrStringTooLarge) {
		t.Fatalf("index overflow: %v", err)
	}
}

func TestStringHash(t *testing.T) {
	z := zone.New(128)
	v := stringValue(t, z, "abc", 0)
	if got := StringHash(z, v); got != uint64(193485963)|1<<63 || StringInfo(z, v).Hash != got {
		t.Fatalf("hash=%d", got)
	}
	if StringHash(z, v) != uint64(193485963)|1<<63 {
		t.Fatal("cached hash")
	}
	if StringHash(z, stringValue(t, z, "", 0)) != uint64(5381)|1<<63 {
		t.Fatal("empty hash")
	}
}

func phpString(text string) string { return "hex2bin('" + hex.EncodeToString([]byte(text)) + "')" }

func checkStringPHP(t *testing.T, program, want string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", `if(PHP_VERSION!=="8.3.6"){fwrite(STDERR,PHP_VERSION);exit(99);} `+program)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	got, err := cmd.Output()
	if err != nil || stderr.Len() != 0 || string(got) != want {
		t.Fatalf("PHP error=%v stderr=%q\ngot=%q\nwant=%q", err, stderr.String(), got, want)
	}
}

func TestStringPHPDifferential(t *testing.T) {
	z := zone.New(1 << 20)
	var program, want strings.Builder
	program.WriteString(`set_error_handler(function($n,$s){echo "W:",$n,":",$s,"\n";});`)
	warn := func(w StringWarning) error {
		var buf [128]byte
		fmt.Fprintf(&want, "W:2:%s\n", w.AppendMessage(buf[:0]))
		return nil
	}
	texts := []string{"", "abc", "a\x00b", "\xc3\xa9\xff", "12345678"}
	for _, text := range texts {
		v := stringValue(t, z, text, 16)
		fmt.Fprintf(&program, "$s=%s;echo strlen($s),\"\\n\";", phpString(text))
		fmt.Fprintln(&want, Strlen(v))
		for _, index := range []int64{math.MinInt64, -9, -3, -1, 0, 1, 3, 9, math.MaxInt64} {
			expr := fmt.Sprint(index)
			if index == math.MinInt64 {
				expr = "PHP_INT_MIN"
			}
			fmt.Fprintf(&program, "echo bin2hex($s[%s]),\"\\n\";", expr)
			got, err := ByteAt(z, v, index, warn)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&want, "%x\n", StringData(z, got))
		}
		for _, start := range []int64{math.MinInt64, -9, -1, 0, 2, 9, math.MaxInt64} {
			for _, length := range []int64{math.MinInt64, -9, -1, 0, 2, 9, math.MaxInt64} {
				se, le := fmt.Sprint(start), fmt.Sprint(length)
				if start == math.MinInt64 {
					se = "PHP_INT_MIN"
				}
				if length == math.MinInt64 {
					le = "PHP_INT_MIN"
				}
				fmt.Fprintf(&program, "echo bin2hex(substr($s,%s,%s)),\"\\n\";", se, le)
				got, err := Substr(z, v, start, length)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&want, "%x\n", StringData(z, got))
			}
		}
		for _, other := range texts {
			b := stringValue(t, z, other, 0)
			joined, err := Concat(z, v, b)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&program, "$b=%s;echo bin2hex($s.$b),\"\\n\"; $c=$s;$c.=$b;echo bin2hex($c),\"\\n\";", phpString(other))
			fmt.Fprintf(&want, "%x\n", StringData(z, joined))
			c := RetainString(z, v)
			if err := Append(z, &c, b); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&want, "%x\n", StringData(z, c))
		}
	}
	checkStringPHP(t, program.String(), want.String())
}

func TestStringWritePHP(t *testing.T) {
	z := zone.New(1 << 16)
	var program, want strings.Builder
	program.WriteString(`set_error_handler(function($n,$s){echo "W:",$n,":",$s,"\n";});`)
	warn := func(w StringWarning) error {
		var buf [128]byte
		fmt.Fprintf(&want, "W:2:%s\n", w.AppendMessage(buf[:0]))
		return nil
	}
	for _, text := range []string{"abc", ""} {
		for _, index := range []int64{-4, -1, 0, 2, 6} {
			for _, replacement := range []string{"x", "xy", "", "\x00"} {
				v := stringValue(t, z, text, 16)
				c := stringValue(t, z, replacement, 0)
				fmt.Fprintf(&program, "$s=%s;try{$s[%d]=%s;}catch(Error $e){echo \"E:\",$e->getMessage(),\"\\n\";}echo bin2hex($s),\"\\n\";", phpString(text), index, phpString(replacement))
				if err := SetByteAt(z, &v, index, c, warn); err != nil {
					fmt.Fprintf(&want, "E:%s\n", err)
				}
				fmt.Fprintf(&want, "%x\n", StringData(z, v))
			}
		}
	}
	checkStringPHP(t, program.String(), want.String())
}

func TestStringComparePHP(t *testing.T) {
	texts := []string{"", "10", "2", "0.5", "0.50", "10a", "9007199254740993", "9007199254740992", "9223372036854775807", "9223372036854775808", "9223372036854775809", "-9223372036854775808", "-9223372036854775809", "1e999", "2e999", "-1e999", "1e-999", "0", "-0", "0e999999999999999999999", " \t+01.0\r\n", "1_0", "0x10", "INF", "NAN", "1e", ".1", "1.", "\x001", "1\x00", "\xff", "1.7976931348623158e308", "1.7976931348623159e308", "5e-324"}
	z := zone.New(1 << 20)
	for _, a := range texts {
		var program, want strings.Builder
		x := stringValue(t, z, a, 0)
		fmt.Fprintf(&program, "$a=%s;", phpString(a))
		for _, b := range texts {
			y := stringValue(t, z, b, 0)
			fmt.Fprintf(&program, "$b=%s;echo ($a<=>$b),\":\",(strcmp($a,$b)<=>0),\"\\n\";", phpString(b))
			fmt.Fprintf(&want, "%d:%d\n", CompareStringsPHP(z, x, y), CompareBinary(z, x, y))
		}
		t.Run(fmt.Sprintf("%x", a), func(t *testing.T) { checkStringPHP(t, program.String(), want.String()) })
	}
}

func TestStringWarningAbort(t *testing.T) {
	z := zone.New(256)
	v := stringValue(t, z, "abc", 16)
	c := stringValue(t, z, "xy", 0)
	stop := errors.New("handler exception")
	warn := func(StringWarning) error { return stop }
	if _, err := ByteAt(z, v, 9, warn); err != stop {
		t.Fatal(err)
	}
	old := v
	if err := SetByteAt(z, &v, 0, c, warn); err != stop {
		t.Fatal(err)
	}
	if v != old {
		t.Fatal("handler abort changed value")
	}
	assertString(t, z, v, "abc")
}

func TestStringWriteM21(t *testing.T) {
	t.Run("exact_capacity", func(t *testing.T) {
		z := zone.New(128)
		v := stringValue(t, z, "123456", 0)
		old := v.DataSlot
		if StringInfo(z, v).Cap != 8 {
			t.Fatal("expected capacity equal to final length plus NUL")
		}
		if err := SetByteAt(z, &v, 6, byteTable['!'], nil); err != nil {
			t.Fatal(err)
		}
		assertString(t, z, v, "123456!")
		if v.DataSlot != old {
			t.Fatal("exact capacity unnecessarily copied")
		}
	})
	for _, mode := range []string{"owned", "shared", "unowned", "interned", "full"} {
		t.Run(mode, func(t *testing.T) {
			z := zone.New(512)
			reserve := uint32(32)
			if mode == "full" {
				reserve = 0
			}
			v := stringValue(t, z, "1234567", reserve)
			if mode == "interned" {
				var err error
				v, err = NewInternedString(z, "1234567")
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "shared" {
				RetainString(z, v)
			}
			if mode == "unowned" {
				h, _ := stringStorage(z, v)
				h.Flags &^= uint32(FlagOwnedMutable)
				v.Flags = Flags(h.Flags) &^ FlagOwnedMutable
			}
			old, oldHash := v, StringHash(z, v)
			c := stringValue(t, z, "\x00", 0)
			if err := SetByteAt(z, &v, 8, c, nil); err != nil {
				t.Fatal(err)
			}
			assertString(t, z, v, "1234567 \x00")
			if (v.DataSlot == old.DataSlot) != (mode == "owned") {
				t.Fatal("write bypassed M21")
			}
			if StringInfo(z, v).Hash != 0 || StringHash(z, v) == oldHash {
				t.Fatal("write retained hash")
			}
			if mode == "interned" || mode == "shared" {
				assertString(t, z, old, "1234567")
			}
		})
	}
}

func TestStringReadAllocationFailures(t *testing.T) {
	z := zone.New(32)
	v := stringValue(t, z, "abc", 0)
	header := StringInfo(z, v)
	for _, op := range []func() (Value, error){
		func() (Value, error) { return NewString(z, "", 0) },
		func() (Value, error) { return NewInternedString(z, "") },
		func() (Value, error) { return Concat(z, v, v) },
		func() (Value, error) { return Substr(z, v, 0, 1) },
	} {
		got, err := op()
		if got != NewUndef() || !errors.Is(err, zone.ErrExhausted) {
			t.Fatalf("got=%+v error=%v", got, err)
		}
		if StringInfo(z, v) != header {
			t.Fatal("read modified header")
		}
	}
	assertString(t, z, v, "abc")
}

func TestStringReferenceOverflow(t *testing.T) {
	z := zone.New(32)
	v := stringValue(t, z, "abc", 0)
	h, _ := stringStorage(z, v)
	h.RefCount = math.MaxUint32 - 1
	defer func() {
		if recover() == nil || h.RefCount != math.MaxUint32-1 {
			t.Error("reference overflow not rejected atomically")
		}
	}()
	RetainString(z, v)
}

func TestStringMissingWarningHandler(t *testing.T) {
	z := zone.New(32)
	v := stringValue(t, z, "abc", 0)
	defer func() {
		if recover() == nil {
			t.Error("warning silently discarded")
		}
	}()
	ByteAt(z, v, 9, nil)
}

func TestStringNumericBoundaries(t *testing.T) {
	// Derive the binary64 overflow midpoint independently of production's decimal constant.
	threshold := new(big.Int).Lsh(big.NewInt(1), 1024)
	threshold.Sub(threshold, new(big.Int).Lsh(big.NewInt(1), 970))
	below := new(big.Int).Sub(threshold, big.NewInt(1)).String()
	above := new(big.Int).Add(threshold, big.NewInt(1)).String()
	for _, text := range []string{below, threshold.String(), above, "-" + below, "-" + threshold.String()} {
		got, _, ok := numericString(text)
		if !ok {
			t.Fatalf("rejected numeric input %s", text)
		}
		want, _, err := big.ParseFloat(text, 10, 2048, big.ToNearestEven)
		if err != nil {
			t.Fatal(err)
		}
		wantFloat, _ := want.Float64()
		if got.ToDouble() != wantFloat {
			t.Fatalf("input=%s got=%g want=%g", text, got.ToDouble(), wantFloat)
		}
		if allocs := testing.AllocsPerRun(100, func() { numericString(text) }); allocs != 0 {
			t.Fatalf("boundary allocations=%g", allocs)
		}
	}
	texts := []string{below, threshold.String(), above, "000000000000000000000000000000000000000001", "1" + strings.Repeat("0", 12000) + "e-12000", "0." + strings.Repeat("0", 12000) + "1e12001", "1e+0000000000000000000001", ".", "1.e1", "1.2.3", "+", "  ", "\v1\f", "\x851", "0b10", "01", "-0009223372036854775809"}
	z := zone.New(1 << 18)
	var program, want strings.Builder
	for _, a := range texts {
		x := stringValue(t, z, a, 0)
		fmt.Fprintf(&program, "$a=%s;", phpString(a))
		for _, b := range []string{"0", "1", "10", "1e308", "1e999", "-9223372036854775808"} {
			y := stringValue(t, z, b, 0)
			fmt.Fprintf(&program, "echo ($a<=>%s),\"\\n\";", phpString(b))
			fmt.Fprintln(&want, CompareStringsPHP(z, x, y))
		}
		checkStringPHP(t, program.String(), want.String())
		program.Reset()
		want.Reset()
	}
}

func TestStringZeroHeap(t *testing.T) {
	z := zone.New(8192)
	for _, text := range []string{"abc", "1.5", "9223372036854775809", "1e999", "1.7976931348623159e308", strings.Repeat("9", 1000)} {
		allocs := testing.AllocsPerRun(100, func() {
			z.Reset(nil)
			a, err := NewString(z, text, 0)
			if err != nil {
				panic(err)
			}
			b, err := NewString(z, "2", 0)
			if err != nil {
				panic(err)
			}
			CompareStringsPHP(z, a, b)
			CompareBinary(z, a, b)
			StringHash(z, a)
			if _, err = Concat(z, a, b); err != nil {
				panic(err)
			}
			if _, err = Substr(z, a, 0, 1); err != nil {
				panic(err)
			}
			if _, err = ByteAt(z, a, 0, nil); err != nil {
				panic(err)
			}
			if err = Append(z, &a, b); err != nil {
				panic(err)
			}
			if err = SetByteAt(z, &a, 0, b, nil); err != nil {
				panic(err)
			}
		})
		if allocs != 0 {
			t.Errorf("input length=%d allocations=%g", len(text), allocs)
		}
	}
}

func BenchmarkString(b *testing.B) {
	for _, op := range []string{"New", "Concat", "AppendOwned", "AppendCoW", "Substr", "ByteAt", "SetByteAt", "Hash", "CompareBinary", "ComparePHP"} {
		b.Run(op, func(b *testing.B) {
			z := zone.New(4096)
			b.ReportAllocs()
			for b.Loop() {
				z.Reset(nil)
				a, err := NewString(z, "123.50", 32)
				if err != nil {
					b.Fatal(err)
				}
				c, err := NewString(z, "2", 0)
				if err != nil {
					b.Fatal(err)
				}
				switch op {
				case "New":
					_, err = NewString(z, "bytes", 0)
				case "Concat":
					_, err = Concat(z, a, c)
				case "AppendOwned":
					err = Append(z, &a, c)
				case "AppendCoW":
					RetainString(z, a)
					err = Append(z, &a, c)
				case "Substr":
					_, err = Substr(z, a, 1, 3)
				case "ByteAt":
					_, err = ByteAt(z, a, -1, nil)
				case "SetByteAt":
					err = SetByteAt(z, &a, 0, c, nil)
				case "Hash":
					StringHash(z, a)
				case "CompareBinary":
					CompareBinary(z, a, c)
				case "ComparePHP":
					CompareStringsPHP(z, a, c)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestFormatLongAndConcatStringLong(t *testing.T) {
	z := zone.New(65536)
	for n := int64(0); n <= 9; n++ {
		v, err := FormatLong(z, n)
		if err != nil {
			t.Fatalf("FormatLong(%d): %v", n, err)
		}
		if v != byteTable['0'+byte(n)] || z.LiveBytes() != 0 {
			t.Fatalf("FormatLong(%d) = %+v live=%d, expected interned byteTable entry", n, v, z.LiveBytes())
		}
		assertString(t, z, v, fmt.Sprint(n))
	}

	nums := []int64{0, 1, 9, 10, 42, 63, 99999, -1, -5, -42, math.MinInt64, math.MaxInt64}
	for _, n := range nums {
		z.Reset(nil)
		v, err := FormatLong(z, n)
		if err != nil {
			t.Fatalf("FormatLong(%d): %v", n, err)
		}
		want := fmt.Sprint(n)
		assertString(t, z, v, want)
		if !bytes.Equal(StringBytes(z, v), []byte(want)) {
			t.Fatalf("StringBytes(%d) = %q, want %q", n, StringBytes(z, v), want)
		}
		ReleaseString(z, &v)
		if z.LiveBytes() != 0 {
			t.Fatalf("FormatLong(%d) leaked %d Zone bytes after release", n, z.LiveBytes())
		}
	}

	prefixes := []string{"", "k_", "item_", "long_prefix_1234567890_"}
	for _, prefix := range prefixes {
		for _, n := range nums {
			z.Reset(nil)
			p := stringValue(t, z, prefix, 0)
			ns, err := FormatLong(z, n)
			if err != nil {
				t.Fatal(err)
			}
			wantVal, err := Concat(z, p, ns)
			if err != nil {
				t.Fatal(err)
			}
			gotVal, err := ConcatStringLong(z, p, n)
			if err != nil {
				t.Fatalf("ConcatStringLong(%q, %d): %v", prefix, n, err)
			}
			wantText := prefix + fmt.Sprint(n)
			assertString(t, z, gotVal, wantText)
			if StringHash(z, gotVal) != StringHash(z, wantVal) {
				t.Fatalf("ConcatStringLong(%q, %d) hash=%#x != Concat hash=%#x", prefix, n, StringHash(z, gotVal), StringHash(z, wantVal))
			}
			ReleaseString(z, &gotVal)
			ReleaseString(z, &wantVal)
			ReleaseString(z, &ns)
			ReleaseString(z, &p)
			if z.LiveBytes() != 0 {
				t.Fatalf("leaked %d Zone bytes", z.LiveBytes())
			}
		}
	}

	if _, err := ConcatStringLong(z, NewLong(1), 2); !errors.Is(err, ErrStringType) {
		t.Fatalf("ConcatStringLong non-string error = %v, want %v", err, ErrStringType)
	}

	z.Reset(nil)
	p, err := NewStringLiteral(z, "k_")
	if err != nil {
		t.Fatal(err)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		v, err := ConcatStringLong(z, p, 63)
		if err != nil {
			panic(err)
		}
		ReleaseString(z, &v)
		fv, err := FormatLong(z, -9223372036854775808)
		if err != nil {
			panic(err)
		}
		ReleaseString(z, &fv)
	}); allocs != 0 {
		t.Fatalf("unexpected heap allocations: %g", allocs)
	}
}

