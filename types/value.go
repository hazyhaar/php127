// Package types defines pointer-free PHP values and immediate scalar operations.
// Zone-backed values are not supported by the scalar conversion methods.
package types

import "math"

type Type uint8
type Flags uint8

const (
	TypeUndef Type = iota
	TypeNull
	TypeFalse
	TypeTrue
	TypeLong
	TypeDouble
	TypeString
	TypeArray
	TypeObject
	TypeResource
	TypeReference
)

const (
	FlagZoneRequest  Flags = 1 << 0
	FlagTainted      Flags = 1 << 1
	FlagOwnedMutable Flags = 1 << 2 // Header-only; never stored in Value.Flags.
	FlagImmutable    Flags = 1 << 3
)

// Value is a 16-byte, pointer-free value aligned on an 8-byte boundary.
// Scalar values do not use Flags, Extra or DataSlot.
type Value struct {
	Type     Type
	Flags    Flags
	Extra    uint16
	DataSlot uint32
	Scalar   uint64
}

func NewUndef() Value { return Value{} }
func NewNull() Value  { return Value{Type: TypeNull} }
func NewBool(v bool) Value {
	if v {
		return Value{Type: TypeTrue}
	}
	return Value{Type: TypeFalse}
}
func NewLong(v int64) Value     { return Value{Type: TypeLong, Scalar: uint64(v)} }
func NewDouble(v float64) Value { return Value{Type: TypeDouble, Scalar: math.Float64bits(v)} }

func (v Value) IsUndef() bool  { return v.Type == TypeUndef }
func (v Value) IsNull() bool   { return v.Type == TypeNull }
func (v Value) IsBool() bool   { return v.Type == TypeFalse || v.Type == TypeTrue }
func (v Value) IsLong() bool   { return v.Type == TypeLong }
func (v Value) IsDouble() bool { return v.Type == TypeDouble }

// ToBool converts an immediate scalar, panicking on unsupported value types.
func (v Value) ToBool() bool {
	switch v.Type {
	case TypeUndef, TypeNull, TypeFalse:
		return false
	case TypeTrue:
		return true
	case TypeLong:
		return v.Scalar != 0
	case TypeDouble:
		return math.Float64frombits(v.Scalar) != 0
	default:
		panic("types: ToBool requires an immediate scalar; zone-backed conversion is not implemented")
	}
}

// FloatToInt truncates floats toward zero, reducing finite out-of-range values
// modulo 2^64 as PHP does. Non-finite floats convert to zero.
func FloatToInt(f float64) int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	if f >= -0x1p63 && f < 0x1p63 {
		return int64(f)
	}
	// Keep the final Go conversion in range: out-of-range casts are
	// implementation-dependent and do not implement PHP's wraparound.
	f = math.Mod(f, 0x1p64)
	if f >= 0x1p63 {
		f -= 0x1p64
	} else if f < -0x1p63 {
		f += 0x1p64
	}
	return int64(f)
}

// ToLong truncates floats toward zero, reducing finite out-of-range values
// modulo 2^64 as PHP does. Non-finite floats convert to zero.
func (v Value) ToLong() int64 {
	switch v.Type {
	case TypeUndef, TypeNull, TypeFalse:
		return 0
	case TypeTrue:
		return 1
	case TypeLong:
		return int64(v.Scalar)
	case TypeDouble:
		return FloatToInt(math.Float64frombits(v.Scalar))
	default:
		panic("types: ToLong requires an immediate scalar; zone-backed conversion is not implemented")
	}
}

// ToDouble converts an immediate scalar, panicking on unsupported value types.
func (v Value) ToDouble() float64 {
	switch v.Type {
	case TypeUndef, TypeNull, TypeFalse:
		return 0
	case TypeTrue:
		return 1
	case TypeLong:
		return float64(int64(v.Scalar))
	case TypeDouble:
		return math.Float64frombits(v.Scalar)
	default:
		panic("types: ToDouble requires an immediate scalar; zone-backed conversion is not implemented")
	}
}
