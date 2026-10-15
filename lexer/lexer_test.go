package lexer

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const oracleProgram = `$tokens = PhpToken::tokenize(file_get_contents($argv[1])); foreach ($tokens as $t) { echo $t->id, "\t", $t->getTokenName(), "\t", $t->line, "\t", base64_encode($t->text), "\n"; }`

func comparePHP(t *testing.T, src string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.php")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-d", "extension=tokenizer", "-d", "display_errors=stderr", "-r", oracleProgram, path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("PHP oracle: %v\n%s\n%s", err, stderr.Bytes(), out)
	}
	if stderr.Len() != 0 {
		t.Logf("PHP lexical diagnostics (not token text): %s", stderr.Bytes())
	}
	got := Tokenize(src)
	rows := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(out) == 0 {
		rows = nil
	}
	pos := 0
	for i, row := range rows {
		cols := strings.Split(row, "\t")
		if len(cols) != 4 {
			t.Fatalf("oracle row %q", row)
		}
		id, err := strconv.Atoi(cols[0])
		if err != nil {
			t.Fatal(err)
		}
		line, err := strconv.Atoi(cols[2])
		if err != nil {
			t.Fatal(err)
		}
		text, err := base64.StdEncoding.DecodeString(cols[3])
		if err != nil {
			t.Fatal(err)
		}
		want := Token{ID: ID(id), Text: string(text), Line: line, Pos: pos}
		if i >= len(got) {
			t.Fatalf("token %d missing: %+v (%s)", i, want, cols[1])
		}
		if got[i] != want || got[i].ID.String() != cols[1] {
			t.Fatalf("token %d at byte %d: got %+v (%s), want %+v (%s)", i, pos, got[i], got[i].ID, want, cols[1])
		}
		pos += len(text)
	}
	if len(got) != len(rows) {
		t.Fatalf("token count got %d want %d", len(got), len(rows))
	}
	if pos != len(src) {
		t.Fatalf("source coverage %d/%d", pos, len(src))
	}
}

func TestPHPVersion(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-d", "extension=tokenizer", "-r", `echo PHP_VERSION, ":", PHP_INT_SIZE, ":", (int)class_exists("PhpToken"), ":", (int)ini_get("short_open_tag");`).CombinedOutput()
	if err != nil || string(out) != "8.3.6:8:1:1" {
		t.Fatalf("oracle configuration: %q %v", out, err)
	}
}

func TestDifferential(t *testing.T) {
	cases := []string{
		"", "plain\r\nhtml", "<?php", "x<?PHP\r\necho 1;?>\r\nx<?=2?>\ny", "<?phpX <? x <?= 3 ?>",
		"<?php \x00\v\f // close ?>\r\nhtml<?php # x\r/** doc */ /**/ /**x*/ /**\t*/ /*\x00*/",
		`<?php self parent static readonly enum E {} enum() enum extends enum implements enum X; match fn yield from $a; yield /* c */ FROM $b; yield fromx;`,
		`<?php function match(){} const readonly=1; enum case; case match; $o->match; $o?->readonly; $o-> class; $o->/*c*/match; Foo\Bar \Foo\Bar namespace\Foo Foo\match;`,
		"<?php ( int ) (\tINTEGER\t) (float) (double) (real) (binary) (string) (array) (object) (bool) (boolean) (unset) (\nint)",
		`<?php 0 09 0777 0o77 0O77 0xAf 0XFF 0b101 0B101 1_000 0xA_B 1__2 0x_1 0b102 0o78 1. .1 1.e+2 1_2.3_4E-5_6 1e 1e+ 9223372036854775807 9223372036854775808 0x7fff_ffff_ffff_ffff 0x8000_0000_0000_0000 01777777777777777777777;`,
		`<?php === !== == != <> <=> <= >= << >> <<= >>= ++ -- ** **= ?? ??= ... :: => += -= *= /= .= %= &= |= ^= && || & $x & /*c*/ ...$x &Foo; #[A]`,
		`<?php 'simple\\\'quoted' "constant\" \$" b'bytes' B"bytes $x" "hi $x {$x} ${x} ${x[0]} ${foo + 1} $a[0] $a[key] $a[$i] $o->class $o?->match!";`,
		"<?php `echo $x {$o->p}`; \"{$a[\"{$b}\"]}\";",
		"<?php <<<LABEL\n hello $x {$a[0]} ${x}\n LABEL;\necho 1;",
		"<?php <<<'LABEL'\r\n $x {$x} \\n\r\n LABEL;\r\n",
		"<?php <<<\"E\"\nE;\n<<<E\nE\n<<<E\nE",
		"<?php b<<<E\n  text\n  E, 1;\n<<<E\n E_suffix\nE;",
		`<?php __halt_compiler /*c*/ ( ) ; binary <?php $x`,
		`<?php "unterminated $x`, `<?php 'unterminated`, `<?php /* unterminated`,
		"<?php \"\\u{X}\ntext\";\necho 1;",
		"<?php \"\\u{}\r\ntext $x\";\necho 1;",
		"<?php `\\u{110000}\ntext $x`;\necho 1;",
	}
	keywords := "abstract array as break callable case catch class clone const continue declare default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile eval exit die extends final finally for foreach function global goto if implements include include_once instanceof insteadof interface isset list namespace new print private protected public require require_once return switch throw trait try unset use var while and or xor __CLASS__ __TRAIT__ __FUNCTION__ __METHOD__ __LINE__ __FILE__ __DIR__ __NAMESPACE__"
	cases = append(cases, "<?php "+keywords, "<?php "+strings.ToUpper(keywords))
	for i, src := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) { comparePHP(t, src) })
	}
}

