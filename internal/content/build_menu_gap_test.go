package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestBuildMenuEndsAtFirstAbsentKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "gamedata"), 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`[CANBUILD] {
        [builder] { canbuild1=first; canbuild2=; canbuild3=third; canbuild5=fifth; canbuild18446744073709551617=overflow; }
        [missingfirst] { canbuild2=second; }
    }`)
	if err := os.WriteFile(filepath.Join(dir, "gamedata", "sidedata.tdf"), data, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	pages, err := CompileBuildMenus(fs)
	if err != nil {
		t.Fatal(err)
	}
	got := pages["builder"].Buttons
	if len(got) != 2 || got[0] != "first" || got[1] != "third" {
		t.Fatalf("build list passed its first absent key: %v", got)
	}
	if len(pages["missingfirst"].Buttons) != 0 {
		t.Fatal("build list skipped its missing first key")
	}
}
