# TDF — Text Definition Files (`.tdf`)

## Overview

TDF is Total Annihilation's universal text data syntax: bracketed sections
containing `key=value;` assignments, optionally nested. The same syntax
underlies `.tdf` files everywhere (gamedata tables, weapons, features,
download menus), plus the role-specific formats documented
separately: unit definitions ([fbi.md](fbi.md)), map/mission metadata
([ota.md](ota.md)), and menu layouts ([gui.md](gui.md)).

This document covers the syntax itself and the following `.tdf` schema families:
`gamedata/`, `features/`, `weapons/`, `download/`, and `camps/useonly/`.

## Syntax

```c
// comment (C++ style, to end of line)
[SectionName]              // any text between brackets; case-insensitive
    {
    Key=Value;             // value runs to the ';', may be empty (key=;)
    Numbers=3500;          // integers, decimals (.4, 0.5), negatives
    List=2, 4, 6, 8, 10;   // comma lists — parsed by the consumer, not the syntax
    Words=ARM TANK LEVEL1; // space-separated word lists likewise
    [Nested]               // sections nest to any observed depth (3 in retail)
        { x1=132; y1=5; }
    }
```

**Established — parser rules:** `[02 §4]`, `[02 R-CAT-01 §3]` and
`[02 R-MALF-01 §4]` own the following syntax. Encoding and line-ending
observations are limited to the sampled retail files.

- Whitespace (spaces, tabs, newlines) between tokens is insignificant;
  multiple assignments may share a line.
- Section and key lookup is ASCII case-insensitive. Retail data mixes cases
  freely (`[UNITINFO]`, `[GlobalHeader]`, `canbuild1=`).
- Values run to the next `;`, including any intervening newlines. There is
  no quoting or escape mechanism. The parser trims space, tab, CR and LF
  from both ends of keys, values and section names; interior whitespace
  remains. A missing semicolon can therefore swallow later assignments.
- `/* ... */` block comments also appear in retail data
  (`weapons/WEAPONS.TDF`: `rendertype=4;	/* 2D bitmap */`), including
  inline after assignments; `//` comments may follow values on a line.
- Duplicate keys are governed by "Duplicate keys" below. Duplicate sibling
  section names are kept as separate nodes; a first-match section accessor
  returns the first.
- The sampled files use ASCII/Windows-1252; localized strings carry high-byte
  characters (`GermanDescription=Überschwerer...`). No BOM or embedded NUL was observed; those are corpus facts, not validation checks.
- Line endings are CRLF in retail data; accept any.

### Duplicate keys

**Established.** A section's key store is a vector kept in case-insensitive
order, and the parser builds it by **insertion in source order**, not by
sorting. Per assignment:

1. take the case-insensitive **lower bound** of the key over the vector — the
   first entry whose folded key is not less than the new key — the head of
   an equal-key run, when present;
2. compare the new spelling **byte for byte against the entry at that position
   only** — the head of the fold-equal run, never the rest of it;
3. equal spelling: the value is **replaced in place**, so the entry keeps the
   position the first occurrence gave it;
4. any other outcome: the entry is **inserted at the lower bound**, i.e. in
   front of every fold-equal entry already present.

Typed accessors take the same case-insensitive lower bound and read the entry
there, so **the variant an accessor returns is the last one parsed**. Two
consequences follow from step 2's narrow comparison:

- repeating one spelling with nothing in between is ordinary last-write-wins,
  one entry;
- but a spelling that a later case variant has pushed behind the run head is no
  longer what step 2 compares against, so a further assignment with that
  spelling is inserted rather than folded — **one spelling can hold two
  entries**.

**Established — corpus example:** twelve unit records in the cited 438-file
sweep author `MaxWaterDepth=0` followed by `maxwaterdepth=255`; the accessor
reads **255**. The aircraft water-floor consequence belongs to
`[04 R-AIR-01 §6a]`. This is a stock-content rule, not merely a third-party
compatibility edge.

### Typed values and defaults

**Established:** a default applies only when the key is absent. Present
`0`, empty text, or unparsable integer text produces zero rather than the
caller's nonzero default. For example, with integer default 7, absent `x`
returns 7 while `x=0;` and `x=junk;` return 0. Numeric accessors do not return
a separate found flag; string/raw accessors do. `[02 §4]`

