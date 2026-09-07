package formats

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/vfs"
)

// GAF is a decoded animation bank: named entries, each a sequence of frames
// [fmt gaf].
type GAF struct {
	Version    uint32
	EntryCount uint32
	Unknown    uint32
	Entries    []GAFEntry
}

// GAFEntry is one named sequence within a bank.
type GAFEntry struct {
	Name       string
	FrameCount uint16
	Unknown1   uint16
	Unknown2   uint32
	Frames     []GAFFrameRef
}

// GAFFrameRef is one frame slot of an entry: the source offset it was read
// from, the authored word beside it, and the decoded frame.
type GAFFrameRef struct {
	Offset uint32
	Value  uint32
	Frame  *GAFFrame
}

// GAFFrame contains decoded indexed pixels. Transparent is parallel to
// Pixels; a false value means the indexed pixel is opaque, even when Pixels is
// palette index zero. Pixels always keeps the raw decoded bytes — the
// color-key match is recorded in Transparent, never overwritten in Pixels.
type GAFFrame struct {
	Width, Height    uint16
	XOffset, YOffset int16
	// ColorKey is frame header byte +8. On the raw path (Compressed==0), the
	// indexed blitter skips every source pixel equal to this byte. It is 9 in
	// every retail frame, so index 9 is the
	// transparent color of raw frames; the RLE path carries its own skip
	// runs and ignores the key.
	ColorKey    uint8
	Compressed  uint8
	Unknown2    uint32
	DataOffset  uint32
	Unknown3    uint32
	Pixels      []byte
	Transparent []bool
	// PlainPixels and PlainTransparent are the host's ordinary keyed raster of
	// this frame. For a composite they contain only children that take the
	// ordinary path; alternate children are deliberately absent because ALP
	// reads the destination at draw time. Pixels and Transparent alias these
	// slices for existing raw-pixel consumers while those caller-specific paths
	// are traced [fmt gaf].
	PlainPixels      []byte
	PlainTransparent []bool
	// SubframeCount is the raw low byte beside AlternateBlitter. It is the
	// effective child count, including when it is zero [fmt gaf].
	SubframeCount uint8
	Subframes     []*GAFFrame
	// AlternateBlitter is the raw high byte beside the low-byte subframe
	// count. A nonzero value selects ALP composition when this frame is drawn
	// as a child; retain its authored byte for round-trip fidelity [fmt gaf].
	AlternateBlitter uint8
	// compositeDepth is the maximum number of subframe links below this frame.
	// It keeps the host composition-depth budget valid when a shared frame is
	// returned from the decode cache.
	compositeDepth uint32
}

const maxGAFFramePixels = 16 << 20

// GAFLimits bounds aggregate host allocation while decoding an animation bank.
// They are implementation safety limits, not retail file-format behavior.
type GAFLimits struct {
	MaxFrameRefs      uint64
	MaxDecodedPixels  uint64
	MaxCompositeDepth uint32
}

// DefaultGAFLimits returns the host-safety budget for one decoded bank.
func DefaultGAFLimits() GAFLimits {
	return GAFLimits{MaxFrameRefs: 1 << 20, MaxDecodedPixels: 128 << 20, MaxCompositeDepth: 64}
}

// LoadGAF decodes an animation bank from its bytes [fmt gaf].
func LoadGAF(data []byte) (*GAF, error) {
	return LoadGAFWithLimits(data, DefaultGAFLimits())
}

