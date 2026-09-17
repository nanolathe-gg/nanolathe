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

// maxInt is the largest value an int holds on this platform; a loose range
// whose remaining length exceeds it cannot be addressed by one slice, the same
// ceiling the archive reader applies to a record size.
const maxInt = int(^uint(0) >> 1)

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
	// readRange, when set, produces a byte range without materializing the
	// whole file. A provider that cannot do better than a full read leaves it
	// nil and FS.ReadFileRange falls back to Open.
	readRange func(offset int64, length int) ([]byte, error)
}

type provider interface {
	lookup(string) (*providerEntry, bool)
	children(string) []*providerEntry
	auditEntries() []EntryInfo
	retailEntries() []EntryInfo
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
						foldLogicalName(host[n:]) == entry.Path {
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

// ProviderIDs lists the mounted provider identities in precedence order. It is
// what a "providers searched" diagnostic means, and deliberately not per-entry
// source paths: for a loose mount those are individual files.
func (f *FS) ProviderIDs() []string {
	providers := f.Providers()
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, provider.ID)
	}
	return names
}

// Notes returns mount-time observations a caller should surface: suppressed
// duplicate mounts, rejected archive candidates, and the archive-count remark described in
// docs/SPEC_CONFLICTS.md SC1.
func (f *FS) Notes() []string { return append([]string(nil), f.notes...) }

// MountTier is an explicit content-overlay tier. Higher tiers win. Retail
// checks loose files first, then scans archive groups in GP3, CCX, UFO, HPI
// order. Files within one group remain lexically ordered below as a Nanolathe
// determinism policy; retail inherits host directory enumeration order.
type MountTier uint8

// MountPlan is the per-extension mount tier policy: the tier each archive
// family joins, and the revision string that identifies the policy itself.
type MountPlan struct {
	Revision string
	HPI, CCX MountTier
	GP3, UFO MountTier
	Loose    MountTier
}

// DefaultRetailMountPlan is a deliberate catalog policy difference, not a
// reproduction of retail's loader. Retail mounts loose data, then rev*.GP3,
// CCX, UFO, HPI and the CD HPI, and resolves a lookup against Windows
// directory enumeration order. Nanolathe sorts archives lexically inside each
// tier so the equal-tier winner is stable across filesystems, and every shadowed
// mount is recorded in manifest identity (content.ManifestRecord.Shadowed) so a
// differing winner can be named rather than silently used.
func DefaultRetailMountPlan() MountPlan {
	return MountPlan{Revision: "retail-binary-tiers-openta-lexical-1", HPI: 1, UFO: 2, CCX: 3, GP3: 4, Loose: 10}
}

// Validate reports a plan that leaves any tier or the revision unset.
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
	return len(f.mounts)
}

// OpenMount opens a logical file from the index-th mount in precedence
// order (0 = the winning mount). It reports ErrNotFound when that mount
// does not provide the path.
func (f *FS) OpenMount(index int, name string) (File, error) {
	entry, err := f.mountEntry(index, name)
	if err != nil {
		return nil, err
	}
	return openEntry(entry, name)
}

func (f *FS) mountEntry(index int, name string) (*providerEntry, error) {
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
	return entry, nil
}

func openEntry(entry *providerEntry, name string) (File, error) {
	if entry.info.IsDir {
		return nil, fmt.Errorf("%w: %s", ErrIsDir, name)
	}
	return entry.open()
}

// PinnedMount is a read view for one mount. Its file and metadata operations
// keep the inspected provider's identity; ReadDir remains the overlay's
// directory-enumeration view.
type PinnedMount struct {
	fs    *FS
	index int
}

// Pinned returns the mount-pinned view for shadow inspection.
func (f *FS) Pinned(index int) *PinnedMount {
	return &PinnedMount{fs: f, index: index}
}

// Open opens a name in the pinned mount alone, never falling through to a
// lower mount.
func (p *PinnedMount) Open(name string) (File, error) {
	return p.fs.OpenMount(p.index, name)
}

// ReadFileLimit reads at most max bytes of a name from the pinned mount.
func (p *PinnedMount) ReadFileLimit(name string, max int64) ([]byte, error) {
	return readFileLimit(p.Open, p.Stat, name, max)
}

