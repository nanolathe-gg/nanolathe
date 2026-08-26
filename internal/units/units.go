// Package units implements unit pools and lifecycle [04 §2] [PLAN_06 WU-06-1] [P0-16].
package units

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/vfs"
)

// DeathCause names how a unit died [04 §2.4].
type DeathCause uint8

const (
	DeathUnknown DeathCause = iota
	DeathKilled
	DeathReclaimed
	DeathSelfDestruct
)

// ClassifierEligibleStatus is the runtime unit-status bit consumed by the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// and is cleared by death finalization [R-P0-04 "Runtime eligibility bit
// lifecycle"].
const ClassifierEligibleStatus uint32 = 0x00000020

const classifierSelectableClear uint32 = 0x00008000

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
	Dying               bool
	DeathCause          DeathCause
	deathHookFired      bool // internal: ensures OnDeath fires exactly once via Destroy or FinalizeDeath [01 §4.4][04 "unit sweep"]
	deathExtraHookFired bool // internal composition observer deduplication
	// Build progress remaining 1→0 [04 §2.3] C3. float32 per the I2 allowlist
	// row "Construction remaining fraction" [05 "Construction target state"].
	// Owned exclusively by construction.Service; Units.Tick never mutates it [05 "Construction arithmetic"].
	Remaining float32
	Flags     uint32 // runtime status bits; bit 0x20 is allocator-initialized [R-P0-04]
	// The six engine-write port markers occupy the instance stance byte's
	// low six bits [R-P0-10]. They remain named fields so production code does
	// not confuse the classifier bit in Flags with COB state.
	InBuildStance bool         // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Busy          bool         // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	YardOpen      bool         // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	BuggerOff     bool         // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Armored       bool         // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Group         uint8        // one stored control-group value 0..9 [07 §9]
	Pending       uint32       // capability/pending word for gate intersection [04 §3.3] C6
	Orders        any          // [04 §3.2] front/rear segment anchors on the unit (stored as *orders.Queue via opaque to avoid import cycle)
	Script        *cob.VM      // typed COB VM per-unit [04 §4.2][P1-I01] — not any, typed per acceptance
	GuardLatches  GuardLatches // per-unit dedup array for guard assistance [04 §3.5]

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
	// Economy state bound to the one ledger per [05] — activation/on-off, cloak, storage, extraction, wind/tidal, makers [P1-I04].
	Activated bool  // operational/activated bit for on/offable units [05 "Unit instance economy state"] [P1-I04]; true when the unit is turned on; for non-OnOffable units always true when complete
	IsCloaked bool  // whether cloak upkeep is due this pass [05 "Cloak debit"] [P1-I04]
	Kills     int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Paralyze state per [06 §10] paralyzer status effects [P0-I04].
	ParalyzeExpire uint32 // absolute tick when stun ends; 0 means not paralyzed [06 §10]
	Stunned        bool   // TODO(question): GAI slow vs binary stun remains open [06 §10]
	PriorSample    uint8  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Placement linkage for P0-04/P0-06 sparse created[] semantics [P0-04][P0-06].
	// Retail maintains created[placementIdx] sparse array and scans it in
	// placement order 0..count-1 skipping NULL gaps for Ident→Unitname first-
	// occurrence resolution (A27). Store provenance to reconstruct that scan
	// without relying on dense w.Iter() prefix.
	PlacementIdx      int // index in Mission.Units placement order, -1 if not scenario-spawned
	PlacementIdent    string
	PlacementUnitName string
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// 0x8000, then set the classifier/selection eligibility bit 0x20. The order
// operates on the runtime status word (Flags here); it does not synthesize
// COB INBUILDSTANCE, which is kept in InBuildStance.
func (u *Unit) MakeSelectable() {
	if u == nil {
		return
	}
	u.Flags = (u.Flags &^ classifierSelectableClear) | ClassifierEligibleStatus
}

// ClearClassifierEligibility clears the retail runtime eligibility bit.
func (u *Unit) ClearClassifierEligibility() {
	if u != nil {
		u.Flags &^= ClassifierEligibleStatus
	}
}

// EconomyActive reports whether the unit is eligible for passive economy
// production and storage [05 "Completed-unit eligibility"] [P1-I04].
// Requires Remaining==0 and alive; for OnOffable units also requires Activated.
func (u *Unit) EconomyActive() bool {
	if u == nil || !u.Alive || u.Dying || u.Remaining != 0 {
		return false
	}
	if u.Def != nil && u.Def.OnOffable {
		return u.Activated
	}
	return true
}

