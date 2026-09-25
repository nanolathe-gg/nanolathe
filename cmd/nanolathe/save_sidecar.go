package main

// The save sidecar's host half, per docs/DESIGN_MODS_MUTATORS.md §7: what
// the shell records beside every bank it writes, and what a load does with
// a sidecar it finds — switch mod, select the recorded mutators and rule
// set, and restore under the recorded Community and unit limit.

import (
	"errors"
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// saveLoadRefusal is a load the shell refuses for a reason the player can
// act on, such as a save made with a mod that is not installed. The load
// dialog shows its text instead of the retail `Invalid savegame file`.
type saveLoadRefusal struct{ message string }

func (e *saveLoadRefusal) Error() string { return e.message }

func refuseLoad(format string, args ...any) error {
	return &saveLoadRefusal{message: fmt.Sprintf(format, args...)}
}

// loadFailureMessage is what the load dialog shows for a failed load.
func loadFailureMessage(err error) string {
	var refusal *saveLoadRefusal
	if errors.As(err, &refusal) {
		return refusal.message
	}
	return retailInvalidSaveMessage
}

// saveSidecar is the sidecar of the battle this shell would save now: the
// session's half (session.SaveSidecar) plus the host's selections — the
// running mod, the resolved content profile and the configured unit-limit
// word that sizes a battle (§7.2).
func (g *gameShell) saveSidecar() save.Sidecar {
	var sess *session.Session
	if g.battle != nil {
		sess = g.battle.sess
	}
	out := session.SaveSidecar(sess)
	out.UnitLimit = g.setup.UnitLimit
	if g.cs != nil {
		out.ContentProfile = g.cs.profile
		switch {
		case g.cs.manualRoots:
			out.Mod.Custom = true
		case g.cs.mod != nil:
			out.Mod.Mod = &save.SidecarMod{ID: g.cs.mod.ID, Version: g.cs.mod.Version, SHA256: g.cs.mod.Receipt.SHA256}
		}
	}
	return out
}

// writeSaveWithSidecar writes one save — the bank through writeBank, then
// its sidecar (§7.1). Any sidecar of an earlier save under the same name is
// removed first, so an interrupted overwrite never pairs the new bank with
// the old selection. A sidecar that cannot be written takes the new bank
// with it: a bank alone would load as if no mutators had been active.
func (g *gameShell) writeSaveWithSidecar(path string, writeBank func() error) error {
	if err := save.RemoveSidecar(path); err != nil {
		return err
	}
	if err := writeBank(); err != nil {
		return err
	}
	if err := save.WriteSidecar(path, g.saveSidecar()); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("nanolathe: save sidecar not written: %v: logical path %s, providers searched [save directory], expected a writable save directory", err, save.SidecarPath(path))
	}
	return nil
}

// sidecarModPlan is what a sidecar's `mod` asks of the running content
// (§7.3 step 2): nothing, a switch to another installed mod, or a refusal.
type sidecarModPlan struct {
	// switchTo is the mod selector to reload onto ("none" for no mod), ""
	// when the running content already matches.
	switchTo  string
	selection settings.ModSelection
}

func (g *gameShell) planSidecarMod(sc save.Sidecar) (sidecarModPlan, error) {
	manual := g.cs != nil && g.cs.manualRoots
	var running *modlibrary.Mod
	if g.cs != nil {
		running = g.cs.mod
	}
	if sc.Mod.Custom {
		if manual {
			return sidecarModPlan{}, nil
		}
		return sidecarModPlan{}, refuseLoad("This game was saved with custom content roots. Start Nanolathe with the same --root flags to load it")
	}
	want := sc.Mod.Mod
	if manual {
		return sidecarModPlan{}, refuseLoad("This game was saved with %s. It cannot be loaded while the command line stacks its own content roots", sidecarModName(want))
	}
	if want == nil {
		if running == nil {
			return sidecarModPlan{}, nil
		}
		return sidecarModPlan{switchTo: "none"}, nil
	}
	selection := settings.ModSelection{ID: want.ID, Version: want.Version}
	if running != nil && running.ID == want.ID && running.Version == want.Version {
		return sidecarModPlan{selection: selection}, nil
	}
	lib, err := openModLibrary()
	if err != nil {
		return sidecarModPlan{}, refuseLoad("This game needs the mod %s, but the mod library is unavailable", sidecarModName(want))
	}
	mod, ok, err := lib.Lookup(want.ID, want.Version)
	if err != nil || !ok {
		client := &modfetch.Client{CatalogURL: modfetch.CatalogURL(), CacheDir: lib.Root}
		if manifest, _, cached := client.CachedManifest(); cached {
			if entry, offered := manifest.Offers(want.ID, want.Version); offered {
				return sidecarModPlan{}, refuseLoad("This game needs %s %s. Download it from MODS, Get more mods, then load the game again", entry.Name, entry.Version)
			}
		}
		return sidecarModPlan{}, refuseLoad("This game needs the mod %s, which is not installed", sidecarModName(want))
	}
	if g.cs != nil {
		if missing := unmetModRequirements(g.cs.baseRoots, mod); len(missing) > 0 {
			return sidecarModPlan{}, refuseLoad("This game needs %s %s, which needs %s from the base install", mod.Name, mod.Version, missing[0])
		}
	}
	return sidecarModPlan{switchTo: modSelectorOf(mod.ID, mod.Version), selection: selection}, nil
}

func sidecarModName(m *save.SidecarMod) string {
	if m == nil {
		return "no mod (Total Annihilation)"
	}
	return m.ID + " " + m.Version
}

