package hud

import "fmt"

// FormatGameTime formats committed game ticks (30 Hz [01 §4.1]) as
// "hh:mm:ss" for the authored battle time readout [07 §6]. The battle rail
// slide itself is owned by ui.BattleState; this package retains only this
// stateless formatting helper for HUD composition.
func FormatGameTime(ticks int) string {
	if ticks < 0 {
		ticks = 0
	}
	secs := ticks / 30
	hh := secs / 3600
	mm := (secs % 3600) / 60
	ss := secs % 60
	return fmt.Sprintf("%02d:%02d:%02d", hh, mm, ss)
}
