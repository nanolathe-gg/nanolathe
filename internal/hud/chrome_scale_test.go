package hud

import "testing"

// Auto steps up at 1440 rows; a fixed choice is used as chosen
// (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
func TestChromeScale(t *testing.T) {
	cases := []struct {
		pref    int
		screenH int32
		want    int32
	}{
		{0, 480, 1}, {0, 1080, 1}, {0, 1439, 1}, {0, 1440, 2}, {0, 2160, 2}, {0, 4320, 2},
		{1, 2160, 1},
		{2, 720, 2}, {2, 1350, 2},
		{3, 2160, 2},
	}
	for _, c := range cases {
		if got := ChromeScale(c.pref, c.screenH, 2); got != c.want {
			t.Errorf("ChromeScale(%d, %d) = %d, want %d", c.pref, c.screenH, got, c.want)
		}
	}
}
