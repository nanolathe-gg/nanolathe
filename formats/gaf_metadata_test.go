package formats

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestGAFMetadataMatchesPixelLoaderAndPreservesAliases(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{
		{Name: "duplicate", Frames: []GAFWriteFrame{{Width: 2, Height: 1, XOffset: -2, YOffset: 3, Duration: 7, Subframes: []GAFWriteFrame{
			{Width: 1, Height: 1, XOffset: 0, YOffset: 0, Pixels: []byte{5}},
			{Width: 1, Height: 1, XOffset: 1, YOffset: 0, Pixels: []byte{7}, AlternateBlitter: 1},
		}}}},
		{Name: "Duplicate", Frames: []GAFWriteFrame{{Width: 1, Height: 1, Duration: 1, Pixels: []byte{4}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A repeated table pointer is an alias, not a second reference budget or
	// a second frame table [fmt gaf]. It also exercises first-match lookup.
	binary.LittleEndian.PutUint32(data[16:20], binary.LittleEndian.Uint32(data[12:16]))
	meta, err := LoadGAFMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	full, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	assertGAFMetadataMatches(t, meta, full)
	if &meta.Entries[0].Frames[0] != &meta.Entries[1].Frames[0] || &full.Entries[0].Frames[0] != &full.Entries[1].Frames[0] {
		t.Fatal("repeated entry pointer did not preserve shared frame references")
	}
	if found, ok := meta.Find("DUPLICATE"); !ok || found != &meta.Entries[0] {
		t.Fatal("metadata lookup did not retain the first authored match")
	}
	for i := range full.Entries {
		assertGAFMetadataMatchesFrameRefs(t, meta.Entries[i].Frames, full.Entries[i].Frames)
	}
}

func TestGAFMetadataUsesSignedLowWordEntryCount(t *testing.T) {
	data := make([]byte, 12)
	binary.LittleEndian.PutUint32(data[4:], 0x7fff8000)
	meta, err := LoadGAFMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Entries) != 0 {
		t.Fatalf("negative low-word entry count loaded %d entries", len(meta.Entries))
	}
}

func TestGAFMetadataRecordsCompositeGeometryDelayAndSelector(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{{Name: "composite", Frames: []GAFWriteFrame{{
		Width: 3, Height: 2, XOffset: -4, YOffset: 5, Duration: 13,
		Subframes: []GAFWriteFrame{
			{Width: 1, Height: 2, XOffset: 6, YOffset: -7, Pixels: []byte{2, 3}},
			{Width: 2, Height: 1, XOffset: -8, YOffset: 9, Pixels: []byte{4, 5}, AlternateBlitter: 1},
		},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := LoadGAFMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	ref := meta.Entries[0].Frames[0]
	if ref.Value != 13 {
		t.Fatalf("delay = %d, want 13", ref.Value)
	}
	frame := ref.Frame
	if frame.Width != 3 || frame.Height != 2 || frame.XOffset != -4 || frame.YOffset != 5 || frame.SubframeCount != 2 {
		t.Fatalf("parent metadata = %+v", *frame)
	}
	if len(frame.Subframes) != 2 {
		t.Fatalf("child count = %d, want 2", len(frame.Subframes))
	}
	first, second := frame.Subframes[0], frame.Subframes[1]
	if first.Width != 1 || first.Height != 2 || first.XOffset != 6 || first.YOffset != -7 || first.AlternateBlitter != 0 {
		t.Fatalf("first child metadata = %+v", *first)
	}
	if second.Width != 2 || second.Height != 1 || second.XOffset != -8 || second.YOffset != 9 || second.AlternateBlitter != 1 {
		t.Fatalf("second child metadata = %+v", *second)
	}
}

func assertGAFMetadataMatches(t *testing.T, meta *GAFMetadata, full *GAF) {
	t.Helper()
	if meta.Version != full.Version || meta.EntryCount != full.EntryCount || meta.Unknown != full.Unknown || len(meta.Entries) != len(full.Entries) {
		t.Fatalf("bank metadata=%+v full=%+v", *meta, *full)
	}
	for i := range meta.Entries {
		m, f := meta.Entries[i], full.Entries[i]
		if m.Name != f.Name || m.FrameCount != f.FrameCount || m.Unknown1 != f.Unknown1 || m.Unknown2 != f.Unknown2 {
			t.Fatalf("entry %d metadata=%+v full=%+v", i, m, f)
		}
		assertGAFMetadataMatchesFrameRefs(t, m.Frames, f.Frames)
	}
}

func assertGAFMetadataMatchesFrameRefs(t *testing.T, meta []GAFMetadataFrameRef, full []GAFFrameRef) {
	t.Helper()
	if len(meta) != len(full) {
		t.Fatalf("frame counts metadata=%d full=%d", len(meta), len(full))
	}
	for i := range meta {
		if meta[i].Offset != full[i].Offset || meta[i].Value != full[i].Value {
			t.Fatalf("frame %d ref metadata=(%d,%d) full=(%d,%d)", i, meta[i].Offset, meta[i].Value, full[i].Offset, full[i].Value)
		}
		assertGAFMetadataMatchesFrame(t, meta[i].Frame, full[i].Frame)
	}
}

func assertGAFMetadataMatchesFrame(t *testing.T, meta *GAFMetadataFrame, full *GAFFrame) {
	t.Helper()
	if meta == nil || full == nil {
		if meta != nil || full != nil {
			t.Fatalf("frame nil mismatch metadata=%v full=%v", meta, full)
		}
		return
	}
	if meta.Width != full.Width || meta.Height != full.Height || meta.XOffset != full.XOffset || meta.YOffset != full.YOffset || meta.ColorKey != full.ColorKey || meta.Compressed != full.Compressed || meta.Unknown2 != full.Unknown2 || meta.DataOffset != full.DataOffset || meta.Unknown3 != full.Unknown3 || meta.SubframeCount != full.SubframeCount || meta.AlternateBlitter != full.AlternateBlitter {
		t.Fatalf("frame metadata %+v disagrees with decoded header %+v", *meta, *full)
	}
	if len(meta.Subframes) != len(full.Subframes) {
		t.Fatalf("subframe count metadata=%d full=%d", len(meta.Subframes), len(full.Subframes))
	}
	for i := range meta.Subframes {
		assertGAFMetadataMatchesFrame(t, meta.Subframes[i], full.Subframes[i])
	}
}

func TestGAFMetadataRejectsInvalidPixelPayloadsLikeFullLoader(t *testing.T) {
	for _, tc := range []struct {
		name    string
		frame   GAFWriteFrame
		corrupt func([]byte)
		want    string
	}{
		{
			name:  "raw",
			frame: GAFWriteFrame{Width: 1, Height: 1, Pixels: []byte{2}},
			corrupt: func(data []byte) {
				frameOffset := gafFirstFrameOffset(data)
				binary.LittleEndian.PutUint32(data[frameOffset+16:], uint32(len(data)))
			},
			want: "raw pixels are truncated",
		},
		{
			name:  "rle",
			frame: GAFWriteFrame{Width: 1, Height: 1, Pixels: []byte{2}, Transparent: []bool{true}},
			corrupt: func(data []byte) {
				frameOffset := gafFirstFrameOffset(data)
				data[binary.LittleEndian.Uint32(data[frameOffset+16:])+2] = 1 // a zero-length skip run
			},
			want: "command is outside file",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := EncodeGAF([]GAFWriteEntry{{Name: "bad", Frames: []GAFWriteFrame{tc.frame}}})
			if err != nil {
				t.Fatal(err)
			}
			tc.corrupt(data)
			if _, err := LoadGAFMetadata(data); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("metadata error = %v, want %q", err, tc.want)
			}
			if _, err := LoadGAF(data); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("full loader error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestGAFMetadataPreservesDepthAndReferenceBudgets(t *testing.T) {
	data := gafCompositeChain(3)
	limits := DefaultGAFLimits()
	limits.MaxCompositeDepth = 1
	if _, err := LoadGAFMetadataWithLimits(data, limits); err == nil || !strings.Contains(err.Error(), "composite depth") {
		t.Fatalf("metadata depth error = %v", err)
	}
	limits = DefaultGAFLimits()
	limits.MaxFrameRefs = 1
	if _, err := LoadGAFMetadataWithLimits(data, limits); err == nil || !strings.Contains(err.Error(), "aggregate frame references") {
		t.Fatalf("metadata reference budget error = %v", err)
	}
}

func gafFirstFrameOffset(data []byte) uint32 {
	entryOffset := binary.LittleEndian.Uint32(data[12:16])
	return binary.LittleEndian.Uint32(data[entryOffset+40 : entryOffset+44])
}
