package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
)

// guardSkipNames are directory names that never contain module packages:
// version-control metadata, nested agent worktrees, authored test fixtures,
// and vendor trees.
var guardSkipNames = map[string]bool{
	".git":         true,
	".worktrees":   true,
	"worktrees":    true,
	"testdata":     true,
	"node_modules": true,
}

// guardWalkDir walks root like filepath.WalkDir but never descends into
// anything outside this module's source tree: any directory named in
// guardSkipNames, and any nested repository root — a directory containing a
// .git entry, the marker git leaves for every linked worktree. A repository
// checkout may hold nested worktrees with other agents' live, uncommitted
// code; filesystem walks must match what the Go module actually builds, so
// the guards must scan only packages of THIS module.
//
// The walk root itself is exempt from the .git-entry rule: this checkout's
// own .git entry marks the module under scan, not a nested checkout.
func guardWalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != root {
			if guardSkipNames[entry.Name()] {
				return filepath.SkipDir
			}
			if _, gitErr := os.Lstat(filepath.Join(path, ".git")); gitErr == nil {
				return filepath.SkipDir
			}
		}
		return fn(path, entry, nil)
	})
}
