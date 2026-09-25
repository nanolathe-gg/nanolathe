package ebitenapp

import (
	"io/fs"

	"github.com/hajimehoshi/ebiten/v2"
)

// droppedPaths reads the real paths of what was dropped onto the window
// (docs/DESIGN_MODS_MUTATORS.md §4.5). Ebitengine offers the drop as a
// virtual file system whose root lists the dropped files and folders; on
// desktops each root entry reports its real path, which is what an install
// needs, because a folder is copied from disk and an archive is hashed from
// disk. An entry without a real path (a browser drop) is skipped, and nil is
// returned when nothing was dropped.
func droppedPaths(dropped fs.FS) []string {
	if dropped == nil {
		return nil
	}
	entries, err := fs.ReadDir(dropped, ".")
	if err != nil {
		return nil
	}
	var paths []string
	for _, entry := range entries {
		if real, ok := entry.(ebiten.AbsPather); ok && real.AbsPath() != "" {
			paths = append(paths, real.AbsPath())
		}
	}
	return paths
}
