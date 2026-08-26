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

TDF parsing in the original engine is key-string driven: the parser is handed a
pointer to a literal key and returns the authored value or a default. So a
whole-string search of `TotalA.exe` settles, without any disassembly, which FBI
keys can possibly do anything. Match whole strings — `hover` is not read merely
because `canhover` exists.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### Authored by retail units, but not readable by the engine

These occur in the shipped FBIs and have no matching string anywhere in the
executable, so nothing can consume them. Preserve them when round-tripping; do
not give them behavior.

| Key | Retail units authoring it | Notes |
| --- | ---: | --- |
| `TEDClass` | 278 | Map-editor classification, as the name says. |
| `Threed` | 278 | — |
| `UnitNumber` | 278 | The engine does not key units by this number. |
| `Designation` | 274 | Cosmetic text that nothing displays. |
| `NoAutoFire` | 272 | Auto-fire comes from the standing fire order instead. |
| `Ovradjust` | 173 | — |
| `SteeringMode` | 152 | No engine steering distinction is selected by it. |
| `BadTargetCategory` (unprefixed) | 99 | Only `wpri_`, `wsec_` and `wspe_` prefixed spellings are read. `NoChaseCategory` *is* read unprefixed. |
| `GermanName`, `FrenchName`, `ItalianName`, `SpanishName` and the four matching `*Description` keys | 276–277 | Localization comes from `gamedata/Translate.tdf`, not the FBI. |
| `Scale` | 28 | — |
| `AltFromSeaLevel` | 8 | `CruiseAlt` is the only altitude key read. |
| `TransportMaxUnits`, `TransMaxUnits` | 3, 1 | `TransportCapacity` and `TransportSize` are the read pair. |
| `JapaneseName`, `PigLatinName`, `PigLatinDescription` | 1 each | Jokes. |

`ArmorCategory` (and its `ArmorCategorie`/`ArmorCategories` variants), `Hover`,
`MetalUse` and `CanRepair` are third-party spellings: no shipped unit authors
them and no string for them exists in the executable either.

### Readable by the engine, but never authored by retail units

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### Exactly three weapon slots

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

## Field reference

Booleans are `0`/`1`. Decimals are written plainly (`0.5`, `.4`). Unknown
or third-party keys should be preserved, not rejected — the engine ignores
what it doesn't know.

### Identity

| Key | Meaning |
| --- | --- |
| `UnitName` | Short name; canonical ID and filename stem. Case-insensitive. |
| `UnitNumber` | Numeric unit ID, documented as needing to be unique. The engine has no string for this key, so it is not how units are identified at runtime; the recording analyzer still uses the authored value as a candidate join for 0x09 unit type IDs and preserves duplicates or gaps as ambiguity. |
| `Version` | Always `1` |
| `Side` | `ARM` or `CORE` |
| `Objectname` | 3DO model name in `objects3d/` (no extension) |
| `Designation` | Free-form designation string. Cosmetic, and the engine cannot read it. |
| `Name` | Display name |
| `Description` | Selection/tooltip description |
| `Copyright` | Retail files require the exact Cavedog copyright string (lore: units failed to load without it) |
| `GermanName`, `FrenchDescription`, `SpanishName`, `ItalianDescription`, `JapaneseName`, `PigLatinName`, … | Localized `Name`/`Description` variants — the pattern is the language name directly followed by `Name` or `Description` |
| `TEDClass` | Editor classification: `TANK`, `KBOT`, `PLANT`, `VTOL`, `WATER`, `SPECIAL`, `FORT`, `METAL`, `ENERGY`, `COMMANDER`, `CNSTR` … The engine cannot read it, so it drives the map editor only, never AI or gameplay. |
| `Category` | Space-separated tag list (e.g. `ARM TANK LEVEL1 WEAPON NOTAIR NOTSUB`). Tags are matched by the per-slot `wpri_`/`wsec_`/`wspe_BadTargetCategory`, by `NoChaseCategory`, and by AI text files; tags need no central declaration. |
| `Downloadable` | Appears on add-on units. The engine reads it and carries the diagnostic `Hey!  Somebody forgot to set downloadable=1 for %s`, so it gates whether an add-on unit is accepted. |

### Construction (being built)