// ReadDir lists a directory across the whole overlay: a pinned mount shadows
// file lookups, not directory enumeration.
func (p *PinnedMount) ReadDir(name string) ([]EntryInfo, error) {
	return p.fs.ReadDir(name)
}

// Stat describes a name as the pinned mount holds it. Pinned views inspect one
// provider, so a missing pinned file does not acquire an overlay identity.
func (p *PinnedMount) Stat(name string) (EntryInfo, error) {
	entry, err := p.fs.mountEntry(p.index, name)
	if err != nil {
		return EntryInfo{}, err
	}
	return entry.info, nil
}

// CacheStamp returns the identity stamp of the pinned mount's copy of a name.
func (p *PinnedMount) CacheStamp(name string) (string, error) {
	info, err := p.Stat(name)
	if err != nil {
		return "", err
	}
	return p.fs.stampFor(info), nil
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

// New returns an empty overlay with no mounts.
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
	f.rememberMount(root)
	f.insertMount(mountedProvider{provider: p, priority: priority, order: f.nextOrder})
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
	f.rememberMount(filename)
	a.setMountInfo(priority, f.nextOrder)
	f.insertMount(mountedProvider{provider: a, priority: priority, order: f.nextOrder})
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
	f.insertMount(mountedProvider{provider: a, priority: priority, order: f.nextOrder})
	return a, nil
}

// MountGameDirectory discovers the standard HPI-family files using the retail
// archive-group precedence. Callers with mod-specific precedence should use
// MountGameDirectoryWithPlan.
func (f *FS) MountGameDirectory(root string) error {
	return f.MountGameDirectoryWithPlan(root, DefaultRetailMountPlan())
}

// MountGameDirectories adds complete roots in load order: every provider in a
// later root outranks every provider in an earlier root. Within each root the
// existing retail tier policy applies. This host extension is deliberate only
// for multiple roots (docs/DESIGN_CONTENT_VFS.md §5). Repeated paths retain
// the existing first-mount deduplication policy [02 §2].
func (f *FS) MountGameDirectories(roots []string) error {
	plan := DefaultRetailMountPlan()
	span := int(max(plan.HPI, plan.UFO, plan.CCX, plan.GP3, plan.Loose)) * 10
	for i, root := range roots {
		if err := f.mountGameDirectory(root, plan, i*span); err != nil {
			return fmt.Errorf("mounting root %q: %w", root, err)
		}
	}
	return nil
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
	if first, ok := f.mountedBy[key]; ok {
		f.notes = append(f.notes, fmt.Sprintf("duplicate mount suppressed: %s (already mounted as %s)", name, first))
		return true
	}
	return false
}

// Failed candidates must remain eligible for a later mount attempt [02 §2].
func (f *FS) rememberMount(name string) {
	if f.mountedBy == nil {
		f.mountedBy = make(map[string]string)
	}
	f.mountedBy[canonicalMountKey(name)] = name
}

// MountGameDirectoryWithPlan mounts an installation directory under an
// explicit tier policy. MountGameDirectory is this with the default plan.
func (f *FS) MountGameDirectoryWithPlan(root string, plan MountPlan) error {
	return f.mountGameDirectory(root, plan, 0)
}

func (f *FS) mountGameDirectory(root string, plan MountPlan, basePriority int) error {
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
		// Retail consumes Windows directory enumeration order. Nanolathe sorts
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
		archive, err := f.MountArchive(full, basePriority+int(tier)*10)
		if err != nil {
			// Only the container gate and an unavailable candidate open are
			// discovery rejection [02 §2]. Indexing and later payload errors
			// keep their own failure boundary.
			var pathErr *os.PathError
			reason := err
			openFailed := errors.As(err, &pathErr) && pathErr.Op == "open"
			if openFailed {
				reason = pathErr.Err // provider identity below replaces the host path
			}
			if errors.Is(err, ErrRejectedArchive) || openFailed {
				f.notes = append(f.notes, fmt.Sprintf("nanolathe: archive rejected: logical path %s, providers searched [%s], expected HPI container: %v", entry.Name(), entry.Name(), reason))
				continue
			}
			return err
		}
		if archive != nil && extension == ".hpi" {
			localHPI++
		}
	}
	if localHPI > 10 {
		f.notes = append(f.notes, fmt.Sprintf(
			"%d local HPI archives mounted; retail documents a cap of 10 (docs/SPEC_CONFLICTS.md SC1)", localHPI))
	}
	return f.MountDirectory(root, basePriority+int(plan.Loose)*10)
}

