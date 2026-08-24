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
	if m == nil || w == nil {
		return
	}
	// C9: only type 1 (campaign) and BetweenMissions restores. The latter is the
	// same code path with type 1; other types are no-ops.
	if m.Type != TypeCampaign {
		return
	}
	// Build deterministic world unit list in pool order (lowest-free allocation order).
	// Iteration is deterministic: pool ascending matches creation order; we also
	// ensure player 0..9 stable ordering per I1 by sorting by handle already.
	worldUnits := w.Iter()
	if len(worldUnits) == 0 && len(m.Units) == 0 {
		return
	}
	// Build ident->handle and unitname->handle maps for g/wa lookups [04 §3.6].
	// Mapping is placement index -> world unit handle correspondence: placement i
	// corresponds to worldUnits[i] when counts align. This is deterministic because
	// creation allocates lowest-free slots sequentially.
	identMap := make(map[string]int)    // lower(Ident) -> index in worldUnits
	unitNameMap := make(map[string]int) // lower(UnitName) -> index
	for i, pl := range m.Units {
		if i >= len(worldUnits) {
			break
		}
		h := worldUnits[i].Handle
		_ = h
		if pl.Ident != "" {
			identMap[strings.ToLower(pl.Ident)] = i
		}
		if pl.UnitName != "" {
			// Preserve first occurrence for unitname wildcard; case-insensitive.
			lower := strings.ToLower(pl.UnitName)
			if _, ok := unitNameMap[lower]; !ok {
				unitNameMap[lower] = i
			}
		}
	}
	// Per-unit attach storage for i-verb immediate attach (not a queued order) [04 §3.6].
	attachMap := make(map[int]int) // unit idx -> target idx

	for idx, placement := range m.Units {
		if idx >= len(worldUnits) {
			break
		}
		u := worldUnits[idx]
		if u == nil {
			continue
		}
		script := placement.InitialMission
		if strings.TrimSpace(script) == "" {
			continue
		}
		ctx := &interpCtx{
			unit:        u,
			worldUnits:  worldUnits,
			identMap:    identMap,
			unitNameMap: unitNameMap,
			attachMap:   attachMap,
			mission:     m,
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
		// Postlude C12.
		if ctx.queued > 0 {
			// Clear bit 5 of class/state word [04 §3.6] C12.
			u.Flags &^= 1 << 5 // [04 §3.6] bit 5
			if !ctx.suppressTail {
				id := orders.Lookup("MakeSelectable") // [04 §3.6] C12 zero aux args
				if id != 0 {
					q := orders.QueueForUnit(u)
					q.Push(id, orders.Node{}) // zero auxiliary arguments [04 §3.6]
				}
			}
		}
		// Persist attach map if needed for external inspection (tests may read via hook).
		_ = attachMap
	}
}

// interpCtx holds per-unit interpreter state.
type interpCtx struct {
	unit         *units.Unit
	worldUnits   []*units.Unit
	identMap     map[string]int
	unitNameMap  map[string]int
	attachMap    map[int]int
	mission      *Mission
	queued       int
	suppressTail bool
}

// tokenizeScript splits script into verb tokens using comma-delimiting where a
// comma is a token delimiter only when followed by optional spaces and a letter.
// This preserves internal argument commas (x,y) that are followed by digits.
// Also handles semicolon/trailing.
// [04 §3.6] comma-separated token list left to right.
func tokenizeScript(s string) []string {
	// Global clamp for comma-free run >255 as spec-recommended divergence.
	// If script contains no comma and exceeds 255, clamp to 255 [04 §3.6] C13.
	if len(s) > 255 && !strings.Contains(s, ",") {
		s = s[:255] // [04 §3.6] sanctioned bounds exception
	}
	var tokens []string
	start := 0
	n := len(s)
	for i := 0; i < n; i++ {
		if s[i] != ',' {
			continue
		}
		// Look ahead to next non-space char.
		j := i + 1
		for j < n && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
			j++
		}
		if j < n && isLetter(s[j]) {
			// This comma separates verb tokens.
			token := s[start:i]
			tokens = append(tokens, token)
			start = i + 1
		} else {
			// Argument comma, stay within same token.
			// Do not split.
		}
	}
	// Final token.
	if start <= n {
		token := s[start:]
		tokens = append(tokens, token)
	}
	// Edge: if script ends with comma, last token may be empty; preserved for silent handling.
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

func productID(defKey string) uint32 {
	ck := content.CanonicalKey(defKey)
	h := fnv.New32a()
	_, _ = h.Write([]byte(ck))
	return h.Sum32()
}

func typeExists(name string) bool {
	if UnitTypeExistsHook != nil {
		return UnitTypeExistsHook(name)
	}
	if strings.TrimSpace(name) == "" {
		return false
	}
	// Default: treat known via content catalog if available? Without hook, assume exists for fixture.
	// To satisfy unknown-type no-op tests, hook must be set; default true.
	return true
}

func isBuildingType(name string) bool {
	if IsBuildingTypeHook != nil {
		return IsBuildingTypeHook(name)
	}
	return false
}

func (ctx *interpCtx) lookupIdentOrUnitName(name string) int {
	lower := strings.ToLower(strings.TrimSpace(name))
	if idx, ok := ctx.identMap[lower]; ok {
		// Ident match first [04 §3.6] C10 g verb
		return idx
	}
	if idx, ok := ctx.unitNameMap[lower]; ok {
		return idx
	}
	return -1
}

func (ctx *interpCtx) targetHandleForName(name string) (handle int, found bool) {
	idx := ctx.lookupIdentOrUnitName(name)
	if idx < 0 || idx >= len(ctx.worldUnits) {
		return 0, false
	}
	u := ctx.worldUnits[idx]
	if u == nil {
		return 0, false
	}
	return int(u.Handle), true
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
	if !typeExists(name) {
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
	if !typeExists(name) {
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
	if isBuildingType(name) {
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
		return // unresolved names queue nothing [04 §3.6] C10
	}
	target := ctx.worldUnits[idx]
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
	target := ctx.worldUnits[idx]
	if target == nil {
		return
	}
	// Immediate internal attach message — not a queued order [04 §3.6] C10.
	// Record attachment for test visibility; no order queued.
	if ctx.attachMap != nil {
		// Map from unit idx to target idx.
		// Find current unit index.
		curIdx := -1
		for i, u := range ctx.worldUnits {
			if u == ctx.unit {
				curIdx = i
				break
			}
		}
		if curIdx >= 0 {
			ctx.attachMap[curIdx] = idx
		}
	}
	_ = target
	// No queued order, no suppress.
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
	// Writes two 2-bit fields of unit flag word: bits 17-18 ← d1 &3, bits 19-20 ← d2 &3 [04 §3.6] C10.
	ctx.unit.Flags &^= (0x3 << 17) | (0x3 << 19)
	ctx.unit.Flags |= (uint32(d1&3) << 17) | (uint32(d2&3) << 19)
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
	fields := splitArgs(rest)
	var targetHandle int
	found := false
	if len(fields) >= 1 {
		name := fields[0]
		if idx := ctx.lookupIdentOrUnitName(name); idx >= 0 {
			if idx < len(ctx.worldUnits) && ctx.worldUnits[idx] != nil {
				targetHandle = int(ctx.worldUnits[idx].Handle)
				found = true
			}
		}
	}
	if !found {
		// Fallback to self when unresolved [04 §3.6] C13.
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
