# OTA — Map and Mission Metadata (`.ota`)

## Overview

**Established** `[02 R-MAP-01]`, `[08 "Mission placement record"]`.
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
variants of the same terrain — different metal/resources/features); missions
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

**Established.** In `[units]` and `[specials]`, `XPos`/`ZPos` are map pixels:
X eastward, Z southward (see conventions in [README.md](README.md)).
**`[features]` is the exception**: there the pair is a **cell** index — a
16-pixel cell — which the loader uses with no pixel-to-cell conversion
`[02 R-MAP-01 §8]`. Reading a `[features]` pair as pixels collapses every
placement into a corner of the map. The Pass authors `size=7 x 4;`
but its TNT is 3584×1632 pixels, about 7×3.2 in 512-pixel squares. `size`
is an editor description, not a source of runtime dimensions.
`YPos` is an integer placement coordinate, scaled like X/Z into 16.16.
At creation, non-mobile units snap X/Z to the footprint grid and replace Y
with the terrain probe; mobile units keep the authored X/Y/Z through that
fixup. It is therefore incorrect to discard Y for every placement
`[08 R-ENTRY-01 §6]`.

### Global keys (`[GlobalHeader]`)

| Key | Meaning |
| --- | --- |
| `missionname` | Display name as authored by editors. **Inert in the OTA**: the executable's two readers of this key read the campaign file's `MISSION<n>` section; a skirmish map's display name is its file name (translated) [02 R-MAP-01 §3], [02 R-MAP-01 §9] |
| `missiondescription` | Description shown in selection screens (default `No description available`; lower-cased and passed through the translation table, original spelling kept when no translation exists) [02 R-MAP-01 §3] |
| `planet` | Planet type string (selects the briefing globe art). The historical 272-file survey found: `Green planet`, `Red Planet`, `Lava`, `Metal`, `Ice`, `Lush`, `Archipelago`, `Slate`, `Lunar`, `Water World`, `Wet Desert`, `Acid`, `Crystal`, `Desert`, `Urban`, and empty. Unrecognized values fall back silently. |
| `missionhint` | Hint text (missions). Parsed into a `camps\hints\<hint>.TXT` path that **nothing reads** — inert [02 R-MAP-01 §1] |
| `brief` | TXT basename in `camps/briefs/` with the mission briefing text |
| `narration` | WAV basename in `camps/briefs/` narrating the briefing |
| `glamour` | PCX basename; the loader stores `\<glamour>.PCX` and the glamour display rebuilds `bitmaps\glamour\<glamour>.PCX` from it [02 R-MAP-01 §1] |
| `glamoursound` | WAV played over the glamour shot |
| `lineofsight` | `1` = true line-of-sight rules, `0` = off |
| `mapping` | `1` = normal fog-of-war mapping rules |
| `tidalstrength` | Energy per tidal generator |
| `solarstrength` | Nominally energy per solar collector. Authored throughout the historical 275-map survey and **inert**: the executable has no string for it, so a solar collector's output is its own `EnergyMake` and the map cannot scale it. |
| `lavaworld` | `1` = "water" is lava (affects effects/pathing visuals) |
| `waterdoesdamage`, `waterdamage` | Acid water: flag and damage rate |
| `nosealeveltrigger` | Non-zero makes water opaque to projectiles and debris: the submerged-impact and debris-water tests return early [06 §8.2], [04 R-COB-04 §2]. Unrelated to the sea level value, which has no OTA key [02 R-MAP-01 §3] |
| `killmul` | Kill score multiplier, default `0.0`. Parsed to a float and read by the end-of-battle score helper — `Score += trunc(Kills × killmul)`, truncated separately from the time term [08 R-CAMP-01 §7], [08 R-CAMP-01 §11]. It is not inert: the doc-02 reader census that reported it so missed the score helper. |
| `timemul` | Time score multiplier, default `0.0`; three retail campaign maps author `-1`. Parsed to a float and read by the same score helper — `Score += trunc(float(unsigned tick / 60) × timemul)`, truncated separately from the kill term [08 R-CAMP-01 §7], [08 R-CAMP-01 §11]. It is not inert: the doc-02 reader census that reported it so missed the score helper. |
| `minwindspeed`, `maxwindspeed` | Wind energy range (wind generators). Any non-negative value is used as authored, including 0; canonical terrain substitutes 100/2000 only for negative values or an unparsed global header. Legacy (`0x1020`) terrain always uses its own wind words [02 R-MAP-01 §6]. The historical survey found both keys on every map |
| `gravity` | Gravity for ballistic weapons, converted as `trunc(g × 65536 / 900)` to 16.16 world units per tick². Every retail map authors it; 192 of 275 use `112`. The built-in runtime value 8155 is used for a **negative** authored value on canonical terrain, or for legacy terrain whose own gravity word is zero; a nonzero legacy gravity word is converted instead; an *omitted* key reads as 0 and yields gravity 0 [02 R-MAP-01 §6]. |
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
| `MohoMetal` | Nominally the extraction concentration for moho mines. Authored throughout the historical 275-map survey and **inert**: only `SurfaceMetal` scales extraction, and a moho mine's advantage is its own larger `ExtractsMetal`. |
| `HumanMetal`, `HumanEnergy` | Player starting resources |
| `ComputerMetal`, `ComputerEnergy` | AI starting resources |
| `MeteorWeapon`, `MeteorRadius`, `MeteorDensity`, `MeteorDuration`, `MeteorInterval` | Meteor storms (weapon name from [tdf.md](tdf.md); defaults in `gamedata/METEOR.TDF`). **Enable rule:** only an empty `MeteorWeapon` disables the shower. Zero radius/density/duration/interval do NOT disable — they substitute all five values from the `[Default]` record and the shower stays enabled. A map authoring nonzero parameters with no weapon key is disabled with its parameters discarded. |

