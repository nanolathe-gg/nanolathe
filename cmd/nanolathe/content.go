package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nanolathe/nanolathe/vfs"
)

// contentSet is the mounted install plus the notes gathered while mounting.
type contentSet struct {
	fs    *vfs.FS
	root  string
	notes []string
}

func (c *contentSet) Close() error {
	if c.fs == nil {
		return nil
	}
	return c.fs.Close()
}

// missingProductError is the standard diagnostic shape from
// docs/ORCHESTRATION.md §7: what failed, the logical path, the providers
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
	info, err := os.Stat(opts.Root)
	if err != nil || !info.IsDir() {
		return nil, &missingProductError{
			what:     "install root is not readable",
			logical:  opts.Root,
			expected: "a Total Annihilation install directory (set --root or $NANOLATHE_TA_ROOT)",
		}
	}

	fileSystem := vfs.New()
	if err := fileSystem.MountGameDirectory(opts.Root); err != nil {
		return nil, fmt.Errorf("nanolathe: mounting %s: %w", opts.Root, err)
	}
	set := &contentSet{fs: fileSystem, root: opts.Root, notes: fileSystem.Notes()}

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
	return set, nil
}

// providerNames lists the mounted providers in precedence order, which is what
// a "providers searched" diagnostic means. It is deliberately not per-entry
// source paths: for a loose mount those are individual files.
func providerNames(fileSystem *vfs.FS) []string {
	providers := fileSystem.Providers()
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, filepath.Base(provider.ID))
	}
	return names
}
