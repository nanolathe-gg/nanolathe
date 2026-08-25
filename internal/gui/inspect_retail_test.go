//go:build retail

package gui

import (
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func TestRetailCommanderPagesUseAuthoredSixProductSlots(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		root = os.Getenv("HOME") + "/TotalAnnihilation"
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer fs.Close()
	for _, name := range []string{"guis/armcom1.gui", "guis/armcom2.gui", "guis/armcom3.gui"} {
		w, err := Load(fs, name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		if w.OriginX != 0 || w.OriginY != 128 || w.Rect.W != 128 || w.Rect.H != 352 {
			t.Fatalf("%s authored rail = origin (%d,%d), size %dx%d; want (0,128), 128x352", name, w.OriginX, w.OriginY, w.Rect.W, w.Rect.H)
		}
		products := 0
		for i, g := range w.Gadgets {
			if i == 0 || g.Kind != KindButton || g.Active == 0 {
				continue
			}
			r := w.PlacedRect(i)
			if r.W == 64 && r.H == 64 {
				products++
			}
		}
		if products != 6 {
			t.Fatalf("%s has %d authored 64x64 product gadgets; want 6", name, products)
		}
	}
}
