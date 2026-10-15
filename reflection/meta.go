// Package reflection implémente l'API de réflexion PHP 8.3 (Reflector,
// ReflectionClass, ReflectionObject, ReflectionMethod, ReflectionFunction,
// ReflectionProperty, ReflectionParameter, ReflectionClassConstant,
// ReflectionEnum, ReflectionEnumUnitCase, ReflectionEnumBackedCase,
// ReflectionNamedType, ReflectionUnionType, ReflectionIntersectionType,
// ReflectionAttribute et ReflectionException) avec respect strict de
// l'invariant 0-Ptr dans la Zone.
package reflection

import (
	"strings"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Constantes officielles PHP 8.3 de visibilité et de modificateurs de réflexion.
const (
	// ReflectionClass
	ClassIsImplicitAbstract int64 = 16
	ClassIsExplicitAbstract int64 = 64
	ClassIsFinal            int64 = 32
	ClassIsReadOnly         int64 = 65536

	// ReflectionMethod (inclut l'alias contractuel MethodIsStaticLegacy = 1 et la valeur PHP 8.3 = 16)
	MethodIsPublic       int64 = 1
	MethodIsProtected    int64 = 2
	MethodIsPrivate      int64 = 4
	MethodIsStatic       int64 = 16
	MethodIsStaticLegacy int64 = 1
	MethodIsFinal        int64 = 32
	MethodIsAbstract     int64 = 64

	// ReflectionProperty
	PropertyIsPublic    int64 = 1
	PropertyIsProtected int64 = 2
	PropertyIsPrivate   int64 = 4
	PropertyIsStatic    int64 = 16
	PropertyIsReadOnly  int64 = 128

	// ReflectionClassConstant
	ConstantIsPublic    int64 = 1
	ConstantIsProtected int64 = 2
	ConstantIsPrivate   int64 = 4
	ConstantIsFinal     int64 = 32

	// ReflectionAttribute
	AttributeIsInstanceOf int64 = 2
)

// Identifiants de classes de réflexion enregistrés dans le paquet types.
const (
	ClassIDReflectionException        uint32 = 5501
	ClassIDReflectionClass            uint32 = 5502
	ClassIDReflectionObject           uint32 = 5503
	ClassIDReflectionMethod           uint32 = 5504
	ClassIDReflectionFunction         uint32 = 5505
	ClassIDReflectionProperty         uint32 = 5506
	ClassIDReflectionParameter        uint32 = 5507
	ClassIDReflectionClassConstant    uint32 = 5508
	ClassIDReflectionEnum             uint32 = 5509
	ClassIDReflectionEnumUnitCase     uint32 = 5510
	ClassIDReflectionEnumBackedCase   uint32 = 5511
	ClassIDReflectionNamedType        uint32 = 5512
	ClassIDReflectionUnionType        uint32 = 5513
	ClassIDReflectionIntersectionType uint32 = 5514
	ClassIDReflectionAttribute        uint32 = 5515

	CleanupReflection zone.CleanupKind = 55
)

// Visibility représente la visibilité d'un membre PHP (public, protected, private).
type Visibility uint8

const (
	VisPublic Visibility = iota
	VisProtected
	VisPrivate
)

func (v Visibility) Modifiers() int64 {
	switch v {
	case VisProtected:
		return 2
	case VisPrivate:
		return 4
	default:
		return 1
	}
}

// TypeKind distingue un type nommé, une union ou une intersection.
type TypeKind uint8

const (
	TypeKindNamed TypeKind = iota
	TypeKindUnion
	TypeKindIntersection
)

// TypeMeta décrit un type déclaré PHP 8.3 (nommé, union `A|B`, intersection `A&B` ou DNF).
type TypeMeta struct {
	Kind       TypeKind
	Name       string
	AllowsNull bool
	IsBuiltin  bool
	Types      []*TypeMeta
}

// ParseTypeString construit un *TypeMeta à partir d'une signature textuelle PHP 8.3
// (ex: "?int", "int|string|null", "Countable&Iterator", "(A&B)|null").
func ParseTypeString(sig string) *TypeMeta {
	sig = strings.TrimSpace(sig)
	if sig == "" {
		return nil
	}
	if strings.HasPrefix(sig, "?") && !strings.ContainsAny(sig, "|&") {
		base := strings.TrimSpace(sig[1:])
		return &TypeMeta{
			Kind:       TypeKindNamed,
			Name:       base,
			AllowsNull: true,
			IsBuiltin:  isBuiltinTypeName(base),
		}
	}
	if parts := splitTopLevel(sig, '|'); len(parts) > 1 {
		allowsNull := false
		sub := make([]*TypeMeta, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "(") && strings.HasSuffix(p, ")") {
				p = strings.TrimSpace(p[1 : len(p)-1])
			}
			tm := ParseTypeString(p)
			if tm == nil {
				continue
			}
			if strings.EqualFold(tm.Name, "null") {
				allowsNull = true
			}
			sub = append(sub, tm)
		}
		sortUnionTypesPHP83(sub)
		return &TypeMeta{
			Kind:       TypeKindUnion,
			AllowsNull: allowsNull,
			Types:      sub,
		}
	}
	if parts := splitTopLevel(sig, '&'); len(parts) > 1 {
		sub := make([]*TypeMeta, 0, len(parts))
		for _, p := range parts {
			tm := ParseTypeString(p)
			if tm != nil {
				sub = append(sub, tm)
			}
		}
		return &TypeMeta{
			Kind:       TypeKindIntersection,
			AllowsNull: false,
			Types:      sub,
		}
	}
	lower := strings.ToLower(sig)
	return &TypeMeta{
		Kind:       TypeKindNamed,
		Name:       sig,
		AllowsNull: lower == "null" || lower == "mixed",
		IsBuiltin:  isBuiltinTypeName(sig),
	}
}

