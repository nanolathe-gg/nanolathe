package mission

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// Campaign is a discovered campaign file in camps/*.tdf. [08 "Campaign discovery"]
// Discovery walks MISSION0..N contiguous sections until the first gap;
// MissionList is an allocation tag, not an authored section — do not require it.
// [08 "Campaign discovery"] [GAP T8] [GAP T14] C1.
type Campaign struct {
	Name         string            // base file name without .tdf, e.g. "Arm Campaign" (original case)
	Path         string            // logical VFS path, e.g. "camps/arm campaign.tdf" (folded)
	OriginalPath string            // original-case path as reported by VFS, e.g. "camps/Arm Campaign.tdf"
	Document     *formats.Document // parsed TDF, retains HEADER and MISSION sections
	Missions     []Stub            // contiguous MISSION0..N until first gap [08 "Campaign discovery"]
}

// Stub is a campaign mission entry discovered under a Campaign. [08 "Campaign discovery"]
// It corresponds to one MISSION%d top-level section until the first gap.
// MissionList is not an authored wrapper and is ignored (C1). Each mission
// section supplies a missionname read through the language-prefixed string
// accessor, defaulting to the built-in "unnamed mission" string. [08 "Campaign discovery"]
type Stub struct {
	Index       int              // numeric suffix of MISSION%d
	Name        string           // missionname (language-prefixed, fallback "unnamed mission")
	MissionFile string           // missionfile value (OTA path), may be empty
	Section     *formats.Section // raw TDF section for MISSION%d
}

// Discover enumerates campaign definition files via the VFS and returns
// the discovered campaigns. [08 "Campaign discovery"] C1.
//
// It walks the VFS logical directory "camps" for *.tdf files and, for each
// file, walks contiguous MISSION0..N sections until the first missing index.
// MissionList is only the allocation tag for the resulting array, not an
// authored section, so its presence or absence does not affect discovery.
// The concrete retail VFS preserves provider/enumeration order for this scan;
// synthetic FSOps retain the canonical sorted order used by library tests.
// Missions are always visited in index order.
func Discover(fs vfs.FSOps) ([]Campaign, error) {
	if fs == nil {
		return nil, fmt.Errorf("mission: nil filesystem")
	}
	retailOrder := false
	var entries []vfs.EntryInfo
	var err error
	if ordered, ok := fs.(interface {
		RetailReadDir(string) ([]vfs.EntryInfo, error)
	}); ok {
		entries, err = ordered.RetailReadDir("camps")
		retailOrder = true
	} else {
		entries, err = fs.ReadDir("camps")
	}
	if err != nil {
		// No camps directory => no campaigns (not an error for fixture tests).
		// vfs.ErrNotFound is the expected signal for a missing logical dir.
		// Treat it as empty rather than fatal; callers that require campaigns
		// can check len==0.
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mission: camps: %w", err)
	}
	// Filter to .tdf files only, non-directories. Retail's concrete VFS uses
	// provider/enumeration order; synthetic FSOps retain the canonical sorted
	// behavior used by the library tests. [08 "Campaign discovery"]
	var tdfEntries []vfs.EntryInfo
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		// Extension check on logical Path (folded lower) and also Name for
		// completeness; the logical Path is authoritative.
		if !isTDF(e.Path) && !isTDF(e.Name) {
			continue
		}
		tdfEntries = append(tdfEntries, e)
	}
	if !retailOrder {
		sort.Slice(tdfEntries, func(i, j int) bool {
			if tdfEntries[i].Path != tdfEntries[j].Path {
				return tdfEntries[i].Path < tdfEntries[j].Path
			}
			return tdfEntries[i].OriginalPath < tdfEntries[j].OriginalPath
		})
	}

	var out []Campaign
	limit := int64(formats.DefaultTDFLimits().MaxBytes)
	for _, e := range tdfEntries {
		data, err := fs.ReadFileLimit(e.Path, limit)
		if err != nil {
			return nil, fmt.Errorf("mission: campaign %q: %w", e.OriginalPath, err)
		}
		doc, err := formats.ParseTDF(data)
		if err != nil {
			return nil, fmt.Errorf("mission: campaign %q: parse: %w", e.OriginalPath, err)
		}
		missions := discoverMissions(doc)
		name := campaignBaseName(e)
		c := Campaign{
			Name:         name,
			Path:         e.Path,
			OriginalPath: e.OriginalPath,
			Document:     doc,
			Missions:     missions,
		}
		out = append(out, c)
	}
	if !retailOrder {
		// Synthetic FSOps are sorted above; keep their returned order stable.
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	}
	return out, nil
}

