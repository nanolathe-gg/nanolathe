//go:build !darwin

package main

import "os"

func main() {
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
