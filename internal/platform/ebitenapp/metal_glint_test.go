package ebitenapp

import (
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"testing"
)

func TestGlintChordOwnsGUntilRelease(t *testing.T) {
	a := &app{mode: RendererModern}
	for _, mods := range []input.Modifiers{{Ctrl: true, Shift: true}, {Shift: true}, {}} {
		s := sampledInput{modifiers: mods, characters: []rune{'G'}}
		s.keys[input.KeyG] = true
		a.consumeMetalGlintShortcut(&s)
		if s.keys[input.KeyG] || len(s.characters) != 0 {
			t.Fatal("captured glint key leaked to game/chat")
		}
	}
	a.consumeMetalGlintShortcut(&sampledInput{})
	s := sampledInput{characters: []rune{'g'}}
	s.keys[input.KeyG] = true
	a.consumeMetalGlintShortcut(&s)
	if !s.keys[input.KeyG] || len(s.characters) != 1 {
		t.Fatal("ordinary G remained captured after release")
	}
}
