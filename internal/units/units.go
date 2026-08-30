// Package units implements unit pools and lifecycle [04 §2] [PLAN_06 WU-06-1] [P0-16].
package units

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
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
// retail manager classifier. It is initialized by the common allocator
// initializer, rather than copied from UnitDef/FBI data, and is cleared by
// death finalization [R-P0-04 "Runtime eligibility bit lifecycle"].
const ClassifierEligibleStatus uint32 = 0x00000020

// BuildingClassStatus and ArmedStatus are the two remaining high bits of the
// runtime unit-status word. Both are written once by the common allocator
// initializer from the definition and by nothing else — a whole-image scan of
// the status word finds exactly one write site for each — so they are stable
// for the unit's lifetime.
//
// BuildingClassStatus is set when the definition's authored bmcode is zero,
// which is what makes a definition a building rather than a mobile unit; the
// same authored byte gates whether a YardMap is parsed at all. It is the
// factory production handler's building-class gate and the manager
// classifier's first high input [08 "Classifier eligibility, destinations,
// and order"; 05 "Factory production lifecycle"].
//
// ArmedStatus is set when the definition resolved at least one of its three
// weapon slots. It is the manager classifier's second high input, separating
// armed from unarmed buildings and selecting the regroup-A destination for
// ordinary armed ground units [08 "Classifier eligibility, destinations, and
// order"].
//
// Both were previously carried as opaque masks in internal/ai with no writer
// anywhere, so every branch that depended on them was dead.
const (
	BuildingClassStatus uint32 = 0x20000000
	ArmedStatus         uint32 = 0x80000000
)

const classifierSelectableClear uint32 = 0x00008000

// initialStatusFlags is the status word the allocator initializer produces for
// a freshly created unit [08 "Classifier eligibility, destinations, and
// order"].
func initialStatusFlags(def *content.UnitDef) uint32 {
	flags := ClassifierEligibleStatus
	if def == nil {
		return flags
	}
	// Building class is the authored bmcode being zero, not a yard-map,
	// footprint or immobility heuristic.
	if !def.BMCode {
		flags |= BuildingClassStatus
	}
	// Armed means at least one active weapon link; the record-0 inactive
	// sentinel a missed link resolves to is not a weapon [02 §5 R-CONTENT-02].
	if !content.IsWeaponInactive(def.Weapon1Def) || !content.IsWeaponInactive(def.Weapon2Def) || !content.IsWeaponInactive(def.Weapon3Def) {
		flags |= ArmedStatus
	}
	return flags
}

// BuildRenderPieceFlags builds the render-piece record [04 §"Piece flag polarity"].
// Allocation is zero-filled then per piece sets bit 1 (0x02 cache) and bit 2 (0x04 shade)
// unconditionally and bit 0 (0x01 draw) only when the piece's model object has at least
// three vertices. The compiled model exposes per-piece vertex counts via Model.Pieces[i].Vertices
// (internal/model — read-only use) [03 §2.4]; [04 §"Piece flag polarity"] and [R-COB-01 §1].
// Geometry pieces default drawn+cached+shaded (0x07), bare attachment points default 0x06.
func BuildRenderPieceFlags(mdl *model.Model) []uint8 {
	if mdl == nil {
		return nil
	}
	n := len(mdl.Pieces)
	if n == 0 {
		return nil
	}
	flags := make([]uint8, n) // zero-filled [04 §"Piece flag polarity"]
	for i, p := range mdl.Pieces {
		f := uint8(0x02 | 0x04) // bit1 cache and bit2 shade unconditionally [04 §"Piece flag polarity"]
		if len(p.Vertices) >= 3 {
			f |= 0x01 // bit0 draw when at least three vertices [04 §"Piece flag polarity"]
		}
		flags[i] = f
	}
	return flags
}

