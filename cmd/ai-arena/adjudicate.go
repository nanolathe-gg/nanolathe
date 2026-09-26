package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/headless"
)

// ticksPerMinute is the simulation's 30 ticks a second.
const ticksPerMinute = 30 * 60

// parseAdjudicate reads the match's -adjudicate rule, "ratio,minutes,from":
// the leader's score must be at least ratio times every other player's at
// every sample for the last minutes, from minute from on
// (headless.ArenaAdjudication). An empty rule plays every match out.
func parseAdjudicate(s string) (headless.ArenaAdjudication, error) {
	if s == "" {
		return headless.ArenaAdjudication{}, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return headless.ArenaAdjudication{}, fmt.Errorf("-adjudicate %q: want ratio,minutes,from", s)
	}
	var v [3]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) {
			return headless.ArenaAdjudication{}, fmt.Errorf("-adjudicate %q: bad number %q", s, p)
		}
		v[i] = f
	}
	if v[0] <= 1 {
		return headless.ArenaAdjudication{}, fmt.Errorf("-adjudicate %q: the ratio must exceed 1", s)
	}
	return headless.ArenaAdjudication{
		RatioPct: uint32(math.Round(v[0] * 100)),
		Window:   uint32(math.Round(v[1] * ticksPerMinute)),
		From:     uint32(math.Round(v[2] * ticksPerMinute)),
	}, nil
}
