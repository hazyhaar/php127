<?php
class E { function __destruct() { throw new Exception("from dtor"); } }
function mk(int $z) { $t = new E; return 5 + $z; }
try { $v = mk(1); echo "v=$v\n"; } catch (Exception $ex) { echo "caught2 ", $ex->getMessage(), "\n"; }
echo "end\n";
