package modlibrary

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// InstallOptions carries what the caller knows about a package before it is
// opened. A catalogue download fills Expect, SHA256, Size and Source from its
// manifest entry; a dropped file or folder usually fills only Validate.
type InstallOptions struct {
	// Expect, when non-nil, is the catalogue entry the archive must agree
	// with on id, version, contentProfile, minimumGameplay and controls
	// (§5.1). A package without metadata cannot agree and is refused.
	Expect *Metadata
	// SHA256, when non-empty, is verified before extraction (§5.3 step 2).
	SHA256 string
	// Size, when non-zero, is verified before extraction.
	Size int64
	// Source is recorded in the receipt: the download URL, or empty for
	// "local:<original file name>".
	Source string
	// Validate runs on the staged content root before anything is committed
	// (§5.3 step 4). ContentValidator is the standard one; nil skips.
	Validate func(stagedRoot string, meta Metadata) error
	// Progress reports extracted bytes against the package's total; may be nil.
	Progress func(done, total int64)
}

// archiveExtensions are the HPI-family containers a content root holds at
// its top level. The list mirrors the extensions vfs mounts from a game
// directory (vfs.FS.MountGameDirectories); it only decides wrapper stripping
// here, never what is mounted.
var archiveExtensions = map[string]bool{
	".hpi": true, ".ufo": true, ".ccx": true, ".gp3": true, ".gp4": true, ".gpf": true, ".swx": true,
}

// executableExtensions are skipped on extraction (§5.3 step 3). Nanolathe
// never runs them, and hosted zips should contain none (§5.5).
var executableExtensions = map[string]bool{
	".exe": true, ".dll": true, ".com": true, ".bat": true, ".cmd": true, ".scr": true,
	".msi": true, ".ps1": true, ".sh": true, ".dylib": true, ".so": true,
}

// sourceEntry is one item of a package as its source spells it, before any
// rule has looked at it.
type sourceEntry struct {
	name string      // raw name, either slash style
	dir  bool        // a directory entry
	mode fs.FileMode // type bits; a symlink or device is refused
	size uint64      // declared uncompressed size of a file
	open func() (io.ReadCloser, error)
}

// plannedEntry is an accepted item at its final path under the content root.
type plannedEntry struct {
	path string // cleaned, slash-separated, relative to the content root
	dir  bool
	size uint64
	open func() (io.ReadCloser, error)
}

// InstallArchive installs a zip through the §5.3 path: verify the archive
// identity, plan the entries under the extraction rules, validate the
// metadata, extract into a unique staging directory, run Validate, write the
// receipt and rename the result to <id>/<version>/. Nothing is visible in the
// library until that rename. The zip itself is never deleted: a download's
// caller removes it after a successful install.
func (l *Library) InstallArchive(zipPath string, opts InstallOptions) (Mod, error) {
	provider := filepath.Base(zipPath)
	info, err := os.Stat(zipPath)
	if err != nil || !info.Mode().IsRegular() {
		return Mod{}, diagnostic("mod archive is not readable", zipPath, []string{provider}, "a readable .zip file")
	}
	if opts.Size != 0 && info.Size() != opts.Size {
		return Mod{}, diagnostic(fmt.Sprintf("mod archive size %d does not match the catalogue's %d", info.Size(), opts.Size), provider, []string{provider}, "the archive the catalogue describes")
	}
	sum, err := fileSHA256(zipPath)
	if err != nil {
		return Mod{}, diagnostic("hashing the mod archive failed: "+err.Error(), provider, []string{provider}, "a readable .zip file")
	}
	if opts.SHA256 != "" && !strings.EqualFold(sum, opts.SHA256) {
		return Mod{}, diagnostic(fmt.Sprintf("mod archive SHA-256 %s does not match the catalogue's %s", sum, strings.ToLower(opts.SHA256)), provider, []string{provider}, "the archive the catalogue describes")
	}
	reader, err := zip.OpenReader(zipPath)
	// archive/zip reports ErrInsecurePath alongside a usable reader when the
	// host opts into that check; the planner applies its own, stricter rules
	// to every name either way.
	if err != nil && !(errors.Is(err, zip.ErrInsecurePath) && reader != nil) {
		return Mod{}, diagnostic("opening the mod archive failed: "+err.Error(), provider, []string{provider}, "a valid .zip file")
	}
	defer reader.Close()
	if maxEntries := l.caps(); len(reader.File) > maxEntries {
		return Mod{}, l.entryCapError(provider)
	}
	entries := make([]sourceEntry, 0, len(reader.File))
	for _, file := range reader.File {
		dir := strings.HasSuffix(file.Name, "/") || strings.HasSuffix(file.Name, "\\") || file.Mode().IsDir()
		entries = append(entries, sourceEntry{
			name: file.Name, dir: dir, mode: file.Mode().Type(), size: file.UncompressedSize64, open: file.Open,
		})
	}
	receipt := Receipt{SHA256: sum, Size: info.Size(), Source: opts.Source}
	return l.install(provider, strings.TrimSuffix(provider, filepath.Ext(provider)), entries, opts, receipt)
}

