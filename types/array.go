package types

import (
	"bytes"
	"errors"
	"math"
	"strconv"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

type ArrayKind uint8

const (
	ArrayPacked ArrayKind = iota
	ArrayHash
	EmptySlot uint32 = math.MaxUint32
)

// ArrayHeader and Bucket contain only scalars and Zone offsets. RefCount must
// stay at byte zero, which Zone.Free clears before linking a dead allocation.
type ArrayHeader struct {
	RefCount   uint32
	Kind       ArrayKind
	Flags      uint8
	Reserved   uint16
	Len        uint32
	Used       uint32
	Cap        uint32
	DataOffset uint32
	NextIdx    int64
}

type Bucket struct {
	Val  Value
	Key  Value
	Hash uint64
	Next uint32
	Pad  uint32
}

const arrayHeaderSize = uint32(unsafe.Sizeof(ArrayHeader{}))
const bucketSize = uint32(unsafe.Sizeof(Bucket{}))

var (
	ErrArrayTooLarge = errors.New("types: array exceeds Zone offset range")
	ErrArrayKey      = errors.New("Illegal offset type")
	ErrArrayValue    = errors.New("types: undefined or unsupported array element")
	ErrArrayOccupied = errors.New("Cannot add element to the array as the next element is already occupied")
)

type arrayKeyError struct {
	name      string
	operation uint8
}

func (e arrayKeyError) Error() string {
	switch e.operation {
	case 1:
		return "Cannot access offset of type " + e.name + " in isset or empty"
	case 2:
		return "Cannot unset offset of type " + e.name + " on array"
	default:
		return "Cannot access offset of type " + e.name + " on array"
	}
}

func (e arrayKeyError) Is(target error) bool { return target == ErrArrayKey }

// ArrayWarning represents E_DEPRECATED (8192). A missing handler is an error in
// the interpreter, not permission to silently discard a PHP diagnostic.
type ArrayWarning struct{ Float float64 }

func (w ArrayWarning) AppendMessage(dst []byte) []byte {
	dst = append(dst, "Implicit conversion from float "...)
	dst = appendPHPNumber(dst, NewDouble(w.Float), -1)
	return append(dst, " to int loses precision"...)
}

// appendPHPNumber supports PHP's default string precision (14) and shortest
// diagnostic formatting (-1). Neither path allocates a temporary string.
func appendPHPNumber(dst []byte, v Value, precision int) []byte {
	if v.Type == TypeLong {
		return strconv.AppendInt(dst, v.ToLong(), 10)
	}
	f := v.ToDouble()
	if math.IsNaN(f) {
		return append(dst, "NAN"...)
	}
	if math.IsInf(f, 1) {
		return append(dst, "INF"...)
	}
	if math.IsInf(f, -1) {
		return append(dst, "-INF"...)
	}
	start := len(dst)
	format := byte('G')
	if precision == -1 && (f == 0 || math.Abs(f) >= 1e-4 && math.Abs(f) < 1e17) {
		format = 'f'
	}
	dst = strconv.AppendFloat(dst, f, format, precision, 64)
	for i := start; i < len(dst); i++ {
		if dst[i] != 'E' {
			continue
		}
		dot := false
		for j := start; j < i; j++ {
			dot = dot || dst[j] == '.'
		}
		if !dot {
			dst = append(dst, 0, 0)
			copy(dst[i+2:], dst[i:len(dst)-2])
			dst[i], dst[i+1] = '.', '0'
			i += 2
		}
		if i+3 < len(dst) && dst[i+2] == '0' {
			copy(dst[i+2:], dst[i+3:])
			dst = dst[:len(dst)-1]
		}
		break
	}
	return dst
}

// NormalizeArrayKey returns a borrowed key. Canonical decimal parsing is bounded
// and does not use strconv's allocating error path for nonnumeric strings.
func NormalizeArrayKey(z *zone.Zone, key Value, warn func(ArrayWarning) error) (Value, error) {
	key = Deref(z, key)
	switch key.Type {
	case TypeLong:
		return key, nil
	case TypeNull:
		return emptyString, nil
	case TypeFalse, TypeTrue:
		return NewLong(key.ToLong()), nil
	case TypeDouble:
		n := key.ToLong()
		if key.ToDouble() != float64(n) && warn != nil {
			if err := warn(ArrayWarning{Float: key.ToDouble()}); err != nil {
				return Value{}, err
			}
		}
		return NewLong(n), nil
	case TypeString:
		text := StringData(z, key)
		if len(text) == 0 {
			return key, nil
		}
		c0 := text[0]
		if (c0 < '0' || c0 > '9') && c0 != '-' {
			return key, nil
		}
		if text == "0" {
			return NewLong(0), nil
		}
		i := 0
		negative := c0 == '-'
		if negative {
			if len(text) == 1 {
				return key, nil
			}
			i = 1
		}
		if text[i] < '1' || text[i] > '9' {
			return key, nil
		}
		limit := uint64(math.MaxInt64)
		if negative {
			limit++
		}
		var n uint64
		for ; i < len(text); i++ {
			c := text[i]
			if c < '0' || c > '9' {
				return key, nil
			}
			d := uint64(c - '0')
			if n > (limit-d)/10 {
				return key, nil
			}
			n = n*10 + d
		}
		if negative {
			return NewLong(-int64(n)), nil
		}
		return NewLong(int64(n)), nil
	case TypeArray:
		return Value{}, arrayKeyError{name: "array"}
	case TypeObject:
		return Value{}, arrayKeyError{name: objectClassName(z, key)}
	default:
		return Value{}, ErrArrayKey
	}
}

func arrayDataSize(kind ArrayKind, capacity uint32) (uint32, error) {
	width := uint64(bucketSize)
	if kind == ArrayHash {
		width += 4
	}
	n := uint64(capacity) * width
	if n > math.MaxUint32 {
		return 0, ErrArrayTooLarge
	}
	return uint32(n), nil
}

func arrayData(z *zone.Zone, h *ArrayHeader) ([]Bucket, []uint32) {
	size, err := arrayDataSize(h.Kind, h.Cap)
	if err != nil {
		panic(err)
	}
	raw, err := z.Bytes(h.DataOffset, size)
	if err != nil {
		panic(err)
	}
	buckets := unsafe.Slice((*Bucket)(unsafe.Pointer(unsafe.SliceData(raw))), h.Cap)
	if h.Kind == ArrayPacked {
		return buckets, nil
	}
	return buckets, unsafe.Slice((*uint32)(unsafe.Pointer(&raw[h.Cap*bucketSize])), h.Cap)
}

func arrayHeader(z *zone.Zone, v Value) *ArrayHeader {
	if v.Type != TypeArray || v.Flags&FlagZoneRequest == 0 || v.DataSlot%8 != 0 {
		panic("types: invalid array value")
	}
	raw, err := z.Bytes(v.DataSlot, arrayHeaderSize)
	if err != nil {
		panic(err)
	}
	h := (*ArrayHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	if h.RefCount == 0 || h.Kind > ArrayHash || h.Cap < 8 || h.Cap&(h.Cap-1) != 0 || h.Len > h.Used || h.Used > h.Cap {
		panic("types: invalid array header")
	}
	return h
}

func ArrayInfo(z *zone.Zone, v Value) ArrayHeader { return *arrayHeader(z, Deref(z, v)) }
func ArrayLen(z *zone.Zone, v Value) uint32 {
	d := Deref(z, v)
	if d.Type != TypeArray {
		return 0
	}
	return arrayHeader(z, d).Len
}

func allocArray(z *zone.Zone, kind ArrayKind, capacity uint32) (Value, error) {
	size, err := arrayDataSize(kind, capacity)
	if err != nil {
		return Value{}, err
	}
	off, err := z.Alloc(arrayHeaderSize)
	if err != nil {
		return Value{}, err
	}
	data, err := z.Alloc(size)
	if err != nil {
		if e := z.Free(off, arrayHeaderSize); e != nil {
			panic(e)
		}
		return Value{}, err
	}
	raw, err := z.Bytes(off, arrayHeaderSize)
	if err != nil {
		panic(err)
	}
	h := (*ArrayHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	*h = ArrayHeader{RefCount: 1, Kind: kind, Cap: capacity, DataOffset: data, NextIdx: math.MinInt64}
	b, slots := arrayData(z, h)
	clear(b)
	for i := range slots {
		slots[i] = EmptySlot
	}
	return Value{Type: TypeArray, Flags: FlagZoneRequest, DataSlot: off}, nil
}

func NewArray(z *zone.Zone) (Value, error) { return allocArray(z, ArrayPacked, 8) }

func RetainArray(z *zone.Zone, v Value) Value {
	h := arrayHeader(z, v)
	if h.RefCount == math.MaxUint32 {
		panic("types: array reference count overflow")
	}
	h.RefCount++
	return v
}

func RetainValue(z *zone.Zone, v Value) Value {
	if v.Type <= TypeDouble {
		return v
	}
	switch v.Type {
	case TypeString:
		return RetainString(z, v)
	case TypeArray:
		return RetainArray(z, v)
	case TypeObject:
		return RetainObject(z, v)
	case TypeReference:
		return RetainReference(z, v)
	default:
		if v.Type > TypeArray {
			panic("types: retain unsupported value")
		}
		return v
	}
}

func ReleaseValue(z *zone.Zone, v *Value) {
	if v.Type <= TypeDouble {
		*v = Value{}
		return
	}
	switch v.Type {
	case TypeString:
		ReleaseString(z, v)
	case TypeArray:
		ReleaseArray(z, v)
	case TypeObject:
		ReleaseObject(z, v)
	case TypeReference:
		ReleaseReference(z, v)
	default:
		if v.Type > TypeArray {
			panic("types: release unsupported value")
		}
		*v = Value{}
	}
}

func ReleaseArray(z *zone.Zone, v *Value) {
	h := arrayHeader(z, *v)
	h.RefCount--
	if h.RefCount == 0 {
		gcUnbuffer(z, *v)
		b, _ := arrayData(z, h)
		for i := uint32(0); i < h.Used; i++ {
			if b[i].Val.IsUndef() {
				continue
			}
			ReleaseValue(z, &b[i].Val)
			if h.Kind == ArrayHash {
				ReleaseValue(z, &b[i].Key)
			}
		}
		size, err := arrayDataSize(h.Kind, h.Cap)
		if err != nil {
			panic(err)
		}
		if err := z.Free(h.DataOffset, size); err != nil {
			panic(err)
		}
		if err := z.Free(v.DataSlot, arrayHeaderSize); err != nil {
			panic(err)
		}
	} else {
		gcPossibleRoot(z, *v)
	}
	*v = Value{}
}

func arrayKeyHash(z *zone.Zone, key Value) uint64 {
	if key.Type == TypeLong {
		return key.Scalar | 1<<63
	}
	h, data := stringStorage(z, key)
	if h.Hash != 0 {
		return h.Hash
	}
	// Do not populate a borrowed key's cache before fallible allocations succeed.
	return Djbx33a(data[:h.Len], int(h.Len))
}

func arrayKeySame(z *zone.Zone, a, b Value) bool {
	if a.Type != b.Type {
		return false
	}
	if a.Type == TypeLong {
		return a.Scalar == b.Scalar
	}
	if a.Scalar != b.Scalar {
		return false
	}
	if a.DataSlot == b.DataSlot && (a.Flags&FlagZoneRequest) == (b.Flags&FlagZoneRequest) {
		return true
	}
	return bytes.Equal(StringBytes(z, a), StringBytes(z, b))
}

func arrayFind(z *zone.Zone, h *ArrayHeader, key Value, hash uint64) uint32 {
	b, slots := arrayData(z, h)
	if h.Kind == ArrayPacked {
		if key.Type == TypeLong && key.Scalar < uint64(h.Used) && !b[key.Scalar].Val.IsUndef() {
			return uint32(key.Scalar)
		}
		return EmptySlot
	}
	for i := slots[hash&uint64(h.Cap-1)]; i != EmptySlot; i = b[i].Next {
		if b[i].Hash == hash && arrayKeySame(z, b[i].Key, key) {
			return i
		}
	}
	return EmptySlot
}

// ArrayGet returns a borrowed value, invalidated by mutation/release of its
// owner. RetainValue is mandatory before editing a nested array and writing it
// back with ArraySet; there are no writable aliases into buckets.
func ArrayGet(z *zone.Zone, a, key Value, warn func(ArrayWarning) error) (Value, bool, error) {
	a = Deref(z, a)
	key, err := NormalizeArrayKey(z, key, warn)
	if err != nil {
		return Value{}, false, err
	}
	if a.Type != TypeArray {
		return Value{}, false, nil
	}
	h := arrayHeader(z, a)
	hash := arrayKeyHash(z, key)
	if key.Type == TypeString && key.Flags&FlagZoneRequest != 0 {
		sh, _ := stringStorage(z, key)
		sh.Hash = hash
	}
	i := arrayFind(z, h, key, hash)
	if i == EmptySlot {
		return Value{}, false, nil
	}
	b, _ := arrayData(z, h)
	return b[i].Val, true, nil
}

func ArrayKeyExists(z *zone.Zone, a, key Value, warn func(ArrayWarning) error) (bool, error) {
	_, ok, err := ArrayGet(z, a, key, warn)
	return ok, err
}

func ArrayIsset(z *zone.Zone, a, key Value, warn func(ArrayWarning) error) (bool, error) {
	v, ok, err := ArrayGet(z, a, key, warn)
	if diagnostic, bad := err.(arrayKeyError); bad {
		diagnostic.operation = 1
		return false, diagnostic
	}
	return ok && !Deref(z, v).IsNull(), err
}

func rebuildArray(z *zone.Zone, h *ArrayHeader) {
	b, slots := arrayData(z, h)
	if h.Kind != ArrayHash {
		return
	}
	for i := range slots {
		slots[i] = EmptySlot
	}
	for i := uint32(0); i < h.Used; i++ {
		if b[i].Val.IsUndef() {
			continue
		}
		s := b[i].Hash & uint64(h.Cap-1)
		b[i].Next, slots[s] = slots[s], i
	}
}

// writableArray prepares every fallible allocation before publishing. Copying
// transfers ownership for a unique owner and retains each child for CoW.
func writableArray(z *zone.Zone, a *Value, kind ArrayKind, capacity uint32, compact bool) error {
	h := arrayHeader(z, *a)
	shared := h.RefCount > 1
	if !shared && kind == h.Kind && capacity == h.Cap {
		if compact {
			b, _ := arrayData(z, h)
			rootHigh := b[0].Pad
			used := uint32(0)
			for i := uint32(0); i < h.Used; i++ {
				if !b[i].Val.IsUndef() {
					b[used] = b[i]
					used++
				}
			}
			clear(b[used:h.Used])
			b[0].Pad = rootHigh
			h.Used = used
			rebuildArray(z, h)
		}
		return nil
	}
	old := *h
	next := *a
	nh := h
	if shared {
		var err error
		next, err = allocArray(z, kind, capacity)
		if err != nil {
			return err
		}
		nh = arrayHeader(z, next)
	} else {
		size, err := arrayDataSize(kind, capacity)
		if err != nil {
			return err
		}
		off, err := z.Alloc(size)
		if err != nil {
			return err
		}
		nh.DataOffset, nh.Kind, nh.Cap = off, kind, capacity
	}
	source, _ := arrayData(z, &old)
	rootHigh := source[0].Pad
	target, _ := arrayData(z, nh)
	clear(target)
	used := uint32(0)
	for i := uint32(0); i < old.Used; i++ {
		if source[i].Val.IsUndef() {
			if !compact {
				used++
			}
			continue
		}
		entry := source[i]
		if kind == ArrayHash && old.Kind == ArrayPacked {
			entry.Key = NewLong(int64(i))
			entry.Hash = arrayKeyHash(z, entry.Key)
		}
		if shared {
			if entry.Val.Type == TypeReference && refCell(z, entry.Val).RefCount == 1 {
				entry.Val = Deref(z, entry.Val)
			}
			entry.Val = RetainValue(z, entry.Val)
			if kind == ArrayHash {
				entry.Key = RetainValue(z, entry.Key)
			}
		}
		target[used] = entry
		used++
	}
	if !shared {
		target[0].Pad = rootHigh
	} else {
		target[0].Pad = 0
	}
	nh.Len, nh.Used, nh.NextIdx = old.Len, used, old.NextIdx
	rebuildArray(z, nh)
	if shared {
		h.RefCount--
		gcPossibleRoot(z, *a)
		next.Flags = a.Flags
		*a = next
	} else {
		size, err := arrayDataSize(old.Kind, old.Cap)
		if err != nil {
			panic(err)
		}
		if err := z.Free(old.DataOffset, size); err != nil {
			panic(err)
		}
	}
	return nil
}

// ArraySet borrows key and val and retains what it stores. The caller keeps its
// owners on both success and failure. All request-backed arguments belong to z.
func ArraySet(z *zone.Zone, a *Value, key, val Value, warn func(ArrayWarning) error) error {
	if a.Type == TypeReference {
		a = &refCell(z, *a).Target
	}
	if a.Type != TypeArray {
		return ErrArrayValue
	}
	if val.IsUndef() || val.Type == TypeResource || val.Type > TypeReference {
		return ErrArrayValue
	}
	key, err := NormalizeArrayKey(z, key, warn)
	if err != nil {
		return err
	}
	return arraySetNormalized(z, a, key, val)
}

func arraySetNormalized(z *zone.Zone, a *Value, key, val Value) error {
	h := arrayHeader(z, *a)
	hash := arrayKeyHash(z, key)
	idx := arrayFind(z, h, key, hash)
	kind, capacity := h.Kind, h.Cap
	compact := false
	if idx == EmptySlot {
		// Filling an old packed hole must append at the end of insertion order.
		if kind == ArrayPacked && (key.Type != TypeLong || key.Scalar != uint64(h.Used)) {
			kind = ArrayHash
		}
		if h.Used == h.Cap {
			compact = true
			if h.Len <= h.Cap/2 {
				kind = ArrayHash
			} else {
				if h.Cap > math.MaxUint32/2 {
					return ErrArrayTooLarge
				}
				capacity *= 2
				if h.Len != h.Used {
					kind = ArrayHash
				}
			}
		}
	}
	// Retaining a self-assignment forces separation, representing PHP's value
	// snapshot rather than constructing a recursive reference.
	val = RetainValue(z, val)
	key = RetainValue(z, key)
	if err := writableArray(z, a, kind, capacity, compact); err != nil {
		ReleaseValue(z, &key)
		ReleaseValue(z, &val)
		return err
	}
	h = arrayHeader(z, *a)
	if h.Kind == ArrayHash && key.Type == TypeString && key.Flags&FlagZoneRequest != 0 {
		sh, _ := stringStorage(z, key)
		sh.Hash = hash
	}
	b, slots := arrayData(z, h)
	if idx != EmptySlot {
		if b[idx].Val.Type == TypeReference && val.Type != TypeReference {
			AssignRef(z, b[idx].Val, val)
			ReleaseValue(z, &val)
			ReleaseValue(z, &key)
			return nil
		}
		prev := b[idx].Val
		b[idx].Val = val
		ReleaseValue(z, &prev)
		ReleaseValue(z, &key)
		return nil
	}
	idx = h.Used
	b[idx] = Bucket{Val: val, Next: EmptySlot, Pad: b[idx].Pad}
	if h.Kind == ArrayHash {
		slot := hash & uint64(h.Cap-1)
		b[idx].Key, b[idx].Hash, b[idx].Next = key, hash, slots[slot]
		slots[slot] = idx
	} else {
		ReleaseValue(z, &key)
	}
	h.Used++
	h.Len++
	if kind == ArrayPacked {
		key = NewLong(int64(idx))
	}
	if key.Type == TypeLong && key.ToLong() >= h.NextIdx {
		h.NextIdx = key.ToLong()
		if h.NextIdx < math.MaxInt64 {
			h.NextIdx++
		}
	}
	return nil
}

func ArrayAppend(z *zone.Zone, a *Value, val Value) error {
	if a.Type == TypeReference {
		a = &refCell(z, *a).Target
	}
	if a.Type != TypeArray {
		return ErrArrayValue
	}
	if val.IsUndef() || val.Type == TypeResource || val.Type > TypeReference {
		return ErrArrayValue
	}
	h := arrayHeader(z, *a)
	k := h.NextIdx
	if k == math.MinInt64 {
		k = 0
	}
	key := NewLong(k)
	if k == math.MaxInt64 && arrayFind(z, h, key, arrayKeyHash(z, key)) != EmptySlot {
		return ErrArrayOccupied
	}
	return arraySetNormalized(z, a, key, val)
}

func ArrayUnset(z *zone.Zone, a *Value, key Value, warn func(ArrayWarning) error) error {
	if a.Type == TypeReference {
		a = &refCell(z, *a).Target
	}
	key, err := NormalizeArrayKey(z, key, warn)
	if err != nil {
		if diagnostic, bad := err.(arrayKeyError); bad {
			diagnostic.operation = 2
			return diagnostic
		}
		return err
	}
	if a.Type != TypeArray {
		return nil
	}
	h := arrayHeader(z, *a)
	hash := arrayKeyHash(z, key)
	idx := arrayFind(z, h, key, hash)
	if idx == EmptySlot {
		return nil
	}
	if err := writableArray(z, a, h.Kind, h.Cap, false); err != nil {
		return err
	}
	h = arrayHeader(z, *a)
	b, slots := arrayData(z, h)
	if h.Kind == ArrayHash {
		link := &slots[hash&uint64(h.Cap-1)]
		for *link != idx {
			link = &b[*link].Next
		}
		*link = b[idx].Next
		ReleaseValue(z, &b[idx].Key)
	}
	ReleaseValue(z, &b[idx].Val)
	h.Len--
	return nil
}

// ArrayCursor owns a retained snapshot, matching foreach by value. It must not
// be copied; Close is required even after early termination. Next returns
// borrowed keys/values. Reference iteration awaits PHP reference values.
type ArrayCursor struct {
	value Value
	next  uint32
}

func NewArrayCursor(z *zone.Zone, a Value) ArrayCursor {
	return ArrayCursor{value: RetainArray(z, Deref(z, a))}
}

func (c *ArrayCursor) Next(z *zone.Zone) (Value, Value, bool) {
	if c.value.IsUndef() {
		return Value{}, Value{}, false
	}
	h := arrayHeader(z, c.value)
	b, _ := arrayData(z, h)
	for c.next < h.Used {
		i := c.next
		c.next++
		if b[i].Val.IsUndef() {
			continue
		}
		key := b[i].Key
		if h.Kind == ArrayPacked {
			key = NewLong(int64(i))
		}
		return key, b[i].Val, true
	}
	return Value{}, Value{}, false
}

func (c *ArrayCursor) Close(z *zone.Zone) {
	if !c.value.IsUndef() {
		ReleaseArray(z, &c.value)
	}
	c.next = 0
}

func ArrayBucketAt(z *zone.Zone, a Value, start uint32) (Value, uint32, bool) {
	a = Deref(z, a)
	if a.Type != TypeArray {
		return Value{}, 0, false
	}
	h := arrayHeader(z, a)
	b, _ := arrayData(z, h)
	for i := start; i < h.Used; i++ {
		if b[i].Val.IsUndef() {
			continue
		}
		key := b[i].Key
		if h.Kind == ArrayPacked {
			key = NewLong(int64(i))
		}
		return key, i + 1, true
	}
	return Value{}, h.Used, false
}

func ArrayForEach(z *zone.Zone, a Value, visit func(Value, Value) bool) {
	c := NewArrayCursor(z, a)
	defer c.Close(z)
	for {
		k, v, ok := c.Next(z)
		if !ok || !visit(k, v) {
			return
		}
	}
}

func ArrayEqual(z *zone.Zone, a, b Value) bool {
	a, b = Deref(z, a), Deref(z, b)
	ha, hb := arrayHeader(z, a), arrayHeader(z, b)
	if a.DataSlot == b.DataSlot {
		return true
	}
	if ha.Len != hb.Len {
		return false
	}
	buckets, _ := arrayData(z, ha)
	other, _ := arrayData(z, hb)
	for i := uint32(0); i < ha.Used; i++ {
		x := buckets[i]
		if x.Val.IsUndef() {
			continue
		}
		key, hash := x.Key, x.Hash
		if ha.Kind == ArrayPacked {
			key = NewLong(int64(i))
			hash = arrayKeyHash(z, key)
		}
		j := arrayFind(z, hb, key, hash)
		if j == EmptySlot || !EqualZ(z, x.Val, other[j].Val) {
			return false
		}
	}
	return true
}

func ArrayIdentical(z *zone.Zone, a, b Value) bool {
	a, b = Deref(z, a), Deref(z, b)
	ha, hb := arrayHeader(z, a), arrayHeader(z, b)
	if a.DataSlot == b.DataSlot {
		return true
	}
	if ha.Len != hb.Len {
		return false
	}
	ax, _ := arrayData(z, ha)
	by, _ := arrayData(z, hb)
	j := uint32(0)
	for i := uint32(0); i < ha.Used; i++ {
		if ax[i].Val.IsUndef() {
			continue
		}
		for j < hb.Used && by[j].Val.IsUndef() {
			j++
		}
		if j == hb.Used {
			return false
		}
		ak, bk := ax[i].Key, by[j].Key
		if ha.Kind == ArrayPacked {
			ak = NewLong(int64(i))
		}
		if hb.Kind == ArrayPacked {
			bk = NewLong(int64(j))
		}
		if !arrayKeySame(z, ak, bk) || !IdenticalZ(z, Deref(z, ax[i].Val), Deref(z, by[j].Val)) {
			return false
		}
		j++
	}
	return true
}

func IdenticalZ(z *zone.Zone, a, b Value) bool {
	a, b = Deref(z, a), Deref(z, b)
	if a.Type == TypeObject && b.Type == TypeObject {
		objectHeader(z, a)
		objectHeader(z, b)
		return a.DataSlot == b.DataSlot && a.Extra == b.Extra
	}
	if a.Type > TypeObject || b.Type > TypeObject {
		panic("types: comparison of resources/references is not implemented")
	}
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case TypeString:
		return CompareBinary(z, a, b) == 0
	case TypeArray:
		return ArrayIdentical(z, a, b)
	default:
		return Identical(a, b)
	}
}

// ToBoolZ converts any value (including zone-backed strings, arrays, objects,
// and references) to its PHP boolean truth value.
func ToBoolZ(z *zone.Zone, v Value) bool {
	return boolZ(z, v)
}

func boolZ(z *zone.Zone, v Value) bool {
	v = Deref(z, v)
	switch v.Type {
	case TypeString:
		s := StringData(z, v)
		return s != "" && s != "0"
	case TypeArray:
		return ArrayLen(z, v) != 0
	case TypeObject:
		objectHeader(z, v)
		return true
	default:
		return v.ToBool()
	}
}

func objectEqual(z *zone.Zone, a, b Value) bool {
	ha, hb := objectHeader(z, a), objectHeader(z, b)
	if a.DataSlot == b.DataSlot && a.Extra == b.Extra {
		return true
	}
	if ha.ClassID != hb.ClassID || ha.PropCount != hb.PropCount {
		return false
	}
	pa, pb := objectProperties(z, ha), objectProperties(z, hb)
	for i := range pa {
		if pa[i].IsUndef() != pb[i].IsUndef() {
			return false
		}
		if !pa[i].IsUndef() && !EqualZ(z, pa[i], pb[i]) {
			return false
		}
	}
	if ha.DynOffset == EmptySlot && hb.DynOffset == EmptySlot {
		return true
	}
	if ha.DynOffset == EmptySlot {
		return ArrayLen(z, Value{Type: TypeArray, Flags: FlagZoneRequest, DataSlot: hb.DynOffset}) == 0
	}
	if hb.DynOffset == EmptySlot {
		return ArrayLen(z, Value{Type: TypeArray, Flags: FlagZoneRequest, DataSlot: ha.DynOffset}) == 0
	}
	return ArrayEqual(z,
		Value{Type: TypeArray, Flags: FlagZoneRequest, DataSlot: ha.DynOffset},
		Value{Type: TypeArray, Flags: FlagZoneRequest, DataSlot: hb.DynOffset},
	)
}

// EqualZ owns loose comparison for the currently implemented scalar, string,
// array and object types. Unsupported future types fail explicitly rather than compare false.
func EqualZ(z *zone.Zone, a, b Value) bool {
	a, b = Deref(z, a), Deref(z, b)
	if a.Type > TypeObject || b.Type > TypeObject {
		panic("types: comparison of resources/references is not implemented")
	}
	if a.IsBool() || b.IsBool() {
		return boolZ(z, a) == boolZ(z, b)
	}
	if a.Type == TypeObject || b.Type == TypeObject {
		if a.Type == TypeObject {
			objectHeader(z, a)
		}
		if b.Type == TypeObject {
			objectHeader(z, b)
		}
		if a.Type == TypeObject && b.Type == TypeObject {
			return objectEqual(z, a, b)
		}
		return false
	}
	if a.Type <= TypeNull || b.Type <= TypeNull {
		if a.Type == TypeString {
			return StringData(z, a) == ""
		}
		if b.Type == TypeString {
			return StringData(z, b) == ""
		}
		return boolZ(z, a) == boolZ(z, b)
	}
	if a.Type == TypeArray || b.Type == TypeArray {
		return a.Type == b.Type && ArrayEqual(z, a, b)
	}
	if a.Type == TypeString && b.Type == TypeString {
		return CompareStringsPHP(z, a, b) == 0
	}
	if a.Type == TypeString {
		a, b = b, a
	}
	if b.Type == TypeString {
		text := StringData(z, b)
		if number, _, ok := numericString(text); ok {
			return CompareScalaires(a, number) == 0
		}
		if a.Type == TypeDouble && math.IsNaN(a.ToDouble()) {
			return false
		}
		var buf [64]byte
		return string(appendPHPNumber(buf[:0], a, 14)) == text
	}
	return CompareScalaires(a, b) == 0
}
