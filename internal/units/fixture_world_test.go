package units

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// fixtureCOBFS supplies one minimal authored Create program for unit tests.
// Tests therefore exercise the same configured source path as production
// without making scriptless allocation a special case in World.Create.
type fixtureCOBFS struct{}

func (fixtureCOBFS) Open(string) (vfs.File, error) { return nil, vfs.ErrNotFound }

func (fixtureCOBFS) ReadFileLimit(name string, _ int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "scripts/") {
		return unitCreateCOB(), nil
	}
	return nil, vfs.ErrNotFound
}

func (fixtureCOBFS) ReadDir(string) ([]vfs.EntryInfo, error) { return nil, vfs.ErrNotFound }
func (fixtureCOBFS) Stat(string) (vfs.EntryInfo, error)      { return vfs.EntryInfo{}, vfs.ErrNotFound }
func (fixtureCOBFS) CacheStamp(string) (string, error)       { return "fixture", nil }

func newFixtureWorld(maxDefs int, cat *content.Catalog) *World {
	w := NewSliced(maxDefs, cat)
	w.SetCOBSource(fixtureCOBFS{}, cob.NewCachedLoader())
	return w
}

func newFixtureWorldWithOrder(maxDefs int, cat *content.Catalog, order pool.PlayerPermutation) (*World, error) {
	w, err := NewSlicedWithOrder(maxDefs, cat, order)
	if err != nil {
		return nil, err
	}
	w.SetCOBSource(fixtureCOBFS{}, cob.NewCachedLoader())
	return w, nil
}
