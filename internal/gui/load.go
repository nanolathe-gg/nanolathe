package gui

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

const (
	logicalWidth  = 640 // logical design space [02 §1][07 §1]
	logicalHeight = 480
)

// CaptionTranslator supplies the parse-time caption lookup. GUI owns no
// language selection; the startup composition supplies the already-selected
// table [07 R-WGT-01 §11].
type CaptionTranslator interface {
	Translate(string) string
}

// Attribute bits the window builder writes [07 R-WGT-01 §12][07 R-WGT-01 §5].
const (
	// AttribInert is the bit the label arm sets on every label whose `link` is
	// empty, which is why plain caption labels never react; a kind-4 gadget
	// carrying it is inert and drawn darkened
	// [07 R-WGT-01 §12][07 R-WGT-01 §7][07 R-WGT-01 §5].
	AttribInert uint32 = 0x10
	// AttribSliderDecrement and AttribSliderIncrement are the attribute words
	// the builder gives a kind-4 gadget's two synthesized arrows: auto-repeat,
	// not focusable, arrow-minus and arrow-plus [07 R-WGT-01 §5].
	AttribSliderDecrement uint32 = 0x3400
	AttribSliderIncrement uint32 = 0x2c00
)

// fontDirectory is the interface context's font directory, set to `fonts` when
// the interface starts and joined to a kind-7 gadget's `filename` with a
// separator [07 R-WGT-01 §12][03 R-FONT-01 §5].
const fontDirectory = "fonts"

// sliderArtEntry is the GAF entry a kind-4 gadget's track, knob and arrows come
// from; the builder resolves it in the window's own GAF, then the common GAF
// [07 R-WGT-01 §5].
const sliderArtEntry = "SLIDERS"

// Load parses a .gui panel file from VFS and applies the window builder's
// per-kind arms [02 §6][07 R-WGT-01 §11][07 R-WGT-01 §12].
//
// Every record survives: the parser rejects no kind, and the builder's
// fourteen-entry switch only decides whether build work follows. What Load does
// is the parse ([COMMON] keys with their documented accessors and defaults, the
// per-kind key table), the rect sentinels and header clamp, the art resolution
// order, and the build arms that need no GAF handle. The arms that resolve
// frames — panel, listbox, text input, button, picture and the kind-4 synthesis
// of BuildSlider — are finished by the presentation layer, which holds the GAF.
func Load(fs vfs.FSOps, name string) (*Window, error) {
	return LoadWithTranslation(fs, name, nil)
}

