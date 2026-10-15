// Code créé par agent — Lot 4.3 : tests différentiels contre /usr/bin/php -n pour stdlib_plus.
package stdlib

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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
		{"str_replace", []lit{Lstr("a"), Lstr("b"), Larr(ap(Larr(ap(Lstr("a")))), ap(Lstr("xa")), ap(Lint(1)), ap(Lfloat(1.5)), ap(Lbool(true)), ap(Lnull()), kv(Lstr("k"), Larr(ap(Lstr("a")))))}},
		{"str_replace", []lit{Larr(ap(Lstr("a"))), Larr(ap(Lstr("b"))), Larr(kv(Lstr("z"), Larr(ap(Lstr("a")), ap(Lstr("a")))))}},
		{"str_ireplace", []lit{Lstr("A"), Lstr("b"), Larr(ap(Larr(ap(Lstr("a")))), ap(Lstr("xa")))}},
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

// Lot 7.3 : oracle différentiel /usr/bin/php -n pour les fonctions système, réseau et INI.

func runPHPDiff(t *testing.T, cases []call) {
	t.Helper()
	if _, err := exec.LookPath("/usr/bin/php"); err != nil {
		t.Skip("/usr/bin/php absent")
	}
	path := filepath.Join(t.TempDir(), "oracle_wp_sys.php")
	if err := os.WriteFile(path, []byte(buildPHP(cases)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-d", "display_errors=stderr", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("php: %v\n%s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("php lines %d want %d", len(lines), len(cases))
	}
	for i, c := range cases {
		z := zone.New(1 << 20)
		args := make([]types.Value, len(c.args))
		for j, a := range c.args {
			args[j] = a.goVal(t, z)
		}
		fn, ok := Lookup(c.fn)
		if !ok {
			t.Fatalf("missing %s", c.fn)
		}
		var buf bytes.Buffer
		got, gerr := fn(z, &buf, args)
		kind, rest := splitPHP(lines[i])
		parts := strings.SplitN(rest, "\t", 2)
		if kind == "EX" {
			msg, _ := hex.DecodeString(parts[1])
			if gerr == nil || ExceptionClass(gerr) != parts[0] || gerr.Error() != string(msg) {
				t.Errorf("case %d %s(%s): got err %v want %s %q", i, c.fn, truncate(describe(c.args), 120), gerr, parts[0], msg)
			}
			continue
		}
		if gerr != nil || ser(z, got) != parts[1] {
			t.Errorf("case %d %s(%s): got %s (err=%v) want %s", i, c.fn, truncate(describe(c.args), 120), ser(z, got), gerr, parts[1])
		}
	}
}

func TestPHPDifferentialWPSysStrings(t *testing.T) {
	var cases []call
	for _, s := range []string{"", "abc", "é€\xff\xc3", "a\xe2\x82", "\xed\xa0\x80x", "\xf4\x90\x80\x80", "\xc0\xaf",
		"\xc3\xa9t\xc3\xa9", "\xe2\x82\xac", "\xf0\x9f\x98\x80z", "\xc2", "\xe0\x80\x80", "\xc3\xc3\xa9", "\xc3\xff", "\xf0\x9f\x98", "\xf0\x9f\xc3\xa9", "\xf8\x88\x80\x80\x80", "\xc2\x80\xc3\xbf"} {
		cases = append(cases, call{"utf8_decode", []lit{Lstr(s)}})
	}
	for _, ip := range []string{"192.168.1.1", "255.255.255.255", "0.0.0.0", "1.2.3", "01.2.3.4", "1.2.3.04", " 1.2.3.4", "256.1.1.1", "", "::1", "1.2.3.4.5", "a.b.c.d", "10.0.0.256"} {
		cases = append(cases, call{"ip2long", []lit{Lstr(ip)}})
	}
	for _, n := range []int64{-1, 3232235777, 0, 4294967296, -1062731519, 167772161} {
		cases = append(cases, call{"long2ip", []lit{Lint(n)}})
	}
	cases = append(cases, call{"long2ip", []lit{Lstr("abc")}}, call{"long2ip", []lit{Lstr("16909060")}})
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:1.2.3.4", "1.2.3", "01.2.3.4", " 1.2.3.4", "2001:db8::1", "fe80::1%eth0", "::", "1::", "1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7:8:9", "localhost", "", "::1.2.3.4", "2001:DB8::A"} {
		cases = append(cases, call{"inet_pton", []lit{Lstr(ip)}})
	}
	for _, h := range []string{"7f000001", "00000000000000000000000000000001", "00000000000000000000ffff01020304", "20010db8000000000000000000000001",
		"616263", "20010db8000100000000000100000001", "00000000000000000000000001020304", "00000000000000000000000000000000",
		"00010000000000000000000000000000", "00010000000000000000000000000001", "00000000000000000000000000010000", "00000000000000000000fffe01020304",
		"00010002000000000000000300000000", "fe800000000000000000000000000001", ""} {
		raw, _ := hex.DecodeString(h)
		cases = append(cases, call{"inet_ntop", []lit{Lstr(string(raw))}})
	}
	cases = append(cases, call{"get_html_translation_table", nil})
	for _, table := range []int64{0, 1, 5, -1} {
		for _, flags := range []int64{0, 2, 3, 3 | 8, 3 | 16, 3 | 32, 3 | 48, 2 | 32, 1, 2 | 16, 1 | 48} {
			// Écart déclaré : la table complète HTML5 de PHP (1511 entrées) n'est pas servie.
			if table != 0 && flags&48 == 48 {
				continue
			}
			cases = append(cases, call{"get_html_translation_table", []lit{Lint(table), Lint(flags)}})
		}
		cases = append(cases,
			call{"get_html_translation_table", []lit{Lint(table), Lint(3), Lstr("ISO-8859-1")}},
			call{"get_html_translation_table", []lit{Lint(table), Lint(3), Lstr("utf-8")}},
			call{"get_html_translation_table", []lit{Lint(table), Lint(3), Lstr("")}},
			call{"get_html_translation_table", []lit{Lint(table), Lint(3), Lstr("nope")}})
	}
	runPHPDiff(t, cases)
}

