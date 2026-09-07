package mission

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/vfs"
)

// Type is the mission type discriminant. [08 "Mission type dispatch"] [C2]
// Type 1 is campaign: it goes through a camps/*.tdf file's MISSION%d block.
// Types 2 and 3 read maps/<name>.ota directly; on a read/parse miss they try
// exactly one translated-name retry through the reverse translation-table
// lookup before failing silently [08 R-CAMP-01 §11].
type Type uint8

const (
	TypeCampaign Type = 1 // [08 "Mission type dispatch"] campaign
	TypeSkirmish Type = 2 // [08 "Mission type dispatch"] skirmish/multiplayer direct OTA
	// TypeSaved selects the file-loading path for a loaded save's mission
	// OTA — dispatched identically to TypeSkirmish, direct maps/<name>.ota
	// with one translated-name retry [08 "Mission type dispatch"]. Its
	// numeral (3) is unrelated to retail's separate *session kind* word,
	// which also runs 1..3 but names campaign/skirmish/multiplayer
	// [08 R-SESS-01 §7]: a loaded save restores its session kind from the
	// save bank's own Summary.Gametype (1 or 2 only — this engine never
	// builds session kind 3) [08 "Load process"], never from this
	// discriminant. Do not read this constant's value as a session kind.
	TypeSaved Type = 3
)

// Mission is the loaded mission object. [08 "Mission object"] [C2] [C4] [C5] [C8]
// It composes the established pieces in the required order: schema selection
// before placement instantiation, then wind-bounds retention, then UseOnlyUnits
// routing. [08 "Schema choice"] [C4] [C5] [C8]
type Mission struct {
	Type        Type
	OTA         *formats.OTA
	TerrainKey  string              // base name without extension, for TNT pairing [fmt ota]
	Schema      Schema              // selected schema [08 "Schema choice"] [C3][C4]
	Units       []UnitPlacement     // 36-byte retail identity, named fields per I13 [C6]
	Specials    []Special           // 12-byte retail identity [C6]
	Features    []FeaturePlacement  // 136-byte retail identity [C6]
	WindBounds  WindBounds          // retained for PLAN_14 battle-entry initializer, no RNG [C5] [08 "Wind initialization"]
	UseOnlyPath string              // routed into camps/useonly [C8] [08 "Mission placement record"]
	Victory     []*triggers.Trigger // victory conditions [PLAN_10 C14-C17]
	Defeat      []*triggers.Trigger // defeat conditions [PLAN_10 C14-C17]
	IsRestore   bool                // BetweenMissions restore: InitialMission also runs on restores [04 §3.6] C9
	// Campaign provenance for progression [08 "Campaign discovery"] [08 "Progression"].
	// For TypeCampaign loaded via camps/*.tdf:MISSION%d, CampaignPath is the
	// logical VFS path (e.g. "camps/arm campaign.tdf") with provenance from
	// Discover / ReadFileLimit, CampaignIndex is the MISSION%d suffix, and
	// CampaignMissionName is that section's language-resolved missionname.
	// For non-campaign missions these are empty / -1.
	CampaignPath        string // logical path of the camps/*.tdf file, "" if not campaign [08 "Campaign discovery"]
	CampaignIndex       int    // MISSION%d index, -1 if not campaign
	CampaignMissionName string // language-resolved MISSION%d missionname, with catalog default [08 "Campaign discovery"]
	Difficulty          int    // difficulty value used for schema selection, -1 if not campaign
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

// Sink is the diagnostic sink per AGENTS.md §Diagnostics. Diagnostics are returned
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

// Verbatim mission-file diagnostics — six exact strings; five report through
// the status pane, the sixth (No suitable schema, defined in schema.go) uses
// a different channel [02 "Mission-file diagnostics"] [08 "Mission type
// dispatch"] [GAP T14] [C2]. Kinds 2/3's own read/parse failure is silent —
// there is no verbatim string for it; see resolveOTAWithFallback.
const (
	verbatimDoesNotExist       = "does not exist"                           // substring of campaign/mission does-not-exist diagnostic [C2]
	verbatimCorrupt            = "is corrupt (no header found)"             // substring of the kind-1 no-GlobalHeader diagnostic [C2]
	verbatimNoMissionDefintion = "There is no mission defintion"            // substring of the kind-1 unopenable/unparsable-OTA diagnostic (sic) [C2]
	verbatimOldTED             = "Old TED format no longer supported!"      // [02] [C2] verbatim
	verbatimNoGlobalHeader     = "No GlobalHeader block in mission file!"   // [02] [C2] verbatim, kinds 2/3 only
	verbatimNoSuitableSchema   = "No suitable schema type in mission file!" // [02] sixth, schema selector
)

// errDoesNotExist constructs the verbatim missing mission-file diagnostic.
// [02 "Mission-file diagnostics"] The requested mission file, %s, does not exist.
// Kind 1 only: %s is the absent MISSION%d block name [08 "Mission type dispatch"].
func errDoesNotExist(name string) error {
	//lint:ignore ST1005 retail diagnostic text, reproduced verbatim [02 "Mission-file diagnostics"]
	return fmt.Errorf("The requested mission file, %s, does not exist.", name)
}

// errCampaignDoesNotExist is the sibling for campaign-file loads: any failure
// to open or parse camps/<name>.tdf itself — not just a missing file —
// raises this diagnostic [08 "Opening a campaign"].
// [02 "Mission-file diagnostics"] The requested campaign file, %s, does not exist.
func errCampaignDoesNotExist(name string) error {
	//lint:ignore ST1005 retail diagnostic text, reproduced verbatim [08 "Opening a campaign"]
	return fmt.Errorf("The requested campaign file, %s, does not exist.", name)
}

// errCorrupt is kind 1's diagnostic for an OTA that parses but carries no
// [GlobalHeader] section; %s is the authored missionfile name
// [02 "Mission-file diagnostics"] [08 "Mission loader, campaign branch (kind 1)"].
// Hey, joker!  Mission file %s is corrupt (no header found). (two spaces after joker!)
func errCorrupt(name string) error {
	//lint:ignore ST1005 retail diagnostic text, reproduced verbatim [02 "Mission-file diagnostics"]
	return fmt.Errorf("Hey, joker!  Mission file %s is corrupt (no header found).", name)
}

// errNoMissionDefinition is kind 1's diagnostic for a missionfile OTA that
// fails to open or parse at all; %s is the authored missionfile name (sic:
// "defintion" is verbatim retail spelling)
// [02 "Mission-file diagnostics"] [08 "Mission loader, campaign branch (kind 1)"].
func errNoMissionDefinition(name string) error {
	//lint:ignore ST1005 retail diagnostic text, reproduced verbatim, misspelling included [02 "Mission-file diagnostics"]
	return fmt.Errorf("Hey, joker!  There is no mission defintion for this mission: %s", name)
}

// errOldTED is verbatim, kind 1 only, raised when the MISSION%d block has no
// missionfile key at all — a campaign-file key test, not a byte signature of
// any map file [02 "Mission-file diagnostics"] [08 R-CAMP-01 §11 point 2].
func errOldTED() error { return fmt.Errorf("%s", verbatimOldTED) }

// errNoGlobalHeader is verbatim, kinds 2/3 only: a directly supplied OTA that
// parses but carries no [GlobalHeader] section
// [02 "Mission-file diagnostics"] [C2].
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
	// Default to skirmish direct OTA (Type 2) [08 "Mission type dispatch"].
	return LoadWithType(fs, TypeSkirmish, path, 0, 0, nil)
}

