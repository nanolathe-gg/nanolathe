package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A sharp radar picture may project a contact between doubled canonical
// pixels. Every pixel of its magnified blip must remain pickable, including
// on letterboxed maps (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
// The canonical squared-distance boundary remains strict [07 R-SEL-02B2].
func TestScaledMinimapBlipHover(t *testing.T) {
	for _, dimensions := range [][2]int32{{512, 512}, {512, 256}, {256, 512}} {
		for _, scale := range []int32{1, 2} {
			t.Run(fmt.Sprintf("%dx%d/%dx", dimensions[0], dimensions[1], scale), func(t *testing.T) {
				const ink = 23
				h := &retailBattleHUD{
					chromeScale:   scale,
					minimapAnchor: hud.Rect{X1: 4, Y1: 3, X2: 129, Y2: 128}, minimapAnchorOK: true,
					radarBlipGAF: &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{
						Width: 1, Height: 1, Pixels: []byte{ink}, Transparent: []bool{false},
					}}}},
				}
				b := &battleSession{hud: h, sess: &session.Session{World: &world.Terrain{
					PlayRight: dimensions[0], PlayBottom: dimensions[1],
				}}}
				contact := frame.RadarContactView{Kind: frame.RadarContactUnit, Handle: 5,
					Owner: 0, Visible: true, PaletteKnown: true,
					X: numeric.Fixed(31) << 16, Z: numeric.Fixed(31) << 16}
				cur := &frame.Frame{Tick: 1, ViewingPlayer: 0,
					Visibility: frame.VisibilityView{W: 1, H: 1, Valid: true, WordVisible: []uint16{1}, Visible: []uint8{1}},
					Radar:      frame.RadarView{MappingLOS: 1, Contacts: []frame.RadarContactView{contact}},
				}
				detail := camera.LayoutMinimapCanvas(dimensions[0], dimensions[1], camera.MinimapLongSide*scale)
				picture := &render.RadarSurface{W: int(detail.W), H: int(detail.H), Pitch: int(detail.W), Bits: make([]byte, detail.W*detail.H)}
				radar := render.NewMinimapService(render.MinimapServiceConfig{Picture: picture, MapW: 1, MapH: 1})
				surface := h.rebuildRadarOn(b, cur, detail, radar, &h.radarDetailFinal, scale)
				if surface == nil {
					t.Fatal("radar picture was not rebuilt")
				}
				rx, ry := render.RadarProjection(31, 31, 0, dimensions[0], dimensions[1], detail)
				dst, _ := h.minimapRect()
				for dy := range scale {
					for dx := range scale {
						if value, _ := surface.At(int(rx+dx), int(ry+dy)); value != ink {
							t.Fatalf("blip pixel %d,%d = %d, want %d", dx, dy, value, ink)
						}
						mx, my := dst.X1+detail.PadX+rx+dx, dst.Y1+detail.PadY+ry+dy
						if got := b.minimapHoverUnit(cur, mx, my); got != contact.Handle {
							t.Fatalf("drawn blip at %d,%d picks %d, want %d", mx, my, got, contact.Handle)
						}
					}
				}
				canonical := camera.LayoutMinimap(dimensions[0], dimensions[1])
				cx, cy := canonical.WorldToRadar(31, 31, dimensions[0], dimensions[1])
				left, top, right, bottom := dst.Ordered()
				mx, my, _ := canonical.CanvasToDisplay(cx+2, cy, left, top, right-left+1, bottom-top+1)
				if got := b.minimapHoverUnit(cur, mx, my); got != 0 {
					t.Fatalf("canonical distance squared 4 picks %d, want no contact", got)
				}
				if got := b.minimapHoverUnit(cur, left-1, top); got != 0 {
					t.Fatalf("outside radar picks %d, want no contact", got)
				}
			})
		}
	}
}
