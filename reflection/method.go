package reflection

import (
	"fmt"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// ReflectionFunction implémente la classe PHP 8.3 ReflectionFunction.
type ReflectionFunction struct {
	z    *zone.Zone
	p    Provider
	obj  types.Value
	meta *FunctionMeta
}

// NewReflectionFunction construit une instance de ReflectionFunction à partir d'un nom ou d'un *FunctionMeta.
func NewReflectionFunction(z *zone.Zone, p Provider, function any) (*ReflectionFunction, error) {
	if p == nil {
		p = ActiveProvider(z)
	}
	var meta *FunctionMeta
	switch v := function.(type) {
	case string:
		if p == nil {
			return nil, ReflectionException{Message: fmt.Sprintf("Function %s() does not exist", v)}
		}
		m, ok := p.ResolveFunction(v)
		if !ok {
			return nil, ReflectionException{Message: fmt.Sprintf("Function %s() does not exist", v)}
		}
		meta = m
	case types.Value:
		val := types.Deref(z, v)
		if val.Type != types.TypeString {
			return nil, TypeError{Message: "ReflectionFunction::__construct(): Argument #1 ($function) must be of type Closure|string"}
		}
		name := strings.Clone(types.StringData(z, val))
		if p == nil {
			return nil, ReflectionException{Message: fmt.Sprintf("Function %s() does not exist", name)}
		}
		m, ok := p.ResolveFunction(name)
		if !ok {
			return nil, ReflectionException{Message: fmt.Sprintf("Function %s() does not exist", name)}
		}
		meta = m
	case *FunctionMeta:
		if v == nil {
			return nil, ReflectionException{Message: "Function does not exist"}
		}
		meta = v
	default:
		return nil, TypeError{Message: "ReflectionFunction::__construct(): Argument #1 ($function) must be of type Closure|string"}
	}
	obj, err := types.NewObject(z, ClassIDReflectionFunction, 0, false)
	if err != nil {
		return nil, err
	}
	rf := &ReflectionFunction{z: z, p: p, obj: obj, meta: meta}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.functions[objKey{slot: obj.DataSlot, gen: obj.Extra}] = rf
	regMu.Unlock()
	return rf, nil
}

func (rf *ReflectionFunction) Object() types.Value   { return rf.obj }
func (rf *ReflectionFunction) GetName() string       { return strings.TrimPrefix(rf.meta.Name, `\`) }
func (rf *ReflectionFunction) GetShortName() string  { _, s := splitNamespace(rf.meta.Name); return s }
func (rf *ReflectionFunction) GetNamespaceName() string {
	ns, _ := splitNamespace(rf.meta.Name)
	return ns
}
func (rf *ReflectionFunction) InNamespace() bool   { ns, _ := splitNamespace(rf.meta.Name); return ns != "" }
func (rf *ReflectionFunction) IsInternal() bool    { return rf.meta.IsInternal }
func (rf *ReflectionFunction) IsUserDefined() bool { return !rf.meta.IsInternal }
func (rf *ReflectionFunction) IsClosure() bool     { return rf.meta.IsClosure }
func (rf *ReflectionFunction) IsGenerator() bool   { return rf.meta.IsGenerator }
func (rf *ReflectionFunction) ReturnsReference() bool {
	return rf.meta.ByRef
}

func (rf *ReflectionFunction) IsVariadic() bool {
	for i := range rf.meta.Params {
		if rf.meta.Params[i].Variadic {
			return true
		}
	}
	return false
}

func (rf *ReflectionFunction) GetNumberOfParameters() int {
	return len(rf.meta.Params)
}

func (rf *ReflectionFunction) GetNumberOfRequiredParameters() int {
	req := 0
	for i := len(rf.meta.Params) - 1; i >= 0; i-- {
		p := &rf.meta.Params[i]
		if !p.HasDefault && !p.Variadic {
			return i + 1
		}
		_ = req
	}
	return 0
}

func (rf *ReflectionFunction) GetParameters() ([]*ReflectionParameter, error) {
	out := make([]*ReflectionParameter, 0, len(rf.meta.Params))
	for i := range rf.meta.Params {
		rp, err := newReflectionParameter(rf.z, rf.p, nil, rf.meta.Name, i, &rf.meta.Params[i], rf.meta.Params)
		if err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	return out, nil
}

func (rf *ReflectionFunction) HasReturnType() bool {
	return rf.meta.ReturnType != nil
}

func (rf *ReflectionFunction) GetReturnType() (*ReflectionType, error) {
	return newReflectionType(rf.z, rf.p, rf.meta.ReturnType)
}

func (rf *ReflectionFunction) Invoke(args ...types.Value) (types.Value, error) {
	return rf.InvokeArgs(args)
}

func (rf *ReflectionFunction) InvokeArgs(args []types.Value) (types.Value, error) {
	if rf.meta.Invoker == nil {
		return types.NewNull(), nil
	}
	return rf.meta.Invoker(rf.z, args)
}

func (rf *ReflectionFunction) GetAttributes(name string, flags int64) []*ReflectionAttribute {
	return filterAttributes(rf.z, rf.p, rf.meta.Attributes, name, flags)
}

func (rf *ReflectionFunction) String() string {
	return fmt.Sprintf("Function [ <%s> function %s ]\n", map[bool]string{true: "internal", false: "user"}[rf.meta.IsInternal], rf.GetName())
}

// ReflectionMethod implémente la classe PHP 8.3 ReflectionMethod.
type ReflectionMethod struct {
	z          *zone.Zone
	p          Provider
	obj        types.Value
	ownerClass *ClassMeta
	meta       *MethodMeta
}

// NewReflectionMethod construit une instance de ReflectionMethod.
func NewReflectionMethod(z *zone.Zone, p Provider, objectOrMethod any, method ...string) (*ReflectionMethod, error) {
	if p == nil {
		p = ActiveProvider(z)
	}
	var classArg any
	var methodName string
	if len(method) > 0 && method[0] != "" {
		classArg = objectOrMethod
		methodName = method[0]
	} else if s, ok := objectOrMethod.(string); ok {
		before, after, found := strings.Cut(s, "::")
		if !found {
			return nil, ReflectionException{Message: fmt.Sprintf("ReflectionMethod::__construct(): Argument #1 ($objectOrMethod) must be a valid method name, \"%s\" given", s)}
		}
		classArg = before
		methodName = after
	} else {
		return nil, TypeError{Message: "ReflectionMethod::__construct(): expects class and method name"}
	}
	rc, err := NewReflectionClass(z, p, classArg)
	if err != nil {
		return nil, err
	}
	return rc.GetMethod(methodName)
}

func newReflectionMethod(z *zone.Zone, p Provider, ownerClass *ClassMeta, meta *MethodMeta) (*ReflectionMethod, error) {
	obj, err := types.NewObject(z, ClassIDReflectionMethod, 0, false)
	if err != nil {
		return nil, err
	}
	rm := &ReflectionMethod{
		z:          z,
		p:          p,
		obj:        obj,
		ownerClass: ownerClass,
		meta:       meta,
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.methods[objKey{slot: obj.DataSlot, gen: obj.Extra}] = rm
	regMu.Unlock()
	return rm, nil
}

func (rm *ReflectionMethod) Object() types.Value      { return rm.obj }
func (rm *ReflectionMethod) GetName() string          { return rm.meta.Name }
func (rm *ReflectionMethod) GetShortName() string     { return rm.meta.Name }
func (rm *ReflectionMethod) GetNamespaceName() string { return "" }
func (rm *ReflectionMethod) InNamespace() bool        { return false }
func (rm *ReflectionMethod) IsPublic() bool           { return rm.meta.Visibility == VisPublic }
func (rm *ReflectionMethod) IsProtected() bool        { return rm.meta.Visibility == VisProtected }
func (rm *ReflectionMethod) IsPrivate() bool          { return rm.meta.Visibility == VisPrivate }
func (rm *ReflectionMethod) IsStatic() bool           { return rm.meta.IsStatic }
func (rm *ReflectionMethod) IsAbstract() bool         { return rm.meta.IsAbstract || (rm.ownerClass != nil && rm.ownerClass.IsInterface) }
func (rm *ReflectionMethod) IsFinal() bool            { return rm.meta.IsFinal }
func (rm *ReflectionMethod) IsInternal() bool         { return rm.meta.IsInternal }
func (rm *ReflectionMethod) IsUserDefined() bool      { return !rm.meta.IsInternal }
func (rm *ReflectionMethod) IsGenerator() bool        { return rm.meta.IsGenerator }
func (rm *ReflectionMethod) IsClosure() bool          { return false }
func (rm *ReflectionMethod) ReturnsReference() bool   { return rm.meta.ByRef }
func (rm *ReflectionMethod) IsConstructor() bool      { return strings.EqualFold(rm.meta.Name, "__construct") }
func (rm *ReflectionMethod) IsDestructor() bool       { return strings.EqualFold(rm.meta.Name, "__destruct") }

func (rm *ReflectionMethod) GetModifiers() int64 {
	mod := rm.meta.Visibility.Modifiers()
	if rm.meta.IsStatic {
		mod |= MethodIsStatic
	}
	if rm.IsAbstract() {
		mod |= MethodIsAbstract
	}
	if rm.meta.IsFinal {
		mod |= MethodIsFinal
	}
	return mod
}

func (rm *ReflectionMethod) IsVariadic() bool {
	for i := range rm.meta.Params {
		if rm.meta.Params[i].Variadic {
			return true
		}
	}
	return false
}

func (rm *ReflectionMethod) GetNumberOfParameters() int {
	return len(rm.meta.Params)
}

func (rm *ReflectionMethod) GetNumberOfRequiredParameters() int {
	for i := len(rm.meta.Params) - 1; i >= 0; i-- {
		p := &rm.meta.Params[i]
		if !p.HasDefault && !p.Variadic {
			return i + 1
		}
	}
	return 0
}

func (rm *ReflectionMethod) GetParameters() ([]*ReflectionParameter, error) {
	out := make([]*ReflectionParameter, 0, len(rm.meta.Params))
	declClass := rm.ownerClass
	if rm.meta.DeclaringClass != "" && rm.p != nil {
		if dc, ok := rm.p.ResolveClass(rm.meta.DeclaringClass); ok {
			declClass = dc
		}
	}
	for i := range rm.meta.Params {
		rp, err := newReflectionParameter(rm.z, rm.p, declClass, rm.meta.Name, i, &rm.meta.Params[i], rm.meta.Params)
		if err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	return out, nil
}

func (rm *ReflectionMethod) HasReturnType() bool {
	return rm.meta.ReturnType != nil
}

func (rm *ReflectionMethod) GetReturnType() (*ReflectionType, error) {
	return newReflectionType(rm.z, rm.p, rm.meta.ReturnType)
}

func (rm *ReflectionMethod) GetDeclaringClass() (*ReflectionClass, error) {
	if rm.meta.DeclaringClass != "" && rm.p != nil {
		if dc, ok := rm.p.ResolveClass(rm.meta.DeclaringClass); ok {
			return NewReflectionClass(rm.z, rm.p, dc)
		}
	}
	return NewReflectionClass(rm.z, rm.p, rm.ownerClass)
}

// SetAccessible est un no-op en PHP 8.1+ (toutes les méthodes sont accessibles par réflexion).
func (rm *ReflectionMethod) SetAccessible(_ bool) {}

func (rm *ReflectionMethod) Invoke(object types.Value, args ...types.Value) (types.Value, error) {
	return rm.InvokeArgs(object, args)
}

func (rm *ReflectionMethod) InvokeArgs(object types.Value, args []types.Value) (types.Value, error) {
	if rm.IsAbstract() {
		return types.Value{}, ReflectionException{Message: fmt.Sprintf("Trying to invoke abstract method %s::%s()", rm.ownerClass.Name, rm.meta.Name)}
	}
	val := types.Deref(rm.z, object)
	if !rm.meta.IsStatic {
		if val.Type != types.TypeObject {
			return types.Value{}, ReflectionException{Message: fmt.Sprintf("Trying to invoke non static method %s::%s() without an object", rm.ownerClass.Name, rm.meta.Name)}
		}
		rc, err := rm.GetDeclaringClass()
		if err == nil {
			if ok, _ := rc.IsInstance(val); !ok {
				return types.Value{}, ReflectionException{Message: fmt.Sprintf("Given object is not an instance of the class this method was declared in")}
			}
		}
	}
	if rm.meta.Invoker == nil {
		return types.NewNull(), nil
	}
	return rm.meta.Invoker(rm.z, val, args)
}

func (rm *ReflectionMethod) GetAttributes(name string, flags int64) []*ReflectionAttribute {
	return filterAttributes(rm.z, rm.p, rm.meta.Attributes, name, flags)
}

func (rm *ReflectionMethod) String() string {
	return fmt.Sprintf("Method [ <%s> %s method %s ]\n",
		map[bool]string{true: "internal", false: "user"}[rm.meta.IsInternal],
		map[Visibility]string{VisPublic: "public", VisProtected: "protected", VisPrivate: "private"}[rm.meta.Visibility],
		rm.meta.Name)
}
