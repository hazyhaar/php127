// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Tests avancés PHP 8.4 (Namespaces, Promotion, Traits, VTable, Enums)

package compiler

import (
	"bytes"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestCompilerConstructorPromotionAndVTable(t *testing.T) {
	src := `
	class BaseEntity {
		public $id;
		function getId() {
			return $this->id;
		}
	}

	class User extends BaseEntity {
		function __construct(public $name, public $email) {
			$this->id = 101;
		}
	}

	$u = new User("Alice", "alice@example.com");
	echo $u->getId() . ":" . $u->name . ":" . $u->email;
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation constructor promotion: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution constructor promotion: %v", err)
	}

	expected := "101:Alice:alice@example.com"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}

func TestCompilerTraitFlattening(t *testing.T) {
	src := `
	class TimestampTrait {
		function getCreatedAt() {
			return "2026-08-27";
		}
	}

	class Post {
		use TimestampTrait;
		public $title;
		function __construct($t) {
			$this->title = $t;
		}
	}

	$p = new Post("Premier Article");
	echo $p->title . ":" . $p->getCreatedAt();
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation traits: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution traits: %v", err)
	}

	expected := "Premier Article:2026-08-27"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}