Integer conversion accepts leading whitespace, an optional sign and decimal
digits, ignores trailing junk, and wraps to 32 bits. Fixed-point conversion
multiplies the floating value by 65,536 before truncation; its absent-key
default is already in stored units. A string destination of size N keeps at
most N−1 authored bytes plus a terminator. Localization first looks up
`<language><key>`, then the plain key `[02 §3]`.

There is no universal Boolean conversion: the accessor returns an integer and
the field's store decides its interpretation. In particular, packed unit and
weapon flags keep `value & 1`; authored 2 clears them. Use the per-family
accessor/default/width table `[02 R-KEYS-01 §5]` rather than treating every
nonzero number as true.

### Floating conversion

**Established — linked-runtime scanner, initial numeric locale.** Floating
reads skip space, tab, newline, vertical tab, form feed and carriage return,
then accept an optional sign, decimal digits with an optional decimal point,
and an optional exponent introduced by `e`, `E`, `d` or `D`. The exponent may
have one sign and must contain digits to take effect: `12e`, `12d-` and
`12E+oops` all produce 12. A sign without an exponent marker ends the number,
so `12-3` also produces 12. Trailing bytes are ignored. A mantissa without
any digits produces positive zero; `Inf` and `NaN` are not special tokens,
and hexadecimal notation is not recognized (`0x1p2` produces zero). The
initial decimal-point character is `.`; a comma ends the prefix [02 §4].

**Established — result boundary and range.** The linked conversion first
constructs a binary64 result, which the accessor returns at working precision.
Caller arithmetic can retain that wider working precision, but it starts from
the already-converted binary64 value. The accessor ignores conversion status:
overflow produces signed infinity; underflow can produce a subnormal or signed
zero. An all-zero mantissa remains signed zero even with a huge positive
exponent. Failed-text zero is positive, so `-junk` and `-0` differ in sign
[02 §4].

**Unknown — exact decimal rounding and locale lifetime.** The conversion uses
bounded significant-digit accumulation and intermediate decimal scaling before
binary64 finalization. Equivalence with Nanolathe's host converter for long
mantissas, rounding boundaries, or extreme exponents offset by very long
mantissas remains unverified. A bounded trace of those arithmetic helpers with
authored edge cases would settle it. The initial numeric locale is established;
whether retail ever selects an alternate decimal separator or extended-byte
classification needs a trace of locale selection. Nanolathe retains host
binary64 rounding and `.` as explicit placeholders for those residuals.

## Schema families

### `gamedata/` — global tables

The movement-class, side-data and sound-category keys the executable reads
are tabulated with their consumers in `[02 R-KEYS-01 §5]`.

**SIDEDATA.TDF** — the two sides and the initial build tree. `[SIDE0]` /
`[SIDE1]` define per-side identity and HUD layout, with nested rectangle
sections; the `[CANBUILD]` section lists what each stock unit can build.

```c
[SIDE0]
	{
	name=ARM;
	nameprefix=ARM;         // prefix for per-side GUI event names
	commander=ARMCOM;
	intgaf=ARMINT;          // in-game interface art GAF
	font=console;
	fontgui=armbutt;
	energycolor=208;        // palette indexes for HUD bars
	metalcolor=224;
	[LOGO]      { x1=132; y1=5; x2=152; y2=25; }
	[ENERGYBAR] { x1=471; y1=11; x2=592; y2=13; }
	// ... ~30 more named HUD rectangles
	}
[CANBUILD]
	{
	[ARMCOM]
		{
		canbuild1=ARMSOLAR;   // buttons 1..6 = first build page,
		canbuild2=ARMWIN;     // 7..12 = second page, etc.
		// ...
		}
	}
```

(Excerpt from the retail `gamedata/SIDEDATA.TDF`.) **Established:** the
resolved download-menu items append after the side `CANBUILD` list under
the documented count gate `[02 R-CAT-01 §5]` / `[02 R-CAT-01 §8]`.

**MOVEINFO.TDF** — movement classes referenced by FBI `MovementClass=`:

```c
[CLASS0]
	{
	Name=KBOTSS2;
	FootprintX=2;
	FootprintZ=2;
	MaxWaterDepth=12;
	MaxSlope=32;
	}
```

Sections are `[CLASS0]`, `[CLASS1]`, …; the `Name` value is what FBI files
reference (the engine interns each section's authored `Name` value into the
class record head and resolves the FBI `MovementClass` string against those
interned values, case-insensitively — confirmed by direct analysis). The
loader scans `CLASS0` through `CLASS31`, bounded by its 32-slot pool: a
missing section is skipped without stopping the scan. The
retail file has 15 classes and four further keys:

| Key | Classes | Meaning |
| --- | ---: | --- |
| `MinWaterDepth` | 5 | Minimum depth the class needs (ship classes) |
| `MaxWaterSlope` | 3 | Slope limit that applies over water, separately from `MaxSlope`. `TANKDH3` authors `30`; both hover classes author `255` alongside `MaxSlope=12`, which is what lets a hovercraft cross steep sea floor. **Initialization:** every class record is pre-filled at startup with `MaxSlope` = `BadSlope` = `MaxWaterSlope` = `BadWaterSlope` = 255 and `MaxWaterDepth`/`MinWaterDepth` = ±10000 before any parse, and the engine then runs three unconditional clamps (`MaxSlope = min(MaxSlope, MaxWaterSlope)`, then the two Bad values clamped to their Max counterparts). An omitted `MaxWaterSlope` therefore leaves `MaxSlope` at its authored value; the pool does **not** start zero-filled, so an omitted class does not compile to `MaxSlope = 0` `[04 §6.1 R-DOC04-A]`. |
| `BadSlope` | 2 | Hover classes only, `12`. **Established:** the movement classifier reads it as the clear-vs-steep boundary — slopes at or below `BadSlope` are clear, slopes between `BadSlope` and `MaxSlope` are the passable-but-penalized steep tier, slopes above `MaxSlope` are hard-blocked. |
| `BadWaterSlope` | 2 | Hover classes only, `255`. Same mechanism over water. |

**Established:** all four slope thresholds are live. The ordered reads and
clamps are `[04 §6.1 R-DOC04-A]`; classification and the clear/steep/blocked
outcomes are owned by doc 04's movement contracts. Nanolathe compiles them into
`content.MovementClass` and consumes them through `movement.Profile`.

**SOUND.TDF** — sound categories referenced by FBI `SoundCategory=`. Each
section maps event slots to WAV basenames (no extension) in `sounds/`:

```c
[ARM_TANK]
	{
	select1=tarmsel;
	ok1=tarmmove;
	arrived1=tarmst0p;
	cant1=cantdo4;
	underattack=warning1;
	count5=count1;      // self-destruct countdown, 5..0
	count4=count2;
	count3=count3;
	count2=count4;
	count1=count5;
	count0=count6;
	canceldestruct=cancel2;
	}
```

The complete slot set used across retail categories: `select1`, `ok1`,
`arrived1`, `cant1`, `underattack`, `count0`–`count5`, `canceldestruct`,
`activate`, `deactivate`, `build`, `repair`, `working`, `cloak`, `uncloak`,
`capture`, `unitcomplete`.

**ALLSOUND.TDF** — global UI/game event sounds: one section per event with a
single `sound=` key (`[ActivateAllStatBars] { sound=explode.wav; }`).

**METEOR.TDF** — `[Default]` section with the meteor-shower parameters used
when an OTA doesn't override them (`MeteorWeapon`, `MeteorRadius`,
`MeteorDensity`, `MeteorDuration`, `MeteorInterval`).

**HELP.TDF** — `[Help]` with `Line0=CTRL+A|Select all units;` … lines for
the F1 help screen (the `|` separates key from description).

**TRANSLATE.TDF** — one section per English string, keys are language names
(`German=`, `French=`, `piglatin=`…).

**LOS.TDF — Established:** `[TABLEINFO] numtables` selects `TABLE0` through
`TABLE<numtables-1>`. Each table's `numlines` selects `line0` onward; a line
stores a point count followed by that many coordinate pairs. `[03 R-VIS-01 §3]`
owns quadrant expansion, table selection and the ray walk. The declared count,
not the number of shipped sections, bounds selection.

**CATEGORY.TDF** — category names with `description=`; informational only
(categories used in FBI files do not need to appear here).

**BUILDINFO.TDF / UNITVIEW.TDF** — build-machine metadata and Unit Viewer
resources; no gameplay effect.

### `features/` — map features and corpses

The executable-read feature keys, with accessor, width, default and consumer,
are tabulated in `[02 R-KEYS-01 §5]`.

Feature definitions describe reclaimable/destructible map objects: trees,
rocks, metal deposits, geothermal vents, and unit corpses (`_dead` /
`_heap`). Files group many `[featurename]` sections. Real example — the ARM
Flash tank's corpse from `features/corpses/arm_corpses.tdf`, referenced by
`Corpse=armflash_dead;` in its FBI:

```c
[armflash_dead]
	{
	world=All Worlds;
	description=Wreckage;
	category=arm_corpses;
	object=armflash_dead;          // 3DO model in objects3d/
	featuredead=armflash_heap;     // next feature when destroyed
	footprintx=2;
	footprintz=2;
	height=20;
	blocking=1;
	hitdensity=100;
	metal=85;                      // reclaim yield
	damage=500;                    // hit points of the wreck
	reclaimable=1;
	featurereclamate=smudge01;     // feature left after reclaiming
	seqnamereclamate=tree1reclamate;
	}
```

Field reference (all optional unless the feature type needs them):

| Key | Meaning |
| --- | --- |
| `world` | World types the feature suits (editor filter, e.g. `All Worlds`). Authored on all 1,645 retail feature records and **inert** — the executable has no string for it. |
| `description` | Hover text (`Wreckage`) |
| `category` | Grouping (`arm_corpses`, `heaps`, `rocks`, `steamvents`, …) |
| `object` | 3DO model name for 3D features (corpses, rocks) |
| `filename` + `seqname` | For 2D (sprite) features: GAF file and entry name |
| `animating` / `animtrans` / `shadtrans` | Animation / transparency flags for sprite features |
| `seqnameshad` | Shadow sprite entry. The engine also reads `seqnamedieshad` and `seqnamereclamateshad`, the shadow companions of `seqnamedie` and `seqnamereclamate`. |
| `footprintx`, `footprintz` | Size in 16-pixel grid cells |
| `height` | Height for shot-over tests. Also the resurrection order's approach-phase draw bound: `y = terrainHeight(cell) + boundedDraw(height)` — the sole simulation-RNG draw resurrection consumes, in phase 1, not a placement effect `[05 R-WORK-01 §7]`. There is no separate authored "resurrection spread" or "jitter spread" key — `resurrectspread`, `jitterspread` and a bare `spread` are all absent from the retail feature parser's key census and from every stock feature section (census: 177 files, 1,645 sections, 41 distinct keys, none of the three) `[05 R-FEAT-01 §1]` |
| `blocking` | `1` = blocks unit movement |
| `hitdensity` | Community-understood as hit-probability weighting. Authored on all 1,645 retail records and **inert** — no string for it exists in the executable. |
| `damage` | HP before turning into `featuredead` (or vanishing) |
| `featuredead` | Feature this becomes when destroyed |
| `metal`, `energy` | Reclaim yield; for metal deposits `metal` is the extraction concentration (~0–255). Read as integers, masked to 16 bits, then stored as floats; a deposit's `metal` is copied (low byte) into every plot cell it covers when the deposit is also `indestructible=1` `[R-FEAT-01 §7]` |
| `reclaimable`, `autoreclaimable` | Can be reclaimed / is a candidate for area reclaim. `autoreclaimable` defaults to **1** and has no other reader `[R-FEAT-01 §6]` |
| `featurereclamate`, `seqnamereclamate` | Leftover feature and animation when reclaimed |
| `flamable`, `sparktime`, `spreadchance`, `burnweapon`, `featureburnt`, `seqnameburn`, `seqnameburnshad` | Fire behavior for burnable features. These are the keys the engine reads. `sparktime` is a float in **seconds**, stored as `trunc(sparktime × 30)` ticks (int16); the ignition countdown is `half + random(half)` visits with `half = ticks >> 1` `[R-FEAT-01 §9]`. A feature without `seqnameburn` can never ignite; `object` features never read the `seqname*` keys at all |
| `burnmin`, `burnmax` | Authored on 9 files (115 records) and **inert** — no string for either exists in the executable, so burn duration is not authored this way. |
| `geothermal` | `1` = geothermal plants can build here |
| `indestructible`, `nodisplayinfo`, `nodrawundergray` | Flags. `permanent` is authored but **inert** — no such key string exists in the executable (the only `Permanent` string is a lobby line-of-sight label). `sinktime` is likewise absent; sinking is a fixed rate `[R-FEAT-01 §13]` |
| `seqnamedie` | Animation/feature left when destroyed |
| `reproduce`, `reproducearea` | Growth mechanic, authored on 19 files (310 records). Both keys are read by the engine, so it is not unused. |

TNT maps reference features **by name** (see [tnt.md](tnt.md)); the engine
searches all loaded feature TDFs for the section, and reports a missing one
with `Record "%s" missing from feature files`.

