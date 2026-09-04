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
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
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

type database struct {
	width       int
	phaseHeight int
	low         []byte
	blocks      [4][]byte
	valid       []byte
	positions   []int32
	offsets     [paletteSize]int
	counts      [paletteSize]int

	hashCounts    []int32
	hashOffsets   []int32
	hashPositions []int32

	parentSampler sampler
	bucketSampler sampler

	// allowed[p][k] is set when a block reducing to k may stand in for parent
	// p, and neighbours lists those k per parent for random draws.
	allowed    [paletteSize][paletteSize]bool
	neighbours sampler
}

// sampler draws from grouped candidate lists with per-entry weights in O(1)
// using alias tables (Vose's method), one table per group.
type sampler struct {
	entries []int32
	offsets []int32 // group g owns entries[offsets[g]:offsets[g+1]]
	prob    []uint32
	alias   []int32
}

func (s sampler) draw(group int, random uint64) int32 {
	begin, end := int(s.offsets[group]), int(s.offsets[group+1])
	count := end - begin
	if count == 0 {
		return -1
	}
	slot := begin + int(random%uint64(count))
	if uint32(random>>32) < s.prob[slot] {
		return s.entries[slot]
	}
	return s.entries[s.alias[slot]]
}

// exampleWeights gives every database position the number of placements that
// share its atlas tile, so random draws follow the map's own tile frequencies
// as they would over the full map. Supplement records weigh one.
func exampleWeights(db database, atlas tileAtlas) []uint32 {
	weights := make([]uint32, len(db.low))
	for index := range weights {
		weights[index] = 1
	}
	multiplicity := make([]uint32, len(atlas.firstPlacements))
	for _, tile := range atlas.tileMap {
		multiplicity[tile]++
	}
	phasePositions := 4 * db.phaseHeight * db.width
	for position := 0; position < phasePositions; position++ {
		y, x := position/db.width, position%db.width
		phase := y / db.phaseHeight
		sourceY := phase/2 + 2*(y%db.phaseHeight)
		sourceX := phase%2 + 2*x
		tile := (sourceY/cellSize)*atlas.columns + sourceX/cellSize
		if tile < len(multiplicity) {
			weights[position] = multiplicity[tile]
		}
	}
	return weights
}

func makeSampler[T int | int32](entries []int32, offsets []T, counts []T, weights []uint32) sampler {
	groups := len(counts)
	result := sampler{
		entries: entries,
		offsets: make([]int32, groups+1),
		prob:    make([]uint32, len(entries)),
		alias:   make([]int32, len(entries)),
	}
	for group := range groups {
		result.offsets[group+1] = int32(offsets[group]) + int32(counts[group])
	}
	var small, large []int32
	var scaled []float64
	for group := range groups {
		begin, end := int(result.offsets[group]), int(result.offsets[group+1])
		count := end - begin
		if count == 0 {
			continue
		}
		total := 0.0
		for slot := begin; slot < end; slot++ {
			total += float64(weights[entries[slot]])
		}
		scaled = scaled[:0]
		small, large = small[:0], large[:0]
		for slot := begin; slot < end; slot++ {
			value := float64(weights[entries[slot]]) * float64(count) / total
			scaled = append(scaled, value)
			if value < 1 {
				small = append(small, int32(slot))
			} else {
				large = append(large, int32(slot))
			}
		}
		for len(small) > 0 && len(large) > 0 {
			less, more := small[len(small)-1], large[len(large)-1]
			small, large = small[:len(small)-1], large[:len(large)-1]
			result.prob[less] = uint32(math.Min(scaled[int(less)-begin], 1) * 4294967295)
			result.alias[less] = more
			scaled[int(more)-begin] += scaled[int(less)-begin] - 1
			if scaled[int(more)-begin] < 1 {
				small = append(small, more)
			} else {
				large = append(large, more)
			}
		}
		for _, rest := range [][]int32{small, large} {
			for _, slot := range rest {
				result.prob[slot] = math.MaxUint32
				result.alias[slot] = slot
			}
		}
	}
	return result
}

