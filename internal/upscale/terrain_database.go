package upscale

// The example database: the parent/child tile records built from the map's
// own authored pixels, their quantized feature vectors, the weighted sampler
// and the hash buckets the search probes. See README.md.

import (
	"math"
)

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
	// sole[p] is the only admitted key for parent p when there is exactly
	// one (no relaxation stand-ins), so random draws skip the alias table;
	// -1 otherwise.
	sole [paletteSize]int16
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

// record packs everything a candidate test needs into one 16-byte unit so
// one random memory access decides a candidate: the quantized PCA features,
// the ALP parent index (or -1 when the position is excluded) and the four
// authored pixel indices of the block. The block's RGB sum is derived from a
// palette table in L1 rather than stored. Features are int8: the first PCA
// coordinate (mean brightness, up to +-2048) is stored in units of 16, the
// others (rarely beyond +-500) in units of 4, and the distance weights the
// dimensions back so costs stay in the int16 units the other terms were
// calibrated against.
type record struct {
	feat  [featureDims]int8
	key   int16
	block [4]byte
	_     uint16
}

// quantizeFeatures maps PCA coordinates to the record's int8 units.
func quantizeFeatures(sums *[featureDims]float32) [featureDims]int8 {
	var out [featureDims]int8
	for dimension := range featureDims {
		scale := float32(featureScaleRest)
		if dimension == 0 {
			scale = featureScaleFirst
		}
		out[dimension] = int8(clamp(int(math.Round(float64(sums[dimension]/scale))), -127, 127))
	}
	return out
}

func makeTileAtlas(data terrainData) tileAtlas {
	mapWidth := data.width / sourceTileSize
	mapHeight := data.height / sourceTileSize
	first := make([]int, 0, mapWidth*mapHeight)
	tileMap := make([]int, mapWidth*mapHeight)
	seen := make(map[string]int)
	var tile [sourceTileSize * sourceTileSize]byte
	for placement := range tileMap {
		tileY, tileX := placement/mapWidth, placement%mapWidth
		for row := range sourceTileSize {
			source := (tileY*sourceTileSize+row)*data.width + tileX*sourceTileSize
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
			sourceY := clamp(tileY*sourceTileSize+y-haloSize, 0, data.height-1)
			for x := range cellSize {
				sourceX := clamp(tileX*sourceTileSize+x-haloSize, 0, data.width-1)
				atlas.pix[(cellY+y)*atlas.width+cellX+x] = data.high[sourceY*data.width+sourceX]
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

func prepareDatabaseCandidates(db *database, data terrainData) {
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
func rareBrightIndices(data terrainData) [paletteSize]bool {
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

func supplementMissingParents(db *database, records *[]record, data terrainData, contributions []float32,
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
	width, height := data.width, data.height
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
			entry := record{feat: quantizeFeatures(&sums), key: int16(parent), block: block}
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

const (
	invalidKey = int16(-1)
	infCost    = int32(math.MaxInt32)
	hashBins   = 16
	hashCells  = hashBins * hashBins * hashBins
	hashSpace  = paletteSize * hashCells
)

func hashBucket(parent byte, feat *[featureDims]int8) int {
	return int(parent)*hashCells + hashBin(feat)
}

// hashBin is the parent-independent part of hashBucket: 16 bins over each of
// the first three quantized PCA coordinates (the int8 range, so 256 native
// units on the first coordinate and 64 on the other two).
func hashBin(feat *[featureDims]int8) int {
	bin0 := (int(feat[0]) + 128) >> 4
	bin1 := (int(feat[1]) + 128) >> 4
	bin2 := (int(feat[2]) + 128) >> 4
	return (bin0*hashBins+bin1)*hashBins + bin2
}

// blockSum is the RGB sum of a block's four pixels, 0..1020 per channel.
func blockSum(block *[4]byte, sums *[paletteSize][3]int32) [3]int32 {
	a, b, c, d := &sums[block[0]], &sums[block[1]], &sums[block[2]], &sums[block[3]]
	return [3]int32{a[0] + b[0] + c[0] + d[0], a[1] + b[1] + c[1] + d[1], a[2] + b[2] + c[2] + d[2]}
}

func paletteSums(palette []byte) [paletteSize][3]int32 {
	var sums [paletteSize][3]int32
	for index := range paletteSize {
		for channel := range 3 {
			sums[index][channel] = int32(palette[index*3+channel])
		}
	}
	return sums
}

func databaseRecords(db database, contributions []float32, workers int) []record {
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
			entry.feat = quantizeFeatures(&sums)
			entry.key = int16(db.low[position])
			if db.valid[position] == 0 {
				entry.key = invalidKey
			}
			entry.block = [4]byte{db.blocks[0][position], db.blocks[1][position], db.blocks[2][position], db.blocks[3][position]}
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
