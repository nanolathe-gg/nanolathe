package combat

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// FireSpy records the fixed callback order for testing [06 §4.1] C2.
type FireSpy struct {
	Events []string
}

func (s *FireSpy) Record(ev string) {
	if s == nil {
		return
	}
	s.Events = append(s.Events, ev)
}

func (s *FireSpy) Reset() {
	if s == nil {
		return
	}
	s.Events = s.Events[:0]
}

// FireScript is the COB port for the fire callbacks [06 §4.1] C2.
//
// The callbacks are real script entry points, not names in a log: FirePrimary
// moves the model's barrel, RockUnit applies the recoil the renderer reads.
// A nil port fires no script, which is what a unit with no COB has.
type FireScript interface {
	// FireWeapon runs FirePrimary, FireSecondary or FireTertiary for the slot
	// [06 §4.1] C2.
	FireWeapon(slotIdx int)
	// RockUnit applies the firing recoil [06 §4.1] C2.
	RockUnit(slotIdx int)
}

// FireEvents is the presentation port for the shot's start sound and start
// puff [06 §4.1] [06 §13.2].
type FireEvents interface {
	StartSound(name string)
	StartSmoke(h pool.Handle)
}

// FirePorts carries everything the spawner needs from outside this package.
//
// Slot reload, stockpile ammunition and resource payment are deliberately NOT
// here. The per-slot pipeline owns those steps [06 §4.1] C1 — see TickSlot —
// and the spawner only validates, allocates, initializes and notifies. Both
// layers owning them is how a stockpile launch could consume two rounds.
type FirePorts struct {
	// ShooterSide is the firing unit's side, recorded on the projectile so
	// damage credit and hostility resolve against a real owner [06 §6.1].
	// Shooterless paths (meteors) use NeutralSide (10) instead [06 §6.5].
	ShooterSide uint8

	// Shooter is the firing unit itself, and it is what makes the fill's
	// real-shooter branch reachable from here. [06 §4.1] has the common
	// initializer, for a real shooter, write the shooter's side byte AND its
	// reference and then stamp the shooter's "fired recently" deadline — all
	// before it hands the start-sound identity to the audio layer, which is
	// the first of the fixed callbacks. Neither the family initializers nor
	// TryFire knew the unit, so the reference was bound by the caller after
	// the whole spawner had returned (after the start sound and the
	// Fire/RockUnit callbacks) and the deadline was never stamped at all.
	//
	// nil is the shooterless path (the meteor creator, [06 §6.5]): no
	// reference, no stamp.
	Shooter *units.Unit

	// InterceptorRescan is the fire-time interceptor rescan a vertical-launch
	// executor retains even on a pool-full failure [06 §4.4] C5. nil skips it.
	InterceptorRescan func(slotIdx int, slot *Slot)

	// Origin is the firing unit's world point. It is the muzzle the shot
	// starts from when the COB query yields no piece, which is the normal
	// muzzle path [06 §4.1] C3 — not a stand-in for an unknown position.
	Origin Vec3

	// MuzzlePiece is the synchronous COB query for the slot's muzzle piece,
	// performed before initialization and retained on pool-full
	// [06 §4.1] C3 [06 §4.4] C5. A nil port or a negative result takes the
	// normal muzzle path.
	MuzzlePiece func(slotIdx int) int32

	// MuzzleWorld resolves a piece index to its world point. When it declines,
	// the shot falls back to Origin.
	MuzzleWorld func(piece int32) (Vec3, bool)

	// TargetWorld resolves a live unit target's world point at creation time.
	// The family initializers derive yaw and pitch from muzzle to target
	// [06 §6.3], so a unit target has no trajectory without it.
	TargetWorld func(h pool.Handle) (Vec3, bool)

	// Gravity is the map's gravity, consumed by the ballistic solver
	// [06 §3.3] [06 §6.4].
	Gravity numeric.Fixed

	// Script and Events are the COB and presentation ports. Nil ports are
	// silent; the callback ORDER is the contract either way [06 §4.1] C2.
	Script FireScript
	Events FireEvents

	// RNG is the single global simulation stream. The draw COUNT is behavior
	// [01 §7.1] I4.
	RNG *rng.Simulation

	// ShooterHealth, ShooterMaxHealth and ShooterKills are the three shooter
	// terms of the turret executor's accuracy spread [06 §4.4]
	// [06 R-WPN-03 §4]. They are the shooter's own current and maximum health
	// and its credited-kill count; nothing about the target enters the bound.
	ShooterHealth    int32
	ShooterMaxHealth int32
	ShooterKills     int32

	// Spy observes the callback order for tests. It is an observer only —
	// nothing in the spawner branches on it.
	Spy *FireSpy
}

