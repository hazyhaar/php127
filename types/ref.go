package types

import (
	"errors"
	"math"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

type RefCell struct {
	RefCount uint32
	Flags    uint32
	Target   Value
}

const refCellSize = uint32(unsafe.Sizeof(RefCell{}))

func refCell(z *zone.Zone, v Value) *RefCell {
	if v.Type != TypeReference || v.Flags&FlagZoneRequest == 0 {
		panic("types: invalid reference")
	}
	raw, err := z.Bytes(v.DataSlot, refCellSize)
	if err != nil {
		panic(err)
	}
	h := (*RefCell)(unsafe.Pointer(unsafe.SliceData(raw)))
	if h.RefCount == 0 {
		panic("types: released reference")
	}
	return h
}

// NewReference borrows initial, flattening an existing cell rather than nesting it.
func NewReference(z *zone.Zone, initial Value) (Value, error) {
	initial = Deref(z, initial)
	off, err := z.Alloc(refCellSize)
	if err != nil {
		return Value{}, err
	}
	raw, err := z.Bytes(off, refCellSize)
	if err != nil {
		panic(err)
	}
	*(*RefCell)(unsafe.Pointer(unsafe.SliceData(raw))) = RefCell{RefCount: 1, Target: RetainValue(z, initial)}
	return Value{Type: TypeReference, Flags: FlagZoneRequest, DataSlot: off}, nil
}

func Deref(z *zone.Zone, v Value) Value {
	if v.Type == TypeReference {
		return refCell(z, v).Target
	}
	return v
}

func IsSharedReference(z *zone.Zone, v Value) bool {
	return v.Type == TypeReference && refCell(z, v).RefCount > 1
}

// AssignRef borrows next; retaining before release makes self-assignment safe.
func AssignRef(z *zone.Zone, ref Value, next Value) {
	h := refCell(z, ref)
	next = RetainValue(z, Deref(z, next))
	prev := h.Target
	h.Target = next
	ReleaseValue(z, &prev)
}

func RetainReference(z *zone.Zone, v Value) Value {
	h := refCell(z, v)
	if h.RefCount == math.MaxUint32 {
		panic("types: reference count overflow")
	}
	h.RefCount++
	return v
}

func ReleaseReference(z *zone.Zone, v *Value) {
	h := refCell(z, *v)
	h.RefCount--
	if h.RefCount == 0 {
		gcUnbuffer(z, *v)
		ReleaseValue(z, &h.Target)
		if err := z.Free(v.DataSlot, refCellSize); err != nil {
			panic(err)
		}
	} else if gcNode(h.Target) {
		gcPossibleRoot(z, *v)
	}
	*v = Value{}
}

// ArrayReference returns an owned alias to an existing element. Missing elements
// are initialized to null. CoW occurs before a previously unshared cell is exposed.
func ArrayReference(z *zone.Zone, a *Value, key Value, warn func(ArrayWarning) error) (Value, error) {
	if a.Type == TypeReference {
		a = &refCell(z, *a).Target
	}
	key, err := NormalizeArrayKey(z, key, warn)
	if err != nil {
		return Value{}, err
	}
	h := arrayHeader(z, *a)
	i := arrayFind(z, h, key, arrayKeyHash(z, key))
	initial := NewNull()
	if i != EmptySlot {
		b, _ := arrayData(z, h)
		initial = b[i].Val
	}
	if initial.Type == TypeReference && (h.RefCount == 1 || refCell(z, initial).RefCount > 1) {
		if err := writableArray(z, a, h.Kind, h.Cap, false); err != nil {
			return Value{}, err
		}
		return RetainReference(z, initial), nil
	}
	r, err := NewReference(z, initial)
	if err != nil {
		return Value{}, err
	}
	if err := arraySetNormalized(z, a, key, r); err != nil {
		ReleaseReference(z, &r)
		return Value{}, err
	}
	return r, nil
}

var ErrReferenceIterationMutation = errors.New("types: structural mutation during reference iteration is not implemented")

// ArrayForEachReference visits owned cells transiently. A visitor retaining its
// loop variable must retain the cell. Visitors may change targets and perform
// structural mutations (append, set, unset) on the iterated array; bucket
// order and compactions are tracked across each callback.
func ArrayForEachReference(z *zone.Zone, a *Value, visit func(Value, Value) bool) error {
	targetArray := func() *Value {
		if a.Type == TypeReference {
			return &refCell(z, *a).Target
		}
		return a
	}
	arr := targetArray()
	h := arrayHeader(z, *arr)
	if err := writableArray(z, arr, h.Kind, h.Cap, false); err != nil {
		return err
	}
	for i := uint32(0); ; {
		arr = targetArray()
		if arr.Type != TypeArray {
			return nil
		}
		h = arrayHeader(z, *arr)
		if i >= h.Used {
			return nil
		}
		b, _ := arrayData(z, h)
		if b[i].Val.IsUndef() {
			i++
			continue
		}
		key := b[i].Key
		if h.Kind == ArrayPacked {
			key = NewLong(int64(i))
		}
		key = RetainValue(z, key)
		r, err := ArrayReference(z, arr, key, nil)
		if err != nil {
			ReleaseValue(z, &key)
			return err
		}
		more := func() bool {
			defer ReleaseReference(z, &r)
			return visit(key, r)
		}()
		if !more {
			ReleaseValue(z, &key)
			return nil
		}
		arr = targetArray()
		if arr.Type != TypeArray {
			ReleaseValue(z, &key)
			return nil
		}
		h = arrayHeader(z, *arr)
		b, _ = arrayData(z, h)
		if i < h.Used && !b[i].Val.IsUndef() {
			curKey := b[i].Key
			if h.Kind == ArrayPacked {
				curKey = NewLong(int64(i))
			}
			if arrayKeySame(z, curKey, key) {
				ReleaseValue(z, &key)
				i++
				continue
			}
		}
		found := arrayFind(z, h, key, arrayKeyHash(z, key))
		ReleaseValue(z, &key)
		if found != EmptySlot {
			i = found + 1
		} else {
			i++
		}
	}
}
