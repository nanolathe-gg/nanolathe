package headless

import (
	"errors"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// TestReportCarriesTheBoundMutators: the report prints the canonical set the
// session bound, and nothing for an unmutated battle
// (docs/DESIGN_MODS_MUTATORS.md §6.6).
func TestReportCarriesTheBoundMutators(t *testing.T) {
	request := Request{Map: "synthetic", SimulationSeed: 17, CRTSeed: 19, TickLimit: 1}
	for _, tc := range []struct {
		m    content.Mutators
		want string
	}{
		{content.Mutators{}, ""},
		{content.Mutators{BuildSpeed: content.Factor{Num: 3, Den: 2}, BuildCost: content.Factor{Num: 4, Den: 1}}, "buildCost=4,buildSpeed=1.5"},
	} {
		sess := syntheticSession(request)
		sess.Mutators = tc.m
		report, err := RunSession(request, sess)
		if !errors.Is(err, ErrTickLimit) {
			t.Fatalf("RunSession error = %v, want tick limit", err)
		}
		if report.Mutators != tc.want {
			t.Fatalf("report mutators = %q, want %q", report.Mutators, tc.want)
		}
	}
}

// TestDisplaylessRequestForwardsMutators: a request's mutators reach the
// session entry, in Strict 3.1 as in every mode, and move the reported
// catalog identity; a request without them runs on the unchanged catalog, so
// the fingerprint locks' catalog is untouched. Skipped without retail assets.
func TestDisplaylessRequestForwardsMutators(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	m := content.Mutators{BuildSpeed: content.Factor{Num: 2, Den: 1}}
	for _, tc := range []struct {
		m       content.Mutators
		mutated bool
	}{{content.Mutators{}, false}, {m, true}} {
		request := Request{Map: "ashap plateau", Gameplay: gameplay.Strict31, SimulationSeed: 7, CRTSeed: 7, TickLimit: 1, Mutators: tc.m}
		report, err := RunWithContent(request, fs, cat)
		if !errors.Is(err, ErrTickLimit) {
			t.Fatalf("RunWithContent error = %v, want tick limit", err)
		}
		if report.Mutators != tc.m.String() || (report.CatalogHash != cat.Hash) != tc.mutated {
			t.Fatalf("mutators %q catalog %q (base %q), want %q mutated=%v", report.Mutators, report.CatalogHash, cat.Hash, tc.m.String(), tc.mutated)
		}
	}
}
