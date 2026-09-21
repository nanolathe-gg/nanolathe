package ui

import (
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// Mode identifies an authored frontend surface or the two platform hand-off
// states. It is intentionally a UI value: the session is not mutated by a
// menu transition [07 "Retail closure for the single-player menu slice"].
type Mode uint8

const (
	ModeMain Mode = iota
	ModeSingle
	ModeMission
	ModeMap
	ModeSkirmish
	ModeLoading
	ModeBattle
)

// Frontend owns the mutable screen transition and panel stack. Configuration
// (map, campaign, and skirmish setup) remains with the composition root; only
// authored widget state lives here [07 §3][07 §4].
type Frontend struct {
	Mode   Mode
	Panels PanelStack
}

// NewFrontend returns a front end in mode with an empty panel stack.
func NewFrontend(mode Mode) *Frontend { return &Frontend{Mode: mode} }

// SetMode records the screen the front end is on without touching the stack.
func (f *Frontend) SetMode(mode Mode) {
	if f != nil {
		f.Mode = mode
	}
}

// Open replaces or saves-under the current authored screen. The caller
// supplies the parsed resource and the save-under decision from its authored
// window rectangle; this model never creates fallback geometry.
func (f *Frontend) Open(mode Mode, panel *Panel, saveUnder bool) {
	if f == nil {
		return
	}
	f.Mode = mode
	f.Panels.CloseModal()
	if panel == nil {
		f.Panels.Replace(nil)
		return
	}
	if saveUnder && f.Panels.Top() != nil {
		f.Panels.Push(panel)
		return
	}
	f.Panels.Replace(panel)
}

// ActivePanel is the panel input and drawing apply to: the panel under an open
// modal, otherwise the top of the stack [07 §3].
func (f *Frontend) ActivePanel() *Panel {
	if f == nil {
		return nil
	}
	if f.Panels.Modal() != nil {
		return f.Panels.Under()
	}
	return f.Panels.Top()
}

// Navigate resolves the fixed authored screen edges. Edges that depend on
// composition configuration (campaign choice, selected map, and loading)
// remain semantic actions for the caller; this method only owns transitions
// intrinsic to the frontend graph [07 "Retail closure for the single-player menu slice"].
func (f *Frontend) Navigate(name string) (Mode, bool) {
	if f == nil {
		return 0, false
	}
	switch gui.CallbackName(name) {
	case "SINGLE":
		if f.Mode == ModeMain {
			return ModeSingle, true
		}
	case "Skirmish":
		if f.Mode == ModeSingle {
			return ModeSkirmish, true
		}
	case "PrevMenu":
		switch f.Mode {
		case ModeSingle:
			return ModeMain, true
		case ModeMission:
			return ModeSingle, true
		case ModeSkirmish:
			return ModeSingle, true
		}
	}
	return 0, false
}

// SkirmishSlot supplies only authored display values to the runtime gadget
// builder. It is deliberately separate from session configuration so building
// a lobby cannot mutate authoritative simulation state.
type SkirmishSlot struct {
	Side  int
	Color int
}

const dynamicSkirmishSource = "RETAIL_DYNAMIC_SKIRMISH"

// InstallSkirmishDynamicGadgets appends the controls that retail creates
// after SKIRMISH.GUI is loaded. Their positions and art keys are the recovered
// authored runtime builder contract [07 §4]. Existing dynamic records are
// removed first so reopening the screen is idempotent.
func InstallSkirmishDynamicGadgets(window *gui.Window, slots []SkirmishSlot) {
	if window == nil {
		return
	}
	base := make([]gui.Gadget, 0, len(window.Gadgets)+len(slots)*6)
	for _, gadget := range window.Gadgets {
		if gadget.SourceName != dynamicSkirmishSource {
			base = append(base, gadget)
		}
	}
	window.Gadgets = base
	n := len(slots)
	if n < 1 {
		return
	}
	step := 200 / n
	rowY := (180-(n-1)*step)/2 + 79
	for i, slot := range slots {
		suffix := strconv.Itoa(i)
		side := dynamicButton("Side"+suffix, 163, rowY, 45, 20, "SIDEx", slot.Side)
		side.Stages = 2
		window.Gadgets = append(window.Gadgets,
			dynamicButton("Player"+suffix, 45, rowY, 112, 20, "skirmname", 0),
			side,
			dynamicSurface("Color"+suffix, 214, rowY, 20, 20, "32xlogos", slot.Color),
			dynamicSurface("Allies"+suffix, 241, rowY, 40, 20, "TEAMICONSx", 10),
			dynamicButton("Metal"+suffix, 286, rowY, 45, 20, "skirmmet", 0),
			dynamicButton("Energy"+suffix, 337, rowY, 45, 20, "skirmmet", 0),
		)
		rowY += step
	}
}

func dynamicButton(name string, x, y, w, h int, art string, status int) gui.Gadget {
	attribs := uint32(2)
	if art == "skirmmet" {
		attribs |= 0x10000
	}
	return gui.Gadget{Kind: gui.KindButton, Name: name, Rect: gui.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)}, Attribs: attribs, Active: 1, Status: int16(status), Art: art, SourceName: dynamicSkirmishSource}
}

// dynamicSurface builds the two clickable row surfaces, Color%d and Allies%d.
// Both are hot. That the retail row builder sets the hot word is a **Supported
// inference** forced by two Established facts: each name has a row callback the
// setup screen dispatches [08 R-SKIR-01 §1], and a surface takes a press — the
// only way it can fire — only while its hot word is 1 [07 R-WGT-01 §8]. The
// row builder's own write of the field is not itself traced
// [07 "Retail closure for the single-player menu slice"].
func dynamicSurface(name string, x, y, w, h int, art string, status int) gui.Gadget {
	return gui.Gadget{Kind: gui.KindSurface, Name: name, Rect: gui.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)}, Active: 1, HotOrNot: 1, Status: int16(status), Art: art, SourceName: dynamicSkirmishSource}
}
