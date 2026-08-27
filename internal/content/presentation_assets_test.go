package content

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/palette"
)

func TestLoadPresentationAssetsEagerlyIndexesAuthoredEntries(t *testing.T) {
	fs := newFixtureFS(t,
		fixtureFile{path: "anims/cursors.gaf", data: string(testGAF("Pointer", 2, 1))},
		fixtureFile{path: "textures/logos.gaf", data: string(testGAFEntries(
			testGAFEntry{name: "Team", frames: 10, duration: 2},
			testGAFEntry{name: "onoff01", frames: 2, duration: 3},
		))},
		fixtureFile{path: "fonts/test.fnt", data: string(testFNT())},
	)
	catalog, err := LoadPresentationAssets(fs)
	if err != nil {
		t.Fatal(err)
	}
	assets := catalog.Assets()
	cursorID := AssetID("anims/cursors.gaf#pointer")
	cursor, ok := assets.Cursor(cursorID)
	if !ok || len(cursor.Frames.Frames) != 2 || cursor.HotspotX != 3 || cursor.HotspotY != -2 {
		t.Fatalf("cursor asset = %#v, ok=%v", cursor, ok)
	}
	logoID := AssetID("textures/logos.gaf#team")
	logo, ok := assets.Model(logoID)
	if !ok || logo.Textures.Logos != logoID || len(logo.Textures.Durations) != 10 {
		t.Fatalf("LOGOS asset = %#v, ok=%v", logo, ok)
	}
	ordinaryID := AssetID("textures/logos.gaf#onoff01")
	ordinary, ok := assets.Model(ordinaryID)
	if !ok || ordinary.Textures.Logos != "" || ordinary.Textures.Default != ordinaryID || len(ordinary.Textures.Durations) != 2 {
		t.Fatalf("ordinary logos entry was misclassified: %#v, ok=%v", ordinary, ok)
	}
	frameID := AssetID("anims/cursors.gaf#pointer/frame/0")
	frame, ok := catalog.Frame(frameID)
	if !ok || catalog.frames[frameID].Duration != 1 || frame.Pixels[0] != 7 {
		t.Fatalf("frame = %#v, ok=%v", frame, ok)
	}
	frame.Pixels[0] = 99
	frameAgain, _ := catalog.Frame(frameID)
	if frameAgain.Pixels[0] != 7 {
		t.Fatal("frame lookup exposed mutable catalog bytes")
	}
	font, ok := catalog.Font("fonts/test.fnt")
	if !ok || font.Glyphs['A'] == nil || !font.Glyphs['A'].On(0, 0) {
		t.Fatal("eager FNT catalog entry missing")
	}
	font.Glyphs['A'].Bits[0] = 0
	fontAgain, _ := catalog.Font("fonts/test.fnt")
	if !fontAgain.Glyphs['A'].On(0, 0) {
		t.Fatal("font lookup exposed mutable catalog bytes")
	}
}

func TestPresentationPaletteIsDetached(t *testing.T) {
	tables := &palette.Tables{}
	tables.Base[7] = [4]byte{1, 2, 3, 0}
	catalog := &PresentationCatalog{pal: tables}
	got := catalog.Palette()
	if got == nil {
		t.Fatal("palette lookup returned nil")
	}
	got.Base[7][0] = 99
	if tables.Base[7][0] != 1 {
		t.Fatal("palette lookup exposed mutable catalog table")
	}
}

func TestLoadPresentationAssetsMissingOptionalArtIsAbsent(t *testing.T) {
	fs := newFixtureFS(t)
	catalog, err := LoadPresentationAssets(fs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.Assets().Cursor("anims/cursors.gaf#missing"); ok {
		t.Fatal("missing cursor was synthesized")
	}
	if catalog.Palette() != nil {
		t.Fatal("partial palette set should remain absent")
	}
}

type testGAFEntry struct {
	name     string
	frames   int
	duration uint32
}

func testGAF(name string, frames int, duration uint32) []byte {
	return testGAFEntries(testGAFEntry{name: name, frames: frames, duration: duration})
}

func testGAFEntries(entries ...testGAFEntry) []byte {
	const entryTableOffset = 12
	const entrySize = 40
	const frameRefSize = 8
	const frameSize = 24
	entryOffsets := make([]int, len(entries))
	cursor := entryTableOffset + len(entries)*4
	totalFrames := 0
	for i, entry := range entries {
		entryOffsets[i] = cursor
		cursor += entrySize + entry.frames*frameRefSize
		totalFrames += entry.frames
	}
	frameDataOffset := cursor + totalFrames*frameSize
	out := make([]byte, frameDataOffset+totalFrames)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(entries)))
	for i, entry := range entries {
		entryOffset := entryOffsets[i]
		refOffset := entryOffset + entrySize
		frameOffset := cursor
		binary.LittleEndian.PutUint32(out[entryTableOffset+i*4:], uint32(entryOffset))
		binary.LittleEndian.PutUint16(out[entryOffset:], uint16(entry.frames))
		copy(out[entryOffset+8:], entry.name)
		for frameIndex := 0; frameIndex < entry.frames; frameIndex++ {
			ref := refOffset + frameIndex*frameRefSize
			frame := frameOffset + frameIndex*frameSize
			binary.LittleEndian.PutUint32(out[ref:], uint32(frame))
			binary.LittleEndian.PutUint32(out[ref+4:], entry.duration)
			binary.LittleEndian.PutUint16(out[frame:], 1)
			binary.LittleEndian.PutUint16(out[frame+2:], 1)
			binary.LittleEndian.PutUint16(out[frame+4:], 3)
			binary.LittleEndian.PutUint16(out[frame+6:], 0xfffe)
			out[frame+8] = 9
			binary.LittleEndian.PutUint32(out[frame+16:], uint32(frameDataOffset))
			out[frameDataOffset] = 7
			frameDataOffset++
		}
		cursor += entry.frames * frameSize
	}
	return out
}

func testFNT() []byte {
	data := make([]byte, 516+2)
	binary.LittleEndian.PutUint16(data[0:], 1)
	binary.LittleEndian.PutUint16(data[4+uint16('A')*2:], 516)
	data[516], data[517] = 1, 0x80
	return data
}
