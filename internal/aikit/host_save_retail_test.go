package aikit_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/tactics"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utility"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// hostSaveRuleSet is a test harness: util+tac behind every computer player
// with full computer income, the Modern AI's play for a battle whose every
// computer player is Modern, which internal packages may not link from
// mods/aikit (internal/architecture).
const hostSaveRuleSet = "aikit-host-save-test"

func init() {
	session.RegisterRuleSet(hostSaveRuleSet, func() session.RuleSet {
		return session.RuleSet{Base: gameplay.Modern, Planner: utilTacTestPlanner{}, ComputerIncome: session.FullComputerIncome{}}
	})
}

// utilTacTestPlanner is zero size, as a cached planner must be; each
// manager's host lives in its Ext.
type utilTacTestPlanner struct{}

func (utilTacTestPlanner) Step(m *ai.Manager, tick uint32, w *units.World, econ *economy.Service) {
	aikit.StepLazy(m, tick, w, econ, func(m *ai.Manager) *aikit.Host {
		st, ec, pr := utility.Policies(utility.DefaultParams())
		return aikit.NewHost(m, core.New("util+tac", st, ec, tactics.New(tactics.DefaultParams()), pr), aikit.PersonaMed)
	})
}

// styleOf is the style a host's utility strategy drew, after its
// preparation has been joined.
func styleOf(t *testing.T, m *ai.Manager) int64 {
	t.Helper()
	h, ok := m.Ext.(*aikit.Host)
	if !ok || h == nil {
		t.Fatalf("player %d has no controller", m.Player)
	}
	h.Join()
	brain, ok := h.Brain().(*core.Brain)
	if !ok {
		t.Fatalf("player %d runs %T", m.Player, h.Brain())
	}
	reporter, ok := brain.Strategy.(aikit.Reporter)
	if !ok {
		t.Fatalf("player %d's strategy %T reports nothing", m.Player, brain.Strategy)
	}
	style := int64(-1)
	reporter.Report(func(name string, v int64) {
		if name == "style" {
			style = v
		}
	})
	return style
}

// closeHosts waits for every controller's work in flight, as battle exit
// does.
func closeHosts(s *session.Session) {
	for _, m := range s.AI {
		if m == nil {
			continue
		}
		if h, ok := m.Ext.(*aikit.Host); ok && h != nil {
			h.Close()
		}
	}
}

func stepTicks(s *session.Session, n int) {
	for i := 0; i < n && s.State == session.StateBattle; i++ {
		s.Step(s.Clock.ScaledAnchor + 1)
	}
}

// A Modern AI game saved and loaded keeps every computer player's
// personality (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player"):
// the sidecar's record carries the battle seed and the generator positions,
// the restored controllers draw the style the saved game drew although the
// load has another entry seed, and the restored game plays on. A load
// without the record seeds from the load's own entry seed, as a retail
// save does.
func TestModernAIPersonalitySurvivesALoadRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	const mapName = "The Pass"
	if _, ok := cat.Maps[content.CanonicalKey(mapName)]; !ok {
		t.Skipf("retail map %q is absent", mapName)
	}
	cfg := session.DirectSkirmishConfig(mapName)
	cfg.NumPlayers = 4
	for i := 2; i < 4; i++ {
		cfg.Players[i] = cfg.Players[1]
		cfg.Players[i].Side, cfg.Players[i].Color, cfg.Players[i].AllyGroup = i&1, i, 5+i
	}
	cfg.Gameplay = gameplay.Mode(hostSaveRuleSet)
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 31, 31
	cfg.ApplyDefaults()
	src, err := session.NewSkirmishWithProgress(fs, cat, cfg, nil)
	if err != nil {
		t.Fatalf("compose %q: %v", mapName, err)
	}
	if err := src.SetRules(hostSaveRuleSet); err != nil {
		t.Fatal(err)
	}
	stepTicks(src, 600)
	computers := []uint8{1, 2, 3}
	styles := map[uint8]int64{}
	for _, p := range computers {
		if src.AI[p] == nil {
			t.Fatalf("player %d is not a computer player", p)
		}
		styles[p] = styleOf(t, src.AI[p])
	}

	rec := session.RecordAIControllers(src)
	if rec == nil || rec.Seed != 31 || len(rec.Generators) != len(computers) {
		t.Fatalf("record %+v: want the entry seed and one generator per computer player", rec)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var carried session.AIControllers
	if err := json.Unmarshal(data, &carried); err != nil {
		t.Fatal(err)
	}
	in, err := src.RetailBattleSaveInputs(session.RetailBattleSummary(src, "modern ai", "0", src.Skirmish.UnitLimit), save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "MODERNAI.SAV")
	if err := src.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write save: %v", err)
	}
	closeHosts(src)

	deps := session.RetailLoadDeps{FS: fs, Catalog: cat, SimSeed: 977, CRTSeed: 977, UnitLimit: src.Skirmish.UnitLimit, Gameplay: gameplay.Mode(hostSaveRuleSet)}
	plain, err := session.LoadRetailSavePath(path, deps)
	if err != nil || plain.Battle == nil || plain.Battle.Session == nil {
		t.Fatalf("load without the record: %v", err)
	}
	for _, p := range computers {
		if m := plain.Battle.Session.AI[p]; m == nil || m.BattleSeed != 977 || m.ResumeGenerator != nil {
			t.Fatalf("player %d: a load without the record did not seed from its own entry seed", p)
		}
	}
	closeHosts(plain.Battle.Session)

	deps.AIControllers = &carried
	result, err := session.LoadRetailSavePath(path, deps)
	if err != nil || result.Battle == nil || result.Battle.Session == nil {
		t.Fatalf("load: %v", err)
	}
	dst := result.Battle.Session
	defer closeHosts(dst)
	// The load's battle-entry prime may already have built a controller; a
	// fresh brain redraws nothing in its first thinks, so its generator
	// stands exactly where the save left it. Otherwise the position still
	// waits on the manager.
	for _, g := range carried.Generators {
		m := dst.AI[g.Player]
		if m == nil || m.BattleSeed != 31 {
			t.Fatalf("player %d: the restored manager does not carry the saved battle seed", g.Player)
		}
		h, _ := m.Ext.(*aikit.Host)
		var position uint64
		begun := false
		if h != nil {
			position, begun = h.Generator()
		}
		if !begun {
			if m.ResumeGenerator == nil {
				t.Fatalf("player %d: the restored manager lost its generator position", g.Player)
			}
			position = *m.ResumeGenerator
		}
		if position != g.Position {
			t.Fatalf("player %d: generator at %#x after the load, saved at %#x", g.Player, position, g.Position)
		}
	}
	stepTicks(dst, 900)
	if dst.State != session.StateBattle {
		t.Fatalf("the restored battle ended (state %v) before the check", dst.State)
	}
	for _, p := range computers {
		if dst.AI[p].ResumeGenerator != nil {
			t.Fatalf("player %d's controller did not take its saved position", p)
		}
		if got := styleOf(t, dst.AI[p]); got != styles[p] {
			t.Errorf("player %d drew style %d after the load, %d before", p, got, styles[p])
		}
	}
}
