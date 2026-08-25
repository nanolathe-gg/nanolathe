// Package units implements unit pools and lifecycle [04 §2] [PLAN_06 WU-06-1] [P0-16].
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P0-16] [01 §6.1]; Nanolathe uses named Go fields in a slot-indexed array
// parallel to pool.Units and does not reproduce packed bytes (I13).
type Unit struct {
	Handle    pool.Handle // TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// Owned exclusively by construction.Service; Units.Tick never mutates it [05 "Construction arithmetic"].
	Remaining    float32
	Flags        uint32       // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Pending      uint32       // capability/pending word for gate intersection [04 §3.3] C6
	Orders       any          // [04 §3.2] front/rear segment anchors on the unit (stored as *orders.Queue via opaque to avoid import cycle)
	Script       any          // COB VM placeholder [04 §4.2] (kept for backward compat, prefer ScriptState)
	GuardLatches GuardLatches // per-unit dedup array for guard assistance [04 §3.5]

	// Typed per-unit state introduced for P0-I02 real pipeline [04 §1.3][04 §4][06][GAP T15].
	// These fields own the authoritative per-unit data that the phase-2 sweep
	// visits in players-asc then slots-asc order [01 §6.2] C2 [P0-16].
	ScriptState   *ScriptState    // per-unit COB VM/thread/piece state [04 §4.1][04 §4.2][GAP T15]; nil if not yet wired
	Slots         [NumSlots]Slot  // three weapon slots [06 §1.2] C1 P0-10; local Slot avoids units→combat→economy→units cycle
	Move          MoveState       // movement status shared with movement.System [04 §8.1][04 §9.1] (movement imports units)
	Attachment    AttachmentState // carrier/cargo linkage [04 §4.4] attach-unit
	CallbackQueue CallbackQueue   // engine→COB callback queues/readiness [GAP T15]
	// SpotMetal is the extractor yield sampled once at placement: Σ(cellMetal+1)*extractsMetal [05 "Terrain metal extraction"] C14 [P1-10][P1-15].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	SpotMetal float32 // [P1-10] once Σ(byte+1)*extractsMetal, [P1-15] uniform char write
	// TODO(question): direct World.Create bypasses extractor sampling; session reconstructUnits and
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// but save-restore forced-slot and any other direct Create caller must also sample via the same hook
	// or via Terrain.ApplySchema post-load; verify universal coverage.
	Kills int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Placement linkage for P0-04/P0-06 sparse created[] semantics [P0-04][P0-06].
	// Retail maintains created[placementIdx] sparse array and scans it in
	// placement order 0..count-1 skipping NULL gaps for Ident→Unitname first-
	// occurrence resolution (A27). Store provenance to reconstruct that scan
	// without relying on dense w.Iter() prefix.
	PlacementIdx      int // index in Mission.Units placement order, -1 if not scenario-spawned
	PlacementIdent    string
	PlacementUnitName string
}

// DeathHook is invoked exactly once per unit at the moment Destroy first
// latches Dying [04 §2.4]. The session uses it to feed mission trigger death
// notifications exactly once [08 "Evaluation"].
type DeathHook func(h pool.Handle, cause DeathCause, u *Unit)

// World is the unit world [PLAN_06 Public API].
// Pool is slot-indexed parallel to world state; iteration is players 0..9
// then slots ascending [01 §6.2] C2 [P0-16]. Allocation is per-player sliced
// when created via NewSliced: physical cap = maxDefs*10+1 of 0x118 bytes at
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
type World struct {
	units   []*Unit
	pool    *pool.Units
	catalog *content.Catalog

	// OnDeath is the death-notification hook [08 "Evaluation"]; nil means no
	// consumer. It fires exactly once per unit, at the first Destroy latch.
	OnDeath DeathHook

	defMap    map[*content.UnitDef]uint16 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	nextDefID uint16
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	liveCounters [10]int
}

