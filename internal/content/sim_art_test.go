package content

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// simArtFixture authors two banks, with no retail bytes.
//
// `anims/trees.gaf` holds a burn sequence whose three frames carry distinct
// geometry and distinct authored delays (3, 0, 2) and a one-frame death
// sequence. The zero delay is the interesting one: [05 R-FEAT-01 §10] holds
// every frame for max(delay, 1) visits, so the burn entry lasts 3 + 1 + 2 = 6
// visits, not 5.
//
// `anims/fx.gaf` is the default effect bank, with a five-frame smoke entry and
// a three-frame flame entry. The fixture and the expected numbers below are
// deliberately the same shape the presentation cache's own cadence test uses,
// because the two readings of one entry must never come apart.
func simArtFixture(t *testing.T) *vfs.FS {
	t.Helper()
	pixels := func(w, h int) []byte {
		p := make([]byte, w*h)
		for i := range p {
			p[i] = 1
		}
		return p
	}
	frame := func(w, h int, xoff, yoff int16, dur uint32) formats.GAFWriteFrame {
		return formats.GAFWriteFrame{Width: uint16(w), Height: uint16(h), XOffset: xoff, YOffset: yoff, Duration: dur, Pixels: pixels(w, h)}
	}
	trees, err := formats.EncodeGAF([]formats.GAFWriteEntry{
		{Name: "treeburn", Frames: []formats.GAFWriteFrame{
			frame(20, 12, 7, 5, 3),
			frame(5, 7, 2, 3, 0),
			frame(16, 24, -3, 9, 2),
		}},
		{Name: "treedie", Frames: []formats.GAFWriteFrame{frame(8, 8, 1, 1, 4)}},
		{Name: "treedieshad", Frames: []formats.GAFWriteFrame{frame(8, 8, 1, 1, 4)}},
	})
	if err != nil {
		t.Fatalf("encode feature gaf fixture: %v", err)
	}
	fx, err := formats.EncodeGAF([]formats.GAFWriteEntry{
		{Name: "smoke 1", Frames: []formats.GAFWriteFrame{
			frame(4, 4, 0, 0, 2), frame(4, 4, 0, 0, 2), frame(4, 4, 0, 0, 2),
			frame(4, 4, 0, 0, 2), frame(4, 4, 0, 0, 2),
		}},
		{Name: "flamestream", Frames: []formats.GAFWriteFrame{
			frame(3, 3, 0, 0, 1), frame(3, 3, 0, 0, 1), frame(3, 3, 0, 0, 1),
		}},
	})
	if err != nil {
		t.Fatalf("encode effect gaf fixture: %v", err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "anims"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "anims", "trees.gaf"), trees, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "anims", "fx.gaf"), fx, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

func simArtFixtureCatalog() *Catalog {
	return &Catalog{Features: map[string]*FeatureDef{
		"tree1": {Filename: "trees", SeqName: "tree1", SeqNameBurn: "treeburn", SeqNameDie: "treedie", SeqNameDieShad: "treedieshad"},
		// A definition whose file does not exist must compile to a miss, not
		// to a load attempt at simulation time.
		"ghost": {Filename: "nosuchfile", SeqNameDie: "ghostdie"},
	}}
}

// TestSimArtWalksTheCursorCadence locks the table against [05 R-FEAT-01 §10]:
// each frame holds for max(delay, 1) visits, the entry's lifetime is the sum of
// those holds, and a visit at or past the end reports the last frame — where
// the cursor sits on the visit that finishes it.
func TestSimArtWalksTheCursorCadence(t *testing.T) {
	art := CompileSimArt(simArtFixture(t), simArtFixtureCatalog())
	type geom struct{ w, h, xoff, yoff int32 }
	first := geom{20, 12, 7, 5}
	second := geom{5, 7, 2, 3}
	third := geom{16, 24, -3, 9}
	for _, tc := range []struct {
		visit int32
		want  geom
	}{
		{-1, first}, {0, first}, {1, first}, {2, first},
		{3, second},
		{4, third}, {5, third},
		// Past the end: the cursor sits on the frame that finishes the record.
		{6, third}, {99, third},
	} {
		w, h, xoff, yoff, visits, ok := art.FeatureSequence("trees", "treeburn", tc.visit)
		if !ok {
			t.Fatalf("visit %d: burn sequence did not resolve", tc.visit)
		}
		if got := (geom{w, h, xoff, yoff}); got != tc.want {
			t.Fatalf("visit %d geometry %+v, want %+v", tc.visit, got, tc.want)
		}
		if visits != 6 {
			t.Fatalf("visit %d lifetime %d, want 3+1+2 = 6", tc.visit, visits)
		}
	}
	if _, _, _, _, visits, ok := art.FeatureSequence("treeS", "TreeDie", 0); !ok || visits != 4 {
		t.Fatalf("death lifetime %d ok=%v, want 4 and a case-insensitive hit", visits, ok)
	}
}

// TestSimArtReportsUnknownRatherThanAFallback locks rule 1 at the seam: a file,
// a sequence or an entry that does not resolve reports ok=false, so the
// consumers keep their documented "unknown" behaviour instead of receiving an
// invented lifetime [I9].
func TestSimArtReportsUnknownRatherThanAFallback(t *testing.T) {
	art := CompileSimArt(simArtFixture(t), simArtFixtureCatalog())
	for _, tc := range []struct{ file, seq string }{
		{"trees", "nosuchseq"},
		{"nosuchfile", "ghostdie"},
		{"trees", "treereclamate"}, // authored by no definition, so not compiled
		{"", ""},
		{"trees", ""},
	} {
		if _, _, _, _, _, ok := art.FeatureSequence(tc.file, tc.seq, 0); ok {
			t.Fatalf("%q|%q resolved, want unknown", tc.file, tc.seq)
		}
	}
	var nilArt *SimArt
	if _, _, _, _, _, ok := nilArt.FeatureSequence("trees", "treeburn", 0); ok {
		t.Fatal("a nil table resolved a sequence")
	}
	if _, ok := nilArt.EffectEntryFrameCount("", "smoke 1"); ok {
		t.Fatal("a nil table resolved an effect entry")
	}
}

func TestSimArtCompilesEventShadowsWithoutGivingThemCursorTiming(t *testing.T) {
	art := CompileSimArt(simArtFixture(t), simArtFixtureCatalog())
	if _, _, _, _, _, ok := art.FeatureSequence("trees", "treedieshad", 0); !ok {
		t.Fatal("authored event shadow did not resolve")
	}
	if _, _, _, _, _, ok := art.FeatureSequence("trees", "missing-shadow", 0); ok {
		t.Fatal("missing event shadow resolved")
	}
}

// TestSimArtSuppressesAValidSequenceWhenAnotherEntryIsCorrupt locks the
// whole-bank policy at the content boundary. Metadata has no pixel planes, but
// it still validates every frame payload, so it cannot let one requested entry
// silently bypass an unrelated malformed raw frame [fmt gaf].
func TestSimArtSuppressesAValidSequenceWhenAnotherEntryIsCorrupt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		unrelated formats.GAFWriteFrame
		corrupt   func([]byte, uint32)
	}{
		{
			name:      "raw",
			unrelated: formats.GAFWriteFrame{Width: 1, Height: 1, Pixels: []byte{3}},
			corrupt: func(bank []byte, frameOffset uint32) {
				binary.LittleEndian.PutUint32(bank[frameOffset+16:frameOffset+20], uint32(len(bank)))
			},
		},
		{
			name:      "rle",
			unrelated: formats.GAFWriteFrame{Width: 1, Height: 1, Pixels: []byte{3}, Transparent: []bool{true}},
			corrupt: func(bank []byte, frameOffset uint32) {
				dataOffset := binary.LittleEndian.Uint32(bank[frameOffset+16 : frameOffset+20])
				bank[dataOffset+2] = 1 // a zero-length RLE skip run
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bank, err := formats.EncodeGAF([]formats.GAFWriteEntry{
				{Name: "treeburn", Frames: []formats.GAFWriteFrame{{Width: 2, Height: 1, XOffset: 3, YOffset: -4, Duration: 7, Pixels: []byte{1, 2}}}},
				{Name: "unrelated", Frames: []formats.GAFWriteFrame{tc.unrelated}},
			})
			if err != nil {
				t.Fatal(err)
			}
			entryOffset := binary.LittleEndian.Uint32(bank[16:20])
			frameOffset := binary.LittleEndian.Uint32(bank[entryOffset+40 : entryOffset+44])
			tc.corrupt(bank, frameOffset)
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "anims"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "anims", "trees.gaf"), bank, 0o644); err != nil {
				t.Fatal(err)
			}
			fs := vfs.New()
			if err := fs.MountDirectory(dir, 1); err != nil {
				t.Fatal(err)
			}
			defer fs.Close()
			art := CompileSimArt(fs, &Catalog{Features: map[string]*FeatureDef{
				"tree": {Filename: "trees", SeqNameBurn: "treeburn"},
			}})
			if _, _, _, _, _, ok := art.FeatureSequence("trees", "treeburn", 0); ok {
				t.Fatal("valid sequence survived an unrelated corrupt entry")
			}
		})
	}
}

