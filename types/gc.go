package types

import (
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	Black uint32 = iota
	Purple
	Grey
	White
	Buffered    uint32 = 4
	GCThreshold int    = 10000
)

func gcNode(v Value) bool {
	return v.Type == TypeArray || v.Type == TypeObject || v.Type == TypeReference
}
func gcID(v Value) uint64 { return uint64(v.Type)<<56 | uint64(v.Extra)<<32 | uint64(v.DataSlot) }
func gcValue(id uint64) Value {
	return Value{Type: Type(id >> 56), Flags: FlagZoneRequest, Extra: uint16(id >> 32), DataSlot: uint32(id)}
}

// Trial counts may be zero while a header remains live. Only the collector uses
// this unchecked view; all public accessors keep their released-handle checks.
func gcMemory(z *zone.Zone, v Value) unsafe.Pointer {
	off, size := v.DataSlot, arrayHeaderSize
	if v.Type == TypeReference {
		size = refCellSize
	}
	if v.Type == TypeObject {
		var ok bool
		off, ok = z.ObjectAddress(v.DataSlot, v.Extra)
		if !ok {
			panic(staleObject)
		}
		size = objectHeaderSize
	}
	raw, err := z.Bytes(off, size)
	if err != nil {
		panic(err)
	}
	return unsafe.Pointer(unsafe.SliceData(raw))
}

func gcCount(z *zone.Zone, v Value) *uint32 { return (*uint32)(gcMemory(z, v)) }
func gcFlags(z *zone.Zone, v Value) uint32 {
	p := gcMemory(z, v)
	switch v.Type {
	case TypeArray:
		return uint32((*ArrayHeader)(p).Flags) & 7
	case TypeObject:
		return uint32((*ObjectHeader)(p).GCFlags) & 7
	default:
		return (*RefCell)(p).Flags & 7
	}
}
func gcSetFlags(z *zone.Zone, v Value, flags uint32) {
	p := gcMemory(z, v)
	switch v.Type {
	case TypeArray:
		h := (*ArrayHeader)(p)
		h.Flags = h.Flags&^7 | uint8(flags)&7
	case TypeObject:
		h := (*ObjectHeader)(p)
		h.GCFlags = h.GCFlags&^7 | uint16(flags)&7
	default:
		h := (*RefCell)(p)
		h.Flags = h.Flags&^7 | flags&7
	}
}

func gcRootSlot(z *zone.Zone, v Value) uint32 {
	p := gcMemory(z, v)
	switch v.Type {
	case TypeArray:
		h := (*ArrayHeader)(p)
		b, _ := arrayData(z, h)
		return uint32(h.Flags>>3) | uint32(h.Reserved)<<5 | b[0].Pad<<21
	case TypeObject:
		return (*ObjectHeader)(p).RootSlot
	default:
		return (*RefCell)(p).Flags >> 3
	}
}

func gcSetRootSlot(z *zone.Zone, v Value, slot uint32) {
	p := gcMemory(z, v)
	switch v.Type {
	case TypeArray:
		h := (*ArrayHeader)(p)
		h.Flags, h.Reserved = h.Flags&7|uint8(slot<<3), uint16(slot>>5)
		// The header holds 21 bits; bucket padding extends this to all 32 bits.
		// Array mutations preserve this metadata independently of bucket values.
		b, _ := arrayData(z, h)
		b[0].Pad = slot >> 21
	case TypeObject:
		(*ObjectHeader)(p).RootSlot = slot
	default:
		h := (*RefCell)(p)
		h.Flags = h.Flags&7 | slot<<3
	}
}

func gcPossibleRoot(z *zone.Zone, v Value) {
	flags := gcFlags(z, v)
	if flags&Buffered == 0 {
		s := z.Cycles()
		if len(s.Roots) >= GCThreshold {
			CollectCycles(z)
			if v.Type == TypeObject {
				if _, ok := z.ObjectAddress(v.DataSlot, v.Extra); !ok {
					return
				}
			}
			if *gcCount(z, v) == 0 {
				return
			}
			flags = gcFlags(z, v)
		}
		if flags&Buffered == 0 {
			gcSetRootSlot(z, v, uint32(len(s.Roots)))
			s.Roots = append(s.Roots, gcID(v))
		}
	}
	gcSetFlags(z, v, Purple|Buffered)
}

func gcUnbuffer(z *zone.Zone, v Value) {
	if gcFlags(z, v)&Buffered == 0 {
		return
	}
	s := z.Cycles()
	i := gcRootSlot(z, v)
	last := uint32(len(s.Roots) - 1)
	if s.Roots[i] != gcID(v) {
		panic("types: inconsistent root slot")
	}
	s.Roots[i] = s.Roots[last]
	if i != last {
		moved := gcValue(s.Roots[i])
		gcSetRootSlot(z, moved, i)
	}
	s.Roots = s.Roots[:last]
	gcSetFlags(z, v, Black)
	gcSetRootSlot(z, v, 0)
}