// LoadGAFWithLimits decodes an animation bank under explicit host-safety
// budgets [fmt gaf].
func LoadGAFWithLimits(data []byte, limits GAFLimits) (*GAF, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("gaf: file is too small")
	}
	if limits.MaxFrameRefs == 0 || limits.MaxDecodedPixels == 0 || limits.MaxCompositeDepth == 0 {
		return nil, fmt.Errorf("gaf: invalid decode limits")
	}
	gaf := &GAF{
		Version:    binary.LittleEndian.Uint32(data[0:4]),
		EntryCount: binary.LittleEndian.Uint32(data[4:8]),
		Unknown:    binary.LittleEndian.Uint32(data[8:12]),
	}
	// Retail consumes the count as a signed low word. A negative low word means
	// the loader takes no entry-table iteration; high bits are not part of the
	// bound [fmt gaf][02 R-MALF-01 §6].
	entryCount := int16(gaf.EntryCount)
	count := uint64(0)
	if entryCount > 0 {
		count = uint64(entryCount)
	}
	if count > uint64((len(data)-12)/4) {
		return nil, fmt.Errorf("gaf: entry offset table is truncated")
	}
	// The on-disk count is a u32, but only its low word is consumed by the
	// retail reader. Cap the Go allocation by the validated low-word count;
	// malformed high bits must not turn into an unbounded allocation.
	gaf.Entries = make([]GAFEntry, 0, int(count))
	frameCache := make(map[uint32]*GAFFrame)
	entryCache := make(map[uint32]GAFEntry)
	budget := gafDecodeBudget{limits: limits}
	for i := uint64(0); i < count; i++ {
		offset := uint64(binary.LittleEndian.Uint32(data[12+i*4 : 16+i*4]))
		if offset > uint64(len(data)) || uint64(len(data))-offset < 40 {
			return nil, fmt.Errorf("gaf: entry %d points outside file", i)
		}
		if cached, ok := entryCache[uint32(offset)]; ok {
			gaf.Entries = append(gaf.Entries, cached)
			continue
		}
		header := data[offset : offset+40]
		frameCount := binary.LittleEndian.Uint16(header[0:2])
		nameEnd := 8 + 32
		nameBytes := header[8:nameEnd]
		if nul := indexByte(nameBytes, 0); nul >= 0 {
			nameBytes = nameBytes[:nul]
		}
		entry := GAFEntry{
			Name:       string(nameBytes),
			FrameCount: frameCount,
			Unknown1:   binary.LittleEndian.Uint16(header[2:4]),
			Unknown2:   binary.LittleEndian.Uint32(header[4:8]),
		}
		refsStart := offset + 40
		refBytes := uint64(frameCount) * 8
		if refsStart > uint64(len(data)) || refBytes > uint64(len(data))-refsStart {
			return nil, fmt.Errorf("gaf: entry %q frame table is truncated", entry.Name)
		}
		if err := budget.addRefs(uint64(frameCount)); err != nil {
			return nil, fmt.Errorf("gaf: entry %q: %w", entry.Name, err)
		}
		entry.Frames = make([]GAFFrameRef, frameCount)
		for frame := range entry.Frames {
			ref := data[refsStart+uint64(frame)*8 : refsStart+uint64(frame+1)*8]
			entry.Frames[frame].Offset = binary.LittleEndian.Uint32(ref[0:4])
			entry.Frames[frame].Value = binary.LittleEndian.Uint32(ref[4:8])
			decoded, err := decodeGAFFrame(data, entry.Frames[frame].Offset, frameCache, make(map[uint32]bool), &budget, 0)
			if err != nil {
				return nil, fmt.Errorf("gaf: entry %q frame %d: %w", entry.Name, frame, err)
			}
			entry.Frames[frame].Frame = decoded
		}
		entryCache[uint32(offset)] = entry
		gaf.Entries = append(gaf.Entries, entry)
	}
	return gaf, nil
}

// LoadGAFFile reads and decodes an animation bank from the VFS.
func LoadGAFFile(fs vfs.FSOps, name string) (*GAF, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadGAF(data)
}

// Find returns the first matching entry in authored table order. ASCII letter
// folding is established; high bytes are preserved as a host fallback.
// TODO(question): establish the locale comparison used for high bytes.
func (g *GAF) Find(name string) (*GAFEntry, bool) {
	if g == nil {
		return nil, false
	}
	for i := range g.Entries {
		if gafASCIIEqual(g.Entries[i].Name, name) {
			return &g.Entries[i], true
		}
	}
	return nil, false
}

func gafASCIIEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if gafASCIIFold(a[i]) != gafASCIIFold(b[i]) {
			return false
		}
	}
	return true
}