// LoadWithTranslation parses a .GUI panel using the already-selected caption
// table. Retail localizes text for kinds 1, 3, 4 and 5 before window build,
// so accelerators inspect the localized bytes [07 R-WGT-01 §3][§11].
func LoadWithTranslation(fs vfs.FSOps, name string, captions CaptionTranslator) (*Window, error) {
	if fs == nil {
		return nil, fmt.Errorf("gui: nil VFS")
	}
	data, err := fs.ReadFileLimit(name, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("gui: %s: %w", name, err)
	}
	fGui, err := formats.LoadGUI(data)
	if err != nil {
		return nil, fmt.Errorf("gui: %s: %w", name, err)
	}
	if len(fGui.Gadgets) == 0 {
		return nil, fmt.Errorf("gui: %s: no gadgets", name)
	}

	w := &Window{
		Name:  name,
		Focus: -1,
		Header: Header{
			TotalGadgets: int16(fGui.Header.TotalGadgets),
			Panel:        fGui.Header.Panel,
			CrDefault:    fGui.Header.CrDefault,
			EscDefault:   fGui.Header.EscDefault,
			DefaultFocus: fGui.Header.DefaultFocus,
			HasVersion:   fGui.Header.HasVersion,
		},
	}
	if fGui.Header.HasVersion {
		w.Header.VersionMajor = uint8(fGui.Header.VersionMajor)
		w.Header.VersionMinor = uint8(fGui.Header.VersionMinor)
		w.Header.VersionRev = uint8(fGui.Header.VersionRev)
	}
	// BackTile fallback chain for panel background [07 §4].
	if w.Header.Panel == "" {
		w.Header.Panel = "BackTile"
	}

	// Convert each formats.Gadget to gui.Gadget with per-kind handling.
	// Preserve file order [07 §4]; numeric suffix in GADGETn is cosmetic — order matters.
	gadgets := make([]Gadget, 0, len(fGui.Gadgets))
	for idx, fg := range fGui.Gadgets {
		// A missing [COMMON] leaves retail's record bytes unspecified. The Go
		// compiler starts from its deterministic zero record and applies common
		// fields only when the subsection exists [02 R-MALF-01 §5][fmt gui
		// "COMMON"].
		g := Gadget{SourceName: fg.SourceName}
		kind := Kind(0)
		if fg.HasCommon {
			// `id` is read as an integer and stored as one byte — the low eight
			// bits — so the byte the builder and the service pass dispatch on is
			// `id mod 256` [07 R-WGT-01 §11].
			kind = Kind(fg.Common.ID & 0xFF)
			g = Gadget{
				Kind:          kind,
				Name:          fg.Common.Name,
				Assoc:         int32(fg.Common.Assoc),
				Attribs:       uint32(fg.Common.Attributes),
				ColorF:        uint16(fg.Common.ColorForeground & 0xFFFF), // masked to 16 bits [02 §6]
				ColorB:        uint16(fg.Common.ColorBackground & 0xFFFF),
				TextureNumber: uint8(fg.Common.TextureNumber & 0xFF), // stored as byte [02 §6]
				FontNumber:    uint8(fg.Common.FontNumber & 0xFF),
				Active:        uint8(fg.Common.Active & 0xFF), // byte [02 §6]
				CommonAttribs: uint8(fg.Common.CommonAttributes & 0xFF),
				Help:          fg.Common.Help,
				SourceName:    fg.SourceName,
			}
		}

		// No record is ever dropped. The parser rejects no kind: an unknown
		// kind keeps its [COMMON] fields [07 R-WGT-01 §11], and the builder's
		// fourteen-entry switch — indexed 0..13 after an unsigned "> 13"
		// bounds test — decides only whether any build work follows
		// [07 R-WGT-01 §12]. Eleven keys select ten arms: {0, 11} share the
		// panel arm and 1, 2, 3, 4, 5, 7, 8, 12, 13 have one each. Keys 6, 9,
		// 10 and every value above 13 do no build work — kind 6's surface
		// callback and kind 10's line painter need nothing built — and are
		// still serviced by the pass as usual.

		// gaffile belongs to COMMON; no common subsection means no store.
		if fg.HasCommon {
			g.GAFFile = int16(fg.Common.GAFFile)
		}

		// The name field is 16 bytes for every kind; the 127-byte cap doc 07 §4
		// attached to a text input's name is the `maxchars` cap, applied below
		// ([07 R-WGT-01 §12] correction to §4). Names are retained in full here
		// for diagnostics; every retail comparison is the 16-byte one.

		if fg.HasCommon {
			// Rect: xpos,ypos,width,height stored as int16 [02 §6]; stored widths honored [PLAN_12].
			// The record stores all four values as signed 16-bit values before
			// testing position sentinels, so authored 65535 becomes -1 and 65537
			// becomes 1 [02 §6].
			rawX := int32(int16(fg.Common.X))
			rawY := int32(int16(fg.Common.Y))
			rawW := int32(int16(fg.Common.Width))
			rawH := int32(int16(fg.Common.Height))
			g.Rect.RawX = rawX
			g.Rect.RawY = rawY
			g.Rect.W = rawW
			g.Rect.H = rawH
			x, y := rawX, rawY
			// Position sentinels -1 centres, -2 anchors to far edge [02 §6][07 §4].
			if x == -1 {
				x = (logicalWidth - rawW) / 2
			} else if x == -2 {
				x = logicalWidth - rawW
			}
			if y == -1 {
				y = (logicalHeight - rawH) / 2
			} else if y == -2 {
				y = logicalHeight - rawH
			}
			// First gadget (header) is clamped so interface stays on-screen [02 §6].
			if idx == 0 {
				if x < 0 {
					x = 0
				}
				if y < 0 {
					y = 0
				}
				if rawW > 0 && x+rawW > logicalWidth {
					x = logicalWidth - rawW
					if x < 0 {
						x = 0
					}
				}
				if rawH > 0 && y+rawH > logicalHeight {
					y = logicalHeight - rawH
					if y < 0 {
						y = 0
					}
				}
			}
			g.Rect.X = x
			g.Rect.Y = y
		}

		// Art resolution order: own named GAF entry first, then side-specific interface GAF, then built-in fallback [07 §4][02 §6].
		// Own entry is Name; panel header also uses Panel.
		if g.Name != "" {
			g.Art = g.Name
		}
		// Kind 11 shares the panel arm with kind 0 [07 R-WGT-01 §12].
		if (kind == KindPanel || kind == KindPanelAlias) && w.Header.Panel != "" {
			// Header panel art; if gadget's own art empty, use panel.
			if g.Art == "" {
				g.Art = w.Header.Panel
			}
			// The chain is closed: "each kind resolves its art from its own
			// named GAF entry first, then the side-specific interface GAF,
			// then the built-in fallback" [07 §4]. BackTile is that built-in
			// fallback for a panel.
			g.FallbackArt = "BackTile"
		} else if g.Art == "" {
			// For non-panel, fallback is empty; built-in may provide default art per type/size.
			// We keep FallbackArt empty and let ArtSources add BackTile only for panel.
		} else {
			// For non-panel with own art, set fallback to empty; side GAF will be tried at draw time.
			// A non-panel with its own art has no built-in fallback; the side
			// GAF is the only remaining link of [07 §4]'s chain and is tried at
			// draw time.
			g.FallbackArt = ""
		}

		// Per-kind type-specific keys [02 §6].
		// Use helper to read from fg.Fields which holds lowercased keys via formats.
		// Button keys: status, text, quickkey, grayedout, stages [02 §6].
		// Scrollbar keys: range, knobpos, knobsize, thick, text [02 §6].
		// List/text/compound: itemheight, maxchars, range, knobpos, knobsize, thick, text, link, filename, hotornot, nuttin [02 §6].
		g.Text = fieldString(fg.Fields, "text", "")
		if captions != nil && (kind == KindButton || kind == KindTextBox || kind == KindScrollBar || kind == KindLabel) {
			g.Text = captions.Translate(g.Text)
		}
		// staged buttons: | separated multi-line labels [07 §4]
		if kind == KindButton && g.Text != "" && strings.Contains(g.Text, "|") {
			g.Labels = strings.Split(g.Text, "|")
		} else if g.Text != "" {
			// Preserve single label as one entry for uniform consumers if needed; keep Labels nil for non-staged to avoid ambiguity.
			// We keep Labels nil when not staged; consumers can use Text directly.
		}
		g.Status = int16(fieldInt(fg.Fields, "status", 0))
		g.GrayedOut = int16(fieldInt(fg.Fields, "grayedout", 0))
		g.Stages = uint8(fieldInt(fg.Fields, "stages", 0) & 0xFF)
		// Keep the authored key until the runtime builder processes this
		// window record. Its collision fold changes ASCII A..Z only; extended
		// bytes compare with themselves [07 R-WGT-01 §3].
		// quickkey string stored as byte [02 §6]
		if v, ok := fg.Fields["quickkey"]; ok && v != "" {
			// In retail files quickkey is often numeric string like "83" or a bare symbol.
			// Try numeric parse first; if numeric, use low byte; else first byte of string.
			iv := formats.ParseTDFInteger(v)
			if iv != 0 || v == "0" {
				// Numeric: check if original string was numeric prefix (digits with optional sign)
				trim := strings.TrimSpace(v)
				isNumeric := len(trim) > 0 && (trim[0] >= '0' && trim[0] <= '9' || trim[0] == '-' || trim[0] == '+')
				if isNumeric {
					g.QuickKey = byte(iv & 0xFF)
				} else {
					g.QuickKey = v[0]
				}
			} else {
				g.QuickKey = v[0]
			}
		}
		g.Range = int16(fieldInt(fg.Fields, "range", 0))
		g.KnobPos = int16(fieldInt(fg.Fields, "knobpos", 0))
		g.KnobSize = int16(fieldInt(fg.Fields, "knobsize", 0))
		g.Thick = int32(fieldInt(fg.Fields, "thick", 0))
		g.Link = fieldString(fg.Fields, "link", "")
		g.FileName = fieldString(fg.Fields, "filename", "")
		g.HotOrNot = int32(fieldInt(fg.Fields, "hotornot", 0))
		g.Nuttin = int32(fieldInt(fg.Fields, "nuttin", 0))
		g.ItemHeight = int16(fieldInt(fg.Fields, "itemheight", 0))
		g.MaxChars = int16(fieldInt(fg.Fields, "maxchars", 0))
		// The parser caps `maxchars` at 128 and the builder caps it again at
		// 127 [07 R-WGT-01 §11][07 R-WGT-01 §12]. A kind-3 record therefore
		// never leaves the builder above 127.
		if kind == KindTextBox {
			if g.MaxChars > 127 {
				g.MaxChars = 127
			}
			if g.MaxChars < 0 {
				g.MaxChars = 0
			}
		}

		// The build arms that touch the parsed record [07 R-WGT-01 §12]. The
		// arms that resolve art (panel, listbox, text input, button, picture)
		// need a GAF handle the loader does not hold; their file-order and
		// naming halves are above and in ArtSources, and the frame lookup
		// itself belongs to the presentation layer.
		//
		// `colorf` is deliberately not zeroed here. It is not one colour with
		// one meaning: on the GAF path it is always a light-table row (the
		// keyed blitter's `mode`); on the FNT fallback path it is a GUIPAL map
		// index for a button and a raw physical palette index for a label —
		// never a palette index for a button, and never a map index for a
		// label. The service pass decays it (by 2 per timer tick for a
		// button, by 1 for a picture box), and the builder zeroes it for
		// every button, label and picture box at open
		// [07 R-WGT-01 §1][07 R-WGT-01 §12] [03 R-FONT-01 §6]. That makes it
		// runtime flash state rather than part of the authored record, so the
		// zeroing belongs to the panel instance, not to this compiled
		// definition. The authored ColorF this loader parses is therefore
		// dead on arrival for kinds 1 (button), 5 (label) and 12 (picture
		// box) either way — the picture-box painter draws no caption and
		// installs no colour at all — and presentation now reads it that
		// way ([03 R-FONT-01 §6] "The FNT foreground each painter installs").
		switch kind {
		case KindScrollBar:
			// The slider arm is BuildSlider below. It needs the SLIDERS entry —
			// the frame base's short-axis size, the knob cap's width and the
			// arrows' extent — and the loader holds no GAF handle, so nothing is
			// synthesized here: the record stays exactly as the parser produced
			// it and the presentation layer finishes it once art is resolved
			// [07 R-WGT-01 §5].
		case KindLabel:
			// The label arm: an empty `link` sets attribute 0x10, which is what
			// makes a plain caption label inert [07 R-WGT-01 §12][07 R-WGT-01 §7].
			if g.Link == "" {
				g.Attribs |= AttribInert
			}
		case KindPicture:
			// The picture arm zeroes the frame pointer and resolves frame 0 of
			// the entry named by the gadget, own GAF first then the common GAF
			// [07 R-WGT-01 §12]. ArtFrame is that pointer and Art is the name
			// (both set above); the lookup needs a GAF handle the loader does
			// not hold.
			g.ArtFrame = 0
		case KindFont:
			// The font arm loads the whole file `<font directory>\<filename>.FNT`
			// into the gadget's file slot. The directory is the interface
			// context's font directory, set to `fonts` when the interface starts;
			// the extension is appended verbatim to the authored name
			// [07 R-WGT-01 §12][03 R-FONT-01 §5].
			if g.FileName != "" {
				g.FilePath = fontDirectory + "/" + g.FileName + ".FNT"
			}
		case KindRawFile:
			// The raw-file arm opens the authored `filename` verbatim — no
			// directory, no extension — into the same slot as kind 7. Nothing
			// reads it afterwards [07 R-WGT-01 §12][07 R-WGT-01 §8].
			g.FilePath = g.FileName
		}

		gadgets = append(gadgets, g)
	}

	// Scrollbars associated with lists take range, knob size, position from the list — authored not trusted [07 §4] C7.
	// Apply after all gadgets collected so association scan sees all.
	assocList := make(map[int32]Gadget) // assoc -> listbox
	for _, g := range gadgets {
		if g.Kind == KindListBox && g.Assoc != 0 {
			assocList[g.Assoc] = g
		}
	}
	for i := range gadgets {
		if gadgets[i].Kind == KindScrollBar && gadgets[i].Assoc != 0 {
			if lb, ok := assocList[gadgets[i].Assoc]; ok {
				// Override with list's values [07 §4].
				gadgets[i].Range = lb.Range
				gadgets[i].KnobPos = lb.KnobPos
				gadgets[i].KnobSize = lb.KnobSize
			}
		}
	}

	// Set window rect from header gadget (first gadget, kind 0) [02 §6].
	if len(gadgets) > 0 && gadgets[0].Kind == KindPanel {
		w.Rect = gadgets[0].Rect
	} else if len(gadgets) > 0 {
		w.Rect = gadgets[0].Rect
	}
	w.OriginX = w.Rect.X
	w.OriginY = w.Rect.Y

	w.Gadgets = gadgets

	// Default focus: gadget name that starts focused [02 §6] defaultfocus.
	if w.Header.DefaultFocus != "" {
		w.Focus = w.GadgetIndex(w.Header.DefaultFocus)
	}

	return w, nil
}

