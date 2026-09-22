# Extended script ports

## Evidence scope and sources

This document records the engine ports that the shipped mod packages expose
beyond retail's twenty, their semantics, and their cross-package behavior. It
does not establish retail behavior and approves no Nanolathe behavior. The
retail port set is owned by
[04 R-COB-03](../retail-executable-spec/04-units-orders-scripts-and-movement.md).

**Established — retail's port set is closed at twenty.** The retail read
switch rejects identifiers above nineteen and reads zero for them; identifiers
`0` and `21` and above read zero, and writing them does nothing but set the
script-touched marker ([04 R-COB-03 §1](../retail-executable-spec/04-units-orders-scripts-and-movement.md)).
Every port in this document is added by a shipped extension DLL.

**Established — the extended ports are a TA Demo Recorder feature.** The
recorder installs a handler over the executable's per-unit script-port reader
and a companion over the setter. Indices `1`–`20` are dispatched through the
engine's own tables; everything else — including index 0, which has no arm
and reads 0 — falls through to the extension handler. **The extended set is
far larger than the eight ports this document tables.** At the pinned
revision the getter serves grouped ranges from 21 to 400: unit identity, type
and transport queries (21–83), player queries including economy and
per-player visibility (90–96), unit position, turn, health, damage, heal,
cloak, state, attach, bar and mex queries (100–118), weapon and attack
queries (130–138), give, create, kill, minion and type-swap (150–155), unit
searches and distance (170–178), order inspection and issuing (190–202),
unit-template grants and GUI index (220–225), speech and effects (240–243),
script control and shared data (250–252), map queries (270–280), map-mission
camera, fade, sound, terrain and text (300–318), word arithmetic (370–372) and
a debug output arm (400); a setter handler covers a subset with real side
effects (speed, aim abort, weapon readiness, turn axes, type swap, weapon
assignment, kill, template grant, gamma, fade, mouse, terrain swap, forced
height, speech, mobile plant, GUI index, mex ratio), and much of the higher
set and most setters are gated off during recorded-game playback. **The
per-port semantics of that larger set are not yet recorded here**; the eight
ports below are the ones the inspected content authors (see the census), and
they are the ones with no playback gate and no setter arm. One source serves
every per-mod recorder build, so nothing in the handler varies by mod. The
source unit is named `COB_extensions`; the plugin registers under a
human-readable name that no user-facing status command surfaces at this
revision.

**Established — source revision.** The per-port semantics below were read from
the recorder source in the TADR repository (MIT,
`https://github.com/tanvanman/TADR` at commit `dcff5dd`, recorder distribution
2026.9.9; see [Community patch engine behavior](community-patch-engine.md)).
The earlier inspection of shipped `3.9.2.x` binaries agrees with this table
except where noted; those builds remain a distinct version lineage.

**Established — installation is gated but always on.** The plugin is created
after a host-version check (a cached three-byte pattern at one location in
the host executable; a mismatch, or a fault while probing, logs the expected
and found bytes and creates no plugin); it is registered unconditionally,
unlike neighbouring plugins gated on the mod id or on settings; the handler's
enable flag is a typed constant initialised on with no writer anywhere; and
no recorder command toggles a plugin (the engine exposes only bulk install and
uninstall). The retail read path is preserved by re-entering the retail
switch tables.

