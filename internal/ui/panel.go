package ui

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/gui"
)

// List is presentation state for one authored list gadget. Items are supplied
// by the owning screen; selection and scrolling do not mutate simulation.
type List struct {
	items    []string
	selected int
	top      int
}

// Items returns a read-only snapshot of the list's authored rows.
func (l *List) Items() []string {
	if l == nil {
		return nil
	}
	return append([]string(nil), l.items...)
}

// Len is the number of rows the list holds.
func (l *List) Len() int {
	if l == nil {
		return 0
	}
	return len(l.items)
}

// Selected is the currently selected row index.
func (l *List) Selected() int {
	if l == nil {
		return 0
	}
	return l.selected
}

// SetSelected records the selected row index.
func (l *List) SetSelected(selected int) {
	if l != nil {
		l.selected = selected
	}
}

// Top is the first visible row index.
func (l *List) Top() int {
	if l == nil {
		return 0
	}
	return l.top
}

// SetTop records the first visible row index.
func (l *List) SetTop(top int) {
	if l != nil {
		l.top = top
	}
}

// ListValues returns an immutable row snapshot and current presentation
// indices for a named list.
func (p *Panel) ListValues(name string) (items []string, selected, top int, ok bool) {
	return p.ListValuesAt(p.Index(name))
}

// ListValuesAt returns the row snapshot for one gadget record.
func (p *Panel) ListValuesAt(index int) (items []string, selected, top int, ok bool) {
	l := p.ListAt(index)
	if l == nil {
		return nil, 0, 0, false
	}
	return l.Items(), l.selected, l.top, true
}

// ListFor returns the runtime list object for geometry code that needs to
// preserve a thumb drag. Mutation of its indices should use SetTop and
// SetSelected; the row source remains private to Panel.
func (p *Panel) ListFor(name string) *List { return p.list(name) }

// Panel is one authored GUI window and its runtime presentation state. The
// Window is the authored geometry/art record; runtime focus, pressed state,
// list scrolling, and list selection belong here [07 §3][07 §4].
type Panel struct {
	Window *gui.Window
	// Each authored record owns its state even when names are identical.
	// Named operations resolve the first matching record [07 R-FE-02 §5].
	states []gadgetState

	focus        int
	pressed      int
	rightPressed int
	drag         scrollDrag
	editor       editorState
	message      string

	// flash is the per-gadget `colorf` word held as runtime state, indexed by
	// authored gadget index. `colorf` is not a colour for a button, a label or
	// a picture box: the painter passes it as the light-table row of the keyed
	// blitter and the service pass decays it, so it is flash state belonging to
	// the instance rather than to the compiled definition
	// [07 R-WGT-01 §1][07 R-WGT-01 §12][03 R-FONT-01 §6].
	flash []uint16
}

type gadgetState struct {
	text, help string
	active     bool
	status     int
	list       *List
}

// ActionKind identifies the presentation event emitted by an authored panel.
// The frontend interprets the gadget name; no simulation object crosses this
// boundary [07 §3][07 §4].
type ActionKind uint8

const (
	ActionNone ActionKind = iota
	ActionActivate
)

// Action is the semantic result of a completed panel gesture.
type Action struct {
	Kind   ActionKind
	Gadget string
	Index  int
}

// scrollDrag is UI-owned pointer capture for an associated list scrollbar.
// Geometry is supplied by the authored renderer; list mutation remains here.
type scrollDrag struct {
	active     bool
	vertical   bool
	startCoord int32
	startTop   int
	maxTop     int
	travel     int
	list       int
}

// PanelEntry is one object in the authored panel stack. Modal entries are
// drawn above the saved-under panel and removed as one unit on close [07 §3].
type PanelEntry struct {
	Panel *Panel
	Modal bool
}

// PanelStack owns frontend windows and their save-under relationship. The
// caller supplies authored panels; this type owns which one is active.
type PanelStack struct {
	entries []PanelEntry
}

