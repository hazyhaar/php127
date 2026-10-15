//go:build vmcheck

package vm

import (
	"fmt"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func (vm *VM) checkSlotsAfterPop(base, span int) {
	if vm.used != base {
		return
	}
	end := min(base+span, len(vm.slots))
	for i := base; i < end; i++ {
		if vm.slots[i].Type != types.TypeUndef {
			panic(fmt.Sprintf("vmcheck: slot[%d] non effacé après popFrame (type=%d)", i, vm.slots[i].Type))
		}
	}
}
