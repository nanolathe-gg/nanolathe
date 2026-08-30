package session

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// RX-01: production sessions bind the AI typed build queue [F-P0-004].
// Human and computer records are both constructed and bound; only the
// controller-2 record dispatches task bodies [08 R-ENTRY-01 §3 step 24, §8].

func TestRX01_ProductionSessionsBindAIQueue(t *testing.T) {
	rng.SeedGlobal(11, 12)
	cat := minimalCatalogForStrict()
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/test.otA":  "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=100;\nZPos=100;\n}\n}\n}\n}\n",
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0 // human
	cfg.Players[1].Controller = 1 // computer
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	// Fixture OTA carries no TNT section; complete composition like the
	// topology test does before validating.
	if s.World == nil {
		s.World = minimalTerrain()
		s.Features = nil
		s.Vis = nil
		s.Movement = nil
		s.Path = nil
		s.Build = nil
		s.Combat = nil
		if err := createAndBindServicesForTest(t, s); err != nil {
			t.Fatalf("createAndBindServices: %v", err)
		}
		publishVisibilityForAll(s)
	}
	count := 0
	for _, m := range s.AI {
		if m != nil {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("want exactly two manager records for live human+computer slots, got %d", count)
	}
	for i := 0; i < 2; i++ {
		if s.AI[i] == nil || s.AI[i].QueueBuildTyped == nil {
			t.Fatalf("production session left player %d manager record unbound [F-P0-004]", i)
		}
	}
	for task, deadline := range s.AI[0].Deadlines {
		if deadline != 0 {
			t.Fatalf("human manager dispatched task %d at tick 0: deadline=%d", task, deadline)
		}
	}
	computerDispatched := false
	for _, deadline := range s.AI[1].Deadlines {
		computerDispatched = computerDispatched || deadline != 0
	}
	if !computerDispatched {
		t.Fatal("controller-2 manager did not execute due tick-0 task bodies")
	}
	if err := s.ValidateComposition(); err != nil {
		t.Fatalf("composition with bound AI must validate: %v", err)
	}
	// An unbound manager is a composition failure, not a silent passive slot.
	mgr := s.AI[1] // RS-02: player-indexed, player 1 at index 1
	mgr.QueueBuildTyped = nil
	err = s.ValidateComposition()
	if err == nil || !strings.Contains(err.Error(), "QueueBuildTyped") {
		t.Fatalf("unbound AI manager must fail validation with binder diagnostic, got %v", err)
	}
}

// The bound callback routes typed requests into the ordinary construction
// queues with site coordinates intact [ON-06][05].
func TestRX01_BoundBuildRequestPreservesMobileBuildCoordinates(t *testing.T) {
	rng.SeedGlobal(13, 14)
	cat := minimalCatalogForStrict()
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/test.otA":  "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=100;\nZPos=100;\n}\n}\n}\n}\n",
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0
	cfg.Players[1].Controller = 1
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	var builder *units.Unit
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive && int(u.Owner) == 1 {
			builder = u
			break
		}
	}
	if builder == nil {
		t.Fatalf("computer commander missing")
	}
	bindAIQueue(s.AI[1], s)
	wantX, wantZ := strictCellToWorld(30), strictCellToWorld(30)
	req := ai.BuildRequest{
		Builder: builder.Handle,
		UnitKey: "armsolar",
		X:       wantX,
		Z:       wantZ,
		Count:   1,
		Kind:    ai.BuildKindMobileSite,
	}
	if err := s.AI[1].QueueBuildTyped(req); err != nil {
		t.Fatalf("typed mobile request rejected: %v", err)
	}
	q := orders.QueueForUnit(builder)
	if q == nil || q.LenPrimary() == 0 {
		t.Fatalf("no order queued on builder")
	}
	head := q.Head()
	if head.GoalX != wantX || head.GoalZ != wantZ {
		t.Fatalf("site coordinates lost in production binding: got %d,%d want %d,%d", head.GoalX.Raw(), head.GoalZ.Raw(), wantX.Raw(), wantZ.Raw())
	}
}
