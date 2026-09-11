//go:build darwin && !ebitenginevmguest

package ebitenapp

import (
	"fmt"

	"github.com/ebitengine/purego/objc"
)

// AppKit supplies momentumPhase on scroll events, but Ebitengine's public
// Wheel API exposes only summed deltas. Observe the events without consuming
// them so zoom can exclude inertia while GUI scrolling keeps it (§16.6 of
// DESIGN_GPU_RENDERER). NSEventMaskScrollWheel is defined by AppKit's NSEvent.h.
func startNativeScrollMonitor() (func(), error) {
	const scrollMask = uint64(1) << 22
	events := objc.ID(objc.GetClass("NSEvent"))
	block := objc.NewBlock(func(_ objc.Block, event objc.ID) objc.ID {
		recordNativeScroll(event)
		return event
	})
	monitor := events.Send(objc.RegisterName("addLocalMonitorForEventsMatchingMask:handler:"), scrollMask, block)
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
	if objc.Send[bool](event, objc.RegisterName("hasPreciseScrollingDeltas")) {
		// Match Ebitengine 2.10.1's Cocoa wheel-unit conversion. Both GUI and
		// zoom keep the existing sensitivity; this hook only separates inertia.
		x *= 0.1
		y *= 0.1
	}
	momentum := objc.Send[uint64](event, objc.RegisterName("momentumPhase")) != 0
	nativeScroll.add(x, y, momentum)
}
