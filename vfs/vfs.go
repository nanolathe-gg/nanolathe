// Package vfs provides the logical content namespace used by OpenTA.
//
// Mounting indexes names and metadata only. Archive payloads are read and
// decompressed when the corresponding file is opened, which keeps startup
// independent of the size of the installed game data.
package vfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrNotFound = errors.New("vfs: file not found")
	ErrIsDir    = errors.New("vfs: path is a directory")
	ErrTooLarge = errors.New("vfs: file exceeds read limit")
)

// Provenance identifies the provider which won an overlay lookup.
type Provenance struct {
	LogicalPath  string
	OriginalPath string
	ProviderType string
	SourcePath   string
	MountRoot    string // absolute loose-mount root; empty for archives
	Priority     int
	MountOrder   int
	Compression  string
}

// ProviderID returns the portable identity of the winning provider: the
// archive file name for archives, or the path relative to the loose mount
// root. It never embeds absolute host paths, so manifests and manifest hashes
// are stable across machines and filesystems [PLAN_01 C13].
func (p Provenance) ProviderID() string {
	if p.MountRoot != "" && p.SourcePath != "" {
		if rel, err := filepath.Rel(p.MountRoot, p.SourcePath); err == nil &&
			!strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	if p.SourcePath != "" {
		return filepath.Base(p.SourcePath)
	}
	return p.ProviderType
}

// EntryInfo describes a logical file or directory without opening its data.
type EntryInfo struct {
	Name         string
	Path         string
	OriginalPath string
	Size         int64
	IsDir        bool
	Source       Provenance
}

// File is the handle returned by FS.Open. It deliberately exposes the common
// reader interfaces so format loaders can remain unaware of the provider.
type File interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
	Info() EntryInfo
}

type providerEntry struct {
	info EntryInfo
	open func() (File, error)
}

type provider interface {
	lookup(string) (*providerEntry, bool)
	children(string) []*providerEntry
	auditEntries() []EntryInfo
	close() error
}

type mountedProvider struct {
	provider provider
	priority int
	order    int
}

// FS is an overlay of loose directories and HPI-family archives. Higher
// priorities win; equal-priority mounts added later win. This makes explicit
// developer/mod overrides straightforward while keeping the result stable.
type FS struct {
	mounts    []mountedProvider
	nextOrder int
	mountedBy map[string]string // canonical full path -> provider already holding it
	notes     []string          // mount-time observations worth reporting
}

// ProviderInfo identifies one mounted provider in precedence order.
type ProviderInfo struct {
	ID         string // archive path, or the directory root for a loose mount
	Type       string // "hpi", "directory", ...
	Priority   int
	MountOrder int
	Files      int
}

// Providers lists the mounted set, best-precedence first. Diagnostics name
// providers with this rather than with per-entry source paths, which for a
// loose mount are individual files.
func (f *FS) Providers() []ProviderInfo {
	ordered := f.orderedMounts()
	infos := make([]ProviderInfo, 0, len(ordered))
	for _, mount := range ordered {
		info := ProviderInfo{Priority: mount.priority, MountOrder: mount.order}
		for _, entry := range mount.provider.auditEntries() {
			if entry.Path == "" && entry.IsDir {
				// The provider's own root: for a loose mount this is the
				// directory, which is the identity we want to report.
				info.Type = entry.Source.ProviderType
				info.ID = entry.Source.SourcePath
				continue
			}
			if entry.IsDir {
				continue
			}
			info.Files++
			if info.Type == "" {
				info.Type = entry.Source.ProviderType
			}
			if info.ID == "" {
				if entry.Source.ProviderType == "directory" {
					// Loose entries carry per-file host paths; recover the
					// mount root by trimming the logical path, which is folded
					// to lower case and so cannot be compared byte-for-byte.
					host := filepath.ToSlash(entry.Source.SourcePath)
					if n := len(host) - len(entry.Path); n > 0 &&
						strings.EqualFold(host[n:], entry.Path) {
						info.ID = strings.TrimSuffix(host[:n], "/")
					} else {
						info.ID = host
					}
				} else {
					info.ID = entry.Source.SourcePath
				}
			}
		}
		if info.ID == "" {
			info.ID = info.Type
		}
		infos = append(infos, info)
	}
	return infos
}

