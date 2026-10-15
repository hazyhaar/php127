package parser

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/ast"
)

func TestLiteralValues(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`'a\nb\\c\'d'`, "a\\nb\\c'd"},
		{`"\n\r\t\v\e\f\\\"\$\x41\101\777\q"`, "\n\r\t\v\x1b\f\\\"$AA\xff\\q"},
		{`"\u{0}\u{7f}\u{80}\u{800}\u{10000}\u{10ffff}"`, "\x00\x7f\xc2\x80\xe0\xa0\x80\xf0\x90\x80\x80\xf4\x8f\xbf\xbf"},
		{`"\u{d800}"`, "\xed\xa0\x80"},
		{"<<<END\n  a\\n\n    b\n  END", "a\n\n  b"},
		{"<<<'END'\n  a\\n\n    b\n  END", "a\\n\n  b"},
		{"<<<END\n\\\"\nEND", `\"`},
		{"<<<END\nEND", ""},
		{"<<<'END'\nEND", ""},
	} {
		t.Run(tc.src, func(t *testing.T) {
			src := "<?php " + tc.src + ";"
			category, _, message := oracle(t, src)
			if category != "" {
				t.Fatal(message)
			}
			file, err := Parse(src, "test.php")
			if err != nil {
				t.Fatal(err)
			}
			lit, ok := file.Stmts[0].(*ast.ExprStmt).Expr.(*ast.StringLit)
			if !ok {
				t.Fatalf("Type recu: %T", file.Stmts[0].(*ast.ExprStmt).Expr)
			}
			if lit.Value != tc.want {
				t.Errorf("valeur=%q, attendu=%q", lit.Value, tc.want)
			}
			if lit.Raw != tc.src {
				t.Errorf("brut=%q, attendu=%q", lit.Raw, tc.src)
			}
		})
	}
}

func TestNumericValues(t *testing.T) {
	for _, tc := range []struct {
		src     string
		integer bool
		value   float64
	}{
		{"0", true, 0}, {"123_456", true, 123456}, {"077", true, 63}, {"0o77", true, 63}, {"0b101", true, 5}, {"0xff", true, 255},
		{"0xffffffffffffffff", false, 18446744073709551615.0}, {"02000000000000000000000", false, 18446744073709551616.0},
		{"0b1000000000000000000000000000000000000000000000000000000000000000", false, 9223372036854775808.0},
		{"1.25e2", false, 125}, {"1e1000", false, math.Inf(1)},
	} {
		t.Run(tc.src, func(t *testing.T) {
			f, err := Parse("<?php "+tc.src+";", "test.php")
			if err != nil {
				t.Fatal(err)
			}
			var got float64
			integer := false
			switch n := f.Stmts[0].(*ast.ExprStmt).Expr.(type) {
			case *ast.IntLit:
				got = float64(n.Value)
				integer = true
			case *ast.FloatLit:
				got = n.Value
			default:
				t.Fatalf("Type recu: %T", n)
			}
			if got != tc.value || integer != tc.integer {
				t.Errorf("valeur=%g entier=%t; attendu=%g entier=%t", got, integer, tc.value, tc.integer)
			}
		})
	}
}

func TestTypedTree(t *testing.T) {
	src := `<?php
declare(strict_types=1);
#[A(1, key: 2)]
readonly class C extends P implements I {
 public function __construct(public int $x = 1) {}
 public const string K = 'v';
}
$a = $o?->match(x: 1, y: 2);
$f = C::f(...);
[$x, &$y] = $items;
$s = "$a[0] {$b->c}";
`
	f, err := Parse(src, "typed.php")
	if err != nil {
		t.Fatal(err)
	}
	if !f.StrictTypes || f.Filename != "typed.php" || len(f.Stmts) != 6 {
		t.Fatalf("Fichier: %+v", f)
	}
	c, ok := f.Stmts[1].(*ast.ClassDecl)
	if !ok {
		t.Fatalf("Classe: %T", f.Stmts[1])
	}
	if c.Name != "C" || c.Extends != "P" || !reflect.DeepEqual(c.Implements, []string{"I"}) || c.Modifiers != ast.Readonly || len(c.Attrs) != 1 || c.Attrs[0].Args[1].Name != "key" {
		t.Fatalf("Classe: %+v", c)
	}
	if c.Line != 4 || c.Pos != strings.Index(src, "readonly") {
		t.Errorf("Position: %+v", c.Position)
	}
	ctor := c.Members[0].(*ast.MethodDecl)
	if !ctor.HasBody || ctor.Params[0].Modifiers != ast.Public || ctor.Params[0].Type.Name != "int" || ctor.Params[0].Default.(*ast.IntLit).Value != 1 {
		t.Fatalf("Constructeur: %+v", ctor)
	}
	call := f.Stmts[2].(*ast.ExprStmt).Expr.(*ast.AssignExpr).Right.(*ast.MethodCallExpr)
	if !call.Nullsafe || call.Method.(*ast.ConstFetchExpr).Name != "match" || len(call.Args) != 2 || call.Args[1].Name != "y" {
		t.Fatalf("Appel: %+v", call)
	}
	if !f.Stmts[3].(*ast.ExprStmt).Expr.(*ast.AssignExpr).Right.(*ast.StaticCallExpr).FirstClass {
		t.Error("Conversion en fermeture absente.")
	}
	list := f.Stmts[4].(*ast.ExprStmt).Expr.(*ast.ListAssignExpr)
	if len(list.Items) != 2 || !list.Items[1].ByRef {
		t.Fatalf("Destructuration: %+v", list)
	}
	enc := f.Stmts[5].(*ast.ExprStmt).Expr.(*ast.AssignExpr).Right.(*ast.EncapsedStringExpr)
	if len(enc.Parts) != 3 {
		t.Fatalf("Interpolation: %+v", enc)
	}
	checkTree(t, reflect.ValueOf(f), len(src))
}

func TestConcurrentParsing(t *testing.T) {
	for i := range 16 {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			t.Parallel()
			for range 50 {
				f, err := Parse("<?php function f(int $x):int { return $x+1; } f(2);", "concurrent.php")
				if err != nil || len(f.Stmts) != 2 {
					t.Fatalf("Arbre=%v, erreur=%v", f, err)
				}
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, src := range []string{`<?php self::`, `<?php class A {`, `<?php $a->`, `<?php 1 +`, `<?php function f() { return 1; }`, `<?php echo "{$x}";`} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		file, err := Parse(src, "fuzz.php")
		if err != nil {
			if file != nil {
				t.Fatal("Un arbre partiel accompagne une erreur.")
			}
			return
		}
		if file == nil {
			t.Fatal("Arbre absent sans erreur.")
		}
		checkTree(t, reflect.ValueOf(file), len(src))
	})
}
