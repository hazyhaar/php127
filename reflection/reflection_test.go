package reflection

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func runPHPOracle(t *testing.T, code string) string {
	t.Helper()
	cmd := exec.Command("/usr/bin/php", "-n", "-r", code)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("échec oracle /usr/bin/php -n: %v\nsortie: %s", err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func buildTestCatalog(z *zone.Zone) *Catalog {
	cat := NewCatalog()

	// Attributs #[AttrBase] et #[Route(path: '/api', methods: 'GET')]
	cat.RegisterClass(&ClassMeta{
		Name: "App\\Attr\\AttrBase",
		ID:   6001,
	})
	cat.RegisterClass(&ClassMeta{
		Name:       "App\\Attr\\Route",
		ID:         6002,
		ParentName: "App\\Attr\\AttrBase",
		Properties: []PropertyMeta{
			{Name: "path", Index: 0, Visibility: VisPublic, IsPromoted: true, Type: ParseTypeString("string")},
			{Name: "methods", Index: 1, Visibility: VisPublic, IsPromoted: true, Type: ParseTypeString("string"), HasDefault: true, DefaultString: "GET"},
		},
		Methods: []MethodMeta{
			{
				Name:       "__construct",
				Visibility: VisPublic,
				Params: []ParamMeta{
					{Name: "path", Position: 0, Type: ParseTypeString("string"), Promoted: true},
					{Name: "methods", Position: 1, Type: ParseTypeString("string"), Promoted: true, HasDefault: true, DefaultString: "GET"},
				},
				Invoker: func(z *zone.Zone, recv types.Value, args []types.Value) (types.Value, error) {
					if len(args) > 0 {
						types.ObjectSet(z, recv, 0, args[0])
					}
					if len(args) > 1 {
						types.ObjectSet(z, recv, 1, args[1])
					}
					return types.NewNull(), nil
				},
			},
		},
	})

	// Interfaces et Trait
	cat.RegisterClass(&ClassMeta{
		Name:        "App\\Contract\\Identifiable",
		ID:          6003,
		IsInterface: true,
		Methods: []MethodMeta{
			{Name: "getId", Visibility: VisPublic, IsAbstract: true, ReturnType: ParseTypeString("int|string")},
		},
	})
	cat.RegisterClass(&ClassMeta{
		Name:        "App\\Contract\\Auditable",
		ID:          6004,
		IsInterface: true,
	})
	cat.RegisterClass(&ClassMeta{
		Name:    "App\\Trait\\TimestampTrait",
		ID:      6005,
		IsTrait: true,
	})

	// Classe parente abstraite BaseEntity
	cat.RegisterClass(&ClassMeta{
		Name:       "App\\Domain\\BaseEntity",
		ID:         6006,
		IsAbstract: true,
		Interfaces: []string{"App\\Contract\\Identifiable"},
		Constants: []ConstantMeta{
			{Name: "VERSION", Visibility: VisPublic, Value: types.NewLong(1)},
			{Name: "INTERNAL_SECRET", Visibility: VisPrivate, Value: types.NewLong(99)},
			{Name: "FINAL_TAG", Visibility: VisProtected, IsFinal: true, HasString: true, ValueString: "v1"},
		},
		Properties: []PropertyMeta{
			{Name: "id", Index: 0, Visibility: VisProtected, IsDefault: true, Type: ParseTypeString("int|string"), HasDefault: true, Default: types.NewLong(0)},
			{Name: "secretBase", Index: 1, Visibility: VisPrivate, IsDefault: true, HasDefault: true, Default: types.NewLong(7)},
		},
		Methods: []MethodMeta{
			{Name: "getId", Visibility: VisPublic, ReturnType: ParseTypeString("int|string"), Invoker: func(z *zone.Zone, recv types.Value, _ []types.Value) (types.Value, error) {
				return types.ObjectGet(z, recv, 0), nil
			}},
			{Name: "validate", Visibility: VisProtected, IsAbstract: true, ReturnType: ParseTypeString("bool")},
		},
	})

	// Classe concrète UserEntity
	pathVal, _ := types.NewString(z, "/users", 0)
	methodVal, _ := types.NewString(z, "POST", 0)
	cat.RegisterClass(&ClassMeta{
		Name:       "App\\Domain\\UserEntity",
		ID:         6007,
		ParentName: "App\\Domain\\BaseEntity",
		Interfaces: []string{"App\\Contract\\Auditable"},
		Traits:     []string{"App\\Trait\\TimestampTrait"},
		IsFinal:    true,
		Attributes: []AttributeMeta{
			{
				Name: "App\\Attr\\Route",
				Args: []AttributeArg{
					{Name: "", Value: pathVal},
					{Name: "methods", Value: methodVal},
				},
			},
		},
		Properties: []PropertyMeta{
			{Name: "id", Index: 0, DeclaringClass: "App\\Domain\\BaseEntity", Visibility: VisProtected, IsDefault: true, Type: ParseTypeString("int|string"), HasDefault: true, Default: types.NewLong(0)},
			{Name: "name", Index: 2, Visibility: VisPublic, IsDefault: true, IsPromoted: true, Type: ParseTypeString("string")},
			{Name: "token", Index: 3, Visibility: VisPublic, IsDefault: true, IsReadOnly: true, Type: ParseTypeString("?string")},
			{Name: "counter", Visibility: VisPublic, IsStatic: true, IsDefault: true, Type: ParseTypeString("int"), HasDefault: true, Default: types.NewLong(10)},
		},
		Methods: []MethodMeta{
			{
				Name:       "__construct",
				Visibility: VisPublic,
				Params: []ParamMeta{
					{Name: "id", Position: 0, Type: ParseTypeString("int|string")},
					{Name: "name", Position: 1, Type: ParseTypeString("string"), Promoted: true, HasDefault: true, DefaultString: "anon"},
				},
				Invoker: func(z *zone.Zone, recv types.Value, args []types.Value) (types.Value, error) {
					if len(args) > 0 {
						types.ObjectSet(z, recv, 0, args[0])
					}
					if len(args) > 1 {
						types.ObjectSet(z, recv, 2, args[1])
					} else {
						sv, err := types.NewString(z, "anon", 0)
						if err == nil {
							types.ObjectSet(z, recv, 2, sv)
							types.ReleaseValue(z, &sv)
						}
					}
					r := getOrCreateRegistry(z)
					regMu.Lock()
					r.propInit[propInitKey{slot: recv.DataSlot, gen: recv.Extra, prop: "id"}] = true
					r.propInit[propInitKey{slot: recv.DataSlot, gen: recv.Extra, prop: "name"}] = true
					regMu.Unlock()
					return types.NewNull(), nil
				},
			},
			{
				Name:       "validate",
				Visibility: VisProtected,
				ReturnType: ParseTypeString("bool"),
				Invoker: func(_ *zone.Zone, _ types.Value, _ []types.Value) (types.Value, error) {
					return types.NewBool(true), nil
				},
			},
			{
				Name:       "compute",
				Visibility: VisPublic,
				IsStatic:   true,
				IsFinal:    true,
				ReturnType: ParseTypeString("(App\\Contract\\Identifiable&App\\Contract\\Auditable)|null"),
				Params: []ParamMeta{
					{Name: "ref", Position: 0, Type: ParseTypeString("?int"), ByRef: true},
					{Name: "items", Position: 1, Type: ParseTypeString("string"), Variadic: true},
				},
				Invoker: func(_ *zone.Zone, _ types.Value, args []types.Value) (types.Value, error) {
					if len(args) > 0 && args[0].Type == types.TypeLong {
						return types.NewLong(args[0].ToLong() * 2), nil
					}
					return types.NewNull(), nil
				},
			},
		},
	})

	// Enums PHP 8.1+ (unitaire et adossée)
	cat.RegisterClass(&ClassMeta{
		Name:   "App\\Domain\\Suit",
		ID:     6008,
		IsEnum: true,
		EnumCases: []EnumCaseMeta{
			{Name: "Hearts"},
			{Name: "Spades"},
		},
	})
	cat.RegisterClass(&ClassMeta{
		Name:        "App\\Domain\\Status",
		ID:          6009,
		IsEnum:      true,
		BackingType: ParseTypeString("string"),
		EnumCases: []EnumCaseMeta{
			{Name: "Draft", IsBacked: true, HasString: true, BackingString: "draft"},
			{Name: "Published", IsBacked: true, HasString: true, BackingString: "pub"},
		},
	})

	// Fonction libre
	cat.RegisterFunction(&FunctionMeta{
		Name:       "App\\Util\\format_tag",
		ReturnType: ParseTypeString("string"),
		Params: []ParamMeta{
			{Name: "prefix", Position: 0, Type: ParseTypeString("string")},
			{Name: "code", Position: 1, Type: ParseTypeString("int"), HasDefault: true, Default: types.NewLong(42)},
		},
		Invoker: func(z *zone.Zone, args []types.Value) (types.Value, error) {
			p := "tag"
			if len(args) > 0 && args[0].Type == types.TypeString {
				p = strings.Clone(types.StringData(z, args[0]))
			}
			c := int64(42)
			if len(args) > 1 && args[1].Type == types.TypeLong {
				c = args[1].ToLong()
			}
			return types.NewString(z, fmt.Sprintf("%s:%d", p, c), 0)
		},
	})

	return cat
}

func TestReflectionDifferentialModifiersAndClassHierarchy(t *testing.T) {
	z := zone.New(1024 * 1024)

	cat := buildTestCatalog(z)
	BindProvider(z, cat)
	defer z.Reset(CleanupDispatcher(z))

	phpCode := `
namespace App\Contract {
	interface Identifiable { public function getId(): int|string; }
	interface Auditable {}
}
namespace App\Trait {
	trait TimestampTrait {}
}
namespace App\Domain {
	abstract class BaseEntity implements \App\Contract\Identifiable {
		public const VERSION = 1;
		private const INTERNAL_SECRET = 99;
		protected final const FINAL_TAG = 'v1';
		protected int|string $id = 0;
		private $secretBase = 7;
		public function getId(): int|string { return $this->id; }
		abstract protected function validate(): bool;
	}
	final class UserEntity extends BaseEntity implements \App\Contract\Auditable {
		use \App\Trait\TimestampTrait;
		public ?string $token;
		public static int $counter = 10;
		public function __construct(int|string $id, public string $name = 'anon') {}
		protected function validate(): bool { return true; }
		public static final function compute(?int &$ref, string ...$items): (\App\Contract\Identifiable&\App\Contract\Auditable)|null { return null; }
	}
	$rc = new \ReflectionClass(UserEntity::class);
	$rb = new \ReflectionClass(BaseEntity::class);
	echo implode('|', [
		$rc->getName(),
		$rc->getShortName(),
		$rc->getNamespaceName(),
		$rc->inNamespace() ? '1' : '0',
		$rc->isFinal() ? '1' : '0',
		$rc->isAbstract() ? '1' : '0',
		$rc->isInstantiable() ? '1' : '0',
		$rc->getModifiers(),
		$rb->isAbstract() ? '1' : '0',
		$rb->isInstantiable() ? '1' : '0',
		$rb->getModifiers(),
		$rc->getParentClass()->getName(),
		$rc->isSubclassOf(BaseEntity::class) ? '1' : '0',
		$rc->implementsInterface(\App\Contract\Identifiable::class) ? '1' : '0',
		$rc->implementsInterface(\App\Contract\Auditable::class) ? '1' : '0',
		implode(',', $rc->getTraitNames()),
		count($rc->getConstants()),
		count($rb->getConstants()),
	]);
}
`
	expected := runPHPOracle(t, phpCode)

	rc, err := NewReflectionClass(z, cat, `App\Domain\UserEntity`)
	if err != nil {
		t.Fatalf("NewReflectionClass UserEntity: %v", err)
	}
	rb, err := NewReflectionClass(z, cat, `App\Domain\BaseEntity`)
	if err != nil {
		t.Fatalf("NewReflectionClass BaseEntity: %v", err)
	}
	parent, err := rc.GetParentClass()
	if err != nil || parent == nil {
		t.Fatalf("GetParentClass: %v", err)
	}
	subBase, _ := rc.IsSubclassOf(`App\Domain\BaseEntity`)
	implIdent, _ := rc.ImplementsInterface(`App\Contract\Identifiable`)
	implAudit, _ := rc.ImplementsInterface(`App\Contract\Auditable`)
	rcConsts, _ := rc.GetReflectionConstants()
	rbConsts, _ := rb.GetReflectionConstants()

	b2s := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}

	got := strings.Join([]string{
		rc.GetName(),
		rc.GetShortName(),
		rc.GetNamespaceName(),
		b2s(rc.InNamespace()),
		b2s(rc.IsFinal()),
		b2s(rc.IsAbstract()),
		b2s(rc.IsInstantiable()),
		fmt.Sprintf("%d", rc.GetModifiers()),
		b2s(rb.IsAbstract()),
		b2s(rb.IsInstantiable()),
		fmt.Sprintf("%d", rb.GetModifiers()),
		parent.GetName(),
		b2s(subBase),
		b2s(implIdent),
		b2s(implAudit),
		strings.Join(rc.GetTraitNames(), ","),
		fmt.Sprintf("%d", len(rcConsts)),
		fmt.Sprintf("%d", len(rbConsts)),
	}, "|")

	if got != expected {
		t.Fatalf("parité différentielle ReflectionClass rompue:\ngot:  %s\nwant: %s", got, expected)
	}
}

func TestReflectionMethodFunctionAndParameterDifferential(t *testing.T) {
	z := zone.New(1024 * 1024)

	cat := buildTestCatalog(z)
	BindProvider(z, cat)
	defer z.Reset(CleanupDispatcher(z))

	phpCode := `
namespace App\Contract {
	interface Identifiable {}
	interface Auditable {}
}
namespace App\Domain {
	class UserEntity {
		public function __construct(int|string $id, public string $name = 'anon') {}
		public static final function compute(?int &$ref, string ...$items): (\App\Contract\Identifiable&\App\Contract\Auditable)|null { return null; }
	}
	$rm = new \ReflectionMethod(UserEntity::class, 'compute');
	$ctor = (new \ReflectionClass(UserEntity::class))->getConstructor();
	$p0 = $ctor->getParameters()[0];
	$p1 = $ctor->getParameters()[1];
	$cp0 = $rm->getParameters()[0];
	$cp1 = $rm->getParameters()[1];
	echo implode('|', [
		$rm->getName(),
		$rm->isPublic() ? '1' : '0',
		$rm->isStatic() ? '1' : '0',
		$rm->isFinal() ? '1' : '0',
		$rm->isVariadic() ? '1' : '0',
		$rm->getModifiers(),
		$rm->getNumberOfParameters(),
		$rm->getNumberOfRequiredParameters(),
		(string)$rm->getReturnType(),
		$ctor->isConstructor() ? '1' : '0',
		$ctor->getNumberOfRequiredParameters(),
		$p0->getName() . ':' . $p0->getPosition() . ':' . ($p0->isOptional() ? '1' : '0') . ':' . ($p0->isPromoted() ? '1' : '0') . ':' . (string)$p0->getType(),
		$p1->getName() . ':' . $p1->getPosition() . ':' . ($p1->isOptional() ? '1' : '0') . ':' . ($p1->isPromoted() ? '1' : '0') . ':' . $p1->getDefaultValue(),
		$cp0->isPassedByReference() ? '1' : '0',
		$cp0->allowsNull() ? '1' : '0',
		$cp1->isVariadic() ? '1' : '0',
		$cp1->isOptional() ? '1' : '0',
	]);
}
`
	expected := runPHPOracle(t, phpCode)

	rm, err := NewReflectionMethod(z, cat, `App\Domain\UserEntity::compute`)
	if err != nil {
		t.Fatalf("NewReflectionMethod: %v", err)
	}
	rc, _ := NewReflectionClass(z, cat, `App\Domain\UserEntity`)
	ctor, err := rc.GetConstructor()
	if err != nil || ctor == nil {
		t.Fatalf("GetConstructor: %v", err)
	}
	ctorParams, _ := ctor.GetParameters()
	compParams, _ := rm.GetParameters()
	retType, _ := rm.GetReturnType()
	p0Type, _ := ctorParams[0].GetType()
	p1Def, _ := ctorParams[1].GetDefaultValue()
	defer types.ReleaseValue(z, &p1Def)

	b2s := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}

	got := strings.Join([]string{
		rm.GetName(),
		b2s(rm.IsPublic()),
		b2s(rm.IsStatic()),
		b2s(rm.IsFinal()),
		b2s(rm.IsVariadic()),
		fmt.Sprintf("%d", rm.GetModifiers()),
		fmt.Sprintf("%d", rm.GetNumberOfParameters()),
		fmt.Sprintf("%d", rm.GetNumberOfRequiredParameters()),
		retType.String(),
		b2s(ctor.IsConstructor()),
		fmt.Sprintf("%d", ctor.GetNumberOfRequiredParameters()),
		fmt.Sprintf("%s:%d:%s:%s:%s", ctorParams[0].GetName(), ctorParams[0].GetPosition(), b2s(ctorParams[0].IsOptional()), b2s(ctorParams[0].IsPromoted()), p0Type.String()),
		fmt.Sprintf("%s:%d:%s:%s:%s", ctorParams[1].GetName(), ctorParams[1].GetPosition(), b2s(ctorParams[1].IsOptional()), b2s(ctorParams[1].IsPromoted()), types.StringData(z, p1Def)),
		b2s(compParams[0].IsPassedByReference()),
		b2s(compParams[0].AllowsNull()),
		b2s(compParams[1].IsVariadic()),
		b2s(compParams[1].IsOptional()),
	}, "|")

	if got != expected {
		t.Fatalf("parité différentielle Method/Parameter rompue:\ngot:  %s\nwant: %s", got, expected)
	}

	// Test d'invocation de fonction libre et de méthode statique
	rf, err := NewReflectionFunction(z, cat, `App\Util\format_tag`)
	if err != nil {
		t.Fatalf("NewReflectionFunction: %v", err)
	}
	argPfx, _ := types.NewString(z, "lot", 0)
	defer types.ReleaseValue(z, &argPfx)
	resFn, err := rf.Invoke(argPfx, types.NewLong(54))
	if err != nil {
		t.Fatalf("ReflectionFunction.Invoke: %v", err)
	}
	defer types.ReleaseValue(z, &resFn)
	if gotFn := types.StringData(z, resFn); gotFn != "lot:54" {
		t.Fatalf("ReflectionFunction.Invoke = %q, attendu \"lot:54\"", gotFn)
	}

	resMeth, err := rm.Invoke(types.NewNull(), types.NewLong(21))
	if err != nil {
		t.Fatalf("ReflectionMethod.Invoke: %v", err)
	}
	if resMeth.ToLong() != 42 {
		t.Fatalf("ReflectionMethod.Invoke = %d, attendu 42", resMeth.ToLong())
	}
}