// Notes returns mount-time observations a caller should surface: suppressed
// duplicate mounts, and the archive-count remark described in
// docs/SPEC_CONFLICTS.md SC1.
func (f *FS) Notes() []string { return append([]string(nil), f.notes...) }

// MountTier is an explicit content-overlay tier. Higher tiers win. Retail
// checks loose files first, then scans archive groups in GP3, CCX, UFO, HPI
// order. Files within one group remain lexically ordered below as an OpenTA
// determinism policy; retail inherits host directory enumeration order.
type MountTier uint8

type MountPlan struct {
	Revision    string
	Provisional bool
	HPI, CCX    MountTier
	GP3, UFO    MountTier
	Loose       MountTier
}

// DefaultRetailMountPlan is a deliberate catalog policy difference, not a
// reproduction of retail's loader. Retail mounts loose data, then rev*.GP3,
// CCX, UFO, HPI and the CD HPI, and resolves a lookup against Windows
// directory enumeration order. OpenTA sorts archives lexically inside each tier
// so the equal-tier winner is stable across filesystems, and every shadowed
// mount is recorded in manifest identity (content.ManifestRecord.Shadowed) so a
// differing winner can be named rather than silently used.
func DefaultRetailMountPlan() MountPlan {
	return MountPlan{Revision: "retail-binary-tiers-openta-lexical-1", HPI: 1, UFO: 2, CCX: 3, GP3: 4, Loose: 10}
}

func (p MountPlan) Validate() error {
	if p.Revision == "" || p.HPI == 0 || p.CCX == 0 || p.GP3 == 0 || p.UFO == 0 || p.Loose == 0 {
		return errors.New("vfs: invalid mount plan")
	}
	return nil
}

// FSOps is the read surface the viewer inspector needs; a mount-pinned view
// can be swapped in for shadow inspection (Phase 10).
type FSOps interface {
	Open(name string) (File, error)
	ReadFileLimit(name string, max int64) ([]byte, error)
	ReadDir(name string) ([]EntryInfo, error)
	Stat(name string) (EntryInfo, error)
	CacheStamp(name string) (string, error)
}

var _ FSOps = (*FS)(nil)

// MountCount reports how many mounts are ordered (0 = the winner).
func (f *FS) MountCount() int {
	return len(f.orderedMounts())
}

// OpenMount opens a logical file from the index-th mount in precedence
// order (0 = the winning mount). It reports ErrNotFound when that mount
// does not provide the path.
func (f *FS) OpenMount(index int, name string) (File, error) {
	logical, err := cleanPath(name)
	if err != nil {
		return nil, err
	}
	mounts := f.orderedMounts()
	if index < 0 || index >= len(mounts) {
		return nil, fmt.Errorf("%w: mount %d", ErrNotFound, index)
	}
	entry, ok := mounts[index].provider.lookup(logical)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return entry.open()
}

// PinnedMount is a read view that resolves Open/Stat/CacheStamp against one
// mount while ReadDir/ReadFileLimit keep resolving through the overlay, so a
// shadowed copy parses with its own bytes but still resolves its cross-asset
// dependencies normally.
type PinnedMount struct {
	fs    *FS
	index int
}

// Pinned returns the mount-pinned view for shadow inspection.
func (f *FS) Pinned(index int) *PinnedMount {
	return &PinnedMount{fs: f, index: index}
}

