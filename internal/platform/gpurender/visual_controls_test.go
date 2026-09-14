package gpurender

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// families reports each executor gate's state, in the order of the controls
// table of GPU design §30.
func families(r *Renderer) map[string]bool {
	return map[string]bool{
		"water":       !r.water.disabled,
		"reflections": !r.reflections.disabled,
		"lighting":    !r.lighting.disabled,
		"glint":       r.metalGlint,
		"materials":   r.materialsEnabled,
		"blast":       !r.distortion.blastDisabled,
		"heat":        !r.heat.disabled,
		"scorch":      r.scorchEnabled,
	}
}

// The player's five Enhanced switches are the whole executor surface (§30):
// every family is on exactly when its switch is, with no second control and no
// environment override beside it. None of this is retail behaviour.
func TestSetEffectsIsTheOnlyGate(t *testing.T) {
	r := &Renderer{}
	r.SetEffects(drawlist.AllEffects())
	for name, on := range families(r) {
		if !on {
			t.Fatalf("all effects on left %s off", name)
		}
	}
	off := drawlist.Effects{}
	r.SetEffects(off)
	for name, on := range families(r) {
		if on {
			t.Fatalf("all effects off left %s on", name)
		}
	}
	if r.Effects() != off {
		t.Fatalf("Effects() = %+v after an all-off selection", r.Effects())
	}
}

// Each switch owns exactly the families the controls table gives it, so turning
// one off cannot silently take another with it.
func TestEachSwitchOwnsItsFamilies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		clear func(*drawlist.Effects)
		off   []string
	}{
		{"water", func(e *drawlist.Effects) { e.Water = false }, []string{"water", "reflections"}},
		{"lighting", func(e *drawlist.Effects) { e.Lighting = false }, []string{"lighting"}},
		{"finish", func(e *drawlist.Effects) { e.Finish = false }, []string{"glint", "materials"}},
		{"distortion", func(e *drawlist.Effects) { e.Distortion = false }, []string{"blast", "heat"}},
		{"marks", func(e *drawlist.Effects) { e.Marks = false }, []string{"scorch"}},
	} {
		e := drawlist.AllEffects()
		tc.clear(&e)
		r := &Renderer{}
		r.SetEffects(e)
		expected := map[string]bool{}
		for _, f := range tc.off {
			expected[f] = true
		}
		for name, on := range families(r) {
			if on == expected[name] {
				t.Fatalf("%s off: %s on=%v", tc.name, name, on)
			}
		}
		// The switch turned back on restores every family it owns.
		r.SetEffects(drawlist.AllEffects())
		for name, on := range families(r) {
			if !on {
				t.Fatalf("%s back on left %s off", tc.name, name)
			}
		}
	}
}

// A source reset retires images, never the applied selection.
func TestSetEffectsSurvivesSourceReset(t *testing.T) {
	r := &Renderer{}
	off := drawlist.Effects{Lighting: true}
	r.SetEffects(off)
	r.resetSources(func(*ebiten.Image) {})
	if r.Effects() != off || !r.water.disabled || !r.heat.disabled || r.lighting.disabled {
		t.Fatalf("a source reset lost the applied selection: %+v", r.Effects())
	}
}
