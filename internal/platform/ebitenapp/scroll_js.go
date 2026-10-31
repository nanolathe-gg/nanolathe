//go:build js && wasm && !ebitenginevmguest

package ebitenapp

import (
	"syscall/js"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// The child page owns browser event cancellation and gesture lifetimes;
// this bridge only publishes presentation input (DESIGN_BROWSER_HOST §4 contract 8).
func startNativeScrollMonitor() (func(), error) {
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 {
			return nil
		}
		e := args[0]
		var batch scrollBatch
		point := &[2]float64{e.Get("atX").Float(), e.Get("atY").Float()}
		switch e.Get("kind").String() {
		case "pointer":
		case "wheel":
			batch.x, batch.y = e.Get("x").Float(), e.Get("y").Float()
			if e.Get("pixels").Bool() {
				batch.panX, batch.panY = batch.x, batch.y
			} else {
				batch.zoomX, batch.zoomY = batch.x, batch.y
			}
		case "pan":
			batch.panX, batch.panY = e.Get("x").Float(), e.Get("y").Float()
		case "pinch":
			batch.pinches = []input.PinchEvent{{
				Delta: e.Get("delta").Float(), Began: e.Get("began").Bool(),
				Ended: e.Get("ended").Bool(), Cancelled: e.Get("cancelled").Bool(),
			}}
			if !batch.pinches[0].Began && (batch.pinches[0].Ended || batch.pinches[0].Cancelled) {
				point = nil // quiet timers must not move a newer mouse position
			}
		default:
			return nil
		}
		nativeScroll.collect(batch, point)
		return nil
	})
	nativeScroll.setActive(true)
	js.Global().Set("nanolatheBrowserGesture", callback)
	js.Global().Set("nanolatheBrowserGestureEnabled", true)
	return func() {
		js.Global().Delete("nanolatheBrowserGesture")
		js.Global().Delete("nanolatheBrowserGestureEnabled")
		nativeScroll.setActive(false)
		callback.Release()
	}, nil
}

// Full-window tools poll Ebiten directly. Leave their wheel stream intact,
// clear pending camera input, and retire browser gesture state on ownership
// changes so returning to battle cannot replay a settings-screen gesture.
func browserGestureScreenOwned(owned bool) {
	global := js.Global()
	enabled := global.Get("nanolatheBrowserGestureEnabled")
	if enabled.Type() != js.TypeBoolean || enabled.Bool() == !owned {
		return
	}
	if cancel := global.Get("nanolatheBrowserCancelGestures"); cancel.Type() == js.TypeFunction {
		cancel.Invoke()
	}
	nativeScroll.setActive(!owned)
	global.Set("nanolatheBrowserGestureEnabled", !owned)
}
