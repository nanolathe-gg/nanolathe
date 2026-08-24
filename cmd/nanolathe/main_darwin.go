//go:build darwin

package main

import (
	"os"
	"runtime"
)

func main() {
	// Ebitengine locks the OS thread itself; keeping the lock here as well is
	// harmless and makes AppKit threading explicit for the process.
	runtime.LockOSThread()

	opts, code, ok := mainOptions(os.Args[1:], os.Stdout)
	if !ok {
		if code != 0 {
			os.Exit(code)
		}
		return
	}
	if code := runOptions(opts, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}
