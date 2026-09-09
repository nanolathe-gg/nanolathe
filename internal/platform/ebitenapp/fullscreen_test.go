package ebitenapp

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

func TestSelectedWindowSizeSurvivesLogicalTransitions(t *testing.T) {
	c, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	w, h := 1024, 768
	a := app{c: c, options: RunOptions{WindowSize: func() (int, int) { return w, h }}}
	for _, size := range [][2]int{{640, 480}, {1024, 768}, {640, 480}} {
		c.Resize(size[0], size[1])
		if x, y := a.desiredWindowSize(); x != w || y != h {
			t.Fatalf("window followed logical transition: %dx%d", x, y)
		}
		if x, y := a.Layout(1920, 1080); x != size[0] || y != size[1] {
			t.Fatalf("desktop changed logical canvas: %dx%d", x, y)
		}
	}
	w, h = 800, 600
	if x, y := a.desiredWindowSize(); x != w || y != h {
		t.Fatal("live display selection was ignored")
	}
}

func TestFullscreenShortcutConsumesEnterUntilRelease(t *testing.T) {
	a := app{}
	in := input.NewState()
	sample := func(enter, alt bool) sampledInput {
		s := sampledInput{modifiers: input.Modifiers{Alt: alt}}
		s.keys[input.KeyEnter] = enter
		return s
	}
	for i, state := range []struct{ enter, alt, toggle bool }{
		{true, true, true}, {true, true, false}, {true, false, false}, {false, false, false},
		{true, true, true}, {false, false, false},
	} {
		s := sample(state.enter, state.alt)
		if state.enter {
			s.characters = []rune{'\r', '\n'}
		}
		if got := a.consumeFullscreenShortcut(&s); got != state.toggle {
			t.Fatalf("sample %d toggle = %v", i, got)
		}
		applyInput(in, s)
		if in.Kbd.KeyHeld(input.KeyEnter) || in.PendingTokens() != 0 {
			t.Fatalf("sample %d leaked Enter to game input", i)
		}
	}
	s := sample(true, false)
	if a.consumeFullscreenShortcut(&s) {
		t.Fatal("ordinary Enter toggled fullscreen")
	}
	applyInput(in, s)
	if !in.Kbd.KeyHeld(input.KeyEnter) || in.PendingTokens() != 1 {
		t.Fatal("ordinary Enter was lost")
	}
}

func TestFullscreenPersistsOnlyObservedChanges(t *testing.T) {
	var changes []bool
	a := app{options: RunOptions{FullscreenChanged: func(v bool) { changes = append(changes, v) }}}
	for _, v := range []bool{false, true, true, false, false} {
		a.observeFullscreen(v)
	}
	if len(changes) != 2 || !changes[0] || changes[1] {
		t.Fatalf("changes = %v", changes)
	}
}
