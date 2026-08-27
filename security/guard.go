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
	ErrSecurityExfiltrationBlock  = errors.New("security: détection de fuite de données à haute entropie dans la réponse sortante")
	ErrSecurityCommandInjection   = errors.New("security: tentative d'injection de commande système (LOLBAS) interceptée")
	ErrSecurityPathTraversal      = errors.New("security: tentative de traversée de répertoire non autorisée")
	ErrSecurityCodeInjection      = errors.New("security: tentative d'injection de code dynamique (eval/assert/unserialize)")
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
}

func NewGuard(mode SecurityMode) *Guard {
	return &Guard{
		Mode:              mode,
		MaxAllowedEntropy: 1843, // 7.2 / 8.0 en Q8.8 (données chiffrées/clés)
		BlockedBinaries: []string{
			"sh", "bash", "zsh", "dash", "curl", "wget", "nc", "ncat", "netcat", "socat", "python", "perl", "ruby", "powershell", "cmd.exe",
		},
		AllowedDocRoots: []string{"."},
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
		if g.Mode == ModeEnforce {
			return v, ErrSecurityCommandInjection
		}
	}

	return v, nil
}

// CheckSink : Vérifie si une valeur peut être passée à une primitive système critique
func (g *Guard) CheckSink(sinkName string, arg types.Value) error {
	if arg.IsTainted() {
		g.AnomaliesCount++
		if g.Mode == ModeEnforce {
			return ErrSecurityTaintSinkViolation
		}
	}

	switch sinkName {
	case "exec", "system", "passthru", "proc_open", "shell_exec", "popen":
		cmdStr := arg.String()
		if g.isCommandInjection(cmdStr) {
			g.AnomaliesCount++
			if g.Mode == ModeEnforce {
				return ErrSecurityCommandInjection
			}
		}

	case "file_get_contents", "fopen", "include", "include_once", "require", "require_once", "file_put_contents", "file", "readfile", "unlink", "rmdir":
		pathStr := arg.String()
		if strings.Contains(pathStr, "../") || strings.Contains(pathStr, "..\\") || strings.HasPrefix(pathStr, "/etc/") || strings.HasPrefix(pathStr, "/root/") {
			g.AnomaliesCount++
			if g.Mode == ModeEnforce {
				return ErrSecurityPathTraversal
			}
		}

	case "eval", "assert", "create_function", "unserialize":
		if arg.IsTainted() || (arg.Flags&types.FlagTrustedSystem) == 0 {
			g.AnomaliesCount++
			if g.Mode == ModeEnforce {
				return ErrSecurityCodeInjection
			}
		}
	}

	return nil
}

// EgressInspect : Inspecte les données sortantes avant émission vers le client HTTP
func (g *Guard) EgressInspect(output []byte) error {
	if len(output) < 32 {
		return nil
	}

	var entropyQ8 uint32
	var charMask uint32
	pClass := c2blueteam.C2bt_classify_payload(output, uint64(len(output)), &entropyQ8, &charMask)

	// Si un flux sortant non prévu ressemble à une clé privée ou un blob chiffré brut
	if (pClass == 4 || entropyQ8 >= g.MaxAllowedEntropy) && len(output) > 128 {
		g.AnomaliesCount++
		if g.Mode == ModeEnforce {
			return ErrSecurityExfiltrationBlock
		}
	}

	return nil
}

func (g *Guard) isCommandInjection(s string) bool {
	lower := strings.ToLower(s)
	// Détection des binaires prohibés en position de commande
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
	// Détection des métacaractères d'injection shell suspects combinés avec binaire
	if strings.Contains(s, ";") || strings.Contains(s, "|") || strings.Contains(s, "`") || strings.Contains(s, "$(") || strings.Contains(s, "&&") || strings.Contains(s, "||") {
		for _, bin := range g.BlockedBinaries {
			if strings.Contains(lower, " "+bin) || strings.Contains(lower, "/"+bin) || strings.HasPrefix(lower, bin) {
				return true
			}
		}
	}
	return false
}
