// Package upscale synthesizes 2x "detail" art for a battle out of the
// original game's own authored pixels: a 64x64 terrain tile for every 32x32
// map tile, and a parallel 2x sprite bank for a feature GAF. Nothing here
// invents art — every output block is an authored 2x2 block of the same map or
// the same bank, chosen because its own reduction through the retail palette
// blend table matches the pixel it replaces [03 §3.7].
//
// The two synthesizers moved here unchanged from
// tools/mapupscale/patchmatchgo and tools/mapupscale/featupscale, which are
// now thin wrappers over this package; their READMEs remain the description of
// the algorithms and of why each cost term exists. This package holds the
// engine-facing contract of docs/DESIGN_GPU_RENDERER.md §14.4: Tiles2x,
// Bank2x, and the on-disk result cache that makes a second load of a map a
// file read.
//
// Everything here is deterministic. Seeds derive from tile, frame and sample
// indices and each tile (and each animation entry) is independent, so the
// worker count never changes a result; the cache depends on that.
package upscale

import (
	"errors"
	"fmt"
	"runtime"
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

	featureScaleFirst = 16
	featureScaleRest  = 4
)

// Options carry what the caller controls about one synthesis rather than what
// it computes. The zero value is the tools' shipped behaviour: every worker
// the machine has and no progress reporting.
type Options struct {
	// Workers is the number of parallel CPU workers; 0 means
	// runtime.NumCPU(). It never changes the result.
	Workers int
	// Progress, when non-nil, is called as the synthesis advances with the
	// number of units finished and the total. Calls are serialized and the
	// done count is monotonic, so a loading bar can read it directly. The
	// unit is one unique terrain tile, or one synthesized sprite frame.
	Progress func(done, total int)
}

func (o Options) workers() int {
	if o.Workers > 0 {
		return o.Workers
	}
	return runtime.NumCPU()
}

// terrainData is one map's authored pixels in the form the search works on:
// row-major palette indices, the 768-byte RGB palette and the 64 KiB ALP
// blend table.
type terrainData struct {
	width, height int
	high          []byte
	palette       []byte
	alp           []byte
}

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

// searchOptions holds the cost-term weights; see patchMatch.
type searchOptions struct {
	tone, spread, deadzone int32
	relax                  bool
	seam, seamZone         int32
	coherence              int32
	settle                 int
}

// TerrainParams are the terrain synthesizer's tuning knobs. They are the
// terrain tool's flags; DefaultTerrainParams returns the shipped defaults and
// is what the engine uses. The meaning of each term is in
// tools/mapupscale/patchmatchgo/README.md.
type TerrainParams struct {
	Iterations int // PatchMatch refinement passes
	Samples    int // PCA patch samples
	Tone       int // weight of the tone term, 0 disables
	Spread     int // eighths of the neighbours' mean tone error a candidate offsets
	Deadzone   int // per-channel RGB-sum tone error tolerated at no cost
	Seam       int // weight of the seam term, 0 disables
	SeamZone   int // squared RGB distance tolerated at no cost; -1 = authored mean
	Coherence  int // weight of the coherence term, 0 disables
	Settle     int // passes a match must survive before its draws are skipped
	Relax      bool
}

// DefaultTerrainParams returns the terrain tool's shipped defaults, which are
// also the values the engine synthesizes with.
func DefaultTerrainParams() TerrainParams {
	return TerrainParams{
		Iterations: 8, Samples: 100_000, Tone: 12, Spread: 16, Deadzone: 8,
		Seam: 0, SeamZone: -1, Coherence: 64, Settle: 2, Relax: true,
	}
}

func (p TerrainParams) validate() error {
	if p.Iterations < 0 || p.Tone < 0 || p.Spread < 0 {
		return errors.New("nanolathe: terrain upscale: iterations, tone and spread must be non-negative")
	}
	if p.Samples < 1 {
		return errors.New("nanolathe: terrain upscale: samples must be positive")
	}
	return nil
}

// TerrainMap is a whole map's authored pixels: the form the terrain tool reads
// from its export dataset. Pixels is Width*Height row-major palette indices,
// Palette is RGB per index and ALP is the 65536-byte blend table indexed
// ALP[a*256+b].
type TerrainMap struct {
	Pixels        []byte
	Width, Height int
	Palette       [256][3]uint8
	ALP           []byte
}

