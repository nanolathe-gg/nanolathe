// ai_bind.go — production binding of the typed AI build request into the
// ordinary construction queues [RX-01][ON-06 F-P0-004][05 "Factory production
// lifecycle"]. Mobile sites carry the selected coordinates through to
// QueueMobileBuild; factory products queue on the factory path. The AI never
// receives privileged world mutation — this is the same command surface the
// human order path reaches.

package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func bindAIQueue(mgr *ai.Manager, s *Session) {
	if mgr == nil || s == nil {
		return
	}
	if s.Build != nil {
		// Ordinary AI move/order producers use the same concrete context as the
		// typed construction sink. The field is assigned before the manager can
		// dispatch in phase 5 [04 §3.3][06 §11.1].
		mgr.OrderBinding = s.Build.OrderBinding
	}
	mgr.QueueBuildTyped = func(req ai.BuildRequest) error {
		if s.Units == nil || s.Catalog == nil {
			return fmt.Errorf("ai build: session units/catalog unavailable")
		}
		builder := s.Units.Unit(req.Builder)
		if builder == nil || !builder.Alive {
			return fmt.Errorf("ai build: builder handle %d not alive", req.Builder)
		}
		// AI may issue its first build before this unit has ever needed a
		// queue. Bind the lazy queue through the session-owned context before
		// construction performs admission [04 §3.3][05][06 §11.1].
		s.bindOrderQueue(builder)
		switch req.Kind {
		case ai.BuildKindMobileSite:
			return construction.QueueMobileBuild(builder, req.UnitKey, req.X, req.Z, req.Count, s.Catalog)
		default:
			return construction.QueueFactoryBuild(builder, req.UnitKey, req.Count, s.Catalog)
		}
	}
}

// aiCanPursueAir is the Modern wave air targets predicate the computer
// player's ModernPlanner asks (DESIGN_SESSIONS_AI_SAVE "Modern wave air
// targets"): some weapon of member could engage the airborne target.
func (s *Session) aiCanPursueAir(member, target *units.Unit) bool {
	return combat.ModernAirPursuitAdmits(member, target, s.World, s.Catalog, s.Combat)
}
