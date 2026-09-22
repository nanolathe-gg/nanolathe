//go:build !darwin || ebitenginevmguest

package ebitenapp

// VM guests use the host's window policy, just as other platforms do.
type nativeFullscreenPresentation struct{}

func startNativeFullscreenPresentation() *nativeFullscreenPresentation { return nil }
func (*nativeFullscreenPresentation) update(fullscreen, focused bool)  {}
func (*nativeFullscreenPresentation) close()                           {}
