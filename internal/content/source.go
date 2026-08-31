// Package content compiles retail's authored data into immutable definitions.
// This file carries the identity primitives every compiled definition shares;
// the family compilers land in phase 2.
package content

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/nanolathe/nanolathe/vfs"
)

// archiveMountOps is the optional source-selection surface implemented by the
// real overlay. Catalog families use it only to recover an archive entry when
// a loose file shadows the same logical path. Keeping this optional preserves
// the small FSOps fixture surface; fixtures identify their entries as archive
// content directly. [02 §2][02 R-CAT-01 §4]
type archiveMountOps interface {
	OpenMount(index int, name string) (vfs.File, error)
	MountCount() int
}

// archiveContentFile is a family-discovery result after the retail archive
// gate has been applied. The compiler must never parse a loose unit FBI or
// weapon TDF, even when it is the overlay winner.
type archiveContentFile struct {
	info vfs.EntryInfo
	data []byte
}

func isArchiveProvider(p vfs.Provenance) bool {
	// All HPI-family providers are indexed by vfs as "hpi". Do not treat an
	// unknown/empty provider as archive content: that would silently broaden
	// the retail gate for arbitrary FSOps implementations.
	return strings.EqualFold(p.ProviderType, "hpi")
}

// discoverArchiveContent enumerates one flat content family and applies the
// executable's archive-only gate. If a loose winner shadows an archive entry,
// the concrete overlay's mount surface is used to open the first archive
// provider in precedence order. A provider without that optional surface can
// still be used by tests when its returned EntryInfo is explicitly archive
// provenance; loose entries are simply ignored.
func discoverArchiveContent(fs vfs.FSOps, directory, suffix string) ([]archiveContentFile, error) {
	entries, err := fs.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	result := make([]archiveContentFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir || !strings.HasSuffix(strings.ToLower(entry.Path), suffix) {
			continue
		}
		if isArchiveProvider(entry.Source) {
			data, readErr := fs.ReadFileLimit(entry.Path, 1<<20)
			if readErr != nil {
				continue
			}
			result = append(result, archiveContentFile{info: entry, data: data})
			continue
		}

		// The overlay can expose a loose winner while the same logical path is
		// present in an archive. Recover that archive entry without changing
		// ordinary VFS precedence for any other family.
		mounts, ok := fs.(archiveMountOps)
		if !ok {
			continue
		}
		for index := 0; index < mounts.MountCount(); index++ {
			file, openErr := mounts.OpenMount(index, entry.Path)
			if openErr != nil {
				continue
			}
			info := file.Info()
			if !isArchiveProvider(info.Source) {
				_ = file.Close()
				continue
			}
			data, readErr := readArchiveFile(file, 1<<20)
			_ = file.Close()
			if readErr != nil {
				continue
			}
			result = append(result, archiveContentFile{info: info, data: data})
			break
		}
	}
	return result, nil
}

func readArchiveFile(file vfs.File, max int64) ([]byte, error) {
	if max < 0 {
		return io.ReadAll(file)
	}
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("content: archive entry exceeds read limit")
	}
	return data, nil
}

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
