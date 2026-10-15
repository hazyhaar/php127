package parser

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.hazyhaar.fr/devhoros/php127/ast"
)

func TestTruncated(t *testing.T) {
	for _, src := range []string{"<?php self::", "<?php static::", "<?php parent::", "<?php class A {", "<?php $a->", "<?php 1 +"} {
		_, err := Parse(src, "test.php")
		if _, ok := errors.AsType[*SyntaxError](err); !ok {
			t.Errorf("%s: %v", src, err)
		}
	}
}

func TestProgressGuard(t *testing.T) {
	p := newParser("<?php self::", "test.php")
	defer func() {
		if _, ok := recover().(*SyntaxError); !ok {
			t.Error("La stagnation doit produire une erreur positionnee.")
		}
	}()
	p.progress(p.cursor)
}

func shape(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BinaryExpr:
		return "(" + shape(v.Left) + v.Op + shape(v.Right) + ")"
	case *ast.UnaryExpr:
		return "(" + v.Op + shape(v.Expr) + ")"
	case *ast.TernaryExpr:
		return "(" + shape(v.Cond) + "?" + shape(v.If) + ":" + shape(v.Else) + ")"
	case *ast.IntLit:
		return v.Raw
	case *ast.ConstFetchExpr:
		return v.Name
	case nil:
		return ""
	default:
		return fmt.Sprintf("%T", e)
	}
}
func TestPrecedence(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"1 . 2 + 3", "(1.(2+3))"}, {"1 ?? 2 ?: 3", "((1??2)?:3)"},
		{"2 ** 3 ** 4", "(2**(3**4))"}, {"!1 instanceof C", "(!(1instanceofC))"},
		{"-2 ** 2", "(-(2**2))"}, {"1 + 2 << 3 . 4", "(((1+2)<<3).4)"},
	} {
		f, err := Parse("<?php "+tc.src+";", "test.php")
		if err != nil {
			t.Error(err)
			continue
		}
		got := shape(f.Stmts[0].(*ast.ExprStmt).Expr)
		if got != tc.want {
			t.Errorf("%s: %s != %s", tc.src, got, tc.want)
		}
	}
}

func TestGrammar(t *testing.T) {
	for _, src := range []string{
		`#[Attr(1, a: 2)] readonly class C { public function __construct(public int $x) {} }`,
		`enum Suit: string { case Match = 'm'; public function list(): string { return self::Match->value; } }`,
		`function f((A&B)|null $x, int|string $y): A&B { return $x; }`,
		`$f = static fn($x) => $x + 1; $g = function($x) use (&$a, $b) { yield $x; yield from $b; }; $h = function &() { yield 1; };`,
		`$x = match ($a) { 1, 2 => 'a', default => 'b' };`,
		`[$a, &$b] = $arr; ['k' => &$b] = $arr; list($a, $b) = $arr;`,
		`$x = new class(1) extends C implements A, B { use T { T::a insteadof U; T::b as private c; } };`,
		`try { f(a: 1, b: 2); } catch (A|B) {} finally { echo 'end'; }`,
		`$a = "$a[0] {$b->c}"; $b = <<<END
hello $a
END;`,
		`$a = <<<'END'
hello $a
END;`,
		`if ($a): echo 1; elseif ($b): echo 2; else: echo 3; endif;`,
		`for ($i=0; $i<10; $i++) { while ($x) { break 2; } }`,
		`$f = C::match(...); $x?->readonly(); $o->class; C::{$name};`,
	} {
		t.Run(src, func(t *testing.T) {
			category, _, message := oracle(t, "<?php "+src)
			if category != "" {
				t.Fatalf("Exemple suppose valide rejete par PHP: %s: %s", category, message)
			}
			f, err := Parse("<?php "+src, "Standard input code")
			if err != nil {
				t.Fatal(err)
			}
			checkTree(t, reflect.ValueOf(f), len("<?php "+src))
		})
	}
}

