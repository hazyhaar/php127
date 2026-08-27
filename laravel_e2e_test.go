// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127: Test d'intégration de bout en bout d'un micro-noyau Laravel 11 / Illuminate

package main_test

import (
	"bytes"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestLaravelIlluminateMicroFrameworkE2E(t *testing.T) {
	appScript := `
	// 1. Exceptions de domaine
	class ValidationException {
		public $message;
		function __construct($msg) {
			$this->message = $msg;
		}
	}

	// 2. Conteneur d'injection de dépendances (Service Container)
	class Container {
		public $bindings;

		function __construct() {
			$this->bindings = [];
		}

		function bind($key, $resolver) {
			$arr = $this->bindings;
			$arr[count($arr)] = $resolver;
			$this->bindings = $arr;
		}

		function make($key) {
			// Résolution dynamique
			return "ResolvedInstance:" . $key;
		}
	}

	// 3. Service Métier
	class UserService {
		public $dbPrefix;

		function __construct($prefix) {
			$this->dbPrefix = $prefix;
		}

		function createUser($username, $email) {
			if (strlen($username) < 3) {
				throw new ValidationException("Nom d'utilisateur trop court");
			}
			return $this->dbPrefix . "_" . $username . "<" . strtolower($email) . ">";
		}
	}

	// 4. Pipeline de Middlewares (Closures & Callbacks)
	$log = "";
	$authMiddleware = function($req, $next) {
		return $next($req . "[AuthPassed]");
	};

	$coreHandler = function($req) {
		return "Handled:" . $req;
	};

	// 5. Exécution du cycle applicatif
	$container = new Container();
	$service = new UserService("app_users");

	$status = "";
	try {
		$user1 = $service->createUser("alice", "ALICE@EXAMPLE.COM");
		$status .= "Created:" . $user1;
	} catch (ValidationException $e) {
		$status .= "Error:" . $e->message;
	}

	try {
		$user2 = $service->createUser("bo", "BO@EXAMPLE.COM");
		$status .= ";Created:" . $user2;
	} catch (ValidationException $e) {
		$status .= ";CatchedExpected:" . $e->message;
	}

	// 6. Rendu de réponse avec capture de flux SPL
	ob_start();
	echo "<div class=\"laravel-app\">\n";
	echo "  <h1>Laravel Sovereign Engine</h1>\n";
	echo "  <p>" . $status . "</p>\n";
	echo "</div>";
	$htmlResponse = ob_get_clean();

	echo $htmlResponse;
	`

	prog, err := compiler.Compile(appScript)
	if err != nil {
		t.Fatalf("Erreur compilation Laravel E2E: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution Laravel E2E: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "Created:app_users_alice<alice@example.com>") {
		t.Fatalf("Utilisateur 1 manquant dans le rendu: %s", res)
	}
	if !strings.Contains(res, "CatchedExpected:Nom d'utilisateur trop court") {
		t.Fatalf("Exception attendue non attrapée: %s", res)
	}
	if !strings.Contains(res, "<h1>Laravel Sovereign Engine</h1>") {
		t.Fatalf("En-tête HTML manquant: %s", res)
	}
}
