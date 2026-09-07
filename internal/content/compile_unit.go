// The unit (FBI) compiler.

package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/vfs"
)

// MobilityDomain is the placement/routing domain selected from authored unit
// capability fields.  It is deliberately narrower than CanMove: factory
// admission needs to distinguish an aircraft from a ground mover even when
// an aircraft has no movement-class record [04 §6.4][02 "Unit record"].
type MobilityDomain uint8

const (
	MobilityUnknown MobilityDomain = iota
	MobilityFixed
	MobilityGround
	MobilityAircraft
)

// String returns the stable diagnostic spelling for a mobility domain.
func (d MobilityDomain) String() string {
	switch d {
	case MobilityFixed:
		return "fixed"
	case MobilityGround:
		return "ground"
	case MobilityAircraft:
		return "aircraft"
	default:
		return "unknown"
	}
}

// UnitDef is a compiled unit definition [02 "Unit record (.fbi)"].
// DefinitionHeader must be the first field per catalog convention [02 §5].
type UnitDef struct {
	DefinitionHeader
	// UnitDefID is the stable 1-based catalog ID (zero is the null sentinel)
	// used by category membership masks [02 §5] [R-P0-03].
	UnitDefID uint32
	// Identity and presentation [02 "Unit record (.fbi)"].
	UnitName      string // unitname string 32 empty — canonical catalog name [02 "Unit record"]
	Name          string // language-prefixed name trial <Language>name then name [02 §3] C7, 32 default empty
	Description   string // language-prefixed description [02 §3] C7, 64 default empty
	Side          string // side string 30 empty [02 "Unit record"] — faction tag
	ObjectName    string // objectname string 32 empty — 3DO model name [02 "Unit record"]
	Category      string // category string 100 empty — category token list [02 "Unit record"]
	SoundCategory string // soundcategory string 100 empty — resolves to sound-category index [02 "Unit record"]
	Corpse        string // corpse string 100 empty — resolves to feature identity [02 "Unit record"]
	MovementClass string // movementclass string 100 empty — resolves to movement class [02 "Unit record"]
	// MobilityDomain is derived after capability fields are read. A class-less
	// aircraft is valid for factory admission; a class-less non-air mobile is
	// unresolved and remains a permanent content error [04 §6.4].
	MobilityDomain        MobilityDomain
	Weapon1               string // weapon1 string 128 empty — resolves to weapon identity [02 "Unit record"]
	Weapon2               string // weapon2 string 128 empty [02 "Unit record"]
	Weapon3               string // weapon3 string 128 empty [02 "Unit record"]
	ExplodeAs             string // explodeas string 128 empty — resolves to weapon identity [02 "Unit record"]
	SelfDestructAs        string // selfdestructas string 128 empty [02 "Unit record"]
	YardMap               string // YardMap string 1024 empty — occupancy map text [02 "Unit record"]
	DefaultMissionType    string // defaultmissiontype string 100 empty [02 "Unit record"]
	BadTargetCategoryWPRI string // wpri_badTargetCategory string 100 default none [02 "Unit record"]
	BadTargetCategoryWSEC string // wsec_badTargetCategory string 100 default none [02 "Unit record"]
	BadTargetCategoryWSPE string // wspe_badTargetCategory string 100 default none [02 "Unit record"]
	NoChaseCategory       string // noChaseCategory string 100 default none [02 "Unit record"]
	AIWeight              string // ai_weight string 64, default empty; parsed by the AI profile grammar [02 "Unit record"][08 "Established AI-facing data and rooted planner"]
	// ai_limit remains in Unknown because research found no semantic reader for
	// this definition field. Embedded limit directives in ai_weight are instead
	// registered by the AI profile application boundary [08 "Established
	// AI-facing data and rooted planner"].
	// Linked category masks. These are value sets indexed by candidate unit
	// definition ID; downstream code need not re-tokenize authored strings
	// [R-P0-03; 06 §3.1].
	BadTargetCategoryWPRIMask CategoryMask
	BadTargetCategoryWSECMask CategoryMask
	BadTargetCategoryWSPEMask CategoryMask
	NoChaseCategoryMask       CategoryMask
	// UnitMask is this definition's own single-ID membership value. Consumers
	// can intersect it with linked target masks without re-tokenizing strings.
	UnitMask CategoryMask

	// Economy [02 "Unit record"].
	BuildCostEnergy int32   // buildcostenergy integer default 0 [02 "Unit record"]
	BuildCostMetal  int32   // buildcostmetal integer default 0 [02 "Unit record"]
	EnergyMake      float64 // energymake floating default 0 [02 "Unit record"]
	EnergyUse       float64 // energyuse floating default 0 [02 "Unit record"]
	MetalMake       float64 // metalmake floating default 0 [02 "Unit record"]
	ExtractsMetal   float64 // extractsmetal floating default 0 [02 "Unit record"]
	WindGenerator   float64 // windgenerator floating default 0 [02 "Unit record"]
	TidalGenerator  float64 // tidalgenerator floating default 0 [02 "Unit record"]
	EnergyStorage   float64 // energystorage floating default 0 [02 "Unit record"]
	MetalStorage    float64 // metalstorage floating default 0 [02 "Unit record"]
	MakesMetal      int32   // makesmetal integer default 0 [02 "Unit record"]
	BuildTime       int32   // buildtime integer default 0 [02 "Unit record"]
	WorkerTime      int32   // workertime integer default 0 [02 "Unit record"]
	HealTime        int32   // healtime integer default 0 [02 "Unit record"]
	CloakCost       int32   // cloakcost integer stored as floating default 0 [02 "Unit record"]
	CloakCostMoving int32   // cloakcostmoving integer stored as floating default cloakcost just read [02 "Unit record"]

	// Movement and geometry [02 "Unit record"].
	MaxVelocity         int32 // maxvelocity fixed default 0 [02 "Unit record"]
	BrakeRate           int32 // brakerate fixed default 0 [02 "Unit record"]
	Acceleration        int32 // acceleration fixed default 0 [02 "Unit record"]
	BankScale           int32 // bankscale fixed default 65536 (1.0) [02 "Unit record"]
	PitchScale          int32 // pitchscale fixed default 0 [02 "Unit record"]
	DamageModifier      int32 // damagemodifier fixed default 65536 (1.0) [02 "Unit record"]
	MoveRate1           int32 // moverate1 fixed default twice maxvelocity just read [02 "Unit record"]
	MoveRate2           int32 // moverate2 fixed default twice maxvelocity just read [02 "Unit record"]
	TurnRate            int32 // turnrate integer default 0 [02 "Unit record"]
	Waterline           int32 // waterline integer default 0 [02 "Unit record"]
	MinWaterDepth       int32 // scratch-profile signed-16 minimum depth; template −10000 [02 §5 "Movement class record"][04 §6.1 R-DOC04-A]
	MaxWaterDepth       int32 // scratch-profile signed-16 maximum depth; template 10000 [02 §5 "Movement class record"][04 §6.1 R-DOC04-A]
	MaxSlope            int32 // scratch-profile byte slope after the ordered clamps [02 §5 "Movement class record"]
	BadSlope            int32 // scratch-profile byte soft land-slope threshold [02 §5 "Movement class record"]
	MaxWaterSlope       int32 // scratch-profile byte water slope copied with MaxSlope [02 §5 "Movement class record"]
	BadWaterSlope       int32 // scratch-profile byte soft water-slope threshold [02 §5 "Movement class record"]
	CruiseAlt           int32 // cruisealt integer default 0 [02 "Unit record"]
	TransportSize       int32 // transportsize integer default 0 [02 "Unit record"]
	TransportCapacity   int32 // transportcapacity integer default 0 [02 "Unit record"]
	BuildAngle          int32 // buildangle integer default 0 [02 "Unit record"]
	BuildDistance       int32 // builddistance integer default 0 [02 "Unit record"]
	SortBias            int32 // sortbias integer default 0 [02 "Unit record"]
	ManeuverLeashLength int32 // maneuverleashlength integer default 0 [02 "Unit record"]
	AttackRunLength     int32 // attackrunlength integer default 0 [02 "Unit record"]
	KamikazeDistance    int32 // kamikazedistance integer default 0 [02 "Unit record"]
	FootprintX          int32 // footprintx integer default 0 — occupancy size in cells [fmt fbi]
	FootprintZ          int32 // footprintz integer default 0 [fmt fbi]

	// Combat and sensors [02 "Unit record"].
	MaxDamage        int32 // maxdamage integer default 0 [02 "Unit record"]
	SightDistance    int32 // sightdistance integer default 0 [02 "Unit record"]
	RadarDistance    int32 // radardistance integer default 0 [02 "Unit record"]
	SonarDistance    int32 // sonardistance integer default 0 [02 "Unit record"]
	RadarDistanceJam int32 // radardistancejam integer default 0 [02 "Unit record"]
	SonarDistanceJam int32 // sonardistancejam integer default 0 [02 "Unit record"]
	MinCloakDistance int32 // mincloakdistance integer default 0 [02 "Unit record"]

	// ModelTop is the model's top extent in whole world units, derived from
	// objects3d/<ObjectName>.3do rather than the FBI. The visibility builder
	// uses this extent as the observer's height addend [03 §3.2]. Zero when the
	// model is missing or entirely below its origin.
	//
	// It is deliberately absent from the canonical string below: that string
	// is the FBI record's identity, and this value comes from a different
	// asset with its own provenance.
	ModelTop int32

	// ModelTopFixed is the SAME model-top walk kept in full 16.16 world units:
	// the dword the definition loader writes as the unit's upper Y bound after
	// the model load, floored at zero [06 R-DMG-01 §7]. ModelTop above is that
	// dword's whole-unit high word, which is what the LOS eye-height writer
	// reads [03 §3.2]; the projectile contact test needs the whole dword,
	// because its vertical band is `unit.Y + modelTop` compared against a full
	// 16.16 projectile height [06 §8.1]. Zero when the model is missing,
	// unparsable, or entirely below its origin.
	//
	// Like ModelTop it comes from a different asset than the FBI record, so it
	// is deliberately absent from the canonical string below and therefore
	// changes neither the per-definition hash nor the catalog hash.
	ModelTopFixed int32

	// BuildPageCount is the definition's build-menu page-count byte
	// [02 R-CAT-01 §5 step 5]. With `<n>` the unit name, the compiler probes
	// `guis/<n>1.GUI`, `guis/<n>2.GUI`, … until the first missing one; the byte
	// is the index of that first missing page when at least one numbered page
	// existed (so page 0 — the orders state — is counted whether or not
	// `guis/<n>0.GUI` exists), else 1 when page 0 exists, else 0. Valid pages
	// are therefore `0 .. BuildPageCount-1`.
	//
	// Two authored sources establish page existence. Physical `<n>N.GUI` files
	// establish the initial count [02 R-CAT-01 §5 step 5]. A later download
	// MENU can raise it; when that physical file is absent, the HUD opens the
	// side's `ARMDL`/`CORDL` template and patches the authored BUTTON slots
	// [02 R-CAT-01 §8][07 R-HUD-03 §6]. Dividing the flat `CANBUILD` list by
	// six is not a third source: it invents pages neither a GUI nor a download
	// record authors.
	//
	// Like ModelTop this comes from a different asset than the FBI record and
	// is deliberately absent from the canonical identity string below.
	//
	// Download-menu compilation subsequently raises this byte to the largest
	// authored MENU byte naming the definition, never lowering it
	// [02 R-CAT-01 §8 step 2]. MENU is one greater than the visible page
	// number [fmt tdf][07 R-HUD-03 §6].
	BuildPageCount int32

	// HasPageZeroGUI is bit 31 of the first flags word: `guis/<n>0.GUI` exists
	// with a non-zero size [02 R-CAT-01 §5 step 5]. It lets page 0 compose a
	// page window instead of the side's `%sGEN.GUI` [07 R-HUD-03 §6]. No entry
	// of the reference install's `guis/` sets it, so stock content never
	// reaches that branch.
	HasPageZeroGUI bool

	// Flags and postures — integer accessor default 0 booleans except standing orders default 2 [02 "Unit record"].
	StandingMoveOrder  int32 // standingmoveorder default 2 [02 "Unit record"]
	StandingFireOrder  int32 // standingfireorder default 2 [02 "Unit record"]
	InitCloaked        bool  // init_cloaked [02 "Unit record"]
	Downloadable       bool  // downloadable [02 "Unit record"] C10
	Builder            bool  // builder [02 "Unit record"]
	Stealth            bool  // stealth [02 "Unit record"]
	BMCode             bool  // bmcode [02 "Unit record"]
	ZBuffer            bool  // zbuffer [02 "Unit record"]
	IsAirBase          bool  // isairbase [02 "Unit record"]
	IsTargetingUpgrade bool  // istargetingupgrade [02 "Unit record"]
	Teleporter         bool  // teleporter [02 "Unit record"]
	HideDamage         bool  // hidedamage [02 "Unit record"]
	ShootMe            bool  // shootme [02 "Unit record"]
	ArmoredState       bool  // armoredstate [02 "Unit record"]
	ActivateWhenBuilt  bool  // activatewhenbuilt [02 "Unit record"]
	CanFly             bool  // canfly [02 "Unit record"]
	CanHover           bool  // canhover [02 "Unit record"]
	Upright            bool  // upright [02 "Unit record"]
	Floater            bool  // floater [02 "Unit record"]
	Amphibious         bool  // amphibious [02 "Unit record"]
	IsFeature          bool  // isfeature [02 "Unit record"]
	NoShadow           bool  // noshadow [02 "Unit record"]
	ImmuneToParalyzer  bool  // immunetoparalyzer [02 "Unit record"]
	HoverAttack        bool  // hoverattack [02 "Unit record"]
	AntiWeapons        bool  // antiweapons [02 "Unit record"]
	Digger             bool  // digger [02 "Unit record"]
	OnOffable          bool  // onoffable [02 "Unit record"]
	MobileStandOrders  bool  // mobilestandorders [02 "Unit record"]
	FireStandOrders    bool  // firestandorders [02 "Unit record"]
	CanStop            bool  // canstop [02 "Unit record"]
	CanAttack          bool  // canattack [02 "Unit record"]
	CanGuard           bool  // canguard [02 "Unit record"]
	CanPatrol          bool  // canpatrol [02 "Unit record"]
	CanMove            bool  // canmove [02 "Unit record"]
	CanLoad            bool  // canload [02 "Unit record"]
	CanReclamate       bool  // canreclamate [02 "Unit record"]
	CanResurrect       bool  // canresurrect [02 "Unit record"]
	CanCapture         bool  // cancapture [02 "Unit record"]
	CanDGun            bool  // candgun [02 "Unit record"]
	Kamikaze           bool  // kamikaze [02 "Unit record"]
	NoRestrict         bool  // norestrict [02 "Unit record"] — bit 15
	ShowPlayerName     bool  // showplayername [02 "Unit record"]
	Commander          bool  // commander [02 "Unit record"]
	CantBeTransported  bool  // cantbetransported [02 "Unit record"]
	Wacky              bool  // wacky [02 "Unit record"] — parsed into bit 16 of same packed flag word as norestrict, no reader but preserved [02 "Unit record"]

	// Limit is the per-definition unit limit. It is not an FBI key: the
	// definition parser writes -1 (unlimited) into this field of every
	// definition it parses, and the only other writer is the multiplayer
	// lobby's restriction apply step, which a skirmish or campaign battle never
	// runs [05 R-SHARE-01 §9]. LimitEnabled records that the field was written,
	// which is what separates the parser's -1 from a hand-built fixture's Go
	// zero value; a written 0 means the definition may not be created at all.
	// UnitLimit is the older fixture-only spelling and stays source-compatible.
	// All three are deliberately excluded from identity.
	UnitLimit    int32
	LimitEnabled bool
	Limit        int32

	// Self-destruct countdown raw accessor — can distinguish authored vs absent [02 "Unit record"].
	SelfDestructCountdown        string // raw value
	SelfDestructCountdownPresent bool   // whether authored

	// Weapon link resolution (resolved after compile per C1 [02 §5]).
	// A name that matches no weapon record — including an empty name, which
	// can never match a record's blanked-out catalog name [02 §5
	// R-CONTENT-02] — resolves to the record-0 inactive sentinel, stock
	// [noweapon], ID 0 [06 R-DMG-01 §5]. The link is nil only when the
	// family carries no record 0 at all. The sentinel is inactive by its
	// zero slot number: consumers test content.IsWeaponInactive, never a nil
	// check, also when a name resolves to record 0 directly.
	Weapon1Def        *WeaponDef // resolved weapon1
	Weapon2Def        *WeaponDef // resolved weapon2
	Weapon3Def        *WeaponDef // resolved weapon3
	ExplodeAsDef      *WeaponDef // resolved explodeas
	SelfDestructAsDef *WeaponDef // resolved selfdestructas

	// Script is the required compiled COB program resolved at catalog link time
	// from scripts/<unitname>.cob. Catalog compilation rejects a missing,
	// unreadable, malformed, nil, or empty program [04 R-COB-04 §8]. Deliberately
	// absent from writeUnitCanonical: the hash is the FBI record's identity, and
	// this value comes from a different asset with its own provenance.
	Script           *cob.Program
	ScriptProvenance vfs.Provenance

	// Unknown retains inert parsed keys so a later phase can consume without re-parsing [02 §5] C14.
	// Keys are OriginalKey preserved case; e.g., wacky, noautofire, ovradjust, steeringmode, TEDClass etc have no behavior.
	Unknown map[string]string
}

