//go:build !darwin || ebitenginevmguest

package ebitenapp

// Other platforms and VM guests use Ebitengine's wheel stream. VM input is
// supplied by its host and never passes through local AppKit events.
func startNativeScrollMonitor() (func(), error) { return func() {}, nil }
