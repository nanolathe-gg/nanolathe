package mission

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// attachPair records one `i name` verb's immediate internal attach request in
// the order the verb was seen during pass two's per-unit script interpretation
// [04 §3.6] "i name": cargoHandle is the acting unit (it boards itself),
// carrierHandle is the named carrier already resolved at the verb site. No
// import cycle exists between internal/mission and internal/movement
// (`go list -deps` confirms neither imports the other), so the pairs are
// applied through the shared cargo representation's own helper rather than
// duplicating its field writes.
type attachPair struct {
	cargoHandle   pool.Handle
	carrierHandle pool.Handle
}

// RunInitialMissionsWithCatalog interprets InitialMission strings once after
// all mission units exist, for mission type 1 and BetweenMissions restores
// only. It registers with no tick dispatcher; from the next tick the ordinary
// pump consumes queued orders [04 §3.6] C9. Unit existence and building-vs-
// mobile classification are resolved exclusively through cat, matching the
// catalog-backed runtime path [04 §3.6].
//
// The extraction rate this interpreter's units carry is not its concern: the
// sample is the CREATOR's, run inside units.World.Create for every unit it
// makes [05 R-PROD-01 §6], and the settlement reads that stored rate rather
// than `extractsmetal` [05 R-PROD-01 §1].
func RunInitialMissionsWithCatalog(m *Mission, w *units.World, cat *content.Catalog) {
	if m == nil || w == nil {
		return
	}
	// C9: only type 1 (campaign) games and BetweenMissions restores run the
	// interpreter [04 §3.6]. IsRestore is set by the save-restore load path.
	if m.Type != TypeCampaign && !m.IsRestore {
		return
	}
	// P0-04/P0-06: two-pass spawner with sparse created[] array [P0-04][P0-06].
	// Pass one calls the unit creator once per placement and stores the result,
	// leaving a null hole where the pool or the per-player limit refused it;
	// pass two walks that sparse array and skips the holes [P0-06].
	// We reconstruct the sparse array by the placement index stored on the unit.
	//
	// PlacementIdx is the only linkage: a unit the creator refused never
	// carries one, and its slot stays null. There is no second reconstruction
	// — a dense-order arm used to stand here for fixtures that created units
	// without stamping the index, and it made "the world holds as many units as
	// there are placements" mean "they are in placement order", which is
	// precisely what the sparse array exists to stop assuming.
	createdSparse := make([]*units.Unit, len(m.Units))
	for _, u := range w.Iter() {
		if u == nil {
			continue
		}
		if u.PlacementIdx >= 0 && u.PlacementIdx < len(createdSparse) {
			createdSparse[u.PlacementIdx] = u
		}
	}
	RunInitialMissionsForCreated(m.Units, createdSparse, w, cat)
}

