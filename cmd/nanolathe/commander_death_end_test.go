package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// skirmishGadget returns the authored SKIRMISH.GUI record for a gadget name.
func skirmishGadget(t *testing.T, p *ui.Panel, name string) gui.Gadget {
	t.Helper()
	if p == nil || p.Window == nil {
		t.Fatal("no skirmish panel is open")
	}
	for _, gad := range p.Window.Gadgets {
		if strings.EqualFold(gad.Name, name) {
			return gad
		}
	}
	t.Fatalf("SKIRMISH.GUI has no %q gadget", name)
	return gui.Gadget{}
}

// TestSkirmishCommanderDeathWidgetSaysGameEndsExactlyWhenItDoes pins the whole
// CommanderDeath chain in one place: the authored stage labels, the state byte
// the screen build stamps, the description text, the xor-1 toggle, and the rule
// word the session actually reads.
//
// [08 R-SKIR-01 §1] "Screen build": CommanderDeath `0` → state 1, `Game
// continues after Commander is destroyed.`, else state 0, `Game ends when
// commander is destroyed.` The authored gadget carries `stages=2` and the two
// labels `Game ends|Continues`, so state 0 is the "Game ends" label and state 1
// is "Continues" — the polarity is inverted with respect to the rule word, and
// that inversion is retail's, not a defect. [08 R-SKIR-01 §1] "Callbacks" gives
// the toggle as `xor 1`, and the registry default as `SkirmishCommanderDeath`
// 1, i.e. the screen opens on "Game ends".
//
// The property the test asserts is the one a player reads off the screen: the
// widget shows "Game ends" exactly when the session's rule word will run the
// owner sweep on commander death [08 R-SKIR-01 §3].
func TestSkirmishCommanderDeathWidgetSaysGameEndsExactlyWhenItDoes(t *testing.T) {
	root := probeRetail(t)
	opts := Options{Root: root, Map: "ashap plateau"}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	g, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	g.openMenu(modeMenuSkirmish)
	p := g.activePanel()
	gad := skirmishGadget(t, p, "CommanderDeath")
	if len(gad.Labels) != 2 || gad.Labels[0] != "Game ends" || gad.Labels[1] != "Continues" {
		t.Fatalf("authored CommanderDeath labels = %v, want [\"Game ends\" \"Continues\"]", gad.Labels)
	}

	// The screen opens on the retail default, which ends the game.
	if g.setup.CommanderDeath != session.SkirmishDefaultCommanderDeath {
		t.Fatalf("default rule word = %d, want %d", g.setup.CommanderDeath, session.SkirmishDefaultCommanderDeath)
	}

	for _, rule := range []int{0, 1, 2} {
		g.setup.CommanderDeath = rule
		g.refreshRetailPanel()
		status := clampMenuStage(p.StageAt(p.Index("CommanderDeath")), len(gad.Labels))
		shown := gad.Labels[status]
		ends := session.CommanderDeathMode(rule) != session.CommanderDeathContinues
		if (shown == "Game ends") != ends {
			t.Errorf("rule %d shows %q but the session ends on commander death = %v", rule, shown, ends)
		}
		wantHelp := "Game ends when commander is destroyed."
		if !ends {
			wantHelp = "Game continues after Commander is destroyed."
		}
		if got := p.HelpOf("CommanderDeath"); got != wantHelp {
			t.Errorf("rule %d description = %q, want %q", rule, got, wantHelp)
		}
	}

	// The toggle is xor 1 and the label follows it in both directions.
	g.setup.CommanderDeath = 1
	g.activateSkirmishGadget("commanderdeath")
	if g.setup.CommanderDeath != 1 {
		t.Fatal("case-folded callback changed CommanderDeath")
	}
	g.activateSkirmishGadget("CommanderDeath")
	if g.setup.CommanderDeath != 0 {
		t.Fatalf("toggle from 1 gave %d, want 0", g.setup.CommanderDeath)
	}
	if got := gad.Labels[clampMenuStage(p.StageAt(p.Index("CommanderDeath")), len(gad.Labels))]; got != "Continues" {
		t.Errorf("after toggling to rule 0 the widget shows %q, want \"Continues\"", got)
	}
	g.activateSkirmishGadget("CommanderDeath")
	if g.setup.CommanderDeath != 1 {
		t.Fatalf("toggle from 0 gave %d, want 1", g.setup.CommanderDeath)
	}
	if got := gad.Labels[clampMenuStage(p.StageAt(p.Index("CommanderDeath")), len(gad.Labels))]; got != "Game ends" {
		t.Errorf("after toggling back to rule 1 the widget shows %q, want \"Game ends\"", got)
	}
}