// BuildRenderPieceFlagsForProgram builds a prog-indexed flag view derived from the model.
// For scripted units the COB piece index is the authoring index; the fill is still defined
// per model object, so we map each prog piece name to its model piece and set bit0 based on
// that model object's vertex count. Bits 1 and 2 stay unconditional [04 §"Piece flag polarity"].
// When prog is nil the model-ordered flags are returned directly (scriptless case).
func BuildRenderPieceFlagsForProgram(mdl *model.Model, prog *cob.Program, pieceMap []int) []uint8 {
	if mdl == nil {
		return nil
	}
	if prog == nil {
		return BuildRenderPieceFlags(mdl)
	}
	if len(prog.Pieces) == 0 {
		return nil
	}
	flags := make([]uint8, len(prog.Pieces))
	for i := range prog.Pieces {
		f := uint8(0x02 | 0x04)
		modelIdx := -1
		if i < len(pieceMap) {
			modelIdx = pieceMap[i]
		} else {
			// Fallback: try name match if pieceMap absent
			for mi, mp := range mdl.Pieces {
				if mp.Name == prog.Pieces[i] {
					modelIdx = mi
					break
				}
			}
		}
		if modelIdx >= 0 && modelIdx < len(mdl.Pieces) && len(mdl.Pieces[modelIdx].Vertices) >= 3 {
			f |= 0x01
		} else if modelIdx == -1 && prog != nil {
			// If prog piece has no model counterpart, it would have been rejected by strict
			// binding diagnostics [cob/binding.go]; here we treat it as bare (no draw) rather
			// than inventing geometry.
		}
		flags[i] = f
	}
	return flags
}

// SetRenderPieceFlag toggles one bit of the unit's render-piece record [04 §"Piece flag polarity"].
// Mask is one of 0x01 (draw), 0x02 (cache), 0x04 (shade). Returns false if piece out of range.
func (u *Unit) SetRenderPieceFlag(piece int, mask uint8, set bool) bool {
	if u == nil || piece < 0 || piece >= len(u.RenderPieceFlags) {
		return false
	}
	if mask != 0x01 && mask != 0x02 && mask != 0x04 {
		return false
	}
	if set {
		u.RenderPieceFlags[piece] |= mask // lower opcode sets [04 §"Piece flag polarity"]
	} else {
		u.RenderPieceFlags[piece] &^= mask // higher opcode clears
	}
	return true
}

