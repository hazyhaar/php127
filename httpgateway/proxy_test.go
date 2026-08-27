// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/httpgateway: Tests unitaires et d'adversarisation du ReverseProxyShield et Dashboard

package httpgateway

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/security"
)

func TestProxyShieldNominalForwarding(t *testing.T) {
	// Backend mock amont (simulant PHP-FPM ou application e-commerce)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","items":["book","laptop"]}`))
	}))
	defer backend.Close()

	shield, err := NewProxyShield(backend.URL, security.ModeEnforce)
	if err != nil {
		t.Fatalf("Erreur création ProxyShield: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/catalog/list?category=tech", nil)
	rec := httptest.NewRecorder()

	shield.ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Attendu status 200, obtenu %d", res.StatusCode)
	}

	body, _ := io.ReadAll(res.Body)
	if !bytes.Contains(body, []byte("laptop")) {
		t.Fatalf("Contenu de réponse inattendu: %s", string(body))
	}
	if res.Header.Get("X-php127-Shield") != "Enforced" {
		t.Fatalf("Header de garde X-php127-Shield manquant")
	}
}

func TestProxyShieldIngressCommandInjectionBlocked(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("SHOULD_NOT_BE_REACHED"))
	}))
	defer backend.Close()

	shield, err := NewProxyShield(backend.URL, security.ModeEnforce)
	if err != nil {
		t.Fatalf("Erreur création ProxyShield: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/search?q=cat%20/etc/passwd", nil)
	rec := httptest.NewRecorder()

	shield.ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("Attendu status 403 Forbidden sur injection de commande, obtenu %d", res.StatusCode)
	}
}

func TestProxyShieldEgressExfiltrationBlocked(t *testing.T) {
	// Backend compromis tentant de fuiter une clé privée SSH
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0Y5...\n-----END RSA PRIVATE KEY-----"))
	}))
	defer backend.Close()

	shield, err := NewProxyShield(backend.URL, security.ModeEnforce)
	if err != nil {
		t.Fatalf("Erreur création ProxyShield: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/debug/dump", nil)
	rec := httptest.NewRecorder()

	shield.ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("Attendu status 403 Forbidden sur exfiltration de clé, obtenu %d", res.StatusCode)
	}
	if res.Header.Get("X-php127-Security-Violation") != "EgressExfiltrationBlocked" {
		t.Fatalf("Header de violation manquant: %s", res.Header.Get("X-php127-Security-Violation"))
	}
}

func TestSecurityDashboardEndpoints(t *testing.T) {
	dh := NewDashboardHandler()

	// 1. Test HTML view
	reqHTML := httptest.NewRequest(http.MethodGet, "/", nil)
	recHTML := httptest.NewRecorder()
	dh.ServeHTTP(recHTML, reqHTML)
	if recHTML.Code != http.StatusOK || !bytes.Contains(recHTML.Body.Bytes(), []byte("php127 RASP")) {
		t.Fatalf("Vue cockpit HTML invalide (code=%d)", recHTML.Code)
	}

	// 2. Test JSON Stats
	reqStats := httptest.NewRequest(http.MethodGet, "/api/security/stats", nil)
	recStats := httptest.NewRecorder()
	dh.ServeHTTP(recStats, reqStats)
	if recStats.Code != http.StatusOK || !bytes.Contains(recStats.Body.Bytes(), []byte("RASP_ENFORCE")) {
		t.Fatalf("Endpoint /api/security/stats invalide (code=%d)", recStats.Code)
	}
}
