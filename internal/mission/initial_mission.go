package mission

import (
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// UnitTypeExistsHook overrides catalog existence check for tests.
// When nil, every name is considered known. Tests set this to simulate
// unknown types for `a name` and `b name` no-ops [04 §3.6] C13.
var UnitTypeExistsHook func(name string) bool

// IsBuildingTypeHook overrides building-vs-mobile classification for `b`.
// When nil, mobile build is chosen when coordinates are present, otherwise building.
// Tests may set to force either path [04 §3.6].
var IsBuildingTypeHook func(name string) bool

// RunInitialMissions interprets InitialMission strings once after all mission
// units exist, for mission type 1 and BetweenMissions restores only.
// It registers with no tick dispatcher; from the next tick the ordinary pump
// consumes queued orders [04 §3.6] C9.
func RunInitialMissions(m *Mission, w *units.World) {
	RunInitialMissionsWithCatalog(m, w, nil)
}

// RunInitialMissionsWithCatalog is RunInitialMissions with the content
// catalog backing type-existence and building-vs-mobile lookups [04 §3.6].
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// We reconstruct sparse by placement index stored on the unit.
	// createdSparse[i] is the unit for placement i or nil if allocation failed.
	createdSparse := make([]*units.Unit, len(m.Units))
	// Build index from world units by PlacementIdx.
	mapped := 0
	for _, u := range w.Iter() {
		if u == nil {
			continue
		}
		if u.PlacementIdx >= 0 && u.PlacementIdx < len(createdSparse) {
			createdSparse[u.PlacementIdx] = u
			mapped++
		}
	}
	// Fallback for legacy fixtures that create world units without
	// placement linkage (PlacementIdx==-1). If no placement-indexed units
	// were found but world size matches placements, assume dense order for
	// test compatibility [P0-06] (retail sparse path would have PlacementIdx).
	if mapped == 0 && len(w.Iter()) == len(m.Units) && len(w.Iter()) > 0 {
		ws := w.Iter()
		for i := range m.Units {
			if i < len(ws) {
				createdSparse[i] = ws[i]
			}
		}
	}
	if len(m.Units) == 0 {
		return
	}
	// Build ident->placementIdx and unitname->placementIdx maps for g/wa lookups
	// via sparse scan in placement order 0..count-1, first-occurrence wins,
	// skipping NULL gaps [P0-06][P0-04] A27. Retail scans created[] directly.
	identMap := make(map[string]int)    // lower(Ident) -> placementIdx
	unitNameMap := make(map[string]int) // lower(UnitName) -> placementIdx
	for i, pl := range m.Units {
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
	// Per-unit attach storage for i-verb immediate attach (not a queued order) [04 §3.6].
	attachMap := make(map[int]int) // placementIdx -> target placementIdx

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	for idx, placement := range m.Units {
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
			attachMap:     attachMap,
			mission:       m,
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
			u.Flags &^= 1 << 5 // [04 §3.6] bit 5
			if !ctx.suppressTail {
				id := orders.Lookup("MakeSelectable") // [04 §3.6] C12 zero aux args
				if id != 0 {
					q := orders.QueueForUnit(u)
					q.Push(id, orders.Node{}) // zero auxiliary arguments [04 §3.6]
				}
			}
		}
		_ = attachMap
	}
}

