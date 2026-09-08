package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/gui"
)

func TestButtonMissingFallbackDoesNotChangeFamily(t *testing.T) {
	for _, tc := range []struct {
		name   string
		attr   uint32
		stages uint8
	}{
		{"checkbox", guiAttribCheckbox, 0},
		{"staged checkbox", guiAttribCheckbox, 1},
		{"staged", 0, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shell := &gameShell{assets: &menuAssets{common: &formats.GAF{Entries: []formats.GAFEntry{widgetArtEntry("BUTTONS0", 1, 2)}}}}
			window := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Name: "missing", Attribs: tc.attr, Stages: tc.stages, Rect: gui.Rect{W: 17, H: 19}}}}
			shell.installRetailWindowButtonArt(window, nil)
			gad := window.Gadgets[1]
			if !gad.ButtonArtResolved || gad.ButtonArt != nil || gad.Rect.W != 17 || gad.Rect.H != 19 {
				t.Fatalf("missing selected family was replaced: %+v", gad)
			}
			if tc.attr != 0 && gad.Stages != tc.stages {
				t.Fatal("missing checkbox fell through to stage forcing")
			}
		})
	}
}

func TestButtonZeroFrameEntryStopsProviderLookup(t *testing.T) {
	for _, ownEntry := range []bool{true, false} {
		common := &formats.GAF{Entries: []formats.GAFEntry{widgetArtEntry("named", 1, 2), widgetArtEntry("BUTTONS0", 3, 4)}}
		own := &formats.GAF{Entries: []formats.GAFEntry{{Name: "named"}}}
		want := &own.Entries[0]
		if !ownEntry {
			own = nil
			common.Entries[0].Frames = nil
			want = &common.Entries[0]
		}
		shell := &gameShell{assets: &menuAssets{common: common}}
		window := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Name: "named", Stages: 3, Rect: gui.Rect{W: 17, H: 19}}}}
		shell.installRetailWindowButtonArt(window, own)
		gad := window.Gadgets[1]
		if !gad.ButtonArtResolved || gad.ButtonArt != want || gad.Rect.W != 17 || gad.Rect.H != 19 || gad.Attribs != 1 {
			t.Fatalf("zero-frame entry lost identity or staged finalization: %+v", gad)
		}
		if got := shell.retailButtonArt(gad, 0, 0, false); got != nil {
			t.Fatal("zero-frame entry unexpectedly produced art")
		}
	}
}
