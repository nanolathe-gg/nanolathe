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
| `missionname` | Display name (also the campaign key for missions) |
| `missiondescription` | Description shown in selection screens |
| `planet` | Planet type string (selects the briefing globe art). The complete retail vocabulary (census of all 272 retail OTAs): `Green planet`, `Red Planet`, `Lava`, `Metal`, `Ice`, `Lush`, `Archipelago`, `Slate`, `Lunar`, `Water World`, `Wet Desert`, `Acid`, `Crystal`, `Desert`, `Urban`, and empty. Unrecognized values fall back silently. |
| `missionhint` | Hint text (missions) |
| `brief` | TXT basename in `camps/briefs/` with the mission briefing text |
| `narration` | WAV basename in `camps/briefs/` narrating the briefing |
| `glamour` | PCX basename (`bitmaps/glamour/`) shown on mission completion |
| `glamoursound` | WAV played over the glamour shot |
| `lineofsight` | `1` = true line-of-sight rules, `0` = off |
| `mapping` | `1` = normal fog-of-war mapping rules |
| `tidalstrength` | Energy per tidal generator |
| `solarstrength` | Nominally energy per solar collector. Authored by all 275 retail maps and **inert**: the executable has no string for it, so a solar collector's output is its own `EnergyMake` and the map cannot scale it. |
| `lavaworld` | `1` = "water" is lava (affects effects/pathing visuals) |
| `waterdoesdamage`, `waterdamage` | Acid water: flag and damage rate |
| `nosealeveltrigger` | Disables sea-level adjustment triggers (exact effect unconfirmed) |
| `killmul` | Kill score multiplier (percent) |
| `timemul` | Time score multiplier; retail campaign maps may author the signed sentinel `-1` |
| `minwindspeed`, `maxwindspeed` | Wind energy range (wind generators) |
| `gravity` | Gravity for ballistic weapons. Every retail map authors it; 192 of 275 use `112`, which is also the executable's own built-in default (stored as `0x1FDB` = `112 x 65536 / 30^2`, i.e. the authored value converted to 16.16 world units per tick squared by dividing by the tick rate twice). |
| `numplayers` | Recommended player counts, comma list (`2, 4, 6, 8, 10`) |
| `size` | Map size in 512-pixel squares (`36 x 16`) |
| `memory` | Recommended RAM (informational) |
| `useonlyunits` | TDF filename (with extension) in `camps/useonly/` restricting buildable units |
| `nomovie` | Skip mission movie |
| `MaxUnits` | Per-player unit cap for this map (retail: 200–400; most common 250) |
| `SCHEMACOUNT` | Nominally the number of `[Schema N]` sections (1–4). **Inert**: the executable formats `Schema %i` and probes upward until a section is missing, so the count is discovered, not read. |

### Schema keys (`[Schema N]`)

**Publication omission:** Historical executable-analysis detail omitted from this public edition.

### Mission end conditions

Missions declare their win/lose conditions as `[GlobalHeader]`-level keys
(in retail data they always sit at the global level, not inside a schema).
The full retail vocabulary, with occurrence counts from a census of all 272
retail OTAs:

| Key | Uses | Meaning |
| --- | ---: | --- |
| `CommanderKilled=1;` | 123 | Ends when the enemy commander dies |
| `KillEnemyCommander=1;` | 7 | Variant spelling, same intent |
| `AllUnitsKilled=1;` | 163 | Ends when all enemy units are dead |
| `DestroyAllUnits=1;` | 90 | Ends when all enemy units/buildings are destroyed |
| `KillAllMobileUnits=1;` | 9 | Ends when all enemy *mobile* units are dead |
| `KillAllOfType=CORKROG;` | 40 | Ends when every unit of the type is dead |
| `AllUnitsKilledOfType=ARMGATE;` | 52 | Variant of the above |
| `KillUnitType=CORLAB, 1;` | 32 | Ends after killing N units of the type |
| `UnitTypeKilled=ARMMOHO, 1;` | 18 | Variant of the above |
| `BuildUnitType=ARMSY;` | 8 | Ends when the player builds a unit of the type |
| `CaptureUnitType=CORGATE;` | 32 | Ends when the player captures a unit of the type |
| `AnyUnitPassesX=4500;` / `AnyUnitPassesZ=60;` | 3 | Ends when any unit crosses the pixel line |
| `UnitTypePassesX=ARMTSHIP, 6000;` / `UnitTypePassesZ=ARMCOM, 800;` | 4 | As above, restricted to a unit type |
| `MoveUnitToRadius=ARMCOM, 1942, 1519, 100;` | 18 | Ends when a unit of the type (or `ANYTYPE`) is within `radius` pixels of X, Z |
| `DeathTimerRunsOut=1200;` | 21 | Loss when the timer (seconds) expires |
| `VictoryTimerRunsOut=3600;` | 4 | Win when the timer (seconds) expires |

Whether a given condition means "win" or "lose" is campaign-context
behavior (most read as the player's victory condition; the timer pair is
explicit). Multiple conditions may appear in one mission.

`AllUnitsKilled=1;` occurs in **every one** of the 176 retail campaign
missions, which is consistent with it being the universal loss condition
("all of the player's units are dead") rather than a per-mission objective.

A mission with no `Player=2` unit at all ends in victory on the first tick:
"no enemies remain" is already true. An authored scenario that needs to run
indefinitely should keep one enemy unit alive with `Immunity=1;` and place it
out of reach.

### `[specials]` — start positions

Ten start positions exist, `StartPos1` … `StartPos10`, placed in the
multiplayer schema:

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
| `CreationCountdown` | 2088 | Delay before the unit appears; always `0` in retail (mission editors emitted it unconditionally) |
| `InitialGroup` | 262 | Group label tying units together for the AI (retail values: `1`, `2`, `3`, `patrol`, `Rockos`, or empty) |
| `BuildPriority` | 213 | Almost always empty in retail (two non-empty stragglers: `CORHRK`, `S`); semantics unconfirmed |
| `AIIgnore` | 23 | Enemy AI never targets this unit |
| `Immunity` | 22 | Unit cannot be damaged |
| `AIPriorityTarget` | 9 | Enemy AI prefers this target |
| `MissionCriticalUnit` | 6 | Mission fails if this unit dies |
| `OffMapUnit` | 3 | Unit starts off the map edge (arriving reinforcements) |

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

## Unknowns and caveats

- `killmul`, `timemul`, `missionhint`, `nosealeveltrigger`, `nomovie`, and
  `SurfaceMetal` scaling have no precise documented semantics.
- The exact `Angle` → facing mapping (degrees, 0 = which direction,
  rotation sense) is unconfirmed.
- Which keys are legal at global vs. schema level is looser than shown —
  the engine appears to read several environment keys at either level
  (retail data itself is consistent: end conditions and `MaxUnits` global,
  resources/meteors per schema).
- `Ident` scoping rules (uniqueness, forward references) are inferred from
  examples only.
- Exact win-vs-lose semantics of each end-condition key, and
  `BuildPriority`'s meaning, are engine behavior not recoverable from data.

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
