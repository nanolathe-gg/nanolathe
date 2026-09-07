package formats

import (
	"encoding/binary"
	"fmt"
)

// GAFWriteFrame is one authored frame of an entry to encode. Transparent is
// optional and parallel to Pixels; a nil slice means every pixel is opaque.
type GAFWriteFrame struct {
	Width, Height    uint16
	XOffset, YOffset int16
	// Duration is the frame reference's display count in whole simulation
	// ticks [fmt gaf]; retail texture art stores 10.
	Duration    uint32
	Pixels      []byte
	Transparent []bool
	// Subframes makes this frame a composite. The child table is written in
	// slice order; its effective count is one byte [fmt gaf].
	Subframes []GAFWriteFrame
	// AlternateBlitter is the authored high byte next to the child count. A
	// nonzero value selects ALP when this frame is a composite child [fmt gaf].
	AlternateBlitter uint8
}

// GAFWriteEntry is one named sequence. Loop is written as the entry's loop
// byte [fmt gaf]; every retail entry stores it set.
type GAFWriteEntry struct {
	Name   string
	Loop   bool
	Frames []GAFWriteFrame
}

// gafColorKey is the raw-path transparent index every retail frame stores
// [fmt gaf]. A frame that must carry an opaque pixel of that index, or any
// transparent pixel, is written on the RLE path where transparency lives in
// skip runs and the key is not consulted.
const gafColorKey = 9

// EncodeGAF serialises entries into the GAF container [fmt gaf]: 12-byte
// header, entry offset table, 40-byte entry headers each followed by its
// 8-byte frame references, then 24-byte frame headers and pixel data or child
// tables. Entry names are at most 31 bytes.
func EncodeGAF(entries []GAFWriteEntry) ([]byte, error) {
	if len(entries) == 0 || len(entries) > 0x7fff {
		return nil, fmt.Errorf("gaf encode: entry count %d outside signed low-word range", len(entries))
	}
	const headerSize, entrySize, refSize, frameSize = 12, 40, 8, 24
	type framePlan struct {
		frame      *GAFWriteFrame
		children   []*framePlan
		header     uint32
		dataOffset uint32
		data       []byte
		raw        bool
	}
	entryOffsets := make([]uint32, len(entries))
	offset := headerSize + 4*len(entries)
	for i, entry := range entries {
		if entry.Name == "" || len(entry.Name) > 31 {
			return nil, fmt.Errorf("gaf encode: entry name %q must be 1..31 bytes", entry.Name)
		}
		if len(entry.Frames) == 0 || len(entry.Frames) > 0xFFFF {
			return nil, fmt.Errorf("gaf encode: entry %q has %d frames", entry.Name, len(entry.Frames))
		}
		entryOffsets[i] = uint32(offset)
		offset += entrySize + refSize*len(entry.Frames)
	}
	allPlans := make([]*framePlan, 0)
	roots := make([][]*framePlan, len(entries))
	maxDepth := DefaultGAFLimits().MaxCompositeDepth
	var planFrame func(entry string, frame *GAFWriteFrame, depth uint32) (*framePlan, error)
	planFrame = func(entry string, frame *GAFWriteFrame, depth uint32) (*framePlan, error) {
		// Bound authored object graphs, including cyclic slices, at the same
		// host depth boundary as decoding. This is not a retail writer rule.
		if depth > maxDepth {
			return nil, fmt.Errorf("gaf encode: composite depth exceeds host limit")
		}
		if frame == nil {
			return nil, fmt.Errorf("gaf encode: entry %q has nil frame", entry)
		}
		if frame.Width == 0 || frame.Height == 0 {
			return nil, fmt.Errorf("gaf encode: entry %q frame has empty dimensions", entry)
		}
		plan := &framePlan{frame: frame}
		allPlans = append(allPlans, plan)
		if len(frame.Subframes) != 0 {
			if len(frame.Subframes) > 0xff {
				return nil, fmt.Errorf("gaf encode: entry %q composite has %d children", entry, len(frame.Subframes))
			}
			plan.children = make([]*framePlan, len(frame.Subframes))
			for i := range frame.Subframes {
				child, err := planFrame(entry, &frame.Subframes[i], depth+1)
				if err != nil {
					return nil, err
				}
				plan.children[i] = child
			}
			return plan, nil
		}
		n := int(frame.Width) * int(frame.Height)
		if len(frame.Pixels) != n {
			return nil, fmt.Errorf("gaf encode: entry %q frame: %d pixels for %dx%d", entry, len(frame.Pixels), frame.Width, frame.Height)
		}
		if frame.Transparent != nil && len(frame.Transparent) != n {
			return nil, fmt.Errorf("gaf encode: entry %q frame: transparency mask length %d for %d pixels", entry, len(frame.Transparent), n)
		}
		raw := true
		for p := 0; p < n; p++ {
			if (frame.Transparent != nil && frame.Transparent[p]) || frame.Pixels[p] == gafColorKey {
				raw = false
				break
			}
		}
		plan.raw = raw
		if raw {
			plan.data = frame.Pixels
		} else {
			plan.data = encodeGAFRLE(*frame)
		}
		return plan, nil
	}
	for i := range entries {
		roots[i] = make([]*framePlan, len(entries[i].Frames))
		for f := range entries[i].Frames {
			plan, err := planFrame(entries[i].Name, &entries[i].Frames[f], 0)
			if err != nil {
				return nil, err
			}
			roots[i][f] = plan
		}
	}
	for _, plan := range allPlans {
		plan.header = uint32(offset)
		offset += frameSize
	}
	for _, plan := range allPlans {
		plan.dataOffset = uint32(offset)
		if len(plan.children) != 0 {
			offset += len(plan.children) * 4
		} else {
			offset += len(plan.data)
		}
	}

	data := make([]byte, offset)
	binary.LittleEndian.PutUint32(data[0:4], 0x00010100)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(entries)))
	for i, entry := range entries {
		binary.LittleEndian.PutUint32(data[12+i*4:16+i*4], entryOffsets[i])
		header := data[entryOffsets[i] : entryOffsets[i]+entrySize]
		binary.LittleEndian.PutUint16(header[0:2], uint16(len(entry.Frames)))
		if entry.Loop {
			binary.LittleEndian.PutUint16(header[2:4], 1)
		}
		copy(header[8:40], entry.Name)
		for f, frame := range entry.Frames {
			ref := data[entryOffsets[i]+entrySize+uint32(f*refSize):]
			binary.LittleEndian.PutUint32(ref[0:4], roots[i][f].header)
			binary.LittleEndian.PutUint32(ref[4:8], frame.Duration)
		}
	}
	for _, plan := range allPlans {
		frame := plan.frame
		fh := data[plan.header : plan.header+frameSize]
		binary.LittleEndian.PutUint16(fh[0:2], frame.Width)
		binary.LittleEndian.PutUint16(fh[2:4], frame.Height)
		binary.LittleEndian.PutUint16(fh[4:6], uint16(frame.XOffset))
		binary.LittleEndian.PutUint16(fh[6:8], uint16(frame.YOffset))
		fh[8] = gafColorKey
		fh[11] = frame.AlternateBlitter
		if len(plan.children) != 0 {
			fh[10] = byte(len(plan.children))
			for i, child := range plan.children {
				binary.LittleEndian.PutUint32(data[int(plan.dataOffset)+i*4:], child.header)
			}
		} else {
			if !plan.raw {
				fh[9] = 1
			}
			copy(data[plan.dataOffset:], plan.data)
		}
		binary.LittleEndian.PutUint32(fh[16:20], plan.dataOffset)
	}
	return data, nil
}