func gafASCIIFold(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// At returns the palette index at (x, y) and whether that pixel is opaque.
// A coordinate outside the frame reads as transparent.
func (f *GAFFrame) At(x, y int) (byte, bool) {
	if x < 0 || y < 0 || x >= int(f.Width) || y >= int(f.Height) {
		return 0, false
	}
	index := y*int(f.Width) + x
	if index >= len(f.Pixels) || index >= len(f.Transparent) {
		return 0, false
	}
	return f.Pixels[index], !f.Transparent[index]
}

type gafDecodeBudget struct {
	limits       GAFLimits
	refs, pixels uint64
}

func (b *gafDecodeBudget) addRefs(n uint64) error {
	if n > b.limits.MaxFrameRefs-b.refs {
		return fmt.Errorf("aggregate frame references exceed limit")
	}
	b.refs += n
	return nil
}

func (b *gafDecodeBudget) addPixels(n uint64) error {
	if n > b.limits.MaxDecodedPixels-b.pixels {
		return fmt.Errorf("aggregate decoded pixels exceed limit")
	}
	b.pixels += n
	return nil
}

func decodeGAFFrame(data []byte, offset uint32, cache map[uint32]*GAFFrame, stack map[uint32]bool, budget *gafDecodeBudget, depth uint32) (*GAFFrame, error) {
	if stack[offset] {
		return nil, fmt.Errorf("frame cycle at 0x%x", offset)
	}
	if depth > budget.limits.MaxCompositeDepth {
		return nil, fmt.Errorf("composite depth exceeds limit")
	}
	if frame, ok := cache[offset]; ok {
		if frame.compositeDepth > budget.limits.MaxCompositeDepth-depth {
			return nil, fmt.Errorf("composite depth exceeds limit")
		}
		return frame, nil
	}
	if uint64(offset) > uint64(len(data)) || uint64(len(data))-uint64(offset) < 24 {
		return nil, fmt.Errorf("frame pointer 0x%x is outside file", offset)
	}
	stack[offset] = true
	defer delete(stack, offset)
	header := data[offset : uint64(offset)+24]
	width := binary.LittleEndian.Uint16(header[0:2])
	height := binary.LittleEndian.Uint16(header[2:4])
	if width == 0 || height == 0 {
		return nil, fmt.Errorf("frame 0x%x has empty dimensions", offset)
	}
	pixelCount := uint64(width) * uint64(height)
	if pixelCount > uint64(math.MaxInt) || pixelCount > maxGAFFramePixels {
		return nil, fmt.Errorf("frame 0x%x is too large", offset)
	}
	if err := budget.addPixels(pixelCount); err != nil {
		return nil, fmt.Errorf("frame 0x%x: %w", offset, err)
	}
	frame := &GAFFrame{
		Width: width, Height: height,
		XOffset:  int16(binary.LittleEndian.Uint16(header[4:6])),
		YOffset:  int16(binary.LittleEndian.Uint16(header[6:8])),
		ColorKey: header[8], Compressed: header[9],
		Unknown2:         binary.LittleEndian.Uint32(header[12:16]),
		DataOffset:       binary.LittleEndian.Uint32(header[16:20]),
		Unknown3:         binary.LittleEndian.Uint32(header[20:24]),
		SubframeCount:    header[10],
		AlternateBlitter: header[11],
	}
	frame.Pixels = make([]byte, int(pixelCount))
	frame.Transparent = make([]bool, int(pixelCount))
	frame.PlainPixels = frame.Pixels
	frame.PlainTransparent = frame.Transparent
	// Retail frames have ColorKey==9 for all 48519 frames; synthetic
	// mod/test frames may use 0 and should remain loadable.
	if frame.Compressed != 0 && frame.Compressed != 1 {
		return nil, fmt.Errorf("frame 0x%x has compression %d", offset, frame.Compressed)
	}
	// The count is the low byte only; the high byte is AlternateBlitter for a
	// frame used as a composite child [fmt gaf][02 R-MALF-01 §6].
	if subCount := uint8(header[10]); subCount != 0 {
		dataStart := uint64(frame.DataOffset)
		bytesNeeded := uint64(subCount) * 4
		if dataStart > uint64(len(data)) || bytesNeeded > uint64(len(data))-dataStart {
			return nil, fmt.Errorf("frame 0x%x subframe table is truncated", offset)
		}
		if err := budget.addRefs(uint64(subCount)); err != nil {
			return nil, fmt.Errorf("frame 0x%x: %w", offset, err)
		}
		// A composite is a child table, not a raster. Its transparent pixel
		// backing avoids preflattening the alternate ALP path against an invented
		// destination; presentation emits its leaves in authored order.
		for i := range frame.Transparent {
			frame.Transparent[i] = true
		}
		frame.Subframes = make([]*GAFFrame, subCount)
		for i := range frame.Subframes {
			subOffset := binary.LittleEndian.Uint32(data[dataStart+uint64(i)*4 : dataStart+uint64(i+1)*4])
			subframe, err := decodeGAFFrame(data, subOffset, cache, stack, budget, depth+1)
			if err != nil {
				return nil, err
			}
			frame.Subframes[i] = subframe
			if subframe.compositeDepth == math.MaxUint32 || subframe.compositeDepth+1 > frame.compositeDepth {
				frame.compositeDepth = subframe.compositeDepth + 1
			}
			if subframe.AlternateBlitter != 0 {
				// This child needs the destination-reading ALP path. Do not
				// flatten it against index zero or any other invented backdrop.
				continue
			}
			// The compatibility raster has only ordinary child coverage. It is
			// for callers that consume a frame as a texture or special mask;
			// general keyed/tinted presentation emits the actual child leaves.
			dx := int(frame.XOffset) - int(subframe.XOffset)
			dy := int(frame.YOffset) - int(subframe.YOffset)
			for sy := 0; sy < int(subframe.Height); sy++ {
				dyPos := dy + sy
				if dyPos < 0 || dyPos >= int(frame.Height) {
					continue
				}
				for sx := 0; sx < int(subframe.Width); sx++ {
					dxPos := dx + sx
					if dxPos < 0 || dxPos >= int(frame.Width) {
						continue
					}
					subIndex := sy*int(subframe.Width) + sx
					if subIndex >= len(subframe.PlainTransparent) || subframe.PlainTransparent[subIndex] {
						continue
					}
					if subIndex >= len(subframe.PlainPixels) {
						continue
					}
					index := dyPos*int(frame.Width) + dxPos
					frame.PlainPixels[index] = subframe.PlainPixels[subIndex]
					frame.PlainTransparent[index] = false
				}
			}
		}
		// TODO(question): retail's ordinary loader relocates direct children;
		// establish whether authored nested child tables are supported before
		// treating recursive host decoding as a retail layout contract.
		// TODO(question): establish alternate-child semantics for raw-pixel
		// callers (scaled, LHT, feature, fog, and model texture paths). They
		// receive this ordinary compatibility raster, with ALP-only children
		// absent rather than incorrectly precomposed.
		if frame.compositeDepth > budget.limits.MaxCompositeDepth-depth {
			return nil, fmt.Errorf("composite depth exceeds limit")
		}
		cache[offset] = frame
		return frame, nil
	}

	if frame.Compressed == 0 {
		dataStart := uint64(frame.DataOffset)
		if dataStart > uint64(len(data)) || pixelCount > uint64(len(data))-dataStart {
			return nil, fmt.Errorf("frame 0x%x raw pixels are truncated", offset)
		}
		copy(frame.Pixels, data[dataStart:dataStart+pixelCount])
		// Raw frames carry no skip runs: their only transparency is the
		// frame's own color key, which the indexed blitter compares per pixel
		// [fmt gaf]. The mask families anims/fog.gaf,
		// anims/fogtiles.gaf and anims/vismasks.gaf are built entirely from
		// key pixels and index 0, so without this the fog clouds blit as
		// solid palette 9 (84,84,252) instead of black, and every sight
		// shape fills to its bounding box [R-RR16-A §3].
		for i, pixel := range frame.Pixels {
			if pixel == frame.ColorKey {
				frame.Transparent[i] = true
			}
		}
		cache[offset] = frame
		return frame, nil
	}

	// Per-row RLE [fmt gaf "RLE pixels"]. Each row is a u16 payload length
	// followed by commands decoded until the row holds exactly width pixels.
	//
	// A decode of the retail interface side panels that comes out as
	// high-entropy, near-black indexes is correct, not a defect: that art is a
	// dithered panel texture built from the darkest entry of many palette
	// ramps, so it reads as coloured static when brightened. WU-17-12 traced
	// it and left the caveat in [fmt gaf "Unknowns and caveats"]; read that
	// before changing anything below to make a picture look better.
	position := uint64(frame.DataOffset)
	for row := 0; row < int(height); row++ {
		if position > uint64(len(data)) || uint64(len(data))-position < 2 {
			return nil, fmt.Errorf("frame 0x%x RLE row header is truncated", offset)
		}
		rowSize := uint64(binary.LittleEndian.Uint16(data[position : position+2]))
		position += 2
		if rowSize > uint64(len(data))-position {
			return nil, fmt.Errorf("frame 0x%x RLE row is truncated", offset)
		}
		if rowSize == 0 {
			for x := 0; x < int(width); x++ {
				frame.Transparent[row*int(width)+x] = true
			}
			continue
		}
		rowEnd := position + rowSize
		x := 0
		for position < rowEnd && x < int(width) {
			command := data[position]
			position++
			switch {
			case command&1 != 0:
				n := int(command >> 1)
				if n == 0 || x+n > int(width) {
					return nil, fmt.Errorf("frame 0x%x invalid transparent run", offset)
				}
				for i := 0; i < n; i++ {
					frame.Transparent[row*int(width)+x+i] = true
				}
				x += n
			case command&2 != 0:
				n := int(command>>2) + 1
				if position >= rowEnd || x+n > int(width) {
					return nil, fmt.Errorf("frame 0x%x invalid repeat run", offset)
				}
				value := data[position]
				position++
				for i := 0; i < n; i++ {
					frame.Pixels[row*int(width)+x+i] = value
				}
				x += n
			default:
				n := int(command>>2) + 1
				if uint64(n) > rowEnd-position || x+n > int(width) {
					return nil, fmt.Errorf("frame 0x%x invalid literal run", offset)
				}
				copy(frame.Pixels[row*int(width)+x:row*int(width)+x+n], data[position:position+uint64(n)])
				position += uint64(n)
				x += n
			}
		}
		if x != int(width) || position != rowEnd {
			return nil, fmt.Errorf("frame 0x%x RLE row %d does not decode to width (decoded %d/%d, payload %d bytes, consumed %d)", offset, row, x, width, rowSize, position-(rowEnd-rowSize))
		}
		position = rowEnd
	}
	cache[offset] = frame
	return frame, nil
}

func indexByte(data []byte, value byte) int {
	for i, item := range data {
		if item == value {
			return i
		}
	}
	return -1
}
