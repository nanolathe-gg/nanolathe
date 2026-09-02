# OTA — Map and Mission Metadata (`.ota`)

## Overview

A Total Annihilation map is a pair of files in `maps/` with the same
basename: a binary `.tnt` with the terrain ([tnt.md](tnt.md)) and a text
`.ota` with everything else — the map's name, environment values (wind,
tides, gravity, water), starting positions, and for single-player missions
the placed units, features, victory conditions, and each unit's scripted
orders.

OTA files use the TDF syntax ([tdf.md](tdf.md)). The top-level section is
`[GlobalHeader]`, containing global properties and one or more
`[Schema N]` subsections. A **schema** is a variant of the map: multiplayer
maps have one or more schemas of type `Network 1` … `Network 4` (selectable
variants of the same terrain — different metal/wind/features); missions
typically have three (`Easy`, `Medium`, `Hard`).

## Format at a glance

Real example — the start of `maps/The Pass.ota` (`totala2.hpi`):

```c
[GlobalHeader]
	{
	missionname=The Pass;
	missiondescription=7 X 4  Fight to control a winding mountian pass.;
	planet=Green planet;
	missionhint=;
	brief=;
	narration=;
	glamour=;
	lineofsight=0;
	mapping=0;
	tidalstrength=0;
	solarstrength=20;
	lavaworld=0;
	killmul=50;
	timemul=0;
	minwindspeed=100;
	maxwindspeed=3000;
	gravity=112;
	numplayers=2;
	size=7 x 4;
	memory=16 mb;
	useonlyunits=The Pass.tdf;
	SCHEMACOUNT=1;
	[Schema 0]
		{
		Type=Network 1;
		aiprofile=;
		SurfaceMetal=3;
		MohoMetal=30;
		HumanMetal=1000;
		ComputerMetal=1000;
		HumanEnergy=1000;
		ComputerEnergy=1000;
		MeteorWeapon=;
		MeteorRadius=0;
		MeteorDensity=0;
		MeteorDuration=0;
		MeteorInterval=0;
		[specials]
			{
			[special0]
				{
				specialwhat=StartPos1;
				XPos=560;
				ZPos=240;
				}
			// ... StartPos2 ...
			}
		}
	}
```

Structure:

```
[GlobalHeader]
 ├─ global keys (name, planet, environment, size, ...)
 └─ [Schema 0], [Schema 1], ... (probed upward until missing)
     ├─ schema keys (Type, aiprofile, metal/energy, meteor settings)
     ├─ [units]    { [unit0]    { Unitname/XPos/ZPos/Player/InitialMission ... } ... }
     ├─ [features] { [feature0] { Featurename/XPos/ZPos } ... }
     └─ [specials] { [special0] { specialwhat=StartPosN; XPos; ZPos } ... }
```

## Reference

### Coordinates and size

`XPos`/`ZPos` are map pixels: X eastward, Z southward (see conventions in
[README.md](README.md)). `size=7 x 4;` is in 512-pixel squares — The Pass's
TNT is 3584×1632 pixels, i.e. 7×~3.2 (the value is informational, shown in
map selection, and rounded). `YPos` appears on units and stores the
terrain height at the unit's position as authored (values like `85`, `45`;
~17% of retail placements say `0`) — the engine re-derives height from the
terrain, so readers can ignore it.

### Global keys (`[GlobalHeader]`)

