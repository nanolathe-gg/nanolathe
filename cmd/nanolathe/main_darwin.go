//go:build darwin

package main

import (
	"os"
	"runtime"

	"kaijuengine.com/platform/windowing"
)

func main() {
	// AppKit and its event loop must remain on the process main thread. Kaiju's
	// host initializes on another locked OS thread and synchronously dispatches
	// window creation back here.
	runtime.LockOSThread()

	opts, code, ok := mainOptions(os.Args[1:], os.Stdout)
	if !ok {
		if code != 0 {
			os.Exit(code)
		}
		return
	}
	if !wantsViewer(opts) {
		if code := runOptions(opts, os.Stdout, os.Stderr); code != 0 {
			os.Exit(code)
		}
		return
	}

	go func() {
		if code := runOptions(opts, os.Stdout, os.Stderr); code != 0 {
			os.Exit(code)
		}
	}()
	windowing.CocoaRunApp()
}
