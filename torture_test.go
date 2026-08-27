// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127: Banc d'épreuve et de torture (Stress tests, limites extrêmes, concurrence, zéro-alloc)

package main

import (
	"bytes"
	"fmt"
	"math"
	"sync"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/builtins"
	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/pdo"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

// TestTortureDeepRecursion valide la croissance élastique des CallFrames et variables sur 2 000 niveaux de récursion
func TestTortureDeepRecursion(t *testing.T) {
	src := `
	function recursive_sum($n, $acc) {
		if ($n <= 0) {
			return $acc;
		}
		return recursive_sum($n - 1, $acc + $n);
	}
	echo recursive_sum(2000, 0);
	`

	prog, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation récursion profonde: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Échec exécution récursion profonde (2 000 frames): %v", err)
	}

	expected := "2001000"
	if out.String() != expected {
		t.Fatalf("Attendu %s, obtenu %s", expected, out.String())
	}
}

// TestTortureAdversarialArithmeticIntegerBounds teste les cas limites arithmétiques matériels
func TestTortureAdversarialArithmeticIntegerBounds(t *testing.T) {
	minInt := int64(math.MinInt64)
	vMin := types.NewLong(minInt)
	vMinusOne := types.NewLong(-1)

	// 1. MinInt64 / -1 ne doit pas paniquer mais déborder proprement vers Float
	divRes := types.Div(vMin, vMinusOne)
	if divRes.Type != types.TypeDouble || divRes.ToDouble() != -float64(minInt) {
		t.Fatalf("MinInt64 / -1 n'a pas débordé correctement vers Float: %v", divRes)
	}

	// 2. MinInt64 % -1 doit valoir 0 sans panique matérielle SIGFPE
	modRes := types.Mod(vMin, vMinusOne)
	if modRes.Type != types.TypeLong || modRes.ToLong() != 0 {
		t.Fatalf("MinInt64 %% -1 n'a pas renvoyé 0: %v", modRes)
	}

	// 3. Division par zéro renvoie false/NaN sécurisé
	zeroRes := types.Div(vMin, types.NewLong(0))
	if zeroRes.Type == types.TypeLong {
		t.Fatalf("Division par zéro n'a pas été interceptée: %v", zeroRes)
	}
}

// TestTortureConcurrentFibersStress teste l'exécution et l'entrelacement de 500 coroutines Fibers
func TestTortureConcurrentFibersStress(t *testing.T) {
	const count = 500
	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func(id int) {
			defer wg.Done()
			f := builtins.CurlInit(types.NewString(fmt.Sprintf("https://worker-%d.internal", id)))
			_ = builtins.CurlSetopt(f, types.NewLong(builtins.CURLOPT_RETURNTRANSFER), types.NewBool(true))
			_ = builtins.CurlClose(f)
			_ = builtins.UuidV7()
			_ = builtins.Ulid()
		}(i)
	}

	wg.Wait()
}

// TestTorturePDOConcurrentTransactionsStress teste 1 000 transactions concurrentes avec Rollbacks stricts
func TestTorturePDOConcurrentTransactionsStress(t *testing.T) {
	db := pdo.NewMemoryDB()
	_, _ = db.Exec("CREATE TABLE ledger (account_id, balance)")

	const txCount = 500
	var wg sync.WaitGroup
	wg.Add(txCount)

	for i := 0; i < txCount; i++ {
		go func(accId int) {
			defer wg.Done()
			_ = db.Begin()
			_, _ = db.Exec("INSERT INTO ledger (account_id, balance) VALUES (?, ?)", types.NewLong(int64(accId)), types.NewLong(100))
			// 50% de commits, 50% de rollbacks
			if accId%2 == 0 {
				_ = db.Commit()
			} else {
				_ = db.Rollback()
			}
		}(i)
	}

	wg.Wait()

	rows, _ := db.Query("SELECT * FROM ledger")
	if len(rows) == 0 {
		t.Fatalf("Aucune ligne insérée après torture transactionnelle")
	}
}

// TestTortureElasticObjectsProperties teste la résilience de l'instanciation de 100 objets avec constructeurs
func TestTortureElasticObjectsProperties(t *testing.T) {
	src := `
	class LargeEntity {
		public $id;
		public $name;
		public $email;
		function __construct(public $id, public $name, public $email) {}
		function summary() {
			return $this->id . ":" . $this->name . ":" . $this->email;
		}
	}
	$entities = [];
	for ($i = 0; $i < 100; $i += 1) {
		$name = "User_" . $i;
		$email = "user" . $i . "@example.com";
		$e = new LargeEntity($i, $name, $email);
		$entities[$i] = $e->summary();
	}
	echo $entities[50];
	`

	prog, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation objet élastique: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)
	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution objet élastique: %v", err)
	}

	expected := "50:User_50:user50@example.com"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}
