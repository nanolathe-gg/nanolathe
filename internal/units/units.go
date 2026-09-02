// Package units implements unit pools and lifecycle [04 §2] [PLAN_06 WU-06-1] [P0-16].
package units

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
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

// Retail damage-kind bytes this package has to name. The full sixteen-value
// enumeration and its producers live in internal/combat, which imports this
// package and so cannot be imported back; these three are repeated here
// because the save boundary and the coarse-label derivation below both need
// them. Kind 3 is the self-destruct countdown and kind 5 the reclaim /
// build-complete pulse [06 §12.1]; kind 0 is the scenario/load removal that
// carries no packet at all.
const (
	damageKindNone         uint8 = 0
	damageKindSelfDestruct uint8 = 3
	damageKindReclaim      uint8 = 5
)

// DeathCauseFromKind derives the coarse label from retail's damage-kind byte.
//
// Retail keeps ONE value: the cause code recorded by the last damage packet,
// which is what the death packet's high nibble carries [06 §12.1] and what the
// save record's death-cause byte holds [08 R-SAVE-02 §6]. DeathCause is this
// build's label for the same event, not a second reading of it, so anything
// that recovers a kind byte — save restore above all — derives the label here
// rather than casting the byte into an enum whose members do not share its
// numbering. Kind 2 is retail's paralyze packet, for instance, while
// DeathCause(2) is DeathReclaimed; the raw cast made those the same value.
//
// The three labelled kinds map to their members and every remaining kind that
// can be a death cause maps to DeathKilled, which is the label the finalizer
// treats as "a packet killed it" — the recorded kind byte, not this label, is
// what selects the credit path and the death explosion [06 §12.1].
func DeathCauseFromKind(kind uint8) DeathCause {
	switch kind {
	case damageKindNone:
		return DeathUnknown
	case damageKindSelfDestruct:
		return DeathSelfDestruct
	case damageKindReclaim:
		return DeathReclaimed
	default:
		return DeathKilled
	}
}

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

// OverlapHostStatus and OverlapIntruderStatus are the occupancy overlap
// protocol's two bookkeeping bits, at bits 26 and 27 of this same runtime
// status word — the word whose bit 29 is BuildingClassStatus above
// [04 R-COLL-01 §4]. *Host* means another unit overlaps a cell this unit
// holds; *intruder* means this unit overlaps a cell it does not hold.
//
// Their reader census is exactly three routines, all inside the occupancy
// layer: the clear (bit 27 cleared; bit 26 set → both cleared and the overlap
// scan run), the restamp (gated on bit 27), and the two writers that raise
// bit 27 to request a restamp — the yard-open port write and the save
// loader's post-load pass. No aim, damage, order, path, visibility or
// presentation code reads either bit [04 R-COLL-01 §4 "reader census of the
// overlap bits"], so they are declared here only because the flag word lives
// here; internal/movement owns every read and write.
const (
	OverlapHostStatus     uint32 = 0x04000000
	OverlapIntruderStatus uint32 = 0x08000000
)

const classifierSelectableClear uint32 = 0x00008000

// CloakRequestedStatus is bit 11 of the runtime unit-status word: the
// cloak-REQUESTED bit, which the constructor seeds from `init_cloaked` in the
// same masked store that copies the two standing-order fields below, and which
// the `Cloak_On` / `Cloak_Off` handlers set and clear behind the definition's
// derived `cloakcost > 0` capability [05 R-ECO-01 §9][03 R-VIS-01 §6]
// [04 R-ORD-01 §2].
//
// Per I13 the logical field is Unit.IsCloaked, which is the authority at
// runtime; this mask exists because the save projection persists the request
// inside the status word, and the save writer projects the bool back into it.
const CloakRequestedStatus uint32 = 0x00000800

// The two standing-order fields live in the status word as two-bit pairs: the
// move stance at bits 18-19, the fire stance at bits 20-21 [04 R-STANCE-01 §2].
// They are seeded here at creation from one packed definition byte the FBI
// reader fills from `standingmoveorder` and `standingfireorder`, each with a
// parsed default of 2 [04 R-STANCE-01 §6].
const (
	StandingMoveShift = 18
	StandingFireShift = 20
	StandingFieldMask = uint32(3)
)

// The build-page field lives in the same status word: the page-shown indicator
// at bit 22 and the page number at bits 23-25 [07 §9]. Unit creation seeds
// page 1 with the indicator set when the definition's page-count byte is at
// least 2, and clears both otherwise; **no click writes the field**
// [07 R-HUD-04 §4 "First build page"][07 R-HUD-03 §6]. internal/hud owns the
// same two masks for the presentation side and cannot be imported here.
const (
	buildPagedStatus     = uint32(1) << 22
	buildPageNumberShift = 23
	buildPageMultiPage   = 2
)

