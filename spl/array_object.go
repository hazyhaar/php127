package spl

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type kvPair struct {
	key types.Value
	val types.Value
}

// arrayBacking détient le tableau PHP sous-jacent partagé entre ArrayObject et ses ArrayIterator.
type arrayBacking struct {
	arr      types.Value
	released bool
}

func newArrayBacking(z *zone.Zone, initial types.Value) (*arrayBacking, error) {
	arr, err := copyPHPArray(z, initial)
	if err != nil {
		return nil, err
	}
	return &arrayBacking{arr: arr}, nil
}

func (b *arrayBacking) release(z *zone.Zone) {
	if b.released {
		return
	}
	b.released = true
	if !b.arr.IsUndef() {
		types.ReleaseArray(z, &b.arr)
	}
}

func copyPHPArray(z *zone.Zone, src types.Value) (types.Value, error) {
	dst, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	src = types.Deref(z, src)
	if src.IsUndef() || src.IsNull() {
		return dst, nil
	}
	if src.Type != types.TypeArray {
		types.ReleaseArray(z, &dst)
		return types.Value{}, SPLException{
			Class:   "InvalidArgumentException",
			Message: "Passed variable is not an array or object",
		}
	}
	var copyErr error
	types.ArrayForEach(z, src, func(k, v types.Value) bool {
		if err := types.ArraySet(z, &dst, k, types.Deref(z, v), nil); err != nil {
			copyErr = err
			return false
		}
		return true
	})
	if copyErr != nil {
		types.ReleaseArray(z, &dst)
		return types.Value{}, copyErr
	}
	return dst, nil
}

// ArrayObject implémente la classe ArrayObject de PHP 8.3.
type ArrayObject struct {
	z             *zone.Zone
	obj           types.Value
	backing       *arrayBacking
	flags         int64
	iteratorClass string
}

// ArrayIterator implémente la classe ArrayIterator de PHP 8.3.
type ArrayIterator struct {
	z       *zone.Zone
	obj     types.Value
	backing *arrayBacking
	flags   int64
	keys    []types.Value
	cursor  int
	dirty   bool
}

// NewArrayObject crée une instance de ArrayObject dans la Zone.
func NewArrayObject(z *zone.Zone, initial types.Value, flags int64, iteratorClass string) (types.Value, *ArrayObject, error) {
	if iteratorClass == "" {
		iteratorClass = "ArrayIterator"
	}
	backing, err := newArrayBacking(z, initial)
	if err != nil {
		return types.Value{}, nil, err
	}
	obj, err := types.NewObject(z, ClassIDArrayObject, 0, false)
	if err != nil {
		backing.release(z)
		return types.Value{}, nil, err
	}
	ao := &ArrayObject{
		z:             z,
		obj:           obj,
		backing:       backing,
		flags:         flags,
		iteratorClass: iteratorClass,
	}
	reg := getOrCreateRegistry(z)
	reg.arrayObjects[objKey{slot: obj.DataSlot, gen: obj.Extra}] = ao
	return obj, ao, nil
}

// ArrayObjectFromObject retrouve l'instance ArrayObject depuis une Value objet.
func ArrayObjectFromObject(z *zone.Zone, v types.Value) (*ArrayObject, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, SPLException{Class: "TypeError", Message: "Expected ArrayObject"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, SPLException{Class: "RuntimeException", Message: "Uninitialized SPL registry"}
	}
	ao, ok := reg.arrayObjects[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, SPLException{Class: "RuntimeException", Message: "Object is not a registered ArrayObject"}
	}
	return ao, nil
}

func (a *ArrayObject) releaseAll(z *zone.Zone) {
	if a.backing != nil {
		a.backing.release(z)
	}
}

// Object retourne la Value objet associée.
func (a *ArrayObject) Object() types.Value {
	return a.obj
}

// NewArrayIterator crée une instance de ArrayIterator dans la Zone.
func NewArrayIterator(z *zone.Zone, initial types.Value, flags int64) (types.Value, *ArrayIterator, error) {
	backing, err := newArrayBacking(z, initial)
	if err != nil {
		return types.Value{}, nil, err
	}
	return newArrayIteratorWithBacking(z, backing, flags)
}

