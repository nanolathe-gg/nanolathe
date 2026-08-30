package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func selectionFixed(v int64) numeric.Fixed { return numeric.Fixed(v * 65536) }

func selectionModel() *compiledmodel.Model {
	return &compiledmodel.Model{
		Root: 0,
		Pieces: []compiledmodel.Piece{
			{
				Name: "base", Parent: -1, Selection: true,
				Vertices: [][3]numeric.Fixed{
					{0, 0, 0}, {selectionFixed(4), 0, 0},
					{selectionFixed(4), 0, selectionFixed(4)}, {selectionFixed(2), 0, selectionFixed(6)},
					{0, 0, selectionFixed(4)},
				},
				Primitives: []compiledmodel.Primitive{{VertexIndices: []uint16{0, 1, 2, 3, 4}}},
			},
			{
				Name: "turret", Parent: 0, Selection: true,
				Translate: [3]numeric.Fixed{selectionFixed(10), selectionFixed(2), selectionFixed(5)},
				Vertices: [][3]numeric.Fixed{
					{0, 0, 0}, {selectionFixed(2), 0, 0},
					{selectionFixed(2), 0, selectionFixed(2)}, {0, 0, selectionFixed(2)},
				},
				Primitives: []compiledmodel.Primitive{{VertexIndices: []uint16{0, 1, 2, 3}}},
			},
		},
	}
}

func selectionCamera() *camera.Camera {
	return &camera.Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
}

func selectionPose() frame.UnitView {
	return frame.UnitView{
		Slot: 7, X: 0, Y: 0, Z: 0,
		Pieces: []frame.PieceView{{Index: 0}, {Index: 1}},
	}
}

func TestSelectionPlateGeometryUsesNestedPoseAndPreservesEdges(t *testing.T) {
	got := BuildSelectionPlateGeometry(selectionModel(), selectionPose(), selectionCamera(), true, true)
	if len(got) != 2 {
		t.Fatalf("plate count=%d, want 2", len(got))
	}
	if got[0].SourcePiece != 0 || got[0].SourcePrimitive != 0 || got[1].SourcePiece != 1 || got[1].SourcePrimitive != 0 {
		t.Fatalf("source identities=%+v, want declaring pieces and primitive zero", got)
	}
	if len(got[0].ScreenVertices) != 5 || len(got[0].ScreenEdges) != 5 || got[0].ScreenEdges[4] != (SelectionPlateEdge{From: 4, To: 0}) {
		t.Fatalf("n-gon vertices/edges=%d/%d, closing edge=%+v", len(got[0].ScreenVertices), len(got[0].ScreenEdges), got[0].ScreenEdges[len(got[0].ScreenEdges)-1])
	}
	if len(got[1].ScreenVertices) != 4 || len(got[1].ScreenEdges) != 4 {
		t.Fatalf("nested plate vertices/edges=%d/%d, want 4/4", len(got[1].ScreenVertices), len(got[1].ScreenEdges))
	}
	for i, edge := range got[1].ScreenEdges {
		if edge.From != i || edge.To != (i+1)%4 {
			t.Fatalf("edge %d=%+v, want %d→%d", i, edge, i, (i+1)%4)
		}
	}
	// Child origin is root origin plus authored parent translation, model
	// (10,2,5) at a unit sitting on the world origin. Model space is mirrored
	// in Z, so the screen Y lane takes the negated model Z and the half-height
	// shear: -5 - (2 >> 1) + 32 = 26 [03 §2.4][R-RAST-01 §2][R-WATER-01 §1].
	//
	// These expectations were corrected: they previously read the model Z with
	// its plain sign (5 - 1 + 32 = 36), which is the world-point projection
	// applied to a model-space vertex. That mirrored every plate against the
	// body it belongs to. See the call site in selection_plate.go.
	if got[1].ScreenVertices[0] != [2]int32{138, 26} {
		t.Fatalf("nested first vertex=%v, want [138 26]", got[1].ScreenVertices[0])
	}
	if got[1].Bounds != (Rect{MinX: 138, MinY: 24, MaxX: 140, MaxY: 26}) {
		t.Fatalf("nested bounds=%+v, want raw authored bounds", got[1].Bounds)
	}
	if got[0].FrontKey != got[1].FrontKey || got[1].FrontKey != 32 {
		t.Fatalf("front keys=%d,%d, want shared unit-origin screen Y 32", got[0].FrontKey, got[1].FrontKey)
	}
	if !got[1].Visible || !got[1].Selectable || !got[1].Valid || got[1].Fallback {
		t.Fatalf("nested status=%+v, want visible selectable valid", got[1])
	}
}

func TestSelectionPlateGeometryReusesHeadingPitchBankAndCOBState(t *testing.T) {
	m := selectionModel()
	pose := selectionPose()
	cam := selectionCamera()
	base := BuildSelectionPlateGeometry(m, pose, cam, true, true)
	if len(base) != 2 {
		t.Fatalf("base plate count=%d, want 2", len(base))
	}
	for _, test := range []struct {
		name   string
		update func(*frame.UnitView)
	}{
		{name: "heading", update: func(v *frame.UnitView) { v.Heading = 16384 }},
		{name: "pitch", update: func(v *frame.UnitView) { v.Pitch = 16384 }},
		{name: "bank", update: func(v *frame.UnitView) { v.Bank = 16384 }},
		{name: "cob", update: func(v *frame.UnitView) { v.Pieces[1].Tx = selectionFixed(4) }},
	} {
		changed := pose
		changed.Pieces = append([]frame.PieceView(nil), pose.Pieces...)
		test.update(&changed)
		got := BuildSelectionPlateGeometry(m, changed, cam, true, true)
		if reflect.DeepEqual(base, got) {
			t.Errorf("%s pose did not change projected selection geometry", test.name)
		}
	}
	// Identical committed input is deterministic and does not retain mutable
	// state between computations [03 §2.4][03 §5.2].
	repeat := BuildSelectionPlateGeometry(m, pose, cam, true, true)
	if !reflect.DeepEqual(base, repeat) {
		t.Fatal("identical committed input produced different plate geometry")
	}
}