// TestSimArtEffectEntryFrameCount locks the length the strip families draw a
// puff's own last frame against [03 R-STRIP-01 §2][06 R-WFX-01 §5], and the
// default bank an entry published without one names [06 R-WFX-01 §1].
func TestSimArtEffectEntryFrameCount(t *testing.T) {
	art := CompileSimArt(simArtFixture(t), simArtFixtureCatalog())
	for _, tc := range []struct {
		bank, entry string
		want        int
	}{
		{"", "smoke 1", 5},
		{"fx", "smoke 1", 5},
		{"FX", "SMOKE 1", 5},
		{"", "flamestream", 3},
	} {
		n, ok := art.EffectEntryFrameCount(tc.bank, tc.entry)
		if !ok || n != tc.want {
			t.Fatalf("%q|%q frame count %d ok=%v, want %d", tc.bank, tc.entry, n, ok, tc.want)
		}
	}
	for _, tc := range []struct{ bank, entry string }{
		{"", ""},
		{"", "smoke 2"},   // absent from this fixture bank
		{"other", "fire"}, // a bank the simulation never names, so never compiled
	} {
		if _, ok := art.EffectEntryFrameCount(tc.bank, tc.entry); ok {
			t.Fatalf("%q|%q resolved, want unknown", tc.bank, tc.entry)
		}
	}
}

// TestSimArtRetainsNoFilesystem locks the property that makes the table safe to
// read from an authoritative phase: compilation happens up front and nothing
// afterwards can turn a miss into a load (I4). Unmounting the fixture must not
// change a single answer.
func TestSimArtRetainsNoFilesystem(t *testing.T) {
	fs := simArtFixture(t)
	art := CompileSimArt(fs, simArtFixtureCatalog())
	fs.Close()
	if _, _, _, _, visits, ok := art.FeatureSequence("trees", "treeburn", 0); !ok || visits != 6 {
		t.Fatalf("burn lifetime %d ok=%v after unmount, want 6", visits, ok)
	}
	if n, ok := art.EffectEntryFrameCount("", "smoke 1"); !ok || n != 5 {
		t.Fatalf("smoke frame count %d ok=%v after unmount, want 5", n, ok)
	}
	if _, _, _, _, _, ok := art.FeatureSequence("trees", "nosuchseq", 0); ok {
		t.Fatal("a miss resolved after unmount")
	}
}
