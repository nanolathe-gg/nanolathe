package content

import "testing"

// TestAIProfileWeightArgumentDefault locks the `weight` directive's argument
// default from [08 R-AI-01 §12]: the second argument is "a float, defaulting to
// 0.0", and the directive applies with that default. Nothing in the grammar
// conditions the write on the argument converting, so an absent or
// unconvertible factor multiplies the running weight by zero rather than
// leaving the directive out.
//
// The regression this guards is the tempting one: treating the conversion
// failure as an admission test, which silently turns "this type is worth
// nothing" into "this type keeps its default of 100" — the opposite outcome.
func TestAIProfileWeightArgumentDefault(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"absent factor", "plan hard\nweight ARMFLAK\n"},
		{"unconvertible factor", "plan hard\nweight ARMFLAK not-a-number\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile, err := ParseAIProfile([]byte(tc.text), "ai/default.txt", Provenance{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pl := profile.Plans["hard"]
			if pl == nil {
				t.Fatal("hard plan missing")
			}
			got, ok := pl.Weights[CanonicalKey("ARMFLAK")]
			if !ok {
				t.Fatal("the directive must apply with the 0.0 default, not be skipped")
			}
			if got != 0 {
				t.Fatalf("weight = %d, want 0 (100 x 0.0, clamped)", got)
			}
		})
	}

	// The converting case must still multiply, so the assertion above cannot
	// pass by the parser having stopped applying weights altogether.
	profile, err := ParseAIProfile([]byte("plan hard\nweight ARMFLAK 0.5\n"), "ai/default.txt", Provenance{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := profile.Plans["hard"].Weights[CanonicalKey("ARMFLAK")]; got != 50 {
		t.Fatalf("converting factor: weight = %d, want 50", got)
	}
}
