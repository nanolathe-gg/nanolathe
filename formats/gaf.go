package formats

import (
	"github.com/nanolathe-gg/nanolathe/vfs"
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
	// directRaster is an immutable view of authored storage for consumers that
	// bypass RLE decoding. Raw leaves use Pixels directly [02 R-MALF-01 §6].
	directRaster *GAFFrame
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
	meta, err := LoadGAFMetadataWithLimits(data, limits)
	if err != nil {
		return nil, err
	}
	return materializeGAF(data, meta)
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
// folding and unchanged high bytes match the inspected retail comparison
// [02 "Animation archive (GAF)"][fmt gaf].
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

// DirectRaster returns the selected frame's row-major storage without expanding
// RLE or composing children [02 R-MALF-01 §6]. The returned view is immutable.
// A false result is a host safety rejection, not a transparent retail image.
func (f *GAFFrame) DirectRaster() (*GAFFrame, bool) {
	if f == nil || f.SubframeCount != 0 || len(f.Subframes) != 0 {
		// TODO(question): composite direct readers sample runtime-relocated child
		// storage. Authored file bytes cannot determine those bytes; establish
		// a portable contract before admitting such a frame to a direct reader.
		return nil, false
	}
	if f.Compressed != 0 {
		return f.directRaster, f.directRaster != nil
	}
	if len(f.Pixels) < int(f.Width)*int(f.Height) {
		return nil, false
	}
	return f, true
}