// interpCtx holds per-unit interpreter state.
type interpCtx struct {
	unit          *units.Unit
	worldUnits    []*units.Unit
	createdSparse []*units.Unit // sparse P0-04/P0-06 created[placementIdx] [P0-04][P0-06]
	identMap      map[string]int
	unitNameMap   map[string]int
	attachMap     map[int]int
	mission       *Mission
	catalog       *content.Catalog // production existence/building lookups [04 §3.6]; may be nil in fixtures with hooks
	queued        int
	suppressTail  bool
	placementIdx  int
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

func isLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
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

func splitArgs(s string) []string {
	// Replace commas with spaces, then fields. This handles both comma and whitespace separators.
	replaced := strings.ReplaceAll(s, ",", " ")
	fields := strings.Fields(replaced)
	return fields
}

func floatToFixed(f float64) numeric.Fixed {
	// Coordinates parse as floats scaled by 65536; times scale by 30 [04 §3.6] C10.
	// Truncate toward zero via int64 conversion [01 §8] I3.
	return numeric.Fixed(int64(f * 65536)) // [04 §3.6] scaled by 65536 [I3] trunc toward zero
}

func timeToTicks(secs float64) int32 {
	return int32(secs * 30) // [04 §3.6] times scale by 30, trunc toward zero [I3]
}

// productID derives the order payload's product identity for `b`. Retail
// stores a u16 catalog TYPE id resolved by binary search at the verb site
// [04 §3.6]; the compiled catalog carries no numeric unit id yet.
// TODO(question): plumb content's numeric unit identity and replace this
// hash; until then it is a deterministic stand-in keyed on the canonical key.
func productID(defKey string) uint32 {
	ck := content.CanonicalKey(defKey)
	h := fnv.New32a()
	_, _ = h.Write([]byte(ck))
	return h.Sum32()
}

func typeExists(ctx *interpCtx, name string) bool {
	if UnitTypeExistsHook != nil {
		return UnitTypeExistsHook(name)
	}
	if strings.TrimSpace(name) == "" {
		return false
	}
	// Production path: the type exists when the catalog holds it [04 §3.6]
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if ctx != nil && ctx.catalog != nil {
		_, ok := ctx.catalog.Unit(content.CanonicalKey(name))
		return ok
	}
	// No catalog and no hook: unknown. Retail would consult its catalog, so
	// defaulting to true queued bogus orders for unknown types.
	return false
}

func isBuildingType(ctx *interpCtx, name string) bool {
	if IsBuildingTypeHook != nil {
		return IsBuildingTypeHook(name)
	}
	// `b` builds BuildingBuild when the found catalog entry's unit-name field
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): the phase-2 compiler falls back UnitName to the base
	// name when the FBI omits `unitname`, which masks authored-empty entries;
	// the fallback site needs a raw-presence flag for this to be exact.
	if ctx != nil && ctx.catalog != nil {
		if def, ok := ctx.catalog.Unit(content.CanonicalKey(name)); ok && def != nil {
			return def.UnitName == ""
		}
		return false
	}
	return false
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

func (ctx *interpCtx) targetHandleForName(name string) (handle int, found bool) {
	idx := ctx.lookupIdentOrUnitName(name)
	if idx < 0 || idx >= len(ctx.createdSparse) {
		return 0, false
	}
	u := ctx.createdSparse[idx]
	if u == nil {
		return 0, false
	}
	return int(u.Handle), true
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
	rest := ""
	if len(token) >= 1 {
		rest = token[1:]
	}
	fields := splitArgs(rest)
	var fx, fy float64
	if len(fields) >= 1 {
		if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
			fx = v
		}
	}
	if len(fields) >= 2 {
		if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
			fy = v
		}
	}
	// Do not test conversion counts [04 §3.6] C13: still queue even if parse failed (we use 0).
	id := orders.Lookup("Move_Ground")
	if ctx.unit.Def != nil && ctx.unit.Def.CanFly {
		if vid := orders.Lookup("VTOL_Move"); vid != 0 {
			id = vid
		}
	}
	// Fallback via resolver for exact variant [04 §3.4] via orders.Resolve? Use Lookup result.
	if id == 0 {
		// Try resolver path: move gate can-move [04 §3.4] code 2.
		id = orders.Resolve(2, ctx.unit, nil, nil)
		if id == 0 {
			return
		}
	}
	node := orders.Node{
		GoalX: floatToFixed(fx), // [04 §3.6] coordinates×65536 [C10]
		GoalZ: floatToFixed(fy),
		GoalY: ctx.unit.Y,
	}
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, node)
	ctx.queued++
}