func (p *PinnedMount) Open(name string) (File, error) {
	return p.fs.OpenMount(p.index, name)
}
func (p *PinnedMount) ReadFileLimit(name string, max int64) ([]byte, error) {
	file, err := p.fs.OpenMount(p.index, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, max))
}
func (p *PinnedMount) ReadDir(name string) ([]EntryInfo, error) {
	return p.fs.ReadDir(name)
}
func (p *PinnedMount) Stat(name string) (EntryInfo, error) {
	if file, err := p.fs.OpenMount(p.index, name); err == nil {
		info := file.Info()
		file.Close()
		return info, nil
	}
	return p.fs.Stat(name)
}
func (p *PinnedMount) CacheStamp(name string) (string, error) {
	if info, err := p.Stat(name); err == nil {
		return p.fs.stampFor(info), nil
	}
	return p.fs.CacheStamp(name)
}

var _ FSOps = (*PinnedMount)(nil)

// Entries returns every indexed provider entry, including entries shadowed by
// the overlay. It is intended for diagnostic tools; normal content consumers
// should continue to use ReadDir, Stat, and Open, which expose only winners.
func (f *FS) Entries() []EntryInfo {
	var result []EntryInfo
	for _, mount := range f.mounts {
		result = append(result, mount.provider.auditEntries()...)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		if result[i].Source.Priority != result[j].Source.Priority {
			return result[i].Source.Priority > result[j].Source.Priority
		}
		if result[i].Source.MountOrder != result[j].Source.MountOrder {
			return result[i].Source.MountOrder > result[j].Source.MountOrder
		}
		return result[i].OriginalPath < result[j].OriginalPath
	})
	return result
}

// CacheStamp returns a cheap host-side freshness token for a winning logical
// file. It is deliberately cache metadata only: it never participates in
// manifest or simulation hashes. Loose files use their file metadata and
// archive entries use the containing archive metadata plus the logical size.
// Synthetic providers without host metadata fall back to the stable VFS
// identity fields, which simply causes a normal content read on cache miss.
func (f *FS) CacheStamp(name string) (string, error) {
	info, err := f.Stat(name)
	if err != nil {
		return "", err
	}
	return f.stampFor(info), nil
}

func (f *FS) stampFor(info EntryInfo) string {
	stamp := fmt.Sprintf("%s|%s|%d|%d|%d", info.Source.ProviderType, path.Base(info.Source.SourcePath), info.Size, info.Source.Priority, info.Source.MountOrder)
	if info.Source.SourcePath != "" {
		if stat, statErr := os.Stat(info.Source.SourcePath); statErr == nil {
			stamp = fmt.Sprintf("%s|%d|%d", stamp, stat.Size(), stat.ModTime().UnixNano())
		}
	}
	return stamp
}

func New() *FS { return &FS{} }

// MountDirectory indexes a loose directory's names and metadata. It does not
// read file contents. The directory is resolved case-insensitively at mount
// time, including on case-sensitive host filesystems. An equal full path
// already mounted is suppressed [02 §2].
func (f *FS) MountDirectory(root string, priority int) error {
	if f.alreadyMounted(root) {
		return nil
	}
	p, err := newLooseProvider(root, priority, f.nextOrder)
	if err != nil {
		return err
	}
	f.mounts = append(f.mounts, mountedProvider{provider: p, priority: priority, order: f.nextOrder})
	f.nextOrder++
	return nil
}

// MountArchive opens and indexes an HPI-family archive. Only its header and
// directory region are read here; compressed file payloads remain on disk. An
// equal full path already mounted is suppressed [02 §2].
func (f *FS) MountArchive(filename string, priority int) (*Archive, error) {
	if f.alreadyMounted(filename) {
		return nil, nil
	}
	a, err := OpenArchive(filename, ArchiveOptions{})
	if err != nil {
		return nil, err
	}
	a.setMountInfo(priority, f.nextOrder)
	f.mounts = append(f.mounts, mountedProvider{provider: a, priority: priority, order: f.nextOrder})
	f.nextOrder++
	return a, nil
}

