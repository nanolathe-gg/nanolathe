package client

import "testing"

func TestMoviePaletteRestoresGammaPalette(t *testing.T) {
	c := newUIBlitClient(t)
	c.SetGammaFactor(1.5)
	before := c.DisplayPalette()
	var movie [256][4]byte
	movie[3] = [4]byte{10, 20, 30, 255}
	c.SetMoviePalette(&movie)
	if c.DisplayPalette() != movie {
		t.Fatal("movie palette was gamma corrected")
	}
	c.SetMoviePalette(nil)
	if c.DisplayPalette() != before {
		t.Fatal("movie left the menu palette changed")
	}
}
