// Command patchmatchgo is a prototype 2x terrain upscaler for retail Total
// Annihilation maps. It synthesizes a 64x64 tile for every unique 32x32 tile
// of a map from the map's own authored pixels, so a "remastered" zoomed view
// can be built from original game data alone. See README.md in this directory
// for the algorithm and the reasoning behind it.
//
// It consumes the decoded map export produced by tools/mapupscale/export.
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
)

const (
	sourceTileSize = 32
	outputTileSize = 64
	paletteSize    = 256
	patchRadius    = 2
	patchSpan      = 2*patchRadius + 1
	patchValues    = patchSpan * patchSpan * 3
	featureDims    = 8 // PCA dimensions kept per neighborhood
	atlasColumns   = 64
	// haloSize is the border of authored neighbors kept around each unique
	// tile: a 5x5 neighborhood of ALP-reduced pixels spans 10 source pixels,
	// so a block inside the tile needs at most 4 pixels beyond its edge.
	haloSize = 2 * patchRadius
	cellSize = sourceTileSize + 2*haloSize
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

const (
	featureScaleFirst = 16
	featureScaleRest  = 4
)

// tileAtlas lays every unique 32x32 tile, surrounded by the halo of its
// first placement, on a grid. Both the example database and the queries are
// derived from it, so the work is proportional to unique tiles rather than
// placements.
type tileAtlas struct {
	pix             []byte
	width, height   int
	columns         int
	firstPlacements []int
	tileMap         []int
	mapWidth        int
	mapHeight       int
}

type tileQueries struct {
	parents         []byte
	features        [][featureDims]int8
	firstPlacements []int
	tileMap         []int
	mapWidth        int
	mapHeight       int
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
	dataPath := flag.String("data", "", "decoded map export directory")
	outPrefix := flag.String("out", "/tmp/great-divide-patchmatch-go", "output filename prefix")
	iterations := flag.Int("iterations", 8, "PatchMatch refinement passes")
	samples := flag.Int("samples", 100_000, "PCA patch samples")
	workers := flag.Int("workers", runtime.NumCPU(), "parallel CPU workers")
	tone := flag.Int("tone", 12, "weight of the tone term: squared RGB-sum distance between a block and 4x its parent colour, 0 disables")
	spread := flag.Int("spread", 16, "eighths of the mean tone error of already-chosen 3x3 neighbours that a candidate must offset")
	deadzone := flag.Int("deadzone", 8, "per-channel RGB-sum tone error tolerated at no cost, so the tone term removes bias without favouring uniform blocks")
	seam := flag.Int("seam", 0, "weight of the seam term: squared RGB distance between a candidate block's edge pixels and the adjacent pixels of already-chosen neighbour blocks, beyond the dead zone; 0 disables")
	seamZone := flag.Int("seamzone", -1, "squared RGB distance between adjacent output pixels tolerated at no cost; -1 uses the authored map's own mean adjacent-pixel distance")
	coherence := flag.Int("coherence", 64, "weight of the coherence term: squared RGB distance between the authored full-resolution pixels around a candidate block (two rows above, two columns left, and their mirror on backward passes) and the output pixels already synthesized there; 0 disables")
	settle := flag.Int("settle", 2, "skip the random-window and global draws for a pixel whose match survived this many consecutive passes unchanged (propagation still runs); 0 disables")
	relax := flag.Bool("relax", true, "also accept blocks that reduce to a palette entry one ALP step from the parent (their blend snaps to one of the two); false keeps the ALP cycle exact")
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
	options := searchOptions{tone: int32(*tone), spread: int32(*spread), deadzone: int32(*deadzone), relax: *relax,
		seam: int32(*seam), seamZone: int32(*seamZone), coherence: int32(*coherence), settle: *settle}
	if err := run(*dataPath, *outPrefix, *iterations, *samples, *workers, options,
		*preview || *fullPreview, *fullPreview); err != nil {
		fmt.Fprintln(os.Stderr, "patchmatchgo:", err)
		os.Exit(1)
	}
}

// searchOptions holds the tone term weights; see patchMatch.
type searchOptions struct {
	tone, spread, deadzone int32
	relax                  bool
	seam, seamZone         int32
	coherence              int32
	settle                 int
}

func run(dataPath, outPrefix string, iterations, samples, workers int, options searchOptions,
	preview, fullPreview bool) error {
	started := time.Now()
	data, err := loadDataset(dataPath)
	if err != nil {
		return err
	}
	loaded := time.Now()
	atlas := makeTileAtlas(data)
	db := buildDatabase(atlas, data.alp, workers)
	prepareDatabaseCandidates(&db, data)
	built := time.Now()
	basis := makePCABasis(db, atlas, data.palette, samples, workers)
	projected := makeContributions(basis, data.palette)
	pcaDone := time.Now()
	records := databaseRecords(db, projected, workers)
	supplemented := supplementMissingParents(&db, &records, data, projected, workers)
	buildHashBuckets(&db, records, workers)
	weights := exampleWeights(db, atlas)
	db.parentSampler = makeSampler(db.positions, db.offsets[:], db.counts[:], weights)
	db.bucketSampler = makeSampler(db.hashPositions, db.hashOffsets, db.hashCounts, weights)
	relaxParents(&db, records, data.alp, data.palette, options.relax)
	queries := makeTileQueries(data, atlas, projected, workers)
	if options.seamZone < 0 {
		options.seamZone = authoredAdjacentDistance(data)
	}
	featuresDone := time.Now()
	matches, costs, unmatched := patchMatch(queries, db, records, data.palette, atlas, iterations, options, workers)
	matched := time.Now()
	cycle := cycleConsistency(queries.parents, matches, records)
	tiles := assembleTiles(queries.parents, matches, db)
	assembled := time.Now()
	if err := writeTileCache(outPrefix, tiles, queries); err != nil {
		return err
	}
	if preview {
		if err := writePreviews(outPrefix, tiles, queries, data.palette, fullPreview); err != nil {
			return err
		}
	}
	finished := time.Now()

	meanCost, _ := costSummary(costs)
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	cpu := time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	fmt.Printf("map=%s size=%dx%d unique-tiles=%d placements=%d workers=%d\n",
		data.metadata.Map, data.metadata.Width, data.metadata.Height,
		len(queries.firstPlacements), len(queries.tileMap), workers)
	fmt.Printf("load=%s database=%s pca=%s features=%s patchmatch=%s assemble=%s write=%s total=%s\n",
		loaded.Sub(started).Round(time.Millisecond), built.Sub(loaded).Round(time.Millisecond),
		pcaDone.Sub(built).Round(time.Millisecond), featuresDone.Sub(pcaDone).Round(time.Millisecond),
		matched.Sub(featuresDone).Round(time.Millisecond), assembled.Sub(matched).Round(time.Millisecond),
		finished.Sub(assembled).Round(time.Millisecond), finished.Sub(started).Round(time.Millisecond))
	fmt.Printf("cpu=%s (user+sys over the whole run; wall times above depend on machine load)\n", cpu.Round(time.Millisecond))
	fmt.Printf("seam=%d seamzone=%d\n", options.seam, options.seamZone)
	fmt.Printf("database-pixels=%d supplemented=%d query-pixels=%d iterations=%d unmatched=%d mean-cost=%.2f cycle=%.4f heap-in-use=%0.1fMiB\n",
		len(db.low), supplemented, len(queries.parents), iterations, unmatched, meanCost, cycle,
		float64(memory.HeapInuse)/1048576)
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
