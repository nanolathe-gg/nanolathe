// The map header compiler.

package content

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// MapSchema is one compiled schema variant from a map OTA [fmt ota] [02 "Map files"].
// Schemas are probed as [Schema 0], [Schema 1] ... until missing [fmt ota].
// The difficulty literals are Easy, Medium, Hard; the network literals are
// Network 1 .. Network 4 [fmt ota] [08 "Schema choice"].
type MapSchema struct {
	// Name is the section name as authored, e.g. "Schema 0" — preserved for diagnostics [fmt ota].
	Name string
	// Type is the schema kind, e.g. "Network 1" or "Easy" [fmt ota] [02 "Map files"].
	Type      string
	AIProfile string // aiprofile string empty [02 "Map files"] [08 "Computer-controlled players"]
	// Resources — authored per schema, stored as typed integers per [02 "Map files"].
	SurfaceMetal   int32 // SurfaceMetal integer default 0 [02 "Map files"]
	MohoMetal      int32 // MohoMetal integer default 0, inert [fmt ota]
	HumanMetal     int32 // HumanMetal integer default 0 [02 "Map files"]
	HumanEnergy    int32 // HumanEnergy integer default 0 [02 "Map files"]
	ComputerMetal  int32 // ComputerMetal integer default 0 [02 "Map files"]
	ComputerEnergy int32 // ComputerEnergy integer default 0 [02 "Map files"]
	// Meteor storm authored per schema [02 "Map files"] [08 "Meteor showers"].
	MeteorWeapon   string  // MeteorWeapon string empty — empty disables [02 "Map files"]
	MeteorRadius   int32   // MeteorRadius integer default 0 [02 "Map files"]
	MeteorDensity  float64 // MeteorDensity floating default 0.0 [02 "Map files"]
	MeteorDuration float64 // MeteorDuration floating default 0.0 [02 "Map files"]
	MeteorInterval float64 // MeteorInterval floating default 0.0 [02 "Map files"]
	// StartPosCount is the number of StartPos specials in this schema [fmt ota] [02 "Map files"].
	// Network schema selection counts StartPos to match lobby player count [08 "Schema choice"].
	StartPosCount int
}

// MapHeader is a compiled map header (OTA + TNT) [02 "Map files"] [fmt ota] [fmt tnt].
// DefinitionHeader must be the first field per catalog convention [02 §5].
// Headers only in this phase — placements ([units], [features], [specials] aside from
// StartPos counts) are retained inside the raw OTA Document for mission consumers
// and are not expanded here [PLAN 02].
type MapHeader struct {
	DefinitionHeader
	// Name is the map's file basename without extension, e.g. "Greenhaven" or
	// "A Gentle Time". The canonical key is CanonicalKey(Name) [02 §5].
	Name string
	// Logical paths (winning VFS paths, folded) for diagnostics.
	LogicalOTA string // e.g. maps/greenhaven.ota
	LogicalTNT string // e.g. maps/greenhaven.tnt
	// Provisional provenance for diagnostics.
	OTAProvenance Provenance
	TNTProvenance Provenance

	// OTA GlobalHeader fields [fmt ota] [02 "Map files"].
	// Language-prefixed keys use LanguageString with empty language (English fallback)
	// so the plain key is read; the typed accessor family is still used [02 §3] [02 §4].
	MissionName        string // reserved campaign field; not read from OTA GlobalHeader [02 "Map files"]
	MissionDescription string // missiondescription string default "No description available" [02 "Map files"]
	MissionFile        string // reserved campaign field; not read from OTA GlobalHeader [02 "Map files"]
	Planet             string // planet string empty [02 "Map files"] — vocabulary listed in [fmt ota]
	Memory             string // memory string empty [fmt ota]
	NumPlayers         string // numplayers string empty [fmt ota]
	Size               string // size string empty, e.g. "16 x 16" in 512-pixel squares [fmt ota]
	Brief              string // brief language-prefixed empty [fmt ota]
	Narration          string // narration language-prefixed empty [fmt ota]
	MissionHint        string // missionhint language-prefixed empty [fmt ota]
	Glamour            string // glamour string empty [fmt ota]
	GlamourSound       string // glamoursound string empty [fmt ota]
	UseOnlyUnits       string // useonlyunits string empty [fmt ota] [08 "Mission placement record"]

	// Environment [02 "Map files"] [fmt ota].
	LineOfSight       int32   // lineofsight integer default 0 [02 "Map files"]
	Mapping           int32   // mapping integer default 0 [02 "Map files"]
	NoMovie           int32   // nomovie integer default 0 [fmt ota]
	TidalStrength     float64 // tidalstrength floating default 0.0 [02 "Map files"]
	SolarStrength     int32   // reserved/inert OTA vocabulary; not read [fmt ota]
	LavaWorld         int32   // lavaworld integer default 0 [02 "Map files"]
	WaterDoesDamage   int32   // waterdoesdamage integer default 0 [fmt ota]
	WaterDamage       int32   // waterdamage integer default 0 [fmt ota]
	NoSeaLevelTrigger int32   // nosealeveltrigger integer default 0 [fmt ota]
	KillMul           float64 // killmul floating default 0.0 [fmt ota]
	TimeMul           float64 // timemul floating default 0.0 [fmt ota]
	MinWindSpeed      int32   // minwindspeed integer default 0 [02 "Map files"]
	MaxWindSpeed      int32   // maxwindspeed integer default 0 [02 "Map files"]
	Gravity           int32   // gravity integer default 0 [02 "Map files"] — 112 is common retail default
	MaxUnits          int32   // maxunits integer default 200 [02 "Map files"]

	// Schemas discovered as [Schema 0] upward until missing [fmt ota].
	Schemas []MapSchema

	// TNT header preview [fmt tnt]. Headers only — tile graphics and feature
	// table are not retained here; the world loader reads the full TNT per map
	// selection. SeaLevel is the water level in height units [fmt tnt].
	TNTWidth       uint32 // Width in 16-pixel cells [fmt tnt] — always even
	TNTHeight      uint32 // Height in 16-pixel cells [fmt tnt]
	TNTSeaLevel    uint32 // SeaLevel [fmt tnt]
	TNTTiles       uint32 // Tiles — unique 32x32 tiles [fmt tnt]
	TNTTileAnims   uint32 // TileAnims — feature records, misnamed [fmt tnt]
	MinimapWidth   uint32 // minimap w [fmt tnt]
	MinimapHeight  uint32 // minimap h [fmt tnt]
	TNTVersion     uint32 // IDVersion, must be 0x2000 for retail [fmt tnt]
	UnknownHeader1 uint32 // header word 0x2C — always 1 in retail corpus [fmt tnt]

	// Raw OTA is retained for mission consumers that need placement records later.
	// It is immutable after compilation; sim packages never mutate it.
	RawOTA *formats.OTA
}