// RunInitialMissionsForCreated interprets one already-created placement pass.
// The caller supplies the full authored placement slice and its equally-sized
// sparse result array, so identifier and unit-name references retain placement
// indices across failed allocations. It is used by the Community schema-unit
// initial pass; the deferred queue never calls it
// (research/extensions/community-patch-engine.md, CP-UD-3).
//
// Unlike RunInitialMissionsWithCatalog, this helper has no mission-type gate:
// its caller owns the approved entry-path decision and the exactly-once
// lifetime. Campaign and restore callers continue through the gated wrapper.
func RunInitialMissionsForCreated(placements []UnitPlacement, createdSparse []*units.Unit, w *units.World, cat *content.Catalog) {
	if w == nil || len(placements) == 0 || len(createdSparse) != len(placements) {
		return
	}
	// Build ident->placementIdx and unitname->placementIdx maps for g/wa lookups
	// via sparse scan in placement order 0..count-1, first-occurrence wins,
	// skipping NULL gaps [P0-06][P0-04] A27. Retail scans created[] directly.
	identMap := make(map[string]int)    // lower(Ident) -> placementIdx
	unitNameMap := make(map[string]int) // lower(UnitName) -> placementIdx
	for i, pl := range placements {
		if createdSparse[i] == nil {
			continue
		}
		if pl.Ident != "" {
			lower := strings.ToLower(pl.Ident)
			if _, ok := identMap[lower]; !ok {
				identMap[lower] = i
			}
		}
		if pl.UnitName != "" {
			lower := strings.ToLower(pl.UnitName)
			if _, ok := unitNameMap[lower]; !ok {
				unitNameMap[lower] = i
			}
		}
	}
	// Immediate internal attach requests posted by `i name` verbs, collected in
	// verb-encounter (== placement) order and applied after pass two's per-unit
	// script loops finish [04 §3.6]; [R-TRIG-01 §9] "no delayed queue, cargo
	// loop, or separate attachment pass exists beyond the immediate attach
	// verb" — this is that one mechanism, not a second one. A slice keeps the
	// application order deterministic without ranging a map (I1).
	attachPairs := make([]attachPair, 0)

	// Pass two interprets the placement's InitialMission text for every entry
	// whose record carries a mission string and whose pass-one creation
	// succeeded [P0-06].
	for idx, placement := range placements {
		u := createdSparse[idx]
		if u == nil {
			continue
		}
		if strings.TrimSpace(placement.InitialMission) == "" {
			continue
		}
		// Also skip if InitialMission string was empty/NULL in retail sense.
		// We already handle empty string above; retain explicit check for NULL
		// equivalent (empty).
		script := placement.InitialMission
		// Prepare worldUnits list for handlers that need dense iteration (e.g., g target handle lookup).
		// Handlers will use createdSparse via identMap, not dense.
		// Keep worldUnits for handle resolution but base maps on sparse.
		worldUnits := w.Iter()
		ctx := &interpCtx{
			unit:          u,
			worldUnits:    worldUnits,
			createdSparse: createdSparse,
			identMap:      identMap,
			unitNameMap:   unitNameMap,
			attachPairs:   &attachPairs,
			catalog:       cat,
			placementIdx:  idx,
		}
		tokens := tokenizeScript(script)
		for _, tok := range tokens {
			trimmed := strings.TrimSpace(tok)
			if trimmed == "" {
				continue
			}
			// Clamp per-token frame 255 [04 §3.6] C13 sanctioned divergence.
			if len(trimmed) > 255 {
				trimmed = trimmed[:255] // [04 §3.6] clamp, spec-recommended divergence
			}
			dispatchToken(trimmed, ctx)
		}
		// Postlude C12: when at least one order queued, bit5 clears and unless
		// suppressTail latch (set by numeric a, p, d, s) a final MakeSelectable queues [P0-06].
		if ctx.queued > 0 {
			u.ClearClassifierEligibility() // [04 §3.6] runtime status bit 5
			if !ctx.suppressTail {
				id := orders.Lookup("MakeSelectable") // [04 §3.6] C12 zero aux args
				if id != 0 {
					q := orders.QueueForUnit(u)
					// The tail record carries no auxiliary arguments [04 §3.6],
					// but it is still this unit's record. It goes through
					// ctx.record rather than ctx.push because it must not raise
					// the issued count the enclosing condition has just read.
					q.Push(id, ctx.record(orders.Node{}))
				}
			}
		}
	}
	// Apply every immediate attach request in the order the `i name` verbs
	// were seen, through the shared cargo representation [04 §3.6]; mode 0 is
	// the request mode "on every ordinary attach" [R-AIR-01 §9], piece −1 is
	// the root fallback when no piece is named [04 §5.3][04 §10.2]. Retail
	// posts this message immediately at the verb site, but since pass two
	// runs entirely before tick 1 (creation, movement and visibility
	// publication all wait on it [08 "Mission-unit creation..."]), applying
	// the pairs here — after every placement's script has run, in the order
	// pass two visited them — is observationally identical: no order queued
	// by any script in this pass depends on an attach's side effects, and the
	// carrier's cargo-list head-link order [R-COB-03 §5] still matches verb
	// order because each pair is recorded exactly once, at its own verb's
	// placement.
	for _, pair := range attachPairs {
		movement.AttachCargoMode(w, pair.carrierHandle, pair.cargoHandle, -1, 0)
	}
}

