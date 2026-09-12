package client

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func waterMetadataClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(Options{Width: 32, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCamera(&camera.Camera{ViewW: 32, ViewH: 24, MapW: 64, MapH: 64, Scale: camera.ViewScaleNative, Zoom: camera.ZoomUnit})
	c.SetTerrain(&world.Terrain{CellW: 4, CellH: 4, TileIndices: []uint16{0, 0, 0, 0}, TileSet: make([][1024]byte, 1)})
	for i := range c.terrain.TileSet[0] {
		c.terrain.TileSet[0][i] = byte(i%251 + 1)
	}
	f := c.buffer.BeginWrite()
	f.Wind = frame.WindView{Heading: 49152, Strength: 7250}
	if err := c.buffer.Publish(17); err != nil {
		t.Fatal(err)
	}
	return c
}

func recordedWater(t *testing.T, c *Client) drawlist.WaterSurface {
	t.Helper()
	c.list.Reset()
	c.drawTerrainPrep()
	var capture scaleCapture
	c.list.Replay(&capture)
	if len(capture.terrain) != 1 {
		t.Fatalf("terrain commands = %d", len(capture.terrain))
	}
	return capture.terrain[0].Water
}

// Enhanced water is an authored visual rule, gated at the recording boundary.
// Wind is copied without interpolation, while capture sampling is tick-exact.
func TestWaterMetadataEnhancedGateAndCapturePhase(t *testing.T) {
	c := waterMetadataClient(t)
	c.SetTickFraction(0.75)
	if got := recordedWater(t, c); got != (drawlist.WaterSurface{}) {
		t.Fatalf("classic enabled water: %+v", got)
	}
	c.enhanced = true
	want := drawlist.WaterSurface{Enabled: true, Tick: 17, WindHeading: 49152, WindStrength: 7250, Energy: 1}
	if got := recordedWater(t, c); got != want {
		t.Fatalf("capture water = %+v, want %+v", got, want)
	}
	c.SetInterpolation(true)
	want.Fraction16 = ClampTickFraction16(0.75)
	if got := recordedWater(t, c); got != want {
		t.Fatalf("interpolated water = %+v, want %+v", got, want)
	}
	c.cam.Zoom = camera.ZoomUnit / 2
	if got := recordedWater(t, c); got != (drawlist.WaterSurface{}) {
		t.Fatalf("strategic view enabled water: %+v", got)
	}
}

func TestWaterMetadataPauseRerecordAndCloneRetainPhase(t *testing.T) {
	c := waterMetadataClient(t)
	c.enhanced = true
	c.SetInterpolation(true)
	c.SetTickFraction(0.375)
	want := recordedWater(t, c)
	saved := c.list.Clone()
	c.SetPresentationPaused(true)
	c.pausedLayer = pausedWorldLayer
	c.cam.X++
	if got := recordedWater(t, c); got != want {
		t.Fatalf("paused camera redraw changed water phase: %+v, want %+v", got, want)
	}
	c.SetTickFraction(0.875)
	f := c.buffer.BeginWrite()
	f.Wind = frame.WindView{Heading: 123, Strength: 400}
	if err := c.buffer.Publish(18); err != nil {
		t.Fatal(err)
	}
	recordedWater(t, c)
	var capture scaleCapture
	saved.Replay(&capture)
	if capture.terrain[0].Water != want {
		t.Fatalf("clone followed later recording: %+v", capture.terrain[0].Water)
	}
}

func TestClassicTerrainIgnoresEnhancedWaterMetadata(t *testing.T) {
	c := waterMetadataClient(t)
	recordedWater(t, c)
	c.list.Replay(c.classicSink())
	want := bytes.Clone(c.indexed)
	c.enhanced = true
	recordedWater(t, c)
	clear(c.indexed)
	c.list.Replay(c.classicSink())
	if !bytes.Equal(c.indexed, want) {
		t.Fatal("water metadata changed classic terrain pixels")
	}
}
