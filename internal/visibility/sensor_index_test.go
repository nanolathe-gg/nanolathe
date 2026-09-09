package visibility

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestSensorCandidateIndexKeepsEveryExactVisitorCandidate exercises the
// index's proof boundary with fractional raw coordinates. The broad phase may
// return extras, but it must keep input order and may never omit a point the
// established inclusive visitor admits [R-VIS-01 §5].
func TestSensorCandidateIndexKeepsEveryExactVisitorCandidate(t *testing.T) {
	units := []SensorUnit{
		{X: numeric.FixedFromRaw(31<<16 | 3<<14), Z: numeric.FixedFromRaw(1 << 15)},
		{X: numeric.FixedFromRaw(64 << 16), Z: numeric.FixedFromRaw(1 << 15)},
		{X: numeric.FixedFromRaw(97 << 16), Z: numeric.FixedFromRaw(400 << 16)},
		{X: numeric.FixedFromRaw(400 << 16), Z: numeric.FixedFromRaw(97 << 16)},
	}
	var index sensorCandidateIndex
	index.rebuild(units)
	if !index.enabled {
		t.Fatal("ordinary bounded snapshot unexpectedly took exhaustive path")
	}
	if got := index.candidatesFor(&units[0], -1); got != nil {
		t.Fatalf("negative radius candidates = %v, want exhaustive fallback", got)
	}
	if got := index.candidatesFor(&units[0], 32768); got != nil {
		t.Fatalf("wrapped radius candidates = %v, want exhaustive fallback", got)
	}
	if got := index.candidatesFor(&units[0], 32767); len(got) != len(units) {
		t.Fatalf("largest bounded radius candidates = %v, want all input indexes", got)
	}
	for i := range units {
		got := index.candidatesFor(&units[i], 33)
		if got == nil {
			t.Fatal("ordinary bounded query unexpectedly took exhaustive path")
		}
		last := -1
		included := make([]bool, len(units))
		for _, j := range got {
			if j <= last {
				t.Fatalf("query %d indexes %v are not unique input order", i, got)
			}
			last = j
			included[j] = true
		}
		for j := range units {
			if planarSquared(&units[i], &units[j]) <= radiusSquared(33) && !included[j] {
				t.Fatalf("query %d omitted admitted index %d from %v", i, j, got)
			}
		}
	}
}

