//go:build !windows || ebitenginevmguest

package ebitenapp

// Other hosts leave the pointer unconfined; see presentedCursorRect.
// TODO(question): macOS and X11 fullscreen can also aspect-fit with bars. No
// report yet shows the pointer escaping there, and neither host has a
// ClipCursor equivalent behind Ebitengine; settle with a manual fullscreen
// check on a display whose aspect differs from the canvas.
type nativeCursorClip struct{}

func (*nativeCursorClip) update(fullscreen, focused, captured bool, logicalW, logicalH int) {}
func (*nativeCursorClip) release()                                                          {}
