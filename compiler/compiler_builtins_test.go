// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Tests d'intégration des builtins exécutés via script

package compiler

import (
	"bytes"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestCompilerBuiltinsExecution(t *testing.T) {
	src := `
	$m = md5("test string");
	$lo = strtolower("HELLO");
	$rep = str_replace("foo", "bar", "foobaz");
	$rpt = str_repeat("ab", 4);
	echo $lo . ":" . $rep . ":" . $rpt;
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Compilation error: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Execution error: %v", err)
	}

	expected := "hello:barbaz:abababab"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}
