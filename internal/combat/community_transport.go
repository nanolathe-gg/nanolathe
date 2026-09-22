package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TransportDeathState retains the two pieces of transient death context used
// by the Community transported-explosion policy. The state belongs to one
// combat service; rules receive it in a request and never retain it
// (DESIGN_COMMUNITY_PATCH §4.2, CP-DMG-3).
type TransportDeathState struct {
	passengers []transportDeathPassenger
	pending    *units.Unit
	lastTick   uint32
	hasTick    bool
}

type transportDeathPassenger struct {
	unit   *units.Unit
	handle pool.Handle
	typeID uint32
	health int32
}

// Tick prunes passenger captures at the authoritative game-time boundary.
// Captures otherwise persist across ticks. Rewinding game time clears both
// capture forms before examining any record
// (research/extensions/community-patch-engine.md, CP-DMG-3).
func (s *TransportDeathState) Tick(tick uint32) {
	if s == nil {
		return
	}
	if s.hasTick && tick < s.lastTick {
		s.passengers = s.passengers[:0]
		s.pending = nil
	}
	s.lastTick = tick
	s.hasTick = true

	kept := s.passengers[:0]
	for _, captured := range s.passengers {
		u := captured.unit
		identityChanged := u == nil || u.Handle != captured.handle || u.Def == nil || u.Def.UnitDefID != captured.typeID
		if identityChanged {
			continue
		}
		// Health is not identity. It only proves that a still-living unit
		// survived damage observed after capture; a pending death retains the
		// capture even when that damage changed health.
		if !u.Dying && u.Health > 0 && u.Health != captured.health {
			continue
		}
		kept = append(kept, captured)
	}
	s.passengers = kept
}

// CapturePreDeath records a currently carried unit immediately before the
// shared death path. There is one pending pointer for the whole service, so a
// nested pre-death capture replaces the outer one (CP-DMG-3).
func (s *TransportDeathState) CapturePreDeath(u *units.Unit) {
	if s == nil {
		return
	}
	s.pending = nil
	if u != nil && u.Attachment.Carrier != 0 {
		s.pending = u
	}
}

// CapturePassenger records a passenger immediately before the transport-death
// cascade damages it. A repeated capture of the same unit refreshes its slot,
// type and health snapshot without changing deterministic record order
// (CP-DMG-3).
func (s *TransportDeathState) CapturePassenger(u *units.Unit) {
	if s == nil || u == nil || u.Def == nil {
		return
	}
	captured := transportDeathPassenger{
		unit:   u,
		handle: u.Handle,
		typeID: u.Def.UnitDefID,
		health: u.Health,
	}
	for i := range s.passengers {
		if s.passengers[i].unit == u {
			s.passengers[i] = captured
			return
		}
	}
	s.passengers = append(s.passengers, captured)
}

// ClearPostDecision clears the single pre-death pointer after the ordinary
// death-explosion decision. Passenger captures have their own lifetime.
func (s *TransportDeathState) ClearPostDecision() {
	if s != nil {
		s.pending = nil
	}
}

// WasTransportedDeath answers the three Community qualification conditions at
// death-weapon selection. A passenger record for u is always consumed before
// the pending pointer is cleared, even when current carriage or the pending
// pointer already makes the answer true (CP-DMG-3).
func (s *TransportDeathState) WasTransportedDeath(u *units.Unit) bool {
	if u == nil {
		return false
	}
	currentlyTransported := u.Attachment.Carrier != 0
	if s == nil {
		return currentlyTransported
	}
	wasTransportedAtDeath := s.pending == u
	wasCapturedPassenger := s.consumePassenger(u)
	s.pending = nil
	return currentlyTransported || wasTransportedAtDeath || wasCapturedPassenger
}

func (s *TransportDeathState) consumePassenger(u *units.Unit) bool {
	for i := range s.passengers {
		captured := s.passengers[i]
		if captured.unit != u {
			continue
		}
		copy(s.passengers[i:], s.passengers[i+1:])
		s.passengers = s.passengers[:len(s.passengers)-1]
		return u.Def != nil && u.Handle == captured.handle && u.Def.UnitDefID == captured.typeID
	}
	return false
}

// DeathWeaponRequest is one death-explosion selection. Enabled is the
// session's already-resolved CP-DMG-3 feature answer; State belongs to the
// supplied combat service. Rules must not retain the request or its pointers.
type DeathWeaponRequest struct {
	State   *TransportDeathState
	Unit    *units.Unit
	Cause   Cause
	Enabled bool
}

// TransportDeathEnabled admits the CP-DMG-3 lifecycle hooks. Strict ignores
// the resolved Community table, so parsed transported keys and capture state
// cannot affect Strict 3.1.
func (StrictRules) TransportDeathEnabled(bool) bool { return false }

// TransportDeathEnabled admits lifecycle hooks only when the resolved feature
// table enables them. Modern inherits this Community answer.
func (CommunityRules) TransportDeathEnabled(enabled bool) bool { return enabled }

// DeathWeapon keeps the retail cause-specific choice in Strict 3.1.
func (StrictRules) DeathWeapon(q DeathWeaponRequest) *content.WeaponDef {
	if q.Unit == nil {
		return SelectDeathExplosionWeapon(nil, q.Cause)
	}
	return SelectDeathExplosionWeapon(q.Unit.Def, q.Cause)
}

// DeathWeapon applies the independent transported kill/self-destruct override
// only for an enabled qualifying death. Empty and unresolved overrides are nil
// compiled links and fall back to the same retail selector (CP-DMG-3).
func (CommunityRules) DeathWeapon(q DeathWeaponRequest) *content.WeaponDef {
	stock := StrictRules{}.DeathWeapon(q)
	if !q.Enabled || q.Unit == nil || q.Unit.Def == nil || !q.State.WasTransportedDeath(q.Unit) {
		return stock
	}
	if q.Cause == CauseSelfDestruct {
		if q.Unit.Def.TransportedSelfDestructAsDef != nil {
			return q.Unit.Def.TransportedSelfDestructAsDef
		}
		return stock
	}
	if q.Unit.Def.TransportedExplodeAsDef != nil {
		return q.Unit.Def.TransportedExplodeAsDef
	}
	return stock
}
