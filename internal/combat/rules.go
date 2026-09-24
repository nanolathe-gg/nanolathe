package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Rules is the gameplay policy seam of the combat service. Strict 3.1 answers
// exactly as retail; Modern carries the approved Nanolathe policies of
// docs/DESIGN_WEAPONS_PROJECTILES.md §2.3.1 and §2.6.1. Each method is asked at
// the decision boundary owned by the affected request;
// binding a rule set chooses the owning package's documented algorithms.
//
// An implementation is either zero size or used by pointer, and no answer may
// draw from a stream it was not handed, so a bound rule set costs one indirect
// call and no allocation per question [I4].
type Rules interface {
	// WeaponReloadTime derives the stored word at battle entry (CP-DMG-5).
	WeaponReloadTime(s *Service, weapon *content.WeaponDef) int32
	// AreaVictims enumerates one blast cell's unit selectors. The callback
	// completes one recipient before the next selector is read, preserving the
	// retail read-after-hit order; Community extends the selector set and
	// Modern may lift only its overflow capacity.
	AreaVictims(AreaVictimQuery, func(pool.Handle))
	// AreaIndexTick eagerly rebuilds CP-DMG-1's tick-stamped footprint index at
	// the session-owned authoritative boundary. Strict 3.1 is a no-op.
	AreaIndexTick(AreaVictimQuery)
	// OffMapAircraftMargin is the common selector for CP-ENV-1's three combat
	// decisions. Zero preserves Strict 3.1; Community returns its projected
	// table parameter in map cells.
	OffMapAircraftMargin(*Service) int32
	// TransportDeathEnabled gates CP-DMG-3 lifecycle capture under the selected
	// rule set; DeathWeapon chooses the cause-specific explosion at death.
	TransportDeathEnabled(bool) bool
	DeathWeapon(DeathWeaponRequest) *content.WeaponDef
	// VeteranLevel resolves the stored kill word through the selected gameplay
	// policy. Unbounded is used only by consumers whose retail arithmetic has
	// no five-tier cap.
	VeteranLevel(VeteranLevelRequest) uint32
	// VeteranLeadAdmitted resolves the one strict-comparison veterancy gate.
	VeteranLeadAdmitted(VeteranRequest) bool
	// VeteranSpreadDivisor resolves the accuracy divisor before its >1 gate.
	VeteranSpreadDivisor(VeteranRequest) uint32
	// CorpseVelocity may seed the placed feature's existing gravity integration.
	CorpseVelocity(s *Service, position, velocity Vec3, terrain *world.Terrain) Vec3
	// InterceptorCoverage tests the stored aim point against coverage.
	InterceptorCoverage(s *Service, aim, origin Vec3, coverage int32) bool
	// InterceptorRingRadius resolves the unscaled minimap radius at publication.
	InterceptorRingRadius(s *Service, coverage int32) int32
	// AutonomousSlot applies the policy to the existing maintenance admission.
	AutonomousSlot(weapon *content.WeaponDef, ownerControlByte uint8) bool
	// SelectTarget chooses among the existing registry query's contacts.
	SelectTarget(s *Service, q *TargetQuery) (pool.Handle, bool)
	// ReconsiderTarget bypasses only the autonomous maintenance retention shortcut.
	ReconsiderTarget(shooter *units.Unit, slot *units.Slot, target *units.Unit, vis *visibility.Service) bool
	// CombatTick supplies the time of a decision without a second clock.
	CombatTick(s *Service, tick uint32, afterProjectiles bool)
	// ObserveDanger forwards a hostile launch or accepted damage observation.
	ObserveDanger(s *Service, victim, attacker *units.Unit, tick uint32)
	// ObserveImpact also carries the victim's observed hit direction. It does
	// not grant knowledge of an unseen attacker's location.
	ObserveImpact(s *Service, victim, attacker *units.Unit, in DamageInput, tick uint32)
	// Launched records only a successfully created projectile, never an aim.
	Launched(s *Service, h pool.Handle, q *ShotQuery)

	// AdmitTarget answers the shared unit-target medium and air gate before
	// ballistic feasibility and range. StrictRules preserves the retail ladder;
	// CommunityRules applies CP-WPN-1..3 when the session feature is enabled.
	AdmitTarget(q TargetAdmission) bool
	// SlotMayFire is asked after reload decrement and before aim-time work.
	SlotMayFire(s *Service, u *units.Unit, weapon *content.WeaponDef, terrain *world.Terrain) bool
	// DetonationBroadcast answers whether central impact should run the
	// area-damage-and-broadcast call. The same answer drives map markers.
	DetonationBroadcast(s *Service, p *Projectile, weapon *content.WeaponDef) bool
	// GuidanceAdmitted answers the water-medium gate for a self-propelled
	// projectile. preMotionY is the signed whole-world position word and sea is
	// the map's zero-extended sea-level byte.
	GuidanceAdmitted(s *Service, p *Projectile, weapon *content.WeaponDef, preMotionY int16, sea uint8) bool
	// ShotTimeAdmitted answers the physical shot gate after a target point is
	// resolved. Strict evaluates range, shooter medium and ballistic solution;
	// Community may short-circuit the latter two clauses for CP-WPN-3.
	ShotTimeAdmitted(q ShotTimeAdmission) bool

	// AdmitShot reports whether one resolved fire attempt may launch.
	// StrictRules returns true without work: retail admits the shot and never
	// samples terrain [06 R-WPN-05 §1]. ModernRules runs the
	// DESIGN_WEAPONS_PROJECTILES §2.3.1 preview and records its verdict on the
	// query.
	AdmitShot(q *ShotQuery) bool

	// HoldsFire reports whether the shooter's standing Hold Fire suppresses one
	// piece of weapon work: a slot visit's aim and launch, a spawner attempt, or
	// a parked burst remainder (DESIGN_WEAPONS_PROJECTILES §2.6.1). ordered says
	// the work belongs to a slot an order holds — the slot control byte's
	// autonomy bit is clear [06 R-WPN-05 §3] — rather than to a target the unit
	// took on its own. StrictRules returns false: no retail weapon path reads
	// the standing fire field [04 R-STANCE-01 §3].
	HoldsFire(u *units.Unit, ordered bool) bool
}

