package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/testing/phpt"
)

func TestCLI(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input.php")
	if err := os.WriteFile(file, []byte("<?php echo 3;"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"raw", []string{"-n", "-r", `echo "hi";`}, ""},
		{"file", []string{"-n", file}, ""},
		{"stdin", []string{"-n"}, "<?php echo 4;"},
		{"lint", []string{"-n", "-l", file}, ""},
		{"lint-invalid", []string{"-n", "-l"}, "<?php echo ;"},
		{"parse-invalid", []string{"-n", "-r", "echo ;"}, ""},
		{"exit", []string{"-n", "-r", "exit(259);"}, ""},
		{"ini", []string{"-n", "-d", "display_errors=1", "-d", "error_reporting=-1", "-r", "echo 5;"}, ""},
		{"missing", []string{"-n", file + "missing"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotOut, gotErr, wantOut, wantErr bytes.Buffer
			cmd := exec.CommandContext(t.Context(), "/usr/bin/php", tc.args...)
			cmd.Stdin = bytes.NewBufferString(tc.stdin)
			cmd.Stdout = &wantOut
			cmd.Stderr = &wantErr
			if err := cmd.Run(); err != nil && cmd.ProcessState == nil {
				t.Fatal(err)
			}
			code := run(tc.args, bytes.NewBufferString(tc.stdin), &gotOut, &gotErr)
			if code != cmd.ProcessState.ExitCode() || gotOut.String() != wantOut.String() || gotErr.String() != wantErr.String() {
				t.Fatalf("got (%q,%q,%d), want (%q,%q,%d)", gotOut.String(), gotErr.String(), code, wantOut.String(), wantErr.String(), cmd.ProcessState.ExitCode())
			}
		})
	}
	t.Run("version", func(t *testing.T) {
		var gotOut, gotErr bytes.Buffer
		code := run([]string{"-v"}, strings.NewReader(""), &gotOut, &gotErr)
		if code != 0 || !strings.Contains(gotOut.String(), "PHP 8.3.6") || gotErr.String() != "" {
			t.Fatalf("got (%q,%q,%d)", gotOut.String(), gotErr.String(), code)
		}
	})
}

