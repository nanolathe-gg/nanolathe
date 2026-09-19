package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func authoredTNTWithMinimap() []byte {
	data := authoredTNTWithoutMinimap(false)
	offset := len(data)
	data = append(data, make([]byte, 8+4*4)...)
	binary.LittleEndian.PutUint32(data[0x28:], uint32(offset))
	binary.LittleEndian.PutUint32(data[0x2c:], 1)
	binary.LittleEndian.PutUint32(data[offset:], 4)
	binary.LittleEndian.PutUint32(data[offset+4:], 4)
	for i := offset + 8; i < len(data); i++ {
		data[i] = 91 // distinct from the terrain tiles' palette index 37
	}
	return data
}

func TestBattleRadarUsesCatalogReadCap(t *testing.T) {
	data := authoredTNTWithMinimap()
	fs := absentMinimapFS{data: data}
	terrain, err := world.Load(fs, nil, "no-mini")
	if err != nil {
		t.Fatal(err)
	}
	pal := &palette.Tables{}
	for i := range pal.Alpha {
		pal.Alpha[i] = byte(i / 256)
	}
	cat := &content.Catalog{Maps: map[string]*content.MapHeader{"no-mini": {LogicalTNT: "maps/no-mini.tnt"}}}
	for _, tc := range []struct {
		cap   int64
		pixel byte
	}{{int64(len(data)), 91}, {int64(len(data) - 1), 37}, {0, 91}} {
		cat.Limits.TNTBytes = tc.cap
		radar := buildBattleRadar(fs, cat, "no-mini", terrain, pal)
		if radar == nil || len(radar.Bits) == 0 {
			t.Fatalf("cap %d produced no radar", tc.cap)
		}
		for _, pixel := range radar.Bits {
			if pixel != tc.pixel {
				t.Fatalf("cap %d: pixel %d, want %d (authored versus generated picture)", tc.cap, pixel, tc.pixel)
			}
		}
	}
}

func TestMenuPreviewUsesProfileViewAndReadCap(t *testing.T) {
	data := authoredTNTWithMinimap()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "modmaps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modmaps", "no-mini.tnt"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountDirectory(dir, 0); err != nil {
		t.Fatal(err)
	}
	view := vfs.NewLayout(map[string]string{"maps": "modmaps"}).Apply(fs)
	for _, cap := range []int64{int64(len(data) - 1), int64(len(data)), 0} {
		g := &gameShell{cs: &contentSet{fs: view, unmappedMount: fs, limits: content.Limits{TNTBytes: cap}}}
		preview := g.mapDataFor("no-mini")
		wantPresent := cap == 0 || cap == int64(len(data))
		if preview == nil || (preview.tnt != nil) != wantPresent {
			t.Fatalf("cap %d: preview %+v, want TNT present %v", cap, preview, wantPresent)
		}
		if preview.tnt != nil && preview.tnt.Minimap[0] != 91 {
			t.Fatal("menu preview lost the authored minimap")
		}
	}
}
