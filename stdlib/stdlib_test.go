package stdlib

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func TestLookupNoAlloc(t *testing.T) {
	if _, ok := Lookup("STRLEN"); !ok {
		t.Fatal("STRLEN")
	}
	if _, ok := Lookup("no_such"); ok {
		t.Fatal("unexpected hit")
	}
	for i := 1; i < len(natives); i++ {
		if natives[i-1].name >= natives[i].name {
			t.Fatalf("unsorted %s %s", natives[i-1].name, natives[i].name)
		}
	}
	allocs := testing.AllocsPerRun(200, func() {
		Lookup("StrLen")
		Lookup("JSON_ENCODE")
		Lookup("missing")
	})
	if allocs != 0 {
		t.Fatalf("allocs %v", allocs)
	}
}

func TestSgoiterParityVsGCC(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "php_strsimd_oracle")
	cmd := exec.Command("gcc", "-O2", "-Wall", "-Wextra", "-Werror", "-mavx2",
		"c_src/php_strsimd_oracle.c", "c_src/php_strsimd.c", "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("oracle: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("php_strsimd_oracle: OK")) {
		t.Fatal("oracle missing OK")
	}
	counts := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "php_strsimd_oracle:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			t.Fatalf("line %q", line)
		}
		counts[f[0]]++
		switch f[0] {
		case "memchr":
			src := decHex(t, f[1])
			target, err := strconv.ParseUint(f[2], 16, 8)
			if err != nil {
				t.Fatal(err)
			}
			want, err := strconv.ParseInt(f[3], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if got := C2_memchr(src, uint64(len(src)), byte(target)); got != want {
				t.Fatalf("memchr %q got %d want %d", line, got, want)
			}
		case "strpos":
			hay := decHex(t, f[1])
			needle := decHex(t, f[2])
			want, err := strconv.ParseInt(f[3], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if got := C2_strpos(hay, uint64(len(hay)), needle, uint64(len(needle))); got != want {
				t.Fatalf("strpos %q got %d want %d", line, got, want)
			}
		case "upper", "lower":
			src := decHex(t, f[1])
			want := decHex(t, f[2])
			dst := make([]byte, len(src))
			if f[0] == "upper" {
				C2_strtoupper(dst, src, uint64(len(src)))
			} else {
				C2_strtolower(dst, src, uint64(len(src)))
			}
			if !bytes.Equal(dst, want) {
				t.Fatalf("%s mismatch %q", f[0], line)
			}
		case "utf8v":
			src := decHex(t, f[1])
			want, err := strconv.ParseInt(f[2], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if got := C2_utf8_validate(src, uint64(len(src))); got != want {
				t.Fatalf("utf8v %q got %d want %d", line, got, want)
			}
		case "utf8c":
			src := decHex(t, f[1])
			want, err := strconv.ParseUint(f[2], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if got := C2_utf8_count(src, uint64(len(src))); got != want {
				t.Fatalf("utf8c %q got %d want %d", line, got, want)
			}
		default:
			t.Fatalf("unknown %q", line)
		}
	}
	for _, op := range []string{"memchr", "strpos", "upper", "lower", "utf8v", "utf8c"} {
		if counts[op] == 0 {
			t.Fatalf("no %s cases", op)
		}
	}
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

type litKind uint8

const (
	kNull litKind = iota
	kBool
	kInt
	kFloat
	kString
	kArray
)

type lit struct {
	k     litKind
	b     bool
	i     int64
	f     float64
	s     string
	elems []el
}

type el struct {
	auto bool
	key  lit
	val  lit
}

func Lnull() lit           { return lit{k: kNull} }
func Lbool(v bool) lit     { return lit{k: kBool, b: v} }
func Lint(v int64) lit     { return lit{k: kInt, i: v} }
func Lfloat(v float64) lit { return lit{k: kFloat, f: v} }
func Lstr(v string) lit    { return lit{k: kString, s: v} }
func Larr(elems ...el) lit { return lit{k: kArray, elems: elems} }
func ap(v lit) el          { return el{auto: true, val: v} }
func kv(k, v lit) el       { return el{key: k, val: v} }

type call struct {
	fn   string
	args []lit
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
		if v.i == math.MinInt64 {
			return "PHP_INT_MIN"
		}
		return strconv.FormatInt(v.i, 10)
	case kFloat:
		if math.IsNaN(v.f) {
			return "NAN"
		}
		if math.IsInf(v.f, 1) {
			return "INF"
		}
		if math.IsInf(v.f, -1) {
			return "-INF"
		}
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v.f))
		return "unpack('e', hex2bin('" + hex.EncodeToString(buf[:]) + "'))[1]"
	case kString:
		if v.s == "" {
			return "''"
		}
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
	case kFloat:
		return types.NewDouble(v.f)
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
				if err = appendKey(z, &a, val); err != nil {
					t.Fatal(err)
				}
			} else if err = setKey(z, &a, e.key.goVal(t, z), val); err != nil {
				t.Fatal(err)
			}
		}
		return a
	default:
		t.Fatal("lit")
		return types.Value{}
	}
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
		keys, vals := arrayItems(z, v)
		for i := range keys {
			if keys[i].IsLong() {
				b.WriteByte('I')
				b.WriteString(strconv.FormatInt(keys[i].ToLong(), 10))
			} else {
				b.WriteByte('S')
				b.WriteString(hex.EncodeToString([]byte(types.StringData(z, keys[i]))))
			}
			b.WriteByte('=')
			b.WriteString(ser(z, vals[i]))
			b.WriteByte(';')
		}
		return b.String()
	case types.TypeObject:
		var b strings.Builder
		b.WriteString("O")
		b.WriteString(safeClass(z, v))
		b.WriteByte('{')
		for _, p := range objectProps(z, v) {
			b.WriteString(hex.EncodeToString([]byte(p.key)))
			b.WriteByte('=')
			b.WriteString(ser(z, p.val))
			b.WriteByte(';')
		}
		b.WriteByte('}')
		return b.String()
	default:
		return "U"
	}
}

