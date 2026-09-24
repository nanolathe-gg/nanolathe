//go:build pathbench && retail

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"testing"
	"time"
)

// This export exercises the pure Modern command producer in
// DESIGN_INTERFACE_HUD_INPUT §3.11. The authored paths are experimental inputs.
type formationExport struct {
	Version int             `json:"version"`
	Cases   []formationCase `json:"cases"`
}
type formationCase struct {
	ID                  string           `json:"id"`
	Family              string           `json:"family"`
	Description         string           `json:"description"`
	Size                int              `json:"size"`
	Ticks               int              `json:"ticks"`
	Terrain             string           `json:"terrain"`
	Width               int32            `json:"width_cells"`
	Height              int32            `json:"height_cells"`
	Actors              []formationActor `json:"actors"`
	Stroke              [][2]int32       `json:"stroke_world"`
	Sampled             [][2]int32       `json:"sampled_world"`
	AssignmentNS        []int64          `json:"assignment_ns"`
	AssignmentAllocs    float64          `json:"assignment_allocs"`
	AssignmentBytes     uint64           `json:"assignment_bytes"`
	TotalDirectDistance float64          `json:"total_direct_distance_world"`
	DuplicateGoals      int              `json:"duplicate_goals"`
	IdentitySHA256      string           `json:"identity_sha256"`
}
type formationActor struct {
	Key   string   `json:"key"`
	Start [2]int32 `json:"start_cell"`
	Goal  [2]int32 `json:"assigned_world"`
}
type formationSpec struct {
	id, desc, terrain, layout string
	sizes                     []int
	stroke                    string
	ticks                     int
	mixed                     bool
}

var formationSpecs = []formationSpec{
	{"line", "capacity-feasible straight line", "flat", "grid", []int{8, 64, 256}, "line", 1800, false},
	{"curve", "two-segment bend with arc-length sampling", "flat", "grid", []int{8, 64}, "curve", 900, false},
	{"zigzag", "four alternating segments", "flat", "grid", []int{8, 64}, "zigzag", 900, false},
	{"reverse", "same line endpoints in reverse order", "flat", "grid", []int{8, 64, 256}, "reverse", 1800, false},
	{"rotated_crossing", "initial ranks rotated across the destination line", "flat", "rotated", []int{8, 64}, "line", 900, false},
	{"crossed", "opposite banks crossing toward one line", "flat", "crossed", []int{8, 64}, "line", 900, false},
	{"long_line", "wide spacing with long travel", "flat", "grid", []int{8, 64}, "long", 1200, false},
	{"short_overfull", "physically overfull line control", "flat", "grid", []int{8, 64, 256}, "short", 600, false},
	{"mixed_footprint", "mixed retail footprint and speed", "flat", "grid", []int{8, 64}, "mixed", 900, true},
	{"blocked_endpoint", "line includes a void endpoint", "blocked", "grid", []int{8, 64}, "line", 600, false},
	{"water_endpoint", "line includes a submerged endpoint", "water", "grid", []int{8, 64}, "line", 600, false},
	{"slope_endpoint", "line includes an impassably steep endpoint", "slope", "grid", []int{8, 64}, "line", 600, false},
	{"choke_reform", "group funnels through a wall opening then reforms", "choke", "grid", []int{8, 64, 256}, "line", 1200, false},
}

