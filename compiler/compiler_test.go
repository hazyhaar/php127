package compiler_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/sapi"
	"code.hazyhaar.fr/devhoros/php127/vm"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func TestDifferential(t *testing.T) {
	data, err := os.ReadFile("/usr/bin/php")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != "1c564e6bafa56d03359c3774be6f4c77d0edecabd2075e18556940a56747c8d7" {
		t.Fatal("oracle fingerprint")
	}
	cases := []struct{ name, src string }{
		{"lot34-generator-rewind-closed", `function g(){yield 1;}$g=g();$g->next();try{$g->rewind();}catch(Exception $e){echo $e->getMessage();}`},
		{"lot34-generator-foreach-rewind", `function g(){yield 1;yield 2;}$g=g();$g->next();try{foreach($g as $v)echo $v;}catch(Exception $e){echo $e->getMessage();}`},
		{"lot34-globals-snapshot", `$x=1;$a=$GLOBALS;$x=2;echo $a["x"],$GLOBALS["x"];function f(){echo $GLOBALS["x"];}$f="f";$f();`},
		{"lot34-prop-writer-strict", `class C{public int $x;}$c=new C;try{eval('declare(strict_types=1);$c->x="3";');}catch(TypeError $e){echo "strict";}$c->x="4";echo $c->x;`},
		{"lot34-prop-writer-weak", `declare(strict_types=1);class C{public int $x;}$c=new C;eval('$c->x="3";');echo $c->x;`},
		{"lot34-type-dnf", `interface I{}interface J{}class A implements I,J{}function f((I&J)|int $v):mixed{return $v;}echo f(new A) instanceof A;echo f(2);`},
		{"lot34-enum-type-strict", `declare(strict_types=1);enum E:string{case A="1";}try{E::tryFrom(true);}catch(TypeError $e){echo $e->getMessage();}`},
		{"lot34-callback-dynamic", `function f(callable $cb){echo $cb("abc");}f("strlen");class C{static function f(){return 4;}}$cb="C::f";echo $cb();`},
		{"lot34-callback-intrinsic", `var_dump(is_callable("call_user_func"),function_exists("enum_exists"),function_exists("is_callable"));`},
		{"lot34-type-missing-return", `function f():?int{}try{f();}catch(TypeError $e){echo $e->getMessage();}`},
		{"lot34-generator-frame-depth", `function g($n){yield $n;if($n)yield from g($n-1);}foreach(g(40) as $v)echo $v,",";`},
		{"lot34-type-loss", `function f(int $x){echo $x;}f(2.5);f("3.5");`},
		{"lot34-type-large", `function f(int $x){echo $x;}f("9223372036854775807");`},
		{"lot34-type-never", `function f():never{}try{f();}catch(TypeError $e){echo $e->getMessage();}`},
		{"lot34-globals-dynamic-bind", `function a(){$GLOBALS["created"]=4;}function b(){global $created;echo $created;}a();b();`},
		{"lot34-globals-nested", `$a=[1];$GLOBALS["a"][0]=4;echo $a[0];`},
		{"lot34-prop-array-write", `class C{public array $a=[1];}$c=new C;$c->a[0]=3;echo $c->a[0];`},
		{"lot34-generator-delegate-send", `function a(){$v=yield 1;yield $v;return 7;}function b(){return yield from a();}$g=b();echo $g->current(),$g->send(3);$g->next();echo $g->getReturn();`},
		{"lot34-generator-interleaved", `function g($n){yield $n;yield $n+1;}$a=g(1);$b=g(5);echo $a->current(),$b->current();$a->next();echo $a->current(),$b->current();`},
		{"lot34-generator-rewind", `function g(){yield 1;yield 2;}$g=g();$g->rewind();echo $g->current();$g->next();try{$g->rewind();}catch(Exception $e){echo $e->getMessage();}`},
		{"lot34-generator-caught", `function g(){yield 1;throw new Exception("x");}$g=g();try{foreach($g as $v)echo $v;}catch(Exception $e){echo $e->getMessage();}echo "end";`},
		{"lot34-generator-autoprime", `function g(){try{throw new Exception("x");}finally{return 42;}yield;}$g=g();echo $g->getReturn();`},
		{"lot34-generator-failed-return", `function g(){try{return 42;}finally{throw new Exception("x");}yield;}$g=g();try{$g->getReturn();}catch(Exception $e){echo $e->getMessage();}try{$g->getReturn();}catch(Exception $e){echo $e->getMessage();}`},
		{"lot34-generator-destroy", `function g(){try{yield "x";}finally{echo "f";}}$g=g();echo $g->current();unset($g);echo "e";`},
		{"lot34-generator-pending", `function f($x,$y){echo $x,$y;}function g(){f(2,yield 3);yield 4;}$g=g();echo $g->current();echo $g->send(5);`},
		{"lot34-type-files", `declare(strict_types=1);eval('function f(int $x):int{return "4";}');try{f("2");}catch(TypeError $e){echo "strict";}echo f(2);`},
		{"lot34-type-files-return", `eval('declare(strict_types=1);function f(int $x):int{return "4";}');try{f("2");}catch(TypeError $e){echo "return";}`},
		{"lot34-type-self", `class A{function f(self $x):self{return $x;}}class B extends A{function g(parent $x):static{return $this;}}$b=new B;echo $b->g(new A)->f($b) instanceof B;`},
		{"lot34-type-callable", `function f(callable $cb,iterable $x):array{return [$cb($x[0])];}$cb=fn($n)=>$n+1;var_dump(f($cb,[2]));`},
		{"lot34-type-reference", `function f(int &$x){$x++;}$x="2";f($x);var_dump($x);`},
		{"lot34-globals-empty", `$x=0;var_dump(empty($GLOBALS["x"]));$x=1;var_dump(empty($GLOBALS["x"]));`},
		{"lot34-type-coerce", `function f(int $a,float $b,string $c,bool $d){var_dump($a,$b,$c,$d);}f("12",2,3,1);`},
		{"lot34-type-strict", `declare(strict_types=1);function f(int $x){echo $x;}try{f("2");}catch(TypeError $e){echo "type";}f(3);`},
		{"lot34-type-widen", `declare(strict_types=1);function f(float $x):float{return $x;}var_dump(f(2));`},
		{"lot34-type-union", `function f(int|string $x){var_dump($x);}f("12");f(3);f(true);`},
		{"lot34-type-null", `function f(?int $x):?int{return $x;}var_dump(f(null),f("2"));`},
		{"lot34-type-return", `function f():int{return "3";}var_dump(f());`},
		{"lot34-type-return-strict", `declare(strict_types=1);function f():int{return "3";}try{f();}catch(TypeError $e){echo "type";}`},
		{"lot34-type-void", `function f():void{echo "v";}var_dump(f());`},
		{"lot34-type-intersection", `interface A{}interface B{}class C implements A,B{}function f(A&B $x):object{return $x;}echo f(new C) instanceof C;`},
		{"lot34-type-literals", `function f(true $a,false $b,null $c):mixed{return [$a,$b,$c];}var_dump(f(true,false,null));`},
		{"lot34-prop-default", `class C{public int $x=42;public $s="ok";function __construct(){echo $this->x,$this->s;}}$c=new C;echo $c->x;`},
		{"lot34-prop-array", `class A{public $a=[1,"x"=>[2]];}class B extends A{public $b=3;}$a=new B;$b=new B;echo $a->a["x"][0],$b->a[0],$a->b;`},
		{"lot34-prop-uninit", `class C{public int $x;}$c=new C;try{echo $c->x;}catch(Error $e){echo $e->getMessage();}$c->x="4";var_dump($c->x);`},
		{"lot34-prop-strict", `declare(strict_types=1);class C{public int $x;}$c=new C;try{$c->x="2";}catch(TypeError $e){echo "type";}`},
		{"lot34-prop-static", `class C{public static string $s="ok";public static $n=1;}echo C::$s;C::$n+=2;echo C::$n++,C::$n;`},
		{"lot34-prop-inherit", `class A{public static $n=1;static function f(){echo self::$n;}}class B extends A{static function g(){self::$n++;echo parent::$n,static::$n;}}B::g();echo A::$n;A::f();`},
		{"lot34-const", `class C{const X=123;const A=[1,"k"=>2];}echo C::X,C::A["k"];`},
		{"lot34-enum-unit", `enum Suit{case Hearts;case Spades;}echo Suit::Hearts->name;echo Suit::Hearts===Suit::Hearts;foreach(Suit::cases() as $s)echo $s->name;`},
		{"lot34-enum-string", `enum Suit:string{case Hearts="H";case Spades="S";}echo Suit::Hearts->value,Suit::from("S")->name;var_dump(Suit::tryFrom("X"));`},
		{"lot34-enum-int", `enum Code:int{case A=1;case B=2;}echo Code::from("2")->name;try{Code::from(3);}catch(ValueError $e){echo "value";}`},
		{"lot34-enum-method", `enum E{case A;function f(){return $this->name;}static function g(){return self::A;}}echo E::g()->f();var_dump(enum_exists("e"),class_exists("E"));`},
		{"lot34-enum-readonly", `enum E{case A;}$e=E::A;try{$e->name="B";}catch(Error $e){echo $e->getMessage();}`},
		{"lot34-globals", `$x=1;echo $GLOBALS["x"];$GLOBALS["x"]=3;echo $x;$x=4;echo $GLOBALS["x"];`},
		{"lot34-globals-function", `$x=1;function f(){$GLOBALS["x"]++;$GLOBALS["y"]=7;}f();echo $x,$y;`},
		{"lot34-globals-unset", `$x=1;unset($GLOBALS["x"]);var_dump(isset($x));$GLOBALS["x"]=2;echo $x;`},
		{"lot34-callback-function", `function f($a,$b){return $a+$b;}echo call_user_func("f",2,3),call_user_func_array("f",[4,5]);`},
		{"lot34-callback-method", `class C{function f($x){return $x+1;}static function g($x){return $x+2;}}echo call_user_func([new C,"f"],2),call_user_func(["C","g"],3),call_user_func("C::g",4);`},
		{"lot34-callback-closure", `$f=fn($x)=>$x+1;echo call_user_func($f,4);var_dump(is_callable($f),is_callable("strlen"),is_callable("missing"));`},
		{"lot34-generator-lazy", `function g(){echo "start";yield 2;echo "end";}$g=g();echo "before";foreach($g as $v)echo $v;`},
		{"lot34-generator-keys", `function g(){yield "a"=>2;yield 4=>3;yield 7;}foreach(g() as $k=>$v)echo $k,":",$v,";";`},
		{"lot34-generator-methods", `function g(){yield 3;yield 4;return 8;}$g=g();var_dump($g->valid(),$g->current(),$g->key());$g->next();echo $g->current();$g->next();var_dump($g->valid(),$g->getReturn());`},
		{"lot34-generator-send", `function g(){$x=yield 1;yield $x;}$g=g();echo $g->current(),$g->send(8);`},
		{"lot34-generator-array", `function g(){yield from ["a"=>2,4];}foreach(g() as $k=>$v)echo $k,$v;`},
		{"lot34-generator-delegate", `function a(){yield 1;return 7;}function b(){$x=yield from a();yield $x;}foreach(b() as $v)echo $v;`},
		{"lot34-generator-finally", `function g(){try{yield 1;return 3;}finally{echo "f";}}$g=g();foreach($g as $v)echo $v;echo $g->getReturn();`},
		{"lot34-generator-object", `class C{function g($x){yield $x;yield 2;}}foreach((new C)->g(4) as $v)echo $v;`},
		{"static-closures", `function make(){return function(){static $x=0;echo ++$x;};}$a=make();$b=make();$a();$a();$b();`},
		{"argument-lifetime", `class C{function __destruct(){echo "d";}}function f($x){$x=null;echo "f";}f(new C);`},
		{"include-once", `echo include_once "/devhoros/php127/docs/lot2_1_astra/php-src-php-8.3.6/tests/lang/015.inc";echo include_once "/devhoros/php127/docs/lot2_1_astra/php-src-php-8.3.6/tests/lang/015.inc";`},
		{"named", `function f($a,$b=2){echo $a,$b;}f(b:4,a:3);f(a:5);`},
		{"named-default", `function f($a=1,$b=2){echo $a,$b;}f(b:4);`},
		{"named-method", `class C{function f($a,$b){echo $a,$b;}}(new C)->f(b:4,a:3);`},
		{"list", `$a=[1,2,3];[$x,,$z]=$a;echo $x,$z;list('k'=>$v)=['k'=>4];echo $v;`},
		{"compound-dim", `$a=[2];$i=0;echo $a[$i++]+=3;echo $a[0],$i;`},
		{"increment-dim", `$a=[2];$i=0;echo $a[$i++]++,$a[0],$i,++$a[0];`},
		{"compound-prop", `class C{public $x;} $c=new C;$c->x=2;echo $c->x+=3;echo $c->x++,$c->x;`},
		{"intrinsic-define", `echo defined("PHP_INT_MAX"),defined("x");define("x",[3,4]);echo defined("x"),constant("x")[1],x[0];`},
		{"intrinsic-args", `function f($a,$b=4){$a=8;echo func_num_args();var_dump(func_get_args());echo func_get_arg(0);}f(1);f(1,2,3);`},
		{"intrinsic-exists", `function f(){} class C{} interface I{} trait T{}var_dump(function_exists("F"),function_exists("STRLEN"),function_exists("nothing"),class_exists("C"),interface_exists("I"),trait_exists("T"),class_exists("I"));`},
		{"global-bind", `$x=3;function f(){global $x;$x++;}f();echo $x;`},
		{"global-create", `function f(){global $x;$x=4;}f();echo $x;`},
		{"global-unset", `$x=3;function f(){global $x;unset($x);$x=8;}f();echo $x;`},
		{"eval-scope", `function f(){ $x=3;echo eval('$x+=2;return $x;');echo $x;}f();`},
		{"eval-function", `eval('function f($x){return $x+1;}');echo f(4);`},
		{"eval-repeat", `$x=1;for($i=0;$i<3;$i++)eval('$x++;');echo $x;`},
		{"intrinsic-reporting", `echo error_reporting(123),":",error_reporting(),":",error_reporting(null);`},
		{"intrinsic-constants", `echo PHP_INT_MAX,":",PHP_INT_MIN,":",PHP_INT_SIZE,PHP_EOL,STR_PAD_LEFT,STR_PAD_RIGHT,STR_PAD_BOTH,E_ALL,E_WARNING,E_NOTICE,E_DEPRECATED; echo is_infinite(INF),is_nan(NAN),M_PI;`},
		{"unknown-function", `try{unknown_function();}catch(Error $e){echo $e->getMessage();}`},
		{"static-local", `function f(){static $x=0;echo ++$x;} f();f();f();`},
		{"static-recursive", `function f($n){static $x=0;$x++;if($n)f($n-1);echo $x;}f(3);f(0);`},
		{"static-initializer", `function init(){echo "i";return 4;}function f(){static $x=init();echo ++$x;}f();f();`},
		{"static-unset", `function f(){static $x=0;echo ++$x;unset($x);}f();f();`},
		{"native-case", `echo StRlEn("abc"), strtoupper("abc"), count([1,2]);`},
		{"native-uncaught", `str_repeat("a",-1);`},
		{"native-reference", `$a="abc";$b=&$a;echo strlen($b),$a;`},
		{"native-type", `try{strlen([]);}catch(TypeError $e){echo $e->getMessage();}`},
		{"native-value", `try{str_repeat("a",-1);}catch(ValueError $e){echo $e->getMessage();}`},
		{"native-arity", `try{strlen();}catch(ArgumentCountError $e){echo $e->getMessage();}`},
		{"native-division", `try{intdiv(1,0);}catch(DivisionByZeroError $e){echo $e->getMessage();}`},
		{"native-arithmetic", `try{intdiv(-9223372036854775807-1,-1);}catch(ArithmeticError $e){echo $e->getMessage();}`},
		{"echo", `echo "hello\n";`},
		{"arithmetic", `echo (2+3)*4-5, "\n";`},
		{"division", `echo 7/2, "\n";`},
		{"modulo", `echo 17%5;`},
		{"power", `echo 2**3**2;`},
		{"bits", `echo (3<<2)|1;`},
		{"concat", `echo "a" . 2 . "b";`},
		{"compare", `echo 2<3,2===2,2!=="2",3<=>2;`},
		{"assign", `$a=2; $b=$a=4; echo $a,$b;`},
		{"compound", `$a=2; $a+=3; $a*=4; $a.="!"; echo $a;`},
		{"increments", `$a=3; echo $a++,$a,++$a,--$a,$a--;`},
		{"if", `$x=3; if($x==1)echo 1; elseif($x==3)echo 3; else echo 2;`},
		{"while", `$i=0; while($i<4){echo $i++;}`},
		{"do", `$i=0; do {echo ++$i;} while($i<3);`},
		{"for", `for($i=0,$j=4;$i<4;$i++,$j--)echo $i,$j;`},
		{"break", `for($i=0;$i<5;$i++){if($i==3)break; echo $i;}`},
		{"continue", `for($i=0;$i<4;$i++){if($i==2)continue; echo $i;}`},
		{"levels", `for($i=0;$i<3;$i++){for($j=0;$j<3;$j++){if($j==1)continue 2; echo $i,$j;}}`},
		{"switch", `$a="2"; switch($a){case 1:echo 1;break;case 2:echo 2;case 3:echo 3;break;default:echo 4;}`},
		{"and", `$a=0; echo false && ++$a; echo $a;`},
		{"or", `$a=0; echo true || ++$a; echo $a;`},
		{"ternary", `echo true?"yes":"no", false?1:2;`},
		{"elvis", `echo 0?:3, 4?:5;`},
		{"coalesce", `$a=null; echo $a??4; $a??=5; echo $a;`},
		{"array", `$a=[2,"x"=>3]; echo $a[0],$a["x"];`},
		{"array-write", `$a=[1]; echo $a[0]=3; $a[]=4; echo $a[0],$a[1];`},
		{"array-nested", `$a=[[1]]; $a[0][0]=4; echo $a[0][0];`},
		{"array-rhs", `$a=[1,2]; $a[0]=($a[1]=3); echo $a[0],$a[1];`},
		{"interpolation", `$a=3;echo "x=$a\n";`},
		{"reference", `$a=1; $b=&$a; $b=4; echo $a;`},
		{"foreach-ref", `$a=[1,2]; foreach($a as &$v){$v*=2;} unset($v); echo $a[0],$a[1];`},
		{"function", `function f($a,$b=2){return $a+$b;} echo f(3);`},
		{"recursion", `function f($n){if($n<2)return 1;return $n*f($n-1);} echo f(6);`},
		{"parameter-ref", `function f(&$x){$x+=2;} $a=3; f($a); echo $a;`},
		{"variadic", `function f(...$xs){return $xs[1];} echo f(2,3);`},
		{"closure", `$x=3; $f=function($y)use($x){return $x+$y;}; echo $f(4);`},
		{"closure-ref", `$x=3; $f=function()use(&$x){$x++;}; $f();echo $x;`},
		{"arrow", `$x=3; $f=fn($y)=>$x+$y;echo $f(4);`},
		{"match", `$a=2;echo match($a){1=>"a",2,3=>"b",default=>"c"};`},
		{"division-error", `echo 1/0;`},
		{"type-error", `throw new TypeError("type");`},
		{"match-error", `echo match(42){1=>2};`},
		{"catch", `try {throw new Exception("x");}catch(Exception $e){echo $e->getMessage();}`},
		{"finally", `function f(){try{return 3;}finally{echo "f";}} echo f();`},
		{"nested-finally", `function f(){try{try{return 2;}finally{echo "a";}}finally{echo "b";}}echo f();`},
		{"break-finally", `while(true){try{break;}finally{echo "f";}}echo "e";`},
		{"isset", `$a=null;echo isset($a),empty($a);$a=3;echo isset($a),empty($a);`},
		{"unset", `$a=[1,2];unset($a[0]);echo isset($a[0]),$a[1];`},
		{"exit", `echo "a";exit(7);echo "b";`},
		{"print", `echo print "a";`},
		{"class", `class C{public $x; function __construct($x){$this->x=$x;} function f(){return $this->x;}} $c=new C(4);echo $c->f();`},
		{"inheritance", `class A{function f(){return 3;}} class B extends A{} echo (new B)->f();`},
		{"late-static", `class A{static function f(){echo static::class;}} class B extends A{} B::f();`},
		{"trait", `trait T{function f(){return 4;}} class A{use T;}echo (new A)->f();`},
		{"interface", `interface I{function f();} class C implements I{function f(){return 5;}} echo (new C)->f();`},
		{"destructor", `class C{function __destruct(){echo "d";}} $c=new C; echo "a";`},
		{"clone", `class C{public $x;} $a=new C;$a->x=3;$b=clone $a;$b->x=4;echo $a->x,$b->x;`},
		{"instanceof", `class A{} class B extends A{} $b=new B;echo $b instanceof A;`},
		{"foreach-value", `foreach([2,3] as $k=>$v){echo $k,$v;}`},
		{"foreach-break", `foreach([1,2] as $v){echo $v;break;}echo "e";`},
		{"lot63-pcre", `$c=0;$r=preg_replace('/(\d+)/','[$1]','a12b34',-1,$c);preg_match('/(\w+)=(\d+)/','k=42',$m);preg_match_all('/\d+/','1 22 333',$all,PREG_PATTERN_ORDER);echo $r,":",$c,":",$m[1],"=",$m[2],":",implode(",",$all[0]),":",preg_quote("a.b+c","/"),":",implode("|",preg_split('/\s+/','x  y z')),":",implode(",",preg_grep('/^\d+$/',['12','ab','34']));`},
		{"lot63-hash-sapi", `echo hash("sha256","horos"),":",hash_hmac("sha256","msg","secret"),":",hash_equals("abc","abc")?"1":"0",":",in_array("sha256",hash_algos())?"1":"0";$u=parse_url("https://u:p@example.com:8080/p/q?a=1&b=2#frag");parse_str($u["query"],$q);echo ":",$u["host"],":",parse_url("https://example.com:8080/x",PHP_URL_PORT),":",http_build_query($q,"","&",PHP_QUERY_RFC3986);`},
		{"lot63-date", `$gm=gmmktime(12,30,45,6,15,2025);echo $gm,":",gmdate("Y-m-d H:i:s",$gm),":",gmdate(DATE_ATOM,$gm),":",strtotime("2025-06-15T12:30:45Z"),":",(time()>1700000000)?"1":"0",":",is_float(microtime(true))?"1":"0",":",is_int(hrtime(true))?"1":"0";`},
		{"lot63-streams", `$f=fopen("php://memory","r+");fwrite($f,"alpha\nbeta\ngamma");rewind($f);$l1=trim(fgets($f));$pos=ftell($f);fseek($f,0,SEEK_SET);$all=stream_get_contents($f);fclose($f);$tmp=tempnam(sys_get_temp_dir(),"php127_");file_put_contents($tmp,"hello_stream",LOCK_EX);echo $l1,":",$pos,":",strlen($all),":",file_get_contents($tmp),":",is_file($tmp)?"1":"0",":",filesize($tmp);unlink($tmp);echo ":",file_exists($tmp)?"1":"0";`},
		{"lot63-spl-stack-queue", `$s=new SplStack();$s->push("a");$s->push("b");$s->push("c");$out=[];foreach($s as $k=>$v){$out[]="$k:$v";}$q=new SplQueue();$q->enqueue(10);$q->enqueue(20);echo count($s),":",$s->top(),":",$s->bottom(),":",implode(",",$out),":",$s->pop(),":",$q->dequeue(),":",count($q),":",($s instanceof Countable)?"1":"0",":",($s instanceof Iterator)?"1":"0";`},
		{"lot63-spl-fixed-storage-arrayobj", `$fa=SplFixedArray::fromArray([10,20,30]);$fa->setSize(4);$fa[3]=40;$s=new SplObjectStorage();$o1=new stdClass();$o2=new stdClass();$s[$o1]="meta1";$s->attach($o2,"meta2");$ao=new ArrayObject(["b"=>2,"a"=>1],ArrayObject::ARRAY_AS_PROPS);$ao->ksort();$keys=[];foreach($ao as $k=>$v){$keys[]="$k=$v";}echo count($fa),":",implode(",",$fa->toArray()),":",count($s),":",$s->contains($o1)?"1":"0",":",$s[$o1],":",$ao->a,":",implode("&",$keys);`},
		{"lot63-fiber", `$f=new Fiber(function(int $x): int { $y = Fiber::suspend($x + 1); return $y * 2; });echo $f->isStarted()?"1":"0",":",$f->start(10),":",$f->isSuspended()?"1":"0",":",$f->resume(20),":",$f->isTerminated()?"1":"0",":",$f->getReturn();`},
		{"lot63-interfaces-user", `class MyCount implements Countable, IteratorAggregate, ArrayAccess { public $d = ["x"=>10,"y"=>20]; public function count(): int { return count($this->d); } public function getIterator(): Traversable { return new ArrayIterator($this->d); } public function offsetExists(mixed $k): bool { return isset($this->d[$k]); } public function offsetGet(mixed $k): mixed { return $this->d[$k]; } public function offsetSet(mixed $k, mixed $v): void { $this->d[$k] = $v; } public function offsetUnset(mixed $k): void { $d = $this->d; unset($d[$k]); $this->d = $d; } } $m = new MyCount(); $m["z"] = 30; unset($m["x"]); $out=[]; foreach($m as $k=>$v){$out[]="$k:$v";} echo count($m),":",isset($m["y"])?"1":"0",":",$m["z"],":",implode(",",$out);`},
		{"audit-constat1-prop-dtor-control", `class D { function __destruct() {} } $p = new stdClass(); $p->a = new D(); $p->a = 1; var_dump($p->a);`},
		{"audit-constat2-foreach-dtor-realloc", `class BigDtor { function __destruct() { $x0=0;$x1=1;$x2=2;$x3=3;$x4=4;$x5=5;$x6=6;$x7=7;$x8=8;$x9=9;$x10=10;$x11=11;$x12=12;$x13=13;$x14=14;$x15=15;$x16=16;$x17=17;$x18=18;$x19=19;$x20=20;$x21=21;$x22=22;$x23=23;$x24=24;$x25=25;$x26=26;$x27=27;$x28=28;$x29=29;$x30=30;$x31=31;$x32=32;$x33=33;$x34=34;$x35=35;$x36=36;$x37=37;$x38=38;$x39=39;$x40=40;$x41=41;$x42=42;$x43=43;$x44=44;$x45=45;$x46=46;$x47=47;$x48=48;$x49=49;$x50=50;$x51=51;$x52=52;$x53=53;$x54=54;$x55=55;$x56=56;$x57=57;$x58=58;$x59=59;$x60=60;$x61=61;$x62=62;$x63=63;$x64=64;$x65=65;$x66=66;$x67=67;$x68=68;$x69=69; } } $obj = new BigDtor(); foreach ([10, 20] as $obj) { var_dump($obj); }`},
		{"audit-constat3-shift-right-64-neg", `$a = -8; $b = 64; var_dump($a >> $b, -8 >> 64, 8 >> 64, -8 << 64);`},
		{"audit-constat4-func-get-args-unset", `function f($x) { unset($x); var_dump(func_get_args(), func_get_arg(0)); } f(10);`},
		{"audit-constat5-isset-empty-globals", `var_dump(isset($GLOBALS), empty($GLOBALS));`},
		{"audit-vague2-constat1-string-scalar-dim-assign", `$s = ""; $s[0] = "A"; var_dump($s); $s2 = "abc"; $s2[1] = "X"; var_dump($s2); try { $s3 = ""; $s3[] = "A"; } catch (Error $e) { echo $e->getMessage(), "\n"; } try { $n = 42; $n[0] = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; } try { $n = 42; $n[] = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }`},
		{"audit-vague2-constat2-dtor-array-prop-overwrite", `$a = ["a"]; $b = $a; $b[0] .= "b"; var_dump($a[0], $b[0]); class DProp { public $t; function __construct($t) { $this->t = $t; } function __destruct() { $this->t->p = "from_dtor"; } } $c = new stdClass(); $c->p = new DProp($c); $c->p = "new_val"; var_dump($c->p); class DArr { function __destruct() { $x0=0;$x1=1;$x2=2;$x3=3;$x4=4;$x5=5;$x6=6;$x7=7;$x8=8;$x9=9;$x10=10;$x11=11;$x12=12;$x13=13;$x14=14;$x15=15;$x16=16;$x17=17;$x18=18;$x19=19;$x20=20;$x21=21;$x22=22;$x23=23;$x24=24;$x25=25;$x26=26;$x27=27;$x28=28;$x29=29;$x30=30;$x31=31;$x32=32;$x33=33;$x34=34;$x35=35;$x36=36;$x37=37;$x38=38;$x39=39;$x40=40;$x41=41;$x42=42;$x43=43;$x44=44;$x45=45;$x46=46;$x47=47;$x48=48;$x49=49;$x50=50;$x51=51;$x52=52;$x53=53;$x54=54;$x55=55;$x56=56;$x57=57;$x58=58;$x59=59;$x60=60;$x61=61;$x62=62;$x63=63;$x64=64;$x65=65;$x66=66;$x67=67;$x68=68;$x69=69; global $g; $g = ["from_dtor"]; } } $g = [new DArr()]; $g[0] = "new_val"; var_dump($g);`},
		{"audit-vague2-constat3-assign-dim-op-parity", `$a = [2]; $a[0] += 3; $m = [[4]]; $m[0][0] *= 5; var_dump($a[0], $m[0][0]);`},
		{"audit-vague2-constat4-assign-op-caught-typeerror", `$a = [0 => 10]; try { $a[0] += []; } catch (TypeError $e) { echo "caught1:"; } var_dump($a[0]); class C { public $p = 10; } $c = new C(); try { $c->p += []; } catch (TypeError $e) { echo "caught2:"; } var_dump($c->p); try { $c->p += []; } catch (TypeError $e) { echo "caught3:"; } var_dump($c->p);`},
		{"audit-vague2-constat5-prop-ic-unset", `error_reporting(0); class C { public $p = 1; } function addP($c, $v) { $c->p += $v; } $c1 = new C(); addP($c1, 3); $c2 = new C(); unset($c2->p); addP($c2, 5); var_dump($c1->p, $c2->p);`},
		{"audit-vague3-b1a-child-exception-props", `class BaseEx extends Exception { public $a = 10; } class ChildEx extends BaseEx { public $b = 20; } $e = new ChildEx("m"); echo $e->a . ":" . $e->b . "\n";`},
		{"audit-vague3-b1b-redeclared-prop-reflection", `class Base { public $a = 1; public $b = 2; } class Child extends Base { public $a = 10; } $rp = new ReflectionProperty(Base::class, "a"); var_dump($rp->getValue(new Child()));`},
		{"audit-vague3-b2-catch-arg-count-error", `function f($a, $b) { return $a + $b; } try { f(1); echo "after\n"; } catch (ArgumentCountError $e) { echo "caught\n"; }`},
		{"audit-vague3-b3-propfetch-dtor-exception", `class A { public static $n = 0; public $x = 42; function __destruct() { throw new Exception("boom_" . self::$n++); } } function test() { for ($i = 0; $i < 2; $i++) { try { $v = (new A())->x; } catch (Exception $e) { echo "caught:" . $e->getMessage() . "\n"; } } } test();`},
		{"audit-vague3-b4-eval-superglobals", `eval('echo gettype($_SERVER), "\n";');`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", tc.src)
			var wantOut, wantErr bytes.Buffer
			cmd.Stdout = &wantOut
			cmd.Stderr = &wantErr
			err := cmd.Run()
			if err != nil && cmd.ProcessState == nil {
				t.Fatal(err)
			}
			prog, err := compiler.CompileSource("<?php "+tc.src, "Command line code")
			if err != nil {
				t.Fatal(err)
			}
			z := zone.New(zone.DefaultCapacity)
			var gotOut, gotErr bytes.Buffer
			code := vm.Execute(z, &gotOut, &gotErr, prog, vm.NewContext())
			if code != cmd.ProcessState.ExitCode() || gotOut.String() != wantOut.String() || gotErr.String() != wantErr.String() {
				t.Fatalf("got (%q,%q,%d), want (%q,%q,%d)", gotOut.String(), gotErr.String(), code, wantOut.String(), wantErr.String(), cmd.ProcessState.ExitCode())
			}
		})
	}
}

