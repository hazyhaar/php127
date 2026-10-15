<?php
$a = 5; $b = &$a;
while ($b < 10) { $b++; }
var_dump($a, $b);
$c = 1; $d = &$c; $c = $c + 1; var_dump($d);
$c2 = 1; $d2 = &$c2; $d2 = $c2 * 3; var_dump($c2);
function inc(&$x) { $x = $x + 1; } $v = 1; inc($v); var_dump($v);
$arr = [1,2,3]; foreach ($arr as &$r) { $r = $r * 2; } unset($r); var_dump($arr);
$g = 10; function useg() { global $g; $g = $g + 5; return $g; } var_dump(useg(), $g);
$n = 0; $m = &$n; for ($i = 0; $i < 3; $i++) { $m = $m + $i; } var_dump($n);
$p = 1; $q = &$p; while ($q < 3) { $p = $p + 1; } var_dump($q);
