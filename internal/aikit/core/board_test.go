package core

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// A spot claimed by a named builder stays planned while that builder
// lives and is released as soon as it is gone — dead, or its slot holding
// another unit; a claim without a builder lasts its lease.
func TestSpotClaimFollowsBuilder(t *testing.T) {
	m := &aikit.MapInfo{SectorW: 10, SectorH: 10, WorldW: 1280, WorldH: 1280,
		Spots: []aikit.MetalSpot{{X: 300, Z: 300}, {X: 900, Z: 900}}}
	k := &aikit.Kit{Map: m}
	con := &aikit.UnitInfo{Role: aikit.RoleMobile | aikit.RoleBuilder, Value: 100}
	own := func(gen uint32) *aikit.Obs {
		return &aikit.Obs{Own: []aikit.OwnUnit{{H: 4, Gen: gen, Info: con, Built: true, X: 600, Z: 600}}}
	}
	var b Board
	think := func(tick uint32, o *aikit.Obs) {
		o.Tick = tick
		b.Update(k, o)
	}
	think(100, own(1))
	b.ClaimSpot(0, 4)
	b.PlanSpot(1)
	think(115, own(1))
	if b.Spots[0] != SpotPlanned || b.Spots[1] != SpotPlanned {
		t.Fatalf("claims not kept: %v", b.Spots)
	}
	think(130, own(2)) // the builder died and its slot holds a new one
	if b.Spots[0] != SpotFree || b.Spots[1] != SpotPlanned {
		t.Errorf("recycled builder slot: %v, want the named claim released", b.Spots)
	}
	b.ClaimSpot(0, 4)
	think(145, &aikit.Obs{}) // the builder is gone
	if b.Spots[0] != SpotFree {
		t.Errorf("dead builder's claim kept: %v", b.Spots)
	}
	think(100+claimTicks, &aikit.Obs{})
	if b.Spots[1] != SpotFree {
		t.Errorf("anonymous claim outlived its lease: %v", b.Spots)
	}
}

// A task starts over when its unit's slot holds another instance.
func TestTaskOfFollowsInstance(t *testing.T) {
	con := &aikit.UnitInfo{Role: aikit.RoleMobile | aikit.RoleBuilder}
	b := Board{O: &aikit.Obs{Own: []aikit.OwnUnit{{H: 2, Gen: 1, Info: con}}}}
	b.TaskOf(0).Since = 500
	if b.TaskOf(0).Since != 500 {
		t.Fatal("task lost for the same unit")
	}
	b.O.Own[0].Gen = 2
	if b.TaskOf(0).Since != 0 {
		t.Error("a new unit in the slot inherited the task")
	}
}

// A held spot (a placement met a building we have not seen) is neither
// free nor planned until an own unit has looked at it with no enemy
// building remembered there — not in the first holdLookMin ticks, so the
// failed builder standing beside it does not release it at once — or the
// hold lapses; a remembered enemy extractor reads as enemy.
func TestSpotHold(t *testing.T) {
	m := &aikit.MapInfo{SectorW: 10, SectorH: 10, WorldW: 1280, WorldH: 1280,
		Spots: []aikit.MetalSpot{{X: 300, Z: 300}, {X: 900, Z: 900}}}
	k := &aikit.Kit{Map: m}
	con := &aikit.UnitInfo{Role: aikit.RoleMobile | aikit.RoleBuilder, Value: 100}
	tower := &aikit.UnitInfo{Role: aikit.RoleDefense, DPS: 10, HP: 100}
	mex := &aikit.UnitInfo{Role: aikit.RoleExtractor, HP: 100}
	at := func(x, z int32, mem ...aikit.Remembered) *aikit.Obs {
		return &aikit.Obs{Own: []aikit.OwnUnit{{H: 4, Gen: 1, Info: con, Built: true, X: x, Z: z}}, Memory: mem}
	}
	var b Board
	think := func(tick uint32, o *aikit.Obs) {
		o.Tick = tick
		b.Update(k, o)
	}
	think(100, at(380, 300)) // the builder stands beside spot 0
	b.ClaimSpot(0, 4)
	b.HoldSpot(0)
	if b.Spots[0] != SpotHeld {
		t.Fatalf("hold not applied: %v", b.Spots)
	}
	think(115, at(380, 300))
	if b.Spots[0] != SpotHeld {
		t.Errorf("released at once by the builder beside it: %v", b.Spots)
	}
	think(100+holdLookMin, at(700, 700))
	if b.Spots[0] != SpotHeld {
		t.Errorf("released with no unit looking: %v", b.Spots)
	}
	think(115+holdLookMin, at(380, 300))
	if b.Spots[0] != SpotFree {
		t.Errorf("a look found nothing but the spot stayed held: %v", b.Spots)
	}
	// A remembered enemy building on the spot keeps it held however
	// close we stand; the hold lapses after holdTicks.
	b.HoldSpot(0)
	towerMem := aikit.Remembered{H: 9, Info: tower, X: 310, Z: 300, Building: true}
	think(2000+holdLookMin, at(320, 300, towerMem))
	if b.Spots[0] != SpotHeld {
		t.Errorf("released although an enemy building is remembered there: %v", b.Spots)
	}
	think(115+holdLookMin+holdTicks, at(700, 700, towerMem))
	if b.Spots[0] != SpotFree {
		t.Errorf("hold outlived its lease: %v", b.Spots)
	}
	b.HoldSpot(1)
	think(116+holdLookMin+holdTicks, at(700, 700, aikit.Remembered{H: 10, Info: mex, X: 900, Z: 905, Building: true}))
	if b.Spots[1] != SpotEnemy {
		t.Errorf("a remembered enemy extractor on a held spot: %v, want enemy", b.Spots)
	}
}

// Before any enemy building is seen the board guesses the nearest start
// nobody of ours has looked at; with the start assignment public it
// guesses only among the opponents' starts.
func TestEnemyGuessKnownStarts(t *testing.T) {
	com := &aikit.UnitInfo{Role: aikit.RoleMobile | aikit.RoleBuilder | aikit.RoleCommander, Value: 2500}
	for _, known := range []bool{false, true} {
		m := &aikit.MapInfo{SectorW: 10, SectorH: 10, WorldW: 1280, WorldH: 1280,
			Starts: [][2]int32{{200, 200}, {700, 200}, {1100, 1100}}}
		want := [2]int32{700, 200}
		if known {
			m.StartEnemy = []bool{false, false, true}
			want = [2]int32{1100, 1100}
		}
		var b Board
		o := &aikit.Obs{Tick: 30, Own: []aikit.OwnUnit{{H: 1, Gen: 1, Info: com, Built: true, X: 200, Z: 200}}}
		b.Update(&aikit.Kit{Map: m}, o)
		if b.EnemyKnown || b.EnemyX != want[0] || b.EnemyZ != want[1] {
			t.Errorf("known %v: enemy guess (%d,%d) known=%v, want (%d,%d)", known, b.EnemyX, b.EnemyZ, b.EnemyKnown, want[0], want[1])
		}
	}
}