func gcChildren(z *zone.Zone, v Value, visit func(Value)) {
	p := gcMemory(z, v)
	switch v.Type {
	case TypeReference:
		visit((*RefCell)(p).Target)
	case TypeArray:
		h := (*ArrayHeader)(p)
		b, _ := arrayData(z, h)
		for i := uint32(0); i < h.Used; i++ {
			if !b[i].Val.IsUndef() {
				visit(b[i].Val)
			}
		}
	case TypeObject:
		h := (*ObjectHeader)(p)
		for _, child := range objectProperties(z, h) {
			visit(child)
		}
		if h.DynOffset != EmptySlot {
			visit(Value{Type: TypeArray, Flags: FlagZoneRequest, DataSlot: h.DynOffset})
		}
	}
}

// CollectCycles performs trial deletion, live-subgraph restoration and white
// collection. Work queues replace recursion; each edge is subtracted/restored
// once, including edges shared through reference cells. The result counts PHP
// containers and destroyed request strings, not transparent reference cells.
// No allocation occurs after New.
func CollectCycles(z *zone.Zone) uint32 {
	s := z.Cycles()
	s.Work = s.Work[:0]
	markGrey := func(v Value) {
		flags := gcFlags(z, v)
		if flags&3 != Grey {
			gcSetFlags(z, v, flags&Buffered|Grey)
			s.Work = append(s.Work, gcID(v))
		}
	}
	for _, id := range s.Roots {
		v := gcValue(id)
		if gcFlags(z, v)&3 == Purple {
			markGrey(v)
		}
	}
	for i := 0; i < len(s.Work); i++ {
		gcChildren(z, gcValue(s.Work[i]), func(child Value) {
			if !gcNode(child) {
				return
			}
			count := gcCount(z, child)
			if *count == 0 {
				panic("types: cycle edge reference count underflow")
			}
			*count--
			markGrey(child)
		})
	}
	// Trial deletion has consumed the root list. Reuse it as the live-node
	// queue; all buffered marks are cleared before those slots are overwritten.
	for _, id := range s.Work {
		v := gcValue(id)
		gcSetFlags(z, v, gcFlags(z, v)&3)
	}
	s.Roots = s.Roots[:0]
	for _, id := range s.Work {
		v := gcValue(id)
		if *gcCount(z, v) != 0 {
			gcSetFlags(z, v, gcFlags(z, v)&Buffered|Black)
			s.Roots = append(s.Roots, id)
		}
	}
	for i := 0; i < len(s.Roots); i++ {
		gcChildren(z, gcValue(s.Roots[i]), func(child Value) {
			if !gcNode(child) {
				return
			}
			*gcCount(z, child)++
			flags := gcFlags(z, child)
			if flags&3 != Black {
				gcSetFlags(z, child, flags&Buffered|Black)
				s.Roots = append(s.Roots, gcID(child))
			}
		})
	}
	for _, id := range s.Work {
		v := gcValue(id)
		if gcFlags(z, v)&3 == Grey {
			gcSetFlags(z, v, White)
		} else {
			gcSetFlags(z, v, Black)
		}
		if v.Type == TypeObject {
			(*ObjectHeader)(gcMemory(z, v)).RootSlot = EmptySlot
		}
	}
	s.Roots = s.Roots[:0]
	for _, id := range s.Work {
		v := gcValue(id)
		if v.Type != TypeObject || gcFlags(z, v) != White {
			continue
		}
		h := (*ObjectHeader)(gcMemory(z, v))
		if h.GCFlags&objHasDestructor != 0 && h.GCFlags&objDestroyed == 0 {
			h.GCFlags |= objDestroyed
			if objectDestructor != nil {
				h.RefCount++
				objectDestructor(z, v)
				h = (*ObjectHeader)(gcMemory(z, v))
				h.RefCount--
			}
		}
	}
	var collected uint32
	for _, id := range s.Work {
		v := gcValue(id)
		if gcFlags(z, v) != White {
			continue
		}
		p := gcMemory(z, v)
		// Container edges were consumed by trial deletion. Only non-graph
		// resources still need ordinary release; no freed header is traversed.
		gcChildren(z, v, func(child Value) {
			if !gcNode(child) {
				if child.Type == TypeString && child.Flags&FlagZoneRequest != 0 && StringInfo(z, child).RefCount == 1 {
					collected++
				}
				ReleaseValue(z, &child)
			}
		})
		switch v.Type {
		case TypeArray:
			h := (*ArrayHeader)(p)
			if h.Kind == ArrayHash {
				b, _ := arrayData(z, h)
				for i := uint32(0); i < h.Used; i++ {
					if !b[i].Val.IsUndef() {
						ReleaseValue(z, &b[i].Key)
					}
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
			collected++
		case TypeObject:
			freeObject(z, v, (*ObjectHeader)(p))
			collected++
		case TypeReference:
			if err := z.Free(v.DataSlot, refCellSize); err != nil {
				panic(err)
			}
		}
	}
	s.Work = s.Work[:0]
	return collected
}
