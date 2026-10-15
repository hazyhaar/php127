package types

import "math"

func requireNumbers(a, b Value) {
	if (a.Type != TypeLong && a.Type != TypeDouble) || (b.Type != TypeLong && b.Type != TypeDouble) {
		panic("types: arithmetic requires Long or Double; other operand types are not implemented")
	}
}

func requireScalars(a, b Value) {
	if a.Type > TypeDouble || b.Type > TypeDouble {
		panic("types: comparison requires immediate scalars; zone-backed comparison is not implemented")
	}
}

// Add adds Long or Double operands, promoting integer overflow to Double.
func Add(a, b Value) Value {
	requireNumbers(a, b)
	taint := (a.Flags | b.Flags) & FlagTainted
	if a.Type == TypeLong && b.Type == TypeLong {
		x, y := int64(a.Scalar), int64(b.Scalar)
		result := x + y
		if (x^result)&(y^result) >= 0 {
			out := NewLong(result)
			out.Flags |= taint
			return out
		}
	}
	out := NewDouble(a.ToDouble() + b.ToDouble())
	out.Flags |= taint
	return out
}

// Sub subtracts Long or Double operands, promoting integer overflow to Double.
func Sub(a, b Value) Value {
	requireNumbers(a, b)
	taint := (a.Flags | b.Flags) & FlagTainted
	if a.Type == TypeLong && b.Type == TypeLong {
		x, y := int64(a.Scalar), int64(b.Scalar)
		result := x - y
		if (x^y)&(x^result) >= 0 {
			out := NewLong(result)
			out.Flags |= taint
			return out
		}
	}
	out := NewDouble(a.ToDouble() - b.ToDouble())
	out.Flags |= taint
	return out
}

// Mul multiplies Long or Double operands, promoting integer overflow to Double.
func Mul(a, b Value) Value {
	requireNumbers(a, b)
	taint := (a.Flags | b.Flags) & FlagTainted
	if a.Type == TypeLong && b.Type == TypeLong {
		x, y := int64(a.Scalar), int64(b.Scalar)
		result := x * y
		// MinInt64 / -1 wraps in Go, so it cannot validate this product.
		if x == 0 || (result/x == y && !(x == -1 && y == math.MinInt64)) {
			out := NewLong(result)
			out.Flags |= taint
			return out
		}
	}
	out := NewDouble(a.ToDouble() * b.ToDouble())
	out.Flags |= taint
	return out
}

// Div preserves Long for exact integer quotients, except MinInt64 / -1.
// A zero divisor returns Undef and DivisionByZeroError, without allocation.
func Div(a, b Value) (Value, error) {
	requireNumbers(a, b)
	if b.ToDouble() == 0 {
		return NewUndef(), DivisionByZeroError{}
	}
	taint := (a.Flags | b.Flags) & FlagTainted
	if a.Type == TypeLong && b.Type == TypeLong {
		x, y := int64(a.Scalar), int64(b.Scalar)
		if !(x == math.MinInt64 && y == -1) && x%y == 0 {
			out := NewLong(x / y)
			out.Flags |= taint
			return out, nil
		}
	}
	out := NewDouble(a.ToDouble() / b.ToDouble())
	out.Flags |= taint
	return out, nil
}

// Identical implements scalar ===. Floating equality is IEEE equality, so
// signed zeros are identical and NaN is not identical even to itself.
// Undef remains distinct from Null as an internal value tag.
func Identical(a, b Value) bool {
	requireScalars(a, b)
	if a.Type != b.Type {
		return false
	}
	if a.Type <= TypeTrue {
		return true
	}
	if a.Type == TypeDouble {
		return math.Float64frombits(a.Scalar) == math.Float64frombits(b.Scalar)
	}
	return a.Scalar == b.Scalar
}

// CompareScalaires implements PHP's scalar <=>, treating Undef as Null.
// Booleans and null force boolean comparison with other immediate scalars.
// Like PHP, an unordered floating comparison (NaN) returns 1 in either order;
// this operation therefore does not define a total order for sorting.
func CompareScalaires(a, b Value) int {
	requireScalars(a, b)
	if a.Type <= TypeTrue || b.Type <= TypeTrue {
		x, y := a.ToBool(), b.ToBool()
		if x == y {
			return 0
		}
		if !x {
			return -1
		}
		return 1
	}
	if a.Type == TypeLong && b.Type == TypeLong {
		x, y := int64(a.Scalar), int64(b.Scalar)
		if x == y {
			return 0
		}
		if x < y {
			return -1
		}
		return 1
	}
	x, y := a.ToDouble(), b.ToDouble()
	if x == y {
		return 0
	}
	if x < y {
		return -1
	}
	return 1
}