func TestReflectionPropertyLifecycleAndReadonlyDifferential(t *testing.T) {
	z := zone.New(1024 * 1024)

	cat := buildTestCatalog(z)
	BindProvider(z, cat)
	defer z.Reset(CleanupDispatcher(z))

	phpCode := `
namespace App\Domain {
	class UserEntity {
		protected int|string $id = 0;
		public string $name;
		public readonly ?string $token;
		public static int $counter = 10;
	}
	$rc = new \ReflectionClass(UserEntity::class);
	$obj = $rc->newInstanceWithoutConstructor();
	$pId = $rc->getProperty('id');
	$pName = $rc->getProperty('name');
	$pToken = $rc->getProperty('token');
	$pCounter = $rc->getProperty('counter');

	$out = [];
	$out[] = $pId->isInitialized($obj) ? '1' : '0';
	$out[] = $pId->getValue($obj);
	$out[] = $pName->isInitialized($obj) ? '1' : '0';
	try {
		$pName->getValue($obj);
	} catch (\Error $e) {
		$out[] = $e->getMessage();
	}
	$pName->setValue($obj, 'alice');
	$out[] = $pName->isInitialized($obj) ? '1' : '0';
	$out[] = $pName->getValue($obj);

	$out[] = $pToken->isReadOnly() ? '1' : '0';
	$out[] = $pToken->getModifiers();
	$pToken->setValue($obj, 'tok-1');
	$out[] = $pToken->getValue($obj);
	try {
		$pToken->setValue($obj, 'tok-2');
	} catch (\Error $e) {
		$out[] = $e->getMessage();
	}

	$out[] = $pCounter->getValue();
	$pCounter->setValue(null, 25);
	$out[] = $pCounter->getValue();
	echo implode('|', $out);
}
`
	expected := runPHPOracle(t, phpCode)

	rc, err := NewReflectionClass(z, cat, `App\Domain\UserEntity`)
	if err != nil {
		t.Fatalf("NewReflectionClass: %v", err)
	}
	obj, err := rc.NewInstanceWithoutConstructor()
	if err != nil {
		t.Fatalf("NewInstanceWithoutConstructor: %v", err)
	}
	defer types.ReleaseObject(z, &obj)

	pId, _ := rc.GetProperty("id")
	pName, _ := rc.GetProperty("name")
	pToken, _ := rc.GetProperty("token")
	pCounter, _ := rc.GetProperty("counter")

	b2s := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}

	var out []string
	initId, _ := pId.IsInitialized(obj)
	out = append(out, b2s(initId))
	valId, _ := pId.GetValue(obj)
	out = append(out, fmt.Sprintf("%d", valId.ToLong()))

	initName, _ := pName.IsInitialized(obj)
	out = append(out, b2s(initName))
	if _, err := pName.GetValue(obj); err != nil {
		out = append(out, err.Error())
	}

	alice, _ := types.NewString(z, "alice", 0)
	defer types.ReleaseValue(z, &alice)
	if err := pName.SetValue(obj, alice); err != nil {
		t.Fatalf("pName.SetValue: %v", err)
	}
	initNameAfter, _ := pName.IsInitialized(obj)
	out = append(out, b2s(initNameAfter))
	valName, _ := pName.GetValue(obj)
	out = append(out, strings.Clone(types.StringData(z, valName)))

	out = append(out, b2s(pToken.IsReadOnly()))
	out = append(out, fmt.Sprintf("%d", pToken.GetModifiers()))
	tok1, _ := types.NewString(z, "tok-1", 0)
	defer types.ReleaseValue(z, &tok1)
	if err := pToken.SetValue(obj, tok1); err != nil {
		t.Fatalf("pToken.SetValue initial: %v", err)
	}
	valTok, _ := pToken.GetValue(obj)
	out = append(out, strings.Clone(types.StringData(z, valTok)))
	tok2, _ := types.NewString(z, "tok-2", 0)
	defer types.ReleaseValue(z, &tok2)
	if err := pToken.SetValue(obj, tok2); err != nil {
		out = append(out, err.Error())
	}

	cBefore, _ := pCounter.GetValue()
	out = append(out, fmt.Sprintf("%d", cBefore.ToLong()))
	if err := pCounter.SetValue(types.NewNull(), types.NewLong(25)); err != nil {
		t.Fatalf("pCounter.SetValue: %v", err)
	}
	cAfter, _ := pCounter.GetValue()
	out = append(out, fmt.Sprintf("%d", cAfter.ToLong()))

	got := strings.Join(out, "|")
	if got != expected {
		t.Fatalf("parité différentielle ReflectionProperty rompue:\ngot:  %s\nwant: %s", got, expected)
	}
}

