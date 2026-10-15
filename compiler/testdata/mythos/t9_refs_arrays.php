<?php
$z = [1]; $w = &$z[0]; $z[0] = 5; var_dump($w);
$z = [1]; $w = &$z[0]; $w = 6; var_dump($z[0]);
$z = ['a' => 1]; $w = &$z['a']; $z['a'] = 7; var_dump($w);
$z = [1]; $w = &$z[0]; $z2 = $z; $z2[0] = 9; var_dump($z[0], $w);
$z = [[1]]; $w = &$z[0][0]; $z[0][0] = 8; var_dump($w);
function setit(&$arr) { $arr[0] = 11; } $z = [1]; $w = &$z[0]; setit($z); var_dump($w);
$z = [1,2]; foreach ($z as &$v) { $v = $v * 10; } unset($v); $w = &$z[1]; $z[1] = 3; var_dump($w, $z);
$z = [1]; $w = &$z[0]; $z[] = 2; $z[0] = 4; var_dump($w);
$z = [1]; $w = &$z[0]; unset($w); $z[0] = 12; var_dump($z);
$o = new stdClass; $o->arr = [1]; $w = &$o->arr[0]; $o->arr[0] = 13; var_dump($w);