func handleA(token string, ctx *interpCtx) {
	rest := ""
	if len(token) >= 1 {
		rest = token[1:]
	}
	fields := splitArgs(rest)
	if len(fields) == 0 {
		return
	}
	// Try numeric detection: two floats -> numeric attack ground position [04 §3.6] C10.
	isNumeric := false
	if len(fields) >= 2 {
		_, err1 := strconv.ParseFloat(fields[0], 64)
		_, err2 := strconv.ParseFloat(fields[1], 64)
		if err1 == nil && err2 == nil {
			isNumeric = true
		}
	}
	if isNumeric {
		// Numeric form: a x,y attack ground position suppresses tail [04 §3.6] C12.
		var fx, fy float64
		fx, _ = strconv.ParseFloat(fields[0], 64)
		fy, _ = strconv.ParseFloat(fields[1], 64)
		// Resolve attack ground position. Spec: positional verbs classify via resolver [04 §3.4].
		// Use generic attack order: Attack_Chase or Attack_NoMove etc via resolver.
		// For ground position attack, we queue an attack order with goal.
		// Choose descriptor via resolver code 3 fallback; if resolver fails (needs target), fallback to Attack_Chase.
		id := orders.Resolve(3, ctx.unit, nil, nil)
		if id == 0 {
			id = orders.Lookup("Attack_Chase")
			if id == 0 {
				id = orders.Lookup("AttackUType")
			}
		}
		if id == 0 {
			return
		}
		node := orders.Node{
			GoalX: floatToFixed(fx), // [04 §3.6] coordinates×65536
			GoalZ: floatToFixed(fy),
			GoalY: ctx.unit.Y,
		}
		q := orders.QueueForUnit(ctx.unit)
		q.Push(id, node)
		ctx.queued++
		ctx.suppressTail = true // numeric-form a suppresses [04 §3.6] C12
		return
	}
	// By-type form: a name attack-by-unit-type [04 §3.6] C10.
	name := fields[0]
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
	pid := productID(name)
	node := orders.Node{
		Param1: uint32(pid), // product type identity [04 §3.2]
	}
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, node)
	ctx.queued++
	// By-type a does NOT suppress tail [04 §3.6] C12.
}

func handleB(token string, ctx *interpCtx) {
	var rest string
	if len(token) >= 1 {
		if token[0] == 'W' {
			rest = token[1:]
		} else {
			rest = token[1:]
		}
	}
	rest = strings.TrimSpace(rest)
	fields := splitArgs(rest)
	if len(fields) == 0 {
		return
	}
	name := fields[0]
	if !typeExists(ctx, name) {
		return // unknown types queue nothing (silent) [04 §3.6] C13
	}
	var n int64 = 1
	var fx, fy float64
	if len(fields) >= 2 {
		if v, err := strconv.ParseInt(fields[1], 10, 32); err == nil {
			n = v
		} else if fv, err2 := strconv.ParseFloat(fields[1], 64); err2 == nil {
			n = int64(fv)
		}
	}
	if len(fields) >= 3 {
		fx, _ = strconv.ParseFloat(fields[2], 64)
	}
	if len(fields) >= 4 {
		fy, _ = strconv.ParseFloat(fields[3], 64)
	}
	// Building build when catalog type has empty unit name, mobile build at x,y otherwise [04 §3.6] C10.
	var id orders.ID
	if isBuildingType(ctx, name) {
		id = orders.Lookup("BuildingBuild")
		if id == 0 {
			id = orders.Lookup("MobileBuild")
		}
	} else {
		// Default to mobile build when coordinates present or not building type.
		// If fields indicate no coordinates, still mobile? Spec uses presence of building flag, not coords.
		// Fallback to MobileBuild then BuildingBuild.
		id = orders.Lookup("MobileBuild")
		if id == 0 {
			id = orders.Lookup("BuildingBuild")
		}
		// If isBuildingType false but no coords, we still choose MobileBuild? Keep per spec.
	}
	if id == 0 {
		return
	}
	pid := productID(name)
	node := orders.Node{
		Param1: uint32(pid),
		Param2: uint32(n), // count n [04 §3.6]
		GoalX:  floatToFixed(fx),
		GoalZ:  floatToFixed(fy),
		GoalY:  ctx.unit.Y,
	}
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, node)
	ctx.queued++
}

func handleBW(token string, ctx *interpCtx) {
	var rest string
	if len(token) >= 2 {
		// token starts with "bw", "BW", "Ww", "Ww" etc.
		if token[0] == 'W' && (token[1] == 'w' || token[1] == 'W') {
			rest = token[2:]
		} else if (token[0] == 'b' || token[0] == 'B') && (token[1] == 'w' || token[1] == 'W') {
			rest = token[2:]
		} else {
			rest = token[1:]
		}
	} else if len(token) >= 1 {
		rest = token[1:]
	}
	rest = strings.TrimSpace(rest)
	fields := splitArgs(rest)
	var n int64
	if len(fields) >= 1 {
		if v, err := strconv.ParseInt(fields[0], 10, 32); err == nil {
			n = v
		} else if fv, err2 := strconv.ParseFloat(fields[0], 64); err2 == nil {
			n = int64(fv) // [04 §3.6] move/… don't test counts but bw maybe? Still parse.
		}
	} else {
		n = 1 // default?
	}
	id := orders.Lookup("BuildWeapon") // [04 §3.1] rear segment [C10]
	if id == 0 {
		return
	}
	node := orders.Node{
		Param2: uint32(n), // count n [04 §3.6] bw n
	}
	q := orders.QueueForUnit(ctx.unit)
	q.PushSecondary(id, node) // secondary [04 §3.3]
	ctx.queued++
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
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, orders.Node{})
	ctx.queued++
	ctx.suppressTail = true // d suppresses [04 §3.6] C12
}

