package hud

// Modern UI scale (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
// This is Nanolathe host presentation policy, not retail: retail always
// draws the rail at one framebuffer pixel per authored pixel [07 R-HUD-05].

// AutoChromeScaleHeight is the surface height per step of the Auto scale:
// 1x below 1440 rows, 2x from 1440.
const AutoChromeScaleHeight = 720

// MinBarScaleWidth is the virtual width Auto leaves the bars right of the
// rail: retail's 640-column surface less its 128-column rail, the width the
// stock strip art and resource anchors are authored for.
const MinBarScaleWidth = 512

// ChromeScale resolves a UI scale preference — zero for Auto, otherwise a
// fixed factor — against a surface screenH rows tall, never above maxScale. A
// fixed factor is used as chosen even when the rail it leaves is shorter than
// the 480 rows stock pages are authored for; the player asked for it.
func ChromeScale(pref int, screenH, maxScale int32) int32 {
	k := int32(pref)
	if k <= 0 {
		k = screenH / AutoChromeScaleHeight
	}
	return max(min(k, maxScale), 1)
}

// SurfaceChromeScale is ChromeScale for a whole screenW x screenH surface.
// Auto also leaves the bars right of the magnified rail the width their art
// and readouts are authored for; a chosen size applies regardless. The battle
// and the Nanolathe screen's sidebar demonstration both resolve through it.
func SurfaceChromeScale(pref int, screenW, screenH, maxScale int32) int32 {
	k := ChromeScale(pref, screenH, maxScale)
	for pref <= 0 && k > 1 && (screenW-(ChromeRailX*k-1))/k < MinBarScaleWidth {
		k--
	}
	return k
}
