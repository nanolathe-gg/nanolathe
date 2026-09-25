# ProTA 4.8 engine package

## Evidence scope and sources

This document records what the ProTA 4.8 install documents and what its shipped
runtime files implement. It does not establish retail behavior, and it approves
no Nanolathe behavior. Retail rules stay owned by
[retail-executable-spec](../retail-executable-spec/README.md); weapon keys
shared with other sets are owned by
[Non-retail weapon target keys](weapon-target-keys.md).

**Established — artifact identity.** The inspected release is ProTA 4.8 (bundle
dated 29 August 2025), identified by its bundled `ProTA 4.8 changelog.txt` and
`ProTA readme.txt`. Inspected files and SHA-256:

| File | SHA-256 | Role |
|---|---|---|
| `TotalA.exe` | `3b9c0fadabf3dc67ed5f05a70f1e1505a0c65deadd1a3c930adfe30e2a84995e` | reference TA 3.1 executable (version resource `v3.1`, Cavedog) |
| `tdraw.dll` | `6b46046aa0ea2ab6164ac19cf8c1aba9b1ee219e1123d3fb359615830a859e73` | engine-extension renderer ("TA engine v2025.8.29" per `ProTA.ini`) |
| `tplayx.dll` | `b641a7c97389088f93e7270425a24a1b6d0c442a112cd6be47a46c2773efdacf` | TA Demo Recorder network/session DLL |
| `dplayx.dll` | `5fbe8e18ca841b2dd3c73e28d1a9f00d1fa3fa460f50aa4682b791d5a2c2de19` | bootstrap and in-memory patch loader |
| `win32.dll` | `cd1969b924a0ef865bf4b3eea2b2fa182764b53daaadb0d93bf3c1a3077193ab` | imported music proxy; BASS-backed WGMUS family |
| `tmusi.dll` | `bfd96f5385984e8082db694f4d0192d65b660c1479df70f1a243a9e959beb202` | MP3 music backend (identical to TA Zero's `zmusi.dll`) |
| `wgmus.dll` | `5ab702665d01390e49fb8c8af34569a83db1342ab7e848f2a7f2794e1931f09c` | alternative music backend (WGMUS 0.0.24) |
| `ProTA.ini` | — | engine preferences, documented as "TA v2025.8.29" defaults |
| `ProTA.gp3` | `ba2ee5c758eaaa1e6409800206f6983515a05ea833d04f40193bda88bd73aa6b` | authored content inspected independently of the executable |

**Established — the shipped executable matches the reference image.** The executable
is byte-identical to the reference 3.1 baseline used by this project
(`3b9c0fad…`, `TotalAnnihilationOld/TotalA.exe`), and its version resource
still reads `v3.1`. Every engine change below therefore reaches the process
through the shipped DLLs (or through the renderer/music backends), rather than
a difference from that reference executable. File identity establishes this
comparison, not the provenance of every import in the reference image.

**Established — the DLLs are builds of a shared community engine family.** The
content of `tdraw.dll` overlaps heavily with TA: Escalation's `TAESC.dll` and,
to a lesser degree, TA Zero's `zdraw.dll` (shared text: megamap drawing,
whiteboard, challenge/response verification, factory recycling fix, start
position assignment, wind synchronization, crash diagnostics labelled
"Swedish Eye ver 0.8"). The three are separate builds of one lineage with
different feature sets and configuration strings; exact source provenance and
licensing are not established for these shipped builds. **Established (since)
— the lineage's current-generation source is MIT-licensed** (`src/DDraw` of
the TADR repository, [community patch engine behavior](community-patch-engine.md)),
and builds the same artifact family (`tdraw`, `taesc`, `zdraw`, `mdraw`) from
one tree under per-build configuration; whether these shipped builds
correspond to a revision of that tree is a version question the source cannot
answer.

