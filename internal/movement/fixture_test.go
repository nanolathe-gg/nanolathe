package movement

import (
	"encoding/binary"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
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
	// A definition that carries a movement record is a mobile unit: retail
	// allocates it a mover, and the sweep runs the mover tick and post-move
	// correction for it every tick [04 R-MOV-03 §1] step 9. Compiled content
	// authors `bmcode` for exactly these definitions; the fixtures omitted it,
	// which made every scratch mover a building-class record.
	def.BMCode = 1
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

// movementFixtureCOB is an authored two-script COB [fmt cob]: `Create`, whose
// body the fixture never needs, and `QueryLandingPad`, which answers pad pieces
// 0 and 1 in output cells 0 and 1 and leaves the other two at their -1 seed.
// Two candidates rather than one, so the fixture can exercise the scan's move
// to the next candidate when the first piece is occupied.
//
// The pad query is not decoration. `queryLandingPad` asks the TARGET's script
// for its candidates with all four outputs pre-seeded -1, so a pad whose script
// answers nothing offers no pad at all [04 R-AIR-01 §6][04 §5.3] — a scriptless
// fixture pad can never be landed on. These bytes are authored by us, not
// copied from any retail file.
func movementFixtureCOB() []byte {
	const (
		headerSize  = 44
		codeWords   = 10
		offIndex    = headerSize        // ScriptCodeIndexArray, 2 words
		offNameOffs = offIndex + 2*4    // ScriptNameOffsetArray, 2 words
		offCode     = offNameOffs + 2*4 // ScriptCode, codeWords words
		offNames    = offCode + codeWords*4
	)
	nameCreate := "Create\x00"
	nameQuery := "QueryLandingPad\x00"
	buf := make([]byte, offNames+len(nameCreate)+len(nameQuery))
	u32 := func(off int, value uint32) { binary.LittleEndian.PutUint32(buf[off:], value) }

	u32(0x00, 4) // VersionSignature
	u32(0x04, 2) // NumberOfScripts
	u32(0x08, 0) // NumberOfPieces
	u32(0x0c, codeWords)
	u32(0x10, 0) // NumberOfStatics
	u32(0x14, 0) // Always_0
	u32(0x18, offIndex)
	u32(0x1c, offNameOffs)
	u32(0x20, 0) // OffsetToPieceNameOffsetArray
	u32(0x24, offCode)
	u32(0x28, offNames)

	u32(offIndex, 0)   // Create starts at code word 0
	u32(offIndex+4, 1) // QueryLandingPad starts at code word 1
	u32(offNameOffs, offNames)
	u32(offNameOffs+4, offNames+uint32(len(nameCreate)))

	u32(offCode+0*4, 0x10000000) // Create body
	u32(offCode+1*4, 0x10021001) // push constant
	u32(offCode+2*4, 0)          //   the constant: pad piece 0
	u32(offCode+3*4, 0x10023002) // pop local
	u32(offCode+4*4, 0)          //   output cell 0
	u32(offCode+5*4, 0x10021001) // push constant
	u32(offCode+6*4, 1)          //   the constant: pad piece 1
	u32(offCode+7*4, 0x10023002) // pop local
	u32(offCode+8*4, 1)          //   output cell 1
	u32(offCode+9*4, 0x10065000) // return

	copy(buf[offNames:], nameCreate)
	copy(buf[offNames+len(nameCreate):], nameQuery)
	return buf
}

func newMovementFixtureWorld(maxDefs int) *units.World {
	w := units.NewSliced(maxDefs, nil)
	w.SetCOBSource(movementFixtureCOBFS{}, cob.NewCachedLoader())
	return w
}
