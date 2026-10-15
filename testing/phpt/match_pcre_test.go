package phpt

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestMatchPCRECompilation(t *testing.T) {
	for _, pattern := range []string{
		`[:a\]b:]`, `[.a\].]`, `[=a\]=]`, `\p{Letter}`, `\p{Assigned}`,
		`(?<1a>x)`, `(?<a>x)(?<a>y)`, `(?<` + strings.Repeat("a", 33) + `>x)`,
		strings.Repeat("(", 251) + "x" + strings.Repeat(")", 251),
		`(?:[a-z][0-9]){1000}`, `[`, `a/b`, `[\d-z]`, `[:alpha:]`, `[.a.]`,
		`\p{Kawi}`, `a$)b`, `[0-[:alpha:]]`,
	} {
		for _, kind := range []string{"EXPECTREGEX", "EXPECTF"} {
			expected := pattern
			if kind == "EXPECTF" {
				expected = "%r" + expected + "%r"
			}
			if got, err := Match(kind, []byte(expected), []byte("x")); err == nil || got {
				t.Errorf("%s %q: got=%v err=%v; want compilation error", kind, expected, got, err)
			}
		}
	}
}

func TestMatchWorkerLifecycle(t *testing.T) {
	// Real subprocess I/O cannot use synctest's virtual clock.
	if got, err := Match("EXPECTREGEX", []byte("a"), []byte("a")); err != nil || !got {
		t.Fatalf("start: %v %v", got, err)
	}
	matcher.mu.Lock()
	process := matcher.cmd.Process
	matcher.mu.Unlock()
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 25 {
				if got, err := Match("EXPECTREGEX", []byte("a"), []byte("a")); err != nil || !got {
					t.Errorf("concurrent match: %v %v", got, err)
				}
			}
		})
	}
	group.Wait()
	matcher.mu.Lock()
	reused := matcher.cmd.Process == process
	err := process.Kill()
	matcher.mu.Unlock()
	if !reused || err != nil {
		t.Fatalf("reuse=%v kill=%v", reused, err)
	}
	if got, err := Match("EXPECTREGEX", []byte("a"), []byte("a")); err != nil || !got {
		t.Fatalf("restart: %v %v", got, err)
	}
	matcher.mu.Lock()
	restarted := matcher.cmd.Process != process
	matcher.mu.Unlock()
	if !restarted {
		t.Fatal("dead worker was not replaced")
	}
}

func TestMatchOracleErrorsAndBinaryFrames(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-php")
	if got, err := matchWithOracle(missing, "EXPECT", []byte("a"), []byte("a")); err != nil || !got {
		t.Fatalf("EXPECT must not start PHP: %v %v", got, err)
	}
	if _, err := matchWithOracle(missing, "EXPECTF", []byte("%r["), nil); err == nil || err.Error() != "unterminated EXPECTF %r block" {
		t.Fatalf("raw validation must precede PHP: %v", err)
	}
	if got, err := matchWithOracle(missing, "EXPECTREGEX", []byte("a"), nil); err == nil || got {
		t.Fatalf("missing PHP: %v %v", got, err)
	}
	data := []byte(strings.Repeat("\x00\xff\r\n\t", 20000))
	if got, err := Match("EXPECTF", data, data); err == nil || got {
		// PCRE's pattern size bound must not become a truncated match.
		t.Fatalf("oversized pattern: %v %v", got, err)
	}
	if got, err := Match("EXPECTF", []byte("%a"), data); err != nil || !got {
		t.Fatalf("large binary frame after error: %v %v", got, err)
	}
	if got, err := Match("EXPECTREGEX", []byte(`(a+)+`), []byte(strings.Repeat("a", 10000)+"b")); err == nil || got {
		t.Fatalf("PCRE execution limit: %v %v", got, err)
	}
}

