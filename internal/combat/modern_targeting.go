package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TargetQuery is reusable service-owned storage for one acquisition request.
// Neither the policy nor its caller retains the candidate scratch afterwards.
type TargetQuery struct {
	Candidates  []Candidate
	Acquisition Acquisition
	Shooter     *units.Unit
	Slot        *units.Slot
	Index       int
	World       *units.World
	Terrain     *world.Terrain
	Visibility  *visibility.Service
	Economy     *economy.Service
	Catalog     *content.Catalog
}

// All tuning in this file is Nanolathe Modern policy, documented in
// docs/DESIGN_WEAPONS_PROJECTILES.md "Modern threat targeting and incoming fire".
const modernIncomingHorizon = 6
const modernAllyRadius = 256

// incomingShot is deliberately separate from the retail record. Identity is
// an allocation's Unit pointer, never a new generation tag on retail handles.
// Reserve clears it and compaction moves it. A restored service starts empty.
type incomingShot struct {
	target, shooter *units.Unit
	weapon          *content.WeaponDef
	motion          modernBeamMotion
	beamInvalid     bool
}

func (*ModernRules) ReconsiderTarget() bool { return true }
func (*ModernRules) CombatTick(s *Service, tick uint32, afterProjectiles bool) {
	s.modernTick = tick
	if afterProjectiles {
		s.modernNextProjectileTick = tick + 1
	}
}

func (*ModernRules) ObserveDanger(s *Service, victim, attacker *units.Unit, tick uint32) {
	if s == nil || s.DangerNotice == nil || victim == nil || attacker == nil ||
		victim == attacker || !victim.Alive || victim.Dying || !attacker.Alive || attacker.Dying ||
		victim.Owner == attacker.Owner || victim.Def == nil || attacker.Def == nil {
		return
	}
	// A composed session supplies the alliance row. Without it, fail closed.
	if s.Reaction == nil || s.Reaction.Allied == nil || s.Reaction.Allied(victim.Owner, attacker.Owner) {
		return
	}
	s.DangerNotice(victim, attacker, tick)
}

func (r *ModernRules) Launched(s *Service, h pool.Handle, q *ShotQuery) {
	if s == nil || q == nil || q.Target == nil || q.Shooter == nil || !s.Alive(h) {
		return
	}
	s.incoming[int(h)-1] = incomingShot{target: q.Target, shooter: q.Shooter, weapon: q.Launch.Weapon, motion: beamMotion(q.Target)}
	r.ObserveDanger(s, q.Target, q.Shooter, q.Tick)
}

// ModernResponseAdmits is the out-of-range response suitability gate. Session
// checks knowledge and hostility; orders owns pursuit distance and provenance.
// Bad categories remain a preference in acquisition, but a response does not
// start a pursuit with a weapon explicitly disfavored for that target.
func ModernResponseAdmits(shooter, target *units.Unit, idx int, terrain *world.Terrain, catalog *content.Catalog) bool {
	if shooter == nil || target == nil || shooter.Def == nil || target.Def == nil || !target.Alive || target.Dying {
		return false
	}
	slot := orderSlot(shooter, idx)
	if slot == nil || !slot.IsEnabled() || slot.Weapon == nil || slot.Weapon.CommandFire || slot.Weapon.Interceptor {
		return false
	}
	weapon := slot.Weapon
	if modernEffectiveDamage(weapon, target, shooter, false, false) <= 0 || (weapon.Paralyzer && (target.Stunned || target.Def.ImmuneToParalyzer)) {
		return false
	}
	if catalog != nil && !IsPreferredCategoryMask(target.Def.DefinitionMask(), badMaskForSlot(shooter.Def, idx)) {
		return false
	}
	sea := int32(0)
	if terrain != nil {
		sea = int32(terrain.SeaLevel)
	}
	return unitToUnitAdmitsBeforeRange(gateEndForUnit(shooter), gateEndForUnit(target), sea, weapon.WaterWeapon, weapon.ToAirWeapon)
}

// Only straight ordinary shots whose first impact directly damages the target
// are counted. Small-area direct hits take full damage [06 §9.1]; splash,
// bursts, homing, ballistic, paralyzer and persistent effects fail open.
func modernReliableWeapon(w *content.WeaponDef) bool {
	return w != nil && liveCreationFamilyForWeapon(w) == CreationOrdinary && MotionFamilyForWeapon(w) == MotionDirect &&
		w.WeaponVelocity > 0 && w.Range > 0 && w.Range < 32768 && uint32(w.AreaOfEffect) <= 16 &&
		w.Burst == 0 && !w.NoExplode && !w.GroundBounce && !w.Paralyzer && !w.Interceptor && !w.WaterWeapon
}

