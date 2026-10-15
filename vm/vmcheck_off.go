//go:build !vmcheck

package vm

func (vm *VM) checkSlotsAfterPop(_, _ int) {}
