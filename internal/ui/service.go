package ui

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

const (
	widgetLeftButton  uint8 = 1
	widgetRightButton uint8 = 2
)

// WidgetFrame is the complete presentation-only sample for one widget pass.
// TimerAdvanced is supplied by the caller's established scaled-timer sample;
// ServiceFrame never derives a clock or frame counter of its own.
type WidgetFrame struct {
	PointerX, PointerY int32
	HeldButtons        uint8
	PointerEvents      []input.PointerEvent
	// Tokens is the input ring's producer-ordered snapshot.  The service
	// consumes only the prefix it reports, so a later token stays available to
	// the next GUI pass.
	Tokens             []input.Token
	TokenMode          bool
	KeyNavigation      bool
	ShiftHeld, AltHeld bool
	TimerAdvanced      bool
}

// WidgetHooks binds screen-owned measurements and side effects to the common
// state machine. Change is synchronous; Fired is returned for the screen to
// consume only after the service pass has completed.
type WidgetHooks struct {
	Metric    func(index int) int
	ArtFrames func(index int) int // resolved button entry, including fallback art
	Measure   func(index int, text string) int
	Change    func(index int)
	Surface   func(index int)
}

// ServiceResult reports the first fired gadget and the pass residue.
type ServiceResult struct {
	Fired          bool
	FiredIndex     int
	FiredButton    uint8
	HoverIndex     int
	ConsumedTokens int
	// StageAdvanced distinguishes a shared gesture which actually advanced a
	// staged button from a mere callback firing [07 R-WGT-01 §§2-3].
	StageAdvanced bool
}

func widgetButton(kind input.PointerEventKind) (uint8, bool, bool) {
	switch kind {
	case input.LeftDown:
		return widgetLeftButton, true, false
	case input.LeftDoubleClick:
		return widgetLeftButton, true, true
	case input.LeftUp:
		return widgetLeftButton, false, false
	case input.RightDown:
		return widgetRightButton, true, false
	case input.RightDoubleClick:
		return widgetRightButton, true, true
	case input.RightUp:
		return widgetRightButton, false, false
	}
	return 0, false, false
}