func TestReflectionEnumAndAttributesDifferential(t *testing.T) {
	z := zone.New(1024 * 1024)

	cat := buildTestCatalog(z)
	BindProvider(z, cat)
	defer z.Reset(CleanupDispatcher(z))

	phpCode := `
namespace App\Attr {
	#[\Attribute] class AttrBase {}
	#[\Attribute] class Route extends AttrBase {
		public function __construct(public string $path, public string $methods = 'GET') {}
	}
}
namespace App\Domain {
	enum Suit { case Hearts; case Spades; }
	enum Status: string { case Draft = 'draft'; case Published = 'pub'; }
	#[\App\Attr\Route('/users', methods: 'POST')]
	class UserEntity {}

	$reSuit = new \ReflectionEnum(Suit::class);
	$reStatus = new \ReflectionEnum(Status::class);
	$caseDraft = $reStatus->getCase('Draft');
	$rcUser = new \ReflectionClass(UserEntity::class);
	$attrsAll = $rcUser->getAttributes();
	$attrsSub = $rcUser->getAttributes(\App\Attr\AttrBase::class, \ReflectionAttribute::IS_INSTANCEOF);
	$routeInst = $attrsAll[0]->newInstance();

	echo implode('|', [
		$reSuit->isBacked() ? '1' : '0',
		$reSuit->hasCase('Hearts') ? '1' : '0',
		count($reSuit->getCases()),
		$reStatus->isBacked() ? '1' : '0',
		(string)$reStatus->getBackingType(),
		$caseDraft->getName(),
		$caseDraft->isEnumCase() ? '1' : '0',
		$caseDraft->getBackingValue(),
		count($attrsAll),
		count($attrsSub),
		$attrsAll[0]->getName(),
		$routeInst->path . ':' . $routeInst->methods,
	]);
}
`
	expected := runPHPOracle(t, phpCode)

	reSuit, err := NewReflectionEnum(z, cat, `App\Domain\Suit`)
	if err != nil {
		t.Fatalf("NewReflectionEnum Suit: %v", err)
	}
	reStatus, err := NewReflectionEnum(z, cat, `App\Domain\Status`)
	if err != nil {
		t.Fatalf("NewReflectionEnum Status: %v", err)
	}
	suitCases, _ := reSuit.GetCases()
	bType, _ := reStatus.GetBackingType()
	caseDraft, err := NewReflectionEnumBackedCase(z, cat, `App\Domain\Status`, "Draft")
	if err != nil {
		t.Fatalf("NewReflectionEnumBackedCase: %v", err)
	}
	backVal, err := caseDraft.GetBackingValue()
	if err != nil {
		t.Fatalf("GetBackingValue: %v", err)
	}
	defer types.ReleaseValue(z, &backVal)

	rcUser, _ := NewReflectionClass(z, cat, `App\Domain\UserEntity`)
	attrsAll := rcUser.GetAttributes("", 0)
	attrsSub := rcUser.GetAttributes(`App\Attr\AttrBase`, AttributeIsInstanceOf)
	routeInst, err := attrsAll[0].NewInstance()
	if err != nil {
		t.Fatalf("ReflectionAttribute.NewInstance: %v", err)
	}
	defer types.ReleaseObject(z, &routeInst)

	ro, err := NewReflectionObject(z, cat, routeInst)
	if err != nil {
		t.Fatalf("NewReflectionObject: %v", err)
	}
	pPath, _ := ro.GetProperty("path")
	pMethods, _ := ro.GetProperty("methods")
	vPath, _ := pPath.GetValue(routeInst)
	vMethods, _ := pMethods.GetValue(routeInst)

	b2s := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}

	got := strings.Join([]string{
		b2s(reSuit.IsBacked()),
		b2s(reSuit.HasCase("Hearts")),
		fmt.Sprintf("%d", len(suitCases)),
		b2s(reStatus.IsBacked()),
		bType.String(),
		caseDraft.GetName(),
		b2s(caseDraft.IsEnumCase()),
		strings.Clone(types.StringData(z, backVal)),
		fmt.Sprintf("%d", len(attrsAll)),
		fmt.Sprintf("%d", len(attrsSub)),
		attrsAll[0].GetName(),
		strings.Clone(types.StringData(z, vPath)) + ":" + strings.Clone(types.StringData(z, vMethods)),
	}, "|")

	if got != expected {
		t.Fatalf("parité différentielle Enum/Attribute rompue:\ngot:  %s\nwant: %s", got, expected)
	}
}

