package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestBattleButtonVerdictUsesInstalledArtAndLowGreyBit(t *testing.T) {
	entry := &formats.GAFEntry{}
	for i := 0; i < 5; i++ {
		entry.Frames = append(entry.Frames, formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: uint16(i + 1)}})
	}
	index := func(gad gui.Gadget, down, stage int, grey bool) int {
		frame, _ := retailButtonFrameFromEntry(entry, gad, 0, down, stage, grey)
		if frame == nil {
			t.Fatal("button frame is nil")
		}
		return int(frame.Width) - 1
	}

	plain := gui.Gadget{Kind: gui.KindButton}
	if got := index(plain, 0, 0, false); got != 0 {
		t.Fatalf("plain rest frame=%d, want 0", got)
	}
	if got := index(plain, 1, 0, false); got != 1 {
		t.Fatalf("plain down frame=%d, want 1", got)
	}
	if got := index(plain, 1, 0, true); got != 3 {
		t.Fatalf("plain grey frame=%d, want 3", got)
	}

	staged := gui.Gadget{Kind: gui.KindButton, Stages: 2}
	if got := index(staged, 0, 1, false); got != 1 {
		t.Fatalf("staged frame=%d, want stage 1", got)
	}
	if got := index(staged, 1, 1, false); got != 3 {
		t.Fatalf("staged down frame=%d, want penultimate 3", got)
	}
	if got := index(staged, 0, 1, true); got != 1 {
		t.Fatalf("grey staged frame=%d, want stage 1", got)
	}

	cycle := gui.Gadget{Kind: gui.KindButton, Attribs: guiAttribCycle}
	if got := index(cycle, 1, 2, false); got != 1 {
		t.Fatalf("ordinary cycle held frame=%d, want down frame 1", got)
	}
	if got := index(cycle, 0, 1, true); got != 4 {
		t.Fatalf("grey cycle frame=%d, want final frame 4", got)
	}

	checkbox := gui.Gadget{Kind: gui.KindButton, Attribs: guiAttribCheckbox}
	if _, verdict := retailButtonFrameFromEntry(entry, checkbox, 0, 0, 0, true); verdict.shade {
		t.Fatal("grey checkbox requested rectangle darkening")
	}
	upperOnly := gui.Gadget{Kind: gui.KindButton, GrayedOut: 2}
	if grey := upperOnly.GrayedOut&1 != 0; grey {
		t.Fatal("upper grey bits selected the disabled art branch")
	}
	noArt := retailButtonVerdict(plain, 0, 0, 1, 0, false)
	if noArt.frame != -1 || noArt.top != 0 || noArt.bot != 17 || noArt.fill != 20 {
		t.Fatalf("art-less down verdict=%+v, want raised bevel 0/17/20", noArt)
	}
}

func TestBattleButtonCaptionPenMatchesQueueCaption(t *testing.T) {
	gad := gui.Gadget{Kind: gui.KindButton, Attribs: 0x20, Stages: 1}
	r := gui.Rect{X: 11, Y: 17, W: 64, H: 64}
	x, y := queueCountLabelPen(gad, r, 13, 14)
	wantX, wantY, build, centred := retailButtonCaptionPen(gad, r, 13, 14)
	if x != wantX || y != wantY || !build || centred {
		t.Fatalf("queue pen=(%d,%d), common=(%d,%d), build=%v centred=%v", x, y, wantX, wantY, build, centred)
	}
}

// TestBattleModalButtonPaintsRuntimeFrames exercises the actual retained modal
// panel path with deliberately distinct authored frame pixels. The common
// verdict is shared with the frontend, but the modal must supply DownAt and
// StageAt itself [07 R-WGT-01 §3].
func TestBattleModalButtonPaintsRuntimeFrames(t *testing.T) {
	entry := widgetArtEntry("modal", 31, 32, 33, 34, 35)
	gad := gui.Gadget{Kind: gui.KindButton, Active: 1, ButtonArtResolved: true, ButtonArt: &entry, Rect: gui.Rect{X: 5, Y: 5, W: 10, H: 10}}
	window := &gui.Window{Rect: gui.Rect{W: 32, H: 24}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, gad}}
	panel := ui.NewPanel(window)
	h := &retailBattleHUD{}
	c := bindingClient(t)
	c.SetUIStage(painterBindingStage(func(c *client.Client) { h.drawGUIWindowState(c, window, nil, "", panel, nil) }))

	paint := func(down, stage int, attribs uint32, grey int16) byte {
		window.Gadgets[1].Attribs = attribs
		window.Gadgets[1].GrayedOut = grey
		panel.SetStatusAt(1, down)
		panel.SetStageAt(1, stage)
		return c.ComposeFrameSnapshot().Indexed[6*32+6]
	}
	if got := paint(0, 0, 0, 0); got != 31 {
		t.Fatalf("modal rest pixel=%d, want frame 0", got)
	}
	if got := paint(1, 0, 0, 0); got != 32 {
		t.Fatalf("modal down pixel=%d, want frame 1", got)
	}
	window.Gadgets[1].Stages = 2
	if got := paint(0, 1, 0, 0); got != 32 {
		t.Fatalf("modal stage pixel=%d, want frame 1", got)
	}
	if got := paint(1, 1, 0, 0); got != 34 {
		t.Fatalf("modal staged-down pixel=%d, want penultimate frame", got)
	}
	window.Gadgets[1].Stages = 0
	if got := paint(1, 0, guiAttribCycle, 0); got != 32 {
		t.Fatalf("modal cycle pixel=%d, want runtime down frame", got)
	}
	if got := paint(1, 0, 0, 2); got != 32 {
		t.Fatalf("modal upper-grey pixel=%d, want ordinary down frame", got)
	}
	if got := paint(1, 0, 0, 1); got != 34 {
		t.Fatalf("modal low-grey pixel=%d, want grey frame", got)
	}
	if got := paint(0, 0, guiAttribCycle, 1); got != 35 {
		t.Fatalf("modal grey-cycle pixel=%d, want final frame", got)
	}
}

