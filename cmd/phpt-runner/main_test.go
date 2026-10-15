package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIReportsVerdictsAndExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name, body, summary string
		args                []string
		code                int
	}{
		{"standalone", "--FILE--\n<?php echo 'ok'; ?>\n--EXPECT--\nok", "PASS=1 FAIL=0 SKIP=0 ERROR=0", nil, 0},
		{"differential", "--FILE--\n<?php echo 'ok'; ?>\n--EXPECT--\nok", "PASS=1 FAIL=0 SKIP=0 ERROR=0", []string{"--oracle", "/usr/bin/php"}, 0},
		{"fail", "--FILE--\n<?php echo 'ok'; ?>\n--EXPECT--\nok ", "PASS=0 FAIL=1 SKIP=0 ERROR=0", nil, 1},
		{"skip", "--SKIPIF--\n<?php echo 'skip test'; ?>\n--FILE--\n<?php ?>\n--EXPECT--\n", "PASS=0 FAIL=0 SKIP=1 ERROR=0", nil, 0},
		{"invalid", "--INI--\nx=1\n", "PASS=0 FAIL=0 SKIP=0 ERROR=1", nil, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "case.phpt")
			if err := os.WriteFile(path, []byte("--TEST--\n"+tc.name+"\n"+tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"--target", "/usr/bin/php"}, tc.args...)
			args = append(args, dir)
			var stdout, stderr bytes.Buffer
			code := runCLI(t.Context(), args, &stdout, &stderr)
			if code != tc.code || !strings.Contains(stdout.String(), tc.summary) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCLIHelpNamesPCRE2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(t.Context(), []string{"--help"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "PCRE2") || !strings.Contains(stderr.String(), "PHP oracle") || strings.Contains(stderr.String(), "RE2;") {
		t.Fatalf("code=%d help=%q", code, stderr.String())
	}
}

func TestCLIStopsOnCancellation(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.phpt", "b.phpt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("invalid"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr bytes.Buffer
	writer := &cancelWriter{Buffer: &stdout, cancel: cancel}
	code := runCLI(ctx, []string{dir}, writer, &stderr)
	if code != 2 || !strings.Contains(stdout.String(), "TOTAL=1 PASS=0 FAIL=0 SKIP=0 ERROR=1") || strings.Contains(stdout.String(), "b.phpt") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

type cancelWriter struct {
	*bytes.Buffer
	cancel context.CancelFunc
}

func (writer *cancelWriter) Write(data []byte) (int, error) {
	writer.cancel()
	return writer.Buffer.Write(data)
}

func TestCLIRejectsEmptySelectionAndInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		nil, {"--bad"}, {"--timeout", "-1s", t.TempDir()},
		{t.TempDir()}, {"/no/such/tests"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(t.Context(), args, &stdout, &stderr); code != 2 {
			t.Errorf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestCLIRecursesDeduplicatesAndContinuesAfterError(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "nested")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(dir, "a.phpt"):   "invalid",
		filepath.Join(child, "b.phpt"): "--TEST--\nb\n--FILE--\n<?php ?>\n--EXPECT--\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	code := runCLI(t.Context(), []string{"--target", "/usr/bin/php", dir, child}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stdout.String(), "TOTAL=2 PASS=1 FAIL=0 SKIP=0 ERROR=1") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
