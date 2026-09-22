package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestCommunityPreviewFacingUsesCommittedFogSafeFirstUnit(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		"builder": {UnitName: "builder", BuildDistance: 100},
	}}
	def := &content.UnitDef{PreviewFaceOpponent: true}
	cur := &frame.Frame{ViewingPlayer: 0}
	cur.Selection.LocalPlayer = 0
	cur.Selection.Handles = []pool.Handle{1}
	cur.Players[0].Present = true
	cur.Players[1].Present = true
	cur.Players[1].UnitSlotStart = 2
	cur.Units = []frame.UnitView{
		{Slot: 1, Owner: 0, DefName: "builder", X: whole(100), Z: whole(100)},
		// Slot 2 is the enemy player's source-defined first unit. Slot 3 must
		// never replace it merely because slot 3 is visible.
		{Slot: 2, Owner: 1, X: whole(180), Z: whole(100), DirectVisibilityKnown: true},
		{Slot: 3, Owner: 1, X: whole(100), Z: whole(180), DirectVisibilityKnown: true, DirectlyVisible: true},
	}
	if got := communityPreviewFacing(cur, cat, def, units.FacingWest, whole(100), whole(100)); got != units.FacingWest {
		t.Fatalf("hidden first unit leaked facing: got %d", got)
	}
	// A dead/absent first slice record does not fall through to a later live
	// unit owned by that player.
	cur.Units = []frame.UnitView{cur.Units[0], cur.Units[2]}
	if got := communityPreviewFacing(cur, cat, def, units.FacingWest, whole(100), whole(100)); got != units.FacingWest {
		t.Fatalf("absent first slice record fell through: got %d", got)
	}
	cur.Units = append(cur.Units, frame.UnitView{Slot: 2, Owner: 1, X: whole(180), Z: whole(100), DirectVisibilityKnown: true, DirectlyVisible: true})
	if got := communityPreviewFacing(cur, cat, def, units.FacingWest, whole(100), whole(100)); got != units.FacingEast {
		t.Fatalf("visible first unit facing = %d, want east", got)
	}
}

func TestCommunityPreviewFacingBuilderRangeInclusiveAndBypassesAllowedFacings(t *testing.T) {
	builder := &content.UnitDef{UnitName: "builder", BuildDistance: 50}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": builder}}
	def := &content.UnitDef{PreviewFaceOpponent: true, Rotations: content.FacingSouth}
	cur := &frame.Frame{ViewingPlayer: 0}
	cur.Selection.LocalPlayer = 0
	cur.Selection.Handles = []pool.Handle{4}
	cur.Players[0].Present = true
	cur.Players[2].Present = true
	cur.Players[2].UnitSlotStart = 8
	cur.Units = []frame.UnitView{
		{Slot: 4, Owner: 0, DefName: "builder", X: whole(50)},
		{Slot: 8, Owner: 2, X: whole(-20), DirectVisibilityKnown: true, DirectlyVisible: true},
	}
	if got := communityPreviewFacing(cur, cat, def, units.FacingSouth, 0, 0); got != units.FacingWest {
		t.Fatalf("inclusive range or rotations bypass failed: got %d", got)
	}
	cur.Units[0].X = whole(50) + 1
	if got := communityPreviewFacing(cur, cat, def, units.FacingSouth, 0, 0); got != units.FacingSouth {
		t.Fatalf("out-of-range builder changed facing: got %d", got)
	}
	cur.Units[0].X, cur.Units[0].BuildRemaining = whole(50), 0.5
	if got := communityPreviewFacing(cur, cat, def, units.FacingSouth, 0, 0); got != units.FacingSouth {
		t.Fatalf("unfinished builder changed facing: got %d", got)
	}
}

func TestCommunityPreviewFacingFiltersInactiveWatcherAndAlliedPlayers(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": {UnitName: "builder", BuildDistance: 100}}}
	def := &content.UnitDef{PreviewFaceOpponent: true}
	cur := &frame.Frame{ViewingPlayer: 0}
	cur.Selection.LocalPlayer = 0
	cur.Selection.Handles = []pool.Handle{1}
	cur.Players[0].Present = true
	cur.Players[1].Present = true
	cur.Players[1].Watcher = true
	cur.Players[1].UnitSlotStart = 2
	cur.Players[2].Present = true
	cur.Players[2].UnitSlotStart = 3
	cur.Players[0].Allies[2] = true
	cur.Units = []frame.UnitView{
		{Slot: 1, Owner: 0, DefName: "builder"},
		{Slot: 2, Owner: 1, X: whole(50), DirectVisibilityKnown: true, DirectlyVisible: true},
		{Slot: 3, Owner: 2, Z: whole(50), DirectVisibilityKnown: true, DirectlyVisible: true},
	}
	if got := communityPreviewFacing(cur, cat, def, units.FacingNorth, 0, 0); got != units.FacingNorth {
		t.Fatalf("filtered player changed facing: got %d", got)
	}
}

func whole(v int64) numeric.Fixed { return numeric.Fixed(v << 16) }