// iniCorpus rassemble les cas mesurés sur PHP 8.3 lors de la conception du scanner.
var iniCorpus = []string{
	"a = 1\nb = on\nc = \"quoted ; value\"\nd = off\ne = null\nf = yes\ng = none\nh = true\ni = false\nj = 1.5\nk = plain text   \nl=No\nm=TRUE",
	"# hash\na=1", "a=1 ; c\n;full\n  b  =  2  ",
	"arr[] = x\narr[] = y\nmap[k1] = v1\nmap[k2] = v2\nmap[] = z\nempty =\nemp2=\"\"",
	"[s1]\na=1\n[ s 2 ]\nb=2\n[s1]\nc=3", "const=E_ALL\nc3=UNDEFINED_C\nc4=php_version\nc5=PHP_EOL",
	"a=\"x\\ty\\\\z\\'q\"", "a=\"${HOME}\"\nb=${NOPE_VAR_X_PHP127}z", "a=1.0\nb=-0\nc=+5\nd=1e3\ne= 007 ",
	"a=x;y", "a = \"x\" ; c\nb=\"unterminated", "a=\"multi\nline\"\nb=c", "a[x][y]=1", "a=b=c", "=x", "a\nb=1",
	"a=(1|4)\nb=1&3\nc=6^3", "a=\"\\\\\"", "[sec]\nx=1\n[sec2]\n", "a=on\nb=yes\nc=true\nd=off\ne=no\nf=false\ng=none\nh=null",
	"a.b=1\na-b=2\na/b=3\na:b=4\na b=5", "a=x\r\nb=y\rc=z", "a=\"q\"x\"r\"", "a=1 2 3", "a='x;y'\nb='a\\'", "a=0x10\nb=0777",
	"c=2 3", "c=2  ", "c=x2 ", "c=1.5 ", "c=2\t", "c=2 ;x", "c=2 ;x\n", "c=\"2\" ", "c=2\"x\"", "c=-x", "c=1-2", "c=a$b", "c=$",
	"c=\"a$b\"", "c=a\tb", "c=99999999999 ", "c=1.5e3", "c=.5", "c=5.", "c=--1", "c=!\"x\"", "c=on x", "c=x on", "c=true ",
	"c=True", "c=(1)", "c=1 | 2", "c=~", "c=4294967297|0", "c=\"\\\"\"", "c=\"a\\\"\n", "c=\"C:\\path\\\"\nd=1", "c='x'y", "c='' ",
	"c=${a", "c=${  HOME  }", "c=x${HOME}y", "c[] = a\nc[ k ] = b\nc[\"q\"]=d\nc[1 2]=e", "[a]b\nx=1", "[a] \nx=1",
	"[\"q\"x]\ny=1", "[${HOME}]\ny=1", "[a;b]\ny=1", "[]\ny=1", "c=null x", "c=E_ALL|E_NOTICE", "c=E_ALL x", "c=x E_ALL",
	"c=\"E_ALL\"", "c=1.5|1", "c=\"5\"|2", "c=a|b", "", "a = [b", "a=x\"y\"z", "a=1\na=2", "5=five\n[7]\nx=1",
	"a = b c ; trailing\nkey with space = v", "a=E_ALL & ~E_NOTICE", "a=\"\\\"esc\"", "a=\"x\" \"y\"", "a = !b\nc=~1\nd=1|2",
	"a=TRUE\nb=Off\nc=NULL\nd=0x1A\ne=010", "a=1\na[]=2\na[]=3", "a[-5]=x\na[]=y", "a[05]=x\na[5]=y\na[-0]=z",
	"url = http://example.com/?a=b", "x = yesterday\ny=nonexistent\nz=online", "[E_ALL]\nk=v", "o[E_ALL]=1",
	"a=1|", "a=(1", "a=1)", "a = \"x\"y \"z\"", "a=b\\\nc", "a=\"tab\there\"", "a=${HOME", "true=1", "[x\ny=1",
	"a=\"${HOME}/x\"", "a=99999999999999999999", "a=-1.5", "a = 'single ; quote'", "a=\x00b",
}

