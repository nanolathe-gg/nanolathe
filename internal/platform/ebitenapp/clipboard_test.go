package ebitenapp

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

func TestPortableClipboardTextPreservesASCIIAndRejectsUnmappedText(t *testing.T) {
	for _, text := range []string{"", "plain +command", "one\r\ntwo\t\x7f"} {
		want := input.ClipboardText{Text: text, Available: true}
		if got := portableClipboardText(want); got != want {
			t.Fatalf("ASCII %q changed to %+v", text, got)
		}
	}
	for _, value := range []input.ClipboardText{{Text: "stale"}, {Text: "café", Available: true}} {
		if got := portableClipboardText(value); got.Available || got.Text != "" {
			t.Fatalf("unavailable/unmapped clipboard became %+v", got)
		}
	}
	if got := portableClipboardText(input.ClipboardText{Text: "ok\x00é", Available: true}); got != (input.ClipboardText{Text: "ok", Available: true}) {
		t.Fatalf("NUL termination = %+v", got)
	}
}
