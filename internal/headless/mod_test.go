package headless

import (
	"errors"
	"testing"
)

// TestReportNamesTheMountedMod: the report states the mounted mod as
// `<id>@<version>`, and `none` without one (docs/DESIGN_MODS_MUTATORS.md §6.6).
func TestReportNamesTheMountedMod(t *testing.T) {
	for mod, want := range map[string]string{"": "none", "prota@4.8": "prota@4.8"} {
		request := Request{Map: "synthetic", SimulationSeed: 17, CRTSeed: 19, TickLimit: 1, Mod: mod}
		report, err := RunSession(request, syntheticSession(request))
		if !errors.Is(err, ErrTickLimit) {
			t.Fatalf("RunSession error = %v, want tick limit", err)
		}
		if report.Mod != want {
			t.Fatalf("report mod = %q, want %q", report.Mod, want)
		}
	}
}
