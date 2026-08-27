// SPDX-License-Identifier: Apache-2.0 OR MIT

package compiler

import (
	"bytes"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/vm"
)

func executeCode(t *testing.T, src string, out *bytes.Buffer) (int64, string, error) {
	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Compilation failed: %v", err)
	}

	virtualMachine := vm.NewVM(out)
	res, err := virtualMachine.Execute(prog)
	if err != nil {
		return 0, "", err
	}
	return res.ToLong(), res.String(), nil
}

func TestCompilerArithmeticAndVariables(t *testing.T) {
	src := `<?php
	$a = 15;
	$b = 27;
	$c = ($a * 2) + ($b - 7);
	return $c;
	`
	res, _, err := executeCode(t, src, nil)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}
	if res != 50 { // (15*2) + (27-7) = 30 + 20 = 50
		t.Fatalf("Expected 50, got %d", res)
	}
}

func TestCompilerEchoAndStrings(t *testing.T) {
	var out bytes.Buffer
	src := `<?php
	$greeting = "Bonjour ";
	$name = "PHP 127";
	echo $greeting . $name . " !";
	`
	_, _, err := executeCode(t, src, &out)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}
	if out.String() != "Bonjour PHP 127 !" {
		t.Fatalf("Expected 'Bonjour PHP 127 !', got %q", out.String())
	}
}

func TestCompilerWhileAndForLoops(t *testing.T) {
	srcWhile := `<?php
	$i = 0;
	$sum = 0;
	while ($i < 10) {
		$sum += $i;
		$i += 1;
	}
	return $sum;
	`
	resWhile, _, err := executeCode(t, srcWhile, nil)
	if err != nil {
		t.Fatalf("While failed: %v", err)
	}
	if resWhile != 45 {
		t.Fatalf("While sum expected 45, got %d", resWhile)
	}

	srcFor := `<?php
	$total = 0;
	for ($j = 1; $j <= 5; $j += 1) {
		$total += $j * 10;
	}
	return $total;
	`
	resFor, _, err := executeCode(t, srcFor, nil)
	if err != nil {
		t.Fatalf("For failed: %v", err)
	}
	if resFor != 150 { // 10+20+30+40+50 = 150
		t.Fatalf("For sum expected 150, got %d", resFor)
	}
}

func TestCompilerForeachArray(t *testing.T) {
	src := `<?php
	$items = [100, 200, 300];
	$sum = 0;
	foreach ($items as $k => $v) {
		$sum += $v + $k;
	}
	return $sum;
	`
	res, _, err := executeCode(t, src, nil)
	if err != nil {
		t.Fatalf("Foreach failed: %v", err)
	}
	// 100+0 + 200+1 + 300+2 = 603
	if res != 603 {
		t.Fatalf("Foreach sum expected 603, got %d", res)
	}
}

func TestCompilerMatchExpression(t *testing.T) {
	src := `<?php
	$status = 2;
	$res = match ($status) {
		1 => 10,
		2 => 20,
		default => 0,
	};
	return $res;
	`
	res, _, err := executeCode(t, src, nil)
	if err != nil {
		t.Fatalf("Match failed: %v", err)
	}
	if res != 20 {
		t.Fatalf("Match expected 20, got %d", res)
	}
}

func TestCompilerSwitchStatement(t *testing.T) {
	src := `<?php
	$val = 42;
	$out = 0;
	switch ($val) {
		case 10:
			$out = 1;
		case 42:
			$out = 99;
		default:
			$out = 100;
	}
	return $out;
	`
	res, _, err := executeCode(t, src, nil)
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}
	if res != 99 {
		t.Fatalf("Switch expected 99, got %d", res)
	}
}

func TestCompilerFunctionsAndRecursion(t *testing.T) {
	src := `<?php
	function multiplyAdd($a, $b, $c) {
		return ($a * $b) + $c;
	}

	$res = multiplyAdd(5, 6, 7);
	return $res;
	`
	res, _, err := executeCode(t, src, nil)
	if err != nil {
		t.Fatalf("Function failed: %v", err)
	}
	if res != 37 { // (5*6) + 7 = 37
		t.Fatalf("Function result expected 37, got %d", res)
	}
}

func TestCompilerBuiltins(t *testing.T) {
	src := `<?php
	$s = "Flatstack127";
	$len = strlen($s);
	$pos = strpos($s, "stack");
	$sub = substr($s, 0, 4);
	if ($len == 12 && $pos == 4 && $sub == "Flat") {
		return 1;
	}
	return 0;
	`
	res, _, err := executeCode(t, src, nil)
	if err != nil {
		t.Fatalf("Builtins failed: %v", err)
	}
	if res != 1 {
		t.Fatalf("Builtins check expected 1, got %d", res)
	}
}

func TestCompilerZeroAllocExecution(t *testing.T) {
	src := `<?php
	$sum = 0;
	for ($i = 0; $i < 100; $i += 1) {
		$sum += $i;
	}
	return $sum;
	`
	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Compilation failed: %v", err)
	}

	virtualMachine := vm.NewVM(nil)
	allocs := testing.AllocsPerRun(1000, func() {
		virtualMachine.Reset()
		_, _ = virtualMachine.Execute(prog)
	})
	if allocs != 0 {
		t.Fatalf("Allocs/op = %.2f, expected 0", allocs)
	}
}