// CargoSelectableStatus is bit 30 of the runtime status word, a static
// mirror of the definition's `isairbase` flag: written once by the unit
// initializer and never touched again [04 R-UNIT-06 §3]. It is not itself a
// selectability bit on the unit that carries it — it is what the
// selection-eligibility predicate's carrier clause reads off a *carrier's*
// status word: a unit with a carrier is eligible only when the carrier has
// this bit set, so cargo aboard an ordinary transport is not selectable
// while cargo attached to an airbase (a landed pad guest, or a factory
// product whose factory definition is itself an airbase) is. internal/units
// owns the write; internal/triggers spells the carrier clause out against
// the carrier's Flags word.
const CargoSelectableStatus uint32 = 0x40000000

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
	// The cargo-selectable mirror is set exactly when the definition is an
	// airbase, and only here — no later write ever touches it
	// [04 R-UNIT-06 §3]. A unit's own copy of this bit is meaningless to its
	// own selectability; it matters only when this unit is read as someone
	// else's carrier.
	if def.IsAirBase {
		flags |= CargoSelectableStatus
	}
	// The two standing-order fields, seeded from the definition's packed
	// standing byte [04 R-STANCE-01 §6]. Both parse with default 2, so a
	// definition authoring neither key starts its units at fire at will and
	// roam; the stock content census authors move 1 (maneuver) and fire 2 on
	// nearly every mobile definition, so a stock unit starts maneuver + fire at
	// will and engages on its own.
	//
	// Nothing seeded these before, so every unit in every battle was created at
	// move 0 (hold position) and fire 0 (hold fire): the auto-engage issuer of
	// [04 R-STANCE-01 §3] refuses on either zero, so no unit ever acquired a
	// target on its own, and the side panel's MOVEORD/FIREORD buttons both
	// staged at their hold value with nothing able to change them.
	flags |= (uint32(def.StandingMoveOrder) & StandingFieldMask) << StandingMoveShift
	flags |= (uint32(def.StandingFireOrder) & StandingFieldMask) << StandingFireShift
	// The first build page. A definition with two or more authored pages starts
	// its units on page 1 with the page-shown indicator set; one page or none
	// clears both. This is the field's only writer — the BUILD gadget's click
	// does not write it, so the page a builder is on is the one creation seeded
	// or a later page move wrote [07 R-HUD-04 §4 "First build page"].
	if def.BuildPageCount >= buildPageMultiPage {
		flags |= buildPagedStatus | uint32(1)<<buildPageNumberShift
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
// When prog is nil the model-ordered flags are returned directly, for the
// pre-attachment fixture stage.
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
// It is the model-driven fill before the VM is bound; the VM must not own this
// storage. Production allocation still requires a loadable COB before a unit
// can be published [R-COB-04 §8].
func (u *Unit) InitRenderPieceFlags(mdl *model.Model) {
	if u == nil {
		return
	}
	u.RenderPieceFlags = BuildRenderPieceFlags(mdl)
}

// YardOpenTransaction owns an installed port-18 request, including admission,
// the YardOpen commit, and any world restamp. The callback is runtime
// composition topology, not authoritative unit state or save data
// [04 §4.7 port 18][04 R-COLL-01 §4].
type YardOpenTransaction func(requested bool)

// The order-event word's bits the weapon layer owns [06 R-WPN-05 §6]. The word
// is the one Unit.Pending holds; §4.2's "fired this tick" status word and
// §3.3's "could not fire" status bit are the same word.
//
//	0x400   a successful non-`commandfire` shot
//	0x800   a successful `commandfire` shot
//	0x1000  could not fire
//	0x2000, 0x4000  the damage-reaction site's feedback bits [06 R-WPN-04 §2]
//	0x8000  the under-construction wait [04 R-ORD-01 §0]
const (
	// PendingCouldNotFire is raised by the slot pipeline when the shot-time
	// physical gate of [06 §3.3] fails (reload was already zero, so a shot was
	// attempted) and by the turret executor when its aim geometry yields no
	// solution — the latter also clearing the Aim latch [06 R-WPN-05 §6].
	//
	// It is raised at most once per slot visit and latched until an order
	// consumes it. Three sites clear it and nothing else: the order pump, once
	// per record it visits, for the bits that record's gate names; the slot
	// target setters, which clear bits 10-14 (PendingSlotSetterClear); and
	// unit construction, which zeroes the whole word.
	//
	// The weapon layer never READS it, so its presence changes no firing
	// decision. It is the attack handlers' disengage signal — "my weapon tried
	// and could not" — and `Attack_NoMove` phase 2 is reached only when it
	// arrives.
	PendingCouldNotFire uint32 = 0x1000

	// PendingSlotSetterClear is bits 10-14 of the order-event word, which the
	// two slot target setters — bind slot to unit, bind slot to point — clear
	// when they install a target [04 R-ORD-01 §7] [06 R-WPN-05 §6]. Binding a
	// new target therefore discards a stale "could not fire".
	PendingSlotSetterClear uint32 = 0x7C00
)

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
	// LastDamageSide/Cause retain the provenance used by repair-patrol
	// admission. Cause 5 is the unit-reclaim bite [04 R-ORD-02 §4].
	// LastDamageSide is the attacker-side SNAPSHOT the damage intake stores
	// beside the attacker pointer, and unit spawn seeds it to the neutral side
	// [06 R-WPN-04 §2]. See NeutralAttackerSide.
	LastDamageSide  uint8
	LastDamageCause uint8
	Alive           bool // slot valid; cleared by the phase-2 finalizer [04 §2.4] C2
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
	InBuildStance   bool  // engine-write port 5 [R-P0-10]
	Busy            bool  // engine-write port 6 [R-P0-10]
	YardOpen        bool  // engine-write port 18 [R-P0-10]
	BuggerOff       bool  // engine-write port 19 [R-P0-10]
	Armored         bool  // engine-write port 20 [R-P0-10]
	BuildingState   bool  // persisted state-byte bit 3 [08 R-SAVE-02 §6]
	Group           uint8 // one stored control-group value 0..9 [07 §9]
	RestoredAIGroup int32 // saved owner-group index, -1 means none [08 R-SAVE-02 §6]
	HasMover        bool  // has-mover flag as stored in the unit save image [08 R-SAVE-02 §6]
	// RestoredMoveMode marks the packed unit-side mover mirror as authoritative
	// during the restore bootstrap. EnsureUnit normally initializes this mirror
	// for newly created units, but must preserve the saved value while the
	// mover-side record is applied [08 R-SAVE-02 §6].
	RestoredMoveMode bool
	// Pending is the unit's ORDER-EVENT WORD: the 16-bit word the order pump
	// merges with each record's own pending word before intersecting the
	// record's gate [04 §3.3] C6 [04 R-ORD-01 §0]. Its producers are the
	// damage-reaction site, the weapon layer and the under-construction wait;
	// see PendingCouldNotFire below. Construction leaves it zero.
	//
	// Retail loads it as a ZERO-EXTENDED 16-bit value when merging
	// [06 R-WPN-05 §6], so gate bit 0x10000 can be satisfied only from a
	// record's own pending word — which answers [04 R-ORD-01 §0]'s standing
	// "what writes bit 16 into the unit capability word": nothing can.
	Pending uint32
	// Save-restored unit words whose consumers are owned by later phases. The
	// names stay neutral where the retail census remains Unknown [08
	// R-SAVE-02 §6].
	RelationDomainByte uint8
	CachedOccupancyX   int16
	CachedOccupancyZ   int16
	SightCellX         int16
	SightCellZ         int16
	FootprintSizeX     int16
	FootprintSizeZ     int16
	// RevealDeadline is the ONE shared reveal/cloak-suppression deadline tick.
	// Retail has a single field here and every producer writes it outright — a
	// later write always wins and no maximum is taken [03 R-VIS-01 §6]:
	//
	//   - the sensor phase's minimum-cloak proximity breach writes `tick + 90`
	//     [03 R-VIS-01 §4 pass 4];
	//   - the work handlers' nanolathe-active stamp writes `tick + 150`
	//     (repair), `tick + 300` (build, feature reclaim, resurrection) or
	//     `tick + 900` (capture, unit reclaim) [04 R-ORD-01 §1].
	//
	// Its consumers are the economy's cloak-payment gate, which pays only when
	// `currentTick >= RevealDeadline` (inclusive) [05 R-ECO-01 §9]
	// [03 R-VIS-01 §6], and presentation's work highlight. Per I13 the two
	// clean-room contracts that name it resolve to this one Go field.
	RevealDeadline       uint32
	UnknownByteAC        uint8
	UnknownByteAD        uint8
	LOSByte              uint8
	UnknownCountdownByte uint8
	Orders               any     // [04 §3.2] front/rear segment anchors on the unit (stored as *orders.Queue via opaque to avoid import cycle)
	Script               *cob.VM // typed COB VM per-unit [04 §4.2][P1-I01] — not any, typed per acceptance
	// RenderPieceFlags is the per-unit render-piece record [04 §"Piece flag polarity"] [R-COB-01 §1].
	// One flags byte per piece in a separate array from the script's piece-animation state.
	// Allocation is zero-filled then the fill pass sets bit 1 (0x02 cache) and bit 2 (0x04 shade)
	// unconditionally and bit 0 (0x01 draw) only when the piece's model object has at least three
	// vertices. A fixture may build this table before script attachment; it must
	// NOT live inside the VM, which is only allocated for scripted units.
	RenderPieceFlags []uint8

	// Typed per-unit state introduced for P0-I02 real pipeline [04 §1.1][04 §4][06][GAP T15].
	// These fields own the authoritative per-unit data that the phase-2 sweep
	// visits in players-asc then slots-asc order [01 §6.2] C2 [P0-16].
	ScriptState *ScriptState   // per-unit COB VM/thread/piece state [04 §4.1][04 §4.2][GAP T15]; nil if not yet wired
	Slots       [NumSlots]Slot // three weapon slots [06 §1.2] C1 P0-10; local Slot avoids units→combat→economy→units cycle
	Move        MoveState      // movement status shared with movement.System [04 §8.1][04 §9.1] (movement imports units)
	// BobPhase is the hover bob's per-unit phase word: a signed 16-bit angle
	// added to the four-corner bob angle so hovercraft rock out of phase with
	// one another [04 R-MOV-01 §5]. Its single writer is the common unit
	// initializer, which stores the low 16 bits of the full-domain simulation
	// draw that follows the `buildangle` draw in the allocator's RNG call order
	// [04 R-MOV-01 §5c][R-P28-ANG-01R §2]. Every unit gets one, hovering or
	// not, and a save that restores the unit record restores the phase.
	BobPhase         int16
	Attachment       AttachmentState // carrier/cargo linkage [04 §4.4] attach-unit
	EngagementTarget pool.Handle     // saved plain engagement link; no attachment side effect [08 R-SAVE-02 §6]
	// SpotMetal is the extractor yield the CREATOR samples once, for every unit
	// it makes: Σ(cell metal byte + 1) over the stamped footprint, times the
	// definition's `extractsmetal` [05 R-PROD-01 §6]. It is never resampled, so
	// later terrain or feature changes do not move it, and the settlement reads
	// this rate rather than `extractsmetal` [05 R-PROD-01 §1].
	SpotMetal float32
	// Economy state bound to the one ledger per [05] — activation/on-off, cloak, storage, extraction, wind/tidal, makers [P1-I04].
	Activated bool // operational/activated bit for on/offable units [05 "Unit instance economy state"] [P1-I04]; true when the unit is turned on; for non-OnOffable units always true when complete
	// IsCloaked is the cloak-REQUESTED status bit — bit 11 of the runtime unit
	// status word, `CloakRequestedStatus` below [05 R-ECO-01 §9]
	// [03 R-VIS-01 §6]. It records what the player (or `init_cloaked`) asked
	// for, NOT whether the unit is hidden. Its three writers are exactly
	// retail's: the constructor's `init_cloaked` seed (InitEconomyState), the
	// `Cloak_On`/`Cloak_Off` handlers behind the definition's `cloakcost > 0`
	// capability (SetCloaked), and the save-game restore. Its one reader is the
	// settlement's cloak debit gate [05 "Cloak debit"].
	//
	// Nothing about visibility or targeting may read this field: a unit whose
	// owner cannot pay the upkeep still requests cloak and is still visible and
	// targetable. The bit those readers want is Hidden.
	IsCloaked bool
	// Hidden is the INSTANCE cloaked bit — bit 2 (mask 4) of the unit's
	// one-byte operational word, sibling of Activated (bit 0), Armored (bit 1)
	// and BuildingState (bit 3) [05 R-ECO-01 §8]. It is what §3.2's visibility
	// predicate step 2, the sensor phase's pass 5 seen probe, and weapon
	// targeting read [03 §3.2][03 R-VIS-01 §4 pass 5][03 R-VIS-01 §6].
	//
	// Its ONE writer in the economy path is the settlement's transition service
	// reaching this bit from the cloak debit: set on a pass the owner paid for,
	// cleared on a pass it could not pay and on a pass the gate was not due at
	// all [05 R-ECO-01 §9][05 "Cloak debit"]. So a freshly placed
	// `init_cloaked` unit is visible until its first paid settlement pass, and
	// a cloaked unit whose owner stalls shows again on the next pass.
	Hidden bool
	Kills  int32 // kill count for capture timer [P0-15]
	// Paralyze state per [06 §10] paralyzer status effects [P0-I04].
	ParalyzeExpire uint32 // absolute tick when stun ends; 0 means not paralyzed [06 §10]
	// Stunned is a candidate mark, not a mechanism. The stun is binary — fully
	// stopped and silent for exactly the credited ticks, with no speed scaling,
	// no movement-class change and no per-tick decrement [06 R-DMG-01 §11]. What
	// stops the unit is the head wait task plus the release verb on all three
	// weapon slots and the unconditional target clear on each of them, which the
	// Paralyze order row performs [04 R-ORD-01 §2]; the flag disables nothing on
	// its bearer. Retail's only two readers both test a *candidate*: the shared
	// autonomous target search and the computer player's target picker reject a
	// marked candidate when the searching weapon is a paralyzer, and no other
	// site reads it [06 R-DMG-01 §11].
	Stunned       bool
	CurrentSample uint8 // current 30-tick-window health sample [04 §5.1]
	PriorSample   uint8 // previous 30-tick-window health sample for death severity [04 §5.1]
	// Placement linkage for P0-04/P0-06 sparse created[] semantics [P0-04][P0-06].
	// Retail maintains created[placementIdx] sparse array and scans it in
	// placement order 0..count-1 skipping NULL gaps for Ident→Unitname first-
	// occurrence resolution (A27). Store provenance to reconstruct that scan
	// without relying on dense w.Iter() prefix.
	PlacementIdx      int // index in Mission.Units placement order, -1 if not scenario-spawned
	PlacementIdent    string
	PlacementUnitName string

	// yardTransaction is runtime topology installed by composition before strict
	// COB Create; it is deliberately absent from authoritative snapshots and
	// save records [04 §4.7 port 18].
	yardTransaction YardOpenTransaction

	// statusCue is the engine status-cue seam installed by composition, the
	// sink the activation and instance-cloak edges raise codes 3/4 and 14/15
	// into [03 R-AUD-01 §7]. Like yardTransaction it is runtime topology and is
	// deliberately absent from authoritative snapshots and save records.
	statusCue StatusCueSink

	// MoveTier is the movement-rate classifier's CACHED category — the two bits
	// the classifier writes as its final act on every run, which retail keeps in
	// bits 2–3 of the movement-mode word [04 §5.2][04 R-MOV-01 §6]. It is 0 when
	// the mover's blocked flag is set, when the unit is attached to a carrier, or
	// when the scalar speed word and the 16-bit turn residual are both zero;
	// otherwise it is 1..3 by the signed inclusive `MoveRate1`/`MoveRate2`
	// comparison. Only the movement integrator writes it; a unit that never runs
	// the integrator (every structure) keeps the seed value 0.
	//
	// It is deliberately a cache and not a live predicate. The blocked flag it
	// folds in is rewritten only by a cross-cell or mode-changing proposal's
	// verdict and is stale by construction between verdicts [04 R-COLL-01 §5], so
	// a unit whose last cross-cell proposal was rejected reads tier 0 while it
	// keeps moving inside its cell. The weapon drift gate reads this field rather
	// than recomputing the terms, which is why such a unit aims under the tight
	// gate until its next verdict [06 R-WPN-03 §2][04 §5.2 "the mover inhibit bit
	// is the blocked flag"].
	MoveTier uint8
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

// Eligible is the shared eligibility predicate `E(u)` of [07 R-WGT-01 §10],
// which every selection, inspect and bulk-walk site reads: the *selectable*
// status bit 5 is set, and the remaining-build fraction compares exactly equal
// to `0.0` — the unit's construction is complete.
//
// Corrected 2026-09-01 (WU-19-45). The second clause used to read a per-unit
// `OrderGuard` float this build wrote from the order pump. [07 R-WGT-01 §10]
// settles that there is no order-guard float: a whole-executable census of
// every load and store of the compared word identifies it as the
// remaining-build fraction — the word the construction step drives from `1.0`
// toward `0.0` [05 R-WORK-01 §1] and COB port 17 reads — and finds that NO
// routine of the order subsystem stores to it. "Eligible" therefore means
// *construction complete*, not *not mid-order*, and reads no order state at
// all. The old form was worse than imprecise: the pump wrote the guard nonzero
// whenever the primary queue was non-empty, and an idle unit's queue holds its
// authored `defaultmissiontype` standing record [04 §3.3], so every idle stock
// mobile unit read as ineligible.
//
// The predicate's last two clauses are both settled and neither adds a term to
// this receiver-only test. The post-capture grace counter is armed only by a
// capture whose new owner is a remote controller, so it is always zero in
// single-player [08 R-TRIG-01 §3]. The carrier clause — "no carrier, or a
// carrier whose cargo-selectable status bit is set" — is closed by
// [04 R-UNIT-06 §3]: that bit is a static mirror of the carrier definition's
// `isairbase` flag, written once by the unit initializer and never touched
// again, so the clause reads "cargo aboard an ordinary transport is not
// selectable, cargo attached to an airbase is". Applying it needs the carrier
// handle resolved through the world, which a method on the unit record cannot
// do, so it belongs to the callers that own a world — `internal/triggers` spells
// the clause out against the carrier's status word. `initialStatusFlags` seeds
// the mirror bit (`CargoSelectableStatus`) from `isairbase` at creation
// (WU-19-81); nothing writes it afterward.
func (u *Unit) Eligible() bool {
	if u == nil || !u.Alive || u.Dying {
		return false
	}
	return u.Flags&ClassifierEligibleStatus != 0 && u.Remaining == 0
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

// SetActivationEdge is the single writer of the unit's activation state — bit
// 0 of retail's one engine-state byte [04 R-UNIT-06 §2]. The state is written
// FIRST and only an actual change is an edge; producer-side suppression of an
// unchanged value is exactly that change test and nothing more. On the rising
// edge the COB `Activate` callback is started asynchronously (deferred, no
// arguments) and engine notification 3 is emitted; on the falling edge
// `Deactivate` and notification 4.
//
// Every producer routes through here so the bit has exactly one writer and
// cannot drift: the `Activate`/`Deactivate` order handlers [04 R-ORD-01 §2],
// the COB port-1 write arm [04 §4.7], construction's `activatewhenbuilt`
// completion and pre-built creation sites [04 R-SPEC-01 §12], and the
// per-player settlement-pass activation toggles [04 R-UNIT-06 §2].
func (u *Unit) SetActivationEdge(on bool) {
	u.setActivationEdge(on, nil)
}

// setActivationEdge is SetActivationEdge with an explicit VM for the port-1
// arm, which is installed before the strict binding is attached and therefore
// has a live VM the unit record cannot yet reach [04 §4.1][04 §4.7].
func (u *Unit) setActivationEdge(on bool, vm *cob.VM) {
	if u == nil || on == u.Activated {
		return
	}
	u.Activated = on
	// Retired (WU-19-99): this carried a TODO(question) saying the notification
	// codes were dropped because "this build has nowhere to put them" and that
	// the consumer was unknown. [03 R-AUD-01 §7] names the consumer: the codes
	// ARE the §8.3 status-cue slot indices, and every producer reaches one raise
	// helper. Codes 3/4 are slots 3 `activate` and 4 `deactivate`, whose static
	// default caption is empty.
	//
	// The COB callback runs BEFORE the cue on both edges [05 R-ECO-01 §8]
	// [03 R-AUD-01 §7].
	u.runActivationCallback(on, vm)
	if on {
		u.raiseStatusCue(StatusCueActivate)
		return
	}
	u.raiseStatusCue(StatusCueDeactivate)
}

// runActivationCallback starts the COB `Activate` / `Deactivate` callback for
// one activation edge. It is the first half of the edge machine's work; the
// status cue follows it [05 R-ECO-01 §8][03 R-AUD-01 §7].
func (u *Unit) runActivationCallback(on bool, vm *cob.VM) {
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		if on {
			binding.Callbacks.Activate()
		} else {
			binding.Callbacks.Deactivate()
		}
		return
	}
	callbackVM := vm
	if callbackVM == nil {
		callbackVM = u.GetScript()
	}
	if callbackVM == nil {
		return
	}
	if on {
		_ = callbackVM.StartByName("Activate", nil)
		return
	}
	_ = callbackVM.StartByName("Deactivate", nil)
}

// SetActivated toggles activation for OnOffable units [05] [P1-I04]. It is a
// thin alias for the one edge setter above; retail has no second activation
// writer [04 R-UNIT-06 §2].
func (u *Unit) SetActivated(on bool) {
	u.SetActivationEdge(on)
}

// SetYardOpenTransaction installs the optional port-18 transaction callback.
// Session composition installs it before strict COB binding runs Create. The
// callback owns the accepted commit and any restamp; a nil callback preserves
// direct writes for synthetic fixtures [04 §4.7 port 18][04 R-COLL-01 §4].
func (u *Unit) SetYardOpenTransaction(transaction YardOpenTransaction) {
	if u != nil {
		u.yardTransaction = transaction
	}
}

// SetCloaked writes the cloak-REQUESTED status bit — what the player asked
// for, the debit gate's first term. It is the one writer the `Cloak_On` /
// `Cloak_Off` handlers use [04 R-ORD-01 §2][05 R-ECO-01 §9]. It does not hide
// the unit: only a paid settlement pass does that, through
// SetCloakedInstance.
func (u *Unit) SetCloaked(on bool) {
	if u == nil {
		return
	}
	u.IsCloaked = on
}

// SetCloakedInstance writes the INSTANCE cloaked bit, bit 2 of the operational
// byte, and is the transition service's arrival point for the cloak debit's
// decision [05 R-ECO-01 §8][05 R-ECO-01 §9]. The write happens unconditionally
// and only the notifications are edge-gated, so the change test lives here.
//
// Retired (WU-19-99): this said "This build has no sink for engine cue codes …
// so the edge is silent here; nothing simulation-visible depends on the cue."
// Both halves are now wrong. [03 R-AUD-01 §7] identifies the sink — the codes
// are the §8.3 slot indices, 14 `cloak` and 15 `uncloak`, whose static default
// captions are `Cloaked` and `Visible` — and the rising edge additionally
// raises pending bit `0x10000` ("target cloaked") on every order record
// observing this unit, which IS simulation-visible [04 R-ORD-01 §6]. Both reach
// the session through the one sink installed here; the sink applies §7's gate
// and owns the observer walk, because this package cannot see order records.
func (u *Unit) SetCloakedInstance(on bool) {
	if u == nil || u.Hidden == on {
		return
	}
	u.Hidden = on
	if on {
		u.raiseStatusCue(StatusCueCloak)
		return
	}
	u.raiseStatusCue(StatusCueUncloak)
}

// Engine status-cue codes. The integer the edge machine passes as a "status
// code" IS the index into the static slot table of [03 §8.3]; there is no
// translation table between the two numberings [03 R-AUD-01 §7].
const (
	StatusCueActivate   uint8 = 3
	StatusCueDeactivate uint8 = 4
	StatusCueCloak      uint8 = 14
	StatusCueUncloak    uint8 = 15
)

// StatusCueSink is the engine status-cue raise seam [03 R-AUD-01 §7]. The one
// implementation is the session's raise helper, which applies the §7 gate (the
// unit's owner slot is the view slot, its alive bit is set and its death latch
// is clear), resolves the caption, inserts against the global tick, and owns
// the sim-visible observer notice the cloak rising edge carries
// [04 R-ORD-01 §6]. It draws from neither RNG stream: the only draw in the cue
// path is the CRT variant pick at resolve time, once per pop, on the
// presentation side [03 §8.3][I4].
type StatusCueSink func(u *Unit, code uint8)

// SetStatusCueSink installs the status-cue seam. Session composition installs
// it on every unit; a nil sink leaves the edges silent, which is what small
// unit-package fixtures want [03 R-AUD-01 §7].
func (u *Unit) SetStatusCueSink(sink StatusCueSink) {
	if u != nil {
		u.statusCue = sink
	}
}

// raiseStatusCue hands one (code, unit) raise to the installed sink. Every gate
// lives in the sink, not here: retail's edge machine calls the raise helper
// unconditionally and the helper returns having touched nothing when the gate
// fails [03 R-AUD-01 §7].
func (u *Unit) raiseStatusCue(code uint8) {
	if u == nil || u.statusCue == nil {
		return
	}
	u.statusCue(u, code)
}

// CloakCost returns the per-pass cloak cost, choosing stationary vs moving
// variant. It reads the cloak REQUEST, because retail selects and integerizes
// the cost inside the gated block and the gate's first term is the request bit
// [05 R-ECO-01 §9]; a unit that is merely hidden from a previous pass without
// a live request never reaches the selection.
func (u *Unit) CloakCost() float32 {
	if u == nil || u.Def == nil || !u.IsCloaked {
		return 0
	}
	if u.Move.Speed != 0 {
		return float32(u.Def.CloakCostMoving)
	}
	return float32(u.Def.CloakCost)
}

// InitEconomyState clears the engine-state activation bit at spawn and seeds
// the cloak-requested bit from the definition's `init_cloaked`
// [05 R-ECO-01 §9][03 R-VIS-01 §6][P1-I04].
//
// Correction (RWU-19-26): this cleared the cloak-requested bit unconditionally,
// on a reading of doc 05 that said the bit "is cleared at spawn along with its
// neighbours" and that `init_cloaked`'s consumer was "the INITIAL-POSTURE path:
// the build-completion transition's capability arm". Both halves were wrong and
// both doc sentences have since been corrected in place. The constructor clears
// the bit in an early masked store and then, in the same masked store that
// copies the definition's two standing-order fields, ORs `init_cloaked` back
// into it: an `init_cloaked` definition is cloak-requested from the tick it is
// placed — as a nanoframe, with no player order, because the cloak debit block
// sits outside the settlement's completion test and charges an unfinished unit
// exactly like a finished one [05 R-ECO-01 §9]. There is no initial-posture
// path; the completion transition's capability arm is `isfeature`
// [04 R-SPEC-01 §12], and the only writers of the request bit besides this one
// are the `Cloak_On` / `Cloak_Off` handlers and the save-game restore.
//
// The two bits are separate fields (WU-19-92). This seeds only the
// cloak-REQUESTED bit, Unit.IsCloaked, which is the debit gate's first term
// [05 R-ECO-01 §9]. The INSTANCE cloaked bit, Unit.Hidden, is left clear: the
// settlement's transition service is its only writer and sets it only on a
// pass the owner actually paid for, so an `init_cloaked` unit is VISIBLE from
// placement until its first paid pass, and visible again on any pass its owner
// cannot pay [03 R-VIS-01 §6][03 §3.2].
//
// EVERY unit is created INACTIVE, with no definition key consulted. Neither
// `onoffable` nor `activatewhenbuilt` is a creation-time copy of the bit:
// `activatewhenbuilt` raises the edge through the shared setter at pre-built
// creation and again at build completion [04 R-SPEC-01 §12], and `onoffable`
// gates only the Activate/Deactivate order handlers [04 R-SPEC-01 §11].
//
// The previous text pinned the bit true for a definition authoring neither
// key, calling it a stand-in for the economy's branch gate and justifying it
// as avoiding "the upkeep of 122 stock definitions" falling silent. Both
// halves were wrong. There is no separate branch gate to stand in for: the
// economy's building branch tests this same engine-state bit [05 R-ECO-01 §2],
// and [05 R-PROD-01 §2] states the consequence outright — a definition that
// omits both keys "never activates and never runs any generator". The count
// was wrong too. Over the full reference install (278 definitions) the
// pinning affects the economy of exactly three: the two Galactic Gates and
// the Stinger. Not one stock generator is touched, because every stock
// solar, wind, tidal, extractor, moho and metal maker authors
// `activatewhenbuilt` and is raised by the completion edge [I14].
//
// The pinning also swallowed the factory's state-0 activate edge, which the
// script-owned yard-door handshake depends on [05 "Factory production
// lifecycle"].
func (u *Unit) InitEconomyState() {
	if u == nil || u.Def == nil {
		return
	}
	u.Activated = false
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

	// iterHint is the live-unit count Iter last produced, used only to size
	// the next Iter allocation. It is presentation-neutral bookkeeping and
	// never reaches simulation arithmetic.
	iterHint int

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
	// createdCounters is the monotonic per-player allocation count.  Result
	// evaluation uses it to distinguish an eliminated owner from an eligible
	// slot that has not created a unit yet [05 "Player slot"][08 R-SKIR-01 §3].
	createdCounters [10]uint32

	// COB loader for per-unit VM creation [04 §4.1][P1-I01].
	cobFS     vfs.FSOps
	cobLoader *cob.CachedLoader
	// cobBinder supersedes the definition/loader program path when installed
	// by session composition. It is also used for units created by construction
	// and save reconstruction, so every production unit follows one path.
	cobBinder COBBinder

	// extraction is the plot the creator samples for a definition that
	// extracts metal [05 R-PROD-01 §6]. It is bound once at battle entry;
	// a nil sampler means no map is mounted (unit-package fixtures), and the
	// creator then leaves the rate at its zero value rather than inventing one.
	extraction ExtractionSampler

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
// and immediately start the Create script if present (hide muzzle etc.) [04 §4.1][R-CB-01 §2].
// A configured source is a production boundary: a definition with no loadable script is rejected
// before pool allocation, rather than entering the retail-null-VM crash path [R-COB-04 §8].
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
// allocations.
func (w *World) SetCOBBinder(binder COBBinder) {
	if w == nil {
		return
	}
	w.cobBinder = binder
}

// HasCOBBinder reports whether strict composition owns future allocations.
func (w *World) HasCOBBinder() bool { return w != nil && w.cobBinder != nil }

// ExtractionSampler is the map plot the creator reads when a definition
// extracts metal. It is the one input the creation-time extraction sample of
// [05 R-PROD-01 §6] needs beyond the definition and the unit's own position;
// internal/world.Terrain satisfies it.
type ExtractionSampler interface {
	SampleMetalWithFootprintSum(cx, cz int32, footX, footZ int, extractsMetal float32) (float32, uint16, error)
}

// SetExtractionSampler binds the battle's plot to the creator. It must be
// installed before the first allocation of a battle, because the extraction
// rate is sampled once at creation and never recomputed [05 R-PROD-01 §6].
func (w *World) SetExtractionSampler(s ExtractionSampler) {
	if w == nil {
		return
	}
	w.extraction = s
}

// sampleExtraction is the creation-time extraction sample of
// [05 R-PROD-01 §6], run by the CREATOR for every unit it makes — not by the
// callers that place units, which is why a direct Create (resurrection, an
// initial-mission spawn, a restore that creates) now yields the same rate as a
// unit placed through the session's own battle entry.
//
// The gate is the definition's `extractsmetal` strictly greater than zero.
// The walk covers the unit's stamped footprint rectangle starting at its
// stamped cell, and every covered cell contributes its plot metal byte plus
// one, so a metal-free cell still contributes one; the rate stored on the unit
// is that sum times `extractsmetal`. The settlement reads this stored rate and
// never `extractsmetal` itself [05 R-PROD-01 §1].
//
// Immediately after storing the rate, and only when the unit has a script, the
// creator hands the raw footprint accumulator to the script as a deferred
// `SetSpeed` so a stock extractor can size its animation [04 R-COB-04 §9];
// NotifyExtractorFootprint owns that contract.
func (w *World) sampleExtraction(u *Unit, def *content.UnitDef) {
	if w == nil || u == nil || def == nil || w.extraction == nil {
		return
	}
	if !(def.ExtractsMetal > 0) { // strictly greater than zero [05 R-PROD-01 §6]
		return
	}
	footX := int(def.FootprintX)
	footZ := int(def.FootprintZ)
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	// The stamped rectangle's minimum corner: the unit's own cell less half the
	// footprint in each axis, the same corner the occupancy stamp uses.
	cx := world.WorldToCell(u.X) - int32(footX/2)
	cz := world.WorldToCell(u.Z) - int32(footZ/2)
	rate, footprintSum, err := w.extraction.SampleMetalWithFootprintSum(cx, cz, footX, footZ, float32(def.ExtractsMetal))
	if err != nil {
		// A rectangle that leaves the map is not an error: its off-map cells
		// contribute nothing and the in-bounds ones still accumulate
		// [05 R-PROD-01 §6]. What remains here is an unseeded or unbuilt plot —
		// a setup fault, not a rate to invent: leave the zero the record was
		// created with.
		return
	}
	u.SpotMetal = rate
	u.NotifyExtractorFootprint(footprintSum)
}

// bindTransportQueries wires the two COB transport query opcodes to this
// unit's own cargo linkage [04 §4.4][04 R-COB-03 §5]. The membership query
// walks the unit's cargo list — head first, the order new cargo is pushed —
// comparing each entry's sixteen-bit identifier; the identity query follows the
// unit's carrier back-pointer. Both read the live linkage at call time, so an
// attach or drop is visible to the next query without rebinding
// [04 R-AIR-01 §9]. Neither opcode appears in shipped content, so this binding
// changes no stock script's behavior; it exists so an authored script that does
// use them gets the established answer instead of a hard-coded zero.
func bindTransportQueries(u *Unit) {
	if u == nil {
		return
	}
	vm := u.GetScript()
	if vm == nil {
		return
	}
	vm.BindTransportQueries(
		func(id int32) bool {
			// A slice walk in link order, never a map range (I1).
			for i := range u.Attachment.Cargo {
				if int32(uint16(u.Attachment.Cargo[i])) == id {
					return true
				}
			}
			return false
		},
		func() int32 { return int32(uint16(u.Attachment.Carrier)) },
	)
}

// hasLoadableCOB verifies the program that the production path will bind
// before the allocator consumes a slot. Definitions from a catalog normally
// carry the parsed program; the configured source-loader path is also checked
// here so a valid VFS-backed program is not mistaken for a missing catalog
// field [R-COB-04 §8].
func (w *World) hasLoadableCOB(def *content.UnitDef) bool {
	if def == nil {
		return false
	}
	if def.Script != nil && len(def.Script.Code) != 0 {
		return true
	}
	if w == nil || w.cobFS == nil || w.cobLoader == nil {
		return false
	}
	for _, name := range []string{def.UnitName, def.CanonicalKey} {
		if strings.TrimSpace(name) == "" {
			continue
		}
		prog, found, err := w.cobLoader.Load(w.cobFS, name)
		if err == nil && found && prog != nil && len(prog.Code) != 0 {
			return true
		}
	}
	return false
}

func (w *World) missingCOBError(def *content.UnitDef) error {
	name := "<unnamed>"
	if def != nil {
		name = def.UnitName
		if name == "" {
			name = def.CanonicalKey
		}
	}
	logical := "scripts/" + strings.ToLower(strings.TrimSpace(name)) + ".cob"
	providers := "<none>"
	if w != nil && w.cobFS != nil {
		if sources, ok := w.cobFS.(interface{ Sources(string) []vfs.EntryInfo }); ok {
			ids := make([]string, 0)
			for _, entry := range sources.Sources(logical) {
				id := entry.Source.ProviderID()
				if id == "" {
					id = entry.Source.ProviderType
				}
				if id != "" {
					ids = append(ids, id)
				}
			}
			if len(ids) != 0 {
				providers = strings.Join(ids, ", ")
			}
		}
	}
	return fmt.Errorf("nanolathe: unit script missing: logical path %s, providers searched [%s], expected COB program", logical, providers)
}

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
// followed by the full-domain draw [R-P28-ANG-01R §2] places immediately after
// it. That second draw's destination is now known: it is the unit's hover bob
// phase word [04 R-MOV-01 §5c] — the initializer stores the low 16 bits of a
// simulation draw below 0x10000 into it, once, for EVERY unit whether or not it
// can hover, and no other writer exists. Storing the value changes no draw
// count and no call order; the draw was already being taken and discarded.
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
		// The phase word is signed 16-bit, so the draw's low 16 bits are kept
		// as they land [04 R-MOV-01 §5][04 R-MOV-01 §5c].
		u.BobPhase = int16(uint16(w.simulationRNG.Uint32n(0x10000)))
	}
}

