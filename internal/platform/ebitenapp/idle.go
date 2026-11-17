package ebitenapp

import (
	"runtime"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Paused windows wait for native input after their camera, resource easing and
// panel transitions settle. Retaining a framebuffer alone still runs the host
// loop and presents it at display refresh (DESIGN_PRESENTATION_CLIENT §2.1).
const pausedIdleDelay = time.Second

type idleInput struct {
	x, y      int32
	keys      [input.KeyCount]bool
	buttons   input.MouseButtons
	modifiers input.Modifiers
	command   bool
}

type idlePresentation struct {
	sleeping      bool
	maintenance   atomic.Bool
	timer         *time.Timer
	outside       [2]int
	layoutChanged bool
	input         idleInput
	view          client.PresentationInputs
	valid         bool
	quietAt       time.Time
}

func (p *idlePresentation) observeInput(now time.Time, sample sampledInput) {
	next := idleInput{sample.x, sample.y, sample.keys, sample.buttons, sample.modifiers, sample.command}
	active := p.inputChanged(sample) || next.buttons.Left || next.buttons.Middle || next.buttons.Right
	for _, held := range next.keys {
		active = active || held
	}
	if active || p.quietAt.IsZero() {
		p.quietAt = now
	}
	p.input = next
}

func (p *idlePresentation) inputChanged(sample sampledInput) bool {
	next := idleInput{sample.x, sample.y, sample.keys, sample.buttons, sample.modifiers, sample.command}
	return next != p.input ||
		sample.wheelX != 0 || sample.wheelY != 0 || sample.zoomWheelY != 0 || sample.zoomWheelX != 0 || sample.panX != 0 || sample.panY != 0 ||
		len(sample.pinches) != 0 || len(sample.characters) != 0 || len(sample.dropped) != 0
}

func (p *idlePresentation) stopTimer() {
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
}

// Only this timer's event can service audio without waking presentation. Real
// input, close, resize and focus changes still take the ordinary host path.
func (a *app) serviceResting() (bool, *sampledInput) {
	if !a.idle.sleeping || !a.closeRequested.IsZero() || !a.idle.maintenance.Swap(false) || a.idle.layoutChanged ||
		ebiten.IsWindowBeingClosed() || ebiten.IsFocused() != a.c.IsFocused() || ebiten.IsFullscreen() != a.fullscreen {
		return false, nil
	}
	sample := readInput(a.scaledInputNow())
	if a.idle.inputChanged(sample) {
		// Native scroll batches are drained by readInput. Transfer the waking
		// sample instead of polling again and losing its one-shot input.
		return false, &sample
	}
	a.serviceIdleAudio()
	return true, nil
}

func (a *app) serviceIdleAudio() {
	if a.options.IdleService == nil {
		return
	}
	if delay := a.options.IdleService(a.c.IsFocused()); delay > 0 {
		a.idle.timer = time.AfterFunc(delay, func() {
			a.idle.maintenance.Store(true)
			//lint:ignore SA1019 Minimum FPS mode needs an explicit native event wake for host audio.
			ebiten.ScheduleFrame()
		})
	}
}

func (p *idlePresentation) ready(now time.Time, eligible, focused bool, view client.PresentationInputs) bool {
	// Paused republications rotate snapshot storage, and every host step bumps
	// Epoch. Neither implies changed pixels. Fractions settle separately from
	// the actual camera samples, which remain part of this comparison.
	view.Epoch, view.Committed = 0, nil
	view.TickFraction16, view.CameraFraction16 = 0, 0
	if !eligible || !p.valid || p.view != view {
		p.quietAt = now
	}
	p.valid, p.view = eligible, view
	return eligible && (!focused || now.Sub(p.quietAt) >= pausedIdleDelay)
}

func (a *app) wakePresentation() {
	if !a.idle.sleeping {
		return
	}
	a.idle.sleeping = false
	a.idle.stopTimer()
	a.idle.maintenance.Store(false)
	a.idle.layoutChanged = false
	a.idle.quietAt = time.Now()
	// Sleeping is not missed camera/menu work. Service the waking input once
	// immediately, instead of issuing a five-step catch-up burst.
	a.hostClock = hostClock{}
	a.refresh = refreshEstimate{}
	a.presentedAt = time.Time{}
	a.pipe.lastDrawAt, a.pipe.tickAt = time.Time{}, time.Time{}
	a.pipe.period, a.pipe.launchPeriod, a.pipe.hasTick = 0, 0, false
	a.ledger = updateLedger{}
	ebiten.SetVsyncEnabled(true)
}

func (a *app) restPresentation() {
	if runtime.GOOS == "js" || a.idle.sleeping {
		return
	}
	eligible := a.c.PresentationPaused() && !a.c.ArrivalActive() &&
		!a.screenActive() && !a.overlayActive() && a.trace == nil && !a.exitPending && a.closeRequested.IsZero() && len(a.hostSamples) == 0
	if a.options.IdleAllowed != nil {
		eligible = eligible && a.options.IdleAllowed()
	}
	if !eligible {
		// An active modern frame may already have launched its recorder. Only
		// the paused path is quiescent enough to inspect client draw inputs.
		a.idle.valid = false
		return
	}
	if !a.idle.ready(time.Now(), eligible, a.c.IsFocused(), a.c.PresentationDigest()) {
		return
	}
	a.c.CancelPreRecord()
	a.pipe.armed = false
	a.idle.sleeping = true
	// This deprecated API still supplies the desktop event wait; screen
	// retention alone avoids raster work but does not stop Update/Draw calls.
	//lint:ignore SA1019 Screen retention does not provide the native event wait required here.
	ebiten.SetFPSMode(ebiten.FPSModeVsyncOffMinimum)
	a.serviceIdleAudio()
}
