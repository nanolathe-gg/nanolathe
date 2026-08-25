//go:build retail

package formats

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func retailFS(t *testing.T) *vfs.FS {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home directory: %v", err)
	}
	root := filepath.Join(home, "TotalAnnihilation")
	if configured := os.Getenv("OPENTA_TA_ROOT"); configured != "" {
		root = configured
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("retail data unavailable: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount game directory: %v", err)
	}
	return fs
}

func TestRetailMainMenuResources(t *testing.T) {
	fs := retailFS(t)
	defer fs.Close()
	checks := []struct {
		path string
		ok   func() error
	}{
		{"guis/MAINMENU.GUI", func() error {
			gui, err := LoadGUIFile(fs, "guis/mainmenu.gui")
			if err != nil {
				return err
			}
			if len(gui.Gadgets) == 0 {
				t.Errorf("GUI has no gadgets")
			}
			return nil
		}},
		{"anims/MAINMENU.GAF", func() error {
			gaf, err := LoadGAFFile(fs, "anims/mainmenu.gaf")
			if err != nil {
				return err
			}
			if len(gaf.Entries) == 0 {
				t.Errorf("GAF has no entries")
			}
			return nil
		}},
		{"anims/HATTFONT12.GAF", func() error {
			gaf, err := LoadGAFFile(fs, "anims/hattfont12.gaf")
			if err != nil {
				return err
			}
			if _, ok := gaf.Find("Haettenschweiler (120)"); !ok {
				return fmt.Errorf("Hattenschweiler entry missing")
			}
			return nil
		}},
		{"fonts/SMLFONT.FNT", func() error {
			fnt, err := LoadFNTFile(fs, "fonts/smlfont.fnt")
			if err != nil {
				return err
			}
			if fnt.Glyphs['A'] == nil {
				t.Errorf("font has no A glyph")
			}
			return nil
		}},
		{"palettes/GUIPAL.PAL", func() error {
			palette, err := LoadPALFile(fs, "palettes/guipal.pal")
			if err != nil {
				return err
			}
			if palette.Colors[255].R != 255 {
				t.Errorf("palette white = %#v", palette.Colors[255])
			}
			return nil
		}},
		{"unitpics/ARMFLASH.PCX", func() error {
			pcx, err := LoadPCXFile(fs, "unitpics/armflash.pcx")
			if err != nil {
				return err
			}
			if pcx.Width != 96 || pcx.Height != 96 {
				t.Errorf("PCX size = %dx%d", pcx.Width, pcx.Height)
			}
			return nil
		}},
		{"units/ARMFLASH.FBI", func() error {
			doc, err := LoadTDF(fs, "units/armflash.fbi")
			if err != nil {
				return err
			}
			if doc.Root.Section("unitinfo") == nil {
				t.Errorf("FBI has no UNITINFO")
			}
			return nil
		}},
	}
	for _, check := range checks {
		if _, err := fs.Stat(check.path); err != nil {
			t.Fatalf("missing %s: %v", check.path, err)
		}
		if err := check.ok(); err != nil {
			t.Fatalf("load %s: %v", check.path, err)
		}
	}
}

func TestRetailMapResources(t *testing.T) {
	fs := retailFS(t)
	defer fs.Close()
	ota, err := LoadOTAFile(fs, "maps/The Pass.ota")
	if err != nil {
		t.Fatal(err)
	}
	if ota.MissionName != "The Pass" || !ota.HasNetworkSchema() {
		t.Fatalf("OTA metadata = %#v", ota)
	}
	tnt, err := LoadTNTFile(fs, "maps/The Pass.tnt")
	if err != nil {
		t.Fatal(err)
	}
	if tnt.Width == 0 || tnt.Height == 0 || tnt.MinimapWidth == 0 || len(tnt.Minimap) != int(tnt.MinimapWidth*tnt.MinimapHeight) {
		t.Fatalf("TNT metadata = %#v", tnt)
	}
}
