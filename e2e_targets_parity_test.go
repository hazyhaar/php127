package main

import (
	"bytes"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestTenStrategicTargetsFullPipeline(t *testing.T) {
	src := `
	// 1. vlucas/phpdotenv
	$envContent = "APP_NAME=LaravelPhp127\nDB_DATABASE=memory_test\nAPI_KEY=Secret123";
	$envCount = parse_dotenv($envContent);
	$appName = getenv("APP_NAME");

	// 2. ramsey/uuid & symfony/uid
	$u4 = uuid_v4();
	$u7 = uuid_v7();
	$ul = ulid();

	// 3. monolog/monolog
	$logLine = monolog_log("INFO", "Bootstrap application completed", "kernel");

	// 4. ext-curl / guzzlehttp
	$ch = curl_init("https://api.github.com/status");
	curl_setopt($ch, 19913, true); // CURLOPT_RETURNTRANSFER
	curl_close($ch);

	// 5. illuminate/database & PDO
	pdo_exec("CREATE TABLE orders (uuid, total)");
	pdo_exec("INSERT INTO orders (uuid, total) VALUES (?, ?)", $u7, 150);
	$lastId = pdo_last_id();

	// 6. Output consolidation
	echo $appName . "|" . $envCount . "|" . strlen($u4) . "|" . strlen($u7) . "|" . strlen($ul) . "|" . $lastId;
	`

	prog, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation pipeline 10 cibles: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution pipeline 10 cibles: %v", err)
	}

	expected := "LaravelPhp127|3|36|36|26|1"
	if strings.TrimSpace(out.String()) != expected {
		t.Fatalf("Sortie inattendue: %q, attendu %q", out.String(), expected)
	}
}
