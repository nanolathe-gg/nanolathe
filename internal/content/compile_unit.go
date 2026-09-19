// The unit (FBI) compiler.

package content

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/vfs"
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
	// DiscoveryProvenance identifies the source of admission and the fields
	// retained across secondary parsing; Provenance identifies the active FBI
	// source for runtime fields [02 R-CAT-01 §§4–5].
	DiscoveryProvenance Provenance
	// DiscoveryOnly records have not received the secondary gameplay parse;
	// category and weapon linking must preserve allocation zeros [02 R-CAT-01 §5].
	DiscoveryOnly bool
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
	BuildCostEnergy float32 // integer parsed, single-float store, default 0 [02 R-KEYS-01 §5]
	BuildCostMetal  float32 // integer parsed, single-float store, default 0 [02 R-KEYS-01 §5]
	EnergyMake      float64 // energymake floating default 0 [02 "Unit record"]
	EnergyUse       float64 // energyuse floating default 0 [02 "Unit record"]
	MetalMake       float64 // metalmake floating default 0 [02 "Unit record"]
	ExtractsMetal   float64 // extractsmetal floating default 0 [02 "Unit record"]
	WindGenerator   float64 // windgenerator floating default 0 [02 "Unit record"]
	TidalGenerator  float64 // tidalgenerator floating default 0 [02 "Unit record"]
	EnergyStorage   float64 // energystorage floating default 0 [02 "Unit record"]
	MetalStorage    float64 // metalstorage floating default 0 [02 "Unit record"]
	MakesMetal      int32   // makesmetal integer default 0, wrapped to an unsigned 8-bit store [02 R-KEYS-01 §5]
	BuildTime       int32   // buildtime integer default 0 [02 "Unit record"]
	WorkerTime      int32   // workertime integer default 0, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §5]
	HealTime        int32   // healtime integer default 0, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §5]
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
	TurnRate            int32 // turnrate integer default 0, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §5]
	Waterline           int32 // waterline integer default 0, wrapped to an unsigned 8-bit store [02 R-KEYS-01 §5]
	MinWaterDepth       int32 // scratch-profile signed-16 minimum depth; template −10000 [02 §5 "Movement class record"][04 §6.1 R-DOC04-A]
	MaxWaterDepth       int32 // scratch-profile signed-16 maximum depth; template 10000 [02 §5 "Movement class record"][04 §6.1 R-DOC04-A]
	MaxSlope            int32 // scratch-profile byte slope after the ordered clamps [02 §5 "Movement class record"]
	BadSlope            int32 // scratch-profile byte soft land-slope threshold [02 §5 "Movement class record"]
	MaxWaterSlope       int32 // scratch-profile byte water slope copied with MaxSlope [02 §5 "Movement class record"]
	BadWaterSlope       int32 // scratch-profile byte soft water-slope threshold [02 §5 "Movement class record"]
	CruiseAlt           int32 // cruisealt integer default 0, wrapped to a signed 16-bit store [02 R-KEYS-01 §5]
	TransportSize       int32 // transportsize integer default 0, wrapped to an unsigned 8-bit store [02 R-KEYS-01 §5]
	TransportCapacity   int32 // transportcapacity integer default 0, wrapped to an unsigned 8-bit store [02 R-KEYS-01 §5]
	BuildAngle          int32 // buildangle integer default 0, wrapped to an unsigned 16-bit store [02 R-P28-ANG-01R §1]
	BuildDistance       int32 // builddistance integer default 0, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §5]
	SortBias            int32 // sortbias integer default 0, wrapped to a 16-bit store [02 R-KEYS-01 §5]
	ManeuverLeashLength int32 // maneuverleashlength integer default 0, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §5]
	AttackRunLength     int32 // attackrunlength integer default 0, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §5]
	KamikazeDistance    int32 // kamikazedistance integer default 0, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §5]
	FootprintX          int32 // footprintx integer default 0 — occupancy size in cells [fmt fbi]
	FootprintZ          int32 // footprintz integer default 0 [fmt fbi]

	// Combat and sensors [02 "Unit record"].
	MaxDamage        int32 // maxdamage integer default 0 [02 "Unit record"]
	SightDistance    int32 // sightdistance integer default 0, wrapped to a signed 16-bit store [02 R-KEYS-01 §5]
	RadarDistance    int32 // radardistance integer default 0, wrapped to a signed 16-bit store [02 R-KEYS-01 §5]
	SonarDistance    int32 // sonardistance integer default 0, wrapped to a signed 16-bit store [02 R-KEYS-01 §5]
	RadarDistanceJam int32 // radardistancejam integer default 0, wrapped to a signed 16-bit store [02 R-KEYS-01 §5]
	SonarDistanceJam int32 // sonardistancejam integer default 0, wrapped to a signed 16-bit store [02 R-KEYS-01 §5]
	MinCloakDistance int32 // mincloakdistance integer default 0, wrapped to a signed 16-bit store; a cloak-capable definition that compiles it to 0 gets the derived 80 [02 "Unit record"]

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
	BMCode             uint8 // bmcode [02 "Unit record"]
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
	// from scripts/<unitname>.cob. Missing, unreadable, malformed, nil, or empty
	// programs remain nil with catalog warnings; unit creation refuses a
	// definition with no program [04 R-COB-04 §8], DESIGN_CONTENT_VFS §3.4 C9. Deliberately
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
	return sortedFoldedKeys(u.Unknown, asciiFoldContent)
}

