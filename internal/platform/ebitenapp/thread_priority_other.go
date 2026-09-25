//go:build !darwin || ebitenginevmguest

package ebitenapp

// RaiseCurrentThread is macOS scheduling policy (thread_priority_darwin.go);
// other hosts keep their default thread scheduling.
func RaiseCurrentThread() {}
