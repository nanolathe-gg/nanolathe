package upscale

// The engine-facing sprite entry: a whole GAF bank in, a parallel 2x bank out
// [DESIGN_GPU_RENDERER §14.3, §14.4].

import (
	"errors"
	"math"
	"sync"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// FromGAFFrame reads one decoded GAF frame as a Sprite. Transparent is
// inverted: Sprite.Alpha marks the opaque pixels [fmt gaf].
func FromGAFFrame(f *formats.GAFFrame) *Sprite {
	s := &Sprite{W: int(f.Width), H: int(f.Height),
		Pix: make([]byte, int(f.Width)*int(f.Height)), Alpha: make([]bool, int(f.Width)*int(f.Height))}
	copy(s.Pix, f.Pixels)
	for i := range s.Alpha {
		s.Alpha[i] = !f.Transparent[i]
	}
	return s
}

// synthesizable reports whether a frame slot is one the sprite synthesizer
// covers. A composite frame is not: its children are separate leaves and the
// alternate-blitter ones are absent from the plain raster altogether, so the
// caller doubles it instead [fmt gaf].
func synthesizable(f *formats.GAFFrame) bool {
	return f != nil && f.Width > 0 && f.Height > 0 && f.SubframeCount == 0 && len(f.Subframes) == 0
}

// doubledOffset doubles an authored anchor offset, reporting false when the
// doubled value leaves the authored 16-bit range and the frame therefore has
// no representable 2x variant.
func doubledOffset(value int16) (int16, bool) {
	doubled := 2 * int32(value)
	if doubled < math.MinInt16 || doubled > math.MaxInt16 {
		return 0, false
	}
	return int16(doubled), true
}

// Bank2x returns a bank parallel to query: the same entry names, the same
// frame counts, and frames at 2x — width, height and the authored anchor
// offsets doubled, the same colour key, raw (uncompressed) pixels, and
// Pixels/Transparent at four times the count with PlainPixels and
// PlainTransparent aliasing them the way LoadGAFFile sets a plain frame
// [fmt gaf].
//
// examples supply the example blocks and are used exactly as given: passing
// the query bank itself is the usual example set, a family of related banks
// widens it, and a bank filtered of the entries whose colours leak into idle
// art (fire, explosion, smoke and reclaim art) narrows it. skip, when non-nil,
// names the query entries that are not synthesized; it does not touch the
// example set, so the map's shadow twins and the entries no feature names
// still lend their authored blocks to the entries that are synthesized
// (DESIGN_GPU_RENDERER §14.4). An earlier form removed skipped entries from
// the examples too, which shrank the example set to the handful of entries a
// map actually places.
//
// A frame slot the synthesizer does not cover keeps a nil Frame: an entry
// matched by skip, a frame with children or alternate leaves, an empty frame,
// or one whose doubled anchor would not fit its authored word. The caller
// doubles those itself [D2].
//
// Entries are independent, so opts.Workers changes only the elapsed time.
// Within an entry the frames run in order, each seeded from the previous
// frame's matches, which is what keeps an animation from flickering.
func Bank2x(query *formats.GAF, examples []*formats.GAF, pal [256][3]uint8, alp []byte,
	skip func(entryName string) bool, opts Options) (*formats.GAF, error) {
	if query == nil {
		return nil, errors.New("nanolathe: sprite upscale: no query bank")
	}
	if len(alp) != paletteSize*paletteSize {
		return nil, errors.New("nanolathe: sprite upscale: ALP table is not 65536 bytes")
	}
	skipped := func(name string) bool { return skip != nil && skip(name) }

	var db []*Sprite
	for _, bank := range examples {
		if bank == nil {
			continue
		}
		for entryIndex := range bank.Entries {
			entry := &bank.Entries[entryIndex]
			for frameIndex, ref := range entry.Frames {
				if ref.Frame == nil || ref.Frame.Width == 0 || ref.Frame.Height == 0 {
					continue
				}
				s := FromGAFFrame(ref.Frame)
				s.Name, s.Frame = entry.Name, frameIndex
				db = append(db, s)
			}
		}
	}
	if len(db) == 0 {
		return nil, errors.New("nanolathe: sprite upscale: the example banks hold no usable frames")
	}

	out := &formats.GAF{Version: query.Version, EntryCount: query.EntryCount, Unknown: query.Unknown,
		Entries: make([]formats.GAFEntry, len(query.Entries))}
	total := 0
	for entryIndex := range query.Entries {
		entry := &query.Entries[entryIndex]
		// Source offsets name bytes in the retail file, which a synthesized
		// bank has none of; the client pairs frames by entry and index [D2].
		out.Entries[entryIndex] = formats.GAFEntry{Name: entry.Name, FrameCount: entry.FrameCount,
			Unknown1: entry.Unknown1, Unknown2: entry.Unknown2,
			Frames: make([]formats.GAFFrameRef, len(entry.Frames))}
		if skipped(entry.Name) {
			continue
		}
		for _, ref := range entry.Frames {
			if synthesizable(ref.Frame) {
				total++
			}
		}
	}

	set := BuildSpriteExamples(db, pal, alp, DefaultSpriteParams())
	var progressLock sync.Mutex
	done := 0
	report := func() {
		if opts.Progress == nil {
			return
		}
		progressLock.Lock()
		done++
		opts.Progress(done, total)
		progressLock.Unlock()
	}

	parallel(len(query.Entries), opts.workers(), func(begin, end int) {
		for entryIndex := begin; entryIndex < end; entryIndex++ {
			entry := &query.Entries[entryIndex]
			if skipped(entry.Name) {
				continue
			}
			var previous *Sprite
			var previousMatches []int32
			for frameIndex, ref := range entry.Frames {
				if !synthesizable(ref.Frame) {
					continue
				}
				x, okX := doubledOffset(ref.Frame.XOffset)
				y, okY := doubledOffset(ref.Frame.YOffset)
				if !okX || !okY {
					continue
				}
				source := FromGAFFrame(ref.Frame)
				source.Name, source.Frame = entry.Name, frameIndex
				var seed []int32
				if previous != nil && previous.W == source.W && previous.H == source.H {
					seed = previousMatches
				}
				result, matches, _ := set.Upscale(source, seed)
				previous, previousMatches = source, matches

				frame := &formats.GAFFrame{
					Width: 2 * ref.Frame.Width, Height: 2 * ref.Frame.Height,
					XOffset: x, YOffset: y, ColorKey: ref.Frame.ColorKey, Compressed: 0,
					Pixels: result.Pix, Transparent: make([]bool, len(result.Alpha)),
				}
				for i, opaque := range result.Alpha {
					frame.Transparent[i] = !opaque
				}
				frame.PlainPixels = frame.Pixels
				frame.PlainTransparent = frame.Transparent
				out.Entries[entryIndex].Frames[frameIndex].Frame = frame
				report()
			}
		}
	})
	return out, nil
}