// insertMount places one mount in final lookup order and takes the next order
// number.
//
// f.mounts is kept in search order at all times — priority descending, then
// mount order descending — so a lookup iterates it directly. Every ordinary
// Open, Stat, ReadDir and provider report used to copy the whole slice and
// sort it first; content loading performs thousands of logical opens against a
// read-mostly structure, so that was a copy and a sort per open.
//
// A new mount always carries the highest order number, so among equal
// priorities it belongs first: the insertion point is the first mount whose
// priority is not greater than this one's. Precedence itself is unchanged.
func (f *FS) insertMount(m mountedProvider) {
	at := len(f.mounts)
	for i := range f.mounts {
		if f.mounts[i].priority <= m.priority {
			at = i
			break
		}
	}
	f.mounts = append(f.mounts, mountedProvider{})
	copy(f.mounts[at+1:], f.mounts[at:])
	f.mounts[at] = m
	f.nextOrder++
}

// orderedMounts returns the mounts in search order. The slice is already in
// that order; it is not copied, so callers must only read it.
func (f *FS) orderedMounts() []mountedProvider { return f.mounts }

// Open returns the winning provider's file for a logical name.
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
		return openEntry(entry, name)
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// WholeFile is the optional capability a File exposes when opening it already
// produced the entire file — an archive record is decoded in one piece, so
// reading it whole need not copy it a second time. The returned slice belongs
// to the caller; a provider that cannot hand over an exclusive buffer does not
// implement this.
type WholeFile interface {
	Whole() ([]byte, bool)
}

// WholeBytes returns a file's complete contents, taking the WholeFile buffer
// when the provider offers one and falling back to a streaming read.
func WholeBytes(file File) ([]byte, error) {
	if w, ok := file.(WholeFile); ok {
		if data, ok := w.Whole(); ok {
			return data, nil
		}
	}
	return io.ReadAll(file)
}

// ReadFile returns the complete contents of the winning file.
func (f *FS) ReadFile(name string) ([]byte, error) {
	h, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	return WholeBytes(h)
}

// ReadFileLimit reads at most max bytes from a winning logical file. The
// extra byte distinguishes an exactly-full file from a truncated result.
func (f *FS) ReadFileLimit(name string, max int64) ([]byte, error) {
	return readFileLimit(f.Open, f.Stat, name, max)
}

// readFileLimit checks indexed metadata before opening a provider. Archive
// opens decode their whole record, so this preserves a caller's budget before
// compressed payload allocation begins [02 §2].
func readFileLimit(open func(string) (File, error), stat func(string) (EntryInfo, error), name string, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, fmt.Errorf("%w: invalid limit %d", ErrTooLarge, max)
	}
	info, err := stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir {
		return nil, fmt.Errorf("%w: %s", ErrIsDir, name)
	}
	if info.Size >= 0 && info.Size > max {
		return nil, fmt.Errorf("%w: %s is %d bytes (limit %d)", ErrTooLarge, name, info.Size, max)
	}
	h, err := open(name)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	var data []byte
	if w, ok := h.(WholeFile); ok {
		if whole, ok := w.Whole(); ok {
			data = whole
		}
	}
	if data == nil {
		limit := max
		if max < int64(^uint64(0)>>1) {
			limit++
		}
		read, err := io.ReadAll(io.LimitReader(h, limit))
		if err != nil {
			return nil, err
		}
		data = read
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrTooLarge, name, max)
	}
	return data, nil
}

// RangeReader is the optional capability FS provides for reading part of a
// file. It exists because a header probe over a whole install must not
// decompress every archived file to read its first sixty-four bytes: the map
// census reads two short ranges out of each TNT and would otherwise decode
// more than a gigabyte to collect a few kilobytes of headers.
type RangeReader interface {
	ReadFileRange(name string, offset int64, length int) ([]byte, error)
}

