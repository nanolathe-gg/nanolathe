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

// Over a whole surface, Auto also keeps the bars beside a magnified rail at
// least 512 virtual columns wide; a chosen size applies regardless.
func TestSurfaceChromeScale(t *testing.T) {
	cases := []struct {
		pref          int
		screenW, rows int32
		want          int32
	}{
		{0, 1920, 1080, 1}, {0, 2560, 1440, 2}, {0, 3840, 2160, 2},
		{0, 1281, 1440, 2}, {0, 1280, 1440, 1}, // (1280-257)/2 = 511 columns
		{2, 1280, 1440, 2}, {1, 3840, 2160, 1},
	}
	for _, c := range cases {
		if got := SurfaceChromeScale(c.pref, c.screenW, c.rows, 2); got != c.want {
			t.Errorf("SurfaceChromeScale(%d, %d, %d) = %d, want %d", c.pref, c.screenW, c.rows, got, c.want)
		}
	}
}
