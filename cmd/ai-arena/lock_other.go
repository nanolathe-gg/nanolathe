//go:build !unix

package main

import "errors"

// tryLock is not available on this platform; -slots needs file locks.
func tryLock(path string) (func(), error) {
	return nil, errors.New("-slots needs file locks, which this platform lacks")
}
