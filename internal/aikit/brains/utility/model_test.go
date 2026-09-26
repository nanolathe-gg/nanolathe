package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// spotTerr keeps each spot's distances to home and to the believed enemy
// base; it answers as spotTerritory does under either territory, with and
// without the contest part, as the enemy base moves and moves back and
// after home moves.
func TestSpotTerrIsSpotTerritory(t *testing.T) {
	m := &aikit.MapInfo{WorldW: 4096, WorldH: 4096, SectorW: 32, SectorH: 32}
	for x := int32(100); x < 4096; x += 450 {
		for z := int32(150); z < 4096; z += 420 {
			m.Spots = append(m.Spots, aikit.MetalSpot{X: x, Z: z})
		}
	}
	b := &core.Board{K: &aikit.Kit{Map: m}, OwnPower: aikit.NewGrid(m), Threat: aikit.NewGrid(m)}
	b.OwnPower.AddDisc(2000, 2000, 900, 100)
	b.Threat.AddDisc(2300, 2200, 300, 70)
	for _, p := range []Params{{}, {Expand: xContest}, {Growth: gExpand, Expand: xContest}} {
		s := &shared{p: p, k: &aikit.Kit{Persona: aikit.PersonaHard}, tick: growFrom * 1800}
		b.HomeX, b.HomeZ = 512, 512
		contested := 0
		for _, at := range [][4]int32{{3584, 3584, 512, 512}, {3584, 512, 512, 512}, {2600, 2400, 512, 512}, {3584, 3584, 512, 512}, {3584, 3584, 700, 300}} {
			s.enemyX, s.enemyZ, b.HomeX, b.HomeZ = at[0], at[1], at[2], at[3]
			for i := range m.Spots {
				sp := &m.Spots[i]
				got, want := s.spotTerr(b, i), s.spotTerritory(b, sp.X, sp.Z)
				if got != want {
					t.Fatalf("params %+v, enemy (%d, %d), home (%d, %d), spot (%d, %d): spotTerr %d, spotTerritory %d",
						p, at[0], at[1], at[2], at[3], sp.X, sp.Z, got, want)
				}
				if p.Growth == 0 && got == one && s.territory(b, sp.X, sp.Z) < one {
					contested++
				}
			}
		}
		if p == (Params{Expand: xContest}) && contested == 0 {
			t.Errorf("params %+v: no spot was contested; the check did not reach contested()", p)
		}
	}
}

// The per-handle tables grow once, on the first handle they are asked
// for, to cover every handle of the owner's pool slice: a later handle in
// the slice finds the table already long enough.
func TestHandleTablesAreSizedOnce(t *testing.T) {
	d := newDefUnits()
	s := &shared{}
	s.observeHandles(&aikit.Obs{UnitLimit: 250, Own: []aikit.OwnUnit{{H: 1001, Info: d.com}, {H: 1003, Info: d.con}}})
	if s.hcap != 1001+250+1 {
		t.Fatalf("hcap %d, want %d (lowest handle + unit limit + 1)", s.hcap, 1001+250+1)
	}
	s.commitOf(&aikit.OwnUnit{H: 1001, Gen: 1, Info: d.com})
	first := &s.commit[0]
	for h := pool.Handle(1001); h < 1001+250; h++ {
		s.commitOf(&aikit.OwnUnit{H: h, Gen: 1, Info: d.con})
	}
	if &s.commit[0] != first || len(s.commit) != s.hcap {
		t.Errorf("the table grew again within the slice: len %d, hcap %d", len(s.commit), s.hcap)
	}
	// Unknown limit: it grows as needed, as before.
	s2 := &shared{}
	s2.observeHandles(&aikit.Obs{Own: []aikit.OwnUnit{{H: 7, Info: d.com}}})
	s2.commitOf(&aikit.OwnUnit{H: 7, Gen: 1, Info: d.com})
	if s2.hcap != 0 || len(s2.commit) != 8 {
		t.Errorf("without a unit limit: hcap %d, len %d (want 0, 8)", s2.hcap, len(s2.commit))
	}
}
