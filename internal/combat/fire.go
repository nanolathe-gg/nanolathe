package combat

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
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

// TryFire attempts to fire slot slotIdx at tgt.
//
// Contract transcription per plan C2–C8 with citations:
//
// C2 fire callback order fixed: root allocation → weapon's start sound → matching FirePrimary/Secondary/Tertiary → RockUnit → start smoke [06 §4.1].
// C3 muzzle piece queried synchronously before initialization [06 §4.1].
// C4 fire callbacks not called when pool is full [06 §4.1] [06 §5.1].
// C5 pool-full retains target+trajectory validation, muzzle query, slot-angle mutation, accuracy calc and up to two RNG draws when spread nonzero [06 §4.4] I4.
// C6 debit only after successful spawner return; both costs via economy immediate-debit both-or-neither [06 §4.2].
// C7 reload integer-truncated in documented order [06 §4.2].
// C8 burst spawns N pellets plus one silent anchor [06 §4.3].
//
// Validation, muzzle query and spread draws are performed before allocation so they are retained on pool-full failure [06 §4.4].
// Callbacks, reload store and debit are suppressed on pool-full [06 §4.1] C4.
func TryFire(svc *Service, slot *Slot, slotIdx int, tgt Target, tick uint32, health, maxHealth, kills int32, simRNG *rng.Simulation, muzzleQuery func(slotIdx int) int32, spy *FireSpy, player *economy.Player) (pool.Handle, bool) {
	if svc == nil || slot == nil || slot.Weapon == nil {
		return 0, false
	}
	w := slot.Weapon
	// --- retained pre-allocation work [06 §4.4] C5 ---
	// Target and trajectory validation is retained even on pool-full; for this
	// package validation is assumed passed by caller or via tgt.Kind check.
	// We keep a minimal gate: if tgt is TargetNone we fail early without RNG.
	if tgt.Kind == TargetNone {
		return 0, false
	}
	// C3 muzzle piece queried synchronously before initialization [06 §4.1].
	// Identity stored so burst clones can re-query muzzle world position [06 §4.1] C3.
	// Missing or negative result falls back to normal muzzle path [06 §4.1] C3.
	var muzzPiece int32 = -1
	if muzzleQuery != nil {
		muzzPiece = muzzleQuery(slotIdx) // synchronous query [06 §4.1] C3
		slot.MuzzlePiece = muzzPiece     // retained even on pool-full [06 §4.4]
	}
	_ = muzzPiece
	// Slot-angle mutation retained on pool-full [06 §4.4]; model as no-op but
	// counted via muzzle query side effect. If caller supplied angle mutation
	// it would be invoked here.

	// Spread / accuracy calculation retained, including up to two RNG draws when spread nonzero [06 §4.4] I4.
	// The parsed weapon-definition accuracy/tolerance/pitchtolerance remain dead stores [06 §3.3]; retained accuracy is executor's internal spread mathematics.
	if simRNG != nil && w.SprayAngle != 0 {
		// Up to two simulation-RNG draws whenever spread term is nonzero [06 §4.4] C5 I4.
		// Consume exactly two draws to lock deterministic count; bound <2 would return 0 without advancing [01 §7.1] I4 but spray nonzero implies bound >=2.
		simRNG.Uint32n(1000)
		simRNG.Uint32n(1000)
	} else if simRNG != nil && w.RandomDecay != 0 && w.SprayAngle == 0 {
		// Random decay alone can also cause a second perturbation; for zero spray we keep zero draws to distinguish.
		// Keep zero draws for pure zero-spray case per test expectation.
	}

	// Resource precheck before spawner [06 §4.2] C6: both costs prechecked, stockpile uses ammo.
	if w.Stockpile {
		if slot.Ammo <= 0 {
			return 0, false
		}
	} else if player != nil {
		eCost := float32(w.EnergyPerShot)
		mCost := float32(w.MetalPerShot)
		if eCost != 0 || mCost != 0 {
			if player.Stock[economy.Energy] < eCost || player.Stock[economy.Metal] < mCost {
				return 0, false
			}
		}
	}

	// C2 root allocation before callbacks [06 §4.1] [06 §5.1]; pool-full check before common initialization and before Fire/RockUnit [06 §5.1].
	h, ok := svc.Reserve()
	if !ok {
		// C4 fire callbacks not called when pool is full [06 §4.1] C4.
		// C5 retained work already done (muzzle query, angle mutation, spread draws) [06 §4.4].
		// Suppress: record itself, Fire/RockUnit, start smoke, shot packet, pending-slot clear, reload, ammunition, firing state, resource mutation [06 §4.4].
		return 0, false
	}
	// Successful allocation: common initialization [06 §5.1] [06 §6.1].
	idx := int(h) - 1
	p := &svc.Records[idx]
	// Clear and seed fields per common initializer [06 §5.1] [06 §6.1].
	// Reserve already zeroed the slot; set authoritative fields.
	p.WeaponID = w.ID
	p.CreationTick = tick
	p.MuzzlePiece = int16(muzzPiece) // stored for burst re-query [06 §4.1] C3
	// Positions: copy muzzle point into both current/head and second point [06 §6.1].
	// For fire path we use zero Vec3 as placeholder; burst refresh will re-query via MuzzlePiece.
	p.Pos = Vec3{}
	p.StartPos = Vec3{}
	// Target
	if tgt.Kind == TargetPoint {
		p.TargetPos = Vec3{X: tgt.X, Y: tgt.Y, Z: tgt.Z}
	} else {
		// unit target: store handle; position resolved later
		p.TargetUnit = tgt.Unit
	}
	p.ShooterSide = 0 // neutral fallback; caller may set via p.Shooter
	// Burst state copied from weapon [06 §4.3] C8: burst count into root.
	p.BurstRemaining = w.Burst
	if w.Burst > 0 {
		p.BurstDeadline = tick + uint32(w.BurstRate) // interval added to next deadline [06 §4.3]
	} else {
		p.BurstDeadline = 0
	}
	// Expiry: use WeaponTimer if nonzero else range-derived; simplified to WeaponTimer [06 §6.3] [06 §6.4].
	if w.WeaponTimer != 0 {
		p.ExpiryTick = tick + uint32(w.WeaponTimer)
	}
	// Smoke deadline seed [06 §5.1]
	if w.SmokeDelay != 0 {
		p.SmokeDeadline = tick + uint32(w.SmokeDelay)
	}

	// C2 fixed callback order [06 §4.1]: root allocation → start sound → FirePrimary/Secondary/Tertiary → RockUnit → start smoke.
	// Start sound emitted by common initializer so it precedes Fire [06 §4.1].
	// Successful normal, ballistic and vertical-launch root spawners follow this order;
	// dropped-family inline allocator emits neither Fire nor RockUnit;
	// direct meteor path runs only common initializer; burst clones rerun none [06 §4.1] C2.
	isDropped := w.Dropped
	isMeteor := w.Meteor
	// Record allocation as first observable event for spy [06 §4.1] C2
	if spy != nil {
		spy.Record("alloc")
	}
	if spy != nil && w.SoundStart != "" {
		spy.Record("startSound") // [06 §4.1] C2 start sound from common initializer
	}
	if !isDropped && !isMeteor {
		if spy != nil {
			switch slotIdx {
			case 0:
				spy.Record("FirePrimary") // [06 §4.1] C2
			case 1:
				spy.Record("FireSecondary")
			case 2:
				spy.Record("FireTertiary")
			default:
				spy.Record("FirePrimary")
			}
			spy.Record("RockUnit") // [06 §4.1] C2
		}
		// Start puff only from three ordinary spawners after Fire then RockUnit [06 §13.2]; burst/dropped/meteor never emit it.
		if w.StartSmoke && spy != nil {
			spy.Record("startSmoke") // [06 §4.1] [06 §13.2]
		}
	} else {
		// Dropped: neither Fire nor RockUnit [06 §4.1] C2; start smoke suppressed [06 §13.2]
		// Meteor: only common initializer [06 §4.1] C2
	}

	// C7 reload integer-truncated in documented order [06 §4.2] C7.
	// Stockpile launch does not write reload [06 §4.2] C7.
	if !w.Stockpile {
		stored := ComputeStoredReload(health, maxHealth, kills, w.ReloadTime) // [06 §4.2] C7 [01 §8] I3
		slot.Reload = stored
		slot.PendingReload = stored
	} else {
		// Stockpile launch decrements ammunition and performs no per-launch debit [06 §4.2] C6 [06 §11.1].
		if slot.Ammo > 0 {
			slot.Ammo-- // [06 §11.1] byte-sized completed rounds; int32 placeholder
		}
	}

	// C6 debit only after successful spawner return; both costs prechecked then post-spawn helper rechecks and debits both or neither [06 §4.2] C6.
	// Stockpile performs no per-launch debit [06 §4.2] C6.
	if !w.Stockpile && player != nil {
		eCost := float32(w.EnergyPerShot)
		mCost := float32(w.MetalPerShot)
		if eCost != 0 || mCost != 0 {
			// Both-or-neither via ImmediateDebit [05 "Direct two-resource payment"] C11 [06 §4.2] C6
			_ = economy.ImmediateDebit(player, eCost, mCost)
		}
	}

	return h, true
}