// reliableETA predicts copies through the existing motion/contact arithmetic,
// never a live tick, event, damage or RNG path. Ordinary shots require static
// geometry; precise beams use the bounded ground-motion estimate below. Both
// are confidence estimates: later movement can still invalidate a forecast.
func (s *Service) reliableETA(p Projectile, weapon *content.WeaponDef, target *units.Unit, w *units.World, terrain *world.Terrain, tick uint32) int {
	if modernBeamWeapon(weapon) && modernBeamTarget(target) {
		return s.beamETA(p, weapon, target, w, terrain, tick)
	}
	if !modernReliableWeapon(weapon) || w == nil || terrain == nil || target == nil || target.Def == nil ||
		!target.Alive || target.Dying || target.Def.BMCode != 0 || target.Attachment.Carrier != 0 || target.Move.Speed != 0 ||
		!terrainPointValid(p.Pos) || !terrainPointValid(p.Velocity) || p.BurstRemaining != 0 {
		return 0
	}
	if tick < s.modernNextProjectileTick {
		tick = s.modernNextProjectileTick
	}
	if tick < p.CreationTick {
		return 0
	}
	for step := 0; step < modernIncomingHorizon; step++ {
		now := tick + uint32(step)
		if now < tick || AdvanceDirect(&p, weapon, now) != AdvanceAlive || !terrainPointValid(p.Pos) {
			return 0
		}
		hit, feature, water, off, ground, bounce := checkCollision(&p, weapon, w, terrain, s.Features, s.OpaqueLiquidMode)
		if hit != 0 {
			if hit == target.Handle {
				return step + 1
			}
			return 0
		}
		if feature != nil || water || off || ground || bounce {
			return 0
		}
	}
	return 0
}

func (s *Service) effectiveDamage(weapon *content.WeaponDef, target, shooter *units.Unit) int64 {
	return modernEffectiveDamage(weapon, target, shooter, s.doubleShot, s.halfShot)
}

func modernEffectiveDamage(weapon *content.WeaponDef, target, shooter *units.Unit, doubleShot, halfShot bool) int64 {
	if weapon == nil || target == nil || target.Def == nil {
		return 0
	}
	kills := int32(0)
	if shooter != nil {
		kills = shooter.Kills
	}
	nominal := weaponNominal(SelectBaseDamage(weapon, target.Def.UnitName), 1, kills, shooter != nil, doubleShot, halfShot)
	// Negative authored damage and narrowing overflow are not kill promises.
	if nominal <= 0 || nominal > 65535 {
		return 0
	}
	amount := scaleAcceptedAmount(nominal, target.Kills, UnitArmored(target), target.Def.DamageModifier)
	if weapon.Paralyzer {
		// Paralyzer credit never enters health subtraction [06 §9.1].
		return int64(amount)
	}
	// Health is a signed word, and the receiver subtracts modulo its width
	// [06 §9.1]. A large packed amount can increase health instead of killing.
	// Only an ordinary, decreasing subtraction supports a damage promise;
	// malformed wider health and wrapped results conservatively promise none.
	health := int32(int16(target.Health))
	if health <= 0 || health != target.Health {
		return 0
	}
	after := ApplyDamage(health, amount)
	if after >= health || after != health-int32(amount) {
		return 0
	}
	return int64(health - after)
}

func (s *Service) incomingDamage(observer, target *units.Unit, w *units.World, terrain *world.Terrain, tick uint32, eta int) int64 {
	if target == nil || target.Health <= 0 || eta <= 0 {
		return 0
	}
	var damage int64
	for i := 0; i < s.Count(); i++ {
		in := &s.incoming[i]
		if in.target != target || in.shooter == nil || in.weapon == nil || !s.Alive(pool.Handle(i+1)) ||
			w.Unit(target.Handle) != target || w.Unit(in.shooter.Handle) != in.shooter {
			continue
		}
		p := s.Records[i]
		if !s.DamageRoutingAdmitted(p.ShooterSide) {
			continue
		}
		if p.TargetUnit != target.Handle || p.Shooter != in.shooter.Handle || p.WeaponID != in.weapon.ID {
			continue
		}
		// Only the observer's own/allied fire can justify withholding its shot.
		if observer.Owner != p.ShooterSide && (s.Reaction == nil || s.Reaction.Allied == nil || !s.Reaction.Allied(observer.Owner, p.ShooterSide)) {
			continue
		}
		if modernBeamWeapon(in.weapon) && target.Def != nil && target.Def.BMCode != 0 {
			if in.motion != beamMotion(target) {
				in.beamInvalid = true
			}
			if in.beamInvalid {
				continue
			}
		}
		arrival := s.reliableETA(p, in.weapon, target, w, terrain, tick)
		if arrival == 0 || arrival > eta {
			continue
		}
		damage += s.effectiveDamage(in.weapon, target, in.shooter)
	}
	return damage
}