func formationWorld(cell int32) int32 { return cell*16 + 8 }
func formationPairs(points []dragPoint) [][2]int32 {
	out := make([][2]int32, len(points))
	for i, p := range points {
		out[i] = [2]int32{p.x, p.z}
	}
	return out
}
func formationPath(spec formationSpec, n int) []dragPoint {
	span := int32(n * 5 / 2)
	if spec.stroke == "mixed" {
		span = int32(n * 4)
	}
	if spec.stroke == "long" {
		span = int32(n * 3)
	}
	if spec.stroke == "short" {
		span = 2
	}
	if span < 12 && spec.stroke != "short" {
		span = 12
	}
	x0, x1, z := int32(180), int32(180)+span, int32(130)
	p := func(x, z int32) dragPoint { return dragPoint{formationWorld(x), formationWorld(z)} }
	switch spec.stroke {
	case "reverse":
		return []dragPoint{p(x1, z), p(x0, z)}
	case "curve":
		return []dragPoint{p(x0, z-20), p(x0+span/2, z+24), p(x1, z)}
	case "zigzag":
		return []dragPoint{p(x0, z-20), p(x0+span/3, z+20), p(x0+2*span/3, z-20), p(x1, z+20)}
	default:
		return []dragPoint{p(x0, z), p(x1, z)}
	}
}
func formationStart(layout string, i, n int) [2]int32 {
	cols := 16
	if n == 8 {
		cols = 4
	}
	x, z := int32(40+(i%cols)*4), int32(90+(i/cols)*4)
	switch layout {
	case "rotated":
		x, z = int32(68+(i/cols)*4), int32(70+(i%cols)*4)
	case "crossed":
		if i%2 == 0 {
			x = 275 + int32((i/2)%cols)*4
		}
	}
	return [2]int32{x, z}
}
func formationMake(spec formationSpec, n int) formationCase {
	c := formationCase{ID: spec.id, Family: "alt_drag", Description: spec.desc, Size: n, Ticks: spec.ticks, Terrain: spec.terrain, Width: 640, Height: 320}
	if n == 256 && spec.stroke != "short" {
		c.Width = 896
	}
	if n == 64 && (spec.id == "line" || spec.id == "reverse") {
		c.Ticks = 2400
	}
	if n == 256 && (spec.id == "line" || spec.id == "reverse") {
		c.Ticks = 6000
	}
	starts := make([]dragPoint, n)
	for i := range starts {
		cell := formationStart(spec.layout, i, n)
		starts[i] = dragPoint{formationWorld(cell[0]), formationWorld(cell[1])}
		key := "armflea"
		if spec.mixed && i%2 == 1 {
			key = "armflash"
		}
		if spec.terrain == "water" {
			key = "armflash"
		}
		c.Actors = append(c.Actors, formationActor{Key: key, Start: cell})
	}
	path := formationPath(spec, n)
	c.Stroke = formationPairs(path)
	sampled := dragSamplePath(path, n)
	c.Sampled = formationPairs(sampled)
	assigned := dragAssignDestinations(starts, sampled)
	seen := make(map[dragPoint]bool, n)
	for i, g := range assigned {
		c.Actors[i].Goal = [2]int32{g.x, g.z}
		if seen[g] {
			c.DuplicateGoals++
		}
		seen[g] = true
		c.TotalDirectDistance += math.Hypot(float64(starts[i].x-g.x), float64(starts[i].z-g.z))
	}
	input, _ := json.Marshal(struct {
		Actors          []formationActor
		Stroke, Sampled [][2]int32
	}{c.Actors, c.Stroke, c.Sampled})
	h := sha256.Sum256(input)
	c.IdentitySHA256 = hex.EncodeToString(h[:])
	return c
}
func TestPathBenchFormationExport(t *testing.T) {
	path := os.Getenv("NANOLATHE_PATH_BENCH_FORMATIONS")
	if path == "" {
		t.Skip("set NANOLATHE_PATH_BENCH_FORMATIONS to export command inputs")
	}
	export := formationExport{Version: 1}
	for _, spec := range formationSpecs {
		for _, n := range spec.sizes {
			c := formationMake(spec, n)
			starts := make([]dragPoint, n)
			for i, a := range c.Actors {
				starts[i] = dragPoint{formationWorld(a.Start[0]), formationWorld(a.Start[1])}
			}
			goals := make([]dragPoint, n)
			for i, p := range c.Sampled {
				goals[i] = dragPoint{p[0], p[1]}
			}
			// Reuse prepared geometry; only production assignment runs inside the samples.
			c.AssignmentAllocs = testing.AllocsPerRun(1, func() { _ = dragAssignDestinations(starts, goals) })
			for sample := 0; sample < 5; sample++ {
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				begin := time.Now()
				result := dragAssignDestinations(starts, goals)
				elapsed := time.Since(begin).Nanoseconds()
				runtime.ReadMemStats(&after)
				if sample == 0 {
					c.AssignmentBytes = after.TotalAlloc - before.TotalAlloc
				}
				c.AssignmentNS = append(c.AssignmentNS, elapsed)
				for i, p := range result {
					if c.Actors[i].Goal != [2]int32{p.x, p.z} {
						t.Fatalf("%s/%d assignment changed", spec.id, n)
					}
				}
			}
			export.Cases = append(export.Cases, c)
		}
	}
	for _, n := range []int{8, 64, 256} {
		var forward, reverse *formationCase
		for i := range export.Cases {
			c := &export.Cases[i]
			if c.Size != n {
				continue
			}
			if c.ID == "line" {
				forward = c
			}
			if c.ID == "reverse" {
				reverse = c
			}
		}
		if forward == nil || reverse == nil {
			t.Fatalf("missing matched line pair for %d", n)
		}
		for i := range forward.Actors {
			if forward.Actors[i].Start != reverse.Actors[i].Start || forward.Actors[i].Goal != reverse.Actors[i].Goal {
				t.Fatalf("forward/reverse differ at size %d actor %d", n, i)
			}
		}
	}
	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("formation fixture: %d cases, %d bytes, %s\n", len(export.Cases), len(data), path)
}
