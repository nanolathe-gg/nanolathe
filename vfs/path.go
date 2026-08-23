package vfs

import (
	"fmt"
	"strings"
)

// cleanPath returns the case-folded logical path used by the overlay.
// Retail HPI lookup splits on backslash only, treats '/' as a literal name
// character, has no special handling of '.' or '..', and scans entries
// last-to-first (02:164). That contract is preserved inside Archive
// indexing: entry names from the directory contain no separators, and
// duplicate entries are resolved by last-wins (hpi.go:338). The overlay
// intentionally accepts both slash styles and normalizes '.'/rejects '..'
// for host-filesystem ergonomics; no shipped archive contains '/' or a
// '.'/'..' entry, so the window is unexercised. Archive-internal
// directory de-obfuscation still uses the retail cipher (§02 HPI).
func cleanPath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || name == "." {
		return "", nil
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("vfs: absolute path %q is not allowed", name)
	}

	parts := make([]string, 0, strings.Count(name, "/")+1)
	for _, part := range strings.Split(name, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("vfs: parent path %q is not allowed", name)
		default:
			parts = append(parts, part)
		}
	}
	return strings.ToLower(strings.Join(parts, "/")), nil
}

func joinPath(parent, child string) (string, error) {
	if parent == "" {
		return cleanPath(child)
	}
	return cleanPath(parent + "/" + child)
}

func originalJoin(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "/" + child
}
