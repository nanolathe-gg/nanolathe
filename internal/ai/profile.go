package ai

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/vfs"
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
// Plan is the active difficulty gate; Weight/Limit are the active plan's maps.
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

	// appliedCatalog prevents applying immutable authored unit directives more
	// than once when a manager is rebound to the same catalog.
	appliedCatalog *content.Catalog
}

// ApplyUnitDefinitions folds each definition's authored ai_weight directives
// into the active profile tables used by selection. Each weight directive is
// applied in source order as int32(float32(current)*factor), then clamped to
// [0,100]; embedded limit directives are registered in the active per-type
// limit table.
//
// Unimplemented: [08 R-AI-01 §12] establishes the precedence — the profile file
// wins whenever its `weight`/`limit` named a type exactly, because the exact
// naming sets that type's weight lock or limit lock and the two per-definition
// passes skip locked types. A category name locks nothing, so per-definition
// text still applies there. Both lock vectors, the exact-versus-category name
// matcher, and the `downloadable` gate on the per-definition passes are
// missing here — see PLAN 19 §2.4.
//
// `ai_limit` is intentionally not read: the limit pass re-reads `ai_weight`, a
// retail defect that leaves `ai_limit` with no reader at all [08 R-AI-01 §12].
func (p *Profile) ApplyUnitDefinitions(catalog *content.Catalog) {
	if p == nil || catalog == nil || p.appliedCatalog == catalog {
		return
	}
	p.appliedCatalog = catalog
	keys := make([]string, 0, len(catalog.Units))
	for key := range catalog.Units {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	if p.Weight == nil {
		p.Weight = make(map[string]int32)
	}
	if p.Limit == nil {
		p.Limit = make(map[string]int32)
	}
	for _, unitKey := range keys {
		applyUnitWeight(catalog.Units[unitKey], p.Weight, p.Limit)
	}
}

func applyUnitWeight(def *content.UnitDef, weights, limits map[string]int32) {
	if def == nil || def.AIWeight == "" {
		return
	}
	directives := content.ParseAIWeight([]byte(def.AIWeight))
	for _, directive := range directives.Weights {
		if weights == nil {
			continue
		}
		current := int32(100)
		if prior, ok := weights[directive.Type]; ok {
			current = prior
		}
		// Retail narrows the running product to float32 before __ftol-style
		// truncation; do not clamp or quantize the authored factor first [08
		// "Established AI-facing data and rooted planner"].
		updated := int32(float32(current) * directive.Factor)
		if updated < 0 {
			updated = 0
		} else if updated > 100 {
			updated = 100
		}
		weights[directive.Type] = updated
	}
	limitKeys := make([]string, 0, len(directives.Limits))
	for key := range directives.Limits {
		limitKeys = append(limitKeys, key)
	}
	sort.Strings(limitKeys)
	for _, key := range limitKeys {
		if limits != nil {
			limits[key] = directives.Limits[key]
		}
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

// LimitFor returns the limit for typeName, default -1 unlimited [PLAN 11 C4].
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

// NewManager constructs a per-player AI manager with explicit profile-load error handling [P0-07] ON-06 F-P0-007.
// It loads ai/<profileName>.txt via LoadProfile (fallback ai/default.txt) and returns error if missing,
// never silently producing a passive manager with nil Profile. The returned manager
// is ready for session binding of its build queue and simulation stream.
// Caller must bind QueueBuildTyped and a simulation RNG before ticks.
// isAlliance is the alliance test injected at construction; nil fails closed
// and grants no inferred relationship [08 R-AI-01 §9].
func NewManager(player uint8, fs vfs.FSOps, profileName string, r *rng.Simulation, catalog *content.Catalog, surfaceMetal int32, isAlliance func(a, b uint8) bool) (*Manager, error) {
	if fs == nil {
		return nil, fmt.Errorf("ai: NewManager: nil VFS")
	}
	clean := strings.TrimSpace(profileName)
	if clean == "" {
		return nil, fmt.Errorf("ai: NewManager: empty profile name (missing selected profile) [P0-07]")
	}
	// Explicit profile-load with error surfacing [P0-07] F-P0-007.
	prof, err := LoadProfile(fs, clean)
	if err != nil {
		return nil, fmt.Errorf("ai: NewManager: profile %q: %w [P0-07]", clean, err)
	}
	if prof == nil {
		return nil, fmt.Errorf("ai: NewManager: profile %q: loaded nil profile [P0-07]", clean)
	}
	m := &Manager{
		Player:       player,
		Profile:      prof,
		Catalog:      catalog,
		SurfaceMetal: surfaceMetal,
		IsAlliance:   isAlliance,
		RNG:          r,
	}
	prof.ApplyUnitDefinitions(catalog)
	m.Strategic.Catalog = catalog
	return m, nil
}