func TestSAPIHTTPServerEndToEnd(t *testing.T) {
	docRoot := t.TempDir()
	sessDir := t.TempDir()
	script := fmt.Sprintf(`<?php
session_save_path(%q);
session_id("httpsess65");
session_start();
$_SESSION["hits"] = ($_SESSION["hits"] ?? 0) + 1;
$hits = $_SESSION["hits"];
session_write_close();

#[Tag("api")]
class Endpoint {
    public function __construct(public readonly string $service) {}
}
$rc = new ReflectionClass(Endpoint::class);
$attr = $rc->getAttributes()[0]->getArguments()[0];
$dt = new DateTimeImmutable("2026-10-14 00:00:00", new DateTimeZone("UTC"));
$email = filter_var($_POST["email"] ?? "none@example.com", FILTER_VALIDATE_EMAIL);

header("X-Powered-By: php127");
http_response_code(201);
echo $_SERVER["REQUEST_METHOD"], "|",
     ($_GET["q"] ?? ""), "|",
     $email, "|",
     ($_COOKIE["theme"] ?? ""), "|",
     $hits, "|",
     $attr, "|",
     $dt->format("Y-m-d");
`, sessDir)
	if err := os.WriteFile(filepath.Join(docRoot, "index.php"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	var errBuf bytes.Buffer
	srv, err := buildHTTPServer("127.0.0.1:0", docRoot, "", &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	for i := 1; i <= 2; i++ {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/index.php?q=horos", strings.NewReader("email=ada%40example.com"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 201 {
			t.Fatalf("req %d status=%d, body=%q, errBuf=%q", i, resp.StatusCode, string(body), errBuf.String())
		}
		if got := resp.Header.Get("X-Powered-By"); got != "php127" {
			t.Fatalf("req %d X-Powered-By=%q", i, got)
		}
		want := fmt.Sprintf("POST|horos|ada@example.com|dark|%d|api|2026-10-14", i)
		if string(body) != want {
			t.Fatalf("req %d body=%q, want %q (err=%q)", i, string(body), want, errBuf.String())
		}
	}
}

func TestPHPTCorpus(t *testing.T) {
	root := "/devhoros/php127/docs/lot2_1_astra"
	data, err := os.ReadFile(filepath.Join(root, "corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct{ Path, SHA256 string }
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	manifest := make(map[string]string, len(entries))
	for _, entry := range entries {
		manifest[entry.Path] = entry.SHA256
	}
	paths, err := filepath.Glob(filepath.Join(root, "php-src-php-8.3.6/tests/lang/0[01][0-9].phpt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 19 {
		t.Fatalf("inventory: %d, want 19", len(paths))
	}
	for _, rel := range []string{
		"tests/lang/020.phpt", "tests/lang/021.phpt", "tests/lang/022.phpt",
		"Zend/tests/compound_assign_with_numeric_strings.phpt",
		"tests/lang/025.phpt",
		"Zend/tests/enum/instanceof.phpt",
		"Zend/tests/enum/instanceof-backed-enum.phpt",
		"Zend/tests/enum/instanceof-unitenum.phpt",
		"Zend/tests/enum/implements.phpt",
		"Zend/tests/enum/backed-int.phpt",
		"Zend/tests/enum/backed-string.phpt",
		"Zend/tests/enum/backed-from.phpt",
		"Zend/tests/enum/backed-tryFrom.phpt",
		"Zend/tests/enum/basic-methods.phpt",
		"Zend/tests/enum/backed-cases-int.phpt",
		"Zend/tests/enum/backed-cases-string.phpt",
		"Zend/tests/match/001.phpt",
		"Zend/tests/match/002.phpt",
		"Zend/tests/match/003.phpt",
		"Zend/tests/match/004.phpt",
		"Zend/tests/match/005.phpt",
	} {
		paths = append(paths, filepath.Join(root, "php-src-php-8.3.6", rel))
	}
	// Les fichiers amont absents du corpus partage restent dans la zone du lot.
	local := map[string]string{
		"generator_send.phpt":         "076e698c15e77f9f9dcad83a8dbab4d6d06813ad4fae1d71d576ea6fe938014a",
		"get_return_and_finally.phpt": "f7fca7617187aeb81965a2dec9aa7c4c609fd2b8572050b7f11d4824d22d87f1",
		"yield_from_array.phpt":       "db0af872a1863a4fcbc5a1462bc2c5dc8b08e21bac9cc356a7b6f7d352e064c3",
	}
	for _, name := range []string{"generator_send.phpt", "get_return_and_finally.phpt", "yield_from_array.phpt"} {
		path, err := filepath.Abs(filepath.Join("testdata/lot34/Zend/tests/generators", name))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		rel, err := filepath.Rel(filepath.Join(root, "php-src-php-8.3.6"), path)
		if err != nil {
			t.Fatal(err)
		}
		manifest[rel] = local[name]
	}
	bin := filepath.Join(t.TempDir(), "php127")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	counts := map[string]int{"PASS": 0, "FAIL": 0, "SKIP": 0}
	baselineCounts := map[string]int{"PASS": 0, "FAIL": 0, "SKIP": 0}
	parity := 0
	for _, path := range paths {
		rel, err := filepath.Rel(filepath.Join(root, "php-src-php-8.3.6"), path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != manifest[rel] {
			t.Fatalf("fingerprint: %s", rel)
		}
		result, err := phpt.Run(t.Context(), path, phpt.Options{Target: bin, Oracle: "/usr/bin/php"})
		if err != nil {
			t.Errorf("%s: infrastructure: %v", rel, err)
			continue
		}
		counts[result.Verdict]++
		baseline, baselineErr := phpt.Run(t.Context(), path, phpt.Options{Target: "/usr/bin/php", Oracle: "/usr/bin/php"})
		if baselineErr != nil {
			t.Errorf("%s: oracle baseline: %v", rel, baselineErr)
		} else {
			baselineCounts[baseline.Verdict]++
		}
		if result.Oracle != nil && bytes.Equal(result.Target.Stdout, result.Oracle.Stdout) && bytes.Equal(result.Target.Stderr, result.Oracle.Stderr) && result.Target.ExitCode == result.Oracle.ExitCode {
			parity++
		} else {
			t.Errorf("%s: exact oracle parity failed: target=(%q,%q,%d), oracle=%+v", rel, result.Target.Stdout, result.Target.Stderr, result.Target.ExitCode, result.Oracle)
		}
		t.Logf("%s: %s: %s", rel, result.Verdict, result.Reason)
		if baselineErr == nil && baseline.Verdict == "PASS" && result.Verdict != "PASS" {
			t.Errorf("%s: target=(%q,%q,%d); oracle-only verdict=%s", rel, result.Target.Stdout, result.Target.Stderr, result.Target.ExitCode, baseline.Verdict)
		}
	}
	t.Logf("N=%d PASS=%d FAIL=%d SKIP=%d", len(paths), counts["PASS"], counts["FAIL"], counts["SKIP"])
	t.Logf("Oracle-only: PASS=%d FAIL=%d SKIP=%d; exact parity=%d/%d", baselineCounts["PASS"], baselineCounts["FAIL"], baselineCounts["SKIP"], parity, len(paths))
}

func TestAuditParityB1B2M4M9(t *testing.T) {
	scripts := map[string]string{
		"b1_no_panic": `<?php
$a = [1, 2];
var_dump($a[1.5] ?? 'def', isset($a[1.5]), array_key_exists(1.5, $a));
$s = "abc";
var_dump($s["x"] ?? 'n', isset($s["x"]), empty($s["x"]));
$n = 42;
var_dump($n[0] ?? 'scalar', isset($n[0]), isset($n->prop));
`,
		"b2_parse_unterm": `<?php
$x = 1 /`,
		"m9_deprecations_and_magic": `<?php
var_dump(1.5 | 2, 5.7 % 2, 1.5 << 1, "1e2" + 1, (-2) ** 63);
var_dump("abc" <=> null, "" <=> null, null <=> "abc");
$n = null; --$n; var_dump($n);
$s = "a9"; ++$s; var_dump($s);
var_dump(strlen(null));
#[AllowDynamicProperties]
class AllowDyn {}
$ad = new AllowDyn; $ad->dyn = 7; var_dump($ad->dyn);
class Box {
    private array $d = [];
    public function __get($k) { return $this->d[$k] ?? 'missing'; }
    public function __set($k, $v) { $this->d[$k] = $v; }
    public function __isset($k) { return isset($this->d[$k]); }
    public function __unset($k) { unset($this->d[$k]); }
    public function __call($m, $a) { return "$m:" . implode(",", $a); }
    public static function __callStatic($m, $a) { return "S:$m:" . implode(",", $a); }
    public function __toString() { return "BoxStr"; }
}
$b = new Box;
$b->k = "v";
var_dump($b->k, isset($b->k), empty($b->k));
unset($b->k);
var_dump(isset($b->k), $b->hello(1, 2), Box::stat(3), (string)$b);
`,
		"m4_vm_callbacks_and_ob": `<?php
var_dump(
    array_map(fn($x) => $x * 2, [1, 2, 3]),
    array_filter([0, 1, 2, "", 3]),
    array_reduce([1, 2, 3, 4], fn($c, $x) => $c + $x, 0)
);
$u = [3, 1, 2];
usort($u, fn($a, $b) => $a <=> $b);
var_dump($u);
$w = [10, 20];
array_walk($w, function(&$v, $k) { $v += $k; });
var_dump($w);
var_dump(preg_replace_callback('/(\d+)/', fn($m) => $m[1] * 10, 'a2b3'));
ob_start();
echo "hello ";
ob_start();
echo "world";
$inner = ob_get_clean();
echo strtoupper($inner);
var_dump(ob_get_clean());
`,
	}
	dir := t.TempDir()
	for name, src := range scripts {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(dir, name+".php")
			if err := os.WriteFile(file, []byte(src), 0600); err != nil {
				t.Fatal(err)
			}
			var gotOut, gotErr, wantOut, wantErr bytes.Buffer
			cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", file)
			cmd.Stdout = &wantOut
			cmd.Stderr = &wantErr
			_ = cmd.Run()
			code := run([]string{"-n", file}, strings.NewReader(""), &gotOut, &gotErr)
			if code != cmd.ProcessState.ExitCode() || gotOut.String() != wantOut.String() || gotErr.String() != wantErr.String() {
				t.Fatalf("%s mismatch:\ngot  code=%d out=%q err=%q\nwant code=%d out=%q err=%q",
					name, code, gotOut.String(), gotErr.String(), cmd.ProcessState.ExitCode(), wantOut.String(), wantErr.String())
			}
		})
	}
}

