package hud

// Rate and stock text for the battle HUD's resource strip.
//
// These formatters consume only values the caller has already read out of the
// immutable presentation frame; they retain no pointer into a session, economy
// service, unit pool, or order queue.

import (
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// HUD palette roles are logical GUI entries, not physical palette indices.
// [07 §6] says normal text uses 15, production uses 10, and consumption uses
// 12; the active palette maps those entries at presentation time.
const (
	PaletteNormal      uint8 = 15
	PaletteProduction  uint8 = 10
	PaletteConsumption uint8 = 12
)

// FormatEnergyRate formats a signed energy value as an integer, with a
// truncated integer K suffix outside the inclusive -99999..99999 range
// [07 §6]. It is retained as the generic signed primitive; callers should use
// FormatEnergyProduced or FormatEnergyConsumed when the value's role is known.
func FormatEnergyRate(value float32) string {
	if value > 99999 || value < -99999 {
		return fmt.Sprintf("%dK", int(numeric.TruncateFloat32ToLow32(value/1000)))
	}
	return fmt.Sprintf("%d", int(numeric.TruncateFloat32ToLow32(value)))
}

// FormatEnergyProduced formats the produced amount. Production retains the
// signed input because the energy formatter's normal and suffix forms are
// signed [07 §6].
func FormatEnergyProduced(amount float32) string { return FormatEnergyRate(amount) }

// FormatEnergyConsumed formats a consumed magnitude. The panel artwork owns
// the minus sign, so this text never receives one, including for a negative
// or negative-zero source value [07 §6].
func FormatEnergyConsumed(magnitude float32) string {
	return FormatEnergyRate(abs32(magnitude))
}

// FormatMetalRate formats a signed metal value with one fractional digit
// [07 §6]. It remains the generic signed primitive for compatibility with
// callers that do not yet have a resource role.
func FormatMetalRate(value float32) string {
	return fmt.Sprintf("%.1f", float64(value))
}

// FormatMetalProduced formats the produced amount with one fractional digit
// [07 §6].
func FormatMetalProduced(amount float32) string { return FormatMetalRate(amount) }

// FormatMetalConsumed formats a consumed magnitude with one fractional digit.
// The panel artwork owns the minus sign, so negative input is normalized to a
// positive magnitude, including negative zero [07 §6].
func FormatMetalConsumed(magnitude float32) string {
	return FormatMetalRate(abs32(magnitude))
}

func abs32(v float32) float32 {
	if math.IsNaN(float64(v)) || v == 0 {
		return 0
	}
	if v < 0 {
		return -v
	}
	return v
}
