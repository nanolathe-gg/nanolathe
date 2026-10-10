package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
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

type scaledMinimapStage struct {
	b     *battleSession
	frame *frame.Frame
}

func (s scaledMinimapStage) DrawUI(c *client.Client, _ client.UIFrame) {
	s.b.hud.drawMinimap(c, s.b, s.frame)
}

type minimapSurfaceTrace struct {
	overlayTrace
	surfaces []drawlist.Surface
}

func (s *minimapSurfaceTrace) Surface(v drawlist.Surface) { s.surfaces = append(s.surfaces, v) }

// Fit truncation must precede magnification: refitting the short side at 252
// can add a drawn column/row that the canonical input lens treats as chrome.
// Exercise the actual draw, pointer classifier, and frame click dispatch at
// both sides of each letterboxed axis (Modern sidebar scale).
func TestScaledMinimapLetterboxEdgesDispatch(t *testing.T) {
	for _, point := range [][4]int32{
		{992, 3968, 8, 1984}, {992, 3968, 984, 1984},
		{3968, 992, 1984, 8}, {3968, 992, 1984, 984},
	} {
		t.Run(fmt.Sprint(point), func(t *testing.T) {
			playW, playH, wx, wz := point[0], point[1], point[2], point[3]
			b := newTestBattle(testCatalogON05(), testWorldON05(playW/16, playH/16))
			t.Cleanup(b.cl.Close)
			withMinimap(b)
			h := b.hud
			h.chromeScale = 2
			h.radarConfig = render.MinimapServiceConfig{MapW: 1, MapH: 1}
			const ink = 23
			h.radarBlipGAF = &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{
				Width: 1, Height: 1, Pixels: []byte{ink}, Transparent: []bool{false},
			}}}}
			h.radarDetailSource = func(side int32) *render.RadarSurface {
				m := camera.LayoutMinimapCanvas(playW, playH, side)
				return &render.RadarSurface{W: int(m.W), H: int(m.H), Pitch: int(m.W), Bits: make([]byte, m.W*m.H)}
			}
			target := placeUnit(b, "armcons", numeric.Fixed(wx)<<16, numeric.Fixed(wz)<<16)
			replaceSelectionForTest(t, b, target)
			cur, _ := b.currentSnapshot()
			written := b.sess.Snapshot.BeginWrite()
			*written = *cur
			written.Visibility = frame.VisibilityView{W: 1, H: 1, Valid: true, WordVisible: []uint16{1}, Visible: []uint8{1}}
			written.Radar.MappingLOS = 0
			written.Radar.Contacts = []frame.RadarContactView{{Kind: frame.RadarContactUnit,
				Handle: target.Handle, Owner: 0, Visible: true, PaletteKnown: true,
				X: numeric.Fixed(wx) << 16, Z: numeric.Fixed(wz) << 16}}
			if err := b.sess.Snapshot.Publish(cur.Tick + 1); err != nil {
				t.Fatal(err)
			}
			cur, _ = b.currentSnapshot()
			b.cl.SetUIStage(scaledMinimapStage{b, cur})
			b.cl.Step(1.0 / 30)
			trace := &minimapSurfaceTrace{}
			b.cl.RecordModernFrame().Replay(trace)
			if len(trace.surfaces) != 1 {
				t.Fatalf("minimap recorded %d pictures, want one", len(trace.surfaces))
			}
			detail := camera.LayoutMinimapCanvas(playW, playH, 252)
			rx, ry := detail.WorldToRadar(wx, wz, playW, playH)
			surf := trace.surfaces[0]
			// At the far edge only one of the two magnified art pixels fits.
			for dy := range int32(2) {
				for dx := range int32(2) {
					mx, my := rx+dx, ry+dy
					if mx < surf.Dst.X || mx >= surf.Dst.X+surf.Dst.W || my < surf.Dst.Y || my >= surf.Dst.Y+surf.Dst.H {
						continue
					}
					if got := surf.Pixels[(my-surf.Dst.Y)*surf.SrcW+mx-surf.Dst.X]; got != ink {
						t.Fatalf("drawn edge pixel %d,%d = %d, want blip", mx, my, got)
					}
					if got := b.classifyPointer(mx, my); got != battlePointerMinimap {
						t.Fatalf("drawn edge pixel %d,%d classified %v, want minimap", mx, my, got)
					}
					if got := b.minimapHoverUnit(cur, mx, my); got != target.Handle {
						t.Fatalf("drawn edge pixel %d,%d picks %d, want %d", mx, my, got, target.Handle)
					}
				}
			}
			// The adjacent letterbox stays inert, including camera capture.
			outsideX, outsideY := rx, ry
			if playW < playH {
				outsideX = surf.Dst.X - 1
				if wx > playW/2 {
					outsideX = surf.Dst.X + surf.Dst.W
				}
			} else {
				outsideY = surf.Dst.Y - 1
				if wz > playH/2 {
					outsideY = surf.Dst.Y + surf.Dst.H
				}
			}
			if b.classifyPointer(outsideX, outsideY) != battlePointerChrome {
				t.Fatal("letterbox classified as a usable region")
			}
			cameraInput := input.NewState()
			cameraInput.Mouse.SetButton(b.minimapCameraButton(), true)
			if b.beginMinimapCameraLatch(outsideX, outsideY, cameraInput.Mouse) {
				t.Fatal("letterbox acquired the camera capture")
			}
			in := input.NewState()
			b.battleState().Input.Latch = input.LatchMove
			in.Mouse.X, in.Mouse.Y = float32(rx), float32(ry)
			in.Mouse.SetButton(input.MouseButtonLeft, true)
			b.handleInput(in, nil)
			pending := b.sess.PendingHumanCommands()
			layout, dst, _ := b.minimapLayout()
			wantX, wantZ, ok := client.MinimapPointerWorld(layout, dst, playW, playH, rx, ry)
			if !ok || len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != hud.LatchToCode(input.LatchMove) || int32(pending[0].Order.Position.X>>16) != wantX || int32(pending[0].Order.Position.Z>>16) != wantZ {
				t.Fatalf("edge click queued %+v, want Move at lens point %d,%d", pending, wantX, wantZ)
			}
		})
	}
}
