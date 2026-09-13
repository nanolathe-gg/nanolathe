package client

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// StrategicIconRevision versions our Enhanced presentation vocabulary. These
// mappings describe authored inputs, not recovered retail icon behavior
// (DESIGN_GPU_RENDERER §18). They never alter a content definition or its hash.
const StrategicIconRevision = 5

// StrategicIconDescriptor is immutable after catalog construction. Evidence and
// Unresolved are audit metadata, never additional enemy hover information.
type StrategicIconDescriptor struct {
	Atlas                              *drawlist.MarkerAtlas `json:"-"`
	Rect                               drawlist.Rect
	Family, Role, Subtype              string
	CommanderAppearance                bool
	Level                              int
	Evidence, Capabilities, Unresolved []string
}

// StrategicIconAuditEntry retains winning-provider provenance and identity.
// Names/descriptions appear only in the review sheet, never in classification.
type StrategicIconAuditEntry struct {
	Definition, Name, Description, Side, Hash, LogicalPath, Provider string
	DefinitionID                                                     uint32
	MountOrder                                                       int
	Category, TEDClass                                               string
	Descriptor                                                       StrategicIconDescriptor
}

type StrategicIconCatalog struct {
	byName   map[string]StrategicIconDescriptor
	byID     map[uint16]string
	byRecord map[uint16]StrategicIconDescriptor
	entries  []StrategicIconAuditEntry
	fallback StrategicIconDescriptor
}

// NewStrategicIconCatalog consumes the final linked catalog, including download
// build menus [02 R-CAT-01 §8]. All geometry is shared and compiled once.
func NewStrategicIconCatalog(cat *content.Catalog) *StrategicIconCatalog {
	c := &StrategicIconCatalog{byName: make(map[string]StrategicIconDescriptor), byID: make(map[uint16]string), byRecord: make(map[uint16]StrategicIconDescriptor)}
	c.fallback = StrategicIconDescriptor{Family: "generic", Role: "support", Unresolved: []string{"definition absent from this catalog"}}
	if cat != nil {
		levels := strategicIconBuildLevels(cat)
		for _, u := range cat.UnitRecords() {
			if u == nil {
				continue
			}
			key := u.CanonicalKey
			if key == "" {
				key = content.CanonicalKey(u.UnitName)
			}
			d := classifyStrategicIcon(cat, u)
			if !d.CommanderAppearance {
				graph := levels[key]
				// Build menus resolve names to one record [02 R-CAT-01 §5].
				// A retained same-name record cannot inherit that record's route.
				if named, ok := cat.Unit(key); !ok || named != u {
					graph = strategicIconLevel{}
				}
				level := strategicResolveIconLevel(u.Category, graph)
				d.Level = level.Level
				d.Evidence = append(d.Evidence, level.Evidence)
				if level.Unresolved != "" {
					d.Unresolved = append(d.Unresolved, level.Unresolved)
				}
			}
			if u.UnitDefID > 0 && u.UnitDefID <= 65535 {
				c.byID[uint16(u.UnitDefID)] = key
			}
			c.entries = append(c.entries, StrategicIconAuditEntry{Definition: key, Name: u.Name, Description: u.Description, Side: u.Side, Hash: u.Hash, LogicalPath: u.Provenance.LogicalPath, Provider: u.Provenance.ProviderID, MountOrder: u.Provenance.MountOrder, DefinitionID: u.UnitDefID, Category: u.Category, TEDClass: strategicEditorClass(u), Descriptor: d})
		}
	}
	art := []StrategicIconDescriptor{c.fallback}
	for _, e := range c.entries {
		art = append(art, e.Descriptor)
	}
	atlas, rects := makeStrategicIconAtlas(art)
	c.fallback.Atlas, c.fallback.Rect = atlas, rects[strategicArtKey(c.fallback)]
	for i := range c.entries {
		d := c.entries[i].Descriptor
		d.Atlas, d.Rect = atlas, rects[strategicArtKey(d)]
		c.entries[i].Descriptor = d
		entry := c.entries[i]
		if _, exists := c.byName[entry.Definition]; !exists {
			c.byName[entry.Definition] = d
		}
		if entry.DefinitionID > 0 && entry.DefinitionID <= 65535 {
			c.byRecord[uint16(entry.DefinitionID)] = d
		}
	}
	return c
}

