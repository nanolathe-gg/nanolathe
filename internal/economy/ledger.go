// This file implements the ledger itself: the buckets, the storage bonus and
// the settlement arithmetic.

package economy

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Res identifies the resource kind per [05 "Player slot"].
type Res int

const (
	Metal Res = iota
	Energy
)

// Bucket holds the four accumulators per resource per unit, mirrored at player level
// per [05 "Unit instance economy state"].
type Bucket struct {
	Production float32
	Requested  float32
	Accepted   float32
	Carry      float32
}

// ArchivedBucket is the two-slot report retained from the previous settlement
// pass. Accepted and Carry are live-only state [R-ECO-01 §5].
type ArchivedBucket struct {
	Production float32
	Requested  float32
}

// Player is the per-player ledger state per [05 "Player slot"].
// Stocks and capacities are single precision per [05 "Player slot"] and I2.
// Cumulative totals and waste are double precision per [05 "Stocks, counters, and waste"] and I2.
type Player struct {
	Stock    [2]float32
	Capacity [2]float32
	Mirror   [2]Bucket
	// AIProduction and AIConsumption are settled aggregates consumed by the
	// strategic planner. They are distinct from the reporting counters for the
	// most recent pass [05 "Player slot"] [08 "Established AI-facing data and
	// rooted planner"].
	AIProduction  [2]float32
	AIConsumption [2]float32
	UpdateTime    uint32 // settlement deadline [05 "Authoritative settlement order"] [GAP T1]
	// WinLoseTime is a persisted-only sibling word: battle init seeds it with
	// the other two deadlines and the save reader restores it, but no gameplay
	// site reads or advances it — the end-condition block rides UpdateTime
	// [08 R-TRIG-01 §6][08 "Player records"]. It is kept because the save
	// writer emits it.
	WinLoseTime   uint32
	DisplayTimer  uint32 // HUD refresh deadline [05 "Saving economy, construction, and features"]
	Waste         [2]float64
	TotalProduced [2]float64
	TotalConsumed [2]float64
	// Kill/loss counters are the signed 16-bit per-player words consumed by
	// the result helper. They are written at the death finalization boundary,
	// not reconstructed from the remaining live pool [06 §12.1][08 R-CAMP-01 §7].
	Kills           int16
	Losses          int16
	CommanderKills  int16
	CommanderLosses int16
	// Rank is the slot's score-panel rank byte. Registration writes it to the
	// slot index, and nothing in battle entry touches it afterwards; its only
	// other writer is the kill-lead shift run after a credited kill
	// [08 R-SKIR-01 §2][08 R-CAMP-01 §9][07 R-HUD-04 §1]. The Space-held score
	// panel emits its rows in rank order and compacts a vacated rank as it
	// scans, so a lower byte is a better placing.
	//
	// It is not a save field. The `Player%i` account is closed at nineteen
	// scalars and names no rank [08 "Player records"], so a restored battle
	// takes the registration value again — see the seeding site.
	Rank uint8
	// Lobby metadata is retained on the authoritative player record so result
	// row eligibility and owner art do not need to inspect mutable world state.
	Name            string
	Side            uint8
	Logo            uint8
	Watcher         bool
	RejectionReason uint8
	// ResultAuxiliary is the player record's 32-bit auxiliary word, and it
	// exists only to be read as zero. Retail reads it at ten sites — the score
	// helper's row gate, the in-battle score panel, the elimination and
	// participant filters, the per-player economy gate, the multiplayer
	// alliance vote — every one a bare zero test, and STORES it nowhere outside
	// the record's bulk initialisation, so in every single-player session it is
	// constantly zero [08 R-CAMP-01 §7][07 R-HUD-04 §1]. Nothing in Nanolathe
	// writes it either: that is the same behavior, not an omission. The field
	// is kept rather than folded into a literal because it is the field both
	// contracts name; no save box reads or writes it.
	ResultAuxiliary uint32
	PassProduced    [2]float32
	PassConsumed    [2]float32
	ArchivedMirror  [2]ArchivedBucket
	// Control fields for deadline block and gate chain per [05 "Authoritative settlement order"].
	Exists bool // whether slot exists and participates [05 "Player slot"]
	// ControllerState is the slot's control byte: 1 a locally controlled human,
	// 2 a computer player, 3 a remote peer [05 R-SHARE-01 §1]. All three
	// traverse; only 1 and 2 settle [05 "Authoritative settlement order"].
	ControllerState uint8
	IsObserver      bool  // observer byte excludes observers [05 "Authoritative settlement order"]
	OptionKind      uint8 // lobby option kind used by network sharing [R-SHARE-01 §3]
	// There is no elimination flag on the player record: elimination is derived
	// from the record's two unit counters. See PlayerEliminated below.
	GameEnded bool // game-ended flag bit clear required [05 "Authoritative settlement order"]
	// EndGameCountdown must be negative for settlement; initialized -1
	// [05 "Authoritative settlement order"].
	//
	// Both arm/decrement sites are placed, and this ledger needs nothing more
	// from them [05 R-ECO-01 §12]. Retail arms the pair from the LOCAL slot's
	// 30-tick due: session kind 1 evaluates the victory predicate and otherwise
	// the defeat predicate, kinds 2 and 3 evaluate the defeat predicate behind
	// the inactive-or-not-watching test, and a session with no human
	// participant arms from the post-loop site [08 R-SKIR-01 §3] "Defeat
	// detection". Nanolathe's session does exactly that — the campaign trigger
	// poll runs on the local record's UpdateTime due, through the EndCondition
	// seam inside this very deadline block [08 R-TRIG-01 §6], and the skirmish
	// result evaluation on its own 30-tick due, and each mirrors the latch's ending
	// flag and countdown onto all ten records — so an unarmed session leaves
	// the gate permanently satisfied, which is what an in-progress game is.
	// The semantic names of the retail flag bits beyond `0x04` stay open as
	// doc 08 items; nothing here reads them.
	EndGameCountdown     int32
	Allies               [10]bool // alliance relations [05 "Player slot"]
	AutoShareMetal       bool     // automatic sharing option [05 "Allied resource and sensor sharing"]
	AutoShareEnergy      bool
	AutoShareSensor      bool
	MetalShareThreshold  float32 // separate sharing threshold [05 "Allied resource and sensor sharing"]
	EnergyShareThreshold float32
	// Storage bonus per [05 "Storage capacity"] [OX P1]: an enable flag plus a
	// per-resource bonus holding max(starting stock, 200). When set, the bonus
	// is added to the player's storage capacity during the ledger's capacity
	// sum. The fields keep float32 with integer values truncated toward zero
	// per I3, matching the retail int->float stores.
	StorageBonusEnabled  bool       // bonus applied to capacity when set [05 "Storage capacity"] [OX P1]
	StorageBonus         [2]float32 // [Metal]=max(startMetal,200), [Energy]=max(startEnergy,200) [05 "Storage capacity"] [OX P1]
	aiAggregatesPrepared bool       // composed settlement populated AI fields before post-commit [R-P0-05]
}

