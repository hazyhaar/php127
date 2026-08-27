// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/httpgateway: Presets métier (WordPress, Laravel, Symfony, etc.) et normalisation d'entrées

package httpgateway

import (
	"encoding/json"
	"net/url"
	"strings"
)

type PresetType string

const (
	PresetWordPress  PresetType = "wordpress"
	PresetLaravel    PresetType = "laravel"
	PresetSymfony    PresetType = "symfony"
	PresetPrestaShop PresetType = "prestashop"
	PresetGeneric    PresetType = "generic"
)

type SecurityPreset struct {
	Name              PresetType
	BlockedPaths      []string // Chemins strictement interdits au public
	UploadPaths       []string // Dossiers d'upload (exécution de scripts interdite)
	SensitiveFiles    []string // Fichiers de configuration / logs protégés
	AllowedMethods    []string // Méthodes HTTP autorisées
	MaxBodySizeBytes  int64    // Taille max de corps par défaut
	BlockPhpInUploads bool
	DisableXmlRpc     bool
}

func GetPreset(preset PresetType) SecurityPreset {
	switch preset {
	case PresetWordPress:
		return SecurityPreset{
			Name: PresetWordPress,
			BlockedPaths: []string{
				"/wp-includes/",
				"/wp-content/debug.log",
				"/.git",
				"/wp-config.php.bak",
			},
			UploadPaths: []string{
				"/wp-content/uploads/",
			},
			SensitiveFiles: []string{
				"wp-config.php",
				"readme.html",
				"license.txt",
			},
			AllowedMethods:    []string{"GET", "POST", "HEAD", "OPTIONS"},
			MaxBodySizeBytes:  32 * 1024 * 1024, // 32 Mo
			BlockPhpInUploads: true,
			DisableXmlRpc:     true,
		}

	case PresetLaravel:
		return SecurityPreset{
			Name: PresetLaravel,
			BlockedPaths: []string{
				"/.env",
				"/storage/logs/",
				"/bootstrap/cache/",
				"/.git",
			},
			UploadPaths: []string{
				"/storage/app/public/",
			},
			SensitiveFiles: []string{
				".env",
				"composer.json",
				"composer.lock",
				"artisan",
			},
			AllowedMethods:    []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			MaxBodySizeBytes:  16 * 1024 * 1024, // 16 Mo
			BlockPhpInUploads: true,
		}

	case PresetSymfony:
		return SecurityPreset{
			Name: PresetSymfony,
			BlockedPaths: []string{
				"/.env",
				"/var/log/",
				"/var/cache/",
				"/config/",
				"/.git",
			},
			UploadPaths: []string{
				"/public/uploads/",
			},
			SensitiveFiles: []string{
				".env",
				"composer.json",
			},
			AllowedMethods:    []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			MaxBodySizeBytes:  16 * 1024 * 1024,
			BlockPhpInUploads: true,
		}

	case PresetPrestaShop:
		return SecurityPreset{
			Name: PresetPrestaShop,
			BlockedPaths: []string{
				"/app/",
				"/config/",
				"/tools/",
				"/translations/",
				"/var/logs/",
				"/.git",
			},
			UploadPaths: []string{
				"/img/",
				"/upload/",
				"/download/",
			},
			SensitiveFiles: []string{
				"parameters.php",
			},
			AllowedMethods:    []string{"GET", "POST", "HEAD", "OPTIONS"},
			MaxBodySizeBytes:  32 * 1024 * 1024,
			BlockPhpInUploads: true,
		}

	default: // Generic
		return SecurityPreset{
			Name: PresetGeneric,
			BlockedPaths: []string{
				"/.env",
				"/.git",
				"/.svn",
				"/.htaccess",
				"/.htpasswd",
			},
			UploadPaths: []string{
				"/uploads/",
			},
			SensitiveFiles: []string{
				".env",
				"config.php",
				"database.php",
			},
			AllowedMethods:    []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			MaxBodySizeBytes:  10 * 1024 * 1024,
			BlockPhpInUploads: true,
		}
	}
}

// NormalizeInput applique un décodage itératif et assainit les encodages malicieux
func NormalizeInput(raw string) string {
	res := raw

	// 1. Décodage d'URL récursif (jusqu'à 3 passes pour neutraliser les doubles encodages %252e)
	for i := 0; i < 3; i++ {
		if unescaped, err := url.QueryUnescape(res); err == nil && unescaped != res {
			res = unescaped
		} else {
			break
		}
	}

	// 2. Suppression des octets nuls (\0)
	res = strings.ReplaceAll(res, "\x00", "")

	// 3. Normalisation des séparateurs Unicode (pleine chasse / homoglyphes)
	res = strings.ReplaceAll(res, "／", "/")
	res = strings.ReplaceAll(res, "＼", "\\")
	res = strings.ReplaceAll(res, "．", ".")

	return res
}

// ExtractStructuredFields extrait récursivement les valeurs textuelles de formulaires ou payloads JSON
func ExtractStructuredFields(contentType string, body []byte) []string {
	var fields []string
	if len(body) == 0 {
		return fields
	}

	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "application/x-www-form-urlencoded") {
		vals, err := url.ParseQuery(string(body))
		if err == nil {
			for k, vList := range vals {
				fields = append(fields, k)
				fields = append(fields, vList...)
			}
		}
	} else if strings.Contains(ct, "application/json") {
		var jsonData interface{}
		if err := json.Unmarshal(body, &jsonData); err == nil {
			extractJSONStrings(jsonData, &fields)
		}
	} else {
		// Corps générique brut
		fields = append(fields, string(body))
	}

	return fields
}

func extractJSONStrings(data interface{}, out *[]string) {
	switch v := data.(type) {
	case string:
		*out = append(*out, v)
	case []interface{}:
		for _, item := range v {
			extractJSONStrings(item, out)
		}
	case map[string]interface{}:
		for k, val := range v {
			*out = append(*out, k)
			extractJSONStrings(val, out)
		}
	}
}
