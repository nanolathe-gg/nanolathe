package ai

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// stockpileTaskFixture is one completed armed building in the armed-building
// record whose single build option is a stockpile pseudo-product, with every
// other task deadline pushed past the tested ticks.
func stockpileTaskFixture(t *testing.T, on bool) (*Manager, *units.World, *units.Unit, *economy.Service, *[]BuildRequest) {
	t.Helper()
	silo := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "silo"}, UnitName: "SILO", Builder: true, MaxDamage: 100}
	round := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "makenukesilo"}, UnitName: "MAKENUKESILO", BMCode: 1, MaxDamage: 100}
	cat := &content.Catalog{
		Units:      map[string]*content.UnitDef{"silo": silo, "makenukesilo": round},
		BuildMenus: map[string]*content.BuildMenuPage{"silo": {Buttons: []string{"makenukesilo"}}},
	}
	w := newAIFixtureWorld(2, cat)
	h, err := w.Create(silo, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.Remaining = 0
	u.Group, u.RestoredAIGroup = 5, 5
	r := rng.NewSimulation(3)
	m := &Manager{Player: 0, Catalog: cat, Profile: &Profile{}, RNG: &r, OrderBinding: aiFixtureOrderBinding(cat, &r)}
	m.Community = community.Features{AIStockpileProducts: on}
	m.EnsureStrategicInitialized()
	m.Strategic.ClassVectors["makenukesilo"] = ClassVector{C0: 100, C1: 100, C2: 100}
	m.RestoreGroupsFromUnits(w.IterSliced())
	for k := range m.Deadlines {
		if TaskKind(k) != TaskNull {
			m.Deadlines[k] = 1 << 30
		}
	}
	var requests []BuildRequest
	m.QueueBuildTyped = func(req BuildRequest) error {
		requests = append(requests, req)
		return nil
	}
	e := testEcon(0, 800, 1000, 400, 500, 300, 10, 0, 0)
	e.Players[0].Exists = true
	return m, w, u, e, &requests
}

// TestProTAStockpileTaskRunsOnTheArmedBuildingRecord locks the shipped 4.8
// stockpile purchasing (research/extensions/prota-engine.md): the
// armed-building record takes the resource/queue body at +30, its product
// branch submits one CANBUILD product, and a secondary order suppresses the
// visit. With the switch off the null task stays inert, as retail.
func TestProTAStockpileTaskRunsOnTheArmedBuildingRecord(t *testing.T) {
	m, w, _, e, requests := stockpileTaskFixture(t, false)
	m.runDueTasks(0, w, e)
	if len(*requests) != 0 || m.Deadlines[TaskNull] != 0 {
		t.Fatalf("retail null task acted: requests=%v deadline=%d", *requests, m.Deadlines[TaskNull])
	}

	m, w, u, e, requests := stockpileTaskFixture(t, true)
	m.runDueTasks(0, w, e)
	if m.Deadlines[TaskNull] != 30 {
		t.Fatalf("null task deadline %d, want tick+30", m.Deadlines[TaskNull])
	}
	if len(*requests) != 1 {
		t.Fatalf("requests %v, want one", *requests)
	}
	if got := (*requests)[0]; got.Builder != u.Handle || got.UnitKey != "makenukesilo" || got.Count != 1 || got.Kind != BuildKindFactoryQueue || got.Tick != 0 {
		t.Fatalf("request %+v, want one factory-queue round of makenukesilo", got)
	}
	m.runDueTasks(29, w, e)
	if len(*requests) != 1 {
		t.Fatal("null task ran before its +30 deadline")
	}

	// Any secondary order suppresses the whole visit; only its presence is read.
	q := orders.BindQueueBinding(u, m.OrderBinding)
	id := orders.Lookup("BuildWeapon")
	n := orders.NewNodeForOrder(id, 0, 0, 0, 0, 30, u.Handle, false)
	n.Param1, n.Param2 = 0, 1
	q.CoalesceTail(id, n)
	if q.LenSecondary() == 0 {
		t.Fatal("fixture secondary order not queued")
	}
	m.runDueTasks(30, w, e)
	if len(*requests) != 1 || m.Deadlines[TaskNull] != 60 {
		t.Fatalf("secondary order did not suppress the visit: requests=%d deadline=%d", len(*requests), m.Deadlines[TaskNull])
	}
}

