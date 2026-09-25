package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
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
	shell, err := newGameShell(Options{Root: cs.root, Roots: cs.roots, ContentProfile: cs.profile}, cs)
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
