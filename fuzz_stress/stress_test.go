package fuzz_stress

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/sapi"
	"code.hazyhaar.fr/devhoros/php127/vm"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const stressWorkloadPHPSource = `<?php
$reqId = isset($_GET['req_id']) ? (int)$_GET['req_id'] : 0;
$workerId = isset($_GET['worker']) ? (int)$_GET['worker'] : 0;
$payload = isset($_POST['payload']) ? (string)$_POST['payload'] : 'none';

class OrderLine {
    public $sku;
    public $qty;
    public $unitPrice;
    public function __construct($sku, $qty, $unitPrice) {
        $this->sku = $sku;
        $this->qty = $qty;
        $this->unitPrice = $unitPrice;
    }
    public function total() {
        return $this->qty * $this->unitPrice;
    }
}

$taxRate = 20;
$applyTax = fn($amount) => $amount + intdiv($amount * $taxRate, 100);

$skuTag = 'SKU-0';
if (preg_match('/^([A-Z]+-\d+):(\d+)$/', $payload, $m)) {
    $skuTag = $m[1];
    $qtyVal = (int)$m[2];
} else {
    $qtyVal = 1;
}

$line1 = new OrderLine($skuTag, $qtyVal, 100);
$line2 = new OrderLine('SKU-BASE', 2, 50);

$pdo = new PDO('sqlite::memory:');
$pdo->exec('CREATE TABLE items (id INTEGER PRIMARY KEY, sku TEXT, total INTEGER)');
$pdo->beginTransaction();
$stmt = $pdo->prepare('INSERT INTO items (sku, total) VALUES (?, ?)');
$stmt->execute([$line1->sku, $line1->total()]);
$stmt->execute([$line2->sku, $line2->total()]);
$pdo->commit();

$q = $pdo->query('SELECT SUM(total) AS grand_total, COUNT(*) AS cnt FROM items');
$row = $q->fetch(PDO::FETCH_ASSOC);
$htTotal = (int)$row['grand_total'];
$ttcTotal = $applyTax($htTotal);
$rowCount = (int)$row['cnt'];

$digest = substr(hash('sha256', $skuTag . ':' . $ttcTotal . ':' . $reqId), 0, 16);

header('Content-Type: application/json');
header('X-PHP127-Worker: ' . $workerId);

echo json_encode([
    'req_id' => $reqId,
    'worker' => $workerId,
    'sku'    => $skuTag,
    'rows'   => $rowCount,
    'ht'     => $htTotal,
    'ttc'    => $ttcTotal,
    'digest' => $digest,
]);
`

type stressResponse struct {
	ReqID  int    `json:"req_id"`
	Worker int    `json:"worker"`
	SKU    string `json:"sku"`
	Rows   int    `json:"rows"`
	HT     int    `json:"ht"`
	TTC    int    `json:"ttc"`
	Digest string `json:"digest"`
}