// compileMapHeader compiles a single paired OTA/TNT into a MapHeader [02 "Map files"] [fmt ota] [fmt tnt].
// It uses typed accessors only from formats/tdf_typed.go for OTA fields [02 §4].
func compileMapHeader(otaLogical, tntLogical string, otaProv, tntProv Provenance, ota *formats.OTA, tntHeader tntHeaderLite) *MapHeader {
	mh := &MapHeader{
		Name:           baseNameWithoutExt(otaLogical),
		LogicalOTA:     otaLogical,
		LogicalTNT:     tntLogical,
		OTAProvenance:  otaProv,
		TNTProvenance:  tntProv,
		RawOTA:         ota,
		TNTWidth:       tntHeader.Width,
		TNTHeight:      tntHeader.Height,
		TNTSeaLevel:    tntHeader.SeaLevel,
		TNTTiles:       tntHeader.Tiles,
		TNTTileAnims:   tntHeader.TileAnims,
		MinimapWidth:   tntHeader.MinimapWidth,
		MinimapHeight:  tntHeader.MinimapHeight,
		TNTVersion:     tntHeader.Version,
		UnknownHeader1: tntHeader.Unknown1,
	}

	global := ota.Global
	if global != nil {
		// Use typed accessors only [02 §4]. StringValue and LanguageString are the
		// typed string family; IntValue/FloatValue are the numeric family.
		// Language-prefixed keys try <language><key> then plain key [02 §3].
		mh.MissionDescription, _ = global.StringValue("missiondescription", "No description available")
		mh.Planet, _ = global.StringValue("planet", "")
		mh.Memory, _ = global.StringValue("memory", "")
		mh.NumPlayers, _ = global.StringValue("numplayers", "")
		mh.Size, _ = global.StringValue("size", "")
		mh.Brief, _ = global.LanguageString("", "brief", "")
		mh.Narration, _ = global.LanguageString("", "narration", "")
		mh.MissionHint, _ = global.LanguageString("", "missionhint", "")
		mh.Glamour, _ = global.StringValue("glamour", "")
		mh.GlamourSound, _ = global.StringValue("glamoursound", "")
		mh.UseOnlyUnits, _ = global.StringValue("useonlyunits", "")

		mh.LineOfSight = global.IntValue("lineofsight", 0)             // [02 "Map files"]
		mh.Mapping = global.IntValue("mapping", 0)                     // [02 "Map files"]
		mh.NoMovie = global.IntValue("nomovie", 0)                     // [fmt ota]
		mh.TidalStrength = global.FloatValue("tidalstrength", 0)       // [02 "Map files"]
		mh.LavaWorld = global.IntValue("lavaworld", 0)                 // [02 "Map files"]
		mh.WaterDoesDamage = global.IntValue("waterdoesdamage", 0)     // [fmt ota]
		mh.WaterDamage = global.IntValue("waterdamage", 0)             // [fmt ota]
		mh.NoSeaLevelTrigger = global.IntValue("nosealeveltrigger", 0) // [fmt ota]
		mh.KillMul = global.FloatValue("killmul", 0)                   // [fmt ota]
		mh.TimeMul = global.FloatValue("timemul", 0)                   // [fmt ota]
		mh.MinWindSpeed = global.IntValue("minwindspeed", 0)           // [02 "Map files"]
		mh.MaxWindSpeed = global.IntValue("maxwindspeed", 0)           // [02 "Map files"]
		mh.Gravity = global.IntValue("gravity", 0)                     // [02 "Map files"]
		mh.MaxUnits = global.IntValue("maxunits", 200)                 // [02 "Map files"] default 200
	}

	// Reuse the format layer's contiguous first-match projection [02 R-MAP-01 §4].
	if global != nil {
		for _, schema := range ota.Schemas {
			sec := schema.Section
			sch := MapSchema{
				Name: schema.Name,
				Type: schema.Type,
			}
			// Use typed accessors only [02 §4].
			sch.AIProfile, _ = sec.StringValue("aiprofile", "")
			sch.SurfaceMetal = sec.IntValue("surfacemetal", 0)        // [02 "Map files"]
			sch.HumanMetal = sec.IntValue("humanmetal", 0)            // [02 "Map files"]
			sch.HumanEnergy = sec.IntValue("humanenergy", 0)          // [02 "Map files"]
			sch.ComputerMetal = sec.IntValue("computermetal", 0)      // [02 "Map files"]
			sch.ComputerEnergy = sec.IntValue("computerenergy", 0)    // [02 "Map files"]
			sch.MeteorWeapon, _ = sec.StringValue("meteorweapon", "") // [02 "Map files"]
			sch.MeteorRadius = sec.IntValue("meteorradius", 0)        // [02 "Map files"]
			sch.MeteorDensity = sec.FloatValue("meteordensity", 0)    // [02 "Map files"]
			sch.MeteorDuration = sec.FloatValue("meteorduration", 0)  // [02 "Map files"]
			sch.MeteorInterval = sec.FloatValue("meteorinterval", 0)  // [02 "Map files"]
			// Count StartPos specials per schema for network schema selection [08 "Schema choice"].
			if specials := sec.Section("specials"); specials != nil {
				for _, sp := range specials.Sections() {
					if sw, ok := sp.StringValue("specialwhat", ""); ok {
						if strings.HasPrefix(CanonicalKey(sw), "startpos") {
							sch.StartPosCount++
						}
					}
				}
			}
			mh.Schemas = append(mh.Schemas, sch)
		}
	}
	// Deterministic hash over canonical bytes including defaults, independent of map
	// iteration and provider order (I1) [02 §5] C12.
	// Never range a map here; schemas are already in probed order; sorting is not needed
	// because discovery order is file-order then probed upwards via OTA's own scan [fmt ota].
	// Hash includes all scalar defaults.
	mh.DefinitionHeader = DefinitionHeader{
		CanonicalKey: CanonicalKey(mh.Name),
		Provenance:   mh.OTAProvenance,
	}
	var b strings.Builder
	// Every compiled scalar is hashed, including defaults [02 §5] C12.
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s|", mh.CanonicalKey, mh.Name, mh.MissionDescription, mh.Planet, mh.Size, mh.NumPlayers, mh.Memory)
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|", mh.Brief, mh.Narration, mh.MissionHint, mh.Glamour, mh.GlamourSound, mh.UseOnlyUnits)
	fmt.Fprintf(&b, "%d|%d|%d|%d|", mh.LineOfSight, mh.Mapping, mh.NoMovie, mh.LavaWorld)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%.10f|%d|", mh.WaterDoesDamage, mh.WaterDamage, mh.NoSeaLevelTrigger, mh.MinWindSpeed, mh.TidalStrength, mh.Gravity)
	fmt.Fprintf(&b, "%d|%d|%d|%.10f|%.10f|", mh.MaxUnits, mh.MaxWindSpeed, mh.TNTVersion, mh.KillMul, mh.TimeMul)
	for i, s := range mh.Schemas {
		fmt.Fprintf(&b, "schema%d:%s|%s|%s|%d|%d|%d|%d|%d|%s|%d|%.10f|%.10f|%.10f|%d|", i, s.Name, s.Type, s.AIProfile, s.SurfaceMetal, s.HumanMetal, s.HumanEnergy, s.ComputerMetal, s.ComputerEnergy, s.MeteorWeapon, s.MeteorRadius, s.MeteorDensity, s.MeteorDuration, s.MeteorInterval, s.StartPosCount)
	}
	fmt.Fprintf(&b, "tnt:%d|%d|%d|%d|%d|%d|%d|%d|", mh.TNTWidth, mh.TNTHeight, mh.TNTSeaLevel, mh.TNTTiles, mh.TNTTileAnims, mh.MinimapWidth, mh.MinimapHeight, mh.UnknownHeader1)
	mh.Hash = HashDefinition([]byte(b.String()))
	return mh
}

