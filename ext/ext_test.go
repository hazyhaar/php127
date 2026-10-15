package ext

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/datetime"
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	kNull = iota
	kBool
	kInt
	kString
	kArray
)

type lit struct {
	k     int
	b     bool
	i     int64
	s     string
	elems []el
}

type el struct {
	auto bool
	key  lit
	val  lit
}

func lnull() lit           { return lit{k: kNull} }
func lbool(v bool) lit     { return lit{k: kBool, b: v} }
func lint(v int64) lit     { return lit{k: kInt, i: v} }
func lstr(v string) lit    { return lit{k: kString, s: v} }
func larr(elems ...el) lit { return lit{k: kArray, elems: elems} }
func ap(v lit) el          { return el{auto: true, val: v} }
func kv(k, v lit) el       { return el{key: k, val: v} }

type call struct {
	fn   string
	args []lit
	refs []int
}

func (v lit) php() string {
	switch v.k {
	case kNull:
		return "null"
	case kBool:
		if v.b {
			return "true"
		}
		return "false"
	case kInt:
		return strconv.FormatInt(v.i, 10)
	case kString:
		return "hex2bin('" + hex.EncodeToString([]byte(v.s)) + "')"
	case kArray:
		var b strings.Builder
		b.WriteString("(function(){$a=[];")
		for _, e := range v.elems {
			if e.auto {
				b.WriteString("$a[]=")
				b.WriteString(e.val.php())
				b.WriteByte(';')
			} else {
				b.WriteString("$a[")
				b.WriteString(e.key.php())
				b.WriteString("]=")
				b.WriteString(e.val.php())
				b.WriteByte(';')
			}
		}
		b.WriteString("return $a;})()")
		return b.String()
	default:
		return "null"
	}
}

func (v lit) goVal(t *testing.T, z *zone.Zone) types.Value {
	t.Helper()
	switch v.k {
	case kNull:
		return types.NewNull()
	case kBool:
		return types.NewBool(v.b)
	case kInt:
		return types.NewLong(v.i)
	case kString:
		s, err := newStr(z, v.s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	case kArray:
		a, err := newArray(z)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range v.elems {
			val := e.val.goVal(t, z)
			if e.auto {
				if err = appendVal(z, &a, val); err != nil {
					t.Fatal(err)
				}
			} else if err = types.ArraySet(z, &a, e.key.goVal(t, z), val, nil); err != nil {
				t.Fatal(err)
			}
		}
		return a
	default:
		t.Fatal("lit")
		return types.Value{}
	}
}

func isRef(refs []int, i int) bool {
	return slices.Contains(refs, i)
}

func ser(z *zone.Zone, v types.Value) string {
	v = deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		return "N"
	case types.TypeTrue:
		return "T"
	case types.TypeFalse:
		return "F"
	case types.TypeLong:
		return "I" + strconv.FormatInt(v.ToLong(), 10)
	case types.TypeDouble:
		f := v.ToDouble()
		if math.IsNaN(f) {
			return "Dnan"
		}
		if math.IsInf(f, 1) {
			return "Dinf"
		}
		if math.IsInf(f, -1) {
			return "D-inf"
		}
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(f))
		return "D" + hex.EncodeToString(buf[:])
	case types.TypeString:
		return "S" + hex.EncodeToString([]byte(types.StringData(z, v)))
	case types.TypeArray:
		var b strings.Builder
		b.WriteByte('A')
		types.ArrayForEach(z, v, func(k, val types.Value) bool {
			k = deref(z, k)
			if k.IsLong() {
				b.WriteByte('I')
				b.WriteString(strconv.FormatInt(k.ToLong(), 10))
			} else {
				b.WriteByte('S')
				b.WriteString(hex.EncodeToString([]byte(types.StringData(z, k))))
			}
			b.WriteByte('=')
			b.WriteString(ser(z, val))
			b.WriteByte(';')
			return true
		})
		return b.String()
	default:
		return "U"
	}
}

func phpPrelude() string {
	return `<?php
function ser($v) {
    if (is_null($v)) return 'N';
    if (is_bool($v)) return $v ? 'T' : 'F';
    if (is_int($v)) return 'I'.$v;
    if (is_float($v)) {
        if (is_nan($v)) return 'Dnan';
        if (is_infinite($v)) return $v > 0 ? 'Dinf' : 'D-inf';
        return 'D'.bin2hex(pack('e', $v));
    }
    if (is_string($v)) return 'S'.bin2hex($v);
    if (is_array($v)) {
        $o = 'A';
        foreach ($v as $k => $item) {
            if (is_int($k)) $o .= 'I'.$k;
            else $o .= 'S'.bin2hex((string)$k);
            $o .= '='.ser($item).';';
        }
        return $o;
    }
    return 'U';
}
`
}

func buildPHP(cases []call) string {
	var b strings.Builder
	b.WriteString(phpPrelude())
	for i, c := range cases {
		b.WriteString("try {\n")
		for _, r := range c.refs {
			b.WriteString("$r" + strconv.Itoa(r) + "=null;\n")
		}
		b.WriteString("$ret=" + c.fn + "(")
		for j, a := range c.args {
			if j > 0 {
				b.WriteByte(',')
			}
			if isRef(c.refs, j) {
				b.WriteString("$r" + strconv.Itoa(j))
			} else {
				b.WriteString(a.php())
			}
		}
		b.WriteString(");\n")
		b.WriteString("echo \"OK\\t\".ser($ret)")
		for _, r := range c.refs {
			b.WriteString(".\"\\t\".ser($r" + strconv.Itoa(r) + ")")
		}
		b.WriteString(";\n")
		b.WriteString("} catch (Throwable $e) {\n")
		b.WriteString("echo \"EX\\t\".get_class($e).\"\\t\".bin2hex($e->getMessage());\n")
		b.WriteString("}\n")
		b.WriteString("echo \"\\n\";\n")
		_ = i
	}
	return b.String()
}

func hashCases() []call {
	return []call{
		{fn: "md5", args: []lit{lstr("abc")}},
		{fn: "md5", args: []lit{lstr("abc"), lbool(true)}},
		{fn: "md5", args: []lit{lstr("")}},
		{fn: "sha1", args: []lit{lstr("abc")}},
		{fn: "sha1", args: []lit{lstr("abc"), lbool(true)}},
		{fn: "hash", args: []lit{lstr("md5"), lstr("abc")}},
		{fn: "hash", args: []lit{lstr("MD5"), lstr("abc")}},
		{fn: "hash", args: []lit{lstr("sha256"), lstr("abc")}},
		{fn: "hash", args: []lit{lstr("sha384"), lstr("abc")}},
		{fn: "hash", args: []lit{lstr("sha512"), lstr("abc")}},
		{fn: "hash", args: []lit{lstr("crc32b"), lstr("abc")}},
		{fn: "hash", args: []lit{lstr("sha256"), lstr("abc"), lbool(true)}},
		{fn: "hash", args: []lit{lstr("nope"), lstr("x")}},
		{fn: "hash_hmac", args: []lit{lstr("sha256"), lstr("data"), lstr("key")}},
		{fn: "hash_hmac", args: []lit{lstr("md5"), lstr("data"), lstr("key")}},
		{fn: "hash_hmac", args: []lit{lstr("sha1"), lstr("data"), lstr("key")}},
		{fn: "hash_hmac", args: []lit{lstr("crc32b"), lstr("data"), lstr("key")}},
		{fn: "hash_equals", args: []lit{lstr("ab"), lstr("ab")}},
		{fn: "hash_equals", args: []lit{lstr("ab"), lstr("ac")}},
		{fn: "hash_equals", args: []lit{lstr("ab"), lstr("a")}},
		{fn: "hash_equals", args: []lit{lint(1), lstr("a")}},
		{fn: "crc32", args: []lit{lstr("abc")}},
		{fn: "crc32", args: []lit{lstr("a")}},
		{fn: "base64_encode", args: []lit{lstr("abc")}},
		{fn: "base64_encode", args: []lit{lstr("a")}},
		{fn: "base64_decode", args: []lit{lstr("YQ=="), lbool(true)}},
		{fn: "base64_decode", args: []lit{lstr("YQ"), lbool(true)}},
		{fn: "base64_decode", args: []lit{lstr("YQ="), lbool(true)}},
		{fn: "base64_decode", args: []lit{lstr("YQ!!"), lbool(false)}},
		{fn: "base64_decode", args: []lit{lstr("YQ!!"), lbool(true)}},
		{fn: "base64_decode", args: []lit{lstr("Y Q=="), lbool(true)}},
		{fn: "base64_decode", args: []lit{lstr(""), lbool(true)}},
		{fn: "urlencode", args: []lit{lstr("hello world!*~()")}},
		{fn: "rawurlencode", args: []lit{lstr("hello world!*~()")}},
		{fn: "urldecode", args: []lit{lstr("a+b%20c")}},
		{fn: "rawurldecode", args: []lit{lstr("a+b%20c")}},
		{fn: "urldecode", args: []lit{lstr("%zz%41%4")}},
	}
}

func pcreCases() []call {
	grep := larr(ap(lstr("a")), ap(lstr("bb")), ap(lstr("a1")), ap(lstr("ccc")))
	return []call{
		{fn: "preg_match", args: []lit{lstr("/(a)(b)/"), lstr("xaby"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/(?P<foo>\\w+)/"), lstr("abc-def"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/(a+)/"), lstr("xxaaa"), lnull(), lint(256)}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/z/"), lstr("abc"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/foo(?=bar)/"), lstr("foobar"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/(\\w+)\\s+\\1/"), lstr("hello hello"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/(é+)/u"), lstr("xxéé"), lnull(), lint(256)}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("foo"), lstr("a"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/a/"), lstr("xxa"), lnull(), lint(0), lint(2)}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/a/"), lstr("abc"), lnull(), lint(0), lint(5)}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/(a)|(b)/"), lstr("b"), lnull()}, refs: []int{2}},
		{fn: "preg_match_all", args: []lit{lstr("/(\\w)(\\d)/"), lstr("a1b2c3"), lnull()}, refs: []int{2}},
		{fn: "preg_match_all", args: []lit{lstr("/(\\w)(\\d)/"), lstr("a1b2"), lnull(), lint(2)}, refs: []int{2}},
		{fn: "preg_match_all", args: []lit{lstr("/(a+)/"), lstr("aa-aaa"), lnull(), lint(256)}, refs: []int{2}},
		{fn: "preg_match_all", args: []lit{lstr("/z/"), lstr("abc"), lnull()}, refs: []int{2}},
		{fn: "preg_replace", args: []lit{lstr("/(\\w+)/"), lstr("X$1-Y"), lstr("ab cd"), lint(-1), lnull()}, refs: []int{4}},
		{fn: "preg_replace", args: []lit{lstr("/(\\w+)/"), lstr("[$0|$1|\\1]"), lstr("ab")}, refs: nil},
		{fn: "preg_replace", args: []lit{lstr("/(a)/"), lstr("\\\\$1"), lstr("a")}},
		{fn: "preg_replace", args: []lit{lstr("/a/"), lstr("b"), lstr("aaa"), lint(2), lnull()}, refs: []int{4}},
		{fn: "preg_replace", args: []lit{lstr("/a/"), lstr("b"), lstr("aaa"), lint(0), lnull()}, refs: []int{4}},
		{fn: "preg_replace", args: []lit{lstr("/(\\w+)(\\d)/"), lstr("${2}-${1}"), lstr("a1 b2")}},
		{fn: "preg_split", args: []lit{lstr("/[:,]/"), lstr("a:b,c")}},
		{fn: "preg_split", args: []lit{lstr("/[:,]/"), lstr(":a::b,"), lint(-1), lint(1)}},
		{fn: "preg_split", args: []lit{lstr("/([:,])/"), lstr("a:b,c"), lint(-1), lint(2)}},
		{fn: "preg_split", args: []lit{lstr("/([:,])/"), lstr(":a::b,"), lint(-1), lint(3)}},
		{fn: "preg_quote", args: []lit{lstr("a.b*c"), lstr("/")}},
		{fn: "preg_quote", args: []lit{lstr("a#b"), lstr("#")}},
		{fn: "preg_quote", args: []lit{lstr(".\\+*?[^]$(){}=!<>|:-#")}},
		{fn: "preg_grep", args: []lit{lstr("/^a/"), grep}},
		{fn: "preg_grep", args: []lit{lstr("/^a/"), grep, lint(1)}},
		{fn: "preg_grep", args: []lit{lstr("/(\\w)\\1/"), larr(ap(lstr("aa")), ap(lstr("ab")))}},
		{fn: "preg_match", args: []lit{lstr("/(?<x>a)|(?<x>b)/J"), lstr("b"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/a.+a/U"), lstr("abaca"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/^b/m"), lstr("a\nb"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/a.b/s"), lstr("a\nb"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/a b # comment\n/x"), lstr("ab"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/a$/D"), lstr("a\n"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/b/A"), lstr("ab"), lnull()}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/ABC/i"), lstr("abc"), lnull()}, refs: []int{2}},
		{fn: "preg_replace", args: []lit{larr(ap(lstr("/a/")), ap(lstr("/b/"))), larr(ap(lstr("X")), ap(lstr("Y"))), lstr("ab"), lint(-1), lnull()}, refs: []int{4}},
		{fn: "preg_replace", args: []lit{larr(ap(lstr("/a/")), ap(lstr("/b/"))), lstr("Z"), lstr("ab"), lint(-1), lnull()}, refs: []int{4}},
		{fn: "preg_replace", args: []lit{larr(ap(lstr("/a/")), ap(lstr("/b/"))), larr(ap(lstr("X"))), lstr("ab"), lint(-1), lnull()}, refs: []int{4}},
		{fn: "preg_replace", args: []lit{lstr("/a/"), lstr("X"), larr(kv(lstr("k"), lstr("a")), kv(lint(2), lstr("b"))), lint(-1), lnull()}, refs: []int{4}},
		{fn: "preg_replace", args: []lit{lstr("/a/"), larr(ap(lstr("X"))), lstr("a")}},
	}
}