// TestCommanderDeathSettingRoundTripsToTheSessionRuleWord follows the same word
// out to the persisted block and back in, then into the config the battle is
// started with. Zero is a legitimate stored choice — "game continues" — and a
// loader that treated it as an absent value would silently promote it to the
// default, so a screen reading "Continues" would start a battle that ends
// [08 R-SKIR-01 §1] "Registry mirror".
func TestCommanderDeathSettingRoundTripsToTheSessionRuleWord(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	t.Setenv(settings.EnvPath, path)
	maps := []string{"Anteer Straight"}

	for _, rule := range []int{0, 1} {
		src := &gameShell{maps: maps}
		src.setup = newSkirmishMenuConfig(maps[0])
		src.setup.CommanderDeath = rule
		src.retailControllers = [session.SkirmishMaxPlayers]int{1, 2}
		src.retailControllersSet = true
		src.settingsWritable = true
		src.saveSettings()

		loaded, err := settings.LoadFrom(path)
		if err != nil {
			t.Fatalf("rule %d: LoadFrom: %v", rule, err)
		}
		if loaded.Skirmish.CommanderDeath != rule {
			t.Fatalf("rule %d survived the file as %d", rule, loaded.Skirmish.CommanderDeath)
		}
		dst := &gameShell{maps: maps}
		dst.setup = newSkirmishMenuConfig(maps[0])
		dst.applySettings(loaded)
		if dst.setup.CommanderDeath != rule {
			t.Fatalf("rule %d came back into the shell as %d", rule, dst.setup.CommanderDeath)
		}
		cfg := dst.skirmishConfigForStart(maps[0])
		if cfg.CommanderDeath != rule {
			t.Fatalf("rule %d reached the session config as %d", rule, cfg.CommanderDeath)
		}
	}
}

