package ai

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func tempFS(t *testing.T, files map[string]string) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	for p, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatalf("mount: %v", err)
	}
	return fs
}

func TestPlanGate(t *testing.T) {
	// C4: weight/limit lines BEFORE first plan do not apply [PLAN 11 C4]
	fs := tempFS(t, map[string]string{
		"ai/test.txt": "weight PRE 0.5\nlimit PRE2 5\nplan any\nweight POST 0.5\nlimit POST2 5\n",
	})
	p, err := LoadProfile(fs, "test")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if p.WeightFor("PRE") != 100 {
		t.Fatalf("weight before plan should not apply: PRE got %d want 100", p.WeightFor("PRE"))
	}
	if p.HasWeight("PRE") {
		t.Fatalf("PRE should not be marked")
	}
	if got := p.WeightFor("POST"); got != 50 {
		t.Fatalf("weight after plan: POST got %d want 50", got)
	}
	if !p.HasWeight("POST") {
		t.Fatalf("POST should be marked")
	}
	if p.LimitFor("PRE2") != -1 {
		t.Fatalf("limit before plan should not apply: PRE2 got %d want -1", p.LimitFor("PRE2"))
	}
	if p.HasLimit("PRE2") {
		t.Fatalf("PRE2 should not be marked")
	}
	if got := p.LimitFor("POST2"); got != 5 {
		t.Fatalf("limit after plan: POST2 got %d want 5", got)
	}
	if !p.HasLimit("POST2") {
		t.Fatalf("POST2 should be marked")
	}
	// Also verify that weight before plan does not affect stored weight for same type after
	fs2 := tempFS(t, map[string]string{
		"ai/test2.txt": "weight SAME 0.1\nplan any\nweight SAME 0.5\n",
	})
	p2, err := LoadProfile(fs2, "test2")
	if err != nil {
		t.Fatalf("LoadProfile test2: %v", err)
	}
	// Stored weight for SAME should be 100*0.5=50, not 100*0.1*0.5=5
	if got := p2.WeightFor("SAME"); got != 50 {
		t.Fatalf("gate prevents chaining: SAME got %d want 50", got)
	}
}

func TestWeightClamp(t *testing.T) {
	// C4 clamp [0,100] both ends
	fs := tempFS(t, map[string]string{
		"ai/clamp.txt": "plan any\nweight HIGH 10\nweight LOW -5\nweight ZERO 0\n",
	})
	p, err := LoadProfile(fs, "clamp")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if got := p.WeightFor("HIGH"); got != 100 {
		t.Fatalf("clamp high: got %d want 100", got)
	}
	if got := p.WeightFor("LOW"); got != 0 {
		t.Fatalf("clamp low: got %d want 0", got)
	}
	if got := p.WeightFor("ZERO"); got != 0 {
		t.Fatalf("zero factor: got %d want 0", got)
	}
	// Test chaining clamp: start 100, *2 => 100 (clamped), *0.5 => 50
	fs2 := tempFS(t, map[string]string{
		"ai/chain.txt": "plan any\nweight CHAIN 2\nweight CHAIN 0.5\n",
	})
	p2, err := LoadProfile(fs2, "chain")
	if err != nil {
		t.Fatalf("LoadProfile chain: %v", err)
	}
	if got := p2.WeightFor("CHAIN"); got != 50 {
		t.Fatalf("chained clamp: got %d want 50", got)
	}
	// Upper clamp after chain: 50 * 10 => 500 clamped 100
	fs3 := tempFS(t, map[string]string{
		"ai/chain2.txt": "plan any\nweight CHAIN 0.5\nweight CHAIN 10\n",
	})
	p3, err := LoadProfile(fs3, "chain2")
	if err != nil {
		t.Fatalf("LoadProfile chain2: %v", err)
	}
	if got := p3.WeightFor("CHAIN"); got != 100 {
		t.Fatalf("chained upper clamp: got %d want 100", got)
	}
}

