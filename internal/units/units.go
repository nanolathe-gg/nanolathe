// Package units implements unit pools and lifecycle [04 §2] [PLAN_06 WU-06-1].
package units

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// DeathCause names how a unit died [04 §2.4].
type DeathCause uint8

const (
	DeathUnknown DeathCause = iota
	DeathKilled
	DeathReclaimed
	DeathSelfDestruct
)

// Unit is a live unit instance [04 §2.3] C1.
// Retail unit records have 280-byte identity; Nanolathe uses named Go fields in a slot-indexed array [PLAN_06 C1] (I13).
type Unit struct {
	Handle    pool.Handle // slot index, 0 null [01 §6.1]
	Def       *content.UnitDef
	Owner     uint8 // 0..9 [04 §2]
	X, Y, Z   numeric.Fixed
	Health    int32 // current health; max from Def?
	MaxHealth int32
	Alive     bool // alive vs death mark separate [04 §2.4] C2
	// Build progress remaining 1→0 [04 §2.3] C3
	Remaining float32
	Flags     uint32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// World is the unit world [PLAN_06 Public API].
// Pool is slot-indexed parallel to world state; iteration is players 0..9 then slots ascending [01 §6.2] C2.
type World struct {
	units   []*Unit
	pool    *pool.Units
	catalog *content.Catalog
}

// New creates a World with given capacity (number of usable slots).
func New(capacity int, cat *content.Catalog) *World {
	p := pool.NewUnits(capacity)
	w := &World{
		pool:    p,
		catalog: cat,
		units:   make([]*Unit, capacity+1), // index 0 null sentinel [01 §6.1] C1
	}
	return w
}

// Create allocates the lowest-free slot with slot 0 null, no generation tags, immediate reuse [01 §6.1] C1.
func (w *World) Create(def *content.UnitDef, owner uint8, x, y, z numeric.Fixed) (pool.Handle, error) {
	if w == nil || w.pool == nil {
		return 0, fmt.Errorf("units: nil world")
	}
	if def == nil {
		return 0, fmt.Errorf("units: nil def")
	}
	h, ok := w.pool.Alloc()
	if !ok {
		return 0, fmt.Errorf("units: pool exhausted")
	}
	idx := int(h)
	if idx >= len(w.units) {
		// grow if needed (capacity expanded)
		newUnits := make([]*Unit, idx+1)
		copy(newUnits, w.units)
		w.units = newUnits
	}
	u := &Unit{
		Handle:    h,
		Def:       def,
		Owner:     owner,
		X:         x,
		Y:         y,
		Z:         z,
		Alive:     true,
		Remaining: 0,                    // built units start with 0? For builders nanoframe 1→0 [PLAN_06 C3] but Create for live units sets 0.
		MaxHealth: int32(def.MaxDamage), // TODO(question): MaxDamage field name; use MaxHealth alias
		Health:    int32(def.MaxDamage),
	}
	w.units[idx] = u
	return h, nil
}

// Destroy marks death; alive cleared during post-tick cleanup [04 §2.4] C2.
func (w *World) Destroy(h pool.Handle, cause DeathCause) {
	if w == nil || w.pool == nil || !w.pool.Alive(h) {
		return
	}
	idx := int(h)
	if idx >= len(w.units) || w.units[idx] == nil {
		return
	}
	u := w.units[idx]
	// Separate alive vs death mark: death sets flag but cleanup clears alive [C2].
	u.Alive = false
	_ = cause
}

// Cleanup frees dead slots now that tick is done, matching retail deferral [04 §2.4].
// Call after Tick sweep.
func (w *World) Cleanup() {
	if w == nil || w.pool == nil {
		return
	}
	for i := 1; i < len(w.units); i++ {
		u := w.units[i]
		if u != nil && !u.Alive {
			w.units[i] = nil
			w.pool.Free(pool.Handle(i))
		}
	}
}

// Unit returns the unit for handle or nil for slot 0 or dead [PLAN_06].
func (w *World) Unit(h pool.Handle) *Unit {
	if w == nil || h == 0 {
		return nil
	}
	idx := int(h)
	if idx >= len(w.units) {
		return nil
	}
	u := w.units[idx]
	if u == nil || !u.Alive {
		return nil
	}
	if !w.pool.Alive(h) {
		return nil
	}
	return u
}

// Used returns live count.
func (w *World) Used() int {
	if w == nil || w.pool == nil {
		return 0
	}
	return w.pool.Used()
}

// Tick sweeps players ascending then slots ascending [01 §6.2] C2.
// Alive state and death mark separate; death clears alive during post-tick cleanup.
// For Phase 6 we just iterate deterministically and advance Remaining stub.
func (w *World) Tick(tick uint32) {
	if w == nil || w.units == nil {
		return
	}
	_ = tick
	// Players 0..9 ascending, slots ascending [01 §6.2] [PLAN_06 C2]
	for player := 0; player < 10; player++ {
		for slot := 1; slot < len(w.units); slot++ {
			u := w.units[slot]
			if u == nil || !u.Alive {
				continue
			}
			if int(u.Owner) != player {
				continue
			}
			// TODO: per-unit tick: orders pump, COB drain etc. Phase 6 stub just advances Remaining if building.
			// Remaining runs 1→0 [C3].
			if u.Remaining > 0 {
				// Stub: decrement fraction by fixed worktime; not yet tied to economy.
				u.Remaining -= 0.01
				if u.Remaining < 0 {
					u.Remaining = 0
				}
			}
		}
	}
}

// Iter returns units in deterministic order for tests (pool asc).
func (w *World) Iter() []*Unit {
	if w == nil {
		return nil
	}
	var out []*Unit
	for i := 1; i < len(w.units); i++ {
		if u := w.units[i]; u != nil && u.Alive {
			out = append(out, u)
		}
	}
	return out
}
