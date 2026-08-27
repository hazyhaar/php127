// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/httpgateway: Bouclier Reverse-Proxy HTTP/FastCGI avec Presets Métier et modes Audit/Enforce

package httpgateway

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/security"
	"code.hazyhaar.fr/devhoros/php127/types"
)

type ProxyShield struct {
	TargetURL    *url.URL
	Guard        *security.Guard
	Preset       SecurityPreset
	ReverseProxy *httputil.ReverseProxy
	BufferPool   sync.Pool
}

func NewProxyShield(target string, mode security.SecurityMode) (*ProxyShield, error) {
	return NewProxyShieldWithPreset(target, mode, PresetGeneric)
}

func NewProxyShieldWithPreset(target string, mode security.SecurityMode, presetType PresetType) (*ProxyShield, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	guard := security.NewGuard(mode)
	preset := GetPreset(presetType)

	ps := &ProxyShield{
		TargetURL: u,
		Guard:     guard,
		Preset:    preset,
	}

	ps.BufferPool.New = func() interface{} {
		return new(bytes.Buffer)
	}

	rp := httputil.NewSingleHostReverseProxy(u)
	rp.Transport = &http.Transport{
		MaxIdleConns:       200,
		IdleConnTimeout:    90 * time.Second,
		DisableCompression: true,
	}

	// Inspection de sécurité RASP sur la réponse sortante (Egress)
	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.Body == nil {
			return nil
		}

		ct := strings.ToLower(resp.Header.Get("Content-Type"))

		// Exclusion des types médias et binaires légitimes à haute entropie (images, pdf, fonts, zip)
		isBinaryMedia := strings.HasPrefix(ct, "image/") ||
			strings.HasPrefix(ct, "font/") ||
			strings.HasPrefix(ct, "video/") ||
			strings.HasPrefix(ct, "audio/") ||
			strings.Contains(ct, "application/pdf") ||
			strings.Contains(ct, "application/zip") ||
			strings.Contains(ct, "application/gzip") ||
			strings.Contains(ct, "application/octet-stream")

		// Lecture bornée à 64 Ko pour inspection sans saturation RAM
		sampleLimit := int64(64 * 1024)
		headBuf := make([]byte, sampleLimit)
		n, _ := io.ReadFull(resp.Body, headBuf)
		sample := headBuf[:n]

		// Reconstitution du flux sortant
		restReader := io.MultiReader(bytes.NewReader(sample), resp.Body)
		resp.Body = io.NopCloser(restReader)

		// 1. Détection ciblée de fuite de clé privée / certificat
		if bytes.Contains(sample, []byte("-----BEGIN")) && (bytes.Contains(sample, []byte("PRIVATE KEY-----")) || bytes.Contains(sample, []byte("RSA PRIVATE KEY-----"))) {
			security.GetTelemetryHub().RecordEvent(security.EventHighEntropyEgressBlocked, "egress_proxy", "Fuite de clé privée PEM interceptée", 0, 0)
			if mode == security.ModeEnforce {
				blockedBody := []byte("<!DOCTYPE html><html><body><h1>403 Forbidden</h1><p>php127 RASP: Exfiltration de secret bloquée.</p></body></html>")
				resp.Body = io.NopCloser(bytes.NewReader(blockedBody))
				resp.ContentLength = int64(len(blockedBody))
				resp.StatusCode = http.StatusForbidden
				resp.Header.Set("X-php127-Security-Violation", "EgressExfiltrationBlocked")
				return nil
			}
			resp.Header.Set("X-php127-Would-Block", "EgressKeyLeakDetected")
		}

		// 2. Inspection d'entropie uniquement sur réponses textuelles/HTML/JSON
		if !isBinaryMedia && len(sample) >= 128 {
			if err := guard.EgressInspect(sample); err != nil {
				if mode == security.ModeEnforce {
					blockedBody := []byte("<!DOCTYPE html><html><body><h1>403 Forbidden</h1><p>php127 RASP: Exfiltration anormale bloquée.</p></body></html>")
					resp.Body = io.NopCloser(bytes.NewReader(blockedBody))
					resp.ContentLength = int64(len(blockedBody))
					resp.StatusCode = http.StatusForbidden
					resp.Header.Set("X-php127-Security-Violation", "EgressExfiltrationBlocked")
					return nil
				}
				resp.Header.Set("X-php127-Would-Block", "EgressHighEntropyBlocked")
			}
		}

		resp.Header.Set("X-php127-Shield", "Enforced")
		return nil
	}

	ps.ReverseProxy = rp
	return ps, nil
}