func TestLimitDefaultAndMarked(t *testing.T) {
	// C4 limit default -1 unlimited and marked entries
	fs := tempFS(t, map[string]string{
		"ai/limit.txt": "plan any\nlimit TANK 2\nlimit ZERO 0\n",
	})
	p, err := LoadProfile(fs, "limit")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if got := p.LimitFor("TANK"); got != 2 {
		t.Fatalf("limit TANK got %d want 2", got)
	}
	if !p.HasLimit("TANK") {
		t.Fatalf("TANK should be marked")
	}
	if got := p.LimitFor("ZERO"); got != 0 {
		t.Fatalf("limit ZERO got %d want 0", got)
	}
	if !p.HasLimit("ZERO") {
		t.Fatalf("ZERO should be marked (0 is valid explicit limit)")
	}
	if got := p.LimitFor("ABSENT"); got != -1 {
		t.Fatalf("limit absent default got %d want -1", got)
	}
	if p.HasLimit("ABSENT") {
		t.Fatalf("ABSENT should not be marked")
	}
	// Default weight 100 for absent
	if got := p.WeightFor("ABSENT"); got != 100 {
		t.Fatalf("weight absent default got %d want 100", got)
	}
	if p.HasWeight("ABSENT") {
		t.Fatalf("ABSENT weight should not be marked")
	}
}

