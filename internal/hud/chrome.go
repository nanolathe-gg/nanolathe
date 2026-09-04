package hud

// Battle chrome layout at the negotiated display size [07 R-HUD-05].
//
// Retail authors the battle interface in a 640×480 design space and, at a
// larger display mode, extends it by rule rather than by scaling: the two
// horizontal strips are stamped rightward until they reach the surface width,
// the bottom strip sits at the surface height minus 32, the left rail keeps
// its authored 129×480 art and nothing paints below it, and the world
// viewport takes the rest [03 §4.1]. The helpers here are the arithmetic of
// those rules, kept free of the client so they can be locked by a test.

// ChromeRailX is the x origin of both horizontal strips: the authored rail
// boundary at which PANELTOP and PANELBOT are stamped [07 §6].
const ChromeRailX = 129

// ChromeStripHeight is the height of the top strip and of the bottom strip's
// visible band: the bottom strip's origin is `surfaceHeight - 32` and the
// world viewport's top inset is 32 [03 §4.1][07 R-HUD-05].
const ChromeStripHeight = 32

// StripStamps returns the x origins at which a horizontal chrome strip is
// stamped across a surface of width screenW: the first stamp at the rail
// boundary with a frame of width firstW, then further stamps each advancing
// by restW, for as long as the running x is still left of the surface edge
// [07 R-HUD-03 §4][07 R-HUD-05]. The first stamp is always emitted; the
// loop is retail's "advance by the frame width while x < width", so a frame
// whose right edge already reaches the surface produces no extension and a
// 640-wide surface with a 511- or 513-wide first frame yields exactly one
// stamp.
//
// A non-positive restW would never advance; retail has no such frame, so the
// loop stops after the first stamp rather than spin.
func StripStamps(screenW, firstW, restW int32) []int32 {
	xs := []int32{ChromeRailX}
	x := int32(ChromeRailX) + firstW
	for x < screenW && restW > 0 {
		xs = append(xs, x)
		x += restW
	}
	return xs
}

// BottomStripY is the y origin of the bottom strip: the surface height minus
// 32 at every display mode, so at 640×480 it is 448 [07 §6][07 R-HUD-05].
func BottomStripY(screenH int32) int32 {
	return screenH - ChromeStripHeight
}

// RailGap returns the rectangle of the left rail that no chrome covers at a
// surface taller than the side panel's art: the columns under the side panel
// from the panel's bottom edge down to the surface edge. Retail paints the
// rail once at battle start onto a surface cleared to palette index 0 and
// never extends it, so this band stays index 0 for the whole battle
// [07 R-HUD-05]. The returned ok is false when the panel reaches the surface
// edge, as it does at 640×480.
func RailGap(screenH, sideW, sideH int32) (r Rect, ok bool) {
	if sideH >= screenH || sideW <= 0 {
		return Rect{}, false
	}
	return Rect{X1: 0, Y1: sideH, X2: sideW - 1, Y2: screenH - 1}, true
}

// ModalPlacement is the window initializer's placement for a battle modal
// opened with the "centre in the view" flag: centred in the surface width
// left over to the right of the 128-pixel rail, and centred in the full
// surface height, both by truncating divides, at the live surface size rather
// than the authored one [07 "Tab options menu and manual exit"][07 R-HUD-05].
func ModalPlacement(screenW, screenH, w, h int32) (x, y int32) {
	return (screenW-128-w)/2 + 128, (screenH - h) / 2
}