// interpCtx holds per-unit interpreter state.
type interpCtx struct {
	unit          *units.Unit
	worldUnits    []*units.Unit
	createdSparse []*units.Unit // sparse P0-04/P0-06 created[placementIdx] [P0-04][P0-06]
	identMap      map[string]int
	unitNameMap   map[string]int
	attachPairs   *[]attachPair    // shared accumulator; see attachPair
	catalog       *content.Catalog // production existence/building lookups [04 §3.6]
	queued        int
	suppressTail  bool
	placementIdx  int
}

// missionEntryTick is the creation-tick snapshot every record this interpreter
// queues carries. The interpreter runs ONCE at battle entry, on the loading
// worker, after all mission units exist and before the first simulation tick is
// stepped [04 §3.6][08 R-ENTRY-01 §6], so the tick current at issue is zero.
const missionEntryTick uint32 = 0

// record stamps the two order-record fields that every ordinary issuer writes
// and that a bare `orders.Node{}` literal leaves at zero. [04 §3.6] states that
// from the next tick "the ordinary order pump consumes the pre-loaded queue
// exactly as if a player had issued the orders", so a mission-script record has
// to be the same shape of record as an interface- or AI-issued one; the queue's
// insertion path fills only the descriptor identity and the gate mask, not
// these.
//
//   - Owner is the acting unit's handle. The owning unit is one of the order
//     record's own fields [04 §3.2], a handler body is handed it alongside the
//     record [04 R-ORD-01 §1], and the movement controller's single goal slot is
//     addressed BY it [04 R-ORD-01 §9]. Leaving it null made every
//     mission-issued record, on every unit in the mission, name the same
//     controller slot at handle 0 — measured on MISSION0 (AC01): 23 records at
//     battle entry sharing one bucket, and 24 of 61 installs over 740 ticks
//     evicting a record belonging to a different unit (WU-19-68, WU-19-69). It
//     also left `Queue.ownerUnit` unable to resolve the record's unit at all,
//     which made every owner-side callback a no-op and made the air installer —
//     which refuses an install whose resolved unit does not carry `canfly` —
//     reject every mission-issued VTOL goal outright.
//   - CreationTick is the creation-tick snapshot [04 §3.2], missionEntryTick
//     above.
//
// It writes nothing else: the goal triple, the target smart-reference and the
// three general parameters are the verb handler's [04 §3.6][04 §3.2].
func (ctx *interpCtx) record(n orders.Node) orders.Node {
	if ctx == nil || ctx.unit == nil {
		return n
	}
	n.Owner = ctx.unit.Handle
	n.CreationTick = missionEntryTick
	return n
}

// push queues one front-segment mission-script record on the acting unit's own
// queue and marks the script as having issued, which is what the postlude's
// bit-5 clear and tail `MakeSelectable` test [04 §3.6] C12.
//
// The insertion carries the QUEUED modifier: "each queuing verb resolves its
// order through the descriptor registry canonical-name lookup and appends one
// record in queued mode" [04 §3.6], restated by [04 §3.4] for the spawner's
// positional verbs. The modifier's one consumer is the producer insertion's
// caption-pending bit, which a non-queued (Replace) issue arms and a queued
// (Append / Shift-queue) issue does not [04 R-ORD-01 §13] — so a non-queued
// push made every mission-script record one-shot-armed, and the first handler
// visit to run the shared caption clear spoke the `ok` cue on the viewing
// player's units at the start of every mission with an InitialMission string.
// Nothing else reads the modifier: the record lands in exactly the same place
// it landed before, at the tail or behind the active marker.
//
// pushSecondary needs no counterpart. The rear-segment insertion has no
// caption arm at all, so the modifier has no consumer on that path.
func (ctx *interpCtx) push(id orders.ID, n orders.Node) {
	if ctx == nil || ctx.unit == nil {
		return
	}
	n.QueuedIssue = true
	orders.QueueForUnit(ctx.unit).Push(id, ctx.record(n))
	ctx.queued++
}

// pushSecondary is push for a rear-segment descriptor [04 §3.3].
func (ctx *interpCtx) pushSecondary(id orders.ID, n orders.Node) {
	if ctx == nil || ctx.unit == nil {
		return
	}
	orders.QueueForUnit(ctx.unit).PushSecondary(id, ctx.record(n))
	ctx.queued++
}