func TestPHPDifferential(t *testing.T) {
	families := []struct {
		name  string
		cases []call
	}{
		{"variable", variableCases()},
		{"strings", stringCases()},
		{"arrays", arrayCases()},
		{"math", mathCases()},
		{"json", jsonCases()},
	}
	var all []call
	var owner []string
	for _, fam := range families {
		if len(fam.cases) < 50 {
			t.Fatalf("%s has %d cases", fam.name, len(fam.cases))
		}
		for _, c := range fam.cases {
			all = append(all, c)
			owner = append(owner, fam.name)
		}
	}
	script := buildPHP(all)
	path := filepath.Join(t.TempDir(), "oracle.php")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/usr/bin/php", "-n", "-d", "extension=/usr/lib/php/20230831/ctype.so", "-d", "display_errors=stderr", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("php: %v\n%s\n%s", err, stderr.String(), out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(all) {
		t.Fatalf("php lines %d want %d\n%s", len(lines), len(all), truncate(string(out), 2000))
	}
	shown := 0
	for i, c := range all {
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
			if len(parts) != 2 {
				t.Fatalf("bad ex %q", lines[i])
			}
			msg, err := hex.DecodeString(parts[1])
			if err != nil {
				t.Fatal(err)
			}
			if gerr == nil || ExceptionClass(gerr) != parts[0] || gerr.Error() != string(msg) {
				shown++
				if shown <= 25 {
					gotC, gotM := "", ""
					if gerr != nil {
						gotC, gotM = ExceptionClass(gerr), gerr.Error()
					}
					t.Errorf("%s %s: exception got %s %q want %s %q", owner[i], c.fn, gotC, gotM, parts[0], msg)
				}
			}
			continue
		}
		parts := strings.SplitN(wantRest, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("bad ok %q", lines[i])
		}
		stdout, err := hex.DecodeString(parts[0])
		if err != nil && parts[0] != "" {
			t.Fatal(err)
		}
		if gerr != nil || string(buf.Bytes()) != string(stdout) || ser(z, got) != parts[1] {
			shown++
			if shown <= 25 {
				t.Errorf("%s %s args=%s\n stdout got %q want %q\n value got %s want %s\n err %v",
					owner[i], c.fn, describe(c.args), buf.String(), stdout, ser(z, got), parts[1], gerr)
			}
		}
	}
	if shown > 0 {
		t.Fatalf("%d mismatches", shown)
	}
}

func splitPHP(line string) (string, string) {
	_, rest, ok := strings.Cut(line, "\t")
	if !ok {
		return "", line
	}
	kind, tail, ok := strings.Cut(rest, "\t")
	if !ok {
		return kind, ""
	}
	return kind, tail
}