// TestSensorTickCandidateIndexMatchesExhaustive locks the optimization to the
// production verdict: statuses, decloak deadlines and completed snapshots are
// the observable output. The cases include fractional strict boundaries,
// wrapped coordinates and radii, owner/stealth/dead gates, and stale primary
// membership [R-VIS-01 §4–§5][06 §3.1].
func TestSensorTickCandidateIndexMatchesExhaustive(t *testing.T) {
	px := func(v int64) numeric.Fixed { return numeric.FixedFromRaw(v << 16) }
	cases := []struct {
		name  string
		units []SensorUnit
	}{
		{
			name: "fractional and stale membership",
			units: []SensorUnit{
				{ID: 1, Owner: 0, Alive: true, Active: true, X: numeric.FixedFromRaw(31<<16 | 3<<14), RadarDistance: 33, SonarDistance: 33},
				{ID: 2, Owner: 1, Alive: true, X: px(64), Stealth: false},
				{ID: 3, Owner: 1, Alive: true, Active: true, X: px(64), RadarJam: 33},
				{ID: 4, Owner: 1, Alive: true, CanCloak: true, OwnerLocallySimulated: true, X: numeric.FixedFromRaw(31<<16 | 3<<14), MinCloakDistance: 33},
				{ID: 5, Owner: 0, Alive: true, X: px(64), PrimaryCandidateOf: 1 << 1},
				{ID: 6, Owner: 0, Alive: true, X: px(64), PrimaryCandidateOf: 1 << 0}, // stale other-side list
				{ID: 7, Owner: 1, Alive: false, X: px(64)},
				{ID: 8, Owner: 1, Alive: true, X: px(64), Stealth: true},
			},
		},
		{
			name: "wrapped coordinates and radius",
			units: []SensorUnit{
				{ID: 1, Owner: 0, Alive: true, Active: true, X: numeric.FixedFromRaw(0), RadarDistance: 32768},
				{ID: 2, Owner: 1, Alive: true, X: numeric.FixedFromRaw(1 << 31)},
				{ID: 3, Owner: 1, Alive: true, Active: true, X: numeric.FixedFromRaw(-1 << 31), RadarJam: 32768},
				{ID: 4, Owner: 1, Alive: true, CanCloak: true, OwnerLocallySimulated: true, X: numeric.FixedFromRaw(0), MinCloakDistance: 32768},
				{ID: 5, Owner: 0, Alive: true, X: numeric.FixedFromRaw(1 << 31), PrimaryCandidateOf: 1 << 1},
			},
		},
		{
			name: "extreme raw coordinate span",
			units: []SensorUnit{
				{ID: 1, Owner: 0, Alive: true, Active: true, X: numeric.FixedFromRaw(-1 << 63), RadarDistance: 1},
				{ID: 2, Owner: 1, Alive: true, X: numeric.FixedFromRaw(1<<63 - 1)},
			},
		},
	}
	// Spread inert fillers across distinct cells so the fractional/stale case
	// exercises a real partial query rather than the compact fallback.
	for i := 0; i < 32; i++ {
		cases[0].units = append(cases[0].units, SensorUnit{
			ID:    uint16(20 + i),
			Owner: PlayerID(i % 2),
			Alive: true,
			X:     px(int64(512 + i*128)),
			Z:     px(int64(512 + (i%3)*128)),
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "fractional and stale membership" {
				var probe sensorCandidateIndex
				probe.rebuild(tc.units)
				if !probe.enabled {
					t.Fatal("fractional/stale fixture unexpectedly took exhaustive path")
				}
				if got := probe.candidatesFor(&tc.units[0], 33); len(got) == 0 || len(got) >= len(tc.units) {
					t.Fatalf("fractional/stale query candidates = %v, want proper subset of %d inputs", got, len(tc.units))
				}
			}
			indexed, indexedStatus, indexedDeadlines := cloneSensorUnits(tc.units)
			exhaustive, exhaustiveStatus, exhaustiveDeadlines := cloneSensorUnits(tc.units)
			fast := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
			baseline := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
			fast.SetLocal(0)
			baseline.SetLocal(0)
			baseline.sensorIndex.forceExhaustive = true
			fast.SensorTick(77, 2, indexed)
			baseline.SensorTick(77, 2, exhaustive)

			if !reflect.DeepEqual(indexedStatus, exhaustiveStatus) {
				t.Fatalf("status words = %#v, want exhaustive %#v", indexedStatus, exhaustiveStatus)
			}
			if !reflect.DeepEqual(indexedDeadlines, exhaustiveDeadlines) {
				t.Fatalf("decloak deadlines = %#v, want exhaustive %#v", indexedDeadlines, exhaustiveDeadlines)
			}
			if got, want := fast.SensorInputs(), baseline.SensorInputs(); !reflect.DeepEqual(got, want) {
				t.Fatalf("published inputs = %#v, want exhaustive %#v", got, want)
			}
		})
	}
}

