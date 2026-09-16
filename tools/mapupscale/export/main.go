// Command export decodes one retail TNT map into the compact indexed dataset
// consumed by the PatchMatch terrain upscaler.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	retailpalette "github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/tools/mapupscale/internal/mapassets"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

const tileSize = 32

type metadata struct {
	Map         string `json:"map"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	TileSize    int    `json:"tile_size"`
	PixelFormat string `json:"pixel_format"`
	Reduction   string `json:"reduction"`
}

func main() {
	root := flag.String("root", mapassets.DefaultRoot(), "Total Annihilation asset root")
	mapName := flag.String("map", "Great Divide", "map name, with or without maps/ and .tnt")
	out := flag.String("out", "/tmp/great-divide-mapupscale", "output dataset directory")
	listMaps := flag.Bool("list-maps", false, "list available TNT maps and exit")
	flag.Parse()

	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(*root); err != nil {
		fatalf("mount asset root %s: %v", *root, err)
	}
	if *listMaps {
		for _, name := range availableMaps(fs) {
			fmt.Println(name)
		}
		return
	}
	if err := exportMap(fs, *mapName, *out); err != nil {
		fatalf("%v", err)
	}
}

func exportMap(fs *vfs.FS, requestedMap, outputDirectory string) error {
	logicalMap, err := mapassets.FindMap(fs, requestedMap)
	if err != nil {
		return err
	}
	tnt, err := formats.LoadTNTFile(fs, logicalMap)
	if err != nil {
		return fmt.Errorf("load %s: %w", logicalMap, err)
	}
	tables, err := retailpalette.Load(fs)
	if err != nil {
		return fmt.Errorf("load palette tables: %w", err)
	}

	width, height := int(tnt.TileMapWidth)*tileSize, int(tnt.TileMapHeight)*tileSize
	pixels := make([]byte, width*height)
	for tileY := 0; tileY < int(tnt.TileMapHeight); tileY++ {
		for tileX := 0; tileX < int(tnt.TileMapWidth); tileX++ {
			placement := tileY*int(tnt.TileMapWidth) + tileX
			graphic := int(tnt.TileIndices[placement])
			if graphic < 0 || graphic >= int(tnt.Tiles) {
				return fmt.Errorf("tile placement %d references graphic %d outside %d graphics", placement, graphic, tnt.Tiles)
			}
			source := tnt.TileGraphics[graphic*tileSize*tileSize : (graphic+1)*tileSize*tileSize]
			for row := 0; row < tileSize; row++ {
				destination := (tileY*tileSize+row)*width + tileX*tileSize
				copy(pixels[destination:destination+tileSize], source[row*tileSize:(row+1)*tileSize])
			}
		}
	}

	paletteRGB := make([]byte, 256*3)
	for index, entry := range tables.Base {
		copy(paletteRGB[index*3:index*3+3], entry[:3])
	}
	meta := metadata{
		Map:         logicalMap,
		Width:       width,
		Height:      height,
		TileSize:    tileSize,
		PixelFormat: "row-major uint8 palette indices",
		Reduction:   "ALP[ALP[p00,p01],ALP[p10,p11]]",
	}
	encodedMetadata, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}

	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	files := []struct {
		name string
		data []byte
	}{
		{"map-indexed.bin", pixels},
		{"palette-rgb.bin", paletteRGB},
		{"palette.alp", tables.Alpha[:]},
		{"metadata.json", append(encodedMetadata, '\n')},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(outputDirectory, file.name), file.data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", file.name, err)
		}
	}

	fmt.Printf("Exported %s: %dx%d indexed pixels -> %s\n", logicalMap, width, height, outputDirectory)
	return nil
}

func availableMaps(fs *vfs.FS) []string {
	seen := make(map[string]string)
	for _, entry := range fs.Entries() {
		logical := filepath.ToSlash(entry.Path)
		folded := strings.ToLower(logical)
		if entry.IsDir || !strings.HasPrefix(folded, "maps/") || !strings.HasSuffix(folded, ".tnt") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(folded, "maps/"), ".tnt")
		if _, exists := seen[key]; !exists {
			seen[key] = strings.TrimSuffix(strings.TrimPrefix(logical, "maps/"), filepath.Ext(logical))
		}
	}
	maps := make([]string, 0, len(seen))
	for _, name := range seen {
		maps = append(maps, name)
	}
	sort.Slice(maps, func(i, j int) bool { return strings.ToLower(maps[i]) < strings.ToLower(maps[j]) })
	return maps
}

func fatalf(format string, arguments ...any) {
	mapassets.Fatalf("nanolathe: map upscale export: ", format, arguments...)
}
