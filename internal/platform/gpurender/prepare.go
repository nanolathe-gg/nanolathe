package gpurender

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// PrepareTerrain uploads the two terrain atlas scales at the loading boundary.
// It uses precisely the keys Execute will request: native omits detail art,
// detail includes the installed provider or nearest-doubles the native tiles.
// Repeated calls reuse the same pages; ResetSources owns their disposal.
// This moves one-time work and memory to loading, not a retail behavior change
// (DESIGN_GPU_RENDERER §14.8). Call only on the graphics-device owner.
func (r *Renderer) PrepareTerrain(sources drawlist.Terrain) {
	if r == nil {
		return
	}
	r.atlasFor(sources.Terrain, nil, camera.ViewScaleNative)
	r.atlasFor(sources.Terrain, sources.Detail, camera.ViewScaleDetail)
}
