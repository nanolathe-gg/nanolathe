package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

func TestSnapshotStatusSelectedBuildAndFactory(t *testing.T) {
	const builder = pool.Handle(7)
	f := snapshot.Frame{
		Units: []snapshot.UnitView{{Slot: builder, Owner: 2, Health: 13, MaxHealth: 100}},
		Builds: []snapshot.BuildProgressView{{
			Builder: builder, Product: 19, ProductKey: "armmex", Remaining: 0.375,
			Health: 25, MaxHealth: 100, Factory: true, QueueIndex: 0, Stalled: true,
		}},
		OrderQueues: []snapshot.OrderQueueView{{
			Unit: builder,
			Primary: []snapshot.OrderView{{Unit: builder, Kind: "Move_Ground", BuildProduct: "armmex"},
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

func TestResourceStatusFormatting(t *testing.T) {
	f := snapshot.Frame{Economy: []snapshot.EconomyView{{
		Player: 3, Metal: 12.9, MetalCapacity: 100.9, Energy: 150000.9, EnergyCapacity: 250000.9,
		MetalProduced: 2.25, MetalConsumed: 1.5, EnergyProduced: 120001.9, EnergyConsumed: 100000.9,
	}}}
	got := ResourceStatusFor(&f, 3)
	if !got.Present || got.EnergyCurrent != "150000" || got.EnergyCapacity != "250000" || got.MetalCurrent != "12" || got.MetalCapacity != "100" {
		t.Fatalf("stocks = %+v", got)
	}
	if got.EnergyProduced.Text != "120K" || got.EnergyConsumed.Text != "-100K" || got.MetalProduced.Text != "2.2" || got.MetalConsumed.Text != "-1.5" {
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
	f := snapshot.Frame{Builds: []snapshot.BuildProgressView{{Builder: 4, QueueIndex: -1}}}
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

func TestSnapshotStatusPublishedFrameIsImmutable(t *testing.T) {
	const builder = pool.Handle(3)
	producer := snapshot.Frame{
		Builds:      []snapshot.BuildProgressView{{Builder: builder, Product: 8, ProductKey: "armmex", Remaining: 0.5, Health: 40, MaxHealth: 80}},
		OrderQueues: []snapshot.OrderQueueView{{Unit: builder, Primary: []snapshot.OrderView{{Unit: builder, Kind: "Move_Ground", BuildProduct: "armmex"}}}},
		Economy:     []snapshot.EconomyView{{Player: 0, Metal: 2, MetalCapacity: 10}},
	}
	var buffer snapshot.Buffer
	buffer.Publish(&producer)
	producer.Builds[0].Product = 99
	producer.Builds[0].ProductKey = "mutated"
	producer.Builds[0].Remaining = 0
	producer.OrderQueues[0].Primary[0].Kind = "mutated"
	producer.Economy[0].Metal = 999
	_, frame, ok := buffer.Read()
	if !ok {
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