// Lookup requires a supplied name and nonzero ID to agree. An ID never rescues
// an unrecognized name: stale/cross-catalog identities must remain generic.
func (c *StrategicIconCatalog) Lookup(defName string, defID uint16) (StrategicIconDescriptor, bool) {
	if c == nil {
		return StrategicIconDescriptor{}, false
	}
	key := content.CanonicalKey(defName)
	if key == "" {
		key = c.byID[defID]
	}
	d, ok := c.byName[key]
	if defID != 0 {
		d, ok = c.byRecord[defID]
		ok = ok && c.byID[defID] == key
	}
	if !ok {
		return c.fallback, false
	}
	return d, true
}

// Audit returns detached evidence lists; its atlas remains shared immutable art.
func (c *StrategicIconCatalog) Audit() []StrategicIconAuditEntry {
	if c == nil {
		return nil
	}
	out := append([]StrategicIconAuditEntry(nil), c.entries...)
	for i := range out {
		d := &out[i].Descriptor
		d.Evidence = append([]string(nil), d.Evidence...)
		d.Capabilities = append([]string(nil), d.Capabilities...)
		d.Unresolved = append([]string(nil), d.Unresolved...)
	}
	return out
}

func strategicTokens(s string) map[string]bool {
	out := make(map[string]bool)
	for _, t := range strings.Fields(s) {
		out[strings.ToUpper(t)] = true
	}
	return out
}
func strategicEditorClass(u *content.UnitDef) string {
	// Unknown retains authored spelling [fmt fbi]. Sorting also makes a synthetic
	// case-colliding map deterministic, without parsing localized descriptions.
	keys := make([]string, 0, len(u.Unknown))
	for k := range u.Unknown {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.EqualFold(k, "TEDClass") {
			return strings.ToUpper(strings.TrimSpace(u.Unknown[k]))
		}
	}
	return ""
}
func strategicFamily(u *content.UnitDef) (string, string, string) {
	if u.BMCode == 0 {
		return "structure", "family: BMCode=0 [SC21]", ""
	}
	tokens := strategicTokens(u.Category)
	families := []struct{ token, family string }{{"KBOT", "kbot"}, {"TANK", "vehicle"}, {"VTOL", "aircraft"}, {"HOVER", "hovercraft"}, {"SHIP", "ship"}, {"UNDERWATER", "submarine"}}
	found := ""
	evidence := ""
	for _, f := range families {
		if tokens[f.token] {
			if found != "" {
				return "generic", "family: conflicting Category tokens", "physical family has conflicting authored tokens"
			}
			found = f.family
			evidence = "family: Category token " + f.token
		}
	}
	ted := strategicEditorClass(u)
	editor := ""
	for _, f := range families {
		if ted == f.token {
			editor = f.family
		}
	}
	if found != "" {
		// Authored editor TANK includes hovercraft, and SHIP includes submarines.
		compatible := editor == "" || editor == found || (found == "hovercraft" && editor == "vehicle") || (found == "submarine" && editor == "ship")
		if compatible {
			return found, evidence, ""
		}
		return "generic", evidence + " conflicts with TEDClass=" + ted, "physical family Category and TEDClass disagree"
	}
	if editor != "" {
		return editor, "family: authored TEDClass=" + ted, ""
	}
	// TODO(question): which physical family is intended when explicit family
	// metadata is absent/conflicting? Review original model/build art and record
	// a content-bound mapping; use the generic contour meanwhile (§18.3).
	return "generic", "family: no explicit family metadata", "physical family lacks explicit authored evidence"
}

