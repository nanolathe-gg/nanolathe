//go:build retail

package formats

import (
	"path"
	"strings"
	"testing"
)

// TestRetailGAFMetadataMatchesPixelLoader checks the complete installed GAF
// corpus. It is tagged because full presentation decoding is intentionally
// expensive; every accepted metadata index must agree with the full loader on
// the facts that cross the immutable content boundary [fmt gaf].
func TestRetailGAFMetadataMatchesPixelLoader(t *testing.T) {
	fs := retailFS(t)
	defer fs.Close()
	seen := 0
	for _, dir := range []string{"anims", "textures"} {
		entries, err := fs.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, info := range entries {
			if info.IsDir || !strings.EqualFold(path.Ext(info.Path), ".gaf") {
				continue
			}
			seen++
			logical := info.Path
			meta, err := LoadGAFMetadataFile(fs, logical)
			if err != nil {
				t.Fatalf("metadata %s: %v", logical, err)
			}
			full, err := LoadGAFFile(fs, logical)
			if err != nil {
				t.Fatalf("full %s: %v", logical, err)
			}
			assertGAFMetadataMatches(t, meta, full)
		}
	}
	if seen == 0 {
		t.Fatal("reference install exposed no GAF files")
	}
}
