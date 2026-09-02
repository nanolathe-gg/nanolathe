package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

// TestPerDefLimitDistinguishesAWrittenZero locks the reader against
// [05 R-SHARE-01 §9]: the definition parser writes -1 into the per-definition
// limit field of every definition it parses, so a written field is
// authoritative — a written 0 admits no unit of that definition at all, and is
// no longer confused with a hand-built fixture's unwritten Go zero value.
func TestPerDefLimitDistinguishesAWrittenZero(t *testing.T) {
	for _, tc := range []struct {
		name    string
		def     *content.UnitDef
		limit   int32
		limited bool
	}{
		{
			// What the definition parser produces for every definition.
			name:    "parser default",
			def:     &content.UnitDef{UnitName: "armck", LimitEnabled: true, Limit: -1},
			limit:   -1,
			limited: false,
		},
		{
			// What the multiplayer restriction apply step produces for a
			// definition with no node; a skirmish or campaign battle never
			// runs that step.
			name:    "written zero",
			def:     &content.UnitDef{UnitName: "armck", LimitEnabled: true, Limit: 0},
			limit:   0,
			limited: true,
		},
		{
			name:    "written positive",
			def:     &content.UnitDef{UnitName: "armck", LimitEnabled: true, Limit: 4},
			limit:   4,
			limited: true,
		},
		{
			// Nothing wrote this definition's limit field.
			name:    "unwritten field",
			def:     &content.UnitDef{UnitName: "armck"},
			limit:   -1,
			limited: false,
		},
	} {
		gotLimit, gotLimited := perDefLimit(tc.def)
		if gotLimit != tc.limit || gotLimited != tc.limited {
			t.Fatalf("%s: perDefLimit = (%d, %v), want (%d, %v)", tc.name, gotLimit, gotLimited, tc.limit, tc.limited)
		}
	}
}

// TestCheckPerDefLimitRejectsAtAWrittenZero proves the written zero reaches the
// allocation gate: no unit of that definition may be created for any owner,
// with none already alive [05 R-SHARE-01 §9].
func TestCheckPerDefLimitRejectsAtAWrittenZero(t *testing.T) {
	w := newConstructionFixtureWorld(10, nil)
	zero := &content.UnitDef{UnitName: "armzero", LimitEnabled: true, Limit: 0}
	if CheckPerDefLimit(w, 0, zero) {
		t.Fatal("a written zero limit admits no unit of that definition")
	}
	unlimited := &content.UnitDef{UnitName: "armck", LimitEnabled: true, Limit: -1}
	if !CheckPerDefLimit(w, 0, unlimited) {
		t.Fatal("the parser's -1 default is unlimited")
	}
}