// tntHeaderLite is a lightweight TNT header preview [fmt tnt]. Headers only in this
// phase — tile graphics and feature table are not retained [PLAN 02].
type tntHeaderLite struct {
	Version        uint32
	Width          uint32
	Height         uint32
	SeaLevel       uint32
	Tiles          uint32
	TileAnims      uint32
	Unknown1       uint32
	MiniMapPresent bool
	MinimapWidth   uint32
	MinimapHeight  uint32
}

// loadTNTHeaderLite parses a TNT header without retaining tile graphics [fmt tnt].
// It reads the raw file and extracts the header integers, including the minimap
// dimensions at PtrMiniMap. This is the lightweight path requested in [PLAN 02].
// loadTNTHeaderLite reads a map's TNT header and its minimap dimensions
// without materializing the terrain.
//
// This matters at the scale of a whole install: a retail install's 275 TNT
// files are 1.3 GB decompressed, and the minimap header sits between 76% and
// 99.5% of the way through each one, so reading them whole to collect a few
// kilobytes of headers is what a map census costs if it opens files instead of
// ranges. Retail's own census opens only the OTA of each map and never its
// terrain [07 §4].
func loadTNTHeaderLite(fs vfs.FSOps, logical string) (tntHeaderLite, error) {
	read := func(offset int64, length int) ([]byte, error) {
		if ranged, ok := fs.(vfs.RangeReader); ok {
			return ranged.ReadFileRange(logical, offset, length)
		}
		data, err := fs.ReadFileLimit(logical, 16<<20)
		if err != nil {
			return nil, err
		}
		if offset > int64(len(data)) {
			return nil, nil
		}
		data = data[offset:]
		if length >= 0 && length < len(data) {
			data = data[:length]
		}
		return data, nil
	}
	head, err := read(0, 0x40)
	if err != nil {
		return tntHeaderLite{}, err
	}
	if len(head) < 0x40 {
		return tntHeaderLite{}, fmt.Errorf("tnt: %s: file is too small", logical)
	}
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(head[off : off+4]) }
	h := tntHeaderLite{
		Version:   u32(0x00),
		Width:     u32(0x04),
		Height:    u32(0x08),
		SeaLevel:  u32(0x24),
		Tiles:     u32(0x18),
		TileAnims: u32(0x1c),
		Unknown1:  u32(0x2c),
	}
	// The minimap offset and flag are version-dependent: slot 10 on canonical files, slot
	// 14 on legacy ones, where slot 10 is the minimum wind speed instead
	// [03 §2.2], [02 "Terrain file"]. Reading slot 10 unconditionally seeks into
	// the middle of a legacy file.
	ptrMini := u32(0x28)
	miniMapPresent := u32(0x2c)&1 != 0
	if h.Version == versionLegacyTNT {
		ptrMini = u32(0x38)
		miniMapPresent = u32(0x3c)&1 != 0
	}
	h.MiniMapPresent = miniMapPresent
	if h.MiniMapPresent {
		if mini, err := read(int64(ptrMini), 8); err == nil && len(mini) == 8 {
			h.MinimapWidth = binary.LittleEndian.Uint32(mini)
			h.MinimapHeight = binary.LittleEndian.Uint32(mini[4:])
		}
	}
	return h, nil
}