### Mission end conditions

Missions declare their win/lose conditions as `[GlobalHeader]`-level keys
(in the historical survey they sit at the global level, not inside a schema).
The trigger vocabulary, with historical occurrence counts from a 272-file
OTA survey, and the meaning the executable gives each key
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
holds at once (AND); lose means any defeat key holds (OR). The loader builds
these records for a skirmish too, but only campaign sessions poll them [08 R-TRIG-01 §1]. Note which side each defeat
key watches: `CommanderKilled` and `AllUnitsKilled` are about the **player's**
units, not the enemy's.

**Established (reference-install survey, 2026-09-12):** the ordinary retail
mount plan yields 277 physical OTA entries but 275 winning logical OTA
paths. Counting the winning bytes once per path gives 163 authored
`AllUnitsKilled` keys, all with value `1`. Of 176 winning maps containing
an `Easy`, `Medium` or `Hard` schema, 146 author the key and 30 omit it.
The 13 winning campaign TDFs contain 175 sequential mission references to
175 distinct maps; 145 of those maps author the key. The additional
difficulty-schema map is `maps/example.ota`, which no campaign references.
Thus the inherited 163 count describes the whole winning map set, while
176 counts difficulty-schema maps; the former claim that all 176 authored
the key is withdrawn. Neither physical-entry totals nor difficulty-schema
classification establish campaign reachability. Retail's injection of a
default defeat condition when none is authored is a separate behavior
[08 R-TRIG-01 §6].

A mission with no `Player=2` unit at all, whose only victory key is the
injected or authored `DestroyAllUnits`, is won at the first 30-tick poll,
with the end latch written five polls later. An authored scenario that needs to run indefinitely
should keep one `Player=2` unit alive and out of reach (`Immunity=1;` only
excludes it from the documented automatic target searches until made
selectable; it is not invulnerability).

### `[specials]` — start positions

Ten start positions exist, `StartPos1` … `StartPos10`, placed in the
multiplayer schema. The engine keeps only `specialwhat` values whose first
eight characters are `StartPos` (case-insensitive); the suffix is parsed as
an integer (a decimal digit run) when its first character is a digit,
otherwise it takes a running counter that starts at 1 and advances only on
the non-numeric labels, in file order (`StartPos5, StartPosA, StartPosB`
gives the lettered labels 1 and 2). The stored index is the value minus one
when positive — so `StartPos0` and `StartPos1` collide on index 0, and
`StartPosA` collides with both [08 R-TRIG-01 §9] [08 R-TRIG-01 §12]:

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
	XPos=362;                    // CELL index, not a pixel
	ZPos=427;                    // CELL index, not a pixel
	}