// Service is the economy service skeleton per plan public API.
// Players are visited 0..9 ascending per [05 "Authoritative settlement order"] and I1.
type Service struct {
	Players          [10]Player
	unitBuckets      []UnitEconomy
	ReferencePlayer  int  // reference/local player for ShareTick dispatcher [05 "Allied resource and sensor sharing"] C12
	SensorShareCalls int  // diagnostic: sensor sharing invocations at tick%450==0 [05]
	EconomySelector  *int // difficulty selector: 0 easy, 1 medium, 2 hard [R-ECO-01 §3]
	Networked        bool // networked-session gate for automatic sharing [R-SHARE-01 §3]

	// EndCondition is the end-condition block of [08 R-TRIG-01 §6]: the
	// victory and defeat polls, the shared countdown and the end latch. It is
	// invoked from inside a slot's settlement deadline block, after the
	// deadline advance and before the settlement gate chain, and only on a due
	// tick — that block's cadence *is* the settlement deadline, not a private
	// word of its own. The seam exists because the block belongs to the
	// session and economy must not import it; the callback receives the slot
	// index and owns the local-slot test, since economy has no notion of which
	// slot is local. A nil hook is a battle with no end conditions bound.
	EndCondition func(player int, tick uint32)
	// CloakCost reports a unit's per-pass cloak upkeep, or zero when the unit
	// is not cloaked [05 "Cloak debit"] C13. It is a seam rather than a field
	// read because the cloak state lives on the unit's runtime status and the
	// authored cost on its definition, and economy owns neither. A nil hook
	// skips the debit entirely, which is what a session with no cloaking units
	// would observe anyway.
	CloakCost func(*units.Unit) float32
	// CloakDue is the narrow seam for the runtime cloak gate. It reports the
	// REQUEST side only; whether the unit ends the pass hidden is decided by
	// ApplyCloakDebits and written to units.Unit.Hidden, never read back here.
	//
	// The full traced predicate is a conjunction of three terms — the
	// cloak-REQUESTED status bit is set, a second status bit is clear, and the
	// unit's per-unit cloak payment deadline is due
	// [05 "Cloak debit"][R-ECO-01 §9] — and economy
	// owns none of the three producers, so it asks rather than infers. A cloak
	// request is never inferred from authored cost alone: `cloakcost > 0` is
	// the capability that lets the order handlers write the bit, not the bit.
	//
	// Term 1 has three writers: the unit constructor seeds it from the
	// definition's `init_cloaked` flag at creation (the same masked store that
	// copies the two standing-order fields), the `Cloak_On`/`Cloak_Off` order
	// handlers set/clear it behind the definition's cloak capability, and
	// save-game restore rebuilds it from the persisted status word. The
	// build-completion transition writes nothing cloak-related — that claim
	// traced to `isfeature`'s completion arm, a different bit entirely
	// [05 R-ECO-01 §9][03 R-VIS-01 §6]. Term 2 is the decloak-forced status
	// bit: [R-ECO-01 §9] records it as inert with its meaning Unknown (a
	// bounded-negative scan of the economy path found no writer), while
	// [03 R-VIS-01 §6] names it bit 12 and gives the sensor phase's proximity
	// breach as its writer and the top of the next first pass as its clear.
	// The two readings are unobservable apart, because the one writer that
	// sets it also writes term 3's deadline to `tick + 90` on the same visit
	// [03 R-VIS-01 §4 pass 4]; the provider supplies doc 03's reading, since
	// that is the one with a named writer. Term 3 is written by the ten
	// handler sites, the breach and the projectile fill as the global tick
	// plus 90, 150, 300, 600 or 900, so an idle cloaked unit pays from the
	// first pass.
	//
	// Term 3's deadline is the ONE shared reveal/cloak-suppression field the
	// unit record carries (`units.Unit.RevealDeadline`): every writer stores
	// outright, never a maximum, and a later write always wins [03 R-VIS-01
	// §6]. The ten handler sites — `SelfRepair`/`RepairUnit`/`RepairUnitNoMove`
	// at +150; `MobileBuild`/`HelpBuild`/`Reclaim`/`Resurrect`/`VTOL_Reclaim`
	// at +300; `Capture`/`ReclaimUnit` at +900 — are the field's only
	// producers; `BuildingBuild`, `GetBuilt` and the other VTOL work handlers
	// never write it [04 R-ORD-01 §5 "The reveal stamp"]. `MobileBuild`'s and
	// `ReclaimUnit`'s work visits are implemented in internal/construction
	// rather than internal/orders, so that package supplies those two of the
	// ten. Economy still asks through the seam above rather than reading the
	// field directly: it owns neither the unit's runtime cloak-status bits nor
	// this deadline, only the gate that conjoins all three [05 R-ECO-01 §9].
	//
	// A nil hook is inert: no unit is cloak-due, which is what a fixture that
	// composes no session observes.
	CloakDue func(*units.Unit) bool

	// Wind holds the authoritative wind holder for wind generation scalar [01 §7.3]
	// [05 "Wind generation"]. Scalar is float32 published per I2.
	Wind *world.Wind
	// Terrain provides the map tidal strength [03 §2.2] [05 "Tidal generation"].
	Terrain *world.Terrain
}

