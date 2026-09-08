//go:build !windows

package benchlock

import (
	"errors"
	"os"
	"syscall"
)

func lock(f *os.File, wait bool) (bool, error) {
	flags := syscall.LOCK_EX
	if !wait {
		flags |= syscall.LOCK_NB
	}
	for {
		err := syscall.Flock(int(f.Fd()), flags)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !wait && errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return err == nil, err
	}
}
