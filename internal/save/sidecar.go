package save

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/internal/community"
)

// The save sidecar is a Nanolathe file beside a retail bank that records the
// match selection the bank was written under: the mod, content profile, rule
// set, Community sources and entry table, unit limit and mutators
// (docs/DESIGN_MODS_MUTATORS.md §7). The bank's bytes are unchanged; the
// retail enumerator lists only `*.SAV`, so the sidecar never shows as a save.

// SidecarSuffix is appended to the bank's full file name:
// `SAVEGAME/<name>.SAV` gets `SAVEGAME/<name>.SAV.nanolathe.json` (§7.1).
const SidecarSuffix = ".nanolathe.json"

// SidecarSchema is the one layout this build reads and writes. A sidecar with
// another schema is refused rather than half-read (§7.3).
const SidecarSchema = 1

// sidecarMaxBytes bounds a read: a sidecar is a few kilobytes at most.
const sidecarMaxBytes = 1 << 20

// SidecarMod names the installed mod a save was written under.
type SidecarMod struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	SHA256  string `json:"sha256,omitempty"`
}

// SidecarModRef is the sidecar's `mod` value: null for no mod, an object
// for an installed mod, and the string "custom" for a command line that
// stacked its own content roots (§7.2).
type SidecarModRef struct {
	Mod    *SidecarMod
	Custom bool
}

const sidecarCustomMod = "custom"

func (r SidecarModRef) MarshalJSON() ([]byte, error) {
	switch {
	case r.Custom:
		return json.Marshal(sidecarCustomMod)
	case r.Mod == nil:
		return []byte("null"), nil
	}
	return json.Marshal(r.Mod)
}

func (r *SidecarModRef) UnmarshalJSON(data []byte) error {
	*r = SidecarModRef{}
	data = bytes.TrimSpace(data)
	switch {
	case bytes.Equal(data, []byte("null")):
		return nil
	case len(data) > 0 && data[0] == '"':
		var word string
		if err := json.Unmarshal(data, &word); err != nil {
			return err
		}
		if word != sidecarCustomMod {
			return fmt.Errorf("mod is %q, expected null, an object or %q", word, sidecarCustomMod)
		}
		r.Custom = true
		return nil
	}
	var mod SidecarMod
	if err := json.Unmarshal(data, &mod); err != nil {
		return err
	}
	if mod.ID == "" || mod.Version == "" {
		return errors.New("mod names no id or version")
	}
	r.Mod = &mod
	return nil
}

// SidecarCommunitySources are the Community declarations the session
// resolved its tables from, in precedence order (DESIGN_COMMUNITY_PATCH §3.2).
type SidecarCommunitySources struct {
	Content     []community.Overrides `json:"content,omitempty"`
	Player      community.Overrides   `json:"player"`
	CommandLine []community.Overrides `json:"commandLine,omitempty"`
}

// SidecarCommunity records the session's sources and its battle-entry table,
// so a restored battle resolves and switches exactly as the saved one did.
type SidecarCommunity struct {
	Sources SidecarCommunitySources `json:"sources"`
	Entry   community.Features      `json:"entry"`
}

// Sidecar is the whole file (§7.2).
type Sidecar struct {
	Schema          int               `json:"schema"`
	Profile         string            `json:"profile"`
	Mod             SidecarModRef     `json:"mod"`
	ContentProfile  string            `json:"contentProfile,omitempty"`
	ContentManifest string            `json:"contentManifest,omitempty"`
	Catalog         string            `json:"catalog,omitempty"`
	Rules           string            `json:"rules"`
	Gameplay        string            `json:"gameplay"`
	Community       SidecarCommunity  `json:"community"`
	UnitLimit       int               `json:"unitLimit"`
	Mutators        map[string]string `json:"mutators"`
}

// SidecarPath is the sidecar file of the bank at bankPath.
func SidecarPath(bankPath string) string { return bankPath + SidecarSuffix }

// WriteSidecar writes the sidecar of the bank at bankPath through a
// temporary file and a rename, so a reader never sees half a file (§7.1).
func WriteSidecar(bankPath string, s Sidecar) error {
	s.Schema = SidecarSchema
	if s.Mutators == nil {
		s.Mutators = map[string]string{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	target := SidecarPath(bankPath)
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err == nil {
		err = os.Rename(name, target)
	}
	if err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// ReadSidecar reads the sidecar of the bank at bankPath. A missing sidecar,
// as with every retail save and every older Nanolathe save, reports false
// and no error (§7.3 step 1). A sidecar that exists but cannot be read, or
// that another schema wrote, is an error: loading it as if it were absent
// would silently drop the selection it records.
func ReadSidecar(bankPath string) (Sidecar, bool, error) {
	path := SidecarPath(bankPath)
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Sidecar{}, false, nil
	}
	if err != nil {
		return Sidecar{}, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, sidecarMaxBytes+1))
	if err != nil {
		return Sidecar{}, false, err
	}
	if len(data) > sidecarMaxBytes {
		return Sidecar{}, false, sidecarError(path, "file is larger than 1 MiB")
	}
	var s Sidecar
	if err := json.Unmarshal(data, &s); err != nil {
		return Sidecar{}, false, sidecarError(path, err.Error())
	}
	if s.Schema != SidecarSchema {
		return Sidecar{}, false, sidecarError(path, fmt.Sprintf("schema %d", s.Schema))
	}
	return s, true, nil
}

// RemoveSidecar deletes the sidecar of the bank at bankPath, if any. It is
// called wherever the bank itself is deleted or overwritten (§7.1).
func RemoveSidecar(bankPath string) error {
	err := os.Remove(SidecarPath(bankPath))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func sidecarError(path, what string) error {
	return fmt.Errorf("nanolathe: save sidecar unreadable: %s: logical path %s, providers searched [save directory], expected a schema %d Nanolathe sidecar", what, path, SidecarSchema)
}
