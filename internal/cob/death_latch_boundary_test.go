package cob

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// TestCOBDoesNotImportUnits locks the reason `ApplyNormalDamage`'s death latch
// cannot be routed through the unit death entry.
//
// `units.World.DestroyBy` / `units.MarkDeath` write the three death fields —
// the latch, the cause, and the recorded-attacker link of [04 R-UNIT-06 §5].
// This package cannot call them: `internal/units` imports `internal/cob`
// (units/cob_binding.go binds a unit's script state to a VM), so the reverse
// edge is a cycle. `VictimState` is this package's own arithmetic-only victim
// shape for the [04 §5.1] C26 funnel, with no handle and no world behind it.
//
// The compiler catches the cycle only while units→cob stands. If that edge is
// ever removed, adding cob→units becomes legal, and a later agent reading the
// latch at ApplyNormalDamage as "the death path" would then wire it to the
// death entry and give COB-funnel arithmetic a second, competing death writer.
// This test is what says no.
func TestCOBDoesNotImportUnits(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse internal/cob: %v", err)
	}
	for name, pkg := range pkgs {
		for file, syntax := range pkg.Files {
			for _, spec := range syntax.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("%s: bad import literal %s", file, spec.Path.Value)
				}
				if strings.HasSuffix(path, "/internal/units") {
					t.Errorf("package %s file %s imports %s: internal/units imports internal/cob, so this edge is a cycle, and the death entry stays out of reach from here [04 §5.1][04 R-UNIT-06 §5]",
						name, file, path)
				}
			}
		}
	}
}
