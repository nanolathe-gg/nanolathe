// The side (battle interface) compiler.

package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// Rect is a corner rectangle stored verbatim as x1,y1,x2,y2 — not normalized,
// so x2 < x1 survives [02 §6 "SIDE and battle interface data"] C8.
type Rect struct {
	X1 int32 // x1 integer default 0 [02 §6]
	Y1 int32 // y1 integer default 0 [02 §6]
	X2 int32 // x2 integer default 0 [02 §6]
	Y2 int32 // y2 integer default 0 [02 §6]
}

// SideDef is a compiled side definition [02 §6 "SIDE and battle interface data"] C8.
// DefinitionHeader must be the first field per catalog convention [02 §5].
type SideDef struct {
	DefinitionHeader
	Index       int    // SIDE ordinal, 0 for SIDE0 etc [02 §6]
	Name        string // name string [02 §6]
	NamePrefix  string // nameprefix string [02 §6]
	Commander   string // commander string — unit name of that side's commander [02 §6]
	IntGAF      string // intgaf string default empty — side's interface GAF, binds PANELTOP/PANELSIDE/PANELBOT [02 §6]
	Font        string // font string — missing font is fatal [GAP T14] C8
	FontGUI     string // fontgui string [02 §6]
	EnergyColor int32  // energycolor integer default 0 — palette index for ENERGYBAR inner bar [02 §6]
	MetalColor  int32  // metalcolor integer default 0 — palette index for METALBAR [02 §6]
	BaseHeight  int32  // baseheight from [GENERAL] integer default 480 [02 §6]

	// Anchors holds the 30 mandatory interface anchors, each stored verbatim
	// as x1,y1,x2,y2 corners — not normalized [02 §6] C8.
	// Keys are the anchor names as authored (upper case in retail, canonicalized via CanonicalKey for lookup).
	// Every side has exactly 30 entries after successful compilation.
	Anchors map[string]Rect
}

// sideAnchorNames is the exact 30 mandatory anchors [02 §6] C8.
// Order is the retail file order observed in gamedata/sidedata.tdf and the
// research's direct + helper split; hash and validation use this order for
// determinism, but the map holds them by canonical key.
var sideAnchorNames = [30]string{
	"LOGO",
	"ENERGYBAR",
	"ENERGYNUM",
	"ENERGYMAX",
	"ENERGY0",
	"METALBAR",
	"METALNUM",
	"METALMAX",
	"METAL0",
	"TOTALUNITS",
	"TOTALTIME",
	"ENERGYPRODUCED",
	"ENERGYCONSUMED",
	"METALPRODUCED",
	"METALCONSUMED",
	"LOGO2",
	"UNITNAME",
	"DAMAGEBAR",
	"UNITMETALMAKE",
	"UNITMETALUSE",
	"UNITENERGYMAKE",
	"UNITENERGYUSE",
	"MISSIONTEXT",
	"UNITNAME2",
	"DAMAGEBAR2",
	"NAME",
	"DESCRIPTION",
	"RELOAD1",
	"RELOAD2",
	"RELOAD3",
}

// Anchor returns the rect for an anchor name (case-insensitive via CanonicalKey).
func (s *SideDef) Anchor(name string) (Rect, bool) {
	if s.Anchors == nil {
		return Rect{}, false
	}
	r, ok := s.Anchors[CanonicalKey(name)]
	return r, ok
}

