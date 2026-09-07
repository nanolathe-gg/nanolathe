package features

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// The active list of [05 R-FEAT-01 §10] pass 3.
//
// Retail's live-instance arena keeps three doubly-linked lists — active,
// dormant and free [05 R-FEAT-01 §2]. The feature phase walks the ACTIVE list
// from its head, reading each slot's next link before the slot is processed,
// so a slot that moves lists mid-visit does not break the walk; the list is
// LIFO, the most recently stamped or ignited instance first. Its members are
// exactly the records the walk has work for: 3D instances that are still
// moving (a 3D instance whose velocity is all zero is moved to the dormant
// list on its visit and never visited again until re-stamped) and sprite
// event records — burning, dying or reclaiming.
//
// Nanolathe keeps an Instance for every stamped anchor, resting sprites
// included, as the presentation and lookup record; the cell-index map is that
// lookup and nothing else. Simulation ORDER comes from this list alone: a
// resting sprite is never on it, so a map's forest costs the walk nothing, and
// the order in which fires spend the simulation stream against their
// candidates is retail's, not the cell order.

// linkActive inserts an instance at the head of the active list — the stamp's
// step 4 for a 3D definition [05 R-FEAT-01 §3], ignition's step 2
// [05 R-FEAT-01 §9] and the transition's step 5 [05 R-FEAT-01 §5]. A record
// already on the list stays where it is.
func (s *Service) linkActive(inst *Instance) {
	if inst == nil || inst.onActive {
		return
	}
	inst.onActive = true
	inst.prevActive = nil
	inst.nextActive = s.activeHead
	if s.activeHead != nil {
		s.activeHead.prevActive = inst
	}
	s.activeHead = inst
}

// unlinkActive removes an instance from the active list: the dormant move of
// pass 3's 3D branch, or the teardown that frees a slot [05 R-FEAT-01 §4].
// The record's own next link is deliberately left in place. The walk reads
// each record's next link before visiting it, and a visit can remove that
// very record (a burn weapon killing the sinking wreck that happens to be
// next); keeping the stale link lets the walk continue past it to the record
// that followed, which is the walk retail's captured pointer performs when
// the freed slot is not re-popped in the same visit. The re-pop case — the
// freed slot immediately re-stamped as a successor at the head, so that the
// captured pointer now names the head and the walk repeats — needs the slot
// arena itself and is not reproduced.
func (s *Service) unlinkActive(inst *Instance) {
	if inst == nil || !inst.onActive {
		return
	}
	inst.onActive = false
	if inst.prevActive != nil {
		inst.prevActive.nextActive = inst.nextActive
	} else if s.activeHead == inst {
		s.activeHead = inst.nextActive
	}
	if inst.nextActive != nil {
		inst.nextActive.prevActive = inst.prevActive
	}
	inst.prevActive = nil
}

// attachEventRecord is the arena admission common to every slot-taking record
// [05 R-FEAT-01 §3 step 4, §5 step 5, §9 step 2]: a 3D instance at its stamp
// (via setInstance) and a resting sprite's Instance becoming an event record
// at ignition or the die/reclaim transition. It takes an arena slot and joins
// the active list at the head.
func (s *Service) attachEventRecord(inst *Instance) {
	s.billArena(inst)
	s.linkActive(inst)
}

// billArena charges one live-arena slot to an instance, once. The arena is
// spent only on 3D instances and sprite event records [05 R-FEAT-01 §3 steps
// 4-5]; see arenaOccupies.
func (s *Service) billArena(inst *Instance) {
	if inst == nil || inst.arenaBilled {
		return
	}
	inst.arenaBilled = true
	s.arenaHeld++
}

// releaseArena returns an instance's slot to the free list, once.
func (s *Service) releaseArena(inst *Instance) {
	if inst == nil || !inst.arenaBilled {
		return
	}
	inst.arenaBilled = false
	s.arenaHeld--
}