// TryFire is the family spawner: it validates, allocates a projectile record,
// initializes it through the creation family, and runs the fire callbacks.
//
// Contract transcription per plan C2–C5, C8 with citations:
//
// C2 fire callback order fixed: root allocation → weapon's start sound → matching FirePrimary/Secondary/Tertiary → RockUnit → start smoke [06 §4.1].
// C3 muzzle piece queried synchronously before initialization [06 §4.1].
// C4 fire callbacks not called when pool is full [06 §4.1] [06 §5.1].
// C5 pool-full retains target+trajectory validation, muzzle query, slot-angle mutation, accuracy calc and up to two RNG draws when spread nonzero [06 §4.4] I4.
// C8 burst spawns N pellets plus one silent anchor [06 §4.3].
//
// Validation, muzzle query and spread draws are performed before allocation so
// they are retained on pool-full failure [06 §4.4]. Callbacks are suppressed on
// pool-full [06 §4.1] C4.
//
// Reload (C7), stockpile ammunition and the resource debit (C6) are the
// pipeline's, not the spawner's: TickSlot performs them after a successful
// return, in the order its PipelineStep enumeration fixes.
func TryFire(svc *Service, slot *Slot, slotIdx int, tgt Target, tick uint32, ports FirePorts) (pool.Handle, bool) {
	if svc == nil || slot == nil || slot.Weapon == nil {
		return 0, false
	}
	w := slot.Weapon

	// --- retained pre-allocation work [06 §4.4] C5 ---

	// Target and trajectory validation is retained even on pool-full.
	if tgt.Kind == TargetNone {
		return 0, false
	}

	// C3 muzzle piece queried synchronously before initialization [06 §4.1].
	// Identity is stored on the slot and on the record so burst clones can
	// re-query the live muzzle position [06 §4.3] C8.
	var muzzPiece int32 = -1
	if ports.MuzzlePiece != nil {
		muzzPiece = ports.MuzzlePiece(slotIdx)
		slot.MuzzlePiece = muzzPiece // retained even on pool-full [06 §4.4]
	}

	// Resolve the muzzle world point. A negative piece, or a port that
	// declines, takes the normal muzzle path: the firing unit's own position.
	muzzle := ports.Origin
	if muzzPiece >= 0 && ports.MuzzleWorld != nil {
		if pos, ok := ports.MuzzleWorld(muzzPiece); ok {
			muzzle = pos
		}
	}

	// Resolve the aim point. A unit target's position is needed at creation
	// time because the family initializers derive yaw and pitch from it
	// [06 §6.3]; a target that cannot be resolved fails validation.
	var target Vec3
	switch tgt.Kind {
	case TargetPoint:
		target = Vec3{X: tgt.X, Y: tgt.Y, Z: tgt.Z}
	case TargetUnit:
		if ports.TargetWorld == nil {
			return 0, false
		}
		pos, ok := ports.TargetWorld(tgt.Unit)
		if !ok {
			return 0, false
		}
		target = pos
	}

	// The accuracy spread. It lives in the **turret** executor and only there
	// [06 §4.4] [06 R-WPN-03 §4]: a weapon without `turret` reaches the same
	// ordinary creator, computes no spread and consumes no randomness at all.
	// Both draws use the same computed bound, yaw first, and the shared
	// simulation generator returns zero without advancing for a bound below
	// two — so a bound of exactly 1 passes the nonzero test and draws nothing.
	//
	// The site is fixed: after the muzzle query above and before the creator
	// below, so the two draws are consumed even when the allocation then fails
	// [06 §4.4] C5 I4.
	//
	// Correction (WU-19-2): this block used to draw twice bounded by the
	// authored `sprayangle`, on the reading that the accuracy family were dead
	// stores. [06 R-WPN-03 §1]'s whole-image census retracts that: `accuracy`
	// is this spread's base term, and `sprayangle`'s one reader is the burst
	// scheduler's single spray draw in the projectile phase [06 §4.3], which
	// AdvanceBursts already performs. Drawing on it here as well both used the
	// wrong bound and double-counted the field.
	var yawSpread, pitchSpread int32
	if w.Turret {
		bound := AccuracySpreadBound(w.Accuracy, ports.ShooterHealth, ports.ShooterMaxHealth, ports.ShooterKills)
		if bound != 0 && ports.RNG != nil {
			yawSpread = recentred(ports.RNG.Uint32n(uint32(bound)), int32(bound))
			pitchSpread = recentred(ports.RNG.Uint32n(uint32(bound)), int32(bound))
		}
		// The spread lands on the slot's stored angles, and that mutation is
		// retained on a pool-full failure: the next tick's drift gate then
		// measures a freshly solved pair against angles that already carry a
		// draw [06 §4.4 "Retention after a full pool"]. Our stored yaw is
		// already absolute where retail's is relative-plus-heading at this
		// point (see the convention marker in StepWeaponsForUnit), so only the
		// draw is added here.
		if yawSpread != 0 || pitchSpread != 0 {
			slot.DesiredYaw = uint16(int32(slot.DesiredYaw) + yawSpread)
			slot.DesiredPitch = uint16(int32(slot.DesiredPitch) + pitchSpread)
		}
	}

	// The creation family decides whether a record is created at all: a weapon
	// matching none of the six creation predicates makes no projectile
	// [06 §6.2] C15. Deciding before reservation keeps the pool untouched.
	fam := CreationFamilyForWeapon(w)
	if fam == CreationNone {
		return 0, false
	}

	// Ballistic weapons need a launch angle before allocation: a target with no
	// solution is not admitted [06 §3.3] [06 §6.4].
	var solvedPitch, solvedYaw numeric.Angle
	var h pool.Handle
	var ok bool
	// Reserve before solve to reproduce #DE leak per P0-10 [06 §6.4]
	ballisticReserved := false
	if fam == CreationBallistic {
		// Reserve before solve to reproduce #DE leak per P0-10 [06 §6.4]
		h, ok = svc.Reserve()
		if !ok {
			// Pool-full retains trajectory validation without count leak [06 §4.4] C5
			dx := target.X.Sub(muzzle.X)
			dy := target.Y.Sub(muzzle.Y)
			dz := target.Z.Sub(muzzle.Z)
			_, _ = BallisticSolve(dx, dy, dz, numeric.Fixed(int64(w.WeaponVelocity)), ports.Gravity, 0)
			return 0, false
		}
		ballisticReserved = true
		if w.WeaponVelocity == 0 {
			panic("combat: ballistic zero velocity divide fault after reserve [06 §6.4]")
		}
		dx := target.X.Sub(muzzle.X)
		dy := target.Y.Sub(muzzle.Y)
		dz := target.Z.Sub(muzzle.Z)
		raw, solverOk := BallisticSolve(dx, dy, dz, numeric.Fixed(int64(w.WeaponVelocity)), ports.Gravity, 0)
		if !solverOk {
			// No solution: admission gate prevents allocation [06 §3.3]; roll back early reserve for non-fault case
			svc.CancelReserve(h)
			return 0, false
		}
		solvedPitch = numeric.Angle(raw)
		solvedYaw = YawFromDelta(dx, dz)
	}

	// C2 root allocation before callbacks [06 §4.1] [06 §5.1]; the pool-full
	// check precedes common initialization and the Fire/RockUnit callbacks
	// [06 §5.1]. Vertical launch retains its slot-angle rewrite (the muzzle
	// query above) and the fire-time interceptor rescan on failure [06 §4.4] C5.
	if fam == CreationVertical && ports.InterceptorRescan != nil {
		ports.InterceptorRescan(slotIdx, slot)
	}
	if !ballisticReserved {
		h, ok = svc.Reserve()
		if !ok {
			// C4 fire callbacks not called when pool is full [06 §4.1].
			// C5 retained work is already done: muzzle query, angle mutation and
			// spread draws. Suppressed: the record, Fire/RockUnit, start smoke,
			// the shot packet, the pending-slot clear, reload, ammunition, firing
			// state and resource mutation [06 §4.4].
			return 0, false
		}
	}
	idx := int(h) - 1
	p := &svc.Records[idx]

	// Ownership and muzzle identity are read back by the family initializers
	// through InitCommon, so they are set before dispatch [06 §6.1].
	p.ShooterSide = ports.ShooterSide
	p.MuzzlePiece = int16(muzzPiece)

	// The rest of the fill's real-shooter branch [06 §4.1]: the shooter's
	// reference beside its side byte, and then the "fired recently" deadline
	// stamped at current tick + 600. The stamp is written OUTRIGHT — the
	// deadline is one shared word with twelve gameplay writers and retail
	// takes no maximum, so a longer reveal already in progress is shortened by
	// a later shot just as a sensor breach's `tick + 90` shortens it
	// [03 R-VIS-01 §6 "Writer census of the shared deadline"]. Its one
	// gameplay reader is the cloak debit gate's `currentTick >= deadline`
	// term [05 R-ECO-01 §9], so a cloaked unit that fires stops paying — and
	// stays visible — for the next 600 ticks.
	//
	// Site: here rather than at the caller's post-return binding, because the
	// section places both writes inside the common initializer, ahead of the
	// start sound and the Fire/RockUnit callbacks below. It draws no RNG (I4).
	if ports.Shooter != nil {
		p.Shooter = ports.Shooter.Handle
		ports.Shooter.RevealDeadline = tick + 600
	}

	// The dropped creator's two operands: the dropping unit's heading and its
	// DEFINITION's maximum velocity, which together are the whole launch state
	// of a bomb [06 §6.4]. They come from the shooter because there is nowhere
	// else they exist — no weapon field carries either. A shooterless path
	// (the meteor creator, [06 §6.5]) reaches no dropped weapon, so zeros here
	// are unreachable rather than a placeholder.
	var dropperHeading numeric.Angle
	var dropperMaxVelocity numeric.Fixed
	if ports.Shooter != nil {
		dropperHeading = numeric.Angle(ports.Shooter.Move.Heading)
		if def := ports.Shooter.Def; def != nil {
			dropperMaxVelocity = numeric.Fixed(int64(def.MaxVelocity))
		}
	}

	// Family dispatch [06 §6.2] C15: this is what gives the record its
	// position, yaw, pitch, scalar speed, velocity and family expiry.
	InitProjectile(p, w, tick, muzzle, target, tgt.Unit, solvedYaw, solvedPitch, nil, slot.DistanceWord, ports.Gravity, dropperHeading, dropperMaxVelocity)

	// Apply the retained spread to the aimed trajectory, recomputing the
	// velocity components from the perturbed angles through the fixed-point
	// helpers rather than nudging them in Cartesian space.
	//
	// BALLISTIC ONLY [06 R-WPN-05 §5]. The turret executor hands both creators
	// the same muzzle point and the same resolved, lead-adjusted target point
	// it received from the pipeline, and never derives an aim point from the
	// slot's stored angles. The ballistic creator copies the slot's stored yaw
	// and pitch into the record [06 §6.4], so the draw reaches its trajectory;
	// the ordinary creator (`lineofsight` or `selfprop`) recomputes yaw and
	// pitch from the muzzle and the target point [06 §6.3] and reads the
	// slot's stored yaw once, for RockUnit's recoil direction alone. So for a
	// turret weapon of the ordinary family, `accuracy`, the health term and
	// the kill divisor change the recoil and NOTHING else: the shot leaves
	// exactly toward the aim point, at full health or near death. Only
	// `ballistic` turret weapons scatter, and burst clones inherit the root's
	// velocity, so a burst of an ordinary weapon is unjittered too until its
	// own spray [06 §4.3].
	//
	// This corrects [06 R-WPN-03 §4], whose "shape of the bound" paragraph
	// states the effect in firing terms ("fires exactly on its solved
	// angles") and was transcribed here as a nudge to both families. The
	// draws themselves are unchanged: they are still taken at the same site
	// for every turret weapon, so the shared simulation stream is untouched
	// [I4], and the mutation of the slot's stored angles is still retained.
	if fam == CreationBallistic && (yawSpread != 0 || pitchSpread != 0) {
		p.Yaw = numeric.Angle(uint16(int32(p.Yaw) + yawSpread))
		p.Pitch = numeric.Angle(uint16(int32(p.Pitch) + pitchSpread))
		// The rebuild goes through the ballistic launch build, not the bare
		// angle helper: retail's spread mutates the SLOT's stored angles before
		// the creator reads them, so the creator's `T0 × gravity` pre-decrement
		// applies to the perturbed trajectory too [06 §6.4][06 R-WPN-03 §4].
		p.Velocity = ballisticLaunchVelocity(p.Yaw, p.Pitch, p.Speed, slot.DistanceWord, ports.Gravity)
	}

	// Burst state copied from the weapon into the root [06 §4.3] C8.
	p.BurstRemaining = w.Burst
	if w.Burst > 0 {
		p.BurstDeadline = tick + uint32(w.BurstRate) // interval added to next deadline [06 §4.3]
	} else {
		p.BurstDeadline = 0
	}

	// C2 fixed callback order [06 §4.1]: root allocation → start sound →
	// FirePrimary/Secondary/Tertiary → RockUnit → start smoke.
	// The start sound is emitted by the common initializer so it precedes Fire.
	// Successful normal, ballistic and vertical-launch root spawners follow
	// this order; the dropped-family inline allocator emits neither Fire nor
	// RockUnit; the direct meteor path runs only the common initializer; burst
	// clones rerun none [06 §4.1] C2.
	if ports.Spy != nil {
		ports.Spy.Record("alloc")
	}
	if w.SoundStart != "" {
		if ports.Spy != nil {
			ports.Spy.Record("startSound")
		}
		if ports.Events != nil {
			ports.Events.StartSound(w.SoundStart)
		}
	}
	if fam != CreationDropped && fam != CreationMeteor {
		if ports.Spy != nil {
			switch slotIdx {
			case 1:
				ports.Spy.Record("FireSecondary")
			case 2:
				ports.Spy.Record("FireTertiary")
			default:
				ports.Spy.Record("FirePrimary")
			}
			ports.Spy.Record("RockUnit")
		}
		if ports.Script != nil {
			ports.Script.FireWeapon(slotIdx) // [06 §4.1] C2
			ports.Script.RockUnit(slotIdx)   // [06 §4.1] C2
		}
		// The start puff comes only from the three ordinary spawners, after
		// Fire and RockUnit [06 §13.2]; burst, dropped and meteor never emit it.
		if w.StartSmoke {
			if ports.Spy != nil {
				ports.Spy.Record("startSmoke")
			}
			if ports.Events != nil {
				ports.Events.StartSmoke(h)
			}
		}
	}

	return h, true
}

