//go:build darwin && !ebitenginevmguest

package ebitenapp

import (
	"testing"

	"github.com/ebitengine/purego/objc"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

func TestNativeClipboardReadsPrivatePasteboard(t *testing.T) {
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	// A unique pasteboard verifies the native bridge without changing the
	// user's clipboard. This is a host check, not a retail behavior claim.
	board := objc.ID(objc.GetClass("NSPasteboard")).Send(objc.RegisterName("pasteboardWithUniqueName"))
	if board == 0 {
		t.Skip("native pasteboard service unavailable")
	}
	defer board.Send(objc.RegisterName("releaseGlobally"))
	if got := clipboardTextFromPasteboard(board); got.Available {
		t.Fatalf("missing text format was available: %+v", got)
	}
	kind := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), "public.utf8-plain-text")
	for _, text := range []string{"+showranges", ""} {
		board.Send(objc.RegisterName("clearContents"))
		value := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), text)
		if !objc.Send[bool](board, objc.RegisterName("setString:forType:"), value, kind) {
			t.Fatal("could not seed private pasteboard")
		}
		if got := clipboardTextFromPasteboard(board); got != (input.ClipboardText{Text: text, Available: true}) {
			t.Fatalf("native read = %+v, want successful %q", got, text)
		}
	}
}
