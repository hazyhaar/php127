# `php127` — Machine Virtuelle PHP 8.3 en Go 1.27 Pur (Zéro CGO), Noyaux SIMD AVX2 & Observabilité Matérielle PMU

[![Language: Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go)](https://go.dev/)
[![Zero CGO](https://img.shields.io/badge/CGO-0%25%20Pure%20Go-success)](#)
[![SIMD: sgoiter AVX2](https://img.shields.io/badge/SIMD-sgoiter%20AVX2-blue)](#)
[![English README](https://img.shields.io/badge/Language-English-blue)](README.md)

`php127` est un moteur d'exécution PHP 8.3 autonome écrit en Go 1.27 pur (zéro CGO, zéro dépendance externe). Il remplace les pointeurs d'union C (`zval`) et le comptage de références par valeur (`Rc<RefCell<...>>`) par une **structure `types.Value` de 16 octets sans pointeur** (deux registres de 64 bits) adossée à une **arène `Zone` isolée par requête** dont la réinitialisation (`Zone.Reset()`) s'exécute en **195 instructions CPU**.

---

## Monographie Technique & Bancs de Mesure (`docs/`)

La monographie complète (incluant les 15 planches haute définition issues des compteurs matériels Linux `perf_event_open` et le guide opérationnel de sécurité et d'observabilité) est versionnée dans [`docs/article_x_micro_design_for_cybersafety.docx`](docs/article_x_micro_design_for_cybersafety.docx), accompagnée de l'instantané JSON [`probebench/testdata/latest_metrics.json`](probebench/testdata/latest_metrics.json).

---

## Démarrage Rapide

```bash
# Compiler le binaire autonome
go build -o php127 ./cmd/php127

# Exécuter les tests avec la garde d'invariants de pile (-tags vmcheck)
go test -tags vmcheck -race -count=1 ./zone/... ./types/... ./lexer/... ./parser/... ./bytecode/... ./compiler/... ./stdlib/... ./vm/... ./sapi/...

# Lancer les sondes matérielles PMU (M0a..M6 et matrice 2D F x D)
go test -v -count=1 -run "TestAtomicMicroProbes|TestStratifiedFunctionAndDataMatrix" ./probebench/...
```
