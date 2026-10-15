package phpt

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
	"unicode"
)

func writeFixture(t *testing.T, dir, name, data string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParsePreservesBytes(t *testing.T) {
	input := "--TEST--\r\nname\r\n--FILE--\r\n<?php echo ' x '; ?>\r\n--EXPECT--\r\n x \r\n\x00\xff"
	test, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if test.Name != "name\r\n" || string(test.Code) != "<?php echo ' x '; ?>\r\n" || string(test.Expected) != " x \r\n\x00\xff" {
		t.Fatalf("sections altered: %+v", test)
	}
	large := bytes.Repeat([]byte("x"), 100_000)
	test, err = Parse(append([]byte("--TEST--\nn\n--FILE--\n<?php ?>\n--EXPECT--\n"), large...))
	if err != nil || !bytes.Equal(test.Expected, large) {
		t.Fatalf("long section: %v", err)
	}
}

func TestParseRejectsInvalidSections(t *testing.T) {
	base := "--TEST--\nn\n--FILE--\n<?php ?>\n--EXPECT--\n"
	for _, input := range []string{
		"", "junk\n" + base,
		strings.Replace(base, "--FILE--\n<?php ?>\n", "", 1),
		strings.Replace(base, "--TEST--\nn\n", "", 1),
		base + "--EXPECTF--\n%s", base + "--FILE_EXTERNAL--\nx.php\n",
		base + "--TEST--\nagain\n", base + "--INI--\nprecision=2\n",
		strings.Replace(base, "--EXPECT--", "--UNKNOWN--", 1),
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("accepted invalid input %q", input)
		}
	}
}

func TestParsePreservesNonSectionDelimiterLines(t *testing.T) {
	expected := "--a value--\n--123--\n----\n"
	test, err := Parse([]byte("--TEST--\nn\n--FILE--\n<?php ?>\n--EXPECT--\n" + expected))
	if err != nil || string(test.Expected) != expected {
		t.Fatalf("literal delimiter lines: %+v, %v", test, err)
	}
}

func TestMatchStrictBytesAndFormats(t *testing.T) {
	for _, tc := range []struct {
		kind, expected, actual string
		want                   bool
	}{
		{"EXPECT", "x\n", "x\n", true},
		{"EXPECT", "x\n", "x", false},
		{"EXPECT", "x \n", "x\n", false},
		{"EXPECT", "x\r\n", "x\n", false},
		{"EXPECT", "\xff", "\xfe", false},
		{"EXPECTF", "%s %d %i %x %f %e", "word 12 -3 aF -1.25e+3 " + string(filepath.Separator), true},
		{"EXPECTF", "%%", "%%", true},
		{"EXPECTF", "%%d", "%12", true},
		{"EXPECTF", "%s", "", false},
		{"EXPECTF", "%s", "a\nb", false},
		{"EXPECTF", "%d", "-1", false},
		{"EXPECTF", "%f", "+.25", true},
		{"EXPECTF", "%f", "1e", false},
		{"EXPECTF", "%f", "1.", false},
		{"EXPECTF", "%0", "\x00", true},
		{"EXPECTF", "[%d]", "[7]", true},
		{"EXPECTF", "%S%w%A", "", true},
		{"EXPECTF", "%a", "a\nb", true},
		{"EXPECTF", "%c", "\xff", true},
		{"EXPECTF", "\xff", "\xfe", false},
		{"EXPECTREGEX", "\xff", "\xfe", false},
		{"EXPECTF", "%r[a-z]+%r", "abc", true},
		{"EXPECTREGEX", "x[0-9]+\n", "x12\n", true},
		{"EXPECTREGEX", "x|y", "prefixx", false},
		{"EXPECTREGEX", "x", "x\n", false},
	} {
		t.Run(tc.kind+"/"+tc.expected+"/"+tc.actual, func(t *testing.T) {
			got, err := Match(tc.kind, []byte(tc.expected), []byte(tc.actual))
			if err != nil || got != tc.want {
				t.Fatalf("Match = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct{ kind, pattern string }{
		{"EXPECTREGEX", "["},
		{"EXPECTF", "%r["}, {"UNKNOWN", "x"},
	} {
		if _, err := Match(tc.kind, []byte(tc.pattern), []byte("a")); err == nil {
			t.Errorf("accepted invalid pattern %+v", tc)
		}
	}
}

func TestRunPHPVerdicts(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"pass", "--FILE--\n<?php echo \"hello\\n\"; ?>\n--EXPECT--\nhello\n", "PASS"},
		{"fail", "--FILE--\n<?php echo \"hello\\n\"; ?>\n--EXPECT--\nhello \n", "FAIL"},
		{"skip", "--SKIPIF--\n<?php echo 'skip unavailable'; ?>\n--FILE--\n<?php exit(99); ?>\n--EXPECT--\n", "SKIP"},
		{"expectf", "--FILE--\n<?php echo \"value=42\\n\"; ?>\n--EXPECTF--\nvalue=%d\n", "PASS"},
		{"regex", "--FILE--\n<?php echo \"abc12\\n\"; ?>\n--EXPECTREGEX--\n[a-z]+[0-9]+\n", "PASS"},
		{"stderr", "--FILE--\n<?php fwrite(STDERR, 'error'); ?>\n--EXPECT--\n", "FAIL"},
		{"exit", "--FILE--\n<?php exit(7); ?>\n--EXPECT--\n", "FAIL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFixture(t, t.TempDir(), "case.phpt", "--TEST--\n"+tc.name+"\n"+tc.body)
			result, err := Run(t.Context(), path, Options{Target: "/usr/bin/php"})
			if err != nil || result.Verdict != tc.want {
				t.Fatalf("Run = %+v, %v", result, err)
			}
		})
	}
}

func TestOfficialPHPTDetectsTrailingNewlineMismatch(t *testing.T) {
	// php-src/php-8.3.6/tests/lang/001.phpt is retained without rewriting EXPECT.
	path := filepath.Join("testdata", "php-src-8.3.6", "001.phpt")
	result, err := Run(t.Context(), path, Options{Target: "/usr/bin/php"})
	if err != nil || result.Verdict != "FAIL" || string(result.Target.Stdout) != "Yes" {
		t.Fatalf("strict official fixture: %+v, %v", result, err)
	}
}

func TestRunRejectsWorkspaceInsideSource(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "case.phpt", "--TEST--\nrecursive workspace\n--FILE--\n<?php ?>\n--EXPECT--\n")
	t.Setenv("TMPDIR", dir)
	if _, err := Run(t.Context(), path, Options{Target: "/usr/bin/php"}); err == nil || !strings.Contains(err.Error(), "outside the fixture tree") {
		t.Fatalf("unsafe workspace error=%v", err)
	}
}