func mbCases() []call {
	return []call{
		{fn: "mb_strlen", args: []lit{lstr("é你🙂")}},
		{fn: "mb_strlen", args: []lit{lstr("abc")}},
		{fn: "mb_strlen", args: []lit{lstr("a"), lstr("nope")}},
		{fn: "mb_strlen", args: []lit{lstr("é"), lstr("UTF-8")}},
		{fn: "mb_strlen", args: []lit{lstr("é"), lstr("utf8")}},
		{fn: "mb_strlen", args: []lit{lstr("\xff")}},
		{fn: "mb_strlen", args: []lit{lstr("\x80\xc0\xaf\xe2\x82\xf0\x9f\x99")}},
		{fn: "mb_strlen", args: []lit{lstr("é\xff你\x80🙂")}},
		{fn: "mb_substr", args: []lit{lstr("é你🙂"), lint(1), lint(2)}},
		{fn: "mb_substr", args: []lit{lstr("é你🙂"), lint(-2)}},
		{fn: "mb_substr", args: []lit{lstr("abcdef"), lint(1), lint(-2)}},
		{fn: "mb_substr", args: []lit{lstr("abcdef"), lint(-3), lint(-1)}},
		{fn: "mb_substr", args: []lit{lstr("abc"), lint(-10), lint(2)}},
		{fn: "mb_substr", args: []lit{lstr("abc"), lint(10), lint(2)}},
		{fn: "mb_strpos", args: []lit{lstr("é你🙂你"), lstr("你")}},
		{fn: "mb_strpos", args: []lit{lstr("abc"), lstr("z")}},
		{fn: "mb_strpos", args: []lit{lstr("é你🙂你"), lstr("你"), lint(2)}},
		{fn: "mb_strpos", args: []lit{lstr("abc"), lstr("a"), lint(5)}},
		{fn: "mb_strrpos", args: []lit{lstr("abcabc"), lstr("a"), lint(2)}},
		{fn: "mb_strrpos", args: []lit{lstr("abcabc"), lstr("a"), lint(-2)}},
		{fn: "mb_strrpos", args: []lit{lstr("é你🙂你"), lstr("你")}},
		{fn: "mb_strtolower", args: []lit{lstr("ÉTÉ")}},
		{fn: "mb_strtoupper", args: []lit{lstr("été")}},
		{fn: "mb_strtolower", args: []lit{lstr("İ")}},
		{fn: "mb_strtoupper", args: []lit{lstr("ß")}},
		{fn: "mb_strtoupper", args: []lit{lstr("ﬁ")}},
		{fn: "mb_check_encoding", args: []lit{lstr("é")}},
		{fn: "mb_check_encoding", args: []lit{lstr("\xff")}},
		{fn: "mb_check_encoding", args: []lit{lstr("🙂")}},
	}
}

func dateCases() []call {
	const ts = 1700000000
	return []call{
		{fn: "gmdate", args: []lit{lstr("Y-m-d H:i:s"), lint(ts)}},
		{fn: "gmdate", args: []lit{lstr("Y y m n d j H G h g i s A a D l F M w N z L U"), lint(ts)}},
		{fn: "gmdate", args: []lit{lstr("c"), lint(ts)}},
		{fn: "gmdate", args: []lit{lstr("r"), lint(ts)}},
		{fn: "gmdate", args: []lit{lstr("P O T Z"), lint(ts)}},
		{fn: "gmdate", args: []lit{lstr("Y\\-m\\-d"), lint(ts)}},
		{fn: "date", args: []lit{lstr("Y-m-d H:i:s T P O"), lint(ts)}},
		{fn: "date", args: []lit{lstr("c"), lint(ts)}},
		{fn: "gmmktime", args: []lit{lint(1), lint(2), lint(3), lint(4), lint(5), lint(2020)}},
		{fn: "mktime", args: []lit{lint(1), lint(2), lint(3), lint(4), lint(5), lint(2020)}},
		{fn: "mktime", args: []lit{lint(0), lint(0), lint(0), lint(1), lint(1), lint(99)}},
		{fn: "mktime", args: []lit{lint(0), lint(0), lint(0), lint(1), lint(1), lint(69)}},
		{fn: "strtotime", args: []lit{lstr("2020-04-05")}},
		{fn: "strtotime", args: []lit{lstr("2020-04-05 01:02:03")}},
		{fn: "strtotime", args: []lit{lstr("2020-04-05T01:02:03")}},
		{fn: "strtotime", args: []lit{lstr("2020-04-05 01:02:03+00:00")}},
		{fn: "strtotime", args: []lit{lstr("+1 day"), lint(ts)}},
		{fn: "strtotime", args: []lit{lstr("-2 hours"), lint(ts)}},
		{fn: "strtotime", args: []lit{lstr("+1 day -2 hours"), lint(ts)}},
		{fn: "strtotime", args: []lit{lstr("+1 month"), lint(ts)}},
		{fn: "strtotime", args: []lit{lstr("Thu, 05 Apr 2012 12:00:00 +0000")}},
		{fn: "strtotime", args: []lit{lstr("not a date")}},
		{fn: "gmdate", args: []lit{lstr("L"), lint(1582934400)}},
		{fn: "gmdate", args: []lit{lstr("z"), lint(1582934400)}},
		{fn: "gmdate", args: []lit{lstr("w N D l"), lint(1577836800)}},
		{fn: "gmdate", args: []lit{lstr(`Y-m-d\TH:i:sP`), lint(ts)}},
		{fn: "gmdate", args: []lit{lstr("jS F Y, t W o u e I p"), lint(ts)}},
		{fn: "gmdate", args: []lit{lstr("jS jS jS jS t W o"), lint(1609459200)}},
		{fn: "date", args: []lit{lstr("jS e I p T"), lint(1704585600)}},
		{fn: "gmdate", args: []lit{lstr("S t"), lint(1709251200)}},
		{fn: "gmdate", args: []lit{lstr("S"), lint(1699660800)}},
		{fn: "gmdate", args: []lit{lstr("S"), lint(1700697600)}},
		{fn: "gmdate", args: []lit{lstr("S"), lint(1701043200)}},
	}
}