func splitTopLevel(s string, sep byte) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func isBuiltinTypeName(name string) bool {
	switch strings.ToLower(name) {
	case "int", "float", "string", "bool", "array", "object", "callable", "iterable", "void", "never", "mixed", "null", "true", "false":
		return true
	default:
		return false
	}
}

func unionTypeRank(t *TypeMeta) int {
	if t == nil || t.Kind != TypeKindNamed || !t.IsBuiltin {
		return 0
	}
	switch strings.ToLower(t.Name) {
	case "object":
		return 1
	case "iterable":
		return 2
	case "array":
		return 3
	case "callable":
		return 4
	case "string":
		return 5
	case "int":
		return 6
	case "float":
		return 7
	case "bool", "false", "true":
		return 8
	case "null":
		return 9
	default:
		return 0
	}
}

func sortUnionTypesPHP83(sub []*TypeMeta) {
	for i := 1; i < len(sub); i++ {
		cur := sub[i]
		rCur := unionTypeRank(cur)
		j := i - 1
		for j >= 0 && unionTypeRank(sub[j]) > rCur {
			sub[j+1] = sub[j]
			j--
		}
		sub[j+1] = cur
	}
}

// String restitue la forme textuelle canonique PHP 8.3 d'un TypeMeta.
func (t *TypeMeta) String() string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case TypeKindUnion:
		parts := make([]string, 0, len(t.Types))
		for _, sub := range t.Types {
			if sub.Kind == TypeKindIntersection {
				parts = append(parts, "("+sub.String()+")")
			} else {
				parts = append(parts, sub.String())
			}
		}
		return strings.Join(parts, "|")
	case TypeKindIntersection:
		parts := make([]string, 0, len(t.Types))
		for _, sub := range t.Types {
			parts = append(parts, sub.String())
		}
		return strings.Join(parts, "&")
	default:
		if t.AllowsNull && !strings.EqualFold(t.Name, "mixed") && !strings.EqualFold(t.Name, "null") {
			return "?" + t.Name
		}
		return t.Name
	}
}

// AttributeArg représente un argument positionnel ou nommé d'un attribut PHP 8 `#[Attr(...)]`.
type AttributeArg struct {
	Name  string
	Value types.Value
}

// AttributeMeta décrit une annotation d'attribut PHP 8.
type AttributeMeta struct {
	Name string
	Args []AttributeArg
}

// ParamMeta décrit un paramètre de fonction ou de méthode PHP.
type ParamMeta struct {
	Name          string
	Position      int
	Type          *TypeMeta
	ByRef         bool
	Variadic      bool
	Promoted      bool
	HasDefault    bool
	Default       types.Value
	DefaultString string
	Attributes    []AttributeMeta
}

// FunctionMeta décrit une fonction libre ou une fermeture PHP.
type FunctionMeta struct {
	Name        string
	IsInternal  bool
	IsClosure   bool
	IsGenerator bool
	ByRef       bool
	ReturnType  *TypeMeta
	Params      []ParamMeta
	Attributes  []AttributeMeta
	Invoker     func(z *zone.Zone, args []types.Value) (types.Value, error)
}

