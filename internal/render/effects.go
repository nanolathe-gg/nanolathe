package render

// The fixed effect pool [03 §1] C5.

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// NanolatheColor is the fixed segment color for build/reclaim beams [03 §5.5].
// Every retail emitter passes the same constant; variation is footprint jitter,
// not color.
//
// It is a GUI semantic index, resolved through the GUIPAL-to-display map at
// draw time [03 §4.3] — the same map the build ghost and the queued build-site
// markers index [07 §9]. GUIPAL entry 6 is the sixteen-color set's brown,
// (170, 85, 0), which lands on the display palette's nearest orange. Passing 6
// straight to an indexed primitive would paint some unrelated palette entry.
const NanolatheColor = 6 // [03 §5.5] GUI index, resolve via Client.GUIColor

// EffectAnimPlayer is one embedded animation player in a fixed effect record [03 §1] C5.
// Its tick integrator single-steps both players and clears non-looping sequences'
// pointers at termination [03 §1]. Go uses named fields per I13; retail 12-byte
// cursor identity (current index, countdown, loop flag) maps to these fields [03 §4.4].
type EffectAnimPlayer struct {
	Idx       int32   // current frame index
	Countdown int32   // ticks remaining for current frame; countdown <2 advances [03 §4.4]
	Loop      bool    // loop-or-hold flag [03 §4.4]
	Active    bool    // sequence pointer present; false == cleared/terminated [03 §1]
	Frames    int     // frame count for this sequence; 0 means no sequence
	Durations []int32 // authored whole-tick durations; nil means unresolved
}

// Step single-steps the player by one simulation tick [03 §1] C5.
// Countdown <2 advances to the next frame, wraps to 0 for looping sequences
// or clears the entry pointer for non-looping sequences and loads the new
// frame's duration [03 §4.4]. The authored duration is established: the GAF
// frame-reference word is "the per-frame display duration in whole simulation
// ticks" — the playback cursor loads it as the countdown and steps in whole
// ticks, and the loader leaves it untouched [fmt gaf "Frame entry"]. Durations
// carries those values; the one-tick answer below is only the fixture path for
// a record whose timing has not been published.
func (a *EffectAnimPlayer) Step() {
	if a == nil || !a.Active {
		return
	}
	if a.Frames <= 0 {
		a.Active = false // no sequence [03 §1]
		return
	}
	if a.Countdown < 2 { // countdown <2 advances [03 §4.4]
		a.Idx++
		if int(a.Idx) >= a.Frames {
			if a.Loop {
				a.Idx = 0
				a.Countdown = a.frameDuration(a.Idx)
			} else {
				// clears non-looping sequences' pointers at termination [03 §1] C5
				a.Active = false
				a.Idx = 0
				a.Countdown = 0
				return
			}
		} else {
			a.Countdown = a.frameDuration(a.Idx) // load authored duration [03 §4.4]
		}
	} else {
		a.Countdown--
	}
}

// live reports whether this player's layer is still to be drawn — the fact
// SnapshotViewsInto publishes as EffectView.ActiveA/ActiveB [03 §1].
//
// Rendering walks the pool once per animation category, so a category draws
// exactly while its sequence pointer is intact: Step clears the pointer at
// termination and that category then draws nothing for this record [03 §1].
// The record itself survives until BOTH players are inactive, which is why the
// terminated one must be reported dead rather than left to repaint the
// index-zero residue its cursor was reset to [03 §4.4].
//
// That is the whole rule — a layer with no player holds no sequence pointer and
// draws nothing, exactly as retail's category walk would. Admission activates a
// player only when authored timing resolved, so unresolved or malformed timing
// stays unresolved instead of becoming a static frame 0 [I9]; every named-art
// layer a producer publishes owns its own player, because the timing lookup is
// per player [06 R-WFX-01 §2].
func (a *EffectAnimPlayer) live() bool {
	if a == nil {
		return false
	}
	return a.Active
}

func (a *EffectAnimPlayer) frameDuration(idx int32) int32 {
	if a != nil && idx >= 0 && int(idx) < len(a.Durations) {
		if a.Durations[idx] > 0 {
			return a.Durations[idx]
		}
		// A malformed authored duration is absent, not a reason to invent
		// timing. Keep the established cursor safety rule for this record.
		return 1
	}
	// Legacy callers that do not yet publish timing retain the cursor's
	// one-tick fixture behavior; production admission leaves no frame player
	// active until authored timing is present [03 §4.4][I9].
	return 1
}

