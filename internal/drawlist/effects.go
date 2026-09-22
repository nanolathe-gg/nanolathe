package drawlist

// Effects is the player's Enhanced presentation switch set
// (docs/DESIGN_GPU_RENDERER.md §30). It is a plain value so the recorder and
// the modern executor can hold and compare the same selection without either
// importing the settings package; the shell converts the persisted integers.
//
// Every field is a modern presentation choice. Classic records and composes the
// same pixels whatever the set says, and no field reaches simulation state [I6].
type Effects struct {
	// TeamNanospray opts into builder-coloured spray (GPU design §23.6).
	TeamNanospray bool
	// Water gates the coastal water surface, surface wakes and hover dust,
	// building foam, water motion and the screen-space reflections (§26).
	Water bool
	// Lighting gates the battle lighting prototype: explosion and nanolathe
	// illumination of models and smoke (§23).
	Lighting bool
	// Finish gates the metallic glint and the metal/paint material finishes
	// (§23.7, §29.1).
	Finish bool
	// Distortion gates the blast rings (§25), the burning-vegetation heat
	// shimmer (§27) and the fresh-wreck shimmer with its cooling emission
	// colour (§28) — one prototype, kept together.
	Distortion bool
	// Marks gates the fading scorch marks (§29.2) and the trail layer of
	// footprints and tracks (§15).
	Marks bool
}

// AllEffects is the default selection: all effect families on; optional team
// nanospray stays off to preserve the standard green appearance.
func AllEffects() Effects {
	return Effects{Water: true, Lighting: true, Finish: true, Distortion: true, Marks: true}
}
