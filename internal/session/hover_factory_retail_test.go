package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Reproduce the captured ARM hover plant's site and queue with stock scripts.
// The constructor must allocate, finish and release the following queued unit
// [05 "Factory production lifecycle"][02 R-MALF-01 §2].
func TestHoverFactoryContinuesPastConstructorRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	cfg := SkirmishConfig{MapName: "Great Divide", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0
	cfg.Players[1].Controller = 1
	s, err := NewSkirmishWithFS(fs, cat, cfg)
	if err != nil {
		t.Fatal(err)
	}
	driver := int32(1 << 20)
	step := func() { s.Step(driver); driver++; fr4Enrich(s) }
	for i := 0; i < 60 && s.State != StateBattle; i++ {
		step()
	}
	if s.State != StateBattle {
		t.Fatal("battle did not open")
	}
	d, ok := cat.Unit("armhp")
	if !ok {
		t.Fatal("missing armhp")
	}
	h, err := s.Units.Create(d, s.LocalOwner, numeric.FixedFromInt(368), numeric.FixedFromInt(80), numeric.FixedFromInt(520))
	if err != nil {
		t.Fatal(err)
	}
	factory := s.Units.Unit(h)
	factory.Move.Heading = 32768
	if err := s.Build.RegisterBuildingPlacement(factory); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"armch", "armsh"} {
		if err := construction.QueueFactoryBuild(factory, key, 1, cat); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"armch", "armsh"} {
		if product := waitCompletedUnit(t, s, step, 4500, func(u *units.Unit) bool {
			return u.Def.CanonicalKey == key && u.Owner == s.LocalOwner
		}); product == nil {
			t.Fatalf("%s never completed: %v", key, s.Build.Messages())
		}
	}
}
