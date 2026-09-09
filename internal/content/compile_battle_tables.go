// The LOS and meteor battle-table compilers [02 §6] [PLAN_02 C15].

package content

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// LOSTable is one compiled LOS table [02 "SIDE and battle interface data"] [fmt tdf] [PLAN_02 C15].
// Each retail table is a list of integer triples/lines authored as
// lineN= a, b, c, d, ... ; the first value in a line is the entry count
// but the compiler preserves the full integer list verbatim so Phase 5
// can apply the terrain-ray clamping [03 §3.2] C2 without re-parsing (I8).
type LOSTable struct {
	TableNum int       // 1 for TABLE1 etc [PLAN_02]
	NumLines int32     // numlines integer default 0 [02 §6]
	Lines    [][]int32 // each lineN parsed as []int32 in authored order
}

// LOSTables is the compiled gamedata/los.tdf [02 §6] [PLAN_02 C15] [GAP T14].
// DefinitionHeader must be the first field per catalog convention [02 §5].
// Provenance is the winning gamedata/los.tdf provider.
type LOSTables struct {
	DefinitionHeader
	NumTables int32      // TABLEINFO numtables integer default 0, retains source precision [PLAN_02 C15]
	Tables    []LOSTable // sorted by TableNum ascending (I1) [02 §5]
}

// MeteorDefaults is the compiled gamedata/meteor.tdf [02 §6] [PLAN_02 C15].
// DefinitionHeader must be first field [02 §5]. Values retain source precision
// and are not re-parsed by combat [PLAN_02 C15] (I8).
type MeteorDefaults struct {
	DefinitionHeader
	// DefaultPresent distinguishes a missing file/section, which leaves a
	// selected mission record unchanged, from a present record with bad values.
	// DefaultValid is deferred validation: an invalid default is fatal only when
	// a selected schema asks for whole-record substitution [02 §6][06 §6.5].
	DefaultPresent bool
	DefaultValid   bool
	MeteorWeapon   string  // MeteorWeapon string, empty is a valid present value [02 §6]
	MeteorRadius   int32   // MeteorRadius integer default 0 [02 §6]
	MeteorDensity  float32 // source single store [06 §6.5]
	MeteorDuration float32 // source single store [06 §6.5]
	MeteorInterval float32 // source single store [06 §6.5]
}

// parseLOSLine parses a LOS line value like " 1, 0, 1" or " 2, 0, 1, 0, 2" into ints.
// It splits on commas, trims spaces, and uses ParseTDFInteger which tolerates
// trailing junk and returns 0 for unparsable tokens [02 §4].
func parseLOSLine(value string) []int32 {
	value = trimContentCWhitespace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]int32, 0, len(parts))
	for _, p := range parts {
		p = trimContentCWhitespace(p)
		if p == "" {
			continue
		}
		out = append(out, formats.ParseTDFInteger(p))
	}
	return out
}

// CompileLOSTables compiles gamedata/los.tdf into LOSTables [02 §6] [PLAN_02 C15].
// The file is an ordinary TDF with [TABLEINFO] { numtables=9 } and [TABLE1]...
// sections each with numlines and line1..lineN [research/formats/tdf.md LOS.TDF].
// Retail declares 9 but contains 12; the compiler preserves all discovered TABLE
// sections sorted by numeric suffix (I1) so downstream can clamp into the parsed
// range [03 §3.2] C2. Phases 5 consumes this compiled form and does not re-parse (I8).
//
// The authored LOS table is required by the terrain-ray visibility path. A
// missing or malformed file is therefore returned as a provenance-rich content
// error; fixtures must provide an authored table [03 §3.2][PLAN_05 C2].
func CompileLOSTables(fs vfs.FSOps) (*LOSTables, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	data, err := fs.ReadFileLimit("gamedata/los.tdf", 1<<20)
	if err != nil {
		return nil, requiredContentError(fs, "gamedata/los.tdf", "retail LOS.TDF terrain-ray tables", err)
	}
	prov := Provenance{}
	if info, statErr := fs.Stat("gamedata/los.tdf"); statErr == nil {
		prov = ProvenanceFrom(info)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, requiredContentError(fs, "gamedata/los.tdf", "retail LOS.TDF terrain-ray tables", formats.WithTDFFile(err, "gamedata/los.tdf"))
	}
	var numTables int32
	if sec := doc.Root.Section("TABLEINFO"); sec != nil {
		numTables = sec.IntValue("numtables", 0) // typed accessor only [02 §4]
	}
	// Collect all TABLE<N> sections; discovery is via file-order sections,
	// then sort by numeric suffix for determinism (I1) [02 §5].
	type rawTbl struct {
		num     int
		section *formats.Section
	}
	var raws []rawTbl
	for _, sec := range doc.Root.Sections() {
		lower := CanonicalKey(sec.Name)
		if !strings.HasPrefix(lower, "table") {
			continue
		}
		suffix := trimTDFSemantic(sec.Name[len("table"):])
		// TABLEINFO is handled above, not a table.
		if CanonicalKey(suffix) == "info" {
			continue
		}
		n := int(formats.ParseTDFInteger(suffix))
		if n <= 0 {
			// Non-numeric TABLE suffix — skip; not part of LOS family.
			continue
		}
		raws = append(raws, rawTbl{num: n, section: sec})
	}
	sort.Slice(raws, func(i, j int) bool { return raws[i].num < raws[j].num })
	tables := make([]LOSTable, 0, len(raws))
	for _, r := range raws {
		t := LOSTable{TableNum: r.num}
		t.NumLines = r.section.IntValue("numlines", 0)
		// line1..lineN in numeric order for determinism (I1); authored numlines
		// may disagree with actual line count, so collect by scanning keys.
		// Gather lineN values by integer suffix.
		type lineEntry struct {
			idx   int
			value string
		}
		var lines []lineEntry
		for _, it := range r.section.Items {
			if it.Kind != formats.Assignment {
				continue
			}
			lk := CanonicalKey(it.Key)
			if !strings.HasPrefix(lk, "line") {
				continue
			}
			suf := trimTDFSemantic(it.Key[len("line"):])
			idx := int(formats.ParseTDFInteger(suf))
			if idx <= 0 {
				continue
			}
			lines = append(lines, lineEntry{idx: idx, value: it.Value})
		}
		sort.Slice(lines, func(i, j int) bool { return lines[i].idx < lines[j].idx })
		// Build Lines slice; if numlines is smaller than discovered lines,
		// preserve all discovered lines so hash is faithful to bytes; if
		// numlines larger, pad with nil lines — downstream clamps into table range anyway.
		t.Lines = make([][]int32, 0, len(lines))
		for _, le := range lines {
			t.Lines = append(t.Lines, parseLOSLine(le.value))
		}
		tables = append(tables, t)
	}
	lt := &LOSTables{
		DefinitionHeader: DefinitionHeader{
			CanonicalKey: CanonicalKey("los"),
			Provenance:   prov,
		},
		NumTables: numTables,
		Tables:    tables,
	}
	// Hash over canonical bytes including numtables and per-table lines (I1) [02 §5] C12.
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%d|", lt.CanonicalKey, lt.NumTables)
	for _, t := range lt.Tables {
		fmt.Fprintf(&b, "table%d:lines=%d|", t.TableNum, t.NumLines)
		for i, line := range t.Lines {
			fmt.Fprintf(&b, "line%d:", i+1)
			for j, v := range line {
				if j > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, "%d", v)
			}
			b.WriteByte('|')
		}
	}
	lt.Hash = HashDefinition([]byte(b.String()))
	return lt, nil
}