**Established — the eight ports are read-only and version-independent.** The
setter has no arm for these eight numbers, so a script write to them is
silently discarded (other extended numbers do have setter arms). The eight are
answered identically during a demonstration playback (the playback gate that
guards the higher groups does not cover them, and it evaluates to "not a
demo" whenever the network layer is disabled). The recorder's historical 2013
option that moved port 75 between 68 and 75 is not mentioned in the shipped
change log and remains an earlier observation; only the fixed 69–75 numbering
applies to any build of interest.

## Port table

**Established — the value each port returns, from the recorder source.** The
reading unit is the unit whose script made the request. The handler always
receives the index plus four signed 32-bit argument slots from the engine's
getter frame, whatever the port; the *Form* column is the authoring
convention, not handler behavior — ports `32`, `69`, `70` and `71` consume no
argument at all and `72`–`75` consume only the first. Only the low 16 bits of
an argument are used where a unit id is expected.

| Port | Form | Value |
|---|---|---|
| `32` | one-argument read | 100 × the reading unit's own kill count. The ×100 scale's purpose is **Unknown** (no inspected content divides it back down). |
| `69` | one-argument read | Constant `1`: the lowest valid unit id, independent of the game's real limit. |
| `70` | one-argument read | Highest valid unit id: `10 ×` the per-player unit limit, ten being the engine's player-count constant; the discriminator is whether a map description is loaded (none: the maximum-per-player limit; otherwise the per-mission limit). |
| `71` | one-argument read | The low 16 bits of the reading unit's stored in-game index field, 0 for a null unit. The earlier observation that the 2013 `3.9.2.0` build derived the index from the unit pointer instead is not verifiable from this tree; the two routes agree when the stored field holds the array index. |
| `72` | five-argument read | Owner (player index) of the unit whose id is the first argument, 0 when the id is out of range. Argument 0 addresses unit slot 0; it is **not** an alias for "the reading unit". |
| `73` | five-argument read | Build-percent-left of the unit whose id is the first argument, or of the reading unit when the argument is 0: 0 when no build time remains, otherwise `1 + trunc(99 × remaining build-time fraction)` — 100 just started, 50 half built, 1 for anything unfinished but nearly done, reaching 0 only when exactly finished; derived from build time, not health. There is no null guard: an out-of-range id dereferences a null unit and faults in release builds (the debug build's exception wrapper turns it into 0). |
| `74` | five-argument read | One-directional ally test: the **reading unit's owner's** own ally-flag array indexed by the target unit's owner, 1 if set, 0 otherwise. An out-of-range target id yields owner index 0 (the comparison is against player slot 0, not the reading player); an owner index at or above the ten-player bound, or a null reading unit, answers 0. Argument 0 addresses unit slot 0. |
| `75` | five-argument read | `1` when the target unit is controlled on this computer, else `0`: the owner's controller must be local human or local AI; remote players return 0. Argument 0 addresses the reading unit. Despite the earlier reading of this port as a visibility class, the source test is controller locality, not line of sight, radar or a viewer test. Unlike port `73` this path is wrapped in an exception handler, so an out-of-range id answers 0 instead of faulting. |

**Established — boundary behavior.** Unit ids are used without an occupancy
check (a freed slot yields whatever the engine left behind — a neighbouring
helper does check a definition id for liveness, so the omission is not an
accident of kind); the id bound is inclusive of the reported maximum, so the
advertised maximum addresses one slot past the last real one; and the recorder's port-`73` path has no
null guard, so content must keep ids inside the advertised range. These are the
recorder's behaviors, not commitments Nanolathe must reproduce as faults.

## Authored use

**Established — census.** Reads by package (units whose scripts read the port
at least once):

| Port | Escalation | ProTA | TA Zero |
|---|---|---|---|
| `32` | 2 (commanders only) | — | — |
| `69` | 289 | — | — |
| `70` | 289 | — | 19 |
| `71` | 254 | — | — |
| `72` | 45 | — | — |
| `73` | 260 | — | — |
| `74` | 243 | — | 13 |
| `75` | 44 | — | — |

Escalation's typical pattern is to read `69`, `70` and `71` once to obtain the
unit-id iteration range and its own id, then to call `72`–`75` on candidate
ids inside a loop while its detection state machine runs. A factory's
third-weapon aim callback reads `71` and then the five-argument `75` on its own
id as a "is this mine to draw for" test before showing its direction-indicator
piece. TA Zero's scripts read only `70` and `74`, from construction aircraft,
dropships, factories and their AI variants, alongside retail ports (`4`, `7`,
`9`, `12`, `17`) while setting up build plates and activation state; the AI
factory variants use the two reads to sample unit ids and compare owners when
choosing their build-plate side. ProTA authors no extended-port reads.

**Established — no collision.** Ports `70` and `74` are read by both
Escalation and TA Zero scripts, and the recorder implements them identically;
the numbers are a shared interface, not two competing meanings. Cross-package
compatibility therefore depends only on the recorder version, not on the mod.

## Unknown

- **Unknown — the semantics of the recorder's wider extended set (21–400 and
  the setter arms).** Only the eight ports above are recorded; the grouped
  ranges are named from the source's structure but no per-port contract has
  been written. Settled by a dedicated read of the recorder's extension unit,
  one group at a time, with the playback gating recorded per port.

- **Unknown — the ×100 kill scale on port `32`.** No inspected consumer needs
  it; whether any unpublished content divides it back down is not established.
  A consumer census outside the three packages would settle it.
- **Unknown — how a demonstrably loaded port-`73` out-of-range id is meant to
  behave.** The recorder faults; Nanolathe must choose a defined result. What
  would settle it: a maintainer statement or an observation of the intended
  content pattern.