A whole-string census of the retail executable finds one bounded feature-key
table. Alongside the keys it carries the literals `REUSE`,
`treeburn`, `Normal Features`, `Fortification`, `Fortification_Core`,
`DragonsTeeth` and `DragonsTeeth_Core`, which are feature names and category
labels the engine knows by name rather than by authored data. One shipped file
misspells the reclaim successor as `featurereclamamate`; the read spelling is
`featurereclamate`.

### `weapons/` — weapon definitions

Every weapon key the executable reads, with its accessor, stored width,
default and consumer section, is tabulated in `[02 R-KEYS-01 §5]`; the
`[DAMAGE]` block's construction (and the fact that a same-`ID` re-parse
*appends* to the earlier record's override table rather than replacing it)
is `[06 R-DMG-01 §1]`.

Weapon sections are referenced by name from FBI `Weapon1..3=` and from OTA
`MeteorWeapon=`. Retail data spreads them over `weapons/*.tdf` (WEAPONS,
LASERS, CANNONS, MISSILES, ROCKETS, UNITS, FIRES, METEORS…).

**Historical asset commentary.** `gamedata/WEAPONS.TDF` contains Cavedog's
field descriptions, but is not a compiled weapon-family input. **Established:**
retail and Nanolathe compile only the selected archive winners under
`weapons/*.tdf`; there is no gamedata fallback `[02 R-CONTENT-02]`. The
commentary is useful authoring context, while executable-owned field contracts
and the generated key table determine which keys are read and what they do.

Real example — the ARM Flash's gun from `weapons/WEAPONS.TDF`:

```c
[EMG]
	{
	ID=16;
	name=E.M.G.;
	rendertype=4;	/* 2D bitmap */
	color=2;		/* EMG bitmap shell, its a hack */
	lineofsight=1;
	turret=1;
	range=180;
	reloadtime=.4;
	weapontimer=1;
	weaponvelocity=300;
	sprayangle=1024;
	areaofeffect=8;
	burst=3;
	burstrate=.1;
	soundstart=armsml2;
	soundhit=lasrhit1;
	soundtrigger=1;
	tolerance=6000;
	startsmoke=1;
	explosiongaf=fx;
	explosionart=explode5;
	waterexplosiongaf=fx;
	waterexplosionart=h2oboom1;
	lavaexplosiongaf=fx;
	lavaexplosionart=lavasplashsm;
	[DAMAGE]
		{
		default=8;
		}
	}
```

Weapons fall into three basic categories (per the retail file's own
comment): ballistic (`ballistic=1`, arcing under gravity), line-of-sight
(`lineofsight=1`, straight), and dropped (`dropped=1`, bombs).

#### Which weapon keys the engine reads

A whole-string census of the retail executable finds one contiguous weapon-key
table alongside the `DAMAGE` subsection name and `default`. The following entries need special care:

| Key | Authored in | Status |
| --- | ---: | --- |
| `ID` | 71 files, 180 records | **Read, and it selects the record slot.** The weapon parser reads `ID` with the integer accessor and a default of −1 *first*; the authored value chooses which weapon record the parser fills, and the section name is then copied into that record as its catalog name (`name` is a separate 64-byte display string). `ID` is not inert, whatever the whole-string census suggests: that census misses very short strings (the same artifact that hid the textual `HAPI` magic), and the executable's weapon-key table does carry `ID`. An implementation that resolves weapons by section name only mis-assigns records whenever authored `ID` values differ from file order. There are 256 nominal slots, IDs 0–255, plus the shared unreachable scratch record selected by default ID −1. Other out-of-range values are unchecked malformed selections [02 R-MALF-01 §5]. |
| `aimrate` | 3 files | **Inert**, despite being documented in `gamedata/WEAPONS.TDF` itself. Nothing in the executable can read it. |
| `startfire` | 1 file | **Inert.** |

`weapontype2` likewise has no string and is not a retail key. Neither does
`movingaccuracy` or `noselfdamage`, both of which circulate in third-party
documentation.

**Established:** `shellweapon` is read even though the cited stock sample
never authors it. Per-shot costs use `energypershot` and `metalpershot`;
the historical comment's bare `energy` and `metal` are not established
weapon-cost aliases `[02 §5]`.

Behavior flags use the integer accessor, default zero, with only the low bit
retained. Their consumers and the stored field widths belong to
`[02 R-KEYS-01 §5]`; the packed runtime representation is not an on-disk TDF
layout. `noautorange` is an established parsed flag, with lifetime behavior
owned by `[06 §7.3]`.

