// Package fuzz_stress implémente le fuzzer différentiel Go 1.27, le banc de
// mise sous forte charge HTTP SAPI/VM/PDO et le benchmark comparatif 3 voies
// (/usr/bin/php vs phpr php-rust vs php127).
package fuzz_stress

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/lexer"
	"code.hazyhaar.fr/devhoros/php127/parser"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/vm"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

var corpusPrograms = []string{
	`<?php echo 1 + 2 * 3 - (4 >> 1), "\n";`,
	`<?php $a = 10; $b = 3; echo intdiv($a, $b), ":", ($a % $b), ":", ($a ** $b), "\n";`,
	`<?php $s = "hello world"; echo strtoupper($s), "|", substr($s, 6, 5), "|", strlen($s), "\n";`,
	`<?php $a = [3, 1, 4, 1, 5]; $r = array_reverse(array_slice($a, 1, 3)); echo implode(",", $r), "\n";`,
	`<?php $m = ["a" => 1, "b" => 2, "c" => 3]; unset($m["b"]); echo count($m), ":", ($m["a"] + $m["c"]), "\n";`,
	`<?php $sum = 0; for ($i = 1; $i <= 20; $i++) { if ($i % 2 == 0) continue; $sum += $i; } echo $sum, "\n";`,
	`<?php $w = 1; $n = 0; while ($w < 100) { $w *= 2; $n++; } echo "$w:$n\n";`,
	`<?php function fib($n) { return $n <= 1 ? $n : fib($n - 1) + fib($n - 2); } echo fib(10), "\n";`,
	`<?php $f = function($x) { return $x * $x + 1; }; echo $f(7), "\n";`,
	`<?php $k = 5; $g = fn($x) => $x + $k; echo $g(12), "\n";`,
	`<?php class Box { public $v; public function __construct($v) { $this->v = $v; } public function inc($d) { return $this->v += $d; } } $b = new Box(10); echo $b->inc(5), "\n";`,
	`<?php class Base { public function tag() { return "B"; } } class Sub extends Base { public function tag() { return parent::tag() . "S"; } } echo (new Sub())->tag(), "\n";`,
	`<?php interface Named { public function name(); } class Item implements Named { public function name() { return "item"; } } $it = new Item(); echo ($it instanceof Named ? "1:" : "0:") . $it->name(), "\n";`,
	`<?php trait TAdd { public function add($a, $b) { return $a + $b; } } class Calc { use TAdd; } echo (new Calc())->add(19, 23), "\n";`,
	`<?php enum Color: string { case Red = 'R'; case Blue = 'B'; } echo Color::Red->value, ":", Color::Blue->name, "\n";`,
	`<?php function gen($n) { for ($i = 1; $i <= $n; $i++) { yield $i * 10; } } $s = 0; foreach (gen(4) as $v) { $s += $v; } echo $s, "\n";`,
	`<?php try { throw new Exception("boom", 7); } catch (Exception $e) { echo $e->getMessage(), ":", $e->getCode(), "\n"; } finally { echo "fin\n"; }`,
	`<?php $x = 2; $r = match($x) { 1 => "un", 2, 3 => "deux-trois", default => "autre" }; echo $r, "\n";`,
	`<?php $v = "b"; switch ($v) { case "a": echo 1; break; case "b": echo 2; break; default: echo 3; } echo "\n";`,
	`<?php $x = null; $y = $x ?? "fallback"; echo $y, "\n";`,
	`<?php $a = 5; $b =& $a; $b = 17; echo $a, "\n";`,
	`<?php function bump(&$v) { $v += 100; } $z = 23; bump($z); echo $z, "\n";`,
	`<?php $d = ["x" => 1, "y" => [2, 3]]; $j = json_encode($d); $u = json_decode($j, true); echo $j, "|", $u["y"][1], "\n";`,
	`<?php $s = serialize(["k" => 42, "v" => "ok"]); $u = unserialize($s); echo $u["k"], ":", $u["v"], "\n";`,
	`<?php if (preg_match('/^([a-z]+)-(\d+)$/', 'lot-54', $m)) { echo $m[1], ":", $m[2], "\n"; }`,
	`<?php echo preg_replace('/\d+/', '#', 'a12b34c56'), "\n";`,
	`<?php echo sprintf("%s-%04d-%.2f", "id", 42, 3.5), "\n";`,
	`<?php $arr = [1, 2, 3, 4]; $fn = fn($x) => $x * 3; $s = 0; foreach ($arr as $v) { $s += $fn($v); } echo $s, "\n";`,
	`<?php $arr = [1, 2, 3, 4, 5, 6]; $pred = fn($x) => $x % 2 == 0; $f = []; foreach ($arr as $v) { if ($pred($v)) $f[] = $v; } echo implode("-", $f), "\n";`,
	`<?php echo implode("|", array_keys(["p" => 10, "q" => 20])), ":", implode("|", array_values(["p" => 10, "q" => 20])), "\n";`,
	`<?php echo strtolower("ABC"), ":", str_repeat("xy", 3), ":", str_replace("b", "B", "abcb"), "\n";`,
	`<?php echo trim("  hello  "), ":", ltrim("--ok", "-"), ":", rtrim("ok--", "-"), "\n";`,
	`<?php $p = explode(":", "a:b:c:d"); echo count($p), ":", $p[2], "\n";`,
	`<?php echo abs(-42), ":", min(3, 1, 9), ":", max(3, 1, 9), "\n";`,
	`<?php echo (10 <=> 20), ":", (20 <=> 20), ":", (30 <=> 20), "\n";`,
	`<?php class Counter { public static $n = 0; public static function inc() { return ++self::$n; } } echo Counter::inc(), ":", Counter::inc(), "\n";`,
	`<?php class CConst { public const MAGIC = 127; } echo CConst::MAGIC, "\n";`,
	`<?php function varsum(...$nums) { $s = 0; foreach ($nums as $n) { $s += $n; } return $s; } echo varsum(1, 2, 3, 4, 5), "\n";`,
	`<?php $a = [10, 20, 30]; [$x, , $z] = $a; echo "$x:$z\n";`,
	`<?php echo hash('sha256', 'php127'), "\n";`,
}