// Stack is the short public name used by frontend callers.
type Stack = PanelStack

// Replace discards the whole stack and makes panel the only entry. A nil
// panel empties the stack.
func (s *PanelStack) Replace(panel *Panel) {
	if s == nil {
		return
	}
	s.entries = s.entries[:0]
	if panel != nil {
		s.entries = append(s.entries, PanelEntry{Panel: panel})
	}
}

// Push opens panel above the current entry, which is saved under it [07 §3].
func (s *PanelStack) Push(panel *Panel) {
	if s != nil && panel != nil {
		s.entries = append(s.entries, PanelEntry{Panel: panel})
	}
}

// PushModal opens panel above the current entry and marks it modal, so input
// stops at it [07 §3].
func (s *PanelStack) PushModal(panel *Panel) {
	if s != nil && panel != nil {
		s.entries = append(s.entries, PanelEntry{Panel: panel, Modal: true})
	}
}

// Top is the active panel, or nil when the stack is empty.
func (s *PanelStack) Top() *Panel {
	if s == nil || len(s.entries) == 0 {
		return nil
	}
	return s.entries[len(s.entries)-1].Panel
}

// Under is the panel beneath the active one, or nil when there is none.
func (s *PanelStack) Under() *Panel {
	if s == nil || len(s.entries) < 2 {
		return nil
	}
	return s.entries[len(s.entries)-2].Panel
}

// SaveUnder is the panel restored/drawn beneath the active entry [07 §3].
func (s *PanelStack) SaveUnder() *Panel { return s.Under() }

// Modal is the active panel when it is modal, nil otherwise.
func (s *PanelStack) Modal() *Panel {
	if s == nil || len(s.entries) == 0 || !s.entries[len(s.entries)-1].Modal {
		return nil
	}
	return s.entries[len(s.entries)-1].Panel
}

// CloseModal pops the active panel when it is modal and returns it; it
// returns nil and pops nothing otherwise.
func (s *PanelStack) CloseModal() *Panel {
	if s == nil || len(s.entries) == 0 || !s.entries[len(s.entries)-1].Modal {
		return nil
	}
	last := len(s.entries) - 1
	p := s.entries[last].Panel
	if p != nil {
		p.ResetPress()
		p.CancelScrollDrag()
	}
	s.entries = s.entries[:last]
	return p
}

// Pop closes the active stack entry. Modal callers should prefer
// CloseModal, which refuses to remove a non-modal screen [07 §3].
func (s *PanelStack) Pop() *Panel {
	if s == nil || len(s.entries) == 0 {
		return nil
	}
	last := len(s.entries) - 1
	p := s.entries[last].Panel
	if p != nil {
		p.ResetPress()
		p.CancelScrollDrag()
	}
	s.entries = s.entries[:last]
	return p
}

// Clear empties the stack.
func (s *PanelStack) Clear() {
	if s != nil {
		s.entries = s.entries[:0]
	}
}

// Len is the number of entries on the stack.
func (s *PanelStack) Len() int {
	if s == nil {
		return 0
	}
	return len(s.entries)
}

// Entries returns a snapshot in draw order, preserving save-under panels for
// the presentation compositor without exposing the stack's backing slice.
func (s *PanelStack) Entries() []PanelEntry {
	if s == nil {
		return nil
	}
	return append([]PanelEntry(nil), s.entries...)
}

