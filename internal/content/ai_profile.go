// Package content compiles retail's authored data into immutable definitions.
// This file implements the AI profile compiler [08 "Computer-controlled players"]
// with the plan any/easy/medium/hard gate [PLAN 11 C4] and the weight/limit grammar.
package content

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// AIProfile is a compiled AI profile from ai/*.txt [08 "Computer-controlled players"] [PLAN 02 Discovery].
// DefinitionHeader must be the first field per catalog convention [02 §5].
// Each profile is gated by plan any/easy/medium/hard — weight and limit lines before the first
// plan do not apply [08 "Computer-controlled players"] [PLAN 11 C4].
type AIProfile struct {
	DefinitionHeader
	Name  string             // basename without extension, e.g. "default"
	Plans map[string]*AIPlan // key is lowercased plan name: any, easy, medium, hard
	Raw   string             // raw text retained for diagnostics (not hashed)
}

// AIPlan holds the per-plan weight/limit tables [08 "Computer-controlled players"] [PLAN 11 C4].
// Defaults are weight 100 and limit -1 unlimited [08 "Computer-controlled players"].
type AIPlan struct {
	Weights map[string]int32 // CanonicalKey(type) -> weight clamped [0,100], default 100
	Limits  map[string]int32 // CanonicalKey(type) -> limit, default -1
}

// AIWeightDirective retains an authored ai_weight multiplier until the AI
// profile boundary. The retail reader narrows the running product to float32
// before truncating it to the stored integer, so converting this factor to a
// percentage here would lose authored precision [08 "Established AI-facing
// data and rooted planner"].
type AIWeightDirective struct {
	Type   string
	Factor float32
}

// AIDirective is one directive of the profile grammar in source order. The
// keyword table is exactly `plan`, `weight` and `limit`; every other token is
// ignored [08 R-AI-01 §12]. Args holds the arguments after the keyword with
// their authored order and spelling intact, because the grammar's gate rule
// distinguishes argument positions.
type AIDirective struct {
	Keyword string
	Args    []string
}

// Directive keywords of the profile grammar [08 R-AI-01 §12].
const (
	AIDirectivePlan   = "plan"
	AIDirectiveWeight = "weight"
	AIDirectiveLimit  = "limit"
)

// ParseAIDirectives tokenizes profile text — a whole ai/*.txt file or the
// fragment authored in a definition's ai_weight field — into the ordered
// directive stream of the three-keyword table [08 R-AI-01 §12]. Order is
// preserved because `weight` multiplies the running per-type value, so two
// directives naming one type do not commute. Unknown keywords and lines with no
// arguments are dropped; the applier owns the gate, the name matcher and the
// lock vectors.
func ParseAIDirectives(data []byte) []AIDirective {
	var out []AIDirective
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ";"))
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		keyword := strings.ToLower(parts[0])
		switch keyword {
		case AIDirectivePlan, AIDirectiveWeight, AIDirectiveLimit:
		default:
			continue
		}
		args := make([]string, len(parts)-1)
		copy(args, parts[1:])
		out = append(out, AIDirective{Keyword: keyword, Args: args})
	}
	return out
}

// ParseAIWeightFactor reads a `weight` directive's second argument. The
// established default is 0.0 [08 R-AI-01 §12], and it covers an absent
// argument and text that is not a float at all alike, so the returned value is
// usable whatever the flag says. The flag reports only whether the text
// converted; nothing in the grammar conditions the directive on it, so a caller
// that uses the flag as an admission test is diverging from the grammar.
func ParseAIWeightFactor(value string) (float64, bool) {
	return parseAIWeightFactor(value)
}

// ParseAILimitValue reads a `limit` directive's second argument through the
// integer accessor, which takes a decimal integer and ignores trailing junk
// [02 §4]. The established default when the argument is absent is 0
// [08 R-AI-01 §12]; -1 means unlimited.
func ParseAILimitValue(value string) int32 {
	return formats.ParseTDFInteger(value)
}

// AIWeightPlan contains the directives authored in a unit's ai_weight field.
// Weights is a slice because repeated directives apply in source order;
// Limits remain a map because a limit is an assignment rather than an
// arithmetic fold [08 "Established AI-facing data and rooted planner"].
type AIWeightPlan struct {
	Weights []AIWeightDirective
	Limits  map[string]int32
}

