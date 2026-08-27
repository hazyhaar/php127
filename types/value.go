// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/types: Modèle de valeurs 24 octets (tagged union) et coercition PHP 8.4

package types

import (
	"math"
	"strconv"
	"strings"
	"unsafe"

	"github.com/hazyhaar/c2pkg/c2ryu"
	"github.com/hazyhaar/c2pkg/c2strsimd"
)

const (
	TypeUndef uint8 = iota
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
	FlagNone             uint8 = 0
	FlagTaintedUserInput uint8 = 1 << 0 // Entrée utilisateur non assainie ($_GET, $_POST, etc.)
	FlagTaintedFileIO    uint8 = 1 << 1 // Donnée issue de flux I/O externe
	FlagHighEntropy      uint8 = 1 << 2 // Donnée à haute entropie (clé/secret/payload chiffré)
	FlagTrustedSystem    uint8 = 1 << 3 // Donnée certifiée par le runtime
)

type Value struct {
	Type  uint8
	Flags uint8
	Extra uint16
	Len   uint32
	Data  int64
	Ptr   unsafe.Pointer
}

// Constructeurs
func NewNull() Value {
	return Value{Type: TypeNull}
}

func NewBool(b bool) Value {
	if b {
		return Value{Type: TypeTrue, Data: 1}
	}
	return Value{Type: TypeFalse, Data: 0}
}

func NewLong(v int64) Value {
	return Value{Type: TypeLong, Data: v}
}

func NewDouble(f float64) Value {
	return Value{Type: TypeDouble, Data: int64(math.Float64bits(f))}
}

func NewString(s string) Value {
	if len(s) == 0 {
		return Value{Type: TypeString, Len: 0, Ptr: nil}
	}
	ptr := unsafe.StringData(s)
	return Value{
		Type: TypeString,
		Len:  uint32(len(s)),
		Ptr:  unsafe.Pointer(ptr),
	}
}

func NewStringBytes(b []byte) Value {
	if len(b) == 0 {
		return Value{Type: TypeString, Len: 0, Ptr: nil}
	}
	ptr := unsafe.SliceData(b)
	return Value{
		Type: TypeString,
		Len:  uint32(len(b)),
		Ptr:  unsafe.Pointer(ptr),
	}
}

// Accesseurs
func (v Value) IsNull() bool {
	return v.Type == TypeNull || v.Type == TypeUndef
}

func (v Value) IsBool() bool {
	return v.Type == TypeTrue || v.Type == TypeFalse
}

func (v Value) IsTainted() bool {
	return (v.Flags & (FlagTaintedUserInput | FlagTaintedFileIO)) != 0
}

func (v Value) WithTaint(flag uint8) Value {
	v.Flags |= flag
	return v
}

func (v Value) ClearTaint() Value {
	v.Flags &= ^(FlagTaintedUserInput | FlagTaintedFileIO)
	return v
}

func (v Value) ToBool() bool {
	switch v.Type {
	case TypeTrue:
		return true
	case TypeFalse, TypeNull, TypeUndef:
		return false
	case TypeLong:
		return v.Data != 0
	case TypeDouble:
		f := math.Float64frombits(uint64(v.Data))
		return f != 0.0 && !math.IsNaN(f)
	case TypeString:
		if v.Len == 0 {
			return false
		}
		s := v.String()
		return s != "0"
	case TypeArray:
		return v.Len > 0
	default:
		return true
	}
}

func parseLeadingInt(s string) int64 {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0
	}
	sign := int64(1)
	i := 0
	if s[0] == '+' {
		i++
	} else if s[0] == '-' {
		sign = -1
		i++
	}
	var n int64
	hasDigit := false
	for ; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			hasDigit = true
			n = n*10 + int64(c-'0')
		} else {
			break
		}
	}
	if !hasDigit {
		return 0
	}
	return sign * n
}

func (v Value) ToLong() int64 {
	switch v.Type {
	case TypeLong:
		return v.Data
	case TypeDouble:
		return int64(math.Float64frombits(uint64(v.Data)))
	case TypeTrue:
		return 1
	case TypeFalse, TypeNull, TypeUndef:
		return 0
	case TypeString:
		return parseLeadingInt(v.String())
	default:
		return 0
	}
}

