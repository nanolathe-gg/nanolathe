package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/vfs"
)

// featureSequenceFixture authors a two-entry GAF: a burn sequence whose three
// frames carry distinct geometry and distinct authored delays (3, 0, 2), and a
// one-frame death sequence. The zero delay is the interesting one: [05
// R-FEAT-01 §10] holds every frame for max(delay, 1) visits, so the burn entry
// lasts 3 + 1 + 2 = 6 visits, not 5.
func featureSequenceFixture(t *testing.T) *vfs.FS {
	t.Helper()
	pixels := func(w, h int) []byte {
		p := make([]byte, w*h)
		for i := range p {
			p[i] = 1
		}
		return p
	}
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{
		{Name: "treeburn", Frames: []formats.GAFWriteFrame{
			{Width: 20, Height: 12, XOffset: 7, YOffset: 5, Duration: 3, Pixels: pixels(20, 12)},
			{Width: 5, Height: 7, XOffset: 2, YOffset: 3, Duration: 0, Pixels: pixels(5, 7)},
			{Width: 16, Height: 24, XOffset: -3, YOffset: 9, Duration: 2, Pixels: pixels(16, 24)},
		}},
		{Name: "treedie", Frames: []formats.GAFWriteFrame{
			{Width: 8, Height: 8, XOffset: 1, YOffset: 1, Duration: 4, Pixels: pixels(8, 8)},
		}},
	})
	if err != nil {
		t.Fatalf("encode gaf fixture: %v", err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "anims"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "anims", "trees.gaf"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

func newFeatureSequenceClient(t *testing.T) *Client {
	t.Helper()
	return &Client{
		modelFS:       featureSequenceFixture(t),
		featureGAFs:   map[string]*formats.GAF{},
		featureGACErr: map[string]error{},
	}
}

// TestFeatureSequenceWalksTheCursorCadence locks the accessor against
// [05 R-FEAT-01 §10]: each frame holds for max(delay, 1) visits, the entry's
// lifetime is the sum of those holds, and a visit at or past the end reports
// the last frame — where the cursor sits on the visit that finishes it.
func TestFeatureSequenceWalksTheCursorCadence(t *testing.T) {
	c := newFeatureSequenceClient(t)
	type geom struct{ w, h, xoff, yoff int32 }
	first := geom{20, 12, 7, 5}
	second := geom{5, 7, 2, 3}
	third := geom{16, 24, -3, 9}
	for _, tc := range []struct {
		visit int32
		want  geom
	}{
		{0, first}, {1, first}, {2, first}, // delay 3
		{3, second},            // delay 0 holds one visit
		{4, third}, {5, third}, // delay 2
		{6, third}, {99, third}, // past the end: the last frame
		{-1, first}, // a negative visit clamps to the start
	} {
		w, h, xoff, yoff, visits, ok := c.FeatureSequence("trees", "treeburn", tc.visit)
		if !ok {
			t.Fatalf("visit %d did not resolve", tc.visit)
		}
		if (geom{w, h, xoff, yoff}) != tc.want {
			t.Fatalf("visit %d geometry %v, want %v", tc.visit, geom{w, h, xoff, yoff}, tc.want)
		}
		if visits != 6 {
			t.Fatalf("visit %d reported %d visits, want 3+1+2 [05 R-FEAT-01 §10]", tc.visit, visits)
		}
	}
}

// TestFeatureSequenceMissesAreMemoisedAndNeverLoadTwice locks the property
// that makes this cache safe to read from an authoritative phase: every answer,
// hit or miss, is resolved once and then served from the map, so a visit of the
// feature phase never reaches the filesystem.
func TestFeatureSequenceMissesAreMemoisedAndNeverLoadTwice(t *testing.T) {
	c := newFeatureSequenceClient(t)
	if _, _, _, _, _, ok := c.FeatureSequence("trees", "nosuchseq", 0); ok {
		t.Fatal("an absent sequence resolved")
	}
	if _, seen := c.featureSeqs[featureSequenceKey("trees", "nosuchseq")]; !seen {
		t.Fatal("the miss was not memoised, so the sim path would load again")
	}
	if _, _, _, _, _, ok := c.FeatureSequence("nosuchfile", "treeburn", 0); ok {
		t.Fatal("an absent file resolved")
	}
	if _, _, _, _, _, ok := c.FeatureSequence("", "", 0); ok {
		t.Fatal("an empty request resolved")
	}
	// Drop the loader: an already-compiled entry must still answer.
	if _, _, _, _, visits, ok := c.FeatureSequence("trees", "treedie", 0); !ok || visits != 4 {
		t.Fatalf("death sequence resolved=%v visits=%d, want 4", ok, visits)
	}
	c.modelFS = nil
	if _, _, _, _, visits, ok := c.FeatureSequence("trees", "treedie", 0); !ok || visits != 4 {
		t.Fatalf("cached death sequence resolved=%v visits=%d with no loader, want 4", ok, visits)
	}
}

// TestWarmFeatureSequencesCompilesTheCatalogOffTheSimPath locks the warm pass:
// after it runs, every sequence a definition names is already compiled, so the
// first visit of the feature phase is a map lookup.
func TestWarmFeatureSequencesCompilesTheCatalogOffTheSimPath(t *testing.T) {
	c := newFeatureSequenceClient(t)
	def := &content.FeatureDef{Filename: "trees", SeqNameBurn: "treeburn", SeqNameDie: "treedie", SeqNameDieShad: "missing-shadow"}
	def.CanonicalKey = "tree1"
	c.WarmFeatureSequences(map[string]*content.FeatureDef{"tree1": def})
	for _, seq := range []string{"treeburn", "treedie", "missing-shadow"} {
		if _, seen := c.featureSeqs[featureSequenceKey("trees", seq)]; !seen {
			t.Fatalf("warm pass left %q uncompiled", seq)
		}
	}
	c.modelFS = nil // the warm pass owns every load
	if _, _, _, _, visits, ok := c.FeatureSequence("trees", "treeburn", 0); !ok || visits != 6 {
		t.Fatalf("warmed burn sequence resolved=%v visits=%d, want 6", ok, visits)
	}
}