func TestPHPDifferential(t *testing.T) {
	file := filepath.Join(t.TempDir(), "digest.txt")
	if err := os.WriteFile(file, []byte("contenu de test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	families := []family{
		{"hash", hashCases()},
		{"pcre", pcreCases()},
		{"mbstring", mbCases()},
		{"date", dateCases()},
		{"zlib", zlibCases()},
		{"mbstring_wp", mbWPCases()},
		{"iconv", iconvCases()},
		{"date_wp", dateWPCases()},
		{"crypto_wp", cryptoWPCases(file)},
	}
	runDifferential(t, families, 60)
	if re2Hits.Load() == 0 || pcre2Hits.Load() == 0 {
		t.Fatalf("paths re2=%d pcre2=%d", re2Hits.Load(), pcre2Hits.Load())
	}
}

type family struct {
	name  string
	cases []call
}

// lookupChain suit l'ordre de résolution de vm/ext_bridge.go restreint à ext puis datetime.
func lookupChain(name string) (stdlib.NativeFunc, bool) {
	if fn, ok := Lookup(name); ok {
		return fn, true
	}
	return datetime.Lookup(name)
}

func phpOracle(t *testing.T, script string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oracle.php")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/usr/bin/php", "-n",
		"-d", "extension=/usr/lib/php/20230831/mbstring.so",
		"-d", "extension=/usr/lib/php/20230831/iconv.so",
		"-d", "extension=/usr/lib/php/20230831/ctype.so",
		"-d", "date.timezone=UTC",
		"-d", "display_errors=stderr",
		path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("php: %v\n%s", err, stderr.String())
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

func runDifferential(t *testing.T, families []family, minTotal int) {
	t.Helper()
	var all []call
	var owner []string
	for _, fam := range families {
		if len(fam.cases) < 8 {
			t.Fatalf("%s has %d cases", fam.name, len(fam.cases))
		}
		for _, c := range fam.cases {
			all = append(all, c)
			owner = append(owner, fam.name)
		}
	}
	if len(all) < minTotal {
		t.Fatalf("cases %d < %d", len(all), minTotal)
	}
	lines := phpOracle(t, buildPHP(all))
	if len(lines) != len(all) {
		t.Fatalf("php lines %d want %d\n%s", len(lines), len(all), truncate(strings.Join(lines, "\n"), 2000))
	}
	shown := 0
	for i, c := range all {
		z := zone.New(1 << 20)
		args := make([]types.Value, len(c.args))
		var refs []types.Value
		for j, a := range c.args {
			if isRef(c.refs, j) {
				r, err := types.NewReference(z, types.NewNull())
				if err != nil {
					t.Fatal(err)
				}
				args[j] = r
				refs = append(refs, r)
			} else {
				args[j] = a.goVal(t, z)
			}
		}
		fn, ok := lookupChain(c.fn)
		if !ok {
			t.Fatalf("missing %s", c.fn)
		}
		var buf bytes.Buffer
		got, gerr := fn(z, &buf, args)
		fields := strings.Split(lines[i], "\t")
		if fields[0] == "EX" {
			if len(fields) != 3 {
				t.Fatalf("bad ex %q", lines[i])
			}
			msg, err := hex.DecodeString(fields[2])
			if err != nil {
				t.Fatal(err)
			}
			if gerr == nil || exceptionClassAny(gerr) != fields[1] || gerr.Error() != string(msg) {
				shown++
				if shown <= 30 {
					gotC, gotM := "", ""
					if gerr != nil {
						gotC, gotM = exceptionClassAny(gerr), gerr.Error()
					} else {
						gotM = ser(z, got)
					}
					t.Errorf("%s %s #%d: exception got %s %q want %s %q", owner[i], c.fn, i, gotC, gotM, fields[1], msg)
				}
			}
			continue
		}
		if fields[0] != "OK" {
			t.Fatalf("bad line %q", lines[i])
		}
		if gerr != nil {
			shown++
			if shown <= 30 {
				t.Errorf("%s %s #%d: unexpected %s %q", owner[i], c.fn, i, exceptionClassAny(gerr), gerr.Error())
			}
			continue
		}
		want := fields[1:]
		gotSer := []string{ser(z, got)}
		for _, r := range refs {
			gotSer = append(gotSer, ser(z, r))
		}
		if len(want) != len(gotSer) {
			t.Fatalf("%s %s: field count %d want %d (%q)", owner[i], c.fn, len(gotSer), len(want), lines[i])
		}
		for k := range want {
			if gotSer[k] != want[k] {
				shown++
				if shown <= 30 {
					t.Errorf("%s %s #%d field %d\n got %s\nwant %s", owner[i], c.fn, i, k, truncate(gotSer[k], 300), truncate(want[k], 300))
				}
			}
		}
	}
	if shown > 0 {
		t.Fatalf("%d mismatches", shown)
	}
}

func exceptionClassAny(err error) string {
	if c := ExceptionClass(err); c != "" {
		return c
	}
	if de, ok := errors.AsType[datetime.DateError](err); ok {
		return de.Class
	}
	return ""
}

func TestLookupNoAlloc(t *testing.T) {
	if _, ok := Lookup("PREG_MATCH"); !ok {
		t.Fatal("missing")
	}
	if _, ok := Lookup("no_such"); ok {
		t.Fatal("unexpected")
	}
	allocs := testing.AllocsPerRun(200, func() {
		Lookup("Hash_Hmac")
		Lookup("Mb_Strlen")
		Lookup("missing")
	})
	if allocs != 0 {
		t.Fatalf("allocs %v", allocs)
	}
}

func TestConstants(t *testing.T) {
	c := Constants()
	if c["PREG_OFFSET_CAPTURE"].ToLong() != 256 {
		t.Fatal(c["PREG_OFFSET_CAPTURE"])
	}
	if c["PREG_SPLIT_DELIM_CAPTURE"].ToLong() != 2 {
		t.Fatal("split delim")
	}
	if types.StringData(nil, c["DATE_ATOM"]) != `Y-m-d\TH:i:sP` {
		t.Fatalf("DATE_ATOM %q", types.StringData(nil, c["DATE_ATOM"]))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Flux produits par /usr/bin/php (zlib 1.3) : gzdeflate("hello hello hello"), gzcompress("hello"), gzencode("hello"), gzencode("b").
var (
	fixRaw  = mustHex("cb48cdc9c957c8409000")
	fixZlib = mustHex("789ccb48cdc9c90700062c0215")
	fixGzip = mustHex("1f8b0800000000000003cb48cdc9c9070086a6103605000000")
)

func mustHex(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func zlibCases() []call {
	return []call{
		{fn: "gzdeflate", args: []lit{lstr("x"), lint(10)}},
		{fn: "gzdeflate", args: []lit{lstr("x"), lint(-1), lint(99)}},
		{fn: "gzcompress", args: []lit{lstr("x"), lint(-2)}},
		{fn: "gzencode", args: []lit{lstr("x"), lint(5), lint(1)}},
		{fn: "zlib_encode", args: []lit{lstr("x"), lint(99)}},
		{fn: "zlib_encode", args: []lit{lstr("x")}},
		{fn: "zlib_encode", args: []lit{lstr("x"), lint(15), lint(11)}},
		{fn: "gzinflate", args: []lit{lstr("garbage")}},
		{fn: "gzinflate", args: []lit{lstr("")}},
		{fn: "gzinflate", args: []lit{lstr(fixRaw)}},
		{fn: "gzinflate", args: []lit{lstr(fixRaw), lint(3)}},
		{fn: "gzinflate", args: []lit{lstr(fixRaw), lint(17)}},
		{fn: "gzinflate", args: []lit{lstr(fixRaw), lint(-1)}},
		{fn: "gzinflate", args: []lit{lstr(fixRaw + "trailing")}},
		{fn: "gzinflate", args: []lit{lstr(fixRaw[:4])}},
		{fn: "gzuncompress", args: []lit{lstr(fixZlib)}},
		{fn: "gzuncompress", args: []lit{lstr(fixRaw)}},
		{fn: "gzuncompress", args: []lit{lstr(fixZlib + "x")}},
		{fn: "gzuncompress", args: []lit{lstr(fixZlib[:len(fixZlib)-1])}},
		{fn: "gzdecode", args: []lit{lstr(fixGzip)}},
		{fn: "gzdecode", args: []lit{lstr(fixZlib)}},
		{fn: "gzdecode", args: []lit{lstr(fixGzip + fixGzip)}},
		{fn: "gzdecode", args: []lit{lstr(fixGzip), lint(4)}},
		{fn: "zlib_decode", args: []lit{lstr(fixZlib)}},
		{fn: "zlib_decode", args: []lit{lstr(fixGzip)}},
		{fn: "zlib_decode", args: []lit{lstr(fixRaw)}},
		{fn: "zlib_decode", args: []lit{lstr("nope")}},
	}
}

func mbWPCases() []call {
	title := []string{"hello world", "o'neil", "a.b c:d", "123abc", "ßtraße ﬁsh", "HELLO-wORLD", "ǆemal", "x_y z2a", "ÉCOLE élève", "a^b `c"}
	cases := []call{
		{fn: "mb_detect_encoding", args: []lit{lstr("abc")}},
		{fn: "mb_detect_encoding", args: []lit{lstr("é")}},
		{fn: "mb_detect_encoding", args: []lit{lstr("\xe9")}},
		{fn: "mb_detect_encoding", args: []lit{lstr("\xe9"), lnull(), lbool(true)}},
		{fn: "mb_detect_encoding", args: []lit{lstr("\xe9"), larr(ap(lstr("UTF-8")), ap(lstr("ISO-8859-1")))}},
		{fn: "mb_detect_encoding", args: []lit{lstr("é"), larr(ap(lstr("ISO-8859-1")), ap(lstr("UTF-8")))}},
		{fn: "mb_detect_encoding", args: []lit{lstr("é"), larr(ap(lstr("ISO-8859-1")), ap(lstr("UTF-8"))), lbool(true)}},
		{fn: "mb_detect_encoding", args: []lit{lstr("\x80abc"), lstr("UTF-8, ASCII")}},
		{fn: "mb_detect_encoding", args: []lit{lstr("abc"), lstr("UTF-8,ASCII"), lbool(true)}},
		{fn: "mb_detect_encoding", args: []lit{lstr("🙂"), lstr("ISO-8859-1,UTF-8")}},
		{fn: "mb_detect_encoding", args: []lit{lstr("\xf0\xaa\xaa\xaa"), lstr("UTF-8,ISO-8859-1"), lbool(true)}},
		{fn: "mb_detect_encoding", args: []lit{lstr("a"), lstr("nope")}},
		{fn: "mb_detect_encoding", args: []lit{lstr("a"), larr()}},
		{fn: "mb_convert_encoding", args: []lit{lstr("é"), lstr("ISO-8859-1")}},
		{fn: "mb_convert_encoding", args: []lit{lstr("\xe9t\xe9"), lstr("UTF-8"), lstr("ISO-8859-1")}},
		{fn: "mb_convert_encoding", args: []lit{lstr("a\xffb"), lstr("UTF-8")}},
		{fn: "mb_convert_encoding", args: []lit{lstr("€"), lstr("ISO-8859-1")}},
		{fn: "mb_convert_encoding", args: []lit{lstr("€‘"), lstr("Windows-1252")}},
		{fn: "mb_convert_encoding", args: []lit{lstr("\x80\x81"), lstr("UTF-8"), lstr("Windows-1252")}},
		{fn: "mb_convert_encoding", args: []lit{lstr("\u0081€"), lstr("Windows-1252")}},
		{fn: "mb_detect_encoding", args: []lit{lstr("\x81\xe9"), lstr("Windows-1252,UTF-8"), lbool(true)}},
		{fn: "mb_convert_encoding", args: []lit{lstr("é"), lstr("nope")}},
		{fn: "mb_convert_encoding", args: []lit{lstr("é"), lstr("UTF-8"), lstr("nope")}},
		{fn: "mb_convert_encoding", args: []lit{lstr("\xe9"), lstr("UTF-8"), lstr("UTF-8,ISO-8859-1")}},
		{fn: "mb_convert_encoding", args: []lit{larr(kv(lstr("clé"), lstr("été")), ap(lstr("à"))), lstr("ISO-8859-1")}},
		{fn: "mb_convert_case", args: []lit{lstr("x"), lint(8)}},
		{fn: "mb_convert_case", args: []lit{lstr("été"), lint(0), lstr("nope")}},
		{fn: "mb_stripos", args: []lit{lstr("ÉTÉ été"), lstr("été")}},
		{fn: "mb_stripos", args: []lit{lstr("ÉTÉ été"), lstr("été"), lint(1)}},
		{fn: "mb_stripos", args: []lit{lstr("ABC"), lstr("b")}},
		{fn: "mb_stripos", args: []lit{lstr("abc"), lstr("A"), lint(5)}},
		{fn: "mb_stripos", args: []lit{lstr("abc"), lstr("z")}},
		{fn: "mb_strripos", args: []lit{lstr("aAbBaA"), lstr("a")}},
		{fn: "mb_strripos", args: []lit{lstr("aAbBaA"), lstr("A"), lint(-3)}},
		{fn: "mb_strstr", args: []lit{lstr("user@example.com"), lstr("@")}},
		{fn: "mb_strstr", args: []lit{lstr("user@example.com"), lstr("@"), lbool(true)}},
		{fn: "mb_strstr", args: []lit{lstr("abc"), lstr("z")}},
		{fn: "mb_strstr", args: []lit{lstr("abc"), lstr("")}},
		{fn: "mb_stristr", args: []lit{lstr("HeLLo"), lstr("ll")}},
		{fn: "mb_stristr", args: []lit{lstr("xÉTÉ"), lstr("té"), lbool(true)}},
		{fn: "mb_stristr", args: []lit{lstr("\u212aelvin K"), lstr("e")}},
		{fn: "mb_strrchr", args: []lit{lstr("a/b/c"), lstr("/")}},
		{fn: "mb_strrchr", args: []lit{lstr("a/b/c"), lstr("/"), lbool(true)}},
		{fn: "mb_strrchr", args: []lit{lstr("abc"), lstr("")}},
		{fn: "mb_substr_count", args: []lit{lstr("ababab"), lstr("ab")}},
		{fn: "mb_substr_count", args: []lit{lstr("aaa"), lstr("aa")}},
		{fn: "mb_substr_count", args: []lit{lstr("été"), lstr("é")}},
		{fn: "mb_substr_count", args: []lit{lstr("x"), lstr("")}},
		{fn: "mb_strwidth", args: []lit{lstr("aé你🙂😀ｱ")}},
		{fn: "mb_strwidth", args: []lit{lstr("你好，世界！")}},
		{fn: "mb_strwidth", args: []lit{lstr("…—«»")}},
		{fn: "mb_strimwidth", args: []lit{lstr("Hello World"), lint(0), lint(8), lstr("...")}},
		{fn: "mb_strimwidth", args: []lit{lstr("你好世界abc"), lint(0), lint(5), lstr("…")}},
		{fn: "mb_strimwidth", args: []lit{lstr("abc"), lint(-2), lint(5)}},
		{fn: "mb_strimwidth", args: []lit{lstr("Hello"), lint(0), lint(10), lstr("...")}},
		{fn: "mb_strimwidth", args: []lit{lstr("Hello World"), lint(0), lint(3), lstr(".....")}},
		{fn: "mb_strimwidth", args: []lit{lstr("Hello World"), lint(-5), lint(4), lstr("..")}},
		{fn: "mb_strimwidth", args: []lit{lstr("abc"), lint(5), lint(1)}},
		{fn: "mb_strimwidth", args: []lit{lstr("你好世界"), lint(1), lint(4)}},
		{fn: "mb_strlen", args: []lit{lstr("\xe9t\xe9"), lstr("ISO-8859-1")}},
	}
	for _, s := range title {
		for mode := range int64(8) {
			cases = append(cases, call{fn: "mb_convert_case", args: []lit{lstr(s), lint(mode)}})
		}
	}
	return cases
}

// mbSettingCases modifie l'état du processus PHP : la famille s'exécute dans un processus séparé, lectures d'abord.
// ext ne conserve aucun réglage ; seules les valeurs de retour des réglages sont comparées.
func mbSettingCases() []call {
	return []call{
		{fn: "mb_internal_encoding"},
		{fn: "mb_regex_encoding"},
		{fn: "mb_language"},
		{fn: "mb_detect_order"},
		{fn: "mb_internal_encoding", args: []lit{lstr("UTF-8")}},
		{fn: "mb_internal_encoding", args: []lit{lstr("nope")}},
		{fn: "mb_regex_encoding", args: []lit{lstr("nope")}},
		{fn: "mb_language", args: []lit{lstr("nope")}},
		{fn: "mb_detect_order", args: []lit{lstr("nope")}},
		{fn: "mb_internal_encoding", args: []lit{lstr("latin1")}},
		{fn: "mb_regex_encoding", args: []lit{lstr("UTF-8")}},
		{fn: "mb_language", args: []lit{lstr("uni")}},
		{fn: "mb_language", args: []lit{lstr("English")}},
		{fn: "mb_detect_order", args: []lit{lstr("UTF-8, ASCII")}},
		{fn: "iconv_set_encoding", args: []lit{lstr("internal_encoding"), lstr("UTF-8")}},
	}
}

func TestPHPDifferentialSettings(t *testing.T) {
	runDifferential(t, []family{{"mbstring_settings", mbSettingCases()}}, 8)
}

func iconvCases() []call {
	return []call{
		{fn: "iconv", args: []lit{lstr("UTF-8"), lstr("ISO-8859-1"), lstr("été")}},
		{fn: "iconv", args: []lit{lstr("UTF-8"), lstr("ISO-8859-1"), lstr("é€")}},
		{fn: "iconv", args: []lit{lstr("UTF-8"), lstr("ISO-8859-1//IGNORE"), lstr("aé€b")}},
		{fn: "iconv", args: []lit{lstr("UTF-8"), lstr("ASCII//TRANSLIT"), lstr("aé€b Œuvre – «ok» ½ 你")}},
		{fn: "iconv", args: []lit{lstr("UTF-8"), lstr("ISO-8859-1//TRANSLIT"), lstr("€a‘")}},
		{fn: "iconv", args: []lit{lstr("UTF-8"), lstr("ASCII//TRANSLIT//IGNORE"), lstr("a你b")}},
		{fn: "iconv", args: []lit{lstr("UTF-8"), lstr("WINDOWS-1252"), lstr("aé€b")}},
		{fn: "iconv", args: []lit{lstr("WINDOWS-1252"), lstr("UTF-8"), lstr("\x80\x81\xe9")}},
		{fn: "iconv", args: []lit{lstr("CP1252"), lstr("UTF-8"), lstr("\x80\xe9\x9f")}},
		{fn: "iconv", args: []lit{lstr("UTF-8"), lstr("UTF-8"), lstr("a\xffb")}},
		{fn: "iconv", args: []lit{lstr("UTF-8"), lstr("UTF-8//IGNORE"), lstr("a\xffb")}},
		{fn: "iconv", args: []lit{lstr("nope"), lstr("UTF-8"), lstr("a")}},
		{fn: "iconv", args: []lit{lstr("latin1"), lstr("utf8"), lstr("\xe9")}},
		{fn: "iconv", args: []lit{lstr("ASCII"), lstr("UTF-8"), lstr("a\xe9")}},
		{fn: "iconv_strlen", args: []lit{lstr("é你")}},
		{fn: "iconv_strlen", args: []lit{lstr("a\xff")}},
		{fn: "iconv_strlen", args: []lit{lstr("\xe9"), lstr("ISO-8859-1")}},
		{fn: "iconv_strlen", args: []lit{lstr("a"), lstr("nope")}},
		{fn: "iconv_substr", args: []lit{lstr("é你🙂x"), lint(1), lint(2)}},
		{fn: "iconv_substr", args: []lit{lstr("abc"), lint(5)}},
		{fn: "iconv_substr", args: []lit{lstr("abc"), lint(-5), lint(2)}},
		{fn: "iconv_substr", args: []lit{lstr("abc"), lint(1), lint(-5)}},
		{fn: "iconv_substr", args: []lit{lstr("abc"), lint(3)}},
		{fn: "iconv_substr", args: []lit{lstr("abcdef"), lint(-3), lint(-1)}},
		{fn: "iconv_substr", args: []lit{lstr("a\xff"), lint(0)}},
		{fn: "iconv_strpos", args: []lit{lstr("é你é"), lstr("é"), lint(1)}},
		{fn: "iconv_strpos", args: []lit{lstr("abc"), lstr(""), lint(0)}},
		{fn: "iconv_strpos", args: []lit{lstr("abc"), lstr("a"), lint(-2)}},
		{fn: "iconv_strpos", args: []lit{lstr("abc"), lstr("a"), lint(5)}},
		{fn: "iconv_strpos", args: []lit{lstr("abcabc"), lstr("c"), lint(-1)}},
		{fn: "iconv_strrpos", args: []lit{lstr("é你é"), lstr("é")}},
		{fn: "iconv_strrpos", args: []lit{lstr("abc"), lstr("")}},
		{fn: "iconv_strrpos", args: []lit{lstr("abc"), lstr("z")}},
		{fn: "iconv_get_encoding"},
		{fn: "iconv_get_encoding", args: []lit{lstr("internal_encoding")}},
		{fn: "iconv_get_encoding", args: []lit{lstr("nope")}},
		{fn: "iconv_set_encoding", args: []lit{lstr("nope"), lstr("UTF-8")}},
		{fn: "iconv_mime_decode", args: []lit{lstr("Subject: =?UTF-8?B?w6l0w6k=?= =?ISO-8859-1?Q?caf=E9_x?= end")}},
		{fn: "iconv_mime_decode", args: []lit{lstr("=?UTF-8?Q?a_b?=")}},
		{fn: "iconv_mime_decode", args: []lit{lstr("plain text")}},
		{fn: "iconv_mime_decode", args: []lit{lstr("=?UTF-8?B?w6l0w6k=?="), lint(0), lstr("ISO-8859-1")}},
		{fn: "iconv_mime_decode", args: []lit{lstr("a =?utf-8?q?=C3=A9?=\r\n =?utf-8?q?t=C3=A9?= z")}},
	}
}

func dateWPCases() []call {
	const ts = 1700000000
	full := "%a %A %b %B %c %d %e %H %I %j %m %M %p %S %y %Y %Z %z %u %w %D %F %T %R %% %h %C %g %G %V %U %W %k %l %P %r %x %X %n %t"
	cases := []call{
		{fn: "gmstrftime", args: []lit{lstr(full), lint(ts)}},
		{fn: "strftime", args: []lit{lstr(full), lint(ts)}},
		// %s est exclu : la glibc le calcule par mktime(3) dans le fuseau TZ du processus PHP, non dans date.timezone.
		{fn: "strftime", args: []lit{lstr("%Y-%m-%d %H:%M:%S %Z %z"), lint(ts)}},
		{fn: "gmstrftime", args: []lit{lstr(""), lint(ts)}},
		{fn: "gmstrftime", args: []lit{lstr("%Q 100%"), lint(ts)}},
		{fn: "preg_last_error"},
		{fn: "preg_last_error_msg"},
		{fn: "getdate", args: []lit{lint(ts)}},
		{fn: "idate", args: []lit{lstr("Y"), lint(ts)}},
		{fn: "idate", args: []lit{lstr("z"), lint(ts)}},
		{fn: "idate", args: []lit{lstr("U"), lint(ts)}},
		{fn: "localtime", args: []lit{lint(ts), lbool(true)}},
		{fn: "date_default_timezone_get"},
	}
	// Jours de bascule de semaine et d'année ISO.
	for _, day := range []int64{1609459200, 1704067200, 1735603200, 1704585600, 1230681600} {
		cases = append(cases, call{fn: "gmstrftime", args: []lit{lstr("%Y-%m-%d %a %U %W %V %G %g %j %u %w"), lint(day)}})
	}
	return cases
}

func cryptoWPCases(file string) []call {
	cbc := "pr8k1DWoIoYlVy4NZ4zSmg=="
	gcm, gcmTag := mustHex("c1f1cde7b61dd23c"), mustHex("734a794d98ad50881c699e5860984817")
	return []call{
		{fn: "hash_hkdf", args: []lit{lstr("sha256"), lstr("key")}},
		{fn: "hash_hkdf", args: []lit{lstr("sha256"), lstr("key"), lint(10), lstr("info"), lstr("salt")}},
		{fn: "hash_hkdf", args: []lit{lstr("sha512"), lstr("key"), lint(100), lstr(""), lstr("s")}},
		{fn: "hash_hkdf", args: []lit{lstr("md5"), lstr("key"), lint(0), lstr("ctx")}},
		{fn: "hash_hkdf", args: []lit{lstr("sha256"), lstr("")}},
		{fn: "hash_hkdf", args: []lit{lstr("crc32b"), lstr("k")}},
		{fn: "hash_hkdf", args: []lit{lstr("sha256"), lstr("k"), lint(-1)}},
		{fn: "hash_hkdf", args: []lit{lstr("sha256"), lstr("k"), lint(255*32 + 1)}},
		{fn: "hash_pbkdf2", args: []lit{lstr("sha256"), lstr("pw"), lstr("salt"), lint(1000)}},
		{fn: "hash_pbkdf2", args: []lit{lstr("sha256"), lstr("pw"), lstr("salt"), lint(1000), lint(5)}},
		{fn: "hash_pbkdf2", args: []lit{lstr("sha1"), lstr("pw"), lstr("salt"), lint(2), lint(5), lbool(true)}},
		{fn: "hash_pbkdf2", args: []lit{lstr("sha512"), lstr("pw"), lstr("salt"), lint(3), lint(0), lbool(true)}},
		{fn: "hash_pbkdf2", args: []lit{lstr("md5"), lstr("pw"), lstr(""), lint(1), lint(40)}},
		{fn: "hash_pbkdf2", args: []lit{lstr("sha256"), lstr("pw"), lstr("salt"), lint(0)}},
		{fn: "hash_pbkdf2", args: []lit{lstr("sha256"), lstr("pw"), lstr("salt"), lint(1), lint(-1)}},
		{fn: "hash_pbkdf2", args: []lit{lstr("nope"), lstr("pw"), lstr("salt"), lint(1)}},
		{fn: "hash_pbkdf2", args: []lit{lstr("crc32b"), lstr("pw"), lstr("salt"), lint(1)}},
		{fn: "md5_file", args: []lit{lstr(file)}},
		{fn: "md5_file", args: []lit{lstr(file), lbool(true)}},
		{fn: "sha1_file", args: []lit{lstr(file)}},
		{fn: "hash_file", args: []lit{lstr("sha256"), lstr(file)}},
		{fn: "hash_file", args: []lit{lstr("crc32b"), lstr(file)}},
		{fn: "hash_hmac_file", args: []lit{lstr("sha256"), lstr(file), lstr("k")}},
		{fn: "md5_file", args: []lit{lstr("/nonexistent/x")}},
		{fn: "hash_file", args: []lit{lstr("nope"), lstr(file)}},
		{fn: "hash_hmac_file", args: []lit{lstr("crc32b"), lstr(file), lstr("k")}},
		{fn: "openssl_cipher_iv_length", args: []lit{lstr("aes-256-cbc")}},
		{fn: "openssl_cipher_iv_length", args: []lit{lstr("AES-128-GCM")}},
		{fn: "openssl_cipher_iv_length", args: []lit{lstr("aes-128-ecb")}},
		{fn: "openssl_cipher_iv_length", args: []lit{lstr("aes-192-ctr")}},
		{fn: "openssl_cipher_iv_length", args: []lit{lstr("nope")}},
		{fn: "openssl_cipher_iv_length", args: []lit{lstr("")}},
		{fn: "openssl_random_pseudo_bytes", args: []lit{lint(0)}},
		{fn: "openssl_decrypt", args: []lit{lstr(cbc), lstr("aes-256-cbc"), lstr("pass"), lint(0), lstr("1234567890123456")}},
		{fn: "openssl_decrypt", args: []lit{lstr(cbc), lstr("aes-256-cbc"), lstr("wrong"), lint(0), lstr("1234567890123456")}},
		{fn: "openssl_decrypt", args: []lit{lstr(cbc), lstr("aes-256-cbc"), lstr("pass"), lint(0), lstr("short")}},
		{fn: "openssl_decrypt", args: []lit{lstr(gcm), lstr("aes-128-gcm"), lstr("k"), lint(1), lstr("123456789012"), lstr(gcmTag), lstr("aad")}},
		{fn: "openssl_decrypt", args: []lit{lstr(gcm), lstr("aes-128-gcm"), lstr("k"), lint(1), lstr("123456789012"), lnull(), lstr("aad")}},
		{fn: "openssl_decrypt", args: []lit{lstr(gcm), lstr("aes-128-gcm"), lstr("k"), lint(1), lstr("123456789012"), lstr(gcmTag), lstr("bad")}},
		{fn: "openssl_decrypt", args: []lit{lstr(mustHex("96862e60")), lstr("aes-128-ctr"), lstr("k"), lint(1), lstr("1234567890123456")}},
		{fn: "openssl_decrypt", args: []lit{lstr(mustHex("b39890ef2c171ece919e98f2f6279480")), lstr("aes-128-ecb"), lstr("k"), lint(1)}},
		{fn: "openssl_decrypt", args: []lit{lstr("x"), lstr("nope"), lstr("k")}},
	}
}

// TestZlibRoundTrip croise les deux sens : PHP décompresse les flux Go, Go décompresse les flux PHP ; les en-têtes doivent coïncider.
func TestZlibRoundTrip(t *testing.T) {
	payloads := []string{"", "a", "hello hello hello", strings.Repeat("WordPress option_value ", 400), "\x00\xff\x10binaire\x00"}
	type enc struct {
		comp, decomp string
		header       int
	}
	encs := []enc{{"gzdeflate", "gzinflate", 0}, {"gzcompress", "gzuncompress", 2}, {"gzencode", "gzdecode", 10}}
	levels := []int64{-1, 0, 1, 6, 9}
	var script strings.Builder
	script.WriteString("<?php\n")
	type item struct {
		e       enc
		level   int64
		payload string
		goOut   string
	}
	var items []item
	for _, e := range encs {
		fn, _ := Lookup(e.comp)
		for _, lv := range levels {
			for _, p := range payloads {
				z := zone.New(1 << 22)
				ps, err := newStr(z, p)
				if err != nil {
					t.Fatal(err)
				}
				got, err := fn(z, nil, []types.Value{ps, types.NewLong(lv)})
				if err != nil {
					t.Fatal(err)
				}
				goOut := types.StringData(z, got)
				items = append(items, item{e, lv, p, goOut})
				script.WriteString("echo bin2hex(" + e.comp + "(hex2bin('" + hex.EncodeToString([]byte(p)) + "'), " + strconv.FormatInt(lv, 10) + ")), \"\\t\", bin2hex((string)" + e.decomp + "(hex2bin('" + hex.EncodeToString([]byte(goOut)) + "'))), \"\\n\";\n")
			}
		}
	}
	lines := phpOracle(t, script.String())
	if len(lines) != len(items) {
		t.Fatalf("php lines %d want %d", len(lines), len(items))
	}
	for i, it := range items {
		f := strings.Split(lines[i], "\t")
		phpOut, err := hex.DecodeString(f[0])
		if err != nil {
			t.Fatal(err)
		}
		back, err := hex.DecodeString(f[1])
		if err != nil {
			t.Fatal(err)
		}
		if string(back) != it.payload {
			t.Errorf("%s level %d: php cannot decode Go output (payload %d bytes)", it.e.comp, it.level, len(it.payload))
		}
		if !bytes.Equal(phpOut[:it.e.header], []byte(it.goOut[:it.e.header])) {
			t.Errorf("%s level %d: header got %x want %x", it.e.comp, it.level, it.goOut[:it.e.header], phpOut[:it.e.header])
		}
		z := zone.New(1 << 22)
		src, err := newStr(z, string(phpOut))
		if err != nil {
			t.Fatal(err)
		}
		dec, _ := Lookup(it.e.decomp)
		got, err := dec(z, nil, []types.Value{src})
		if err != nil {
			t.Fatal(err)
		}
		if ser(z, got) != "S"+hex.EncodeToString([]byte(it.payload)) {
			t.Errorf("%s level %d: Go cannot decode php output", it.e.decomp, it.level)
		}
	}
}

// TestDefaultTimezoneCoherence vérifie que date(), mktime(), strtotime() et strftime() de ext suivent date_default_timezone_set de datetime.
func TestDefaultTimezoneCoherence(t *testing.T) {
	const ts = 1700000000
	seq := []call{
		{fn: "date_default_timezone_get"},
		{fn: "date", args: []lit{lstr("Y-m-d H:i:s T P e"), lint(ts)}},
		{fn: "date_default_timezone_set", args: []lit{lstr("Europe/Paris")}},
		{fn: "date_default_timezone_get"},
		{fn: "date", args: []lit{lstr("Y-m-d H:i:s T P"), lint(ts)}},
		{fn: "date", args: []lit{lstr("c I"), lint(1690000000)}},
		{fn: "gmdate", args: []lit{lstr("Y-m-d H:i:s T"), lint(ts)}},
		{fn: "mktime", args: []lit{lint(12), lint(0), lint(0), lint(7), lint(14), lint(2024)}},
		{fn: "strtotime", args: []lit{lstr("2024-07-14 12:00:00")}},
		{fn: "strftime", args: []lit{lstr("%H:%M %Z %z"), lint(ts)}},
		{fn: "gmstrftime", args: []lit{lstr("%H:%M %Z %z"), lint(ts)}},
		{fn: "getdate", args: []lit{lint(ts)}},
		{fn: "idate", args: []lit{lstr("H"), lint(ts)}},
		{fn: "date_default_timezone_set", args: []lit{lstr("Nope/Nowhere")}},
		{fn: "date_default_timezone_set", args: []lit{lstr("America/New_York")}},
		{fn: "date", args: []lit{lstr("Y-m-d H:i:s T"), lint(ts)}},
		{fn: "date_default_timezone_set", args: []lit{lstr("GMT")}},
		{fn: "date", args: []lit{lstr("T"), lint(ts)}},
	}
	lines := phpOracle(t, buildPHP(seq))
	if len(lines) != len(seq) {
		t.Fatalf("php lines %d want %d", len(lines), len(seq))
	}
	z := zone.New(1 << 20)
	for i, c := range seq {
		fn, ok := lookupChain(c.fn)
		if !ok {
			t.Fatalf("missing %s", c.fn)
		}
		args := make([]types.Value, len(c.args))
		for j, a := range c.args {
			args[j] = a.goVal(t, z)
		}
		got, err := fn(z, nil, args)
		if err != nil {
			t.Fatalf("%s: %v", c.fn, err)
		}
		if want := strings.TrimPrefix(lines[i], "OK\t"); ser(z, got) != want {
			t.Errorf("#%d %s\n got %s\nwant %s", i, c.fn, ser(z, got), want)
		}
	}
}

func TestOpensslRandomPseudoBytes(t *testing.T) {
	z := zone.New(1 << 16)
	ref, err := types.NewReference(z, types.NewNull())
	if err != nil {
		t.Fatal(err)
	}
	fn, _ := Lookup("openssl_random_pseudo_bytes")
	a, err := fn(z, nil, []types.Value{types.NewLong(32), ref})
	if err != nil {
		t.Fatal(err)
	}
	b, err := fn(z, nil, []types.Value{types.NewLong(32)})
	if err != nil {
		t.Fatal(err)
	}
	if len(types.StringData(z, a)) != 32 || types.StringData(z, a) == types.StringData(z, b) {
		t.Fatalf("random bytes %x %x", types.StringData(z, a), types.StringData(z, b))
	}
	if ser(z, ref) != "T" {
		t.Fatalf("strong_result %s", ser(z, ref))
	}
}

func TestWPConstants(t *testing.T) {
	c := Constants()
	want := map[string]int64{
		"FORCE_GZIP": 31, "FORCE_DEFLATE": 15, "ZLIB_ENCODING_RAW": -15, "ZLIB_ENCODING_GZIP": 31, "ZLIB_ENCODING_DEFLATE": 15,
		"MB_CASE_UPPER": 0, "MB_CASE_LOWER": 1, "MB_CASE_TITLE": 2,
		"PREG_NO_ERROR": 0, "PREG_INTERNAL_ERROR": 1, "PREG_BACKTRACK_LIMIT_ERROR": 2, "PREG_RECURSION_LIMIT_ERROR": 3,
		"PREG_BAD_UTF8_ERROR": 4, "PREG_BAD_UTF8_OFFSET_ERROR": 5, "PREG_JIT_STACKLIMIT_ERROR": 6,
		"OPENSSL_RAW_DATA": 1, "OPENSSL_ZERO_PADDING": 2,
	}
	var script strings.Builder
	script.WriteString("<?php\n")
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		script.WriteString("echo " + name + ", \"\\n\";\n")
	}
	lines := phpOracle(t, script.String())
	for i, name := range names {
		v, ok := c[name]
		if !ok || v.ToLong() != want[name] {
			t.Errorf("%s = %v", name, v)
		}
		if lines[i] != strconv.FormatInt(want[name], 10) {
			t.Errorf("%s: php %s want %d", name, lines[i], want[name])
		}
	}
}

// Chaînes UTF-8 invalides : le découpage de cpIndex et celui de charLen coïncident, donc aucune
// fonction de position ou d'extraction n'indexe au-delà de la table des points de code.
func mbInvalidCases() []call {
	return []call{
		{fn: "mb_stripos", args: []lit{lstr("\x80a"), lstr("z")}},
		{fn: "mb_stripos", args: []lit{lstr("\x80a"), lstr("A")}},
		{fn: "mb_stripos", args: []lit{lstr("\xc0\xafa"), lstr("A")}},
		{fn: "mb_stripos", args: []lit{lstr("\xed\xa0\x80a"), lstr("A")}},
		{fn: "mb_substr", args: []lit{lstr("\x80\x81a"), lint(1), lint(1)}},
		{fn: "mb_substr", args: []lit{lstr("\x80\x81a"), lint(1)}},
		{fn: "mb_substr", args: []lit{lstr("\xed\xa0\x80a"), lint(1)}},
		{fn: "mb_substr", args: []lit{lstr("a\xf0\x9f\x98"), lint(1)}},
		{fn: "mb_substr", args: []lit{lstr("\xe2\x82a\xe2\x82\xac"), lint(1), lint(2)}},
		{fn: "mb_substr", args: []lit{lstr("\xff\xfea"), lint(-1)}},
		{fn: "mb_strpos", args: []lit{lstr("\xff\xfe"), lstr("a")}},
		{fn: "mb_strpos", args: []lit{lstr("\xe2\x82a"), lstr("a")}},
		{fn: "mb_strpos", args: []lit{lstr("\xf0\x9f\x98a"), lstr("a")}},
		{fn: "mb_strpos", args: []lit{lstr("\xff\xfea"), lstr("a")}},
		{fn: "mb_strlen", args: []lit{lstr("\x80\x81a")}},
		{fn: "mb_strlen", args: []lit{lstr("\xed\xa0\x80a")}},
	}
}

func opensslEncryptCases() []call {
	key16 := "0123456789abcdef"
	return []call{
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-256-cbc"), lstr("pass"), lint(0), lstr("1234567890123456")}},
		{fn: "openssl_encrypt", args: []lit{lstr(""), lstr("aes-256-cbc"), lstr("pass"), lint(0), lstr("1234567890123456")}},
		{fn: "openssl_encrypt", args: []lit{lstr("exactement16oct!"), lstr("AES-128-CBC"), lstr(key16), lint(1), lstr("1234567890123456")}},
		{fn: "openssl_encrypt", args: []lit{lstr("exactement16oct!"), lstr("aes-128-cbc"), lstr(key16), lint(3), lstr("1234567890123456")}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-128-cbc"), lstr(key16), lint(2), lstr("1234567890123456")}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-256-cbc"), lstr("pass"), lint(0), lstr("short")}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-256-cbc"), lstr("pass"), lint(0), lstr("")}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-256-cbc"), lstr("pass"), lint(4), lstr("1234567890123456")}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello world, ctr"), lstr("aes-192-ctr"), lstr("k"), lint(1), lstr("1234567890123456")}},
		{fn: "openssl_encrypt", args: []lit{lstr("bloc ecb de 21 octets"), lstr("aes-128-ecb"), lstr("k"), lint(1)}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-128-gcm"), lstr("k"), lint(1), lstr("123456789012"), lnull(), lstr("aad")}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-128-gcm"), lstr("k"), lint(0), lstr("12345678"), lnull()}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-256-gcm"), lstr("k"), lint(1), lstr("123456789012"), lnull(), lstr(""), lint(8)}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-128-gcm"), lstr("k"), lint(1), lstr("123456789012"), lnull(), lstr(""), lint(3)}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-128-gcm"), lstr("k"), lint(1), lstr("123456789012"), lnull(), lstr(""), lint(17)}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-128-gcm"), lstr("k"), lint(1), lstr(""), lnull()}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-128-gcm"), lstr("k"), lint(1), lstr("123456789012")}},
		{fn: "openssl_encrypt", args: []lit{lstr("hello"), lstr("aes-256-cbc"), lstr("pass"), lint(0), lstr("1234567890123456"), lnull()}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("x"), lstr("nope"), lstr("k")}},
	}
}

