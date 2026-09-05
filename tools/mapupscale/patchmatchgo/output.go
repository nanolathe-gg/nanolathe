package main

// Assembling the synthesized tiles and writing the cache, the previews and
// the atlas PNG. See README.md.

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

// authoredAdjacentDistance is the mean squared RGB distance between
// horizontally adjacent pixels of the authored map, the natural dead zone for
// the seam term: pairs at least this different are what the map already
// contains.
func authoredAdjacentDistance(data dataset) int32 {
	width, height := data.metadata.Width, data.metadata.Height
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
