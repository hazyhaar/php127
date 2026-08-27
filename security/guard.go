// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/security: Couche méta de sécurité souveraine et garde mécanique sans altération du langage PHP

package security

import (
	"errors"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"github.com/hazyhaar/c2pkg/c2blueteam"
)

var (
	ErrSecurityTaintSinkViolation = errors.New("security: tentative d'exécution d'une entrée utilisateur non assainie (Taint Sink Violation)")
	ErrSecurityExfiltrationBlock  = errors.New("security: détection de fuite de données à haute entropie dans la réponse sortante (Anti-Exfiltration Block)")
	ErrSecurityCommandInjection   = errors.New("security: tentative d'injection de commande système (LOLBAS) interceptée")
	ErrSecurityPathTraversal      = errors.New("security: tentative de traversée de répertoire non autorisée")
	ErrSecurityCodeInjection      = errors.New("security: tentative d'injection de code dynamique (eval/assert/unserialize)")
	ErrSecuritySQLInjection       = errors.New("security: injection SQL potentielle via concaténation de variable contaminée non préparée")
)

type SecurityMode uint8

const (
	ModeAudit   SecurityMode = iota // Journalise les anomalies sans bloquer
	ModeEnforce                     // Bloque immédiatement et lève une exception de sécurité
)

type Guard struct {
	Mode              SecurityMode
	MaxAllowedEntropy uint32 // Seuil Q8.8 (ex: 7.2 * 256 = 1843)
	BlockedBinaries   []string
	AllowedDocRoots   []string
	AnomaliesCount    uint64
	SSRF              *SSRFGuard
}

func NewGuard(mode SecurityMode) *Guard {
	return &Guard{
		Mode:              mode,
		MaxAllowedEntropy: 1843, // 7.2 / 8.0 en Q8.8 (données chiffrées/clés)
		BlockedBinaries: []string{
			"sh", "bash", "zsh", "dash", "curl", "wget", "nc", "ncat", "netcat", "socat", "python", "python3", "perl", "ruby", "powershell", "cmd.exe", "cat", "rm", "whoami", "id",
		},
		AllowedDocRoots: []string{"."},
		SSRF:            NewSSRFGuard(),
	}
}

// IngressInspect : Inspecte une entrée externe (GET/POST/COOKIE) et lui applique son profil métrologique
func (g *Guard) IngressInspect(raw []byte) (types.Value, error) {
	if len(raw) == 0 {
		return types.NewString(""), nil
	}

	var entropyQ8 uint32
	var charMask uint32
	pClass := c2blueteam.C2bt_classify_payload(raw, uint64(len(raw)), &entropyQ8, &charMask)

	v := types.NewStringBytes(raw).WithTaint(types.FlagTaintedUserInput)

	// Détection de charge utile à très haute entropie suspecte
	if entropyQ8 >= g.MaxAllowedEntropy && len(raw) > 64 {
		v = v.WithTaint(types.FlagHighEntropy)
		g.AnomaliesCount++
		GetTelemetryHub().RecordEvent(EventHighEntropyEgressBlocked, "ingress", string(raw), entropyQ8, uint32(v.Flags))
	}

	// Détection de classes suspectes (obfuscation binaire / tokens inattendus)
	if pClass == 2 || pClass == 4 { // HEX ou CRYPTO_COMPRESSED
		v = v.WithTaint(types.FlagHighEntropy)
		g.AnomaliesCount++
	}

	// Détection d'injections de commandes
	s := string(raw)
	if g.isCommandInjection(s) {
		g.AnomaliesCount++
		GetTelemetryHub().RecordEvent(EventCommandInjectionBlocked, "ingress", s, entropyQ8, uint32(v.Flags))
		if g.Mode == ModeEnforce {
			return v, ErrSecurityCommandInjection
		}
	}

	return v, nil
}

