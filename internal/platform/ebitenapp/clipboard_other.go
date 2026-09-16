//go:build !darwin || ebitenginevmguest

package ebitenapp

import "github.com/nanolathe-gg/nanolathe/internal/input"

func readHostClipboard() input.ClipboardText {
	// TODO(T25): supply the native clipboard bridge for this host. Missing
	// access preserves editor text, including on the VM guest [07 §2].
	return input.ClipboardText{}
}
