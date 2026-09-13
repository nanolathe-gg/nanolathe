package gpurender

import "github.com/nanolathe-gg/nanolathe/internal/drawlist"

// SetMaterials and SetScorch are local comparison controls for Enhanced
// presentation (GPU design §29). They never change simulation or saved state.
func (r *Renderer) SetMaterials(enabled bool) { r.materialsEnabled = enabled }
func (r *Renderer) SetScorch(enabled bool)    { r.scorchEnabled = enabled }

// effectEnv is the set of construction-time environment overrides the player's
// switches are AND-ed with (GPU design §29.2, §30). A `…=0` override keeps its
// family off whatever the options page selects; the default leaves each one on.
type effectEnv struct {
	materials  bool
	scorch     bool
	metalGlint bool
}

// SetEffects applies the player's Enhanced effect selection (§30). It is called
// once per presented frame beside SetGlow, so the early return on an unchanged
// selection is load-bearing: without it every frame would overwrite the local
// comparison controls, and the host's metallic-glint shortcut would revert on
// the next Draw instead of holding until the player changes a setting.
//
// Nothing here allocates, compiles or submits; each assignment is read by the
// owning pass on its next frame. A source reset preserves these switches, so an
// executor swap or a new battle keeps the selection that was last applied.
func (r *Renderer) SetEffects(e drawlist.Effects) {
	if r == nil || (r.effectsSet && r.effects == e) {
		return
	}
	r.effects, r.effectsSet = e, true
	// Water: the coastal surface with its wakes and foam, and the above-water
	// screen-space reflections (§26.1, §26.4).
	r.SetWaterEffects(e.Water)
	r.SetWaterReflections(e.Water)
	// Lighting: the bounded explosion and nanolathe illumination pass (§23).
	r.SetBattleLighting(e.Lighting)
	// Finish: the metallic glint and the metal/paint material finishes, each
	// under its own environment override (§23.7, §29.1).
	r.SetMetalGlint(e.Finish && r.effectEnv.metalGlint)
	r.SetMaterials(e.Finish && r.effectEnv.materials)
	// Distortion: the blast rings and the burning-vegetation heat shimmer,
	// which share one refraction pass (§25, §27.2).
	r.SetBlastDistortion(e.Distortion)
	r.SetTreeHeat(e.Distortion)
	// Marks: the fading scorch layer, under its environment override (§29.2).
	// The trail layer is recorder-side only, so it has no executor switch.
	r.SetScorch(e.Marks && r.effectEnv.scorch)
}

// Effects returns the last selection SetEffects applied. The zero value before
// the first call means the construction-time defaults are still in force.
func (r *Renderer) Effects() drawlist.Effects {
	if r == nil {
		return drawlist.Effects{}
	}
	return r.effects
}