func TestUnsupportedIsExplicit(t *testing.T) {
	for _, src := range []string{
		"echo `date`;",
	} {
		prog, err := compiler.CompileSource("<?php "+src, "unsupported.php")
		if prog != nil || err == nil || !strings.Contains(err.Error(), "fonctionnalite absente:") {
			t.Errorf("%s: program=%v error=%v", src, prog, err)
		}
	}
}

func TestWideFrameAndExactStack(t *testing.T) {
	var src strings.Builder
	src.WriteString("<?php ")
	for i := range 200 {
		fmt.Fprintf(&src, "$x%d=%d;", i, i)
	}
	src.WriteString("echo $x199 + (2*3);")
	prog, err := compiler.CompileSource(src.String(), "wide.php")
	if err != nil {
		t.Fatal(err)
	}
	if prog.Main.NumCVs != 200 || prog.Main.MaxStack != 3 {
		t.Fatalf("frame: CVs=%d stack=%d", prog.Main.NumCVs, prog.Main.MaxStack)
	}
	var out bytes.Buffer
	if code := vm.Execute(zone.New(zone.DefaultCapacity), &out, nil, prog, vm.NewContext()); code != 0 || out.String() != "205" {
		t.Fatalf("output=%q", out.String())
	}
}

func TestLot63MBStringAndPDO(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"lot63-mbstring", `echo mb_strlen("héllo 😊"),":",mb_substr("héllo 😊",1,4),":",mb_strpos("héllo 😊","😊"),":",mb_strrpos("héllo é","é"),":",mb_strtoupper("éàç"),":",mb_strtolower("ÉÀÇ"),":",mb_check_encoding("héllo","UTF-8")?"1":"0";`},
		{"lot63-pdo-consts", `echo PDO::FETCH_ASSOC,":",PDO::FETCH_NUM,":",PDO::FETCH_BOTH,":",PDO::FETCH_OBJ,":",PDO::FETCH_COLUMN,":",PDO::PARAM_INT,":",PDO::PARAM_STR,":",PDO::ERRMODE_EXCEPTION,":",class_exists("PDO")?"1":"0",":",class_exists("PDOStatement")?"1":"0",":",class_exists("PDOException")?"1":"0";`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-r", tc.src)
			var wantOut, wantErr bytes.Buffer
			cmd.Stdout = &wantOut
			cmd.Stderr = &wantErr
			err := cmd.Run()
			if err != nil && cmd.ProcessState == nil {
				t.Fatal(err)
			}
			prog, err := compiler.CompileSource("<?php "+tc.src, "Command line code")
			if err != nil {
				t.Fatal(err)
			}
			z := zone.New(zone.DefaultCapacity)
			var gotOut, gotErr bytes.Buffer
			code := vm.Execute(z, &gotOut, &gotErr, prog, vm.NewContext())
			if code != cmd.ProcessState.ExitCode() || gotOut.String() != wantOut.String() || gotErr.String() != wantErr.String() {
				t.Fatalf("got (%q,%q,%d), want (%q,%q,%d)", gotOut.String(), gotErr.String(), code, wantOut.String(), wantErr.String(), cmd.ProcessState.ExitCode())
			}
		})
	}
	t.Run("lot63-pdo-sqlite", func(t *testing.T) {
		src := `$pdo=new PDO("sqlite::memory:");$pdo->exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, qty INTEGER)");$pdo->beginTransaction();echo $pdo->inTransaction()?"1":"0",":";$stmt=$pdo->prepare("INSERT INTO items (name, qty) VALUES (:name, :qty)");$stmt->execute([":name"=>"pomme",":qty"=>5]);$id1=$pdo->lastInsertId();$n="poire";$q=8;$stmt->bindParam(":name",$n);$stmt->bindParam(":qty",$q,PDO::PARAM_INT);$stmt->execute();$n="banane";$q=12;$stmt->execute();$pdo->commit();echo $pdo->inTransaction()?"1":"0",":",$id1,":";$q1=$pdo->query("SELECT name, qty FROM items ORDER BY id");$r1=$q1->fetch(PDO::FETCH_ASSOC);$r2=$q1->fetch(PDO::FETCH_OBJ);$r3=$q1->fetch(PDO::FETCH_NUM);echo $r1["name"],"=",$r1["qty"],":",$r2->name,"=",$r2->qty,":",$r3[0],"=",$r3[1],":";$q2=$pdo->query("SELECT name FROM items ORDER BY id");echo implode(",",$q2->fetchAll(PDO::FETCH_COLUMN,0)),":";$q3=$pdo->query("SELECT COUNT(*) FROM items");echo $q3->fetchColumn(0),":";$rows=[];foreach($pdo->query("SELECT name FROM items ORDER BY id") as $row){$rows[]=$row["name"];}echo implode("|",$rows),":";try{$pdo->exec("SELECT * FROM nonexistent_table");}catch(PDOException $e){echo ($e instanceof RuntimeException)?"1":"0";}`
		prog, err := compiler.CompileSource("<?php "+src, "Command line code")
		if err != nil {
			t.Fatal(err)
		}
		z := zone.New(zone.DefaultCapacity)
		var gotOut, gotErr bytes.Buffer
		code := vm.Execute(z, &gotOut, &gotErr, prog, vm.NewContext())
		want := "1:0:1:pomme=5:poire=8:banane=12:pomme,poire,banane:3:pomme|poire|banane:1"
		if code != 0 || gotOut.String() != want || gotErr.String() != "" {
			t.Fatalf("got (%q,%q,%d), want (%q,%q,0)", gotOut.String(), gotErr.String(), code, want, "")
		}
	})
}

