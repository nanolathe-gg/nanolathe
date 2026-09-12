package ebitenapp

import (
	"fmt"
	"os"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"
)

// Prototype-only host control. Track the chord ourselves because modern Draw
// can run more than once per Update; one press must toggle exactly once.
type metalGlintControl struct{ held bool }

// Retain ownership until G is released, even when a modifier goes up first.
// Otherwise the suppressed G would become a fresh game/chat key mid-press.
func (a *app) consumeMetalGlintShortcut(sample *sampledInput) {
	if !sample.keys[input.KeyG] {
		a.glintInputCaptured = false
		return
	}
	if a.mode == RendererModern && sample.modifiers.Ctrl && sample.modifiers.Shift {
		a.glintInputCaptured = true
	}
	if !a.glintInputCaptured {
		return
	}
	sample.keys[input.KeyG] = false
	chars := sample.characters[:0]
	for _, ch := range sample.characters {
		if ch != 'g' && ch != 'G' {
			chars = append(chars, ch)
		}
	}
	sample.characters = chars
}

func (c *metalGlintControl) update(r *gpurender.Renderer) bool {
	held := ebiten.IsKeyPressed(ebiten.KeyControl) && ebiten.IsKeyPressed(ebiten.KeyShift) && ebiten.IsKeyPressed(ebiten.KeyG)
	pressed := held && !c.held
	c.held = held
	if !pressed || r == nil {
		return false
	}
	r.SetMetalGlint(!r.MetalGlint())
	state := "OFF"
	if r.MetalGlint() {
		state = "ON"
	}
	fmt.Fprintf(os.Stderr, "Metallic glint %s (Ctrl+Shift+G)\n", state)
	return true
}
