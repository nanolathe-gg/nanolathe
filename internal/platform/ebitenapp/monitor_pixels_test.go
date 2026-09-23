package ebitenapp

import "testing"

// The physical monitor size is recovered from Ebitengine's truncated
// device-independent size and its scale factor (DESIGN_PRESENTATION_CLIENT
// §2.1): a 3440x1440 panel at 125% reports 2752x1152 and must read back as
// its native size, including scales whose division truncates.
func TestMonitorPixelsRecoversNativeSize(t *testing.T) {
	for _, tc := range []struct {
		dip   int
		scale float64
		want  int
	}{
		{2752, 1.25, 3440}, {1152, 1.25, 1440},
		{1706, 1.5, 2560}, {960, 1.5, 1440}, // 2560/1.5 truncates to 1706
		{1965, 1.75, 3440}, {822, 1.75, 1440}, // both truncate
		{1092, 1.25, 1366},              // 1366/1.25 truncates to 1092
		{1512, 2, 3024}, {982, 2, 1964}, // macOS: exact device-independent size
		{1920, 1, 1920}, {1920, 0, 1920}, {0, 1.25, 0},
	} {
		if got := monitorPixels(tc.dip, tc.scale); got != tc.want {
			t.Errorf("monitorPixels(%d, %v) = %d, want %d", tc.dip, tc.scale, got, tc.want)
		}
	}
}
