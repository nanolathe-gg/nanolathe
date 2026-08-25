package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/input"
)

func TestMiddleButtonPollingFixed(t *testing.T) {
	m := &MouseState{}
	// Simulate middle press via Inject helper (which uses correct mapping)
	m.InjectMouseButton(input.MouseButtonMiddle, true)
	if !m.Held(input.MouseButtonMiddle) {
		t.Fatalf("middle Held should be true after Inject")
	}
	if !m.Pressed(input.MouseButtonMiddle) {
		t.Fatalf("middle Pressed should be true on edge")
	}
	// Left should not be affected
	if m.Held(input.MouseButtonLeft) {
		t.Fatalf("left should not be held when middle is")
	}
	m.ClearEdges()
	m.InjectMouseButton(input.MouseButtonMiddle, true)
	if m.Pressed(input.MouseButtonMiddle) {
		t.Fatalf("holding should not generate new Pressed edge")
	}
	m.InjectMouseButton(input.MouseButtonMiddle, false)
	if !m.Released(input.MouseButtonMiddle) {
		t.Fatalf("middle release should be reported")
	}
}

func TestHeadlessInjectionHelpers(t *testing.T) {
	m := &MouseState{X: 10, Y: 10}
	m.InjectMouseMove(20, 30)
	if !m.Moved() {
		t.Fatalf("move should be reported")
	}
	if m.X != 20 || m.Y != 30 {
		t.Fatalf("pos wrong %f %f", m.X, m.Y)
	}
	m.ClearEdges()
	if m.Moved() {
		t.Fatalf("cleared should not report moved")
	}
	m.InjectWheel(0, 1)
	if !m.Scrolled() || m.ScrollY != 1 {
		t.Fatalf("wheel inject failed")
	}
	ks := &KeyboardState{}
	ks.InjectKey(input.KeyW, true)
	if !ks.KeyHeld(input.KeyW) {
		t.Fatalf("W held")
	}
	if !ks.KeyDown(input.KeyW) {
		t.Fatalf("W down edge")
	}
	ks.ClearEdges()
	ks.InjectKey(input.KeyW, true)
	if ks.KeyDown(input.KeyW) {
		t.Fatalf("held should not be down again")
	}
}
