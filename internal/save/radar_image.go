package save

import "encoding/binary"

// The `Radar Image` box of the Summary account: an 8-byte header — a `u32`
// width and a `u32` height — followed by `height` rows of `width` palette
// bytes. A short read yields no image rather than an error, which is what the
// load screen's `RADAR` gadget does with a truncated box [08 R-SAVE-02 §3].
//
// The box is written on live-battle saves only and is never restored: the load
// dispatcher treats it as presentation for the list [08 "Summary"]
// [08 "Account inventory"].

// RadarImageHeaderSize is the width/height header that precedes the rows
// [08 R-SAVE-02 §3].
const RadarImageHeaderSize = 8

// EncodeRadarImage builds the box from an indexed raster of width*height
// palette bytes whose rows are `pitch` bytes apart. A raster that cannot
// supply every row yields no box, so a caller that has no picture writes
// nothing rather than a short box the reader would drop.
func EncodeRadarImage(width, height int, pixels []byte, pitch int) []byte {
	if width <= 0 || height <= 0 || pitch < width {
		return nil
	}
	if int64(height-1)*int64(pitch)+int64(width) > int64(len(pixels)) {
		return nil
	}
	box := make([]byte, RadarImageHeaderSize+width*height)
	binary.LittleEndian.PutUint32(box[0:], uint32(width))
	binary.LittleEndian.PutUint32(box[4:], uint32(height))
	for y := 0; y < height; y++ {
		copy(box[RadarImageHeaderSize+y*width:RadarImageHeaderSize+(y+1)*width], pixels[y*pitch:y*pitch+width])
	}
	return box
}

// DecodeRadarImage reads the header and returns the packed rows. A box whose
// declared extent is not fully present is a short read and yields no image
// [08 R-SAVE-02 §3]. The returned rows alias the box.
func DecodeRadarImage(box []byte) (width, height int, pixels []byte, ok bool) {
	if len(box) < RadarImageHeaderSize {
		return 0, 0, nil, false
	}
	w := int64(binary.LittleEndian.Uint32(box[0:]))
	h := int64(binary.LittleEndian.Uint32(box[4:]))
	if w <= 0 || h <= 0 {
		return 0, 0, nil, false
	}
	// The declared extent is bounded by the box itself rather than by an
	// invented maximum: a larger extent than the payload holds is the short
	// read the panel drops.
	if w > int64(len(box)) || h > int64(len(box)) || w*h > int64(len(box)-RadarImageHeaderSize) {
		return 0, 0, nil, false
	}
	return int(w), int(h), box[RadarImageHeaderSize : RadarImageHeaderSize+w*h], true
}
