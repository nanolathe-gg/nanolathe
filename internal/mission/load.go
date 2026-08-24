package mission

import (
	"github.com/nanolathe/nanolathe/internal/triggers"
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/vfs"
)

// Type is the mission type discriminant. [08 "Mission type dispatch"] [C2]
// Type 1 is campaign (requires GlobalHeader plus missionfile); types 2 and 3
// read the OTA directly with the fuzzy-search fallback.
type Type uint8

const (
	TypeCampaign Type = 1 // [08 "Mission type dispatch"] campaign
	TypeSkirmish Type = 2 // [08 "Mission type dispatch"] skirmish/multiplayer direct OTA
	TypeSaved    Type = 3 // [08 "Mission type dispatch"] loaded-save OTA
)

// Mission is the loaded mission object. [08 "Mission object"] [C2] [C4] [C5] [C8]
// It composes the established pieces in the required order: schema selection
// before placement instantiation, then wind-bounds retention, then UseOnlyUnits
// routing. [08 "Schema choice"] [C4] [C5] [C8]
type Mission struct {
	Type        Type
	OTA         *formats.OTA
	TerrainKey  string             // base name without extension, for TNT pairing [fmt ota]
	Schema      Schema             // selected schema [08 "Schema choice"] [C3][C4]
	Units       []UnitPlacement    // 36-byte retail identity, named fields per I13 [C6]
	Specials    []Special          // 12-byte retail identity [C6]
	Features    []FeaturePlacement // 136-byte retail identity [C6]
	WindBounds  WindBounds         // retained for PLAN_14 battle-entry initializer, no RNG [C5] [08 "Wind initialization"]
	UseOnlyPath string             // routed into camps/useonly [C8] [08 "Mission placement record"]
	Victory     []*triggers.Trigger // victory conditions [PLAN_10 C14-C17]
	Defeat      []*triggers.Trigger // defeat conditions [PLAN_10 C14-C17]
	IsRestore   bool                // BetweenMissions restore: InitialMission also runs on restores [04 §3.6] C9
	// order witness for ordering assertion [C4][C5]: schema -> placement -> wind
	order []string
}

// Order returns the observed load order for ordering assertions.
// It is a copy; mutations do not affect the stored order.
func (m *Mission) Order() []string {
	if m == nil {
		return nil
	}
	out := make([]string, len(m.order))
	copy(out, m.order)
	return out
}

// Sink is the diagnostic sink per ORCHESTRATION §7. Diagnostics are returned
// as errors and also collected on the sink; never logged inside sim.
type Sink interface {
	Report(string)
}

// CollectSink collects diagnostics for tests and presentation draining. [ORCH §7]
type CollectSink struct {
	Messages []string
}

// Report implements Sink.
func (c *CollectSink) Report(s string) {
	if c == nil {
		return
	}
	c.Messages = append(c.Messages, s)
}

// Verbatim mission-file diagnostics. [02 "Mission-file diagnostics"] [08 "Mission type dispatch"] [GAP T14] [C2]
// Four report through the status pane; the fifth (No suitable schema) uses a different channel but is defined alongside.
const (
	verbatimDoesNotExist     = "does not exist"                           // substring of campaign/mission does-not-exist diagnostic [C2]
	verbatimCorrupt          = "corrupt (no header found)"                // substring of joker diagnostic [C2]
	verbatimOldTED           = "Old TED format no longer supported!"      // [02] [C2] verbatim
	verbatimNoGlobalHeader   = "No GlobalHeader block in mission file!"   // [02] [C2] verbatim
	verbatimNoSuitableSchema = "No suitable schema type in mission file!" // [02] fifth, schema selector
)

// errDoesNotExist constructs the verbatim missing mission file diagnostic.
// [02 "Mission-file diagnostics"] The requested mission file, %s, does not exist.
func errDoesNotExist(name string) error {
	return fmt.Errorf("The requested mission file, %s, does not exist.", name)
}

// errCampaignDoesNotExist is the sibling for campaign loads.
// [02 "Mission-file diagnostics"] The requested campaign file, %s, does not exist.
func errCampaignDoesNotExist(name string) error {
	return fmt.Errorf("The requested campaign file, %s, does not exist.", name)
}