func handleG(token string, ctx *interpCtx) {
	rest := ""
	if len(token) >= 1 {
		rest = token[1:]
	}
	rest = strings.TrimSpace(rest)
	fields := splitArgs(rest)
	if len(fields) == 0 {
		return
	}
	name := fields[0]
	idx := ctx.lookupIdentOrUnitName(name)
	if idx < 0 {
		return // unresolved names queue nothing [P0-06] C10, uses sparse first-occurrence
	}
	target := ctx.sparseUnit(idx)
	if target == nil {
		return
	}
	var id orders.ID
	if ctx.unit.Def != nil && ctx.unit.Def.CanFly {
		id = orders.Lookup("VTOL_Follow")
		if id == 0 {
			id = orders.Lookup("Follow_Ground")
		}
	} else {
		id = orders.Lookup("Follow_Ground")
		if id == 0 {
			id = orders.Lookup("VTOL_Follow")
		}
	}
	if id == 0 {
		// Fallback via resolver code 7.
		id = orders.Resolve(7, ctx.unit, target, nil)
		if id == 0 {
			return
		}
	}
	node := orders.Node{
		Target: target.Handle,
	}
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, node)
	ctx.queued++
}

func handleI(token string, ctx *interpCtx) {
	rest := ""
	if len(token) >= 1 {
		rest = token[1:]
	}
	rest = strings.TrimSpace(rest)
	fields := splitArgs(rest)
	if len(fields) == 0 {
		return
	}
	name := fields[0]
	idx := ctx.lookupIdentOrUnitName(name)
	if idx < 0 {
		return
	}
	target := ctx.sparseUnit(idx)
	if target == nil {
		return
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if ctx.attachMap != nil {
		curIdx := ctx.placementIdx
		if curIdx >= 0 {
			ctx.attachMap[curIdx] = idx
		}
	}
	_ = target
}

func handleO(token string, ctx *interpCtx) {
	rest := ""
	if len(token) >= 1 {
		rest = token[1:]
	}
	rest = strings.TrimSpace(rest)
	fields := splitArgs(rest)
	var d1, d2 int64
	if len(fields) >= 1 {
		if v, err := strconv.ParseInt(fields[0], 10, 32); err == nil {
			d1 = v
		} else if fv, err2 := strconv.ParseFloat(fields[0], 64); err2 == nil {
			d1 = int64(fv) // [04 §3.6] flag tokens don't test conversion counts
		}
	}
	if len(fields) >= 2 {
		if v, err := strconv.ParseInt(fields[1], 10, 32); err == nil {
			d2 = v
		} else if fv, err2 := strconv.ParseFloat(fields[1], 64); err2 == nil {
			d2 = int64(fv)
		}
	}
	// Writes two 2-bit fields of the unit flag word: bits 18-19 ← d1 &3 and
	// bits 20-21 ← d2 &3 per mask 0xffc3ffff [04 §3.6] [P0-06].
	// TODO(question): o-verb bits 17-20 vs 18-21 conflict; using 18-21 per mask 0xffc3ffff
	ctx.unit.Flags &^= (0x3 << 18) | (0x3 << 20)         // mask 0xffc3ffff => bits 18-21
	ctx.unit.Flags |= uint32(((d2&3)<<2)|(d1&3)) << 0x12 // shift 0x12 = 18 [P0-06]
	// No order queued and no issued marker [04 §3.6] C10.
}

func handleP(token string, ctx *interpCtx) {
	rest := ""
	if len(token) >= 1 {
		rest = token[1:]
	}
	rest = strings.TrimSpace(rest)
	fields := splitArgs(rest)
	var fx, fy, ft float64
	if len(fields) >= 1 {
		fx, _ = strconv.ParseFloat(fields[0], 64) // [04 §3.6] don't test conversion counts
	}
	if len(fields) >= 2 {
		fy, _ = strconv.ParseFloat(fields[1], 64)
	}
	if len(fields) >= 3 {
		ft, _ = strconv.ParseFloat(fields[2], 64)
	}
	ticks := timeToTicks(ft) // [04 §3.6] times×30 [C10]
	var id orders.ID
	if ctx.unit.Def != nil && ctx.unit.Def.CanFly {
		id = orders.Lookup("VTOL_Patrol")
		if id == 0 {
			id = orders.Lookup("Patrol")
		}
	} else {
		id = orders.Lookup("Patrol")
		if id == 0 {
			id = orders.Lookup("VTOL_Patrol")
		}
	}
	if id == 0 {
		id = orders.Resolve(9, ctx.unit, nil, nil)
		if id == 0 {
			return
		}
	}
	node := orders.Node{
		GoalX:  floatToFixed(fx), // [04 §3.6] coordinates×65536
		GoalZ:  floatToFixed(fy),
		GoalY:  ctx.unit.Y,
		Param1: uint32(ticks), // timeout ticks [04 §3.6] C10
	}
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, node)
	ctx.queued++
	ctx.suppressTail = true // p suppresses [04 §3.6] C12
}