// CompileMeteor compiles gamedata/meteor.tdf into MeteorDefaults [02 §6] [PLAN_02 C15].
// The file is an ordinary TDF with a single [Default] section containing
// MeteorWeapon (string), MeteorRadius (integer), MeteorDensity/Duration/Interval
// (floatings) [research/formats/tdf.md METEOR.TDF]. Values retain source
// precision and are not re-parsed by combat [PLAN_02 C15] (I8).
//
// A missing file or [Default] section records no default without error. Other
// read failures remain compilation failures. A present but invalid block is
// retained for the selected-schema startup path to reject if it is needed;
// otherwise a complete authored schema is valid without it [02 §6][06 §6.5].
func CompileMeteor(fs vfs.FSOps) (*MeteorDefaults, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	data, err := fs.ReadFileLimit("gamedata/meteor.tdf", 1<<20)
	if err != nil {
		if !errors.Is(err, vfs.ErrNotFound) {
			return nil, requiredContentError(fs, "gamedata/meteor.tdf", "retail METEOR.TDF default table", err)
		}
		md := &MeteorDefaults{}
		md.CanonicalKey = CanonicalKey("meteor")
		md.Hash = HashDefinition([]byte(md.CanonicalKey + "|present=false|"))
		return md, nil
	}
	prov := Provenance{}
	if info, statErr := fs.Stat("gamedata/meteor.tdf"); statErr == nil {
		prov = ProvenanceFrom(info)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, formats.WithTDFContext(fs, err, "gamedata/meteor.tdf")
	}
	md := &MeteorDefaults{
		DefinitionHeader: DefinitionHeader{
			CanonicalKey: CanonicalKey("meteor"),
			Provenance:   prov,
		},
	}
	// Section may be [Default] or [DEFAULT]; case-insensitive [02 §3].
	sec := doc.Root.Section("default")
	if sec == nil {
		var b strings.Builder
		fmt.Fprintf(&b, "%s|present=false|", md.CanonicalKey)
		md.Hash = HashDefinition([]byte(b.String()))
		return md, nil
	}
	md.DefaultPresent = true
	var hasWeapon bool
	md.MeteorWeapon, hasWeapon = sec.StringValue("MeteorWeapon", "")
	// A missing weapon is the fatal branch that precedes all numeric reads when
	// this block is selected. Keep that semantic outcome through compilation;
	// an unused bad block remains admissible [02 §6][06 §6.5].
	if hasWeapon {
		md.MeteorRadius = sec.IntValue("MeteorRadius", 0)                // typed int accessor [02 §4]
		md.MeteorDensity = float32(sec.FloatValue("MeteorDensity", 0))   // source single store [06 §6.5]
		md.MeteorDuration = float32(sec.FloatValue("MeteorDuration", 0)) // source single store [06 §6.5]
		md.MeteorInterval = float32(sec.FloatValue("MeteorInterval", 0)) // source single store [06 §6.5]
		md.DefaultValid = md.MeteorRadius != 0 && md.MeteorDensity != 0 && md.MeteorDuration != 0 && md.MeteorInterval != 0
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s|present=%t|valid=%t|%s|%d|%08x|%08x|%08x|", md.CanonicalKey, md.DefaultPresent, md.DefaultValid, md.MeteorWeapon, md.MeteorRadius, math.Float32bits(md.MeteorDensity), math.Float32bits(md.MeteorDuration), math.Float32bits(md.MeteorInterval))
	md.Hash = HashDefinition([]byte(b.String()))
	return md, nil
}
