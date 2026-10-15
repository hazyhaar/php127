// Code créé par agent — Lot 4.3 : tests différentiels contre /usr/bin/php -n pour stdlib_plus.
package stdlib

import (
	"bytes"
	"encoding/hex"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func TestPHPDifferentialPlus(t *testing.T) {
	cases := []call{
		// serialize / unserialize
		{"serialize", []lit{Lnull()}},
		{"serialize", []lit{Lbool(true)}},
		{"serialize", []lit{Lbool(false)}},
		{"serialize", []lit{Lint(0)}},
		{"serialize", []lit{Lint(-42)}},
		{"serialize", []lit{Lint(math.MaxInt64)}},
		{"serialize", []lit{Lint(math.MinInt64)}},
		{"serialize", []lit{Lfloat(3.5)}},
		{"serialize", []lit{Lfloat(math.NaN())}},
		{"serialize", []lit{Lfloat(math.Inf(1))}},
		{"serialize", []lit{Lfloat(math.Inf(-1))}},
		{"serialize", []lit{Lstr("bonjour\x00monde")}},
		{"serialize", []lit{Larr(ap(Lint(10)), kv(Lstr("cle"), Lstr("val")), ap(Lbool(true)))}},
		{"unserialize", []lit{Lstr("N;")}},
		{"unserialize", []lit{Lstr("b:1;")}},
		{"unserialize", []lit{Lstr("b:0;")}},
		{"unserialize", []lit{Lstr("i:-98765;")}},
		{"unserialize", []lit{Lstr("d:2.5;")}},
		{"unserialize", []lit{Lstr("d:NAN;")}},
		{"unserialize", []lit{Lstr("d:INF;")}},
		{"unserialize", []lit{Lstr("d:-INF;")}},
		{"unserialize", []lit{Lstr(`s:5:"hello";`)}},
		{"unserialize", []lit{Lstr(`a:2:{i:0;s:1:"a";s:1:"k";i:42;}`)}},
		{"unserialize", []lit{Lstr(`O:8:"stdClass":2:{s:1:"x";i:1;s:1:"y";s:2:"ok";}`)}},
		{"unserialize", []lit{Lstr("invalid")}},

		// strtr
		{"strtr", []lit{Lstr("baab"), Lstr("ab"), Lstr("01")}},
		{"strtr", []lit{Lstr("hi all, I said hello"), Larr(kv(Lstr("hi"), Lstr("hello")), kv(Lstr("hello"), Lstr("hi")))}},
		{"strtr", []lit{Lstr("abc"), Lstr("a"), Lstr("xyz")}},

		// substr_replace
		{"substr_replace", []lit{Lstr("abcdefgh"), Lstr("X"), Lint(2)}},
		{"substr_replace", []lit{Lstr("abcdefgh"), Lstr("X"), Lint(2), Lint(3)}},
		{"substr_replace", []lit{Lstr("abcdefgh"), Lstr("X"), Lint(-3), Lint(-1)}},
		{"substr_replace", []lit{Lstr("abcdefgh"), Lstr("X"), Lint(2), Lint(0)}},

		// substr_compare
		{"substr_compare", []lit{Lstr("abcde"), Lstr("bc"), Lint(1), Lint(2)}},
		{"substr_compare", []lit{Lstr("abcde"), Lstr("BC"), Lint(1), Lint(2), Lbool(true)}},
		{"substr_compare", []lit{Lstr("abcde"), Lstr("de"), Lint(-2), Lint(2)}},
		{"substr_compare", []lit{Lstr("abcde"), Lstr("bcg"), Lint(1), Lint(2)}},

		// str_rot13
		{"str_rot13", []lit{Lstr("PHP 8.3.0!")}},
		{"str_rot13", []lit{Lstr("CUC 8.3.0!")}},

		// str_word_count
		{"str_word_count", []lit{Lstr("Hello fri3nd, you're looking good today!")}},
		{"str_word_count", []lit{Lstr("Hello fri3nd, you're looking good today!"), Lint(1)}},
		{"str_word_count", []lit{Lstr("Hello fri3nd, you're looking good today!"), Lint(2)}},
		{"str_word_count", []lit{Lstr("Hello fri3nd, you're looking good today!"), Lint(1), Lstr("3")}},

		// strpbrk, strcspn, strspn
		{"strpbrk", []lit{Lstr("This is a Simple text."), Lstr("mi")}},
		{"strpbrk", []lit{Lstr("This is a Simple text."), Lstr("z")}},
		{"strcspn", []lit{Lstr("abcd"), Lstr("apple")}},
		{"strcspn", []lit{Lstr("abcd"), Lstr("banana")}},
		{"strcspn", []lit{Lstr("hello"), Lstr("l"), Lint(1), Lint(3)}},
		{"strspn", []lit{Lstr("42 is the answer"), Lstr("1234567890")}},
		{"strspn", []lit{Lstr("foo"), Lstr("o"), Lint(1), Lint(2)}},

		// strnatcmp, strnatcasecmp, strncasecmp
		{"strnatcmp", []lit{Lstr("img12.png"), Lstr("img2.png")}},
		{"strnatcmp", []lit{Lstr("img2.png"), Lstr("img12.png")}},
		{"strnatcasecmp", []lit{Lstr("IMG2.png"), Lstr("img12.png")}},
		{"strncasecmp", []lit{Lstr("Hello"), Lstr("heLLoWorld"), Lint(5)}},
		{"strncasecmp", []lit{Lstr("Hello"), Lstr("abd"), Lint(2)}},

		// chunk_split, wordwrap, quotemeta, addcslashes, stripcslashes, nl2br
		{"chunk_split", []lit{Lstr("abcdef"), Lint(2), Lstr(":")}},
		{"wordwrap", []lit{Lstr("The quick brown fox jumped over the lazy dog."), Lint(20), Lstr("<br />\n")}},
		{"wordwrap", []lit{Lstr("A verylongwordindeed"), Lint(8), Lstr("\n"), Lbool(true)}},
		{"quotemeta", []lit{Lstr("Hello. (can you hear me?) $100 ^ [a-z] * + ? \\")}},
		{"addcslashes", []lit{Lstr("foo[ ]"), Lstr("A..z")}},
		{"addcslashes", []lit{Lstr("hello\n\r\t"), Lstr("\x00..\x1f")}},
		{"stripcslashes", []lit{Lstr(`\f\o\o\[ \]\n\r\t\x41\102`)}},
		{"nl2br", []lit{Lstr("foo\nbar\r\nbaz")}},
		{"nl2br", []lit{Lstr("foo\nbar\r\nbaz"), Lbool(false)}},

		// strip_tags, htmlentities, html_entity_decode
		{"strip_tags", []lit{Lstr("<p>Test paragraph.</p><!-- Comment --> <a href=\"#fragment\">Other text</a>")}},
		{"strip_tags", []lit{Lstr("<p>Test <b>bold</b></p>"), Lstr("<b>")}},
		{"strip_tags", []lit{Lstr("<p>Test <b>bold</b></p>"), Larr(ap(Lstr("p")))}},
		{"htmlentities", []lit{Lstr("A 'quote' is <b>bold</b> & \"nice\"")}},
		{"html_entity_decode", []lit{Lstr("A &#039;quote&#039; is &lt;b&gt;bold&lt;/b&gt; &amp; &quot;nice&quot;")}},

		// number_format, sscanf, levenshtein, similar_text, soundex
		{"number_format", []lit{Lfloat(1234.56)}},
		{"number_format", []lit{Lfloat(1234.56), Lint(2), Lstr(","), Lstr(" ")}},
		{"number_format", []lit{Lfloat(1234.5678), Lint(2), Lstr("."), Lstr("")}},
		{"sscanf", []lit{Lstr("SN/2350001"), Lstr("SN/%d")}},
		{"sscanf", []lit{Lstr("24\tLewis Carroll"), Lstr("%d\t%s %s")}},
		{"levenshtein", []lit{Lstr("kitten"), Lstr("sitting")}},
		{"levenshtein", []lit{Lstr("kitten"), Lstr("sitting"), Lint(1), Lint(2), Lint(3)}},
		{"similar_text", []lit{Lstr("bafoobar"), Lstr("barfoo")}},
		{"soundex", []lit{Lstr("Euler")}},
		{"soundex", []lit{Lstr("Ellery")}},
		{"soundex", []lit{Lstr("Gauss")}},

		// pack / unpack
		{"pack", []lit{Lstr("nvc*"), Lint(0x1234), Lint(0x5678), Lint(65), Lint(66)}},
		{"pack", []lit{Lstr("H*"), Lstr("414243")}},
		{"pack", []lit{Lstr("a5A5"), Lstr("hi"), Lstr("hi")}},
		{"unpack", []lit{Lstr("nbe/vle/c2chars"), Lstr("\x12\x34\x78\x56AB")}},
		{"unpack", []lit{Lstr("C*"), Lstr("ABC")}},
		{"unpack", []lit{Lstr("H*hex"), Lstr("ABC")}},

		// array_change_key_case, array_column, array_count_values
		{"array_change_key_case", []lit{Larr(kv(Lstr("FirSt"), Lint(1)), kv(Lstr("SecOnd"), Lint(4))), Lint(1)}},
		{"array_change_key_case", []lit{Larr(kv(Lstr("FirSt"), Lint(1)), kv(Lstr("SecOnd"), Lint(4))), Lint(0)}},
		{"array_column", []lit{
			Larr(
				ap(Larr(kv(Lstr("id"), Lint(10)), kv(Lstr("name"), Lstr("Alice")))),
				ap(Larr(kv(Lstr("id"), Lint(20)), kv(Lstr("name"), Lstr("Bob")))),
			),
			Lstr("name"),
			Lstr("id"),
		}},
		{"array_count_values", []lit{Larr(ap(Lint(1)), ap(Lstr("hello")), ap(Lint(1)), ap(Lstr("world")), ap(Lstr("hello")))}},

		// array_diff_assoc, array_diff_key, array_intersect_assoc, array_intersect_key
		{"array_diff_assoc", []lit{
			Larr(kv(Lstr("a"), Lstr("green")), kv(Lstr("b"), Lstr("brown")), kv(Lstr("c"), Lstr("blue")), ap(Lstr("red"))),
			Larr(kv(Lstr("a"), Lstr("green")), ap(Lstr("yellow")), ap(Lstr("red"))),
		}},
		{"array_diff_key", []lit{
			Larr(kv(Lstr("blue"), Lint(1)), kv(Lstr("red"), Lint(2)), kv(Lstr("green"), Lint(3))),
			Larr(kv(Lstr("blue"), Lint(5)), kv(Lstr("yellow"), Lint(7))),
		}},
		{"array_intersect_assoc", []lit{
			Larr(kv(Lstr("a"), Lstr("green")), kv(Lstr("b"), Lstr("brown")), kv(Lstr("c"), Lstr("blue"))),
			Larr(kv(Lstr("a"), Lstr("green")), kv(Lstr("b"), Lstr("yellow")), kv(Lstr("c"), Lstr("blue"))),
		}},
		{"array_intersect_key", []lit{
			Larr(kv(Lstr("blue"), Lint(1)), kv(Lstr("red"), Lint(2)), kv(Lstr("green"), Lint(3))),
			Larr(kv(Lstr("blue"), Lint(5)), kv(Lstr("green"), Lint(7))),
		}},

		// array_product, array_replace, array_replace_recursive, array_merge_recursive
		{"array_product", []lit{Larr(ap(Lint(2)), ap(Lint(4)), ap(Lint(6)), ap(Lint(8)))}},
		{"array_product", []lit{Larr()}},
		{"array_replace", []lit{
			Larr(ap(Lstr("orange")), ap(Lstr("banana")), ap(Lstr("apple"))),
			Larr(kv(Lint(0), Lstr("pineapple")), kv(Lint(2), Lstr("cherry"))),
		}},
		{"array_replace_recursive", []lit{
			Larr(kv(Lstr("citrus"), Larr(ap(Lstr("orange")))), kv(Lstr("berries"), Larr(ap(Lstr("blackberry")), ap(Lstr("raspberry"))))),
			Larr(kv(Lstr("citrus"), Larr(ap(Lstr("pineapple")))), kv(Lstr("berries"), Larr(ap(Lstr("blueberry"))))),
		}},
		{"array_merge_recursive", []lit{
			Larr(kv(Lstr("ar"), Larr(kv(Lstr("favorite"), Lstr("red")))), ap(Lint(5))),
			Larr(ap(Lint(10)), kv(Lstr("ar"), Larr(kv(Lstr("favorite"), Lstr("green")), ap(Lstr("blue"))))),
		}},

		// math / random / env
		{"mt_getrandmax", nil},
		{"getrandmax", nil},
		{"mt_rand", []lit{Lint(42), Lint(42)}},
		{"rand", []lit{Lint(-7), Lint(-7)}},
		{"random_int", []lit{Lint(99), Lint(99)}},
		{"atan2", []lit{Lfloat(1.0), Lfloat(1.0)}},
		{"sinh", []lit{Lfloat(0.5)}},
		{"cosh", []lit{Lfloat(0.5)}},
		{"tanh", []lit{Lfloat(0.5)}},
		{"asinh", []lit{Lfloat(0.5)}},
		{"acosh", []lit{Lfloat(1.5)}},
		{"atanh", []lit{Lfloat(0.5)}},
		{"expm1", []lit{Lfloat(0.5)}},
		{"log1p", []lit{Lfloat(0.5)}},
		{"pi", nil},
		{"php_sapi_name", nil},

		// M8 & M9 : round deux étapes et sprintf remplissage personnalisé / exposant %e
		{"round", []lit{Lfloat(1.955), Lint(2)}},
		{"round", []lit{Lfloat(2.5)}},
		{"round", []lit{Lfloat(-2.5)}},
		{"round", []lit{Lfloat(2.5), Lint(0), Lint(2)}},
		{"round", []lit{Lfloat(2.5), Lint(0), Lint(3)}},
		{"round", []lit{Lfloat(2.5), Lint(0), Lint(4)}},
		{"sprintf", []lit{Lstr("%'*10d"), Lint(42)}},
		{"sprintf", []lit{Lstr("%'#8s"), Lstr("php")}},
		{"sprintf", []lit{Lstr("%e"), Lfloat(12345.0)}},
		{"sprintf", []lit{Lstr("%.2f"), Lfloat(3.14159)}},
		{"vsprintf", []lit{Lstr("%s-%d"), Larr(ap(Lstr("lot")), ap(Lint(4)))}},

		// M4 : fonctions tableaux pures
		{"array_sum", []lit{Larr(ap(Lint(10)), ap(Lint(20)), ap(Lfloat(1.5)))}},
		{"array_unique", []lit{Larr(ap(Lstr("a")), ap(Lstr("b")), ap(Lstr("a")), ap(Lint(1)))}},
		{"array_diff", []lit{
			Larr(ap(Lstr("a")), ap(Lstr("b")), ap(Lstr("c"))),
			Larr(ap(Lstr("b"))),
		}},
		{"array_intersect", []lit{
			Larr(ap(Lstr("a")), ap(Lstr("b")), ap(Lstr("c"))),
			Larr(ap(Lstr("b")), ap(Lstr("c"))),
		}},
		{"array_fill_keys", []lit{Larr(ap(Lstr("x")), ap(Lstr("y"))), Lint(7)}},
		{"array_pad", []lit{Larr(ap(Lint(1)), ap(Lint(2))), Lint(4), Lint(0)}},
		{"array_pad", []lit{Larr(ap(Lint(1)), ap(Lint(2))), Lint(-4), Lint(9)}},
		{"array_chunk", []lit{Larr(ap(Lint(1)), ap(Lint(2)), ap(Lint(3)), ap(Lint(4)), ap(Lint(5))), Lint(2)}},
		{"array_key_first", []lit{Larr(kv(Lstr("first"), Lint(1)), kv(Lstr("second"), Lint(2)))}},
		{"array_key_last", []lit{Larr(kv(Lstr("first"), Lint(1)), kv(Lstr("second"), Lint(2)))}},
		{"array_is_list", []lit{Larr(ap(Lint(10)), ap(Lint(20)))}},
		{"array_is_list", []lit{Larr(kv(Lint(1), Lint(10)), kv(Lint(0), Lint(20)))}},
		{"array_flip", []lit{Larr(kv(Lstr("a"), Lint(1)), kv(Lstr("b"), Lint(2)))}},

		// M4 : fonctions chaînes pures
		{"strcmp", []lit{Lstr("abc"), Lstr("abc")}},
		{"strcasecmp", []lit{Lstr("AbC"), Lstr("aBc")}},
		{"strncmp", []lit{Lstr("abcdef"), Lstr("abcxyz"), Lint(3)}},
		{"str_split", []lit{Lstr("hello"), Lint(2)}},
		{"strrev", []lit{Lstr("abcdef")}},
		{"substr_count", []lit{Lstr("This is a test"), Lstr("is")}},
		{"strstr", []lit{Lstr("name@example.com"), Lstr("@")}},
		{"strstr", []lit{Lstr("name@example.com"), Lstr("@"), Lbool(true)}},
		{"stristr", []lit{Lstr("USER@EXAMPLE.COM"), Lstr("example")}},
		{"strrchr", []lit{Lstr("/var/www/html/index.php"), Lstr("/")}},
		{"str_ireplace", []lit{Lstr("WORLD"), Lstr("PHP"), Lstr("Hello world!")}},
		{"ucwords", []lit{Lstr("hello world\tfoo-bar"), Lstr(" \t-")}},
		{"addslashes", []lit{Lstr("O'Reilly \"quote\" \\ \x00")}},
		{"stripslashes", []lit{Lstr(`O\'Reilly \"quote\" \\`)}},
		{"htmlspecialchars", []lit{Lstr("<a href='test'>\"&\"</a>")}},
		{"htmlspecialchars_decode", []lit{Lstr("&lt;a href=&#039;test&#039;&gt;&quot;&amp;&quot;&lt;/a&gt;")}},

		// M4 : math, conversions de base, version_compare
		{"exp", []lit{Lfloat(1.0)}},
		{"sin", []lit{Lfloat(0.5)}},
		{"cos", []lit{Lfloat(0.5)}},
		{"tan", []lit{Lfloat(0.5)}},
		{"deg2rad", []lit{Lfloat(180.0)}},
		{"rad2deg", []lit{Lfloat(math.Pi)}},
		{"log", []lit{Lfloat(100.0), Lfloat(10.0)}},
		{"log10", []lit{Lfloat(1000.0)}},
		{"hypot", []lit{Lfloat(3.0), Lfloat(4.0)}},
		{"dechex", []lit{Lint(255)}},
		{"hexdec", []lit{Lstr("ff")}},
		{"decbin", []lit{Lint(42)}},
		{"bindec", []lit{Lstr("101010")}},
		{"decoct", []lit{Lint(64)}},
		{"octdec", []lit{Lstr("100")}},
		{"base_convert", []lit{Lstr("ff"), Lint(16), Lint(2)}},
		{"version_compare", []lit{Lstr("8.3.0"), Lstr("8.2.0")}},
		{"version_compare", []lit{Lstr("8.3.0"), Lstr("8.3.0"), Lstr("==")}},
		{"is_iterable", []lit{Larr(ap(Lint(1)))}},
		{"is_iterable", []lit{Lint(42)}},
		{"is_countable", []lit{Larr(ap(Lint(1)))}},
		{"is_countable", []lit{Lstr("nope")}},
	}

	script := buildPHP(cases)
	path := filepath.Join(t.TempDir(), "oracle_plus.php")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/usr/bin/php", "-n", "-d", "display_errors=stderr", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("php: %v\n%s\n%s", err, stderr.String(), out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("php lines %d want %d", len(lines), len(cases))
	}
	for i, c := range cases {
		z := zone.New(1 << 20)
		var buf bytes.Buffer
		args := make([]types.Value, len(c.args))
		for j, a := range c.args {
			args[j] = a.goVal(t, z)
		}
		fn, ok := Lookup(c.fn)
		if !ok {
			t.Fatalf("missing %s", c.fn)
		}
		got, gerr := fn(z, &buf, args)
		wantKind, wantRest := splitPHP(lines[i])
		if wantKind == "EX" {
			parts := strings.SplitN(wantRest, "\t", 2)
			msg, _ := hex.DecodeString(parts[1])
			if gerr == nil || ExceptionClass(gerr) != parts[0] || gerr.Error() != string(msg) {
				t.Errorf("case %d (%s): got err %v want %s %q", i, c.fn, gerr, parts[0], msg)
			}
			continue
		}
		parts := strings.SplitN(wantRest, "\t", 2)
		if gerr != nil || ser(z, got) != parts[1] {
			t.Errorf("case %d (%s): got %s (err=%v) want %s", i, c.fn, ser(z, got), gerr, parts[1])
		}
	}
}

