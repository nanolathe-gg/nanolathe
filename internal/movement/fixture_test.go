package movement

import (
	"encoding/binary"
	"strings"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// movementFixtureCOBFS provides the smallest authored Create program needed
// by strict unit allocation. It is test-only source data; production never
// accepts a missing COB program.
type movementFixtureCOBFS struct{}

func (movementFixtureCOBFS) Open(string) (vfs.File, error) { return nil, vfs.ErrNotFound }
func (movementFixtureCOBFS) ReadFileLimit(name string, _ int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "scripts/") {
		return movementFixtureCOB(), nil
	}
	return nil, vfs.ErrNotFound
}

// setScratchMovement makes hand-built test definitions carry the same
// movement record that their System profile represents. Production compiled
// definitions already contain these fields from the FBI scratch record.
func setScratchMovement(def *content.UnitDef, p Profile) *content.UnitDef {
	if def == nil {
		return nil
	}
	def.FootprintX = int32(p.FootPrintX)
	def.FootprintZ = int32(p.FootPrintZ)
	def.MaxWaterDepth = p.MaxWaterDepth
	def.MinWaterDepth = p.MinWaterDepth
	def.MaxSlope = int32(p.MaxSlope)
	def.BadSlope = int32(p.BadSlope)
	def.MaxWaterSlope = int32(p.MaxWaterSlope)
	def.BadWaterSlope = int32(p.BadWaterSlope)
	return def
}
func (movementFixtureCOBFS) ReadDir(string) ([]vfs.EntryInfo, error) { return nil, vfs.ErrNotFound }
func (movementFixtureCOBFS) Stat(string) (vfs.EntryInfo, error) {
	return vfs.EntryInfo{}, vfs.ErrNotFound
}
func (movementFixtureCOBFS) CacheStamp(string) (string, error) { return "movement-fixture", nil }

func movementFixtureCOB() []byte {
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
	u32(headerSize+8, 0x10000000)
	copy(buf[headerSize+12:], "Create\x00")
	return buf
}

func newMovementFixtureWorld(maxDefs int) *units.World {
	w := units.NewSliced(maxDefs, nil)
	w.SetCOBSource(movementFixtureCOBFS{}, cob.NewCachedLoader())
	return w
}