func TestPHPDifferentialLot7Reprise(t *testing.T) {
	runDifferential(t, []family{
		{"mbstring_invalid_utf8", mbInvalidCases()},
		{"openssl_encrypt", opensslEncryptCases()},
	}, 16)
}

// Les noyaux utf8_scan découpent comme mbSegment, la définition de référence de mb_strlen : exhaustif sur
// les chaînes de 1 à 3 octets tirés d'un alphabet couvrant chaque classe d'octet UTF-8, puis aléatoire sur
// des chaînes plus longues que le bloc vectoriel de 32 octets.
func TestUTF8ScanMatchesMBSegment(t *testing.T) {
	alphabet := []byte{0x00, 'a', 0x7f, 0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0xc0, 0xc1, 0xc2, 0xdf, 0xe0, 0xe1, 0xec, 0xed, 0xee, 0xef, 0xf0, 0xf1, 0xf3, 0xf4, 0xf5, 0xfe, 0xff}
	check := func(s string) {
		var bounds []int
		for i := 0; i < len(s); {
			bounds = append(bounds, i)
			n, _ := mbSegment(s, i)
			i += n
		}
		bounds = append(bounds, len(s))
		if got, want := len(bounds)-1, charLen(s, encUTF8); got != want {
			t.Fatalf("%x : %d segments, charLen=%d", s, got, want)
		}
		for k, off := range bounds {
			if got := cpOffset(s, k); got != off {
				t.Fatalf("%x : cpOffset(%d)=%d, attendu %d", s, k, got, off)
			}
			if got := C2_utf8_byte_to_char(strBytes(s), uint64(off)); got != uint64(k) {
				t.Fatalf("%x : byte_to_char(%d)=%d, attendu %d", s, off, got, k)
			}
		}
		if got := cpOffset(s, len(bounds)+3); got != len(s) {
			t.Fatalf("%x : cpOffset au-delà = %d", s, got)
		}
	}
	var buf []byte
	var rec func(depth int)
	rec = func(depth int) {
		if depth > 0 {
			check(string(buf))
		}
		if depth == 3 {
			return
		}
		for _, b := range alphabet {
			buf = append(buf, b)
			rec(depth + 1)
			buf = buf[:len(buf)-1]
		}
	}
	rec(0)
	seed := uint64(0x9e3779b97f4a7c15)
	for range 20000 {
		n := int(seed % 96)
		b := make([]byte, n)
		for i := range b {
			seed ^= seed << 13
			seed ^= seed >> 7
			seed ^= seed << 17
			if seed%3 == 0 {
				b[i] = byte('a' + seed%26)
			} else {
				b[i] = alphabet[seed%uint64(len(alphabet))]
			}
		}
		check(string(b))
	}
}