func newArrayIteratorWithBacking(z *zone.Zone, backing *arrayBacking, flags int64) (types.Value, *ArrayIterator, error) {
	obj, err := types.NewObject(z, ClassIDArrayIterator, 0, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	ai := &ArrayIterator{
		z:       z,
		obj:     obj,
		backing: backing,
		flags:   flags,
		cursor:  0,
		dirty:   true,
	}
	ai.rebuildKeys(z)
	reg := getOrCreateRegistry(z)
	reg.arrayIters[objKey{slot: obj.DataSlot, gen: obj.Extra}] = ai
	return obj, ai, nil
}

// ArrayIteratorFromObject retrouve l'instance ArrayIterator depuis une Value objet.
func ArrayIteratorFromObject(z *zone.Zone, v types.Value) (*ArrayIterator, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, SPLException{Class: "TypeError", Message: "Expected ArrayIterator"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, SPLException{Class: "RuntimeException", Message: "Uninitialized SPL registry"}
	}
	ai, ok := reg.arrayIters[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, SPLException{Class: "RuntimeException", Message: "Object is not a registered ArrayIterator"}
	}
	return ai, nil
}

func (it *ArrayIterator) releaseKeys(z *zone.Zone) {
	for i := range it.keys {
		if !it.keys[i].IsUndef() {
			types.ReleaseValue(z, &it.keys[i])
		}
	}
	it.keys = it.keys[:0]
}

func (it *ArrayIterator) releaseAll(z *zone.Zone) {
	it.releaseKeys(z)
	if it.backing != nil {
		it.backing.release(z)
	}
}

func (it *ArrayIterator) rebuildKeys(z *zone.Zone) {
	it.releaseKeys(z)
	if it.backing == nil || it.backing.released || it.backing.arr.IsUndef() {
		it.dirty = false
		return
	}
	types.ArrayForEach(z, it.backing.arr, func(k, _ types.Value) bool {
		it.keys = append(it.keys, types.RetainValue(z, k))
		return true
	})
	it.dirty = false
}

// Object retourne la Value objet associée à l'ArrayIterator.
func (it *ArrayIterator) Object() types.Value {
	return it.obj
}

// Méthodes communes sur ArrayObject :

func (a *ArrayObject) Append(z *zone.Zone, val types.Value) error {
	return types.ArrayAppend(z, &a.backing.arr, types.Deref(z, val))
}

func (a *ArrayObject) Count(z *zone.Zone) int {
	return int(types.ArrayLen(z, a.backing.arr))
}

func (a *ArrayObject) GetFlags() int64 {
	return a.flags
}

func (a *ArrayObject) SetFlags(flags int64) {
	a.flags = flags
}

func (a *ArrayObject) GetIteratorClass() string {
	return a.iteratorClass
}

func (a *ArrayObject) SetIteratorClass(class string) {
	a.iteratorClass = class
}

func (a *ArrayObject) GetArrayCopy(z *zone.Zone) (types.Value, error) {
	return copyPHPArray(z, a.backing.arr)
}

func (a *ArrayObject) ExchangeArray(z *zone.Zone, next types.Value) (types.Value, error) {
	newArr, err := copyPHPArray(z, next)
	if err != nil {
		return types.Value{}, err
	}
	old := a.backing.arr
	a.backing.arr = newArr
	return old, nil
}

func (a *ArrayObject) GetIterator(z *zone.Zone) (types.Value, *ArrayIterator, error) {
	return newArrayIteratorWithBacking(z, a.backing, a.flags)
}

func (a *ArrayObject) OffsetExists(z *zone.Zone, key types.Value) (bool, error) {
	return types.ArrayKeyExists(z, a.backing.arr, key, nil)
}

func (a *ArrayObject) OffsetGet(z *zone.Zone, key types.Value) (types.Value, error) {
	v, ok, err := types.ArrayGet(z, a.backing.arr, key, nil)
	if err != nil || !ok {
		return types.NewNull(), err
	}
	return v, nil
}

func (a *ArrayObject) OffsetSet(z *zone.Zone, key, val types.Value) error {
	key = types.Deref(z, key)
	if key.IsNull() || key.IsUndef() {
		return types.ArrayAppend(z, &a.backing.arr, types.Deref(z, val))
	}
	return types.ArraySet(z, &a.backing.arr, key, types.Deref(z, val), nil)
}

func (a *ArrayObject) OffsetUnset(z *zone.Zone, key types.Value) error {
	return types.ArrayUnset(z, &a.backing.arr, key, nil)
}

func (a *ArrayObject) Asort(z *zone.Zone, flags int64) error {
	return sortBacking(z, a.backing, false, func(x, y kvPair) (int, error) {
		return compareValuesByFlags(z, x.val, y.val, flags), nil
	})
}

func (a *ArrayObject) Ksort(z *zone.Zone, flags int64) error {
	return sortBacking(z, a.backing, true, func(x, y kvPair) (int, error) {
		return compareValuesByFlags(z, x.key, y.key, flags), nil
	})
}

func (a *ArrayObject) Natsort(z *zone.Zone) error {
	return a.Asort(z, SORT_NATURAL)
}

func (a *ArrayObject) Natcasesort(z *zone.Zone) error {
	return a.Asort(z, SORT_NATURAL|SORT_FLAG_CASE)
}

func (a *ArrayObject) Uasort(z *zone.Zone, cmpFn Comparator) error {
	if cmpFn == nil {
		return SPLException{Class: "TypeError", Message: "uasort(): Argument #1 ($callback) must be a valid callback"}
	}
	return sortBacking(z, a.backing, false, func(x, y kvPair) (int, error) {
		return cmpFn(z, x.val, y.val)
	})
}

func (a *ArrayObject) Uksort(z *zone.Zone, cmpFn Comparator) error {
	if cmpFn == nil {
		return SPLException{Class: "TypeError", Message: "uksort(): Argument #1 ($callback) must be a valid callback"}
	}
	return sortBacking(z, a.backing, true, func(x, y kvPair) (int, error) {
		return cmpFn(z, x.key, y.key)
	})
}

// Méthodes sur ArrayIterator :

func (it *ArrayIterator) Append(z *zone.Zone, val types.Value) error {
	if err := types.ArrayAppend(z, &it.backing.arr, types.Deref(z, val)); err != nil {
		return err
	}
	it.rebuildKeys(z)
	return nil
}

func (it *ArrayIterator) Count(z *zone.Zone) int {
	return int(types.ArrayLen(z, it.backing.arr))
}

func (it *ArrayIterator) GetFlags() int64 {
	return it.flags
}

func (it *ArrayIterator) SetFlags(flags int64) {
	it.flags = flags
}

func (it *ArrayIterator) GetArrayCopy(z *zone.Zone) (types.Value, error) {
	return copyPHPArray(z, it.backing.arr)
}

func (it *ArrayIterator) OffsetExists(z *zone.Zone, key types.Value) (bool, error) {
	return types.ArrayKeyExists(z, it.backing.arr, key, nil)
}

func (it *ArrayIterator) OffsetGet(z *zone.Zone, key types.Value) (types.Value, error) {
	v, ok, err := types.ArrayGet(z, it.backing.arr, key, nil)
	if err != nil || !ok {
		return types.NewNull(), err
	}
	return v, nil
}

func (it *ArrayIterator) OffsetSet(z *zone.Zone, key, val types.Value) error {
	key = types.Deref(z, key)
	var err error
	if key.IsNull() || key.IsUndef() {
		err = types.ArrayAppend(z, &it.backing.arr, types.Deref(z, val))
	} else {
		err = types.ArraySet(z, &it.backing.arr, key, types.Deref(z, val), nil)
	}
	if err == nil {
		it.rebuildKeys(z)
	}
	return err
}

func (it *ArrayIterator) OffsetUnset(z *zone.Zone, key types.Value) error {
	curKey := types.Value{}
	hasCur := it.Valid(z)
	if hasCur {
		curKey = it.keys[it.cursor]
	}
	normKey, err := types.NormalizeArrayKey(z, key, nil)
	if err != nil {
		return err
	}
	deletingCurrent := hasCur && sameNormalizedKey(z, curKey, normKey)
	if err := types.ArrayUnset(z, &it.backing.arr, key, nil); err != nil {
		return err
	}
	it.rebuildKeys(z)
	if deletingCurrent && it.cursor > len(it.keys) {
		it.cursor = len(it.keys)
	}
	return nil
}

func sameNormalizedKey(z *zone.Zone, a, b types.Value) bool {
	if a.Type != b.Type {
		return false
	}
	if a.Type == types.TypeLong {
		return a.ToLong() == b.ToLong()
	}
	if a.Type == types.TypeString {
		return types.StringData(z, a) == types.StringData(z, b)
	}
	return false
}

func (it *ArrayIterator) Rewind(z *zone.Zone) {
	it.rebuildKeys(z)
	it.cursor = 0
}

func (it *ArrayIterator) Valid(z *zone.Zone) bool {
	if it.dirty {
		it.rebuildKeys(z)
	}
	return it.cursor >= 0 && it.cursor < len(it.keys)
}

func (it *ArrayIterator) Current(z *zone.Zone) types.Value {
	if !it.Valid(z) {
		return types.NewNull()
	}
	v, ok, err := types.ArrayGet(z, it.backing.arr, it.keys[it.cursor], nil)
	if err != nil || !ok {
		return types.NewNull()
	}
	return v
}

func (it *ArrayIterator) Key(z *zone.Zone) types.Value {
	if !it.Valid(z) {
		return types.NewNull()
	}
	return it.keys[it.cursor]
}

func (it *ArrayIterator) Next(z *zone.Zone) {
	if it.dirty {
		it.rebuildKeys(z)
	}
	it.cursor++
}

func (it *ArrayIterator) Seek(z *zone.Zone, position int) error {
	it.rebuildKeys(z)
	if position < 0 || position >= len(it.keys) {
		return SPLException{
			Class:   "OutOfBoundsException",
			Message: fmt.Sprintf("Seek position %d is out of range", position),
		}
	}
	it.cursor = position
	return nil
}

func (it *ArrayIterator) Asort(z *zone.Zone, flags int64) error {
	if err := sortBacking(z, it.backing, false, func(x, y kvPair) (int, error) {
		return compareValuesByFlags(z, x.val, y.val, flags), nil
	}); err != nil {
		return err
	}
	it.rebuildKeys(z)
	return nil
}

func (it *ArrayIterator) Ksort(z *zone.Zone, flags int64) error {
	if err := sortBacking(z, it.backing, true, func(x, y kvPair) (int, error) {
		return compareValuesByFlags(z, x.key, y.key, flags), nil
	}); err != nil {
		return err
	}
	it.rebuildKeys(z)
	return nil
}

func (it *ArrayIterator) Natsort(z *zone.Zone) error {
	return it.Asort(z, SORT_NATURAL)
}

func (it *ArrayIterator) Natcasesort(z *zone.Zone) error {
	return it.Asort(z, SORT_NATURAL|SORT_FLAG_CASE)
}

func (it *ArrayIterator) Uasort(z *zone.Zone, cmpFn Comparator) error {
	if cmpFn == nil {
		return SPLException{Class: "TypeError", Message: "uasort(): Argument #1 ($callback) must be a valid callback"}
	}
	if err := sortBacking(z, it.backing, false, func(x, y kvPair) (int, error) {
		return cmpFn(z, x.val, y.val)
	}); err != nil {
		return err
	}
	it.rebuildKeys(z)
	return nil
}

func (it *ArrayIterator) Uksort(z *zone.Zone, cmpFn Comparator) error {
	if cmpFn == nil {
		return SPLException{Class: "TypeError", Message: "uksort(): Argument #1 ($callback) must be a valid callback"}
	}
	if err := sortBacking(z, it.backing, true, func(x, y kvPair) (int, error) {
		return cmpFn(z, x.key, y.key)
	}); err != nil {
		return err
	}
	it.rebuildKeys(z)
	return nil
}

func sortBacking(z *zone.Zone, b *arrayBacking, byKey bool, cmpPair func(x, y kvPair) (int, error)) error {
	_ = byKey
	var pairs []kvPair
	types.ArrayForEach(z, b.arr, func(k, v types.Value) bool {
		pairs = append(pairs, kvPair{
			key: types.RetainValue(z, k),
			val: types.RetainValue(z, types.Deref(z, v)),
		})
		return true
	})
	defer func() {
		for i := range pairs {
			types.ReleaseValue(z, &pairs[i].key)
			types.ReleaseValue(z, &pairs[i].val)
		}
	}()

	var sortErr error
	slices.SortStableFunc(pairs, func(x, y kvPair) int {
		if sortErr != nil {
			return 0
		}
		c, err := cmpPair(x, y)
		if err != nil {
			sortErr = err
			return 0
		}
		return c
	})
	if sortErr != nil {
		return sortErr
	}

	nextArr, err := types.NewArray(z)
	if err != nil {
		return err
	}
	for _, p := range pairs {
		if err := types.ArraySet(z, &nextArr, p.key, p.val, nil); err != nil {
			types.ReleaseArray(z, &nextArr)
			return err
		}
	}
	types.ReleaseArray(z, &b.arr)
	b.arr = nextArr
	return nil
}

func valueToString(z *zone.Zone, v types.Value) string {
	v = types.Deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull, types.TypeFalse:
		return ""
	case types.TypeTrue:
		return "1"
	case types.TypeLong:
		return strconv.FormatInt(v.ToLong(), 10)
	case types.TypeDouble:
		f := v.ToDouble()
		if math.IsNaN(f) {
			return "NAN"
		}
		if math.IsInf(f, 1) {
			return "INF"
		}
		if math.IsInf(f, -1) {
			return "-INF"
		}
		return strconv.FormatFloat(f, 'G', 14, 64)
	case types.TypeString:
		return types.StringData(z, v)
	case types.TypeArray:
		return "Array"
	default:
		return ""
	}
}