func TestLanguageBoundaries(t *testing.T) {
	for _, src := range []string{
		`1 < 2 < 3;`, `1 == 2 == 3;`, `(1 < 2) < 3;`, `1 < (2 < 3);`,
		`$a ? $b : $c ? $d : $e;`, `$a ?: $b ?: $c;`, `($a ? $b : $c) ? $d : $e;`,
		`$a ? $b : ($c ? $d : $e);`, `$a ?: $b ? $c : $d;`, `$a ? $b : $c ?: $d;`,
		`const X = $a = 1;`, `echo $a[];`, `$a[] = 1;`, `$a[][] = 1;`, `echo $a[][1];`,
		`break;`, `while (true) { continue 0; }`, `while (true) { break 2; }`,
		`while (true) { break -1; }`, `while (true) { break 1+1; }`,
		`function f($x,$x) {}`, `function f(void $x) {}`, `function f(): void|int {}`, `function f(): never { return; }`,
		`class C { public void $x; }`, `class C { public callable $x; }`, `class C { public $x; }`, `class C { $x; }`,
		`class C { public public $x; }`, `class C { final public $x; }`, `class C { static const X=1; }`,
		`class C { function f(); }`, `abstract class C { abstract function f(); }`, `interface I { function f() {} }`,
		`function f(A&B|C $x) {}`, `function f((A&B)|C $x) {}`, `function f(?A|B $x) {}`, `function f((A) $x) {}`,
		`function f(int&Countable $x) {}`, `function f(int|INT $x) {}`, `function f(bool|false $x) {}`, `function f(?mixed $x) {}`,
		`f(a:1,2);`, `f(...$args,1);`, `f(a:1,...$args);`, `f(a:1,a:2);`, `f(...$args,a:1);`,
		`new C(...);`, `$x?->f(...);`, `#[A(...)] class C {}`, `isset(1);`, `empty(1);`, `unset($x,);`,
		`yield 1;`, `yield from $a;`, `function g() { yield 1; }`, `fn() => yield 1;`,
		`$x = new readonly class {};`, `namespace match; function readonly() {}`,
		`$x = new C()->f();`, `$x = (new C())->f();`, `1();`, `(1)();`,
		`[$a,'k'=>&$b]=$arr;`, `list()=$arr;`, `$a=[,];`, `[$a,,]=$arr;`,
		`function f(...$x,$y) {}`, `function f(...$x=1) {}`, `function f(public int $x) {}`,
		`function f(): void { return 1; }`, `function f(): int { return; }`, `function f(): never { yield 1; }`,
		`function &g() { yield from $a; }`,
		`goto missing;`, `a: a:`, `function f() { __halt_compiler(); }`,
		`namespace N; #[Attribute] function f() {}`,
		`namespace N; use Attribute as A; #[A] function f() {}`,
		`namespace N; #[\Attribute] class A {} class B extends A {}`,
		`declare(strict_types=2);`, `echo 1; declare(strict_types=1);`, `declare(strict_types=true);`,
		`break; 1 + ;`, `1=2;`, `($a)=1;`, `$a = &1;`, `$a = &$b[];`,
		`function f() {} function F() {}`, `class C { const class=1; }`,
		`goto x; while (true) { x: break; }`, `while (true) { goto x; } x:`,
		`class C { abstract function f() {} }`, `final abstract class C {}`,
		`class C { const X=foo(); }`, `function f($x=foo()) {}`,
		`echo ;`, `isset();`, `global 1;`, `1 instanceof 2;`, `new 1;`,
		`if (false) { function f() {} } function f() {}`,
		`while (false) { function f() {} } function f() {}`,
		`try { function f() {} } finally {} function f() {}`,
		`if (false) { final class A {} } class B extends A {}`,
	} {
		t.Run(src, func(t *testing.T) {
			category, line, message := oracle(t, "<?php "+src)
			_, err := Parse("<?php "+src, "Standard input code")
			if category == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var gotCategory string
			var gotLine int
			switch e := err.(type) {
			case *SyntaxError:
				gotCategory, gotLine = e.Category(), e.Line
			case *CompileError:
				gotCategory, gotLine = e.Category(), e.Line
			}
			if gotCategory != category || gotLine != line {
				t.Fatalf("PHP=%s:%d: %s; parseur=%v", category, line, message, err)
			}
		})
	}
}

