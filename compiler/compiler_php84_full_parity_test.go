// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Tests de parité intégrale PHP 8.4 (Attributes, Traits adaptations, Fibers, SQL)

package compiler

import (
	"bytes"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestCompilerAttributesAndTraitAdaptations(t *testing.T) {
	src := `
	trait TraitA {
		function action() {
			return "ActionA";
		}
	}

	trait TraitB {
		function action() {
			return "ActionB";
		}
	}

	#[Route("/api/test")]
	class Controller {
		use TraitA, TraitB {
			TraitA::action insteadof TraitB;
		}

		#[HttpGet]
		function handle() {
			return $this->action();
		}
	}

	$c = new Controller();
	echo $c->handle();
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation attributes & traits: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution attributes & traits: %v", err)
	}

	expected := "ActionA"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}

func TestCompilerFibersExecution(t *testing.T) {
	src := `
	$fib = fiber_create(function() {
		return 84 / 2;
	});

	$val = fiber_start($fib);
	$isTerminated = fiber_is_terminated($fib);
	echo $val . ":" . ($isTerminated ? "done" : "running");
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation fibers: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution fibers: %v", err)
	}

	expected := "42:done"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}