func verifyCompiledProgram(t testing.TB, prog *bytecode.Program) {
	t.Helper()
	if prog == nil || prog.Main == nil {
		t.Fatalf("programme compilé nil")
	}
	if err := bytecode.Verify(prog.Main); err != nil {
		t.Fatalf("bytecode.Verify(Main) en échec: %v", err)
	}
	for i, fn := range prog.Funcs {
		if err := bytecode.Verify(fn); err != nil {
			t.Fatalf("bytecode.Verify(Funcs[%d] %s) en échec: %v", i, fn.Name, err)
		}
	}
}

func runInPHP127(z *zone.Zone, src string) (string, int, error) {
	prog, err := compiler.CompileSource(src, "fuzz_diff.php")
	if err != nil {
		return "", 255, err
	}
	var out bytes.Buffer
	var errBuf bytes.Buffer
	code := vm.Execute(z, &out, &errBuf, prog, nil)
	z.Reset(func(item zone.CleanupItem) {
		types.DispatchObjectDestructor(z, item)
	})
	return out.String(), code, nil
}

func runInHostPHP(src string) (string, error) {
	trimmed := strings.TrimSpace(src)
	trimmed = strings.TrimPrefix(trimmed, "<?php")
	cmd := exec.Command("/usr/bin/php", "-n", "-r", trimmed)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("oracle /usr/bin/php -n: %w (%s)", err, string(out))
	}
	return string(out), nil
}