// versionLegacyTNT is the legacy TNT version word [03 §2.2]. The catalog still
// lists a legacy map — only loading its terrain fails, in world.Load — so this
// header reader must resolve its slots correctly rather than reject.
const versionLegacyTNT = 0x1020

func baseNameWithoutExt(logical string) string {
	base := logical
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if dot := strings.LastIndex(base, "."); dot >= 0 {
		base = base[:dot]
	}
	// Preserve original spacing/case for Name, but trim spaces for canonical.
	return base
}

// CompileMaps compiles map headers from maps/*.ota + maps/*.tnt [02 "Map files"] [fmt ota] [fmt tnt].
// Discovery is via VFS union directory "maps" filtered to *.ota and paired with *.tnt [PLAN 02 Discovery].
// It is headers only in this phase — OTA is parsed via formats/ota.go and TNT header
// via a lightweight header reader inspired by formats/tnt.go [PLAN 02].
// It returns a map keyed by CanonicalKey(basename) [02 §5].
func CompileMaps(fs vfs.FSOps) (map[string]*MapHeader, error) {
	return compileMapsWithProgress(fs, nil)
}

// compileMapsWithProgress is CompileMaps with a running percentage. The map
// census reads one OTA and one TNT header per installed map, so on a full
// retail install it dominates a whole-install compile; it is the only family
// the loading screen can show advancing rather than completing.
func compileMapsWithProgress(fs vfs.FSOps, report Progress) (map[string]*MapHeader, error) {
	maps, _, err := compileMapsWithDiagnostics(fs, report)
	return maps, err
}

