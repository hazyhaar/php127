// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/httpgateway: Cockpit de supervision et dashboard temps-réel souverain

package httpgateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"code.hazyhaar.fr/devhoros/php127/security"
)

type DashboardHandler struct {
	Hub *security.TelemetryHub
}

func NewDashboardHandler() *DashboardHandler {
	return &DashboardHandler{
		Hub: security.GetTelemetryHub(),
	}
}

func (dh *DashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/security/stream" {
		dh.serveEventStream(w, r)
		return
	}
	if r.URL.Path == "/api/security/stats" {
		dh.serveStats(w, r)
		return
	}
	dh.serveCockpitHTML(w, r)
}

func (dh *DashboardHandler) serveEventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming non supporté", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	eventsCh := dh.Hub.Subscribe()
	defer dh.Hub.Unsubscribe(eventsCh)

	notify := r.Context().Done()

	for {
		select {
		case <-notify:
			return
		case ev, ok := <-eventsCh:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err == nil {
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		case <-time.After(15 * time.Second):
			// Keep-alive ping
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (dh *DashboardHandler) serveStats(w http.ResponseWriter, r *http.Request) {
	total, vetoes := dh.Hub.Stats()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"total_events": total,
		"total_vetoes": vetoes,
		"status":       "operational",
		"mode":         "RASP_ENFORCE",
	})
}

func (dh *DashboardHandler) serveCockpitHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := `<!DOCTYPE html>
<html lang="fr">
<head>
    <meta charset="UTF-8">
    <title>php127 RASP Security Cockpit</title>
    <style>
        :root {
            --bg: #0d1117;
            --card-bg: #161b22;
            --border: #30363d;
            --text: #c9d1d9;
            --accent: #58a6ff;
            --danger: #f85149;
            --success: #3fb950;
            --warning: #d29922;
        }
        body {
            font-family: system-ui, -apple-system, sans-serif;
            background: var(--bg);
            color: var(--text);
            margin: 0;
            padding: 24px;
        }
        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            border-bottom: 1px solid var(--border);
            padding-bottom: 16px;
            margin-bottom: 24px;
        }
        .badge {
            background: rgba(63, 185, 80, 0.2);
            color: var(--success);
            border: 1px solid var(--success);
            padding: 4px 12px;
            border-radius: 4px;
            font-size: 14px;
        }
        .grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
            gap: 16px;
            margin-bottom: 24px;
        }
        .card {
            background: var(--card-bg);
            border: 1px solid var(--border);
            border-radius: 6px;
            padding: 16px;
        }
        .card h3 {
            margin-top: 0;
            color: var(--accent);
            font-size: 14px;
            text-transform: uppercase;
        }
        .metric {
            font-size: 32px;
            font-weight: bold;
            color: #fff;
        }
        .feed {
            background: var(--card-bg);
            border: 1px solid var(--border);
            border-radius: 6px;
            padding: 16px;
            height: 380px;
            overflow-y: auto;
            font-family: monospace;
            font-size: 13px;
        }
        .event-entry {
            border-bottom: 1px solid rgba(255,255,255,0.05);
            padding: 8px 0;
            display: flex;
            justify-content: space-between;
        }
        .event-type {
            color: var(--danger);
            font-weight: bold;
        }
        .event-src {
            color: var(--accent);
        }
    </style>
</head>
<body>
    <div class="header">
        <h1>php127 RASP Security Shield & Test Harness</h1>
        <div class="badge" id="status-badge">SHIELD ACTIF (ENFORCE)</div>
    </div>
    <div class="grid">
        <div class="card">
            <h3>Vetoes Bloquants</h3>
            <div class="metric" id="metric-vetoes" style="color: var(--danger);">0</div>
        </div>
        <div class="card">
            <h3>Événements Sécurité</h3>
            <div class="metric" id="metric-events">0</div>
        </div>
        <div class="card">
            <h3>Entropie Sortante Max (Q8.8)</h3>
            <div class="metric" id="metric-entropy" style="color: var(--warning);">0.0 / 8.0</div>
        </div>
        <div class="card">
            <h3>Allocations VM Résiduelles</h3>
            <div class="metric" style="color: var(--success);">0 B (ARCHTIME)</div>
        </div>
    </div>
    <div class="card">
        <h3>Flux d'Interception d'Attaques en Direct (SSE Push)</h3>
        <div class="feed" id="event-feed">
            <div style="color: #6e7681;">En attente d'événements de sécurité...</div>
        </div>
    </div>
    <script>
        let vetoCount = 0;
        let eventCount = 0;
        const feed = document.getElementById('event-feed');
        const vEl = document.getElementById('metric-vetoes');
        const eEl = document.getElementById('metric-events');
        const entEl = document.getElementById('metric-entropy');

        const es = new EventSource('/api/security/stream');
        es.onmessage = function(e) {
            const ev = JSON.parse(e.data);
            eventCount++;
            if (ev.event_type !== 7) vetoCount++;
            vEl.textContent = vetoCount;
            eEl.textContent = eventCount;
            if (ev.entropy_q8) {
                entEl.textContent = (ev.entropy_q8 / 256).toFixed(2) + ' / 8.0';
            }

            const row = document.createElement('div');
            row.className = 'event-entry';
            const dateStr = new Date(ev.timestamp_us / 1000).toLocaleTimeString();
            row.innerHTML = '<span><span style="color:#8b949e;">[' + dateStr + ']</span> <span class="event-type">' + ev.type_str + '</span> (<span class="event-src">' + ev.source + '</span>) : ' + ev.details + '</span>';
            feed.insertBefore(row, feed.firstChild);
        };
    </script>
</body>
</html>`
	w.Write([]byte(html))
}
