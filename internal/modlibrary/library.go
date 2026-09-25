package modlibrary

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Receipt is install.json: where an installed mod came from and when
// (DESIGN_MODS_MUTATORS §4.1). SHA256 is the archive identity a save records
// (§5.4); a directory install has none.
type Receipt struct {
	SHA256    string    `json:"sha256,omitempty"`
	Size      int64     `json:"size,omitempty"`
	Source    string    `json:"source,omitempty"` // URL or "local:<original file name>"
	Installed time.Time `json:"installed"`
}

// Mod is one installed mod version.
type Mod struct {
	Metadata
	Dir     string // the extracted content root: pass this as an extra --root
	Receipt Receipt
	Local   bool // installed without metadata (P11)
}

// Extraction caps (§5.3 step 3, P12). A violation refuses the install.
const (
	MaxUncompressedBytes int64 = 4 << 30
	MaxEntries                 = 100_000
)

// stagingName is the library's scratch directory for extractions and
// removals in progress. Partial downloads are not kept here: a download is
// written to <library>/.downloads/<id>-<version>.zip.part and kept after an
// interruption so a later attempt resumes it (DESIGN_MODS_MUTATORS §5.3).
// Staging holds only work that dies with the process, so the first Open of a
// library in each process clears it (see Open).
const stagingName = ".staging"

// staging tracks, per library root, whether this process has cleared the
// staging directory yet and how many installs are extracting into it. It is
// process-wide because every Open of one root shares one directory: the
// desktop command opens the library on each mount and each Mods screen, and a
// later Open must never delete an install that an earlier one started (§4.1).
var staging = struct {
	sync.Mutex
	cleared  map[string]bool
	installs map[string]int
}{cleared: map[string]bool{}, installs: map[string]int{}}

// stagingKey names one library root for the staging bookkeeping.
func stagingKey(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return filepath.Clean(root)
}

// beginInstall records an install extracting into root's staging directory
// until the returned function is called.
func beginInstall(root string) func() {
	key := stagingKey(root)
	staging.Lock()
	staging.installs[key]++
	staging.Unlock()
	return func() {
		staging.Lock()
		staging.installs[key]--
		staging.Unlock()
	}
}

// ErrAlreadyInstalled reports an install whose <id>/<version>/ already
// exists. Versions are immutable once installed; remove first to reinstall.
var ErrAlreadyInstalled = errors.New("mod version already installed")

// ErrNotInstalled reports a Remove of a version that is not installed.
var ErrNotInstalled = errors.New("mod version not installed")

// DefaultRoot is the library location: $XDG_DATA_HOME/nanolathe/mods, else
// ~/.local/share/nanolathe/mods. Like the settings file, the XDG layout is
// used on every platform so the library lives in one documented place (§4.1).
func DefaultRoot() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", diagnostic("locating the home directory failed: "+err.Error(), "<mod library>", []string{"$XDG_DATA_HOME", "$HOME"}, "a data directory for the mod library")
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "nanolathe", "mods"), nil
}

// Library is the mod data directory. The directory is the index (§4.1): an
// installed mod is an <id>/<version>/ directory whose metadata parses and
// whose receipt exists, and nothing else records the set.
type Library struct {
	Root string

	// now stamps receipts; tests pin it to order installs.
	now func() time.Time
	// maxBytes and maxEntries are the extraction caps, lowered by tests.
	maxBytes   int64
	maxEntries int
}

// Open prepares the library at root: it creates the directory and, the first
// time this process opens that root, clears the staging area, which then can
// only hold the leftovers of an extraction or removal that an earlier process
// did not finish. Later Opens of the same root leave staging alone, and no
// Open clears it while an install of this process is extracting into it, so
// opening the library again never cuts an install off. Open may be called as
// often as convenient.
func Open(root string) (*Library, error) {
	if strings.TrimSpace(root) == "" {
		return nil, diagnostic("mod library root is empty", "<mod library>", nil, "a data directory path")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, diagnostic("creating the mod library failed: "+err.Error(), root, nil, "a writable data directory")
	}
	lib := &Library{Root: root, now: time.Now, maxBytes: MaxUncompressedBytes, maxEntries: MaxEntries}
	dir := lib.StagingDir()
	key := stagingKey(root)
	staging.Lock()
	defer staging.Unlock()
	if !staging.cleared[key] && staging.installs[key] == 0 {
		if err := os.RemoveAll(dir); err != nil {
			return nil, diagnostic("clearing mod staging failed: "+err.Error(), dir, nil, "a removable staging directory")
		}
		staging.cleared[key] = true
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, diagnostic("creating mod staging failed: "+err.Error(), dir, nil, "a writable staging directory")
	}
	return lib, nil
}

// StagingDir is where extractions and removals in progress live. Nothing
// else belongs there: a download is kept in <library>/.downloads so that it
// survives for a resume, and InstallArchive never deletes its input.
func (l *Library) StagingDir() string { return filepath.Join(l.Root, stagingName) }

// modDir is the committed location of one version.
func (l *Library) modDir(id, version string) string { return filepath.Join(l.Root, id, version) }