// ServiceFrame performs one ordered generic widget pass [07 R-WGT-01 §§1-8].
func (p *Panel) ServiceFrame(frame WidgetFrame, hooks WidgetHooks) ServiceResult {
	result := ServiceResult{FiredIndex: -1, HoverIndex: -1}
	if p == nil || p.Window == nil {
		return result
	}
	// A held sample outside the window is discarded, preserving the last
	// accepted pointer until the release sample is accepted.  The input sample
	// precedes token service in the GUI pass [07 R-WGT-01 §1].
	if frame.HeldButtons == 0 || pointInRect(frame.PointerX, frame.PointerY, p.Window.Rect) {
		p.pointerX, p.pointerY = frame.PointerX, frame.PointerY
	}
	for _, event := range frame.PointerEvents {
		if frame.HeldButtons == 0 || pointInRect(event.X, event.Y, p.Window.Rect) {
			p.pointerX, p.pointerY = event.X, event.Y
		}
	}
	finalX, finalY := p.pointerX, p.pointerY
	p.hover = -1
	finish := func() ServiceResult {
		p.pointerX, p.pointerY = finalX, finalY
		result.HoverIndex = p.hover
		p.updateHelpText()
		return result
	}
	// Retail takes the keyboard token before visiting any gadget. The matrix is
	// gated only by the two window words; a captured editor gets the matrix's
	// unclaimed token at its own indexed visit [07 R-WGT-01 §§1-2, §6].
	matrixConsumed := false
	if frame.TokenMode && len(frame.Tokens) != 0 {
		// Token mode pops one record regardless of whether the matrix recognizes
		// it; retail would then offer the residue to its window callback, which
		// this bounded service has no client for [07 R-WGT-01 §1].
		result.ConsumedTokens = 1
		if frame.KeyNavigation {
			matrixConsumed = p.serviceKeyboardToken(frame.Tokens[0], frame, hooks, &result)
		}
		if result.Fired {
			return finish()
		}
	}
	for i, g := range p.Window.Gadgets {
		if i == 0 || !p.ActiveAt(i) {
			continue
		}
		if frame.TimerAdvanced {
			p.decayFlashAt(i)
		}
		if pointInRect(p.pointerX, p.pointerY, p.Window.PlacedRect(i)) {
			p.hover = i
		}
		if g.Kind == gui.KindSurface && hooks.Surface != nil {
			hooks.Surface(i)
		}
		// An editor drains its token prefix only when its record is reached. An
		// earlier indexed pointer or quickkey result therefore wins this pass
		// [07 R-WGT-01 §1, §6]. Alt affects a later quickkey's admission only.
		if !matrixConsumed && p.EditorCaptured() && p.EditorIndex() == i && len(frame.Tokens) != 0 {
			measure := func(text string) int { return len(text) }
			if hooks.Measure != nil {
				editor := i
				measure = func(text string) int { return hooks.Measure(editor, text) }
			}
			er := p.ApplyEditorTokens(frame.Tokens, measure)
			if er.Consumed > result.ConsumedTokens {
				result.ConsumedTokens = er.Consumed
			}
			matrixConsumed = er.Consumed != 0
			if er.Action.Kind == ActionActivate {
				p.fire(er.Action.Index, 0, &result)
				return finish()
			}
		}
		if len(frame.Tokens) != 0 && !matrixConsumed && !suppressedPeekToken(frame.TokenMode, frame.Tokens[0]) && p.keyboardQuickKeyAt(i, frame.Tokens[0], frame.AltHeld, &result) {
			result.ConsumedTokens = 1
			return finish()
		}
		handled := false
		for _, event := range frame.PointerEvents {
			if frame.HeldButtons == 0 || pointInRect(event.X, event.Y, p.Window.Rect) {
				p.pointerX, p.pointerY = event.X, event.Y
			}
			button, down, double := widgetButton(event.Kind)
			if button == 0 {
				continue
			}
			if down && p.servicePressTest(p.pointerX, p.pointerY) == i {
				handled = true
				if p.serviceDown(button, double, hooks, &result) {
					return finish()
				}
			}
			if !down && p.capture == i {
				handled = true
				if p.serviceUp(button, hooks, &result) {
					return finish()
				}
			}
		}
		p.pointerX, p.pointerY = finalX, finalY
		if !handled && p.capture == i {
			if p.serviceHeld(frame, hooks, &result) {
				return finish()
			}
		}
	}
	return finish()
}

func suppressedPeekToken(tokenMode bool, token input.Token) bool {
	return !tokenMode && token.Kind == input.TokenEdit && token.Key >= input.KeyF1 && token.Key <= input.KeyF10
}

func (p *Panel) serviceDown(button uint8, double bool, hooks WidgetHooks, result *ServiceResult) bool {
	idx := p.servicePressTest(p.pointerX, p.pointerY)
	if idx < 0 {
		return false
	}
	// A live non-editor owner keeps capture regardless of which button made
	// the later press.  Text capture is replaceable by a new gadget take.
	if p.capture >= 0 && !p.EditorCaptured() && p.capture != idx {
		return false
	}
	if p.EditorCaptured() && p.editor.captured != idx {
		p.editor.captured = -1 // text capture is the one replaceable capture.
	}
	p.capture, p.captureButton = idx, button
	p.focus = idx
	g := p.Window.Gadgets[idx]
	if g.Kind == gui.KindTextBox {
		p.FocusEditor(idx)
		p.clearCapture()
		return false
	}
	if g.Kind == gui.KindListBox {
		p.listEdgeTicks[idx] = 0
		p.serviceList(idx, hooks)
		if g.Attribs&0x40 != 0 && p.listPointerSelectable(idx, hooks) {
			return p.fire(idx, button, result)
		}
		if double && button == widgetLeftButton && p.listPointerSelectable(idx, hooks) {
			return p.fire(idx, button, result)
		}
		return false
	}
	if g.Kind == gui.KindScrollBar {
		p.sliderBegin(idx, hooks)
		p.serviceSlider(idx, hooks)
		return false
	}
	if g.Kind != gui.KindButton {
		return false
	}
	p.savedDown = p.StatusAt(idx)
	attr := g.Attribs
	if attr&0x10 != 0 && p.inside(idx) {
		p.SetStatusAt(idx, 1)
		p.clearGroup(idx)
		p.markDirty()
		return p.fire(idx, button, result)
	}
	if attr&0x100 != 0 {
		if button == widgetLeftButton {
			p.cycleDown(idx, hooks)
			p.clearCapture()
			return p.fire(idx, button, result)
		}
		return false
	}
	if attr&0x1800 != 0 {
		// Slider arrows trigger once on their initial press; their repeat arm
		// remains gated by the 15-tick delay below [07 R-WGT-01 §3].
		p.stepAssociatedSlider(idx, hooks)
		p.repeat = 15
		return false
	}
	if attr&0x40 != 0 {
		p.SetStatusAt(idx, 1)
	} else {
		p.SetStatusAt(idx, 1)
		p.repeat = 15
	}
	p.markDirty()
	return false
}