// InitRenderPieceFlags installs the render-piece table from the model [04 §"Piece flag polarity"] [R-COB-01 §1].
// It is the explicit scriptless branch's table builder and the scripted branch's model-driven
// fill before the VM is bound; the VM must not own this storage.
func (u *Unit) InitRenderPieceFlags(mdl *model.Model) {
	if u == nil {
		return
	}
	u.RenderPieceFlags = BuildRenderPieceFlags(mdl)
}

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
// Retail unit records have a 280-byte identity [P0-16] [01 §6.1]; Nanolathe
// uses named Go fields in a slot-indexed array parallel to pool.Units and
// does not reproduce packed bytes (I13).
type Unit struct {
	Handle    pool.Handle // slot index, 0 null [01 §6.1] [P0-16 §2.1]; slot number retained stale after free [P0-16 §3.4]
	Def       *content.UnitDef
	Owner     uint8 // 0..9 [04 §2]
	X, Y, Z   numeric.Fixed
	Health    int32 // current health; max from Def?
	MaxHealth int32
	Alive     bool // slot valid; cleared by the phase-2 finalizer [04 §2.4] C2
	// Dying is the death mark, separate from Alive [04 §2.3] C2: Destroy sets
	// it and the unit stays visible to later phases and Unit() until the next
	// phase-2 slot finalizer frees the slot.
	Dying               bool
	DeathCause          DeathCause
	deathHookFired      bool // internal: ensures OnDeath fires exactly once at FinalizeDeath [01 §4.4][04 "unit sweep"]
	deathExtraHookFired bool // internal composition observer deduplication
	// Build progress remaining 1→0 [04 §2.3] C3. float32 per the I2 allowlist
	// row "Construction remaining fraction" [05 "Construction target state"].
	// Owned exclusively by construction.Service; Units.Tick never mutates it [05 "Construction arithmetic"].
	Remaining float32
	Flags     uint32 // runtime status bits; bit 0x20 is allocator-initialized [R-P0-04]
	// The six engine-write port markers occupy the instance stance byte's
	// low six bits [R-P0-10]. They remain named fields so production code does
	// not confuse the classifier bit in Flags with COB state.
	InBuildStance bool         // engine-write port 5 [R-P0-10]
	Busy          bool         // engine-write port 6 [R-P0-10]
	YardOpen      bool         // engine-write port 18 [R-P0-10]
	BuggerOff     bool         // engine-write port 19 [R-P0-10]
	Armored       bool         // engine-write port 20 [R-P0-10]
	Group         uint8        // one stored control-group value 0..9 [07 §9]
	Pending       uint32       // capability/pending word for gate intersection [04 §3.3] C6
	Orders        any          // [04 §3.2] front/rear segment anchors on the unit (stored as *orders.Queue via opaque to avoid import cycle)
	Script        *cob.VM      // typed COB VM per-unit [04 §4.2][P1-I01] — not any, typed per acceptance
	GuardLatches  GuardLatches // per-unit dedup array for guard assistance [04 §3.5]
	// RenderPieceFlags is the per-unit render-piece record [04 §"Piece flag polarity"] [R-COB-01 §1].
	// One flags byte per piece in a separate array from the script's piece-animation state.
	// Allocation is zero-filled then the fill pass sets bit 1 (0x02 cache) and bit 2 (0x04 shade)
	// unconditionally and bit 0 (0x01 draw) only when the piece's model object has at least three
	// vertices. A null-program (scriptless) unit still builds this table; it must NOT live inside
	// the VM, which is only allocated for scripted units.
	RenderPieceFlags []uint8

	// Typed per-unit state introduced for P0-I02 real pipeline [04 §1.1][04 §4][06][GAP T15].
	// These fields own the authoritative per-unit data that the phase-2 sweep
	// visits in players-asc then slots-asc order [01 §6.2] C2 [P0-16].
	ScriptState   *ScriptState    // per-unit COB VM/thread/piece state [04 §4.1][04 §4.2][GAP T15]; nil if not yet wired
	Slots         [NumSlots]Slot  // three weapon slots [06 §1.2] C1 P0-10; local Slot avoids units→combat→economy→units cycle
	Move          MoveState       // movement status shared with movement.System [04 §8.1][04 §9.1] (movement imports units)
	Attachment    AttachmentState // carrier/cargo linkage [04 §4.4] attach-unit
	CallbackQueue CallbackQueue   // engine→COB callback queues/readiness [GAP T15]
	// SpotMetal is the extractor yield sampled once at placement: Σ(cellMetal+1)*extractsMetal [05 "Terrain metal extraction"] C14 [P1-10][P1-15].
	// Stored on the instance once at creation via SampleMetal, never resampled even if terrain metal changes.
	SpotMetal float32 // [P1-10] once Σ(byte+1)*extractsMetal, [P1-15] uniform char write
	// OrderGuard is the per-unit order-guard float of the shared eligibility
	// predicate [07 §8/§9]: zero at unit creation and at order completion,
	// nonzero (a clamped 0..1 ratio) while an order is being processed.
	// Eligibility compares it exactly equal to 0.0, so the guard means "not
	// mid-order". float32 per the I2 allowlist row "Per-unit order-guard
	// float". Written by the orders pump; the exact ratio source is
	// unattested — only the 0/nonzero distinction is established TODO(question).
	OrderGuard float32
	// TODO(question): direct World.Create bypasses extractor sampling; session reconstructUnits and
	// construction.allocateNanoframe sample via Terrain.SampleMetal once at placement [P1-10][P1-15],
	// but save-restore forced-slot and any other direct Create caller must also sample via the same hook
	// or via Terrain.ApplySchema post-load; verify universal coverage.
	// Economy state bound to the one ledger per [05] — activation/on-off, cloak, storage, extraction, wind/tidal, makers [P1-I04].
	Activated bool  // operational/activated bit for on/offable units [05 "Unit instance economy state"] [P1-I04]; true when the unit is turned on; for non-OnOffable units always true when complete
	IsCloaked bool  // whether cloak upkeep is due this pass [05 "Cloak debit"] [P1-I04]
	Kills     int32 // kill count for capture timer [P0-15]
	// Paralyze state per [06 §10] paralyzer status effects [P0-I04].
	ParalyzeExpire uint32 // absolute tick when stun ends; 0 means not paralyzed [06 §10]
	Stunned        bool   // TODO(question): GAI slow vs binary stun remains open [06 §10]
	PriorSample    uint8  // previous 30-tick-window health sample for death severity [04 §5.1]
	// Placement linkage for P0-04/P0-06 sparse created[] semantics [P0-04][P0-06].
	// Retail maintains created[placementIdx] sparse array and scans it in
	// placement order 0..count-1 skipping NULL gaps for Ident→Unitname first-
	// occurrence resolution (A27). Store provenance to reconstruct that scan
	// without relying on dense w.Iter() prefix.
	PlacementIdx      int // index in Mission.Units placement order, -1 if not scenario-spawned
	PlacementIdent    string
	PlacementUnitName string
}

