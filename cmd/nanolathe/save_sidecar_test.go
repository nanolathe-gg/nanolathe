package main

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// A battle running with mutators saves through the dialog with a sidecar,
// and loading it restores the recorded selection whatever the host has
// selected since: the mutators, the rule set, the Community entry table and
// the catalog identity, and the shell then selects the recorded mutators and
// rule set itself (docs/DESIGN_MODS_MUTATORS.md §7, P6). The same bank
// without its sidecar loads as a retail save always has, and a sidecar
// naming a mod that is not installed refuses with a message that says so.
func TestSaveSidecarRestoresTheRecordedSelection(t *testing.T) {
	resetSaveLoadScreenState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var mutators content.Mutators
	if err := mutators.SetFactor("health", content.Factor{Num: 2, Den: 1}); err != nil {
		t.Fatal(err)
	}
	opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 7, Mutators: mutators, Gameplay: gameplay.Community39, GameplaySet: true}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail content unavailable: %v", err)
	}
	defer cs.Close()
	previous := clPtr
	defer func() { clPtr = previous }()
	shell, cl, err := newDirectBattleView(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.teardownBattle(cl)
	shell.opts.Root = t.TempDir()
	millis := &shotMillisSource{}
	for i := 0; i < 30; i++ {
		shell.battle.millisSource = millis
		millis.step++
		cl.Step(1.0 / 30)
	}
	saved := shell.battle.sess
	if saved.Mutators != mutators || saved.Rules.Name != session.CommunityRuleSetName {
		t.Fatalf("battle runs %q under %s, want %q under Community", saved.Mutators, saved.Rules.Name, mutators)
	}

	// Saving is no longer refused while mutators are active.
	if err := shell.openSaveLoadScreen(saveScreenMode, saveLoadFromBattle); err != nil || saveLoadUI == nil {
		t.Fatalf("the save dialog did not open: %v", err)
	}
	saveLoadUI.SetName("mutated")
	shell.commitSaveLoadWrite()
	path := session.RetailSavePath(shell.saveLoadDir(), "mutated")
	sc, ok, err := save.ReadSidecar(path)
	if err != nil || !ok {
		t.Fatalf("the save has no sidecar: (%v, %v)", ok, err)
	}
	if sc.Mutators["health"] != "2" || len(sc.Mutators) != 1 || sc.Rules != session.CommunityRuleSetName ||
		sc.Catalog != saved.Catalog.Hash || sc.Community.Entry != saved.EntryCommunity ||
		sc.UnitLimit != shell.setup.UnitLimit || sc.Mod.Mod != nil || sc.Mod.Custom {
		t.Fatalf("sidecar %+v does not record the battle", sc)
	}

	// The load dialog names the save's mod and mutators before loading it.
	if err := shell.openSaveLoadScreen(loadScreenMode, saveLoadFromBattle); err != nil || saveLoadUI == nil {
		t.Fatalf("the load dialog did not open: %v", err)
	}
	for i, entry := range saveLoadUI.Entries() {
		if entry.Path == path {
			shell.selectSaveLoadRow(i)
		}
	}
	if got, want := saveLoadPanel.TextOf(saveSidecarLineGadget), "Total Annihilation - Health x2"; got != want {
		t.Fatalf("load dialog sidecar line %q, want %q", got, want)
	}
	cl.ComposeFrame()
	captureSaveLoadUI(t, cl, "load-sidecar-line")
	shell.closeSaveLoadScreen()

	// The host's selection moves on; the load puts the recorded one back.
	shell.opts.Mutators, shell.gameplay = content.Mutators{}, gameplay.Strict31
	if err := shell.loadRetailSavePath(path); err != nil {
		t.Fatal(err)
	}
	restored := shell.battle.sess
	if restored == saved || restored.Mutators != mutators || restored.Rules.Name != session.CommunityRuleSetName ||
		restored.Catalog.Hash != sc.Catalog || restored.EntryCommunity != sc.Community.Entry {
		t.Fatalf("restored %q under %s (catalog %s), want the sidecar's %q under %s (catalog %s)",
			restored.Mutators, restored.Rules.Name, restored.Catalog.Hash, mutators, sc.Rules, sc.Catalog)
	}
	if shell.opts.Mutators != mutators || shell.gameplay != gameplay.Community39 {
		t.Fatalf("the shell selects %q under %s after the load, want the save's", shell.opts.Mutators, shell.gameplay)
	}
	for _, line := range shell.battle.messageRing().Visible() {
		if strings.Contains(line.Text, "different content") {
			t.Fatalf("a matching catalog warned: %q", line.Text)
		}
	}

	// Without its sidecar the bank restores under the host's selection.
	if err := save.RemoveSidecar(path); err != nil {
		t.Fatal(err)
	}
	shell.opts.Mutators = content.Mutators{}
	if err := shell.loadRetailSavePath(path); err != nil {
		t.Fatal(err)
	}
	if !shell.battle.sess.Mutators.IsZero() {
		t.Fatalf("a save without a sidecar restored mutators %q", shell.battle.sess.Mutators)
	}

	// A sidecar naming a mod that is not installed refuses and says why.
	sc.Mod = save.SidecarModRef{Mod: &save.SidecarMod{ID: "absent", Version: "1"}}
	if err := save.WriteSidecar(path, sc); err != nil {
		t.Fatal(err)
	}
	err = shell.loadRetailSavePath(path)
	if message := loadFailureMessage(err); err == nil || !strings.Contains(message, "absent 1") || !strings.Contains(message, "not installed") {
		t.Fatalf("load of an uninstalled mod's save = %v (%q), want a refusal naming it", err, message)
	}

	// Deleting the save through the dialog takes the sidecar with it.
	if err := shell.openSaveLoadScreen(saveScreenMode, saveLoadFromBattle); err != nil || saveLoadUI == nil {
		t.Fatalf("the save dialog did not open: %v", err)
	}
	for i, entry := range saveLoadUI.Entries() {
		if entry.Path == path {
			saveLoadUI.Select(i)
		}
	}
	shell.activateSaveLoadGadget("DELETE")
	if _, err := os.Stat(save.SidecarPath(path)); !os.IsNotExist(err) {
		t.Fatalf("the sidecar outlived its deleted save: %v", err)
	}
}