func describe(args []lit) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(a.php())
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func buildPHP(cases []call) string {
	var b strings.Builder
	b.WriteString(`<?php
function ser($v) {
  if (is_null($v)) return "N";
  if (is_bool($v)) return $v ? "T" : "F";
  if (is_int($v)) return "I".$v;
  if (is_float($v)) {
    if (is_nan($v)) return "Dnan";
    if (is_infinite($v)) return $v < 0 ? "D-inf" : "Dinf";
    return "D".bin2hex(pack("e", $v));
  }
  if (is_string($v)) return "S".bin2hex($v);
  if (is_array($v)) {
    $o = "A";
    foreach ($v as $k => $x) {
      $o .= is_int($k) ? "I".$k : "S".bin2hex($k);
      $o .= "=".ser($x).";";
    }
    return $o;
  }
  if (is_object($v)) {
    $o = "O".get_class($v)."{";
    foreach ((array)$v as $k => $x) {
      $ks = (string)$k;
      if ($ks !== "" && $ks[0] === "\0") {
        $p = strrpos($ks, "\0");
        $ks = substr($ks, $p + 1);
      }
      $o .= bin2hex($ks)."=".ser($x).";";
    }
    return $o."}";
  }
  return "U".gettype($v);
}
`)
	for i, c := range cases {
		b.WriteString("$fn=")
		b.WriteString(strconv.Quote(c.fn))
		b.WriteString(";$args=[")
		for j, a := range c.args {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(a.php())
		}
		b.WriteString("];ob_start();try{$r=$fn(...$args);$o=ob_get_clean();echo ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(",\"\\tOK\\t\",bin2hex($o),\"\\t\",ser($r),\"\\n\";}catch(Throwable $e){ob_end_clean();echo ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(",\"\\tEX\\t\",get_class($e),\"\\t\",bin2hex($e->getMessage()),\"\\n\";}\n")
	}
	return b.String()
}

func variableCases() []call {
	var cs []call
	scalars := []lit{Lnull(), Lbool(true), Lbool(false), Lint(0), Lint(1), Lint(-3), Lint(math.MinInt64), Lfloat(1.5), Lfloat(0), Lstr(""), Lstr("0"), Lstr("1"), Lstr("abc"), Lstr("  12  "), Lstr("1e2"), Lstr("0x10"), Larr(ap(Lint(1)))}
	for _, v := range scalars {
		cs = append(cs,
			call{"gettype", []lit{v}},
			call{"get_debug_type", []lit{v}},
			call{"is_null", []lit{v}},
			call{"is_bool", []lit{v}},
			call{"is_int", []lit{v}},
			call{"is_float", []lit{v}},
			call{"is_string", []lit{v}},
			call{"is_array", []lit{v}},
			call{"is_scalar", []lit{v}},
			call{"is_numeric", []lit{v}},
			call{"boolval", []lit{v}},
			call{"intval", []lit{v}},
			call{"floatval", []lit{v}},
			call{"strval", []lit{v}},
		)
	}
	cs = append(cs,
		call{"is_integer", []lit{Lint(2)}},
		call{"is_long", []lit{Lstr("1")}},
		call{"is_double", []lit{Lfloat(1)}},
		call{"is_object", []lit{Lnull()}},
		call{"intval", []lit{Lstr("  -12abc"), Lint(10)}},
		call{"intval", []lit{Lstr("1011"), Lint(2)}},
		call{"intval", []lit{Lstr("-ff"), Lint(16)}},
		call{"intval", []lit{Lstr("010"), Lint(0)}},
		call{"intval", []lit{Lstr("0x10"), Lint(0)}},
		call{"intval", []lit{Lstr("0b11"), Lint(0)}},
		call{"intval", []lit{Lstr("999999999999999999999")}},
		call{"intval", []lit{Lstr("10"), Lint(1)}},
		call{"floatval", []lit{Lstr("  -1.5e1x")}},
		call{"floatval", []lit{Lstr("1.5abc")}},
		call{"boolval", []lit{Lstr("0")}},
		call{"boolval", []lit{Lstr("0.0")}},
		call{"boolval", []lit{Lstr("00")}},
		call{"boolval", []lit{Lstr(" ")}},
		call{"is_numeric", []lit{Lstr(".5")}},
		call{"is_numeric", []lit{Lstr("5.")}},
		call{"is_numeric", []lit{Lstr("+1")}},
		call{"is_numeric", []lit{Lstr(" ")}},
		call{"var_dump", []lit{Lnull(), Lbool(true), Lint(1), Lfloat(1.5), Lstr("ab")}},
		call{"var_export", []lit{Lint(1)}},
		call{"var_export", []lit{Lfloat(1)}},
		call{"var_export", []lit{Lstr("b'c"), Lbool(true)}},
		call{"print_r", []lit{Lint(1)}},
		call{"print_r", []lit{Larr(ap(Lint(1)), kv(Lstr("a"), Lint(2))), Lbool(true)}},
		call{"gettype", []lit{}},
	)
	return cs
}