// record packs everything a candidate test needs into one contiguous 18-byte
// unit: the quantized PCA features and the ALP parent index (or -1 when the
// position is excluded), so one random memory access decides a candidate
// instead of three.
type record struct {
	feat [8]int16
	key  int16
	sum  [3]int16 // RGB sums of the block's four pixels, 0..1020 per channel
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
	features        [][8]int16
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
	tone := flag.Int("tone", 4, "weight of the tone term: squared RGB-sum distance between a block and 4x its parent colour, 0 disables")
	spread := flag.Int("spread", 16, "eighths of the mean tone error of already-chosen 3x3 neighbours that a candidate must offset")
	deadzone := flag.Int("deadzone", 8, "per-channel RGB-sum tone error tolerated at no cost, so the tone term removes bias without favouring uniform blocks")
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
	options := searchOptions{tone: int32(*tone), spread: int32(*spread), deadzone: int32(*deadzone), relax: *relax}
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
	records := databaseRecords(db, data.palette, projected, workers)
	supplemented := supplementMissingParents(&db, &records, data, projected, workers)
	buildHashBuckets(&db, records, workers)
	weights := exampleWeights(db, atlas)
	db.parentSampler = makeSampler(db.positions, db.offsets[:], db.counts[:], weights)
	db.bucketSampler = makeSampler(db.hashPositions, db.hashOffsets, db.hashCounts, weights)
	relaxParents(&db, records, data.alp, data.palette, options.relax)
	queries := makeTileQueries(data, atlas, projected, workers)
	featuresDone := time.Now()
	matches, costs, unmatched := patchMatch(queries, db, records, data.palette, iterations, options, workers)
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

func makeTileAtlas(data dataset) tileAtlas {
	mapWidth := data.metadata.Width / sourceTileSize
	mapHeight := data.metadata.Height / sourceTileSize
	first := make([]int, 0, mapWidth*mapHeight)
	tileMap := make([]int, mapWidth*mapHeight)
	seen := make(map[string]int)
	var tile [sourceTileSize * sourceTileSize]byte
	for placement := range tileMap {
		tileY, tileX := placement/mapWidth, placement%mapWidth
		for row := range sourceTileSize {
			source := (tileY*sourceTileSize+row)*data.metadata.Width + tileX*sourceTileSize
			copy(tile[row*sourceTileSize:(row+1)*sourceTileSize], data.high[source:source+sourceTileSize])
		}
		key := string(tile[:])
		identifier, ok := seen[key]
		if !ok {
			identifier = len(first)
			seen[key] = identifier
			first = append(first, placement)
		}
		tileMap[placement] = identifier
	}
	rows := (len(first) + atlasColumns - 1) / atlasColumns
	atlas := tileAtlas{
		width: atlasColumns * cellSize, height: rows * cellSize, columns: atlasColumns,
		firstPlacements: first, tileMap: tileMap, mapWidth: mapWidth, mapHeight: mapHeight,
	}
	atlas.pix = make([]byte, atlas.width*atlas.height)
	for identifier, placement := range first {
		tileY, tileX := placement/mapWidth, placement%mapWidth
		cellY, cellX := (identifier/atlasColumns)*cellSize, (identifier%atlasColumns)*cellSize
		for y := range cellSize {
			sourceY := clamp(tileY*sourceTileSize+y-haloSize, 0, data.metadata.Height-1)
			for x := range cellSize {
				sourceX := clamp(tileX*sourceTileSize+x-haloSize, 0, data.metadata.Width-1)
				atlas.pix[(cellY+y)*atlas.width+cellX+x] = data.high[sourceY*data.metadata.Width+sourceX]
			}
		}
	}
	return atlas
}

// buildDatabase reduces the atlas through the retail ALP blend at the four
// 2x2 phase offsets. A position is valid only when the 10x10 source footprint
// of its neighborhood lies inside one atlas cell, so no example straddles two
// unrelated tiles.
func buildDatabase(atlas tileAtlas, alp []byte, workers int) database {
	phaseHeight := (atlas.height - 1) &^ 1
	phaseWidth := (atlas.width - 1) &^ 1
	phaseHeight /= 2
	phaseWidth /= 2
	phasePixels := phaseHeight * phaseWidth
	result := database{
		width:       phaseWidth,
		phaseHeight: phaseHeight,
		low:         make([]byte, phasePixels*4),
		valid:       make([]byte, phasePixels*4),
	}
	for plane := range result.blocks {
		result.blocks[plane] = make([]byte, phasePixels*4)
	}
	parallel(4*phaseHeight, workers, func(begin, end int) {
		for combinedY := begin; combinedY < end; combinedY++ {
			phase := combinedY / phaseHeight
			lowY := combinedY % phaseHeight
			offsetY, offsetX := phase/2, phase%2
			sourceY := offsetY + lowY*2
			footprintTop := sourceY - 2*patchRadius
			rowValid := footprintTop >= 0 && footprintTop/cellSize == (sourceY+2*patchRadius+1)/cellSize
			databaseRow := combinedY * phaseWidth
			for lowX := 0; lowX < phaseWidth; lowX++ {
				sourceX := offsetX + lowX*2
				topLeft := sourceY*atlas.width + sourceX
				a := atlas.pix[topLeft]
				b := atlas.pix[topLeft+1]
				c := atlas.pix[topLeft+atlas.width]
				d := atlas.pix[topLeft+atlas.width+1]
				position := databaseRow + lowX
				result.blocks[0][position] = a
				result.blocks[1][position] = b
				result.blocks[2][position] = c
				result.blocks[3][position] = d
				top := alp[int(a)*paletteSize+int(b)]
				bottom := alp[int(c)*paletteSize+int(d)]
				result.low[position] = alp[int(top)*paletteSize+int(bottom)]
				footprintLeft := sourceX - 2*patchRadius
				if rowValid && footprintLeft >= 0 && footprintLeft/cellSize == (sourceX+2*patchRadius+1)/cellSize {
					result.valid[position] = 1
				}
			}
		}
	})
	return result
}

func prepareDatabaseCandidates(db *database, data dataset) {
	rareBright := rareBrightIndices(data)
	for position, parent := range db.low {
		if db.valid[position] == 0 {
			continue
		}
		for plane := range 4 {
			child := db.blocks[plane][position]
			if child != parent && rareBright[child] {
				db.valid[position] = 0
				break
			}
		}
	}
	indexDatabaseCandidates(db)
}

// rareBrightIndices marks palette entries that are both rare in the source
// map and bright: copying one of those into a block whose parent differs
// scatters isolated highlights, so such blocks are excluded as examples.
func rareBrightIndices(data dataset) [paletteSize]bool {
	var sourceCounts [paletteSize]int
	for _, index := range data.high {
		sourceCounts[index]++
	}
	var rareBright [paletteSize]bool
	for index := range paletteSize {
		red := float64(data.palette[index*3])
		green := float64(data.palette[index*3+1])
		blue := float64(data.palette[index*3+2])
		luminance := red*0.2126 + green*0.7152 + blue*0.0722
		rareBright[index] = sourceCounts[index] < 16 && luminance > 150
	}
	return rareBright
}

func indexDatabaseCandidates(db *database) {
	db.counts = [paletteSize]int{}
	for position, parent := range db.low {
		if db.valid[position] == 1 {
			db.counts[parent]++
		}
	}
	total := 0
	for index := range paletteSize {
		db.offsets[index] = total
		total += db.counts[index]
	}
	db.positions = make([]int32, total)
	cursors := db.offsets
	for position, parent := range db.low {
		if db.valid[position] == 0 {
			continue
		}
		db.positions[cursors[parent]] = int32(position)
		cursors[parent]++
	}
}

// supplementMissingParents appends examples for parent indices that occur in
// the map but have no valid example in the atlas database: blocks that only
// exist where two different tiles meet at a placement other than the first.
// The full map is scanned once at the four phase offsets and up to
// supplementLimit blocks per missing parent are appended as extra records that
// take part in random draws but not in propagation.
const supplementLimit = 8192

func supplementMissingParents(db *database, records *[]record, data dataset, contributions []float32,
	workers int) int {
	dimensions := featureDims
	var missing [paletteSize]bool
	any := false
	for _, index := range data.high {
		if db.counts[index] == 0 {
			missing[index] = true
			any = true
		}
	}
	if !any {
		return 0
	}
	rareBright := rareBrightIndices(data)
	width, height := data.metadata.Width, data.metadata.Height
	reduce := func(y, x int) byte {
		y = clamp(y, 0, height-2)
		x = clamp(x, 0, width-2)
		topLeft := y*width + x
		top := data.alp[int(data.high[topLeft])*paletteSize+int(data.high[topLeft+1])]
		bottom := data.alp[int(data.high[topLeft+width])*paletteSize+int(data.high[topLeft+width+1])]
		return data.alp[int(top)*paletteSize+int(bottom)]
	}
	type found struct{ y, x int }
	// Each worker scans a band of block rows at every phase and keeps at most
	// supplementLimit hits per parent; bands are merged in order so the result
	// is independent of scheduling.
	perWorker := make([][]found, workers)
	blockRows := height / 2
	parallelIndexed(blockRows, workers, func(worker, begin, end int) {
		var taken [paletteSize]int
		var hits []found
		for phase := range 4 {
			offsetY, offsetX := phase/2, phase%2
			for row := begin; row < end; row++ {
				y := offsetY + 2*row
				if y+1 >= height {
					continue
				}
				for x := offsetX; x+1 < width; x += 2 {
					parent := reduce(y, x)
					if !missing[parent] || taken[parent] >= supplementLimit {
						continue
					}
					topLeft := y*width + x
					valid := true
					for _, child := range [4]byte{data.high[topLeft], data.high[topLeft+1], data.high[topLeft+width], data.high[topLeft+width+1]} {
						if child != parent && rareBright[child] {
							valid = false
						}
					}
					if valid {
						hits = append(hits, found{y, x})
						taken[parent]++
					}
				}
			}
		}
		perWorker[worker] = hits
	})
	var taken [paletteSize]int
	added := 0
	for _, hits := range perWorker {
		for _, hit := range hits {
			y, x := hit.y, hit.x
			parent := reduce(y, x)
			if taken[parent] >= supplementLimit {
				continue
			}
			topLeft := y*width + x
			block := [4]byte{data.high[topLeft], data.high[topLeft+1], data.high[topLeft+width], data.high[topLeft+width+1]}
			var sums [8]float32
			patchPosition := 0
			for dy := -patchRadius; dy <= patchRadius; dy++ {
				for dx := -patchRadius; dx <= patchRadius; dx++ {
					index := int(reduce(y+2*dy, x+2*dx))
					contribution := (patchPosition*paletteSize + index) * dimensions
					for dimension := range dimensions {
						sums[dimension] += contributions[contribution+dimension]
					}
					patchPosition++
				}
			}
			var entry record
			for dimension := range 8 {
				entry.feat[dimension] = quantize(sums[dimension])
			}
			entry.key = int16(parent)
			entry.sum = blockSum(block, data.palette)
			*records = append(*records, entry)
			db.low = append(db.low, parent)
			db.valid = append(db.valid, 1)
			for plane := range 4 {
				db.blocks[plane] = append(db.blocks[plane], block[plane])
			}
			taken[parent]++
			added++
		}
	}
	if added > 0 {
		indexDatabaseCandidates(db)
	}
	return added
}

// makePCABasis fits the neighborhood basis to sampled 5x5 windows of the
// reduced examples. Each sample first draws a placement uniformly, so the
// basis reflects the map as laid out rather than its unique tile set: fitted
// over unique tiles the rare high-contrast graphics dominate and the retained
// dimensions stop discriminating the common smooth textures, which raised the
// share of high-contrast output blocks from 35% to 49% on Great Divide.
func makePCABasis(db database, atlas tileAtlas, palette []byte, samples, workers int) []float32 {
	positions := make([]int, samples)
	interior := sourceTileSize / 2 // block-aligned low positions per tile axis
	for sample := range samples {
		random := splitmix64(uint64(sample) + 0x8d58ac26afe12e47)
		tile := atlas.tileMap[random%uint64(len(atlas.tileMap))]
		random = splitmix64(random)
		phase := int(random & 3)
		localY := int((random >> 8) % uint64(interior))
		localX := int((random >> 32) % uint64(interior))
		cellY, cellX := (tile/atlas.columns)*cellSize, (tile%atlas.columns)*cellSize
		lowY := (cellY+haloSize-phase/2)/2 + localY
		lowX := (cellX+haloSize-phase%2)/2 + localX
		center := (phase*db.phaseHeight+lowY)*db.width + lowX
		positions[sample] = center - patchRadius*db.width - patchRadius
	}
	partials := make([][]float64, workers)
	parallelIndexed(samples, workers, func(worker, begin, end int) {
		local := make([]float64, patchValues*patchValues)
		var values [patchValues]float64
		for _, position := range positions[begin:end] {
			originY, originX := position/db.width, position%db.width
			component := 0
			for y := 0; y < patchSpan; y++ {
				row := (originY+y)*db.width + originX
				for x := 0; x < patchSpan; x++ {
					paletteIndex := int(db.low[row+x]) * 3
					values[component] = float64(palette[paletteIndex])
					values[component+1] = float64(palette[paletteIndex+1])
					values[component+2] = float64(palette[paletteIndex+2])
					component += 3
				}
			}
			for left, leftValue := range values {
				base := left * patchValues
				for right := 0; right <= left; right++ {
					local[base+right] += leftValue * values[right]
				}
			}
		}
		partials[worker] = local
	})
	covariance := make([]float64, patchValues*patchValues)
	for _, partial := range partials {
		for left := range patchValues {
			for right := 0; right <= left; right++ {
				covariance[left*patchValues+right] += partial[left*patchValues+right]
			}
		}
	}
	for left := range patchValues {
		for right := 0; right < left; right++ {
			covariance[right*patchValues+left] = covariance[left*patchValues+right]
		}
	}
	return dominantSubspace(covariance, featureDims)
}

func dominantSubspace(covariance []float64, dimensions int) []float32 {
	vectors := make([]float64, dimensions*patchValues)
	for dimension := range dimensions {
		for component := range patchValues {
			random := splitmix64(uint64(dimension*patchValues+component) + 0xb6c8e9cf570932bd)
			vectors[dimension*patchValues+component] = float64(int64(random>>11)&0xfffff)/524288 - 1
		}
	}
	orthonormalize(vectors, dimensions)
	next := make([]float64, len(vectors))
	for range 32 {
		for dimension := range dimensions {
			input := vectors[dimension*patchValues : (dimension+1)*patchValues]
			output := next[dimension*patchValues : (dimension+1)*patchValues]
			for row := range patchValues {
				sum := 0.0
				base := row * patchValues
				for column, value := range input {
					sum += covariance[base+column] * value
				}
				output[row] = sum
			}
		}
		orthonormalize(next, dimensions)
		vectors, next = next, vectors
	}
	result := make([]float32, len(vectors))
	for index, value := range vectors {
		result[index] = float32(value)
	}
	return result
}

func orthonormalize(vectors []float64, dimensions int) {
	for current := range dimensions {
		vector := vectors[current*patchValues : (current+1)*patchValues]
		for prior := 0; prior < current; prior++ {
			other := vectors[prior*patchValues : (prior+1)*patchValues]
			dot := 0.0
			for index, value := range vector {
				dot += value * other[index]
			}
			for index := range vector {
				vector[index] -= dot * other[index]
			}
		}
		norm := 0.0
		for _, value := range vector {
			norm += value * value
		}
		norm = math.Sqrt(norm)
		if norm < 1e-20 {
			clear(vector)
			vector[current%patchValues] = 1
			continue
		}
		for index := range vector {
			vector[index] /= norm
		}
	}
}

func makeContributions(basis []float32, palette []byte) []float32 {
	dimensions := featureDims
	result := make([]float32, patchSpan*patchSpan*paletteSize*dimensions)
	for patchPosition := range patchSpan * patchSpan {
		component := patchPosition * 3
		for paletteIndex := range paletteSize {
			red := float32(palette[paletteIndex*3])
			green := float32(palette[paletteIndex*3+1])
			blue := float32(palette[paletteIndex*3+2])
			output := (patchPosition*paletteSize + paletteIndex) * dimensions
			for dimension := range dimensions {
				weight := dimension * patchValues
				result[output+dimension] = red*basis[weight+component] +
					green*basis[weight+component+1] + blue*basis[weight+component+2]
			}
		}
	}
	return result
}

// quantize rounds a PCA coordinate to the nearest integer. The basis is
// orthonormal over 75 byte-valued components, so every coordinate lies within
// +-2209 and the rounding error is at most half a unit per dimension.
func quantize(value float32) int16 {
	return int16(math.Round(float64(value)))
}

const (
	invalidKey = int16(-1)
	infCost    = int32(math.MaxInt32)
	hashBins   = 16
	hashShift  = 8 // 256-wide bins over the +-2048 core of the coordinate range
	hashSpace  = paletteSize * hashBins * hashBins
)

func hashBucket(parent byte, feat *[8]int16) int {
	return int(parent)*hashBins*hashBins + hashBin(feat)
}

// hashBin is the parent-independent part of hashBucket.
func hashBin(feat *[8]int16) int {
	bin0 := clamp((int(feat[0])+2048)>>hashShift, 0, hashBins-1)
	bin1 := clamp((int(feat[1])+2048)>>hashShift, 0, hashBins-1)
	return bin0*hashBins + bin1
}

func blockSum(block [4]byte, palette []byte) [3]int16 {
	var sum [3]int16
	for _, index := range block {
		for channel := range 3 {
			sum[channel] += int16(palette[int(index)*3+channel])
		}
	}
	return sum
}

func databaseRecords(db database, palette []byte, contributions []float32, workers int) []record {
	dimensions := featureDims
	result := make([]record, len(db.low))
	parallel(len(db.low), workers, func(begin, end int) {
		var sums [8]float32
		for position := begin; position < end; position++ {
			clear(sums[:])
			y, x := position/db.width, position%db.width
			phase := y / db.phaseHeight
			localY := y % db.phaseHeight
			patchPosition := 0
			for dy := -patchRadius; dy <= patchRadius; dy++ {
				sampleY := clamp(localY+dy, 0, db.phaseHeight-1) + phase*db.phaseHeight
				for dx := -patchRadius; dx <= patchRadius; dx++ {
					sampleX := clamp(x+dx, 0, db.width-1)
					index := int(db.low[sampleY*db.width+sampleX])
					contribution := (patchPosition*paletteSize + index) * dimensions
					for dimension := range dimensions {
						sums[dimension] += contributions[contribution+dimension]
					}
					patchPosition++
				}
			}
			entry := &result[position]
			for dimension := range 8 {
				entry.feat[dimension] = quantize(sums[dimension])
			}
			entry.key = int16(db.low[position])
			if db.valid[position] == 0 {
				entry.key = invalidKey
			}
			entry.sum = blockSum([4]byte{db.blocks[0][position], db.blocks[1][position], db.blocks[2][position], db.blocks[3][position]}, palette)
		}
	})
	return result
}

// buildHashBuckets groups valid database positions by (parent, coarse bin of
// the first two PCA coordinates) so a "global" random draw can land near the
// query in feature space instead of anywhere in the parent's list.
func buildHashBuckets(db *database, records []record, workers int) {
	db.hashCounts = make([]int32, hashSpace)
	keys := make([]int32, len(records))
	parallel(len(records), workers, func(begin, end int) {
		for position := begin; position < end; position++ {
			entry := &records[position]
			if entry.key == invalidKey {
				keys[position] = -1
				continue
			}
			keys[position] = int32(hashBucket(byte(entry.key), &entry.feat))
		}
	})
	for _, key := range keys {
		if key >= 0 {
			db.hashCounts[key]++
		}
	}
	db.hashOffsets = make([]int32, hashSpace+1)
	for bucket := range hashSpace {
		db.hashOffsets[bucket+1] = db.hashOffsets[bucket] + db.hashCounts[bucket]
	}
	db.hashPositions = make([]int32, db.hashOffsets[hashSpace])
	cursors := make([]int32, hashSpace)
	copy(cursors, db.hashOffsets[:hashSpace])
	for position, key := range keys {
		if key < 0 {
			continue
		}
		db.hashPositions[cursors[key]] = int32(position)
		cursors[key]++
	}
}

func makeTileQueries(data dataset, atlas tileAtlas, contributions []float32, workers int) tileQueries {
	dimensions := featureDims
	mapWidth, mapHeight := atlas.mapWidth, atlas.mapHeight
	first, tileMap := atlas.firstPlacements, atlas.tileMap
	parents := make([]byte, len(first)*sourceTileSize*sourceTileSize)
	features := make([][8]int16, len(parents))
	parallel(len(first), workers, func(begin, end int) {
		var sums [8]float32
		for identifier := begin; identifier < end; identifier++ {
			placement := first[identifier]
			tileY, tileX := placement/mapWidth, placement%mapWidth
			originY, originX := tileY*sourceTileSize, tileX*sourceTileSize
			for y := range sourceTileSize {
				for x := range sourceTileSize {
					query := identifier*sourceTileSize*sourceTileSize + y*sourceTileSize + x
					parents[query] = data.high[(originY+y)*data.metadata.Width+originX+x]
					clear(sums[:])
					patchPosition := 0
					for dy := -patchRadius; dy <= patchRadius; dy++ {
						sampleY := clamp(originY+y+dy, 0, data.metadata.Height-1)
						for dx := -patchRadius; dx <= patchRadius; dx++ {
							sampleX := clamp(originX+x+dx, 0, data.metadata.Width-1)
							index := int(data.high[sampleY*data.metadata.Width+sampleX])
							contribution := (patchPosition*paletteSize + index) * dimensions
							for dimension := range dimensions {
								sums[dimension] += contributions[contribution+dimension]
							}
							patchPosition++
						}
					}
					for dimension := range 8 {
						features[query][dimension] = quantize(sums[dimension])
					}
				}
			}
		}
	})
	return tileQueries{
		parents: parents, features: features, firstPlacements: first, tileMap: tileMap,
		mapWidth: mapWidth, mapHeight: mapHeight,
	}
}

// patchMatch runs the classic sequential PatchMatch independently per unique
// tile: alternating raster scans propagate matches from the two already-visited
// neighbors, then each pixel tries a shrinking random window around its match
// plus two global draws. Every tile is self-contained, so the result does not
// depend on worker scheduling.
// patchMatch's tone term (options.tone > 0) penalizes the squared distance
// between a candidate block's RGB sum and four times its parent colour, so the
// 2x result averages back to the 1x map instead of inheriting the ALP blend's
// brightening and hue drift [03 §3.7]. Because for dark parents every authored
// block may be brighter than the parent, options.spread makes each candidate
// also offset a fraction of the mean error of its already-chosen 3x3
// neighbours, letting neighbouring pixels compensate for one another.
func patchMatch(queries tileQueries, db database, records []record, palette []byte, iterations int,
	options searchOptions, workers int) ([]int32, []int32, int) {
	toneWeight, spread, deadzone := options.tone, options.spread, options.deadzone
	var targets [paletteSize][3]int32
	for index := range paletteSize {
		for channel := range 3 {
			targets[index][channel] = 4 * int32(palette[index*3+channel])
		}
	}
	const tilePixels = sourceTileSize * sourceTileSize
	n := len(queries.parents)
	tileCount := n / tilePixels
	matches := make([]int32, n)
	costs := make([]int32, n)

	parallel(tileCount, workers, func(begin, end int) {
		var candidates [8]int32
		var buckets [tilePixels]int32
		var chosen [tilePixels][3]int32 // RGB-sum error of each pixel's current block
		var featCosts [tilePixels]int32
		for tile := begin; tile < end; tile++ {
			base := tile * tilePixels
			parents := queries.parents[base : base+tilePixels : base+tilePixels]
			features := queries.features[base : base+tilePixels : base+tilePixels]
			tileMatches := matches[base : base+tilePixels : base+tilePixels]
			rng := splitmix64(uint64(tile) + 0x8d58ac26afe12e47)
			next := func() uint64 {
				rng = splitmix64(rng)
				return rng
			}
			// globalDraw returns a random valid database position with the
			// query's parent, from the feature-hash bucket when asked and the
			// bucket is populated, and -1 when no position exists.
			globalDraw := func(pixel int, bucketed bool) int32 {
				parent := db.neighbours.draw(int(parents[pixel]), next())
				if parent < 0 {
					return -1
				}
				if bucketed {
					bucket := int(parent)*hashBins*hashBins + int(buckets[pixel])
					if candidate := db.bucketSampler.draw(bucket, next()); candidate >= 0 {
						return candidate
					}
				}
				return db.parentSampler.draw(int(parent), next())
			}
			// shifted returns the database position displaced by (dy, dx) from
			// a match, or -1 when it leaves the match's pyramid phase.
			shifted := func(match int32, dy, dx int) int32 {
				if match < 0 {
					return -1
				}
				y, x := int(match)/db.width, int(match)%db.width
				phaseBase := y - y%db.phaseHeight
				y += dy
				x += dx
				if y < phaseBase || y >= phaseBase+db.phaseHeight || x < 0 || x >= db.width || y*db.width+x >= len(records) {
					return -1
				}
				return int32(y*db.width + x)
			}
			// neighbourError sums the tone error of the up-to-eight chosen
			// neighbours inside the tile and scales it by spread/8 per neighbour.
			neighbourError := func(pixel int) [3]int32 {
				var total [3]int32
				if spread == 0 {
					return total
				}
				y, x := pixel/sourceTileSize, pixel%sourceTileSize
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						ny, nx := y+dy, x+dx
						if (dy == 0 && dx == 0) || ny < 0 || ny >= sourceTileSize || nx < 0 || nx >= sourceTileSize {
							continue
						}
						err := &chosen[ny*sourceTileSize+nx]
						total[0] += err[0]
						total[1] += err[1]
						total[2] += err[2]
					}
				}
				for channel := range 3 {
					total[channel] = total[channel] * spread / 64
				}
				return total
			}
			toneCost := func(err [3]int32, offset [3]int32) int32 {
				d0 := softenAbs(err[0]+offset[0], deadzone)
				d1 := softenAbs(err[1]+offset[1], deadzone)
				d2 := softenAbs(err[2]+offset[2], deadzone)
				return toneWeight * (d0*d0 + d1*d1 + d2*d2)
			}
			consider := func(pixel int, count int) {
				parent := parents[pixel]
				feat := &features[pixel]
				target := &targets[parent]
				offset := neighbourError(pixel)
				bestFeat, best := featCosts[pixel], tileMatches[pixel]
				bestErr := chosen[pixel]
				bestCost := bestFeat
				if bestFeat != infCost && toneWeight > 0 {
					bestCost += toneCost(bestErr, offset)
				}
				for _, candidate := range candidates[:count] {
					if candidate < 0 {
						continue
					}
					entry := &records[candidate]
					if entry.key < 0 || !db.allowed[parent][entry.key] {
						continue
					}
					feat := featureCost(feat, &entry.feat)
					cost := feat
					err := [3]int32{int32(entry.sum[0]) - target[0], int32(entry.sum[1]) - target[1], int32(entry.sum[2]) - target[2]}
					if toneWeight > 0 {
						cost += toneCost(err, offset)
					}
					if cost < bestCost {
						bestCost, bestFeat, best, bestErr = cost, feat, candidate, err
					}
				}
				featCosts[pixel], tileMatches[pixel], chosen[pixel] = bestFeat, best, bestErr
			}

			for pixel := range tilePixels {
				buckets[pixel] = int32(hashBin(&features[pixel]))
			}
			for pixel := range tilePixels {
				featCosts[pixel], tileMatches[pixel], chosen[pixel] = infCost, -1, [3]int32{}
				for slot := range 4 {
					candidates[slot] = globalDraw(pixel, slot%2 == 0)
				}
				consider(pixel, 4)
			}
			for iteration := range iterations {
				forward := iteration%2 == 0
				for scan := range tilePixels {
					pixel := scan
					step := 1
					if !forward {
						pixel = tilePixels - 1 - scan
						step = -1
					}
					y, x := pixel/sourceTileSize, pixel%sourceTileSize
					count := 0
					if forward && x > 0 || !forward && x < sourceTileSize-1 {
						candidates[count] = shifted(tileMatches[pixel-step], 0, step)
						count++
					}
					if forward && y > 0 || !forward && y < sourceTileSize-1 {
						candidates[count] = shifted(tileMatches[pixel-step*sourceTileSize], step, 0)
						count++
					}
					consider(pixel, count)
					match := tileMatches[pixel]
					count = 0
					for _, radius := range [...]int{8, 4, 2, 1} {
						random := next()
						span := uint64(2*radius + 1)
						dy := int(random%span) - radius
						dx := int((random>>32)%span) - radius
						candidates[count] = shifted(match, dy, dx)
						count++
					}
					candidates[count] = globalDraw(pixel, true)
					candidates[count+1] = globalDraw(pixel, false)
					consider(pixel, count+2)
				}
			}
			copy(costs[base:base+tilePixels], featCosts[:])
		}
	})
	mean, unmatched := costSummary(costs)
	fmt.Printf("iterations=%d mean-cost=%.2f unmatched=%d\n", iterations, mean, unmatched)
	return matches, costs, unmatched
}