| Key | Meaning |
| --- | --- |
| `ID` | Numeric weapon ID. **Read by the engine and used to select the weapon record slot** (default −1 = the slot before the table); the section name becomes the record's catalog name. See the `ID` row of the census table above. |
| `name` | Display name |
| `rendertype` | Projectile rendering: 0 laser, 1 3D model, 2 not rendered, 3 dgun, 4 plasma/bitmap shell, 5 flame, 6 bomb, 7 lightning |
| `model` | 3DO model (rendertype 1), no extension |
| `color`, `color2` | Palette indexes for beam/shell colors |
| `range` | Range in map pixels |
| `reloadtime` | Seconds between shots/bursts (decimal) |
| `burst`, `burstrate` | Shots per burst and intra-burst delay |
| `weaponvelocity`, `startvelocity` | Authored world units/second; floating accessor, default 0, scaled by 65,536/30 before truncation to stored 16.16 units/tick [02 §5]. |
| `weaponacceleration` | Authored world units/second squared; floating accessor, default 0, scaled by 65,536/900 before truncation [02 §5]. |
| `weapontimer` | Projectile lifetime in seconds (0 = computed for ballistic) |
| `duration` | Beam length/time for beam weapons |
| `beamweapon` | `1` = laser-style beam |
| `ballistic` / `lineofsight` / `dropped` | Category flags |
| `guidance`, `tracks`, `turnrate` | Homing behavior; `turnrate` in angular units (65536 = circle)/sec |
| `cruise`, `vlaunch`, `twophase`, `flighttime` | Vertical-launch / two-phase missiles (nukes, starbursts). `weapontype2` circulates in third-party docs and is not a retail key. |
| `accuracy`, `tolerance`, `pitchtolerance` | Aiming: `tolerance` is how far off-aim firing is allowed (angular units) |
| `sprayangle` | Random spread (angular units) for burst weapons |
| `areaofeffect` | Splash diameter in pixels |
| `edgeeffectiveness` | Damage fraction at splash edge (0–1) |
| `energypershot`, `metalpershot` | **Established:** firing costs `[02 §5]`. The historical asset commentary mentions bare `energy`/`metal`; Nanolathe does not treat them as aliases for these parsed fields. |
| `commandfire` | Requires explicit user fire order (D-gun, nukes) |
| `toairweapon` | Weapon only engages air targets (anti-air missiles; retail key, undocumented historically) |
| `holdtime` | Follow-camera hold, in whole simulation ticks (authored in seconds, multiplied by 30 and truncated with the other time-valued weapon keys). When the projectile the camera is following retires, the camera freezes on that projectile's last point and stays there for `holdtime` ticks before resuming ordinary following. It has no projectile-motion effect at all: every reader is a projectile-retirement path that loads the camera hold counter, not motion code. Established; the engine side is `[06 §7.3]`, the camera side `[07 §10]`. |
| `turret` | Weapon must be deployed from a mount with 360° rotation and pitch (143 retail weapons) |
| `coverage` | "What the protection umbrella is for weapons that shoot other weapons" — the interceptor's protected radius |
| `minbarrelangle` | Lowest angle in degrees the barrels can point, used in the ballistic solution |
| `aimrate` | Documented in `gamedata/WEAPONS.TDF` as average aiming speed in 64K degrees per second, and inert — the executable has no string for it. |
| `propeller` | The weapon's model has a propeller that spins |
| `startfire` | Authored once in the retail corpus, and inert. |
| `stockpile`, `targetable` | Stockpiled weapon; can be intercepted |
| `interceptor` | Retail interceptor weapon selector; the runtime reserves one legal stockpiled slot per targetable projectile |
| `paralyzer` | Stuns instead of damages (damage value = stun ticks) |
| `noautorange`, `noexplode`, `burnblow`, `groundbounce`, `selfprop`, `waterweapon`, `unitsonly`, `noradar` | Projectile behavior flags |
| `firestarter` | % chance to ignite flammable features |
| `smoketrail`, `smokedelay`, `startsmoke`, `endsmoke` | Smoke visuals |
| `soundstart`, `soundhit`, `soundwater`, `soundtrigger` | WAV basenames; `soundtrigger=1` plays per burst shot |
| `explosiongaf`/`explosionart` (+ `water…`, `lava…` variants) | Impact animation: GAF file basename + entry name |
| `shakemagnitude`, `shakeduration` | Screen shake |
| `randomdecay` | Floating seconds, default 0, multiplied by 30 and truncated into a 16-bit tick value. The centered burst-clone expiry jitter is established in [06 §4.3]. |
| `meteor` | Marks the meteor weapon |
| `[DAMAGE]` | Subsection: `default=` damage per hit, plus per-unit-name overrides (`corkrog=2460;`) |