func stringCases() []call {
	var cs []call
	for i := range 12 {
		cs = append(cs, call{"strlen", []lit{Lstr(strings.Repeat("x", i))}})
		cs = append(cs, call{"strtoupper", []lit{Lstr("abC" + strings.Repeat("z", i%3))}})
		cs = append(cs, call{"strtolower", []lit{Lstr("ABz" + strings.Repeat("Q", i%3))}})
	}
	cs = append(cs,
		call{"strlen", []lit{Lint(12)}},
		call{"strlen", []lit{Lbool(true)}},
		call{"strlen", []lit{Lbool(false)}},
		call{"strlen", []lit{Lfloat(1.5)}},
		call{"strlen", []lit{Larr()}},
		call{"strpos", []lit{Lstr("abcdef"), Lstr("c"), Lint(-4)}},
		call{"strpos", []lit{Lstr("abcdef"), Lstr("c"), Lint(0)}},
		call{"strpos", []lit{Lstr("abc"), Lstr("z")}},
		call{"strpos", []lit{Lstr("abc"), Lstr(""), Lint(3)}},
		call{"strpos", []lit{Lstr("abc"), Lstr("a"), Lint(4)}},
		call{"strpos", []lit{Lstr("abc"), Lstr("a"), Lint(-4)}},
		call{"strpos", []lit{Lstr("abcabc"), Lstr("bc")}},
		call{"strrpos", []lit{Lstr("abcabc"), Lstr("bc"), Lint(-2)}},
		call{"strrpos", []lit{Lstr("abcabc"), Lstr("a"), Lint(2)}},
		call{"strrpos", []lit{Lstr("hello"), Lstr("l"), Lint(-2)}},
		call{"strrpos", []lit{Lstr("abc"), Lstr("")}},
		call{"str_contains", []lit{Lstr("abc"), Lstr("")}},
		call{"str_contains", []lit{Lstr("abc"), Lstr("b")}},
		call{"str_contains", []lit{Lint(123), Lstr("2")}},
		call{"str_contains", []lit{Lstr("abc"), Lstr("z")}},
		call{"str_starts_with", []lit{Lstr("abc"), Lstr("")}},
		call{"str_starts_with", []lit{Lstr("abc"), Lstr("ab")}},
		call{"str_starts_with", []lit{Lstr("abc"), Lstr("b")}},
		call{"str_ends_with", []lit{Lstr("abc"), Lstr("")}},
		call{"str_ends_with", []lit{Lstr("abc"), Lstr("bc")}},
		call{"str_ends_with", []lit{Lstr("abc"), Lstr("ab")}},
		call{"ucfirst", []lit{Lstr("hELLO")}},
		call{"ucfirst", []lit{Lstr("")}},
		call{"lcfirst", []lit{Lstr("HELLO")}},
		call{"substr", []lit{Lstr("abcdef"), Lint(-3), Lint(-1)}},
		call{"substr", []lit{Lstr("abcdef"), Lint(-2)}},
		call{"substr", []lit{Lstr("abc"), Lint(5)}},
		call{"substr", []lit{Lstr("abcdef"), Lint(2), Lint(-1)}},
		call{"substr", []lit{Lstr("abc"), Lint(0), Lint(0)}},
		call{"substr", []lit{Lstr("abc"), Lint(-10)}},
		call{"substr", []lit{Lstr("hello"), Lint(-3), Lint(2)}},
		call{"trim", []lit{Lstr("  ab  ")}},
		call{"trim", []lit{Lstr("xyabxy"), Lstr("xy")}},
		call{"ltrim", []lit{Lstr("  ab")}},
		call{"rtrim", []lit{Lstr("ab  ")}},
		call{"str_repeat", []lit{Lstr("ab"), Lint(3)}},
		call{"str_repeat", []lit{Lstr("ab"), Lint(0)}},
		call{"str_repeat", []lit{Lstr("a"), Lint(-1)}},
		call{"str_pad", []lit{Lstr("a"), Lint(4), Lstr("xy"), Lint(STR_PAD_LEFT)}},
		call{"str_pad", []lit{Lstr("a"), Lint(5), Lstr("xy"), Lint(STR_PAD_BOTH)}},
		call{"str_pad", []lit{Lstr("abcd"), Lint(2)}},
		call{"str_pad", []lit{Lstr("a"), Lint(3), Lstr("")}},
		call{"str_replace", []lit{Lstr("a"), Lstr("b"), Lstr("aaa")}},
		call{"str_replace", []lit{Larr(ap(Lstr("a")), ap(Lstr("b"))), Larr(ap(Lstr("X"))), Lstr("ab")}},
		call{"str_replace", []lit{Lstr("a"), Lstr("b"), Larr(ap(Lstr("a")), ap(Lstr("c")))}},
		call{"implode", []lit{Lstr(","), Larr(ap(Lint(1)), ap(Lstr("a")))}},
		call{"implode", []lit{Lstr(","), Larr()}},
		call{"implode", []lit{Larr(ap(Lint(1)), ap(Lint(2)))}},
		call{"join", []lit{Lstr("-"), Larr(ap(Lint(1)), ap(Lint(2)))}},
		call{"explode", []lit{Lstr(","), Lstr("a,b,c")}},
		call{"explode", []lit{Lstr(","), Lstr("a,b,c"), Lint(2)}},
		call{"explode", []lit{Lstr(","), Lstr("a,b,c"), Lint(0)}},
		call{"explode", []lit{Lstr(","), Lstr("a,b,c"), Lint(1)}},
		call{"explode", []lit{Lstr(","), Lstr("a,b,c"), Lint(-1)}},
		call{"explode", []lit{Lstr(""), Lstr("abc")}},
		call{"chr", []lit{Lint(65)}},
		call{"chr", []lit{Lint(256)}},
		call{"chr", []lit{Lint(-1)}},
		call{"chr", []lit{Lint(256 + 65)}},
		call{"ord", []lit{Lstr("A")}},
		call{"ord", []lit{Lstr("é")}},
		call{"ord", []lit{Lstr("")}},
		call{"ord", []lit{Lint(65)}},
		call{"bin2hex", []lit{Lstr("A\x00")}},
		call{"hex2bin", []lit{Lstr("4100")}},
		call{"hex2bin", []lit{Lstr("abc")}},
		call{"hex2bin", []lit{Lstr("zz")}},
		call{"sprintf", []lit{Lstr("%d %s"), Lint(3), Lstr("a")}},
		call{"sprintf", []lit{Lstr("%.2f"), Lfloat(1.2)}},
		call{"sprintf", []lit{Lstr("%04x"), Lint(255)}},
		call{"sprintf", []lit{Lstr("%g"), Lfloat(1.5)}},
		call{"sprintf", []lit{Lstr("%%")}},
		call{"printf", []lit{Lstr("%d"), Lint(7)}},
	)
	return cs
}