// AreaVictimQuery is one cell visit in the shared area-damage walk. Rules must
// not retain it or the callback [06 §9.3][CP-DMG-1].
type AreaVictimQuery struct {
	Service *Service
	World   *units.World
	Terrain *world.Terrain
	Tick    uint32
	CellX   int32
	CellZ   int32
}

// ShotTimeAdmission is one request at the physical shot-time gate. Target is
// a world point, so it deliberately carries no target-unit state. Rules must
// not retain it [06 R-WPN-05 §9].
type ShotTimeAdmission struct {
	Service *Service
	Shooter *units.Unit
	Weapon  *content.WeaponDef
	Target  Vec3
	Terrain *world.Terrain
}

// TargetAdmission is one unit-to-unit target check at the shared boundary of
// autonomous acquisition, damage reaction and order installation. Heights are
// already truncated to the whole-world words the retail gate consumes.
// Rules must not retain it.
type TargetAdmission struct {
	Service *Service
	Weapon  *content.WeaponDef
	Shooter TargetAdmissionEnd
	Target  TargetAdmissionEnd
	Sea     int32
	// WaterWeapon and ToAir retain the value-only Acquisition fixture API when
	// Weapon is nil. Authoritative requests carry Weapon, whose flags win.
	WaterWeapon, ToAir bool
}

