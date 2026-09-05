// The AI profile compiler.

package content

import (
	"errors"
	"fmt"
	"math"
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
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ";"))
		parts := aiLineTokens(line)
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

// ApplyAIWeightFactor applies one `weight` directive's factor token to a
// running per-type weight and returns the stored result
// [08 R-AI-01 §12] [08 R-AI-01 §20]: the token is read by the runtime's atof
// (an absent argument is ""), the product of the running weight and the factor
// is truncated toward zero by the runtime's float-to-integer routine, and the
// clamp is "at or below zero becomes zero, at or above 100 becomes 100". A
// product outside the signed 32-bit range or not a number is that routine's
// integer-indefinite value, which the clamp stores as 0.
//
// This is the whole store — token in, stored weight out — because the profile
// grammar has two readers, ParseAIProfile here and the catalog-aware applier in
// internal/ai, and neither may keep its own copy of the conversion, the
// product's width or the clamp. The directive applies with whatever atof
// produced; nothing conditions the write on the token converting, so a caller
// must not gate on parseAIWeightFactor's flag.
func ApplyAIWeightFactor(current int32, factorToken string) int32 {
	factor, _ := parseAIWeightFactor(factorToken)
	return aiWeightStore(current, factor)
}

// ParseAILimitValue reads a `limit` directive's second argument through the
// integer accessor, which takes a decimal integer and ignores trailing junk
// [02 §4]. The established default when the argument is absent is 0
// [08 R-AI-01 §12]; -1 means unlimited.
func ParseAILimitValue(value string) int32 {
	return formats.ParseTDFInteger(value)
}

// aiDirectiveTypeAndValue reads the type-name and value arguments of a
// `weight` or `limit` directive's Args, exactly as both dispatchers of
// [08 R-AI-01 §18] read them: only the type name (Args[0]) is required, and
// the value argument (Args[1]) falls back to "" when absent so the caller
// applies the established default instead of dropping the directive — retail
// dispatches a definition's `ai_weight` fragment "exactly as a line of
// ai\default.txt is dispatched", with no argument-conversion gate. Both
// ParseAIProfile and the applier in internal/ai read through this so the two
// readers cannot drift apart on what counts as "no argument".
func aiDirectiveTypeAndValue(args []string) (typeName, valueStr string, ok bool) {
	if len(args) == 0 {
		return "", "", false // no name argument: nothing for the matcher to expand
	}
	typeName = args[0]
	if len(args) >= 2 {
		valueStr = args[1]
	}
	return typeName, valueStr, true
}

// parseAIWeightFactor is the runtime `atof` read of [08 R-AI-01 §20]; the
// flag reports whether the token contributed at least one digit.
func parseAIWeightFactor(value string) (float64, bool) {
	return crtAtof(value)
}

// crtAtof converts s the way the C runtime's atof does [08 R-AI-01 §20]:
// leading whitespace is skipped, then the longest prefix of the form
// `[+-]digits[.digits][(e|E|d|D)[+-]digits]` is converted and everything
// after it is ignored. A prefix with no digit (including an empty string)
// is 0.0. There is no hexadecimal form and no `inf`/`nan` token; an
// exponent that overflows yields the runtime's overflow value (±Inf), which
// the weight store then turns into 0 (aiWeightStore). The second result is
// whether a digit was consumed — informational only, the grammar never
// conditions a directive on it.
func crtAtof(s string) (float64, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || (s[i] >= '\t' && s[i] <= '\r')) {
		i++ // C isspace: space, \t \n \v \f \r
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return 0, false
	}
	mant := s[start:i]
	exp := ""
	if i < len(s) && (s[i] == 'e' || s[i] == 'E' || s[i] == 'd' || s[i] == 'D') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			exp = "e" + s[i+1:k] // the runtime accepts d/D as exponent letters; Go does not
		}
	}
	f, err := strconv.ParseFloat(mant+exp, 64)
	if err != nil {
		// ParseFloat returns ±Inf (and a range error) on overflow and 0 on
		// underflow, which is the runtime's result too; any other error is
		// impossible for a string of this shape.
		var ne *strconv.NumError
		if !errors.As(err, &ne) || ne.Err != strconv.ErrRange {
			return 0, false
		}
	}
	return f, true
}