// InstallDirectory installs a folder through the same rules as an archive:
// the tree is walked without following links, planned, copied into staging,
// validated and committed. SHA256 and Size describe archives and are refused
// here.
func (l *Library) InstallDirectory(dir string, opts InstallOptions) (Mod, error) {
	provider := filepath.Base(filepath.Clean(dir))
	if opts.SHA256 != "" || opts.Size != 0 {
		return Mod{}, diagnostic("an archive identity cannot verify a folder", dir, []string{provider}, "a .zip file for a catalogue install")
	}
	// The chosen folder itself may be an alias; links inside it are refused.
	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil {
		dir = resolved
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Mod{}, diagnostic("mod folder is not readable", dir, []string{provider}, "a readable folder")
	}
	var entries []sourceEntry
	errCap := errors.New("entry cap")
	maxEntries := l.caps()
	err = filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == dir {
			return nil
		}
		if len(entries) >= maxEntries {
			return errCap
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		entry := sourceEntry{name: filepath.ToSlash(rel), dir: d.IsDir(), mode: d.Type()}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			entry.size = uint64(info.Size())
			entry.open = func() (io.ReadCloser, error) { return os.Open(name) }
		}
		entries = append(entries, entry)
		return nil
	})
	if errors.Is(err, errCap) {
		return Mod{}, l.entryCapError(provider)
	}
	if err != nil {
		return Mod{}, diagnostic("reading the mod folder failed: "+err.Error(), dir, []string{provider}, "a readable folder")
	}
	return l.install(provider, provider, entries, opts, Receipt{Source: opts.Source})
}

func (l *Library) entryCapError(provider string) error {
	maxEntries := l.caps()
	return diagnostic(fmt.Sprintf("mod package has more than %d entries", maxEntries), provider, []string{provider}, fmt.Sprintf("at most %d entries", maxEntries))
}

// caps returns the entry cap; a Library built without Open gets the
// defaults for both caps.
func (l *Library) caps() int {
	if l.maxBytes == 0 {
		l.maxBytes = MaxUncompressedBytes
	}
	if l.maxEntries == 0 {
		l.maxEntries = MaxEntries
	}
	return l.maxEntries
}

