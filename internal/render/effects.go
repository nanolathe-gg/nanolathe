// Package render implements the fixed effect pool [03 §1] C5.
package render

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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
	Idx       int32 // current frame index
	Countdown int32 // ticks remaining for current frame; countdown <2 advances [03 §4.4]
	Loop      bool  // loop-or-hold flag [03 §4.4]
	Active    bool  // sequence pointer present; false == cleared/terminated [03 §1]
	Frames    int   // frame count for this sequence; 0 means no sequence
}

// Step single-steps the player by one simulation tick [03 §1] C5.
// Countdown <2 advances to the next frame, wraps to 0 for looping sequences
// or clears the entry pointer for non-looping sequences and loads the new
// frame's duration [03 §4.4]. Retail durations are whole ticks [03 §4.4]; this
// pool uses 1 tick per frame as the minimal established duration (A24).
// TODO(question): authored per-frame durations for fixed effects are not
// established in [03 §1]; this uses 1 tick per frame. If probes show a
// different cadence, replace the constant (A24).
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
				a.Countdown = 1
			} else {
				// clears non-looping sequences' pointers at termination [03 §1] C5
				a.Active = false
				a.Idx = 0
				a.Countdown = 0
				return
			}
		} else {
			a.Countdown = 1 // load new frame's duration [03 §4.4]
		}
	} else {
		a.Countdown--
	}
}

// EffectRecord is one fixed-size effect record in the 300-entry pool [03 §1] C5.
// Retail size is 0x54 bytes per I5; Go uses named fields per I13.
type EffectRecord struct {
	// Position and velocity in 16.16 world units [I2][03 §2.1].
	X, Y, Z    numeric.Fixed // current position [03 §1]
	VX, VY, VZ numeric.Fixed // velocity; advanced against gravity each tick [03 §1]
	Gravity    numeric.Fixed // per-record gravity; zero means use pool default [03 §2.2]

	// Two embedded animation players [03 §1] C5.
	AnimA EffectAnimPlayer
	AnimB EffectAnimPlayer

	// HasModel indicates a model-bearing record; the integrator can clear the
	// model pointer on terrain/water contact as the alternative to bouncing [03 §1] C5.
	HasModel bool
}

// isEmpty reports whether the record is emptied and should be removed.
// A record is emptied when both embedded animation players are inactive
// (cleared/terminated) and it has no model pointer [03 §1] C5.
func (r *EffectRecord) isEmpty() bool {
	if r == nil {
		return true
	}
	return !r.AnimA.Active && !r.AnimB.Active && !r.HasModel
}

// Len returns the number of live records [03 §1] C5.
func (p *FixedEffectPool) Len() int {
	if p == nil {
		return 0
	}
	return len(p.records)
}

// Cap returns the fixed capacity 300 [03 §1] C5 (I5).
func (p *FixedEffectPool) Cap() int { return FixedEffectCap }

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
		p.records[i] = EffectRecord{}
	}
	p.records = p.records[:0]
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
	if len(p.records) >= FixedEffectCap { // at or above the cap allocates nothing [03 §1] C5
		return false
	}
	if p.records == nil {
		p.records = make([]EffectRecord, 0, FixedEffectCap)
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

		// Single-step both embedded animation players [03 §1] C5.
		rec.AnimA.Step()
		rec.AnimB.Step()
		// Non-looping sequences' pointers cleared at termination inside Step [03 §1] C5.

		// Remove emptied records by stable left compaction within the same call [03 §1] C5.
		if rec.isEmpty() {
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

// SnapshotEffectsCapacity is the presentation-side bound for snapshot effect
// views. It mirrors the fixed pool capacity [03 §1] C5 but is intentionally
// separate from the strip's 400-object eviction bound [R-P0-06].
const SnapshotEffectsCapacity = FixedEffectCap

// SnapshotEffectIsVisual reports whether an EffectView originates from a
// visual event kind that should be drawn in the world pass. Shake and sound
// are consumed via Frame.Events, not via the effect strip [03 §5.6][03 §8.3].
func SnapshotEffectIsVisual(kind string) bool {
	switch kind {
	case "nanolathe", "muzzle_flash", "smoke_start", "smoke_end", "projectile_trail", "impact", "water_impact", "explosion", "lht_flash", "cob_sfx", "corpse":
		return true
	default:
		return false
	}
}
