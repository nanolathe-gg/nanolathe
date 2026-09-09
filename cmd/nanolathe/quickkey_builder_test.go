package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestEnglishTranslationTableLocalizesBuilderCaption(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gamedata"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gamedata", "translate.tdf"), []byte("[Alpha]\n{\n english=Zulu;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "caption.gui"), []byte("[GADGET0]\n{ [COMMON] { id=0; name=PANEL; width=640; height=480; } }\n[GADGET1]\n{ [COMMON] { id=1; name=BUTTON; } text=Alpha; quickkey=0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	table, err := content.LoadTranslationTable(fs, "english")
	if err != nil {
		t.Fatal(err)
	}
	w, err := gui.LoadWithTranslation(fs, "caption.gui", table)
	if err != nil {
		t.Fatal(err)
	}
	shell := &gameShell{quickKeyPreclearDisabled: true}
	shell.installRetailWindowButtonArt(w, nil)
	if got := w.Gadgets[1].QuickKey; got != 'Z' {
		t.Fatalf("localized caption accelerator = %q, want Z", got)
	}
}

func TestRetailWindowBuilderAssignsBeforeButtonArt(t *testing.T) {
	entry := widgetArtEntry("ORDINARY", 3)
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "ORDINARY", Text: "Alpha", QuickKey: 'Q', Active: 1},
		{Kind: gui.KindButton, Name: "LATER", QuickKey: 'A', Active: 1},
		{Kind: gui.KindButton, Name: "STAGED", Text: "Stage", QuickKey: 'S', Stages: 1},
		{Kind: gui.KindButton, Name: "KEEP", Text: "Keep", QuickKey: 'K', Attribs: 0x10000},
		{Kind: gui.KindLabel, Name: "LINK", Text: "Linked", Link: "ORDINARY", QuickKey: 'x'},
		{Kind: gui.KindLabel, Name: "PLAIN", Text: "Caption", QuickKey: 'C'},
	}}
	shell := &gameShell{quickKeyPreclearDisabled: true, assets: &menuAssets{common: &formats.GAF{Entries: []formats.GAFEntry{entry}}}}
	shell.installRetailWindowButtonArt(w, nil)
	if got := w.Gadgets[1].QuickKey; got != 'l' {
		t.Fatalf("ordinary key = %q, want l after later A excludes first candidate", got)
	}
	if got := w.Gadgets[3].QuickKey; got != 0 {
		t.Fatalf("staged key = %q, want clear", got)
	}
	if got := w.Gadgets[4].QuickKey; got != 'K' {
		t.Fatalf("preserve key = %q, want K", got)
	}
	if got := w.Gadgets[5].QuickKey; got != 'i' {
		t.Fatalf("linked label key = %q, want i after the prior button takes L", got)
	}
	if got := w.Gadgets[6].QuickKey; got != 'C' {
		t.Fatalf("unlinked label key = %q, want unchanged C", got)
	}
	if got := w.Gadgets[1].ButtonArt; got != &shell.assets.common.Entries[0] || !w.Gadgets[1].ButtonArtResolved {
		t.Fatalf("ordinary installed art = %p resolved=%t", got, w.Gadgets[1].ButtonArtResolved)
	}
}