// UnitEconomy holds per-unit live and archived buckets per [05 "Unit instance economy state"].
// Live buckets are the four accumulators; archived values are from the most recent settlement pass.
type UnitEconomy struct {
	Buckets  [2]Bucket
	Archived [2]ArchivedBucket
}

// ensureUnitBuckets grows the per-unit bucket slice to cover handle.
// Allocation is lowest-free ascending with slot 0 null per [01 §6.1].
func (s *Service) ensureUnitBuckets(handle pool.Handle) {
	if s == nil {
		return
	}
	need := int(handle) + 1
	if len(s.unitBuckets) >= need {
		return
	}
	nb := make([]UnitEconomy, need)
	copy(nb, s.unitBuckets)
	s.unitBuckets = nb
}

// UnitBuckets returns the live bucket pair for handle, growing the slice if needed.
// Caller must hold handle validity; slot 0 returns nil.
// DeclaresAlliance is the ONE-DIRECTIONAL alliance read of [05 R-SHARE-01 §1]:
// row A of `from`, indexed by `toward` — "this player's declaration toward each
// other slot (non-zero = allied)". A slot is always allied to itself.
//
// It exists because the shared predicate every other consumer reaches through
// composition ORs both rows, which answers "either side has declared". Two
// traced sites read exactly one row and differ from the OR under a one-sided
// declaration: automatic resource sharing (`source.A[candidate] != 0`,
// [05 R-SHARE-01 §3]) and the guard's combat join
// (`attackerOwner.A[guardOwner] == 0`, [04 R-UNIT-06 §1] as corrected by
// RWU-19-13). The symmetric predicate stays where the resolver's own hostility
// test uses it [04 R-ORD-02 §1]; this is not a replacement for it.
func (s *Service) DeclaresAlliance(from, toward uint8) bool {
	if from == toward {
		return true
	}
	if s == nil || int(from) >= len(s.Players) || int(toward) >= len(s.Players) {
		return false
	}
	return s.Players[from].Allies[toward]
}

