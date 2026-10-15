<?php
$out = [];
$pats = ['a','ab','abc','xyz','NEEDLE','zz','ba'];
for ($hl = 0; $hl <= 70; $hl++) {
  $hay = ''; for ($i=0;$i<$hl;$i++) $hay .= chr(97 + ($i*7)%26);
  foreach ($pats as $p) {
    $r = strpos($hay, $p); $out[] = ($r === false ? 'F' : $r);
    $r = strpos($hay . $p, $p); $out[] = ($r === false ? 'F' : $r);
    $r = strpos($p . $hay, $p); $out[] = ($r === false ? 'F' : $r);
    $r = strpos($hay . $p . $hay, $p, 1); $out[] = ($r === false ? 'F' : $r);
    $r = strrpos($hay . $p, $p); $out[] = ($r === false ? 'F' : $r);
    $r = str_contains($hay, $p) ? 1 : 0; $out[] = $r;
  }
}
echo md5(implode(',', $out)), ' ', count($out), "\n";
echo implode(',', array_slice($out, 0, 60)), "\n";
var_dump(strpos('abc', ''), strpos('', ''), strpos('abc', 'abcd'), strpos('abc', 'c', 2), strpos('abc', 'c', 3), strpos('abcabc', 'c', -1), strpos('abcabc', 'bc', -4), strpos('', 'a'));
foreach ([1,2,31,32,33,63,64,65] as $n) { $h = str_repeat('a', $n-1) . 'b'; echo $n, ':', strpos($h, 'b'), ':', strpos($h, 'ab'), ':', var_export(strpos($h, 'bb'), true), ':', var_export(strpos($h, 'aab'), true), ';'; }
echo "\n";
$h = str_repeat('a', 31) . 'ba' . str_repeat('a', 31); echo strpos($h, 'ba'), ' ', strpos($h, 'aba'), ' ', strpos($h, 'ab'), ' ', strpos($h, 'baa'), "\n";
$h = str_repeat('a', 32) . 'ba' . str_repeat('a', 30); echo strpos($h, 'ba'), ' ', strpos($h, 'aba'), ' ', strpos($h, 'ab'), ' ', strrpos($h, 'aa'), "\n";
$h = str_repeat('xy', 40); echo strpos($h, 'yx'), ' ', strpos($h, 'xyx', 5), ' ', strpos($h, 'yy'), "\n";
var_dump(strpos("a\0b\0c", "\0c"), strpos("abc", "c", 1), substr("hello", -3, 2), substr("hello", 10), substr("hello", 2, -10), substr("", 0), substr("abc", 3));
