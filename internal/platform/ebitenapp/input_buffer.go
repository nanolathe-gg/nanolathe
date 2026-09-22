package ebitenapp

import "github.com/nanolathe-gg/nanolathe/internal/input"

// hostInputBuffer bridges refresh-paced polling and the 30 Hz host service
// (DESIGN_GPU_RENDERER §13.5). This is host snapshot policy, not native event
// history: normal keys/buttons retain a press until the next service, and
// modifiers retain any down state, matching Ebitengine's snapshot semantics.
// Multiple transitions of one key/button still coalesce into one held sample.
// The existing applyInput observation order remains unchanged [07 §2].
// All access stays on the game goroutine.
type hostInputBuffer struct {
	pending sampledInput
}

func (b *hostInputBuffer) add(sample sampledInput) {
	p := &b.pending
	p.x, p.y, p.timestamp = sample.x, sample.y, sample.timestamp
	p.clipboard = sample.clipboard
	p.heldKeys, p.heldButtons = sample.heldKeys, sample.heldButtons
	p.heldModifiers, p.heldCommand = sample.heldModifiers, sample.heldCommand
	for key := input.Key(1); key < input.KeyCount; key++ {
		p.pressedKeys[key] = p.pressedKeys[key] || sample.pressedKeys[key]
		p.keys[key] = sample.heldKeys[key] || p.pressedKeys[key]
	}
	p.pressedButtons = unionButtons(p.pressedButtons, sample.pressedButtons)
	p.buttons = unionButtons(sample.heldButtons, p.pressedButtons)
	p.modifiers.Shift = p.modifiers.Shift || sample.modifiers.Shift
	p.modifiers.Ctrl = p.modifiers.Ctrl || sample.modifiers.Ctrl
	p.modifiers.Alt = p.modifiers.Alt || sample.modifiers.Alt
	p.keys[input.KeyShift], p.keys[input.KeyCtrl], p.keys[input.KeyAlt] = p.modifiers.Shift, p.modifiers.Ctrl, p.modifiers.Alt
	p.command = p.command || sample.command
	p.wheelX += sample.wheelX
	p.wheelY += sample.wheelY
	p.zoomWheelY += sample.zoomWheelY
	p.panX += sample.panX
	p.panY += sample.panY
	p.pinches = append(p.pinches, sample.pinches...)

	// Filter each character batch with the modifiers that accompanied it.
	// A later Alt/Cmd chord must not suppress earlier ordinary text, nor may
	// a released Ctrl+V leak its printable companion into the editor.
	p.filteredText = true
	if sample.modifiers.Alt || sample.command {
		return
	}
	for _, r := range sample.characters {
		if sample.modifiers.Ctrl && sample.keys[input.KeyV] && (r == 'v' || r == 'V' || r == '\x16') {
			continue
		}
		p.characters = append(p.characters, r)
	}
}

// take transfers the one-shot slices to the caller, which may queue the sample
// until a deferred update body runs. No later add may reuse their backing
// storage. Empty/catch-up drains retain only the latest physical held state,
// pointer coordinates and timestamp; they never replay an earlier press.
func (b *hostInputBuffer) take() sampledInput {
	sample := b.pending
	b.pending = sampledInput{
		x: sample.x, y: sample.y, timestamp: sample.timestamp,
		keys: sample.heldKeys, buttons: sample.heldButtons,
		modifiers: sample.heldModifiers, command: sample.heldCommand,
		heldKeys: sample.heldKeys, heldButtons: sample.heldButtons,
		heldModifiers: sample.heldModifiers, heldCommand: sample.heldCommand,
		clipboard: sample.clipboard, filteredText: true,
	}
	return sample
}

func unionButtons(a, b input.MouseButtons) input.MouseButtons {
	return input.MouseButtons{Left: a.Left || b.Left, Middle: a.Middle || b.Middle, Right: a.Right || b.Right}
}
