// The 68-order descriptor table [04 §3.1][R-DOC04-C].

package orders

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// ID is the index into the sorted descriptor table; 0 is the reject sentinel [04 §3.1] C4.
type ID uint8

// PresentationHelper is the optional presentation helper a descriptor carries,
// run during command resolution [04 §3.1]. The field takes exactly four
// identities across all 68 descriptors [R-DOC04-C]; it is an identity the
// presentation layer branches on, never per-descriptor behavior.
type PresentationHelper uint8

const (
	// HelperNone: the descriptor carries no presentation helper.
	HelperNone PresentationHelper = iota
	// HelperGoalResolveAck runs goal resolution with acknowledgement text and
	// rings — the attack, suppress, capture, pickup, unload, teleport, and
	// help-build families [R-DOC04-C]. Per-record resolution: the attack
	// family is Attack_NoMove, Attack_Chase, Attack_Kamikaze, AttackSpecial,
	// AirStrike, AirToAir, AirToGround, and AirToGroundHover — AttackUType
	// carries no helper despite its name; VTOL_Landing belongs with the
	// unload family; RepairUnitNoMove carries acknowledgement without path
	// markers [R-DOC04-C].
	HelperGoalResolveAck
	// HelperGoalResolveAckPathMarkers is HelperGoalResolveAck plus moving path
	// markers — the move (incl. the queued variant QMove), patrol (incl.
	// QPatrol), repair-patrol, follow, repair-unit, reclaim (the point order
	// Reclaim alongside ReclaimUnit), and resurrect families [R-DOC04-C].
	HelperGoalResolveAckPathMarkers
	// HelperBuildFootprint draws the build-footprint marker; carried by
	// MobileBuild and VTOL_MobileBuild only [R-DOC04-C].
	HelperBuildFootprint
)

