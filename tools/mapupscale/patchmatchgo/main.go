// Command patchmatchgo is the terrain half of the 2x upscaler for retail
// Total Annihilation maps. It synthesizes a 64x64 tile for every unique 32x32
// tile of a map from the map's own authored pixels, so a "remastered" zoomed
// view can be built from original game data alone. See README.md in this
// directory for the algorithm and the reasoning behind it.
//
// The synthesizer itself lives in internal/upscale, which is what the engine
// calls at load time; this command is the wrapper that reads the decoded map
// export produced by tools/mapupscale/export and writes the tile set, the
// placement map and the inspection previews.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/nanolathe/nanolathe/internal/upscale"
)

const (
	sourceTileSize = 32
	paletteSize    = 256
)

type metadata struct {
	Map      string `json:"map"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	TileSize int    `json:"tile_size"`
}

type dataset struct {
	metadata metadata
	high     []byte
	palette  []byte
	alp      []byte
}

type tileMapFile struct {
	TileSize int   `json:"tile_size"`
	Columns  int   `json:"columns"`
	Tiles    int   `json:"tiles"`
	Width    int   `json:"width"`
	Height   int   `json:"height"`
	Map      []int `json:"map"`
}

func main() {
	defaults := upscale.DefaultTerrainParams()
	dataPath := flag.String("data", "", "decoded map export directory")
	outPrefix := flag.String("out", "/tmp/great-divide-patchmatch-go", "output filename prefix")
	iterations := flag.Int("iterations", defaults.Iterations, "PatchMatch refinement passes")
	samples := flag.Int("samples", defaults.Samples, "PCA patch samples")
	workers := flag.Int("workers", runtime.NumCPU(), "parallel CPU workers")
	tone := flag.Int("tone", defaults.Tone, "weight of the tone term: squared RGB-sum distance between a block and 4x its parent colour, 0 disables")
	spread := flag.Int("spread", defaults.Spread, "eighths of the mean tone error of already-chosen 3x3 neighbours that a candidate must offset")
	deadzone := flag.Int("deadzone", defaults.Deadzone, "per-channel RGB-sum tone error tolerated at no cost, so the tone term removes bias without favouring uniform blocks")
	seam := flag.Int("seam", defaults.Seam, "weight of the seam term: squared RGB distance between a candidate block's edge pixels and the adjacent pixels of already-chosen neighbour blocks, beyond the dead zone; 0 disables")
	seamZone := flag.Int("seamzone", defaults.SeamZone, "squared RGB distance between adjacent output pixels tolerated at no cost; -1 uses the authored map's own mean adjacent-pixel distance")
	coherence := flag.Int("coherence", defaults.Coherence, "weight of the coherence term: squared RGB distance between the authored full-resolution pixels around a candidate block (two rows above, two columns left, and their mirror on backward passes) and the output pixels already synthesized there; 0 disables")
	settle := flag.Int("settle", defaults.Settle, "skip the random-window and global draws for a pixel whose match survived this many consecutive passes unchanged (propagation still runs); 0 disables")
	relax := flag.Bool("relax", defaults.Relax, "also accept blocks that reduce to a palette entry one ALP step from the parent (their blend snaps to one of the two); false keeps the ALP cycle exact")
	profilePath := flag.String("cpuprofile", "", "write a CPU profile to this file")
	preview := flag.Bool("preview", false, "also write source and processed tile-atlas PNG previews")
	fullPreview := flag.Bool("full-preview", false, "also write the reassembled full-map PNG preview")
	flag.Parse()
	if *dataPath == "" {
		fmt.Fprintln(os.Stderr, "patchmatchgo: -data is required")
		os.Exit(2)
	}
	if *iterations < 0 || *workers < 1 || *samples < 1 || *tone < 0 || *spread < 0 {
		fmt.Fprintln(os.Stderr, "patchmatchgo: iterations/tone/spread must be non-negative and workers/samples positive")
		os.Exit(2)
	}
	if *profilePath != "" {
		file, err := os.Create(*profilePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "patchmatchgo:", err)
			os.Exit(1)
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			fmt.Fprintln(os.Stderr, "patchmatchgo:", err)
			os.Exit(1)
		}
		defer pprof.StopCPUProfile()
	}
	params := upscale.TerrainParams{
		Iterations: *iterations, Samples: *samples, Tone: *tone, Spread: *spread, Deadzone: *deadzone,
		Seam: *seam, SeamZone: *seamZone, Coherence: *coherence, Settle: *settle, Relax: *relax,
	}
	if err := run(*dataPath, *outPrefix, params, *workers, *preview || *fullPreview, *fullPreview); err != nil {
		fmt.Fprintln(os.Stderr, "patchmatchgo:", err)
		os.Exit(1)
	}
}

func run(dataPath, outPrefix string, params upscale.TerrainParams, workers int, preview, fullPreview bool) error {
	started := time.Now()
	data, err := loadDataset(dataPath)
	if err != nil {
		return err
	}
	loaded := time.Now()
	result, err := upscale.SynthesizeTerrain(upscale.TerrainMap{
		Pixels: data.high, Width: data.metadata.Width, Height: data.metadata.Height,
		Palette: unflattenPalette(data.palette), ALP: data.alp,
	}, params, upscale.Options{Workers: workers})
	if err != nil {
		return err
	}
	synthesized := time.Now()
	if err := writeTileCache(outPrefix, result); err != nil {
		return err
	}
	if preview {
		if err := writePreviews(outPrefix, result, data.palette, fullPreview); err != nil {
			return err
		}
	}
	finished := time.Now()

	stats := result.Stats
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	cpu := time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	fmt.Printf("iterations=%d mean-cost=%.2f unmatched=%d\n", stats.Iterations, stats.MeanCost, stats.Unmatched)
	fmt.Printf("map=%s size=%dx%d unique-tiles=%d placements=%d workers=%d\n",
		data.metadata.Map, data.metadata.Width, data.metadata.Height,
		result.UniqueTiles, len(result.TileMap), workers)
	fmt.Printf("load=%s database=%s pca=%s features=%s patchmatch=%s assemble=%s write=%s total=%s\n",
		loaded.Sub(started).Round(time.Millisecond), stats.Database.Round(time.Millisecond),
		stats.PCA.Round(time.Millisecond), stats.Features.Round(time.Millisecond),
		stats.PatchMatch.Round(time.Millisecond), stats.Assemble.Round(time.Millisecond),
		finished.Sub(synthesized).Round(time.Millisecond), finished.Sub(started).Round(time.Millisecond))
	fmt.Printf("cpu=%s (user+sys over the whole run; wall times above depend on machine load)\n", cpu.Round(time.Millisecond))
	fmt.Printf("seam=%d seamzone=%d\n", params.Seam, stats.SeamZone)
	fmt.Printf("database-pixels=%d supplemented=%d query-pixels=%d iterations=%d unmatched=%d mean-cost=%.2f cycle=%.4f heap-in-use=%0.1fMiB\n",
		stats.DatabasePixels, stats.Supplemented, stats.QueryPixels, stats.Iterations, stats.Unmatched,
		stats.MeanCost, stats.Cycle, float64(memory.HeapInuse)/1048576)
	return nil
}

func loadDataset(path string) (dataset, error) {
	var result dataset
	encoded, err := os.ReadFile(filepath.Join(path, "metadata.json"))
	if err != nil {
		return result, fmt.Errorf("read metadata: %w", err)
	}
	if err := json.Unmarshal(encoded, &result.metadata); err != nil {
		return result, fmt.Errorf("decode metadata: %w", err)
	}
	if result.metadata.Width <= 0 || result.metadata.Height <= 0 || result.metadata.TileSize != sourceTileSize {
		return result, fmt.Errorf("unsupported metadata dimensions %dx%d tile=%d",
			result.metadata.Width, result.metadata.Height, result.metadata.TileSize)
	}
	files := []struct {
		name string
		dst  *[]byte
		want int
	}{
		{"map-indexed.bin", &result.high, result.metadata.Width * result.metadata.Height},
		{"palette-rgb.bin", &result.palette, paletteSize * 3},
		{"palette.alp", &result.alp, paletteSize * paletteSize},
	}
	for _, file := range files {
		value, readErr := os.ReadFile(filepath.Join(path, file.name))
		if readErr != nil {
			return result, fmt.Errorf("read %s: %w", file.name, readErr)
		}
		if len(value) != file.want {
			return result, fmt.Errorf("%s has %d bytes, expected %d", file.name, len(value), file.want)
		}
		*file.dst = value
	}
	return result, nil
}

func unflattenPalette(rgb []byte) [256][3]uint8 {
	var palette [256][3]uint8
	for index := range palette {
		palette[index] = [3]uint8{rgb[index*3], rgb[index*3+1], rgb[index*3+2]}
	}
	return palette
}
