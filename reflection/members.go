package reflection

import (
	"fmt"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// ReflectionProperty implémente la classe PHP 8.3 ReflectionProperty.
type ReflectionProperty struct {
	z          *zone.Zone
	p          Provider
	obj        types.Value
	ownerClass *ClassMeta
	meta       *PropertyMeta
}

// NewReflectionProperty construit une instance de ReflectionProperty pour une classe et un nom de propriété.
func NewReflectionProperty(z *zone.Zone, p Provider, class any, property string) (*ReflectionProperty, error) {
	if p == nil {
		p = ActiveProvider(z)
	}
	rc, err := NewReflectionClass(z, p, class)
	if err != nil {
		return nil, err
	}
	property = strings.TrimPrefix(property, "$")
	return rc.GetProperty(property)
}

func newReflectionProperty(z *zone.Zone, p Provider, ownerClass *ClassMeta, meta *PropertyMeta) (*ReflectionProperty, error) {
	obj, err := types.NewObject(z, ClassIDReflectionProperty, 0, false)
	if err != nil {
		return nil, err
	}
	rp := &ReflectionProperty{
		z:          z,
		p:          p,
		obj:        obj,
		ownerClass: ownerClass,
		meta:       meta,
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.properties[objKey{slot: obj.DataSlot, gen: obj.Extra}] = rp
	regMu.Unlock()
	return rp, nil
}

func (rp *ReflectionProperty) Object() types.Value { return rp.obj }
func (rp *ReflectionProperty) GetName() string     { return rp.meta.Name }
func (rp *ReflectionProperty) IsPublic() bool      { return rp.meta.Visibility == VisPublic }
func (rp *ReflectionProperty) IsProtected() bool   { return rp.meta.Visibility == VisProtected }
func (rp *ReflectionProperty) IsPrivate() bool     { return rp.meta.Visibility == VisPrivate }
func (rp *ReflectionProperty) IsStatic() bool      { return rp.meta.IsStatic }
func (rp *ReflectionProperty) IsReadOnly() bool    { return rp.meta.IsReadOnly || (rp.ownerClass != nil && rp.ownerClass.IsReadOnly && !rp.meta.IsStatic) }
func (rp *ReflectionProperty) IsDefault() bool     { return rp.meta.IsDefault }
func (rp *ReflectionProperty) IsPromoted() bool    { return rp.meta.IsPromoted }

func (rp *ReflectionProperty) GetModifiers() int64 {
	mod := rp.meta.Visibility.Modifiers()
	if rp.meta.IsStatic {
		mod |= PropertyIsStatic
	}
	if rp.IsReadOnly() {
		mod |= PropertyIsReadOnly
	}
	return mod
}

func (rp *ReflectionProperty) HasType() bool {
	return rp.meta.Type != nil
}

func (rp *ReflectionProperty) GetType() (*ReflectionType, error) {
	return newReflectionType(rp.z, rp.p, rp.meta.Type)
}

func (rp *ReflectionProperty) HasDefaultValue() bool {
	if rp.meta.HasDefault {
		return true
	}
	return rp.meta.Type == nil && rp.meta.IsDefault
}

func (rp *ReflectionProperty) GetDefaultValue() (types.Value, error) {
	if !rp.HasDefaultValue() {
		return types.NewNull(), nil
	}
	if rp.meta.DefaultString != "" {
		return types.NewString(rp.z, rp.meta.DefaultString, 0)
	}
	if rp.meta.HasDefault {
		return rp.meta.Default, nil
	}
	return types.NewNull(), nil
}

// SetAccessible est un no-op en PHP 8.1+ (toutes les propriétés sont accessibles par réflexion).
func (rp *ReflectionProperty) SetAccessible(_ bool) {}

func (rp *ReflectionProperty) staticKey() string {
	decl := rp.meta.DeclaringClass
	if decl == "" && rp.ownerClass != nil {
		decl = rp.ownerClass.Name
	}
	return strings.ToLower(strings.TrimPrefix(decl, `\`)) + "::$" + rp.meta.Name
}

func (rp *ReflectionProperty) IsInitialized(object ...types.Value) (bool, error) {
	r := getOrCreateRegistry(rp.z)
	if rp.meta.IsStatic {
		skey := rp.staticKey()
		regMu.Lock()
		_, ok := r.staticVals[skey]
		regMu.Unlock()
		if ok {
			return true, nil
		}
		if rp.ownerClass != nil && rp.ownerClass.StaticProps != nil {
			if _, ok := rp.ownerClass.StaticProps[rp.meta.Name]; ok {
				return true, nil
			}
		}
		if rp.meta.HasDefault || rp.meta.Type == nil {
			return true, nil
		}
		return false, nil
	}
	if len(object) == 0 {
		return false, TypeError{Message: "ReflectionProperty::isInitialized(): Argument #1 ($object) must be provided for non-static properties"}
	}
	val := types.Deref(rp.z, object[0])
	if val.Type != types.TypeObject {
		return false, TypeError{Message: "ReflectionProperty::isInitialized(): Argument #1 ($object) must be of type ?object"}
	}
	if rp.ownerClass != nil && rp.ownerClass.PropGetter != nil {
		_, init, err := rp.ownerClass.PropGetter(rp.z, rp.ownerClass, rp.meta, val)
		return init, err
	}
	pkey := propInitKey{slot: val.DataSlot, gen: val.Extra, prop: rp.meta.Name}
	regMu.Lock()
	init := r.propInit[pkey]
	regMu.Unlock()
	if init {
		return true, nil
	}
	slotVal := types.ObjectGet(rp.z, val, rp.meta.Index)
	return !slotVal.IsUndef(), nil
}

func (rp *ReflectionProperty) GetValue(object ...types.Value) (types.Value, error) {
	declName := rp.meta.DeclaringClass
	if declName == "" && rp.ownerClass != nil {
		declName = rp.ownerClass.Name
	}
	r := getOrCreateRegistry(rp.z)
	if rp.meta.IsStatic {
		skey := rp.staticKey()
		regMu.Lock()
		v, ok := r.staticVals[skey]
		regMu.Unlock()
		if ok {
			return v, nil
		}
		if rp.ownerClass != nil && rp.ownerClass.StaticProps != nil {
			if sv, ok := rp.ownerClass.StaticProps[rp.meta.Name]; ok {
				return sv, nil
			}
		}
		if rp.meta.HasDefault {
			return rp.GetDefaultValue()
		}
		if rp.meta.Type == nil {
			return types.NewNull(), nil
		}
		return types.Value{}, Error{Message: fmt.Sprintf("Typed static property %s::$%s must not be accessed before initialization", declName, rp.meta.Name)}
	}
	if len(object) == 0 {
		return types.Value{}, TypeError{Message: "ReflectionProperty::getValue(): Argument #1 ($object) must be provided for non-static properties"}
	}
	obj := types.Deref(rp.z, object[0])
	if obj.Type != types.TypeObject {
		return types.Value{}, TypeError{Message: "ReflectionProperty::getValue(): Argument #1 ($object) must be of type ?object"}
	}
	if rp.ownerClass != nil && rp.ownerClass.PropGetter != nil {
		v, init, err := rp.ownerClass.PropGetter(rp.z, rp.ownerClass, rp.meta, obj)
		if err != nil {
			return types.Value{}, err
		}
		if !init {
			return types.Value{}, Error{Message: fmt.Sprintf("Typed property %s::$%s must not be accessed before initialization", declName, rp.meta.Name)}
		}
		return v, nil
	}
	init, err := rp.IsInitialized(obj)
	if err != nil {
		return types.Value{}, err
	}
	if !init {
		return types.Value{}, Error{Message: fmt.Sprintf("Typed property %s::$%s must not be accessed before initialization", declName, rp.meta.Name)}
	}
	v := types.ObjectGet(rp.z, obj, rp.meta.Index)
	if v.IsUndef() {
		return types.NewNull(), nil
	}
	return v, nil
}

func (rp *ReflectionProperty) SetValue(objectOrValue types.Value, value ...types.Value) error {
	declName := rp.meta.DeclaringClass
	if declName == "" && rp.ownerClass != nil {
		declName = rp.ownerClass.Name
	}
	r := getOrCreateRegistry(rp.z)
	if rp.meta.IsStatic {
		var newVal types.Value
		if len(value) > 0 {
			newVal = value[0]
		} else {
			newVal = objectOrValue
		}
		types.RetainValue(rp.z, newVal)
		skey := rp.staticKey()
		regMu.Lock()
		old, hadOld := r.staticVals[skey]
		r.staticVals[skey] = newVal
		regMu.Unlock()
		if hadOld && !old.IsUndef() {
			types.ReleaseValue(rp.z, &old)
		}
		return nil
	}
	if len(value) == 0 {
		return TypeError{Message: "ReflectionProperty::setValue(): expects exactly 2 arguments for non-static properties"}
	}
	obj := types.Deref(rp.z, objectOrValue)
	if obj.Type != types.TypeObject {
		return TypeError{Message: "ReflectionProperty::setValue(): Argument #1 ($objectOrValue) must be of type object"}
	}
	if rp.IsReadOnly() {
		init, err := rp.IsInitialized(obj)
		if err != nil {
			return err
		}
		if init {
			return Error{Message: fmt.Sprintf("Cannot modify readonly property %s::$%s", declName, rp.meta.Name)}
		}
	}
	if rp.ownerClass != nil && rp.ownerClass.PropSetter != nil {
		if err := rp.ownerClass.PropSetter(rp.z, rp.ownerClass, rp.meta, obj, value[0]); err != nil {
			return err
		}
	} else {
		types.ObjectSet(rp.z, obj, rp.meta.Index, value[0])
	}
	pkey := propInitKey{slot: obj.DataSlot, gen: obj.Extra, prop: rp.meta.Name}
	regMu.Lock()
	r.propInit[pkey] = true
	regMu.Unlock()
	return nil
}

func (rp *ReflectionProperty) GetDeclaringClass() (*ReflectionClass, error) {
	if rp.meta.DeclaringClass != "" && rp.p != nil {
		if dc, ok := rp.p.ResolveClass(rp.meta.DeclaringClass); ok {
			return NewReflectionClass(rp.z, rp.p, dc)
		}
	}
	return NewReflectionClass(rp.z, rp.p, rp.ownerClass)
}

func (rp *ReflectionProperty) GetAttributes(name string, flags int64) []*ReflectionAttribute {
	return filterAttributes(rp.z, rp.p, rp.meta.Attributes, name, flags)
}

func (rp *ReflectionProperty) String() string {
	return fmt.Sprintf("Property [ %s $%s ]\n",
		map[Visibility]string{VisPublic: "public", VisProtected: "protected", VisPrivate: "private"}[rp.meta.Visibility],
		rp.meta.Name)
}

// ReflectionParameter implémente la classe PHP 8.3 ReflectionParameter.
type ReflectionParameter struct {
	z         *zone.Zone
	p         Provider
	obj       types.Value
	declClass *ClassMeta
	funcName  string
	pos       int
	meta      *ParamMeta
	allParams []ParamMeta
}

// NewReflectionParameter construit une instance de ReflectionParameter à partir d'une fonction/méthode et d'un index ou nom.
func NewReflectionParameter(z *zone.Zone, p Provider, function any, param any) (*ReflectionParameter, error) {
	if p == nil {
		p = ActiveProvider(z)
	}
	var params []*ReflectionParameter
	var err error
	switch fn := function.(type) {
	case string:
		if strings.Contains(fn, "::") {
			rm, merr := NewReflectionMethod(z, p, fn)
			if merr != nil {
				return nil, merr
			}
			params, err = rm.GetParameters()
		} else {
			rf, ferr := NewReflectionFunction(z, p, fn)
			if ferr != nil {
				return nil, ferr
			}
			params, err = rf.GetParameters()
		}
	case [2]string:
		rm, merr := NewReflectionMethod(z, p, fn[0], fn[1])
		if merr != nil {
			return nil, merr
		}
		params, err = rm.GetParameters()
	case []string:
		if len(fn) != 2 {
			return nil, TypeError{Message: "ReflectionParameter::__construct(): callable array must have 2 elements"}
		}
		rm, merr := NewReflectionMethod(z, p, fn[0], fn[1])
		if merr != nil {
			return nil, merr
		}
		params, err = rm.GetParameters()
	case *ReflectionFunction:
		params, err = fn.GetParameters()
	case *ReflectionMethod:
		params, err = fn.GetParameters()
	default:
		return nil, TypeError{Message: "ReflectionParameter::__construct(): unsupported function specification"}
	}
	if err != nil {
		return nil, err
	}
	switch pv := param.(type) {
	case int:
		if pv < 0 || pv >= len(params) {
			return nil, ReflectionException{Message: fmt.Sprintf("Parameter #%d does not exist", pv)}
		}
		return params[pv], nil
	case string:
		target := strings.TrimPrefix(pv, "$")
		for _, rp := range params {
			if rp.GetName() == target {
				return rp, nil
			}
		}
		return nil, ReflectionException{Message: fmt.Sprintf("Parameter $%s does not exist", target)}
	default:
		return nil, TypeError{Message: "ReflectionParameter::__construct(): Argument #2 ($param) must be of type int|string"}
	}
}

func newReflectionParameter(z *zone.Zone, p Provider, declClass *ClassMeta, funcName string, pos int, meta *ParamMeta, all []ParamMeta) (*ReflectionParameter, error) {
	obj, err := types.NewObject(z, ClassIDReflectionParameter, 0, false)
	if err != nil {
		return nil, err
	}
	Position := pos
	if meta.Position > 0 && pos == 0 {
		Position = meta.Position
	}
	rp := &ReflectionParameter{
		z:         z,
		p:         p,
		obj:       obj,
		declClass: declClass,
		funcName:  funcName,
		pos:       Position,
		meta:      meta,
		allParams: all,
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.parameters[objKey{slot: obj.DataSlot, gen: obj.Extra}] = rp
	regMu.Unlock()
	return rp, nil
}

func (rp *ReflectionParameter) Object() types.Value        { return rp.obj }
func (rp *ReflectionParameter) GetName() string            { return rp.meta.Name }
func (rp *ReflectionParameter) GetPosition() int           { return rp.pos }
func (rp *ReflectionParameter) HasType() bool              { return rp.meta.Type != nil }
func (rp *ReflectionParameter) GetType() (*ReflectionType, error) {
	return newReflectionType(rp.z, rp.p, rp.meta.Type)
}
func (rp *ReflectionParameter) IsPassedByReference() bool { return rp.meta.ByRef }
func (rp *ReflectionParameter) IsVariadic() bool          { return rp.meta.Variadic }
func (rp *ReflectionParameter) IsPromoted() bool          { return rp.meta.Promoted }

func (rp *ReflectionParameter) AllowsNull() bool {
	if rp.meta.Type == nil {
		return true
	}
	if rp.meta.Type.AllowsNull {
		return true
	}
	if rp.meta.HasDefault && rp.meta.Default.Type == types.TypeNull && rp.meta.DefaultString == "" {
		return true
	}
	return false
}

func (rp *ReflectionParameter) IsOptional() bool {
	if len(rp.allParams) == 0 {
		return rp.meta.HasDefault || rp.meta.Variadic
	}
	for i := rp.pos; i < len(rp.allParams); i++ {
		if !rp.allParams[i].HasDefault && !rp.allParams[i].Variadic {
			return false
		}
	}
	return true
}

func (rp *ReflectionParameter) IsDefaultValueAvailable() bool {
	return rp.meta.HasDefault
}

func (rp *ReflectionParameter) GetDefaultValue() (types.Value, error) {
	if !rp.meta.HasDefault {
		return types.Value{}, ReflectionException{Message: fmt.Sprintf("Internal error: Failed to retrieve the default value of parameter $%s", rp.meta.Name)}
	}
	if rp.meta.DefaultString != "" {
		return types.NewString(rp.z, rp.meta.DefaultString, 0)
	}
	return rp.meta.Default, nil
}

func (rp *ReflectionParameter) GetDeclaringClass() (*ReflectionClass, error) {
	if rp.declClass == nil {
		return nil, nil
	}
	return NewReflectionClass(rp.z, rp.p, rp.declClass)
}

func (rp *ReflectionParameter) GetAttributes(name string, flags int64) []*ReflectionAttribute {
	return filterAttributes(rp.z, rp.p, rp.meta.Attributes, name, flags)
}

func (rp *ReflectionParameter) String() string {
	typePart := ""
	if rp.meta.Type != nil {
		typePart = rp.meta.Type.String() + " "
	}
	reqPart := "<required>"
	if rp.IsOptional() {
		reqPart = "<optional>"
	}
	return fmt.Sprintf("Parameter #%d [ %s %s$%s ]", rp.pos, reqPart, typePart, rp.meta.Name)
}

// ReflectionClassConstant implémente la classe PHP 8.3 ReflectionClassConstant.
type ReflectionClassConstant struct {
	z          *zone.Zone
	p          Provider
	obj        types.Value
	ownerClass *ClassMeta
	meta       *ConstantMeta
	enumCase   *EnumCaseMeta
}

// NewReflectionClassConstant construit une instance de ReflectionClassConstant.
func NewReflectionClassConstant(z *zone.Zone, p Provider, class any, constant string) (*ReflectionClassConstant, error) {
	if p == nil {
		p = ActiveProvider(z)
	}
	rc, err := NewReflectionClass(z, p, class)
	if err != nil {
		return nil, err
	}
	rcc, err := rc.GetReflectionConstant(constant)
	if err != nil {
		return nil, err
	}
	if rcc == nil {
		return nil, ReflectionException{Message: fmt.Sprintf("Constant %s::%s does not exist", rc.GetName(), constant)}
	}
	return rcc, nil
}

func newReflectionClassConstant(z *zone.Zone, p Provider, ownerClass *ClassMeta, meta *ConstantMeta) (*ReflectionClassConstant, error) {
	return newReflectionClassConstantWithClassID(z, p, ClassIDReflectionClassConstant, ownerClass, meta, nil)
}

func newReflectionClassConstantWithClassID(z *zone.Zone, p Provider, classID uint32, ownerClass *ClassMeta, meta *ConstantMeta, enumCase *EnumCaseMeta) (*ReflectionClassConstant, error) {
	obj, err := types.NewObject(z, classID, 0, false)
	if err != nil {
		return nil, err
	}
	if enumCase == nil && ownerClass != nil && ownerClass.IsEnum {
		for i := range ownerClass.EnumCases {
			if ownerClass.EnumCases[i].Name == meta.Name {
				enumCase = &ownerClass.EnumCases[i]
				break
			}
		}
	}
	rcc := &ReflectionClassConstant{
		z:          z,
		p:          p,
		obj:        obj,
		ownerClass: ownerClass,
		meta:       meta,
		enumCase:   enumCase,
	}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.constants[objKey{slot: obj.DataSlot, gen: obj.Extra}] = rcc
	regMu.Unlock()
	return rcc, nil
}

func (rcc *ReflectionClassConstant) Object() types.Value { return rcc.obj }
func (rcc *ReflectionClassConstant) GetName() string     { return rcc.meta.Name }
func (rcc *ReflectionClassConstant) IsPublic() bool      { return rcc.meta.Visibility == VisPublic }
func (rcc *ReflectionClassConstant) IsProtected() bool   { return rcc.meta.Visibility == VisProtected }
func (rcc *ReflectionClassConstant) IsPrivate() bool     { return rcc.meta.Visibility == VisPrivate }
func (rcc *ReflectionClassConstant) IsFinal() bool       { return rcc.meta.IsFinal }
func (rcc *ReflectionClassConstant) IsEnumCase() bool    { return rcc.enumCase != nil }

func (rcc *ReflectionClassConstant) GetModifiers() int64 {
	mod := rcc.meta.Visibility.Modifiers()
	if rcc.meta.IsFinal {
		mod |= ConstantIsFinal
	}
	return mod
}

func (rcc *ReflectionClassConstant) GetValue() (types.Value, error) {
	if rcc.enumCase != nil {
		if !rcc.enumCase.UnitObject.IsUndef() {
			return rcc.enumCase.UnitObject, nil
		}
		classID := uint32(0)
		if rcc.ownerClass != nil {
			classID = rcc.ownerClass.ID
		}
		obj, err := types.NewObject(rcc.z, classID, 2, false)
		if err != nil {
			return types.Value{}, err
		}
		ns, err := types.NewString(rcc.z, rcc.enumCase.Name, 0)
		if err != nil {
			types.ReleaseObject(rcc.z, &obj)
			return types.Value{}, err
		}
		types.ObjectSet(rcc.z, obj, 0, ns)
		types.ReleaseValue(rcc.z, &ns)
		if rcc.enumCase.IsBacked || (rcc.ownerClass != nil && rcc.ownerClass.BackingType != nil) {
			var bv types.Value
			if rcc.enumCase.HasString {
				bv, err = types.NewString(rcc.z, rcc.enumCase.BackingString, 0)
				if err != nil {
					types.ReleaseObject(rcc.z, &obj)
					return types.Value{}, err
				}
			} else {
				bv = rcc.enumCase.BackingValue
			}
			types.ObjectSet(rcc.z, obj, 1, bv)
			types.ReleaseValue(rcc.z, &bv)
		}
		return obj, nil
	}
	if rcc.meta.HasString {
		return types.NewString(rcc.z, rcc.meta.ValueString, 0)
	}
	return rcc.meta.Value, nil
}

func (rcc *ReflectionClassConstant) GetDeclaringClass() (*ReflectionClass, error) {
	if rcc.meta.DeclaringClass != "" && rcc.p != nil {
		if dc, ok := rcc.p.ResolveClass(rcc.meta.DeclaringClass); ok {
			return NewReflectionClass(rcc.z, rcc.p, dc)
		}
	}
	return NewReflectionClass(rcc.z, rcc.p, rcc.ownerClass)
}

func (rcc *ReflectionClassConstant) GetAttributes(name string, flags int64) []*ReflectionAttribute {
	if rcc.enumCase != nil && len(rcc.enumCase.Attributes) > 0 {
		return filterAttributes(rcc.z, rcc.p, rcc.enumCase.Attributes, name, flags)
	}
	return filterAttributes(rcc.z, rcc.p, rcc.meta.Attributes, name, flags)
}

func (rcc *ReflectionClassConstant) String() string {
	return fmt.Sprintf("Constant [ %s %s ]\n",
		map[Visibility]string{VisPublic: "public", VisProtected: "protected", VisPrivate: "private"}[rcc.meta.Visibility],
		rcc.meta.Name)
}

// ReflectionEnum implémente la classe PHP 8.1+ ReflectionEnum (héritant de ReflectionClass).
type ReflectionEnum struct {
	*ReflectionClass
}

// NewReflectionEnum construit une instance de ReflectionEnum.
func NewReflectionEnum(z *zone.Zone, p Provider, objectOrClass any) (*ReflectionEnum, error) {
	if p == nil {
		p = ActiveProvider(z)
	}
	meta, targetObj, hasObj, err := resolveTargetClass(z, p, objectOrClass)
	if err != nil {
		return nil, err
	}
	if !meta.IsEnum {
		return nil, ReflectionException{Message: fmt.Sprintf("Class \"%s\" is not an enum", strings.TrimPrefix(meta.Name, `\`))}
	}
	obj, err := types.NewObject(z, ClassIDReflectionEnum, 0, false)
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
	return &ReflectionEnum{ReflectionClass: rc}, nil
}

func (re *ReflectionEnum) IsBacked() bool {
	return re.meta.BackingType != nil
}

func (re *ReflectionEnum) GetBackingType() (*ReflectionType, error) {
	if re.meta.BackingType == nil {
		return nil, nil
	}
	return newReflectionType(re.z, re.p, re.meta.BackingType)
}

func (re *ReflectionEnum) HasCase(name string) bool {
	for i := range re.meta.EnumCases {
		if re.meta.EnumCases[i].Name == name {
			return true
		}
	}
	return false
}

func (re *ReflectionEnum) GetCase(name string) (*ReflectionEnumUnitCase, error) {
	for i := range re.meta.EnumCases {
		ec := &re.meta.EnumCases[i]
		if ec.Name == name {
			return newEnumCaseInstance(re.z, re.p, re.meta, ec)
		}
	}
	return nil, ReflectionException{Message: fmt.Sprintf("Case %s::%s does not exist", re.GetName(), name)}
}

func (re *ReflectionEnum) GetCases() ([]*ReflectionEnumUnitCase, error) {
	out := make([]*ReflectionEnumUnitCase, 0, len(re.meta.EnumCases))
	for i := range re.meta.EnumCases {
		uc, err := newEnumCaseInstance(re.z, re.p, re.meta, &re.meta.EnumCases[i])
		if err != nil {
			return nil, err
		}
		out = append(out, uc)
	}
	return out, nil
}

// ReflectionEnumUnitCase implémente la classe PHP 8.1+ ReflectionEnumUnitCase (héritant de ReflectionClassConstant).
type ReflectionEnumUnitCase struct {
	*ReflectionClassConstant
}

// ReflectionEnumBackedCase implémente la classe PHP 8.1+ ReflectionEnumBackedCase (héritant de ReflectionEnumUnitCase).
type ReflectionEnumBackedCase struct {
	*ReflectionEnumUnitCase
}

func newEnumCaseInstance(z *zone.Zone, p Provider, ownerClass *ClassMeta, ec *EnumCaseMeta) (*ReflectionEnumUnitCase, error) {
	cmeta := &ConstantMeta{
		Name:           ec.Name,
		DeclaringClass: ownerClass.Name,
		Visibility:     VisPublic,
		Value:          ec.UnitObject,
		Attributes:     ec.Attributes,
	}
	classID := ClassIDReflectionEnumUnitCase
	if ec.IsBacked || ownerClass.BackingType != nil {
		classID = ClassIDReflectionEnumBackedCase
	}
	rcc, err := newReflectionClassConstantWithClassID(z, p, classID, ownerClass, cmeta, ec)
	if err != nil {
		return nil, err
	}
	uc := &ReflectionEnumUnitCase{ReflectionClassConstant: rcc}
	r := getOrCreateRegistry(z)
	regMu.Lock()
	r.unitCases[objKey{slot: rcc.obj.DataSlot, gen: rcc.obj.Extra}] = uc
	regMu.Unlock()
	return uc, nil
}

// NewReflectionEnumUnitCase construit une instance de ReflectionEnumUnitCase.
func NewReflectionEnumUnitCase(z *zone.Zone, p Provider, class any, constant string) (*ReflectionEnumUnitCase, error) {
	re, err := NewReflectionEnum(z, p, class)
	if err != nil {
		return nil, err
	}
	return re.GetCase(constant)
}

// NewReflectionEnumBackedCase construit une instance de ReflectionEnumBackedCase.
func NewReflectionEnumBackedCase(z *zone.Zone, p Provider, class any, constant string) (*ReflectionEnumBackedCase, error) {
	re, err := NewReflectionEnum(z, p, class)
	if err != nil {
		return nil, err
	}
	if !re.IsBacked() {
		return nil, ReflectionException{Message: fmt.Sprintf("Enum %s is not a backed enum", re.GetName())}
	}
	uc, err := re.GetCase(constant)
	if err != nil {
		return nil, err
	}
	return &ReflectionEnumBackedCase{ReflectionEnumUnitCase: uc}, nil
}

func (uc *ReflectionEnumUnitCase) GetEnum() (*ReflectionEnum, error) {
	return NewReflectionEnum(uc.z, uc.p, uc.ownerClass)
}

func (uc *ReflectionEnumUnitCase) GetValue() (types.Value, error) {
	if !uc.enumCase.UnitObject.IsUndef() {
		return uc.enumCase.UnitObject, nil
	}
	obj, err := types.NewObject(uc.z, uc.ownerClass.ID, 2, false)
	if err != nil {
		return types.Value{}, err
	}
	ns, err := types.NewString(uc.z, uc.enumCase.Name, 0)
	if err != nil {
		types.ReleaseObject(uc.z, &obj)
		return types.Value{}, err
	}
	types.ObjectSet(uc.z, obj, 0, ns)
	types.ReleaseValue(uc.z, &ns)
	if uc.enumCase.IsBacked || uc.ownerClass.BackingType != nil {
		bv, berr := uc.GetBackingValue()
		if berr != nil {
			types.ReleaseObject(uc.z, &obj)
			return types.Value{}, berr
		}
		types.ObjectSet(uc.z, obj, 1, bv)
		types.ReleaseValue(uc.z, &bv)
	}
	return obj, nil
}

// GetBackingValue retourne la valeur scalaire (int ou string) adossée au cas d'énumération.
func (uc *ReflectionEnumUnitCase) GetBackingValue() (types.Value, error) {
	if !uc.enumCase.IsBacked && (uc.ownerClass == nil || uc.ownerClass.BackingType == nil) {
		return types.Value{}, ReflectionException{Message: fmt.Sprintf("Enum case %s::%s is not a backed case", uc.ownerClass.Name, uc.enumCase.Name)}
	}
	if uc.enumCase.HasString {
		return types.NewString(uc.z, uc.enumCase.BackingString, 0)
	}
	return uc.enumCase.BackingValue, nil
}
