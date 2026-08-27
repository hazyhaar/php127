// SPDX-License-Identifier: Apache-2.0 OR MIT

package types

import (
	"math"
	"testing"
	"unsafe"
)

func TestValueSizeInvariant(t *testing.T) {
	sz := unsafe.Sizeof(Value{})
	if sz != 24 {
		t.Fatalf("Taille struct Value = %d octets, invariant dur = 24 octets (3 mots 64-bit)", sz)
	}
}

func TestValueArithmeticOverflow(t *testing.T) {
	// Normal int addition
	v1 := NewLong(100)
	v2 := NewLong(200)
	res := Add(v1, v2)
	if res.Type != TypeLong || res.ToLong() != 300 {
		t.Fatalf("100 + 200: got %v (type %d), want 300", res.ToLong(), res.Type)
	}

	// Overflow int addition -> Double
	maxInt := NewLong(math.MaxInt64)
	one := NewLong(1)
	resOverflow := Add(maxInt, one)
	if resOverflow.Type != TypeDouble {
		t.Fatalf("MaxInt64 + 1: attendu TypeDouble, got %d", resOverflow.Type)
	}
	expectedFloat := float64(math.MaxInt64) + 1.0
	if math.Abs(resOverflow.ToDouble()-expectedFloat) > 1.0 {
		t.Fatalf("MaxInt64 + 1 double value: got %f, want %f", resOverflow.ToDouble(), expectedFloat)
	}
}

func TestValueCoercionPHP84(t *testing.T) {
	// Booleans
	if NewString("").ToBool() != false {
		t.Fatalf("empty string to bool should be false")
	}
	if NewString("0").ToBool() != false {
		t.Fatalf("\"0\" to bool should be false")
	}
	if NewString("1").ToBool() != true {
		t.Fatalf("\"1\" to bool should be true")
	}
	if NewString("foo").ToBool() != true {
		t.Fatalf("\"foo\" to bool should be true")
	}

	// Longs
	if NewString("42").ToLong() != 42 {
		t.Fatalf("\"42\" to long should be 42")
	}
	if NewDouble(3.14).ToLong() != 3 {
		t.Fatalf("3.14 to long should be 3")
	}
}

func TestValuePosSIMD(t *testing.T) {
	haystack := NewString("PHP 127 Flatstack Sovereign Engine")
	needle := NewString("Flatstack")
	pos := haystack.Pos(needle)
	if pos != 8 {
		t.Fatalf("Pos(\"Flatstack\") = %d, attendu 8", pos)
	}

	notFound := haystack.Pos(NewString("Missing"))
	if notFound != -1 {
		t.Fatalf("Pos(\"Missing\") = %d, attendu -1", notFound)
	}
}

func TestValueZeroAllocArith(t *testing.T) {
	v1 := NewLong(12345)
	v2 := NewLong(67890)

	allocs := testing.AllocsPerRun(1000, func() {
		_ = Add(v1, v2)
		_ = Sub(v2, v1)
		_ = Mul(v1, v2)
		_ = Div(v2, v1)
		_ = Mod(v2, v1)
		_ = BitAnd(v1, v2)
		_ = BitOr(v1, v2)
		_ = BitXor(v1, v2)
		_ = BitNot(v1)
		_ = ShiftLeft(v1, NewLong(2))
		_ = ShiftRight(v1, NewLong(2))
		_ = Equal(v1, v2)
		_ = Identical(v1, v2)
		_ = Compare(v1, v2)
	})
	if allocs != 0 {
		t.Fatalf("Allocs/op = %.2f, attendu 0", allocs)
	}
}

func TestValueComparisons(t *testing.T) {
	a := NewLong(10)
	b := NewLong(20)
	c := NewLong(10)
	s10 := NewString("10")
	s20 := NewString("20")
	sFoo := NewString("foo")

	// Equal
	if !Equal(a, c) {
		t.Fatalf("Equal(10, 10) should be true")
	}
	if Equal(a, b) {
		t.Fatalf("Equal(10, 20) should be false")
	}
	if !Equal(a, s10) {
		t.Fatalf("Equal(10, \"10\") should be true in PHP 8.4")
	}
	if Equal(a, sFoo) {
		t.Fatalf("Equal(10, \"foo\") should be false in PHP 8.4")
	}

	// Identical
	if !Identical(a, c) {
		t.Fatalf("Identical(10, 10) should be true")
	}
	if Identical(a, s10) {
		t.Fatalf("Identical(10, \"10\") should be false (different types)")
	}

	// Compare (Spaceship)
	if Compare(a, b) != -1 {
		t.Fatalf("Compare(10, 20) should be -1")
	}
	if Compare(b, a) != 1 {
		t.Fatalf("Compare(20, 10) should be 1")
	}
	if Compare(a, c) != 0 {
		t.Fatalf("Compare(10, 10) should be 0")
	}
	if Compare(s10, s20) != -1 {
		t.Fatalf("Compare(\"10\", \"20\") should be -1")
	}

	// Inégalités
	if !Lt(a, b) || !Lte(a, b) || !Lte(a, c) {
		t.Fatalf("Lt/Lte checks failed for 10 < 20")
	}
	if !Gt(b, a) || !Gte(b, a) || !Gte(a, c) {
		t.Fatalf("Gt/Gte checks failed for 20 > 10")
	}
}

