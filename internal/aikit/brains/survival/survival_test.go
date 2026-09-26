package survival

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// A bearing and the direction it points bin into the same sector, at every
// sector's centre and just inside both of its edges (the sine table steps
// every 128 bearing units).
func TestSectorOfAgreesWithBearings(t *testing.T) {
	for s := 0; s < numSectors; s++ {
		for _, off := range []int32{0, sectorAngle/2 - 160, -(sectorAngle/2 - 160)} {
			a := uint16(int32(s)*sectorAngle + off)
			if got := sectorOfAngle(a); got != s {
				t.Fatalf("bearing %d: sector %d, want %d", a, got, s)
			}
			if got := sectorOf(int64(numeric.Cos(numeric.Angle(a))), int64(numeric.Sin(numeric.Angle(a)))); got != s {
				t.Fatalf("direction of bearing %d: sector %d, want %d", a, got, s)
			}
		}
	}
	if ringDist(0, numSectors-1) != 1 || ringDist(3, 11) != numSectors/2 {
		t.Fatal("ring distance does not wrap")
	}
}

// Sectors are split between the computer buddies by the bearing of their
// starts from the site, so two buddies never plan the same lane; a lone
// buddy owns them all, and the human's start owns none.
func TestSectorsSplitBetweenBuddies(t *testing.T) {
	owned := func(sc Scenario) [numSectors]bool {
		st := &state{sc: sc}
		b := &core.Board{K: &aikit.Kit{Map: &aikit.MapInfo{WorldW: 4096, WorldH: 4096}, Table: &aikit.Table{}}, HomeX: 2048, HomeZ: 2048}
		st.setup(b)
		return st.mine
	}
	two := Scenario{CentreX: 2048, CentreZ: 2048, Team: []uint8{0, 1, 2}, Computer: []bool{false, true, true},
		Starts: [][2]int32{{2048, 2048}, {2368, 2048}, {1728, 2048}}}
	two.Me = 1
	east := owned(two)
	two.Me = 2
	west := owned(two)
	for s := 0; s < numSectors; s++ {
		if east[s] == west[s] {
			t.Fatalf("sector %d: east %v west %v, want exactly one owner", s, east[s], west[s])
		}
	}
	if !east[0] || !west[numSectors/2] {
		t.Fatalf("each buddy owns the sector its start faces: east %v west %v", east, west)
	}
	one := Scenario{CentreX: 2048, CentreZ: 2048, Me: 1, Team: []uint8{0, 1}, Computer: []bool{false, true},
		Starts: [][2]int32{{2048, 2048}, {2368, 2048}}}
	for s, m := range owned(one) {
		if !m {
			t.Fatalf("a lone buddy does not own sector %d", s)
		}
	}
}

// The configuration keys parse strictly and the defaults are Specs'.
func TestParamsFromChecksRanges(t *testing.T) {
	p, err := ParamsFrom(map[string]string{"sv_tower": "30", "sv_walls": "0", "w_army": "120"})
	if err != nil || p.TowerShare != 30 || p.Walls != 0 || p.ClaimShare != DefaultParams().ClaimShare {
		t.Fatalf("got %+v, %v", p, err)
	}
	for _, bad := range []map[string]string{{"sv_tower": "101"}, {"sv_walls": "2"}, {"sv_claim": "x"}, {"survival": "01"}} {
		if _, err := ParamsFrom(bad); err == nil {
			t.Fatalf("%v was accepted", bad)
		}
	}
	if !Enabled(nil) || Enabled(map[string]string{"survival": "0"}) {
		t.Fatal("the survival switch reads wrong")
	}
}

// A warned ground direction draws the tower share toward its sector and
// its neighbours, an air warning does not, and nothing is owed before
// minute 2 (towerStart).
func TestWarnedDirectionDrawsTheTowerShare(t *testing.T) {
	st := &state{p: DefaultParams()}
	for s := range st.mine {
		st.mine[s], st.siteable[s] = true, true
		st.radius[s] = humanRoom
	}
	st.income = 100000
	st.tick = towerStart
	st.live = []Approach{{Angle: 4 * sectorAngle, Other: true}, {Angle: 12 * sectorAngle, Air: true}}
	st.observeWeights()
	total := st.towerWant()
	if want := st.income * int64(st.p.TowerShare) / 100; total != want {
		t.Fatalf("tower total %d, want %d", total, want)
	}
	if !(st.want[4] > st.want[3] && st.want[3] > st.want[2] && st.want[2] > st.want[1] && st.want[1] == st.want[12]) {
		t.Fatalf("wants around the warned sector: %v", st.want)
	}
	st.tick = towerStart - 1
	if st.towerWant() != 0 {
		t.Fatal("towers owed before towerStart")
	}
}

// A buddy placed inside a commander's death explosion of the human's start
// (the session's 320 wu) keeps its home at blastClear on the same bearing,
// and faces outward from there; one placed farther out keeps its start.
func TestHomeStandsClearOfTheHumansCommander(t *testing.T) {
	home := func(start [2]int32) (int32, int32, int32, int32) {
		st := &state{sc: Scenario{CentreX: 2048, CentreZ: 2048, Me: 1, Team: []uint8{0, 1}, Computer: []bool{false, true},
			Starts: [][2]int32{{2048, 2048}, start}}}
		st.setup(&core.Board{K: &aikit.Kit{Map: &aikit.MapInfo{WorldW: 4096, WorldH: 4096}, Table: &aikit.Table{}}})
		return st.hx, st.hz, st.ex, st.ez
	}
	// The facing point is clamped 64 wu inside the map.
	if x, z, ex, ez := home([2]int32{2048, 2368}); x != 2048 || z != 2048+blastClear || ex != 2048 || ez != min(z+econFacing, 4096-64) {
		t.Fatalf("home (%d,%d) facing (%d,%d), want (2048,%d) facing south", x, z, ex, ez, 2048+blastClear)
	}
	if x, z, _, _ := home([2]int32{2048 + 700, 2048}); x != 2048+700 || z != 2048 {
		t.Fatalf("a start clear of the blast moved to (%d,%d)", x, z)
	}
}