// ReadFileRange returns length bytes of name starting at offset. length < 0
// means "to the end of the file", and a length that runs past the end is
// clamped to it — so the allocation follows the file, never the caller's word.
// An offset exactly at the end is an empty read; an offset past it is refused.
// Every provider answers the same way, because the caller cannot tell whether
// the winning entry is loose or archived. A provider that can decode part of a
// file does so; otherwise the file is read whole and sliced, which is what the
// caller would have had to do anyway.
func (f *FS) ReadFileRange(name string, offset int64, length int) ([]byte, error) {
	if offset < 0 {
		return nil, fmt.Errorf("%w: negative offset %d", ErrNotFound, offset)
	}
	logical, err := cleanPath(name)
	if err != nil {
		return nil, err
	}
	for _, mount := range f.orderedMounts() {
		entry, ok := mount.provider.lookup(logical)
		if !ok {
			continue
		}
		if entry.readRange != nil {
			return entry.readRange(offset, length)
		}
		break
	}
	// The fallback keeps the same ceiling the archive reader uses.
	data, err := f.ReadFileLimit(name, defaultMaxFileBytes)
	if err != nil {
		return nil, err
	}
	if offset > int64(len(data)) {
		return nil, fmt.Errorf("%w: range offset %d outside %d-byte file", ErrNotFound, offset, len(data))
	}
	data = data[offset:]
	if length >= 0 && length < len(data) {
		data = data[:length]
	}
	return data, nil
}

// Stat describes the winning entry for a logical name.
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
		for _, info := range mount.provider.retailEntries() {
			if info.Path == "" || !strings.HasPrefix(info.Path, prefix) {
				continue
			}
			rest := strings.TrimPrefix(info.Path, prefix)
			if rest == "" || strings.Contains(rest, "/") {
				continue
			}
			key := foldLogicalName(info.Path)
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
	// whole is the complete file when the provider materialized it to open
	// the handle. Only a provider that hands over an exclusive buffer sets
	// it; see WholeFile.
	whole []byte
}

func (h *fileHandle) Read(p []byte) (int, error)                { return h.reader.Read(p) }
func (h *fileHandle) ReadAt(p []byte, off int64) (int, error)   { return h.readerAt.ReadAt(p, off) }
func (h *fileHandle) Seek(off int64, whence int) (int64, error) { return h.reader.Seek(off, whence) }
func (h *fileHandle) Close() error                              { return h.closeFn() }
func (h *fileHandle) Info() EntryInfo                           { return h.info }

// Whole implements WholeFile for a provider that decoded the file in one
// piece. It reports false for a handle streaming from the host filesystem.
func (h *fileHandle) Whole() ([]byte, bool) {
	if h.whole == nil {
		return nil, false
	}
	return h.whole, true
}

func newOSFile(filename string, info EntryInfo) (File, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	return &fileHandle{reader: file, readerAt: file, closeFn: file.Close, info: info}, nil
}

type looseProvider struct {
	root string
	providerIndex
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
	p := &looseProvider{root: absolute, providerIndex: newProviderIndex()}
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
			entry.readRange = func(offset int64, length int) ([]byte, error) {
				return readFileRangeOS(full, offset, length)
			}
		} else if err := p.indexDir(full, logical, priority, order); err != nil {
			return err
		}
		p.entries[logical] = entry
	}
	return nil
}

// readFileRangeOS reads a byte range from a loose file without reading the
// rest of it, under the same contract the archive reader follows: a negative
// or past-the-end offset is refused, length < 0 means "to the end", and any
// other length is clamped to what remains. The clamp is not a nicety — it is
// what keeps the allocation proportional to the file rather than to the
// caller's word, so the same "read the rest" sentinel costs the same for a
// loose file and for an archived one. The two differ only in their sentinel
// error, each naming its own provider.
func readFileRangeOS(path string, offset int64, length int) ([]byte, error) {
	handle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	stat, statErr := handle.Stat()
	if statErr != nil {
		return nil, statErr
	}
	size := stat.Size()
	if offset < 0 || offset > size {
		return nil, fmt.Errorf("%w: range offset %d outside %d-byte file", ErrNotFound, offset, size)
	}
	remaining := size - offset
	if remaining > int64(maxInt) {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrTooLarge, path, int64(maxInt))
	}
	if length < 0 || int64(length) > remaining {
		length = int(remaining)
	}
	data := make([]byte, length)
	read, err := handle.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return data[:read], nil
}

func (p *looseProvider) close() error { return nil }

func (p *looseProvider) retailEntries() []EntryInfo { return p.auditEntries() }

func (p *looseProvider) auditEntries() []EntryInfo {
	return append([]EntryInfo(nil), p.indexedEntries...)
}
