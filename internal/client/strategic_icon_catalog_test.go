package client

import (
	"bytes"
	"image/color"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func TestStrategicIconConstructorClassRequiresQualifiedAuthoredLevel(t *testing.T) {
	cat := iconTestCatalog()
	cases := []struct {
		name, category, subtype string
		products                bool
		unresolved              bool
	}{
		{"basic", "TANK CONSTR level1 LEVEL1", "basic", true, false},
		{"advanced", "TANK CONSTR LEVEL2", "advanced", true, false},
		{"conflict", "TANK CONSTR LEVEL1 LEVEL2", "", true, true},
		{"missing", "TANK CONSTR", "", true, true},
		{"bare", "TANK CONSTR LEVEL", "", true, true},
		{"other", "TANK CONSTR LEVEL20", "", true, true},
		{"mixed", "TANK CONSTR LEVEL2 LEVEL3", "", true, true},
		{"minelayer", "TANK MINELAYER LEVEL2", "", true, false},
		{"assist", "TANK CONSTR LEVEL2", "", false, false},
	}
	for i, tc := range cases {
		u := iconUnit(tc.name, tc.category, uint32(i+1))
		u.Builder = true
		// Localized names and misleading identity/cost metadata cannot promote a
		// basic constructor or supply missing authored level evidence (§18.3).
		u.Name, u.Description, u.UnitName = "Advanced Constructor", "Tech Level 2", "armacv"
		u.BuildCostMetal = 100000
		cat.Units[tc.name] = u
		if tc.products {
			cat.BuildMenus[tc.name] = &content.BuildMenuPage{Buttons: []string{"product"}}
		}
	}
	icons := NewStrategicIconCatalog(cat)
	for _, tc := range cases {
		d, _ := icons.Lookup(tc.name, 0)
		role := "construction"
		if !tc.products {
			role = "assist"
		}
		if d.Role != role || d.Subtype != tc.subtype || (len(d.Unresolved) > 0) != tc.unresolved {
			t.Errorf("%s: got role=%s subtype=%s unresolved=%v", tc.name, d.Role, d.Subtype, d.Unresolved)
		}
	}
	basic, _ := icons.Lookup("basic", 0)
	advanced, _ := icons.Lookup("advanced", 0)
	bg := color.RGBA{30, 42, 35, 255}
	team, halo := color.RGBA{75, 215, 240, 255}, color.RGBA{255, 240, 135, 255}
	if bytes.Equal(StrategicIconPreview(basic, 24, team, halo, bg, false).Pix, StrategicIconPreview(advanced, 24, team, halo, bg, false).Pix) {
		t.Fatal("basic and advanced constructor art must differ at the selected display size")
	}
}

func iconUnit(name, category string, id uint32) *content.UnitDef {
	return &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: name}, UnitName: name, UnitDefID: id, Category: category, BMCode: 1}
}
func iconTestCatalog(units ...*content.UnitDef) *content.Catalog {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, BuildMenus: map[string]*content.BuildMenuPage{}}
	for _, u := range units {
		cat.Units[u.CanonicalKey] = u
	}
	return cat
}

