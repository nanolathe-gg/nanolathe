package vfs

import "strings"

// ResourcePath forms the resource request used by the retail unit compiler.
// Its extension replacement scans the whole assembled path, including directory
// components; it is not filepath.Ext semantics [02 R-CAT-01 §5]. Canonicalization
// and path validation remain the receiving filesystem's responsibility.
func ResourcePath(directory, name, extension string) string {
	logical := directory + "/" + name
	if dot := strings.LastIndexByte(logical, '.'); dot >= 0 {
		logical = logical[:dot]
	}
	return logical + "." + extension
}
