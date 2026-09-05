package docs

import (
	"strings"
	"testing"
)

const repoRoot = "../.."

// TestEveryCitationResolves is the mechanical half of docs/ARCHITECTURE.md
// §"Deliberately out of scope": if a design document cites research that is
// not there, the unit dispatched against it has no contract to implement.
func TestEveryCitationResolves(t *testing.T) {
	bad, err := Dangling(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) == 0 {
		return
	}
	var b strings.Builder
	for _, c := range bad {
		b.WriteString("\n  " + c.String())
	}
	t.Errorf("%d dangling citation(s):%s", len(bad), b.String())
}

func TestNormHeadingStripsClosureTagsAndDates(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Campaign discovery — Established [P0-05]", "campaign discovery — established"},
		{"Placement and battle entry [P0-04] [P0-05] [P0-06]", "placement and battle entry"},
		{"Closed — schema selection, exactly [R-MAP-01 §4] (2026-08-29)", "closed — schema selection, exactly"},
		{"Unit record (`.fbi`)", "unit record (.fbi)"},
	} {
		if got := normHeading(tc.in); got != tc.want {
			t.Errorf("normHeading(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A heading may be cited by its qualifier-free prefix, which is how documents
// address the "Heading — Established [P0-nn]" shape.
func TestHeadingKeysIncludePrefix(t *testing.T) {
	keys := headingKeys("Campaign discovery — Established [P0-05]")
	var found bool
	for _, k := range keys {
		if k == "campaign discovery" {
			found = true
		}
	}
	if !found {
		t.Errorf("headingKeys = %q, want a bare %q key", keys, "campaign discovery")
	}
}

func TestResolveRejectsMissingTargets(t *testing.T) {
	docs := map[string]research{
		"04": {
			sections: map[string]bool{"7.2": true},
			headings: map[string]bool{"ground path search": true},
			anchors:  map[string]bool{"R-PATH-01": true},
		},
	}
	formats := map[string]bool{"tnt": true}
	for _, tc := range []struct{ text, want string }{
		{"04 §7.2", ""},
		{`04 "Ground path search"`, ""},
		{"04 R-PATH-01 §10", ""},
		{"fmt tnt", ""},
		{"I2", ""}, // not a citation at all
		{"04 §9.9", "§9.9 is not in document 04"},
		{`04 "Nowhere"`, `no heading "Nowhere" in document 04`},
		{"04 R-NOPE-01", "R-NOPE-01 is not in document 04"},
		{"09 §1", "no category document 09"},
		{"fmt nope", "no research/formats/nope.md"},
	} {
		if got := resolve(tc.text, docs, formats); got != tc.want {
			t.Errorf("resolve(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// TestEveryFindingHasADesignHome is the other half of coverage:
// docs/ARCHITECTURE.md §"Deliberately out of scope" claims every research
// section is either cited by a design document or listed as excluded. A
// traced contract no design document cites is work with no owner — the
// failure names it so the choice (design it, or add it to the exclusion
// table) is made deliberately rather than by omission.
func TestEveryFindingHasADesignHome(t *testing.T) {
	uncited, err := UncitedFindings(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(uncited) == 0 {
		return
	}
	var b strings.Builder
	for _, f := range uncited {
		b.WriteString("\n  " + f.String())
	}
	t.Errorf("%d research finding(s) with no design citation — cite each from the owning docs/DESIGN_*.md, "+
		"or list it in docs/ARCHITECTURE.md §\"Deliberately out of scope\":%s", len(uncited), b.String())
}
