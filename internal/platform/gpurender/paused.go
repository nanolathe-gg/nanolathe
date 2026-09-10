package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// ExecuteOver restores a previously completed world composite, then executes
// a foreground-only list in the same destination. This preserves fog-before-UI
// and destination-reading modal/overlay semantics without replaying the world
// (DESIGN_GPU_RENDERER §13.10). The background must be independent of this
// renderer's returned composite, and the list must not contain a frame clear.
func (r *Renderer) ExecuteOver(list *drawlist.List, background *ebiten.Image, w, h int) *ebiten.Image {
	if r == nil || list == nil || background == nil || w <= 0 || h <= 0 {
		return nil
	}
	r.ensureSize(w, h)
	options := ebiten.DrawImageOptions{Blend: ebiten.BlendCopy}
	r.surfaces[0].DrawImage(background, &options)
	return r.Execute(list, w, h)
}