func TestDifferentialComparesAllObservations(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "case.phpt", "--TEST--\ndifferential\n--FILE--\n<?php echo \"ok\\n\"; ?>\n--EXPECT--\nok\n")
	for _, tc := range []struct{ name, script, want string }{
		{"same", "exec /usr/bin/php \"$@\"", "PASS"},
		{"stdout", "/usr/bin/php \"$@\"; printf ' '", "FAIL"},
		{"stderr", "/usr/bin/php \"$@\"; printf error >&2", "FAIL"},
		{"exit", "/usr/bin/php \"$@\"; exit 7", "FAIL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := writeFixture(t, t.TempDir(), "target", "#!/bin/sh\n"+tc.script+"\n")
			result, err := Run(t.Context(), path, Options{Oracle: "/usr/bin/php", Target: target})
			if err != nil || result.Verdict != tc.want || result.Oracle == nil {
				t.Fatalf("Run = %+v, %v", result, err)
			}
			if tc.name != "same" && !strings.HasPrefix(result.Reason, "oracle divergence: "+tc.name) {
				t.Fatalf("separate observation not compared: %+v", result)
			}
		})
	}
}

func TestDifferentialRejectsUnexpectedStderrAndSupportsFatalErrors(t *testing.T) {
	for _, kind := range []string{"EXPECT", "EXPECTF", "EXPECTREGEX"} {
		for _, tc := range []struct {
			code, stderr, expect, want string
			exit                       int
		}{
			{"echo 'ok'; fwrite(STDERR, 'err'); exit(7);", "err", "ok", "FAIL", 7},
			{"echo 'ok'; fwrite(STDERR, 'err');", "err", "ok", "FAIL", 0},
			{"exit(7);", "", "", "FAIL", 7},
			{"echo 'fatal'; exit(255);", "", "fatal", "PASS", 255},
			{"fwrite(STDERR, 'fatal err'); exit(255);", "fatal err", "fatal err", "PASS", 255},
		} {
			for _, oracle := range []string{"", "/usr/bin/php"} {
				path := writeFixture(t, t.TempDir(), "case.phpt", "--TEST--\nexit\n--FILE--\n<?php "+tc.code+" ?>\n--"+kind+"--\n"+tc.expect)
				result, err := Run(t.Context(), path, Options{Oracle: oracle, Target: "/usr/bin/php"})
				if err != nil || result.Verdict != tc.want || result.Target.ExitCode != tc.exit || string(result.Target.Stderr) != tc.stderr {
					t.Fatalf("oracle=%q %s %s: Run = %+v, %v", oracle, kind, tc.code, result, err)
				}
				if oracle != "" && (result.Oracle == nil || !bytes.Equal(result.Oracle.Stdout, result.Target.Stdout) || !bytes.Equal(result.Oracle.Stderr, result.Target.Stderr) || result.Oracle.ExitCode != result.Target.ExitCode) {
					t.Fatalf("oracle=%q %s %s: oracle mismatch = %+v", oracle, kind, tc.code, result)
				}
			}
		}
	}
}

func TestRunValidatesPatternBeforeSkip(t *testing.T) {
	for _, oracle := range []string{"", "/usr/bin/php"} {
		path := writeFixture(t, t.TempDir(), "case.phpt", "--TEST--\ninvalid skipped pattern\n--SKIPIF--\n<?php echo 'skip'; ?>\n--FILE--\n<?php ?>\n--EXPECTREGEX--\n[")
		result, err := Run(t.Context(), path, Options{Oracle: oracle, Target: "/usr/bin/php"})
		if err == nil || result.Verdict == "SKIP" || !strings.Contains(err.Error(), "PCRE") {
			t.Fatalf("oracle=%q: Run = %+v, %v; want PCRE error before SKIP", oracle, result, err)
		}
	}
}

func TestExecuteParentDeathKillsPHP(t *testing.T) {
	if dir := os.Getenv("PHPT_PARENT_DEATH_DIR"); dir != "" {
		_, err := execute(t.Context(), "/usr/bin/php", filepath.Join(dir, "program.php"), dir, 30*time.Second)
		t.Fatalf("helper unexpectedly returned: %v", err)
	}
	// The kernel signal and subprocess I/O require real time, not synctest.
	dir := t.TempDir()
	writeFixture(t, dir, "program.php", "<?php file_put_contents('pid', getmypid()); while (true) { usleep(10000); }")
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestExecuteParentDeathKillsPHP$")
	command.Env = append(os.Environ(), "PHPT_PARENT_DEATH_DIR="+dir)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		if command.ProcessState == nil {
			_ = command.Wait()
		}
	})
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(dir, "pid"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		pid, _ = strconv.Atoi(string(data))
		if pid > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("PHP did not publish its PID")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("parent unexpectedly exited successfully")
	}
	assertProcessTerminated(t, pid)
}

func TestDifferentialCannotHideBadOracle(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "case.phpt", "--TEST--\nbad oracle\n--FILE--\n<?php echo 'wrong'; ?>\n--EXPECT--\nright")
	result, err := Run(t.Context(), path, Options{Oracle: "/usr/bin/php", Target: "/usr/bin/php"})
	if err != nil || result.Verdict != "FAIL" {
		t.Fatalf("Run = %+v, %v", result, err)
	}
}

func TestRunExternalAndFreshIdenticalWorkspace(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "program.php", "<?php require __DIR__.'/helper.inc'; echo __FILE__, '\\n', getcwd(); file_put_contents('state', 'changed'); ?>")
	writeFixture(t, dir, "helper.inc", "<?php if (file_exists('state')) { echo 'dirty'; } ?>")
	path := writeFixture(t, dir, "case.phpt", "--TEST--\nexternal\n--FILE_EXTERNAL--\nprogram.php\n--EXPECTF--\n%s")
	result, err := Run(t.Context(), path, Options{Oracle: "/usr/bin/php", Target: "/usr/bin/php"})
	if err != nil || result.Verdict != "PASS" || strings.Contains(string(result.Target.Stdout), "dirty") {
		t.Fatalf("Run = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Fatalf("source modified: %v", err)
	}
	workspace := strings.Split(string(result.Target.Stdout), `\n`)[1]
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace not removed: %v", err)
	}
}

func TestRunRejectsInvalidSkipAndInfrastructureFailures(t *testing.T) {
	for _, skip := range []string{"echo 'not skip';", "echo 'skip'; exit(1);", "fwrite(STDERR, 'bad');"} {
		path := writeFixture(t, t.TempDir(), "case.phpt", "--TEST--\nskip\n--SKIPIF--\n<?php "+skip+" ?>\n--FILE--\n<?php ?>\n--EXPECT--\n")
		if _, err := Run(t.Context(), path, Options{Target: "/usr/bin/php"}); err == nil {
			t.Errorf("invalid SKIPIF accepted: %q", skip)
		}
	}
	path := writeFixture(t, t.TempDir(), "case.phpt", "--TEST--\nerrors\n--FILE--\n<?php ?>\n--EXPECT--\n")
	if _, err := Run(t.Context(), path, Options{Target: "/no/such/php"}); err == nil {
		t.Fatal("missing binary accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Run(ctx, path, Options{Target: "/usr/bin/php"}); err == nil {
		t.Fatal("cancelled execution accepted")
	}
}

func TestDifferentialSkipDisagreementFails(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "case.phpt", "--TEST--\nskip\n--SKIPIF--\n<?php ?>\n--FILE--\n<?php ?>\n--EXPECT--\n")
	target := writeFixture(t, t.TempDir(), "target", "#!/bin/sh\nprintf 'skip target only'\n")
	result, err := Run(t.Context(), path, Options{Target: target, Oracle: "/usr/bin/php"})
	if err != nil || result.Verdict != "FAIL" {
		t.Fatalf("Run = %+v, %v", result, err)
	}
}

func TestRunRejectsEscapingExternalFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../outside.php", "/etc/passwd", "missing.php"} {
		path := writeFixture(t, dir, "case.phpt", "--TEST--\nexternal\n--FILE_EXTERNAL--\n"+name+"\n--EXPECT--\n")
		if _, err := Run(t.Context(), path, Options{Target: "/usr/bin/php"}); err == nil {
			t.Errorf("accepted external path %q", name)
		}
	}
}

func TestRunRejectsSymlinkFixtures(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "case.phpt", "--TEST--\nlink\n--FILE--\n<?php ?>\n--EXPECT--\n")
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), path, Options{Target: "/usr/bin/php"}); err == nil {
		t.Fatal("symlink fixture accepted")
	}
}

func TestExecuteReportsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		time.Sleep(time.Second)
		synctest.Wait()
		dir := t.TempDir()
		program := writeFixture(t, dir, "program.php", "<?php ?>")
		_, err := execute(ctx, "/usr/bin/php", program, dir, time.Second)
		if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
			t.Fatalf("execute error=%v, want deadline exceeded", err)
		}
	})
}

func TestExecuteReportsSignalAndOutputLimit(t *testing.T) {
	dir := t.TempDir()
	program := writeFixture(t, dir, "program.php", "<?php echo str_repeat('x', 9 * 1024 * 1024); ?>")
	_, err := execute(t.Context(), "/usr/bin/php", program, dir, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("execute error=%v, want output limit", err)
	}
	binary := writeFixture(t, dir, "target", "#!/bin/sh\nkill -KILL $$\n")
	if _, err := execute(t.Context(), binary, "unused", dir, time.Second); err == nil {
		t.Fatal("signal reported as a normal exit")
	}
}

func TestMatchRejectsAnchorInjection(t *testing.T) {
	for _, tc := range []struct{ kind, pattern string }{
		{"EXPECTREGEX", "attendu)|(?s:.*"},
		{"EXPECTF", "ok %rx))|((?s:.*%r"},
		{"EXPECTF", "%r)%r%r(%r"},
	} {
		matched, err := Match(tc.kind, []byte(tc.pattern), []byte("SORTIE TOTALEMENT FAUSSE"))
		if err == nil || matched {
			t.Errorf("%s %q: matched=%v error=%v; want explicit syntax error", tc.kind, tc.pattern, matched, err)
		}
	}
}

func TestMatchASCIICaseFolding(t *testing.T) {
	for _, tc := range []struct {
		pattern, actual string
		want            bool
	}{
		{`(?i)\xE9`, "\xc9", false},
		{`(?i)\xE9`, "\xe9", true},
		{"(?i)\xe9", "\xc9", false},
		{`(?i)[\xC0-\xDE]`, "\xe9", false},
		{`(?i)[^\xE9]`, "\xc9", true},
		{`(?i)[^\xE9]`, "\xe9", false},
		{`(?i)[a-z]+`, "AbZ", true},
		{`(?i)[^a]`, "A", false},
		{`(?i:a)(?-i:b)`, "AB", false},
		{`(?i)[\351]`, "\xc9", false},
		{`\0400`, " 0", true},
		{`\0377`, "\x1f7", true},
		{`[\377]`, "\xff", true},
		{`(?i)\x{e9}`, "\xc9", false},
		{`(?i)\p{L}`, "\xe9", true},
		{`(?i)[\x7f-\xff]`, "k", false},
		{`(?i)[\x7f-\xff]`, "s", false},
		{`(?i)[[:alpha:]]`, "Z", true},
		{`(?i)\Q` + "\xe9" + `\E`, "\xc9", false},
		{`[^^]`, "^", false},
		{`[^^]`, "a", true},
		{`(?i)[^^a]`, "A", false},
		{`(?i)[^^a]`, "b", true},
		{`(?i:[a\p{Lu}])(?-i:[b\p{Lu}])`, "Ab", true},
		{`(?i:[a\p{Lu}])(?-i:[b\p{Lu}])`, "ab", true},
		{`(?i:[a\p{Lu}])(?-i:[b\p{Lu}])`, "cb", false},
		{`(?i:(?-i:[a\p{Lu}]))[b\p{Lu}]`, "aB", true},
		{`(?i:(?-i:[a\p{Lu}]))[b\p{Lu}]`, "ab", true},
		{`(?i:(?-i:[a\p{Lu}]))[b\p{Lu}]`, "ac", false},
		{`(?i)[^^\p{Lu}]`, "^", false},
		{`(?i)[^^\p{Lu}]`, "a", true},
		{`(?i)[\\p{Lu}]`, "P", true},
		{`(?i:[B\p{Lu}])[C\p{Lu}]`, "bc", false},
		{`(?i:[B\p{Lu}])[C\p{Lu}]`, "bC", true},
		{`(?i)(?-i:[B\p{Lu}])[C\p{Lu}]`, "Bc", true},
		{`(?i)(?-i:[B\p{Lu}])[C\p{Lu}]`, "bc", false},
	} {
		for _, kind := range []string{"EXPECTREGEX", "EXPECTF"} {
			pattern := tc.pattern
			if kind == "EXPECTF" {
				pattern = "%r" + pattern + "%r"
			}
			got, err := Match(kind, []byte(pattern), []byte(tc.actual))
			if err != nil || got != tc.want {
				t.Errorf("%s %q / %q: %v, %v; want %v", kind, pattern, tc.actual, got, err, tc.want)
			}
		}
	}
}

