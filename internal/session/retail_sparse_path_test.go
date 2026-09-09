package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"testing"
)

// Decoded player accounts retain their indices; composition must not turn the
// participant count into a prefix of eligible slots [08 "Player records"]
// [04 R-PATH-01 §6]. Exercise the same player adapter as battle staging.
func TestDecodedSparsePlayersComposePathService(t *testing.T) {
	b := save.NewBuilder()
	save.WriteGameTime(b, &clock.State{})
	save.WritePlayerSlot(b, save.PlayerSlot{Index: 0, Controller: 1})
	save.WritePlayerSlot(b, save.PlayerSlot{Index: 3, Controller: 2})
	bank, err := save.OpenBytes(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cat := strictMinimalCatalog()
	s := &Session{Catalog: cat, World: strictMinimalTerrain(), Mission: strictSyntheticMission(), Clock: &clock.State{}, Econ: &economy.Service{}}
	s.Units, err = newSlicedWorld(cat)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range save.ReadAllPlayerSlots(bank) {
		p.ApplyToEconomy(&s.Econ.Players[p.Index])
		s.Econ.Players[p.Index].Exists = true
	}
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatal(err)
	}
	if s.Movement.PathPlayers != 2 || s.Econ.Players[1].Exists {
		t.Fatal("sparse topology was compacted or divisor changed")
	}
	h, err := s.Units.Create(cat.Units["armcom"], 3, world.CellToWorld(2), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatal(err)
	}
	s.Movement.EnsureUnit(s.Units.Unit(h))
	s.Movement.BeginTick(1)
	s.Movement.SubmitMove(h, 3, path.Cell{X: 2, Z: 2}, path.Cell{X: 9, Z: 9})
	for tick := uint32(1); tick < 10 && s.Movement.HasPathRequest(h); tick++ {
		s.Path.Tick(tick)
	}
	if route := s.Movement.Routes[h]; route == nil || route.Count == 0 || route.Status != 0 {
		t.Fatalf("restored slot 3 route = %+v", route)
	}
	if s.Units.Unit(h).Owner != 3 {
		t.Fatal("unit owner renumbered")
	}
}
