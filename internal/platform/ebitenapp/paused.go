package ebitenapp

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/client"
)

// pausedWorld retains one screen-sized GPU image, never a second model list or
// copy of model geometry. Foreground draws are restored over it every present.
type pausedWorld struct {
	image           *ebiten.Image
	inputs          client.PausedWorldInputs
	valid           bool
	records, reuses uint64
}

func (p *pausedWorld) clear() {
	p.valid = false
	if p.image != nil {
		p.image.Deallocate()
		p.image = nil
	}
}

// drawPaused presents a split frame when reuse is eligible. Audio/resource
// advancement and fraction sampling have already run. Its caller always runs
// the ordinary update-ledger tail, even when the world was reused (§13.10).
func (a *app) drawPaused(screen *ebiten.Image, width, height int) bool {
	inputs, eligible := a.c.PausedWorldDigest()
	if !eligible || width <= 0 || height <= 0 {
		a.paused.clear()
		return false
	}
	// The last running Draw may have launched a speculative full frame. It
	// must be discarded before either split pass reuses its list and arenas.
	a.c.CancelPreRecord()
	a.pipe.armed = false
	a.gpu.SetDisplayPalette(a.c.DisplayPalette())
	a.gpu.SetGlow(a.c.Glow())
	a.gpu.SetGlowStrength(a.c.GlowStrength())
	a.gpu.SetGlowFamilies(a.c.GlowFamilies())
	// A changed effect selection advances the client's paused-world revision,
	// so the digest below already rejects the cached raster (§30).
	a.gpu.SetEffects(a.c.Effects())
	if !a.paused.valid || a.paused.inputs != inputs {
		world := a.gpu.Execute(a.c.RecordPausedWorld(), width, height)
		if world == nil {
			a.paused.clear()
			return false
		}
		if a.paused.image == nil || a.paused.image.Bounds().Dx() != width || a.paused.image.Bounds().Dy() != height {
			a.paused.clear()
			a.paused.image = ebiten.NewImage(width, height)
		}
		a.paused.image.DrawImage(world, &ebiten.DrawImageOptions{Blend: ebiten.BlendCopy})
		a.paused.inputs, a.paused.valid = inputs, true
		a.paused.records++
	} else {
		a.paused.reuses++
	}
	foreground := a.c.RecordPausedForeground()
	x, y := ebiten.CursorPosition()
	a.c.PositionPresentationCursor(foreground, x, y)
	if img := a.gpu.ExecuteOver(foreground, a.paused.image, width, height); img != nil {
		a.c.CommitStrategicPresentation()
		screen.DrawImage(img, &ebiten.DrawImageOptions{})
	}
	a.pipe.synchronous++ // a live foreground was presented without speculation
	return true
}