// UnitBuckets returns a unit's metal and energy buckets, allocating the
// unit's storage on first use. It returns nil for the null handle.
func (s *Service) UnitBuckets(handle pool.Handle) *[2]Bucket {
	if s == nil || handle == 0 {
		return nil
	}
	s.ensureUnitBuckets(handle)
	return &s.unitBuckets[handle].Buckets
}

// UnitArchived returns a copy of the per-unit archived report pair written by
// the settlement apply-back of [R-ECO-01 §5]. It is a read-only accessor: the
// slots are written there and nowhere else, and a reader must not be able to
// disturb them. An unallocated handle reports the zero pair rather than
// growing the slice, so a presentation read cannot allocate.
func (s *Service) UnitArchived(handle pool.Handle) [2]ArchivedBucket {
	if s == nil || handle == 0 || int(handle) >= len(s.unitBuckets) {
		return [2]ArchivedBucket{}
	}
	return s.unitBuckets[handle].Archived
}

// RestoreUnitEconomy installs the complete per-unit live and archived bucket
// state after a save image has been validated. The bounded setter keeps the
// ledger's unit arena authoritative; no parallel raw image store is needed
// [05 "Unit instance economy state"].
func (s *Service) RestoreUnitEconomy(handle pool.Handle, buckets [2]Bucket, archived [2]ArchivedBucket) bool {
	if s == nil || handle == 0 {
		return false
	}
	s.ensureUnitBuckets(handle)
	s.unitBuckets[handle] = UnitEconomy{Buckets: buckets, Archived: archived}
	return true
}

// InitializeUnitEconomy creates or resets the complete zero account for one
// newly allocated unit. Allocation and immediate slot reuse both enter the
// same session creation barrier, before COB Create can observe or publish the
// unit [05 "Unit instance economy state"].
func (s *Service) InitializeUnitEconomy(handle pool.Handle) bool {
	return s.RestoreUnitEconomy(handle, [2]Bucket{}, [2]ArchivedBucket{})
}

// PlayerEliminated is the retail player record's elimination test, derived
// rather than flagged. Retail keeps no elimination bit; the automatic share
// dispatcher spells the predicate out as "the slot is not eliminated (`live
// unit count != 0` or `total units ever created == 0`)" [05 R-SHARE-01 §3],
// so a slot is eliminated exactly when its live count is zero AND it has
// created at least one unit. A participating slot that has never created a
// unit is NOT eliminated — that is what keeps a row alive through battle
// entry, and it is why the victory sweep answers "no victory" rather than
// "eliminated" for such a slot [08 R-SKIR-01 §3] "Victory detection".
//
// The two counters are the player record's, per [08 R-SKIR-01 §3] "Counters":
// a live unit count and a units-ever-created count, both incremented by both
// unit allocators, with the live count decremented by the kill-record handler
// when the unit is finally removed. Nanolathe files them on units.World
// (`liveCounters`, `createdCounters`) because that is where both allocators
// and the death finalizer are; per I13 the same logical field has one Go home,
// and this package already takes the world as a parameter everywhere it
// settles. A mirrored copy on Player would be read stale: the phase-2 player
// gate consults this predicate per unit, inside the same sweep that decrements
// the live count.
//
// Capture does not move either counter: [08 R-SKIR-01 §3] names the allocators
// and the kill-record handler as the only writers, and nothing in the
// ownership-transfer contract touches them.
//
// Neither counter is persisted, and that is retail behaviour rather than a
// Nanolathe omission [05 R-ECO-01 §12]. The save writer's per-player block
// enumerates its keys — the two stocks, the six cumulative totals, the two
// storage values and the storage-bonus flag, kills, losses, the three
// deadlines and the alliance block — and neither counter is among them. The
// restore path rebuilds both through the forced-slot allocator, one increment
// of each per restored unit, so a slot that had lost its last unit at save
// time reloads with both counters zero, reads as "never created" rather than
// "eliminated", and resumes settling.
func PlayerEliminated(w *units.World, player int) bool {
	if w == nil || player < 0 || player >= 10 {
		return false
	}
	return w.LiveCountForPlayer(player) == 0 && w.CreatedCountForPlayer(player) != 0
}

