package formats

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// GAFMetadata is the validated, pixel-free index of an animation bank. It
// retains every frame fact consumers can observe without decoding its raster
// [fmt gaf]. The index shares the full loader's structural and payload
// validation, so choosing metadata never accepts corrupt content that a later
// presentation load would reject.
type GAFMetadata struct {
	Version    uint32
	EntryCount uint32
	Unknown    uint32
	Entries    []GAFMetadataEntry
}

// GAFMetadataEntry is one named sequence in a pixel-free GAF index.
type GAFMetadataEntry struct {
	Name       string
	FrameCount uint16
	Unknown1   uint16
	Unknown2   uint32
	Frames     []GAFMetadataFrameRef

	sourceOffset uint32
}

// GAFMetadataFrameRef preserves the authored frame pointer and display-delay
// word beside it [fmt gaf].
type GAFMetadataFrameRef struct {
	Offset uint32
	Value  uint32
	Frame  *GAFMetadataFrame
}

// GAFMetadataFrame records a frame header and its validated composite graph.
// Pixel planes are intentionally absent.
type GAFMetadataFrame struct {
	Width, Height    uint16
	XOffset, YOffset int16
	ColorKey         uint8
	Compressed       uint8
	Unknown2         uint32
	DataOffset       uint32
	Unknown3         uint32
	SubframeCount    uint8
	AlternateBlitter uint8
	Subframes        []*GAFMetadataFrame

	compositeDepth uint32
	// Costs of a full traversal, counting shared children once per reference.
	// Cached costs keep validation linear in the stored graph, not its expansion.
	expandedFrames, expandedPixels uint64
}

// LoadGAFMetadata validates and indexes an animation bank without allocating
// decoded pixel or transparency planes [fmt gaf].
func LoadGAFMetadata(data []byte) (*GAFMetadata, error) {
	return LoadGAFMetadataWithLimits(data, DefaultGAFLimits())
}