// generateDeterministicPHP construit un programme PHP 8.3 synthétique mais
// sémantiquement valide, déterministe et sans boucle infinie à partir d'une graine d'octets.
func generateDeterministicPHP(seed []byte) string {
	b := func(idx int) int {
		if len(seed) == 0 {
			return (idx*37 + 13) & 0xff
		}
		return int(seed[idx%len(seed)])
	}

	a := (b(0) % 97) - 48
	c := (b(1) % 89) - 44
	d := (b(2) % 15) + 1
	shift := (b(3) % 5) + 1
	loopBound := (b(4) % 12) + 3

	words := []string{"alpha", "beta", "gamma", "delta", "omega", "horos", "php127", "zone"}
	w1 := words[b(5)%len(words)]
	w2 := words[b(6)%len(words)]
	v1 := (b(7) % 50) + 1
	v2 := (b(8) % 50) + 1
	v3 := (b(9) % 50) + 1
	matchSel := b(10) % 4

	var sb strings.Builder
	sb.WriteString("<?php\n")
	fmt.Fprintf(&sb, "$a = %d; $c = %d; $d = %d;\n", a, c, d)
	fmt.Fprintf(&sb, "$arith = ($a * 3 - $c) + intdiv($a, $d) + ($d << %d);\n", shift)
	fmt.Fprintf(&sb, "$acc = 0;\nfor ($i = 0; $i < %d; $i++) {\n    $acc += ($i * 2) - ($i %% %d);\n}\n", loopBound, d)
	fmt.Fprintf(&sb, "$str = strtoupper(%q) . '-' . substr(%q, 0, 3) . ':' . strlen(%q);\n", w1, w2, w1+w2)
	fmt.Fprintf(&sb, "$arr = [%d, %d, %d];\n$arr['k'] = $acc;\n", v1, v2, v3)
	sb.WriteString("$sum = 0;\nforeach ($arr as $k => $val) { $sum += $val; }\n")
	fmt.Fprintf(&sb, "function f_calc($x, $y = %d) { return ($x ^ $y) + ($x & 15); }\n", d)
	sb.WriteString("class Holder { public $v; public function __construct($v) { $this->v = $v; } public function step($m) { return $this->v + $m; } }\n")
	sb.WriteString("$h = new Holder($arith);\n")
	fmt.Fprintf(&sb, "$m = match(%d) { 0 => 'z0', 1, 2 => 'z12', default => 'zdef' };\n", matchSel)
	sb.WriteString("$exc = 'ok';\ntry {\n    if ($sum < -999999) { throw new Exception('never'); }\n    $exc = 'pass:' . f_calc($h->step($sum));\n} catch (Exception $e) {\n    $exc = $e->getMessage();\n}\n")
	sb.WriteString("echo $arith, '|', $acc, '|', $str, '|', $sum, '|', $m, '|', $exc, \"\\n\";\n")
	return sb.String()
}

// FuzzLexerParserCompilerVM vérifie que toute chaîne d'octets arbitraire soumise au
// lexer, au parser, au compilateur et au vérificateur de bytecode se termine sans panique.
func FuzzLexerParserCompilerVM(f *testing.F) {
	for _, prog := range corpusPrograms {
		f.Add(prog)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 64*1024 {
			return
		}
		l := lexer.New(src)
		maxSteps := len(src) + 128
		steps := 0
		for {
			tok := l.Next()
			steps++
			if tok.ID == lexer.EOF {
				break
			}
			if steps > maxSteps {
				t.Fatalf("lexer non borné après %d jetons sur entrée de taille %d", steps, len(src))
			}
		}

		astFile, err := parser.Parse(src, "fuzz.php")
		if err != nil || astFile == nil {
			return
		}
		prog, err := compiler.Compile(astFile)
		if err != nil || prog == nil {
			return
		}
		verifyCompiledProgram(t, prog)
	})
}