func TestLexicalBoundaries(t *testing.T) {
	for word := range strings.FieldsSeq("self parent enum readonly match fn function const case namespace yield from __halt_compiler") {
		for _, prefix := range []string{"", "$o->", "$o?->", "$o-> ", "$o->/*x*/", "function ", "const ", "enum ", "case ", "X\\", "\\", "namespace\\"} {
			t.Run(prefix+word, func(t *testing.T) { comparePHP(t, "<?php "+prefix+word+";") })
		}
	}
	for i := range 256 {
		t.Run(fmt.Sprintf("byte_%02x", i), func(t *testing.T) { comparePHP(t, "<?php "+string(byte(i))+" x ?>"+string(byte(i))+"<?php echo 1;") })
	}
	for _, body := range []string{
		`$a[0]`, `$a[01]`, `$a[0xFF]`, `$a[1_2]`, `$a[1.2]`, `$a['key']`, `$a[ key]`, `$a["key"]`, `$a[-1]`, `$a[[]]`,
		`$a[${x}]`, `$a[$x]`, `$o->match`, `$o?->enum`, `$o->x->y`, `$o-> x`, `$o->`, `$o?->`, `${x}`, `${x[0]}`, `${x + 1}`,
		`{$a["{$b}"]}`, `{$o->{"$x"}}`, `$$x`, `\$x`, `\\$x`, `\{$x}`, `{$x`, `${x`, `$a[`,
	} {
		for _, quote := range []string{"\"", "`"} {
			t.Run(quote+body, func(t *testing.T) { comparePHP(t, "<?php "+quote+body+quote+";") })
		}
		t.Run("heredoc_"+body, func(t *testing.T) { comparePHP(t, "<?php <<<E\n"+body+"\nE;\n") })
	}
	for _, end := range []string{"", "\n", ";", "_", "0", "é", "-", "\r\n"} {
		for _, indent := range []string{"", " ", "\t", " \t"} {
			for _, quote := range []string{"", "'", "\""} {
				t.Run(fmt.Sprintf("doc_%q_%q_%q", quote, indent, end), func(t *testing.T) { comparePHP(t, "<?php <<<"+quote+"E"+quote+"\r\n body $x\r\n"+indent+"E"+end) })
			}
		}
	}
	for _, base := range []int{2, 8, 10, 16} {
		prefix := map[int]string{2: "0b", 8: "0o", 10: "", 16: "0x"}[base]
		for _, value := range []uint64{0, 1, 1<<63 - 1, 1 << 63, 1<<63 + 1, ^uint64(0)} {
			text := strconv.FormatUint(value, base)
			for _, literal := range []string{prefix + text, prefix + "000" + text, prefix + text + "0"} {
				t.Run(literal, func(t *testing.T) { comparePHP(t, "<?php "+literal+";") })
			}
		}
	}
}

func TestStreamingAllocations(t *testing.T) {
	src := `<?php #[A] function f($x) { return "value {$x[0]} $x"; }`
	want := Tokenize(src)
	if allocs := testing.AllocsPerRun(100, func() {
		l := New(src)
		for _, token := range want {
			if got := l.Next(); got != token {
				t.Fatalf("stream: %+v != %+v", got, token)
			}
		}
		if l.Next().ID != EOF || l.Next().ID != EOF {
			t.Fatal("unstable EOF")
		}
	}); allocs != 0 {
		t.Fatalf("stream allocations=%g", allocs)
	}
}

