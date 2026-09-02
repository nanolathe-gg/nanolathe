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

// Load parses a .gui panel file from VFS [02 §6][07 §4].
// It handles the twelve control-kind cases, [COMMON] keys with documented accessors/defaults,
// gadget rects with -1/-2 sentinel centering, attribs, art refs in established resolution order,
// and stored widths honored. Unhandled ids beyond the twelve are skipped per research.
func Load(fs vfs.FSOps, name string) (*Window, error) {
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
	gadgets := make([]Gadget, 0, len(fGui.Gadgets)+2) // +2 for slider synthesis
	for idx, fg := range fGui.Gadgets {
		kind := Kind(fg.Common.ID & 0xFF) // stored as byte, truncate [02 §6]

		// Twelve handled cases [07 §4]: background/panel, button, listbox, text input, slider,
		// text case, zeroing, two embedded-file, three single-purpose.
		// Known retail corpus uses ids {0,1,2,3,4,5,6,7,12} [fmt gui]; remaining three free ids 8,9,10
		// are mapped to slider/text/zeroing here. Unknown ids >15 are treated as unhandled and skipped.
		// [07 §4] closes WHICH twelve cases the control-kind byte selects among
		// — panel, button, listbox, text input, slider, a text case, an unnamed
		// zeroing case, two embedded-file cases and three single-purpose ones —
		// but not which byte value selects which of the last seven. The retail
		// corpus authors only {0,1,2,3,4,5,6,7,12} [fmt gui].
		// TODO(question): what byte value does the parser's control-kind switch
		// give each of the seven cases beyond the authored corpus? Decider: a
		// static trace of that switch's case table; the finding belongs in
		// [07 §4].
		isHandled := false
		switch kind {
		case KindPanel, KindButton, KindListBox, KindTextBox, KindScrollBar, KindLabel, KindSurface, KindFont, KindSlider, KindText, KindZero, KindPicture:
			isHandled = true
		case KindEmbedded1, KindRepeat, KindSingle1, KindSingle2:
			// These cover the remaining two embedded-file and two single-purpose slots; treat as handled for completeness.
			isHandled = true
		default:
			// Unhandled stored kinds have no established runtime family. Preserve
			// the parser's authored records only for the closed set above; do not
			// synthesize a generic widget for an unknown kind [07 §4].
			continue
		}
		if !isHandled {
			continue
		}

		g := Gadget{
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
		// gaffile is in COMMON but not parsed by formats.fillCommon; read from Fields map if present.
		if v, ok := fg.Fields["common.gaffile"]; ok {
			g.GAFFile = int16(formats.ParseTDFInteger(v))
		} else if v, ok := fg.Fields["gaffile"]; ok {
			g.GAFFile = int16(formats.ParseTDFInteger(v))
		}

		// Name cap for text input [07 §4]: name capped at 127 bytes.
		if kind == KindTextBox && len(g.Name) > 127 {
			g.Name = g.Name[:127]
		}

		// Rect: xpos,ypos,width,height stored as int16 [02 §6]; stored widths honored [PLAN_12].
		rawX := int32(fg.Common.X)
		rawY := int32(fg.Common.Y)
		rawW := int32(fg.Common.Width)
		rawH := int32(fg.Common.Height)
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

		// Art resolution order: own named GAF entry first, then side-specific interface GAF, then built-in fallback [07 §4][02 §6].
		// Own entry is Name; panel header also uses Panel.
		if g.Name != "" {
			g.Art = g.Name
		}
		if kind == KindPanel && w.Header.Panel != "" {
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
		// Text input maxchars capped at 128 [07 §4].
		if kind == KindTextBox && g.MaxChars > 128 {
			g.MaxChars = 128
		}
		if kind == KindTextBox && g.MaxChars < 0 {
			g.MaxChars = 0
		}
		// Handle common strings length limits: name 16 bytes, help empty, panel etc 16 bytes [02 §6].
		// We preserve full string for diagnostics but note truncation would be 16 for non-textbox.
		// For button text, quickkey, etc., no explicit limit stated beyond storage.

		// Unnamed zeroing case [07 §4]: zero out fields? Research says a text case and an unnamed zeroing case.
		if kind == KindZero {
			// TODO(question): which fields does the unnamed zeroing case clear?
			// [07 §4] names the case among the twelve and says nothing about its
			// body. Decider: a static trace of that case arm. Clearing the four
			// text-bearing fields and retaining the rect is the placeholder.
			g.Text = ""
			g.Labels = nil
			g.Link = ""
			g.FileName = ""
			// Keep rect/attribs as authored? The name implies zeroing, but we retain minimal.
		}

		// TODO(question): what do the two embedded-file cases and the three
		// single-purpose cases do? [07 §4] names all five among the twelve and
		// describes none. Decider: a static trace of those five case arms.
		// Retained as generic gadgets meanwhile.

		gadgets = append(gadgets, g)

		// Slider synthesizes two scrollbar child gadgets with derived knob travel [07 §4].
		if kind == KindSlider {
			// TODO(question): how does the slider derive its two scrollbar
			// children's knob travel and placement? [07 §4] establishes that it
			// "synthesizes two scrollbar child gadgets with derived knob
			// travel" and stops there. Decider: a static trace of the slider
			// case arm's child construction. Placeholder: two children sharing
			// assoc, split width, knob sizes halved.
			childW := rawW / 2
			if childW < 1 {
				childW = rawW
			}
			child1 := Gadget{
				Kind:       KindScrollBar,
				Name:       g.Name + "_S1",
				Assoc:      g.Assoc,
				Rect:       Rect{X: g.Rect.X, Y: g.Rect.Y, W: childW, H: rawH, RawX: rawX, RawY: rawY},
				Active:     g.Active,
				Range:      g.Range,
				KnobPos:    g.KnobPos,
				KnobSize:   g.KnobSize / 2,
				Thick:      g.Thick,
				Art:        g.Art + "_S1",
				SourceName: g.SourceName + "_SLIDER_CHILD0",
			}
			child2 := Gadget{
				Kind:       KindScrollBar,
				Name:       g.Name + "_S2",
				Assoc:      g.Assoc,
				Rect:       Rect{X: g.Rect.X + childW, Y: g.Rect.Y, W: rawW - childW, H: rawH, RawX: rawX, RawY: rawY},
				Active:     g.Active,
				Range:      g.Range,
				KnobPos:    g.KnobPos,
				KnobSize:   g.KnobSize - child1.KnobSize,
				Thick:      g.Thick,
				Art:        g.Art + "_S2",
				SourceName: g.SourceName + "_SLIDER_CHILD1",
			}
			gadgets = append(gadgets, child1, child2)
		}
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
		for idx, g := range w.Gadgets {
			if strings.EqualFold(g.Name, w.Header.DefaultFocus) {
				w.Focus = idx
				break
			}
		}
	}

	return w, nil
}

// HitTest returns the index of the gadget containing (x,y) inclusive; grayed/hidden reject [07 §3][07 §4].
// Returns -1 if none. Scan is in file order; header (KindPanel) is skipped as it is not interactive.
func (w *Window) HitTest(x, y int32) int {
	if w == nil {
		return -1
	}
	for i, g := range w.Gadgets {
		if g.Kind == KindPanel {
			continue
		}
		if g.Active == 0 {
			continue // hidden rejects [07 §3]
		}
		if g.GrayedOut != 0 {
			continue // grayed rejects [07 §3]
		}
		// Retail tests the grayed attribute bit before activation [07 §3]; the
		// GrayedOut field above carries it, and [07 §4] lists disabled/hidden
		// among the observed attributes without giving the attribs bit its own
		// number.
		// TODO(question): which attribs bit is the grayed bit, and is it tested
		// in addition to the grayedout key? Decider: a static trace of the
		// activation gate's attribute test [07 §3].
		// Inclusive bounds: gx <= x <= gx+w-1 && gy <= y <= gy+h-1 [07 §3].
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
		return i
	}
	return -1
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