func TestBattleModalButtonFNTCaptionClipsToWindow(t *testing.T) {
	font := &formats.FNT{Height: 1}
	font.Glyphs['A'] = &formats.FNTGlyph{Width: 4, Height: 1, Bits: []byte{0xf0}}
	window := &gui.Window{
		Rect:    gui.Rect{X: 8, Y: 6, W: 8, H: 8},
		OriginX: 8, OriginY: 6,
		Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, {Kind: gui.KindButton, Active: 1, ButtonArtResolved: true, Text: "A", Rect: gui.Rect{X: -4, Y: 1, W: 8, H: 3}}},
	}
	panel := ui.NewPanel(window)
	panel.SetFlashRow(1, 9)
	h := &retailBattleHUD{guiFont: font}
	c := bindingClient(t)
	c.SetFNT(font)
	c.SetUIStage(painterBindingStage(func(c *client.Client) { h.drawGUIWindowState(c, window, nil, "", panel, nil) }))
	snap := c.ComposeFrameSnapshot()
	// The centred pen starts at x=6, but the modal surface begins at x=8.
	if snap.Indexed[7*32+6] != 0 || snap.Indexed[7*32+7] != 0 {
		t.Fatal("modal FNT caption escaped its private surface")
	}
	if snap.Indexed[7*32+8] != 9 || snap.Indexed[7*32+9] != 9 {
		t.Fatalf("modal FNT caption did not retain its normal layout inside the clip: %v", snap.Indexed[7*32+4:7*32+12])
	}
}

func TestBattleSideButtonPaintsCaptureAndArtlessBevel(t *testing.T) {
	entry := widgetArtEntry("side", 41, 42, 43, 44, 45)
	gad := gui.Gadget{Kind: gui.KindButton, Active: 1, ButtonArtResolved: true, ButtonArt: &entry, Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10}}
	window := &gui.Window{Rect: gui.Rect{W: 32, H: 24}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, gad}}
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Selection = frame.SelectionView{Handles: []pool.Handle{1}, Primary: 1, Count: 1}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	h := &retailBattleHUD{fs: vfs.New(), windows: map[string]*gui.Window{"gen": window}}
	b := &battleSession{sess: &session.Session{Snapshot: buf, LocalOwner: 0}, cat: &content.Catalog{}, hud: h}
	c := bindingClient(t)
	c.SetUIStage(painterBindingStage(func(c *client.Client) { h.drawSidePage(c, b, buf.Current()) }))
	if got := c.ComposeFrameSnapshot().Indexed[3*32+3]; got != 41 {
		t.Fatalf("side rest pixel=%d, want frame 0", got)
	}
	panel := h.palettePanel(window)
	panel.ServiceFrame(ui.WidgetFrame{PointerX: 3, PointerY: 3, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 3, Y: 3}}}, ui.WidgetHooks{})
	panel.ServiceFrame(ui.WidgetFrame{PointerX: 30, PointerY: 20, HeldButtons: 1}, ui.WidgetHooks{})
	if got := c.ComposeFrameSnapshot().Indexed[3*32+3]; got != 41 {
		t.Fatalf("outside capture pixel=%d want rest frame", got)
	}
	panel.ServiceFrame(ui.WidgetFrame{PointerX: 3, PointerY: 3, HeldButtons: 1}, ui.WidgetHooks{})
	if got := c.ComposeFrameSnapshot().Indexed[3*32+3]; got != 42 {
		t.Fatalf("side captured pixel=%d, want down frame", got)
	}
	window.Gadgets[1].ButtonArt = nil
	panel.ResetPress()
	window.Gadgets[1].GrayedOut = 0
	snap := c.ComposeFrameSnapshot()
	if snap.Indexed[2*32+5] != 17 || snap.Indexed[11*32+5] != 0 || snap.Indexed[6*32+6] != 20 {
		t.Fatal("side artless button did not paint the 17/0/20 bevel")
	}
	panel.ServiceFrame(ui.WidgetFrame{PointerX: 3, PointerY: 3, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 3, Y: 3}}}, ui.WidgetHooks{})
	snap = c.ComposeFrameSnapshot()
	if snap.Indexed[2*32+5] != 0 || snap.Indexed[11*32+5] != 17 || snap.Indexed[6*32+6] != 20 {
		t.Fatal("side artless captured button did not paint the 0/17/20 bevel")
	}
	panel.ResetPress()
	window.Gadgets[1].GrayedOut = 1
	snap = c.ComposeFrameSnapshot()
	if snap.Indexed[2*32+5] != 0 || snap.Indexed[11*32+5] != 19 || snap.Indexed[6*32+6] != 19 {
		t.Fatal("side artless grey button did not paint the 0/19/19 bevel")
	}
}