func BenchmarkLexer(b *testing.B) {
	data, err := os.ReadFile("../docs/lot2_1_astra/php-src-php-8.3.6/Zend/tests/attributes/009_doctrine_annotations_example.phpt")
	if err != nil {
		b.Fatal(err)
	}
	src := string(data)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for b.Loop() {
		l := New(src)
		for l.Next().ID != EOF {
		}
	}
}

func TestPHPTCorpus(t *testing.T) {
	baseDir := "../docs/lot2_1_astra"
	if _, err := os.Stat(baseDir); err != nil {
		baseDir = "/devhoros/php127/docs/lot2_1_astra"
	}
	root := filepath.Join(baseDir, "php-src-php-8.3.6")
	manifest, err := os.ReadFile(filepath.Join(baseDir, "corpus.json"))
	if err != nil {
		t.Skipf("PHPT corpus absent (%v)", err)
	}
	var records []struct {
		Path   string
		SHA256 string
	}
	if err := json.Unmarshal(manifest, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 724 {
		t.Fatalf("corpus denominator=%d want 724", len(records))
	}
	expected := make(map[string]string, len(records))
	for _, record := range records {
		if _, ok := expected[record.Path]; ok {
			t.Fatalf("duplicate corpus path %s", record.Path)
		}
		expected[record.Path] = record.SHA256
	}
	count := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".phpt") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if digest := fmt.Sprintf("%x", sha256.Sum256(data)); digest != expected[relative] {
			return fmt.Errorf("corpus integrity: %s hash %s", relative, digest)
		}
		// Only extract the FILE section; execution-only INI, SKIPIF and EXPECT
		// sections are irrelevant to the lexical oracle.
		var marker []byte
		start := -1
		fileEOF := false
		for _, candidate := range []string{"--FILE--\n", "--FILE--\r\n", "--FILEEOF--\n", "--FILEEOF--\r\n"} {
			if index := bytes.Index(data, []byte(candidate)); index >= 0 {
				marker, start = []byte(candidate), index
				fileEOF = strings.Contains(candidate, "FILEEOF")
				break
			}
		}
		if start < 0 {
			return fmt.Errorf("missing FILE section: %s", path)
		}
		code := data[start+len(marker):]
		end := -1
		for offset := 0; offset < len(code); {
			next := bytes.IndexByte(code[offset:], '\n')
			if next < 0 {
				next = len(code)
			} else {
				next += offset + 1
			}
			line := bytes.TrimSuffix(bytes.TrimSuffix(code[offset:next], []byte("\n")), []byte("\r"))
			if len(line) > 4 && bytes.HasPrefix(line, []byte("--")) && bytes.HasSuffix(line, []byte("--")) {
				section := true
				for _, c := range line[2 : len(line)-2] {
					if c != '_' && (c < 'A' || c > 'Z') {
						section = false
						break
					}
				}
				if section {
					end = offset
					break
				}
			}
			offset = next
		}
		if end < 0 {
			return fmt.Errorf("missing section after FILE: %s", path)
		}
		code = code[:end]
		if fileEOF {
			code = bytes.TrimRight(code, "\r\n")
		}
		count++
		t.Run(strings.TrimPrefix(path, root+"/"), func(t *testing.T) { comparePHP(t, string(code)) })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != len(records) {
		t.Fatalf("corpus coverage %d/%d", count, len(records))
	}
	t.Logf("compared %d upstream PHPT FILE sections", count)
}

func TestDeepInterpolation(t *testing.T) {
	comparePHP(t, "<?php "+strings.Repeat("{", 128)+`echo "{$a["{$b}"]}";`+strings.Repeat("}", 128))
}

func FuzzDifferential(f *testing.F) {
	baseDir := "../docs/lot2_1_astra/php-src-php-8.3.6/"
	if _, err := os.Stat(baseDir); err != nil {
		baseDir = "/devhoros/php127/docs/lot2_1_astra/php-src-php-8.3.6/"
	}
	for _, path := range []string{
		"Zend/tests/heredoc_005.phpt", "Zend/tests/flexible-heredoc-complex-test1.phpt", "Zend/tests/enum/backed-int.phpt",
	} {
		data, err := os.ReadFile(baseDir + path)
		if err != nil {
			continue
		}
		f.Add(string(data))
	}
	f.Fuzz(func(t *testing.T, src string) { comparePHP(t, src) })
}