// ForEachUnitOrdered visits the current owner's fixed pool slice in stable
// slot order per [05 "Authoritative settlement order"] C6 and I1. Earlier
// units consume live stock before later units are tested, so slot order
// matters. The World walk is live: a later unit freed by a callback is skipped
// and a later allocation in the same slice can be reached; this is the pool's
// fixed-slice immediate-reuse behavior [04 §1.1][P0-16 §3.2]. The economy
// source applies its alive visit gate and capacity addition at each unit visit
// [05 R-ECO-01 §2][05 R-ECO-01 §4], and its high-level pass is explicitly a
// visit of eligible owned units [05 "Authoritative settlement order"]. Those
// sources do not separately settle a callback that mutates a later slot. The
// owner check remains at the visit point because the unit runtime record,
// rather than the slice bounds, is authoritative for this economy pass.
//
// This differs from the former whole-world pointer snapshot only for an
// external callback that frees and reuses a later slot. The assembled CloakDue
// and cloak-edge paths read or update existing state, order notices and staged
// presentation events; they do not allocate, free or transfer units, so the
// production settlement trace retains the same membership [05 "Cloak debit"]
// [05 R-ECO-01 §8][05 R-ECO-01 §9].
func ForEachUnitOrdered(w *units.World, player int, fn func(*units.Unit)) {
	if w == nil || fn == nil {
		return
	}
	if player < 0 || player >= 10 {
		return
	}
	// The direct slice scan has the raw-unit liveness semantics of IterSliced
	// without materializing every player's units before filtering this owner.
	w.ForEachPlayerSliceLive(player, func(u *units.Unit) {
		if int(u.Owner) != player {
			return
		}
		fn(u)
	})
}

// AddProduction verbatim accumulates authored per-pass production without time conversion
// per [05 "Authoritative settlement order"] C1. There is no division or multiplication
// by the tick rate in the ledger; the only floating constants in the whole ledger are
// -0.7 and -0.5 per [05 "Authoritative settlement order"] C1.
func AddProduction(b *Bucket, amount float32) {
	if b == nil {
		return
	}
	b.Production += amount
}

// InstallStorageBonus installs the retail storage bonus [05 "Storage capacity"]
// [OX P1]: sets the enable flag and stores max(value, 200) for each resource as
// the bonus. In Go the bonus is kept as float32 with integer value; the retail
// int->float store is exact for this range and truncates toward zero per I3.
func (p *Player) InstallStorageBonus(startMetal, startEnergy int) {
	if p == nil {
		return
	}
	p.StorageBonusEnabled = true
	if startMetal < 200 {
		startMetal = 200
	}
	if startEnergy < 200 {
		startEnergy = 200
	}
	p.StorageBonus[Metal] = float32(int32(startMetal))
	p.StorageBonus[Energy] = float32(int32(startEnergy))
}

// RebuildCapacity rebuilds player storage capacities from scratch each pass
// by summing eligible completed units' authored storage per [05 "Storage capacity"] C14.
// Eligible means alive and remaining construction fraction zero per [05 "Completed-unit eligibility"].
func RebuildCapacity(s *Service, w *units.World) {
	if s == nil {
		return
	}
	for i := range s.Players {
		rebuildCapacityPlayer(s, i, w)
	}
}

// rebuildCapacityPlayer recomputes only one player's capacities. Settlement
// uses this scoped form so a player deadline cannot publish another player's
// derived state [R-ECO-01 §4].
func rebuildCapacityPlayer(s *Service, player int, w *units.World) {
	if s == nil || player < 0 || player >= len(s.Players) {
		return
	}
	s.Players[player].Capacity[Metal] = 0
	s.Players[player].Capacity[Energy] = 0
	// Stable player-slice and slot visitation per C6 [I1].
	if w == nil {
		if s.Players[player].StorageBonusEnabled {
			s.Players[player].Capacity[Metal] += s.Players[player].StorageBonus[Metal]
			s.Players[player].Capacity[Energy] += s.Players[player].StorageBonus[Energy]
		}
		return
	}
	ForEachUnitOrdered(w, player, func(u *units.Unit) {
		if u.Def == nil {
			return
		}
		if u.Remaining != 0 {
			return
		}
		// Metal is accumulated before energy, each add re-rounded to the live
		// single-precision capacity field [R-ECO-01 §4].
		s.Players[player].Capacity[Metal] = float32(float64(s.Players[player].Capacity[Metal]) + float64(float32(u.Def.MetalStorage)))
		s.Players[player].Capacity[Energy] = float32(float64(s.Players[player].Capacity[Energy]) + float64(float32(u.Def.EnergyStorage)))
	})
	// Optional player bonuses per [05 "Storage capacity"]: when the
	// bonus flag is set, the stored bonus is added to the capacity sum after
	// the unit-storage total, with integer semantics via truncation per I3.
	if s.Players[player].StorageBonusEnabled {
		s.Players[player].Capacity[Metal] += s.Players[player].StorageBonus[Metal]
		s.Players[player].Capacity[Energy] += s.Players[player].StorageBonus[Energy]
	}
}