No executable addresses, offsets, disassembly or decompiler output are recorded
here. **Evidence provenance:** earlier binary-derived findings were retained as
background under the [extension evidence policy](README.md#evidence-policy).
On 22 September 2026 the user explicitly authorized disassembly of third-party
patches for this remaining-gap investigation. The shipped-binary audit below
uses that authorization for the identified ProTA 4.8 artifacts; the
24 September 2026 follow-up (order-handler slot patches, player-11 feature
drawing, victory cue, resurrection text, megamap, and the two former AI
Unknowns) uses the same authorization and artifacts. Its raw traces
remain in the private analysis corpus; only independently worded behavioral
contracts enter this document. Current-source findings retain their own revision
scope and are not substituted for historical binary behavior.

## Engine bootstrap

**Established — the shipped `dplayx.dll` is the loader and gate.** On load it
verifies the host executable's version signature and the patch-site bytes it
expects (a mismatch exits with "Incompatible game files detected"), applies
its engine patches (failure exits with "Failed to apply game patches"), loads
`tdraw.dll`, requires that DLL's DirectDraw creation export, and redirects two
executable call sites to it; missing or incompatible `tdraw.dll` exits with
the corresponding message. It then exposes the DirectPlay entry points as
forwarders into `tplayx.dll`. The executable stays byte-identical to the
reference image because these changes are applied in memory at load — which is
what the changelogs call "`.exe` hacks".

## Global engine changes (not asset-driven)

**Established — documented.** The following are described by the bundle's own
changelogs. Sources: `OTA 3.1 to ProTA 4.3 changelog.txt`,
`ProTA 4.4` through `ProTA 4.8 changelog.txt`.

- **Megamap.** Full-screen strategic minimap with unit icons, mouse-wheel
  zoom, under-attack flashing, and player-icon/line colors; configured through
  `ProTA.ini` (`FullScreenMinimap`, `WheelZoom`, `WheelMoveMegaMap`,
  `DoubleClickMoveMegamap`, `UnderAttackFlash`, `MegamapFPSLimit`,
  `MegaMapConfig`, per-sensor minimum ring distances, `PlayerNDotColors`,
  `PlayerMarkerPcx`). The icon configuration lives in `Icon/iconcfg.ini`; the
  release adds custom megamap icons. The shipped 4.8 renderer's contract —
  a view toggled by key release or entered/left by the wheel, with no zoom
  steps, plus its input, rings, flash and colour rules — is recorded in
  [Shared draw-DLL interface, ProTA 4.8 shipped megamap](draw-engine-interface.md#prota-48-shipped-megamap).
- **Click snap.** `ClickSnap` snaps a reclaim command to the nearest reclaimable
  feature; release notes also name mex/geo snapping and an override key
  (`ClickSnapOverrideKey` in the DLL, configurable in the ctrl-f2 menu).
- **Whiteboard / ally map tools.** Allied line drawing, dot and text markers,
  erase, camera move to newest marker, ally resource bars, map-position
  sharing, and an expanded multiplayer sharing menu (`+shareall`,
  `+sharemetal`, `+shareenergy`, `+setsharemetal/-energy`); release notes add
  `+noshake` and `.ready` to the sharing menu, and `.autopause` to the
  battleroom.
- **Selection improvements.** Double-click to select same-type units on
  screen (`DoubleClick` preference); drag-selection filters added by the
  community engine (`W` weapons, `B` builders, `Y` factories per the ESC and
  Zero documentation of the same family); `CTRL+S` selects on-screen armed
  units; `CTRL+B`/`CTRL+F` idle-builder/factory cycling with `CTRL+SHIFT`
  variants. ProTA's own notes additionally state that `CTRL+F` centres the
  view on the selected factory and that `CTRL+B` does not select aircraft
  carriers.
- **Construction-unit behaviour options.** Per-unit-definition builder
  schedules became user-configurable: the ctrl-f2 menu exposes
  `ConUnitsGuard/HoldPos|Maneuver|Roam` and
  `ConUnitsPatrol/HoldPos|Maneuver|Roam` (strings `GUARDING CONSTRUCTION
  UNITS`, `PATROLLING CONSTRUCTION UNITS`). Release notes describe the
  intent: guarding builders assist/repair, patrolling builders reclaim when
  held and reclaim/repair/assist when mobile.
- **Build queue interaction.** Queued orders can be dragged to a new
  position; build orders may be queued under the player's own mobile units
  with automatic kick-out, and the build square previews yellow when a
  kick-out will occur (4.8 notes). The earlier 4.6 list also names the fix
  for "units exploding in factories" by holding a destroyed unit's identity
  for a fixed delay.
- **Multiplayer / session features.** `+autoteam`/`+randomteam` team
  assignment, `.exereport`/`.tdreport`/`.tpreport`/`.gp3report`/`.crcreport`
  CRC reports, challenge/response verification of executable, DLLs and game
  data, start positions derived from battleroom teams, wind-speed
  synchronization across clients, ten-player replay support, and a fixed
  "ghost commander" first-seconds artifact. All are documented in the 4.6/4.7
  release notes; the DLL strings corroborate the mechanisms (challenge
  response, start-position assignment, wind synchronization, ten-player
  funnel).
- **Runtime executable patches documented as "`.exe` hacks".** AI
  resource/feature-reclamation income multiplied by difficulty
  (Hard 4.0×, Medium 1.0×, Easy 0.5×; 4.8), AI nuke and anti-nuke build/fire
  behaviour and stockpile-queue limiting (4.5), scoreboard completeness,
  reclaim-sound fixes, the Necro "Resurrection failed" text, and the AI
  builder-count threshold (4.5); weapons acquiring targets during five kinds
  of construction work and map-owned player-11 features drawn regardless of
  LOS (4.6); and a victory sound for multiplayer/skirmish wins (4.7). The
  shipped executable matches the reference image; the loader applies these
  changes at runtime. The established AI boundaries and the separate
  current-source contract are recorded under
  [AI and economy evidence audit](#ai-and-economy-evidence-audit); the order,
  drawing, sound and text patches under
  [Shipped order, drawing, sound and text patches](#shipped-order-drawing-sound-and-text-patches).
  The release notes alone do not supply missing arithmetic or ordering.
- **Display and hosting defaults.** `ProTA.ini` documents engine defaults that
  differ from retail 3.1: unit limit 1500, pathfinding cycles 66650, effect
  limit 20480, unit model buffer 1280×1280, unit and weapon identifier limits
  both 16000 (multiplayer weapon-limit increase off because of replay
  compatibility), ten skirmish players, 3D sound with 128 mixing buffers,
  default game speed normal, `SwitchAlt` group keys, and menu/sound/music
  registry defaults. These require the DLL's limit-raising hooks; they are
  documented defaults, not engine arithmetic.

**Supported inference.** The engine also raises executable-side limits and
installs hooks at load: strings name limit adjusters for pathfinding map
entries, composite buffer, effect limit, unit count and unit/weapon type
tables. Which retail constant each adjusts, and the exact ordering of the
adjustment, is not established in this document.

## Asset-driven extensions

**Established — authored key census.** A census of the installed unit and
weapon text found no non-retail unit-definition key with a demonstrated
reader. ProTA's FBI files author the
retail key set plus localized/mission metadata (for example `Designation`,
`NoAutoFire`, `Ovradjust`, `SteeringMode`, `TEDClass`, `ThreeD`, `UnitNumber`,
`altfromsealevel`, `resurrect`, `TransMaxUnits`, `Scale`, `ai_limit`), which
are the same keys stock content uses; several are simply not yet compiled by
Nanolathe's catalog. This establishes the inspected content's vocabulary,
not that the DLL cannot read other keys. The current source's common startup
offers rotation and veterancy readers for the `prota` build too, and registers
preview keys unless the host disables nanoframe preview;
their absence from ProTA 4.8 content does not remove those readers from the
later source (see the version boundary below).

**Established — scripts use retail opcodes and retail ports.** A scan of all
compiled unit scripts found only the retail instruction set and only the
retail engine port identifiers (reads 4, 7, 8, 9, 11, 12, 14, 15, 16, 17, 18;
writes 1, 5, 6, 18, 19, 20), matching
[04](../retail-executable-spec/04-units-orders-scripts-and-movement.md)'s port
table. The shipped recorder DLL exposes the shared extended ports (see
[Extended script ports](script-ports.md)), but ProTA content authors no
extended-port reads. Apparent unknown opcode words in the census are trailing
Scriptor banner text after a script's return, not executable instructions.

**Established — one non-retail weapon key is authored with no
established reader.** `toaironly=1` appears on the two anti-missile
interceptor rockets (`amd_rocket`, `fmd_rocket`); see
[Non-retail weapon target keys](weapon-target-keys.md) for the census and the
unknown reader. The engine DLLs of this build do not contain the literal key
name.

**Established — rotated exits are content, not engine scripting.** ProTA 4.4
adds separate shipyard units for each exit direction (for example the east,
north and west shipyard variants) rather than an engine command; their
scripts are ordinary retail scripts.

## Version boundaries

The engine is updated release by release and versioned by date
("TA engine v2024.3.25", "v2024.12.05", "v2025.8.29" in the inspected
bundles). Capabilities are therefore bounded by the engine build each ProTA
release ships; the 4.6 change list in particular is the source for start
position/team handling, click snap, queue dragging, patrol behaviour, the
factory identity-recycle fix, whiteboard refinements, wind synchronization and
ten-player support. Earlier ProTA releases are not covered here.

### Current source profile is a separate target

**Established — source, not a reconstruction of 4.8.** The MIT TADR tree at
`dcff5ddeb6bd1030e3f452c0f16e5f005850f62f` selects `TDRAW_CONFIG_PROTA` in
[`config_prota.h`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/config_prota.h).
Its [`compile.yml`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/.github/workflows/compile.yml)
packages a newly compiled `tdraw.dll`, generic `totala.ini`, feature text and
the committed `dist/prota/tplayx.dll` as `tdraw-prota.zip`. The recorder
distribution is `2026.9.9`. This is a later engine-pair distribution, not the
ProTA 4.8 content archive or its 2025 engine binaries. The installation table
in that revision's `tdraw.txt` still names ProTA 4.6 and instructs users to
rename configuration files to the mod's names; its version label does not
certify a match to 4.8.

**Established — selected source behavior.** This profile enables guarding and
patrolling construction changes, construction-site kick-out, air-stack splash
handling and the contested-cell tie-break. Its off-map aircraft margin is one
tile, metal/geothermal snap radius is three cells and wreck snap radius one.
It disables the proportional repair module, aircraft-wreck fall, extended
weapon-ID protocol, share-abuse guard, local mute and percentage-share
commands; allied queued-build display also remains disabled. Common startup
and the host's preview setting, not the `prota` name, govern the shared
unit-key readers. Exact contracts and effective fallback values belong to
[Community patch engine behavior](community-patch-engine.md#31-feature-matrix-at-the-pinned-revision).
Neither these switches nor the `prota` name establish the historical 4.8
switches, authorize Nanolathe gameplay changes, or follow from choosing a
content directory profile.

### AI and economy evidence audit

This audit is deliberately split between the **shipped 2025 package** and the
**pinned 2026 source**. A contract established for one is not evidence that the
other implements it. The shipped-binary findings below use the explicitly
authorized static audit described under Evidence scope and sources.

**Established — licensed source identity and binary separation.** The readable
source is the TADR repository at
`dcff5ddeb6bd1030e3f452c0f16e5f005850f62f` (20 September 2026). Its root
[`LICENSE`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/LICENSE)
grants the MIT license separately to `src/DDRaw` and `src/Recorder`. The
[`compile.yml`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/.github/workflows/compile.yml)
workflow builds the `prota` draw DLL from this revision but packages a
committed recorder DLL because the hosted builder cannot compile its Delphi
source. That recorder identifies itself as `2026.9.9`; its SHA-256 is
`865f00361bc2726bc0f7349d9a16ad22f5d2673df1c99685ee21b68e3baff9f5`.
It is not the ProTA 4.8 recorder DLL, whose SHA-256 is
`b641a7c97389088f93e7270425a24a1b6d0c442a112cd6be47a46c2773efdacf`.
The committed 2026 binary was identified and hashed, not inspected for
behavior, and its correspondence to the readable recorder source remains
**Unknown**.

**Established — historical source does not close the package boundary.** The
[2014 recorder source](https://github.com/tanvanman/TADR/blob/6e142287d53d2bc18c486ee1cffbe601ed3eae45/src/Recorder/plugins/Builders.pas)
and its
[2015 revision](https://github.com/tanvanman/TADR/blob/3e92d8eb9eeb1b46e2698c43afeccf96d26164c0/src/Recorder/plugins/Builders.pas)
already differ: the older mobile-stockpile helper submits one round for a
matching type and reports that it consumed the request; the later helper
adds completed/queued-stock guards and reports false even after submission.
The later behavior survives in the pinned-source contract below. Neither
revision is identified as the source of the shipped ProTA recorder. Comparing
the [official release archives](https://prota.tauniverse.com/installation.html)
also finds the same recorder DLL in ProTA 4.4 and 4.5, although the 4.5
changelog introduces its AI changes. A matching feature name or older source
file therefore cannot establish the shipped algorithm. The subsequent binary
audit traces the identified loader's embedded patch definitions into the host
callers; it does not infer behavior from those older source versions.

**Established — shipped 4.8 difficulty factors, documentation scope.** The
ProTA 4.8 release note assigns the computer player's resource-generation and
feature-reclamation income the factors Easy `0.5`, Medium `1.0` and Hard
`4.0`. It contrasts those values with the documented earlier factors Easy
`0.5`, Medium `0.7` and Hard `1.0`. The statement names resource generation
and feature reclamation together, so both are required acceptance surfaces for
4.8 support.

**Established — shipped 4.8 production and feature-reclaim arithmetic.** The
identified `dplayx.dll` applies these changes after validating the host image.
Seven per-unit contribution routes participate: negative energy-use refund,
terrain extraction, metal-maker output, wind, tidal, passive energy production
and passive metal production. Each contribution independently checks for an
owner player record with computer control and reads the difficulty selector.
Easy (`0`) uses `0.5`, Medium (`1`) uses `1`, and every other selector uses `4`.
Humans retain the ordinary addition.

For contribution `c` and production accumulator `p`, Easy computes
`float32(p - c * double(-0.5))`, Medium computes `float32(p + c)`, and
Hard/other computes `float32(p - c * double(-4))`. Arithmetic retains the host's
working precision until the single-precision accumulator store. The patch adds
no sign or finiteness guard and does not move scaling after accumulation. The
negative-energy-use route still negates its authored operand before scaling.

Feature reclamation retains its existing protection guard, then credits the
whole authored energy pool before the whole metal pool. Each credit separately
uses the builder owner's computer-control gate, difficulty selector and the
same arithmetic/store boundary. Feature removal or replacement follows payout.
Unit reclaim is a different path: it, spawn credits, player transfers, reverse
construction and factory-cancellation refunds retain the retail
Easy/Medium/Hard factors `0.5/0.7/1`. These are bounded findings for the loader's
modified callers, not a global replacement of the retail difficulty factor.

**Established — shipped 4.8 stockpile intent.** The ProTA 4.5 release note,
retained in the 4.8 package, states three results: computer players can build
and fire nuclear and anti-nuclear stockpiles, they do not queue stockpile
weapons without bound, and the change applies as a runtime engine patch. That
document does not give the queue predicate or distinguish stationary and
mobile producers.

**Established — shipped 4.8 stockpile purchasing.** The identified loader
adds an empty-secondary-order requirement to the resource/builder-queue task's
initial live, completed, building-class admission. It also assigns the armed
building group's task to this same resource/queue method. Members are visited
in group-vector order, with the next deadline set to the current tick plus 30.
This admission precedes both the appliance branch below and the product branch.

The product branch additionally requires a nonempty compiled build-option list
and no primary order. It uses the ordinary cumulative-reservoir choice from
CANBUILD and submits one selected product through the ordinary build-queue
producer. Stockpile silos therefore need authored builder membership and a
stockpile product in that list. The new guard checks whether a secondary order
exists; it does not inspect that order's count or completed ammunition. Once
both order paths are empty, another round can be requested even when completed
rounds remain. This is not the completed-stock guard in the later source below.
The inherited task and queue contracts are [08 R-AI-01 §2] and
[07 "Factory product click producer (R-P0-11 §1)"].

**Established — authored stationary stockpile producers.** `ARMAMD`, `ARMEMP`,
`ARMSILO`, `CORFMD`, `CORTRON` and `CORSILO` are armed buildings with
`builder=1`, `BMCode=0` and `energyuse=0`. Each authors exactly one corresponding
`MAKEANTI` or `MAKENUKE` pseudo-product. The ordinary classifier places them
in the armed-building group, the patched task reaches the product branch, and
the ordinary submission helper converts that product into slot-zero
`BuildWeapon`. Queued count and completed ammunition remain separate; production
and its completion cap retain [06 §11.1]. The two mobile anti-nuke units
`ARMSCAB` and `CORMABM` have CANBUILD sections but author `builder=0` and
`BMCode=1`; this group-task patch does not establish generation for them.

**Established — no shipped route generates mobile anti-nuke rounds for the
computer player.** A compiled build-option list exists only for a definition
that authors `builder` [02 R-CAT-01 §5], so `ARMSCAB` and `CORMABM` have none,
whatever their CANBUILD sections say. In the reference executable the only
computer-player caller of the product-submission helper — the helper that
turns a chosen `MAKENUKE`/`MAKEANTI` product into `BuildWeapon` — is the
resource/queue task above, and its product branch first requires a nonempty
compiled list [08 R-AI-01 §2]. The loader redirects no other task to it, and
none of the three shipped DLLs contains either product name, a CANBUILD
reader, or another stockpile-queue hook. The 4.8 computer player therefore
never queues rounds for these two units; any stock they hold comes from human
orders. This is a bounded static negative over the shipped package, not a
manual observation, and it does not describe the later source's mobile
helper below.

**Established — shipped target retention and firing boundary.** A separate
loader patch modifies the ordinary rotating three-slot weapon-maintenance scan.
It retains the sweep cadence and slot order, enabled/autonomous admission and
dropped/command-fire exclusions. Its outer standing-fire test admits encoded
values two and three; ordinary unit acquisition still requires exactly two
(Fire at Will), which the computer-unit classifier normally assigns.

For a retained unit target, the patch adds the ordinary physical shot-admission
test. Failure directly clears the stored target-unit identity without invoking
the ordinary clear callback, but retains the old local target through the
remaining alliance, bad-target-category and paralyzer/stunned checks. If those
checks accept, reacquisition waits until the slot's next maintenance visit; if
one rejects, acquisition occurs in the current visit. This ordering matters:
clearing an out-of-range target does not always reacquire immediately.

The patch adds no distinct strategic nuclear selector or launch timer. Unit
acquisition retains [06 §3.1–3.2], and interceptor choice retains [06 §11.2].
The fire executor still requires completed stock, consumes one round only after
successful projectile creation, and charges no per-launch resources. Interceptor
scanning likewise requires completed stock. This establishes the patch delta
and inherited calls; it is not a reported end-to-end match observation.

**Established — pinned-source stockpile admission.** The readable recorder
[`Builders.pas`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/Builders.pas)
has one optional AI-stockpile group, enabled only when the
`Preferences/AiNukes` Boolean is true;
[`IniOptions.pas`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/IniOptions.pas)
sets its source default to false. Neither the current source package's generic
`totala.ini` nor any readable INI in ProTA 4.8 sets that key. Thus a build from
the readable source, used with those files, keeps these hooks disabled. This
does not establish the state of either committed recorder binary.

When enabled, the pinned source recognizes a stockpile product only when its
authored name contains the case-sensitive substring `MAKENUKE` or `MAKEANTI`.
Its stationary-producer wrapper handles a positive queue request as follows,
in order:

1. If weapon slot zero already has at least one completed round, reject the
   request without calling the ordinary stockpile-queue helper.
2. Otherwise, if a secondary order exists and its second integer parameter is
   positive, reject in the same way.
3. Otherwise, forward the caller's original requested count unchanged.

A zero or negative request, and every name outside those two substring
classes, bypasses both guards and is forwarded unchanged. Consequently the
source does not by itself establish a universal queue size of one: that result
also depends on what count the caller supplies.

For a mobile producer, a matching nonzero candidate type with no completed
round in weapon slot zero and no positive secondary-order count submits one
round to the ordinary helper. The helper's return value remains false on every
path, including after that submission, so its wrapper then resumes the
ordinary build-as-unit arm. This fallthrough is part of the pinned source
contract; skipping the ordinary arm after submitting the round would be a
different algorithm. What that inherited arm subsequently changes is
**Unknown** from this extension source alone because its implementation belongs
to the host executable.

The same option installs a fixed host-instruction replacement described by its
source as allowing AI-owned stationary stockpile producers to make nuclear
rounds. The readable source does not express the replaced algorithm, and no
separate readable hook implements the documented firing behavior. Production
arithmetic, target choice, launch timing, interceptor response and any random
draws therefore remain **Unknown** from this pinned-source package alone.
The separately audited shipped-loader behavior above must not be attributed to
that later source or its unverified prebuilt recorder.

**Established — shipped 4.8 low-energy appliances.** After the shared task
admission above, the loader replaces the authored `makesmetal` selector with a
test of `energyuse`: interpret the most significant byte of its IEEE-754
single-precision representation as signed and require it to be at least 66.
For finite nonnegative values this means `energyuse >= 32`; negative values
fail, while positive infinity and positive-sign NaNs pass the bit test. This
is not a generic test for any positive consumption.

Admitted appliances retain the resource task's ordinary decision order. Disable
when energy stock is at most twice metal stock. Otherwise leave the unit
unchanged unless net energy production is strictly positive; then consume one
global simulation draw with bound five and request enable only for a nonzero
result. Disabling consumes no draw. The ordinary activation helper supplies
the state change and COB callbacks. A secondary order suppresses this entire
visit, including energy management, and appliances do not fall through to the
product branch. No extra energy threshold or separate reactivation timer was
added. The inherited resource comparison and activation contract is
[08 R-AI-01 §2].

**Established — shipped 4.8 construction threshold is asymmetric.** The 4.5
release note describes a change from five to ten build units before the
commander enters repair patrol. The loader changes only the construction task's
first comparison: a capture-capable group member skips the placement pass at a
signed build-capable count of ten or more. The later reposition pass retains
its retail comparison, admitting that member at five or more.

| Build-capable count | Placement pass | Reposition / repair-patrol pass |
|---|---|---|
| Below 5 | Eligible | Skipped |
| 5 through 9 | Eligible | Eligible |
| 10 or more | Skipped | Eligible |

Both passes retain their other build-list, order, placement and movement gates;
eligibility does not guarantee order submission. The reposition action remains
a move followed by queued patrol; the ordinary reclaim-capability resolver
selects repair patrol. These are generic capture-capable branches, not
commander-identity tests.

The unchanged count refresh runs every 30 ticks and includes own live,
completed units with a compiled builder list, even if that list has no entries.
Disabled units are not excluded by this predicate. The construction task
reschedules every 90 ticks and reads the stored count at each qualifying
capture-capable member's gate in both passes
[08 R-AI-01 §3][08 R-AI-01 §16]. Thus the shipped change raises the
stop-building cutoff, while leaving repair-patrol admission at five; the
changelog's single-boundary description is insufficient. The complete authored
membership of ProTA's `builder` flag was not censused here.

**Established — Nanolathe comparison.** Nanolathe currently implements the
retail baseline for each reachable surface:

- `internal/economy` applies Easy `0.5`, Medium `0.7` and Hard `1.0` at the
  per-unit production/refund contribution store, and applies the same selector
  separately to feature-reclaim energy and metal credit. The feature service
  returns raw pools, so the ledger is already the single scaling boundary.
- `internal/ai` uses the retail build-capable count in two complementary
  commander branches: a capture-capable builder stops attempting placement at
  five or more, and starts the reposition/repair-patrol arm at five or more.
- The retail AI eco task toggles only completed building-class metal makers.
  Its disable test is `energy stock <= 2 × metal stock`; a possible enable
  requires positive net energy and consumes one simulation draw with bound
  five. It is not the documented ProTA "any energy-hungry appliance" rule.
- The planner has no producer for the secondary `BuildWeapon` order. Existing
  order and combat code can produce and launch stockpiled rounds after that
  counted order has been inserted, but no computer-player task inserts it.

These comparisons describe the current implementation; the newly established
historical contracts above remain implementation gaps, and research alone does
not change Nanolathe's rules. The existing
gameplay-selection requirements remain in
[DESIGN_GAMEPLAY_RULES §9](../../docs/DESIGN_GAMEPLAY_RULES.md#9-extending-the-existing-mechanism).

### Shipped order, drawing, sound and text patches

This audit covers the rest of the "`.exe` hacks" that the 4.5–4.7 release
notes list, as the identified 4.8 loader and renderer implement them. It uses
the same authorized static method and scope as the audit above. Each contract
is stated as a change to the named retail contract. Anything not listed keeps
that retail contract.

#### Weapons acquire targets while working

**Established — documented intent.** The 4.6 release note (kept in the 4.8
package) lists five changes: weapons may acquire targets while reclaiming a
unit, capturing a unit, repairing a unit, assisting ("assist-nanolating") a
new unit and building a new unit.

**Established — retail baseline.** Each weapon slot's control byte has an
autonomy bit, and only slots with that bit set take part in autonomous
acquisition and the retaliation offer [06 §3.2][04 R-ORD-01 §7]. Two order
verbs write it. Retail calls them *release* and *inhibit*, and the names are
misleading: *release* takes a slot away from autonomy, and *inhibit* gives it
back and clears its target. Both skip a slot whose enabled bit is clear, or
whose autonomy bit already has the requested value. When a verb does change a
slot, it also clears a nonempty target pair and schedules `TargetCleared`
[04 R-ORDER-02 §3]. Several ground work handlers call "release all three
slots" (the all-slots form covers slots 0, 1 and 2, in that order) at a fixed
point [04 R-ORD-01 §5]. The record destructor gives the slots back when the
order is removed [04 R-ORD-01 §7], so in retail the builder's weapons are
silent for the rest of the job.

**Established — the shipped change.** The loader redirects exactly five of
those all-slot *release* calls to the all-slot *inhibit* verb. The call site
and its argument stay the same. The five sites are:

| Handler | Retail moment of the replaced call [04 R-ORD-01 §5] |
|---|---|
| `MobileBuild` | phase 1, after the reach test and a legal placement check, before the site is prepared and the nanoframe created |
| `HelpBuild` | phase 1, after the unreachable and already-complete exits, before `StartBuilding` |
| `Capture` | phase 0, after the admission, caption and capture-timer steps, before the goal is installed |
| `ReclaimUnit` | phase 0, inside the admission-pass arm, after the `Reclaiming` caption |
| `RepairUnit` | phase 1, in the in-reach arm, before `StartBuilding` |

At each site, every enabled slot not yet autonomous becomes autonomous. If
its target pair was nonempty, the pair is cleared and `TargetCleared` is
scheduled with the slot index. Slots that were already autonomous are left
alone, target included. Normally all three slots are autonomous when a work
order starts, so in practice the builder simply keeps its weapons for the
whole job. The call is not a no-op, though: a slot that an earlier order still
held would be handed back here. The rest of the pipeline is unchanged:
autonomous acquisition still needs a complete, armed builder in the
fire-at-will stance and the usual per-slot tests [06 §3.2], and the destructor
still gives the slots back when the order ends.

**Established — what stays retail.** The following work paths keep the
all-slot *release*: `RepairUnitNoMove`, `VTOL_MobileBuild` [04 R-ORD-02 §2]
and all five VTOL work twins [04 R-ORD-01 §7], whose shared preamble takes the
slots (so construction aircraft still silence their weapons while working).
The factory `BuildingBuild` machine calls neither verb, in retail or in 4.8.
**Feature reclaim is not included:** retail `Reclaim` (feature) and
`Resurrect` never touch the slot verbs [04 R-ORD-01 §5], so their weapons
were already autonomous. The pump's unconditional three-slot target clear for
records that are satisfied with the `0x10000` gate bit is unchanged
[04 R-ORDER-02 §2].

**Established — two related weapon-slot patches in the same list.** The
loader also changes two attack handlers. The same edits appear in
TA: Escalation's patched executable. None of the bundled changelogs (3.1
through 4.8) lists them; only the loader's embedded patch definitions
describe them.

- *`Attack_Chase` phase 1* (the shot gate admits slot `p1` [04 R-ORD-01 §3]):
  retail takes slots 0 and 2 before binding slot `p1`. The patch takes only
  one slot: slot 2 when `p1` is greater than 1 (signed), otherwise slot 0. The
  other slots stay autonomous while the unit fires at its ordered target.
  Phase 3's retail "take slots 0 and 2" is unchanged.
- *`Suppress` phase 1 with `p1 = 2`* (fire at a position with the third
  weapon, the D-gun path): retail takes all three slots before binding slot 2
  to the goal. The patch takes only slot 2, so slots 0 and 1 keep acquiring.
  The `p1 ≠ 2` branch (take slots 0 and 1, bind both) is unchanged.

**Established — comparison with TA: Escalation.** Escalation's patched
executable makes the same swap only at the `MobileBuild` site
([TA: Escalation, "Weapons stay active while building"](taesc-engine.md)).
Its bytes at the `HelpBuild`, `Capture`, `ReclaimUnit` and `RepairUnit` sites
match the reference executable, and its `Attack_Chase` and `Suppress` sites
match the ProTA edits above. The only change common to both packages is
therefore the build-a-new-unit case. The Escalation doc's "switches them on"
wording is correct in effect: the replacement verb is the one that gives
slots back to autonomy.

#### Map-owned features drawn without line of sight

**Established — retail baseline.** The feature stamp service writes a
four-bit *placer nibble* into each anchor cell. Terrain-file, mission-file,
successor, reproduction and reload stamps write 10; a corpse writes its owner's
slot [05 R-FEAT-01 §3]. The composer's two feature passes draw a feature when
its definition lacks `nodrawundergray`, when the placer nibble equals the
local player's slot, or when the two-corner LOS test passes. There is no
separate explored-state term [03 §5.1.5][03 R-RAST-01 §6]. Four section names
(`DragonsTeeth`, `DragonsTeeth_Core`, `Fortification`, `Fortification_Core`)
force `nodrawundergray` on [02 "Feature record"]. Retail map-owned walls
therefore needed current LOS to be drawn.

**Established — loader half.** The loader changes the placer argument of the
battle-entry terrain-file stamping from 10 to 11. This covers both terrain
attribute layouts and the void pass; a void stamp never writes the nibble.
Mission-file `[features]` placement, burnt successors, reproduction, save
reload and corpse stamping are unchanged. A player slot is never 11, so this
edit alone changes no drawing decision.

**Established — renderer half.** The shipped `tdraw.dll` installs one hook at
startup, with no preference gate: `ProTA.ini` has no such key, and the
source's compile-time `VISIBLE_MAP_DTS` switch
([Community patch engine behavior](community-patch-engine.md) CP-ENV-3) is
built in. The hook sits on the **second (tall-feature) pass only**, after the
`nodrawundergray` and placer-equals-local-slot tests have failed and before
the LOS test. A placer nibble of 11 draws the feature without that LOS test.
The renderer does not write owner 11 itself: the three owner-rewrite
addresses are present in its data but never used. The loader edit above
supplies them, which is why CP-ENV-3's "inert" finding does not apply to the
shipped package.

**Established — resulting contract.** A feature is drawn regardless of LOS
when all of these hold: it came from the map's terrain file; its definition
has `height` of 10 or more (the tall-feature pass); and its definition has
`nodrawundergray`, authored or forced. Features without `nodrawundergray`
already drew unconditionally. ProTA's `DragonsTeeth`, `DragonsTeeth_Core`
(height 20) and `Fortification` (height 55) are forced. `FloatingTeeth`
(height 75) authors the flag. Short `nodrawundergray` features (height
below 10) keep the retail first-pass gate, so they still need LOS. Mission-placed walls, and
walls that players built or wrecked (whose corpses carry the owner's slot),
are unaffected. The feature is still composed under the fog overlay, which
keeps unexplored cells dark ([03 §5.1.5]). What changes is that map walls in
explored cells outside current LOS are no longer skipped.

**Established — no effect beyond drawing.** No simulation reader compares the
placer nibble. Retail's only readers are the two composer gates and the save
file's `PlayerFeatures` blob [08 R-SAVE-02 §12], which packs and restores the
nibble; this is the bounded census of [03 §3.3]. A 4.8 save made in battle therefore stores 11 for these cells and
restores it on load. Targeting, pathing, reclaim and the economy never read
it. The retail minimap contacts pass never draws features
[03 §3.9], and the 4.8 megamap has no feature layer
([Shared draw-DLL interface](draw-engine-interface.md#prota-48-shipped-megamap)),
so neither map surface shows these features.

#### Victory cue on multiplayer and skirmish wins

**Established — mechanism.** The shipped renderer hooks the battle frame
composer where it is about to draw the in-game victory title (`igvictory`).
That branch is reached only when the end latch has set the won-path title bit
and the composer's existing gate before both end titles passes
[07 §11][08 R-TRIG-01 §6]. The hook is installed at startup with no
preference gate. It keeps one process-wide "last call" game tick, zero at
start. Each time the title branch runs, it compares the current global tick
with that value. If the current tick is lower, or more than 300 ticks later,
it plays the sound alias `Victory Condition` through the same retail
sound-alias routine and no-broadcast argument that the campaign victory
trigger uses [08 R-TRIG-01 §8]. It then stores the current tick. No packet is
sent.

**Established — asset.** ProTA's `gamedatP/ALLSOUND.TDF` maps
`[Victory Condition]` to `sound=victory2`, the same alias and stock sample as
retail. The release note calls it the "Mission Complete" sound. **Supported
inference:** the note is describing how the `victory2` sample sounds, not
naming a different asset; nothing in the DLL names another sound.

**Established — conditions and timing.** The cue plays locally for the
viewer whose session latched *won*. A defeated or resigned client never
reaches the victory-title branch. Nothing checks the session kind, so the hook
also runs for campaign wins. There it adds one cue when the title first
appears, on top of the per-condition `Victory Condition` cues that retail
plays when each victory condition is satisfied [08 R-TRIG-01 §8]. While the
title is on screen, the stored tick is refreshed every frame, so the cue does
not repeat. Two edge cases follow from the stored value. First, a win whose
title first appears at global tick 300 or earlier in the first battle of a
process plays no cue. Second, a later battle plays the cue only when its
title tick is below the stored value or more than 300 ticks above it. These
edges are properties of the shipped hook, not of retail.

#### Resurrection failure text

**Established — retail baseline.** `Resurrect` has two failure captions.
When the feature lookup fails it shows status 7 `Resurrection failed`. When
phase 3 cannot resolve the corpse name to a unit definition, it shows status
7 `Ressurection failed` (retail's spelling) [04 R-ORD-01 §5]. Both strings
are in the reference executable.

**Established — shipped change.** The loader repoints the phase-3 caption to
the executable's existing correctly spelled string. In 4.8 both failure arms
therefore display `Resurrection failed`, with the same status code 7 and the
same control flow. The misspelled string is left unused. No new text or
localization is involved.

## Authored package, interface and single-player coverage

**Established — archive namespace.** The identified `ProTA.gp3` uses
`gamedatP`, `guiP`, `unitpicsP` and `weaponP`; it also declares an empty
`downloadP` directory. Unit definitions remain under `units`, AI profiles under
`ai`, and models, scripts, textures, sounds and animation keep their shared
names. Therefore "retail content directories" is incorrect for this release,
even though its executable on disk matches retail. Empty `downloadP` is not
evidence that retail `download` records should be imported. Sources: the
archive directory and `gamedatP/SIDEDATA.TDF`; the latter supplies `[CANBUILD]`
lists including the separately named rotated shipyards.

**Established — directional shipyard GUI and membership are separate.** The
identified 4.8 archive defines the regular Core shipyards `CORSYE` and
`CORSYW`, and provides physical GUI pages under those same names. Its
populated `SIDEDATA/CANBUILD` sections instead use `CORSYNE` and `CORSYNW`.
Exact-name membership lookup therefore leaves the two defined yards' CANBUILD
lists empty. This does not mean that their human build buttons are empty:
`CORSYE1.GUI` and `CORSYW1.GUI` each install a `CORCS` product gadget. Retail
factory activation resolves the installed gadget name independently of
CANBUILD [07 §9]. Nanolathe's command-level package check clicks those authored
gadgets into counted factory queues while preserving empty CANBUILD membership.
No inferred alias or GUI-to-AI membership conversion is needed for that path.
Sources: the archive's unit definitions, physical GUI pages and
`gamedatP/SIDEDATA.TDF`; `TestRetailProTADirectionalCoreShipyardClicks` and
`TestProTA48PackageAcceptance` exercise the Nanolathe boundary.

**Established — the shipped package adds no AI membership for these yards.**
The loader's patch list has no build-list or shipyard entry. The renderer's
only hooks inside the catalog compiler enlarge a build-list allocation (a
memory-safety fix) and signal the end of catalog loading so that the
extension can rebuild its selection category masks; neither renames or
aliases a CANBUILD section. No shipped DLL contains a CANBUILD or `SIDEDATA`
reader. Retail exact-name membership therefore stands: `CORSYE` and `CORSYW`
have empty compiled build lists, so a computer player never chooses a product
from them, while their GUI gadgets still work for humans as described above.
Nanolathe's empty membership matches the shipped package.

**Established — authored interface.** `SIDEDATA` orders Arm then Core, with
commanders `ARMCOM` and `CORCOM`, interface GAFs `ARMINT` and `CORINT`, and
button fonts `armbutt` and `corbutt`. `guiP` contains the faction main panels,
per-builder pages and front-end windows, including campaign and save/load
windows. The 3.1-to-4.3 changelog documents twelve build icons per page,
orders on the same panel, build hotkeys and their overlays. These are required
interface inputs; recognizing the unit definitions alone does not exercise
them. The 4.6 changelog further specifies that idle-builder/factory shortcuts
prefer authored `CTRL_B`/`CTRL_F` categories over heuristics, and armed-unit
selection uses `CTRL_W` with `NOTAIR`/`NAIR`. It does not provide the complete
order/build hotkey table.

**Established — palette and artwork overrides.** The archive supplies
`palettes/PALETTE.PAL`, `GUIPAL.PAL`, `GUIPAL.PCX`, `PALETTE.SHD`,
`PALETTE.LHT` and `PALETTE.ALP`, as well as replacement textures, models,
cursors, build pictures and fonts. The 4.8 changelog documents pink/slate
replacing white/black team colors and revised minimap colors; the shipped
`Icon/iconcfg.ini` and `ProTA.ini` configure megamap art separately. A stock
palette, stock interface or stock icon set cannot stand in for these assets
when assessing ProTA rendering.

**Established — campaign and AI are part of the package.** The 4.4 changelog
documents enabling retail and Core Contingency campaigns; 4.5 documents
campaign build-menu, campaign AI and Core mission 1 display fixes; 4.7 documents
correcting Core Contingency mission 6 maximum wind from 9 to 900. The archive
supplies campaign descriptors, briefs and `camps/useonly` restrictions,
`ai/MISSIONS.txt` alongside skirmish profiles, `maps/CC01.TNT` and
`maps/EXP1CC06.OTA`. Those are overlays on original mission assets, not a
self-contained campaign distribution. The 4.5 AI changes also include nuke
and anti-nuke use, stockpile queue limiting, low-energy appliance shutdown
and a higher builder threshold. Authored AI files and documented engine
changes are separate requirements; loading an `ai` file proves neither the
patched decision logic nor campaign progression.

**Established — music configuration is separate from content layout.** The
4.8 notes include WGMUS 0.0.24; `wgmus.ini` selects MP3 files, folder playback
and `MusicFolder=music`. The bundled WGMUS readme instead describes `tamus`
as its default and warns that filename ordering depends on consistent number
padding. No music directory or tracks appear in the inspected 4.8 package
alone. The coexistence of `tmusi.dll` and `wgmus.dll` does not identify the
active backend. The authorized shipped-binary audit under
[Versioned music documentation](#versioned-music-documentation) instead traces
the executable import to `win32.dll` and establishes its enumeration and
empty-folder behavior. No third-party implementation code is required to
reproduce the authored music-directory contract independently.

### Package acceptance cases

These are acceptance requirements derived from the established sources above,
not reported passing tests or new gameplay authorization:

- Mount original assets followed by the identified 4.8 archive; resolve
  `SIDEDATA`, GUI pages, pictures and weapons from the renamed trees while
  preserving winning archive provenance. Show the new Spark, Blaze and Apex
  entries and both faction commanders from their authored definitions.
- Capture the Arm and Core twelve-slot build menus, hotkey overlays, page
  changes, unit portraits and pink/slate team colors with the package's own
  palette/shade/interface assets. Exercise the distinct shipyard exit variants
  rather than introducing one generic rotation command.
- Enter an original campaign and a Core Contingency mission, apply their
  use-only restrictions, issue build orders through the campaign GUI and
  advance to the next mission. Check the documented Core mission 1 layout and
  Core Contingency mission 6 wind overlay with their actual base maps.
- Exercise campaign and skirmish AI separately, including low-energy
  shutdown, stockpiles and the named difficulty resource factors; do not infer
  the patched AI behavior from successful catalog loading.
- Give a fire-at-will armed constructor each of the five patched work orders
  with an enemy in range, then the same with a construction aircraft and with
  `RepairUnitNoMove`; only the five ground cases should fire while working.
  Place map-authored and mission-authored walls outside LOS and compare which
  are drawn.
- Verify music configuration with an explicitly identified backend and
  user-supplied tracks. A silent launch without that backend evidence is not
  evidence of music compatibility.

### Shipped selection and hotkey audit

**Established — ProTA 4.8 draw DLL, authorized binary audit.** The installed
window procedure calls the extension selection handler before forwarding an
unconsumed event to the game. The extension must be active and the game must
be in battle. The handler reserves these inputs; Shift variants below are
explicitly left to the ordinary game handler, not alternate extension cycles.

| Input | Shipped extension action |
|---|---|
| Ctrl+B without Shift | Deselect, find the next idle member of the constructor mask, select it and centre the camera. |
| Ctrl+F without Shift | Deselect, find the next idle member of the factory mask, select it and centre the camera. |
| Ctrl+S without Shift | Replace selection with eligible own units from the on-screen list that belong to `CTRL_W` and cannot fly. |
| Ctrl+Shift+B/F | The extension does not consume the key; ordinary authored-category selection remains available, with Shift additive [07 R-CAM-01 §2]. |
| Ctrl+Shift+S | The extension does not consume the key; ordinary on-screen selection remains available [07 R-CAM-01 §2]. |
| Left or right double-click | With `DoubleClick` enabled, replace selection with eligible on-screen own units whose definition occurs in the prior selection. Shift is not additive. |
| W, B or Y held during selection filtering | Keep the weapon, constructor or factory mask, respectively; first held key wins in W → B → Y order. |

**Established — membership and admission.** The weapon mask is authored
`CTRL_W` with `canfly` definitions removed; neither `NOTAIR` nor `NAIR` is
consulted by this implementation. Constructor and factory masks use authored
`CTRL_B` and `CTRL_F` whenever those categories contain any definition. An
empty category falls back to builders that are not airbases, split by nonzero
versus zero `BMCode`, excluding the commander/decoy mask. That exclusion is
not subtraction of `CTRL_W`: the older interface audit misidentified which
mask was used. Drag filtering only revisits already selected, selectable,
completed local records with an owner link; it removes nonmembers instead of
adding other units.

**Established — historical idle-cycle boundary.** Constructor and factory
searches keep separate persistent cursors, start at zero, and accept only a
candidate strictly after their previous index. On exhaustion they reset to
zero and retry once. The upper bound is the player's live unit count,
inclusive, rather than the end of its allocated slot block. Consequently slot
zero is never selected by these cycles and a survivor above the live count
can be missed. Factory candidates must be selectable, completed, have an owner
link, belong to the factory mask, and have no primary order or a primary order
other than `BuildingBuild`. Constructor candidates require a nonzero unit
marker, a prior health sample other than zero or one, constructor membership,
and no primary order or `Standby`/`VTOL_Standby`; they do not inspect secondary
queued work. Camera centring clamps to the map's scroll limits.

**Established — double-click and menu boundaries.** The pointer must be
strictly inside the game viewport and hover an own unit; hovering is only an
admission check and does not add that definition to the selected type set.
The replacement megamap must not be displaying, and the configured `Eye/KeyCode`
(default X) must not be held. This is the autoclick key, despite a misleading
source comment calling it the whiteboard key. The OS supplies double-click
classification. Selection refresh in this historical DLL preserves the
prepared order around the menu refresh; the current source deliberately
cancels it for these selection actions. The later source also fixes the
live-count scan and slot-zero omission, as documented by
[the September 2026 cycle fix](https://github.com/tanvanman/TADR/commit/63d779d87293539894341929a68e8c0a0bd83241).
Nanolathe's optional controls target that current contract; this audit does not
request reintroducing historical faults.

**Established — group-key preference.** ProTA 4.8 explicitly authors
`SwitchAlt=1`: digits recall groups and Alt+digit selects build pages. This is
the existing retail preference [07 R-CAM-01 §4], not an additional selection
algorithm. Unit build shortcuts remain authored GUI gadget inputs. The table
above closes the previously unresolved selection-key assignment for this
specific DLL; it does not claim every other patch UI command has been audited.

### Versioned music documentation

**Established — authored 4.8 music preferences.** `wgmus.ini` selects MP3
files, folder playback and the `music` directory. `ProTA.ini` selects Random
music (`CDMode=2`); it describes Play All, Random, Repeat and Custom as values
one through four. These are host preferences, not simulation rules. Nanolathe
already exposes those modes and portable MP3 playback; selecting the ProTA
content profile does not import its INI preferences.

**Established — versioned upstream instructions differ from the bundled readme.**
WGMUS [release 0.0.24](https://github.com/MnHebi/wgmus/releases/tag/v.0.0.24-alpha),
published 16 May 2025, points to revision
`6d8d098a31cd276d2f76327cc9f75eb32e3aaa52`. Its
[README](https://github.com/MnHebi/wgmus/blob/6d8d098a31cd276d2f76327cc9f75eb32e3aaa52/readme.MD)
instructs the user to install the DLL under the game's existing music-library
name and documents `music` as the default directory. ProTA 4.8's bundled
`wgmus readme.MD` instead instructs editing the executable's library reference
and calls the default directory `tamus`. Both recommend consistently padded
numeric filenames to obtain the desired ordering. ProTA's actual INI explicitly
selects `music`, so neither README default overrides that authored setting.

**Established — shipped music proxy and folder enumeration, authorized binary
audit.** The identified executable imports its MCI and auxiliary music functions
from `WIN32.dll`. That bundled file is a BASS-backed WGMUS-family proxy; it is
not byte-identical to the separately named `wgmus.dll`. Its initialization reads
`wgmus.ini` beside the proxy, with defaults `FileFormat=0` (WAV),
`PlaybackMode=0` (CD) and `MusicFolder=tamus`. File-format selectors zero through
four mean WAV, MP3, OGG, FLAC and AIFF; out-of-range selectors become WAV. The
shipped INI explicitly chooses MP3 folder playback under `music`.

The proxy constructs the folder path relative to its own directory, enumerates
only the selected extension with Windows file enumeration, and stores tracks
in that returned order. There is no lexical or numeric sort. It exposes the
first file as CD track two and the last as the file count plus one, retaining
the data-track convention. An empty or missing folder clears current/next/last
track state and marks playback stopped; it does not select the CD branch or
load another music DLL. CD playback is the separate explicit mode-zero branch.
The proxy forwards unrelated sound functions to the system multimedia library.
Thus filename padding is an authoring recommendation, not proof of a portable
sorting contract. The exact enumeration order depends on Windows and the
filesystem; Nanolathe's documented portable ordering remains a host policy.

**Unknown — alternative installs.** This establishes the shipped executable's
normal import route and the imported proxy's behavior, not every possible
user-replaced DLL, launcher injection, device-driver failure, or undocumented
alternate backend. No corresponding source revision has been assigned to this
particular `win32.dll`; its hash above is the behavioral target. The upstream
WGMUS 0.0.24 repository has no license grant in its root files or README, so
its implementation was not used. The authorized analysis used the shipped
proxy binary instead.

## Unknown

- **Settled (2026-09-24) — regular Core east/west shipyard AI membership and
  mobile anti-nuke generation.** Both former Unknowns are closed as bounded
  static negatives over the shipped package. See the directional-shipyard
  paragraph under
  [Authored package, interface and single-player coverage](#authored-package-interface-and-single-player-coverage)
  and "no shipped route generates mobile anti-nuke rounds" under
  [AI and economy evidence audit](#ai-and-economy-evidence-audit). A manual 4.8
  observation would only corroborate them.
- **Unknown — engine-family provenance (settled for the current line).** The
  relationship between `tdraw.dll`, TA: Escalation's `TAESC.dll` and TA Zero's
  `zdraw.dll` is established for the current generation: one MIT-licensed
  source tree builds all of them under per-build configuration (see the
  evidence scope above). What remains unknown is whether the three shipped
  builds correspond to revisions of that tree, and no Nanolathe implementation
  may rely on their behavior as that tree's behavior.
