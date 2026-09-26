//go:build retail

package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// modernBuddies is a Survival setup whose every computer buddy is a Modern
// AI player (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
// "Per-player selection").
func modernBuddies(t *testing.T, mapName string, buddies int) session.SkirmishConfig {
	t.Helper()
	cfg := session.SurvivalSkirmishConfig(mapName, buddies, session.SurvivalOptions{})
	if err := cfg.ApplyComputerAI([]session.ComputerAI{{Row: session.ComputerAIEveryRow, Controller: ai.ControllerModern}}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A Modern Survival battle gives its Modern computer buddies the survival
// brain, which hears the wave warnings, and a buddy thinking on another
// core plays the same battle as one thinking on the simulation thread
// (docs/DESIGN_SURVIVAL.md "Computer survivors under the Modern AI").
func TestSurvivalBuddiesPlayTheSurvivalBrainRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	const mapName = "Painted Desert"
	if _, ok := cat.Maps[content.CanonicalKey(mapName)]; !ok {
		t.Skipf("retail map %q is absent", mapName)
	}
	const ticks = 7200 // four minutes: warnings, waves and the first towers
	play := func(async bool) (string, int) {
		t.Helper()
		battle, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
			Kind: headless.ScenarioSurvival, Gameplay: gameplay.Modern, Map: mapName, LocalOwner: -1,
			Skirmish:       modernBuddies(t, mapName, 2),
			Difficulty:     2,
			SimulationSeed: 5, CRTSeed: 5, FS: fs, Catalog: cat,
		})
		if err != nil {
			t.Fatal(err)
		}
		s := battle.Session
		defer closeControllers(s)
		for p := uint8(1); p <= 2; p++ {
			m := s.AI[p]
			if m == nil || !m.Survival.ComputerSurvivor(p) {
				t.Fatalf("buddy %d was not told the scenario", p)
			}
			if async {
				persona := personaFor(m)
				persona.Async = true
				m.Ext = aikit.NewHost(m, survivalBrain(m.Survival, p, nil), persona)
			}
		}
		// A composed battle starts loading; its first step enters it.
		for s.Clock.GlobalTick < ticks && s.State != session.StatePostBattle {
			s.Step(s.Clock.ScaledAnchor + 1)
		}
		warned := len(s.AI[1].Survival.Warnings(s.Clock.GlobalTick, nil))
		for p := uint8(1); p <= 2; p++ {
			h, ok := s.AI[p].Ext.(*aikit.Host)
			if !ok || h == nil {
				t.Fatalf("buddy %d has no controller", p)
			}
			h.Join()
			if got := h.Brain().Name(); got != "survival" || h.Thinks == 0 {
				t.Fatalf("buddy %d plays %s with %d thinks", p, got, h.Thinks)
			}
		}
		fp, err := s.PartialStateFingerprint()
		if err != nil {
			t.Fatal(err)
		}
		return fp, warned
	}
	sync, warned := play(false)
	if warned == 0 {
		t.Fatal("no warning reached the survivors in three minutes")
	}
	if async, _ := play(true); async != sync {
		t.Fatalf("the asynchronous buddies played another battle:\n sync %s\nasync %s", sync, async)
	}
}

// allyRepairBrain has its commander repair the first damaged allied
// commander its observation lists, once.
type allyRepairBrain struct {
	actor, target pool.Handle
	seen          bool // an allied unit was listed
}

func (b *allyRepairBrain) Name() string    { return "ally-repair" }
func (b *allyRepairBrain) Init(*aikit.Kit) {}
func (b *allyRepairBrain) Think(k *aikit.Kit, o *aikit.Obs) {
	b.seen = b.seen || len(o.Allies) > 0
	if b.target != 0 {
		return
	}
	for i := range o.Own {
		if o.Own[i].Info.Role.Has(aikit.RoleCommander) {
			b.actor = o.Own[i].H
		}
	}
	for i := range o.Allies {
		if a := &o.Allies[i]; a.Info.Role.Has(aikit.RoleCommander) && a.HP < a.MaxHP && b.actor != 0 {
			b.target = a.H
			k.Repair([]pool.Handle{b.actor}, a.H, false)
			return
		}
	}
}

// A Survival buddy sees the human's commander in its observation (the team
// shares sight) and the ordinary repair order takes it as a target: the
// resolver admits a target on nano-reach alone, with no ownership test
// [04 R-ORD-02 §1], so the buddy's commander repairs its ally's hit points
// back (docs/MODERN_AI_RESEARCH.md §3, docs/DESIGN_SURVIVAL.md §16).
func TestSurvivalBuddyRepairsAnAllyRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	const mapName = "Painted Desert"
	if _, ok := cat.Maps[content.CanonicalKey(mapName)]; !ok {
		t.Skipf("retail map %q is absent", mapName)
	}
	battle, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioSurvival, Gameplay: gameplay.Modern, Map: mapName, LocalOwner: -1,
		Skirmish:       modernBuddies(t, mapName, 1),
		Difficulty:     2,
		SimulationSeed: 5, CRTSeed: 5, FS: fs, Catalog: cat,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := battle.Session
	defer closeControllers(s)
	b := &allyRepairBrain{}
	s.AI[1].Ext = aikit.NewHost(s.AI[1], b, aikit.PersonaHard)
	var human pool.Handle
	for s.Clock.GlobalTick < 60 && s.State != session.StatePostBattle {
		s.Step(s.Clock.ScaledAnchor + 1)
	}
	for _, u := range s.Units.AppendLiveSliced(nil) {
		if u.Owner == 0 && u.Def != nil && u.Def.Commander {
			human = u.Handle
		}
	}
	hc := s.Units.Unit(human)
	if hc == nil {
		t.Fatal("no human commander")
	}
	hc.Health = hc.MaxHealth / 2
	hurt := hc.Health
	sawOrder := false
	for s.Clock.GlobalTick < 1500 && s.State != session.StatePostBattle {
		s.Step(s.Clock.ScaledAnchor + 1)
		if bc := s.Units.Unit(b.actor); bc != nil && !sawOrder {
			if q := orders.QueueOfUnit(bc); q != nil && q.PrimaryLen() > 0 {
				if n := q.PrimaryAt(0); n != nil && n.Target == human && orders.Table()[n.ID].Name == "RepairUnit" {
					sawOrder = true
				}
			}
		}
	}
	if h, ok := s.AI[1].Ext.(*aikit.Host); ok {
		h.Join()
	}
	if !b.seen || b.target != human {
		t.Fatalf("the buddy's observation listed allies %v and chose %d, want the human's commander %d", b.seen, b.target, human)
	}
	if !sawOrder {
		t.Fatal("the buddy's commander never carried a repair order on the human's commander")
	}
	if hc = s.Units.Unit(human); hc == nil || hc.Health <= hurt {
		t.Fatalf("the human's commander was not repaired: %v", hc)
	}
}
