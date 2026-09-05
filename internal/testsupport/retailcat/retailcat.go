package retailcat

import (
	"sync"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// Compiling the full retail catalog is the expensive part of every
// asset-gated test: it mounts the HPI set and builds the whole unit, weapon,
// feature, side and map corpus, costing hundreds of megabytes of peak
// resident memory. `go test ./...` runs one package binary per CPU, so a
// per-test compile multiplied across concurrent packages is what exhausted a
// 16 GB machine.
//
// The catalog is immutable once compiled and most tests only read it, so one
// compile per package binary is enough. This mirrors the cache
// internal/content has kept for its own retail tests.
//
// The mounted filesystem is kept alive for the process lifetime rather than
// closed per test, because the catalog holds handles into it.
var (
	once sync.Once
	cat  *content.Catalog
	fsys *vfs.FS
	err  error
)

// Shared returns the process-wide compiled retail catalog and the filesystem
// it was compiled from, skipping the test when the assets are absent.
//
// Callers MUST NOT mutate the returned catalog: every other test in the
// package observes the same pointer. A test that installs a fixture unit,
// overrides a COB or otherwise writes to the catalog must use Fresh.
//
// An installed but malformed corpus fails rather than skips, so a content
// regression is not hidden by substitution.
func Shared(t *testing.T) (*content.Catalog, *vfs.FS) {
	t.Helper()
	root := testsupport.RetailRoot(t)
	once.Do(func() {
		f := vfs.New()
		if mountErr := f.MountGameDirectory(root); mountErr != nil {
			err = mountErr
			return
		}
		c, compileErr := content.Compile(f)
		if compileErr != nil {
			err = compileErr
			_ = f.Close()
			return
		}
		cat, fsys = c, f
	})
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
	return cat, fsys
}