// EconomyOperational reports whether wind/tidal generators may run.
// Per [05 "Resource contributions"] wind and tidal require the operational bit
// and a secondary state bit; we model it as EconomyActive (complete and on).
func (u *Unit) EconomyOperational() bool {
	return u.EconomyActive()
}

// SetActivated toggles activation for OnOffable units [05] [P1-I04].
func (u *Unit) SetActivated(on bool) {
	if u == nil {
		return
	}
	u.Activated = on
}

// SetCloaked sets cloak state for upkeep debit [05 "Cloak debit"] [P1-I04].
func (u *Unit) SetCloaked(on bool) {
	if u == nil {
		return
	}
	u.IsCloaked = on
}

// CloakCost returns the per-pass cloak cost, choosing stationary vs moving
// variant when the unit is cloaked [05 "Cloak debit"] [P1-I04].
func (u *Unit) CloakCost() float32 {
	if u == nil || u.Def == nil || !u.IsCloaked {
		return 0
	}
	if u.Move.Speed != 0 {
		return float32(u.Def.CloakCostMoving)
	}
	return float32(u.Def.CloakCost)
}

// InitEconomyState initializes Activated and IsCloaked from the definition
// per [02 "Unit record"] ActivateWhenBuilt / OnOffable / InitCloaked [P1-I04].
func (u *Unit) InitEconomyState() {
	if u == nil || u.Def == nil {
		return
	}
	if u.Def.OnOffable {
		u.Activated = u.Def.ActivateWhenBuilt
	} else {
		u.Activated = true
	}
	u.IsCloaked = u.Def.InitCloaked
}

// DeathHook is invoked exactly once per unit at the moment Destroy first
// latches Dying [04 §2.4]. The session uses it to feed mission trigger death
// notifications exactly once [08 "Evaluation"].
type DeathHook func(h pool.Handle, cause DeathCause, u *Unit)

// CreateHook is invoked exactly once per unit at creation, after the unit
// is inserted into the world and before any visibility publish [08
// "Evaluation"] slot 3 present but unused by shipped conditions; bind exactly
// once so future conditions see it without extra polling.
type CreateHook func(h pool.Handle, u *Unit)

// CaptureHook is invoked exactly once per capture transfer, after ownership
// has changed and visibility republished [08 "Evaluation"] slot 2
// capture/transfer notification. Session.CaptureUnit and any construction
// capture path feed it.
type CaptureHook func(h pool.Handle, oldOwner, newOwner uint8, u *Unit)

// COBBinder is the composition-owned production attachment seam. It runs
// before a newly allocated unit becomes observable through OnCreate. A
// strict session binder resolves the authored model/script, invokes Create
// exactly once, and returns a fatal error for missing or malformed assets.
// Synthetic fixtures continue to use SetScript directly.
type COBBinder func(*Unit) error

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
	// OnDeathExtra is a narrow composition observer that survives replacement
	// of the primary session hook. It fires at the same latch, independently
	// deduplicated, and must not emit duplicate Killed/corpse notifications.
	OnDeathExtra DeathHook
	// OnCreate is the creation-notification hook [08 "Evaluation"] slot 3;
	// nil means no consumer. It fires exactly once per unit after Create inserts.
	OnCreate CreateHook
	// OnCapture is the capture-transfer hook [08 "Evaluation"] slot 2; nil means
	// no consumer. It fires exactly once per ownership transfer.
	OnCapture CaptureHook

	defMap    map[*content.UnitDef]uint16 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	nextDefID uint16
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	liveCounters [10]int

	// COB loader for per-unit VM creation [04 §4.1][P1-I01].
	cobFS     vfs.FSOps
	cobLoader *cob.CachedLoader
	// cobBinder supersedes the legacy empty fallback when installed by session
	// composition. It is also used for units created by construction and save
	// reconstruction, so every production unit follows one path.
	cobBinder COBBinder
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

// SetCOBSource installs the VFS-backed COB loader for per-unit VM creation [04 §4.1][P1-I01].
// When set, every successful Create/CreateWithForcedSlot will load the unit's Program via cob.Load
// (or cached lookup) and create a VM with statics zero-init, piece count from program, 8 threads [04 §4.2] C13,
// and immediately start the Create script if present (hide muzzle etc.) [04 §4.1][GAP T15].
// The loader is used for all future units, including those created by construction.
func (w *World) SetCOBSource(fs vfs.FSOps, loader *cob.CachedLoader) {
	if w == nil {
		return
	}
	w.cobFS = fs
	w.cobLoader = loader
}

