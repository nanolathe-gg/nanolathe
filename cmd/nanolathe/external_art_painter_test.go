package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestFrontendNonbuttonPainterKeepsItsKindArtAfterExternalPrepass(t *testing.T) {
	for _, kind := range []gui.Kind{gui.KindPicture, gui.KindSurface} {
		for _, exists := range []bool{false, true} {
			root := t.TempDir()
			if exists {
				data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "TOY", Frames: []formats.GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{83}}}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "anims"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "anims", "TOY_gadget.GAF"), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			fs := vfs.New()
			if err := fs.MountDirectory(root, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = fs.Close() })
			gad := gui.Gadget{Kind: kind, Name: "TOY", Active: 1, GAFFile: 1, Rect: gui.Rect{X: 2, Y: 2, W: 8, H: 8}}
			w := &gui.Window{Rect: gui.Rect{W: 32, H: 24}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, gad}}
			ordinary := &formats.GAF{Entries: []formats.GAFEntry{bindingEntry("TOY", 37)}}
			g := &gameShell{cs: &contentSet{fs: fs}, frontend: ui.NewFrontend(modeMenuMain), assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuMain: {window: w, art: ordinary}}}}
			g.installRetailWindowButtonArt(w, ordinary)
			p := ui.NewPanel(w)
			g.frontend.Panels.Replace(p)
			c := bindingClient(t)
			c.SetUIStage(painterBindingStage(func(c *client.Client) {
				c.UIFillRect(0, 0, 32, 24, 11)
				if kind == gui.KindSurface {
					g.drawRetailSurface(c, p, 1, w.Gadgets[1], gad.Rect)
				} else {
					g.drawRetailArt(c, p, 1, w.Gadgets[1], gad.Rect)
				}
			}))
			snap := c.ComposeFrameSnapshot()
			want := byte(37) // The generic external prepass is not this kind's image slot.
			if got := snap.Indexed[2*snap.Width+2]; got != want {
				t.Fatalf("kind=%d external exists=%t: pixel=%d, want %d (external art is 83)", kind, exists, got, want)
			}
		}
	}
}