// NewPanel builds the mutable state for one authored window: its focus,
// press indices and the per-gadget text, help, active, status and list state
// [07 §5] [07 R-WGT-01 §1].
func NewPanel(window *gui.Window) *Panel {
	p := &Panel{Window: window, focus: -1, pressed: -1, rightPressed: -1, editor: editorState{captured: -1}}
	if window == nil {
		return p
	}
	// The window builder zeroes `colorf` for every button, label and picture
	// box at open; every other kind keeps the authored word, which for a
	// listbox and a text input really is a colour-table entry
	// [07 R-WGT-01 §1][07 R-WGT-01 §12][07 R-WGT-01 §4].
	p.flash = make([]uint16, len(window.Gadgets))
	for i, gadget := range window.Gadgets {
		switch gadget.Kind {
		case gui.KindButton, gui.KindLabel, gui.KindPicture:
			p.flash[i] = 0
		default:
			p.flash[i] = gadget.ColorF
		}
	}
	p.states = make([]gadgetState, len(window.Gadgets))
	for i, gadget := range window.Gadgets {
		p.states[i] = gadgetState{text: gadget.Text, help: gadget.Help, active: gadget.Active != 0, status: int(gadget.Status)}
		if gadget.Kind == gui.KindListBox {
			p.states[i].list = &List{}
		}
	}
	if window.Header.DefaultFocus == "" {
		// The empty authored default starts at the header and traverses forward
		// after runtime activity has been installed [07 R-WGT-01 §2].
		p.focus = 0
		p.MoveFocus(FocusForward)
	} else if window.Focus >= 0 && window.Focus < len(window.Gadgets) {
		// A nonempty default retains the compiler's exact first-match result,
		// including its missing-name -1 result [07 R-FE-01 §12].
		p.focus = window.Focus
	}

	return p
}