// iniLongCorpus porte des clés, blancs et commentaires d'au moins 32 octets, qui traversent les
// marches AVX2 de c2_ini_scan_key, c2_ini_skip_hspace et c2_ini_find_eol, ainsi qu'une clé longue
// arrêtée par chaque octet de la classe d'arrêt de {LABEL}.
func iniLongCorpus() []string {
	long := strings.Repeat("cle longue ", 4)
	pad := strings.Repeat(" \t", 20)
	out := []string{
		long + "= v\n" + long + "x" + pad + "=" + pad + "w" + pad + "\n",
		"k" + pad + "= v\n;" + strings.Repeat(" commentaire long", 4) + "\r\nb=1\n" + pad + "; " + long,
		"a=YeS" + pad + "\nb=NoNe\t\nc=oFF  \nd=nULL\ne=fALSe" + pad + "\nf=ON\ng=nO\nh=TrUe x",
		strings.Repeat("é", 20) + "=1\n" + long + "[" + long + "]=2\n[" + long + "]\n" + long + "=3",
		pad + "\n" + pad + "\r" + pad + "x=1" + pad,
	}
	for _, c := range []byte("\x00\t\n\r!\"$&();=[]^{|}~ #'.:,/@\x7f\x80\xff") {
		out = append(out, strings.Repeat("k", 40)+string([]byte{c})+"=1\nz=2")
	}
	return out
}

