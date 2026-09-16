package content

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestUnitOnOffableCompilesDefinitionFlag(t *testing.T) {
	def := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=ARMSTEALTH;
 OnOffable=1;
}
`).Root.Sections()[0], "units/armstealth.fbi", "", Provenance{})
	if !def.OnOffable {
		t.Fatal("authored OnOffable definition flag was not compiled")
	}
	if _, ok := def.Unknown["OnOffable"]; ok {
		t.Fatal("typed OnOffable definition flag retained as Unknown")
	}
}

func TestUnitPlacementProfileAuthoredAndDefaults(t *testing.T) {
	authored := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=ARMSOLAR;
 FootprintX=65535;
 FootprintZ=65538;
 MaxSlope=266;
 BadSlope=260;
 MaxWaterSlope=9;
 BadWaterSlope=300;
 MaxWaterDepth=70000;
 MinWaterDepth=-70001;
 Waterline=2;
}
`).Root.Sections()[0], "units/armsolar.fbi", "", Provenance{})
	// The authored values narrow before the unsigned-byte clamps: 266 -> 10,
	// 260 -> 4, 9 -> 9, and 300 -> 44. Clamp 1 then lowers MaxSlope to 9;
	// clamp 2 leaves BadSlope 4; clamp 3 lowers BadWaterSlope to 9 [02 §5].
	if authored.FootprintX != -1 || authored.FootprintZ != 2 {
		t.Fatalf("authored narrowed footprint = %d/%d, want -1/2", authored.FootprintX, authored.FootprintZ)
	}
	if authored.MaxSlope != 9 || authored.BadSlope != 4 || authored.MaxWaterSlope != 9 || authored.BadWaterSlope != 9 || authored.MaxWaterDepth != 4464 || authored.MinWaterDepth != -4465 || authored.Waterline != 2 {
		t.Fatalf("authored scratch profile = slopes %d/%d waterslopes %d/%d maxwater %d minwater %d waterline %d, want 9/4/9/9/4464/-4465/2", authored.MaxSlope, authored.BadSlope, authored.MaxWaterSlope, authored.BadWaterSlope, authored.MaxWaterDepth, authored.MinWaterDepth, authored.Waterline)
	}
	for _, key := range []string{"FootprintX", "FootprintZ", "MaxSlope", "BadSlope", "MaxWaterSlope", "BadWaterSlope", "MaxWaterDepth", "MinWaterDepth"} {
		for unknown := range authored.Unknown {
			if CanonicalKey(unknown) == CanonicalKey(key) {
				t.Fatalf("typed placement key %q retained as inert Unknown", key)
			}
		}
	}

	defaults := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=ARMLAB;
}
`).Root.Sections()[0], "units/armlab.fbi", "", Provenance{})
	if defaults.FootprintX != 0 || defaults.FootprintZ != 0 || defaults.MaxSlope != 255 || defaults.BadSlope != 127 || defaults.MaxWaterSlope != 255 || defaults.BadWaterSlope != 127 || defaults.MaxWaterDepth != 10000 || defaults.MinWaterDepth != -10000 || defaults.Waterline != 0 {
		t.Fatalf("default scratch profile = footprint %d/%d slopes %d/%d waterslopes %d/%d maxwater %d minwater %d waterline %d, want 0/0/255/127/255/127/10000/-10000/0", defaults.FootprintX, defaults.FootprintZ, defaults.MaxSlope, defaults.BadSlope, defaults.MaxWaterSlope, defaults.BadWaterSlope, defaults.MaxWaterDepth, defaults.MinWaterDepth, defaults.Waterline)
	}

	second := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=ARMLAB;
}
`).Root.Sections()[0], "units/armlab.fbi", "", Provenance{})
	if second.Hash != defaults.Hash {
		t.Fatalf("identical scratch profiles have unstable canonical hashes: %s != %s", defaults.Hash, second.Hash)
	}

	resolved := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=ARMTANK;
 MovementClass=TANK;
 FootprintX=7;
 FootprintZ=8;
 MaxWaterDepth=1;
 MinWaterDepth=2;
 MaxSlope=3;
 MaxWaterSlope=4;
}
`).Root.Sections()[0], "units/armtank.fbi", "", Provenance{})
	scratchHash := resolved.Hash
	movement := map[string]*MovementClass{CanonicalKey("TANK"): {
		FootprintX: 2, FootprintZ: 3,
		MaxWaterDepth: 12, MinWaterDepth: -10000,
		MaxSlope: 32, BadSlope: 16, MaxWaterSlope: 255, BadWaterSlope: 127,
	}}
	ApplyMovementFootprints(map[string]*UnitDef{resolved.CanonicalKey: resolved}, movement)
	if resolved.FootprintX != 2 || resolved.FootprintZ != 3 || resolved.MaxWaterDepth != 12 || resolved.MinWaterDepth != -10000 || resolved.MaxSlope != 32 || resolved.BadSlope != 16 || resolved.MaxWaterSlope != 255 || resolved.BadWaterSlope != 127 {
		t.Fatalf("resolved movement profile = footprint %d/%d maxwater %d minwater %d slopes %d/%d waterslopes %d/%d, want 2/3/12/-10000/32/16/255/127", resolved.FootprintX, resolved.FootprintZ, resolved.MaxWaterDepth, resolved.MinWaterDepth, resolved.MaxSlope, resolved.BadSlope, resolved.MaxWaterSlope, resolved.BadWaterSlope)
	}
	if resolved.Hash == scratchHash {
		t.Fatal("resolved movement profile did not change the canonical unit hash")
	}
	linkedHash := resolved.Hash
	ApplyMovementFootprints(map[string]*UnitDef{resolved.CanonicalKey: resolved}, movement)
	if resolved.Hash != linkedHash {
		t.Fatalf("reapplying the same resolved profile changed the canonical hash: %s != %s", resolved.Hash, linkedHash)
	}

	unresolved := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=MODBUILDING;
 MovementClass=MISSING;
 MinWaterDepth=11;
	BadSlope=12;
	BadWaterSlope=34;
}
`).Root.Sections()[0], "units/modbuilding.fbi", "", Provenance{})
	unresolvedHash := unresolved.Hash
	ApplyMovementFootprints(map[string]*UnitDef{unresolved.CanonicalKey: unresolved}, map[string]*MovementClass{})
	if unresolved.MinWaterDepth != 11 || unresolved.MaxWaterDepth != 10000 || unresolved.MaxSlope != 255 || unresolved.BadSlope != 12 || unresolved.MaxWaterSlope != 255 || unresolved.BadWaterSlope != 34 {
		t.Fatalf("unresolved class scratch profile = minwater %d maxwater %d slopes %d/%d waterslopes %d/%d, want 11/10000/255/12/255/34", unresolved.MinWaterDepth, unresolved.MaxWaterDepth, unresolved.MaxSlope, unresolved.BadSlope, unresolved.MaxWaterSlope, unresolved.BadWaterSlope)
	}
	if unresolved.Hash != unresolvedHash {
		t.Fatalf("unresolved class linking changed the scratch canonical hash: %s != %s", unresolved.Hash, unresolvedHash)
	}
}

