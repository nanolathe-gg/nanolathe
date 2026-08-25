package combat

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// VisibilityHook is the canonical LOS predicate injected by the session [03 §3.2] C8 P0-11.
// When non-nil, target acquisition and retention consult it instead of ad-hoc distance.
// The hook must be deterministic and have no map iteration or float on the sim path.
var VisibilityHook func(viewer visibility.PlayerID, target visibility.Target) bool

// IsVisibleForCombat reports whether target is visible to viewer via the canonical predicate [03 §3.2] C8.
// It prefers the injected VisibilityHook when set, otherwise falls back to true (no LOS service).
func IsVisibleForCombat(viewer visibility.PlayerID, x, y, z numeric.Fixed, owner visibility.PlayerID, hidden bool, status uint32) bool {
	if VisibilityHook != nil {
		return VisibilityHook(viewer, visibility.Target{Owner: owner, X: x, Y: y, Z: z, Hidden: hidden, Status: status})
	}
	return true
}
