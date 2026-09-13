package world

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// A legacy TNT selects its own projectile gravity, but the bombing release
// leg still reads the mission's original OTA word [04 R-AIR-01 §8].
func TestLoadRetainsOTAGravitySeparatelyFromTerrain(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "maps", "legacy.tnt"), legacyTNTBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	for _, tc := range []struct {
		name   string
		parsed bool
		value  int32
		want   int32
	}{
		{name: "unparsed", want: -1},
		{name: "omitted or zero", parsed: true, want: 0},
		{name: "negative", parsed: true, value: -7, want: -7},
		{name: "authored", parsed: true, value: 8, want: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mh := &content.MapHeader{Gravity: tc.value}
			if tc.parsed {
				mh.RawOTA = &formats.OTA{Global: &formats.Section{}}
			}
			cat := &content.Catalog{Maps: map[string]*content.MapHeader{"legacy": mh}}
			terrain, err := Load(fs, cat, "legacy")
			if err != nil {
				t.Fatal(err)
			}
			if terrain.OTAGravity != tc.want || terrain.AuthoredGravity != 112 || terrain.Gravity != 112*65536/900 {
				t.Fatalf("OTA/terrain gravity = %d/%d/%d, want %d/112/converted112", terrain.OTAGravity, terrain.AuthoredGravity, terrain.Gravity, tc.want)
			}
		})
	}
}
