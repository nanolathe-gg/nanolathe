package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// targetBufferFixture reaches the service's ordinary registry acquisition path
// with a sized primary population. The registry is set directly so the test
// isolates the per-attempt query from the independently tested rebuild cadence.
func targetBufferFixture(t testing.TB, count int, secondary, invalid, stunned bool) (*Service, *units.World, *units.Unit, *content.Catalog) {
	t.Helper()
	cat := &content.Catalog{}
	w := newCombatFixtureWorld(max(1, count+1), cat)
	shooterDef := &content.UnitDef{UnitName: "shooter", MaxDamage: 100, Limit: -1}
	// Definition mask word 0 bit 1 is the authored bad-target membership;
	// bit 2 is preferred. The catalog pointer makes the production path use
	// these masks rather than the fixture-only legacy category word.
	shooterDef.BadTargetCategoryWPRIMask = content.MaskForID(1)
	prefDef := &content.UnitDef{UnitName: "preferred", MaxDamage: 100, Limit: -1}
	prefDef.UnitMask = content.MaskForID(2)
	fallbackDef := &content.UnitDef{UnitName: "fallback", MaxDamage: 100, Limit: -1}
	fallbackDef.UnitMask = content.MaskForID(1)

	shooterHandle, err := w.Create(shooterDef, 0, numeric.FixedFromInt(0), numeric.FixedFromInt(10), numeric.FixedFromInt(0))
	if err != nil {
		t.Fatal(err)
	}
	shooter := w.Unit(shooterHandle)
	weapon := &content.WeaponDef{ID: 1, Range: 10_000, Paralyzer: stunned}
	shooter.InstallWeapon(0, weapon)

	var handles []pool.Handle
	for i := 0; i < count; i++ {
		def := prefDef
		if i%3 == 0 {
			def = fallbackDef
		}
		x := numeric.FixedFromInt(int64(i%64 + 1))
		z := numeric.FixedFromInt(int64(i/64 + 1))
		if secondary && i == 0 {
			x = numeric.FixedFromInt(20_000) // leaves primary preliminary query empty
		}
		h, err := w.Create(def, 1, x, numeric.FixedFromInt(10), z)
		if err != nil {
			t.Fatal(err)
		}
		cand := w.Unit(h)
		if invalid {
			cand.Y = numeric.FixedFromInt(-1)
		}
		cand.Stunned = stunned
		handles = append(handles, h)
	}

	s := &Service{}
	if secondary {
		s.targets.primary[0] = handles[:1]
		s.targets.secondary[0] = handles[1:]
		s.targets.gate[0] = true
	} else {
		s.targets.primary[0] = handles
	}
	return s, w, shooter, cat
}

// referenceTargetBufferAcquisition is the public query applied to snapshots
// built in registry order. It is the allocation-heavy shape the service path
// used before this change and is retained only as a contract oracle for these
// tests; production must call acquireTargetForSlot.
func referenceTargetBufferAcquisition(s *Service, shooter *units.Unit, w *units.World, catalog *content.Catalog, sim *rng.Simulation) (pool.Handle, bool) {
	primary := make([]Candidate, 0, len(s.targets.primary[shooter.Owner]))
	for _, h := range s.targets.primary[shooter.Owner] {
		if cand := w.Unit(h); cand != nil && cand.Alive && !cand.Dying && cand.Handle != shooter.Handle {
			primary = append(primary, acquisitionCandidate(shooter, cand, 0, 0, catalog))
		}
	}
	acq := slotAcquisition(nil, shooter, shooter.SlotAt(0), 0, w, nil, nil, sim, catalog, 0, -1)
	acq.HasUpgrade = s.targets.gate[shooter.Owner]
	if acq.HasUpgrade {
		secondary := make([]Candidate, 0, len(s.targets.secondary[shooter.Owner]))
		for _, h := range s.targets.secondary[shooter.Owner] {
			if cand := w.Unit(h); cand != nil && cand.Alive && !cand.Dying && cand.Handle != shooter.Handle {
				secondary = append(secondary, acquisitionCandidate(shooter, cand, 0, 0, catalog))
			}
		}
		acq.Secondary = secondary
	}
	return AcquireTarget(primary, acq)
}

func TestTargetBufferServiceMatchesPublicQuery(t *testing.T) {
	cases := []struct {
		name                          string
		count                         int
		secondary, invalid, paralyzed bool
	}{
		{name: "preferred-fallback-49", count: 49},
		{name: "preferred-fallback-50", count: 50},
		{name: "preferred-fallback-51", count: 51},
		{name: "preferred-fallback-large", count: 400},
		{name: "secondary", count: 51, secondary: true},
		{name: "invalid-primary", count: 51, invalid: true},
		{name: "stunned-primary", count: 51, paralyzed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, w, shooter, catalog := targetBufferFixture(t, tc.count, tc.secondary, tc.invalid, tc.paralyzed)
			if tc.invalid || tc.paralyzed {
				// A rejected primary pick cannot open secondary fallback. Keep
				// a valid secondary target present to distinguish that boundary
				// from an empty secondary list [06 §3.1].
				def := &content.UnitDef{UnitName: "secondary", MaxDamage: 100, Limit: -1}
				h, err := w.Create(def, 1, numeric.FixedFromInt(1), numeric.FixedFromInt(10), numeric.FixedFromInt(1))
				if err != nil {
					t.Fatal(err)
				}
				s.targets.secondary[0] = []pool.Handle{h}
				s.targets.gate[0] = true
			}
			wantRNG := rng.NewSimulation(0x1234)
			want, wantOK := referenceTargetBufferAcquisition(s, shooter, w, catalog, &wantRNG)
			gotRNG := rng.NewSimulation(0x1234)
			got, gotOK := s.acquireTargetForSlot(shooter, shooter.SlotAt(0), 0, w, nil, nil, &gotRNG, nil, catalog)
			if (tc.invalid || tc.paralyzed) && gotOK {
				t.Fatal("rejected primary population incorrectly opened secondary fallback")
			}
			if got != want || gotOK != wantOK {
				t.Fatalf("target = %d,%v; want %d,%v", got, gotOK, want, wantOK)
			}
			if gotRNG.State != wantRNG.State || gotRNG.Draws() != wantRNG.Draws() {
				t.Fatalf("RNG = state %#x draws %d; want state %#x draws %d", gotRNG.State, gotRNG.Draws(), wantRNG.State, wantRNG.Draws())
			}
		})
	}
}

func BenchmarkTargetBufferService(b *testing.B) {
	for _, tc := range []struct {
		name      string
		count     int
		secondary bool
	}{
		{name: "population-49", count: 49},
		{name: "population-50", count: 50},
		{name: "population-51", count: 51},
		{name: "population-large", count: 400},
		{name: "dense-secondary", count: 400, secondary: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			s, w, shooter, catalog := targetBufferFixture(b, tc.count, tc.secondary, false, false)
			var got pool.Handle
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sim := rng.NewSimulation(uint32(i + 1))
				got, _ = s.acquireTargetForSlot(shooter, shooter.SlotAt(0), 0, w, nil, nil, &sim, nil, catalog)
			}
			if got == 0 {
				b.Fatal("fixture did not acquire a target")
			}
		})
	}
}