func TestValueBitwiseAndMath(t *testing.T) {
	v1 := NewLong(0b1100)
	v2 := NewLong(0b1010)

	if BitAnd(v1, v2).ToLong() != 0b1000 {
		t.Fatalf("BitAnd failed")
	}
	if BitOr(v1, v2).ToLong() != 0b1110 {
		t.Fatalf("BitOr failed")
	}
	if BitXor(v1, v2).ToLong() != 0b0110 {
		t.Fatalf("BitXor failed")
	}
	if BitNot(NewLong(0)).ToLong() != -1 {
		t.Fatalf("BitNot failed")
	}
	if ShiftLeft(NewLong(1), NewLong(3)).ToLong() != 8 {
		t.Fatalf("ShiftLeft failed")
	}
	if ShiftRight(NewLong(16), NewLong(2)).ToLong() != 4 {
		t.Fatalf("ShiftRight failed")
	}

	// Mod & Pow & Neg & Not
	if Mod(NewLong(17), NewLong(5)).ToLong() != 2 {
		t.Fatalf("Mod failed")
	}
	if Pow(NewLong(2), NewLong(8)).ToLong() != 256 {
		t.Fatalf("Pow failed")
	}
	if Neg(NewLong(42)).ToLong() != -42 {
		t.Fatalf("Neg failed")
	}
	if Not(NewBool(true)).ToBool() != false {
		t.Fatalf("Not failed")
	}
}

func TestValueTaintPropagation(t *testing.T) {
	clean := NewLong(10)
	tainted := NewLong(20).WithTaint(FlagTaintedUserInput)

	// Arithmetics inherit taint
	resAdd := Add(clean, tainted)
	if !resAdd.IsTainted() {
		t.Fatalf("Add(clean, tainted) should be tainted")
	}

	resMul := Mul(tainted, clean)
	if !resMul.IsTainted() {
		t.Fatalf("Mul(tainted, clean) should be tainted")
	}

	// Concat inherits taint
	var buf [64]byte
	strClean := NewString("hello ")
	strTainted := NewString("world").WithTaint(FlagTaintedUserInput)
	resConcat := Concat(strClean, strTainted, buf[:])
	if !resConcat.IsTainted() {
		t.Fatalf("Concat(clean, tainted) should be tainted")
	}

	// Bitwise inherits taint
	resBit := BitOr(clean, tainted)
	if !resBit.IsTainted() {
		t.Fatalf("BitOr(clean, tainted) should be tainted")
	}
}

func TestAdversarialSignedArithmeticAndCoercion(t *testing.T) {
	// 1. Signed negative arithmetic stays integer
	vAdd := Add(NewLong(-1), NewLong(-1))
	if vAdd.Type != TypeLong || vAdd.ToLong() != -2 {
		t.Fatalf("Add(-1, -1) got %v (type %d), want -2", vAdd.ToLong(), vAdd.Type)
	}

	vSub := Sub(NewLong(0), NewLong(1))
	if vSub.Type != TypeLong || vSub.ToLong() != -1 {
		t.Fatalf("Sub(0, 1) got %v (type %d), want -1", vSub.ToLong(), vSub.Type)
	}

	vMul := Mul(NewLong(-2), NewLong(-2))
	if vMul.Type != TypeLong || vMul.ToLong() != 4 {
		t.Fatalf("Mul(-2, -2) got %v (type %d), want 4", vMul.ToLong(), vMul.Type)
	}

	// 2. Negation of MinInt64 overflows to Double
	vNegMin := Neg(NewLong(math.MinInt64))
	if vNegMin.Type != TypeDouble || vNegMin.ToDouble() != -float64(math.MinInt64) {
		t.Fatalf("Neg(MinInt64) got %v (type %d), want Double", vNegMin.ToDouble(), vNegMin.Type)
	}

	// 3. String leading int coercion
	if NewString("12abc").ToLong() != 12 {
		t.Fatalf("ToLong(\"12abc\") = %d, want 12", NewString("12abc").ToLong())
	}
	if NewString("3.14").ToLong() != 3 {
		t.Fatalf("ToLong(\"3.14\") = %d, want 3", NewString("3.14").ToLong())
	}
	if NewString(" 42").ToLong() != 42 {
		t.Fatalf("ToLong(\" 42\") = %d, want 42", NewString(" 42").ToLong())
	}

	// 4. Large 64-bit int exact comparison (no float64 precision loss)
	a := NewLong(9007199254740993)
	b := NewLong(9007199254740992)
	if Equal(a, b) {
		t.Fatalf("Equal(2^53+1, 2^53) should be false")
	}

	// 5. Compare(10, "foo") in PHP 8 compares as strings: "10" < "foo" -> -1
	if Compare(NewLong(10), NewString("foo")) != -1 {
		t.Fatalf("Compare(10, \"foo\") got %d, want -1", Compare(NewLong(10), NewString("foo")))
	}

	// 6. Concat zero-alloc
	v1 := NewString("hello")
	v2 := NewString("world")
	var dst [128]byte
	allocs := testing.AllocsPerRun(100, func() {
		_ = Concat(v1, v2, dst[:])
	})
	if allocs != 0 {
		t.Fatalf("Concat with preallocated buffer had %.2f allocs, want 0", allocs)
	}
}