// These lock Enhanced presentation policies, not inferred retail icon behavior.
func TestStrategicIconFamilyUsesWholeTokensAndStructureIdentity(t *testing.T) {
	ground := iconUnit("ground", "NOTAIR NOTSUB TANK", 1)
	amph := iconUnit("amph", "KBOT", 2)
	amph.CanHover = true
	factory := iconUnit("factory", "PLANT", 3)
	factory.BMCode = 0
	factory.CanMove = true
	factory.Builder = true
	conflict := iconUnit("conflict", "TANK KBOT", 4)
	cat := iconTestCatalog(ground, amph, factory, conflict)
	cat.BuildMenus["factory"] = &content.BuildMenuPage{Buttons: []string{"ground"}}
	icons := NewStrategicIconCatalog(cat)
	for _, tc := range []struct{ name, family, role, sub string }{{"ground", "vehicle", "support", ""}, {"amph", "kbot", "support", ""}, {"factory", "structure", "factory", "vehicle"}, {"conflict", "generic", "support", ""}} {
		d, ok := icons.Lookup(tc.name, 0)
		if !ok || d.Family != tc.family || d.Role != tc.role || d.Subtype != tc.sub {
			t.Errorf("%s: got %+v", tc.name, d)
		}
	}
}
func TestStrategicIconCommanderAppearanceCoversEntireDescriptorArt(t *testing.T) {
	commander := iconUnit("commander", "COMMANDER", 1)
	commander.Commander = true
	commander.Builder = true
	decoy := iconUnit("decoy", "WEAPON", 2)
	decoy.Unknown = map[string]string{"tedclass": "Commander"}
	decoy.Weapon1Def = &content.WeaponDef{ID: 1, Dropped: true}
	decoy.BMCode = 0
	c := NewStrategicIconCatalog(iconTestCatalog(commander, decoy))
	a, _ := c.Lookup("commander", 1)
	b, _ := c.Lookup("decoy", 2)
	if !a.CommanderAppearance || !b.CommanderAppearance || a.Family != b.Family || a.Role != b.Role || a.Subtype != b.Subtype || a.Rect != b.Rect || a.Atlas != b.Atlas {
		t.Fatal("commander and decoy must share the complete appearance")
	}
}
func TestStrategicIconSpecificPurposePrecedesIncidentalResources(t *testing.T) {
	constructor := iconUnit("constructor", "TANK CONSTR ENERGY STORAGE", 1)
	constructor.Builder = true
	constructor.EnergyMake = 20
	constructor.EnergyStorage = 100
	solar := iconUnit("solar", "ENERGY", 2)
	solar.BMCode = 0
	solar.EnergyUse = -20
	storage := iconUnit("storage", "ENERGY STORAGE", 3)
	storage.BMCode = 0
	storage.EnergyStorage = 100
	radar := iconUnit("radar", "RAD CTRL_R", 4)
	radar.RadarDistance = 100
	radar.EnergyMake = 5
	feature := iconUnit("feature", "METAL", 5)
	feature.IsFeature = true
	feature.ExtractsMetal = 1
	resurrect := iconUnit("resurrect", "KBOT WEAPON", 6)
	resurrect.CanResurrect = true
	resurrect.Builder = true
	resurrect.Weapon1Def = &content.WeaponDef{ID: 0, BeamWeapon: true}
	cat := iconTestCatalog(constructor, solar, storage, radar, feature, resurrect)
	cat.BuildMenus["constructor"] = &content.BuildMenuPage{Buttons: []string{"solar"}}
	c := NewStrategicIconCatalog(cat)
	for _, tc := range []struct{ name, role string }{{"constructor", "construction"}, {"solar", "energy"}, {"storage", "storage"}, {"radar", "radar"}, {"feature", "fortification"}, {"resurrect", "resurrection"}} {
		d, _ := c.Lookup(tc.name, 0)
		if d.Role != tc.role {
			t.Errorf("%s role=%s want %s", tc.name, d.Role, tc.role)
		}
	}
}
func TestStrategicIconUsesEveryActiveWeaponSlot(t *testing.T) {
	u := iconUnit("mixed", "TANK WEAPON", 1)
	u.Weapon1Def = &content.WeaponDef{ID: 1, BeamWeapon: true}
	u.Weapon2Def = &content.WeaponDef{ID: 2, SelfProp: true}
	u.Weapon3Def = &content.WeaponDef{ID: 0, Interceptor: true}
	u.ExplodeAsDef = &content.WeaponDef{ID: 3, Dropped: true}
	cat := iconTestCatalog(u)
	c := NewStrategicIconCatalog(cat)
	d, _ := c.Lookup("mixed", 1)
	if d.Role != "combat" || d.Subtype != "mixed" {
		t.Fatalf("all active slots must contribute: %+v", d)
	}
	u.Weapon1Def = nil
	c = NewStrategicIconCatalog(cat)
	d, _ = c.Lookup("mixed", 1)
	if d.Subtype != "propelled" {
		t.Fatalf("inactive and explosion links must not contribute: %+v", d)
	}
}
func TestStrategicIconCatalogIdentityAndDisjointAtlas(t *testing.T) {
	a, b := iconUnit("a", "TANK", 1), iconUnit("b", "KBOT", 2)
	cat := iconTestCatalog(a, b)
	c := NewStrategicIconCatalog(cat)
	if _, ok := c.Lookup("unknown", 1); ok {
		t.Fatal("unknown name resolved via unrelated ID")
	}
	if _, ok := c.Lookup("a", 2); ok {
		t.Fatal("mismatched ID accepted")
	}
	if _, ok := c.Lookup("A", 1); !ok {
		t.Fatal("canonical name not accepted")
	}
	d, _ := c.Lookup("a", 1)
	e, _ := c.Lookup("b", 2)
	if d.Atlas != e.Atlas {
		t.Fatal("atlas is not shared")
	}
	for i := 0; i < len(d.Atlas.Pixels); i += 4 {
		sum := 0
		for j := 0; j < 4; j++ {
			sum += int(d.Atlas.Pixels[i+j])
		}
		if sum > 255 {
			t.Fatalf("coverage sum %d at pixel %d", sum, i/4)
		}
	}
	for y := int(d.Rect.Y); y < int(d.Rect.Y+d.Rect.H); y++ {
		for x := int(d.Rect.X); x < int(d.Rect.X+d.Rect.W); x++ {
			if x != int(d.Rect.X) && x != int(d.Rect.X+d.Rect.W)-1 && y != int(d.Rect.Y) && y != int(d.Rect.Y+d.Rect.H)-1 {
				continue
			}
			offset := (y*d.Atlas.Width + x) * 4
			for _, v := range d.Atlas.Pixels[offset : offset+4] {
				if v != 0 {
					t.Fatal("source tile gutter must be transparent")
				}
			}
		}
	}
	c2 := NewStrategicIconCatalog(cat)
	d2, _ := c2.Lookup("a", 1)
	if !reflect.DeepEqual(d.Atlas, d2.Atlas) {
		t.Fatal("atlas changed on identical catalog")
	}
}

