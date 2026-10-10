//go:build windows && !ebitenginevmguest

package ebitenapp

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// The Win32 clipboard as plain Unicode text, called with no cgo.
// https://learn.microsoft.com/en-us/windows/win32/dataxchg/using-the-clipboard
var (
	clipboardUser32       = syscall.NewLazyDLL("user32.dll")
	clipboardKernel32     = syscall.NewLazyDLL("kernel32.dll")
	clipboardOpen         = clipboardUser32.NewProc("OpenClipboard")
	clipboardClose        = clipboardUser32.NewProc("CloseClipboard")
	clipboardEmpty        = clipboardUser32.NewProc("EmptyClipboard")
	clipboardGetData      = clipboardUser32.NewProc("GetClipboardData")
	clipboardSetData      = clipboardUser32.NewProc("SetClipboardData")
	clipboardGlobalAlloc  = clipboardKernel32.NewProc("GlobalAlloc")
	clipboardGlobalFree   = clipboardKernel32.NewProc("GlobalFree")
	clipboardGlobalLock   = clipboardKernel32.NewProc("GlobalLock")
	clipboardGlobalUnlock = clipboardKernel32.NewProc("GlobalUnlock")
	clipboardGlobalSize   = clipboardKernel32.NewProc("GlobalSize")
)

const (
	cfUnicodeText = 13     // CF_UNICODETEXT: NUL-terminated UTF-16
	gmemMoveable  = 0x0002 // GMEM_MOVEABLE, which SetClipboardData requires
)

// Another program may hold the clipboard open for a moment, so opening it is
// retried for at most clipboardOpenWait. These are host bounds, not
// simulation values.
const (
	clipboardOpenWait  = 200 * time.Millisecond
	clipboardOpenRetry = 10 * time.Millisecond
)

// openClipboard opens the clipboard for the calling OS thread, which must
// stay locked until closeClipboard. No window owns the open: the bridge sets
// only immediately rendered CF_UNICODETEXT, never delayed formats that would
// need an owner window to render them.
func openClipboard() bool {
	deadline := time.Now().Add(clipboardOpenWait)
	for {
		if opened, _, _ := clipboardOpen.Call(0); opened != 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(clipboardOpenRetry)
	}
}

func closeClipboard() { clipboardClose.Call() }

// globalPointer turns a locked global memory handle's address into a
// pointer. The memory belongs to the system heap, not Go's, and stays valid
// until the matching GlobalUnlock.
func globalPointer(address uintptr) *uint16 {
	return *(**uint16)(unsafe.Pointer(&address))
}

// readHostClipboard runs only for a new paste key token. It copies the text
// into Go memory before unlocking the system's block; Windows owns the block
// and supplies CF_UNICODETEXT for CF_TEXT data itself.
func readHostClipboard() input.ClipboardText {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !openClipboard() {
		return input.ClipboardText{}
	}
	defer closeClipboard()
	handle, _, _ := clipboardGetData.Call(cfUnicodeText)
	if handle == 0 {
		return input.ClipboardText{}
	}
	address, _, _ := clipboardGlobalLock.Call(handle)
	if address == 0 {
		return input.ClipboardText{}
	}
	defer clipboardGlobalUnlock.Call(handle)
	size, _, _ := clipboardGlobalSize.Call(handle)
	units := unsafe.Slice(globalPointer(address), min(size/2, clipboardReadLimit))
	return input.ClipboardText{Text: clipboardUTF16Text(units), Available: true}
}

// HostClipboardWritable reports whether WriteHostClipboard has a native
// bridge on this host.
func HostClipboardWritable() bool { return true }

// WriteHostClipboard replaces the clipboard's contents with text as
// CF_UNICODETEXT, for a front-end Copy button, and reports whether Windows
// took it.
func WriteHostClipboard(text string) bool {
	units, err := syscall.UTF16FromString(text)
	if err != nil {
		return false // text with a NUL has no CF_UNICODETEXT form
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !openClipboard() {
		return false
	}
	defer closeClipboard()
	if emptied, _, _ := clipboardEmpty.Call(); emptied == 0 {
		return false
	}
	handle, _, _ := clipboardGlobalAlloc.Call(gmemMoveable, uintptr(len(units))*2)
	if handle == 0 {
		return false
	}
	address, _, _ := clipboardGlobalLock.Call(handle)
	if address == 0 {
		clipboardGlobalFree.Call(handle)
		return false
	}
	copy(unsafe.Slice(globalPointer(address), len(units)), units)
	clipboardGlobalUnlock.Call(handle)
	// The system owns the block once SetClipboardData succeeds; only a
	// refused block is still ours to free.
	if set, _, _ := clipboardSetData.Call(cfUnicodeText, handle); set == 0 {
		clipboardGlobalFree.Call(handle)
		return false
	}
	return true
}
