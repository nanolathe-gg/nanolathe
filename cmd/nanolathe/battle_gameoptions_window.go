package main

// `GAMEOPTIONS.GUI`, the read-only in-battle game-settings overlay ARMOPT's
// `MISSION` opens outside a campaign [07 R-FE-01 §7][08 R-SKIR-01 §11].
//
// The window authors only `OK`; every row is a pair of appended labels — the
// name in a column at x 18 of width 110 and the value in a column at x 140 of
// width 120, the first pair at y 90 and each next pair 18 lower. The rows are
// presentation only: the opener reads the live words and writes none.
// Multiplayer's `Cheat Codes` and `Watching` rows are out of scope, so the
// single-player order is Commander Death, Starting Locations, Mapping Mode,
// Line of Sight, Difficulty, Map, Starting Metal, Starting Energy, Max Units.

import (
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// The overlay's row geometry [07 R-FE-01 §7].
const (
	gameOptionsNameX  = 18
	gameOptionsNameW  = 110
	gameOptionsValueX = 140
	gameOptionsValueW = 120
	gameOptionsFirstY = 90
	gameOptionsStepY  = 18
)

// The label vocabulary the overlay prints. These are the names the executable
// selects; each one is localised through the translation table before it is
// stored in the label [07 R-FE-01 §7][08 R-SKIR-01 §11][03 R-VIS-01 §1].
var (
	gameOptionsCommanderDeath = [3]string{"Game Continues", "Game Ends", "Deathmatch"}
	gameOptionsStartLocation  = [2]string{"Random", "Fixed"}
	gameOptionsMapping        = [2]string{"Mapped", "Unmapped"}
	gameOptionsDifficulty     = [3]string{"Easy", "Medium", "Hard"}
)

// battleGameOptionsRow is one printed name/value pair.
type battleGameOptionsRow struct {
	Name  string
	Value string
}

func gameOptionsLabel(table []string, value int) string {
	if value < 0 || value >= len(table) {
		// Every writer of these words clamps to the authored set before the
		// battle starts; an out-of-range word has no authored label, so the
		// row prints the first one rather than inventing a name.
		value = 0
	}
	return table[value]
}

// battleGameOptionsLineOfSight names the mode word's two line-of-sight bits:
// bit 1 clear is `Permanent` whatever bit 2 holds, bit 1 set with bit 2 set is
// `True`, and bit 1 set with bit 2 clear is `Circular` [03 R-VIS-01 §1]
// [08 R-SKIR-01 §11].
func battleGameOptionsLineOfSight(mode visibility.Mode) string {
	if mode&visibility.ModeCurrentEnabled == 0 {
		return "Permanent"
	}
	if mode&visibility.ModeTerrainRay != 0 {
		return "True"
	}
	return "Circular"
}

// battleGameOptionsRows reads the live session words the overlay prints.
func battleGameOptionsRows(b *battleSession) []battleGameOptionsRow {
	if b == nil || b.sess == nil {
		return nil
	}
	cfg := b.sess.Skirmish
	var mode visibility.Mode
	if b.sess.Vis != nil {
		mode = b.sess.Vis.Mode()
	}
	location := 0
	if cfg.Location != 0 {
		location = 1
	}
	mapping := 0
	if mode&visibility.ModeHistoryEnabled != 0 {
		mapping = 1
	}
	metal, energy := 0, 0
	if slot := int(b.sess.LocalOwner); slot >= 0 && slot < len(cfg.Players) {
		metal, energy = cfg.Players[slot].Metal, cfg.Players[slot].Energy
	}
	// `Max Units` is the session's own unit-limit word, which the committed
	// tick already publishes for the slide strip [08 R-SKIR-01 §6]
	// [07 R-HUD-03 §12].
	maxUnits := 0
	if cur, ok := b.currentSnapshot(); ok {
		maxUnits = int(cur.Strip.UnitLimit)
	}
	return []battleGameOptionsRow{
		{"Commander Death:", gameOptionsLabel(gameOptionsCommanderDeath[:], cfg.CommanderDeath)},
		{"Starting Locations:", gameOptionsLabel(gameOptionsStartLocation[:], location)},
		{"Mapping Mode:", gameOptionsLabel(gameOptionsMapping[:], mapping)},
		{"Line of Sight:", battleGameOptionsLineOfSight(mode)},
		{"Difficulty:", gameOptionsLabel(gameOptionsDifficulty[:], cfg.Difficulty)},
		{"Map:", cfg.MapName},
		{"Starting Metal:", strconv.Itoa(metal)},
		{"Starting Energy:", strconv.Itoa(energy)},
		{"Max Units:", strconv.Itoa(maxUnits)},
	}
}

// openBattleGameOptionsWindow appends the rows onto the authored record and
// installs the window's retained widget state. The row values are read once,
// at open: the overlay never refreshes them [08 R-SKIR-01 §11].
func (b *battleSession) openBattleGameOptionsWindow() {
	if b == nil || b.hud == nil {
		return
	}
	h := b.hud
	window := h.info.gameOptionsWin
	if window == nil {
		return
	}
	truncateBattleInfoWindow(window, h.info.gameOptionsAuthored)
	captions := hudCaptionTranslator(h)
	translate := func(text string) string {
		if captions == nil {
			return text
		}
		return captions.Translate(text)
	}
	y := gameOptionsFirstY
	for _, row := range battleGameOptionsRows(b) {
		appendBattleInfoLabel(window, translate(row.Name), gameOptionsNameX, y, gameOptionsNameW)
		// The numeric rows are printed through a radix-10 conversion and are
		// not looked up in the translation table; the named ones are
		// [07 R-FE-01 §7].
		value := row.Value
		if !battleGameOptionsNumericRow(row.Name) {
			value = translate(value)
		}
		appendBattleInfoLabel(window, value, gameOptionsValueX, y, gameOptionsValueW)
		y += gameOptionsStepY
	}
	h.installWindow(window, nil)
	h.applyBattleInfoPlacement(window, true)
	h.info.gameOptionsPanel = ui.NewPanel(window)
}

// battleGameOptionsNumericRow reports the three rows whose value is a decimal
// conversion rather than a localised name [07 R-FE-01 §7].
func battleGameOptionsNumericRow(name string) bool {
	switch name {
	case "Starting Metal:", "Starting Energy:", "Max Units:":
		return true
	}
	return false
}

// applyBattleInfoPlacement re-places a child that carries the centring flag.
// `GAMEOPTIONS` and `HELP` do; the in-battle briefing opens with no flags and
// keeps its authored origin [07 R-FE-01 §7].
func (h *retailBattleHUD) applyBattleInfoPlacement(window *gui.Window, centred bool) {
	if h == nil || window == nil || !centred {
		return
	}
	placeBattleModal(window, int(h.screenW), int(h.screenH))
}