func (v Value) ToDouble() float64 {
	switch v.Type {
	case TypeDouble:
		return math.Float64frombits(uint64(v.Data))
	case TypeLong:
		return float64(v.Data)
	case TypeTrue:
		return 1.0
	case TypeFalse, TypeNull, TypeUndef:
		return 0.0
	case TypeString:
		s := strings.TrimSpace(v.String())
		f, _ := strconv.ParseFloat(s, 64)
		return f
	default:
		return 0.0
	}
}

func (v Value) String() string {
	if v.Type != TypeString || v.Len == 0 || v.Ptr == nil {
		return ""
	}
	return unsafe.String((*byte)(v.Ptr), v.Len)
}

func (v Value) Bytes() []byte {
	if v.Type != TypeString || v.Len == 0 || v.Ptr == nil {
		return nil
	}
	return unsafe.Slice((*byte)(v.Ptr), v.Len)
}

func (v Value) InheritTaint(other Value) Value {
	v.Flags |= other.Flags & (FlagTaintedUserInput | FlagTaintedFileIO | FlagHighEntropy)
	return v
}

func (v Value) CombineTaint(a, b Value) Value {
	v.Flags |= (a.Flags | b.Flags) & (FlagTaintedUserInput | FlagTaintedFileIO | FlagHighEntropy)
	return v
}

// Opérations Arithmétiques avec Débordement Déterministe vers Double et Taint Propagation
func Add(a, b Value) Value {
	var res Value
	if a.Type == TypeLong && b.Type == TypeLong {
		x := a.Data
		y := b.Data
		r := x + y
		if (x > 0 && y > 0 && r <= 0) || (x < 0 && y < 0 && r >= 0) {
			res = NewDouble(float64(x) + float64(y))
		} else {
			res = NewLong(r)
		}
	} else {
		res = NewDouble(a.ToDouble() + b.ToDouble())
	}
	return res.CombineTaint(a, b)
}

func Sub(a, b Value) Value {
	var res Value
	if a.Type == TypeLong && b.Type == TypeLong {
		x := a.Data
		y := b.Data
		r := x - y
		if (x >= 0 && y < 0 && r < 0) || (x < 0 && y > 0 && r >= 0) {
			res = NewDouble(float64(x) - float64(y))
		} else {
			res = NewLong(r)
		}
	} else {
		res = NewDouble(a.ToDouble() - b.ToDouble())
	}
	return res.CombineTaint(a, b)
}

func Mul(a, b Value) Value {
	var res Value
	if a.Type == TypeLong && b.Type == TypeLong {
		x := a.Data
		y := b.Data
		if x == 0 || y == 0 {
			res = NewLong(0)
		} else if x == math.MinInt64 && y == -1 {
			res = NewDouble(-float64(math.MinInt64))
		} else {
			r := x * y
			if r/y != x {
				res = NewDouble(float64(x) * float64(y))
			} else {
				res = NewLong(r)
			}
		}
	} else {
		res = NewDouble(a.ToDouble() * b.ToDouble())
	}
	return res.CombineTaint(a, b)
}

func Div(a, b Value) Value {
	var res Value
	denom := b.ToDouble()
	if denom == 0.0 {
		res = NewDouble(math.NaN())
	} else if a.Type == TypeLong && b.Type == TypeLong {
		x := a.Data
		y := b.Data
		if x == math.MinInt64 && y == -1 {
			res = NewDouble(-float64(math.MinInt64))
		} else if y != 0 && x%y == 0 {
			res = NewLong(x / y)
		} else {
			res = NewDouble(a.ToDouble() / denom)
		}
	} else {
		res = NewDouble(a.ToDouble() / denom)
	}
	return res.CombineTaint(a, b)
}

func Mod(a, b Value) Value {
	var res Value
	y := b.ToLong()
	if y == 0 {
		res = NewDouble(math.NaN())
	} else {
		x := a.ToLong()
		if x == math.MinInt64 && y == -1 {
			res = NewLong(0)
		} else {
			res = NewLong(x % y)
		}
	}
	return res.CombineTaint(a, b)
}

