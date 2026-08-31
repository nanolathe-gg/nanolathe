package orders

import "github.com/nanolathe/nanolathe/internal/units"

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// after mutating the owning unit's runtime status word.
func makeSelectableHandler(u *units.Unit, _ *Node, _ uint32, _ uint32) Code {
	if u != nil {
		u.MakeSelectable()
	}
	return Code(5)
}