| Key | Meaning |
| --- | --- |
| `missionname` | Display name as authored by editors. **Inert in the OTA**: the executable's two readers of this key read the campaign file's `MISSION<n>` section; a skirmish map's display name is its file name (translated) [02 R-MAP-01 §3], [02 R-MAP-01 §9] |
| `missiondescription` | Description shown in selection screens (default `No description available`; lower-cased and passed through the translation table, original spelling kept when no translation exists) [02 R-MAP-01 §3] |
| `planet` | Planet type string (selects the briefing globe art). The complete retail vocabulary (census of all 272 retail OTAs): `Green planet`, `Red Planet`, `Lava`, `Metal`, `Ice`, `Lush`, `Archipelago`, `Slate`, `Lunar`, `Water World`, `Wet Desert`, `Acid`, `Crystal`, `Desert`, `Urban`, and empty. Unrecognized values fall back silently. |
| `missionhint` | Hint text (missions). Parsed into a `camps\hints\<hint>.TXT` path that **nothing reads** — inert [02 R-MAP-01 §1] |
| `brief` | TXT basename in `camps/briefs/` with the mission briefing text |
| `narration` | WAV basename in `camps/briefs/` narrating the briefing |
| `glamour` | PCX basename; the loader stores `\<glamour>.PCX` and the glamour display rebuilds `bitmaps\glamour\<glamour>.PCX` from it [02 R-MAP-01 §1] |
| `glamoursound` | WAV played over the glamour shot |
| `lineofsight` | `1` = true line-of-sight rules, `0` = off |
| `mapping` | `1` = normal fog-of-war mapping rules |
| `tidalstrength` | Energy per tidal generator |
| `solarstrength` | Nominally energy per solar collector. Authored by all 275 retail maps and **inert**: the executable has no string for it, so a solar collector's output is its own `EnergyMake` and the map cannot scale it. |
| `lavaworld` | `1` = "water" is lava (affects effects/pathing visuals) |
| `waterdoesdamage`, `waterdamage` | Acid water: flag and damage rate |
| `nosealeveltrigger` | Non-zero makes water opaque to projectiles and debris: the submerged-impact and debris-water tests return early [06 §8.2], [04 R-COB-04 §2]. Unrelated to the sea level value, which has no OTA key [02 R-MAP-01 §3] |
| `killmul` | Kill score multiplier, default `0.0`. Parsed to a float and read by the end-of-battle score helper — `Score += __ftol(Kills × killmul)`, truncated separately from the time term [08 R-CAMP-01 §7]. Correction, `[08 R-CAMP-01 §11]`: this row previously read "Nominally a kill score multiplier. Parsed to a float and **inert** (reader census: none) [02 R-MAP-01 §3]"; that census missed the score helper. |
| `timemul` | Time score multiplier, default `0.0`; three retail campaign maps author `-1`. Parsed to a float and read by the same score helper — `Score += __ftol(float(int64(tick/60 unsigned)) × timemul)`, truncated separately from the kill term [08 R-CAMP-01 §7]. Correction, `[08 R-CAMP-01 §11]`: this row previously read "Nominally a time score multiplier; three retail campaign maps author `-1`. Parsed to a float and **inert** (reader census: none) [02 R-MAP-01 §3]"; that census missed the score helper. |
| `minwindspeed`, `maxwindspeed` | Wind energy range (wind generators). Any non-negative value is used as authored, including 0; the engine's 100/2000 fallbacks apply only to a negative value or a legacy (`0x1020`) terrain file [02 R-MAP-01 §6]. Every retail map authors both |
| `gravity` | Gravity for ballistic weapons, converted as `trunc(g × 65536 / 900)` to 16.16 world units per tick². Every retail map authors it; 192 of 275 use `112`. The executable's built-in `0x1FDB` (= the conversion of 112) is used only for a **negative** authored value or a legacy terrain file; an *omitted* key reads as 0 and yields gravity 0 [02 R-MAP-01 §6]. |
| `numplayers` | Recommended player counts, comma list (`2, 4, 6, 8, 10`). Stored as a string and **inert** (reader census: none); the lobby derives its count from the `[specials]` census [02 R-MAP-01 §3] |
| `size` | Map size in 512-pixel squares (`36 x 16`). **Inert**: no string in the executable |
| `memory` | Recommended RAM. Stored as a string and **inert** (reader census: none) |
| `useonlyunits` | TDF filename (with extension) in `camps/useonly/` restricting buildable units |
| `nomovie` | Skip mission movie |
| `MaxUnits` | Per-player unit cap for this map (retail: 200–400; most common 250; 184 of 275 author it). Read **only on the campaign path** (default 200); skirmish and multiplayer take the limit from the setup record [02 R-MAP-01 §2], [08 R-SKIR-01 §6] |
| `SCHEMACOUNT` | Nominally the number of `[Schema N]` sections (1–4). **Inert**: the executable formats `Schema %i` and probes upward until a section is missing, so the count is discovered, not read. |