// recentred converts a draw in [0, bound) into a symmetric offset by
// subtracting half the bound, which is the sampling shape [06 §4.3] gives for
// spray and random decay. Halving truncates toward zero (I3), so an odd bound
// leaves the window one step wider on the positive side — that asymmetry is
// the integer division's, not a choice.
func recentred(draw uint32, bound int32) int32 {
	return int32(draw) - bound/2
}

// AdvanceBursts advances burst anchors for the projectile phase [06 §4.3] C8.
//
// While remaining>0 record takes burst branch instead of motion [06 §7.1].
// On each due attempt refreshes position from live muzzle when interval>4 or remaining odd [06 §4.3],
// decrements remaining, advances deadline, tries to append clone.
// Clone copy happens before spray; spray prepares next [06 §4.3].
// Spray is ONE simulation draw per successful clone, bounded by the authored
// sprayangle; it perturbs a scratch heading used only to rebuild the parent's
// velocity X/Z (magnitude weaponvelocity, pitch and velocity Y untouched) and
// is then discarded, so the parent's stored yaw is unchanged for the whole
// burst and every pellet scatters about the ORIGINAL aim [06 §4.3].
// Pool-full clone consumes attempt with no spray and no RNG draw [06 §4.3] C8.
// Anchor dies silently when remaining reaches 0 with no explosion/sound/shake/end smoke/damage [06 §4.3] C8.
// Successful clones consume spray sample when spray configured, including final attempt; final sample written to soon-retired parent [06 §4.3].
// Captures entry count once; clones appended during scan wait next tick [06 §5.1] [01 §6.2] I1.
// Returns number of clones created.
//
// muzzlePos re-runs the piece-to-world conversion for the anchor's own shooter
// with the record's *stored* firing piece; it makes no COB call [06 §4.3]
// [R-P0-07]. It reports false when that conversion cannot be made, in which
// case the anchor keeps the position it already holds. Retail reaches no such
// state: every anchor belongs to a live shooter, because a shooter's death runs
// SweepBurstAnchorsForShooter below [06 §4.3] [06 §5.2]. The branch stands for
// fixtures that drive this without a muzzle port at all.
func (s *Service) AdvanceBursts(tick uint32, simRNG *rng.Simulation, weapons map[int32]*content.WeaponDef, muzzlePos func(shooter pool.Handle, piece int16) (Vec3, bool)) int {
	if s == nil {
		return 0
	}
	entry := s.Count() // capture once at entry [01 §6.2] [06 §5.1] I1
	clones := 0
	for i := 0; i < entry; i++ {
		h := pool.Handle(i + 1)
		// [06 §5.1] the updater does NOT test the dead flag at the top of its
		// captured-span loop: a record marked dead before its turn still takes
		// the burst branch until compaction.
		p := &s.Records[i]
		if p.BurstRemaining <= 0 {
			continue // zero follows ordinary moving path [06 §4.3] C8
		}
		if tick < p.BurstDeadline {
			continue // not due; first attempt due when creation+interval <= tick [06 §4.3]
		}
		// Due attempt: retrieve authored interval and spray params.
		var interval int32
		var sprayAngle int32
		var randomDecay int32
		var wDef *content.WeaponDef
		if weapons != nil {
			if w, ok := weapons[p.WeaponID]; ok && w != nil {
				wDef = w
				interval = w.BurstRate // [02 "Weapon record"] burstRate *30
				sprayAngle = w.SprayAngle
				randomDecay = w.RandomDecay
			}
		}
		// Refresh position from live muzzle when interval>4 or remaining odd [06 §4.3] C8.
		// Condition uses current remaining before decrement [06 §4.3].
		if interval > 4 || (p.BurstRemaining%2 == 1) {
			if muzzlePos != nil {
				if pos, ok := muzzlePos(p.Shooter, p.MuzzlePiece); ok {
					p.Pos = pos
					p.StartPos = pos
				}
			}
		}
		// Decrement remaining and advance deadline [06 §4.3] burst state copy.
		p.BurstRemaining--
		p.BurstDeadline += uint32(interval) // interval added to next deadline [06 §4.3]
		// Try to append clone [06 §4.3].
		cloneH, ok := s.Reserve()
		if !ok {
			// Pool-full failure still consumes attempt: count and deadline already advanced [06 §4.3] C8.
			// No clone, no spray, no RNG draw, no clone-trigger sound [06 §4.3] C8.
			if p.BurstRemaining == 0 {
				// Anchor dies silently when count reaches zero [06 §4.3] C8: dead flag set directly with no removal dispatch.
				s.MarkDead(h)
			}
			continue
		}
		// Successful clone receives full copy of parent, then has creation/expiry state updated and its own remaining cleared [06 §4.3] C8.
		cIdx := int(cloneH) - 1
		clone := &s.Records[cIdx]
		*clone = *p // full copy before spray [06 §4.3] C8
		clone.CreationTick = tick
		clone.BurstRemaining = 0 // ordinary moving projectile [06 §4.3] C8
		clone.BurstDeadline = 0
		clone.Dead = false
		// The clone "has its creation/expiry state updated" after the copy
		// [06 §4.3]: shift the parent's expiry by the elapsed root-to-clone
		// delay so the pellet keeps the parent's remaining lifetime rather
		// than inheriting time already spent.
		if clone.ExpiryTick != 0 && tick != p.CreationTick {
			clone.ExpiryTick += uint32(int64(tick) - int64(p.CreationTick))
		}
		// Ensure clone's position is parent's refreshed position.
		// Clone is ordinary moving projectile on next phase [06 §4.3]; captured entry prevents moving this tick [06 §5.1].

		// Random decay changes the successful clone's expiry [06 §4.3] C8.
		if randomDecay != 0 && simRNG != nil {
			// One draw bounded by the authored field, re-centred by half its
			// bound. [06 §4.3] states the whole expression: the CLONE's expiry
			// becomes `expiry + draw - (randomdecay >> 1)` — a centred expiry
			// jitter on the clone only, with the halving an unsigned 16-bit
			// shift. It lands on the expiry tick, not the velocity, and it is
			// centred, not one-sided; the marker that recorded both as open is
			// retired. The draw is taken first, before the spray.
			d := recentred(simRNG.Uint32n(uint32(randomDecay)), randomDecay)
			if clone.ExpiryTick != 0 {
				clone.ExpiryTick = uint32(int64(clone.ExpiryTick) + int64(d))
			}
		}
		// Spray. The clone copy happens BEFORE spray is calculated, so spray
		// changes the parent/template velocity and prepares the velocity the
		// NEXT successful clone inherits; the first clone uses the root's
		// initially aimed velocity. Every successful attempt consumes a sample
		// when spray is configured, including the final one, whose sample is
		// written to the soon-retired parent and inherited by nobody
		// [06 §4.3] C8.
		//
		// [06 §4.3] correction: earlier text had this rewriting the parent's
		// stored heading to heading-sprayAngle/2+draw over two draws (yaw and
		// pitch). It does not: there is one draw, bounded by sprayangle, and
		// the perturbed heading it feeds is computed into a scratch value and
		// discarded — never written back to the parent's stored yaw. Pitch is
		// never jittered. So every pellet scatters about the ORIGINAL aim,
		// not about the previous pellet's heading, and the parent's stored
		// yaw is unchanged for the whole burst.
		if sprayAngle != 0 && simRNG != nil && wDef != nil {
			// One draw bounded by the authored spray field, re-centred by
			// half its bound: a = draw + (parentYaw - sprayAngle/2) [06 §4.3].
			draw := simRNG.Uint32n(uint32(sprayAngle))
			a := numeric.Angle(uint16(int32(p.Yaw) + recentred(draw, sprayAngle)))
			// The parent/template velocity X and Z are rebuilt from `a` and
			// the parent's UNCHANGED pitch, magnitude the weapon's authored
			// weaponvelocity: H = cos(pitch, weaponvelocity),
			// velocityX = -sin(a, H), velocityZ = -cos(a, H). Velocity Y is
			// left alone [06 §4.3]. (This codebase's stored yaw is retail's
			// muzzle-to-target atan2 argument order flipped — see
			// VelocityFromAngles — so the retail negation cancels here the
			// same way it does for every other creator's velocity build.)
			h := numeric.MulRound(numeric.Cos(p.Pitch), wDef.WeaponVelocity)
			p.Velocity.X = numeric.Fixed(int64(numeric.MulRound(numeric.Sin(a), h)))
			p.Velocity.Z = numeric.Fixed(int64(numeric.MulRound(numeric.Cos(a), h)))
			// p.Yaw, p.Pitch, and p.Velocity.Y are NOT written: `a` is
			// discarded after building the velocity components [06 §4.3].
		}
		// Burst clones do not rerun Fire or RockUnit [06 §4.1] C2.
		clones++
		if p.BurstRemaining == 0 {
			// Anchor dies silently at instant pellet N launches [06 §4.3] C8: dead flag set directly with no dispatch.
			s.MarkDead(h)
		}
		// At most one emission attempt per phase per projectile [06 §4.3] C8; no catch-up loop.
	}
	return clones
}

