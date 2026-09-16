//go:build darwin && !ebitenginevmguest

package ebitenapp

import (
	"github.com/ebitengine/purego/objc"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// readHostClipboard runs only for a new paste key token. The host owns the
// Unicode string; copy it into immutable Go text before releasing the pool.
func readHostClipboard() input.ClipboardText {
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	board := objc.ID(objc.GetClass("NSPasteboard")).Send(objc.RegisterName("generalPasteboard"))
	return clipboardTextFromPasteboard(board)
}

func clipboardTextFromPasteboard(board objc.ID) input.ClipboardText {
	kind := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), "public.utf8-plain-text")
	value := board.Send(objc.RegisterName("stringForType:"), kind)
	if value == 0 {
		return input.ClipboardText{}
	}
	return input.ClipboardText{Text: objc.Send[string](value, objc.RegisterName("UTF8String")), Available: true}
}
