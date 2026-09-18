package ai

import (
	"encoding/binary"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
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

// aiFixtureOrderBinding is the queue binding a package-local fixture needs so
// that the command resolver can answer code 14's build-list gate, which reads
// the compiled `CANBUILD` page through the binding rather than from a catalog
// handle the order package does not hold [04 R-ORD-02 §1]. Production binds the
// session's own [internal/session/composition.go].
//
// The simulation stream goes in too, so that a fixture which pumps the queue
// sees the pump's own jitter draws instead of panicking on an uninjected
// stream [I4].
func aiFixtureOrderBinding(cat *content.Catalog, sim *rng.Simulation) *orders.QueueBinding {
	return &orders.QueueBinding{
		SimRNG: sim,
		// Code 14's gate is list presence — the `builder` flag — not the
		// entry count [04 R-ORD-02 §1]; the production binding answers the same.
		BuildList: func(def *content.UnitDef) bool {
			return def != nil && def.Builder
		},
	}
}
