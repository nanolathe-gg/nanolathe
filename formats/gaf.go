package formats

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/nanolathe/nanolathe/vfs"
)

type GAF struct {
	Version    uint32
	EntryCount uint32
	Unknown    uint32
	Entries    []GAFEntry
	byName     map[string]int
}

type GAFEntry struct {
	Name       string
	FrameCount uint16
	Unknown1   uint16
	Unknown2   uint32
	Frames     []GAFFrameRef
}

type GAFFrameRef struct {
	Offset uint32
	Value  uint32
	Frame  *GAFFrame
}

// GAFFrame contains decoded indexed pixels. Transparent is parallel to
// Pixels; a false value means the indexed pixel is opaque, even when Pixels is
// palette index zero.
type GAFFrame struct {
	Width, Height    uint16
	XOffset, YOffset int16
	Unknown1         uint8
	Compressed       uint8
	Unknown2         uint32
	DataOffset       uint32
	Unknown3         uint32
	Pixels           []byte
	Transparent      []bool
	Subframes        []*GAFFrame
}

const maxGAFFramePixels = 16 << 20

func LoadGAF(data []byte) (*GAF, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("gaf: file is too small")
	}
	gaf := &GAF{
		Version:    binary.LittleEndian.Uint32(data[0:4]),
		EntryCount: binary.LittleEndian.Uint32(data[4:8]),
		Unknown:    binary.LittleEndian.Uint32(data[8:12]),
		byName:     make(map[string]int),
	}
	// Retail uses only low 16 bits of the entry count (02:GAF). High bits are
	// ignored; we mask for parity and keep full value in EntryCount for diagnostics.
	count := uint64(gaf.EntryCount & 0xFFFF)
	if count > uint64((len(data)-12)/4) {
		return nil, fmt.Errorf("gaf: entry offset table is truncated")
	}
	gaf.Entries = make([]GAFEntry, 0, gaf.EntryCount)
	frameCache := make(map[uint32]*GAFFrame)
	for i := uint64(0); i < count; i++ {
		offset := uint64(binary.LittleEndian.Uint32(data[12+i*4 : 16+i*4]))
		if offset > uint64(len(data)) || uint64(len(data))-offset < 40 {
			return nil, fmt.Errorf("gaf: entry %d points outside file", i)
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
		if refBytes > uint64(len(data))-refsStart {
			return nil, fmt.Errorf("gaf: entry %q frame table is truncated", entry.Name)
		}
		entry.Frames = make([]GAFFrameRef, frameCount)
		for frame := range entry.Frames {
			ref := data[refsStart+uint64(frame)*8 : refsStart+uint64(frame+1)*8]
			entry.Frames[frame].Offset = binary.LittleEndian.Uint32(ref[0:4])
			entry.Frames[frame].Value = binary.LittleEndian.Uint32(ref[4:8])
			decoded, err := decodeGAFFrame(data, entry.Frames[frame].Offset, frameCache, make(map[uint32]bool))
			if err != nil {
				return nil, fmt.Errorf("gaf: entry %q frame %d: %w", entry.Name, frame, err)
			}
			entry.Frames[frame].Frame = decoded
		}
		gaf.byName[strings.ToLower(entry.Name)] = len(gaf.Entries)
		gaf.Entries = append(gaf.Entries, entry)
	}
	return gaf, nil
}

func LoadGAFFile(fs vfs.FSOps, name string) (*GAF, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadGAF(data)
}

// LoadGAFFileWithLimit bounds the archive backing store before decoding it.
// Callers that inspect user-installed content should prefer this variant.
func LoadGAFFileWithLimit(fs vfs.FSOps, name string, maxBytes int64) (*GAF, error) {
	data, err := readVFSWithLimit(fs, name, maxBytes)
	if err != nil {
		return nil, err
	}
	return LoadGAF(data)
}

func (g *GAF) Find(name string) (*GAFEntry, bool) {
	index, ok := g.byName[strings.ToLower(name)]
	if !ok {
		return nil, false
	}
	return &g.Entries[index], true
}

func (f *GAFFrame) At(x, y int) (byte, bool) {
	if x < 0 || y < 0 || x >= int(f.Width) || y >= int(f.Height) {
		return 0, false
	}
	index := y*int(f.Width) + x
	return f.Pixels[index], !f.Transparent[index]
}

func decodeGAFFrame(data []byte, offset uint32, cache map[uint32]*GAFFrame, stack map[uint32]bool) (*GAFFrame, error) {
	if stack[offset] {
		return nil, fmt.Errorf("frame cycle at 0x%x", offset)
	}
	if frame, ok := cache[offset]; ok {
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
	frame := &GAFFrame{
		Width: width, Height: height,
		XOffset:  int16(binary.LittleEndian.Uint16(header[4:6])),
		YOffset:  int16(binary.LittleEndian.Uint16(header[6:8])),
		Unknown1: header[8], Compressed: header[9],
		Unknown2:   binary.LittleEndian.Uint32(header[12:16]),
		DataOffset: binary.LittleEndian.Uint32(header[16:20]),
		Unknown3:   binary.LittleEndian.Uint32(header[20:24]),
	}
	frame.Pixels = make([]byte, int(pixelCount))
	frame.Transparent = make([]bool, int(pixelCount))
	// Retail frames have Unknown1==9 for all 48519 frames; synthetic
	// mod/test frames may use 0 and should remain loadable.
	if frame.Compressed != 0 && frame.Compressed != 1 {
		return nil, fmt.Errorf("frame 0x%x has compression %d", offset, frame.Compressed)
	}
	// Retail subframe count is a single byte at header[10] (02:GAF) with
	// header[11]==0 for strict parity; synthetic frames may use other
	// values and remain loadable for the viewer.
	if subCountByte := header[10]; subCountByte != 0 {
		subCount := uint16(subCountByte)
		dataStart := uint64(frame.DataOffset)
		bytesNeeded := uint64(subCount) * 4
		if dataStart > uint64(len(data)) || bytesNeeded > uint64(len(data))-dataStart {
			return nil, fmt.Errorf("frame 0x%x subframe table is truncated", offset)
		}
		// A composite frame only owns the pixels its subframes cover. Everything
		// else stays transparent instead of decoding to an opaque index zero.
		for i := range frame.Transparent {
			frame.Transparent[i] = true
		}
		frame.Subframes = make([]*GAFFrame, subCount)
		for i := range frame.Subframes {
			subOffset := binary.LittleEndian.Uint32(data[dataStart+uint64(i)*4 : dataStart+uint64(i+1)*4])
			subframe, err := decodeGAFFrame(data, subOffset, cache, stack)
			if err != nil {
				return nil, err
			}
			frame.Subframes[i] = subframe
			// XOffset/YOffset are anchor distances: a frame's top-left corner
			// sits that many pixels before its anchor point. A subframe shares
			// the parent anchor, so its position inside the parent is the
			// parent's offset minus its own.
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
					if subframe.Transparent[subIndex] {
						continue
					}
					index := dyPos*int(frame.Width) + dxPos
					frame.Pixels[index] = subframe.Pixels[subIndex]
					frame.Transparent[index] = false
				}
			}
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
		cache[offset] = frame
		return frame, nil
	}

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
