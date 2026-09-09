package orders

import "github.com/nanolathe-gg/nanolathe/internal/units"

// activateHandler and deactivateHandler are the trivial activation handlers
// [04 R-ORD-01 §2]: if the definition has `onoffable`, raise / lower edge bit 0
// of the unit's engine-state byte — which arranges the `Activate` /
// `Deactivate` callback and emits status 3 / 4 through the one edge setter
// [04 R-UNIT-06 §2] — and complete either way.
//
// The `onoffable` test is the handlers' own; a definition without it silently
// accepts and discards the order, still reporting completion
// [04 R-SPEC-01 §11][05 R-PROD-01 §2].
func activateHandler(u *units.Unit, _ *Node, _ uint32, _ uint32) Code {
	setActivationIfOnOffable(u, true)
	return Code(5)
}

func deactivateHandler(u *units.Unit, _ *Node, _ uint32, _ uint32) Code {
	setActivationIfOnOffable(u, false)
	return Code(5)
}

// setActivationIfOnOffable is the shared gate of the two handlers above. The
// edge setter itself suppresses an unchanged value [04 R-UNIT-06 §2], so a
// repeated order is a no-op rather than a second callback.
func setActivationIfOnOffable(u *units.Unit, on bool) {
	if u == nil || u.Def == nil || !u.Def.OnOffable {
		return
	}
	u.SetActivationEdge(on)
}
