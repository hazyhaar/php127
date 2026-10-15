package types

import (
	"maps"
	"math"
	"sync/atomic"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	objHasDestructor uint16 = 1 << 8
	objDestroyed     uint16 = 1 << 9
)

type ObjectHeader struct {
	RefCount   uint32
	ClassID    uint32
	Generation uint16
	GCFlags    uint16
	PropCount  uint32
	PropOffset uint32
	DynOffset  uint32
	RootSlot   uint32
}

const objectHeaderSize = uint32(unsafe.Sizeof(ObjectHeader{}))
const staleObject = "types: stale or released object handle"

type objectClassTable struct {
	dense []string
	extra map[uint32]string
}

var objectClassNames = make(map[uint32]string)
var objectClassSnap atomic.Pointer[objectClassTable]
var objectDestructor func(z *zone.Zone, v Value)

func resetObjectClassNamesLocked() {
	objectClassNames = make(map[uint32]string)
	publishObjectClassNamesLocked()
}

func publishObjectClassNamesLocked() {
	var maxDense uint32
	for id := range objectClassNames {
		if id < 4096 && id+1 > maxDense {
			maxDense = id + 1
		}
	}
	snap := &objectClassTable{}
	if maxDense != 0 {
		snap.dense = make([]string, maxDense)
	}
	for id, name := range objectClassNames {
		if id < uint32(len(snap.dense)) {
			snap.dense[id] = name
		} else {
			if snap.extra == nil {
				snap.extra = make(map[uint32]string)
			}
			snap.extra[id] = name
		}
	}
	objectClassSnap.Store(snap)
}

// RegisterObjectClassName provides diagnostic names, not class semantics. Like
// process strings, these metadata must be constructed before FreezeProcessZone.
func RegisterObjectClassName(classID uint32, name string) {
	processZoneMu.Lock()
	defer processZoneMu.Unlock()
	if processZoneFrozen {
		panic("types: process zone is frozen")
	}
	if name == "" {
		panic("types: empty object class name")
	}
	if old, ok := objectClassNames[classID]; ok && old != name {
		panic("types: conflicting object class name")
	}
	next := maps.Clone(objectClassNames)
	next[classID] = name
	objectClassNames = next
	publishObjectClassNamesLocked()
}

func objectClassName(z *zone.Zone, v Value) string {
	id := objectHeader(z, v).ClassID
	snap := objectClassSnap.Load()
	if snap != nil {
		if id < uint32(len(snap.dense)) {
			if name := snap.dense[id]; name != "" {
				return name
			}
		} else if name, ok := snap.extra[id]; ok {
			return name
		}
	}
	panic("types: object class name is not registered")
}

// SetObjectDestructor installs the callback invoked at most once when an object
// with hasDestructor reaches RefCount == 0, is collected in a cycle, or is
// drained at request shutdown while still live.
func SetObjectDestructor(fn func(z *zone.Zone, v Value)) {
	objectDestructor = fn
}