// MountArchiveReader is useful for nested archives and tests. The reader must
// remain valid until the returned archive is closed.
func (f *FS) MountArchiveReader(name string, reader io.ReaderAt, size int64, priority int, options ArchiveOptions) (*Archive, error) {
	a, err := NewArchive(name, reader, size, options)
	if err != nil {
		return nil, err
	}
	a.setMountInfo(priority, f.nextOrder)
	f.mounts = append(f.mounts, mountedProvider{provider: a, priority: priority, order: f.nextOrder})
	f.nextOrder++
	return a, nil
}

// MountGameDirectory discovers the standard HPI-family files using the retail
// archive-group precedence. Callers with mod-specific precedence should use
// MountGameDirectoryWithPlan.
func (f *FS) MountGameDirectory(root string) error {
	return f.MountGameDirectoryWithPlan(root, DefaultRetailMountPlan())
}

// canonicalMountKey folds a provider path the way retail's mount dedup does:
// full path, compared case-insensitively [02 §2].
func canonicalMountKey(name string) string {
	abs, err := filepath.Abs(name)
	if err != nil {
		abs = name
	}
	return strings.ToLower(filepath.Clean(abs))
}

// noteDuplicateMount records and reports a suppressed duplicate. Retail
// suppresses the second mount of an equal full path [02 §2].
func (f *FS) alreadyMounted(name string) bool {
	key := canonicalMountKey(name)
	if f.mountedBy == nil {
		f.mountedBy = make(map[string]string)
	}
	if first, ok := f.mountedBy[key]; ok {
		f.notes = append(f.notes, fmt.Sprintf("duplicate mount suppressed: %s (already mounted as %s)", name, first))
		return true
	}
	f.mountedBy[key] = name
	return false
}

func (f *FS) MountGameDirectoryWithPlan(root string, plan MountPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	archives := make([]os.DirEntry, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".hpi", ".ufo", ".ccx", ".gp3", ".gp4", ".gpf", ".swx":
			archives = append(archives, entry)
		}
	}
	sort.Slice(archives, func(i, j int) bool {
		// Retail consumes Windows directory enumeration order. OpenTA sorts
		// explicitly so the equal-tier winner is stable across filesystems.
		// Equal-priority mounts added later win, so lexically later archive
		// names take precedence within one tier.
		li, lj := strings.ToLower(archives[i].Name()), strings.ToLower(archives[j].Name())
		if li != lj {
			return li < lj
		}
		return archives[i].Name() < archives[j].Name()
	})
	// Retail's documented ten-archive cap on local HPI [02 §2] is not applied:
	// a full install carries thirteen and plays. Applying it would drop map and
	// campaign archives. See docs/SPEC_CONFLICTS.md SC1.
	localHPI := 0
	for _, entry := range archives {
		if strings.EqualFold(filepath.Ext(entry.Name()), ".hpi") {
			localHPI++
		}
	}
	if localHPI > 10 {
		f.notes = append(f.notes, fmt.Sprintf(
			"%d local HPI archives mounted; retail documents a cap of 10 (docs/SPEC_CONFLICTS.md SC1)", localHPI))
	}

	for _, entry := range archives {
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		tier := plan.HPI
		switch extension {
		case ".hpi":
			tier = plan.HPI
		case ".ccx":
			tier = plan.CCX
		case ".gp3", ".gp4", ".gpf", ".swx":
			tier = plan.GP3
		case ".ufo":
			tier = plan.UFO
		}
		full := filepath.Join(root, entry.Name())
		if _, err := f.MountArchive(full, int(tier)*10); err != nil {
			return err
		}
	}
	return f.MountDirectory(root, int(plan.Loose)*10)
}

func (f *FS) orderedMounts() []mountedProvider {
	result := append([]mountedProvider(nil), f.mounts...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].priority != result[j].priority {
			return result[i].priority > result[j].priority
		}
		return result[i].order > result[j].order
	})
	return result
}