// install is the shared path behind both entry points.
func (l *Library) install(provider, baseName string, entries []sourceEntry, opts InstallOptions, receipt Receipt) (Mod, error) {
	planned, total, err := l.plan(provider, entries)
	if err != nil {
		return Mod{}, err
	}
	meta, generated, err := packageMetadata(provider, baseName, planned)
	if err != nil {
		return Mod{}, err
	}
	if opts.Expect != nil {
		if generated {
			return Mod{}, diagnostic("mod archive has no metadata to check against the catalogue", MetadataFile, []string{provider}, "a nanolathe-mod.json at the archive root")
		}
		if err := expectMatches(provider, meta, *opts.Expect); err != nil {
			return Mod{}, err
		}
	}
	target := l.modDir(meta.ID, meta.Version)
	if err := l.refuseExisting(target, meta); err != nil {
		return Mod{}, err
	}

	// Staging is shared by every Open of this root; Open leaves it alone while
	// this install is extracting into it.
	defer beginInstall(l.Root)()
	if err := os.MkdirAll(l.StagingDir(), 0o755); err != nil {
		return Mod{}, diagnostic("creating mod staging failed: "+err.Error(), l.StagingDir(), nil, "a writable staging directory")
	}
	staged, err := os.MkdirTemp(l.StagingDir(), "extract-"+meta.ID+"-"+meta.Version+"-*")
	if err != nil {
		return Mod{}, diagnostic("creating a staging directory failed: "+err.Error(), l.StagingDir(), nil, "a writable staging directory")
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staged)
		}
	}()

	if err := extract(staged, provider, planned, total, opts.Progress); err != nil {
		return Mod{}, err
	}
	if generated {
		data, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return Mod{}, err
		}
		if err := os.WriteFile(filepath.Join(staged, MetadataFile), append(data, '\n'), 0o644); err != nil {
			return Mod{}, diagnostic("writing generated mod metadata failed: "+err.Error(), MetadataFile, []string{provider}, "a writable staging directory")
		}
	}
	if opts.Validate != nil {
		if err := opts.Validate(staged, meta); err != nil {
			return Mod{}, err
		}
	}

	if receipt.Source == "" {
		receipt.Source = "local:" + provider
	}
	now := l.now
	if now == nil {
		now = time.Now
	}
	receipt.Installed = now().UTC()
	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return Mod{}, err
	}
	if err := os.WriteFile(filepath.Join(staged, ReceiptFile), append(receiptBytes, '\n'), 0o644); err != nil {
		return Mod{}, diagnostic("writing the install receipt failed: "+err.Error(), ReceiptFile, []string{provider}, "a writable staging directory")
	}

	// Commit. The rename is within one filesystem, so an interrupted install
	// leaves only staging litter and never a half-installed version.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Mod{}, diagnostic("creating the mod directory failed: "+err.Error(), filepath.Dir(target), []string{l.Root}, "a writable mod library")
	}
	if err := l.refuseExisting(target, meta); err != nil {
		return Mod{}, err
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Remove(filepath.Dir(target)) // only succeeds if this install created it
		return Mod{}, diagnostic("committing the mod failed: "+err.Error(), target, []string{l.Root}, "a writable mod library")
	}
	committed = true
	return Mod{Metadata: meta, Dir: target, Receipt: receipt, Local: generated}, nil
}

func (l *Library) refuseExisting(target string, meta Metadata) error {
	if _, err := os.Lstat(target); err == nil {
		return &diagError{
			what:      fmt.Sprintf("mod %s@%s is already installed", meta.ID, meta.Version),
			logical:   target,
			providers: []string{l.Root},
			expected:  "a version that is not installed yet (remove the installed one first)",
			wrapped:   ErrAlreadyInstalled,
		}
	}
	return nil
}

// plan applies the §5.3 step 3 rules to every entry and returns what will be
// extracted and its declared byte total. The order of checks is fixed: an
// unsafe name or a link is refused even where a skip rule would have dropped
// it, so a hostile entry cannot hide behind __MACOSX.
func (l *Library) plan(provider string, entries []sourceEntry) ([]plannedEntry, int64, error) {
	unsafe := func(name, reason string) error {
		return diagnostic("mod package entry is unsafe ("+reason+")", name, []string{provider}, "relative paths inside the mod root, without links")
	}
	planned := make([]plannedEntry, 0, len(entries))
	for _, entry := range entries {
		clean, err := cleanRelative(entry.name)
		if err != nil {
			return nil, 0, unsafe(entry.name, err.Error())
		}
		if entry.mode&fs.ModeSymlink != 0 {
			return nil, 0, unsafe(entry.name, "symbolic link")
		}
		if !entry.dir && entry.mode&fs.ModeType != 0 {
			return nil, 0, unsafe(entry.name, "not a regular file")
		}
		if clean == "" {
			continue // the root's own directory entry
		}
		if skipEntry(clean, entry.dir) {
			continue
		}
		planned = append(planned, plannedEntry{path: clean, dir: entry.dir, size: entry.size, open: entry.open})
	}

	planned = stripWrapper(planned)

	// Root-level reserved names: the metadata keeps one canonical spelling so
	// the library can read it on a case-sensitive host, and a packaged
	// receipt is dropped because the library writes its own.
	kept := planned[:0]
	for _, entry := range planned {
		if !entry.dir && !strings.Contains(entry.path, "/") {
			if strings.EqualFold(entry.path, ReceiptFile) {
				continue
			}
			if strings.EqualFold(entry.path, MetadataFile) {
				entry.path = MetadataFile
			}
		}
		kept = append(kept, entry)
	}
	planned = kept

	if err := refuseFoldedDuplicates(provider, planned); err != nil {
		return nil, 0, err
	}
	l.caps()
	var total uint64
	files := 0
	for _, entry := range planned {
		if entry.dir {
			continue
		}
		files++
		total += entry.size
		if total > uint64(l.maxBytes) {
			return nil, 0, diagnostic(fmt.Sprintf("mod package expands to more than %d bytes", l.maxBytes), provider, []string{provider}, fmt.Sprintf("at most %d uncompressed bytes", l.maxBytes))
		}
	}
	if files == 0 {
		return nil, 0, diagnostic("mod package contains no files", provider, []string{provider}, "a content root of archives or loose content")
	}
	return planned, int64(total), nil
}

