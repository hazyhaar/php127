package spl

import (
	"slices"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// SplDoublyLinkedList implémente SplDoublyLinkedList, SplStack et SplQueue de PHP 8.3.
type SplDoublyLinkedList struct {
	z       *zone.Zone
	obj     types.Value
	classID uint32
	items   []types.Value
	mode    int64
	cursor  int
}

// NewSplDoublyLinkedList crée une instance de SplDoublyLinkedList enregistrée dans la Zone.
func NewSplDoublyLinkedList(z *zone.Zone) (types.Value, *SplDoublyLinkedList, error) {
	return newDLLWithClass(z, ClassIDSplDoublyLinkedList, IT_MODE_FIFO|IT_MODE_KEEP)
}

// NewSplStack crée une instance de SplStack (mode IT_MODE_LIFO | IT_MODE_KEEP | IT_MODE_FIX = 6).
func NewSplStack(z *zone.Zone) (types.Value, *SplDoublyLinkedList, error) {
	return newDLLWithClass(z, ClassIDSplStack, IT_MODE_LIFO|IT_MODE_KEEP|IT_MODE_FIX)
}

// NewSplQueue crée une instance de SplQueue (mode IT_MODE_FIFO | IT_MODE_KEEP | IT_MODE_FIX = 4).
func NewSplQueue(z *zone.Zone) (types.Value, *SplDoublyLinkedList, error) {
	return newDLLWithClass(z, ClassIDSplQueue, IT_MODE_FIFO|IT_MODE_KEEP|IT_MODE_FIX)
}

func newDLLWithClass(z *zone.Zone, classID uint32, mode int64) (types.Value, *SplDoublyLinkedList, error) {
	obj, err := types.NewObject(z, classID, 0, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	dll := &SplDoublyLinkedList{
		z:       z,
		obj:     obj,
		classID: classID,
		mode:    mode,
		cursor:  -1,
	}
	reg := getOrCreateRegistry(z)
	reg.dlls[objKey{slot: obj.DataSlot, gen: obj.Extra}] = dll
	return obj, dll, nil
}

// DLLFromObject retrouve l'instance SplDoublyLinkedList/SplStack/SplQueue depuis une Value objet.
func DLLFromObject(z *zone.Zone, v types.Value) (*SplDoublyLinkedList, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, SPLException{Class: "TypeError", Message: "Expected SplDoublyLinkedList object"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, SPLException{Class: "RuntimeException", Message: "Uninitialized SPL registry"}
	}
	dll, ok := reg.dlls[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, SPLException{Class: "RuntimeException", Message: "Object is not a registered SplDoublyLinkedList"}
	}
	return dll, nil
}

func (d *SplDoublyLinkedList) releaseAll(z *zone.Zone) {
	for i := range d.items {
		if !d.items[i].IsUndef() {
			types.ReleaseValue(z, &d.items[i])
		}
	}
	d.items = nil
	d.cursor = -1
}

// Object retourne la Value objet associée.
func (d *SplDoublyLinkedList) Object() types.Value {
	return d.obj
}

// Push ajoute un élément à la fin (sommet) de la liste.
func (d *SplDoublyLinkedList) Push(z *zone.Zone, val types.Value) {
	d.items = append(d.items, types.RetainValue(z, types.Deref(z, val)))
}

// Pop retire et retourne le dernier élément (sommet) de la liste.
func (d *SplDoublyLinkedList) Pop(z *zone.Zone) (types.Value, error) {
	if len(d.items) == 0 {
		return types.NewNull(), SPLException{
			Class:   "RuntimeException",
			Message: "Can't pop from an empty datastructure",
		}
	}
	last := len(d.items) - 1
	v := d.items[last]
	d.items[last] = types.Value{}
	d.items = d.items[:last]
	if d.cursor >= len(d.items) {
		d.cursor = len(d.items) - 1
	}
	return v, nil
}

// Shift retire et retourne le premier élément (bas) de la liste.
func (d *SplDoublyLinkedList) Shift(z *zone.Zone) (types.Value, error) {
	if len(d.items) == 0 {
		return types.NewNull(), SPLException{
			Class:   "RuntimeException",
			Message: "Can't shift from an empty datastructure",
		}
	}
	v := d.items[0]
	copy(d.items[0:], d.items[1:])
	d.items[len(d.items)-1] = types.Value{}
	d.items = d.items[:len(d.items)-1]
	if d.cursor >= len(d.items) {
		d.cursor = len(d.items) - 1
	}
	return v, nil
}

// Unshift insère un élément au début (bas) de la liste.
func (d *SplDoublyLinkedList) Unshift(z *zone.Zone, val types.Value) {
	d.items = slices.Insert(d.items, 0, types.RetainValue(z, types.Deref(z, val)))
	if d.cursor >= 0 {
		d.cursor++
	}
}

// Enqueue ajoute un élément à la file (alias de Push pour SplQueue).
func (d *SplDoublyLinkedList) Enqueue(z *zone.Zone, val types.Value) {
	d.Push(z, val)
}

// Dequeue retire et retourne le premier élément de la file (alias de Shift pour SplQueue).
func (d *SplDoublyLinkedList) Dequeue(z *zone.Zone) (types.Value, error) {
	return d.Shift(z)
}

// Top retourne le dernier élément (sommet) sans le retirer (valeur empruntée).
func (d *SplDoublyLinkedList) Top() (types.Value, error) {
	if len(d.items) == 0 {
		return types.NewNull(), SPLException{
			Class:   "RuntimeException",
			Message: "Can't peek at an empty datastructure",
		}
	}
	return d.items[len(d.items)-1], nil
}

// Bottom retourne le premier élément (bas) sans le retirer (valeur empruntée).
func (d *SplDoublyLinkedList) Bottom() (types.Value, error) {
	if len(d.items) == 0 {
		return types.NewNull(), SPLException{
			Class:   "RuntimeException",
			Message: "Can't peek at an empty datastructure",
		}
	}
	return d.items[0], nil
}

// Count retourne le nombre d'éléments de la liste.
func (d *SplDoublyLinkedList) Count() int {
	return len(d.items)
}

// IsEmpty indique si la liste est vide.
func (d *SplDoublyLinkedList) IsEmpty() bool {
	return len(d.items) == 0
}

// SetIteratorMode modifie le mode d'itération (en respectant le gel LIFO/FIFO de SplStack et SplQueue).
func (d *SplDoublyLinkedList) SetIteratorMode(mode int64) (int64, error) {
	if d.mode&IT_MODE_FIX != 0 {
		if (d.mode & IT_MODE_LIFO) != (mode & IT_MODE_LIFO) {
			return d.mode, SPLException{
				Class:   "RuntimeException",
				Message: "Iterators' LIFO/FIFO modes for SplStack/SplQueue objects are frozen",
			}
		}
		d.mode = (mode & (IT_MODE_DELETE | IT_MODE_LIFO)) | IT_MODE_FIX
		return d.mode, nil
	}
	d.mode = mode & (IT_MODE_DELETE | IT_MODE_LIFO)
	return d.mode, nil
}

// GetIteratorMode retourne le mode d'itération courant.
func (d *SplDoublyLinkedList) GetIteratorMode() int64 {
	return d.mode
}

func (d *SplDoublyLinkedList) isLIFO() bool {
	return d.mode&IT_MODE_LIFO != 0
}

func (d *SplDoublyLinkedList) resolveIndex(index int) (int, bool) {
	n := len(d.items)
	if index < 0 || index >= n {
		return -1, false
	}
	if d.isLIFO() {
		return n - 1 - index, true
	}
	return index, true
}

// OffsetExists vérifie si l'indice existe dans la liste.
func (d *SplDoublyLinkedList) OffsetExists(index int) bool {
	_, ok := d.resolveIndex(index)
	return ok
}

// OffsetGet retourne la valeur à l'indice donné (en tenant compte du mode FIFO/LIFO).
func (d *SplDoublyLinkedList) OffsetGet(index int) (types.Value, error) {
	pos, ok := d.resolveIndex(index)
	if !ok {
		return types.NewNull(), SPLException{
			Class:   "OutOfRangeException",
			Message: "SplDoublyLinkedList::offsetGet(): Argument #1 ($index) is out of range",
		}
	}
	return d.items[pos], nil
}

// OffsetSet remplace la valeur à l'indice donné, ou ajoute en fin si index == nil.
func (d *SplDoublyLinkedList) OffsetSet(z *zone.Zone, index *int, val types.Value) error {
	if index == nil {
		d.Push(z, val)
		return nil
	}
	pos, ok := d.resolveIndex(*index)
	if !ok {
		return SPLException{
			Class:   "OutOfRangeException",
			Message: "SplDoublyLinkedList::offsetSet(): Argument #1 ($index) is out of range",
		}
	}
	retained := types.RetainValue(z, types.Deref(z, val))
	types.ReleaseValue(z, &d.items[pos])
	d.items[pos] = retained
	return nil
}

// OffsetSetValue gère offsetSet($index, $value) avec une Value PHP pour $index (null => push).
func (d *SplDoublyLinkedList) OffsetSetValue(z *zone.Zone, index, val types.Value) error {
	index = types.Deref(z, index)
	if index.IsNull() {
		return d.OffsetSet(z, nil, val)
	}
	idx := int(index.ToLong())
	return d.OffsetSet(z, &idx, val)
}

// OffsetUnset supprime l'élément à l'indice donné.
func (d *SplDoublyLinkedList) OffsetUnset(z *zone.Zone, index int) error {
	pos, ok := d.resolveIndex(index)
	if !ok {
		return SPLException{
			Class:   "OutOfRangeException",
			Message: "SplDoublyLinkedList::offsetUnset(): Argument #1 ($index) is out of range",
		}
	}
	types.ReleaseValue(z, &d.items[pos])
	copy(d.items[pos:], d.items[pos+1:])
	d.items[len(d.items)-1] = types.Value{}
	d.items = d.items[:len(d.items)-1]
	if d.cursor >= len(d.items) {
		d.cursor = len(d.items) - 1
	}
	return nil
}

// Add insère une nouvelle valeur avant l'élément situé à index (ou en fin si index == Count()).
func (d *SplDoublyLinkedList) Add(z *zone.Zone, index int, val types.Value) error {
	n := len(d.items)
	if index < 0 || index > n {
		return SPLException{
			Class:   "OutOfRangeException",
			Message: "SplDoublyLinkedList::add(): Argument #1 ($index) is out of range",
		}
	}
	if index == n {
		d.Push(z, val)
		return nil
	}
	pos := index
	if d.isLIFO() {
		pos = n - 1 - index
	}
	d.items = slices.Insert(d.items, pos, types.RetainValue(z, types.Deref(z, val)))
	return nil
}

// Rewind repositionne l'itérateur au début selon le mode FIFO ou LIFO.
func (d *SplDoublyLinkedList) Rewind() {
	if len(d.items) == 0 {
		d.cursor = -1
		return
	}
	if d.isLIFO() {
		d.cursor = len(d.items) - 1
	} else {
		d.cursor = 0
	}
}

// Valid indique si la position courante de l'itérateur est valide.
func (d *SplDoublyLinkedList) Valid() bool {
	return d.cursor >= 0 && d.cursor < len(d.items)
}

// Current retourne l'élément courant de l'itérateur, ou null si hors bornes.
func (d *SplDoublyLinkedList) Current() types.Value {
	if !d.Valid() {
		return types.NewNull()
	}
	return d.items[d.cursor]
}

// Key retourne la clé entière courante de l'itérateur.
func (d *SplDoublyLinkedList) Key() int {
	if !d.Valid() {
		return 0
	}
	return d.cursor
}

// Next avance l'itérateur à l'élément suivant (et supprime l'élément courant si IT_MODE_DELETE est actif).
func (d *SplDoublyLinkedList) Next(z *zone.Zone) {
	if !d.Valid() {
		return
	}
	if d.mode&IT_MODE_DELETE != 0 {
		pos := d.cursor
		types.ReleaseValue(z, &d.items[pos])
		copy(d.items[pos:], d.items[pos+1:])
		d.items[len(d.items)-1] = types.Value{}
		d.items = d.items[:len(d.items)-1]
		if d.isLIFO() {
			d.cursor--
		} else if d.cursor >= len(d.items) {
			d.cursor = -1
		}
		return
	}
	if d.isLIFO() {
		d.cursor--
	} else {
		d.cursor++
	}
}

// Prev recule l'itérateur à l'élément précédent.
func (d *SplDoublyLinkedList) Prev(z *zone.Zone) {
	if !d.Valid() {
		return
	}
	if d.mode&IT_MODE_DELETE != 0 {
		pos := d.cursor
		types.ReleaseValue(z, &d.items[pos])
		copy(d.items[pos:], d.items[pos+1:])
		d.items[len(d.items)-1] = types.Value{}
		d.items = d.items[:len(d.items)-1]
		if !d.isLIFO() {
			d.cursor--
		} else if d.cursor >= len(d.items) {
			d.cursor = -1
		}
		return
	}
	if d.isLIFO() {
		d.cursor++
	} else {
		d.cursor--
	}
}
