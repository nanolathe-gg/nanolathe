// The identity primitives every compiled definition shares.

package content

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// archiveContentFile preserves the union enumerator's winning entry and its
// bytes. The unit compiler needs loose winners through its parse-before-drop
// gate; weapon discovery filters them before parsing [02 R-CAT-01 §4].
type archiveContentFile struct {
	info    vfs.EntryInfo
	archive bool
}

func isArchiveProvider(p vfs.Provenance) bool {
	// All HPI-family providers are indexed by vfs as "hpi". Do not treat an
	// unknown/empty provider as archive content: that would silently broaden
	// the retail gate for arbitrary FSOps implementations.
	return strings.EqualFold(p.ProviderType, "hpi")
}

// discoverArchiveContent enumerates archive-backed winning entries only. A
// loose winner is never replaced with a shadowed archive entry [02 R-CAT-01 §1].
func discoverArchiveContent(fs vfs.FSOps, directory, suffix string) ([]archiveContentFile, error) {
	return discoverEntries(fs, directory, suffix, true)
}

// discoverUnitContent preserves every winning FBI so CompileUnits can parse a
// loose winner before applying its silent compatibility drop [02 R-CAT-01 §4].
func discoverUnitContent(fs vfs.FSOps) ([]archiveContentFile, error) {
	return discoverEntries(fs, "units", ".fbi", false)
}

func discoverEntries(fs vfs.FSOps, directory, suffix string, archiveOnly bool) ([]archiveContentFile, error) {
	entries, err := fs.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	result := make([]archiveContentFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir || !strings.HasSuffix(asciiFoldContent(entry.Path), suffix) {
			continue
		}
		archive := isArchiveProvider(entry.Source)
		if archiveOnly && !archive {
			continue
		}
		result = append(result, archiveContentFile{info: entry, archive: archive})
	}
	return result, nil
}

func readContentEntry(fs vfs.FSOps, entry archiveContentFile) ([]byte, error) {
	data, err := fs.ReadFileLimit(entry.info.Path, 1<<20)
	if err != nil {
		provider := entry.info.Source.ProviderID()
		if provider == "" {
			provider = "unknown"
		}
		return nil, fmt.Errorf("nanolathe: content entry read: logical path %s, providers searched [%s], expected readable content definition: %w", entry.info.Path, provider, portableContentCause(err))
	}
	return data, nil
}

// sortedFoldedKeys collects a map's keys and orders them by the caller's fold,
// breaking a fold-equal tie with the raw key so the comparison is a total
// order and the iteration is deterministic (I1). The fold differs per record:
// the catalog key fold trims first, the unit record folds the authored key as
// parsed.
func sortedFoldedKeys[V any](values map[string]V, fold func(string) string) []string {
	if values == nil {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		li, lj := fold(keys[i]), fold(keys[j])
		if li != lj {
			return li < lj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// asciiFoldContent is the catalog's name for the shared A..Z fold. Bytes at
// or above 0x80 stay literal until the retail code-page rule is traced
// [02 R-CAT-01 §3].
func asciiFoldContent(value string) string { return formats.FoldASCII(value) }

// CanonicalKey folds the established ASCII domain and trims only the four TDF
// semantic whitespace bytes. Bytes at or above 0x80 remain literal until the
// retail code-page rule is traced [02 R-CAT-01 §3].
// TODO(question): trace retail's active code-page comparison for bytes >= 0x80.
func CanonicalKey(name string) string {
	return asciiFoldContent(trimTDFSemantic(name))
}

// trimTDFSemantic is for an authored TDF semantic value or name. It accepts
// only the four parser whitespace bytes [02 R-CAT-01 §3].
func trimTDFSemantic(name string) string { return formats.TrimASCIIFunc(name, contentTDFSpace) }

func contentTDFSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

// contentASCIIFields is the C-runtime isspace token scan used by category and
// AI directive grammars. Unlike TDF semantic trimming it includes form-feed
// and vertical-tab, while preserving all high bytes [02 R-P0-03 §3][08 R-AI-01 §20].
func contentASCIIFields(value string) []string {
	var fields []string
	for i := 0; i < len(value); {
		for i < len(value) && contentCWhitespace(value[i]) {
			i++
		}
		start := i
		for i < len(value) && !contentCWhitespace(value[i]) {
			i++
		}
		if start < i {
			fields = append(fields, value[start:i])
		}
	}
	return fields
}

// trimContentCWhitespace trims the wider C-runtime isspace set, which the
// category and AI directive grammars use instead of the TDF set
// [02 R-P0-03 §3][08 R-AI-01 §20].
func trimContentCWhitespace(value string) string {
	return formats.TrimASCIIFunc(value, contentCWhitespace)
}

func contentCWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

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