// TestProTAApplianceSelectorBoundary locks the energyuse sign-byte selector:
// the signed top byte of the binary32 encoding must be at least 66, which for
// finite nonnegative values is energyuse >= 32. Negative zero and negative
// values fail; positive infinity passes. makesmetal no longer selects.
func TestProTAApplianceSelectorBoundary(t *testing.T) {
	m := &Manager{Community: community.Features{AIApplianceEnergy: true}}
	for _, tc := range []struct {
		energyUse  float64
		makesMetal int32
		want       bool
	}{
		{32, 0, true},
		{float64(math.Nextafter32(32, 0)), 0, false},
		{31, 1, false},
		{1000, 0, true},
		{math.Inf(1), 0, true},
		{math.Copysign(0, -1), 0, false},
		{-40, 0, false},
		{0, 1, false},
	} {
		def := &content.UnitDef{EnergyUse: tc.energyUse, MakesMetal: tc.makesMetal}
		if got := m.activationBranch(def); got != tc.want {
			t.Errorf("energyuse %v makesmetal %d: appliance %v, want %v", tc.energyUse, tc.makesMetal, got, tc.want)
		}
	}
	retail := &Manager{}
	if !retail.activationBranch(&content.UnitDef{MakesMetal: 1}) || retail.activationBranch(&content.UnitDef{EnergyUse: 500}) {
		t.Fatal("zero switch changed retail's makes-metal selector")
	}
}

// TestProTAApplianceKeepsRetailDecisionOrder checks that an admitted appliance
// keeps the retail toggle: disable at energy <= 2 x metal with no draw, one
// bound-five draw on a positive net surplus.
func TestProTAApplianceKeepsRetailDecisionOrder(t *testing.T) {
	appliance := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "radar"}, UnitName: "RADAR", EnergyUse: 32, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"radar": appliance}}
	w := newAIFixtureWorld(2, cat)
	h, err := w.Create(appliance, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.Remaining = 0
	u.SetActivated(true)
	r := rng.NewSimulation(5)
	m := &Manager{Player: 0, Catalog: cat, RNG: &r, GroupResource: []pool.Handle{h}, Community: community.Features{AIApplianceEnergy: true}}

	m.doResource(30, w, testEcon(0, 800, 1000, 400, 500, 300, 10, 0, 0)) // 800 <= 2 x 400
	if u.Activated || r.Draws() != 0 {
		t.Fatalf("disable arm: activated=%v draws=%d, want disabled with no draw", u.Activated, r.Draws())
	}
	m.doResource(60, w, testEcon(0, 900, 1000, 400, 500, 300, 10, 0, 0))
	if r.Draws() != 1 {
		t.Fatalf("surplus arm drew %d, want one bound-five draw", r.Draws())
	}
	m.doResource(90, w, testEcon(0, 900, 1000, 400, 500, 10, 10, 300, 0))
	if r.Draws() != 1 {
		t.Fatal("nonpositive net energy drew")
	}
}

// TestProTABuilderThresholdOverlap locks the asymmetric construction cutoff:
// a capture-capable builder places below ten under the switch (below five in
// retail), while the reposition pass keeps admitting at five or more, so
// counts five through nine are eligible for both passes.
func TestProTABuilderThresholdOverlap(t *testing.T) {
	for _, tc := range []struct {
		on       bool
		count    int32
		place    bool
		position bool
	}{
		{false, 4, true, false},
		{false, 5, false, true},
		{true, 4, true, false},
		{true, 5, true, true},
		{true, 9, true, true},
		{true, 10, false, true},
	} {
		m, w, builder, econ, _, submissions := constructionOrderGateFixture(t)
		builder.Def.CanCapture = true
		m.Community.AIBuilderStopThreshold = tc.on
		m.Strategic.BuildCapable = tc.count
		m.constructionPlacePass(90, w, econ, m.Strategic.CenterX, m.Strategic.CenterZ, m.Strategic.BuildCapable)
		if placed := *submissions == 1; placed != tc.place {
			t.Errorf("switch %v count %d: placement submitted=%v, want %v", tc.on, tc.count, placed, tc.place)
		}

		m, w, builder, _, _, _ = constructionOrderGateFixture(t)
		builder.Def.CanCapture = true
		m.Community.AIBuilderStopThreshold = tc.on
		m.constructionRepositionPass(90, w, m.Strategic.CenterX, m.Strategic.CenterY, m.Strategic.CenterZ, tc.count)
		if moved := orders.QueueOfUnit(builder).LenPrimary() > 0; moved != tc.position {
			t.Errorf("switch %v count %d: reposition issued=%v, want %v", tc.on, tc.count, moved, tc.position)
		}
	}
}
