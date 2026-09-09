package upscale

// Assembling the synthesized tiles, plus the small helpers the terrain and
// sprite synthesizers share. See tools/mapupscale/patchmatchgo/README.md.

import (
	"sync"
)

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

// authoredAdjacentDistance is the mean squared RGB distance between
// horizontally adjacent pixels of the authored map, the natural dead zone for
// the seam term: pairs at least this different are what the map already
// contains.
func authoredAdjacentDistance(data terrainData) int32 {
	width, height := data.width, data.height
	var total, count int64
	for y := 0; y < height; y += 4 {
		row := data.high[y*width : (y+1)*width]
		for x := 0; x+1 < width; x++ {
			a, b := int(row[x])*3, int(row[x+1])*3
			for channel := range 3 {
				d := int64(data.palette[a+channel]) - int64(data.palette[b+channel])
				total += d * d
			}
			count++
		}
	}
	return int32(total / max(count, 1))
}
