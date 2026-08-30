package combat

import (
	"encoding/binary"
	"strings"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
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

func newCombatFixtureWorld(maxDefs int, cat *content.Catalog) *units.World {
	w := units.NewSliced(maxDefs, cat)
	w.SetCOBSource(combatFixtureCOBFS{}, cob.NewCachedLoader())
	return w
}
