// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/foreign: Tests unitaires du pool de délégation hybride sécurisé

package foreign

import (
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func TestMockWorkerPoolExecution(t *testing.T) {
	pool := NewMockWorkerPool()
	pool.RegisterHandler("ldap_bind", func(args []types.Value) (types.Value, error) {
		if len(args) >= 2 && args[1].String() == "secret_pass" {
			return types.NewBool(true), nil
		}
		return types.NewBool(false), nil
	})

	// Test appel avec succès
	res, err := pool.Call("ldap_bind", []types.Value{
		types.NewString("cn=admin,dc=example,dc=org"),
		types.NewString("secret_pass"),
	})
	if err != nil {
		t.Fatalf("Erreur délégation foreign: %v", err)
	}
	if !res.ToBool() {
		t.Fatalf("Attendu true, obtenu false")
	}

	// Test fonction non enregistrée (fallback générique)
	resGen, err := pool.Call("imagecreate", []types.Value{types.NewLong(100), types.NewLong(100)})
	if err != nil {
		t.Fatalf("Erreur fallback générique: %v", err)
	}
	if resGen.Type != types.TypeString {
		t.Fatalf("Type inattendu pour fallback générique: %v", resGen.Type)
	}
}