// knownUnitKeys is the set of lower-cased keys that have a typed reader [02 "Unit record"].
// Anything not in this set is retained in Unknown (C14). Keys are foldName lowercased.
var knownUnitKeys = map[string]struct{}{
	"unitname": {}, "name": {}, "description": {}, "side": {}, "objectname": {}, "category": {}, "soundcategory": {}, "corpse": {}, "movementclass": {}, "weapon1": {}, "weapon2": {}, "weapon3": {}, "explodeas": {}, "selfdestructas": {}, "yardmap": {}, "defaultmissiontype": {}, "wpri_badtargetcategory": {}, "wsec_badtargetcategory": {}, "wspe_badtargetcategory": {}, "nochasecategory": {}, "ai_weight": {},
	"buildcostenergy": {}, "buildcostmetal": {}, "energymake": {}, "energyuse": {}, "metalmake": {}, "extractsmetal": {}, "windgenerator": {}, "tidalgenerator": {}, "energystorage": {}, "metalstorage": {}, "makesmetal": {}, "buildtime": {}, "workertime": {}, "healtime": {}, "cloakcost": {}, "cloakcostmoving": {},
	"maxvelocity": {}, "brakerate": {}, "acceleration": {}, "bankscale": {}, "pitchscale": {}, "damagemodifier": {}, "moverate1": {}, "moverate2": {}, "turnrate": {}, "waterline": {}, "minwaterdepth": {}, "maxwaterdepth": {}, "maxslope": {}, "badslope": {}, "maxwaterslope": {}, "badwaterslope": {}, "cruisealt": {}, "transportsize": {}, "transportcapacity": {}, "buildangle": {}, "builddistance": {}, "sortbias": {}, "maneuverleashlength": {}, "attackrunlength": {}, "kamikazedistance": {}, "footprintx": {}, "footprintz": {},
	"maxdamage": {}, "sightdistance": {}, "radardistance": {}, "sonardistance": {}, "radardistancejam": {}, "sonardistancejam": {}, "mincloakdistance": {},
	"standingmoveorder": {}, "standingfireorder": {}, "init_cloaked": {}, "downloadable": {}, "builder": {}, "stealth": {}, "bmcode": {}, "zbuffer": {}, "isairbase": {}, "istargetingupgrade": {}, "teleporter": {}, "hidedamage": {}, "shootme": {}, "armoredstate": {}, "activatewhenbuilt": {}, "canfly": {}, "canhover": {}, "upright": {}, "floater": {}, "amphibious": {}, "isfeature": {}, "noshadow": {}, "immunetoparalyzer": {}, "hoverattack": {}, "antiweapons": {}, "digger": {}, "onoffable": {}, "mobilestandorders": {}, "firestandorders": {}, "canstop": {}, "canattack": {}, "canguard": {}, "canpatrol": {}, "canmove": {}, "canload": {}, "canreclamate": {}, "canresurrect": {}, "cancapture": {}, "candgun": {}, "kamikaze": {}, "norestrict": {}, "showplayername": {}, "commander": {}, "cantbetransported": {}, "wacky": {},
	"selfdestructcountdown": {}, "version": {}, "copyright": {},
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
	objectName, objectAuthored := section.StringValue("objectname", "")
	if !objectAuthored {
		objectName = unitName
	}
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
	buildCostEnergy := float32(section.IntValue("buildcostenergy", 0))
	buildCostMetal := float32(section.IntValue("buildcostmetal", 0))
	energyMake := section.FloatValue("energymake", 0)
	energyUse := section.FloatValue("energyuse", 0)
	metalMake := section.FloatValue("metalmake", 0)
	extractsMetal := section.FloatValue("extractsmetal", 0)
	windGenerator := section.FloatValue("windgenerator", 0)
	tidalGenerator := section.FloatValue("tidalgenerator", 0)
	energyStorage := section.FloatValue("energystorage", 0)
	metalStorage := section.FloatValue("metalstorage", 0)
	// The record's integer fields are not uniformly 32 bits, and a narrow store
	// is wrapped here once so the compiled definition already holds the value
	// retail's readers see — the same rule the weapon compiler follows. The
	// widening back to int32 is the extension that key's own readers apply, and
	// each is named below; widths and extensions are tabulated per key in
	// [02 R-KEYS-01 §5]. No stock definition authors a value outside its
	// field's range, so this is what third-party content sees, not a change to
	// the shipped catalog.
	makesMetal := int32(uint8(section.IntValue("makesmetal", 0))) // 8-bit store, read zero-extended [05 R-PROD-01 §1]
	buildTime := section.IntValue("buildtime", 0)                 // 32-bit store [02 R-KEYS-01 §5]
	// workertime is a 16-bit store its readers zero-extend before the
	// build-rate divide [05 "Construction arithmetic"].
	workerTime := int32(uint16(section.IntValue("workertime", 0)))
	// healtime is a 16-bit store its one reader zero-extends explicitly before
	// scaling it and dividing by the tick rate; the same reader first tests the
	// bare word for zero [02 R-KEYS-01 §5].
	healTime := int32(uint16(section.IntValue("healtime", 0)))
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
	// turnrate is a 16-bit store its reader zero-extends before the turn
	// arithmetic [04 R-MOV-01 §2].
	turnRate := int32(uint16(section.IntValue("turnrate", 0)))
	// waterline is an 8-bit store; every reader zero-extends it or masks the
	// result of byte arithmetic on it back to a byte [04 R-MOV-01 §9].
	waterline := int32(uint8(section.IntValue("waterline", 0)))
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
	// cruisealt is a 16-bit store, and it is the one in this group whose
	// readers SIGN-extend it — every flight-height reader does [04 R-AIR-01 §1].
	cruiseAlt := int32(int16(section.IntValue("cruisealt", 0)))
	// transportsize and transportcapacity are 8-bit stores; the capacity's
	// reader zero-extends it before the signed load-count compare [04 §10.2].
	transportSize := int32(uint8(section.IntValue("transportsize", 0)))
	transportCapacity := int32(uint8(section.IntValue("transportcapacity", 0)))
	// buildangle is a 16-bit store used as an UNSIGNED bound: its readers hand
	// the 16-bit word straight to the simulation-RNG sampler and halve it with
	// a logical shift [02 R-P28-ANG-01R §1].
	buildAngle := int32(uint16(section.IntValue("buildangle", 0)))
	// builddistance is a 16-bit store its reach test zero-extends
	// [05 "Construction arithmetic"].
	buildDistance := int32(uint16(section.IntValue("builddistance", 0)))
	// sortbias is a 16-bit store with no reader in the export beyond the
	// record copy, so only the store width is observable; the extension is
	// taken as unsigned with the rest of this group [02 R-KEYS-01 §5].
	sortBias := int32(uint16(section.IntValue("sortbias", 0)))
	// maneuverleashlength, attackrunlength and kamikazedistance are 16-bit
	// stores every reader zero-extends; the kamikaze one is additionally
	// compared unsigned against its floor [04 R-AIR-01 §9][04 R-SPEC-01 §1].
	maneuverLeashLength := int32(uint16(section.IntValue("maneuverleashlength", 0)))
	attackRunLength := int32(uint16(section.IntValue("attackrunlength", 0)))
	kamikazeDistance := int32(uint16(section.IntValue("kamikazedistance", 0)))
	footprintX := scratchMovement.FootprintX
	footprintZ := scratchMovement.FootprintZ

	// Combat and sensors [02 "Unit record"].
	maxDamage := section.IntValue("maxdamage", 0) // 32-bit store [02 R-KEYS-01 §5]
	// The three sensor ranges, both jammer ranges and the cloak radius are
	// six 16-bit stores, and every one of their readers SIGN-extends the word
	// before squaring or scaling it [03 R-VIS-01 §4][02 R-KEYS-01 §5].
	sightDistance := int32(int16(section.IntValue("sightdistance", 0)))
	radarDistance := int32(int16(section.IntValue("radardistance", 0)))
	sonarDistance := int32(int16(section.IntValue("sonardistance", 0)))
	radarDistanceJam := int32(int16(section.IntValue("radardistancejam", 0)))
	sonarDistanceJam := int32(int16(section.IntValue("sonardistancejam", 0)))
	minCloakDistance := int32(int16(section.IntValue("mincloakdistance", 0)))

	// Flags and postures — integer accessor default 0 booleans except standing orders default 2 [02 "Unit record"].
	standingMoveOrder := section.IntValue("standingmoveorder", 2)
	standingFireOrder := section.IntValue("standingfireorder", 2)
	initCloaked := storedFlag(section, "init_cloaked", false)
	downloadable := storedFlag(section, "downloadable", false)
	builder := storedFlag(section, "builder", false)
	stealth := storedFlag(section, "stealth", false)
	bmcode := uint8(section.IntValue("bmcode", 0))
	zbuffer := storedFlag(section, "zbuffer", false)
	isAirBase := storedFlag(section, "isairbase", false)
	isTargetingUpgrade := storedFlag(section, "istargetingupgrade", false)
	teleporter := storedFlag(section, "teleporter", false)
	hideDamage := storedFlag(section, "hidedamage", false)
	shootMe := storedFlag(section, "shootme", false)
	armoredState := storedFlag(section, "armoredstate", false)
	activateWhenBuilt := storedFlag(section, "activatewhenbuilt", false)
	canFly := storedFlag(section, "canfly", false)
	canHover := storedFlag(section, "canhover", false)
	upright := storedFlag(section, "upright", false)
	floater := storedFlag(section, "floater", false)
	amphibious := storedFlag(section, "amphibious", false)
	isFeature := storedFlag(section, "isfeature", false)
	noShadow := storedFlag(section, "noshadow", false)
	immuneToParalyzer := storedFlag(section, "immunetoparalyzer", false)
	hoverAttack := storedFlag(section, "hoverattack", false)
	antiWeapons := storedFlag(section, "antiweapons", false)
	digger := storedFlag(section, "digger", false)
	onOffable := storedFlag(section, "onoffable", false)
	mobileStandOrders := storedFlag(section, "mobilestandorders", false)
	fireStandOrders := storedFlag(section, "firestandorders", false)
	canStop := storedFlag(section, "canstop", false)
	canAttack := storedFlag(section, "canattack", false)
	canGuard := storedFlag(section, "canguard", false)
	canPatrol := storedFlag(section, "canpatrol", false)
	canMove := storedFlag(section, "canmove", false)
	canLoad := storedFlag(section, "canload", false)
	canReclamate := storedFlag(section, "canreclamate", false)
	canResurrect := storedFlag(section, "canresurrect", false)
	canCapture := storedFlag(section, "cancapture", false)
	canDGun := storedFlag(section, "candgun", false)
	kamikaze := storedFlag(section, "kamikaze", false)
	noRestrict := storedFlag(section, "norestrict", false)
	showPlayerName := storedFlag(section, "showplayername", false)
	commander := storedFlag(section, "commander", false)
	cantBeTransported := storedFlag(section, "cantbetransported", false)
	wacky := storedFlag(section, "wacky", false) // bit 16 same word as norestrict bit15 [02 "Unit record"]

	// Derived field, applied where retail applies it — in the tail of the
	// record compiler, after every key has been read, so the compiled
	// definition is what every consumer sees [02 "Unit record"] ("Two derived
	// fields the key table does not show"). The gate is the derived
	// cloak-capable bit, `cloakcost > 0` — strictly greater, so an authored
	// zero or negative clears it — and the substitution fires only when the
	// *stored* mincloakdistance is zero.
	//
	// One stock definition depends on it: a cloakable construction structure
	// authors `cloakcost` and omits `mincloakdistance`, so retail gives it a
	// proximity breach radius of 80 world units while an unsubstituted 0
	// leaves a `d² <= 0` test that never breaches [03 R-VIS-01 §4] pass 4.
	if cloakCost > 0 && minCloakDistance == 0 {
		minCloakDistance = 80
	}

	// Raw accessor for selfdestructcountdown so we can tell authored vs absent [02 "Unit record"].
	selfDestructCountdown, selfDestructCountdownPresent := section.RawValue("selfdestructcountdown")
	mobilityDomain := deriveMobilityDomain(bmcode != 0, canFly, movementClass)

	unknown := unitSourceUnknown(section, language)

	// The stored UnitName is the identity even when empty; retail does not
	// recover it from the discovered filename [02 R-CAT-01 §5][fmt fbi].
	canonical := CanonicalKey(unitName)

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

// unitSourceUnknown retains untyped source fields for diagnostics [02 §5] C14.
func unitSourceUnknown(section *formats.Section, language string) map[string]string {
	// Unknown inert keys retained [02 §5] C14.
	unknown := make(map[string]string)
	for _, item := range section.Items {
		if item.Kind != formats.Assignment {
			continue
		}
		fold := asciiFoldContent(item.Key)
		if _, ok := knownUnitKeys[fold]; ok {
			continue
		}
		// Language-prefixed variants for current language are already consumed via LanguageString — not unknown.
		if language != "" {
			lfold := asciiFoldContent(language + "name")
			if fold == lfold {
				continue
			}
			lfold = asciiFoldContent(language + "description")
			if fold == lfold {
				continue
			}
		}
		unknown[item.OriginalKey] = item.Value
	}
	if len(unknown) == 0 {
		unknown = nil
	}

	return unknown
}

// writeUnitCanonical renders a UnitDef's canonical byte form: fixed order,
// never ranging a map directly (I1) [02 §5] C12. Both the compile-time hash
// and the downloadable enforcement's re-hash use it so the two can never
// drift.
func writeUnitCanonical(u *UnitDef) []byte {
	var b strings.Builder
	if u.DiscoveryOnly {
		b.WriteString("discovery-only|")
	}
	fmt.Fprintf(&b, "%s|%d|%s|%s|%s|%s|%s|%s|%s|", u.CanonicalKey, u.UnitDefID, u.UnitName, u.Name, u.Description, u.Side, u.ObjectName, u.Category, u.SoundCategory)
	fmt.Fprintf(&b, "%s|", u.Corpse)
	fmt.Fprintf(&b, "%s|%d|%s|%s|%s|%s|%s|%s|", u.MovementClass, u.MobilityDomain, u.Weapon1, u.Weapon2, u.Weapon3, u.ExplodeAs, u.SelfDestructAs, u.YardMap)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|", u.MinWaterDepth, u.MaxWaterDepth, u.MaxSlope, u.BadSlope, u.MaxWaterSlope, u.BadWaterSlope)
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|", u.DefaultMissionType, u.BadTargetCategoryWPRI, u.BadTargetCategoryWSEC, u.BadTargetCategoryWSPE, u.NoChaseCategory)
	fmt.Fprintf(&b, "%s|", u.AIWeight)
	fmt.Fprintf(&b, "%.0f|%.0f|%.10f|%.10f|%.10f|%.10f|", u.BuildCostEnergy, u.BuildCostMetal, u.EnergyMake, u.EnergyUse, u.MetalMake, u.ExtractsMetal)
	fmt.Fprintf(&b, "%.10f|%.10f|%.10f|%.10f|%d|%d|%d|%d|%d|%d|", u.WindGenerator, u.TidalGenerator, u.EnergyStorage, u.MetalStorage, u.MakesMetal, u.BuildTime, u.WorkerTime, u.HealTime, u.CloakCost, u.CloakCostMoving)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|%d|%d|%d|%d|", u.MaxVelocity, u.BrakeRate, u.Acceleration, u.BankScale, u.PitchScale, u.DamageModifier, u.MoveRate1, u.MoveRate2, u.TurnRate, u.Waterline)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|%d|%d|%d|%d|", u.CruiseAlt, u.TransportSize, u.TransportCapacity, u.BuildAngle, u.BuildDistance, u.SortBias, u.ManeuverLeashLength, u.AttackRunLength, u.KamikazeDistance, u.FootprintX)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|%d|", u.FootprintZ, u.MaxDamage, u.SightDistance, u.RadarDistance, u.SonarDistance, u.RadarDistanceJam, u.SonarDistanceJam)
	fmt.Fprintf(&b, "%d|%d|%d|", u.MinCloakDistance, u.StandingMoveOrder, u.StandingFireOrder)
	writeFlags := func(flags ...bool) {
		for _, f := range flags {
			if f {
				b.WriteString("1|")
			} else {
				b.WriteString("0|")
			}
		}
	}
	writeFlags(u.InitCloaked, u.Downloadable, u.Builder, u.Stealth)
	// Preserve the byte in its existing canonical position; stock 0/1 hashes stay unchanged.
	fmt.Fprintf(&b, "%d|", u.BMCode)
	writeFlags(u.ZBuffer, u.IsAirBase, u.IsTargetingUpgrade, u.Teleporter, u.HideDamage, u.ShootMe, u.ArmoredState, u.ActivateWhenBuilt, u.CanFly, u.CanHover, u.Upright, u.Floater, u.Amphibious, u.IsFeature, u.NoShadow, u.ImmuneToParalyzer, u.HoverAttack, u.AntiWeapons, u.Digger, u.OnOffable, u.MobileStandOrders, u.FireStandOrders, u.CanStop, u.CanAttack, u.CanGuard, u.CanPatrol, u.CanMove, u.CanLoad, u.CanReclamate, u.CanResurrect, u.CanCapture, u.CanDGun, u.Kamikaze, u.NoRestrict, u.ShowPlayerName, u.Commander, u.CantBeTransported, u.Wacky)
	fmt.Fprintf(&b, "%s|%t|", u.SelfDestructCountdown, u.SelfDestructCountdownPresent)
	fmt.Fprintf(&b, "catmasks|")
	// Masks hash in their canonical 32-bit form, padded to the retail
	// registry width, so a definition's digest depends on which IDs are
	// members and not on the domain the catalog was compiled over [02 §5] C12.
	for _, m := range []CategoryMask{u.UnitMask, u.BadTargetCategoryWPRIMask, u.BadTargetCategoryWSECMask, u.BadTargetCategoryWSPEMask, u.NoChaseCategoryMask} {
		for _, word := range m.canonicalWords32(CategoryMaskWords) {
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
// It returns the first-equal name index; Compile retains all records for
// Catalog.UnitRecords [02 R-CAT-01 §5]. The helper uses only typed accessors
// from formats/tdf_typed.go [02 §4].
// Language-prefixed name fallback is applied via LanguageString (<Language>name then name) with
// Translate.tdf identity fallback (byte-exact) when the file is missing [02 §3] C7.
func CompileUnits(fs vfs.FSOps) (map[string]*UnitDef, error) {
	result, err := compileUnitsWithLanguage(fs, "")
	if err != nil {
		return nil, err
	}
	return result.units, nil
}

type unitCompileResult struct {
	units    map[string]*UnitDef
	records  []*UnitDef
	warnings []string
}

func compileUnitsWithLanguage(fs vfs.FSOps, language string) (unitCompileResult, error) {
	if fs == nil {
		return unitCompileResult{}, fmt.Errorf("content: nil VFS")
	}
	// Note: gamedata/translate.tdf is NOT loaded here. The unit name/
	// description fallback (<Language>name then name) is handled by
	// LanguageString [02 §3] C7; runtime-message translation belongs to
	// whichever later phase owns the configured language (phase 7), which
	// should load the table itself rather than re-parse or carry an unused
	// map through the catalog.

	entries, err := discoverUnitContent(fs)
	if err != nil {
		return unitCompileResult{}, fmt.Errorf("content: units: %w", err)
	}
	// ReadDir already sorts by Path [vfs.ReadDir], so iteration is stable (I1).
	records := make([]*UnitDef, len(entries))
	completed := true
	for i, entry := range entries {
		e := entry.info
		data, err := readContentEntry(fs, entry)
		if err != nil {
			return unitCompileResult{}, err
		}
		prov := ProvenanceFrom(e)
		doc, err := formats.ParseTDF(data)
		if err != nil {
			return unitCompileResult{}, formats.WithTDFContext(fs, err, e.Path)
		}
		// Each unit file contributes one UNITINFO section [02 "Unit record"].
		// A file without one aborts the whole discovery stage: the loader
		// fails at once and every file after it in enumeration order stays an
		// unparsed record that the compiler compacts out, so the catalog is
		// exactly the files that preceded it [02 R-CAT-01 §4]. The stock
		// corpus has no such file.
		unitSection := doc.Root.Section("UNITINFO")
		if unitSection == nil {
			completed = false
			break
		}
		// Nanolathe admits a unit definition whatever its authored Version
		// and Copyright text say. Retail drops a definition whose Version is
		// newer than 3.1 or whose copyright line does not match its template
		// [02 R-MALF-01 §5]; that gate is deliberately not implemented here
		// (DESIGN_CONTENT_VFS §5 "Unit admission (Nanolathe policy)").
		// The third retail drop gate is a separate rule and is retained:
		// unit content must come from a mounted archive, so a loose FBI
		// winner is parsed and then dropped [02 R-CAT-01 §4].
		if !entry.archive {
			continue
		}
		// Discovery initializes only the admission/display subset. Gameplay
		// defaults belong to the successful secondary parse [02 R-CAT-01 §§4–5].
		records[i] = compileUnitDiscovery(unitSection, language, prov)
	}
	// Completed discovery removes a dropped record by moving the last survivor
	// into its place. An early missing-UNITINFO return skips that epilogue;
	// only the compiler's stable compaction then runs [02 R-CAT-01 §§4–5].
	if completed {
		for i := len(records) - 1; i >= 0; i-- {
			if records[i] == nil {
				records[i] = records[len(records)-1]
				records = records[:len(records)-1]
			}
		}
	} else {
		kept := records[:0]
		for _, u := range records {
			if u != nil {
				kept = append(kept, u)
			}
		}
		records = kept
	}
	sortUnitRecords(records)
	var warnings []string
	for i, u := range records {
		u.UnitDefID = uint32(i + 1)
		warning, err := compileUnitSecondary(fs, u, language)
		if err != nil {
			return unitCompileResult{}, err
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
		u.Hash = HashDefinition(writeUnitCanonical(u))
	}
	return unitCompileResult{units: firstUnitNames(records), records: records, warnings: warnings}, nil
}

// linkUnitWeaponRecords resolves weapon1..3 (and explodeas/selfdestructas) after all weapons
// compile so enumeration order cannot leak into identity [02 §5] C1.
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
func linkUnitWeaponRecords(records []*UnitDef, weapons map[string]*WeaponDef) {
	if records == nil || weapons == nil {
		return
	}
	// Deterministic iteration follows retained record order (I1).
	// The miss policy lives in one place, Catalog.WeaponLink [02 §5
	// R-CONTENT-02]; a read-only view over the map is enough — the method
	// derives its slot-order scan and the record-0 lookup from Weapons alone.
	view := &Catalog{Weapons: weapons}
	for _, u := range records {
		if u.DiscoveryOnly {
			continue
		}
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
	applyMovementFootprintRecords(unitMapRecords(units), movement)
}

func applyMovementFootprintRecords(records []*UnitDef, movement map[string]*MovementClass) {
	for _, u := range records {
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
	return enforceDownloadableRecords(unitMapRecords(units), buildMenuNames)
}

func enforceDownloadableRecords(records []*UnitDef, buildMenuNames []string) []string {
	if records == nil || len(buildMenuNames) == 0 {
		return nil
	}
	// Canonicalize build menu names for case-insensitive comparison [02 "Unit record"].
	menuSet := make(map[string]struct{}, len(buildMenuNames))
	for _, n := range buildMenuNames {
		menuSet[CanonicalKey(n)] = struct{}{}
	}
	// Deterministic iteration follows retained record order (I1).
	var warnings []string
	for _, u := range records {
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
