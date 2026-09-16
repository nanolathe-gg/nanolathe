package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func battleWindowEntry(name string) *formats.GAFEntry {
	return &formats.GAFEntry{Name: name, Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{Width: 11, Height: 7}}}}
}

func TestBattleWindowPainterUsesInstalledArtAndResolvedMiss(t *testing.T) {
	installed := battleWindowEntry("installed")
	installed.Frames = append(installed.Frames,
		formats.GAFFrameRef{Frame: &formats.GAFFrame{}},
		formats.GAFFrameRef{Frame: &formats.GAFFrame{}},
		formats.GAFFrameRef{Frame: &formats.GAFFrame{}},
		formats.GAFFrameRef{Frame: &formats.GAFFrame{}},
	)
	fallback := battleWindowEntry("fallback")
	h := &retailBattleHUD{common: &formats.GAF{Entries: []formats.GAFEntry{*fallback}}}
	page := &formats.GAF{Entries: []formats.GAFEntry{*fallback}}

	button := gui.Gadget{Kind: gui.KindButton, Name: "same", Art: "same", ArtFrame: 4, ButtonArt: installed, ButtonArtResolved: true}
	if got := h.gadgetArtEntry(button, page); got != installed {
		t.Fatalf("page entry = %p, want retained installed entry %p", got, installed)
	}
	if got := h.gadgetButtonFrame(button, page, 0, 0, false); got != installed.Frames[4].Frame {
		t.Fatalf("page frame = %p, want retained installed frame base %p", got, installed.Frames[4].Frame)
	}
	if got := h.modalGadgetFrameState(button, page, 0, 0, false); got != installed.Frames[4].Frame {
		t.Fatalf("modal frame = %p, want retained installed frame base %p", got, installed.Frames[4].Frame)
	}

	picture := gui.Gadget{Kind: gui.KindPicture, Name: "fallback", Art: "fallback", ExternalArtResolved: true, ExternalArt: installed}
	if got := h.gadgetArtEntry(picture, page); got != &page.Entries[0] {
		t.Fatal("nonbutton borrowed generic external slot")
	}
	if got := h.modalGadgetFrameState(picture, page, 0, 0, false); got != page.Entries[0].Frames[0].Frame {
		t.Fatal("modal nonbutton borrowed generic external slot")
	}
	miss := gui.Gadget{Kind: gui.KindButton, Name: "same", Art: "same", ExternalArtResolved: true}
	if got := h.gadgetArtEntry(miss, page); got != nil {
		t.Fatalf("resolved external miss fell through to %p", got)
	}
	if got := h.gadgetButtonFrame(miss, page, 0, 0, false); got != nil {
		t.Fatalf("resolved external miss frame fell through to %p", got)
	}
	if got := h.modalGadgetFrameState(miss, page, 0, 0, false); got != nil {
		t.Fatalf("modal resolved external miss fell through to %p", got)
	}
}

func TestGeneratedPageReparsesSourceThenBuildsOnce(t *testing.T) {
	root := t.TempDir()
	guiPath := filepath.Join(root, "guis", "armdl.gui")
	if err := os.MkdirAll(filepath.Dir(guiPath), 0o755); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	text.WriteString("[GADGET0]{[COMMON]{id=0;name=PANEL;width=640;height=480;}}\n")
	for i := 1; i < 10; i++ {
		caption, key := "Unused", "0"
		if i == 1 {
			caption = "Alpha"
		}
		if i == 2 {
			key = "65" // later authored A excludes Alpha's first candidate.
		}
		text.WriteString("[GADGET" + string(rune('0'+i)) + "]{[COMMON]{id=1;name=BUTTON;}text=" + caption + ";quickkey=" + key + ";}\n")
	}
	if err := os.WriteFile(guiPath, []byte(text.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	// This is deliberately an already-built cache record with a different
	// caption. Generated pages must parse ARMDL again before their one build.
	built := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Text: "Wrong", QuickKey: 'W'}}}
	disabled := true
	h := &retailBattleHUD{
		fs:            fs,
		side:          &content.SideDef{NamePrefix: "ARM"},
		windows:       map[string]*gui.Window{"armdl": built},
		windowContext: &battleWindowContext{content: &contentSet{fs: fs}, preclearDisabled: &disabled},
	}
	w, _, err := h.numberedPage("armlab2", []frame.GeneratedProductPlacement{{ProductKey: "product", Button: 4}})
	if err != nil {
		t.Fatal(err)
	}
	// One pass sees authored later A and chooses L. A second pass would see the
	// later button's assigned U instead and change Alpha back to A.
	if w == nil || w.Gadgets[1].Text != "Alpha" || w.Gadgets[1].QuickKey != 'l' {
		t.Fatalf("generated page did not parse/build authored source once: %#v", w)
	}
}

func TestBattleWindowBuildLifetimeAndOptionsRelabel(t *testing.T) {
	preclear := false
	context := &battleWindowContext{preclearDisabled: &preclear}
	startup := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Text: "Keep", QuickKey: 'K', Attribs: 0x10000}}}
	context.install(startup, nil, nil)
	if got := startup.Gadgets[1].QuickKey; got != 0 {
		t.Fatalf("startup preserve key = %q, want preclear zero", got)
	}
	// A failed candidate has no transition writer, so the next window still
	// sees startup preclear.
	failed := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Text: "Keep", QuickKey: 'K', Attribs: 0x10000}}}
	context.install(failed, nil, nil)
	if got := failed.Gadgets[1].QuickKey; got != 0 {
		t.Fatalf("failed candidate changed preclear lifetime: %q", got)
	}
	context.completeTransition()
	later := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Text: "Keep", QuickKey: 'K', Attribs: 0x10000}}}
	context.install(later, nil, nil)
	if got := later.Gadgets[1].QuickKey; got != 'K' {
		t.Fatalf("post-transition preserve key = %q, want K", got)
	}

	h := &retailBattleHUD{
		windowContext:  context,
		optionsRelabel: true,
		optionsWin: &gui.Window{Gadgets: []gui.Gadget{
			{Kind: gui.KindPanel},
			{Kind: gui.KindButton, Name: optionsMissionButton, Text: "Mission", QuickKey: 'Q'},
		}},
	}
	h.openOptionsWindow()
	mission := h.optionsWin.Gadgets[1]
	if mission.Text != optionsSettingsLabel || mission.QuickKey != 'M' {
		t.Fatalf("options relabel/key = %q/%q, want Settings/M", mission.Text, mission.QuickKey)
	}
}