// MethodMeta décrit une méthode de classe, d'interface, de trait ou d'énumération.
type MethodMeta struct {
	Name           string
	DeclaringClass string
	Visibility     Visibility
	IsStatic       bool
	IsAbstract     bool
	IsFinal        bool
	IsInternal     bool
	IsGenerator    bool
	ByRef          bool
	ReturnType     *TypeMeta
	Params         []ParamMeta
	Attributes     []AttributeMeta
	Invoker        func(z *zone.Zone, recv types.Value, args []types.Value) (types.Value, error)
}

// PropertyMeta décrit une propriété déclarée ou dynamique d'une classe PHP.
type PropertyMeta struct {
	Name           string
	DeclaringClass string
	Index          uint32
	Visibility     Visibility
	IsStatic       bool
	IsReadOnly     bool
	IsDefault      bool
	IsPromoted     bool
	Type           *TypeMeta
	HasDefault     bool
	Default        types.Value
	DefaultString  string
	Attributes     []AttributeMeta
}

// ConstantMeta décrit une constante de classe (`const X = ...`).
type ConstantMeta struct {
	Name           string
	DeclaringClass string
	Visibility     Visibility
	IsFinal        bool
	Value          types.Value
	ValueString    string
	HasString      bool
	Attributes     []AttributeMeta
}

// EnumCaseMeta décrit un cas d'énumération PHP 8.1+ (unitaire ou adossé `int`/`string`).
type EnumCaseMeta struct {
	Name          string
	EnumClass     string
	IsBacked      bool
	BackingValue  types.Value
	BackingString string
	HasString     bool
	UnitObject    types.Value
	Attributes    []AttributeMeta
}

// ClassMeta décrit une classe, interface, trait ou énumération PHP 8.3.
type ClassMeta struct {
	Name          string
	ID            uint32
	ParentName    string
	Interfaces    []string
	Traits        []string
	IsInternal    bool
	IsInterface   bool
	IsTrait       bool
	IsEnum        bool
	IsAbstract    bool
	IsFinal       bool
	IsReadOnly    bool
	IsAnonymous   bool
	BackingType   *TypeMeta
	Properties    []PropertyMeta
	Methods       []MethodMeta
	Constants     []ConstantMeta
	EnumCases     []EnumCaseMeta
	Attributes    []AttributeMeta
	StaticProps   map[string]types.Value
	Instantiator  func(z *zone.Zone, class *ClassMeta, withConstructor bool, args []types.Value) (types.Value, error)
	PropGetter    func(z *zone.Zone, class *ClassMeta, prop *PropertyMeta, obj types.Value) (types.Value, bool, error)
	PropSetter    func(z *zone.Zone, class *ClassMeta, prop *PropertyMeta, obj types.Value, val types.Value) error
}

// Provider est l'interface de résolution des métadonnées d'exécution que la VM
// branche sur une Zone de requête.
type Provider interface {
	ResolveClass(name string) (*ClassMeta, bool)
	ResolveClassByID(id uint32) (*ClassMeta, bool)
	ResolveFunction(name string) (*FunctionMeta, bool)
}

// ReflectionException représente l'exception PHP ReflectionException.
type ReflectionException struct {
	Message string
}

func (e ReflectionException) Error() string { return e.Message }

// TypeError représente une erreur de typage d'argument PHP 8.
type TypeError struct {
	Message string
}

func (e TypeError) Error() string { return e.Message }

// ValueError représente une erreur de valeur d'argument PHP 8.
type ValueError struct {
	Message string
}

func (e ValueError) Error() string { return e.Message }

// Error représente l'erreur moteur PHP Error (ex: instanciation de classe abstraite).
type Error struct {
	Message string
}

func (e Error) Error() string { return e.Message }

// Reflector est l'interface implémentée par toutes les entités de réflexion PHP 8.3.
type Reflector interface {
	String() string
}

// Catalog est une implémentation prête à l'emploi de Provider stockant les
// métadonnées de classes et de fonctions d'un programme.
type Catalog struct {
	mu        sync.RWMutex
	byName    map[string]*ClassMeta
	byID      map[uint32]*ClassMeta
	functions map[string]*FunctionMeta
}