func arrayCases() []call {
	packed := Larr(ap(Lint(1)), ap(Lint(2)), ap(Lint(3)), ap(Lint(4)))
	assoc := Larr(kv(Lstr("a"), Lint(1)), kv(Lstr("b"), Lint(2)))
	hole := Larr(kv(Lint(0), Lint(1)), kv(Lint(2), Lint(3)))
	var cs []call
	for i := range 8 {
		cs = append(cs, call{"count", []lit{Larr(ap(Lint(int64(i))), ap(Lint(1)))}})
		cs = append(cs, call{"array_keys", []lit{Larr(kv(Lstr("k"+strconv.Itoa(i)), Lint(int64(i))))}})
	}
	cs = append(cs,
		call{"count", []lit{packed}},
		call{"sizeof", []lit{Larr(ap(Lint(1)))}},
		call{"count", []lit{Lnull()}},
		call{"count", []lit{Lint(1)}},
		call{"array_keys", []lit{Larr(kv(Lstr("a"), Lint(1)), kv(Lint(2), Lint(3)))}},
		call{"array_values", []lit{assoc}},
		call{"array_key_exists", []lit{Lstr("1"), Larr(kv(Lint(1), Lstr("x")))}},
		call{"array_key_exists", []lit{Lstr("z"), assoc}},
		call{"key_exists", []lit{Lnull(), Larr(kv(Lstr(""), Lint(1)))}},
		call{"in_array", []lit{Lstr("1"), Larr(ap(Lint(1)), ap(Lint(2)))}},
		call{"in_array", []lit{Lstr("1"), Larr(ap(Lint(1)), ap(Lint(2))), Lbool(true)}},
		call{"in_array", []lit{Lnull(), Larr(ap(Lint(0)), ap(Lnull()))}},
		call{"array_search", []lit{Lstr("1"), Larr(kv(Lstr("a"), Lint(1)))}},
		call{"array_search", []lit{Lstr("z"), Larr(ap(Lint(1)))}},
		call{"array_search", []lit{Lbool(false), Larr(ap(Lint(0)), ap(Lbool(false)))}},
		call{"array_merge", []lit{Larr(ap(Lint(1)), ap(Lint(2))), Larr(kv(Lstr("a"), Lint(3)), ap(Lint(4)))}},
		call{"array_merge", []lit{Larr(kv(Lint(2), Lstr("a"))), Larr(ap(Lstr("b")))}},
		call{"array_merge", []lit{Larr(kv(Lstr("b"), Lint(1)), kv(Lstr("a"), Lint(2))), Larr(kv(Lstr("a"), Lint(9)), kv(Lstr("c"), Lint(3)))}},
		call{"array_merge", []lit{}},
		call{"array_slice", []lit{packed, Lint(-2), Lint(1)}},
		call{"array_slice", []lit{Larr(ap(Lint(1)), ap(Lint(2)), ap(Lint(3))), Lint(1), Lnull(), Lbool(true)}},
		call{"array_slice", []lit{hole, Lint(0)}},
		call{"array_slice", []lit{packed, Lint(0), Lint(-1)}},
		call{"array_reverse", []lit{Larr(ap(Lint(1)), ap(Lint(2)), ap(Lint(3)))}},
		call{"array_reverse", []lit{assoc, Lbool(true)}},
		call{"array_reverse", []lit{assoc, Lbool(false)}},
		call{"array_fill", []lit{Lint(-2), Lint(3), Lstr("x")}},
		call{"array_fill", []lit{Lint(0), Lint(0), Lint(1)}},
		call{"array_fill", []lit{Lint(0), Lint(-1), Lint(1)}},
		call{"array_combine", []lit{Larr(ap(Lstr("a")), ap(Lstr("b"))), Larr(ap(Lint(1)), ap(Lint(2)))}},
		call{"array_combine", []lit{Larr(ap(Lint(1))), Larr(ap(Lint(1)), ap(Lint(2)))}},
		call{"range", []lit{Lint(1), Lint(3)}},
		call{"range", []lit{Lint(3), Lint(1)}},
		call{"range", []lit{Lint(0), Lint(5), Lint(2)}},
		call{"range", []lit{Lfloat(1.5), Lfloat(3.5), Lint(1)}},
		call{"range", []lit{Lstr("a"), Lstr("c")}},
		call{"range", []lit{Lstr("1"), Lstr("3")}},
		call{"range", []lit{Lint(1), Lint(3), Lint(0)}},
		call{"in_array", []lit{Lint(2), packed, Lbool(false)}},
		call{"array_search", []lit{Lint(3), packed}},
		call{"array_slice", []lit{assoc, Lint(1)}},
		call{"count", []lit{Larr(ap(Larr(ap(Lint(1)))), ap(Lint(2))), Lint(1)}},
	)
	return cs
}

