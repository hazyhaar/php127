// Package zone provides fixed-capacity, pointer-free request memory and LIFO
// cleanup. Raw memory must not contain Go pointers; retain resources outside the
// arena and refer to them by scalar identifiers.
package zone

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

const DefaultCapacity uint32 = 2 << 20

// DefaultUmask est le masque de création initial de chaque Zone PHP.
const DefaultUmask = 0o022

// MaxSlots bounds the per-Zone state slots reserved for extension packages.
const MaxSlots = 16

// Slot indexes owned by engine packages. Each package stores its per-request
// state in its own slot only; Reset clears every slot after the cleanup drain.
const (
	SlotStreams = 0
	SlotExt     = 1
	SlotStdlib  = 2
	SlotMySQLi  = 3
	SlotPDO     = 4
	SlotVM      = 5
)

// poisonByte fills released payload bytes when poisoning is enabled.
const poisonByte = 0xAA

var (
	ErrExhausted     = errors.New("zone: capacity exhausted")
	ErrInvalidSize   = errors.New("zone: allocation size must be positive")
	ErrInvalidOffset = errors.New("zone: address outside allocated memory")
)

// noCopy lets go vet detect accidental copies of an owning Zone.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// Zone owns one contiguous block whose base never moves. Offsets are relative
// to that base; zero is a valid offset, not a null sentinel. The zero value has
// zero capacity. A Zone must not be copied after first use or used concurrently.
type Zone struct {
	noCopy     noCopy
	memory     []byte
	cursor     uint32
	cleanups   CleanupRegistry
	freeHead   [28]uint32
	cycles     CycleState
	objects    []objectSlot
	objectFree uint32
	objectTail uint32
	objectUsed uint32
	epoch      uint32
	poison     bool
	cwd        string
	umask      int
	umaskSet   bool
	slots      [MaxSlots]any
}

// New reserves capacity bytes, rounded down to a multiple of eight. Use
// DefaultCapacity for a 2 MiB arena. Only construction allocates arena memory;
// Alloc never grows or replaces it. Like make, New can panic on host exhaustion.
func New(capacity uint32) *Zone {
	// A uint64 backing array guarantees base alignment, including tiny arenas.
	words := make([]uint64, capacity/8)
	z := &Zone{objectFree: math.MaxUint32, objectTail: math.MaxUint32}
	z.cycles.Roots = make([]uint64, 0, min(capacity/24, 10000))
	z.cycles.Work = make([]uint64, 0, min(capacity/24, 10000))
	z.objects = make([]objectSlot, min(capacity/32, 4096))
	for i := range z.freeHead {
		z.freeHead[i] = math.MaxUint32
	}
	if len(words) != 0 {
		z.memory = unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*8)
	}
	return z
}

// Capacity rend la capacité totale de l'arène en octets.
func (z *Zone) Capacity() uint32 {
	if z == nil {
		return 0
	}
	return uint32(len(z.memory))
}

// Grow étend la capacité de l'arène à au moins minCap octets en conservant le préfixe alloué et le curseur.
// Réservé à la construction de la zone de processus avant son gel.
func (z *Zone) Grow(minCap uint32) {
	if z == nil || minCap <= uint32(len(z.memory)) {
		return
	}
	words := make([]uint64, (uint64(minCap)+7)/8)
	nextMem := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*8)
	copy(nextMem[:z.cursor], z.memory[:z.cursor])
	z.memory = nextMem
}

// sizeClass widens before rounding so sizes near 4 GiB cannot wrap to zero.
// Callers reject zero before selecting a class.
func sizeClass(size uint32) (class int, span uint64) {
	rounded := (uint64(size) + 7) &^ uint64(7)
	if rounded <= 64 {
		return int(rounded/8) - 1, rounded
	}
	if rounded <= 64<<20 {
		exponent := bits.Len64(rounded - 1)
		return exponent + 1, uint64(1) << exponent
	}
	return -1, rounded
}

// Alloc reserves a positive size rounded up to its size class without zeroing it.
// Failure leaves the cursor unchanged. Returned offsets fit types.Value.DataSlot.
func (z *Zone) Alloc(size uint32) (uint32, error) {
	if size == 0 {
		return 0, ErrInvalidSize
	}
	class, span := sizeClass(size)
	// The zero-value Zone has no initialized heads and no allocated prefix.
	if class >= 0 && z.cursor != 0 {
		if offset := z.freeHead[class]; offset != math.MaxUint32 {
			z.freeHead[class] = binary.LittleEndian.Uint32(z.memory[offset+4 : offset+8])
			return offset, nil
		}
	}
	end := uint64(z.cursor) + span
	if end > uint64(len(z.memory)) {
		return 0, ErrExhausted
	}
	offset := z.cursor
	z.cursor = uint32(end)
	return offset, nil
}

