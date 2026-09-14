package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// allocationFeatureClient is one 3DO feature — a wreck — and the modern
// recorder that draws it.
func allocationFeatureClient() (*Client, frame.FeatureView) {
	c := testModelTextureClient()
	c.models = map[string]*unitModel{"feature": {compiled: &compiledmodel.Model{Pieces: []compiledmodel.Piece{{
		// A root piece has no parent; the compiler stores -1 [03 §2.4].
		Parent:     -1,
		Primitives: []compiledmodel.Primitive{{ColorIndex: 56, IsColored: 1, VertexIndices: []uint16{0, 1, 2, 3}}},
		Vertices:   [][3]numeric.Fixed{{0, 0, 0}, {1 << 16, 0, 0}, {1 << 16, 0, -1 << 16}, {0, 0, -1 << 16}},
	}}}}}
	c.modelScratch.active = true
	c.geometryOnlyModels = true
	c.recordModelGeometry = true
	return c, frame.FeatureView{Model: "feature", InstanceID: 3}
}

// Every wreck on the map is recorded on every frame, so the feature draw must
// borrow the frame's pooled scratch the way a unit does rather than allocate a
// fresh state, transform, piece and vertex arena per feature per frame
// (DESIGN_GPU_RENDERER.md §11.5 "CPU"). This fixture measured sixteen
// allocations per feature draw while the feature path built its own scratch.
func TestFeatureModelRecordingBorrowsPooledScratch(t *testing.T) {
	c, v := allocationFeatureClient()
	// Warm the pool: the first frames grow the slots the steady state reuses.
	for i := 0; i < 4; i++ {
		c.modelScratch.reset()
		if !c.drawFeatureModel(v) {
			t.Fatal("the feature model was suppressed")
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		c.modelScratch.reset()
		c.drawFeatureModel(v)
	})
	if allocs != 0 {
		t.Fatalf("warm feature draw allocated %v times per frame, want 0", allocs)
	}
}

// The reset that begins a frame has to rewind the draw slots the feature path
// now borrows, or the pool would grow without bound across frames.
func TestFrameResetRewindsBorrowedDrawScratch(t *testing.T) {
	c, v := allocationFeatureClient()
	for i := 0; i < 3; i++ {
		c.modelScratch.reset()
		for k := 0; k < 5; k++ {
			if !c.drawFeatureModel(v) {
				t.Fatal("the feature model was suppressed")
			}
		}
		if c.modelScratch.drawNext != 5 {
			t.Fatalf("frame %d borrowed %d draw slots for five features", i, c.modelScratch.drawNext)
		}
		if len(c.modelScratch.draws) != 5 {
			t.Fatalf("frame %d holds %d draw slots, want the five it reuses", i, len(c.modelScratch.draws))
		}
	}
}