func (f *FS) Open(name string) (File, error) {
	logical, err := cleanPath(name)
	if err != nil {
		return nil, err
	}
	for _, mount := range f.orderedMounts() {
		entry, ok := mount.provider.lookup(logical)
		if !ok {
			continue
		}
		if entry.info.IsDir {
			return nil, fmt.Errorf("%w: %s", ErrIsDir, name)
		}
		return entry.open()
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
}

func (f *FS) ReadFile(name string) ([]byte, error) {
	h, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	return io.ReadAll(h)
}

// ReadFileLimit reads at most max bytes from a winning logical file. The
// extra byte distinguishes an exactly-full file from a truncated result.
func (f *FS) ReadFileLimit(name string, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, fmt.Errorf("%w: invalid limit %d", ErrTooLarge, max)
	}
	h, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	if info := h.Info(); info.Size >= 0 && info.Size > max {
		return nil, fmt.Errorf("%w: %s is %d bytes (limit %d)", ErrTooLarge, name, info.Size, max)
	}
	data, err := io.ReadAll(io.LimitReader(h, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrTooLarge, name, max)
	}
	return data, nil
}

func (f *FS) Stat(name string) (EntryInfo, error) {
	logical, err := cleanPath(name)
	if err != nil {
		return EntryInfo{}, err
	}
	for _, mount := range f.orderedMounts() {
		entry, ok := mount.provider.lookup(logical)
		if ok {
			return entry.info, nil
		}
	}
	return EntryInfo{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Sources returns every mount that carries a logical path, in the same
// precedence order Open resolves. The first entry is the winner; anything after
// it is shadowed. Callers use it to make a duplicate explicit rather than
// silently taking the winner.
func (f *FS) Sources(name string) []EntryInfo {
	logical, err := cleanPath(name)
	if err != nil {
		return nil
	}
	var out []EntryInfo
	for _, mount := range f.orderedMounts() {
		if entry, ok := mount.provider.lookup(logical); ok {
			out = append(out, entry.info)
		}
	}
	return out
}

// ReadDir returns the union of child names, with each collision resolved by
// the same precedence rules as Open. Results are sorted canonically so callers
// never derive IDs from map iteration order.
func (f *FS) ReadDir(name string) ([]EntryInfo, error) {
	logical, err := cleanPath(name)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(logical); err == nil && !info.IsDir {
		return nil, fmt.Errorf("%w: %s", ErrIsDir, name)
	}

	winners := make(map[string]EntryInfo)
	for _, mount := range f.orderedMounts() {
		for _, entry := range mount.provider.children(logical) {
			if _, exists := winners[entry.info.Path]; !exists {
				winners[entry.info.Path] = entry.info
			}
		}
	}
	result := make([]EntryInfo, 0, len(winners))
	for _, info := range winners {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	if len(result) == 0 && logical != "" {
		if _, err := f.Stat(logical); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
	}
	return result, nil
}

// RetailReadDir returns the winning direct children of a logical directory in
// the provider/enumeration order exposed by the mounted providers. Retail's
// front-end wildcard scans do not sort these results; ordinary ReadDir keeps
// its canonical sorted contract for deterministic content consumers.
func (f *FS) RetailReadDir(name string) ([]EntryInfo, error) {
	logical, err := cleanPath(name)
	if err != nil {
		return nil, err
	}
	prefix := logical
	if prefix != "" {
		prefix += "/"
	}
	seen := make(map[string]bool)
	var result []EntryInfo
	for _, mount := range f.orderedMounts() {
		for _, info := range mount.provider.auditEntries() {
			if info.Path == "" || !strings.HasPrefix(info.Path, prefix) {
				continue
			}
			rest := strings.TrimPrefix(info.Path, prefix)
			if rest == "" || strings.Contains(rest, "/") {
				continue
			}
			key := strings.ToLower(info.Path)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, info)
		}
	}
	if len(result) == 0 && logical != "" {
		if _, err := f.Stat(logical); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
	}
	return result, nil
}

// Close closes all mounted archive handles. Loose-directory mounts have no
// resources to release.
func (f *FS) Close() error {
	var first error
	for _, mount := range f.mounts {
		if err := mount.provider.close(); err != nil && first == nil {
			first = err
		}
	}
	f.mounts = nil
	return first
}

type fileHandle struct {
	reader   io.ReadSeeker
	readerAt io.ReaderAt
	closeFn  func() error
	info     EntryInfo
}

func (h *fileHandle) Read(p []byte) (int, error)                { return h.reader.Read(p) }
func (h *fileHandle) ReadAt(p []byte, off int64) (int, error)   { return h.readerAt.ReadAt(p, off) }
func (h *fileHandle) Seek(off int64, whence int) (int64, error) { return h.reader.Seek(off, whence) }
func (h *fileHandle) Close() error                              { return h.closeFn() }
func (h *fileHandle) Info() EntryInfo                           { return h.info }

func newOSFile(filename string, info EntryInfo) (File, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	return &fileHandle{reader: file, readerAt: file, closeFn: file.Close, info: info}, nil
}

type looseProvider struct {
	root           string
	entries        map[string]*providerEntry
	indexedEntries []EntryInfo
}

func newLooseProvider(root string, priority, order int) (*looseProvider, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("vfs: %s is not a directory", root)
	}
	p := &looseProvider{root: absolute, entries: make(map[string]*providerEntry)}
	rootInfo := EntryInfo{Path: "", Name: "", IsDir: true, OriginalPath: "", Source: Provenance{ProviderType: "directory", SourcePath: absolute, MountRoot: absolute, Priority: priority, MountOrder: order}}
	p.entries[""] = &providerEntry{info: rootInfo}
	if err := p.indexDir(absolute, "", priority, order); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *looseProvider) indexDir(dirname, parent string, priority, order int) error {
	items, err := os.ReadDir(dirname)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Type()&os.ModeSymlink != 0 {
			// A loose mount is a logical content root. Do not make a host
			// symlink an alternate path into arbitrary files outside it.
			continue
		}
		logical, err := joinPath(parent, item.Name())
		if err != nil {
			return err
		}
		original := originalJoin(parent, item.Name())
		full := filepath.Join(dirname, item.Name())
		isDir := item.IsDir()
		var size int64
		if !isDir {
			if stat, statErr := item.Info(); statErr == nil {
				size = stat.Size()
			}
		}
		info := EntryInfo{Path: logical, Name: item.Name(), OriginalPath: original, Size: size, IsDir: isDir,
			Source: Provenance{LogicalPath: logical, OriginalPath: original, ProviderType: "directory", SourcePath: full, MountRoot: p.root, Priority: priority, MountOrder: order}}
		entry := &providerEntry{info: info}
		p.indexedEntries = append(p.indexedEntries, info)
		if !isDir {
			entry.open = func() (File, error) { return newOSFile(full, info) }
		} else if err := p.indexDir(full, logical, priority, order); err != nil {
			return err
		}
		p.entries[logical] = entry
	}
	return nil
}

func (p *looseProvider) lookup(name string) (*providerEntry, bool) {
	entry, ok := p.entries[name]
	return entry, ok
}

func (p *looseProvider) children(parent string) []*providerEntry {
	result := make([]*providerEntry, 0)
	prefix := parent
	if prefix != "" {
		prefix += "/"
	}
	for name, entry := range p.entries {
		if name == "" || !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if rest != "" && !strings.Contains(rest, "/") {
			result = append(result, entry)
		}
	}
	return result
}

func (p *looseProvider) close() error { return nil }

func (p *looseProvider) auditEntries() []EntryInfo {
	return append([]EntryInfo(nil), p.indexedEntries...)
}