// A game saved under an installed mod, loaded while another mod (here none)
// runs, reloads onto the recorded mod and then restores the battle on it,
// selecting the mod and the recorded mutators (docs/DESIGN_MODS_MUTATORS.md
// §7.3 step 2, P6). The mod is an authored package that changes no content,
// so the bank written on the base install restores on it unchanged.
func TestLoadingAnotherModsSaveSwitchesToItFirst(t *testing.T) {
	resetSaveLoadScreenState(t)
	retail := testsupport.RetailRoot(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	pack := t.TempDir()
	for name, body := range map[string]string{
		modlibrary.MetadataFile: `{"schema":1,"id":"plain","name":"Plain","version":"1"}`,
		"readme.txt":            "An authored package that overrides nothing.",
	} {
		if err := os.WriteFile(filepath.Join(pack, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer devnull.Close()
	if err := runInstallMod(Options{Root: retail, InstallMod: pack}, devnull); err != nil {
		t.Fatal(err)
	}
	saved := clPtr
	defer func() { clPtr = saved }()

	// The bank comes from a battle on the base install; its sidecar names the
	// mod, as a save written while it ran would.
	battleOpts := Options{Root: retail, Map: "ashap plateau", Seed: 7}
	battleContent, err := openContent(battleOpts)
	if err != nil {
		t.Skipf("retail content unavailable: %v", err)
	}
	defer battleContent.Close()
	battleShell, battleClient, err := newDirectBattleView(battleOpts, battleContent)
	if err != nil {
		t.Fatal(err)
	}
	defer battleShell.teardownBattle(battleClient)
	path := session.RetailSavePath(t.TempDir(), "plain")
	if err := battleShell.writeBattleSave(path, "plain"); err != nil {
		t.Fatal(err)
	}
	sc := battleShell.saveSidecar()
	sc.Mod = save.SidecarModRef{Mod: &save.SidecarMod{ID: "plain", Version: "1"}}
	sc.Mutators = map[string]string{"sight": "2"}
	sc.Catalog = ""
	if err := save.WriteSidecar(path, sc); err != nil {
		t.Fatal(err)
	}

	cs, err := openContent(Options{Root: retail})
	if err != nil {
		t.Fatal(err)
	}
	shell, err := newGameShell(Options{Root: cs.root, Roots: cs.roots, ModConfig: cs.configPath}, cs)
	if err != nil {
		t.Fatal(err)
	}
	shell.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	clPtr = cl
	host := &shellHost{shell: shell}
	if err := bindShellContent(shell, cl); err != nil {
		t.Fatal(err)
	}
	if err := shell.loadRetailSavePath(path); err != nil {
		t.Fatalf("the load was refused: %v", err)
	}
	if pendingContentReload == nil || pendingContentReload.loadSave != path || pendingContentReload.selector != "plain@1" {
		t.Fatalf("the load requested %+v, want a switch to plain@1 that then loads the game", pendingContentReload)
	}
	host.step(1.0/30, cl)
	next := host.shell
	defer func() { next.teardownBattle(cl); _ = next.cs.Close() }()
	if next == shell || next.cs.mod == nil || next.cs.mod.ID != "plain" {
		t.Fatalf("the host did not switch to the save's mod: %+v", next.cs.mod)
	}
	if pendingContentReload != nil {
		t.Fatal("the load after the switch asked for another switch")
	}
	var sight content.Mutators
	if err := sight.SetFactor("sight", content.Factor{Num: 2, Den: 1}); err != nil {
		t.Fatal(err)
	}
	if next.battle == nil || next.battle.sess == nil || next.battle.sess.Mutators != sight {
		t.Fatal("the game was not restored on the new mod with its mutators")
	}
	if next.modSetting.ID != "plain" || next.modSetting.Version != "1" || next.opts.Mutators != sight {
		t.Fatalf("the shell selects mod %+v and mutators %q, want the save's", next.modSetting, next.opts.Mutators)
	}
}

// A battle with unit restrictions saves a schema 2 sidecar that records
// them, and loading it restores the restricted index space whatever the
// host has selected since, then makes the recorded set the running content's
// restriction setting and the next battle's (docs/DESIGN_MODS_MUTATORS.md
// §15.5, proposals R-P6 and R-P7). The load dialog counts them. A recorded
// unit the content cannot take, or an entry that does not read, refuses the
// load with a message naming it. Skipped without retail assets.
func TestSaveSidecarRestoresUnitRestrictions(t *testing.T) {
	resetSaveLoadScreenState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	restrictions, err := content.ParseRestrictions(map[string]int{"armflash": 2, "armpw": 0})
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 7, Restrictions: restrictions, RestrictionsSet: true}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail content unavailable: %v", err)
	}
	defer cs.Close()
	previous := clPtr
	defer func() { clPtr = previous }()
	shell, cl, err := newDirectBattleView(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.teardownBattle(cl)
	saved := shell.battle.sess
	if !saved.Restrictions.Equal(restrictions) {
		t.Fatalf("the battle runs %q, want %q", saved.Restrictions, restrictions)
	}
	path := session.RetailSavePath(t.TempDir(), "restricted")
	if err := shell.writeSaveWithSidecar(path, func() error { return shell.writeBattleSave(path, "restricted") }); err != nil {
		t.Fatal(err)
	}
	sc, ok, err := save.ReadSidecar(path)
	if err != nil || !ok || sc.Schema != save.SidecarSchemaRestrictions || len(sc.Restrictions) != 2 || sc.Restrictions["armpw"] != 0 || sc.Restrictions["armflash"] != 2 {
		t.Fatalf("sidecar %+v (%v, %v), want schema 2 recording the battle's restrictions", sc, ok, err)
	}
	if got, want := saveSidecarLine(path, nil), "Total Annihilation - 2 restrictions"; got != want {
		t.Fatalf("load dialog sidecar line %q, want %q", got, want)
	}

	// The host's selection moves on; the load puts the recorded one back.
	shell.selectRestrictions(content.Restrictions{})
	if err := shell.loadRetailSavePath(path); err != nil {
		t.Fatal(err)
	}
	restored := shell.battle.sess
	if restored == saved || !restored.Restrictions.Equal(restrictions) || restored.Catalog.Hash != sc.Catalog {
		t.Fatalf("restored %q over catalog %s, want the sidecar's %v over %s", restored.Restrictions, restored.Catalog.Hash, sc.Restrictions, sc.Catalog)
	}
	want, got := saved.Catalog.UnitRecords(), restored.Catalog.UnitRecords()
	if len(got) != len(want) {
		t.Fatalf("restored %d definitions, want the restricted table's %d", len(got), len(want))
	}
	for i := range want {
		if got[i].UnitName != want[i].UnitName || got[i].UnitDefID != want[i].UnitDefID || got[i].Limit != want[i].Limit {
			t.Fatalf("definition %d restored as %s #%d, want %s #%d", i, got[i].UnitName, got[i].UnitDefID, want[i].UnitName, want[i].UnitDefID)
		}
	}
	if !shell.opts.Restrictions.Equal(restrictions) || shell.opts.RestrictionsSet || !maps.Equal(shell.captureSettings().Restrictions, restrictions.Map()) {
		t.Fatalf("the shell selects %q (flag %v, setting %v) after the load, want the save's", shell.opts.Restrictions, shell.opts.RestrictionsSet, shell.captureSettings().Restrictions)
	}
	if line := shell.modStatusLine(); !strings.HasSuffix(line, " - 2 restrictions") {
		t.Fatalf("chip %q after the load, want the save's two restrictions", line)
	}

	// A recorded unit the content cannot take, and an entry that does not
	// read, refuse the load and name it.
	for _, tc := range []struct {
		recorded map[string]int
		want     string
	}{
		{map[string]int{"armflash": 2, "notaunit": 0}, "notaunit (not in this content)"},
		{map[string]int{"ArmPW": 0}, "ArmPW"},
	} {
		sc.Restrictions = tc.recorded
		if err := save.WriteSidecar(path, sc); err != nil {
			t.Fatal(err)
		}
		err := shell.loadRetailSavePath(path)
		if message := loadFailureMessage(err); err == nil || !strings.Contains(message, tc.want) {
			t.Fatalf("load recording %v = %v (%q), want a refusal naming %s", tc.recorded, err, message, tc.want)
		}
	}
	if shell.battle.sess != restored || !shell.opts.Restrictions.Equal(restrictions) {
		t.Fatal("a refused load changed the running battle or the selection")
	}
}

// Smart makers follows the player's current selection on every load, while
// every other mutator is the save's own (docs/DESIGN_MODS_MUTATORS.md §6.8,
// §7.3, D26): a save made without it restores with it when it is selected,
// its metal makers marked, without the different-content warning, and the
// shell keeps it selected; a save made with it restores without it when it
// is not; a save without a sidecar takes it from the selection alone; the
// load dialog's line names the set the load will run under (§8.4); and a
// save with other content still warns.
func TestLoadingAnySaveFollowsSmartMakers(t *testing.T) {
	resetSaveLoadScreenState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	health := content.Mutators{Health: content.Factor{Num: 2, Den: 1}}
	opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 7, Mutators: health}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail content unavailable: %v", err)
	}
	defer cs.Close()
	previous := clPtr
	defer func() { clPtr = previous }()
	shell, cl, err := newDirectBattleView(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.teardownBattle(cl)
	shell.opts.Root = t.TempDir()
	millis := &shotMillisSource{}
	for i := 0; i < 30; i++ {
		shell.battle.millisSource = millis
		millis.step++
		cl.Step(1.0 / 30)
	}
	write := func(name string) string {
		t.Helper()
		if err := shell.openSaveLoadScreen(saveScreenMode, saveLoadFromBattle); err != nil || saveLoadUI == nil {
			t.Fatalf("the save dialog did not open: %v", err)
		}
		saveLoadUI.SetName(name)
		shell.commitSaveLoadWrite()
		return session.RetailSavePath(shell.saveLoadDir(), name)
	}
	load := func(path string, selected content.Mutators) *session.Session {
		t.Helper()
		shell.opts.Mutators = selected
		if err := shell.loadRetailSavePath(path); err != nil {
			t.Fatal(err)
		}
		for _, line := range shell.battle.messageRing().Visible() {
			if strings.Contains(line.Text, "different content") {
				t.Fatalf("a matching catalog warned: %q", line.Text)
			}
		}
		return shell.battle.sess
	}
	on := content.Mutators{SmartMakers: content.SwitchOn(), Sight: content.Factor{Num: 3, Den: 1}}
	plain := write("plain")
	// The load dialog names what the load will run under.
	switchOn, off := content.SwitchOn(), content.Factor{}
	if got, want := saveSidecarLine(plain, &switchOn), "Total Annihilation - Smart makers, Health x2"; got != want {
		t.Fatalf("load dialog line with Smart makers selected %q, want %q", got, want)
	}
	// The Save dialog shows what the file records.
	if got, want := saveSidecarLine(plain, nil), "Total Annihilation - Health x2"; got != want {
		t.Fatalf("save dialog line %q, want %q", got, want)
	}

	// Made without it, loaded with it selected: the save's Health with the
	// selection's Smart makers, and not the selection's Sight.
	want := health
	want.SmartMakers = content.SwitchOn()
	restored := load(plain, on)
	if restored.Mutators != want || shell.opts.Mutators != want {
		t.Fatalf("restored %q, selection %q, want %q", restored.Mutators, shell.opts.Mutators, want)
	}
	if maker, ok := restored.Catalog.Unit("armmakr"); !ok || !maker.SmartMaker {
		t.Fatal("the restored catalog does not mark the stock metal maker")
	}

	// Made with it, loaded with it off: Smart makers is off.
	smart := write("smart")
	if sc, ok, err := save.ReadSidecar(smart); err != nil || !ok || sc.Mutators["smartMakers"] != "on" {
		t.Fatalf("the second save does not record Smart makers: %+v %v %v", sc.Mutators, ok, err)
	}
	if got, want := saveSidecarLine(smart, &off), "Total Annihilation - Health x2"; got != want {
		t.Fatalf("load dialog line with Smart makers off %q, want %q", got, want)
	}
	if got, want := saveSidecarLine(smart, nil), "Total Annihilation - Smart makers, Health x2"; got != want {
		t.Fatalf("save dialog line %q, want %q", got, want)
	}
	if restored = load(smart, content.Mutators{}); restored.Mutators != health || shell.opts.Mutators != health {
		t.Fatalf("restored %q, selection %q, want %q", restored.Mutators, shell.opts.Mutators, health)
	}

	// A save whose recorded content differs still warns, whatever the
	// Smart makers selection.
	sc, _, err := save.ReadSidecar(smart)
	if err != nil {
		t.Fatal(err)
	}
	sc.Catalog = "another catalog"
	if err := save.WriteSidecar(smart, sc); err != nil {
		t.Fatal(err)
	}
	shell.opts.Mutators = on
	if err := shell.loadRetailSavePath(smart); err != nil {
		t.Fatal(err)
	}
	warned := false
	for _, line := range shell.battle.messageRing().Visible() {
		warned = warned || strings.Contains(line.Text, "different content")
	}
	if !warned {
		t.Fatal("a save with another catalog loaded without the different-content warning")
	}

	// Without a sidecar, the selection's Smart makers is the only mutator.
	if err := save.RemoveSidecar(plain); err != nil {
		t.Fatal(err)
	}
	if restored = load(plain, on); restored.Mutators != (content.Mutators{SmartMakers: content.SwitchOn()}) {
		t.Fatalf("a save without a sidecar restored %q", restored.Mutators)
	}
}

// TestContinuationFollowsSmartMakers: a between-missions save enters the
// next mission under its recorded mutators, Smart makers excepted, which
// follows the player's selection as on every load
// (docs/DESIGN_MODS_MUTATORS.md §7.3, D26).
func TestContinuationFollowsSmartMakers(t *testing.T) {
	health := content.Mutators{Health: content.Factor{Num: 2, Den: 1}}
	smart := health
	smart.SmartMakers = content.SwitchOn()
	for _, tc := range []struct {
		name                      string
		recorded, selected, wants content.Mutators
	}{
		{"made without, loaded with", health, smart, smart},
		{"made with, loaded without", smart, content.Mutators{}, health},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetSaveLoadScreenState(t)
			t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
			shell, saveDir := retailShellForTest(t)
			shell.activateGadget("SINGLE")
			shell.activateGadget("NewCamp")
			if shell.campaignIdx < 0 || shell.campaignIdx >= len(shell.campaignOptions) {
				t.Skip("the reference install exposes no campaign on NEWGAME")
			}
			campaign := shell.campaignOptions[shell.campaignIdx]
			if len(campaign.Missions) < 2 {
				t.Skip("the selected campaign has no successor mission")
			}
			first, second := campaign.Missions[0], campaign.Missions[1]
			// The results surface of a won first mission, with the recorded
			// mutators on its session, as the continuation walk builds it.
			shell.battle = &battleSession{
				shell: shell,
				sess: &session.Session{Mutators: tc.recorded, Mission: &mission.Mission{
					Type: mission.TypeCampaign, CampaignPath: campaign.Path, CampaignIndex: first.Index,
					CampaignMissionName: first.Name, Difficulty: 1,
				}},
				postBattle: session.NewPostBattleController(
					frame.ResultView{Ended: true, Kind: "victory"},
					session.PostBattleConfig{
						Kind: session.PostBattleCampaign, CampaignCDOK: true, Windowed: true,
						CampaignPath: campaign.Path, Campaign: campaign.Path,
						Mission: first.Name, Map: first.Name, MissionIndex: first.Index,
						HasNext: true, NextMission: second.Name, Difficulty: 1, Players: 1,
					}),
			}
			if err := shell.openSaveLoadScreen(saveScreenMode, saveLoadFromResults); err != nil || saveLoadUI == nil {
				t.Fatalf("the save dialog did not open: %v", err)
			}
			saveLoadUI.SetName("continuation")
			shell.commitSaveLoadWrite()
			shell.battle = nil
			path := session.RetailSavePath(saveDir, "continuation")
			if sc, ok, err := save.ReadSidecar(path); err != nil || !ok || sc.Mutators["health"] != "2" {
				t.Fatalf("continuation sidecar %+v (%v, %v), want the recorded mutators", sc.Mutators, ok, err)
			}

			shell.opts.Mutators = tc.selected
			if err := shell.loadRetailSavePath(path); err != nil {
				t.Fatal(err)
			}
			if shell.briefing == nil {
				t.Fatal("loading the continuation did not open the campaign briefing")
			}
			if shell.opts.Mutators != tc.wants {
				t.Fatalf("the next mission's selection %q, want %q", shell.opts.Mutators, tc.wants)
			}
		})
	}
}
