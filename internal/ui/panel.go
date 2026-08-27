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

// Activate returns a semantic action for a focused authored gadget. It is
// used by keyboard activation and applies the same visibility/grayed checks
// as pointer hit testing [07 §3].
func (p *Panel) Activate(index int) Action {
	if p == nil || p.Window == nil || index < 0 || index >= len(p.Window.Gadgets) {
		return Action{Kind: ActionNone, Index: -1}
	}
	g := p.Window.Gadgets[index]
	if g.Kind == gui.KindPanel || !p.ActiveOf(g.Name) || g.GrayedOut != 0 {
		return Action{Kind: ActionNone, Index: -1}
	}
	return Action{Kind: ActionActivate, Gadget: g.Name, Index: index}
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

// SetMessage stores a modal message without fabricating controls or geometry.
// The authored message window decides whether and where this text is bound;
// unresolved binding details remain a research question [07 §3].
func (p *Panel) SetMessage(message string) bool {
	if p == nil || p.Window == nil {
		return false
	}
	for i, gadget := range p.Window.Gadgets {
		if i == 0 || gadget.Active == 0 || (gadget.Kind != gui.KindLabel && gadget.Kind != gui.KindText) {
			continue
		}
		p.message = message
		p.SetText(gadget.Name, message)
		return true
	}
	return false
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

// HitTest is inclusive on both edges and rejects hidden/grayed gadgets.
func (p *Panel) HitTest(x, y int32) int {
	if p == nil || p.Window == nil {
		return -1
	}
	for i, gadget := range p.Window.Gadgets {
		if gadget.Kind == gui.KindPanel || !p.ActiveOf(gadget.Name) || gadget.GrayedOut != 0 {
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
// authored gadget index or -1 when the press is outside an active control.
func (p *Panel) Press(x, y int32) int {
	if p == nil {
		return -1
	}
	p.pressed = p.HitTest(x, y)
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
	if pressed < 0 || p.HitTest(x, y) != pressed || pressed >= len(p.Window.Gadgets) {
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