// HitTest returns the index of the gadget the pointer is over, or -1.
//
// This is the service pass's hover test, and it skips only hidden gadgets: a
// greyed gadget still becomes the hovered gadget and still feeds `HELPTEXT`
// [07 R-WGT-01 §13][07 R-WGT-01 §1]. The grey bit belongs to press/fire time
// and is tested by Fires, never here.
//
// Gadgets are visited 1..N in index order — index 0 is the window's own header
// record, which the pass never visits — and every visit that hits replaces the
// hovered gadget, so where rectangles overlap the answer is the last hit in
// index order [07 R-WGT-01 §1 step 5]. Bounds are inclusive:
// gx <= x <= gx+w-1 and gy <= y <= gy+h-1 [07 §3].
func (w *Window) HitTest(x, y int32) int {
	if w == nil {
		return -1
	}
	hovered := -1
	for i, g := range w.Gadgets {
		if i == 0 || g.Kind == KindPanel {
			continue
		}
		if g.Active == 0 {
			continue // hidden gadgets are skipped before the hit test [07 R-WGT-01 §1]
		}
		r := w.PlacedRect(i)
		if r.W <= 0 || r.H <= 0 {
			continue
		}
		if x < r.X || x > r.X+r.W-1 {
			continue
		}
		if y < r.Y || y > r.Y+r.H-1 {
			continue
		}
		hovered = i
	}
	return hovered
}

