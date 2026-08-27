// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Tests unitaires pour les fonctionnalités Objets et SPL

package compiler

import (
	"bytes"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestCompilerOOPClassesAndMethods(t *testing.T) {
	src := `
	class Point {
		public $x;
		public $y;

		function __construct($x, $y) {
			$this->x = $x;
			$this->y = $y;
		}

		function normSquared() {
			return $this->x * $this->x + $this->y * $this->y;
		}
	}

	$p = new Point(3, 4);
	echo $p->normSquared();
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation OOP: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur execution VM OOP: %v", err)
	}

	if out.String() != "25" {
		t.Fatalf("Attendu '25', obtenu: %q", out.String())
	}
}

func TestCompilerOOPInheritanceAndInstanceOf(t *testing.T) {
	src := `
	class Animal {}
	class Canine extends Animal {}
	class Dog extends Canine {}

	$d = new Dog();
	if ($d instanceof Canine) {
		echo "IS_CANINE ";
	}
	if ($d instanceof Animal) {
		echo "IS_ANIMAL";
	}
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur execution: %v", err)
	}

	expected := "IS_CANINE IS_ANIMAL"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu: %q", expected, out.String())
	}
}

func TestCompilerOOPStaticMethods(t *testing.T) {
	src := `
	class MathUtils {
		public static function square($n) {
			return $n * $n;
		}
	}

	echo MathUtils::square(9);
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur execution: %v", err)
	}

	if out.String() != "81" {
		t.Fatalf("Attendu '81', obtenu: %q", out.String())
	}
}

func TestCompilerOutputBuffering(t *testing.T) {
	src := `
	ob_start();
	echo "Buffer Content: ";
	echo 12345;
	$content = ob_get_clean();
	echo "CAPTURED[" . $content . "]";
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur execution: %v", err)
	}

	expected := "CAPTURED[Buffer Content: 12345]"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu: %q", expected, out.String())
	}
}

func TestCompilerOOPZeroAllocExecution(t *testing.T) {
	src := `
	class Counter {
		public $val;
		function __construct($init) {
			$this->val = $init;
		}
		function inc() {
			$this->val = $this->val + 1;
			return $this->val;
		}
	}

	$c = new Counter(0);
	$i = 0;
	while ($i < 100) {
		$c->inc();
		$i = $i + 1;
	}
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)

	// Warmup
	_, _ = machine.Execute(prog)

	allocs := testing.AllocsPerRun(10, func() {
		machine.Reset()
		_, _ = machine.Execute(prog)
	})

	if allocs != 0 {
		t.Fatalf("Zéro allocation violée sur boucle OOP: %f allocs/run", allocs)
	}
}
