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

func TestCompilerCanonicalErrorVsExceptionCatch(t *testing.T) {
	src := `
	$log = "";
	try {
		try {
			$log .= "1";
			throw new TypeError("Type mismatch detected");
			$log .= "2";
		} catch (Exception $e) {
			$log .= ":ExceptionCaught"; // Must NOT catch TypeError!
		}
	} catch (Error $e) {
		$log .= ":ErrorCaught"; // Must catch TypeError because TypeError extends Error!
	} catch (Throwable $e) {
		$log .= ":ThrowableCaught";
	}

	try {
		try {
			$log .= "|A";
			throw new InvalidArgumentException("Invalid value");
		} catch (Error $e) {
			$log .= ":ErrorCaught"; // Must NOT catch InvalidArgumentException!
		}
	} catch (Exception $e) {
		$log .= ":ExceptionCaught"; // Must catch InvalidArgumentException!
	}

	echo $log;
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation error vs exception: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution error vs exception: %v", err)
	}

	expected := "1:ErrorCaught|A:ExceptionCaught"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}

func TestCompilerSilenceOperatorAt(t *testing.T) {
	src := `
	$val = @(40 + 2);
	echo "Res:" . $val;
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation opérateur @: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution opérateur @: %v", err)
	}

	expected := "Res:42"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}

func TestCompilerPDOOOPExecution(t *testing.T) {
	src := `
	$pdo = new PDO("sqlite::memory:");
	$pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
	$pdo->exec("CREATE TABLE users (id, name, email)");

	$stmt = $pdo->prepare("INSERT INTO users (id, name, email) VALUES (?, ?, ?)");
	$stmt->execute([101, "Ada Lovelace", "ada@example.com"]);

	$qStmt = $pdo->query("SELECT id, name, email FROM users WHERE id = ?", 101);
	$row = $qStmt->fetch(PDO::FETCH_ASSOC);

	echo $pdo->getAttribute(PDO::ATTR_ERRMODE) . "|" . $row[1] . "|" . $qStmt->rowCount();
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation PDO OOP: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution PDO OOP: %v", err)
	}

	expected := "2|Ada Lovelace|1"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}
