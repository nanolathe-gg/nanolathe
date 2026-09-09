package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestSkirmishPalettePreflightAdmitsPCXRecovery(t *testing.T) {
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
	p := &skirmishPreflight{fs: fs, manifest: &SkirmishManifest{}}
	p.paletteBundle()
	result := p.finish()
	if err := result.Error(); err != nil {
		t.Fatal(err)
	}
	if len(result.Assets) != 2 || result.Assets[0].Logical != "palettes/guipal.pcx" || result.Assets[1].Logical != "palettes/palette.pcx" {
		t.Fatalf("recovery manifest retained unavailable PAL/table files: %+v", result.Assets)
	}
}
