// Package debugcapture writes host diagnostics, never simulation state or assets.
package debugcapture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"runtime/pprof"
	"time"
)

type FileStatus struct {
	Name        string
	CollectedAt time.Time
	Bytes       int64
	Error       string
}
type Manifest struct {
	SchemaVersion int
	CaptureID     string
	StartedAt     time.Time
	DurationNanos int64
	PID           int
	Complete      bool
	Metadata      any
	Files         []FileStatus
	Omissions     []string
}
type Capture struct {
	Directory string
	Manifest  Manifest
	start     time.Time
}

// Begin captures initial runtime statistics before engine snapshots allocate.
// base is an explicit host/test override; the default lives outside the repo.
func Begin(base string, metadata any) (*Capture, error) {
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, "Nanolathe", "diagnostics")
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	start := time.Now()
	dir, err := os.MkdirTemp(base, start.UTC().Format("20060102T150405.000000000Z")+"-")
	if err != nil {
		return nil, err
	}
	c := &Capture{Directory: dir, start: start, Manifest: Manifest{SchemaVersion: 1, CaptureID: filepath.Base(dir), StartedAt: start, PID: os.Getpid(), Complete: true, Metadata: metadata}}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	samples := []metrics.Sample{}
	for _, d := range metrics.All() {
		if d.Kind == metrics.KindUint64 || d.Kind == metrics.KindFloat64 {
			samples = append(samples, metrics.Sample{Name: d.Name})
		}
	}
	metrics.Read(samples)
	values := make(map[string]any, len(samples))
	for _, s := range samples {
		switch s.Value.Kind() {
		case metrics.KindUint64:
			values[s.Name] = s.Value.Uint64()
		case metrics.KindFloat64:
			values[s.Name] = s.Value.Float64()
		}
	}
	c.JSON("runtime.json", struct {
		CollectedAt                            time.Time
		MemStats                               runtime.MemStats
		Metrics                                map[string]any
		Goroutines, GOMAXPROCS, MemProfileRate int
		ForcedGC                               bool
	}{time.Now(), memory, values, runtime.NumGoroutine(), runtime.GOMAXPROCS(0), runtime.MemProfileRate, false})
	exe, exeErr := os.Executable()
	build, _ := debug.ReadBuildInfo()
	process := struct {
		PID, PPID                                            int
		Executable, ExecutableError, GOOS, GOARCH, GoVersion string
		Build                                                *debug.BuildInfo
		OSDetails                                            string
	}{os.Getpid(), os.Getppid(), exe, "", runtime.GOOS, runtime.GOARCH, runtime.Version(), build, "See process-memory.txt and virtual-memory.txt for best-effort OS counters; threadcreate.pprof records Go thread creation only"}
	if exeErr != nil {
		process.ExecutableError = exeErr.Error()
	}
	c.JSON("process.json", process)
	return c, nil
}

func (c *Capture) Write(name string, write func(io.Writer) error) {
	status := FileStatus{Name: name, CollectedAt: time.Now()}
	f, err := os.OpenFile(filepath.Join(c.Directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		err = write(f)
		err = errors.Join(err, f.Close())
		if st, e := os.Stat(f.Name()); e == nil {
			status.Bytes = st.Size()
		}
	}
	if err != nil {
		status.Error = err.Error()
		c.Manifest.Complete = false
	}
	c.Manifest.Files = append(c.Manifest.Files, status)
}
func (c *Capture) JSON(name string, value any) {
	c.Write(name, func(w io.Writer) error { return json.NewEncoder(w).Encode(value) })
}
func (c *Capture) Unavailable(name, reason string) {
	c.Manifest.Complete = false
	c.Manifest.Files = append(c.Manifest.Files, FileStatus{Name: name, CollectedAt: time.Now(), Error: reason})
}

// Device runs on the window owner. A partial writer preserves successful files.
func (c *Capture) Device(write func(string) error) {
	err := write(c.Directory)
	for _, name := range []string{"renderer.json", "last-frame.png"} {
		status := FileStatus{Name: name, CollectedAt: time.Now()}
		st, e := os.Stat(filepath.Join(c.Directory, name))
		if e != nil {
			status.Error = e.Error()
			c.Manifest.Complete = false
		} else {
			status.Bytes = st.Size()
			if err != nil {
				status.Error = "device writer failed; file completion uncertain: " + err.Error()
				c.Manifest.Complete = false
			}
		}
		c.Manifest.Files = append(c.Manifest.Files, status)
	}
	if err != nil {
		c.Unavailable("device", err.Error())
	}
}
func (c *Capture) Profiles() {
	for _, name := range []string{"heap", "allocs", "goroutine", "threadcreate"} {
		c.Write(name+".pprof", func(w io.Writer) error {
			p := pprof.Lookup(name)
			if p == nil {
				return fmt.Errorf("profile %s unavailable", name)
			}
			return p.WriteTo(w, 0)
		})
	}
	c.Write("goroutines.txt", func(w io.Writer) error { return pprof.Lookup("goroutine").WriteTo(w, 2) })
	c.Manifest.Omissions = append(c.Manifest.Omissions, "No forced GC, raw heap dump, or CPU profile. Block and mutex histories are omitted because sampling was not enabled by this capture.")
}
func (c *Capture) Finish() error {
	c.Manifest.DurationNanos = time.Since(c.start).Nanoseconds()
	f, err := os.OpenFile(filepath.Join(c.Directory, "manifest.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	err = errors.Join(json.NewEncoder(f).Encode(c.Manifest), f.Close())
	if err != nil {
		return err
	}
	if !c.Manifest.Complete {
		return fmt.Errorf("nanolathe: diagnostic bundle incomplete: logical path %s, providers searched [capture writers], expected complete files; see manifest.json", c.Directory)
	}
	return nil
}
