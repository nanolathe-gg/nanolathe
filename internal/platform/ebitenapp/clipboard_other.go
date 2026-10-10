//go:build (!darwin && !js && !windows && (!unix || android)) || ebitenginevmguest

package ebitenapp

import "github.com/nanolathe-gg/nanolathe/internal/input"

func readHostClipboard() input.ClipboardText {
	// TODO(T25): supply the native clipboard bridge for this host. Missing
	// access preserves editor text, including on the VM guest [07 §2].
	return input.ClipboardText{}
}

// HostClipboardWritable reports that this host has no clipboard bridge, so a
// front-end Copy button is hidden.
func HostClipboardWritable() bool { return false }

// WriteHostClipboard has no bridge on this host and reports failure.
func WriteHostClipboard(string) bool {
	// TODO(T25): supply the native clipboard bridge for this host.
	return false
}