// TerrainStats are the summary numbers the terrain tool prints. They describe
// the search, not the art.
type TerrainStats struct {
	DatabasePixels int
	Supplemented   int
	QueryPixels    int
	Iterations     int
	Unmatched      int
	MeanCost       float64
	Cycle          float64
	SeamZone       int32
	Database       time.Duration
	PCA            time.Duration
	Features       time.Duration
	PatchMatch     time.Duration
	Assemble       time.Duration
}

// TerrainResult is one map's synthesized detail tile set in *atlas* order: the
// unique 32x32 tiles of the map, numbered by first appearance, one 64x64 tile
// each. TileMap sends a placement to its atlas tile.
type TerrainResult struct {
	// Tiles holds UniqueTiles consecutive 64x64 index tiles.
	Tiles []byte
	// Sources holds the same tiles at the authored 32x32 size, in the same
	// order, which is what the previews compare against.
	Sources []byte
	// TileMap[placement] is the atlas tile at that placement, row-major over
	// MapWidth x MapHeight placements.
	TileMap                []int
	UniqueTiles            int
	MapWidth, MapHeight    int
	Columns                int // atlas columns the preview and tile map use
	SourceSize, OutputSize int
	Stats                  TerrainStats
}

// SynthesizeTerrain runs the terrain upscaler over one whole map. It is the
// entry the terrain tool uses; the engine goes through Tiles2x, which speaks
// tile sets rather than composed maps.
func SynthesizeTerrain(m TerrainMap, params TerrainParams, opts Options) (TerrainResult, error) {
	if err := params.validate(); err != nil {
		return TerrainResult{}, err
	}
	if m.Width <= 0 || m.Height <= 0 || m.Width%sourceTileSize != 0 || m.Height%sourceTileSize != 0 {
		return TerrainResult{}, fmt.Errorf("nanolathe: terrain upscale: map is %dx%d, expected a positive multiple of %d on both axes",
			m.Width, m.Height, sourceTileSize)
	}
	if len(m.Pixels) != m.Width*m.Height {
		return TerrainResult{}, fmt.Errorf("nanolathe: terrain upscale: %d pixels for a %dx%d map, expected %d",
			len(m.Pixels), m.Width, m.Height, m.Width*m.Height)
	}
	if len(m.ALP) != paletteSize*paletteSize {
		return TerrainResult{}, fmt.Errorf("nanolathe: terrain upscale: ALP table has %d bytes, expected %d",
			len(m.ALP), paletteSize*paletteSize)
	}
	workers := opts.workers()
	data := terrainData{width: m.Width, height: m.Height, high: m.Pixels, palette: flatPalette(m.Palette), alp: m.ALP}
	options := searchOptions{
		tone: int32(params.Tone), spread: int32(params.Spread), deadzone: int32(params.Deadzone),
		relax: params.Relax, seam: int32(params.Seam), seamZone: int32(params.SeamZone),
		coherence: int32(params.Coherence), settle: params.Settle,
	}

	started := time.Now()
	atlas := makeTileAtlas(data)
	db := buildDatabase(atlas, data.alp, workers)
	prepareDatabaseCandidates(&db, data)
	built := time.Now()
	basis := makePCABasis(db, atlas, data.palette, params.Samples, workers)
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
	matches, costs, unmatched := patchMatch(queries, db, records, data.palette, atlas,
		params.Iterations, options, workers, opts.Progress)
	matched := time.Now()
	cycle := cycleConsistency(queries.parents, matches, records)
	tiles := assembleTiles(queries.parents, matches, db)
	assembled := time.Now()

	meanCost, _ := costSummary(costs)
	return TerrainResult{
		Tiles:       tiles,
		Sources:     queries.parents,
		TileMap:     queries.tileMap,
		UniqueTiles: len(queries.firstPlacements),
		MapWidth:    queries.mapWidth,
		MapHeight:   queries.mapHeight,
		Columns:     atlasColumns,
		SourceSize:  sourceTileSize,
		OutputSize:  outputTileSize,
		Stats: TerrainStats{
			DatabasePixels: len(db.low), Supplemented: supplemented, QueryPixels: len(queries.parents),
			Iterations: params.Iterations, Unmatched: unmatched, MeanCost: meanCost, Cycle: cycle,
			SeamZone: options.seamZone,
			Database: built.Sub(started), PCA: pcaDone.Sub(built), Features: featuresDone.Sub(pcaDone),
			PatchMatch: matched.Sub(featuresDone), Assemble: assembled.Sub(matched),
		},
	}, nil
}

