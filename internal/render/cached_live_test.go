package render

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
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
	draw := BuildUnitDrawInto(m, base, 0, 0, 0, frame.UnitView{}, cache, &DrawScratch{})
	if cache.Valid {
		t.Fatal("pose construction advanced the retained orientation reference")
	}
	if len(draw.Pieces) != 2 || draw.Pieces[0].DontCache || !draw.Pieces[1].DontCache {
		t.Fatalf("published cache lanes = %#v", draw.Pieces)
	}
	cache.UpdateKey(m.Name, 0, 0, 0)
	draw = BuildUnitDrawInto(m, base, 7, 0, 0, frame.UnitView{}, cache, &DrawScratch{})
	if draw.PieceStates[0].RotY != 0 {
		t.Fatal("sub-threshold unit rotation reached the piece transform")
	}
	if draw.NeedsRebuild {
		t.Fatal("exactly seven scheduled a cached-body rebuild")
	}
	draw = BuildUnitDrawInto(m, base, 8, 0, 0, frame.UnitView{}, cache, &DrawScratch{})
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

// A deferred draw reports the eager draw's face and shading verdicts before any
// piece is built, builds only the lane it is asked for, and once every lane is
// materialized carries exactly the eager pieces: the client decides its lanes
// from those verdicts and must not see a different pose.
func TestDeferredUnitDrawMaterializesTheEagerPieces(t *testing.T) {
	previous := Shading
	Shading = true
	t.Cleanup(func() { Shading = previous })

	mdl := otaRND02BModel()
	states := []model.PieceState{{}, {DontCache: true}, {}}
	view := frame.UnitView{BMCode: false}
	eager := BuildUnitDrawInto(mdl, states, 900, 30, 0, view, nil, &DrawScratch{})
	lazy := BuildUnitDrawDeferredInto(mdl, states, 900, 30, 0, view, nil, &DrawScratch{})
	if lazy.HasFaces() != hasAnyPrimitive(eager) || lazy.ShadedFaces() != eager.ShadedFaces() {
		t.Fatalf("deferred verdicts faces=%v shaded=%v, eager %v/%v", lazy.HasFaces(), lazy.ShadedFaces(), hasAnyPrimitive(eager), eager.ShadedFaces())
	}
	lazy.Materialize(PieceLaneLive)
	if !reflect.DeepEqual(lazy.Pieces[1], eager.Pieces[1]) {
		t.Fatal("the live piece differs from the eager build")
	}
	if len(lazy.Pieces[2].WorldVertices) != 0 || len(lazy.Pieces[2].Primitives) != 0 {
		t.Fatal("materializing the live lane built cached pieces")
	}
	lazy.Materialize(PieceLaneAll)
	if !reflect.DeepEqual(lazy.Pieces, eager.Pieces) || !lazy.HasFaces() {
		t.Fatal("a fully materialized deferred draw differs from the eager build")
	}
}

func hasAnyPrimitive(d *UnitDraw) bool {
	for i := range d.Pieces {
		if len(d.Pieces[i].Primitives) != 0 {
			return true
		}
	}
	return false
}
