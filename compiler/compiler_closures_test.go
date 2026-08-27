// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Tests de conformité des fermetures, fonctions fléchées et callables

package compiler

import (
	"bytes"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestCompilerClosuresAndArrowFunctions(t *testing.T) {
	src := `
	$double = fn($x) => $x * 2;
	$res1 = $double(21);

	$multiplier = function($a, $b) {
		return $a * $b;
	};
	$res2 = $multiplier(6, 7);

	$isCallableDouble = is_callable($double);
	echo $res1 . ":" . $res2 . ":" . ($isCallableDouble ? "yes" : "no");
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Compilation closures error: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Execution closures error: %v", err)
	}

	expected := "42:42:yes"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}
