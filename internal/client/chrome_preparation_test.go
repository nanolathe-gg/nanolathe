package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

type chromePreparationFixture struct {
	want     camera.ChromeInsets
	prepares int
}

func (s *chromePreparationFixture) PrepareUI(c *Client) {
	s.prepares++
	c.cam.Chrome = s.want
	c.SnapCameraBlend()
}

func (*chromePreparationFixture) DrawUI(*Client, UIFrame) {}

// Preparation belongs to the joined host boundary, ahead of audio and the
// world's first marker. Pure composition and speculative recording never
// apply a newer layout request (DESIGN_GPU_RENDERER §13.10).
func TestUIChromePreparationStaysAtHostBoundary(t *testing.T) {
	c := zoomRecorderClient(t)
	t.Cleanup(c.Close)
	c.cam.X, c.cam.Z = 1000, 1000
	c.SetAudioService(audio.NewService(nil))
	stage := &chromePreparationFixture{want: camera.ChromeInsets{Left: 257}}
	c.SetUIStage(stage)
	c.BeginPresentationFrame()
	if stage.prepares != 1 || c.AudioViewport().Left != 1257 || c.AudioViewport().Width != (640-257)/16 {
		t.Fatal("host preparation did not precede audio viewport refresh")
	}
	want := drawlist.Rect{X: 257, Y: 32, W: 640 - 257, H: 480 - 64}
	for range 2 {
		spaces := c.RecordModernFrame().WorldSpaces()
		if len(spaces) == 0 || spaces[0].Viewport != want {
			t.Fatalf("first recorded world viewport = %+v, want %+v", spaces, want)
		}
	}
	stage.want.Left = 300
	c.StartPreRecord(0, 0, false)
	c.JoinPreRecord()
	if stage.prepares != 1 || c.cam.Chrome.Left != 257 {
		t.Fatal("pure or speculative recording prepared host layout")
	}
	c.BeginPresentationFrame()
	if stage.prepares != 2 || c.cam.Chrome.Left != 300 {
		t.Fatal("the next host frame did not apply the requested layout")
	}
	if _, ok := c.TakePreRecord(c.PresentationDigest(), 0); ok {
		t.Fatal("host layout change presented a stale speculative viewport")
	}
	spaces := c.RecordModernFrame().WorldSpaces()
	if len(spaces) == 0 || spaces[0].Viewport.X != 300 {
		t.Fatal("synchronous retry recorded the old viewport")
	}
}

// Bounds can change while the origin stays fixed, so neither origin identity
// nor the host's UI epoch can stand in for the insets in the two cache keys.
func TestChromeInsetsInvalidatePresentationCachesWithoutOriginMotion(t *testing.T) {
	for _, chrome := range []camera.ChromeInsets{{Left: 129}, {Top: 33}, {Bottom: 33}} {
		c := pausedClient(t)
		c.cam.X, c.cam.Z = 1000, 1000
		c.SnapCameraBlend()
		before, ok := c.PausedWorldDigest()
		if !ok {
			t.Fatal("paused fixture is ineligible")
		}
		c.StartPreRecord(ClampTickFraction16(0.5), 0, false)
		c.JoinPreRecord()
		old := c.PresentationDigest()
		c.cam.Chrome = chrome
		after, ok := c.PausedWorldDigest()
		if !ok || before == after {
			t.Fatalf("Chrome %+v reused the old paused raster", chrome)
		}
		if before.camX != after.camX || before.camZ != after.camZ {
			t.Fatal("fixture moved the projected camera origin")
		}
		current := c.PresentationDigest()
		if old.CamX != current.CamX || old.CamZ != current.CamZ || old.Epoch != current.Epoch {
			t.Fatal("fixture moved the camera origin or host epoch")
		}
		if _, ok := c.TakePreRecord(current, 0); ok {
			t.Fatalf("Chrome %+v presented the old speculative viewport", chrome)
		}
	}
}
