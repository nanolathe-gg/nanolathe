package units

import "github.com/nanolathe-gg/nanolathe/internal/cob"

// unitPortHandlers collapses the production port bindings into the older
// single-callback shape the VM used before reads and writes were separated. It
// has no production caller — bindUnitPortHandlers installs bindings directly —
// and lives here so this package's port fixtures can call one port without
// building a binding [04 R-COB-03 §1][04 R-COB-03 §4].
func unitPortHandlers(vm *cob.VM, u *Unit) map[cob.Port]func([]int32) int32 {
	bindings := unitPortBindings(vm, u)
	ports := make(map[cob.Port]func([]int32) int32, len(bindings))
	for port, binding := range bindings {
		binding := binding
		ports[port] = func(args []int32) int32 {
			if len(args) >= 2 && binding.Write != nil {
				binding.Write(args[1])
				return 0
			}
			if binding.Read == nil {
				return 0
			}
			var cells [4]int32
			copy(cells[:], args[1:])
			return binding.Read(cells)
		}
	}
	return ports
}