func TestFallbackToDefault(t *testing.T) {
	// LoadProfile fallback ai/default.txt [PLAN 11 C4]
	fs := tempFS(t, map[string]string{
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	p, err := LoadProfile(fs, "missing")
	if err != nil {
		t.Fatalf("LoadProfile missing should fallback to default: %v", err)
	}
	if got := p.WeightFor("FALLBACK"); got != 50 {
		t.Fatalf("fallback weight got %d want 50", got)
	}
	if p.Name() != "default" {
		t.Fatalf("fallback name got %q want %q", p.Name(), "default")
	}
	// Direct load of existing should not use fallback
	fs2 := tempFS(t, map[string]string{
		"ai/default.txt": "plan any\nweight DEFAULT 0.5\n",
		"ai/exist.txt":   "plan any\nweight EXIST 0.25\n",
	})
	p2, err := LoadProfile(fs2, "exist")
	if err != nil {
		t.Fatalf("LoadProfile exist: %v", err)
	}
	if got := p2.WeightFor("EXIST"); got != 25 {
		t.Fatalf("exist weight got %d want 25", got)
	}
	if got := p2.WeightFor("DEFAULT"); got != 100 {
		t.Fatalf("exist should not see default weight: got %d want 100", got)
	}
}

func TestUnknownPlanLineBehavior(t *testing.T) {
	// The gate of [08 R-AI-01 §12]: a `plan` directive clears the gate first and
	// sets it only for `any` (first position only) or the active difficulty's
	// keyword, so a directive naming nothing disables every directive after it
	// until the next `plan`. (The comment here used to point at the parser's own
	// open marker, which asks a different question — which decimal conversion
	// reads the weight factor — and at a `currentPlan` string the parser no
	// longer has; it keeps a plan-name slice.)
	fs := tempFS(t, map[string]string{
		"ai/unknown.txt": "plan any\nweight GOOD1 0.5\nplan unknown\nweight BAD 0.5\nplan hard\nweight GOOD2 0.5\n",
	})
	p, err := LoadProfile(fs, "unknown")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	// Active plan should be hard (last valid)
	if p.Plan != DifficultyHard {
		t.Fatalf("active plan got %q want %q", p.Plan, DifficultyHard)
	}
	// BAD should not be present in any difficulty
	if p.WeightForDifficulty(DifficultyAny, "BAD") != 100 {
		t.Fatalf("BAD in any got %d want 100", p.WeightForDifficulty(DifficultyAny, "BAD"))
	}
	if p.WeightForDifficulty(DifficultyHard, "BAD") != 100 {
		t.Fatalf("BAD in hard got %d want 100", p.WeightForDifficulty(DifficultyHard, "BAD"))
	}
	if p.HasWeightForDifficulty(DifficultyHard, "BAD") {
		t.Fatalf("BAD should not be marked in hard")
	}
	// GOOD1 should be in any, GOOD2 in hard, per-plan isolation
	if got := p.WeightForDifficulty(DifficultyAny, "GOOD1"); got != 50 {
		t.Fatalf("GOOD1 in any got %d want 50", got)
	}
	if got := p.WeightForDifficulty(DifficultyHard, "GOOD2"); got != 50 {
		t.Fatalf("GOOD2 in hard got %d want 50", got)
	}
	// GOOD1 should not be in hard (isolation)
	if p.WeightForDifficulty(DifficultyHard, "GOOD1") != 100 {
		t.Fatalf("GOOD1 should not be in hard: got %d", p.WeightForDifficulty(DifficultyHard, "GOOD1"))
	}
	// Verify via active view BAD absent
	if p.WeightFor("BAD") != 100 {
		t.Fatalf("active BAD got %d want 100", p.WeightFor("BAD"))
	}
	if got := p.WeightFor("GOOD2"); got != 50 {
		t.Fatalf("active GOOD2 got %d want 50", got)
	}
}

func TestDifficultyValidation(t *testing.T) {
	fs := tempFS(t, map[string]string{
		"ai/diff.txt": "plan easy\nweight X 0.5\nplan medium\nweight Y 0.5\nplan hard\nweight Z 0.5\nplan any\nweight W 0.5\n",
	})
	p, err := LoadProfile(fs, "diff")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	for _, d := range []Difficulty{DifficultyAny, DifficultyEasy, DifficultyMedium, DifficultyHard} {
		found := false
		for _, dd := range p.Difficulties() {
			if dd == d {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("difficulty %q not found in profile", d)
		}
	}
}

func TestKrogothGateRegression(t *testing.T) {
	// Regression from retail krogoth.txt: two weight lines before first plan must not apply
	fs := tempFS(t, map[string]string{
		"ai/krogoth.txt": "weight cormakr 0.2\nweight armmakr 0.2\nplan easy\nWeight ARM 0.2\n",
	})
	p, err := LoadProfile(fs, "krogoth")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if p.WeightFor("cormakr") != 100 {
		t.Fatalf("krogoth cormakr before plan should be ignored: got %d want 100", p.WeightFor("cormakr"))
	}
	if p.WeightFor("armmakr") != 100 {
		t.Fatalf("krogoth armmakr before plan should be ignored: got %d want 100", p.WeightFor("armmakr"))
	}
	// ARM weight 0.2 => 20
	if got := p.WeightForDifficulty(DifficultyEasy, "ARM"); got != 20 {
		t.Fatalf("krogoth ARM easy got %d want 20", got)
	}
}

// TestWeightFactorAppliesWhateverTheConversionYields locks the `weight`
// directive's store in the catalog-aware applier [08 R-AI-01 §12]
// [08 R-AI-01 §20]. The factor is read by the C runtime's atof and the
// directive applies with whatever that produced — nothing conditions the write
// on the token converting — so a token with no digit and an absent argument are
// both the established default 0.0 and ZERO the weight rather than leaving it
// alone. A product the runtime's float-to-integer routine cannot represent is
// its integer-indefinite value, which the clamp stores as 0, not 100.
//
// This used to be wrong here: applyWeight took the conversion flag as an
// admission test and returned when it was false, so `weight ARMFLASH abc` left
// the weight at 100.
func TestWeightFactorAppliesWhateverTheConversionYields(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want int32
	}{
		{"no digits", "plan any\nweight ARMFLASH abc\n", 0},
		{"overflowing exponent", "plan any\nweight ARMFLASH 1e10\n", 0},
		{"plain decimal", "plan any\nweight ARMFLASH 0.5\n", 50},
		{"absent factor", "plan any\nweight ARMFLASH\n", 0},
		// The product is formed at the width of the runtime's conversion, not
		// narrowed to single precision first: 100 x 0.29 is just under 29 and
		// truncates to 28, where a float32 product would round up to exactly 29
		// and truncate to 29. This case is the two widths' first disagreement.
		{"width of the product", "plan any\nweight ARMFLASH 0.29\n", 28},
	} {
		profile := grammarProfile(tc.text, DifficultyAny)
		profile.ApplyUnitDefinitions(grammarCatalog(t, nil))
		if got := profile.WeightFor("ARMFLASH"); got != tc.want {
			t.Fatalf("%s: %q gives weight %d, want %d [08 R-AI-01 §20]", tc.name, tc.text, got, tc.want)
		}
	}
}