func TestZoneResetIsolationAndZeroLeak(t *testing.T) {
	z := zone.New(1024 * 1024)

	for cycle := range 5 {
		cat := buildTestCatalog(z)
		BindProvider(z, cat)

		rc, err := NewReflectionClass(z, cat, `App\Domain\UserEntity`)
		if err != nil {
			t.Fatalf("cycle %d: NewReflectionClass: %v", cycle, err)
		}
		idVal := types.NewLong(int64(100 + cycle))
		nameVal, err := types.NewString(z, fmt.Sprintf("user-%d", cycle), 0)
		if err != nil {
			t.Fatalf("cycle %d: NewString: %v", cycle, err)
		}
		inst, err := rc.NewInstance(idVal, nameVal)
		types.ReleaseValue(z, &nameVal)
		if err != nil {
			t.Fatalf("cycle %d: NewInstance: %v", cycle, err)
		}
		ro, err := NewReflectionObject(z, cat, inst)
		if err != nil {
			t.Fatalf("cycle %d: NewReflectionObject: %v", cycle, err)
		}
		if ro.GetName() != `App\Domain\UserEntity` {
			t.Fatalf("cycle %d: GetName = %q", cycle, ro.GetName())
		}
		pCounter, err := ro.GetProperty("counter")
		if err != nil {
			t.Fatalf("cycle %d: GetProperty counter: %v", cycle, err)
		}
		if err := pCounter.SetValue(types.NewNull(), types.NewLong(int64(500+cycle))); err != nil {
			t.Fatalf("cycle %d: SetValue static: %v", cycle, err)
		}
		types.ReleaseObject(z, &inst)

		z.Reset(CleanupDispatcher(z))

		regMu.Lock()
		_, stillBound := regsByZ[z]
		regMu.Unlock()
		if stillBound {
			t.Fatalf("cycle %d: le registre de réflexion n'a pas été purgé après z.Reset()", cycle)
		}
	}
}

