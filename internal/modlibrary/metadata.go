// Package modlibrary owns the mod library: the data directory that holds one
// extracted content root per installed mod version, the metadata every mod
// carries, the install receipts, and the extraction and validation path that
// both downloads and manual installs go through
// (docs/DESIGN_MODS_MUTATORS.md §4 and §5.3).
//
// A mod here is a downloadable content package, never a gameplay rule set;
// the repository's mods/ directory is unrelated (DESIGN_MODS_MUTATORS §1
// "Terminology"). The package touches no network: the catalogue and downloads
// belong to internal/modfetch (DESIGN_MODS_MUTATORS §9). Nothing it does runs
// inside a tick, and no simulation package may import it.
package modlibrary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// MetadataFile is the metadata document at the root of every hosted zip and
// every installed mod (DESIGN_MODS_MUTATORS §4.2).
const MetadataFile = "nanolathe-mod.json"

// ReceiptFile is the install receipt beside the metadata (§4.1). A package
// that ships a root-level file of this name has it skipped, because the
// library writes its own.
const ReceiptFile = "install.json"

// metadataSchema is the only metadata schema this build reads.
const metadataSchema = 1

// maxMetadataBytes bounds the metadata read. The document is a dozen short
// fields; anything larger is not a metadata file.
const maxMetadataBytes = 64 << 10