// deriveMobilityDomain applies the class split used by placement. BMcode
// selects fixed/building versus mobile placement; among mobile definitions,
// canfly is the established aircraft discriminator. All other profile-backed
// mobile definitions share the ground/profile admission domain here; an
// absent class remains Unknown so it cannot silently acquire ground rules
// [04 §6.4][04 §9].
func deriveMobilityDomain(bmcode, canFly bool, movementClass string) MobilityDomain {
	if !bmcode {
		return MobilityFixed
	}
	if canFly {
		return MobilityAircraft
	}
	if movementClass != "" {
		return MobilityGround
	}
	return MobilityUnknown
}

// DefinitionMask returns the prelinked single-bit identity mask for this
// unit definition. It is a value copy and therefore safe to intersect with a
// weapon's linked category mask [R-P0-03; 06 §3.1].
func (u *UnitDef) DefinitionMask() CategoryMask {
	if u == nil {
		return CategoryMask{}
	}
	return u.UnitMask
}

// BoundingExtents returns the definition's bounding record as six signed
// 16.16 world-unit extents relative to a unit's own position, in the order
// `min = (x1, y1, z1)`, `max = (x2, y2, z2)`. This is the record the
// nano-segment submission routine expands into the six-word box for a unit
// target: `x1 = target.x + extent[0]`, `x2 = target.x + extent[3]`, and so on
// for Y and Z [05 R-WORK-01 §8].
//
// Only the maximum Y comes from model geometry [02 R-CAT-01 §7]. The X and Z
// bounds are footprint-derived, `±(footprint << 20) / 2`, where the shift is
// the sixteen world units per cell folded into the 16.16 representation; the
// minimum Y word is the zero store the definition loader makes immediately
// before the model-top walk (there is no min-Y walk anywhere in retail), and
// the maximum Y word is that walk's result, which ModelTopFixed already holds
// floored at zero.
//
// Two consequences worth stating, because both look like open questions in
// [05 R-WORK-01 §8] and neither is one here. First, that section records that
// `ReclaimUnit`, `Capture`, `SelfRepair`, `BuildingBuild` and the ground
// `RepairUnit` variants write `y1 = target.y` with the first Y extent omitted
// while `MobileBuild` and `HelpBuild` add it: the omitted extent is zero for
// every definition, so the two forms coincide and the divergence is
// unobservable. That also settles, for this build, the section's Unknown about
// which form the four VTOL work executors use.
//
// Second, these are world-space extents, so the model/world Z mirror of
// [03 R-RAST-01 §2] does not apply — the sign defect WU-19-138 found in the
// slot-distance word cannot recur here. X and Z are symmetric halves of the
// footprint, and Y is the height walk, which runs after the model-mirroring
// pass and on the one coordinate that pass never negates [02 "Mirroring"].
//
// A nil definition, or one with a zero footprint, yields a degenerate box at
// the unit's position; no fallback size is invented [I9].
func (u *UnitDef) BoundingExtents() (min, max [3]int32) {
	if u == nil {
		return min, max
	}
	// (footprint << 20) / 2 == footprint << 19, formed at the same 32-bit
	// width the definition loader uses.
	halfX := u.FootprintX << 19
	halfZ := u.FootprintZ << 19
	top := u.ModelTopFixed
	if top < 0 {
		top = 0 // the walk is floored at zero [02 R-CAT-01 §7]
	}
	return [3]int32{-halfX, 0, -halfZ}, [3]int32{halfX, top, halfZ}
}

