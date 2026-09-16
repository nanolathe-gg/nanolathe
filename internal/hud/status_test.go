package hud

import (
	"math"
	"strings"
	"testing"
)

// TestEnergyRateSuffixEdges locks the inclusive -99999..99999 window outside
// which the energy text takes the truncated K suffix [07 §6].
func TestEnergyRateSuffixEdges(t *testing.T) {
	if FormatEnergyRate(99999) != "99999" || FormatEnergyRate(100000) != "100K" || FormatEnergyRate(-100000) != "-100K" {
		t.Fatal("energy boundary formatting")
	}
}

func TestResourceRateFormattingUsesPanelSignForConsumption(t *testing.T) {
	negativeZero := float32(math.Copysign(0, -1))

	// These values mirror the authored resource text anchors: consumption is
	// a magnitude because the panel supplies the single minus glyph [07 §6].
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{name: "metal consumption at anchor", got: FormatMetalConsumed(8.7), want: "8.7"},
		{name: "metal negative input", got: FormatMetalConsumed(-8.7), want: "8.7"},
		{name: "metal zero", got: FormatMetalConsumed(0), want: "0.0"},
		{name: "metal negative zero", got: FormatMetalConsumed(negativeZero), want: "0.0"},
		{name: "energy consumption at anchor", got: FormatEnergyConsumed(42), want: "42"},
		{name: "energy negative input", got: FormatEnergyConsumed(-42), want: "42"},
		{name: "energy zero", got: FormatEnergyConsumed(0), want: "0"},
		{name: "energy negative zero", got: FormatEnergyConsumed(negativeZero), want: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
			if strings.Contains(tc.got, "--") {
				t.Fatalf("consumption text contains a doubled minus: %q", tc.got)
			}
		})
	}

	// The production forms remain signed/generic and retain their existing
	// precision and suffix behavior [07 §6].
	if got := FormatEnergyProduced(120001.9); got != "120K" {
		t.Fatalf("energy production changed: got %q", got)
	}
	if got := FormatMetalProduced(2.25); got != "2.2" {
		t.Fatalf("metal production changed: got %q", got)
	}
}

func TestEnergyRateSuffixBoundariesForProductionAndConsumption(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{name: "production maximum normal", got: FormatEnergyProduced(99999), want: "99999"},
		{name: "production first suffix", got: FormatEnergyProduced(100000), want: "100K"},
		{name: "production negative maximum normal", got: FormatEnergyProduced(-99999), want: "-99999"},
		{name: "production negative first suffix", got: FormatEnergyProduced(-100000), want: "-100K"},
		{name: "consumption maximum normal", got: FormatEnergyConsumed(99999), want: "99999"},
		{name: "consumption first suffix", got: FormatEnergyConsumed(100000), want: "100K"},
		{name: "consumption negative first suffix", got: FormatEnergyConsumed(-100000), want: "100K"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