// maxReloadTicks returns the maximum reload field across all three authored
// weapon-definition links. The creation initializer scans all three pointers
// without an in-use test [R-CB-01 §4].
func maxReloadTicks(def *content.UnitDef) int32 {
	if def == nil {
		return 0
	}
	max := int32(0)
	for _, weapon := range []*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
		if weapon != nil && weapon.ReloadTime > max {
			max = weapon.ReloadTime
		}
	}
	return max
}

// attachCOB binds the definition's program to a per-unit VM and runs Create
// once as a deferred start with wake=1 [04 §4.1][R-CB-01 §2]. Missing or
// empty programs are rejected before allocation by Create and
// CreateWithForcedSlot [R-COB-04 §8].
func (w *World) attachCOB(u *Unit) error {
	if w == nil {
		return fmt.Errorf("nanolathe: COB attachment: nil world")
	}
	if u == nil {
		return fmt.Errorf("nanolathe: COB attachment: nil unit")
	}
	if u.Def == nil {
		return fmt.Errorf("nanolathe: COB attachment: nil unit definition")
	}
	if u.GetScript() != nil {
		return nil // already has VM [P1-I01]
	}
	if w.cobBinder != nil {
		// Retail has exactly one failure boundary here, and it is taken before
		// anything is allocated: "the definition carries no compiled script",
		// which yields a live unit with a null VM, a model-only piece map and no
		// `Create` [04 R-COB-01 §3]. Nothing refuses after allocation — there is
		// no bind-time validation of the program against the model, no
		// "attachment failed" state and no rollback — so a strict binder that
		// fails after allocation is a Nanolathe diagnostic with no retail
		// analog: surface it as an error and let the caller decide, never as a
		// simulation state. Every draw the allocator already took is retained on
		// both branches, the creation path's `buildangle` heading draw in
		// particular, because both branches are reached only after it: see
		// initializeAllocationHeading at the two call sites below. Do not unwind
		// a draw and do not reorder the successful path around this error.
		if err := w.cobBinder(u); err != nil {
			return err
		}
		bindTransportQueries(u)
		return nil
	}
	prog := u.Def.Script
	if (prog == nil || len(prog.Code) == 0) && w.cobLoader != nil && w.cobFS != nil {
		// Resolve via the configured loader (cached, case-insensitive) [04 §4.1].
		if p, ok, _ := w.cobLoader.Load(w.cobFS, u.Def.UnitName); ok && p != nil {
			prog = p
		} else if p, ok, _ := w.cobLoader.Load(w.cobFS, u.Def.CanonicalKey); ok && p != nil {
			prog = p
		}
	}
	if prog == nil || len(prog.Code) == 0 {
		// Retail's own scriptless branch keeps the unit alive with a null VM
		// [04 R-COB-01 §3], but the weapon-slot initializer that runs
		// immediately afterwards dereferences the VM with no null test, so a
		// scriptless unit faults retail during creation [04 R-COB-04 §8]. The
		// sanctioned divergence is to refuse instead of creating one.
		return fmt.Errorf("nanolathe: COB attachment: unit %q has no COB program", u.Def.UnitName)
	}
	vm := cob.NewVM(prog)
	bindUnitPortHandlers(vm, u)
	// Build the render-piece record for the direct loader path [04 §"Piece flag polarity"].
	// Without an authored model the geometry test cannot be applied, so we share
	// the VM's default 0x07 table as the unit's table and delegate writes there
	// via the bridge. Production scripted units with a model get their geometry-
	// driven table from the strict binder; this path has no model to apply.
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
	bindTransportQueries(u)
	// Run the shared D+wake Create adapter so hide/show and other writes are
	// visible before the first snapshot [04 §4.1][R-CB-01 §2]. The reload
	// callback is a separate deferred start after Create [R-CB-01 §4].
	bridge := u.ScriptBridge()
	bridge.Create()
	initializeCreationCallbacks(u, bridge, maxReloadTicks(u.Def))
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
// The encoding is settled: retail stores the **catalog table index** in the
// unit record's definition-identity halfword and uses it directly — the
// forced-slot allocator writes the record's definition index there and indexes
// the definition table with the same unchanged value, while the per-player unit
// visit and the commander-death sweep read a non-zero halfword as "slot
// occupied" and zero as free. That works because the catalog's record 0 is the
// reserved `None` sentinel [02 "Unit record"], so every real definition has
// index 1 or higher and 0 doubles as the free mark. Nanolathe's stable 1-based
// catalog index in the pool's "0 = free" uint16 identity space is the same
// encoding [04 R-UNIT-06 §6][P0-16 §3.2][01 §6.1].
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

// CreatedMoverMode is the mover-mode mirror every newly created unit record
// carries. Retail's creation service forces the flags word's low two bits to
// `1` before it has tested the definition's class at all, and the spawn
// wrapper that calls it then rewrites those bits from its own mode argument —
// which every spawn call site in the image passes as the literal `1`: the
// order handler that lays a building nanoframe, the mobile build handlers, the
// map-start placer, commander respawn and the campaign placer alike. The mover
// constructor writes `1` too, so a mobile unit's two words agree.
//
// The mirror therefore starts at `1` for every unit, including a building that
// will never own a mover, and only the air setter ever moves it off `1` —
// to `2` on takeoff, back to `1` on landing, and to `0` for a unit attached to
// a carrier or parked on a pad [04 R-MOV-01 §8].
//
// This matters beyond movement: the frame composer's two unit passes select on
// this mirror, and a unit left at `0` sorts into the pass that runs after the
// nanolathe strip, which hid every construction spray behind the building it
// was completing [03 R-RAST-01 §7, correction of 2026-08-30].
const CreatedMoverMode uint8 = 1

// NeutralAttackerSide is the value unit spawn writes into the attacker-side
// snapshot: retail seeds the field to the neutral side 10 alongside a null
// attacker pointer [06 R-WPN-04 §2]. It matters for the `Under Attack` notice,
// which fires when the stored snapshot differs from the victim's owner byte —
// the seed is what makes the FIRST hit on a fresh unit always announce, for
// every owner byte 0..9. A zero seed silenced that notice for player 0.
//
// It is the same neutral side a shooter-less projectile record carries; the
// combat package names its own copy for the projectile side byte and cannot be
// imported here, since combat depends on this package.
const NeutralAttackerSide uint8 = 10

// Create allocates an ALREADY-BUILT unit record through the canonical
// per-player allocator: lowest-free slot in the owning player's slice with
// slot 0 null, no generation tags, immediate reuse [01 §6.1] C1
// [P0-16 §3.2]. A slice-full failure is reported even when other players have
// free slots [P0-16 §7.3]. Failures before common initialization consume zero
// RNG draws [R-P28-ANG-01R §2].
//
// Already-built is the creation service's own argument in retail, and it is
// what conditions the `activatewhenbuilt` raise of [04 R-SPEC-01 §12] site 1.
// Nothing later in the record can stand in for it — a caller that demotes the
// record to a frame afterwards has already let the raise start the unit's
// `Activate` script. Callers building an unfinished frame must therefore say so
// up front, through CreateNanoframe.
func (w *World) Create(def *content.UnitDef, owner uint8, x, y, z numeric.Fixed) (pool.Handle, error) {
	return w.create(def, owner, x, y, z, true)
}

// CreateNanoframe allocates a unit record that is NOT created already-built:
// the unfinished-frame form of the creation service. It is Create in every
// respect except that `activatewhenbuilt` does not raise the activation edge,
// because [04 R-SPEC-01 §12] site 1 conditions that raise on the creation being
// an already-built one. Site 2 — build completion — raises it instead, and only
// then does the unit's `Activate` script run.
//
// The caller still demotes the record's construction state (remaining fraction
// and health); this entry point owns only the activation half.
func (w *World) CreateNanoframe(def *content.UnitDef, owner uint8, x, y, z numeric.Fixed) (pool.Handle, error) {
	return w.create(def, owner, x, y, z, false)
}

func (w *World) create(def *content.UnitDef, owner uint8, x, y, z numeric.Fixed, alreadyBuilt bool) (pool.Handle, error) {
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
	if !w.hasLoadableCOB(def) {
		return 0, w.missingCOBError(def)
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
	// The unfinished form seeds its construction state here, before the script
	// bind below runs `Create`: creating an unfinished unit "sets the remaining
	// construction fraction to one and its health to zero" as part of the
	// creation act [05 "Nanoframe allocation"], which is what port 4 means by
	// health "seeded ... to 0 for a nanoframe" and port 17 by a remaining
	// fraction of 1.0 for a fresh one [04 §4.4].
	//
	// The ordering is load-bearing, not tidiness. `Create` is a wake callback:
	// its drain runs all eight thread slots inline at the creation site
	// [04 R-CB-01 §2][04 "barriers and flush points"], so a thread that `Create`
	// starts reads these two ports before the creation call has returned. The
	// stock damage-smoke helper (`scripts/SMOKEUNIT.H`, started from `Create` by
	// most unit scripts) is exactly such a thread, and it waits on
	// `while (get BUILD_PERCENT_LEFT)`. Seeding a nanoframe as built and
	// demoting it after the bind walks that thread straight past its "wait until
	// the unit is actually built" loop, and the frame smokes for its whole
	// build.
	remaining := float32(0)
	health := int32(def.MaxDamage)
	if !alreadyBuilt {
		remaining = 1
		health = 0
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
		Move:         MoveState{Mode: CreatedMoverMode},
		Remaining:    remaining,
		MaxHealth:    int32(def.MaxDamage),
		Health:       health,
		PlacementIdx: -1,
		// The attacker-side snapshot is seeded to the neutral side at spawn
		// [06 R-WPN-04 §2].
		LastDamageSide: NeutralAttackerSide,
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
	// Site 1 of `activatewhenbuilt` [04 R-SPEC-01 §12]: "the unit creation
	// service, WHEN CALLED WITH ITS ALREADY BUILT ARGUMENT ... activatewhenbuilt
	// → raise bit 0", after placement and before the unit is counted. The raise
	// goes through the shared edge setter without consulting `onoffable`, so the
	// `Activate` callback runs at creation.
	//
	// A nanoframe is not created already-built, so it must not reach this at
	// all. Raising here and clearing the bit afterwards is not equivalent and
	// was a defect: the raise starts a script whose effect is self-sustaining
	// (a stock extractor's `Activate` spins its arms until `Deactivate` stops
	// it), so a mex spun and a solar opened for the whole of its own build.
	if alreadyBuilt && def.ActivateWhenBuilt {
		u.SetActivationEdge(true)
	}
	w.liveCounters[player]++
	w.createdCounters[player]++
	if w.OnCreate != nil {
		w.OnCreate(h, u)
	}
	// The creator samples the extraction rate for every unit it makes
	// [05 R-PROD-01 §6]. It runs last so the deferred `SetSpeed` it may start
	// follows `Create` and the `activatewhenbuilt` `Activate` raise above, the
	// order the placement call sites this replaces produced.
	w.sampleExtraction(u, def)
	return h, nil
}

// installWeapons is the weapon-slot initializer unit construction runs
// [06 R-WPN-05 §3]. It links each slot's weapon definition from the
// definition's ordered weapon1..3 list and writes the slot's whole control
// byte: bit 1 from the resolved definition's active byte, bits 2-3 the slot's
// own index, bit 4 set, bit 0 clear. It is the ONLY writer of the enabled bit
// in retail, which is why a slot is never disabled during play and why
// [04 R-ORD-01 §7]'s "no runtime writer of bit 1 was found" is closed.
//
// Setting bit 4 here is what [04 R-UNIT-06 §5 part 3] describes as "spawn runs
// the return verb on all three slots after writing the empty pair, so a new
// unit's slots are autonomous from its first tick": without it a freshly built
// unit's slots would never be offered a target by the retaliation walk or
// rebound by a guard's leg 2 until some order had run its return verb.
//
// TODO(question): the initializer also zeroes the slot's reload word and
// stockpile byte and stores an initial value into the slot's **distance
// word** — the word the ballistic creator divides [06 §6.4] — derived from the
// two muzzle-query points. What is unknown is only that expression: which two
// query points (aim-from and muzzle piece, or the two ends of one query), in
// which order, and whether the stored value is their separation, its square, or
// a reciprocal-shaped term the divide expects [06 R-WPN-05 §3]. What would
// settle it: a trace of the initializer's arithmetic between the weapon-link
// store and the SetMaxReloadTime dispatch, read together with the divide in the
// ballistic creator that consumes it. This build carries no such word, so
// nothing here stands in for it and no ballistic term reads one; adding a
// placeholder would change projectile arithmetic on a guess.
func installWeapons(u *Unit, def *content.UnitDef) {
	if u == nil || def == nil {
		return
	}
	// [04 §5.3] muzzle piece identity queried synchronously via AimFrom→Query fallback;
	// a missing COB query leaves -1 so muzzleWorldPosResolved falls back to root
	// rather than piece 0. Seed default before wiring weapons [06 §4.1] C3.
	for i := 0; i < NumSlots; i++ {
		u.Slots[i].MuzzlePiece = -1 // [04 §5.3] [06 §4.1] C3
		u.Slots[i].AimOriginPiece = -1
	}
	// Only active links populate a slot [06 §1.2] P0-10: the record-0
	// inactive sentinel a missed link resolves to is not a weapon [02 §5
	// R-CONTENT-02], so it must not enable the slot.
	defs := [NumSlots]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def}
	for i := 0; i < NumSlots; i++ {
		s := &u.Slots[i]
		// Bits 2-3 are the slot's own index and go on whether or not the link
		// resolved: the record is self-describing [06 R-WPN-05 §3]. Bits 5-7
		// keep whatever the record held; the initializer preserves them.
		s.Flags = (s.Flags &^ (SlotFlagAimLatch | SlotFlagEnabled | SlotFlagIndexMask | SlotFlagAutonomous)) |
			(uint8(i)<<SlotFlagIndexShift)&SlotFlagIndexMask
		if content.IsWeaponInactive(defs[i]) {
			continue
		}
		s.Weapon = defs[i]
		s.Flags |= SlotFlagEnabled | SlotFlagAutonomous
	}
}

// CreateWithForcedSlot allocates a unit at the exact forcedSlot for save
// reconstruction: the candidate is verified against the owning player's
// slice bounds and free occupancy, and the per-def limit is re-checked
// [P0-16 §3.3]. A successful reconstruction allocation runs the normal draw
// sequence before its caller restores the saved heading; validation failures
// consume zero draws [R-P28-ANG-01R §2]. Returns error on
// limit/slice-full/forced-OOB/occupied. The retail staged battle loader uses
// this path for active in-battle restoration; later fix-up passes restore the
// remaining saved unit state.
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
	if !w.hasLoadableCOB(def) {
		return 0, w.missingCOBError(def)
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
		Move:         MoveState{Mode: CreatedMoverMode},
		Remaining:    0,
		MaxHealth:    int32(def.MaxDamage),
		Health:       int32(def.MaxDamage),
		PlacementIdx: -1,
		// Same spawn seed as the ordinary allocator [06 R-WPN-04 §2]. The
		// restore adapter that follows this call does not carry an
		// attacker-side snapshot, so whatever a caller writes afterwards
		// stands.
		LastDamageSide: NeutralAttackerSide,
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
	// Site 1 of `activatewhenbuilt` [04 R-SPEC-01 §12]; see the note on the
	// ordinary allocation path above. The forced-slot entry point restores an
	// already-built record and has no unbuilt form, because every caller it has
	// today places a finished unit; a restore that had to bring back a unit
	// still under construction would need the already-built argument here too,
	// the same way CreateNanoframe carries it on the ordinary path.
	if def.ActivateWhenBuilt {
		u.SetActivationEdge(true)
	}
	w.liveCounters[player]++
	w.createdCounters[player]++
	if w.OnCreate != nil {
		w.OnCreate(h, u)
	}
	// Same creation-time sample as the ordinary allocator [05 R-PROD-01 §6].
	// The restore adapter that follows this call overwrites the rate with the
	// saved one where the save image carries it, which is what retail restores.
	w.sampleExtraction(u, def)
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
//
// This is the arm for a death that carries no damage packet — a reclaimed
// unit, a cancelled factory product, a captured victim's old record. The
// recorded-attacker link is written null for those, which is what
// [04 R-UNIT-06 §5]'s death row means by "may be null". A death that does
// carry a packet goes through DestroyBy with that packet's attacker.
func (w *World) Destroy(h pool.Handle, cause DeathCause) {
	w.DestroyBy(h, cause, 0)
}

// DestroyBy is Destroy with the death packet's attacker. [04 R-UNIT-06 §5]'s
// writer table for the recorded-attacker link has five rows, and unit death is
// one of them: the death handler writes the death packet's attacker (which may
// be null) **always** — there is no condition on it, unlike the damage
// dispatcher's row, which writes only for a non-heal packet with a nonzero
// attacker id.
//
// "Always" is the load-bearing half. A unit that had been shot, and then dies
// with no attacker behind the killing blow — drowning, a meteor, a cancelled
// build — does not keep the stale link from the earlier hit: the death handler
// overwrites it with the packet's null. Retail has no clear anywhere else, so
// this write is the only thing that can erase a link [04 R-UNIT-06 §5 part 1].
//
// The killer is stored as the pool slot the packet names, live or not; §5's
// readers are required to tolerate a dead or reused slot, and the guard's
// command resolver already does.
func (w *World) DestroyBy(h pool.Handle, cause DeathCause, killer pool.Handle) {
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
	MarkDeath(u, cause, killer)
}

// MarkDeath writes the three fields a unit's death handler writes: the death
// latch, the cause, and the recorded-attacker link taken from the death
// packet's attacker [04 §2.3][04 §2.4][04 R-UNIT-06 §5].
//
// It exists because one production death path cannot reach the world to call
// DestroyBy — the `SelfDestruct` order handler of [04 R-ORD-01 §2] applies its
// own 30000 damage and latches the death inside `internal/orders` — and the
// link must not be a field only one of the two paths remembers to write.
// Callers that hold a handle should prefer DestroyBy, which also applies the
// pool and already-marked guards.
func MarkDeath(u *Unit, cause DeathCause, killer pool.Handle) {
	if u == nil {
		return
	}
	u.Dying = true
	u.DeathCause = cause
	u.EngagementTarget = killer // [04 R-UNIT-06 §5] death row: always written
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

// FreeNeverCreated releases a record the engine must treat as never having been
// allocated at all: no death latch, no death cause, no kill record, no death
// hook, and BOTH per-player counters restored to their pre-allocation values.
// The slot returns to the pool and is lowest-free reusable the same tick
// [P0-16 §3.4][P0-16 §6.3]. Zero RNG draws.
//
// Why both counters. [04 R-FAC-02 §3] closes the abnormal ends of factory
// production and says of the third: a product freed by pool exhaustion or the
// per-definition limit "never existed". Retail reaches that state inside the
// allocator: [05 R-SHARE-01 §8] orders its tests so the null-definition,
// creatable-bit, per-definition-limit and no-free-slot refusals all return the
// null unit at steps 1-4, and only step 5's success path increments the owner's
// sixteen-bit live count and its 32-bit units-ever-created counter. A refused
// product was therefore never counted [08 R-SKIR-01 §3 "Counters"], and a
// caller unwinding an allocation this build already completed has to put both
// counters back for the outcome to match.
//
// FreeImmediate is the other free, and the two are not interchangeable: it is
// the death finalizer's, for a unit that did exist and was counted, so it
// decrements the live count only and leaves units-ever-created standing —
// exactly what [08 R-SKIR-01 §3] says the kill-record handler does. Use it for
// a unit that died; use this one only to unwind an allocation.
func (w *World) FreeNeverCreated(h pool.Handle) {
	if w == nil || w.pool == nil || h == 0 {
		return
	}
	idx := int(h)
	if idx <= 0 || idx >= len(w.units) {
		w.pool.Free(h)
		return
	}
	u := w.units[idx]
	if u == nil || !w.pool.Alive(h) {
		w.pool.Free(h)
		return
	}
	// The slot is reusable in the same tick, so a handle left in a carrier's
	// cargo list would alias whatever occupies the slot next [P0-16 §6.3]. The
	// factory rollbacks that call this cannot reach an attached product — every
	// attachment gate fails before the linkage is written — but the unwind must
	// not depend on that.
	w.unlinkAttachmentsForFree(u)
	player := int(u.Owner)
	u.Alive = false
	u.Flags &^= ClassifierEligibleStatus
	w.units[idx] = nil
	w.pool.Free(h)
	if player >= 0 && player < 10 {
		if w.liveCounters[player] > 0 {
			w.liveCounters[player]--
		}
		if w.createdCounters[player] > 0 {
			w.createdCounters[player]--
		}
	}
}

// unlinkAttachmentsForFree drops a record out of the carried representation
// without running any detach event: an allocation being unwound has no carried
// state retail ever observed, so there is nothing to publish, only linkage to
// drop before the slot is reusable [04 R-FAC-02 §3].
func (w *World) unlinkAttachmentsForFree(u *Unit) {
	if u == nil {
		return
	}
	if carrier := w.Unit(u.Attachment.Carrier); carrier != nil {
		kept := carrier.Attachment.Cargo[:0]
		for _, h := range carrier.Attachment.Cargo {
			if h != u.Handle {
				kept = append(kept, h)
			}
		}
		carrier.Attachment.Cargo = kept
	}
	u.Attachment.Carrier = 0
	u.Attachment.AttachPiece = -1
	for _, h := range u.Attachment.Cargo {
		if child := w.Unit(h); child != nil {
			child.Attachment.Carrier = 0
			child.Attachment.AttachPiece = -1
		}
	}
	u.Attachment.Cargo = nil
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

// CreatedCountForPlayer returns the monotonic number of units allocated for a
// player, including units later finalized.  It is the retail "ever created"
// counter used by elimination sweeps [08 R-SKIR-01 §3].
func (w *World) CreatedCountForPlayer(player int) uint32 {
	if w == nil || player < 0 || player >= 10 {
		return 0
	}
	return w.createdCounters[player]
}

// Iter returns units in deterministic order (pool slot ascending, I1). It is
// read several times a tick by the economy, movement, combat and publication
// phases, so the result is sized from the previous call's answer: an append
// from nil regrows the slice five or six times on the way to a battle's unit
// count, and the hint turns that into one allocation. The hint only affects
// capacity — the contents, order and length are what the scan produces.
func (w *World) Iter() []*Unit {
	if w == nil {
		return nil
	}
	var out []*Unit
	if w.iterHint > 0 {
		out = make([]*Unit, 0, w.iterHint)
	}
	for i := 1; i < len(w.units); i++ {
		if u := w.units[i]; u != nil && u.Alive {
			out = append(out, u)
		}
	}
	w.iterHint = len(out)
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