// tokenizeScript splits the script into verb tokens on EVERY comma — the
// tokenizer delimiter is the single byte ',' (_strcspn(p,",")), and a token's
// own arguments are SPACE-separated by the scan formats (" %f %f") [04 §3.6];
// decompile notes/campaign/02 §4. A trailing coordinate after an argument
// comma is therefore its own token whose digit lead is silently ignored,
// exactly as retail drops it.
func tokenizeScript(s string) []string {
	// Global clamp for comma-free run >255 as spec-recommended divergence.
	// If script contains no comma and exceeds 255, clamp to 255 [04 §3.6] C13.
	if len(s) > 255 && !strings.Contains(s, ",") {
		s = s[:255] // [04 §3.6] sanctioned bounds exception
	}
	tokens := strings.Split(s, ",")
	// strings.Split always returns at least one element; an empty final token
	// after a trailing comma is preserved for silent handling.
	return tokens
}

// dispatchToken keys on leading letter, effectively case-insensitive [04 §3.6],
// with the uppercase-W quirk [04 §3.6] C11.
func dispatchToken(token string, ctx *interpCtx) {
	if token == "" {
		return
	}
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return
	}
	first := trimmed[0]
	// C11: uppercase-led W… token enters BUILD block, never plain Wait.
	if first == 'W' {
		// BUILD block: second char w selects BuildWeapon.
		if len(trimmed) >= 2 && (trimmed[1] == 'w' || trimmed[1] == 'W') {
			handleBW(trimmed, ctx)
		} else {
			handleB(trimmed, ctx)
		}
		return
	}
	// Normal case-insensitive dispatch [04 §3.6].
	lower := first
	if lower >= 'A' && lower <= 'Z' {
		lower = lower + 'a' - 'A'
	}
	switch lower {
	case 'm':
		handleM(trimmed, ctx)
	case 'a':
		handleA(trimmed, ctx)
	case 'b':
		// Distinguish bw vs b: second char w selects BuildWeapon [04 §3.6].
		if len(trimmed) >= 2 && (trimmed[1] == 'w' || trimmed[1] == 'W') {
			handleBW(trimmed, ctx)
		} else {
			handleB(trimmed, ctx)
		}
	case 'd':
		handleD(trimmed, ctx)
	case 'g':
		handleG(trimmed, ctx)
	case 'i':
		handleI(trimmed, ctx)
	case 'o':
		handleO(trimmed, ctx)
	case 'p':
		handleP(trimmed, ctx)
	case 's':
		handleS(trimmed, ctx)
	case 'u':
		handleU(trimmed, ctx)
	case 'w':
		// Distinguish wa vs w: second char a selects WaitForAttack [04 §3.6].
		// Must be lowercase wa per C11; dispatch here is lowercased first char only,
		// but second char test is case-insensitive for wa? Spec says wa must be lowercase.
		// Our dispatch was already diverted for uppercase W, so reaching here means first char is lowercase w or uppercase W was already handled.
		// For 'w' lower, check second char is 'a' case-insensitive to allow wa lower.
		if len(trimmed) >= 2 && (trimmed[1] == 'a' || trimmed[1] == 'A') {
			// Ensure the token is lowercase-led wa: first char must be lowercase 'w' to be WaitForAttack per C11.
			// Since we are in lower== 'w' case, first char could be 'W' uppercase but that case was already diverted, so this is lowercase w.
			// Also 'Wa' with uppercase A should maybe be? But spec says wa must be lowercase.
			// We accept case-insensitive second char for robustness, but the uppercase-W quirk already prevents 'W'+'a' reaching here.
			handleWA(trimmed, ctx)
		} else {
			handleW(trimmed, ctx)
		}
	default:
		// Unknown letters/digits/punctuation silent ignored scanning resumes past comma [04 §3.6] C13.
		return
	}
}

// Helpers.

