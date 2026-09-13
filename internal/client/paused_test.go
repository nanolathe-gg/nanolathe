package client

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

func pausedClient(t *testing.T) *Client {
	c, _ := pipelineClient(t)
	c.SetCamera(&camera.Camera{ViewW: 320, ViewH: 240, MapW: 4096, MapH: 4096})
	c.SetPresentationPaused(true)
	c.SetTickFraction(0.5)
	c.Step(0)
	c.Step(0)
	return c
}

func TestPausedWorldIgnoresUIEpochAndStationaryCameraFraction(t *testing.T) {
	c := pausedClient(t)
	c.SetCameraFraction(0.25)
	want, ok := c.PausedWorldDigest()
	if !ok {
		t.Fatal("paused fixture is ineligible")
	}
	c.BumpPresentationEpoch()
	c.SetSelectionDrag(SelectionDrag{Active: true, EndX: 100, EndY: 90})
	c.SetCameraFraction(0.75)
	got, ok := c.PausedWorldDigest()
	if !ok || got != want {
		t.Fatal("cursor/UI update or stationary camera fraction invalidated the world")
	}
	c.cam.X = 24
	c.Step(0)
	c.SetCameraFraction(0.25)
	first, _ := c.PausedWorldDigest()
	c.SetCameraFraction(0.75)
	second, _ := c.PausedWorldDigest()
	if first == second {
		t.Fatal("moving camera fractions reused a world raster")
	}
}

func TestPausedWorldInvalidationAndRandomProjectileFallback(t *testing.T) {
	c := pausedClient(t)
	before, _ := c.PausedWorldDigest()
	c.cam.Zoom = camera.ZoomUnit * 3 / 4
	after, _ := c.PausedWorldDigest()
	if before == after {
		t.Fatal("zoom reused the old raster")
	}
	before = after
	c.SetShadowOptions(false, false, false)
	c.SetFeatureShadows(!c.FeatureShadows())
	after, _ = c.PausedWorldDigest()
	if before == after {
		t.Fatal("render preferences reused the old raster")
	}
	before = after
	c.SetGlow(!c.Glow())
	after, _ = c.PausedWorldDigest()
	if before == after {
		t.Fatal("glow switch reused the old raster")
	}
	before = after
	c.SetModelFS(nil)
	after, _ = c.PausedWorldDigest()
	if before == after {
		t.Fatal("asset rebind reused the old raster")
	}
	f := c.buffer.BeginWrite()
	f.Projectiles = append(f.Projectiles, frame.ProjectileView{RenderType: render.RenderTypeSegmented})
	if err := c.buffer.Publish(3); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.PausedWorldDigest(); ok {
		t.Fatal("render-time projectile randomness was frozen")
	}
	c.SetPresentationPaused(false)
	if _, ok := c.PausedWorldDigest(); ok {
		t.Fatal("unpaused world eligible for reuse")
	}
	c.SetPresentationPaused(true)
	c.SetSnapshot(frame.NewBuffer())
	if c.PresentationPaused() {
		t.Fatal("a replaced session retained pause truth")
	}
}

func TestPausedBlendReusesPoseUntilPublication(t *testing.T) {
	c := pausedClient(t)
	f := c.presentationFrame()
	// Poison retained scratch to distinguish rebuilding from retaining without
	// relying on timings or allocation counts. It is never a committed frame.
	c.interp.units[0].Model = "retained-blend"
	if c.presentationFrame() != f || c.interp.units[0].Model != "retained-blend" {
		t.Fatal("unchanged paused pose was rebuilt")
	}
	next := c.buffer.BeginWrite()
	next.Units = append(next.Units, unitAt(1, wu(30), 0, 0))
	if err := c.buffer.Publish(3); err != nil {
		t.Fatal(err)
	}
	if c.presentationFrame().Units[0].Model == "retained-blend" {
		t.Fatal("new publication retained stale pose")
	}
}

func TestPausedEntryCancelsSpeculationAndRestoresCRT(t *testing.T) {
	c := pausedClient(t)
	seed := rng.NewCRT(1234)
	c.SetPresentationCRT(&seed)
	before := *c.crt
	c.StartPreRecord(ClampTickFraction16(0.5), 0, false)
	c.JoinPreRecord()
	c.crt.Rand()
	c.CancelPreRecord()
	if c.pre.pending || *c.crt != before {
		t.Fatal("paused entry retained speculative work or CRT advance")
	}
	c.CancelPreRecord()
}

type pausedForegroundFixture struct{ x, calls int }

func (s *pausedForegroundFixture) DrawUI(c *Client, f UIFrame) {
	s.calls++
	c.UIFillRect(s.x, 80, 12, 8, 41)
	c.UIShadeRect(c.pal, 30, 60, 70, 40, 2)
}

// The split preserves world-before-interface order, destination-reading UI,
// and removal of the old gesture. This fixture uses the shared classic sink to
// compare exact indexed bytes; the device fixture checks the Enhanced raster.
func TestPausedSplitKeepsForegroundLiveAndRasterOrder(t *testing.T) {
	c, source := viewScaleScene(t)
	c.SetCamera(&camera.Camera{ViewW: 320, ViewH: 240, MapW: 4096, MapH: 4096})
	f := c.buffer.BeginWrite()
	*f = *source
	if err := c.buffer.Publish(1); err != nil {
		t.Fatal(err)
	}
	stage := &pausedForegroundFixture{x: 20}
	c.SetUIStage(stage)
	c.SetSelectionDrag(SelectionDrag{Active: true, StartX: 150, StartY: 70, EndX: 190, EndY: 130})
	c.Frame()
	want := bytes.Clone(c.indexed)
	c.pausedLayer = pausedWorldLayer
	c.Frame()
	world := bytes.Clone(c.indexed)
	c.pausedLayer = pausedForegroundLayer
	c.Frame()
	if !bytes.Equal(c.indexed, want) {
		t.Fatal("split frame differs from the whole composition")
	}
	stage.x = 70
	c.SetSelectionDrag(SelectionDrag{})
	copy(c.indexed, world)
	c.Frame()
	got := bytes.Clone(c.indexed)
	c.pausedLayer = pausedWholeFrame
	c.Frame()
	if !bytes.Equal(c.indexed, got) {
		t.Fatal("foreground move/removal retained old pixels")
	}
	if bytes.Equal(got, want) || stage.calls != 4 {
		t.Fatal("foreground did not update on every presentation")
	}
}

func TestPausedForegroundOmitsWorldModelPreparation(t *testing.T) {
	c, source := viewScaleScene(t)
	c.SetCamera(&camera.Camera{ViewW: 320, ViewH: 240, MapW: 4096, MapH: 4096})
	f := c.buffer.BeginWrite()
	*f = *source
	if err := c.buffer.Publish(1); err != nil {
		t.Fatal(err)
	}
	stage := &pausedForegroundFixture{x: 20}
	c.SetUIStage(stage)
	wholeModels := len(c.RecordModernFrame().ModelCommands())
	if wholeModels == 0 {
		t.Fatal("fixture has no world model")
	}
	if got := len(c.RecordPausedWorld().ModelCommands()); got != wholeModels {
		t.Fatalf("paused world recorded %d model commands, whole frame recorded %d", got, wholeModels)
	}
	for range 3 {
		if got := len(c.RecordPausedForeground().ModelCommands()); got != 0 {
			t.Fatalf("paused foreground prepared %d world model commands", got)
		}
	}
	if stage.calls != 4 {
		t.Fatal("world preparation or foreground redraw count changed")
	}
}