// Free consumes a live allocation with its original size. It checks prefix
// bounds, not allocation identity: double frees and interior frees are invalid
// caller behavior. Views expire immediately. Non-top blocks above 64 MiB are
// retained until Reset. Classes are multiples of eight through 64 bytes, then
// powers of two through 64 MiB.
func (z *Zone) Free(offset, size uint32) error {
	if size == 0 {
		return ErrInvalidSize
	}
	class, span := sizeClass(size)
	end := uint64(offset) + span
	if offset%8 != 0 || end > uint64(z.cursor) {
		return ErrInvalidOffset
	}
	// Keep a released object's reference count invalid, apart from the list link.
	binary.LittleEndian.PutUint32(z.memory[offset:offset+4], 0)
	// Poisoning precedes the link write and never touches the eight header bytes.
	if z.poison && span > 8 {
		fillPoison(z.memory[offset+8 : end])
	}
	if end == uint64(z.cursor) {
		z.cursor = offset
	} else if class >= 0 {
		binary.LittleEndian.PutUint32(z.memory[offset+4:offset+8], z.freeHead[class])
		z.freeHead[class] = offset
	}
	return nil
}

// Bytes returns a capacity-limited view into the allocated prefix, including
// alignment padding. It checks arena bounds, not individual allocation bounds.
// Views and offsets expire at Reset; stale references cannot be detected after
// the same address is allocated again. No Go pointers may be stored in the view.
func (z *Zone) Bytes(offset, size uint32) ([]byte, error) {
	end := uint64(offset) + uint64(size)
	if end > uint64(z.cursor) {
		return nil, ErrInvalidOffset
	}
	return z.memory[offset:end:end], nil
}

// Offset converts an address in the current allocated prefix back to a byte
// offset. Interior addresses are accepted; nil and one-past-end are rejected.
func (z *Zone) Offset(ptr *byte) (uint32, error) {
	if ptr == nil || z.cursor == 0 {
		return 0, ErrInvalidOffset
	}
	base := uintptr(unsafe.Pointer(unsafe.SliceData(z.memory)))
	address := uintptr(unsafe.Pointer(ptr))
	if address < base || uint64(address-base) >= uint64(z.cursor) {
		return 0, ErrInvalidOffset
	}
	return uint32(address - base), nil
}

func fillPoison(b []byte) {
	for i := range b {
		b[i] = poisonByte
	}
}

// SetPoisonOnFree makes Free overwrite released payload bytes beyond the
// eight-byte free-list header with 0xAA, so stale views read poison at once.
func (z *Zone) SetPoisonOnFree(enable bool) { z.poison = enable }

// PoisonOnFree reports whether Free poisons released payload bytes.
func (z *Zone) PoisonOnFree() bool { return z.poison }

// Slot returns the extension state stored at id, or nil for a nil Zone or an
// id outside [0, MaxSlots). Slots hold Go values outside the arena.
func (z *Zone) Slot(id int) any {
	if z == nil || id < 0 || id >= MaxSlots {
		return nil
	}
	return z.slots[id]
}

// SetSlot stores extension state at id; out-of-range ids and nil Zones are ignored.
// Reset clears every slot after the cleanup drain.
func (z *Zone) SetSlot(id int, v any) {
	if z == nil || id < 0 || id >= MaxSlots {
		return
	}
	z.slots[id] = v
}

// Cwd rend le répertoire courant de la Zone, ou celui du processus si aucun chdir n'a eu lieu.
func (z *Zone) Cwd() string {
	if z != nil && z.cwd != "" {
		return z.cwd
	}
	wd, _ := os.Getwd()
	return wd
}

// SetCwd fixe le répertoire courant de la Zone à une copie indépendante de dir.
func (z *Zone) SetCwd(dir string) {
	if z == nil {
		return
	}
	z.cwd = strings.Clone(dir)
}

// ResolvePath résout un chemin relatif contre le répertoire courant de la Zone.
func (z *Zone) ResolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	cwd := z.Cwd()
	if cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}

// Umask rend le masque de création de la Zone (DefaultUmask par défaut).
func (z *Zone) Umask() int {
	if z == nil || !z.umaskSet {
		return DefaultUmask
	}
	return z.umask
}

// SetUmask pose le masque de création de la Zone (sur 9 bits) et rend l'ancien masque.
func (z *Zone) SetUmask(mask int) int {
	old := z.Umask()
	if z != nil {
		z.umask = mask & 0o777
		z.umaskSet = true
	}
	return old
}

// CreateMode rend perm privé du masque de création de la Zone.
func (z *Zone) CreateMode(perm os.FileMode) os.FileMode {
	return perm &^ os.FileMode(z.Umask()) & 0o777
}

func (z *Zone) RegisterCleanup(item CleanupItem) {
	z.cleanups.RegisterCleanup(item)
}

// PendingCleanups excludes the item currently being dispatched.
func (z *Zone) PendingCleanups() int { return z.cleanups.Len() }