func floatToFixed(f float32) numeric.Fixed {
	// Callers parse into single precision before promoting for this exact
	// product. Coordinates scale by 65536 [08 R-ENTRY-01 §6] [04 §3.6] C10.
	// Truncate and sign-extend the stored low word [01 R-DET-01 §1].
	return numeric.Fixed(numeric.TruncateFloat64ToLow32(float64(f) * 65536)) // [04 §3.6] scaled by 65536 [I3] trunc toward zero
}

func timeToTicks(secs float32) int32 {
	// The parsed single-precision seconds are promoted before scaling; no
	// second single-precision rounding intervenes [08 R-ENTRY-01 §6].
	return numeric.TruncateFloat64ToLow32(float64(secs) * 30) // [04 §3.6] times scale by 30, trunc toward zero [I3]
}

// productIdentity resolves an authored unit name to the product identity that
// runtime consumers actually read: the canonical catalog key, and the catalog
// definition index alongside it.
//
// This previously returned an FNV hash of the canonical key. Nothing consumes
// such a value: construction resolves a build node by BuildDefKey first and
// falls back to the catalog index in Param1, so an authored initial build
// order carried an identity that matched neither and could be queued but never
// resolved to a definition. Retail stores a catalog type id resolved at the
// verb site [04 §3.6]; the canonical key is the stable spelling of that same
// identity across catalog changes and save/load, so it is what the node
// carries, with the index kept beside it for the index-keyed fallback.
//
// The index is zero when no catalog is bound; BuildDefKey still carries the
// canonical identity, but no authored type can pass the catalog lookup.
func productIdentity(cat *content.Catalog, defKey string) (string, uint32) {
	ck := content.CanonicalKey(defKey)
	if cat == nil {
		return ck, 0
	}
	idx, _ := cat.UnitDefIndex(ck)
	return ck, idx
}

func typeExists(ctx *interpCtx, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	// Production path: the type exists when the catalog holds it [04 §3.6];
	// retail resolves the authored name by binary search over the catalog.
	if ctx != nil && ctx.catalog != nil {
		_, ok := ctx.catalog.Unit(content.CanonicalKey(name))
		return ok
	}
	// No catalog means no type can be resolved. Production always supplies the
	// immutable catalog; this keeps malformed fixture setup from queuing bogus
	// orders.
	return false
}

// actingUnitIsBuilding is the `b` verb's discriminant. Retail chooses
// BuildingBuild when the ACTING unit's movement record is null and MobileBuild
// at x,y when it has one [04 §3.6 correction 2026-09-02]; the record exists
// exactly for a definition whose `bmcode` is 1 [04 R-COLL-01 §1]. The product
// definition's own name field plays no part (corrected 2026-09-02: this used
// to test the product's UnitName for emptiness, which a catalog hit can never
// satisfy because that name is the catalog key).
func actingUnitIsBuilding(ctx *interpCtx) bool {
	if ctx == nil || ctx.unit == nil || ctx.unit.Def == nil {
		return false
	}
	return ctx.unit.Def.BMCode != 1
}

func (ctx *interpCtx) lookupIdentOrUnitName(name string) int {
	lower := strings.ToLower(strings.TrimSpace(name))
	if idx, ok := ctx.identMap[lower]; ok {
		// Ident match first [P0-06] scans placement order sparse first-occurrence.
		return idx
	}
	if idx, ok := ctx.unitNameMap[lower]; ok {
		return idx
	}
	return -1
}

// sparseUnit returns the unit for sparse placement index or nil.
func (ctx *interpCtx) sparseUnit(placementIdx int) *units.Unit {
	if placementIdx < 0 || placementIdx >= len(ctx.createdSparse) {
		return nil
	}
	return ctx.createdSparse[placementIdx]
}

// Handlers.

func handleM(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[1:]}
	fx, fy := missionCoordinateSeeds()
	scan.float(&fx)
	scan.float(&fy)
	// Positional mission verbs classify through the same resolver as live
	// commands, including capability gates and factory rally descriptors
	// [04 §3.6][04 R-ORD-02 §1].
	id := orders.Resolve(2, ctx.unit, nil, nil)
	if id == 0 {
		return
	}
	node := orders.Node{
		GoalX:        floatToFixed(fx), // [04 §3.6] coordinates×65536 [C10]
		GoalZ:        floatToFixed(fy),
		GoalY:        0, // [08 R-ENTRY-01 §6] the authored position has no altitude
		GoalSupplied: true,
	}
	ctx.push(id, node)
}