// compileMapsWithDiagnostics retains per-candidate rejection diagnostics for
// the catalog. A parsed OTA without GlobalHeader is a nonfatal discovery miss,
// before any terrain access [02 R-MAP-01 §2][02 R-MAP-01 §9].
func compileMapsWithDiagnostics(fs vfs.FSOps, report Progress) (map[string]*MapHeader, []string, error) {
	if fs == nil {
		return nil, nil, fmt.Errorf("content: nil VFS")
	}
	entries, err := fs.ReadDir("maps")
	if err != nil {
		return nil, nil, fmt.Errorf("content: maps: %w", err)
	}
	// ReadDir is sorted by Path [vfs.ReadDir] (I1). Build paired sets.
	otaByBase := make(map[string]vfs.EntryInfo) // base -> entry
	tntByBase := make(map[string]vfs.EntryInfo)
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		lower := asciiFoldContent(e.Path)
		if strings.HasSuffix(lower, ".ota") {
			base := strings.TrimSuffix(strings.TrimPrefix(lower, "maps/"), ".ota")
			otaByBase[base] = e
		} else if strings.HasSuffix(lower, ".tnt") {
			base := strings.TrimSuffix(strings.TrimPrefix(lower, "maps/"), ".tnt")
			tntByBase[base] = e
		}
	}
	// Deterministic iteration over paired bases (I1).
	bases := make([]string, 0, len(otaByBase))
	for b := range otaByBase {
		if _, ok := tntByBase[b]; ok {
			bases = append(bases, b)
		}
	}
	sort.Strings(bases)
	result := make(map[string]*MapHeader, len(bases))
	var warnings []string
	for i, base := range bases {
		if len(bases) != 0 {
			report.Report(FamilyMaps, i*100/len(bases))
		}
		otaEntry := otaByBase[base]
		tntEntry := tntByBase[base]
		otaLogical := otaEntry.Path
		tntLogical := tntEntry.Path
		otaProv := ProvenanceFrom(otaEntry)
		tntProv := ProvenanceFrom(tntEntry)
		// OTA via formats/ota.go [PLAN 02].
		// The parser owns the host byte bound; there is no separate retail OTA
		// size gate [02 R-MALF-01 §4].
		otaData, err := fs.ReadFileLimit(otaLogical, int64(formats.DefaultTDFLimits().MaxBytes))
		if err != nil {
			return nil, warnings, fmt.Errorf("content: %s: %w", otaLogical, err)
		}
		ota, err := formats.LoadOTA(otaData)
		if errors.Is(err, formats.ErrMissingOTAHeader) {
			warnings = append(warnings, fmt.Sprintf("nanolathe: map rejected: logical path %s, providers searched [%s], expected OTA GlobalHeader: %v", otaLogical, otaProv.ProviderID, err))
			continue
		}
		if err != nil {
			return nil, warnings, fmt.Errorf("content: %s: %w", otaLogical, err)
		}
		// TNT header lightweight [fmt tnt].
		hdr, err := loadTNTHeaderLite(fs, tntLogical)
		if err != nil {
			return nil, warnings, fmt.Errorf("content: %s: %w", tntLogical, err)
		}
		mh := compileMapHeader(otaLogical, tntLogical, otaProv, tntProv, ota, hdr)
		key := CanonicalKey(mh.Name)
		// Duplicate canonical keys: last wins in sorted order is deterministic (I1).
		result[key] = mh
	}
	return result, warnings, nil
}
