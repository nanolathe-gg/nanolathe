//go:build unix

package main

import (
	"os"
	"syscall"
)

// tryLock takes an exclusive, non-blocking lock on path, returning its
// release, or nil when another process holds it.
func tryLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, nil
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}
