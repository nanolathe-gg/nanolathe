# FBI — Unit Definitions (`.fbi`)

## Overview

Every unit is defined by one FBI file in `units/` — a text file in TDF
syntax ([tdf.md](tdf.md)) with a single `[UNITINFO]` section holding all of
the unit's stats, capabilities, and resource references. The filename
matches the unit's short name (`units/ARMFLASH.FBI` for `UnitName=ARMFLASH`).

An FBI ties the rest of the data together: it names the 3DO model
(`Objectname` → [3do.md](3do.md)), the corpse feature (`Corpse` →
[tdf.md](tdf.md) features), up to three weapons (`Weapon1..3` →
[tdf.md](tdf.md) weapons), a movement class (`MovementClass` →
`gamedata/MOVEINFO.TDF`), and a sound category (`SoundCategory` →
`gamedata/SOUND.TDF`). The COB script is *not* named here — the engine
loads `scripts/<UnitName>.COB` by convention.

## Format at a glance

Real example — the start of `units/ARMFLASH.FBI` (`totala1.hpi`):

```c
[UNITINFO]
	{
	UnitName=ARMFLASH;
	Version=1;
	Side=ARM;
	Objectname=ARMFLASH;
	Designation=FAT3;
	Name=Flash;
	Description=Fast Assault Tank;
	FootprintX=2;
	FootprintZ=2;
	BuildCostEnergy=870;
	BuildCostMetal=106;
	MaxDamage=625;
	MaxWaterDepth=12;
	MaxSlope=10;
	EnergyUse=0.5;
	BuildTime=1676;
	WorkerTime=0;
	BMcode=1;
	Builder=0;
	SightDistance=225;
	SoundCategory=ARM_TANK;
	Category=ARM TANK LEVEL1 WEAPON NOTAIR NOTSUB ;
	TEDClass=TANK;
	Corpse=armflash_dead;
	UnitNumber=32;
	MaxVelocity=2;
	TurnRate=475;
	MovementClass=TANKSH2;
	Weapon1=EMG;
	canmove=1;
	canattack=1;
	// ... localized names, order defaults, etc.
	}
```

## Which keys the engine actually reads

**Established:** key support comes from the unit parser and its consumers,
not merely a search for literal strings. The accessor/default/width table is
`[02 R-KEYS-01 §5]`; field effects belong to its cited behavioral sections.
A string census can miss dynamically constructed localized keys and short
strings. Presence establishes neither a consumer nor an effect.

The tables distinguish established parsed fields from bounded-negative reader
findings and retained unknown source fields. Counts describe the cited retail
sample, not all editions or mods.

### Authored by retail units, with no recovered runtime reader

The bounded unit-parser census finds no reader for these authored keys
`[02 §5]`. Preserve them when round-tripping; do
not give them behavior.

The language-prefixed `Name` and `Description` keys do not belong in this
table even though no literal `GermanName`-style string exists in the image: the
accessor builds the key at runtime from the configured language name, so the
whole-string census cannot see them and the engine honors them, as recorded in
the Identity table below. That covers the retail joke keys too —
`JapaneseName`, `PigLatinName` and `PigLatinDescription`, one unit each.

| Key | Retail units authoring it | Notes |
| --- | ---: | --- |
| `TEDClass` | 278 | Map-editor classification, as the name says. |
| `Threed` | 278 | — |
| `UnitNumber` | 278 | The engine does not key units by this number. |
| `Designation` | 274 | Cosmetic text that nothing displays. |
| `NoAutoFire` | 272 | Auto-fire comes from the standing fire order instead. |
| `Ovradjust` | 173 | Retained source field; intended meaning remains **Unknown** [02 R-P28-ANG-01R §1]. |
| `SteeringMode` | 152 | No engine steering distinction is selected by it. |
| `BadTargetCategory` (unprefixed) | 99 | Only `wpri_`, `wsec_` and `wspe_` prefixed spellings are read. `NoChaseCategory` *is* read unprefixed. |
| `Scale` | 28 | — |
| `AltFromSeaLevel` | 8 | `CruiseAlt` is the only altitude key read. |
| `TransportMaxUnits`, `TransMaxUnits` | 3, 1 | `TransportCapacity` and `TransportSize` are the read pair. |

