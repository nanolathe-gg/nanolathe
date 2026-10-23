package main

// The save sidecar's host half, per docs/DESIGN_MODS_MUTATORS.md §7: what
// the shell records beside every bank it writes, and what a load does with
// a sidecar it finds — switch mod, select the recorded mutators, unit
// restrictions and rule set, and restore under the recorded Community and
// unit limit (§15.5 for the restrictions).

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/addonlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
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

// loadFailureMessage is what the load dialog shows for a failed load. A
// restore that refused the save's recorded unit restrictions names the
// entries the running content cannot take (§15.3, §15.5).
func loadFailureMessage(err error) string {
	var refusal *saveLoadRefusal
	if errors.As(err, &refusal) {
		return refusal.message
	}
	var restrictions *content.RestrictionsError
	if errors.As(err, &restrictions) {
		parts := make([]string, len(restrictions.Issues))
		for i, issue := range restrictions.Issues {
			parts[i] = fmt.Sprintf("%s (%s)", issue.Unit, restrictionReasonText(issue.Reason))
		}
		return "This game's unit restrictions name units this content cannot take: " + strings.Join(parts, ", ")
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
	if g.battle != nil {
		out.SurvivalBest = g.battle.recordSurvivalBest()
	}
	out.UnitLimit = g.setup.UnitLimit
	if g.cs != nil {
		out.ContentProfile = g.cs.profile
		switch {
		case g.cs.manualRoots:
			out.Mod.Custom = true
		case g.cs.mod != nil:
			out.Mod.Mod = &save.SidecarMod{ID: g.cs.mod.ID, Version: g.cs.mod.Version, SHA256: g.cs.mod.Receipt.SHA256}
		}
		for _, a := range g.cs.addons {
			out.Addons = append(out.Addons, save.SidecarAddon{ID: a.ID, Version: a.Version, SHA256: fmt.Sprintf("%x", addonlibrary.Digest(a.Archives))})
		}
	}
	// A generated map's recipe, so the game loads after its memory mount is
	// gone (docs/DESIGN_MAP_GENERATOR.md §10).
	out.GeneratedMap = g.battleRecipe()
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
	// addons, when set, is the add-on selection to reload with, because the
	// running content mounts other add-ons than the save recorded (§5.7).
	addons *[]settings.AddonSelection
}

// planSidecarAddons is what a sidecar's `addons` asks of the running
// content (§5.7): nothing when it mounts exactly the recorded add-ons;
// otherwise the selection to reload with, each recorded add-on from the
// library unless the base folder holds it; and a refusal naming a recorded
// add-on this install has nowhere.
func (g *gameShell) planSidecarAddons(sc save.Sidecar) (*[]settings.AddonSelection, error) {
	if g.cs == nil {
		return nil, nil
	}
	want := make([]addonIdentity, 0, len(sc.Addons))
	for _, a := range sc.Addons {
		var digest [32]byte
		if b, err := hex.DecodeString(a.SHA256); err == nil && len(b) == len(digest) {
			copy(digest[:], b)
		}
		want = append(want, addonIdentity{a.ID, a.Version, digest})
	}
	known := libraryAddonsOf()
	if addonsSatisfied(g.cs, want, known) {
		return nil, nil
	}
	if g.cs.manualRoots {
		// A reload would mount the base install alone and lose the stack.
		return nil, refuseLoad("This game was saved with other official add-ons. Start Nanolathe with --addon naming them to load it with these --root flags")
	}
	selection, missing := addonSelectionFor(g.cs, want, known)
	if missing != nil {
		return nil, refuseLoad("This game needs the official add-on %s %s. Get it from Add-ons on the Nanolathe screen, then load the game again", missing.id, missing.version)
	}
	return &selection, nil
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
	// restrictions are a skirmish save's unit restrictions, which the
	// restore checks against its catalog and applies before the mutators
	// (§15.5).
	restrictions content.Restrictions
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
	restrictions, err := content.ParseRestrictions(sc.Restrictions)
	if err != nil {
		return sidecarSelection{}, refuseLoad("This game's unit restrictions cannot be read: %s", strings.TrimPrefix(err.Error(), "content: "))
	}
	out := sidecarSelection{mutators: mutators, restrictions: restrictions, sources: session.SidecarCommunitySources(sc.Community), entry: sc.Community}
	// A sidecar written before mod configs names the per-mod tables this
	// build no longer carries. Its recorded entry table is the complete
	// value the battle ran under, so it stands in as that source's base and
	// the restored battle resolves exactly as saved (DESIGN_MODS_MUTATORS
	// §7.2).
	for i, src := range out.sources.Content {
		if src.Table == "" || src.Table == community.Mainline {
			continue
		}
		if _, err := community.Table(src.Table); err == nil {
			continue
		}
		entry := sc.Community.Entry
		out.sources.Content[i] = community.Overrides{Base: &entry}
	}
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
// restored is the restored battle, nil for a campaign continuation. A
// restored skirmish's unit restrictions, none included, become the running
// content's restriction setting (proposal R-P6); a campaign game says
// nothing about them, since a mission keeps its own unit list (§15.1), so
// it leaves the setting as it was.
func (g *gameShell) selectFromSidecar(sel sidecarSelection, mod settings.ModSelection, restored *session.Session) {
	g.opts.Mutators, g.mutatorSetting = sel.mutators, sel.mutators.Map()
	g.gameplay, g.opts.Gameplay = sel.gameplay.Normalize(), sel.gameplay.Normalize()
	g.modSetting = mod
	if restored != nil && (restored.Mission == nil || restored.Mission.Type != mission.TypeCampaign) {
		g.selectRestrictions(restored.Restrictions)
	}
	g.saveSettings()
}

// continuationRestrictionsRefusal refuses a between-missions save whose
// sidecar records unit restrictions: the next mission is entered under its
// own unit list, so the pairing is malformed (§15.1, §15.5).
func continuationRestrictionsRefusal(sel sidecarSelection) error {
	if sel.restrictions.IsZero() {
		return nil
	}
	return refuseLoad("This campaign save records unit restrictions, which campaign missions do not take")
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
		if plan.addons != nil && plan.switchTo == g.cs.modSelector() {
			name = "other official add-ons"
		}
		return refuseLoad("This game was saved with %s. Start Nanolathe without --map to load it", name)
	}
	pendingContentReload = &contentReloadRequest{
		selector: plan.switchTo,
		mod:      plan.selection,
		mutators: sel.mutators,
		gameplay: sel.gameplay,
		loadSave: path,
		addons:   plan.addons,
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

// saveSummaryDifficulty reads the selected save's recorded entry table, which
// fixes its AI plan/persona and parameters even after a rule switch. A
// continuation instead starts the next mission fresh under its recorded
// current selection (DESIGN_COMMUNITY_PATCH §4.8). Today's mounted mod,
// preferences and running battle never participate in this label.
// Without a sidecar the format alone establishes retail's index order; an
// unreadable sidecar leaves the label empty rather than guessing a tier.
func saveSummaryDifficulty(bankPath string, summary save.Summary) int32 {
	sc, ok, err := save.ReadSidecar(bankPath)
	if err != nil {
		return -1
	}
	if !ok {
		return summary.Difficulty
	}
	features := sc.Community.Entry
	// Load preflight branches on scalar presence, including a zero payload
	// [08 R-SAVE-02 §11]; the label must describe that same fresh entry.
	if summary.HasBetweenMissions {
		sel, err := resolveSidecarSelection(sc)
		if err != nil {
			return -1
		}
		features, err = session.ResolveCommunity(sel.gameplay, sel.sources)
		if err != nil {
			return -1
		}
	}
	return int32(session.DifficultyTier(features, int(summary.Difficulty)))
}

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
// when any are active, the mutators and the count of unit restrictions
// (§8.4). With loadSmartMakers, the Load dialog's player selection, the
// mutators are the set the load will run under: the recorded ones with Smart
// makers as selected, which a load follows instead of the record (§7.3). Nil,
// for the Save dialog, shows the recorded set. An installed mod is named as
// the Mods screen names it; one that is not installed by its id and version.
func saveSidecarLine(bankPath string, loadSmartMakers *content.Factor) string {
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
	line := name
	switch len(sc.Addons) {
	case 0:
	case 1:
		line += " + 1 add-on"
	default:
		line += fmt.Sprintf(" + %d add-ons", len(sc.Addons))
	}
	if m, err := content.ParseMutators(sc.Mutators); err == nil {
		if loadSmartMakers != nil {
			m.SmartMakers = *loadSmartMakers
		}
		if !m.IsZero() {
			line += " - " + mutatorSummary(m)
		}
	}
	if n := len(sc.Restrictions); n > 0 {
		line += " - " + restrictionCountText(n)
	}
	return line
}
