#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0 OR MIT
# php127: Porte de contrôle CI, homologation et banc de torture (CI Gate)
# Code retour 0 = Validé. Usage: ./scripts/ci_gate.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "============================================================"
echo " [php127 CI GATE] 1. Contrôle Statique & Formatage Go"
echo "============================================================"
gofmt_diff=$(gofmt -s -d . 2>&1 || true)
if [[ -n "$gofmt_diff" ]]; then
    echo "ERREUR: Fichiers non formatés selon gofmt:"
    echo "$gofmt_diff"
    exit 1
fi
go vet ./...
echo " [OK] Formatage et analyse statique validés."

echo ""
echo "============================================================"
echo " [php127 CI GATE] 2. Suite Complète sous Détecteur de Courses"
echo "============================================================"
go test -v -race -count=1 ./...
echo " [OK] Zéro anomalie de concurrence détectée."

echo ""
echo "============================================================"
echo " [php127 CI GATE] 3. Banc de Torture & Stress Extrême"
echo "============================================================"
go test -v -race -run "TestTorture" .
echo " [OK] Épreuves de torture et récursion profonde validées."

echo ""
echo "============================================================"
echo " [php127 CI GATE] 4. Invariants ARCHTIME & Zéro-Allocation"
echo "============================================================"
go test -v -run "TestZeroAlloc" .
echo " [OK] Invariant testing.AllocsPerRun == 0 validé."

echo ""
echo "============================================================"
echo " [php127 CI GATE] 5. Parité Oracle Bit-Exacte contre PHP Officiel"
echo "============================================================"
go test -v -run "TestOracleParity" .
echo " [OK] Parité bit-exacte contre php -r validée."

echo ""
echo "============================================================"
echo " [php127 CI GATE] 6. Intégration des 10 Cibles Stratégiques"
echo "============================================================"
go test -v -run "TestTenStrategicTargets" .
echo " [OK] Pipeline Dotenv, UUID, Monolog, cURL, PDO validé."

echo ""
echo "============================================================"
echo " [php127 CI GATE] 7. Compilation & Auto-Test du Binaire CLI"
echo "============================================================"
mkdir -p bin
go build -o bin/php127 ./cmd/php127

cli_out=$(./bin/php127 -r '
parse_dotenv("APP_ENV=ci_gate_production");
$u7 = uuid_v7();
$fib = fiber_create(fn() => 127);
$val = fiber_start($fib);
pdo_exec("CREATE TABLE ci_gate (status, val)");
pdo_exec("INSERT INTO ci_gate (status, val) VALUES (?, ?)", "PASS", $val);
echo getenv("APP_ENV") . "|" . substr($u7, 0, 8) . "|" . $val . "|" . pdo_last_id();
')

expected_prefix="ci_gate_production|"
if [[ "$cli_out" != $expected_prefix* ]]; then
    echo "ERREUR: Sortie du binaire CLI inattendue: $cli_out"
    exit 1
fi
echo "Sortie CLI: $cli_out"
echo " [OK] Binaire CLI autonome opérationnel."

echo ""
echo "============================================================"
echo " >>> TOUS LES 7 GATES CI SONT HOMOLOGUÉS AVEC SUCCÈS <<<"
echo "============================================================"
