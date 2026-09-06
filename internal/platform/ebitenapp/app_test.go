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