func TestLot65ExtensionsAndStdlib(t *testing.T) {
	sessDir := t.TempDir()
	for _, tc := range []struct{ name, src string }{
		{
			"lot65-datetime-oop",
			`$tz = new DateTimeZone("UTC");
$dt = new DateTimeImmutable("2026-10-14 12:30:00", $tz);
$dt2 = $dt->add(new DateInterval("P2DT3H"));
$diff = $dt->diff($dt2);
$iv = DateInterval::createFromDateString("1 day");
$period = new DatePeriod($dt, $iv, 3);
$dates = [];
foreach ($period as $d) { $dates[] = $d->format("Y-m-d"); }
$mut = date_create("2026-01-01 00:00:00", $tz);
$mut->modify("+5 days");
echo $dt->format(DateTime::ATOM), "|", $dt2->format("Y-m-d H:i"), "|", $diff->d, ":", $diff->h, ":", $diff->days, ":", $diff->format("%d days %h hours"), "|", implode(",", $dates), "|", $mut->format("Y-m-d"), "|", checkdate(2, 29, 2024) ? "1" : "0", ":", checkdate(2, 29, 2025) ? "1" : "0";`,
		},
		{
			"lot65-reflection",
			`#[Route("/users", 1)]
final class UserDto {
    public function __construct(
        public readonly string $name,
        private int $age = 30
    ) {}
    #[Log("fast")]
    public function greet(string $prefix = "Hi"): string {
        return $prefix . " " . $this->name;
    }
}
enum Status: string {
    case Active = "A";
    case Pending = "P";
}
$rc = new ReflectionClass(UserDto::class);
$attrs = $rc->getAttributes();
$rm = $rc->getMethod("greet");
$mattrs = $rm->getAttributes();
$rp = $rc->getProperty("name");
$u = new UserDto("Ada");
$param = $rm->getParameters()[0];
$re = new ReflectionEnum(Status::class);
echo $rc->getName(), ":", $rc->isFinal() ? "1" : "0", ":", $attrs[0]->getName(), "=", implode(",", $attrs[0]->getArguments()), "|",
     $rm->getName(), ":", $mattrs[0]->getName(), ":", $rm->invoke($u, "Hello"), "|",
     $rp->isPublic() ? "1" : "0", ":", $rp->isReadonly() ? "1" : "0", ":", $rp->getValue($u), "|",
     $param->getName(), ":", $param->getType()->getName(), ":", $param->isOptional() ? "1" : "0", ":", $param->getDefaultValue(), "|",
     $re->isBacked() ? "1" : "0", ":", $re->getBackingType()->getName(), ":", count($re->getCases()), ":", $re->getCase("Active")->getBackingValue();`,
		},
		{
			"lot65-session",
			fmt.Sprintf(`session_save_path(%q);
$s0 = session_status() === PHP_SESSION_NONE ? "1" : "0";
session_id("lot65sess01");
session_start();
$s1 = session_status() === PHP_SESSION_ACTIVE ? "1" : "0";
function bump_session() {
    $_SESSION["user"] = "claire";
    $_SESSION["visits"] = ($_SESSION["visits"] ?? 0) + 1;
}
bump_session();
session_write_close();
$_SESSION = [];
session_id("lot65sess01");
session_start();
bump_session();
$res = $_SESSION["user"] . "=" . $_SESSION["visits"];
session_destroy();
$s2 = session_status() === PHP_SESSION_NONE ? "1" : "0";
echo $s0, ":", $s1, ":", $res, ":", $s2;`, sessDir),
		},
		{
			"lot65-filter",
			`echo filter_var("42", FILTER_VALIDATE_INT), ":",
     filter_var("bad", FILTER_VALIDATE_INT) === false ? "F" : "T", ":",
     filter_var("ada@example.com", FILTER_VALIDATE_EMAIL), ":",
     filter_var("192.168.1.1", FILTER_VALIDATE_IP, FILTER_FLAG_IPV4), ":",
     filter_var("abc-123+45", FILTER_SANITIZE_NUMBER_INT), ":",
     filter_id("int") === FILTER_VALIDATE_INT ? "1" : "0", ":",
     in_array("int", filter_list()) ? "1" : "0";`,
		},
		{
			"lot65-stdlib-plus",
			`$a = ["img12.png", "img10.png", "img2.png", "img1.png"];
natsort($a);
$k = [3, 1, 2];
$v = ["c", "a", "b"];
array_multisort($k, SORT_ASC, $v);
$pct = 0.0;
$sim = similar_text("golang", "gopher", $pct);
$n = 0; $s = "";
sscanf("42 widgets", "%d %s", $n, $s);
$bin = pack("nvc*", 0x1234, 0x5678, 65, 66);
$unp = unpack("nbe/vle/c2chars", $bin);
$ser = serialize(["x" => 10, "y" => ["a", true, null]]);
$unser = unserialize($ser);
putenv("LOT65_VAR=horos");
echo implode(",", array_values($a)), "|",
     implode(",", $k), ":", implode(",", $v), "|",
     $sim, ":", round($pct, 1), "|",
     $n, ":", $s, "|",
     str_word_count("hello brave new world"), ":", strtr("hilla", "ia", "eo"), "|",
     $unp["be"], ":", $unp["le"], ":", $unp["chars1"], ":", $unp["chars2"], "|",
     $unser["x"], ":", $unser["y"][0], ":", ($unser["y"][1] ? "1" : "0"), "|",
     round(atan2(1, 1), 4), ":", round(cosh(0), 2), ":", round(expm1(0), 2), ":", round(log1p(0), 2), ":", mt_getrandmax(), ":", getenv("LOT65_VAR"), ":", php_sapi_name();`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-r", tc.src)
			var wantOut, wantErr bytes.Buffer
			cmd.Stdout = &wantOut
			cmd.Stderr = &wantErr
			err := cmd.Run()
			if err != nil && cmd.ProcessState == nil {
				t.Fatal(err)
			}
			prog, err := compiler.CompileSource("<?php "+tc.src, "Command line code")
			if err != nil {
				t.Fatal(err)
			}
			z := zone.New(zone.DefaultCapacity)
			var gotOut, gotErr bytes.Buffer
			code := vm.Execute(z, &gotOut, &gotErr, prog, vm.NewContext())
			if code != cmd.ProcessState.ExitCode() || gotOut.String() != wantOut.String() || gotErr.String() != wantErr.String() {
				t.Fatalf("got (%q,%q,%d), want (%q,%q,%d)", gotOut.String(), gotErr.String(), code, wantOut.String(), wantErr.String(), cmd.ProcessState.ExitCode())
			}
		})
	}
}