// sidecarSelection is what a sidecar asks the restore to run under and the
// shell to select afterwards (§7.3 steps 3–6).
type sidecarSelection struct {
	mutators content.Mutators
	// gameplay is the rule set to bind: the recorded name when this build
	// can select it, else its recorded base (P7).
	gameplay gameplay.Mode
	sources  session.CommunitySources
	entry    save.SidecarCommunity
	warnings []string
}

func resolveSidecarSelection(sc save.Sidecar) (sidecarSelection, error) {
	mutators, err := content.ParseMutators(sc.Mutators)
	if err != nil {
		return sidecarSelection{}, refuseLoad("This game was saved with mutators this build does not know: %v", err)
	}
	out := sidecarSelection{mutators: mutators, sources: session.SidecarCommunitySources(sc.Community), entry: sc.Community}
	if mode, err := gameplay.Parse(sc.Rules); err == nil {
		out.gameplay = mode
		return out, nil
	}
	base, err := gameplay.Parse(sc.Gameplay)
	if err != nil {
		base = gameplay.Modern
	}
	out.gameplay = session.BaseModeOf(base)
	out.warnings = append(out.warnings, fmt.Sprintf("Rule set %s is not in this build: loaded under %s", sc.Rules, gameplayLabel(out.gameplay)))
	return out, nil
}

// selectFromSidecar makes a loaded game's recorded selection the shell's
// own (P6): the mutators, and the rule set the restored battle is bound to,
// so the chip, a restart of the battle and the next new battle all match
// the game that was loaded. The mod was already selected by mounting it.
func (g *gameShell) selectFromSidecar(sel sidecarSelection, mod settings.ModSelection) {
	g.opts.Mutators, g.mutatorSetting = sel.mutators, sel.mutators.Map()
	g.gameplay, g.opts.Gameplay = sel.gameplay.Normalize(), sel.gameplay.Normalize()
	g.modSetting = mod
	g.saveSettings()
}

// postLoadWarnings shows the sidecar's warnings (§7.3 steps 3 and 7) on the
// battle's message line. A restored battle opens with no loading screen, so
// the message line is where the player is looking.
func (g *gameShell) postLoadWarnings(warnings []string) {
	if len(warnings) == 0 || g.battle == nil {
		return
	}
	ring := g.battle.messageRing()
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "nanolathe: "+w)
		if ring != nil {
			ring.PostSilent(w, 0, g.battle.currentTick())
		}
	}
}

// switchModForLoad loads a game saved under another mod by reloading onto
// that mod first and then loading the game on the new content (§7.3 step
// 2, §4.4). The switch carries the recorded mutators and rule set, so the
// new shell already holds the selection the load then confirms. The direct
// battle view (--map) has no menu shell to reload, so it refuses instead.
func (g *gameShell) switchModForLoad(path string, plan sidecarModPlan, sel sidecarSelection) error {
	if g.opts.Map != "" {
		name := "no mod"
		if plan.selection.ID != "" {
			name = plan.selection.ID + " " + plan.selection.Version
		}
		return refuseLoad("This game was saved with %s. Start Nanolathe without --map to load it", name)
	}
	pendingContentReload = &contentReloadRequest{
		selector: plan.switchTo,
		mod:      plan.selection,
		mutators: sel.mutators,
		gameplay: sel.gameplay,
		loadSave: path,
	}
	return nil
}

// The load dialog's sidecar line (§8.4, P13): one Nanolathe label beneath
// the authored summary fields naming the selected save's mod and mutators,
// so the player knows before loading that the game will switch. It is a
// copy of the authored `TIME` label moved below it and widened; a save with
// no sidecar leaves it empty.
const (
	saveSidecarLineGadget = "NLSIDECAR"
	saveSidecarLineWidth  = 350
)

func installSaveSidecarLine(window *gui.Window) {
	if window == nil || window.GadgetIndex(saveSidecarLineGadget) >= 0 {
		return
	}
	i := window.GadgetIndex("TIME")
	if i < 0 {
		return
	}
	label := window.Gadgets[i]
	label.Name, label.SourceName, label.Text = saveSidecarLineGadget, saveSidecarLineGadget, ""
	label.Rect.X, label.Rect.Y, label.Rect.W = window.Gadgets[i].Rect.X-51, label.Rect.Y+label.Rect.H+2, saveSidecarLineWidth
	window.Gadgets = append(window.Gadgets, label)
}

// saveSidecarLine is the line's text for the save at bankPath: the mod and,
// when any are active, the mutators. An installed mod is named as the Mods
// screen names it; one that is not installed by its id and version.
func saveSidecarLine(bankPath string) string {
	sc, ok, err := save.ReadSidecar(bankPath)
	if err != nil {
		return "Unreadable Nanolathe sidecar"
	}
	if !ok {
		return ""
	}
	name := "Total Annihilation"
	switch {
	case sc.Mod.Custom:
		name = "Custom content"
	case sc.Mod.Mod != nil:
		name = sidecarModName(sc.Mod.Mod)
		if lib, err := openModLibrary(); err == nil {
			if mod, installed, _ := lib.Lookup(sc.Mod.Mod.ID, sc.Mod.Mod.Version); installed {
				name = mod.Name + " " + mod.Version
			} else {
				name += " (not installed)"
			}
		}
	}
	if m, err := content.ParseMutators(sc.Mutators); err == nil && !m.IsZero() {
		return name + " - " + mutatorSummary(m)
	}
	return name
}