// LoadGAFMetadataWithLimits validates and indexes an animation bank under the
// same host limits as LoadGAFWithLimits. MaxDecodedPixels remains a bound on
// the accepted unique frame geometry: it keeps metadata and full decoding on
// one corrupt-content policy even though metadata owns no pixel planes.
func LoadGAFMetadataWithLimits(data []byte, limits GAFLimits) (*GAFMetadata, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("gaf: file is too small")
	}
	if limits.MaxFrameRefs == 0 || limits.MaxDecodedPixels == 0 || limits.MaxCompositeDepth == 0 {
		return nil, fmt.Errorf("gaf: invalid decode limits")
	}
	defaults := DefaultGAFLimits()
	if limits.MaxExpandedFrames == 0 {
		limits.MaxExpandedFrames = defaults.MaxExpandedFrames
	}
	if limits.MaxExpandedPixels == 0 {
		limits.MaxExpandedPixels = defaults.MaxExpandedPixels
	}
	meta := &GAFMetadata{
		Version:    binary.LittleEndian.Uint32(data[0:4]),
		EntryCount: binary.LittleEndian.Uint32(data[4:8]),
		Unknown:    binary.LittleEndian.Uint32(data[8:12]),
	}
	count := uint64(0)
	if n := int16(meta.EntryCount); n > 0 {
		count = uint64(n)
	}
	if count > uint64((len(data)-12)/4) {
		return nil, fmt.Errorf("gaf: entry offset table is truncated")
	}
	meta.Entries = make([]GAFMetadataEntry, 0, int(count))
	budget := gafMetadataBudget{limits: limits}
	entryCache := make(map[uint32]GAFMetadataEntry)
	frameCache := make(map[uint32]*GAFMetadataFrame)
	for i := uint64(0); i < count; i++ {
		offset := binary.LittleEndian.Uint32(data[12+i*4 : 16+i*4])
		if uint64(offset) > uint64(len(data)) || uint64(len(data))-uint64(offset) < 40 {
			return nil, fmt.Errorf("gaf: entry %d points outside file", i)
		}
		if cached, ok := entryCache[offset]; ok {
			meta.Entries = append(meta.Entries, cached)
			continue
		}
		header := data[offset : uint64(offset)+40]
		nameBytes := header[8:40]
		if nul := indexByte(nameBytes, 0); nul >= 0 {
			nameBytes = nameBytes[:nul]
		}
		entry := GAFMetadataEntry{
			Name:         string(nameBytes),
			FrameCount:   binary.LittleEndian.Uint16(header[0:2]),
			Unknown1:     binary.LittleEndian.Uint16(header[2:4]),
			Unknown2:     binary.LittleEndian.Uint32(header[4:8]),
			sourceOffset: offset,
		}
		refsStart := uint64(offset) + 40
		refBytes := uint64(entry.FrameCount) * 8
		if refsStart > uint64(len(data)) || refBytes > uint64(len(data))-refsStart {
			return nil, fmt.Errorf("gaf: entry %q frame table is truncated", entry.Name)
		}
		if err := budget.addRefs(uint64(entry.FrameCount)); err != nil {
			return nil, fmt.Errorf("gaf: entry %q: %w", entry.Name, err)
		}
		entry.Frames = make([]GAFMetadataFrameRef, entry.FrameCount)
		for frame := range entry.Frames {
			ref := data[refsStart+uint64(frame)*8 : refsStart+uint64(frame+1)*8]
			entry.Frames[frame].Offset = binary.LittleEndian.Uint32(ref[0:4])
			entry.Frames[frame].Value = binary.LittleEndian.Uint32(ref[4:8])
			decoded, err := indexGAFFrame(data, entry.Frames[frame].Offset, frameCache, make(map[uint32]bool), &budget, 0)
			if err != nil {
				return nil, fmt.Errorf("gaf: entry %q frame %d: %w", entry.Name, frame, err)
			}
			if err := budget.addExpandedRoot(decoded); err != nil {
				return nil, fmt.Errorf("gaf: entry %q frame %d: %w", entry.Name, frame, err)
			}
			entry.Frames[frame].Frame = decoded
		}
		entryCache[offset] = entry
		meta.Entries = append(meta.Entries, entry)
	}
	return meta, nil
}

// LoadGAFMetadataFile reads and indexes an animation bank from the VFS.
func LoadGAFMetadataFile(fs vfs.FSOps, name string) (*GAFMetadata, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadGAFMetadata(data)
}

