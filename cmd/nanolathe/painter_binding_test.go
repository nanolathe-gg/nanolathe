package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type painterBindingStage func(*client.Client)

func (s painterBindingStage) DrawUI(c *client.Client, _ client.UIFrame) { s(c) }
func bindingEntry(name string, pixel byte) formats.GAFEntry {
	return formats.GAFEntry{Name: name, Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{pixel}, Transparent: []bool{false}}}}}
}
func bindingWindow(name string) *gui.Window {
	return &gui.Window{Rect: gui.Rect{W: 32, H: 24}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, {Kind: gui.KindSurface, Name: name, Art: "generic", Active: 1, Rect: gui.Rect{X: 2, Y: 2, W: 8, H: 8}}, {Kind: gui.KindSurface, Name: name, Art: "generic", Active: 1, Rect: gui.Rect{X: 18, Y: 2, W: 8, H: 8}}}}
}
func bindingClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 32, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMapPreviewBindsOnlyFirstNamedSurface(t *testing.T) {
	w := bindingWindow("MAPPIC")
	w.Gadgets[1].Name = "MAPPIC\x00tail"
	panel := ui.NewPanel(w)
	art := &formats.GAF{Entries: []formats.GAFEntry{bindingEntry("generic", 83)}}
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMap), cs: testContentSet(vfs.New()), assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuMap: {window: w, art: art}}}, maps: []string{"fixture"}, mapData: map[string]*retailMapData{"fixture": {tnt: &formats.TNT{Width: 4, Height: 10, MinimapWidth: 1, MinimapHeight: 1, Minimap: []byte{37}}}}}
	shell.frontend.Panels.Replace(panel)
	backdrop := make([]byte, 32*24)
	for i := range backdrop {
		backdrop[i] = 31
	}
	shell.assets.panel[modeMenuMap].background = &formats.PCX{Width: 32, Height: 24, Pixels: backdrop}
	c := bindingClient(t)
	c.SetUIStage(gameShellUIStage{shell: shell})
	snap := c.ComposeFrameSnapshot()
	if snap.Indexed[4*snap.Width+4] != 37 || snap.Indexed[4*snap.Width+20] != 83 {
		t.Fatal("map preview replaced later duplicate's generic surface")
	}
	if snap.Indexed[4*snap.Width+9] != 31 || snap.Indexed[9*snap.Width+4] != 31 {
		t.Fatal("map surface overwrote the backdrop at its exclusive trailing edges")
	}
	writeShellShot(t, c, os.Getenv("NANOLATHE_BINDING_SHOT"))
}

func TestBriefingPanoramaBindsOnlyFirstNamedSurface(t *testing.T) {
	w := bindingWindow("PANORAMA")
	panel := ui.NewPanel(w)
	art := &formats.GAF{Entries: []formats.GAFEntry{bindingEntry("strip", 37), bindingEntry("generic", 83)}}
	shell := &gameShell{briefingPanel: panel, briefing: &campaignBriefingController{planet: BriefingPlanet{Panorama: "strip"}}, assets: &menuAssets{briefing: &retailPanelAssets{window: w, art: art}}}
	c := bindingClient(t)
	c.SetUIStage(painterBindingStage(shell.drawBriefing))
	snap := c.ComposeFrameSnapshot()
	if snap.Indexed[2*snap.Width+2] != 37 || snap.Indexed[2*snap.Width+18] != 83 {
		t.Fatal("panorama callback replaced later duplicate's generic art")
	}
}

func TestModalTitleBindsOnlyFirstNamedLabel(t *testing.T) {
	w := bindingWindow("TITLE")
	for i := 1; i < len(w.Gadgets); i++ {
		w.Gadgets[i].Kind = gui.KindLabel
		w.Gadgets[i].Text = "B"
		w.Gadgets[i].ColorF = 100
	}
	font := &formats.GAFEntry{Frames: make([]formats.GAFFrameRef, 256)}
	for code, pixel := range map[byte]byte{'I': 37, 'A': 37, 'B': 83} {
		font.Frames[code] = bindingEntry("glyph", pixel).Frames[0]
	}
	hud := &retailBattleHUD{modalFont: font}
	c := bindingClient(t)
	c.SetUIStage(painterBindingStage(func(c *client.Client) { hud.drawGUIWindow(c, w, nil, "A") }))
	actual := c.ComposeFrameSnapshot()
	w.Gadgets[2].Name = "ordinary"
	expected := c.ComposeFrameSnapshot()
	if !bytes.Equal(actual.Indexed, expected.Indexed) {
		t.Fatal("later duplicate TITLE received named caption binding")
	}
	w.Gadgets[1].Name = "ordinary"
	unbound := c.ComposeFrameSnapshot()
	if bytes.Equal(actual.Indexed, unbound.Indexed) {
		t.Fatal("fixture did not exercise the named title caption")
	}
}
