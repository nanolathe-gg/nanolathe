package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The build ghost is positioned from WORLD coordinates and composed in the UI
// stage, after the frame's own world region has closed. It therefore has to open
// a region of its own, or the modern executor leaves it at record coordinates
// while it shrinks the world under it: at half scale the ghost lands twice as
// far from the framebuffer origin as the site it marks, which is the play-test
// report of build placement being "way off" when zoomed out
// (DESIGN_GPU_RENDERER §16.3).
//
// The test asserts both halves: the ghost's fills are recorded inside a world
// region, and the rectangle they carry, put through the executor's own
// factor/step transform, is the presented rectangle f·(site − camera).
func TestBuildGhostRecordsInsideAWorldRegion(t *testing.T) {
	for _, tc := range []struct {
		name string
		zoom camera.Zoom
		step camera.ViewScale
	}{
		{"below the step", camera.ZoomUnit / 2, camera.ViewScaleNative},
		{"between the steps", camera.ZoomUnit * 13 / 10, camera.ViewScaleDetail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl, err := client.New(client.Options{Width: 640, Height: 480})
			if err != nil {
				t.Fatalf("client.New: %v", err)
			}
			cam := &camera.Camera{X: 96, Z: 64, ViewW: 640, ViewH: 480, MapW: 16384, MapH: 16384}
			cam.Zoom, cam.Scale = tc.zoom, tc.step
			cl.SetCamera(cam)

			b := &battleSession{battleUI: ui.NewProductionBattleState(), cam: cam}
			// An armed placement whose pointer is over the world, on a site the
			// camera can see. The ghost draws from the cell-aligned footprint, not
			// from the pointer.
			const cellX, cellZ, foot int32 = 20, 14, 3
			state := b.battleState()
			state.ArmPlacement("armed_fixture", foot, foot)
			state.Input.BuildCellX, state.Input.BuildCellZ = cellX, cellZ
			state.Input.BuildOK = true
			state.Input.PointerX, state.Input.PointerY = 300, 200
			if !b.overWorld(state.Input.PointerX, state.Input.PointerY) {
				t.Fatal("the fixture pointer is not over the world viewport")
			}
			if !b.worldOverlayArmed(nil) {
				t.Fatal("an armed placement did not ask for a world overlay region")
			}

			cl.SetUIStage(battleHUDUIStage{hud: &retailBattleHUD{}, battle: b})
			trace := &overlayTrace{}
			cl.RecordFrame().Replay(trace)

			// One region for the world, one for the UI stage's overlay.
			if trace.regions != 4 {
				t.Fatalf("recorded %d world markers, want two open/close pairs", trace.regions)
			}
			// The ghost is two nested outlines on the projected footprint.
			left, top, right, bottom := b.placementRect()
			ghost := 0
			for _, f := range trace.fills {
				if f.rect.X == left && f.rect.Y == top {
					if !f.inWorld {
						t.Fatalf("the ghost outline %+v was recorded outside every world region", f.rect)
					}
					ghost++
				}
			}
			if ghost == 0 {
				t.Fatalf("no ghost outline at the projected site (%d,%d) among %d fills", left, top, len(trace.fills))
			}

			// The executor scales a world region by factor over the step's own
			// factor, about the surface origin. Applying that to the recorded
			// rectangle must land on the presented rectangle f·(site − camera),
			// which is what "the ghost sits on its site" means at any factor.
			k := float64(tc.zoom) / float64(camera.ZoomOf(tc.step))
			for _, axis := range []struct {
				name           string
				recorded, want float64
			}{
				{"left", float64(left) * k, float64(tc.zoom.Project(cellX*16 - cam.X))},
				{"right", float64(right) * k, float64(tc.zoom.Project((cellX+foot)*16 - cam.X))},
				{"top", float64(top) * k, float64(tc.zoom.Project(cellZ*16 - cam.Z))},
				{"bottom", float64(bottom) * k, float64(tc.zoom.Project((cellZ+foot)*16 - cam.Z))},
			} {
				if d := axis.recorded - axis.want; d > 1 || d < -1 {
					t.Fatalf("%s: the transformed ghost edge is %.2f, want the presented %.2f", axis.name, axis.recorded, axis.want)
				}
			}
		})
	}
}

// overlayTrace records each Fill with whether a world region was open when it
// replayed, and counts the boundary markers.
type overlayTrace struct {
	open    bool
	regions int
	fills   []struct {
		rect    drawlist.Rect
		inWorld bool
	}
}

func (s *overlayTrace) World(w drawlist.WorldSpace) {
	s.regions++
	s.open = w.Begin
}

func (s *overlayTrace) Fill(v drawlist.Fill) {
	s.fills = append(s.fills, struct {
		rect    drawlist.Rect
		inWorld bool
	}{v.Rect, s.open})
}

func (s *overlayTrace) Clear()                   {}
func (s *overlayTrace) Terrain(drawlist.Terrain) {}
func (s *overlayTrace) Sprite(drawlist.Sprite)   {}
func (s *overlayTrace) Glyphs(drawlist.Glyphs)   {}
func (s *overlayTrace) Line(drawlist.Line)       {}
func (s *overlayTrace) Points(drawlist.Points)   {}
func (s *overlayTrace) Model(drawlist.Model)     {}
func (s *overlayTrace) Fog(drawlist.Fog)         {}
func (s *overlayTrace) Surface(drawlist.Surface) {}
func (s *overlayTrace) Cursor(drawlist.Cursor)   {}
func (s *overlayTrace) Expand()                  {}