// DebitCloak applies a single cloak upkeep debit with truncation toward zero
// per [05 "Cloak debit"] C13. Cost is converted to integer via truncation (I3), compared
// with owner's live energy stock, and if affordable subtracted immediately with an energy
// request recorded. Units are visited in slot order by the caller.
func DebitCloak(p *Player, cost float32) bool {
	if p == nil {
		return false
	}
	need := float32(numeric.TruncateFloat32ToLow32(cost)) // truncation toward zero per [01 §8] and I3
	if need > p.Stock[Energy] {
		return false
	}
	p.Stock[Energy] = float32(float64(p.Stock[Energy]) - float64(need))
	p.Mirror[Energy].Requested = float32(float64(p.Mirror[Energy].Requested) + float64(need))
	return true
}

// ApplyCloakDebits sequentially debits eligible cloak costs for all units of
// player in slot order per [05 "Cloak debit"] C13. Earlier slots consume live
// stock before later slots are tested. CloakDue is required because economy
// does not own the runtime status/deadline producers.
//
// The outcome drives the transition service of [R-ECO-01 §8], which is the
// only writer of the unit's operational byte in the economy path and reaches
// it here with bit 2 — the INSTANCE cloaked bit, units.Unit.Hidden, the bit
// visibility and targeting read [05 R-ECO-01 §9][03 R-VIS-01 §6]. This is NOT
// the request bit CloakDue tests: the request says what the player asked for,
// and only a pass the owner actually paid for hides the unit.
//
// Three outcomes, one per pass and per unit, in slot order:
//
//   - gate due and the integerized cost is affordable — debit live stock,
//     record the request, and SET bit 2 [R-ECO-01 §9];
//   - gate due and unaffordable — no partial payment, CLEAR bit 2, so a
//     stalled owner's cloaked units show again on that pass ([05 "Cloak
//     debit"] step 6);
//   - gate not due at all — CLEAR bit 2. Established: every exit of the cloak
//     block, success and failure alike, ends at the same transition call with
//     bit 2 as the mask, and no exit leaves the bit alone [05 R-ECO-01 §9
//     "every exit"]. This was recorded here as a Supported inference with a
//     open-question marker asking whether a not-due pass clears the bit or
//     leaves it;
//     the decider it named — a trace of the debit block's exit paths — has been
//     met, and the inference is confirmed. The consequences the earlier note
//     reasoned to are the traced ones: a working builder that has cloak
//     requested stays visible after its last stroke [04 R-ORD-01 §5], and
//     `Cloak_Off`, which clears the request bit and nothing else
//     [04 R-ORD-01 §2], decloaks the unit on its next pass.
//
// The transition write itself is unconditional and only its cue notifications
// are edge-gated, so an already-clear bit costs nothing and raises nothing
// [05 R-ECO-01 §8][03 R-AUD-01 §7].
//
// onSuccess / onFailure remain the caller's own seam for anything beyond bit 2
// and are unrelated to it; economy cannot import cob, so they arrive as seams.
func ApplyCloakDebits(s *Service, w *units.World, player int, getCost func(*units.Unit) float32, onSuccess, onFailure func(*units.Unit)) {
	if s == nil || w == nil || getCost == nil {
		return
	}
	if player < 0 || player >= len(s.Players) {
		return
	}
	p := &s.Players[player]
	ForEachUnitOrdered(w, player, func(u *units.Unit) {
		if s.CloakDue == nil || !s.CloakDue(u) {
			u.SetCloakedInstance(false)
			return
		}
		cost := getCost(u)
		s.ensureUnitBuckets(u.Handle)
		if debitCloakToBucket(p, &s.unitBuckets[u.Handle].Buckets[Energy], cost) {
			u.SetCloakedInstance(true)
			if onSuccess != nil {
				onSuccess(u)
			}
		} else {
			u.SetCloakedInstance(false)
			if onFailure != nil {
				onFailure(u)
			}
		}
	})
}

func debitCloakToBucket(p *Player, b *Bucket, cost float32) bool {
	if p == nil || b == nil {
		return false
	}
	need := float32(numeric.TruncateFloat32ToLow32(cost))
	if need > p.Stock[Energy] {
		return false
	}
	p.Stock[Energy] = float32(float64(p.Stock[Energy]) - float64(need))
	b.Requested = float32(float64(b.Requested) + float64(need))
	return true
}

// clearPassInputs zeroes the three pass-local accumulators and preserves the
// carry [05 "Authoritative settlement order"] steps 1, 6 and 8.
//
// Carry is not a pass input. Step 1 clears "pass-local production, request,
// acceptance"; step 6 writes the remaining carry back to each unit and to the
// player bucket; step 8 clears the "live bucket inputs". Zeroing the whole
// four-value subrecord destroys the debt the next pass is supposed to service,
// which makes oldCarry permanently zero and the first stage of the two-stage
// algorithm dead [05 "Two-stage settlement algorithm"].
func clearPassInputs(b *Bucket) {
	b.Production = 0
	b.Requested = 0
	b.Accepted = 0
	// b.Carry survives.
}