// encodeGAFRLE writes the per-row command stream of [fmt gaf]: a u16 payload
// length per row, then skip runs (bit 0 set, count in the upper seven bits),
// repeat runs (bit 1 set, count-1 in the upper six bits, one value byte) and
// literal runs (count-1 in the upper six bits, then the bytes).
func encodeGAFRLE(frame GAFWriteFrame) []byte {
	var out []byte
	w := int(frame.Width)
	for y := 0; y < int(frame.Height); y++ {
		row := frame.Pixels[y*w : (y+1)*w]
		var mask []bool
		if frame.Transparent != nil {
			mask = frame.Transparent[y*w : (y+1)*w]
		}
		transparent := func(x int) bool { return mask != nil && mask[x] }
		var payload []byte
		for x := 0; x < w; {
			if transparent(x) {
				n := 0
				for x+n < w && n < 127 && transparent(x+n) {
					n++
				}
				payload = append(payload, byte(n<<1)|1)
				x += n
				continue
			}
			n := 1
			for x+n < w && n < 64 && !transparent(x+n) && row[x+n] == row[x] {
				n++
			}
			if n >= 3 {
				payload = append(payload, byte((n-1)<<2)|2, row[x])
				x += n
				continue
			}
			// Literal run: stop before a transparent pixel or a run of three.
			n = 0
			for x+n < w && n < 64 && !transparent(x+n) {
				if n >= 1 && x+n+1 < w && row[x+n] == row[x+n-1] && !transparent(x+n+1) && row[x+n+1] == row[x+n] {
					n-- // leave the repeat for the next command
					break
				}
				n++
			}
			if n == 0 {
				n = 1
			}
			payload = append(payload, byte((n-1)<<2))
			payload = append(payload, row[x:x+n]...)
			x += n
		}
		out = binary.LittleEndian.AppendUint16(out, uint16(len(payload)))
		out = append(out, payload...)
	}
	return out
}
