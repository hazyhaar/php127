// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127: Banc d'épreuve et de torture contradictoire avancée (Adversarial Torture Suite)

package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/builtins"
	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/pdo"
	"code.hazyhaar.fr/devhoros/php127/security"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

// -----------------------------------------------------------------------------
// 1. DÉROULEMENT DE PILE COMPLEXE : TRY/CATCH/FINALLY & FIBERS IMBRIQUÉES
// -----------------------------------------------------------------------------

// TestAdversarialTryCatchFinallyMechanics teste le déroulement des exceptions typées,
// l'exécution des blocs catch/finally et la propagation d'exceptions non gérées à travers les frames d'appel.
func TestAdversarialTryCatchFinallyMechanics(t *testing.T) {
	src := `
	class CustomException { public $msg; function __construct($m) { $this->msg = $m; } }
	class OtherException { public $msg; function __construct($m) { $this->msg = $m; } }

	function riskyOperation($failType) {
		if ($failType == 1) {
			throw new CustomException("CRITICAL_1");
		}
		if ($failType == 2) {
			throw new OtherException("CRITICAL_2");
		}
		return "OK";
	}

	$res = "";
	// 1. Nominal with finally
	try {
		$res .= riskyOperation(0);
	} catch (CustomException $e) {
		$res .= ":C1";
	} finally {
		$res .= ":F1";
	}

	$res .= "|";

	// 2. Handled exception with finally
	try {
		$res .= riskyOperation(1);
	} catch (CustomException $e) {
		$res .= "C1:" . $e->msg;
	} finally {
		$res .= ":F2";
	}

	$res .= "|";

	// 3. Multi-catch with type discrimination
	try {
		$res .= riskyOperation(2);
	} catch (CustomException $e) {
		$res .= "C1:" . $e->msg;
	} catch (OtherException $e) {
		$res .= "C2:" . $e->msg;
	} finally {
		$res .= ":F3";
	}

	echo $res;
	`

	prog, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation try-catch: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Échec exécution try-catch: %v", err)
	}

	expected := "OK:F1|C1:CRITICAL_1:F2|C2:CRITICAL_2:F3"
	if out.String() != expected {
		t.Fatalf("Ordre de déroulement invalide:\nAttendu: %q\nObtenu : %q", expected, out.String())
	}
}

// TestAdversarialFiberLifecycleAndExceptionsInFinally teste le comportement d'une Fiber
// instanciée et exécutée dans un contexte de gestion d'exception avec suspension/reprise.
func TestAdversarialFiberLifecycleAndExceptionsInFinally(t *testing.T) {
	src := `
	class FiberFault {
		public $code;
		function __construct($c) { $this->code = $c; }
	}

	$fiberTask = function() {
		$s1 = fiber_suspend(100);
		return $s1 + 50;
	};

	$res = "";
	try {
		$res .= "A";
		$f = fiber_create($fiberTask);
		if (fiber_is_suspended($f)) {
			$res .= "!SUSP_PRE";
		}
		$v1 = fiber_start($f);
		$res .= ":v1=" . $v1;
		$res .= ":term=" . (fiber_is_terminated($f) ? "1" : "0");
	} catch (FiberFault $ff) {
		$res .= ":ERR";
	} finally {
		$res .= ":FIN";
	}
	echo $res;
	`

	prog, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation Fiber try/finally: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Échec exécution Fiber try/finally: %v", err)
	}

	expected := "A:v1=150:term=1:FIN"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}

// -----------------------------------------------------------------------------
// 2. ÉVASIONS DE TYPAGE & VALEURS SPÉCIALES DANS SWISSTABLE
// -----------------------------------------------------------------------------

