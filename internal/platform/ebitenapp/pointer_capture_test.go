package ebitenapp

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"testing"
)

func TestFocusLossReleasesAfterClientFrameReset(t *testing.T) {
	var c *client.Client
	steppedCaptured := false
	var err error
	c, err = client.New(client.Options{Width: 64, Height: 64, Step: func(float64) {
		steppedCaptured = c.PointerCaptured()
	}})
	if err != nil {
		t.Fatal(err)
	}
	c.SetPointerCaptured(true)
	c.SetFocused(false)
	(&app{c: c}).stepClient()
	if !steppedCaptured || c.PointerCaptured() {
		t.Fatal("focus release occurred before the client cleared prior-frame cursor restoration")
	}
}