// relaxParents lets a query with parent p also use blocks whose reduction is
// a palette entry k one ALP step away: blending p with k snaps to p or k, so no
// palette entry lies between them. The retail blend rounds to the nearest
// entry, so on a sparse palette the authored blocks under p can all be
// brighter than p [03 §3.7]; the tone term could then only reach p's colour
// with uniform blocks, which show as a 2x2 grid, while the adjacent entries'
// blocks carry the same texture at the right mean. The neighbourhood comes
// from the map's own ALP table, so it is tight on dense palettes and wide on
// sparse ones. A stand-in is only admitted when its blocks are on average
// closer to the parent's colour than the parent's own blocks, so relaxation
// can only reduce the bias, never add examples that are further off tone.
// Random draws pick a stand-in parent with probability proportional to its
// number of examples, as if the lists were merged.
func relaxParents(db *database, records []record, alp, palette []byte, relax bool) {
	var entries []int32
	var offsets, counts [paletteSize]int32
	var weights [paletteSize]uint32
	var meanSum [paletteSize][3]float64
	for key := range paletteSize {
		weights[key] = uint32(db.counts[key])
		for _, position := range db.positions[db.offsets[key] : db.offsets[key]+db.counts[key]] {
			for channel := range 3 {
				meanSum[key][channel] += float64(records[position].sum[channel])
			}
		}
		for channel := range 3 {
			meanSum[key][channel] /= float64(max(db.counts[key], 1))
		}
	}
	toneDistance := func(parent, key int) float64 {
		total := 0.0
		for channel := range 3 {
			d := meanSum[key][channel] - 4*float64(palette[3*parent+channel])
			total += d * d
		}
		return total
	}
	for parent := range paletteSize {
		offsets[parent] = int32(len(entries))
		for key := range paletteSize {
			if db.counts[key] == 0 {
				continue
			}
			blend := int(alp[parent*paletteSize+key])
			if key != parent && (!relax || blend != parent && blend != key ||
				toneDistance(parent, key) >= toneDistance(parent, parent)) {
				continue
			}
			db.allowed[parent][key] = true
			entries = append(entries, int32(key))
			counts[parent]++
		}
	}
	db.neighbours = makeSampler(entries, offsets[:], counts[:], weights[:])
}

