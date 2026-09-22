//go:build darwin && !ebitenginevmguest

package ebitenapp

import (
	"github.com/ebitengine/purego/objc"
	"github.com/hajimehoshi/ebiten/v2"
)

// Public NSApplicationPresentationOptions from AppKit/NSApplication.h. Hiding
// the menu bar requires hiding the Dock, and excludes both auto-hide flags and
// autoHideToolbar (which requires autoHideMenuBar).
const (
	presentationAutoHideDock    uintptr = 1 << 0
	presentationHideDock        uintptr = 1 << 1
	presentationAutoHideMenuBar uintptr = 1 << 2
	presentationHideMenuBar     uintptr = 1 << 3
	presentationAutoHideToolbar uintptr = 1 << 11
	presentationEdgeControls            = presentationAutoHideDock | presentationHideDock |
		presentationAutoHideMenuBar | presentationHideMenuBar | presentationAutoHideToolbar
)

// nativeFullscreenPresentation keeps the top edge available to camera input
// (DESIGN_PRESENTATION_CLIENT §2.1). This is an application policy, not a system
// preference or a replacement for Ebitengine's native fullscreen delegate.
type nativeFullscreenPresentation struct {
	application     objc.ID
	windowedOptions uintptr
	applied         bool
}

// Called before RunGame, on the main thread, while the app is still windowed.
func startNativeFullscreenPresentation() *nativeFullscreenPresentation {
	application := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	return &nativeFullscreenPresentation{
		application:     application,
		windowedOptions: objc.Send[uintptr](application, objc.RegisterName("presentationOptions")),
	}
}

func fullscreenPresentationOptions(current uintptr) uintptr {
	return current&^presentationEdgeControls | presentationHideDock | presentationHideMenuBar
}

func restoreFullscreenPresentationOptions(current, windowed uintptr) uintptr {
	return current&^presentationEdgeControls | windowed&presentationEdgeControls
}

// AppKit recalculates its presentation options during fullscreen transitions
// and when returning from another Space. Reconcile while focused rather than
// setting them only once at entry. The setter runs only when they differ.
func (p *nativeFullscreenPresentation) update(fullscreen, focused bool) {
	if p == nil || (!fullscreen && !p.applied) || (fullscreen && !focused) {
		return
	}
	ebiten.RunOnMainThread(func() {
		p.apply(fullscreen)
	})
}

// apply runs on AppKit's main thread. Restore only the flags we own, preserving
// native fullscreen and unrelated application options. In particular, never
// add the flags that disable application switching or Force Quit.
func (p *nativeFullscreenPresentation) apply(fullscreen bool) {
	current := objc.Send[uintptr](p.application, objc.RegisterName("presentationOptions"))
	next := restoreFullscreenPresentationOptions(current, p.windowedOptions)
	if fullscreen {
		next = fullscreenPresentationOptions(current)
	}
	if next != current {
		p.application.Send(objc.RegisterName("setPresentationOptions:"), next)
	}
	p.applied = fullscreen
}

// RunGame has returned to the main thread; do not call RunOnMainThread here.
func (p *nativeFullscreenPresentation) close() {
	if p != nil && p.applied {
		p.apply(false)
	}
}
