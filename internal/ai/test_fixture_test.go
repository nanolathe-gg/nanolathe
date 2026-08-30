package ai

import (
	"encoding/binary"
	"strings"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// aiFixtureCOBFS supplies the smallest authored Create program needed by AI
// tests that allocate synthetic units. Production remains strict when a unit's
// authored script is absent [R-COB-04 §8].
type aiFixtureCOBFS struct{}

func (aiFixtureCOBFS) Open(string) (vfs.File, error) { return nil, vfs.ErrNotFound }

func (aiFixtureCOBFS) ReadFileLimit(name string, _ int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "scripts/") {
		return aiFixtureCOB(), nil
	}
	return nil, vfs.ErrNotFound
}

func (aiFixtureCOBFS) ReadDir(string) ([]vfs.EntryInfo, error) { return nil, vfs.ErrNotFound }
func (aiFixtureCOBFS) Stat(string) (vfs.EntryInfo, error) {
	return vfs.EntryInfo{}, vfs.ErrNotFound
}
func (aiFixtureCOBFS) CacheStamp(string) (string, error) { return "ai-fixture", nil }

// aiFixtureCOB is an authored one-piece COB fixture whose Create entry point
// immediately returns [fmt cob].
func aiFixtureCOB() []byte {
	const (
		offCode          = 44
		offScriptIndex   = offCode + 4
		offScriptNames   = offScriptIndex + 4
		offPieceNames    = offScriptNames + 4
		stringsStart     = offPieceNames + 4
		createNameOffset = stringsStart
		pieceNameOffset  = stringsStart + len("Create") + 1
	)
	data := make([]byte, pieceNameOffset+len("base")+1)
	put := func(off int, value uint32) { binary.LittleEndian.PutUint32(data[off:], value) }
	put(0, 4)  // TA COB version signature [fmt cob].
	put(4, 1)  // One script entry point.
	put(8, 1)  // One model piece.
	put(12, 1) // One code word.
	put(24, offScriptIndex)
	put(28, offScriptNames)
	put(32, offPieceNames)
	put(36, offCode)
	put(40, uint32(createNameOffset))
	put(offCode, 0x10065000) // return [fmt cob].
	put(offScriptIndex, 0)
	put(offScriptNames, uint32(createNameOffset))
	put(offPieceNames, uint32(pieceNameOffset))
	copy(data[createNameOffset:], "Create\x00")
	copy(data[pieceNameOffset:], "base\x00")
	return data
}

func newAIFixtureWorld(maxDefs int, cat *content.Catalog) *units.World {
	w := units.NewSliced(maxDefs, cat)
	w.SetCOBSource(aiFixtureCOBFS{}, cob.NewCachedLoader())
	return w
}
