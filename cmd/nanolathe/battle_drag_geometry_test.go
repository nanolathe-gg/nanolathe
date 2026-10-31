package main

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

// These fixtures lock the user-requested modern input policy in
// DESIGN_INTERFACE_HUD_INPUT §3.11, not inferred retail behavior.
func TestDragBuildFootprintSpacing(t *testing.T) {
	tests := []struct {
		name       string
		start, end dragPoint
		foot       dragPoint
		grid       bool
		want       []dragPoint
	}{
		{"stationary", dragPoint{4, 7}, dragPoint{4, 7}, dragPoint{2, 3}, false, []dragPoint{{4, 7}}},
		{"short", dragPoint{}, dragPoint{2, 1}, dragPoint{3, 2}, false, []dragPoint{{0, 0}}},
		{"horizontal", dragPoint{3, 4}, dragPoint{10, 4}, dragPoint{3, 2}, false, []dragPoint{{3, 4}, {6, 4}, {9, 4}}},
		{"vertical reverse", dragPoint{3, 4}, dragPoint{3, -3}, dragPoint{3, 2}, false, []dragPoint{{3, 4}, {3, 2}, {3, 0}, {3, -2}}},
		{"normalized Z dominates", dragPoint{}, dragPoint{8, 6}, dragPoint{4, 2}, false, []dragPoint{{0, 0}, {3, 2}, {5, 4}, {8, 6}}},
		{"normalized X dominates", dragPoint{}, dragPoint{6, 8}, dragPoint{2, 4}, false, []dragPoint{{0, 0}, {2, 3}, {4, 5}, {6, 8}}},
		{"half toward end", dragPoint{2, 3}, dragPoint{6, 4}, dragPoint{2, 2}, false, []dragPoint{{2, 3}, {4, 4}, {6, 4}}},
		{"reverse half toward end", dragPoint{6, 4}, dragPoint{2, 3}, dragPoint{2, 2}, false, []dragPoint{{6, 4}, {4, 3}, {2, 3}}},
		{"grid row order", dragPoint{1, 2}, dragPoint{6, 6}, dragPoint{2, 3}, true, []dragPoint{{1, 2}, {3, 2}, {5, 2}, {1, 5}, {3, 5}, {5, 5}}},
		{"reverse grid row order", dragPoint{6, 6}, dragPoint{1, 2}, dragPoint{2, 3}, true, []dragPoint{{6, 6}, {4, 6}, {2, 6}, {6, 3}, {4, 3}, {2, 3}}},
		{"zero footprint rejected", dragPoint{}, dragPoint{8, 8}, dragPoint{0, 2}, false, nil},
		{"negative footprint rejected", dragPoint{}, dragPoint{8, 8}, dragPoint{2, -1}, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dragBuildCells(tt.start, tt.end, tt.foot.x, tt.foot.z, 0, tt.grid)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("cells = %v, want %v", got, tt.want)
			}
		})
	}
}