// ShotQuery is one resolved fire attempt put to Rules.AdmitShot. It carries
// exactly what the pipeline knows at the admission point: the post-spread
// per-shot slot copy, the muzzle the synchronous query returned, the resolved
// aim point, the current tick, the map terrain, the resolved unit target (nil
// for a point target) and the session wind whose phase-8 deadline bounds future
// wind knowledge [01 §7.3].
//
// The service reuses its own query storage and saves/restores it across nested
// fire attempts, so interface dispatch allocates nothing per shot. Rules must
// not retain the query. Launch, Muzzle and Aim are filled by the
// spawner once the muzzle query and accuracy spread have run [06 §4.4]; Blocked
// is the answer's output, read by the caller after the attempt returns.
type ShotQuery struct {
	Service *Service
	World   *units.World
	Shooter *units.Unit
	// Covered is a tactical hold, distinct from physical obstruction.
	Covered bool

	// Launch is the pipeline's per-shot slot copy, carrying the spread-adjusted
	// angles and the ballistic distance word the preview re-solves from.
	Launch Slot
	// Muzzle is the FORCED fire-time muzzle point, not the aim origin
	// [06 §4.1].
	Muzzle Vec3
	// Aim is the resolved aim point the creators derive yaw and pitch from
	// [06 §6.3].
	Aim  Vec3
	Tick uint32

	Terrain *world.Terrain
	// Target is the resolved unit target, or nil for a point target. Only its
	// current geometry and motion feed Modern's bounded hit-confidence estimate.
	Target *units.Unit
	Wind   *world.Wind

	// Blocked reports that the rule set proved terrain obstruction before the
	// resolved target. A refused launch keeps the relative Aim pair, so the
	// caller reads this to decide whether to copy the slot's angles back
	// [06 R-WPN-05 §4].
	Blocked bool
}

// StrictRules is the retail rule set: every question answers as the executable
// does, with no work and no state. It is zero size, so converting a value to
// Rules never allocates.
type StrictRules struct{}

// CommunityRules is the reserved Community 3.9 layer. It embeds StrictRules so
// every unchanged answer remains retail's; Community contracts override only
// the questions they own without changing the baseline or Modern's overrides.
type CommunityRules struct{ StrictRules }

// AreaVictims reads the two stock occupancy words in order. The first
// callback returns before the second word is read, because damage may replace
// either occupant during the visit [06 §9.3].
func (StrictRules) AreaVictims(q AreaVictimQuery, visit func(pool.Handle)) {
	if q.Terrain == nil || visit == nil {
		return
	}
	cell := q.Terrain.PlotAt(q.CellX, q.CellZ)
	if cell == nil {
		return
	}
	if word := cell.OccupantA(); word > 0 {
		visit(pool.Handle(word))
	}
	if word := cell.OccupantB(); word > 0 {
		visit(pool.Handle(word))
	}
}

func (StrictRules) AreaIndexTick(AreaVictimQuery) {}

func (StrictRules) OffMapAircraftMargin(*Service) int32 { return 0 }

// AreaVictims adds the source-defined six overflow selectors when CP-DMG-1
// is enabled. Community retains the six-entry saturation limit.
func (CommunityRules) AreaVictims(q AreaVictimQuery, visit func(pool.Handle)) {
	communityAreaVictims(q, visit)
}

func (CommunityRules) AreaIndexTick(q AreaVictimQuery) {
	if q.Service != nil && q.Service.Community.AreaDamageOverflow {
		q.Service.ensureCommunityAreaIndex(q.World, q.Terrain, q.Tick, communityAreaOverflowSlots)
	}
}

func (CommunityRules) OffMapAircraftMargin(s *Service) int32 {
	if s == nil || s.Community.OffMapAircraftMarginTiles <= 0 {
		return 0
	}
	return int32(s.Community.OffMapAircraftMarginTiles)
}

// VeteranLevelRequest is one veterancy-level lookup. Kills is already narrowed
// to the unsigned stored word; rules must not retain the definition or request.
type VeteranLevelRequest struct {
	Service    *Service
	Definition *content.UnitDef
	Kills      uint16
	Unbounded  bool
}

// VeteranRequest carries the same stored inputs for the lead and spread
// consumers, whose answers are not levels.
type VeteranRequest struct {
	Service    *Service
	Definition *content.UnitDef
	Kills      uint16
}

// VeteranLevel is Strict 3.1's shared kill-word reader. The unbounded form is
// the capture factor; every combat consumer asks for the bounded form.
func (StrictRules) VeteranLevel(q VeteranLevelRequest) uint32 {
	level := uint32(q.Kills) / 5
	if !q.Unbounded && level > 5 {
		level = 5
	}
	return level
}

