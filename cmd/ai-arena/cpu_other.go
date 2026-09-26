//go:build !unix

package main

// processCPUSeconds is not measured on this platform.
func processCPUSeconds() float64 { return 0 }