```

These add to the features already embedded in the TNT's cell grid, and they
are authored in the same cell units as that grid — the pixel convention of
`[units]`/`[specials]` does not apply here. Both keys default to `-1`, and a
negative value is cleared to that sentinel and never placed. The pair is the
feature's **centre** for every definition that names no sprite `filename`:
the loader subtracts half the definition's footprint before the bounds test
and the stamp, so the authored cell is not in general the anchor
`[02 R-MAP-01 §8]`, `[05 R-FEAT-01 §3]`.

### `[units]` — placed units (missions)

```c
[unit13]
	{
	Unitname=CORCS;
	Ident=;                      // optional unique ID for order references
	XPos=3216;
	YPos=0;                      // authored Y; non-mobile fixup probes terrain
	ZPos=1184;
	Player=2;                    // owning player (1 = human)
	HealthPercentage=100;
	Angle=0;                     // facing in degrees
	Kills=0;                     // editor-authored; placement parser ignores it
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
| `Immunity` | 22 | Suppresses primary automatic target acquisition and the AI nearest-hostile search until make-selectable clears the state; not damage immunity [08 "Mission placement record"], [06 §3.1]. |
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

**Established.** The interpreter runs once after all mission units have been
created, during campaign entry or a between-missions restore. It builds the
ordinary order queues; it does not run as a separate script on each tick
`[04 §3.6]`, `[08 R-ENTRY-01 §6]`.

Every comma terminates a command. Arguments within a command are separated
by whitespace; a comma between coordinates would instead start a new,
usually ignored token. The table describes well-formed inputs. Unknown
leading characters are skipped, and many argument scans do not check their
conversion count, so malformed operands have no general zero-default rule.
Most verbs accept either case, with the uppercase-led `W` build-dispatch
quirk described in `[04 §3.6]`.

| Syntax | Meaning |
| --- | --- |
| `m X Z` | Queue move to the point. |
| `a X Z` | Queue numeric ground attack; both coordinates must parse. |
| `a UNITTYPE` | Queue attack-by-type when the catalog type resolves; an unknown type queues nothing. |
| `b UNITTYPE N X Z` | Queue a counted build. An actor with a mover (`BMcode == 1`) gets mobile build at X/Z; an actor without a mover gets building build and no position. A factory may author only name/count. Product lookup must succeed. |
| `bw N` | Queue stockpile production. |
| `p X Z [SECONDS]` | Queue one patrol point with optional timeout (zero when omitted); more points use further comma-separated `p` commands. |
| `w SECONDS [N]` | Queue wait; the optional integer is the wait-for-unit selector. |
| `wa [NAME]` | Queue wait-for-attack on the named unit; missing/unresolved name falls back to self. |
| `g NAME` | Queue guard of the first matching spawned Ident/Unitname; an unresolved name queues nothing. Numeric text is still a name, not an InitialGroup selector. |
| `i NAME` | Send an immediate attach request to board the named carrier; no order is queued. |
| `o MOVE FIRE` | Write the standing move/fire fields, retaining each operand's low two bits. Missing or malformed operands retain that field's prior value; no order is queued. |
| `u X Z` | Queue unload at the point. |
| `d` | Queue the self-destruct front gate. |
| `s` | Queue make-selectable. |

**Established — numeric boundary.** Coordinates and durations first parse
into single precision, then promote before multiplication by 65,536 or 30
and truncation toward zero. Positional orders carry literal zero Y. For
example `w 0.7` installs 20 ticks, not 21 `[08 R-ENTRY-01 §6]`.

**Established (scanner and caller trace) — prefixes and failed fields.**
Each command's argument scan advances through one byte string. Leading
whitespace is skipped; a successful conversion consumes its numeric or name
prefix rather than requiring a whole whitespace-delimited token. The first
conversion that cannot match stops the scan, and its destination and all
later destinations retain their previous contents. A later valid-looking
word cannot bypass that failure.

- Integer fields use signed decimal scanning: optional sign, then a decimal
  digit run. Accumulation and negation wrap to 32 bits; they do not saturate
  or retry as floating point. For example, `2.5` converts the integer prefix
  `2` and leaves `.5` for the next conversion; `2x` leaves `x`, which makes
  a following numeric conversion fail. `0x10` supplies decimal zero, not
  hexadecimal sixteen. Missing digits fail without assigning the field.
- Floating fields accept decimal digits with an optional sign, decimal point
  and `e/E` exponent (`d/D` is not an exponent here). At least one mantissa
  digit is required; `nan` and `inf` are not accepted spellings. The scanner
  can consume an exponent marker and sign without exponent digits after a
  valid mantissa; the converter
  then retains the mantissa value. Conversion stores binary32 and does not
  turn a binary32 overflow into a failed scan: signed infinity can reach the
  ordinary subsequent numeric conversion. This is not a saturation rule.
- Names consume the allowed prefix: letters, digits, underscore and dot,
  except `wa`, whose scanset omits underscore. A hyphen or other excluded
  byte ends the name and remains at the current scan position. Empty name
  conversion leaves its destination untouched. The numeric-attack caller
  requires two successful floats, otherwise it tries the name form; `wa`
  requires one successful name, otherwise it selects self.

**Established — caller initialization matters.** Build and stockpile counts
start at 1; wait duration and wait selector start at zero; patrol timeout
starts at zero; standing move/fire operands start from the unit's current
values. Failed fields retain these initial values where supplied. Move,
unload, patrol and mobile-build coordinate temporaries are not all
initialized before scanning, and most callers ignore the conversion count.
Their failed-coordinate results therefore cannot be reconstructed from OTA
text alone. A checked host must identify its chosen handling as policy,
rather than inventing a universal zero default [04 §3.6],
[08 "Argument parsing"], [08 R-ENTRY-01 §6].

**Established — selection postlude.** Queuing at least one order clears the
unit's selectable state. Unless the string contains numeric attack, patrol,
self-destruct or make-selectable, the interpreter appends a final
make-selectable order. Thus an explicit `s` can unlock the unit before the
rest of its queue completes, but absence of `s` does not imply a permanent
lock. Whether later player orders replace the queue is ordinary order
behavior, not an extra OTA script-control rule `[04 §3.6]`.

**Established — name scope.** Named-unit lookups walk the sparse array of
successfully created placements in file order, testing Ident and then
Unitname on each record case-insensitively. The first match wins; forward
references work because all placements are created before interpretation.
Failed allocations leave skipped holes `[08 "Argument parsing"]`.

## Which keys the engine reads

The generated key → consumer table `[02 R-KEYS-01 §5]` lists every
`[GlobalHeader]`, `[Schema N]`, placed-object and trigger key the loader
reads, with accessor, stored width, default and consumer; `[02 R-MAP-01 §3]`
and `[02 R-MAP-01 §5]` give the read order and the retail authoring census.

**Established.** The OTA loader does not consume `SolarStrength`,
`MohoMetal`, `SCHEMACOUNT`, `size`, `missionname` or placement `Kills` and
`OffMapUnit`. A spelling elsewhere in the executable does not establish an
OTA reader: `missionname`, for example, belongs to campaign metadata
`[02 R-MAP-01 §3]`, `[08 R-TRIG-01 §9]`.

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
- **Established:** `Angle` uses the integer accessor, wraps the degree value
  multiplied by 65,536 to signed 32-bit, divides by 360 with truncation toward
  zero, then stores the low 16 bits. Fractional authoring never survives as a
  fraction `[08 "Mission placement record"]`. Model-facing conventions belong
  to the unit/model contract, not an additional OTA transform.
- No environment key is read at both levels: each is read from exactly one
  section, see "Which keys the engine reads" above.
- **Unknown:** run-specific coordinate values consumed after failed,
  unchecked argument conversions, and outcomes of overlong commands that
  overwrite temporary token storage. The scanner's prefix, integer wrap,
  name termination and stop-on-failure rules are established above; another
  scanner trace cannot supply untouched temporary values. Nanolathe's
  local sequential scanner now implements those rules, with zero as explicit
  host policy for untouched uninitialized coordinates and a bounded token
  length. These policies are documented in DESIGN_SESSIONS_AI_SAVE C13.
- **Unknown:** exact rounding for adversarial decimal inputs through retail's
  intermediate floating converter has not been established. Its grammar,
  binary32 store and signed-infinity overflow result are established above;
  a trace of the converter's digit reduction and rounding arithmetic would
  settle remaining last-bit differences. Nanolathe currently uses Go's
  binary32 decimal conversion at that boundary, marked as host implementation
  rather than a claim of proven equality for every decimal string.
- Malformed-input outcomes for an OTA (syntax error fatal, missing
  `GlobalHeader`, unknown unit name skipped, bad `Player` fatal, unknown
  feature name fatal, missing TNT fatal) are tabulated in
  `[02 R-MALF-01 §2]`.
- The mission open path's six diagnostics, including the misspelled
  `Hey, joker!  There is no mission defintion for this mission: %s`, are
  recorded in the executable spec doc 02 §6 "Mission-file diagnostics".

## Sources

- Historical key/command surveys cite 272 and 275 OTA files, and 176
  campaign missions. The winning-path survey in "Mission end conditions"
  reconciles the current 275-map/175-campaign-map scope with the 176 maps
  containing difficulty schemas; it does not retrospectively establish the
  provider set of the older 272-file survey. Counts do not override loader
  evidence or turn physical entries into winning paths.
- *OTA File Content Description*, TA Design Guide — variable catalog,
  schema and InitialMission documentation with retail excerpts:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/otadesc.htm>
- Verified against `maps/The Pass.ota` from `totala2.hpi`, plus a key
  census of all 272 retail OTA files (base, patch, Core Contingency,
  Battle Tactics, bonus maps). The executable key tables, rather than that
  survey alone, establish what the loader consumes.
- Nanolathe boundaries: `formats/ota.go` retains the TDF tree and exposes
  metadata, including inert editor fields; `internal/mission/load.go` and
  `internal/mission/initial_mission.go` own runtime reads and interpretation.
  Authored checks include `formats/retail_test.go`,
  `internal/mission/load_test.go`, and
  `internal/mission/initial_mission_arguments_test.go`.