// skipEntry reports the §5.3 skips: macOS archive litter and executables or
// libraries, which Nanolathe never runs.
func skipEntry(clean string, dir bool) bool {
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		if strings.EqualFold(part, "__MACOSX") {
			return true
		}
	}
	if dir {
		return false
	}
	base := parts[len(parts)-1]
	return strings.EqualFold(base, ".DS_Store") || executableExtensions[strings.ToLower(path.Ext(base))]
}

// stripWrapper removes one enclosing folder. The hosted contract has no
// wrapper (§5.5), but real-world zips and dragged folders often put the
// content root inside a single top-level folder; as a manual-install
// convenience, when every entry sits under one folder and that folder itself
// holds nanolathe-mod.json or an HPI-family archive, the folder is dropped.
// A folder holding only loose directories is left alone: nothing then shows
// that it, rather than its parent, is the content root.
func stripWrapper(entries []plannedEntry) []plannedEntry {
	wrapper := ""
	for _, entry := range entries {
		top, _, nested := strings.Cut(entry.path, "/")
		if !nested && !entry.dir {
			return entries // a root-level file: the root is the content root
		}
		if wrapper == "" {
			wrapper = top
		} else if !strings.EqualFold(top, wrapper) {
			return entries
		}
	}
	if wrapper == "" {
		return entries
	}
	qualifies := false
	for _, entry := range entries {
		_, rest, nested := strings.Cut(entry.path, "/")
		if nested && !entry.dir && !strings.Contains(rest, "/") &&
			(strings.EqualFold(rest, MetadataFile) || archiveExtensions[strings.ToLower(path.Ext(rest))]) {
			qualifies = true
			break
		}
	}
	if !qualifies {
		return entries
	}
	stripped := make([]plannedEntry, 0, len(entries))
	for _, entry := range entries {
		_, rest, nested := strings.Cut(entry.path, "/")
		if !nested || rest == "" {
			continue // the wrapper's own directory entry
		}
		entry.path = rest
		stripped = append(stripped, entry)
	}
	return stripped
}

// refuseFoldedDuplicates refuses two entries whose paths are equal case
// insensitively, and a file whose path is also a directory's (§5.3 step 3):
// the overlay folds case, so the winner would depend on the host filesystem.
// Folding here is Unicode lower-casing, a superset of the overlay's ASCII
// folding that also covers what case-insensitive hosts merge.
func refuseFoldedDuplicates(provider string, entries []plannedEntry) error {
	duplicate := func(a, b string) error {
		return diagnostic(fmt.Sprintf("mod package entries %s and %s differ only in case", a, b), b, []string{provider}, "entry paths that stay distinct when case is folded")
	}
	files := make(map[string]string, len(entries))
	dirs := make(map[string]string)
	for _, entry := range entries {
		key := strings.ToLower(entry.path)
		if entry.dir {
			if previous, ok := dirs[key]; ok && previous != "" {
				return duplicate(previous, entry.path)
			}
			dirs[key] = entry.path
		} else {
			if previous, ok := files[key]; ok {
				return duplicate(previous, entry.path)
			}
			files[key] = entry.path
		}
		for parent := path.Dir(entry.path); parent != "."; parent = path.Dir(parent) {
			if _, ok := dirs[strings.ToLower(parent)]; !ok {
				dirs[strings.ToLower(parent)] = "" // implied, not an entry of its own
			}
		}
	}
	for _, entry := range entries {
		if entry.dir {
			continue
		}
		if _, ok := dirs[strings.ToLower(entry.path)]; ok {
			return duplicate(entry.path, entry.path+"/")
		}
	}
	return nil
}

