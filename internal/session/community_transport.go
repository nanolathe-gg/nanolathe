package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func (s *Session) transportDeathEnabled() bool {
	return s != nil && s.Combat != nil && s.Rules.Combat != nil && s.Rules.Combat.TransportDeathEnabled(s.Community.TransportedExplosions)
}

func (s *Session) tickTransportDeaths(tick uint32) {
	if s.transportDeathEnabled() {
		s.Combat.TransportDeaths.Tick(tick)
	}
}

func (s *Session) captureTransportPreDeath(u *units.Unit) {
	if s.transportDeathEnabled() {
		s.Combat.TransportDeaths.CapturePreDeath(u)
	}
}

func (s *Session) captureTransportPassenger(u *units.Unit) {
	if s.transportDeathEnabled() {
		s.Combat.TransportDeaths.CapturePassenger(u)
	}
}

func (s *Session) clearTransportDeathDecision() {
	if s.Combat != nil {
		s.Combat.TransportDeaths.ClearPostDecision()
	}
}

func (s *Session) deathExplosionWeapon(u *units.Unit, cause combat.Cause) *content.WeaponDef {
	if u == nil {
		return nil
	}
	if s.Combat == nil || s.Rules.Combat == nil {
		return combat.SelectDeathExplosionWeapon(u.Def, cause)
	}
	return s.Rules.Combat.DeathWeapon(combat.DeathWeaponRequest{
		State: &s.Combat.TransportDeaths, Unit: u, Cause: cause,
		Enabled: s.Community.TransportedExplosions,
	})
}
