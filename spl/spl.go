// Package spl implémente les structures de données, itérateurs, exceptions et fonctions SPL de PHP 8.3.
// Toutes les valeurs PHP manipulées respectent l'invariant 0-Ptr dans la Zone.
package spl

import (
	"fmt"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Constantes d'itération de SplDoublyLinkedList, SplStack et SplQueue.
const (
	IT_MODE_FIFO   int64 = 0
	IT_MODE_LIFO   int64 = 2
	IT_MODE_KEEP   int64 = 0
	IT_MODE_DELETE int64 = 1
	IT_MODE_FIX    int64 = 4
)

// Constantes d'options de ArrayObject et ArrayIterator.
const (
	STD_PROP_LIST  int64 = 1
	ARRAY_AS_PROPS int64 = 2
)

// Constantes de tri PHP utilisées par ArrayObject et ArrayIterator.
const (
	SORT_REGULAR   int64 = 0
	SORT_NUMERIC   int64 = 1
	SORT_STRING    int64 = 2
	SORT_NATURAL   int64 = 6
	SORT_FLAG_CASE int64 = 8
)

// Identifiants de classes SPL enregistrés dans le paquet types.
const (
	ClassIDSplDoublyLinkedList uint32 = 5301
	ClassIDSplStack            uint32 = 5302
	ClassIDSplQueue            uint32 = 5303
	ClassIDSplFixedArray       uint32 = 5304
	ClassIDSplObjectStorage    uint32 = 5305
	ClassIDArrayObject         uint32 = 5306
	ClassIDArrayIterator       uint32 = 5307

	CleanupSPL zone.CleanupKind = 53
)

// SPLException représente une exception SPL ou standard PHP (RuntimeException, OutOfRangeException, etc.).
type SPLException struct {
	Class   string
	Message string
}

func (e SPLException) Error() string {
	return e.Class + ": " + e.Message
}

// Comparator est la fonction de rappel fournie à uasort / uksort.
type Comparator func(z *zone.Zone, a, b types.Value) (int, error)

type objKey struct {
	slot uint32
	gen  uint16
}

type zoneRegistry struct {
	id            uint32
	z             *zone.Zone
	dlls          map[objKey]*SplDoublyLinkedList
	fixedArrays   map[objKey]*SplFixedArray
	objStorages   map[objKey]*SplObjectStorage
	arrayObjects  map[objKey]*ArrayObject
	arrayIters    map[objKey]*ArrayIterator
	cleaned       bool
}

var (
	regMu     sync.Mutex
	nextRegID uint32
	regsByZ   = make(map[*zone.Zone]*zoneRegistry)
	regsByID  = make(map[uint32]*zoneRegistry)
)

var splClassList = []string{
	"AppendIterator",
	"ArrayIterator",
	"ArrayObject",
	"BadFunctionCallException",
	"BadMethodCallException",
	"CachingIterator",
	"CallbackFilterIterator",
	"DirectoryIterator",
	"DomainException",
	"EmptyIterator",
	"FilesystemIterator",
	"FilterIterator",
	"GlobIterator",
	"InfiniteIterator",
	"InvalidArgumentException",
	"IteratorIterator",
	"LengthException",
	"LimitIterator",
	"LogicException",
	"MultipleIterator",
	"NoRewindIterator",
	"OuterIterator",
	"OutOfBoundsException",
	"OutOfRangeException",
	"OverflowException",
	"ParentIterator",
	"RangeException",
	"RecursiveArrayIterator",
	"RecursiveCachingIterator",
	"RecursiveCallbackFilterIterator",
	"RecursiveDirectoryIterator",
	"RecursiveFilterIterator",
	"RecursiveIterator",
	"RecursiveIteratorIterator",
	"RecursiveRegexIterator",
	"RecursiveTreeIterator",
	"RegexIterator",
	"RuntimeException",
	"SeekableIterator",
	"SplDoublyLinkedList",
	"SplFileInfo",
	"SplFileObject",
	"SplFixedArray",
	"SplHeap",
	"SplMinHeap",
	"SplMaxHeap",
	"SplObjectStorage",
	"SplObserver",
	"SplPriorityQueue",
	"SplQueue",
	"SplStack",
	"SplSubject",
	"SplTempFileObject",
	"UnderflowException",
	"UnexpectedValueException",
}

func init() {
	EnsureClassNames()
}

// EnsureClassNames enregistre les noms de classes SPL dans la table du paquet types si elle n'est pas gelée.
func EnsureClassNames() {
	if !types.IsProcessZoneFrozen() {
		types.RegisterObjectClassName(ClassIDSplDoublyLinkedList, "SplDoublyLinkedList")
		types.RegisterObjectClassName(ClassIDSplStack, "SplStack")
		types.RegisterObjectClassName(ClassIDSplQueue, "SplQueue")
		types.RegisterObjectClassName(ClassIDSplFixedArray, "SplFixedArray")
		types.RegisterObjectClassName(ClassIDSplObjectStorage, "SplObjectStorage")
		types.RegisterObjectClassName(ClassIDArrayObject, "ArrayObject")
		types.RegisterObjectClassName(ClassIDArrayIterator, "ArrayIterator")
	}
}

func getOrCreateRegistry(z *zone.Zone) *zoneRegistry {
	if z == nil {
		panic("spl: nil zone")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if r, ok := regsByZ[z]; ok {
		return r
	}
	nextRegID++
	id := nextRegID
	r := &zoneRegistry{
		id:           id,
		z:            z,
		dlls:         make(map[objKey]*SplDoublyLinkedList),
		fixedArrays:  make(map[objKey]*SplFixedArray),
		objStorages:  make(map[objKey]*SplObjectStorage),
		arrayObjects: make(map[objKey]*ArrayObject),
		arrayIters:   make(map[objKey]*ArrayIterator),
	}
	regsByZ[z] = r
	regsByID[id] = r
	z.RegisterCleanup(zone.CleanupItem{
		Kind:   CleanupSPL,
		Target: id,
	})
	return r
}

func lookupRegistry(z *zone.Zone) *zoneRegistry {
	regMu.Lock()
	defer regMu.Unlock()
	return regsByZ[z]
}

// CleanupDispatcher retourne la fonction de rappel compatible avec z.Reset pour le paquet spl.
func CleanupDispatcher(z *zone.Zone) func(zone.CleanupItem) {
	return func(item zone.CleanupItem) {
		DispatchCleanup(z, item)
	}
}

// DispatchCleanup libère toutes les structures SPL associées à la Zone lors de z.Reset.
func DispatchCleanup(z *zone.Zone, item zone.CleanupItem) {
	switch item.Kind {
	case CleanupSPL:
		regMu.Lock()
		r := regsByID[item.Target]
		regMu.Unlock()
		if r == nil {
			return
		}
		if z == nil {
			z = r.z
		}
		r.cleanupAll(z)
		regMu.Lock()
		delete(regsByID, r.id)
		if regsByZ[z] == r {
			delete(regsByZ, z)
		}
		regMu.Unlock()
	case zone.CleanupDestructor:
		if z != nil {
			types.DispatchObjectDestructor(z, item)
		}
	}
}

func (r *zoneRegistry) cleanupAll(z *zone.Zone) {
	if r.cleaned {
		return
	}
	r.cleaned = true
	for _, dll := range r.dlls {
		dll.releaseAll(z)
	}
	for _, fa := range r.fixedArrays {
		fa.releaseAll(z)
	}
	for _, os := range r.objStorages {
		os.releaseAll(z)
	}
	for _, ao := range r.arrayObjects {
		ao.releaseAll(z)
	}
	for _, ai := range r.arrayIters {
		ai.releaseAll(z)
	}
	clear(r.dlls)
	clear(r.fixedArrays)
	clear(r.objStorages)
	clear(r.arrayObjects)
	clear(r.arrayIters)
}

// SplObjectID implémente spl_object_id($object): int.
func SplObjectID(z *zone.Zone, obj types.Value) (types.Value, error) {
	obj = types.Deref(z, obj)
	if obj.Type != types.TypeObject {
		return types.NewNull(), SPLException{
			Class:   "TypeError",
			Message: "spl_object_id(): Argument #1 ($object) must be of type object",
		}
	}
	types.ObjectInfo(z, obj)
	return types.NewLong(int64(obj.DataSlot) + 1), nil
}

// SplObjectHashString retourne la chaîne hexadécimale de 32 caractères identifiant un objet.
func SplObjectHashString(z *zone.Zone, obj types.Value) (string, error) {
	obj = types.Deref(z, obj)
	if obj.Type != types.TypeObject {
		return "", SPLException{
			Class:   "TypeError",
			Message: "spl_object_hash(): Argument #1 ($object) must be of type object",
		}
	}
	types.ObjectInfo(z, obj)
	return fmt.Sprintf("%016x0000000000000000", uint64(obj.DataSlot)+1), nil
}

// SplObjectHash implémente spl_object_hash($object): string.
func SplObjectHash(z *zone.Zone, obj types.Value) (types.Value, error) {
	s, err := SplObjectHashString(z, obj)
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewString(z, s, 0)
}

// SplClasses implémente spl_classes(): array.
func SplClasses(z *zone.Zone) (types.Value, error) {
	arr, err := types.NewArray(z)
	if err != nil {
		return types.NewNull(), err
	}
	for _, name := range splClassList {
		sv, err := types.NewString(z, name, 0)
		if err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
		err = types.ArraySet(z, &arr, sv, sv, nil)
		types.ReleaseString(z, &sv)
		if err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
	}
	return arr, nil
}