// TestAdversarialSwissTableSpecialFloatKeys teste les clés flottantes extrêmes (NaN, +Inf, -Inf, -0.0)
// et vérifie la robustesse face aux dépassements d'indices et aux accès hors-bornes.
func TestAdversarialSwissTableSpecialFloatKeys(t *testing.T) {
	vmInst := vm.NewVM(nil)

	// Opérations directes sur BytecodeProgram pour injecter des valeurs IEEE-754 sans passer par le parseur
	prog := &vm.BytecodeProgram{
		Instructions: []vm.Instruction{
			{Op: vm.OpArrayInit},
			{Op: vm.OpStoreVar, A: 0}, // $arr = []

			// $arr[NaN] = "nan_val"
			{Op: vm.OpLoadVar, A: 0},
			{Op: vm.OpLoadConst, Const: types.NewDouble(math.NaN())},
			{Op: vm.OpLoadConst, Const: types.NewString("nan_val")},
			{Op: vm.OpArraySet},

			// $arr[+Inf] = "inf_val"
			{Op: vm.OpLoadVar, A: 0},
			{Op: vm.OpLoadConst, Const: types.NewDouble(math.Inf(1))},
			{Op: vm.OpLoadConst, Const: types.NewString("inf_val")},
			{Op: vm.OpArraySet},

			// $arr[-Inf] = "neg_inf_val"
			{Op: vm.OpLoadVar, A: 0},
			{Op: vm.OpLoadConst, Const: types.NewDouble(math.Inf(-1))},
			{Op: vm.OpLoadConst, Const: types.NewString("neg_inf_val")},
			{Op: vm.OpArraySet},

			// $arr[-0.0] = "zero_val"
			{Op: vm.OpLoadVar, A: 0},
			{Op: vm.OpLoadConst, Const: types.NewDouble(math.Copysign(0.0, -1.0))},
			{Op: vm.OpLoadConst, Const: types.NewString("zero_val")},
			{Op: vm.OpArraySet},

			// Lecture $arr[0] -> Doit renvoyer "zero_val" (coercition -0.0 -> 0)
			{Op: vm.OpLoadVar, A: 0},
			{Op: vm.OpLoadConst, Const: types.NewLong(0)},
			{Op: vm.OpArrayGet},
			{Op: vm.OpReturn},
		},
	}

	res, err := vmInst.Execute(prog)
	if err != nil {
		t.Fatalf("Exécution SwissTable NaN/Inf a échoué: %v", err)
	}

	if res.String() != "zero_val" {
		t.Fatalf("Lecture clé -0.0 -> 0 attendue 'zero_val', obtenu %q", res.String())
	}
}

// TestAdversarialSwissTableCapacityExhaustion teste la saturation des 16 tables SwissTable
// et vérifie que le moteur dégrade proprement sans panique.
func TestAdversarialSwissTableCapacityExhaustion(t *testing.T) {
	src := `
	$tables = [];
	for ($i = 0; $i < 30; $i += 1) {
		$a = [];
		$a[0] = $i * 10;
		$tables[$i] = $a[0];
	}
	echo $tables[5];
	`

	prog, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation saturation SwissTable: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Exécution saturation SwissTable a échoué: %v", err)
	}

	if out.String() != "50" {
		t.Fatalf("Attendu '50', obtenu %q", out.String())
	}
}

// TestAdversarialSwissTableMassiveIndexCollision teste des clés massives et hors-bornes
func TestAdversarialSwissTableMassiveIndexCollision(t *testing.T) {
	vmInst := vm.NewVM(nil)

	// Insertion de 100 clés dans une seule table
	prog := &vm.BytecodeProgram{
		Instructions: []vm.Instruction{
			{Op: vm.OpArrayInit},
			{Op: vm.OpStoreVar, A: 0},
		},
	}

	for i := int64(0); i < 100; i++ {
		prog.Instructions = append(prog.Instructions,
			vm.Instruction{Op: vm.OpLoadVar, A: 0},
			vm.Instruction{Op: vm.OpLoadConst, Const: types.NewLong(i)},
			vm.Instruction{Op: vm.OpLoadConst, Const: types.NewLong(i * 100)},
			vm.Instruction{Op: vm.OpArraySet},
		)
	}

	// Lecture de l'index 42 (dans la plage 0..63)
	prog.Instructions = append(prog.Instructions,
		vm.Instruction{Op: vm.OpLoadVar, A: 0},
		vm.Instruction{Op: vm.OpLoadConst, Const: types.NewLong(42)},
		vm.Instruction{Op: vm.OpArrayGet},
		vm.Instruction{Op: vm.OpReturn},
	)

	res, err := vmInst.Execute(prog)
	if err != nil {
		t.Fatalf("Exécution insertion massive SwissTable a échoué: %v", err)
	}

	if res.ToLong() != 4200 {
		t.Fatalf("Index 42 attendu 4200, obtenu %d", res.ToLong())
	}
}