func classifyStrategicIcon(cat *content.Catalog, u *content.UnitDef) StrategicIconDescriptor {
	d := StrategicIconDescriptor{Role: "support"}
	d.Family = "generic"
	family, evidence, unknown := strategicFamily(u)
	d.Family = family
	d.Evidence = append(d.Evidence, evidence)
	if unknown != "" {
		d.Unresolved = append(d.Unresolved, unknown)
	}
	t := strategicTokens(u.Category)
	d.Evidence = append(d.Evidence,
		fmt.Sprintf("inputs: BMCode=%d Builder=%t CanMove=%t CanFly=%t CanHover=%t IsFeature=%t", u.BMCode, u.Builder, u.CanMove, u.CanFly, u.CanHover, u.IsFeature),
		fmt.Sprintf("economy: EnergyMake=%g EnergyUse=%g WindGenerator=%g TidalGenerator=%g ExtractsMetal=%g MakesMetal=%d MetalMake=%g EnergyStorage=%g MetalStorage=%g", u.EnergyMake, u.EnergyUse, u.WindGenerator, u.TidalGenerator, u.ExtractsMetal, u.MakesMetal, u.MetalMake, u.EnergyStorage, u.MetalStorage),
		fmt.Sprintf("sensors: RadarDistance=%d SonarDistance=%d RadarDistanceJam=%d SonarDistanceJam=%d; transport: CanLoad=%t TransportCapacity=%d", u.RadarDistance, u.SonarDistance, u.RadarDistanceJam, u.SonarDistanceJam, u.CanLoad, u.TransportCapacity))
	cap := func(name string, on bool) {
		if on {
			d.Capabilities = append(d.Capabilities, name)
		}
	}
	cap("builder", u.Builder)
	cap("flight", u.CanFly)
	cap("hover", u.CanHover)
	cap("amphibious", u.Amphibious)
	cap("capture", u.CanCapture)
	cap("teleporter", u.Teleporter)
	cap("targeting upgrade", u.IsTargetingUpgrade)
	cap("feature conversion", u.IsFeature)
	cap("kamikaze", u.Kamikaze)
	cap("resurrection", u.CanResurrect)
	cap("reclamation", u.CanReclamate)
	cap("airbase", u.IsAirBase)
	cap("transport", u.CanLoad && u.TransportCapacity > 0)
	cap("radar", u.RadarDistance > 0)
	cap("sonar", u.SonarDistance > 0)
	cap("radar jammer", u.RadarDistanceJam > 0)
	cap("sonar jammer", u.SonarDistanceJam > 0)
	cap("energy production", u.EnergyMake > 0 || u.EnergyUse < 0 || u.WindGenerator > 0 || u.TidalGenerator > 0)
	cap("metal extraction", u.ExtractsMetal > 0)
	cap("metal conversion", u.MakesMetal > 0)
	cap("metal production", u.MetalMake > 0)
	cap("energy storage", u.EnergyStorage > 0)
	cap("metal storage", u.MetalStorage > 0)
	primaryWeapon := ""
	for slot, w := range []*content.WeaponDef{u.Weapon1Def, u.Weapon2Def, u.Weapon3Def} {
		if content.IsWeaponInactive(w) {
			continue
		}
		kind := strategicWeaponKind(w)
		if primaryWeapon == "" {
			primaryWeapon = kind
			d.Evidence = append(d.Evidence, fmt.Sprintf("primary weapon: first active authored slot, weapon%d", slot+1))
		}
		flags := []string{}
		for _, f := range []struct {
			name string
			set  bool
		}{{"Interceptor", w.Interceptor}, {"Dropped", w.Dropped}, {"WaterWeapon", w.WaterWeapon}, {"Paralyzer", w.Paralyzer}, {"BeamWeapon", w.BeamWeapon}, {"Ballistic", w.Ballistic}, {"SelfProp", w.SelfProp}, {"Stockpile", w.Stockpile}, {"ToAirWeapon", w.ToAirWeapon}} {
			if f.set {
				flags = append(flags, f.name)
				cap("weapon: "+f.name, true)
			}
		}
		d.Evidence = append(d.Evidence, fmt.Sprintf("weapon%d: active %s [%s]; glyph=%s", slot+1, w.CanonicalKey, strings.Join(flags, ", "), kind))
	}
	active := primaryWeapon != ""
	menu := cat.BuildMenus[content.CanonicalKey(u.CanonicalKey)]
	products := 0
	if menu != nil {
		products = len(menu.Buttons)
	}
	set := func(role, subtype, why string) {
		d.Role, d.Subtype = role, subtype
		d.Evidence = append(d.Evidence, "role: "+why)
	}
	switch {
	case u.Commander || t["COMMANDER"] || strategicEditorClass(u) == "COMMANDER":
		// Entire appearance is shared, even if a commander-looking mod has unusual
		// family flags. Never expose true-command capability through shape or size.
		d.Family, d.Role, d.Subtype = "commander", "commander", ""
		d.CommanderAppearance = true
		d.Unresolved = nil
		d.Evidence = append(d.Evidence, "appearance: Commander flag or authored COMMANDER metadata; shared disguise policy §18.4")
	case u.IsFeature:
		set("fortification", "", "IsFeature; converted feature remains in feature layer")
	case u.CanResurrect:
		set("resurrection", "", "CanResurrect")
	case u.Builder && u.BMCode == 0 && products > 0:
		set("factory", "", "BMCode=0; Builder; nonempty resolved final build menu")
	case u.IsAirBase:
		set("airbase", "", "IsAirBase; aircraft repair/support")
	case u.Builder && products > 0:
		set("construction", "", "Builder with final build products")
	case u.Builder:
		set("assist", "", "Builder with empty product menu; general build/repair support")
	case u.CanLoad && u.TransportCapacity > 0:
		set("transport", "", "CanLoad and positive TransportCapacity")
	case t["MINE"] && u.Kamikaze:
		set("mine", "", "MINE category and Kamikaze")
	case u.Kamikaze:
		set("kamikaze", "", "Kamikaze")
	case u.Teleporter:
		set("teleporter", "", "Teleporter")
	case u.IsTargetingUpgrade:
		set("targeting", "", "IsTargetingUpgrade")
	case !active && t["STORAGE"] && t["ENERGY"] && u.EnergyStorage > 0:
		set("storage", "energy", "STORAGE and ENERGY category; EnergyStorage>0")
	case !active && t["STORAGE"] && t["METAL"] && u.MetalStorage > 0:
		set("storage", "metal", "STORAGE and METAL category; MetalStorage>0")
	case !active && (t["METAL"] || t["EXTRACTOR"]) && u.ExtractsMetal > 0:
		set("extractor", "", "METAL/EXTRACTOR category and ExtractsMetal>0")
	case !active && t["METAL"] && u.MakesMetal > 0:
		set("converter", "", "METAL category and MakesMetal>0")
	case !active && t["ENERGY"] && (u.EnergyMake > 0 || u.EnergyUse < 0 || u.WindGenerator > 0 || u.TidalGenerator > 0):
		set("energy", "", "ENERGY category; positive EnergyMake/wind/tide or negative EnergyUse")
	case !active && (t["JAM"] || t["CTRL_R"]) && (u.RadarDistanceJam > 0 || u.SonarDistanceJam > 0):
		set("jammer", "", "JAM/CTRL_R category and positive sensor jamming distance")
	case !active && (t["SONAR"] || t["CTRL_R"]) && u.SonarDistance > 0:
		set("sonar", "", "SONAR/CTRL_R category and SonarDistance>0")
	case !active && (t["RAD"] || t["CTRL_R"]) && u.RadarDistance > 0:
		set("radar", "", "RAD/CTRL_R category and RadarDistance>0")
	case !active && t["SPY"]:
		set("spy", "", "authored SPY category")
	case active:
		set("combat", primaryWeapon, "first active resolved weapon slot; flag-derived primary weapon glyph")
		// TODO(question): AA/fighter/artillery/scout/heavy roles have no complete
		// reviewed mapping (§18.3). Keep weapon capabilities and a generic combat
		// purpose; authored descriptions and targeting review can settle the gap.
		d.Unresolved = append(d.Unresolved, "combat purpose (AA/fighter/artillery/heavy) not inferred from weapon flags")
	default:
		// TODO(question): what primary purpose is supported for this unqualified
		// definition? Review its authored build art and capabilities; a content-bound
		// presentation mapping can replace this generic symbol (§18.3).
		d.Evidence = append(d.Evidence, "role: conservative support fallback")
		d.Unresolved = append(d.Unresolved, "primary role lacks qualified authored evidence")
	}
	sort.Strings(d.Capabilities)
	d.Capabilities = compactStrategicStrings(d.Capabilities)
	return d
}

func compactStrategicStrings(a []string) []string {
	out := a[:0]
	for _, s := range a {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}
func strategicWeaponKind(w *content.WeaponDef) string {
	// These are presentation glyph choices for literal flags [02 "Weapon
	// record"], not assertions of target restrictions or balance roles.
	switch {
	case w.Interceptor:
		return "interceptor"
	case w.Paralyzer:
		return "paralyzer"
	case w.Dropped:
		return "dropped"
	case w.WaterWeapon:
		return "water"
	case w.BeamWeapon:
		return "beam"
	case w.Ballistic:
		return "ballistic"
	case w.SelfProp:
		return "propelled"
	default:
		return "projectile"
	}
}
