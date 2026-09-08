// Package benchlock serializes host benchmark runs independently of worktrees.
package benchlock

import (
	"os"
	"path/filepath"
)

// Path is shared by this user's checkouts on the host, not by output directory.
func Path() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nanolathe", "battle-bench.lock"), nil
}

// Acquire waits for exclusive ownership. Closing the file or exiting the
// process releases it, including abnormal exits. Never unlink the lock file:
// a waiter may already have opened it, and replacing it would split the lock.
// onWait is called once if another process currently holds the lock.
func Acquire(path string, onWait func()) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	acquired, err := lock(f, false)
	if err == nil && !acquired {
		if onWait != nil {
			onWait()
		}
		_, err = lock(f, true)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
