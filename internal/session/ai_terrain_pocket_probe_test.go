// Terrain-reachability probes for the stock vehicle movement classes. They
// began as WU-19-41's reproducer for a start plateau the vehicle classes could
// not leave; WU-19-46 applied [04 R-SLOPE-01] — per-cell slope, minimum tier
// over the footprint — and they now serve as the standing re-measurement of
// the four numbers that finding took off the reference install.
//
// They assert nothing. A pocket census is a reading of one map's terrain
// against one class's authored limits; pinning it would freeze a measurement
// rather than a contract, and the contract itself is locked in
// internal/movement. Set NANOLATHE_AI_POCKET_PROBE to a file path to run
// them; they skip otherwise.
package session

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// tanksh2 is the stock MOVEINFO class the level-1/2 ground vehicles carry:
// 2×2 footprint, MaxSlope 15, BadSlope defaulted to maxslope>>1
// [fmt tdf "MOVEINFO.TDF"][02 §5].
var tanksh2 = movement.Profile{
	FootPrintX: 2, FootPrintZ: 2,
	MaxWaterDepth: 12, MinWaterDepth: -10000,
	MaxSlope: 15, BadSlope: 7,
	MaxWaterSlope: 255, BadWaterSlope: 127,
}

func aiPocketProbeOut(t *testing.T) *os.File {
	t.Helper()
	path := os.Getenv("NANOLATHE_AI_POCKET_PROBE")
	if path == "" {
		t.Skip("NANOLATHE_AI_POCKET_PROBE unset")
	}
	// One file per test so two probes in one run do not overwrite each other.
	f, err := os.Create(path + "." + t.Name())
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// floodFrom counts the cells a profile can reach from one anchor by eight-way
// steps over the same footprint predicate the path layer's classifier uses
// [04 §6.1 R-DOC04-B].
func floodFrom(p movement.Profile, sess *Session, sx, sz int32, goalX, goalZ int32) (int, bool) {
	w := sess.World
	seen := map[[2]int32]bool{{sx, sz}: true}
	queue := [][2]int32{{sx, sz}}
	n, found := 0, false
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		n++
		if c[0] == goalX && c[1] == goalZ {
			found = true
		}
		for _, d := range [8][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
			nc := [2]int32{c[0] + d[0], c[1] + d[1]}
			if nc[0] < 0 || nc[1] < 0 || nc[0] >= w.CellW || nc[1] >= w.CellH || seen[nc] {
				continue
			}
			if !p.IsPassableFootprint(w, nc[0], nc[1]) {
				continue
			}
			seen[nc] = true
			queue = append(queue, nc)
		}
	}
	return n, found
}

// TestAIVehiclePocketProbe reports, per map, how much of the map's
// vehicle-passable area the vehicle class can actually reach from a start
// unit. A start pocket that is a small fraction of the passable total is a map
// on which no vehicle-built attack wave can ever arrive.
func TestAIVehiclePocketProbe(t *testing.T) {
	out := aiPocketProbeOut(t)
	root := aiE2ERetailRoot(t)
	names := strings.Split("ashap plateau,acid foursome,brilliant cut lake,aqua verdigris", ",")
	if v := os.Getenv("NANOLATHE_AI_POCKET_MAPS"); v != "" {
		names = strings.Split(v, ",")
	}
	for _, name := range names {
		fs := vfs.New()
		if err := fs.MountGameDirectory(root); err != nil {
			t.Skipf("mount retail install: %v", err)
		}
		cfg := DirectSkirmishConfig(strings.TrimSpace(name))
		cfg.ApplyDefaults()
		cfg.RNGSimSeed, cfg.RNGCrtSeed = aiE2ESeed, aiE2ESeed
		sess, err := NewSkirmishWithProgress(fs, nil, cfg, nil)
		if err != nil || sess == nil || sess.World == nil {
			fmt.Fprintf(out, "%-24s compose failed: %v\n", name, err)
			_ = fs.Close()
			continue
		}
		w := sess.World
		total := 0
		for z := int32(0); z < w.CellH; z++ {
			for x := int32(0); x < w.CellW; x++ {
				if tanksh2.IsPassableFootprint(w, x, z) {
					total++
				}
			}
		}
		best := 0
		for _, u := range sess.Units.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			// One cell is 0x100000 in 16.16 world units [03 §2.1].
			n, _ := floodFrom(tanksh2, sess, int32(int64(u.X)>>20), int32(int64(u.Z)>>20), -1, -1)
			if n > best {
				best = n
			}
		}
		pct := 0.0
		if total > 0 {
			pct = 100 * float64(best) / float64(total)
		}
		fmt.Fprintf(out, "%-24s cells=%dx%d vehiclePassable=%d startPocket=%d (%.0f%%)\n",
			name, w.CellW, w.CellH, total, best, pct)
		_ = fs.Close()
	}
}