// COBSource returns the VFS and loader configured for production attachment.
// It is intentionally read-only so session composition can install one strict
// binder without reaching into pool state.
func (w *World) COBSource() (vfs.FSOps, *cob.CachedLoader) {
	if w == nil {
		return nil, nil
	}
	return w.cobFS, w.cobLoader
}

// SetCOBBinder installs a strict composition-owned binder for future unit
// allocations. A nil binder restores the legacy fixture/source path.
func (w *World) SetCOBBinder(binder COBBinder) {
	if w == nil {
		return
	}
	w.cobBinder = binder
}

// HasCOBBinder reports whether strict composition owns future allocations.
func (w *World) HasCOBBinder() bool { return w != nil && w.cobBinder != nil }

// attachCOB loads the unit's Program and binds a VM with Create started [04 §4.1][P1-I01].
func (w *World) attachCOB(u *Unit) error {
	if w == nil || u == nil || u.Def == nil {
		return nil
	}
	if u.GetScript() != nil {
		return nil // already has VM [P1-I01]
	}
	if w.cobBinder != nil {
		return w.cobBinder(u)
	}
	var prog *cob.Program
	var found bool
	if w.cobLoader != nil && w.cobFS != nil {
		// Try via loader (cached, case-insensitive) [04 §4.1]
		if p, ok, _ := w.cobLoader.Load(w.cobFS, u.Def.UnitName); ok && p != nil {
			prog = p
			found = true
		} else if p, ok, _ := w.cobLoader.Load(w.cobFS, u.Def.CanonicalKey); ok && p != nil {
			prog = p
			found = true
		}
	}
	if !found {
		// Empty fallback program: zero statics, zero pieces, no scripts [04 §4.2][P1-I01].
		// Keep drain path consistent; Create is no-op.
		prog = &cob.Program{
			Code:        []uint32{},
			Scripts:     map[string]int{},
			Pieces:      []string{},
			Statics:     0,
			ScriptsByID: []int{},
		}
	}
	vm := cob.NewVM(prog)
	bindUnitPortHandlers(vm, u)
	u.SetScript(vm)
	// Run Create immediately with wake flag (delta 0 barrier) so hide/show etc. are visible before first snapshot [04 §4.1][GAP T15].
	if prog != nil {
		if _, ok := prog.Scripts["Create"]; ok {
			_ = vm.StartByName("Create", nil)
			vm.Drain(0) // immediate wake-flag drain [GAP T15] C17
		}
	}
	return nil
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
			Flags:        ClassifierEligibleStatus,
			Remaining:    0,
			MaxHealth:    int32(def.MaxDamage),
			Health:       int32(def.MaxDamage),
			PlacementIdx: -1,
		}
		installWeapons(u, def) // [06 §1.2] wire Weapon1/2/3 definitions into Slots [P0-I04]
		u.InitEconomyState()   // [P1-I04] on/off, cloak, activation from definition
		w.units[idx] = u
		if err := w.attachCOB(u); err != nil {
			w.units[idx] = nil
			w.pool.Free(h)
			return 0, fmt.Errorf("units: strict COB binding for %q: %w", def.UnitName, err)
		} // per-unit VM with statics/pieces, Create run [04 §4.1][P1-I01]
		if player >= 0 && player < 10 {
			w.liveCounters[player]++
		}
		if w.OnCreate != nil {
			w.OnCreate(h, u)
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
		Flags:        ClassifierEligibleStatus,
		Remaining:    0,
		MaxHealth:    int32(def.MaxDamage),
		Health:       int32(def.MaxDamage),
		PlacementIdx: -1,
	}
	installWeapons(u, def) // [06 §1.2] wire Weapon1/2/3 definitions [P0-I04]
	u.InitEconomyState()   // [P1-I04]
	w.units[idx] = u
	if err := w.attachCOB(u); err != nil {
		w.units[idx] = nil
		w.pool.Free(h)
		return 0, fmt.Errorf("units: strict COB binding for %q: %w", def.UnitName, err)
	} // [P1-I01] VM per-unit
	if w.OnCreate != nil {
		w.OnCreate(h, u)
	}
	return h, nil
}