func Pow(a, b Value) Value {
	var res Value
	base := a.ToDouble()
	exp := b.ToDouble()
	p := math.Pow(base, exp)
	if float64(int64(p)) == p && !math.IsNaN(p) && !math.IsInf(p, 0) {
		res = NewLong(int64(p))
	} else {
		res = NewDouble(p)
	}
	return res.CombineTaint(a, b)
}

func Neg(a Value) Value {
	var res Value
	if a.Type == TypeLong {
		if a.Data == math.MinInt64 {
			res = NewDouble(-float64(math.MinInt64))
		} else {
			res = NewLong(-a.Data)
		}
	} else {
		res = NewDouble(-a.ToDouble())
	}
	return res.InheritTaint(a)
}

func Not(a Value) Value {
	b := !a.ToBool()
	return NewBool(b).InheritTaint(a)
}

// Opérations Bitwise
func BitAnd(a, b Value) Value {
	res := NewLong(a.ToLong() & b.ToLong())
	return res.CombineTaint(a, b)
}

func BitOr(a, b Value) Value {
	res := NewLong(a.ToLong() | b.ToLong())
	return res.CombineTaint(a, b)
}

func BitXor(a, b Value) Value {
	res := NewLong(a.ToLong() ^ b.ToLong())
	return res.CombineTaint(a, b)
}

func BitNot(a Value) Value {
	res := NewLong(^a.ToLong())
	return res.InheritTaint(a)
}

func ShiftLeft(a, b Value) Value {
	shift := uint64(b.ToLong() & 63)
	res := NewLong(int64(uint64(a.ToLong()) << shift))
	return res.CombineTaint(a, b)
}

func ShiftRight(a, b Value) Value {
	shift := uint64(b.ToLong() & 63)
	res := NewLong(a.ToLong() >> shift)
	return res.CombineTaint(a, b)
}

// Opérations de Comparaison PHP 8.4
func Equal(a, b Value) bool {
	if a.Type == b.Type {
		switch a.Type {
		case TypeNull, TypeUndef:
			return true
		case TypeFalse, TypeTrue:
			return a.Type == b.Type
		case TypeLong:
			return a.Data == b.Data
		case TypeDouble:
			return a.ToDouble() == b.ToDouble()
		case TypeString:
			return a.String() == b.String()
		case TypeArray:
			return a.Data == b.Data
		default:
			return a.Data == b.Data
		}
	}

	// Coercition booléenne
	if a.IsBool() || b.IsBool() {
		return a.ToBool() == b.ToBool()
	}

	// Comparaison numérique
	if (a.Type == TypeLong || a.Type == TypeDouble) && (b.Type == TypeLong || b.Type == TypeDouble) {
		return a.ToDouble() == b.ToDouble()
	}

	// Null vs String
	if a.IsNull() && b.Type == TypeString {
		return b.Len == 0
	}
	if b.IsNull() && a.Type == TypeString {
		return a.Len == 0
	}

	// Coercition String vs Numérique (règle PHP 8+)
	if a.Type == TypeString && (b.Type == TypeLong || b.Type == TypeDouble) {
		s := a.String()
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f == b.ToDouble()
		}
		return false
	}
	if b.Type == TypeString && (a.Type == TypeLong || a.Type == TypeDouble) {
		s := b.String()
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return a.ToDouble() == f
		}
		return false
	}

	return a.String() == b.String()
}

func Identical(a, b Value) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case TypeNull, TypeUndef:
		return true
	case TypeFalse, TypeTrue:
		return true
	case TypeLong:
		return a.Data == b.Data
	case TypeDouble:
		return a.Data == b.Data
	case TypeString:
		return a.String() == b.String()
	case TypeArray:
		return a.Data == b.Data
	default:
		return a.Data == b.Data
	}
}

