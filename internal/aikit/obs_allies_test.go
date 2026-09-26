package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// obsBrain keeps a copy of the lists of its latest observation.
type obsBrain struct {
	countBrain
	own    []OwnUnit
	enemy  []Contact
	allies []AllyUnit
}

func (b *obsBrain) Think(_ *Kit, o *Obs) {
	b.thinks++
	b.own = append(b.own[:0], o.Own...)
	b.enemy = append(b.enemy[:0], o.Enemy...)
	b.allies = append(b.allies[:0], o.Allies...)
}

// The observation lists an allied player's units that the owner's sight
// predicate passes, with their type and state, and leaves them out of the
// own and enemy lists; an allied unit out of sight is not listed, and no
// enemy or own unit is ever an ally (docs/MODERN_AI_RESEARCH.md §3).
func TestObsListsAlliedUnitsInSight(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tank"}, UnitName: "tank", CanMove: true, BMCode: 1, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"tank": def}}
	w := fixtureWorld(cat)
	econ := computerEconomy()
	econ.Players[1].Exists = true
	econ.Players[2].Exists = true
	at := func(owner uint8, x int64) pool.Handle {
		h, err := w.Create(def, owner, numeric.FixedFromInt(x), 0, numeric.FixedFromInt(100))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	own := at(0, 100)
	seen := at(1, 200)
	hidden := at(1, 900)
	enemy := at(2, 300)
	if u := w.Unit(seen); u != nil {
		u.Health = 40
	}
	m := &ai.Manager{
		Player: 0, Catalog: cat,
		IsAlliance:  func(a, b uint8) bool { return a == b || a+b == 1 },
		UnitVisible: func(_ uint8, u *units.Unit) bool { return u.Handle != hidden },
	}
	b := &obsBrain{}
	h := NewHost(m, b, PersonaMax)
	defer h.Close()
	for tick := uint32(1); b.thinks == 0; tick++ {
		h.Step(tick, w, econ)
		h.Join()
	}
	if len(b.own) != 1 || b.own[0].H != own {
		t.Fatalf("own %+v: want the one own tank", b.own)
	}
	if len(b.enemy) != 1 || b.enemy[0].H != enemy {
		t.Fatalf("enemy %+v: want the enemy tank alone", b.enemy)
	}
	if len(b.allies) != 1 {
		t.Fatalf("allies %+v: want the allied tank in sight alone", b.allies)
	}
	a := b.allies[0]
	if a.H != seen || a.Owner != 1 || a.Info == nil || a.Info.Key != "tank" || a.X != 200 || a.Z != 100 ||
		a.HP != 40 || a.MaxHP != w.Unit(seen).MaxHealth || !a.Built || a.Progress != 100 || a.Gen == 0 {
		t.Fatalf("ally %+v: wrong record", a)
	}
}