// packageMetadata reads the root nanolathe-mod.json, or generates the local
// description when the package has none (P11).
func packageMetadata(provider, baseName string, planned []plannedEntry) (Metadata, bool, error) {
	for _, entry := range planned {
		if entry.dir || entry.path != MetadataFile {
			continue
		}
		if entry.size > maxMetadataBytes {
			return Metadata{}, false, diagnostic("mod metadata is too large", MetadataFile, []string{provider}, fmt.Sprintf("a metadata document of at most %d bytes", maxMetadataBytes))
		}
		reader, err := entry.open()
		if err != nil {
			return Metadata{}, false, diagnostic("reading mod metadata failed: "+err.Error(), MetadataFile, []string{provider}, "a readable metadata entry")
		}
		data, err := io.ReadAll(io.LimitReader(reader, maxMetadataBytes+1))
		reader.Close()
		if err != nil {
			return Metadata{}, false, diagnostic("reading mod metadata failed: "+err.Error(), MetadataFile, []string{provider}, "a readable metadata entry")
		}
		meta, err := ParseMetadata(data)
		if err != nil {
			return Metadata{}, false, withProvider(err, provider)
		}
		if strings.HasPrefix(meta.ID, localIDPrefix) || meta.Version == localVersion {
			return Metadata{}, false, diagnostic(fmt.Sprintf("mod metadata claims %s@%s, which is reserved for packages without metadata", meta.ID, meta.Version), MetadataFile, []string{provider}, "an id without the local- prefix and a version other than local")
		}
		return meta, false, nil
	}
	return localMetadata(baseName), true, nil
}

// expectMatches holds an archive's metadata to its catalogue entry (§5.1).
func expectMatches(provider string, got, want Metadata) error {
	for _, field := range [...]struct{ name, got, want string }{
		{"id", got.ID, want.ID},
		{"version", got.Version, want.Version},
		{"contentProfile", got.ContentProfile, want.ContentProfile},
		{"minimumGameplay", got.MinimumGameplay, want.MinimumGameplay},
		{"controls", got.Controls, want.Controls},
	} {
		if field.got != field.want {
			return diagnostic(fmt.Sprintf("mod archive metadata disagrees with the catalogue on %s (%q, catalogue %q)", field.name, field.got, field.want), MetadataFile, []string{provider}, "archive metadata that matches its catalogue entry")
		}
	}
	return nil
}

// extract writes every planned entry beneath staged. Writes go through an
// os.Root, so no entry can reach outside the staging directory even if a
// planning rule were wrong, and O_EXCL turns any residual collision on a
// case-insensitive host into a refusal rather than an overwrite. Files are
// written 0644 whatever the package says: nothing extracted is ever run.
func extract(staged, provider string, planned []plannedEntry, total int64, progress func(done, total int64)) error {
	root, err := os.OpenRoot(staged)
	if err != nil {
		return diagnostic("opening the staging directory failed: "+err.Error(), staged, nil, "a writable staging directory")
	}
	defer root.Close()
	counter := &progressCounter{total: total, report: progress}
	counter.emit()
	for _, entry := range planned {
		native := filepath.FromSlash(entry.path)
		fail := func(err error) error {
			return diagnostic("extracting a mod entry failed: "+err.Error(), entry.path, []string{provider}, "a readable entry that extracts to its declared size")
		}
		if entry.dir {
			if err := root.MkdirAll(native, 0o755); err != nil {
				return fail(err)
			}
			continue
		}
		if parent := filepath.Dir(native); parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return fail(err)
			}
		}
		if err := copyEntry(root, native, entry, counter); err != nil {
			return fail(err)
		}
	}
	return nil
}

func copyEntry(root *os.Root, native string, entry plannedEntry, counter *progressCounter) error {
	reader, err := entry.open()
	if err != nil {
		return err
	}
	defer reader.Close()
	file, err := root.OpenFile(native, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	// One byte past the declared size is read so an entry that grows past
	// its header is caught rather than silently cut.
	written, err := io.Copy(io.MultiWriter(file, counter), io.LimitReader(reader, int64(entry.size)+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if uint64(written) != entry.size {
		return fmt.Errorf("extracted %d bytes, declared %d", written, entry.size)
	}
	return nil
}

// progressCounter reports extracted bytes as they are written.
type progressCounter struct {
	done, total int64
	report      func(done, total int64)
}

func (c *progressCounter) Write(p []byte) (int, error) {
	c.done += int64(len(p))
	c.emit()
	return len(p), nil
}

func (c *progressCounter) emit() {
	if c.report != nil {
		c.report(c.done, c.total)
	}
}

// fileSHA256 is the archive identity (§5.4), lower-case hex.
func fileSHA256(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// withProvider names the package in a diagnostic raised without one.
func withProvider(err error, provider string) error {
	var diag *diagError
	if errors.As(err, &diag) && len(diag.providers) == 0 {
		copied := *diag
		copied.providers = []string{provider}
		return &copied
	}
	return err
}