func mathCases() []call {
	var cs []call
	nums := []lit{Lint(0), Lint(1), Lint(-3), Lint(math.MinInt64), Lfloat(1.5), Lfloat(-1.5), Lfloat(1.25), Lfloat(0), Lfloat(math.Inf(1)), Lfloat(math.NaN()), Lstr("-3"), Lstr("1.5"), Lstr("3.2")}
	for _, n := range nums {
		cs = append(cs, call{"abs", []lit{n}})
		cs = append(cs, call{"floor", []lit{n}})
		cs = append(cs, call{"ceil", []lit{n}})
	}
	cs = append(cs,
		call{"abs", []lit{Larr()}},
		call{"abs", []lit{Lstr("abc")}},
		call{"abs", []lit{Lnull()}},
		call{"abs", []lit{Lbool(true)}},
		call{"min", []lit{Larr(ap(Lint(3)), ap(Lint(1)), ap(Lint(2)))}},
		call{"min", []lit{Lint(3), Lfloat(1.5), Lint(2)}},
		call{"min", []lit{Lstr("10"), Lstr("2")}},
		call{"min", []lit{Lstr("abc"), Lint(1)}},
		call{"min", []lit{Larr()}},
		call{"min", []lit{}},
		call{"max", []lit{Larr(ap(Lint(1)), ap(Lint(5)), ap(Lint(2)))}},
		call{"max", []lit{Lstr("10"), Lstr("9")}},
		call{"max", []lit{Larr(ap(Lint(1))), Larr(ap(Lint(1)), ap(Lint(2)))}},
		call{"intdiv", []lit{Lint(math.MinInt64), Lint(-1)}},
		call{"intdiv", []lit{Lint(1), Lint(0)}},
		call{"intdiv", []lit{Lint(-5), Lint(2)}},
		call{"intdiv", []lit{Lint(7), Lint(3)}},
		call{"fdiv", []lit{Lint(1), Lint(0)}},
		call{"fdiv", []lit{Lfloat(1), Lfloat(2)}},
		call{"fmod", []lit{Lfloat(5.7), Lfloat(1.3)}},
		call{"fmod", []lit{Lfloat(1), Lfloat(0)}},
		call{"round", []lit{Lfloat(1.5)}},
		call{"round", []lit{Lfloat(-1.5)}},
		call{"round", []lit{Lfloat(1.25), Lint(1)}},
		call{"round", []lit{Lint(1234), Lint(-2)}},
		call{"sqrt", []lit{Lint(9)}},
		call{"sqrt", []lit{Lint(-1)}},
		call{"pow", []lit{Lint(2), Lint(10)}},
		call{"pow", []lit{Lint(2), Lint(-1)}},
		call{"pow", []lit{Lint(0), Lint(0)}},
		call{"is_nan", []lit{Lfloat(math.NaN())}},
		call{"is_nan", []lit{Lstr("foo")}},
		call{"is_nan", []lit{Lnull()}},
		call{"is_infinite", []lit{Lfloat(math.Inf(1))}},
		call{"is_infinite", []lit{Lfloat(1)}},
		call{"is_finite", []lit{Lfloat(1)}},
		call{"is_finite", []lit{Lfloat(math.Inf(-1))}},
		call{"floor", []lit{Lfloat(-1.1)}},
		call{"ceil", []lit{Lfloat(1.1)}},
	)
	return cs
}