// New creates a World with given usable capacity (number of usable slots).
// This is the legacy unsliced constructor [04 §2.3]; for retail slicing use
// NewSliced with maxDefs [P0-16].
func New(capacity int, cat *content.Catalog) *World {
	p := pool.NewUnits(capacity)
	w := &World{
		pool:      p,
		catalog:   cat,
		units:     make([]*Unit, capacity+1), // index 0 null sentinel [01 §6.1] C1
		defMap:    make(map[*content.UnitDef]uint16),
		nextDefID: 1,
	}
	return w
}

// NewSliced creates a sliced retail pool for maxDefs catalog definitions
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// sliced per-player maxDefs each [P0-16 §3.1]. Stock ~2000-5001, not 500
// folklore [P0-16]. Slot 0 null, immediate reuse, no generation tags.
func NewSliced(maxDefs int, cat *content.Catalog) *World {
	p := pool.NewUnitsSliced(maxDefs)
	total := p.TotalRecords()
	if total < 1 {
		total = 1
	}
	w := &World{
		pool:      p,
		catalog:   cat,
		units:     make([]*Unit, total),
		defMap:    make(map[*content.UnitDef]uint16),
		nextDefID: 1,
	}
	return w
}

// IsSliced reports whether the world uses per-player slicing [P0-16 §3.1].
func (w *World) IsSliced() bool {
	if w == nil || w.pool == nil {
		return false
	}
	return w.pool.IsSliced()
}

// MaxDefs returns the catalog maxDefs used for slicing, or 0 if unsliced [P0-16 §3.1].
func (w *World) MaxDefs() int {
	if w == nil || w.pool == nil {
		return 0
	}
	return w.pool.MaxDefs()
}

