//go:build linux && amd64

package probebench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/ext"
	"code.hazyhaar.fr/devhoros/php127/lexer"
	"code.hazyhaar.fr/devhoros/php127/parser"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/vm"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	binPHP83 = "/usr/bin/php"
	binPHPR  = "/devhoros/php127/testdata/php-rust/php-rust/target/release/phpr"
)

// =============================================================================
// 1. ANATOMIE MICROARCHITECTURALE DES OPÉRATIONS ATOMIQUES (PAR MUTATION)
// =============================================================================

func TestAtomicMicroProbes(t *testing.T) {
	const N = 100000
	z := zone.New(64 << 20)

	type atomicCase struct {
		name string
		ops  int64
		fn   func() uint64
	}

	sampleIdent := []byte("wordpress_register_block_type_metadata_callback ")
	pregMatchFn, _ := ext.Lookup("preg_match")

	cases := []atomicCase{
		{
			name: "M0a: Zone.Alloc(64B) amorti",
			ops:  N,
			fn: func() uint64 {
				for range N {
					_, _ = z.Alloc(64)
				}
				used := z.LiveBytes()
				z.Reset(nil)
				return used
			},
		},
		{
			name: "M0b: Zone.Reset() O(1) seul",
			ops:  N,
			fn: func() uint64 {
				for range N {
					_, _ = z.Alloc(64)
					z.Reset(nil)
				}
				return 64 * N
			},
		},
		{
			name: "M1: types.Add (Long + Long 16B Value)",
			ops:  N,
			fn: func() uint64 {
				a := types.NewLong(17)
				b := types.NewLong(25)
				for range N {
					a = types.Add(a, b)
				}
				_ = a
				return 0
			},
		},
		{
			name: "M2: NewStringLiteral 1-byte (ARCHTIME 0-alloc)",
			ops:  N,
			fn: func() uint64 {
				var v types.Value
				for range N {
					v, _ = types.NewStringLiteral(z, "x")
				}
				_ = v
				return z.LiveBytes()
			},
		},
		{
			name: "M3: NewString(32B) + DJBX33A Hash (sgoiter)",
			ops:  N,
			fn: func() uint64 {
				for range N {
					s, _ := types.NewString(z, "horos_php127_zero_gc_arena_str!!", 0)
					_ = types.StringHash(z, s)
				}
				used := z.LiveBytes()
				z.Reset(nil)
				return used
			},
		},
		{
			name: "M4: ArrayGet (Clé chaîne hash O(1) caché)",
			ops:  N,
			fn: func() uint64 {
				arr, _ := types.NewArray(z)
				k, _ := types.NewString(z, "user_id", 0)
				_ = types.StringHash(z, k)
				_ = types.ArraySet(z, &arr, k, types.NewLong(999), nil)
				var v types.Value
				for range N {
					v, _, _ = types.ArrayGet(z, arr, k, nil)
				}
				_ = v
				used := z.LiveBytes()
				z.Reset(nil)
				return used
			},
		},
		{
			name: "M5: ArraySet (Mutation in-place Bucket 32B)",
			ops:  N,
			fn: func() uint64 {
				arr, _ := types.NewArray(z)
				k, _ := types.NewString(z, "hits", 0)
				_ = types.StringHash(z, k)
				_ = types.ArraySet(z, &arr, k, types.NewLong(0), nil)
				for i := range N {
					_ = types.ArraySet(z, &arr, k, types.NewLong(int64(i)), nil)
				}
				used := z.LiveBytes()
				z.Reset(nil)
				return used
			},
		},
		{
			name: "M6: ObjectGet + ObjectSet (Slot indexé O(1))",
			ops:  N,
			fn: func() uint64 {
				obj, _ := types.NewObject(z, 1, 2, false)
				types.ObjectSet(z, obj, 0, types.NewLong(0))
				for i := range N {
					v := types.ObjectGet(z, obj, 0)
					types.ObjectSet(z, obj, 0, types.NewLong(v.ToLong()+int64(i&1)))
				}
				used := z.LiveBytes()
				z.Reset(nil)
				return used
			},
		},
		{
			name: "M7: Lexer Php_scan_label AVX2 (sgoiter 47B)",
			ops:  N,
			fn: func() uint64 {
				var end int
				for range N {
					end = lexer.Php_scan_label(sampleIdent, 0, len(sampleIdent))
				}
				_ = end
				return 0
			},
		},
		{
			name: "M8: PCRE Fast-Path RE2 + Cache (preg_match)",
			ops:  N / 10,
			fn: func() uint64 {
				s, _ := types.NewString(z, "user.name+tag@sub.domain.example.org", 0)
				p, _ := types.NewString(z, `/^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/`, 0)
				args := []types.Value{p, s}
				for range N / 10 {
					_, _ = pregMatchFn(z, io.Discard, args)
				}
				used := z.LiveBytes()
				z.Reset(nil)
				return used
			},
		},
	}

	t.Log("=================================================================================================================================")
	t.Log("SONDES ATOMIQUES PAR OPÉRATION DE DONNÉES (P-Core 0 Golden Cove, LFENCE+RDTSCP, PMU cpu_core sans multiplexage)")
	t.Log("=================================================================================================================================")
	t.Logf("%-44s | %7s | %7s | %7s | %5s | %7s | %7s | %6s | %6s",
		"Opération Atomique / Mutation", "ns/op", "Cyc/op", "Insn/op", "IPC", "L1D/op", "BrM/1k", "GC B", "Zone B")
	t.Log(strings.Repeat("-", 125))

	var m0a, m0b ProbeMetrics
	for _, tc := range cases {
		var lastZoneUsed uint64
		m, err := MeasureInProcessRuns(tc.name, tc.ops, 2, 7, func() uint64 { return lastZoneUsed }, func() {
			lastZoneUsed = tc.fn()
		})
		if err != nil {
			t.Fatalf("MeasureInProcessRuns(%s): %v", tc.name, err)
		}
		if m.Multiplexed {
			t.Fatalf("MeasureInProcessRuns(%s): PMU multiplexing detected (time_enabled != time_running)", tc.name)
		}
		u := m.PerOp(tc.ops)
		if strings.HasPrefix(tc.name, "M0a:") {
			m0a = u
		} else if strings.HasPrefix(tc.name, "M0b:") {
			m0b = u
		}
		brMissPer1k := float64(m.BranchMisses) * 1000.0 / float64(tc.ops)
		t.Logf("%-44s | %7.2f | %7d | %7d | %5.2f | %7d | %7.2f | %6d | %6d",
			tc.name,
			u.WallNs,
			u.CyclesUser,
			u.Instructions,
			m.IPC,
			u.L1DLoads+u.L1DStores,
			brMissPer1k,
			u.GoHeapBytes,
			u.ZoneBytes,
		)
	}
	resetInsn := m0b.Instructions - m0a.Instructions
	resetCyc := m0b.CyclesUser - m0a.CyclesUser
	resetNs := m0b.WallNs - m0a.WallNs
	t.Log(strings.Repeat("-", 125))
	t.Logf("%-44s | %7.2f | %7d | %7d | %5s | %7d | %7s | %6d | %6d",
		"M0Δ: Coût pur Zone.Reset() = M0b - M0a",
		resetNs,
		resetCyc,
		resetInsn,
		"-",
		(m0b.L1DLoads+m0b.L1DStores)-(m0a.L1DLoads+m0a.L1DStores),
		"-",
		0,
		0,
	)
}