`ArmorCategory` (and its `ArmorCategorie`/`ArmorCategories` variants), `Hover`,
`MetalUse` and `CanRepair` are third-party spellings: no shipped unit authors
them and no string for them exists in the executable either.

### Readable by the engine, but never authored by retail units

| Key | Notes |
| --- | --- |
| `armoredstate` | Parsed and stored, but the definition flag has no recovered reader. It does **not** initialize runtime armor; runtime armor starts clear and is controlled by COB `ARMORED` [06 R-DMG-01 §2]. |
| `wacky` | Parsed flag, default 0. The multiplayer restriction UI uses it for zero-valued restriction defaults/reset; no single-player gameplay effect is established [05 R-SHARE-01 §9][08 R-SKIR-01 §10]. |

### Exactly three weapon slots

`weapon1`, `weapon2`, and `weapon3` occur in the bounded key census. There is
no fourth slot in the traced unit compiler and weapon-slot runtime. Three is
the established slot count [02 R-KEYS-01 §5][06 §4.2].

## Field reference

**Established:** packed Boolean flags retain the integer accessor result
modulo 2: author `0`/`1`, because `2` clears a one-bit flag. Byte/word scalar
fields have their own narrowing; do not apply this rule to `BMcode` or other
whole-byte fields. Decimals use the accessor named for the key, so a decimal
in an integer field loses its fractional suffix. Defaults and narrowing are
listed in `[02 R-KEYS-01 §5]`. Unknown
or third-party keys should be preserved, not rejected — the engine ignores
what it doesn't know.

### Identity