// UnknownKeysSorted returns inert keys sorted for hash stability (I1).
func (u *UnitDef) UnknownKeysSorted() []string {
	if u.Unknown == nil {
		return nil
	}
	keys := make([]string, 0, len(u.Unknown))
	for k := range u.Unknown {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		li, lj := strings.ToLower(keys[i]), strings.ToLower(keys[j])
		if li != lj {
			return li < lj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// knownUnitKeys is the set of lower-cased keys that have a typed reader [02 "Unit record"].
// Anything not in this set is retained in Unknown (C14). Keys are foldName lowercased.
var knownUnitKeys = map[string]struct{}{
	"unitname": {}, "name": {}, "description": {}, "side": {}, "objectname": {}, "category": {}, "soundcategory": {}, "corpse": {}, "movementclass": {}, "weapon1": {}, "weapon2": {}, "weapon3": {}, "explodeas": {}, "selfdestructas": {}, "yardmap": {}, "defaultmissiontype": {}, "wpri_badtargetcategory": {}, "wsec_badtargetcategory": {}, "wspe_badtargetcategory": {}, "nochasecategory": {}, "ai_weight": {},
	"buildcostenergy": {}, "buildcostmetal": {}, "energymake": {}, "energyuse": {}, "metalmake": {}, "extractsmetal": {}, "windgenerator": {}, "tidalgenerator": {}, "energystorage": {}, "metalstorage": {}, "makesmetal": {}, "buildtime": {}, "workertime": {}, "healtime": {}, "cloakcost": {}, "cloakcostmoving": {},
	"maxvelocity": {}, "brakerate": {}, "acceleration": {}, "bankscale": {}, "pitchscale": {}, "damagemodifier": {}, "moverate1": {}, "moverate2": {}, "turnrate": {}, "waterline": {}, "minwaterdepth": {}, "maxwaterdepth": {}, "maxslope": {}, "badslope": {}, "maxwaterslope": {}, "badwaterslope": {}, "cruisealt": {}, "transportsize": {}, "transportcapacity": {}, "buildangle": {}, "builddistance": {}, "sortbias": {}, "maneuverleashlength": {}, "attackrunlength": {}, "kamikazedistance": {}, "footprintx": {}, "footprintz": {},
	"maxdamage": {}, "sightdistance": {}, "radardistance": {}, "sonardistance": {}, "radardistancejam": {}, "sonardistancejam": {}, "mincloakdistance": {},
	"standingmoveorder": {}, "standingfireorder": {}, "init_cloaked": {}, "downloadable": {}, "builder": {}, "stealth": {}, "bmcode": {}, "zbuffer": {}, "isairbase": {}, "istargetingupgrade": {}, "teleporter": {}, "hidedamage": {}, "shootme": {}, "armoredstate": {}, "activatewhenbuilt": {}, "canfly": {}, "canhover": {}, "upright": {}, "floater": {}, "amphibious": {}, "isfeature": {}, "noshadow": {}, "immunetoparalyzer": {}, "hoverattack": {}, "antiweapons": {}, "digger": {}, "onoffable": {}, "mobilestandorders": {}, "firestandorders": {}, "canstop": {}, "canattack": {}, "canguard": {}, "canpatrol": {}, "canmove": {}, "canload": {}, "canreclamate": {}, "canresurrect": {}, "cancapture": {}, "candgun": {}, "kamikaze": {}, "norestrict": {}, "showplayername": {}, "commander": {}, "cantbetransported": {}, "wacky": {},
	"selfdestructcountdown": {},
}

// compileUnitSection compiles a single UNITINFO section into a UnitDef.
// It uses typed accessors only from formats/tdf_typed.go [02 §4].
// Language-prefixed name trial <Language>name then name is applied for name/description [02 §3] C7.
// Translate.tdf identity fallback (byte-exact when missing) is handled by loadTranslateTable [02 §3] C7.
func compileUnitSection(section *formats.Section, logicalPath string, language string, prov Provenance) *UnitDef {
	unitName, _ := section.StringValue("unitname", "")
	// C7 language-prefixed trial: <Language>name then name [02 §3]. Missing Translate.tdf yields identity (handled by caller).
	displayName, _ := section.LanguageString(language, "name", "")
	description, _ := section.LanguageString(language, "description", "")
	side, _ := section.StringValue("side", "")
	objectName, _ := section.StringValue("objectname", "")
	category, _ := section.StringValue("category", "")
	soundCategory, _ := section.StringValue("soundcategory", "")
	corpse, _ := section.StringValue("corpse", "")
	movementClass, _ := section.StringValue("movementclass", "")
	weapon1, _ := section.StringValue("weapon1", "")
	weapon2, _ := section.StringValue("weapon2", "")
	weapon3, _ := section.StringValue("weapon3", "")
	explodeAs, _ := section.StringValue("explodeas", "")
	selfDestructAs, _ := section.StringValue("selfdestructas", "")
	yardMap, _ := section.StringValue("yardmap", "")
	defaultMissionType, _ := section.StringValue("defaultmissiontype", "")
	wpri, _ := section.StringValue("wpri_badtargetcategory", "none")
	wsec, _ := section.StringValue("wsec_badtargetcategory", "none")
	wspe, _ := section.StringValue("wspe_badtargetcategory", "none")
	noChase, _ := section.StringValue("nochasecategory", "none")
	aiWeight, _ := section.StringValue("ai_weight", "")
	// FBI string fields are fixed-size NUL-terminated buffers [02 "Unit record"].
	unitName = boundedString(unitName, 31)
	displayName = boundedString(displayName, 31)
	description = boundedString(description, 63)
	side = boundedString(side, 29)
	objectName = boundedString(objectName, 31)
	category = boundedString(category, 99)
	soundCategory = boundedString(soundCategory, 99)
	corpse = boundedString(corpse, 99)
	movementClass = boundedString(movementClass, 99)
	weapon1 = boundedString(weapon1, 127)
	weapon2 = boundedString(weapon2, 127)
	weapon3 = boundedString(weapon3, 127)
	explodeAs = boundedString(explodeAs, 127)
	selfDestructAs = boundedString(selfDestructAs, 127)
	yardMap = boundedString(yardMap, 1023)
	defaultMissionType = boundedString(defaultMissionType, 99)
	wpri = boundedString(wpri, 99)
	wsec = boundedString(wsec, 99)
	wspe = boundedString(wspe, 99)
	noChase = boundedString(noChase, 99)
	aiWeight = boundedString(aiWeight, 63)

	// Economy — integer/floating accessors [02 "Unit record"].
	buildCostEnergy := section.IntValue("buildcostenergy", 0)
	buildCostMetal := section.IntValue("buildcostmetal", 0)
	energyMake := section.FloatValue("energymake", 0)
	energyUse := section.FloatValue("energyuse", 0)
	metalMake := section.FloatValue("metalmake", 0)
	extractsMetal := section.FloatValue("extractsmetal", 0)
	windGenerator := section.FloatValue("windgenerator", 0)
	tidalGenerator := section.FloatValue("tidalgenerator", 0)
	energyStorage := section.FloatValue("energystorage", 0)
	metalStorage := section.FloatValue("metalstorage", 0)
	makesMetal := section.IntValue("makesmetal", 0)
	buildTime := section.IntValue("buildtime", 0)
	workerTime := section.IntValue("workertime", 0)
	healTime := section.IntValue("healtime", 0)
	cloakCost := section.IntValue("cloakcost", 0)
	// cloakcostmoving default is the value just read for cloakcost [02 "Unit record"].
	cloakCostMoving := section.IntValue("cloakcostmoving", cloakCost)

	// Movement and geometry — fixed accessor with chained defaults [02 "Unit record"].
	maxVelocity := section.FixedValue("maxvelocity", 0)
	brakeRate := section.FixedValue("brakerate", 0)
	acceleration := section.FixedValue("acceleration", 0)
	bankScale := section.FixedValue("bankscale", 65536)           // 1.0 [02 "Unit record"]
	pitchScale := section.FixedValue("pitchscale", 0)             // [02 "Unit record"]
	damageModifier := section.FixedValue("damagemodifier", 65536) // 1.0 [02 "Unit record"]
	moveRate1 := section.FixedValue("moverate1", maxVelocity*2)   // twice maxvelocity just read [02 "Unit record"]
	moveRate2 := section.FixedValue("moverate2", maxVelocity*2)   // [02 "Unit record"]
	turnRate := section.IntValue("turnrate", 0)
	waterline := section.IntValue("waterline", 0)
	// Unit discovery precedes movement-class linking, so compile the per-unit
	// scratch record now; linking replaces it when the authored class resolves,
	// while an absent or unresolved name retains it. Reuse the CLASS reader so
	// the startup template, eight-key parse order, field-width narrowing, and
	// three unconditional clamps cannot drift between pool and scratch records
	// [02 §5 "Movement class record"][04 §6.1 R-DOC04-A][fmt fbi].
	scratchMovement := compileMovementSection(section, unitName, prov)
	minWaterDepth := scratchMovement.MinWaterDepth
	maxWaterDepth := scratchMovement.MaxWaterDepth
	maxSlope := scratchMovement.MaxSlope
	badSlope := scratchMovement.BadSlope
	maxWaterSlope := scratchMovement.MaxWaterSlope
	badWaterSlope := scratchMovement.BadWaterSlope
	cruiseAlt := section.IntValue("cruisealt", 0)
	transportSize := section.IntValue("transportsize", 0)
	transportCapacity := section.IntValue("transportcapacity", 0)
	buildAngle := section.IntValue("buildangle", 0)
	buildDistance := section.IntValue("builddistance", 0)
	sortBias := section.IntValue("sortbias", 0)
	maneuverLeashLength := section.IntValue("maneuverleashlength", 0)
	attackRunLength := section.IntValue("attackrunlength", 0)
	kamikazeDistance := section.IntValue("kamikazedistance", 0)
	footprintX := scratchMovement.FootprintX
	footprintZ := scratchMovement.FootprintZ

	// Combat and sensors [02 "Unit record"].
	maxDamage := section.IntValue("maxdamage", 0)
	sightDistance := section.IntValue("sightdistance", 0)
	radarDistance := section.IntValue("radardistance", 0)
	sonarDistance := section.IntValue("sonardistance", 0)
	radarDistanceJam := section.IntValue("radardistancejam", 0)
	sonarDistanceJam := section.IntValue("sonardistancejam", 0)
	minCloakDistance := section.IntValue("mincloakdistance", 0)

	// Flags and postures — integer accessor default 0 booleans except standing orders default 2 [02 "Unit record"].
	standingMoveOrder := section.IntValue("standingmoveorder", 2)
	standingFireOrder := section.IntValue("standingfireorder", 2)
	initCloaked := section.BoolValue("init_cloaked", false)
	downloadable := section.BoolValue("downloadable", false)
	builder := section.BoolValue("builder", false)
	stealth := section.BoolValue("stealth", false)
	bmcode := section.BoolValue("bmcode", false)
	zbuffer := section.BoolValue("zbuffer", false)
	isAirBase := section.BoolValue("isairbase", false)
	isTargetingUpgrade := section.BoolValue("istargetingupgrade", false)
	teleporter := section.BoolValue("teleporter", false)
	hideDamage := section.BoolValue("hidedamage", false)
	shootMe := section.BoolValue("shootme", false)
	armoredState := section.BoolValue("armoredstate", false)
	activateWhenBuilt := section.BoolValue("activatewhenbuilt", false)
	canFly := section.BoolValue("canfly", false)
	canHover := section.BoolValue("canhover", false)
	upright := section.BoolValue("upright", false)
	floater := section.BoolValue("floater", false)
	amphibious := section.BoolValue("amphibious", false)
	isFeature := section.BoolValue("isfeature", false)
	noShadow := section.BoolValue("noshadow", false)
	immuneToParalyzer := section.BoolValue("immunetoparalyzer", false)
	hoverAttack := section.BoolValue("hoverattack", false)
	antiWeapons := section.BoolValue("antiweapons", false)
	digger := section.BoolValue("digger", false)
	onOffable := section.BoolValue("onoffable", false)
	mobileStandOrders := section.BoolValue("mobilestandorders", false)
	fireStandOrders := section.BoolValue("firestandorders", false)
	canStop := section.BoolValue("canstop", false)
	canAttack := section.BoolValue("canattack", false)
	canGuard := section.BoolValue("canguard", false)
	canPatrol := section.BoolValue("canpatrol", false)
	canMove := section.BoolValue("canmove", false)
	canLoad := section.BoolValue("canload", false)
	canReclamate := section.BoolValue("canreclamate", false)
	canResurrect := section.BoolValue("canresurrect", false)
	canCapture := section.BoolValue("cancapture", false)
	canDGun := section.BoolValue("candgun", false)
	kamikaze := section.BoolValue("kamikaze", false)
	noRestrict := section.BoolValue("norestrict", false)
	showPlayerName := section.BoolValue("showplayername", false)
	commander := section.BoolValue("commander", false)
	cantBeTransported := section.BoolValue("cantbetransported", false)
	wacky := section.BoolValue("wacky", false) // bit 16 same word as norestrict bit15 [02 "Unit record"]

	// Raw accessor for selfdestructcountdown so we can tell authored vs absent [02 "Unit record"].
	selfDestructCountdown, selfDestructCountdownPresent := section.RawValue("selfdestructcountdown")
	mobilityDomain := deriveMobilityDomain(bmcode, canFly, movementClass)

	// Unknown inert keys retained [02 §5] C14.
	unknown := make(map[string]string)
	for _, item := range section.Items {
		if item.Kind != formats.Assignment {
			continue
		}
		fold := strings.ToLower(item.Key)
		if _, ok := knownUnitKeys[fold]; ok {
			continue
		}
		// Language-prefixed variants for current language are already consumed via LanguageString — not unknown.
		if language != "" {
			lfold := strings.ToLower(language + "name")
			if fold == lfold {
				continue
			}
			lfold = strings.ToLower(language + "description")
			if fold == lfold {
				continue
			}
		}
		unknown[item.OriginalKey] = item.Value
	}
	if len(unknown) == 0 {
		unknown = nil
	}

	// Canonical key is the lowercased unitname per retail case-insensitive catalog [02 §5].
	// Fallback to logical filename stem when unitname is empty (should not happen in retail, but keep deterministic).
	canonical := CanonicalKey(unitName)
	if canonical == "" {
		base := logicalPath
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		if dot := strings.LastIndex(base, "."); dot >= 0 {
			base = base[:dot]
		}
		canonical = CanonicalKey(base)
		unitName = base
	}

	u := &UnitDef{
		DefinitionHeader: DefinitionHeader{
			CanonicalKey: canonical,
			Provenance:   prov,
		},
		UnitName:              unitName,
		Name:                  displayName,
		Description:           description,
		Side:                  side,
		ObjectName:            objectName,
		Category:              category,
		SoundCategory:         soundCategory,
		Corpse:                corpse,
		MovementClass:         movementClass,
		MobilityDomain:        mobilityDomain,
		Weapon1:               weapon1,
		Weapon2:               weapon2,
		Weapon3:               weapon3,
		ExplodeAs:             explodeAs,
		SelfDestructAs:        selfDestructAs,
		YardMap:               yardMap,
		DefaultMissionType:    defaultMissionType,
		BadTargetCategoryWPRI: wpri,
		BadTargetCategoryWSEC: wsec,
		BadTargetCategoryWSPE: wspe,
		NoChaseCategory:       noChase,
		AIWeight:              aiWeight,
		// The definition parser stores -1 (unlimited) into the per-definition
		// limit field of every definition it parses; in every single-player
		// session that stays the final value, and a 0 can only come from the
		// multiplayer restriction apply step [05 R-SHARE-01 §9]. Marking the
		// field written is what lets a reader tell that authored 0 from an
		// unwritten Go zero.
		LimitEnabled:                 true,
		Limit:                        -1,
		BuildCostEnergy:              buildCostEnergy,
		BuildCostMetal:               buildCostMetal,
		EnergyMake:                   energyMake,
		EnergyUse:                    energyUse,
		MetalMake:                    metalMake,
		ExtractsMetal:                extractsMetal,
		WindGenerator:                windGenerator,
		TidalGenerator:               tidalGenerator,
		EnergyStorage:                energyStorage,
		MetalStorage:                 metalStorage,
		MakesMetal:                   makesMetal,
		BuildTime:                    buildTime,
		WorkerTime:                   workerTime,
		HealTime:                     healTime,
		CloakCost:                    cloakCost,
		CloakCostMoving:              cloakCostMoving,
		MaxVelocity:                  maxVelocity,
		BrakeRate:                    brakeRate,
		Acceleration:                 acceleration,
		BankScale:                    bankScale,
		PitchScale:                   pitchScale,
		DamageModifier:               damageModifier,
		MoveRate1:                    moveRate1,
		MoveRate2:                    moveRate2,
		TurnRate:                     turnRate,
		Waterline:                    waterline,
		MinWaterDepth:                minWaterDepth,
		MaxWaterDepth:                maxWaterDepth,
		MaxSlope:                     maxSlope,
		BadSlope:                     badSlope,
		MaxWaterSlope:                maxWaterSlope,
		BadWaterSlope:                badWaterSlope,
		CruiseAlt:                    cruiseAlt,
		TransportSize:                transportSize,
		TransportCapacity:            transportCapacity,
		BuildAngle:                   buildAngle,
		BuildDistance:                buildDistance,
		SortBias:                     sortBias,
		ManeuverLeashLength:          maneuverLeashLength,
		AttackRunLength:              attackRunLength,
		KamikazeDistance:             kamikazeDistance,
		FootprintX:                   footprintX,
		FootprintZ:                   footprintZ,
		MaxDamage:                    maxDamage,
		SightDistance:                sightDistance,
		RadarDistance:                radarDistance,
		SonarDistance:                sonarDistance,
		RadarDistanceJam:             radarDistanceJam,
		SonarDistanceJam:             sonarDistanceJam,
		MinCloakDistance:             minCloakDistance,
		StandingMoveOrder:            standingMoveOrder,
		StandingFireOrder:            standingFireOrder,
		InitCloaked:                  initCloaked,
		Downloadable:                 downloadable,
		Builder:                      builder,
		Stealth:                      stealth,
		BMCode:                       bmcode,
		ZBuffer:                      zbuffer,
		IsAirBase:                    isAirBase,
		IsTargetingUpgrade:           isTargetingUpgrade,
		Teleporter:                   teleporter,
		HideDamage:                   hideDamage,
		ShootMe:                      shootMe,
		ArmoredState:                 armoredState,
		ActivateWhenBuilt:            activateWhenBuilt,
		CanFly:                       canFly,
		CanHover:                     canHover,
		Upright:                      upright,
		Floater:                      floater,
		Amphibious:                   amphibious,
		IsFeature:                    isFeature,
		NoShadow:                     noShadow,
		ImmuneToParalyzer:            immuneToParalyzer,
		HoverAttack:                  hoverAttack,
		AntiWeapons:                  antiWeapons,
		Digger:                       digger,
		OnOffable:                    onOffable,
		MobileStandOrders:            mobileStandOrders,
		FireStandOrders:              fireStandOrders,
		CanStop:                      canStop,
		CanAttack:                    canAttack,
		CanGuard:                     canGuard,
		CanPatrol:                    canPatrol,
		CanMove:                      canMove,
		CanLoad:                      canLoad,
		CanReclamate:                 canReclamate,
		CanResurrect:                 canResurrect,
		CanCapture:                   canCapture,
		CanDGun:                      canDGun,
		Kamikaze:                     kamikaze,
		NoRestrict:                   noRestrict,
		ShowPlayerName:               showPlayerName,
		Commander:                    commander,
		CantBeTransported:            cantBeTransported,
		Wacky:                        wacky,
		SelfDestructCountdown:        selfDestructCountdown,
		SelfDestructCountdownPresent: selfDestructCountdownPresent,
		Unknown:                      unknown,
	}

	// Hash over canonical bytes including defaults, independent of map iteration (I1) [02 §5] C12.
	u.Hash = HashDefinition(writeUnitCanonical(u))
	return u
}

// writeUnitCanonical renders a UnitDef's canonical byte form: fixed order,
// never ranging a map directly (I1) [02 §5] C12. Both the compile-time hash
// and the downloadable enforcement's re-hash use it so the two can never
// drift.
func writeUnitCanonical(u *UnitDef) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%d|%s|%s|%s|%s|%s|%s|%s|", u.CanonicalKey, u.UnitDefID, u.UnitName, u.Name, u.Description, u.Side, u.ObjectName, u.Category, u.SoundCategory)
	fmt.Fprintf(&b, "%s|", u.Corpse)
	fmt.Fprintf(&b, "%s|%d|%s|%s|%s|%s|%s|%s|", u.MovementClass, u.MobilityDomain, u.Weapon1, u.Weapon2, u.Weapon3, u.ExplodeAs, u.SelfDestructAs, u.YardMap)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|", u.MinWaterDepth, u.MaxWaterDepth, u.MaxSlope, u.BadSlope, u.MaxWaterSlope, u.BadWaterSlope)
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|", u.DefaultMissionType, u.BadTargetCategoryWPRI, u.BadTargetCategoryWSEC, u.BadTargetCategoryWSPE, u.NoChaseCategory)
	fmt.Fprintf(&b, "%s|", u.AIWeight)
	fmt.Fprintf(&b, "%d|%d|%.10f|%.10f|%.10f|%.10f|", u.BuildCostEnergy, u.BuildCostMetal, u.EnergyMake, u.EnergyUse, u.MetalMake, u.ExtractsMetal)
	fmt.Fprintf(&b, "%.10f|%.10f|%.10f|%.10f|%d|%d|%d|%d|%d|%d|", u.WindGenerator, u.TidalGenerator, u.EnergyStorage, u.MetalStorage, u.MakesMetal, u.BuildTime, u.WorkerTime, u.HealTime, u.CloakCost, u.CloakCostMoving)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|%d|%d|%d|%d|", u.MaxVelocity, u.BrakeRate, u.Acceleration, u.BankScale, u.PitchScale, u.DamageModifier, u.MoveRate1, u.MoveRate2, u.TurnRate, u.Waterline)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|%d|%d|%d|%d|", u.CruiseAlt, u.TransportSize, u.TransportCapacity, u.BuildAngle, u.BuildDistance, u.SortBias, u.ManeuverLeashLength, u.AttackRunLength, u.KamikazeDistance, u.FootprintX)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|%d|", u.FootprintZ, u.MaxDamage, u.SightDistance, u.RadarDistance, u.SonarDistance, u.RadarDistanceJam, u.SonarDistanceJam)
	fmt.Fprintf(&b, "%d|%d|%d|", u.MinCloakDistance, u.StandingMoveOrder, u.StandingFireOrder)
	flags := []bool{u.InitCloaked, u.Downloadable, u.Builder, u.Stealth, u.BMCode, u.ZBuffer, u.IsAirBase, u.IsTargetingUpgrade, u.Teleporter, u.HideDamage, u.ShootMe, u.ArmoredState, u.ActivateWhenBuilt, u.CanFly, u.CanHover, u.Upright, u.Floater, u.Amphibious, u.IsFeature, u.NoShadow, u.ImmuneToParalyzer, u.HoverAttack, u.AntiWeapons, u.Digger, u.OnOffable, u.MobileStandOrders, u.FireStandOrders, u.CanStop, u.CanAttack, u.CanGuard, u.CanPatrol, u.CanMove, u.CanLoad, u.CanReclamate, u.CanResurrect, u.CanCapture, u.CanDGun, u.Kamikaze, u.NoRestrict, u.ShowPlayerName, u.Commander, u.CantBeTransported, u.Wacky}
	for _, f := range flags {
		if f {
			b.WriteString("1|")
		} else {
			b.WriteString("0|")
		}
	}
	fmt.Fprintf(&b, "%s|%t|", u.SelfDestructCountdown, u.SelfDestructCountdownPresent)
	fmt.Fprintf(&b, "catmasks|")
	for _, m := range []CategoryMask{u.UnitMask, u.BadTargetCategoryWPRIMask, u.BadTargetCategoryWSECMask, u.BadTargetCategoryWSPEMask, u.NoChaseCategoryMask} {
		for _, word := range m.Words {
			fmt.Fprintf(&b, "%08x|", word)
		}
	}
	b.WriteString("unknown|")
	for _, k := range u.UnknownKeysSorted() {
		fmt.Fprintf(&b, "%s=%s|", k, u.Unknown[k])
	}
	return []byte(b.String())
}

// CompileUnits compiles units from the VFS. Discovery is units/*.fbi (278) — filter .fbi only
// (directory also holds .bat/.pl/.txt/.xls junk) [PLAN 02 Discovery].
// It returns a map keyed by CanonicalKey(unitname) [02 §5]. The helper uses only typed accessors
// from formats/tdf_typed.go [02 §4].
// Language-prefixed name fallback is applied via LanguageString (<Language>name then name) with
// Translate.tdf identity fallback (byte-exact) when the file is missing [02 §3] C7.
func CompileUnits(fs vfs.FSOps) (map[string]*UnitDef, error) {
	return CompileUnitsWithLanguage(fs, "")
}

// CompileUnitsWithLanguage compiles units with an explicit language for the language-prefixed
// name trial [02 §3] C7. An empty language yields the base name/description; a non-empty language
// like "German" tries "<Language>name" then "name". Missing Translate.tdf yields identity (byte-exact) [02 §3].
func CompileUnitsWithLanguage(fs vfs.FSOps, language string) (map[string]*UnitDef, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	// Note: gamedata/translate.tdf is NOT loaded here. The unit name/
	// description fallback (<Language>name then name) is handled by
	// LanguageString [02 §3] C7; runtime-message translation belongs to
	// whichever later phase owns the configured language (phase 7), which
	// should load the table itself rather than re-parse or carry an unused
	// map through the catalog.

	entries, err := discoverArchiveContent(fs, "units", ".fbi")
	if err != nil {
		return nil, fmt.Errorf("content: units: %w", err)
	}
	// ReadDir already sorts by Path [vfs.ReadDir], so iteration is stable (I1).
	result := make(map[string]*UnitDef)
	for _, entry := range entries {
		data := entry.data
		e := entry.info
		prov := ProvenanceFrom(e)
		doc, err := formats.ParseTDF(data)
		if err != nil {
			return nil, fmt.Errorf("content: %s: %w", e.Path, formats.WithTDFFile(err, e.Path))
		}
		// Each unit file contributes one UNITINFO section [02 "Unit record"].
		// A file without one aborts the whole discovery stage: the loader
		// fails at once and every file after it in enumeration order stays an
		// unparsed record that the compiler compacts out, so the catalog is
		// exactly the files that preceded it [02 R-CAT-01 §4]. The stock
		// corpus has no such file.
		unitSection := doc.Root.Section("UNITINFO")
		if unitSection == nil {
			break
		}
		u := compileUnitSection(unitSection, e.Path, language, prov)
		// Catalog construction is case-insensitive for names [02 §5].
		key := u.CanonicalKey
		// Duplicate canonical keys — last wins deterministic since we iterate sorted ReadDir (I1).
		result[key] = u
	}
	return result, nil
}

// LinkUnitWeapons resolves weapon1..3 (and explodeas/selfdestructas) after all weapons compile
// so enumeration order cannot leak into identity [02 §5] C1.
//
// Resolution follows the recovered runtime name resolution [02 §5 R-CONTENT-02]:
// a case-insensitive comparison against each record's catalog name, first match
// wins — the compiled map serves that directly, holding exactly one record per
// surviving catalog name. A name that matches no record does not abort and is
// not left unresolved: the link slot is filled with a reference to record 0 —
// the inactive sentinel, stock [noweapon] (ID 0) — whenever the weapon family
// carries one. Only a family without any record 0 leaves the nil inactive
// marker, and that case stays explicit here rather than silent. The sentinel
// is inactive by its zero slot number; consumers gate weapon links on
// content.IsWeaponInactive, also when a name resolves to record 0 directly
// [02 §5 R-CONTENT-02].
//
// An empty name resolves the same way as a miss: the loader's record scan
// cannot match an empty name against a record's blanked-out catalog name
// [02 §5 R-CONTENT-02], so the lookup "returns not-found both for a name
// that matches nothing and for an **empty** name — and replaces not-found
// with a reference to weapon record 0"; the five links "are therefore never
// null for any definition that went through the loader" [06 R-DMG-01 §5].
// Established. An unarmed definition's weapon links resolve to record 0 like
// any other miss, never to nil, for a family that carries the sentinel.
func LinkUnitWeapons(units map[string]*UnitDef, weapons map[string]*WeaponDef) {
	if units == nil || weapons == nil {
		return
	}
	// Deterministic iteration: sorted unit keys (I1).
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// The miss policy lives in one place, Catalog.WeaponLink [02 §5
	// R-CONTENT-02]; a read-only view over the map is enough — the method
	// derives its slot-order scan and the record-0 lookup from Weapons alone.
	view := &Catalog{Weapons: weapons}
	for _, k := range keys {
		u := units[k]
		u.Weapon1Def, _ = view.WeaponLink(u.Weapon1)
		u.Weapon2Def, _ = view.WeaponLink(u.Weapon2)
		u.Weapon3Def, _ = view.WeaponLink(u.Weapon3)
		u.ExplodeAsDef, _ = view.WeaponLink(u.ExplodeAs)
		u.SelfDestructAsDef, _ = view.WeaponLink(u.SelfDestructAs)
	}
}

// ApplyMovementFootprints copies the resolved movement record fields retained
// by the unit definition: footprint, depth limits, and all four slope bytes
// [02 §5 "Movement class record"]. An unresolved name keeps the complete FBI
// scratch record compiled above. A resolved class replaces every linked field,
// including zero-valued footprint extents.
func ApplyMovementFootprints(units map[string]*UnitDef, movement map[string]*MovementClass) {
	keys := make([]string, 0, len(units))
	for key := range units {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		u := units[key]
		if u == nil || u.MovementClass == "" || movement == nil {
			continue
		}
		mc := movement[CanonicalKey(u.MovementClass)]
		if mc == nil {
			continue
		}
		u.FootprintX = mc.FootprintX
		u.FootprintZ = mc.FootprintZ
		u.MaxWaterDepth = mc.MaxWaterDepth
		u.MinWaterDepth = mc.MinWaterDepth
		u.MaxSlope = mc.MaxSlope
		u.BadSlope = mc.BadSlope
		u.MaxWaterSlope = mc.MaxWaterSlope
		u.BadWaterSlope = mc.BadWaterSlope
		// The resolved profile is part of the immutable compiled definition and
		// thus must participate in its canonical identity/hash.
		u.Hash = HashDefinition(writeUnitCanonical(u))
	}
}

// EnforceDownloadable walks every unit definition and compares its name case-insensitively
// against every build-menu button name. A match whose downloadable bit is clear raises the
// exact warning "Hey! Somebody forgot to set downloadable=1 for %s" once, silently forces
// the bit on, and re-finalizes the record [02 "Unit record"] C10.
// buildMenuNames are canonical button names (case-insensitive comparison via CanonicalKey).
//
// The warnings are returned verbatim in unit-key order instead of printed:
// Compile routes them onto Catalog.Warnings so the caller owns diagnostics.
// Each fixed unit is re-hashed through writeUnitCanonical; the catalog-level
// hash is recomputed by the caller afterwards.
func EnforceDownloadable(units map[string]*UnitDef, buildMenuNames []string) []string {
	if units == nil || len(buildMenuNames) == 0 {
		return nil
	}
	// Canonicalize build menu names for case-insensitive comparison [02 "Unit record"].
	menuSet := make(map[string]struct{}, len(buildMenuNames))
	for _, n := range buildMenuNames {
		menuSet[CanonicalKey(n)] = struct{}{}
	}
	// Deterministic iteration: sorted unit keys (I1).
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var warnings []string
	for _, k := range keys {
		u := units[k]
		if u.Downloadable {
			continue
		}
		// Compare the unit's canonical UnitName against the menu buttons
		// case-insensitively [02 "Unit record"] enforcement walk.
		if _, ok := menuSet[CanonicalKey(u.UnitName)]; !ok {
			continue
		}
		// Verbatim warning [02 "Unit record"] C10.
		warnings = append(warnings, fmt.Sprintf("Hey! Somebody forgot to set downloadable=1 for %s", u.UnitName))
		u.Downloadable = true
		u.Hash = HashDefinition(writeUnitCanonical(u))
	}
	return warnings
}
