<?php
class D { public $n; function __construct($n){$this->n=$n;} function __destruct(){ $a=1;$b=2;$c=3;$d=4;$e=5;$f=6;$g=7;$h=8;$i=9;$j=10;$k=11;$l=12; $s = "x$a$b$c$d$e$f$g$h$i$j$k$l"; $t = [$s, $s, $a+$b+$c+$d+$e+$f+$g+$h+$i+$j+$k+$l]; echo "d{$this->n};"; } }
function rec($depth) {
  $x1 = "s$depth"; $x2 = [$depth]; $x3 = 3; $x4 = 4;
  if ($depth == 0) { $o = new D(0); return 1; }
  $r = rec($depth - 1);
  $o = new D($depth);
  $p = new D($depth + 1000);
  $x5 = "t$depth";
  return $r + 1;
}
for ($d = 1; $d < 70; $d++) { echo rec($d), "\n"; $q = new D(-1); $q = null; $str = str_repeat("y", 100); }
echo "done\n";
