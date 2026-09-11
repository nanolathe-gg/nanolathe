package ai

import (
	"errors"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// Established: a resource member must be a completed building, and an empty
// primary queue admits one selection even when no queue has been allocated.
// Save restoration can enroll a mobile builder in this record without running
// the classifier [08 R-AI-01 §2][08 R-SAVE-02 §11-A].
func TestRestoredResourceMemberStartsFactoryProduction(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		name := "building"
		if mobile {
			name = "mobile"
		}
		t.Run(name, func(t *testing.T) {
			builder := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", Builder: true, MaxDamage: 100}
			if mobile {
				builder.BMCode = 1
			}
			product := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "product"}, UnitName: "product", BMCode: 1, CanFly: true, MaxDamage: 100}
			cat := &content.Catalog{
				Units:      map[string]*content.UnitDef{"builder": builder, "product": product},
				BuildMenus: map[string]*content.BuildMenuPage{"builder": {Buttons: []string{"product"}}},
			}
			w := newAIFixtureWorld(2, cat)
			h, err := w.Create(builder, 0, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			u := w.Unit(h)
			u.Group, u.RestoredAIGroup = 1, 1
			r := rng.NewSimulation(7)
			m := &Manager{Player: 0, Catalog: cat, Profile: &Profile{}, RNG: &r}
			m.EnsureStrategicInitialized()
			m.Strategic.ClassVectors["product"] = ClassVector{C0: 100, C1: 100, C2: 100}
			m.RestoreGroupsFromUnits(w.IterSliced())
			e := testEcon(0, 800, 1000, 400, 500, 300, 10, 0, 0)
			e.Players[0].Exists = true
			attempts := 0
			reject := true
			m.QueueBuildTyped = func(req BuildRequest) error {
				attempts++
				if req.Builder != h || req.UnitKey != "product" || req.Count != 1 || req.Kind != BuildKindFactoryQueue {
					t.Fatalf("factory request = %+v", req)
				}
				if reject {
					return errors.New("fixture build admission refused")
				}
				return construction.QueueFactoryBuild(u, req.UnitKey, req.Count, cat)
			}
			if orders.QueueOfUnit(u) != nil {
				t.Fatal("fixture should begin without an allocated queue")
			}
			m.doResource(30, w, e)
			if mobile {
				if attempts != 0 || r.Draws() != 0 || orders.QueueOfUnit(u) != nil {
					t.Fatalf("mobile resource member acted: attempts=%d draws=%d", attempts, r.Draws())
				}
				return
			}
			if attempts != 1 || orders.QueueOfUnit(u) != nil {
				t.Fatalf("first admission attempts=%d queue=%v, want one rejected attempt with no queue", attempts, orders.QueueOfUnit(u))
			}
			reject = false
			m.doResource(60, w, e)
			q := orders.QueueOfUnit(u)
			if attempts != 2 || q == nil || q.LenPrimary() != 1 || q.Head().BuildDefKey != "product" {
				t.Fatalf("retry did not queue the first product: attempts=%d queue=%v", attempts, q)
			}
			draws := r.Draws()
			m.doResource(90, w, e)
			if attempts != 2 || r.Draws() != draws || q.Head().Param2 != 1 {
				t.Fatal("busy factory selected or queued another product")
			}
			if err := construction.CancelTailMost(u, "product"); err != nil {
				t.Fatal(err)
			}
			m.doResource(120, w, e)
			if attempts != 3 || q.LenPrimary() != 1 || q.Head().Param2 != 1 {
				t.Fatal("cancelled factory did not resume one-product production")
			}
		})
	}
}