| Key | Meaning |
| --- | --- |
| `BuildCostEnergy`, `BuildCostMetal` | Total resource cost |
| `BuildTime` | Total build effort (divided by the builder's `WorkerTime` rate) |
| `FootprintX`, `FootprintZ` | Occupied size in 16-pixel grid cells |
| `YardMap` | Per-cell footprint map (see below) |
| `BuildAngle` | Total random yaw span around the requested build facing, in the native 65,536-units-per-turn heading domain; the exact sampling formula remains provisional |
| `MaxDamage` | Hit points |
| `DamageModifier` | Scale applied to incoming damage while the unit's script has put it in the armored state (COB `ARMORED`). See the note below. |
| `HealTime` | Self-heal interval (commanders) |
| `ActivateWhenBuilt` | Unit starts activated |
| `norestrict` | Excluded from the multiplayer unit-restriction list |

#### YardMap

`YardMap` refines a building's footprint cell-by-cell, `FootprintX` values
per row, `FootprintZ` rows, whitespace-separated groups (whitespace is
ignored — it's one flat cell list). Characters:

| Char | Meaning |
| --- | --- |
| `o` | occupied ground (default for all cells when no YardMap) |
| `O` | occupied only while the yard is open (documented as never used) |
| `c` | buildable "hole" — open when yard opens (factory exits), land |
| `C` | as `c` for water buildings |
| `w` | shipyard cells above water |
| `f` | occupied by a feature (dragon's teeth) |
| `g` / `G` | can sit over a geothermal vent (otherwise like `o`/`O`) |
| `y` | never occupied (land) — rounds off square footprints |
| `Y` | never occupied (water) |

Example (ARM Kbot Lab, 6×6): `YardMap=yoccoy ooccoo ooccoo ooccoo ooccoo
yoccoy;` — corners always open, center opens/closes with the unit's
`OpenYard()`/`CloseYard()` script. Buildings without yard scripts can just
use a single character (`YardMap=o;`).

### Building others

| Key | Meaning |
| --- | --- |
| `Builder` | Can construct (`1` for factories, construction units, commander) |
| `BMcode` | `0` for structures, `1` for mobile units. Perfectly correlated with `YardMap` across the stock corpus: all 126 definitions with a yard map author `0`, all 152 without author `1` (see `docs/SPEC_CONFLICTS.md` SC21). It is not a factory marker — stock factories author `1` for `CanMove` |
| `WorkerTime` | Nanolathe rate (build effort contributed per unit time) |
| `Builddistance` | Build/repair reach in pixels (mobile builders) |
| `MetalMake` | Metal produced while active (also used by builders) |
| `CanCapture`, `CanReclamate` | Capture / reclaim abilities |
| `IsAirBase` | Repair-pad/carrier flag |
| `TransMaxUnits` / `transportmaxunits` / `transportcapacity`, `transportsize`, `cantbetransported`, `canload` | Transport capacity and eligibility (`transportmaxunits` is a retail spelling variant; `canload` marks a unit able to load/carry other units, i.e. is itself a transport) |
| `teleporter` | Galactic gate flag |

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
| `MaxVelocity` | Top speed (pixels per tick-ish; Commander 1.07, fast scout ~3) |
| `Acceleration`, `BrakeRate` | Speed ramps |
| `TurnRate` | Turn speed in angular units (65536 = full circle) per tick |
| `SteeringMode` | Turning style (tank skid vs. wheeled arcs; small int). 152 retail units author it, but the engine has no string for it, so it selects nothing. |
| `MovementClass` | Movement class name in `gamedata/MOVEINFO.TDF` (supplies footprint/slope/depth for pathing) |
| `MaxSlope` | Steepest passable slope |
| `MaxWaterDepth`, `MinWaterDepth` | Water depth limits (ships set Min, subs/amphibians set Max high) |
| `amphibious` | Can traverse underwater and land |
| `Floater` | Floats on water |
| `WaterLine` | Non-negative decimal draft describing how deep the model sits in water (ships); retail data includes values such as `0.3` |
| `Upright` | Keep model vertical on slopes (Kbots) |
| `maneuverleashlength` | How far it strays from orders when distracted |
| `MoveRate1`, `MoveRate2` | Plane speed classes (8 for combat planes, 1 for transports); correspond to the `MoveRate1/2/3` script callbacks |
| `canfly` | Aircraft flag |
| `canhover` | Hovercraft flag |
| `cruisealt` | Flight altitude |
| `altfromsealevel` | Nominally "altitude measured from sea level". The engine has no string for it; `cruisealt` is the only altitude key it reads. |
| `BankScale`, `PitchScale` | Flight-model tilt factors; both are read by the engine. |
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

This distinction is visible in presentation as well as simulation. A
zero-velocity factory remains on the fixed-structure model-lighting path even
though its FBI exposes the move command and its COB animates doors, pads, and
other pieces. In retail, its shaded pieces use the signed, vertex-interpolated
`PALETTE.SHD` ramp described in [pal.md](pal.md); classifying the same unit as
mobile incorrectly replaces that gradient with mobile flat-face shading.

### Combat

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### Sensors and stealth

| Key | Meaning |
| --- | --- |
| `SightDistance` | Fog-of-war reveal radius in pixels (practical max ~400) |
| `RadarDistance`, `SonarDistance` | Radar/sonar radii |
| `RadarDistanceJam`, `SonarDistanceJam` | Jamming radii |
| `Stealth` | Invisible to radar/sonar |
| `CloakCost`, `CloakCostMoving`, `mincloakdistance`, `init_cloaked` | Cloaking energy costs, decloak radius, initial state |
| `istargetingupgrade` | Radar-targeting upgrade flag |
| `HideDamage` | Hide health bar from enemies (commanders) |
| `ShowPlayerName` | Show owner name as description (commanders) |

### Miscellaneous

**Publication omission:** Historical executable-analysis detail omitted from this public edition.

## Unknowns and caveats

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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
- OpenTA parser: `internal/content/compiler.go`.