func handleA(token string, ctx *interpCtx) {
	args := token[1:]
	scan := missionArgScanner{text: args}
	var fx, fy float32
	// Numeric attack alone requires both coordinates [04 §3.6].
	if scan.float(&fx) && scan.float(&fy) {
		// A rejected positional attack remains rejected; inventing a chase
		// descriptor here bypasses the resolver's weapon and capability gates
		// [04 §3.6][04 R-ORD-02 §1].
		id := orders.Resolve(3, ctx.unit, nil, nil)
		if id == 0 {
			return
		}
		node := orders.Node{
			GoalX:        floatToFixed(fx), // [04 §3.6] coordinates×65536
			GoalZ:        floatToFixed(fy),
			GoalY:        0, // [08 R-ENTRY-01 §6] the authored position has no altitude
			GoalSupplied: true,
		}
		ctx.push(id, node)
		ctx.suppressTail = true // numeric-form a suppresses [04 §3.6] C12
		return
	}
	// By-type form: a name attack-by-unit-type [04 §3.6] C10.
	scan = missionArgScanner{text: args} // Name retry starts at the original argument.
	var name string
	scan.name(&name, true)
	if !typeExists(ctx, name) {
		// Unknown types queue nothing [04 §3.6] C10.
		return
	}
	id := orders.Lookup("AttackUType") // [04 §3.1] AttackUType
	if id == 0 {
		id = orders.Lookup("Attack_Chase")
	}
	if id == 0 {
		return
	}
	// Retired (WU-19-4): this carried an open-question marker saying `AttackUType` had
	// no runtime consumer, so an authored attack-by-type order was admitted and
	// never executed, and that the acquisition rule was unknown. Both halves
	// are closed: [04 R-ORD-01 §3] gives the row (phase 0 `deadline RNG(90)+1`,
	// phase 1 scans every live unit from the second slot on whose definition
	// index equals p1 and whose owner is hostile, scores each `d² − RNG(d²/2)`,
	// keeps the lowest with later slots winning ties, and spawns the resolved
	// code-3 attack at the head), and the scan now runs over the queue
	// binding's live-unit enumerator. p1 below is the catalog index that scan
	// compares against, so the authored name reaches the handler as an
	// identity, not as text.
	ck, idx := productIdentity(ctx.catalog, name)
	node := orders.Node{
		BuildDefKey: ck,  // canonical product identity [04 §3.2][02 §5]
		Param1:      idx, // catalog index fallback [04 §3.2]
	}
	ctx.push(id, node)
	// By-type a does NOT suppress tail [04 §3.6] C12.
}

func handleB(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[1:]}
	var name string
	scan.name(&name, true)
	if !typeExists(ctx, name) {
		return // Unknown types queue nothing [04 §3.6].
	}
	n := int32(1)
	fx, fy := missionCoordinateSeeds()
	scan.integer(&n)
	scan.float(&fx)
	scan.float(&fy)
	// BuildingBuild when the acting unit has no mover (a structure), MobileBuild
	// at x,y when it has one [04 §3.6 correction 2026-09-02] C10. The presence
	// of coordinates plays no part in the choice.
	var id orders.ID
	building := actingUnitIsBuilding(ctx)
	if building {
		id = orders.Lookup("BuildingBuild")
	} else {
		id = orders.Lookup("MobileBuild")
	}
	if id == 0 {
		return
	}
	ck, idx := productIdentity(ctx.catalog, name)
	node := orders.Node{
		BuildDefKey: ck,        // canonical product identity construction resolves first [02 §5]
		Param1:      idx,       // catalog index fallback [04 §3.2]
		Param2:      uint32(n), // count n [04 §3.6]
	}
	// BuildingBuild supplies no position, so its entire goal stays zero and the
	// record is constructed with NO goal; only MobileBuild receives the authored
	// X/Z, and states that it supplied one so the constructor keeps static bit
	// 10 [08 R-ENTRY-01 §6][04 R-MOV-03 §7].
	if !building {
		node.GoalX = floatToFixed(fx)
		node.GoalZ = floatToFixed(fy)
		node.GoalSupplied = true
	}
	ctx.push(id, node)
}

