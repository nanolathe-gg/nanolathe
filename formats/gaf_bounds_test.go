package formats

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestGAFSharedEntryTableUsesOneReferenceBudget(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{
		{Name: "shared", Frames: []GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{2}}}},
		{Name: "unused", Frames: []GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{3}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Both table slots point at the same immutable entry and frame-reference
	// table. A host budget should count that storage once.
	binary.LittleEndian.PutUint32(data[16:20], binary.LittleEndian.Uint32(data[12:16]))
	limits := DefaultGAFLimits()
	limits.MaxFrameRefs = 1
	gaf, err := LoadGAFWithLimits(data, limits)
	if err != nil {
		t.Fatalf("LoadGAFWithLimits: %v", err)
	}
	if len(gaf.Entries) != 2 || &gaf.Entries[0].Frames[0] != &gaf.Entries[1].Frames[0] {
		t.Fatal("shared entry table did not retain one frame-reference slice")
	}
}

func TestGAFAggregateReferenceAndPixelBudgets(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{
		{Name: "one", Frames: []GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{2}}}},
		{Name: "two", Frames: []GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{3}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultGAFLimits()
	limits.MaxFrameRefs = 1
	if _, err := LoadGAFWithLimits(data, limits); err == nil || !strings.Contains(err.Error(), "aggregate frame references") {
		t.Fatalf("reference budget error = %v", err)
	}
	limits = DefaultGAFLimits()
	limits.MaxDecodedPixels = 1
	if _, err := LoadGAFWithLimits(data, limits); err == nil || !strings.Contains(err.Error(), "aggregate decoded pixels") {
		t.Fatalf("pixel budget error = %v", err)
	}
}

func TestGAFCompositeDepthBudget(t *testing.T) {
	data := gafCompositeChain(3)
	limits := DefaultGAFLimits()
	limits.MaxCompositeDepth = 1
	if _, err := LoadGAFWithLimits(data, limits); err == nil || !strings.Contains(err.Error(), "composite depth") {
		t.Fatalf("depth budget error = %v", err)
	}
	limits.MaxCompositeDepth = 2
	if _, err := LoadGAFWithLimits(data, limits); err != nil {
		t.Fatalf("accepted composite chain: %v", err)
	}
}

func TestGAFCompositeDepthBudgetIncludesCachedSubtrees(t *testing.T) {
	// Decode the leaf and intermediate frames before the root. The cache must
	// retain their depth, otherwise the root could bypass the nesting limit.
	data := gafCompositeChainWithReferences(3, []int{2, 1, 0})
	limits := DefaultGAFLimits()
	limits.MaxCompositeDepth = 1
	if _, err := LoadGAFWithLimits(data, limits); err == nil || !strings.Contains(err.Error(), "composite depth") {
		t.Fatalf("cached depth budget error = %v", err)
	}
}

func gafCompositeChain(frames int) []byte {
	return gafCompositeChainWithReferences(frames, []int{0})
}

func gafCompositeChainWithReferences(frames int, references []int) []byte {
	const entryOffset = 16
	frameOffset := entryOffset + 40 + len(references)*8
	subtableOffset := frameOffset + frames*24
	pixelOffset := subtableOffset + (frames-1)*4
	data := make([]byte, pixelOffset+1)
	binary.LittleEndian.PutUint32(data[4:8], 1)
	binary.LittleEndian.PutUint32(data[12:16], entryOffset)
	binary.LittleEndian.PutUint16(data[entryOffset:], uint16(len(references)))
	copy(data[entryOffset+8:], "chain")
	for i, reference := range references {
		binary.LittleEndian.PutUint32(data[entryOffset+40+i*8:], uint32(frameOffset+reference*24))
	}
	for i := 0; i < frames; i++ {
		header := data[frameOffset+i*24:]
		binary.LittleEndian.PutUint16(header, 1)
		binary.LittleEndian.PutUint16(header[2:], 1)
		if i+1 < frames {
			binary.LittleEndian.PutUint16(header[10:], 1)
			binary.LittleEndian.PutUint32(header[16:], uint32(subtableOffset+i*4))
			binary.LittleEndian.PutUint32(data[subtableOffset+i*4:], uint32(frameOffset+(i+1)*24))
		} else {
			binary.LittleEndian.PutUint32(header[16:], uint32(pixelOffset))
		}
	}
	data[pixelOffset] = 2
	return data
}