// CheckSink : Vérifie si une valeur peut être passée à une primitive système critique
func (g *Guard) CheckSink(sinkName string, arg types.Value) error {
	if (arg.Flags & types.FlagTrustedSystem) != 0 {
		return nil
	}

	if arg.IsTainted() {
		g.AnomaliesCount++
		GetTelemetryHub().RecordEvent(EventTaintSinkViolation, sinkName, arg.String(), 0, uint32(arg.Flags))
		if g.Mode == ModeEnforce {
			return ErrSecurityTaintSinkViolation
		}
	}

	switch sinkName {
	case "exec", "system", "passthru", "proc_open", "shell_exec", "popen":
		cmdStr := arg.String()
		if g.isCommandInjection(cmdStr) {
			g.AnomaliesCount++
			GetTelemetryHub().RecordEvent(EventCommandInjectionBlocked, sinkName, cmdStr, 0, uint32(arg.Flags))
			if g.Mode == ModeEnforce {
				return ErrSecurityCommandInjection
			}
		}

	case "file_get_contents", "fopen", "include", "include_once", "require", "require_once", "file_put_contents", "file", "readfile", "unlink", "rmdir":
		pathStr := arg.String()
		if strings.Contains(pathStr, "../") || strings.Contains(pathStr, "..\\") || strings.HasPrefix(pathStr, "/etc/") || strings.HasPrefix(pathStr, "/root/") {
			g.AnomaliesCount++
			GetTelemetryHub().RecordEvent(EventPathTraversalBlocked, sinkName, pathStr, 0, uint32(arg.Flags))
			if g.Mode == ModeEnforce {
				return ErrSecurityPathTraversal
			}
		}
		// SSRF check for URLs in file primitives
		if strings.HasPrefix(pathStr, "http://") || strings.HasPrefix(pathStr, "https://") {
			if err := g.CheckSSRF(pathStr); err != nil {
				return err
			}
		}

	case "eval", "assert", "create_function", "unserialize":
		if arg.IsTainted() || (arg.Flags&types.FlagTrustedSystem) == 0 {
			g.AnomaliesCount++
			GetTelemetryHub().RecordEvent(EventCodeInjectionBlocked, sinkName, arg.String(), 0, uint32(arg.Flags))
			if g.Mode == ModeEnforce {
				return ErrSecurityCodeInjection
			}
		}
	}

	return nil
}

// CheckSQL inspecte les requêtes SQL brutes avant exécution directe (non préparée)
func (g *Guard) CheckSQL(rawQuery string, isTainted bool) error {
	if isTainted {
		g.AnomaliesCount++
		GetTelemetryHub().RecordEvent(EventSQLiBlocked, "pdo", rawQuery, 0, uint32(types.FlagTaintedUserInput))
		if g.Mode == ModeEnforce {
			return ErrSecuritySQLInjection
		}
	}

	// Détection heuristique d'injections SQL classiques dans des requêtes non paramétrées
	upper := strings.ToUpper(rawQuery)
	if strings.Contains(upper, "UNION SELECT") ||
		strings.Contains(upper, "' OR 1=1") ||
		strings.Contains(upper, "\" OR 1=1") ||
		strings.Contains(upper, "' OR '1'='1") ||
		strings.Contains(upper, "1; DROP TABLE") ||
		strings.Contains(upper, "SLEEP(") ||
		strings.Contains(upper, "BENCHMARK(") {
		g.AnomaliesCount++
		GetTelemetryHub().RecordEvent(EventSQLiBlocked, "pdo_heuristic", rawQuery, 0, 0)
		if g.Mode == ModeEnforce {
			return ErrSecuritySQLInjection
		}
	}
	return nil
}

// CheckSSRF valide une destination réseau pour cURL ou les flux de connexion externes
func (g *Guard) CheckSSRF(targetURL string) error {
	if g.SSRF == nil {
		g.SSRF = NewSSRFGuard()
	}
	if err := g.SSRF.InspectURL(targetURL); err != nil {
		g.AnomaliesCount++
		GetTelemetryHub().RecordEvent(EventSSRFBlocked, "ssrf_guard", targetURL, 0, 0)
		if g.Mode == ModeEnforce {
			return err
		}
	}
	return nil
}

