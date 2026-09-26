package aikit

import (
	"encoding/binary"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// fixtureWorld is a unit world whose units get the smallest authored
// script, a Create that returns at once; production stays strict about a
// missing script [R-COB-04 §8].
func fixtureWorld(cat *content.Catalog) *units.World {
	w := units.NewSliced(8, cat)
	w.SetCOBSource(fixtureCOBFS{}, cob.NewCachedLoader())
	return w
}

type fixtureCOBFS struct{}

func (fixtureCOBFS) Open(string) (vfs.File, error) { return nil, vfs.ErrNotFound }
func (fixtureCOBFS) ReadFileLimit(name string, _ int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "scripts/") {
		return fixtureCOB(), nil
	}
	return nil, vfs.ErrNotFound
}
func (fixtureCOBFS) ReadDir(string) ([]vfs.EntryInfo, error) { return nil, vfs.ErrNotFound }
func (fixtureCOBFS) Stat(string) (vfs.EntryInfo, error)      { return vfs.EntryInfo{}, vfs.ErrNotFound }
func (fixtureCOBFS) CacheStamp(string) (string, error)       { return "aikit-fixture", nil }

// fixtureCOB is a one-piece program whose Create entry point returns
// [fmt cob].
func fixtureCOB() []byte {
	const (
		offCode        = 44
		offScriptIndex = offCode + 4
		offScriptNames = offScriptIndex + 4
		offPieceNames  = offScriptNames + 4
		createName     = offPieceNames + 4
		pieceName      = createName + len("Create") + 1
	)
	data := make([]byte, pieceName+len("base")+1)
	put := func(off int, v uint32) { binary.LittleEndian.PutUint32(data[off:], v) }
	put(0, 4)  // version signature
	put(4, 1)  // one script
	put(8, 1)  // one piece
	put(12, 1) // one code word
	put(24, offScriptIndex)
	put(28, offScriptNames)
	put(32, offPieceNames)
	put(36, offCode)
	put(40, uint32(createName))
	put(offCode, 0x10065000) // return
	put(offScriptNames, uint32(createName))
	put(offPieceNames, uint32(pieceName))
	copy(data[createName:], "Create\x00")
	copy(data[pieceName:], "base\x00")
	return data
}
