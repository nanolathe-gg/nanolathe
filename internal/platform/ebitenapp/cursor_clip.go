package ebitenapp

import "math"

// presentedCursorRect is the host rectangle, in client device pixels with an
// exclusive right and bottom, that confines the pointer to the presented
// canvas while fullscreen (DESIGN_PRESENTATION_CLIENT §2.1).
//
// Retail's fullscreen path sets an exclusive display mode at the configured
// width and height, so the whole display is the canvas and the pointer cannot
// leave it [03 §4.2]. A modern fullscreen window keeps the desktop mode and
// aspect-fits the logical canvas, so a 4:3 canvas on a 21:9 display has bars
// the hidden pointer can wander into, carrying the software cursor out of
// sight. Confining the pointer to the fitted canvas restores the retail
// extent. This is host presentation policy, not gameplay.
//
// Ebitengine fits the canvas with scale s = min(cw/w, ch/h) and centres it at
// offset o, reporting the logical position int((p - o) / s), which truncates
// toward zero. The rectangle is therefore every device pixel p with
// o - s < p < o + w*s: exactly the pixels that report 0..w-1, so the exact
// edge pixels the camera scroll pass tests [07 §10] stay reachable on both
// sides and the pointer never reports a coordinate off the canvas.
func presentedCursorRect(clientW, clientH, logicalW, logicalH int) (left, top, right, bottom int32, ok bool) {
	if clientW <= 0 || clientH <= 0 || logicalW <= 0 || logicalH <= 0 {
		return 0, 0, 0, 0, false
	}
	cw, ch := float64(clientW), float64(clientH)
	w, h := float64(logicalW), float64(logicalH)
	s := min(cw/w, ch/h)
	span := func(client, logical float64) (int32, int32) {
		o := (client - logical*s) / 2
		lo := math.Floor(o-s) + 1
		hi := math.Ceil(o + logical*s)
		return int32(max(0, lo)), int32(min(client, hi))
	}
	left, right = span(cw, w)
	top, bottom = span(ch, h)
	return left, top, right, bottom, right > left && bottom > top
}
