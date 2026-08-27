// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/httpgateway: Bouclier Reverse-Proxy HTTP & FastCGI transparent sans allocation résiduelle

package httpgateway

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/security"
	"code.hazyhaar.fr/devhoros/php127/types"
)

type ProxyShield struct {
	TargetURL    *url.URL
	Guard        *security.Guard
	ReverseProxy *httputil.ReverseProxy
	BufferPool   sync.Pool
}

func NewProxyShield(target string, mode security.SecurityMode) (*ProxyShield, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	guard := security.NewGuard(mode)
	ps := &ProxyShield{
		TargetURL: u,
		Guard:     guard,
	}

	ps.BufferPool.New = func() interface{} {
		return new(bytes.Buffer)
	}

	rp := httputil.NewSingleHostReverseProxy(u)
	rp.Transport = &http.Transport{
		MaxIdleConns:       100,
		IdleConnTimeout:    90 * time.Second,
		DisableCompression: true,
	}

	// Modification de la réponse sortante pour inspection de sécurité RASP
	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.Body == nil {
			return nil
		}
		bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 Mo max
		_ = resp.Body.Close()
		if err != nil {
			return err
		}

		// Inspection Anti-Exfiltration Egress
		if err := guard.EgressInspect(bodyBytes); err != nil {
			// Substitution du corps par un message de blocage sécurisé
			blockedBody := []byte("<!DOCTYPE html><html><body><h1>403 Forbidden</h1><p>php127 RASP: Exfiltration de données bloquée.</p></body></html>")
			resp.Body = io.NopCloser(bytes.NewReader(blockedBody))
			resp.ContentLength = int64(len(blockedBody))
			resp.StatusCode = http.StatusForbidden
			resp.Header.Set("X-php127-Security-Violation", "EgressExfiltrationBlocked")
			return nil
		}

		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		resp.ContentLength = int64(len(bodyBytes))
		resp.Header.Set("X-php127-Shield", "Enforced")
		return nil
	}

	ps.ReverseProxy = rp
	return ps, nil
}

func (ps *ProxyShield) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Ingress Inspection : URL Query
	if r.URL.RawQuery != "" {
		unescaped, _ := url.QueryUnescape(r.URL.RawQuery)
		if _, err := ps.Guard.IngressInspect([]byte(r.URL.RawQuery)); err != nil {
			http.Error(w, "Requête bloquée par php127 Shield (Injection/Menace détectée)", http.StatusForbidden)
			return
		}
		if unescaped != r.URL.RawQuery {
			if _, err := ps.Guard.IngressInspect([]byte(unescaped)); err != nil {
				http.Error(w, "Requête bloquée par php127 Shield (Injection/Menace détectée)", http.StatusForbidden)
				return
			}
		}
	}

	// 2. Ingress Inspection : En-têtes et Cookies suspects
	for k, vals := range r.Header {
		for _, v := range vals {
			if _, err := ps.Guard.IngressInspect([]byte(k + ": " + v)); err != nil {
				http.Error(w, "En-tête prohibé bloqué par php127 Shield", http.StatusForbidden)
				return
			}
		}
	}

	// 3. Ingress Inspection : Corps de requête (POST/PUT/PATCH)
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) {
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		_ = r.Body.Close()
		if err != nil {
			http.Error(w, "Erreur lecture du corps de requête", http.StatusBadRequest)
			return
		}

		if len(bodyBytes) > 0 {
			if _, err := ps.Guard.IngressInspect(bodyBytes); err != nil {
				http.Error(w, "Corps de requête bloqué par php127 Shield", http.StatusForbidden)
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// 4. Délégation transparente vers le serveur amont (PHP-FPM ou Backend HTTP)
	ps.ReverseProxy.ServeHTTP(w, r)
}

// InspectValue applique le profil de contamination Taint
func (ps *ProxyShield) InspectValue(raw []byte) types.Value {
	v, _ := ps.Guard.IngressInspect(raw)
	return v
}
