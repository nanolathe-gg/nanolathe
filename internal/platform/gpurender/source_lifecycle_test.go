package gpurender

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Repeated battles have new immutable source identities, even on the same map.
// Reset must release those identities and shared device pages without losing
// the process's shaders or display surfaces (DESIGN_GPU_RENDERER §2.3).
func TestLifecycleResetSourcesReleasesBattleResources(t *testing.T) {
	shared, tile, table, output := &ebiten.Image{}, &ebiten.Image{}, &ebiten.Image{}, &ebiten.Image{}
	shader := &ebiten.Shader{}
	frame := &formats.GAFFrame{}
	geometry := &drawlist.ModelGeometry{}
	r := &Renderer{
		tileAtlases:  map[tileAtlasKey]*tileAtlas{{terrain: &world.Terrain{}}: {pages: []*ebiten.Image{tile}}},
		gafImages:    map[*formats.GAFFrame]*ebiten.Image{frame: shared},
		scene:        sceneAtlas{pages: []*scenePage{{img: shared}}, frames: map[*formats.GAFFrame]sceneEntry{frame: {}}, pcx: map[*formats.PCX]sceneEntry{{}: {}}, fonts: map[*formats.FNT]*fntAtlas{{}: {}}},
		textureAtlas: modelTextureAtlas{slots: map[*formats.GAFFrame]modelTextureSlot{frame: {img: shared}}, page: shared},
		modelAtlas:   modelSlotAtlas{slots: map[*drawlist.ModelGeometry]modelSlot{geometry: {}}, page: modelPage{img: shared}},
		modelGroups:  modelStageAtlas{index: map[*drawlist.ModelGeometry]int{geometry: 0}},
		fog:          fogPass{shader: shader, compiled: true, atlas: shared, atlasGray: [4]*formats.GAFEntry{{}}},
		scene2D:      shader, surfaces: [2]*ebiten.Image{output}, w: 640, h: 480, modelPageLimit: 1024,
	}
	flash := &ebiten.Image{}
	r.sched.flash.img = flash
	r.tables.atlas = table
	r.sceneOpts.Images[0] = shared
	r.surfaceCache[0] = surfaceUpload{identity: 8, entry: sceneEntry{ok: true}}
	released := map[*ebiten.Image]int{}
	r.resetSources(func(img *ebiten.Image) { released[img]++ })
	if released[tile] != 1 || released[shared] != 1 || released[flash] != 1 || len(released) != 3 {
		t.Fatalf("shared source release counts=%v", released)
	}
	if len(r.tileAtlases) != 0 || len(r.gafImages) != 0 || len(r.scene.pages) != 0 || len(r.scene.frames) != 0 || len(r.scene.pcx) != 0 || len(r.scene.fonts) != 0 || len(r.textureAtlas.slots) != 0 || len(r.modelAtlas.slots) != 0 || len(r.modelGroups.index) != 0 {
		t.Fatal("previous battle source identities retained")
	}
	if r.fog.atlas != nil || r.fog.atlasGray[0] != nil || r.sceneOpts.Images[0] != nil || r.surfaceCache[0].identity != 0 {
		t.Fatal("compiled or paused-source dependencies retained")
	}
	if r.scene2D != shader || r.fog.shader != shader || !r.fog.compiled || r.tables.atlas != table || r.surfaces[0] != output || r.w != 640 || r.h != 480 || r.modelPageLimit != 1024 {
		t.Fatal("source reset changed renderer configuration")
	}
	r.resetSources(func(img *ebiten.Image) { t.Fatal("empty reset released an already retired image") })
}
