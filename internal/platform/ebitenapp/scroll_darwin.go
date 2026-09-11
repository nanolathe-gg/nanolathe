//go:build darwin && !ebitenginevmguest

package ebitenapp

import (
	"fmt"

	"github.com/ebitengine/purego/objc"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Observe AppKit scroll and magnify events without consuming them (§16.6 of
// DESIGN_GPU_RENDERER). Ebitengine's Wheel API loses precision and momentum
// metadata and does not expose native pinch gestures. Event masks and phases
// below are the public AppKit constants from NSEvent.h.
func startNativeScrollMonitor() (func(), error) {
	const (
		scrollEvent    = 22
		magnifyEvent   = 30
		phaseBegan     = 1
		phaseEnded     = 8
		phaseCancelled = 16
		gestureMask    = uint64(1)<<scrollEvent | uint64(1)<<magnifyEvent
	)
	events := objc.ID(objc.GetClass("NSEvent"))
	block := objc.NewBlock(func(_ objc.Block, event objc.ID) objc.ID {
		switch objc.Send[uint64](event, objc.RegisterName("type")) {
		case scrollEvent:
			recordNativeScroll(event)
		case magnifyEvent:
			phase := objc.Send[uint64](event, objc.RegisterName("phase"))
			nativeScroll.pinch(input.PinchEvent{
				Delta: objc.Send[float64](event, objc.RegisterName("magnification")),
				Began: phase&phaseBegan != 0, Ended: phase&phaseEnded != 0, Cancelled: phase&phaseCancelled != 0,
			})
		}
		return event
	})
	monitor := events.Send(objc.RegisterName("addLocalMonitorForEventsMatchingMask:handler:"), gestureMask, block)
	if monitor == 0 {
		block.Release()
		return nil, fmt.Errorf("nanolathe: install scroll monitor: logical path AppKit/NSEvent, providers searched [AppKit], expected local scroll event monitor")
	}
	nativeScroll.setActive(true)
	return func() {
		events.Send(objc.RegisterName("removeMonitor:"), monitor)
		nativeScroll.setActive(false)
		block.Release()
	}, nil
}

func recordNativeScroll(event objc.ID) {
	x := objc.Send[float64](event, objc.RegisterName("scrollingDeltaX"))
	y := objc.Send[float64](event, objc.RegisterName("scrollingDeltaY"))
	precise := objc.Send[bool](event, objc.RegisterName("hasPreciseScrollingDeltas"))
	momentum := objc.Send[uint64](event, objc.RegisterName("momentumPhase")) != 0
	nativeScroll.add(x, y, precise, momentum)
}