// TestHighConcurrencyLoadStress soumet 2 000 requêtes HTTP concurrentes réparties sur
// 32 goroutines clientes simultanées vers un serveur SAPI borné à 16 arènes Zone.
// Chaque requête exécute le programme PHP complet (superglobales $_GET/$_POST, objets,
// fermetures, PCRE preg_match, SQLite PDO en mémoire avec transaction et requête préparée,
// hash SHA-256, en-tête HTTP et sérialisation JSON).
func TestHighConcurrencyLoadStress(t *testing.T) {
	prog, err := compiler.CompileSource(stressWorkloadPHPSource, "stress_workload.php")
	if err != nil {
		t.Fatalf("compiler.CompileSource: %v", err)
	}
	verifyCompiledProgram(t, prog)

	const (
		maxWorkers     = 16
		numClients     = 32
		totalRequests  = 2000
		zoneCapBytes   = 4 << 20
	)

	runner := sapi.NewProgramRunner(prog, func(z *zone.Zone, p *bytecode.Program, _ *sapi.RequestContext, out io.Writer) error {
		code := vm.Execute(z, out, io.Discard, p, nil)
		if code != 0 {
			return fmt.Errorf("vm.Execute exit code %d", code)
		}
		return nil
	})

	srv := sapi.NewServer(runner, sapi.Options{
		ZoneCapacity: zoneCapBytes,
		MaxWorkers:   maxWorkers,
		ServerConfig: sapi.ServerConfig{
			DocumentRoot:   "/devhoros/php127",
			ScriptFilename: "/devhoros/php127/stress_workload.php",
			ScriptName:     "/stress_workload.php",
			ServerName:     "php127.local",
			ServerPort:     "8080",
		},
	})

	latencies := make([]time.Duration, totalRequests)
	var nextReq atomic.Int64
	var errCount atomic.Int64
	var firstErr sync.Once
	var firstErrMsg string

	recordErr := func(msg string) {
		errCount.Add(1)
		firstErr.Do(func() {
			firstErrMsg = msg
		})
	}

	startWall := time.Now()
	var wg sync.WaitGroup
	for clientID := range numClients {
		wg.Go(func() {
			for {
				idx := int(nextReq.Add(1) - 1)
				if idx >= totalRequests {
					return
				}
				qty := (idx % 9) + 1
				sku := fmt.Sprintf("ITEM-%d", idx%100)
				formBody := fmt.Sprintf("payload=%s:%d", sku, qty)
				targetURL := fmt.Sprintf("http://php127.local:8080/stress_workload.php?req_id=%d&worker=%d", idx, clientID)

				req := httptest.NewRequest(http.MethodPost, targetURL, strings.NewReader(formBody))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()

				t0 := time.Now()
				srv.ServeHTTP(rec, req)
				latencies[idx] = time.Since(t0)

				res := rec.Result()
				if res.StatusCode != http.StatusOK {
					recordErr(fmt.Sprintf("req %d: status=%d body=%s", idx, res.StatusCode, rec.Body.String()))
					continue
				}
				if gotHdr := res.Header.Get("X-PHP127-Worker"); gotHdr != fmt.Sprintf("%d", clientID) {
					recordErr(fmt.Sprintf("req %d: X-PHP127-Worker=%q attendu %d", idx, gotHdr, clientID))
					continue
				}
				var parsed stressResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
					recordErr(fmt.Sprintf("req %d: json.Unmarshal: %v (body=%q)", idx, err, rec.Body.String()))
					continue
				}
				wantHT := qty*100 + 2*50
				wantTTC := wantHT + (wantHT*20)/100
				if parsed.ReqID != idx || parsed.Worker != clientID || parsed.SKU != sku || parsed.Rows != 2 || parsed.HT != wantHT || parsed.TTC != wantTTC || len(parsed.Digest) != 16 {
					recordErr(fmt.Sprintf("req %d: réponse invalide %+v (attendu ht=%d ttc=%d)", idx, parsed, wantHT, wantTTC))
					continue
				}
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(startWall)

	if errCount.Load() > 0 {
		t.Fatalf("%d erreurs sur %d requêtes concurrentes ; première erreur : %s", errCount.Load(), totalRequests, firstErrMsg)
	}
	if got := srv.RequestsHandled(); got != totalRequests {
		t.Fatalf("RequestsHandled = %d, attendu %d", got, totalRequests)
	}
	if got := srv.ZonesCreated(); got != maxWorkers {
		t.Fatalf("ZonesCreated = %d, attendu exactement %d (fuite d'arène détectée)", got, maxWorkers)
	}

	slices.Sort(latencies)
	p50 := latencies[totalRequests*50/100]
	p95 := latencies[totalRequests*95/100]
	p99 := latencies[totalRequests*99/100]
	rps := float64(totalRequests) / elapsed.Seconds()

	t.Logf("STRESS_RESULT requests=%d concurrency=%d workers=%d zones_created=%d elapsed=%v rps=%.1f p50=%v p95=%v p99=%v",
		totalRequests, numClients, maxWorkers, srv.ZonesCreated(), elapsed, rps, p50, p95, p99)
}

// BenchmarkHighConcurrencySAPI mesure le débit soutenu du serveur SAPI/VM sous charge parallèle.
func BenchmarkHighConcurrencySAPI(b *testing.B) {
	prog, err := compiler.CompileSource(stressWorkloadPHPSource, "stress_workload.php")
	if err != nil {
		b.Fatalf("compiler.CompileSource: %v", err)
	}
	runner := sapi.NewProgramRunner(prog, func(z *zone.Zone, p *bytecode.Program, _ *sapi.RequestContext, out io.Writer) error {
		vm.Execute(z, out, io.Discard, p, nil)
		return nil
	})
	srv := sapi.NewServer(runner, sapi.Options{
		ZoneCapacity: 4 << 20,
		MaxWorkers:   16,
	})

	var seq atomic.Int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := int(seq.Add(1))
			req := httptest.NewRequest(http.MethodPost,
				fmt.Sprintf("http://php127.local/stress.php?req_id=%d&worker=1", id),
				strings.NewReader("payload=ITEM-1:3"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatalf("statut HTTP inattendu: %d", rec.Code)
			}
		}
	})
}