// TerrainInput is a map's tile set and placements. Tiles are 32x32 index
// bytes; TileMap is row-major TilesW*TilesH tile ids.
type TerrainInput struct {
	Tiles          [][1024]byte
	TileMap        []uint16
	TilesW, TilesH int
	Palette        [256][3]uint8 // RGB per index
	ALP            []byte        // 65536 bytes, ALP[a*256+b]
}

// Tiles2x returns one 64x64 index tile per input tile, in input order.
//
// The synthesizer works on the map as laid out — a tile's halo of authored
// neighbours is what tells it which detail belongs there — so the tile set is
// composed back into the map, upscaled, and the result redistributed to the
// input tile ids. Two input tiles with identical pixels share one synthesis
// and therefore one result, which is what the deduplicating atlas already did
// inside the tool.
//
// A tile the tile map never places has no authored neighbourhood anywhere on
// the map and so has no query: it is doubled by nearest sampling, the same
// fallback the client uses for art the remaster does not cover [D2].
func Tiles2x(in TerrainInput, opts Options) ([][4096]byte, error) {
	if in.TilesW <= 0 || in.TilesH <= 0 {
		return nil, fmt.Errorf("nanolathe: terrain upscale: tile map is %dx%d tiles, expected positive dimensions",
			in.TilesW, in.TilesH)
	}
	if len(in.TileMap) != in.TilesW*in.TilesH {
		return nil, fmt.Errorf("nanolathe: terrain upscale: tile map has %d entries for %dx%d tiles, expected %d",
			len(in.TileMap), in.TilesW, in.TilesH, in.TilesW*in.TilesH)
	}
	if len(in.Tiles) == 0 {
		return nil, errors.New("nanolathe: terrain upscale: tile set is empty")
	}
	width, height := in.TilesW*sourceTileSize, in.TilesH*sourceTileSize
	pixels := make([]byte, width*height)
	for placement, id := range in.TileMap {
		if int(id) >= len(in.Tiles) {
			return nil, fmt.Errorf("nanolathe: terrain upscale: placement %d names tile %d outside the %d-tile set",
				placement, id, len(in.Tiles))
		}
		tile := &in.Tiles[id]
		tileY, tileX := placement/in.TilesW, placement%in.TilesW
		for row := range sourceTileSize {
			destination := (tileY*sourceTileSize+row)*width + tileX*sourceTileSize
			copy(pixels[destination:destination+sourceTileSize], tile[row*sourceTileSize:(row+1)*sourceTileSize])
		}
	}
	result, err := SynthesizeTerrain(TerrainMap{
		Pixels: pixels, Width: width, Height: height, Palette: in.Palette, ALP: in.ALP,
	}, DefaultTerrainParams(), opts)
	if err != nil {
		return nil, err
	}
	detail := make([][4096]byte, len(in.Tiles))
	filled := make([]bool, len(in.Tiles))
	for placement, id := range in.TileMap {
		if filled[id] {
			continue
		}
		synthesized := result.TileMap[placement]
		copy(detail[id][:], result.Tiles[synthesized*outputTileSize*outputTileSize:])
		filled[id] = true
	}
	for id := range detail {
		if !filled[id] {
			detail[id] = doubleTile(&in.Tiles[id])
		}
	}
	return detail, nil
}

// doubleTile is nearest doubling: every authored pixel becomes a 2x2 block.
func doubleTile(tile *[1024]byte) [4096]byte {
	var out [4096]byte
	for y := range sourceTileSize {
		for x := range sourceTileSize {
			value := tile[y*sourceTileSize+x]
			base := 2*y*outputTileSize + 2*x
			out[base] = value
			out[base+1] = value
			out[base+outputTileSize] = value
			out[base+outputTileSize+1] = value
		}
	}
	return out
}

func flatPalette(palette [256][3]uint8) []byte {
	out := make([]byte, paletteSize*3)
	for index, entry := range palette {
		out[index*3], out[index*3+1], out[index*3+2] = entry[0], entry[1], entry[2]
	}
	return out
}