func TestInPlaceAndStatefulFunctions(t *testing.T) {
	z := zone.New(1 << 20)
	var buf bytes.Buffer

	// Test strtok stateful iteration
	s1, _ := newStr(z, "hello/world-php")
	sep, _ := newStr(z, "/-")
	tok1, err := nativeStrtok(z, &buf, []types.Value{s1, sep})
	if err != nil || types.StringData(z, tok1) != "hello" {
		t.Fatalf("strtok 1: %v %q", err, types.StringData(z, tok1))
	}
	tok2, err := nativeStrtok(z, &buf, []types.Value{sep})
	if err != nil || types.StringData(z, tok2) != "world" {
		t.Fatalf("strtok 2: %v %q", err, types.StringData(z, tok2))
	}
	tok3, err := nativeStrtok(z, &buf, []types.Value{sep})
	if err != nil || types.StringData(z, tok3) != "php" {
		t.Fatalf("strtok 3: %v %q", err, types.StringData(z, tok3))
	}
	tok4, err := nativeStrtok(z, &buf, []types.Value{sep})
	if err != nil || tok4.Type != types.TypeFalse {
		t.Fatalf("strtok 4: expected false, got %v", tok4)
	}

	// Test natsort & natcasesort in-place
	arr, _ := newArray(z)
	for _, s := range []string{"img12.png", "img10.png", "img2.png", "img1.png"} {
		sv, _ := newStr(z, s)
		_ = appendKey(z, &arr, sv)
	}
	if _, err := nativeNatsort(z, &buf, []types.Value{arr}); err != nil {
		t.Fatalf("natsort: %v", err)
	}
	_, vals := arrayItems(z, arr)
	wantNat := []string{"img1.png", "img2.png", "img10.png", "img12.png"}
	for i, w := range wantNat {
		if got := types.StringData(z, vals[i]); got != w {
			t.Fatalf("natsort[%d] = %q, want %q", i, got, w)
		}
	}

	// Test array_multisort in-place
	a1, _ := newArray(z)
	a2, _ := newArray(z)
	_ = appendKey(z, &a1, types.NewLong(30))
	_ = appendKey(z, &a1, types.NewLong(10))
	_ = appendKey(z, &a1, types.NewLong(20))
	sA, _ := newStr(z, "trois")
	sB, _ := newStr(z, "un")
	sC, _ := newStr(z, "deux")
	_ = appendKey(z, &a2, sA)
	_ = appendKey(z, &a2, sB)
	_ = appendKey(z, &a2, sC)
	if _, err := nativeArrayMultisort(z, &buf, []types.Value{a1, a2}); err != nil {
		t.Fatalf("array_multisort: %v", err)
	}
	_, v2 := arrayItems(z, a2)
	if types.StringData(z, v2[0]) != "un" || types.StringData(z, v2[1]) != "deux" || types.StringData(z, v2[2]) != "trois" {
		t.Fatalf("array_multisort a2 unexpected order")
	}

	// Test shuffle, str_shuffle, array_rand, random_bytes, lcg_value, putenv/getenv, phpversion, zend_version
	if _, err := nativeShuffle(z, &buf, []types.Value{a1}); err != nil {
		t.Fatalf("shuffle: %v", err)
	}
	if types.ArrayLen(z, a1) != 3 {
		t.Fatalf("shuffle len: %d", types.ArrayLen(z, a1))
	}
	shufS, err := nativeStrShuffle(z, &buf, []types.Value{sA})
	if err != nil || len(types.StringData(z, shufS)) != 5 {
		t.Fatalf("str_shuffle: %v", err)
	}
	rk, err := nativeArrayRand(z, &buf, []types.Value{a1, types.NewLong(2)})
	if err != nil || types.ArrayLen(z, rk) != 2 {
		t.Fatalf("array_rand: %v", err)
	}
	rb, err := nativeRandomBytes(z, &buf, []types.Value{types.NewLong(16)})
	if err != nil || len(types.StringData(z, rb)) != 16 {
		t.Fatalf("random_bytes: %v", err)
	}
	lcg, err := nativeLcgValue(z, &buf, nil)
	if err != nil || !lcg.IsDouble() || lcg.ToDouble() < 0 || lcg.ToDouble() >= 1 {
		t.Fatalf("lcg_value: %v %v", err, lcg)
	}
	assign, _ := newStr(z, "PHP127_TEST_ENV=horos43")
	if ok, err := nativePutenv(z, &buf, []types.Value{assign}); err != nil || ok.Type != types.TypeTrue {
		t.Fatalf("putenv: %v", err)
	}
	envKey, _ := newStr(z, "PHP127_TEST_ENV")
	if ev, err := nativeGetenv(z, &buf, []types.Value{envKey}); err != nil || types.StringData(z, ev) != "horos43" {
		t.Fatalf("getenv: %v %q", err, types.StringData(z, ev))
	}
	pv, err := nativePhpversion(z, &buf, nil)
	if err != nil || !strings.HasPrefix(types.StringData(z, pv), "8.3") {
		t.Fatalf("phpversion: %v", err)
	}
	zv, err := nativeZendVersion(z, &buf, nil)
	if err != nil || !strings.HasPrefix(types.StringData(z, zv), "4.") {
		t.Fatalf("zend_version: %v", err)
	}

	// Test M4 : array cursor (current, next, prev, end, reset, key) + array_push/pop/shift/unshift/splice + sort/rsort/ksort/krsort
	ca, _ := newArray(z)
	_ = appendKey(z, &ca, types.NewLong(100))
	_ = appendKey(z, &ca, types.NewLong(300))
	_ = appendKey(z, &ca, types.NewLong(200))
	if v, err := nativeCurrent("current")(z, &buf, []types.Value{ca}); err != nil || v.ToLong() != 100 {
		t.Fatalf("current: %v %v", err, v)
	}
	if v, err := nativeNext(z, &buf, []types.Value{ca}); err != nil || v.ToLong() != 300 {
		t.Fatalf("next: %v %v", err, v)
	}
	if v, err := nativeEnd(z, &buf, []types.Value{ca}); err != nil || v.ToLong() != 200 {
		t.Fatalf("end: %v %v", err, v)
	}
	if v, err := nativePrev(z, &buf, []types.Value{ca}); err != nil || v.ToLong() != 300 {
		t.Fatalf("prev: %v %v", err, v)
	}
	if v, err := nativeKey(z, &buf, []types.Value{ca}); err != nil || v.ToLong() != 1 {
		t.Fatalf("key: %v %v", err, v)
	}
	if v, err := nativeReset(z, &buf, []types.Value{ca}); err != nil || v.ToLong() != 100 {
		t.Fatalf("reset: %v %v", err, v)
	}
	if _, err := nativeSort(z, &buf, []types.Value{ca}); err != nil {
		t.Fatalf("sort: %v", err)
	}
	_, sortedVals := arrayItems(z, ca)
	if sortedVals[0].ToLong() != 100 || sortedVals[1].ToLong() != 200 || sortedVals[2].ToLong() != 300 {
		t.Fatalf("sort unexpected: %v", sortedVals)
	}
	if n, err := nativeArrayPush(z, &buf, []types.Value{ca, types.NewLong(400)}); err != nil || n.ToLong() != 4 {
		t.Fatalf("array_push: %v %v", err, n)
	}
	if popped, err := nativeArrayPop(z, &buf, []types.Value{ca}); err != nil || popped.ToLong() != 400 {
		t.Fatalf("array_pop: %v %v", err, popped)
	}
	if shifted, err := nativeArrayShift(z, &buf, []types.Value{ca}); err != nil || shifted.ToLong() != 100 {
		t.Fatalf("array_shift: %v %v", err, shifted)
	}
	if n, err := nativeArrayUnshift(z, &buf, []types.Value{ca, types.NewLong(50)}); err != nil || n.ToLong() != 3 {
		t.Fatalf("array_unshift: %v %v", err, n)
	}
	if sp, err := nativeArraySplice(z, &buf, []types.Value{ca, types.NewLong(1), types.NewLong(1)}); err != nil || types.ArrayLen(z, sp) != 1 {
		t.Fatalf("array_splice: %v", err)
	}
	// password_hash / password_verify / ctype_punct
	pw, _ := newStr(z, "secret123")
	ph, err := nativePasswordHash(z, &buf, []types.Value{pw, types.NewLong(1)})
	if err != nil {
		t.Fatalf("password_hash: %v", err)
	}
	pvOk, err := nativePasswordVerify(z, &buf, []types.Value{pw, ph})
	if err != nil || pvOk.Type != types.TypeTrue {
		t.Fatalf("password_verify: %v %v", err, pvOk)
	}
	punctStr, _ := newStr(z, "!@#$%^&*()")
	cpOk, err := nativeCtype("ctype_punct", ctypePunct)(z, &buf, []types.Value{punctStr})
	if err != nil || cpOk.Type != types.TypeTrue {
		t.Fatalf("ctype_punct: %v %v", err, cpOk)
	}
}