// =============================================================================
// 2. MATRICE STRATIFIÉE F x D (FONCTION / STRATE x MUTATION DE DONNÉES)
// =============================================================================

func TestStratifiedFunctionAndDataMatrix(t *testing.T) {
	type dataMutation struct {
		id   string
		name string
		src  string
		ops  int64
	}

	mutations := []dataMutation{
		{
			id:   "D0_Noop",
			name: "Baseline vide (<?php ;)",
			src:  `<?php ;`,
			ops:  5000,
		},
		{
			id:   "D1_ALU",
			name: "Mutation scalaire ALU (500 tours)",
			src:  `<?php $x = 1; for ($i = 1; $i <= 500; $i++) { $x = ($x + $i * 3) ^ ($i >> 1); }`,
			ops:  1000,
		},
		{
			id:   "D2_String",
			name: "Mutation chaînes Arène (200 concat/substr)",
			src:  `<?php $s = "seed"; for ($i = 0; $i < 200; $i++) { $s = substr($s . "_chunk_" . $i, 0, 48); }`,
			ops:  500,
		},
		{
			id:   "D3_HashArr",
			name: "Mutation table associative (200 clés)",
			src:  `<?php $a = []; for ($i = 0; $i < 200; $i++) { $a["k_" . ($i & 31)] = $i * 2; } $sum = 0; foreach ($a as $k => $v) { $sum += $v; }`,
			ops:  500,
		},
		{
			id:   "D4_ObjRegex",
			name: "Graphe Objets + Closure + PCRE + JSON",
			src: `<?php
class Node {
    public int $val = 0;
    public function __construct(int $v) { $this->val = $v; }
    public function bump(int $d): int { $this->val += $d; return $this->val; }
}
$fn = fn(int $x): int => $x * 2 + 1;
$n = new Node(10);
for ($i = 0; $i < 50; $i++) {
    $n->bump($fn($i));
}
$ok = preg_match('/^[a-z]+_[0-9]+$/', 'node_42');
$j = json_encode(['v' => $n->val, 'ok' => $ok]);
`,
			ops: 400,
		},
	}

	t.Log("=====================================================================================================================================")
	t.Log("MATRICE STRATIFIÉE F × D DE PHP127 (Sondes Kernel PMU par Strate Fonctionnelle F1..F4 et par Régime de Mutation D0..D4)")
	t.Log("=====================================================================================================================================")
	t.Logf("%-12s | %-18s | %9s | %9s | %9s | %5s | %9s | %7s | %7s | %8s",
		"Mutation D", "Strate F", " µs/exec", "Cycles", "Insn", "IPC", "L1D R+W", "LLC M", "BrMiss%", "GC B/op")
	t.Log(strings.Repeat("-", 125))

	z := zone.New(64 << 20)

	for _, dm := range mutations {
		parsedAST, err := parser.Parse(dm.src, dm.id+".php")
		if err != nil {
			t.Fatalf("Parse(%s): %v", dm.id, err)
		}
		compiledProg, err := compiler.CompileSource(dm.src, dm.id+".php")
		if err != nil {
			t.Fatalf("Compile(%s): %v", dm.id, err)
		}

		// F1 : Lexer seul
		rawF1, err := MeasureInProcessRuns(dm.id+"/F1_Lexer", dm.ops, 2, 5, nil, func() {
			for range dm.ops {
				lx := lexer.New(dm.src)
				for {
					tok := lx.Next()
					if tok.ID == lexer.EOF {
						break
					}
				}
			}
		})
		if err != nil {
			t.Fatalf("MeasureInProcessRuns(%s/F1): %v", dm.id, err)
		}
		mF1 := rawF1.PerOp(dm.ops)

		// F2 : Parser + Validate
		rawF2, err := MeasureInProcessRuns(dm.id+"/F2_Parser", dm.ops, 2, 5, nil, func() {
			for range dm.ops {
				_, _ = parser.Parse(dm.src, dm.id+".php")
			}
		})
		if err != nil {
			t.Fatalf("MeasureInProcessRuns(%s/F2): %v", dm.id, err)
		}
		mF2 := rawF2.PerOp(dm.ops)

		// F3 : Compiler + Link + Verify isolé
		rawF3, err := MeasureInProcessRuns(dm.id+"/F3_Compiler", dm.ops, 2, 5, nil, func() {
			for range dm.ops {
				prog, _ := compiler.Compile(parsedAST)
				_ = bytecode.Link(prog)
				_ = bytecode.Verify(prog.Main)
			}
		})
		if err != nil {
			t.Fatalf("MeasureInProcessRuns(%s/F3): %v", dm.id, err)
		}
		mF3 := rawF3.PerOp(dm.ops)

		// F4 : VM Execute + Zone.Reset isolé
		rawF4, err := MeasureInProcessRuns(dm.id+"/F4_VM_Exec", dm.ops, 2, 5, nil, func() {
			for range dm.ops {
				vm.Execute(z, io.Discard, io.Discard, compiledProg, nil)
				z.Reset(func(item zone.CleanupItem) {
					types.DispatchObjectDestructor(z, item)
				})
			}
		})
		if err != nil {
			t.Fatalf("MeasureInProcessRuns(%s/F4): %v", dm.id, err)
		}
		mF4 := rawF4.PerOp(dm.ops)

		for _, row := range []struct {
			strate string
			m      ProbeMetrics
		}{
			{"F1: Lexer SIMD", mF1},
			{"F2: Parser AST", mF2},
			{"F3: Compiler+Link", mF3},
			{"F4: VM + ZoneReset", mF4},
		} {
			if row.m.Multiplexed {
				t.Fatalf("PMU multiplexing detected on %s/%s", dm.id, row.strate)
			}
			t.Logf("%-12s | %-18s | %9.2f | %9d | %9d | %5.2f | %9d | %7d | %6.2f%% | %8d",
				dm.id,
				row.strate,
				row.m.WallNs/1000.0,
				row.m.CyclesUser,
				row.m.Instructions,
				row.m.IPC,
				row.m.L1DLoads+row.m.L1DStores,
				row.m.CacheMisses,
				row.m.BranchMissPct,
				row.m.GoHeapBytes,
			)
		}
		t.Log(strings.Repeat("-", 125))
	}
}

