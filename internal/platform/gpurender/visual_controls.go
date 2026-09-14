package gpurender

import "github.com/nanolathe-gg/nanolathe/internal/drawlist"

// setMaterials and setScorch are the executor gates the Finish and Marks
// switches drive (GPU design §29, §30). They never change simulation or saved
// state.
func (r *Renderer) setMaterials(enabled bool) { r.materialsEnabled = enabled }
func (r *Renderer) setScorch(enabled bool)    { r.scorchEnabled = enabled }

// SetEffects applies the player's Enhanced effect selection (§30). It is the
// single entry point to every executor gate: each family's switch is set from
// the selection and from nothing else, so a family is on exactly when its
// switch is. The per-family setters below it are package-internal.
//
// Nothing here allocates, compiles or submits; each assignment is read by the
// owning pass on its next frame. A source reset preserves these switches, so an
// executor swap or a new battle keeps the selection that was last applied.
func (r *Renderer) SetEffects(e drawlist.Effects) {
	if r == nil {
		return
	}
	r.effects = e
	// Water: the coastal surface with its wakes and foam, and the above-water
	// screen-space reflections (§26.1, §26.4).
	r.setWaterEffects(e.Water)
	r.setWaterReflections(e.Water)
	// Lighting: the bounded explosion and nanolathe illumination pass (§23).
	r.setBattleLighting(e.Lighting)
	// Finish: the metallic glint and the metal/paint material finishes
	// (§23.7, §29.1).
	r.setMetalGlint(e.Finish)
	r.setMaterials(e.Finish)
	// Distortion: the blast rings and the burning-vegetation heat shimmer,
	// which share one refraction pass (§25, §27.2).
	r.setBlastDistortion(e.Distortion)
	r.setTreeHeat(e.Distortion)
	// Marks: the fading scorch layer (§29.2). The trail layer is recorder-side
	// only, so it has no executor switch.
	r.setScorch(e.Marks)
}

// Effects returns the last selection SetEffects applied. New applies the
// all-on construction default, so this is never the zero value on a renderer
// the constructor produced.
func (r *Renderer) Effects() drawlist.Effects {
	if r == nil {
		return drawlist.Effects{}
	}
	return r.effects
}