// SweepBurstAnchorsForShooter is the unit-death burst-anchor sweep
// [06 §4.3] [06 §5.2]. It runs from the central death handler, last of the
// fixed teardown helpers [06 §12.1].
//
// A forward scan over the ACTIVE POOL. For every record whose remaining burst
// count is nonzero and whose shooter reference is the dying unit, it takes the
// follow-camera snap, sets the dead bit, and runs the compaction pass of
// [06 §5.2] INSIDE the loop — so the pool is compacted once per anchor found
// while the same ascending walk continues over the moved records. A unit that
// dies with two anchors alive therefore compacts twice.
//
// Three details of the match and the walk are contract, not tidiness:
//
//   - the dead bit is NOT consulted by the match test [06 §5.2], so an anchor
//     already retired this tick and not yet compacted away still matches and
//     is re-marked, rather than being skipped;
//   - this is NOT a general removal of the dying unit's projectiles
//     [06 §5.2]. Only anchors match — a clone has had its own remaining count
//     cleared — so pellets already in flight keep flying and still credit
//     their dead shooter;
//   - after a match the scan ADVANCES rather than revisiting the record moved
//     into the vacated slot [06 §5.2]. Two matching anchors that are adjacent
//     therefore leave the second one alive. [06 §5.2] files whether ordinary
//     firing can reach that adjacency as a Supported inference; reproducing
//     the skip costs nothing, and replacing it with a re-visit would not be
//     free.
//
// "Killed" here is the silent retirement of [06 §4.3]: the dead flag set
// directly with NO removal dispatch — no explosion, no sound, no shake, no end
// smoke, no damage — and the anchor's still-pending clones simply never come
// into existence, because the scheduler that would have emitted them is gone
// before the next projectile phase.
//
// The follow-camera snap has no representation in this build: nothing holds a
// followed-projectile pointer, and the projectile phase's own tail compaction
// passes nil for the same reason [06 §5.2]. The compaction call here is
// identical to that one.
//
// Returns the number of anchors killed.
func (s *Service) SweepBurstAnchorsForShooter(shooter pool.Handle) int {
	if s == nil || shooter == 0 {
		return 0
	}
	killed := 0
	// s.Count() is re-read every step on purpose: the compaction below shrinks
	// the active span under the walk, and the walk is defined over the span as
	// it stands, not over a captured entry count [06 §5.2].
	for i := 0; i < s.Count(); i++ {
		p := &s.Records[i]
		if p.BurstRemaining == 0 || p.Shooter != shooter {
			continue
		}
		s.MarkDead(pool.Handle(i + 1))
		s.Compact(nil)
		killed++
		// No `i--`: the record shifted into the vacated slot is skipped.
	}
	return killed
}