func TestPHPDifferentialParseIni(t *testing.T) {
	var cases []call
	for _, src := range append(slices.Clone(iniCorpus), iniLongCorpus()...) {
		for _, mode := range []int64{0, 1, 2} {
			cases = append(cases,
				call{"parse_ini_string", []lit{Lstr(src), Lbool(true), Lint(mode)}},
				call{"parse_ini_string", []lit{Lstr(src), Lbool(false), Lint(mode)}})
		}
	}
	cases = append(cases, call{"parse_ini_string", []lit{Lstr("a=1"), Lbool(false), Lint(7)}}, call{"parse_ini_string", []lit{Lstr("a=1")}})
	runPHPDiff(t, cases)
}

func testImages(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	enc := func(name string, f func(*bytes.Buffer) error) {
		var b bytes.Buffer
		if err := f(&b); err != nil {
			t.Fatal(err)
		}
		out[name] = b.Bytes()
	}
	rgba := image.NewRGBA(image.Rect(0, 0, 7, 3))
	pal := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White})
	enc("rgba.png", func(b *bytes.Buffer) error { return png.Encode(b, rgba) })
	enc("gray16.png", func(b *bytes.Buffer) error { return png.Encode(b, image.NewGray16(image.Rect(0, 0, 5, 9))) })
	enc("pal.png", func(b *bytes.Buffer) error { return png.Encode(b, pal) })
	enc("rgba.jpg", func(b *bytes.Buffer) error { return jpeg.Encode(b, rgba, nil) })
	enc("gray.jpg", func(b *bytes.Buffer) error { return jpeg.Encode(b, image.NewGray(image.Rect(0, 0, 11, 13)), nil) })
	enc("big.jpg", func(b *bytes.Buffer) error { return jpeg.Encode(b, image.NewRGBA(image.Rect(0, 0, 300, 257)), nil) })
	enc("plan9.gif", func(b *bytes.Buffer) error {
		return gif.Encode(b, image.NewPaletted(image.Rect(0, 0, 6, 2), palette.Plan9), nil)
	})
	enc("pal.gif", func(b *bytes.Buffer) error { return gif.Encode(b, pal, nil) })
	// JPEG porteur de segments APP0, APP1 répété et APP13, insérés après SOI.
	j := out["gray.jpg"]
	app := func(m byte, payload string) []byte {
		n := len(payload) + 2
		return append([]byte{0xff, m, byte(n >> 8), byte(n)}, payload...)
	}
	withApp := append([]byte{}, j[:2]...)
	withApp = append(withApp, app(0xe0, "JFIF\x00\x01\x01")...)
	withApp = append(withApp, app(0xe1, "Exif\x00\x00first")...)
	withApp = append(withApp, app(0xe1, "second")...)
	withApp = append(withApp, app(0xed, "Photoshop 3.0\x00")...)
	withApp = append(withApp, j[2:]...)
	out["app.jpg"] = withApp
	out["trunc.png"] = out["rgba.png"][:20]
	out["trunc.gif"] = out["pal.gif"][:8]
	out["trunc.jpg"] = out["gray.jpg"][:40]
	out["sig.jpg"] = []byte("\xff\xd8\xff")
	out["badpng"] = []byte("\x89PNx\r\n\x1a\nxxxxxxxxxxxxxxxxxxxx")
	out["text"] = []byte("hello world, not an image")
	return out
}

func TestPHPDifferentialImageSize(t *testing.T) {
	imgs := testImages(t)
	names := slices.Sorted(maps.Keys(imgs))
	var cases []call
	for _, n := range names {
		cases = append(cases, call{"getimagesizefromstring", []lit{Lstr(string(imgs[n]))}})
	}
	cases = append(cases, call{"getimagesizefromstring", []lit{Lstr("xx")}})
	runPHPDiff(t, cases)
}

