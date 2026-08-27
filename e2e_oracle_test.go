// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127: Banc de validation formelle de parité contre l'oracle CLI PHP

package main_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

func runPhpOracle(t *testing.T, code string) string {
	cmd := exec.Command("php", "-r", code)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Oracle PHP execution error: %v, output: %s", err, string(out))
	}
	return string(out)
}

func runPhp127Engine(t *testing.T, code string) (string, int64, error) {
	prog, err := compiler.Compile("<?php " + code)
	if err != nil {
		return "", 0, err
	}

	var out bytes.Buffer
	v := vm.NewVM(&out)
	res, err := v.Execute(prog)
	if err != nil {
		return out.String(), 0, err
	}
	return out.String(), res.ToLong(), nil
}

func assertOracleParity(t *testing.T, testName, code string) {
	t.Run(testName, func(t *testing.T) {
		oracleOut := runPhpOracle(t, code)
		engineOut, _, err := runPhp127Engine(t, code)
		if err != nil {
			t.Fatalf("php127 execution failed: %v", err)
		}

		if strings.TrimSpace(engineOut) != strings.TrimSpace(oracleOut) {
			t.Fatalf("Parity mismatch on %s:\nOracle Output: %q\nphp127 Output: %q", testName, oracleOut, engineOut)
		}
	})
}

func TestOracleParitySuite(t *testing.T) {
	// 1. Calcul Arithmétique & Boucle For
	codeArith := `
	$sum = 0;
	for ($i = 1; $i <= 10; $i += 1) {
		$sum += $i * $i;
	}
	echo $sum;
	`
	assertOracleParity(t, "ArithAndForLoop", codeArith)

	// 2. Manipulation de chaînes & Concaténation
	codeStr := `
	$prefix = "PHP127";
	$sep = "::";
	$suffix = "SOVEREIGN";
	$res = $prefix . $sep . $suffix;
	echo $res;
	`
	assertOracleParity(t, "StringConcat", codeStr)

	// 3. Substr et strlen
	codeSubstr := `
	$s = "AntigravityFlatstack";
	$sub = substr($s, 4, 7);
	echo strlen($s) . "_" . $sub;
	`
	assertOracleParity(t, "SubstrAndStrlen", codeSubstr)

	// 4. Foreach sur tableaux
	codeForeach := `
	$arr = [10, 20, 30, 40];
	$total = 0;
	foreach ($arr as $k => $v) {
		$total += $v - $k;
	}
	echo $total;
	`
	assertOracleParity(t, "ForeachArray", codeForeach)

	// 5. Fonctions utilisateur & Récursion (Fibonacci)
	codeFib := `
	function fib($n) {
		if ($n <= 1) {
			return $n;
		}
		$a = 0;
		$b = 1;
		for ($i = 2; $i <= $n; $i += 1) {
			$c = $a + $b;
			$a = $b;
			$b = $c;
		}
		return $b;
	}
	echo fib(10);
	`
	assertOracleParity(t, "FibonacciIterative", codeFib)

	// 6. Match Expression
	codeMatch := `
	$role = 2;
	$label = match ($role) {
		1 => "Admin",
		2 => "Operator",
		default => "Guest",
	};
	echo $label;
	`
	assertOracleParity(t, "MatchExpression", codeMatch)

	// 7. Opérateur Spaceship & Logique bitwise
	codeBitwise := `
	$a = 12; // 0b1100
	$b = 10; // 0b1010
	$and = $a & $b;
	$or = $a | $b;
	$cmp = $a <=> $b;
	echo $and . "-" . $or . "-" . $cmp;
	`
	assertOracleParity(t, "BitwiseAndSpaceship", codeBitwise)
}

func TestZeroAllocParityAtScale(t *testing.T) {
	code := `<?php
	function calc($x, $y) {
		return ($x * $y) + ($x - $y);
	}
	$acc = 0;
	for ($i = 0; $i < 50; $i += 1) {
		$acc += calc($i, 2);
	}
	return $acc;
	`
	prog, err := compiler.Compile(code)
	if err != nil {
		t.Fatalf("Compilation failed: %v", err)
	}

	v := vm.NewVM(nil)
	allocs := testing.AllocsPerRun(1000, func() {
		v.Reset()
		_, _ = v.Execute(prog)
	})

	if allocs != 0 {
		t.Fatalf("Allocs/op = %.2f, invariant dur attendu = 0", allocs)
	}
}
