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
	TableNum int       // 1 for TABLE1 etc; TABLE d+1 fills zero-based slot d [03 R-COMP-02 §1]
	NumLines int32     // numlines integer default 0 [02 §6]
	Lines    [][]int32 // each lineN parsed as []int32 in authored order
}

// LOSTables is the compiled gamedata/los.tdf [02 §6] [PLAN_02 C15] [GAP T14].
// DefinitionHeader must be the first field per catalog convention [02 §5].
// Provenance is the winning gamedata/los.tdf provider.
type LOSTables struct {
	DefinitionHeader
	NumTables int32 // TABLEINFO numtables integer default 0, retains source precision [PLAN_02 C15]
	// Tables is the loader's zero-based table list: slot d holds the section
	// named TABLE d+1, with an empty record where that section is absent
	// [03 R-COMP-02 §1]. Sections outside the declared range are appended
	// after the slots in ascending order so nothing authored is lost (SC9);
	// no reader addresses them. The clamp bound is NumTables, never len (I1).
	Tables []LOSTable
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

// compileLOSTable compiles one [TABLE<n>] section [02 §6] [fmt tdf].
//
// line1..lineN are collected in numeric order for determinism (I1); an authored
// numlines may disagree with the discovered line count, and every discovered
// line is preserved so the catalog hash stays faithful to the authored bytes.
func compileLOSTable(sec *formats.Section, num int) LOSTable {
	t := LOSTable{TableNum: num}
	t.NumLines = sec.IntValue("numlines", 0)
	type lineEntry struct {
		idx   int
		value string
	}
	var lines []lineEntry
	for _, it := range sec.Items {
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
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].idx < lines[j].idx })
	t.Lines = make([][]int32, 0, len(lines))
	for _, le := range lines {
		t.Lines = append(t.Lines, parseLOSLine(le.value))
	}
	return t
}

// CompileLOSTables compiles gamedata/los.tdf into LOSTables [02 §6] [PLAN_02 C15].
// The file is an ordinary TDF with [TABLEINFO] { numtables=9 } and [TABLE1]...
// sections each with numlines and line1..lineN [research/formats/tdf.md LOS.TDF].
//
// The compiled list reproduces the loader's storage: it sizes the list to the
// declared numtables and fills zero-based slot d from the section it names by
// building "TABLE" followed by d+1, so slot d holds TABLE d+1 and a name the
// loader never builds is never read [03 R-COMP-02 §1]. File order carries no
// meaning, and a numbering gap leaves that slot's empty line list rather than
// shifting later tables down. The reference install declares nine and ships
// twelve, so its TABLE10..TABLE12 are unreachable residue; they are kept after
// the slots, in ascending order, so nothing authored is lost (SC9). Phase 5
// consumes this compiled form and does not re-parse (I8) [03 §3.2] C2.
//
// The authored LOS table is required by the terrain-ray visibility path. A
// missing or malformed file is therefore returned as a provenance-rich content
// error; fixtures must provide an authored table [03 §3.2][PLAN_05 C2].
//
// Nothing here assumes how many tables, lines or points an authored file may
// carry. The retail loader's storage for this file is three nested dynamic
// arrays: it resizes the table list to the declared numtables, a table's line
// list to its declared numlines, and a line's point list to the pairs the line
// spells [03 R-COMP-02 §1]. A content set that authors ninety tables is
// therefore read the same way a nine-table one is, and the only host bound is
// the read cap below.
//
// The one host bound is limits.LOSBytes, the content profile's battle-table
// read cap; RetailLimits() is the retail baseline. Only the cap moves with the
// profile — the compiled shape, the slot fill and the hash do not.
func CompileLOSTables(fs vfs.FSOps, limits Limits) (*LOSTables, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	limits, err := limits.normalize()
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFileLimit("gamedata/los.tdf", limits.LOSBytes)
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
	// Discover every TABLE<N> section for the residue pass below; the slots
	// themselves are filled by name, the way the loader builds them.
	type rawTbl struct {
		num     int
		section *formats.Section
	}
	var raws []rawTbl
	highest := 0
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
		if n > highest {
			highest = n
		}
		raws = append(raws, rawTbl{num: n, section: sec})
	}
	// Stable so two sections carrying the same number keep file order (I1).
	sort.SliceStable(raws, func(i, j int) bool { return raws[i].num < raws[j].num })

	slots := int(numTables)
	if slots < 0 {
		slots = 0
	}
	if slots > highest {
		// A declared slot no section fills reads as the empty line list whether
		// or not a record is materialized, so the list stops at the highest
		// authored number: a mistyped numtables cannot force an unbounded
		// allocation here. The clamp bound stays the declared NumTables.
		slots = highest
	}
	tables := make([]LOSTable, 0, slots+len(raws))
	filled := make(map[*formats.Section]bool, slots) // build-time only, never ranged (I1)
	for d := 0; d < slots; d++ {
		// The loader builds the section name from the slot: slot d asks for
		// TABLE d+1 [03 R-COMP-02 §1]. An absent section leaves the slot's
		// empty line list.
		t := LOSTable{TableNum: d + 1}
		if sec := doc.Root.Section(fmt.Sprintf("TABLE%d", d+1)); sec != nil {
			t = compileLOSTable(sec, d+1)
			filled[sec] = true
		}
		tables = append(tables, t)
	}
	// Sections the loader never names — numbers past the declared count, and
	// any duplicate the name lookup skipped — are retained after the slots so
	// nothing authored is lost (SC9). No reader addresses them.
	for _, r := range raws {
		if filled[r.section] {
			continue
		}
		tables = append(tables, compileLOSTable(r.section, r.num))
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
//
// limits.LOSBytes is the read cap, the same one the LOS table is read under: a
// content set that raises one raises both.
func CompileMeteor(fs vfs.FSOps, limits Limits) (*MeteorDefaults, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	limits, err := limits.normalize()
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFileLimit("gamedata/meteor.tdf", limits.LOSBytes)
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
