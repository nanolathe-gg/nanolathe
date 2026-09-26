//go:build retail

package aikit

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utility"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// A Modern skirmish whose computer players are all Modern AI players,
// entered with configured parameters, gives each computer player its merged
// layers, each controller plays the style they name, and a save carries
// them: a load with the record rebuilds the same controllers whatever the
// loading host configures, and a load without it restores every computer
// player Classic with no parameters (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern
// AI computer player", "Configuration" and "Saves").
func TestConfiguredStylesReachTheControllersAndSurviveALoadRetail(t *testing.T) {
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
	cfg.Gameplay = gameplay.Modern
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 41, 41
	cfg.ApplyDefaults()
	cfg.Difficulty = 2
	if err := cfg.ApplyComputerAI([]session.ComputerAI{{Row: session.ComputerAIEveryRow, Controller: ai.ControllerModern}}); err != nil {
		t.Fatal(err)
	}
	var o session.AIOverrides
	o.All = "jitter=0,style=units"
	o.Difficulty[2] = "style=tower"
	o.Players[3] = "style=eco"
	src, err := session.NewSkirmishWithEntryOptions(fs, cat, cfg, session.SkirmishEntryOptions{AIOverrides: o})
	if err != nil {
		t.Fatalf("compose %q: %v", mapName, err)
	}
	want := map[uint8]struct{ params, style string }{
		1: {"jitter=0,style=tower", "tower"},
		2: {"jitter=0,style=tower", "tower"},
		3: {"jitter=0,style=eco", "eco"},
	}
	stepBattle(src, 600)
	for p, w := range want {
		if got := src.AI[p].ControllerParams; got != w.params {
			t.Fatalf("player %d was given %q, want %q", p, got, w.params)
		}
		if got := drawnStyle(t, src.AI[p]); got != w.style {
			t.Fatalf("player %d plays %s, want %s", p, got, w.style)
		}
	}

	rec := session.RecordAIControllers(src)
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var carried session.AIControllers
	if err := json.Unmarshal(data, &carried); err != nil {
		t.Fatal(err)
	}
	in, err := src.RetailBattleSaveInputs(session.RetailBattleSummary(src, "configured ai", "0", src.Skirmish.UnitLimit), save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "CONFIGAI.SAV")
	if err := src.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write save: %v", err)
	}
	closeControllers(src)

	deps := session.RetailLoadDeps{FS: fs, Catalog: cat, SimSeed: 977, CRTSeed: 977, UnitLimit: src.Skirmish.UnitLimit, Gameplay: gameplay.Modern}
	plain, err := session.LoadRetailSavePath(path, deps)
	if err != nil || plain.Battle == nil || plain.Battle.Session == nil {
		t.Fatalf("load without the record: %v", err)
	}
	for p := range want {
		if m := plain.Battle.Session.AI[p]; m.ControllerParams != "" || m.Controller != ai.ControllerClassic {
			t.Fatalf("player %d: a load without the record was given %q as %s", p, m.ControllerParams, m.Controller)
		}
	}
	closeControllers(plain.Battle.Session)

	deps.AIControllers = &carried
	loaded, err := session.LoadRetailSavePath(path, deps)
	if err != nil || loaded.Battle == nil || loaded.Battle.Session == nil {
		t.Fatalf("load: %v", err)
	}
	dst := loaded.Battle.Session
	defer closeControllers(dst)
	stepBattle(dst, 300)
	for p, w := range want {
		if got := dst.AI[p].ControllerParams; got != w.params {
			t.Fatalf("player %d restored %q, want %q", p, got, w.params)
		}
		if got := drawnStyle(t, dst.AI[p]); got != w.style {
			t.Fatalf("player %d plays %s after the load, want %s", p, got, w.style)
		}
	}
}

// drawnStyle is the style a controller's utility strategy drew.
func drawnStyle(t *testing.T, m *ai.Manager) string {
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
	if style < 0 || int(style) >= len(utility.Styles) {
		t.Fatalf("player %d reported style %d", m.Player, style)
	}
	return utility.Styles[style].Name
}

func closeControllers(s *session.Session) {
	for _, m := range s.AI {
		if m == nil {
			continue
		}
		if h, ok := m.Ext.(*aikit.Host); ok && h != nil {
			h.Close()
		}
	}
}

func stepBattle(s *session.Session, n int) {
	for i := 0; i < n && s.State == session.StateBattle; i++ {
		s.Step(s.Clock.ScaledAnchor + 1)
	}
}
