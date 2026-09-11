package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
)

// ResetSources retires one terrain generation after the host has joined the
// recorder and finished submitting the previous frame. A restart loads fresh
// source identities, so keeping their maps for the process lifetime retains
// every earlier battle. Shaders, palette tables, output size and options survive
// (DESIGN_GPU_RENDERER §2.3 "Source lifetime").
func (r *Renderer) ResetSources() {
	r.resetSources(func(img *ebiten.Image) { img.Deallocate() })
}

// release is injectable so the ownership walk can be verified without opening
// a graphics device. Several source caches can share the same image.
func (r *Renderer) resetSources(release func(*ebiten.Image)) {
	if r == nil {
		return
	}
	seen := make(map[*ebiten.Image]struct{})
	retire := func(img *ebiten.Image) {
		if img == nil {
			return
		}
		if _, ok := seen[img]; ok {
			return
		}
		seen[img] = struct{}{}
		release(img)
	}
	// These maps are resource owners only. Retirement order cannot change any
	// submitted pixels or simulation state; all previous work is enqueued [I6].
	for _, a := range r.tileAtlases {
		if a != nil {
			for _, img := range a.pages {
				retire(img)
			}
		}
	}
	for _, img := range r.gafImages {
		retire(img)
	}
	for _, p := range r.scene.pages {
		if p != nil {
			retire(p.img)
		}
	}
	for _, slot := range r.textureAtlas.slots {
		retire(slot.img)
	}
	retire(r.textureAtlas.page)
	for _, p := range []*modelPage{&r.modelAtlas.page, &r.modelAtlas.overflow[0], &r.modelAtlas.overflow[1]} {
		retire(p.img)
		retire(p.key)
		retire(p.post)
	}
	retire(r.modelAtlas.quads.img)
	retire(r.modelGroups.a)
	retire(r.modelGroups.b)
	retire(r.modelGroups.out)
	retire(r.modelStage)
	retire(r.modelStageScratch)
	retire(r.modelStageOut)
	retire(r.fog.atlas)
	retire(r.fog.grid)
	retire(r.pointPlane.img)
	retire(r.sched.flash.img)

	r.tileAtlases = make(map[tileAtlasKey]*tileAtlas)
	r.gafImages = make(map[*formats.GAFFrame]*ebiten.Image)
	r.scene = sceneAtlas{
		frames: make(map[*formats.GAFFrame]sceneEntry),
		pcx:    make(map[*formats.PCX]sceneEntry),
		fonts:  make(map[*formats.FNT]*fntAtlas),
	}
	r.textureAtlas = modelTextureAtlas{}
	r.modelAtlas = modelSlotAtlas{}
	r.modelGroups = modelStageAtlas{}
	r.modelPrep = modelPrepScratch{}
	r.modelStage, r.modelStageScratch, r.modelStageOut = nil, nil, nil
	r.modelStageW, r.modelStageH = 0, 0
	r.fog = fogPass{shader: r.fog.shader, shaderErr: r.fog.shaderErr, compiled: r.fog.compiled}
	// Retained compiled runs and options also reference source images. Drop
	// those and frame scratch together; nothing from the old frame is replayable.
	r.sched = scheduler{}
	r.sceneOpts = ebiten.DrawTrianglesShaderOptions{}
	r.modelOpts = ebiten.DrawTrianglesShaderOptions{}
	r.surfaceDynamic = surfaceUpload{}
	r.surfaceCache = [4]surfaceUpload{}
	r.pointPlane = pointPlane{}
	r.pointRows = nil
	r.pointRuns = nil
	r.pointGroups = nil
	r.pointGroupIdx = nil
	r.lastDest = nil
}
