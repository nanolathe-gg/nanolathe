package main

import (
	"fmt"
	"image"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Replay controls are recorded and clicked in physical screen coordinates,
// beside the magnified chrome and below messages/weather (interface Replays).
func TestReplayOverlayProductionClearsScaledChrome(t *testing.T) {
	for _, scale := range []int{1, 2} {
		for _, weather := range []int{0, 1} {
			t.Run(fmt.Sprintf("scale%d/weather%d", scale, weather), func(t *testing.T) {
				b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
				t.Cleanup(b.cl.Close)
				const width, height = 1280, 1440
				b.cl.Resize(width, height)
				b.cam.ViewW, b.cam.ViewH = width, height
				b.cl.SetCamera(b.cam)
				b.cl.SetEnhanced(true)
				b.cl.SetTerrain(b.sess.World)
				prefs := settings.DefaultPresentation()
				prefs.UIScale, prefs.WeatherReport = scale, weather
				b.hostPresentation = &prefs
				b.fpsVisible = true
				b.sess.Wind = world.NewWind(0, 1)
				b.playback = testPlayback()
				b.sess.Snapshot.BeginWrite()
				if err := b.sess.Snapshot.Publish(0); err != nil {
					t.Fatal(err)
				}
				b.cl.Step(1.0 / 30)
				h := &retailBattleHUD{console: netTestFont()}
				b.hud = h
				b.cl.SetFNT(h.console)
				b.cl.MessageRing().Append("message", 1, 0, 10, 0)
				b.cl.SetUIStage(battleHUDUIStage{hud: h, battle: b})
				b.cl.BeginPresentationFrame()
				trace := &networkRecordingTrace{}
				b.cl.RecordModernFrame().Replay(trace)
				r := b.playback.overlay.backdrop
				left, top, bottom := b.cam.ChromeInset()
				view := image.Rect(int(left), int(top), width, height-int(bottom))
				fps := image.Rect(width-onlineFPSPanelW-onlineFPSPanelMargin, onlineFPSPanelMargin, width-onlineFPSPanelMargin, onlineFPSPanelMargin+onlineFPSPanelH)
				if r.Empty() || !r.In(view) || r.Overlaps(fps) || r.Min.Y < b.cl.MessageColumnBottom() {
					t.Fatalf("replay %v overlaps reserved areas: view %v, FPS %v, messages end %d", r, view, fps, b.cl.MessageColumnBottom())
				}
				if weather != 0 && r.Min.Y < (36+2*int(h.console.Height)+6)*scale {
					t.Fatalf("replay %v overlaps scaled weather report", r)
				}
				found := false
				for _, fill := range trace.fills {
					if fill.rect == (drawlist.Rect{X: int32(r.Min.X), Y: int32(r.Min.Y), W: int32(r.Dx()), H: int32(r.Dy())}) {
						found = true
						if fill.inWorld {
							t.Fatal("replay backdrop recorded inside an affine region")
						}
					}
				}
				if !found {
					t.Fatal("production HUD did not record replay backdrop")
				}
				button := b.playback.overlay.buttons[replayControlPause]
				x, y := int32(button.Min.X+1), int32(button.Min.Y+1)
				mouse := &input.MouseState{}
				mouse.SetButton(input.MouseButtonLeft, true)
				if !b.serviceReplayOverlayPointer(mouse, x, y) {
					t.Fatal("physical Pause press was not consumed")
				}
				mouse.SetButton(input.MouseButtonLeft, false)
				if !b.serviceReplayOverlayPointer(mouse, x, y) || !b.playback.Paused() {
					t.Fatal("physical Pause release did not pause playback")
				}
			})
		}
	}
}