// Installed lists every installed mod version, sorted by name (case
// folded, then exact), then by install time ascending, then by id and
// version. A directory that is not a complete install — missing or
// unparsable metadata, no receipt, or metadata naming a different id or
// version than its directory — is not listed.
func (l *Library) Installed() ([]Mod, error) {
	ids, err := os.ReadDir(l.Root)
	if err != nil {
		return nil, diagnostic("reading the mod library failed: "+err.Error(), l.Root, nil, "a readable mod library directory")
	}
	var mods []Mod
	for _, idEntry := range ids {
		id := idEntry.Name()
		if !idEntry.IsDir() || !idPattern.MatchString(id) {
			continue // manifest.json, .staging and anything foreign
		}
		versions, err := os.ReadDir(filepath.Join(l.Root, id))
		if err != nil {
			continue
		}
		for _, versionEntry := range versions {
			if !versionEntry.IsDir() {
				continue
			}
			if mod, ok := l.readMod(id, versionEntry.Name()); ok {
				mods = append(mods, mod)
			}
		}
	}
	sort.SliceStable(mods, func(i, j int) bool {
		a, b := mods[i], mods[j]
		if fa, fb := strings.ToLower(a.Name), strings.ToLower(b.Name); fa != fb {
			return fa < fb
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if !a.Receipt.Installed.Equal(b.Receipt.Installed) {
			return a.Receipt.Installed.Before(b.Receipt.Installed)
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Version < b.Version
	})
	return mods, nil
}

// readMod loads one <id>/<version>/ directory, or reports it incomplete.
func (l *Library) readMod(id, version string) (Mod, bool) {
	dir := l.modDir(id, version)
	data, err := readLimited(filepath.Join(dir, MetadataFile), maxMetadataBytes)
	if err != nil {
		return Mod{}, false
	}
	meta, err := ParseMetadata(data)
	if err != nil || meta.ID != id || meta.Version != version {
		return Mod{}, false
	}
	receiptBytes, err := readLimited(filepath.Join(dir, ReceiptFile), maxMetadataBytes)
	if err != nil {
		return Mod{}, false
	}
	var receipt Receipt
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		return Mod{}, false
	}
	return Mod{Metadata: meta, Dir: dir, Receipt: receipt, Local: meta.isLocal()}, true
}

// Lookup finds an installed version. An empty version selects the most
// recently installed version of the id (§4.3 "A missing version selects the
// newest installed version"): newest by receipt time, the later listing
// order breaking a tie.
func (l *Library) Lookup(id, version string) (Mod, bool, error) {
	mods, err := l.Installed()
	if err != nil {
		return Mod{}, false, err
	}
	var found Mod
	ok := false
	for _, mod := range mods {
		if mod.ID != id {
			continue
		}
		if version != "" {
			if mod.Version == version {
				return mod, true, nil
			}
			continue
		}
		if !ok || !mod.Receipt.Installed.Before(found.Receipt.Installed) {
			found, ok = mod, true
		}
	}
	return found, ok, nil
}

// Remove deletes one installed version (P9: nothing is removed implicitly).
// The version directory is first renamed into staging, so it disappears from
// the library in one step even if the deletion that follows is interrupted;
// the next process's first Open clears whatever is left. The <id>/ directory
// goes when it empties.
func (l *Library) Remove(id, version string) error {
	if !idPattern.MatchString(id) || !versionPattern.MatchString(version) {
		return diagnostic(fmt.Sprintf("mod %s@%s cannot be removed", id, version), filepath.Join(l.Root, id, version), []string{l.Root}, "an installed mod id and an explicit version")
	}
	dir := l.modDir(id, version)
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return &diagError{what: fmt.Sprintf("mod %s@%s is not installed", id, version), logical: dir, providers: []string{l.Root}, expected: "an installed mod version", wrapped: ErrNotInstalled}
	}
	if err := os.MkdirAll(l.StagingDir(), 0o755); err != nil {
		return diagnostic("creating mod staging failed: "+err.Error(), l.StagingDir(), nil, "a writable staging directory")
	}
	trash, err := os.MkdirTemp(l.StagingDir(), "remove-*")
	if err != nil {
		return diagnostic("creating a removal area failed: "+err.Error(), l.StagingDir(), nil, "a writable staging directory")
	}
	moved := filepath.Join(trash, "mod")
	if err := os.Rename(dir, moved); err != nil {
		_ = os.Remove(trash)
		return diagnostic("removing the mod failed: "+err.Error(), dir, []string{l.Root}, "a removable mod directory")
	}
	// The version is gone from the library once the rename succeeds; a failed
	// deletion only leaves staging litter that a later process clears.
	_ = os.RemoveAll(trash)
	_ = os.Remove(filepath.Join(l.Root, id)) // fails harmlessly while other versions remain
	return nil
}

// readLimited reads a small file, refusing one larger than limit.
func readLimited(name string, limit int64) ([]byte, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fs.ErrInvalid
	}
	return os.ReadFile(name)
}