func handleS(token string, ctx *interpCtx) {
	_ = token
	id := orders.Lookup("MakeSelectable") // [04 §3.1]
	if id == 0 {
		return
	}
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, orders.Node{}) // zero auxiliary args [04 §3.6] C10
	ctx.queued++
	ctx.suppressTail = true // s suppresses [04 §3.6] C12
}

func handleU(token string, ctx *interpCtx) {
	rest := ""
	if len(token) >= 1 {
		rest = token[1:]
	}
	rest = strings.TrimSpace(rest)
	fields := splitArgs(rest)
	var fx, fy float64
	if len(fields) >= 1 {
		fx, _ = strconv.ParseFloat(fields[0], 64)
	}
	if len(fields) >= 2 {
		fy, _ = strconv.ParseFloat(fields[1], 64)
	}
	var id orders.ID
	if ctx.unit.Def != nil && ctx.unit.Def.CanFly {
		id = orders.Lookup("VTOL_Unload")
		if id == 0 {
			id = orders.Lookup("Ground_Unload")
		}
	} else {
		id = orders.Lookup("Ground_Unload")
		if id == 0 {
			id = orders.Lookup("VTOL_Unload")
		}
	}
	if id == 0 {
		id = orders.Resolve(5, ctx.unit, nil, nil)
		if id == 0 {
			return
		}
	}
	node := orders.Node{
		GoalX: floatToFixed(fx), // [04 §3.6] coordinates×65536
		GoalZ: floatToFixed(fy),
		GoalY: ctx.unit.Y,
	}
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, node)
	ctx.queued++
}

func handleW(token string, ctx *interpCtx) {
	rest := ""
	if len(token) >= 1 {
		rest = token[1:]
	}
	rest = strings.TrimSpace(rest)
	fields := splitArgs(rest)
	var secs float64
	var trailing int64
	if len(fields) >= 1 {
		if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
			secs = v
		} else {
			secs = 0 // [04 §3.6] don't test conversion counts
		}
	}
	if len(fields) >= 2 {
		if v, err := strconv.ParseInt(fields[1], 10, 32); err == nil {
			trailing = v
		} else if fv, err2 := strconv.ParseFloat(fields[1], 64); err2 == nil {
			trailing = int64(fv)
		}
	}
	ticks := timeToTicks(secs)  // [04 §3.6] times×30
	id := orders.Lookup("Wait") // [04 §3.1]
	if id == 0 {
		return
	}
	node := orders.Node{
		Param1: uint32(ticks),    // secs×30 [04 §3.6] C10
		Param2: uint32(trailing), // trailing n [04 §3.6] w secs[,n]
	}
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, node)
	ctx.queued++
}

func handleWA(token string, ctx *interpCtx) {
	var rest string
	if len(token) >= 2 {
		rest = token[2:]
	} else {
		rest = ""
	}
	rest = strings.TrimSpace(rest)
	// P0-06: only wa tests sscanf ==1 (name form). Retail scanset " %[a-zA-Z0-9.]"
	// (no underscore) [P0-06 §2]. We mimic by checking first field existence and
	// treating missing/empty as sscanf 0 → fallback to self.
	fields := splitArgs(rest)
	var targetHandle int
	found := false
	if len(fields) >= 1 {
		name := fields[0]
		// sscanf for wa would be one name; test ==1 else self. Our fields length
		// check mirrors that: presence of name token counts as ==1, but we also
		// verify lookup succeeds. Retail would fallback to self if lookup fails
		// as well [P0-06].
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
	q := orders.QueueForUnit(ctx.unit)
	q.Push(id, node)
	ctx.queued++
}
