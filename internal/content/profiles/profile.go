// Package profiles describes a mounted content set: which directories hold
// each authored family, and how large its tables and files may be.
//
// A content profile is load-time data, not a gameplay rule set. It is selected
// before the catalog compiles and, by itself, never changes what a tick does:
// the catalog keeps asking for `units/`, `weapons/` and the rest, and the
// profile's directory table answers with whatever the content set actually
// ships. That is sanctioned content policy, documented in
// docs/DESIGN_CONTENT_VFS.md §5 "Content profiles" and listed in
// docs/INVARIANTS.md I11.
//
// The four shipped profiles are embedded JSON, so adding one is a data edit.
// Their evidence is the content sets' own documentation and configuration
// files, recorded in the E1 inventory; no third-party executable was examined.
package profiles

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

//go:embed *.json
var shipped embed.FS

// detectionOrder is the order detection walks the shipped profiles. It is an
// explicit list rather than a directory listing so ambiguity diagnostics have
// a stable order [I1]. Retail is the fallback when no renamed layout matches.
var detectionOrder = [...]string{"escalation", "prota", "zero", "retail"}

// RetailName is the profile every unmodified install resolves to.
const RetailName = "retail"

// The controls presets a profile, like mod metadata, may recommend
// (docs/DESIGN_MODS_MUTATORS.md §4.3).
const (
	ControlsCommunity = "community"
	ControlsRetail    = "retail"
)

// Limits records the table sizes and read caps a content set needs. The
// catalog compile reads Units, Weapons, TNTBytes and LOSBytes through
// content.LimitsFromProfile; UnitLimit and SearchEntries are carried for the
// consumers that will read them, so a profile is one complete description of a
// content set rather than several half-descriptions landing a unit apart.
//
// Units and Weapons are definition-table sizes; TNTBytes and LOSBytes are the
// largest map and LOS table a loader may read; UnitLimit is the per-player
// unit limit the content set is authored for; SearchEntries is the
// pathfinding step allowance it expects.
type Limits struct {
	Units         int   `json:"units"`
	Weapons       int   `json:"weapons"`
	TNTBytes      int64 `json:"tnt_bytes"`
	LOSBytes      int64 `json:"los_bytes"`
	UnitLimit     int   `json:"unit_limit"`
	SearchEntries int   `json:"search_entries"`
}

// Presentation carries optional mod-authored UI defaults, never simulation rules.
// Omitted placement_weapon_ranges keeps the Modern placement guide enabled.
type Presentation struct {
	ShowRanges            bool  `json:"show_ranges"`
	PlacementWeaponRanges *bool `json:"placement_weapon_ranges,omitempty"`
}

// Profile is one content set's load-time description.
type Profile struct {
	// Name is the profile's selector: the word `--content-profile` takes and
	// the word the reports carry.
	Name string `json:"name"`
	// Detect is the marker set. A profile is detected when every marker
	// resolves in the mounted overlay. Shipped presets name all their renamed
	// directories; the empty retail marker list denotes the fallback.
	Detect []string `json:"detect"`
	// Directories maps the retail directory the loaders ask for to the
	// directory this content set ships. Keys are the retail names in lower
	// case; values are spelled as the content set spells them, though every
	// lookup is case-insensitive anyway [02 §2].
	Directories  map[string]string   `json:"layout"`
	Limits       Limits              `json:"limits"`
	Presentation Presentation        `json:"presentation"`
	Gameplay     community.Overrides `json:"gameplay,omitempty"`
	// Controls and MinimumGameplay are the content set's recommended
	// controls preset and gameplay minimum, spelled as in mod metadata. A
	// mounted mod's own metadata wins; these apply only where it names none,
	// as a metadata-less local package does, and Controls also offers the
	// preset when the profile is mounted without a mod
	// (docs/DESIGN_MODS_MUTATORS.md §4.3).
	Controls        string `json:"controls,omitempty"`
	MinimumGameplay string `json:"minimumGameplay,omitempty"`
}

// Layout returns the first-segment redirection this profile applies. The
// retail profile's layout is empty, and an empty layout wraps nothing.
func (p Profile) Layout() vfs.Layout { return vfs.NewLayout(p.Directories) }

// Names lists the shipped profile names in detection order, for flag help and
// diagnostics.
func Names() []string {
	return append([]string(nil), detectionOrder[:]...)
}

// load reads one shipped profile. The embedded set is compiled in, so a
// failure here is a repository defect rather than a host condition.
func load(name string) (Profile, error) {
	data, err := shipped.ReadFile(name + ".json")
	if err != nil {
		return Profile{}, fmt.Errorf("nanolathe: content profile is missing from the embedded set: logical path %s.json, providers searched [internal/content/profiles], expected a shipped profile", name)
	}
	return parse(data, name+".json")
}