// SliceForPlayer returns the inclusive bounds for the player's slice [P0-16 §3.1].
func (w *World) SliceForPlayer(player int) (int, int, bool) {
	if w == nil || w.pool == nil {
		return 0, 0, false
	}
	return w.pool.SliceForPlayer(player)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (w *World) SlotIndex(h pool.Handle) uint16 {
	if w == nil || w.pool == nil {
		return 0
	}
	return w.pool.SlotIndex(h)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// a new ID on first encounter. 0 is never returned (0 means free). DefID
// mapping is stable for the lifetime of the world; retail uses def array index
// via 0x1439B+defId*0x249 [P0-16 §3.2]. Zero RNG draws.
func (w *World) defIDForDef(def *content.UnitDef) uint16 {
	if def == nil {
		return 0
	}
	if w.defMap == nil {
		w.defMap = make(map[*content.UnitDef]uint16)
		w.nextDefID = 1
	}
	if id, ok := w.defMap[def]; ok {
		return id
	}
	id := w.nextDefID
	if id == 0 {
		id = 1
		w.nextDefID = 2
	}
	// Reserve 0 for free sentinel; skip 0 if wrap.
	if id == 0 {
		id++
	}
	w.defMap[def] = id
	w.nextDefID++
	if w.nextDefID == 0 {
		w.nextDefID = 1
	}
	return id
}

// Create allocates the lowest-free slot with slot 0 null, no generation tags,
// immediate reuse [01 §6.1] C1 [P0-16]. For sliced pools the allocation is
// per-player slice with per-def limit check [P0-16 §3.2]; a slice-full failure
// is reported even when global spare exists [P0-16 §7.3]. Zero RNG draws.
func (w *World) Create(def *content.UnitDef, owner uint8, x, y, z numeric.Fixed) (pool.Handle, error) {
	if w == nil || w.pool == nil {
		return 0, fmt.Errorf("units: nil world")
	}
	if def == nil {
		return 0, fmt.Errorf("units: nil def")
	}
	// Sliced path [P0-16 §3.2]
	if w.pool.IsSliced() {
		player := int(owner)
		if player < 0 || player >= 10 {
			return 0, fmt.Errorf("units: player %d out of range", player)
		}
		defID := w.defIDForDef(def)
		limitEnabled := def.LimitEnabled
		limit := def.Limit
		// Normalize: if limit == -1, treat as unlimited regardless of enabled bit
		if limit == -1 {
			limitEnabled = false
		}
		h, ok := w.pool.AllocForPlayerWithDef(player, defID, limitEnabled, limit)
		if !ok {
			// Distinguish per-def limit vs slice-full vs forced OOB; all return NULL in retail
			return 0, fmt.Errorf("units: pool exhausted")
		}
		idx := int(h)
		if idx >= len(w.units) {
			newUnits := make([]*Unit, idx+1)
			copy(newUnits, w.units)
			w.units = newUnits
		}
		u := &Unit{
			Handle:       h,
			Def:          def,
			Owner:        owner,
			X:            x,
			Y:            y,
			Z:            z,
			Alive:        true,
			Remaining:    0,
			MaxHealth:    int32(def.MaxDamage),
			Health:       int32(def.MaxDamage),
			PlacementIdx: -1,
		}
		w.units[idx] = u
		if player >= 0 && player < 10 {
			w.liveCounters[player]++
		}
		return h, nil
	}
	// Legacy unsliced path
	h, ok := w.pool.Alloc()
	if !ok {
		return 0, fmt.Errorf("units: pool exhausted")
	}
	// For unsliced pools also enforce per-def limit if enabled (global count)
	if def.LimitEnabled && def.Limit != -1 {
		cnt := 0
		for i := 1; i < len(w.units); i++ {
			if w.units[i] != nil && w.units[i].Alive && w.units[i].Def == def {
				cnt++
			}
		}
		if int32(cnt) >= def.Limit {
			// Roll back allocation without RNG draw
			w.pool.Free(h)
			return 0, fmt.Errorf("units: per-def limit %d reached", def.Limit)
		}
	}
	idx := int(h)
	if idx >= len(w.units) {
		newUnits := make([]*Unit, idx+1)
		copy(newUnits, w.units)
		w.units = newUnits
	}
	// Stamp defID sentinel for unsliced occupancy tracking
	if w.pool != nil {
		defID := w.defIDForDef(def)
		w.pool.SetDefID(h, defID)
	}
	u := &Unit{
		Handle:       h,
		Def:          def,
		Owner:        owner,
		X:            x,
		Y:            y,
		Z:            z,
		Alive:        true,
		Remaining:    0,
		MaxHealth:    int32(def.MaxDamage),
		Health:       int32(def.MaxDamage),
		PlacementIdx: -1,
	}
	w.units[idx] = u
	return h, nil
}

// CreateWithForcedSlot allocates a unit at the exact forcedSlot for save
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Zero RNG draws. Returns error on limit/slice-full/forced-OOB/occupied.
func (w *World) CreateWithForcedSlot(def *content.UnitDef, owner uint8, x, y, z numeric.Fixed, forced pool.Handle) (pool.Handle, error) {
	if w == nil || w.pool == nil {
		return 0, fmt.Errorf("units: nil world")
	}
	if def == nil {
		return 0, fmt.Errorf("units: nil def")
	}
	if forced == 0 {
		return 0, fmt.Errorf("units: forced slot 0 is null")
	}
	player := int(owner)
	if w.pool.IsSliced() {
		if player < 0 || player >= 10 {
			return 0, fmt.Errorf("units: player %d out of range", player)
		}
		defID := w.defIDForDef(def)
		limitEnabled := def.LimitEnabled
		limit := def.Limit
		if limit == -1 {
			limitEnabled = false
		}
		h, ok := w.pool.AllocForcedWithDef(player, defID, forced, limitEnabled, limit)
		if !ok {
			return 0, fmt.Errorf("units: forced slot %d rejected (OOB/occupied/limit)", forced)
		}
		idx := int(h)
		if idx >= len(w.units) {
			newUnits := make([]*Unit, idx+1)
			copy(newUnits, w.units)
			w.units = newUnits
		}
		u := &Unit{
			Handle:       h,
			Def:          def,
			Owner:        owner,
			X:            x,
			Y:            y,
			Z:            z,
			Alive:        true,
			Remaining:    0,
			MaxHealth:    int32(def.MaxDamage),
			Health:       int32(def.MaxDamage),
			PlacementIdx: -1,
		}
		w.units[idx] = u
		w.liveCounters[player]++
		return h, nil
	}
	// Unsliced forced path: verify slot free and not OOB via pool
	h, ok := w.pool.AllocForced(player, forced)
	if !ok {
		return 0, fmt.Errorf("units: forced slot %d rejected", forced)
	}
	idx := int(h)
	if idx >= len(w.units) {
		newUnits := make([]*Unit, idx+1)
		copy(newUnits, w.units)
		w.units = newUnits
	}
	w.pool.SetDefID(h, w.defIDForDef(def))
	u := &Unit{
		Handle:       h,
		Def:          def,
		Owner:        owner,
		X:            x,
		Y:            y,
		Z:            z,
		Alive:        true,
		Remaining:    0,
		MaxHealth:    int32(def.MaxDamage),
		Health:       int32(def.MaxDamage),
		PlacementIdx: -1,
	}
	w.units[idx] = u
	return h, nil
}

// Destroy marks death; the slot stays alive and visible until post-tick
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// Exactly-once death notification [08 "Evaluation"]: the latch transition
	// is the single fire point.
	if w.OnDeath != nil {
		w.OnDeath(h, cause, u)
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// reusable the same tick if the slice is still ahead in the 0..9 asc scan
// [P0-16 §6.3]. Zero RNG draws.
func (w *World) FreeImmediate(h pool.Handle) {
	if w == nil || w.pool == nil || h == 0 {
		return
	}
	idx := int(h)
	if idx <= 0 || idx >= len(w.units) {
		// Still need to free pool slot if world slice shorter due to growth race
		w.pool.Free(h)
		return
	}
	u := w.units[idx]
	if u == nil || !w.pool.Alive(h) {
		// Already free; ensure pool defID cleared
		w.pool.Free(h)
		return
	}
	// Clear unit linkage: queues, attachments, etc would be cleared here;
	// for Nanolathe the world entry is nulled and pool occupancy cleared,
	// but slotIndex retained stale [P0-16 §3.4].
	player := int(u.Owner)
	u.Alive = false
	w.units[idx] = nil
	w.pool.Free(h)
	if player >= 0 && player < 10 && w.liveCounters[player] > 0 {
		w.liveCounters[player]--
	}
	// Note: w.defMap retains the def->ID mapping (IDs are not recycled) so
	// future per-def scans remain stable; this matches retail where defId
	// itself is the catalog index not a generation.
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// validation: u16 slot targeting validates only slot!=0 && alive
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// reused the damage aliases the new occupant silently [P0-16 §6][06 "Damage
// identity"]. No generation tag anywhere (bounded 3901) [P0-16 §2.2].
// Returns false if validation fails (slot 0 or dead/free). Zero RNG draws.
func (w *World) ApplyDamage(target pool.Handle, dmg int32) bool {
	if w == nil || w.pool == nil || target == 0 {
		return false
	}
	if !w.pool.Alive(target) {
		return false
	}
	u := w.Unit(target)
	if u == nil {
		return false
	}
	u.Health -= dmg
	if u.Health <= 0 {
		// Mark dying but do not free immediately; caller may FreeImmediate
		// separately. Stale alias after free will hit next occupant.
		u.Health = 0
	}
	return true
}

// Cleanup frees death-marked slots now that tick is done, matching retail
// deferral [04 §2.4]. Call after Tick sweep. For sliced pools this clears
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Zero RNG draws.
func (w *World) Cleanup() {
	if w == nil || w.pool == nil {
		return
	}
	for i := 1; i < len(w.units); i++ {
		u := w.units[i]
		if u != nil && u.Dying {
			player := int(u.Owner)
			u.Alive = false
			w.units[i] = nil
			w.pool.Free(pool.Handle(i))
			if player >= 0 && player < 10 && w.liveCounters[player] > 0 {
				w.liveCounters[player]--
			}
		}
	}
}

// Unit returns the unit for handle or nil for slot 0 or dead [PLAN_06].
// Validation is slot!=0 && alive, so a stale handle that has been freed and
// reused aliases the new occupant [P0-16 §6].
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

// Capacity returns usable slot count excluding null sentinel [P0-16][01 §6.1].
func (w *World) Capacity() int {
	if w == nil || w.pool == nil {
		return 0
	}
	return w.pool.Capacity()
}

// TotalRecords returns total record count including null sentinel [P0-16 §3.1].
func (w *World) TotalRecords() int {
	if w == nil || w.pool == nil {
		return 0
	}
	return w.pool.TotalRecords()
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (w *World) LiveCountForPlayer(player int) int {
	if w == nil || player < 0 || player >= 10 {
		return 0
	}
	return w.liveCounters[player]
}

// Tick sweeps players ascending then slots ascending [01 §6.2] C2.
// Alive state and death mark separate; death clears alive during post-tick cleanup [04 §2.4] C2.
// Construction Remaining is owned exclusively by construction.Service and is never
// mutated here [05 "Construction target state"] [05 "Construction arithmetic"].
// Per-unit pipeline per [04 §1.3][GAP T15] C17 (I7): pre-update → water damage →
// weapon-slot update (Aim can block) → normal COB drain delta 1 → deferred
// build/order → preserved movement → slot-end death handling. Tick never frees
// slots; Cleanup handles that in phase 10 [04 §2.4] C2.
func (w *World) Tick(tick uint32) {
	if w == nil || w.units == nil {
		return
	}
	// Players 0..9 ascending, slots ascending [01 §6.2] [PLAN_06 C2] [P0-16 §3.1]
	// For sliced pools, per-player slices are scanned; for unsliced, global scan
	// with player filter. No map iteration; deterministic (I1).
	if w.pool != nil && w.pool.IsSliced() {
		for player := 0; player < 10; player++ {
			start, end, ok := w.pool.SliceForPlayer(player)
			if !ok {
				continue
			}
			for slot := start; slot <= end && slot < len(w.units); slot++ {
				u := w.units[slot]
				if u == nil || !u.Alive {
					continue
				}
				if int(u.Owner) != player {
					continue
				}
				// Real per-unit pipeline; does not mutate Remaining [05 "Construction target state"].
				w.tickUnit(u, tick) // [04 §1.3][GAP T15] C17
			}
		}
		return
	}
	for player := 0; player < 10; player++ {
		for slot := 1; slot < len(w.units); slot++ {
			u := w.units[slot]
			if u == nil || !u.Alive {
				continue
			}
			if int(u.Owner) != player {
				continue
			}
			w.tickUnit(u, tick) // [04 §1.3][GAP T15] C17; no Remaining mutation
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

// IterSliced returns units in sliced deterministic order: players 0..9 asc,
// slots asc within each slice [P0-16 §3.1]. For unsliced pools it falls back
// to Iter.
func (w *World) IterSliced() []*Unit {
	if w == nil {
		return nil
	}
	if w.pool == nil || !w.pool.IsSliced() {
		return w.Iter()
	}
	var out []*Unit
	for player := 0; player < 10; player++ {
		start, end, ok := w.pool.SliceForPlayer(player)
		if !ok {
			continue
		}
		for slot := start; slot <= end && slot < len(w.units); slot++ {
			if u := w.units[slot]; u != nil && u.Alive {
				out = append(out, u)
			}
		}
	}
	return out
}
