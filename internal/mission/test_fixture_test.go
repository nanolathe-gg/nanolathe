package mission

import (
	"encoding/binary"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type missionFixtureCOBFS struct{}

func (missionFixtureCOBFS) Open(string) (vfs.File, error) { return nil, vfs.ErrNotFound }
func (missionFixtureCOBFS) ReadFileLimit(name string, _ int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "scripts/") {
		return missionFixtureCOB(), nil
	}
	return nil, vfs.ErrNotFound
}
func (missionFixtureCOBFS) ReadDir(string) ([]vfs.EntryInfo, error) { return nil, vfs.ErrNotFound }
func (missionFixtureCOBFS) Stat(string) (vfs.EntryInfo, error) {
	return vfs.EntryInfo{}, vfs.ErrNotFound
}
func (missionFixtureCOBFS) CacheStamp(string) (string, error) { return "mission-fixture", nil }

// missionFixtureCOB is an authored test program whose Create script returns
// immediately. It keeps mission tests independent of retail COB assets while
// exercising the production strict-binding path [R-COB-04 §8] [fmt cob].
func missionFixtureCOB() []byte {
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

func newMissionFixtureWorld(maxDefs int, cat *content.Catalog) *units.World {
	w := units.NewSliced(maxDefs, cat)
	w.SetCOBSource(missionFixtureCOBFS{}, cob.NewCachedLoader())
	return w
}

// atPlacement stamps the index of the placement a fixture unit stands for and
// returns it, so a fixture can be written as one expression per unit.
//
// The retail spawner is two passes over a SPARSE created[] array [P0-06]: pass
// one calls the creator once per placement and stores the result at that
// placement's index, leaving a null hole where the pool or the per-player limit
// refused it; pass two walks that array and skips the holes. PlacementIdx is
// how a unit says which slot it occupies, and it is the only linkage the
// interpreter has — the `g`, `i` and `wa` verbs resolve a name to a placement
// index and then look for the unit standing there [04 §3.6].
//
// A fixture that creates units without stamping it is not exercising that
// scan. RunInitialMissionsWithCatalog used to carry a second, non-retail arm
// for exactly those fixtures — if nothing carried an index and the world's unit
// count happened to equal the placement count, it assumed dense creation order
// — which meant twenty tests were passing through a code path retail has no
// equivalent of.
func atPlacement(u *units.Unit, placement int) *units.Unit {
	if u != nil {
		u.PlacementIdx = placement
	}
	return u
}
