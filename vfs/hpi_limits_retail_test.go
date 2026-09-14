package vfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// The installed corpus must remain below the host limits [I11]. Log the
// observed headroom, without treating a particular install census as a rule.
func TestArchiveDirectoryLimitsRetail(t *testing.T) {
	root := testsupport.RetailRoot(t)
	files, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	options := ArchiveOptions{}.withDefaults()
	archives := 0
	for _, file := range files {
		switch strings.ToLower(filepath.Ext(file.Name())) {
		case ".hpi", ".ufo", ".ccx", ".gp3", ".gp4", ".gpf", ".swx":
		default:
			continue
		}
		archive, err := OpenArchive(filepath.Join(root, file.Name()), ArchiveOptions{})
		if err != nil {
			t.Fatalf("%s: %v", file.Name(), err)
		}
		archives++
		depth := 1
		var stringWork int64
		for _, entry := range archive.indexedEntries {
			// Shipped names are single components and need no path normalization;
			// verify this before deriving the pre-normalization byte budget.
			if entry.Name == "." || entry.Name == ".." || strings.ContainsAny(entry.Name, "/\\") {
				t.Fatalf("%s: census needs raw path accounting for name %q", file.Name(), entry.Name)
			}
			stringWork += 2*(int64(len(entry.Name))+1) + int64(len(entry.Path)) + int64(len(entry.OriginalPath))
			if entry.IsDir {
				depth = max(depth, strings.Count(entry.OriginalPath, "/")+2)
			}
		}
		count := len(archive.indexedEntries)
		if int64(count) > options.MaxDirectoryEntries || depth > options.MaxDirectoryDepth || stringWork > options.MaxDirectoryStringBytes {
			t.Fatalf("%s exceeds host limits: entries=%d depth=%d string bytes=%d", file.Name(), count, depth, stringWork)
		}
		t.Logf("%s: entries=%d depth=%d string bytes=%d", file.Name(), count, depth, stringWork)
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if archives == 0 {
		t.Fatal("no archives checked")
	}
	t.Logf("accepted %d archives with default expansion limits", archives)
}
