<?php
class D { public $n; function __construct($n){$this->n=$n;} function __destruct(){ global $arr, $obj, $log; $log[] = "d{$this->n}"; if ($this->n == 1) { $arr['x'] = 'from-dtor'; $arr[] = 'app'; } if ($this->n == 2) { $obj->p = 'dtor-set'; } if ($this->n == 3) { global $arr2; $arr2['c'] = 'added'; } } }
$log = [];
$arr = ['x' => new D(1)];
$arr['x'] = 'new';
var_dump($arr);
$obj = new stdClass; $obj->p = new D(2);
$obj->p = 'new2';
var_dump($obj->p);
$arr2 = ['a' => new D(3), 'b' => 1];
unset($arr2['a']);
var_dump(count($arr2), $arr2);
$o = new D(4); $o = null; echo "after4\n";
class E { function __destruct() { throw new Exception("from dtor"); } }
try { $e = new E; $e = null; echo "not reached\n"; } catch (Exception $ex) { echo "caught ", $ex->getMessage(), "\n"; }
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
