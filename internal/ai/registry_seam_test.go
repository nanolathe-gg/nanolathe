package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// The per-side target registry rebuild and the 30-tick strategic refresh are
// ONE retail routine on one object per player slot, and the bound-30 draw is
// the one draw that routine takes [06 §3.1][08 R-AI-01 §16]. This build splits
// the routine across two packages, so the contract that survives the split is:
// one gate, one call into the combat half per due, in retail's order — the
// candidate lists first, then the census and the centroid, then the draw.
func TestRegistrySeamRunsOncePerDueBeforeTheDraw(t *testing.T) {
	s := &Strategic{}
	s.Init([]string{"armfav", "corfav"})
	r := rng.NewSimulation(1)

	type call struct {
		tick        uint32
		player      uint8
		drawsBefore uint64
		refreshTick uint32
	}
	var calls []call
	s.BindTargetRegistryRebuild(func(tick uint32, player uint8) {
		calls = append(calls, call{tick, player, r.Draws(), s.LastRefreshTick})
	})
	if !s.TargetRegistryRebuildBound() {
		t.Fatal("the seam reports itself unbound after BindTargetRegistryRebuild")
	}

	// Ticks 0..29 are inside the first window: the gate refuses, so neither
	// half runs and no draw is taken [06 §3.1 "Which slots draw"].
	for tick := uint32(0); tick < refreshInterval; tick++ {
		if s.MaybeRefresh(tick, &r, 3, nil) {
			t.Fatalf("tick %d refreshed inside the first window", tick)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("the combat half ran %d times before the first due, want 0", len(calls))
	}

	// Ninety ticks is three dues: 30, 60, 90.
	for tick := refreshInterval; tick <= 3*refreshInterval; tick++ {
		s.MaybeRefresh(tick, &r, 3, nil)
	}
	if len(calls) != 3 {
		t.Fatalf("the combat half ran %d times over three dues, want one per due: %+v", len(calls), calls)
	}
	if r.Draws() != 3 {
		t.Fatalf("draws %d over three dues, want exactly one bound-30 draw per due (I4)", r.Draws())
	}
	for i, c := range calls {
		wantTick := uint32(i+1) * refreshInterval
		if c.tick != wantTick {
			t.Fatalf("call %d ran at tick %d, want the due at %d", i, c.tick, wantTick)
		}
		if c.player != 3 {
			t.Fatalf("call %d carried player %d, want the refreshing slot 3", i, c.player)
		}
		// Retail's order inside the routine: the lists are filed during the
		// walk and the draw is taken at the end of it, so the combat half must
		// see the draw count the due started with [06 §3.1].
		if c.drawsBefore != uint64(i) {
			t.Fatalf("call %d saw %d draws, want %d: the draw must follow the rebuild, not precede it", i, c.drawsBefore, i)
		}
		// It must also see the PREVIOUS due's stamp, i.e. it runs before the
		// gate word is advanced, so a rebuild and its cadence stamp cannot be
		// separated by an early return.
		if c.refreshTick != uint32(i)*refreshInterval {
			t.Fatalf("call %d saw LastRefreshTick %d, want %d", i, c.refreshTick, uint32(i)*refreshInterval)
		}
	}
}

// The seam is optional: a fixture that binds nothing still refreshes its census
// half on the same gate and takes the same one draw, so binding cannot change
// the stream (I4).
func TestRegistrySeamUnboundKeepsTheDrawLedger(t *testing.T) {
	bound, unbound := &Strategic{}, &Strategic{}
	bound.Init([]string{"armfav"})
	unbound.Init([]string{"armfav"})
	bound.BindTargetRegistryRebuild(func(uint32, uint8) {})

	rb, ru := rng.NewSimulation(9), rng.NewSimulation(9)
	for tick := uint32(0); tick <= 5*refreshInterval; tick++ {
		if bound.MaybeRefresh(tick, &rb, 0, nil) != unbound.MaybeRefresh(tick, &ru, 0, nil) {
			t.Fatalf("the two gates disagreed at tick %d", tick)
		}
	}
	if rb.Draws() != ru.Draws() {
		t.Fatalf("bound drew %d and unbound drew %d; the seam must consume nothing (I4)", rb.Draws(), ru.Draws())
	}
	if rb.Draws() != 5 {
		t.Fatalf("draws %d over five dues, want 5", rb.Draws())
	}
}