// VeteranLeadAdmitted is retail's unique strict comparison: the sixth kill is
// the first one that enables pre-fire lead [06 §3.3][06 R-DMG-01 §8].
func (StrictRules) VeteranLeadAdmitted(q VeteranRequest) bool { return q.Kills > 5 }

// VeteranSpreadDivisor is the retail accuracy divisor before the caller's
// greater-than-one test [06 §4.4][06 R-WPN-03 §4].
func (StrictRules) VeteranSpreadDivisor(q VeteranRequest) uint32 { return uint32(q.Kills) / 12 }

// AdmitShot admits every resolved attempt. Retail's admission gate consults
// range, medium and aim readiness and never samples terrain along the flight
// path [06 R-WPN-05 §1].
func (StrictRules) AdmitShot(*ShotQuery) bool { return true }

// HoldsFire never suppresses. No retail weapon-slot, aim, shot-admission or
// projectile path reads the standing fire field: the field gates acquisition
// and the auto-engage issuer, never a target already installed
// [04 R-STANCE-01 §2] [04 R-STANCE-01 §3].
func (StrictRules) HoldsFire(*units.Unit, bool) bool { return false }

func (StrictRules) AdmitTarget(q TargetAdmission) bool {
	waterWeapon, toAir := q.WaterWeapon, q.ToAir
	if q.Weapon != nil {
		waterWeapon, toAir = q.Weapon.WaterWeapon, q.Weapon.ToAirWeapon
	}
	return unitToUnitAdmitsBeforeRange(q.Shooter, q.Target, q.Sea, waterWeapon, toAir)
}

func (StrictRules) SlotMayFire(*Service, *units.Unit, *content.WeaponDef, *world.Terrain) bool {
	return true
}

func (StrictRules) DetonationBroadcast(*Service, *Projectile, *content.WeaponDef) bool {
	return true
}

func (StrictRules) GuidanceAdmitted(_ *Service, _ *Projectile, weapon *content.WeaponDef, preMotionY int16, sea uint8) bool {
	return weapon != nil && (!weapon.WaterWeapon || int32(preMotionY) < int32(sea))
}

func (StrictRules) ShotTimeAdmitted(q ShotTimeAdmission) bool {
	return strictShotTimeAdmits(q)
}

// strictRules is the shared retail answer handed back for an unbound service.
// Holding the interface value in a package variable keeps the substitution off
// the per-question path entirely.
var strictRules Rules = StrictRules{}

// rules returns the service's bound rule set. A nil field is Strict 3.1, so a
// fixture that builds a Service without rules keeps the retail path.
func (s *Service) rules() Rules {
	if s == nil || s.Rules == nil {
		return strictRules
	}
	return s.Rules
}

// shotPreviewer is the optional extension a rule set implements when its
// AdmitShot inspects the post-spread launch copy rather than answering from the
// attempt alone. The spawner then runs the accuracy spread on value copies, so
// a refusal leaves the shared simulation stream and the slot's stored angles
// exactly where retail left them [06 §4.4]. A rule set that does not implement
// it is never asked to preview and pays nothing: the retail path builds no
// query and takes no copies.
type shotPreviewer interface {
	previewsShot() bool
}

// previewsShot reports whether the bound rule set wants the speculative
// preview described on shotPreviewer.
func previewsShot(r Rules) bool {
	p, ok := r.(shotPreviewer)
	return ok && p.previewsShot()
}

func (StrictRules) SelectTarget(_ *Service, q *TargetQuery) (pool.Handle, bool) {
	return acquireFilteredTarget(q.Candidates, q.Acquisition)
}
func (StrictRules) ReconsiderTarget(*units.Unit, *units.Slot, *units.Unit, *visibility.Service) bool {
	return false
}
func (StrictRules) CombatTick(*Service, uint32, bool)                        {}
func (StrictRules) ObserveDanger(*Service, *units.Unit, *units.Unit, uint32) {}

func (StrictRules) ObserveImpact(*Service, *units.Unit, *units.Unit, DamageInput, uint32) {}
func (StrictRules) Launched(*Service, pool.Handle, *ShotQuery)                            {}

func (StrictRules) AutonomousSlot(w *content.WeaponDef, owner uint8) bool {
	return AutonomousScanAdmitsSlot(w, owner)
}