// activeWalk is pass 3 of the feature phase [05 R-FEAT-01 §10], the whole of
// TickLifecycle: from the head, following each record's next link captured
// before the record is visited.
func (s *Service) activeWalk(tick uint32) {
	if s == nil || s.Terrain == nil {
		return
	}
	// One smoke flag per call, true when the global tick is a multiple of
	// three, shared across every burning instance in the pass
	// [05 "Feature burning"].
	smoke := tick%3 == 0
	for inst := s.activeHead; inst != nil; {
		next := inst.nextActive
		s.visitActive(inst, tick, smoke)
		// A record removed by the visit keeps its next link (unlinkActive), so
		// the walk carries on to whatever followed it at that moment. A record
		// inserted by the visit went to the HEAD — behind the walk — and is
		// first visited next tick, as retail's captured pointer guarantees.
		for next != nil && !next.onActive {
			next = next.nextActive
		}
		inst = next
	}
}

// visitActive is one record's visit, by definition class and mode bits
// [05 R-FEAT-01 §10] pass 3.
func (s *Service) visitActive(inst *Instance, tick uint32, smoke bool) {
	if inst == nil || inst.Def == nil {
		s.unlinkActive(inst)
		return
	}
	if !isSpriteDef(inst.Def) {
		s.visit3D(inst)
		return
	}
	if !inst.IsBurning {
		s.visitDieOrReclaim(inst)
		return
	}
	s.visitBurning(inst, tick, smoke)
}

// visit3D is the 3D branch: the dormant move is tested BEFORE the integration
// on each visit, so a snapped instance is retired on the visit after it lands
// [05 R-FEAT-01 §13]. Nothing writes a horizontal velocity in the corpse path
// or the integration, and the stamp zeroes the triple rather than inheriting
// a predecessor's [05 R-FEAT-01 §14], so the vertical word is the whole test.
func (s *Service) visit3D(inst *Instance) {
	if inst.Vy == 0 {
		inst.IsSinking = false
		inst.Settled = true // dormant: never visited again until re-stamped
		s.unlinkActive(inst)
		return
	}
	s.integrateSink(inst)
}

// visitDieOrReclaim is the "sprite instance, burning bit clear" branch: one
// visit advances the main cursor (then the shadow cursor, which has no
// simulation effect and is presentation's to draw), and the visit whose
// advance clears the sequence pointer runs §5 step 6's replacement at the
// anchor — HERE, at the visit, so a later record in the same walk observes the
// successor. No sprite animation consumes a draw on any stream [01 §7.5].
func (s *Service) visitDieOrReclaim(inst *Instance) {
	if !inst.IsAnimating {
		// Not an event record: a resting sprite has no business on the list.
		s.unlinkActive(inst)
		return
	}
	if !inst.cursor.advance() {
		return
	}
	// [05 R-FEAT-01 §10] pass 3's die/reclaim completion runs §5 step 6's
	// replacement at the anchor with argument 0. Step 6 takes `featuredead`
	// for that argument, EXCEPT that an instance whose reclaim-animation bit
	// is set promotes the successor to `featurereclamate` regardless — which
	// is the whole point of carrying the bit. Selector 2 is that record. A
	// successor word of 0xFFFF makes the stamp a no-op, i.e. final removal,
	// which the replacement path models as a nil successor definition.
	//
	// This is the REPLACEMENT, not the transition: going back through the
	// transition entry would meet its own record at step 4 and drop the
	// removal, leaving the finished animation on the cell forever.
	var succ *content.FeatureDef
	if inst.AnimationSelector == featureAnimSelectorReclaim {
		succ = inst.Def.FeatureReclamateDef
	} else {
		succ = inst.Def.FeatureDeadDef
	}
	s.replaceFeatureAt(inst.CX, inst.CZ, succ)
}

