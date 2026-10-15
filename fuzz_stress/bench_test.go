package fuzz_stress

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/vm"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	microDirPath  = "/devhoros/php127/testdata/php-rust/php-rust/wp97-harness/micro"
	hostPHPBin    = "/usr/bin/php"
	phpRustBin    = "/devhoros/php127/testdata/php-rust/php-rust/target/release/phpr"
	php127CLIBin  = "/devhoros/php127/bin/php127"
)

type benchCase struct {
	Name         string
	SourceFile   string
	Replacements [][2]string
	SkipCLI      bool
}

var microSuite = []benchCase{
	{Name: "empty", SourceFile: "empty.php"},
	{Name: "noop_small", SourceFile: "noop_small.php"},
	{Name: "add_small", SourceFile: "add_small.php"},
	{Name: "arith_small", SourceFile: "arith_small.php"},
	{Name: "prop_small", SourceFile: "prop_small.php"},
	{Name: "calls_small", SourceFile: "calls_small.php"},
	{Name: "str_small", SourceFile: "str.php", Replacements: [][2]string{{"4000000", "20000"}}},
	{Name: "arr_small", SourceFile: "arr.php", Replacements: [][2]string{{"100000", "2000"}, {"$r<60", "$r<10"}}},
	{Name: "re_small", SourceFile: "re.php", Replacements: [][2]string{{"2000000", "200"}}},
	{Name: "re_20k", SourceFile: "re.php", Replacements: [][2]string{{"2000000", "20000"}}, SkipCLI: true},
}

func loadBenchSource(t testing.TB, bc benchCase) (string, string) {
	t.Helper()
	fullPath := filepath.Join(microDirPath, bc.SourceFile)
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("lecture %s: %v", fullPath, err)
	}
	src := string(raw)
	if len(bc.Replacements) > 0 {
		for _, rep := range bc.Replacements {
			src = strings.ReplaceAll(src, rep[0], rep[1])
		}
		tmpFile := filepath.Join(t.TempDir(), bc.Name+".php")
		if err := os.WriteFile(tmpFile, []byte(src), 0o600); err != nil {
			t.Fatalf("écriture %s: %v", tmpFile, err)
		}
		return src, tmpFile
	}
	return src, fullPath
}

func runSubprocessMedian(t testing.TB, bin string, args []string, runs int) (string, time.Duration, error) {
	t.Helper()
	warmCmd := exec.Command(bin, args...)
	if out, err := warmCmd.CombinedOutput(); err != nil {
		return "", 0, fmt.Errorf("%s %v: %w (%s)", bin, args, err, string(out))
	}

	durations := make([]time.Duration, 0, runs)
	var lastOut string
	for range runs {
		t0 := time.Now()
		cmd := exec.Command(bin, args...)
		out, err := cmd.CombinedOutput()
		d := time.Since(t0)
		if err != nil {
			return "", 0, fmt.Errorf("%s %v: %w (%s)", bin, args, err, string(out))
		}
		durations = append(durations, d)
		lastOut = string(out)
	}
	slices.Sort(durations)
	return lastOut, durations[len(durations)/2], nil
}

func runInProcessVMMedian(t testing.TB, z *zone.Zone, src string, name string, runs int) (string, time.Duration) {
	t.Helper()
	prog, err := compiler.CompileSource(src, name)
	if err != nil {
		t.Fatalf("CompileSource(%s): %v", name, err)
	}
	verifyCompiledProgram(t, prog)

	var warmOut bytes.Buffer
	vm.Execute(z, &warmOut, io.Discard, prog, nil)
	z.Reset(func(item zone.CleanupItem) {
		types.DispatchObjectDestructor(z, item)
	})

	durations := make([]time.Duration, 0, runs)
	var lastOut string
	for range runs {
		var out bytes.Buffer
		t0 := time.Now()
		code := vm.Execute(z, &out, io.Discard, prog, nil)
		z.Reset(func(item zone.CleanupItem) {
			types.DispatchObjectDestructor(z, item)
		})
		d := time.Since(t0)
		if code != 0 {
			t.Fatalf("vm.Execute(%s) code=%d", name, code)
		}
		durations = append(durations, d)
		lastOut = out.String()
	}
	slices.Sort(durations)
	return lastOut, durations[len(durations)/2]
}