| Key | Meaning |
| --- | --- |
| `UnitName` | Short name used as the canonical catalog identity and secondary resource basename, compared case-insensitively. Absent or empty stays empty; the discovered FBI filename is not a fallback (Established; see below). |
| `UnitNumber` | Numeric unit ID, documented as needing to be unique. The engine has no string for this key, so it is not how units are identified at runtime; the recording analyzer still uses the authored value as a candidate join for 0x09 unit type IDs and preserves duplicates or gaps as ambiguity. |
| `Version` | Always `1` in retail files. **Read by the catalog loader** (floating accessor, default 0): `major = low32(trunc64(floor(v)))`, `minor = low32(trunc64(floor((v − float64(major)) × 10)))`; the unit is kept when `major < 3`, or `major = 3` and `minor ≤ 1` (the executable's own version is 3.1); otherwise it is compacted out of the catalog. Unless another silent rejection suppresses the warning, the non-fatal `Error` box `Incompatible units found.  They will be ignored.  Please download the latest version of the game.` is shown once (`[02 R-MALF-01 §5]`). |
| `Side` | `ARM` or `CORE` |
| `Objectname` | 3DO model name in `objects3d/` (no extension) |
| `Designation` | Free-form designation string. Cosmetic, and the engine cannot read it. |
| `Name` | Display name |
| `Description` | Selection/tooltip description |
| `Copyright` | **Read by the catalog loader** (string accessor, 128 bytes): after the four characters at the year position are overwritten with `0000` the value must equal `Copyright 0000 Humongous Entertainment. All rights reserved.` byte for byte; a unit that fails is dropped from the catalog **silently** (and the same flag suppresses the `Incompatible units` box for that pass). The lore that units without the line fail to load is established, with the mechanism (`[02 R-MALF-01 §5]`). |
| `GermanName`, `FrenchDescription`, `SpanishName`, `ItalianDescription`, `JapaneseName`, `PigLatinName`, … | Localized `Name`/`Description` variants — the pattern is the language name directly followed by `Name` or `Description`. **The engine honors them** via the language-prefixed accessor, which builds the key from the configured language name (see the note above the inert-key table). |
| `TEDClass` | Editor classification: `TANK`, `KBOT`, `PLANT`, `VTOL`, `WATER`, `SPECIAL`, `FORT`, `METAL`, `ENERGY`, `COMMANDER`, `CNSTR` … The engine cannot read it, so it drives the map editor only, never AI or gameplay. |
| `Category` | Space-separated tag list (e.g. `ARM TANK LEVEL1 WEAPON NOTAIR NOTSUB`). Tags are matched by the per-slot `wpri_`/`wsec_`/`wspe_BadTargetCategory`, by `NoChaseCategory`, and by AI text files; tags need no central declaration. |
| `Downloadable` | Parsed flag, default 0. After menu compilation, a matching first-item product in a download file forces this flag on. That site formats `Hey!  Somebody forgot to set downloadable=1 for %s` without displaying it; it does not reject the unit for omitting the flag [02 R-CAT-01 §8]. |

### Construction (being built)

| Key | Meaning |
| --- | --- |
| `BuildCostEnergy`, `BuildCostMetal` | Total resource cost: integer accessor, default 0, narrowed to binary32 storage [02 R-KEYS-01 §5]. |
| `BuildTime` | Integer build-effort scale, default 0, retained as 32 bits. Work scheduling and division are established in [05 "Construction arithmetic"] and [05 R-WORK-01]. |
| `FootprintX`, `FootprintZ` | Occupied size in 16-pixel grid cells. Read by the **movement-class record reader** applied to the unit's own `[UNITINFO]` section — the same eight-key parser that reads `gamedata/MOVEINFO.TDF` classes — which runs for every unit whose `MovementClass` does not resolve; a unit with a resolved class takes its footprint from the class, not from these keys [02 §5 "Movement class record"]. |
| `YardMap` | Per-cell footprint map (see below) |
| `BuildAngle` | Authored integer read as a low-16-bit unsigned bound by the unit initializer; heading arithmetic and lifecycle are defined in [04 §2.3b] |
| `MaxDamage` | Hit points |
| `DamageModifier` | Fixed-point accessor, default 65,536 (1.0), retained as signed 16.16. Armored damage scaling and its amount gate are established in [06 R-DMG-01 §2]. |
| `HealTime` | Integer self-repair work parameter, default 0, low 16 bits. It is not an interval; cadence and work conversion are established in [05 R-WORK-01 §3]. |
| `ActivateWhenBuilt` | Unit starts activated |
| `norestrict` | Excluded from the multiplayer unit-restriction list |

`BuildAngle` is the unsigned bound consumed by the common unit-initialization
sampler — not a random yaw span around the requested build facing, the reading
community notes give it. The runtime heading arithmetic and lifecycle belong to
[04 §2.3b]; this format entry records only the field's authored type and
interpretation. **Established (bounded unit-parser and allocator trace):**
`Ovradjust` has no recovered runtime reader. Its original authoring purpose
remains **Unknown**; it supplies no overlap, heading, or geometry behavior.

#### YardMap

**Established:** the unit compiler reads at most 1,023 authored bytes and
allocates `FootprintX × FootprintZ` output cells only when `BMcode` is zero.
The exact character table is case-sensitive `[02 R-CAT-01 §5]`:

| Character | Cell byte | Character | Cell byte |
| --- | ---: | --- | ---: |
| `.` | 0x00 | `c` | 0x2D |
| `C` | 0x35 | `f` | 0x6F |
| `G` | 0x8F | `o` | 0x2F |
| `O` | 0x2B | `w` | 0x37 |
| `Y` | 0x31 | `y` | 0x29 |

Only listed characters consume cells. Spaces, newlines, lowercase `g`, and
all other unlisted characters are skipped. A final listed character repeats
until the footprint is full; excess characters after the final cell are
ignored. Thus `YardMap=o;` fills a larger footprint, and `ARMLAB`'s spaced
6×6 source has the same cells as its unspaced form.

**Established (malformed-source boundary):** there is no all-`o` absent-key
retail default. Empty text, or text ending in an unlisted character while
cells remain, makes the skip branch advance through the string terminator;
that branch neither consumes a cell nor checks the source end. A recognized
final character is different: its next-byte terminator check parks the
source and repeats that character. The output footprint bounds the number
of accepted cells, not how far an unsuccessful source scan can read.

**Established (Nanolathe host policy):** when the bounded source is exhausted,
`ParseYardMap` fills remaining cells with `o` and rejects nonpositive host
footprints. That preserves a blocking building footprint without reading
unowned memory; it is not a retail absent-key rule. **Unknown:** the bytes
beyond retail's exhausted source and the resulting cells or fault in a
particular run. They depend on temporary storage, not solely on FBI bytes,
so no file-format default can settle them. Cell-bit consumers, including
uppercase `G`'s vent
requirement and open-yard occupancy, belong to [05 "Geothermal requirement"]
and [04 §6.4]; the character names alone do not define those rules.

### Building others

| Key | Meaning |
| --- | --- |
| `Builder` | Can construct (`1` for factories, construction units, commander) |
| `BMcode` | `0` for structures, `1` for mobile units. Perfectly correlated with `YardMap` across the stock corpus: all 126 definitions with a yard map author `0`, all 152 without author `1` (see `docs/SPEC_CONFLICTS.md` SC21). It is not a factory marker — stock factories author `1` for `CanMove`. The engine copies "`BMcode` is zero" into the instance's structure-class bit at creation and reads it back for yard-map allocation, build-order placement, and the model shading gate |
| `WorkerTime` | Integer work parameter, default 0, low 16 bits. Work quantum and cadence are established in [05 "Construction arithmetic"] and [05 R-WORK-01]. |
| `Builddistance` | Build/repair reach in pixels (mobile builders) |
| `MetalMake` | Metal produced while active (also used by builders) |
| `CanCapture`, `CanReclamate` | Capture / reclaim abilities. `CanReclamate` is stored in capability bit 10, and the parser also writes capability bit 9 as a **copy of bit 10** while storing `CanResurrect` — no FBI key maps to bit 9 [02 R-KEYS-01 §1]. |
| `IsAirBase` | Repair-pad/carrier flag |
| `transportcapacity`, `transportsize`, `cantbetransported`, `canload` | Transport capacity and eligibility [04 R-AIR-01 §9]. `TransMaxUnits` and `TransportMaxUnits` have no recovered parser read; they are not aliases. |
| `teleporter` | Parsed flag with no recovered consumer; it does not establish a teleport ability [04 R-SPEC-01 §2]. |

### Resources

| Key | Meaning |
| --- | --- |
| `EnergyMake`, `EnergyUse` | Energy produced / consumed while active |
| `MetalMake` | Metal produced while active. Retail has no `MetalUse` string: metal upkeep is expressed as a negative `MetalMake`. |
| `EnergyStorage`, `MetalStorage` | Added storage capacity |
| `ExtractsMetal` | Extraction rate factor (mexes; multiplied by the deposit's `metal` value) |
| `MakesMetal` | Metal-maker flag |
| `WindGenerator` | Energy from wind (scaled by the map's wind speed) |
| `TidalGenerator` | Energy from tides (map `tidalstrength`) |
| `onoffable` | User can toggle active state |

### Movement

| Key | Meaning |
| --- | --- |
| `canmove`, `canpatrol`, `canstop`, `canguard` | Order availability flags |
| `MaxVelocity` | Top speed. Fixed-point accessor (`[02 "Typed accessors"]`): the authored decimal is multiplied by 65,536 and truncated toward zero, so the compiled field is **16.16 world units per tick** and needs no further scaling. Default 0. Commander 1.07, fast scout ~3 [04 §8.1 R-MOV-01 §1] |
| `Acceleration`, `BrakeRate` | Speed ramps, both fixed-point, so **16.16 world units per tick squared**, added to and subtracted from the scalar speed once per tick. Default 0 for both. `BrakeRate` is also the divisor of the braking-distance test and `TurnRate` the divisor of the turn-distance test, and neither division is zero-guarded, so a mobile unit that omits either key faults retail [04 §8.1 R-MOV-01 §4] |
| `TurnRate` | Turn speed in angular units (65536 = full circle) per tick. **Integer** accessor, stored into a 16-bit field that every reader zero-extends, so the effective domain is 0..65535 and an authored value is taken modulo 65,536. Default 0. The per-tick heading change is the signed heading error saturated at ±`TurnRate` [04 §8.1 R-MOV-01 §2] |
| `SteeringMode` | Turning style (tank skid vs. wheeled arcs; small int). 152 retail units author it, but the engine has no string for it, so it selects nothing. |
| `MovementClass` | Movement class name in `gamedata/MOVEINFO.TDF` (supplies footprint/slope/depth for pathing) |
| `MaxSlope` | Steepest passable slope. Like the footprint keys, `MaxSlope`, `BadSlope`, `MaxWaterSlope`, `BadWaterSlope`, `MaxWaterDepth` and `MinWaterDepth` are read from the FBI only by the movement-class record reader applied to the unit section when `MovementClass` is absent or unresolvable; the scratch record it fills starts from the 255/±10000 template and the unconditional clamps then run [02 §5 "Movement class record"][04 §6.1]. |
| `MaxWaterDepth`, `MinWaterDepth` | Water depth limits (ships set Min, subs/amphibians set Max high); read via the movement-class record reader as above. |
| `amphibious` | Can traverse underwater and land |
| `Floater` | Floats on water |
| `WaterLine` | **Integer** accessor, default 0, low 8 bits. Authored `0.3` converts to zero; no decimal draft is retained. Water-position consumers are [04 R-MOV-01 §9] and [04 §9.2]. |
| `Upright` | Keep model vertical on slopes (Kbots) |
| `maneuverleashlength` | How far it strays from orders when distracted |
| `MoveRate1`, `MoveRate2` | The two thresholds of the movement-tier classifier that raises the `MoveRate1/2/3` script callbacks — **not** plane-specific. Fixed-point accessor, so 16.16 world units per tick, each **defaulting to `MaxVelocity` shifted left one** (twice top speed). Tier 1 is `speed <= MoveRate1`, tier 2 is `MoveRate1 < speed <= MoveRate2`, tier 3 is above; since committed speed never exceeds `MaxVelocity`, a unit that authors neither key is always tier 1 [04 §5.2][04 §8.1 R-MOV-01 §6] |
| `canfly` | Aircraft flag |
| `canhover` | Hovercraft flag |
| `cruisealt` | Flight altitude |
| `altfromsealevel` | Nominally "altitude measured from sea level". The engine has no string for it; `cruisealt` is the only altitude key it reads. |
| `BankScale`, `PitchScale` | Fixed-point tilt factors, defaults 65,536 (1.0) and 0 respectively. Flight lean arithmetic is established in [04 R-AIR-01 §2]. |
| `Scale` | Model scale. The engine has no string for it, so it is inert. |
| `HoverAttack` | Aircraft hovers in place to attack (gunships) |
| `attackrunlength` | Bombing run length before release |
| `DefaultMissionType` | Initial standing mission (`Standby`, `VTOL_standby`, …) |

`canmove` is an order/UI capability, not a locomotion classifier. Every one
of the 21 stationary factory definitions in the retail corpus sets
`canmove=1` while also authoring `MaxVelocity=0` and no `MovementClass`; the
move order selects the rally point that newly completed units travel toward.
The factory itself does not move. Engine systems that need the physical
stationary/mobile distinction must therefore use actual locomotion data such
as `MaxVelocity`, not `canmove`.

This distinction is visible in presentation as well as simulation, but the
field that decides it is `BMcode`, not `canmove`. Retail runs its shaded piece
renderer only for `BMcode=0` definitions (and only while the `Shading` display
option is on); everything else is drawn by a second piece renderer that maps
textured faces with no `PALETTE.SHD` step. A zero-velocity factory authors
`canmove=1` and `BMcode=0`, so it is shaded through the vertex-interpolated
ramp described in [pal.md](pal.md) even though its FBI exposes the move
command and its COB animates doors, pads, and other pieces. Classifying that
unit as mobile — or gating the renderer on `canmove` or `MaxVelocity` —
removes its shading entirely. See
`research/retail-executable-spec/03` `[R-RND-02A]`.

### Combat

| Key | Meaning |
| --- | --- |
| `canattack` | Can be given attack orders |
| `Weapon1`, `Weapon2`, `Weapon3` | Weapon names ([tdf.md](tdf.md) weapons); map to the script's Primary/Secondary/Tertiary callbacks. Three is the hard limit: the executable contains `weapon1`, `weapon2` and `weapon3` and no `weapon4`. A 1998–2001 community unit-manager tool defensively parses `Weapon4`/`Weapon5`; those keys cannot reach the engine. |
| `NoAutoFire` | Documented as `1` = never auto-engages, and authored on 272 retail units, but the executable has no string for it. Standing fire orders carry that behavior instead. |
| `StandingFireOrder` | Initial fire order: 0 hold, 1 return, 2 fire at will |
| `StandingMoveOrder` | Initial move order: 0 hold position, 1 move, 2 roam |
| `firestandorders`, `mobilestandorders` | Whether those order toggles exist for the unit |
| `wpri_badTargetCategory`, `wsec_badTargetCategory`, `wspe_badTargetCategory` | Per-slot bad-target categories. All three spellings exist in the executable; retail content authors the first two (107 and 22 units). |
| `NoChaseCategory` | Never-chase categories, read unprefixed. |
| `BadTargetCategory` (unprefixed) | Authored on 99 retail units and **inert** — the executable has only the three prefixed spellings. |
| `antiweapons` | Enables coverage-ring presentation for interceptor slots; interception itself uses the weapon's `interceptor` flag [02 R-KEYS-01 §1]. |
| `CanDgun` | Has a D-gun |
| `kamikaze`, `kamikazedistance` | Self-destruct attack |
| `SelfDestructAs`, `ExplodeAs` | Weapon names for self-destruct and death explosions |
| `selfdestructcountdown` | Countdown seconds |
| `ShootMe` | `1` = the definition may be picked by another player's autonomous target search (dragon's teeth author 0). Parsed into word A bit 15 with a default of **0**, and read only by the shared target search, where it is one of three disjuncts — a computer-controlled shooter or a session option bit admits a candidate without it [04 R-SPEC-01 §5]. |
| `ImmuneToParalyzer` | EMP immunity |

**Established — category references:** the three prefixed bad-target fields
and `NoChaseCategory` resolve an ordinary category-registry name. A matching
unit name does not substitute that unit's identity mask; only authored category
membership populates the mask. Missing fields select `none`, while explicit
empty values select the ordinary empty-name registry entry. Later definitions
can contribute membership to an earlier field's selected category
[02 R-P0-03 §5].

### Sensors and stealth

| Key | Meaning |
| --- | --- |
| `SightDistance` | Integer sight radius, default 0, low 16 bits; LOS sampling and truncation are owned by [03 §3.2]. No universal 400-pixel format limit is established. |
| `RadarDistance`, `SonarDistance` | Radar/sonar radii |
| `RadarDistanceJam`, `SonarDistanceJam` | Jamming radii |
| `Stealth` | Invisible to radar/sonar |
| `CloakCost`, `CloakCostMoving`, `mincloakdistance`, `init_cloaked` | Cloaking energy costs, decloak radius, initial state |
| `istargetingupgrade` | Radar-targeting upgrade flag |
| `HideDamage` | Hide health bar from enemies (commanders) |
| `ShowPlayerName` | Parsed flag with no recovered reader [04 R-SPEC-01 §14]. |

### Miscellaneous

| Key | Meaning |
| --- | --- |
| `Commander` | Is a commander |
| `IsFeature` | Becomes its `Corpse` feature immediately when finished (dragon's teeth) |
| `digger` | Has underground pieces (pop-up guns). Concretely: it adds `+75` to every vertex's height key, and the unit image is then erased wherever the key is at or below `125` — i.e. everything at or below the model origin is cut away. Authored on exactly three stock units: `ARMAMB`, `CORTOAST`, `CORVIPE`. See `research/retail-executable-spec/03` `[R-REN-03A §8]`. |
| `NoShadow` | No cast shadow (ships) |
| `ZBuffer` | **Selects a per-pixel depth plane on the unit's offscreen composition image.** With it set the unit's image carries a second byte plane holding an interpolated height key, and every span writer admits a pixel only when `storedKey <= incomingKey`, so pieces resolve by world height rather than by draw order; with it clear the image is one plane and composition is pure painter order. 276 of the 278 stock units author `1`; `CORFAV` and `CORTRUCK` author `0`. See `research/retail-executable-spec/03` `[R-REN-03A §2]`. |
| `ThreeD` | Always `1`; the engine has no string for it. |
| `SoundCategory` | Category in `gamedata/SOUND.TDF` |
| `Corpse` | Feature left on death ([tdf.md](tdf.md)); chained via the feature's `featuredead` |
| `ai_limit`, `ai_weight` | AI directives stored as raw text. The two keys differ: `ai_weight` IS consumed — the strategic-AI pass parses its text with the profile grammar; `weight` directives reach the live per-unit-type weight array (default 100, clamped to 0..100) that scales build-candidate scores, and embedded `limit` directives are registered too. `ai_limit` has NO runtime reader — the live per-type limit array is populated only by the `ai/` profile parser's `limit` token, never by this key; do not treat `ai_limit` as the source of the retail candidate limit. |
| `Ovradjust` | Authored as `1` on 173 retail units. No runtime reader was found in the bounded census, so its semantics remain **Unknown**; the field is retained as authored data and no overlap, heading, or geometry behavior is assigned. |
| `sortbias` | Parsed into a signed 16-bit field and **never read** — reader census: none [04 R-SPEC-01 §7]. The census settles it as inert. |
| `armoredstate` | Parsed but unconsumed definition flag; never seeds runtime armor [06 R-DMG-01 §2]. |
| `wacky` | Parsed flag used by multiplayer restriction defaults/reset [05 R-SHARE-01 §9][08 R-SKIR-01 §10]; no single-player effect is established. |

## Implementation coverage

**Established — implementation inspection:** `internal/content/compile_unit.go`
applies typed reads, low-bit/byte/word narrowing, movement-class fallback and
source-field retention; `formats/tdf.go` owns the syntax. Known defaults are
listed in `[02 R-KEYS-01 §5]`, including `StandingFireOrder=2`,
`StandingMoveOrder=2`, `ShootMe=0` and `DamageModifier=1.0`. Authored zero does
not select an absent-key default.

The compiler retains `armoredstate` without seeding runtime armor, matching
`[06 R-DMG-01 §2]`. The implementation's `Wacky` field is retained without a
consumer; the documented retail consumers are in the multiplayer restriction
UI, outside the current single-player scope. Unknown source-field retention
must not imply that the engine acts on those values.

The catalog retains every compatible record in retail sort order, including
duplicate and empty names, and preserves the runtime name lookup. Construction,
model resources, cloning and category/profile linking retain record IDs.
The post-sort secondary FBI pass uses the stored name, preserving only the
established discovery-only fields (`side`, `ai_weight`, `ai_limit`, `wacky`
and catalog state). A missing, empty, unreadable or sectionless second file
keeps discovery-only values; it does not initialize gameplay defaults. A
successful second parse replaces other runtime fields even when keys are
absent. A changed `UnitName` does not trigger another sort; the name index
projects the unchanged lower-bound lookup, including its misses if the final
names no longer sort [02 R-CAT-01 §5]. Empty model/script identities use
`objects3d/.3do` and `scripts/.cob`. FBI, model and script resources replace
the last-period suffix of the assembled path before appending the requested
extension; the suffix scan does not stop at a directory separator. The AI
strategic planner's name-based candidate/count tables remain separate from
this loader and record-identity contract.

**Established — catalog admission:** loose FBI winners are silently skipped
before unit compilation `[02 R-CAT-01 §4]` / `docs/SPEC_CONFLICTS.md` SC24.
Archive-backed definitions also pass the version/copyright checks. The
version warning is suppressed when the same pass encounters a silent
rejection; it is not guaranteed for every rejected unit `[02 R-MALF-01 §5]`.

## Unknowns and caveats

- **Established (catalog identity and resource trace):** absent and empty
  `UnitName` both remain empty through discovery, compatible-record compaction,
  case-insensitive sorting and secondary FBI selection. The resource helper
  consequently probes `units/.FBI`. If that file exists and is nonempty, the
  ordinary second parser can reread its fields; otherwise the empty identity
  is retained and the later script path is
  `scripts/.COB`; it does not recover the discovered FBI filename. An absent
  `Objectname` copies the stored `UnitName`, so it yields `objects3d/.3DO` in
  this case. An explicitly empty `Objectname` stays empty even with a
  nonempty unit name; an explicitly nonempty model name remains usable.
  A missing model follows the ordinary fatal resource path. GUI probes use
  the stored name plus page number, beginning with `guis/0.GUI` for an empty
  name [02 R-CAT-01 §4], [02 R-CAT-01 §5]. Nanolathe likewise preserves the
  empty identity rather than synthesizing a filename stem.
- **Established (bounded duplicate-name behavior):** retail keeps compatible
  definitions as records and its name lookup selects the first equal record
  in the sorted range, excluding the sentinel. Empty is not a special
  lookup rejection. The bounded static trace also establishes the unstable
  partition/insertion sort and its equal-name swaps [02 R-CAT-01 §5]. An
  insertion-only range preserves equal records; a partitioned range can
  exchange them. Consumers must retain record identity instead of collapsing
  names or imposing a discovery-order winner.
- **Established (bounded negative):** neither unit parsing nor allocation
  reads `Ovradjust`; a case-insensitive executable string census also finds
  no spelling of that key. This closes the runtime-reader question for the
  inspected unit path. **Unknown:** the original producer's intended meaning;
  no period producer or authoring contract was inspected. Preserve its text
  without assigning behavior [02 R-P28-ANG-01R §1].
- **Established:** `BMcode`, `BankScale`, `PitchScale`, `BuildAngle`,
  `BuildTime`, `WorkerTime` and `DamageModifier` have traced contracts. They
  must not be listed as unresolved community guesses. `sortbias`,
  `teleporter`, `ShowPlayerName`, the definition `armoredstate`, and
  `ai_limit` are parsed without the effects their names suggest.
- **Established:** `ShootMe=0` does not universally forbid autonomous
  targeting; computer-shooter and session-option alternatives remain in the
  admission predicate [04 R-SPEC-01 §5].
- **Unknown:** run-specific output after a malformed yard source is exhausted.
  The unchecked scan and the separate bounded host policy are established in
  "YardMap" above; further source scanning cannot define the adjacent bytes.
- **Established:** no key names the COB script; `UnitName` selects
  `scripts/<name>.cob` through the catalog loader [02 R-CAT-01 §5].

## Sources

- *FBI File Content Description*, TA Design Guide — the variable catalog
  (originally from the TAORF tutorial):
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/fbidesc.htm>
- Verified against `units/ARMFLASH.FBI` and `units/ARMPW.FBI` from
  `totala1.hpi`, plus a key inventory of every retail FBI (815 unit records;
  278 winning files in the default mount) — the table above covers every key
  that occurs.
- The "which keys the engine reads" section is a whole-string census of
  `TotalA.exe` (GOG build, MD5 `8e74a1dffa1f5988624c52048f5b20cd`). It reports
  the presence or absence of literal key strings only, which is a fact about
  the data segment rather than about any code.

- Retail unit accessor/consumer audit: `[02 R-KEYS-01 §5]`, with runtime
  contracts in the cited categories. Literal-string presence is not the
  deciding evidence for field semantics.
