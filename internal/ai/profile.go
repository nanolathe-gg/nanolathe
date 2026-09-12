package ai

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Difficulty selects the plan gate [08 "Established AI-facing data and rooted planner"] [PLAN 11 C4].
// Valid values are any/easy/medium/hard. A `plan` directive clears the gate
// first and sets it only on a match, so an unknown plan name leaves the gate
// clear and disables every directive after it [08 R-AI-01 §12].
type Difficulty string

const (
	DifficultyAny    Difficulty = "any"
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

// isValidDifficulty reports whether d is one of the four plan names [PLAN 11 C4].
func isValidDifficulty(d Difficulty) bool {
	switch d {
	case DifficultyAny, DifficultyEasy, DifficultyMedium, DifficultyHard:
		return true
	default:
		return false
	}
}

// Profile is the compiled AI profile view [PLAN 11 Public API] [08 "Established AI-facing data and rooted planner"].
//
// Weight is clamped [0,100]; default 100 [PLAN 11 C4] [08 "Established AI-facing data and rooted planner"].
// Limit default -1 = unlimited [PLAN 11 C4].
// Plan is the active difficulty gate; Weight/Limit are first-match name views
// of the active per-definition tables.
// The underlying per-plan tables are retained for per-difficulty lookup (needed
// because a single ai/*.txt file carries easy/medium/hard sections and the
// global difficulty selects among them [08 "Established AI-facing data and rooted planner"]).
type Profile struct {
	Plan   Difficulty       // gate: any / easy / medium / hard [PLAN 11 C4]
	Weight map[string]int32 // clamped [0,100]; default 100 [PLAN 11 C4]
	Limit  map[string]int32 // default -1 = unlimited [PLAN 11 C4]

	name       string
	allWeights map[Difficulty]map[string]int32
	allLimits  map[Difficulty]map[string]int32

	// directives is the profile file's directive stream in authored order. The
	// catalog-aware applier of [08 R-AI-01 §12] replays it against the active
	// difficulty, because the name matcher and the two lock vectors need the
	// definition catalog that a bare parse does not have. A hand-built fixture
	// profile carries none; textLoaded separates "the file authored no
	// directive" from "this profile was never parsed from text", so applying a
	// catalog to a fixture keeps the tables the fixture set.
	directives []content.AIDirective
	textLoaded bool

	// appliedCatalog prevents applying immutable authored unit directives more
	// than once when a manager is rebound to the same catalog.
	appliedCatalog *content.Catalog
	weightsByID    map[uint32]int32
	limitsByID     map[uint32]int32
	recordIDs      map[*content.UnitDef]uint32
	// Fixture inputs are captured before deriving per-record state so rebinding
	// a cloned catalog or changing difficulty cannot multiply prior results.
	fixtureWeights map[string]int32
	fixtureLimits  map[string]int32
}

// controlByteComputer is the player slot control byte of a computer player
// [05 R-SHARE-01 §1].
const controlByteComputer uint8 = 2

// ApplyUnitDefinitions runs the whole profile grammar against a definition
// catalog [08 R-AI-01 §12]: the directive stream replays under the plan gate,
// each `weight`/`limit` name is expanded by the exact-versus-category matcher,
// the two lock vectors record every exact naming, and the two per-definition
// passes then fold each downloadable definition's authored `ai_weight` fragment
// into the types the file did not lock.
//
// The resulting tables are the same for every player slot, because retail parses
// the profile text once and applies it to every slot that has a manager. The one
// per-slot distinction is the control byte: `limit` applies only to slots whose
// control byte is 2, so p.Limit is the control-byte-2 table and the read site
// gates it — see LimitForControl. `weight` has no such gate: it writes every
// slot that has a manager, the human's included [08 R-AI-01 §18].
//
// `ai_limit` is intentionally not read: the limit pass re-reads `ai_weight`, a
// retail defect that leaves `ai_limit` with no reader at all [08 R-AI-01 §12].
//
// This entry applies the per-definition passes for ONE computer player. The
// scoring seam that calls it (selection.go) sees a player row, not the lobby,
// so it cannot count the control-byte-2 slots; a caller that can — a session
// composing the battle — should call ApplyUnitDefinitionsForPlayers with the
// real count, because the pass pair runs once per computer player
// [08 R-AI-01 §18] and the multiplication is visible with more than one.
func (p *Profile) ApplyUnitDefinitions(catalog *content.Catalog) {
	p.ApplyUnitDefinitionsForPlayers(catalog, 1)
}

// ApplyUnitDefinitionsForPlayers is ApplyUnitDefinitions with the number of
// slots whose record exists and whose control byte is 2. The pass PAIR runs
// once per computer player and every run writes every manager [08 R-AI-01 §18]:
// with k of them a category-naming fragment — which locks nothing — multiplies
// its members' weights 2·k times, while an exact-naming fragment applies once
// and is then refused by the per-type lock it set. A count below 1 is treated
// as 1; these tables are only ever read where a computer player exists, and a
// fixture that never filled its player rows is one manager, not none.
func (p *Profile) ApplyUnitDefinitionsForPlayers(catalog *content.Catalog, computerPlayers int) {
	if p == nil || catalog == nil {
		return
	}
	if computerPlayers < 1 {
		computerPlayers = 1
	}
	if p.appliedCatalog == catalog {
		return
	}
	state := &profileApply{
		catalog: catalog,
		// Keep every retained definition in ascending ID order, including
		// equal names [02 R-CAT-01 §5][08 R-AI-01 §12].
		records:    catalog.UnitRecords(),
		difficulty: p.Plan,
		weightLock: make(map[uint32]bool),
		limitLock:  make(map[uint32]bool),
	}
	state.ids = make(map[*content.UnitDef]uint32, len(state.records))
	for i, def := range state.records {
		if def != nil {
			state.ids[def] = uint32(i + 1)
		}
	}
	if p.textLoaded {
		// Every per-type weight starts at the default 100 and every limit at
		// -1; absent map entries are those defaults, so the replay starts from
		// empty tables [08 R-AI-01 §12].
		state.weights = make(map[uint32]int32, len(state.records))
		state.limits = make(map[uint32]int32, len(state.records))
		state.run(p.directives, false)
	} else {
		// A hand-built fixture profile has no directive stream; its authored
		// tables stand and only the per-definition passes run over them.
		if p.fixtureWeights == nil {
			p.fixtureWeights = cloneWeightTable(p.Weight)
			p.fixtureLimits = cloneWeightTable(p.Limit)
		}
		state.weights = make(map[uint32]int32)
		state.limits = make(map[uint32]int32)
		for _, key := range catalog.SortedUnitKeys() {
			id := state.ids[catalog.Units[key]]
			if id == 0 {
				continue
			}
			if v, ok := p.fixtureWeights[key]; ok {
				state.weights[id] = v
			}
			if v, ok := p.fixtureLimits[key]; ok {
				state.limits[id] = v
			}
		}
	}
	// The profile TEXT is parsed once and applied once; the two per-definition
	// passes are the part that runs per computer player [08 R-AI-01 §18].
	for i := 0; i < computerPlayers; i++ {
		state.perDefinitionPass(passWeightLock)
		state.perDefinitionPass(passLimitLock)
	}
	p.weightsByID, p.limitsByID, p.recordIDs = state.weights, state.limits, state.ids
	p.appliedCatalog = catalog
	p.Weight, p.Limit = make(map[string]int32), make(map[string]int32)
	for _, key := range catalog.SortedUnitKeys() {
		id := state.ids[catalog.Units[key]]
		if id == 0 {
			continue
		}
		if value, ok := state.weights[id]; ok {
			p.Weight[key] = value
		}
		if value, ok := state.limits[id]; ok {
			p.Limit[key] = value
		}
	}
}

// cloneWeightTable copies a per-type table without ranging the source map (I1).
func cloneWeightTable(src map[string]int32) map[string]int32 {
	out := make(map[string]int32, len(src))
	if len(src) == 0 {
		return out
	}
	keys := make([]string, 0, len(src))
	for key := range src {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out[key] = src[key]
	}
	return out
}

// The two per-definition passes, named by the lock vector each consults when it
// decides whether to SKIP a definition [08 R-AI-01 §18]. That vector is their
// only difference: both hand the whole `ai_weight` fragment to the shared
// dispatcher with all three keywords admitted, so a fragment's `weight` runs in
// both and a fragment's `limit` runs in both. Nothing filters a keyword by pass.
type perDefinitionLock int

const (
	passWeightLock perDefinitionLock = iota
	passLimitLock
)

// profileApply is the per-catalog state of the profile grammar: the two
// per-type tables and the two separate lock vectors [08 R-AI-01 §12].
type profileApply struct {
	catalog    *content.Catalog
	records    []*content.UnitDef // all retained records in ascending ID order
	ids        map[*content.UnitDef]uint32
	difficulty Difficulty
	weights    map[uint32]int32
	limits     map[uint32]int32
	weightLock map[uint32]bool
	limitLock  map[uint32]bool
}

// planGateOpen evaluates a `plan` directive against the active difficulty
// [08 R-AI-01 §12]. The directive clears the gate, then walks its arguments
// from the first to the last; for each argument it compares THE FIRST argument
// against `any` and THE CURRENT argument against the active difficulty's
// keyword, setting the gate on either match. Comparing `any` at a literal
// position rather than the loop position is a retail quirk: `any` is honoured
// only in the first argument position and repeating it later has no effect;
// reproduce it. A `plan` with no arguments runs no iterations and therefore
// leaves the gate clear, disabling every directive after it until the next
// `plan`.
func planGateOpen(args []string, active Difficulty) bool {
	open := false
	for i := range args {
		if strings.EqualFold(args[0], string(DifficultyAny)) {
			open = true
		}
		if strings.EqualFold(args[i], string(active)) {
			open = true
		}
	}
	return open
}

// match expands the name argument of a `weight` or `limit` directive and
// reports whether the naming was exact [08 R-AI-01 §12]. The name is first
// binary-searched against the definition catalog by authored `unitname`, which
// is the catalog's sort key: a hit names exactly that one type and is an exact
// naming; a miss instead expands to the whole category bitset registered for
// that name and is not exact, so it locks nothing. A name that is neither a
// definition nor a registered category expands to nothing.
func (a *profileApply) match(name string) ([]uint32, bool) {
	if a.catalog == nil {
		return nil, false
	}
	if def, ok := a.catalog.Unit(name); ok && def != nil {
		if id := a.ids[def]; id != 0 {
			return []uint32{id}, true
		}
	}
	mask, ok := a.catalog.Category(name)
	if !ok || mask.IsZero() {
		return nil, false
	}
	out := make([]uint32, 0, 8)
	for i, def := range a.records {
		if def != nil && mask.Contains(def.UnitDefID) {
			out = append(out, uint32(i+1))
		}
	}
	return out, false
}

// run replays a directive stream under the plan gate and returns the gate's
// state at the end of the stream. gate is its state on entry: closed for a
// profile file, and for a per-definition fragment whatever the PASS left it at
// — the pass opens it once at its start and never resets it per definition
// [08 R-AI-01 §18], so a fragment whose `plan` closes the gate closes it for
// every later definition of that pass.
func (a *profileApply) run(directives []content.AIDirective, gate bool) bool {
	for _, directive := range directives {
		switch directive.Keyword {
		case content.AIDirectivePlan:
			gate = planGateOpen(directive.Args, a.difficulty)
		case content.AIDirectiveWeight:
			if gate {
				a.applyWeight(directive.Args)
			}
		case content.AIDirectiveLimit:
			if gate {
				a.applyLimit(directive.Args)
			}
		}
	}
	return gate
}

// applyWeight multiplies the running per-type weight of every unlocked type in
// the name's bitset and clamps the product; an exact naming then locks those
// types against any later per-definition text [08 R-AI-01 §12].
func (a *profileApply) applyWeight(args []string) {
	if len(args) == 0 {
		return
	}
	// The second argument is a float defaulting to 0.0 [08 R-AI-01 §12], read
	// by the C runtime's atof — longest decimal prefix, trailing junk ignored,
	// no digit consumed means 0.0 [08 R-AI-01 §20]. The directive then applies
	// with whatever that yielded: nothing in the grammar conditions the write on
	// the token converting, so `weight ARMCK abc` zeroes the weight rather than
	// leaving it alone. This used to take the conversion flag as an admission
	// test and return when it was false, which is the one arm research rules
	// out. An absent argument is the same default, spelled "".
	factorToken := ""
	if len(args) > 1 {
		factorToken = args[1]
	}
	types, exact := a.match(args[0])
	for _, ck := range types {
		if a.weightLock[ck] {
			continue
		}
		current := int32(100)
		if prior, ok := a.weights[ck]; ok {
			current = prior
		}
		// One store for both readers of the grammar: content owns the
		// conversion, the truncation toward zero and the [0,100] clamp,
		// including the out-of-range product that the runtime's float-to-integer
		// routine turns into 0 rather than 100 [08 R-AI-01 §20] [I3]. Keeping
		// the arithmetic there also keeps the float out of this package (I2).
		a.weights[ck] = content.ApplyAIWeightFactor(current, factorToken)
	}
	if exact {
		for _, ck := range types {
			a.weightLock[ck] = true
		}
	}
}

// applyLimit assigns the per-type limit of every unlocked type in the name's
// bitset; an exact naming then sets the limit lock. -1 means unlimited
// [08 R-AI-01 §12].
func (a *profileApply) applyLimit(args []string) {
	if len(args) == 0 {
		return
	}
	// The second argument is an integer defaulting to 0 [08 R-AI-01 §12].
	value := int32(0)
	if len(args) > 1 {
		value = content.ParseAILimitValue(args[1])
	}
	types, exact := a.match(args[0])
	for _, ck := range types {
		if a.limitLock[ck] {
			continue
		}
		a.limits[ck] = value
	}
	if exact {
		for _, ck := range types {
			a.limitLock[ck] = true
		}
	}
}

// perDefinitionPass is one of the two passes of [08 R-AI-01 §18], exactly:
//
//  1. open the plan gate once, at the start of the pass — not once per
//     definition;
//  2. walk the definitions in ascending type order, skipping any that lacks
//     the authored `downloadable` flag, any whose lock in THIS pass's vector is
//     set, and any whose `ai_weight` text is empty;
//  3. hand the whole fragment to the same line-splitting dispatcher a line of
//     the profile file goes through, with all three keywords admitted.
//
// The gate is not reset between definitions: a fragment whose `plan` closes it
// — a difficulty that does not match, or an argument-less `plan` — leaves it
// closed for every later definition of the same pass, which makes catalog order
// load-bearing.
//
// The pass gate decides whether a fragment RUNS; the handlers decide whether it
// has an EFFECT. So a definition whose fragment locked its own weight in the
// first pass is parsed again in the second and its `weight` is refused there by
// applyWeight's lock.
//
// Both passes read `ai_weight`; `ai_limit` is parsed by the definition loader
// and read by nothing, which is the retail defect [08 R-AI-01 §12] names.
func (a *profileApply) perDefinitionPass(lock perDefinitionLock) {
	gate := true // opened once, at the start of the pass [08 R-AI-01 §18]
	for i, def := range a.records {
		ck := uint32(i + 1)
		if def == nil || !def.Downloadable {
			continue
		}
		if lock == passWeightLock && a.weightLock[ck] {
			continue
		}
		if lock == passLimitLock && a.limitLock[ck] {
			continue
		}
		if strings.TrimSpace(def.AIWeight) == "" {
			continue
		}
		gate = a.run(content.ParseAIDirectives([]byte(def.AIWeight)), gate)
	}
}

// CanonicalKey folds a type name the way the catalog does [02 §5] [08 "Established AI-facing data and rooted planner"].
func canonicalKey(name string) string {
	return content.CanonicalKey(name)
}

// WeightFor returns the weight for typeName, default 100 [PLAN 11 C4].
func (p *Profile) WeightFor(typeName string) int32 {
	if p == nil {
		return 100
	}
	if p.Weight != nil {
		if v, ok := p.Weight[canonicalKey(typeName)]; ok {
			return v
		}
	}
	return 100
}

// WeightForIndex returns the applied per-definition weight. Index zero and
// absent entries have the default 100 [08 R-AI-01 §12]. Name views expose only
// the first equal record; this accessor also reaches later equal records.
func (p *Profile) WeightForIndex(id uint32) int32 {
	if p != nil {
		if value, ok := p.weightsByID[id]; ok {
			return value
		}
	}
	return 100
}

// LimitForIndex returns the applied per-definition limit for a computer slot.
func (p *Profile) LimitForIndex(id uint32) int32 {
	if p != nil {
		if value, ok := p.limitsByID[id]; ok {
			return value
		}
	}
	return -1
}

// WeightForDefinition reads an applied catalog record without reducing it to
// its name. Unbound hand-built fixtures keep the existing name-table fallback.
func (p *Profile) WeightForDefinition(def *content.UnitDef) int32 {
	if def == nil {
		return 100
	}
	if p != nil && p.appliedCatalog != nil {
		if id := p.recordIDs[def]; id != 0 {
			return p.WeightForIndex(id)
		}
	}
	return p.WeightFor(def.UnitName)
}

// LimitForDefinition keeps the control-byte gate while preserving record ID.
func (p *Profile) LimitForDefinition(controlByte uint8, def *content.UnitDef) int32 {
	if controlByte != controlByteComputer || def == nil {
		return -1
	}
	if p != nil && p.appliedCatalog != nil {
		if id := p.recordIDs[def]; id != 0 {
			return p.LimitForIndex(id)
		}
	}
	return p.LimitFor(def.UnitName)
}

// LimitForControl returns the per-type limit a slot with the given control byte
// sees. The profile grammar's `limit` directive applies only to slots whose
// control byte is 2, so every other slot — a locally controlled human at 1, a
// remote peer at 3 — sees the unlimited default even though it has a manager
// and shares the profile's weight table [08 R-AI-01 §12] [05 R-SHARE-01 §1].
func (p *Profile) LimitForControl(controlByte uint8, typeName string) int32 {
	if controlByte != controlByteComputer {
		return -1
	}
	return p.LimitFor(typeName)
}

// LimitFor returns the limit for typeName, default -1 unlimited [PLAN 11 C4].
// It is the control-byte-2 table; LimitForControl owns the slot gate.
func (p *Profile) LimitFor(typeName string) int32 {
	if p == nil {
		return -1
	}
	if p.Limit != nil {
		if v, ok := p.Limit[canonicalKey(typeName)]; ok {
			return v
		}
	}
	return -1
}

// WeightForDifficulty returns the weight for typeName under difficulty d, default 100.
// If d has no table, the default is returned; this keeps the per-plan isolation
// of content.AIProfile [08 "Established AI-facing data and rooted planner"] [PLAN 11 C4].
func (p *Profile) WeightForDifficulty(d Difficulty, typeName string) int32 {
	if p == nil {
		return 100
	}
	if m, ok := p.allWeights[d]; ok {
		if v, ok2 := m[canonicalKey(typeName)]; ok2 {
			return v
		}
	}
	return 100
}

// LimitForDifficulty returns the limit for typeName under d, default -1.
func (p *Profile) LimitForDifficulty(d Difficulty, typeName string) int32 {
	if p == nil {
		return -1
	}
	if m, ok := p.allLimits[d]; ok {
		if v, ok2 := m[canonicalKey(typeName)]; ok2 {
			return v
		}
	}
	return -1
}

// HasWeight reports whether typeName was explicitly marked for the active plan
// (i.e., present in Weight) [08 "Established AI-facing data and rooted planner"].
func (p *Profile) HasWeight(typeName string) bool {
	if p == nil || p.Weight == nil {
		return false
	}
	_, ok := p.Weight[canonicalKey(typeName)]
	return ok
}

// HasLimit reports whether typeName was explicitly marked for the active plan
// (i.e., present in Limit) [08 "Established AI-facing data and rooted planner"].
func (p *Profile) HasLimit(typeName string) bool {
	if p == nil || p.Limit == nil {
		return false
	}
	_, ok := p.Limit[canonicalKey(typeName)]
	return ok
}

// HasWeightForDifficulty reports whether typeName was marked for difficulty d.
func (p *Profile) HasWeightForDifficulty(d Difficulty, typeName string) bool {
	if p == nil {
		return false
	}
	if m, ok := p.allWeights[d]; ok {
		_, ok2 := m[canonicalKey(typeName)]
		return ok2
	}
	return false
}

// HasLimitForDifficulty reports whether typeName was marked for d.
func (p *Profile) HasLimitForDifficulty(d Difficulty, typeName string) bool {
	if p == nil {
		return false
	}
	if m, ok := p.allLimits[d]; ok {
		_, ok2 := m[canonicalKey(typeName)]
		return ok2
	}
	return false
}

// Difficulties returns the sorted difficulties present in the profile (deterministic I1).
func (p *Profile) Difficulties() []Difficulty {
	if p == nil || len(p.allWeights) == 0 && len(p.allLimits) == 0 {
		return nil
	}
	set := make(map[Difficulty]struct{})
	for k := range p.allWeights {
		set[k] = struct{}{}
	}
	for k := range p.allLimits {
		set[k] = struct{}{}
	}
	out := make([]Difficulty, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SetDifficulty selects the difficulty word the plan gate compares each
// directive's arguments against — 0 easy, 1 medium, 2 hard, written from the
// lobby setting or the campaign difficulty control [08 R-AI-01 §12]. It clears
// the applied-catalog memo so the next ApplyUnitDefinitions replays the whole
// directive stream under the new gate. A word outside the vocabulary is
// ignored, leaving LoadProfile's fallback — the last plan the file names — in
// place. Session composition supplies the word at battle entry, before any
// directive is applied.
func (p *Profile) SetDifficulty(d Difficulty) {
	if p == nil || !isValidDifficulty(d) {
		return
	}
	if p.Plan == d {
		return
	}
	p.Plan = d
	p.appliedCatalog = nil
	p.weightsByID, p.limitsByID, p.recordIDs = nil, nil, nil
	if p.textLoaded {
		p.Weight, p.Limit = cloneWeightTable(p.allWeights[d]), cloneWeightTable(p.allLimits[d])
	} else if p.fixtureWeights != nil {
		p.Weight, p.Limit = cloneWeightTable(p.fixtureWeights), cloneWeightTable(p.fixtureLimits)
	}
}

// Name returns the profile basename (without extension) as loaded.
func (p *Profile) Name() string {
	if p == nil {
		return ""
	}
	return p.name
}

// LoadProfile loads ai/<name>.txt with fallback ai/default.txt [08 "Established AI-facing data and rooted planner"] [PLAN 11 WU-11-1].
// It reuses phase 2's parser (internal/content ai_profile.go) so the plan gate,
// clamp, and unknown-plan reset are identical [PLAN 11 C4].
func LoadProfile(fs vfs.FSOps, name string) (*Profile, error) {
	if fs == nil {
		return nil, fmt.Errorf("ai: nil VFS")
	}
	clean := strings.TrimSpace(name)
	if clean == "" {
		clean = "default"
	}
	// Strip extension if caller passed "default.txt".
	if strings.HasSuffix(strings.ToLower(clean), ".txt") {
		clean = strings.TrimSuffix(clean, clean[len(clean)-4:])
		// Re-trim after stripping extension
		clean = strings.TrimSpace(clean)
		if clean == "" {
			clean = "default"
		}
	}
	primary := path.Join("ai", clean+".txt")
	fallback := path.Join("ai", "default.txt")

	data, err := fs.ReadFileLimit(primary, 1<<20)
	usedName := clean
	usedPath := primary
	if err != nil {
		if !isNotFound(err) || strings.EqualFold(primary, fallback) {
			return nil, fmt.Errorf("ai: %s: %w", primary, err)
		}
		data2, err2 := fs.ReadFileLimit(fallback, 1<<20)
		if err2 != nil {
			return nil, fmt.Errorf("ai: %s: %w (fallback %s: %v)", primary, err, fallback, err2)
		}
		data = data2
		usedName = "default"
		usedPath = fallback
	}

	// Provenance is not critical for AI planner but preserve logical path for diagnostics.
	prov := content.Provenance{LogicalPath: usedPath}
	if info, serr := fs.Stat(usedPath); serr == nil {
		prov = content.Provenance{LogicalPath: info.Path, MountOrder: info.Source.MountOrder, ProviderID: info.Source.SourcePath}
		if prov.ProviderID == "" {
			prov.ProviderID = info.Source.ProviderType
		}
	}

	cprof, err := content.ParseAIProfile(data, usedName, prov)
	if err != nil {
		return nil, fmt.Errorf("ai: %s: %w", usedPath, err)
	}

	prof := &Profile{
		name:       usedName,
		allWeights: make(map[Difficulty]map[string]int32),
		allLimits:  make(map[Difficulty]map[string]int32),
		// The authored directive stream is retained because the grammar's name
		// matcher and lock vectors need the definition catalog, which arrives
		// later than the parse [08 R-AI-01 §12].
		directives: content.ParseAIDirectives(data),
		textLoaded: true,
	}

	for k, v := range cprof.Plans {
		d := Difficulty(strings.ToLower(k))
		if !isValidDifficulty(d) {
			continue
		}
		wm := make(map[string]int32, len(v.Weights))
		for kk, vv := range v.Weights {
			wm[kk] = vv
		}
		lm := make(map[string]int32, len(v.Limits))
		for kk, vv := range v.Limits {
			lm[kk] = vv
		}
		prof.allWeights[d] = wm
		prof.allLimits[d] = lm
	}

	// Determine active plan: last valid plan in raw text, else priority fallback.
	active := lastValidPlan(data)
	if _, ok := prof.allWeights[active]; !ok {
		if _, ok2 := prof.allLimits[active]; !ok2 {
			active = ""
		}
	}
	if active == "" {
		// Priority fallback: any > easy > medium > hard > sorted first
		for _, cand := range []Difficulty{DifficultyAny, DifficultyEasy, DifficultyMedium, DifficultyHard} {
			if _, ok := prof.allWeights[cand]; ok {
				active = cand
				break
			}
			if _, ok := prof.allLimits[cand]; ok {
				active = cand
				break
			}
		}
		if active == "" && len(prof.allWeights)+len(prof.allLimits) > 0 {
			keys := make([]string, 0)
			for k := range prof.allWeights {
				keys = append(keys, string(k))
			}
			for k := range prof.allLimits {
				if _, ok := prof.allWeights[k]; !ok {
					keys = append(keys, string(k))
				}
			}
			sort.Strings(keys)
			if len(keys) > 0 {
				active = Difficulty(keys[0])
			}
		}
	}
	prof.Plan = active
	if wm, ok := prof.allWeights[active]; ok {
		prof.Weight = wm
	} else {
		prof.Weight = make(map[string]int32)
	}
	if lm, ok := prof.allLimits[active]; ok {
		prof.Limit = lm
	} else {
		prof.Limit = make(map[string]int32)
	}
	return prof, nil
}

// lastValidPlan scans raw profile text for the last valid `plan` directive.
// A `plan` clears the gate and sets it only on a match, so an unknown name
// leaves the gate clear [08 R-AI-01 §12]; this helper reports the last name
// that would have opened it, which is a different question from the gate state
// and is why an unknown name does not clear `last` [PLAN 11 C4].
func lastValidPlan(data []byte) Difficulty {
	text := string(data)
	lines := strings.Split(text, "\n")
	var last Difficulty
	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		trimmed = strings.TrimSuffix(trimmed, ";")
		trimmed = strings.TrimSpace(trimmed)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		if !strings.EqualFold(fields[0], "plan") {
			continue
		}
		cand := Difficulty(strings.ToLower(strings.TrimSpace(fields[1])))
		if isValidDifficulty(cand) {
			last = cand
		}
		// An unknown name leaves the gate clear in the parser [08 R-AI-01 §12];
		// here it leaves `last` alone, because this helper answers "which plan
		// tables did the file ever open", not "is the gate open now".
	}
	return last
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	// vfs.ErrNotFound is the canonical not-found sentinel.
	return errors.Is(err, vfs.ErrNotFound)
}