// Key is the legacy screen-action normalization. Gadget lookup uses Index.
// TODO(I16): migrate screen callback comparisons to [07 R-FE-02 §5].
func Key(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// ActiveOf reports the active flag recorded for a named control.
func (p *Panel) ActiveOf(name string) bool { return p.ActiveAt(p.Index(name)) }

// SetFocus records the focused gadget index; -1 is no focus.
func (p *Panel) SetFocus(index int) {
	if p == nil {
		return
	}
	if p.Window != nil && index >= 0 && index < len(p.Window.Gadgets) && p.Window.Gadgets[index].Kind == gui.KindTextBox {
		if p.FocusEditor(index) {
			return
		}
	}
	p.focus = index
	if p.editor.captured >= 0 && p.editor.captured != index {
		p.editor.captured = -1
	}
}

// Focused is the focused gadget index, -1 when none.
func (p *Panel) Focused() int {
	if p == nil {
		return -1
	}
	return p.focus
}

// PressedIndex is the gadget the left button is held on, -1 when none.
func (p *Panel) PressedIndex() int {
	if p == nil {
		return -1
	}
	return p.pressed
}

// SetPressed records the gadget the left button is held on.
func (p *Panel) SetPressed(index int) {
	if p != nil {
		p.pressed = index
	}
}

// RightPressedIndex is the gadget the right button is held on, -1 when none.
func (p *Panel) RightPressedIndex() int {
	if p == nil {
		return -1
	}
	return p.rightPressed
}

// SetRightPressed records the gadget the right button is held on.
func (p *Panel) SetRightPressed(index int) {
	if p != nil {
		p.rightPressed = index
	}
}

// ResetPress clears both held-button indices.
func (p *Panel) ResetPress() {
	if p != nil {
		p.pressed = -1
		p.rightPressed = -1
	}
}

// ReleaseAction completes release-inside activation and returns a semantic
// gadget action. It always clears the press latch [07 §3].
func (p *Panel) ReleaseAction(x, y int32) Action {
	idx, ok := p.Release(x, y)
	if !ok || p == nil || p.Window == nil {
		return Action{Kind: ActionNone, Index: -1}
	}
	gadget := p.Window.Gadgets[idx]
	if gadget.Kind == gui.KindLabel && gadget.Link != "" {
		if target := p.Index(gadget.Link); target >= 0 {
			candidate := p.Window.Gadgets[target]
			if !p.ActiveAt(target) {
				return Action{Kind: ActionNone, Index: -1}
			}
			if candidate.Kind == gui.KindButton {
				if candidate.GrayedOut != 0 {
					return Action{Kind: ActionNone, Index: -1}
				}
				p.SetFocus(target)
				return Action{Kind: ActionActivate, Gadget: candidate.Name, Index: target}
			}
			p.SetFocus(target)
			return Action{Kind: ActionNone, Index: -1}
		}
	}
	return Action{Kind: ActionActivate, Gadget: gadget.Name, Index: idx}
}

// BeginScrollDrag captures a scrollbar thumb. The associated list is found
// from the authored gadget record, keeping drag state out of cmd/frontend.
func (p *Panel) BeginScrollDrag(index int, vertical bool, coordinate int32, maxTop, travel int) bool {
	if p == nil || p.Window == nil || index < 0 || index >= len(p.Window.Gadgets) || travel <= 0 || maxTop <= 0 {
		return false
	}
	g := p.Window.Gadgets[index]
	if g.Kind != gui.KindScrollBar {
		return false
	}
	listIndex := -1
	for i, list := range p.Window.Gadgets {
		if i > 0 && list.Kind == gui.KindListBox && list.Assoc == g.Assoc {
			listIndex = i
			break
		}
	}
	if p.ListAt(listIndex) == nil {
		return false
	}
	p.drag = scrollDrag{active: true, vertical: vertical, startCoord: coordinate, startTop: p.ListAt(listIndex).Top(), maxTop: maxTop, travel: travel, list: listIndex}
	return true
}

// ScrollDragging reports whether the panel owns an active thumb capture.
func (p *Panel) ScrollDragging() bool { return p != nil && p.drag.active }

// UpdateScrollDrag maps pointer displacement to the associated list origin.
// Integer division deliberately preserves retail truncation toward zero.
func (p *Panel) UpdateScrollDrag(x, y int32, held bool) bool {
	if p == nil || !p.drag.active {
		return false
	}
	if !held {
		p.drag = scrollDrag{}
		return false
	}
	coordinate := x
	if p.drag.vertical {
		coordinate = y
	}
	d := int(coordinate - p.drag.startCoord)
	top := p.drag.startTop + d*p.drag.maxTop/p.drag.travel
	p.SetListTopAt(p.drag.list, top, p.drag.maxTop)
	return true
}

// CancelScrollDrag abandons an in-progress scrollbar thumb drag.
func (p *Panel) CancelScrollDrag() {
	if p != nil {
		p.drag = scrollDrag{}
	}
}

// SetMessage records the modal message this panel is showing so a caller can
// read it back.
//
// It no longer binds the text to an authored control. `MSGBOX.GUI` authors only
// its panel and its `OK` button; the message box's `TEXT` labels are appended by
// the opener, one per wrapped line, so the message already lives in those
// gadgets' own text before a Panel is built over the window [07 R-FE-01 §9].
// Searching for an authored label could only ever fail, and that failure used to
// swallow the very diagnostic the box exists to show.
func (p *Panel) SetMessage(message string) {
	if p == nil {
		return
	}
	p.message = message
}

// Message returns the modal message recorded by SetMessage.
func (p *Panel) Message() string {
	if p == nil {
		return ""
	}
	return p.message
}

// Index finds the first matching authored record after the window header.
// Comparison is byte-exact, stops at a terminator and examines at most 16
// bytes; case and whitespace are significant [07 R-FE-02 §5].
func (p *Panel) Index(name string) int {
	if p == nil {
		return -1
	}
	return p.Window.GadgetIndex(name)
}

func (p *Panel) stateAt(index int) *gadgetState {
	if p == nil || index < 0 || index >= len(p.states) {
		return nil
	}
	return &p.states[index]
}

func (p *Panel) ActiveAt(index int) bool { s := p.stateAt(index); return s != nil && s.active }
func (p *Panel) SetActiveAt(index int, active bool) {
	if s := p.stateAt(index); s != nil {
		s.active = active
		if !active && index == p.focus {
			p.MoveFocus(FocusForward)
		}
	}
}
func (p *Panel) SetActive(name string, active bool) { p.SetActiveAt(p.Index(name), active) }
func (p *Panel) StatusAt(index int) int {
	if s := p.stateAt(index); s != nil {
		return s.status
	}
	return 0
}
func (p *Panel) SetStatusAt(index, status int) {
	if s := p.stateAt(index); s != nil {
		s.status = status
	}
}
func (p *Panel) StatusOf(name string) int          { return p.StatusAt(p.Index(name)) }
func (p *Panel) SetStatus(name string, status int) { p.SetStatusAt(p.Index(name), status) }
func (p *Panel) TextAt(index int) string {
	if s := p.stateAt(index); s != nil {
		return s.text
	}
	return ""
}
func (p *Panel) SetTextAt(index int, value string) {
	if s := p.stateAt(index); s != nil {
		s.text = value
		if p.editor.captured == index {
			p.editor.caret = len(value)
		}
	}
}
func (p *Panel) SetText(name, value string) { p.SetTextAt(p.Index(name), value) }
func (p *Panel) TextOf(name string) string  { return p.TextAt(p.Index(name)) }

func (p *Panel) HelpAt(index int) string {
	if s := p.stateAt(index); s != nil {
		return s.help
	}
	return ""
}
func (p *Panel) SetHelpAt(index int, value string) {
	if s := p.stateAt(index); s != nil {
		s.help = value
	}
}
func (p *Panel) SetHelp(name, value string) { p.SetHelpAt(p.Index(name), value) }
func (p *Panel) HelpOf(name string) string  { return p.HelpAt(p.Index(name)) }
func (p *Panel) ListAt(index int) *List {
	if s := p.stateAt(index); s != nil {
		return s.list
	}
	return nil
}

// FlashRow returns the gadget's light-table row — the runtime `colorf` word.
// It is zero for a button, a label and a picture box until a screen sets one,
// because the builder zeroes those three at open [07 R-WGT-01 §1].
func (p *Panel) FlashRow(index int) uint16 {
	if p == nil || index < 0 || index >= len(p.flash) {
		return 0
	}
	return p.flash[index]
}

// SetFlashRow is the gadget-colour setter screens use to make a control flash
// and fade. The value is a light-table row, not a palette index
// [07 R-WGT-01 §1][03 R-FONT-01 §6].
func (p *Panel) SetFlashRow(index int, row uint16) {
	if p != nil && index >= 0 && index < len(p.flash) {
		p.flash[index] = row
	}
}

// DecayFlash is the service pass's once-per-timer-tick flash decay: 2 per tick
// for a button, 1 for a picture box, both clamped at zero. No other kind
// decays [07 R-WGT-01 §1].
func (p *Panel) DecayFlash() {
	if p == nil || p.Window == nil {
		return
	}
	for i := range p.flash {
		if i >= len(p.Window.Gadgets) {
			break
		}
		var step uint16
		switch p.Window.Gadgets[i].Kind {
		case gui.KindButton:
			step = 2
		case gui.KindPicture:
			step = 1
		default:
			continue
		}
		if p.flash[i] <= step {
			p.flash[i] = 0
			continue
		}
		p.flash[i] -= step
	}
}

// HitTest is the service pass's hover test. Gadgets are visited in index
// order, index 0 being the window's own header record the pass never visits;
// hidden gadgets are skipped before the inclusive rectangle test and every
// later hit replaces the earlier one, so where rectangles overlap the hovered
// gadget is the **last** hit [07 R-WGT-01 §1 step 5].
//
// A greyed gadget is still hovered: the grey bit is tested at press/fire time,
// not here, which is how HELPTEXT shows help for a greyed button
// [07 R-WGT-01 §13]. Use Fires or PressTest for the press-time answer.
func (p *Panel) HitTest(x, y int32) int {
	if p == nil || p.Window == nil {
		return -1
	}
	hovered := -1
	for i, gadget := range p.Window.Gadgets {
		if i == 0 || gadget.Kind == gui.KindPanel || !p.ActiveAt(i) {
			continue
		}
		r := p.Window.PlacedRect(i)
		if r.W <= 0 || r.H <= 0 || x < r.X || y < r.Y || x > r.X+r.W-1 || y > r.Y+r.H-1 {
			continue
		}
		hovered = i
	}
	return hovered
}

// Fires reports whether a press or a quickkey on the gadget may capture and
// fire. "Greyed" is bit 0 of a per-gadget word that is not `attribs`; the
// button handler returns on it as its first statement, before its own hit
// test, so a greyed gadget neither captures nor fires
// [07 R-WGT-01 §13][07 R-WGT-01 §3]. Active is the panel's runtime visibility,
// which screens change after the window was compiled.
func (p *Panel) Fires(index int) bool {
	if p == nil || p.Window == nil || index <= 0 || index >= len(p.Window.Gadgets) {
		return false
	}
	g := p.Window.Gadgets[index]
	return kindHasPressHandler(g) && p.ActiveAt(index) && g.GrayedOut == 0
}

// kindHasPressHandler reports whether a gadget kind's handler accepts a press
// at all. Buttons [07 R-WGT-01 §3], lists [07 R-WGT-01 §4], sliders
// [07 R-WGT-01 §5] and text inputs [07 R-WGT-01 §6] do; a label does unless
// it is inert — attribute 0x10 with no quickkey, which is every label whose
// `link` is empty [07 R-WGT-01 §7]; a blank surface does only while its
// `hotornot` word is 1 [07 R-WGT-01 §8]. A picture box blits its frame and
// returns, a line gadget only draws, and the panel, font and raw-file kinds
// have no handler [07 R-WGT-01 §8][07 R-WGT-01 §12] — so none of them can
// take the capture, and a plate authored in front of a column of buttons
// (the in-battle PREFS.GUI does this) leaves the press to the buttons.
func kindHasPressHandler(g gui.Gadget) bool {
	switch g.Kind {
	case gui.KindButton, gui.KindListBox, gui.KindTextBox, gui.KindScrollBar:
		return true
	case gui.KindLabel:
		return g.Attribs&gui.AttribInert == 0 || g.QuickKey != 0
	case gui.KindSurface:
		return g.HotOrNot == 1
	}
	return false
}

// PressTest returns the gadget that takes the pointer capture, or -1.
//
// Exactly one gadget can hold the capture and a take is refused while another
// non-text gadget holds it, so within one pass the capture goes to the
// **first** gadget in index order whose handler accepts the press — greyed
// gadgets return before their own hit test and so are passed over
// [07 R-WGT-01 §1 "Capture"][07 R-WGT-01 §13]. That is the opposite end of the
// index order from HitTest's hover answer.
func (p *Panel) PressTest(x, y int32) int {
	if p == nil || p.Window == nil {
		return -1
	}
	for i := range p.Window.Gadgets {
		if i == 0 || !p.Fires(i) {
			continue
		}
		r := p.Window.PlacedRect(i)
		if r.W <= 0 || r.H <= 0 || x < r.X || y < r.Y || x > r.X+r.W-1 || y > r.Y+r.H-1 {
			continue
		}
		return i
	}
	return -1
}

// Press begins the release-inside gesture and updates focus. It returns the
// authored gadget index or -1 when the press is outside a control that can
// take the capture.
func (p *Panel) Press(x, y int32) int {
	if p == nil {
		return -1
	}
	p.pressed = p.PressTest(x, y)
	if p.pressed >= 0 {
		p.focus = p.pressed
	}
	return p.pressed
}

// Release completes a gesture only when the same authored gadget is still
// under the pointer. It always clears the pressed latch.
func (p *Panel) Release(x, y int32) (int, bool) {
	if p == nil {
		return -1, false
	}
	pressed := p.pressed
	p.pressed = -1
	if pressed < 0 || p.PressTest(x, y) != pressed || pressed >= len(p.Window.Gadgets) {
		return -1, false
	}
	return pressed, true
}

// SetList replaces rows and clamps selection/top to the list's range.
func (p *Panel) SetList(name string, items []string) { p.SetListAt(p.Index(name), items) }

// SetListAt replaces the rows owned by one gadget record.
func (p *Panel) SetListAt(index int, items []string) {
	state := p.stateAt(index)
	if state == nil {
		return
	}
	l := state.list
	if l == nil {
		l = &List{}
		state.list = l
	}
	l.items = append(l.items[:0], items...)
	if len(l.items) == 0 {
		l.selected, l.top = 0, 0
		return
	}
	if l.selected >= len(l.items) {
		l.selected = len(l.items) - 1
	}
	if l.selected < 0 {
		l.selected = 0
	}
	if l.top >= len(l.items) {
		l.top = len(l.items) - 1
	}
	if l.top < 0 {
		l.top = 0
	}
}

// SetListSelection updates selection and keeps it inside visible rows.
func (p *Panel) SetListSelection(name string, selected, visibleRows int) bool {
	return p.SetListSelectionAt(p.Index(name), selected, visibleRows)
}

// SetListSelectionAt updates the list owned by one gadget record.
func (p *Panel) SetListSelectionAt(index int, selected, visibleRows int) bool {
	l := p.ListAt(index)
	if l == nil || len(l.items) == 0 {
		return false
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(l.items) {
		selected = len(l.items) - 1
	}
	if visibleRows < 1 {
		visibleRows = 1
	}
	l.selected = selected
	p.clampList(l, visibleRows)
	return true
}

// ScrollList changes a list's top row and clamps to items-visibleRows. It
// returns the resulting top row and whether a list was found.
func (p *Panel) ScrollList(name string, delta, visibleRows int) (int, bool) {
	return p.ScrollListAt(p.Index(name), delta, visibleRows)
}

// ScrollListAt updates the list owned by one gadget record.
func (p *Panel) ScrollListAt(index int, delta, visibleRows int) (int, bool) {
	l := p.ListAt(index)
	if l == nil || len(l.items) == 0 {
		return 0, false
	}
	l.top += delta
	p.clampListRange(l, visibleRows)
	return l.top, true
}

// SetListTop sets a list's scroll origin and clamps it to the supplied
// geometry-derived maximum top row. Callers that have row geometry should
// derive maxTop from items-visibleRows before calling this method.
func (p *Panel) SetListTop(name string, top, maxTop int) bool {
	return p.SetListTopAt(p.Index(name), top, maxTop)
}

// SetListTopAt updates the list owned by one gadget record.
func (p *Panel) SetListTopAt(index int, top, maxTop int) bool {
	l := p.ListAt(index)
	if l == nil {
		return false
	}
	if len(l.items) == 0 {
		l.top = 0
		return true
	}
	if maxTop < 0 {
		maxTop = 0
	}
	if maxTop >= len(l.items) {
		maxTop = len(l.items) - 1
	}
	l.top = top
	if l.top < 0 {
		l.top = 0
	}
	if l.top > maxTop {
		l.top = maxTop
	}
	return true
}

func (p *Panel) list(name string) *List {
	if p == nil {
		return nil
	}
	return p.ListAt(p.Index(name))
}

func (p *Panel) clampList(l *List, visibleRows int) {
	p.clampListRange(l, visibleRows)
	if l.selected < l.top {
		l.top = l.selected
	}
	if l.selected >= l.top+visibleRows {
		l.top = l.selected - visibleRows + 1
	}
	maxTop := len(l.items) - visibleRows
	if maxTop < 0 {
		maxTop = 0
	}
	if l.top > maxTop {
		l.top = maxTop
	}
}

func (p *Panel) clampListRange(l *List, visibleRows int) {
	if visibleRows < 1 {
		visibleRows = 1
	}
	maxTop := len(l.items) - visibleRows
	if maxTop < 0 {
		maxTop = 0
	}
	if l.top < 0 {
		l.top = 0
	}
	if l.top > maxTop {
		l.top = maxTop
	}
}