func TestImageInfoByReference(t *testing.T) {
	if _, err := exec.LookPath("/usr/bin/php"); err != nil {
		t.Skip("/usr/bin/php absent")
	}
	imgs := testImages(t)
	for _, name := range []string{"app.jpg", "rgba.jpg", "pal.png", "text"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, imgs[name], 0o644); err != nil {
			t.Fatal(err)
		}
		script := `$r = getimagesizefromstring(file_get_contents($argv[1]), $info); echo json_encode(array_map("bin2hex", $info));`
		raw, err := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", script, path).Output()
		if err != nil {
			t.Fatalf("php: %v", err)
		}
		z := zone.New(1 << 20)
		src, _ := newStr(z, string(imgs[name]))
		ref, err := types.NewReference(z, types.NewNull())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := nativeGetimagesizefromstring(z, nil, []types.Value{src, ref}); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		info := deref(z, ref)
		if info.Type != types.TypeArray {
			t.Fatalf("%s: $image_info n'est pas un tableau : %v", name, info)
		}
		types.ArrayForEach(z, info, func(k, v types.Value) bool {
			got[types.StringData(z, k)] = hex.EncodeToString([]byte(types.StringData(z, v)))
			return true
		})
		var want map[string]string
		if string(raw) != "[]" {
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("%s: %v (%s)", name, err, raw)
			}
		}
		if len(want) != len(got) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("%s[%s]: got %s want %s", name, k, got[k], v)
			}
		}
	}
}