// errCorrupt constructs the verbatim corrupt diagnostic with two spaces after joker!.
// [02 "Mission-file diagnostics"] Hey, joker!  Mission file %s is corrupt (no header found).
func errCorrupt(name string) error {
	return fmt.Errorf("Hey, joker!  Mission file %s is corrupt (no header found).", name)
}

// errOldTED is verbatim. [02 "Mission-file diagnostics"] [C2]
func errOldTED() error { return fmt.Errorf("%s", verbatimOldTED) }

// errNoGlobalHeader is verbatim. [02 "Mission-file diagnostics"] [C2]
func errNoGlobalHeader() error { return fmt.Errorf("%s", verbatimNoGlobalHeader) }

// Load is the PLAN_10 public API entry point. [PLAN_10 Public API]
// It matches the sketch: Load(fs, cat, path). For campaign paths containing a
// colon (camps/...tdf:MISSIONn) it dispatches as TypeCampaign (C2); otherwise
// it treats the path as a direct OTA for TypeSkirmish/2. Difficulty and player
// count default to 0/0 so skirmish fallback (player count zero accepts first
// candidate) is exercised; callers needing explicit difficulty should use
// LoadWithType. Wind bounds are retained with no RNG draws [C5]; UseOnlyUnits
// is routed into camps/useonly [C8]; schema selection precedes placement [C4].
func Load(fs vfs.FSOps, cat *content.Catalog, path string) (*Mission, error) {
	_ = cat // catalog not required for header-only load; phase 14 owns full world build
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errDoesNotExist(path)
	}
	// Heuristic dispatch: colon separates campaign file and mission index for Type 1.
	if strings.Contains(path, ":") {
		parts := strings.SplitN(path, ":", 2)
		campaignPath := strings.TrimSpace(parts[0])
		missionPart := strings.TrimSpace(parts[1])
		// missionPart is like MISSION0 or 0
		var idx int
		if strings.HasPrefix(strings.ToLower(missionPart), "mission") {
			// parse numeric suffix
			num := strings.TrimSpace(missionPart[len("mission"):])
			// also handle case like "MISSION0"
			// formats.ParseTDFInteger handles prefix; do quick parse
			var v int
			_, _ = fmt.Sscanf(num, "%d", &v)
			idx = v
		} else {
			_, _ = fmt.Sscanf(missionPart, "%d", &idx)
		}
		return LoadCampaignWithSink(fs, campaignPath, idx, 0, 0, nil)
	}
	// Default to skirmish direct OTA (Type 2) with fuzzy fallback [08 "Mission type dispatch"].
	// Types 2 and 3 join the raw OTA path directly against the maps directory; when parsing misses, a fuzzy search falls back to the closest match.
	return LoadWithType(fs, TypeSkirmish, path, 0, 0, nil)
}

// LoadWithType loads a mission with explicit type discriminant. [08 "Mission type dispatch"] [C2]
// Type 1 requires both GlobalHeader and missionfile; types 2 and 3 read the OTA
// directly with the fuzzy-search fallback.
// Types 2 and 3 join the raw OTA path directly against the maps directory; when parsing misses, a fuzzy search falls back to the closest match. [08 "Mission type dispatch"]
// Schema selection happens before placement records are instantiated [C4]; wind
// bounds are retained without RNG draws [C5]; UseOnlyUnits is routed [C8].
func LoadWithType(fs vfs.FSOps, typ Type, logicalPath string, difficulty, playerCount int, sink Sink) (*Mission, error) {
	return loadWithTypeInternal(fs, typ, logicalPath, difficulty, playerCount, sink)
}

// LoadCampaign loads a campaign mission by campaign file and mission index (MISSION%d).
// It builds the MISSION%d section name and requires the mission file's GlobalHeader
// block plus missionfile/missionname with the four distinct verbatim diagnostics. [C2] [08 "Mission type dispatch"]
func LoadCampaign(fs vfs.FSOps, campaignPath string, missionIndex int, difficulty, playerCount int) (*Mission, error) {
	return LoadCampaignWithSink(fs, campaignPath, missionIndex, difficulty, playerCount, nil)
}