### Schema keys (`[Schema N]`)

| Key | Meaning |
| --- | --- |
| `Type` | `Network 1` … `Network 4` for multiplayer variants; `Easy` / `Medium` / `Hard` for mission difficulty variants. Compared case-insensitively (32-byte read); selection order and the `StartPos`-count rule are [02 R-MAP-01 §4] |
| `aiprofile` | AI profile TXT basename in `ai/`; an empty or absent value loads `ai\default.txt` [02 R-MAP-01 §5] |
| `SurfaceMetal` | Metal extraction concentration on ordinary ground; seeded into every plot cell as a signed byte (retail authors 1–255) [03 R-TERR-01 §1] |
| `MohoMetal` | Nominally the extraction concentration for moho mines. Authored by all 275 retail maps and **inert**: only `SurfaceMetal` scales extraction, and a moho mine's advantage is its own larger `ExtractsMetal`. |
| `HumanMetal`, `HumanEnergy` | Player starting resources |
| `ComputerMetal`, `ComputerEnergy` | AI starting resources |
| `MeteorWeapon`, `MeteorRadius`, `MeteorDensity`, `MeteorDuration`, `MeteorInterval` | Meteor storms (weapon name from [tdf.md](tdf.md); defaults in `gamedata/METEOR.TDF`). **Enable rule (correction 2026-08-26):** only an empty `MeteorWeapon` disables the shower. Zero radius/density/duration/interval do NOT disable — they substitute all five values from the `[Default]` record and the shower stays enabled. An earlier reading in this document ("disabled when the radius, density, duration, or interval is zero") was wrong. A map authoring nonzero parameters with no weapon key is disabled with its parameters discarded. |

### Mission end conditions

Missions declare their win/lose conditions as `[GlobalHeader]`-level keys
(in retail data they always sit at the global level, not inside a schema).
The full retail vocabulary, with occurrence counts from a census of all 272
retail OTAs, and the meaning the executable gives each key
([08 R-TRIG-01 §4] owns the behaviour; only the grammar is restated here):

| Key | Uses | Side | Meaning |
| --- | ---: | --- | --- |
| `KillEnemyCommander=1;` | 7 | victory | a `Player=2` unit whose type is its side's commander is removed |
| `DestroyAllUnits=1;` | 90 | victory | `Player=2` has no live units (polled, not latched) |
| `KillAllMobileUnits=1;` | 9 | victory | the last `Player=2` unit with `BMcode=1` is removed |
| `BuildUnitType=ARMSY;` | 8 | victory | `Player=1` owns a finished unit of the type; **name only, no count** |
| `CaptureUnitType=CORGATE;` | 32 | victory | a `Player=2` unit of the type is captured; **name only, no count** |
| `KillAllOfType=CORKROG;` | 40 | victory | the last `Player=2` unit of the type is removed; **name only** |
| `KillUnitType=CORLAB, 1;` | 32 | victory | N `Player=2` units of the type removed (`%[a-zA-Z],%i`) |
| `MoveUnitToRadius=ARMCOM, 1942, 1519, 100;` | 18 | victory | a selectable, finished `Player=1` unit of the type (or `ANYTYPE`) within `radius` pixels (planar, inclusive) of the de-projected X, Z |
| `UnitTypePassesX=ARMTSHIP, 6000;` / `UnitTypePassesZ=ARMCOM, 800;` | 4 | victory | a `Player=1` unit of the type (or `ANYTYPE`) whose footprint cell is within two cells of `X>>4` (`Z>>4`) |
| `VictoryTimerRunsOut=3600;` | 4 | victory | game tick `>=` seconds × 30; value must be `> 0` |
| `CommanderKilled=1;` | 123 | defeat | a `Player=1` unit whose type is its side's commander is removed |
| `AllUnitsKilled=1;` | 163 | defeat | `Player=1` has no selectable, finished unit (script-locked units do not count) |
| `AllUnitsKilledOfType=ARMGATE;` | 52 | defeat | the last unit of the type in `Player=1` or `Player=2` is removed; **name only** |
| `UnitTypeKilled=ARMMOHO, 1;` | 18 | defeat | N units of the type, any owner, removed |
| `DeathTimerRunsOut=1200;` | 21 | defeat | game tick `>=` seconds × 30; value must be `> 0` |
| `AnyUnitPassesX=4500;` / `AnyUnitPassesZ=60;` | 3 | defeat | a `Player=2` unit's footprint cell is within two cells of `X>>4` (`Z>>4`); value must be `>= 0` |