func TestRetailWindowBuilderGAFAndArrowBypasses(t *testing.T) {
	root := t.TempDir()
	pixels := make([]byte, 7*9)
	for i := range pixels {
		pixels[i] = byte(i + 1)
	}
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "ODD", Frames: []formats.GAFWriteFrame{{Width: 7, Height: 9, Pixels: pixels}}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "anims", "ODD_gadget.GAF")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	shell := &gameShell{cs: &contentSet{fs: fs}, quickKeyPreclearDisabled: true, assets: &menuAssets{common: &formats.GAF{Entries: []formats.GAFEntry{widgetArtEntry("BUTTONS0", 1)}}}}
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "ODD", Text: "Odd", QuickKey: 'Q', GAFFile: -1, ColorF: 19, Rect: gui.Rect{W: 2, H: 3}},
		{Kind: gui.KindButton, Name: "EVEN", Text: "Even", QuickKey: 'Q', GAFFile: 2, Rect: gui.Rect{W: 2, H: 3}},
		{Kind: gui.KindButton, Name: "MISSING", Text: "Missing", QuickKey: 'Q', GAFFile: 1, Rect: gui.Rect{W: 2, H: 3}},
		{Kind: gui.KindButton, Name: "ARROW", Text: "Arrow", QuickKey: 'Q', Attribs: 0x1800, Rect: gui.Rect{W: 2, H: 3}},
		{Kind: gui.KindLabel, Name: "LABEL", Text: "Label", Link: "target", QuickKey: 'Q', GAFFile: 1, ColorF: 23},
	}}
	shell.installRetailWindowButtonArt(w, nil)
	if g := w.Gadgets[1]; g.ButtonArt == nil || g.ButtonArt.Name != "ODD" || g.Rect.W != 2 || g.Rect.H != 3 || g.QuickKey != 'Q' || g.ColorF != 0 {
		t.Fatalf("odd GAF result = %+v", g)
	}
	if g := w.Gadgets[2]; g.ButtonArt == nil || g.ButtonArt.Name != "BUTTONS0" || g.QuickKey == 0 {
		t.Fatalf("even nonzero GAF did not use ordinary builder: %+v", g)
	}
	if g := w.Gadgets[3]; !g.ButtonArtResolved || g.ButtonArt != nil || g.QuickKey != 'Q' || g.Rect.W != 2 || g.Rect.H != 3 {
		t.Fatalf("missing odd GAF fell through: %+v", g)
	}
	if g := w.Gadgets[4]; g.ButtonArtResolved || g.QuickKey != 'Q' || g.Rect.W != 2 || g.Rect.H != 3 {
		t.Fatalf("arrow entered ordinary builder: %+v", g)
	}
	if g := w.Gadgets[5]; g.QuickKey != 'L' || g.ColorF != 0 || !g.ExternalArtResolved || g.ExternalArt != nil {
		t.Fatalf("odd-GAF label skipped its kind arm: %+v", g)
	}
}

func TestQuickKeyPreclearLifetime(t *testing.T) {
	window := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Text: "Keep", QuickKey: 'K', Attribs: 0x10000}}}
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
	shell.installRetailWindowButtonArt(window, nil)
	if got := window.Gadgets[1].QuickKey; got != 0 {
		t.Fatalf("startup preserve key = %q, want precleared zero", got)
	}
	window.Gadgets[1].QuickKey = 'K'
	shell.commitBattleCandidate(&battleSession{})
	shell.frontend.SetMode(modeMenuMain)
	shell.installRetailWindowButtonArt(window, nil)
	if got := window.Gadgets[1].QuickKey; got != 'K' {
		t.Fatalf("post-battle preserve key = %q, want K", got)
	}

	failed := &gameShell{frontend: ui.NewFrontend(modeLoading), loadingReturn: modeMenuMain, loading: newLoadingState("")}
	failed.loading.done <- loadResult{err: os.ErrNotExist}
	failed.stepLoading(0)
	if failed.quickKeyPreclearDisabled {
		t.Fatal("failed load disabled process preclear")
	}
}

func TestMenuOpenBuildsFreshWindowFromCachedDefinition(t *testing.T) {
	authored := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Text: "Keep", QuickKey: 'K', Attribs: 0x10000}}}
	shell := &gameShell{
		frontend: ui.NewFrontend(modeMenuMain),
		assets:   &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuMain: {window: authored}}},
	}
	shell.openMenu(modeMenuMain)
	if authored.Gadgets[1].QuickKey != 'K' || shell.activePanel().Window.Gadgets[1].QuickKey != 0 {
		t.Fatalf("startup source/runtime keys = %q/%q, want K/0", authored.Gadgets[1].QuickKey, shell.activePanel().Window.Gadgets[1].QuickKey)
	}
	shell.commitBattleCandidate(&battleSession{})
	shell.openMenu(modeMenuMain)
	if authored.Gadgets[1].QuickKey != 'K' || shell.activePanel().Window.Gadgets[1].QuickKey != 'K' {
		t.Fatalf("post-battle source/runtime keys = %q/%q, want K/K", authored.Gadgets[1].QuickKey, shell.activePanel().Window.Gadgets[1].QuickKey)
	}
}