// =============================================================================
// 3. GRAND BENCHMARK KERNEL-MAX : GO 1.27 (php127) vs RUST 1.98 (phpr) vs C (php 8.3)
// =============================================================================

type differentialWorkload struct {
	Name    string
	Desc    string
	LowN    int64
	HighN   int64
	MakeSrc func(n int64) string
}

func benchmarkWorkloads() []differentialWorkload {
	return []differentialWorkload{
		{
			Name:  "1_ColdBoot",
			Desc:  "Démarrage processus + init runtime + sortie immédiate",
			LowN:  0,
			HighN: 1,
			MakeSrc: func(n int64) string {
				return "<?php echo 42;\n"
			},
		},
		{
			Name:  "2_LoopBranch",
			Desc:  "Boucle pure et branchement conditionnel VM",
			LowN:  100,
			HighN: 100100,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\n$i = 0;\nwhile ($i < %d) {\n    $i++;\n}\necho $i, \"\\n\";\n", n)
			},
		},
		{
			Name:  "3_IntArithBit",
			Desc:  "Mutations arithmétiques 64-bit & opérations bit-à-bit",
			LowN:  100,
			HighN: 100100,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\n$acc = 0;\nfor ($i = 1; $i <= %d; $i++) {\n    $acc += ($i * 3) - ($i >> 1) + ($i & 7);\n}\necho $acc, \"\\n\";\n", n)
			},
		},
		{
			Name:  "4_FuncCalls",
			Desc:  "Appels de fonctions PHP (création CallFrame + retour)",
			LowN:  100,
			HighN: 100100,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\nfunction mix($a, $b) { return ($a + $b) ^ ($a >> 2); }\n$s = 0;\nfor ($i = 0; $i < %d; $i++) {\n    $s = mix($s, $i);\n}\necho $s, \"\\n\";\n", n)
			},
		},
		{
			Name:  "5_ObjPropMut",
			Desc:  "Mutations de propriétés d'objet ($obj->x / $obj->y)",
			LowN:  100,
			HighN: 100100,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\nclass Counter { public $x = 1; public $y = 2; }\n$c = new Counter();\nfor ($i = 0; $i < %d; $i++) {\n    $c->x = $c->x + $c->y;\n    $c->y = ($c->x & 255) + 1;\n}\necho $c->x, \":\", $c->y, \"\\n\";\n", n)
			},
		},
		{
			Name:  "6_StrArenaVsRc",
			Desc:  "Concaténations & substr (Arène Go Zone vs Rc<Vec<u8>> Rust)",
			LowN:  100,
			HighN: 20100,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\n$s = 'init_seed_';\nfor ($i = 0; $i < %d; $i++) {\n    $s = substr($s . 'abcd' . ($i & 7), 1, 32);\n}\necho strlen($s), ':', $s, \"\\n\";\n", n)
			},
		},
		{
			Name:  "7_HashArrayMut",
			Desc:  "Mutations & lectures de tables de hachage associatives",
			LowN:  10,
			HighN: 2010,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\n$h = ['k0'=>1,'k1'=>2,'k2'=>3,'k3'=>4,'k4'=>5,'k5'=>6,'k6'=>7,'k7'=>8];\n$sum = 0;\nfor ($i = 0; $i < %d; $i++) {\n    $h['k3'] = $i;\n    $sum += $h['k0'] + $h['k3'] + $h['k7'];\n}\necho $sum, \"\\n\";\n", n)
			},
		},
		{
			Name:  "8_RegexPCRE",
			Desc:  "Validation d'expressions régulières (preg_match)",
			LowN:  10,
			HighN: 2010,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\n$hits = 0;\nfor ($i = 0; $i < %d; $i++) {\n    if (preg_match('/^[a-z0-9._%%+-]+@[a-z0-9.-]+\\.[a-z]{2,}$/i', 'contact.horos57@devhoros.fr')) {\n        $hits++;\n    }\n}\necho $hits, \"\\n\";\n", n)
			},
		},
		{
			Name:  "9_W_GoSimdPos",
			Desc:  "[WHITE Go] Recherche strpos AVX2 Two-Way Sentinel (sgoiter) sur tampon 1290 octets",
			LowN:  50,
			HighN: 5050,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\n$hay = str_repeat('abcdefgh01234567', 64) . 'NEEDLE_XYZ' . str_repeat('abcdefgh01234567', 16);\n$acc = 0;\nfor ($i = 0; $i < %d; $i++) {\n    $p = strpos($hay, 'NEEDLE_XYZ');\n    if ($p !== false) {\n        $acc += $p;\n    }\n}\necho $acc, \"\\n\";\n", n)
			},
		},
		{
			Name:  "10_W_GoZoneObj",
			Desc:  "[WHITE Go] Allocation arène Zone + Reset O(1) vs cascade Rc/Drop sur graphe d'objets",
			LowN:  50,
			HighN: 5050,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\nclass Node { public $v = 0; public $next = null; }\n$head = null;\nfor ($i = 0; $i < %d; $i++) {\n    $n = new Node();\n    $n->v = $i;\n    $n->next = $head;\n    $head = $n;\n}\necho $head->v, \"\\n\";\n", n)
			},
		},
		{
			Name:  "11_W_RustFloat",
			Desc:  "[WHITE Rust/C] Calcul flottant dense (orbite de Julia float64 en registres XMM)",
			LowN:  100,
			HighN: 50100,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\n$zr = 0.0; $zi = 0.0; $cr = -0.7; $ci = 0.27015;\n$escaped = 0;\nfor ($i = 0; $i < %d; $i++) {\n    $tr = $zr * $zr - $zi * $zi + $cr;\n    $zi = 2.0 * $zr * $zi + $ci;\n    $zr = $tr;\n    if ($zr * $zr + $zi * $zi > 4.0) {\n        $zr = 0.0; $zi = 0.0;\n        $escaped++;\n    }\n}\necho $escaped, \"\\n\";\n", n)
			},
		},
		{
			Name:  "12_B_GoStrFree",
			Desc:  "[BLACK Go] Écrasement intensif de chaînes en boucle (recyclage z.freeHead L1d vs Drop)",
			LowN:  100,
			HighN: 10100,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\n$s = 'seed';\n$len = 0;\nfor ($i = 0; $i < %d; $i++) {\n    $s = 'chunk_payload_0123456789abcdef_' . $i . '_' . ($i & 15);\n    $len += strlen($s);\n}\necho $len, \"\\n\";\n", n)
			},
		},
		{
			Name:  "13_B_GoSwiBTB",
			Desc:  "[BLACK Go] Dispatch chaotique multi-opcodes (switch unique Go vs computed goto C)",
			LowN:  100,
			HighN: 20100,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\n$acc = 1;\nfor ($i = 1; $i <= %d; $i++) {\n    $op = $i & 7;\n    if ($op == 0) { $acc = ($acc + $i) ^ 3; }\n    elseif ($op == 1) { $acc = ($acc - $i) | 5; }\n    elseif ($op == 2) { $acc = ($acc * 3) & 0xFFFF; }\n    elseif ($op == 3) { $acc = ($acc >> 1) + ($i << 2); }\n    elseif ($op == 4) { $acc = ($acc == $i) ? ($acc + 7) : ($acc ^ $i); }\n    elseif ($op == 5) { $acc = ($acc < 1000) ? ($acc + $i * 2) : ($acc - 11); }\n    elseif ($op == 6) { $acc = (int)(!$acc) + $acc + ($i & 3); }\n    else { $acc = ($acc ^ ($i * 7)) & 0x7FFF; }\n}\necho $acc, \"\\n\";\n", n)
			},
		},
		{
			Name:  "14_B_GoDeepRec",
			Desc:  "[BLACK Go] Récursion profonde de 32 cadres par itération (pile frames + slots)",
			LowN:  10,
			HighN: 1010,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\nfunction climb($depth, $acc) {\n    if ($depth <= 0) {\n        return $acc + 1;\n    }\n    return climb($depth - 1, $acc + $depth);\n}\n$total = 0;\nfor ($i = 0; $i < %d; $i++) {\n    $total += climb(32, $i);\n}\necho $total, \"\\n\";\n", n)
			},
		},
		{
			Name:  "15_B_RustCowDrp",
			Desc:  "[BLACK Rust] Mutation CoW d'un tableau passé par valeur + Drop du clone au retour",
			LowN:  10,
			HighN: 2010,
			MakeSrc: func(n int64) string {
				return fmt.Sprintf("<?php\nfunction cow_touch($arr, $k, $v) {\n    $arr[$k] = $v;\n    return $arr['k0'] + $arr[$k];\n}\n$base = ['k0'=>10,'k1'=>20,'k2'=>30,'k3'=>40,'k4'=>50,'k5'=>60,'k6'=>70,'k7'=>80];\n$sum = 0;\nfor ($i = 0; $i < %d; $i++) {\n    $sum += cow_touch($base, 'k3', $i);\n}\necho $sum, ':', $base['k3'], \"\\n\";\n", n)
			},
		},
	}
}

