<?php
function thrower($x) { if ($x > 0) throw new RuntimeException("boom$x"); return $x; }
function noret($x) { if ($x) return; echo "no\n"; }
function nested($x) { return thrower($x) + 1; }
try { echo thrower(0), "\n"; echo thrower(1), "\n"; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
try { nested(2); } catch (RuntimeException $e) { echo "caught2 ", $e->getMessage(), "\n"; }
var_dump(noret(1)); var_dump(noret(0));
try { $r = thrower(3) ; } catch (Exception $e) { echo "caught3\n"; } finally { echo "fin\n"; }
function rec($n) { return $n <= 0 ? 0 : 1 + rec($n - 1); } echo rec(100), "\n";
function f2($a, $b) { try { return thrower($a); } catch (Exception $e) { return $b; } }
echo f2(1, 42), "\n";
function g($a) { $t = thrower($a); return $t; }
try { g(5); } catch (Exception $e) { echo "caught5 ", $e->getLine(), "\n"; }
$sum = 0; for ($i = 0; $i < 10; $i++) { try { $sum += thrower($i % 2); } catch (Exception $e) { $sum += 100; } } echo $sum, "\n";
function h1($x) { return h2($x) * 2; } function h2($x) { return h3($x) + 1; } function h3($x) { if ($x) throw new LogicException("deep"); return 7; }
try { echo h1(0), "\n"; echo h1(1), "\n"; } catch (LogicException $e) { echo "deep caught\n"; }
echo "end\n";
thrower(9);