func (p *Panel) serviceUp(button uint8, hooks WidgetHooks, result *ServiceResult) bool {
	if p.capture < 0 || p.captureButton != button {
		return false
	}
	idx := p.capture
	p.clearCapture()
	g := p.Window.Gadgets[idx]
	inside := p.inside(idx)
	switch g.Kind {
	case gui.KindScrollBar:
		p.sliderDragging = false
		return false
	case gui.KindListBox:
		p.listEdgeTicks[idx] = 0
		return false
	case gui.KindButton:
		if g.Attribs&0x100 != 0 {
			return false
		}
		if g.Attribs&0x10 != 0 {
			if !inside {
				p.SetStatusAt(idx, p.savedDown)
			}
			return false
		}
		if g.Attribs&0x40 != 0 {
			if inside {
				p.SetStatusAt(idx, boolToInt(p.savedDown == 0))
				p.clearGroup(idx)
				p.markDirty()
				return p.fire(idx, button, result)
			}
			p.SetStatusAt(idx, p.savedDown)
			return false
		}
		if g.Attribs&8 != 0 && inside {
			p.SetStatusAt(idx, flipBinary(p.savedDown))
			p.markDirty()
			return p.fire(idx, button, result)
		}
		p.SetStatusAt(idx, 0)
		if !inside {
			p.markDirty()
			return false
		}
		p.clearGroup(idx)
		if g.Attribs&0x1800 != 0 {
			return false
		}
		if g.Stages > 0 {
			p.cycleButton(idx)
			result.StageAdvanced = true
		}
		p.markDirty()
		return p.fire(idx, button, result)
	case gui.KindLabel, gui.KindSurface:
		if !inside {
			return false
		}
		return p.serviceLinkOrFire(idx, button, result)
	}
	return false
}

func (p *Panel) serviceHeld(frame WidgetFrame, hooks WidgetHooks, result *ServiceResult) bool {
	if p.capture < 0 || frame.HeldButtons&p.captureButton == 0 {
		return false
	}
	idx := p.capture
	g := p.Window.Gadgets[idx]
	switch g.Kind {
	case gui.KindListBox:
		p.serviceList(idx, hooks)
		if frame.TimerAdvanced {
			p.scrollCapturedList(idx, hooks)
		}
	case gui.KindScrollBar:
		p.serviceSlider(idx, hooks)
	case gui.KindButton:
		if g.Attribs&0x100 != 0 {
			return false
		}
		if g.Attribs&0x10 != 0 {
			if !p.inside(idx) {
				p.SetStatusAt(idx, p.savedDown)
			}
			return false
		}
		if g.Attribs&0x40 != 0 {
			p.SetStatusAt(idx, boolToInt(p.inside(idx)))
			return false
		}
		if !p.inside(idx) {
			p.SetStatusAt(idx, 0)
			return false
		}
		if p.StatusAt(idx) == 0 {
			// Returning inside begins a fresh held press, including its full repeat
			// delay; a cycle control never reaches this branch [07 R-WGT-01 §3].
			p.SetStatusAt(idx, 1)
			p.repeat = 15
			p.markDirty()
			return false
		}
		if g.Attribs&0x2000 != 0 && frame.TimerAdvanced {
			if p.repeat > 0 {
				p.repeat--
				return false
			}
			p.markDirty()
			if g.Attribs&0x1800 != 0 {
				p.stepAssociatedSlider(idx, hooks)
			}
			return false
		}
	}
	return false
}

