package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// slideRailArt is a PANELSIDE stand-in whose index varies with the row, so a
// vertical translation of one pixel changes the composed rail.
func slideRailArt() *formats.GAFFrame {
	f := &formats.GAFFrame{
		Width: 129, Height: 480,
		Pixels: make([]byte, 129*480), Transparent: make([]bool, 129*480),
	}
	for y := 0; y < 480; y++ {
		for x := 0; x < 129; x++ {
			// 1..250 by row: never 0, so the cleared surface cannot pass for art.
			f.Pixels[y*129+x] = byte(1 + y%250)
		}
	}
	return f
}

// TestPanelSlideNeverMovesTheSideRail locks WU-19-223's finding. The §6 slide
// offset drives the bottom slide strip that "slides up from the bottom edge of
// the view when Space is held" [07 R-HUD-03 §1 "the panel-slide gate"], not the
// side rail: PANELSIDE's only origin is (0,0) and it "is stamped here and
// nowhere else", while "every rail window and gadget rectangle" is fixed in
// authored coordinates [07 R-HUD-05][07 §6 "Panel asset binding and draw
// origins"]. The composer used to blit PANELSIDE at (0, offset), so holding
// Space lifted the whole left rail 31 pixels up the screen.
//
// Both display modes are exercised because the rail's rule is the same at each:
// the art is reused as-is at its authored origin [07 R-HUD-05].
func TestPanelSlideNeverMovesTheSideRail(t *testing.T) {
	for _, size := range []struct{ w, h int }{{640, 480}, {800, 600}} {
		buf := frame.NewBuffer()
		cur := buf.BeginWrite()
		cur.Selection.LocalPlayer = 0
		cur.Visibility = frame.VisibilityView{
			W: 1, H: 1, Valid: true, WordVisible: []uint16{1}, Visible: []uint8{1},
		}
		if err := buf.Publish(1); err != nil {
			t.Fatal(err)
		}
		terrain := &world.Terrain{PlayRight: 512, PlayBottom: 512}
		sess := &session.Session{Snapshot: buf, World: terrain, LocalOwner: 0}
		h := &retailBattleHUD{side: &content.SideDef{}, panelSide: slideRailArt()}
		b := &battleSession{sess: sess, hud: h}
		c, err := client.New(client.Options{Buffer: buf, Width: size.w, Height: size.h})
		if err != nil {
			t.Fatal(err)
		}
		c.SetUIStage(battleHUDUIStage{hud: h, battle: b})

		b.battleState().PanelOffset = ui.PanelVisible
		parked := c.ComposeFrame()
		var want []byte
		for y := 0; y < 480; y++ {
			for x := 0; x < 129; x++ {
				want = append(want, parked.RGBAAt(x, y).R)
			}
		}
		b.battleState().PanelOffset = ui.PanelParked
		slid := c.ComposeFrame()
		for y := 0; y < 480; y++ {
			for x := 0; x < 129; x++ {
				if got := slid.RGBAAt(x, y).R; got != want[y*129+x] {
					t.Fatalf("%dx%d: rail pixel (%d,%d) moved with the slide: got %d, want %d",
						size.w, size.h, x, y, got, want[y*129+x])
				}
			}
		}
	}
}

// TestPanelSlideNeverMovesRailHitTests is the input half of the same rule: a
// rail gadget rectangle is fixed in authored coordinates, so the pointer pass,
// the press/release capture and the button identity all answer the same at
// every slide position [07 R-HUD-05].
func TestPanelSlideNeverMovesRailHitTests(t *testing.T) {
	gadget := func(name string, y int32) gui.Gadget {
		return gui.Gadget{Kind: gui.KindButton, Active: 1, Name: name, Rect: gui.Rect{X: 0, Y: y, W: 20, H: 10}}
	}
	window := &gui.Window{Gadgets: []gui.Gadget{{}, gadget("PLAINBUTTON", 100), gadget("PLAINBUTTON2", 120)}}
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0})
	w.Selection = frame.SelectionView{Handles: append(w.Selection.Handles, 1), Primary: 1, Count: 1}
	w.CommandPage = frame.CommandPageView{MoveStance: 4, FireStance: 4, CloakState: 3, OnOffState: 3}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	h := &retailBattleHUD{fs: vfs.New(), windows: map[string]*gui.Window{"gen": window}}
	b := &battleSession{sess: &session.Session{Snapshot: buf, LocalOwner: 0}, cat: &content.Catalog{}, hud: h}
	f := buf.Current()

	// (5,105) is inside the authored MOVE rectangle; (5,74) is where the old
	// -31 translation would have put it.
	for _, offset := range []int8{ui.PanelVisible, -11, -21, ui.PanelParked} {
		b.battleState().PanelOffset = offset
		h.updateHoveredGadget(b, f, 5, 105)
		if _, name := h.hoveredGadgetSource(); name != "PLAINBUTTON" {
			t.Fatalf("offset %d: hovered gadget at the authored rectangle = %q, want PLAINBUTTON", offset, name)
		}
		h.updateHoveredGadget(b, f, 5, 74)
		if _, name := h.hoveredGadgetSource(); name != "" {
			t.Fatalf("offset %d: pointer 31 rows above the authored rectangle hovered %q, want none", offset, name)
		}
		if !h.hitTestFor(b, 5, 105) {
			t.Fatalf("offset %d: authored MOVE rectangle reported no hit", offset)
		}
		if h.hitTestFor(b, 5, 74) {
			t.Fatalf("offset %d: a point 31 rows above the authored rectangle reported a hit", offset)
		}
		if got := h.buttonAt(b, 5, 105); got != 1 {
			t.Fatalf("offset %d: buttonAt the authored rectangle = %d, want 1", offset, got)
		}
	}
}
