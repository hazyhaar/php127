package types

import (
	"cmp"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

const FlagInterned Flags = 1 << 4

// StringHeader and its byte buffer form one aligned Zone allocation. Cap includes
// the terminal NUL and padding; Len excludes both. No field contains a Go pointer.
type StringHeader struct {
	RefCount uint32
	Flags    uint32
	Cap      uint32
	Len      uint32
	Hash     uint64
}

const stringHeaderSize = uint32(unsafe.Sizeof(StringHeader{}))

//go:generate go run gen_archtime.go

// Process construction precedes request workers. After FreezeProcessZone,
// storage and eager hashes are read-only and require no lock on request reads.
var processZone = zone.New(256 << 10)
var processStrings map[string]Value
var processZoneMu sync.Mutex
var processZoneFrozen bool

func init() {
	resetProcessZoneLocked()
}

func resetProcessZoneLocked() {
	if processZone.Capacity() != 256<<10 {
		processZone = zone.New(256 << 10)
	} else {
		processZone.Reset(nil)
	}
	processZoneFrozen = false
	processStrings = make(map[string]Value, len(archtimeInternedTable))
	for _, entry := range archtimeInternedTable {
		offset, err := processZone.Alloc(stringHeaderSize + entry.cap)
		if err != nil {
			panic(err)
		}
		if offset != entry.offset {
			panic("types: archtime process zone offset mismatch")
		}
		raw, err := processZone.Bytes(offset, stringHeaderSize+entry.cap)
		if err != nil {
			panic(err)
		}
		h := (*StringHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
		*h = StringHeader{
			RefCount: math.MaxUint32,
			Flags:    uint32(FlagImmutable | FlagInterned),
			Cap:      entry.cap,
			Len:      uint32(len(entry.text)),
			Hash:     entry.hash,
		}
		data := raw[stringHeaderSize:]
		copy(data, entry.text)
		data[len(entry.text)] = 0
		v := Value{Type: TypeString, Flags: FlagImmutable | FlagInterned, DataSlot: offset, Scalar: uint64(len(entry.text))}
		processStrings[StringData(nil, v)] = v
	}
	resetObjectClassNamesLocked()
}

// ResetProcessZone restores the precomputed ARCHTIME table at the start of the
// process arena before workers start.
func ResetProcessZone() {
	processZoneMu.Lock()
	defer processZoneMu.Unlock()
	resetProcessZoneLocked()
}

// ReserveProcessZone étend la capacité de la zone de processus avant son gel tout en préservant les décalages existants.
func ReserveProcessZone(capacity uint32) {
	processZoneMu.Lock()
	defer processZoneMu.Unlock()
	if processZoneFrozen {
		panic("types: process zone is frozen")
	}
	if capacity <= processZone.Capacity() {
		return
	}
	processZone.Grow(capacity)
	rebuilt := make(map[string]Value, len(processStrings))
	for _, v := range processStrings {
		rebuilt[StringData(nil, v)] = v
	}
	processStrings = rebuilt
}

func internProcessStringLocked(text string) (Value, error) {
	if processZoneFrozen {
		panic("types: process zone is frozen")
	}
	if v, ok := processStrings[text]; ok {
		return v, nil
	}
	v, h, data, err := allocString(processZone, uint64(len(text)), 0)
	if err != nil {
		return Value{}, err
	}
	copy(data, text)
	h.Flags = uint32(FlagImmutable | FlagInterned)
	h.RefCount = math.MaxUint32
	v.Flags = FlagImmutable | FlagInterned
	StringHash(nil, v)
	// The key borrows the permanent immutable bytes, not the caller's storage.
	processStrings[StringData(nil, v)] = v
	return v, nil
}

// InternProcessString deduplicates literals before workers start. Construction
// may allocate Go memory; exhaustion returns an error without publishing a value.
// Calling it after freeze panics, including for an existing literal.
func InternProcessString(text string) (Value, error) {
	processZoneMu.Lock()
	defer processZoneMu.Unlock()
	return internProcessStringLocked(text)
}

// InternProcessValue promeut une valeur scalaire, une chaîne ou un tableau (et récursivement ses sous-tableaux et
// clés) depuis src vers la zone de processus avant son gel. Un appel après FreezeProcessZone panique.
func InternProcessValue(src *zone.Zone, v Value) (Value, error) {
	processZoneMu.Lock()
	defer processZoneMu.Unlock()
	if processZoneFrozen {
		panic("types: process zone is frozen")
	}
	return internProcessValueLocked(src, v)
}

func internProcessValueLocked(src *zone.Zone, v Value) (Value, error) {
	v = Deref(src, v)
	switch v.Type {
	case TypeUndef, TypeNull, TypeFalse, TypeTrue, TypeLong, TypeDouble:
		return v, nil
	case TypeString:
		if v.Flags&FlagZoneRequest == 0 {
			return v, nil
		}
		return internProcessStringLocked(StringData(src, v))
	case TypeArray:
		if v.Flags&FlagZoneRequest == 0 {
			return v, nil
		}
		srcH := arrayHeader(src, v)
		srcBuckets, srcSlots := arrayData(src, srcH)
		buckets := make([]Bucket, srcH.Cap)
		for i := uint32(0); i < srcH.Used; i++ {
			if srcBuckets[i].Val.IsUndef() {
				continue
			}
			iv, err := internProcessValueLocked(src, srcBuckets[i].Val)
			if err != nil {
				return Value{}, err
			}
			var ik Value
			if srcH.Kind == ArrayHash {
				ik, err = internProcessValueLocked(src, srcBuckets[i].Key)
				if err != nil {
					return Value{}, err
				}
			}
			buckets[i] = Bucket{
				Val:  iv,
				Key:  ik,
				Hash: srcBuckets[i].Hash,
				Next: srcBuckets[i].Next,
			}
		}
		size, err := arrayDataSize(srcH.Kind, srcH.Cap)
		if err != nil {
			return Value{}, err
		}
		off, err := processZone.Alloc(arrayHeaderSize)
		if err != nil {
			return Value{}, err
		}
		dataOff, err := processZone.Alloc(size)
		if err != nil {
			_ = processZone.Free(off, arrayHeaderSize)
			return Value{}, err
		}
		raw, err := processZone.Bytes(off, arrayHeaderSize)
		if err != nil {
			panic(err)
		}
		h := (*ArrayHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
		*h = ArrayHeader{
			RefCount:   math.MaxUint32,
			Kind:       srcH.Kind,
			Flags:      0,
			Len:        srcH.Len,
			Used:       srcH.Used,
			Cap:        srcH.Cap,
			DataOffset: dataOff,
			NextIdx:    srcH.NextIdx,
		}
		dstBuckets, dstSlots := arrayData(processZone, h)
		copy(dstBuckets, buckets)
		if srcH.Kind == ArrayHash {
			copy(dstSlots, srcSlots)
		}
		return Value{Type: TypeArray, Flags: FlagImmutable | FlagInterned, DataSlot: off}, nil
	default:
		return Value{}, ErrArrayValue
	}
}

// FreezeProcessZone permanently ends construction before request workers start.
func FreezeProcessZone() {
	processZoneMu.Lock()
	defer processZoneMu.Unlock()
	processZoneFrozen = true
	processStrings = nil
}

func IsProcessZoneFrozen() bool {
	processZoneMu.Lock()
	defer processZoneMu.Unlock()
	return processZoneFrozen
}

var (
	ErrStringTooLarge        = errors.New("types: string exceeds Zone offset range")
	ErrEmptyStringAssignment = errors.New("Cannot assign an empty string to a string offset")
	ErrStringType            = errors.New("types: string operation requires a string")
)

// allocString does not publish uninitialized contents to callers.
func allocString(z *zone.Zone, length, reserve uint64) (Value, *StringHeader, []byte, error) {
	capacity := (max(length+1, reserve) + 7) &^ uint64(7)
	if length >= math.MaxUint32 || reserve > math.MaxUint32 || capacity > math.MaxUint32-uint64(stringHeaderSize) {
		return Value{}, nil, nil, ErrStringTooLarge
	}
	offset, err := z.Alloc(stringHeaderSize + uint32(capacity))
	if err != nil {
		return Value{}, nil, nil, err
	}
	raw, err := z.Bytes(offset, stringHeaderSize+uint32(capacity))
	if err != nil {
		panic(err)
	}
	h := (*StringHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	*h = StringHeader{RefCount: 1, Flags: uint32(FlagZoneRequest | FlagOwnedMutable), Cap: uint32(capacity), Len: uint32(length)}
	data := raw[stringHeaderSize:]
	data[length] = 0
	return Value{Type: TypeString, Flags: FlagZoneRequest, DataSlot: offset, Scalar: length}, h, data, nil
}

// NewString copies text into z. reserve is a minimum byte capacity INCLUDING the
// NUL; zero requests the smallest aligned buffer. All views expire at z.Reset.
func NewString(z *zone.Zone, text string, reserve uint32) (Value, error) {
	v, _, data, err := allocString(z, uint64(len(text)), uint64(reserve))
	if err == nil {
		copy(data, text)
	}
	return v, err
}

// NewStringLiteral returns the pre-interned ProcessZone string for empty and
// single-byte strings without allocating in z, and allocates an interned string
// in z for longer literals.
func NewStringLiteral(z *zone.Zone, text string) (Value, error) {
	if len(text) == 0 {
		return emptyString, nil
	}
	if len(text) == 1 {
		return byteTable[text[0]], nil
	}
	return NewInternedString(z, text)
}

// NewInternedString creates an immutable literal, not a deduplicating intern
// table. Its storage lives until this Zone resets, just like every other string.
func NewInternedString(z *zone.Zone, text string) (Value, error) {
	v, h, data, err := allocString(z, uint64(len(text)), 0)
	if err != nil {
		return v, err
	}
	copy(data, text)
	h.Flags = uint32(FlagZoneRequest | FlagImmutable | FlagInterned)
	h.RefCount = math.MaxUint32
	v.Flags = Flags(h.Flags)
	return v, nil
}

// stringStorage resolves request offsets or process-zone offsets.
// Request values must belong to z and must not survive Reset.
func stringStorage(z *zone.Zone, v Value) (*StringHeader, []byte) {
	if v.Type != TypeString || v.DataSlot%8 != 0 {
		panic("types: invalid string value")
	}
	if v.Flags&FlagZoneRequest == 0 {
		if v.Flags&^FlagTainted != FlagImmutable|FlagInterned || v.Extra != 0 {
			panic("types: invalid process string")
		}
		z = processZone
	}
	raw, err := z.Bytes(v.DataSlot, stringHeaderSize)
	if err != nil {
		panic(err)
	}
	h := (*StringHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	if h.RefCount == 0 || h.Len >= h.Cap || h.Cap%8 != 0 || uint64(h.Len) != v.Scalar || uint64(stringHeaderSize)+uint64(h.Cap) > math.MaxUint32 {
		panic("types: invalid string header")
	}
	raw, err = z.Bytes(v.DataSlot, stringHeaderSize+h.Cap)
	if err != nil {
		panic(err)
	}
	return h, raw[stringHeaderSize:]
}

// StringInfo returns a snapshot, never a writable header alias.
func StringInfo(z *zone.Zone, v Value) StringHeader {
	h, _ := stringStorage(z, v)
	info := *h
	// Interned bytes are shared; provenance belongs to the immediate value.
	info.Flags |= uint32(v.Flags & FlagTainted)
	return info
}

// StringData returns a read-only borrowed view without copying. It must not be
// retained across mutation or Reset, nor used concurrently with either.
func StringData(z *zone.Zone, v Value) string {
	h, data := stringStorage(z, v)
	return unsafe.String(unsafe.SliceData(data), h.Len)
}

// StringBytes returns a read-only borrowed byte slice without copying. It must
// not be mutated or retained across mutation or Reset.
func StringBytes(z *zone.Zone, v Value) []byte {
	h, data := stringStorage(z, v)
	return data[:h.Len:h.Len]
}

func Strlen(v Value) uint64 {
	if v.Type != TypeString {
		panic("types: strlen requires a string")
	}
	return v.Scalar
}

// RetainString must accompany a PHP value copy; a plain Go assignment alone
// does not create an owning reference. Interned reference counts never change.
func RetainString(z *zone.Zone, v Value) Value {
	h, _ := stringStorage(z, v)
	if h.Flags&uint32(FlagInterned) == 0 {
		if h.RefCount >= math.MaxUint32-1 {
			panic("types: string reference count overflow")
		}
		h.RefCount++
	}
	return v
}

// ReleaseString consumes one owner and recycles non-interned storage at zero.
func ReleaseString(z *zone.Zone, v *Value) {
	h, _ := stringStorage(z, *v)
	if h.Flags&uint32(FlagInterned) == 0 {
		h.RefCount--
		if h.RefCount == 0 {
			if err := z.Free(v.DataSlot, stringHeaderSize+h.Cap); err != nil {
				panic(err)
			}
		}
	}
	*v = Value{}
}

func Concat(z *zone.Zone, a, b Value) (Value, error) {
	ax, x := stringStorage(z, a)
	by, y := stringStorage(z, b)
	x, y = x[:ax.Len], y[:by.Len]
	v, h, data, err := allocString(z, uint64(len(x))+uint64(len(y)), 0)
	if err == nil {
		copy(data, x)
		copy(data[len(x):], y)
		h.Flags |= (ax.Flags | by.Flags | uint32(a.Flags|b.Flags)) & uint32(FlagTainted)
		v.Flags = Flags(h.Flags) &^ FlagOwnedMutable
	}
	return v, err
}

// FormatLong formats n as a base-10 PHP string. Single-digit non-negative
// integers return pre-interned ProcessZone values without allocating in z.
func FormatLong(z *zone.Zone, n int64) (Value, error) {
	if n >= 0 && n <= 9 {
		return byteTable['0'+byte(n)], nil
	}
	var buf [24]byte
	b := strconv.AppendInt(buf[:0], n, 10)
	v, _, data, err := allocString(z, uint64(len(b)), 0)
	if err == nil {
		copy(data, b)
	}
	return v, err
}

// ConcatStringLong concatenates string a and the base-10 representation of n
// in a single Zone allocation without allocating an intermediate string for n.
func ConcatStringLong(z *zone.Zone, a Value, n int64) (Value, error) {
	if a.Type != TypeString {
		return Value{}, ErrStringType
	}
	var buf [24]byte
	rb := strconv.AppendInt(buf[:0], n, 10)
	if len(rb) == 0 {
		return RetainString(z, a), nil
	}
	ax, ab := stringStorage(z, a)
	ab = ab[:ax.Len]
	tainted := (ax.Flags | uint32(a.Flags)) & uint32(FlagTainted)
	if len(ab) == 0 {
		v, err := FormatLong(z, n)
		if err != nil {
			return Value{}, err
		}
		if tainted != 0 {
			if v.Flags&FlagZoneRequest != 0 {
				vh, _ := stringStorage(z, v)
				vh.Flags |= tainted
				v.Flags = Flags(vh.Flags) &^ FlagOwnedMutable
			} else {
				v.Flags |= FlagTainted
			}
		}
		return v, nil
	}
	v, h, data, err := allocString(z, uint64(len(ab))+uint64(len(rb)), 0)
	if err == nil {
		copy(data, ab)
		copy(data[len(ab):], rb)
		h.Flags |= tainted
		v.Flags = Flags(h.Flags) &^ FlagOwnedMutable
	}
	return v, err
}

// writableString owns the M21 decision for both append and byte assignment.
// Allocation failure leaves the value, its header and its buffer untouched.
func writableString(z *zone.Zone, v *Value, length uint64) (*StringHeader, []byte, error) {
	h, data := stringStorage(z, *v)
	if length >= math.MaxUint32 {
		return nil, nil, ErrStringTooLarge
	}
	if h.RefCount == 1 && h.Flags&uint32(FlagOwnedMutable) != 0 && h.Flags&uint32(FlagInterned|FlagImmutable) == 0 && uint64(h.Cap) >= length+1 {
		h.Flags |= uint32(v.Flags & FlagTainted)
		return h, data, nil
	}
	reserve := uint64(0)
	if length+1 > uint64(h.Cap) {
		// Double the whole block, not just its buffer, to fit geometric classes.
		reserve = min(max(length+1, (uint64(h.Cap)+uint64(stringHeaderSize))*2-uint64(stringHeaderSize)), (uint64(math.MaxUint32)-uint64(stringHeaderSize))&^7)
	}
	next, nh, buffer, err := allocString(z, length, reserve)
	if err != nil {
		return nil, nil, err
	}
	copy(buffer, data[:h.Len])
	nh.Flags |= (h.Flags | uint32(v.Flags)) & uint32(FlagTainted)
	next.Flags = Flags(nh.Flags) &^ FlagOwnedMutable
	if h.Flags&uint32(FlagInterned) == 0 {
		h.RefCount--
		if h.RefCount == 0 {
			if err := z.Free(v.DataSlot, stringHeaderSize+h.Cap); err != nil {
				panic(err)
			}
		}
	}
	*v = next
	return nh, buffer, nil
}

func Append(z *zone.Zone, v *Value, suffix Value) error {
	sh, tail := stringStorage(z, suffix)
	tail = tail[:sh.Len]
	tainted := (sh.Flags | uint32(suffix.Flags)) & uint32(FlagTainted)
	self := suffix.DataSlot == v.DataSlot && suffix.Flags&FlagZoneRequest == v.Flags&FlagZoneRequest
	oldLen := Strlen(*v)
	length := oldLen + uint64(len(tail))
	h, data, err := writableString(z, v, length)
	if err != nil {
		return err
	}
	if self {
		// writableString may have freed the old buffer; use its copied prefix.
		copy(data[oldLen:], data[:oldLen])
	} else {
		copy(data[oldLen:], tail)
	}
	data[length] = 0
	h.Flags |= tainted
	v.Flags = Flags(h.Flags) &^ FlagOwnedMutable
	h.Len, h.Hash, v.Scalar = uint32(length), 0, length
	return nil
}

// Substr accepts PHP byte offsets and lengths. MaxInt64 represents an omitted
// length, since every supported string is shorter than that bound.
func Substr(z *zone.Zone, v Value, start, length int64) (Value, error) {
	source, text := stringStorage(z, v)
	n := int64(source.Len)
	if start < 0 {
		start = max(n+start, 0)
	} else {
		start = min(start, n)
	}
	end := n
	if length < 0 {
		end = max(start, n+length)
	} else if length < n-start {
		end = start + length
	}
	next, h, data, err := allocString(z, uint64(end-start), 0)
	if err == nil {
		copy(data, text[start:end])
		h.Flags |= (source.Flags | uint32(v.Flags)) & uint32(FlagTainted)
		next.Flags = Flags(h.Flags) &^ FlagOwnedMutable
	}
	return next, err
}

type StringWarningKind uint8

const (
	UninitializedStringOffset StringWarningKind = iota
	IllegalStringOffset
	FirstByteOnly
)

// StringWarning carries an E_WARNING (PHP severity 2), without allocating a
// formatted message. The interpreter supplies source location and routing.
type StringWarning struct {
	Kind   StringWarningKind
	Offset int64
}

func (w StringWarning) AppendMessage(dst []byte) []byte {
	switch w.Kind {
	case UninitializedStringOffset:
		dst = append(dst, "Uninitialized string offset "...)
	case IllegalStringOffset:
		dst = append(dst, "Illegal string offset "...)
	case FirstByteOnly:
		return append(dst, "Only the first byte will be assigned to the string offset"...)
	default:
		panic("types: unknown string warning")
	}
	return strconv.AppendInt(dst, w.Offset, 10)
}

func emitStringWarning(warn func(StringWarning) error, kind StringWarningKind, offset int64) error {
	if warn == nil {
		panic("types: PHP string warning requires a handler")
	}
	return warn(StringWarning{Kind: kind, Offset: offset})
}

// ByteAt calls warn before returning an empty string on an invalid offset.
// A handler error aborts the operation, as a throwing PHP error handler does.
func ByteAt(z *zone.Zone, v Value, index int64, warn func(StringWarning) error) (Value, error) {
	text := StringData(z, v)
	original := index
	if index < 0 {
		index += int64(len(text))
	}
	if index < 0 || index >= int64(len(text)) {
		if err := emitStringWarning(warn, UninitializedStringOffset, original); err != nil {
			return Value{}, err
		}
		result := emptyString
		result.Flags |= Flags(StringInfo(z, v).Flags) & FlagTainted
		return result, nil
	}
	result := byteTable[text[index]]
	result.Flags |= Flags(StringInfo(z, v).Flags) & FlagTainted
	return result, nil
}

// SetByteAt pads positive out-of-bounds writes with spaces. Invalid negative
// offsets warn without mutation; empty replacements raise PHP's Error.
func SetByteAt(z *zone.Zone, v *Value, index int64, replacement Value, warn func(StringWarning) error) error {
	text := StringData(z, *v)
	original := index
	if index < 0 {
		index += int64(len(text))
	}
	if index < 0 {
		return emitStringWarning(warn, IllegalStringOffset, original)
	}
	c := StringData(z, replacement)
	if len(c) == 0 {
		return ErrEmptyStringAssignment
	}
	if len(c) > 1 {
		if err := emitStringWarning(warn, FirstByteOnly, 0); err != nil {
			return err
		}
	}
	first := c[0]
	tainted := StringInfo(z, replacement).Flags & uint32(FlagTainted)
	length := max(uint64(len(text)), uint64(index)+1)
	h, data, err := writableString(z, v, length)
	if err != nil {
		return err
	}
	for i := len(text); uint64(i) < uint64(index); i++ {
		data[i] = ' '
	}
	data[index], data[length] = first, 0
	h.Flags |= tainted
	v.Flags = Flags(h.Flags) &^ FlagOwnedMutable
	h.Len, h.Hash, v.Scalar = uint32(length), 0, length
	return nil
}

// CompareBinary is normalized bytewise ordering, equivalent to bytes.Compare.
// Equality here is PHP string ===, not PHP numeric-string equality.
func CompareBinary(z *zone.Zone, a, b Value) int {
	return strings.Compare(StringData(z, a), StringData(z, b))
}

func StringHash(z *zone.Zone, v Value) uint64 {
	h, data := stringStorage(z, v)
	if h.Hash == 0 {
		h.Hash = Djbx33a(data[:h.Len], int(h.Len))
	}
	return h.Hash
}

// CompareStringsPHP follows PHP 8.3 zend_smart_strcmp, including its binary
// fallback when overflowing numeric strings lose distinguishing precision.
func CompareStringsPHP(z *zone.Zone, a, b Value) int {
	x, y := StringData(z, a), StringData(z, b)
	xn, xo, xok := numericString(x)
	yn, yo, yok := numericString(y)
	if !xok || !yok {
		return strings.Compare(x, y)
	}
	if xn.Type == TypeLong && yn.Type == TypeLong {
		return cmp.Compare(int64(xn.Scalar), int64(yn.Scalar))
	}
	xf, yf := xn.ToDouble(), yn.ToDouble()
	if xo != 0 && xo == yo && xf == yf || xf == yf && math.IsInf(xf, 0) {
		return strings.Compare(x, y)
	}
	if xn.Type == TypeLong && yo != 0 {
		return -yo
	}
	if yn.Type == TypeLong && xo != 0 {
		return xo
	}
	return cmp.Compare(xf, yf)
}

func numericString(text string) (Value, int, bool) {
	text = strings.TrimLeft(text, " \t\n\r\v\f")
	if len(text) == 0 {
		return Value{}, 0, false
	}
	i, sign := 0, 1
	if text[0] == '-' || text[0] == '+' {
		if text[0] == '-' {
			sign = -1
		}
		i++
	}
	start := i
	var integer uint64
	limit := uint64(math.MaxInt64)
	if sign < 0 {
		limit++
	}
	overflow, fractional := false, false
	digits, beforeDot, leading := 0, 0, 0
	// This exact decimal midpoint rounds to infinity in binary64 (2^1024-2^970).
	const infinityThreshold = "179769313486231580793728971405303415079934132710037826936173778980444968292764750946649017977587207096330286416692887910946555547851940402630657488671505820681908902000708383676273854845817711531764475730270069855571366959622842914819860834936475292719074168444365510704342711559699508093042880177904174497792"
	thresholdCmp, significant := 0, 0
	for i < len(text) {
		c := text[i]
		if c == '.' && !fractional {
			fractional = true
			beforeDot = digits
			i++
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		digits++
		if c == '0' && leading == digits-1 {
			leading++
		} else {
			if significant < len(infinityThreshold) && thresholdCmp == 0 {
				thresholdCmp = cmp.Compare(c, infinityThreshold[significant])
			}
			significant++
		}
		if !fractional && !overflow {
			d := uint64(c - '0')
			if integer > (limit-d)/10 {
				overflow = true
			} else {
				integer = integer*10 + d
			}
		}
		i++
	}
	if digits == 0 || i == start {
		return Value{}, 0, false
	}
	if !fractional {
		beforeDot = digits
	}
	exponent := int64(0)
	hasExponent := i < len(text) && (text[i] == 'e' || text[i] == 'E')
	if hasExponent {
		i++
		expSign := int64(1)
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			if text[i] == '-' {
				expSign = -1
			}
			i++
		}
		expStart := i
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			// Any larger exponent dominates even a whole Zone of zero digits.
			exponent = min(exponent*10+int64(text[i]-'0'), int64(math.MaxUint32)+1024)
			i++
		}
		if i == expStart {
			return Value{}, 0, false
		}
		exponent *= expSign
	}
	if strings.TrimLeft(text[i:], " \t\n\r\v\f") != "" {
		return Value{}, 0, false
	}
	// PHP's 19-digit integer check compares the entire suffix, including
	// validated trailing whitespace, against the magnitude of LONG_MIN.
	if !fractional && !hasExponent && digits-leading == 19 {
		order := strings.Compare(text[start+leading:], "9223372036854775808")
		overflow = order > 0 || order == 0 && sign > 0
	}
	if !fractional && !hasExponent && !overflow {
		return NewLong(int64(integer) * int64(sign)), 0, true
	}
	overflowSign := 0
	// PHP switches to strtod after 20 significant integer digits and retains
	// the overflow marker even if a later exponent brings the value in range.
	if beforeDot-min(leading, beforeDot) >= 20 || !fractional && !hasExponent && overflow {
		overflowSign = sign
	}
	if significant == 0 {
		return NewDouble(math.Copysign(0, float64(sign))), overflowSign, true
	}
	if thresholdCmp == 0 && significant < len(infinityThreshold) {
		for j := significant; j < len(infinityThreshold); j++ {
			if infinityThreshold[j] != '0' {
				thresholdCmp = -1
				break
			}
		}
	}
	magnitude := int64(beforeDot-leading-1) + exponent
	// Avoid strconv's allocating range-error path; grammar was validated above.
	if magnitude > 308 || magnitude == 308 && thresholdCmp >= 0 {
		return NewDouble(math.Inf(sign)), overflowSign, true
	}
	number, err := strconv.ParseFloat(text[:i], 64)
	if err != nil {
		panic("types: validated numeric string conversion failed")
	}
	return NewDouble(number), overflowSign, true
}
