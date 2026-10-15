<?php
class D { public $n; function __construct($n){$this->n=$n;} function __destruct(){ global $log; $log[] = "d{$this->n}"; } }
$log=[];
class E { function __destruct() { throw new Exception("from dtor"); } }
 { $t = new E; return 5; }
function mk() { $t = new E; return 5; }
try { $v = mk(); echo "v=$v\n"; } catch (Exception $ex) { echo "caught2 ", $ex->getMessage(), "\n"; }
class R { public static $keep; function __destruct() { self::$keep = $this; echo "resurrect\n"; } }
$r = new R; $r = null; var_dump(R::$keep instanceof R);
$x = 1; $y = 2;
class F { function __destruct() { echo "F\n"; } }
$f = new F; $z = $x + $y; $f = $z; var_dump($f);
$list = [new D(10), new D(11)]; $list = []; echo "cleared\n";
$k = ['a' => new D(20)]; $k['a'] = $k; echo "self\n"; var_dump(count($k));
var_dump($log);
echo "end\n";
