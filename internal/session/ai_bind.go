// ai_bind.go — production binding of the typed AI build request into the
// ordinary construction queues [RX-01][ON-06 F-P0-004][05 "Factory production
// lifecycle"]. Mobile sites carry the selected coordinates through to
// QueueMobileBuild; factory products queue on the factory path. The AI never
// receives privileged world mutation — this is the same command surface the
// human order path reaches.

package session

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
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
		case ai.BuildKindFactoryQueue:
			if def, ok := s.Catalog.Unit(req.UnitKey); ok && def != nil && stockpileAliasName(def.UnitName) {
				// The ordinary submission helper routes a MAKENUKE/MAKEANTI
				// product to a counted BUILDWEAPON round in slot zero, not to
				// a unit order [07 R-P0-11 §1].
				// ProTA 4.8 authors those products as pseudo-unit definitions
				// in its stockpile producers' CANBUILD lists, which is how the
				// computer player's queue task reaches this arm
				// (research/extensions/prota-engine.md "authored stationary
				// stockpile producers").
				if !s.queueStockpileRounds(builder, req.Count, req.Tick) {
					return fmt.Errorf("ai build: stockpile round refused for builder %d", req.Builder)
				}
				return nil
			}
			return construction.QueueFactoryBuild(builder, req.UnitKey, req.Count, s.Catalog)
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

// stockpileAliasName reports whether a product name is one of the two
// stockpile aliases the ordinary submission helper routes to BUILDWEAPON: the
// name contains `MAKENUKE` or `MAKEANTI`, compared case-sensitively
// [07 R-P0-11 §1]. Stock build pages name
// the toys ARMMAKEANTI, EMPMAKENUKE and the like; ProTA 4.8 names its
// pseudo-products MAKENUKEARM, MAKEANTICOR and the like.
func stockpileAliasName(name string) bool {
	return strings.Contains(name, "MAKENUKE") || strings.Contains(name, "MAKEANTI")
}

// queueStockpileRounds is the counted BUILDWEAPON insertion shared by the
// build-page toy and the computer player's queue task: a positive count of
// rounds for weapon slot zero, coalesced into the rear segment's tail record
// [07 R-P0-11 §1][06 §11.1]. It refuses a
// slot whose weapon is not a stockpile weapon (orders.StockpileSlotAcceptsBuildWeapon).
func (s *Session) queueStockpileRounds(u *units.Unit, count int, tick uint32) bool {
	id := orders.Lookup("BuildWeapon")
	if u == nil || id == 0 || count <= 0 {
		return false
	}
	// The UI alias path always supplies zero, which is where shipped
	// stockpile weapons live [06 §11.1][06 R-WPN-05 §2].
	const stockpileAliasSlot = 0
	if !orders.StockpileSlotAcceptsBuildWeapon(u, stockpileAliasSlot) {
		return false
	}
	s.bindOrderQueue(u)
	// The queued/non-queued argument is NOT the click's Shift bit: the
	// world-order shift chain does not participate on the counted path
	// [07 R-P0-11 §1], and this producer issues no Replace, so it never
	// purges. The argument is inert for a rear-segment record in any case
	// — the caption clear is never called for BUILDWEAPON [04 R-ORD-01 §1].
	n := orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false)
	n.Param1, n.Param2 = uint32(stockpileAliasSlot), uint32(count)
	q := orders.QueueForUnit(u)
	if q == nil {
		return false
	}
	q.CoalesceTail(id, n)
	return true
}
