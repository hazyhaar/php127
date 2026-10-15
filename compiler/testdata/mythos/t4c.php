<?php
class E { public $n; function __construct($n){$this->n=$n;} function __destruct() { throw new Exception("dtor{$this->n}"); } }
try { $arr = ['x' => new E(1)]; $arr['x'] = 'new'; echo "nr1\n"; } catch (Exception $e) { echo "c1 ", $e->getMessage(), "\n"; }
try { $o = new stdClass; $o->p = new E(2); $o->p = 'v'; echo "nr2\n"; } catch (Exception $e) { echo "c2 ", $e->getMessage(), "\n"; }
try { $a2 = ['k' => new E(3)]; unset($a2['k']); echo "nr3\n"; } catch (Exception $e) { echo "c3 ", $e->getMessage(), "\n"; }
try { $x = new E(4); $x = 1; echo "nr4\n"; } catch (Exception $e) { echo "c4 ", $e->getMessage(), "\n"; }
function f() { $t = new E(5); }
try { f(); echo "nr5\n"; } catch (Exception $e) { echo "c5 ", $e->getMessage(), "\n"; }
function g() { $t = new E(6); return 1; }
try { $r = g(); echo "nr6 $r\n"; } catch (Exception $e) { echo "c6 ", $e->getMessage(), "\n"; }
try { (function() { $t = new E(7); })(); echo "nr7\n"; } catch (Exception $e) { echo "c7 ", $e->getMessage(), "\n"; }
try { $l = [new E(8)]; $l = null; echo "nr8\n"; } catch (Exception $e) { echo "c8 ", $e->getMessage(), "\n"; }
try { $s = 'a'; $s .= (string)(new E(9) ? 'b' : 'c'); echo "nr9 $s\n"; } catch (Exception $e) { echo "c9 ", $e->getMessage(), "\n"; }
echo "end\n";