// MakeSelectable applies retail's make-selectable order: clear the transient status bit
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

// Eligible implements the shared eligibility predicate [07 §8/§9]: the
// classifier/selection status bit 0x20 is set, and the per-unit order-guard
// float compares exactly equal to 0.0 — the unit is not mid-order. The
// predicate's remaining retail clauses (no disqualifying state reference; a
// parent unit whose status word carries bit 0x40000000) have no nanolathe
// counterpart yet TODO(question): map parent status bits when the
// transport/carrier flag set is closed.
func (u *Unit) Eligible() bool {
	if u == nil || !u.Alive || u.Dying {
		return false
	}
	return u.Flags&ClassifierEligibleStatus != 0 && u.OrderGuard == 0.0
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

// DeathHook is invoked exactly once per unit when the phase-2 slot finalizer
// retires a Dying unit [01 §4.4][04 §2.4]. The session uses it to feed mission
// trigger death notifications exactly once [08 "Evaluation"].
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
// then slots ascending [01 §6.2] C2 [P0-16]. The pool is the retail sliced
// shape via NewSliced: physical cap = maxDefs*10+1, sliced per-player maxDefs
// each [P0-16 §3.1]; the canonical allocator is the sole allocation site,
// scanning the owning player's slice for the lowest free slot with immediate
// reuse [P0-16 §3.2] [01 §6.1]; per-def limits are enforced per slice
// [P0-16 §3.2]; forcedSlot reconstruction verifies slice bounds and
// occupancy [P0-16 §3.3].
type World struct {
	units   []*Unit
	pool    *pool.Units
	catalog *content.Catalog

	// OnDeath is the death-notification hook [08 "Evaluation"]; nil means no
	// consumer. It fires exactly once per unit at slot-end finalization.
	OnDeath DeathHook
	// OnDeathExtra is a narrow composition observer that survives replacement
	// of the primary session hook. It fires at the same finalizer boundary,
	// independently deduplicated, and must not emit duplicate notifications.
	OnDeathExtra DeathHook
	// OnCreate is the creation-notification hook [08 "Evaluation"] slot 3;
	// nil means no consumer. It fires exactly once per unit after Create inserts.
	OnCreate CreateHook
	// OnCapture is the capture-transfer hook [08 "Evaluation"] slot 2; nil means
	// no consumer. It fires exactly once per ownership transfer.
	OnCapture CaptureHook

	defMap    map[*content.UnitDef]uint16 // def -> occupancy identity for per-def scan [P0-16][CNT-05]
	nextDefID uint16
	// claimedDefIDs mirrors every identity handed out, for the fixture
	// counter's collision check without a map range (I1).
	claimedDefIDs []uint16
	// per-player live counters mirror the retail live count, which decrements on free [P0-16 §3.4]
	liveCounters [10]int

	// COB loader for per-unit VM creation [04 §4.1][P1-I01].
	cobFS     vfs.FSOps
	cobLoader *cob.CachedLoader
	// cobBinder supersedes the definition/loader program path when installed
	// by session composition. It is also used for units created by construction
	// and save reconstruction, so every production unit follows one path.
	cobBinder COBBinder

	// simulationRNG is the session-owned, battle-wide Park-Miller stream. It is
	// bound by session composition before the first production allocation; nil
	// is retained only for small unit-package fixtures, which receive the
	// deterministic zero-sample heading and consume no draws [R-P28-ANG-01R §2].
	simulationRNG *rng.Simulation
}

// NewSliced creates the unit world over a retail sliced pool for maxDefs
// catalog definitions: physical cap = maxDefs*10+1 records, sliced per-player
// maxDefs each [P0-16 §3.1]. Stock ~2000-5001 [P0-16]. Slot 0 null,
// lowest-free allocation with immediate reuse, no generation tags [01 §6.1].
// This is the only production world constructor; tests build the same slices
// at smaller fixture sizes by passing a small maxDefs.
func NewSliced(maxDefs int, cat *content.Catalog) *World {
	p := pool.NewUnitsSliced(maxDefs)
	return newSlicedWorld(p, cat)
}

// NewSlicedWithOrder creates a production unit world with the battle-entry
// player permutation already computed by session setup. Invalid permutations
// are rejected before any pool state is allocated [R-P0-16-A].
func NewSlicedWithOrder(maxDefs int, cat *content.Catalog, order pool.PlayerPermutation) (*World, error) {
	p, err := pool.NewUnitsSlicedWithOrder(maxDefs, order)
	if err != nil {
		return nil, err
	}
	return newSlicedWorld(p, cat), nil
}

func newSlicedWorld(p *pool.Units, cat *content.Catalog) *World {
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

// SetSimulationRNG binds the one authoritative simulation stream used by the
// common unit initializer [01 §7.1][R-P28-ANG-01R §2]. A World never creates
// a substitute or per-unit stream.
func (w *World) SetSimulationRNG(sim *rng.Simulation) {
	if w != nil {
		w.simulationRNG = sim
	}
}

// initializeAllocationHeading performs the two common-initializer RNG
// invocations in retail order: the buildangle-bounded heading invocation,
// followed by the separate full-domain initialization draw whose semantic
// destination remains unresolved [R-P28-ANG-01R §2].
func (w *World) initializeAllocationHeading(u *Unit, def *content.UnitDef) {
	if u == nil || def == nil {
		return
	}
	var draw uint32
	if w != nil && w.simulationRNG != nil {
		draw = w.simulationRNG.Uint32n(uint32(uint16(def.BuildAngle)))
	}
	u.Move.Heading = uint16(int32(int16(uint16(draw))) - int32(uint16(def.BuildAngle)>>1) + 32768)
	if w != nil && w.simulationRNG != nil {
		// Only this full-domain invocation and its call position are established;
		// no semantic destination is assigned [R-P28-ANG-01R §2].
		_ = w.simulationRNG.Uint32n(0x10000)
	}
}

// attachCOB binds the definition's program to a per-unit VM and runs Create
// once in mode I [04 §4.1][P1-I01].
//
// Missing/empty COB policy [04 R-COB-04 §8] (supersedes the UNIT-04 reading
// of [R-COB-01 §1]): retail faults while creating a unit whose program is
// null — the creation-time QueryPrimary query dereferences the VM reference
// with no null test — so such a definition cannot exist in retail. Nanolathe
// rejects it at catalog compile with the standard diagnostic (content's
// definition load). The program-less branch below therefore serves only
// fixture definitions that content never compiled: no VM, no Create, the
// unit stays live for the test. The definition-stored program is preferred;
// the loader lookup remains for those fixtures.
func (w *World) attachCOB(u *Unit) error {
	if w == nil || u == nil || u.Def == nil {
		return nil
	}
	if u.GetScript() != nil {
		return nil // already has VM [P1-I01]
	}
	if w.cobBinder != nil {
		// TODO(question): two attachment failure boundaries remain open. Nanolathe's
		// strict binder can return an error after allocation, but retail establishes
		// RNG order only for successful initialization and pre-initializer refusal; a
		// traced retail post-allocation failure would settle whether either draw is
		// retained. Do not roll back or reorder the successful path.
		return w.cobBinder(u)
	}
	prog := u.Def.Script
	if prog == nil && w.cobLoader != nil && w.cobFS != nil {
		// Fixture path: resolve via the loader (cached, case-insensitive) [04 §4.1].
		if p, ok, _ := w.cobLoader.Load(w.cobFS, u.Def.UnitName); ok && p != nil {
			prog = p
		} else if p, ok, _ := w.cobLoader.Load(w.cobFS, u.Def.CanonicalKey); ok && p != nil {
			prog = p
		}
	}
	if prog == nil || len(prog.Code) == 0 {
		// Null program: fixture-only scriptless creation — no VM, no Create
		// (a compiled catalog never carries one, [04 R-COB-04 §8]). The unit
		// stays live; its pieces never animate. The render-piece table is still
		// built when a model is available via the strict binder; for this
		// fixture path without a model we leave the table nil — the production
		// strict binder (session) builds it from the authored 3DO [04 §"Piece flag polarity"].
		return nil
	}
	vm := cob.NewVM(prog)
	bindUnitPortHandlers(vm, u)
	// Build the render-piece record for the fixture path [04 §"Piece flag polarity"].
	// Without an authored model the geometry test cannot be applied, so we share
	// the VM's default 0x07 table as the unit's table and delegate writes there
	// via the bridge. Production scripted units with a model get their geometry-
	// driven table from the strict binder; this fallback keeps fixture tests green.
	if u.RenderPieceFlags == nil {
		// SnapshotFlags returns the VM-local 0x07 defaults [04 §4.3].
		if flags := vm.SnapshotFlags(); flags != nil {
			u.RenderPieceFlags = flags
			// Delegate VM flag writes to the unit record [04 §"Piece flag polarity"].
			vm.BindRenderFlagHandlers(func() []uint8 { return u.RenderPieceFlags }, func(piece int, mask uint8, set bool) bool {
				return u.SetRenderPieceFlag(piece, mask, set)
			})
		}
	}
	u.SetScript(vm)
	// Run Create immediately with wake flag (delta 0 barrier) so hide/show etc. are visible before first snapshot [04 §4.1][GAP T15].
	if _, ok := prog.Scripts["Create"]; ok {
		_ = vm.StartByName("Create", nil)
		vm.Drain(0) // immediate wake-flag drain [GAP T15] C17
	}
	return nil
}

// IsSliced reports whether the world's pool uses per-player slices; true for
// every production world [P0-16 §3.1].
func (w *World) IsSliced() bool {
	if w == nil || w.pool == nil {
		return false
	}
	return w.pool.IsSliced()
}

// MaxDefs returns the catalog maxDefs used for slicing [P0-16 §3.1].
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

// SlotIndex returns the slot number stamped at init and retained stale after
// free for the handle [P0-16 §3.4].
func (w *World) SlotIndex(h pool.Handle) uint16 {
	if w == nil || w.pool == nil {
		return 0
	}
	return w.pool.SlotIndex(h)
}

// defIDForDef resolves the definition's pool occupancy identity [CNT-05]
// [P0-16 §3.2] and reports whether creation may proceed. The identity is the
// definition's stable 1-based catalog index (UnitDefID, where 0 stays the
// free sentinel), stored directly from the immutable catalog — never handed
// out in first-use order. Production worlds always carry the finalized
// catalog, and a definition that is not the catalog's own record is rejected
// with an error and no allocation.
//
// TODO(question): the research establishes that the pool slices and the
// per-def limit gate both derive from the definition catalog's size/position
// [P0-16 §3.2][01 §6.1], but not the exact encoding retail stores in the
// unit record's definition-identity field (a 0-based catalog ordinal, this
// 1-based index, or another form). Nanolathe stores the 1-based catalog
// index, which lands the catalog position in the pool's "0 = free" uint16
// identity space. Re-derive by tracing the per-def limit scan's comparison
// operand and the allocator's identity write in the retail pool.
//
// Fixture worlds built with a nil catalog have no catalog position to store;
// a definition carrying a stamped UnitDefID stores it, and a synthetic
// definition falls back to a first-use counter scoped to that fixture world.
// The fallback is test scaffolding, not retail behavior. Zero RNG draws.
func (w *World) defIDForDef(def *content.UnitDef) (uint16, error) {
	if def == nil {
		return 0, fmt.Errorf("units: nil def")
	}
	if w.defMap == nil {
		w.defMap = make(map[*content.UnitDef]uint16)
		w.nextDefID = 1
	}
	if id, ok := w.defMap[def]; ok {
		return id, nil
	}
	if w.catalog != nil && w.catalog.Finalized() {
		// Finalized-catalog gate [CNT-05][P0-16 §3.2]: the world's catalog is
		// the immutable compiled catalog (session's pool constructor requires
		// one), so a definition that is not the catalog's own record has no
		// catalog position to store and is rejected with no allocation.
		// Catalogs that were never finalized (hand-built fixtures, Hash
		// unstamped) carry no finalized identity to check against.
		idx, member := w.catalog.UnitIndexOf(def)
		if !member {
			return 0, fmt.Errorf("units: definition %q is not from the world's finalized catalog", def.UnitName)
		}
		if id, ok := w.catalogID(idx); ok {
			w.defMap[def] = id
			w.claimedDefIDs = append(w.claimedDefIDs, id)
			return id, nil
		}
	} else if id, ok := w.catalogID(def.UnitDefID); ok {
		w.defMap[def] = id
		w.claimedDefIDs = append(w.claimedDefIDs, id)
		return id, nil
	}
	// Fixture fallback: first-use counter for a definition with no catalog
	// position. Skip identities already claimed by stamped definitions so
	// per-def counting never conflates two definitions. Claimed identities
	// live in a slice scanned linearly: Create runs inside the simulation,
	// and I1 bans map iteration on sim-visible paths.
	for w.nextDefID <= 0xFFFF {
		id := w.nextDefID
		if id == 0 {
			id = 1
		}
		w.nextDefID = id + 1
		if !w.defIDClaimed(id) {
			w.defMap[def] = id
			w.claimedDefIDs = append(w.claimedDefIDs, id)
			return id, nil
		}
	}
	return 0, fmt.Errorf("units: fixture definition identities exhausted")
}

// catalogID narrows a catalog index into the pool's uint16 identity space,
// keeping 0 reserved as the free sentinel [P0-16 §3.1]. The compiled catalog
// caps definitions at 511 ([R-P0-03] 512-bit category domain), so the
// narrowing is unreachable for compiled content; a synthetic definition with
// an out-of-range stamp is treated as unstamped.
func (w *World) catalogID(idx uint32) (uint16, bool) {
	if idx == 0 || idx > 0xFFFF {
		return 0, false
	}
	return uint16(idx), true
}

// defIDClaimed reports whether any definition already holds the identity.
// Scanned over the claimed-identity slice, not the defMap (I1).
func (w *World) defIDClaimed(id uint16) bool {
	for _, used := range w.claimedDefIDs {
		if used == id {
			return true
		}
	}
	return false
}

// Create allocates through the canonical per-player allocator: lowest-free
// slot in the owning player's slice with slot 0 null, no generation tags,
// immediate reuse [01 §6.1] C1 [P0-16 §3.2]. A slice-full failure is
// reported even when other players have free slots [P0-16 §7.3]. Failures
// before common initialization consume zero RNG draws [R-P28-ANG-01R §2].
func (w *World) Create(def *content.UnitDef, owner uint8, x, y, z numeric.Fixed) (pool.Handle, error) {
	if w == nil || w.pool == nil {
		return 0, fmt.Errorf("units: nil world")
	}
	if def == nil {
		return 0, fmt.Errorf("units: nil def")
	}
	player := int(owner)
	if player < 0 || player >= 10 {
		return 0, fmt.Errorf("units: player %d out of range", player)
	}
	defID, err := w.defIDForDef(def)
	if err != nil {
		// CNT-05: a definition that is not the finalized catalog's own record
		// is rejected with no allocation [P0-16 §3.2].
		return 0, err
	}
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
		Flags:        initialStatusFlags(def),
		Remaining:    0,
		MaxHealth:    int32(def.MaxDamage),
		Health:       int32(def.MaxDamage),
		PlacementIdx: -1,
	}
	installWeapons(u, def) // [06 §1.2] wire Weapon1/2/3 definitions into Slots [P0-I04]
	u.InitEconomyState()   // [P1-I04] on/off, cloak, activation from definition
	w.initializeAllocationHeading(u, def)
	w.units[idx] = u
	if err := w.attachCOB(u); err != nil {
		w.units[idx] = nil
		w.pool.Free(h)
		return 0, fmt.Errorf("units: strict COB binding for %q: %w", def.UnitName, err)
	} // per-unit VM with statics/pieces, Create run [04 §4.1][P1-I01]
	w.liveCounters[player]++
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
	// Only active links populate a slot [06 §1.2] P0-10: the record-0
	// inactive sentinel a missed link resolves to is not a weapon [02 §5
	// R-CONTENT-02], so it must not arm the slot.
	if !content.IsWeaponInactive(def.Weapon1Def) {
		u.Slots[0].Weapon = def.Weapon1Def
		u.Slots[0].Flags |= 0x02 // armed/hasTarget when populated [06 §1.2] P0-10
	}
	if !content.IsWeaponInactive(def.Weapon2Def) {
		u.Slots[1].Weapon = def.Weapon2Def
		u.Slots[1].Flags |= 0x02
	}
	if !content.IsWeaponInactive(def.Weapon3Def) {
		u.Slots[2].Weapon = def.Weapon3Def
		u.Slots[2].Flags |= 0x02
	}
}

// CreateWithForcedSlot allocates a unit at the exact forcedSlot for save
// reconstruction: the candidate is verified against the owning player's
// slice bounds and free occupancy, and the per-def limit is re-checked
// [P0-16 §3.3]. A successful reconstruction allocation runs the normal draw
// sequence before its caller restores the saved heading; validation failures
// consume zero draws [R-P28-ANG-01R §2]. Returns error on
// limit/slice-full/forced-OOB/occupied.
// Active in-battle restore is explicitly unsupported, so this allocator does
// not add a save codec or reconstruct a saved heading [INVARIANTS I13].
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
	if player < 0 || player >= 10 {
		return 0, fmt.Errorf("units: player %d out of range", player)
	}
	defID, err := w.defIDForDef(def)
	if err != nil {
		// CNT-05: same finalized-catalog gate as the canonical allocator
		// [P0-16 §3.2]; save reconstruction must not seat a foreign definition.
		return 0, err
	}
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
		Flags:        initialStatusFlags(def),
		Remaining:    0,
		MaxHealth:    int32(def.MaxDamage),
		Health:       int32(def.MaxDamage),
		PlacementIdx: -1,
	}
	installWeapons(u, def) // [06 §1.2] wire Weapon1/2/3 definitions [P0-I04]
	u.InitEconomyState()   // [P1-I04]
	w.initializeAllocationHeading(u, def)
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