// TestCommanderKillReachesThePostBattleScreen is the whole reported path, end
// to end through the presentation seam: a skirmish composed the way the
// skirmish screen composes one, the enemy commander killed, and the post-battle
// screen up within the six 30-tick settlement dues plus one frame.
//
// It exercises the two things a session-level test cannot: that the latching
// sub-tick's publication is what the asynchronous client presents after the
// host joins it [03 §2.4][I6], and that the battle frame's gate
// (`isResultVisible`, then the post-battle controller) fires on that frame
// rather than on some later one.
func TestCommanderKillReachesThePostBattleScreen(t *testing.T) {
	root := probeRetail(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()

	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	// The rows the skirmish screen starts a battle with: one human, one
	// computer, the retail rule-word defaults [08 R-SKIR-01 §1].
	shell.retailControllers = [session.SkirmishMaxPlayers]int{1, 2}
	shell.retailControllersSet = true
	cfg := shell.skirmishConfigForStart(opts.Map)
	if cfg.CommanderDeath != session.SkirmishDefaultCommanderDeath {
		t.Fatalf("start config rule word = %d, want the default %d", cfg.CommanderDeath, session.SkirmishDefaultCommanderDeath)
	}
	request, err := skirmishBattleRequest(opts, cs, cfg, headlessScenarioSkirmish, nil, newBattleSeedSource(opts))
	if err != nil {
		t.Fatal(err)
	}
	composed, err := composeAuthoritativeBattle(request)
	if err != nil {
		t.Fatal(err)
	}
	sess := composed.Session
	var b *battleSession
	var cl *client.Client
	cl, err = client.New(client.Options{Width: 800, Height: 600, Buffer: &frame.Buffer{},
		PresentationTick: func() (uint32, bool) { return b.presentationTick() },
		JoinSimulation:   func() { b.stopSimulation(cl) },
	})
	if err != nil {
		t.Fatal(err)
	}
	previous := clPtr
	clPtr = cl
	defer func() { clPtr = previous }()
	cl.SetModelFS(cs.unmappedMount)
	b, err = composeBattleEntry(sess, sess.Catalog, cs, cl, shell)
	if err != nil {
		t.Fatal(err)
	}
	defer b.teardown(cl)
	shell.battle = b
	shell.frontend.Mode = modeBattle
	cl.SetAsyncSimulation(true)
	defer cl.SetAsyncSimulation(false)
	b.syncSimulationMode(cl)

	step := func() {
		b.prepareSimulationStep(sess.Clock.ScaledAnchor + 1)
		b.launchSimulation(cl)
		b.joinSimulation(cl)
		cl.PinPresentation()
	}
	for i := 0; i < 10; i++ {
		step()
	}
	if b.isResultVisible() {
		t.Fatal("the post-battle screen is up before anything has died")
	}

	var commander pool.Handle
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Owner == 1 && u.Def != nil && u.Def.Commander {
			commander = u.Handle
			break
		}
	}
	if commander == 0 {
		t.Fatal("the computer player has no commander to kill")
	}
	sess.Units.Destroy(commander, units.DeathKilled)

	// The kill lands on an arbitrary tick; the end-condition block only runs on
	// the local slot's 30-tick settlement due, so the first true due is up to
	// one due away. From there the shared countdown arms and then takes five
	// more true dues to cross below zero — "the sixth consecutive true due, 150
	// ticks after the first" [08 R-TRIG-01 §6] "Countdown and latch". Six dues
	// plus the frame the latch publishes on, with one frame of slack for the
	// tick the kill is finalized on.
	//
	// **Correction (WU-19-116).** This was 30*5+2, which held only while the
	// evaluator ran every sub-tick and armed its own private deadline from the
	// kill tick. The poll rides UpdateTime now [05 R-ECO-01 §1], and the wait
	// to the first due is real.
	const bound = 30*6 + 2
	killTick := sess.Clock.GlobalTick
	visibleAt := uint32(0)
	for i := 0; i < bound; i++ {
		step()
		if b.isResultVisible() {
			visibleAt = sess.Clock.GlobalTick
			break
		}
	}
	if visibleAt == 0 {
		t.Fatalf("the post-battle screen never came up: result %+v latch %+v live1=%d",
			sess.GetResult(), sess.Latch, sess.Units.LiveCountForPlayer(1))
	}
	if elapsed := visibleAt - killTick; elapsed > bound {
		t.Fatalf("the post-battle screen took %d ticks, want at most %d", elapsed, bound)
	}
	// The client reads the committed frame, not the live session: the latching
	// sub-tick's publication carries the terminal result [I6].
	cur, ok := b.currentSnapshot()
	if !ok || !cur.Result.Ended || cur.Tick != visibleAt {
		t.Fatalf("committed frame at the visible edge = %+v (tick %d), want the ended result at tick %d", cur.Result, cur.Tick, visibleAt)
	}
	if cur.Result.Kind != "victory" {
		t.Fatalf("killing the only opponent's commander gave %q", cur.Result.Kind)
	}
	if presented := cl.PresentedFrame(); presented == nil || presented.Tick != visibleAt || !presented.Result.Ended {
		t.Fatalf("the asynchronous client did not present the terminal tick %d", visibleAt)
	}
	if dir := os.Getenv("NANOLATHE_RESULT_SHOTS"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "victory-title.png"))
	}
	// The host must advance the same result the renderer sees, including the
	// front-end size change and the authored return control [08 R-CAMP-01 §8].
	for range 30 {
		shell.step(1.0/30, cl)
		cl.PinPresentation()
	}
	if !b.resultScreenActive(cl.PresentedFrame()) {
		t.Fatal("the post-battle host and the presented frame did not reach ENDMSN together")
	}
	if w, h := cl.Size(); w != retailScreenW || h != retailScreenH {
		t.Fatalf("ENDMSN surface = %dx%d, want 640x480", w, h)
	}
	cl.ComposeFrame()
	if !b.hud.resultState.initialized || shell.resultBackground == nil {
		t.Fatal("ENDMSN did not render its result rows and outcome background")
	}
	if dir := os.Getenv("NANOLATHE_RESULT_SHOTS"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "victory-results.png"))
	}
	panel := b.hud.resultPanel
	r := panel.Window.PlacedRect(panel.Index("MainMenu"))
	in := cl.Input()
	in.Mouse.SetPosition(float32(r.X+1), float32(r.Y+1))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	shell.step(1.0/30, cl)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	shell.step(1.0/30, cl)
	if shell.battle != nil || shell.frontend.Mode != modeMenuMain {
		t.Fatal("ENDMSN MainMenu did not return to the front end")
	}
	if dir := os.Getenv("NANOLATHE_RESULT_SHOTS"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "returned-main-menu.png"))
	}
}
