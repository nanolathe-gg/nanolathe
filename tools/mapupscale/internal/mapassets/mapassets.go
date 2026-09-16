// Package mapassets holds the asset-root, map-lookup and diagnostic helpers
// the map upscale research commands share. These are development tools, not
// engine code: nothing here is reachable from the simulation.
package mapassets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// DefaultRoot is the asset root a command uses when the operator names none:
// the NANOLATHE_TA_ROOT override, else TotalAnnihilation under the home
// directory.
func DefaultRoot() string {
	if configured := os.Getenv("NANOLATHE_TA_ROOT"); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "TotalAnnihilation"
	}
	return filepath.Join(home, "TotalAnnihilation")
}

// FindMap resolves a map named with or without its maps/ directory and .tnt
// extension to the logical path of the mounted entry, comparing case-folded.
func FindMap(fs *vfs.FS, requested string) (string, error) {
	wanted := strings.ToLower(strings.TrimSpace(requested))
	wanted = strings.TrimSuffix(strings.TrimPrefix(wanted, "maps/"), ".tnt")
	for _, entry := range fs.Entries() {
		logical := strings.ToLower(filepath.ToSlash(entry.Path))
		if entry.IsDir || !strings.HasPrefix(logical, "maps/") || !strings.HasSuffix(logical, ".tnt") {
			continue
		}
		if strings.TrimSuffix(strings.TrimPrefix(logical, "maps/"), ".tnt") == wanted {
			return entry.Path, nil
		}
	}
	return "", fmt.Errorf("map %q not found under maps/", requested)
}

// Fatalf writes one diagnostic under the calling command's prefix and exits.
func Fatalf(prefix, format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, prefix+format+"\n", arguments...)
	os.Exit(1)
}
