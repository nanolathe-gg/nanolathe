package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// A PCX-only palette installation loads: the loader's recovery policy accepts
// it rather than demanding the PAL files [02 R-MALF-01 §9]. internal/palette
// owns the per-file recovery vectors; this pins the whole-install case the
// content pipeline depends on.
func TestPCXOnlyInstallLoadsPalette(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "palettes"), 0700); err != nil {
		t.Fatal(err)
	}
	pcx := make([]byte, 129+768)
	pcx[0], pcx[1], pcx[66] = 10, 5, 1
	for _, name := range []string{"palette.pcx", "guipal.pcx"} {
		if err := os.WriteFile(filepath.Join(root, "palettes", name), pcx, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if _, err := palette.Load(fs); err != nil {
		t.Fatalf("PCX-only installation refused: %v", err)
	}
}