// EffectRecord is one fixed-size effect record in the 300-entry pool [03 §1] C5.
// Retail size is 0x54 bytes per I5; Go uses named fields per I13.
type EffectRecord struct {
	// Presentation identity is metadata copied from an admitted event. It is
	// not an authoritative pool handle and survives stable compaction [03 §1].
	PresentationID uint64
	ID             uint32
	EventSeq       uint64
	Source         pool.Handle
	Target         pool.Handle
	EffectID       uint32
	Piece          int32
	SFXType        int32
	SFXClass       frame.SFXClass
	Mode           uint8
	StartTick      uint32
	Kind           string
	Graphic        string
	AssetID        string
	SequenceID     string
	// Strip is the effect-strip destination (beam/muzzle/nanolathe = 6 etc.)
	// routed at admission [03 §5.5][R-P0-06 §5]. It is preserved through the
	// pool so the client's strip-6 nanolathe draw gate can fire.
	Strip int8
	// Position and velocity in 16.16 world units [I2][03 §2.1].
	X, Y, Z                   numeric.Fixed // current position [03 §1]
	TargetX, TargetY, TargetZ numeric.Fixed // producer endpoint metadata [03 §5.5]
	VX, VY, VZ                numeric.Fixed // velocity; advanced against gravity each tick [03 §1]
	Gravity                   numeric.Fixed // per-record gravity; zero means use pool default [03 §2.2]
	ExpiryTick                uint32        // optional authored deadline; zero means unresolved [03 §5.5]

	// Two embedded animation players [03 §1] C5.
	AnimA EffectAnimPlayer
	AnimB EffectAnimPlayer

	// Blast profile copies the impact weapon's compiled values for the modern
	// presentation prototype (DESIGN_GPU_RENDERER §25). Presence is separate
	// from zero damage; these values never feed authoritative damage.
	HasBlastProfile   bool
	BlastAreaOfEffect int32
	BlastDamage       int32

	// HasCalculatedFlash and CalculatedTable select the procedurally generated
	// table the secondary cursor indexes [06 R-WFX-01 §2].
	HasCalculatedFlash bool
	CalculatedTable    uint8

	// HasModel indicates a model-bearing record; the integrator can clear the
	// model pointer on terrain/water contact as the alternative to bouncing [03 §1] C5.
	HasModel bool
	// FragmentSlot is one-based into FixedEffectPool.fragments. It moves with
	// stable record compaction, while the geometry remains slot-owned [04 R-COB-04 §3].
	FragmentSlot            uint16
	FragmentExplodeOnHit    bool
	NanolatheGeometryKnown  bool
	NanolatheTargetBoxKnown bool
	NanolatheTargetMin      [3]numeric.Fixed
	NanolatheTargetMax      [3]numeric.Fixed
	// NanolatheBoxAtSource: the box above is the SOURCE end of the segment and
	// the target point is the destination [05 R-WORK-01 §8].
	NanolatheBoxAtSource bool
}

