package session

// The skirmish Random-start walk of [08 "Randomization for skirmish starts"]
// [08 R-SKIR-01 §2]: element k = 1 … count−1 swaps with element `draw mod k`.
// The divisor is the element's own index — one for the second element, growing
// by one after each swap — not the index plus one. This file locks the
// property that distinguishes the two: dividing by the index draws the partner
// from the prefix strictly before k, so no element can stay where it started
// and the walk yields only cyclic permutations. With the textbook `k+1` bound
// two eligible slots would keep their own starts on half of all seeds.

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// crtPartners replays the CRT recurrence independently of rng.CRT and returns
// the partner index the walk must pick for each element, so the expectations
// below are derived from the stream definition [01 §7.2] rather than from the
// code under test.
func crtPartners(seed uint32, count int) []int {
	state := seed
	partners := make([]int, 0, count)
	for k := 1; k < count; k++ {
		state = state*214013 + 2531011
		draw := (state >> 16) & 0x7FFF
		partners = append(partners, int(draw)%k)
	}
	return partners
}

func TestSkirmishStartShuffleBoundIsTheIndex(t *testing.T) {
	const seed = 67890
	cases := []struct {
		count int
		want  []int
	}{
		// Hand-derived from the CRT stream at this seed: count 2 takes one
		// draw with bound 1, which always names element 0; count 4 takes three
		// draws with bounds 1, 2 and 3.
		{count: 2, want: []int{1, 0}},
		{count: 4, want: []int{1, 2, 3, 0}},
	}
	for _, tc := range cases {
		crt := rng.NewCRT(seed)
		order := make([]int, tc.count)
		for i := range order {
			order[i] = i
		}
		shuffleEligibleStarts(&crt, order)
		for i := range order {
			if order[i] != tc.want[i] {
				t.Fatalf("count %d permutation %v, want %v [08 R-SKIR-01 §2]", tc.count, order, tc.want)
			}
		}
		// The same permutation re-derived from the stream definition, so the
		// literals above cannot drift with the implementation.
		replay := make([]int, tc.count)
		for i := range replay {
			replay[i] = i
		}
		for k, partner := range crtPartners(seed, tc.count) {
			replay[k+1], replay[partner] = replay[partner], replay[k+1]
		}
		for i := range replay {
			if replay[i] != tc.want[i] {
				t.Fatalf("count %d independent replay %v, want %v [01 §7.2][08 R-SKIR-01 §2]", tc.count, replay, tc.want)
			}
		}
		// One draw per swap, count−1 in total: the bound-1 first swap still
		// takes its draw, so correcting the bound must not move the stream
		// [01 §7.2] [I4].
		if got := crt.Draws(); got != uint64(tc.count-1) {
			t.Fatalf("count %d consumed %d draws, want %d [08 R-SKIR-01 §2]", tc.count, got, tc.count-1)
		}
	}
}

// TestSkirmishStartShuffleLeavesNoSlotInPlace is the structural half: because
// element k's partner is drawn modulo k, every element is moved, whatever the
// seed. A walk that divided by k+1 would leave fixed points on most seeds.
func TestSkirmishStartShuffleLeavesNoSlotInPlace(t *testing.T) {
	for _, count := range []int{2, 4} {
		for seed := uint32(1); seed <= 64; seed++ {
			crt := rng.NewCRT(seed)
			order := make([]int, count)
			for i := range order {
				order[i] = i
			}
			shuffleEligibleStarts(&crt, order)
			for i := range order {
				if order[i] == i {
					t.Fatalf("count %d seed %d left slot %d on its own start: %v; the partner is drawn modulo the index, so only cyclic permutations result [08 R-SKIR-01 §2]", count, seed, i, order)
				}
			}
			// The permutation stays a permutation of the eligible list.
			seen := make(map[int]bool, count)
			for _, v := range order {
				if v < 0 || v >= count || seen[v] {
					t.Fatalf("count %d seed %d produced %v, which is not a permutation [08 R-SKIR-01 §2]", count, seed, order)
				}
				seen[v] = true
			}
			if want := crtPartners(seed, count); len(want) != int(crt.Draws()) {
				t.Fatalf("count %d seed %d consumed %d draws, want %d [08 R-SKIR-01 §2]", count, seed, crt.Draws(), len(want))
			}
		}
	}
}