// Compare (<=> Spaceship Operator) : -1 si a < b, 0 si a == b, 1 si a > b
func Compare(a, b Value) int {
	if Equal(a, b) {
		return 0
	}
	if a.Type == TypeLong && b.Type == TypeLong {
		if a.Data < b.Data {
			return -1
		}
		return 1
	}
	if (a.Type == TypeLong || a.Type == TypeDouble) && (b.Type == TypeLong || b.Type == TypeDouble) {
		af := a.ToDouble()
		bf := b.ToDouble()
		if af < bf {
			return -1
		}
		return 1
	}
	if a.Type == TypeString && b.Type == TypeString {
		as := a.String()
		bs := b.String()
		if as < bs {
			return -1
		}
		return 1
	}
	// PHP 8+: Si un des opérandes est une chaîne non-numérique et l'autre un nombre, comparer sous forme de chaînes
	if a.Type == TypeString && (b.Type == TypeLong || b.Type == TypeDouble) {
		as := a.String()
		if _, err := strconv.ParseFloat(as, 64); err != nil {
			var buf [32]byte
			bs := string(b.ToStringBuffered(buf[:]))
			if as < bs {
				return -1
			}
			return 1
		}
	}
	if b.Type == TypeString && (a.Type == TypeLong || a.Type == TypeDouble) {
		bs := b.String()
		if _, err := strconv.ParseFloat(bs, 64); err != nil {
			var buf [32]byte
			as := string(a.ToStringBuffered(buf[:]))
			if as < bs {
				return -1
			}
			return 1
		}
	}
	af := a.ToDouble()
	bf := b.ToDouble()
	if af < bf {
		return -1
	}
	if af > bf {
		return 1
	}
	return 0
}

func Lt(a, b Value) bool  { return Compare(a, b) < 0 }
func Lte(a, b Value) bool { return Compare(a, b) <= 0 }
func Gt(a, b Value) bool  { return Compare(a, b) > 0 }
func Gte(a, b Value) bool { return Compare(a, b) >= 0 }

// Concaténation utilisant c2ryu sans allocation superflue
func Concat(a, b Value, dstBuf []byte) Value {
	var tmpA [64]byte
	aStr := a.ToStringBuffered(tmpA[:])
	aLen := len(aStr)
	bStr := b.ToStringBuffered(dstBuf[aLen:])
	bLen := len(bStr)
	totalLen := aLen + bLen
	if len(dstBuf) >= totalLen {
		copy(dstBuf[:aLen], aStr)
		copy(dstBuf[aLen:totalLen], bStr)
		val := NewStringBytes(dstBuf[:totalLen])
		return val.CombineTaint(a, b)
	}
	res := make([]byte, totalLen)
	copy(res, aStr)
	copy(res[aLen:], bStr)
	val := NewStringBytes(res)
	return val.CombineTaint(a, b)
}

func (v Value) ToStringBuffered(dst []byte) []byte {
	switch v.Type {
	case TypeString:
		return v.Bytes()
	case TypeLong:
		if len(dst) >= 32 {
			l := c2ryu.C2_i64_to_str(v.ToLong(), dst)
			return dst[:l]
		}
		return []byte(strconv.FormatInt(v.ToLong(), 10))
	case TypeDouble:
		if len(dst) >= 64 {
			l := c2ryu.C2_d2s_buffered(v.ToDouble(), dst)
			return dst[:l]
		}
		return []byte(strconv.FormatFloat(v.ToDouble(), 'g', -1, 64))
	case TypeTrue:
		return []byte("1")
	case TypeFalse, TypeNull, TypeUndef:
		return []byte("")
	default:
		return []byte("")
	}
}

// Recherche SIMD de sous-chaîne dans une valeur
func (v Value) Pos(needle Value) int64 {
	if v.Type != TypeString || needle.Type != TypeString {
		return -1
	}
	hBytes := v.Bytes()
	nBytes := needle.Bytes()
	return c2strsimd.C2_strpos(hBytes, uint64(len(hBytes)), nBytes, uint64(len(nBytes)))
}

// Substr avec sémantique d'offsets PHP et propagation du Taint
func Substr(str Value, start, length int64) Value {
	if str.Type != TypeString || str.Len == 0 {
		return NewString("")
	}
	s := str.Bytes()
	n := int64(len(s))
	if start < 0 {
		start = n + start
		if start < 0 {
			start = 0
		}
	}
	if start > n {
		return NewString("")
	}
	if length < 0 {
		end := n + length
		if end < start {
			return NewString("")
		}
		length = end - start
	}
	if start+length > n {
		length = n - start
	}
	if length <= 0 {
		return NewString("")
	}
	sub := s[start : start+length]
	res := NewStringBytes(sub)
	return res.InheritTaint(str)
}
