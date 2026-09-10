package debugcapture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// ProcessMemory collects only the current process. The bounded commands are
// host diagnostics; failures remain independent per-file manifest entries.
func (c *Capture) ProcessMemory() {
	pid := strconv.Itoa(os.Getpid())
	switch runtime.GOOS {
	case "darwin":
		c.command("process-memory.txt", "/bin/ps", "-p", pid, "-o", "pid=,ppid=,rss=,vsz=,state=,etime=,comm=")
		c.command("virtual-memory.txt", "/usr/bin/vmmap", "-summary", pid)
	case "linux":
		for _, name := range []string{"status", "smaps_rollup"} {
			c.Write("process-"+name+".txt", func(w io.Writer) error {
				f, err := os.Open(filepath.Join("/proc/self", name))
				if err != nil {
					return err
				}
				defer f.Close()
				_, err = io.Copy(w, f)
				return err
			})
		}
	default:
		c.Unavailable("process-memory.txt", "OS memory collection not implemented on "+runtime.GOOS)
	}
	fdRoot := "/dev/fd"
	if runtime.GOOS == "linux" {
		fdRoot = "/proc/self/fd"
	}
	// A names-only read avoids statting transient descriptors on macOS devfs.
	// ReadDir can fail its implicit lstat when an audio/runtime descriptor closes.
	var entries []string
	f, err := os.Open(fdRoot)
	if err == nil {
		entries, err = f.Readdirnames(-1)
		_ = f.Close()
	}
	if err != nil {
		c.Unavailable("file-descriptors.json", err.Error())
	} else {
		c.JSON("file-descriptors.json", struct {
			Count int
			Note  string
		}{len(entries), "Approximate current descriptor count, including the directory read; descriptor paths and contents are not captured"})
	}
}
func (c *Capture) command(file, path string, args ...string) {
	c.Write(file, func(w io.Writer) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Stdout = w
		cmd.Stderr = w
		err := cmd.Run()
		if ctx.Err() != nil {
			return fmt.Errorf("%s: %w", path, ctx.Err())
		}
		return err
	})
}