func TestDiagnostics(t *testing.T) {
	for _, src := range []string{`self::`, `static::`, `parent::`, `$a->`, `class A {`, `1 +`, "\n\nbreak;", `function f($a,$a){}`, `const X = $a = 1;`, `class C { public void $x; }`, `function f():void|int {}`, `$a ? $b : $c ? $d : $e;`} {
		t.Run(src, func(t *testing.T) {
			category, line, message := oracle(t, "<?php "+src)
			if category == "" {
				t.Fatal("Le cas doit etre refuse par PHP.")
			}
			_, err := Parse("<?php "+src, "Standard input code")
			if err == nil {
				t.Fatal("Source acceptee a tort.")
			}
			want := fmt.Sprintf("%s: %s in Standard input code on line %d", category, message, line)
			if err.Error() != want {
				t.Errorf("recu: %s\nattendu: %s", err, want)
			}
		})
	}
}

func TestMutations(t *testing.T) {
	for _, mutation := range []struct{ name, file, old, replacement, test string }{
		{"guard", "parser.go", "p.cursor <= before", "false", "TestProgressGuard"},
		{"cursor", "parser.go", "p.cursor++", "p.cursor += 0", "TestDiagnostics"},
		{"concat", "expr.go", "return 150, false, false", "return 175, false, false", "TestPrecedence"},
		{"coalesce", "expr.go", "return 70, true, false", "return 55, true, false", "TestPrecedence"},
		{"power", "expr.go", "return 220, true, false", "return 220, false, false", "TestPrecedence"},
		{"instanceof", "expr.go", "return 200, false, false", "return 185, false, false", "TestPrecedence"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			original, err := filepath.Abs(mutation.file)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(data), mutation.old) != 1 {
				t.Fatalf("Ancre de mutation non unique: %s", mutation.old)
			}
			dir := t.TempDir()
			mutated := filepath.Join(dir, mutation.file)
			if err := os.WriteFile(mutated, []byte(strings.Replace(string(data), mutation.old, mutation.replacement, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			overlay, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{original: mutated}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "overlay.json")
			if err := os.WriteFile(path, overlay, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-overlay", path, "-count=1", "-run", "^"+mutation.test+"$", "./ast/...", "./parser/...")
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("Mutation bloquee: %v\n%s", ctx.Err(), out)
			}
			failure, ok := errors.AsType[*exec.ExitError](err)
			if !ok || failure.ExitCode() != 1 || !strings.Contains(string(out), "--- FAIL: "+mutation.test) || strings.Contains(string(out), "build failed") {
				t.Fatalf("Mutation non mordante: %v\n%s", err, out)
			}
			t.Logf("Mutation %s: code %d, echec du test apres compilation.", mutation.name, failure.ExitCode())
		})
	}
}

var diagnostic = regexp.MustCompile(`(?m)^(Parse error|Fatal error): (.*) in .* on line ([0-9]+)$`)

func oracle(t *testing.T, src string) (string, int, string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-l")
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.CombinedOutput()
	if err == nil {
		if !strings.Contains(string(out), "No syntax errors detected") {
			t.Fatalf("oracle: %s", out)
		}
		return "", 0, ""
	}
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Fatal(err)
	}
	m := diagnostic.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("oracle sans diagnostic: %s (%v)", out, err)
	}
	line, err := strconv.Atoi(m[3])
	if err != nil {
		t.Fatal(err)
	}
	return m[1], line, m[2]
}
func checkTree(t *testing.T, v reflect.Value, size int) {
	t.Helper()
	if !v.IsValid() {
		return
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if !v.IsNil() {
			checkTree(t, v.Elem(), size)
		}
		return
	}
	if v.Type() == reflect.TypeFor[ast.Position]() {
		p := v.Interface().(ast.Position)
		if p.Line < 1 || p.Pos < 0 || p.Pos > size {
			t.Errorf("position: %+v", p)
		}
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[ast.TypeRef]() && v.Interface().(ast.TypeRef).Name == "" && v.FieldByName("Union").Len() == 0 && v.FieldByName("Intersection").Len() == 0 {
			return
		}
		for _, field := range v.Fields() {
			checkTree(t, field, size)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			checkTree(t, v.Index(i), size)
		}
	}
}