// Descriptor is one order descriptor [04 §3.1] C4.
type Descriptor struct {
	Name       string // canonical, the sort key and binary-search key [04 §3.1]
	StateLabel string // state label the interface uses for a unit running this order [04 §3.1]
	// Class is [04 §3.1]'s "small class parameter". Its reader is now known:
	// it is the **order-queue overlay's draw mask** [07 R-P0-11 §3]. The
	// Shift-gated overlay walker forms `descriptor.Class & callerMask` per
	// order node and dispatches five helpers off the result — bit 1 the
	// build-site marker, bit 2 the travelling-dash chain, bit 4 the
	// sixteen-segment circle, bit 8 the queued-order icon, bit 16 the labeled
	// range rings. That is why every observed value (0x00, 0x02, 0x03, 0x08,
	// 0x10, 0x12, 0x13, 0x18) lies inside 0x1F, and why MobileBuild's 0x13 is
	// exactly the marker, dash and rings retail draws at a queued build site.
	//
	// The field is left named Class and typed uint8 rather than renamed: the
	// name is the citation key the research table is read by, and retail's
	// 32-bit word never holds a value wider than the five-bit mask (I13 — Go
	// layout is not retail layout). The presentation-side census and the
	// pinning test live in internal/hud (queueoverlay.go).
	//
	// This retires the open-question marker [P0-07] that stood here, which recorded
	// that a bounded census over function boundaries had found no reader;
	// [04 §3.1] records the same answer in place since 2026-09-02.
	Class uint8 // order-queue overlay draw mask [04 §3.1][07 R-P0-11 §3]
	// AckGroup is [04 §3.1]'s "acknowledgement group index" and the same byte
	// [07 R-P0-11 §3] calls the descriptor's **icon byte**: the overlay's bit-8
	// helper indexes the cursor handle array with it and animates the frame at
	// `tick/(tpf*2) % nFrames`. The two descriptions are one field, which is
	// what makes §3.1's "two of the groups additionally draw the weapon
	// area-of-effect, coverage radius, and attack-length rings" the same rule
	// as §3's "the attack icons (1/2) ... additionally draw the weapon
	// AOE/coverage/attack-length rings". Icon 0 encodes "no icon", not cursor
	// slot 0.
	AckGroup   uint8  // acknowledgement group index / overlay icon byte [04 §3.1][07 R-P0-11 §3]
	StaticGate uint32 // 32-bit static gate mask; see the census note below
	// Handler is called with the owning unit, the order record, the bits
	// satisfied this tick [04 §3.1], and the tick the pump is running.
	//
	// The tick is an argument because handler bodies read the current tick
	// directly: [04 R-ORD-01 §1]'s deadline setter stores "current tick + n"
	// into the record, and a record's deadline is an absolute tick [04 §3.2].
	// Before WU-18-7 the primary walk passed no tick and four descriptors
	// reached one through by-name special cases in the pump, so every other row
	// that arms an exact deadline (`Attack_Kamikaze`'s 60, `AttackUType`'s
	// RNG(90)+1, the work family's 1/2/15/30) either could not form it or
	// measured it from the rear-segment walk's stale publication.
	Handler func(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code
	// Presentation is the descriptor's goal-resolution presentation-helper
	// identity, one of the four PresentationHelper values [04 §3.1][R-DOC04-C].
	Presentation PresentationHelper
	// Driver names what advances a record of this descriptor when it carries
	// neither a Handler nor an owning subsystem's queue registration
	// (queue_handlers.go). Retail compiles a handler into every static
	// descriptor, so this field describes Nanolathe's build, not retail: it is
	// how a record the movement scheduler's route lifecycle advances is
	// distinguished from one that is simply unimplemented.
	//
	// It replaces a by-name move-family switch in the pump that decided the
	// same thing from the descriptor's spelling. The routing fact belongs on
	// the descriptor, beside the handler it stands in for.
	Driver Driver
}

// Driver identifies what advances an order record that carries neither a
// descriptor handler nor an owning subsystem's queue registration
// (queue_handlers.go).
type Driver uint8

const (
	// DriverPump is the ordinary case: the descriptor's own Handler runs the
	// record, and a nil Handler with no queue registration means the order is
	// unimplemented in this build.
	DriverPump Driver = iota
	// DriverMovementRoute marks the path-backed move family. The movement
	// scheduler owns the route lifecycle [04 §7], so the pump parks the record
	// with the contract's wait and is re-dispatched after 30+rand15
	// [04 §3.3] C3 while path-submit and movement-integrate drive the route.
	DriverMovementRoute
)

// Retired (WU-19-191): a third value, DriverExternalMachine, stood here for the
// six rows another subsystem advances from its own per-unit step — the factory
// and mobile-build lifecycle and the unit-reclaim machine in
// internal/construction ([05 "Factory production lifecycle"][04 R-FAC-02 §4]
// [05 "Unit reclaim"]), and the air executors in internal/movement
// ([04 R-AIR-01 §6, §7]). The pump special-cased it by table lookup so it would
// not write over a live state machine's phase, gate and deadline.
//
// It is replaced by the shape `GetBuilt` already used: the owning subsystem
// registers the row's handler on the queue (Queue.SetExternallyDrivenHandler in
// queue_handlers.go), so the routing fact is the owner's own statement rather
// than a value in this table, and the pump reads one seam instead of two. The
// rows are BuildingBuild, MobileBuild, VTOL_MobileBuild, ReclaimUnit and
// VTOL_ReclaimUnit (internal/construction) and VTOL_Standby
// (internal/movement).

// StaticGate census [04 §3.1][R-DOC04-C]. Bit 1 selects nearby selection
// offsets in the command broadcast [04 R-STANCE-01 §5]. Bit 9 (0x200) is
// cleared when the order is constructed without a target unit; bit 10 (0x400)
// is cleared when constructed without a goal position; bit 18 (0x40000) marks
// a record that belongs in the rear queue segment; bit 20 (0x100000) marks
// the nanolathe/build-site class, read by the guard-assist branch. Bits 14
// and 21 exist only at runtime (tail-record inheritance and the cached target
// position) and appear in no static mask. The remaining static bits (2-8, 11,
// 16, 17, 19, 24) have no located reader in the [R-DOC04-C] census: the raw
// mask is stored verbatim and never interpreted — do not add readers, do not
// add or drop bits, without a new research finding.

// The four static batches below are transcribed verbatim in their compiled
// registration order [R-DOC04-C]. buildTable appends them batch by batch and
// re-sorts the whole table after every batch with foldCompare, the C runtime's
// case-insensitive compare — the same comparator Lookup searches with
// [04 R-STANCE-01 §9] — so an order's identity is its index in the final sorted
// table [04 §3.1] C4. buildTable records why that comparator, and not a
// case-sensitive byte compare, is the one retail registers with.

// batch1 is registration batch 1: 23 records [R-DOC04-C].
var batch1 = []Descriptor{
	{Name: "Stop", StateLabel: "Stopping", Class: 0x00, AckGroup: 19, StaticGate: 0x0, Presentation: HelperNone},
	{Name: "Attack_NoMove", StateLabel: "Attacking", Class: 0x08, AckGroup: 1, StaticGate: 0x280, Presentation: HelperGoalResolveAck},
	{Name: "Activate", StateLabel: "Activate", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone, Handler: activateHandler},
	{Name: "Deactivate", StateLabel: "Deactivate", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone, Handler: deactivateHandler},
	{Name: "Cloak_On", StateLabel: "Cloaking", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone},
	{Name: "Cloak_Off", StateLabel: "Decloaking", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone},
	{Name: "Standing_MoveOrder", StateLabel: "Acknowledged", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone},
	{Name: "Standing_FireOrder", StateLabel: "Acknowledged", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone},
	{Name: "BuildingBuild", StateLabel: "Nanolathing", Class: 0x00, AckGroup: 19, StaticGate: 0x10010c, Presentation: HelperNone},
	{Name: "BuildWeapon", StateLabel: "Nanolathing", Class: 0x00, AckGroup: 19, StaticGate: 0xc0140, Presentation: HelperNone},
	{Name: "SelfDestruct", StateLabel: "SELF DESTRUCT ENGAGED", Class: 0x00, AckGroup: 19, StaticGate: 0x40040, Presentation: HelperNone},
	{Name: "SelfDestructFG", StateLabel: "SELF DESTRUCT ENGAGED", Class: 0x00, AckGroup: 19, StaticGate: 0x0, Presentation: HelperNone},
	{Name: "Paralyze", StateLabel: "Paralyzed", Class: 0x00, AckGroup: 19, StaticGate: 0x24, Presentation: HelperNone},
	{Name: "GetBuilt", StateLabel: "Under construction", Class: 0x00, AckGroup: 19, StaticGate: 0x224, Presentation: HelperNone},
	{Name: "BeCarried", StateLabel: "Being transported", Class: 0x00, AckGroup: 19, StaticGate: 0x24, Presentation: HelperNone},
	{Name: "MakeSelectable", StateLabel: "Unit is available", Class: 0x00, AckGroup: 19, StaticGate: 0x4, Presentation: HelperNone, Handler: makeSelectableHandler},
	{Name: "Wait", StateLabel: "Waiting", Class: 0x00, AckGroup: 19, StaticGate: 0x4, Presentation: HelperNone},
	{Name: "WaitForAttack", StateLabel: "Waiting for attack", Class: 0x00, AckGroup: 19, StaticGate: 0x204, Presentation: HelperNone},
	{Name: "AttackUType", StateLabel: "Attacking", Class: 0x00, AckGroup: 19, StaticGate: 0x4, Presentation: HelperNone},
	{Name: "Guard_NoMove", StateLabel: "Ready", Class: 0x00, AckGroup: 19, StaticGate: 0x20, Presentation: HelperNone},
	{Name: "SelfRepair", StateLabel: "Repairing", Class: 0x00, AckGroup: 19, StaticGate: 0x1000204, Presentation: HelperNone},
	{Name: "QMove", StateLabel: "Ready with orders", Class: 0x02, AckGroup: 14, StaticGate: 0x400, Presentation: HelperGoalResolveAckPathMarkers, Driver: DriverMovementRoute},
	{Name: "QPatrol", StateLabel: "Ready with orders", Class: 0x02, AckGroup: 7, StaticGate: 0x400, Presentation: HelperGoalResolveAckPathMarkers, Driver: DriverMovementRoute},
}

// batch2 is registration batch 2: 22 records [R-DOC04-C].
var batch2 = []Descriptor{
	{Name: "Standby", StateLabel: "Standby", Class: 0x10, AckGroup: 15, StaticGate: 0x20000, Presentation: HelperNone},
	{Name: "Standby_Mine", StateLabel: "Standby", Class: 0x10, AckGroup: 15, StaticGate: 0x1020000, Presentation: HelperNone},
	{Name: "Move_Ground", StateLabel: "Moving", Class: 0x12, AckGroup: 14, StaticGate: 0x402, Presentation: HelperGoalResolveAckPathMarkers, Driver: DriverMovementRoute},
	{Name: "Follow_Ground", StateLabel: "Guarding", Class: 0x12, AckGroup: 5, StaticGate: 0x200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "Suppress", StateLabel: "Suppressing fire", Class: 0x08, AckGroup: 1, StaticGate: 0x410, Presentation: HelperGoalResolveAck},
	{Name: "Attack_Chase", StateLabel: "Attacking", Class: 0x08, AckGroup: 1, StaticGate: 0x280, Presentation: HelperGoalResolveAck},
	{Name: "Attack_Kamikaze", StateLabel: "Attacking", Class: 0x08, AckGroup: 1, StaticGate: 0x600, Presentation: HelperGoalResolveAck},
	{Name: "AttackSpecial", StateLabel: "Annihilating", Class: 0x08, AckGroup: 1, StaticGate: 0x680, Presentation: HelperGoalResolveAck},
	{Name: "Park", StateLabel: "Parking", Class: 0x00, AckGroup: 14, StaticGate: 0x0, Presentation: HelperNone},
	{Name: "Patrol", StateLabel: "Patrolling", Class: 0x12, AckGroup: 7, StaticGate: 0x412, Presentation: HelperGoalResolveAckPathMarkers, Driver: DriverMovementRoute},
	{Name: "Ground_Pickup", StateLabel: "Loading", Class: 0x08, AckGroup: 12, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "Ground_Unload", StateLabel: "Unloading", Class: 0x08, AckGroup: 13, StaticGate: 0x400, Presentation: HelperGoalResolveAck},
	{Name: "Teleport", StateLabel: "Teleporting", Class: 0x08, AckGroup: 9, StaticGate: 0x600, Presentation: HelperGoalResolveAck},
	{Name: "MobileBuild", StateLabel: "Nanolathing", Class: 0x13, AckGroup: 0, StaticGate: 0x100508, Presentation: HelperBuildFootprint},
	{Name: "HelpBuild", StateLabel: "Nanolathing", Class: 0x18, AckGroup: 6, StaticGate: 0x100208, Presentation: HelperGoalResolveAck},
	{Name: "RepairPatrol", StateLabel: "Repair patrol", Class: 0x12, AckGroup: 7, StaticGate: 0x412, Presentation: HelperGoalResolveAckPathMarkers, Driver: DriverMovementRoute},
	{Name: "RepairUnit", StateLabel: "Repairing", Class: 0x12, AckGroup: 6, StaticGate: 0x100200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "Capture", StateLabel: "Capturing", Class: 0x08, AckGroup: 4, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "Resurrect", StateLabel: "Resurrecting", Class: 0x12, AckGroup: 11, StaticGate: 0x200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "Reclaim", StateLabel: "Reclaiming", Class: 0x12, AckGroup: 11, StaticGate: 0x100800, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "ReclaimUnit", StateLabel: "Reclaiming", Class: 0x12, AckGroup: 11, StaticGate: 0x100200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "RepairUnitNoMove", StateLabel: "Repairing", Class: 0x18, AckGroup: 6, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
}

// batch3 is registration batch 3: 22 records [R-DOC04-C].
var batch3 = []Descriptor{
	{Name: "VTOL_Standby", StateLabel: "Standby", Class: 0x00, AckGroup: 15, StaticGate: 0x20000, Presentation: HelperNone},
	{Name: "VTOL_Move", StateLabel: "Moving", Class: 0x02, AckGroup: 14, StaticGate: 0x402, Presentation: HelperGoalResolveAckPathMarkers, Driver: DriverMovementRoute},
	{Name: "VTOL_Landing", StateLabel: "Landing", Class: 0x08, AckGroup: 14, StaticGate: 0x600, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_Pickup", StateLabel: "Loading", Class: 0x08, AckGroup: 8, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_Unload", StateLabel: "Unloading", Class: 0x08, AckGroup: 9, StaticGate: 0x400, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_Follow", StateLabel: "Guarding", Class: 0x02, AckGroup: 5, StaticGate: 0x200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_Patrol", StateLabel: "Patrolling", Class: 0x02, AckGroup: 7, StaticGate: 0x412, Presentation: HelperGoalResolveAckPathMarkers, Driver: DriverMovementRoute},
	{Name: "AirStrike", StateLabel: "Airstrike", Class: 0x08, AckGroup: 2, StaticGate: 0x600, Presentation: HelperGoalResolveAck},
	{Name: "AirToAir", StateLabel: "Engaging target", Class: 0x08, AckGroup: 1, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "AirToGround", StateLabel: "Engaging target", Class: 0x08, AckGroup: 1, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "AirToGroundHover", StateLabel: "Engaging target", Class: 0x08, AckGroup: 1, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_MobileBuild", StateLabel: "Nanolathing", Class: 0x03, AckGroup: 0, StaticGate: 0x100508, Presentation: HelperBuildFootprint},
	{Name: "VTOL_HelpBuild", StateLabel: "Nanolathing", Class: 0x08, AckGroup: 6, StaticGate: 0x100208, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_RepairPatrol", StateLabel: "Repair patrol", Class: 0x02, AckGroup: 7, StaticGate: 0x412, Presentation: HelperGoalResolveAckPathMarkers, Driver: DriverMovementRoute},
	{Name: "VTOL_RepairUnit", StateLabel: "Repairing", Class: 0x02, AckGroup: 6, StaticGate: 0x100200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_Reclaim", StateLabel: "Reclaiming", Class: 0x02, AckGroup: 11, StaticGate: 0x100800, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_ReclaimUnit", StateLabel: "Reclaiming", Class: 0x02, AckGroup: 11, StaticGate: 0x100200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_Evade", StateLabel: "Evading", Class: 0x00, AckGroup: 19, StaticGate: 0x0, Presentation: HelperNone},
	{Name: "VTOL_SeekAttack", StateLabel: "Seeking to attack", Class: 0x00, AckGroup: 19, StaticGate: 0x600, Presentation: HelperNone},
	{Name: "VTOL_SeekGuard", StateLabel: "Seeking to guard", Class: 0x00, AckGroup: 19, StaticGate: 0x600, Presentation: HelperNone},
	{Name: "VTOL_GetRepaired", StateLabel: "Under repair", Class: 0x00, AckGroup: 19, StaticGate: 0x200, Presentation: HelperNone},
	{Name: "VTOL_LandIfCan", StateLabel: "Seeking to land", Class: 0x00, AckGroup: 19, StaticGate: 0x400, Presentation: HelperNone},
}

// batch4 is registration batch 4: the single empty-name record. It has no
// static image in the read-only data; that it is appended at registration
// rather than compiled in is Supported inference [R-DOC04-C]. Ordering is
// unaffected either way — every batch append re-sorts the whole table, and
// the empty name sorts to index 0, the reject sentinel [04 §3.1] C4.
var batch4 = []Descriptor{
	{Name: "", StateLabel: "", Class: 0x00, AckGroup: 0, StaticGate: 0x0, Presentation: HelperNone},
}

var table []Descriptor

func init() { buildTable() }

// foldCompare orders two canonical command names the way the C runtime's
// case-insensitive string compare does: byte by byte over the lowercased
// bytes, shorter string first on a common prefix. The names are ASCII, so
// folding only A-Z is exact.
//
// The distinction that matters is the underscore. Lowercasing maps A-Z into
// the a-z range, which puts `_` (0x5f) *below* every letter instead of between
// the upper- and lower-case ranges — that is the whole of the ordering
// difference against a raw byte compare [04 R-STANCE-01 §9].
func foldCompare(a, b string) int {
	fold := func(c byte) byte {
		if c >= 'A' && c <= 'Z' {
			return c + ('a' - 'A')
		}
		return c
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := fold(a[i]), fold(b[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// buildTable builds the 68-entry table from the four static batches of 23,
// 22, 22, and 1 records [R-DOC04-C]; after every batch the whole table
// re-sorts ascending by canonical name. The final sorted order is the 67
// named commands plus the empty sentinel at index 0.
//
// The comparator is case-insensitive. §3.1 used to say the sort was a
// case-sensitive byte comparison and this code implemented that, which put
// seven rows in the wrong order: identities 6-10 held AttackSpecial,
// AttackUType, Attack_Chase, Attack_Kamikaze, Attack_NoMove and 12-13 held
// BuildWeapon, BuildingBuild. The registration routine sorts with the same
// C-runtime case-insensitive compare the lookup below uses, so the Attack*
// and Build* clusters invert: 6-10 are Attack_Chase, Attack_Kamikaze,
// Attack_NoMove, AttackSpecial, AttackUType and 12-13 are BuildingBuild,
// BuildWeapon [04 R-STANCE-01 §9]. A case-sensitive sort under a
// case-insensitive search is not merely inconsistent — the search cannot
// find Attack_Chase at all, which disables the chase attack of §3.5. No
// other identity moves; GetBuilt keeps index 0x13 under either order.
func buildTable() {
	all := make([]Descriptor, 0, 68)
	for _, batch := range [][]Descriptor{batch1, batch2, batch3, batch4} {
		all = append(all, batch...)
		sort.Slice(all, func(i, j int) bool { return foldCompare(all[i].Name, all[j].Name) < 0 })
	}
	table = all
	getBuiltID = Lookup("GetBuilt") // the row whose owner registers per queue
	resolveRows()                   // every row this package names, resolved once
	installHandlers()               // the table is not finished until its handlers are on it
}

// The rows this package names by canonical string. Lookup is a fold-compare
// binary search over 68 names, and the pump, the guard family, the parking
// helpers, the stop family and the reaction routine each reached for one or
// more of them per unit per tick; a row identity cannot move once the table is
// sorted, so each name is resolved once here instead.
//
// They are assigned by resolveRows rather than by package-variable
// initializers because Go runs every package-level initializer BEFORE init,
// and the table this package searches does not exist until buildTable runs
// inside init. A var initializer would resolve every one of these against an
// empty table and silently produce the sentinel row 0.
//
// A zero id means the table names no such row, which every call site already
// spelled as an explicit `!= 0` guard; a zero row can never equal a live
// node's id, so those guards keep their meaning unchanged.
var (
	rowSelfDestruct    ID
	rowVTOLSeekAttack  ID
	rowMobileBuild     ID
	rowVTOLMobileBuild ID
	rowBuildingBuild   ID
	rowHelpBuild       ID
	rowVTOLSeekGuard   ID
	rowAttackChase     ID
	rowFollowGround    ID
	rowVTOLFollow      ID
	rowVTOLMove        ID
	rowPark            ID
	rowMoveGround      ID
	rowStop            ID
	rowVTOLLanding     ID
	rowParalyze        ID
	rowVTOLLandIfCan   ID
	rowBuildWeapon     ID
)

func resolveRows() {
	rowSelfDestruct = Lookup("SelfDestruct")
	rowVTOLSeekAttack = Lookup("VTOL_SeekAttack")
	rowMobileBuild = Lookup("MobileBuild")
	rowVTOLMobileBuild = Lookup("VTOL_MobileBuild")
	rowBuildingBuild = Lookup("BuildingBuild")
	rowHelpBuild = Lookup("HelpBuild")
	rowVTOLSeekGuard = Lookup("VTOL_SeekGuard")
	rowAttackChase = Lookup("Attack_Chase")
	rowFollowGround = Lookup("Follow_Ground")
	rowVTOLFollow = Lookup("VTOL_Follow")
	rowVTOLMove = Lookup("VTOL_Move")
	rowPark = Lookup("Park")
	rowMoveGround = Lookup("Move_Ground")
	rowStop = Lookup("Stop")
	rowVTOLLanding = Lookup("VTOL_Landing")
	rowParalyze = Lookup("Paralyze")
	rowVTOLLandIfCan = Lookup("VTOL_LandIfCan")
	rowBuildWeapon = Lookup("BuildWeapon")
}

// handlerInstallers is the ordered list of per-family handler installers — the
// single place a handler family is registered onto the descriptor table.
//
// Retail compiles the handler into each static descriptor, so all 68 records
// carry theirs before play begins [04 §3.1]. We build the same table from Go
// files that cannot all initialise before table.go's init, so each family owns
// an installer that assigns its handlers onto the built table, and this list
// runs them. One family, one file, one line here.
//
// The list is a slice, not a map: registration order is source order, and map
// iteration would make "which family claimed a descriptor first" vary per run
// (I1). Every installer is idempotent, and all but one reach that by assigning
// only where the descriptor's Handler is still nil. The exception is
// ensureHandlers (resolve.go), whose registerChaseGuardHandlers assigns its
// three rows — `Attack_Chase`, `Follow_Ground`, `VTOL_Follow` — unconditionally;
// it is idempotent because the installer itself returns early once the family
// has registered, which it decides by testing whether `Attack_Chase` already
// carries a handler. Nothing re-runs this list after buildTable: installHandlers
// has exactly one caller.
//
// Adding a family: write internal/orders/<family>.go with an ensure<Family>
// function shaped like ensureStopHandler, then add exactly one line below.
var handlerInstallers = []func(){
	ensureMoveHandlers,         // pump.go — Move_Ground, and only that row
	ensurePatrolHandlers,       // patrol.go — the queued-move pair, both ground patrols, the two air moves
	ensureTransportHandlers,    // transport.go — pickup, unload, landing, BeCarried
	ensureParkHandler,          // park.go
	ensureStopHandler,          // stop.go
	ensureStandingHandlers,     // standing.go — standing, cloak, wait, paralyze, teleport, standby
	ensureSelfDestructHandlers, // selfdestruct.go — SelfDestruct and SelfDestructFG
	ensureWorkHandlers,         // work.go — capture, reclaim, resurrect, assist, the repair trio
	ensureVTOLWorkHandlers,     // vtolwork.go — the VTOL work twins of [04 R-ORD-01 §7]
	ensureHandlers,             // resolve.go — Attack_Chase and the three guards
	ensureVTOLAirHandlers,      // vtolair.go — the air executors of [04 R-AIR-01 §7, §8] and the four air-attack rows
	ensureCombatHandlers,       // combat.go — the combat handlers of [04 R-ORD-01 §3]
}

// installHandlers runs every family installer in list order. buildTable is its
// only caller, and calls it so the table is complete before the first pump; the
// pump does not re-run it. One family has a lazy retry of its own: Resolve calls
// ensureHandlers on every resolution, which is what covers a fixture whose init
// order ran before table.go's.
//
// Handlers a subsystem owns rather than this package are not installed here.
// They bind per queue through the registration seam in queue_handlers.go:
// `GetBuilt`'s lifecycle belongs to the construction service
// [04 R-FAC-02 §4], and the six rows another package advances from its own
// per-unit step are registered by that package where it binds the queue.
func installHandlers() {
	for _, install := range handlerInstallers {
		install()
	}
}

// Table returns the 68-entry descriptor table, index 0 is "" sentinel [04 §3.1] C4.
func Table() []Descriptor { return table }

// Lookup returns the ID for name, or the reject sentinel 0 on a miss.
//
// This is retail's ordinary lower_bound over the sorted table using the same
// case-insensitive comparator the sort used, so callers need not match the
// table's spelling: the interface transmits STANDING_FIREORDER and it resolves
// to the descriptor named Standing_FireOrder [04 R-STANCE-01 §9].
//
// There is no name map beside this search. An exact-case map answered before
// the search and so silently reintroduced case-sensitive lookup for every name
// spelled as the table spells it, leaving the fold to act only as a fallback.
func Lookup(name string) ID {
	i := sort.Search(len(table), func(i int) bool { return foldCompare(table[i].Name, name) >= 0 })
	if i < len(table) && foldCompare(table[i].Name, name) == 0 {
		return ID(i)
	}
	return 0
}

// DescriptorFor returns the descriptor for id, or the sentinel if out of range.
func DescriptorFor(id ID) Descriptor {
	if int(id) < len(table) {
		return table[int(id)]
	}
	return table[0]
}
