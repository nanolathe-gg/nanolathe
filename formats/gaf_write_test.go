package formats

import "testing"

func TestEncodeGAFRoundTrip(t *testing.T) {
	// Entry one: opaque raw art. Entry two: two frames with skip runs, repeat
	// runs, an opaque colour-key index and a literal tail, so every RLE
	// command shape is exercised.
	opaque := GAFWriteFrame{Width: 4, Height: 2, Duration: 10, Pixels: []byte{1, 2, 3, 4, 5, 5, 5, 5}}
	rle := GAFWriteFrame{Width: 8, Height: 2, XOffset: -3, YOffset: 4, Duration: 2,
		Pixels:      []byte{0, 0, 9, 9, 9, 7, 7, 8, 1, 2, 3, 4, 4, 4, 4, 5},
		Transparent: []bool{true, true, false, false, false, false, false, false, false, false, false, false, false, false, false, true}}
	second := rle
	second.Pixels = append([]byte(nil), rle.Pixels...)
	second.Pixels[2] = 6
	data, err := EncodeGAF([]GAFWriteEntry{{Name: "rm_one", Loop: true, Frames: []GAFWriteFrame{opaque}}, {Name: "rm_two", Frames: []GAFWriteFrame{rle, second}}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Entries) != 2 || g.Entries[0].Name != "rm_one" || g.Entries[1].Name != "rm_two" || g.Entries[0].Unknown1 != 1 || g.Entries[1].Unknown1 != 0 {
		t.Fatalf("entries %+v", g.Entries)
	}
	check := func(entry, frame int, want GAFWriteFrame) {
		ref := g.Entries[entry].Frames[frame]
		got := ref.Frame
		if ref.Value != want.Duration || got.Width != want.Width || got.Height != want.Height || got.XOffset != want.XOffset || got.YOffset != want.YOffset {
			t.Fatalf("entry %d frame %d header %+v value %d", entry, frame, got, ref.Value)
		}
		for i := range want.Pixels {
			transparent := want.Transparent != nil && want.Transparent[i]
			if got.Transparent[i] != transparent {
				t.Fatalf("entry %d frame %d pixel %d transparency %v want %v", entry, frame, i, got.Transparent[i], transparent)
			}
			if !transparent && got.Pixels[i] != want.Pixels[i] {
				t.Fatalf("entry %d frame %d pixel %d = %d want %d", entry, frame, i, got.Pixels[i], want.Pixels[i])
			}
		}
	}
	check(0, 0, opaque)
	check(1, 0, rle)
	check(1, 1, second)
	if g.Entries[0].Frames[0].Frame.Compressed != 0 || g.Entries[1].Frames[0].Frame.Compressed != 1 {
		t.Fatal("opaque art without the colour key stores raw; anything else stores RLE")
	}
}