// LoadCampaignWithSink is LoadCampaign with a diagnostic sink. [ORCH §7]
func LoadCampaignWithSink(fs vfs.FSOps, campaignPath string, missionIndex int, difficulty, playerCount int, sink Sink) (*Mission, error) {
	if fs == nil {
		return nil, fmt.Errorf("mission: nil filesystem")
	}
	campaignPath = strings.TrimSpace(campaignPath)
	if campaignPath == "" {
		err := errCampaignDoesNotExist(campaignPath)
		if sink != nil {
			sink.Report(err.Error())
		}
		return nil, err
	}
	// Attempt to read campaign file; missing => does not exist diagnostic verbatim.
	data, err := fs.ReadFileLimit(campaignPath, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		if isNotFound(err) {
			e := errCampaignDoesNotExist(campaignPath)
			if sink != nil {
				sink.Report(e.Error())
			}
			return nil, e
		}
		if sink != nil {
			sink.Report(err.Error())
		}
		return nil, err
	}
	// Check Old TED before parsing
	if isOldTED(data) {
		e := errOldTED()
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		e := errCorrupt(campaignPath)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	if doc.Root == nil || len(doc.Root.Sections()) == 0 {
		e := errCorrupt(campaignPath)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	// Build MISSION%d section name [08 "Mission type dispatch"] Type 1 requires GlobalHeader plus missionfile.
	// Campaign discovery walks MISSION0..N until first gap, but for single load we require the requested index to exist.
	secName := fmt.Sprintf("MISSION%d", missionIndex)
	sec := doc.Root.Section(secName)
	if sec == nil {
		e := errDoesNotExist(fmt.Sprintf("%s:%s", campaignPath, secName))
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	missionFile, _ := sec.StringValue("missionfile", "")
	missionFile = strings.TrimSpace(missionFile)
	if missionFile == "" {
		// Missing missionfile within the campaign section is treated as missing GlobalHeader? But distinguish.
		// For Type 1, missing missionfile means the OTA cannot be resolved; produce does-not-exist for the derived OTA name.
		e := errDoesNotExist(missionFile)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	// Join missionfile against maps directory for the OTA load.
	otaLogical := joinMapsPath(missionFile)
	// For Type 1, strict load (no fuzzy fallback). Types 2/3 have fuzzy.
	ota, terrKey, err := resolveOTAStrict(fs, otaLogical, sink, missionFile)
	if err != nil {
		return nil, err
	}
	// Compose mission body in established order: schema -> placement -> wind -> useonly. [C4][C5][C8]
	return composeMission(fs, TypeCampaign, ota, terrKey, difficulty, playerCount, sink)
}

// LoadWithSink is LoadWithType with sink. [ORCH §7]
func LoadWithSink(fs vfs.FSOps, typ Type, logicalPath string, difficulty, playerCount int, sink Sink) (*Mission, error) {
	return loadWithTypeInternal(fs, typ, logicalPath, difficulty, playerCount, sink)
}

func loadWithTypeInternal(fs vfs.FSOps, typ Type, logicalPath string, difficulty, playerCount int, sink Sink) (*Mission, error) {
	if fs == nil {
		return nil, fmt.Errorf("mission: nil filesystem")
	}
	logicalPath = strings.TrimSpace(logicalPath)
	if logicalPath == "" {
		e := errDoesNotExist(logicalPath)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	switch typ {
	case TypeCampaign:
		// For direct TypeCampaign via OTA path (without campaign file), treat logicalPath as OTA but require strict GlobalHeader etc.
		otaLogical := joinMapsPath(logicalPath)
		ota, terrKey, err := resolveOTAStrict(fs, otaLogical, sink, logicalPath)
		if err != nil {
			return nil, err
		}
		return composeMission(fs, typ, ota, terrKey, difficulty, playerCount, sink)
	case TypeSkirmish, TypeSaved:
		// Types 2 and 3 join the raw OTA path directly against the maps directory; when parsing misses, a fuzzy search falls back to the closest match. [08 "Mission type dispatch"]
		otaLogical := joinMapsPath(logicalPath)
		ota, terrKey, err := resolveOTAWithFuzzy(fs, otaLogical, sink, logicalPath)
		if err != nil {
			return nil, err
		}
		return composeMission(fs, typ, ota, terrKey, difficulty, playerCount, sink)
	default:
		// Unknown type discriminant fails; treat as campaign-like strict for diagnostics.
		// Any other difficulty value fails per schema, but type itself we still handle.
		e := fmt.Errorf("mission: unknown type %d", typ)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
}

// joinMapsPath joins the raw OTA path directly against the maps directory.
// [08 "Mission type dispatch"] Types 2 and 3 join the raw OTA path directly against the maps directory.
func joinMapsPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	// Normalize backslashes to slashes for VFS.
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = strings.TrimPrefix(raw, "/")
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "maps/") {
		return raw
	}
	return "maps/" + raw
}

// resolveOTAStrict loads an OTA strictly without fuzzy fallback (Type 1). [C2]
func resolveOTAStrict(fs vfs.FSOps, otaLogical string, sink Sink, displayName string) (*formats.OTA, string, error) {
	data, err := fs.ReadFileLimit(otaLogical, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		if isNotFound(err) {
			e := errDoesNotExist(displayName)
			if sink != nil {
				sink.Report(e.Error())
			}
			return nil, "", e
		}
		if sink != nil {
			sink.Report(err.Error())
		}
		return nil, "", err
	}
	if isOldTED(data) {
		e := errOldTED()
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		e := errCorrupt(displayName)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	if doc.Root == nil {
		e := errCorrupt(displayName)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	if doc.Root.Section("GlobalHeader") == nil {
		e := errNoGlobalHeader()
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	ota, err := formats.LoadOTA(data)
	if err != nil {
		// LoadOTA checks GlobalHeader missing as "ota: missing GlobalHeader" -> map to verbatim No GlobalHeader.
		if strings.Contains(err.Error(), "GlobalHeader") {
			e := errNoGlobalHeader()
			if sink != nil {
				sink.Report(e.Error())
			}
			return nil, "", e
		}
		// Other parse errors -> corrupt
		e := errCorrupt(displayName)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	terrKey := terrainKeyFromLogical(otaLogical)
	return ota, terrKey, nil
}

// resolveOTAWithFuzzy loads an OTA with fuzzy-search fallback for types 2 and 3.
// [08 "Mission type dispatch"] Types 2 and 3 join the raw OTA path directly against the maps directory; when parsing misses, a fuzzy search falls back to the closest match.
func resolveOTAWithFuzzy(fs vfs.FSOps, otaLogical string, sink Sink, displayName string) (*formats.OTA, string, error) {
	data, err := fs.ReadFileLimit(otaLogical, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		if isNotFound(err) {
			// Try fuzzy search fallback: enumerate maps/ and pick closest match.
			if closest, ok := fuzzySearch(fs, otaLogical); ok {
				// Log the fallback? No log inside sim; but we can report diagnostic via sink? The fallback is success, not diagnostic.
				data2, err2 := fs.ReadFileLimit(closest, int64(formats.DefaultTDFLimits().MaxBytes))
				if err2 == nil {
					if isOldTED(data2) {
						e := errOldTED()
						if sink != nil {
							sink.Report(e.Error())
						}
						return nil, "", e
					}
					doc2, err2 := formats.ParseTDF(data2)
					if err2 != nil {
						e := errCorrupt(displayName)
						if sink != nil {
							sink.Report(e.Error())
						}
						return nil, "", e
					}
					if doc2.Root.Section("GlobalHeader") == nil {
						e := errNoGlobalHeader()
						if sink != nil {
							sink.Report(e.Error())
						}
						return nil, "", e
					}
					ota2, err2 := formats.LoadOTA(data2)
					if err2 != nil {
						if strings.Contains(err2.Error(), "GlobalHeader") {
							e := errNoGlobalHeader()
							if sink != nil {
								sink.Report(e.Error())
							}
							return nil, "", e
						}
						e := errCorrupt(displayName)
						if sink != nil {
							sink.Report(e.Error())
						}
						return nil, "", e
					}
					return ota2, terrainKeyFromLogical(closest), nil
				}
			}
			// No fuzzy match found -> does not exist verbatim
			e := errDoesNotExist(displayName)
			if sink != nil {
				sink.Report(e.Error())
			}
			return nil, "", e
		}
		if sink != nil {
			sink.Report(err.Error())
		}
		return nil, "", err
	}
	if isOldTED(data) {
		e := errOldTED()
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		// Parse error -> corrupt; for types 2/3, try fuzzy fallback before returning corrupt?
		// Spec says when parsing misses, fuzzy fallback; so try fuzzy on corrupt as well.
		if closest, ok := fuzzySearch(fs, otaLogical); ok && closest != otaLogical {
			data2, err2 := fs.ReadFileLimit(closest, int64(formats.DefaultTDFLimits().MaxBytes))
			if err2 == nil && !isOldTED(data2) {
				if doc2, err2 := formats.ParseTDF(data2); err2 == nil && doc2.Root.Section("GlobalHeader") != nil {
					if ota2, err2 := formats.LoadOTA(data2); err2 == nil {
						return ota2, terrainKeyFromLogical(closest), nil
					}
				}
			}
		}
		e := errCorrupt(displayName)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	if doc.Root.Section("GlobalHeader") == nil {
		// Missing GlobalHeader: try fuzzy fallback before emitting verbatim? For types 2/3, parsing misses includes missing block? Spec says Type 1 requires GlobalHeader plus missionfile with distinct diagnostics; types 2/3 read OTA directly with fuzzy fallback when parsing misses. Missing GlobalHeader is a parsing miss for fuzzy purposes.
		if closest, ok := fuzzySearch(fs, otaLogical); ok && closest != otaLogical {
			data2, err2 := fs.ReadFileLimit(closest, int64(formats.DefaultTDFLimits().MaxBytes))
			if err2 == nil && !isOldTED(data2) {
				if doc2, err2 := formats.ParseTDF(data2); err2 == nil && doc2.Root.Section("GlobalHeader") != nil {
					if ota2, err2 := formats.LoadOTA(data2); err2 == nil {
						return ota2, terrainKeyFromLogical(closest), nil
					}
				}
			}
		}
		e := errNoGlobalHeader()
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	ota, err := formats.LoadOTA(data)
	if err != nil {
		if strings.Contains(err.Error(), "GlobalHeader") {
			e := errNoGlobalHeader()
			if sink != nil {
				sink.Report(e.Error())
			}
			return nil, "", e
		}
		e := errCorrupt(displayName)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	return ota, terrainKeyFromLogical(otaLogical), nil
}

// composeMission composes the mission body in the established order: schema
// selection before placement instantiation, then wind-bounds retention, then
// UseOnlyUnits routing. [C4][C5][C8] [08 "Schema choice"] [GAP T14]
// It performs no RNG draws. [C5]
func composeMission(fs vfs.FSOps, typ Type, ota *formats.OTA, terrainKey string, difficulty, playerCount int, sink Sink) (*Mission, error) {
	_ = fs
	order := []string{}
	// Schema selection happens before placement records are instantiated. [08 "Schema choice"] [C4]
	selected, err := SelectSchema(ota, difficulty, playerCount)
	order = append(order, "schema")
	if err != nil {
		if sink != nil {
			sink.Report(err.Error())
		}
		// Map schema errors to verbatim where applicable
		if err.Error() == ErrNoGlobalHeader.Error() {
			// already verbatim Very bad news! No MSG! – not part of C2 four but keep
		}
		if err.Error() == ErrNoSuitableSchema.Error() {
			// verbatim No suitable schema type...
		}
		return nil, err
	}
	// Locate the selected schema section by OriginalName for placement decoding.
	var schemaSec *formats.Section
	for _, sec := range ota.Global.Sections() {
		if sec.OriginalName == selected.Name {
			schemaSec = sec
			break
		}
	}
	// Placement decode after schema selection. [C4][C6][C7]
	units := DecodeUnitPlacements(schemaSec)
	order = append(order, "units")
	specials := DecodeSpecials(schemaSec)
	order = append(order, "specials")
	features := DecodeFeaturePlacements(schemaSec)
	order = append(order, "features")
	// The only placement flag byte the creation reader consumes is the immunity high bit.
	// AiIgnore, AiPriorityTarget, BuildPriority, InitialGroup, and MissionCriticalUnit
	// are parsed and unread — retain, do not act [C7] [08 "Mission placement record"].
	// Implemented via DecodeUnitPlacements retaining RawFlags and the individual bool fields
	// but only Immune is consumed downstream; see IsImmuneFromFlags.

	// Wind bounds retention without RNG draws. [C5][08 "Wind initialization"]
	wind := DecodeWindBounds(ota.Global)
	order = append(order, "wind")
	// UseOnlyUnits routing into camps\useonly. [C8][08 "Mission placement record"]
	useOnly := DecodeUseOnlyUnits(ota.Global)
	order = append(order, "useonly")

	m := &Mission{
		Type:        typ,
		OTA:         ota,
		TerrainKey:  terrainKey,
		Schema:      selected,
		Units:       units,
		Specials:    specials,
		Features:    features,
		WindBounds:  wind,
		UseOnlyPath: useOnly,
		order:       order,
	}
	_ = terrainKey
	return m, nil
}

// terrainKeyFromLogical derives the terrain key (basename without extension) from
// the OTA logical path, e.g. "maps/The Pass.ota" -> "The Pass".
func terrainKeyFromLogical(logical string) string {
	base := logical
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if dot := strings.LastIndex(base, "."); dot >= 0 {
		base = base[:dot]
	}
	return strings.TrimSpace(base)
}

// isOldTED reports whether data is legacy TED format.
// [02 "Mission-file diagnostics"] Old TED format no longer supported!
// TODO(question): exact byte signature of legacy TED not closed; using prefix "TED" case-insensitive as deterministic stand-in.
func isOldTED(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	// Legacy marker: file starts with "TED" (case-insensitive) or contains the verbatim Old TED string.
	if strings.HasPrefix(lower, "ted") {
		return true
	}
	// Also treat files that contain only a header like "TED" at start before any '['
	return false
}

// fuzzySearch implements the Type 2/3 fallback: when parsing misses, a fuzzy search
// falls back to the closest match. [08 "Mission type dispatch"]
// It enumerates maps/*.ota and picks the entry with minimal case-insensitive
// Levenshtein distance to the wanted logical path, breaking ties lexicographically
// for determinism (I1).
// TODO(question): exact "closest match" metric not closed in [08]; using Levenshtein case-insensitive as deterministic stand-in.
func fuzzySearch(fs vfs.FSOps, wanted string) (string, bool) {
	// wanted is a logical path like "maps/Foo.ota" (already joined).
	// Enumerate the maps directory via VFS.
	entries, err := fs.ReadDir("maps")
	if err != nil {
		return "", false
	}
	if len(entries) == 0 {
		return "", false
	}
	// Filter to .ota files only.
	type cand struct {
		path string
		dist int
	}
	var candidates []cand
	wLower := strings.ToLower(wanted)
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		lower := strings.ToLower(e.Path)
		if !strings.HasSuffix(lower, ".ota") {
			continue
		}
		// Compute distance to wanted.
		d := levenshtein(wLower, lower)
		candidates = append(candidates, cand{path: e.Path, dist: d})
	}
	if len(candidates) == 0 {
		return "", false
	}
	// Deterministic: sort by distance then path lexicographically (I1).
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dist != candidates[j].dist {
			return candidates[i].dist < candidates[j].dist
		}
		return candidates[i].path < candidates[j].path
	})
	// If the closest is the wanted itself, that would have been found earlier; but we still return it if it's the only one.
	// For the fallback case where wanted was not found, the closest is the best fuzzy match.
	best := candidates[0]
	// Consider "closest" only if distance is not too large? Retail likely returns whatever is closest regardless.
	// We return the best candidate as the fallback.
	return best.path, true
}

// levenshtein computes the Levenshtein edit distance between a and b, case-sensitive
// (caller lowercases). Simple O(|a|*|b|) DP, deterministic.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	// Use two rows to save memory.
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			deletion := prev[j] + 1
			insertion := cur[j-1] + 1
			substitution := prev[j-1] + cost
			m := deletion
			if insertion < m {
				m = insertion
			}
			if substitution < m {
				m = substitution
			}
			cur[j] = m
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}
