package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/input"
)

func TestMiddleButtonPollingFixed(t *testing.T) {
	m := &MouseState{}
	// Construct the canonical platform-neutral state at the input boundary.
	m.SetButton(input.MouseButtonMiddle, true)
	if !m.Held(input.MouseButtonMiddle) {
		t.Fatalf("middle Held should be true after SetButton")
	}
	if !m.Pressed(input.MouseButtonMiddle) {
		t.Fatalf("middle Pressed should be true on edge")
	}
	// Left should not be affected
	if m.Held(input.MouseButtonLeft) {
		t.Fatalf("left should not be held when middle is")
	}
	m.ResetEdges()
	m.SetButton(input.MouseButtonMiddle, true)
	if m.Pressed(input.MouseButtonMiddle) {
		t.Fatalf("holding should not generate new Pressed edge")
	}
	m.SetButton(input.MouseButtonMiddle, false)
	if !m.Released(input.MouseButtonMiddle) {
		t.Fatalf("middle release should be reported")
	}
}

func TestCanonicalInputState(t *testing.T) {
	m := &MouseState{X: 10, Y: 10}
	m.SetPosition(20, 30)
	if !m.Moved() {
		t.Fatalf("move should be reported")
	}
	if m.X != 20 || m.Y != 30 {
		t.Fatalf("pos wrong %f %f", m.X, m.Y)
	}
	m.ResetEdges()
	if m.Moved() {
		t.Fatalf("cleared should not report moved")
	}
	m.SetWheel(0, 1)
	if !m.Scrolled() || m.ScrollY != 1 {
		t.Fatalf("wheel state failed")
	}
	ks := &KeyboardState{}
	ks.SetKey(input.KeyW, true)
	if !ks.KeyHeld(input.KeyW) {
		t.Fatalf("W held")
	}
	if !ks.KeyDown(input.KeyW) {
		t.Fatalf("W down edge")
	}
	ks.ResetEdges()
	ks.SetKey(input.KeyW, true)
	if ks.KeyDown(input.KeyW) {
		t.Fatalf("held should not be down again")
	}
}