// aiWeightStore is the `weight` directive's store [08 R-AI-01 §12]
// [08 R-AI-01 §20]: the product of the running weight and the factor is
// truncated toward zero by the runtime's float-to-integer routine, and the
// clamp is "at or below zero becomes zero, at or above 100 becomes 100". That
// routine returns the integer-indefinite value (-2^31) for a product outside
// the signed 32-bit range or not a number, so such a product stores 0 — a
// factor of 1e10 zeroes the weight rather than pinning it at 100. Go's
// float-to-int conversion is implementation-defined out of range, hence the
// explicit test.
func aiWeightStore(cur int32, factor float64) int32 {
	product := float64(cur) * factor
	if math.IsNaN(product) || product >= 2147483648.0 || product < -2147483648.0 {
		return 0
	}
	newWeight := int32(product) // trunc toward zero [INVARIANTS I3]
	if newWeight < 0 {
		newWeight = 0
	} else if newWeight > 100 {
		newWeight = 100
	}
	return newWeight
}

// aiLineTokens splits one profile line the way retail's directive tokenizer
// does [08 R-AI-01 §20]: a `#` ends the line (whether it starts a token or
// sits inside one), tokens are separated by runtime whitespace, and at most
// twenty tokens are kept. `//` is not a comment introducer.
func aiLineTokens(line string) []string {
	if idx := strings.IndexByte(line, '#'); idx >= 0 {
		line = line[:idx]
	}
	parts := strings.Fields(line)
	if len(parts) > 20 {
		parts = parts[:20]
	}
	return parts
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
		// Correction (RWU-19-198): this used to cut the line at `//`. Retail's
		// tokenizer knows only `#` as a comment introducer; `//` is an
		// ordinary token, so a line starting with it is an unknown keyword
		// (ignored whole) and a `//` between a name and its factor IS the
		// factor [08 R-AI-01 §20]. Stock profiles are indifferent — a trailing
		// `// note` after the factor is beyond the two read arguments either way.
		parts := aiLineTokens(line)
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
			// The factor is "a float, defaulting to 0.0" [08 R-AI-01 §12], read
			// through the C runtime's atof — the longest decimal prefix of the
			// token, junk ignored, no digits → 0.0 — and the directive applies
			// with whatever that yields; nothing in the grammar conditions the
			// write on the token converting [08 R-AI-01 §20]. This used to skip
			// the directive when the factor was absent or unconvertible, which
			// is the one arm research rules out: retail writes
			// clamp(trunc(current * 0.0)) = 0 rather than leaving the weight
			// alone. `limit` below is the same shape with an integer default of
			// 0, and already reads that way. It is unauthored either way — the
			// reference install's ten AI profiles carry 947 `weight` directives
			// and every factor is a plain decimal (WU-19-167 census) — so the
			// grammar matters only for third-party profiles.
			typeName, factorStr, ok := aiDirectiveTypeAndValue(parts[1:])
			if !ok {
				continue
			}
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
				pl.Weights[ck] = ApplyAIWeightFactor(cur, factorStr)
			}
		case "limit":
			if len(currentPlans) == 0 {
				continue // gate [PLAN 11 C4]
			}
			// "an integer defaulting to 0" [08 R-AI-01 §12], and the directive
			// applies with that default, exactly as `weight` does above. Stock
			// content reaches this: `ai/krogoth.txt` authors both a unit name
			// containing a space and a letter O in place of a zero, and retail's
			// integer accessor converts neither.
			typeName, valueStr, ok := aiDirectiveTypeAndValue(parts[1:])
			if !ok {
				continue
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
