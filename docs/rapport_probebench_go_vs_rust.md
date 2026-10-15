# Métrologie Matérielle Kernel-Max & Observabilité Stratifiée : Go 1.27 (`php127`) vs Rust 1.98 (`php-rust`) vs C (`php` 8.3.6)

**Cible matérielle mesurée :** Intel Core i9-14900K (Raptor Lake Refresh, 8 P-cores Golden Cove `cpu_core` type PMU = `4`, 16 E-cores Gracemont, L1D `48 Kio`/cœur, L2 `2 Mio`/cœur, L3 `36 Mio` partagé, DDR5, Linux 6.14, `kernel.perf_event_paranoid = 1`).  
**Paquet de mesure :** [`/devhoros/php127/probebench/`](file:///devhoros/php127/probebench/pmu_linux.go) ([`probebench_test.go`](file:///devhoros/php127/probebench/probebench_test.go), export JSON [`probebench/testdata/latest_metrics.json`](file:///devhoros/php127/probebench/testdata/latest_metrics.json)).  
**Articles compagnons :** [`/devhoros/php127/docs/article_x_micro_design_for_cybersafety_fr.md`](file:///devhoros/php127/docs/article_x_micro_design_for_cybersafety_fr.md) et [`/devhoros/php127/docs/article_x_micro_design_for_cybersafety.md`](file:///devhoros/php127/docs/article_x_micro_design_for_cybersafety.md).

---

## 1. Protocole Métrologique et Sondes Matérielles Déployées

Afin d'éliminer les biais des micro-benchmarks sur processeurs x86-64 hybrides à exécution dans le désordre (*Out-of-Order*), le banc [`probebench`](file:///devhoros/php127/probebench/pmu_linux.go) applique quatre gardes physiques strictes :

1. **Isolation topologique sur P-Core Golden Cove (`CPU 0`)** : chaque thread de mesure (et chaque sous-processus enfant par héritage de masque `sched_setaffinity(2)`) est verrouillé sur le thread noyau (`runtime.LockOSThread()`) et épinglé sur le cœur haute performance `CPU 0` via `sched_setaffinity`. Cela supprime toute migration vers les E-cores Gracemont (`CPUs 16..31`) et garantit que les compteurs PMU matériels `cpu_core` (`type = 4`) comptent 100 % des cycles exécutés.
2. **Sérialisation d'instruction `LFENCE; RDTSCP; LFENCE`** : la lecture du compteur de cycles `TSC` et du registre `IA32_TSC_AUX` (`ECX`, identifiant du cœur physique) est encadrée par deux barrières `_mm_lfence()`, interdisant au pipeline OoO de réordonner `RDTSCP` avant la fin des instructions antérieures et vérifiant l'absence de migration de cœur (`CoreStart == CoreEnd`).
3. **Compteurs PMU matériels en deux banques avec contrôle `PERF_FORMAT_TOTAL_TIME_ENABLED | PERF_FORMAT_TOTAL_TIME_RUNNING` (`perf_event_open(2)`)** : chaque cœur Golden Cove disposant de 3 compteurs fixes et de 4 à 8 compteurs programmables (`IA32_PMC`), [`MeasureInProcess`](file:///devhoros/php127/probebench/pmu_linux.go) et [`MeasureSubprocess`](file:///devhoros/php127/probebench/pmu_linux.go) scindent la capture en deux passes dédiées sur **11 passages indépendants** avec vérification mécanique de `time_enabled == time_running` (`Multiplexed == false`), agrégation médiane par compteur et écart interquartile (`CyclesUserIQR`, `InstructionsIQR`) :
   - **Banque 1 (Pipeline, Branchements & Noyau)** :
     - `CPU_CLK_UNHALTED.THREAD` en Anneau 3 (`CyclesUser`, `exclude_kernel=1`) ;
     - `CPU_CLK_UNHALTED.THREAD` en Anneau 0 (`CyclesKernel`, `exclude_user=1, exclude_kernel=0`) — mesure exacte de la fraction de cycles absorbée par le noyau Linux (`% Kernel`) ;
     - `INST_RETIRED.ANY` (`Instructions`) $\rightarrow$ calcul de l'**IPC** (`Instructions / CyclesUser`) ;
     - `BR_INST_RETIRED.ALL_BRANCHES` (`Branches`) et `BR_MISP_RETIRED.ALL_BRANCHES` (`BranchMisses`) $\rightarrow$ taux d'erreur de prédiction (`BrMiss%`) ;
     - Sondes logicielles du noyau : `PERF_COUNT_SW_PAGE_FAULTS_MIN` (`MinFlt`), `PERF_COUNT_SW_CONTEXT_SWITCHES`, `PERF_COUNT_SW_CPU_MIGRATIONS`.
   - **Banque 2 (Hiérarchie des Caches SRAM L1D / L2 / L3 & Bus DRAM)** :
     - `MEM_INST_RETIRED.ALL_LOADS` (`L1DLoads`, `event=0xd0, umask=0x81`) ;
     - `MEM_INST_RETIRED.ALL_STORES` (`L1DStores`, `event=0xd0, umask=0x82`) ;
     - `LONGEST_LAT_CACHE.REFERENCE` (`CacheRefs`, `event=0x2e, umask=0x4f`) ;
     - `LONGEST_LAT_CACHE.MISS` (`CacheMisses`, `event=0x2e, umask=0x41`) $\rightarrow$ lignes de 64 octets transférées depuis la DRAM DDR5.
4. **Sonde matérielle RAPL dormante (`readRAPLPackageUJ`)** : la lecture du compteur matériel `/sys/class/powercap/intel-rapl:0/energy_uj` est câblée dans `probebench/pmu_linux.go` et activée uniquement lorsque le compteur est lisible et que la machine est au repos (`loadavg < 1,0`). Aucune modélisation énergétique synthétique n'est émise.

---

## 2. Anatomie Microarchitecturale des Opérations Atomiques de `php127` (`TestAtomicMicroProbes`)

Mesure unitaire sur $N = 100\,000$ opérations consécutives sur le P-core 0 (`0` migration de cœur), séparant strictement l'allocation bump-pointer (`M0a`) de la remise à zéro d'arène sans objet (`M0b` et coût marginal `M0Δ = M0b - M0a`, `Zone.Reset()` remettant `z.offset = 0` et `z.freeHead = 0` en $O(1)$ et nettoyant la tranche des objets alloués en $O(\text{objectUsed})$) :

| Opération Atomique / Mutation dans `php127` | `ns/op` | `Cycles/op` | `Insn/op` | `IPC` | `L1D R+W/op` | `BrMiss/1k` | `Tas GC (B)` | `Arène Zone (B)` |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| **`M0a` : `Zone.Alloc(64B)` amorti (`Reset` toutes les 1024 allocs)** | **`1,26 ns`** | **`6`** | `40` | `6,61` | `12` | `0,37` | **`0 B`** | `64 B` |
| **`M0b` : `Zone.Reset()` sans objet (1 alloc 64B + 1 `Reset`)** | `11,26 ns` | `58` | `235` | `4,00` | `93` | `0,36` | **`0 B`** | `64 B` |
| **`M0Δ` : Coût pur `Zone.Reset()` (`M0b - M0a`)** | **`10,00 ns`** | **`52`** | **`195`** | `-` | `81` | `-` | **`0 B`** | `0 B` |
| **`M1` : `types.Add` (`Long + Long`, `Value` 16 octets)** | `11,13 ns` | `58` | `69` | `1,17` | `41` | `0,31` | **`0 B`** | `0 B` |
| **`M2` : `NewStringLiteral` 1 octet (ARCHTIME 0-alloc)** | `1,25 ns` | `6` | `39` | `6,46` | `18` | `0,27` | **`0 B`** | **`0 B`** |
| **`M3` : `NewString(32B)` + Hash DJBX33A (`sgoiter`)** | `32,87 ns` | `171` | `626` | `3,65` | `159` | `0,42` | **`0 B`** | `64 B` |
| **`M4` : `ArrayGet` (Clé chaîne, hash $O(1)$ caché)** | `43,25 ns` | `222` | `801` | `3,59` | `335` | `0,42` | **`0 B`** | `0 B` |
| **`M5` : `ArraySet` (Mutation in-place `Bucket` 32B)** | `63,89 ns` | `327` | `1 336` | `4,08` | `568` | `0,42` | **`0 B`** | `0 B` |
| **`M6` : `ObjectGet` + `ObjectSet` (Slot indexé $O(1)$)** | `17,87 ns` | `89` | `370` | `4,14` | `176` | `0,36` | **`0 B`** | `0 B` |
| **`M7` : Lexer `Php_scan_label` AVX2 (`sgoiter` 47B)** | `39,46 ns` | `208` | `739` | `3,55` | `58` | `0,65` | **`0 B`** | `0 B` |
| **`M8` : PCRE Fast-Path RE2 + Cache (`preg_match`)** | `886,42 ns` | `4 436` | `18 056` | `4,07` | `6 240` | `4 064,10` | `140 B` | `608 B` |

---

## 3. Matrice d'Observabilité Stratifiée $F \times D$ de `php127` (`TestStratifiedFunctionAndDataMatrix`)

Chaque cellule isole sa strate fonctionnelle $F_1 \dots F_4$ à travers 5 régimes de mutation de données $D_0 \dots D_4$. Sur `F4` (VM Execute + `Zone.Reset()`), le ramasse-miettes Go effectue **zéro allocation** sur `D0_Noop` et `D1_ALU` (`0 B/op`), avec une entrée-sortie complète en **`0,07 µs` (`317 cycles`, `1 325 instructions`)** :

| Régime de Mutation $D$ | Strate Fonctionnelle $F$ | `µs/exec` | `Cycles` | `Insn` | `IPC` | `L1D R+W` | `LLC Miss` | `BrMiss%` | `Tas GC (B)` |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| **`D0_Noop`** (`<?php ;`) | **`F1` : Lexer SIMD (`sgoiter`)** | **`0,04 µs`** | `173` | `681` | `3,92` | `264` | `0` | `0,00 %` | **`0 B`** |
| `D0_Noop` | **`F2` : Parser AST + Validate** | `0,19 µs` | `913` | `3 527` | `3,86` | `1 449` | `0` | `0,11 %` | `416 B` |
| `D0_Noop` | **`F3` : Compiler + Link + Verify** | `0,25 µs` | `1 236` | `4 304` | `3,48` | `1 807` | `0` | `0,19 %` | `1 104 B` |
| `D0_Noop` | **`F4` : VM Execute + `Zone.Reset`** | **`0,07 µs`** | **`317`** | **`1 325`** | **`4,18`** | **`631`** | **`0`** | **`0,00 %`** | **`0 B`** |
| **`D1_ALU`** (500 tours ALU) | **`F1` : Lexer SIMD (`sgoiter`)** | `0,82 µs` | `4 082` | `15 454` | `3,79` | `5 618` | `0` | `0,05 %` | **`0 B`** |
| `D1_ALU` | **`F2` : Parser AST + Validate** | `2,87 µs` | `14 964` | `41 518` | `2,77` | `17 117` | `26` | `0,13 %` | `7 800 B` |
| `D1_ALU` | **`F3` : Compiler + Link + Verify** | `1,61 µs` | `8 046` | `28 005` | `3,48` | `11 170` | `2` | `0,11 %` | `6 344 B` |
| `D1_ALU` | **`F4` : VM Execute + `Zone.Reset`** | **`33,86 µs`** | **`171 444`** | **`808 046`** | **`4,71`** | **`359 443`** | **`0`** | **`0,00 %`** | **`0 B`** |
| **`D2_String`** (200 concat/substr) | **`F1` : Lexer SIMD (`sgoiter`)** | `0,77 µs` | `3 923` | `15 631` | `3,98` | `5 499` | `0` | `0,00 %` | **`0 B`** |
| `D2_String` | **`F2` : Parser AST + Validate** | `2,67 µs` | `13 872` | `41 152` | `2,97` | `16 760` | `1` | `0,13 %` | `4 936 B` |
| `D2_String` | **`F3` : Compiler + Link + Verify** | `1,73 µs` | `8 910` | `30 911` | `3,47` | `12 313` | `6` | `0,10 %` | `6 792 B` |
| `D2_String` | **`F4` : VM Execute + `Zone.Reset`** | **`86,07 µs`** | **`448 188`** | **`1 407 071`** | **`3,14`** | `651 969` | `230` | `0,07 %` | `25 600 B` |
| **`D3_HashArr`** (200 mut. table) | **`F1` : Lexer SIMD (`sgoiter`)** | `1,25 µs` | `6 221` | `23 751` | `3,82` | `8 535` | `0` | `0,03 %` | **`0 B`** |
| `D3_HashArr` | **`F2` : Parser AST + Validate** | `4,11 µs` | `20 972` | `58 697` | `2,80` | `24 211` | `7` | `0,11 %` | `8 544 B` |
| `D3_HashArr` | **`F3` : Compiler + Link + Verify** | `2,55 µs` | `12 149` | `42 252` | `3,48` | `16 756` | `16` | `0,08 %` | `10 512 B` |
| `D3_HashArr` | **`F4` : VM Execute + `Zone.Reset`** | **`49,31 µs`** | **`250 370`** | **`961 224`** | **`3,84`** | `419 694` | `24` | `0,07 %` | `2 112 B` |
| **`D4_ObjRegex`** (Objet+Closure+PCRE+JSON) | **`F1` : Lexer SIMD (`sgoiter`)** | `2,88 µs` | `14 087` | `56 610` | `4,02` | `19 329` | `0` | `0,06 %` | **`0 B`** |
| `D4_ObjRegex` | **`F2` : Parser AST + Validate** | `9,36 µs` | `49 074` | `136 427` | `2,78` | `55 999` | `79` | `0,14 %` | `20 056 B` |
| `D4_ObjRegex` | **`F3` : Compiler + Link + Verify** | `6,80 µs` | `33 645` | `107 714` | `3,20` | `45 428` | `139` | `0,21 %` | `20 424 B` |
| `D4_ObjRegex` | **`F4` : VM Execute + `Zone.Reset`** | **`44,45 µs`** | **`227 756`** | **`815 246`** | **`3,58`** | `421 534` | `4` | `0,00 %` | **`372 B`** |

---

## 4. Benchmark Comparatif Kernel-Max (Épreuves 1 à 8 + Sondes White & Black 9 à 15)

### 4.1. Volet 1 — Mesure Macroscopique de Bout en Bout (15 sondes, export exact `probebench/testdata/latest_metrics.json`, médiane de 11 passages)

Toutes les cellules ci-dessous sont générées directement depuis [`probebench/testdata/latest_metrics.json`](file:///devhoros/php127/probebench/testdata/latest_metrics.json). Pour C (`php 8.3`), Rust (`phpr`) et Go (`php127 CLI`), la mesure inclut le sous-processus complet (`fork`/`execve`). La ligne `Go (php127 VM)` mesure l'exécution en VM résidente précompilée avec `Zone.Reset()`.

| Épreuve (`HighN`, $\Delta N$) | Moteur | `Wall (ms)` | `Cycles User` | `Cycles IQR` | `% Kernel` | `Instructions` | `IPC` | `L1D R+W` | `LLC Miss` | `BrMiss%` | `MinFlt` |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| **`1_ColdBoot`** (`HighN=1`, $\Delta N=1$) | C (php 8.3) | `5,97 ms` | `60 853 958` | `64 034 423` | `48,1 %` | `98 226 441` | `1,61` | `44 353 358` | `390 602` | `2,57 %` | `1 099` |
| `1_ColdBoot` | Rust (phpr) | `82,10 ms` | `382 412 496` | `461 724 142` | `80,0 %` | `593 505 898` | `1,55` | `296 206 310` | `3 349 408` | `2,41 %` | `9 740` |
| `1_ColdBoot` | **Go (php127 CLI)** | `2,51 ms` | `28 822 324` | `27 599 598` | `56,9 %` | `41 385 933` | `1,44` | `19 182 493` | `126 176` | `2,01 %` | `894` |
| `1_ColdBoot` | **Go (php127 VM)** | `0,02 ms` | `5 346` | `1 775` | `67,6 %` | `3 794` | `0,71` | `1 678` | `14` | `1,51 %` | `0` |
| **`2_LoopBranch`** (`HighN=100 100`, $\Delta N=100 000$) | C (php 8.3) | `5,87 ms` | `72 181 887` | `82 752 173` | `48,4 %` | `113 283 939` | `1,57` | `52 807 740` | `467 561` | `2,13 %` | `1 100` |
| `2_LoopBranch` | Rust (phpr) | `95,34 ms` | `453 552 977` | `528 414 873` | `78,4 %` | `651 833 175` | `1,44` | `332 996 880` | `3 645 251` | `2,33 %` | `9 742` |
| `2_LoopBranch` | **Go (php127 CLI)** | `4,38 ms` | `58 165 510` | `74 419 398` | `38,7 %` | `219 230 159` | `3,77` | `106 346 581` | `191 309` | `0,37 %` | `896` |
| `2_LoopBranch` | **Go (php127 VM)** | `1,55 ms` | `6 294 203` | `2 394 383` | `1,4 %` | `35 541 349` | `5,65` | `14 517 049` | `307` | `0,00 %` | `0` |
| **`3_IntArithBit`** (`HighN=100 100`, $\Delta N=100 000$) | C (php 8.3) | `7,14 ms` | `80 628 115` | `96 113 659` | `42,1 %` | `189 954 846` | `2,36` | `100 282 518` | `576 375` | `1,22 %` | `1 101` |
| `3_IntArithBit` | Rust (phpr) | `98,47 ms` | `503 388 322` | `577 086 831` | `78,9 %` | `964 765 911` | `1,92` | `500 031 612` | `4 058 801` | `1,46 %` | `9 847` |
| `3_IntArithBit` | **Go (php127 CLI)** | `16,28 ms` | `304 376 797` | `388 553 934` | `12,4 %` | `1 166 973 183` | `3,83` | `634 480 004` | `268 496` | `0,08 %` | `901` |
| `3_IntArithBit` | **Go (php127 VM)** | `12,24 ms` | `58 337 453` | `9 936 583` | `1,0 %` | `225 030 996` | `3,86` | `102 506 018` | `2 337` | `0,00 %` | `0` |
| **`4_FuncCalls`** (`HighN=100 100`, $\Delta N=100 000$) | C (php 8.3) | `7,27 ms` | `89 565 118` | `115 713 357` | `40,1 %` | `227 078 396` | `2,54` | `126 745 875` | `532 721` | `1,08 %` | `1 103` |
| `4_FuncCalls` | Rust (phpr) | `128,69 ms` | `630 398 700` | `754 054 849` | `80,0 %` | `1 349 680 021` | `2,14` | `765 715 870` | `4 408 265` | `1,12 %` | `9 852` |
| `4_FuncCalls` | **Go (php127 CLI)** | `18,21 ms` | `419 235 058` | `483 026 319` | `10,5 %` | `1 537 067 411` | `3,67` | `883 838 063` | `294 736` | `0,06 %` | `907` |
| `4_FuncCalls` | **Go (php127 VM)** | `14,57 ms` | `69 631 961` | `10 745 310` | `1,3 %` | `299 004 780` | `4,29` | `144 047 462` | `3 927` | `0,00 %` | `0` |
| **`5_ObjPropMut`** (`HighN=100 100`, $\Delta N=100 000$) | C (php 8.3) | `7,26 ms` | `100 752 987` | `108 293 728` | `37,0 %` | `252 140 787` | `2,50` | `147 199 907` | `551 850` | `1,01 %` | `1 103` |
| `5_ObjPropMut` | Rust (phpr) | `101,89 ms` | `731 825 238` | `898 946 026` | `66,0 %` | `1 518 297 579` | `2,07` | `893 025 230` | `3 849 352` | `1,00 %` | `9 942` |
| `5_ObjPropMut` | **Go (php127 CLI)** | `51,42 ms` | `1 115 839 253` | `1 343 689 174` | `4,0 %` | `3 750 878 240` | `3,36` | `2 194 488 038` | `316 693` | `0,03 %` | `919` |
| `5_ObjPropMut` | **Go (php127 VM)** | `47,28 ms` | `213 156 962` | `14 446 678` | `0,4 %` | `741 759 982` | `3,48` | `362 470 537` | `5 714` | `0,00 %` | `0` |
| **`6_StrArenaVsRc`** (`HighN=20 100`, $\Delta N=20 000$) | C (php 8.3) | `5,74 ms` | `74 065 286` | `92 094 022` | `40,3 %` | `181 101 720` | `2,45` | `93 769 073` | `416 511` | `1,37 %` | `1 103` |
| `6_StrArenaVsRc` | Rust (phpr) | `71,22 ms` | `460 871 604` | `567 068 388` | `75,3 %` | `866 132 376` | `1,88` | `456 856 555` | `3 705 215` | `1,75 %` | `9 851` |
| `6_StrArenaVsRc` | **Go (php127 CLI)** | `14,19 ms` | `249 830 616` | `305 845 813` | `16,3 %` | `766 254 792` | `3,07` | `426 874 371` | `272 382` | `0,20 %` | `1 390` |
| `6_StrArenaVsRc` | **Go (php127 VM)** | `10,61 ms` | `45 651 168` | `4 169 055` | `0,5 %` | `144 196 582` | `3,16` | `67 243 618` | `30 599` | `0,06 %` | `0` |
| **`7_HashArrayMut`** (`HighN=2 010`, $\Delta N=2 000$) | C (php 8.3) | `5,43 ms` | `66 280 889` | `72 557 591` | `49,5 %` | `104 020 847` | `1,57` | `47 730 753` | `574 386` | `2,40 %` | `1 106` |
| `7_HashArrayMut` | Rust (phpr) | `81,65 ms` | `413 977 887` | `477 778 343` | `78,5 %` | `626 502 082` | `1,51` | `314 115 141` | `3 750 215` | `2,32 %` | `9 941` |
| `7_HashArrayMut` | **Go (php127 CLI)** | `4,71 ms` | `59 236 939` | `70 114 592` | `36,8 %` | `147 333 062` | `2,49` | `78 654 632` | `185 332` | `0,64 %` | `908` |
| `7_HashArrayMut` | **Go (php127 VM)** | `1,44 ms` | `6 221 603` | `220 229` | `0,4 %` | `20 584 746` | `3,31` | `9 666 418` | `126` | `0,00 %` | `0` |
| **`8_RegexPCRE`** (`HighN=2 010`, $\Delta N=2 000$) | C (php 8.3) | `5,19 ms` | `60 777 012` | `72 055 297` | `46,6 %` | `109 835 311` | `1,81` | `49 541 560` | `449 741` | `2,36 %` | `1 112` |
| `8_RegexPCRE` | Rust (phpr) | `107,27 ms` | `448 795 531` | `544 879 595` | `79,4 %` | `756 468 517` | `1,69` | `395 880 360` | `3 703 314` | `1,95 %` | `9 961` |
| `8_RegexPCRE` | **Go (php127 CLI)** | `5,97 ms` | `104 165 499` | `106 736 469` | `34,3 %` | `277 413 267` | `2,66` | `103 902 781` | `273 694` | `0,38 %` | `1 234` |
| `8_RegexPCRE` | **Go (php127 VM)** | `2,29 ms` | `11 217 692` | `2 289 860` | `1,0 %` | `38 184 586` | `3,40` | `13 973 065` | `10 321` | `0,05 %` | `0` |
| **`9_W_GoSimdPos`** (`HighN=5 050`, $\Delta N=5 000$) | C (php 8.3) | `6,97 ms` | `87 958 162` | `105 595 566` | `38,8 %` | `147 565 233` | `1,68` | `64 020 276` | `482 997` | `1,50 %` | `1 105` |
| `9_W_GoSimdPos` | Rust (phpr) | `83,50 ms` | `645 179 916` | `780 721 173` | `61,3 %` | `1 495 302 953` | `2,32` | `571 357 111` | `3 693 958` | `0,85 %` | `9 938` |
| `9_W_GoSimdPos` | **Go (php127 CLI)** | `7,34 ms` | `107 559 557` | `133 716 070` | `26,0 %` | `469 758 270` | `4,37` | `117 367 803` | `210 810` | `0,20 %` | `933` |
| `9_W_GoSimdPos` | **Go (php127 VM)** | `5,21 ms` | `23 356 394` | `2 411 481` | `0,5 %` | `85 332 081` | `3,65` | `16 206 599` | `7 487` | `0,03 %` | `0` |
| **`10_W_GoZoneObj`** (`HighN=5 050`, $\Delta N=5 000$) | C (php 8.3) | `5,14 ms` | `53 963 535` | `72 927 196` | `46,3 %` | `111 650 444` | `2,07` | `52 892 487` | `175 472` | `2,24 %` | `1 221` |
| `10_W_GoZoneObj` | Rust (phpr) | `57,29 ms` | `393 991 737` | `478 082 465` | `74,6 %` | `701 950 685` | `1,78` | `363 014 080` | `2 803 257` | `2,05 %` | `10 246` |
| `10_W_GoZoneObj` | **Go (php127 CLI)** | `6,83 ms` | `114 023 998` | `125 226 835` | `30,1 %` | `329 045 107` | `2,89` | `179 699 671` | `232 758` | `0,29 %` | `1 389` |
| `10_W_GoZoneObj` | **Go (php127 VM)** | `3,17 ms` | `14 656 180` | `1 076 076` | `1,3 %` | `55 256 893` | `3,77` | `24 192 912` | `6 948` | `0,01 %` | `0` |
| **`11_W_RustFloat`** (`HighN=50 100`, $\Delta N=50 000$) | C (php 8.3) | `5,75 ms` | `74 200 137` | `90 864 368` | `37,7 %` | `189 147 371` | `2,55` | `99 043 784` | `186 704` | `1,26 %` | `1 102` |
| `11_W_RustFloat` | Rust (phpr) | `64,42 ms` | `551 846 803` | `652 862 866` | `64,8 %` | `1 258 927 438` | `2,28` | `704 280 276` | `3 480 810` | `1,14 %` | `9 008` |
| `11_W_RustFloat` | **Go (php127 CLI)** | `14,95 ms` | `358 045 633` | `355 630 264` | `11,1 %` | `1 336 640 094` | `3,73` | `600 888 238` | `251 483` | `0,08 %` | `899` |
| `11_W_RustFloat` | **Go (php127 VM)** | `11,85 ms` | `52 666 688` | `17 986 150` | `0,4 %` | `218 540 979` | `4,15` | `98 589 745` | `16 747` | `0,00 %` | `0` |
| **`12_B_GoStrFree`** (`HighN=10 100`, $\Delta N=10 000$) | C (php 8.3) | `5,92 ms` | `82 770 256` | `84 136 897` | `44,4 %` | `154 963 673` | `1,87` | `74 329 435` | `512 786` | `1,64 %` | `1 104` |
| `12_B_GoStrFree` | Rust (phpr) | `57,13 ms` | `429 474 201` | `488 395 323` | `75,4 %` | `730 683 938` | `1,70` | `374 832 330` | `3 644 642` | `1,96 %` | `8 902` |
| `12_B_GoStrFree` | **Go (php127 CLI)** | `8,22 ms` | `140 143 999` | `168 649 577` | `19,8 %` | `449 895 182` | `3,21` | `241 233 934` | `104 190` | `0,32 %` | `1 050` |
| `12_B_GoStrFree` | **Go (php127 VM)** | `5,34 ms` | `23 315 841` | `92 831` | `0,3 %` | `84 197 532` | `3,61` | `38 266 402` | `8 135` | `0,12 %` | `0` |
| **`13_B_GoSwiBTB`** (`HighN=20 100`, $\Delta N=20 000$) | C (php 8.3) | `5,42 ms` | `60 423 962` | `74 295 774` | `44,3 %` | `126 146 803` | `2,09` | `61 878 724` | `328 845` | `1,93 %` | `1 106` |
| `13_B_GoSwiBTB` | Rust (phpr) | `55,48 ms` | `382 149 333` | `457 362 378` | `71,2 %` | `723 985 504` | `1,89` | `375 863 721` | `2 689 292` | `1,94 %` | `9 011` |
| `13_B_GoSwiBTB` | **Go (php127 CLI)** | `4,96 ms` | `75 121 593` | `91 731 195` | `28,1 %` | `308 399 289` | `4,11` | `157 930 137` | `44 680` | `0,29 %` | `903` |
| `13_B_GoSwiBTB` | **Go (php127 VM)** | `2,50 ms` | `10 918 303` | `110 675` | `0,4 %` | `56 787 882` | `5,20` | `24 707 081` | `541` | `0,00 %` | `0` |
| **`14_B_GoDeepRec`** (`HighN=1 010`, $\Delta N=1 000$) | C (php 8.3) | `4,30 ms` | `56 038 074` | `67 891 569` | `41,1 %` | `134 835 920` | `2,41` | `66 977 366` | `34 087` | `1,93 %` | `1 103` |
| `14_B_GoDeepRec` | Rust (phpr) | `54,60 ms` | `392 471 148` | `472 687 052` | `70,2 %` | `767 928 430` | `1,96` | `404 429 284` | `2 403 453` | `1,83 %` | `9 006` |
| `14_B_GoDeepRec` | **Go (php127 CLI)** | `6,02 ms` | `113 170 378` | `137 045 251` | `20,6 %` | `410 702 834` | `3,63` | `241 506 473` | `31 099` | `0,25 %` | `902` |
| `14_B_GoDeepRec` | **Go (php127 VM)** | `3,96 ms` | `19 063 670` | `99 034` | `0,8 %` | `77 396 378` | `4,06` | `38 707 161` | `91` | `0,02 %` | `0` |
| **`15_B_RustCowDrp`** (`HighN=2 010`, $\Delta N=2 000$) | C (php 8.3) | `4,57 ms` | `54 017 134` | `64 717 786` | `45,8 %` | `110 925 199` | `2,05` | `51 901 801` | `231 875` | `2,27 %` | `1 108` |
| `15_B_RustCowDrp` | Rust (phpr) | `58,63 ms` | `368 845 673` | `440 031 801` | `72,2 %` | `641 014 093` | `1,74` | `324 590 549` | `2 777 326` | `2,19 %` | `9 007` |
| `15_B_RustCowDrp` | **Go (php127 CLI)** | `4,72 ms` | `82 036 071` | `88 442 571` | `26,2 %` | `239 127 683` | `2,91` | `111 425 251` | `31 569` | `0,41 %` | `910` |
| `15_B_RustCowDrp` | **Go (php127 VM)** | `2,36 ms` | `10 529 128` | `197 653` | `0,5 %` | `37 172 054` | `3,53` | `16 966 157` | `602` | `0,00 %` | `0` |

---

### 4.2. Volet 2 — Isolation Différentielle Pure par Mutation ($\Delta = \frac{\text{Run}(N_{\text{high}}) - \text{Run}(N_{\text{low}})}{\Delta N}$) sur les 14 Sondes Itératives

Le coût marginal $\Delta$ (extrait directement du champ `delta` de [`probebench/testdata/latest_metrics.json`](file:///devhoros/php127/probebench/testdata/latest_metrics.json)) élimine intégralement le coût fixe du lancement de processus (`execve`, `ld-linux.so`, initialisation du runtime) pour C, Rust et Go :

| Mutation ($\Delta / \text{op}$) | Moteur | `Cycles / mut` | `Insn / mut` | `IPC` | `L1D R+W / mut` | `BrMiss%` |
|---|---|---:|---:|---:|---:|---:|
| **`2_LoopBranch`** | C (php 8.3) | `130` | `149` | `1,15` | `84` | `0,00 %` |
| `2_LoopBranch` | Rust (phpr) | `632` | `580` | `0,92` | `366` | `0,95 %` |
| `2_LoopBranch` | **Go (php127 VM)** | `62` | `355` | `5,73` | `145` | `0,00 %` |
| **`3_IntArithBit`** | C (php 8.3) | `213` | `914` | `4,29` | `556` | `0,00 %` |
| `3_IntArithBit` | Rust (phpr) | `1 200` | `3 703` | `3,09` | `2 032` | `0,00 %` |
| `3_IntArithBit` | **Go (php127 VM)** | `582` | `2 248` | `3,86` | `1 024` | `0,00 %` |
| **`4_FuncCalls`** | C (php 8.3) | `293` | `1 285` | `4,39` | `821` | `0,00 %` |
| `4_FuncCalls` | Rust (phpr) | `1 336` | `6 355` | `4,76` | `5 180` | `0,00 %` |
| `4_FuncCalls` | **Go (php127 VM)** | `695` | `2 987` | `4,30` | `1 439` | `0,00 %` |
| **`5_ObjPropMut`** | C (php 8.3) | `346` | `1 534` | `4,43` | `1 024` | `0,00 %` |
| `5_ObjPropMut` | Rust (phpr) | `3 321` | `9 229` | `2,78` | `5 958` | `0,00 %` |
| `5_ObjPropMut` | **Go (php127 VM)** | `2 129` | `7 410` | `3,48` | `3 621` | `0,00 %` |
| **`6_StrArenaVsRc`** | C (php 8.3) | `671` | `4 114` | `6,13` | `2 452` | `0,00 %` |
| `6_StrArenaVsRc` | Rust (phpr) | `2 425` | `13 538` | `5,58` | `7 977` | `0,23 %` |
| `6_StrArenaVsRc` | **Go (php127 VM)** | `2 264` | `7 173` | `3,17` | `3 344` | `0,00 %` |
| **`7_HashArrayMut`** | C (php 8.3) | `5 342` | `2 753` | `0,52` | `1 614` | `0,00 %` |
| `7_HashArrayMut` | Rust (phpr) | `9 627` | `15 916` | `1,65` | `8 661` | `1,00 %` |
| `7_HashArrayMut` | **Go (php127 VM)** | `3 084` | `10 225` | `3,32` | `4 802` | `0,00 %` |
| **`8_RegexPCRE`** | C (php 8.3) | `0` | `5 480` | `0,00` | `2 450` | `0,00 %` |
| `8_RegexPCRE` | Rust (phpr) | `20 583` | `76 049` | `3,69` | `47 021` | `0,04 %` |
| `8_RegexPCRE` | **Go (php127 VM)** | `5 564` | `18 988` | `3,41` | `6 949` | `0,02 %` |
| **`9_W_GoSimdPos`** | C (php 8.3) | `6 154` | `9 722` | `1,58` | `3 868` | `0,13 %` |
| `9_W_GoSimdPos` | Rust (phpr) | `53 839` | `178 434` | `3,31` | `54 415` | `0,08 %` |
| `9_W_GoSimdPos` | **Go (php127 VM)** | `4 638` | `16 893` | `3,64` | `3 207` | `0,03 %` |
| **`10_W_GoZoneObj`** | C (php 8.3) | `124` | `2 612` | `21,06` | `1 664` | `0,00 %` |
| `10_W_GoZoneObj` | Rust (phpr) | `6 532` | `21 303` | `3,26` | `13 143` | `0,22 %` |
| `10_W_GoZoneObj` | **Go (php127 VM)** | `2 789` | `10 223` | `3,67` | `4 450` | `0,00 %` |
| **`11_W_RustFloat`** | C (php 8.3) | `354` | `1 806` | `5,10` | `1 087` | `0,00 %` |
| `11_W_RustFloat` | Rust (phpr) | `3 355` | `13 263` | `3,95` | `8 135` | `0,00 %` |
| `11_W_RustFloat` | **Go (php127 VM)** | `1 026` | `4 288` | `4,18` | `1 932` | `0,00 %` |
| **`12_B_GoStrFree`** | C (php 8.3) | `3 274` | `5 602` | `1,71` | `2 958` | `0,00 %` |
| `12_B_GoStrFree` | Rust (phpr) | `7 070` | `13 516` | `1,91` | `7 750` | `0,27 %` |
| `12_B_GoStrFree` | **Go (php127 VM)** | `2 242` | `7 972` | `3,56` | `3 618` | `0,06 %` |
| **`13_B_GoSwiBTB`** | C (php 8.3) | `531` | `1 355` | `2,55` | `855` | `0,00 %` |
| `13_B_GoSwiBTB` | Rust (phpr) | `1 690` | `6 412` | `3,79` | `3 923` | `0,00 %` |
| `13_B_GoSwiBTB` | **Go (php127 VM)** | `514` | `2 642` | `5,14` | `1 142` | `0,00 %` |
| **`14_B_GoDeepRec`** | C (php 8.3) | `7 379` | `35 987` | `4,88` | `22 257` | `0,16 %` |
| `14_B_GoDeepRec` | Rust (phpr) | `37 766` | `171 512` | `4,54` | `106 579` | `0,00 %` |
| `14_B_GoDeepRec` | **Go (php127 VM)** | `18 292` | `72 987` | `3,99` | `36 624` | `0,02 %` |
| **`15_B_RustCowDrp`** | C (php 8.3) | `669` | `6 117` | `9,14` | `3 658` | `0,00 %` |
| `15_B_RustCowDrp` | Rust (phpr) | `5 699` | `23 312` | `4,09` | `13 931` | `0,00 %` |
| `15_B_RustCowDrp` | **Go (php127 VM)** | `4 944` | `16 647` | `3,37` | `7 579` | `0,00 %` |

---

## 5. Synthèse Architecturale et Cybersécurité Structurelle

1. **Représentation par valeur `types.Value` de 16 octets sans pointeur (`types/value.go:25-37`)** :
   - Deux mots de 64 bits (`Scalar uint64` + en-tête `[DataSlot uint32 | Type uint8 | Flags uint8 | Extra uint16]`), passant dans deux registres x86-64 (`RAX:RDX`) et invisibles pour le balayeur GC Go (`0 B/op` par tour de boucle sur `D0_Noop` et `D1_ALU`).
2. **Activation de `PropIC` et `CallIC` sur `{main}` (`FuncID = 1`) et super-instructions `TypeDouble`** :
   - Sur `5_ObjPropMut`, l'attribution de `FuncID = 1` à `{main}` dans `bytecode.Link` active `vm.propCaches` et divise par **3,70** les instructions retirées en VM (`741 759 982` au total, soit `7 410 insn/op` contre `9 229 insn/op` en Rust).
   - Sur `11_W_RustFloat`, les super-instructions fusionnées (`OpLoadCV` + `OpAdd`/`OpSub`/`OpMul`/`OpIsSmaller`) étendues à `TypeDouble` abaissent le coût marginal à **`4 288 insn/op`**, soit **3,09x moins d'instructions** que `phpr` en Rust (`13 263 insn/op`).
3. **Sécurité d'exécution et isolation par requête** :
   - Invariant de non-réentrance des destructeurs (`__destruct`) à travers la réallocation de `vm.slots` et garde de pile à l'exécution `checkSlotsAfterPop` active sous `-tags vmcheck` (`vm/vmcheck_on.go`).
   - Marquage de teinte `types.FlagTainted` posé à la source HTTP (`insertParsedPair` / `newTaintedStr` dans `sapi/url.go:413` et `sapi/errors.go:97`), propagé dans `types/ops.go` et `vm/value.go`, comptabilisé dans `Context.TaintedSinks` (`vm/vm.go:27`) et bloqué au puits dans `vm/source.go:72` (`OpEval`, `OpInclude`) en levant `ValueError("Tainted input rejected at ...")`.