// proposedShotWindow compares a new direct shot with existing promises. A
// longer or unproved new trajectory cannot outrun a proved incoming kill within
// the short horizon. It creates no promise of its own until actually launched.
func (s *Service) proposedShotWindow(p Projectile, weapon *content.WeaponDef, target *units.Unit, w *units.World, terrain *world.Terrain, tick uint32) int {
	if !modernReliableWeapon(weapon) || target == nil || target.Def == nil {
		return 0
	}
	beam := modernBeamWeapon(weapon) && modernBeamTarget(target)
	if target.Def.BMCode != 0 && !beam {
		return 0
	}
	if eta := s.reliableETA(p, weapon, target, w, terrain, tick); eta > 0 {
		return eta
	}
	if beam {
		return modernBeamHorizon
	}
	return modernIncomingHorizon
}

func (s *Service) candidateCoverage(q *TargetQuery, target *units.Unit) (int64, int64) {
	if !modernReliableWeapon(q.Slot.Weapon) {
		return 0, 0
	}
	p := Projectile{Shooter: q.Shooter.Handle, ShooterSide: q.Shooter.Owner}
	origin := Vec3{X: q.Shooter.X, Y: q.Shooter.Y, Z: q.Shooter.Z}
	aim := Vec3{X: target.X, Y: target.Y, Z: target.Z}
	InitOrdinary(&p, q.Slot.Weapon, s.modernTick, origin, aim, target.Handle)
	eta := s.proposedShotWindow(p, q.Slot.Weapon, target, q.World, q.Terrain, s.modernTick)
	if eta == 0 {
		return 0, 0
	}
	return s.incomingDamage(q.Shooter, target, q.World, q.Terrain, s.modernTick, eta), s.effectiveDamage(q.Slot.Weapon, target, q.Shooter)
}

func (s *Service) allied(a, b uint8, econ *economy.Service) bool {
	return a == b || registryOwnerDeclaresAllianceWithCandidate(a, b, econ)
}

// threatScore examines at most three enemy slots. A weapon must actually be
// capable of damaging this unit or its currently targeted nearby ally. There
// is no building-name table, or whole-world scan inside the candidate loop.
func (s *Service) threatScore(q *TargetQuery, target *units.Unit) int64 {
	score := int64(1)
	if target.Stunned || target.Remaining != 0 {
		return score
	}
	for idx := 0; idx < NumSlots; idx++ {
		slot := target.SlotAt(idx)
		if slot == nil || !slot.IsEnabled() || slot.Weapon == nil || slot.Weapon.Interceptor || slot.Weapon.CommandFire {
			continue
		}
		weapon := slot.Weapon
		if weapon.Stockpile && slot.Ammo <= 0 {
			continue
		}
		victim := q.Shooter
		active := false
		if slot.Target.Kind == units.TargetUnit {
			ally := q.World.Unit(slot.Target.Unit)
			if ally != nil && ally.Def != nil && s.allied(q.Shooter.Owner, ally.Owner, q.Economy) && WithinRange(q.Shooter.X, q.Shooter.Z, ally.X, ally.Z, modernAllyRadius) && s.CanEngageSlotTarget(target, ally, idx, q.Terrain) {
				victim = ally
				active = true
			}
		}
		if !s.CanEngageSlotTarget(target, victim, idx, q.Terrain) {
			continue
		}
		damage := s.effectiveDamage(weapon, victim, target)
		if damage <= 0 || (weapon.Paralyzer && victim.Def.ImmuneToParalyzer) {
			continue
		}
		reload := int64(weapon.ReloadTime)
		if reload < 1 {
			reload = 1
		}
		strength := damage * 300 / reload
		if strength > 10000 {
			strength = 10000
		}
		value := int64(1000) + strength
		if active {
			value += 1000
		}
		if value > score {
			score = value
		}
	}
	return score
}

