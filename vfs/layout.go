// Content directory layouts: the first-segment redirection a content set may
// need so its authored trees are read under the names the loaders ask for
// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").

package vfs

import (
	"sort"
	"strings"
)

// Layout redirects the first segment of a logical path. Some content sets
// author their trees under renamed directories — `weapons` becomes `weaponP`,
// `units` becomes `unitsE` — so a loader that asks for the retail name finds
// nothing. A Layout carries one such rename table: the key is the retail
// directory the loaders name, the value is the directory the content set
// actually ships. An empty Layout is the retail case and rewrites nothing.
//
// Matching is case-insensitive, like every other lookup in the overlay
// [02 §2]. The zero value is usable.
type Layout struct {
	// entries is sorted by retail name, so two equal tables produce one
	// order and a diagnostic that lists them never depends on map iteration
	// [I1]. The tables are a handful of rows, so a linear scan of a slice is
	// cheaper than a map probe and allocates nothing.
	entries []layoutEntry
}

type layoutEntry struct {
	retail string // the directory the loaders ask for, as authored here
	target string // the directory the content set ships, as it spells it
}

// NewLayout builds a Layout from a retail-to-target table. Rows whose two
// names are equal, or whose either name is empty, are dropped: they would
// rewrite a path to itself.
func NewLayout(table map[string]string) Layout {
	if len(table) == 0 {
		return Layout{}
	}
	entries := make([]layoutEntry, 0, len(table))
	for retail, target := range table {
		if retail == "" || target == "" || strings.EqualFold(retail, target) {
			continue
		}
		entries = append(entries, layoutEntry{retail: retail, target: target})
	}
	if len(entries) == 0 {
		return Layout{}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].retail < entries[j].retail })
	return Layout{entries: entries}
}

// Names returns the retail-to-target rows in retail-name order, for
// diagnostics and reports. The result is a fresh slice; the Layout itself is
// immutable once built.
func (l Layout) Names() [][2]string {
	if len(l.entries) == 0 {
		return nil
	}
	rows := make([][2]string, 0, len(l.entries))
	for _, entry := range l.entries {
		rows = append(rows, [2]string{entry.retail, entry.target})
	}
	return rows
}

// firstSegment splits a logical path at its first separator. Both slash styles
// are accepted here for the same reason cleanPath accepts them: a caller may
// hand over an authored backslash path (C7).
func firstSegment(name string) (first, rest string) {
	for i := 0; i < len(name); i++ {
		if name[i] == '/' || name[i] == '\\' {
			return name[:i], name[i:]
		}
	}
	return name, ""
}

// rewrite maps a requested retail-named path onto the directory the content
// set ships. It allocates one short concatenation when a row matches and
// nothing at all otherwise.
func (l Layout) rewrite(name string) string {
	if len(l.entries) == 0 {
		return name
	}
	first, rest := firstSegment(name)
	for _, entry := range l.entries {
		if strings.EqualFold(first, entry.retail) {
			if rest == "" {
				return entry.target
			}
			return entry.target + rest
		}
	}
	return name
}

// restore is rewrite's inverse: it maps a path the providers hold back onto
// the retail name the loaders asked for, so provenance, diagnostics and every
// identity derived from a logical path stay retail-named.
func (l Layout) restore(name string) string {
	if len(l.entries) == 0 {
		return name
	}
	first, rest := firstSegment(name)
	for _, entry := range l.entries {
		if strings.EqualFold(first, entry.target) {
			if rest == "" {
				return entry.retail
			}
			return entry.retail + rest
		}
	}
	return name
}

// Apply returns a read view of inner whose five lookups take retail-named
// paths and whose results carry retail-named paths, with this Layout's
// redirection in between. An empty Layout returns inner unchanged, so a
// retail content set keeps the concrete overlay it mounted — including the
// optional views a caller may type-assert for.
//
// The wrapper is a read view only: it never mounts, never closes, and leaves
// the overlay's own per-mount surface (Providers, Entries, Pinned) alone,
// because those report what is on disk and are not what a loader reads.
func (l Layout) Apply(inner FSOps) FSOps {
	if len(l.entries) == 0 || inner == nil {
		return inner
	}
	view := &layoutFS{inner: inner, layout: l}
	if sources, ok := inner.(interface{ Sources(string) []EntryInfo }); ok {
		view.sources = sources
	}
	if manifest, ok := inner.(interface{ ManifestHash() (string, error) }); ok {
		view.manifest = manifest
	}
	// Byte-range reads are a capability, not a report: a caller probes for
	// RangeReader and takes a different path when it is absent. So the
	// wrapper carries the method only when the wrapped view has it, and a
	// probe through a layout answers exactly as a probe of the view itself
	// would. Forwarding it matters because the map census reads two short
	// header ranges out of every TNT; without this a profiled content set
	// would decompress every terrain file whole to collect them.
	if ordered, ok := inner.(retailDirReader); ok {
		orderedView := &layoutOrderedFS{layoutFS: view, ordered: ordered}
		if ranged, ok := inner.(RangeReader); ok {
			return &layoutRangeOrderedFS{layoutOrderedFS: orderedView, ranged: ranged}
		}
		return orderedView
	}
	if ranged, ok := inner.(RangeReader); ok {
		return &layoutRangeFS{layoutFS: view, ranged: ranged}
	}
	return view
}

