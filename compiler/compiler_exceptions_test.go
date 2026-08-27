// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Tests de conformité des exceptions et du try-catch-finally

package compiler

import (
	"bytes"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestCompilerTryCatchExecution(t *testing.T) {
	src := `
	class CustomException {
		public $message;
		function __construct($msg) {
			$this->message = $msg;
		}
	}

	$res = "";
	try {
		$res .= "A";
		throw new CustomException("Erreur critique");
		$res .= "B";
	} catch (CustomException $e) {
		$res .= "C:" . $e->message;
	} finally {
		$res .= ":F";
	}
	echo $res;
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Compilation try-catch error: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Execution try-catch error: %v", err)
	}

	expected := "AC:Erreur critique:F"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}

func TestCompilerNestedTryCatchUnwinding(t *testing.T) {
	src := `
	class DatabaseException {
		public $message;
		function __construct($msg) {
			$this->message = $msg;
		}
	}

	function queryDB() {
		throw new DatabaseException("Connexion perdue");
		return 42;
	}

	function executeWorkflow() {
		$val = queryDB();
		return $val + 1;
	}

	$caught = "";
	try {
		executeWorkflow();
	} catch (DatabaseException $e) {
		$caught = "Attrapé: " . $e->message;
	}
	echo $caught;
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Compilation nested error: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Execution nested error: %v", err)
	}

	expected := "Attrapé: Connexion perdue"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}