// FuzzDifferentialGenerator génère un programme PHP 8.3 valide à partir de la graine de
// fuzzing et vérifie la parité bit à bit entre php127 et /usr/bin/php -n.
func FuzzDifferentialGenerator(f *testing.F) {
	for i := range 32 {
		f.Add([]byte{byte(i), byte(i * 7), byte(i * 13), byte(i + 3), byte(i * 5), byte(i + 11), byte(i * 17), byte(i * 19), byte(i * 23), byte(i * 29), byte(i * 31)})
	}
	z := zone.New(2 << 20)
	f.Fuzz(func(t *testing.T, seed []byte) {
		src := generateDeterministicPHP(seed)
		astFile, err := parser.Parse(src, "gen.php")
		if err != nil {
			t.Fatalf("parser.Parse sur programme généré: %v\n%s", err, src)
		}
		prog, err := compiler.Compile(astFile)
		if err != nil {
			t.Fatalf("compiler.Compile sur programme généré: %v\n%s", err, src)
		}
		verifyCompiledProgram(t, prog)

		var out bytes.Buffer
		code := vm.Execute(z, &out, io.Discard, prog, nil)
		z.Reset(func(item zone.CleanupItem) {
			types.DispatchObjectDestructor(z, item)
		})
		if code != 0 {
			t.Fatalf("vm.Execute exit=%d sur programme généré:\n%s", code, src)
		}
		want, err := runInHostPHP(src)
		if err != nil {
			t.Fatalf("runInHostPHP: %v", err)
		}
		if out.String() != want {
			t.Fatalf("divergence différentielle:\ngot:  %q\nwant: %q\nprog:\n%s", out.String(), want, src)
		}
	})
}

// FuzzPHP127Differential est un alias de FuzzDifferentialGenerator pour l'exécution par nom.
func FuzzPHP127Differential(f *testing.F) {
	FuzzDifferentialGenerator(f)
}

// TestFuzzDifferentialCorpus valide la parité bit à bit entre php127 et /usr/bin/php -n
// sur les 40 programmes du corpus initial ainsi que sur 64 graines déterministes.
func TestFuzzDifferentialCorpus(t *testing.T) {
	z := zone.New(4 << 20)
	for i, src := range corpusPrograms {
		astFile, err := parser.Parse(src, fmt.Sprintf("corpus_%d.php", i))
		if err != nil {
			t.Fatalf("corpus[%d] parser.Parse: %v\n%s", i, err, src)
		}
		prog, err := compiler.Compile(astFile)
		if err != nil {
			t.Fatalf("corpus[%d] compiler.Compile: %v\n%s", i, err, src)
		}
		verifyCompiledProgram(t, prog)

		got, code, err := runInPHP127(z, src)
		if err != nil || code != 0 {
			t.Fatalf("corpus[%d] runInPHP127 err=%v code=%d\n%s", i, err, code, src)
		}
		want, err := runInHostPHP(src)
		if err != nil {
			t.Fatalf("corpus[%d] runInHostPHP: %v", i, err)
		}
		if got != want {
			t.Fatalf("corpus[%d] divergence:\ngot:  %q\nwant: %q\nsrc:  %s", i, got, want, src)
		}
	}

	for i := range 64 {
		seed := []byte{
			byte(i * 3), byte(i*11 + 5), byte(i*17 + 2), byte(i + 1),
			byte(i*7 + 3), byte(i*13 + 9), byte(i*19 + 4), byte(i*23 + 7),
			byte(i*29 + 1), byte(i*31 + 6), byte(i*37 + 8),
		}
		src := generateDeterministicPHP(seed)
		got, code, err := runInPHP127(z, src)
		if err != nil || code != 0 {
			t.Fatalf("seed[%d] runInPHP127 err=%v code=%d", i, err, code)
		}
		want, err := runInHostPHP(src)
		if err != nil {
			t.Fatalf("seed[%d] runInHostPHP: %v", i, err)
		}
		if got != want {
			t.Fatalf("seed[%d] divergence:\ngot:  %q\nwant: %q", i, got, want)
		}
	}
}