// Les noyaux transpilés par sgoiter rendent, ligne à ligne, les résultats du même C compilé par gcc -O2.
func TestSgoiterUTF8ScanParityVsGCC(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "utf8_scan_oracle")
	cmd := exec.Command("gcc", "-O2", "-Wall", "-Wextra", "-Werror", "-mavx2",
		"c_src/utf8_scan_oracle.c", "c_src/utf8_scan.c", "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if !bytes.HasSuffix(out, []byte("utf8_scan_oracle: OK\n")) {
		t.Fatal("oracle sans OK final")
	}
	num := func(f string) uint64 {
		n, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	var cur []byte
	counts := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		switch f[0] {
		case "utf8_scan_oracle:":
		case "str":
			cur = decHex(t, f[1])
			counts["str"]++
		case "c2b":
			if got, want := C2_utf8_char_to_byte(cur, uint64(len(cur)), num(f[1])), num(f[2]); got != want {
				t.Fatalf("c2b %x %s : %d, gcc %d", cur, f[1], got, want)
			}
			counts[f[0]]++
		case "b2c":
			if got, want := C2_utf8_byte_to_char(cur, num(f[1])), num(f[2]); got != want {
				t.Fatalf("b2c %x %s : %d, gcc %d", cur, f[1], got, want)
			}
			counts[f[0]]++
		case "cut":
			if got, want := C2_utf8_cut_floor(cur, num(f[1]), num(f[2])), num(f[3]); got != want {
				t.Fatalf("cut %x %s %s : %d, gcc %d", cur, f[1], f[2], got, want)
			}
			counts[f[0]]++
		default:
			t.Fatalf("ligne inconnue %q", line)
		}
	}
	for _, op := range []string{"str", "c2b", "b2c", "cut"} {
		if counts[op] < 400 {
			t.Fatalf("%s : %d cas", op, counts[op])
		}
	}
	// Le Go versionné est la sortie exacte de sgoiter sur la source C versionnée ; un transpileur absent
	// est un échec, non un saut, pour qu'aucun Go retouché à la main ne passe la porte.
	t.Run("regen", func(t *testing.T) {
		const sgoiter = "/devhoros/c2simd/bin/sgoiter"
		src, err := filepath.Abs("c_src/utf8_scan.c")
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		cmd := exec.CommandContext(t.Context(), sgoiter, "-in", src, "-out", filepath.Join(dir, "utf8_scan_gen.go"), "-pkg", "ext", "-internal-linkage", "on")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sgoiter : %v\n%s", err, out)
		}
		for _, name := range []string{"utf8_scan_gen.go", "utf8_scan_nosimd_gen.go"} {
			want, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s diffère de la sortie de sgoiter sur c_src/utf8_scan.c", name)
			}
		}
	})
}

func decHex(t *testing.T, s string) []byte {
	t.Helper()
	if s == "-" {
		return []byte{}
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Le découpage UTF-8 de mb_substr, mb_strpos et mb_strcut n'alloue plus de table de décalages.
func TestUTF8ScanNoAlloc(t *testing.T) {
	s := strings.Repeat("aé你🙂", 64)
	ln := 40
	allocs := testing.AllocsPerRun(100, func() {
		_ = cpOffset(s, 150)
		_ = sliceChars(s, 17, &ln, encUTF8)
		_ = strcut(s, 33, 77, encUTF8)
		_, _ = cpPos(s, "你🙂a", 3, false, charLen(s, encUTF8), false)
		_, _ = cpPos(s, "你🙂a", 3, false, charLen(s, encUTF8), true)
	})
	if allocs != 0 {
		t.Fatalf("allocations par appel : %v", allocs)
	}
}

// Les fonctions de position et d'extraction ne paniquent sur aucune combinaison de chaînes invalides.
func TestMBInvalidUTF8NoPanic(t *testing.T) {
	inputs := []string{"\x80a", "\x80\x81a", "\xff\xfe", "\xc0\xafa", "\xed\xa0\x80a", "a\xf0\x9f\x98", "\xe2\x82", "a\xbfa", "\xbf\xbf\xbf\xbf"}
	needles := []string{"a", "z", "A", "\x80", "\xbf", "\xe2\x82", ""}
	z := zone.New(1 << 20)
	for _, fn := range []string{"mb_strpos", "mb_stripos", "mb_strrpos", "mb_strripos", "mb_strstr", "mb_stristr", "mb_strrchr", "mb_substr_count"} {
		f, ok := Lookup(fn)
		if !ok {
			t.Fatalf("Lookup(%s)", fn)
		}
		for _, h := range inputs {
			for _, n := range needles {
				hv, _ := newStr(z, h)
				nv, _ := newStr(z, n)
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("%s(%x, %x) : panique %v", fn, h, n, r)
						}
					}()
					_, _ = f(z, nil, []types.Value{hv, nv})
				}()
			}
		}
	}
	sub, _ := Lookup("mb_substr")
	for _, h := range inputs {
		for start := int64(-5); start <= 5; start++ {
			for _, ln := range []int64{-3, -1, 0, 1, 2, 9} {
				hv, _ := newStr(z, h)
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("mb_substr(%x, %d, %d) : panique %v", h, start, ln, r)
						}
					}()
					_, _ = sub(z, nil, []types.Value{hv, types.NewLong(start), types.NewLong(ln)})
				}()
			}
		}
	}
}

