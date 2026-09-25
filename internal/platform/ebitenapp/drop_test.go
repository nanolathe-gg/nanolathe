package ebitenapp

import (
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// realEntry is a dropped root entry that knows its path on disk, as
// Ebitengine's desktop drop entries do.
type realEntry struct {
	fs.DirEntry
	path string
}

func (e realEntry) AbsPath() string { return e.path }

// dropFS lists a fixed set of root entries.
type dropFS struct {
	fstest.MapFS
	entries []fs.DirEntry
}

func (d dropFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == "." {
		return d.entries, nil
	}
	return d.MapFS.ReadDir(name)
}

// A drop reaches the shell once, as the real paths of its root entries; an
// entry with no real path is skipped, and the next service carries none.
func TestDroppedPathsReachTheShellOnce(t *testing.T) {
	plain, err := fs.ReadDir(fstest.MapFS{"pack.zip": {}, "virtual.zip": {}}, ".")
	if err != nil {
		t.Fatal(err)
	}
	drop := dropFS{entries: []fs.DirEntry{realEntry{plain[0], "/home/player/pack.zip"}, plain[1]}}
	if got := droppedPaths(drop); !reflect.DeepEqual(got, []string{"/home/player/pack.zip"}) {
		t.Fatalf("droppedPaths = %v", got)
	}
	if got := droppedPaths(nil); got != nil {
		t.Fatalf("droppedPaths(nil) = %v", got)
	}

	var buffer hostInputBuffer
	buffer.add(sampledInput{dropped: []string{"/a.zip"}})
	buffer.add(sampledInput{})
	state := input.NewState()
	applyInputWith(state, buffer.take(), &doubleClickRecognizer{})
	if !reflect.DeepEqual(state.DroppedPaths, []string{"/a.zip"}) {
		t.Fatalf("first service dropped = %v", state.DroppedPaths)
	}
	applyInputWith(state, buffer.take(), &doubleClickRecognizer{})
	if len(state.DroppedPaths) != 0 {
		t.Fatalf("the drop was delivered twice: %v", state.DroppedPaths)
	}
}
