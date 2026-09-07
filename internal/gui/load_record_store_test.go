package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func guiRecordStoreFS(t *testing.T, panel string) vfs.FSOps {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.gui")
	if err := os.WriteFile(path, []byte(panel), 0o600); err != nil {
		t.Fatalf("write panel: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatalf("mount panel: %v", err)
	}
	return fs
}

func TestLoadMissingCommonKeepsDeterministicRecord(t *testing.T) {
	window, err := Load(guiRecordStoreFS(t, `
[GADGET0] { [COMMON] { id=0; name=HEADER; width=640; height=480; } }
[GADGET1] { gaffile=99; text=still parsed; }
`), "panel.gui")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(window.Gadgets) != 2 {
		t.Fatalf("gadgets = %d, want 2", len(window.Gadgets))
	}
	got := window.Gadgets[1]
	if got.Kind != KindPanel || got.Name != "" || got.Active != 0 || got.GAFFile != 0 || got.Rect != (Rect{}) {
		t.Fatalf("missing COMMON record = %+v, want deterministic zero common fields", got)
	}
	if got.Text != "still parsed" {
		t.Fatalf("type-specific text = %q, want preserved authored field", got.Text)
	}
}

func TestLoadCommonGAFFileUsesTypedDuplicateLookup(t *testing.T) {
	window, err := Load(guiRecordStoreFS(t, `
[GADGET0] { [COMMON] { id=0; name=HEADER; width=640; height=480; } }
[GADGET1] {
  [COMMON] {
    id=1;
    GAFFILE=3;
    gaffile=9;
  }
}
`), "panel.gui")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := window.Gadgets[1].GAFFile; got != 9 {
		t.Fatalf("GAFFile = %d, want last parsed case variant", got)
	}
}

func TestLoadStoresCoordinatesAsSigned16BeforeSentinels(t *testing.T) {
	window, err := Load(guiRecordStoreFS(t, `
[GADGET0] { [COMMON] { id=0; name=HEADER; width=640; height=480; } }
[GADGET1] {
  [COMMON] {
    id=1;
    name=WRAPPED;
    xpos=65535;
    ypos=65534;
    width=65537;
    height=65538;
    active=1;
  }
}
`), "panel.gui")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := window.Gadgets[1].Rect
	want := Rect{X: 319, Y: 478, W: 1, H: 2, RawX: -1, RawY: -2}
	if got != want {
		t.Fatalf("wrapped rect = %+v, want %+v", got, want)
	}
}
