//go:build retail

package main

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// The in-battle pause menu's plate is authored eight rows taller than the art
// behind it: `ARMOPT.GUI`'s `OPTBG` picture box is 128x362 while
// `anims/armopt.gaf`'s `OPTBG` frame is a 128x354 RLE frame. Retail stamps an
// RLE frame at the gadget origin and leaves the surplus rows unpainted; it
// resamples only a raw frame in a blank surface (kind 6)
// [07 "Retail frontend control activation and raster rules"][07 R-WGT-01 §8].
//
// Stretching the plate onto the authored rectangle instead walked every recess
// progressively down the rail, away from the button meant to sit in it — the
// pause menu's "buttons outside their containers". This locks the authored
// mismatch that made the defect visible together with the raster decision, so
// neither can drift back silently.
func TestRetailOptionsPanelPlateIsStampedNotStretched(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	defer fs.Close()

	window, err := gui.Load(fs, "guis/armopt.gui")
	if err != nil {
		t.Fatalf("load ARMOPT.GUI: %v", err)
	}
	data, err := fs.ReadFileLimit("anims/armopt.gaf", 32<<20)
	if err != nil {
		t.Fatalf("read armopt.gaf: %v", err)
	}
	page, err := formats.LoadGAF(data)
	if err != nil {
		t.Fatalf("parse armopt.gaf: %v", err)
	}
	entry, ok := page.Find("OPTBG")
	if !ok || len(entry.Frames) == 0 || entry.Frames[0].Frame == nil {
		t.Fatal("armopt.gaf has no OPTBG frame")
	}
	plate := entry.Frames[0].Frame

	index := -1
	for i, gad := range window.Gadgets {
		if i != 0 && strings.EqualFold(gad.Name, "OPTBG") {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("ARMOPT.GUI has no OPTBG gadget")
	}
	gad := window.Gadgets[index]
	if gad.Kind != gui.KindPicture {
		t.Fatalf("OPTBG kind = %d, want the picture box %d", gad.Kind, gui.KindPicture)
	}
	if plate.Compressed == 0 {
		t.Fatal("OPTBG frame is raw; the stamp/resample split under test assumes the retail RLE plate")
	}
	if int32(plate.Height) == gad.Rect.H {
		t.Skip("OPTBG art now matches its authored rectangle; the stretch this test guards is unreachable")
	}
	if modalArtResampled(gad.Kind, plate, window.PlacedRect(index)) {
		t.Fatalf("OPTBG (%dx%d art in a %dx%d record) is being resampled; retail stamps it at the gadget origin",
			plate.Width, plate.Height, gad.Rect.W, gad.Rect.H)
	}

	// The buttons the plate's recesses are drawn for sit at their authored
	// window-local offsets, so a stamped plate keeps every one of them in its
	// own recess whatever the display mode.
	for i, g := range window.Gadgets {
		if i == 0 || g.Kind != gui.KindButton {
			continue
		}
		placed := window.PlacedRect(i)
		if placed.X-window.Rect.X != g.Rect.X || placed.Y-window.Rect.Y != g.Rect.Y {
			t.Fatalf("%s offset = (%d,%d), want authored (%d,%d)",
				g.Name, placed.X-window.Rect.X, placed.Y-window.Rect.Y, g.Rect.X, g.Rect.Y)
		}
	}
}
