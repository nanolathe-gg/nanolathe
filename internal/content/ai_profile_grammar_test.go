package content

import (
	"math"
	"reflect"
	"testing"
)

// TestParseAIDirectivesKeepsSourceOrderAndArguments locks the tokenizer the
// profile grammar shares between an ai/*.txt file and a definition's ai_weight
// fragment: only the three keywords survive, arguments keep their authored
// order and spelling, and a `#` comment is not an argument [08 R-AI-01 §12]
// [08 R-AI-01 §20]. Correction (RWU-19-198): this test used to lock `//` as
// the comment introducer; retail's tokenizer knows only `#`, and `//` is an
// ordinary token — a line starting with it is an unknown keyword, a `//`
// inside a directive is an argument.
func TestParseAIDirectivesKeepsSourceOrderAndArguments(t *testing.T) {
	got := ParseAIDirectives([]byte(
		"# header\n" +
			"// old-style header is an unknown keyword\n" +
			"plan any easy hard # trailing\n" +
			"Weight ARMCK 0.5 // trailing tokens are arguments\n" +
			"nonsense ARMCK 1\n" +
			"limit LEVEL1 3;\n" +
			"limit LEVEL2 4#inside a token ends the line\n" +
			"plan\n"))
	want := []AIDirective{
		{Keyword: "plan", Args: []string{"any", "easy", "hard"}},
		{Keyword: "weight", Args: []string{"ARMCK", "0.5", "//", "trailing", "tokens", "are", "arguments"}},
		{Keyword: "limit", Args: []string{"LEVEL1", "3"}},
		{Keyword: "limit", Args: []string{"LEVEL2", "4"}},
		{Keyword: "plan", Args: []string{}},
	}
	if len(got) != len(want) {
		t.Fatalf("directives = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Keyword != want[i].Keyword || !reflect.DeepEqual(got[i].Args, want[i].Args) {
			t.Fatalf("directive %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestMultiArgumentPlanIndexesEveryDifficultyItNames locks the per-plan table
// index against a multi-argument `plan`: every difficulty the directive names
// gets the directives that follow, and `any` is indexed only from the first
// argument position, the retail quirk of [08 R-AI-01 §12].
func TestMultiArgumentPlanIndexesEveryDifficultyItNames(t *testing.T) {
	profile, err := ParseAIProfile([]byte("plan easy hard\nweight ARMCK 0.5\nlimit ARMCK 3\n"), "multi", Provenance{})
	if err != nil {
		t.Fatalf("ParseAIProfile: %v", err)
	}
	for _, name := range []string{"easy", "hard"} {
		if got := profile.Weight(name, "ARMCK"); got != 50 {
			t.Fatalf("plan easy hard: %s weight = %d, want 50", name, got)
		}
		if got := profile.Limit(name, "ARMCK"); got != 3 {
			t.Fatalf("plan easy hard: %s limit = %d, want 3", name, got)
		}
	}
	if got := profile.Weight("medium", "ARMCK"); got != 100 {
		t.Fatalf("plan easy hard must not name medium: %d, want 100", got)
	}

	// `any` in a later position names nothing extra.
	late, err := ParseAIProfile([]byte("plan easy any\nweight ARMCK 0.5\n"), "late", Provenance{})
	if err != nil {
		t.Fatalf("ParseAIProfile: %v", err)
	}
	if got := late.Weight("any", "ARMCK"); got != 100 {
		t.Fatalf("`any` in the second position must be inert: %d, want 100", got)
	}
	if got := late.Weight("easy", "ARMCK"); got != 50 {
		t.Fatalf("`plan easy any` still names easy: %d, want 50", got)
	}

	// A `plan` naming nothing leaves the gate clear for everything after it.
	empty, err := ParseAIProfile([]byte("plan hard\nweight ARMCK 0.5\nplan\nweight CORCK 0.5\n"), "empty", Provenance{})
	if err != nil {
		t.Fatalf("ParseAIProfile: %v", err)
	}
	if got := empty.Weight("hard", "CORCK"); got != 100 {
		t.Fatalf("an argument-less plan must close the gate: %d, want 100", got)
	}
	if got := empty.Weight("hard", "ARMCK"); got != 50 {
		t.Fatalf("directives before the argument-less plan still apply: %d, want 50", got)
	}
}

// TestAIPlanTableNamesDropsUnknownWords keeps a name outside the difficulty
// vocabulary from opening any table, and keeps duplicates from repeating.
func TestAIPlanTableNamesDropsUnknownWords(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, nil},
		{[]string{"sometimes"}, nil},
		{[]string{"easy", "sometimes", "hard"}, []string{"easy", "hard"}},
		{[]string{"any", "any"}, []string{"any"}},
		{[]string{"HARD"}, []string{"hard"}},
	} {
		if got := aiPlanTableNames(tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("aiPlanTableNames(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// TestAIWeightFactorReadsLikeTheRuntimeAtof locks the `weight` factor's
// conversion [08 R-AI-01 §20]: the C runtime's atof converts the longest
// decimal prefix of the token, accepts d/D as exponent letters, ignores what
// follows, has no hexadecimal form, and yields 0.0 for a token with no digit.
// The store is locked with it: the factor first narrows to float32, then the
// product retains the shared signed-64 helper's low word before clamping.
func TestAIWeightFactorReadsLikeTheRuntimeAtof(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"0.5", 0.5, true}, {".5", 0.5, true}, {"+.5", 0.5, true}, {"5e-1", 0.5, true}, {"5d-1", 0.5, true},
		{"1e0", 1, true}, {"1E0", 1, true}, {"1d0", 1, true}, {"1.", 1, true}, {"  2", 2, true},
		{"2x", 2, true}, {"2,5", 2, true}, {"2//c", 2, true}, {"1e", 1, true}, {"1e+", 1, true},
		{"abc", 0, false}, {"x1", 0, false}, {"0x10", 0, true}, {"-", 0, false}, {".", 0, false}, {"", 0, false},
		{"-2", -2, true},
	}
	for _, c := range cases {
		got, ok := parseAIWeightFactor(c.in)
		if got != c.want || ok != c.ok {
			t.Fatalf("parseAIWeightFactor(%q) = (%v, %v), want (%v, %v) [08 R-AI-01 §20]", c.in, got, ok, c.want, c.ok)
		}
	}
	// `0x10` converts its leading zero and stops at the x: factor 0, digit consumed.
	// Overflow is the runtime's overflow value; the store makes it 0.
	if f, _ := parseAIWeightFactor("1e999"); !math.IsInf(f, 1) {
		t.Fatalf("parseAIWeightFactor(1e999) = %v, want +Inf (runtime overflow value)", f)
	}
	stores := []struct {
		cur    int32
		factor float64
		want   int32
	}{
		{100, 0.5, 50}, {100, 0.29, 28}, {50, 2, 100}, {100, 1.5, 100}, {100, -0.5, 0},
		{100, 1e10, 0}, {100, math.Inf(1), 0}, {0, math.Inf(1), 0}, {100, 21474836.48, 100}, {100, 21474836.0, 100},
	}
	for _, s := range stores {
		if got := aiWeightStore(s.cur, s.factor); got != s.want {
			t.Fatalf("aiWeightStore(%d, %v) = %d, want %d [08 R-AI-01 §20]", s.cur, s.factor, got, s.want)
		}
	}
	// End to end: the profile applies the runtime's reading, and a `#` ends the line.
	profile, err := ParseAIProfile([]byte("plan any\nweight ARMCK 2x # halve? no: doubles, then clamps\nweight ARMCK .25\nweight ARMPW 1e10\n"), "ai/t.txt", Provenance{})
	if err != nil {
		t.Fatalf("ParseAIProfile: %v", err)
	}
	any := profile.Plans["any"]
	if w := any.Weights[CanonicalKey("armck")]; w != 25 {
		t.Fatalf("armck weight = %d, want 25 (100 x 2 clamped to 100, x .25)", w)
	}
	if w := any.Weights[CanonicalKey("armpw")]; w != 0 {
		t.Fatalf("armpw weight = %d, want 0 (out-of-range product stores the indefinite value, clamped)", w)
	}
}
