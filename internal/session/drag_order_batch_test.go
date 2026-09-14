package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// These tests lock the explicit area-command policy of
// DESIGN_INTERFACE_HUD_INPUT §3.11; admission still follows [04 R-ORD-02 §1].
func newDragOrderBatchSession(t *testing.T) (*Session, *content.UnitDef) {
	t.Helper()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", BMCode: 1, CanMove: true, CanReclamate: true, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": def}}
	return &Session{Units: newSessionFixtureWorld(16, cat), Catalog: cat, LocalOwner: 0}, def
}

func applyDragOrderBatch(t *testing.T, s *Session, c HumanOrderCommand) {
	t.Helper()
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: c}); err != nil {
		t.Fatal(err)
	}
	s.applyHumanCommands(1)
}

func TestDragOrderBatchReplacesAtFirstAdmittedTargetPerActor(t *testing.T) {
	s, def := newDragOrderBatchSession(t)
	a, _ := s.Units.Create(def, 0, 0, 0, 0)
	b, _ := s.Units.Create(def, 0, 0, 0, 0)
	full, _ := s.Units.Create(def, 0, 30<<16, 0, 0)
	damaged, _ := s.Units.Create(def, 0, 40<<16, 3<<16, 5<<16)
	unfinished, _ := s.Units.Create(def, 0, 80<<16, 6<<16, 9<<16)
	s.Units.Unit(damaged).Health = 50
	s.Units.Unit(unfinished).Health = 10
	s.Units.Unit(unfinished).Remaining = 1
	handles := []pool.Handle{a, b}
	applyDragOrderBatch(t, s, HumanOrderCommand{Handles: handles, Code: 2, Position: orders.ResolvePos{X: 200 << 16}})
	applyDragOrderBatch(t, s, HumanOrderCommand{Handles: handles, Code: 8, Targets: []HumanOrderTarget{
		{Target: full}, // A live but full-health target fails nano-reach.
		{Target: damaged, Position: orders.ResolvePos{X: 999 << 16}},
		{Target: unfinished},
	}})
	for _, h := range handles {
		got := orders.QueueForUnit(s.Units.Unit(h)).Primary()
		if len(got) != 2 || got[0].ID != orders.Lookup("RepairUnit") || got[1].ID != orders.Lookup("HelpBuild") {
			t.Fatalf("actor %d queue = %+v, want repair then assistance", h, got)
		}
		for i, target := range []pool.Handle{damaged, unfinished} {
			u := s.Units.Unit(target)
			if got[i].Target != target || got[i].GoalX != u.X || got[i].GoalY != u.Y || got[i].GoalZ != u.Z || got[i].Owner != h {
				t.Fatalf("actor %d target %d lost live position or owner: %+v", h, target, got[i])
			}
			if got[i].CaptionPending != (i == 0) {
				t.Fatalf("actor %d target %d caption = %v; only the replacement should speak", h, target, got[i].CaptionPending)
			}
		}
	}
}

func TestDragOrderBatchAppendsNearbyFeatureGoalsWithoutToggle(t *testing.T) {
	s, def := newDragOrderBatchSession(t)
	h, _ := s.Units.Create(def, 0, 0, 0, 0)
	goals := []HumanOrderTarget{
		{Position: orders.ResolvePos{X: 32 << 16, Z: 32 << 16, HasFeature: true}},
		{Position: orders.ResolvePos{X: 48 << 16, Z: 32 << 16, HasFeature: true}},
	}
	c := HumanOrderCommand{Handles: []pool.Handle{h}, Code: 12, Targets: goals, Queued: true}
	applyDragOrderBatch(t, s, c)
	applyDragOrderBatch(t, s, c)
	q := orders.QueueForUnit(s.Units.Unit(h))
	got := q.Primary()
	if len(got) != 4 {
		t.Fatalf("repeated area queue = %+v, want both nearby goals twice", got)
	}
	for i, n := range got {
		if n.ID != orders.Lookup("Reclaim") || n.GoalX != goals[i%2].Position.X || n.CaptionPending {
			t.Fatalf("queued feature %d = %+v", i, n)
		}
	}
	// Ordinary queued clicks retain the inclusive one-cell toggle, removing
	// only the first nearby match [07 R-P0-11 §6]. Empty Targets selects it.
	c.Targets = nil
	c.Position = goals[1].Position
	applyDragOrderBatch(t, s, c)
	if got := q.Primary(); len(got) != 3 || got[0].GoalX != goals[1].Position.X {
		t.Fatalf("ordinary repeat click = %+v, want first nearby goal removed", got)
	}
	c.Queued = false
	applyDragOrderBatch(t, s, c)
	if got := q.Primary(); len(got) != 1 || got[0].GoalX != goals[1].Position.X || !got[0].CaptionPending {
		t.Fatalf("ordinary replacement = %+v", got)
	}
}

