// Package chrome remasters the battle interface's button art at 2x for the
// Modern renderer's magnified chrome (DESIGN_GPU_RENDERER §14.9).
//
// A frame whose pixels match a known stock frame is redrawn as worn gunmetal
// from a procedural plate and a font, with its caption and lights taken from
// the element table. Any other frame in a covered entry — modded art, a new
// side's buttons, a localized caption — is enlarged from its own pixels by
// Scale2x, so a mod's art is never replaced by stock captions.
package chrome

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"strings"

	"golang.org/x/image/font/opentype"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/upscale"
)

// Version names the generated art; the cache key carries it, so a change to
// the look or the table invalidates old results.
const Version = 7

//go:embed font/SairaCondensed-800.ttf
var sairaCondensed []byte

// Options configures Bank2x. A nil Font uses the embedded Saira Condensed.
type Options struct {
	Font []byte
}

// Bank2x returns a bank parallel to bank — the same entry names and frame
// counts — whose covered frames are 2x variants. Uncovered slots are nil, which
// the client doubles itself (DESIGN_GPU_RENDERER §14.3).
func Bank2x(bank *formats.GAF, pal [256][3]uint8, opts Options) (*formats.GAF, error) {
	if bank == nil {
		return nil, errors.New("nanolathe: chrome remaster: no bank")
	}
	fontBytes := opts.Font
	if fontBytes == nil {
		fontBytes = sairaCondensed
	}
	face, err := opentype.Parse(fontBytes)
	if err != nil {
		return nil, fmt.Errorf("nanolathe: chrome remaster: font: %w", err)
	}
	s := &Style{Scale: 2, Light: Light{-1, 1}, Cool: 0.05, Grime: 0.6, CaptionFont: face, LabelFont: face}
	palette := make(color.Palette, 256)
	for i, c := range pal {
		palette[i] = color.NRGBA{c[0], c[1], c[2], 255}
	}
	out := &formats.GAF{Version: bank.Version, EntryCount: bank.EntryCount, Unknown: bank.Unknown,
		Entries: make([]formats.GAFEntry, len(bank.Entries))}
	for i := range bank.Entries {
		entry := &bank.Entries[i]
		out.Entries[i] = formats.GAFEntry{Name: entry.Name, FrameCount: entry.FrameCount,
			Unknown1: entry.Unknown1, Unknown2: entry.Unknown2,
			Frames: make([]formats.GAFFrameRef, len(entry.Frames))}
		spec, ok := lookup(entry.Name)
		if !ok {
			continue
		}
		for j, ref := range entry.Frames {
			f := ref.Frame
			if !plainFrame(f) {
				continue
			}
			var variant *formats.GAFFrame
			if known, ok := stockFrames[FrameHash(f)]; ok && strings.EqualFold(known.name, entry.Name) && known.frame == j {
				if known.keep {
					continue
				}
				if s.Material == nil {
					s.Material = Material()
				}
				variant = fromLayer(f, spec.draw(s, entry.Name, j, int(f.Width)), palette)
			} else {
				variant = scale2x(f)
			}
			out.Entries[i].Frames[j].Frame = variant
		}
	}
	return out, nil
}

// CachedBank2x is Bank2x through the shared upscale cache. The key covers
// Version, the font, the palette and every frame of bank.
func CachedBank2x(cache *upscale.Cache, bank *formats.GAF, pal [256][3]uint8, opts Options) (*formats.GAF, bool, error) {
	if bank == nil {
		return nil, false, errors.New("nanolathe: chrome remaster: no bank")
	}
	h := sha256.New()
	fmt.Fprintf(h, "nanolathe.upscale.chrome.%d\x00", Version)
	font := opts.Font
	if font == nil {
		font = sairaCondensed
	}
	fmt.Fprintf(h, "%d\x00", len(font))
	h.Write(font)
	for _, c := range pal {
		h.Write(c[:])
	}
	for _, entry := range bank.Entries {
		fmt.Fprintf(h, "%s\x00%d\x00", entry.Name, len(entry.Frames))
		for _, ref := range entry.Frames {
			if !plainFrame(ref.Frame) {
				h.Write([]byte{0})
				continue
			}
			fmt.Fprintf(h, "%s%d,%d,%d\x00", FrameHash(ref.Frame), ref.Frame.XOffset, ref.Frame.YOffset, ref.Frame.ColorKey)
		}
	}
	key := fmt.Sprintf("%x", h.Sum(nil))
	return cache.DerivedBank(key, func() (*formats.GAF, error) { return Bank2x(bank, pal, opts) })
}

