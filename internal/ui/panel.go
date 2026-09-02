// Package ui owns mutable authored frontend-panel presentation state.
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

func (l *List) Len() int {
	if l == nil {
		return 0
	}
	return len(l.items)
}

func (l *List) Selected() int {
	if l == nil {
		return 0
	}
	return l.selected
}

func (l *List) SetSelected(selected int) {
	if l != nil {
		l.selected = selected
	}
}

func (l *List) Top() int {
	if l == nil {
		return 0
	}
	return l.top
}

func (l *List) SetTop(top int) {
	if l != nil {
		l.top = top
	}
}

// ListValues returns an immutable row snapshot and current presentation
// indices for a named list.
func (p *Panel) ListValues(name string) (items []string, selected, top int, ok bool) {
	l := p.list(name)
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
	Text   map[string]string
	Help   map[string]string
	Active map[string]bool
	Status map[string]int
	Lists  map[string]*List
	Owner  map[string]string

	focus        int
	pressed      int
	rightPressed int
	drag         scrollDrag
	message      string

	// flash is the per-gadget `colorf` word held as runtime state, indexed by
	// authored gadget index. `colorf` is not a colour for a button, a label or
	// a picture box: the painter passes it as the light-table row of the keyed
	// blitter and the service pass decays it, so it is flash state belonging to
	// the instance rather than to the compiled definition
	// [07 R-WGT-01 §1][07 R-WGT-01 §12][03 R-FONT-01 §6].
	flash []uint16
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
	list       string
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

func NewStack() *PanelStack { return &PanelStack{} }

func (s *PanelStack) Replace(panel *Panel) {
	if s == nil {
		return
	}
	s.entries = s.entries[:0]
	if panel != nil {
		s.entries = append(s.entries, PanelEntry{Panel: panel})
	}
}

func (s *PanelStack) Push(panel *Panel) {
	if s != nil && panel != nil {
		s.entries = append(s.entries, PanelEntry{Panel: panel})
	}
}

func (s *PanelStack) PushModal(panel *Panel) {
	if s != nil && panel != nil {
		s.entries = append(s.entries, PanelEntry{Panel: panel, Modal: true})
	}
}

func (s *PanelStack) Top() *Panel {
	if s == nil || len(s.entries) == 0 {
		return nil
	}
	return s.entries[len(s.entries)-1].Panel
}

func (s *PanelStack) Under() *Panel {
	if s == nil || len(s.entries) < 2 {
		return nil
	}
	return s.entries[len(s.entries)-2].Panel
}

// SaveUnder is the panel restored/drawn beneath the active entry [07 §3].
func (s *PanelStack) SaveUnder() *Panel { return s.Under() }

func (s *PanelStack) Modal() *Panel {
	if s == nil || len(s.entries) == 0 || !s.entries[len(s.entries)-1].Modal {
		return nil
	}
	return s.entries[len(s.entries)-1].Panel
}

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

func (s *PanelStack) Clear() {
	if s != nil {
		s.entries = s.entries[:0]
	}
}

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

func NewPanel(window *gui.Window) *Panel {
	p := &Panel{Window: window, Text: make(map[string]string), Help: make(map[string]string), Active: make(map[string]bool), Status: make(map[string]int), Lists: make(map[string]*List), Owner: make(map[string]string), focus: -1, pressed: -1, rightPressed: -1}
	if window == nil {
		return p
	}
	if window.Focus >= 0 && window.Focus < len(window.Gadgets) {
		p.focus = window.Focus
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
	for _, gadget := range window.Gadgets {
		key := Key(gadget.Name)
		if key == "" {
			continue
		}
		if _, exists := p.Owner[key]; exists {
			continue
		}
		p.Owner[key] = gadget.SourceName
		p.Text[key] = gadget.Text
		p.Help[key] = gadget.Help
		p.Active[key] = gadget.Active != 0
		p.Status[key] = int(gadget.Status)
		if gadget.Kind == gui.KindListBox {
			p.Lists[key] = &List{}
		}
	}
	return p
}

func Key(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func (p *Panel) ActiveOf(name string) bool { return p != nil && p.Active[Key(name)] }

func (p *Panel) SetFocus(index int) {
	if p != nil {
		p.focus = index
	}
}

func (p *Panel) Focused() int {
	if p == nil {
		return -1
	}
	return p.focus
}
func (p *Panel) PressedIndex() int {
	if p == nil {
		return -1
	}
	return p.pressed
}
func (p *Panel) SetPressed(index int) {
	if p != nil {
		p.pressed = index
	}
}
func (p *Panel) RightPressedIndex() int {
	if p == nil {
		return -1
	}
	return p.rightPressed
}
func (p *Panel) SetRightPressed(index int) {
	if p != nil {
		p.rightPressed = index
	}
}

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
	return Action{Kind: ActionActivate, Gadget: p.Window.Gadgets[idx].Name, Index: idx}
}

// Activate returns a semantic action for a focused authored gadget. It is used
// by keyboard activation, and Enter's `crdefault` and Space both refuse a
// greyed button, so it applies the same fire-time predicate as a press
// [07 R-WGT-01 §2][07 R-WGT-01 §13].
func (p *Panel) Activate(index int) Action {
	if !p.Fires(index) {
		return Action{Kind: ActionNone, Index: -1}
	}
	return Action{Kind: ActionActivate, Gadget: p.Window.Gadgets[index].Name, Index: index}
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
	name := ""
	for _, list := range p.Window.Gadgets {
		if list.Kind == gui.KindListBox && list.Assoc == g.Assoc {
			name = list.Name
			break
		}
	}
	if name == "" || p.list(name) == nil {
		return false
	}
	p.drag = scrollDrag{active: true, vertical: vertical, startCoord: coordinate, startTop: p.list(name).Top(), maxTop: maxTop, travel: travel, list: name}
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
	p.SetListTop(p.drag.list, top, p.drag.maxTop)
	return true
}

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

func (p *Panel) Message() string {
	if p == nil {
		return ""
	}
	return p.message
}
func (p *Panel) SetActive(name string, active bool) {
	if p != nil {
		p.Active[Key(name)] = active
	}
}
func (p *Panel) StatusOf(name string) int {
	if p == nil {
		return 0
	}
	return p.Status[Key(name)]
}
func (p *Panel) SetStatus(name string, status int) {
	if p != nil {
		p.Status[Key(name)] = status
	}
}
func (p *Panel) SetText(name, value string) {
	if p != nil {
		p.Text[Key(name)] = value
	}
}
func (p *Panel) TextOf(name string) string {
	if p == nil {
		return ""
	}
	return p.Text[Key(name)]
}
func (p *Panel) TextFor(gadget gui.Gadget) string {
	if p == nil {
		return ""
	}
	key := Key(gadget.Name)
	if owner, ok := p.Owner[key]; ok && owner != gadget.SourceName {
		return gadget.Text
	}
	return p.Text[key]
}
func (p *Panel) SetHelp(name, value string) {
	if p != nil {
		p.Help[Key(name)] = value
	}
}
func (p *Panel) HelpOf(name string) string {
	if p == nil {
		return ""
	}
	return p.Help[Key(name)]
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
		if i == 0 || gadget.Kind == gui.KindPanel || !p.ActiveOf(gadget.Name) {
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
	return g.Kind != gui.KindPanel && p.ActiveOf(g.Name) && g.GrayedOut == 0
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
func (p *Panel) SetList(name string, items []string) {
	if p == nil {
		return
	}
	key := Key(name)
	l := p.Lists[key]
	if l == nil {
		l = &List{}
		p.Lists[key] = l
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
	l := p.list(name)
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
	l := p.list(name)
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
	l := p.list(name)
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
	return p.Lists[Key(name)]
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
