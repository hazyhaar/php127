# php127

> **Standalone, zero-allocation PHP 8.4 runtime with in-process HTTP, PDO & Fiber support in pure Go (No PHP-FPM, No CGO).**

[![CI Gate & Torture Pipeline](https://github.com/hazyhaar/php127/actions/workflows/ci.yml/badge.svg)](https://github.com/hazyhaar/php127/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0%20%2F%20MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/code.hazyhaar.fr/devhoros/php127)](https://goreportcard.com/report/code.hazyhaar.fr/devhoros/php127)

---

## Highlights

- **PHP 8.4 Core Parity:** Promoted constructor properties, attributes (`#[Route]`), dynamic VTable method dispatch, trait conflict adaptations (`insteadof` / `as`), backed enums, closures, arrow functions, and typed exceptions with protected stack unwinding.
- **Zero-Allocation Hot Path:** Enforced `testing.AllocsPerRun == 0` on core execution loops, linear arenas (`c2mem`), and insertion-ordered Swiss Tables (`c2ordered_swiss`).
- **First-Class Coroutines:** Native Fibers (`fiber_create`, `fiber_start`, `fiber_suspend`, `fiber_resume`).
- **In-Process Infrastructure:**
  - **HTTP Gateway:** Direct request/response lifecycle (`$_GET`, `$_POST`, `$_SERVER`, JSON bodies) without FastCGI.
  - **Relational PDO:** In-memory & SQLite transactional relational engine supporting atomic `Rollback()` via memory snapshot restoration.
  - **Built-in Packages:** `vlucas/phpdotenv`, `ramsey/uuid` (v4, v7, ULID), PSR-3 `monolog/monolog`, and pure-Go `ext-curl` (Guzzle compatible).
- **Runtime BlueTeam Guard:** Opcode-level Taint-tracking (blocking command injection and path traversal) + statistical Q8.8 entropy filter preventing egress secret exfiltration.

---

## Quick Start

### 1. Build the Standalone Binary

```bash
go build -o bin/php127 ./cmd/php127
```

### 2. Run PHP 8.4 Code Directly

```bash
./bin/php127 -r '
#[Route("/orders")]
class OrderController {
    public function __construct(public string $service) {}
    public function run() {
        $fib = fiber_create(fn() => uuid_v7());
        $id = fiber_start($fib);
        pdo_exec("CREATE TABLE IF NOT EXISTS orders (id, service)");
        pdo_exec("INSERT INTO orders (id, service) VALUES (?, ?)", $id, $this->service);
        return json_encode(["status" => "ok", "order_id" => $id]);
    }
}
$ctrl = new OrderController("BillingCluster");
echo $ctrl->run();
'
```

### 3. Run the Industrial 7-Gate CI & Torture Suite

```bash
chmod +x ./scripts/ci_gate.sh
./scripts/ci_gate.sh
```

---

## Architecture Overview

```
                          ┌─────────────────────────────┐
                          │   PHP 8.4 Source Code (.php) │
                          └──────────────┬──────────────┘
                                         │
                                [ Lexer & Parser ]
                                         │
                          ┌──────────────▼──────────────┐
                          │    Compact Bytecode AST     │
                          └──────────────┬──────────────┘
                                         │
                                [ Bytecode Compiler ]
                                         │
                          ┌──────────────▼──────────────┐
                          │     Flatstack127 VM &       │
                          │   24-Byte Tagged Values     │
                          └──────────────┬──────────────┘
                                         │
          ┌──────────────────────────────┼──────────────────────────────┐
          ▼                              ▼                              ▼
┌───────────────────┐          ┌───────────────────┐          ┌───────────────────┐
│ C2SIMD Containers │          │ In-Process PDO &  │          │ BlueTeam Security │
│ (Swiss, Ryu, Mem) │          │ HTTP Gateway      │          │ (Taint & Entropy) │
└───────────────────┘          └───────────────────┘          └───────────────────┘
```

---

## CI & Verification Gates

All pull requests and commits must pass the **7-Gate CI Pipeline**:

1. **Gate 1 (Static Quality):** `gofmt -s -d` and `go vet ./...`.
2. **Gate 2 (Data Race Detection):** `go test -v -race -count=1 ./...`.
3. **Gate 3 (Torture Stress Suite):** Deep recursion (2,500 frames), 500 concurrent Fibers, 500 PDO transactions under `-race`.
4. **Gate 4 (ARCHTIME Invariant):** `testing.AllocsPerRun == 0` validation on hot paths.
5. **Gate 5 (Oracle Parity):** Bit-exact comparison against official `php -r` output.
6. **Gate 6 (10 Strategic Targets):** End-to-end integration tests for ecosystem dependencies.
7. **Gate 7 (CLI Sanity):** Self-test binary execution and bootstrap validation.

---

## License

Dual-licensed under [Apache-2.0](LICENSE) or [MIT](LICENSE-MIT).