// The wheel gap adds free cells to every step, on both grid axes; a gap of
// zero is the touching row above.
func TestDragBuildGap(t *testing.T) {
	tests := []struct {
		name       string
		start, end dragPoint
		foot       dragPoint
		gap        int32
		grid       bool
		want       []dragPoint
	}{
		{"row", dragPoint{3, 4}, dragPoint{14, 4}, dragPoint{3, 2}, 2, false, []dragPoint{{3, 4}, {8, 4}, {13, 4}}},
		{"reverse row", dragPoint{3, 4}, dragPoint{3, -6}, dragPoint{3, 2}, 1, false, []dragPoint{{3, 4}, {3, 1}, {3, -2}, {3, -5}}},
		{"short of a step", dragPoint{}, dragPoint{4, 0}, dragPoint{3, 2}, 2, false, []dragPoint{{0, 0}}},
		{"grid", dragPoint{1, 2}, dragPoint{9, 9}, dragPoint{2, 3}, 1, true, []dragPoint{{1, 2}, {4, 2}, {7, 2}, {1, 6}, {4, 6}, {7, 6}}},
		{"negative rejected", dragPoint{}, dragPoint{9, 0}, dragPoint{3, 2}, -1, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dragBuildCells(tt.start, tt.end, tt.foot.x, tt.foot.z, tt.gap, tt.grid)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("cells = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDragBuildBoundAndWideCoordinates(t *testing.T) {
	for _, grid := range []bool{false, true} {
		got := dragBuildCells(dragPoint{math.MinInt32, math.MinInt32}, dragPoint{math.MaxInt32, math.MaxInt32}, 1, 1, 0, grid)
		if len(got) != dragBuildSiteLimit || got[0] != (dragPoint{math.MinInt32, math.MinInt32}) {
			t.Fatalf("grid %v: limit or press anchor lost: %v", grid, got)
		}
		for i, point := range got {
			wantZ := int32(math.MinInt32)
			if !grid {
				wantZ += int32(i)
			}
			if point != (dragPoint{math.MinInt32 + int32(i), wantZ}) {
				t.Fatalf("grid %v: site %d = %v", grid, i, point)
			}
		}
	}
	got := dragBuildCells(dragPoint{math.MinInt32, math.MinInt32}, dragPoint{math.MaxInt32, math.MaxInt32}, math.MaxInt32, math.MaxInt32, 0, false)
	want := []dragPoint{{math.MinInt32, math.MinInt32}, {-1, -1}, {math.MaxInt32 - 1, math.MaxInt32 - 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("wide interpolation = %v, want %v", got, want)
	}
}

func TestDragSampleArcLength(t *testing.T) {
	tests := []struct {
		name  string
		path  []dragPoint
		count int
		want  []dragPoint
	}{
		{"unequal segments", []dragPoint{{0, 0}, {8, 0}, {8, 4}}, 4, []dragPoint{{0, 0}, {4, 0}, {8, 0}, {8, 4}}},
		{"midpoint follows bend", []dragPoint{{0, 0}, {8, 0}, {8, 4}}, 1, []dragPoint{{6, 0}}},
		{"zigzag", []dragPoint{{0, 0}, {3, 4}, {6, 0}}, 5, []dragPoint{{0, 0}, {2, 2}, {3, 4}, {5, 2}, {6, 0}}},
		{"duplicate vertices", []dragPoint{{0, 0}, {0, 0}, {4, 0}, {4, 0}, {4, 4}, {4, 4}}, 3, []dragPoint{{0, 0}, {4, 0}, {4, 4}}},
		{"zero length", []dragPoint{{7, -2}, {7, -2}}, 3, []dragPoint{{7, -2}, {7, -2}, {7, -2}}},
		{"single vertex", []dragPoint{{7, -2}}, 1, []dragPoint{{7, -2}}},
		{"negative half", []dragPoint{{-1, -1}, {-2, -2}}, 1, []dragPoint{{-2, -2}}},
		{"reverse", []dragPoint{{8, 4}, {8, 0}, {0, 0}}, 4, []dragPoint{{8, 4}, {8, 0}, {4, 0}, {0, 0}}},
		{"closed path", []dragPoint{{0, 0}, {4, 0}, {0, 0}}, 3, []dragPoint{{0, 0}, {4, 0}, {0, 0}}},
		{"wide coordinates", []dragPoint{{math.MinInt32, 0}, {math.MaxInt32, 0}}, 3, []dragPoint{{math.MinInt32, 0}, {-1, 0}, {math.MaxInt32, 0}}},
		{"empty path", nil, 3, nil},
		{"no actors", []dragPoint{{0, 0}, {4, 4}}, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dragSamplePath(tt.path, tt.count)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("destinations = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDragAssignGlobalNearest(t *testing.T) {
	tests := []struct {
		name                 string
		actors, destinations []dragPoint
		want                 []dragPoint
	}{
		{"actor order", []dragPoint{{8, 0}, {1, 0}, {5, 0}}, []dragPoint{{0, 0}, {5, 0}, {10, 0}}, []dragPoint{{10, 0}, {0, 0}, {5, 0}}},
		{"early actor must yield closest spot", []dragPoint{{6, 0}, {9, 0}}, []dragPoint{{0, 0}, {10, 0}}, []dragPoint{{0, 0}, {10, 0}}},
		{"stable tie", []dragPoint{{0, 0}, {0, 0}}, []dragPoint{{1, 0}, {-1, 0}}, []dragPoint{{-1, 0}, {1, 0}}},
		{"duplicate destinations remain separate", []dragPoint{{0, 0}, {0, 0}, {0, 0}}, []dragPoint{{1, 0}, {1, 0}, {2, 0}}, []dragPoint{{1, 0}, {1, 0}, {2, 0}}},
		{"wide coordinate distance", []dragPoint{{math.MinInt32, math.MinInt32}}, []dragPoint{{math.MaxInt32, math.MaxInt32}, {math.MaxInt32, math.MinInt32}}, []dragPoint{{math.MaxInt32, math.MinInt32}}},
		{"fewer destinations", []dragPoint{{0, 0}, {4, 0}}, []dragPoint{{3, 0}}, []dragPoint{{3, 0}}},
		{"empty", []dragPoint{{0, 0}}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dragAssignDestinations(tt.actors, tt.destinations)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("assignments = %v, want %v", got, tt.want)
			}
		})
	}
}

// Compare the solver with every possible assignment for small authored layouts.
// This checks the total-cost bound independently of the auction implementation.
func TestDragAssignNearMinimumTotalDistance(t *testing.T) {
	for scene := 0; scene < 24; scene++ {
		actors := make([]dragPoint, 5)
		goals := make([]dragPoint, 5+scene%2)
		for i := range actors {
			actors[i] = dragPoint{int32((i*7 + scene*3) % 19), int32((i*i + scene*5) % 23)}
		}
		for i := range goals {
			goals[i] = dragPoint{int32((i*11 + scene) % 29), int32((i*i*3 + scene*2) % 17)}
		}
		distance := func(a, b dragPoint) float64 { return math.Hypot(float64(a.x)-float64(b.x), float64(a.z)-float64(b.z)) }
		best := math.Inf(1)
		var search func(int, uint, float64)
		search = func(i int, used uint, cost float64) {
			if i == len(actors) {
				best = min(best, cost)
				return
			}
			for j, g := range goals {
				if used&(1<<j) == 0 {
					search(i+1, used|(1<<j), cost+distance(actors[i], g))
				}
			}
		}
		search(0, 0, 0)
		original := slices.Clone(goals)
		assigned := dragAssignDestinations(actors, goals)
		total := 0.0
		remaining := slices.Clone(goals)
		for i, g := range assigned {
			j := slices.Index(remaining, g)
			if j < 0 {
				t.Fatalf("scene %d: destination reused or invented: %v", scene, assigned)
			}
			remaining = slices.Delete(remaining, j, j+1)
			total += distance(actors[i], g)
		}
		if total < best-1e-9 || total-best >= 1 {
			t.Fatalf("scene %d: total %g, minimum %g", scene, total, best)
		}
		if !slices.Equal(goals, original) {
			t.Fatal("destination input mutated")
		}
		slices.Reverse(goals)
		if reversed := dragAssignDestinations(actors, goals); !slices.Equal(assigned, reversed) {
			t.Fatalf("scene %d: reversing destination order changed assignment", scene)
		}
	}
}

func BenchmarkDragAssignDestinations(b *testing.B) {
	for _, n := range []int{250, 500, 1000} {
		for _, layout := range []string{"spread", "cluster"} {
			b.Run(fmt.Sprintf("%s/%d", layout, n), func(b *testing.B) {
				actors := make([]dragPoint, n)
				for i := range actors {
					actors[i] = dragPoint{int32((i * 79) % 1024), int32((i * 53) % 256)}
					if layout == "cluster" {
						actors[i].x %= 64
						actors[i].z %= 64
					}
				}
				goals := dragSamplePath([]dragPoint{{0, 300}, {512, 600}, {1024, 300}}, n)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					dragAssignDestinations(actors, goals)
				}
			})
		}
	}
}

// dragNearestChainFullScan is the plain definition the grid walk must equal:
// at every step the nearest remaining point, ties to the lower index.
func dragNearestChainFullScan(start dragPoint, points []dragPoint) []int {
	taken := make([]bool, len(points))
	order := make([]int, 0, len(points))
	from := start
	for range points {
		best, bestD := -1, int64(0)
		for i, p := range points {
			dx, dz := int64(p.x)-int64(from.x), int64(p.z)-int64(from.z)
			if d := dx*dx + dz*dz; !taken[i] && (best < 0 || d < bestD) {
				best, bestD = i, d
			}
		}
		taken[best] = true
		order = append(order, best)
		from = points[best]
	}
	return order
}

func TestDragNearestChainWalksOutward(t *testing.T) {
	// Publication order 300, 100, 200 along a row; a worker at 0 visits
	// 100, 200, 300 and one at 400 visits 300, 200, 100.
	points := []dragPoint{{300, 50}, {100, 50}, {200, 50}}
	if got := dragNearestChain(dragPoint{0, 50}, points); !slices.Equal(got, []int{1, 2, 0}) {
		t.Fatalf("from the west %v", got)
	}
	if got := dragNearestChain(dragPoint{400, 50}, points); !slices.Equal(got, []int{0, 2, 1}) {
		t.Fatalf("from the east %v", got)
	}
	if got := dragNearestChain(dragPoint{}, nil); len(got) != 0 {
		t.Fatalf("empty %v", got)
	}
}

func TestDragNearestChainMatchesFullScan(t *testing.T) {
	// A fixed linear congruential stream keeps the cases reproducible. Small
	// coordinate ranges force duplicates and ties; the wide ranges widen the
	// grid cell; starts far outside the area exercise the first full scan.
	seed := uint32(12345)
	next := func(n int32) int32 {
		seed = seed*1103515245 + 12345
		return int32((seed >> 8) % uint32(n))
	}
	for c, spread := range []int32{8, 300, 2000, 40000, 2000000} {
		for trial := range 25 {
			points := make([]dragPoint, 1+next(300))
			for i := range points {
				points[i] = dragPoint{next(spread) - spread/3, next(spread) - spread/2}
			}
			start := dragPoint{next(3*spread) - spread, next(3*spread) - spread}
			got, want := dragNearestChain(start, points), dragNearestChainFullScan(start, points)
			if !slices.Equal(got, want) {
				t.Fatalf("spread %d trial %d (case %d): grid %v, full scan %v", spread, trial, c, got, want)
			}
		}
	}
}

func TestDragAreaGroupsKeepTheFanOutBudget(t *testing.T) {
	row := func(n int) []dragPoint {
		out := make([]dragPoint, n)
		for i := range out {
			// Selection order deliberately differs from position order.
			out[i] = dragPoint{int32((i * 7919) % (n * 10)), int32(i % 3)}
		}
		return out
	}
	// Room for everyone: each worker starts from where it stands.
	groups, starts := dragAreaGroups(row(5), 100, dragAreaCommandLimit)
	if len(groups) != 5 || !slices.Equal(starts, row(5)) {
		t.Fatalf("five workers, 100 entries: %v from %v", groups, starts)
	}
	for i, g := range groups {
		if !slices.Equal(g, []int{i}) {
			t.Fatalf("worker %d grouped %v", i, g)
		}
	}
	for _, tc := range []struct{ workers, entries int }{
		{1, 1}, {16, 256}, {17, 10}, {40, 10}, {64, 1}, {200, 3}, {5, 1000}, {5, 3000}, {9, 4096}, {12, 9000}, {3, 0},
	} {
		workers := row(tc.workers)
		groups, starts := dragAreaGroups(workers, tc.entries, dragAreaCommandLimit)
		if len(groups) == 0 || len(groups) != len(starts) || len(groups) > dragAreaCommandLimit {
			t.Fatalf("%+v: %d groups", tc, len(groups))
		}
		if total := len(groups) * tc.entries; total > max(tc.entries, dragAreaEntryBudget) {
			t.Fatalf("%+v: %d entries sent", tc, total)
		}
		var seen []int
		for g, members := range groups {
			if len(members) == 0 || !slices.IsSorted(members) {
				t.Fatalf("%+v: group %d is %v", tc, g, members)
			}
			if !slices.ContainsFunc(members, func(w int) bool { return workers[w] == starts[g] }) {
				t.Fatalf("%+v: group %d starts outside itself", tc, g)
			}
			seen = append(seen, members...)
		}
		slices.Sort(seen)
		for i, w := range seen {
			if w != i {
				t.Fatalf("%+v: workers covered %v", tc, seen)
			}
		}
		// Shared runs are contiguous along the wider axis, here X; singletons
		// keep selection order.
		for g := 1; g < len(groups) && len(groups) < tc.workers; g++ {
			prevMax, nextMin := int32(math.MinInt32), int32(math.MaxInt32)
			for _, w := range groups[g-1] {
				prevMax = max(prevMax, workers[w].x)
			}
			for _, w := range groups[g] {
				nextMin = min(nextMin, workers[w].x)
			}
			if prevMax > nextMin {
				t.Fatalf("%+v: groups %d and %d overlap along X", tc, g-1, g)
			}
		}
	}
	// A list longer than the budget goes as one command, as it always could.
	if groups, _ := dragAreaGroups(row(5), 5000, dragAreaCommandLimit); len(groups) != 1 || len(groups[0]) != 5 {
		t.Fatalf("oversized list split into %v", groups)
	}
	// Online the limit is one command: every worker shares the list, which
	// walks from the middle worker along the wider axis.
	workers := []dragPoint{{900, 0}, {100, 5}, {500, 9}, {300, 1}}
	groups, starts = dragAreaGroups(workers, 3, 1)
	if len(groups) != 1 || !slices.Equal(groups[0], []int{0, 1, 2, 3}) || starts[0] != (dragPoint{500, 9}) {
		t.Fatalf("online grouping %v from %v", groups, starts)
	}
}

func TestAreaCommandLimitIsOneOnline(t *testing.T) {
	b := &battleSession{}
	if got := b.areaCommandLimit(); got != dragAreaCommandLimit {
		t.Fatalf("single-player limit %d", got)
	}
	b.multiplayer = &battleMultiplayer{}
	if got := b.areaCommandLimit(); got != 1 {
		t.Fatalf("online limit %d", got)
	}
}
