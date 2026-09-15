package ebitenapp

import "github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"

// prepareBattlePresentation runs at successful loading completion on the game
// goroutine, before the next interactive frame. The source-generation barrier
// runs first so adoption cannot discard the pages just prepared (§14.8).
func (a *app) prepareBattlePresentation() {
	if a.mode != RendererModern {
		return
	}
	a.c.CancelPreRecord()
	a.pipe.armed = false
	a.syncRendererSources()
	if a.gpu == nil {
		w, h := a.c.Size()
		a.gpu = gpurender.New(a.c.PaletteTables(), w, h)
	}
	a.c.SetEnhanced(true)
	a.gpu.PrepareTerrain(a.c.BattleTerrainSources())
	a.sourcesPrepared = true
}
