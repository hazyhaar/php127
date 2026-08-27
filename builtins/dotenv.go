// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Implémentation native vlucas/phpdotenv et variables d'environnement

package builtins

import (
	"os"
	"strings"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/types"
)

var (
	envMu   sync.RWMutex
	envVars = make(map[string]string)
)

func init() {
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envVars[parts[0]] = parts[1]
		}
	}
}

// ParseDotenv analyse le contenu d'un fichier .env selon la spécification phpdotenv
func ParseDotenv(content string) map[string]string {
	res := make(map[string]string)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Supprimer les guillemets englobants si présents
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
			(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			if len(val) >= 2 {
				val = val[1 : len(val)-1]
			}
		}

		// Résolution de variables imbriquées ${VAR}
		for vKey, vVal := range res {
			val = strings.ReplaceAll(val, "${"+vKey+"}", vVal)
		}

		res[key] = val
		envMu.Lock()
		envVars[key] = val
		envMu.Unlock()
	}

	return res
}

func Getenv(keyVal types.Value) types.Value {
	key := keyVal.String()
	envMu.RLock()
	defer envMu.RUnlock()

	if val, ok := envVars[key]; ok {
		return types.NewString(val)
	}
	if val, ok := os.LookupEnv(key); ok {
		return types.NewString(val)
	}
	return types.NewBool(false)
}

func Putenv(settingVal types.Value) types.Value {
	setting := settingVal.String()
	parts := strings.SplitN(setting, "=", 2)
	if len(parts) == 2 {
		envMu.Lock()
		envVars[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		envMu.Unlock()
		return types.NewBool(true)
	}
	return types.NewBool(false)
}