// parse decodes one profile document and rejects a shape no loader could use.
// Unknown fields are refused so a typo in a hand-written profile is reported
// instead of silently ignored.
func parse(data []byte, origin string) (Profile, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var profile Profile
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, fmt.Errorf("nanolathe: reading content profile failed: logical path %s, providers searched [%s], expected a content profile document: %v", origin, origin, err)
	}
	profile.Name = strings.ToLower(strings.TrimSpace(profile.Name))
	if profile.Name == "" {
		return Profile{}, fmt.Errorf("nanolathe: content profile has no name: logical path %s, providers searched [%s], expected a named content profile", origin, origin)
	}
	switch profile.Controls {
	case "", ControlsCommunity, ControlsRetail:
	default:
		return Profile{}, fmt.Errorf("nanolathe: content profile controls preset %q is unknown: logical path %s, providers searched [%s], expected %s, %s or omitted", profile.Controls, origin, origin, ControlsCommunity, ControlsRetail)
	}
	switch gameplay.Mode(profile.MinimumGameplay) {
	case "", gameplay.Strict31, gameplay.Community39, gameplay.Modern:
	default:
		return Profile{}, fmt.Errorf("nanolathe: content profile minimumGameplay %q is not a reserved gameplay word: logical path %s, providers searched [%s], expected %s, %s, %s or omitted", profile.MinimumGameplay, origin, origin, gameplay.Strict31, gameplay.Community39, gameplay.Modern)
	}
	for retail, target := range profile.Directories {
		if strings.TrimSpace(retail) == "" || strings.TrimSpace(target) == "" {
			return Profile{}, fmt.Errorf("nanolathe: content profile directory row is empty: logical path %s, providers searched [%s], expected a retail directory name and the directory this content set ships", origin, origin)
		}
		if strings.ContainsAny(retail, "/\\") || strings.ContainsAny(target, "/\\") {
			return Profile{}, fmt.Errorf("nanolathe: content profile directory row is not a single directory: logical path %s, providers searched [%s], expected two top-level directory names without separators", origin, origin)
		}
	}
	return profile, nil
}

// Lookup selects a profile by name from the shipped set, or reads one from a
// JSON file when the selector names no shipped profile. A user-authored
// profile is the escape hatch for a content set Nanolathe does not ship a
// table for.
func Lookup(selector string) (Profile, error) {
	trimmed := strings.TrimSpace(selector)
	if trimmed == "" {
		return Profile{}, fmt.Errorf("nanolathe: content profile selector is empty: logical path <content-profile>, providers searched [%s], expected a profile name or the path of a profile JSON file", strings.Join(Names(), ", "))
	}
	folded := strings.ToLower(trimmed)
	for _, name := range detectionOrder {
		if folded == name {
			return load(name)
		}
	}
	data, err := os.ReadFile(trimmed)
	if err != nil {
		return Profile{}, fmt.Errorf("nanolathe: content profile is unknown and not readable as a file: logical path %s, providers searched [%s], expected a shipped profile name or a readable profile JSON file", trimmed, strings.Join(Names(), ", "))
	}
	return parse(data, trimmed)
}

// Detect selects a complete known directory layout in the mounted namespace.
// Archive filenames are packaging, not content: an archive need not expose its
// host filename through Stat. Multiple matching layouts require an explicit
// selector rather than silently choosing one content set by preset order.
func Detect(mounted vfs.FSOps) (Profile, error) {
	if mounted == nil {
		return load(RetailName)
	}
	var matches []Profile
	for _, name := range detectionOrder {
		if name == RetailName {
			continue
		}
		profile, err := load(name)
		if err != nil {
			return Profile{}, err
		}
		matched := true
		for _, marker := range profile.Detect {
			if info, err := mounted.Stat(marker); err != nil || !info.IsDir {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, profile)
		}
	}
	if len(matches) > 1 {
		names := make([]string, len(matches))
		for i, profile := range matches {
			names[i] = profile.Name
		}
		return Profile{}, fmt.Errorf("nanolathe: content layout is ambiguous: logical path <content-profile>, providers searched [%s], expected one mounted content layout or an explicit --content-profile selector", strings.Join(names, ", "))
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return load(RetailName)
}

// Resolve is the mount-time entry point: an explicit selector overrides
// detection, and an empty selector detects. The caller applies the returned
// profile's layout to the mounted overlay and reports its name.
func Resolve(mounted vfs.FSOps, selector string) (Profile, error) {
	if strings.TrimSpace(selector) != "" {
		return Lookup(selector)
	}
	return Detect(mounted)
}

// GameplaySources carries the authored table and legacy parameter defaults.
// Strict ignores these declarations (DESIGN_COMMUNITY_PATCH §3.2 and §5).
func (p Profile) GameplaySources() []community.Overrides {
	legacy := community.Overrides{}
	if p.Name != RetailName && p.Limits.UnitLimit != 0 {
		legacy.UnitLimit = &p.Limits.UnitLimit
	}
	if p.Name != RetailName && p.Limits.SearchEntries != 0 {
		legacy.PathStepAllowance = &p.Limits.SearchEntries
	}
	table := community.Overrides{Table: p.Gameplay.Table}
	explicit := p.Gameplay
	explicit.Table = ""
	return []community.Overrides{table, legacy, explicit}
}