type exportedBenchmarkRow struct {
	Workload string       `json:"workload"`
	HighN    int64        `json:"high_n"`
	LowN     int64        `json:"low_n"`
	DeltaN   int64        `json:"delta_n"`
	Engine   string       `json:"engine"`
	High     ProbeMetrics `json:"high"`
	Delta    ProbeMetrics `json:"delta"`
}

func TestKernelProbeGoVsRustVsC(t *testing.T) {
	if _, err := os.Stat(binPHPR); err != nil {
		t.Fatalf("binaire Rust phpr introuvable (%s): %v", binPHPR, err)
	}

	tmpDir := t.TempDir()
	cliBin := filepath.Join(tmpDir, "php127")
	buildCmd := exec.Command("go", "build", "-o", cliBin, "../cmd/php127")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build php127 CLI: %v (%s)", err, string(out))
	}

	workloads := benchmarkWorkloads()
	const runs = 11
	z := zone.New(64 << 20)

	t.Log("=================================================================================================================================================================")
	t.Log("VOLET 1 — BENCHMARK MACROSCOPIQUE KERNEL-MAX (BOUT EN BOUT : C [php 8.3.6] vs RUST 1.98 [phpr] vs GO 1.27 [php127 CLI & VM Zone], médiane sur 11 runs)")
	t.Log("=================================================================================================================================================================")
	t.Logf("%-15s | %-14s | %8s | %10s | %9s | %7s | %10s | %5s | %10s | %7s | %6s | %6s | %8s",
		"Épreuve (HighN)", "Moteur", "Wall ms", "Cyc User", "Cyc IQR", "Kern %", "Insn", "IPC", "L1D R+W", "LLC M", "BrM %", "MinFlt", "RSS KiB")
	t.Log(strings.Repeat("-", 160))

	type deltaRow struct {
		workload string
		engine   string
		deltaOps int64
		d        ProbeMetrics
	}
	var deltas []deltaRow
	var exported []exportedBenchmarkRow

	for _, wl := range workloads {
		highFile := filepath.Join(tmpDir, wl.Name+"_high.php")
		highSrc := wl.MakeSrc(wl.HighN)
		if err := os.WriteFile(highFile, []byte(highSrc), 0o600); err != nil {
			t.Fatal(err)
		}

		// 1. C (php 8.3.6) High
		cHigh, outC, err := MeasureSubprocess(wl.Name+"/C", binPHP83, []string{"-n", highFile}, wl.HighN, runs)
		if err != nil {
			t.Fatalf("php 8.3 (%s): %v", wl.Name, err)
		}
		// 2. Rust (phpr) High
		rHigh, outR, err := MeasureSubprocess(wl.Name+"/Rust", binPHPR, []string{highFile}, wl.HighN, runs)
		if err != nil {
			t.Fatalf("phpr (%s): %v", wl.Name, err)
		}
		// 3. Go (php127 CLI) High
		gCLIHigh, outGCLI, err := MeasureSubprocess(wl.Name+"/GoCLI", cliBin, []string{highFile}, wl.HighN, runs)
		if err != nil {
			t.Fatalf("php127 CLI (%s): %v", wl.Name, err)
		}
		// 4. Go (php127 VM In-Process) High
		progHigh, err := compiler.CompileSource(highSrc, highFile)
		if err != nil {
			t.Fatalf("CompileSource(%s): %v", wl.Name, err)
		}
		var outGVM bytes.Buffer
		gVMHigh, err := MeasureInProcessRuns(wl.Name+"/GoVM", wl.HighN, 2, runs, nil, func() {
			outGVM.Reset()
			vm.Execute(z, &outGVM, io.Discard, progHigh, nil)
			z.Reset(func(item zone.CleanupItem) {
				types.DispatchObjectDestructor(z, item)
			})
		})
		if err != nil {
			t.Fatalf("MeasureInProcessRuns(%s/GoVM): %v", wl.Name, err)
		}

		// Contrôle strict de parité bit à bit de la sortie entre C, Rust et Go
		if outR != outC || outGCLI != outC || outGVM.String() != outC {
			t.Fatalf("divergence Stdout sur %s:\nC:    %q\nRust: %q\nGoCLI:%q\nGoVM: %q",
				wl.Name, outC, outR, outGCLI, outGVM.String())
		}

		for _, row := range []struct {
			eng string
			m   ProbeMetrics
		}{
			{"C (php 8.3)", cHigh},
			{"Rust (phpr)", rHigh},
			{"Go (php127 CLI)", gCLIHigh},
			{"Go (php127 VM)", gVMHigh},
		} {
			if row.m.Multiplexed {
				t.Fatalf("PMU multiplexing detected on %s/%s", wl.Name, row.eng)
			}
			t.Logf("%-15s | %-14s | %8.2f | %10d | %9d | %6.1f%% | %10d | %5.2f | %10d | %7d | %5.2f%% | %6d | %8d",
				wl.Name,
				row.eng,
				row.m.WallNs/1e6,
				row.m.CyclesUser,
				row.m.CyclesUserIQR,
				row.m.KernelCyclePct,
				row.m.Instructions,
				row.m.IPC,
				row.m.L1DLoads+row.m.L1DStores,
				row.m.CacheMisses,
				row.m.BranchMissPct,
				row.m.PageFaultsMin,
				row.m.MaxRSSKiB,
			)
		}
		t.Log(strings.Repeat("-", 160))

		var cDelta, rDelta, gCLIDelta, gVMDelta ProbeMetrics
		dOps := wl.HighN - wl.LowN
		if wl.LowN > 0 && wl.HighN > wl.LowN {
			lowFile := filepath.Join(tmpDir, wl.Name+"_low.php")
			lowSrc := wl.MakeSrc(wl.LowN)
			if err := os.WriteFile(lowFile, []byte(lowSrc), 0o600); err != nil {
				t.Fatal(err)
			}

			cLow, _, err := MeasureSubprocess(wl.Name+"/C_Low", binPHP83, []string{"-n", lowFile}, wl.LowN, runs)
			if err != nil {
				t.Fatalf("php 8.3 low (%s): %v", wl.Name, err)
			}
			rLow, _, err := MeasureSubprocess(wl.Name+"/Rust_Low", binPHPR, []string{lowFile}, wl.LowN, runs)
			if err != nil {
				t.Fatalf("phpr low (%s): %v", wl.Name, err)
			}
			gCLILow, _, err := MeasureSubprocess(wl.Name+"/GoCLI_Low", cliBin, []string{lowFile}, wl.LowN, runs)
			if err != nil {
				t.Fatalf("php127 CLI low (%s): %v", wl.Name, err)
			}
			progLow, err := compiler.CompileSource(lowSrc, lowFile)
			if err != nil {
				t.Fatalf("CompileSource low (%s): %v", wl.Name, err)
			}
			gVMLow, err := MeasureInProcessRuns(wl.Name+"/GoVM_Low", wl.LowN, 2, runs, nil, func() {
				vm.Execute(z, io.Discard, io.Discard, progLow, nil)
				z.Reset(func(item zone.CleanupItem) {
					types.DispatchObjectDestructor(z, item)
				})
			})
			if err != nil {
				t.Fatalf("MeasureInProcessRuns low (%s): %v", wl.Name, err)
			}

			cDelta = DeltaPerOp(wl.Name, cLow, cHigh, dOps)
			rDelta = DeltaPerOp(wl.Name, rLow, rHigh, dOps)
			gCLIDelta = DeltaPerOp(wl.Name, gCLILow, gCLIHigh, dOps)
			gVMDelta = DeltaPerOp(wl.Name, gVMLow, gVMHigh, dOps)

			deltas = append(deltas,
				deltaRow{wl.Name, "C (php 8.3)", dOps, cDelta},
				deltaRow{wl.Name, "Rust (phpr)", dOps, rDelta},
				deltaRow{wl.Name, "Go (php127 CLI)", dOps, gCLIDelta},
				deltaRow{wl.Name, "Go (php127 VM)", dOps, gVMDelta},
			)
		}

		exported = append(exported,
			exportedBenchmarkRow{wl.Name, wl.HighN, wl.LowN, dOps, "C (php 8.3)", cHigh, cDelta},
			exportedBenchmarkRow{wl.Name, wl.HighN, wl.LowN, dOps, "Rust (phpr)", rHigh, rDelta},
			exportedBenchmarkRow{wl.Name, wl.HighN, wl.LowN, dOps, "Go (php127 CLI)", gCLIHigh, gCLIDelta},
			exportedBenchmarkRow{wl.Name, wl.HighN, wl.LowN, dOps, "Go (php127 VM)", gVMHigh, gVMDelta},
		)
	}

	t.Log("=========================================================================================================================================")
	t.Log("VOLET 2 — ISOLATION DIFFÉRENTIELLE PURE PAR MUTATION UNITAIRE : Δ = (Run(HighN) - Run(LowN)) / ΔN (Hors coût fork/exec et démarrage)")
	t.Log("=========================================================================================================================================")
	t.Logf("%-15s | %-15s | %8s | %8s | %8s | %5s | %8s | %8s | %7s",
		"Mutation (Δ/op)", "Moteur", "ns / mut", "Cyc/mut", "Insn/mut", "IPC", "L1D/mut", "Br/mut", "BrMiss%")
	t.Log(strings.Repeat("-", 115))

	lastWL := ""
	for _, dr := range deltas {
		if lastWL != "" && lastWL != dr.workload {
			t.Log(strings.Repeat("-", 115))
		}
		lastWL = dr.workload
		t.Logf("%-15s | %-15s | %8.1f | %8d | %8d | %5.2f | %8d | %8d | %6.2f%%",
			dr.workload,
			dr.engine,
			dr.d.WallNs,
			dr.d.CyclesUser,
			dr.d.Instructions,
			dr.d.IPC,
			dr.d.L1DLoads+dr.d.L1DStores,
			dr.d.Branches,
			dr.d.BranchMissPct,
		)
	}

	if os.Getenv("PROBEBENCH_UPDATE_METRICS") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err == nil {
			if b, err := json.MarshalIndent(exported, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join("testdata", "latest_metrics.json"), b, 0o644)
			}
		}
	}
}
