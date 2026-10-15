package spl

import (
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// SplFixedArray implémente la classe SplFixedArray de PHP 8.3.
type SplFixedArray struct {
	z      *zone.Zone
	obj    types.Value
	items  []types.Value
	cursor int
}

// NewSplFixedArray crée une instance de SplFixedArray de taille size.
func NewSplFixedArray(z *zone.Zone, size int) (types.Value, *SplFixedArray, error) {
	if size < 0 {
		return types.Value{}, nil, SPLException{
			Class:   "ValueError",
			Message: "SplFixedArray::__construct(): Argument #1 ($size) must be greater than or equal to 0",
		}
	}
	obj, err := types.NewObject(z, ClassIDSplFixedArray, 0, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	items := make([]types.Value, size)
	for i := range items {
		items[i] = types.NewNull()
	}
	fa := &SplFixedArray{
		z:      z,
		obj:    obj,
		items:  items,
		cursor: 0,
	}
	reg := getOrCreateRegistry(z)
	reg.fixedArrays[objKey{slot: obj.DataSlot, gen: obj.Extra}] = fa
	return obj, fa, nil
}

// FixedArrayFromObject retrouve l'instance SplFixedArray depuis une Value objet.
func FixedArrayFromObject(z *zone.Zone, v types.Value) (*SplFixedArray, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, SPLException{Class: "TypeError", Message: "Expected SplFixedArray object"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, SPLException{Class: "RuntimeException", Message: "Uninitialized SPL registry"}
	}
	fa, ok := reg.fixedArrays[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, SPLException{Class: "RuntimeException", Message: "Object is not a registered SplFixedArray"}
	}
	return fa, nil
}

func (f *SplFixedArray) releaseAll(z *zone.Zone) {
	for i := range f.items {
		if !f.items[i].IsUndef() {
			types.ReleaseValue(z, &f.items[i])
		}
	}
	f.items = nil
}

// Object retourne la Value objet associée.
func (f *SplFixedArray) Object() types.Value {
	return f.obj
}

// Count retourne la taille du tableau fixe.
func (f *SplFixedArray) Count() int {
	return len(f.items)
}

// GetSize retourne la taille du tableau fixe.
func (f *SplFixedArray) GetSize() int {
	return len(f.items)
}

// SetSize redimensionne le tableau fixe.
func (f *SplFixedArray) SetSize(z *zone.Zone, size int) (bool, error) {
	if size < 0 {
		return false, SPLException{
			Class:   "ValueError",
			Message: "SplFixedArray::setSize(): Argument #1 ($size) must be greater than or equal to 0",
		}
	}
	oldLen := len(f.items)
	if size < oldLen {
		for i := size; i < oldLen; i++ {
			if !f.items[i].IsUndef() {
				types.ReleaseValue(z, &f.items[i])
			}
		}
		f.items = f.items[:size]
		return true, nil
	}
	if size > oldLen {
		grown := make([]types.Value, size)
		copy(grown, f.items)
		for i := oldLen; i < size; i++ {
			grown[i] = types.NewNull()
		}
		f.items = grown
	}
	return true, nil
}

// ToArray convertit le SplFixedArray en un tableau PHP alloué dans la Zone.
func (f *SplFixedArray) ToArray(z *zone.Zone) (types.Value, error) {
	arr, err := types.NewArray(z)
	if err != nil {
		return types.NewNull(), err
	}
	for i, item := range f.items {
		if err := types.ArraySet(z, &arr, types.NewLong(int64(i)), item, nil); err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
	}
	return arr, nil
}

// FixedArrayFromArray implémente SplFixedArray::fromArray($array, $preserveKeys = true).
func FixedArrayFromArray(z *zone.Zone, arr types.Value, preserveKeys bool) (types.Value, *SplFixedArray, error) {
	arr = types.Deref(z, arr)
	if arr.Type != types.TypeArray {
		return types.Value{}, nil, SPLException{
			Class:   "TypeError",
			Message: "SplFixedArray::fromArray(): Argument #1 ($array) must be of type array",
		}
	}
	if !preserveKeys {
		n := int(types.ArrayLen(z, arr))
		obj, fa, err := NewSplFixedArray(z, n)
		if err != nil {
			return types.Value{}, nil, err
		}
		idx := 0
		types.ArrayForEach(z, arr, func(_, val types.Value) bool {
			fa.items[idx] = types.RetainValue(z, types.Deref(z, val))
			idx++
			return true
		})
		return obj, fa, nil
	}

	maxKey := int64(-1)
	var badKey bool
	types.ArrayForEach(z, arr, func(k, _ types.Value) bool {
		if k.Type != types.TypeLong || k.ToLong() < 0 {
			badKey = true
			return false
		}
		if k.ToLong() > maxKey {
			maxKey = k.ToLong()
		}
		return true
	})
	if badKey {
		return types.Value{}, nil, SPLException{
			Class:   "InvalidArgumentException",
			Message: "array must contain only positive integer keys",
		}
	}
	size := 0
	if maxKey >= 0 {
		size = int(maxKey + 1)
	}
	obj, fa, err := NewSplFixedArray(z, size)
	if err != nil {
		return types.Value{}, nil, err
	}
	types.ArrayForEach(z, arr, func(k, val types.Value) bool {
		idx := int(k.ToLong())
		fa.items[idx] = types.RetainValue(z, types.Deref(z, val))
		return true
	})
	return obj, fa, nil
}

// OffsetExists indique si l'indice est dans les bornes et que la valeur n'est pas null.
func (f *SplFixedArray) OffsetExists(index int) bool {
	if index < 0 || index >= len(f.items) {
		return false
	}
	return !f.items[index].IsNull() && !f.items[index].IsUndef()
}

// OffsetGet retourne la valeur à l'indice donné.
func (f *SplFixedArray) OffsetGet(index int) (types.Value, error) {
	if index < 0 || index >= len(f.items) {
		return types.NewNull(), SPLException{
			Class:   "RuntimeException",
			Message: "Index invalid or out of range",
		}
	}
	return f.items[index], nil
}

// OffsetSet écrit la valeur à l'indice donné.
func (f *SplFixedArray) OffsetSet(z *zone.Zone, index int, val types.Value) error {
	if index < 0 || index >= len(f.items) {
		return SPLException{
			Class:   "RuntimeException",
			Message: "Index invalid or out of range",
		}
	}
	retained := types.RetainValue(z, types.Deref(z, val))
	if !f.items[index].IsUndef() {
		types.ReleaseValue(z, &f.items[index])
	}
	f.items[index] = retained
	return nil
}

// OffsetUnset remet à null l'élément à l'indice donné.
func (f *SplFixedArray) OffsetUnset(z *zone.Zone, index int) error {
	if index < 0 || index >= len(f.items) {
		return SPLException{
			Class:   "RuntimeException",
			Message: "Index invalid or out of range",
		}
	}
	if !f.items[index].IsUndef() {
		types.ReleaseValue(z, &f.items[index])
	}
	f.items[index] = types.NewNull()
	return nil
}

// Rewind remet le curseur d'itération à 0.
func (f *SplFixedArray) Rewind() {
	f.cursor = 0
}

// Valid indique si le curseur d'itération est dans les bornes du tableau fixe.
func (f *SplFixedArray) Valid() bool {
	return f.cursor >= 0 && f.cursor < len(f.items)
}

// Current retourne l'élément courant, ou null si hors bornes.
func (f *SplFixedArray) Current() types.Value {
	if !f.Valid() {
		return types.NewNull()
	}
	return f.items[f.cursor]
}

// Key retourne l'indice courant du curseur.
func (f *SplFixedArray) Key() int {
	return f.cursor
}

// Next avance le curseur d'itération.
func (f *SplFixedArray) Next() {
	f.cursor++
}