func handleBW(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[2:]}
	n := int32(1) // Failed conversion retains the caller seed [04 §3.6].
	scan.integer(&n)
	id := orders.Lookup("BuildWeapon") // [04 §3.1] rear segment [C10]
	if id == 0 {
		return
	}
	// `bw n` carries a count and no slot [04 §3.6], so the node's build-type
	// argument is zero — slot 0, where shipped stockpile weapons live
	// [06 §11.1] — and the handler selects that slot verbatim, testing neither
	// that its weapon carries `stockpile` nor that it is a real weapon
	// [06 R-WPN-05 §2]. On a definition whose slot 0 holds weapon record 0 the
	// queue runs away: every round completes free in a single visit, and a
	// node that outlives the visit faults the build page's percentage on a
	// divide by zero. Refusing to enqueue is the one behavior both safe and
	// indistinguishable from retail on shipped content, where a mission
	// authors `bw` only for a silo. An unqueued verb is the same outcome the
	// table already gives an unresolved `a name` or `g name`.
	const bwSlot = 0 // [04 §3.6] the verb names no slot; the node's is zero
	if !orders.StockpileSlotAcceptsBuildWeapon(ctx.unit, bwSlot) {
		return
	}
	node := orders.Node{
		Param1: bwSlot,    // slot 0 [06 §11.1]
		Param2: uint32(n), // count n [04 §3.6] bw n
	}
	ctx.pushSecondary(id, node) // secondary [04 §3.3]
}

func handleD(token string, ctx *interpCtx) {
	_ = token
	id := orders.Lookup("SelfDestructFG") // [04 §3.6] d uses front-gate descriptor
	if id == 0 {
		id = orders.Lookup("SelfDestruct")
	}
	if id == 0 {
		return
	}
	ctx.push(id, orders.Node{})
	ctx.suppressTail = true // d suppresses [04 §3.6] C12
}

func handleG(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[1:]}
	var name string
	if !scan.name(&name, true) {
		return
	}
	idx := ctx.lookupIdentOrUnitName(name)
	if idx < 0 {
		return // unresolved names queue nothing [P0-06] C10, uses sparse first-occurrence
	}
	target := ctx.sparseUnit(idx)
	if target == nil {
		return
	}
	// Preserve the canonical command admission and variant [04 §3.6].
	id := orders.Resolve(7, ctx.unit, target, nil)
	if id == 0 {
		return
	}
	node := orders.Node{
		Target: target.Handle,
	}
	ctx.push(id, node)
}

func handleI(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[1:]}
	var name string
	if !scan.name(&name, true) {
		return
	}
	idx := ctx.lookupIdentOrUnitName(name)
	if idx < 0 {
		return
	}
	target := ctx.sparseUnit(idx)
	if target == nil {
		return
	}
	// Immediate internal attach message, not a queued order [04 §3.6] "i name";
	// [08 "Argument parsing..."] "the immediate attach verb posts an internal
	// attach without queuing". Record the request now; applied after pass
	// two's per-unit script loops finish (see RunInitialMissionsWithCatalog).
	if ctx.attachPairs != nil && ctx.placementIdx >= 0 {
		*ctx.attachPairs = append(*ctx.attachPairs, attachPair{
			cargoHandle:   ctx.unit.Handle, // the acting unit boards itself [04 §3.6]
			carrierHandle: target.Handle,   // the named carrier, already resolved
		})
	}
}

