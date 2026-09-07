package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/mission"
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

func TestP28SkirmishScenarioAngleOverwritesAfterAllocatorDrawSequence(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 43, 47)
	def := s.Catalog.Units["armcom"]
	def.BuildAngle = 4096
	before := s.SimRNG().Draws()
	m := &mission.Mission{Units: []mission.UnitPlacement{{UnitName: "armcom", Player: 0, Angle: 54321}}}
	if err := skirmishReconstructUnits(s, SkirmishConfig{NumPlayers: 0, Location: 1}, m); err != nil {
		t.Fatalf("skirmishReconstructUnits: %v", err)
	}
	created := s.Units.Iter()
	if len(created) != 1 || created[0].Move.Heading != 54321 {
		t.Fatalf("skirmish scenario heading = %+v, want authored 54321", created)
	}
	if got := s.SimRNG().Draws() - before; got != 2 {
		t.Fatalf("skirmish scenario allocation draw delta = %d, want 2", got)
	}
}
