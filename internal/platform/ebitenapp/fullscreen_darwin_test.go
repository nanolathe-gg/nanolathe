//go:build darwin && !ebitenginevmguest

package ebitenapp

import "testing"

func TestFullscreenPresentationLeavesSystemEscapeRoutesEnabled(t *testing.T) {
	// Native fullscreen normally requests auto-hide. Toolbar auto-hide is
	// another legal input, but cannot coexist with a fully hidden menu bar.
	const nativeFullscreen = uintptr(1 << 10)
	const disableSwitchingOrForceQuit = uintptr(1<<5 | 1<<6)
	for _, current := range []uintptr{0, 1029, 3077} {
		got := fullscreenPresentationOptions(current)
		want := current&nativeFullscreen | 10 // HideDock | HideMenuBar
		if got != want || got&disableSwitchingOrForceQuit != 0 {
			t.Fatalf("presentation options %d: got %d, want %d with system escape routes enabled", current, got, want)
		}
		if again := fullscreenPresentationOptions(got); again != got {
			t.Fatalf("reconciling unchanged options: got %d, want %d", again, got)
		}
	}
}

func TestFullscreenPresentationRestoresWindowedPolicyOnly(t *testing.T) {
	// AppKit owns the fullscreen bit. Restore the previous edge policy without
	// clobbering a different option changed after startup.
	const unrelated = uintptr(1 << 12) // DisableCursorLocationAssistance
	for _, windowed := range []uintptr{0, 1, 5, 10} {
		for _, native := range []uintptr{0, 1 << 10} {
			current := fullscreenPresentationOptions(native | unrelated)
			if got, want := restoreFullscreenPresentationOptions(current, windowed), native|unrelated|windowed; got != want {
				t.Fatalf("restore %d from %d: got %d, want %d", windowed, current, got, want)
			}
		}
	}
}
