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

// TODO(question): [04 §3.5] establishes the per-unit dedup array but not its capacity.
const GuardLatchSize = 8

// GuardLatches holds the per-unit dedup arrays for guard assistance triggers [04 §3.5].
// Four classes × fixed-size arrays of pool.Handle; auto-fire per weapon slot.
type GuardLatches struct {
	BuildAssist [GuardLatchSize]pool.Handle
	AutoFire    [3][GuardLatchSize]pool.Handle
	Repair      [GuardLatchSize]pool.Handle
	HelpBuild   [GuardLatchSize]pool.Handle
}

// Unit is a live unit instance [04 §2.3] C1.
// Retail unit records have 280-byte identity; Nanolathe uses named Go fields in a slot-indexed array [PLAN_06 C1] (I13).
type Unit struct {
	Handle    pool.Handle // slot index, 0 null [01 §6.1]
	Def       *content.UnitDef
	Owner     uint8 // 0..9 [04 §2]
	X, Y, Z   numeric.Fixed
	Health    int32 // current health; max from Def?
	MaxHealth int32
	Alive     bool // slot valid; cleared only by post-tick cleanup [04 §2.4] C2
	// Dying is the death mark, separate from Alive [04 §2.3] C2: Destroy sets
	// it and the unit stays visible to the sweep and to Unit() until Cleanup
	// frees the slot.
	Dying      bool
	DeathCause DeathCause
	// Build progress remaining 1→0 [04 §2.3] C3. float32 per the I2 allowlist
	// row "Construction remaining fraction" [05 "Construction target state"].
	Remaining    float32
	Flags        uint32       // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Pending      uint32       // capability/pending word for gate intersection [04 §3.3] C6
	Orders       any          // [04 §3.2] front/rear segment anchors on the unit (stored as *orders.Queue via opaque to avoid import cycle)
	Script       any          // COB VM placeholder [04 §4.2]
	GuardLatches GuardLatches // per-unit dedup array for guard assistance [04 §3.5]
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

// Destroy marks death; the slot stays alive and visible until post-tick
// cleanup [04 §2.3][04 §2.4] C2.
func (w *World) Destroy(h pool.Handle, cause DeathCause) {
	if w == nil || w.pool == nil || !w.pool.Alive(h) {
		return
	}
	idx := int(h)
	if idx >= len(w.units) || w.units[idx] == nil {
		return
	}
	u := w.units[idx]
	if u.Dying {
		return // already marked
	}
	u.Dying = true
	u.DeathCause = cause
}

// Cleanup frees death-marked slots now that tick is done, matching retail
// deferral [04 §2.4]. Call after Tick sweep.
func (w *World) Cleanup() {
	if w == nil || w.pool == nil {
		return
	}
	for i := 1; i < len(w.units); i++ {
		u := w.units[i]
		if u != nil && u.Dying {
			u.Alive = false
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
			// Remaining runs 1→0 [C3]. The fixed 0.01 decrement is a Gate-2
			// placeholder; the authoritative worker quantum and fractional
			// carry are PLAN_08 C24's, not this package's.
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