// Fires reports whether a press or a quickkey on the gadget at index may
// capture and fire.
//
// "Greyed" is bit 0 of a per-gadget word that is not the `attribs` word —
// `attribs` has no greyed bit — written by the parser from `grayedout` and at
// run time by the grey/lock helpers. GrayedOut carries that word, and it is the
// whole test: the button handler returns on it as its first statement, before
// its own hit test, so a greyed button neither captures nor fires whether the
// press is a click or a quickkey [07 R-WGT-01 §13][07 R-WGT-01 §3]. A hidden
// gadget is not reachable at all [07 R-WGT-01 §1].
func (w *Window) Fires(index int) bool {
	if w == nil || index <= 0 || index >= len(w.Gadgets) {
		return false
	}
	g := w.Gadgets[index]
	return g.Kind != KindPanel && g.Active != 0 && g.GrayedOut == 0
}

// SliderArt carries the SLIDERS frame metrics the window builder reads when it
// finishes a kind-4 gadget [07 R-WGT-01 §5]. Frame indices are relative to the
// base returned by SliderFrameBase.
type SliderArt struct {
	// BaseExtent is the short-axis size of frame `base`, the track's end cap:
	// the builder replaces the gadget's short axis with it.
	BaseExtent int32
	// KnobExtent is the width of frame base+5, which a horizontal bar adopts
	// as its `knobsize`.
	KnobExtent int32
	// ArrowExtent is the long-axis size of frames base+6 and base+8, the two
	// arrows: their width on a horizontal bar, their height on a vertical one.
	ArrowExtent int32
	// ArrowCrossExtent is the arrows' own cross-axis size: a horizontal bar's
	// arrow height, a vertical bar's arrow width. The builder writes each
	// arrow's full rectangle — both axes — straight from that arrow's own
	// frame (base+6 for the decrement arrow, base+8 for the increment one),
	// never from the bar's own short axis (BaseExtent, above): the two reads
	// are independent, from different frames, made before either the
	// horizontal or vertical branch below touches the bar. In the shipped
	// `anims/commongui.gaf` SLIDERS entry the two arrows' cross-axis size
	// happens to equal each other and BaseExtent in both orientations
	// (16px), which is why one field carries it for the pair rather than
	// two [07 R-WGT-01 §5].
	ArrowCrossExtent int32
}