// ParseAIWeight parses the directive text stored in a unit definition's
// ai_weight field. Unlike a profile file, this field contains directives
// directly (the shipped form is `weight <type> <factor>`), so there is no plan
// gate. The directive vocabulary and limit assignment follow the profile
// grammar; weight multiplication is deferred to the active profile boundary
// so the authored factor remains intact at the established float32 narrowing
// point [08 "Computer-controlled players"] [08 "Established AI-facing data and rooted planner"].
// An unknown keyword is ignored. A malformed argument still drops the
// directive here; see the TODO(T25) in the loop for why that diverges from the
// profile reader and what has to move for it to stop.
func ParseAIWeight(data []byte) *AIWeightPlan {
	plan := &AIWeightPlan{
		Limits: make(map[string]int32),
	}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ";"))
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		// TODO(T25): this fragment reader still drops a directive whose
		// argument is absent or unconvertible, where ParseAIProfile now applies
		// the established default (0.0 for `weight`, 0 for `limit`). Retail has
		// one dispatcher for both — the per-definition fragment is dispatched
		// "exactly as a line of ai\default.txt is dispatched", with every
		// keyword admitted and no argument-conversion gate [08 R-AI-01 §18], so
		// the two must agree. The wiring is the four lines below (require only
		// the name, then read parts[2] with a "" fallback); the blocker is
		// TestParseAIWeightMalformedLinesIgnored in ai_weight_test.go, which
		// locks the drop arm and is outside this unit's file ownership.
		// Unauthored either way: of the reference install's 278 FBI files only
		// 7 author ai_weight, carrying 7 directive lines, all well formed
		// (WU-19-167 census).
		if len(parts) < 3 {
			continue
		}
		switch strings.ToLower(parts[0]) {
		case "weight":
			factor, ok := parseAIWeightFactor(parts[2])
			if !ok {
				continue
			}
			ck := CanonicalKey(parts[1])
			if ck == "" {
				continue
			}
			// Keep the source multiplier intact. The active profile value is
			// supplied later, and each directive is narrowed and clamped there.
			plan.Weights = append(plan.Weights, AIWeightDirective{
				Type:   ck,
				Factor: float32(factor),
			})
		case "limit":
			ck := CanonicalKey(parts[1])
			if ck == "" {
				continue
			}
			plan.Limits[ck] = formats.ParseTDFInteger(parts[2])
		}
	}
	return plan
}

func parseAIWeightFactor(value string) (float64, bool) {
	factor, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err == nil {
		return factor, true
	}
	factor = parseAIFloat(value)
	if factor == 0 && !isZeroFloatString(value) {
		return 0, false
	}
	return factor, true
}

// aiPlanNames is the difficulty vocabulary of the plan gate [08 R-AI-01 §12].
var aiPlanNames = map[string]struct{}{
	"any":    {},
	"easy":   {},
	"medium": {},
	"hard":   {},
}

// aiPlanTableNames returns the per-difficulty table names a `plan` directive
// opens, given its arguments.
//
// [08 R-AI-01 §12] establishes the gate itself: the directive clears the gate,
// walks its arguments from the first to the last, and for each argument
// compares the FIRST argument against `any` and the CURRENT argument against
// the active difficulty's keyword. `any` in a later position is therefore
// inert. A directive with no arguments runs no iterations and leaves the gate
// clear, disabling every directive after it until the next `plan`.
//
// The per-difficulty tables of AIProfile are Nanolathe's lookup index over one
// parse, not a retail structure: retail knows the active difficulty while it
// parses. This helper indexes a multi-argument directive under each difficulty
// it names, and keeps `any` as its own table entry only in the first argument
// position, so the position quirk is visible here too. The authoritative gate
// evaluation against a chosen difficulty lives with the profile applier.
func aiPlanTableNames(args []string) []string {
	var names []string
	for i, arg := range args {
		name := strings.ToLower(strings.TrimSpace(arg))
		if _, ok := aiPlanNames[name]; !ok {
			continue
		}
		if name == "any" && i != 0 {
			continue
		}
		duplicate := false
		for _, have := range names {
			if have == name {
				duplicate = true
				break
			}
		}
		if !duplicate {
			names = append(names, name)
		}
	}
	return names
}

// Weight returns the weight for a type under a plan, or the default 100 when absent
// [08 "Computer-controlled players"]. The type key is folded via CanonicalKey [02 §5].
func (p *AIProfile) Weight(plan, typeName string) int32 {
	if p == nil || p.Plans == nil {
		return 100
	}
	pl := p.Plans[strings.ToLower(plan)]
	if pl == nil || pl.Weights == nil {
		return 100
	}
	if v, ok := pl.Weights[CanonicalKey(typeName)]; ok {
		return v
	}
	return 100
}