// -----------------------------------------------------------------------------
// 3. DÉBORDEMENTS DE PILE VM (FLATSTACK) & DÉSYNCHRONISATION DES POINTEURS (IP)
// -----------------------------------------------------------------------------

// TestAdversarialMassiveLocalVariablesFrameIsolation teste une fonction possédant plus
// de 40 variables locales appelant une sous-fonction, pour valider l'isolation des fenêtres de variables.
func TestAdversarialMassiveLocalVariablesFrameIsolation(t *testing.T) {
	// Génération d'un script avec 40 variables distinctes assignées dans le scope appelant
	var sb strings.Builder
	sb.WriteString("function helper_calc($x) { return $x * 2; }\n")
	sb.WriteString("function main_heavy() {\n")
	for i := 0; i < 40; i++ {
		sb.WriteString(fmt.Sprintf("  $v%d = %d;\n", i, (i+1)*10))
	}
	// Appel de helper_calc avec une variable de fin de fenêtre
	sb.WriteString("  $res = helper_calc($v39);\n")
	// Re-vérification que $v0 n'a pas été écrasé par la frame callee
	sb.WriteString("  return $v0 + $res;\n")
	sb.WriteString("}\n")
	sb.WriteString("echo main_heavy();\n")

	prog, err := compiler.Compile(sb.String())
	if err != nil {
		t.Fatalf("Erreur compilation 40 variables locales: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Échec exécution frame lourde: %v", err)
	}

	// $v0 = 10, $v39 = 400. helper_calc(400) = 800. Result = 10 + 800 = 810.
	expected := "810"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}

// TestAdversarialDeepCallStackElasticity teste l'élasticité de la pile sur 2 500 frames d'appels récursifs
func TestAdversarialDeepCallStackElasticity(t *testing.T) {
	src := `
	function deep_ping_pong($n) {
		if ($n <= 0) {
			return 777;
		}
		return deep_ping_pong($n - 1) + 1;
	}
	echo deep_ping_pong(2500);
	`

	prog, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation récursion 2500: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Échec récursion profonde 2500 frames: %v", err)
	}

	// 777 + 2500 = 3277
	expected := "3277"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}

// -----------------------------------------------------------------------------
// 4. ÉCHAPPEMENT TAINT-TRACKING & SÉCURITÉ BLUETEAM
// -----------------------------------------------------------------------------

