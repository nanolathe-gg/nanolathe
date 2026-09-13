package gpurender

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The player's Enhanced effect switches (GPU design §30). These lock the
// mapping onto the existing per-family controls, the environment overrides and
// the unchanged-selection early return; none of it is retail behaviour.
func TestSetEffectsMapsFamiliesAndHonoursEnvOverrides(t *testing.T) {
	r := &Renderer{effectEnv: effectEnv{materials: true, scorch: true, metalGlint: true}}
	r.SetEffects(drawlist.AllEffects())
	if r.water.disabled || r.reflections.disabled || r.lighting.disabled ||
		r.distortion.blastDisabled || r.heat.disabled ||
		!r.metalGlint || !r.materialsEnabled || !r.scorchEnabled {
		t.Fatalf("all effects on left a family off: %+v", r.Effects())
	}

	off := drawlist.Effects{}
	r.SetEffects(off)
	if !r.water.disabled || !r.reflections.disabled || !r.lighting.disabled ||
		!r.distortion.blastDisabled || !r.heat.disabled ||
		r.metalGlint || r.materialsEnabled || r.scorchEnabled {
		t.Fatal("all effects off left a family on")
	}
	if r.Effects() != off {
		t.Fatalf("Effects() = %+v after an all-off selection", r.Effects())
	}

	// An environment override keeps its family off whatever the player selects.
	r = &Renderer{effectEnv: effectEnv{materials: false, scorch: false, metalGlint: false}}
	r.SetEffects(drawlist.AllEffects())
	if r.metalGlint || r.materialsEnabled || r.scorchEnabled {
		t.Fatal("an environment override was overridden by the player switch")
	}
	// The other families have no override and follow the selection.
	if r.water.disabled || r.lighting.disabled || r.distortion.blastDisabled {
		t.Fatal("an unoverridden family followed the environment")
	}
}

// SetEffects runs once per presented frame, so an unchanged selection must not
// overwrite the host's Ctrl+Shift+G glint toggle between setting changes.
func TestSetEffectsLeavesComparisonControlsAloneWhenUnchanged(t *testing.T) {
	r := &Renderer{effectEnv: effectEnv{materials: true, scorch: true, metalGlint: true}}
	all := drawlist.AllEffects()
	r.SetEffects(all)
	r.SetMetalGlint(false)
	for range 3 {
		r.SetEffects(all)
	}
	if r.MetalGlint() {
		t.Fatal("a repeated identical selection revived the glint")
	}
	// A real change re-applies every family, including the one the shortcut
	// had turned off.
	changed := all
	changed.Water = false
	r.SetEffects(changed)
	if !r.MetalGlint() || !r.water.disabled {
		t.Fatalf("a changed selection did not re-apply: glint %v water disabled %v", r.MetalGlint(), r.water.disabled)
	}
}

// A source reset retires images, never the applied selection.
func TestSetEffectsSurvivesSourceReset(t *testing.T) {
	r := &Renderer{effectEnv: effectEnv{materials: true, scorch: true, metalGlint: true}}
	off := drawlist.Effects{Lighting: true}
	r.SetEffects(off)
	r.resetSources(func(*ebiten.Image) {})
	if r.Effects() != off || !r.water.disabled || !r.heat.disabled || r.lighting.disabled {
		t.Fatalf("a source reset lost the applied selection: %+v", r.Effects())
	}
}
