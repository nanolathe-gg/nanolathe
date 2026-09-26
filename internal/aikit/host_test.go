package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// countBrain counts its calls and emits nothing.
type countBrain struct{ inits, thinks int }

func (b *countBrain) Name() string     { return "count" }
func (b *countBrain) Init(*Kit)        { b.inits++ }
func (b *countBrain) Think(*Kit, *Obs) { b.thinks++ }
func computerEconomy() *economy.Service {
	e := &economy.Service{}
	e.Players[0].Exists, e.Players[0].ControllerState = true, 2
	return e
}

// The Survival attacker is a Passive computer slot: the wave director orders
// its units and its manager keeps only the engine upkeep
// (docs/DESIGN_SURVIVAL.md §4.1), so a host bound to it never initializes or
// thinks. The same host starts once the slot decides again.
func TestHostLeavesPassiveManagerAlone(t *testing.T) {
	w := units.NewSliced(4, nil)
	econ := computerEconomy()
	m := &ai.Manager{Player: 0, Passive: true}
	b := &countBrain{}
	h := NewHost(m, b, PersonaHard)
	defer h.Close()
	tick := uint32(1)
	for ; tick < 120; tick++ {
		h.Step(tick, w, econ)
	}
	if b.inits != 0 || b.thinks != 0 {
		t.Fatalf("passive slot: %d inits, %d thinks, want none", b.inits, b.thinks)
	}
	m.Passive = false
	for ; tick < 240; tick++ {
		h.Step(tick, w, econ)
	}
	h.Join()
	if b.inits != 1 || b.thinks == 0 {
		t.Fatalf("deciding slot: %d inits, %d thinks, want one init and some thinks", b.inits, b.thinks)
	}
}

// A blip carries no identity, so it neither creates nor refreshes a record:
// an identified unit that drops to radar is remembered where it was last
// seen, not tracked by type across the map.
func TestBlipRefreshesNoMemory(t *testing.T) {
	h := &Host{m: &ai.Manager{}}
	h.ob.ensure(8)
	info := &UnitInfo{Key: "com", Role: RoleMobile | RoleCommander}
	h.obs.Enemy = []Contact{{H: 5, Gen: 1, Info: info, Owner: 1, X: 100, Z: 100, Visible: true}}
	h.updateMemory(30)
	h.obs.Enemy = []Contact{{Owner: 1, X: 900, Z: 900, HPPct: 100}}
	h.updateMemory(60)
	if len(h.obs.Memory) != 1 {
		t.Fatalf("memory holds %d records, want the one sighting", len(h.obs.Memory))
	}
	if r := h.obs.Memory[0]; r.X != 100 || r.Z != 100 || r.LastSeen != 30 {
		t.Fatalf("record %+v: a blip moved or refreshed it", r)
	}
}

// genFixture is a host for player 0 over a world with one own tank and one
// enemy tank, both in sight.
type genFixture struct {
	w     *units.World
	econ  *economy.Service
	def   *content.UnitDef
	h     *Host
	own   pool.Handle
	enemy pool.Handle
}

func newGenFixture(t *testing.T, b Brain) *genFixture {
	t.Helper()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tank"}, UnitName: "tank", CanMove: true, BMCode: 1, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"tank": def}}
	f := &genFixture{w: fixtureWorld(cat), econ: computerEconomy(), def: def}
	f.econ.Players[1].Exists = true
	var err error
	if f.own, err = f.w.Create(def, 0, numeric.FixedFromInt(100), 0, numeric.FixedFromInt(100)); err != nil {
		t.Fatal(err)
	}
	if f.enemy, err = f.w.Create(def, 1, numeric.FixedFromInt(300), 0, numeric.FixedFromInt(100)); err != nil {
		t.Fatal(err)
	}
	m := &ai.Manager{Player: 0, Catalog: cat, UnitVisible: func(uint8, *units.Unit) bool { return true }}
	f.h = NewHost(m, b, PersonaMax) // thinks every 10 ticks, reacts in 3
	return f
}

// replace kills the unit in slot h and creates another in the same slot.
func (f *genFixture) replace(t *testing.T, h pool.Handle, owner uint8, tick uint32) {
	t.Helper()
	f.w.Destroy(h, units.DeathKilled)
	f.w.FinalizeDeath(h, tick)
	got, err := f.w.CreateWithForcedSlot(f.def, owner, numeric.FixedFromInt(200), 0, numeric.FixedFromInt(200), h)
	if err != nil || got != h {
		t.Fatalf("recreate in slot %d: %d, %v", h, got, err)
	}
}