// Limit returns the limit for a type under a plan, or -1 unlimited when absent
// [08 "Computer-controlled players"]. The type key is folded via CanonicalKey [02 §5].
func (p *AIProfile) Limit(plan, typeName string) int32 {
	if p == nil || p.Plans == nil {
		return -1
	}
	pl := p.Plans[strings.ToLower(plan)]
	if pl == nil || pl.Limits == nil {
		return -1
	}
	if v, ok := pl.Limits[CanonicalKey(typeName)]; ok {
		return v
	}
	return -1
}

// PlansSorted returns the plan names in sorted order for deterministic iteration (I1).
func (p *AIProfile) PlansSorted() []string {
	if p == nil || p.Plans == nil {
		return nil
	}
	keys := make([]string, 0, len(p.Plans))
	for k := range p.Plans {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WeightKeysSorted returns weight type keys for a plan in sorted order for hash stability (I1).
func (a *AIPlan) WeightKeysSorted() []string {
	if a == nil || a.Weights == nil {
		return nil
	}
	keys := make([]string, 0, len(a.Weights))
	for k := range a.Weights {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// LimitKeysSorted returns limit type keys for a plan in sorted order for hash stability (I1).
func (a *AIPlan) LimitKeysSorted() []string {
	if a == nil || a.Limits == nil {
		return nil
	}
	keys := make([]string, 0, len(a.Limits))
	for k := range a.Limits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ParseAIProfile parses a single AI profile text into an AIProfile
// [08 R-AI-01 §12] [PLAN 11 C4].
// The input is the raw file bytes. It implements the plan gate: weight and
// limit lines before the first plan do not apply [PLAN 11 C4]. Weight lines
// multiply the stored per-type weight and clamp to [0,100]; limit lines store
// the limit; the defaults are weight 100 and limit -1 [08 R-AI-01 §12].
//
// A `plan` may carry several arguments; aiPlanTableNames owns that rule and
// the first-position `any` quirk [08 R-AI-01 §12].
//
// The per-plan tables this builds are a lookup index over one parse, keyed by
// the difficulty vocabulary. They do not carry the grammar's name matcher or
// its two lock vectors, both of which need the definition catalog: the
// catalog-aware applier that owns them consumes ParseAIDirectives instead
// (internal/ai, [08 R-AI-01 §12]).
func ParseAIProfile(data []byte, name string, prov Provenance) (*AIProfile, error) {
	text := string(data)
	// Keep raw for diagnostics; not part of hash directly (hash uses canonical bytes).
	profile := &AIProfile{
		DefinitionHeader: DefinitionHeader{
			CanonicalKey: CanonicalKey(name),
			Provenance:   prov,
		},
		Name:  name,
		Plans: make(map[string]*AIPlan),
		Raw:   text,
	}
	// currentPlans is empty until a `plan` opens the gate; lines before the
	// first `plan`, and lines after a `plan` that names nothing, do not apply
	// [08 R-AI-01 §12] [PLAN 11 C4].
	var currentPlans []string
	lines := strings.Split(text, "\n")
	for _, rawLine := range lines {
		// Strip carriage return from Windows line endings.
		line := strings.TrimRight(rawLine, "\r")
		// Blank // comments to spaces before parsing, preserving offsets idea from [02 §4],
		// but for AI plain text we simply truncate at // [08 "Computer-controlled players"].
		// Inline // after a value is a comment and must be ignored.
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.Fields(trimmed)
		if len(parts) == 0 {
			continue
		}
		cmd := strings.ToLower(parts[0])
		switch cmd {
		case "plan":
			// A `plan` clears the gate first and sets it only when an argument
			// matches `any` (first position only) or the active difficulty's
			// keyword, so a directive naming nothing leaves the gate clear and
			// disables every directive after it until the next `plan`
			// [08 R-AI-01 §12].
			currentPlans = aiPlanTableNames(parts[1:])
			for _, name := range currentPlans {
				if _, ok := profile.Plans[name]; !ok {
					profile.Plans[name] = &AIPlan{
						Weights: make(map[string]int32),
						Limits:  make(map[string]int32),
					}
				}
			}
		case "weight":
			if len(currentPlans) == 0 {
				continue // gate: weight lines before plan do not apply [PLAN 11 C4]
			}
			if len(parts) < 2 {
				continue // no name argument: nothing for the matcher to expand
			}
			typeName := parts[1]
			// The factor is "a float, defaulting to 0.0" [08 R-AI-01 §12], and
			// the directive applies with that default — nothing in the grammar
			// conditions the write on the argument converting. This used to
			// skip the directive when the factor was absent or unconvertible,
			// which is the one arm research rules out: retail would have
			// written clamp(trunc(current * 0.0)) = 0, not left the weight
			// alone. `limit` below is the same shape with an integer default
			// of 0, and already reads that way.
			//
			// TODO(question): which conversion reads the factor is still open.
			// [08 R-AI-01 §12] gives the type and the default but names no
			// routine, and the CRT decimal converter differs from
			// strconv.ParseFloat on trailing junk and on hex forms. Decider:
			// the `weight` handler's conversion call, to be recorded in
			// [08 R-AI-01 §12]. It is unauthored either way — the reference
			// install's ten AI profiles carry 947 `weight` directives and every
			// factor is a plain decimal (WU-19-167 census) — so the arms differ
			// only on third-party profiles.
			factorStr := ""
			if len(parts) >= 3 {
				factorStr = parts[2]
			}
			factor, _ := parseAIWeightFactor(factorStr)
			ck := CanonicalKey(typeName)
			for _, name := range currentPlans {
				pl := profile.Plans[name]
				if pl == nil {
					pl = &AIPlan{Weights: make(map[string]int32), Limits: make(map[string]int32)}
					profile.Plans[name] = pl
				}
				cur := int32(100)
				if v, ok := pl.Weights[ck]; ok {
					cur = v
				}
				// [08 R-AI-01 §12]: the directive multiplies the running per-type
				// weight and clamps the product to [0,100].
				newWeight := int32(float64(cur) * factor) // trunc toward zero [INVARIANTS I3]
				if newWeight < 0 {
					newWeight = 0
				} else if newWeight > 100 {
					newWeight = 100
				}
				pl.Weights[ck] = newWeight
			}
		case "limit":
			if len(currentPlans) == 0 {
				continue // gate [PLAN 11 C4]
			}
			if len(parts) < 2 {
				continue // no name argument: nothing for the matcher to expand
			}
			typeName := parts[1]
			// "an integer defaulting to 0" [08 R-AI-01 §12], and the directive
			// applies with that default, exactly as `weight` does above. Stock
			// content reaches this: `ai/krogoth.txt` authors both a unit name
			// containing a space and a letter O in place of a zero, and retail's
			// integer accessor converts neither.
			valueStr := ""
			if len(parts) >= 3 {
				valueStr = parts[2]
			}
			// Integer accessor style: leading decimal digits, trailing junk
			// ignored, non-numeric text is 0 [02 §4][fmt tdf].
			val := formats.ParseTDFInteger(valueStr)
			ck := CanonicalKey(typeName)
			for _, name := range currentPlans {
				pl := profile.Plans[name]
				if pl == nil {
					pl = &AIPlan{Weights: make(map[string]int32), Limits: make(map[string]int32)}
					profile.Plans[name] = pl
				}
				pl.Limits[ck] = val
			}
		default:
			// Unknown token — ignore per being plain text profile with only these three verbs [08].
			continue
		}
	}
	// Hash over canonical bytes including defaults, independent of map iteration (I1) [02 §5] C12.
	// Hash is identical across two runs (I1) and includes all per-plan weights/limits sorted.
	var b strings.Builder
	fmt.Fprintf(&b, "%s|", profile.CanonicalKey)
	planKeys := make([]string, 0, len(profile.Plans))
	for k := range profile.Plans {
		planKeys = append(planKeys, k)
	}
	sort.Strings(planKeys)
	for _, pk := range planKeys {
		pl := profile.Plans[pk]
		fmt.Fprintf(&b, "plan:%s|", pk)
		wk := pl.WeightKeysSorted()
		for _, tk := range wk {
			fmt.Fprintf(&b, "w:%s=%d|", tk, pl.Weights[tk])
		}
		lk := pl.LimitKeysSorted()
		for _, tk := range lk {
			fmt.Fprintf(&b, "l:%s=%d|", tk, pl.Limits[tk])
		}
	}
	profile.Hash = HashDefinition([]byte(b.String()))
	return profile, nil
}

func isZeroFloatString(s string) bool {
	trim := strings.TrimSpace(s)
	if trim == "" {
		return false
	}
	for _, c := range trim {
		if c != '0' && c != '.' && c != '+' && c != '-' {
			return false
		}
	}
	return strings.Contains(trim, "0")
}

func parseAIFloat(s string) float64 {
	trim := strings.TrimSpace(s)
	if trim == "" {
		return 0
	}
	// Handle leading dot ".1"
	if strings.HasPrefix(trim, ".") {
		trim = "0" + trim
	} else if strings.HasPrefix(trim, "-.") {
		trim = "-0." + trim[1:]
	} else if strings.HasPrefix(trim, "+.") {
		trim = "+0." + trim[1:]
	}
	f, err := strconv.ParseFloat(trim, 64)
	if err != nil {
		return 0
	}
	return f
}

// CompileAIProfiles compiles AI profiles from ai/*.txt [08 "Computer-controlled players"] [PLAN 02 Discovery].
// Discovery is ai/*.txt (10, incl default.txt) via VFS union directory "ai" [PLAN 02].
// It returns a map keyed by CanonicalKey(basename without extension) [02 §5].
// Each profile is parsed with the plan gate exactly as specified [PLAN 11 C4].
func CompileAIProfiles(fs vfs.FSOps) (map[string]*AIProfile, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	entries, err := fs.ReadDir("ai")
	if err != nil {
		return nil, fmt.Errorf("content: ai: %w", err)
	}
	// ReadDir is sorted by Path [vfs.ReadDir] (I1).
	result := make(map[string]*AIProfile)
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		lower := strings.ToLower(e.Path)
		if !strings.HasSuffix(lower, ".txt") {
			continue
		}
		base := strings.TrimSuffix(strings.ToLower(strings.TrimPrefix(lower, "ai/")), ".txt")
		base = strings.TrimSpace(base)
		// Preserve original display name from OriginalPath basename without ext.
		displayName := baseNameWithoutExtAI(e.OriginalPath)
		if displayName == "" {
			displayName = base
		}
		data, err := fs.ReadFileLimit(e.Path, 1<<20)
		if err != nil {
			// Skip unreadable entries but keep deterministic iteration; do not fail whole compile
			// unless it's default.txt which is expected. Missing alias is handled by fallback elsewhere
			// [08 "Computer-controlled players"] — fallback ai/default.txt.
			continue
		}
		prov := ProvenanceFrom(e)
		prof, err := ParseAIProfile(data, displayName, prov)
		if err != nil {
			return nil, fmt.Errorf("content: %s: %w", e.Path, err)
		}
		key := CanonicalKey(base)
		// Normalize canonical to base lowercased; display name kept as Name field.
		prof.CanonicalKey = key
		// Hash already includes canonical; ensure consistent.
		result[key] = prof
	}
	// Ensure default.txt exists — it is the fallback for mission aiprofile [08 "Computer-controlled players"].
	// Explicit diagnostic for missing selected profile [P0-07] ON-06 F-P0-007: never silently produce passive manager.
	if _, ok := result[CanonicalKey("default")]; !ok {
		return nil, fmt.Errorf("content: ai/default.txt: not found (missing selected AI profile fallback) [P0-07]")
	}
	return result, nil
}

// compileAIProfiles is an unexported alias for Catalog integration [02 §5] C1 two-stage.
func compileAIProfiles(fs vfs.FSOps) (map[string]*AIProfile, error) {
	return CompileAIProfiles(fs)
}

// CompileAIProfilesSorted returns AI profiles sorted by canonical key for
// hash-stable iteration and tests (I1).
func CompileAIProfilesSorted(fs vfs.FSOps) ([]*AIProfile, error) {
	m, err := CompileAIProfiles(fs)
	if err != nil {
		return nil, err
	}
	out := make([]*AIProfile, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CanonicalKey < out[j].CanonicalKey })
	return out, nil
}

// baseNameWithoutExtAI extracts the basename without extension from a path,
// preserving case for display name; AI profiles are case-insensitive [02 §5] but
// the Name field keeps the authored case for diagnostics.
func baseNameWithoutExtAI(path string) string {
	base := path
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if idx := strings.LastIndex(base, "\\"); idx >= 0 {
		base = base[idx+1:]
	}
	if dot := strings.LastIndex(base, "."); dot >= 0 {
		base = base[:dot]
	}
	return strings.TrimSpace(base)
}