func TestMatchByteClassesAgainstPHP(t *testing.T) {
	patterns := []string{
		`\s`, `\S`, `[\s]`, `[^\s]`, `\d`, `\D`, `\w`, `\W`,
		`[\S]`, `[^\S]`, `[\d]`, `[^\d]`, `[\D]`, `[\w]`, `[^\w]`, `[\W]`,
		`[a\s]`, `[^a\s]`, `(?i)\p{Lu}`, `(?i)\P{Lu}`, `%w`,
		`(?i)[\p{Lu}]`, `(?i)[^\p{Lu}]`, `(?i)[a\p{Lu}]`,
		`(?i)[^a\p{Lu}]`, `(?i)[\P{Lu}]`, `(?i:[\p{Lu}])`,
		`\v`, `[\v]`, `[^\v]`, `[^k\v]`, `(?i)[^\xe9\v]`, `[^\S\v]`,
	}
	for name := range strings.FieldsSeq("alnum alpha ascii blank cntrl digit graph lower print punct space upper word xdigit") {
		for _, form := range []string{"[[:%s:]]", "[[:^%s:]]", "[^[:%s:]]", "(?i)[[:%s:]]", "(?i)[^[:%s:]]", "(?i)[[:^%s:]]"} {
			patterns = append(patterns, fmt.Sprintf(form, name))
		}
	}
	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			oraclePattern := pattern
			kind := "EXPECTREGEX"
			if pattern == "%w" {
				kind, oraclePattern = "EXPECTF", `\s*`
			}
			command := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", `
$pattern = '~\A(?:' . $argv[1] . ')\z~s';
for ($b = 0; $b < 256; $b++) {
    $matched = preg_match($pattern, chr($b));
    if ($matched === false) { exit(2); }
    echo $matched;
}`, oraclePattern)
			oracle, err := command.CombinedOutput()
			if err != nil || len(oracle) != 256 {
				t.Fatalf("PHP oracle: %v, output=%q", err, oracle)
			}
			for b := range 256 {
				got, err := Match(kind, []byte(pattern), []byte{byte(b)})
				if err != nil || got != (oracle[b] == '1') {
					t.Errorf("byte 0x%02x: Match=%v error=%v PHP=%c", b, got, err, oracle[b])
				}
			}
		})
	}
}

func TestMatchRawFormatWildcards(t *testing.T) {
	for _, tc := range []struct {
		pattern, actual string
		want            bool
	}{
		{"%rfoo%d%r", "foo12", true},
		{"%rfoo%d%r", "foo%d", false},
		{"%r%%%r", "%%", true},
		{"%r%%d%r", "%12", true},
		{"%r%%d%r", "%%d", false},
		{"%%", "%%", true},
		{"%%d", "%12", true},
		{"%r%s%r", "word", true},
		{"%r%S%r", "", true},
		{"%r%a%r", "a\nb", true},
		{"%r%A%r", "", true},
		{"%r%w%r", "\t\n\v\f\r ", true},
		{"%r%w%r", "\x85", false},
		{"%r%i%r", "-12", true},
		{"%r%x%r", "aF", true},
		{"%r%f%r", "-.25e+2", true},
		{"%r%c%r", "\xff", true},
		{"%r%e%r", string(filepath.Separator), true},
		{"%r%0%r", "\x00", true},
	} {
		got, err := Match("EXPECTF", []byte(tc.pattern), []byte(tc.actual))
		if err != nil || got != tc.want {
			t.Errorf("%q / %q: got %v, %v; want %v", tc.pattern, tc.actual, got, err, tc.want)
		}
	}
}

// expectf_to_regex is retained from php-src php-8.3.6/run-tests.php:2876-2924.
const expectfOracle = `
function expectf_to_regex(?string $wanted): string
{
    $wanted_re = $wanted ?? '';
    $wanted_re = preg_replace('/\r\n/', "\n", $wanted_re);
    $temp = "";
    $r = "%r";
    $startOffset = 0;
    $length = strlen($wanted_re);
    while ($startOffset < $length) {
        $start = strpos($wanted_re, $r, $startOffset);
        if ($start !== false) {
            $end = strpos($wanted_re, $r, $start + 2);
            if ($end === false) {
                $end = $start = $length;
            }
        } else {
            $start = $end = $length;
        }
        $temp .= preg_quote(substr($wanted_re, $startOffset, $start - $startOffset), '/');
        if ($end > $start) {
            $temp .= '(' . substr($wanted_re, $start + 2, $end - $start - 2) . ')';
        }
        $startOffset = $end + 2;
    }
    $wanted_re = $temp;
    return strtr($wanted_re, [
        '%e' => preg_quote(DIRECTORY_SEPARATOR, '/'),
        '%s' => '[^\r\n]+', '%S' => '[^\r\n]*',
        '%a' => '.+', '%A' => '.*', '%w' => '\s*',
        '%i' => '[+-]?\d+', '%d' => '\d+', '%x' => '[0-9a-fA-F]+',
        '%f' => '[+-]?(?:\d+|(?=\.\d))(?:\.\d+)?(?:[Ee][+-]?\d+)?',
        '%c' => '.', '%0' => '\x00',
    ]);
}
if (PHP_VERSION !== '8.3.6') { fwrite(STDERR, PHP_VERSION); exit(2); }
while (($line = fgets(STDIN)) !== false) {
    [$kind, $p, $a] = explode("\t", rtrim($line, "\n"));
    $p = base64_decode($p); $a = base64_decode($a);
    $re = $kind === 'EXPECTF' ? expectf_to_regex($p) : $p;
    $r = @preg_match('/^' . $re . '$/s', $a);
    $strict = @preg_match('/\A(?:' . $re . ')\z/s', $a);
    echo $r === false ? 'E' : (string)$r, ':', $strict === false ? 'E' : (string)$strict, "\n";
}`

func TestMatchReprise4AgainstPHP(t *testing.T) {
	testReprise4AgainstPHP(t, false)
}

func TestR3(t *testing.T) {
	testReprise4AgainstPHP(t, true)
}

func testReprise4AgainstPHP(t *testing.T, auditOnly bool) {
	t.Helper()
	// These are the 45 original audit cases, including empty output fields.
	const auditCases = `EXPECTF JXJbJWRdJXI= W10=
EXPECTF JXJbJWRdJXI= NQ==
EXPECTF JXJbJWRdJXI= Kw==
EXPECTF JXJbXiVkXSVy YV0=
EXPECTF JXJbXiVkXSVy YQ==
EXPECTF JXJbJWFdJXI= cw==
EXPECTF JXJbJWFdJXI= Lg==
EXPECTF JXJbJWNdJXI= cw==
EXPECTF JXJbJWNdJXI= KA==
EXPECTF JXJbJUFdJXI= Pw==
EXPECTF JXJbJXddJXI= XQ==
EXPECTF JXJbJXddJXI= Kg==
EXPECTF JXJcJWQlcg== WzAtOV0=
EXPECTF JXJcJWQlcg== XGRkZA==
EXPECTF JXJcJXclcg== WwktDSBd
EXPECTF JXIlYT8lcg== 
EXPECTF JXIlYSolcg== 
EXPECTF JXIlYXsyfSVy YWI=
EXPECTF JXIlYXsyfSVy YQ==
EXPECTF JXIoPy1zKSVhJXI= YQpi
EXPECTF JXIoPy1zKSVjJXI= Cg==
EXPECTF JXIoPy1zKSVBJXI= Cg==
EXPECTF JXIoPy1zOnglYSklcg== eAp5
EXPECTREGEX W15ceHsxMDB9XQ== YQ==
EXPECTREGEX YXxceHsxMDB9 YQ==
EXPECTREGEX KD86XHh7MWZmfSk/ 
EXPECTREGEX W15cNDAwXQ== YQ==
EXPECTREGEX YXxcNzc3 YQ==
EXPECTREGEX KD9pKVtbOl0= Ww==
EXPECTREGEX KD9pKVtbOl0= Og==
EXPECTREGEX W1s6XQ== Ww==
EXPECTREGEX KD9pKVthWzpd QQ==
EXPECTREGEX KHgpKHgpKHgpKHgpKHgpKHgpKHgpKHgpKHgpKHgpXDEw eHh4eHh4eHh4eAg=
EXPECTREGEX KHgpKHgpKHgpKHgpKHgpKHgpKHgpKHgpKHgpKHgpXDEw eHh4eHh4eHh4eHg=
EXPECTREGEX KD9pKVtccHtMdX1bOl51cHBlcjpdXQ== YQ==
EXPECTREGEX KD9pKVtccHtMdX1bOl5sb3dlcjpdXQ== Yg==
EXPECTREGEX KD9pKVteXHB7TGx9WzpedXBwZXI6XV0= YQ==
EXPECTREGEX KD9pKVtbOl51cHBlcjpdWzpebG93ZXI6XV0= YQ==
EXPECTREGEX KD9pKVteWzpedXBwZXI6XV0= YQ==
EXPECTREGEX XHB7QW55fQ== /w==
EXPECTREGEX W15ccHtBbnl9XQ== /w==
EXPECTREGEX KD9pKVtbOl51cHBlcjpdYV0= QQ==
EXPECTREGEX KD9pOltbOl5sb3dlcjpdXSk= QQ==
EXPECTREGEX KD9pKSg/LWk6W1s6XnVwcGVyOl1dKQ== YQ==
EXPECTREGEX KD9pKSg/LWk6W1s6XnVwcGVyOl1dKQ== QQ==`
	type matchCase struct {
		kind, pattern, actual string
	}
	cases := make([]matchCase, 0, 1060)
	for line := range strings.SplitSeq(auditCases, "\n") {
		fields := strings.Split(line, " ")
		pattern, err := base64.StdEncoding.DecodeString(fields[1])
		if err != nil {
			t.Fatal(err)
		}
		actual, err := base64.StdEncoding.DecodeString(fields[2])
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, matchCase{fields[0], string(pattern), string(actual)})
	}
	if len(cases) != 45 {
		t.Fatalf("audit cases=%d", len(cases))
	}
	for joker := range strings.FieldsSeq("%e %s %S %a %A %w %i %d %x %c %0 %f") {
		for _, form := range []string{"[%s]", `\%s`, "%s?", "%s*", "%s+", "%s{2}", "(?-s)%s"} {
			for _, actual := range []string{"", "a", "a\nb", ".", "+", "*", "5", "[]", `\ddd`, "1.5", "-.25e+2", "\x00"} {
				block := fmt.Sprintf(form, joker)
				cases = append(cases, matchCase{"EXPECTF", "%r" + block + "%r", actual})
			}
		}
	}
	for _, pattern := range []string{"%r(?-s)%a%r%a", "%r(?-s)%c%r%r%c%r", "%r(?-s:%c)%c%r"} {
		cases = append(cases, matchCase{"EXPECTF", pattern, "a\nb"})
	}
	for _, pattern := range []string{`%r[]%f]%r`, `%r[^]%f]%r`, `%r[[:digit:]%f]%r`, `%r\Q%f\E%r`} {
		cases = append(cases, matchCase{"EXPECTF", pattern, "5"})
	}
	if len(cases) != 1060 {
		t.Fatalf("total cases=%d; want 1060", len(cases))
	}
	if auditOnly {
		cases = cases[:45]
	}
	var input strings.Builder
	for _, tc := range cases {
		fmt.Fprintf(&input, "%s\t%s\t%s\n", tc.kind, base64.StdEncoding.EncodeToString([]byte(tc.pattern)), base64.StdEncoding.EncodeToString([]byte(tc.actual)))
	}
	command := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", expectfOracle)
	command.Stdin = strings.NewReader(input.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PHP oracle: %v: %s", err, output)
	}
	results := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(results) != len(cases) {
		t.Fatalf("PHP results=%d cases=%d", len(results), len(cases))
	}
	strictDifferences := 0
	for i, tc := range cases {
		official, strict, ok := strings.Cut(results[i], ":")
		if !ok {
			t.Fatalf("invalid oracle result %q", results[i])
		}
		if official != strict {
			strictDifferences++
		}
		t.Run(fmt.Sprintf("%d/%s", i+1, tc.pattern), func(t *testing.T) {
			got, err := Match(tc.kind, []byte(tc.pattern), []byte(tc.actual))
			if official == "E" || strict == "E" {
				if err == nil || got {
					t.Fatalf("%q: got=%v err=%v PHP=%s; want error", tc.actual, got, err, strict)
				}
			} else if err != nil || got != (strict == "1") {
				t.Fatalf("%q: got=%v err=%v PHP=%s", tc.actual, got, err, strict)
			}
		})
	}
	if strictDifferences != 1 {
		t.Fatalf("official $ versus strict \\z differences=%d; want 1", strictDifferences)
	}
	t.Logf("audit=45 generated=%d total=%d strict_anchor_differences=%d", len(cases)-45, len(cases), strictDifferences)
}

func TestMatchRejectsNonByteEscapes(t *testing.T) {
	for _, pattern := range []string{`(?i)\x{e0e9}`, `\x{100}`, `[^\x{100}]`, `a|\x{1ff}`, `[^\400]`, `a|\777`, `[\pL\x{100}]`, `[^\pL\400]`} {
		for _, kind := range []string{"EXPECTREGEX", "EXPECTF"} {
			expected := pattern
			if kind == "EXPECTF" {
				expected = "%r" + pattern + "%r"
			}
			if got, err := Match(kind, []byte(expected), []byte("a")); err == nil || got {
				t.Errorf("%s %q: got=%v error=%v; want unsupported escape error", kind, pattern, got, err)
			}
		}
	}
}

func FuzzMatch(f *testing.F) {
	for _, pattern := range []string{`(?i)[[:]`, `(?i)[a[:]`, `(?i)[[:unknown:]]`, `[^\x{100}]`, `%r[%f]%r`, `%r(?-s)%a%r`, `\0400`} {
		f.Add(pattern, "a")
	}
	f.Fuzz(func(t *testing.T, pattern, actual string) {
		for _, kind := range []string{"EXPECTF", "EXPECTREGEX"} {
			got, err := Match(kind, []byte(pattern), []byte(actual))
			if err != nil && got {
				t.Fatalf("error returned with PASS: %v", err)
			}
		}
	})
}

func TestMatchRejectsRecentUnicodeScripts(t *testing.T) {
	for name := range strings.FieldsSeq("Beria_Erfe Garay Gurung_Khema Kawi Kirat_Rai Nag_Mundari Ol_Onal Sidetic Sunuwar Tai_Yo Todhri Tolong_Siki Tulu_Tigalari") {
		for _, form := range []string{`\p{%s}`, `\P{%s}`, `[^\p{%s}]`, `(?i)[a\P{%s}]`} {
			pattern := fmt.Sprintf(form, name)
			command := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", `exit(@preg_match('~'.$argv[1].'~', 'x') === false ? 0 : 1);`, pattern)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("PHP accepted %q: %v %s", pattern, err, output)
			}
			for _, kind := range []string{"EXPECTREGEX", "EXPECTF"} {
				expected := pattern
				if kind == "EXPECTF" {
					expected = "%r" + pattern + "%r"
				}
				if got, err := Match(kind, []byte(expected), []byte("x")); err == nil || got {
					t.Errorf("%s %q: matched=%v error=%v; want unsupported property error", kind, expected, got, err)
				}
			}
		}
	}
}

func TestMatchWhitespaceVerticalTab(t *testing.T) {
	matched, err := Match("EXPECTF", []byte("%w"), []byte{'\v'})
	if err != nil || !matched {
		t.Fatalf("%%w must accept vertical tab: matched=%v error=%v", matched, err)
	}
}

func TestMatchCompensatedRawBlocks(t *testing.T) {
	matched, err := Match("EXPECTF", []byte("%r(%r%r)%r"), nil)
	if err != nil || !matched {
		t.Fatalf("balanced complete pattern: matched=%v error=%v", matched, err)
	}
}

func TestRunKeepsPrivateWorkspace(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "private.phpt", "--TEST--\nprivate\n--FILE--\n<?php printf('%o', fileperms(getcwd()) & 0777); chmod(getcwd(), 0777); ?>\n--EXPECT--\n700")
	result, err := Run(t.Context(), path, Options{Target: "/usr/bin/php", Oracle: "/usr/bin/php"})
	if err != nil || result.Verdict != "PASS" {
		t.Fatalf("Run=%+v error=%v", result, err)
	}
}

func TestRunCopiesOnlyLocalFixtures(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "unrelated")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, child, "payload", "not a dependency")
	writeFixture(t, dir, "other.phpt", "not a dependency")
	writeFixture(t, dir, "helper.inc", "<?php echo 'ok';")
	path := writeFixture(t, dir, "local.phpt", "--TEST--\nlocal\n--FILE--\n<?php require 'helper.inc'; if (file_exists('unrelated') || file_exists('other.phpt')) echo 'unrelated copied'; ?>\n--EXPECT--\nok")
	result, err := Run(t.Context(), path, Options{Target: "/usr/bin/php"})
	if err != nil || result.Verdict != "PASS" {
		t.Fatalf("Run=%+v error=%v", result, err)
	}
}

func TestRunCopiesNestedExternalFixtures(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "scripts")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, nested, "program.php", "<?php require __DIR__.'/helper.inc';")
	writeFixture(t, nested, "helper.inc", "<?php echo 'ok';")
	path := writeFixture(t, dir, "external.phpt", "--TEST--\nexternal\n--FILE_EXTERNAL--\nscripts/program.php\n--EXPECT--\nok")
	result, err := Run(t.Context(), path, Options{Target: "/usr/bin/php", Oracle: "/usr/bin/php"})
	if err != nil || result.Verdict != "PASS" {
		t.Fatalf("Run=%+v error=%v", result, err)
	}
	if err := os.Remove(filepath.Join(nested, "program.php")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(nested, "program.php")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), path, Options{Target: "/usr/bin/php"}); err == nil {
		t.Fatal("nested symlink accepted")
	}
}

func TestExecuteActiveDeadlineKillsGroup(t *testing.T) {
	// Real subprocess I/O cannot run on synctest's virtual clock.
	dir := t.TempDir()
	program := writeFixture(t, dir, "active.php", `<?php
$child = proc_open(['/usr/bin/php', '-n', '-r', 'echo getmypid(), "\\n"; fflush(STDOUT); sleep(30);'], [0 => STDIN, 1 => STDOUT, 2 => STDERR], $pipes);
echo getmypid(), "\n"; fflush(STDOUT); sleep(30);
`)
	start := time.Now()
	output, err := execute(t.Context(), "/usr/bin/php", program, dir, 500*time.Millisecond)
	elapsed := time.Since(start)
	var pids []int
	for line := range strings.FieldsSeq(string(output.Stdout)) {
		pid, parseErr := strconv.Atoi(line)
		if parseErr != nil {
			t.Fatalf("pid output=%q: %v", output.Stdout, parseErr)
		}
		pids = append(pids, pid)
	}
	t.Cleanup(func() {
		for _, pid := range pids {
			if killErr := syscall.Kill(pid, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
				t.Errorf("cleanup pid %d: %v", pid, killErr)
			}
		}
	})
	if !errors.Is(err, context.DeadlineExceeded) || elapsed > 3*time.Second || len(pids) != 2 {
		t.Fatalf("error=%v elapsed=%v stdout=%q; want two active processes and bounded deadline", err, elapsed, output.Stdout)
	}
	for _, pid := range pids {
		assertProcessTerminated(t, pid)
	}
}

func TestExecuteBoundsInheritedPipes(t *testing.T) {
	// A detached descendant deliberately escapes group killing but holds stdout.
	dir := t.TempDir()
	program := writeFixture(t, dir, "pipes.php", `<?php
$child = proc_open(['/usr/bin/setsid', '/usr/bin/php', '-n', '-r', 'echo getmypid(), "\\n"; fflush(STDOUT); file_put_contents("ready", "1"); sleep(4);'], [0 => STDIN, 1 => STDOUT, 2 => STDERR], $pipes);
while (!file_exists('ready')) { usleep(1000); }
exit(0);
`)
	start := time.Now()
	output, err := execute(t.Context(), "/usr/bin/php", program, dir, 8*time.Second)
	elapsed := time.Since(start)
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(output.Stdout)))
	if parseErr != nil {
		t.Fatalf("child output=%q: %v", output.Stdout, parseErr)
	}
	if killErr := syscall.Kill(pid, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
		t.Fatal(killErr)
	}
	if !errors.Is(err, exec.ErrWaitDelay) || elapsed > 3*time.Second {
		t.Fatalf("error=%v elapsed=%v; want bounded inherited pipes", err, elapsed)
	}
}

func TestExecuteNormalExitKillsGroup(t *testing.T) {
	// Real process completion and /proc observations require the host clock.
	dir := t.TempDir()
	program := writeFixture(t, dir, "normal.php", `<?php
$child = proc_open(['/usr/bin/php', '-n', '-r', 'file_put_contents("ready", (string)getmypid()); sleep(30);'], [0 => ['file', '/dev/null', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']], $pipes);
if ($child === false) { exit(2); }
while (!file_exists('ready') || filesize('ready') === 0) { clearstatcache(); usleep(1000); }
echo file_get_contents('ready');
exit(0);
`)
	start := time.Now()
	output, err := execute(t.Context(), "/usr/bin/php", program, dir, 5*time.Second)
	elapsed := time.Since(start)
	pid, parseErr := strconv.Atoi(string(output.Stdout))
	if parseErr != nil || pid <= 0 {
		t.Fatalf("child pid=%q error=%v execution=%v", output.Stdout, parseErr, err)
	}
	t.Cleanup(func() {
		if killErr := syscall.Kill(pid, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
			t.Errorf("cleanup pid %d: %v", pid, killErr)
		}
	})
	if err != nil || output.ExitCode != 0 || len(output.Stderr) != 0 || elapsed > 3*time.Second {
		t.Fatalf("normal exit: output=%+v error=%v elapsed=%v", output, err, elapsed)
	}
	assertProcessTerminated(t, pid)
}

func assertProcessTerminated(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		// comm may itself contain parentheses; state follows the final one.
		end := strings.LastIndex(string(stat), ") ")
		if end < 0 || end+2 >= len(stat) {
			t.Fatalf("invalid process stat: %q", stat)
		}
		if stat[end+2] == 'Z' || stat[end+2] == 'X' {
			return
		}
		if !time.Now().Before(deadline) {
			t.Errorf("process %d still active after 200ms: %s", pid, stat)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMatchReprise5AgainstPHP(t *testing.T) {
	// cas_r4.txt (33 cases), then cas_r4b.txt (20 cases), unchanged.
	const auditCases = `EXPECTREGEX YS9i YS9i
EXPECTREGEX YVwvYg== YS9i
EXPECTREGEX Wy9d Lw==
EXPECTF JXJhL2Ilcg== YS9i
EXPECTF eCAlci8lciB5 eCAvIHk=
EXPECTF JXJbL10lcg== Lw==
EXPECTF YS9i YS9i
EXPECTF JWYuNQ== LjU=
EXPECTF JWYuNQ== Ky41
EXPECTF JXIlZlwuNSVy LjU=
EXPECTF JWYuNQ== MS41LjU=
EXPECTF JXIoJWYpJXI= MS41
EXPECTF JXIlZnx4JXI= eA==
EXPECTF JXIoP2kpJWYlcg== MUU1
EXPECTF JXIlZSVy Lw==
EXPECTF JXJbJWVdJXI= Lw==
EXPECTF JXIlZT8lcg== 
EXPECTF JXJcXCVmJXI= XDE=
EXPECTREGEX W1wxXQ== AQ==
EXPECTREGEX W1wxMl0= Cg==
EXPECTREGEX XDAxMg== Cg==
EXPECTREGEX W1wwNDAwXQ== IA==
EXPECTREGEX W1wwNDAwXQ== MA==
EXPECTREGEX XHh7ZmZ9 /w==
EXPECTREGEX W1x4ezBmZn1d /w==
EXPECTREGEX XHh7MDAwMDAxMDB9 YQ==
EXPECTREGEX KD9pKVtbOl1hOl1d YQ==
EXPECTREGEX KD9pKVtbOmFd YQ==
EXPECTREGEX KD9pKVtbOmFscGhhOl0tel0= LQ==
EXPECTREGEX KD9pKVtbOl1d XQ==
EXPECTREGEX KD9pKVtbOjpdXQ== Og==
EXPECTREGEX KD9pKVt4WzpedXBwZXI6XV0= QQ==
EXPECTREGEX KD9pKVs6YWxwaGE6XQ== YQ==
EXPECTREGEX W1s6YWxwaGE6XS16XQ== LQ==
EXPECTREGEX KD9pKVtbOmFscGhhOl0tel0= LQ==
EXPECTREGEX WzphbHBoYTpd YQ==
EXPECTREGEX KD9pKVs6YWxwaGE6XQ== YQ==
EXPECTREGEX WzpeYWxwaGE6XQ== YQ==
EXPECTREGEX eFs6ZGlnaXQ6XQ== eDE=
EXPECTREGEX W1xkLXpd LQ==
EXPECTREGEX W2EtXGRd LQ==
EXPECTREGEX W1x3LV0= LQ==
EXPECTREGEX W1s6YWxwaGE6XS1d LQ==
EXPECTREGEX W2EtWzpkaWdpdDpdXQ== LQ==
EXPECTREGEX W1xwTC16XQ== LQ==
EXPECTF JXJbJWQtel0lcg== LQ==
EXPECTF JXJbJXctel0lcg== LQ==
EXPECTF JXJbJXMtel0lcg== LQ==
EXPECTF JXJbJWktel0lcg== LQ==
EXPECTREGEX W1suYS5dXQ== YQ==
EXPECTREGEX W1s9YT1dXQ== YQ==
EXPECTREGEX Wy5hLl0= YQ==
EXPECTREGEX Wz1hPV0= YQ==`
	type matchCase struct{ kind, pattern, actual string }
	cases := make([]matchCase, 0, 512)
	for line := range strings.SplitSeq(auditCases, "\n") {
		fields := strings.Split(line, " ")
		pattern, err := base64.StdEncoding.DecodeString(fields[1])
		if err != nil {
			t.Fatal(err)
		}
		actual, err := base64.StdEncoding.DecodeString(fields[2])
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, matchCase{fields[0], string(pattern), string(actual)})
	}
	if len(cases) != 53 {
		t.Fatalf("audit cases=%d", len(cases))
	}
	patterns := []string{`a/b`, `[/]`, `\/`, `[\Q/\E]`, `\Q/\E`, `\\/`, `\\\/`, `[\/]`, `\Q[:alpha:]\E`, `[:alpha:]`, `[:^alpha:]`, `[:foo:]`, `[::]`, `[:]`, `[.a.]`, `[=a=]`, `[[.a.]]`, `[[=a=]]`, `[.]`, `[=]`, `\[.a.\]`, `[[:alpha:]]`, `(?i)[[:alpha:]-z]`}
	for _, atom := range []string{`\d`, `\D`, `\s`, `\S`, `\w`, `\W`, `\v`, `\pL`, `\PL`, `\p{Lu}`, `\P{Lu}`, `[:alpha:]`, `[:^digit:]`} {
		for _, form := range []string{`[%s-z]`, `[a-%s]`, `[-%s]`, `[^-%s]`, `[%s-]`, `[%s\-z]`, `[a\-%s]`} {
			patterns = append(patterns, fmt.Sprintf(form, atom))
		}
	}
	for _, pattern := range patterns {
		for _, kind := range []string{"EXPECTREGEX", "EXPECTF"} {
			p := pattern
			if kind == "EXPECTF" {
				p = "%r" + p + "%r"
			}
			for _, actual := range []string{"-", "/", "a", `\/`} {
				cases = append(cases, matchCase{kind, p, actual})
			}
		}
	}
	var input strings.Builder
	for _, tc := range cases {
		fmt.Fprintf(&input, "%s\t%s\t%s\n", tc.kind, base64.StdEncoding.EncodeToString([]byte(tc.pattern)), base64.StdEncoding.EncodeToString([]byte(tc.actual)))
	}
	command := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", expectfOracle)
	command.Stdin = strings.NewReader(input.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PHP: %v: %s", err, output)
	}
	results := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(results) != len(cases) {
		t.Fatalf("results=%d cases=%d", len(results), len(cases))
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%d/%s", i+1, tc.pattern), func(t *testing.T) {
			official, strict, ok := strings.Cut(results[i], ":")
			if !ok {
				t.Fatalf("invalid PHP result %q", results[i])
			}
			got, err := Match(tc.kind, []byte(tc.pattern), []byte(tc.actual))
			if official == "E" || strict == "E" {
				if err == nil || got {
					t.Fatalf("PHP rejects: got=%v err=%v", got, err)
				}
				return
			}
			if err != nil || got != (strict == "1") || got && official != "1" {
				t.Fatalf("%q: got=%v err=%v PHP=%s", tc.actual, got, err, results[i])
			}
		})
	}
	t.Logf("audit=53 total=%d", len(cases))
}

func TestAuditDifferentiel(t *testing.T) {
	// Preserve the original auditor's 9330 patterns and all byte/pair inputs.
	type probe struct {
		pattern string
		width   int
	}
	atoms := []string{`\s`, `\S`, `\d`, `\D`, `\w`, `\W`, `\v`, `\pL`, `\PL`, `\pN`, `\p{^Lu}`, `\P{^Lu}`, `\p{Any}`}
	var names []string
	for name := range unicode.Categories {
		names = append(names, name)
	}
	for name := range unicode.Scripts {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		atoms = append(atoms, `\p{`+name+`}`, `\P{`+name+`}`)
	}
	var probes []probe
	add := func(p string) { probes = append(probes, probe{p, 1}) }
	for _, atom := range atoms {
		for _, form := range []string{
			"%s", "(?i)%s", "[%s]", "[^%s]", "(?i)[%s]", "(?i)[^%s]",
			"[k%s]", "[^k%s]", "(?i)[k%s]", "(?i)[^k%s]",
			`(?i)[\xC9%s]`, `(?i)[^\xe9%s]`, `[\s%s]`, `[^\S%s]`, `(?i)[^\W%s]`,
			"(?i:%s)", "(?i)(?-i:%s)", "(?i:(?-i:[a%s]))", "(?-i:(?i:[^a%s]))",
			"x|%s", "(?i)z|[Z%s]",
		} {
			add(fmt.Sprintf(form, atom))
		}
	}
	for name := range strings.FieldsSeq("alnum alpha ascii blank cntrl digit graph lower print punct space upper word xdigit") {
		for _, form := range []string{"[[:%s:]]", "[[:^%s:]]", "[^[:%s:]]", "(?i)[[:%s:]]", "(?i)[^[:%s:]]", "(?i)[[:^%s:]]", `[\p{Lu}[:%s:]]`, `(?i)[^\p{Ll}[:%s:]]`} {
			add(fmt.Sprintf(form, name))
		}
	}
	for _, p := range []string{
		`(?i)[\x00-\xff]`, `(?i)[^\x00-\x7f]`, `(?i)\xff`, `(?i)[\xb5]`, `(?i)\x{b5}`, `\0`, `\012`, `\x0b`, `[\v]`, `[^\v]`,
		`(?i)K`, `(?i)S`, `(?i)[^k]`, `(?i)[^s]`, `(?i)k|\p{Lu}`, `(?i)(?:a|\p{Lu})`, `a(?i)b|\p{Lu}`, `a(?i)b|[\p{Lu}]`,
		`\Q\p{Lu}\E`, `(?i)\Q` + "\xc9" + `\E`, `[\Q]\E]`, `[\]\p{Lu}]`, `[[:alpha:]\p{Nd}]`, `[^]\p{Lu}]`, `[]\p{Lu}]`,
		`(?U)\w`, `(?m)\W`, `(?s).`, `.`, `\C`,
	} {
		add(p)
	}
	for _, p := range []string{`.\b.`, `.\B.`, `\w\b\W`, `(?m).$.`, `(?m).^.`, `.$`, `\s\S`, `(?i)\p{Lu}[a\P{Ll}]`, `(?i)(.)\p{Lu}`, `\v.`, `[\s\S]{2}`} {
		probes = append(probes, probe{p, 2})
	}
	if len(probes) != 9330 {
		t.Fatalf("patterns=%d; want 9330", len(probes))
	}
	var input strings.Builder
	for _, p := range probes {
		fmt.Fprintf(&input, "%d\t%s\n", p.width, base64.StdEncoding.EncodeToString([]byte(p.pattern)))
	}
	for _, delimiter := range []string{"~", "/"} {
		t.Run(delimiter, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", `
if (PHP_VERSION !== '8.3.6') { exit(2); }
while (($line = fgets(STDIN)) !== false) {
    [$width, $p] = explode("\t", rtrim($line, "\n"));
    $pattern = $argv[1] . '^' . base64_decode($p) . '$' . $argv[1] . 's';
    $strict = $argv[1] . '\A(?:' . base64_decode($p) . ')\z' . $argv[1] . 's';
    if (@preg_match($pattern, '') === false) { echo "E\n"; continue; }
    $count = $width === '1' ? 256 : 65536;
    for ($b = 0; $b < $count; $b++) {
        $actual = $width === '1' ? chr($b) : chr($b >> 8) . chr($b & 255);
        $r1 = preg_match($pattern, $actual);
        $r2 = preg_match($strict, $actual);
        if ($r1 === false || $r2 === false) { exit(3); }
        echo $r1, $r2;
    }
    echo "\n";
}`, delimiter)
			command.Stdin = strings.NewReader(input.String())
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("PHP: %v: %s", err, output)
			}
			lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
			if len(lines) != len(probes) {
				t.Fatalf("results=%d patterns=%d", len(lines), len(probes))
			}
			observations, falsePass, strict, runnerOnly, bothErrors := 0, 0, 0, 0, 0
			for i, p := range probes {
				_, err := Match("EXPECTREGEX", []byte(p.pattern), nil)
				if err != nil {
					if lines[i] == "E" {
						bothErrors++
					} else {
						runnerOnly++
					}
					continue
				}
				if lines[i] == "E" {
					t.Errorf("PHP rejects compiled pattern %q", p.pattern)
					continue
				}
				count := 256
				if p.width == 2 {
					count = 65536
				}
				if len(lines[i]) != 2*count {
					t.Fatalf("pattern %q: observations=%d", p.pattern, len(lines[i]))
				}
				patternRefusals := 0
				for b := range count {
					actual := []byte{byte(b)}
					if p.width == 2 {
						actual = []byte{byte(b >> 8), byte(b)}
					}
					got, err := Match("EXPECTREGEX", []byte(p.pattern), actual)
					if err != nil {
						t.Fatalf("%q input=%04x: %v", p.pattern, b, err)
					}
					want := lines[i][2*b] == '1'
					strictWant := lines[i][2*b+1] == '1'
					observations++
					if got && !want {
						falsePass++
						t.Errorf("false PASS %q input=%04x", p.pattern, b)
					}
					if !got && want {
						strict++
						patternRefusals++
					}
					if got != (want && strictWant) {
						t.Errorf("envelope intersection mismatch %q input=%04x", p.pattern, b)
					}
				}
				if patternRefusals != 0 {
					t.Logf("pattern=%q official matches rejected by strict envelope=%d", p.pattern, patternRefusals)
				}
			}
			t.Logf("motifs=%d observations=%d faux_pass=%d refus_stricts=%d erreur_runner_seul=%d erreurs_des_deux=%d", len(probes), observations, falsePass, strict, runnerOnly, bothErrors)
			if falsePass != 0 || runnerOnly != 0 || bothErrors != 546 {
				t.Fatal("audit baseline changed")
			}
		})
	}
}
