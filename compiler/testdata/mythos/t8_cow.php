<?php
$a = ['k'=>1]; $b = $a; $b['k'] = 2; $b[] = 3; var_dump($a, $b);
function f($arr) { $arr['k'] = 9; $arr[] = 8; return $arr; } $c = f($a); var_dump($a, $c);
$r = &$a; $r['k'] = 7; $r[] = 6; var_dump($a);
$s = 'str'; try { $s[] = 'x'; } catch (Error $e) { echo 'Error: ', $e->getMessage(), "\n"; }
$n = null; $n['q'] = 1; var_dump($n);
$i = 5; try { $i['q'] = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }
$str = 'abc'; $str[1] = 'X'; var_dump($str); $str[5] = 'Z'; var_dump($str);
$h = []; for ($i=0;$i<40;$i++) { $h["k$i"] = $i; } unset($h['k3']); $h['k3'] = 99; var_dump(count($h), array_keys($h)[39]);
$arr = [1,2,3]; $copy = $arr; $arr[1] += 10; var_dump($copy[1], $arr[1]);
$m = ['x' => ['y' => 1]]; $m2 = $m; $m2['x']['y'] = 2; var_dump($m['x']['y'], $m2['x']['y']);
$big = []; for ($i = 0; $i < 100; $i++) { $big[] = $i; } $alias = $big; $big[] = 100; var_dump(count($alias), count($big));
$z = [1]; $w = &$z[0]; $z[0] = 5; var_dump($w); $z2 = $z; $z2[0] = 9; var_dump($z[0]);