// Find returns the first matching entry in authored table order.
func (g *GAFMetadata) Find(name string) (*GAFMetadataEntry, bool) {
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

type gafMetadataBudget struct {
	limits                         GAFLimits
	refs, pixels                   uint64
	expandedFrames, expandedPixels uint64
	roots                          map[*GAFMetadataFrame]bool
}

func (b *gafMetadataBudget) addExpandedRoot(frame *GAFMetadataFrame) error {
	if b.roots[frame] {
		return nil
	}
	if frame.expandedFrames > b.limits.MaxExpandedFrames-b.expandedFrames {
		return fmt.Errorf("aggregate expanded frames exceed limit")
	}
	if frame.expandedPixels > b.limits.MaxExpandedPixels-b.expandedPixels {
		return fmt.Errorf("aggregate expanded pixels exceed limit")
	}
	b.expandedFrames += frame.expandedFrames
	b.expandedPixels += frame.expandedPixels
	if b.roots == nil {
		b.roots = make(map[*GAFMetadataFrame]bool)
	}
	b.roots[frame] = true
	return nil
}

func (b *gafMetadataBudget) addRefs(n uint64) error {
	if n > b.limits.MaxFrameRefs-b.refs {
		return fmt.Errorf("aggregate frame references exceed limit")
	}
	b.refs += n
	return nil
}

func (b *gafMetadataBudget) addPixels(n uint64) error {
	if n > b.limits.MaxDecodedPixels-b.pixels {
		return fmt.Errorf("aggregate decoded pixels exceed limit")
	}
	b.pixels += n
	return nil
}

func indexGAFFrame(data []byte, offset uint32, cache map[uint32]*GAFMetadataFrame, stack map[uint32]bool, budget *gafMetadataBudget, depth uint32) (*GAFMetadataFrame, error) {
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
	frame := &GAFMetadataFrame{
		Width:            binary.LittleEndian.Uint16(header[0:2]),
		Height:           binary.LittleEndian.Uint16(header[2:4]),
		XOffset:          int16(binary.LittleEndian.Uint16(header[4:6])),
		YOffset:          int16(binary.LittleEndian.Uint16(header[6:8])),
		ColorKey:         header[8],
		Compressed:       header[9],
		Unknown2:         binary.LittleEndian.Uint32(header[12:16]),
		DataOffset:       binary.LittleEndian.Uint32(header[16:20]),
		Unknown3:         binary.LittleEndian.Uint32(header[20:24]),
		SubframeCount:    header[10],
		AlternateBlitter: header[11],
	}
	if frame.Width == 0 || frame.Height == 0 {
		return nil, fmt.Errorf("frame 0x%x has empty dimensions", offset)
	}
	pixelCount := uint64(frame.Width) * uint64(frame.Height)
	if pixelCount > uint64(math.MaxInt) || pixelCount > maxGAFFramePixels {
		return nil, fmt.Errorf("frame 0x%x is too large", offset)
	}
	if err := budget.addPixels(pixelCount); err != nil {
		return nil, fmt.Errorf("frame 0x%x: %w", offset, err)
	}
	frame.expandedFrames = 1
	frame.expandedPixels = pixelCount
	if pixelCount > budget.limits.MaxExpandedPixels {
		return nil, fmt.Errorf("expanded pixels exceed limit")
	}
	if frame.Compressed != 0 && frame.Compressed != 1 {
		return nil, fmt.Errorf("frame 0x%x has compression %d", offset, frame.Compressed)
	}
	if frame.SubframeCount != 0 {
		dataStart := uint64(frame.DataOffset)
		bytesNeeded := uint64(frame.SubframeCount) * 4
		if dataStart > uint64(len(data)) || bytesNeeded > uint64(len(data))-dataStart {
			return nil, fmt.Errorf("frame 0x%x subframe table is truncated", offset)
		}
		if err := budget.addRefs(uint64(frame.SubframeCount)); err != nil {
			return nil, fmt.Errorf("frame 0x%x: %w", offset, err)
		}
		frame.Subframes = make([]*GAFMetadataFrame, frame.SubframeCount)
		for i := range frame.Subframes {
			subOffset := binary.LittleEndian.Uint32(data[dataStart+uint64(i)*4 : dataStart+uint64(i+1)*4])
			subframe, err := indexGAFFrame(data, subOffset, cache, stack, budget, depth+1)
			if err != nil {
				return nil, err
			}
			// Check before adding, including cache hits: repeated child pointers
			// multiply downstream work and must not wrap either budget counter.
			if subframe.expandedFrames > budget.limits.MaxExpandedFrames-frame.expandedFrames {
				return nil, fmt.Errorf("expanded frames exceed limit")
			}
			if subframe.expandedPixels > budget.limits.MaxExpandedPixels-frame.expandedPixels {
				return nil, fmt.Errorf("expanded pixels exceed limit")
			}
			frame.expandedFrames += subframe.expandedFrames
			frame.expandedPixels += subframe.expandedPixels
			frame.Subframes[i] = subframe
			if subframe.compositeDepth == math.MaxUint32 || subframe.compositeDepth+1 > frame.compositeDepth {
				frame.compositeDepth = subframe.compositeDepth + 1
			}
		}
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
	} else if err := validateGAFRLE(data, offset, frame); err != nil {
		return nil, err
	}
	cache[offset] = frame
	return frame, nil
}

func validateGAFRLE(data []byte, offset uint32, frame *GAFMetadataFrame) error {
	position := uint64(frame.DataOffset)
	for row := 0; row < int(frame.Height); row++ {
		if position > uint64(len(data)) || uint64(len(data))-position < 2 {
			return fmt.Errorf("frame 0x%x RLE row header is truncated", offset)
		}
		rowSize := uint64(binary.LittleEndian.Uint16(data[position : position+2]))
		position += 2
		if rowSize > uint64(len(data))-position {
			return fmt.Errorf("frame 0x%x RLE row is truncated", offset)
		}
		if rowSize == 0 {
			continue
		}
		rowEnd := position + rowSize
		x := 0
		for position < rowEnd && x < int(frame.Width) {
			command := data[position]
			position++
			switch {
			case command&1 != 0:
				n := int(command >> 1)
				if n == 0 || x+n > int(frame.Width) {
					return fmt.Errorf("frame 0x%x invalid transparent run", offset)
				}
				x += n
			case command&2 != 0:
				n := int(command>>2) + 1
				if position >= rowEnd || x+n > int(frame.Width) {
					return fmt.Errorf("frame 0x%x invalid repeat run", offset)
				}
				position++
				x += n
			default:
				n := int(command>>2) + 1
				if uint64(n) > rowEnd-position || x+n > int(frame.Width) {
					return fmt.Errorf("frame 0x%x invalid literal run", offset)
				}
				position += uint64(n)
				x += n
			}
		}
		if x != int(frame.Width) || position != rowEnd {
			return fmt.Errorf("frame 0x%x RLE row %d does not decode to width (decoded %d/%d, payload %d bytes, consumed %d)", offset, row, x, frame.Width, rowSize, position-(rowEnd-rowSize))
		}
		position = rowEnd
	}
	return nil
}

func materializeGAF(data []byte, meta *GAFMetadata) (*GAF, error) {
	gaf := &GAF{Version: meta.Version, EntryCount: meta.EntryCount, Unknown: meta.Unknown, Entries: make([]GAFEntry, 0, len(meta.Entries))}
	entries := make(map[uint32]GAFEntry)
	frames := make(map[*GAFMetadataFrame]*GAFFrame)
	for i := range meta.Entries {
		source := &meta.Entries[i]
		if cached, ok := entries[source.sourceOffset]; ok {
			gaf.Entries = append(gaf.Entries, cached)
			continue
		}
		entry := GAFEntry{Name: source.Name, FrameCount: source.FrameCount, Unknown1: source.Unknown1, Unknown2: source.Unknown2, Frames: make([]GAFFrameRef, len(source.Frames))}
		for j := range source.Frames {
			ref := source.Frames[j]
			frame, err := materializeGAFFrame(data, ref.Frame, frames)
			if err != nil {
				return nil, err
			}
			entry.Frames[j] = GAFFrameRef{Offset: ref.Offset, Value: ref.Value, Frame: frame}
		}
		entries[source.sourceOffset] = entry
		gaf.Entries = append(gaf.Entries, entry)
	}
	return gaf, nil
}

func materializeGAFFrame(data []byte, meta *GAFMetadataFrame, cache map[*GAFMetadataFrame]*GAFFrame) (*GAFFrame, error) {
	if frame, ok := cache[meta]; ok {
		return frame, nil
	}
	pixelCount := int(meta.Width) * int(meta.Height)
	frame := &GAFFrame{Width: meta.Width, Height: meta.Height, XOffset: meta.XOffset, YOffset: meta.YOffset, ColorKey: meta.ColorKey, Compressed: meta.Compressed, Unknown2: meta.Unknown2, DataOffset: meta.DataOffset, Unknown3: meta.Unknown3, SubframeCount: meta.SubframeCount, AlternateBlitter: meta.AlternateBlitter, compositeDepth: meta.compositeDepth}
	frame.Pixels = make([]byte, pixelCount)
	frame.Transparent = make([]bool, pixelCount)
	frame.PlainPixels = frame.Pixels
	frame.PlainTransparent = frame.Transparent
	cache[meta] = frame
	if meta.SubframeCount != 0 {
		// TODO(question): retail's ordinary loader relocates direct children;
		// establish whether authored nested child tables are supported before
		// treating recursive host materialization as a retail layout contract.
		// TODO(question): trace remaining feature-mask and structure-texture
		// consumers before replacing their ordinary-only compatibility raster.
		// The established fog, glyph and direct readers use their own paths
		// [02 R-MALF-01 §6].
		for i := range frame.Transparent {
			frame.Transparent[i] = true
		}
		frame.Subframes = make([]*GAFFrame, len(meta.Subframes))
		for i, childMeta := range meta.Subframes {
			child, err := materializeGAFFrame(data, childMeta, cache)
			if err != nil {
				return nil, err
			}
			frame.Subframes[i] = child
			if child.AlternateBlitter != 0 {
				continue
			}
			compositeGAFPlain(frame, child)
		}
		return frame, nil
	}
	if meta.Compressed == 0 {
		copy(frame.Pixels, data[meta.DataOffset:uint64(meta.DataOffset)+uint64(pixelCount)])
		for i, pixel := range frame.Pixels {
			if pixel == frame.ColorKey {
				frame.Transparent[i] = true
			}
		}
		return frame, nil
	}
	// A direct consumer sees the encoded bytes, even on an RLE frame. Preserve
	// at most one raster under the existing per-frame geometry budget. A short
	// file span is rejected by DirectRaster, without rejecting ordinary decode.
	if end := uint64(meta.DataOffset) + uint64(pixelCount); end <= uint64(len(data)) {
		view := *frame
		view.Compressed = 0
		view.Pixels = append([]byte(nil), data[meta.DataOffset:end]...)
		view.Transparent, view.PlainPixels, view.PlainTransparent = nil, nil, nil
		frame.directRaster = &view
	}
	decodeGAFRLEPixels(data, meta, frame)
	return frame, nil
}

func compositeGAFPlain(parent, child *GAFFrame) {
	dx := int(parent.XOffset) - int(child.XOffset)
	dy := int(parent.YOffset) - int(child.YOffset)
	for sy := 0; sy < int(child.Height); sy++ {
		dyPos := dy + sy
		if dyPos < 0 || dyPos >= int(parent.Height) {
			continue
		}
		for sx := 0; sx < int(child.Width); sx++ {
			dxPos := dx + sx
			if dxPos < 0 || dxPos >= int(parent.Width) {
				continue
			}
			subIndex := sy*int(child.Width) + sx
			if child.PlainTransparent[subIndex] {
				continue
			}
			index := dyPos*int(parent.Width) + dxPos
			parent.PlainPixels[index] = child.PlainPixels[subIndex]
			parent.PlainTransparent[index] = false
		}
	}
}

func decodeGAFRLEPixels(data []byte, meta *GAFMetadataFrame, frame *GAFFrame) {
	position := uint64(meta.DataOffset)
	for row := 0; row < int(meta.Height); row++ {
		rowSize := uint64(binary.LittleEndian.Uint16(data[position : position+2]))
		position += 2
		if rowSize == 0 {
			for x := 0; x < int(meta.Width); x++ {
				frame.Transparent[row*int(meta.Width)+x] = true
			}
			continue
		}
		rowEnd := position + rowSize
		x := 0
		for position < rowEnd && x < int(meta.Width) {
			command := data[position]
			position++
			switch {
			case command&1 != 0:
				n := int(command >> 1)
				for i := 0; i < n; i++ {
					frame.Transparent[row*int(meta.Width)+x+i] = true
				}
				x += n
			case command&2 != 0:
				n := int(command>>2) + 1
				value := data[position]
				position++
				for i := 0; i < n; i++ {
					frame.Pixels[row*int(meta.Width)+x+i] = value
				}
				x += n
			default:
				n := int(command>>2) + 1
				copy(frame.Pixels[row*int(meta.Width)+x:row*int(meta.Width)+x+n], data[position:position+uint64(n)])
				position += uint64(n)
				x += n
			}
		}
		position = rowEnd
	}
}

func indexByte(data []byte, value byte) int {
	for i, item := range data {
		if item == value {
			return i
		}
	}
	return -1
}