func TestPHPTCorpus(t *testing.T) {
	binary, err := os.ReadFile("/usr/bin/php")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(binary)) != "1c564e6bafa56d03359c3774be6f4c77d0edecabd2075e18556940a56747c8d7" {
		t.Fatal("Empreinte PHP divergente.")
	}
	baseDir := "../docs/lot2_1_astra"
	if _, err := os.Stat(baseDir); err != nil {
		baseDir = "/devhoros/php127/docs/lot2_1_astra"
	}
	data, err := os.ReadFile(filepath.Join(baseDir, "corpus.json"))
	if err != nil {
		t.Skipf("PHPT corpus absent (%v)", err)
	}
	var records []struct{ Path, SHA256 string }
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 724 {
		t.Fatalf("N=%d, attendu 724", len(records))
	}
	root := filepath.Join(baseDir, "php-src-php-8.3.6")
	actual := make(map[string]bool, len(records))
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".phpt") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		actual[relative] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(records) {
		t.Fatalf("N disque=%d, N manifeste=%d", len(actual), len(records))
	}
	seen := make(map[string]bool, len(records))
	accepted, rejected := 0, 0
	for _, record := range records {
		if !actual[record.Path] {
			t.Fatal("Reference absente de l'inventaire: " + record.Path)
		}
		if seen[record.Path] {
			t.Fatal("Doublon: " + record.Path)
		}
		seen[record.Path] = true
		t.Run(record.Path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, record.Path))
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != record.SHA256 {
				t.Fatal("Empreinte du corpus divergente.")
			}
			src := extractFILE(t, string(data))
			category, line, message := oracle(t, src)
			file, err := Parse(src, "Standard input code")
			if category == "" {
				accepted++
				if err != nil {
					t.Fatal(err)
				}
				if file == nil || len(file.Stmts) == 0 {
					t.Fatal("Arbre vide.")
				}
				checkTree(t, reflect.ValueOf(file), len(src))
				return
			}
			var gotCategory string
			rejected++
			var gotMessage string
			var gotLine int
			switch e := err.(type) {
			case *SyntaxError:
				gotCategory, gotLine = e.Category(), e.Line
				gotMessage = e.Message
			case *CompileError:
				gotCategory, gotLine = e.Category(), e.Line
				gotMessage = e.Message
			}
			if category != gotCategory || line != gotLine || message != gotMessage {
				t.Errorf("oracle=%s:%d: %s; parseur=%v", category, line, message, err)
			}
		})
	}
	t.Logf("N=%d fichiers confrontes, sans exclusion; PHP accepte=%d, rejette=%d", len(records), accepted, rejected)
}
func extractFILE(t *testing.T, data string) string {
	t.Helper()
	active, eof := false, false
	var result strings.Builder
	for line := range strings.SplitAfterSeq(data, "\n") {
		marker := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.HasPrefix(marker, "--") && strings.HasSuffix(marker, "--") && len(marker) > 4 {
			valid := true
			for _, c := range marker[2 : len(marker)-2] {
				if c != '_' && (c < 'A' || c > 'Z') {
					valid = false
				}
			}
			if valid {
				if active {
					break
				}
				if marker == "--FILE--" || marker == "--FILEEOF--" {
					active = true
					eof = marker == "--FILEEOF--"
				}
				continue
			}
		}
		if active {
			result.WriteString(line)
		}
	}
	if !active {
		t.Fatal("Section FILE absente.")
	}
	if eof {
		return strings.TrimRight(result.String(), "\r\n")
	}
	return result.String()
}
