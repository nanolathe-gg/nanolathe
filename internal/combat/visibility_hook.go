package combat

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// IsVisibleForCombat reports whether target is visible to viewer via the canonical predicate [03 §3.2] C8.
// Per-session predicate lives on Service.Visibility [RS-P0-018][INVARIANTS I1][I6]; this package-global wrapper is deprecated.
// For authoritative path, call Service.IsVisibleForCombat; the global fallback remains for tests until callers migrate.
func IsVisibleForCombat(viewer visibility.PlayerID, x, y, z numeric.Fixed, owner visibility.PlayerID, hidden bool, status uint32) bool {
	// Deprecated global fallback: no per-service context available here, so fallback to true (no LOS service).
	// Production code should use (*Service).IsVisibleForCombat with per-session Visibility [RS-P0-018].
	return true
}

// IsVisibleForCombatService is the per-Service LOS check [03 §3.2] C8 [RS-P0-018].
// It consults s.Visibility when set, otherwise falls back to true (no LOS service).
func (s *Service) IsVisibleForCombat(viewer visibility.PlayerID, x, y, z numeric.Fixed, owner visibility.PlayerID, hidden bool, status uint32) bool {
	if s != nil && s.Visibility != nil {
		return s.Visibility(viewer, visibility.Target{Owner: owner, X: x, Y: y, Z: z, Hidden: hidden, Status: status})
	}
	return true
}
