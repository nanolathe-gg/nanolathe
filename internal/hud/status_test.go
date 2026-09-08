package hud

import (
	"math"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestSnapshotStatusSelectedBuildAndFactory(t *testing.T) {
	const builder = pool.Handle(7)
	f := frame.Frame{
		Units: []frame.UnitView{{Slot: builder, Owner: 2, Health: 13, MaxHealth: 100}},
		Builds: []frame.BuildProgressView{{
			Builder: builder, Product: 19, ProductKey: "armmex", Remaining: 0.375,
			Health: 25, MaxHealth: 100, Factory: true, QueueIndex: 0, Stalled: true,
		}},
		OrderQueues: []frame.OrderQueueView{{
			Unit: builder,
			Primary: []frame.OrderView{{Unit: builder, Kind: "Move_Ground", BuildProduct: "armmex"},
				{Unit: builder, Kind: "Build", BuildProduct: "armllt"}},
		}},
	}
	got := SnapshotStatus(&f, 2, []pool.Handle{builder})
	if !got.Selected.Present || got.Selected.Unit != builder {
		t.Fatalf("selected = %+v", got.Selected)
	}
	build := got.Selected.Construction
	if !build.Present || build.Product != 19 || build.ProductKey != "armmex" || build.Percent != 62 {
		t.Fatalf("construction = %+v, want 62%% arm mex", build)
	}
	if build.Health != 25 || build.MaxHealth != 100 || build.HealthPercent != 25 || !build.Stalled || !build.StalledKnown {
		t.Fatalf("construction health/stall = %+v", build)
	}
	factory := got.Selected.Factory
	if !factory.Present || factory.QueueCount != 2 || !factory.QueueCountKnown || len(factory.QueueProductKeys) != 2 {
		t.Fatalf("factory = %+v", factory)
	}
	if got.Selected.Order.Name != "Move_Ground" || got.Selected.Order.Label != "Moving" {
		t.Fatalf("order = %+v", got.Selected.Order)
	}
}

func TestHealthPercentUsesIntegerDamageBarArithmetic(t *testing.T) {
	// The unit readout's bar uses signed integer multiply/divide, so 53/100
	// remains 53. A float32 quotient can land just below 53 before truncation
	// [07 R-HUD-03 §2].
	f := frame.Frame{Units: []frame.UnitView{{Slot: 1, Owner: 0, Health: 53, MaxHealth: 100}}}
	got := SnapshotStatus(&f, 0, []pool.Handle{1})
	if got.Selected.HealthPercent != 53 {
		t.Fatalf("health percent = %d, want 53 [07 R-HUD-03 §2]", got.Selected.HealthPercent)
	}
}

func TestResourceStatusFormatting(t *testing.T) {
	f := frame.Frame{Economy: []frame.EconomyView{{
		Player: 3, Metal: 12.9, MetalCapacity: 100.9, Energy: 150000.9, EnergyCapacity: 250000.9,
		MetalProduced: 2.25, MetalConsumed: 1.5, EnergyProduced: 120001.9, EnergyConsumed: 100000.9,
	}}}
	got := ResourceStatusFor(&f, 3)
	if !got.Present || got.EnergyCurrent != "150000" || got.EnergyCapacity != "250000" || got.MetalCurrent != "12" || got.MetalCapacity != "100" {
		t.Fatalf("stocks = %+v", got)
	}
	if got.EnergyProduced.Text != "120K" || got.EnergyConsumed.Text != "100K" || got.MetalProduced.Text != "2.2" || got.MetalConsumed.Text != "1.5" {
		t.Fatalf("rates = %+v", got)
	}
	if got.EnergyProduced.Palette != PaletteProduction || got.MetalProduced.Palette != PaletteProduction || got.EnergyConsumed.Palette != PaletteConsumption || got.MetalConsumed.Palette != PaletteConsumption || got.EnergyZero.Palette != PaletteNormal {
		t.Fatalf("palette roles = %+v", got)
	}
	if got.Energy.Fraction != ResourceFraction(150000.9, 250000.9) || got.Metal.Fraction != ResourceFraction(12.9, 100.9) {
		t.Fatalf("bars = %+v", got)
	}
}

func TestSnapshotStatusAbsentAndUnknown(t *testing.T) {
	f := frame.Frame{Builds: []frame.BuildProgressView{{Builder: 4, QueueIndex: -1}}}
	got := SnapshotStatus(&f, 1, []pool.Handle{9})
	if got.Selected.Present || got.Resources.Present {
		t.Fatalf("absent status = %+v", got)
	}
	selected := SnapshotStatus(&f, 1, []pool.Handle{4}).Selected
	if !selected.Present || selected.Construction.StalledKnown || selected.Factory.QueueCountKnown || selected.Factory.QueueIndexKnown {
		t.Fatalf("unknown publication was inferred: %+v", selected)
	}
	if FormatEnergyRate(99999) != "99999" || FormatEnergyRate(100000) != "100K" || FormatEnergyRate(-100000) != "-100K" {
		t.Fatalf("energy boundary formatting")
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

func TestSnapshotStatusPublishedFrameIsImmutable(t *testing.T) {
	const builder = pool.Handle(3)
	producer := frame.Frame{
		Builds:      []frame.BuildProgressView{{Builder: builder, Product: 8, ProductKey: "armmex", Remaining: 0.5, Health: 40, MaxHealth: 80}},
		OrderQueues: []frame.OrderQueueView{{Unit: builder, Primary: []frame.OrderView{{Unit: builder, Kind: "Move_Ground", BuildProduct: "armmex"}}}},
		Economy:     []frame.EconomyView{{Player: 0, Metal: 2, MetalCapacity: 10}},
	}
	var buffer frame.Buffer
	w := buffer.BeginWrite()
	*w = producer
	_ = buffer.Publish(producer.Tick)
	// The writer must not mutate a committed slot. BeginWrite selects the
	// alternate slot, so changes there leave Current stable without cloning.
	next := buffer.BeginWrite()
	next.Builds = append(next.Builds, frame.BuildProgressView{Builder: builder, Product: 99, ProductKey: "mutated"})
	next.OrderQueues = append(next.OrderQueues, frame.OrderQueueView{Unit: builder, Primary: []frame.OrderView{{Kind: "mutated"}}})
	next.Economy = append(next.Economy, frame.EconomyView{Player: 0, Metal: 999})
	frame := buffer.Current()
	if frame == nil {
		t.Fatal("published frame unavailable")
	}
	got := SnapshotStatus(frame, 0, []pool.Handle{builder})
	if got.Selected.Construction.Product != 8 || got.Selected.Construction.ProductKey != "armmex" || got.Selected.Construction.Percent != 50 || got.Selected.Order.Name != "Move_Ground" {
		t.Fatalf("published status changed after producer mutation: %+v", got.Selected)
	}
	if got.Resources.Metal.Current != 2 {
		t.Fatalf("published resources changed after producer mutation: %+v", got.Resources)
	}
}

// The visible stock number is a signed 32-bit retail store, independent of host int size [01 R-DET-01 §1].
func TestCurrentResourceTextUsesStoredLowWord(t *testing.T) {
	if got := FormatCurrent(2147483648); got != "-2147483648" {
		t.Fatalf("stock text = %s", got)
	}
	if got := FormatCurrent(4294967296); got != "0" {
		t.Fatalf("wrapped stock text = %s", got)
	}
}