// DiscoverCampaign loads a single campaign TDF by logical VFS path and
// discovers its contiguous MISSION0..N run. Use this when the caller already
// knows the campaign path (e.g., "camps/Arm Campaign.tdf") rather than
// enumerating the whole directory. [08 "Campaign discovery"] C1.
func DiscoverCampaign(fs vfs.FSOps, logicalPath string) (*Campaign, error) {
	if fs == nil {
		return nil, fmt.Errorf("mission: nil filesystem")
	}
	// Normalise logical path via VFS Stat to obtain canonical EntryInfo (case
	// folding, backslash handling). Fall back to the supplied path if Stat fails
	// for a synthetic test FS.
	var info vfs.EntryInfo
	if st, err := fs.Stat(logicalPath); err == nil {
		info = st
	} else {
		info = vfs.EntryInfo{Path: strings.ToLower(logicalPath), OriginalPath: logicalPath, Name: logicalPath}
		// Extract base name for Name field.
		if idx := strings.LastIndex(logicalPath, "/"); idx >= 0 {
			info.Name = logicalPath[idx+1:]
		} else if idx := strings.LastIndex(logicalPath, "\\"); idx >= 0 {
			info.Name = logicalPath[idx+1:]
		}
		if info.Path == "" {
			info.Path = strings.ToLower(logicalPath)
		}
		if info.OriginalPath == "" {
			info.OriginalPath = logicalPath
		}
	}
	limit := int64(formats.DefaultTDFLimits().MaxBytes)
	data, err := fs.ReadFileLimit(info.Path, limit)
	if err != nil {
		return nil, fmt.Errorf("mission: campaign %q: %w", info.OriginalPath, err)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, fmt.Errorf("mission: campaign %q: parse: %w", info.OriginalPath, err)
	}
	missions := discoverMissions(doc)
	name := campaignBaseName(info)
	c := &Campaign{
		Name:         name,
		Path:         info.Path,
		OriginalPath: info.OriginalPath,
		Document:     doc,
		Missions:     missions,
	}
	return c, nil
}

func discoverMissions(doc *formats.Document) []Stub {
	if doc == nil || doc.Root == nil {
		return nil
	}
	var out []Stub
	// Walk MISSION0..N until first missing index. [08 "Campaign discovery"] C1.
	// MissionList is allocation tag, not authored, so ignore it entirely.
	for i := 0; ; i++ {
		secName := fmt.Sprintf("MISSION%d", i)
		sec := doc.Root.Section(secName)
		if sec == nil {
			break
		}
		// missionname via language-prefixed string accessor, defaulting to
		// built-in fallback is retail's verbatim error string
		// ("Error -- Unnamed Mission") when a block has neither key
		// [08 R-CAMP-01 §1].
		rawName, _ := sec.LanguageString("", "missionname", "Error -- Unnamed Mission")
		rawName = strings.TrimSpace(rawName)
		if rawName == "" {
			rawName = "Error -- Unnamed Mission"
		}
		mf, _ := sec.StringValue("missionfile", "")
		mf = strings.TrimSpace(mf)
		out = append(out, Stub{
			Index:       i,
			Name:        rawName,
			MissionFile: mf,
			Section:     sec,
		})
	}
	return out
}

func campaignBaseName(e vfs.EntryInfo) string {
	base := e.Name
	if base == "" {
		// Derive from Path if Name absent (synthetic FS).
		if idx := strings.LastIndex(e.Path, "/"); idx >= 0 {
			base = e.Path[idx+1:]
		} else {
			base = e.Path
		}
		// Restore original case from OriginalPath if Path was folded.
		if e.OriginalPath != "" {
			if idx := strings.LastIndex(e.OriginalPath, "/"); idx >= 0 {
				base = e.OriginalPath[idx+1:]
			} else if idx := strings.LastIndex(e.OriginalPath, "\\"); idx >= 0 {
				base = e.OriginalPath[idx+1:]
			} else {
				base = e.OriginalPath
			}
		}
	}
	// Strip .tdf extension case-insensitively.
	if isTDF(base) {
		base = base[:len(base)-4]
	}
	return base
}

func isTDF(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".tdf")
}

func isNotFound(err error) bool {
	// vfs.ErrNotFound is sentinel; use string fallback if errors.Is not available?
	// Use errors.Is via type assertion for minimal import.
	if err == nil {
		return false
	}
	// Direct equality and substring check for wrapped errors.
	if err.Error() == "vfs: file not found" {
		return true
	}
	// Check wrapped via errors.Is semantics without importing errors: look for substring.
	// Also handle "vfs: file not found: camps" style.
	if strings.Contains(err.Error(), "file not found") {
		return true
	}
	return false
}