// cycleConsistency is the fraction of matched pixels whose block reduces to
// exactly the query's parent, 1 when no relaxation was used.
func cycleConsistency(parents []byte, matches []int32, records []record) float64 {
	exact, total := 0, 0
	for pixel, match := range matches {
		if match < 0 {
			continue
		}
		total++
		if records[match].key == int16(parents[pixel]) {
			exact++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(exact) / float64(total)
}

// softenAbs returns |value| reduced by the dead zone, clamped at zero.
func softenAbs(value, deadzone int32) int32 {
	if value < 0 {
		value = -value
	}
	if value <= deadzone {
		return 0
	}
	return value - deadzone
}

func featureCost(a, b *[8]int16) int32 {
	d0 := int32(a[0]) - int32(b[0])
	d1 := int32(a[1]) - int32(b[1])
	d2 := int32(a[2]) - int32(b[2])
	d3 := int32(a[3]) - int32(b[3])
	d4 := int32(a[4]) - int32(b[4])
	d5 := int32(a[5]) - int32(b[5])
	d6 := int32(a[6]) - int32(b[6])
	d7 := int32(a[7]) - int32(b[7])
	return d0*d0 + d1*d1 + d2*d2 + d3*d3 + d4*d4 + d5*d5 + d6*d6 + d7*d7
}

func costSummary(costs []int32) (float64, int) {
	total := 0.0
	count, unmatched := 0, 0
	for _, cost := range costs {
		if cost == infCost {
			unmatched++
			continue
		}
		total += float64(cost)
		count++
	}
	return total / float64(max(count, 1)), unmatched
}

func assembleTiles(parents []byte, matches []int32, db database) []byte {
	tileCount := len(parents) / (sourceTileSize * sourceTileSize)
	result := make([]byte, tileCount*outputTileSize*outputTileSize)
	for tile := range tileCount {
		for y := range sourceTileSize {
			for x := range sourceTileSize {
				query := tile*sourceTileSize*sourceTileSize + y*sourceTileSize + x
				position := int(matches[query])
				block := [4]byte{parents[query], parents[query], parents[query], parents[query]}
				if position >= 0 {
					for plane := range 4 {
						block[plane] = db.blocks[plane][position]
					}
				}
				output := tile*outputTileSize*outputTileSize + y*2*outputTileSize + x*2
				result[output] = block[0]
				result[output+1] = block[1]
				result[output+outputTileSize] = block[2]
				result[output+outputTileSize+1] = block[3]
			}
		}
	}
	return result
}

func writeTileCache(prefix string, tiles []byte, queries tileQueries) error {
	if err := os.MkdirAll(filepath.Dir(prefix), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(prefix+"-tileset.bin", tiles, 0o644); err != nil {
		return fmt.Errorf("write indexed tiles: %w", err)
	}
	value := tileMapFile{
		TileSize: outputTileSize, Columns: atlasColumns,
		Tiles: len(queries.firstPlacements), Width: queries.mapWidth, Height: queries.mapHeight,
		Map: queries.tileMap,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode tile map: %w", err)
	}
	if err := os.WriteFile(prefix+"-tilemap.json", encoded, 0o644); err != nil {
		return fmt.Errorf("write tile map: %w", err)
	}
	return nil
}

func writePreviews(prefix string, tiles []byte, queries tileQueries, paletteRGB []byte, fullPreview bool) error {
	palette := make(color.Palette, paletteSize)
	for index := range paletteSize {
		palette[index] = color.RGBA{
			R: paletteRGB[index*3], G: paletteRGB[index*3+1], B: paletteRGB[index*3+2], A: 255,
		}
	}
	if err := writeTileAtlas(prefix+"-tileset.png", tiles, outputTileSize,
		len(queries.firstPlacements), palette); err != nil {
		return err
	}
	if err := writeTileAtlas(prefix+"-source-tileset.png", queries.parents, sourceTileSize,
		len(queries.firstPlacements), palette); err != nil {
		return err
	}
	if !fullPreview {
		return nil
	}

	fullWidth, fullHeight := queries.mapWidth*outputTileSize, queries.mapHeight*outputTileSize
	full := image.NewPaletted(image.Rect(0, 0, fullWidth, fullHeight), palette)
	for placement, tile := range queries.tileMap {
		tileY, tileX := placement/queries.mapWidth, placement%queries.mapWidth
		for row := range outputTileSize {
			source := tile*outputTileSize*outputTileSize + row*outputTileSize
			destination := (tileY*outputTileSize+row)*full.Stride + tileX*outputTileSize
			copy(full.Pix[destination:destination+outputTileSize], tiles[source:source+outputTileSize])
		}
	}
	return encodePNG(prefix+".png", full)
}

func writeTileAtlas(path string, tiles []byte, size, count int, palette color.Palette) error {
	rows := (count + atlasColumns - 1) / atlasColumns
	atlasWidth, atlasHeight := atlasColumns*size, rows*size
	atlas := image.NewPaletted(image.Rect(0, 0, atlasWidth, atlasHeight), palette)
	for tile := range count {
		atlasX := (tile % atlasColumns) * size
		atlasY := (tile / atlasColumns) * size
		for row := range size {
			source := tile*size*size + row*size
			destination := (atlasY+row)*atlas.Stride + atlasX
			copy(atlas.Pix[destination:destination+size], tiles[source:source+size])
		}
	}
	return encodePNG(path, atlas)
}

func encodePNG(path string, value image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	encodeErr := encoder.Encode(file, value)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode %s: %w", path, encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", path, closeErr)
	}
	return nil
}

func parallel(total, workers int, work func(begin, end int)) {
	parallelIndexed(total, workers, func(_ int, begin, end int) { work(begin, end) })
}

func parallelIndexed(total, workers int, work func(worker, begin, end int)) {
	workers = min(workers, max(total, 1))
	var group sync.WaitGroup
	group.Add(workers)
	for worker := range workers {
		begin := total * worker / workers
		end := total * (worker + 1) / workers
		go func() {
			defer group.Done()
			work(worker, begin, end)
		}()
	}
	group.Wait()
}

func splitmix64(value uint64) uint64 {
	value += 0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}