// AppendView admits one immutable event view into the canonical fixed pool.
// Missing authored timing remains missing; no synthetic frame or lifetime is
// manufactured here [03 §4.4][03 §5.5][I9].
func (p *FixedEffectPool) AppendView(v frame.EffectView) bool {
	if p == nil {
		return false
	}
	r := EffectRecord{
		PresentationID: v.PresentationID,
		ID:             v.ID,
		EventSeq:       v.EventSeq,
		Source:         v.Source, Target: v.Target, EffectID: v.EffectID,
		Piece: v.Piece, SFXType: v.SFXType, SFXClass: v.SFXClass,
		Mode: v.Mode, StartTick: v.StartTick,
		Kind:       v.Kind,
		Graphic:    v.Graphic,
		AssetID:    v.AssetID,
		SequenceID: v.SequenceID,
		Strip:      v.Strip,
		X:          v.X, Y: v.Y, Z: v.Z,
		TargetX: v.TargetX, TargetY: v.TargetY, TargetZ: v.TargetZ,
		VX: v.VX, VY: v.VY, VZ: v.VZ,
		Gravity: v.Gravity, ExpiryTick: v.ExpiryTick,
		HasCalculatedFlash: v.HasCalculatedFlash, CalculatedTable: v.CalculatedTable,
		HasBlastProfile: v.HasBlastProfile, BlastAreaOfEffect: v.BlastAreaOfEffect, BlastDamage: v.BlastDamage,
		AnimA:    EffectAnimPlayer{Idx: v.SeqA, Active: validDurations(v.DurationsA), Frames: len(v.DurationsA), Durations: append([]int32(nil), v.DurationsA...), Loop: v.LoopA},
		AnimB:    EffectAnimPlayer{Idx: v.SeqB, Active: validDurations(v.DurationsB), Frames: len(v.DurationsB), Durations: append([]int32(nil), v.DurationsB...), Loop: v.LoopB},
		HasModel: v.HasModel, NanolatheGeometryKnown: v.NanolatheGeometryKnown,
		NanolatheTargetBoxKnown: v.NanolatheTargetBoxKnown,
		NanolatheTargetMin:      v.NanolatheTargetMin, NanolatheTargetMax: v.NanolatheTargetMax,
		NanolatheBoxAtSource: v.NanolatheBoxAtSource,
	}
	if r.AnimA.Active {
		r.AnimA.Countdown = r.AnimA.frameDuration(r.AnimA.Idx)
	}
	if r.AnimB.Active {
		r.AnimB.Countdown = r.AnimB.frameDuration(r.AnimB.Idx)
	}
	return p.Append(r)
}

// resolveUnresolvedPrimary supplies authored timing to named primary players
// admitted before composition installed its resolver. A player with valid
// durations has already resolved (or completed), so binding never restarts it; the
// secondary player and stable record order are untouched [03 §1][I6].
func (p *FixedEffectPool) resolveUnresolvedPrimary(resolver TimingResolver) {
	if p == nil || resolver == nil {
		return
	}
	for i := range p.records {
		record := &p.records[i]
		if record.Graphic == "" || record.AnimA.Active || validDurations(record.AnimA.Durations) {
			continue
		}
		timing, ok := resolver(Event{Graphic: record.Graphic, AssetID: record.AssetID})
		if !ok || !validDurations(timing.Durations) {
			continue
		}
		primary := EffectAnimPlayer{
			Idx:       record.AnimA.Idx,
			Loop:      timing.Loop,
			Active:    true,
			Frames:    len(timing.Durations),
			Durations: append([]int32(nil), timing.Durations...),
		}
		primary.Countdown = primary.frameDuration(primary.Idx)
		record.AnimA = primary
	}
}

// SnapshotViews returns a detached, stable read-only view of the active pool.
// It is the only data presentation consumers need from the mutable pool [I6].
func (p *FixedEffectPool) SnapshotViews() []frame.EffectView {
	if p == nil || len(p.records) == 0 {
		return nil
	}
	return p.SnapshotViewsInto(nil)
}

// SnapshotViewsInto copies the active pool in stable order while reusing the
// caller's top-level and nested timing storage.
func (p *FixedEffectPool) SnapshotViewsInto(out []frame.EffectView) []frame.EffectView {
	if p == nil || len(p.records) == 0 {
		return out[:0]
	}
	if cap(out) < len(p.records) {
		out = make([]frame.EffectView, 0, len(p.records))
	}
	out = out[:0]
	for _, r := range p.records {
		i := len(out)
		out = out[:i+1]
		a, b := out[i].DurationsA, out[i].DurationsB
		out[i] = frame.EffectView{
			PresentationID: r.PresentationID,
			ID:             r.ID, EventSeq: r.EventSeq,
			Source: r.Source, Target: r.Target, EffectID: r.EffectID,
			Piece: r.Piece, SFXType: r.SFXType, SFXClass: r.SFXClass,
			Mode: r.Mode, StartTick: r.StartTick,
			Kind: r.Kind, Graphic: r.Graphic, AssetID: r.AssetID, SequenceID: r.SequenceID,
			SeqA: r.AnimA.Idx, SeqB: r.AnimB.Idx,
			// Liveness is published, never inferred downstream: a terminated
			// player leaves index 0 behind, which is indistinguishable from a
			// live first frame [03 §1][03 §4.4].
			ActiveA: r.AnimA.live(), ActiveB: r.AnimB.live(),
			HasCalculatedFlash: r.HasCalculatedFlash, CalculatedTable: r.CalculatedTable,
			HasBlastProfile: r.HasBlastProfile, BlastAreaOfEffect: r.BlastAreaOfEffect, BlastDamage: r.BlastDamage,
			DurationsA: append(a[:0], r.AnimA.Durations...), DurationsB: append(b[:0], r.AnimB.Durations...),
			LoopA: r.AnimA.Loop, LoopB: r.AnimB.Loop,
			Strip: r.Strip,
			X:     r.X, Y: r.Y, Z: r.Z, VX: r.VX, VY: r.VY, VZ: r.VZ,
			TargetX: r.TargetX, TargetY: r.TargetY, TargetZ: r.TargetZ,
			HasModel:     r.HasModel,
			FragmentSlot: r.FragmentSlot,
			Gravity:      r.Gravity, ExpiryTick: r.ExpiryTick,
			NanolatheGeometryKnown:  r.NanolatheGeometryKnown,
			NanolatheTargetBoxKnown: r.NanolatheTargetBoxKnown,
			NanolatheTargetMin:      r.NanolatheTargetMin, NanolatheTargetMax: r.NanolatheTargetMax,
			NanolatheBoxAtSource: r.NanolatheBoxAtSource,
		}
	}
	return out
}