func (p *Player) commitPassCounters() {
	if p == nil {
		return
	}
	prepared := p.aiAggregatesPrepared
	for _, r := range [...]Res{Energy, Metal} {
		if !prepared {
			p.AIProduction[r] = p.Mirror[r].Production
			p.AIConsumption[r] = p.Mirror[r].Requested
		}
		p.PassProduced[r] = p.AIProduction[r]
		p.PassConsumed[r] = p.AIConsumption[r]
		p.TotalProduced[r] += float64(p.PassProduced[r])
		p.TotalConsumed[r] += float64(p.PassConsumed[r])
	}
}

func (p *Player) commitCapacityWaste() {
	if p == nil {
		return
	}
	for _, r := range [...]Res{Energy, Metal} {
		if p.Stock[r] > p.Capacity[r] {
			overflow := float64(p.Stock[r]) - float64(p.Capacity[r])
			p.Waste[r] += overflow
			p.Stock[r] = p.Capacity[r]
		}
	}
}

// Mirror bucket closed writer set per [05 "Stocks, counters, and waste"] C11.
// There is NO factory queue-draw writer — the player mirror bucket has no such writer
// per [05 "Stocks, counters, and waste"].

// InitPlayer initializes the player mirror bucket and ledger state per C11.
func InitPlayer(p *Player) {
	if p == nil {
		return
	}
	for _, r := range [...]Res{Energy, Metal} {
		p.Mirror[r] = Bucket{}
		p.ArchivedMirror[r] = ArchivedBucket{}
		p.AIProduction[r] = 0
		p.AIConsumption[r] = 0
		p.PassProduced[r] = 0
		p.PassConsumed[r] = 0
	}
	p.aiAggregatesPrepared = false
	// Stock, Capacity, Waste, totals are zeroed by caller as needed; no float constants here.
	// Initialize control countdown to -1 so gate starts satisfied per [05 "Authoritative settlement order"].
	p.EndGameCountdown = -1
	p.GameEnded = false
}

// AdmitTwoResource admits energy and metal demands as one transaction per [05 "Two-resource admission"].
// It records both requests before testing either carry. Positive carry denies;
// unordered carry admits [05 R-ECO-01 §1][05 R-ECO-01 §7].
// The bool is the helper's own verdict — true when the work was admitted — so a
// caller takes the decision from here instead of repeating the gate for itself
// [05 R-ECO-01 §7].
func AdmitTwoResource(buckets *[2]Bucket, energy, metal float32) bool {
	if buckets == nil {
		return false
	}
	buckets[Energy].Requested += energy
	buckets[Metal].Requested += metal
	if carryAdmits(buckets[Energy].Carry) && carryAdmits(buckets[Metal].Carry) {
		buckets[Energy].Accepted += energy
		buckets[Metal].Accepted += metal
		return true
	}
	return false
}

// AdmitOneResource admits an energy-only demand per [05 "One-resource admission"].
// It returns its own admission decision, including unordered carry acceptance.
func AdmitOneResource(buckets *[2]Bucket, energy float32) bool {
	if buckets == nil {
		return false
	}
	buckets[Energy].Requested += energy
	if !carryAdmits(buckets[Energy].Carry) {
		return false
	}
	buckets[Energy].Accepted += energy
	return true
}

// carryAdmits preserves the carry gate's unordered branch: only an ordered
// positive value denies. Go's carry <= 0 would reject NaN [05 R-ECO-01 §1, §7].
func carryAdmits(carry float32) bool { return !(carry > 0) }

// AdmitTwoResourceToMirror is the mirror-bucket two-resource admission helper per C11.
// Body arrives with WU-08-3 for settlement ratios; this records admission per C11.
func AdmitTwoResourceToMirror(p *Player, energy, metal float32) {
	if p == nil {
		return
	}
	AdmitTwoResource(&p.Mirror, energy, metal)
}

// AdmitOneResourceToMirror is the mirror-bucket one-resource admission helper per C11.
func AdmitOneResourceToMirror(p *Player, energy float32) {
	if p == nil {
		return
	}
	AdmitOneResource(&p.Mirror, energy)
}