func (ps *ProxyShield) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cleanPath := NormalizeInput(r.URL.Path)

	// 1. Contrôle des méthodes HTTP autorisées
	if len(ps.Preset.AllowedMethods) > 0 {
		allowed := false
		for _, m := range ps.Preset.AllowedMethods {
			if strings.EqualFold(r.Method, m) {
				allowed = true
				break
			}
		}
		if !allowed {
			ps.handleViolation(w, r, "MethodNotAllowed", "Méthode HTTP non autorisée")
			return
		}
	}

	// 2. Contrôle d'accès aux chemins sensibles du preset (ex: .env, wp-config.php)
	for _, blocked := range ps.Preset.BlockedPaths {
		if strings.HasPrefix(cleanPath, blocked) || cleanPath == blocked {
			ps.handleViolation(w, r, "BlockedPath", "Accès à un chemin sensible interdit")
			return
		}
	}

	baseFile := path.Base(cleanPath)
	for _, sensitive := range ps.Preset.SensitiveFiles {
		if strings.EqualFold(baseFile, sensitive) {
			ps.handleViolation(w, r, "SensitiveFileAccess", "Accès à un fichier protégé interdit")
			return
		}
	}

	// 3. Interdiction de l'exécution de scripts dans les dossiers d'upload
	if ps.Preset.BlockPhpInUploads {
		for _, uploadDir := range ps.Preset.UploadPaths {
			if strings.HasPrefix(cleanPath, uploadDir) {
				ext := strings.ToLower(path.Ext(cleanPath))
				if ext == ".php" || ext == ".phtml" || ext == ".phar" || ext == ".php5" || ext == ".php7" || ext == ".php8" {
					ps.handleViolation(w, r, "PhpExecutionInUploads", "Exécution de script interdite dans le dossier d'upload")
					return
				}
			}
		}
	}

	// 4. Ingress Inspection structurée sur les paramètres de requête (Query)
	if r.URL.RawQuery != "" {
		normalizedQuery := NormalizeInput(r.URL.RawQuery)
		parsedQuery, _ := url.ParseQuery(normalizedQuery)
		for k, vals := range parsedQuery {
			if _, err := ps.Guard.IngressInspect([]byte(k)); err != nil {
				ps.handleViolation(w, r, "SuspiciousQueryParam", "Paramètre de requête prohibé")
				return
			}
			for _, v := range vals {
				if _, err := ps.Guard.IngressInspect([]byte(v)); err != nil {
					ps.handleViolation(w, r, "SuspiciousQueryValue", "Valeur de requête prohibée")
					return
				}
			}
		}
	}

	// 5. Ingress Inspection structurée sur le corps de requête (POST/PUT/PATCH)
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) {
		maxBody := ps.Preset.MaxBodySizeBytes
		if maxBody <= 0 {
			maxBody = 10 * 1024 * 1024
		}

		// Inspection bornée des premiers 64 Ko pour ne pas stocker 10 Mo en mémoire
		inspectLimit := int64(64 * 1024)
		if maxBody < inspectLimit {
			inspectLimit = maxBody
		}

		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, inspectLimit))
		_ = r.Body.Close()
		if err != nil {
			http.Error(w, "Erreur lecture du corps", http.StatusBadRequest)
			return
		}

		ct := r.Header.Get("Content-Type")
		fields := ExtractStructuredFields(ct, bodyBytes)
		for _, f := range fields {
			normalized := NormalizeInput(f)
			if _, err := ps.Guard.IngressInspect([]byte(normalized)); err != nil {
				ps.handleViolation(w, r, "SuspiciousBodyPayload", "Payload de corps prohibé")
				return
			}
		}

		// Reconstitution du body pour le serveur amont
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(bodyBytes), r.Body))
	}

	// 6. Délégation vers le serveur amont
	ps.ReverseProxy.ServeHTTP(w, r)
}

func (ps *ProxyShield) handleViolation(w http.ResponseWriter, r *http.Request, violationType, message string) {
	security.GetTelemetryHub().RecordEvent(security.EventCommandInjectionBlocked, "proxy_shield", violationType+": "+cleanForLog(r.URL.Path), 0, 0)

	if ps.Guard.Mode == security.ModeEnforce {
		http.Error(w, "Requête bloquée par php127 Shield ("+message+")", http.StatusForbidden)
		return
	}

	// Mode Audit : Ajout de l'en-tête de signalement sans interruption du flux
	w.Header().Set("X-php127-Would-Block", violationType)
	ps.ReverseProxy.ServeHTTP(w, r)
}

func cleanForLog(s string) string {
	if len(s) > 128 {
		return s[:128] + "..."
	}
	return s
}

func (ps *ProxyShield) InspectValue(raw []byte) types.Value {
	v, _ := ps.Guard.IngressInspect(raw)
	return v
}