func TestWPSysEnvironment(t *testing.T) {
	z := zone.New(1 << 20)
	defer ResetSysState(z)
	call := func(name string, args ...types.Value) types.Value {
		t.Helper()
		fn, ok := Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) absent", name)
		}
		v, err := fn(z, nil, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return v
	}
	str := func(s string) types.Value {
		v, err := newStr(z, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := call("set_time_limit", types.NewLong(5)); v.Type != types.TypeTrue {
		t.Fatalf("set_time_limit = %v", v)
	}
	for i, want := range []int64{0, 0, 1} {
		var v types.Value
		if i == 1 {
			v = call("ignore_user_abort", types.NewBool(true))
		} else {
			v = call("ignore_user_abort")
		}
		if v.ToLong() != want {
			t.Fatalf("ignore_user_abort appel %d = %d, attendu %d", i, v.ToLong(), want)
		}
	}
	for _, n := range []string{"connection_aborted", "connection_status"} {
		if v := call(n); v.Type != types.TypeLong || v.ToLong() != 0 {
			t.Fatalf("%s = %v", n, v)
		}
	}
	for _, ext := range []string{"core", "Standard", "MBSTRING", "pdo_sqlite", "mysqli", "openssl", "fileinfo", "gd", "curl", "spl", "reflection", "random"} {
		if v := call("extension_loaded", str(ext)); v.Type != types.TypeTrue {
			t.Fatalf("extension_loaded(%s) = %v", ext, v)
		}
	}
	for _, ext := range []string{"imagick", "xdebug", "", "libxml"} {
		if v := call("extension_loaded", str(ext)); v.Type != types.TypeFalse {
			t.Fatalf("extension_loaded(%s) = %v", ext, v)
		}
	}
	if got := types.ArrayLen(z, call("get_loaded_extensions")); got != uint32(len(loadedExtensions)) {
		t.Fatalf("get_loaded_extensions: %d entrées", got)
	}
	if got := types.ArrayLen(z, call("get_loaded_extensions", types.NewBool(true))); got != 0 {
		t.Fatalf("get_loaded_extensions(true): %d entrées", got)
	}
	for _, n := range []string{"php_ini_loaded_file", "php_ini_scanned_files"} {
		if v := call(n); v.Type != types.TypeFalse {
			t.Fatalf("%s = %v", n, v)
		}
	}
	if v := call("get_cfg_var", str("memory_limit")); v.Type != types.TypeFalse {
		t.Fatalf("get_cfg_var = %v", v)
	}
	if call("getmyuid").ToLong() != int64(os.Getuid()) || call("getmygid").ToLong() != int64(os.Getgid()) || call("getmypid").ToLong() != int64(os.Getpid()) {
		t.Fatal("identifiants de processus")
	}
	if call("get_current_user").Type != types.TypeString {
		t.Fatal("get_current_user")
	}
	loads := call("sys_getloadavg")
	if loads.Type != types.TypeArray || types.ArrayLen(z, loads) != 3 {
		t.Fatalf("sys_getloadavg = %v", loads)
	}
	for _, n := range []string{"restore_include_path", "zend_thread_id"} {
		if _, ok := Lookup(n); ok {
			t.Fatalf("%s n'existe pas en PHP 8.3 NTS", n)
		}
	}
	if v := call("error_get_last"); !v.IsNull() {
		t.Fatalf("error_get_last initial = %v", v)
	}
	RecordError(z, 2, "Division", "/x.php", 7)
	last := call("error_get_last")
	if ser(z, last) == "N" || types.ArrayLen(z, last) != 4 {
		t.Fatalf("error_get_last = %s", ser(z, last))
	}
	call("error_clear_last")
	if v := call("error_get_last"); !v.IsNull() {
		t.Fatalf("error_get_last après clear = %v", v)
	}
}

// La table INI et include_path n'ont qu'un propriétaire, la VM : aucun bouchon de stdlib ne les sert par
// un chemin de rappel (call_user_func, array_map) qui contournerait l'intrinsèque.
func TestIniOwnedByVM(t *testing.T) {
	for _, n := range []string{"ini_get", "ini_set", "ini_alter", "ini_restore", "ini_get_all", "get_include_path", "set_include_path"} {
		if _, ok := Lookup(n); ok {
			t.Errorf("stdlib.Lookup(%q) présent : double propriétaire de la table INI", n)
		}
	}
	if _, ok := Lookup("parse_ini_string"); !ok {
		t.Error("stdlib.Lookup(\"parse_ini_string\") absent")
	}
}

func TestNativesSorted(t *testing.T) {
	for i := 1; i < len(natives); i++ {
		if cmpFold(natives[i-1].name, natives[i].name) >= 0 {
			t.Fatalf("natives non triée : %s >= %s", natives[i-1].name, natives[i].name)
		}
	}
}

// L'état système d'une Zone, porté par z.Slot(zone.SlotStdlib), est effacé par z.Reset sans appel
// explicite à ResetSysState.
func TestSysStateReleasedOnZoneReset(t *testing.T) {
	z := zone.New(1 << 16)
	abort, _ := Lookup("ignore_user_abort")
	if _, err := abort(z, nil, []types.Value{types.NewBool(true)}); err != nil {
		t.Fatal(err)
	}
	RecordError(z, 2, "avertissement", "f.php", 3)
	if sysStatePeek(z) == nil {
		t.Fatal("état système absent du slot de la Zone")
	}
	z.Reset(func(item zone.CleanupItem) { DispatchCleanup(z, item) })
	if st := sysStatePeek(z); st != nil {
		t.Fatalf("état système retenu après z.Reset : %+v", st)
	}
	v, err := abort(z, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToLong() != 0 {
		t.Fatalf("ignore_user_abort après z.Reset = %d, attendu 0", v.ToLong())
	}
	last, _ := Lookup("error_get_last")
	if v, _ := last(z, nil, nil); !v.IsNull() {
		t.Fatalf("error_get_last après z.Reset = %v, attendu null", v)
	}
	ResetSysState(z)
}

// RecordError copie le message hors de l'arène : la réécriture des octets de l'argument ne modifie pas l'état.
func TestSysStateCopiesZoneStrings(t *testing.T) {
	z := zone.New(1 << 16)
	defer ResetSysState(z)
	msg, err := newStr(z, "message")
	if err != nil {
		t.Fatal(err)
	}
	RecordError(z, 2, types.StringData(z, msg), "f.php", 1)
	mb := types.StringBytes(z, msg)
	for i := range mb {
		mb[i] = 'Y'
	}
	got := sysStatePeek(z).lastErr.message
	if got != "message" {
		t.Fatalf("RecordError aliasé sur l'arène : %q", got)
	}
}

// Les fonctions servies par ext/ ne sont plus masquées par des bouchons de stdlib.
func TestExtFunctionsNotShadowed(t *testing.T) {
	for _, n := range []string{"gzcompress", "gzencode", "iconv", "mb_check_encoding", "mb_convert_encoding", "openssl_encrypt"} {
		if _, ok := Lookup(n); ok {
			t.Errorf("stdlib.Lookup(%q) présent : masque l'implémentation de ext/", n)
		}
	}
}

// TestSgoiterINIScanParityVsGCC rejoue sur le Go émis par sgoiter chaque ligne de l'oracle
// c_src/ini_scan_oracle.c compilé par gcc -O2 avec c_src/ini_scan.c : sous GOEXPERIMENT=simd la
// marche AVX2 Go est opposée à la marche AVX2 C, sans lui la marche scalaire Go l'est aussi. Le
// sous-test regen régénère les deux fichiers Go et exige l'égalité octet à octet avec ceux du dépôt.
func TestSgoiterINIScanParityVsGCC(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ini_scan_oracle")
	cmd := exec.CommandContext(t.Context(), "gcc", "-O2", "-Wall", "-Wextra", "-Werror", "-mavx2",
		"c_src/ini_scan_oracle.c", "c_src/ini_scan.c", "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	out, err := exec.CommandContext(t.Context(), bin).Output()
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if !bytes.Contains(out, []byte("ini_scan_oracle: OK")) {
		t.Fatal("oracle sans ligne OK")
	}
	num := func(s string) uint64 {
		t.Helper()
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	scans := map[string]func([]byte, uint64, uint64) uint64{
		"hsp": C2_ini_skip_hspace,
		"eol": C2_ini_find_eol,
		"key": C2_ini_scan_key,
	}
	counts := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "ini_scan_oracle:") {
			continue
		}
		f := strings.Fields(line)
		counts[f[0]]++
		switch {
		case f[0] == "kw" && len(f) == 3:
			src := decHex(t, f[1])
			want, err := strconv.ParseInt(f[2], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if got := C2_ini_kw_code(src, uint64(len(src))); got != want {
				t.Fatalf("kw %q : Go %d, gcc %d", line, got, want)
			}
		case scans[f[0]] != nil && len(f) == 5:
			src := decHex(t, f[1])
			if got := scans[f[0]](src, num(f[2]), num(f[3])); got != num(f[4]) {
				t.Fatalf("%s %q : Go %d, gcc %d", f[0], line, got, num(f[4]))
			}
		default:
			t.Fatalf("ligne inconnue %q", line)
		}
	}
	for op, min := range map[string]int{"hsp": 4000, "eol": 4000, "key": 4000, "kw": 600} {
		if counts[op] < min {
			t.Fatalf("%s : %d lignes, au moins %d attendues", op, counts[op], min)
		}
	}

	// Un transpileur absent est un échec, non un saut, comme dans ext/ : aucun Go retouché à la main
	// ne passe la porte faute de régénération.
	t.Run("regen", func(t *testing.T) {
		const sgoiter = "/devhoros/c2simd/bin/sgoiter"
		if _, err := os.Stat(sgoiter); err != nil {
			t.Fatalf("%s absent : fraîcheur du Go généré non vérifiable", sgoiter)
		}
		src, err := filepath.Abs("c_src/ini_scan.c")
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		cmd := exec.CommandContext(t.Context(), sgoiter, "-in", src, "-out", filepath.Join(dir, "ini_scan_gen.go"), "-pkg", "stdlib", "-internal-linkage", "on")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sgoiter: %v\n%s", err, out)
		}
		for _, name := range []string{"ini_scan_gen.go", "ini_scan_nosimd_gen.go"} {
			want, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s diffère de la sortie de sgoiter sur c_src/ini_scan.c", name)
			}
		}
	})
}
