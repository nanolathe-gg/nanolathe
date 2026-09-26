package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// mapSeconds is the measured mean wall time of one util+tac hard game per
// map, at 20 minutes (36000 ticks) and at 40 minutes (72000 ticks), on the
// development host with four niced match processes (protocol v2 runs,
// 2026-09-24/25; the 20-minute figures of the three long-pool-only maps are
// the first 20 minutes' share of their 40-minute games). Only the order
// matters: a tournament starts its longest games first, so the last games
// to finish are short ones and no straggler sets the finish time.
var mapSeconds = map[string][2]float64{
	"ashap plateau":         {10.2, 0},
	"brain coral":           {21.6, 0},
	"canal crossing":        {171, 0},
	"coast to coast":        {8.4, 0},
	"comet catcher":         {15.0, 0},
	"dark side":             {6.3, 0},
	"evad river confluence": {16.6, 0},
	"full moon":             {10.2, 0},
	"great divide":          {15.9, 28.7},
	"greenhaven":            {20.4, 116.1},
	"hundred isles":         {7.4, 0},
	"lake shore":            {18.6, 0},
	"metal heck":            {12.4, 0},
	"painted desert":        {19.3, 0},
	"pillopeens":            {110.5, 0},
	"plains and passes":     {36.8, 150.7},
	"red planet":            {10.1, 0},
	"ring atoll":            {37.2, 0},
	"sail away":             {57.7, 0},
	"sherwood":              {8.8, 12.5},
	"shore to shore":        {61.2, 0},
	"show down":             {21.8, 35.9},
	"the pass":              {9.1, 28.4},
}

// estimateSeconds is a game's expected wall time: the measured figure for
// its map at the nearer of the two lengths, scaled to its tick limit; a map
// measured only at the other length, or not at all, takes the median of the
// maps that were.
func estimateSeconds(mapName string, ticks uint) float64 {
	col, ref := 0, 36000.0
	if ticks > 54000 {
		col, ref = 1, 72000
	}
	v := mapSeconds[strings.ToLower(strings.TrimSpace(mapName))][col]
	if v == 0 {
		var known []float64
		for _, m := range mapSeconds {
			if m[col] > 0 {
				known = append(known, m[col])
			}
		}
		slices.Sort(known)
		v = known[len(known)/2]
	}
	return v * float64(ticks) / ref
}

// orderQueue sorts jobs for play: by look (a sequential tournament plays
// each look's seeds before the next look's), then, when byPair, pair by pair
// (so each pair reaches its look, and a decided pair drops its later games,
// before the next pair's look is played), then longest expected first, then
// in spec order. The order changes when a game is played, never the game:
// every job keeps its arguments and its id.
func orderQueue(queue []matchJob, longest, byPair bool) {
	slices.SortStableFunc(queue, func(a, b matchJob) int {
		if a.look != b.look {
			return a.look - b.look
		}
		if byPair && a.pair != b.pair {
			return a.pair - b.pair
		}
		if longest && a.cost != b.cost {
			if a.cost > b.cost {
				return -1
			}
			return 1
		}
		return 0
	})
}

// parseJobs reads -jobs: a count, or "auto" for the cores the host has free
// (logical CPUs less the one-minute load average, rounded up), at least one
// and at most autoJobsCap.
func parseJobs(s string) (int, string, error) {
	if s != "auto" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return 0, "", fmt.Errorf("-jobs %q: want a positive count or auto", s)
		}
		return n, "", nil
	}
	load, err := loadAverage()
	if err != nil {
		return 0, "", fmt.Errorf("-jobs auto: %v", err)
	}
	n := min(autoJobsCap, max(1, runtime.NumCPU()-int(math.Ceil(load))))
	return n, fmt.Sprintf("auto: %d CPUs, load %.1f", runtime.NumCPU(), load), nil
}

// autoJobsCap bounds -jobs auto on a shared host (docs/MODERN_AI_RESEARCH.md
// §5: at most four niced match processes).
const autoJobsCap = 4

// loadAverage is the host's one-minute load average.
func loadAverage() (float64, error) {
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) > 0 {
			return strconv.ParseFloat(f[0], 64)
		}
	}
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0, err
	}
	for _, f := range strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}")) {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			return v, nil
		}
	}
	return 0, fmt.Errorf("no load average in %q", out)
}

// slotPool shares match slots between tournaments: a match runs only while
// its tournament holds an exclusive lock on one of n files in dir, so every
// tournament given the same dir and n together runs at most n matches. A
// lock belongs to the holding process and goes when it exits, so a killed
// tournament leaves nothing to clean up.
type slotPool struct {
	dir string
	n   int
}

// parseSlots reads -slots: dir:n, "off" (or empty) for no pool, or "auto"
// (the default) for the host's shared pool, so that tournaments started by
// different people or agents take turns instead of oversubscribing the
// host: autoSlotsDir with half the logical CPUs, since a match process with
// asynchronous thinking keeps more than one core busy.
func parseSlots(s string) (*slotPool, error) {
	switch s {
	case "", "off":
		return nil, nil
	case "auto":
		dir, err := autoSlotsDir()
		if err != nil {
			return nil, err
		}
		s = dir + ":" + strconv.Itoa(max(1, runtime.NumCPU()/2))
	}
	i := strings.LastIndexByte(s, ':')
	if i <= 0 {
		return nil, fmt.Errorf("-slots %q: want dir:n", s)
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n < 1 {
		return nil, fmt.Errorf("-slots %q: want a positive slot count", s)
	}
	dir := s[:i]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &slotPool{dir: dir, n: n}, nil
}

// autoSlotsDir is the shared pool's directory in the user's cache.
func autoSlotsDir() (string, error) {
	c, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "nanolathe", "arena-slots"), nil
}

// acquire waits for a free slot and returns its release.
func (p *slotPool) acquire(ctx context.Context) (func(), error) {
	for {
		for i := 0; i < p.n; i++ {
			release, err := tryLock(filepath.Join(p.dir, fmt.Sprintf("slot-%d.lock", i)))
			if err != nil {
				return nil, err
			}
			if release != nil {
				return release, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