// installWeapons copies Weapon1/2/3 definitions from UnitDef into Units.Slots [06 §1.2] C1 [P0-I04].
// It is the sole wiring of weapon definitions to per-unit slots; no other site fabricates them.
func installWeapons(u *Unit, def *content.UnitDef) {
	if u == nil || def == nil {
		return
	}
	// [04 §5.3] muzzle piece identity queried synchronously via AimFrom→Query fallback;
	// a missing COB query leaves -1 so muzzleWorldPosResolved falls back to root
	// rather than piece 0. Seed default before wiring weapons [06 §4.1] C3.
	for i := 0; i < NumSlots; i++ {
		u.Slots[i].MuzzlePiece = -1 // [04 §5.3] [06 §4.1] C3
	}
	if def.Weapon1Def != nil {
		u.Slots[0].Weapon = def.Weapon1Def
		u.Slots[0].Flags |= 0x02 // armed/hasTarget when populated [06 §1.2] P0-10
	}
	if def.Weapon2Def != nil {
		u.Slots[1].Weapon = def.Weapon2Def
		u.Slots[1].Flags |= 0x02
	}
	if def.Weapon3Def != nil {
		u.Slots[2].Weapon = def.Weapon3Def
		u.Slots[2].Flags |= 0x02
	}
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
			Flags:        ClassifierEligibleStatus,
			Remaining:    0,
			MaxHealth:    int32(def.MaxDamage),
			Health:       int32(def.MaxDamage),
			PlacementIdx: -1,
		}
		installWeapons(u, def) // [06 §1.2] wire Weapon1/2/3 definitions [P0-I04]
		u.InitEconomyState()   // [P1-I04]
		w.units[idx] = u
		if err := w.attachCOB(u); err != nil {
			w.units[idx] = nil
			w.pool.Free(h)
			return 0, fmt.Errorf("units: strict COB binding for %q: %w", def.UnitName, err)
		} // [P1-I01] VM per-unit for forced slot
		w.liveCounters[player]++
		if w.OnCreate != nil {
			w.OnCreate(h, u)
		}
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
		Flags:        ClassifierEligibleStatus,
		Remaining:    0,
		MaxHealth:    int32(def.MaxDamage),
		Health:       int32(def.MaxDamage),
		PlacementIdx: -1,
	}
	installWeapons(u, def) // [06 §1.2] wire Weapon1/2/3 definitions [P0-I04]
	u.InitEconomyState()   // [P1-I04]
	w.units[idx] = u
	if err := w.attachCOB(u); err != nil {
		w.units[idx] = nil
		w.pool.Free(h)
		return 0, fmt.Errorf("units: strict COB binding for %q: %w", def.UnitName, err)
	} // [P1-I01] VM per-unit for forced unsliced
	if w.OnCreate != nil {
		w.OnCreate(h, u)
	}
	return h, nil
}

// NotifyCapture fires the capture hook exactly once after ownership transfer
// [08 "Evaluation"] slot 2. Caller must have already changed u.Owner and
// republished visibility.
func (w *World) NotifyCapture(h pool.Handle, oldOwner, newOwner uint8) {
	if w == nil || w.OnCapture == nil {
		return
	}
	if int(h) >= len(w.units) {
		return
	}
	u := w.units[int(h)]
	if u == nil || !u.Alive {
		return
	}
	w.OnCapture(h, oldOwner, newOwner, u)
}

// Destroy marks death; the slot stays alive and visible until post-tick
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// It fires OnDeath exactly once via an internal fired flag that FinalizeDeath
// also respects, so hook and free are deduplicated across the two paths
// [01 §4.4][04 "unit sweep"].
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
	// is the single fire point. Also deduped with FinalizeDeath.
	if !u.deathHookFired && w.OnDeath != nil {
		w.OnDeath(h, cause, u)
		u.deathHookFired = true
	}
	if !u.deathExtraHookFired && w.OnDeathExtra != nil {
		w.OnDeathExtra(h, cause, u)
		u.deathExtraHookFired = true
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
	u.Flags &^= ClassifierEligibleStatus
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
			u.Flags &^= ClassifierEligibleStatus
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
//
// Deprecated: Tick is a legacy package-wide sweep retained only for
// compatibility. It is non-authoritative. New code should use the explicit
// traversal API: VisitActiveSlots with StepPreUpdate at the front and
// FinalizeDeath at slot-end, running weapon/COB/orders/movement between
// those two boundaries [01 §4.4][04 "unit sweep"].
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Zero means free sentinel or not yet assigned. It looks up the per-definition ID via defMap.
func (w *World) DefIDForHandle(h pool.Handle) uint16 {
	if w == nil || h == 0 {
		return 0
	}
	idx := int(h)
	if idx < 0 || idx >= len(w.units) {
		return 0
	}
	u := w.units[idx]
	if u == nil || u.Def == nil {
		return 0
	}
	if w.defMap == nil {
		return 0
	}
	if id, ok := w.defMap[u.Def]; ok {
		return id
	}
	return 0
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