func (p *Panel) serviceList(idx int, hooks WidgetHooks) {
	l := p.ListAt(idx)
	if l == nil || l.Len() == 0 {
		return
	}
	// TODO(question): record-list payloads provide only the known row-height
	// boundary here; the variable record payload needed for full hit walking is
	// unresolved [07 R-WGT-01 §4].
	g, r := p.Window.Gadgets[idx], p.Window.PlacedRect(idx)
	if g.Attribs&0x10 == 0 {
		return
	}
	metric := 0
	if hooks.Metric != nil {
		metric = hooks.Metric(idx)
	}
	rowH, rows := p.listRows(idx, metric)
	if rowH <= 0 {
		return
	}
	if rows <= 0 {
		return
	}
	if p.pointerX < r.X || p.pointerX > r.X+r.W-1 || p.pointerY < r.Y+2 || p.pointerY > r.Y+r.H-4 {
		return
	}
	sel := l.Top() + int((p.pointerY-(r.Y+2))/int32(rowH))
	if sel >= l.Top()+rows {
		sel = l.Top() + rows - 1
	}
	if sel >= l.Len() {
		sel = l.Len() - 1
	}
	if sel < 0 {
		return
	}
	if g.Attribs&0x200 != 0 && p.listHeading(idx, sel) {
		return
	}
	p.setListSelection(idx, sel, hooks)
}

func (p *Panel) scrollCapturedList(idx int, hooks WidgetHooks) {
	l := p.ListAt(idx)
	if l == nil {
		return
	}
	r := p.Window.PlacedRect(idx)
	metric := 0
	if hooks.Metric != nil {
		metric = hooks.Metric(idx)
	}
	_, rows := p.listRows(idx, metric)
	if rows <= 0 {
		return
	}
	p.listEdgeTicks[idx]++
	if p.listEdgeTicks[idx] < 2 {
		return
	}
	p.listEdgeTicks[idx] = 0
	if p.Window.Gadgets[idx].Attribs&0x10 == 0 {
		return
	}
	if p.pointerY > r.Y+r.H-4 && l.top < p.ListMaxTopAt(idx) {
		l.top++
		if !p.listHeading(idx, l.top+rows-1) {
			p.setListSelection(idx, l.top+rows-1, hooks)
		}
	}
	if p.pointerY < r.Y+2 && l.top > 0 {
		next := minInt(l.selected, l.top) - 1
		l.top--
		if !p.listHeading(idx, next) {
			p.setListSelection(idx, next, hooks)
		}
	} else if p.pointerY < r.Y+2 && l.top == 0 {
		if !p.listHeading(idx, 0) {
			p.setListSelection(idx, 0, hooks)
		}
	}
}

