package combat

import (
	"encoding/binary"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type combatFixtureCOBFS struct{}

func (combatFixtureCOBFS) Open(string) (vfs.File, error) { return nil, vfs.ErrNotFound }
func (combatFixtureCOBFS) ReadFileLimit(name string, _ int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "scripts/") {
		return combatFixtureCOB(), nil
	}
	return nil, vfs.ErrNotFound
}
func (combatFixtureCOBFS) ReadDir(string) ([]vfs.EntryInfo, error) { return nil, vfs.ErrNotFound }
func (combatFixtureCOBFS) Stat(string) (vfs.EntryInfo, error) {
	return vfs.EntryInfo{}, vfs.ErrNotFound
}
func (combatFixtureCOBFS) CacheStamp(string) (string, error) { return "combat-fixture", nil }

// combatFixtureCOB is an authored test program whose Create script returns
// immediately. It keeps combat tests independent of retail COB assets while
// exercising the production strict-binding path [R-COB-04 §8] [fmt cob].
func combatFixtureCOB() []byte {
	const headerSize = 44
	buf := make([]byte, 64)
	u32 := func(off, value uint32) { binary.LittleEndian.PutUint32(buf[off:], value) }
	u32(0x00, 4)
	u32(0x04, 1)
	u32(0x08, 0)
	u32(0x0c, 1)
	u32(0x10, 0)
	u32(0x14, 0)
	u32(0x18, headerSize)
	u32(0x1c, headerSize+4)
	u32(0x20, 0)
	u32(0x24, headerSize+8)
	u32(0x28, 0)
	u32(headerSize, 0)
	u32(headerSize+4, headerSize+12)
	u32(headerSize+8, 0x10065000) // RETURN [04 §4.3].
	copy(buf[headerSize+12:], "Create\x00")
	return buf
}

// wideDriftTolerance is an authored `tolerance` wide enough that the angular
// drift gate of [06 R-WPN-03 §2] admits any bearing a fixture's geometry can
// produce (the largest possible error is 32,768, the wrap of 0x8000).
//
// Fixtures whose subject is something other than aiming carry it so that the
// shot they are about still happens. Before WU-19-2 nothing read `tolerance`
// and every weapon fired regardless of alignment; a fixture that places its
// target off the shooter's heading and authors no tolerance is now correctly
// refused by the fixed-forward gate, which is retail behavior, not a
// regression. The gate itself is locked by TestDriftGate* in aim_test.go.
const wideDriftTolerance = 32767

func newCombatFixtureWorld(maxDefs int, cat *content.Catalog) *units.World {
	w := units.NewSliced(maxDefs, cat)
	w.SetCOBSource(combatFixtureCOBFS{}, cob.NewCachedLoader())
	return w
}

// primeTargetRegistry runs one registry rebuild for every side and returns the
// service, so a fixture that calls an acquisition directly reads the same
// candidate lists a stepped battle would [06 §3.1].
//
// A battle reaches the rebuild once per visited slot from the per-player
// phase, driven by that slot's strategic refresh gate, and nothing is
// acquirable before the first rebuild is due — that is the cadence, not a
// fixture accident — so a test that skips the phase must run it itself. The
// tick is the first at which `lastRebuild + 30 <= tick` holds from zero.
func primeTargetRegistry(s *Service, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service) *Service {
	rebuildEverySlot(s, targetRegistryPeriod, w, vis, terrain, econ)
	return s
}

// rebuildEverySlot stands in for the per-player phase's ascending slot walk:
// one RebuildTargetRegistryIfDue per slot, slots 0..9 ascending [06 §3.1] (I1).
// The session drives that walk from each slot's strategic refresh gate; a
// combat-only fixture owns no strategic state, so it drives the same entry
// point directly.
func rebuildEverySlot(s *Service, tick uint32, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service) {
	for slot := 0; slot < combatPlayerSlots; slot++ {
		s.RebuildTargetRegistryIfDue(tick, uint8(slot), w, vis, terrain, econ)
	}
}
