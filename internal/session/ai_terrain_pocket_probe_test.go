// WU-19-41 diagnostic probe. It is the reproducer for the finding recorded at
// TestComputerPlayerEliminatesIdleHumanRetail: on `ashap plateau` the stock
// vehicle movement classes cannot leave the start plateau, so the computer
// player's attack wave freezes at the rim whenever the difficulty tables make
// it build vehicles rather than kbots.
//
// It asserts nothing. Locking either number would freeze a terrain reading
// that is only two height bytes away from the opposite answer, which is the
// point of the measurement. Set NANOLATHE_AI_POCKET_PROBE to a file path to
// run it; it skips otherwise.
package session

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/vfs"
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

// TestAIVehicleSlopeSensitivityProbe reports at which MaxSlope the computer
// player's start pocket on `ashap plateau` joins the rest of the map. The
// authored TANKSH2 limit is 15; the answer measures how far the rim sits from
// it, and therefore how much of the outcome rests on the derived floor pair of
// internal/world.deriveFloorPair and the footprint aggregation of
// [04 §6.1 R-DOC04-B step 6].
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
