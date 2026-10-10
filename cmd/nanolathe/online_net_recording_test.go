package main

import (
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

type networkRecordingTrace struct {
	overlayTrace
	glyphs       []drawlist.Glyphs
	textInRegion bool
}

func (s *networkRecordingTrace) Glyphs(g drawlist.Glyphs) {
	s.glyphs = append(s.glyphs, g)
	s.textInRegion = s.textInRegion || s.open
}

// The full production HUD must record the network meter in physical pixels:
// its layout already accounts for chrome insets, FPS and the message column.
// Applying the rail/strip transform again moves it into those reserved areas
// (DESIGN_INTERFACE_HUD_INPUT §3.3). A placement-helper test cannot catch that.
func TestOnlineNetworkProductionRecordingClearsScaledChrome(t *testing.T) {
	for _, size := range [][2]int{{1280, 720}, {1281, 1440}, {2560, 1440}} {
		for _, messages := range []int{0, 3} {
			t.Run(fmt.Sprintf("%dx%d/messages%d", size[0], size[1], messages), func(t *testing.T) {
				b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
				t.Cleanup(b.cl.Close)
				b.cl.Resize(size[0], size[1])
				b.cam.ViewW, b.cam.ViewH = int32(size[0]), int32(size[1])
				b.cl.SetCamera(b.cam)
				b.cl.SetEnhanced(true)
				b.cl.SetTerrain(b.sess.World)
				prefs := settings.DefaultPresentation()
				prefs.UIScale = 0
				b.hostPresentation = &prefs
				b.fpsVisible = true
				const tick = 5400
				b.sess.Clock.GlobalTick = tick
				b.sess.Snapshot.BeginWrite()
				if err := b.sess.Snapshot.Publish(tick); err != nil {
					t.Fatal(err)
				}
				b.cl.Step(1.0 / 30)
				clock := &netClock{at: time.Unix(1000, 0)}
				b.multiplayer = &battleMultiplayer{net: netCaptureStats(netCaptureCase{humans: 10, reported: true}, tick, clock)}
				h := &retailBattleHUD{console: netTestFont()}
				b.hud = h
				b.cl.SetFNT(h.console)
				for range messages {
					b.cl.MessageRing().Append("message", 1, 0, 10, tick)
				}
				b.cl.SetUIStage(battleHUDUIStage{hud: h, battle: b})
				b.cl.BeginPresentationFrame()
				left, top, bottom := b.cam.ChromeInset()
				layout := onlineNetMeasure(h.console, b.multiplayer.net.overlay(b.sess)).place(size[0], size[1], true, b.cl.MessageColumnBottom(), camera.ChromeInsets{Left: left, Top: top, Bottom: bottom})
				world := image.Rect(int(left), int(top), size[0], size[1]-int(bottom))
				fps := image.Rect(size[0]-onlineFPSPanelW-onlineFPSPanelMargin, onlineFPSPanelMargin, size[0]-onlineFPSPanelMargin, onlineFPSPanelMargin+onlineFPSPanelH)
				if !layout.fits || !layout.backdrop.In(world) || layout.backdrop.Overlaps(fps) || layout.backdrop.Min.Y < b.cl.MessageColumnBottom() {
					t.Fatalf("layout %v overlaps reserved physical areas: world %v, FPS %v, messages end %d", layout.backdrop, world, fps, b.cl.MessageColumnBottom())
				}
				trace := &networkRecordingTrace{}
				b.cl.RecordModernFrame().Replay(trace)
				found := false
				for _, fill := range trace.fills {
					if fill.rect == (drawlist.Rect{X: int32(layout.backdrop.Min.X), Y: int32(layout.backdrop.Min.Y), W: int32(layout.backdrop.Dx()), H: int32(layout.backdrop.Dy())}) {
						found = true
						if fill.inWorld {
							t.Fatalf("physical network backdrop %v recorded inside an affine region", fill.rect)
						}
					}
				}
				if !found || len(trace.glyphs) == 0 || trace.textInRegion {
					t.Fatalf("production network recording: backdrop found %v, %d glyph runs, text inside region %v", found, len(trace.glyphs), trace.textInRegion)
				}
				if first := trace.glyphs[0]; first.X != int32(layout.x) || first.Y != int32(layout.y) {
					t.Fatalf("network text origin (%d,%d), want physical (%d,%d)", first.X, first.Y, layout.x, layout.y)
				}
			})
		}
	}
}
