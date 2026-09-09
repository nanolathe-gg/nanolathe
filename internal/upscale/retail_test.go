//go:build retail

package upscale

// The retail tier of the U1 gate: the two engine entries run on real assets
// and report what a first load of a map costs
// [DESIGN_GPU_RENDERER §14.4, §14.7 item 5]. It asserts only the shape of the
// result — the art itself is judged by looking at it — and prints the wall and
// CPU time so the load-time budget can be checked against the READMEs'
// numbers.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	retailpalette "github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func processCPU() time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// cacheFileSizes describes what one synthesis costs on disk, which is the
// number the load-time budget of §14.4 is spent against.
func cacheFileSizes(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, fmt.Sprintf("%.2f MiB", float64(info.Size())/(1<<20)))
	}
	return "cache files [" + strings.Join(parts, ", ") + "]"
}

func mountRetail(t *testing.T) *vfs.FS {
	t.Helper()
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		fs.Close()
		t.Fatalf("mount %s: %v", root, err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

func retailPalette(t *testing.T, fs *vfs.FS) ([256][3]uint8, []byte) {
	t.Helper()
	tables, err := retailpalette.Load(fs)
	if err != nil {
		t.Fatalf("palette: %v", err)
	}
	var palette [256][3]uint8
	for index := range palette {
		r, g, b, _ := tables.RGBA(byte(index))
		palette[index] = [3]uint8{r, g, b}
	}
	return palette, tables.Alpha[:]
}

// findMap returns the first of the named maps that the install holds, so the
// test does not depend on one campaign disc being present.
func findMap(t *testing.T, fs *vfs.FS, candidates ...string) string {
	t.Helper()
	have := map[string]string{}
	for _, entry := range fs.Entries() {
		logical := strings.ToLower(filepath.ToSlash(entry.Path))
		if entry.IsDir || !strings.HasPrefix(logical, "maps/") || !strings.HasSuffix(logical, ".tnt") {
			continue
		}
		have[strings.TrimSuffix(strings.TrimPrefix(logical, "maps/"), ".tnt")] = entry.Path
	}
	for _, name := range candidates {
		if path, ok := have[name]; ok {
			return path
		}
	}
	t.Skipf("none of %v is present under maps/", candidates)
	return ""
}

func TestRetailTiles2xTiming(t *testing.T) {
	fs := mountRetail(t)
	palette, alp := retailPalette(t, fs)
	logical := findMap(t, fs, "ac01", "great divide", "painted desert")
	tnt, err := formats.LoadTNTFile(fs, logical)
	if err != nil {
		t.Fatalf("load %s: %v", logical, err)
	}
	input := TerrainInput{
		Tiles:   make([][1024]byte, tnt.Tiles),
		TileMap: tnt.TileIndices,
		TilesW:  int(tnt.TileMapWidth), TilesH: int(tnt.TileMapHeight),
		Palette: palette, ALP: alp,
	}
	for index := range input.Tiles {
		copy(input.Tiles[index][:], tnt.TileGraphics[index*1024:])
	}

	cache := &Cache{Dir: t.TempDir()}
	wall, cpu := time.Now(), processCPU()
	tiles, cached, err := cache.Tiles2x(input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Fatal("an empty cache reported a hit")
	}
	t.Logf("Tiles2x %s: %d tiles, %dx%d placements, first load wall %s cpu %s",
		logical, len(tiles), input.TilesW, input.TilesH,
		time.Since(wall).Round(time.Millisecond), (processCPU() - cpu).Round(time.Millisecond))

	wall, cpu = time.Now(), processCPU()
	restored, cached, err := cache.Tiles2x(input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Fatal("the second load missed the cache")
	}
	t.Logf("cached load wall %s cpu %s, %s",
		time.Since(wall).Round(time.Millisecond), (processCPU() - cpu).Round(time.Millisecond),
		cacheFileSizes(t, cache.Dir))
	for index := range tiles {
		if tiles[index] != restored[index] {
			t.Fatalf("cached tile %d differs from the computed tile", index)
		}
	}

	if len(tiles) != len(input.Tiles) {
		t.Fatalf("got %d detail tiles for %d source tiles", len(tiles), len(input.Tiles))
	}
	// Every output tile must reduce back toward its source: the detail tile's
	// 2x2 block at (x,y) exists because the search matched the source pixel
	// there, so a tile of only zero bytes means a slot was never filled.
	for index := range tiles {
		if tiles[index] == ([4096]byte{}) && input.Tiles[index] != ([1024]byte{}) {
			t.Fatalf("detail tile %d is empty for a non-empty source tile", index)
		}
	}
}

func TestRetailBank2xTiming(t *testing.T) {
	fs := mountRetail(t)
	palette, alp := retailPalette(t, fs)
	bank, err := formats.LoadGAFFile(fs, "anims/trees.gaf")
	if err != nil {
		t.Fatalf("load anims/trees.gaf: %v", err)
	}
	// The tool's shipped exclusions: fire, explosion and reclaim art carries
	// colours the idle art never has.
	skip := func(name string) bool {
		lower := strings.ToLower(name)
		for _, part := range []string{"burn", "boom", "fire", "smoke", "rec"} {
			if strings.Contains(lower, part) {
				return true
			}
		}
		return false
	}

	cache := &Cache{Dir: t.TempDir()}
	wall, cpu := time.Now(), processCPU()
	detail, cached, err := cache.Bank2x(bank, []*formats.GAF{bank}, palette, alp, skip, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Fatal("an empty cache reported a hit")
	}
	elapsed, used := time.Since(wall), processCPU()-cpu

	synthesized, slots := 0, 0
	for entry := range detail.Entries {
		for _, ref := range detail.Entries[entry].Frames {
			slots++
			if ref.Frame != nil {
				synthesized++
			}
		}
	}
	t.Logf("Bank2x anims/trees.gaf: %d entries, %d of %d frame slots synthesized, first load wall %s cpu %s",
		len(detail.Entries), synthesized, slots, elapsed.Round(time.Millisecond), used.Round(time.Millisecond))

	wall, cpu = time.Now(), processCPU()
	if _, cached, err := cache.Bank2x(bank, []*formats.GAF{bank}, palette, alp, skip, Options{}); err != nil {
		t.Fatal(err)
	} else if !cached {
		t.Fatal("the second load missed the cache")
	}
	t.Logf("cached load wall %s cpu %s, %s",
		time.Since(wall).Round(time.Millisecond), (processCPU() - cpu).Round(time.Millisecond),
		cacheFileSizes(t, cache.Dir))
	if synthesized == 0 {
		t.Fatal("no frame of the bank was synthesized")
	}
	for entry := range bank.Entries {
		if detail.Entries[entry].Name != bank.Entries[entry].Name ||
			len(detail.Entries[entry].Frames) != len(bank.Entries[entry].Frames) {
			t.Fatalf("entry %d does not parallel the query bank", entry)
		}
	}
}