func TestStrategicIconFactoryMixedProductsAndNameIndependence(t *testing.T) {
	plane := iconUnit("plane", "VTOL", 1)
	ship := iconUnit("ship", "SHIP", 2)
	factory := iconUnit("factory", "PLANT", 3)
	factory.BMCode = 0
	factory.Builder = true
	cat := iconTestCatalog(plane, ship, factory)
	cat.BuildMenus["factory"] = &content.BuildMenuPage{Buttons: []string{"plane", "ship"}}
	c := NewStrategicIconCatalog(cat)
	d, _ := c.Lookup("factory", 3)
	if d.Role != "factory" || d.Subtype != "" || len(d.Unresolved) == 0 {
		t.Fatal("mixed final products need generic factory art")
	}
	before, _ := c.Lookup("plane", 1)
	plane.Name = "Heavy anti-air artillery"
	plane.Description = "Fighter tech level 10"
	plane.UnitName = "corcom"
	c = NewStrategicIconCatalog(cat)
	after, _ := c.Lookup("plane", 1)
	if strategicArtKey(before) != strategicArtKey(after) || after.CommanderAppearance {
		t.Fatal("display names and unit-name patterns changed classification")
	}
	cat.BuildMenus["factory"].Buttons = []string{"plane", "missing"}
	c = NewStrategicIconCatalog(cat)
	d, _ = c.Lookup("factory", 3)
	if d.Subtype != "" {
		t.Fatal("missing final product fabricated a factory family")
	}
}

func TestStrategicIconDuplicateNamesKeepRecordDescriptors(t *testing.T) {
	first := iconUnit("same", "TANK", 1)
	second := iconUnit("same", "KBOT", 2)
	// The map-only fixture supplies two ordered records, as a compiled catalog's
	// retained table does for duplicate authored names [02 R-CAT-01 §5].
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"a": first, "b": second}}
	icons := NewStrategicIconCatalog(cat)
	a, ok := icons.Lookup("same", 1)
	if !ok {
		t.Fatal("first record missing")
	}
	b, ok := icons.Lookup("same", 2)
	if !ok || a.Family == b.Family {
		t.Fatal("later duplicate lost its own descriptor")
	}
	byName, ok := icons.Lookup("same", 0)
	if !ok || byName.Family != a.Family {
		t.Fatal("name lookup did not select first equal record")
	}
}