func TestRetailClasslessExtractorScratchProfiles(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail assets: %v", err)
	}
	cat, err := Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}

	cormex := cat.Units[CanonicalKey("CORMEX")]
	if cormex == nil {
		t.Fatal("retail CORMEX definition missing")
	}
	if cormex.MovementClass != "" {
		t.Fatalf("CORMEX movement class = %q, want classless scratch profile", cormex.MovementClass)
	}
	// CORMEX authors MaxWaterDepth=0 and MaxSlope=10; the two absent fields
	// retain the startup template. This mixed record proves both authored
	// overrides and the classless land default that keeps the waterline gate
	// from rejecting every terrain sample [02 §5][04 §6.1 R-DOC04-A].
	if cormex.MaxWaterDepth != 0 || cormex.MinWaterDepth != -10000 || cormex.MaxSlope != 10 || cormex.BadSlope != 5 || cormex.MaxWaterSlope != 255 || cormex.BadWaterSlope != 127 {
		t.Fatalf("CORMEX scratch profile = maxwater %d minwater %d slopes %d/%d waterslopes %d/%d, want 0/-10000/10/5/255/127", cormex.MaxWaterDepth, cormex.MinWaterDepth, cormex.MaxSlope, cormex.BadSlope, cormex.MaxWaterSlope, cormex.BadWaterSlope)
	}

	coruwmex := cat.Units[CanonicalKey("CORUWMEX")]
	if coruwmex == nil {
		t.Fatal("retail CORUWMEX definition missing")
	}
	if coruwmex.MovementClass != "" {
		t.Fatalf("CORUWMEX movement class = %q, want classless scratch profile", coruwmex.MovementClass)
	}
	if coruwmex.MinWaterDepth != 10 {
		t.Fatalf("CORUWMEX minimum depth = %d, want authored signed-16 value 10", coruwmex.MinWaterDepth)
	}
}
