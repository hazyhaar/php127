package reflection

import (
	"fmt"
	"strings"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type objKey struct {
	slot uint32
	gen  uint16
}

type propInitKey struct {
	slot uint32
	gen  uint16
	prop string
}

type zoneRegistry struct {
	id         uint32
	z          *zone.Zone
	provider   Provider
	classes    map[objKey]*ReflectionClass
	methods    map[objKey]*ReflectionMethod
	functions  map[objKey]*ReflectionFunction
	properties map[objKey]*ReflectionProperty
	parameters map[objKey]*ReflectionParameter
	constants  map[objKey]*ReflectionClassConstant
	unitCases  map[objKey]*ReflectionEnumUnitCase
	typesMap   map[objKey]*ReflectionType
	attributes map[objKey]*ReflectionAttribute
	propInit   map[propInitKey]bool
	staticVals map[string]types.Value
	cleaned    bool
}

var (
	regMu     sync.Mutex
	nextRegID uint32
	regsByZ   = make(map[*zone.Zone]*zoneRegistry)
	regsByID  = make(map[uint32]*zoneRegistry)
)

func getOrCreateRegistry(z *zone.Zone) *zoneRegistry {
	if z == nil {
		panic("reflection: nil zone")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if r, ok := regsByZ[z]; ok {
		return r
	}
	nextRegID++
	id := nextRegID
	r := &zoneRegistry{
		id:         id,
		z:          z,
		classes:    make(map[objKey]*ReflectionClass),
		methods:    make(map[objKey]*ReflectionMethod),
		functions:  make(map[objKey]*ReflectionFunction),
		properties: make(map[objKey]*ReflectionProperty),
		parameters: make(map[objKey]*ReflectionParameter),
		constants:  make(map[objKey]*ReflectionClassConstant),
		unitCases:  make(map[objKey]*ReflectionEnumUnitCase),
		typesMap:   make(map[objKey]*ReflectionType),
		attributes: make(map[objKey]*ReflectionAttribute),
		propInit:   make(map[propInitKey]bool),
		staticVals: make(map[string]types.Value),
	}
	regsByZ[z] = r
	regsByID[id] = r
	z.RegisterCleanup(zone.CleanupItem{
		Kind:   CleanupReflection,
		Target: id,
	})
	return r
}

// BindProvider attache un Provider de métadonnées à la Zone z pour la durée de la requête.
func BindProvider(z *zone.Zone, p Provider) {
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.provider = p
	regMu.Unlock()
}

// ActiveProvider retourne le Provider attaché à la Zone z.
func ActiveProvider(z *zone.Zone) Provider {
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	return r.provider
}

// ClassFromObject retrouve l'instance *ReflectionClass depuis un objet ReflectionClass, ReflectionObject ou ReflectionEnum.
func ClassFromObject(z *zone.Zone, v types.Value) (*ReflectionClass, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, false
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	rc, ok := r.classes[objKey{slot: v.DataSlot, gen: v.Extra}]
	return rc, ok
}

// MethodFromObject retrouve l'instance *ReflectionMethod depuis un objet ReflectionMethod.
func MethodFromObject(z *zone.Zone, v types.Value) (*ReflectionMethod, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, false
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	rm, ok := r.methods[objKey{slot: v.DataSlot, gen: v.Extra}]
	return rm, ok
}

// FunctionFromObject retrouve l'instance *ReflectionFunction depuis un objet ReflectionFunction.
func FunctionFromObject(z *zone.Zone, v types.Value) (*ReflectionFunction, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, false
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	rf, ok := r.functions[objKey{slot: v.DataSlot, gen: v.Extra}]
	return rf, ok
}

// PropertyFromObject retrouve l'instance *ReflectionProperty depuis un objet ReflectionProperty.
func PropertyFromObject(z *zone.Zone, v types.Value) (*ReflectionProperty, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, false
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	rp, ok := r.properties[objKey{slot: v.DataSlot, gen: v.Extra}]
	return rp, ok
}

// ParameterFromObject retrouve l'instance *ReflectionParameter depuis un objet ReflectionParameter.
func ParameterFromObject(z *zone.Zone, v types.Value) (*ReflectionParameter, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, false
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	rp, ok := r.parameters[objKey{slot: v.DataSlot, gen: v.Extra}]
	return rp, ok
}

// ClassConstantFromObject retrouve l'instance *ReflectionClassConstant depuis un objet ReflectionClassConstant ou ReflectionEnumUnitCase.
func ClassConstantFromObject(z *zone.Zone, v types.Value) (*ReflectionClassConstant, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, false
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	rcc, ok := r.constants[objKey{slot: v.DataSlot, gen: v.Extra}]
	return rcc, ok
}

// EnumUnitCaseFromObject retrouve l'instance *ReflectionEnumUnitCase depuis un objet ReflectionEnumUnitCase ou ReflectionEnumBackedCase.
func EnumUnitCaseFromObject(z *zone.Zone, v types.Value) (*ReflectionEnumUnitCase, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, false
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	uc, ok := r.unitCases[objKey{slot: v.DataSlot, gen: v.Extra}]
	return uc, ok
}

// TypeFromObject retrouve l'instance *ReflectionType depuis un objet ReflectionNamedType, ReflectionUnionType ou ReflectionIntersectionType.
func TypeFromObject(z *zone.Zone, v types.Value) (*ReflectionType, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, false
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	rt, ok := r.typesMap[objKey{slot: v.DataSlot, gen: v.Extra}]
	return rt, ok
}

// AttributeFromObject retrouve l'instance *ReflectionAttribute depuis un objet ReflectionAttribute.
func AttributeFromObject(z *zone.Zone, v types.Value) (*ReflectionAttribute, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, false
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	defer regMu.Unlock()
	ra, ok := r.attributes[objKey{slot: v.DataSlot, gen: v.Extra}]
	return ra, ok
}

// CleanupDispatcher retourne la fonction de nettoyage compatible avec z.Reset().
func CleanupDispatcher(z *zone.Zone) func(zone.CleanupItem) {
	return func(item zone.CleanupItem) {
		DispatchCleanup(z, item)
	}
}

// DispatchCleanup libère toutes les structures de réflexion associées à la Zone lors de z.Reset().
func DispatchCleanup(z *zone.Zone, item zone.CleanupItem) {
	switch item.Kind {
	case CleanupReflection:
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
	for k, v := range r.staticVals {
		if !v.IsUndef() {
			types.ReleaseValue(z, &v)
		}
		delete(r.staticVals, k)
	}
	clear(r.classes)
	clear(r.methods)
	clear(r.functions)
	clear(r.properties)
	clear(r.parameters)
	clear(r.constants)
	clear(r.unitCases)
	clear(r.typesMap)
	clear(r.attributes)
	clear(r.propInit)
}

func splitNamespace(fullName string) (ns, short string) {
	fullName = strings.TrimPrefix(fullName, `\`)
	idx := strings.LastIndexByte(fullName, '\\')
	if idx < 0 {
		return "", fullName
	}
	return fullName[:idx], fullName[idx+1:]
}

func resolveTargetClass(z *zone.Zone, p Provider, objectOrClass any) (*ClassMeta, types.Value, bool, error) {
	if p == nil {
		return nil, types.Value{}, false, ReflectionException{Message: "No reflection provider bound to Zone"}
	}
	switch v := objectOrClass.(type) {
	case string:
		meta, ok := p.ResolveClass(v)
		if !ok {
			return nil, types.Value{}, false, ReflectionException{Message: fmt.Sprintf("Class \"%s\" does not exist", v)}
		}
		return meta, types.Value{}, false, nil
	case types.Value:
		val := types.Deref(z, v)
		switch val.Type {
		case types.TypeString:
			name := strings.Clone(types.StringData(z, val))
			meta, ok := p.ResolveClass(name)
			if !ok {
				return nil, types.Value{}, false, ReflectionException{Message: fmt.Sprintf("Class \"%s\" does not exist", name)}
			}
			return meta, types.Value{}, false, nil
		case types.TypeObject:
			hdr := types.ObjectInfo(z, val)
			meta, ok := p.ResolveClassByID(hdr.ClassID)
			if !ok {
				return nil, types.Value{}, false, ReflectionException{Message: fmt.Sprintf("Class ID %d does not exist", hdr.ClassID)}
			}
			return meta, val, true, nil
		default:
			return nil, types.Value{}, false, TypeError{Message: "ReflectionClass::__construct(): Argument #1 ($objectOrClass) must be of type object|string"}
		}
	case *ClassMeta:
		if v == nil {
			return nil, types.Value{}, false, ReflectionException{Message: "Class does not exist"}
		}
		return v, types.Value{}, false, nil
	default:
		return nil, types.Value{}, false, TypeError{Message: "ReflectionClass::__construct(): Argument #1 ($objectOrClass) must be of type object|string"}
	}
}

func isClassOrSubclass(p Provider, sub *ClassMeta, targetName string) bool {
	if sub == nil {
		return false
	}
	targetNorm := strings.ToLower(strings.TrimPrefix(targetName, `\`))
	cur := sub
	visited := make(map[string]bool)
	for cur != nil {
		curNorm := strings.ToLower(strings.TrimPrefix(cur.Name, `\`))
		if curNorm == targetNorm {
			return true
		}
		for _, iface := range cur.Interfaces {
			if strings.ToLower(strings.TrimPrefix(iface, `\`)) == targetNorm {
				return true
			}
		}
		if cur.ParentName == "" || visited[curNorm] {
			break
		}
		visited[curNorm] = true
		if p == nil {
			break
		}
		next, ok := p.ResolveClass(cur.ParentName)
		if !ok {
			break
		}
		cur = next
	}
	return false
}

func filterAttributes(z *zone.Zone, p Provider, attrs []AttributeMeta, filterName string, flags int64) []*ReflectionAttribute {
	var out []*ReflectionAttribute
	filterNorm := strings.ToLower(strings.TrimPrefix(filterName, `\`))
	for i := range attrs {
		a := &attrs[i]
		if filterName != "" {
			attrNorm := strings.ToLower(strings.TrimPrefix(a.Name, `\`))
			if flags&AttributeIsInstanceOf != 0 {
				if attrNorm != filterNorm {
					if p == nil {
						continue
					}
					cm, ok := p.ResolveClass(a.Name)
					if !ok || !isClassOrSubclass(p, cm, filterName) {
						continue
					}
				}
			} else if attrNorm != filterNorm {
				continue
			}
		}
		ra, err := newReflectionAttribute(z, p, a)
		if err == nil {
			out = append(out, ra)
		}
	}
	return out
}

// ReflectionType implémente ReflectionNamedType, ReflectionUnionType et ReflectionIntersectionType.
type ReflectionType struct {
	z    *zone.Zone
	p    Provider
	obj  types.Value
	meta *TypeMeta
}

func newReflectionType(z *zone.Zone, p Provider, meta *TypeMeta) (*ReflectionType, error) {
	if meta == nil {
		return nil, nil
	}
	var classID uint32
	switch meta.Kind {
	case TypeKindUnion:
		classID = ClassIDReflectionUnionType
	case TypeKindIntersection:
		classID = ClassIDReflectionIntersectionType
	default:
		classID = ClassIDReflectionNamedType
	}
	obj, err := types.NewObject(z, classID, 0, false)
	if err != nil {
		return nil, err
	}
	rt := &ReflectionType{z: z, p: p, obj: obj, meta: meta}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.typesMap[objKey{slot: obj.DataSlot, gen: obj.Extra}] = rt
	regMu.Unlock()
	return rt, nil
}

func (rt *ReflectionType) Object() types.Value { return rt.obj }
func (rt *ReflectionType) Kind() TypeKind      { return rt.meta.Kind }
func (rt *ReflectionType) GetName() string     { return rt.meta.Name }
func (rt *ReflectionType) IsBuiltin() bool     { return rt.meta.IsBuiltin }
func (rt *ReflectionType) AllowsNull() bool    { return rt.meta.AllowsNull }
func (rt *ReflectionType) String() string      { return rt.meta.String() }

func (rt *ReflectionType) GetTypes() ([]*ReflectionType, error) {
	if rt.meta.Kind == TypeKindNamed {
		return nil, nil
	}
	out := make([]*ReflectionType, 0, len(rt.meta.Types))
	for _, sub := range rt.meta.Types {
		srt, err := newReflectionType(rt.z, rt.p, sub)
		if err != nil {
			return nil, err
		}
		out = append(out, srt)
	}
	return out, nil
}

// ReflectionAttribute implémente la classe PHP 8 ReflectionAttribute.
type ReflectionAttribute struct {
	z    *zone.Zone
	p    Provider
	obj  types.Value
	meta *AttributeMeta
}

func newReflectionAttribute(z *zone.Zone, p Provider, meta *AttributeMeta) (*ReflectionAttribute, error) {
	obj, err := types.NewObject(z, ClassIDReflectionAttribute, 0, false)
	if err != nil {
		return nil, err
	}
	ra := &ReflectionAttribute{z: z, p: p, obj: obj, meta: meta}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.attributes[objKey{slot: obj.DataSlot, gen: obj.Extra}] = ra
	regMu.Unlock()
	return ra, nil
}

func (ra *ReflectionAttribute) Object() types.Value { return ra.obj }
func (ra *ReflectionAttribute) GetName() string     { return ra.meta.Name }

func (ra *ReflectionAttribute) GetArguments() (types.Value, error) {
	arr, err := types.NewArray(ra.z)
	if err != nil {
		return types.Value{}, err
	}
	for _, arg := range ra.meta.Args {
		if arg.Name == "" {
			if err := types.ArrayAppend(ra.z, &arr, arg.Value); err != nil {
				types.ReleaseArray(ra.z, &arr)
				return types.Value{}, err
			}
		} else {
			k, err := types.NewString(ra.z, arg.Name, 0)
			if err != nil {
				types.ReleaseArray(ra.z, &arr)
				return types.Value{}, err
			}
			err = types.ArraySet(ra.z, &arr, k, arg.Value, nil)
			types.ReleaseValue(ra.z, &k)
			if err != nil {
				types.ReleaseArray(ra.z, &arr)
				return types.Value{}, err
			}
		}
	}
	return arr, nil
}

func (ra *ReflectionAttribute) NewInstance() (types.Value, error) {
	rc, err := NewReflectionClass(ra.z, ra.p, ra.meta.Name)
	if err != nil {
		return types.Value{}, err
	}
	args := make([]types.Value, len(ra.meta.Args))
	for i, a := range ra.meta.Args {
		args[i] = a.Value
	}
	return rc.NewInstance(args...)
}

func (ra *ReflectionAttribute) String() string {
	return fmt.Sprintf("Attribute [ %s ]\n", ra.meta.Name)
}