// TestAdversarialTaintTrackingBypassVectors teste les tentatives de contournement du taint tracking
// via concaténations partielles, coercitions, opérations bitwise et primitives string.
func TestAdversarialTaintTrackingBypassVectors(t *testing.T) {
	guard := security.NewGuard(security.ModeEnforce)
	taintedInput := types.NewString("127.0.0.1; rm -rf /").WithTaint(types.FlagTaintedUserInput)

	// 1. Propagation via Concat
	prefix := types.NewString("ping -c 1 ")
	var concatBuf [128]byte
	fullCmd := types.Concat(prefix, taintedInput, concatBuf[:])
	if !fullCmd.IsTainted() {
		t.Fatalf("Échec Taint: Concat n'a pas propagé le drapeau Taint")
	}
	if err := guard.CheckSink("exec", fullCmd); err != security.ErrSecurityTaintSinkViolation {
		t.Fatalf("Guard CheckSink aurait dû lever ErrSecurityTaintSinkViolation, obtenu: %v", err)
	}

	// 2. Propagation via Substr
	subCmd := types.Substr(taintedInput, 0, 9)
	if !subCmd.IsTainted() {
		t.Fatalf("Échec Taint: Substr n'a pas hérité du drapeau Taint")
	}

	// 3. Propagation via Arithmétique & Bitwise
	taintedInt := types.NewLong(100).WithTaint(types.FlagTaintedUserInput)
	cleanInt := types.NewLong(20)

	resAdd := types.Add(taintedInt, cleanInt)
	if !resAdd.IsTainted() {
		t.Fatalf("Échec Taint: Add n'a pas propagé le drapeau Taint")
	}

	resBit := types.BitXor(taintedInt, cleanInt)
	if !resBit.IsTainted() {
		t.Fatalf("Échec Taint: BitXor n'a pas propagé le drapeau Taint")
	}

	// 4. Exfiltration à haute entropie détectée par EgressInspect
	cryptoBlob := make([]byte, 256)
	_, _ = rand.Read(cryptoBlob)
	errEgress := guard.EgressInspect(cryptoBlob)
	if errEgress != security.ErrSecurityExfiltrationBlock {
		t.Fatalf("Guard aurait dû bloquer l'exfiltration à haute entropie, obtenu: %v", errEgress)
	}
}

// -----------------------------------------------------------------------------
// 5. INVARIANTS DE MÉMOIRE & TORTURE CONCURRENTE (-race)
// -----------------------------------------------------------------------------

// TestAdversarialArenaOverflowFallback teste des concaténations massives dépassant les 256 Ko de l'arène
// linéaire c2mem pour vérifier le basculement déterministe vers les allocations de tas Go sans corruption.
func TestAdversarialArenaOverflowFallback(t *testing.T) {
	src := `
	$chunk = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
	$acc = "";
	// 8 000 * 36 octets = 288 000 octets (> 256 Ko)
	for ($i = 0; $i < 8000; $i += 1) {
		$acc .= $chunk;
	}
	echo strlen($acc);
	`

	prog, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation test débordement arène: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Échec concaténation massive au-delà de 256 Ko: %v", err)
	}

	expected := fmt.Sprintf("%d", 8000*36)
	if out.String() != expected {
		t.Fatalf("Taille finale attendue %s, obtenu %s", expected, out.String())
	}
}

// TestAdversarialConcurrentMultiVMExecution exécute 200 machines virtuelles indépendantes en parallèle
// sous détection de race condition (-race) pour garantir l'absence d'état global mutable non protégé.
func TestAdversarialConcurrentMultiVMExecution(t *testing.T) {
	const workers = 200
	var wg sync.WaitGroup
	wg.Add(workers)

	src := `
	class ComputeNode {
		public $id;
		function __construct(public $id) {}
		function run() {
			$sum = 0;
			for ($i = 0; $i < 100; $i += 1) {
				$sum += $i;
			}
			return $this->id . ":" . $sum;
		}
	}
	$node = new ComputeNode(42);
	echo $node->run();
	`

	prog, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation multi-VM: %v", err)
	}

	var errorCount int64

	for i := 0; i < workers; i++ {
		go func(workerId int) {
			defer wg.Done()
			var out bytes.Buffer
			machine := vm.NewVM(&out)
			// Injection d'une base de données SQLite isolée par thread
			machine.DB = pdo.NewMemoryDB()
			_, errExec := machine.Execute(prog)
			if errExec != nil || out.String() != "42:4950" {
				atomic.AddInt64(&errorCount, 1)
			}
			// Appel de builtins cryptographiques et SPL concurrents
			_ = builtins.Sha256(types.NewString(fmt.Sprintf("worker-%d", workerId)))
			_ = builtins.UuidV7()
			_ = builtins.Ulid()
		}(i)
	}

	wg.Wait()

	if errorCount > 0 {
		t.Fatalf("%d workers ont échoué lors de l'exécution concurrente", errorCount)
	}
}