// visitBurning is the "sprite instance, burning" branch, sub-steps a-d in
// order [05 R-FEAT-01 §10] pass 3.
func (s *Service) visitBurning(inst *Instance, tick uint32, smoke bool) {
	if smoke {
		s.emitBurnSmoke(inst)
	}
	// b. advance the burn cursor (the shadow cursor is presentation's).
	if inst.cursor.advance() {
		// c. the sequence pointer is null: look up the anchor cell, tear it
		// down, and stamp `featureburnt` when linked. This is NOT the
		// replacement routine: no position or orientation is carried, and
		// the successor is placed at the snapped footprint centre.
		def := inst.Def
		cx, cz := inst.CX, inst.CZ
		s.clearFootprint(cx, cz, def)
		if def.FeatureBurntDef != nil {
			s.spawnFeatureAt(cx, cz, def.FeatureBurntDef)
		}
		return
	}
	// d. else if the countdown is nonzero and the remote bit is clear,
	// decrement it and run the burn event once when it reaches zero.
	if inst.BurnCountdown != 0 && !inst.RemoteSuppressed {
		inst.BurnCountdown--
		if inst.BurnCountdown == 0 {
			s.fireBurnEvent(inst)
		}
	}
}

// emitBurnSmoke is sub-step a: one smoke particle at the footprint centre,
// terrain height, jittered by two CRT-stream draws scaled by the current burn
// frame's width and height [05 R-FEAT-01 §10] pass 3a.
//
// This is the burning-feature strip-5 smoke producer [R-STRIP-01 §1 strip 5]:
// one wind-drifted smoke puff every third tick, reached through the BurnSmoke
// seam the session binds to its strip table. The two draws — first the
// horizontal one, then the vertical one, in that order — are each scaled by
// the CURRENT BURN FRAME's width and height, with the frame's own offsets
// recentring the result:
//
//	x += (draw·(w/2))/32768 - frame.xoff + w/4
//	y += 2·(frame.yoff - (draw·(h/2))/32768) - 2·(h/4)
//
// taking integer parts with 16-bit truncation; burnSmokeJitter is exactly
// those two addends. WHICH AXES they move: world X and world HEIGHT, with Z
// passed through at the footprint centre [05 R-FEAT-01 §16], the factor of
// two on the y term being the half-row projection shear [03 §2.5].
//
// THE THIRD DRAW. An emission costs THREE CRT draws, all in this phase: the
// horizontal jitter, the vertical jitter, then the puff's last-frame draw
// inside the producer, because the smoke family's init calls its spawn
// virtual [05 R-FEAT-01 §16][03 R-STRIP-01 §2]. The two jitter draws are
// taken here unconditionally, before the seam is consulted, in the order the
// site takes them. The third is the producer's own and is taken only when a
// producer is bound; the session always binds one, and a service with no
// producer is a fixture, not a battle.
func (s *Service) emitBurnSmoke(inst *Instance) {
	var drawX, drawY int32
	if crt := s.crt(); crt != nil {
		drawX = crt.Rand()
		drawY = crt.Rand()
	}
	if s.BurnSmoke == nil {
		return
	}
	// The base is the footprint centre at the sampled terrain height — the
	// same centre the burn weapon fires at, `((footprintx + 2x)·8,
	// (footprintz + 2z)·8)` [05 R-FEAT-01 §11 step 3].
	px := footprintCentreWorld(inst.CX, inst.FootprintX)
	pz := footprintCentreWorld(inst.CZ, inst.FootprintZ)
	py := s.Terrain.CoarseHeightAt(int32(inst.CX), int32(inst.CZ))
	if s.BurnFrameGeometry != nil {
		// The frame the cursor is on, asked for by the first visit of that
		// frame so the resolver's cadence walk lands on it exactly.
		w, h, xoff, yoff := s.BurnFrameGeometry(inst.Def, inst.cursor.visitIndex())
		dx, dy := burnSmokeJitter(burnFrameGeometry{W: w, H: h, XOff: xoff, YOff: yoff}, drawX, drawY)
		px = px.Add(numeric.FixedFromInt(int64(dx)))
		py = py.Add(numeric.FixedFromInt(int64(dy)))
	}
	s.BurnSmoke([3]numeric.Fixed{px, py, pz})
}
