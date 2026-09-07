package main

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// absentMinimapFS supplies one authored TNT to the production terrain loader.
// The boundary under test only needs ReadFileLimit; the remaining FSOps methods
// make that production interface explicit.
type absentMinimapFS struct{ data []byte }

func (f absentMinimapFS) Open(string) (vfs.File, error) {
	return nil, fmt.Errorf("fixture: open unused")
}
func (f absentMinimapFS) ReadFileLimit(name string, max int64) ([]byte, error) {
	if name != "maps/no-mini.tnt" || int64(len(f.data)) > max {
		return nil, fmt.Errorf("fixture: %s unavailable", name)
	}
	return f.data, nil
}
func (absentMinimapFS) ReadDir(string) ([]vfs.EntryInfo, error) {
	return nil, fmt.Errorf("fixture: readdir unused")
}
func (absentMinimapFS) Stat(string) (vfs.EntryInfo, error) {
	return vfs.EntryInfo{}, fmt.Errorf("fixture: stat unused")
}
func (absentMinimapFS) CacheStamp(string) (string, error) {
	return "", fmt.Errorf("fixture: stamp unused")
}

func authoredTNTWithoutMinimap(present bool) []byte {
	const (
		cellW   = 10
		cellH   = 10
		tileMap = 0x40
		attrs   = tileMap + cellW/2*cellH/2*2
		tiles   = attrs + cellW*cellH*4
	)
	data := make([]byte, tiles+1024)
	put := func(off int, value uint32) { binary.LittleEndian.PutUint32(data[off:], value) }
	put(0x00, uint32(world.VersionCanonical))
	put(0x04, cellW)
	put(0x08, cellH)
	put(0x0c, tileMap)
	put(0x10, attrs)
	put(0x14, tiles)
	put(0x18, 1)
	put(0x1c, 0)
	put(0x28, ^uint32(0)) // deliberately unusable when the flag is clear
	if present {
		put(0x2c, 1)
	}
	for i := 0; i < cellW*cellH; i++ {
		binary.LittleEndian.PutUint16(data[attrs+i*4+1:], 0xffff)
	}
	for i := tiles; i < len(data); i++ {
		data[i] = 37
	}
	return data
}

func TestAbsentTNTMinimapLoadsTerrainAndBuildsGeneratedRadar(t *testing.T) {
	terrain, err := world.Load(absentMinimapFS{data: authoredTNTWithoutMinimap(false)}, nil, "no-mini")
	if err != nil {
		t.Fatalf("flag-clear terrain load: %v", err)
	}
	if len(terrain.TileSet) != 1 || terrain.TileSet[0][0] != 37 {
		t.Fatalf("terrain tile source was not retained: %#v", terrain.TileSet)
	}
	tables := &palette.Tables{}
	for i := range tables.Alpha {
		tables.Alpha[i] = byte(i / 256)
	}
	radar := buildBattleRadar(nil, nil, "no-mini", terrain, tables)
	if radar == nil || len(radar.Bits) == 0 {
		t.Fatal("absent TNT minimap did not produce the generated radar picture")
	}
	for _, pixel := range radar.Bits {
		if pixel != 37 {
			t.Fatalf("generated radar pixel = %d, want tile-derived 37", pixel)
		}
	}
}

func TestPresentMalformedTNTMinimapFailsBeforeRadarFallback(t *testing.T) {
	if _, err := world.Load(absentMinimapFS{data: authoredTNTWithoutMinimap(true)}, nil, "no-mini"); err == nil {
		t.Fatal("flag-set TNT with unusable minimap pointer was accepted")
	}
}