func jsonCases() []call {
	var cs []call
	for i := range 10 {
		cs = append(cs, call{"ctype_digit", []lit{Lstr(strings.Repeat("1", i))}})
		cs = append(cs, call{"ctype_alpha", []lit{Lstr(strings.Repeat("a", i) + "Z")}})
		cs = append(cs, call{"json_encode", []lit{Lint(int64(i))}})
	}
	cs = append(cs,
		call{"json_encode", []lit{Larr(ap(Lint(1)), ap(Lint(2)))}},
		call{"json_encode", []lit{Larr(kv(Lint(0), Lint(1)), kv(Lint(2), Lint(2)))}},
		call{"json_encode", []lit{Lstr("a/b")}},
		call{"json_encode", []lit{Lstr("é")}},
		call{"json_encode", []lit{Lfloat(math.Inf(1))}},
		call{"json_encode", []lit{Lbool(true)}},
		call{"json_encode", []lit{Lnull()}},
		call{"json_encode", []lit{Larr()}},
		call{"json_encode", []lit{Larr(), Lint(JSON_FORCE_OBJECT)}},
		call{"json_encode", []lit{Lfloat(1)}},
		call{"json_encode", []lit{Lfloat(1.5)}},
		call{"json_decode", []lit{Lstr("[1,2]"), Lbool(true)}},
		call{"json_decode", []lit{Lstr(`{"a":1}`), Lbool(true)}},
		call{"json_decode", []lit{Lstr(`{"b":1,"a":2}`), Lbool(true)}},
		call{"json_decode", []lit{Lstr("1")}},
		call{"json_decode", []lit{Lstr("1.5")}},
		call{"json_decode", []lit{Lstr("{")}},
		call{"json_decode", []lit{Lstr(`{"a":1}`), Lbool(false)}},
		call{"json_encode", []lit{Larr(ap(Larr(ap(Larr(ap(Larr(ap(Lint(1))))))))), Lint(0), Lint(2)}},
		call{"ctype_digit", []lit{Lstr("")}},
		call{"ctype_alnum", []lit{Lstr("")}},
		call{"ctype_alpha", []lit{Lstr("")}},
		call{"ctype_lower", []lit{Lstr("")}},
		call{"ctype_upper", []lit{Lstr("")}},
		call{"ctype_space", []lit{Lstr("")}},
		call{"ctype_xdigit", []lit{Lstr("")}},
		call{"ctype_digit", []lit{Lint(48)}},
		call{"ctype_digit", []lit{Lint(5)}},
		call{"ctype_digit", []lit{Lint(-1)}},
		call{"ctype_alnum", []lit{Lstr("A1")}},
		call{"ctype_alpha", []lit{Lstr("Ab")}},
		call{"ctype_alpha", []lit{Lstr("A1")}},
		call{"ctype_lower", []lit{Lstr("abc")}},
		call{"ctype_lower", []lit{Lstr("abC")}},
		call{"ctype_upper", []lit{Lstr("ABC")}},
		call{"ctype_space", []lit{Lstr(" \t\n\r\v\f")}},
		call{"ctype_xdigit", []lit{Lstr("0aF")}},
		call{"ctype_xdigit", []lit{Lstr("0g")}},
		call{"json_encode", []lit{Lstr("a\nb")}},
		call{"json_decode", []lit{Lstr("null")}},
		call{"json_decode", []lit{Lstr("true"), Lbool(true)}},
		call{"json_encode", []lit{Larr(kv(Lstr("a"), Lint(1)), kv(Lstr("b"), Lint(2)))}},
	)
	return cs
}

