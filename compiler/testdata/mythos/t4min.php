<?php
class E { function __destruct() { throw new Exception("from dtor"); } }
function mk() { $t = new E; return 5; }
try { $v = mk(); echo "v=$v\n"; } catch (Exception $ex) { echo "caught2 ", $ex->getMessage(), "\n"; }
echo "end\n";