func TestReflectionCompleteSurfaceAndEdgeCases(t *testing.T) {
	z := zone.New(1024 * 1024)
	cat := buildTestCatalog(z)
	BindProvider(z, cat)
	defer z.Reset(CleanupDispatcher(z))

	rc, err := NewReflectionClass(z, nil, `App\Domain\UserEntity`)
	if err != nil {
		t.Fatalf("NewReflectionClass via ActiveProvider: %v", err)
	}
	rb, _ := NewReflectionClass(z, nil, `App\Domain\BaseEntity`)
	if _, err := rb.NewInstance(); err == nil {
		t.Fatalf("instanciation d'une classe abstraite aurait dû échouer")
	}

	staticMethods, err := rc.GetMethods(MethodIsStatic)
	if err != nil || len(staticMethods) != 1 || staticMethods[0].GetName() != "compute" {
		t.Fatalf("GetMethods(MethodIsStatic) = %v, err=%v", staticMethods, err)
	}
	roProps, err := rc.GetProperties(PropertyIsReadOnly)
	if err != nil || len(roProps) != 1 || roProps[0].GetName() != "token" {
		t.Fatalf("GetProperties(PropertyIsReadOnly) = %v, err=%v", roProps, err)
	}
	finalConsts, err := rb.GetReflectionConstants(ConstantIsFinal)
	if err != nil || len(finalConsts) != 1 || finalConsts[0].GetName() != "FINAL_TAG" {
		t.Fatalf("GetReflectionConstants(ConstantIsFinal) = %v, err=%v", finalConsts, err)
	}

	rpDirect, err := NewReflectionProperty(z, nil, `App\Domain\UserEntity`, "$token")
	if err != nil || rpDirect.GetName() != "token" {
		t.Fatalf("NewReflectionProperty: %v", err)
	}
	rparamDirect, err := NewReflectionParameter(z, nil, [2]string{`App\Domain\UserEntity`, "compute"}, "items")
	if err != nil || !rparamDirect.IsVariadic() {
		t.Fatalf("NewReflectionParameter: %v", err)
	}
	rccDirect, err := NewReflectionClassConstant(z, nil, `App\Domain\BaseEntity`, "FINAL_TAG")
	if err != nil || !rccDirect.IsFinal() || !rccDirect.IsProtected() {
		t.Fatalf("NewReflectionClassConstant: %v", err)
	}
	ucDirect, err := NewReflectionEnumUnitCase(z, nil, `App\Domain\Suit`, "Hearts")
	if err != nil || !ucDirect.IsEnumCase() {
		t.Fatalf("NewReflectionEnumUnitCase: %v", err)
	}
	ucVal, err := ucDirect.GetValue()
	if err != nil || ucVal.Type != types.TypeObject {
		t.Fatalf("ReflectionEnumUnitCase.GetValue: %v", err)
	}
	types.ReleaseObject(z, &ucVal)

	attrs := rc.GetAttributes("", 0)
	argsArr, err := attrs[0].GetArguments()
	if err != nil {
		t.Fatalf("ReflectionAttribute.GetArguments: %v", err)
	}
	if types.ArrayLen(z, argsArr) != 2 {
		t.Fatalf("ArrayLen(GetArguments) = %d, attendu 2", types.ArrayLen(z, argsArr))
	}
	types.ReleaseArray(z, &argsArr)

	reflectors := []Reflector{
		rc,
		staticMethods[0],
		rpDirect,
		rparamDirect,
		rccDirect,
		ucDirect,
		attrs[0],
	}
	for i, r := range reflectors {
		if r.String() == "" {
			t.Fatalf("reflector[%d].String() vide", i)
		}
	}
}