func valueToFloat(z *zone.Zone, v types.Value) float64 {
	v = types.Deref(z, v)
	switch v.Type {
	case types.TypeLong, types.TypeDouble, types.TypeTrue, types.TypeFalse, types.TypeNull, types.TypeUndef:
		return v.ToDouble()
	case types.TypeString:
		s := strings.TrimSpace(types.StringData(z, v))
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

func compareValuesByFlags(z *zone.Zone, a, b types.Value, flags int64) int {
	a, b = types.Deref(z, a), types.Deref(z, b)
	foldCase := flags&SORT_FLAG_CASE != 0
	base := flags &^ SORT_FLAG_CASE

	switch base {
	case SORT_NUMERIC:
		return cmp.Compare(valueToFloat(z, a), valueToFloat(z, b))
	case SORT_STRING:
		sa, sb := valueToString(z, a), valueToString(z, b)
		if foldCase {
			return strings.Compare(strings.ToLower(sa), strings.ToLower(sb))
		}
		return strings.Compare(sa, sb)
	case SORT_NATURAL:
		sa, sb := valueToString(z, a), valueToString(z, b)
		return strnatcmpPHP(sa, sb, foldCase)
	default:
		// SORT_REGULAR
		if a.Type == types.TypeString && b.Type == types.TypeString {
			return types.CompareStringsPHP(z, a, b)
		}
		if a.Type <= types.TypeDouble && b.Type <= types.TypeDouble {
			return types.CompareScalaires(a, b)
		}
		return strings.Compare(valueToString(z, a), valueToString(z, b))
	}
}

// strnatcmpPHP implémente l'algorithme exact strnatcmp de Martin Pool utilisé par PHP 8.3.
func strnatcmpPHP(a, b string, foldCase bool) int {
	ai, bi := 0, 0
	for ai < len(a) && isASCIISpace(a[ai]) {
		ai++
	}
	for bi < len(b) && isASCIISpace(b[bi]) {
		bi++
	}
	for {
		var ca, cb byte
		if ai < len(a) {
			ca = a[ai]
		}
		if bi < len(b) {
			cb = b[bi]
		}
		if isASCIIDigit(ca) && isASCIIDigit(cb) {
			var res int
			if ca == '0' || cb == '0' {
				res, ai, bi = natCompareLeft(a, b, ai, bi)
			} else {
				res, ai, bi = natCompareRight(a, b, ai, bi)
			}
			if res != 0 {
				return res
			}
			continue
		}
		if ai >= len(a) && bi >= len(b) {
			return 0
		}
		if foldCase {
			ca = asciiUpper(ca)
			cb = asciiUpper(cb)
		}
		if ca < cb {
			return -1
		}
		if ca > cb {
			return 1
		}
		ai++
		bi++
	}
}

func natCompareLeft(a, b string, ai, bi int) (int, int, int) {
	for {
		var ca, cb byte
		if ai < len(a) {
			ca = a[ai]
		}
		if bi < len(b) {
			cb = b[bi]
		}
		da, db := isASCIIDigit(ca), isASCIIDigit(cb)
		if !da && !db {
			return 0, ai, bi
		}
		if !da {
			return -1, ai, bi
		}
		if !db {
			return 1, ai, bi
		}
		if ca < cb {
			return -1, ai, bi
		}
		if ca > cb {
			return 1, ai, bi
		}
		ai++
		bi++
	}
}

func natCompareRight(a, b string, ai, bi int) (int, int, int) {
	bias := 0
	for {
		var ca, cb byte
		if ai < len(a) {
			ca = a[ai]
		}
		if bi < len(b) {
			cb = b[bi]
		}
		da, db := isASCIIDigit(ca), isASCIIDigit(cb)
		if !da && !db {
			return bias, ai, bi
		}
		if !da {
			return -1, ai, bi
		}
		if !db {
			return 1, ai, bi
		}
		if ca < cb {
			if bias == 0 {
				bias = -1
			}
		} else if ca > cb {
			if bias == 0 {
				bias = 1
			}
		}
		ai++
		bi++
	}
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func asciiUpper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}
