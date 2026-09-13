package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
)

func TestP28MissionAngleOverwritesAfterAllocatorDrawSequence(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 7, 11)
	def := s.Catalog.Units["armcom"]
	def.BuildAngle = 4096
	before := s.SimRNG().Draws()
	m := &mission.Mission{Units: []mission.UnitPlacement{{UnitName: "armcom", Player: 1, Angle: 12345}}}
	if err := reconstructUnits(s, m); err != nil {
		t.Fatalf("reconstructUnits: %v", err)
	}
	created := s.Units.Iter()
	if len(created) != 1 || created[0].Move.Heading != 12345 {
		t.Fatalf("mission heading = %+v, want authored 12345", created)
	}
	if got := s.SimRNG().Draws() - before; got != 2 {
		t.Fatalf("mission allocation draw delta = %d, want 2 before authored overwrite", got)
	}
}

func TestP28SameSeedProducesSameHeadingAndPartialFingerprint(t *testing.T) {
	makeRun := func(seed uint32) (uint16, string) {
		s := strictNewSessionWithUnits(t, 0, seed, 19)
		def := s.Catalog.Units["armcom"]
		def.BuildAngle = 4096
		h, err := s.Units.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		hash, err := s.PartialStateFingerprint()
		if err != nil {
			t.Fatalf("PartialStateFingerprint: %v", err)
		}
		return s.Units.Unit(h).Move.Heading, hash
	}
	headingA, hashA := makeRun(29)
	headingB, hashB := makeRun(29)
	if headingA != headingB || hashA != hashB {
		t.Fatalf("same seed differs: heading %d/%d hash %q/%q", headingA, headingB, hashA, hashB)
	}
	headingC, hashC := makeRun(30)
	if headingA == headingC && hashA == hashC {
		t.Fatal("different seed produced identical heading and partial fingerprint")
	}
}

func TestP28CompositionBindsSimulationStreamBeforeCreation(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 37, 41)
	def := s.Catalog.Units["armcom"]
	def.BuildAngle = 4096
	before := s.SimRNG().Draws()
	if _, err := s.Units.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := s.SimRNG().Draws() - before; got != 2 {
		t.Fatalf("composition-bound allocation draw delta = %d, want 2", got)
	}
}

func TestP28SkirmishIgnoresAuthoredUnitsWithoutConsumingDraws(t *testing.T) {
	makeRun := func(placements []mission.UnitPlacement) *Session {
		s := strictNewSessionWithUnits(t, 0, 43, 47)
		s.Catalog.Units["armcom"].BuildAngle = 4096
		cfg := SkirmishConfig{NumPlayers: 2, Location: 1}
		prepareFixtureSkirmishCatalog(s.Catalog, &cfg)
		m := &mission.Mission{
			Specials: []mission.Special{{Kind: 1, ID: 0, X: 10, Z: 10}, {Kind: 1, ID: 1, X: 20, Z: 20}},
			Units:    placements,
		}
		before := s.SimRNG().Draws()
		if err := skirmishReconstructUnits(s, cfg, m); err != nil {
			t.Fatalf("skirmishReconstructUnits: %v", err)
		}
		if got := len(s.Units.Iter()); got != cfg.NumPlayers {
			t.Fatalf("created %d units, want only %d eligible commanders", got, cfg.NumPlayers)
		}
		if got := s.SimRNG().Draws() - before; got != uint64(2*cfg.NumPlayers) {
			t.Fatalf("allocation draw delta = %d, want two per eligible commander", got)
		}
		return s
	}
	plain := makeRun(nil)
	scenario := makeRun([]mission.UnitPlacement{
		{UnitName: "armcom", Player: 0, Angle: 54321},
		{UnitName: "armcom", Player: 6, Angle: 12345},
	})
	for i, want := range plain.Units.Iter() {
		got := scenario.Units.Iter()[i]
		if got.Owner != want.Owner || got.Move.Heading != want.Move.Heading || got.PlacementIdx != -1 {
			t.Fatalf("scenario changed eligible commander %d: got %+v, want %+v", i, got, want)
		}
	}
}