Grammar facts the executable fixes: a flag key with value `0` is absent; each
key builds at most one condition, in the vocabulary order above regardless
of authored order; the argument scanset `%[a-zA-Z]` is **letters only**, so a
type name containing a digit is truncated at the digit and never matches;
`ANYTYPE` is recognised only by `MoveUnitToRadius`, `UnitTypePassesX` and
`UnitTypePassesZ`; the four "name only" keys store the whole value, so
`BuildUnitType=ARMSY, 1;` matches nothing. Win means every victory key
holds at once (AND); lose means any defeat key holds (OR). Only campaign
sessions read these keys; a skirmish on a map that authors them builds the
records but never polls them [08 R-TRIG-01 §1]. (**Correction 2026-08-29:**
the previous table's "Meaning" column, which called `CommanderKilled` "ends
when the enemy commander dies" and `AllUnitsKilled` "ends when all enemy
units are dead", had the sides reversed, and the earlier "win-vs-lose is
campaign-context behavior" caveat is withdrawn.)

`AllUnitsKilled=1;` occurs in **every one** of the 176 retail campaign
missions: it is the universal loss condition.

A mission with no `Player=2` unit at all, whose only victory key is the
injected or authored `DestroyAllUnits`, is won at the first 30-tick poll,
with the end latch written five polls later. An authored scenario that needs to run indefinitely
should keep one `Player=2` unit alive and out of reach (`Immunity=1;` is
parsed but has no reader, so it does not protect the unit).

### `[specials]` — start positions

Ten start positions exist, `StartPos1` … `StartPos10`, placed in the
multiplayer schema. The engine keeps only `specialwhat` values whose first
eight characters are `StartPos` (case-insensitive); the suffix is parsed as
an integer when it starts with a digit, otherwise it is a running counter
(1, 2, … in file order), and the stored index is the value minus one when
positive — so `StartPos0` and `StartPos1` collide on index 0
[08 R-TRIG-01 §9]:

```c
[special0]
	{
	specialwhat=StartPos1;
	XPos=560;
	ZPos=240;
	}
```

### `[features]` — placed features

```c
[feature0]
	{
	Featurename=WaterAquaOre3;   // feature section name (tdf.md)
	XPos=362;
	ZPos=427;
	}
```

These add to the features already embedded in the TNT's cell grid.

### `[units]` — placed units (missions)

```c
[unit13]
	{
	Unitname=CORCS;
	Ident=;                      // optional unique ID for order references
	XPos=3216;
	YPos=0;                      // authored terrain height; engine re-derives
	ZPos=1184;
	Player=2;                    // owning player (1 = human)
	HealthPercentage=100;
	Angle=0;                     // facing in degrees
	Kills=0;                     // pre-set veterancy (>5 = veteran)
	InitialMission=w 900,p 1416 852,;
	}
```

Less common unit keys in retail missions (occurrence counts from the full
census; all flags are `=1;` when present):

| Key | Uses | Meaning |
| --- | ---: | --- |
| `CreationCountdown` | 2088 | Parsed as an integer and **inert** (no reader; units are always created at battle entry). Always `0` in retail |
| `InitialGroup` | 262 | Parsed as an **integer** into a 4-bit field (retail values `patrol`, `Rockos` read as 0) and **inert** — no reader found [08 R-TRIG-01 §11] |
| `BuildPriority` | 213 | Parsed as an integer and **inert** |
| `AIIgnore` | 23 | Parsed and **inert** |
| `Immunity` | 22 | Parsed; copied to a unit status bit that nothing reads — **inert** |
| `AIPriorityTarget` | 9 | Parsed and **inert** |
| `MissionCriticalUnit` | 6 | Parsed and **inert** |
| `OffMapUnit` | 3 | No string in the executable — **inert** |
| `Kills` | — | Written by editors on every unit; **inert** — the placement parser has no reader, so veterancy cannot be authored |

`Player=0` is read as `1`. A `Player` value with no matching active slot
(after the one-based fixup, slot index 10 or more, an inactive slot, or a
slot with no human/computer/remote controller) is **fatal**: the executable
reports `Player number %d invalid for unit %s` and exits [08 R-TRIG-01 §9].
A `[feature]` block with a missing name or a negative `XPos`/`ZPos` is
dropped.

### `InitialMission` — the order mini-language

A comma-separated command list executed by the unit when the mission
starts. When the list runs out (or immediately if absent), the AI takes
over computer units; player units without an `s` stay locked until their
list completes. Commands:

| Syntax | Meaning |
| --- | --- |
| `m X Y` | Move to pixel coordinates |
| `p X Y [X Y ...]` | Patrol between current position and the point(s); with multiple points, patrol the cycle. Must be last — later commands are ignored. |
| `a NAME` | Attack nearest unit of type `NAME`, or the unit with `Ident=NAME`. If no unit of that type exists on the map, the unit reverts to AI control rather than idling (per a 1998 community format note; not yet independently verified against engine behavior). |
| `w SECONDS` | Wait (fractions allowed: `w 0.5`) |
| `wa` | Wait until attacked |
| `wa IDENT` | Wait until the unit with that Ident is attacked |
| `b UNITTYPE X Y` | (Mobile builders) build a unit/building at X Y. **Ground builders only** — retail never issues `b` to a construction aircraft, and a retail capture (2026-08-20) confirmed an ARMCA silently skips it: the preceding `m` executed, the `b` did nothing. See `probes/retail-reference/air-constructor`. |
| `b UNITTYPE N` | (Factories) build N units of the type |
| `i TRANSPORT` | Board the named transport (type or Ident) |
| `u X Y` | (Transports) unload most recent cargo at X Y |
| `g TARGET` | Guard. 2218 retail uses; the operand is an `Ident` (`g sebastian`, `g f12`), a unit type (`g CORGATE`), or a small integer that matches an `InitialGroup` value (`g 3`). Not in the 1998 format note; read off retail data. |
| `o ...` | 988 retail uses; meaning unconfirmed. |
| `d` | Self-destruct instantly |
| `s` | Become selectable/controllable before the list is done |

Two properties of `s` matter when authoring a mission, both established by a
census of all 176 retail campaign missions:

- **A `Player=1` unit carrying an `InitialMission` is locked out of player
  control unless the script contains `s`.** 1937 of the 2653 scripted
  player-owned units carry it; retail always writes it as the first command
  and spaced, `s, w 10, m 3200 1500`. The 716 that omit it are performing
  something the player must not interrupt, typically `i TRANSPORT`.
- **`s` does not end script control, it only unlocks selection.** A script the
  player is meant to override must terminate on a real order. Across retail
  scripts containing `s`, the last command is `p` (914), `m` (730), `g` (197),
  `b` (64) or a bare `s` (30), and never a long `w`. A trailing wait such as
  `w 9999` leaves the unit under script control for its whole duration: it
  stands still and ignores player orders. Long waits do appear in retail
  (`w 44444`, `w 100000`, `w 444444`) but only on AI-owned units meant to stay
  parked.

Example (retail ARM mission): a unit waits 900 seconds, then patrols:
`InitialMission=w 900,p 1416 852,;` — a factory building a loop of ships:
`InitialMission=w 920,b CORSEAP 1,w 300,b CORSEAP 1;`.

## Which keys the engine reads

The generated key → consumer table `[02 R-KEYS-01 §5]` lists every
`[GlobalHeader]`, `[Schema N]`, placed-object and trigger key the loader
reads, with accessor, stored width, default and consumer; `[02 R-MAP-01 §3]`
and `[02 R-MAP-01 §5]` give the read order and the retail authoring census.

A whole-string census of the retail executable finds every key in the tables
above except
`SolarStrength`, `MohoMetal`, `SCHEMACOUNT` and `OffMapUnit`, which have no
string in the image and therefore cannot be read.

Schema sections are found by formatting `Schema %i` and probing upward from
0 until a section is missing (no retail OTA has a gap; retail schema counts
are 1, 2, 3 or 4). Keys are read from **one section at a time** — the
accessors never fall through to the parent section — so a global key inside
`[Schema N]` or a schema key at global level is invisible [02 R-MAP-01 §3].
The difficulty and network vocabulary is the literal set `Easy`,
`Medium`, `Hard`, `Network 1`, `Network 2`, `Network 3`, `Network 4`; the
placement blocks are labelled `MISSIONUNIT DATA`, `MISSIONFEATURE DATA` and
`MISSIONRULE DATA`, and the raw map payload sections `Raw Plot Data` and
`Raw Feature Data`. The loader's own diagnostics name its failure modes:
`No GlobalHeader block in mission file!`, `No suitable schema type in mission
file!`, `Old TED format no longer supported!`, and `Hey, joker!  Mission file
%s is corrupt (no header found).`

## Unknowns and caveats

- `nomovie`'s reader is the end-of-campaign movie gate; which cinematic it
  suppresses is doc 07/08 territory [02 R-MAP-01 §3].
- The exact `Angle` → facing mapping (degrees, 0 = which direction,
  rotation sense) is unconfirmed.
- (Withdrawn 2026-08-29.) This list previously said "the engine appears to
  read several environment keys at either level"; it does not — each key is
  read from exactly one section, see "Which keys the engine reads" above.
- `Ident` scoping rules (uniqueness, forward references) are inferred from
  examples only.
- Malformed-input outcomes for an OTA (syntax error fatal, missing
  `GlobalHeader`, unknown unit name skipped, bad `Player` fatal, unknown
  feature name fatal, missing TNT fatal) are tabulated in
  `[02 R-MALF-01 §2]`.
- The mission open path's six diagnostics, including the misspelled
  `Hey, joker!  There is no mission defintion for this mission: %s`, are
  recorded in the executable spec doc 02 §6 "Mission-file diagnostics".

## Sources

- Key/command censuses in this document were computed over all 272 retail
  OTA files, and the campaign-specific counts over the 176 missions reachable
  from `camps/*.tdf`; scenario authoring rules derived from them live in
  `probes/retail-reference/README.md`.
- *OTA File Content Description*, TA Design Guide — variable catalog,
  schema and InitialMission documentation with retail excerpts:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/otadesc.htm>
- Verified against `maps/The Pass.ota` from `totala2.hpi`, plus a key
  census of all 272 retail OTA files (base, patch, Core Contingency,
  Battle Tactics, bonus maps) — the tables above cover every key that
  occurs in retail data.
- OpenTA parser: `formats/ota.go`.