// AnchorKeysSorted returns anchor names_sorted for deterministic iteration (I1).
func (s *SideDef) AnchorKeysSorted() []string {
	if s.Anchors == nil {
		return nil
	}
	keys := make([]string, 0, len(s.Anchors))
	for k := range s.Anchors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// compileSideSection compiles a single SIDE section into a SideDef.
// It uses only typed accessors from formats/tdf_typed.go [02 §4].
func compileSideSection(section *formats.Section, ordinal int, prov Provenance, baseHeight int32) (*SideDef, error) {
	// Per-side scalar keys [02 §6 "SIDE and battle interface data"].
	// Use typed accessors only from formats/tdf_typed.go.
	name, _ := section.StringValue("name", "")
	namePrefix, _ := section.StringValue("nameprefix", "")
	commander, _ := section.StringValue("commander", "")
	intgaf, _ := section.StringValue("intgaf", "")
	font, hasFont := section.StringValue("font", "")
	fontGUI, _ := section.StringValue("fontgui", "")
	energyColor := section.IntValue("energycolor", 0)
	metalColor := section.IntValue("metalcolor", 0)

	// [GAP T14] Missing side font is fatal — same modal/fatal diagnostic channel as missing anchor.
	// A missing side font is a data error, not a silent fallback.
	if !hasFont || trimTDFSemantic(font) == "" {
		return nil, fmt.Errorf("content: side SIDE%d: missing font is fatal [GAP T14]", ordinal)
	}

	anchors := make(map[string]Rect, len(sideAnchorNames))
	for _, anchorName := range sideAnchorNames {
		sub := section.Section(anchorName)
		if sub == nil {
			return nil, fmt.Errorf("content: side SIDE%d: missing anchor %s is fatal [02 §6] C8", ordinal, anchorName)
		}
		// Every anchor is a subsection holding four integer keys x1,y1,x2,y2 — corner rectangles, not origin-plus-size [02 §6].
		// All four default to 0, stored verbatim — not normalized, so x2 < x1 survives [02 §6] C8.
		x1 := sub.IntValue("x1", 0)
		y1 := sub.IntValue("y1", 0)
		x2 := sub.IntValue("x2", 0)
		y2 := sub.IntValue("y2", 0)
		anchors[CanonicalKey(anchorName)] = Rect{X1: x1, Y1: y1, X2: x2, Y2: y2}
	}

	sideKey := fmt.Sprintf("SIDE%d", ordinal)
	sd := &SideDef{
		DefinitionHeader: DefinitionHeader{
			CanonicalKey: CanonicalKey(sideKey),
			Provenance:   prov,
		},
		Index:       ordinal,
		Name:        name,
		NamePrefix:  namePrefix,
		Commander:   commander,
		IntGAF:      intgaf,
		Font:        font,
		FontGUI:     fontGUI,
		EnergyColor: energyColor,
		MetalColor:  metalColor,
		BaseHeight:  baseHeight,
		Anchors:     anchors,
	}
	// Hash over canonical bytes including defaults, independent of map iteration (I1) [02 §5] C12.
	// Never range the map directly — use fixed anchor order.
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%d|%s|%s|%s|%s|%s|%s|%d|%d|%d|", sd.CanonicalKey, sd.Index, sd.Name, sd.NamePrefix, sd.Commander, sd.IntGAF, sd.Font, sd.FontGUI, sd.EnergyColor, sd.MetalColor, sd.BaseHeight)
	for _, anchorName := range sideAnchorNames {
		ck := CanonicalKey(anchorName)
		r := anchors[ck]
		fmt.Fprintf(&b, "%s:%d,%d,%d,%d|", ck, r.X1, r.Y1, r.X2, r.Y2)
	}
	sd.Hash = HashDefinition([]byte(b.String()))
	return sd, nil
}

// CompileSides compiles sides from gamedata/sidedata.tdf [02 §6 "SIDE and battle interface data"] C8.
// Discovery is SIDE0..N stop at first gap, plus optional [GENERAL] baseheight integer default 480.
// Each side must have 30 mandatory anchors stored verbatim; missing font is fatal [GAP T14].
// It returns a slice indexed by SIDE ordinal — index = SIDE ordinal [PLAN 02 Public API].
func CompileSides(fs vfs.FSOps) ([]*SideDef, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	data, err := fs.ReadFileLimit("gamedata/sidedata.tdf", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("content: gamedata/sidedata.tdf: %w", err)
	}
	prov := Provenance{}
	if info, statErr := fs.Stat("gamedata/sidedata.tdf"); statErr == nil {
		prov = ProvenanceFrom(info)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, formats.WithTDFContext(fs, err, "gamedata/sidedata.tdf")
	}
	// Optional [GENERAL] baseheight integer default 480 [02 §6].
	baseHeight := int32(480)
	if general := doc.Root.Section("GENERAL"); general != nil {
		baseHeight = general.IntValue("baseheight", 480)
	}
	// SIDE0..N to first gap [02 §6] C8.
	var sides []*SideDef
	for i := 0; ; i++ {
		sideName := fmt.Sprintf("SIDE%d", i)
		section := doc.Root.Section(sideName)
		if section == nil {
			break
		}
		sd, err := compileSideSection(section, i, prov, baseHeight)
		if err != nil {
			return nil, err
		}
		sides = append(sides, sd)
	}
	return sides, nil
}