**Established:** render fields remain compiled data. Projectile sprite selection
and frame progression belong to `[03 §5.4]` and `[06 R-WFX-01 §1]`; impact
smoke belongs to `[03 §5.5]`. This format document does not prescribe a second
EMG frame-selection or impact-smoke rule. The older
`WEAPON-VISUAL-EMG-CANNONSHELL-001` and `WEAPON-VISUAL-IMPACT-SMOKE-001` labels
are historical references, superseded by those owning contracts.

### `download/` — build-menu placement

Add-on units (and expansion units) attach themselves to construction menus
with `download/<unit>.tdf`. Real example — `download/ARMAMB.TDF` from
`CCDATA.CCX`:

```c
[MENUENTRY1]
	{
	UNITMENU=ARMACK;    // the builder whose menu gains a button
	MENU=3;             // menu page; page numbering starts at 2 (!)
	BUTTON=5;           // button slot 0..5 on that page
	UNITNAME=ARMAMB;    // the unit to build
	}
[MENUENTRY2]
	{
	UNITMENU=ARMACV;
	MENU=3;
	BUTTON=5;
	UNITNAME=ARMAMB;
	}
```

**Established:** sections are visited in file order; their names are not
parsed as ordinals. Stock authors `[MENUENTRY1]`, `[MENUENTRY2]`, … one per
builder/menu/button placement. `MENU` and `BUTTON` use the integer accessor
and retain the low byte; `UNITMENU` and `UNITNAME` use 32-byte string reads
`[02 R-CAT-01 §8]`. The first visible build page is `MENU=2` (page numbering is
off-by-one); two units claiming the same builder/page/button conflict —
only one appears.

### `camps/useonly/` — mission unit restrictions

A mission's OTA can set `useonlyunits=<name>.tdf;` referring to
`camps/useonly/<name>.tdf`, which whitelists buildable units as empty
sections:

```c
[ARMACSUB]
	{
	}
[ARMASON]
	{
	}
```

**Established:** mission restriction loading clears eligibility and re-enables
the listed unit names before the battle-entry catalog compaction. This changes
the available definitions, not merely button color `[08 R-ENTRY-01 §2]`.

### Other TDF-syntax files

- `maps/*.ota` — [ota.md](ota.md)
- `units/*.fbi` — [fbi.md](fbi.md)
- `guis/*.gui` — [gui.md](gui.md)
- `maps/MULTIPLAY.TDF`, campaign definitions under `camps/` — mission
  sequencing (`tdfcamp` schema: campaign entries pointing at OTA missions,
  briefing text/sounds).
- `ai/*.txt` — AI profiles referenced by OTA `aiprofile=`; plain text,
  not TDF.

## How the engine parses it, and what a malformed file does

Owned by `[02 §4]` and `[02 R-MALF-01 §4]`; the byte-level facts:

- Comments are blanked to spaces first, length preserved: `//` to the end of
  the line (newline kept), `/* … */` inclusive, an unterminated `/*` to the
  end of the text.
- The parser then walks the text: `[` needs a `]` somewhere after it and,
  after whitespace, a `{`; `}` closes the section (a `}` at the top level
  **ends the parse** and the rest of the file is ignored); anything else
  starts a `key = value ;` field whose `=` and `;` are found by scanning
  forward to the end of the text.
- Every failure — `Data field - '=' not found`, `Data field - ';' not
  found`, `Sub-record - closing ']' not found`, `Sub-record - opening '{'
  not found`, `End of file - nextblock not zero` (end of text inside an
  open section) — is shown as `Parse error in .TDF File! <diagnostic> -
  name = '<section>' from file <path>` in a system-modal box and the
  process exits with code 1. There is no recovery and no partial tree.
- A missing file, or one of zero length, is the only recoverable failure:
  the loader returns no tree and typed reads return their defaults.
- Numeric text: the integer accessor wraps modulo 2³² (no overflow test);
  the fixed-point accessor multiplies by 65,536, truncates to signed 64 bits,
  and retains the low 32 bits. Finite signed-32 overflow wraps; non-finite
  or signed-64 overflow produces a zero low word [01 R-DET-01 §1]
  [02 R-MALF-01 §4, RT-01]. The floating accessor is the C-runtime decimal
  conversion.

## Unknowns and caveats