func handleO(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[1:]}
	// Failed fields retain current standing state; the first failure also
	// prevents later assignment [04 §3.6].
	const fieldMask = int32(units.StandingFieldMask)
	d1 := int32(ctx.unit.Flags>>units.StandingMoveShift) & fieldMask
	d2 := int32(ctx.unit.Flags>>units.StandingFireShift) & fieldMask
	scan.integer(&d1)
	scan.integer(&d2)
	// Writes the two standing-order fields of the unit state word: the standing
	// move field (bits 18-19) takes d1&3 and the standing fire field (bits
	// 20-21) takes d2&3 [04 §3.6 correction 2026-09-02][P0-06] — the same bits
	// the COB ports 2 and 3 read [04 §4.4] and the factory copies onto a
	// product [04 §3.8]. (The §3.6 table row used to say 17-18 / 19-20; the
	// handler's clear mask settled it as off by one.)
	ctx.unit.Flags &^= (units.StandingFieldMask << units.StandingMoveShift) | (units.StandingFieldMask << units.StandingFireShift)
	ctx.unit.Flags |= (uint32(d1) & units.StandingFieldMask) << units.StandingMoveShift
	ctx.unit.Flags |= (uint32(d2) & units.StandingFieldMask) << units.StandingFireShift
	// No order queued and no issued marker [04 §3.6] C10.
}

func handleP(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[1:]}
	fx, fy := missionCoordinateSeeds()
	var ft float32 // Patrol timeout is initialized to zero [04 §3.6].
	scan.float(&fx)
	scan.float(&fy)
	scan.float(&ft)
	ticks := timeToTicks(ft) // [04 §3.6] times×30 [C10]
	// Preserve the canonical command admission and variant [04 §3.6].
	id := orders.Resolve(9, ctx.unit, nil, nil)
	if id == 0 {
		return
	}
	node := orders.Node{
		GoalX:        floatToFixed(fx), // [04 §3.6] coordinates×65536
		GoalZ:        floatToFixed(fy),
		GoalY:        0,             // [08 R-ENTRY-01 §6] the authored position has no altitude
		Param1:       uint32(ticks), // timeout ticks [04 §3.6] C10
		GoalSupplied: true,
	}
	ctx.push(id, node)
	ctx.suppressTail = true // p suppresses [04 §3.6] C12
}

func handleS(token string, ctx *interpCtx) {
	_ = token
	id := orders.Lookup("MakeSelectable") // [04 §3.1]
	if id == 0 {
		return
	}
	ctx.push(id, orders.Node{}) // zero auxiliary args [04 §3.6] C10
	ctx.suppressTail = true     // s suppresses [04 §3.6] C12
}

func handleU(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[1:]}
	fx, fy := missionCoordinateSeeds()
	scan.float(&fx)
	scan.float(&fy)
	// Preserve the canonical command admission and variant [04 §3.6].
	id := orders.Resolve(5, ctx.unit, nil, nil)
	if id == 0 {
		return
	}
	node := orders.Node{
		GoalX:        floatToFixed(fx), // [04 §3.6] coordinates×65536
		GoalZ:        floatToFixed(fy),
		GoalY:        0, // [08 R-ENTRY-01 §6] the authored position has no altitude
		GoalSupplied: true,
	}
	ctx.push(id, node)
}

func handleW(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[1:]}
	var secs float32
	var trailing int32 // Both wait operands start at zero [04 §3.6].
	scan.float(&secs)
	scan.integer(&trailing)
	ticks := timeToTicks(secs)  // [04 §3.6] times×30
	id := orders.Lookup("Wait") // [04 §3.1]
	if id == 0 {
		return
	}
	node := orders.Node{
		Param1: uint32(ticks),    // secs×30 [04 §3.6] C10
		Param2: uint32(trailing), // trailing n [04 §3.6] w secs[,n]
	}
	ctx.push(id, node)
}

func handleWA(token string, ctx *interpCtx) {
	scan := missionArgScanner{text: token[2:]}
	var name string
	var targetHandle int
	found := false
	// The wait-for-attack scanset omits underscore [08 "Argument parsing"].
	if scan.name(&name, false) {
		if idx := ctx.lookupIdentOrUnitName(name); idx >= 0 {
			if u := ctx.sparseUnit(idx); u != nil {
				targetHandle = int(u.Handle)
				found = true
			}
		}
	}
	if !found {
		// Fallback to self when unresolved or sscanf !=1 [P0-06] C13.
		targetHandle = int(ctx.unit.Handle)
	}
	id := orders.Lookup("WaitForAttack") // [04 §3.1]
	if id == 0 {
		return
	}
	node := orders.Node{
		Target: pool.Handle(targetHandle),
	}
	ctx.push(id, node)
}