// openssl_encrypt puis openssl_decrypt rendent le clair pour chaque chiffre, taille de clé, longueur et option.
func TestOpensslEncryptDecryptRoundTrip(t *testing.T) {
	enc, ok := Lookup("openssl_encrypt")
	if !ok {
		t.Fatal("openssl_encrypt absent de ext")
	}
	dec, _ := Lookup("openssl_decrypt")
	z := zone.New(1 << 22)
	s := func(v string) types.Value {
		x, err := newStr(z, v)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	for _, size := range []string{"128", "192", "256"} {
		for _, mode := range []string{"cbc", "ctr", "ecb", "gcm"} {
			algo := "aes-" + size + "-" + mode
			iv := "1234567890123456"
			if mode == "gcm" {
				iv = "123456789012"
			}
			for _, opts := range []int64{0, 1} {
				for n := range 34 {
					plain := strings.Repeat("p", n)
					tag, err := types.NewReference(z, types.NewNull())
					if err != nil {
						t.Fatal(err)
					}
					ct, err := enc(z, nil, []types.Value{s(plain), s(algo), s("passphrase"), types.NewLong(opts), s(iv), tag, s("aad")})
					if err != nil || ct.Type != types.TypeString {
						t.Fatalf("%s opts=%d n=%d : openssl_encrypt = %v, %v", algo, opts, n, ct, err)
					}
					tv := types.Deref(z, tag)
					if mode == "gcm" && (tv.Type != types.TypeString || types.Strlen(tv) != 16) {
						t.Fatalf("%s : étiquette %v", algo, tv)
					}
					args := []types.Value{ct, s(algo), s("passphrase"), types.NewLong(opts), s(iv)}
					if mode == "gcm" {
						args = append(args, tv, s("aad"))
					}
					pt, err := dec(z, nil, args)
					if err != nil || pt.Type != types.TypeString || types.StringData(z, pt) != plain {
						t.Fatalf("%s opts=%d n=%d : aller-retour %v, %v", algo, opts, n, pt, err)
					}
				}
			}
		}
	}
}

// La configuration mb_* et iconv_* est propre à la Zone et revient aux valeurs par défaut à z.Reset.
func TestExtSettingsPerZone(t *testing.T) {
	z1, z2 := zone.New(1<<16), zone.New(1<<16)
	reset := func(z *zone.Zone) { z.Reset(func(item zone.CleanupItem) { DispatchCleanup(z, item) }) }
	defer reset(z2)
	call := func(z *zone.Zone, fn string, args ...string) types.Value {
		t.Helper()
		f, ok := Lookup(fn)
		if !ok {
			t.Fatalf("Lookup(%s)", fn)
		}
		vals := make([]types.Value, len(args))
		for i, a := range args {
			vals[i], _ = newStr(z, a)
		}
		v, err := f(z, nil, vals)
		if err != nil {
			t.Fatalf("%s%q : %v", fn, args, err)
		}
		return v
	}
	str := func(z *zone.Zone, v types.Value) string {
		if v.Type == types.TypeArray {
			var parts []string
			types.ArrayForEach(z, v, func(k, item types.Value) bool {
				if k.Type == types.TypeString {
					parts = append(parts, types.StringData(z, k)+"="+types.StringData(z, item))
				} else {
					parts = append(parts, types.StringData(z, item))
				}
				return true
			})
			return strings.Join(parts, ",")
		}
		return types.StringData(z, v)
	}
	call(z1, "mb_internal_encoding", "latin1")
	call(z1, "mb_regex_encoding", "ascii")
	call(z1, "mb_language", "en")
	call(z1, "mb_detect_order", "UTF-8, latin1")
	call(z1, "iconv_set_encoding", "internal_encoding", "latin1")
	call(z1, "iconv_set_encoding", "Output_Encoding", "ascii")
	want1 := map[string]string{
		"mb_internal_encoding": "ISO-8859-1",
		"mb_regex_encoding":    "ASCII",
		"mb_language":          "English",
		"mb_detect_order":      "UTF-8,ISO-8859-1",
		"iconv_get_encoding":   "input_encoding=UTF-8,output_encoding=ascii,internal_encoding=latin1",
	}
	defaults := map[string]string{
		"mb_internal_encoding": "UTF-8",
		"mb_regex_encoding":    "UTF-8",
		"mb_language":          "neutral",
		"mb_detect_order":      "ASCII,UTF-8",
		"iconv_get_encoding":   "input_encoding=UTF-8,output_encoding=UTF-8,internal_encoding=UTF-8",
	}
	for fn, want := range want1 {
		if got := str(z1, call(z1, fn)); got != want {
			t.Errorf("Zone 1 %s() = %q, attendu %q", fn, got, want)
		}
		if got := str(z2, call(z2, fn)); got != defaults[fn] {
			t.Errorf("Zone 2 %s() = %q, attendu %q (état partagé entre Zones)", fn, got, defaults[fn])
		}
	}
	reset(z1)
	if z1.Slot(zone.SlotExt) != nil {
		t.Fatal("état ext retenu après z.Reset")
	}
	for fn, want := range defaults {
		if got := str(z1, call(z1, fn)); got != want {
			t.Errorf("après z.Reset %s() = %q, attendu %q", fn, got, want)
		}
	}
	reset(z1)
}

// TestGCMShortTagRoundTrip vérifie la symétrie GCM pour toute longueur d'étiquette de 1 à 16 octets et tout
// nonce : le préfixe produit par openssl_encrypt se déchiffre, une étiquette altérée est refusée.
func TestGCMShortTagRoundTrip(t *testing.T) {
	enc, _ := Lookup("openssl_encrypt")
	dec, _ := Lookup("openssl_decrypt")
	z := zone.New(1 << 22)
	s := func(v string) types.Value {
		x, err := newStr(z, v)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	for _, iv := range []string{"123456789012", "12345678", "1234567890123456"} {
		for tagLen := int64(1); tagLen <= 16; tagLen++ {
			tag, _ := types.NewReference(z, types.NewNull())
			ct, err := enc(z, nil, []types.Value{s("bonjour"), s("aes-128-gcm"), s("k"), types.NewLong(0), s(iv), tag, s("aad"), types.NewLong(tagLen)})
			if err != nil || ct.Type != types.TypeString {
				t.Fatalf("iv %d tag %d : openssl_encrypt = %v, %v", len(iv), tagLen, ct, err)
			}
			tv := types.StringData(z, types.Deref(z, tag))
			if int64(len(tv)) != tagLen {
				t.Fatalf("iv %d : étiquette de %d octets, attendu %d", len(iv), len(tv), tagLen)
			}
			pt, err := dec(z, nil, []types.Value{ct, s("aes-128-gcm"), s("k"), types.NewLong(0), s(iv), s(tv), s("aad")})
			if err != nil || types.StringData(z, pt) != "bonjour" {
				t.Fatalf("iv %d tag %d : déchiffrement %v, %v", len(iv), tagLen, pt, err)
			}
			bad := []byte(tv)
			bad[0] ^= 1
			if pt, _ := dec(z, nil, []types.Value{ct, s("aes-128-gcm"), s("k"), types.NewLong(0), s(iv), s(string(bad)), s("aad")}); pt.Type != types.TypeFalse {
				t.Fatalf("iv %d tag %d : étiquette altérée acceptée", len(iv), tagLen)
			}
		}
	}
}

// TestMBSettingsApplied vérifie que mb_internal_encoding et mb_detect_order, conservés par Zone, gouvernent
// les opérations appelées sans encodage explicite (valeurs mesurées sur PHP 8.3.6 avec mbstring).
func TestMBSettingsApplied(t *testing.T) {
	z := zone.New(1 << 16)
	defer z.Reset(func(item zone.CleanupItem) { DispatchCleanup(z, item) })
	call := func(name string, args ...types.Value) types.Value {
		t.Helper()
		fn, ok := Lookup(name)
		if !ok {
			t.Fatalf("%s absent", name)
		}
		v, err := fn(z, nil, args)
		if err != nil {
			t.Fatalf("%s : %v", name, err)
		}
		return v
	}
	s := func(v string) types.Value {
		x, err := newStr(z, v)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	if n := call("mb_strlen", s("é")).ToLong(); n != 1 {
		t.Fatalf("mb_strlen UTF-8 = %d", n)
	}
	call("mb_internal_encoding", s("ISO-8859-1"))
	if n := call("mb_strlen", s("é")).ToLong(); n != 2 {
		t.Fatalf("mb_strlen après mb_internal_encoding(ISO-8859-1) = %d, attendu 2", n)
	}
	if got := types.StringData(z, call("mb_substr", s("éa"), types.NewLong(0), types.NewLong(1))); got != "\xc3" {
		t.Fatalf("mb_substr en ISO-8859-1 = %q", got)
	}
	call("mb_detect_order", s("UTF-8"))
	if got := types.StringData(z, call("mb_detect_encoding", s("abc"))); got != "UTF-8" {
		t.Fatalf("mb_detect_encoding avec l'ordre UTF-8 = %q", got)
	}
}

func mbLot84Cases() []call {
	s := "aé你🙂b"
	return []call{
		{fn: "mb_strcut", args: []lit{lstr(s), lint(0)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(2)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(2), lint(1)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(3), lint(4)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(-2)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(-3), lint(2)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(0), lint(-1)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(5), lint(-2)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(20)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(-20), lint(3)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(1), lint(-100)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(4), lint(100)}},
		{fn: "mb_strcut", args: []lit{lstr(s), lint(1), lnull()}},
		{fn: "mb_strcut", args: []lit{lstr("\x80\x81abc\xe2\x82"), lint(1), lint(3)}},
		{fn: "mb_strcut", args: []lit{lstr("\xe2\x82\xac\xe2\x82"), lint(4), lint(2)}},
		{fn: "mb_strcut", args: []lit{lstr("\xc1\x80abcdef"), lint(1), lint(2)}},
		{fn: "mb_strcut", args: []lit{lstr("\xf8abcdef"), lint(0), lint(2)}},
		{fn: "mb_strcut", args: []lit{lstr("\xf4\x8f\xbf\xbfz\xf5\x80"), lint(2), lint(4)}},
		{fn: "mb_strcut", args: []lit{lstr(strings.Repeat("abcdefgh", 9) + "你好" + strings.Repeat("x", 40)), lint(73), lint(41)}},
		{fn: "mb_strcut", args: []lit{lstr("abc"), lint(1), lint(2), lstr("ASCII")}},
		{fn: "mb_strcut", args: []lit{lstr("aé"), lint(1), lint(1), lstr("ISO-8859-1")}},
		{fn: "mb_strcut", args: []lit{lstr("a"), lint(0), lint(1), lstr("nope")}},
		{fn: "mb_strcut", args: []lit{lstr(""), lint(0)}},
		{fn: "mb_strpos", args: []lit{lstr("aé你🙂b你🙂c"), lstr("你🙂c")}},
		{fn: "mb_strpos", args: []lit{lstr("é你🙂aé你🙂b"), lstr("é你🙂b"), lint(1)}},
		{fn: "mb_strrpos", args: []lit{lstr("aé你🙂b你🙂c"), lstr("你🙂")}},
		{fn: "mb_strrpos", args: []lit{lstr("aé你🙂b你🙂cé你"), lstr("é你"), lint(-3)}},
		{fn: "mb_stripos", args: []lit{lstr("AÉ你🙂B你🙂C"), lstr("🙂c")}},
		{fn: "mb_strripos", args: []lit{lstr("ÉTÉ🙂été🙂x"), lstr("été🙂")}},
		{fn: "mb_substr", args: []lit{lstr(strings.Repeat("aé你🙂", 20)), lint(37), lint(23)}},
		{fn: "mb_strimwidth", args: []lit{lstr("aé你🙂b你🙂cdef"), lint(2), lint(7), lstr("…")}},
		{fn: "mb_strrichr", args: []lit{lstr("ABCabcABC"), lstr("b")}},
		{fn: "mb_strrichr", args: []lit{lstr("ABCabcABC"), lstr("B"), lbool(true)}},
		{fn: "mb_strrichr", args: []lit{lstr("ÉtéÉTÉ"), lstr("é")}},
		{fn: "mb_strrichr", args: []lit{lstr("ÉtéÉTÉ"), lstr("é"), lbool(true)}},
		{fn: "mb_strrichr", args: []lit{lstr("abc"), lstr("z")}},
		{fn: "mb_strrichr", args: []lit{lstr("abc"), lstr("")}},
		{fn: "mb_strrichr", args: []lit{lstr(""), lstr("")}},
		{fn: "mb_strrichr", args: []lit{lstr("xAbYaB"), lstr("ab"), lbool(false), lstr("ASCII")}},
		{fn: "mb_strrichr", args: []lit{lstr("x"), lstr("x"), lbool(false), lstr("nope")}},
	}
}

func dateParseLot84Cases() []call {
	pairs := [][2]string{
		{"Y-m-d", "2024-02-29"}, {"Y-m-d H:i:s", "2024-02-29 13:45:07"}, {"d/m/Y H:i", "15/08/2023 9:05"},
		{"Y-m-d H:i:s.u", "2024-01-02 03:04:05.123456"}, {`Y-m-d\TH:i:sP`, "2024-01-02T03:04:05+02:00"},
		{"U", "1700000000"}, {"U", "-1700000000"}, {"U", "abc"}, {"U", "+12"}, {"U", "99999999999999999999999"},
		{"Y-m-d", "2024-13-45"}, {"Y-m-d", "2024-02"}, {"Y-m-d", "2024-02-03 extra"}, {"H:i", "25:61"},
		{"j.n.y", "5.6.07"}, {"Y-m-d e", "2024-01-02 Europe/Paris"}, {"Y-m-d O", "2024-01-02 -0530"},
		{"G:i A", "9:05 PM"}, {"D, d M Y", "Fri, 15 Nov 2024"}, {"!d", "15"}, {"Y-m-d|", "2024-01-02"},
		{"Y-m-d T", "2024-01-02 EST"}, {"v", "123"}, {"", ""}, {"", "x"}, {"Y", "abc"}, {"T", "UTC"}, {"T", "utc"},
		{"T", "EDT"}, {"e", "europe/paris"}, {"e", "Foo/Bar"}, {"O", "GMT+02:00"}, {"T", "(CET)"},
		{"O", "+02:00:30"}, {"O", "+020030"}, {"O", "+1:5"}, {"O", "+12:5"}, {"O", "-130"}, {"O", "+123456789"},
		{"T", "IST"}, {"T", "A"}, {"P", "Z"}, {"Y", "24"}, {"Y", "12345"}, {"y", "123"}, {"y", "75"},
		{"H:i:s", "1:2:3"}, {"g A", "12 AM"}, {"g A", "12 PM"}, {"g a", "12 a.m."}, {"h A", "13 PM"}, {"A", "PM"},
		{"d M Y", "1 xii 2020"}, {"D d", "sat 1"}, {"D d", "day 1"}, {"D", "msec"}, {"D", "xyz"}, {"D", "Mon,"},
		{"z Y", "59 2024"}, {"Y z", "2024 59"}, {"Y z", "2023 400"}, {"Y-m-d+", "2024-01-02 junk"}, {`\`, "x"},
		{"d#m", "1;2"}, {"d?m", "1x2"}, {"d*m", "1abc2"}, {`d\Tm`, "1T2"}, {"Y-m-d H:i:s", "2024-01-02\t03:04:05"},
		{"Y m", "2024\xc2\xa001"}, {"A", "xx"}, {"A", "12"}, {"G A", "1 xx"}, {"a", ""}, {"A", "P"}, {"u", "1234567"}, {"Y-m-d jS", "2024-01-02 3rd"}, {"dS", "1nd"}, {"S", "  "},
		{"v", "x12"}, {"G i", "1 5"}, {"Y-m-d e", "2024-01-02 UTC"}, {"d!", "15"}, {"X", "-0044"}, {"x", "+12345"},
		{"X", "abc"}, {"e", "Europe/Paris,"}, {"T", "EST.x"}, {"e", "zone.tab"}, {"e", "right/Europe/Paris"},
		{"H", "24"}, {"G", "007"}, {"M", "Sept"}, {"F", "IX"}, {"Y\x00m", "2024"}, {"Y", "20\x0024"},
	}
	out := make([]call, len(pairs))
	for i, p := range pairs {
		out[i] = call{fn: "date_parse_from_format", args: []lit{lstr(p[0]), lstr(p[1])}}
	}
	return out
}

func TestPHPDifferentialLot84(t *testing.T) {
	runDifferential(t, []family{
		{"mbstring_lot84", mbLot84Cases()},
		{"date_parse_from_format", dateParseLot84Cases()},
	}, 100)
}

// phpLines exécute un script complet sous l'oracle PHP 8.3 et rend ses lignes de sortie.
func phpLines(t *testing.T, body string) []string {
	t.Helper()
	return phpOracle(t, phpPrelude()+body)
}

// Les fonctions de fuseau sont comparées à PHP 8.3 sur des objets DateTimeZone construits des deux côtés.
func TestTimezoneLot84Differential(t *testing.T) {
	type tzCase struct {
		fn    string
		name  string
		begin *int64
		end   *int64
	}
	i64 := func(v int64) *int64 { return &v }
	names := []string{"Europe/Paris", "UTC", "GMT", "America/New_York", "Asia/Kolkata", "Australia/Lord_Howe",
		"Antarctica/Troll", "Etc/GMT+5", "America/Argentina/Buenos_Aires", "Europe/Busingen", "+02:00", "Pacific/Chatham"}
	var cases []tzCase
	for _, n := range names {
		cases = append(cases,
			tzCase{fn: "timezone_location_get", name: n},
			tzCase{fn: "timezone_transitions_get", name: n},
			tzCase{fn: "timezone_transitions_get", name: n, begin: i64(1700000000)},
			tzCase{fn: "timezone_transitions_get", name: n, begin: i64(1700000000), end: i64(1750000000)},
			tzCase{fn: "timezone_transitions_get", name: n, begin: i64(-5000000000), end: i64(-1000000000)},
			tzCase{fn: "timezone_transitions_get", name: n, begin: i64(2100000000), end: i64(2300000000)},
			tzCase{fn: "timezone_transitions_get", name: n, begin: i64(1750000000), end: i64(1700000000)},
			tzCase{fn: "timezone_transitions_get", name: n, begin: i64(1700000000), end: i64(4102444800)},
			tzCase{fn: "timezone_transitions_get", name: n, begin: i64(math.MinInt64), end: i64(2300000000)},
		)
	}
	var b strings.Builder
	for _, c := range cases {
		b.WriteString("$tz = new DateTimeZone('" + c.name + "');\n")
		b.WriteString("echo ser(" + c.fn + "($tz")
		if c.begin != nil {
			b.WriteString("," + strconv.FormatInt(*c.begin, 10))
		}
		if c.end != nil {
			b.WriteString("," + strconv.FormatInt(*c.end, 10))
		}
		b.WriteString(")), \"\\n\";\n")
	}
	lines := phpLines(t, b.String())
	if len(lines) != len(cases) {
		t.Fatalf("php : %d lignes pour %d cas", len(lines), len(cases))
	}
	z := zone.New(1 << 22)
	for i, c := range cases {
		obj, _, err := datetime.NewDateTimeZone(z, c.name)
		if err != nil {
			t.Fatalf("NewDateTimeZone(%s) : %v", c.name, err)
		}
		args := []types.Value{obj}
		if c.begin != nil {
			args = append(args, types.NewLong(*c.begin))
		}
		if c.end != nil {
			args = append(args, types.NewLong(*c.end))
		}
		fn, _ := Lookup(c.fn)
		got, err := fn(z, nil, args)
		if err != nil {
			t.Fatalf("%s(%s) : %v", c.fn, c.name, err)
		}
		if g := ser(z, got); g != lines[i] {
			k := 0
			for k < len(g) && k < len(lines[i]) && g[k] == lines[i][k] {
				k++
			}
			k = max(k-200, 0)
			t.Errorf("%s(%s, %v, %v) diverge à l'octet %d\n got …%s\nwant …%s", c.fn, c.name, c.begin, c.end, k, truncate(g[k:], 500), truncate(lines[i][k:], 500))
		}
	}
	// Extension du brief : une chaîne d'identifiant vaut l'objet DateTimeZone correspondant.
	fn, _ := Lookup("timezone_location_get")
	sv, _ := newStr(z, "Europe/Paris")
	got, err := fn(z, nil, []types.Value{sv})
	if err != nil || ser(z, got) != lines[0] {
		t.Fatalf("timezone_location_get(chaîne) = %s, %v", ser(z, got), err)
	}
}

// preg_last_error et preg_last_error_msg suivent, appel après appel dans une même Zone, la séquence de PHP 8.3.
func TestPregLastErrorLot84Differential(t *testing.T) {
	cases := []call{
		{fn: "preg_match", args: []lit{lstr("/a/"), lstr("xa")}},
		{fn: "preg_match", args: []lit{lstr("/(/"), lstr("x")}},
		{fn: "preg_match", args: []lit{lstr("/a/u"), lstr("\xff")}},
		{fn: "preg_quote", args: []lit{lstr("a.b")}},
		{fn: "preg_match", args: []lit{lstr("/a/"), lstr("x")}},
		{fn: "preg_match", args: []lit{lstr("/a/u"), lstr("éa"), lnull(), lint(0), lint(1)}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr("/a/"), lstr("x"), lnull(), lint(0), lint(5)}, refs: []int{2}},
		{fn: "preg_match", args: []lit{lstr(`/(?:\D+|<\d+>)*[!?]/U`), lstr(strings.Repeat("foobar ", 6))}},
		{fn: "preg_match", args: []lit{lstr(`/(?:a|b)*+c/`), lstr(strings.Repeat("ab", 10))}},
		{fn: "preg_match", args: []lit{lstr("/b/"), lstr("abc")}},
		{fn: "preg_split", args: []lit{lstr("/a/u"), lstr("\xff")}},
		{fn: "preg_split", args: []lit{lstr("/,/"), lstr("a,b")}},
		{fn: "preg_replace", args: []lit{lstr("/a/u"), lstr("b"), lstr("\xff")}},
		{fn: "preg_replace", args: []lit{lstr("/a/"), lstr("b"), lstr("aa")}},
		{fn: "preg_grep", args: []lit{lstr("/a/u"), larr(ap(lstr("\xff")), ap(lstr("a")))}},
		{fn: "preg_grep", args: []lit{lstr("/a/u"), larr(ap(lstr("a")), ap(lstr("\xff")), ap(lstr("ba")))}},
		{fn: "preg_match_all", args: []lit{lstr("/a/u"), lstr("\xff")}},
		{fn: "preg_match_all", args: []lit{lstr("/(a)|b/"), lstr("ab")}},
		{fn: "preg_match", args: []lit{lstr("abc"), lstr("abc")}},
		{fn: "preg_match", args: []lit{lstr("/é/u"), lstr("é")}},
	}
	var b strings.Builder
	for _, c := range cases {
		for _, r := range c.refs {
			b.WriteString("$r" + strconv.Itoa(r) + "=null;\n")
		}
		b.WriteString("$ret=@" + c.fn + "(")
		for j, a := range c.args {
			if j > 0 {
				b.WriteByte(',')
			}
			if isRef(c.refs, j) {
				b.WriteString("$r" + strconv.Itoa(j))
			} else {
				b.WriteString(a.php())
			}
		}
		b.WriteString(");\necho ser($ret), \"\\t\", ser(preg_last_error()), \"\\t\", ser(preg_last_error_msg()), \"\\n\";\n")
	}
	lines := phpLines(t, b.String())
	if len(lines) != len(cases) {
		t.Fatalf("php : %d lignes pour %d cas", len(lines), len(cases))
	}
	z := zone.New(1 << 22)
	lastErr, _ := Lookup("preg_last_error")
	lastMsg, _ := Lookup("preg_last_error_msg")
	for i, c := range cases {
		args := make([]types.Value, len(c.args))
		for j, a := range c.args {
			if isRef(c.refs, j) {
				args[j], _ = types.NewReference(z, types.NewNull())
			} else {
				args[j] = a.goVal(t, z)
			}
		}
		fn, _ := Lookup(c.fn)
		ret, err := fn(z, nil, args)
		if err != nil {
			t.Fatalf("%s #%d : %v", c.fn, i, err)
		}
		code, _ := lastErr(z, nil, nil)
		msg, _ := lastMsg(z, nil, nil)
		if g := ser(z, ret) + "\t" + ser(z, code) + "\t" + ser(z, msg); g != lines[i] {
			t.Errorf("%s #%d\n got %s\nwant %s", c.fn, i, truncate(g, 300), truncate(lines[i], 300))
		}
	}
}

// openssl_error_string dépile, dans l'ordre, les messages que PHP 8.3 relaie après chaque échec.
func TestOpensslErrorStringLot84Differential(t *testing.T) {
	iv16, iv12 := lstr("1234567890123456"), lstr("123456789012")
	zero := lstr("AAAAAAAAAAAAAAAAAAAAAA==")
	cases := []call{
		{fn: "openssl_encrypt", args: []lit{lstr("x"), lstr("nope-cipher"), lstr("k")}},
		{fn: "openssl_decrypt", args: []lit{lstr("x"), lstr("nope-cipher"), lstr("k")}},
		{fn: "openssl_decrypt", args: []lit{zero, lstr("aes-128-cbc"), lstr("k"), lint(0), iv16}},
		{fn: "openssl_decrypt", args: []lit{lstr("!!!"), lstr("aes-128-cbc"), lstr("k"), lint(0), iv16}},
		{fn: "openssl_decrypt", args: []lit{lstr(""), lstr("aes-128-cbc"), lstr("k"), lint(0), iv16}},
		{fn: "openssl_decrypt", args: []lit{lstr("AAAA"), lstr("aes-128-cbc"), lstr("k"), lint(1), iv16}},
		{fn: "openssl_decrypt", args: []lit{lstr("AAAA"), lstr("aes-128-gcm"), lstr("k"), lint(0), iv12, lstr("")}},
		{fn: "openssl_decrypt", args: []lit{zero, lstr("aes-128-gcm"), lstr("k"), lint(0), iv12, lstr("0123456789abcdef")}},
		{fn: "openssl_encrypt", args: []lit{lstr("abc"), lstr("aes-128-cbc"), lstr("k"), lint(2), iv16}},
		{fn: "openssl_encrypt", args: []lit{lstr("abc"), lstr("aes-128-cbc"), lstr("k"), lint(4), iv16}},
		{fn: "openssl_decrypt", args: []lit{zero, lstr("aes-128-cbc"), lstr("k"), lint(4), iv16}},
		{fn: "openssl_decrypt", args: []lit{zero, lstr("aes-128-ecb"), lstr("k"), lint(1)}},
		{fn: "openssl_encrypt", args: []lit{lstr("abc"), lstr("aes-128-gcm"), lstr("k"), lint(0), lstr(""), lnull()}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("abc"), lstr("aes-128-gcm"), lstr("k"), lint(0), iv12, lnull(), lstr(""), lint(17)}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("abc"), lstr("aes-128-gcm"), lstr("k"), lint(0), iv12, lnull(), lstr(""), lint(0)}, refs: []int{5}},
		{fn: "openssl_encrypt", args: []lit{lstr("abc"), lstr("aes-128-cbc"), lstr("k"), lint(0), iv16}},
	}
	var b strings.Builder
	b.WriteString("echo ser(openssl_error_string()), \"\\n\";\n")
	for _, c := range cases {
		for _, r := range c.refs {
			b.WriteString("$r" + strconv.Itoa(r) + "=null;\n")
		}
		b.WriteString("$ret=@" + c.fn + "(")
		for j, a := range c.args {
			if j > 0 {
				b.WriteByte(',')
			}
			if isRef(c.refs, j) {
				b.WriteString("$r" + strconv.Itoa(j))
			} else {
				b.WriteString(a.php())
			}
		}
		b.WriteString(");\necho ser($ret); while (($e = openssl_error_string()) !== false) echo \"\\t\", ser($e); echo \"\\t\", ser(openssl_error_string()), \"\\n\";\n")
	}
	lines := phpLines(t, b.String())
	if len(lines) != len(cases)+1 {
		t.Fatalf("php : %d lignes pour %d cas", len(lines), len(cases)+1)
	}
	z := zone.New(1 << 22)
	errStr, _ := Lookup("openssl_error_string")
	first, _ := errStr(z, nil, nil)
	if g := ser(z, first); g != lines[0] {
		t.Fatalf("file initiale : %s, attendu %s", g, lines[0])
	}
	for i, c := range cases {
		args := make([]types.Value, len(c.args))
		for j, a := range c.args {
			if isRef(c.refs, j) {
				args[j], _ = types.NewReference(z, types.NewNull())
			} else {
				args[j] = a.goVal(t, z)
			}
		}
		fn, _ := Lookup(c.fn)
		ret, err := fn(z, nil, args)
		if err != nil {
			t.Fatalf("%s #%d : %v", c.fn, i, err)
		}
		g := ser(z, ret)
		for {
			e, _ := errStr(z, nil, nil)
			g += "\t" + ser(z, e)
			if e.Type == types.TypeFalse {
				break
			}
		}
		if g != lines[i+1] {
			t.Errorf("%s #%d\n got %s\nwant %s", c.fn, i, g, lines[i+1])
		}
	}
}

// mb_convert_variables convertit en place les variables transmises par référence, comme PHP 8.3.
func TestMBConvertVariablesLot84Differential(t *testing.T) {
	type cvCase struct {
		to   string
		from lit
		vars []lit
	}
	cases := []cvCase{
		{"UTF-8", lstr("ISO-8859-1"), []lit{lstr("caf\xe9"), larr(kv(lstr("x"), lstr("\xe9t\xe9")), ap(lint(5)), ap(larr(kv(lstr("k\xe9"), lstr("\xe0")))))}},
		{"ISO-8859-1", lstr("ASCII,UTF-8"), []lit{lstr("café")}},
		{"UTF-8", lstr("ASCII,UTF-8"), []lit{lstr("abc")}},
		{"UTF-8", lstr("ASCII,UTF-8"), []lit{lstr("caf\xe9")}},
		{"UTF-8", larr(ap(lstr("ASCII")), ap(lstr("UTF-8")), ap(lstr("ISO-8859-1"))), []lit{lstr("caf\xe9")}},
		{"UTF-8", lstr("auto"), []lit{lstr("é")}},
		{"UTF-8", lstr("ASCII,UTF-8"), []lit{lint(12)}},
		{"UTF-8", lstr("ASCII,UTF-8"), []lit{lnull()}},
		{"ISO-8859-1", lstr("UTF-8"), []lit{lstr("é"), lstr("\xff")}},
		{"UTF-8", lstr("UTF-8,ISO-8859-1"), []lit{lstr("é"), lstr("\xe9")}},
		{"UTF-8", lstr("Windows-1252"), []lit{lstr("caf\xe9"), lstr("\x80")}},
		{"nope", lstr("UTF-8"), []lit{lstr("a")}},
		{"UTF-8", lstr("nope"), []lit{lstr("a")}},
		{"UTF-8", lstr(""), []lit{lstr("a")}},
	}
	var b strings.Builder
	for _, c := range cases {
		var names []string
		for j, v := range c.vars {
			name := "$v" + strconv.Itoa(j)
			b.WriteString(name + "=" + v.php() + ";\n")
			names = append(names, name)
		}
		b.WriteString("try { $ret = mb_convert_variables(" + lstr(c.to).php() + "," + c.from.php() + "," + strings.Join(names, ",") + ");\n")
		b.WriteString("echo \"OK\\t\", ser($ret)")
		for _, n := range names {
			b.WriteString(", \"\\t\", ser(" + n + ")")
		}
		b.WriteString("; } catch (Throwable $e) { echo \"EX\\t\", get_class($e), \"\\t\", bin2hex($e->getMessage()); }\necho \"\\n\";\n")
	}
	lines := phpLines(t, b.String())
	if len(lines) != len(cases) {
		t.Fatalf("php : %d lignes pour %d cas", len(lines), len(cases))
	}
	fn, _ := Lookup("mb_convert_variables")
	for i, c := range cases {
		z := zone.New(1 << 20)
		args := []types.Value{lstr(c.to).goVal(t, z), c.from.goVal(t, z)}
		for _, v := range c.vars {
			r, err := types.NewReference(z, v.goVal(t, z))
			if err != nil {
				t.Fatal(err)
			}
			args = append(args, r)
		}
		ret, err := fn(z, nil, args)
		var g string
		if err != nil {
			g = "EX\t" + exceptionClassAny(err) + "\t" + hex.EncodeToString([]byte(err.Error()))
		} else {
			g = "OK\t" + ser(z, ret)
			for _, r := range args[2:] {
				g += "\t" + ser(z, r)
			}
		}
		if g != lines[i] {
			t.Errorf("cas %d\n got %s\nwant %s", i, g, lines[i])
		}
	}
}

// L'état de preg_last_error et la file d'openssl_error_string appartiennent à la Zone et meurent avec elle.
func TestLot84ErrorStatePerZone(t *testing.T) {
	z1, z2 := zone.New(1<<16), zone.New(1<<16)
	reset := func(z *zone.Zone) { z.Reset(func(item zone.CleanupItem) { DispatchCleanup(z, item) }) }
	defer reset(z2)
	match, _ := Lookup("preg_match")
	lastErr, _ := Lookup("preg_last_error")
	dec, _ := Lookup("openssl_decrypt")
	errStr, _ := Lookup("openssl_error_string")
	str := func(z *zone.Zone, s string) types.Value {
		v, _ := newStr(z, s)
		return v
	}
	if _, err := match(z1, nil, []types.Value{str(z1, "/a/u"), str(z1, "\xff")}); err != nil {
		t.Fatal(err)
	}
	if _, err := dec(z1, nil, []types.Value{str(z1, ""), str(z1, "aes-128-cbc"), str(z1, "k")}); err != nil {
		t.Fatal(err)
	}
	if v, _ := lastErr(z2, nil, nil); v.ToLong() != 0 {
		t.Fatalf("Zone 2 preg_last_error = %d", v.ToLong())
	}
	if v, _ := errStr(z2, nil, nil); v.Type != types.TypeFalse {
		t.Fatalf("Zone 2 openssl_error_string = %s", ser(z2, v))
	}
	if v, _ := lastErr(z1, nil, nil); v.ToLong() != pregBadUTF8Error {
		t.Fatalf("Zone 1 preg_last_error = %d", v.ToLong())
	}
	reset(z1)
	if v, _ := lastErr(z1, nil, nil); v.ToLong() != 0 {
		t.Fatalf("après z.Reset preg_last_error = %d", v.ToLong())
	}
	if v, _ := errStr(z1, nil, nil); v.Type != types.TypeFalse {
		t.Fatalf("après z.Reset openssl_error_string = %s", ser(z1, v))
	}
	reset(z1)
}

// S9-M3 : $idna_info reçoit par référence result, isTransitionalDifferent et errors ; IDNA_CHECK_BIDI
// (RFC 5893) et IDNA_CHECK_CONTEXTJ (RFC 5892) rejettent, y compris sous un label xn-- déjà encodé. Les
// labels acceptés et refusés sont confrontés au paquet python3 idna 3.6 (encode uts46, non transitionnel),
// oracle indépendant faute d'extension intl dans /usr/bin/php.
func TestIdnToASCIIBidiContextJAndInfoRef(t *testing.T) {
	z := zone.New(1 << 16)
	defer z.Reset(nil)
	const nt = idnaNontransitionalToASCII
	cases := []struct {
		in        string
		flags     int
		want      string // forme ASCII attendue, vide pour un rejet
		transDiff bool
		err       uint32 // bit d'erreur attendu dans $idna_info['errors'] pour un rejet
	}{
		{in: "אa.com", flags: idnaCheckBidi, err: idnaErrBidi},
		{in: "1א.com", flags: idnaCheckBidi, err: idnaErrBidi},
		{in: "א1٠.com", flags: idnaCheckBidi, err: idnaErrBidi},
		{in: "xn--a-zhc.com", flags: idnaCheckBidi, err: idnaErrBidi},
		{in: "אבג.com", flags: idnaCheckBidi, want: "xn--4dbcd.com"},
		{in: "xn--4gbrim.com", flags: idnaCheckBidi, want: "xn--4gbrim.com"},
		{in: "a\u200cb.com", flags: nt | idnaCheckContextJ, err: idnaErrContextJ},
		{in: "a\u200db.com", flags: nt | idnaCheckContextJ, err: idnaErrContextJ},
		{in: "ا\u200cب.com", flags: nt | idnaCheckContextJ, err: idnaErrContextJ},
		{in: "ب\u200ca.com", flags: nt | idnaCheckContextJ, err: idnaErrContextJ},
		{in: "xn--ab-m1t.com", flags: idnaCheckContextJ, err: idnaErrContextJ},
		{in: "क\u094d\u200dष.com", flags: nt | idnaCheckContextJ, want: "xn--11b2ezcw70k.com", transDiff: true},
		{in: "क\u094d\u200cष.com", flags: nt | idnaCheckContextJ, want: "xn--11b2ezcs70k.com", transDiff: true},
		{in: "ب\u200cب.com", flags: nt | idnaCheckContextJ, want: "xn--ngba799q.com", transDiff: true},
		{in: "ب\u064e\u200c\u064eب.com", flags: nt | idnaCheckContextJ, want: "xn--ngba7ia3604a.com", transDiff: true},
		// Sans drapeau, les mêmes labels passent ; en mode transitionnel, la correspondance efface ZWNJ.
		{in: "אa.com", want: "xn--a-zhc.com"},
		{in: "a\u200cb.com", flags: nt, want: "xn--ab-j1t.com", transDiff: true},
		{in: "a\u200cb.com", flags: idnaCheckContextJ, want: "ab.com", transDiff: true},
		// Labels xn-- : Punycode invalide, décodage tout ASCII, forme non NFC, casse indifférente.
		{in: "xn--zzzzzzzzz.com", err: idnaErrPunycode},
		{in: "xn--abc-.com", err: idnaErrPunycode},
		{in: "xn--ex-8tb.com", err: idnaErrInvalidACE},
		{in: "XN--BCHER-KVA.example", want: "xn--bcher-kva.example"},
	}
	for _, c := range cases {
		ref, err := types.NewReference(z, types.NewNull())
		if err != nil {
			t.Fatal(err)
		}
		got := mustCall(t, z, "idn_to_ascii", c.in, c.flags, intlIDNAVariantUTS46, ref)
		info := types.Deref(z, ref)
		if c.want != "" {
			if ser(z, got) != ser(z, mustStr(t, z, c.want)) {
				t.Errorf("idn_to_ascii(%q, %d) = %s, attendu %q", c.in, c.flags, ser(z, got), c.want)
			}
			if w := ser(z, idnaInfoValue(t, z, c.want, c.transDiff, 0)); ser(z, info) != w {
				t.Errorf("idn_to_ascii(%q, %d) : $idna_info = %s, attendu %s", c.in, c.flags, ser(z, info), w)
			}
			continue
		}
		if got.Type != types.TypeFalse {
			t.Errorf("idn_to_ascii(%q, %d) = %s, false attendu", c.in, c.flags, ser(z, got))
		}
		key := mustStr(t, z, "errors")
		e, ok, err := types.ArrayGet(z, info, key, nil)
		if err != nil || !ok || uint32(e.ToLong())&c.err == 0 {
			t.Errorf("idn_to_ascii(%q, %d) : $idna_info = %s, bit d'erreur %#x attendu", c.in, c.flags, ser(z, info), c.err)
		}
	}
	consts := Constants()
	if b, j := consts["IDNA_ERROR_BIDI"], consts["IDNA_ERROR_CONTEXTJ"]; b.ToLong() != 2048 || j.ToLong() != 4096 {
		t.Errorf("IDNA_ERROR_BIDI = %s, IDNA_ERROR_CONTEXTJ = %s", ser(z, b), ser(z, j))
	}

	// S9R3-B1 : une entrée marquée FlagTainted propage son marquage aussi bien au retour direct qu'à
	// $idna_info['result'], y compris lorsque la conversion retourne false pour erreur IDNA.
	for _, dom := range []string{"example.com", "phpinfo();", "אa.com"} {
		taintedIn, err := newStr(z, dom)
		if err != nil {
			t.Fatal(err)
		}
		taintedIn.Flags |= types.FlagTainted
		ref, err := types.NewReference(z, types.NewNull())
		if err != nil {
			t.Fatal(err)
		}
		fn, _ := Lookup("idn_to_ascii")
		direct, err := fn(z, nil, []types.Value{taintedIn, types.NewLong(idnaCheckBidi), types.NewLong(intlIDNAVariantUTS46), ref})
		if err != nil {
			t.Fatal(err)
		}
		if direct.Type == types.TypeString && direct.Flags&types.FlagTainted == 0 {
			t.Errorf("idn_to_ascii(%q) : retour direct non marqué FlagTainted", dom)
		}
		info := types.Deref(z, ref)
		resVal, ok, err := types.ArrayGet(z, info, mustStr(t, z, "result"), nil)
		if err != nil || !ok || resVal.Type != types.TypeString || resVal.Flags&types.FlagTainted == 0 {
			t.Errorf("idn_to_ascii(%q) : $idna_info['result'] = %+v (ok=%v), FlagTainted attendu", dom, resVal, ok)
		}
	}
}

func mustStr(t *testing.T, z *zone.Zone, s string) types.Value {
	t.Helper()
	v, err := newStr(z, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Les Viramas cités par RFC 5892 pour ContextJ ont la classe combinatoire canonique 9 ; une lettre non.
func TestIsViramaFromCombiningClass(t *testing.T) {
	for _, r := range []rune{0x094D, 0x09CD, 0x0A4D, 0x0ACD, 0x0B4D, 0x0BCD, 0x0C4D, 0x0CCD, 0x0D4D, 0x0DCA,
		0x0E3A, 0x0F84, 0x1039, 0x1714, 0x1734, 0x17D2, 0x1A60, 0x1B44, 0x1BAA, 0x1BF2, 0x1BF3, 0xA806, 0xA8C4,
		0xA953, 0xA9C0, 0xAAF6, 0xABED, 0x10A3F, 0x11046, 0x110B9, 0x11133, 0x111C0, 0x11235, 0x1134D, 0x11442,
		0x114C2, 0x115BF, 0x1163F, 0x116B6, 0x1172B} {
		if !isVirama(r) {
			t.Errorf("isVirama(%U) = false", r)
		}
	}
	if isVirama('a') || isVirama(0x0915) {
		t.Error("isVirama vrai pour une lettre")
	}
}

// punycodeDecode inverse punycodeEncode et refuse les entrées invalides de RFC 3492.
func TestPunycodeDecode(t *testing.T) {
	for _, s := range []string{"bücher", "他们为什么不说中文", "ليهمابتكلموشعربي؟", "a\u200db", "abc", "ü"} {
		enc, ok := punycodeEncode([]rune(s))
		if !ok {
			t.Fatalf("punycodeEncode(%q)", s)
		}
		dec, ok := punycodeDecode(enc)
		if !ok || string(dec) != s {
			t.Errorf("punycodeDecode(%q) = %q, %v, attendu %q", enc, string(dec), ok, s)
		}
	}
	for _, s := range []string{"zzzzzzzzz", "-abc", "a-é", "a-!", "a-9999999999", "a-b"} {
		if dec, ok := punycodeDecode(s); ok {
			t.Errorf("punycodeDecode(%q) = %q, refus attendu", s, string(dec))
		}
	}
}

// La table Bidi et Joining_Type embarquée est la sortie exacte de son générateur.
func TestIdnaTableRegeneration(t *testing.T) {
	out := filepath.Join(t.TempDir(), "idna_table_gen.go")
	cmd := exec.CommandContext(t.Context(), "/usr/bin/python3", "-I", "testdata/gen_idna_table.py", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("générateur : %v\n%s", err, b)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("idna_table_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("idna_table_gen.go diffère de la sortie de testdata/gen_idna_table.py")
	}
}
