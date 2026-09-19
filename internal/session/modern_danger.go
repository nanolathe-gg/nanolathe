package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The combat and order policies share observations through session composition,
// never by consulting presentation or installing another policy registry.
// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern danger response".
func (s *Session) noticeModernDanger(victim, attacker *units.Unit, tick uint32) {
	if !s.dangerVisible(victim, attacker) {
		return
	}
	s.bindOrderQueue(victim)
	orders.ObserveDanger(victim, attacker, tick)
}

func (s *Session) noticeModernImpact(victim, attacker *units.Unit, bearing numeric.Angle, tick uint32) {
	if s == nil || s.Econ == nil || victim == nil || attacker == nil ||
		victim.Owner == attacker.Owner || s.Econ.DeclaresAlliance(victim.Owner, attacker.Owner) ||
		s.dangerVisible(victim, attacker) {
		return // visible contacts were already delivered through DangerNotice
	}
	s.bindOrderQueue(victim)
	orders.ObserveImpact(victim, bearing, tick)
}

func (s *Session) dangerVisible(observer, target *units.Unit) bool {
	return s != nil && s.Econ != nil && observer != nil && target != nil &&
		observer.Def != nil && target.Def != nil && observer.Alive && !observer.Dying &&
		target.Alive && !target.Dying && observer.Owner != target.Owner &&
		!s.Econ.DeclaresAlliance(observer.Owner, target.Owner) &&
		s.IsUnitVisible(int(observer.Owner), target)
}

func (s *Session) dangerCanRespond(observer, target *units.Unit, slot int) bool {
	return s.dangerVisible(observer, target) &&
		combat.ModernResponseAdmits(observer, target, slot, s.World, s.Catalog)
}

func (s *Session) dangerStepFeasible(u *units.Unit, x, z numeric.Fixed) bool {
	return s != nil && s.Movement != nil && s.Movement.DangerStepFeasible(u, x, z)
}

func (s *Session) dangerRouteFeasible(u *units.Unit, x, z numeric.Fixed) bool {
	return s != nil && s.Movement != nil && s.Movement.DangerRouteFeasible(u, x, z)
}
