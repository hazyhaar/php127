// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/httpgateway: Tests unitaires des Presets Métier et de la normalisation

package httpgateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/security"
)

func TestPresetsWordPressProtection(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK_BACKEND"))
	}))
	defer backend.Close()

	shield, err := NewProxyShieldWithPreset(backend.URL, security.ModeEnforce, PresetWordPress)
	if err != nil {
		t.Fatalf("Erreur création ProxyShield WordPress: %v", err)
	}

	// 1. Blocage accès direct à wp-config.php
	req1 := httptest.NewRequest(http.MethodGet, "/wp-config.php", nil)
	rec1 := httptest.NewRecorder()
	shield.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusForbidden {
		t.Fatalf("Attendu 403 sur /wp-config.php, obtenu %d", rec1.Code)
	}

	// 2. Blocage exécution PHP dans uploads
	req2 := httptest.NewRequest(http.MethodGet, "/wp-content/uploads/2026/08/backdoor.php", nil)
	rec2 := httptest.NewRecorder()
	shield.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("Attendu 403 sur backdoor.php dans uploads, obtenu %d", rec2.Code)
	}

	// 3. Autorisation requête légitime sur article
	req3 := httptest.NewRequest(http.MethodGet, "/2026/08/mon-premier-article/", nil)
	rec3 := httptest.NewRecorder()
	shield.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("Faux positif sur URL légitime (code=%d)", rec3.Code)
	}
}

func TestPresetsLaravelProtection(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK_LARAVEL"))
	}))
	defer backend.Close()

	shield, err := NewProxyShieldWithPreset(backend.URL, security.ModeEnforce, PresetLaravel)
	if err != nil {
		t.Fatalf("Erreur création ProxyShield Laravel: %v", err)
	}

	// Blocage accès direct au fichier .env
	req := httptest.NewRequest(http.MethodGet, "/.env", nil)
	rec := httptest.NewRecorder()
	shield.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Attendu 403 sur /.env, obtenu %d", rec.Code)
	}
}

func TestProxyShieldAuditModeDoesNotBreakTraffic(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("FORWARDED_TO_BACKEND"))
	}))
	defer backend.Close()

	shield, err := NewProxyShieldWithPreset(backend.URL, security.ModeAudit, PresetWordPress)
	if err != nil {
		t.Fatalf("Erreur création ProxyShield en mode Audit: %v", err)
	}

	// Requête suspecte en mode Audit -> Passe au backend avec header X-php127-Would-Block
	req := httptest.NewRequest(http.MethodGet, "/wp-config.php", nil)
	rec := httptest.NewRecorder()
	shield.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Le mode Audit ne doit pas bloquer (attendu 200, obtenu %d)", rec.Code)
	}
	if rec.Header().Get("X-php127-Would-Block") != "SensitiveFileAccess" {
		t.Fatalf("Header X-php127-Would-Block manquant en mode Audit: %s", rec.Header().Get("X-php127-Would-Block"))
	}
}

func TestNormalizeInputRecursiveDecoding(t *testing.T) {
	// Double encodage URL
	doubleEncoded := "%252e%252e%252fetc%252fpasswd"
	normalized := NormalizeInput(doubleEncoded)
	if normalized != "../etc/passwd" {
		t.Fatalf("Décodage récursif échoué: attendu '../etc/passwd', obtenu %q", normalized)
	}

	// Octet nul
	nullByteStr := "avatar.png\x00.php"
	if NormalizeInput(nullByteStr) != "avatar.png.php" {
		t.Fatalf("Suppression octet nul échouée: %q", NormalizeInput(nullByteStr))
	}
}

func TestExtractStructuredFieldsJSON(t *testing.T) {
	jsonBody := []byte(`{"user":"admin","action":"search","filters":{"query":"tech","limit":10}}`)
	fields := ExtractStructuredFields("application/json", jsonBody)

	foundQuery := false
	for _, f := range fields {
		if f == "tech" {
			foundQuery = true
			break
		}
	}
	if !foundQuery {
		t.Fatalf("Champ structuré 'tech' non extrait du JSON: %v", fields)
	}
}
