package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/install"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// headlessMod is a mod the command line selected, resolved against the
// installed library.
type headlessMod struct {
	mod  modlibrary.Mod
	base []string // the resolved base install roots, without the mod
}

// resolveHeadlessMod applies --mod the way the desktop command does
// (docs/DESIGN_MODS_MUTATORS.md §4.3): `none` or no flag selects no mod;
// otherwise the installed mod is looked up, its base requirements (§4.2) are
// checked against the resolved base install, and it will be mounted as the
// last root. Several --root flags are a manual stack, which a mod cannot join.
// The settings file's saved mod is never read and nothing is fetched, so a run
// reproduces from its command line alone. roots are the --root flags as given.
func resolveHeadlessMod(selector string, roots []string) (*headlessMod, error) {
	id, _, err := modlibrary.ParseSelector(selector)
	if err != nil || id == "" {
		return nil, err
	}
	if len(roots) >= 2 {
		return nil, fmt.Errorf("nanolathe: mod selection conflicts with a manual root stack: logical path <command line>, providers searched [%s], expected either several --root flags or one --mod", strings.Join(roots, ", "))
	}
	base, err := install.Resolve(roots)
	if err != nil {
		return nil, err
	}
	libraryRoot, err := modlibrary.DefaultRoot()
	if err != nil {
		return nil, err
	}
	lib, err := modlibrary.Open(libraryRoot)
	if err != nil {
		return nil, err
	}
	mod, ok, err := lib.Select(selector, base)
	if err != nil || !ok {
		return nil, err
	}
	return &headlessMod{mod: mod, base: base}, nil
}

// selector names the mounted mod in the report, `<id>@<version>`.
func (m *headlessMod) selector() string { return m.mod.ID + "@" + m.mod.Version }

// roots is the mount order: the base install, then the mod, which wins.
func (m *headlessMod) roots() []string {
	return append(append([]string(nil), m.base...), m.mod.Dir)
}

// checkGameplay refuses a command line that names a gameplay mode below the
// mod's minimum (§4.3); a mode left to its default is never below one.
func (m *headlessMod) checkGameplay(mode gameplay.Mode) error {
	if m.mod.MinimumGameplay == "" {
		return nil
	}
	minimum, err := gameplay.Parse(m.mod.MinimumGameplay)
	if err != nil || gameplayStage(mode) >= gameplayStage(minimum) {
		return nil
	}
	return fmt.Errorf("nanolathe: gameplay mode is below the mod's minimum: logical path <command line>, providers searched [--gameplay, --mod], expected %s or a later mode for %s", minimum, m.mod.Name)
}

// gameplayStage orders the reserved bases Strict 3.1, Community 3.9, Modern,
// the order a mod's minimum is read in; a registered set takes its base's
// place (§4.3).
func gameplayStage(mode gameplay.Mode) int {
	switch session.BaseModeOf(mode) {
	case gameplay.Strict31:
		return 0
	case gameplay.Community39:
		return 1
	}
	return 2
}