// Destroy marks death; the slot stays alive and visible until the next phase-2
// slot finalizer [04 §2.3][04 §2.4] C2. Death callbacks are deferred to that
// finalizer so later phases can observe the marked unit without running
// destruction side effects [01 §4.4][04 "unit sweep"].
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

// FreeImmediate performs the retail finalization free immediately within the
// same tick: it clears the occupancy identity and alive mask, releases the
// per-unit heaps, order queues and attachments, and decrements the per-player
// live counter, while retaining the slot number stale [P0-16 §3.4]. The slot
// becomes lowest-free reusable the same tick if the slice is still ahead in
// the 0..9 ascending scan [P0-16 §6.3]. Zero RNG draws.
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

// ApplyDamage implements the damage packet 0x0B handler's stale validation:
// a 16-bit slot target validates only slot nonzero and alive, then subtracts
// health; if the slot was freed and
// reused the damage aliases the new occupant silently [P0-16 §6][06 "Damage
// identity"]. No generation tag anywhere (bounded 3901) [P0-16 §2.2].
// Returns false if validation fails (slot 0 or dead/free). Zero RNG draws.
//
// UNIT-05 signed overkill: the subtraction result is stored SIGNED — no
// clamp at zero. The local Killed severity contract consumes the signed
// health, severity = ((−health·100)/maxHealth + priorSample)/2 [04 §5.1], so
// an overkill intermediate must survive until severity and the death
// callbacks are finished. Death marking still triggers on a non-positive
// result at the caller's Destroy (combat damage application, the reclaim
// pulse, or the slot-end death latch), and the signed value is retained
// through FinalizeDeath and TeardownCleanup. Zero RNG draws.
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
	return true
}

// TeardownCleanup frees death-marked slots during explicit world teardown.
// Gameplay phase-2 visitation is the only in-battle finalizer; this method is
// for non-running-world cleanup and test fixture disposal [01 §4.4][04 §2.4].
func (w *World) TeardownCleanup() {
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

// LiveCountForPlayer returns the per-player live counter [P0-16 §3.4].
func (w *World) LiveCountForPlayer(player int) int {
	if w == nil || player < 0 || player >= 10 {
		return 0
	}
	return w.liveCounters[player]
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

// DefIDForHandle returns the retail occupancy identity for the unit occupying handle [P0-16] (I13).
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
// slots asc within each slice [P0-16 §3.1].
func (w *World) IterSliced() []*Unit {
	if w == nil {
		return nil
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