func TestDragOrderBatchCopiesTargetsAndKeepsEachInterfaceType(t *testing.T) {
	s, def := newDragOrderBatchSession(t)
	h, _ := s.Units.Create(def, 0, 0, 0, 0)
	target, _ := s.Units.Create(def, 0, 40<<16, 0, 0)
	s.Units.Unit(target).Health = 50
	s.Units.Unit(target).Flags |= units.ClassifierEligibleStatus
	goals := []HumanOrderTarget{
		{Target: target, Position: orders.ResolvePos{InterfaceType: orders.InterfaceTypeLeftClick}},
		{Target: target, Position: orders.ResolvePos{InterfaceType: orders.InterfaceTypeRightClick}},
	}
	want := goals[1]
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{h}, Code: 1, Targets: goals}}); err != nil {
		t.Fatal(err)
	}
	goals[1] = HumanOrderTarget{}
	pending := s.PendingHumanCommands()
	if pending[0].Order.Targets[1] != want {
		t.Fatalf("enqueue retained caller's target slice: %+v", pending)
	}
	pending[0].Order.Targets[1] = HumanOrderTarget{}
	if got := s.PendingHumanCommands()[0].Order.Targets[1]; got != want {
		t.Fatalf("pending inspection exposed target slice: %+v", got)
	}
	s.applyHumanCommands(1)
	got := orders.QueueForUnit(s.Units.Unit(h)).Primary()
	if len(got) != 1 || got[0].Target != target || got[0].ID != orders.Lookup("RepairUnit") || !got[0].CaptionPending {
		t.Fatalf("per-target interface resolution = %+v, want only right-click repair", got)
	}
}

func TestDragOrderBatchNoAdmissionPreservesExistingQueue(t *testing.T) {
	s, def := newDragOrderBatchSession(t)
	h, _ := s.Units.Create(def, 0, 0, 0, 0)
	full, _ := s.Units.Create(def, 0, 40<<16, 0, 0)
	dead, _ := s.Units.Create(def, 0, 80<<16, 0, 0)
	s.Units.Unit(dead).Alive = false
	applyDragOrderBatch(t, s, HumanOrderCommand{Handles: []pool.Handle{h}, Code: 2, Position: orders.ResolvePos{X: 200 << 16}})
	q := orders.QueueForUnit(s.Units.Unit(h))
	prior := q.Primary()[0]
	for _, c := range []HumanOrderCommand{
		{Code: 8, Targets: []HumanOrderTarget{{Target: full}}},
		{Code: 2, Targets: []HumanOrderTarget{{Target: dead}, {Target: 999}}},
		{Code: 4, Targets: []HumanOrderTarget{{Position: orders.ResolvePos{X: 40 << 16}}}}, // no D-gun capability
	} {
		c.Handles = []pool.Handle{h}
		applyDragOrderBatch(t, s, c)
		if got := q.Primary(); len(got) != 1 || got[0] != prior {
			t.Fatalf("rejected code %d changed existing queue: %+v", c.Code, got)
		}
	}
}