// layoutFS is the read view Layout.Apply returns.
type layoutFS struct {
	inner  FSOps
	layout Layout
	// sources and manifest are the overlay's two optional views, resolved
	// once here rather than asserted per call. Both methods below answer as
	// an FSOps without them would when the field is nil, so wrapping never
	// changes what a caller that probes for them observes.
	sources  interface{ Sources(string) []EntryInfo }
	manifest interface{ ManifestHash() (string, error) }
}

var _ FSOps = (*layoutFS)(nil)

func (f *layoutFS) Open(name string) (File, error) {
	return f.inner.Open(f.layout.rewrite(name))
}

func (f *layoutFS) ReadFileLimit(name string, max int64) ([]byte, error) {
	return f.inner.ReadFileLimit(f.layout.rewrite(name), max)
}

func (f *layoutFS) ReadDir(name string) ([]EntryInfo, error) {
	entries, err := f.inner.ReadDir(f.layout.rewrite(name))
	for i := range entries {
		entries[i].Path = f.layout.restore(entries[i].Path)
	}
	return entries, err
}

func (f *layoutFS) Stat(name string) (EntryInfo, error) {
	info, err := f.inner.Stat(f.layout.rewrite(name))
	info.Path = f.layout.restore(info.Path)
	return info, err
}

func (f *layoutFS) CacheStamp(name string) (string, error) {
	return f.inner.CacheStamp(f.layout.rewrite(name))
}

// Sources reports the providers that hold a path, best precedence first. It
// answers nil when the wrapped view has no such report, which is what a
// caller probing for the optional interface would observe without the
// wrapper.
func (f *layoutFS) Sources(name string) []EntryInfo {
	if f.sources == nil {
		return nil
	}
	infos := f.sources.Sources(f.layout.rewrite(name))
	for i := range infos {
		infos[i].Path = f.layout.restore(infos[i].Path)
	}
	return infos
}

// ManifestHash forwards the overlay's mount identity, or an empty identity
// when the wrapped view has none — again the answer a caller would get from
// an FSOps that does not offer it.
func (f *layoutFS) ManifestHash() (string, error) {
	if f.manifest == nil {
		return "", nil
	}
	return f.manifest.ManifestHash()
}

// layoutRangeFS is the view Apply returns over a wrapped view that offers
// byte-range reads. It is the same read view with one more method, so the
// redirection is described once.
type layoutRangeFS struct {
	*layoutFS
	ranged RangeReader
}

var (
	_ FSOps       = (*layoutRangeFS)(nil)
	_ RangeReader = (*layoutRangeFS)(nil)
)

// ReadFileRange forwards a partial read under the rewritten path. The result
// is bytes, not provenance, so nothing needs restoring on the way back.
func (f *layoutRangeFS) ReadFileRange(name string, offset int64, length int) ([]byte, error) {
	return f.ranged.ReadFileRange(f.layout.rewrite(name), offset, length)
}

// Retail directory enumeration is a capability too: download membership uses
// the provider's order, rather than ReadDir's sorted order [02 R-CAT-01 §8].
// Keep it only when the wrapped view has it, preserving synthetic fallbacks.
type retailDirReader interface {
	RetailReadDir(string) ([]EntryInfo, error)
}

type layoutOrderedFS struct {
	*layoutFS
	ordered retailDirReader
}

func (f *layoutOrderedFS) RetailReadDir(name string) ([]EntryInfo, error) {
	entries, err := f.ordered.RetailReadDir(f.layout.rewrite(name))
	for i := range entries {
		entries[i].Path = f.layout.restore(entries[i].Path)
	}
	return entries, err
}

type layoutRangeOrderedFS struct {
	*layoutOrderedFS
	ranged RangeReader
}

func (f *layoutRangeOrderedFS) ReadFileRange(name string, offset int64, length int) ([]byte, error) {
	return f.ranged.ReadFileRange(f.layout.rewrite(name), offset, length)
}