// ImmediateDebit is the direct two-resource payment of [05 R-ECO-01 §7], the
// helper weapon fire and other immediate operations use:
//
//	if (e <= playerEnergyStock && m <= playerMetalStock) {
//	    playerEnergyStock = float32( playerEnergyStock - e )
//	    energyRequested   = float32( e + energyRequested )
//	    playerMetalStock  = float32( playerMetalStock - m )
//	    metalRequested    = float32( m + metalRequested )
//	    return paid
//	}
//	return refused
//
// Both compares are inclusive and both must hold before anything is debited, so
// this is genuinely all-or-nothing. The requested accumulator is credited and
// the accepted accumulator is not, so an immediate payment appears in the pass's
// requested counter but never becomes carry.
//
// CORRECTION (AU-7) — WHOSE `requested` IS CREDITED. §7 opens by saying all five
// admission helpers "are small methods on the economy SUBRECORD of
// [R-ECO-01 §2] — which means they work identically on a unit's embedded
// subrecord and on the player-level mirror bucket, and that the 'builder's
// subrecord' a caller passes is always a subrecord, never a player record". The
// two direct payments reach through the subrecord's OWNER POINTER for the live
// stock; the `requested` write stays on the subrecord itself. This helper wrote
// both to the player mirror, so a shot's request was archived against the player
// bucket rather than against the shooter.
//
// The pass totals are identical either way — the settlement fold sums the
// per-unit subrecords into the mirror — so this is hash-neutral. What it changes
// is the per-unit archived slot, which is the only place a caller can ask "what
// did THIS unit request", and which had no reader precisely because it was never
// written.
//
// `buckets` is the paying unit's subrecord, from Service.UnitBuckets. A nil
// `buckets` credits the player mirror, which is what a payment with no unit
// behind it (a fixture, a neutral firer) has always done.
func ImmediateDebit(p *Player, buckets *[2]Bucket, energy, metal float32) bool {
	if p == nil {
		return false
	}
	// Direct payment requires ordered inclusive comparisons, unlike carry gates
	// [05 R-ECO-01 §7]. Neither resource changes on an unordered comparison.
	if !(energy <= p.Stock[Energy] && metal <= p.Stock[Metal]) {
		return false
	}
	if buckets == nil {
		buckets = &p.Mirror
	}
	p.Stock[Energy] = float32(float64(p.Stock[Energy]) - float64(energy))
	buckets[Energy].Requested = float32(float64(buckets[Energy].Requested) + float64(energy))
	p.Stock[Metal] = float32(float64(p.Stock[Metal]) - float64(metal))
	buckets[Metal].Requested = float32(float64(buckets[Metal].Requested) + float64(metal))
	return true
}

// Transfer debits the source's live stock and stages the recipient's credit
// through the existing sharing ledger. Negative amounts retain their retail
// direction; recipient capacity is applied at settlement [05 R-SHARE-01 §2].
func (s *Service) Transfer(source, destination uint8, res Res, amount float32) {
	if s == nil || source >= 10 || destination >= 10 || res < Metal || res > Energy {
		return
	}
	s.transfer(&s.Players[source], &s.Players[destination], res, amount, true)
}

// transfer is the sharing helper used by the dispatcher and packet drain.
// Debit is local-only; received packets credit the destination without
// debiting again. Credits land in mirror production and are settled later
// [R-SHARE-01 §2, §4]. A network emitter, when wired by the session layer,
// encodes Energy as subtype 1 and Metal as subtype 2 [R-SHARE-01 §2].
func (s *Service) transfer(src, dst *Player, res Res, amount float32, debit bool) {
	if s == nil || src == nil || dst == nil || amount == 0 {
		return
	}
	if debit {
		if amount > src.Stock[res] {
			amount = src.Stock[res]
		}
		if amount > src.Stock[res] {
			return
		}
		src.Stock[res] = float32(float64(src.Stock[res]) - float64(amount))
		src.Mirror[res].Requested = float32(float64(src.Mirror[res].Requested) + float64(amount))
	}
	addContribution(s, dst, &dst.Mirror[res], float64(amount))
}

// CreditSpawn writes spawn credit directly to live stock outside the ledger
// per [05 "Authoritative settlement order"] C11.
func CreditSpawn(p *Player, res Res, amount float32) {
	if p == nil {
		return
	}
	p.Stock[res] += amount
}

// CreditConstructionTermination credits metal spent on an unfinished build per C11.
// Refund is trunc((1 - remaining) × metalBuildCost) per [05 "Cancel-current and stop interrupts"] C21.
// Normally adds to builder's metal bucket; special player modes add the
// difficulty-scaled credit [R-ECO-01 §3].
// The only floating constants in the whole ledger are -0.7 and -0.5 per C1.
func CreditConstructionTermination(p *Player, remaining float32, metalBuildCost int32, specialMode int) {
	if p == nil {
		return
	}
	// Truncation toward zero per I3.
	refund := float32(numeric.TruncateFloat32ToLow32((1 - remaining) * float32(metalBuildCost)))
	switch specialMode {
	case 0:
		// selector value 0 credits one half through the negative-factor form.
		p.Mirror[Metal].Production = float32(float64(p.Mirror[Metal].Production) - float64(refund)*-0.5)
	case 1:
		// selector value 1 credits seven tenths through the negative-factor form.
		p.Mirror[Metal].Production = float32(float64(p.Mirror[Metal].Production) - float64(refund)*-0.7)
	default:
		p.Mirror[Metal].Production = float32(float64(p.Mirror[Metal].Production) + float64(refund))
	}
}
