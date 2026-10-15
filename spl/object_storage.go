package spl

import (
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type storageEntry struct {
	key  objKey
	obj  types.Value
	info types.Value
}

// SplObjectStorage implémente la classe SplObjectStorage de PHP 8.3 indexée par identité d'objet.
type SplObjectStorage struct {
	z       *zone.Zone
	obj     types.Value
	entries []storageEntry
	byKey   map[objKey]int
	cursor  int
}

// NewSplObjectStorage crée une instance de SplObjectStorage enregistrée dans la Zone.
func NewSplObjectStorage(z *zone.Zone) (types.Value, *SplObjectStorage, error) {
	obj, err := types.NewObject(z, ClassIDSplObjectStorage, 0, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	os := &SplObjectStorage{
		z:      z,
		obj:    obj,
		byKey:  make(map[objKey]int),
		cursor: 0,
	}
	reg := getOrCreateRegistry(z)
	reg.objStorages[objKey{slot: obj.DataSlot, gen: obj.Extra}] = os
	return obj, os, nil
}

// ObjectStorageFromObject retrouve l'instance SplObjectStorage depuis une Value objet.
func ObjectStorageFromObject(z *zone.Zone, v types.Value) (*SplObjectStorage, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, SPLException{Class: "TypeError", Message: "Expected SplObjectStorage object"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, SPLException{Class: "RuntimeException", Message: "Uninitialized SPL registry"}
	}
	os, ok := reg.objStorages[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, SPLException{Class: "RuntimeException", Message: "Object is not a registered SplObjectStorage"}
	}
	return os, nil
}

func (s *SplObjectStorage) releaseAll(z *zone.Zone) {
	for i := range s.entries {
		if !s.entries[i].obj.IsUndef() {
			types.ReleaseValue(z, &s.entries[i].obj)
		}
		if !s.entries[i].info.IsUndef() {
			types.ReleaseValue(z, &s.entries[i].info)
		}
	}
	s.entries = nil
	clear(s.byKey)
}

// Object retourne la Value objet associée.
func (s *SplObjectStorage) Object() types.Value {
	return s.obj
}

func validateObjectKey(z *zone.Zone, obj types.Value) (types.Value, objKey, error) {
	obj = types.Deref(z, obj)
	if obj.Type != types.TypeObject {
		return types.Value{}, objKey{}, SPLException{
			Class:   "TypeError",
			Message: "SplObjectStorage: Argument #1 ($object) must be of type object",
		}
	}
	types.ObjectInfo(z, obj)
	return obj, objKey{slot: obj.DataSlot, gen: obj.Extra}, nil
}

// Attach ajoute un objet et ses métadonnées associées (ou met à jour info s'il est déjà présent).
func (s *SplObjectStorage) Attach(z *zone.Zone, obj, info types.Value) error {
	obj, k, err := validateObjectKey(z, obj)
	if err != nil {
		return err
	}
	info = types.Deref(z, info)
	if info.IsUndef() {
		info = types.NewNull()
	}
	retainedInfo := types.RetainValue(z, info)
	if idx, exists := s.byKey[k]; exists {
		types.ReleaseValue(z, &s.entries[idx].info)
		s.entries[idx].info = retainedInfo
		return nil
	}
	retainedObj := types.RetainObject(z, obj)
	s.byKey[k] = len(s.entries)
	s.entries = append(s.entries, storageEntry{
		key:  k,
		obj:  retainedObj,
		info: retainedInfo,
	})
	return nil
}

// Detach retire un objet du stockage s'il est présent.
func (s *SplObjectStorage) Detach(z *zone.Zone, obj types.Value) error {
	_, k, err := validateObjectKey(z, obj)
	if err != nil {
		return err
	}
	idx, exists := s.byKey[k]
	if !exists {
		return nil
	}
	delete(s.byKey, k)
	types.ReleaseValue(z, &s.entries[idx].obj)
	types.ReleaseValue(z, &s.entries[idx].info)
	copy(s.entries[idx:], s.entries[idx+1:])
	s.entries[len(s.entries)-1] = storageEntry{}
	s.entries = s.entries[:len(s.entries)-1]
	for i := idx; i < len(s.entries); i++ {
		s.byKey[s.entries[i].key] = i
	}
	return nil
}

// Contains indique si l'objet appartient au stockage.
func (s *SplObjectStorage) Contains(z *zone.Zone, obj types.Value) (bool, error) {
	_, k, err := validateObjectKey(z, obj)
	if err != nil {
		return false, err
	}
	_, ok := s.byKey[k]
	return ok, nil
}

// AddAll ajoute tous les éléments d'un autre SplObjectStorage et retourne le nouveau nombre d'éléments.
func (s *SplObjectStorage) AddAll(z *zone.Zone, other *SplObjectStorage) int {
	if other == nil {
		return len(s.entries)
	}
	if other == s {
		return len(s.entries)
	}
	for _, e := range other.entries {
		_ = s.Attach(z, e.obj, e.info)
	}
	return len(s.entries)
}

// RemoveAll retire tous les éléments présents dans other et retourne le nouveau nombre d'éléments.
func (s *SplObjectStorage) RemoveAll(z *zone.Zone, other *SplObjectStorage) int {
	if other == nil {
		return len(s.entries)
	}
	if other == s {
		s.releaseAll(z)
		return 0
	}
	toRemove := make([]types.Value, 0, len(other.entries))
	for _, e := range other.entries {
		if _, ok := s.byKey[e.key]; ok {
			toRemove = append(toRemove, e.obj)
		}
	}
	for _, obj := range toRemove {
		_ = s.Detach(z, obj)
	}
	return len(s.entries)
}

// RemoveAllExcept retire tous les éléments absents de other et retourne le nouveau nombre d'éléments.
func (s *SplObjectStorage) RemoveAllExcept(z *zone.Zone, other *SplObjectStorage) int {
	if other == nil {
		return len(s.entries)
	}
	if other == s {
		return len(s.entries)
	}
	toRemove := make([]types.Value, 0, len(s.entries))
	for _, e := range s.entries {
		if _, ok := other.byKey[e.key]; !ok {
			toRemove = append(toRemove, e.obj)
		}
	}
	for _, obj := range toRemove {
		_ = s.Detach(z, obj)
	}
	return len(s.entries)
}

// Count retourne le nombre d'objets stockés.
func (s *SplObjectStorage) Count() int {
	return len(s.entries)
}

// GetInfo retourne l'information associée à l'élément courant de l'itérateur, ou null si invalide.
func (s *SplObjectStorage) GetInfo() types.Value {
	if !s.Valid() {
		return types.NewNull()
	}
	return s.entries[s.cursor].info
}

// SetInfo modifie l'information associée à l'élément courant de l'itérateur.
func (s *SplObjectStorage) SetInfo(z *zone.Zone, info types.Value) {
	if !s.Valid() {
		return
	}
	info = types.Deref(z, info)
	if info.IsUndef() {
		info = types.NewNull()
	}
	retained := types.RetainValue(z, info)
	types.ReleaseValue(z, &s.entries[s.cursor].info)
	s.entries[s.cursor].info = retained
}

// OffsetExists est un alias de Contains.
func (s *SplObjectStorage) OffsetExists(z *zone.Zone, obj types.Value) (bool, error) {
	return s.Contains(z, obj)
}

// OffsetGet retourne l'information associée à l'objet, ou lève UnexpectedValueException s'il est absent.
func (s *SplObjectStorage) OffsetGet(z *zone.Zone, obj types.Value) (types.Value, error) {
	_, k, err := validateObjectKey(z, obj)
	if err != nil {
		return types.NewNull(), err
	}
	idx, ok := s.byKey[k]
	if !ok {
		return types.NewNull(), SPLException{
			Class:   "UnexpectedValueException",
			Message: "Object not found",
		}
	}
	return s.entries[idx].info, nil
}

// OffsetSet est un alias de Attach.
func (s *SplObjectStorage) OffsetSet(z *zone.Zone, obj, info types.Value) error {
	return s.Attach(z, obj, info)
}

// OffsetUnset est un alias de Detach.
func (s *SplObjectStorage) OffsetUnset(z *zone.Zone, obj types.Value) error {
	return s.Detach(z, obj)
}

// Rewind remet le curseur d'itération au début.
func (s *SplObjectStorage) Rewind() {
	s.cursor = 0
}

// Valid indique si le curseur d'itération est sur un élément valide.
func (s *SplObjectStorage) Valid() bool {
	return s.cursor >= 0 && s.cursor < len(s.entries)
}

// Current retourne l'objet courant de l'itérateur, ou null si hors bornes.
func (s *SplObjectStorage) Current() types.Value {
	if !s.Valid() {
		return types.NewNull()
	}
	return s.entries[s.cursor].obj
}

// Key retourne l'indice courant de l'itérateur.
func (s *SplObjectStorage) Key() int {
	return s.cursor
}

// Next avance le curseur d'itération.
func (s *SplObjectStorage) Next() {
	s.cursor++
}

// GetHash retourne l'identifiant de hachage de l'objet (identique à spl_object_hash par défaut).
func (s *SplObjectStorage) GetHash(z *zone.Zone, obj types.Value) (types.Value, error) {
	return SplObjectHash(z, obj)
}