func TestSubstrNoZoneLeakAndStringZeroGCAllocs(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	hay, err := types.NewString(z, "abcdefghijklmnopqrstuvwx", 0)
	if err != nil {
		t.Fatal(err)
	}
	needle, err := types.NewString(z, "mnop", 0)
	if err != nil {
		t.Fatal(err)
	}
	padded, err := types.NewString(z, "  Hello World  ", 0)
	if err != nil {
		t.Fatal(err)
	}
	baseLive := z.LiveBytes()

	// 1. Vérifier que substr sur TypeString et sur TypeLong ne fuit aucun octet dans la Zone.
	for range 1000 {
		sub, err := nativeSubstr(z, nil, []types.Value{hay, types.NewLong(1), types.NewLong(22)})
		if err != nil {
			t.Fatal(err)
		}
		types.ReleaseValue(z, &sub)

		subInt, err := nativeSubstr(z, nil, []types.Value{types.NewLong(123456789), types.NewLong(2), types.NewLong(4)})
		if err != nil {
			t.Fatal(err)
		}
		types.ReleaseValue(z, &subInt)
	}
	if got := z.LiveBytes(); got != baseLive {
		t.Fatalf("Zone leak in nativeSubstr: LiveBytes=%d, want %d", got, baseLive)
	}

	// 2. Vérifier 0 allocation sur le tas GC Go pour substr, strpos, str_contains, strtolower, trim.
	argsSub := []types.Value{hay, types.NewLong(1), types.NewLong(22)}
	argsPos := []types.Value{hay, needle}
	argsTrim := []types.Value{padded}
	allocs := testing.AllocsPerRun(200, func() {
		v1, _ := nativeSubstr(z, nil, argsSub)
		types.ReleaseValue(z, &v1)
		_, _ = nativeStrpos(z, nil, argsPos)
		_, _ = nativeContains(z, nil, argsPos)
		v2, _ := nativeStrtolower(z, nil, argsTrim)
		types.ReleaseValue(z, &v2)
		v3, _ := nativeTrim(z, nil, argsTrim)
		types.ReleaseValue(z, &v3)
	})
	if allocs != 0 {
		t.Fatalf("unexpected Go heap allocs: got %v, want 0", allocs)
	}
}