func TestRunUsesConfiguredMatcherOracle(t *testing.T) {
	t.Cleanup(func() {
		matcher.mu.Lock()
		defer matcher.mu.Unlock()
		matcher.stop()
	})
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	oracle := writeFixture(t, dir, "oracle", "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+logPath+"'\nexec /usr/bin/php \"$@\"\n")
	path := writeFixture(t, dir, "case.phpt", "--TEST--\nconfigured matcher\n--FILE--\n<?php echo 'a'; ?>\n--EXPECTREGEX--\n(?=a)a")
	result, err := Run(t.Context(), path, Options{Target: "/usr/bin/php", Oracle: oracle})
	if err != nil || result.Verdict != "PASS" {
		t.Fatalf("configured oracle: %+v %v", result, err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil || !strings.HasPrefix(string(logData), "-n -r") {
		t.Fatalf("configured matcher not invoked: %s %v", logData, err)
	}
	matcher.mu.Lock()
	finalOracle := matcher.path
	matcher.mu.Unlock()
	if finalOracle != oracle {
		t.Fatalf("final matcher oracle=%q; want %q", finalOracle, oracle)
	}
}

func TestMatchPCRERecursionEnvelope(t *testing.T) {
	for _, recursion := range []string{`(?R)`, `(?0)`, `\g<0>`} {
		pattern := `(?(R)[^\n]*|(?!` + recursion + `).*)`
		for _, kind := range []string{"EXPECTREGEX", "EXPECTF"} {
			expected := pattern
			if kind == "EXPECTF" {
				expected = "%r" + pattern + "%r"
			}
			command := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", expectfOracle)
			command.Stdin = strings.NewReader(kind + "\t" + base64.StdEncoding.EncodeToString([]byte(expected)) + "\t" + base64.StdEncoding.EncodeToString([]byte("x\n")) + "\n")
			output, err := command.CombinedOutput()
			if err != nil || string(output) != "0:1\n" {
				t.Fatalf("%s %q: oracle=%q err=%v; want 0:1", kind, pattern, output, err)
			}
			if got, err := Match(kind, []byte(expected), []byte("x\n")); err != nil || got {
				t.Errorf("%s %q: match=%v err=%v; official envelope rejects", kind, pattern, got, err)
			}
		}
	}
}

func TestMatchReprise6AuditFiles(t *testing.T) {
	root := os.Getenv("PHPT_AUDIT_ROOT")
	if root == "" {
		t.Skip("set PHPT_AUDIT_ROOT to the original audit worktrees")
	}
	type auditCase struct{ kind, pattern, actual string }
	var cases []auditCase
	for _, source := range []struct {
		revision, name string
		count          int
	}{
		{"3", "cas_r3.txt", 45}, {"4", "cas_r4.txt", 33}, {"4", "cas_r4b.txt", 20},
		{"5", "cas_r5.txt", 64}, {"5", "cas_r5b.txt", 18}, {"5", "cas_r5c.txt", 5},
	} {
		path := filepath.Join(root, "audit-php127-lot02-reprise"+source.revision, "sondes", source.name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if len(lines) != source.count {
			t.Fatalf("%s: %d cases, want %d", path, len(lines), source.count)
		}
		for _, line := range lines {
			fields := strings.Split(line, "\t")
			if len(fields) != 3 {
				t.Fatalf("bad audit line %q", line)
			}
			decode := func(field string) string {
				if source.revision == "5" {
					value, err := strconv.Unquote(field)
					if err != nil {
						t.Fatal(err)
					}
					return value
				}
				value, err := base64.StdEncoding.DecodeString(field)
				if err != nil {
					t.Fatal(err)
				}
				return string(value)
			}
			cases = append(cases, auditCase{fields[0], decode(fields[1]), decode(fields[2])})
		}
		t.Logf("%s: %d cases read", path, len(lines))
	}
	props, err := os.ReadFile(filepath.Join(root, "audit-php127-lot02-reprise5/sondes/props_php_refus.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, names, ok := strings.Cut(string(props), "\n")
	if !ok || len(strings.Fields(names)) != 47 {
		t.Fatal("want 47 property names")
	}
	for name := range strings.FieldsSeq(names) {
		cases = append(cases, auditCase{"EXPECTREGEX", `\p{` + name + `}`, "a"})
	}
	var input strings.Builder
	for _, tc := range cases {
		fmt.Fprintf(&input, "%s\t%s\t%s\n", tc.kind, base64.StdEncoding.EncodeToString([]byte(tc.pattern)), base64.StdEncoding.EncodeToString([]byte(tc.actual)))
	}
	command := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", expectfOracle)
	command.Stdin = strings.NewReader(input.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("oracle: %v %s", err, output)
	}
	results := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(results) != len(cases) {
		t.Fatal("oracle result count mismatch")
	}
	falsePass, strictRefusal, anchorDifferences, rawRefusals, compileDifferences := 0, 0, 0, 0, 0
	for i, tc := range cases {
		official, strict, ok := strings.Cut(results[i], ":")
		if !ok {
			t.Fatal(results[i])
		}
		got, err := Match(tc.kind, []byte(tc.pattern), []byte(tc.actual))
		unclosed := tc.kind == "EXPECTF" && strings.Count(tc.pattern, "%r")%2 != 0
		if unclosed {
			rawRefusals++
		}
		if official != "E" && strict == "E" {
			compileDifferences++
			t.Logf("case %d: strict wrapper rejects compilation (pattern bytes=%d, prefix=%.32q)", i+1, len(tc.pattern), tc.pattern)
		}
		if official == "1" && strict == "0" {
			anchorDifferences++
		}
		if official == "E" || strict == "E" || unclosed {
			if err == nil || got {
				t.Errorf("case %d %q: want error, got=%v err=%v PHP=%s", i+1, tc.pattern, got, err, results[i])
			}
			continue
		}
		if err != nil {
			t.Errorf("case %d: unexpected error %v", i+1, err)
		}
		if got && official != "1" {
			falsePass++
		}
		if !got && strict == "1" {
			strictRefusal++
		}
	}
	t.Logf("cases=%d false_pass=%d strict_refusal=%d dollar_vs_z=%d strict_compile_difference=%d unclosed_raw=%d", len(cases), falsePass, strictRefusal, anchorDifferences, compileDifferences, rawRefusals)
	if len(cases) != 232 || falsePass != 0 || strictRefusal != 0 || anchorDifferences != 1 || compileDifferences != 3 || rawRefusals != 1 {
		t.Fatal("audit mismatch")
	}
}

func BenchmarkMatchPCRE(b *testing.B) {
	for b.Loop() {
		if got, err := Match("EXPECTF", []byte("value=%d"), []byte("value=42")); err != nil || !got {
			b.Fatalf("match=%v err=%v", got, err)
		}
	}
}

func TestMatchPCRESemantics(t *testing.T) {
	for _, tc := range []struct {
		kind, pattern, actual string
		want                  bool
	}{
		{"EXPECTREGEX", `(?=a)a`, "a", true},
		{"EXPECTREGEX", strings.Repeat("(x)", 10) + `\10`, strings.Repeat("x", 11), true},
		{"EXPECTF", `%f.5`, ".5", true},
		{"EXPECTF", `%r(a)\2%r`, "aa", true},
		{"EXPECTF", `%r(a)%r%r(b)\4%r`, "abb", true},
		{"EXPECTREGEX", `[a-b-\d]`, "5", true},
		{"EXPECTREGEX", `[a-b-\d]`, "-", true},
		{"EXPECTF", "x\r\n", "x\n", false},
		{"EXPECTREGEX", "x\r\n", "x\n", false},
		{"EXPECTF", "x\n", "x\r\n", false},
		{"EXPECTREGEX", "x\n", "x\r\n", false},
		{"EXPECTF", "x\r\n", "x\r\n", true},
		{"EXPECTREGEX", "x", "x\n", false},
		{"EXPECTREGEX", "\x00\xff", "\x00\xff", true},
	} {
		if got, err := Match(tc.kind, []byte(tc.pattern), []byte(tc.actual)); err != nil || got != tc.want {
			t.Errorf("%s %q / %q: got=%v err=%v want=%v", tc.kind, tc.pattern, tc.actual, got, err, tc.want)
		}
	}
}