- **Established:** the grammar is traced, not merely inferred from corpus
  style. `}` closes a section; it does not consume a following `;`. Do not
  prescribe `};` as an optional terminator. Comments are blanked before parsing
  even inside would-be values. Duplicate sections remain in source order;
  duplicate-key lookup follows the insertion rule above.
- **Established:** `randomdecay` and the scalar conversions are documented in
  `[02 §5]` / `[06 §4.3]`. Historical weapon commentary is authoring context,
  not proof a key is live or its prose matches the traced consumer.
- `MOVEINFO.TDF`'s `BadSlope`/`BadWaterSlope` pair is authored only by the
  two hover classes; both keys are read by the engine along with
  `MaxWaterSlope`, as the clear-vs-steep boundary of the movement classifier
  (see the table above).
  The third-party controller keys `pivotturn`, `reverse`, `arcturn`,
  `minturnradius` and `minturnspeed` have no strings in the executable.
- Retail authors `featurereclamamate` (a typo for `featurereclamate`) ten
  times in `features/acid/acidplants.tdf`. **Established:** retail and
  Nanolathe read only `featurereclamate`; the typo is retained as inert source
  data and those records receive no reclaim successor from it `[02 §5]`.
- **Established:** side `CANBUILD` lists are populated first, then resolved
  download-menu items append under the count gate `[02 R-CAT-01 §5]`
  `[02 R-CAT-01 §8]`.
- **Established:** `LOS.TDF` declares `numtables=9` while containing 12 table
  sections; only the declared range is selected `[03 R-VIS-01 §3]`.

- **Unknown:** exact long-mantissa floating rounding, extreme exponent
  cancellation and alternate numeric-locale selection remain open under
  "Floating conversion" above. Exponent spelling, ordinary malformed-prefix
  handling, overflow result selection and signed-zero behavior are established;
  ordinary host tests do not prove the remaining arithmetic edges.
- **Established — evidence limit:** a literal-string census alone cannot
  prove a key unreadable. Language-prefixed keys and numbered keys are built
  dynamically, and a minimum-length string export can omit short keys such
  as `ID`. Negative key claims require the relevant parser/caller census.

## Implementation coverage

**Established — implementation inspection:** `formats/tdf.go` preserves source
items and builds a separate resolved key view; it implements comment blanking,
trimming, multiline values, first-match sibling lookup and the duplicate-key
rule. It additionally accepts an adjacent semicolon after `}`; that host
extension is not part of the traced retail grammar. `formats/tdf_typed.go`
applies absent-only defaults and the established floating-prefix/range rules;
`formats/tdf_typed_test.go` locks alternate exponents, incomplete exponents,
ASCII whitespace, overflow and signed zero. Its `BoolValue` helper tests nonzero, so packed catalog flags instead use their field-specific
low-bit stores. Parse size/depth limits and returned Go errors are host safety
policy; retail's malformed-input path is fatal. Source-item preservation does
not mean byte-for-byte preservation of comments and outer whitespace.

## Sources

- **`gamedata/WEAPONS.TDF` (from `totala1.hpi`)** — Cavedog's own comment
  header supplies historical authoring descriptions. Executable-owned
  contracts `[02 §5]` / `[02 R-KEYS-01 §5]` determine live keys, defaults and
  conversions when that commentary disagrees.
- TA Design Guide pages, at
  `https://units.tauniverse.com/tutorials/tadesign/tadesign/<page>`
  (also mirrored under
  `https://files.tauniverse.com/files/ta/resources/tutorials/ta-design-guide/browse-online/tadesign/`):
  *Gamedata TDF Information* (`tdfgdata.htm`), *Features TDF Information*
  (`tdffeat.htm`), *Weapon TDF Information* (`tdfweapon.htm`),
  *Download TDF Information* (`tdfdown.htm`), *Useonly TDF Information*
  (`tdfuonly.htm`), *Campaign TDF Information* (`tdfcamp.htm`).
- Examples verified against `gamedata/SIDEDATA.TDF`,
  `gamedata/MOVEINFO.TDF`, `gamedata/SOUND.TDF`,
  `features/corpses/arm_corpses.tdf`, `weapons/WEAPONS.TDF` from
  `totala1.hpi` and `download/ARMAMB.TDF` from `CCDATA.CCX`.
- Nanolathe implementation: `formats/tdf.go` and `formats/tdf_typed.go`
  parse text; `internal/content/compile_*.go` compiles the family records.
  Behavioral precedence remains with `research/retail-executable-spec`.
