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
These are extension interfaces; the current-source sections do not establish
which older shipped DLLs provide the wider set.

**Established — the extended ports are a TA Demo Recorder feature.** The
recorder replaces the per-unit script-port reader and setter while preserving
retail ports `1`–`20`. The current-source interface spans groups from 21 to
400 and includes commands issued through getters as well as setters. The
[dispatch contract](#current-source-dispatch-contract) below records every
handled group, its permissions, substantive helper behavior and remaining
limits. The eight ports in the first table are the extended reads found in
the inspected three-package content census, not the complete engine surface.

**Established — source revision.** The eight-port table below was checked against
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

**Established — the eight census ports are read-only in the pinned source.** The
setter has no arm for these eight numbers, so a script write to them is
silently discarded (other extended numbers do have setter arms). The eight are
answered identically during a demonstration playback (the playback gate that
guards the higher groups does not cover them, and it evaluates to "not a
demo" whenever the network layer is disabled). The recorder's historical 2013
option that moved port 75 between 68 and 75 is not mentioned in the shipped
change log and remains an earlier observation; the pinned source uses fixed
69–75 numbering.

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
accident of kind). The id bound is inclusive of the reported maximum. The
retail pool includes a null sentinel in addition to ten player slices
([04 §2.3](../retail-executable-spec/04-units-orders-scripts-and-movement.md)),
so the inclusive bound alone does not establish a one-past-allocation defect.
The recorder's port-`73` path has no null guard, so content must keep ids inside
the advertised range. These are the
recorder's behaviors, not commitments Nanolathe must reproduce as faults.

## Current-source dispatch contract

**Established — source and scope.** The sections below describe implemented
behavior at TADR commit `dcff5ddeb6bd1030e3f452c0f16e5f005850f62f`, read on
2026-09-22. Their primary sources are the getter and setter dispatch in
[`COB_extensions.pas`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/COB_extensions.pas),
the corresponding unit helpers in
[`TA_MemUnits.pas`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/TAMem/TA_MemUnits.pas),
the map, effect and lookup helpers in
[`TA_MemoryLocations.pas`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/TAMem/TA_MemoryLocations.pas),
and the authoring enumerations in
[`TA_MemoryStructures.pas`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/TAMem/TA_MemoryStructures.pas).
The tables describe the script interface, not engine memory layouts. This is
current-source evidence; it does not establish the larger surface in the older
DLLs shipped with the three inspected packages.

**Established — dispatch and arguments.** Retail ports `1`–`20` pass through
unchanged. A getter receives four signed 32-bit arguments, called `a` through
`d` below. A setter receives one value, `v`, and acts on the calling unit.
An unhandled getter answers zero; an unhandled setter does nothing. A command
issued through a getter still answers zero unless a result is stated. In
particular, setter-only ports have no implicit getter. The implemented getter arms
are exactly `29`, `32`, `69`–`83`, `90`–`96`, `100`–`118`, `130`–`138`, `150`,
`151`, `153`, `154`, `170`–`178`, `190`–`202`, `221`–`223`, `241`–`243`,
`250`–`252`, `270`–`280`, `300`–`303`, `306`–`311`, `313`–`318`, and
`370`–`372`; `400` emits diagnostic text only in a debug build. Every gap in
`21`–`400` therefore reads zero, rather than inheriting a neighboring port's
meaning. The complete setter set appears below.

**Established — gating.** In the getter tables, **live** means suppressed
during recorded-game playback, with zero returned and no operation. The
predicate permits the call whenever networking is disabled; otherwise it
requires the recorder's not-playing-a-recording state. Unmarked getters have
no such gate, even when they mutate state. This is not a general read-only
playback interface. Setter gates are separate. No getter acquires a general
local-owner check; local checks mentioned below come from a specific arm.

**Established — identifiers and coordinates.** Unit-id helper calls narrow
to sixteen bits. Type checksums identify the first matching catalog definition;
checksum zero has no match. Direct type-index lookup lacks a range guard.
Where the table says `unit(a)`, zero selects the caller only when explicitly
stated. Most packed positions place integer X in the high word and integer Z
in the low word; port `106` reverses those components, and port `137` uses a
different short-position interface with X low and Z high. Coordinates returned by `100`–`102` are
the unsigned upper words of fixed-point coordinates, not a signed clamp.
Many helpers assume valid, occupied units, valid indices and loaded tables;
there is no shared validation layer or universal failure result.

### Identity, players and unit state

**Established — getter contracts.** The eight content-used ports retain the
[port table](#port-table) above. The remaining low groups are:

| Port | Arguments and result or action |
|---|---|
| `29` | Current raw movement speed of `unit(a)`, zero meaning caller; zero when it has no movement state. This is not a percentage. |
| `76` | Whether `unit(a)`'s owner lists the local player as allied; zero addresses unit slot zero. The direction differs from testing the local player's alliance flags. |
| `77` | Type checksum of `unit(a)`, zero meaning caller. |
| `78`, `79` | Convert checksum `a` to type index, or type index `a` to checksum. No checksum match returns zero for `78`; `79` has no bounds check. |
| `80` | Whether type checksum `b` belongs to the caller's no-chase category (`a=0`) or primary/secondary/tertiary bad-target category (`a=1/2/3`). Missing definitions or category sets answer zero. |
| `81`–`83` | Prior linked unit, transporter, or first transported unit id of `unit(a)`, zero meaning caller; an absent link yields zero. These are links, not a list of all cargo. |
| `90`–`93` | Player `a`'s active flag as 0/1, controller code (1 local human, 2 local AI, 3 remote), side index, or kill count. These do not default to the calling owner. |
| `94` | Player `a`'s economy value, rounded with the source runtime's `Round`: `b=1/2` current energy/metal, `3/4` energy/metal production, `5/6` energy/metal capacity. Other selectors return zero. |
| `95` | Raw engine unit-visibility result for `unit(a)` as seen by the **local player**, provided `a` is nonzero and the unit has an owner. It does not accept a player selector. The result is not normalized to 0/1 here. |
| `96` | 0/1 map-position visibility for player `a` and packed position `b`, after successful position lookup. |
| `100`–`102` | Integer X, Z, Y of `unit(a)`, zero meaning caller. |
| `103`–`105` | Sixteen-bit X, Z, Y turn values of `unit(a)`, zero meaning caller. |
| `106` | **Reversed selection in this source:** nonzero `a` reads the caller's grid position; zero `a` reads unit slot zero. Packs grid X low and grid Z high. |
| `107` | Current health value of `unit(a)`, zero meaning caller; no percentage conversion. |
| `108` | **live:** damage type `a`, amount `b`, source unit `c`, target unit `d`; see health commands below. |
| `109` | Call the engine heal operation with worker `unit(a)`, target `unit(b)`, and worker-time divided by 30; return the engine result. No playback gate and no zero-to-caller conversion. |
| `110` | Current cloak-state flag of `unit(a)`, zero meaning caller, normalized to 0/1. |
| `111` | **live:** request cloak when `a=1`, otherwise clear the request; target `unit(b)`, zero meaning caller. This changes the request, not the actual state read by `110`. |
| `112`, `113` | Return the target's basic or full engine state word; `a=0` means caller. Decoding those words requires the retail contract, not a new extension layout. |
| `114` | Set selectability of `unit(b)`, zero meaning caller, exactly when `a=1`; clear otherwise. The flag matches the retail selectable predicate ([04 §1.1](../retail-executable-spec/04-units-orders-scripts-and-movement.md)); this alone does not bypass construction, capture or transport restrictions. |
| `115` | **live:** `a=0/1` detaches/attaches transported unit `b` and transporter `c`, piece `d`; `a=2` returns the first unit attached to the caller at piece `b`. Even this query is playback-gated. Existing attachment is removed before attach/detach. |
| `116` | Choose a free caller piece between `a` and `b` inclusive. See the random-helper limitations below. |
| `117` | Store custom bar current=`a`, maximum=`b` for the caller. When `b=0`, clear both; no normalization or clamping on assignment. |
| `118` | Caller metal-extraction ratio multiplied by 100, truncated toward zero. |

**Established — health commands.** Port `108` delegates damage types 1 and 2
(weapon and paralysis) with the source-to-target heading when both units are
present, otherwise zero heading. Type 10 invokes healing only below the target
definition's maximum health. Other damage types pass through with heading
zero. The helper reads the target definition before its conditional null tests,
so a null target is not made safe by those tests. The port discards the helper's
post-operation health return. Port `109` forwards fractional worker-time/30 to
the engine; the engine's resource spending, healing amount and admission are
not reimplemented by the recorder. The exact engine behavior must be matched
to retail evidence before implementing either delegated path.

### Weapon, lifecycle and spawning commands

**Established — getter contracts.** Weapon selectors below are the port
numbers `130`, `131`, `132`, not one-based weapon slots.

| Port | Arguments and result or action |
|---|---|
| `130`–`132` | Caller primary/secondary/tertiary weapon id, zero for no weapon. |
| `133` | Unscaled kills of `unit(a)`, zero meaning caller. |
| `134` | Caller's recorded attacker id, zero for no attacker. |
| `135` | Locked unit target of weapon selector `a`; zero unless that weapon target is a unit. Selector validation is absent. |
| `136` | Caller's raw recent-damage value, not a Boolean conversion. |
| `137` | **live:** invoke weapon selector `a` with target unit `b`, or short-position `c` when target lookup is null. Return the selected fire callback's result. It temporarily marks the weapon as firing and clears that mark afterward. Missing weapon or unsupported firing family returns zero. This bypass entry is not evidence for normal attack admission, reload or spending. |
| `138` | `a=0`: engine stockpile-build progress; otherwise current stock of the caller's primary weapon. |
| `150` | Give unit `a` to player `b` through the engine give operation. No playback gate; null unit does nothing. |
| `151` | **live:** create type checksum `a` at packed position `b`, owner `c`, initial state `d`; owner 10 means the caller's owner. Return the created id or zero. |
| `153` | **live:** kill unit `a` using mode `b`, with modes shared by setter `152`. |
| `154` | **live:** create minions of type checksum `a`, count `b` narrowed to a byte, initial action `c`, result-array selector `d`; details below. |

**Established — direct creation.** Port `151` first resolves the ground
position, then the type. With nonzero cruise altitude and initial state 6 it
sets fixed-point Y to `(terrain height + cruise altitude - sea level) × 65535`.
The multiplier is **65535**, unlike the 65536 multiplier used by minions.
It calls the engine creator with no supplied orientation and no random
orientation, and on success emits build-finished notification when networking
is enabled. It does not perform an extension-side resource deduction, build
spot test, or explicit local-owner check. Type lookup failure is not guarded.
Creation limits and constructor failure remain delegated to the engine.

**Established — minion placement and return.** Port `154` computes a ring
radius from separately rounded footprint diagonals: for the caller's catalog
definition and the requested minion definition,
round `14 × sqrt(footprintX² + footprintZ²)`; add the rounded results, then
round the sum times 1.4. For each requested minion it tries at most 25 positions.
Each attempt draws an integer in `0..254`, halves and rounds it for radial
jitter, and draws an integer in `1..360` for the angle. The source passes that
angle directly to sine and cosine, whose unit is radians; it does **not**
convert degrees. Add the resulting offset to the signed integer caller X/Z,
round the coordinates, and require both coordinates strictly greater than
zero and strictly less than map width/height. Successful terrain lookup is
followed by the engine build-spot test. Flying types bypass a failed build-spot
test; other types must pass it.

Accepted air positions use cruise altitude ×65536 when cruise altitude is
positive and initial state 6. Ground positions use terrain height ×65536 when
height lookup does not return -1, and state 1. Creation uses the caller's
owner and does not randomize facing. Successful minions receive a
build-finished notification when networked; action zero adds no order, while
other actions issue an order targeting the caller with queue flag 1.
Successful ids enter the minion result list.

The ordinary return is the number of **accepted positions**, recorded before
creation, not necessarily the number of successful units: a failed creation
can accept another position on the next retry. Selector `d=65535` instead
returns the last creation result's unit id; `d=0` stores no result list; another
selector stores successful ids only if that array has no existing contents.
Failure to store a nonempty result in a newly requested array changes the
return to zero. An occupied array is left untouched. These distinctions matter
at unit limits and when callers reuse arrays.

**Established — kill modes.** Setter `152` kills the caller and getter `153`
kills its explicit target. Mode 0 applies 30000 of engine damage type 3; modes
1 and 2 first invoke the two explosion variants, then apply 30000 of type 4;
mode 3 applies type 4 directly; mode 4 queues self-destruct with queue flag 1;
mode 5 sets the engine removal-related state flag. Other values do nothing.
The distinction between engine damage types and the later death lifecycle is
not supplied by these wrapper calls.

### Searches, result arrays and order commands

**Established — search interface and selection.** Ports `170`, `171`, `172`
search respectively within range, within the caller's inclusive three-axis
model/footprint box, or across the map. Arguments are filter mask `a`, range
`b`, result-array selector `c`, optional type checksum `d`. Only range search
uses positive `b`; nonpositive range removes that distance restriction.
Port `177` returns the nearest match using filter `a`, range `b`, optional
type checksum `c`. Searches traverse ids in ascending order, skip the caller
and empty slots, and compare a requested type by definition identity, so a
private copied definition need not match its original catalog definition.
Missing type checksums remove the type restriction. Selector `65535` returns
the first accepted id immediately. Other selectors store ascending ids only
into an empty search array and return the count; no matches, or an occupied
array, yields zero. Nearest search retains the first id on a distance tie.

**Established — search masks.** Mask bits with values 1, 2, 4 select owner,
allied, enemy; 8 admits local AI when the relationship/visibility filter is
active; 16 excludes flying types; 32 excludes floaters; 64 excludes types
with building movement code zero; 128 excludes types without weapons; 256
includes unfinished units; 512 includes transported units; 1024 requires
nonzero engine visibility for the caller's owner. Relationship priority is
allied, else owner, else enemy. Without bit 8, local AI is excluded only
inside the relationship-or-visibility branch; remote-controller units are not
classified as AI by this test. Transported and unfinished units are excluded
by default. Extra mask bits have no tested behavior here.

**Established — two distinct distance algorithms.** Range admission compares
`floor(dx²/2³²) + floor(dz²/2³²) <= range²`, using fixed-point coordinate
differences and wide products. Nearest ordering and port `178` instead first
discard the fractional part of each absolute component, then round the
Euclidean length of those integer components. Port `178` measures between
units `a` and `b` when `b` is nonzero, otherwise caller and `a`. Height never
participates. Implementations must not replace both algorithms with one
floating-point distance comparison.

**Established — result-array commands and randomness.** Port `173` reads
one-based entry `b` in array `a`, selecting search/minion family by `c=1/2`;
a missing array returns zero but the entry index is unchecked. Port `174`
shuffles array `a`, family `b`, and returns 1 even when nothing changes.
Port `175` returns a randomly sampled free array id for family `a`, selecting
from `1..65534` and giving up after 101 unsuccessful draws. Port `176` clears
array `b`, family `a`. Arrays are recorder-global, not private to a script
or owner. Initialization provisions the two families for indices `0..65534`;
`65535` is a search/create sentinel, not a storage index.

The recorder uses the host language's random generator for these helpers;
minion creation, free-piece selection and array shuffling explicitly reseed
it. This is not a use of the retail simulation RNG. Free-piece selection
removes nonzero occupied attachment-piece numbers from the inclusive requested
range and returns zero when none remain. Its stored candidates are signed
bytes. Its shuffle and the search-array shuffle walk backward, drawing a
partner from 1 through the current index, with an additional index-zero
iteration. The minion-array shuffle has an inverted presence condition and
does not shuffle an existing list. Exact seeded sequences and the runtime's
zero-bound random behavior remain Unknown; treating these operations as an
unbiased deterministic shuffle would not reproduce the inspected source.

**Established — order dispatch.** None of the following are setters.
Arguments denoted low/high are unsigned sixteen-bit halves.

| Port | Arguments and result or action |
|---|---|
| `190` | Cancel and release the caller's current order through the recorder cancellation helper. No playback gate. Its linked main/suborder repair needs a separate retail-order match before implementation. |
| `191` | Current action code of `unit(a)`, zero meaning caller; 68 when no order. |
| `192` | Caller order target packed X high/Z low; zero without an order. Construction targets use the building-position conversion before packing. |
| `193` | Caller order target unit id, zero for no target/order. |
| `194` | Caller order parameter 1 if `a=1`, otherwise parameter 2; zero without an order. |
| `195` | **live:** set parameter selected by `a` to `b`; answer 1 when an order exists, else 0. |
| `196` | **live:** issue caller action `a`, no unit/position target, queue flag `b`, parameters `c`,`d`. |
| `197` | **live:** caller action `a`, queue flag `b`, packed position `c`, parameters low(`d`), high(`d`). |
| `198` | **live:** actor unit `b`, target unit `c`, action `a`, queue flag low(`d`), first parameter high(`d`), second parameter zero. |
| `199` | **live:** actor unit `b`, packed position `c`, action `a`, queue/parameters as `198`. |
| `200` | **live:** caller action `a`, target unit `b`, packed position `c`, queue/parameters as `198`. |
| `201` | **live:** clear the caller's main-order reference directly; does not invoke `190`'s cancellation helper. |
| `202` | **live:** `a=1` queues `b` stockpile builds; `a=2` adds `b` to stock in one-based weapon slot `c`; `a=3` queues `b` factory builds of type index `c`. Other selectors do nothing. |

Position-bearing order commands require successful position lookup. Order
issuance returns the engine order routine's result; queue flags narrow to a
byte. The interface forwards action codes rather than defining new opcodes.
The action handlers and their ordinary resource/admission effects remain
engine behavior, with any recorder overrides separately evidenced.

### Definition editing and setter permissions

**Established — complete setter dispatch.** Setter values target the caller,
except that GUI selection and catalog definition references can affect other
units sharing that type. `Local` means a local human **or local AI** owner;
`live` uses the playback predicate defined above.

| Gate | Ports | Action |
|---|---|---|
| None | `102` | With `v=-1`, release forced height; otherwise remember forced integer height `v` and immediately set fixed-point Y to `v × 65536`. The continuing override is consumed by the mod-only unit-action hook. |
| None | `303` | Apply gamma `v/10` and store integer gamma `v`. |
| None | `304` | Set camera fade level to `v`; the map hook draws only when nonzero and passes `v-31` to the transparency operation. |
| None | `305` | Change mouse drawing from `v` and lock mouse input exactly when `v=0`; any nonzero value releases the lock. |
| None | `312` | Load the terrain variant selected by the low byte of `v`; see map commands. |
| Local | `118` | Store extraction ratio `v/100`. |
| Local | `224` | Store GUI variant `v` for the caller's type. Variant zero requests ordinary `<unit><page>.GUI`; nonzero requests `<unit><page>_<variant>.GUI`. This is type-wide state. |
| Local | `225` | Value 1 enables the mobile-plant state and clears its paired basic-state flag; all other values reverse both. The downstream movement/yard interpretation is not established merely by the write. |
| Local | `240` | Invoke caller speech category `v`, with no text argument. |
| Local and live | `21` | Mark current attack-family actions 2–10 or stationary guard action 22 as aim-aborted; clear target id for weapon selector `v=130/131/132`. The order effect does not depend on a valid weapon selector. |
| Local and live | `22` | Reset reload countdown to zero for weapon selector `v=130/131/132`. |
| Local and live | `29` | Assign raw movement speed `v` when movement state exists; no percentage conversion. |
| Local and live | `77` | Replace caller type reference/index with type checksum `v` and broadcast the swap when networked. This does not recreate the unit or migrate its other state. |
| Local and live | `103`–`105` | Assign X, Z, Y turn from low sixteen bits of `v`. |
| Local and live | `130`–`132` | Replace the selected weapon definition using id `v`, then broadcast when networked. No reload/stock reset is present in this helper. |
| Local and live | `152` | Apply kill mode `v` described above. |
| Local and live | `155` | Create checksum `v` at caller location/owner, copying only its Z turn, initial state 1; only if creation succeeds, kill the old unit with mode 3. It does not preserve health, experience or orders. |
| Local and live | `220` | Value 1 grants a private copy of the current definition; other values restore the catalog definition reference. Broadcast the grant/release when networked. |

**Established — private definitions and editing.** Getter `221` takes field
selector `a`, unit `b` (zero means caller). It prefers the stored private
copy when one exists, otherwise the unit's current definition. Getter `222`
changes caller field `a` to `b`, negating `b` first when `c` is nonzero. It
requires a previously granted private copy, but has no playback or local-owner
gate of its own. On helper success it broadcasts the edit when networked;
the port still returns zero. Releasing a grant changes the unit's definition
reference but does not clear the stored private copy, so later get/edit calls
can still address that retained copy. Repeated grants allocate another copy.
These are source behaviors, not a recommendation for ownership management.

**Established — field vocabulary and arithmetic.** Selectors and semantic
fields below are the author-facing interface. Unlisted selectors read zero
and perform no field assignment. A private copy's presence is enough for the
editing helper to report success even for an unhandled selector.

| Selectors | Fields and conversion |
|---|---|
| `1`, `4` | Energy, metal storage. Reads are raw integer amounts. **Setter 4 writes energy storage**, not metal storage, in the inspected source. |
| `2`, `3`, `5`–`8`, `13`, `14` | Energy make/use, metal make/use, tidal/wind production, cloak cost/stationary and moving. Reads truncate value×100; use selectors 3 and 6 additionally take absolute value. Writes divide by 100 and preserve the supplied sign. |
| `9` | Makes-metal byte. |
| `10`–`12` | Energy cost, metal cost (truncated), build time. Read-only in this helper. |
| `18`, `19` | Acceleration/braking: truncate value×100 on read, divide by 100 on write. |
| `20`–`30` | Bank scale, turn rate, cruise altitude, maximum slope, raw maximum speed, minimum/maximum water depth, maximum water slope, waterline, maneuver leash, attack-run length. No extra scale conversion. |
| `31`–`36` | Hover attack, upright, flying, hovering, amphibious, floater flags. Reads 0/1; writes enable only for value exactly 1. |
| `37`, `38` | Assign movement-class index and copy that class's water-depth/slope restrictions. Selector 37 additionally recreates movement state while preserving Z turn. Both read zero. |
| `40`–`49` | Maximum health, damage modifier, hide-damage flag, heal time, movement code, footprint X/Z, build distance, builder flag, worker-time. Footprint selectors 45/46 are read-only; other fields are assigned without resource or health rescaling. |
| `51`–`59` | Sight, sonar, sonar jamming, radar, radar jamming, kamikaze flag/radius, minimum cloak distance, shield range. Sight and radar writes also invoke LOS refresh; the other radius writes do not. Shield range lives in per-unit extension state but editing still requires a private definition. |
| `63`–`75` | On/off, commander, transport capacity/size, nontransportable, airbase, targeting upgrade, teleporter, digger, stealth, paralysis immunity, has-weapons, anti-weapons. Capacity/size are bytes; the others are exact-1 flags. |
| `81`–`90` | Stop, attack, guard, patrol, move, load, reclaim, resurrect, capture, special-weapon capabilities, as exact-1 flags. Capture changes the paired engine capability flags together. |
| `94`–`96` | Explosion weapon id, self-destruct weapon id, sound category; write-only here. |
| `97`, `98` | Feature flag (read-only) and display-player-name flag (read/write). |

Writes narrow to the field's authored scalar domain: makes-metal, waterline, movement code and
transport capacity/size are bytes; turn rate, cruise altitude, leash, attack
run, maximum health, heal time, build distance, worker-time, sensor/radius
values and sound category are unsigned words; depth values are signed words;
slope values are signed bytes. The helper adds no validation or clamp.
Getter `223` is a separate **catalog-wide** operation: for a local-human
caller only (AI excluded), replace checksum `a`'s build limit with `b` and
return the old limit. It has no playback gate and no broadcast here.

### Sound, animation, script calls and shared data

**Established — effect commands.** Port `241` is **live**: play sound id `a`
at packed position `b`, requesting network broadcast only when `c=1`, after
successful position lookup; return the sound helper's result. Setter `240`
uses the unit-speech helper, while mission sound ports select names from the
map's sound table. They are different namespaces.

Port `242` plays animation selector `a` at packed position `b`, glow `c`,
smoke `d`. Selector/glow/smoke narrow to bytes. Glow/smoke zero become -1;
other values become value minus one. Selectors 0–5 select `explosion`,
`explode2`, `explode3`, `explode4`, `explode5`, `nuke1`; 6–99 index additional
explosions from zero after subtracting 6; 100–199 index custom animations
after subtracting 100. It returns zero even after drawing. There is no
playback gate. Additional arrays need only be nonempty to enter the indexing
path; selector bounds are not checked against their actual lengths.

The mod-only
[`GAFSequences` loader](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/GAFSequences.pas)
loads consecutive `Explode6`, `Explode7`, … and `CustAnim1`, `CustAnim2`, …
from `anims/customfx.gaf`, stopping each series at its first missing name.
This gives the indices content-defined meanings; the selector alone does not
name a fixed effect beyond the first six.

Port `243` emits from caller piece `a` toward unit `c`, effect type `b` narrowed
to a byte: 6 ordinary construction particles, 7 reversed construction
particles, 8 teleport effect. Other types leave the local effect result zero.
The target's definition and piece zero are accessed without a null guard.
It returns 1 exactly when the effect helper reports nonzero. Broadcast
requires all three: caller owner is the local player, `BroadcastNanolathe`
enabled, networking enabled; no playback gate is applied here.

**Established — custom-bar consumer.** The
[`GUIEnhancements` draw path](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/GUIEnhancements.pas)
uses port `117`'s values only for a finished unit whose definition enables
`customreloadbar`, with positive stored maximum. A teleport-reload bar can
supersede them. Negative current values are clamped to zero when drawing;
there is no corresponding maximum clamp. The inner fill width is
`Round(32 × current / maximum)`. Custom bars bypass normal veteran reload
adjustment and stockpile-progress substitution. The definition key defaults
to false in the
[`UnitInfoExpand` loader](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/UnitInfoExpand.pas).
A script writing the port alone therefore does not guarantee a visible bar.

**Established — invoking another unit's script.** Port `250` queries unit
`a`, method selector `b`, with initial parameters `c`,`d`,0,0. Method selectors
0–4 map to `Activate`, `Deactivate`, `Upgrade`, `Reminder`, `Cloak`; no
arbitrary string name is accepted. Null unit or missing script state returns
zero. If the engine query reports success code 1, the port returns the query's
first parameter after execution; otherwise it returns the engine status.
Port `251` starts the same selected method on unit `a` with exactly two
arguments, `c` and `d`, passing the engine's immediate-start option (named
“guaranteed” by the wrapper). It returns zero. Neither port is playback-gated or aliases unit zero to the
caller. Invalid method selectors have no guard in the wrapper. The source
binding matches the retail argument-carrying name-start adapter:
a successful immediate start drains all active slots with zero time delta,
then runs a zero-delta piece pass; it does not guarantee allocation under a
full slot pool. Query `250` uses the synchronous query adapter, which can
snapshot a script that has yielded without completing ([04 §4.2](../retail-executable-spec/04-units-orders-scripts-and-movement.md)).

**Established — shared data and lifetime.** Port `252` with false `a` reads
entry `b`; with true `a` stores `c` there and returns zero. The array has 1024
signed integer entries, indices `0..1023`; no bounds check or player partition
is present. The
[`ExtensionsMem` lifecycle](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/ExtensionsMem.pas)
clears shared data and both result-array families during extension-state
initialization/cleanup. The
[`SaveGame` extension path](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/SaveGame.pas)
saves/restores both array families and submits **1024 bytes**, not 1024
integers, for shared data. Thus the source requests persistence for the first
256 four-byte values only. Persistence of all shared entries must not be
assumed from the array declaration. No network broadcast occurs for shared
writes or search-array operations.

### Map, mission and arithmetic ports

**Established — map-query group.** These getter arms have no playback gate.

| Port | Arguments and result or action |
|---|---|
| `270`, `271` | Sea level and the map's raw lava flag. |
| `272` | If `a` is nonzero, assign map surface-metal value `a`; return the resulting value. Zero is a read, so this command cannot set zero. |
| `273` | Occupying unit id at packed position `a`, after successful position lookup; use the primary grid occupancy value when nonzero, otherwise the secondary occupancy value. |
| `274` | Test whether unit `a` can attach/unload at its own current position, using its own definition and build-grid conversion. |
| `275` | Test buildability of checksum `a` at packed position `b` for the caller's owner; true only for engine result exactly 1. Missing type/player gives zero. |
| `276` | Forward caller and requested yard state `a` to the engine's open/close-yard predicate; its name does not establish an inverted “occupied” result. |
| `277` | Rebuild caller footprint through the engine helper and return its result. |
| `278` | Feature definition at grid X=`a`, Z=`b`; nonnegative feature id narrowed to a word, otherwise zero. |
| `279` | Feature type `a`, property `b`: selectors 0–11 are respectively object present, animating, transparent animation, transparent shadow, flammable, geothermal, blocking, reclaimable, auto-reclaimable, indestructible, suppress info, suppress drawing under grey fog; 12/13 footprint X/Z, 14 height, 15 damage, 16/17 metal/energy truncated toward zero, 18 whether the definition name contains an underscore. |
| `280` | Grid X=`a`, Z=`b`; `c=0/1/2` returns raw extracted-metal, height, or yard-type value, zero for a missing grid or another selector. |

**Established — mission commands.** The following table records each live
arm, including delegates whose deeper behavior is still Unknown. Their
number range is not an adoption decision. The map tables and continuing map
script runner are loaded only by the mod-id-greater-than-one plugin path;
see [recorder script integration](ta-demo-recorder.md#current-source-script-integration).
All getters here operate during playback as well.

| Port | Arguments and result or action |
|---|---|
| `300` | Scroll view to `a`,`b`, with Boolean option `c` forwarded. Exact scrolling/clamping remains an engine delegate. |
| `301` | Set camera target to unit `a`, zero meaning caller. |
| `302` | Camera shake magnitudes `a`,`b`, duration `c`: add each nonzero magnitude to the stored magnitude; replace the averaging accumulator with integer `(old+c)/2`, store duration `c`, enable shake when the new accumulator is positive. |
| `303` | Current integer gamma; writing uses setter `303`. |
| `306` | Deselect all units and refresh the in-game GUI. |
| `307` | Play map sound-table name at index `a` through the 2D sound operation with option `b`; missing table does nothing. |
| `308` | Play map sound-table name `a` through the 3D operation with option `b`. Its position input is taken directly from the argument storage beginning at `c`, rather than the usual packed-position conversion; a portable authoring contract remains Unknown. |
| `309` | Smoke at integer X=`b`, Z=`c`: `a=0` infinite smoke with parameter 4, `a=1/2` grey/black smoke with parameter 9; other selectors do nothing. No successful-position check. |
| `310` | Place map feature-table entry `a` at integer X=`b`, Z=`c`, with Z turn `d`; return the placement Boolean. Only that turn axis is assigned by this caller. |
| `311` | Remove feature at coordinates `a`,`b`, passing true only when `c=1`; deeper remove semantics remain delegated. |
| `313`, `314` | Local viewing-player id and engine game-time counter. No conversion to seconds. |
| `315` | Apply map initial-mission string `b` to unit `a` through the campaign parser. |
| `316` | Fire map weapon id `a` at packed position `b` through the map-weapon helper. |
| `317` | `a=0`: chat text-table entry `c` attributed to player `b`, optionally substituting player `d`'s secondary name when `d` is nonzero; `a=1`: reminder text-table entry `b` with option `c`. Other selectors do nothing. |
| `318` | AI difficulty code: easy 0, medium 1, hard 2. |

**Established — terrain swap boundary.** Setter `312` narrows its selector to
a byte and replaces the current terrain path's extension with
`<selector>.TNT`. The
[`MapExtensions` swap helper](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/MapExtensions.pas)
loads replacement tile graphics, tile indices, terrain heights and minimap,
retains existing plot contents apart from height, and rebuilds height-derived
and radar presentation data. It sizes work using the current map dimensions,
so it is not a supported general map resize. It then examines units' footprint
cells for the lava marker and can kill affected units, choosing kill mode 1
on lava maps and 0 otherwise. The unit-state exemption and downstream terrain
rebuild effects still need semantic matching; this command cannot safely be
implemented as a visual texture swap alone. Repeated calls derive the next
filename from the already-updated path.

**Established — word arithmetic and diagnostic.** Port `370` returns the
unsigned low sixteen bits of `a`; `371` the unsigned high sixteen bits;
`372` combines low sixteen bits of `a` as the result's low word and low
sixteen bits of `b` as its high word. A high sign bit is preserved in the
32-bit script result. Port `400` prints unit id and supplied debug values only
in debug builds, returning zero; release builds simply return zero.

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

**Established — no current-source collision for these reads.** Ports `70` and
`74` are read by both Escalation and TA Zero scripts and have one meaning in
the pinned recorder source. This does not certify the entire interface across
historical builds or supporting plugins selected by mod id; those boundaries
are recorded above and in the recorder reference.

## Unknown

- **Unknown — older shipped builds' wider interface.** The current-source
  dispatch does not establish which additional getter/setter arms, callbacks,
  map commands or slot-limit changes exist in `3.9.2.0` or `3.9.2.416`.
  Release-matched primary documentation/source or bounded manual observations
  would settle each target separately.
- **Unknown — engine delegates and raw state interpretation.** Full health
  and damage admission, fire callback admission/cost, order cancellation and
  issuance, placement/yard predicates, speech ids, screen scrolling, feature
  removal and map-weapon behavior still require matching to the owning retail
  contracts. Port `225`'s paired mobile-plant state, kill mode 5 and the
  terrain-swap exemption must not be inferred from helper names. Trace the corresponding engine consumers before depending on
  them. Raw state getters are not permission to introduce executable layouts.
- **Unknown — invalid inputs and runtime-dependent arithmetic.** Numerous
  arms lack index/null guards. This source does not establish a safe contract
  for missing types, invalid method/effect/array selectors, slot zero, or
  mission port `308`'s position convention. Port `310` initializes only one
  turn axis. The language runtime's rounding
  mode, host random seeding and zero-bound random results also need an exact
  build/runtime match for bitwise reproduction. Primary authoring examples
  and bounded observations would settle intended inputs; an explicit design
  decision is required for defined Nanolathe failure behavior.
- **Unknown — result-array insertion stability.** The storage helper inserts
  into a prefilled dynamic-array container instead of directly replacing an
  element. Whether later insertions shift previously assigned array ids needs
  a license-reviewed audit of that container or a bounded observation. The
  port-level empty-array guard does not establish stable ids across stores.
- **Unknown — persistence beyond explicitly submitted state.** Shared data
  submits only its first 1024 bytes, and the save extension submits the search
  and minion arrays. Forced height, private definitions and custom bars are
  not submitted by that path. Whether another mechanism preserves them needs
  a save/load caller trace or a versioned observation; do not assume all
  per-unit extension state survives reload.

- **Unknown — the ×100 kill scale on port `32`.** No inspected consumer needs
  it; whether any unpublished content divides it back down is not established.
  A consumer census outside the three packages would settle it.
- **Unknown — how a demonstrably loaded port-`73` out-of-range id is meant to
  behave.** The recorder faults; Nanolathe must choose a defined result. What
  would settle it: a maintainer statement or an observation of the intended
  content pattern.
