package ext

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
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
	}
}

func TestPHPDifferential(t *testing.T) {
	families := []struct {
		name  string
		cases []call
	}{
		{"hash", hashCases()},
		{"pcre", pcreCases()},
		{"mbstring", mbCases()},
		{"date", dateCases()},
	}
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
	if len(all) < 60 {
		t.Fatalf("cases %d < 60", len(all))
	}
	tz := "UTC"
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		tz = strings.TrimSpace(string(b))
	}
	script := buildPHP(all)
	path := filepath.Join(t.TempDir(), "oracle.php")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/usr/bin/php", "-n",
		"-d", "extension=/usr/lib/php/20230831/mbstring.so",
		"-d", "date.timezone="+tz,
		"-d", "display_errors=stderr",
		path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("php: %v\n%s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(all) {
		t.Fatalf("php lines %d want %d\n%s", len(lines), len(all), truncate(string(out), 2000))
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
		fn, ok := Lookup(c.fn)
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
			if gerr == nil || ExceptionClass(gerr) != fields[1] || gerr.Error() != string(msg) {
				shown++
				if shown <= 30 {
					gotC, gotM := "", ""
					if gerr != nil {
						gotC, gotM = ExceptionClass(gerr), gerr.Error()
					}
					t.Errorf("%s %s: exception got %s %q want %s %q", owner[i], c.fn, gotC, gotM, fields[1], msg)
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
				t.Errorf("%s %s: unexpected %s %q", owner[i], c.fn, ExceptionClass(gerr), gerr.Error())
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
					t.Errorf("%s %s field %d\n got %s\nwant %s", owner[i], c.fn, k, truncate(gotSer[k], 300), truncate(want[k], 300))
				}
			}
		}
	}
	if shown > 0 {
		t.Fatalf("%d mismatches", shown)
	}
	if re2Hits.Load() == 0 || pcre2Hits.Load() == 0 {
		t.Fatalf("paths re2=%d pcre2=%d", re2Hits.Load(), pcre2Hits.Load())
	}
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