// DispatchObjectDestructor executes the registered destructor for a cleanup
// item during z.Reset if the object is still live and has not been destroyed yet.
func DispatchObjectDestructor(z *zone.Zone, item zone.CleanupItem) {
	if item.Kind != zone.CleanupDestructor {
		return
	}
	gen := uint16(item.Extra)
	off, ok := z.ObjectAddress(item.Target, gen)
	if !ok {
		return
	}
	raw, err := z.Bytes(off, objectHeaderSize)
	if err != nil {
		return
	}
	h := (*ObjectHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	if h.RefCount == 0 || h.Generation != gen {
		return
	}
	if h.GCFlags&objHasDestructor == 0 || h.GCFlags&objDestroyed != 0 {
		return
	}
	h.GCFlags |= objDestroyed
	if objectDestructor != nil {
		objectDestructor(z, Value{Type: TypeObject, Flags: FlagZoneRequest, Extra: gen, DataSlot: item.Target})
	}
}

func objectHeader(z *zone.Zone, v Value) *ObjectHeader {
	off, ok := z.ObjectAddress(v.DataSlot, v.Extra)
	if v.Type != TypeObject || v.Flags&FlagZoneRequest == 0 || !ok {
		panic(staleObject)
	}
	raw, err := z.Bytes(off, objectHeaderSize)
	if err != nil {
		panic(staleObject)
	}
	h := (*ObjectHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	if h.RefCount == 0 || h.Generation != v.Extra {
		panic(staleObject)
	}
	return h
}

func objectProperties(z *zone.Zone, h *ObjectHeader) []Value {
	if h.PropCount == 0 {
		return nil
	}
	raw, err := z.Bytes(h.PropOffset, h.PropCount*16)
	if err != nil {
		panic(err)
	}
	return unsafe.Slice((*Value)(unsafe.Pointer(unsafe.SliceData(raw))), h.PropCount)
}

func NewObject(z *zone.Zone, classID, propCount uint32, hasDestructor bool) (Value, error) {
	if propCount > math.MaxUint32/16 {
		return Value{}, zone.ErrInvalidSize
	}
	off, err := z.Alloc(objectHeaderSize)
	if err != nil {
		return Value{}, err
	}
	props := EmptySlot
	if propCount != 0 {
		props, err = z.Alloc(propCount * 16)
		if err != nil {
			if e := z.Free(off, objectHeaderSize); e != nil {
				panic(e)
			}
			return Value{}, err
		}
	}
	index, gen, err := z.NewObjectHandle(off)
	if err != nil {
		if propCount != 0 {
			if e := z.Free(props, propCount*16); e != nil {
				panic(e)
			}
		}
		if e := z.Free(off, objectHeaderSize); e != nil {
			panic(e)
		}
		return Value{}, err
	}
	raw, err := z.Bytes(off, objectHeaderSize)
	if err != nil {
		panic(err)
	}
	var gcFlags uint16
	if hasDestructor {
		gcFlags = objHasDestructor
	}
	h := (*ObjectHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	*h = ObjectHeader{RefCount: 1, ClassID: classID, Generation: gen, GCFlags: gcFlags, PropCount: propCount, PropOffset: props, DynOffset: EmptySlot, RootSlot: EmptySlot}
	clear(objectProperties(z, h))
	v := Value{Type: TypeObject, Flags: FlagZoneRequest, Extra: gen, DataSlot: index}
	if hasDestructor {
		z.RegisterCleanup(zone.CleanupItem{Kind: zone.CleanupDestructor, Target: index, Extra: uint32(gen)})
	}
	return v, nil
}

func ObjectInfo(z *zone.Zone, v Value) ObjectHeader { return *objectHeader(z, v) }
func ObjectGet(z *zone.Zone, v Value, index uint32) Value {
	return objectProperties(z, objectHeader(z, v))[index]
}
func ObjectSet(z *zone.Zone, v Value, index uint32, next Value) {
	props := objectProperties(z, objectHeader(z, v))
	old := &props[index]
	if old.Type == TypeReference && next.Type != TypeReference {
		AssignRef(z, *old, next)
		return
	}
	next = RetainValue(z, next)
	prev := *old
	*old = next
	ReleaseValue(z, &prev)
}

func RetainObject(z *zone.Zone, v Value) Value {
	h := objectHeader(z, v)
	if h.RefCount == math.MaxUint32 {
		panic("types: object reference count overflow")
	}
	h.RefCount++
	return v
}

func ReleaseObject(z *zone.Zone, v *Value) {
	obj := *v
	*v = Value{}
	h := objectHeader(z, obj)
	if h.RefCount == 1 && h.GCFlags&objHasDestructor != 0 && h.GCFlags&objDestroyed == 0 {
		h.GCFlags |= objDestroyed
		if objectDestructor != nil {
			objectDestructor(z, obj)
			h = objectHeader(z, obj)
		}
	}
	h.RefCount--
	if h.RefCount != 0 {
		gcPossibleRoot(z, obj)
	} else {
		gcUnbuffer(z, obj)
		props := objectProperties(z, h)
		for i := range props {
			ReleaseValue(z, &props[i])
		}
		if h.DynOffset != EmptySlot {
			a := Value{Type: TypeArray, Flags: FlagZoneRequest, DataSlot: h.DynOffset}
			ReleaseArray(z, &a)
		}
		freeObject(z, obj, h)
	}
}

func freeObject(z *zone.Zone, v Value, h *ObjectHeader) {
	if h.PropCount != 0 {
		if err := z.Free(h.PropOffset, h.PropCount*16); err != nil {
			panic(err)
		}
	}
	off, ok := z.ObjectAddress(v.DataSlot, v.Extra)
	if !ok {
		panic(staleObject)
	}
	if err := z.Free(off, objectHeaderSize); err != nil {
		panic(err)
	}
	z.FreeObjectHandle(v.DataSlot, v.Extra)
}
