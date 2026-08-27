// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Primitives de rappel et fonctions d'ordre supérieur

package builtins

import (
	"code.hazyhaar.fr/devhoros/php127/types"
)

func IsCallable(v types.Value) types.Value {
	if v.Type == types.TypeLong || v.Type == types.TypeString {
		return types.NewBool(true)
	}
	if v.Type == types.TypeObject {
		return types.NewBool(true)
	}
	return types.NewBool(false)
}
