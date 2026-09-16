package ebitenapp

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// portableClipboardText preserves ASCII bytes, including control bytes that
// retail paste does not send through printable admission [07 §2].
func portableClipboardText(value input.ClipboardText) input.ClipboardText {
	if !value.Available {
		return input.ClipboardText{}
	}
	if end := strings.IndexByte(value.Text, 0); end >= 0 {
		value.Text = value.Text[:end]
	}
	for i := range len(value.Text) {
		if value.Text[i] > 0x7f {
			// TODO(T25): establish Unicode-to-retail-codepage conversion;
			// preserve the editor rather than inventing replacement bytes [07 §2].
			return input.ClipboardText{}
		}
	}
	return value
}
