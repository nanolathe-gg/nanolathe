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
			got := dragBuildCells(tt.start, tt.end, tt.foot.x, tt.foot.z, tt.grid)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("cells = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDragBuildBoundAndWideCoordinates(t *testing.T) {
	for _, grid := range []bool{false, true} {
		got := dragBuildCells(dragPoint{math.MinInt32, math.MinInt32}, dragPoint{math.MaxInt32, math.MaxInt32}, 1, 1, grid)
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
	got := dragBuildCells(dragPoint{math.MinInt32, math.MinInt32}, dragPoint{math.MaxInt32, math.MaxInt32}, math.MaxInt32, math.MaxInt32, false)
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