func TestSgoiterParityVsGCCOracle(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "test_php_c_oracle")
	cmd := exec.CommandContext(t.Context(), "gcc", "-O2", "-Wall", "-Wextra", "-Werror", "-mavx2",
		"c_src/test_php_c_oracle.c", "c_src/php_kw_resolve.c", "c_src/php_scan.c", "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	out, err := exec.CommandContext(t.Context(), bin).CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("php_c_oracle: OK")) {
		t.Fatalf("C oracle self-check: %v\n%s", err, out)
	}

	kwOut, err := exec.CommandContext(t.Context(), bin, "kw").Output()
	if err != nil {
		t.Fatalf("C oracle kw cases: %v", err)
	}
	tables := map[string]struct {
		t     *C2_kw_table_t
		ident uint16
	}{
		"kw":   {&PhpKwTable, uint16(T_STRING)},
		"cast": {&PhpCastTable, uint16(EOF)},
	}
	kwCount := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(kwOut)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 8 {
			t.Fatalf("malformed kw line: %q", line)
		}
		tab := tables[fields[0]]
		fill, err1 := strconv.ParseUint(fields[1], 16, 8)
		length, err2 := strconv.ParseUint(fields[2], 10, 32)
		var word []byte
		var err3 error
		if fields[3] != "-" {
			word, err3 = hex.DecodeString(fields[3])
		}
		if err1 != nil || err2 != nil || err3 != nil || uint64(len(word)) != length {
			t.Fatalf("invalid kw fields: %q", line)
		}
		buf := bytes.Repeat([]byte{byte(fill)}, 32)
		copy(buf, word)
		gotScalar := C2_kw_resolve_scalar(tab.t, buf, uint32(length), tab.ident)
		gotResolve := C2_kw_resolve(tab.t, buf, uint32(length), tab.ident)
		gotPyramid := C2_kw_resolve_pyramid(tab.t, buf, uint32(length), tab.ident)
		if strconv.Itoa(int(gotScalar)) != fields[4] || strconv.Itoa(int(gotResolve)) != fields[6] || strconv.Itoa(int(gotPyramid)) != fields[7] {
			t.Fatalf("kw parity mismatch on %q: Go=(%d,%d,%d) C=(%s,%s,%s)", line, gotScalar, gotResolve, gotPyramid, fields[4], fields[6], fields[7])
		}
		kwCount++
	}
	if kwCount == 0 {
		t.Fatal("zero kw oracle cases")
	}

	scanOut, err := exec.CommandContext(t.Context(), bin, "scan").Output()
	if err != nil {
		t.Fatalf("C oracle scan cases: %v", err)
	}
	scanCount := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(scanOut)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 9 {
			t.Fatalf("malformed scan line: %q", line)
		}
		pos, err1 := strconv.Atoi(fields[0])
		end, err2 := strconv.Atoi(fields[1])
		var buf []byte
		var err3 error
		if fields[2] != "-" {
			buf, err3 = hex.DecodeString(fields[2])
		}
		if err1 != nil || err2 != nil || err3 != nil || len(buf) != end {
			t.Fatalf("invalid scan fields: %q", line)
		}
		got := [6]int{
			Php_scan_label(buf, pos, end),
			Php_scan_line_comment(buf, pos, end),
			Php_scan_single_quote(buf, pos, end),
			Php_scan_double_quote(buf, pos, end),
			Php_scan_inline_html(buf, pos, end),
			Php_scan_block_comment(buf, pos, end),
		}
		gotScalar := [6]int{
			Php_scan_label_scalar(buf, pos, end),
			Php_scan_line_comment_scalar(buf, pos, end),
			Php_scan_single_quote_scalar(buf, pos, end),
			Php_scan_double_quote_scalar(buf, pos, end),
			Php_scan_inline_html_scalar(buf, pos, end),
			Php_scan_block_comment_scalar(buf, pos, end),
		}
		for k := range 6 {
			want, err := strconv.Atoi(fields[3+k])
			if err != nil || got[k] != want || gotScalar[k] != want {
				t.Fatalf("scan kernel %d mismatch at pos=%d end=%d: got=%d scalar=%d want=%d", k, pos, end, got[k], gotScalar[k], want)
			}
		}
		scanCount++
	}
	if scanCount == 0 {
		t.Fatal("zero scan oracle cases")
	}
	t.Logf("verified %d kw cases and %d scan cases against gcc -O2", kwCount, scanCount)
}

