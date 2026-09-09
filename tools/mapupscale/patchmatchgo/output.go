package main

// Writing the tile cache, the previews and the atlas PNGs. The synthesis
// itself is internal/upscale; everything here is inspection output.

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/internal/upscale"
)

func writeTileCache(prefix string, result upscale.TerrainResult) error {
	if err := os.MkdirAll(filepath.Dir(prefix), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(prefix+"-tileset.bin", result.Tiles, 0o644); err != nil {
		return fmt.Errorf("write indexed tiles: %w", err)
	}
	value := tileMapFile{
		TileSize: result.OutputSize, Columns: result.Columns,
		Tiles: result.UniqueTiles, Width: result.MapWidth, Height: result.MapHeight,
		Map: result.TileMap,
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

func writePreviews(prefix string, result upscale.TerrainResult, paletteRGB []byte, fullPreview bool) error {
	palette := make(color.Palette, paletteSize)
	for index := range paletteSize {
		palette[index] = color.RGBA{
			R: paletteRGB[index*3], G: paletteRGB[index*3+1], B: paletteRGB[index*3+2], A: 255,
		}
	}
	if err := writeTileAtlas(prefix+"-tileset.png", result.Tiles, result.OutputSize,
		result.UniqueTiles, result.Columns, palette); err != nil {
		return err
	}
	if err := writeTileAtlas(prefix+"-source-tileset.png", result.Sources, result.SourceSize,
		result.UniqueTiles, result.Columns, palette); err != nil {
		return err
	}
	if !fullPreview {
		return nil
	}

	size := result.OutputSize
	fullWidth, fullHeight := result.MapWidth*size, result.MapHeight*size
	full := image.NewPaletted(image.Rect(0, 0, fullWidth, fullHeight), palette)
	for placement, tile := range result.TileMap {
		tileY, tileX := placement/result.MapWidth, placement%result.MapWidth
		for row := range size {
			source := tile*size*size + row*size
			destination := (tileY*size+row)*full.Stride + tileX*size
			copy(full.Pix[destination:destination+size], result.Tiles[source:source+size])
		}
	}
	return encodePNG(prefix+".png", full)
}

func writeTileAtlas(path string, tiles []byte, size, count, columns int, palette color.Palette) error {
	rows := (count + columns - 1) / columns
	atlasWidth, atlasHeight := columns*size, rows*size
	atlas := image.NewPaletted(image.Rect(0, 0, atlasWidth, atlasHeight), palette)
	for tile := range count {
		atlasX := (tile % columns) * size
		atlasY := (tile / columns) * size
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