// plainFrame reports whether f has one complete keyed raster the remaster can
// replace. A composite qualifies when every child takes the ordinary path:
// its plain raster is then the whole picture its leaves draw [fmt gaf].
func plainFrame(f *formats.GAFFrame) bool {
	if f == nil || f.Width == 0 || f.Height == 0 || len(f.PlainPixels) < int(f.Width)*int(f.Height) {
		return false
	}
	for _, child := range f.Subframes {
		if child == nil || child.AlternateBlitter != 0 || len(child.Subframes) != 0 {
			return false
		}
		// The plain raster crops a child to the parent's rectangle; its leaves
		// would not, so an overhanging child keeps the leaf walk.
		dx, dy := int(f.XOffset)-int(child.XOffset), int(f.YOffset)-int(child.YOffset)
		if dx < 0 || dy < 0 || dx+int(child.Width) > int(f.Width) || dy+int(child.Height) > int(f.Height) {
			return false
		}
	}
	return true
}

// FrameHash identifies a decoded frame by its size and pixels, transparency
// included. The stock table holds these, never the art itself.
func FrameHash(f *formats.GAFFrame) string {
	h := sha256.New()
	var hdr [4]byte
	binary.LittleEndian.PutUint16(hdr[0:], f.Width)
	binary.LittleEndian.PutUint16(hdr[2:], f.Height)
	h.Write(hdr[:])
	n := int(f.Width) * int(f.Height)
	h.Write(f.PlainPixels[:n])
	mask := make([]byte, n)
	for i := range n {
		if i < len(f.PlainTransparent) && f.PlainTransparent[i] {
			mask[i] = 1
		}
	}
	h.Write(mask)
	return fmt.Sprintf("%x", h.Sum(nil)[:12])
}

// fromLayer dithers a drawn element to the palette as the 2x variant of f.
// Opaque pixels never take f's colour key.
func fromLayer(f *formats.GAFFrame, l *Layer, pal color.Palette) *formats.GAFFrame {
	img := Dither(l, pal, f.ColorKey)
	out := doubledHeader(f)
	n := int(out.Width) * int(out.Height)
	out.Pixels, out.Transparent = make([]byte, n), make([]bool, n)
	for y := range int(out.Height) {
		for x := range int(out.Width) {
			i := y*int(out.Width) + x
			if x < l.W && y < l.H {
				out.Pixels[i] = img.ColorIndexAt(x, y)
				out.Transparent[i] = l.At(x, y).A < 0.5
			} else {
				out.Pixels[i], out.Transparent[i] = f.ColorKey, true
			}
		}
	}
	out.PlainPixels, out.PlainTransparent = out.Pixels, out.Transparent
	return out
}

// doubledHeader is f's header at 2x. Like the sprite remaster's frames it is
// a raw raster, whatever f's own encoding was.
func doubledHeader(f *formats.GAFFrame) *formats.GAFFrame {
	return &formats.GAFFrame{
		Width: f.Width * 2, Height: f.Height * 2,
		XOffset: f.XOffset * 2, YOffset: f.YOffset * 2,
		ColorKey: f.ColorKey,
		Unknown2: f.Unknown2, Unknown3: f.Unknown3,
	}
}

// scale2x enlarges f by the Scale2x rule: each pixel becomes a 2x2 block whose
// corners take a neighbour's index where two edge neighbours agree, which
// rounds diagonal steps in lettering without inventing colours.
func scale2x(f *formats.GAFFrame) *formats.GAFFrame {
	w, h := int(f.Width), int(f.Height)
	px := func(x, y int) int {
		x, y = min(max(x, 0), w-1), min(max(y, 0), h-1)
		i := y*w + x
		if i < len(f.PlainTransparent) && f.PlainTransparent[i] {
			return -1
		}
		return int(f.PlainPixels[i])
	}
	out := doubledHeader(f)
	ow := 2 * w
	pixels, transparent := make([]byte, ow*2*h), make([]bool, ow*2*h)
	set := func(x, y, v int) {
		i := y*ow + x
		if v < 0 {
			pixels[i], transparent[i] = f.ColorKey, true
			return
		}
		pixels[i] = byte(v)
	}
	for y := range h {
		for x := range w {
			e := px(x, y)
			b, d, ff, hh := px(x, y-1), px(x-1, y), px(x+1, y), px(x, y+1)
			e0, e1, e2, e3 := e, e, e, e
			if b != hh && d != ff {
				if d == b {
					e0 = d
				}
				if b == ff {
					e1 = ff
				}
				if d == hh {
					e2 = d
				}
				if hh == ff {
					e3 = ff
				}
			}
			set(2*x, 2*y, e0)
			set(2*x+1, 2*y, e1)
			set(2*x, 2*y+1, e2)
			set(2*x+1, 2*y+1, e3)
		}
	}
	out.Pixels, out.Transparent = pixels, transparent
	out.PlainPixels, out.PlainTransparent = pixels, transparent
	return out
}

// stockFrame names the stock frame a hash identifies. keep marks stock art
// with no gunmetal design, which stays nearest-doubled rather than taking the
// fallback meant for modded art.
type stockFrame struct {
	name  string
	frame int
	keep  bool
}