func (p *Panel) sliderBegin(idx int, hooks WidgetHooks) {
	p.sliderDragging = p.inSliderKnob(idx, hooks)
	p.sliderStartPointer = p.sliderAxis(idx)
	p.sliderStartKnob = p.knob[idx]
}
func (p *Panel) serviceSlider(idx int, hooks WidgetHooks) {
	if p.sliderDragging {
		p.setKnob(idx, p.sliderStartKnob+int(p.sliderAxis(idx)-p.sliderStartPointer), hooks)
		return
	}
	if p.sliderAxis(idx) < p.sliderKnobStart(idx) {
		p.setKnob(idx, p.knob[idx]-1, hooks)
	} else if p.sliderAxis(idx) > p.sliderKnobStart(idx)+int32(p.sliderKnobSize(idx, hooks)) {
		p.setKnob(idx, p.knob[idx]+1, hooks)
	}
}
func (p *Panel) sliderAxis(idx int) int32 {
	g := p.Window.Gadgets[idx]
	if g.Attribs&1 != 0 {
		return p.pointerX
	}
	return p.pointerY
}
func (p *Panel) sliderKnobStart(idx int) int32 {
	r := p.Window.PlacedRect(idx)
	g := p.Window.Gadgets[idx]
	if g.Attribs&1 != 0 {
		return r.X + 1 + int32(p.knob[idx])
	}
	return r.Y + 2 + int32(p.knob[idx])
}
func (p *Panel) inSliderKnob(idx int, hooks WidgetHooks) bool {
	start := p.sliderKnobStart(idx)
	size := int32(p.sliderKnobSize(idx, hooks))
	axis := p.sliderAxis(idx)
	return axis >= start && axis <= start+size
}
func (p *Panel) setKnob(idx, value int, hooks WidgetHooks) {
	travel := p.sliderTravel(idx, hooks)
	if travel <= 0 {
		return
	}
	value = maxInt(0, minInt(value, travel-1))
	if p.knob[idx] == value {
		return
	}
	p.knob[idx] = value
	p.markDirty()
	p.syncSlider(idx, hooks)
	if hooks.Change != nil {
		hooks.Change(idx)
	}
}
func (p *Panel) stepAssociatedSlider(idx int, hooks WidgetHooks) {
	g := p.Window.Gadgets[idx]
	delta := 1
	if g.Attribs&0x1000 != 0 {
		delta = -1
	}
	for i, other := range p.Window.Gadgets {
		if other.Kind == gui.KindScrollBar && other.Assoc == g.Assoc {
			p.setKnob(i, p.knob[i]+delta, hooks)
			return
		}
	}
}
func (p *Panel) syncSlider(idx int, hooks WidgetHooks) {
	g := p.Window.Gadgets[idx]
	travel := p.sliderTravel(idx, hooks)
	for i, other := range p.Window.Gadgets {
		if i == idx || other.Assoc != g.Assoc {
			continue
		}
		if other.Kind == gui.KindListBox {
			l := p.ListAt(i)
			if l == nil || other.ItemHeight == 0 || travel <= 1 {
				continue
			}
			count := l.Len()
			visible := int(p.Window.PlacedRect(i).H) / int(other.ItemHeight)
			e := 0
			if other.Attribs&0x20 != 0 {
				e = travel / (p.ListMaxTopAt(i) + 1)
			}
			l.top = (count - visible) * (p.knob[idx] + e) / (travel - 1)
			if l.top < 0 {
				l.top = 0
			}
		}
	}
}

// listRows is the pointer and edge-scroll geometry. The default is the
// selected font metric plus one; fill normalizes authored heights through that
// floor before service reads them [07 R-WGT-01 §4].
func (p *Panel) listRows(idx, metric int) (rowH, rows int) {
	if p == nil || p.Window == nil || idx < 0 || idx >= len(p.Window.Gadgets) {
		return 0, 0
	}
	g, r := p.Window.Gadgets[idx], p.Window.PlacedRect(idx)
	rowH = int(g.ItemHeight)
	if rowH <= metric+1 {
		rowH = metric + 1
	}
	if rowH <= 0 {
		return rowH, 0
	}
	return rowH, int((r.H - 2) / int32(rowH))
}

// sliderTravel and sliderKnobSize recompute an associated text-list bar from
// the loaded control dimensions each pass. A standalone bar retains the
// builder's loaded travel and knob length [07 R-WGT-01 §5].
func (p *Panel) sliderTravel(idx int, hooks WidgetHooks) int {
	if p == nil || p.Window == nil || idx < 0 || idx >= len(p.Window.Gadgets) {
		return 0
	}
	g := p.Window.Gadgets[idx]
	for i, other := range p.Window.Gadgets {
		if other.Kind != gui.KindListBox || other.Assoc != g.Assoc {
			continue
		}
		l := p.ListAt(i)
		if l == nil || other.Attribs&0x10 == 0 {
			continue
		}
		metric := 0
		if hooks.Metric != nil {
			metric = hooks.Metric(i)
		}
		rowH := maxInt(metric+1, int(other.ItemHeight))
		if rowH <= 0 {
			return 0
		}
		rows := int((p.Window.PlacedRect(i).H - 2) / int32(rowH))
		if l.Len() <= rows {
			return 0
		}
		barH := int(p.Window.PlacedRect(idx).H)
		knob := maxInt(10, int(float32(rows)/float32(l.Len())*float32(barH-3)))
		return maxInt(0, barH-knob-3)
	}
	return maxInt(0, int(g.Range))
}

