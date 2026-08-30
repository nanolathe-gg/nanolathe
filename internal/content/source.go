// Package content compiles retail's authored data into immutable definitions.
// This file carries the identity primitives every compiled definition shares;
// the family compilers land in phase 2.
package content

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/nanolathe/nanolathe/vfs"
)

// CanonicalKey folds a name the way retail's catalogs compare them:
// case-insensitively [02 §5]. It is the one key rule — every catalog map, every
// cross-reference and every hash input goes through it, so that lookups cannot
// disagree with sort order.
func CanonicalKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// boundedString models a fixed-width authored string buffer.  Retail copies
// bytes into the destination and leaves one byte for a terminator on the
// fields whose documented width is a C string; callers pass the usable width.
func boundedString(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		if maxBytes <= 0 {
			return ""
		}
		return value
	}
	return value[:maxBytes]
}

// Provenance records where a definition's bytes came from, for diagnostics.
type Provenance struct {
	LogicalPath string
	ProviderID  string
	MountOrder  int
}

// ProvenanceFrom adapts a VFS entry.
func ProvenanceFrom(info vfs.EntryInfo) Provenance {
	// ProviderID is the portable identity supplied by the VFS provider.  A
	// source path may be absolute (and therefore machine-specific), while the
	// provider identity is stable across equivalent mounts [02 §2].
	id := info.Source.ProviderID()
	if id == "" {
		id = info.Source.ProviderType
	}
	return Provenance{
		LogicalPath: info.Path,
		ProviderID:  id,
		MountOrder:  info.Source.MountOrder,
	}
}

// DefinitionHeader is the first field of every compiled definition. Definitions
// are immutable after compilation: there are no setters, and instances live in
// the simulation's pools instead.
type DefinitionHeader struct {
	CanonicalKey string
	Provenance   Provenance
	Hash         string
}

// HashDefinition produces the stable identity for a definition's canonical
// bytes. Callers must feed fields in a fixed order — never by ranging a map —
// so the hash is reproducible across runs (INVARIANTS I1).
func HashDefinition(canonicalBytes []byte) string {
	sum := sha256.Sum256(canonicalBytes)
	return hex.EncodeToString(sum[:])
}