// NewCatalog construit un catalogue de métadonnées de réflexion vide.
func NewCatalog() *Catalog {
	return &Catalog{
		byName:    make(map[string]*ClassMeta),
		byID:      make(map[uint32]*ClassMeta),
		functions: make(map[string]*FunctionMeta),
	}
}

// RegisterClass enregistre une classe, interface, trait ou énumération dans le catalogue.
func (c *Catalog) RegisterClass(meta *ClassMeta) {
	if meta == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if meta.StaticProps == nil {
		meta.StaticProps = make(map[string]types.Value)
	}
	for i := range meta.Properties {
		if meta.Properties[i].DeclaringClass == "" {
			meta.Properties[i].DeclaringClass = meta.Name
		}
	}
	for i := range meta.Methods {
		if meta.Methods[i].DeclaringClass == "" {
			meta.Methods[i].DeclaringClass = meta.Name
		}
	}
	for i := range meta.Constants {
		if meta.Constants[i].DeclaringClass == "" {
			meta.Constants[i].DeclaringClass = meta.Name
		}
	}
	for i := range meta.EnumCases {
		if meta.EnumCases[i].EnumClass == "" {
			meta.EnumCases[i].EnumClass = meta.Name
		}
		found := false
		for j := range meta.Constants {
			if meta.Constants[j].Name == meta.EnumCases[i].Name {
				found = true
				break
			}
		}
		if !found {
			meta.Constants = append(meta.Constants, ConstantMeta{
				Name:           meta.EnumCases[i].Name,
				DeclaringClass: meta.Name,
				Visibility:     VisPublic,
				Value:          meta.EnumCases[i].UnitObject,
				Attributes:     meta.EnumCases[i].Attributes,
			})
		}
	}
	c.byName[strings.ToLower(strings.TrimPrefix(meta.Name, `\`))] = meta
	if meta.ID != 0 {
		c.byID[meta.ID] = meta
	}
}

// RegisterFunction enregistre une fonction dans le catalogue.
func (c *Catalog) RegisterFunction(meta *FunctionMeta) {
	if meta == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.functions[strings.ToLower(strings.TrimPrefix(meta.Name, `\`))] = meta
}

func (c *Catalog) ResolveClass(name string) (*ClassMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.byName[strings.ToLower(strings.TrimPrefix(name, `\`))]
	return m, ok
}

func (c *Catalog) ResolveClassByID(id uint32) (*ClassMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.byID[id]
	return m, ok
}

func (c *Catalog) ResolveFunction(name string) (*FunctionMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.functions[strings.ToLower(strings.TrimPrefix(name, `\`))]
	return m, ok
}

func init() {
	EnsureClassNames()
}

// EnsureClassNames enregistre les noms des 15 classes de réflexion dans types si la ProcessZone n'est pas gelée.
func EnsureClassNames() {
	if !types.IsProcessZoneFrozen() {
		types.RegisterObjectClassName(ClassIDReflectionException, "ReflectionException")
		types.RegisterObjectClassName(ClassIDReflectionClass, "ReflectionClass")
		types.RegisterObjectClassName(ClassIDReflectionObject, "ReflectionObject")
		types.RegisterObjectClassName(ClassIDReflectionMethod, "ReflectionMethod")
		types.RegisterObjectClassName(ClassIDReflectionFunction, "ReflectionFunction")
		types.RegisterObjectClassName(ClassIDReflectionProperty, "ReflectionProperty")
		types.RegisterObjectClassName(ClassIDReflectionParameter, "ReflectionParameter")
		types.RegisterObjectClassName(ClassIDReflectionClassConstant, "ReflectionClassConstant")
		types.RegisterObjectClassName(ClassIDReflectionEnum, "ReflectionEnum")
		types.RegisterObjectClassName(ClassIDReflectionEnumUnitCase, "ReflectionEnumUnitCase")
		types.RegisterObjectClassName(ClassIDReflectionEnumBackedCase, "ReflectionEnumBackedCase")
		types.RegisterObjectClassName(ClassIDReflectionNamedType, "ReflectionNamedType")
		types.RegisterObjectClassName(ClassIDReflectionUnionType, "ReflectionUnionType")
		types.RegisterObjectClassName(ClassIDReflectionIntersectionType, "ReflectionIntersectionType")
		types.RegisterObjectClassName(ClassIDReflectionAttribute, "ReflectionAttribute")
	}
}
