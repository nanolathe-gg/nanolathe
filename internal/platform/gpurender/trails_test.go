package gpurender

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// TestTrailsReplayThroughOptionalSink locks the draw-list contract of §15: a
// sink without the hook replays the frame unchanged, and one with it receives
// the batch in record order.
func TestTrailsReplayThroughOptionalSink(t *testing.T) {
	var list drawlist.List
	list.RecordClear()
	list.RecordTrails(drawlist.Trails{Marks: []drawlist.Trail{{X: 1, Y: 2, AxisX: 256, CrossY: 256, Strength: 90}}})
	list.RecordExpand()
	clone := list.Clone()
	list.Reset()
	r := &Renderer{}
	// A nil-surface renderer accepts the batch and draws nothing; the point is
	// that the call arrives through the hook.
	var got int
	var sink drawlist.TrailSink = trailCounter{&got}
	clone.Replay(struct {
		drawlist.Sink
		drawlist.TrailSink
	}{r, sink})
	if got != 1 {
		t.Fatalf("trail batches replayed through the hook: %d, want 1", got)
	}
}

type trailCounter struct{ n *int }

func (c trailCounter) Trails(drawlist.Trails) { *c.n++ }

// checkTrailDevicePixels darkens a flat grey field with one footprint and one
// track segment and reads the result back: the terrain outside a mark is
// untouched, the mark's centre is scaled by 1 − strength, and the track's
// orientation follows its axis (§15).
func checkTrailDevicePixels() error {
	pal := fixturePalette()
	const w, h, field = 64, 32, 200
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: w, H: h}, Index: field, Style: drawlist.FillSolid})
	list.RecordTrails(drawlist.Trails{Marks: []drawlist.Trail{
		// A footprint at (16,16), 3 px half-length along +x, 2 px half-width.
		{X: 16, Y: 16, AxisX: 3 * 256, AxisY: 0, CrossX: 0, CrossY: 2 * 256, Shape: drawlist.TrailFootprint, Strength: 255},
		// Smallest geometry-derived tip: two pixels in both dimensions. A
		// one-pixel oval centred at an integer misses native raster samples.
		{X: 32, Y: 8, AxisX: 256, CrossY: 256, Shape: drawlist.TrailFootprint, Strength: 102},
		// A vertical track segment at (48,16): 8 px half-length along +y, 1.5 px half-width.
		{X: 48, Y: 16, AxisX: 0, AxisY: 8 * 256, CrossX: -384, CrossY: 0, Shape: drawlist.TrailTrack, Strength: 128},
	}})
	list.RecordExpand()
	img := r.Execute(&list, w, h)
	if img == nil {
		return fmt.Errorf("trail device fixture returned no image")
	}
	pixels := make([]byte, w*h*4)
	img.ReadPixels(pixels)
	at := func(x, y int) int { return int(pixels[(y*w+x)*4]) }
	if err := checkExactIndex("untouched field", pixels, (2*w+2)*4, &pal, field); err != nil {
		return err
	}
	// Footprint: the centre is fully darkened (scale 0 at strength 255), and
	// 3 px across (beyond the 2 px half-width) the field is untouched.
	if c := at(16, 16); c > field/8 {
		return fmt.Errorf("footprint centre reads %d, want near 0 for a full-strength mark on %d", c, field)
	}
	if c := at(16, 20); c != field {
		return fmt.Errorf("outside the footprint across reads %d, want the untouched field %d", c, field)
	}
	if c := at(21, 16); c != field {
		return fmt.Errorf("outside the footprint along reads %d, want the untouched field %d", c, field)
	}
	if c := at(32, 8); c >= field {
		return fmt.Errorf("minimum footprint vanished at native scale: centre=%d, field=%d", c, field)
	}
	// Track: half strength halves the field at the centre line for its whole
	// length, and the sides are untouched 3 px away.
	for _, y := range []int{9, 16, 23} {
		if c := at(48, y); c < field/2-6 || c > field/2+6 {
			return fmt.Errorf("track centre at y=%d reads %d, want about %d", y, c, field/2)
		}
	}
	if c := at(52, 16); c != field {
		return fmt.Errorf("beside the track reads %d, want the untouched field %d", c, field)
	}
	if c := at(48, 27); c != field {
		return fmt.Errorf("past the track's end reads %d, want the untouched field %d", c, field)
	}
	return nil
}

// TestTrailDeviceFixture is opt-in because ordinary tests must not require a
// graphics device (C-G10); the hidden loop in TestMain runs the check.
func TestTrailDeviceFixture(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device trail fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}

// TestTrailsCompileIsAllocationFree locks the §11.2 allocation policy for the
// mark batch. The scheduler needs the batch's rectangle before any quad is
// appended, so the marks are visited twice; neither pass may keep a list, which
// is what the per-frame corner, strength and shape slices used to be.
func TestTrailsCompileIsAllocationFree(t *testing.T) {
	r, _ := schedulerFixture(t)
	marks := make([]drawlist.Trail, 64)
	for i := range marks {
		marks[i] = drawlist.Trail{
			X: int32(i % 60), Y: int32((i * 7) % 60),
			AxisX: 512, AxisY: 128, CrossX: -128, CrossY: 512,
			Strength: uint8(20 + i%200),
		}
	}
	// Two marks that contribute nothing: a faded one and a degenerate one. Both
	// passes must agree on skipping them, or the emitted quads would not match
	// the rectangle the bounds pass measured.
	marks[3].Strength = 0
	marks[9].AxisX, marks[9].AxisY = 0, 0
	compile := func() {
		r.sched.resetFrame(64, 64)
		r.Trails(drawlist.Trails{Marks: marks})
	}
	compile()
	if got, want := len(r.sched.classVerts(schedDest)), (len(marks)-2)*quadVertices; got != want {
		t.Fatalf("the mark batch compiled %d vertices, want %d (one quad per contributing mark)", got, want)
	}
	if got := testing.AllocsPerRun(8, compile); got != 0 {
		t.Fatalf("the mark batch allocated %v objects per frame, want none", got)
	}
}
