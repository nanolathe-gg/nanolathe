package palette

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func recoveryFixture(t *testing.T, files map[string][]byte) (*vfs.FS, string) {
	t.Helper()
	root := t.TempDir()
	for name, data := range files {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs, root
}

func recoveryPaletteFiles() map[string][]byte {
	pal := make([]byte, 1024)
	for i := 0; i < 256; i++ {
		pal[i*4], pal[i*4+1], pal[i*4+2] = byte(i), byte(i), byte(i)
	}
	pcx := make([]byte, 129+768) // independently authored one-pixel PCX
	pcx[0], pcx[1], pcx[2], pcx[3], pcx[65] = 10, 5, 1, 8, 1
	binary.LittleEndian.PutUint16(pcx[66:], 1)
	for i := 0; i < 256; i++ {
		pcx[129+i*3], pcx[130+i*3], pcx[131+i*3] = byte(i), byte(i), byte(i)
	}
	return map[string][]byte{
		"palettes/palette.pal": pal, "palettes/guipal.pal": pal,
		"palettes/palette.pcx": pcx, "palettes/guipal.pcx": pcx,
		"palettes/palette.alp": bytes.Repeat([]byte{238}, 65536),
		"palettes/palette.lht": bytes.Repeat([]byte{238}, 8192),
		"palettes/palette.shd": bytes.Repeat([]byte{238}, 8192),
	}
}

func TestPaletteRecoveryRebuildsTablesWithoutChangingAssets(t *testing.T) {
	for _, name := range []string{"palettes/palette.pal", "palettes/guipal.pal"} {
		for _, empty := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/missing", true: "/empty"}[empty], func(t *testing.T) {
				files := recoveryPaletteFiles()
				if empty {
					files[name] = nil
				} else {
					delete(files, name)
				}
				fs, root := recoveryFixture(t, files)
				tables, err := Load(fs)
				if err != nil {
					t.Fatal(err)
				}
				if tables.Base[100] != [4]byte{100, 100, 100, 255} || tables.GUI[200] != [4]byte{200, 200, 200, 255} {
					t.Fatal("PCX recovery changed base colors")
				}
				if tables.Alpha[100*256+200] != 150 || tables.Alpha[101*256+200] != 150 {
					t.Fatal("blend did not floor the channel average")
				}
				if tables.Light[30*256+100] != 200 || tables.Shade[15][100] != 103 {
					t.Fatalf("wrong independent light/shade factors: %d/%d", tables.Light[30*256+100], tables.Shade[15][100])
				}
				if tables.Gray[100] != 100 || tables.Logical[100] != 100 {
					t.Fatal("recovered derived maps disagree with palette")
				}
				got, readErr := os.ReadFile(filepath.Join(root, name))
				if empty {
					if readErr != nil || len(got) != 0 {
						t.Fatal("empty source PAL was changed")
					}
				} else if !os.IsNotExist(readErr) {
					t.Fatal("missing source PAL was written")
				}
				original, err := os.ReadFile(filepath.Join(root, "palettes/palette.alp"))
				if err != nil || !bytes.Equal(original, files["palettes/palette.alp"]) {
					t.Fatal("authored derived table was changed")
				}
			})
		}
	}
}

func TestPaletteRecoveryPreservesOrdinaryFilesAndRejectsOtherFailures(t *testing.T) {
	files := recoveryPaletteFiles()
	fs, _ := recoveryFixture(t, files)
	tables, err := Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	if tables.Alpha[100*256+200] != 238 || tables.Light[30*256+100] != 238 || tables.Shade[15][100] != 238 {
		t.Fatal("nonempty authored tables were regenerated")
	}
	delete(files, "palettes/palette.pal")
	delete(files, "palettes/palette.pcx")
	fs, _ = recoveryFixture(t, files)
	if _, err = Load(fs); err == nil || !strings.Contains(err.Error(), "logical path palettes/palette.pcx, providers searched [") || !strings.Contains(err.Error(), "expected PCX recovery palette") {
		t.Fatalf("missing recovery diagnostic: %v", err)
	}
	files = recoveryPaletteFiles()
	files["palettes/palette.pal"] = []byte{1}
	fs, _ = recoveryFixture(t, files)
	if _, err = Load(fs); err == nil || !strings.Contains(err.Error(), "logical path palettes/palette.pal") {
		t.Fatalf("nonempty malformed PAL incorrectly recovered: %v", err)
	}
}

func TestMissingDerivedTableBuildsOnlyItsOwnTable(t *testing.T) {
	files := recoveryPaletteFiles()
	files["palettes/palette.lht"] = nil
	fs, _ := recoveryFixture(t, files)
	tables, err := Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	if tables.Light[30*256+100] != 200 || tables.Alpha[100*256+200] != 238 || tables.Shade[15][100] != 238 {
		t.Fatal("independent derived table recovery changed another table")
	}
}

func TestBlendDiagonalPreservesDuplicatePaletteIndices(t *testing.T) {
	var tables Tables // every entry is an identical black
	buildAlphaTable(&tables)
	if tables.Alpha[77*256+77] != 77 || tables.Alpha[77*256+78] != 0 {
		t.Fatal("diagonal bypass or nearest-color tie changed")
	}
}