// TestThreeWayBenchmark compare sur la suite micro de wp97-harness :
// 1. Oracle /usr/bin/php -n (CLI sous-processus)
// 2. phpr de francescotinti/php-rust (CLI sous-processus release)
// 3. php127 CLI (/devhoros/php127/bin/php127)
// 4. php127 VM en mémoire (Program précompilé + Zone réutilisée via z.Reset())
// et vérifie la parité bit à bit des sorties Stdout entre les moteurs.
func TestThreeWayBenchmark(t *testing.T) {
	const runs = 3
	z := zone.New(64 << 20)

	hasPHPR := false
	if st, err := os.Stat(phpRustBin); err == nil && !st.IsDir() {
		hasPHPR = true
	}

	cliBin := filepath.Join(t.TempDir(), "php127")
	buildCmd := exec.Command("go", "build", "-o", cliBin, "/devhoros/php127/cmd/php127")
	buildCmd.Dir = "/devhoros/php127"
	hasPHP127CLI := true
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Logf("avertissement compilation temporaire php127 CLI: %v (%s)", err, string(out))
		if st, serr := os.Stat(php127CLIBin); serr == nil && !st.IsDir() {
			cliBin = php127CLIBin
		} else {
			hasPHP127CLI = false
		}
	}

	t.Logf("%-12s | %-11s | %-11s | %-12s | %-11s | %-14s | %-14s | %-6s",
		"Benchmark", "php -n CLI", "phpr (Rust)", "php127 (CLI)", "php127 (VM)", "Gain CLI/phpr", "Gain VM/phpr", "Parité")
	t.Logf("%s", strings.Repeat("-", 108))

	for _, bc := range microSuite {
		src, scriptPath := loadBenchSource(t, bc)

		oracleOut, oracleDur, err := runSubprocessMedian(t, hostPHPBin, []string{"-n", scriptPath}, runs)
		if err != nil {
			t.Fatalf("[%s] oracle php -n: %v", bc.Name, err)
		}

		vmOut, vmDur := runInProcessVMMedian(t, z, src, bc.Name+".php", runs)
		if vmOut != oracleOut {
			t.Fatalf("[%s] divergence VM php127 vs oracle php:\ngot:  %q\nwant: %q", bc.Name, vmOut, oracleOut)
		}

		var cliDur time.Duration
		cliDurStr := "n/d (64M)"
		if hasPHP127CLI && !bc.SkipCLI {
			cliOut, d, err := runSubprocessMedian(t, cliBin, []string{scriptPath}, runs)
			if err != nil {
				t.Fatalf("[%s] php127 CLI: %v", bc.Name, err)
			}
			if cliOut != oracleOut {
				t.Fatalf("[%s] divergence CLI php127 vs oracle php:\ngot:  %q\nwant: %q", bc.Name, cliOut, oracleOut)
			}
			cliDur = d
			cliDurStr = fmt.Sprintf("%.2f ms", float64(cliDur.Microseconds())/1000.0)
		}

		phprDurStr := "n/d"
		gainCLIStr := "n/d"
		gainVMStr := "n/d"
		if hasPHPR {
			phprOut, phprDur, err := runSubprocessMedian(t, phpRustBin, []string{scriptPath}, runs)
			if err != nil {
				t.Fatalf("[%s] phpr: %v", bc.Name, err)
			}
			if phprOut != oracleOut {
				t.Fatalf("[%s] divergence phpr vs oracle php:\ngot:  %q\nwant: %q", bc.Name, phprOut, oracleOut)
			}
			phprDurStr = fmt.Sprintf("%.2f ms", float64(phprDur.Microseconds())/1000.0)
			if cliDur > 0 {
				gainCLIStr = fmt.Sprintf("%.2fx", float64(phprDur)/float64(cliDur))
			}
			if vmDur > 0 {
				gainVMStr = fmt.Sprintf("%.2fx", float64(phprDur)/float64(vmDur))
			}
		}

		t.Logf("%-12s | %8.2f ms | %11s | %12s | %8.2f ms | %14s | %14s | %-6s",
			bc.Name,
			float64(oracleDur.Microseconds())/1000.0,
			phprDurStr,
			cliDurStr,
			float64(vmDur.Microseconds())/1000.0,
			gainCLIStr,
			gainVMStr,
			"EXACTE",
		)
	}
}

// BenchmarkInProcessVMArithSmall mesure l'exécution en mémoire de arith_small.php avec réutilisation de Zone.
func BenchmarkInProcessVMArithSmall(b *testing.B) {
	raw, err := os.ReadFile(filepath.Join(microDirPath, "arith_small.php"))
	if err != nil {
		b.Fatalf("ReadFile: %v", err)
	}
	prog, err := compiler.CompileSource(string(raw), "arith_small.php")
	if err != nil {
		b.Fatalf("CompileSource: %v", err)
	}
	z := zone.New(4 << 20)
	for b.Loop() {
		vm.Execute(z, io.Discard, io.Discard, prog, nil)
		z.Reset(func(item zone.CleanupItem) {
			types.DispatchObjectDestructor(z, item)
		})
	}
}
