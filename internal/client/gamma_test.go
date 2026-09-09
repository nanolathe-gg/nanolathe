package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"testing"
)

func TestGammaUsesAuthoredChannelsAndLowByteTruncation(t *testing.T) {
	c, err := New(Options{Width: 1, Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	var p palette.Tables
	p.Base[7] = [4]byte{3, 170, 255, 0}
	original := p
	c.SetPalette(&p)
	if c.DisplayPalette()[7] != p.Base[7] {
		t.Fatal("initial factor changed palette")
	}
	for _, tc := range []struct {
		factor float32
		want   [4]byte
	}{
		{1.5, [4]byte{4, 255, 255, 0}},
		{-0.5, [4]byte{255, 171, 129, 0}},
		{1, [4]byte{3, 170, 255, 0}},
	} {
		c.SetGammaFactor(tc.factor)
		if got := c.DisplayPalette()[7]; got != tc.want {
			t.Fatalf("factor %g: %v, want %v", tc.factor, got, tc.want)
		}
	}
	c.SetGammaFactor(0.5)
	replacement := p
	replacement.Base[7][0] = 9
	c.SetPalette(&replacement)
	if got := c.DisplayPalette()[7][0]; got != 4 {
		t.Fatalf("replacement palette channel = %d", got)
	}
	if p != original {
		t.Fatal("gamma modified authored tables")
	}
}
