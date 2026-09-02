package orders

import "github.com/nanolathe/nanolathe/internal/units"

// makeSelectableHandler implements the MakeSelectable order handler [04 §3.1].
// The engine returns completion code 5 after mutating the owning unit's
// runtime status word.
func makeSelectableHandler(u *units.Unit, _ *Node, _ uint32, _ uint32) Code {
	if u != nil {
		u.MakeSelectable()
	}
	return Code(5)
}
