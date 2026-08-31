package client

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/vfs"
)

// projectileGAFFixture is an authored in-memory bank: each named entry has a
// one-pixel raw frame whose index identifies the entry. It exercises the
// decoder/cache boundary without copying bytes from the retail installation.
func projectileGAFFixture(names []string) []byte {
	const entrySize = 40 + 8 + 24 + 1
	data := make([]byte, 12+4*len(names)+entrySize*len(names))
	binary.LittleEndian.PutUint32(data[0:4], 1)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(names)))
	base := 12 + 4*len(names)
	for i, name := range names {
		entryOffset := base + i*entrySize
		binary.LittleEndian.PutUint32(data[12+i*4:16+i*4], uint32(entryOffset))
		binary.LittleEndian.PutUint16(data[entryOffset:entryOffset+2], 1)
		copy(data[entryOffset+8:entryOffset+8+32], []byte(name))
		frameOffset := entryOffset + 48
		binary.LittleEndian.PutUint32(data[entryOffset+40:entryOffset+44], uint32(frameOffset))
		binary.LittleEndian.PutUint16(data[frameOffset:frameOffset+2], 1)
		binary.LittleEndian.PutUint16(data[frameOffset+2:frameOffset+4], 1)
		data[frameOffset+8] = 9 // authored key; index 9 remains transparent
		binary.LittleEndian.PutUint32(data[frameOffset+16:frameOffset+20], uint32(frameOffset+24))
		data[frameOffset+24] = byte(i + 1)
	}
	return data
}

func TestProjectileGAFResolvesOnlyEstablishedSharedEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "anims"), 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"shadow", "cannonshell", "plasmasm", "plasmamd", "ultrashell", "flamestream"}
	if err := os.WriteFile(filepath.Join(dir, "anims", "fx.gaf"), projectileGAFFixture(names), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	c := &Client{modelFS: fs}

	for _, family := range []int32{
		render.RenderTypeBaseSpriteModel,
		render.RenderTypeBaseModelDistinct,
		render.RenderTypeSelectorGAF,
		render.RenderTypeRecordOrientation,
	} {
		got, ok := c.resolveProjectileGAF(render.ProjectileGAFRequest{Family: family, Base: true})
		if !ok || got == nil || len(got.Pixels) != 1 || got.Pixels[0] != 1 {
			t.Fatalf("family %d shadow = %#v, ok=%v", family, got, ok)
		}
	}
	for selector, want := range []byte{2, 3, 4, 5, 3} {
		got, ok := c.resolveProjectileGAF(render.ProjectileGAFRequest{Family: render.RenderTypeSelectorGAF, Sequence: int32(selector)})
		if !ok || got == nil || got.Pixels[0] != want {
			t.Fatalf("selector %d = %#v, ok=%v, want pixel %d", selector, got, ok, want)
		}
	}
	got, ok := c.resolveProjectileGAF(render.ProjectileGAFRequest{Family: render.RenderTypeLifetimeGAF, Sequence: 0})
	if !ok || got == nil || got.Pixels[0] != 6 {
		t.Fatalf("lifetime sequence = %#v, ok=%v, want flamestream", got, ok)
	}
	for _, req := range []render.ProjectileGAFRequest{
		{Family: render.RenderTypeSelectorGAF, Sequence: -1},
		{Family: render.RenderTypeSelectorGAF, Sequence: 5},
		{Family: render.RenderTypeLifetimeGAF, Sequence: 1},
		{Family: render.RenderTypeGlobalGAF, Sequence: 0},
	} {
		if got, ok := c.resolveProjectileGAF(req); ok || got != nil {
			t.Fatalf("unestablished request resolved: req=%+v frame=%#v ok=%v", req, got, ok)
		}
	}
	// The published AssetID is not the route for these fixed engine slots;
	// an unrelated value cannot redirect an established sequence lookup.
	if got, ok := c.resolveProjectileGAF(render.ProjectileGAFRequest{Family: render.RenderTypeSelectorGAF, Sequence: 0, AssetID: "invented"}); !ok || got == nil {
		t.Fatal("fixed selector route incorrectly depended on AssetID")
	}
	// The raw decoder keeps the authored index byte; it is not palette-mapped
	// or replaced with a transparency sentinel at this boundary [fmt gaf].
	if got, ok := c.resolveProjectileGAF(render.ProjectileGAFRequest{Family: render.RenderTypeSelectorGAF, Sequence: 0}); !ok || got.Pixels[0] != 2 {
		t.Fatalf("cached raw indexed frame changed: %#v, ok=%v", got, ok)
	}
	if !c.projectileGAFLoaded || c.projectileGAF == nil {
		t.Fatal("shared projectile bank was not retained after lazy load")
	}
}