// tagBrain tags every own unit on its first think and records what later
// observations report.
type tagBrain struct {
	countBrain
	tags, gens []int32
}

func (b *tagBrain) Think(k *Kit, o *Obs) {
	b.thinks++
	for i := range o.Own {
		u := &o.Own[i]
		if b.thinks == 1 {
			k.SetTag(u.H, 7)
		}
		b.tags = append(b.tags, u.Tag)
		b.gens = append(b.gens, int32(u.Gen))
	}
}

// The pool recycles slots without a generation: a new unit in a dead one's
// slot is a different instance (Gen) and starts untagged.
func TestRecycledSlotStartsUntagged(t *testing.T) {
	b := &tagBrain{}
	f := newGenFixture(t, b)
	tick := uint32(1)
	for ; b.thinks < 2; tick++ {
		f.h.Step(tick, f.w, f.econ)
		f.h.Join()
	}
	f.replace(t, f.own, 0, tick)
	for n := b.thinks; b.thinks == n; tick++ {
		f.h.Step(tick, f.w, f.econ)
		f.h.Join()
	}
	last := len(b.tags) - 1
	if b.tags[last-1] != 7 || b.tags[last] != 0 || b.gens[last] == b.gens[last-1] {
		t.Fatalf("tags %v gens %v: want the tag kept, then reset with a new generation", b.tags, b.gens)
	}
}

// attackBrain orders every own unit at the first enemy it sees, once.
type attackBrain struct{ countBrain }

func (b *attackBrain) Think(k *Kit, o *Obs) {
	b.thinks++
	if b.thinks == 1 && len(o.Enemy) > 0 && len(o.Own) > 0 {
		k.Attack([]pool.Handle{o.Own[0].H}, o.Enemy[0].H, false)
	}
}

// A unit order names the instance it was issued at: when the target dies
// in the reaction window and another unit takes its slot, the order is
// dropped as stale rather than landing on the newcomer.
func TestStaleTargetIsDropped(t *testing.T) {
	b := &attackBrain{}
	f := newGenFixture(t, b)
	tick := uint32(1)
	for ; b.thinks == 0; tick++ {
		f.h.Step(tick, f.w, f.econ)
		f.h.Join() // the brain's counters are read here
	}
	f.replace(t, f.enemy, 1, tick)
	for end := tick + PersonaMax.Reaction + 1; tick <= end; tick++ {
		f.h.Step(tick, f.w, f.econ)
	}
	st := f.h.Stats()
	if st.Applied != 0 || st.Stale != 1 || st.Reasons[FailTarget] != 1 {
		t.Fatalf("stats %+v: want the attack dropped as a stale target", st)
	}
}

// drawBrain draws once in Init, as a brain drawing its style does.
type drawBrain struct {
	countBrain
	first uint32
}

func (b *drawBrain) Init(k *Kit) {
	b.inits++
	b.first = k.Rand.Uint32()
}

// A controller rebuilt after a load draws its Init from the recorded battle
// seed, exactly as the saved game's did, then continues its generator from
// the recorded position; the resume is taken once, so a controller built
// after a later switch starts from the seed again. The position a save
// records is the generator's own.
func TestRestoredHostRedrawsInitAndResumesItsGenerator(t *testing.T) {
	w := units.NewSliced(4, nil)
	const seed, slot = 4242, 0
	resume := uint64(0x0123456789abcdef)
	m := &ai.Manager{Player: slot, BattleSeed: seed, ResumeGenerator: &resume}
	b := &drawBrain{}
	h := NewHost(m, b, PersonaHard)
	if _, ok := h.Generator(); ok {
		t.Fatal("a host that has not begun reported a generator")
	}
	h.Step(1, w, computerEconomy())
	if m.ResumeGenerator != nil {
		t.Fatal("the first controller left the resume position for the next")
	}
	position, ok := h.Generator()
	if !ok || position != resume {
		t.Fatalf("generator at %#x (%v) after the preparation, want the recorded %#x", position, ok, resume)
	}
	want := PlayerRand(seed, slot)
	if b.first != want.Uint32() {
		t.Fatal("Init did not draw from the battle seed")
	}
	h.Close()

	again := &drawBrain{}
	h2 := NewHost(m, again, PersonaHard)
	h2.Step(1, w, computerEconomy())
	fresh := PlayerRand(seed, slot)
	fresh.Uint32()
	if position, _ := h2.Generator(); position != fresh.Position() || again.first != b.first {
		t.Fatal("a controller built after the first resumed the saved position again")
	}
	h2.Close()
}