// TestAIVehicleRetailSlopeProbe re-measures the four numbers [04 R-SLOPE-01 §4]
// records for TANKSH2 on `ashap plateau`, so the per-cell classifier of
// WU-19-46 can be checked against them: the passable anchor total, the flood
// from the computer player's start cell (53, 231), whether that flood reaches
// the human start at (219, 19) (yes), and the tiers of the three rim anchors
// the finding lists (all three passable, per-cell slopes 9/8/9/9, 12/12/6/4,
// 13/6/10/5).
//
// The two counts are labelled below for what they are. §4's 53901 and 53370
// are this loader's own count over retail's map data, taken while the loader
// still carried the pre-correction south strip of the void sweep; with that
// sweep implemented as [03 R-TERR-01 §2] states it (WU-19-48) the same census
// reads 53808 and 53279, the south void band having moved up one row. Which
// the retail executable's own layer would hold is the Unknown filed under
// [04 R-SLOPE-01 §3] item 2's correction — neither figure has been read out of
// retail.
//
// It asserts nothing: it prints, so a divergence is read rather than pinned.
func TestAIVehicleRetailSlopeProbe(t *testing.T) {
	out := aiPocketProbeOut(t)
	sess := aiE2ESkirmishAt(t, "ashap plateau", aiE2ESeed, SkirmishDefaultDifficulty)
	w := sess.World
	total := 0
	for z := int32(0); z < w.CellH; z++ {
		for x := int32(0); x < w.CellW; x++ {
			if tanksh2.IsPassableFootprint(w, x, z) {
				total++
			}
		}
	}
	var startX, startZ, humanX, humanZ int32 = -1, -1, -1, -1
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		// One cell is 0x100000 in 16.16 world units [03 §2.1].
		cx, cz := int32(int64(u.X)>>20), int32(int64(u.Z)>>20)
		if u.Owner == 1 && startX < 0 {
			startX, startZ = cx, cz
		}
		if u.Owner == 0 && humanX < 0 {
			humanX, humanZ = cx, cz
		}
	}
	reach, found := floodFrom(tanksh2, sess, startX, startZ, humanX, humanZ)
	fmt.Fprintf(out, "cells=%dx%d passableAnchors=%d (53808 corrected strips, 53901 as [04 R-SLOPE-01 §4] records it)\n", w.CellW, w.CellH, total)
	fmt.Fprintf(out, "computerStart=(%d,%d) humanStart=(%d,%d)\n", startX, startZ, humanX, humanZ)
	fmt.Fprintf(out, "floodFromComputerStart=%d (53279 corrected strips, 53370 as recorded) reachesHumanStart=%v (want true)\n", reach, found)
	for _, a := range [3][2]int32{{48, 213}, {34, 208}, {20, 221}} {
		fx, fz := int32(tanksh2.FootPrintX), int32(tanksh2.FootPrintZ)
		slopes := make([]int32, 0, fx*fz)
		for dz := int32(0); dz < fz; dz++ {
			for dx := int32(0); dx < fx; dx++ {
				c := w.PlotAt(a[0]+dx, a[1]+dz)
				if c == nil {
					slopes = append(slopes, -1)
					continue
				}
				slopes = append(slopes, int32(c.MaxHeight())-int32(c.MinHeight()))
			}
		}
		fmt.Fprintf(out, "rim(%d,%d) class=%v passable=%v perCellSlopes=%v\n",
			a[0], a[1], tanksh2.ClassifyFootprint(w, a[0], a[1]),
			tanksh2.IsPassableFootprint(w, a[0], a[1]), slopes)
	}
}

// TestAIVehicleSlopeSensitivityProbe reports at which MaxSlope the computer
// player's start pocket on `ashap plateau` joins the rest of the map. The
// authored TANKSH2 limit is 15; the answer measures how far the rim sits from
// it, and therefore how much of the outcome rests on the derived floor pair of
// internal/world.deriveFloorPair and the per-cell minimum-tier footprint rule
// of [04 R-SLOPE-01 §3].
func TestAIVehicleSlopeSensitivityProbe(t *testing.T) {
	out := aiPocketProbeOut(t)
	sess := aiE2ESkirmishAt(t, "ashap plateau", aiE2ESeed, SkirmishDefaultDifficulty)
	var startX, startZ int32
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Owner == 1 {
			startX, startZ = int32(int64(u.X)>>20), int32(int64(u.Z)>>20)
			break
		}
	}
	for _, ms := range []uint8{15, 16, 17, 18, 20, 32} {
		p := tanksh2
		p.MaxSlope, p.BadSlope = ms, ms/2
		n, _ := floodFrom(p, sess, startX, startZ, -1, -1)
		fmt.Fprintf(out, "MaxSlope=%3d pocketFromComputerStart=%6d\n", ms, n)
	}
}
