package formats

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Use the shipping file reader so ownership and payload validation are tested
// at the same boundary as client effect loading.
func testGAFSource(t *testing.T, data []byte, limits GAFLimits) (*GAFSource, error) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "effect.gaf"), data, 0600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	return LoadGAFSourceFile(fs, "effect.gaf", int64(len(data)), limits)
}

func TestGAFSourceMatchesEagerCompositeAndOwnsBytes(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{{Name: "burst", Frames: []GAFWriteFrame{{Width: 3, Height: 2, XOffset: 1, Duration: 3, Subframes: []GAFWriteFrame{
		{Width: 2, Height: 1, Pixels: []byte{7, 9}, Transparent: []bool{false, false}},
		{Width: 1, Height: 2, XOffset: -1, Pixels: []byte{5, 8}, AlternateBlitter: 1},
	}}}}})
	if err != nil {
		t.Fatal(err)
	}
	eager, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	source, err := testGAFSource(t, data, DefaultGAFLimits())
	if err != nil {
		t.Fatal(err)
	}
	clear(data)
	if _, err := source.Frame("BURST", 0, 9); err == nil {
		t.Fatal("root budget ignored children")
	}
	got, err := source.Frame("BURST", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var removeHint func(*GAFFrame)
	removeHint = func(f *GAFFrame) {
		if !f.Transient {
			t.Fatal("source child lost transient hint")
		}
		f.Transient = false
		if f.directRaster != nil {
			f.directRaster.Transient = false
		}
		for _, child := range f.Subframes {
			removeHint(child)
		}
	}
	removeHint(got)
	if !reflect.DeepEqual(got, eager.Entries[0].Frames[0].Frame) {
		t.Fatal("selected materialization changed composite pixels or dispatch")
	}
	second, err := source.Frame("burst", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if second == got || !second.Transient || !second.Doubled().Subframes[0].Transient {
		t.Fatal("source retained decoded frames or doubling lost lifetime")
	}
}

func TestGAFSourceChecksUnselectedPayloadAndSharedChildCost(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{{Name: "burst", Frames: []GAFWriteFrame{
		{Width: 1, Height: 1, Pixels: []byte{7}},
		{Width: 1, Height: 1, Subframes: []GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{8}}, {Width: 1, Height: 1, Pixels: []byte{9}}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := LoadGAFMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	childTable := meta.Entries[0].Frames[1].Frame.DataOffset
	binary.LittleEndian.PutUint32(data[childTable+4:], binary.LittleEndian.Uint32(data[childTable:]))
	source, err := testGAFSource(t, data, DefaultGAFLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Frame("burst", 1, 2); err == nil {
		t.Fatal("root work budget ignored repeated child traversal")
	}
	f, err := source.Frame("burst", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if f.Subframes[0] != f.Subframes[1] {
		t.Fatal("shared child was allocated or charged twice")
	}
	clone, child := *f, *f.Subframes[0]
	clone.Subframes = []*GAFFrame{f.Subframes[0], &child}
	if GAFFrameBytes(&clone)-GAFFrameBytes(f) != GAFFrameBytes(&child) {
		t.Fatal("alias cache accounting charged the shared node twice")
	}
	// Damage the second frame's payload even though a consumer might request
	// only frame zero: source validation must still reject the whole bank.
	damagedChild := meta.Entries[0].Frames[1].Frame.Subframes[0]
	data = data[:damagedChild.DataOffset]
	if _, err := testGAFSource(t, data, DefaultGAFLimits()); err == nil {
		t.Fatal("accepted malformed unselected frame")
	}
}

// This opt-in probe never retains an earlier decoded frame and commits no
// package assets. It checks every authored frame, including composite children.
func TestGAFSourceEscalationFrames(t *testing.T) {
	root := os.Getenv("NANOLATHE_ESC_GAF_ROOT")
	if root == "" {
		t.Skip("set NANOLATHE_ESC_GAF_ROOT for the authored bank probe")
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"esc_nuke_a_02", "esc_nuke_x_01", "esc_weap_x_01"} {
		t.Run(name, func(t *testing.T) {
			limits := DefaultGAFLimits()
			limits.MaxDecodedPixels, limits.MaxExpandedPixels = 512<<20, 512<<20
			source, err := LoadGAFSourceFile(fs, "anims/"+name+".gaf", 256<<20, limits)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.New()
			var maxBytes uint64
			count := 0
			var add func(*GAFFrame)
			add = func(f *GAFFrame) {
				_, _ = hash.Write(f.Pixels)
				for _, child := range f.Subframes {
					add(child)
				}
			}
			for _, entry := range source.Metadata().Entries {
				for i, ref := range entry.Frames {
					f, err := source.Frame(entry.Name, i, 32<<20)
					if err != nil {
						t.Fatalf("%s frame %d: %v", entry.Name, i, err)
					}
					if f.Width != ref.Frame.Width || f.Height != ref.Frame.Height || f.XOffset != ref.Frame.XOffset || f.YOffset != ref.Frame.YOffset {
						t.Fatal("root geometry changed")
					}
					maxBytes = max(maxBytes, GAFFrameBytes(f))
					add(f)
					count++
				}
			}
			if count == 0 || bytes.Equal(hash.Sum(nil), make([]byte, 32)) {
				t.Fatal("no frames decoded")
			}
			t.Logf("frames=%d encoded=%d max-root-bytes=%d pixel-digest=%x", count, source.EncodedBytes(), maxBytes, hash.Sum(nil))
		})
	}
}