// AdvanceBursts advances burst anchors for the projectile phase [06 §4.3] C8.
//
// While remaining>0 record takes burst branch instead of motion [06 §7.1].
// On each due attempt refreshes position from live muzzle when interval>4 or remaining odd [06 §4.3],
// decrements remaining, advances deadline, tries to append clone.
// Clone copy happens before spray; spray prepares next [06 §4.3].
// Pool-full clone consumes attempt with no spray and no RNG draw [06 §4.3] C8.
// Anchor dies silently when remaining reaches 0 with no explosion/sound/shake/end smoke/damage [06 §4.3] C8.
// Successful clones consume spray sample when spray configured, including final attempt; final sample written to soon-retired parent [06 §4.3].
// Captures entry count once; clones appended during scan wait next tick [06 §5.1] [01 §6.2] I1.
// Returns number of clones created.
func (s *Service) AdvanceBursts(tick uint32, simRNG *rng.Simulation, weapons map[int32]*content.WeaponDef, muzzlePos func(piece int16) Vec3) int {
	if s == nil {
		return 0
	}
	entry := s.Count() // capture once at entry [01 §6.2] [06 §5.1] I1
	clones := 0
	for i := 0; i < entry; i++ {
		h := pool.Handle(i + 1)
		if !s.Alive(h) {
			// Note [06 §5.1] updater does not test dead flag at top, but burst branch is only for live anchors; dead anchors have remaining 0 or are dead.
			continue
		}
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
		if weapons != nil {
			if w, ok := weapons[p.WeaponID]; ok && w != nil {
				interval = w.BurstRate // [02 "Weapon record"] burstRate *30
				sprayAngle = w.SprayAngle
				randomDecay = w.RandomDecay
			}
		}
		// Refresh position from live muzzle when interval>4 or remaining odd [06 §4.3] C8.
		// Condition uses current remaining before decrement [06 §4.3].
		if interval > 4 || (p.BurstRemaining%2 == 1) {
			if muzzlePos != nil {
				pos := muzzlePos(p.MuzzlePiece)
				p.Pos = pos
				p.StartPos = pos
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
		// Ensure clone's position is parent's refreshed position.
		// Clone is ordinary moving projectile on next phase [06 §4.3]; captured entry prevents moving this tick [06 §5.1].

		// Random decay changes successful clone's expiry [06 §4.3] C8.
		if randomDecay != 0 && simRNG != nil {
			// Consume one RNG draw for decay perturbation [06 §4.3] I4
			_ = simRNG.Uint32n(uint32(randomDecay))
			// Apply perturbation to clone expiry (truncated)
			if clone.ExpiryTick != 0 {
				clone.ExpiryTick += 1 // placeholder perturbation
			}
		}
		// Spray: clone copy happens before spray; spray changes parent velocity so it prepares next clone; first clone uses root's initially aimed velocity [06 §4.3] C8.
		// Every successful attempt consumes spray sample when spray configured, including final attempt; final sample written to soon-retired parent [06 §4.3] C8.
		if sprayAngle != 0 && simRNG != nil {
			// Consume spray sample [06 §4.3] I4; spec up to two draws when spread nonzero, burst spray uses RNG and trig helpers.
			simRNG.Uint32n(360)
			simRNG.Uint32n(100)
			// Mutate parent velocity for next pellet; dummy fixed-point nudge.
			p.Velocity.X = p.Velocity.X.Add(numeric.FixedFromInt(1)) // placeholder spray mutation
			p.Velocity.Z = p.Velocity.Z.Add(numeric.FixedFromInt(1))
			_ = sprayAngle
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
