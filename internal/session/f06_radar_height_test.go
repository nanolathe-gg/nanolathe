package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Established: the sensor adapter carries the full catalog bound, while LOS
// retains its own byte consumer [03 R-VIS-01 §5][03 R-P0-18-A §1].
func TestSensorAdapterKeepsFullModelTop(t *testing.T) {
	const sea = 100 << 16
	cases := []struct {
		name      string
		y, top    int32
		seen      bool
		losHeight uint8
	}{
		{"fractional crossing", sea - (10<<16 | 1<<14), 10<<16 | 1<<15, true, 111},
		{"equality", sea - (10<<16 | 1<<15), 10<<16 | 1<<15, true, 111},
		{"below", sea - (10<<16 | 1<<15) - 1, 10<<16 | 1<<15, false, 111},
		{"above byte range", sea - (266<<16 | 1<<14), 266<<16 | 1<<15, true, 111},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newSessionFixtureWorld(4, nil)
			radar := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "radar"}, MaxDamage: 1, RadarDistance: 100}
			target := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "target"}, MaxDamage: 1, ModelTopFixed: tc.top, ModelTop: int32(uint8(tc.top >> 16))}
			sourceID, err := w.Create(radar, 0, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			targetID, err := w.Create(target, 1, 32<<16, numeric.Fixed(tc.y), 0)
			if err != nil {
				t.Fatal(err)
			}
			w.Unit(sourceID).Activated = true
			u := w.Unit(targetID)
			u.Y = numeric.Fixed(tc.y)
			u.Hidden = true // skip the final direct-LOS probe; radar ignores cloak
			terrain := &world.Terrain{CellW: 64, CellH: 64, SeaLevel: 100}
			vis := visibility.New(terrain, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
			vis.SetLocal(0)
			s := &Session{Units: w, World: terrain, Vis: vis, Econ: economyForTest()}
			s.Econ.Players[0].Exists = true
			s.Econ.Players[1].Exists = true
			s.stepSensorPhase(1)
			want := uint32(0)
			if tc.seen {
				want = visibility.SeenBit
			}
			if got := u.Flags & (visibility.FriendlyMask | visibility.JammedBit); got != want {
				t.Fatalf("sensor status = %#x, want radar-only %#x", got, want)
			}
			if got := heightByteAt(u, 100); got != tc.losHeight {
				t.Fatalf("LOS emitter height = %d, want %d", got, tc.losHeight)
			}
		})
	}
}