// TestSensorTickCandidateIndexMultiTickMatchesExhaustive makes the indexed
// path prune a spread population, then compares four moving rebuilds against
// the unchanged exhaustive walk. IDs are deliberately unrelated to input
// order, which is the unit-slot order production supplies [R-VIS-01 §4–§5].
func TestSensorTickCandidateIndexMultiTickMatchesExhaustive(t *testing.T) {
	const n = 97
	seed := make([]SensorUnit, n)
	for i := range seed {
		owner := PlayerID((i*3 + 1) % 3)
		seed[i] = SensorUnit{
			ID:                    uint16((i*17)%n + 1),
			Owner:                 owner,
			Alive:                 i%19 != 0,
			Dying:                 i%23 == 0,
			Stealth:               i%17 == 0,
			Active:                i%3 != 0,
			CanCloak:              i%4 == 0,
			OwnerLocallySimulated: i%5 != 0,
			PrimaryCandidateOf:    1 << uint((i+1)%3),
			X:                     numeric.FixedFromRaw(int64((i%16)*256)<<16 | int64(i&3)<<14),
			Z:                     numeric.FixedFromRaw(int64((i/16)*256)<<16 | int64((i+1)&3)<<14),
			RadarDistance:         400,
			SonarDistance:         300,
			RadarJam:              180,
			SonarJam:              120,
			MinCloakDistance:      190,
		}
	}
	indexed, indexedStatus, indexedDeadlines := cloneSensorUnits(seed)
	exhaustive, exhaustiveStatus, exhaustiveDeadlines := cloneSensorUnits(seed)
	fast := newTestService(&world.Terrain{CellW: 256, CellH: 256}, ModeHistoryEnabled|ModeCurrentEnabled)
	baseline := newTestService(&world.Terrain{CellW: 256, CellH: 256}, ModeHistoryEnabled|ModeCurrentEnabled)
	baseline.sensorIndex.forceExhaustive = true

	for tick, local := range []PlayerID{0, 1, 2, 0} {
		mode := ModeHistoryEnabled | ModeCurrentEnabled
		if tick%2 != 0 {
			mode |= ModeTerrainRay
		}
		fast.SetLocal(local)
		baseline.SetLocal(local)
		fast.SetMode(mode)
		baseline.SetMode(mode)
		if tick != 0 {
			for i := range indexed {
				if i%3 == tick%3 {
					indexed[i].X = indexed[i].X.Add(numeric.FixedFromRaw(17 << 16))
					exhaustive[i].X = exhaustive[i].X.Add(numeric.FixedFromRaw(17 << 16))
				}
			}
		}
		fast.SensorTick(uint32(tick), 3, indexed)
		baseline.SensorTick(uint32(tick), 3, exhaustive)
		if !fast.sensorIndex.enabled {
			t.Fatalf("tick %d did not exercise the sparse indexed path", tick)
		}
		if got := fast.sensorIndex.candidatesFor(&indexed[0], 400); len(got) == 0 || len(got) >= len(indexed) {
			t.Fatalf("tick %d representative query candidates = %v, want proper subset of %d inputs", tick, got, len(indexed))
		}
		if !reflect.DeepEqual(indexedStatus, exhaustiveStatus) || !reflect.DeepEqual(indexedDeadlines, exhaustiveDeadlines) {
			t.Fatalf("tick %d status/deadline differs from exhaustive", tick)
		}
		if got, want := fast.SensorInputs(), baseline.SensorInputs(); !reflect.DeepEqual(got, want) {
			t.Fatalf("tick %d published inputs differ from exhaustive", tick)
		}
	}
}

// TestSensorCandidateIndexReusesScratch exercises grow, shrink and an unsafe
// wrapped snapshot before returning to a bounded population. The warm rebuild
// needs no allocation, and an unsafe frame must not poison the next one.
func TestSensorCandidateIndexReusesScratch(t *testing.T) {
	units := make([]SensorUnit, 97)
	for i := range units {
		units[i].X = numeric.FixedFromRaw(int64((i%16)*256) << 16)
		units[i].Z = numeric.FixedFromRaw(int64((i/16)*256) << 16)
	}
	var index sensorCandidateIndex
	index.rebuild(units)
	if !index.enabled {
		t.Fatal("initial bounded index disabled")
	}
	headCap, nextCap, candidateCap := cap(index.heads), cap(index.next), cap(index.candidates)
	index.rebuild(units[:13])
	index.rebuild(units)
	if !index.enabled || cap(index.heads) != headCap || cap(index.next) != nextCap || cap(index.candidates) != candidateCap {
		t.Fatal("grow/shrink rebuild did not reuse scratch")
	}
	extreme := []SensorUnit{{X: numeric.FixedFromRaw(-1 << 63)}, {X: numeric.FixedFromRaw(1<<63 - 1)}}
	index.rebuild(extreme)
	if index.enabled {
		t.Fatal("overflowing span enabled geometric index")
	}
	index.rebuild(units)
	if !index.enabled {
		t.Fatal("bounded rebuild after fallback remained disabled")
	}
	if got := testing.AllocsPerRun(100, func() { index.rebuild(units) }); got != 0 {
		t.Fatalf("warm rebuild allocations = %v, want 0", got)
	}
}

func cloneSensorUnits(source []SensorUnit) ([]SensorUnit, []uint32, []uint32) {
	units := append([]SensorUnit(nil), source...)
	statuses := make([]uint32, len(units))
	deadlines := make([]uint32, len(units))
	for i := range units {
		units[i].Status = &statuses[i]
		units[i].DecloakDeadline = &deadlines[i]
	}
	return units, statuses, deadlines
}
