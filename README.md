# `php127` — Pure Go 1.27 Zero-CGO PHP 8.3 Virtual Machine, AVX2 SIMD Kernels & Hardware PMU Observability

[![Language: Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go)](https://go.dev/)
[![Zero CGO](https://img.shields.io/badge/CGO-0%25%20Pure%20Go-success)](#)
[![SIMD: sgoiter AVX2](https://img.shields.io/badge/SIMD-sgoiter%20AVX2-blue)](#)
[![French README](https://img.shields.io/badge/Langue-Fran%C3%A7ais-blue)](README.fr.md)

`php127` is a self-contained, zero-CGO PHP 8.3 virtual machine written in Go 1.27. It replaces C union pointers (`zval`) and per-value `Rc<RefCell<...>>` reference counting with a **16-byte pointer-free `types.Value`** (two 64-bit registers) backed by a **per-request `Zone` arena** whose byte cursor resets in **195 CPU instructions** (`Zone.Reset()`).

---

## Technical Monograph & Full Benchmarks (`docs/`)

The complete architectural monograph (with 15 embedded high-DPI hardware PMU charts, `perf_event_open` + Intel RAPL measurements, and the security/observability operational guide) is tracked in [`docs/article_x_micro_design_for_cybersafety.docx`](docs/article_x_micro_design_for_cybersafety.docx), paired with the raw JSON metrics snapshot [`probebench/testdata/latest_metrics.json`](probebench/testdata/latest_metrics.json).

---

## Six Architectural Pillars

1. **16-Byte Pointer-Free `types.Value` (`types/value.go`)**:
   `Type uint8`, `Flags uint8`, `Extra uint16`, `DataSlot uint32`, `Scalar uint64`. Because `Value` contains zero Go pointers, the Go garbage collector (`runtime.scanobject`) skips the entire VM evaluation stack (`vm.slots`) and array bucket tables in $O(1)$ time.
2. **Per-Request `Zone` Arena & 195-Instruction `Zone.Reset()` (`zone/zone.go`)**:
   All dynamic request allocations live inside an indexed `Zone` arena validated by an epoch counter (`z.epoch`, `FlagZoneRequest`). At request completion, `Zone.Reset()` reclaims the byte slab in **195 instructions (40 cycles)**.
3. **Compile-Time (`ARCHTIME`) Interning & Monomorphic Inline Caches (`bytecode/program.go`, `vm/class.go`, `vm/call.go`)**:
   Single-byte strings (`byteTable[256]`), class property layouts (`ResolvedProps`), property inline caches (`PropIC`), and call inline caches (`CallIC`) are bound ahead of execution.
4. **In-Place 1D Array Mutation (`FlagAssignDimCV`, `vm/array.go`) & Specialized `int64`/`float64` Super-Instructions (`vm/vm.go`)**:
   Direct CV-targeted array updates (`$h[$k] = $v`, `$h[] = $v`) keep `RefCount == 1`, avoiding defensive Copy-on-Write clones (`10,225 insn/op` on `7_HashArrayMut`). Arithmetic loops promote automatically to `TypeDouble` on 64-bit signed integer overflow, matching PHP 8.3 semantics bit-for-bit.
5. **Transpiled AVX2 SIMD Kernels (`sgoiter`, `lexer/php_scan_gen.go`, `stdlib/php_strsimd_gen.go`)**:
   Vectorized lexer scanning (`Php_scan_ident`, `Php_scan_block_comment`) and two-way sentinel substring search (`C2_strpos`) are transpiled mechanically from C99 AVX2 to Go 1.27 `simd/archsimd` (`313 instructions`, `69 cycles`, `IPC 4.54` on a 1,290-byte buffer).
6. **Hardware-Assisted Taint Tracking (`FlagTainted`) & Deterministic Observability (`sapi/`, `vm/source.go`, `probebench/`)**:
   Untrusted HTTP inputs (`$_GET`, `$_POST`, `$_COOKIE`, `$_REQUEST`) are stamped with `FlagTainted` at ingestion (`insertParsedPair` in `sapi/url.go`), propagated across string operations via bitwise `OR` inside the 16-byte `Value`, and intercepted at `OpEval` / `OpInclude` sinks (`vm/source.go`) with `ValueError` circuit-breaking and `Context.TaintedSinks` telemetry.

---

## Quick Start

### 1. Build and Run the Standalone CLI
```bash
go build -o php127 ./cmd/php127
./php127 -r '$a = [10, 20, 30]; echo array_sum($a), "\n";'
```

### 2. Run the Test & Differential Parity Suites
```bash
go test -race -count=1 ./zone/... ./types/... ./lexer/... ./parser/... ./ast/... ./bytecode/... ./compiler/... ./stdlib/... ./vm/... ./sapi/...
# With runtime operand-stack invariant guard enabled:
go test -tags vmcheck -race -count=1 ./vm/... ./compiler/... ./sapi/...
```

### 3. Run the Hardware PMU & RAPL Observability Suite (`probebench`)
```bash
# Atomic mechanism probes (M0a..M6):
go test -v -count=1 -run TestAtomicMicroProbes ./probebench/...

# 2D Stratified Function x Data-Size Matrix (F0..F5 x D0..D4):
go test -v -count=1 -run TestStratifiedFunctionAndDataMatrix ./probebench/...

# Full 15-workload C vs Rust vs Go CLI vs Go VM suite (read-only mode):
go test -v -count=1 -run TestKernelProbeGoVsRustVsC ./probebench/...
```
