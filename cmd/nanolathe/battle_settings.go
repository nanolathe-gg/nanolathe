package main

// Persisted preferences the battle reads and writes: damage bars, message
// lines and the shared palette [02 "Settings"] [07 §10].

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// loadedSettings reads the persisted block, ignoring a read failure the same
// way scrollSetting does: a preferences file that cannot be read yields the
// defaults rather than refusing to start the battle.
func loadedSettings() settings.Settings {
	s, _ := settings.Load()
	return s
}

// applyDamageBarsSetting installs bit 0 of the interface-flags word from the
// loaded block. Retail reads `damagebars` once at settings load
// [07 R-HUD-03 §7]; the bit's only consumer is the composer's label walk
// [03 R-FX-01 §6].
func applyDamageBarsSetting(s settings.Settings) {
	client.SetDamageBars(s.DamageBarsEnabled())
}

// applySwitchAltSetting captures the digit-key mux at battle install. The
// frontend shell owns its already-loaded preference copy; direct battles use
// the settings block read by installBattleClient. No input path reads disk
// [07 R-CAM-01 §4].
func (b *battleSession) applySwitchAltSetting(s settings.Settings) {
	if b == nil {
		return
	}
	if b.shell != nil {
		b.switchAlt = b.shell.switchAlt
		return
	}
	b.switchAlt = s.SwitchAltEnabled()
}

// applyClockSetting installs the persistent stand-alone clock bit and the
// active-FNT condition at battle entry. The retail composer selects COMIX
// through the message-column pass when textlines is nonzero; with that pass
// disabled the clock inherits the side console FNT [07 R-CAM-01 §6]
// [07 R-HUD-03 §14.4].
func (b *battleSession) applyClockSetting(s settings.Settings) {
	if b == nil {
		return
	}
	if b.shell != nil {
		b.clockVisible = b.shell.clockVisible
		b.clockUsePrimaryFont = b.shell.messages.TextLines != 0
		return
	}
	b.clockVisible = s.ClockEnabled()
	b.clockUsePrimaryFont = s.Messages.TextLines != 0
}

func (b *battleSession) clockShown() bool {
	if b == nil {
		return false
	}
	if b.shell != nil {
		return b.shell.clockVisible
	}
	return b.clockVisible
}

// applyInterfaceTypeSetting installs the direct-entry LEFTCLICK stage once at
// battle setup. A frontend-backed battle keeps the shell as the live owner, so
// interfaceTypeRightClick reads that in-memory value on each pointer event;
// neither route reads preferences from disk in the input path [07 R-CAM-01
// §5][07 R-CAM-01 §7].
func (b *battleSession) applyInterfaceTypeSetting(s settings.Settings) {
	if b == nil || b.shell != nil {
		return
	}
	b.interfaceType = s.InterfaceType
}

// interfaceTypeRightClick is the one input polarity gate. Only the two
// normalized settings stages are meaningful; an uninitialized test battle
// naturally retains the documented Type-0 default [07 R-CAM-01 §5].
func (b *battleSession) interfaceTypeRightClick() bool {
	if b == nil {
		return false
	}
	if b.shell != nil {
		return b.shell.interfaceType == settings.InterfaceTypeRightClick
	}
	return b.interfaceType == settings.InterfaceTypeRightClick
}

// applyMessageLineSettings installs the loaded block's message-column ring
// configuration onto the battle client. `textlines` is the ring's line
// budget and `textscroll` its line-age limit; both are read once at settings
// load, the same as `damagebars` [02 §3][07 R-HUD-03 §14.3]. `screenchat`
// sets the class filter the message column paints through [07 R-HUD-03
// §14.4]. Normalize (already run by settings.Load) guarantees non-negative
// values here.
func applyMessageLineSettings(cl *client.Client, s settings.Settings) {
	if cl == nil {
		return
	}
	m := s.Messages
	cl.ConfigureMessageLines(uint16(m.TextLines), uint16(m.TextScroll))
	cl.SetScreenChat(uint8(m.ScreenChat))
}

// damageBarsSettingValue is the live bit, in the form the persisted block
// stores it. The whole block is written from live state, so the value the
// frontend writes back has to come from the interface word, not from the file
// [07 R-HUD-03 §7].
func damageBarsSettingValue() int {
	if client.DamageBars() {
		return settings.InterfaceFlagDamageBars
	}
	return 0
}

// toggleDamageBars is the battle key command of [07 R-CAM-01 §2]: it flips bit
// 0 of the interface-flags word and writes every setting back immediately
// [07 R-HUD-03 §7]. A failed write costs the persistence, never the toggle.
func (b *battleSession) toggleDamageBars() {
	on := client.ToggleDamageBars()
	if err := settings.StoreDamageBars(on); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", err)
	}
}

func loadPaletteStrict(cs *contentSet) (*palette.Tables, error) {
	if cs == nil || cs.fs == nil {
		return nil, retailFrontendAssetError(cs, "retail palette", "palettes/PALETTE.PAL", "the shared retail palette tables", fmt.Errorf("missing VFS"))
	}
	p, err := palette.Load(cs.fs)
	if err != nil {
		return nil, retailFrontendAssetError(cs, "retail palette", "palettes/PALETTE.PAL", "the shared retail palette tables", err)
	}
	return p, nil
}