func TestSelectionPlateGeometryHonorsHiddenPieceAndAncestor(t *testing.T) {
	pose := selectionPose()
	pose.Pieces[1].Hidden = true
	got := BuildSelectionPlateGeometry(selectionModel(), pose, selectionCamera(), true, true)
	if got[1].Valid != true || got[1].Visible || got[1].Selectable || len(got[1].ScreenVertices) != 0 {
		t.Fatalf("hidden child geometry=%+v, want valid but non-visible/non-selectable", got[1])
	}
	pose = selectionPose()
	pose.Pieces[0].Hidden = true
	got = BuildSelectionPlateGeometry(selectionModel(), pose, selectionCamera(), true, true)
	if got[1].Visible || got[1].Selectable {
		t.Fatalf("hidden ancestor status=%+v, want suppressed", got[1])
	}
}

func TestSelectionPlateGeometryExposesRawBounds(t *testing.T) {
	m := selectionModel()
	pose := selectionPose()
	pose.X = selectionFixed(-128)
	pose.Z = selectionFixed(-32)
	cam := &camera.Camera{ViewW: 3, ViewH: 3}
	got := BuildSelectionPlateGeometry(m, pose, cam, true, true)
	if got[0].ScreenVertices[0] != [2]int32{0, 0} {
		t.Fatalf("translated vertex=%v, want [0 0]", got[0].ScreenVertices[0])
	}
	// The plate reaches up-screen from the unit, not down: model +Z projects to
	// negative screen Y through the handedness flip [R-RAST-01 §2]. These
	// expectations were corrected with the call site; they previously read the
	// model Z with its plain sign.
	if got[0].Bounds != (Rect{MinX: 0, MinY: -6, MaxX: 4, MaxY: 0}) {
		t.Fatalf("raw bounds=%+v, want [0,-6]..[4,0]", got[0].Bounds)
	}
	// Viewport/HUD-rail clipping is deferred to OTA-SEL-02; this helper exposes
	// the raw projection even when Camera.ViewW/ViewH are smaller than the plate.
	if got[0].ScreenVertices[2] != [2]int32{4, -4} {
		t.Fatalf("unclipped vertex=%v, want [4 -4]", got[0].ScreenVertices[2])
	}
}

func TestSelectionPlateGeometryNoPrimitiveIsExplicitFallback(t *testing.T) {
	m := &compiledmodel.Model{Root: 0, Pieces: []compiledmodel.Piece{{Name: "base", Parent: -1}}}
	got := BuildSelectionPlateGeometry(m, selectionPose(), selectionCamera(), true, true)
	if len(got) != 1 || !got[0].Fallback || got[0].Valid || got[0].Selectable || got[0].SourcePiece != -1 || got[0].SourcePrimitive != -1 {
		t.Fatalf("fallback=%+v, want explicit invalid marker", got)
	}
	if got[0].Diagnostic == "" || len(got[0].ScreenVertices) != 0 || !got[0].Bounds.IsEmpty() {
		t.Fatalf("fallback lacks diagnostic/empty geometry: %+v", got[0])
	}
}

func TestSelectionPlateGeometryInvalidResultsHaveEmptyBounds(t *testing.T) {
	hidden := selectionPose()
	hidden.Pieces[1].Hidden = true
	cases := []struct {
		name    string
		got     []SelectionPlateGeometry
		invalid []int
		valid   []int
	}{
		{name: "nil model", got: BuildSelectionPlateGeometry(nil, selectionPose(), selectionCamera(), true, true), invalid: []int{0}},
		{name: "nil camera", got: BuildSelectionPlateGeometry(selectionModel(), selectionPose(), nil, true, true), invalid: []int{0}},
		{name: "no authored primitive", got: BuildSelectionPlateGeometry(&compiledmodel.Model{Root: 0, Pieces: []compiledmodel.Piece{{Parent: -1}}}, selectionPose(), selectionCamera(), true, true), invalid: []int{0}},
		{name: "hidden piece", got: BuildSelectionPlateGeometry(selectionModel(), hidden, selectionCamera(), true, true), invalid: []int{1}, valid: []int{0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.got) == 0 {
				t.Fatal("no geometry result")
			}
			for _, i := range tc.invalid {
				if i >= len(tc.got) {
					t.Fatalf("invalid result index %d absent from %d results", i, len(tc.got))
				}
				if !tc.got[i].Bounds.IsEmpty() {
					t.Fatalf("invalid result %d bounds=%+v, want explicit empty bounds", i, tc.got[i].Bounds)
				}
			}
			for _, i := range tc.valid {
				if i >= len(tc.got) {
					t.Fatalf("valid result index %d absent from %d results", i, len(tc.got))
				}
				if tc.got[i].Bounds.IsEmpty() || !tc.got[i].Valid {
					t.Fatalf("unaffected result %d=%+v, want valid non-empty geometry", i, tc.got[i])
				}
			}
		})
	}
}
