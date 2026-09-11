package main

import (
	"io"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// This is an authored benchmark terrain, not a retail-behavior fixture.
// The tempting central area is a hillside; a broad northern plateau is flat.
func TestBenchmarkCentreAvoidsHillsideAndWater(t *testing.T) {
	terrain := &world.Terrain{CellW: 160, CellH: 256, PlayRight: 2528, PlayBottom: 3968, Plot: make([]world.PlotCell, 160*256)}
	for z := int32(0); z < terrain.CellH; z++ {
		for x := int32(0); x < terrain.CellW; x++ {
			h := uint8(50)
			if z > 105 {
				h += uint8((z - 105) / 2)
			}
			terrain.PlotAt(x, z).SetHeight(h)
		}
	}
	_, z, relief, err := benchmarkBattleCentre(terrain)
	if err != nil || z+520 >= 105*16 || relief != 0 {
		t.Fatalf("centre z=%d relief=%d err=%v; wanted the flat northern plateau", z, relief, err)
	}
	terrain.SeaLevel = 255
	if _, _, _, err := benchmarkBattleCentre(terrain); err == nil {
		t.Fatal("accepted an entirely submerged formation")
	}
}

func TestBattleBenchmarkLeadInFlags(t *testing.T) {
	opts, err := parseFlags([]string{"--battle-benchmark=/tmp/unused-battle"}, io.Discard)
	if err != nil || opts.Map != "great divide" || opts.BenchmarkPreTicks != 300 {
		t.Fatalf("defaults: map=%q pre=%d err=%v", opts.Map, opts.BenchmarkPreTicks, err)
	}
	opts, err = parseFlags([]string{"--battle-benchmark=/tmp/unused-battle", "--benchmark-pre-ticks=0"}, io.Discard)
	if err != nil || opts.BenchmarkPreTicks != 0 {
		t.Fatalf("opening capture: pre=%d err=%v", opts.BenchmarkPreTicks, err)
	}
	for _, value := range []string{"-1", "18001"} {
		if _, err := parseFlags([]string{"--battle-benchmark=/tmp/unused-battle", "--benchmark-pre-ticks=" + value}, io.Discard); err == nil {
			t.Fatalf("accepted pre-ticks=%s", value)
		}
	}
}