func (*ModernRules) SelectTarget(s *Service, q *TargetQuery) (pool.Handle, bool) {
	if q == nil || q.Shooter == nil || q.Slot == nil || q.World == nil {
		return 0, false
	}
	// Modern keeps fire-at-will automatic acquisition, while order requests
	// with an explicit released slot retain their caller's existing stance gate.
	//
	// Modern replaces the sampling and scoring of [06 §3.2], not the medium,
	// range and trajectory gates ("retains the existing medium, range and
	// trajectory gates", docs/DESIGN_WEAPONS_PROJECTILES.md "Modern threat
	// targeting and incoming fire"). The physical gate below therefore compares
	// against the slot weapon's authored range under both rule sets, and the
	// sight-distance caller's `nochasecategory` rejection holds here too — the
	// same mask Modern's own danger response already honors before it proposes
	// an attack. No Modern policy claims the no-chase category; a Modern unit
	// that chased aircraft its Strict twin ignores would be an accident, not a
	// departure.
	//
	// A slot with NO active weapon is outside this policy altogether. The
	// approved Modern threat targeting is scoped to an armed slot — it ranks by
	// the damage "the weapon can cause" — so it has nothing to say about the
	// weaponless kamikaze search that carries a stock mine or crawling bomb
	// [04 R-SPEC-01 §1]. Modern therefore delegates that query to the strict
	// sampled selection, unchanged, rather than duplicating or approximating
	// it; the boundary is recorded in docs/DESIGN_WEAPONS_PROJECTILES.md
	// "Modern threat targeting and incoming fire". This adds no departure: the
	// scan is a detonation TRIGGER, not a weapon aim, and a deterministic
	// ranker feeding `Standby_Mine`'s grounded-target post-check could starve
	// a mine that a sampled search would have fired.
	if q.Slot.Weapon == nil {
		return StrictRules{}.SelectTarget(s, q)
	}

	var best, retained pool.Handle
	var bestScore, retainedScore int64
	var bestDistance int64
	var bestOverage, retainedOverage bool
	for _, c := range q.Candidates {
		target := q.World.Unit(c.Handle)
		if target == nil || target.Def == nil || !c.Hostile || s.allied(q.Shooter.Owner, target.Owner, q.Economy) ||
			!q.Acquisition.admits(c) || q.Acquisition.rejectsNoChase(c) || q.Acquisition.rejectsStunned(c) ||
			s.effectiveDamage(q.Slot.Weapon, target, q.Shooter) <= 0 || (q.Slot.Weapon.Paralyzer && target.Def.ImmuneToParalyzer) {
			continue
		}
		// A registry may be thirty ticks old. Recheck present contact knowledge;
		// never acquire or retain a vanished contact merely because it scored well.
		visible := q.Visibility != nil && q.Visibility.IsVisible(visibility.PlayerID(q.Shooter.Owner), visibilityTarget(target, target.Flags))
		if q.Visibility == nil && s.Visibility != nil {
			visible = s.Visibility(visibility.PlayerID(q.Shooter.Owner), visibilityTarget(target, target.Flags))
		}
		if !visible {
			continue
		}
		incoming, shot := s.candidateCoverage(q, target)
		if target.Health > 0 && incoming >= int64(target.Health) {
			continue
		}
		score := s.threatScore(q, target)
		preferred := IsPreferredCategory(c.Category, q.Acquisition.BadMask)
		if q.Acquisition.MaskResolved && c.CategoryMaskResolved {
			preferred = IsPreferredCategoryMask(c.CategoryMask, q.Acquisition.BadTargetMask)
		}
		if preferred {
			score += 100
		}
		// Twenty percent is a preference only. Undercovered targets remain legal,
		// including a sole indivisible shot that would overshoot by far more.
		overage := target.Health > 0 && shot > 0 && (incoming+shot)*5 > int64(target.Health)*6
		if overage {
			score = score * 4 / 5
		}
		dx, dz := q.Shooter.X.Int()-target.X.Int(), q.Shooter.Z.Int()-target.Z.Int()
		distance := int64(dx)*int64(dx) + int64(dz)*int64(dz)
		if best == 0 || score > bestScore || (score == bestScore && (distance < bestDistance || (distance == bestDistance && c.Handle < best))) {
			best, bestScore, bestDistance = c.Handle, score, distance
			bestOverage = overage
		}
		if q.Slot.Target.Kind == units.TargetUnit && q.Slot.Target.Unit == c.Handle {
			retained, retainedScore = c.Handle, score
			retainedOverage = overage
		}
	}
	// A 25% improvement is required to leave a still-eligible automatic target.
	// Orders can request automatic selection while owning a nonautonomous slot.
	// Explicit bindings remain protected by the maintenance scan admission;
	// this query returns a choice without installing it.
	// Avoid cancelling the 20% overage preference with the 25% retention
	// margin: an otherwise preferred non-overage alternative may take over.
	if retained != 0 && !(retainedOverage && !bestOverage) && bestScore*4 <= retainedScore*5 {
		return retained, true
	}
	return best, best != 0
}

func (*ModernRules) AutonomousSlot(w *content.WeaponDef, _ uint8) bool {
	return w != nil && !w.Dropped && !w.CommandFire
}