// isEmpty reports whether the record is emptied and should be removed.
// A record is emptied when both embedded animation players are inactive
// (cleared/terminated) and it has no model pointer [03 §1] C5.
func (r *EffectRecord) isEmpty(tick uint32) bool {
	if r == nil {
		return true
	}
	if r.ExpiryTick != 0 && tick < r.ExpiryTick {
		return false
	}
	return !r.AnimA.Active && !r.AnimB.Active && !r.HasModel
}

// RemoveMatching is the canonical end-event removal hook for effects whose
// producer supplies source/target identity. It stably removes only the first
// matching record [03 §5.5].
func (p *FixedEffectPool) RemoveMatching(source, target pool.Handle, kind string) {
	if p == nil {
		return
	}
	write := 0
	removed := false
	for _, record := range p.records {
		if !removed && record.Kind == kind && sameEndpoint(record.Source, source, record.Target, target) {
			p.releaseFragment(record.FragmentSlot)
			removed = true
			continue
		}
		p.records[write] = record
		write++
	}
	for i := write; i < len(p.records); i++ {
		p.records[i] = EffectRecord{}
	}
	p.records = p.records[:write]
}

// Len returns the number of live records [03 §1] C5.
func (p *FixedEffectPool) Len() int {
	if p == nil {
		return 0
	}
	return len(p.records)
}

// Cap returns the battle-entry capacity. A zero-value pool reports retail's
// 300-record limit [03 §1][CP-LIM-1].
func (p *FixedEffectPool) Cap() int {
	if p == nil {
		return 0
	}
	if p.capacity <= 0 {
		return FixedEffectCap
	}
	return p.capacity
}

// Records returns a snapshot slice of live records in stable insertion order [03 §1].
// The returned slice aliases internal storage for tests; callers must not retain it
// across mutations.
func (p *FixedEffectPool) Records() []EffectRecord {
	if p == nil {
		return nil
	}
	return p.records
}

// Clear empties the pool.
func (p *FixedEffectPool) Clear() {
	if p == nil {
		return
	}
	for i := range p.records {
		p.releaseFragment(p.records[i].FragmentSlot)
		p.records[i] = EffectRecord{}
	}
	for i := range p.fragments {
		p.fragments[i] = fragmentGeometry{}
	}
	p.records = p.records[:0]
	p.fragmentCursor = 0
}

// SetGravity sets the pool default per-tick gravity [03 §2.2].
// Per-record Gravity overrides this when nonzero.
func (p *FixedEffectPool) SetGravity(g numeric.Fixed) {
	if p == nil {
		return
	}
	p.gravity = g
}

// Gravity returns the pool default gravity.
func (p *FixedEffectPool) Gravity() numeric.Fixed {
	if p == nil {
		return 0
	}
	return p.gravity
}

// SetHeightFunc installs the terrain height query used for the bounce check [03 §1] C5.
// When nil, terrain contact is skipped and only water (seaLevel) is tested.
func (p *FixedEffectPool) SetHeightFunc(fn func(x, z numeric.Fixed) numeric.Fixed) {
	if p == nil {
		return
	}
	p.heightAt = fn
}