// SliderFrameBase returns the SLIDERS frame base for a kind-4 gadget's
// rectangle: 10 when the bar is horizontal (w > h), else 0 [07 R-WGT-01 §5].
func SliderFrameBase(r Rect) int32 {
	if r.W > r.H {
		return 10
	}
	return 0
}

// BuildSlider is the window builder's kind-4 arm [07 R-WGT-01 §5]. It returns
// the finished bar and the arrow gadgets the builder appends to the window.
//
// With no art (art nil — SLIDERS resolved in neither the window's own GAF nor
// the common GAF) the bar keeps its rectangle and takes travel
// `max(w, h) - 6`, and no arrows are appended.
//
// With art the short axis is replaced by the base frame's size and two BUTTON
// gadgets are appended, so the window's gadget count grows by two: frames
// base+6 and base+8, attributes 0x3400 (decrement) and 0x2c00 (increment), the
// bar's own `assoc` and `active`. Horizontal: the second arrow sits at
// `x + w - arrowW`, the bar then shrinks by `2*arrowW` and shifts right by
// `arrowW`, `knobsize` becomes the width of frame base+5 and travel becomes
// `w' - knobsize - 4` over the shrunken width. Vertical: the second arrow sits
// at `y + h - arrowH`, the bar shrinks and shifts likewise, and travel is left
// as authored — a vertical bar takes its travel from its list or the screen.
//
// Each arrow's cross-axis extent (a horizontal bar's arrow height, a vertical
// bar's arrow width) is that arrow's own frame's size on that axis — read
// directly from frame base+6 or base+8, not from the bar's short axis
// [07 R-WGT-01 §5], carried here as art.ArrowCrossExtent.
func BuildSlider(bar Gadget, art *SliderArt) (Gadget, []Gadget) {
	if art == nil {
		travel := bar.Rect.W
		if bar.Rect.H > travel {
			travel = bar.Rect.H
		}
		bar.Range = int16(travel - 6)
		return bar, nil
	}
	base := SliderFrameBase(bar.Rect)
	horizontal := bar.Rect.W > bar.Rect.H
	if horizontal {
		bar.Rect.H = art.BaseExtent
	} else {
		bar.Rect.W = art.BaseExtent
	}
	arrows := []Gadget{
		{
			Kind: KindButton, Assoc: bar.Assoc, Active: bar.Active,
			Attribs: AttribSliderDecrement, Art: sliderArtEntry, ArtFrame: base + 6,
			SourceName: bar.SourceName + "/arrow-decrement",
		},
		{
			Kind: KindButton, Assoc: bar.Assoc, Active: bar.Active,
			Attribs: AttribSliderIncrement, Art: sliderArtEntry, ArtFrame: base + 8,
			SourceName: bar.SourceName + "/arrow-increment",
		},
	}
	if horizontal {
		arrows[0].Rect = Rect{X: bar.Rect.X, Y: bar.Rect.Y, W: art.ArrowExtent, H: art.ArrowCrossExtent}
		arrows[1].Rect = Rect{X: bar.Rect.X + bar.Rect.W - art.ArrowExtent, Y: bar.Rect.Y, W: art.ArrowExtent, H: art.ArrowCrossExtent}
		bar.Rect.W -= 2 * art.ArrowExtent
		bar.Rect.X += art.ArrowExtent
		bar.KnobSize = int16(art.KnobExtent)
		bar.Range = int16(bar.Rect.W - int32(bar.KnobSize) - 4)
	} else {
		arrows[0].Rect = Rect{X: bar.Rect.X, Y: bar.Rect.Y, W: art.ArrowCrossExtent, H: art.ArrowExtent}
		arrows[1].Rect = Rect{X: bar.Rect.X, Y: bar.Rect.Y + bar.Rect.H - art.ArrowExtent, W: art.ArrowCrossExtent, H: art.ArrowExtent}
		bar.Rect.H -= 2 * art.ArrowExtent
		bar.Rect.Y += art.ArrowExtent
	}
	return bar, arrows
}

func fieldInt(m map[string]string, key string, def int) int {
	if v, ok := m[key]; ok {
		return int(formats.ParseTDFInteger(v))
	}
	return def
}

func fieldString(m map[string]string, key, def string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return def
}
