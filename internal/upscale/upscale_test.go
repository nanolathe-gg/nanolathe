package upscale

import (
	"bytes"
	"encoding/binary"
	"os"
	"runtime"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// The fixtures below are authored here, not copied from retail art. The
// palette is a grey ramp so the blend table is exactly "the entry nearest the
// average", which is the shape of the retail ALP the synthesizers assume
// [03 §3.7], and the map texture is a small deterministic pattern with enough
// structure that the search has something to match.

func testPalette() [256][3]uint8 {
	var palette [256][3]uint8
	for index := range palette {
		palette[index] = [3]uint8{uint8(index), uint8(index), uint8(index)}
	}
	return palette
}

func testALP() []byte {
	alp := make([]byte, 256*256)
	for a := range 256 {
		for b := range 256 {
			alp[a*256+b] = byte((a + b + 1) / 2)
		}
	}
	return alp
}

func testTerrainInput(t *testing.T, tilesW, tilesH int, spare int) TerrainInput {
	t.Helper()
	tiles := make([][1024]byte, tilesW*tilesH+spare)
	tileMap := make([]uint16, tilesW*tilesH)
	for placement := range tileMap {
		tileMap[placement] = uint16(placement)
		tileY, tileX := placement/tilesW, placement%tilesW
		for row := range 32 {
			for column := range 32 {
				y, x := tileY*32+row, tileX*32+column
				tiles[placement][row*32+column] = byte(40 + (x*3+y*5+(x*y)%11)%48)
			}
		}
	}
	for index := tilesW * tilesH; index < len(tiles); index++ {
		for pixel := range tiles[index] {
			tiles[index][pixel] = byte(100 + pixel%7)
		}
	}
	return TerrainInput{Tiles: tiles, TileMap: tileMap, TilesW: tilesW, TilesH: tilesH,
		Palette: testPalette(), ALP: testALP()}
}

// The cache exists because a synthesis costs seconds and its inputs decide its
// output completely [DESIGN_GPU_RENDERER §14.4]. This locks both halves of
// that: a second call for the same inputs reports a hit, and the bytes it
// hands back are the bytes that were computed.
func TestCacheTilesRoundTripReturnsTheComputedBytes(t *testing.T) {
	input := testTerrainInput(t, 2, 2, 1)
	cache := &Cache{Dir: t.TempDir()}

	computed, cached, err := cache.Tiles2x(input, Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Fatal("an empty cache reported a hit")
	}
	restored, cached, err := cache.Tiles2x(input, Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Fatal("the second call missed the cache")
	}
	if len(restored) != len(computed) {
		t.Fatalf("cache returned %d tiles, computed %d", len(restored), len(computed))
	}
	for index := range computed {
		if computed[index] != restored[index] {
			t.Fatalf("cached tile %d differs from the computed tile", index)
		}
	}

	// One pixel of one tile is a different input, so it must be a different
	// key rather than the stored answer.
	input.Tiles[0][0] ^= 1
	_, cached, err = cache.Tiles2x(input, Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Fatal("a changed tile set hit the cache")
	}
}

// Determinism is the premise the cache rests on: seeds derive from tile
// indices and tiles are independent, so the worker count is a scheduling
// choice and never an input.
func TestTilesAreIndependentOfWorkerCount(t *testing.T) {
	input := testTerrainInput(t, 2, 2, 1)
	one, err := Tiles2x(input, Options{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	four, err := Tiles2x(input, Options{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	for index := range one {
		if one[index] != four[index] {
			t.Fatalf("tile %d differs between 1 and 4 workers", index)
		}
	}
}

// A tile the tile map never places has no authored neighbourhood to search
// against, so Tiles2x doubles it by nearest sampling rather than leaving a
// hole [D2].
func TestUnplacedTileIsNearestDoubled(t *testing.T) {
	input := testTerrainInput(t, 2, 2, 1)
	spare := len(input.Tiles) - 1
	tiles, err := Tiles2x(input, Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	want := doubleTile(&input.Tiles[spare])
	if tiles[spare] != want {
		t.Fatal("an unplaced tile was not doubled by nearest sampling")
	}
}

// testBank builds a small authored bank: two entries, a few frames each, with
// one composite frame and one empty slot so the "not covered" rules of D2 are
// exercised.
func testBank() *formats.GAF {
	frame := func(w, h int, seed byte) *formats.GAFFrame {
		f := &formats.GAFFrame{Width: uint16(w), Height: uint16(h), XOffset: 3, YOffset: -4, ColorKey: 9}
		f.Pixels = make([]byte, w*h)
		f.Transparent = make([]bool, w*h)
		for index := range f.Pixels {
			f.Pixels[index] = byte(60 + (int(seed)*7+index*5+(index%w)*3)%40)
			f.Transparent[index] = (index%w+index/w)%9 == 0
		}
		f.PlainPixels, f.PlainTransparent = f.Pixels, f.Transparent
		return f
	}
	composite := frame(8, 8, 4)
	composite.SubframeCount = 1
	composite.Subframes = []*formats.GAFFrame{frame(4, 4, 5)}
	return &formats.GAF{Version: 0x10100, EntryCount: 2, Entries: []formats.GAFEntry{
		{Name: "leaf", FrameCount: 3, Frames: []formats.GAFFrameRef{
			{Frame: frame(12, 10, 1)}, {Frame: frame(12, 10, 2)}, {Frame: nil},
		}},
		{Name: "leafshadow", FrameCount: 2, Frames: []formats.GAFFrameRef{
			{Frame: frame(9, 7, 3)}, {Frame: composite},
		}},
	}}
}

// Bank2x's output shape is what the client indexes into: same entry names,
// same frame counts, doubled geometry, and a nil frame wherever the
// synthesizer does not cover the slot so the client doubles it itself [D2].
func TestBankShapeAndUncoveredSlots(t *testing.T) {
	query := testBank()
	skip := func(name string) bool { return name == "leafshadow" }
	bank, err := Bank2x(query, []*formats.GAF{query}, testPalette(), testALP(), skip, Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(bank.Entries) != len(query.Entries) {
		t.Fatalf("bank has %d entries, want %d", len(bank.Entries), len(query.Entries))
	}
	for index := range query.Entries {
		want, got := &query.Entries[index], &bank.Entries[index]
		if got.Name != want.Name || got.FrameCount != want.FrameCount || len(got.Frames) != len(want.Frames) {
			t.Fatalf("entry %d is %q/%d/%d, want %q/%d/%d", index,
				got.Name, got.FrameCount, len(got.Frames), want.Name, want.FrameCount, len(want.Frames))
		}
	}
	for _, uncovered := range [][2]int{{0, 2}, {1, 0}, {1, 1}} {
		if bank.Entries[uncovered[0]].Frames[uncovered[1]].Frame != nil {
			t.Fatalf("entry %d frame %d should be uncovered", uncovered[0], uncovered[1])
		}
	}
	for _, covered := range [][2]int{{0, 0}, {0, 1}} {
		source := query.Entries[covered[0]].Frames[covered[1]].Frame
		frame := bank.Entries[covered[0]].Frames[covered[1]].Frame
		if frame == nil {
			t.Fatalf("entry %d frame %d was not synthesized", covered[0], covered[1])
		}
		if frame.Width != 2*source.Width || frame.Height != 2*source.Height {
			t.Fatalf("frame is %dx%d, want %dx%d", frame.Width, frame.Height, 2*source.Width, 2*source.Height)
		}
		if frame.XOffset != 2*source.XOffset || frame.YOffset != 2*source.YOffset {
			t.Fatalf("anchor is %d,%d, want %d,%d", frame.XOffset, frame.YOffset, 2*source.XOffset, 2*source.YOffset)
		}
		if frame.ColorKey != source.ColorKey || frame.Compressed != 0 {
			t.Fatalf("frame carries colour key %d compressed %d", frame.ColorKey, frame.Compressed)
		}
		want := 4 * len(source.Pixels)
		if len(frame.Pixels) != want || len(frame.Transparent) != want {
			t.Fatalf("frame holds %d pixels and %d flags, want %d of each", len(frame.Pixels), len(frame.Transparent), want)
		}
		if &frame.PlainPixels[0] != &frame.Pixels[0] || len(frame.PlainTransparent) != want {
			t.Fatal("the plain raster does not alias the frame's own pixels")
		}
	}
}

func TestCacheBankRoundTripReturnsTheComputedBytes(t *testing.T) {
	query := testBank()
	cache := &Cache{Dir: t.TempDir()}
	computed, cached, err := cache.Bank2x(query, []*formats.GAF{query}, testPalette(), testALP(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Fatal("an empty cache reported a hit")
	}
	restored, cached, err := cache.Bank2x(query, []*formats.GAF{query}, testPalette(), testALP(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Fatal("the second call missed the cache")
	}
	for entry := range computed.Entries {
		for slot := range computed.Entries[entry].Frames {
			want := computed.Entries[entry].Frames[slot].Frame
			got := restored.Entries[entry].Frames[slot].Frame
			if (want == nil) != (got == nil) {
				t.Fatalf("entry %d frame %d: presence differs after the round trip", entry, slot)
			}
			if want == nil {
				continue
			}
			if want.Width != got.Width || want.Height != got.Height ||
				want.XOffset != got.XOffset || want.YOffset != got.YOffset || want.ColorKey != got.ColorKey {
				t.Fatalf("entry %d frame %d: geometry differs after the round trip", entry, slot)
			}
			if !bytes.Equal(want.Pixels, got.Pixels) {
				t.Fatalf("entry %d frame %d: pixels differ after the round trip", entry, slot)
			}
			for index := range want.Transparent {
				if want.Transparent[index] != got.Transparent[index] {
					t.Fatalf("entry %d frame %d: transparency differs at %d", entry, slot, index)
				}
			}
		}
	}
}

// A stored file that will not parse must be recomputed, not returned as art
// and not treated as a failure.
func TestUnparsableCacheFileIsRecomputed(t *testing.T) {
	input := testTerrainInput(t, 2, 2, 0)
	cache := &Cache{Dir: t.TempDir()}
	computed, _, err := cache.Tiles2x(input, Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	path, err := cache.path(terrainKey(input))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTruncated(path); err != nil {
		t.Fatal(err)
	}
	restored, cached, err := cache.Tiles2x(input, Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Fatal("a corrupt cache file was reported as a hit")
	}
	for index := range computed {
		if computed[index] != restored[index] {
			t.Fatalf("tile %d differs after a recomputation", index)
		}
	}
	if _, cached, _ := cache.Tiles2x(input, Options{Workers: 2}); !cached {
		t.Fatal("the corrupt file was not rewritten")
	}
}

func writeTruncated(path string) error {
	return os.WriteFile(path, []byte(cacheMagic+"\x01"), 0o644)
}

// The cache is an untrusted file. A mutated record count must be rejected
// from the remaining length, before it sizes a slice: the reported case is a
// small mutated file whose counts reserved hundreds of megabytes, and
// 0xFFFFFFFF would have asked for roughly 200 GB. A rejected decode already
// fell through to recompute, so what this locks is the reservation, not the
// verdict.
func TestMutatedBankCountsAreRejectedNotAllocated(t *testing.T) {
	sound := encodeBank(&formats.GAF{Version: 0x10100, EntryCount: 1, Entries: []formats.GAFEntry{{
		Name: "e", FrameCount: 1, Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{
			Width: 2, Height: 2, Pixels: []byte{1, 2, 3, 4}, Transparent: make([]bool, 4),
		}}},
	}}})
	if _, ok := decodeBank(sound); !ok {
		t.Fatal("a well-formed bank did not decode")
	}
	// Word 3 is the entry count; the first frame count follows the entry's
	// name chunk (4 length bytes, one name byte) and its three other words.
	for _, field := range []struct {
		name   string
		offset int
	}{{"entries", 12}, {"frames", 16 + 4 + 1 + 4*3}} {
		name, offset := field.name, field.offset
		for _, count := range []uint32{0xFFFFFFFF, 1 << 24, 0x00C00000} {
			mutated := append([]byte(nil), sound...)
			binary.LittleEndian.PutUint32(mutated[offset:], count)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, ok := decodeBank(mutated)
			runtime.ReadMemStats(&after)
			if ok {
				t.Fatalf("%s = %#x in a %d-byte payload was accepted", name, count, len(mutated))
			}
			// Before the length check the same three counts reserved 201 MB,
			// 604 MB and more from these 74 bytes.
			if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
				t.Fatalf("%s = %#x allocated %d bytes from a %d-byte payload", name, count, grew, len(mutated))
			}
		}
	}
}
