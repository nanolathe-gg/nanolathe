package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/model"
)

func TestPieceLaneUsesPublishedCachePolarity(t *testing.T) {
	if !PieceLaneCached.Includes(false, false) || PieceLaneCached.Includes(true, false) {
		t.Fatal("cached lane did not retain the published cache polarity")
	}
	if PieceLaneLive.Includes(false, false) || !PieceLaneLive.Includes(true, false) {
		t.Fatal("live lane did not retain the published cache polarity")
	}
	for _, lane := range []PieceLane{PieceLaneCached, PieceLaneLive, PieceLaneAll} {
		if !lane.Includes(false, true) || !lane.Includes(true, true) {
			t.Fatalf("construction did not admit every piece in lane %d", lane)
		}
	}
}

func TestBuildUnitDrawDoesNotAdvanceOrientationBeforeBodyRebuild(t *testing.T) {
	m := &model.Model{Pieces: []model.Piece{{Name: "cached", Parent: -1}, {Name: "live", Parent: -1}}, Root: 0, Name: "lanes"}
	base := []model.PieceState{{}, {DontCache: true}}
	cache := &OrientationCache{}
	draw := BuildUnitDraw(m, base, 0, 0, 0, frame.UnitView{}, cache)
	if cache.Valid {
		t.Fatal("pose construction advanced the retained orientation reference")
	}
	if len(draw.Pieces) != 2 || draw.Pieces[0].DontCache || !draw.Pieces[1].DontCache {
		t.Fatalf("published cache lanes = %#v", draw.Pieces)
	}
	cache.UpdateKey(m.Name, 0, 0, 0)
	draw = BuildUnitDraw(m, base, 7, 0, 0, frame.UnitView{}, cache)
	if draw.PieceStates[0].RotY != 0 {
		t.Fatal("sub-threshold unit rotation reached the piece transform")
	}
	if draw.NeedsRebuild {
		t.Fatal("exactly seven scheduled a cached-body rebuild")
	}
	draw = BuildUnitDraw(m, base, 8, 0, 0, frame.UnitView{}, cache)
	if draw.PieceStates[0].RotY != 8 {
		t.Fatal("threshold-crossing unit rotation did not reach the piece transform")
	}
	if !draw.NeedsRebuild {
		t.Fatal("eight did not schedule a cached-body rebuild")
	}
	if cache.Heading != 0 {
		t.Fatal("pose construction refreshed the cache before the consumer rebuilt")
	}
}