func TestMythosParitySuite(t *testing.T) {
	entries, err := os.ReadDir("testdata/mythos")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("zero mythos testdata files")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".php") {
			continue
		}
		path, err := filepath.Abs(filepath.Join("testdata/mythos", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		t.Run(entry.Name(), func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", path)
			var wantOut, wantErr bytes.Buffer
			cmd.Stdout = &wantOut
			cmd.Stderr = &wantErr
			err := cmd.Run()
			if err != nil && cmd.ProcessState == nil {
				t.Fatal(err)
			}
			wantCode := cmd.ProcessState.ExitCode()

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var gotOut, gotErr bytes.Buffer
			var gotCode int
			prog, cerr := compiler.CompileSource(string(raw), path)
			if cerr != nil {
				fmt.Fprintf(&gotOut, "\n%s\n", cerr)
				gotCode = 255
			} else {
				z := zone.New(zone.DefaultCapacity)
				gotCode = vm.Execute(z, &gotOut, &gotErr, prog, vm.NewContext()) & 255
			}
			if gotCode != wantCode || gotOut.String() != wantOut.String() || gotErr.String() != wantErr.String() {
				t.Fatalf("got (%q,%q,%d), want (%q,%q,%d)", gotOut.String(), gotErr.String(), gotCode, wantOut.String(), wantErr.String(), wantCode)
			}
		})
	}
}

func TestTaintedSuperglobalToEvalSink(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/index.php?cmd=echo+42%3B&n=10", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqCtx := &sapi.RequestContext{}
	if err := sapi.PopulateSuperglobals(z, req, sapi.ServerConfig{}, reqCtx); err != nil {
		t.Fatal(err)
	}
	sapi.BindContext(z, reqCtx)

	prog, err := compiler.CompileSource(`<?php
$num = $_GET['n'] + 5;
$payload = $_GET['cmd'] . " /* " . $num . " */";
try {
    eval($payload);
    echo "ALLOWED";
} catch (ValueError $e) {
    echo "BLOCKED:" . $e->getMessage();
}
`, "taint_test.php")
	if err != nil {
		t.Fatal(err)
	}
	ctx := vm.NewContext()
	ctx.BlockTaintedSinks = true
	var out, errBuf bytes.Buffer
	code := vm.Execute(z, &out, &errBuf, prog, ctx)
	if code != 0 || ctx.TaintedSinks != 1 || out.String() != "BLOCKED:Tainted input rejected at OpEval" {
		t.Fatalf("got code=%d sinks=%d out=%q err=%q", code, ctx.TaintedSinks, out.String(), errBuf.String())
	}
}