// SetSeaLevel sets sea level in world units (byte*65536) [03 §2.2].
func (p *FixedEffectPool) SetSeaLevel(s numeric.Fixed) {
	if p == nil {
		return
	}
	p.seaLevel = s
}

// Append appends rec at the end; when the count is at or above 300 it
// allocates nothing and returns false [03 §1] C5. Stable insertion order is
// preserved [03 §1][I1].
func (p *FixedEffectPool) Append(rec EffectRecord) bool {
	if p == nil {
		return false
	}
	p.ensureStorage()
	if len(p.records) >= p.capacity { // at or above the battle-entry cap allocates nothing [03 §1][CP-LIM-1]
		return false
	}
	p.records = append(p.records, rec)
	return true
}

// Update ticks the pool by one simulation tick [03 §1] C5.
// For each record it advances velocity against gravity, integrates position,
// handles terrain/water contact (restore prior position and invert/halve
// vertical velocity, or clear model pointer), single-steps both embedded
// animation players (clearing non-looping sequences at termination), and
// removes emptied records by stable left compaction within the same updater
// call — unlike generic strip objects whose terminal condition is noticed only
// on the next invocation [03 §1] C5.
func (p *FixedEffectPool) Update(tick uint32) {
	if p == nil {
		return
	}
	_ = tick // tick kept for Strip-like signature parity [03 §1] C4/C5; ordering is stable [I1]
	write := 0
	for read := 0; read < len(p.records); read++ {
		rec := &p.records[read]
		if rec.ExpiryTick != 0 && tick >= rec.ExpiryTick {
			p.releaseFragment(rec.FragmentSlot)
			continue
		}
		if rec.FragmentSlot != 0 {
			p.stepFragment(read)
			rec = &p.records[read]
		} else {

			// Save prior position for possible restore on terrain/water contact [03 §1].
			prevX, prevY, prevZ := rec.X, rec.Y, rec.Z

			// Advance velocity against gravity [03 §1] C5.
			g := rec.Gravity
			if g == 0 {
				g = p.gravity
			}
			if g != 0 {
				rec.VY -= g // velocity against gravity [03 §1]; Fixed is integer [I2]
			}
			// Integrate position.
			rec.X += rec.VX
			rec.Y += rec.VY
			rec.Z += rec.VZ

			// Terrain or water contact check [03 §1] C5.
			contacted := false
			if p.heightAt != nil {
				h := p.heightAt(rec.X, rec.Z)
				if rec.Y < h {
					contacted = true
				}
			}
			if !contacted && p.seaLevel != 0 && rec.Y < p.seaLevel {
				contacted = true
			} else if !contacted && p.heightAt == nil && p.seaLevel == 0 {
				// no terrain query and sea level zero: no contact
			}
			if contacted {
				if rec.HasModel {
					// clear record's model pointer alternative branch [03 §1] C5
					rec.HasModel = false
				} else {
					// restore prior position and invert/halve vertical velocity [03 §1] C5
					rec.X = prevX
					rec.Y = prevY
					rec.Z = prevZ
					// invert/halve with trunc toward zero [01 §8][I3]
					rec.VY = numeric.Fixed(-int64(rec.VY) / 2)
				}
			}
		}

		// Single-step both embedded animation players [03 §1] C5.
		rec.AnimA.Step()
		rec.AnimB.Step()
		// Non-looping sequences' pointers cleared at termination inside Step [03 §1] C5.

		// Remove emptied records by stable left compaction within the same call [03 §1] C5.
		if rec.isEmpty(tick) {
			p.releaseFragment(rec.FragmentSlot)
			continue
		}
		if write != read {
			p.records[write] = *rec
		}
		// when write==read mutation already in place via pointer
		write++
	}
	for i := write; i < len(p.records); i++ {
		p.records[i] = EffectRecord{}
	}
	p.records = p.records[:write]
}

func validDurations(durations []int32) bool {
	if len(durations) == 0 {
		return false
	}
	for _, duration := range durations {
		if duration <= 0 {
			return false
		}
	}
	return true
}

func sameEndpoint(a, b, c, d pool.Handle) bool {
	return (a == b && (c == d || b == 0 || d == 0)) || (a == 0 && b == 0)
}