// RunCleanups drains the registry without releasing arena memory.
func (z *Zone) RunCleanups(dispatch func(CleanupItem)) {
	z.cleanups.RunCleanups(dispatch)
}

// Reset drains cleanups while memory is live, invalidates issued object slots,
// rewinds the arena cursor and free lists in O(1), then clears state slots.
// Neither memory nor registry capacity is cleared or freed. After dispatch
// panics, draining and rewinding finish before the first panic is rethrown.
// Invalid or recursive dispatch leaves memory and pending items unchanged.
// If Goexit interrupts draining with pending items, memory remains live.
func (z *Zone) Reset(dispatch func(CleanupItem)) {
	z.cleanups.checkDispatch(dispatch)
	defer func() {
		if z.cleanups.Len() == 0 {
			z.cycles.Roots = z.cycles.Roots[:0]
			z.cycles.Work = z.cycles.Work[:0]
			z.objectFree = math.MaxUint32
			z.objectTail = math.MaxUint32
			z.objectUsed = 0
			z.epoch++
			z.cursor = 0
			z.cwd = ""
			z.umask = 0
			z.umaskSet = false
			clear(z.slots[:])
			for i := range z.freeHead {
				z.freeHead[i] = math.MaxUint32
			}
		}
	}()
	z.cleanups.RunCleanups(dispatch)
}

// CycleState stores opaque scalar node identities. The types package alone
// interprets them and owns the collection protocol. Construction reserves the
// maximum number of nodes that can fit in the arena (minimum node size: 24).
type CycleState struct {
	Roots, Work []uint64
}

func (z *Zone) Cycles() *CycleState { return &z.cycles }

// BufferedRoots reports the number of candidate cycle roots without allocating.
func (z *Zone) BufferedRoots() int { return len(z.cycles.Roots) }

// LiveBytes counts allocated spans below the cursor excluding recycled blocks.
// Non-top blocks larger than the largest free class remain reserved until Reset.
func (z *Zone) LiveBytes() uint64 {
	remaining := uint64(z.cursor)
	if remaining == 0 {
		return 0
	}
	for class, head := range z.freeHead {
		span := uint64(class+1) * 8
		if class >= 8 {
			span = uint64(1) << (class - 1)
		}
		for off := head; off != math.MaxUint32; off = binary.LittleEndian.Uint32(z.memory[off+4 : off+8]) {
			remaining -= span
		}
	}
	return remaining
}

type objectSlot struct {
	offset     uint32
	next       uint32
	epoch      uint32
	generation uint16
}

// NewObjectHandle publishes a header already allocated in this Zone. Generation
// metadata lives outside recyclable bytes so other allocation kinds cannot erase it.
func (z *Zone) NewObjectHandle(offset uint32) (uint32, uint16, error) {
	maxSlots := uint32(len(z.memory)) / 32
	for z.objectUsed < maxSlots {
		if z.objectUsed == uint32(len(z.objects)) {
			nextCap := min(max(uint32(len(z.objects))*2, 64), maxSlots)
			grown := make([]objectSlot, nextCap)
			copy(grown, z.objects)
			z.objects = grown
		}
		index := z.objectUsed
		z.objectUsed++
		s := &z.objects[index]
		if s.generation == math.MaxUint16 {
			continue
		}
		s.generation++
		s.offset = offset
		s.next = math.MaxUint32
		s.epoch = z.epoch
		return index, s.generation, nil
	}
	if z.objectFree != math.MaxUint32 {
		index := z.objectFree
		z.objectFree = z.objects[index].next
		if z.objectFree == math.MaxUint32 {
			z.objectTail = math.MaxUint32
		}
		s := &z.objects[index]
		s.generation++
		s.offset = offset
		s.next = math.MaxUint32
		s.epoch = z.epoch
		return index, s.generation, nil
	}
	return 0, 0, ErrExhausted
}

func (z *Zone) ObjectAddress(index uint32, generation uint16) (uint32, bool) {
	if index >= z.objectUsed {
		return 0, false
	}
	s := &z.objects[index]
	return s.offset, generation != 0 && s.epoch == z.epoch && s.generation == generation && s.offset != math.MaxUint32
}

func (z *Zone) FreeObjectHandle(index uint32, generation uint16) {
	if _, ok := z.ObjectAddress(index, generation); !ok {
		panic("zone: invalid object handle")
	}
	s := &z.objects[index]
	// A 16-bit generation cannot wrap without admitting an old handle (ABA).
	// Retire the slot permanently; exhaustion is safer than aliasing a new object.
	if s.generation == math.MaxUint16 {
		s.offset = math.MaxUint32
		return
	}
	s.offset, s.next = math.MaxUint32, math.MaxUint32
	if z.objectFree == math.MaxUint32 {
		z.objectFree = index
	} else {
		z.objects[z.objectTail].next = index
	}
	z.objectTail = index
}
