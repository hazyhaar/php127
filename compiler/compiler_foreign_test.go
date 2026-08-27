// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Tests de délégation transparente vers le Foreign Worker Pool

package compiler

import (
	"bytes"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/foreign"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

func TestCompilerForeignFallbackExecution(t *testing.T) {
	src := `
	$res1 = custom_unimplemented_ldap("user_alice", 123);
	$res2 = custom_crypto_sign("my_payload");
	echo $res1 . "|" . $res2;
	`

	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Erreur compilation foreign fallback: %v", err)
	}

	var out bytes.Buffer
	machine := vm.NewVM(&out)

	// Configuration du mock foreign worker pool
	mockPool := foreign.NewMockWorkerPool()
	mockPool.RegisterHandler("custom_unimplemented_ldap", func(args []types.Value) (types.Value, error) {
		return types.NewString("LDAP_OK:" + args[0].String()), nil
	})
	mockPool.RegisterHandler("custom_crypto_sign", func(args []types.Value) (types.Value, error) {
		return types.NewString("SIGNED:" + args[0].String()), nil
	})
	machine.ForeignPool = mockPool

	_, err = machine.Execute(prog)
	if err != nil {
		t.Fatalf("Erreur exécution foreign fallback: %v", err)
	}

	expected := "LDAP_OK:user_alice|SIGNED:my_payload"
	if out.String() != expected {
		t.Fatalf("Attendu %q, obtenu %q", expected, out.String())
	}
}
