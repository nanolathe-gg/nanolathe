//go:build windows && !ebitenginevmguest

package ebitenapp

import (
	"syscall"
	"unsafe"

	"github.com/hajimehoshi/ebiten/v2"
)

var (
	clipUser32              = syscall.NewLazyDLL("user32.dll")
	clipGetActiveWindow     = clipUser32.NewProc("GetActiveWindow")
	clipGetForegroundWindow = clipUser32.NewProc("GetForegroundWindow")
	clipGetClientRect       = clipUser32.NewProc("GetClientRect")
	clipClientToScreen      = clipUser32.NewProc("ClientToScreen")
	clipGetClipCursor       = clipUser32.NewProc("GetClipCursor")
	clipClipCursor          = clipUser32.NewProc("ClipCursor")
)

// Win32 POINT and RECT members are signed 32-bit LONGs on every architecture.
type clipPoint struct{ X, Y int32 }
type clipRect struct{ Left, Top, Right, Bottom int32 }

// nativeCursorClip confines the pointer to the presented canvas while the
// window is fullscreen and in the foreground (presentedCursorRect). ClipCursor
// is a desktop-wide setting that other windows, Ebitengine's own capture mode
// and system events replace, so it is reconciled on every focused host update
// and released on focus loss, windowed mode and shutdown.
// https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-clipcursor
type nativeCursorClip struct {
	applied bool
}

func (c *nativeCursorClip) update(fullscreen, focused, captured bool, logicalW, logicalH int) {
	if c == nil {
		return
	}
	if captured {
		// Captured mode clips to the client area itself and clears the clip
		// when it ends; the next update reapplies the canvas rectangle.
		c.applied = false
		return
	}
	if !fullscreen || !focused {
		c.release()
		return
	}
	ebiten.RunOnMainThread(func() {
		if !c.confine(logicalW, logicalH) {
			c.release()
		}
	})
}

// confine runs on the window's thread: GetActiveWindow reads that thread's
// message queue, and requiring it to be the foreground window avoids clipping
// for a stale focus bit after another application activates.
func (c *nativeCursorClip) confine(logicalW, logicalH int) bool {
	window, _, _ := clipGetActiveWindow.Call()
	foreground, _, _ := clipGetForegroundWindow.Call()
	if window == 0 || window != foreground {
		return false
	}
	var client clipRect
	if ok, _, _ := clipGetClientRect.Call(window, uintptr(unsafe.Pointer(&client))); ok == 0 {
		return false
	}
	var origin clipPoint
	if ok, _, _ := clipClientToScreen.Call(window, uintptr(unsafe.Pointer(&origin))); ok == 0 {
		return false
	}
	// Ebitengine's window thread is per-monitor DPI aware, so the client
	// rectangle, screen coordinates and ClipCursor all use device pixels.
	left, top, right, bottom, ok := presentedCursorRect(int(client.Right), int(client.Bottom), logicalW, logicalH)
	if !ok {
		return false
	}
	want := clipRect{origin.X + left, origin.Y + top, origin.X + right, origin.Y + bottom}
	var current clipRect
	if got, _, _ := clipGetClipCursor.Call(uintptr(unsafe.Pointer(&current))); got != 0 && current == want {
		c.applied = true
		return true
	}
	if ok, _, _ := clipClipCursor.Call(uintptr(unsafe.Pointer(&want))); ok == 0 {
		return false
	}
	c.applied = true
	return true
}

// release clears only a clip this adapter set. ClipCursor is not bound to a
// thread, so shutdown may call it after RunGame returns.
func (c *nativeCursorClip) release() {
	if c == nil || !c.applied {
		return
	}
	clipClipCursor.Call(0)
	c.applied = false
}
