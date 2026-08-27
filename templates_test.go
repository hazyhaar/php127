// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127: Test de moteur de templates et compatibilité CMS

package main_test

import (
	"bytes"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestTemplateEngineRendering(t *testing.T) {
	templateScript := `
	class TemplateView {
		public $title;
		public $items;

		function __construct($title) {
			$this->title = $title;
			$this->items = [];
		}

		function addItem($item) {
			$arr = $this->items;
			$arr[count($arr)] = $item;
			$this->items = $arr;
		}

		function render() {
			ob_start();
			echo "<div class=\"card\">\n";
			echo "  <h2>" . $this->title . "</h2>\n";
			echo "  <ul>\n";
			foreach ($this->items as $item) {
				echo "    <li>" . strtoupper($item) . "</li>\n";
			}
			echo "  </ul>\n";
			echo "</div>";
			return ob_get_clean();
		}
	}

	$view = new TemplateView("Dashboard Articles");
	$view->addItem("Introduction to php127");
	$view->addItem("Sovereign zero-alloc runtime");
	$view->addItem("BlueTeam Taint Security");

	echo $view->render();
	`

	prog, err := compiler.Compile(templateScript)
	if err != nil {
		t.Fatalf("Erreur compilation template: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution template: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "<h2>Dashboard Articles</h2>") {
		t.Fatalf("Titre manquant dans le rendu: %s", res)
	}
	if !strings.Contains(res, "<li>INTRODUCTION TO PHP127</li>") {
		t.Fatalf("Élément transformé manquant: %s", res)
	}
	if !strings.Contains(res, "<li>SOVEREIGN ZERO-ALLOC RUNTIME</li>") {
		t.Fatalf("Élément 2 manquant: %s", res)
	}
}