// CheckForeignSink vérifie les arguments avant passage à un worker externe
func (g *Guard) CheckForeignSink(funcName string, args []types.Value) error {
	lower := strings.ToLower(funcName)
	for _, arg := range args {
		if arg.IsTainted() {
			if g.isDangerousForeignSink(lower) {
				g.AnomaliesCount++
				GetTelemetryHub().RecordEvent(EventTaintSinkViolation, "foreign:"+funcName, arg.String(), 0, uint32(arg.Flags))
				if g.Mode == ModeEnforce {
					return ErrSecurityTaintSinkViolation
				}
			}
		}
	}
	return nil
}

func (g *Guard) isDangerousForeignSink(funcName string) bool {
	switch funcName {
	case "exec", "system", "passthru", "shell_exec", "proc_open", "popen", "pcntl_exec", "dl",
		"eval", "assert", "unserialize", "create_function", "preg_replace_callback",
		"ldap_bind", "ldap_search", "mail", "mb_send_mail":
		return true
	}
	return false
}

// EgressInspect : Inspecte les données sortantes avant émission vers le client HTTP
func (g *Guard) EgressInspect(output []byte) error {
	if len(output) < 32 {
		return nil
	}

	var entropyQ8 uint32
	var charMask uint32
	pClass := c2blueteam.C2bt_classify_payload(output, uint64(len(output)), &entropyQ8, &charMask)

	// Détection de clés privées textuelles
	outStr := string(output)
	if strings.Contains(outStr, "-----BEGIN PRIVATE KEY-----") ||
		strings.Contains(outStr, "-----BEGIN RSA PRIVATE KEY-----") ||
		strings.Contains(outStr, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		g.AnomaliesCount++
		GetTelemetryHub().RecordEvent(EventHighEntropyEgressBlocked, "egress:pem_leak", "Private key detected in output", entropyQ8, 0)
		if g.Mode == ModeEnforce {
			return ErrSecurityExfiltrationBlock
		}
	}

	// Si un flux sortant non prévu ressemble à une clé privée ou un blob chiffré brut
	if (pClass == 4 || entropyQ8 >= g.MaxAllowedEntropy) && len(output) > 128 {
		g.AnomaliesCount++
		GetTelemetryHub().RecordEvent(EventHighEntropyEgressBlocked, "egress:entropy", "High entropy payload detected in output", entropyQ8, 0)
		if g.Mode == ModeEnforce {
			return ErrSecurityExfiltrationBlock
		}
	}

	return nil
}

func (g *Guard) isCommandInjection(s string) bool {
	lower := strings.ToLower(s)

	// Command substitution patterns $(...) or `...`
	if (strings.Contains(s, "$(") && strings.Contains(s, ")")) || (strings.Count(s, "`") >= 2) {
		return true
	}

	for _, bin := range g.BlockedBinaries {
		if strings.Contains(lower, bin+" ") ||
			strings.Contains(lower, "/bin/"+bin) ||
			strings.Contains(lower, "/usr/bin/"+bin) ||
			lower == bin ||
			strings.HasSuffix(lower, " "+bin) ||
			strings.HasSuffix(lower, "/"+bin) {
			return true
		}
	}

	if strings.Contains(s, ";") || strings.Contains(s, "|") || strings.Contains(s, "`") || strings.Contains(s, "$(") || strings.Contains(s, "&&") || strings.Contains(s, "||") {
		for _, bin := range g.BlockedBinaries {
			if strings.Contains(lower, " "+bin) || strings.Contains(lower, "/"+bin) || strings.HasPrefix(lower, bin) {
				return true
			}
		}
		if strings.Contains(s, "||") || strings.Contains(s, "&&") || strings.Contains(s, "|") {
			return true
		}
	}
	return false
}
