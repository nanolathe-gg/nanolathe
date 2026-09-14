package client

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// Enhanced policy: the cursor anchor must survive each displayed fraction,
// including the final settle and the change of recording step at native.
func TestZoomMotionKeepsAnchorAndMovesMonotonically(t *testing.T) {
	for _, targets := range [][]camera.Zoom{{camera.ZoomMax, camera.ZoomUnit, camera.ZoomUnit / 4, camera.ZoomUnit}, {camera.ZoomUnit / 4, camera.ZoomMax}} {
		c := zoomRecorderClient(t)
		c.cam.X, c.cam.Z = 4000, 4000
		c.SetInterpolation(true)
		for tick := uint32(1); tick <= 2; tick++ {
			c.buffer.BeginWrite()
			if err := c.buffer.Publish(tick); err != nil {
				t.Fatal(err)
			}
			c.sampleCameraOrigin()
		}
		const ax, ay = 437, 271
		var controller camera.ZoomController
		for _, target := range targets {
			start := c.cam.PresentationView()
			wx, wz := start.X+ax/start.Factor, start.Z+ay/start.Factor
			controller.SetTarget(c.cam, ax+camera.OriginX, ay+camera.OriginY, target)
			lastFactor := start.Factor
			direction := 1.0
			if target.Float() < start.Factor {
				direction = -1
			}
			for step := 0; step < 80; step++ {
				active := controller.Step(c.cam)
				c.sampleCameraOrigin()
				saved := *c.cam
				for _, f := range []float32{0, .25, .5, .75, .9999} {
					c.SetCameraFraction(f)
					if !c.beginCameraBlend() {
						t.Fatal("no camera blend")
					}
					c.refreshRecordExtent()
					w := c.worldSpace(true)
					factor := float64(w.Factor)
					px := (wx-float64(c.cam.X))*factor + float64(w.OffsetX)
					py := (wz-float64(c.cam.Z))*factor + float64(w.OffsetY)
					if math.Abs(px-ax) > .001 || math.Abs(py-ay) > .001 {
						t.Fatalf("target %v step %d fraction %g: anchor (%g,%g)", target, step, f, px, py)
					}
					if (factor-lastFactor)*direction < -1e-7 {
						t.Fatalf("zoom reversed: %g -> %g", lastFactor, factor)
					}
					lastFactor = factor
					if float64(w.RecordW)*factor/float64(c.cam.EffectiveScale().Norm())*2+float64(w.OffsetX) < float64(c.width) {
						t.Fatal("record extent does not cover view")
					}
					c.endCameraBlend(true)
					if *c.cam != saved {
						t.Fatal("recording changed live camera")
					}
				}
				if !active {
					break
				}
				if step == 79 {
					t.Fatal("zoom did not settle")
				}
			}
		}
	}
}

func TestPausedZoomDigestIncludesSubpixelMotion(t *testing.T) {
	c := zoomRecorderClient(t)
	for tick := uint32(1); tick <= 2; tick++ {
		c.buffer.BeginWrite()
		if err := c.buffer.Publish(tick); err != nil {
			t.Fatal(err)
		}
	}
	c.SetInterpolation(true)
	c.SetPresentationPaused(true)
	c.camSamples = 2
	c.camPrevView = camera.PresentationView{X: 100, Z: 100, Factor: 1}
	c.camCurView = camera.PresentationView{X: 100.25, Z: 100, Factor: 1}
	c.SetCameraFraction(.25)
	a, ok := c.PausedWorldDigest()
	if !ok {
		t.Fatal("no paused digest")
	}
	c.SetCameraFraction(.5)
	b, ok := c.PausedWorldDigest()
	if !ok {
		t.Fatal("no paused digest")
	}
	if a == b {
		t.Fatal("paused cache reused a different subpixel view")
	}
	c.camPrevView = c.camCurView
	a, _ = c.PausedWorldDigest()
	c.SetCameraFraction(.75)
	b, _ = c.PausedWorldDigest()
	if a != b {
		t.Fatal("stationary paused view invalidated by fraction alone")
	}
}

func TestStrategicZoomUsesSubmittedTransform(t *testing.T) {
	for _, factors := range [][2]float64{{.4, .5}, {.49, .51}} {
		t.Run("submitted", func(t *testing.T) { testStrategicSubmittedZoom(t, factors[0], factors[1]) })
	}
}

func testStrategicSubmittedZoom(t *testing.T, from, to float64) {
	c, f := iconLayoutFixture(t)
	buffer := frame.NewBuffer()
	for tick := uint32(1); tick <= 2; tick++ {
		dst := buffer.BeginWrite()
		*dst = *f
		if err := buffer.Publish(tick); err != nil {
			t.Fatal(err)
		}
	}
	c.SetSnapshot(buffer)
	c.SetInterpolation(true)
	c.camSamples = 2
	c.camPrevView = camera.PresentationView{Factor: from}
	c.camCurView = camera.PresentationView{X: 50, Z: 40, Factor: to}
	c.cam.Zoom = camera.ZoomUnit / 2
	c.SetCameraFraction(.25)
	c.drawStrategicMarkers(buffer.Current())
	if len(c.markerArena) == 0 {
		t.Fatal("missing markers")
	}
	m := c.markerArena[0]
	c.CommitStrategicPresentation()
	c.SetCameraFraction(.9)
	if _, _, ok := c.PickPresentedUnit(buffer.Current(), m.X, m.Y, 0); !ok {
		t.Fatal("picker did not use submitted zoom and translation")
	}
}