// Metadata is one mod version's self-description, as nanolathe-mod.json and
// each catalogue entry carry it (DESIGN_MODS_MUTATORS §4.2, §5.1).
type Metadata struct {
	Schema          int      `json:"schema"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Summary         string   `json:"summary,omitempty"`
	Homepage        string   `json:"homepage,omitempty"`
	ContentProfile  string   `json:"contentProfile,omitempty"`
	MinimumGameplay string   `json:"minimumGameplay,omitempty"` // a reserved gameplay word or ""
	Controls        string   `json:"controls,omitempty"`        // "community", "retail", "zero" or ""
	Requires        []string `json:"requires,omitempty"`        // logical paths the BASE install must resolve
}

// idPattern is the stable id's alphabet (§4.2): lowercase letters, digits and
// hyphens.
var idPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// versionPattern keeps the opaque version usable as one directory name on
// every host: it becomes the <version> segment of <id>/<version>/, so it may
// not carry a separator, a drive or stream colon, or a dot-only name. The
// design calls the version opaque and compares it for equality only; this is
// the narrowest alphabet that stays a safe path segment everywhere, not a
// version grammar.
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// maxSegment bounds id and version lengths so <id>/<version> stays well
// inside every host's path-component limit.
const maxSegment = 64

// Local packages (P11) own the `local-` id prefix and the `local` version.
const (
	localIDPrefix = "local-"
	localVersion  = "local"
)

// ParseMetadata decodes and validates one nanolathe-mod.json document.
// Unknown fields are refused, as for content profiles, so a misspelt key is
// reported rather than silently ignored; a new field is a new schema.
func ParseMetadata(data []byte) (Metadata, error) {
	if len(data) > maxMetadataBytes {
		return Metadata{}, diagnostic("mod metadata is too large", MetadataFile, nil, fmt.Sprintf("a metadata document of at most %d bytes", maxMetadataBytes))
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var meta Metadata
	if err := decoder.Decode(&meta); err != nil {
		return Metadata{}, diagnostic("reading mod metadata failed: "+err.Error(), MetadataFile, nil, "a schema 1 mod metadata document")
	}
	if decoder.More() {
		return Metadata{}, diagnostic("mod metadata has trailing data", MetadataFile, nil, "exactly one JSON object")
	}
	if err := meta.Validate(); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

// Validate checks one metadata record against the §4.2 contract. The
// catalogue fetcher applies it to every manifest entry, so a hosted entry and
// the metadata inside its zip are held to the same rules.
func (m Metadata) Validate() error {
	fail := func(what, expected string) error {
		return diagnostic(what, MetadataFile, nil, expected)
	}
	if m.Schema != metadataSchema {
		return fail(fmt.Sprintf("mod metadata schema %d is not supported", m.Schema), "schema 1")
	}
	if len(m.ID) > maxSegment || !idPattern.MatchString(m.ID) {
		return fail(fmt.Sprintf("mod id %q is invalid", m.ID), fmt.Sprintf("an id of 1 to %d characters from [a-z0-9-]", maxSegment))
	}
	if strings.TrimSpace(m.Name) == "" {
		return fail("mod name is empty", "a non-empty display name")
	}
	if len(m.Version) > maxSegment || !versionPattern.MatchString(m.Version) {
		return fail(fmt.Sprintf("mod version %q is invalid", m.Version), fmt.Sprintf("a version of 1 to %d characters from [A-Za-z0-9._+-] beginning with a letter or digit", maxSegment))
	}
	switch gameplay.Mode(m.MinimumGameplay) {
	case "", gameplay.Strict31, gameplay.Community39, gameplay.Modern:
	default:
		return fail(fmt.Sprintf("mod minimumGameplay %q is not a reserved gameplay word", m.MinimumGameplay),
			fmt.Sprintf("one of %s, %s, %s or omitted", gameplay.Strict31, gameplay.Community39, gameplay.Modern))
	}
	switch m.Controls {
	case "", controlsCommunity, controlsRetail, contentprofiles.ControlsZero:
	default:
		return fail(fmt.Sprintf("mod controls preset %q is unknown", m.Controls), "community, retail, zero or omitted")
	}
	if m.ContentProfile != "" && strings.TrimSpace(m.ContentProfile) != m.ContentProfile {
		return fail("mod contentProfile has surrounding space", "a shipped profile name or a profile path relative to the mod root")
	}
	if m.ContentProfile != "" && !isShippedProfile(m.ContentProfile) {
		if _, err := cleanRelative(m.ContentProfile); err != nil {
			return fail(fmt.Sprintf("mod contentProfile %q leaves the mod root", m.ContentProfile), "a shipped profile name or a profile path relative to the mod root")
		}
	}
	for _, required := range m.Requires {
		if strings.TrimSpace(required) == "" {
			return fail("mod requires an empty logical path", "logical paths the base install must resolve")
		}
		if _, err := cleanRelative(required); err != nil {
			return fail(fmt.Sprintf("mod requires %q, which is not a logical path", required), "relative logical paths the base install must resolve")
		}
	}
	return nil
}

// Controls presets a mod may name (§4.3), spelled as content profiles spell
// them.
const (
	controlsCommunity = contentprofiles.ControlsCommunity
	controlsRetail    = contentprofiles.ControlsRetail
)

// ParseSelector reads a `--mod` argument: `<id>`, `<id>@<version>`, or
// `none` for no mod (§4.3). An id is folded to lower case, since ids are
// lowercase by definition; the version is compared for equality and kept as
// written.
func ParseSelector(s string) (id, version string, err error) {
	text := strings.TrimSpace(s)
	if strings.EqualFold(text, "none") {
		return "", "", nil
	}
	expected := "<id>, <id>@<version> or none"
	if text == "" {
		return "", "", diagnostic("mod selector is empty", "<command line>", nil, expected)
	}
	id, version, hasVersion := strings.Cut(text, "@")
	id = strings.ToLower(id)
	if len(id) > maxSegment || !idPattern.MatchString(id) {
		return "", "", diagnostic(fmt.Sprintf("mod selector %q has an invalid id", s), "<command line>", nil, expected)
	}
	if hasVersion && (len(version) > maxSegment || !versionPattern.MatchString(version)) {
		return "", "", diagnostic(fmt.Sprintf("mod selector %q has an invalid version", s), "<command line>", nil, expected)
	}
	return id, version, nil
}

// localMetadata is the generated description of a package that carries no
// nanolathe-mod.json (P11, §4.5): `local-<sanitized name>`, version `local`,
// no minimum, no preset, and a detected content profile.
func localMetadata(baseName string) Metadata {
	return Metadata{
		Schema:  metadataSchema,
		ID:      localIDPrefix + sanitizeName(baseName),
		Name:    baseName,
		Version: localVersion,
	}
}

// sanitizeName lowercases a file or folder name and collapses every run of
// characters outside [a-z0-9] into one hyphen, so "ProTA 4.8" becomes
// "prota-4-8". A name with nothing usable becomes "mod".
func sanitizeName(name string) string {
	var out strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			hyphen = false
			continue
		}
		if !hyphen && out.Len() > 0 {
			out.WriteByte('-')
			hyphen = true
		}
	}
	result := strings.TrimRight(out.String(), "-")
	if limit := maxSegment - len(localIDPrefix); len(result) > limit {
		result = strings.TrimRight(result[:limit], "-")
	}
	if result == "" {
		return "mod"
	}
	return result
}

// isLocal reports whether metadata is the generated local shape. Packaged
// metadata may not claim it (installFrom refuses that), so the shape alone
// identifies a metadata-less install.
func (m Metadata) isLocal() bool {
	return strings.HasPrefix(m.ID, localIDPrefix) && m.Version == localVersion
}

// cleanRelative normalizes a slash- or backslash-separated relative path and
// refuses one that is absolute, names a drive or stream, or climbs out with
// `..`. It returns the cleaned slash form, or "" for the root itself.
func cleanRelative(name string) (string, error) {
	slashed := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(slashed, "/") {
		return "", fmt.Errorf("absolute path")
	}
	if len(slashed) >= 2 && slashed[1] == ':' && isASCIILetter(slashed[0]) {
		return "", fmt.Errorf("drive letter")
	}
	parts := make([]string, 0, strings.Count(slashed, "/")+1)
	for _, part := range strings.Split(slashed, "/") {
		switch {
		case part == "" || part == ".":
			continue
		case part == "..":
			return "", fmt.Errorf("parent component")
		case strings.ContainsAny(part, ":\x00"):
			// A colon inside a component is a drive or an alternate data
			// stream on Windows; NUL is never a valid name byte.
			return "", fmt.Errorf("drive or stream name")
		}
		parts = append(parts, part)
	}
	return path.Join(parts...), nil
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// diagError is the AGENTS.md diagnostic shape:
// `nanolathe: <what failed>: logical path <path>, providers searched [<a>], expected <product>`.
// A wrapped sentinel lets a caller branch on the failure class.
type diagError struct {
	what, logical, expected string
	providers               []string
	wrapped                 error
}

func (e *diagError) Error() string {
	return fmt.Sprintf("nanolathe: %s: logical path %s, providers searched [%s], expected %s",
		e.what, e.logical, strings.Join(e.providers, ", "), e.expected)
}

func (e *diagError) Unwrap() error { return e.wrapped }

func diagnostic(what, logical string, providers []string, expected string) error {
	return &diagError{what: what, logical: logical, providers: providers, expected: expected}
}
