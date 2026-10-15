<?php
function f() { return 1; } var_dump(f());
function g() { $name = 'y'; $$name = 2; return $y ?? 'none'; } var_dump(g());
function h() { return $_GET['q'] ?? 'noq'; } var_dump(h());
function k() { $_GET['q'] = 'set'; } k(); var_dump($_GET['q'] ?? 'noq');
$_POST['p'] = 'tainted'; function m() { return $_POST['p']; } var_dump(m());
function n() { global $zz; $zz = 5; } n(); var_dump($zz);
var_dump(compact('zz'));