// LoadWithType loads a mission with explicit type discriminant. [08 "Mission
// type dispatch"] [C2] Type 1 requires a campaign wrapper (use LoadCampaign);
// this entry treats logicalPath as a direct OTA path even for TypeCampaign,
// with no translated-name retry (strict). Types 2 and 3 read maps/<name>.ota
// with the translated-name retry [08 R-CAMP-01 §11]. Schema selection happens
// before placement records are instantiated [C4]; wind bounds are retained
// without RNG draws [C5]; UseOnlyUnits is routed [C8].
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
	// Any failure to open or parse camps/<name>.tdf raises the campaign
	// does-not-exist box, not a distinct "corrupt" message
	// [08 "Opening a campaign"].
	data, err := fs.ReadFileLimit(campaignPath, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		e := errCampaignDoesNotExist(campaignPath)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	doc, err := formats.ParseTDF(data)
	if err != nil || doc.Root == nil || len(doc.Root.Sections()) == 0 {
		e := errCampaignDoesNotExist(campaignPath)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	// Build MISSION%d section name [08 "Mission list"]. A missing block
	// raises the mission-file does-not-exist box naming the block itself,
	// not the campaign path [02 "Mission-file diagnostics"].
	secName := fmt.Sprintf("MISSION%d", missionIndex)
	sec := doc.Root.Section(secName)
	if sec == nil {
		e := errDoesNotExist(secName)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	missionName, _ := sec.LanguageString("", "missionname", "Error -- Unnamed Mission")
	missionName = strings.TrimSpace(missionName)
	if missionName == "" {
		missionName = "Error -- Unnamed Mission"
	}
	// missionfile absence (the key not authored at all, not merely empty) is
	// the sole Old-TED test — a campaign-file key test, not any byte
	// signature of a map file [08 R-CAMP-01 §11 point 2].
	missionFileRaw, present := sec.RawValue("missionfile")
	if !present {
		e := errOldTED()
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
	missionFile := strings.TrimSpace(missionFileRaw)
	// An empty missionfile value counts as present: the path becomes
	// Maps\.OTA, which then fails to open/parse and raises the "no mission
	// defintion" box below, not Old TED [08 R-CAMP-01 §11 point 2].
	otaLogical := buildOTAPath(missionFile)
	ota, terrKey, err := resolveOTAStrict(fs, otaLogical, missionFile, sink)
	if err != nil {
		return nil, err
	}
	// Compose mission body in established order: schema -> placement -> wind -> useonly. [C4][C5][C8]
	m, err := composeMission(fs, TypeCampaign, ota, terrKey, difficulty, playerCount, sink)
	if err != nil {
		return nil, err
	}
	// Provenance for progression: retain campaign file and index [08 "Campaign discovery"].
	m.CampaignPath = campaignPath
	m.CampaignIndex = missionIndex
	m.CampaignMissionName = missionName
	m.Difficulty = difficulty
	return m, nil
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
		// Direct OTA path supplied as a TypeCampaign load (no campaign
		// wrapper). This is not itself a retail-attested entry point — real
		// Type 1 always goes through camps/*.tdf:MISSION%d, above — but it is
		// resolved with the same strict (no retry) rule as kind 1's OTA step.
		otaLogical := buildOTAPath(logicalPath)
		ota, terrKey, err := resolveOTAStrict(fs, otaLogical, logicalPath, sink)
		if err != nil {
			return nil, err
		}
		return composeMission(fs, typ, ota, terrKey, difficulty, playerCount, sink)
	case TypeSkirmish, TypeSaved:
		// Types 2 and 3 build maps/<name>.ota directly; on a read/parse miss
		// they retry exactly once via the reverse translation-table lookup,
		// then fail silently [08 R-CAMP-01 §11].
		ota, terrKey, err := resolveOTAWithFallback(fs, defaultLanguage, logicalPath, sink)
		if err != nil {
			return nil, err
		}
		return composeMission(fs, typ, ota, terrKey, difficulty, playerCount, sink)
	default:
		e := fmt.Errorf("mission: unknown type %d", typ)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, e
	}
}

// defaultLanguage is the current-language string reaching this package. No
// caller in this codebase yet plumbs the configured language this far (the
// content catalog's language-prefixed name trial takes the same parameter
// explicitly from its own caller, unresolved for the same reason — see
// CompileUnitsWithLanguage); an empty string is retail's own default and
// selects English [02 "Translation table"]. With the stock English table
// this never matters: no authored section carries an empty-named key, so the
// translation table loaded for English is always empty and the reverse
// lookup below never has an entry to find — matching the residual
// Supported-inference note in [08 R-CAMP-01 §11] that the fallback only
// fires for a non-English translation file.
const defaultLanguage = ""

// buildOTAPath builds Maps\<name>.OTA with the extension replaced: any
// directory component and any existing extension on name are discarded, and
// ".ota" is appended [08 R-CAMP-01 §11] (e.g. an empty name yields
// "maps/.ota", matching retail's Maps\.OTA for an authored-empty
// missionfile). The same construction is used for kinds 1, 2 and 3.
func buildOTAPath(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[:dot]
	}
	return "maps/" + name + ".ota"
}

// tryLoadOTA attempts to read and structurally parse the OTA at otaLogical
// through the single formats.LoadOTA entry point, which fails exactly two
// ways: a TDF syntax error, or a document with no [GlobalHeader] section.
// miss reports the former — the read-or-parse failure that kinds 2/3's
// translated-name retry exists for [08 R-CAMP-01 §11 point 1]. noGlobalHeader
// reports the latter, a distinct condition that is never retried — the
// common tail's [GlobalHeader] seek raises its own diagnostic instead
// [08 "Common tail (all kinds)"].
func tryLoadOTA(fs vfs.FSOps, otaLogical string) (ota *formats.OTA, terrainKey string, noGlobalHeader bool, miss bool) {
	data, err := fs.ReadFileLimit(otaLogical, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		return nil, "", false, true
	}
	o, err := formats.LoadOTA(data)
	if err != nil {
		if strings.Contains(err.Error(), "GlobalHeader") {
			return nil, "", true, false
		}
		return nil, "", false, true
	}
	return o, terrainKeyFromLogical(otaLogical), false, false
}

// resolveOTAStrict resolves kind 1's OTA (both the campaign-wrapped path and
// the direct-OTA TypeCampaign entry): no translated-name retry — that
// mechanism belongs only to kinds 2/3 [08 R-CAMP-01 §11 point 1]. The two
// ways the OTA step can fail carry distinct verbatim diagnostics naming
// missionFileName: unopenable/unparsable raises the "no mission defintion"
// box, and parsed-but-missing-[GlobalHeader] raises the "is corrupt" box —
// the same wording the campaign-file-open failure uses, but naming the
// missionfile instead of the campaign path
// [02 "Mission-file diagnostics"] [08 "Mission loader, campaign branch (kind 1)"].
func resolveOTAStrict(fs vfs.FSOps, otaLogical, missionFileName string, sink Sink) (*formats.OTA, string, error) {
	ota, terrKey, noGlobal, miss := tryLoadOTA(fs, otaLogical)
	if noGlobal {
		e := errCorrupt(missionFileName)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	if miss {
		e := errNoMissionDefinition(missionFileName)
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	return ota, terrKey, nil
}

// resolveOTAWithFallback resolves kinds 2/3's OTA [08 R-CAMP-01 §11 point 1].
// It tries maps/<requestedName>.ota; on a read/parse miss it looks
// requestedName up as a translated map display name in the translation table
// for language (case-insensitive equality on the translated text, first
// entry in source-sorted order) and, on a hit, retries once with the source
// string as both the display name and the file name. No table, no matching
// entry, or a second miss fails the load SILENTLY: no diagnostic is reported
// by this function — the skirmish Start preflight is what tells the player
// "The terrain for the selected map does not exist." A parsed OTA with no
// [GlobalHeader] is never retried and still raises the verbatim
// "No GlobalHeader block in mission file!" whether it is the first
// candidate or the retried one.
func resolveOTAWithFallback(fs vfs.FSOps, language, requestedName string, sink Sink) (*formats.OTA, string, error) {
	otaLogical := buildOTAPath(requestedName)
	ota, terrKey, noGlobal, miss := tryLoadOTA(fs, otaLogical)
	if noGlobal {
		e := errNoGlobalHeader()
		if sink != nil {
			sink.Report(e.Error())
		}
		return nil, "", e
	}
	if !miss {
		return ota, terrKey, nil
	}
	// Read/parse miss: exactly one retry through the reverse translation
	// lookup [08 R-CAMP-01 §11 point 1].
	table, err := content.LoadTranslationTable(fs, language)
	if err != nil {
		if sink != nil {
			sink.Report(err.Error())
		}
		return nil, "", err
	}
	if source, hit := table.Source(requestedName); hit {
		retryLogical := buildOTAPath(source)
		ota2, terrKey2, noGlobal2, miss2 := tryLoadOTA(fs, retryLogical)
		if noGlobal2 {
			e := errNoGlobalHeader()
			if sink != nil {
				sink.Report(e.Error())
			}
			return nil, "", e
		}
		if !miss2 {
			return ota2, terrKey2, nil
		}
	}
	// Silent failure: no message box from the loader
	// [02 "Mission-file diagnostics"] [08 R-CAMP-01 §11 point 1].
	return nil, "", fmt.Errorf("mission: map %q not found", requestedName)
}

// composeMission composes the mission body in the established order: schema
// selection before placement instantiation, then wind-bounds retention, then
// UseOnlyUnits routing. [C4][C5][C8] [08 "Schema choice"] [GAP T14]
// It performs no RNG draws. [C5]
func composeMission(fs vfs.FSOps, typ Type, ota *formats.OTA, terrainKey string, difficulty, playerCount int, sink Sink) (*Mission, error) {
	_ = fs
	order := []string{}
	// Schema selection happens before placement records are instantiated. [08 "Schema choice"] [C4]
	selected, err := SelectSchemaForType(ota, typ, difficulty, playerCount)
	order = append(order, "schema")
	if err != nil {
		if sink != nil {
			sink.Report(err.Error())
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

	// The common tail "builds trigger objects" [08 "Mission type dispatch"]:
	// the authored end conditions are read out of [GlobalHeader] and the two
	// defaults injected when a queue comes back empty, so every loaded mission
	// has at least one way to win and one way to lose [08 "Default triggers"]
	// [C14][C16]. A mission that reaches the tick site with empty queues can
	// never end.
	victory, defeat := DecodeTriggers(ota.Global)
	victory, defeat = triggers.EnsureDefaults(victory, defeat)
	order = append(order, "triggers")

	m := &Mission{
		Type:          typ,
		OTA:           ota,
		TerrainKey:    terrainKey,
		Schema:        selected,
		Units:         units,
		Specials:      specials,
		Features:      features,
		WindBounds:    wind,
		UseOnlyPath:   useOnly,
		Victory:       victory,
		Defeat:        defeat,
		CampaignPath:  "",
		CampaignIndex: -1,
		Difficulty:    -1,
		order:         order,
	}
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
