package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/install"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// contentSet is the mounted install plus the notes gathered while mounting.
type contentSet struct {
	fs           *vfs.FS
	root         string
	roots        []string
	notes        []string
	translations *content.TranslationTable
}

func (c *contentSet) Close() error {
	if c.fs == nil {
		return nil
	}
	return c.fs.Close()
}

// missingProductError is the standard diagnostic shape from
// AGENTS.md §Diagnostics: what failed, the logical path, the providers
// searched, and what was expected.
type missingProductError struct {
	what      string
	logical   string
	providers []string
	expected  string
}

func (e *missingProductError) Error() string {
	providers := "none"
	if len(e.providers) > 0 {
		providers = strings.Join(e.providers, ", ")
	}
	return fmt.Sprintf("nanolathe: %s: logical path %s, providers searched [%s], expected %s",
		e.what, e.logical, providers, e.expected)
}

// openContent mounts a retail install. The archives live at the install root;
// the loose gamedata directory on a real install is empty, so never probe for
// it on disk (PLAN_00 C6).
func openContent(opts Options) (*contentSet, error) {
	explicit := opts.Roots
	if len(explicit) == 0 && opts.Root != "" {
		explicit = []string{opts.Root}
	}
	roots, err := install.Resolve(explicit)
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, &missingProductError{
				what: "install root is not readable", logical: root,
				providers: roots,
				expected:  "a Total Annihilation content directory (set --root or $NANOLATHE_TA_ROOT)",
			}
		}
	}
	fileSystem := vfs.New()
	if err := fileSystem.MountGameDirectories(roots); err != nil {
		fileSystem.Close()
		return nil, &missingProductError{what: "mounting content failed: " + err.Error(), logical: "<content roots>", providers: roots, expected: "readable content directories and archives"}
	}

	if opts.Remaster != "" {
		if err := mountRemaster(fileSystem, opts.Remaster); err != nil {
			fileSystem.Close()
			return nil, err
		}
	}
	set := &contentSet{fs: fileSystem, root: roots[0], roots: append([]string(nil), roots...), notes: fileSystem.Notes()}

	// One required product proves the mount produced game data rather than an
	// empty directory. MOVEINFO.TDF and SIDEDATA.TDF are the hard requirements
	// [02 §1]; GAMEDATA.TDF is not — it does not exist in a real install
	// (docs/SPEC_CONFLICTS.md SC2).
	for _, required := range []string{"gamedata/moveinfo.tdf", "gamedata/sidedata.tdf"} {
		if _, err := fileSystem.Stat(required); err != nil {
			set.Close()
			return nil, &missingProductError{
				what:      "required content is missing",
				logical:   required,
				providers: providerNames(fileSystem),
				expected:  "a mounted archive or loose file supplying it",
			}
		}
	}
	// The modeled startup state selects retail's literal lowercase English
	// default before GUI parsing. TODO(T25): host command-line/registry
	// non-default language selection has not been integrated yet.
	translations, err := content.LoadTranslationTable(fileSystem, "english")
	if err != nil {
		set.Close()
		return nil, fmt.Errorf("nanolathe: loading default GUI translation table: %w", err)
	}
	set.translations = translations

	// The modern renderer's material annotation is authored presentation data
	// (docs/DESIGN_GPU_RENDERER.md §29.1). An install may replace the embedded
	// table by supplying client.MaterialTablePath; a broken override is
	// reported and ignored, because presentation art must never fail a load.
	if err := client.LoadMaterialTable(fileSystem); err != nil {
		set.notes = append(set.notes, err.Error())
		fmt.Fprintln(os.Stderr, err)
	}
	return set, nil
}

func (c *contentSet) loadGUI(name string) (*gui.Window, error) {
	if c == nil {
		return nil, fmt.Errorf("nanolathe: GUI load: no mounted content")
	}
	return gui.LoadWithTranslation(c.fs, name, c.translations)
}

// providerNames lists the mounted providers in precedence order, which is what
// a "providers searched" diagnostic means. It is deliberately not per-entry
// source paths: for a loose mount those are individual files.
func providerNames(fileSystem *vfs.FS) []string {
	providers := fileSystem.Providers()
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, provider.ID)
	}
	return names
}

// remasterPriority is the legacy minimum override priority. mountRemaster
// raises it above the highest root when the root list spans more tiers.
const remasterPriority = 1000

// mountRemaster mounts a user-supplied loose art override or packed archive.
// Art-only overrides leave the retail unit definitions unchanged.
func mountRemaster(fileSystem *vfs.FS, path string) error {
	priority := remasterPriority
	for _, provider := range fileSystem.Providers() {
		priority = max(priority, provider.Priority+1)
	}
	info, err := os.Stat(path)
	if err != nil {
		return &missingProductError{what: "remaster override is not readable", logical: path, expected: "a loose art directory or .hpi archive"}
	}
	if info.IsDir() {
		if err := fileSystem.MountDirectory(path, priority); err != nil {
			return fmt.Errorf("nanolathe: mounting remaster directory %s: %w", path, err)
		}
		return nil
	}
	if _, err := fileSystem.MountArchive(path, priority); err != nil {
		return fmt.Errorf("nanolathe: mounting remaster archive %s: %w", path, err)
	}
	return nil
}