func (p *Panel) sliderKnobSize(idx int, hooks WidgetHooks) int {
	if p == nil || p.Window == nil || idx < 0 || idx >= len(p.Window.Gadgets) {
		return 0
	}
	g := p.Window.Gadgets[idx]
	for i, other := range p.Window.Gadgets {
		if other.Kind != gui.KindListBox || other.Assoc != g.Assoc || other.Attribs&0x10 == 0 {
			continue
		}
		l := p.ListAt(i)
		if l == nil || l.Len() == 0 {
			continue
		}
		metric := 0
		if hooks.Metric != nil {
			metric = hooks.Metric(i)
		}
		rowH := maxInt(metric+1, int(other.ItemHeight))
		if rowH <= 0 {
			return 0
		}
		rows := int((p.Window.PlacedRect(i).H - 2) / int32(rowH))
		return maxInt(10, int(float32(rows)/float32(l.Len())*float32(int(p.Window.PlacedRect(idx).H)-3)))
	}
	return int(g.KnobSize)
}
func (p *Panel) setListSelection(idx, sel int, hooks WidgetHooks) {
	l := p.ListAt(idx)
	if l == nil || l.Len() == 0 {
		return
	}
	sel = maxInt(0, minInt(sel, l.Len()-1))
	if l.selected == sel {
		return
	}
	l.selected = sel
	p.markDirty()
	g := p.Window.Gadgets[idx]
	for i, other := range p.Window.Gadgets {
		if i == idx || other.Assoc != g.Assoc {
			continue
		}
		if other.Kind == gui.KindListBox {
			if peer := p.ListAt(i); peer != nil {
				peer.selected, peer.top = minInt(l.selected, peer.Len()-1), l.top
			}
			continue
		}
		if other.Kind == gui.KindScrollBar && l.Len() > 1 {
			travel := p.sliderTravel(i, hooks)
			if p.ListMaxTopAt(idx) == 0 || travel <= 0 {
				p.knob[i] = 0
			} else {
				p.knob[i] = l.top * travel / p.ListMaxTopAt(idx)
			}
			p.markDirty()
			continue
		}
		if other.Kind == gui.KindTextBox && g.Attribs&8 != 0 {
			p.SetTextAt(i, l.items[l.selected])
			p.markDirty()
		}
	}
	if hooks.Change != nil {
		hooks.Change(idx)
	}
}

// listPointerSelectable applies the list click's interior and heading checks
// to the row under the pointer.  Double-click must not reuse the old selected
// row after a heading rejected the preceding click [07 R-WGT-01 §4].
func (p *Panel) listPointerSelectable(idx int, hooks WidgetHooks) bool {
	l := p.ListAt(idx)
	if l == nil || l.Len() == 0 {
		return false
	}
	g, r := p.Window.Gadgets[idx], p.Window.PlacedRect(idx)
	if g.Attribs&0x10 == 0 || p.pointerX < r.X || p.pointerX > r.X+r.W-1 || p.pointerY < r.Y+2 || p.pointerY > r.Y+r.H-4 {
		return false
	}
	metric := 0
	if hooks.Metric != nil {
		metric = hooks.Metric(idx)
	}
	rowH, rows := p.listRows(idx, metric)
	if rowH <= 0 || rows <= 0 {
		return false
	}
	sel := l.top + int((p.pointerY-(r.Y+2))/int32(rowH))
	sel = minInt(sel, l.top+rows-1)
	sel = minInt(sel, l.Len()-1)
	return sel >= 0 && !(g.Attribs&0x200 != 0 && p.listHeading(idx, sel))
}
func (p *Panel) listHeading(idx, selection int) bool {
	l := p.ListAt(idx)
	return l != nil && selection >= 0 && selection < l.Len() && (p.ListRowFlagAt(idx, selection) == 1 || strings.HasPrefix(l.items[selection], "&G"))
}
func (p *Panel) serviceLinkOrFire(idx int, button uint8, result *ServiceResult) bool {
	g := p.Window.Gadgets[idx]
	if g.Kind == gui.KindLabel && g.Link != "" {
		target := p.Index(g.Link)
		if target < 0 || !p.ActiveAt(target) {
			return false
		}
		tg := p.Window.Gadgets[target]
		if tg.Kind == gui.KindButton {
			if tg.GrayedOut&1 != 0 {
				return false
			}
			if tg.Stages > 0 {
				p.cycleButton(target)
				result.StageAdvanced = true
			}
			return p.fire(target, button, result)
		}
		if tg.Kind == gui.KindScrollBar && (tg.Attribs&0x10 != 0 || tg.GrayedOut != 0) {
			return false
		}
		p.SetFocus(target)
		return false
	}
	return p.fire(idx, button, result)
}
func (p *Panel) cycleButton(idx int) {
	g := p.Window.Gadgets[idx]
	n := int(g.Stages)
	if n <= 0 {
		n = 1
	}
	p.stage[idx] = (p.stage[idx] + 1) % n
	p.markDirty()
}
func (p *Panel) cycleDown(idx int, hooks WidgetHooks) {
	if hooks.ArtFrames == nil {
		return // no bound art cannot supply a frame cycle
	}
	n := hooks.ArtFrames(idx)
	if n > 0 {
		p.SetStatusAt(idx, (p.StatusAt(idx)+1)%n)
		p.markDirty()
	}
}

