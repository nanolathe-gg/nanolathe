package ai

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A forward reference can restore the higher slot first. The empty wave takes
// that first peer member, not the lowest slot [08 R-SAVE-02 §6][08 R-P0-04 §3].
func TestRestoredGroupOrderSelectsWaveBootstrap(t *testing.T) {
	def := &content.UnitDef{UnitName: "restored", MaxDamage: 100}
	w := newAIFixtureWorld(8, nil)
	first, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.Create(def, 0, numeric.FixedFromInt(300), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []pool.Handle{first, second} {
		w.Unit(h).Group = 3
		w.Unit(h).RestoredAIGroup = 3
	}
	m := &Manager{Player: 0}
	m.RestoreGroupsFromUnits([]*units.Unit{w.Unit(second), w.Unit(first)})
	m.mergeWaveGroupRecords(2, 3, w, 20000)
	if got := m.GroupMembers(2); !slices.Equal(got, []pool.Handle{second}) {
		t.Fatalf("wave bootstrap = %v, want restored first member %d", got, second)
	}
	if got := m.GroupMembers(3); !slices.Equal(got, []pool.Handle{first}) {
		t.Fatalf("remaining peer = %v, want distant member %d", got, first)
	}
}
