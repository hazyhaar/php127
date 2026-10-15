package reflection

import (
	"fmt"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// ReflectionClass implémente la classe PHP 8.3 ReflectionClass.
type ReflectionClass struct {
	z      *zone.Zone
	p      Provider
	obj    types.Value
	meta   *ClassMeta
	target types.Value
	hasObj bool
}

// ReflectionObject implémente la classe PHP 8.3 ReflectionObject (héritant de ReflectionClass).
type ReflectionObject struct {
	*ReflectionClass
}

// NewReflectionClass construit une instance de ReflectionClass à partir d'un nom de classe,
// d'un types.Value (chaîne ou objet) ou d'un *ClassMeta.
func NewReflectionClass(z *zone.Zone, p Provider, objectOrClass any) (*ReflectionClass, error) {
	if p == nil {
		p = ActiveProvider(z)
	}
	meta, targetObj, hasObj, err := resolveTargetClass(z, p, objectOrClass)
	if err != nil {
		return nil, err
	}
	obj, err := types.NewObject(z, ClassIDReflectionClass, 0, false)
	if err != nil {
		return nil, err
	}
	rc := &ReflectionClass{
		z:      z,
		p:      p,
		obj:    obj,
		meta:   meta,
		target: targetObj,
		hasObj: hasObj,
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.classes[objKey{slot: obj.DataSlot, gen: obj.Extra}] = rc
	regMu.Unlock()
	return rc, nil
}

// NewReflectionObject construit une instance de ReflectionObject à partir d'un objet PHP.
func NewReflectionObject(z *zone.Zone, p Provider, object types.Value) (*ReflectionObject, error) {
	if p == nil {
		p = ActiveProvider(z)
	}
	val := types.Deref(z, object)
	if val.Type != types.TypeObject {
		return nil, TypeError{Message: "ReflectionObject::__construct(): Argument #1 ($object) must be of type object"}
	}
	meta, targetObj, hasObj, err := resolveTargetClass(z, p, val)
	if err != nil {
		return nil, err
	}
	obj, err := types.NewObject(z, ClassIDReflectionObject, 0, false)
	if err != nil {
		return nil, err
	}
	rc := &ReflectionClass{
		z:      z,
		p:      p,
		obj:    obj,
		meta:   meta,
		target: targetObj,
		hasObj: hasObj,
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.classes[objKey{slot: obj.DataSlot, gen: obj.Extra}] = rc
	regMu.Unlock()
	return &ReflectionObject{ReflectionClass: rc}, nil
}

func (rc *ReflectionClass) Object() types.Value   { return rc.obj }
func (rc *ReflectionClass) Meta() *ClassMeta      { return rc.meta }
func (rc *ReflectionClass) GetName() string       { return strings.TrimPrefix(rc.meta.Name, `\`) }
func (rc *ReflectionClass) GetShortName() string  { _, s := splitNamespace(rc.meta.Name); return s }
func (rc *ReflectionClass) GetNamespaceName() string {
	ns, _ := splitNamespace(rc.meta.Name)
	return ns
}
func (rc *ReflectionClass) InNamespace() bool   { ns, _ := splitNamespace(rc.meta.Name); return ns != "" }
func (rc *ReflectionClass) IsInternal() bool    { return rc.meta.IsInternal }
func (rc *ReflectionClass) IsUserDefined() bool { return !rc.meta.IsInternal }
func (rc *ReflectionClass) IsInterface() bool   { return rc.meta.IsInterface }
func (rc *ReflectionClass) IsTrait() bool       { return rc.meta.IsTrait }
func (rc *ReflectionClass) IsEnum() bool        { return rc.meta.IsEnum }
func (rc *ReflectionClass) IsFinal() bool       { return rc.meta.IsFinal }
func (rc *ReflectionClass) IsReadOnly() bool    { return rc.meta.IsReadOnly }
func (rc *ReflectionClass) IsAnonymous() bool   { return rc.meta.IsAnonymous }

func (rc *ReflectionClass) IsAbstract() bool {
	if rc.meta.IsAbstract || rc.meta.IsInterface {
		return true
	}
	for i := range rc.meta.Methods {
		if rc.meta.Methods[i].IsAbstract {
			return true
		}
	}
	return false
}

func (rc *ReflectionClass) GetModifiers() int64 {
	var mod int64
	if rc.meta.IsAbstract {
		mod |= ClassIsExplicitAbstract
	} else {
		for i := range rc.meta.Methods {
			if rc.meta.Methods[i].IsAbstract {
				mod |= ClassIsImplicitAbstract
				break
			}
		}
	}
	if rc.meta.IsFinal {
		mod |= ClassIsFinal
	}
	if rc.meta.IsReadOnly {
		mod |= ClassIsReadOnly
	}
	return mod
}

func (rc *ReflectionClass) IsInstantiable() bool {
	if rc.meta.IsInterface || rc.meta.IsTrait || rc.meta.IsEnum || rc.IsAbstract() {
		return false
	}
	if ctor := rc.findMethodMeta("__construct"); ctor != nil {
		if ctor.Visibility != VisPublic {
			return false
		}
	}
	return true
}

func (rc *ReflectionClass) IsCloneable() bool {
	if rc.meta.IsInterface || rc.meta.IsTrait || rc.meta.IsEnum || rc.IsAbstract() {
		return false
	}
	if cloneM := rc.findMethodMeta("__clone"); cloneM != nil {
		return cloneM.Visibility == VisPublic
	}
	return true
}

func (rc *ReflectionClass) GetParentClass() (*ReflectionClass, error) {
	if rc.meta.ParentName == "" {
		return nil, nil
	}
	return NewReflectionClass(rc.z, rc.p, rc.meta.ParentName)
}

func (rc *ReflectionClass) IsSubclassOf(classOrName any) (bool, error) {
	targetMeta, _, _, err := resolveTargetClass(rc.z, rc.p, classOrName)
	if err != nil {
		return false, err
	}
	targetNorm := strings.ToLower(strings.TrimPrefix(targetMeta.Name, `\`))
	selfNorm := strings.ToLower(strings.TrimPrefix(rc.meta.Name, `\`))
	if selfNorm == targetNorm {
		return false, nil
	}
	return isClassOrSubclass(rc.p, rc.meta, targetMeta.Name), nil
}

func (rc *ReflectionClass) ImplementsInterface(interfaceOrName any) (bool, error) {
	targetMeta, _, _, err := resolveTargetClass(rc.z, rc.p, interfaceOrName)
	if err != nil {
		return false, err
	}
	if !targetMeta.IsInterface {
		return false, ReflectionException{Message: fmt.Sprintf("Interface \"%s\" is a class", targetMeta.Name)}
	}
	return isClassOrSubclass(rc.p, rc.meta, targetMeta.Name), nil
}

func (rc *ReflectionClass) GetInterfaceNames() []string {
	var out []string
	seen := make(map[string]bool)
	cur := rc.meta
	for cur != nil {
		for _, iface := range cur.Interfaces {
			norm := strings.ToLower(strings.TrimPrefix(iface, `\`))
			if !seen[norm] {
				seen[norm] = true
				out = append(out, strings.TrimPrefix(iface, `\`))
			}
		}
		if cur.ParentName == "" || rc.p == nil {
			break
		}
		next, ok := rc.p.ResolveClass(cur.ParentName)
		if !ok {
			break
		}
		cur = next
	}
	return out
}

func (rc *ReflectionClass) GetInterfaces() (map[string]*ReflectionClass, error) {
	names := rc.GetInterfaceNames()
	out := make(map[string]*ReflectionClass, len(names))
	for _, name := range names {
		irc, err := NewReflectionClass(rc.z, rc.p, name)
		if err != nil {
			return nil, err
		}
		out[irc.GetName()] = irc
	}
	return out, nil
}

func (rc *ReflectionClass) GetTraitNames() []string {
	out := make([]string, len(rc.meta.Traits))
	for i, t := range rc.meta.Traits {
		out[i] = strings.TrimPrefix(t, `\`)
	}
	return out
}

func (rc *ReflectionClass) GetTraits() (map[string]*ReflectionClass, error) {
	out := make(map[string]*ReflectionClass, len(rc.meta.Traits))
	for _, t := range rc.meta.Traits {
		trc, err := NewReflectionClass(rc.z, rc.p, t)
		if err != nil {
			return nil, err
		}
		out[trc.GetName()] = trc
	}
	return out, nil
}

func (rc *ReflectionClass) collectAllMethods() []*MethodMeta {
	var out []*MethodMeta
	seen := make(map[string]bool)
	cur := rc.meta
	for cur != nil {
		for i := range cur.Methods {
			m := &cur.Methods[i]
			norm := strings.ToLower(m.Name)
			if !seen[norm] {
				seen[norm] = true
				out = append(out, m)
			}
		}
		if cur.ParentName == "" || rc.p == nil {
			break
		}
		next, ok := rc.p.ResolveClass(cur.ParentName)
		if !ok {
			break
		}
		cur = next
	}
	return out
}

func (rc *ReflectionClass) findMethodMeta(name string) *MethodMeta {
	for _, m := range rc.collectAllMethods() {
		if strings.EqualFold(m.Name, name) {
			return m
		}
	}
	return nil
}

func (rc *ReflectionClass) HasMethod(name string) bool {
	return rc.findMethodMeta(name) != nil
}

func (rc *ReflectionClass) GetMethod(name string) (*ReflectionMethod, error) {
	m := rc.findMethodMeta(name)
	if m == nil {
		return nil, ReflectionException{Message: fmt.Sprintf("Method %s::%s() does not exist", rc.GetName(), name)}
	}
	return newReflectionMethod(rc.z, rc.p, rc.meta, m)
}

func (rc *ReflectionClass) GetMethods(filter ...int64) ([]*ReflectionMethod, error) {
	hasFilter := len(filter) > 0 && filter[0] != 0
	var mask int64
	if hasFilter {
		mask = filter[0]
	}
	all := rc.collectAllMethods()
	var out []*ReflectionMethod
	for _, m := range all {
		rm, err := newReflectionMethod(rc.z, rc.p, rc.meta, m)
		if err != nil {
			return nil, err
		}
		if hasFilter && rm.GetModifiers()&mask == 0 {
			continue
		}
		out = append(out, rm)
	}
	return out, nil
}

func (rc *ReflectionClass) GetConstructor() (*ReflectionMethod, error) {
	m := rc.findMethodMeta("__construct")
	if m == nil {
		return nil, nil
	}
	return newReflectionMethod(rc.z, rc.p, rc.meta, m)
}

func (rc *ReflectionClass) collectAllProperties() []*PropertyMeta {
	var out []*PropertyMeta
	seen := make(map[string]bool)
	cur := rc.meta
	for cur != nil {
		for i := range cur.Properties {
			p := &cur.Properties[i]
			if cur != rc.meta && p.Visibility == VisPrivate {
				continue
			}
			if !seen[p.Name] {
				seen[p.Name] = true
				out = append(out, p)
			}
		}
		if cur.ParentName == "" || rc.p == nil {
			break
		}
		next, ok := rc.p.ResolveClass(cur.ParentName)
		if !ok {
			break
		}
		cur = next
	}
	return out
}

func (rc *ReflectionClass) findPropertyMeta(name string) *PropertyMeta {
	for _, p := range rc.collectAllProperties() {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func (rc *ReflectionClass) HasProperty(name string) bool {
	return rc.findPropertyMeta(name) != nil
}

func (rc *ReflectionClass) GetProperty(name string) (*ReflectionProperty, error) {
	p := rc.findPropertyMeta(name)
	if p == nil {
		return nil, ReflectionException{Message: fmt.Sprintf("Property %s::$%s does not exist", rc.GetName(), name)}
	}
	return newReflectionProperty(rc.z, rc.p, rc.meta, p)
}

func (rc *ReflectionClass) GetProperties(filter ...int64) ([]*ReflectionProperty, error) {
	hasFilter := len(filter) > 0 && filter[0] != 0
	var mask int64
	if hasFilter {
		mask = filter[0]
	}
	all := rc.collectAllProperties()
	var out []*ReflectionProperty
	for _, p := range all {
		rp, err := newReflectionProperty(rc.z, rc.p, rc.meta, p)
		if err != nil {
			return nil, err
		}
		if hasFilter && rp.GetModifiers()&mask == 0 {
			continue
		}
		out = append(out, rp)
	}
	return out, nil
}

func (rc *ReflectionClass) collectAllConstants() []*ConstantMeta {
	var out []*ConstantMeta
	seen := make(map[string]bool)
	cur := rc.meta
	for cur != nil {
		for i := range cur.Constants {
			c := &cur.Constants[i]
			if cur != rc.meta && c.Visibility == VisPrivate {
				continue
			}
			if !seen[c.Name] {
				seen[c.Name] = true
				out = append(out, c)
			}
		}
		if cur.ParentName == "" || rc.p == nil {
			break
		}
		next, ok := rc.p.ResolveClass(cur.ParentName)
		if !ok {
			break
		}
		cur = next
	}
	return out
}

func (rc *ReflectionClass) findConstantMeta(name string) *ConstantMeta {
	for _, c := range rc.collectAllConstants() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (rc *ReflectionClass) HasConstant(name string) bool {
	return rc.findConstantMeta(name) != nil
}

func (rc *ReflectionClass) GetConstant(name string) (types.Value, bool, error) {
	c := rc.findConstantMeta(name)
	if c == nil {
		return types.NewBool(false), false, nil
	}
	rcConst, err := newReflectionClassConstant(rc.z, rc.p, rc.meta, c)
	if err != nil {
		return types.Value{}, false, err
	}
	val, err := rcConst.GetValue()
	return val, true, err
}

func (rc *ReflectionClass) GetConstants(filter ...int64) (types.Value, error) {
	rcConsts, err := rc.GetReflectionConstants(filter...)
	if err != nil {
		return types.Value{}, err
	}
	arr, err := types.NewArray(rc.z)
	if err != nil {
		return types.Value{}, err
	}
	for _, rcc := range rcConsts {
		val, err := rcc.GetValue()
		if err != nil {
			types.ReleaseArray(rc.z, &arr)
			return types.Value{}, err
		}
		k, err := types.NewString(rc.z, rcc.GetName(), 0)
		if err != nil {
			types.ReleaseValue(rc.z, &val)
			types.ReleaseArray(rc.z, &arr)
			return types.Value{}, err
		}
		err = types.ArraySet(rc.z, &arr, k, val, nil)
		types.ReleaseValue(rc.z, &k)
		types.ReleaseValue(rc.z, &val)
		if err != nil {
			types.ReleaseArray(rc.z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

func (rc *ReflectionClass) GetReflectionConstant(name string) (*ReflectionClassConstant, error) {
	c := rc.findConstantMeta(name)
	if c == nil {
		return nil, nil
	}
	return newReflectionClassConstant(rc.z, rc.p, rc.meta, c)
}

func (rc *ReflectionClass) GetReflectionConstants(filter ...int64) ([]*ReflectionClassConstant, error) {
	hasFilter := len(filter) > 0 && filter[0] != 0
	var mask int64
	if hasFilter {
		mask = filter[0]
	}
	all := rc.collectAllConstants()
	var out []*ReflectionClassConstant
	for _, c := range all {
		rcc, err := newReflectionClassConstant(rc.z, rc.p, rc.meta, c)
		if err != nil {
			return nil, err
		}
		if hasFilter && rcc.GetModifiers()&mask == 0 {
			continue
		}
		out = append(out, rcc)
	}
	return out, nil
}

func (rc *ReflectionClass) instantiateInternal(withCtor bool, args []types.Value) (types.Value, error) {
	if !rc.IsInstantiable() {
		if rc.meta.IsInterface {
			return types.Value{}, Error{Message: fmt.Sprintf("Cannot instantiate interface %s", rc.GetName())}
		}
		if rc.meta.IsTrait {
			return types.Value{}, Error{Message: fmt.Sprintf("Cannot instantiate trait %s", rc.GetName())}
		}
		if rc.meta.IsEnum {
			return types.Value{}, Error{Message: fmt.Sprintf("Cannot instantiate enum %s", rc.GetName())}
		}
		if rc.IsAbstract() {
			return types.Value{}, Error{Message: fmt.Sprintf("Cannot instantiate abstract class %s", rc.GetName())}
		}
		return types.Value{}, ReflectionException{Message: fmt.Sprintf("Access to non-public constructor of class %s", rc.GetName())}
	}
	if rc.meta.Instantiator != nil {
		return rc.meta.Instantiator(rc.z, rc.meta, withCtor, args)
	}
	allProps := rc.collectAllProperties()
	var maxSlot uint32
	for _, p := range allProps {
		if !p.IsStatic && p.Index+1 > maxSlot {
			maxSlot = p.Index + 1
		}
	}
	if uint32(len(allProps)) > maxSlot {
		maxSlot = uint32(len(allProps))
	}
	obj, err := types.NewObject(rc.z, rc.meta.ID, maxSlot, false)
	if err != nil {
		return types.Value{}, err
	}
	r := getOrCreateRegistry(rc.z)
	for _, p := range allProps {
		if p.IsStatic {
			continue
		}
		if p.HasDefault {
			defVal := p.Default
			if p.DefaultString != "" {
				sv, err := types.NewString(rc.z, p.DefaultString, 0)
				if err != nil {
					types.ReleaseObject(rc.z, &obj)
					return types.Value{}, err
				}
				types.ObjectSet(rc.z, obj, p.Index, sv)
				types.ReleaseValue(rc.z, &sv)
			} else {
				types.ObjectSet(rc.z, obj, p.Index, defVal)
			}
			regMu.Lock()
			r.propInit[propInitKey{slot: obj.DataSlot, gen: obj.Extra, prop: p.Name}] = true
			regMu.Unlock()
		} else if p.Type == nil {
			types.ObjectSet(rc.z, obj, p.Index, types.NewNull())
			regMu.Lock()
			r.propInit[propInitKey{slot: obj.DataSlot, gen: obj.Extra, prop: p.Name}] = true
			regMu.Unlock()
		}
	}
	if withCtor {
		if ctor := rc.findMethodMeta("__construct"); ctor != nil && ctor.Invoker != nil {
			if _, err := ctor.Invoker(rc.z, obj, args); err != nil {
				types.ReleaseObject(rc.z, &obj)
				return types.Value{}, err
			}
		}
	}
	return obj, nil
}

func (rc *ReflectionClass) NewInstance(args ...types.Value) (types.Value, error) {
	return rc.instantiateInternal(true, args)
}

func (rc *ReflectionClass) NewInstanceArgs(args []types.Value) (types.Value, error) {
	return rc.instantiateInternal(true, args)
}

func (rc *ReflectionClass) NewInstanceWithoutConstructor() (types.Value, error) {
	if rc.meta.IsInterface || rc.meta.IsTrait || rc.meta.IsEnum || rc.IsAbstract() {
		return rc.instantiateInternal(false, nil)
	}
	if rc.meta.IsInternal {
		if ctor := rc.findMethodMeta("__construct"); ctor != nil && ctor.IsFinal {
			return types.Value{}, ReflectionException{Message: fmt.Sprintf("Class %s is an internal class marked as final that cannot be instantiated without invoking its constructor", rc.GetName())}
		}
	}
	if rc.meta.Instantiator != nil {
		return rc.meta.Instantiator(rc.z, rc.meta, false, nil)
	}
	allProps := rc.collectAllProperties()
	var maxSlot uint32
	for _, p := range allProps {
		if !p.IsStatic && p.Index+1 > maxSlot {
			maxSlot = p.Index + 1
		}
	}
	if uint32(len(allProps)) > maxSlot {
		maxSlot = uint32(len(allProps))
	}
	obj, err := types.NewObject(rc.z, rc.meta.ID, maxSlot, false)
	if err != nil {
		return types.Value{}, err
	}
	r := getOrCreateRegistry(rc.z)
	for _, p := range allProps {
		if p.IsStatic {
			continue
		}
		if p.HasDefault {
			if p.DefaultString != "" {
				sv, err := types.NewString(rc.z, p.DefaultString, 0)
				if err != nil {
					types.ReleaseObject(rc.z, &obj)
					return types.Value{}, err
				}
				types.ObjectSet(rc.z, obj, p.Index, sv)
				types.ReleaseValue(rc.z, &sv)
			} else {
				types.ObjectSet(rc.z, obj, p.Index, p.Default)
			}
			regMu.Lock()
			r.propInit[propInitKey{slot: obj.DataSlot, gen: obj.Extra, prop: p.Name}] = true
			regMu.Unlock()
		} else if p.Type == nil {
			types.ObjectSet(rc.z, obj, p.Index, types.NewNull())
			regMu.Lock()
			r.propInit[propInitKey{slot: obj.DataSlot, gen: obj.Extra, prop: p.Name}] = true
			regMu.Unlock()
		}
	}
	return obj, nil
}

func (rc *ReflectionClass) IsInstance(object types.Value) (bool, error) {
	val := types.Deref(rc.z, object)
	if val.Type != types.TypeObject {
		return false, TypeError{Message: "ReflectionClass::isInstance(): Argument #1 ($object) must be of type object"}
	}
	hdr := types.ObjectInfo(rc.z, val)
	if hdr.ClassID == rc.meta.ID && rc.meta.ID != 0 {
		return true, nil
	}
	if rc.p == nil {
		return false, nil
	}
	objClass, ok := rc.p.ResolveClassByID(hdr.ClassID)
	if !ok {
		return false, nil
	}
	return isClassOrSubclass(rc.p, objClass, rc.meta.Name), nil
}

func (rc *ReflectionClass) GetAttributes(name string, flags int64) []*ReflectionAttribute {
	return filterAttributes(rc.z, rc.p, rc.meta.Attributes, name, flags)
}

func (rc *ReflectionClass) String() string {
	kind := "Class"
	if rc.meta.IsInterface {
		kind = "Interface"
	} else if rc.meta.IsTrait {
		kind = "Trait"
	} else if rc.meta.IsEnum {
		kind = "Enum"
	}
	return fmt.Sprintf("%s [ <%s> class %s ]\n", kind, map[bool]string{true: "internal", false: "user"}[rc.meta.IsInternal], rc.GetName())
}
