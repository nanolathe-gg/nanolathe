package ebitenapp

import "testing"

func TestConsumePresentationOncePerUpdate(t *testing.T) {
	a := app{}
	if a.consumePresentation() {
		t.Fatal("new app unexpectedly has a presentation pending")
	}

	a.presentPending = true
	if !a.consumePresentation() {
		t.Fatal("update did not permit a presentation")
	}
	if a.consumePresentation() {
		t.Fatal("presentation was not consumed")
	}

	// Several updates before the next VSync still require only the latest
	// committed state to be presented once.
	a.presentPending = true
	a.presentPending = true
	if !a.consumePresentation() || a.consumePresentation() {
		t.Fatal("multiple updates did not coalesce to one presentation")
	}
}

// A process that never entered Run owns no window, so the desktop query must
// answer from the seam rather than reaching the window layer: the bring-up
// wants the process's own main thread and faults on a test goroutine. (0, 0)
// is the headless answer the display-mode gate already expects.
func TestDesktopSizeIsHeadlessWithoutAWindow(t *testing.T) {
	if windowOwned.Load() {
		t.Fatal("a test process reported owning a window")
	}
	if w, h := DesktopSize(); w != 0 || h != 0 {
		t.Fatalf("headless desktop size = %dx%d, want 0x0", w, h)
	}
}