// ClearButtonGroup releases every button in the indexed control's authored
// association, including the control itself [07 R-HUD-04 §3].
func (p *Panel) ClearButtonGroup(idx int) {
	if p == nil || p.Window == nil || idx < 0 || idx >= len(p.Window.Gadgets) || p.Window.Gadgets[idx].Kind != gui.KindButton {
		return
	}
	p.SetStatusAt(idx, 0)
	p.clearGroup(idx)
	p.markDirty()
}

func (p *Panel) clearGroup(idx int) {
	g := p.Window.Gadgets[idx]
	for i, o := range p.Window.Gadgets {
		if i != idx && o.Kind == gui.KindButton && o.Assoc == g.Assoc && p.StatusAt(i) != 0 {
			p.SetStatusAt(i, 0)
			p.markDirty()
		}
	}
}
func (p *Panel) inside(idx int) bool {
	if idx < 0 || idx >= len(p.Window.Gadgets) {
		return false
	}
	r := p.Window.PlacedRect(idx)
	if p.Window.Gadgets[idx].Kind == gui.KindSurface {
		r = p.Window.Gadgets[idx].Rect
	}
	return pointInRect(p.pointerX, p.pointerY, r)
}
func (p *Panel) fire(idx int, button uint8, result *ServiceResult) bool {
	p.focus = idx
	result.Fired = true
	result.FiredIndex = idx
	result.FiredButton = button
	result.HoverIndex = p.hover
	return true
}
func (p *Panel) clearCapture() { p.capture = -1; p.captureButton = 0; p.repeat = 0 }
func (p *Panel) markDirty()    { p.dirty = true }
func (p *Panel) updateHelpText() {
	value := ""
	if p.hover >= 0 {
		value = p.HelpAt(p.hover)
	}
	help := p.Index("HELPTEXT")
	if help >= 0 && p.TextAt(help) != value {
		p.SetTextAt(help, value)
		p.markDirty()
	}
}
func pointInRect(x, y int32, r gui.Rect) bool {
	return r.W > 0 && r.H > 0 && x >= r.X && y >= r.Y && x <= r.X+r.W-1 && y <= r.Y+r.H-1
}
func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func flipBinary(value int) int {
	if value == 0 {
		return 1
	}
	if value == 1 {
		return 0
	}
	return value
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (p *Panel) decayFlashAt(index int) {
	if p.flash[index] == 0 {
		return
	}
	var step uint16
	switch p.Window.Gadgets[index].Kind {
	case gui.KindButton:
		step = 2
	case gui.KindPicture:
		step = 1
	default:
		return
	}
	p.markDirty()
	if p.flash[index] <= step {
		p.flash[index] = 0
		return
	}
	p.flash[index] -= step
}

func (p *Panel) servicePressTest(x, y int32) int {
	for i, g := range p.Window.Gadgets {
		if i == 0 || !p.Fires(i) {
			continue
		}
		r := p.Window.PlacedRect(i)
		if g.Kind == gui.KindSurface {
			r = g.Rect
		}
		if pointInRect(x, y, r) {
			return i
		}
	}
	return -1
}
