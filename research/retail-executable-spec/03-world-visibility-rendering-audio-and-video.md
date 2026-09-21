# Retail executable contract: world presentation, visibility, audio, and video

This document is a clean-room presentation contract derived only from static
analysis of the retail executable. It omits executable addresses, memory
offsets, and raw decompiler identifiers. **Established** means direct static
instruction or data-flow evidence; **supported inference** identifies a
reasonable composition whose final detail is not proven; **unknown** is left
for further executable analysis.

Its subject areas are the renderer and its passes, terrain grids and
deformation, visibility and radar, font and interface drawing, audio mixing and
music, and video playback and capture.

## 1. Presentation boundary and frame model

Retail presentation is an 8-bit indexed software renderer with two output
backends: a GDI windowed path and an exclusive DirectDraw path. Both consume a
software framebuffer and palette. The frame composer reads current simulation
state and cached visual state; it does not advance authoritative simulation.
Simulation and presentation are connected by the main pump, not by a proven
render worker thread.

The renderer’s frame work is staged through exactly **ten fixed-order effect
strips** inside the single frame composer, each closed by a barrier invocation
carrying the pass number 0 through 9. The contract is the numeric order together with
each pass’s gate; semantic pass naming beyond that order is cosmetic. In
numeric order:

1. Terrain/static preparation, minimap/radar preparation, and viewport clip,
   unconditionally.
2. Strips 0, 1, and 2, unconditionally.
3. Screen-Y bucket build, then the first map-cell/object traversal — the
   feature pass, which owns the unexplored-marker logic of section 3.3.
4. Strips 3 and 4, unconditionally.
5. Intervening unit traversals under mixed internal predicates.
6. Strip 5, unconditionally.
7. Strip 6, the projectile pool, the fixed effect pool, strip 7, and the
   remaining unit auxiliary-draw traversal — all gated on the composer’s
   render-mode argument being nonzero.
8. Strip 8 draws **always**, outside the render-mode gate.
9. A key-controlled overlay under its key predicate; then unit labels (each
   gated on an options byte and on the labeled owner equaling the local
   player slot) followed by strip 9 under the render-mode argument; then two
   optional mode overlays under option bits and the same argument.
10. Fog presentation under the render-mode argument, after all ten strips,
    projectiles, and effects but **before** selection/interface work;
    selection rectangle, interface, diagnostics, and present prep close the
    frame under local predicates.

Consequences a reimplementation must preserve: projectiles and effects sit
between strips 6 and 7 and are not strip objects; strip 8 is unconditional;
fog covers everything world-drawn below it but never selection or interface.
Units reach these loops through per-row screen-Y bucket insertion (bucket row
computed from projected screen Y, appended in enumeration order), so paint
order is Y-sorted rows with in-row enumeration order — there is no depth
test.

#### Strip storage and lifecycle

Each strip is a vector descriptor; a draw
dispatcher forwards each stored object to its draw entry, and an update
dispatcher evaluates removal BEFORE update for every object, destroying and
stably compacting on a positive verdict so survivors keep their order. A
terminal condition created during an update is noticed only on the next
invocation. Producers append at the end; when the pre-insert count exceeds
400 the oldest object is destroyed first, so steady state holds at most 401
records per strip and same-strip order among survivors equals insertion
order. Both dispatchers are identified ([R-STRIP-01]): the
update dispatcher is the per-tick sweep of document 01 §4.4 phase 11, and the
draw dispatcher is the frame composer itself, which walks each strip in the
staged order of this section and invokes each object's draw entry with the
framebuffer descriptor; the object's draw entry forwards the call to each of
its sub-records together with the camera origin, which is how sub-records
acquire their screen positions ([R-STRIP-01 §2]).

The producer census is image-wide — every reader of the strip-table root
word, every append invocation, and every call site of all twelve producer
functions. The retail producers take the strip index as the low word of a
stack argument pushed first, often many bytes ahead of the call, which is why
a census that only looks at the push adjacent to the call misses sites. The
producer table and per-strip events are [R-STRIP-01 §1] below; the 0/1/3/8
"none" rows are verified there by two independent exhaustive enumerations
([R-FX-02 §5]).

#### R-STRIP-01 §1 — producer census and per-strip events

Every strip object enters its strip through one of twelve producer functions,
each of which allocates from one shared fixed pool (exhaustion silently drops
the object; a root flag byte disables all strip allocation when set), runs the
family's init virtual, evicts the oldest object when the pre-insert count
exceeds 400, and appends at the vector end. The strip index is a literal
argument at every call site. The complete strip → producer/event map:

| Strip | Producer events (Established, direct-static) | Sites |
|---|---|---|
| 0 | none — no producer exists anywhere in the image | always empty |
| 1 | none | always empty |
| 2 | COB emit-sfx vector types 2–5 (the emit-sfx type-byte dispatch, below): one jittered smoke puff (three CRT draws of `rand×7/0x8000 − 3` per axis) per spawn, spawn interval 1 tick with the per-site spacing parameter (16 or 8) scaling puff lifetime; palette colors `0x61`/`0x67` | 4 |
| 3 | none | always empty |
| 4 | the geothermal steam producer of [05 R-ECO-02 §3]: an object of the vent's smoke-puff class ([R-FX-02 §3]) appended with init `(interval 5, hold 0 → 7, lifetime 150)` under the pool gate and the 400-cap eviction; there is no crater/decal producer on this strip | 1 |
| 5 | teleport order effect (1 site — see [R-LAYER §4]: the producer is the Teleport order-state handler, and the handler's second call is the unit position commit): for every other unit inside the ordering unit's definition-relative world box, a 30-tick flame-stream object that lays one animated segment every 10 ticks with a random start frame while the handler commits that unit to its displaced position; burning-feature smoke (1 site, phase 6 of doc 01 §4.4): one wind-drifted smoke puff every 3rd tick with two CRT jitter draws at the call site and a third, the puff's last frame, inside the producer; init row `(0, 1, 0, 0, 0)` — `smoke 1`, lifetime 0, a one-shot container ([05 R-FEAT-01 §16]) | 2 |
| 6 | construction/reclaim nanolathe emitters: a source point and a target box, five particles per spawn tick over a two-tick spawn window (six CRT draws per particle) | 17 ([R-FX-02 §5]) |
| 7 | flame-stream trail (2 sites): one animated flame segment per tick over a 6–7 tick flight from source to target; smoke sprinkle variant (1 site): the strip-2 family with 8-tick spacing and a 7-tick life | 3 |
| 8 | none (the composer still draws the strip, unconditionally) | always empty |
| 9 | impact smoke: the authoritative impact dispatcher under a weapon-definition flag (1), the projectile phase's trail-window and impact branches (2), the land/water/lava impact effect variants under a second weapon flag (3), the emit-sfx smoke point cases — white `0x101` and black `0x102`, two sites, the fixed-effect-pool append side effect when the effect lands above sea level (1), and the death-corpse finalizer's land-path long-lived (900-tick) smoke column (1 — see [R-LAYER §3]) | 12 |

The strip-6 count is by call site: the two nanolathe producer routines have
ten and seven callers, seventeen sites, every one pushing the literal 6; two
construction routines hold two calls each, so a count by calling function
gives sixteen and is wrong.

**The emit-sfx type dispatch (Established, direct-static).** The strip-2
producer is reached from the COB `emit-sfx` opcode's type-byte dispatch, not
from the impact dispatcher: vector types 2–5 run the strip-2/7 sprinkle
family (types 2/3 source→target, 4/5 with the endpoints swapped; the swapped
pair needs the piece's second effect vertex, whose derivation is
`TODO(question)`), type `0x101` is a white smoke point and `0x102` a black
smoke point (both strip 9), and type `0x103` is the strip-7 water-line
sub-bubble sprinkle.

Two sprinkle-family mechanics: the 16-or-8 value
is the per-site **spacing** parameter, which scales puff lifetime (puffs live
`spacing×6` ticks); the spawn interval itself is 1 tick, and a sprinkle
container holds two puffs (one at construction, one at the single gate fire —
the object's window closes one tick after creation). The smoke family's
animation delay defaults to 7 when a producer passes zero.

#### R-STRIP-01 §2 — object families, update work, and terminal state

Every strip object is a pooled container record holding a dynamic vector of
fixed-stride sub-records (particles or segments). The per-tick update dispatcher
of document 01 §4.4 phase 11 evaluates, per object in insertion order, a
removal verdict virtual BEFORE the update virtual; the update advances each
sub-record's position by its velocity, advances its animation state, removes
expired sub-records (each carries its own expiry tick) with stable in-place
compaction, and may spawn new sub-records through a spawn gate. Both virtuals
are per class, and a reimplementation must take each family's pair from its
own class. For the flame, nano, trail and sprinkle families the removal
verdict is "the internal list is empty" (the container dies once its last
particle/segment expires). "The smoke family" is two classes: the strips-5/9
smoke puffer — impact, muzzle, trail, emit-sfx, burning-feature and
land-wreck smoke, which is all the smoke a player normally sees — has the
verdict "list empty **and** stored deadline passed" and a spawn gate that
compares the next-spawn tick against both the object's window end and the
global tick, so the deadline its producer stores is a real lifetime and a
weapon-side container with lifetime 0 is a one-shot; the geothermal vent's
class alone has a constant-false verdict and a gate with no window term, so a
vent steams until the strip evicts it. The table of the class differences, and
the parts that are identical, is in [R-FX-01 §3]. One family's sub-records
also expire early when the terrain height beneath them falls below sea level —
its marks die on water.

The smoke puff's per-spawn draw is its **last frame**, not its start frame:
the spawn writes `crtRand·(limit − 2)/0x8000 + 2` against the emitter's
frame-limit field — `min(frameCount − 1, frameCap)` when the producer caps it
and `frameCount − 1` otherwise — and the puff's cursor starts at **0**
(`[06 R-WFX-01 §5]` carries the four producers' parameter rows). The particle
is removed when its frame index **reaches** its last frame, which is the
animation-driven expiry this section otherwise leaves to "each carries its own
expiry tick" — the smoke family's expiry is a frame cursor, not a tick
deadline. The staggered last frame is what makes a plume thin out instead of
standing still, and it is why the container holds the entry's frame count from
its init. The draw entry invoked by the composer's per-strip walk forwards each sub-record to a
per-sub-record draw that applies the ordinary projection with the half-height
shear, gates on the local player's mode-selected coverage at the projected
tile, and blits either a GAF frame or a two-by-two filled rectangle (fill
colors: the nano ramp `0xa1..0xa7`; the impact-sprinkle palette colors `0x61`
and `0x67`). Asset bindings: the flame families blit the flame-stream GAF
entry; the smoke family blits one of two smoke GAF entries selected by an init
flag. Sub-record strides are 52 bytes (flame segments of strip 5), 48 bytes
(nano particles of strip 6), 60 bytes (strip 7 trail segments), 68 bytes
(strips 2/7 sprinkle puffs), and 32 bytes (strips 5/9 smoke puffs); container
records are 68, 76, 68, 72, and 56 bytes respectively. The nano particle
carries the unexplained word set to `0x100` at spawn noted in §5.5.

#### R-STRIP-01 §3 — random draws inside the sweep (CRT stream)

The phase-11 sweep consumes no draws at the dispatcher level, but its objects
do, all from the CRT presentation stream — this quantifies doc 01 §7.2's
"object-internal" census row for phase 11: the nano emitters spend thirty
draws per spawning record per spawn tick (five particles × six coordinate
draws); the smoke puffs spend one draw per spawned puff plus one
draw per animation-frame advance (the next frame's delay, drawn as half to
full of the authored delay); the flame-stream segments spend one draw per
segment (random start frame); the impact sprinkle spends three draws per spawn
(per-axis jitter); the strip-7 trail spends none. The composer-time draw
entries consume no draws. All of this randomness is **CRT-stream only**; the
simulation Park–Miller stream is never touched by phase 11.

The drawn *values* are presentation, but the draw *count* is not. The CRT
stream is one per-thread state, and the wind interval, the meteor scheduler
and the skirmish victory-timer arm read that same state (`[01 §7.5]`,
`[01 §7.6]`). Phase 11 of one tick advances the stream before phase 5 of the
next, so a container or sub-record that lives one tick longer — or one the
1000-slot shared pool of `[R-FX-02 §4]` refuses, which spends no draws at all —
moves every later wind change, meteor strike and victory instant. Anything
that decides a strip lifetime is therefore a determinism input even though
nothing it produces is ever read by the simulation.

#### R-LAYER §3 — the wreck-smoke trigger is the corpse finalizer's land path

**Established (direct-static).** The long-lived smoke column of the strip-9
producer census spawns inside the death-time corpse finalizer — the same pass
that resolves the dying unit's corpse chain and stamps the wreck feature — and
not at any point during sinking. Its complete trigger, per death:

1. The death dispatcher passes three values: the dying unit, the corpse-chain
   depth (the death packet's low nibble, [06 §12.1]), and a notification flag
   computed as "the death packet's cause nibble is not the immediate-feature-
   conversion cause" (cause 7 of [06 §12.1]'s severity bypass map) — deaths
   that convert the unit straight into a feature never notify ([06 §12.1]
   cause table; [05 "Feature sinking and water interaction"] "when the death
   cause permits notification").
2. The finalizer walks the `featuredead` chain depth-minus-one times; a
   sentinel chain aborts silently with no wreck and no smoke.
3. It classifies the medium by comparing the interpolated terrain height under
   the victim against the sea-level byte (the same medium predicate that
   decides the descent start of [05 "Feature sinking and water interaction"]).
4. **Land path** (terrain above sea level): the corpse stamps normally and the
   notification flag survives. **Underwater path** (terrain at or below sea
   level): the corpse stamps with the −11468 fixed-point descent latch applied
   when the dying definition lacks the isfeature flag, and the finalizer
   **clears the notification flag** — the underwater path is silent.
5. Only when the flag survives does the finalizer call the strip-9 smoke
   producer at the wreck position with the long-lived variant: a 900-tick
   smoke column (the smoke family's variant and life are the producer's
   second and third arguments; the strip index is its fourth and is the
   literal 9).

**Established (direct-static) — the column's full parameter row.** The wreck
column and the above-sea explosion's land dust are the two call sites of the
**parameterised** smoke producer of [R-FX-02 §5], the one smoke-puff producer
that takes a spawn interval and a lifetime from its caller instead of holding
both as literals. Those two values are the whole of the per-site difference:
the wreck column passes **interval 15, lifetime 900** where the land dust
passes 7 and 15. Frame cap, frame hold and smoke selector are the producer's
own literals and are the land-dust row's — cap 0 (every frame of the bound
entry), hold 0, which the family reads as 7, and selector 0 (`smoke 1`) — so
the emitter's six-argument init row of [06 R-WFX-01 §5] is
`(wreck point, 0, 15, 0, 900, 0)`: one puff at the stamp and one every
fifteen ticks while the window is open, sixty-one puffs over the column's
thirty seconds, each spending the family's one CRT draw.

The trigger is therefore the death/corpse-stamp event itself — a feature-state
transition at death time — not a descent timer, not a height threshold during
the descent, and not any state change of the sinking wreck afterwards. A
sinking (underwater) wreck emits no smoke at all, which is exactly [05
"Feature sinking and water interaction"]'s "underwater stamps are silent"; the
900-tick column is the **land-wreck** smoke. The smoke producer takes a
fourth argument, the literal strip index, at both of its call sites —
consistent with the census's "strip index is a literal argument at every call
site" rule.

#### R-LAYER §4 — strip 5 has no combat producer: the "flame scan" is the teleport order effect

**Established (direct-static).** The strip-5 flame-stream producer is reached
through the **order descriptor table, not any weapon path**. The producer's sole caller is the
handler of the canonical **Teleport** order state (the order descriptor table's
teleport entry — state label, handler, and the acknowledgement-ring
presentation helper of the teleport family; document 04 §3.1 owns the table).
The handler scans every *other* unit inside the ordering unit's
definition-relative world box (six signed world-unit extents stored in the unit
definition around its position), and for each boxed unit does exactly two
things:

1. spawns the strip-5 flame-stream object at that unit's position — a 30-tick
   container that lays one animated 52-byte flame segment every 10 ticks
   between the unit's old position and its displaced position; and
2. commits that unit to its displaced position (destination-relative offset
   preserved) through the direct position-commit path that unstamps and
   restamps occupancy without the movement validator — the same commit the
   carried-unit path uses every tick.

The second call is the position commit, not a combat ignition. **No combat event —
projectile impact class, fire-damage application, or building burning state —
produces strip-5 flame events anywhere in the image.** The flame *weapon*
render type, `firestarter`, and feature fire (document 06 §6.10) have no
producer on strip 5; their presentation effects run through the ordinary
projectile, effect-pool, and feature-fire channels. Document 06 §6.10 carries
the cross-reference.

**Flame segment field layout (Established, 52 bytes),** supplementing
[R-STRIP-01 §2]'s stride table — one segment, in order:

- a word pair naming the GAF sequence to blit: the flame-stream animation
  entry (the same entry the strip-7 trail family uses), with its frame count
  read at spawn time;
- the segment's source world position triple (16.16 fixed), copied from the
  container's current flight position;
- the segment's target world position triple, the flight endpoint for this
  segment;
- the per-step flight vector triple (container total displacement divided by
  its segment count, computed once at container init);
- the segment's expiry tick: spawn tick plus `segLife = floor(spanWorldUnits
  / 5)`, computed once at container init from the source→target span
  ([R-FX-02 §2] has the arithmetic); this word is a segment life, not the
  10-tick re-lay interval below;
- the animation's frame count minus one; and
- the start frame: one CRT-stream draw, scaled by `frame × (frameCount−1) /
  0x8000` — the "random start frame" of the producer census.

The update pass removes expired segments by stable in-place compaction and
re-lays the next segment when the current tick reaches the container's
next-spawn slot — the update loop re-arms that slot to "current tick + 10"
after each lay, which is the established one-segment-per-10-ticks cadence —
and the container dies when its last segment expires
([R-STRIP-01 §2] removal verdict). The random start frame is the flame
family's one CRT draw per segment already counted in [R-STRIP-01 §3].

#### R-WIND-01 — the wind direction vector: which table feeds which axis

Document 01 §7.3 (phase 8) records that the wind direction pair is computed as
−2 × the fixed-point trig of the heading, scaled by the speed; the producer
side is doc 01's territory. This section names the axes from the consumer
side (Established, direct-static): the first word of the pair is
the **X** term and the second word is the **Z** term. The first word is −2 ×
speed × **sin**(heading), the second word is −2 × speed × **cos**(heading),
where the trigonometry is one shared 512-entry sine table of signed 16-bit
entries whose entry *k* is 8192·sin(2π·k/512); the cosine is the same table
read a quarter turn (128 entries) ahead. Both helpers round the product to
the nearest whole world unit (half-up bias), and the phase-8 producer stores
each result multiplied by −2. Verified in two independent consumer families:
the strip-5/9 smoke drift applies the first word to the world-X velocity term
and the second to the world-Z term, and the feature fire-spread probe
accumulates the first word into its X-cell coordinate and the second into its
Z-cell coordinate while walking plot cells X-major. A reimplementation should
therefore publish `windX = −2·round(speed·sin(h))` and
`windZ = −2·round(speed·cos(h))`; the smoke family multiplies the published
words by a further 8 per tick and the fire probe by 2 per probe step, both
factors belonging to those contracts' own scales.

#### Fixed effect pool

Effects are not strip objects: a separate fixed pool
holds up to 300 fixed-size effect records, and appends at or above the cap
allocate nothing. Rendering walks the whole pool once per embedded animation
category and again for model-bearing records, each draw guarded by buffer
admission. Its tick integrator advances velocity against gravity, can restore
a prior position and invert/halve vertical velocity on terrain/water contact
or clear a record’s model pointer, single-steps both embedded animation
players, clears non-looping sequences’ pointers at termination, and removes
emptied records by stable left compaction within the same updater invocation — an
animation terminating during its step retires its record that same invocation,
unlike generic strip objects.

#### Composer-side diagnostics helpers: frame rate, profile buckets, packet-rate lines, and the developer draw hook [R-COMP-01 §5]

These are the helpers the frame composer calls under its developer-mode
predicates (the toggles and the overlay text itself are document 07's;
[07 R-FE-02] owns the developer overlay). The counters measure host activity;
the selected-unit hook reads movement state for presentation. The terrain
diagnostics are specified separately in §3.12, including their CRT draw.
**Established (direct-static)** unless marked.

**Frame-rate counter.** One call per rendering-enabled composed frame while
film mode is on, over three words held beside the display descriptor
(an accumulator in milliseconds, a frame count, and the published figure):

```
acc    += GetTickCount() − last ;  last = now ;  frames += 1
if acc > 2000 : acc = 1000                      // a stall longer than two seconds collapses to one
if acc > 1000 : published = frames ; acc −= 1000 ; frames = 0
return published                                // the "FRATE %d" figure
```

The strict comparisons matter: exactly 1000 accumulated milliseconds does
not publish. A stall that raises the accumulator above 2000 resets it to
1000 without publishing on that call; the next positive elapsed interval
crosses the publication threshold.

**Profile buckets.** Nine wall-clock buckets accumulate `GetTickCount()`
deltas between stamps (`bucket[i] += now − last; last = now`); the composer
stamps bucket 3 as its last act of every frame. Under the profile-display
flag the composer draws, before the options slide and the present, nine rows
labelled `Network`, `Units`, `Logic`, `Render Static`, `Render Stuff`,
`Render Fog`, `SFX`, `Weapon`, `Misc`: row `i` writes its
label at `(screenW − 85, 40 + i × fontHeight)` in the default text colour and
a bar from `screenW − 90 − 2 × (bucket[i] × 100 / total)` to `screenW − 90`
(integer percent, two pixels per percent, growing leftward), with inclusive
vertical bounds `y .. y + fontHeight`, filled with raw palette index `i + 1`;
each row call also redraws the enclosing frame
`[screenW − 290, 38] .. [639, 9 × fontHeight + 41]` in index
255. Which pump sites stamp buckets 0–2 and 4–8 is document 01's territory
(the frame-end stamp of bucket 3 is the only one established here). These
rows also require the composer's rendering-enabled argument. They follow
the GUI painter and the restoration of the full-surface clip, so their
coordinates are not clipped to the game viewport. The options slide,
cursor restoration and optional presentation follow them.

**Packet-rate line.** Under film mode together with one session-flag bit,
the developer text block gains a line formatted
`pS %4d pR %4d S %d %4d R %d %4d …` from six network tallies (packets and
bytes sent and received — the network layer of [08 R-OOS-01]; in a
single-player session they never move). The rates are recomputed only when
more than 30 units of the scaled wall clock ([R-FX-01 §5]'s
`GetTickCount × 30 / 1000`) have elapsed since the previous recompute, as
`rate = (current − previous) × 30 / elapsed` (unsigned division; roughly
per-second), plus one percentage `(a + Δ − b) × 100 / Δ` term for the
packet-loss figure. A second helper of the same shape feeds two of the
tallies to the **Send/Receive lines**: under a separate statistics flag the
composer writes `Send %1.1f K/s` and, one text row lower, `Receive %1.1f
K/s` at `x = 129` starting `y = screenH − 95`, each with a 64-pixel bar
`[129 .. 193]` framed in colour-map entry 15 and filled to
`min(100, rate × 100 / 5600)` percent. **Supported inference:** the number
printed is fed to the formatter as an 8-byte slot holding the integer rate in
its low half and zero in its high half — read as a double that is a denormal,
so the text most likely reads `0.0`; a retail multiplayer capture would settle
it.

**Selected-unit movement overlay.** Film mode and its `i` information toggle
must both be on, and the composer must be rendering. Just before fog, after
the ordinary world objects and unit labels, it takes the first selected unit
in the local player's slice and asks that unit's first movement follower to
draw. A missing unit, controller or follower draws nothing; the search does
not continue to a later selected unit. This is separate from the `m` display
selector of §3.12. The ground follower draws:

1. Its owner's **committed footprint**, not the requested goal rectangle,
   outlined in logical colour 15. The top-left is the committed cell origin
   multiplied by 16, projected with the terrain height sampled at that origin;
   the right and bottom add `16 × FootPrintX/Z`. All four corners use that
   single anchor height rather than conforming individually to terrain.
2. Consecutive stored route points, producing `pointCount − 1` segments when
   there are at least two points. Each endpoint gets its own terrain-height
   query and ordinary world projection. The line colour is logical entry 9
   when **has-waypoint** is set, and entry 12 otherwise. The point sequence
   and flag are the follower's existing state [04 R-PATH-01 §8]; this draw
   neither requests nor recomputes a route.

The base follower and the other recovered controller families inherit an
empty draw method. All these primitives clip to the game viewport and remain
under the subsequent fog painter. The dormant probe painters of
[07 R-CAM-01 §9] are separate from this ground-follower overlay.

## 2. World coordinates, terrain grids, and projection

### 2.1 Coordinate units

The terrain uses a hierarchy of 16-bit attribute cells and 32-pixel tile
blocks:

- one attribute cell represents 16 by 16 map pixels;
- one tile contains 32 by 32 indexed pixels and covers four attribute cells;
- world coordinates use 16.16 fixed-point, so one map pixel is 65,536 world
  units;
- a cell is therefore 16 times 65,536 world units, and a tile is 32 times
  65,536 world units.

Cell and tile division uses signed, floor-like shifts with a sign correction
before shifting for negative camera/world coordinates. This prevents truncation
toward zero from moving the left/top edge by one cell.

### 2.2 TNT map consumption

The loader accepts exactly two TNT versions and rejects any other version word
with a diagnostic and resource-failure path. Version `0x1020` is legacy: it
reads gravity, minimum wind, and maximum wind from header slots 13, 10, and 11;
its minimap offset is slot 14 and its minimap-present flag is slot 15 bit 0;
and it selects the legacy attribute path. Version `0x2000` is canonical: it
hard-codes gravity 0, minimum wind 100, and maximum wind 2000; its minimap
offset is slot 10 and its flag is slot 11 bit 0; and it selects the four-byte
attribute path. An authored nonnegative OTA `wind` or `gravity` value overrides
the terrain value only for canonical maps; legacy maps retain their own header
values. When neither source supplies gravity the engine falls back to the
constant `0x1FDB`; tidal strength falls back to `0.5` ([R-TERR-01 §6] has
the omitted-key case, which is not "neither source").

The tile map is a row-major array of 16-bit tile indices with dimensions
`cellWidth/2` by `cellHeight/2`. A tile index selects a 1,024-byte block (32 by
32) in the indexed tile set. The tile blitter computes source block plus
intra-tile pixel remainder, clips the edge tiles at the viewport clip
rectangle (not at map bounds; [R-COMP-01 §1] has the exact pass), and handles partial edge rectangles. It does not use a depth buffer or a textured water mesh.

The loader expands each canonical attribute entry (four bytes: height,
feature `uint16` little-endian, and a zero unknown byte) into a 13-byte plot
cell in row-major order. Document 02 carries the typed layout and sentinel
table. In plain terms, each 13-byte cell holds:

* occupancy words at the first four bytes (two `uint16` mobile planes, zeroed at
  load then stamped per building occupancy);
* height byte at offset 4;
* derived minimum and maximum heights at offsets 5 and 6 (maximum at 5, minimum
  at 6, recomputed over the 2×2 neighbourhood; average is the coarse floor
  query);
* metal byte at offset 7, seeded uniformly from the mission `SurfaceMetal`
  scalar — every cell receives the same signed byte, no per-cell raster;
* feature word at offset 8 with sentinels: `0xFFFF` empty, `0xFFFE`
  fringe (follow the anchor offsets below), `< 0xFFFB` live feature index, and two void
  codes with two writers — `0xFFFC` from the stamp service for every
  TNT-authored void cell, `0xFFFD` from the edge/lava sweep; no reader compares
  the word with either void value (every consumer classifies `== 0xFFFF` empty,
  `< 0xFFFB` live, `== 0xFFFE` fringe, anything else blocked/void), and
  `0xFFFB` has no writer;
* anchor offsets at offsets 10 and 11: the Z delta is the width-scaled byte and
  the X delta is the unscaled byte, both counted from fringe toward anchor and
  **subtracted**, so the anchor always lies at or north-west of the fringe cell.
  Every reader in the fringe role **zero-extends** both bytes — none of them
  sign-extends — so the effective range is `0..255` and a value of 128 or more
  is a large positive displacement, not a negative one. Stamped values are
  `0..footX-1` / `0..footZ-1` (below), well inside the byte, so the distinction
  never bites on stamped data; out-of-range stays zero and leaves the fringe
  unresolved. At the anchor the same two bytes hold the live instance slot index
  while attached or the accumulated blast damage otherwise — never simultaneous;
* flag byte at offset 12, stamped at load as `flags = (flags & 0xD7) | 0x50`
  (preserve bits 0,1,2,7; clear bits 3 and 5; set bits 4 and 6), carrying bit 0
  live instance present, bit 1 building occupied, bit 2 never-seen fog, bits
  3–6 placer nibble (map load passes 10), and bit 7 preserved with no isolated
  reader (`TODO(T23)`).

Fringe-anchor offsets are offsets, not absolute coordinates. The retail corpus
contains maps up to 402×408 cells, which needs 9 bits to name an absolute
coordinate, so an 8-bit absolute field could not address the board — only
offsets fit. The Z byte is scaled by map width when forming the anchor address
(the row stride multiplies it); the X byte is not.

**Two reader shapes, and only one of them bounds-checks the anchor
(Established, whole-image census of the readers of these two bytes in the
fringe role).** Every reader first requires the feature word to be `0xFFFE`
and finally requires the anchor's own feature word to be `< 0xFFFB`, otherwise
the fringe remains not-found (blocking for yard-occupancy bit 5,
non-satisfying for geothermal bit 7). They differ in how they reach the anchor:

* the **coordinate form** — four sites, one of them the standalone
  fringe-following cell-pointer helper of §2.3 — subtracts the two bytes from
  the fringe cell's own map-cell coordinates and bounds-checks the result
  against zero and the map extents on both axes, yielding no anchor at all
  when it leaves the board (either inline or through the shared
  cell-pointer-by-coordinates helper, which returns null off-map);
* the **pointer form** — the large majority, twenty-two sites, including the
  projectile collision gate of `[06 §8.1]` — instead forms
  `zByte · mapWidth + xByte` cell strides and subtracts that byte count
  directly from the fringe cell's own address. It performs **no** coordinate
  bounds check: a byte pair that walks off the board reads whatever attribute
  cell the arithmetic lands on, and the only guard is the `< 0xFFFB` test on
  the word found there.

Both forms are safe for stamped data, whose offsets can only address a cell
inside the stamping feature's own footprint rectangle.

No hidden map sections or separate flood-fill geometry exists beyond this array — a bounded writer census found only the expansion zero and the
derived stamp. **Fringe partition is the placement order, not a heuristic.** The
feature stamper writes every `0xFFFE` cell at stamp time with its anchor→fringe
offset (`0..footX-1` in the X byte, `0..footZ-1` in the width-scaled Z byte) as it
stamps the feature's footprint rectangle, in the loader's row-major attribute
order. A TNT-authored `0xFFFE` that no footprint rectangle covers is never
stamped and ends up `0xFFFF` after load — such cells are not fringe at all, they
disappear (measured: about 5,283 such cells across the 275-map corpus). Merged
blobs resolve by last-stamp-wins: a later footprint overwrites an earlier one's
fringe cells with its own offsets; a row-major left/above later-wins heuristic
misassigns the seam cells of overlapping blobs. Declaration footprints alone resolve 65.1% of raw fringe; the
sequential stamp resolves 100% of footprint-covered fringe by construction.
`TODO(question)` remains only for the dense-pack rule — whether a footprint that
overlaps a live anchor cell is rejected or silently overwrites — which the
stamper's occupancy guard decides per consumer.

Void and edge generation runs after the full-map minimum/maximum recompute and
after feature placement. Right columns `Width-2` and `Width-1` are set to
`0xFFFD` where the feature word is empty or fringe — live features and anchors
in those columns survive. Playable
insets `PlayRight = WidthPixels
- 32` and `PlayBottom = HeightPixels - 128` are set at that time and gate the
camera clamp. When the mission `lavaworld` flag is set, a bulk sweep sets
`0xFFFD` for every cell where `hmin ≤ SeaLevel` and the feature word is
`0xFFFF` or `0xFFFE`. North and south height-dependent void strips (north
`z*16 < height>>1` on raw height, south `(Height-1-z)*16 + (height>>1) <
112`; empty-or-fringe cells only) are stated exactly in `[R-TERR-01 §2]`
below; the south predicate is evaluated on row `z` but the cell voided is
row `z-1`.
Outside the map rectangle, height returns sentinel `-1` with unsigned
candidate bounds before any terrain read; movement is blocked for generic modes
and allowed only for factory-exit search mode 2; the LOS writer stores an empty
footprint and returns; projectiles do not collide with terrain; and the camera
remains clamped to the playable insets.

Per-cell metal is uniform on canonical maps: every cell's metal byte is seeded
from the single mission `SurfaceMetal` value (non-negative, canonical version
only; negative or legacy seed 0). The TNT unknown byte is zero corpus-wide and
is not a metal source, and no `Width × Height` metal raster is allocated. The
legacy terrain version seeds each cell from its 8-byte attribute record's
per-cell metal byte — there is no varying metal file; the legacy attribute
byte is the only per-cell source (document 02 §6). An extractor
at placement sums `unsigned(metalByte) + 1` over its footprint and multiplies by
its `extractsmetal` scalar; the stored result is never resampled. Feature metal
is reclaim reward only.

Sea level is copied from the map header as a byte and is compared in world
units by multiplying by 65,536. Water/lava map state and minimum/maximum water
depth/slope thresholds are cached for placement and impact decisions.


#### The two attribute encodings, the header slot map, and what the loader writes per cell [R-TERR-01 §1]

**Established** unless marked (direct static trace of the map loader,
the feature stamp entry, the TNT reader, and the save-blob writers). The
byte offsets of the *file* are `[fmt tnt]`'s; this section states what the
engine does with them.

**Header slot map, both versions.** The loader reads the header as sixteen
little-endian 32-bit slots and branches on slot 0:

| Slot | canonical `0x2000` | legacy `0x1020` |
|---:|---|---|
| 1, 2 | cell width, cell height | same |
| 3, 4, 5 | tile-map, attribute-map, tile-graphics offsets | same (attribute records are 8 bytes) |
| 6, 7, 8 | tile count, feature-record count, feature-record offset | same |
| 9 | sea level (stored as a **byte**) | same |
| 10, 11 | minimap offset, **minimap-present flag (bit 0)** | minimum wind, maximum wind |
| 13 | — | gravity (authored units, see §6) |
| 14, 15 | — | minimap offset, minimap-present flag (bit 0) |

Slot 11 (file offset `0x2C`) is the minimap-present flag — when bit 0 is clear the engine keeps no embedded
minimap image at all and the minimap presentation falls back to the generated
picture (§3.7). Any other version word raises the diagnostic
`Unknown TNT version:  0x%08x` (two spaces, as shipped) through the fatal
resource-failure path. The canonical version hard-codes minimum wind 100,
maximum wind 2000 and **legacy-gravity 0** as the values the legacy slots would
have carried; "gravity 0" is not a runtime gravity, it is the marker that makes
the compiled default win (§6).

**The legacy attribute record** is 8 bytes per cell: byte 0 is the height,
byte 2 is a **one-byte** feature index whose live range is `0x00..0xFB`
(`< 0xFC` stamps a feature; `0xFC..0xFF` stamp nothing and leave the cell
empty), and byte 6 is the per-cell metal byte copied verbatim into the plot
cell's metal byte. Bytes 1, 3, 4, 5 and 7 are never read. The legacy path
has **no void stamp** — there is no code that writes `0xFFFC` from an 8-byte
record — and it does **not** run the mission-file feature placement pass at
all: OTA `[Features]` entries are placed only on canonical maps. (Bounded
negative within the loader; no stock map is legacy, so the decider for any
observed difference is a static re-read of the loader.)

**The canonical attribute record** is 4 bytes: height at byte 0, a
little-endian `uint16` feature reference at bytes 1–2, byte 3 never read.

**Per-cell initialization, in order.** Before either attribute pass the
loader walks every 13-byte plot cell and writes: the two occupancy words
(bytes 0–3) to zero; the feature word (bytes 8–9) to `0xFFFF`; the metal byte
(byte 7) to the seed; and **clears bits 0 and 1 of the flag byte**. The seed
is the mission's `SurfaceMetal` when it is non-negative *and* the map is
canonical, otherwise 0. It does **not** touch the derived maximum/minimum
bytes (5, 6) or the anchor-offset bytes (10, 11). The attribute pass then
writes, per cell in row-major order, the height byte and
`flags = (flags & 0xD7) | 0x50`.

**Feature stamping order (canonical).** Two separate full-map passes, both
in row-major order: the first stamps **only** the authored void cells
(attribute word `0xFFFC`), the second stamps every live index (`< 0xFFFB`)
through the shared stamp service `[R-FEAT-01 §3]` with placer nibble 10, and
then the mission-file placement pass runs. The second pass and the mission
pass are skipped entirely when the between-missions flag is set (a save is
being restored and the feature blob will be replayed instead, `[R-FEAT-01
§9]`); the void pass always runs. Consequences: an authored word of
`0xFFFB`, `0xFFFD`, `0xFFFE` or `0xFFFF` stamps nothing and the cell is
`0xFFFF` after this stage (which is why unfootprinted `0xFFFE` cells
disappear, above); and because voids are stamped first, a footprint that
overlaps an authored void is vetoed by the stamp service's teardown test —
the void wins, not the feature.

**Two void codes.** At runtime there are **two** void codes with
two writers: `0xFFFC` is written by the stamp service for every
TNT-authored void cell, and `0xFFFD` is written only by the edge/lava sweep
of §2. No reader anywhere compares the feature word with either value; every
consumer classifies through the same ladder — `== 0xFFFF` empty, `< 0xFFFB`
live, `== 0xFFFE` fringe, anything else "blocked/void" — so the two codes
are indistinguishable to gameplay and the distinction only matters for a
save/plot dump. The `0xFFFB` value has no writer at all.

**Reader census of the 13-byte cell (which field the sim reads).**

| Bytes | Readers |
|---|---|
| 0–1 occupant slot word | movement commit and the building stamp/unstamp (§8.2 of doc 04, `[R-PATH-01 §2]`); the class-layer classifier; the dead segment sampler |
| 2–3 second occupancy word | the mover mode-2 (airborne) occupancy plane (`[R-MOV-01 §8]`) |
| 4 height | the bilinear query, the four-corner conform, the void strips, the LOS height-word builder, the cell-height helper of §2.3, the min/max recompute |
| 5 derived maximum | the coarse average query; the air sector grid sweep (§5) |
| 6 derived minimum | the coarse average query; the lava flood of §2 |
| 7 metal | extractor placement sum (§2.2 above), and the save `Metal` blob, which is exactly the `Width × Height` bytes in cell order and is written back verbatim on reload |
| 8–9 feature word | every feature consumer; the void strips; the classifier ladders |
| 10–11 anchor offsets | the fringe resolver; at anchors the live slot / damage accumulator `[R-FEAT-01 §3]` |
| 12 flags | bit 0 instance present; bit 1 building-occupied, set by the building stamp for every yard cell whose yardmap byte has bit 0 and cleared by the unstamp; bit 2 never-seen; bits 3–6 placer nibble — the save `PlayerFeatures` blob packs two cells' nibbles per byte as `(odd.flags >> 3 & 0xF) \| ((even.flags & 0xF8) << 1)` |

**The last column and last row's derived bytes are heap garbage in
retail.** The min/max recompute (§3) never writes column
`Width-1` or row `Height-1`, and the loader's own initialisation loop writes
only bytes 0–3 (zero), byte 7 (the per-map value), bytes 8–9 (`0xFFFF`) and
clears bits 0–1 of byte 12; bytes 4–6, 10–11 and bits 2–7 of byte 12 keep
whatever the allocator left, and the allocator **does not zero-fill** (the
labelled wrapper discards its label and calls the C runtime allocator with no
fill; [01 R-PLAT-01 §5]). So the sector-grid sweep and the lava flood read
undefined bytes for that column and row in retail. Nanolathe's zero
initialisation is a documented divergence, not a contract.

#### The void strips, exactly, and the row-above rule [R-TERR-01 §2]

**Established** (direct static trace; the south-edge rule re-read at
the instruction level because the decompiler's row arithmetic is easy to
misread).

The sweep runs once, after the full min/max recompute and the sector-grid
build, and only ever converts cells whose feature word is `0xFFFF` or
`0xFFFE`. It writes `0xFFFD`. Four rules in this order:

1. **Play insets.** `PlayRight = Width·16 − 32`, `PlayBottom = Height·16 −
   128` (map pixels), written here and read by the camera clamp and the
   minimap lens.
2. **Right columns.** For every row `z`, cells `(Width−2, z)` and `(Width−1,
   z)`.
3. **North strip.** For every column `x`, rows `z = 0, 1, 2, …` in order,
   stopping at the first row for which `z·16 − (height(x, z) >> 1) ≥ 0`;
   every row before it is voided. The tested and voided cell are the same.
   Since `height >> 1 ≤ 127`, row 7 is the deepest possible (`112 < 127`);
   row 8 never.
4. **South strip — one row above the tested row.** For every column `x`,
   rows `z = Height−1, Height−2, …` in order, stopping at the first row for
   which `z·16 − (height(x, z) >> 1) ≤ PlayBottom`; for every row before it
   the cell voided is **`(x, z−1)`**, the row *above* the one whose height
   was tested. Equivalently the row `z` is tested with
   `(Height−1−z)·16 + (height(x,z) >> 1) < 112` and, when true, row `z−1`
   is voided. So the bottom row `Height−1` is **never** voided by this rule
   (only rule 2 can void it, in its two columns); row `Height−2` is voided
   when the bottom row's height is below 224, row `Height−3` when row
   `Height−2`'s height is below 192, and so on down to row `Height−8`, voided
   when row `Height−7`'s height is below 32.
5. **Lava flood.** When the mission's `lavaworld` is non-zero, every cell
   whose derived **minimum** byte is `≤ SeaLevel` (unsigned byte compare).

**The target row.** The loader steps the cell pointer back one row (a
subtraction of one row stride) *before* it reads and writes the feature
word, so the voided cell is the row above the tested one and the bottom row
itself is untouched. An implementation that voids the tested row voids
one extra row at the south edge on every map and voids the bottom row,
which retail never does.

**Who treats void specially — nobody, by name.** Established from the
reader census of §1: no consumer tests `0xFFFD` or `0xFFFC`. Void cells are
"not empty, not live, not fringe" to every ladder, which yields: the fringe
resolver returns not-found; the yard/placement validators and the
class-layer classifier classify the cell as blocked (doc 04 §6.1 "void cells
block"); the stamp service's teardown vetoes any footprint over it; the
edge/lava sweep itself skips it (it only converts empty/fringe). There is no
void raster for rendering — the tile art under a void cell is drawn
normally; the "hole" look of retail map edges is authored tile art.

#### Terrain deformation does not exist [R-TERR-01 §3]

**Established — bounded negative** over the complete decompiled
function set.

The plot height byte has exactly one writer: the loader's attribute pass.
The tile-index map has one writer (the loader's copy) and two readers (the
tile blitter and the minimap generator). No weapon impact, feature death,
construction or COB path writes either. There is no deformation to sequence
against movers, and craters and scorch marks are not height edits.

What does exist, and is easy to mistake for deformation, is the **derived
min/max recompute**, which has three live callers: the loader (whole map,
once), and the **building occupancy stamp and unstamp**, each of which
recomputes the rectangle `(footprintX + 2) × (footprintZ + 2)` anchored one
cell up-left of the footprint — the footprint plus its one-cell ring — and
then raises the occupancy-listener notify over the footprint. Because no
height changed, the recompute is a no-op on retail data; it is documented
here so that an implementer does not infer a hidden height edit from the
call, and so that the recompute's own edge rule is stated once: it clamps
the rectangle to `x < Width−1`, `z < Height−1` **exclusive**, evaluating each
cell as the maximum/minimum over itself, its east neighbour (when `x <
Width−1`), its south neighbour (when `z < Height−1`) and its south-east
neighbour (when both), writing maximum to byte 5 and minimum to byte 6. The
last column and last row are never evaluated (the heap-garbage note of
[R-TERR-01 §1]).

### 2.3 Height queries

The world-owned height query samples four neighboring plot-cell heights and
performs bilinear interpolation using the low four bits of each cell-space
coordinate. A separate coarse average `(hmax + hmin) >> 1` over the two derived
bytes is used by some placement/airborne tests; it must not be substituted for
the bilinear query everywhere. Height at offset 4 is the raw corner sample;
`hmax` at offset 5 and `hmin` at offset 6 are the derived 2×2 neighbourhood
maximum and minimum that feed the coarse query and the LOS aggregation.

**Established — the exact query.** The input is a position record holding
16.16 X, Y and Z; the query reads only the **high sixteen bits** of X and of Z,
each taken as a *signed* 16-bit map-pixel coordinate. From those:

```
cx = X >> 4        fx = X & 0xF          (a cell is 16 map pixels, §2.1)
cz = Z >> 4        fz = Z & 0xF
if !(cx >= 0 && cx+1 < Width && cz >= 0 && cz+1 < Height) return -1
cell(i,j) = plotBase + (Width*j + i) * 13     ; height byte at cell offset 4
h00 = height(cx,   cz  )   h10 = height(cx+1, cz  )
h01 = height(cx,   cz+1)   h11 = height(cx+1, cz+1)
top    = h00 + trunc16((h10 - h00) * fx)
bottom = h01 + trunc16((h11 - h01) * fx)
return   top + trunc16((bottom - top) * fz)
where trunc16(v) = (v + ((v >> 31) & 0xF)) >> 4
```

The interpolation order is **X first, on both Z rows, then Z between the two
results** — not a single fused expression, and not Z first. Each of the three
interpolations rounds independently through `trunc16`, so the result is not the
same as one exact bilinear evaluation rounded once. `trunc16` divides by 16
truncating **toward zero**: the `>> 31` term adds 15 before the arithmetic
shift when and only when the value is negative, which cancels the shift's
floor. The height bytes are unsigned `0..255`; their differences are signed, so
a downhill corner pair is exactly the case the bias exists for. A valid result
is in `0..255`, which is what makes `-1` usable as an off-map sentinel. The row
stride multiplies the **second** coordinate, which is the decisive evidence
that the record's first component is X and its third is Z `[04 §4.4]`.

**Established — one arithmetic, two call shapes.** The movement layer's
four-corner terrain conform `[04 §8.1]` does not call this query; it inlines
the same cell indexing, the same `trunc16`, and the same X-then-Z order, and
differs only in its bounds handling: it tests `(unsigned)cx >= Width-1` and
`(unsigned)cz >= Height-1` — one unsigned compare per axis, which rejects
negatives as very large values — and on failure **abandons the whole conform**
for that unit rather than returning a sentinel. An implementation may share one
sampler between the two, but must keep the two failure behaviors distinct.

The LOS writer uses a different, coarser height representation. It quantizes
to 32-pixel visibility tiles and reads a `uint16` word per visibility tile (see
section 3.2), aggregated from the terrain heights at map load — not the
four-corner bilinear query, and not rebuilt during a battle. The table is
seeded low = `0x00` / high = `0xFF` and updated with `low = max`, `high = min`,
so the **low byte is the neighbourhood MAXIMUM and the high byte its MINIMUM**.
Cells are scattered into it through the same height shear the observer's
coverage tile uses, carrying a perspective-scaled value, and a tail pass blends
the pair by thirds and floors both at sea level. See §3.5
`[R-P0-18-B §1–§4]` for the derivation; a (minimum, maximum) pairing is the
maximally occlusive one and produces false shadows on ground retail leaves
visible. A tall feature does not raise
the LOS ray height — nothing but the map-load build does, since the word is
never invalidated.


#### The height queries: every caller family and the sentinel each one gets [R-TERR-01 §4]

**Established** (direct static trace of both query bodies and of
every call site the call graph reaches; the two dead helpers are named so
the census is complete).

**Two queries, two roundings, two bounds.** Both take a position record
(16.16 X at the first word, Y second, Z third) and read only the high 16
bits of X and Z as *signed* map-pixel coordinates.

| | bilinear (§2.3 above) | coarse average |
|---|---|---|
| cell index | `px >> 4` — arithmetic shift, i.e. **floor**: pixels `−16..−1` give cell `−1` | `(px + ((px >> 31) & 0xF)) >> 4` — **toward zero**: pixels `−15..−1` give cell `0` |
| bounds | `cx ≥ 0 && cx+1 < Width && cz ≥ 0 && cz+1 < Height` | `cx ≥ 0 && cx < Width && cz ≥ 0 && cz < Height` |
| value | three `trunc16` lerps over the raw height byte of the 2×2 | `(hmin + hmax) >> 1` of the one cell's derived bytes (unsigned add, so no overflow) |
| off-map | `−1` | `−1` |

Two consequences worth stating: a position up to 15 pixels *west or north
of the map* is on-map to the coarse query and returns cell (0, ·)'s
average, while the same position is off-map (`−1`) to the bilinear query;
and a position in the last column or row (`cx == Width−1`) is off-map to the
bilinear query but on-map to the coarse one — where it reads the
uninitialized derived bytes of [R-TERR-01 §1].

**Cell-space helpers.** Alongside the two queries sit four small helpers that
callers use instead of the queries: (a) *cell height by cell coordinates* —
takes `(cellX, cellZ)` as 16-bit values, returns the raw height byte, and
returns **0** (not −1) off-map; used by the yard validator and the route-line
draw; (b) *cell pointer by cell coordinates* and (c) *cell pointer by 16.16
position* (`X >> 20`), both returning null off-map; and (d) the fringe-
following variant of (c) used once by resurrection. Two further helpers — a
segment sampler that walks a line returning the maximum of `feature height +
cell height` and occupant `unit Y + model height`, and a cell-corner
position builder — have **no callers** and are dead.

**Caller families and what each does with the sentinel.** The sentinel is
never tested by name. Every consumer either shifts it, floors it, or
compares it, and the table below is what each family *does*, so the −1
outcome is derivable:

| Family | Use | Sentinel outcome |
|---|---|---|
| COB `GROUND_HEIGHT`-style port `[R-COB-03 §2]` | returns `h << 16` | `−65536` (−1.0 in 16.16) |
| post-move Y correction `[R-MOV-01 §5]` (ground, non-floater) | `Y = h << 16` unconditionally; the hover/floater branches use `max(seaLevel − authored offset, h)` | a mover that reaches an off-map cell is placed at `Y = −1.0`; the floor branches clamp it to the sea-level expression instead |
| four-corner conform `[R-MOV-01 §5]` | inline copy of the bilinear arithmetic with the unsigned `≥ Width−1` guard | whole conform abandoned; no sentinel exists |
| feature stamp centre, burn origin, feature phase, static reference point `[R-FEAT-01 §3]`, `[06 §9.3]` | `Y = h << 16` at the footprint centre `((fx + 2x)·8) << 16` | `−1.0`; the stamp's own bounds test makes this unreachable for a stamped cell |
| corpse creator `[R-FEAT-01 §13]` | `seaLevel < h` → land path else water path | off-map counts as water |
| aircraft marker altitude, `VTOL_LandIfCan`, weapon target-point resolution, cruise-missile waypoint `[R-AIR-01 §4]`, `[R-WPN-03 §3]` | `Y = max(seaLevel, h)`, then the family's own offset/cap | floors to sea level |
| reclaim and resurrect approach point `[R-WORK-01 §5]`, `[R-WORK-01 §7]` | `Y = (h + draw(featureHeight)) << 16` — one simulation-RNG draw bounded by the feature's authored `height` | `h` is −1 before the draw is added |
| reclaim/resurrect nano effect box (same anchors) | box from `Y0 = h << 16` to `Y1 = Y0 + featureHeight << 16` | as above |
| ground resolver for the minimap lens and `MoveUnitToRadius` centre (§3.11, doc 08) | clamps the input to `0..Width·16−1`, `0..Height·16−1` first, then iterates `max(seaLevel, h)` down the screen column (below) | never off-map after the clamp |
| presentation: unit draw origin `[R-REN-03A]`, weapon-range arc vertices, route polyline | `screenY = z − (h >> 1) + 32 − cameraZ` (the range arc floors `h` at the record's own Y first) | `−1 >> 1 = −1`: one pixel lower, no other effect |
| smoke/steam strip particle test `[R-STRIP-01 §2]` | `h < seaLevel` terminates the particle once it has passed its age threshold | off-map particle terminates |
| coarse: fragment pool, debris bounce `[R-COB-04 §2]`, feature 3D physics `[R-FEAT-01 §10]` | `Y ≤ h·65536` style floor tests | −1 floor: nothing ever lands off-map |
| coarse: commander respawn on lava maps `[R-SKIR-01 §3]` | reject the candidate when `h ≤ seaLevel` | off-map candidate rejected |

**The ground resolver, exactly** (the screen-column-to-world inversion the
lens and the radius trigger share; owned here because it is the only caller
that solves *for* the height). Input `(X, targetZ)` in map pixels, already
clamped as above. Start at `z = (targetZ & ~0xF) + 128` and probe at most
nine rows northward in steps of 16 pixels (`z` decreasing): at each row take
`hz = max(seaLevel, bilinear(X, z))` and `screen = z − (hz >> 1)`; stop at the
first row with `screen ≤ targetZ`. If nine rows are exhausted the last row's
position is returned as is. Otherwise probe the next row south, `z + 16`, to
get `screen'`. The interpolation gate is a **plain disjunction** of two arms,
in this order:

```
interpolate iff  screen < screen'            (and screen <= targetZ, which the
                                              stopping condition guarantees)
             or  targetZ <= screen'
```

— that is, interpolate when the next row south projects strictly further
south than the stopping row, **or** when the clicked row is at or north of
that next row's projection. The first arm carries a redundant test that
`targetZ` is at or south of `screen`, which the loop's own exit condition has
already established; there is no upper bound `targetZ ≤ screen'` on that arm.
Only the second arm is reachable with a non-positive denominator, and retail
interpolates there anyway: `screen' == screen` faults the divide, and
`screen' < screen` steps the row *north*. That case needs the column one cell
south to stand at least 32 world units higher, and it is reachable on steep
terrain; it is also only reachable when the loop stopped on its **first**
probe, which needs a column height of roughly 226 or more.

Interpolation is `z += ((targetZ − screen) << 20) / (screen' − screen)` (a
16.16 result, signed truncating division), then the height is re-sampled at
the interpolated point, again floored at sea level; the returned Y is that
height `<< 16`.

#### The air sector grid [R-TERR-01 §5]

**Established.** `[04 R-AIR-01 §5]` states the grid, the sentinel and its
eight consumers at implementable precision; the build is repeated here where
doc 03 owns the input.

* Built by the same map-load routine that then builds the LOS height-word
  table of §3.5 `[R-P0-18-B]`; it runs after the full min/max recompute and
  before the void sweep, so the void codes are invisible to it (it reads no
  feature word).
* Cell side 128 world units (8 attribute cells). Columns and rows are
  `((extent · 65536) + 0x7FFFFF) >> 23` where `extent` is the pixel width or
  height — a round-up to whole 128-pixel cells. Record count is that product
  rounded up to a multiple of 8. The padding records take the sea-level byte
  like every other record — the sea-level pass walks the whole allocation —
  but **no edge bits**: the four edge passes are bounded by the column and row
  counts, so a padding record's edge word keeps the zero initialisation left
  there. They are never swept and never indexed.
* Edge bits `1` top row, `2` bottom row, `4` left column, `8` right column,
  OR'd in that order; the sentinel record carries `0x1F`.
* **The swept byte.** The sweep reads the cell's
  **derived maximum byte** (byte 5 of the plot cell — the 2×2 maximum), not
  the raw height at byte 4. The difference is one cell of reach: a peak in the
  first column or row of the *next* sector already raises this sector's
  byte. The value is therefore `max(seaLevel, max over the sector's cells of
  hmax)`, and the smoothed second byte is the 3×3 sector maximum of that.
* Consumers are `[R-AIR-01 §1]` (cruise altitude reads the smoothed byte) and
  the sentinel readers of `[R-AIR-01 §5]`; no terrain-side reader exists.

#### The map-global block: sources, conversions, defaults [R-TERR-01 §6]

**Established** (loader trace at the FPU-instruction level for the
gravity conversion; the OTA parser for key names; consumers cited).

| Runtime value | Source and rule | Default when absent |
|---|---|---|
| minimum wind, maximum wind (integers) | OTA `minwindspeed` / `maxwindspeed` when the authored value is `≥ 0` **and** the map is canonical; otherwise the legacy header slots 10/11; on a canonical map the "header" values are the hard-coded `100` / `2000` | `100`, `2000` on a canonical map with a **negative** authored value, or when no `[GlobalHeader]` was parsed at all (the loader prologue seeds −1). An **omitted** key is not "unparsed": the OTA parser stores its integer default `0`, which passes the `≥ 0` test, so the wind range is `0..0` (`[02 R-MAP-01]`; `[R-PROD-01 §3]` is the consumer) |
| gravity (runtime word) | OTA `gravity` when `≥ 0` and canonical: `ftol((double)g × 65536.0 × (1/900))` — the integer is converted to double, multiplied by 65536.0, then by the double constant `0.001111…` (exactly the nearest double to 1/900), then truncated toward zero. Otherwise, when the legacy header slot 13 is non-zero, the same conversion of that slot | `0x1FDB` = 8155, which is exactly what authored `112` converts to (`112·65536/900 = 8155.59`). The default is reached for a **negative** authored value, for legacy terrain whose slot 13 is zero, or for a session with no parsed `[GlobalHeader]`. A canonical map whose OTA merely **omits** `gravity` gets the parser's integer default `0`, which passes the `≥ 0` test — runtime gravity is then **0**, not `0x1FDB`, and `AirStrike` cancels ([04 R-AIR-01 §8], `docs/SPEC_CONFLICTS.md` SC23) |
| tidal strength (single float) | mission `tidalstrength` unless it is `< 0.0` (strict) | `0.5` (`[R-PROD-01 §4]`; no version test); an omitted key is the parser's `0.0`, not `0.5` |
| sea level (byte) | terrain header slot 9, low byte | none — always present. There is no OTA key: the string `SeaLevel` exists in the image with no reader, and `nosealeveltrigger` is a mission flag unrelated to the value |
| surface metal seed (signed byte) | mission `SurfaceMetal` when `≥ 0` and canonical | `0` |
| lava world (flag) | mission `lavaworld` | `0` |
| play insets | §2 rule 1 | — |

The wind pair, gravity and tidal value are written once at map load, in that
order, before any plot memory is allocated; nothing rewrites them during a
battle. Units: the gravity word is 16.16 world units per tick², which is why
the divisor is `30²`; `[fmt ota]` states the same identity from the asset
side and the 192-of-275 census of `gravity=112`. All 275 retail OTAs author
all four keys, so the fallbacks are reachable only through authored negatives.

### 2.4 3DO model hierarchy

A 3DO object piece has a 52-byte header with vertex/primitive counts, selection
primitive, signed 16.16 parent translation, name, vertex/primitive arrays, and
sibling/child links. Sibling and child links form a depth-first hierarchy.
Vertices are three 16.16 coordinates; primitives contain color, vertex-index,
texture-name, and colored/texture flags.

Model loading visits compiled unit definitions in their definition order and
loads one model object for every definition with an object name. The resulting
pointer table is indexed by definition identity: two definitions naming the
same file still have distinct loaded pieces and texture cursors, while every
runtime instance of one definition shares its selected loaded object. The
definition traversal makes piece/type identity independent of provider
enumeration order. **Established
(direct-static):** colored primitives retain their authored polygon ring for the
generic edge-table filler; textured primitives draw only at arity four. There
is no fan-triangulation step [§2.4.1] [R-RAST-01 §1]. Leaf pieces with a vertex
but no primitive are valid attachment/emit points.

**Established (direct static trace).** After relocation and before any draw,
each object reorders its primitives once at load time. An authored selection
index other than the all-ones sentinel exchanges that primitive with primitive
zero and is rewritten to zero. Primitive zero is excluded from ordering even
when no selection primitive exists. Indexes one through the end are stably
ordered by ascending signed mean of their vertices' second coordinate: each
coordinate is accumulated in a wrapping signed 32-bit sum, the signed division
truncates toward zero, and adjacent records exchange only for strict less-than.
Ties therefore retain their post-selection-swap order. `[02 "Model archive
(3DO)"]` owns the arithmetic and malformed-input boundary. A separate
recursive pass then negates the first and third vertex coordinates and the first
and third parent translations of every object in the hierarchy, a half-turn about
the vertical axis applied to the whole model. The trailing sign on Z seen in projection helpers is the `Z - Y/2` orthographic shear — the high word of Z is transiently negated in place, then half of Y is subtracted, with the `+32` viewport bias, and the result is never stored back (the hover-pick projection in [07 R-REV-01 §3] writes that same negation explicitly as `(unitZ - cameraZ) - z`) — not a second model-space sign fixup; the load-time half-turn (negating X and Z of each vertex and each parent translation) is the sole persistent conversion (net `-X,-Z` established by store-path data-flow; a net `-X`-only conversion and a second store are both rejected, the latter bounded-negative). Child `flare` and piece translations queried at muzzle reuse the pristine post-load vectors without a second negation: a flare authored at `(2,1,-30)` is stored as `(-2,1,+30)` in model space, and the piece locator of [R-RAST-01 §8] hands a simulation consumer the world offset `(-2, 1, -30)` — model-space `(x, y, z)` maps to world `(x, y, −z)` on every consumer path. Which authored axis is the nose at heading zero remains
supported inference (an asset-side question; decider: a probe of child
translation signs at headings 0/90/180/270). Draw order within a piece is
therefore fixed at load time, not recomputed per frame, and a per-frame sort
does not reproduce retail tie order.

**Piece transform composition.** There is no matrix stack and no per-frame
matrix build anywhere in the model path: the renderer applies ordered in-place
rotation+translation passes over vertex arrays, ancestors applied after
descendants, so a leaf vertex receives exactly the chain product

```
world(v) = M_root * ... * M_leaf * v        with        M_i = T(t_i) * R_i
```

— each piece rotates about its own origin FIRST, then translates. The
per-node translation is the componentwise sum of the piece’s script
translation lanes (32-bit 16.16 values) and the model’s authored parent
translation. Rotation composes three unsigned 16-bit accumulators (65,536
units per circle) applied chronologically about Z, then X, then Y:

```
Rz: x' = c*x - s*y ; y' = s*x + c*y      (pair x,y)
Rx: y' = c*y - s*z ; z' = s*y + c*z      (pair y,z)
Ry: x' = c*x - s*z ; z' = s*x + c*z      (pair x,z)
theta = angle * 2*pi / 65536 ; results rounded to nearest integer
```

All three axes share this one rotation template; reproduce the formulas
verbatim rather than adopting a named clockwise/counterclockwise convention.
Rendering evaluates this trigonometry in floating point with round-to-nearest
integer conversion — **not** through the fixed-point trig tables, which serve
simulation velocity integration only. TURN, turn-now, and SPIN converge on
the same accumulators through one script adapter, so there is a single angle
per axis and the last writer wins; no separate aim-versus-spin stage exists.

Residuals: frames sample the accumulators exactly as committed at the current
tick — no interpolation between updates exists in the draw path — and a
vestigial per-piece rebuild-gate counter is never observed holding a nonzero
value, so every dirty frame rebuilds each reachable piece from pristine
coordinates through its full ancestor chain.

#### The composed piece offset a simulation consumer adds is `(x, y, −z)` of the model-space composition [R-RAST-01 §8]

The sense of Z that a *simulation* consumer of a queried piece position
owes: there is one piece locator and every simulation-side consumer goes
through it, so the build plate, the nano source ([§5.5 "The nano source
point's coordinate space"]) and the weapon muzzle ([06 §4.1]) share one
answer.

**Established — the locator.** Given a unit and a piece index it returns a
three-word offset, built as follows (all 16.16, integer arithmetic except the
rotation helper's trig):

1. Start with the piece's own translation: the post-load parent translation
   of §2.4 (already half-turned: `−X, −Z` of the authored file) plus the
   piece's three script translation lanes. The piece's own rotation words
   are **not** applied — a piece rotates about its own origin, so they cannot
   move it.
2. Walk the parent chain to the root. For each ancestor: rotate the running
   offset by that ancestor's three rotation words in the order Z, then X,
   then Y through the shared coordinate-pair rotation helper — the same
   order §2.4 gives the draw path — and then add the ancestor's post-load
   parent translation plus its script lanes. When the ancestor is the root
   (no parent of its own), the unit's bank is added to its Z word, the
   unit's heading to its Y word and the unit's pitch to its X word before
   the rotation, by plain sixteen-bit addition with no negation — the C24
   fold, confirmed at the simulation locator and not only in the draw path.
3. Return `(x, y, −z)`: the Z component is negated **on output**, in a
   register, as the last step. Nothing is stored back to the model.

**Established — the consumers.** Every simulation caller that turns the
triple into a world point adds it to the unit's own position with no further
sign change: `world = unitPosition + (x, y, −z)`. Those callers are the
weapon muzzle query for all three weapon slots (the
`QueryPrimary`/`QuerySecondary`/`QueryTertiary` path of [06 §4.1] and the
burst re-query of [06 §4.3]), the `QueryNanoPiece` spray source of §5.5, and
the two piece-position COB ports of [04 R-COB-03 §2] (packed X/Z whole
parts, and world Y). The only other caller, the transport pickup executor of
[04 R-AIR-01 §9], reads the locator for its `QueryTransport` piece and uses
the Y word alone (negated, as the hang altitude of the follow marker), so
the Z sense never reaches it. No caller re-negates, and no caller adds the
un-negated triple.

**Consequence — the two paths agree, and the sign convention.** The draw
path narrows a model-relative vertex as `hi16(−vz)` while a unit's own
position enters unnegated ([R-RAST-01 §2]); the locator negates the
composed Z once on output. Both therefore map model-space `(x, y, z)` to
world `(x, y, −z)`, and a muzzle, a spray source or a script-queried piece
sits where the model pass draws it. In authored (file) coordinates, with the
half-turn of §2.4 folded in, a piece authored at `(ax, ay, az)` on a unit at
heading 0 — which faces world `−Z` ([04 R-MOV-01 §4], [06 R-WPN-05 §11]) —
lands at world `unit + (−ax, ay, +az)`: the authored X is mirrored, the
authored Z is kept, and the heading rotation is the plain Y-word rotation
of §2.4 with the unit heading added unnegated. Confidence: **Established**
for the sense (direct read of the locator's output and of every caller's
addition); the rotation helper's own formulas are §2.4's and are not
re-derived here.

**Implementation rule.** Compose in model space exactly as the draw path
does (post-half-turn translations plus script lanes, rotations Z, X, Y with
the unit's bank/heading/pitch added to the root's words), then negate Z
once, then add the unit position. Do not negate per consumer; do not negate
inside the composition; do not skip the negation for any of the three
consumer families that form a world point.

### 2.4.1 Model rasterization — face dispatch and shading

The per-unit rasterizer projects every piece vertex with the section 2.5
projection, then walks pieces and primitives with these established rules:

1. **Selection plate exclusion.** When a piece declares a selection
   primitive (swapped to index 0 at load), the primitive loop starts at
   index 1 — the plate is never drawn as a model face. Unit picking is a 2D
   bounding-box test elsewhere and does not consult the mesh.
2. **Flat-colored faces render at any vertex count; textured faces are quads
   only.** The dispatcher reads the primitive's colored flag first: when it is set the
   face goes to the **generic edge-table polygon filler**, which takes an
   explicit vertex count and draws lines, triangles, quads and n-gons alike;
   when it is clear the dispatcher **requires a vertex count of exactly four**
   before it binds any texture or calls the quad mapper, and a textured
   triangle or n-gon therefore draws nothing. The quad mapper is hard-wired to
   four corners — it has no vertex-count parameter at all. See
   [R-REN-03A §5] for the dispatch in full and for the stock-asset census:
   across all 608 base 3DO models and 50,443 primitives, every one of the
   43,845 textured primitives is a quad, while the 6,598 flat primitives occur
   at vertex counts 2, 3, 4, 5, 6, 7, 8, 10, 12, 13 and 16 (3,312 of them are
   non-quads). The team-colour bits are consulted only on the textured branch,
   and the byte they select with is the owner's colour index choosing a
   `LOGOS` frame, not a shade — see [R-RAST-01 §3].
3. **`SHD` applies to flat fills too, in the shaded renderer.** There are two
   flat span writers, one per renderer. The **unshaded** renderer's flat
   writer stores the color byte raw. The **shaded** renderer's flat writer
   stores `SHD[row*256 + color]` with the same Gouraud-interpolated row the
   textured path uses. So a flat face on a `BMcode=1` unit, or on any unit
   with `Shading` off, does not vary with orientation (a two-normal 3DO probe
   on the unshaded renderer measured exactly that), but a flat face on a
   `BMcode=0` structure with `Shading` on does. Everything in this item
   describes the **shaded** piece renderer,
   which retail reaches only for a `BMcode=0` unit with the `Shading` display
   option on ([R-RND-02A]); the unshaded renderer that every other unit takes
   maps the same textured faces with no `PALETTE.SHD` step at all. In the
   shaded renderer the texture is sampled through `PALETTE.SHD` at a row
   derived from geometry:

```
row = trunc( dot(N, L) * 5.0 ) mod 32
L default = (-0.8, 1.0, 0.25)      (user-settable light direction)
N = per-vertex smooth normal:
    face normal = normalize(cross(v[b]-v[a], v[b]-v[c]))
      over the polygon's first three vertex indexes (0-based a,b,c);
    degenerate faces (any two equal indexes) use (0, 1, 0);
    vertex normal = average of the normals of all faces touching the vertex
```

   The row interpolates across the face with the corners. Rows are palette
   remaps, not brightness ramps (row 15 identity; row 0 near-black; row 31
   saturated; intermediate rows shift hue per entry). `dont-shade` (the
   render-piece record's shade bit cleared by the COB adapter) forces row
   `0x0F` (15); else `row = trunc(dot*5.0) & 0x1F`
   wrapping negatives to `27..31` (not clamped); the gouraud interpolant is
   `rowStep = (rowR-rowL)/width` in signed 16.16 fixed point, and each pixel samples
   `SHD[row*256+texel]` on the textured path and `SHD[row*256+color]` on the
   flat path (direct-static). **Established — light setter and image invalidation.** The global model
   light direction takes three signed integers. Each is multiplied in working
   precision by the binary32 value of 0.01 (exactly 5368709 / 2^29), then
   stored once as binary32, without normalization or clamping. The command
   then invalidates the shared model image arena, which contains body and
   auxiliary image allocations. Owner references are cleared; subsequent
   draws follow their ordinary missing-image rules. Invalidation does not
   reset retained orientation or piece transforms, advance texture cursors,
   reload assets, or synchronously render replacement images. `RCache`
   performs this same invalidation without changing the light vector
   [07 R-CAM-01 §6].

4. **Texture resolution at load**: each primitive's texture name resolves
   case-insensitively against the side's texture GAF set, then a fallback
   set. A miss rewrites the primitive to flat color `0xd1` (a gray placeholder
   quad, subject to the quads-only rule). One-frame entries are static;
   multi-frame entries become animated textures driven by loaded-primitive
   players shared by instances of that loaded model and ticked once per simulation frame with per-frame delays from the
   GAF table. Only a ten-frame entry resolved from the fallback LOGOS bank
   after the primary banks miss becomes a team texture: it is never animated
   and its frame is selected by owner player at draw time. A ten-frame entry
   found in a primary bank remains an ordinary registered animation.

#### R-REN-03A — the per-unit composition image, the height key, and structure anti-aliasing

Retail does not rasterize a unit's pieces straight into the world
framebuffer. Every unit is composed, whole, into a private indexed image and
that image is blitted once. This section is the complete contract for that
image: how it is sized, what its two planes mean, which pieces go into it and
in what order, how a structure's image is anti-aliased through a 2×
supersample, and how the result is placed on screen. Everything below is
**Established (direct-static)** unless a paragraph says otherwise. The piece
transform chain of [03 §2.4] is unchanged by it.

##### 1. The composition image

Before any piece is drawn the engine measures the model. It walks every
**visible** piece (draw bit set) and every vertex of those pieces, projects
each vertex with the ordinary orthographic rule of [03 §2.5] **minus the
viewport bias** — `sx = hi16(x)`, `sy = hi16(-z) - (hi16(y) >> 1)`, where
`hi16` is the high word of the 16.16 value, a floor ([R-RAST-01 §2] has the
exact pipeline), each component narrowed to signed 16-bit before use — and
tracks the minimum and
maximum of `sx` and `sy`. The running extrema are **seeded at zero, not at
the first vertex**, so the box always contains the model origin even for a
model that lies entirely off to one side. The image is then

```
width   = (maxX - minX) + 4          origin.x = 2 - minX
height  = (maxY - minY) + 4          origin.y = 2 - minY
```

— a two-pixel margin on every side, with the origin recording where the
model's own `(0,0)` sits inside the image. Both margin terms are literal: the
minimum is biased by `-2` and the span is then widened by a further `+2`.

The image is a 24-byte header followed by its planes. The header carries
width, height, the two origin components, a **transparent index**, a
raw/RLE flag, a sub-image count, and one pointer per plane. For a unit
composition image the transparent index is always **1** and the RLE flag is
always clear.

Two allocators exist and the draw path picks between them:

- **One plane.** Colour only; the key-plane pointer is null. The colour plane
  is prefilled with the transparent index (byte `0x01`).
- **Two planes.** Colour plane prefilled with `0x01` as above, plus a
  **height-key plane** of the same dimensions prefilled with `0`.

##### 2. The height key and the `ZBuffer` gate

The key plane is a per-pixel depth buffer whose unit is world height. While
the model rasterizes, every projected vertex carries a third component beside
its two screen coordinates:

```
key = hi16(vertexY) + 50 + (definition authors Digger ? 75 : 0)
```

where `vertexY` is the vertex's height in whole world units **relative to the
unit origin** — the piece chain of [03 §2.4] has already been applied, and the
unit's world position has not (position enters only at the final blit). The
span writers interpolate the key across each scanline in signed 16.16, narrow
it to a byte, and admit a pixel only when

```
storedKey <= incomingKey
```

writing both the colour and the new key when they do. The comparison is
non-strict, so **the highest face at each pixel wins and equal keys go to the
later-drawn face**. When the key-plane pointer is null every span writer falls
through to an unconditional write and the image is pure painter order.

**The two vertex paths compute the same key.** The renderer has two vertex
paths — plain, and the supersampled path of §6, which has already doubled the
vertex two instructions before the key is formed — and they compute

```
plain:        key = 50 [+75] + hi16(vertexY)
supersampled: key = 50 [+75] + (2 * hi16(vertexY)) / 2
```

which are the same number. The `/2` is there to undo the doubling, not to
halve the height. **The key is the whole world height, not half of it**, and
implementing a halved form throws away half the depth resolution and
roughly doubles how often two faces tie. The definition bit is
the FBI key **`Digger`**, and its contribution is `+75`, which makes
`125` (`50 + 75`); see §8 for what the raised base is for.

**The `ZBuffer` gate.** Whether a unit's image gets a key plane at all is
**authored data**. At unit creation the instance copies one bit out of its
definition's packed flag word, and the FBI key that writes that bit is
`ZBuffer`. The draw path then allocates the two-plane image when *any* of
these holds, and the one-plane image otherwise:

- the caller asked for a key plane explicitly (the attached-child path of §4
  always does), or
- the unit's definition authors `ZBuffer` nonzero, or
- the unit's construction fraction is not the completed sentinel (a nanoframe
  always gets a key plane, because [R-P0-19-N]'s reveal reads it).

The engine's feature-backed pseudo-unit sets the same instance bit
unconditionally, having no FBI to read.

**Established (asset census, base `totala1.hpi`).** All 278 stock
unit definitions author `ZBuffer`. Exactly two author `0` — `CORFAV` and
`CORTRUCK` — and the other 276 author `1`. The per-pixel height buffer is
therefore the normal case for stock content, not an exotic one, and an
implementation that omits it reproduces retail for two units out of 278.

##### 3. Piece order and the tie rule

The renderer walks the piece list **from the last piece to the first**. Within
a piece, primitives are drawn in the load-fixed order established in
[03 §2.4] — the selection primitive swapped to index 0 and skipped, primitive
zero otherwise retained as the first unsorted face, and indexes one onward
stably ordered by the signed 32-bit mean key.

Because the key test admits equal keys, draw order is the tie-break, and the
reversed piece walk therefore means **piece 0 wins every tie against every
later piece**, while inside a piece the higher-mean-Y primitive wins. Equal-mean
faces retain their post-selection-swap order, so the later face in that stable
sequence wins an equal-key pixel. A forward piece walk inverts every one of the
inter-piece tie-breaks. This is not a
cosmetic detail: on stock models whole regions of the silhouette change owner.

**Established (composition experiment against stock geometry).**
Composing `ARMSOLAR` and `ARMLAB` from the base archive under the traced
policy and under a forward walk, and counting pixels whose owning piece
differs:

| Model / pose | Pixels covered | Forward walk | Forward walk + halved key |
|---|---:|---:|---:|
| `ARMSOLAR` rest | 1,609 | 18 differ | 20 differ |
| `ARMSOLAR` dishes open | 1,960 | 98 differ, all lost by `base` | 98 differ |
| `ARMLAB` rest | 6,023 | 194 differ | 194 differ |
| `ARMSOLAR` rest, reverse walk, halved key | 1,609 | — | 15 differ |
| `ARMLAB` rest, reverse walk, halved key | 6,023 | — | 38 differ |

Reading the table: the piece walk direction is the dominant term and the
halved key is a smaller independent one. With the dishes open, `ARMSOLAR`'s
`base` piece loses 98 pixels — the panels swallow the column and cap they
should be intersecting. On `ARMLAB` the pieces that lose area under a forward
walk are `base` (9 px), `stand1` (10), `stand2` (9) and six of the eight door
pieces — `door1` (47), `door1A` (23), `door2` (16), `door3` (40), `door3A`
(24), `door4` (16). The piece that takes those pixels is the last-indexed one,
`pad` — the build plate, which a forward walk lifts out through the roof it
should sit under.

The experiment is geometry-only: it composes the projected polygons of the
rest pose (plus, for the open case, each `ARMSOLAR` dish rotated 135 degrees
about its Z accumulator) and records which piece owns each pixel under the §2
admission rule. It does not sample textures, so it isolates the ordering and
key terms from everything else.

##### 4. Cached body, live pieces, and attached units

Each piece carries a **cache bit** beside its draw bit. The draw path uses it
to split the model in two, and passes a mode selector into the renderer that
says which half to draw:

- **mode "cached"** — draw only pieces whose cache bit is set;
- **mode "live"** — draw only pieces whose cache bit is clear;
- **mode "all"** — draw every visible piece regardless.

A unit under construction overrides the filter: while the construction
fraction is not the completed sentinel every visible piece is admitted in
every mode.

The composition image built in §1 is the **cached** half, and it is rebuilt
when the unit is first drawn, when the orientation cache of [03 §5.2]
goes dirty, when a structure's construction state changes, or when the
script writes described in [R-COMP-01 §4] invalidate it. Changes to a live
piece do not invalidate the cached half through the conditional cache-bit
gate. Discarding the image reference is separate from invalidating that
state: a missing structure or required key-plane image triggers a rebuild,
but a mobile subject without a required key plane may draw all visible
pieces directly when its image is absent and validity remains set. That
fallback also advances the validity state. Presentation
then proceeds down one of two paths, chosen on whether that cached image has a
key plane:

**No key plane.** The cached image is blitted to the framebuffer, and then
every piece whose cache bit is clear is rasterized **directly to the
framebuffer**, in reverse piece order, with no key plane and therefore in pure
painter order. Attached child units are drawn the same way.

**Key plane present.** A **staging image** is prepared whose box is the union
of the unit's own box and the boxes of all its attached child units, offset by
each child's world position relative to the parent; the cached image is copied
or re-blitted into it, both planes. Then:

1. the **live** pieces are rasterized into the staging image with the key test
   — so an animated door or plate resolves against the cached body per pixel,
   not by draw order. This pass is skipped for a structure under construction,
   whose cached image already holds every piece;
2. each attached child unit is composed into its own two-plane image and
   **composited into the staging image with the key test**, at the child's
   pixel offset and with the child's world-height difference added to every
   key it contributes. A child pixel is written when it is not the child
   image's transparent index and `stagingKey <= childKey + heightDelta`;
3. the waterline and digger passes of §7 run over the staging image;
4. the staging image is blitted once.

**Placement space (Established).** The child offset belongs to the composition
image: it positions the child within the union before the completed image is
blitted. The final image placement therefore moves the carrier and its
composited children together; it is not another independent child placement.

**Store width of the shifted key (Established, direct-static).** In step 2 the comparison is made at full register width — both
stored key bytes widened, the signed height delta added to the child's — but
the store narrows: the staging plane receives the **low byte** of
`childKey + heightDelta`, a wrapping byte add, not a saturation. A child
lifted far enough above its carrier for the sum to pass 255 therefore wins the
comparison and then records a small key that later children and the waterline
and digger erases see as low. Stock cargo and factory products stay within a
few tens of world units of their carrier, so the wrap is unreachable with
stock content; it is not a saturating clamp. The composite also has no clip of its own: it is skipped entirely when
the child's pixel offset into the staging image is negative on either axis,
and otherwise writes the child's whole box, which the union box always holds.

The unit placement path's second, separate invocation of the unshaded piece
renderer, targeting a different image record than the dispatcher's
([R-RND-02A]), is this live-piece pass: same renderer, different target (the staging image, not the cached one),
different mode selector (live rather than cached), and its guard — "the
structure-class bit is clear, or the construction fraction equals the completed
sentinel" — is exactly the "skip for a structure under construction" rule
above. It is neither a silhouette pass nor a second body pass.

##### 5. Primitive dispatch and the four span writers

Per primitive the dispatcher reads one flag word:

```
if (colored)                      -> flat polygon filler, explicit vertex count
else if (vertexCount != 4)        -> draw nothing
else {
    if (resolve-at-draw-time) {
        if (team)  texture = LOGOS entry frame chosen by the owner's shade byte
        else if (mode != cached-name-resolution) texture = entry frame 0
        else                                     texture = resolve by authored name
    } else          texture = the image the loader already resolved
    -> textured quad mapper
}
```

The `colored` flag is the authored 3DO `IsColored` field's bit 0; the
resolve-at-draw-time and team bits are written by the model loader, not by the
artist. **Established (asset census):** authored `IsColored` bit 0 is set on
exactly the 6,598 primitives with no texture name and clear on exactly the
43,845 with one, across all 608 base models — a perfect partition, so bit 0 is
a sound flat/textured discriminator on stock content. The resolve-at-draw-time
bit is **never** authored (0 of 50,443), confirming that the loader owns it.

The textured quad mapper builds the ten-dword edge records of [03 §5.2] and,
when the caller supplies no UV table, defaults the four corners to
`(0,0) (w-1,0) (w-1,h-1) (0,h-1)` — the corner-index affine rule of
[03 §5.2], with the clamp built into the default rather than applied
afterwards.

There are four span writers, one per (shaded, unshaded) × (textured, flat):

| Renderer | Face | Writes |
|---|---|---|
| unshaded | textured quad | the sampled texel, raw |
| unshaded | flat polygon | the color byte, raw |
| shaded | textured quad | `SHD[row*256 + texel]` |
| shaded | flat polygon | `SHD[row*256 + color]` |

All four apply the §2 key test identically and all four fall through to
unconditional writes when the key plane is absent. **Established
(bounded-negative):** none of the four tests the sampled texel against a
transparent or color-key index — within the model raster path a texture is
fully opaque. Transparency in the model path is expressed only by the
composition image's own background index, which is what the final blit keys
against.

##### 6. Structure anti-aliasing: the 2× supersample and the ALP downscale

Both renderers — shaded and unshaded, the same code in each — open with the
same gate. When **all three** of

- the global display option `Anti_Alias` is on,
- the unit instance's class bit says structure (`BMcode=0`, [R-RND-02A]), and
- the mode selector is not "live"

hold, the renderer does not rasterize into the caller's image. It takes the
shared scratch image, sets its width, height and both origin components to
**exactly twice** the caller's, clears its key plane to `0` and its colour
plane to the transparent index, and rasterizes the whole model into that.
Every projected vertex component is shifted left by one before the shear:

```
sx = 2*hi16(x) + 2*origin.x
sy = (2*hi16(-z) - ((2*hi16(y)) >> 1)) + 2*origin.y
```

Note that `(2y) >> 1` is exactly `y`, whereas `2 * (y >> 1)` loses the low
bit, so the supersampled shear is a half-pixel more accurate than a doubled
copy of the plain one — reproduce the expression, not a scaled version of the
1× result. The key is computed as in §2 and comes out on the **same scale** as
the 1× path.

When the model is finished the scratch image is resolved down 2:1 into the
caller's image, colour and key by different rules:

**Colour — a three-lookup box filter through `ALP`.** For each destination
pixel `(x, y)` the four source pixels of the corresponding 2×2 block are
combined with the 256×256 blend table `ALP` of [03 §4.3]:

```
top    = ALP[ src[2y  ][2x] * 256 + src[2y  ][2x+1] ]
bottom = ALP[ src[2y+1][2x] * 256 + src[2y+1][2x+1] ]
dst[y][x] = ALP[ top * 256 + bottom ]
```

Three table reads, no arithmetic on the indices, no special case for any
value. Order matters (`ALP` is not required to be symmetric): left index
first within a row, top result first between rows.

**Key — nearest sample, no blend.** Only when the destination image actually
has a key plane, each destination key is copied from the **top-left** source
sample of its block, `dst[y][x] = srcKey[2y][2x]`. The three other samples are
discarded.

Two consequences follow directly and both are visible in retail:

- **Mobile units are never anti-aliased.** The gate is the same structure-class
  bit the shading gate uses, so `BMcode=1` units are rasterized at 1× whatever
  the `Anti_Alias` setting is. Buildings are smoother than units, and that is
  authored-data-driven, not an artefact.
- **Animated pieces are not anti-aliased either.** The live-piece pass of §4
  runs in "live" mode, which fails the third condition, so a factory's doors,
  pads and nano beams are rasterized at 1× directly into the staging image
  over an anti-aliased cached body.

**The `Anti_Alias` default is on.** The settings reader sets the bit on a
registry miss and writes it back ([R-RAST-01 §4]).

**Bounded-negative residual.** The 2× scratch and the §4 staging image are the
**same** buffer. Within one unit's presentation they are used in sequence, so
they do not collide; but the attached-child composition of §4 step 2 asks for
each child's image *after* the staging image has been built in that buffer,
and a child that is itself a structure would therefore rasterize over the
staging image. No stock configuration reaches it — a factory's child is the
mobile unit on its pad and a transport's child is its cargo, both `BMcode=1`,
and the gate needs `BMcode=0` — so the case is unreachable on stock content
rather than handled. Do not reproduce the aliasing; keep the two buffers
separate. `TODO(question): whether any retail-reachable configuration attaches
a BMcode=0 child unit.`

##### 7. The red/purple fringe is the downscale blending with palette index 1

The mechanism is arithmetic; no capture is needed to attribute the fringe
pixels ([R-REN-02R] carries the bounded-negative census of what they are not).

The composition image's background is not "nothing". It is the ordinary
palette index **1**, written into every pixel of the colour plane before
rasterization and recorded in the header as the index the final blit will skip.
The downscale of §6 has no notion of that: it feeds all four samples of every
2×2 block through `ALP` unconditionally. So

- a block wholly outside the model is `ALP[ALP[1,1] * 256 + ALP[1,1]]`, and
  because `ALP`'s diagonal is exact identity — **Established (measured against
  the retail `PALETTE.ALP`: all 256 diagonal entries are self-mapping)** —
  this is `ALP[1*256 + 1] = 1`, the background index again, and the pixel
  stays transparent;
- a block wholly inside the model blends four model colours, which is the
  anti-aliasing the option is for;
- a block **straddling the silhouette** blends model colour with index 1.

`PALETTE.PAL` entry 1 is `(128, 0, 0)` — dark maroon. Blending any model
colour halfway toward dark maroon and snapping to the nearest palette entry
lands, for most of the palette, in the reds and dusty purples: **Established
(measured against the retail `PALETTE.PAL` and `PALETTE.ALP`)** — of the 256
possible partners `x`, the entry `ALP[1][x]` is red-dominant (its red channel
exceeds both green and blue by more than 40) for 169 of them, and
purple/magenta (red and blue each exceed green by more than 30) for 19. The
two criteria overlap; they are stated as thresholds so the count can be
recomputed rather than taken on trust. `ALP[1][255]` (white against the background) is entry 20,
`(175, 111, 127)`, a dusty pink; `ALP[1][0]` and `ALP[1][240]` are entry 207,
`(79, 15, 0)`; `ALP[1][208]` is entry 200, `(223, 79, 7)`.

That is the fringe, exactly: a one-pixel border of reds and pinks along the
outline of every anti-aliased building, absent from mobile units and absent
from the interior. It is a genuine defect in the retail engine — the
downscale should either exclude background samples or blend against what is
actually behind the unit — and the reason the palette makes it lurid is
incidental: index 1 is the first entry of the classic EGA-order ramp and TA
never repurposed it.

**We reproduce it.** The engine is a clone; a filter that skips background
samples would draw cleaner silhouettes than retail and would not match a
reference screenshot. Implement the three `ALP` lookups verbatim, including
the background samples.

Note also what this rules out. The fringe is **not** authored texture content
— an authored-fringe reading does not explain why the colours appear only on
edges, only on buildings, and only in one-pixel width. It is not the model
shadow, not a dither, not a team mapping, and not an outline writer; no
generic outline writer exists ([R-REN-02R]). The fringe pixels are downscale
outputs, and their neighbours inside the silhouette are ordinary blended
texture.

##### 8. Waterline, digger clipping, and the model shadow

Three passes run over the finished image before it is blitted, all of them
keyed on §2's height key. They are the reason the key base is 50 rather than 0:
the key must stay non-negative for geometry below the model origin.

**Waterline.** With `t = seaLevel - trunc(unitWorldY)`, when `t > 0` part of
the unit is below the water surface and the threshold `t + 50 [+75 if Digger]`
selects it. Which pass runs depends on ownership:

- if the unit does not carry the sonar-contact bit of [R-VIS-01 §4] **and**
  its owner is not the local player, every pixel with `key <= threshold` is
  **erased** — set to the image's transparent index — so a submerged enemy
  simply is not drawn below the surface;
- otherwise every pixel with `key <= threshold` whose colour is not already
  the transparent index is recoloured through a 256-entry **`BLUE TABLE`**, so
  the local player sees their own submerged hull tinted rather than cut off.

**Digger clipping.** A definition that authors `Digger` gets `+75` added to
every key in §2, and after the waterline pass the image is erased wherever
`key <= 125`. Since `125 = 50 + 75`, that erases exactly the geometry at or
below the model origin: the buried half of a pop-up defence. **Established
(asset census):** `Digger=1` on three stock units, `ARMAMB`, `CORTOAST` and
`CORVIPE`.

**Model shadows use three branches.** The complete Established contract is
[R-REN-03D], with the branch census in [R-RAST-01 §4]. After the common
master-shadow and `noshadow` tests, retail selects exactly one branch:

1. A **Digger** copies the finished body image, flattens each non-transparent
   pixel to palette index 0, erases the part at or below key `50 + 75`, and
   sends the result through the tinted blitter. This branch additionally
   requires vehicle shadows and rejects `canhover` and `floater`.
2. An ordinary **mobile** subject uses the same copy-and-flatten silhouette.
   When it is partly submerged, it erases pixels at or below
   `seaLevel - hi16(unitY) + 50` before the tinted blit. It has the same
   vehicle-shadow, `canhover`, and `floater` gate as the Digger branch.
3. A **structure** that is not a Digger re-rasterizes the model with the
   dedicated ground-shadow projection, punches the body silhouette out of the
   result, run-length-encodes it, and caches that shadow image. It does not test
   the vehicle-shadow, `canhover`, or `floater` gates. Its extra predicate is
   `definitionOrdinal == 0 && hi16(unitY) < seaLevel`; identifying ordinal 0
   exclusively with the feature pseudo-unit is the Supported inference stated
   in [R-REN-03D §1].

All three branches place the shadow five pixels right of the body, shear it by
the terrain height under the subject, and blend palette index 0 through `ALP`.
The tinted blitter requires the alpha-blend table capability enabled at window
startup; `Shading` does not control shadow visibility [R-REN-03D §4]. None of
the branches uses a stencil, dither, or `SHD` row.

**Table roster.** The renderer installs **five** tables. In install order and
size: the 65,536-byte
`ALPHA TABLE` (`ALP`), the 8,192-byte `SHADE TABLE` (`SHD`), the 8,192-byte
`LIGHT TABLE` (`LHT`), a 256-byte `GRAY TABLE` ([03 §4.3.3]), and a 256-byte
`BLUE TABLE`. The anti-alias downscale of §6 reads `ALP`; the shaded span
writers read `SHD`; the submerged tint of this section reads `BLUE TABLE`.
The fifth table slot is the blue tint and its consumer is the waterline, not
a shadow; `ALP` is used, by the anti-alias downscale, three times per output
pixel.

##### 9. Order of operations, in one place

```
per unit, once its cached image is valid:
  1. shadow    the Digger, mobile, or structure branch above,
               tinted-blitted at (x+133, z - ground/2 + 32)
  2. staging   union box of the unit and its attached children; cached image
               copied in, both planes
  3. live      pieces with the cache bit clear, rasterized into staging with
               the key test, at 1x (never anti-aliased)
  4. children  each attached unit composed into its own two-plane image and
               key-composited into staging with its height delta
  5. water     erase or blue-tint below the waterline; erase below key 125
               for a Digger
  6. blit      staging blitted at (x+128, z - unitY/2 + 32), skipping the
               transparent index

building the cached image (step 0, on first draw / orientation dirty /
construction change):
  a. measure the visible pieces, allocate one- or two-plane image per ZBuffer
  b. if Anti_Alias and BMcode=0: rasterize into the 2x scratch instead
  c. walk pieces last-to-first; per piece, primitives in load-fixed order,
     selection primitive skipped
  d. per primitive: colored -> flat filler at any arity; else quads only
  e. per pixel: admit when storedKey <= incomingKey
  f. if the 2x scratch was used: ALP box-filter colour 2:1, nearest-sample key
```

#### R-REN-03D — model shadows: projection, fill, tinting, and cache

The Digger and ordinary-mobile branches of [R-REN-03A §8] use the flattened
silhouette copy, while the structure branch re-rasterizes the model through a
dedicated shadow projection and caches the result. This section is the whole
contract. Everything is **Established (direct-static)** unless a paragraph
says otherwise.

##### 1. The gate

A subject reaches model-shadow work only when the options word's master shadow
bit is set and its definition does not author `noshadow`. Retail then selects
exactly one branch:

1. **Digger:** requires the vehicle-shadow bit and rejects `canhover` and
   `floater`; copies and flattens the finished body silhouette, then erases
   pixels whose key is at or below `50 + 75` so the buried half casts no
   shadow.
2. **Mobile:** selected when the structure-class bit is clear; has the same
   vehicle-shadow, `canhover`, and `floater` gate; copies and flattens the body
   silhouette, then, when `t = seaLevel - hi16(unitY) > 0`, erases pixels at
   or below `t + 50` so only the above-water hull casts a shadow.
3. **Structure:** selected when the structure-class bit is set and Digger is
   clear; uses the dedicated rerasterization of §2 and the punched RLE cache of
   §5. It does not test vehicle shadows, `canhover`, or `floater`. It is skipped
   when the definition ordinal is 0 and `hi16(unitY) < seaLevel`; ordinal 0 is
   the feature pseudo-unit, so this suppresses the shadow of a 3DO wreck below
   sea level. The conclusion that ordinal 0 is reserved is a **Supported
   inference** from the loader walk; a live-type writer to ordinal 0 would
   settle it.

All branches end at the tinted blitter of §4. Its alpha-blend table capability
is enabled at window startup independently of the **`Shading`** preference.
Turning that preference off does not suppress shadows.

The bulk INI shadow key writes one value into the feature-shadow bit and then
fans it down to the vehicle and master bits, so a player toggling shadows moves
all three together; the per-category keys move only their own bit.

##### 2. The structure-shadow rasterization

**Established (direct-static):** only visible cached pieces enter the shadow
rasterizer, both during construction and after completion. The body's
construction override does not apply to this separate pass. Shadow bounds
still measure every visible piece, including noncached pieces.

The structure shadow is **a second rasterization of the model**, not a reuse of
the body image. Its projection is the composition path's own narrowing
([R-RAST-01 §2]) — the high word of each model-relative 16.16 coordinate, with
the Z lane negated *before* that narrowing, which is the 3DO handedness flip
the body pass applies too. It differs from the body only in the shear: there is
no half-height term at all — the shadow lies flat on the ground — and instead
each vertex is sheared by a quarter of its own height:

```
q   = hi16(vertexY) >> 2                 (arithmetic shift; floors)
sx  = hi16(vertexX) + q
sy  = hi16(-vertexZ) - q
key = hi16(vertexY) + 25
```

The narrowing is the high word of the 16.16 value (floor, as for every
composition-path vertex, [R-RAST-01 §2]) and the Z lane is negated before it,
exactly as the body's own screen Y lane `Zn = hi16(-vz)`; the shadow walk
differs from the body walk only in the cache-bit gate, the flat filler at any
vertex count, the literal fill colour, and the quarter shear in place of the
half shear. Two projections of one model that disagreed on handedness would
make a structure's shadow a Z-mirrored copy of its own body, out of step with
the Digger and mobile branches of §6, which cast the finished body silhouette
itself and therefore carry the body's sign by construction. **Established
(direct-static).**

That is a 45-degree light in screen space: a vertex one unit up moves a quarter
pixel right and a quarter pixel up. The key base is **25**, not the body
image's 50 — the two images are independent, and each base only has to keep its
own geometry non-negative.

The image is measured and allocated exactly as the body image is
([R-REN-03A §1]): extrema seeded at the model origin, two-pixel margin, colour
plane prefilled with the transparent index, key plane zeroed.

The walk is the body walk with three differences:

1. a piece contributes only when **both** its draw bit and its cache bit are
   set, so animated pieces cast no shadow;
2. every primitive goes to the **flat polygon filler** at any vertex count —
   textures are never consulted and no `SHD` row is computed;
3. the fill colour is the literal **palette index 0** for every face.

The load-time selection primitive is skipped, as everywhere else.

##### 3. Placement

```
shadowX = trunc(worldX - camX) + 128 + 5
shadowY = trunc(worldZ - camZ) - (terrainHeightUnderUnit >> 1) + 32
```

The body's own placement is the same expression with `+128` and the unit's own
height in the shear ([R-REN-03A §9]). So the shadow sits **five pixels right of
the body** and is sheared by the ground rather than by the model, which is what
makes a shadow slide up a slope while the unit stays put.

##### 4. The tinted blitter is an `ALP` blend

The blitter every shadow goes through is, per destination pixel:

```
if (src != srcImage.transparentIndex)
    dst = ALP[ src * 256 + dst ]
```

Three facts follow. It reads `ALP`, the same 65,536-byte blend table the
anti-alias downscale uses. It is a **blend with what is already on the
ground**, so a shadow darkens terrain rather than replacing it. And because
every shadow pixel is index 0, the result is `ALP[0*256 + ground]`: each ground
pixel snapped to the nearest palette entry halfway to black. No `SHD` row, no
stencil, no dither, no per-category darkness.

**Established (direct-static): table capabilities and display preferences are
separate state.** The tinted blitter returns without drawing when the window's
alpha-blend table capability is clear. The same capability controls allocation,
loading and rebuilding of `ALP`. Ordinary application startup explicitly requests
this capability before window initialization; it also requests the shade, light,
gray and blue tables. These resources live for the window's lifetime and are
released during window teardown. Changing display mode preserves their flags.

The `Shading` preference's INI loader, Options handler and restore-defaults
handler write a different state word. That preference selects the shaded
structure-body renderer [R-RND-02A]; its Options handler also invalidates cached
model compositions. It does not change table capabilities. Thus disabling
`Shading` leaves model shadows, tinted feature shadows and tinted strip sprites
visible whenever their own admission gates pass. The earlier claim equating
this capability with `Shading` is withdrawn after tracing both writers and their
consumers.

The flash blitter has a separate light-table capability gate, also enabled by
ordinary startup. The lens blitter has no palette-table capability gate at all;
its capture and keyed-copy path is specified in [R-FX-01 §4].

##### 5. The structure shadow's cached sprite

After the structure rasterization of §2, retail additionally:

1. composites the **body** image into the finished shadow image, writing the
   shadow image's *transparent* index wherever the body is opaque — punching
   the body's own silhouette out of its shadow so the ground beneath it is not
   darkened before the body covers it;
2. run-length-encodes the shadow image's colour plane row by row, each row
   prefixed with its encoded length;
3. allocates a second image record holding that encoded stream, marks it
   RLE, copies the dimensions and origin across, and caches it on the draw
   record. The cache is dropped whenever the composition image is rebuilt.

The punch-out's five-pixel offset and the blit's cancel: the hole lands
exactly at the body's own screen position ([R-RAST-01 §4]). Implement the
punch-out.

##### 6. The silhouette branches

The Digger and ordinary-mobile branches copy the unit's own finished
composition image — both planes — and flatten every non-transparent colour
pixel to palette index 0. Their branch-specific key-plane erasures are stated
in §1. The structure branch instead re-rasterizes through §2 and fills its
faces with index 0 directly. All three end at the same §4 blitter with the same
§3 placement: a black silhouette blended over the ground, five pixels right
and sheared by terrain height.

#### R-REN-02R — red/purple fringe provenance

The fringe is produced by the structure anti-alias downscale of
[R-REN-03A §6]–[R-REN-03A §7]: a building is rasterized into a 2x offscreen
image whose background is palette index 1, and the 2:1 downscale feeds all
four samples of every 2x2 block through the `ALP` blend table without
excluding background samples. Blocks that straddle the silhouette therefore
blend model colour with `PALETTE.PAL` entry 1, `(128, 0, 0)` — dark maroon —
and land on red or dusty-purple palette entries. The fringe pixels are already
present in the unit's own composition image before it reaches the world
surface. Reproduce the filter verbatim, background samples included: a
downscale that skipped them would draw cleaner silhouettes than retail. This
section records the asset and renderer census that excludes every other
candidate.

**Established (asset census).** The representative ARMSOLAR and
ARMLAB 3DOs reference ordinary indexed GAF model textures. The inspected
entries are uncompressed, use color key `9`, and contain authored red/maroon
or purple palette indices as ordinary texels:

| Model examples | Representative referenced texture entries (non-exhaustive) | Asset evidence |
| --- | --- | --- |
| ARMSOLAR base and dish pieces | `stone2`, `CorSol1a`, `metal3a`–`metal3d`, `Arm01b`–`Arm01d`, `32XGouraud` | `stone2` is 64×64 with no key texels and includes red/maroon source indices 19, 21, 22, 23, and 27; `CorSol1a` is 32×64 with no key texels and includes purple source indices 154–159 and 221. |
| ARMLAB base and child pieces | `ArmV3a`–`ArmV3d`, `noise6b`–`noise6d`, `Energy1`, `Energy4`, `ArmPlat02`, `ArmPlat02c`, `32XGouraud` | The entries are likewise indexed model textures; red/purple indices occur in `noise6*` and `Energy4` rather than in one special fringe resource. |

These are source-texture indices, before any model `SHD` lookup. Such values
occur inside opaque texture interiors and recur in unrelated model textures;
they are real authored content, but they do not explain a one-pixel border
that appears only on edges, only on `BMcode=0` units, and only when the
`Anti_Alias` option is on. The two observed color-key texels in `Arm01b` are
on its top edge; that bounded example does not generalize to a color-key
edge effect. Texture selection, indexed sampling, and the shaded/unshaded
`SHD` boundary are the contracts in [03 §2.4.1] and [03 §4.3].

**Established (bounded renderer behavior).** The model face mapper consumes a
source GAF index and either writes that index directly or applies the
selected `SHD` row; it has no generic anti-alias, outline, RGB blend, or
random/dither fringe writer of its own — the anti-aliasing is the resolve
step that runs after the whole model is rasterized, not the mapper. The
separate model-shadow path is the three-branch `ALP` contract of [R-REN-03D]
and uses neither a stencil nor dither; it does not write the model's body
pixels. Painter ordering means a later admitted writer can replace an earlier
indexed pixel; this is not evidence that the replacing writer is a fringe
pass.

The candidate causes, classified:

- **Palette lookup — Established, and it is `ALP`, not `SHD`.** The row and
  output index are given by the three-lookup box filter in [R-REN-03A §6];
  the specific outputs for white, black and yellow sources are measured in
  [R-REN-03A §7].
- **Authored texture fringe — not the cause.** The colored source texels
  catalogued above are real, but they do not explain the border's
  edge-only, structure-only, option-gated appearance.
- **Team mapping, shadow, dither, colour-key edge — excluded.** Model shadows
  are drawn before the body and do not write body pixels; no model-shadow
  branch uses dither; the exactly-10-frame `32XGouraud` team-texture rule
  is established but writes no fringe pixel.
- **Outline writer — none exists.** This is not permission to add one.
- **Framebuffer compositing — not involved.**

#### R-SEL-02A — selection geometry, palette, and composition boundary

**Established (direct-static).** The authored 3DO selection primitive is
swapped to primitive zero during model loading and the ordinary model-face
walk starts at primitive one whenever such a primitive exists. It therefore
does not contribute a model pixel, a model paint key, or a model hit shape.
The same exclusion is used by completed
and under-construction model paths; a construction image does not re-admit
primitive zero.

The visible selection rectangle is a separate composer overlay. While the
box-selection/wake predicate is true, the composer projects its two recorded
world endpoints with the ordinary integer projection, sorts X and Y
independently, and draws an outer and an inner one-pixel frame. Both frames
use inclusive endpoints: a frame covering `[left,right] × [top,bottom]`
writes its edge pixels through `right` and `bottom`, and the inner frame is
formed by incrementing left/top and decrementing right/bottom. A degenerate
inset where a minimum exceeds a maximum writes no inner frame. The solid
frame writer is clipped against the active inclusive world-surface clip.

The palette argument is already a physical indexed-pixel value. The outer
frame selects logical map entry 15 and the inner frame always selects entry 0;
the 6/4 pair replaces the outer entry only while the armed latch is MOBILEBUILD
— entry 6 when **bit 6 of the pointer-flags byte** is set, entry 4 when it is
clear [07 R-P0-11 §1 "The drawing."][07 §6 "Frame composition passes"]. That
bit is the site-valid bit of [07 R-CAM-01 §14] step 1, whose one writer is the
in-view placement preview and whose clearer is the world rebuild; it is not
bit `0x40` of the latch-flags word, which carries immediate-versus-special
helptext. Each logical
entry is resolved once through the runtime logical-to-physical map before the
solid writer; there is no ALP, LHT, or SHD operation and no per-pixel blend.
This is the complete palette contract for the observed selection outline; no
authored-plate wireframe palette exists in the traced renderer.

Fog is composed after the world strips, units, projectiles, and effects, and
before this selection overlay. Thus world pixels are subject to the fog/LOS
presentation, while the selection outline is not. The selection solid-frame
writer consumes the inclusive clip rectangle copied into the active surface
descriptor from presentation state. Its rectangle coordinates, however, are
formed in the beam/projection space with the separate `+128/+32` projection
offsets. These are distinct coordinate records: the projection offsets must
not be substituted for the clip rectangle's left/top values. The HUD rail is
composed later and may cover overlapping outline pixels. A separate
authored-plate clip policy cannot be claimed because the plate is not drawn.

**Viewport-coordinate record.** The transition-time battle viewport record
and the beam-space projection origin are distinct. The static call chain proves
which record the selection writer consumes, but not the record's left edge
for every panel/mode state (the corpus contains both a visible-panel
`(128,32,W-1,H-33)` description and a transition/input `(0,32,W-1,H-33)`
description). Therefore the selection clip's left value outside a captured
state is **Unknown**; a mode/panel capture of the descriptor at the selection
call would settle it. Neither prior tuple is a universal canonical value.

**Unknown.** The static trace does not establish a per-selected-unit plate
pass, an additional primary-selection treatment, or any use of the authored
selection polygon by cursor targeting. It also does not establish the exact
edge convention of the projected four-corner hover hull's polygon test
beyond the separate drag rectangle's inclusive rule. A focused retail
capture with two selected units, one primary/single selection, and points on
each projected hull edge would settle those remaining presentation and
boundary questions.

### OTA-RND-02A — model-path shading and stock reachability [R-RND-02A]

The unit piece-draw dispatcher makes **two independent decisions**. The
first — which image record the piece geometry is composed into — is taken
from runtime draw state and tests no FBI field. The second, taken further
down the same dispatcher, chooses **which of two piece renderers** runs, and
it is a class gate: retail shades structures and never shades mobile units.
Retail visibly shades buildings, or parts of them, and never shades mobile
units; the contract below is what produces it.

**Established (direct-static): the two decisions.**

1. *Image source.* The fixed/mobile split selects an image-cache/rebuild path
   from runtime draw state (main versus auxiliary draw, the runtime unit state
   bit, and construction fraction). It tests no FBI field.
2. *Rasterizer.* The dispatcher then runs the **shaded** piece renderer if and
   only if two conditions both hold: the unit instance's class/status word has
   the structure-class bit set, **and** the global display option named
   `Shading` is enabled. Otherwise it runs a separate **unshaded** piece
   renderer. The two renderers take identical arguments and are the only two
   consumers of this dispatch.

**Established (direct-static): the class bit is `BMcode`.** When a unit
instance is bound to its definition at creation, the structure-class bit of
the instance class/status word is written as the truth of *the definition's
`BMcode` byte is zero* — the same byte, and the same "zero means structure"
test, that the yard-map parser uses [05 "Geothermal requirement"] and that the
build-order
and build-placement paths use [07 §9]. It is not derived from `CanMove`,
`MaxVelocity`, `MovementClass`, or `CanFly`. The engine's internal
feature-backed pseudo-unit sets the same bit unconditionally at construction,
having no FBI to read. So the renderer's class gate reduces to authored data:
**`BMcode=0` (structures) are shaded; `BMcode=1` (mobile units) are not.**

**Established (direct-static): what the two renderers differ in.** Both walk
the same piece list, honour the same per-piece draw and cache bits, apply the
same selection-plate exclusion, the same quads-only rule for textured
primitives and any-arity rule for flat ones, and the same team-texture
(`LOGOS`) treatment. The shaded renderer
additionally computes face normals and per-vertex smooth normals, derives a
`SHD` row per vertex, emits a fourth per-vertex component carrying that row,
and hands textured faces to the Gouraud `SHD` scanline mapper of section
2.4.1. The unshaded renderer emits three-component vertices with no row at
all and hands textured faces to a plain texture mapper that performs no `SHD`
lookup. Untextured flat faces take the shaded flat writer
(`SHD[row*256 + color]`) in the shaded renderer and the raw colour byte in
the unshaded one ([R-REN-03A §5]).

**Established (bounded-negative):** the per-piece shade bit is read **only**
inside the shaded renderer. The unshaded renderer reads the same piece flags
byte for the draw bit and the cache bit and never tests the shade bit.
`SHADE` and `DONT_SHADE` are therefore inert on a `BMcode=1` unit — nothing
downstream of the script consumes the bit they write.

**Established (direct-static): the `Shading` option.** The display option word
carries one bit per toggle; the bit the shading gate tests is the one written
by the handler bound to the name string `Shading`. The Options screen's
restore-defaults path sets that bit, so shading is on unless the player turns
it off. With it off, structures take the unshaded renderer too and the game
draws no model shading at all.

**Established (direct-static): piece-flag polarity.** The model fill starts each geometry-bearing piece with the shade
bit **set**; the interpreter passes one for `SHADE` and zero for `DONT_SHADE`
into a per-piece setter that writes that value into the bit; and the shaded
renderer computes the normal-derived row when the bit is set and pins row 15
when it is clear. See [04 "OTA-RND-02A script-side shading census"].

The stock asset census below is a bounded check of the gate and of the
script-side overrides. Values in
the FBI columns are authored values (an absent key is the normal false
default); `3DO` is `pieces / primitives / textured / flat / clear`, and
`Create DONT` is the number of `DONT_SHADE` operations found in the named
script's `Create` callback, followed by the addressed piece names where useful.
All requested models had zero clear primitives. **Established (asset census,
base `totala1.hpi`):**

| Unit / observed counterpart | BMcode | CanMove | CanFly | 3DO | Create DONT |
|---|---:|---:|---:|---:|---|
| ARMCOM / CORCOM | 1 / 1 | 1 / 1 | 0 / 0 | 15/102/77/25/0 / 16/87/44/43/0 | 0 / 0 |
| ARMPW / CORAK | 1 / 1 | 1 / 1 | 0 / 0 | 15/88/41/47/0 / 16/59/32/27/0 | 0 / 0 |
| ARMSTUMP / no same-role CORE model in base corpus | 1 / — | 1 / — | 0 / — | 4/58/53/5/0 / — | 0 / — |
| ARMFIG / CORVAMP | 1 / 1 | 1 / 1 | 1 / 1 | 8/59/54/5/0 / 9/52/46/6/0 | 0 / 0 |
| ARMSOLAR / CORSOLAR | 0 / 0 | 0 / 0 | 0 / 0 | 5/39/38/1/0 / 10/63/58/5/0 | 0 / 10 (base, shell, leg1–4, wing1–4) |
| ARMLAB / CORLAB | 0 / 0 | 1 / 1 | 0 / 0 | 16/107/103/4/0 / 19/174/165/9/0 | 15 / 18 |
| ARMVP / CORVP | 0 / 0 | 1 / 1 | 0 / 0 | 14/165/160/5/0 / 17/110/107/3/0 | 11 / 15 |
| ARMAAP / CORAAP | 0 / 0 | 1 / 1 | 0 / 0 | 12/165/164/1/0 / 19/170/164/6/0 | 11 / 18 |

The `DONT_SHADE` piece operands were: CORSOLAR `base`, `leg1`–`leg4`,
`shell`, `wing1`–`wing4`; ARMLAB `beam1`, `beam2`, `door1`, `door1A`,
`door2`, `door2A`, `door3`, `door3A`, `door4`, `door4A`, `nano1`, `nano2`,
`pad`, `stand1`, `stand2`; CORLAB `blink`, `beam1`, `beam2`, `gun1`, `gun2`,
`lbox1`, `lbox2`, `ldoor1`, `ldoor2`, `lower1`, `lower2`, `pad`, `ubox1`,
`ubox2`, `udoor1`, `udoor2`, `upper1`, `upper2`; ARMVP `doo2`, `door1`,
`nano1`, `nano2`, `pad`, `plate1`, `plate2`, `post1`, `post2`, `side1`,
`side2`; CORVP `arm1`, `arm2`, `pad`, `gun1`, `gun2`, `layer1a`–`layer1c`,
`layer2a`–`layer2c`, `layer3a`–`layer3c` (the `pad` operation occurs twice);
ARMAAP `lights`, `radar`, `beam1`, `beam2`, `building1`, `building2`, `nano1`,
`nano2`, `nanobox1`, `nanobox2`, `pad`; and CORAAP `dish`, `blinks`, `beam1`,
`beam2`, `block1`, `block2`, `bump1`, `bump2`, `conduit1`, `conduit2`, `gun1`,
`gun2`, `head1`, `head2`, `pad`, `pedistal`, `sleeve1`, `sleeve2`. The
zero-count rows have no addressed pieces. No additional FBI flag was found
in the bounded renderer selector; the runtime state bit's authored semantic
name remains Unknown.

The apparent counterpart gaps are deliberate: the base archive contains no
`CORSTUMP` or `CORFIG`, so this census does not substitute another model by
name or presumed role. The `ARMPW` and `ARMFIG` rows use the observed
`CORAK` and `CORVAMP` assets, respectively. `CanMove=1` on the BMcode-zero
structures ARMLAB, ARMVP, ARMAAP and their CORE counterparts is a direct
asset counterexample to using `CanMove` as the renderer classifier. The model
format's primitive dispatch and texture fields are defined in [fmt 3do], and
the FBI defaults and `BMcode` field are defined in [fmt fbi].

The opcode census also changes the interpretation of “building piece
shading”: stock factory scripts explicitly clear shading on selected pieces
in `Create` (including nearly every piece of ARMLAB, CORLAB, ARMVP, CORVP,
ARMAAP, and CORAAP), while the requested mobile scripts contain no such
`Create` operation. This is piece/script-authored behavior, not an FBI class
gate. **Established (static bytecode census):** no requested script uses
`SHADE` or `DONT_SHADE` in `Activate`/`Deactivate`; no requested script uses
`SHADE` in `Create`; `DONT_SHADE` is the only stock override found in the
`Create`/activation callback set, and it addresses the named pieces in the
table.

Reading the census: every row with `Create DONT` greater
than zero is a `BMcode=0` structure, and every `BMcode=1` row has a zero
count. That is not a coincidence of authoring taste: a `DONT_SHADE` in a
mobile unit's script would have no observable effect, because the renderer
that unit reaches never reads the bit. The stock scripts that do use the
opcode are the animated structures, exempting doors, pads, nano beams,
landing plates and blinkers from the normal-derived row while the rest of the
structure stays shaded — which is exactly the "parts of a building are
shaded" appearance. `ARMSOLAR`, a `BMcode=0` structure with no `DONT_SHADE`
at all, is fully shaded.

**Effective policy.** Select the shaded piece renderer when
the unit's definition authors `BMcode=0` **and** the `Shading` display option
is on; otherwise select the unshaded renderer. In the shaded renderer, apply
the per-piece shade bit (set by default, cleared by `DONT_SHADE`, restored by
`SHADE`), pin row 15 when it is clear, and write flat faces through the same
row. In the unshaded renderer, ignore the per-piece shade bit entirely.
Do **not** gate on `CanMove`, `MaxVelocity`, or `CanFly`.

**Unknown.** The identical-model six-variant runtime matrix was not run: the
clean-room work-unit constraint forbids automating the retail executable, so
pixel-for-pixel outcomes of that synthetic matrix remain unverified against a
running retail build. The static contract above does not depend on it.

**The second unshaded-renderer invocation** — targeting a different image
record than the dispatcher's, guarded by "the structure-class bit is clear,
or it is set and the construction fraction equals the completed sentinel" —
is the **live-piece pass** of [R-REN-03A §4]: the same
renderer, targeting the staging image rather than the cached one, with the
mode selector set to draw exactly the pieces whose cache bit is clear — the
animated doors, pads, nano beams and blinkers. Its guard is the "skip for a
structure under construction" rule, because a structure under construction has
every visible piece in its cached image already. It is neither a second body
pass nor a silhouette pass. Note also that the live pass always runs the
**unshaded** renderer: it is not routed through the `BMcode`/`Shading` class
gate, so an animated piece on a `BMcode=0` structure takes no `SHD` row even
when the rest of that structure is shaded. Stock factory scripts clear the
per-piece shade bit on exactly those pieces anyway (the `DONT_SHADE` census
above), so the two mechanisms agree rather than compete.

#### The polygon raster, exactly: edge walk, span inclusion, the winding cull, and the fixed-point steps [R-RAST-01 §1]

[R-REN-03A §5] named the four span writers and the key test; this section is
the scan converter that feeds them, at the precision an implementer needs to
reproduce every edge pixel. Everything here is **Established (direct-static)**
unless a paragraph says otherwise. There are two filler families — the **flat
polygon filler** (explicit vertex count, one colour byte) and the **textured
quad mapper** (exactly four corners, one texture frame, an optional UV table
that no model caller ever supplies) — and each family exists three times: an
**unshaded** and a **shaded composition-image** variant, one per renderer,
each writing into a unit's private image with the key test and bounded by that
image's own dimensions, and a **framebuffer** variant used by the live-piece
fallback of [R-REN-03A §4] and by the projectile/debris model paths, which
clips against the surface clip rectangle and has no key plane. Those six
clipping fillers share one edge walk; the differences are only in clipping
bounds and in what the span writer stores. A seventh routine repeats the flat
filler with **no bounds reject at all** and has exactly one caller; which
drawing path uses it is **Unknown** (its caller was not traced).

**Inputs.** Per corner: integer pixel `x`, integer pixel `y` (already
projected and origin-biased per §2 below), an integer `key`, and — shaded
renderer only — an integer `SHD` row `0..31`. For the textured mapper, the
texture frame's width `w`, height `h`, and pixel plane, plus the default UV
corners `(0,0) (w-1,0) (w-1,h-1) (0,h-1)` in vertex-index order. Every
attribute is promoted to signed 16.16 by `<< 16` when the walk starts.

**1. Extrema.** One pass over the corners records `minY` and the index `t` of
the **first** corner attaining it (strict `<`), `maxY` and the index `m` of
the first corner attaining it (strict `>`), `minX` and `maxX`. Sentinels are
`±999999`, so a corner list is never empty in practice.

**2. Reject and clip.** With `[L, T, R, B]` the bounds — the composition
variants use `L = 0`, `T = 0`, `R = w-1`, `B = h-1` of the target image; the
framebuffer variants read the surface clip rectangle, whose right and bottom
are the inclusive last column and row ([R-FX-01 §6], [03 §4]) — the polygon
is dropped when `maxX < L`, `minX > R`, `maxY < T` or `minY > B`. The two
**flat composition** fillers are the one exception: both the shaded and the
unshaded one compute `maxX` and never compare it, so they reject only on
`minX > R`, `maxY < T` and `minY > B`. Both textured composition mappers and
both framebuffer variants test all four. The omission is not observable: an
entirely off-left polygon reaches the row loop, but a span is offered only
where `right > left`, and the span writer clamps a negative left endpoint to
`0` while every right endpoint is still negative, so no pixel is written.
Otherwise `yStart = max(minY, T)` and `yEnd = min(maxY, B)`, and when
`yStart == yEnd`
nothing is drawn. Because `yEnd` is clamped to `B` and the fill loop below is
exclusive of `yEnd`, **the row `B` itself is never written by a polygon** —
the clip rectangle's inclusive bottom row and the image's last row are dead
to the filler. The same holds for the last column (step 5). Inside a
composition image this is harmless: the box of [R-REN-03A §1] carries a
two-pixel margin. On the framebuffer it means a live piece can never touch
the viewport's last column or row.

**3. The left chain.** Starting at `t`, step to index `t-1` (wrapping to
`n-1`) and keep stepping until the *next* index equals `m`. For each edge
`cur → next` on this chain the edge contributes only when `y[next] > y[cur]`
— horizontal edges and edges that go up are skipped outright. For a
contributing edge:

```
dy     = y[next] - y[cur]                        (> 0)
xStep  = ((x[next] - x[cur]) << 16) / dy         signed, 64-bit dividend, truncates toward zero
x      = (x[cur] << 16) + 0xFFFF                 the only biased quantity
aStep  = ((a[next] - a[cur]) << 16) / dy         for each attribute a in {u, v, key, row}
a      = a[cur] << 16                            no bias
if y[cur] < T:  x += xStep * (T - y[cur]);  a += aStep * (T - y[cur]);  y[cur] = T
yStop  = min(y[next], B)
for r in [y[cur], yStop):                        top inclusive, bottom exclusive
    left[r]  = x >> 16                           arithmetic shift
    leftA[r] = a                                 still 16.16
    x += xStep; a += aStep
```

The framebuffer variants add a cheap pre-test `y[next] > T` before the
`y[next] > y[cur]` test; it rejects nothing the row clip would not have
rejected.

**4. The right chain** is the same walk from `t` stepping to `t+1` (wrapping
to `0`) until the next index equals `m`, writing `right[r]` and `rightA[r]`.
Both chains index the edge table from row `yStart`; an edge clipped at `T`
lands on the same table row as an unclipped one would have.

**Edge table rows are always written (Established).**
The two tables are uninitialised scratch — stack arrays the filler only
probes for — and there is no per-face clear, yet no row the fill reads is ever
stale. Each chain is an index path that starts at the corner holding `minY`
and ends at the corner holding `maxY`, so for every row `r` in
`[minY, maxY)` the path has a last corner with `y ≤ r` whose successor has
`y > r`: a descending edge, and it writes row `r`. A chain that folds back on
itself writes some rows more than once, and such a row keeps the **later**
edge's values. Clipping does not change this: the fill's `[yStart, yEnd)` lies
inside `[minY, maxY)`, and an edge clipped at `T` still writes every row of its
own range at or below `T`. An implementation needs neither a "written" mark nor
a clear; a row it would consider unwritten cannot occur.

**5. The fill.** For `r` in `[yStart, yEnd)`, with `xl = left[r]` and
`xr = right[r]`:

- the flat framebuffer filler clamps `xl = max(xl, L)`, `xr = min(xr, R)` and
  writes `xr - xl` bytes of the colour starting at `xl` when that count is
  `> 0`;
- every other variant calls its span writer only when `xr > xl`; the writer
  first computes each per-pixel attribute step as
  `da = (rightA[r] - leftA[r]) / (xr - xl)` — signed 16.16 divided by the
  **unclamped** width, truncating toward zero — then clamps
  `xl < L → a += da * (L - xl), xl = L` and `xr > R → xr = R`, and writes the
  pixels `[xl, xr)` (nothing when the clamped width is not `> 0`).

So a pixel column `c` on row `r` is covered exactly when
`ceil(xL(r)) <= c < ceil(xR(r))`, where `xL(r)`/`xR(r)` are the chains'
16.16 edge positions sampled at the integer row `r` — the `+0xFFFF` bias
followed by the arithmetic shift is a ceiling for every non-integer edge
position and the identity for an integer one. Rows are `[yTop, yBottom)`.
Left and top are inclusive; right and bottom are exclusive; two faces
sharing an edge neither overlap nor leave a gap along it; there is no
half-pixel centre convention anywhere.

**6. Per pixel.** The textured writers sample `texel = pixels[(v >> 16) * w
+ (u >> 16)]` (the widths 8, 16, 32, 64 and 128 use a shift-and-mask form of
the same product) with **no clamp and no wrap** — the default corners keep
`u` in `[0, w-1]` and `v` in `[0, h-1]` because the interpolation never
reaches the right corner value (the last pixel of a span is at
`leftA + (width-1) * da`). The four writers then store what
[R-REN-03A §5]'s table says, under the key test `storedKey <= (key >> 16) &
0xFF` — an unsigned byte comparison, the incoming key narrowed to a byte —
writing colour and key together when it passes, or unconditionally when the
target has no key plane. Colour and key are the only outputs; the row is
consumed as `SHD[(row >> 16) * 256 + texel]`.

**7. The winding cull.** There is no normal test, no signed-area test and no
"backface" flag. What removes back faces is step 5: the chain that walks
*decreasing* indices from the top corner is always treated as the left edge
and the *increasing* chain as the right edge, so a convex face whose corners,
read in index order on the screen with Y increasing downward, run
**counter-clockwise** produces `xr <= xl` on every row and paints nothing,
while a **clockwise** face paints. A face that folds (concave, or a quad whose
projection is a bow-tie) paints only the rows where its right chain is still
to the right of its left chain. Reproduce the mechanism, not a derived rule:
apply the same two-chain walk to the same vertex order and the same faces
vanish. A corollary that the asset census in [R-REN-03A §5] makes reachable:
an authored flat primitive with **two** vertices (there are stock ones)
draws nothing — both chains consist of the same single edge, so
`xr == xl` on every row.

**8. What the framebuffer variants add.** They take the current surface clip
rectangle, or install one for the duration of the call when none is active,
and otherwise behave identically. The projected corners are computed by
their caller (the live-piece path, §2) rather than being origin-relative.

**Bounded-negative.** No filler reads a texel's transparency, no filler reads
`LHT` or `ALP`, and none of the eight functions in the two families draws a
random number from either RNG stream. Presentation of a model is
RNG-silent end to end (the composer, the per-unit present, both piece
renderers, the staging and child composite, and every filler call no RNG
helper).

#### The vertex pipeline: floor, not truncation; and the live-piece path sums before it floors [R-RAST-01 §2]

The projected components of [R-REN-03A §1] and [R-REN-03A §6] are the
**high 16-bit word of the 16.16 value**, i.e. `floor`, not a truncation; for
a negative fractional coordinate floor and trunc differ by one. The
rotate helper of [03 §2.4] rounds to nearest before storing ([R-DET-01 §2]
owns that rounding), so a rotated vertex generally carries a fractional
part, and the distinction is live. Established (direct-static), the
composition path per vertex, in the order the renderer evaluates it:

```
vx, vy, vz : the piece-chain output of [03 §2.4], signed 16.16, model-relative
X  = hi16(vx)                                 floor(vx / 65536), then widened from int16
Y  = hi16(vy)
Zn = hi16(-vz)                                floor(-vz / 65536)  ==  -ceil(vz / 65536)
if supersampled ([R-REN-03A §6]):  X <<= 1;  Y <<= 1;  Zn <<= 1
sx  = X + origin.x
sy  = (Zn - (Y >> 1)) + origin.y              Y >> 1 is an arithmetic shift (floor)
key = 50 [+ 75 if Digger] + (supersampled ? Y / 2 : Y)    Y / 2 truncates, and Y is even, so this is the 1x Y
```

The negation happens **before** the floor, which is why the shear term is
`-ceil(z)` and not `-floor(z)`: a vertex at `z = 0.25` lands one pixel higher
on screen than one at `z = 0`. The unit's world position enters only at the
final blit, as `hi16(unitX - camX·65536) + 128` and
`hi16(unitZ - camZ·65536) - (hi16(unitY) >> 1) + 32` ([R-REN-03A §9]).

**The live-piece fallback projects differently.** The path that rasterizes
pieces straight into the framebuffer — taken when a unit has no cached image
(the no-key-plane branch of [R-REN-03A §4], and every projectile or debris
model) — adds the world offset **in 16.16 before flooring**:

```
sx = hi16(vx + (unitX - camX·65536)) + 128
sy = hi16((unitZ - camZ·65536) - vz) - (hi16(vy + unitY) >> 1) + 32
```

Since `floor(a) + floor(b)` is `floor(a + b)` or one less, a live piece can
sit one pixel left of, or one pixel below/above, where the same vertex would
have landed in the cached body. Reproduce both expressions as written; do
not share one projection helper between the two paths.

**Rounding at each remaining step.** Edge steps and attribute steps truncate
toward zero (§1 step 3); edge positions round up (the `+0xFFFF` bias);
attributes round down at the pixel (`>> 16`). The `SHD` row itself is
`trunc(dot · 5.0) & 0x1F` per [03 §2.4.1] and is carried as an integer into
the same 16.16 interpolation as the key.

#### Team colour: the owner's colour index selects the `LOGOS` frame, and the frame's own size feeds the default corners [R-RAST-01 §3]

The team bits are read only on the **textured** branch (authored `IsColored`
bit 0 clear, exactly four vertices), and the byte they select with is not a
shade — it is the owner's **colour index**, used as a frame number. A flat
primitive goes to the flat filler with its authored colour byte and never
consults the team bits ([R-REN-03A §5]).

**Established (direct-static).** For a textured quad whose loader-written
resolve-at-draw-time bit is set: when the team bit is also set the frame is
`frame[colour]` of the primitive's resolved entry, where `colour` is the
owning player's colour index byte; the frame lookup returns **no frame** when
`colour` is outside `[0, frameCount)`, and the mapper draws nothing for a null
frame. So a unit whose owner has the unassigned colour (`0xFF`, written by the
player-slot reset paths) shows holes where its team quads would be.

**Where the colour index comes from.** It is a per-player byte `0..9`. The
skirmish and lobby paths assign it from the `Color%d` gadget / the
`Player%dColor` registry mirror, with the collision re-step described in
[08 R-SKIR-01 §1] (the assignment helper walks candidates `0..9`,
wrapping past `9` to `0`, until it finds one no other live slot holds); the
campaign start writes colours `(0, 1)` for the local and enemy players
[08 R-CAMP-01 §3]; a lobby packet carries a remote slot's byte. Doc 08
owns those writers; the renderer only reads the byte.

**Which primitives are team-coloured.** The loader sets the team bit on
a primitive only when its texture name misses the primary banks and resolves
to an entry with **exactly ten frames in the fallback LOGOS bank**
([03 §2.4.1] item 4, [R-CRD-005 §1]); the artist authors nothing but the name.
A ten-frame primary-bank match remains ordinary and registered. **Established (asset census, stock `textures/logos.gaf`):** the
file holds 18 entries; 17 have ten frames — `colorslt`,
`colorsmd`, `colorsdk`, `colordk2`, `Solid1a`, `Solid2a`, `Solid3a`,
`Solid3b`, `Solgradb`, `32xlogos`, `32XGouraud`, `Arm32Lt`, `Arm32Dk`,
`Core32Lt`, `Core32Dk` and two more of the same shape — and `onoff01` has
two frames and is therefore an ordinary animated texture, not a team one.

**Per-player dimension deltas.** There are none to apply. The textured mapper reads only the selected frame's width,
height and pixel plane; it never reads the frame's `xOff`/`yOff`, and it
builds the default corners `(0,0) (w-1,0) (w-1,h-1) (0,h-1)` from **that
frame's** `w` and `h`. Frames of one entry are not all the same size —
**Established (asset census):** `32XGouraud`, `Arm32Lt`, `Arm32Dk`,
`Core32Lt` and `Core32Dk` mix `32×32` and `32×33` frames across the ten
colours — so a team quad on a player with a `32×33` frame samples 33 texel
rows over the same screen area that another player's `32×32` frame covers in
32. That is the whole per-player difference: the corner rule is unchanged;
`h-1` is one larger. The frame offsets, which vary wildly across colours in
the stock file, are inert in the model path.

**Established (direct-static): feature team colour.** The shared feature
pseudo-unit is cleared at initialization, leaving its owner slot at zero.
Initialization then supplies its fixed rendering flags and definition; the
per-feature dispatcher replaces only its model, position and orientation.
Neither that reuse nor the ordinary model presenter changes its owner slot.
Both shaded and unshaded composition paths pass that retained slot to the
ordinary player-colour lookup. A feature `LOGOS` quad therefore selects
**player slot zero's current colour**, irrespective of the feature's placer,
the dying unit's owner, or the viewer. This is not a constant frame-zero
selector: if player zero's colour is seven, the feature uses frame seven.
The selected frame's dimensions and out-of-range omission follow the ordinary
lookup above.

**Established (bounded stock asset census).** The reference install's compiled
feature catalog contains 432 model-backed definitions naming 285 distinct
models. After primary-bank precedence and the textured-quad dispatch rules,
only `corkrog_dead` uses fallback ten-frame `LOGOS` art: three textured quads.
This census includes every compiled feature definition, rather than only
currently placed map features or one battle's wrecks. It does not claim that
other content sets cannot author such faces.

#### The option defaults, the waterline bit, the shadow gate and the punch-out offset [R-RAST-01 §4]

**`Anti_Alias` shipped default.** The settings reader looks the value up under the registry key `Anti-Alias` (the
value name carries a hyphen; the option's in-engine name is the underscore
form) alongside `Shadows`, `VehicleShadows` and `FeatureShadows`. On a miss
it **sets** the bit — anti-aliasing is on by default — and writes the default
back through the registry miss-path helper of [R-TERR-01 §8]. The default
is in the reader, not in the option handlers. **Established (direct-static).** The Options
screen's `RESTORE` gadget likewise sets bits 1..5 (anti-alias, master
shadows, vehicle shadows, feature shadows, shading) together.

**The Options screen gadgets.** The visual-options screen (`VISUALS.GUI`)
carries three named toggles whose handler writes the option word directly:
`ANTI` → bit 1; `SHADING` → bit 5; **`BSHADOWS`** → bit 4, after which the
handler copies bit 4 into bit 3 and then bit 3 into bit 2, so the one gadget
sets feature, vehicle and master shadows to the same value. Every one of the
three then rebuilds the shadow caches and repaints when a battle is live.
`BSHADOWS` is that gadget's name, not a registry value and not an authored
key; the registry mirrors are the four names above.

**The submerged erase-versus-tint bit.** The status bit the waterline pass
of [R-REN-03A §8] tests is the **sonar-contact bit** of the sensor phase ([R-VIS-01 §4], [R-VIS-01 §5]): the same bit the
direct-visibility predicate consults to accept a fully submerged unit. The
waterline pass therefore reads: a submerged enemy the viewer has **no sonar
contact on** is cut off at the surface; a unit the viewer owns, or has on
sonar, is tinted through the `BLUE TABLE` instead. The engine's feature-backed
pseudo-unit sets the bit permanently at construction, so a 3DO feature — a
wreck — is **always tinted, never cut** (§6 below).

**The shadow gate.** The unit present has **three** shadow branches, selected after the master-shadows bit and the definition's
`noshadow` have passed:

1. **Digger** (definition bit set): silhouette copy of the finished body
   image ([R-REN-03A §8]), then the silhouette is **erased wherever
   `key <= 50 + 75`** — the buried half casts no shadow — and blitted through
   the tinted blitter. Requires the vehicle-shadow bit and none of
   `canhover`/`floater`.
2. **Mobile** (structure-class bit clear, [R-RND-02A]): silhouette copy;
   when the unit is below the surface (`t = seaLevel - hi16(unitY) > 0`) the
   silhouette is erased wherever `key <= t + 50` before the blit, so the
   shadow of a partly submerged hull is the shadow of the part above water.
   Same option and definition gate as branch 1.
3. **Structure** (structure-class bit set, not a Digger): the re-rasterized,
   punched, RLE-cached shadow of [R-REN-03D §2]–§5. This branch tests **only
   the master bit and `noshadow`** — not the vehicle-shadow bit, not
   `canhover`, not `floater` — and one more condition: it is skipped when the
   subject's definition ordinal is `0` **and** `hi16(unitY) < seaLevel`.
   The unit catalog's ordinal `0` is the reserved slot the loader's sort
   walk starts past (Supported inference from the loader: the walk begins at
   ordinal 1; nothing else was found using ordinal 0 as a live type), and
   the feature pseudo-unit records ordinal `0`; so for every real unit the
   test is vacuous and for a 3DO feature it means **a wreck at or below sea
   level casts no shadow**.

So toggling `VehicleShadows` off removes the shadows of mobile units and
diggers and leaves structure shadows in place; the master `Shadows` bit
removes all three. Ships are mobile and take branch 2; the cached image
belongs to structures, and the sea-level gate only ever bites on wrecks.
Everything in
this paragraph is **Established (direct-static)** except the ordinal-0
inference, which the decider names.

**The punch-out offset.** The punch-out composites the body image into the shadow image with an X shift of
`+5` applied to the *body* column and a row mapping through the two images'
recorded origins; in model space that places the body silhouette **five
pixels to the left** of where the shadow geometry sits. The blit of
[R-REN-03D §3] then places the shadow image five pixels to the **right** of
the body. The two cancel exactly: the hole lands at the body's own screen
position, the same on X and Y, so the ground directly under the body is not
darkened before the body covers it. The opposite signs are the difference
between shifting a source and shifting a destination. Implement the
punch-out. **Established (direct-static).**

**The no-key-plane present is not a reduced copy of the full one.** When the
cached image has no key plane ([R-REN-03A §2] gate false), the unit present
draws: shadow (the three branches above, but branch 1 and 2 without their
erasures, which need a key), then the body blit, then live pieces straight to
the framebuffer, then every attached child's draw-bit pieces straight to the
framebuffer; there is no staging, no waterline and no digger pass at all.
**Established (direct-static).** A `CORFAV` or `CORTRUCK` — the two stock
`ZBuffer=0` types — is therefore never cut at the waterline or tinted.

#### Lighting in the model path is `SHD` only [R-RAST-01 §5]

**Established (bounded-negative over the model raster path).** The only
palette table a model span writer reads is `SHD`, and only in the shaded
renderer ([03 §2.4.1] item 3, [R-RND-02A]); the row is chosen per vertex from
the smoothed normal and interpolated along the edges and across the span
exactly like the key (§1). No writer reads `LHT` — that table belongs to the
flash blitter ([R-FX-01 §4]) — and `ALP` is read only by the anti-alias
downscale ([R-REN-03A §6]) and by the tinted blitter that shadows and cloaked
bodies go through ([R-REN-03D §4]). There is no per-polygon light selection
beyond the row; the light direction is the one global vector of [03 §2.4.1].

#### Image scratch records, the transform pass order, and what a script write dirties [R-COMP-01 §4]

**Image scratch records (Established, direct-static).** The composition and
staging images of [R-REN-03A §1] and §4 are allocated by two routines over
one 24-byte header: width, height, a zero placement-offset pair, the
transparent-key byte set to **1**, three zero flag bytes (not compressed, no
sub-frames, no alternate blitter), a zero word, the pixel-plane pointer (the
byte after the header) and the key-plane pointer. The colour plane is
prefilled with `0x01`; the key-plane variant allocates `w × h × 2 + 24` bytes,
places the key plane immediately after the colour plane and zero-fills it.
Because the header is the same shape the GAF blitters read ([R-COMP-01 §2]),
a finished composition image is blitted by the ordinary keyed frame blitter
with key 1 — which is why palette index 1 is the fringe colour of
[R-REN-03A §7].

**Transform pass order (Established).** [03 §2.4] "Piece transform
composition" is implemented as two recursive passes over the piece tree, run
per rebuild: (1) a *reset* pass that, for every piece whose rebuild-gate word
is zero (it always is — the vestigial counter of §2.4) or whose parent was
reset, copies the pristine vertex array over the live one and zeroes the
piece's live translation triple; (2) a *transform* pass that recurses into
the child chain **first**, then rotates the piece's own translation triple
and every live vertex by the piece's angle triple, then adds the accumulated
parent translation, and finally applies the same rotation and translation to
the whole child chain again — which is how ancestors are applied after
descendants without a matrix stack. The per-piece translation is the sum of
the piece's script translation lanes and the model's authored parent
translation, as §2.4 states. The **root** piece's angle triple additionally
receives the unit's three orientation words, added component-wise in
**reverse storage order** (the unit's first orientation word adds to the
piece's third angle word, the second to the second, the third to the first);
which of the three is heading, pitch or bank is the shared rotation helper's
naming and is **Unknown** (the tail's pitch/bank naming item).

**Face-normal helpers (Established).** The shaded renderer's per-vertex
`SHD` row ([R-RAST-01 §5]) starts from a face normal built by three float
helpers: vector difference (one integer-input and one float-input variant),
the standard cross product `a × b = (a.y × b.z − a.z × b.y, a.z × b.x − a.x ×
b.z, a.x × b.y − a.y × b.x)` of its two argument triples in argument order, and
normalisation by `sqrt(x² + y² + z²)` (single-precision throughout; a
zero-length normal divides by zero — the same edge as the beam projection's
degenerate case, and a fault is the retail outcome).

**What a script write dirties (Established).** Translation and angle setters
write only on a changed value. They invalidate the piece's transform stamp,
request a model transform rebuild, and invalidate the cached image when the
changed piece has its cache bit set. A changed draw bit also invalidates the
piece stamp and conditionally invalidates the cached image, but does not
request the transform rebuild. Cache and shade setters write their flags
unconditionally and discard the cached-image reference on every call
[04 R-MOV-03 §4].

The cached-body draw entry directly reads the validity state cleared by those
conditional writes: an invalid state requests a cached-body rebuild. This
settles the former reader inference. Rebuilding also discards the cached
shadow. The image builder installs the resulting composition image in the
reference discarded by the cache/shade setters; that reference is an image,
not a separate generation counter. Missing-image and construction/key-plane
requirements retain the additional rebuild gates of [R-REN-03A §4]. The draw
entry advances its validity state after either the direct-piece fallback or
cached-image path. This finding is bounded to the named script setters,
cached-body draw gate and image-builder/reference consumers; it does not
replace the orientation-cache contract with a guessed dirty heuristic.

#### Render piece state: allocation, the point-list copies, release, and the composition-cache purge [R-COMP-02 §3]

**Established** (direct-static: the allocator, the recursive fill,
the piece counter, the release and the cache purge). The flag polarity is
[04 §4.3 "Piece flag polarity"] and is not restated; the transform passes over
these records are [R-COMP-01 §4].

**Allocation.** Binding a model to a unit (and to a feature that carries a
3DO) allocates one tagged block `Object State` of `34 + 54 × n` bytes,
zero-filled, where `n` is the piece count of the model tree — counted
recursively as `1 + count(child) + count(sibling)` from the root. The 34-byte
header holds the piece count, a word that the allocator sets to 1, a back
pointer the unit binder fills with the unit record, two cache-key words (the
composition-image cache of [R-REN-03A §4] keys its entries on the address of
each), and the pointer to the root piece state. Piece states follow, 54 bytes
each, filled by a pre-order walk (self, then the child chain, then the
sibling chain) so the root is state 0 and a piece's children immediately
follow it. Each piece state holds: the model piece pointer; a live vertex
array — a tagged `Point List` block of `12 × vertexCount` bytes, filled with a
copy of the model piece's vertices (three 32-bit words each); the flags byte
(bit 0 set iff `vertexCount ≥ 3`, bits 1 and 2 set); a zero word; and the
child, sibling and parent state pointers (null when absent). A piece with no
vertices still gets a zero-length `Point List` allocation.

**Release.** Freeing a state frees every piece's `Point List` in state order,
then — if the composition-image cache exists — **purges** every cache entry
whose key equals either of the header's two cache-key addresses, and frees
the block. The purge walks the cache's variable-length records (each carries
its own byte length) from the front to the recorded total and nulls the key
of every match; it does not reclaim the record. A clone that reuses cache
entries across a unit's death without this purge shows the dead unit's body
on the next unit to occupy the same record address.

#### The animated-texture cursor registry and the phase-7 advance [R-COMP-02 §4]

**Established** (direct-static: the model loader's texture bind, the
registry, the phase-7 walker; the cursor step itself is [06 R-WFX-01 §1] /
[02 "Animation playback"]).

When the model loader resolves a textured primitive's name against the
texture GAF banks, it examines the entry's frame count. An entry with fewer
than two frames binds its single frame and clears the primitive's animated
bit. An entry with two or more frames binds a **playback cursor** inside the
primitive record (start frame 0), sets the animated bit, and — unless the
primitive's *team-colour* bit is set — appends the cursor's address to a
global registry (grown by one slot per append; the count starts at zero at
battle entry and is reset at texture-system teardown). These cursors belong
to the loaded model primitive, shared by runtime instances referencing that
model [R-CRD-005 §1]. Team-colour
textures are therefore never animated; every other multi-frame texture is.
The loader searches the primary texture banks first, then the fallback LOGOS
bank only after those miss. Only a ten-frame entry resolved from that fallback
sets the team-colour bit; a ten-frame primary-bank entry remains an ordinary
registered animation.

Phase 7 of the sub-tick ([01 §4.4]) walks the registry **from the last
registered cursor to the first** and steps each once with the playback-cursor
advance of [06 R-WFX-01 §1] (countdown, then frame; wrap or detach by the
entry's loop byte). Order matters only for determinism of the step count,
which is one per cursor per sub-tick; no random draw is made. The step is the
same one the effect strips use, which is why [01 §4.4] groups them.

### 2.5 Orthographic screen projection

The established beam/line projection is orthographic and integer based:

```
screenX = (worldX >> 16) - cameraX + 128
screenY = (worldZ >> 16) - ((worldY >> 16) >> 1) - cameraZ + 32
```

Equivalent paths use the same world-to-pixel scale and a half-height shear. The transient negation of Z before high-word extraction in bounds/raster helpers is the shear term above, not a persistent vertex store — a bounded-negative census finds no second stored negation outside the load-time half-turn (direct-static).
There is no perspective divide, depth buffer, or distance-based line width in
the observed beam renderer. Camera scroll and edge scrolling are integer state;
camera speed is separately configurable.

## 3. Visibility, LOS, radar, and fog presentation

### 3.1 Visibility storage

Retail separates terrain, mode-dependent mapping and sight state, feature
memory, and radar:

1. Terrain tiles are always available to the tile pass.
2. A mode-dependent word grid stores one player bit per 32-pixel coverage or
   mapping tile. Its allocation is exactly *cellWidth* times *cellHeight*
   divided by two bytes, indexed as little-endian 16-bit cells on a grid half
   the cell dimensions in each axis — one cell per 32 world pixels — with ten
   usable player bits. Exactly one routine sets bits in it, and both of the
   per-owner byte-grid routines are separate from it. Its universal meaning is
   not established: its initialization and its consumers are mode-dependent, so
   calling it current line of sight, explored memory, radar, or occupancy would
   overstate the evidence. A separate per-player byte grid stores overlapping
   current-sight coverage as a reference count, incremented and decremented by
   two dedicated routines.
3. Plot cells are primarily terrain and feature-occupancy records. Their flag
   byte carries an instance-present bit, a placement blocker, a
   never-explored/fogged marker, and a placer nibble stamped at feature
   placement time that presentation reads back as an owner-memory accept
   (section 3.3); it is distinct from the mapping grid and is not a general
   current-LOS store.
4. Radar/sonar uses minimap surfaces and blip lists, not the gameplay LOS bitset.

A mode word governs which raster and which predicate are active. Bit 0
selects history versus always-visible mapping, bit 1 selects the byte-grid
predicate versus the word-mask predicate, bit 2 selects sprite-mask versus
terrain-ray raster (the exact polarity of all three, and the authored option
each one comes from, is `[R-VIS-01 §1]` below), and bit 3 marks the
fog-cache-valid state — the
composer's two-byte fog/minimap overlay cache (section 3.3) is rebuilt when it
is clear and the bit is set again after the rebuild. It is cleared at map load,
on camera moves, and by LOS publication (any local coverage change), so a
visibility change or camera pan triggers one fog-cache rebuild — it does not
gate the LOS terrain-height word, which is built once at map load and never
invalidated (section 3.5). The
byte-grid path treats any nonzero `uint8` count as visible; its increment is a
plain wrapping `uint8` addition with no clamp (256 overlapping observers wraps to
zero and reads as fogged) and its decrement is the matching plain subtract. The
word path stores one `uint16` per visibility tile with ten usable player bits;
its update is an idempotent bit OR — a cell receives its owner's bit only when
absent, never decremented, rebuilt by reset instead. The global word map is
reset and rebuilt when the visibility mode or map state requires it. This
establishes that the word grid gates a form of visibility or mapping without
proving one universal semantic name. The byte grid is rebuilt by walking
current sight sources. The grid's consumer census ([R-LAYER §1] below):
every non-presentation reader tests it as a per-player gate on path requests,
placement validation, and order destination validation, and the path-search
consumption of document 04 §6.1 ([R-DOC04-B]) reads this grid. What remains
unnamed is the mode-word initialization semantics, not the gameplay role.

#### R-LAYER §1 — the mapping word grid is the path search's "owner/building-mask": complete write-site census

**Established (direct-static, bounded writer census).** The mode-dependent
word grid of section 3.1 item 2 — allocated at map load as the mapped-memory
array of `cellWidth × cellHeight / 2` bytes, one 16-bit word per 2×2-cell
(32-pixel) tile, bits 0–9 per player slot — is the **same array** that
document 04 §6.1's path consumption ([R-DOC04-B]) calls the "owner/building-
mask word": identical allocation and geometry, and the path passability test
reads the requesting player's own bit `1 << playerSlot` from exactly this
grid, returning its bit-miss value 2 when the tile is not mapped for the
requesting player. The name "owner/building-mask" is a misnomer: **no
building bit exists in the grid and no building state is ever written to
it.** The tested bit is the requesting player's mapping bit; hard building
blocking happens at the movement commit validator, as [R-DOC04-B] itself
states.

**Writer census.** Five write sites exist:

1. **Map load.** The terrain loader allocates the array and zero-fills it
   (before any battle state exists).
2. **The bulk wipe-and-rebuild.** At battle entry (after mode setup) and —
   inside phase 5 — only in the commander-spawn and commander-defeat branches
   ([01 §4.4]), never per tick. **Its word-grid fill is conditional**: the
   routine takes a history-reset argument and skips the whole-grid fill when
   that argument is zero (see [R-VIS-01 §1]); the per-player byte-grid refill
   below it runs on every invocation. When it does run it
   fills the whole grid from mode-word bit 0
   (all-zero when the history/mapping mode is enabled — nothing explored yet —
   all-ones when disabled, the always-visible mode), refills every active
   player's per-player byte grid from mode-word bit 1 (one everywhere when the
   byte-grid predicate is disabled, zero when enabled), re-stamps every
   eligible unit's coverage through the per-unit stamp when the byte-grid
   predicate is active, and clears the fog-cache-valid bit.
3. **The phase-5 per-player LOS stamp sweep — the only runtime bit writer.**
   Per player 0..9 ascending, after that player's order dispatch and
   per-player work and before the 30-tick cadence block ([01 §4.4]), the sweep
   walks that player's unit slice over in-game (alive) units. Per unit it is
   dirty-checked: a unit whose stored stamp cell and sight range are unchanged
   writes nothing; otherwise the change path first unpublishes the old
   byte-grid coverage, then re-stamps the new coverage, and the stamp ORs the
   unit's **owner player's** bit into each covered tile's word. The coverage
   raster is one routine carrying **three** separate 16-bit store sites, not
   one. The word grid
   is never decremented — bits persist until a bulk rebuild (mapping memory,
   not current sight; current sight lives in the per-player byte grids).
4. **The two-slot mapping-share routine.** It walks the whole grid tile by
   tile and, for every tile already carrying a **source** player slot's bit,
   ORs a **destination** player slot's bit in — an idempotent copy of one
   player's explored memory into another's, with no sight, unit or feature
   input. It has exactly two static callers and no data reference: the
   in-battle `MAPINFO` console command (which passes the local player's slot
   and the command's argument slot) and one arm of the in-battle
   message/command dispatcher. Neither is on an ordinary single-player path,
   and Nanolathe implements no mapping-share path.
5. **The saved-game restore — Established.** The battle-restore chain's
   mapping step selects the save's `Mapping` section and its `Data` box,
   computes the expected byte count as attribute width × attribute height ÷ 2
   — half a byte per attribute cell, the allocation geometry above — and
   copies the box straight into the grid through the generic chunk reader,
   but only when the stored length equals that count **exactly**; a missing
   section, a missing box, or any other length leaves the grid untouched. Its
   mirror is the saver, which emits the same section, box and byte count out
   of the same array. Each has exactly one static caller and no data
   reference, both on the single-player save/restore path, so reloading a
   saved battle restores explored memory rather than rebuilding it. This
   writer is invisible to a scan that looks for a store through the loaded
   pointer, because the store lives inside the generic chunk copy the pointer
   is *passed* to.

**What does not write it.** Unit creation, death/wreck conversion,
construction completion, and feature spawn/remove/reclaim have **no write
site**: a newly created unit stamps at its first sweep that reaches it (a
building completed during the unit phase shows up in the next tick's phase-5
sweep); a dead unit merely stops being swept and its already-mapped bits
persist, which is the explored-memory behavior the idempotent-OR rule
produces; features never touch the grid at all. The defensible closure is
therefore **no unit, feature, construction or occupancy path writes the
grid** — which is what the paragraph above needs — rather than "no other
function writes it at all": the raster that publishes bits is reached only
from the per-unit stamp and the bulk rebuild, and the only further writers are
the command-driven mapping share of site 4 and the saved-game restore of
site 5, neither of which derives anything from unit, feature or occupancy
state.

**The register-relative route, enumerated — Established.** The displacement
scan behind the census is exhaustive for accesses that name the grid
pointer's own displacement, but a routine holding the map record's address in
a register reaches the same field through a register-relative offset, which is
how the loader's zero fill (site 1) is itself spelled. That form is now
enumerated in full: the map record's address is taken into a register at four
places image-wide and none of them passes it to a callee, so the pointer never
escapes those routines; and across the whole image the only register-indexed
word-array dereference of the grid pointer is a **read**, inside the
class-layer stamp. The register-relative route therefore contributes exactly
site 1 and that one reader, and no further writer. The same enumeration closes
[R-TERR-01 §7]'s read side.

**Same-tick ordering.** The path scheduler runs first in phase 5, before any
player's stamp sweep, so a tick's path requests consume the previous tick's
mapping state; placement and order validation later in the same phase see the
same-tick stamps of players already processed (ascending player order). No
visibility pass exists in phase 12, the cadence flip, or the post-loop tail —
the phase-5 sweep is the final publisher ([01 §4.4]).

**Consumers (all direct readers, none write).** The A\* passability test and
greedy-ray probes ([04 §6.1 R-DOC04-B]); the build-site and placement
validators; the order destination validators; and the mode-selected
targeting/visibility predicates of section 3.1. This consumer set is what
pins the grid's gameplay role as the per-player explored/mapping gate while
the semantic naming hedge of section 3.1 item 2 stands.

**The occupancy commit has no write site either.** The occupancy commit,
the footprint stamp and clear, unit creation and building completion do not
reference the grid; the five writers above are the complete set, and the
runtime bit writer is the phase-5 LOS sweep alone. The grid the path search,
the landing-legality accept (`[04 R-AIR-01 §6a]`) and the placement
validators read is this one — a movement-side "owner mask" is an alias of
the visibility publisher's array, never a separately maintained copy
(`[04 R-PATH-01 §14]`). Established.

#### R-VIS-01 §1 — the visibility mode word: authored option provenance and exact polarity

**Established** (direct static trace of the battle-entry initializer,
the registry loader, the skirmish setup screen, the in-battle options overlay
and the bulk rebuild's fill constants).

The visibility predicate's mode-selected source (§3.2) and the raster
selection (§3.2) are exactly the two branches the user-facing options drive;
the provenance chain is below.

**Three authored options, one word.** Three session options — *Mapping*,
*Line of Sight* and *LOS Type* — are written into the low three bits of the
visibility mode word once at battle entry and never touched again by the
simulation. Retail persists them under `Software\Cavedog Entertainment` (doc
02 §3) as three parallel triples keyed by session kind:
`SingleMapping` / `SingleLineOfSight` / `SingleLOSType`,
`SkirmishMapping` / `SkirmishLineOfSight` / `SkirmishLOSType`, and
`MultiMapping` / `MultiLineOfSight` / `MultiLOSType`. **Each defaults to 1**
when the registry value is absent, and the loader then **stores** that default
back into the registry, so the key exists from the first run on
(`[R-TERR-01 §8]`).

**Battle entry.** The session kind selects the source, and each assignment is
a plain one-bit copy — no inversion anywhere:

| Session kind | mode bit 0 (Mapping) | mode bit 1 (LineOfSight) | mode bit 2 (LOSType) |
|---|---|---|---|
| campaign/mission | single-player Mapping global `& 1` | single-player LineOfSight global `& 1` | single-player LOSType global `& 1` |
| skirmish | setup record's Mapping field `& 1` | its LineOfSight field `& 1` | its LOSType field `& 1` |
| multiplayer | session rule word bit 8 | rule word bit 9 | rule word bit 10 |

The multiplayer session rule word is a 16-bit field in the local player's
option record; the same word carries the commander-death rule in bits 11–12
(copied to the commander-death global at battle entry), the cheat flag in bit
13, fixed start locations in bit 14 and the game-closed flag in bit 15
`[08 "Skirmish configuration"]`. The skirmish setup record is doc 08's object;
only its three visibility fields are named here.

**Polarity, pinned by the bulk rebuild's fill constants.** The wipe-and-
rebuild (`[R-LAYER §1]` write site 2) fills the mapping word grid with the byte
`((-((mode & 1) != 0)) & 1) - 1` and every eligible player's current-sight byte
grid with `(~(mode >> 1)) & 1`. The **byte-grid refill is unconditional**; the
**word-grid fill is not** — it is skipped entirely unless the refresh's
history-reset argument is non-zero, which is why `LOS` and `LOSType` leave
explored history intact while `Mapping` and `NowISee` wipe it (the paragraph
below states which command passes what). Both fills are plain byte fills over
the whole allocation, so they still read the polarity out directly:

| Bit | Value | Grid fill at rebuild | Meaning |
|---|---|---|---|
| 0 | 1 | word grid `0x00` | **Unmapped** — nothing explored; the grid accumulates |
| 0 | 0 | word grid `0xFF` | **Mapped** — every player bit set everywhere at once |
| 1 | 1 | byte grids `0` | current-sight tracking **on**; the predicate samples the byte grid |
| 1 | 0 | byte grids `1` | current-sight tracking **off**; the predicate samples the word grid |
| 2 | 1 | — | terrain-ray raster |
| 2 | 0 | — | sprite-mask (circular) raster |

The eligibility test for a player's byte grid is: the player record is active,
its controller type is one of human / computer / remote, and its slot index is
not the unassigned sentinel 10.

**The three user-facing states, named.** The in-battle options overlay reads
the live mode word back and prints a label, which fixes the naming:

* Mapping Mode: bit 0 clear → `Mapped`; bit 0 set → `Unmapped`.
* Line of Sight: bit 1 clear → `Permanent` (regardless of bit 2); bit 1 set
  and bit 2 set → `True`; bit 1 set and bit 2 clear → `Circular`.

The skirmish setup screen states the same three as sentences: LineOfSight = 0
is `All mapped terrain is visible.` (Permanent); LineOfSight ≠ 0 with LOSType
= 1 is `Terrain elevations affect a unit's view.` (True); LineOfSight ≠ 0
otherwise is `Terrain elevations do not affect a unit's view.` (Circular).
Mapping = 0 is `Terrain is visible.`; Mapping ≠ 0 is `Terrain is blacked out
until explored.` The retail default (all three = 1) is therefore **Unmapped +
True line of sight**.

**Established — live commands alter the same word and immediately rebuild
coverage.** The chat commands `LOSType`, `LOS`, `Mapping`, and `NowISee`
modify live bits 2, 1, 0, and bits 0–1 respectively, then invoke the bulk
visibility refresh ([07 R-CAM-01 §6]). They do not alter the separate setup
record. The refresh always refills every eligible current-coverage byte grid
from the new bit 1. Its per-unit clear and publication pass is gated by current
coverage: when bit 1 is clear it preserves every stored observer field and
does no immediate unit raster. When bit 1 is set, Circular mode re-rasterizes
every defined unit from its current position with the new shape. The terrain-ray
path clears each defined unit's stored coverage byte and calls its ordinary
refresh, so the new raster does not compare a ray emitter height with a
circular shape index. It retains the stored tile pair, however: any ray
observer whose current tile still equals that pair and whose newly computed
emitter byte is in `0..5` can pass that ordinary throttle after the grid wipe
and publish nothing. Apart from this retained-tile case, moved and stationary
ray observers are rasterized from their current state. A removed unit has no
defined record to re-publish. The byte-grid reset, rather than an attempted
retirement through the new raster, disposes of every preceding current-coverage
footprint.

The refresh has a separate history-reset argument. `LOSType` and `LOS` leave
the mapping word grid intact, preserving history while rebuilding current
coverage. `Mapping` and `NowISee` request a word-grid refill using the new bit
0: mapped fills every usable player bit, unmapped clears the grid before the
new stamps accumulate. The refresh then invalidates the fog presentation.

**What each state changes in the simulation.** Nothing branches on a
"LOS mode" enumeration; the three bits are consumed separately:

1. **Permanent** (bit 1 clear). Every byte grid is filled with 1 at rebuild
   and no unit ever publishes *current* coverage: the per-unit publication
   block of the bulk rebuild and the current-coverage half of the per-unit
   refresh are both gated on bit 1. The raster still runs, publishing into the
   word grid whenever bit 0 is set. Every visibility test therefore falls to
   the word grid, which is monotone (§3.2: idempotent OR, never decremented).
   A tile once stamped stays visible forever — that is what "Permanent" means,
   and it is also why a Permanent game still hides never-explored ground.
2. **Circular** (bit 1 set, bit 2 clear). Current coverage is tracked, and the
   raster is the authored `vismasks.gaf` disc — no terrain read at all.
3. **True** (bit 1 set, bit 2 set). Current coverage is tracked and the raster
   is the terrain-ray walk of §3.2 with the height-word horizon test.
4. **Mapping** (bit 0) is orthogonal to all three: it decides the word grid's
   initial fill *and* whether the word-grid publisher runs at all
   (`[R-VIS-01 §2]`). With bit 0 clear the grid starts all-ones and no stamp
   is ever needed.

**Two combinations a clone will get wrong if it folds the bits into one
enum.** Bits 0 and 1 gate the two publishers independently:

* *Mapped + Permanent* (bits 0 and 1 both clear) publishes **nothing at all**:
  the bulk rebuild's per-unit block is skipped, the per-tick refresh skips both
  publishers, and both grids stay at their all-visible fill for the whole
  battle. LOSType is still copied into bit 2 and still selects a raster that is
  never entered.
* *Unmapped + Permanent* (bit 0 set, bit 1 clear) still rasterizes every tick
  — into the word grid only — so the explored region grows and never shrinks.


#### R-TERR-01 §7 — the Mapping array and the rectangle-plus-ring stamp, stated once for doc 03

**Established** (restated from the traces of `[R-LAYER §1]` above
and `[R-PATH-01 §2]` in doc 04, which agree; no new trace).

The mode-dependent word grid of §3.1 item 2 **is** the per-player mapping
memory: allocated at map load as `Width × Height / 2` bytes and zeroed,
serialized under the save section name `Mapping` (`[R-PATH-01 §2]`), filled
at the bulk rebuild with all-ones when the session's *Mapping* option bit is
clear and left at zero when it is set (`[R-VIS-01 §1]`, whose word-grid fill
is itself gated on the refresh's history-reset argument), and OR'd with a
player's slot bit as tiles become seen by the per-unit coverage raster — the
only runtime bit writer in ordinary play, but not the array's only writer:
`[R-LAYER §1]` carries the five-site census, including the command-driven
mapping-share routine that copies one player slot's bits into another's and
the saved-game restore that copies the save's `Mapping` box back into the
array. Its only simulation readers are the per-player gates of `[R-LAYER §1]`
and the
path search's passability probe, which returns its "unexplored" value 2 —
treated as passable by every consumer — when the requesting player's bit is
absent (`[R-PATH-01 §2]`). No building state is written to it.

The class-layer stamp that consumes the plot grid classifies a **rectangle,
not a cell**: the class's authored `FootPrintX × FootPrintZ` rectangle
anchored at the cell is aggregated over the gates of doc 04 §6.1, and when
it comes back clear, four further rectangles — the one-cell ring above,
right, below and left of it — must also be clear or the result is demoted
to *steep*. That is doc 04's statement (`[R-PATH-01 §2]`); doc 03 restates
it here because §2.2's plot-cell census names the classifier as a reader and
an implementer reading only this document would otherwise classify single
cells. Buildings block the search through the occupant-age channel of the
same anchor, not through this grid.

#### R-TERR-01 §8 — the registry miss path stores the default

**Established.** The registry loader that supplies the three
visibility options of `[R-VIS-01 §1]` calls, on every missing key, the same
DWORD store helper it uses for every other key it reads; there is no delete
call in it, and nothing in the loader deletes registry values. Doc 08's
`[R-SKIR-01 §1]` traces the identical helper as a store.

### 3.2 Sight shape and terrain occlusion

Sight distance quantizes differently in the two raster algorithms selected by
mode-word bit 2. In sprite-mask mode the sight radius quantizes to
`trunc(radius / 32) - 5`, clamped into the authored visibility-mask shape
range; the selected shape supplies width, height, anchor offsets, a
transparent palette sentinel, and row-major mask bytes. In terrain-ray mode
the radius quantizes by signed division by 32 **without** the -5 offset and
clamps into the parsed LOS.TDF table range. Both paths write the same word
mask; bit 2 only changes which shape is ORed in. Whether either publisher runs
at all is decided separately: the word mask is written only when mode-word bit
0 is set and the current-sight byte grid only when bit 1 is set
(`[R-VIS-01 §2]`).

**The sight-shape table is an authored GAF resource.** The sprite-mask shapes
are not synthesized: the engine holds a handle to the visibility-mask GAF file
`anims/vismasks.gaf` and indexes the entry named `vismask` by the quantized
value. **Established:** that GAF entry carries ten frames with sides 11, 13,
15, 17, 19, 21, 23, 25, 27, and 29, each anchored at the frame centre. A frame's
opaque (non-transparent) pixels are the covered tiles; its width, height, anchor
offsets and transparent palette index are the shape fields. This GAF binding is
not a first-match search over multiple candidates — the ten-frame handle is the
authoritative shape table. An older file `anims/vismask.gaf` exists in the
archive but is a distinct cursor set and is not the table that the shape fetcher
counts; its frame counts (22 entries, varying opacity) do not match the counted
ten-frame table.

**Quantization is common, then biased differently.** Both rasters start from
the same quotient `q = sightdistance / 32`, computed as a signed divide that
**truncates toward zero** (the sign-bias-then-shift form, not an arithmetic
shift), so it is floor division only for the non-negative ranges stock content
authors and rounds the other way for a negative one. Sprite-mask mode then forms `idx = clamp(q - 5, 0, nsMask-1)` where
`nsMask` is the ten-frame count; terrain-ray mode forms `g = clamp(q, 0,
nsRay-1)` where `nsRay` is the declared LOS.TDF table count. The `-5` bias is
therefore sprite-only.

**The -5 is an index bias, not a radius reduction.** Shape *k* has radius
`k + 5` tiles, so the subtraction that selects the frame is undone by the frame
geometry: a unit whose `sightdistance` quantizes to index *k* covers `k + 5`
tiles, i.e. `trunc(radius / 32)` tiles. Reading the index as the radius shrinks
every unit's sight by five tiles. The clamp is into `0 .. ns-1` where `ns` is the
shape count carried by the resource, and radii below the first shape clamp up to
index 0 rather than publishing nothing.

The terrain-ray group index is the unbiased `q` clamped into `0 .. nsRay-1`
over the parsed LOS.TDF tables, and the spokes walked for that group are that
table's authored line list — line counts grow with the table index (a radius-9
table carries fourteen lines, radius-10 sixteen). LOS.TDF declares
`numtables = 9` but ships twelve table sections; the clamp uses the declared
nine and the three excess tables are unreachable authoring residue (the clamp's
own off-by-one of `[R-COMP-02 §1]` leaves `TABLE9`, the last loaded table,
unreachable too).
Neither raster uses a synthesized circle or a fixed spoke set.

**Terrain height word for the ray.** The LOS reader does not use the per-cell
`hmax`/`hmin` at per-step granularity; it reads a dense `uint16` word per
visibility tile (`TileW × TileH` words, two bytes per tile) that was built once
at map load from the terrain heights and is never rebuilt during a battle
(invalidation by deformation does not exist; only the fog cache is
dirty-tracked). The low byte is tested for admission and the
high byte is tested to advance the retained horizon — identical strict
comparisons, but the low byte decides whether the cell is seen and the high byte
decides whether the horizon rises. The full builder contract is **Established**
(§3.5 [R-P0-18-B]): the low byte is the **maximum** of raw heights over the
scattered neighbourhood and the high byte the **minimum** of
perspective-scaled values, with the pair blended by thirds and floored at sea
level in a final pass. The builder fills the whole word array in one pass at load; the lazy
cache that rebuilds "when the cache-valid mode bit is clear" is the fog/minimap
overlay cache of section 3.3, not this table.

**Spoke geometry.** Each LOS.TDF line is expanded at load time into four
quadrant copies by quarter-turn rotation; the exact transform, the storage
layout and the file grammar are `[R-VIS-01 §3]` — the copies are rotations,
not reflections. The authored offsets are **absolute positions from the
observer, not cumulative deltas** (established): the ray stepper applies each
rotated pair directly to the origin cell (`x = tileX + dx`, `y = tileY + dy`)
with no running accumulation, and the step-distance counter used by the horizon
test counts from one — both only cohere if the authored pairs are absolute
positions along the spoke.

**Jammer separation.** The sensor phase's jammer circles are drawn onto
separate radar-presentation surfaces that are wiped each tick and never affect
the gameplay LOS word mask or the per-player byte grids. Radar, sonar, and
jammer presentation never authors the LOS mask.

**The per-player byte grid's increment has no upper clamp.** It is a plain
byte increment with no comparison against 255, so a cell covered by 256
simultaneous observers wraps to zero and reads as fogged. The decrement is the
matching plain decrement; the word mask is never decremented and is instead
reset and rebuilt.

Sprite-mask publication clips start-inclusive/end-exclusive: right/bottom
ends clip to the half-resolution grid bounds, negative left/top origins skip
to `max(0, -origin)`, every bounds compare is unsigned so signed underflow
cannot wrap into border cells, and only mask bytes unequal to the transparent
sentinel touch the mask or its reference state. Accumulation is idempotent:
a cell receives its owner’s player-slot bit only when absent — the writer
never decrements. When any cell changed for the LOCAL player it clears the
fog-cache-valid mode bit and wakes the presentation composer; remote players’
changes dirty nothing locally.

The observer's emitter height byte is `clamp(worldY_high + modelTop, 0, 255)`
with the world Y first raised to at least `(SeaLevel+1) << 16`, and its coverage
tile **in terrain-ray mode** is `tileX = worldX_high >> 5`,
`tileZ = (worldZ_high - emitter/2) >> 5` (arithmetic shifts). Sprite-mask mode
computes a different tile — no model top, two independent floors, and the GAF
frame's own offsets subtracted; both forms are in `[R-VIS-01 §2]`.
`modelTop` is the high word of the model-top dword computed
once at model load as the maximum of `vertexY + pieceY` over the piece
hierarchy, floored at zero, so the eye sits at the top of the unit's 3DO model
rather than on the ground — see §3.5 [R-P0-18-A] for the full builder contract
and the Z-shear term.

In terrain-ray mode the origin cell is admitted unconditionally before any
spoke is walked. Each spoke step bounds-checks the candidate cell (unsigned,
before any terrain read) and admits it only when its height-relative slope
STRICTLY exceeds the retained horizon slope — equality fails. Exactly, with
the observer height byte clamped to 0..255, a retained numerator/distance
pair initialized to (-1, 0) per spoke line, the candidate difference taken
from the LOW byte of the aggregated two-byte terrain word, and step distances
counted from one: admit iff

```
retainedNumerator * stepDistance < candidateDiff * retainedDistance
```

(the comparison is the loop's only test — there is no separate compare of the
difference against zero — and it is strict, so an exact tie never admits).
After admission, the HIGH byte of the same terrain word is tested
with the identical strict comparison against the same retained pair; only
then does the pair become that high-byte difference and step distance.

Footprint refresh is throttled: in terrain-ray mode nothing is recomputed
unless the coverage tile X changed OR tile Y changed OR the observer height
byte moved by more than 5 since the stored footprint. On refresh the old
current footprint is removed (only when current coverage is enabled and the
old height byte is nonzero), the new origin/height is stored, an out-of-bounds
new origin stores an empty footprint and returns, then current coverage
publishes and history accumulates. Sprite-mask mode refreshes on tile X, tile
Y, or a changed stored quantized-radius byte instead.

A full rebuild refills both stores from scratch: when the history mode is
DISABLED the word grid fills with all-bits-set cells (otherwise zero); when
the current-coverage mode is DISABLED every eligible player’s byte grid fills
with 1 (otherwise zero). It then republishes all active units’ footprints and
wakes presentation.

**Gameplay visibility predicate.** One four-point gate serves unit auxiliary
draw preparation, tick-time enumeration into target buckets, and weapon
targeting — targeting queries with the attacker’s own owner identity, so the
self bypass admits same-owner targets. The query argument IS a player record,
not a raw pixel grid. Evaluation order:

1. Owner identity bypass: when the queried record equals the candidate unit’s
   owning player record, visible immediately — you always see your own units.
2. Instance-state early false: the hidden/cloaked instance bit in the unit’s
   instance-state byte returns not-visible at once.
3. Base point: center plus definition extents in 16.16 world units. Unless
   the unit’s 32-bit runtime status field carries the underwater-exemption bit
   (mask 0x200), a base height below sea level returns not-visible. Because
   the sensor phase’s friendly marking sets that same bit on owned and allied
   units (section 3.4), those units are implicitly exempt. **Sea level here is
   the map header byte scaled to world units — the same `byte × 65,536`
   comparison as section 2.2, not a comparison against zero.**
4. Each sample projects with the half-height shear (`v = (Z - (Y >> 1)) >> 5`,
   `u = X >> 5`, pixel components) and unsigned bounds against the queried
   record’s grid dimensions; the mode-selected source is that record’s
   current-coverage byte grid (any nonzero byte visible) or, otherwise, the
   word grid tested at the LOCAL player’s bit.

   **"Pixel components" is load-bearing.** Each 16.16 world coordinate is
   first narrowed to its signed 16-bit high word — the map-pixel component —
   and the shift by five is applied to *that*. Shifting the 16.16 value
   directly is wrong by a factor of 65,536, and the narrowing to a signed
   16-bit quantity is itself part of the contract: coordinates beyond ±32,768
   map pixels wrap rather than saturate.
5. Hull diamond: center, then east (definition X extent added), then north
   (definition Z extent added, half-height subtracted from the height), then
   west (X extent subtracted again). Any admitted sample returns visible.

   **The four samples accumulate; they are not independent offsets from the
   center.** One coordinate triple is carried through all four tests and each
   step mutates it, which is why step four subtracts the X extent "again":

   | sample | X | Y | Z |
   |---|---|---|---|
   | 0 center | `X` | `Y` | `Z` |
   | 1 east | `X + ex` | `Y` | `Z` |
   | 2 north | `X + ex` | `Y - ey` | `Z + ez` |
   | 3 west | `X` | `Y - ey` | `Z + ez` |

   The height decrement `ey` is its own definition field, distinct from the Z
   extent `ez`; the two are not the same value and neither is half of the
   unit's height. The resulting quadrilateral is a rectangle in projected
   space, not a diamond centered on the base point — the historical "hull
   diamond" label describes the sampling order, not the figure.

The remaining consumers apply equivalent tests rather than calling this gate:
weapon placement/order validation inlines a word-grid-first reject plus the
same mode-selected source test at the projected cell; projectiles use the
one-point form; feature drawing uses a **two-corner form** over footprint
extents — first corner at the cell origin (sheared), then a single corner
displaced by the footprint offsets (§5.1.5); the sensor phase's final pass
inlines a single-point test.

**Ally semantics: never OR’d.** The writer ORs only the source
unit’s own player-slot bit into each cell, and every reader tests only the
local player’s bit. No routine merges an alliance group into a cell before
test, and allied owners hold distinct player records, so the owner bypass
cannot fire cross-owner. Allied vision sharing does not exist through this
mechanism; the only residual question is whether some unresolved identity
path shares grids by other means.

**Cloak is a predicate early-out, not a mask edit.** Cloaking does not erase or
dim the LOS mask; the visibility predicate returns not-visible for cloaked
units until an exception applies — most notably proximity breach within the
cloaking unit's **compiled** minimum-cloak distance (`mincloakdistance`, the
definition field whose squared value the horizontal distance is compared
against; the compiler substitutes 80 when a cloak-capable definition leaves it
at 0, `[R-VIS-01 §4]` pass 4). Stealth and init-cloaked definition flags feed the same predicate
state.

Radar, sonar, and jammers never author this mask: the sensor phase rasterizes
range and jam circles onto separate presentation surfaces that are wiped each
tick, while the LOS mask persists.

#### R-VIS-01 §2 — LOS stamping: the observer record, the refresh throttle, and which publisher runs

**Established** (direct static trace of the sweep, the per-unit record
builder, the throttled refresh and both publishers).

**Producers of the temporary-sight ("eyeball") record.** Besides
the per-unit stamp below, the central unit-death handler appends a 60-tick
eyeball for a locally owned victim under `Circular`/`True` LOS in every
session kind, single player included ([08 R-SESS-01 §3]); its expiry and
compaction pass is [R-COMP-02 §2].

**The sweep.** The phase-5 per-player LOS stamp sweep (`[R-LAYER §1]` write
site 3) walks the player record's own unit block — a contiguous range of unit
records delimited by a first and a last record pointer — stepping one unit
record at a time, and calls the per-unit stamp for every unit whose alive
status bit is set. The block is exclusive: at battle setup the unit pool is
partitioned so that player *i* owns records `unitArray + (unitLimit × i + 1) ×
recordStride` through `+ (unitLimit − 1) × recordStride`, and every record in
the block is pre-stamped with that player's record pointer and slot byte. No
player's sweep ever visits another player's unit. Slot 0 is the null unit and
is not in any block.

**The observer record.** The per-unit stamp fills one stack record and hands it
to the throttled refresh. Its fields, in order, are: the owning player record;
a pointer to the unit's stored coverage-tile pair; the definition's
`sightdistance` as a signed 16-bit value; the low byte of the definition's
reference-height word (the model top of `[R-P0-18-A §1]`); a pointer to the
unit's stored coverage byte; the unit's world X; the unit's world Y **raised
to at least `(SeaLevel + 1) << 16`**; and the unit's world Z. Two consequences
are contracts: the model top reaches the emitter as a **byte**, so a model
whose top exceeds 255 whole world units wraps rather than saturates before the
clamp; and the sea-level raise happens in the record, so both rasters see the
raised Y.

**The stored coverage byte carries two different quantities.** In terrain-ray
mode it is the emitter height byte; in sprite-mask mode it is the quantized
shape index. The refresh throttle compares whichever one its own mode
produces. A live change of mode-word bit 2 is reachable through `LOSType`
([07 R-CAM-01 §6]). When switching to terrain-ray with current coverage
enabled, its command refresh clears the stored byte and re-enters each defined unit through the ray refresh
before a later ordinary refresh can compare the two encodings. The retained
tile pair admits the low-emitter exception stated above. When current coverage
is disabled, the command instead preserves those observer fields and performs
no immediate raster. In both cases the current-coverage grids are refilled
first, so a preceding current-coverage footprint cannot survive the mode
change.

**The refresh throttle and the publication gates**, exactly, with all shifts
arithmetic (floor) unless stated:

*Terrain-ray branch (mode bit 2 set):*

```
tileX   = worldX_high >> 5
emitter = clamp(modelTopByte + worldY_high, 0, 255)      ; worldY_high signed
tileZ   = (worldZ_high - (emitter >> 1)) >> 5
refresh iff storedTileX != tileX
         or storedTileZ != tileZ
         or abs(storedByte - emitter) > 5                ; STRICT >, so 5 does not refresh
on refresh:
    if storedByte != 0 and mode bit 1:  remove the old byte-grid footprint
    store tileX, tileZ
    if (unsigned)tileX >= visTileW or (unsigned)tileZ >= visTileH:
        storedByte = 0 ; return                          ; empty footprint, nothing published
    storedByte = emitter
    if mode bit 1:  publish current coverage  (byte grid, +1 per covered tile)
    if mode bit 0:  publish history           (word grid, OR the owner's bit)
```

*Sprite-mask branch (mode bit 2 clear):*

```
q       = clamp(floorDiv(sightdistance, 32) - 5, 0, shapeCount - 1)
frame   = the vismask GAF frame at index q
tileX   = floorDiv(worldX, 2^21) - frame.XOffset
tileZ   = floorDiv(worldZ, 2^21) - floorDiv(worldY_high, 64) - frame.YOffset
refresh iff storedTileX != tileX or storedTileZ != tileZ or storedByte != q
on refresh:
    if mode bit 1:  remove the old byte-grid footprint ; store ; publish current coverage
    else:                                                store
    if mode bit 0:  publish history
```

`floorDiv(worldX, 2^21)` is the 16.16 coordinate shifted right 21 places with
the negative-value correction — the map-pixel value divided by 32, i.e. the
visibility tile. Note that the two branches do **not** compute the same shear:
the ray branch takes one floor of `(worldZ_high − emitter/2)`, while the
sprite branch takes two independent floors, `floorDiv(worldZ, 2^21) −
floorDiv(worldY_high, 64)`, and the sprite branch does **not** add the model
top. The sprite branch also subtracts the GAF frame's own signed offsets, so
its stored tile is the footprint's top-left corner rather than the observer
cell; the ray branch's stored tile is the observer cell itself.

**Retail edge, stated as a contract.** A unit is constructed with its stored
coverage byte and both stored tile coordinates zeroed, and the sprite branch's
removal call carries **no** `storedByte != 0` guard (the ray branch does carry
one). A unit's first sprite-mask refresh therefore decrements the shape-0
footprint at tile (0, 0) in its owner's byte grid before publishing its real
footprint — an unbalanced decrement near the map origin, once per unit per
game, in Circular mode only. The decrement is a plain byte subtract with no
clamp (§3.2), so the affected cells wrap to 255 and read as permanently
visible until a bulk rebuild. Nanolathe may bound this as a sanctioned
divergence; it must not be "fixed" silently in a way that changes the ray
branch, which is guarded.

#### R-VIS-01 §3 — the LOS.TDF spoke tables and the ray walk, exactly

**Established** (direct static trace of the loader, the per-table
parse, the per-line quadrant expansion and the raster).

**File grammar.** The tables live in `gamedata/los.tdf`. A `[TABLEINFO]`
section carries `numtables`; each table is a section named `TABLE%d` for
`%d` = 0 … `numtables − 1` carrying `numlines`; each line is a key named
`line%d` whose value is a comma-and-space separated integer list whose **first
token is the point count**, followed by that many `(u, v)` pairs. Tokenization
is by the separator set `", "` and each token is converted with the ordinary
decimal string-to-integer conversion (so trailing garbage in a token is
ignored). A missing `TABLE%d` section leaves that table's line list empty; a
missing `line%d` key empties that line. `research/formats` does not own this
file: it is a TDF, and its grammar is `[fmt tdf]`; only the key meanings are
stated here.

**Quadrant expansion happens at load, not at raster time.** Each table's line
vector is sized to **four times** `numlines`, and each authored line is
written four times. Writing the authored pair as `(u, v)` and the stored pair
as `(dx, dz)`, line *i* of *M* is stored at indices *i*, *i + M*, *i + 2M* and
*i + 3M* with

```
copy 0 : (dx, dz) = ( u, -v)
copy 1 : (dx, dz) = ( v,  u)
copy 2 : (dx, dz) = (-u,  v)
copy 3 : (dx, dz) = (-v, -u)
```

which is `copy k = R^k(u, −v)` for the quarter-turn `R(x, y) = (−y, x)`. It is
a pure rotation: no copy is a reflection, and the sign flip on `v` in copy 0 is
part of the base transform, not a mirror. A clone that mirrors instead of
rotating produces the same set only for lines that are symmetric about the
diagonal.

**Table selection.** The group index is `min(max(floorDiv(sightdistance, 32),
0), numtables − 1)`, using the **declared** `numtables`, which is why
`los.tdf`'s twelve shipped `TABLE%d` sections with `numtables = 9` leave three
unreachable. The table-by-index accessor is **one-based**, so the table walked
for group `g` is record `g − 1` — and the loader filled record `d` from the
section it names `TABLE d + 1`, so that record *is* `TABLE g`. `[R-COMP-02 §1]`
states both halves and their consequences (the highest reachable table is
`TABLE numtables − 1`, the last loaded table is unreachable, and group 0 reads
before the list's storage).

**The walk**, per observer, after the origin cell has been admitted
unconditionally:

```
for each line of the selected table (all 4 × numlines of them):
    retainedNum = -1 ; retainedDen = 0 ; step = 1
    for each point (dx, dz) of the line, in authored order:
        x = tileX + dx ; z = tileZ + dz
        if (unsigned)x < heightTableW and (unsigned)z < heightTableH:
            lowDiff  = heightWord(x, z).low  - emitter
            highDiff = heightWord(x, z).high - emitter
            if retainedNum * step < lowDiff * retainedDen:      ; STRICT
                OR the owner's player bit into the word grid cell (x, z)
                if retainedNum * step < highDiff * retainedDen: ; STRICT, same pair
                    retainedDen = step ; retainedNum = highDiff
        step = step + 1
```

Four details that a summary loses and an implementer needs:

1. `step` advances on **every** point of the line, including points rejected by
   the bounds test and points rejected by the horizon test. It is the point's
   ordinal in the authored list, counted from one — which is why §3.2's
   "the authored offsets are absolute positions from the observer" and this
   counter cohere.
2. The retained pair resets **per line**, not per table.
3. The initial pair `(-1, 0)` makes the first point of every line admit
   unconditionally: `-1 × 1 < lowDiff × 0 = 0` for every terrain height.
4. The horizon advance is tested against the **same** retained pair as the
   admission, before the pair is updated — not against the newly admitted
   value. Both comparisons are the identical strict form — the loop contains
   no other test, in particular no separate comparison of the difference
   against zero — so an exact tie fails both and therefore never admits and
   never advances the horizon.

All products are signed 32-bit. `emitter` is the clamped 0–255 byte; the two
height bytes are unsigned, so both differences lie in −255 … 255.

### 3.3 Fog and unexplored edges

Anchors of the fog findings (`[R-RR16-A]`):

| Anchor | Finding in this section |
|---|---|
| `[R-RR16-A §1]` | A fully current-fogged cell remaps existing pixels through the gray palette table. |
| `[R-RR16-A §2]` | Dithered current fog writes black checker pixels instead of a constant fog color. |
| `[R-RR16-A §3]` | GAF frame offsets anchor each fog shape at its visibility-cell corner. |
| `[R-RR16-A §4]` | Each fog cell combines the four visibility tiles that meet at that corner. |
| `[R-RR16-A §5]` | The four-way variant selector is world-anchored and camera-independent. |
| `[R-RR16-A §6]` | A missing GAF entry leaves the destination untouched. |
| `[R-RR16-A §7]` | Conditional border fixups propagate corner bits when the cache crosses a map edge. |
| `[R-RR16-A §8]` | Black, gray, and dithered-gray families share mask geometry but differ in pixel effect. |

The frame composer draws terrain tiles before visibility gates. Explored terrain
therefore remains as tile art under fog. Unit, feature, projectile, and other
sprite/model passes use the hard visibility predicate and are either drawn or
skipped; no observed intermediate opacity is applied at the LOS edge.

The edge is aligned to 32-pixel visibility tiles. GAF transparency is binary
RLE skip/opaque copy, not a fog blend. No observed LOS path indexes the 256 by
256 ALP blend table. The `SHD` table belongs to model lighting, while model
shadows use `ALP` and no dither. `DitheredFog` is therefore not permission to
add checkerboard fog to the LOS mask.

Unexplored map borders/voids can remain black where no valid tile blit reaches
the backbuffer. The exact distinction between an in-map void tile and an
out-of-map clipped region is **Supported inference** (decider: a capture at
the map edge distinguishes them and shows whether the backbuffer persists
stale bytes beyond the play rect); that visibility culling itself is binary
and hard-edged is **Established**.

The mapping word grid is serialized in a save blob; the transient byte sight
grid, dirty flags, eyeball queue, and radar surfaces are not all serialized.

**Two-channel fog cache.** A dirty-triggered composer wakes on the
presentation dirty bit, clears it, and rebuilds the fog/minimap byte surfaces
by sampling the local player’s history bit and mode-selected current grid; it
is a pure consumer and never writes the word mask. The final overlay lazily
rebuilds a two-byte-per-cell cache (pointer, width, and height held in engine
root state) whenever its cache-valid mode bit is clear, aligning the cache to
the camera in 32-pixel cells including signed residues. Per cell:

- Channel zero == 15: fill the whole 32x32 cell with the default dark palette
  entry; nothing else is processed for that cell (short-circuit).
- Else channel one == 15: **a per-pixel palette-LUT remap** — `dst[i] =
  grayTable[dst[i]]` over the clipped cell — which desaturates the terrain
  already under the cell through the nearest gray palette entries, preserving
  texture; it never writes a constant color [R-RR16-A]. When the options-storage
  dither bit is set, the same state instead writes literal
  palette index 0 (black) at checker positions `(x + y + parity) & 1 == 1` with
  `parity = (camX + camZ) & 1` — black dots over whatever is on screen, never
  the fog color.
- Else channel one in 1..14: GAF frame `value - 1` drawn from a four-way
  variant family selected by `(cellX + cellY + cameraPhaseSum) & 3`, blitted
  plain or parity-seeded patterned per the same option bit.
- Then channel zero in 1..14: frame `value - 1` from a second four-way family
  via the plain blitter. Channel one therefore renders BEFORE channel zero.

This overlay sits at the compositor position after all strips/effects and
before selection/interface (section 1). The cached channel derivation is established (direct-static): hi accumulates the per-player byte-grid (`cur==0`) only when mode bit 1 is set else zeroed, lo accumulates the word-grid history mask `1<<player` regardless; each holds a 4-bit nibble `0..15` via four bounded bit-OR sites for masks `1,2,4,8` (`0` transparent, `15` solid dark, `1..14` index `value-1` into the Gray=hi=current and Black=lo=history four-way variant families with `variant=(col+row+camPhase)&3` deterministically from `floorMod(camera,32)` residues `0..31` via `offX/offZ=(res<16?-16:+16)-res` and `rect=[vpLeft+offX+col*32, vpTop+offZ+row*32, +31]` inclusive), edge rows/cols reached by conditional border fixups when the viewport extends beyond the map (see the fixup table below — never unconditional 15 stores). The bit→cache-cell geometry is direct-static: a fogged tile ORs bit 1 into the cache cell of its own tile, bit 2 into the west cell, bit 4 into the north cell, bit 8 into the northwest cell — the cell accumulates from the four tiles around its centre corner. Corner→bit `1=NW,2=NE,4=SW,8=SE` (which GAF quarter each bit paints) is supported inference (decider: an asymmetric fog.gaf probe); retail’s hard fog edge must not be softened to improve image metrics.

**Fog art and family behavior [R-RR16-A].** The four-way variant selector is
world-anchored, not camera-relative: substituting the cache-relative column
into `variant = (col + row + camPhase) & 3` with `camPhase = floorDiv(camX+16,32)
+ floorDiv(camZ+16,32)` collapses identically to `(gx + gy + 2) & 3` — the
camera phase cancels exactly, and the tiling must not rotate as the camera
pans. The 32×32 fog cell is centered on the corner where the four
visibility tiles `(gx,gy)`, `(gx−1,gy)`, `(gx,gy−1)`, `(gx−1,gy−1)` meet
(`sx = vpLeft + offX + col*32` with `startX = floorDiv(camX−16,32)` reduces for
every camera residue to map pixel `gx*32+16`; `floorDiv(x+16,32) −
floorDiv(x−16,32) == 1` identically), which is exactly the four tiles whose
fogged state the cell's nibble accumulates. A fully fogged tile gets one 16×16
cloud quarter in each of the four cells around its bottom-right corner.

The GAF fog frames carry their placement in the frame header's signed
`XOffset`/`YOffset` words — destination `(x − XOffset, y − YOffset)` before
clipping — and every shipped frame uses exactly two pixel values: palette
index 9 is the transparent color key and palette index 0 is the cloud
(the offsets are `[fmt gaf]` frame header +8). Frame `n` covers the
cell area implied by nibble value `n+1`: value 1 → 16×16 quarter at `(0,0)`,
2 → 16×16 at `(-16,0)`, 4 → 16×16 at `(0,-16)`, 8 → 16×16 at `(-16,-16)`;
intermediate values are unions with sizes `16×16..33×21` and matching offsets.
A missing GAF entry leaves the cell untouched — terrain stays visible through
it; there is no solid-fill fallback.

**Family behavior.** Mask geometry is identical across the three fog blitters —
non-key pixels covered, key pixels skipped — but what lands differs: the black
family (channel zero/history) is a plain keyed copy of source pixel 0 (paints
black); the gray family (channel one/current) is a masked LUT remap that never
writes the source pixel; the dithered gray family steps x by two and stores
literal palette index 0 at checker positions. The gray frames are geometrically
LARGER than the black frames for the same nibble value (gray frame-1 is 19×19
where black is 16×16), and the gray channel draws before the black channel, so
a boundary cell gets a desaturated fringe surrounding the black cloud — the
grey rocky border around the explored area in retail screenshots is fog art,
not terrain. It reads grey only if the gray table itself is built correctly
(§4.3.3): the nearest-color search must walk retail's sum-sorted permutation,
or the fringe comes out red and yellow.

**Map-edge propagation [R-RR16-A].** The four producer border fixups run only
when the cache window crosses the map edge and apply conditional bit ORs, never
unconditional stores:

| Fixup | Triggers when | Effect |
|---|---|---|
| Top (void row 0, all columns) | window crosses north edge | `bit4 → |= 1`, `bit8 → |= 2`; `hi` gated by mode bit 0x2, `lo` always |
| Bottom (row h−2, all columns) | window crosses south edge | `bit1 → |= 4`, `bit2 → |= 8` |
| Left (void column 0, all rows) | window crosses west edge | `bit8 → |= 4`, `bit2 → |= 1` |
| Right (column w−2, all rows) | window crosses east edge | `bit4 → |= 8`, `bit1 → |= 2` |

The passes run in order top, bottom, left, right on shared bytes, so corner
cells compound (an unexplored bottom-right in-map corner reaches 15 via bottom
`1→4` then right `4→8` then `1→2`). Void cells north/west of the map can draw
partial clouds or reach the unexplored solid-black short-circuit; south/east
void cells stay zero. **Supported inference:** the fixup rows/cols `h−2`/`w−2`
are relative to the viewport+border cache, so the exact camera band where
bottom/right thickening is active depends on the `+2 or +3` border width;
anchoring to the window end minus two and gating on the window crossing the map
edge is visually equivalent (the thickened 16px strip is only on screen when
the window crosses).

**Unexplored-versus-fogged mechanism.** The plot flag byte’s
0x04 bit marks a cell never-explored/fogged. The composer’s feature pass
clears it per cell before evaluating; a skipped hidden feature leaves it set,
and a cell whose anchor feature exists with feature height >= 10 is re-marked
immediately — tall features cast a permanent unexplored shadow over their own
cells independent of current line of sight. Drawing them is instead decided
each frame by the acceptance tests below, admission under the strict horizon
rule effectively requiring observation from ground high enough to see over
the intervening terrain. Pass two redraws a fogged cell’s feature only when
the feature quick-accepts or the two-corner predicate admits; otherwise the
marker stays set.

Full flag-byte semantics (write model verified):

- Bit 0 — live feature-instance present: set exactly when the feature stamper
  allocates an animation slot, cleared by teardown, and read by the feature
  reproduction walker as “no live instance”.
- Bit 1 — authored no-build/placement blocker.
- Bit 2 — the never-explored/fogged marker above.
- Bits 3..6 — placer nibble, written at stamp time as `(placer & 0xF) << 3`
  with bits 0,1,2,7 preserved. Map load stamps placer value 10 everywhere;
  corpse stamping passes the dying unit’s OWNER PLAYER SLOT, so your own
  wrecks carry your slot while map-authored features (nibble 10) can never
  match a real slot. The composer accepts a fogged feature for drawing
  without a line-of-sight test exactly when `(flags >> 3) & 0xF` equals the
  local player slot, for features whose definition carries the memory-accept
  flag.
- Bit 7 — unobserved.

**Post-load visibility timing (exact).** The visibility rebuild runs BEFORE
the serialized Mapping blob is read. That rebuild prefills both stores —
history cells all-set when history mode is disabled, current grids 1 when
current mode is disabled — and republishes any units already present. Unit
reconstruction then publishes each reconstructed unit’s footprint
synchronously to every mode-enabled store before the loader returns: there is
NO empty-coverage first frame. A missing or size-mismatched Mapping blob
leaves the array unchanged while publication proceeds. A deferred next-frame
rebuild must not be implemented.

### 3.4 Radar and sonar

The radar picture is built from the terrain tile set or an optional baked minimap.
Its aspect ratio preserves the map shape with a fixed long side. When generated,
the renderer supersamples to twice the radar dimensions, maps each output sample
back to map/tile coordinates, and **samples the tile-set pixel bytes directly**
— there is no height read and no terrain radar table (§3.7): the fill picks the tile from the
tile map and copies the indexed pixel at `(z & 31)*32 + (x & 31)` within the
tile block. It then creates radar-picture, mapped, and final surfaces.

Each tick, radar blips are projected from unit/world coordinates into radar
coordinates. Radar and sonar range circles, jammer circles, and weapon-range
circles are rasterized onto the radar surface using distinct palette colors.
The radar-mapped surface is wiped and rebuilt each tick, while the authoritative
LOS mask persists untouched by this path.

**Jamming is authoritative and acts on status bits, not through a surface.**
The jam callbacks write the unit status word directly: `radardistancejam`
clears the runtime **seen** bit and `sonardistancejam` clears the runtime
**sonar** bit (`[R-VIS-01 §5]`). Both effects change acquisition candidacy
`[06 §3.1]` and, through the sonar bit, the direct-visibility predicate's
underwater rejection (`[R-WPN-02 §4]`). The surfaces play no part in it: no
reader ORs the jammer or sensor-circle surfaces into the mapping word grid or
the per-player byte grids, and the gameplay visibility predicate never samples
them (bounded negative over the sensor and predicate families).

**Sensor and proximity phase.** The deadline-driven sensor phase runs only
on due viewing-player entries and when more than one player is present
([R-SENSOR-01]). It is five unit walks and it writes nothing but
unit status bits; `[R-VIS-01 §4]` states them in order, at implementable
precision, and `[R-VIS-01 §5]` states the radius visitor and the three
callbacks. In summary: a first pass clears the decloak-timer bit for every live
unit and sets the friendly status pair `0x300` on own units (plus everything,
when the viewing player has been defeated) while clearing `0x700` otherwise; a
second pass, over the viewing player's units only, emits one radar/sonar
contact query per active unit with a nonzero radar or sonar distance; a third
pass emits radar-jam and sonar-jam queries from every active unit not owned by
the viewing player; a fourth performs the minimum-cloak proximity scan; a fifth
sets the seen bit for any remaining unit standing on a lit visibility tile.
Status-field roles: `0x100` = seen marker, `0x200` = sonar (which doubles as
the underwater-rejection exemption of section 3.2), `0x300` = the friendly pair
the first pass writes, `0x400` = jammed (a marker with no reader anywhere),
`0x1000` = decloak timer. The phase never writes the word mask or any
per-player byte grid.

There is no rasterization in the sensor phase at all: the shift of 23 and
the 128-world-unit cell of `[R-VIS-01 §5]` are the **unit spatial grid** the
shared radius visitor walks to find candidates, not a pixel surface, and the
three callback tables hold three one-line functions that write unit status
bits. The arbitration between the passes is the bit-write order of
`[R-VIS-01 §4]`. The minimap's sensor circles are drawn by presentation
(§3.9, §3.10). The secondary radar-like candidate list of `[06 §3.1]` is the
seen set this phase writes, from the viewing observer's point of view
(`[R-WPN-02 §6]`).

**Sensor callback gate (Established).** “Active” in the sensor
phase means the unit instance's activation/on-state bit is set. A live unit
whose radar or sonar distance is nonzero emits its contact query only after
that activation test. The cloak/hidden instance bit is not consulted by this
circle-callback gate; it belongs to the separate visibility and decloak paths.
The selected-unit circle presentation has an additional definition test
documented in §3.9.

#### R-SENSOR-01 — sensor phase placement in the tick

**Established.** The sensor phase executes inside document 01 §4.4 phase 5,
in the local **viewing** player's iteration, after manager work, strategic
refresh, LOS publication, minimap contacts, the due victory/defeat block and
settlement gates. It immediately precedes the mapped-minimap rebuild.

The sensor call is **inside the viewing player's settlement-deadline block**:
the unsigned future-deadline branch skips directly to the next player. A due
block advances the deadline once by 30 and reaches the sensor tail even when
later settlement gates refuse resource settlement. The sensor routine itself
also requires more than one active player. It is therefore one pass per due
viewing-player entry, not one pass on every simulation tick. An overdue
deadline catches up one due entry at a time; loading retains that deadline.

Player order remains ascending, so the sensor work follows the viewing
player's stamps and precedes higher-indexed players' stamps in that same
phase. Its five unit walks cover the whole pool at this one placement. The
phase's first walk and the radar-jam callback own their documented seen-marker
clears ([R-VIS-01 §4]); no extra between-tick clearing pass is implied.

#### R-VIS-01 §4 — the sensor phase's five passes, exactly

**Established** (direct static trace of the phase and its three
callbacks; complete image-wide writer census for the three status bits).

There are **five** unit walks, not four: the mode-selected seen probe and
the minimum-cloak proximity scan are separate walks, and the phase's first
pass is both the friendly-marking pass and the clear.

**Which observer.** Two per-battle globals hold a player slot: the local
player's **own** slot, and the **viewing** slot. They are written together at
battle entry and re-pointed together when the local player becomes an observer.
Every read in this phase, in the mode-selected visibility probes of §3.2, and
in the minimap contacts pass of §3.9 uses the **viewing** slot. Nanolathe must
carry both and must not collapse them: in an observer session they differ, and
the entire secondary target list of `[06 §3.1]` follows the viewing slot.

**Gate.** The whole phase runs only when the active player count is strictly
greater than one. In a session with one active player none of the five passes
runs, so the seen, sonar and jammed bits keep the values unit construction gave
them: the seen bit clear, the jammed bit clear, and the sonar bit set exactly
for units whose owning player's slot equals the viewing slot at construction
time. A one-player session therefore has an empty secondary candidate list
and a `sonar` bit that means "mine".

**Status bits.** Three bits of the unit's 32-bit runtime status word are the
phase's whole output: **seen** (`0x100`), **sonar** (`0x200`) and **jammed**
(`0x400`). The friendly marking writes the pair `0x300`; the underwater
exemption of §3.2 and the underwater rejection of `[06 §3.1]` read the sonar
bit; `[06 §3.1]`'s secondary candidate list reads the seen bit; §3.9's
minimap contacts pass reads the pair. **The jammed bit has no reader anywhere
in the image** (bounded negative over all recovered functions): it is a marker
only, and jamming's authoritative effect is entirely the *clearing* it does.

**Pass 1 — clear and friendly marking.** Over every unit slot from 1 to the
end of the pool, for units whose alive bit is set:

```
clear the decloak-timer bit (0x1000)
own      = unit.ownerSlotByte == viewingSlot
allied   = candidateOwner.allianceRow[viewingPlayer.slot] != 0
           and candidateOwner.optionRecord.optionWord bit 6      ; see [R-VIS-01 §7]
observer = viewingPlayer.record is active
           and viewingPlayer.optionRecord.ruleWord bit 6         ; the defeated/observer flag
status = (own or allied or observer) ? (status | 0x300) : (status & ~0x700)
```

`allianceRow` is an eleven-byte row in the player record indexed by player
slot, loaded from the mission/save `Alliances` list, with a player's own entry
always set. The observer disjunct is what makes a defeated player see
everything: the defeat handler sets that rule-word bit, clears mode-word bits
0 and 1 (`[R-VIS-01 §1]`: Mapped + Permanent — both grids fill all-visible),
and forces one bulk rebuild.

**This pass is the seen-marker clear writer.** The `& ~0x700` branch clears
seen, sonar and jammed together, on each due sensor pass, for every unit that is not own,
allied-with-sharing, or seen by an observer. The only other clear in the image
is the radar-jam callback (`[R-VIS-01 §5]`).

**Pass 2 — radar and sonar emission.** Over the **viewing player's own unit
block only**, for units that are alive, not death-latched, whose activation
bit is set, and whose definition declares a nonzero `radardistance` **or** a
nonzero `sonardistance`:

```
radarRadius = radardistance + 2 × worldY_high      ; whole world units, signed
visit every unit within max(radardistance, sonardistance) of this unit's
     position and apply the contact callback with (radarRadius², sonardistance²)
```

The `+ 2 × worldY_high` bonus is the emitter's **world Y high word** — its
altitude in whole world units — not its model height and not its
`sightdistance`. A radar tower on a hill therefore reaches further than the
same tower in a valley, which is the elevation effect the option text
advertises, and it applies to **radar only**: `sonardistance` gets no bonus.

The bonus enters the **squared test radius only, never the search**: the
visitor is called with `max(radardistance, sonardistance)` — the two *authored*
values, unbonused — so no unit further than that is ever examined, however high
the emitter sits. Whenever `radardistance + 2 × worldY_high` exceeds
`max(radardistance, sonardistance)` the extra reach is unreachable and the
effective radar radius saturates at the authored maximum, which for the ordinary
radar-only unit (`sonardistance = 0`) means the elevation bonus has **no effect
at all**. It is observable only on a unit whose `sonardistance` exceeds its
`radardistance`, where it lets radar detection extend into the sonar circle.
This is a retail contract, not an oversight to correct: an implementation that
searches out to the bonused radius will detect units retail never examines.

**Pass 3 — jam emission.** Over every unit slot from 1 to the end of the pool,
for units that are alive, whose **owner slot differs from the viewing player's
slot**, and whose activation bit is set: if `radardistancejam` is nonzero,
visit every unit within it and apply the radar-jam callback; then if
`sonardistancejam` is nonzero, visit every unit within it and apply the
sonar-jam callback. Allied jammers are included — the test is "not mine", not
"hostile" — and so are the jammer's own side's units as *targets*, because the
callbacks apply no owner test at all.

**Pass 4 — minimum-cloak proximity.** Over every unit slot from 1 to the end of
the pool, for units that are alive, whose owning player record is active with
controller type 1 or 2 (the locally simulated human and computer controllers —
the same predicate as `[R-WPN-02 §2]`), and whose definition carries the
derived can-cloak flag:

```
if any entry of the primary candidate list of registry[unit.ownerSlot]
   is alive, not death-latched, and at planar squared distance
   <= mincloakdistance × mincloakdistance                       ; INCLUSIVE
then unit.cloakSuppressionDeadline = currentTick + 90
     status |= 0x1000
```

The list searched is the **per-side primary candidate list of `[06 §3.1]`** —
hostile units that passed the direct-visibility predicate when that side's
registry was last rebuilt, which can be up to thirty ticks earlier. So the
breach test is "an enemy I could see up to a second ago is within
`mincloakdistance`", not "an enemy is within `mincloakdistance`". The
definition flag is not an authored key: it is derived at parse time as
`cloakcost > 0.0` (strict, on the parsed float). The distance is a plain 32-bit
signed square of the **compiled** value — and for a cloak-capable definition
that value is never 0: the unit compiler substitutes **80** whenever the
can-cloak bit is set and `mincloakdistance` compiled to 0
(`[02 §5]` "Three derived fields the key table does not show"). So a
cloak-capable definition that omits the key breaches at 80 world units, not
never. The pass does not test whether the unit is currently cloaked.

**Pass 5 — the seen probe.** Over every unit slot from 1 to the end of the
pool, for units that are alive, whose seen bit is **clear**, and whose
instance cloak bit is clear:

```
tileX = worldX_high >> 5
tileZ = (worldZ_high - (worldY_high >> 1)) >> 5              ; arithmetic shifts
bounds: (unsigned)tileX < viewingPlayer.gridWidth
    and (unsigned)tileZ < viewingPlayer.gridHeight
if mode-word bit 1:  hit = viewingPlayer.byteGrid[gridWidth × tileZ + tileX] != 0
else:                hit = wordGrid[gridWidth × tileZ + tileX] has the viewing slot's bit
if hit: status |= 0x100
```

This is the single-point form of the §3.2 predicate: the same half-height
shear, the same unsigned bounds, the same mode-selected source, and — in
word-grid mode — the same **viewing player's** bit regardless of who owns the
unit. Note that both index expressions use the *player record's* grid width as
the row stride, which is the visibility-tile width.

**What that means for a single-player game with computer opponents.** The
phase evaluates exactly one observer. A computer player's own sensors never
write the seen bit for anybody, and a computer player's fallback (secondary)
acquisition therefore consumes the **human's** sensor picture: it can acquire a
unit precisely when the human can see or detect it, and it loses that unit
when the human's radar is jammed. This is not a per-side model with a shared
implementation detail; it is a single global picture with one owner. A clone
that gives each AI its own sensor grids will diverge from retail on every
fallback acquisition. The primary candidate list has the same property
whenever the word-grid predicate is selected (`[06 §3.1]`, "In word-mask mode
every probe tests the local player's bit").

**Ordering and outputs.** The five passes run in the order above, once per
**due** entry of the viewing player's settlement-deadline block inside the
per-player pass — the 30-tick cadence of `[R-SENSOR-01]`, not once per
simulation tick — and the deadline advance precedes them, so a due entry
reaches the sensor tail even when the later settlement gates refuse
settlement. (The per-tick work in the same per-player pass is the LOS stamp
sweep, which sits before the cadence block; `[R-LAYER §1]` write site 3 owns
it.) Pass 3 runs after pass 2, so a jam circle overwrites a radar
contact from the same pass; pass 5 runs after pass 3, so line of sight restores
a jammed unit's seen bit within the same pass if the unit is in the viewing
player's visibility state. The phase writes **only** the three status bits, the
decloak-timer bit and the cloak-suppression deadline. It writes neither
visibility grid, does not touch the fog cache, and raises no presentation
event.

**Random draws: none.** The phase, its three callbacks, the unit-grid radius
visitor and the minimum-cloak probe call nothing but the 64-bit multiply and
shift helpers. Neither the simulation stream nor the CRT stream is consulted
anywhere in the sensor or LOS-stamp families (bounded negative over the
complete callee sets of the phase, the sweep, the throttled refresh, both
publishers and the bulk rebuild).

#### R-VIS-01 §5 — the radius visitor and the three contact callbacks

**Established** (direct static trace; complete writer census).

**The visitor.** All three sensor callbacks are driven by one shared routine
that walks the unit spatial grid. Its cells are **128 world units** on a side
— positions reduce by an arithmetic shift of 23 places on the 16.16 coordinate
— and each cell holds the head of a singly linked chain of unit records. Given
a centre and a 16.16 radius it computes the cell rectangle

```
[floorShift(centreX - r), floorShift(centreZ - r)] ..
[floorShift(centreX + r), floorShift(centreZ + r)]
```

clamping each coordinate into `0 … dim-1` (negative values clamp to 0, values
at or above the dimension clamp to `dim-1`, tested unsigned). It then walks
every chain in that rectangle and, for each unit whose **planar** squared
distance to the centre is `<= r²`, invokes the callback. Both the distance and
the radius square are taken as the high 32 bits of the 64-bit product, i.e.
squared **whole world units**, and the axis terms are summed as signed 32-bit
values — the same metric as `[06 §3.1]`. The visitor's own test is
**inclusive**; the callbacks' tests are strict.

**The contact callback (radar and sonar).** Rejects, in order: a candidate
whose definition index is zero; a candidate whose owner slot equals the
**viewing** slot; and a candidate whose definition carries the `stealth` flag.
`stealth` therefore suppresses radar *and* sonar detection outright, with no
distance or elevation term — it is not a range reduction. Then, with `d²` the
planar squared distance to the emitter recomputed from the callback record:

```
sonar: if candidate.worldY <= SeaLevel << 16  and  d² < sonardistance²
           status |= 0x200
radar: if SeaLevel << 16 <= candidate.worldY + definition.boundingBoxMaxY
           and d² < radarRadius²
           status |= 0x100
```

**Established — radar height arithmetic.** The candidate world Y and the
definition's full 16.16 upper Y bound are added at 32-bit width, retaining the
low 32 bits of the sum. That result is compared as signed against the sea-level
byte scaled to 16.16; equality admits radar contact. Neither operand loses its
fractional bits, and the upper bound does not pass through the LOS emitter
byte conversion of `[R-P0-18-A §1]`. A sum that overflows the positive signed
range therefore compares as negative, rather than as a widened positive height.

Both distance comparisons are **strict**. The sonar admission test is on the
candidate's own 16.16 world Y against the sea plane; the radar admission test
is on the top of the candidate's bounding box, so a submarine whose hull top
breaks the surface is radar-visible while a fully submerged one is not, and a
unit sitting exactly at sea level satisfies **both** tests. The two writes are
independent: a unit can gain both bits from one emitter.

**The jam callbacks.** Two distinct one-line callbacks, one per jam field:

```
radar jam:  status = (status & ~0x100) | 0x400
sonar jam:  status = (status & ~0x200) | 0x400
```

They apply to **every** unit the visitor delivers — no owner test, no alliance
test, no stealth test, no re-test of distance beyond the visitor's inclusive
`d² <= r²`. So `radardistancejam` clears the seen bit and `sonardistancejam`
clears the sonar bit, for friend and foe alike, including the jammer's own
side (`[R-WPN-02 §4]`). **What a jammed contact loses**, stated
positively:

* *radar jam* — the seen bit, hence membership of every side's secondary
  candidate list (`[06 §3.1]`) and the "detected" half of the minimap contacts
  test (§3.9). It does **not** lose line of sight: pass 5 runs after the jam
  pass and re-sets the seen bit for any jammed unit whose tile is lit in the
  viewing player's visibility state, so jamming only hides what was known by
  radar alone.
* *sonar jam* — the sonar bit, hence the underwater exemption of §3.2 and the
  underwater rejection of `[06 §3.1]` step 4. A jammed submerged unit becomes
  invisible to the direct-visibility predicate outright, whatever the line of
  sight, because that predicate rejects a below-sea-level probe point when the
  sonar bit is clear. Sonar jamming is the stronger of the two.
* neither loses anything on the mapping word grid, the per-player byte grids,
  or the LOS raster. The presentation circles of the paragraphs above remain
  presentation-only.

**Complete writer census for the three bits.** Across the whole recovered
image, the seen, sonar and jammed bits are written at exactly five sites: the
sensor phase's first pass (`| 0x300` / `& ~0x700`), the contact callback
(`| 0x100`, `| 0x200`), the two jam callbacks, and the unit constructor, which
seeds the sonar bit as `(owner.slot == viewingSlot)` and clears the other two.
Nothing else in the image touches them; a producer of the seen bit outside
the sensor phase is bounded-negative over the recovered set and open only
for the unrecovered regions.

#### R-VIS-01 §6 — cloak, stealth, and the decloak deadline

**Established** for the fields, the flags and the gate; the
per-tick cost arithmetic is doc 05's.

**Authored inputs**, all read by the unit-definition parser:

| Key | Storage | Default | Consumer |
|---|---|---|---|
| `stealth` | definition flag word bit | absent → 0 | the contact callback's third reject (`[R-VIS-01 §5]`) |
| `init_cloaked` | definition flag word bit | absent → 0 | seeds the runtime cloak-wanted status bit at unit construction |
| `cloakcost` | 32-bit float | absent → 0 | the per-tick cloak upkeep charge; **also** derives the can-cloak flag |
| `cloakcostmoving` | 32-bit float | absent → the truncated `cloakcost` | the moving-unit upkeep charge |
| `mincloakdistance` | signed 16-bit | absent or 0 → **80** on a cloak-capable definition, by the compiler's substitution (`[02 §5]`); 0 otherwise | the proximity breach radius of `[R-VIS-01 §4]` pass 4 |

The can-cloak flag is **derived, not authored**: the parser sets a bit of the
second definition flag word exactly when the parsed `cloakcost` is strictly
greater than `0.0`. That bit is the gate on pass 4, so a definition with
`mincloakdistance` but no `cloakcost` is never scanned. It is also the gate on
the compiler's `mincloakdistance` substitution above, which runs at startup and
again at every battle entry, so the compiled radius a clone must reproduce is
80 for every cloak-capable definition that leaves the key at 0.

**The init-cloak spawn writer is a single site, not a walk.** The unit
constructor copies the `init_cloaked` definition bit into the runtime
**cloak-wanted** status bit (bit 11) as part of the same masked store that
copies the two standing-order fields, and there is no other writer of that bit
in the image. There is no spawn-time walk of nearby units, no spawn-time
proximity test, and no spawn-time visibility edit. A unit authored
`init_cloaked=1` simply starts with cloak requested, and the ordinary upkeep
below decides tick by tick whether it is actually cloaked.

**The constructor store, exactly.** The constructor clears bit 11 in an early masked store and then, in its final
status-word store, ORs in the definition's `init_cloaked` flag shifted to
bit 11 alongside the standing-move and standing-fire copies; the complete
writer set for bit 11 is that constructor store, the `Cloak_On` / `Cloak_Off`
handlers, and the save-game restore. The build-completion service writes
neither bit 11 nor the instance cloaked bit and never reads `init_cloaked`;
its capability-bit-24 arm is `isfeature` (death cause 7 plus the death
latch, [04 R-SPEC-01 §12]). Since the cloak upkeep block is not gated on
completion (doc 05), an `init_cloaked` nanoframe is cloak-requested — and,
when paid for, cloaked — while still under construction. No consumer of
`init_cloaked` exists outside the constructor: the visibility predicate of
§3.2 and pass 5 read only the instance cloaked bit.

**The cloak gate.** The per-player economy pass evaluates, for each of that
player's units:

```
cloakActive = status bit 11 (cloak wanted)
          and status bit 12 (decloak forced) is clear
          and currentTick >= unit.cloakSuppressionDeadline
```

and only when `cloakActive` does it attempt the upkeep charge; if the owner
cannot pay, the unit is not cloaked this tick. The result is pushed to the
instance cloak bit, which is what §3.2's predicate step 2 and the sensor
phase's pass 5 read.

**The not-due pass clears too.** "The result
is pushed" holds on every exit: a pass on which `cloakActive` is false — the
request bit clear, the decloak-forced bit set, *or* the deadline not yet
reached — pushes *clear* to the instance bit through the same transition call
the paid pass uses to set it, with no cost selection and no compare. The block
has no exit that leaves the bit alone; the per-exit table is
[05 R-ECO-01 §9]. So every reveal producer in the census below decloaks the
unit on its owner's next settlement pass — up to 30 ticks after the stamp —
never on the tick of the stamp itself, and `Cloak_Off` likewise takes effect
at the next pass.

**Two mechanisms, not one.** Bit 12 is cleared at the top of the sensor
phase's first walk on each due viewing-player entry and re-set by that pass's
proximity breach. It persists until a later sensor pass clears it. The
suppression deadline supplies the longer interval: the
breach writes `currentTick + 90` (three seconds at 30 Hz), and the unit stays
uncloaked until the tick counter reaches it even though bit 12 has long since
been cleared. The deadline field is **shared** with other reveal producers
outside this document — firing and several order transitions write
`currentTick + 150`, `+ 300`, `+ 600` and `+ 900` into the same field (docs 04
and 06 own those) — so a later write always wins outright, and a shorter
sensor breach can *shorten* a longer reveal already in progress. Retail does
not take a maximum.

**Writer census of the shared deadline.** Every
store to the per-unit deadline word in the recovered image, by value:

| Value | Writer | Owner |
|---|---|---|
| `0` | the unit constructor (creation) | [04 §2] |
| `tick + 90` | the sensor phase's proximity breach, pass 4 (`[R-VIS-01 §4]`) | this section |
| `tick + 150` | `SelfRepair`, `RepairUnit` (work phase), `RepairUnitNoMove` | [04 R-ORD-01 §5] |
| `tick + 300` | `MobileBuild` (work phase), `HelpBuild`, `Reclaim`, `Resurrect`, `VTOL_Reclaim` | [04 R-ORD-01 §5] |
| `tick + 600` | the projectile fill that every shot runs, stamping the shooter | [06 §4.1] |
| `tick + 900` | `Capture`, `ReclaimUnit` | [04 R-ORD-01 §5] |
| persisted value | the save-game restore | doc 08 |

Ten order-handler sites, one sensor site, one weapon site: the field is one
word and all twelve gameplay writers overwrite it. `BuildingBuild`,
`GetBuilt`, `VTOL_MobileBuild`, `VTOL_HelpBuild`, `VTOL_RepairUnit` and
`VTOL_ReclaimUnit` do not write it. The word has exactly **one** gameplay
reader — the cloak debit gate's `currentTick >= deadline` term (doc 05
[R-ECO-01 §9]); the save writer copies it out and nothing else, presentation
included, reads it. The sensor breach and the order handlers only write.
This table is the contract.

**Cloak is still a predicate early-out, not a mask edit** (§3.2): nothing in
this path writes either visibility grid.

#### R-VIS-01 §7 — allied sensor sharing: what is shared, and the bounded absence

**Established** that no allied sharing occurs anywhere in the
recovered image; **Unknown** whether the gate bit the phase reads is the one
retail's authors intended.

Doc 05 records only the option and cadence: every 450 authoritative ticks a
separate option can emit a radar/sensor share command for allied players, and
defers the shared state to this document. The answer is that **no shared state
exists on the receiving side in the recovered image**, on any of the three
channels a clone might expect:

1. **The mapping word grid is never OR'd across players** (§3.2, "Ally
   semantics are closed"): the writer ORs only the source unit's own slot bit,
   and every reader tests one slot's bit.
2. **The per-player current-sight byte grids are never merged.** Each is
   incremented and decremented only by its owner's footprints, filled only by
   the bulk rebuild, and read only through its own player record.
3. **The sensor phase's allied disjunct cannot fire.** Pass 1's second
   disjunct requires a bit of a 16-bit option word in the candidate owner's
   option record. A complete reference census of that word across the whole
   recovered image finds twenty-six sites; **every one except this read tests
   bit 0**, the "this slot is me" flag, and **no site anywhere writes bit 6**.
   The structurally parallel bit — the same bit number in the *other* 16-bit
   option word four bytes further into the same record — is written, by the
   defeat handler and by two session-transition cases, and is exactly the
   defeated/observer flag that pass 1's *third* disjunct reads.

So the friendly marking in the recovered image marks own units and, when the
viewing player has been defeated, everything; allied units get nothing. The
consequence for a clone is concrete: an ally's radar contact never appears on
your minimap, an ally's units are not exempted from §3.2's underwater
rejection on your behalf, and an ally's vision never enters the secondary
candidate list.

**Unknown:** whether reading the option word rather than the rule word at that
one site is a retail defect (the two are adjacent words of one record and the
bit number is the same in both) or a deliberate second flag whose writer lies
outside the recovered functions. *Deciders, in order:* a static trace over the
unrecovered regions and the lobby/session option parser for any writer of that
bit; failing that, a manual retail observation — an authored two-human-ally
skirmish probe in which one ally alone has radar coverage of a third player's
unit, checking whether the other ally's minimap shows the contact. Until one of
those lands, Nanolathe must implement the bounded behavior — no allied sensor
sharing — and must not "restore" sharing on the grounds that it seems intended.

**What the 450-tick command does carry** is doc 05's and doc 08's question,
not this document's: nothing in the recovered visibility or sensor path
consumes an incoming share.

#### R-VIS-01 §8 — what the sensor and LOS phases publish to presentation

**Established**.

The simulation half writes exactly two presentation inputs, both from the LOS
publisher and the bulk rebuild, never from the sensor phase:

1. **The fog-cache-valid mode bit is cleared** when a word-grid publication
   changed at least one cell **and** the publishing observer's owner slot
   equals the viewing slot. Remote and computer players' stamps dirty nothing.
   The bulk rebuild clears it unconditionally. Camera motion clears it too
   (§3.1).
2. **A presentation dirty byte gains its visibility bit** at the same two
   sites; camera motion sets a different bit of the same byte.

The compositor that consumes them — the two-channel fog cache, its nibble
derivation, and the minimap surfaces — is §3.3's and §3.6–3.9's, and is a pure
consumer. Nothing in §§3.1–3.5 writes a surface.

The one presentation string this section owns an answer for is
`Unidentified object`, the caption the HUD prints for the unit under the
cursor. It is produced when the §3.2 direct-visibility predicate, queried with
the **viewing** player's record, returns false for that unit. It is **not** a
distinct sensor state: there is no "radar-only contact" or "jammed contact"
presentation state, and the seen/sonar/jammed bits are not consulted by the
caption at all. Doc 07 owns the caption's placement.

#### R-VIS-01 §9 — the five dead sensor definitions are retail's: the activation bit's complete writer census

**Established** (direct static read of the sensor phase's gate; a
whole-image census of every store to the unit's first state byte, direct and
through the shared edge setter; an asset census of the five definitions).

**The five.** `ARMANNI` (`radardistance` 1200), `ARMSS` and `CORSS`
(`sonardistance` 489) and `ARMACSUB` and `CORACSUB` (`sonardistance` 400 and
500) author a sensor range but no writer of the activation bit reaches
them, so under §3.4's sensor callback gate they never emit. Neither a missed
writer nor a sonar-specific gate exists:

**One bit, one read, for radar and sonar alike.** Pass 2 of `[R-VIS-01 §4]`
admits a unit with one compound test — alive, not death-latched, **activation
bit set**, and `radardistance` or `sonardistance` nonzero — and the jam pass
reads the same bit through the same byte. There is no sonar-specific
activation state; a submerged, cloaked or stealthy unit is not exempted from
the test and the cloak/hidden bit is not consulted by it.

**The complete writer set of the activation bit.** The bit is bit 0 of the
unit's first state byte. Three routines store that byte directly: the unit
constructor (zero), the save-game unit reader (the saved byte, `0xB2` of the
184-byte record, `[08 R-SAVE-02 §6]`), and the shared **edge setter** — the
mask-and-set/clear service whose rising edge runs `Activate` and whose
falling edge runs `Deactivate` (`[04 R-SPEC-01 §12]`). Every caller of the
edge setter whose mask includes bit 0, exhaustively:

1. **Creation with the already-built argument** and **build completion**,
   both behind the definition's `activatewhenbuilt` (`[04 R-SPEC-01 §12]`).
2. The **`Activate` / `Deactivate` order handlers**, both behind
   `onoffable` (`[04 R-SPEC-01 §11]`, `[05 R-PROD-01 §2]`).
3. The **COB `ACTIVATION` port** (port 1 of `[04 §4.7]`), written only by a
   unit's own script.
4. The **factory production pump** (`BuildingBuild` phase 0): a
   building-class unit is set active while its queue count is positive and
   cleared when it is empty (`[05 "Factory production lifecycle"]`).
5. The **computer player's metal-maker toggle**: for units whose definition's
   makes-metal byte is nonzero only, cleared when the owner's energy stock is
   at or below twice its storage-derived threshold, otherwise set with a
   four-in-five chance per visit (`[08 R-AI-01 §16]` owns the planner).
6. The **mover-mode setter** — mode 2 (airborne) sets, mode 1 (landed)
   clears (`[04 R-AIR-01 §1]`) — and the **air order executors** of the
   third static descriptor array (`VTOL_*` and `Air*`, `[04 §3.1]`), whose
   shared prologue sets the bit unconditionally and then takes off when the
   mover is landed; `VTOL_Landing` clears it on touchdown.
7. Three **copies of an existing byte**: the capture/ownership transfer
   (the new record receives the old record's byte, set bits then clear
   bits), the save-game reader's state-byte apply, and the network
   unit-state event that the edge setter itself emits to remote peers.

No writer reads the cloak bit, the submerged state, a sonar or radar field,
or a weapon state. Every other caller of the edge setter carries a mask
without bit 0 (cloak, building, the two interface bits).

**Applied to the five.** Each authors neither `activatewhenbuilt` nor
`onoffable`, has no build list (so never receives `BuildingBuild`), authors
`makesmetal 0`, is not `canfly` (so never runs an air executor or the
mover-mode setter's airborne arm), and its script writes no port 1
(a census of all 278 stock scripts). Writer 7
only propagates a byte another writer produced. So for their entire life the
bit holds the constructor's zero, the sensor gate rejects them on every due
sensor pass,
and their authored range is dead data **in retail**. This is the behavior
Nanolathe reproduces; widening the gate for them would assert a writer that
does not exist. **Established.**

#### Both the LOS table accessor and the loader's table names are one-based: group `g` walks `TABLE g` [R-COMP-02 §1]

**Established** (instruction-level read of the six table accessors, the
`los.tdf` loader's per-slot section lookup, and all three LOS publishers' table
selection; re-read 2026-09-16, which settled the loader half).

The loaded `los.tdf` lives in three nested dynamic arrays with six trivial
accessors: the table list (16-byte table records; *count* = `(end − begin) /
16`, 0 when unallocated), a table's line list (16-byte line records; the same
count form), and a line's point list (4-byte records holding one `(dx, dz)`
signed 16-bit pair; count = `(end − begin) / 4`; the point accessor returns
the pair at `begin + 4 × index`). The line-by-index accessor returns
`begin + 16 × index`. The **table-by-index accessor returns `begin + 16 ×
(index − 1)`** — it is one-based.

**The loader's slot fill is one-based too.** It reads `numtables`, resizes the
table list to exactly that many records, and then, for each zero-based slot
`d`, *synthesizes* the section name `TABLE` followed by `d + 1` and looks that
name up in the parsed file. Slot `d` therefore holds `TABLE d + 1`. File order
is irrelevant; a section whose synthesized name is never generated
(`TABLE10`…`TABLE12` with the shipped `numtables = 9`) is never read, and a
requested name that is absent leaves that slot's default empty line list — an
observer selecting it walks no spoke and sees only its own tile. Line keys are
formed the same way: authored `line1`…`lineN` fill zero-based line slots
`0`…`N − 1`.

All three publishers (the ray-walk stamper, the byte-grid increment and the
byte-grid decrement) select the table identically:

```
g = floorDiv(sightdistance, 32)          ; signed 16-bit sightdistance
if g < count − 1 : g = max(g, 0)
else             : g = count − 1
table = tableByIndex(g)                  ; = record g − 1 = TABLE g
```

The two one-based conventions cancel: record `g − 1` is the section named
`TABLE g`. So the table actually walked for a sight distance in
`[32k, 32(k+1))` is `TABLE k`, whose authored extent is exactly `k` cells
(stock `TABLE k` has maximum coordinate magnitude `k` and is commented "Radius
of k"), matching the sprite-mask raster's `floor(sightdistance / 32)` cells for
the same observer. The surviving off-by-one is at the top: the clamp stops at
`count − 1` where the one-based accessor would accept `count`, so the highest
table any unit can reach is `TABLE numtables − 1` (`TABLE8` with the shipped
`numtables = 9`), the last loaded table `TABLE9` is unreachable, and so are the
three undeclared sections. A sight distance below 32 selects
`g = 0`, whose record lies **16 bytes before the table list's storage**. What
those bytes hold at run time — and therefore how many "lines" such an
observer walks — is **Unknown** (decider: the tagged allocator's block header
layout, or a retail capture of a unit with `sightdistance < 32`). A clone must
reproduce this selection for `g ≥ 1` — including the unreachable top table; for
`g = 0` it may substitute an empty line list as a sanctioned divergence, stated
as such.

**No stock definition reaches group 0 (Established, catalog census over the
reference install).** All 278 stock
`.fbi` definitions author a `sightdistance`, and the minimum over the corpus is
**55** (the seven mines), giving `g = floor(55/32) = 1` and `TABLE1`. So the
group-0 record is never selected in stock play, and the sanctioned empty-line
substitution above is unobservable there — it becomes reachable only under a
mod that authors a sight distance below 32.

#### Eyeball expiry: the post-phase sweep removes the byte-grid footprint and compacts the list [R-COMP-02 §2]

**Established** (direct-static: the sweep, its call site in the tick
executor, and the decrement publisher). This is how a temporary sight source
("eyeball", [01 R-PLAT-02 §5] / [08 R-OOS-01 §1]) expires; the central death
handler appends eyeballs in every session kind ([08 R-SESS-01 §3]), so the
sweep does real work in single player.

The tick executor runs the eyeball sweep once per sub-tick **after the twelve
phases and after the deadline-ring slide**, as its last step. Over the eyeball
list (at most 20 records, 36 bytes each, count held beside the block):

1. For every record whose **expiry tick is strictly below the current tick**
   (unsigned compare), the byte-grid **decrement publisher** of [R-VIS-01 §2]
   is invoked with the record — the same removal call the unit stamp uses,
   with the record's own stored tile pair and stored coverage byte, and with
   no `storedByte != 0` guard. Records with `expiry ≥ tick` are untouched, so
   an eyeball still covers on its expiry tick and is removed on the next.
2. If at least one expired, the list is compacted **in place and stably**:
   from the first expired record onward, every surviving record is copied
   down one slot at a time, and because each record carries pointers to its
   own inline coverage-tile pair and coverage byte, those two pointers are
   re-aimed at the destination slot during the copy. The count becomes the
   number of survivors.

The history (word-grid) footprint an eyeball published is never removed —
history is permanent for every observer. No CRT or simulation draw is made.

### 3.5 LOS observer height, coverage tile, and the terrain height word [R-P0-18-A] [R-P0-18-B]

Every finding below is **Established** (direct static evidence; the
aggregation formula verified instruction-by-instruction against the map-load
builder, including the per-column carry). This section carries R-P0-18-A and
R-P0-18-B: the emitter-height addend provenance, the coverage tile's Z term,
and the aggregated two-byte height-word derivation. The terrain word is built
once per map load and never rebuilt.

#### R-P0-18-A §1 — Observer emitter height and model-top provenance

The LOS observer footprint builder fills one record per unit with: the
definition's `sightdistance`; the definition's model-top field (as a byte); the
unit's previously stored height byte (the value the refresh throttle compares);
and the unit's current world X, Y, Z as 16.16 fixed-point. Before the emitter
byte is formed, the record's world Y is raised to at least
`(SeaLevel + 1) << 16` — the map's sea-level byte, incremented by one and
scaled to 16.16 — so this clamp only moves the value upward.

The emitter byte is then formed from the record's high words:

```
heightByte = clamp(modelTop + (worldY >> 16), 0, 255)
```

with the world-Y high word taken as a signed 16-bit value (the signed high
word of the 16.16 coordinate). A sum below zero saturates to 0; a sum above
255 saturates to 255.

##### Model-top provenance

The model-top byte is not a definition field of its own. At model load a single
pass computes the model's top extent once and stores it as a dword; the LOS
record consumes its high word, so the emitter addend is the model top in whole
world units. The walk visits each piece and its sibling chain and accumulates
the maximum of `vertexY + pieceY` over the piece's vertices — each vertex
record is three 16.16 coordinates, Y the second — then adds the recursive
result of the child subtree plus this piece's own Y translation (sibling/child
links per §2.4). The accumulator starts at zero, so the result is floored at
zero. The same load-time site also derives the model height as the model top
minus a separate definition field; the LOS path never consumes that value.

Measured on the reference install (whole world units): ARMCOM 39, CORCOM 38,
ARMPW 26, CORAK 26, ARMSOLAR 38, ARMLLT 46.

##### Why the model top matters

The terrain-ray horizon test admits a step only when its slope STRICTLY
exceeds the retained horizon (§3.2). An observer whose height equals the
ground beneath it retains slope `(0, 1)` after its first step, and on flat
terrain every later step ties and is rejected — every unit's sight collapses
to a single ring of roughly one cell. Sighting from the model's top gives the
ray a negative slope to spend, so flat ground stays open and a laser tower
(model top 46) genuinely outranges a peewee (model top 26) at equal
`sightdistance`. This is also the mechanism behind the attested "a tank in a
valley sees less than a radar tower on a hill".

#### R-P0-18-A §2 — Coverage tile shear

Immediately after the clamp the same builder computes the coverage tile with
arithmetic (flooring) shifts:

```
tileX = (worldX >> 16) >> 5
tileZ = ((worldZ >> 16) - emitter/2) >> 5
```

`emitter/2` is itself an arithmetic shift by one. Both shifts are arithmetic —
floor, not truncation toward zero. The Z subtraction is the same beam shear the
camera projection applies: a tall observer's LOS footprint sits half its height
north of its ground position. The record's stored tile X, tile Z, and stored
height byte are exactly what the refresh throttle of §3.2 compares: recompute
only when tile X changed, or tile Z changed, or the height byte moved by more
than 5.

#### R-P0-18-B §1 — Terrain height-word polarity

**Polarity: the low byte is the maximum, the high byte the minimum.** The
table is allocated as `((TileW * TileH) + 7) & ~7` sixteen-bit words with
`TileW = CellW/2` and `TileH = CellH/2` (arithmetic shifts), i.e. one word per
visibility tile, and each word is seeded low byte `0x00`, high byte `0xFF`.
Every update then applies, per scattered value:

```
if (value > low)  low  = value
if (value < high) high = value
```

A 0-seeded accumulator that keeps the larger is a **maximum**; a 255-seeded
one that keeps the smaller is a **minimum**. So: **low byte = MAXIMUM, high
byte = MINIMUM**. Retail writes the complementary non-strict form — it
replaces low whenever `low <= value` and high whenever `high >= value` — so
on equality it stores the value it already holds; the outcome is identical to
the strict form above, and only a write census would see the difference.

The pair is not symmetric in the horizon rule of §3.2: admission tests
`retainedNum * stepDist < candidateDiff * retainedDen` with
`candidateDiff = low - emitter`, and the horizon advances on
`highDiff = high - emitter`. The inverse pairing is the maximally occlusive
one — low as the minimum makes the candidate difference more negative (harder
to admit) and high as the maximum makes the retained horizon shallower
(harder to admit afterwards) — and scatters false shadows across ground
retail leaves fully visible.

#### R-P0-18-B §2 — Cell scatter through the beam shear

The builder walks
columns then rows — outer loop over CellW, inner loop over CellH — with the row
coordinate carried in pixels and stepped by 16 (one attribute cell) per row.
For each attribute cell it reads the raw height byte at plot-cell offset 4 (per
§2.2 layout) and computes

```
zs    = zPixels - height/2     (arithmetic shift by one)
tileZ = zs >> 5                (arithmetic shift)
if (tileZ <= -1) skip the projection
```

— the same height shear the observer's own coverage tile uses. The cell
projects onto tile columns `(x-1) >> 1` and `x >> 1` at row `tileZ`. The two
columns **coincide for odd `x`**, and retail tests for that and skips the
second resolution, leaving the second element of the pair empty for that
column: "the two tiles" below is one tile on every odd column. Scattering
twice into one word would be idempotent, but the skip is what the carried
pair's bookkeeping records.

Two values reach the table per cell. First the perspective-scaled

```
value = ((tileZ*32 + 31) * height) / (zs + 31)      (truncating division)
```

is scattered into the two tiles the PREVIOUS row's cell in this column
resolved to (a carried pair, zeroed when out of bounds) and then into this
cell's own two tiles. Then the raw `height` is scattered into this cell's own
two tiles; on the skipped-`tileZ` path only the carried pair is updated, and
with the raw height.

The carry is what stops tiles being missed where the shear jumps a row. Since
`value <= height` for every non-skipped `tileZ`, the net effect is: the low
byte ends up the maximum of raw heights over the scattered neighbourhood and
the high byte the minimum of scaled values.

#### R-P0-18-B §3 — Tail blend and sea-level floor

A final pass over every
word blends, both divisions by three truncating (executed through a
fixed-point reciprocal multiplier, not an arithmetic divide):

```
newLow  = (high + 2*low)  / 3
newHigh = (low  + 2*high) / 3
if (newLow  <= SeaLevel) newLow  = SeaLevel
if (newHigh <= SeaLevel) newHigh = SeaLevel
```

Both results are computed from the ORIGINAL pair — not chained. The blend pulls
the two bytes a third of the way toward each other (on flat ground they
converge); the floor is the map's sea-level byte.

#### R-P0-18-B §4 — Build lifetime: once per map load, no lazy rebuild

The table is built exactly once per map load: the TNT loader calls the builder
after the derived height pass and before the void fixup, and the array is freed
at battle teardown. There is **no** lazy rebuild and no invalidation during a
battle — terrain deformation does not refresh it, a tall feature never does,
and the mode word's bit 3 is not a terrain-word dirty bit: it is the
fog-cache-valid bit of section 3.1, whose lazy rebuild is the fog/minimap
overlay cache of section 3.3. A reimplementation must build the word once at
map load and keep it stale for the battle.

### 3.6 Minimap surfaces and lifecycle

Retail holds four indexed surfaces for the minimap rather than one framebuffer region:

- **Radar picture** — the terrain picture, aspect-fitted to the fixed 126-pixel
  long side, built once and cached. Its source is either a baked minimap shipped
  in the TNT header or a generated sampling of the tile set. It never contains
  units or fog.
- **Radar mapped** — the picture masked by the authoritative LOS grids (the
  mapping word mask and the per-player byte grids of section 3.1); this is
  where unexplored and fogged terrain appears on the minimap. Rebuilt only when
  its dirty bit is set.
- **Radar final** — the composited minimap the player sees: mapped wiped onto
  final as the background, then unit blips, feature dots and sensor circles.
  Rebuilt every tick from mapped. The viewport rectangle is not on FINAL —
  the HUD composer strokes it onto its own destination surface after copying
  FINAL there, `[R-MM-01 §1]`.
- **Radar temp** — a transient 2× supersampled buffer used only while
  generating the picture, freed immediately afterwards.

Panel chrome lives on two further surfaces: the flip surface, sized by the GUI
root (640×480 and so on), and a fixed 300×480 backup surface cleared to index
0. The main-view fog overlay (the Gray/Black GAF families of section 3.3,
viewport-sized) is a distinct surface; the minimap path and the fog overlay
share only a dirty bit.

Radar, sonar, and jammer presentation never authors the gameplay LOS word mask
— circles reach the final surface only through the contacts pass of
section 3.9. Fog and unexplored territory on the minimap is mapped masking, not
the viewport fog cache.

**Lifecycles.** The panel surfaces live from battle enter until battle exit. The
radar surfaces' lifetimes are tied to map load; the temp buffer is the only
radar surface explicitly freed — the free path for the picture, mapped, and
final surfaces is bounded-negative in the traced corpus (no free site observed)
and remains `TODO(T23)`.

**Cadence.** A radar dirty word carries the schedule: bit 0 is the blink phase,
bit 1 is final dirty (set by the mapped composite and by the contacts pass),
bit 2 is mapped dirty (set by surface allocation and by placement invalidation,
cleared by the mapped composite after it checks). A countdown byte counting 7
down to 0 drives the blink phase. The picture is built once, dirty-triggered;
mapped composites when dirty; final is rebuilt every tick after the sensor
phase.

**Mapped-surface invalidation, exactly (Established).** "Placement
invalidation" in the cadence paragraph above is the LOS *raster* publication,
not unit or building placement. The tail of the raster that ORs a footprint's
bits into the mapping word grid raises the mapped-dirty bit and clears the
fog-cache-valid mode bit **together**, under one shared pair of conditions: the
raster changed at least one cell, and the observer's owning slot is the local
viewing slot. The bulk rebuild of the whole mapping state (§3.2's rebuild-fills
path) ends with the same two writes, followed immediately by the mapped
composite and the final rebuild. There is no third writer: nothing else in the
minimap, sensor or contacts paths sets the mapped-dirty bit, so the only events
that recomposite MAPPED are surface allocation, a local observer's raster
changing a cell, and a bulk rebuild. This is the same event set §3.1 already
records for the fog-cache-valid bit ("cleared at map load, on camera moves, and
by LOS publication (any local coverage change)"), minus camera moves, which
touch the fog cache only.

Consequence for a reimplementation: the mapped composite is a pure function of
the picture and the two LOS stores, and the dirty bit is only a cache over it.
A port whose presentation layer reads the committed stores once per tick may
composite unconditionally and obtain the identical surface; what it must not do
is gate on a bit that only surface allocation ever raises, which freezes the
minimap at the first composited frame and loses the explored-terrain memory the
idempotent-OR word grid exists to provide (§3.2 "a dead unit merely stops being
swept and its already-mapped bits persist").

**Save/load.** The final surface is serialized into the save blob. The picture,
mapped, and temp are rebuilt through the dirty bits on load; the mapping word
mask and the per-player byte grids are serialized as the Mapping blob. The fog
cache is not saved and is rebuilt lazily when its cache-valid mode bit is clear.

### 3.7 Radar picture build: baked versus generated

**TNT baked slot, version-gated** `[fmt tnt]`. The baked minimap lives in the
TNT header with version-specific slot positions: legacy version 0x1020 stores
the present flag in header word 15 bit 0 and the offset in header word 14;
canonical version 0x2000 stores the flag in header word 11 bit 0 and the offset
in header word 10. When the flag is clear there is no baked picture; when set,
a surface is allocated for the shipped bytes (tagged "TED GENERATED PIC"). The
stock corpus uses the generated path; both legs are traced.

**Generated picture, 2× supersampled.** When no baked image exists, retail
allocates the temp surface at 2·RadarW × 2·RadarH, samples terrain at doubled
resolution, then blends down through the ALP table:

```
for y in 0 .. 2*RadarH-1, x in 0 .. 2*RadarW-1:
    worldX = PlayRight * x / (2*RadarW)     truncating
    worldZ = PlayBottom * y / (2*RadarH)    truncating
    tileX = floor(worldX / 32)              signed floor (sign-corrected shift)
    tileZ = floor(worldZ / 32)
    tileIdx = uint16 tile map[tileZ*TileW + tileX]
    if tileIdx >= tileSetCount: tileIdx = 0   guard
    pix = tileSet[tileIdx*1024 + (worldZ & 31)*32 + (worldX & 31)]
    store index pix via the single-pixel blitter (no palette translation at build time)
```

The tile map and tile-set pixels never mutate after load (bounded-negative: no
writer outside the map loader). Crater decals are not strip objects — the
producer census [R-STRIP-01 §1] finds no crater/decal producer on any strip
— and how ground scorch marks are authored (a direct map/tile edit versus
another store) is **Unknown** (decider: trace the crater writer). Picture
bytes are PALETTE.PAL
indices — no LHT brightening or SHD shading — resolved to RGB only at
presentation.

**Downsample: two-level ALP blend, row-first.** The 2×2 downsample uses the
palette's 256×256 ALP table (64 KiB: an ordered pair of palette indices maps to
the nearest-color palette index):

```
blendTop    = ALP[ p00*256 + p01 ]
blendBottom = ALP[ p10*256 + p11 ]
out         = ALP[ blendTop*256 + blendBottom ]
```

**Established** (direct-static). The pairing is row-first horizontal: p00 =
(2x, 2y), p01 = (2x+1, 2y), p10 = (2x, 2y+1), p11 = (2x+1, 2y+1) — the top-row
pair is blended first, the bottom-row pair second, and the two blend results
combine through a third lookup of the same table (both results are palette
indices, so the second level is well-formed). Column-first and diagonal
pairings are rejected by exhaustive search (bounded-negative).
`TODO(question):` whether the left/right orientation within the row is mirrored
on screen remains open; an asymmetric-palette probe (row-first, column-first,
and diagonal orderings yielding distinct results) settles it.

**The shared resampler (Established, direct-static).** Both picture legs
enter one generic routine with independent source and destination
dimensions. For each destination axis
it truncates the source coordinate ratio, blends that sample with its adjacent
source sample, and applies the same row-first three-lookup ALP sequence above.
There is no nearest-neighbor path. This includes the authored 252×252 and
252×256 TNT minimaps when the destination lens is 126×126; no exact
`2*destination` dimension predicate is part of the gate. Confidence is
**Established** for the ratio/truncation and ALP order; the source bytes and
dimensions remain authored by the TNT format [fmt tnt].

**The baked source sub-rectangle** (`[fmt tnt "How the used sub-rectangle is
sized"]`). The source dimensions that feed this resize are not always the
baked minimap's stored `width x height`: on a non-square map, only a top-left
sub-rectangle of the stored bitmap is real terrain, and the rest of its short
axis is the format's `0x64` fill (`[fmt tnt]`). A reader that hands the full
stored dimensions to the generic resize above stretches that fill into the
visible picture, which on a markedly non-square map reads as the terrain
being shifted toward one corner with a solid band of the fill color occupying
the rest. The used sub-rectangle's size is computed from the map's
`PlayRight`/`PlayBottom` by the same long-side fit the on-screen radar
rectangle uses (the aspect rule below), substituting the stored bitmap
dimension for the 126-pixel canvas constant; the formula and its verification against five
shipped maps are in `[fmt tnt]`. Only the cropped sub-rectangle enters the
resize described above.

**Aspect and letterbox.** The play area is window width minus 32 by window
height minus 128, derived from the mode maxima, not from the raw window
dimensions. RadarW/RadarH are 1..126:

```
if playW < playH:  RadarW = playW*126/playH;  RadarH = 126;  originX = (126-RadarW)/2; originY = 0
else:              RadarW = 126;              RadarH = playH*126/playW; originX = 0; originY = (126-RadarH)/2
```

All divisions truncate; the centering halvings round toward negative infinity
for odd remainders. The minimap hit rect is inclusive: left = originX, top =
originY, right = originX + RadarW − 1, bottom = originY + RadarH − 1.

**Established.** The picture allocation is exactly RadarW × RadarH bytes plus
its descriptor header — there are no heap bytes beyond the radar rect that
could leak into the letterbox bars. The blend overwrites every destination
byte, and the surface allocator never memsets the pixel region. The bars are
therefore HUD canvas outside the radar rect, not picture heap.

**The bars are the rail's own art (Established).** Nothing fills the bars,
before or after the FINAL blit. What occupies those pixels is what the battle
presenter's **first paint** left there: it clears the whole surface to palette
index 0 once and stamps `PANELSIDE` at `(0, 0)`, and that stamp is the only one
in the image — the per-frame composer repaints the two horizontal strips and
dirty GUI windows and never the rail's columns ([R-HUD-05] "the first paint",
"what the per-frame composer repaints, and where it stops"). The stock
`PANELSIDE` raster is opaque at every pixel of its authored 129×480, the radar
area included, so the index-0 clear is never what shows through. Measured on the
reference install: over the 126×126 the radar canvas covers, `ARMINT.GAF` and
`CORINT.GAF` `PANELSIDE` each carry 15,876 opaque pixels and not one transparent
or index-0 pixel, so index 0 cannot appear in the bars at all. The art there is
the dithered near-black panel texture ([fmt gaf "Unknowns and caveats"]).

Surface descriptors hold width, height, and a pointer to the w×h pixel block;
the pixel pitch is (w+3) & ~3 (DWORD-aligned), while allocation is w×h rather
than pitch×h. Drawing clips against the descriptor bounds.

**The letterbox bars and the sampling domain (Established, [R-MM-01 §3]).**
No radar surface covers the bars. The picture, mapped and
final surfaces are all allocated at exactly the fitted `RadarW × RadarH`,
the radar rectangle words are `(padX, padY)–(padX + RadarW − 1,
padY + RadarH − 1)`, and the presenter blits FINAL at `(padX, padY)` into
the composer's frame and paints no fill. What shows in the bars is whatever
the frame holds there, and that is the **side-panel shell's own art**
(above). The generated picture's loop never leaves the tile map: `worldX = PlayRight · x / (2·RadarW)` with
`x < 2·RadarW` lies in `[0, PlayRight)`, likewise Z, so there is no
out-of-domain sample to define; the tile-index guard above is the only one.

### 3.8 Mapped composite: fog and unexplored on the minimap

Gate: mapped dirty bit clear means return; otherwise clear the bit and rebuild:

```
mapW2 = cellWidth/2;  mapH2 = cellHeight/2
for y in 0 .. h-1, x in 0 .. w-1:
    visIdx = (y*mapH2/h)*mapW2 + (x*mapW2/w)       truncating integer scale
    word = mapping word mask[visIdx]
    if word bit (1 << (localPlayer & 31)) is set:
        if local byte grid[visIdx] == 0:  out = grayTable[src]   explored but currently unseen → desaturated
        else:                             out = src             currently visible → raw picture byte
    else:
        out = fogFillIndex                                     unexplored → solid fill
    write out; advance both src and dst
```

The order is word → byte → remap: the word bit decides explored, the byte grid
decides currently seen, and explored-but-unseen cells pass through a 256-entry
index-translation table populated at palette load instead of drawing raw.
Unexplored cells take the fog-fill palette index, the same dark index the
viewport fog cache uses as its default fill. The composite shares the LOS grids
with gameplay visibility but is a distinct presentation: it never writes the
word mask, and it sets the final-dirty bit when done.

**The remap is the gray table, not a GUI remap (Established, direct
static).** The composite loads the remap pointer from the display
environment's table slot that the platform layer fills with the 256-byte
**GRAY TABLE** at environment initialization — the same allocation, the same
slot, and the same table the main-view fog overlay's gray-family span writer
applies as `dst = grayTable[dst]` for fogged-but-explored terrain (§3.3,
§4.3.3, and §4.3's "applies it to the screen for fogged-but-explored tiles").
Explored-but-unseen minimap terrain is therefore **desaturated with its texture
preserved**, exactly as the main view's fogged terrain is, and the minimap and
the viewport agree on what fog looks like.

The GUI palette resolves authored `.GUI` colour fields and is not defined over
image bytes (§4.3), so a port that feeds terrain picture indices through a
GUI→base lookup paints explored terrain in unrelated interface colours rather
than dimming it. Confidence: **Established** — the slot the composite reads is
the slot the named-block allocator writes, and no other writer touches it.

### 3.9 Contacts pass on the minimap

Layer order on the final surface (later layers overwrite; no blending):

1. wipe final from mapped;
2. regular unit blip from the FX `radlogo` GAF, drawn when the visibility
   gate passes AND the unit's per-instance blink-suppress byte reads zero OR
   the blink phase bit is set — so the blip draws when
   `blinkSuppressByte == 0 || blinkPhase`. The byte is a per-unit countdown,
   decremented each tick while nonzero, that forces the blip into blink-only
   mode while it runs;
3. hover ring from the FX `radlogohigh` GAF, frame 0, when the unit's identity
   matches the **hovered-unit word** in engine root state — the pointer
   record's unit word of `[07 R-HUD-03 §1]` source 2, rewritten every host
   frame while the pointer is inside the view with no drag armed, or over the
   minimap. It is not a commander marker and carries no commander term:
   marking every seen commander would put an enemy commander's identity on
   the minimap. **Established.** The ring obeys the same admission as the
   blip, so an unadmitted hovered unit draws nothing;
4. sensor circles (radar/sonar outer, jammer) in their distinct palette indices
   via the solid-circle rasterizer (2,048 angular steps over 32 segments);
5. weapon/interceptor rings (below);
6. projectile art, LOS-gated, selected by the firing weapon's **definition**
   flags (below): a `targetable` or `interceptor` weapon draws the FX
   `nuclogo` marker, a `noradar` weapon draws nothing at all, and every other
   weapon draws a 1×1 pixel in the projectile palette index.

The three GAF handles above are loaded from the FX archive during battle-data
initialization, from four consecutive loads in one FX initialization routine:
`radlogo`, `radlogohigh`, `nuclogo`, `h2oboom2`. The contacts pass reads the
first three; nothing in it reads `h2oboom2`, whose handle belongs to another
effect family. A regular unit's owning-player record supplies the frame selector
for `radlogo` — the byte at a fixed offset in the record the unit's owner slot
indexes — and the marker branch of layer 6 applies the same selector to
`nuclogo`. The hover ring always selects frame 0 of `radlogohigh`. The
selected frame bytes are copied as indexed pixels through the GAF blitter, so
they already refer to the active `PALETTE.PAL` and are not recolored through
`GUIPAL.PAL` or a separate blit color argument. **Established.**

The stock art corroborates the assignment exactly: `radlogo` carries ten 4×4
frames whose interiors are the ten player colours and whose border is one shared
index, `nuclogo` carries ten 7×7 frames on the same ten-slot pattern, and
`radlogohigh` carries a single 6×6 frame — one ring, which is what a
frame-0-only hover marker needs and what an owning-player selector cannot
use `[fmt gaf]`.

**Blip gate.** The unit blip draws when any of: a global options word bit 9 is
set, the minimap mode word's low two bits are zero, the unit carries the
friendly-contact status bits (mask 0x300), or the unit's owner is the viewing
player.

*Named ([R-MM-01 §3]):* the "global options word bit 9" is the
full-radar bit of the mode-flags word that the `+Radar` cheat toggles
([07 R-CAM-01 §6]) and the world rebuild clears; the "minimap mode word's
low two bits" are the render-flags word's mapping and LOS mask bits (the
`+Mapping`/`+LOS` toggles of the same section, both cleared for a watcher at
battle start [07 R-CAM-01 §14]). The weapon/interceptor rings of layer 5 sit
inside the selected-unit branch — see that anchor.

**Projection.** Each unit's 16.16 world position is first narrowed to its
signed 16-bit map-pixel components (the same narrowing contract as section
3.2), then:

```
rx = unitMapX * RadarW / PlayRight         truncating
ry = (unitMapZ - (unitMapY >> 1)) * RadarH / PlayBottom   half-height shear
```

A second pass repeats the same projection over the **projectile pool** — the
one fixed-size, fixed-capacity record array the projectile allocator fills, its
live count and base held in engine root state. The contacts pass has exactly
two loops, the ascending unit pass and this one; there is no third walk and no
feature list, so **a feature never reaches the contacts pass at all** and no
feature marker is ever drawn. **Established.** (This replaces the earlier
reading of a shared, kind-agnostic projectile/feature span.)

A candidate is admitted through the mode-selected local player visibility
source at its projected cell; when that source does not admit it, the
candidate's owner-local identity is the bypass. The two art branches take that
bypass from different places: the dot branch compares the owner slot **stored
on the record** with the viewing slot, while the marker branch follows the
record's firing-unit pointer and compares that unit's owner slot, with no null
check. **Established.**

**The art selector is three weapon-definition flags.** Each record carries a
reference to the **weapon definition** it was fired from, and the
selector reads that definition's behaviour flags word — not any per-record
status or runtime word. In the definition the weapon parser writes,
`targetable` and `interceptor` are the two high flags the selector tests
together, and `noradar` is the low flag it tests second. So: `targetable ||
interceptor` → the `nuclogo` marker branch; else `noradar` → nothing drawn;
else the 1×1 dot. **Established by analysis** (2026-09-19; the earlier
"runtime status bits 29/30/0x40" wording named the right flags in the wrong
record). `[06 §11.3]` states the `noradar` half of the same contract, and the
marker frame is the firing player's colour selector applied to `nuclogo`.
Stock reach: `targetable` on `nuclear_missile`, `crblmssl`, `armemp_weapon`
and `cortron_weapon`; `interceptor` on `amd_rocket`, `fmd_rocket`,
`armscab_weapon` and `cormabm_weapon`; `noradar` on `earthquake` alone.

**HOT list.** Every unit visited in pool (ascending-slot) order appends a
10-byte entry — id, originX + rx, originY + ry, and two pad shorts — to the
HOT RADAR list. The list allocation is 100 bytes per entry of the session
**per-player unit limit** ([05 R-SHARE-01 §7]) — not per unit definition —
so with the 10-byte stride the capacity is `limit · 10` entries, 2,500 at the
stock skirmish limit of 250. **Bounded-negative.** No capacity check precedes
the append; retail relies on the active unit count never reaching capacity by construction. A
reimplementation must enforce the cap itself rather than reproduce the
unbounded write.

**Blink.** A per-host-frame ticker decrements the countdown 7..0, reloading to
7, and toggles the blink phase bit every 8 frames. Units whose per-instance
blink-suppress byte is nonzero (a per-tick-decremented countdown) show their
blip only while the blink phase bit is set.

**Weapon/interceptor rings.** A unit-definition flags dword bit 29 enables the
ring loop, which walks the unit's three weapon slots; per slot, a
weapon-definition flags dword bit 30 admits the ring. Radius:

```
ringRadius = RadarW * (weaponRange - 512) / PlayRight     truncating
```

with the authored per-slot range reduced by the constant 512 bias before
scaling (short-range weapons can therefore produce a negative radius, which
clipping drops). When the slot's interceptor flag byte is zero the ring is
solid via the solid-circle rasterizer; otherwise it is dashed, and the
dashed-circle routine receives the same radius, the ring palette index, a
literal 32, and the blink phase bit. **Established.** Both variants use the
ring palette index. **Established.** The literal 32 is the segment count: the
dashed routine divides the full circle into `0x10000 / 32 = 0x800`-unit angular
steps and draws alternating one-segment dashes and one-segment gaps — a
segment is emitted only when `(segmentIndex + blinkPhase) & 1 == 1`, so the
dash parity is seeded by the blink phase bit and flips per segment.

**Selected-unit circle gate (Established).** In the contact pass, the
selected/range-status bit
must be set, and circles are then drawn when the unit instance is active OR
the definition's `onoffable` bit is clear. The unit parser stores `onoffable`
in definition flags bit 2 (`0x04`); it does not load a unit `noradar` key.
Thus an on/off-capable unit must be active for this selected-unit circle
branch, while a unit without `onoffable` may draw its selected-unit circles
regardless of activation state. The cloak/hidden instance bit and the
definition `stealth` flag are not this callback gate. The blip gate remains
independent: no `onoffable` or cloak test suppresses a blip once its visibility
and blink conditions pass.

**Contact layering and ring-only cases (Established).** The ascending unit
pass draws at most one regular blip and, for the hovered identity, one
additional `radlogohigh` ring; it does not draw duplicate regular blips. Circles
and weapon/interceptor rings are emitted later in that same unit iteration, so
they overwrite earlier contact pixels where opaque. There is no independent
ring-only contact list. A visible unit can appear ring-only when its regular
blip is suppressed by the per-unit blink countdown on a non-blink phase, while
the range/ring branches still run. Every admitted unit still contributes one
HOT entry after those presentation branches.

**Start-position markers.** **Bounded-negative.** No start-marker GAF and no
START string literal is found near the minimap build or contacts paths. The
logical layer between the mapped wipe and the unit blips — so that contacts
overwrite markers — is **supported inference**. `TODO(question):` the marker
blit site has not been traced.

### 3.10 Sensor circles on the minimap

The minimap's radar, sonar and jammer circles are **presentation drawn by
the contacts pass** of §3.9, layer 4, through the solid-circle rasterizer
(2,048 angular steps over 32 segments), in the distinct radar and jammer
palette indices held in engine root state. For each unit that passes the
contacts pass's gates, the radar and sonar outer circles are drawn at their
two authored distances (one circle per nonzero distance, both in the radar
index) and the two jam circles use `radardistancejam` and
`sonardistancejam` (both in the jammer index); each radius scales as `RadarW · distance / PlayRight`
(truncating), the centre is the unit's projected minimap position of §3.9,
and the circles land on the final surface, which is wiped from the mapped
composite and rebuilt every tick. Nothing in this path writes the mapping
word grid or any per-player byte grid.

The deadline-driven **sensor phase** (§3.4, `[R-SENSOR-01]`, `[R-VIS-01 §4]`,
`[R-VIS-01 §5]`) is a different thing: it runs only when more than one player
is present, walks
units through the 128-world-unit spatial grid, and writes unit status bits —
the seen marker, the sonar bit, and the jam clears — that the contacts pass
and the acquisition predicate then read. It draws nothing. The two palette
indices are colour-map entry 10 for the radar and sonar outer circles and
entry 12 for both jam circles (`[R-MM-01 §2]`).

### 3.11 Lens: minimap ↔ world mapping

World → radar: `rx = worldX * RadarW / PlayRight`, `ry = (worldZ - worldY/2) *
RadarH / PlayBottom`. Radar → world: `worldX = (radarX - originX) * PlayRight /
RadarW`, `worldZ = (radarY - originY) * PlayBottom / RadarH` — both truncating.

Click handling hit-tests the inclusive minimap rect first. The conversion of
the pointer to a **world position** is the pointer classification of step 1
of the host frame (it feeds orders and hover, [07 R-CAM-01 §1]), not a camera
write. When the click misses the rect, or the drag-mode flag is set, the
pointer's world position is the stored camera plus (the mouse clamped to the
viewport rectangle − the viewport origin). Otherwise the **lens branch**
inverts the minimap projection directly: `(mouseX − letterboxOriginX) ·
PlayRight / RadarW` and `(mouseY − letterboxOriginY) · PlayBottom / RadarH`,
truncating each operation, with no `−viewSize/2` term and no
mapWidth/mapHeight scale; it does not reuse the main view's cursor-to-world
projection. Either way the result is clamped to the play area and the
terrain height queried at the result.

The **camera** jump performed while the minimap latch is held is

```
cameraX = (ptrX − padX) · PlayRight  / RadarW − trunc(viewWidth  / 2)
cameraZ = (ptrY − padY) · PlayBottom / RadarH − trunc(viewHeight / 2)
```

(signed truncating divisions, half-viewport terms signed), so the clicked
point becomes the view **centre**. The Ctrl+right drag-scroll of the world
view moves the camera by `(trunc(Δ / 4) + trunc(prev / 16)) · 16` per axis.
[07 R-CAM-01 §11] is the owning statement; this section owns the
radar↔world scale.

### 3.12 Ground-pick crosshair, and the debug display mode (composer-time)

The ground-pick crosshair is a film-mode diagnostic of the **world**
composer, not a minimap figure:

* **It is drawn into the main backbuffer** through the clipped **game
  viewport** descriptor the composer re-establishes before its tile pass
  (§4.1) — the same surface the tile pass, the debug terrain grid and the
  unit band draw into. The `+128` and `+32` are that viewport's origin
  literals (§4.1), applied per-primitive exactly as §4.1 records for beam and
  site projection; they are not a minimap offset. The lines clip to the game
  viewport, not to the minimap rect. (The minimap's own viewport rectangle is
  a different figure with a different producer, `[R-MM-01 §1]`.)
* **The crossing point is the ground resolver's output** — the world point
  under the pointer (or, while the pointer is over the radar rect, the
  lens-projected point), as §3.11 and the ground resolver of `[07 §8]`
  produce it. The resolver writes X, Y and Z as three 16.16 words and the
  composer reads each one's high word, so the values are the integer world
  coordinates of the picked point. The half-height shear and the
  camera-scroll subtraction are then the ordinary world-to-screen projection
  of §2.1.
* **The mode byte is not a minimap byte.** Nothing about it concerns the
  radar; it selects among the composer's debug displays. See "Debug display
  mode" below.

**Established.** The figure is two 1-pixel Bresenham lines in the ring/viewport palette index — the same index the
weapon and interceptor rings use, distinct from the radar-circle and jammer
indices — drawn when the debug display mode byte equals 2, as
`(px+126, py+32) → (px+130, py+32)` and `(px+128, py+30) → (px+128, py+34)`
where `px = pickX − camX` and `py = pickZ − (pickY >> 1) − camZ`. So it is a
five-pixel cross of two one-pixel lines crossing at `(px+128, py+32)`, and the
endpoints are literals, not an inference. Thickness claims of 6/4 pixels belong
to the selection brackets, not this figure.

**Debug display mode (Established).** The mode byte has no simulation,
session, mission or map input at all: it is a **developer film-mode display
selector**, and its complete writer set is three sites.

* The **battle interface initializer** stores zero, alongside the rest of the
  interface state it clears at battle entry.
* **Film mode's `m` key** increments the byte and stores zero when the
  incremented value reaches 5. The cycle is therefore `0,1,2,3,4,0`: 4 is a
  valid mode and 5 is never observable. This is the second switch the battle
  key dispatcher runs after the first, on the same token, and only while film
  mode is on — the `m` case of the film-mode key set enumerated in
  `[07 R-CAM-01 §9]`.
* **Leaving film mode** stores zero again and clears its information toggle
  `[07 R-CAM-01 §9]`.

Film mode is reached only by F11, F11 only in developer mode, and developer mode
only through the six-word `+Now` password of `[07 R-CAM-01 §6]` or the registry
pair `DisplaymodeDepth = 256` with `Games = 1` `[07 R-CAM-01 §9]`. These
controls are disabled until explicitly activated; the password can be entered
through ordinary chat in a stock installation. In ordinary play the byte holds
zero from battle entry to battle exit and neither the crosshair above nor the
mode-specific grid below is drawn. A player who remembers a camera indicator on TA's
minimap is remembering the viewport rectangle of `[R-MM-01 §1]`, not this
figure.

#### The five displays, terrain cells and body blending

**Established (direct-static).** The display names below describe their
observed inputs; they are not retail menu labels. The terrain pass runs when
the selector is nonzero **or** contour spacing is nonzero. It runs immediately
after the TNT tile pass, using the same game-viewport clip, before features,
units and fog. The mode-2 pick cross follows the whole terrain diagnostic
pass. Within each terrain cell, mode-specific marks precede contours
[07 R-FE-02 §11]. Neither the terrain pass nor the pick cross tests the
compositor's rendering-enabled argument; the later movement overlay and film
text do [R-COMP-01 §5].

| Mode | Terrain diagnostic |
|---|---|
| 0 | No mode-specific marks. Contours still draw if their spacing is nonzero. |
| 1 | Selected unit's movement-class tier crosses, plus the session's shared path-search cells, terminal markers and direction arrows. |
| 2 | Height grid, ground occupancy or feature fills, air-occupancy crosses and building-footprint diamonds; also the ground-pick cross described above. |
| 3 | Height grid and the unsigned per-cell metal byte printed in decimal. |
| 4 | Grid and square markers wherever the local player's current-coverage byte is nonzero. |

**Cell walk and geometry.** With the ordinary clamped camera, start at
`x0 = trunc(camX / 16)`, `z0 = trunc(camZ / 16)`. Visit columns in increasing
order up to, but excluding,
`min(x0 + trunc(viewportWidth / 16) + 1, mapCellWidth − 1)`. Visit rows in
increasing order while `z < mapCellHeight − 1`. Finish each row, then stop
if every visited cell's projected northwest corner has
`screenY >= screenHeight`; otherwise advance to the next row. The stop test
uses the full screen height, while the line and text primitives still clip
against the game viewport. There is no per-cell LOS or mapped-memory gate.

For each quad, read the four height bytes in northwest, northeast,
southeast, southwest order. Project each corner independently:

```
screenX = 16 × cornerCellX − camX + 128
screenY = 16 × cornerCellZ − floor(heightByte / 2) − camZ + 32
```

The following logical colours use the logical-to-physical colour map of
§4.3; colours explicitly called **raw** bypass it. Ordinary strokes are
one-pixel clipped lines [R-COMP-01 §2].

**Mode 1: class and search.** Once at pass entry, find the first selected
local unit and its movement class. When present, read the class's packed
two-bit tier for each cell [04 R-DOC04-B]. Values 0, 1 and 2 draw both quad
diagonals, northwest-to-southeast first, in logical entries 4, 14 and 10
respectively; value 3 draws no cross. The classifier ordinarily produces
0, 1 and 3, but the painter has a defined branch for 2.

The search marks read the **single shared working set** [04 R-PATH-01 §1],
even when no unit is selected. They do not establish that the displayed
search belongs to the unit whose class crosses are shown. For each cell:

* If its acceptable-terminal flag is set, select `SMLFONT`,
  consume **one CRT random value**, use its low byte as a raw foreground
  colour, and print `G` at the northwest corner. The draw consumes the value
  before text clipping, so even a clipped marker advances this stream.
* Remove only the acceptable-terminal flag from the status byte. If the
  result is neither 0 nor 3, draw a direction arrow. Exact original status
  1 selects logical entry 15; exact original status 2 selects entry 4.
  **Unknown:** a stable colour for other admitted statuses. The painter
  does not assign a colour on those branches: a previous cell's colour or
  uninitialized scratch can be used. A particular frame's stack history
  would settle its pixels; assigning all open or terminal cells one colour
  would be a new tooling policy, not an established retail rule.

For the arrow, let `C = northwest + (8, 8)` and let the cell's direction
index choose `D` from
`[(0,−1), (−1,−1), (−1,0), (−1,1), (0,1), (1,1), (1,0), (1,−1)]`.
Draw `C − 14 × D[d]` to `C`, then `C − 4 × D[(d+1) mod 8]` to `C`, then
`C − 4 × D[(d−1) mod 8]` to `C`. These screen-space arrows are anchored
to the northwest corner's height; their tips are not a separately sampled
terrain point. Search state meanings and producer bounds remain owned by
[04 R-PATH-01 §1].

**Mode 2: terrain and occupancy.** Draw the north and west edges first,
in logical entry 15 when the northwest height is **strictly greater** than
sea level, otherwise entry 13. Then, in order:

1. If the ground-occupant identity is nonzero, flat-fill the projected quad
   with its low byte as a raw colour. Otherwise, if the feature word differs
   from the no-feature sentinel, flat-fill with
   `(featureWord − 56) modulo 256`, also raw. This tests the stored word
   directly; it does not resolve a feature definition, and special feature
   sentinels other than no-feature take this branch too. The fill follows
   the edges and can overwrite their pixels.
2. If the air-occupant identity is nonzero, draw the two diagonals in its
   low byte as a raw colour.
3. If the completed-building yard flag is set, draw an inset diamond in logical
   entry 15. Its vertices are the north edge midpoint plus `(0,2)`, east
   midpoint plus `(−2,0)`, south midpoint plus `(0,−2)`, and west midpoint
   plus `(2,0)`, joined cyclically in that order. Each midpoint coordinate
   uses signed division of the endpoint sum by two, truncated toward zero.

Plot-cell identities and flags are those of §2.2; the diagnostic does not
perform a collision or placement query of its own.

**Mode 3: metal.** Draw the same north/west height grid as mode 2, then
select `SMLFONT` and logical foreground 15. Print the cell's unsigned metal
byte at `northwest + (2,2)`, in base ten. It is the
raw source byte, before the extractor's `+1` and multiplier (§2.2).

**Mode 4: coverage.** Draw north/west edges in logical entry 0. Read the
local player's coverage byte at `(trunc(x/2), trunc(z/2))`. If nonzero,
fill an inclusive rectangle from `northwest − (5,5)` to
`northwest + (5,5)` in logical entry 15: an 11-by-11-pixel square. This is
the current-coverage reference-count grid (§3.1), sampled directly;
the painter does not combine it with sensor contacts or unit visibility.
Four neighboring attribute-cell anchors can therefore display the same
coarser coverage cell.

**Unit bodies remain present.** Every nonzero mode forces the unit image
commit through the **tinted** blitter, the same family used by cloak
[R-RAST-01 §7], in both composition-image branches. Mode zero uses the
ordinary keyed body blit when the unit is not cloaked. The alternate branch
still draws the body. This selector alone neither skips the unit list nor suppresses its shadows,
features, effects or the later fog pass. No general model wireframe mode
is established by these five displays.

Nanolathe keeps the byte as an explicit authoritative session input
(`Session.DebugDisplayMode`) with the two established producers — a reset to
zero and the `0..4` cycle — and publishes it unchanged through the frame. It
implements neither developer nor film mode, so nothing calls the cycle and the
value is zero throughout, which is what retail does too. The debug crosshair of
this section takes no minimap argument; the minimap's own viewport rectangle is
a different figure with a different producer — `[R-MM-01 §1]` immediately
below.

#### The minimap viewport rectangle, and the two sensor-circle colours [R-MM-01]

##### R-MM-01 §1 — the minimap *does* carry a viewport rectangle

Retail draws a camera viewport rectangle on the minimap in every ordinary
battle frame. It is unrelated to the film-mode crosshair of §3.12, and it is
not on the FINAL surface (§3.6); the layering is below.

**Established — the producer.** The rectangle is not drawn by the contacts pass
and is not part of the FINAL surface. The battle HUD composer, once per host
frame, calls a small minimap-presentation routine that does exactly two things
when the FINAL-dirty bit is set (clearing it first): it copies the FINAL radar
surface onto the HUD's own destination surface at the canvas letterbox origin
`(padX, padY)`, and then strokes the camera-to-radar rectangle's outline onto
that same destination. So the rectangle lives on the destination surface, above
a FINAL that never contains it — which is why it survives the per-tick FINAL
wipe and why a save-restored FINAL carries no marker. FINAL is *mapped wipe →
blips → hover ring → sensor circles → weapon rings →
projectile dots and markers*, and nothing else (§3.6).

**Established — the rectangle.** The rectangle is the **camera-to-radar
rectangle**, a four-integer inclusive record recomputed by the per-axis camera
clamp — the same clamp `[07 R-CAM-01 §10]` and `[07 §10]` describe, which runs
after every camera write. It is the world→radar projection of the *game
viewport*, in canvas coordinates (the letterbox origin is already folded in):

```
left   = padX + trunc(RadarW · cameraX  / PlayRight)
top    = padY + trunc(RadarH · cameraZ  / PlayBottom)
right  = left - 1 + trunc(RadarW · viewWidth  / PlayRight)
bottom = top  - 1 + trunc(RadarH · viewHeight / PlayBottom)
```

where `cameraX`/`cameraZ` are the camera origin in map pixels (retail's origin
is the world point at the game viewport's **top-left**, `[03 §4.1]`),
`viewWidth`/`viewHeight` are the game viewport's extents in map pixels — held
as a cell count and shifted left by 4, and numerically the `W-128 × H-64` of
`[03 §4.1]` — and `PlayRight`/`PlayBottom` are the play-area extents of
`[03 §3.4]`. Every division is a signed truncating divide. There is **no**
half-height shear on `top`: this is a camera origin, not a unit position.

**Established — the stroke and the colour.** The figure is a one-pixel
**outline**, never a fill: four clipped one-pixel Bresenham segments joining the
four inclusive corners, through the same rectangle-outline primitive the rest of
the interface uses. Clipping is to the destination surface, not to the radar
rect; in the ordinary domain the clamped camera keeps the rectangle inside the
radar rect anyway, because `cameraX ≤ PlayRight − viewWidth`. The colour is
**colour-map entry 14** — logical entry 14 of the logical→physical map of
`[03 §4.3]`, whose GUIPAL source is `(255,255,85)` and which resolves in the
stock install to physical index 194, `(247,227,103)`. That is the pale yellow a
retail screen shot shows around the visible area, and it is a different entry
from the crosshair's entry 15 (white) and from the drag rectangle's 6/4/15
family `[07 §6]`.

Note that entry 14 is shared with the minimap's 1×1 projectile dot (§3.9 layer
6); the two figures simply use the same colour-map slot.

##### R-MM-01 §2 — the sensor-circle colours are entries 10 and 12

**Established.** The contacts pass reads the same colour-map block as everything else in §3.9/§3.10 and takes:

| Circle | Colour-map entry | GUIPAL source | Stock physical |
|---|---|---|---|
| radar outer | 10 | `(85,255,85)` | 233 `(83,223,79)` |
| sonar outer | 10 | as above | as above |
| radar jam | 12 | `(255,85,85)` | 211 `(255,71,0)` |
| sonar jam | 12 | as above | as above |
| weapon / interceptor ring | 15 | `(255,255,255)` | 255 white |

So the bright green ring a retail screen shot shows around a radar-carrying
unit's revealed area is the **radar coverage circle** of §3.10, not an outline
of the LOS-revealed region: nothing in retail traces the revealed region's
boundary. The radar and sonar outer circles are two separate circles at the two
authored distances, both in entry 10 (§3.10); the visible result is identical
to a single circle at the larger distance whenever one dominates, which is
the stock case.

##### R-MM-01 §3 — the blip gate's two words, the ring gate, the bars, and the picture's domain

**Established — the two gate words.** In the contacts pass (§3.9) a live
unit is admitted when any of: the mode-flags word's **full-radar bit** (bit
9 — the `+Radar` cheat of [07 R-CAM-01 §6], cleared by the world rebuild
[08 R-ENTRY-01 §3]); the render-flags word's **mapping and LOS mask bits
both clear** (the `+Mapping`/`+LOS` toggles; the world-rebuild tail clears
both for a watcher slot [07 R-CAM-01 §14]); the unit's status word has
either friendly-contact bit (`0x300`); or the unit's owner is the viewing
slot. There is no separate per-unit "visible" term — visibility enters only
through those bits. Rule:
`admit = radarCheat || (!mappingMask && !losMask) || status & 0x300 != 0 || owner == viewer`.
Until a `+` command vocabulary exists the first term reads false and the
masks read true, so the gate reduces to the last two terms.

**Established — rings are gated on selection.** Within one unit's
iteration: blip; hover ring; then, **only if the unit's
selected bit (status bit 4) is set**: the four sensor circles when the
instance is active or the definition lacks `onoffable` (the selected-unit
circle gate of §3.9), and then — independently of that inner test but still
under the selected bit — the weapon/interceptor ring loop when definition
word A bit 29 is set. A detected enemy is never ringed; §3.9's layer 5 is
the *selected* unit's rings.

**Established — the bars and the domain.** See the closure at the end of
§3.7: the three radar surfaces are `RadarW × RadarH`, the presenter paints
no bar fill, and the generated-picture loop samples only inside the play
area. Bar content: the rail's own `PANELSIDE` art, **Established** (§3.7).

## 4. Indexed renderer, palettes, and asset layers

### 4.1 GDI windowed backend

The windowed path obtains a window DC, creates a compatible memory DC, and
creates a top-down 8-bit `CreateDIBSection`. Rows use a four-byte-aligned stride
`(width + 3) & ~3`. The software renderer writes indexed pixels to the DIB
backbuffer.

For presentation, it obtains the window DC, calls `SelectPalette` and
`RealizePalette`, copies the memory DC with `BitBlt` using `SRCCOPY`, and
releases the DC. Palette installation creates a 256-entry Windows logical
palette and calls `SetDIBColorTable`.

Negotiated surface width and height are set by a dedicated size setter. The
surface builder uses byte pitch `(w+3)&~3` and a top-down DIB height of `-h`.
On mode changes, the visible game viewport is rebuilt with inclusive bounds
`left=128`, `top=32`, `right=W-1`, `bottom=H-33`, giving width `W-128` and
height `H-64`. A separate hidden-panel expansion writer was not established
in the bounded search; this remains `TODO(T23)`.

The composer copies the surface description and re-establishes its clip
rectangle before drawing tiles. Tiles use the viewport's left and top as
their destination origin. Beam and site projection separately add 128 and 32;
those equal the visible viewport origin at 640×480 but are independent inputs.

| Mode (W×H) | Viewport `left,top,right,bottom` incl | Viewport `W×H` | Pitch `(W+3)&~3` | Status |
|---|---|---|---|---|
| 640×480 | 128,32,639,447 | 512×416 (`W-128 × H-64`) | 640 | Established (direct-static) |
| 800×600 | 128,32,799,567 | 672×536 | 800 | Established |
| 1024×768 | 128,32,1023,735 | 896×704 | 1024 | Established |
| any×any hidden | predicted 0,0,W-1,H-1 → W×H if hidden expanded, else 128,32,W-1,H-33 if retained | — | — | `TODO(T23)` (bounded-negative, no writer) |

### 4.2 DirectDraw fullscreen backend

The fullscreen path calls `DirectDrawCreate`, sets cooperative level on the game
window, requests the configured width/height at 8 bits per pixel, creates a
primary/backbuffer surface, attaches/installs a palette, composites into the
surface, and presents with DirectDraw surface blit/flip calls. The observed
surface descriptor is the legacy 108-byte form with primary/backbuffer flags;
exact flag naming remains medium-confidence, but lost-surface recovery is
documented: the surface blit/copy wrappers retry on the lost-surface error
through a restore callback held in the surface record, so a lost surface is
restored and the blit replayed rather than dropped.

The GDI DIBSection descriptor (`w,h,pitch,bits`) and a primary lock descriptor
mirroring the OFFSCREEN surface record are held in presentation state; GDI
present clones the descriptor through one routine, DirectDraw fullscreen locks
through the surface vtable's lock entry and verifies the descriptor's negotiated
width/height match before presenting (direct-static for the clone path,
bounded-negative for a second viewport-subrect writer; hidden-panel paths
remain `TODO(T23)`).

Both paths share the indexed software framebuffer and serialize present work
through the renderer’s global MAIN lock. There is no observed 16/24/32-bit
fallback in the retail renderer.

### 4.3 Palette tables and color indirection

`PALETTE.PAL` is 1,024 bytes: 256 entries of four bytes (RGB plus zero
reserved byte) and is the shared active display palette for the indexed
renderer, including GUI/HUD surfaces. `GUIPAL.PAL` is an authored frontend
source palette used to build a GUI-to-display lookup; it is not installed as
the physical UI palette. The auxiliary tables are:

- `ALP`: 65,536 bytes, a 256-by-256 nearest-color blend table;
- `LHT`: 8,192 bytes, 32 rows by 256 entries for brightening;
- `SHD`: 8,192 bytes, 32 rows by 256 entries for shading/darkening.

The renderer installs two further 256-entry tables beside those three, in this
order: a **gray table** (§4.3.3) built at palette-install time, and a **blue
table** read only by the submerged-hull tint of [R-REN-03A §8]. Five slots,
sized 65,536 / 8,192 / 8,192 / 256 / 256. `ALP` has a second consumer besides
the minimap: the structure anti-alias downscale reads it three times per output
pixel ([R-REN-03A §6]).

No table has a header; identification is by size alone. Each of `LHT` and
`SHD` is loaded as a raw 8192-byte block and is retained for the life of the
session. Only `PALETTE.PAL` participates in the `LOGPALETTE`/`CreatePalette`/
`SetDIBColorTable` install; `ALP`/`LHT`/`SHD` never enter the OS palette and
are consulted only by the indexed software renderer via a single byte lookup.

The palette install helper copies RGB entries, creates a `LOGPALETTE` with
version 0x300 and 256 entries, and installs either DirectDraw palette entries
or the GDI palette/DIB color table. A 256-byte logical-to-physical lookup maps
TDF/UI indices into the live `PALETTE.PAL` display palette. During GUI
bootstrap, `GUIPAL.PAL` is copied into a separate window record and its RGB
entries are compared with all 256 display entries using the sum of absolute
channel differences; the first entry on a tie wins. That map resolves GUI
semantic color fields before GUI primitives or FNT glyphs are written. It is
not applied to image bytes: the retail GAF blitter copies opaque frame bytes
directly, and PCX/TNT bytes are likewise already indexed for `PALETTE.PAL`.
Every final indexed pixel is resolved to RGB only at present time through
`PALETTE.PAL`.

#### 4.3.1 LHT brightening

**Layout and formula.** `LHT` is 32 rows × 256 columns, row stride 256:

```
result = LHT[level * 256 + index]      level 0 .. 31 clamped, index 0 .. 255
```

`index` is the source palette index after the logical-to-physical map;
`result` is again a palette index. The engine evaluates a single byte load —
no channel arithmetic and no interpolation between rows.

**Table shape (established, measured against the retail tables).**

* Row 0 is near-identity: 242 of 256 entries map to themselves; the 14
  exceptions are isolated duplicates near palette gaps. Mean luminance delta
  is effectively ±0.0 at row 0 and rises monotonically to +51.51 at row 31.
  White (255) maps to white and black (0) maps to black at every level.
* The mean-luminance trend increases, but the nearest-color remap does not
  guarantee nondecreasing luminance for every source index. Each row remaps
  the brightened RGB target through the palette search rather than adding
  `level*step` to the source index. The top rows collapse many sources onto the
  same bright band (row 31 maps sources 1–6 onto 249–254, the bright
  orange/yellow band).

**When it is used (established).** `LHT` drives exactly one presentation
family — the lit-ground halo drawn around an explosion. The engine precomputes a small square flash texture (`N×N` plus a
24-byte header) whose bytes encode intensity — the inner core varies around
index `0x6F` minus a jittered radial distance, a thin ring is exactly `0x6E`,
and outside the disc the byte is `0xFF` transparent — then for each screen
pixel where that texture is opaque the underlying indexed pixel `src` is
replaced by `LHT[level * 256 + src]` at a level derived from the disc
intensity. Its whole-tick countdown cadence is shared with the explosion
animation, and the disc itself is seeded from the CRT presentation random
stream (`*214013+2531011`), not the simulation stream. The state it draws
from is the **loading thread's own** CRT block, created fresh for battle entry
and seeded from the time of day before the world rebuild ([08 R-ENTRY-01 §2],
[01 R-PLAT-01 §4]), so the noise pattern differs between two retail battles by
design and follows from no authored datum; a clone's choice of seed is
presentation-only, and the shape — the ramp, the ellipse, the up-to-nine
thirty-seconds jitter — is the whole contract (Established).

**Where it composites.** The disc is the explosion pool's **secondary**
cursor, and that pool's draw entry — both of its walks, the secondary through
the flash blitter and then the primary art — is invoked from the world-draw
sequence between the projectile renderer and strip 7, which is stage 7 of
§1's ten-stage order and therefore **after** every unit traversal. Read
directly from the composer's own call sequence: strips 0–2, the feature
pass, strips 3–4, the grounded unit pass, strip 5, strip 6, the projectile
draw, the explosion pool, strip 7 (`[06 R-WFX-01 §2]`). There is **no
muzzle-time sprite producer in any weapon path** (`[06 R-WFX-01 §4]`) — the
only muzzle-time presentation is the `startsmoke` puff, the `soundstart`
sound, and whatever the unit's `Fire*` script emits — so a halo at a muzzle
arrives through one of those, not through a producer of its own.

**The first-walk blitter brightens; the row is `src − 0x4F` (Established).**
The first-walk blitter is its own routine, gated on the display's
light-table-loaded bit, and **both** of its span writers — the raw one and
the run-length one — replace each destination byte under an opaque source
byte with `LHT[(src − 0x4F)·256 + dst]`, exactly as [R-FX-01 §4] states. The
disc is a brightening of what is under it; with no light table loaded the
blitter draws nothing, not an opaque disc. The row is `discByte − 0x4F = 32 −
q` for the ramp bytes `q = 1..32` and `31` for the ring byte, needing no
clamp because the ramp `0x4F..0x6E` is exactly rows `0..31`. The effect
remains presentation-only, not authoritative, not hashed, and not
save/loaded.

**Established (bounded asset measurement):** `LHT` serves the brightening
family; it is not a per-cell luminance guarantee. In the installed
`totala1.hpi` tables, 16 of 8,192 cells have a negative delta under
`0.299R + 0.587G + 0.114B`; for example `LHT[3][161] = 66` gives
−10.764. This measures that weighted RGB metric, not perceived brightness,
and does not constrain modded tables. `SHD` rows 0–14 provide the darkening
ramp. The `discByte→level` mapping is established (direct-static for the byte thresholds): the disc canvas is filled per pixel with one CRT draw each, `R = trunc(CRT*10/0x8000)` in `0..9`, radial distance `sqrt(dx*dx + 1.33*dy*dy)`, `q = trunc(((R + sqrt) / H) * 32.0)`, then the stored byte is `0x6F − q` while `(0x20 − q) mod 256 < 0x20`, `0x6E` while that byte-compare lies in `0x20..0x21`, and `0xFF` from `0x22` up — the 0xFF band is what makes the disc's outer area transparent, the visible region being the band where `q mod 256` lies in `0..31`. The halo level is `discByte − 0x4F = 32 − q` (`31` for the ring byte) bright centre to rim, and the disc is drawn to screen as radial spokes from angle `0x800` through `0x10000` in steps of `0x800` through the sin/cos helper pair (the same angular step as the minimap circle rasterizers).

**The ellipticity axis and the division.** The generator's own arithmetic
([06 R-WFX-01 §2]): the **row** term carries
the factor — for row `y` and column `x` of an `n`-sided frame with `H = n/2`,
`A = (H − y)² · 1.33` and the distance is `sqrt((H − x)² + A)` — and the sum
**is** divided by `H` before the ×32. So the disc is an ellipse compressed
**vertically** by `sqrt(1.33)`, reaching about 15% further horizontally than
vertically, and the `/H` is what makes every frame of every table span the same
number of ramp steps regardless of its side. Measured against a rendered
impact: the brightened region is 49×43 pixels, a ratio of 1.140 against
`sqrt(1.33) = 1.153`. **Established:** the generated shrinking frames and their whole-tick
countdown provide the complete explosion-flash envelope. Allocation binds the
secondary cursor, ticking advances it, and drawing submits its current frame
directly to LHT; no additional age-based opacity or intensity factor is applied.
Completion clears the cursor, and the pool retains the record only while a
cursor or debris piece is live [R-FX-01 §4]. Thus the 32-row/256-column layout, the near-identity row 0, the +51.51 bright end, and the exclusive flash binding are direct. The two brightening ramps overlap: `LHT` row 3 and `SHD` row 16
both lift mean luminance by +6.83, `LHT` row 5 and `SHD` row 17 both by +12.60,
but the files are distinct and neither is synthesized from the other.

#### 4.3.2 SHD shading

Indexed texture pixels may be passed through an `SHD` row for model face
lighting; flat-colored 3DO primitives take the same row through the shaded
flat writer in the shaded renderer and bypass `SHD` in the unshaded one
([R-REN-03A §5]); laser lines bypass `SHD`. Team/logo
textures select the player-specific frame before palette/shading lookup. `SHD`
rows 0–14 darken (row 0 near-black, only index 0 survives; mean −97.59),
row 15 is near-identity (232 of 256 self, mean +0.17), and rows 16–31 brighten
past identity to +53.53 at row 31 — a full signed ramp that `LHT` does not
replicate. The `SHD` row selection is established (direct-static):
the cleared render-piece shade bit (`DONT_SHADE`) pins row `15`, else
`row = trunc(dot*5.0) & 0x1F` with
`L=(-0.8,1,0.25)`, wrapping negatives; the row interpolates gouraud-style as
`(rowR-rowL)/width`, sampling
`SHD[row*256+texel]` per pixel on textured faces and `SHD[row*256+color]` on
flat ones. All of that lives in the shaded piece renderer, which retail reaches
only for a `BMcode=0` unit with the `Shading` display option on ([R-RND-02A]);
the renderer every other unit takes maps textured faces with no `SHD` step. Identity row `15` and the 32-row layout are direct; the `1=NW`
corner→bit mapping of §3.3 is supported inference.

#### 4.3.3 Gray table construction [R-RR16-A §1]

**Established.** The gray table is a 256-entry shared block ("GRAY TABLE",
alongside the 8,192-byte "SHADE TABLE" and "LIGHT TABLE" sibling blocks) filled
by the palette-install builder:

- For each palette index `i` with RGB `(r,g,b)` (1024-byte PALETTE.PAL,
  stride 4): `avg = (r+g+b)/3` truncated toward zero.
- Target color is `(avg,avg,avg)`. The builder uses the sum-sorted nearest-color
  search described in §4.3.4 and [R-WATER-01 §2]; ties preserve the earliest
  sorted position, not necessarily the lowest original palette index.
- The result is stored as `grayTable[i]`.

Net effect: fogged-but-explored tiles show their terrain desaturated through
the nearest gray palette entries; texture is preserved (see §3.3 [R-RR16-A]).

#### 4.3.4 Missing palette and derived-table recovery [R-PAL-RECOVERY §1]

**Established.** Missing or empty PAL files recover through the corresponding
PCX, as specified by [02 R-MALF-01 §9]. That recovery invalidates all three
derived tables. Independently, each missing or empty ALP, LHT or SHD invokes
its own builder; a nonempty file is loaded instead. These are load-time tables
with no RNG consumption.

All three builders use the same nearest-color search as gray/blue generation.
Initialize a palette-index permutation and RGB sums, then for each position
compare every later position and exchange both arrays on a strictly smaller
sum. Equal sums are not directly exchanged, but intervening swaps can change
their order. Scan this permutation, skipping sums below target sum minus 40
and stopping above target sum plus 40. Keep only strictly smaller squared RGB
distance. If no candidate qualifies, use the scan's exit position modulo 256.
Return the original palette index at the selected permutation position.

- **ALP:** for distinct source indices, target each channel at the integer
  floor of their channel sum divided by two. A diagonal entry preserves its
  own index directly, even when the palette contains duplicate colors.
- **LHT:** for each row 0 through 31, first multiply the row by the stored
  binary64 constant `-0.03333333333333333`, then subtract that product from
  binary64 `1.0`. Each operation rounds separately under the default precision
  [01 §8]. Multiply each source byte by the stored factor, truncate toward
  zero, and clamp results greater than 255 before the nearest-color search.
- **SHD:** start a stored binary64 factor at zero. Compute each channel by
  multiplication, truncation toward zero, and a clamp when the unsigned low
  word exceeds 255. After each row, subtract the stored binary64 constant
  `-0.06875` and store the next factor. Do not replace these successive rounded
  updates with multiplication by the row. In these 32 rows every channel
  product is nonnegative and below 544, so the word comparison is equivalent
  to clamping a nonnegative integer at 255.

The builders ignore each palette entry's reserved fourth byte. Their output
is separate from the policy of trusting an existing authored table: a shipped
or modded table need not be regenerated merely because a builder exists.

### 4.4 GAF sprites and animation

The GAF loader keeps entries by case-insensitive name and allocates a frame
canvas with a small header plus `width * height` pixels. A frame may be raw or
RLE-compressed. RLE rows use a command bit for transparent skip, a repeat-byte
command, and a literal-copy command. Palette index zero is not inherently
transparent; transparency comes from skip commands.

Subframes are placed at their own signed offsets from the parent frame's pen
and composited in order, clipped against the destination surface and not
against the parent's dimensions ([R-COMP-01 §2]), with later opaque pixels
overwriting earlier pixels.
The loader wires named entries for smoke, fire, explosions, water/lava impacts,
shadows, cursors, victory/defeat/pause art, logos, and GUI panels. Optional
entry lookup failure leaves the relevant visual absent rather than inventing a
replacement.

A GAF frame reference is eight bytes: a frame-header offset and a 32-bit
authored duration in whole simulation ticks (or in scaled wall-clock units for
cursor playback). A playback cursor is a 12-byte non-owning view over shared
sequence data: current frame index, countdown, loop-or-hold flag, and entry
pointer. It does not free sequence storage on termination. Binding a cursor
clamps an out-of-range start index to zero, loads that frame's authored
duration into the countdown, and copies the sequence's loop byte. Each
simulation-tick step decrements the countdown; when the countdown is below two
it advances to the next frame, wraps to zero for looping sequences or clears the
entry pointer for non-looping sequences, and loads the new frame's duration.
Multiple simulation ticks in one present advance the cursor multiple times;
a pause with no logical ticks freezes it. A separate delta step subtracts a
signed 16-bit tick delta and can cross multiple frames in one invocation while
accumulating each newly selected frame's duration. Single-frame entries never
advance. The registered sequence players for model textures are stepped once per
simulation tick. Cursor sequences are stepped from a 30-unit-per-second scaled
wall-clock delta and accumulate authoring countdowns in those scaled units. The
bounded producer that drives model-texture players is the per-tick walker that
single-steps every registered model-texture player; the bounded cursor driver is
the wall-clock delta path that subtracts the scaled delta via the multi-frame
countdown stepper. Multi-frame model textures receive a cursor in each
qualifying loaded primitive; runtime instances referencing that same loaded
model share its texture phase [R-CRD-005 §1].

#### CRD-005 closure — phase-7 model-texture sequence traversal [R-CRD-005 §1]

**Established — ownership and binding lifetime.** The session's global registry
contains playback cursors embedded in qualifying primitives of **loaded shared
models**. The unit-definition loader resolves and binds the model before any
unit instance uses it. Unit creation allocates separate transform/script state
and mutable vertex arrays while retaining references to the loaded pieces; it
does not clone the primitive texture cursors or register new ones. The earlier
per-unit cursor wording was incorrect.

Consequently two instances of one unit definition share that definition's
loaded primitive phase, regardless of creation time, visibility or draw
history. Two primitives naming the same sequence have distinct cursors, and
two unit definitions retain separate cursors even when their object names
match. Projectile records instead reuse the first earlier compiled weapon
record whose model name matches case-insensitively; only a first occurrence
loads and binds a model, so later matching records share its cursors. A feature
definition with an object name loads its own model when admitted to the battle
feature-definition table; unused discovered definitions do not bind models.
At a normal battle entry, weapon models bind first, then the terrain file's
feature-name table and map-specified feature additions admit feature records,
then the unit-definition loop admits a missing `corpse` feature immediately
before binding that unit model. The subsequent feature-link pass walks the
admitted table by increasing ordinal with a live count. For every visited
record it resolves `featuredead`, then `featurereclamate`, then `featureburnt`;
each missing target is appended and is visited later in that same pass. A cycle
or repeated name therefore admits one record, not a recursive duplicate. Save
restoration adds its saved feature records and runs its own link pass only after
those records are admitted. The feature-model and projectile-model loaders use
the same texture binder; those 3D texture cursors belong to this registry too.
Their separate sprite/event cursors are different objects with different phase
owners. **Established (direct-static).**

Each player stores its frame, remaining authored duration, loop/hold flag and
non-owning sequence reference. Fewer than two frames bind statically; a
team-colour entry resolved from the LOGOS bank is selected at draw time and is
not registered [R-RAST-01 §3]. Other qualifying primitives register at model
load/bind, before the first possible visible composition. The binder visits
its sibling subtree, child subtree, then the current piece's primitives in
ascending authored order. Registration appends in that load/bind order.

The registry is advanced exactly once at phase 7 of every runnable simulation
sub-tick, after phase 6 feature work and before phase 8 wind work [01 §4.4].
Phase 7 performs no visibility test, pixel write, wall-clock conversion, or
authoritative RNG draw. It advances only cursor metadata; the presentation
layer resolves the selected frame later. **Established.**

At the beginning of one phase-7 invocation, the walker captures the current
registry count `N`. It then visits exactly the entries at indices `N-1` through
`0`, in descending index order, once each. The captured count is not reread
inside the loop. Therefore a registration that becomes visible after the count
is captured cannot participate until the next phase-7 invocation, while the
already captured entries retain their order. This is a count snapshot, not map
iteration and not a work queue. **Established.**

Each visited player uses the simulation-tick step, not the cursor driver's
scaled-delta step. If its sequence is absent it is inactive and unchanged. With
remaining duration at least two, the step subtracts one and keeps the current
frame. With remaining duration below two (the strict predicate `remaining < 2`),
it advances the frame, wraps to frame zero when looping is enabled, or clears
the sequence reference when a non-looping sequence has ended; a selected frame
reloads its authored duration. The next frame is consequently observable on
the presentation following that simulation tick. A one-frame entry is never a
phase-7 player. **Established.**

**Established — teardown.** Destroying a unit releases its instance transform
state and mutable vertex arrays and purges its composition-cache entries
[R-COMP-02 §3]. It does not destroy the shared model's primitive cursors, so
there is no per-unit registry removal, tombstone or compaction to invent. The
texture-system teardown releases the registry as a whole and resets its count;
the next battle's texture loading initializes an empty registry. The previously
recorded per-unit teardown Unknown resulted from attributing shared primitive
state to the unit instance. This closure is bounded to the recovered unit,
feature and projectile model loaders, unit-state constructors/destructor,
registry append/advance and texture-system initialization/teardown paths.

The phase-7 consumer census is consequently narrow. Model-texture players are
the only global registry consumers. Feature-definition and feature-instance
normal/shadow cursors are stepped by phase 6; fixed effect cursors are stepped
by their phase-4 effect records; projectile visual cursors are stepped by the
projectile phase; and interface/cursor sequences use the scaled wall-clock
delta path. They share cursor arithmetic but do not share the phase-7 registry
or its ordering. **Established within the recovered caller census.**

The following probes are the minimum edge coverage for a phase-7 implementation
without importing presentation details:

| Probe | Required observation |
|---|---|
| Empty registry | No callback, frame change, or RNG draw. |
| One player at countdown 2 | The first invocation decrements to 1; the next invocation sees `remaining < 2`, advances, and reloads. |
| Looping final frame | A below-two countdown selects frame zero and loads its duration. |
| Non-looping final frame | A below-two countdown clears the sequence; later invocations do not advance it. |
| Two players | The newer entry is visited before the older entry (descending index order). |
| Append after count capture | The appended player is not visited in that invocation and is first eligible on the next one. |
| Unit destruction or player termination | Registry entries persist until whole-system teardown; unit destruction does not remove them, and a terminated player remains registered but inactive. |
| Five one-tick pumps versus one five-tick pump | Equal positions only when the registered population is unchanged; a player created between sub-ticks starts at its first eligible phase-7 boundary, not retroactively. |

These probes also separate the phase-7 strict countdown predicate from the
wall-clock cursor path's signed-delta and multi-frame loop. **Established.**

#### Named GAF banks and slots, the fog fade families, and the cursor bank [R-FX-01 §1]

**Established (direct-static; reader census over the whole decompile
export).** The startup binder of §4.4 ("wires named entries") loads five
banks through the animation-bank cache (a bank name
resolves to `anims\<name>.gaf`; a bank that fails to load ends the process
with a message box — [06 R-WFX-01 §1]) and looks up fixed entry names in
each. A name that is absent leaves a null slot; the binder additionally clears
the loop byte of the explosion-art entries so they play once. The banks, in
binding order, and every reader found:

| Bank | Entries bound (in order) | Readers |
|---|---|---|
| `fx` | `smoke 1`, `smoke 2`, `fire1`, `alfboom1`, `radlogo`, `radlogohigh`, `nuclogo`, `h2oboom2`, `lavasplash`, `cannonshell`, `plasmasm`, `plasmamd`, `ultrashell`, `plasmasm` (bound twice: render-type-4 selector slots 1 and 4), `flamestream`, `explosion`, `explode2`..`explode5`, `nuke1`, `shadow` | itemized in [06 R-WFX-01 §1]; `fire1` and `alfboom1` are bound and read by nothing (dead bindings); `radlogo`/`radlogohigh`/`nuclogo` are HUD (doc 07) |
| `igtitles` | `igvictory`, `igdefeat`, `igpaused` | the end-of-battle and pause overlays (doc 07) |
| `vismasks` | `vismask` | the coverage-mask draw family of §3.3 (frame index = the cached nibble, clamped to the entry's last frame) |
| `fog` | `Black1`..`Black4`, `Gray1`..`Gray4` | the fog overlay composer only (below) |
| `cursors` | twenty-one entries, listed in [R-FX-01 §5] | the cursor setter and the order-queue icon draw ([R-FX-01 §5]) |

The bank pointers and every slot are cleared by the session teardown; no
other code writes them.

**The eight "fade strips" are the fog tile families.** `Black1`..`Black4` and
`Gray1`..`Gray4` (stock `fog.gaf`: 14 frames each — quarter-tile 16×16 frames
whose placement offsets select the tile corner, then 32×32 full-tile frames;
every hold word 10; raw, not RLE) are the two four-way variant families §3.3
describes without naming them. The fog overlay draws frame `value − 1` of
`Gray<1 + ((cellX + cellY + cameraPhaseSum) & 3)>` for the current-sight
channel, then the same-indexed `Black` entry for the history channel — the
Gray slot base plus the variant times the slot stride, and the Black slot base
likewise. **No other reader exists**: not the shadow pass, not cloak, not the
nanolathe (which has no fade at all, [R-P0-19-P]), not any palette fade;
every other candidate use is bounded-negative over the whole export.
**Established.**

#### The frame blitter family and the raster primitives [R-COMP-01 §2]

GAF sprite, panel, fog, cursor and scratch-image composition uses the blitter
shell and primitives below. Direct model-texture and visibility-mask raster
readers have separate contracts [02 R-MALF-01 §6]. This section is
the implementer's contract for that layer; byte offsets inside the frame
header are `[fmt gaf]`'s. **Established (direct-static)** unless marked.

**The frame record as the blitters read it.** Width, height, a signed
placement-offset pair, the transparent-key byte, a compressed flag, a
sub-frame count, an alternate-blitter flag (meaningful on sub-frames), and
either a pixel pointer or a table of sub-frame pointers.

**The shell.** Destination origin `(dx, dy) = (penX − xOffset, penY −
yOffset)`; source rectangle `[0, 0, w−1, h−1]`; destination rectangle
`[dx, dy, dx+w−1, dy+h−1]`. The target image carries its own **clip
rectangle** (four inclusive edges; one helper resets it to the whole image,
another sets it — the composer sets it to the viewport rectangle before the
world passes), and the shell clips by moving *both* rectangles together:

```
d = dst.left − clip.left  ; if d < 0 : src.left −= d ; dst.left −= d
d = dst.right − clip.right; if d > 0 : src.right −= d; dst.right −= d
(top and bottom likewise)
if dst.left > dst.right or dst.top > dst.bottom or src empty : draw nothing
```

A null target means *the screen*: the screen surface is locked for the call
and unlocked afterwards. A frame with a nonzero sub-frame count draws no
pixels of its own: each sub-frame is drawn in table order with the same pen,
through the selected consumer. **Ordinary keyed composition** sends a child
whose alternate-blitter flag is set through the **tinted** blitter
([R-REN-03D §4]); tinted composition propagates tint to every child. Gray and
dithered fog instead recurse through every child using their own operation and
raw-frame gate, without testing that selector. Nonzero-mode glyph composition
sends all children through tint ([R-FONT-01 §6]); flash preserves LHT
([R-FX-01 §4]). **Established corpus observation:** no alternate child was
found in the 958-GAF survey `[fmt gaf]`; that sample does not forbid authored
alternate children.

**The five span writers.** After the shell:

| Family | Uncompressed frame | Compressed frame | Gate |
|---|---|---|---|
| opaque (keyed) | copy every source byte `≠ key` | RLE decode: rows above the clip skipped by their row-length prefix, left/right clipping applied while decoding; grammar per `[fmt gaf]` (bit 0 set → transparent run of `byte >> 1`; bit 1 set → repeat the next byte `(byte >> 2) + 1` times; else `(byte >> 2) + 1` literal bytes) | none |
| raw | whole rectangle copied, no key test (dword-wise when the width is a multiple of four, word-wise when even, else byte-wise) | RLE decode as above | none; used only by the tile pass ([R-COMP-01 §1]) |
| gray (fog, §3.3) | `if src ≠ key : dst = grayTable[dst]` | **nothing is drawn** | gray table present |
| dithered gray (§3.3) | x steps by two from parity `(y + dx + parity) & 1`; `if src ≠ key : dst = 0` | nothing is drawn | none |
| tinted | `if src ≠ key : dst = ALP[src × 256 + dst]` ([R-REN-03D §4]) | tinted RLE variant | window alpha-blend capability, enabled at startup independently of `Shading` ([R-REN-03D §4]) |

The gray-family gate closes a corner of §3.3: a fog cloud frame that is
authored compressed, or a palette whose gray table failed to build, draws
**no** current-fog cloud (the black history family still draws), rather than
falling back to any constant colour. The same gate guards the gray
rectangle remap used for a fully-current-fogged cell.

**Lines.** The line entry clips first, against the target's clip rectangle,
parametrically — endpoints are moved along the line with truncating integer
division in the fixed order *x0 < left, y0 < top, x0 > right, y0 > bottom, x1 <
left, y1 < top, x1 > right, y1 > bottom*, rejecting when the line runs away
from the violated edge or has no extent along that axis. **Established
(direct-static):** all eight adjustments use the original endpoint differences
and direction tests. Moving an endpoint does not recompute the slope for the
remaining edges. The line is then clipped a second time
against the surface extent `[0, w−1] × [0, h−1]` with 64-bit products. The
drawer then takes a vertical run (`|dy| + 1` pixels) when `dx = 0`; otherwise
orders the endpoints so `x` increases, takes a horizontal run (`dx + 1`
pixels) when `dy = 0`, and otherwise runs Bresenham with `major = max(|dx|,
|dy|)`, `minor = min(|dx|, |dy|)`, `e = 2 × minor − major` and `major + 1`
pixels:

```
write pixel
if e < 0 : e += 2 × minor              ; step along the major axis only
else     : e += 2 × (minor − major)    ; step along both axes
```

Both endpoints are painted; the colour byte is written raw (callers pass the
logical-to-physical map entry they want). A **rectangle outline** is four
such lines in the order top `(x1,y1)–(x2,y1)`, right `(x2,y1)–(x2,y2)`,
bottom `(x1,y2)–(x2,y2)`, left `(x1,y1)–(x1,y2)`.

**Rectangles and copies.** The rectangle clipper rejects on any inclusive
edge test (`right < clip.left`, `left > clip.right`, `bottom < clip.top`,
`top > clip.bottom`), clamps, and re-checks `left ≤ right` and `top ≤
bottom`; the solid fill, gray remap and dithered fills of [R-P0-19-P] and
§3.3 follow it. Image-to-image rectangle copies with an offset clip against
both images' extents and copy dword-wise when the destination pointer and
the width are four-aligned; the 32 × 32 block copy of the tile pass is
unclipped. Two more helpers: an **inclusive** rectangle-overlap test (`b.right
≥ a.left ∧ a.right ≥ b.left ∧ b.bottom ≥ a.top ∧ b.top ≤ a.bottom`) used by the
gadget repaint, and a "pen = authored offset + delta" wrapper used by the
chrome and HUD stamps, which exists so the shell's offset subtraction cancels
and the art lands at the delta (the contract [07 §6] states for the panels).

**The RLE encoder** that [R-REN-03D §5] uses on the structure-shadow sprite
emits the same grammar: literal runs of at most 64 bytes (`(n−1) << 2`), repeat
runs of at most 64 (`((n−1) << 2) | 2` and the byte), transparent runs of at
most 127 (`(n << 1) | 1`); a repeat is only recognised from three equal bytes
onward.

**Not reachable (DEAD, reference census).** An XOR line drawer with its XOR
rectangle, two float-scaled (magnifying) keyed/tinted frame blitters, and a
named-region registry have no callers anywhere in the image; an
implementation does not need them.

#### Circle rasterizers, the rectangle shader, image-record wrappers, and screen redirection [R-COMP-02 §5]

**Established (direct-static)** unless marked. These are the raster
helpers [R-COMP-01 §2] listed by name but did not spell out.

**The sin/cos helper pair.** These are not private raster helpers: they are
the shared simulation trig component routines that [04 R-MOV-01 §4] owns and
[06 §3.3] restates, called from roughly fifty sites across the image,
of which the minimap circles and the flash disc spokes below are only two
families. Read the arithmetic there rather than here; the only facts this
section needs are that the *sine* helper of a `uint16` angle `a` and radius
`r` reads the shared 8192-scaled sine table at **word index
`((a + 0x20) >> 7) & 0x1ff`** — one of 512 steps of 128 angle units — and
returns `(t × r + 0x1000) >> 13` as a 64-bit product with an arithmetic
shift, and that the *cosine* helper is the same with `a + 0x4020`, a quarter
turn (128 entries) ahead. The `+0x20` biases each entry boundary a **quarter
step** early; it is not round-to-nearest, which would be `+0x40`. The
`+0x1000` in the tail *is* round-to-nearest at the table's 8192 scale.
The addressable circle is 512 entries ([R-WIND-01]); the table object
physically carries 640 signed words, entries 512..639 repeating 0..127 so
that a second, radius-less accessor pair can address a full circle from a
base of entry 128 without running off the end. That second pair applies no
`+0x20` bias — its word index is simply `a >> 7`, and it returns the raw
table entry — and no section of this document describes its callers.

**The solid circle** (centre `(cx, cy)`, radius `r`, colour byte, target
image or null = screen) draws **32 chords**: starting from `p₀ = (cx + r,
cy)`, for `k = 1 … 32` it computes `pₖ = (cx + cos(k × 0x800) × r, cy +
sin(k × 0x800) × r)` with the helpers above and draws the clipped line
`pₖ₋₁ → pₖ` through the line entry of [R-COMP-01 §2]. **The dashed circle**
takes a segment count `n` and a phase word: `step = trunc(0x10000 / n)`
(64-bit division; `n = 0` faults), angles `step, 2·step, …` while `≤ 0x10000`
(so `n` chords, or `n + 1` when `0x10000` is not a multiple of `step`), the
same chord construction, and the chord for segment index `i` (counting from
0 at the first chord) is drawn iff `(i + phase) & 1 == 1` — exactly the
[03 §3.10] parity rule. Neither routine paints the last chord back to `p₀`
separately; with 32 steps of `0x800` the final angle is `0x10000 ≡ 0`, so
the ring closes on `p₀` itself.

**The rectangle shader** (target or null = screen; rectangle or null = the
whole surface; a signed level). Level `< 0` selects the **darken** table and
uses row `level + 32` after clamping the level to `≥ −32`; level `≥ 0`
selects the **lighten** table at row `min(level, 31)`. If the selected table
is absent the call returns 0 and draws nothing. Otherwise, over the clipped
rectangle (the inclusive clipper of [R-COMP-01 §2]) every destination byte is
replaced by `table[row × 256 + byte]` in place. The two tables are the
`PALETTE.SHD` block (rows 0–31, [03 §4.3.2]) and the light table of
[R-FONT-01 §6]; the caller census is the campaign report state 3 and the
camera fade ([08 R-CAMP-01 §6]).

**The 64-bit scale helper** the line clipper's second pass uses computes
`(a × b) / c` with a 64-bit product and truncating division and returns **0
when `c` is 0** — a zero-extent clip axis never faults.

**Image-record wrappers.** Besides the DIB descriptor of [03 §4.4] and the
scratch images of [R-COMP-01 §4], a third builder turns a **GAF frame header**
into a 12-dword drawable image: width and height from the frame, pitch =
width, the frame's pixel pointer, the constant words `10000` and `−1`, the
frame's signed placement-offset pair, flags `|= 1, &= ~2`, then the common
finaliser sets the clip rectangle to `[0, 0, w−1, h−1]`. It is how the
generated minimap picture and the nanolathe scratch frames become blit
targets. The **screen-redirection** setter stores an image record pointer in
the display window and raises its redirect flag; while the flag is set, every
null-target draw "locks the screen" by copying that record instead of
touching the DIB or DirectDraw surface, and the corresponding unlock is a
no-op. The front-end screens and the battle composer alike redirect
null-target drawing to their back image this way. The **current-cursor-frame accessor** returns
the frame header the cursor layer of [R-FX-01 §5] is showing; the gadget
driver compares it to decide whether a widget's cursor request changed.

**The minimap repaint pre-pass** the composer runs right before it sets the
viewport clip: when bit 1 of the minimap dirty word is set, clear it, copy the
FINAL radar surface (an unclipped image-to-image copy, no key) to the canvas
letterbox origin held in engine state, then draw the **camera-to-radar
rectangle**'s outline (four clipped one-pixel lines, [R-COMP-01 §2]) in logical
entry 14 of the logical-to-physical map. The film-mode crosshair of [03 §3.12]
is a separate world-composer figure. Which events set bit 1 is [03 §3.6]'s
lifecycle; the pre-pass itself is only the consumer.

The rectangle's four inclusive corners are the camera origin and the game
viewport's extents projected through the radar lens, recomputed by the
per-axis camera clamp; it is the minimap's viewport indicator, this pre-pass
is its only producer, and `[R-MM-01 §1]` is the owning statement.

#### The two standalone unshaded-renderer entries: debris pieces and effect/projectile models [R-COMP-02 §6]

**Established** (direct-static). The primitive dispatch and span
writers are [R-REN-03A §5]; [R-RND-02A] names the unshaded renderer; this
section states the two entries that reach it outside the unit path.

Both entries take a model piece and a world position triple (16.16), project
every vertex to the screen — `sx = high16(X + vx) + 128`, `sy = high16(Z −
vz) − (high16(Y + vy) >> 1) + 32`, each `high16` a truncating shift after the
32-bit add — into a scratch point array, and then walk the piece's
primitives **from index 1 when the piece has a selection primitive, else
from 0** (the primitive count and the "first primitive is the selection
box" marker come from the model piece). Per 32-byte primitive: gather its
vertex indices into the point array; if the coloured flag is set, the flat
polygon filler; else if the vertex count is exactly 4, resolve the texture
(the resolve-at-draw-time bit → the current frame of the cursor stored in that
same primitive record of the loaded model — see "Which cursor a detached piece
reads" below; the **debris** entry additionally honours the team bit by taking
the `LOGOS` frame selected by the owning player's colour index) and run the
textured quad mapper; else draw nothing.

**Established (direct-static).** The model binder initializes every
multi-frame entry cursor at frame zero. Only after primary texture-bank lookup
fails does it search the fallback LOGOS bank; an exactly-ten-frame entry found
there becomes team-colour and is excluded from the advancement registry. A
ten-frame primary-bank entry remains an ordinary registered animation. The
effect/projectile entry has no team-colour branch, so for a **team** entry —
which is bound but never registered, and therefore never advanced — its
ordinary resolver reads that still-initial frame zero; it neither creates a
hole nor borrows an owning player's colour. For an ordinary registered
animation the same resolver reads the advancing frame, as the next paragraph
states.

**Which cursor a detached piece reads (Established, direct-static).** There is
exactly **one** playback cursor per *(loaded model, piece, primitive)*, and it
lives inside the loaded model's own primitive record — the same record the
piece's colour index, vertex indices and flags live in ([R-COMP-02 §4]). No
per-unit, per-debris, per-projectile or per-call copy of it exists anywhere.
Both standalone entries are handed the **loaded model piece itself** — a debris
record names the loaded piece it was thrown from, and the effect entry is
likewise given a loaded model's piece — and they reach the frame through the
identical read the unit composition renderer performs, the same accessor on the
same primitive record. What is per-instance is only the *point workspace* —
the debris record rebuilds its own rotated copy from the loaded piece's original
vertices — never the cursor.

Consequently a piece **detached** from a unit, whether by death debris or by a
standalone effect draw, shares the living unit's cursor and shows the same
animation frame at the same time; it does not restart, rest at frame zero, or
run on a cadence of its own. The phase-7 walk reinforces this: the registry is
populated at **model load** ([R-COMP-02 §4]), one append per multi-frame
non-team primitive of each loaded model, and the walk steps every registered
cursor with no reference to instances, visibility, ownership, or whether
anything is drawing that model at all. A cursor nothing currently owns still
advances. The one way a cursor stops is the advance's own detach: when a
non-looping sequence runs off its last frame the advance clears the cursor's
entry link, after which every reader — unit, debris and effect alike — resolves
nothing for that primitive ([06 R-WFX-01 §1]).

Two definitions that name the same `3DO` do **not** share a cursor: each model
bind loads a fresh instance of the file (the authored half-turn is applied to
that instance once) and then registers that instance's own cursors, so cursor
identity follows the *load*, not the file name (Established, direct-static).

The **effect/projectile entry** (3DO projectiles and the pooled explosion
models) first rotates each vertex by the object's three angle words through
the rotation helper of [03 §2.4] — about the Z axis by the first word, then
the X axis by the third, then the Y axis by the second — into the shared
rotated-point scratch, then projects. The **debris entry** ([04 R-COB-04 §2])
likewise rebuilds its detached point workspace from the model's original
vertices using the debris record's current angle triple, then projects at the
record's position. It is gated on the projected origin lying inside the
viewport rectangle (inclusive point-in-rectangle test); the fixed-effect draw
pass applies the same test before calling the effect entry. Neither entry reads
`SHD`, the height key, or the composition image; both write straight to the
target image.

**Consequently a projectile or effect model has no key plane and never reaches
the structure anti-alias gate (Established, direct-static).** The fillers these entries call are the **framebuffer** variants
of [R-RAST-01 §1], which carry no key plane and resolve overlapping primitives
in painter order — the primitives' stored order within the piece — and the
supersampled vertex path of [R-REN-03A §6] is reachable only from the unit
composition renderer, which neither entry is or calls. A clone that composes a
projectile model into a one-plane image and blits it produces the same painter
order at the same pixels. Each call also draws **one piece**: render type 1 of
§5.4 passes the definition's model and, under its flag and deadline, a second
piece taken from the model header's child slot as the "secondary model"
(**Established, direct-static:** the loader relocates that header child link,
and the renderer reads that same direct child without following its sibling
link). The standalone call rotates the selected piece's raw vertices and adds
the projectile world point; it does not compose the selected object's authored
translation or any parent translation.
**Established asset census (reference install, 2026-09-07).** The render-type
1/6 projectile records name 19 distinct 3DOs. Six roots have a header child;
one, `advtorpedo`, has more than one direct child, and no non-root piece has a
child. Retail's bounded parent-plus-first-child dispatch therefore leaves the
additional `advtorpedo` direct child undisplayed; no stock projectile reaches a
grandchild through this entry. The census is repeated by the asset-gated
`TestRetailProjectileModelHeaderChildCensus` check.

#### The composition memory cache and its composite surface, the strip pool's growth, and the container `finished` query [R-COMP-02 §7]

**Established (direct-static)** except where marked.

**The composition memory cache — creation and size.** The catalog compiler
([02 R-CAT-01 §5] step 2) creates the cache after the movement classes are
read: a 20-byte cache object (capacity, block pointer, first-span pointer,
composite-surface pointer) is allocated and reset, then initialised with a
size computed as follows, in this order:

1. `a = trunc(double(W × H × 2) × 1.3)` — `W`, `H` the display width and
   height ([07 R-FE-02 §2]'s 640×480 in the battle), the product and doubling
   in 32-bit integer arithmetic, the multiply in double precision, the
   result truncated toward zero;
2. `n = trunc(v / 2^20) + 1`, where `v` is a word of the terrain record and
   the division truncates toward zero (the sign-corrected arithmetic shift);
3. `f = 1.0` when `n ≤ 16`; otherwise `f = n × 0.0625` when that product is
   `≤ 5.0`, else `f = 5.0` — so `f = min(n / 16, 5)` for every map deeper
   than the threshold;
4. `size = trunc(a × f)` rounded **up** to a multiple of 4096 (add 4095 and
   clear the low twelve bits).

The initialiser frees any previous block, allocates `size` bytes under the
tag `CMemoryCache`, records `size` as the capacity, points both the block and
the first-span pointer at it, and writes one free-span header at the start of
the block — a zero word followed by the capacity — so the whole block begins
as a single free span; the records [R-COMP-02 §3]'s purge walks are carved
from it. It then allocates the `CompositeBuffer` surface: an indexed image of
**600 × 600** — a 24-byte header (width and height words, zeroed offset and
flag words) followed by two contiguous `w × h` planes, the body plane and
then the key plane, with the header holding a pointer to each — the staging
image of [R-REN-03A §4]. Failure of either allocation leaves the compiler's
return unread; retail does not check it. **Supported inference:** `v` is the
map's height in 16.16 world units, making `n` the map height in 16-unit
footprint cells plus one (the reading of the catalog trail); decider: the
terrain loader's writer of that word. The purge-time record format and the
two-key match are [R-COMP-02 §3]'s and are not restated.

**The strip pool's constructor and growth** ([R-FX-02 §1], the "vector grown
by the pool's own allocator"). The constructor stores the low byte of the
requested slot count, clears the block vector and the slot array, installs
the pool's table, and — only when both the count `n` and the slot size `m`
are non-zero — grows to `n` slots of `m` bytes. Growth is a no-op returning
success when `n` does not exceed the current count. Otherwise it reallocates
the slot-pointer array to `n` entries, allocates **one** block of
`(n − old) × m` bytes, fills entries `old … n − 1` with successive `m`-byte
offsets into that block, appends the block pointer to the block vector (the
doubling rule of the shared vector helpers), and records `n` and `m`. Either
allocation failing returns 0 with the pool unchanged in count. The static
initialiser that builds the pool and its slot count is
[01 R-PLAT-02 §1]'s.

**The container `finished` query.** The effect base class of [R-FX-02 §3]
carries a virtual entry returning `deadline ≤ currentTick` (unsigned,
**inclusive**). The removal rule the family bodies apply is [R-FX-01 §3]'s
strict `expiry < tick`; because the call is virtual, which bodies consult
this base entry (and on which side of the tick increment) is not traced —
**Unknown**, decider: a caller census of the table slot.

**Cited, not restated.** The presentation-surface (re)creation family (GDI
DIB or DirectDraw path, the lost-surface restore, the surface-lock event) is
[03 §4.1] and [03 §4.2]; the surface clear (one index over the whole surface,
a DirectDraw colour fill when the surface is a device surface) and the
surface descriptor initialiser (size, pitch, clip rectangle, owned bit) are
[R-COMP-01 §2] and [03 §4.4]'s descriptor rules. Nanolathe's presentation is
Ebitengine (`docs/INVARIANTS.md` I6); the contract those sections carry is the 640×480
enforcement and the lost-surface replay, not the DirectDraw calls.

## 5. World render passes and object presentation

### 5.1 Terrain and features

The tile pass fetches and clips indexed 32-by-32 tiles. Feature plot cells then
provide anchors, height, and footprint metadata for feature sprites and
shadows. This section is the consolidated retail contract for feature
presentation: definitions, placement sources, composer staging, screen
anchoring, the visibility/memory/fog gates, animation, and shadows. The
authoritative feature lifecycle — reclaim, capture, burning, sinking, economy
yields — is owned by document 05 ("Feature reclaim", "Feature catalog and
placement", "Feature burning").

#### 5.1.1 Definitions and asset resolution

Feature definitions are authored in TDF under `features/<group>/*.tdf`, one
section per type. Discovery is recursive; the retail install ships 17 groups:
`acid`, `all worlds`, `archi`, `corpses`, `crystal`, `desert`, `green`, `ice`,
`lava`, `lush`, `mars`, `metal`, `moon`, `slate`, `urban`, `water`,
`wetdesert`. Every file contributes one catalog entry per top-level section;
compiled records sit at a fixed 256-byte stride with a fixed field vocabulary
(the Feature record, `[02 "Feature record"]`). Linking of successor hops
`featuredead`, `featurereclamate`, and `featureburnt` runs as a second pass
after all sections are parsed; a missing link stores the sentinel `0xFFFF` and
ends the chain rather than substituting. Definitions are compiled once and
never mutated thereafter except for late resolution of names to indices.

Keys that matter for rendering: `object` vs `filename` (mutually exclusive
asset selector), `seqname` / `seqnameshad` (idle sprite/shadow), the
`seqnameburn` family, `seqnamedie`, the `seqnamereclamate` families,
`animating`, `animtrans`, `shadtrans`, `height`, `footprintx`/`footprintz`,
`nodrawundergray`, `sparktime`, `burnweapon`, `flamable`, plus the placement
vocabulary (`blocking`, `reclaimable`, `autoreclaimable`, `indestructible`,
`geothermal`, `spreadchance`, `reproduce`, `reproducearea`,
`metal`/`energy`/`damage`).

Two visual families exist:

- **Sprite / GAF class** — indicated by a `filename` string with no `object`.
  The loader resolves `filename` through the GAF set alias (for example
  `trees`, `rocks`, `greenvents` / `geotherm`) and then resolves each named
  sequence (`seqname`, `seqnameshad`, the `seqnameburn` family, and so on) to
  a handle that identifies the entry. When `animating` is set the handle is
  copied into a per-instance cursor store and advanced each sim tick;
  otherwise the handle is used statically. All stock metal deposits and all
  trees/shrubs on Great Divide are of this family.
- **3DO / model class** — indicated by an `object` name with no `filename`.
  The loader resolves the name through the object directory. At draw time the
  shared FeatureUnit placeholder is filled with the feature's model pointer
  and world position and dispatched through the 3DO model path. None of Great
  Divide's initially-placed features use this path; corpses and later wrecks
  do.

A missing **sprite** asset does not crash: when a feature definition's main
GAF handle is null the per-cell dispatch returns without drawing, and a null
shadow handle skips only the shadow. The **3DO class behaves differently**:
when an `object` name fails to resolve under the object directory — missing
and unreadable are the same failure — the feature definition loader raises a
system-modal message box naming the path and terminates the process. No
build, debug or quiet predicate guards that path; it is the image's generic
fatal-asset handler, shared with dozens of other load sites. The dispatcher is
therefore never reached with a null model, and its 3DO branch has no
null-model test: it stores the pointer into the shared placeholder and
dereferences it immediately. Stock GAFs are valid, and stock `object` names
resolve; only a malformed install exercises either path.

#### 5.1.2 Placement sources and the single stamper

Retail funnels every feature placement through a single stamping service. It
validates the anchor rectangle against map bounds (`anchorX + footX ≤ mapW`,
and the same for Z), checks footprint collision by attempting a conditional
teardown of any overlapping occupant (guarded by `indestructible`), stamps the
anchor `feature = featureId` and the fringe cells `0xFFFE` with their
anchor-offset bytes (§2.2), and notifies derived occupancy. That service is
the only writer of the plotted footprint; the invoking paths are responsible for ordering. Both the
stamping service and the footprint teardown helper it uses for collision end
by restamping every named movement class over the footprint rectangle —
[R-LAYER §2] below.

Four sources feed the stamper:

- **TNT tile attributes** — the map file's attribute grid supplies height and a
  feature reference per cell. The four-byte on-disk cell (`height:u8`,
  `feature:u16`, `unk:u8`) expands into the runtime 13-byte plot cell with the
  feature word at offset 8 and sentinels `0xFFFF` empty, `0xFFFE` fringe,
  `0xFFFD` void hole, and `< 0xFFFB` live index. Fringe offsets are signed
  8-bit deltas scaled by map width where required. On **Great Divide** this is
  the dominant source: 2,552 real cells out of 40,960 attribute cells, drawn
  from a 17-entry feature table.
- **OTA mission features** — `Number of Normal / Animating / 3D Features`
  partitions are stamped after the type-name indirection `[Feature Type Names]`
  maps OTA-local indices to global catalog indices. Great Divide carries none;
  campaign maps exercise this path.
- **Unit-death corpses** — the dying unit's `Corpse=` FBI link followed by the
  `featuredead` chain (depth derived from kill severity) is stamped at the
  victim's cell. The placement is subject to the same bounds/collision checks
  as map features and fails silently when blocked.
- **Burn / reclaim / damage successors** — a live feature transitions
  atomically to its successor (`featureburnt` after a fire finishes,
  `featuredead` after lethal weapon damage, `featurereclamate` after
  successful reclaim). The transition is removal followed by re-stamping at
  the same anchor, preserving world position when the predecessor had an
  animation slot. Sinking wrecks pass their submerged descent velocity to the
  successor.

**Reproduction** uses a global cursor descending one cell per sim tick. The
decrement happens **before** the range test and the wrap returns immediately,
so the tick that resets the cursor to `mapW*mapH-1` visits no cell at all and
the cell at that index is never scanned; the sweep covers `mapW*mapH-2 … 0`
and then spends one tick wrapping. A visited cell consumes one simulation-RNG
draw **only** when its feature word is a live index (`< 0xFFFB`) **and** its
plot cell carries no animation-instance record; empty, fringe, void and
already-instanced cells consume none. On that qualifying cell the draw is
taken **before** the comparison against the definition's `reproduce` byte, so
a `reproduce=0` definition still spends it. Document 05 owns the walk, the two
further offset draws a passing roll spends, and the target arithmetic
([05 "Feature reproduction"], [05 R-FEAT-01 §12]). Stock maps are inert
because every shipped definition authors `reproduce=0` — inert as placement,
not as RNG.

#### R-LAYER §2 — feature changes restamp every movement class directly; they bypass the request revision pass

**Established (direct-static, bounded caller census).** The movement-class
layers of [04 §6.1 R-DOC04-B] — the packed 2-bit per-cell stamps, one heap per
named class — are restamped at every feature change **synchronously inside the
feature services themselves**, not through the 30-tick request revision pass.
One shared "restamp every named class over a rectangle" service does the work:
it iterates the movement-class records, skips the unnamed ones, and for each
re-runs the rectangle form of the per-cell passability classifier over the
rectangle, repacking the 2-bit values with the same local neighbor-demotion
rule the map-load stamp applies ([04 §6.1 R-DOC04-B] classifier and contagion).
The rectangle is the feature's anchor cell and footprint extents (the feature
definition's footprint pair).

**Call-site census — exactly five non-load callers share that service:**

1. The **single stamping service** of §5.1.2, after it writes the anchor
   feature word and fringe deltas — so a newly placed feature blocks its
   footprint cells in every class layer in the same call that stamps the plot
   cells. Map load's per-cell stamps route through the same service.
2. The **footprint teardown helper** (used by the stamping service's collision
   check, by the burn/reclaim/damage successor transitions, and by the
   feature-maintenance walker's reproduce/fire teardowns): it first clears the
   footprint's plot cells (feature word back to empty, flags bit cleared,
   fringe reset) and *then* restamps — so removal unblocks the cells in the
   same call, and every successor transition (`featureburnt`,
   `featuredead`, `featurereclamate`) is a teardown-plus-restamp followed by a
   fresh placement stamp.
3. The **plot-occupancy stamp's completion path**: after the per-cell
   occupancy loop it restamps the committed unit's footprint rectangle (rect
   words built from the unit's cached cell and footprint bias).
4./5. The two **building-commit/clear validation sites** in the movement
   commit region, which restamp over the footprint rectangle the same way.

So: feature creation, destruction, and reclaim each restamp all named class
layers twice where a successor is involved (teardown, then placement) —
always at change time, in the calling phase.

**Relation to the request revision pass.** The revision pass of [04 §6.1
R-DOC04-B] (watermark `max(tick,30) − 30`, run at path-request init) walks the
**unit** pool only: alive units whose last occupancy-commit tick falls in the
previous watermark window get their footprint rectangles restamped, and the
requesting unit's commit tick is refreshed. Feature changes never route
through it — the occupant-age machinery exists for mobile occupants, and the
direct restamps above are how feature blocking enters and leaves the class
layers. Unit-side building commits likewise restamp directly (sites 3–5), so
the revision pass is purely the mobile-occupant aging channel.

#### 5.1.3 Composer staging and pool membership

The frame composer stages through ten fixed-order barriers (section 1). Feature
work sits outside the strip abstraction:

- Strips `0..2` (unconditionally) and strips `3..4` (unconditionally) flank a
  feature pass that owns the unexplored-marker state. That pass loops the plot
  row-major, clears the never-seen marker, and dispatches visible features
  through the per-cell dispatcher. A second, interleaved pass walks the
  window's world-Z plot rows mixing the units whose mode mirror is `1`
  (grounded movers, landed aircraft and every structure) with the deferred
  tall features; the units whose mirror is not `1` are drawn in a later pass
  after projectiles — [R-RAST-01 §6], [R-RAST-01 §7].
- The ten-strip strip objects are confined to the strip dispatcher and capped
  per strip; the fixed 300-record effect pool sits between strips 6 and 7
  behind the same mode gate. Features are not strip objects and are not
  subject to the 401-record eviction rule; their fixed-slot pool is capped at
  2,048 (`0x800`) entries and allocated through a dedicated freelist.
- Fog is staged last, after all strips, projectiles, and effects but before
  selection and interface, so fog tints world drawing including features but
  never the selection rectangle.

#### 5.1.4 Screen anchor, footprint centering, and height averaging

A feature's screen anchor is derived from its footprint center plus its cell
origin:

```
screenX = footX*16/2 + (cellX + 8)*16 - cameraX
screenY = footZ*16/2 - (h0+h1+h2+h3)/8 + (cellZ + 2)*16 - cameraZ
```

Heights `h0..h3` are the terrain height bytes of the covered cells (current
cell, next-X, next-Z, diagonal). The `>>3` average of the four height bytes is
the mean height halved — the same half-height shear the unit path applies as
`worldY >> 1`, at map-pixel scale. The `+8` on X and `+2` on Z fold in the
viewport left and top offsets (128 and 32, §2.5, §4.1). Footprint
centering (`foot*16/2`) puts a `3×3` metal deposit centered over its anchor
rather than straddled on it; `1×1` trees are pinned to their anchor cell. The
GAF frame's own `xOff`/`yOff` are then applied and clipped to an inclusive
rectangle; fully off-screen features are culled whole.

#### 5.1.5 Visibility, memory, and fog interaction

Per-frame visibility for features is a predicate-consensus:

- Short features (`height < 10`) are tested against memory/fog. Features that
  do not carry the `nodrawundergray` flag draw unconditionally — the gate has
  no explored or fog term ([R-RAST-01 §6-A]); the fog overlay is what hides
  them. Features that do (Great Divide has none of these on its native table, but
  wall/fort types do) draw only when the plot's placer nibble equals the local
  player slot or when the two-corner LOS test passes — first corner at the
  sheared cell origin, then a single corner displaced by the footprint offsets.
  The placer nibble is stamped as `(placer & 0xF) << 3`, with a nibble of 10
  for map-authored features and the owning player's slot for corpses, so
  owned wrecks are remembered. The memory accept is keyed to the placer
  nibble and applies only to features whose definition carries
  `nodrawundergray`.
- Tall features (`height >= 10`) force the never-seen marker — bit 2 (value
  `0x04`) of the plot cell's flag byte at offset 12 — regardless of LOS,
  casting a persistent unexplored shadow over their own cells. Their drawing
  still passes through the same memory-or-LOS gate.

These mechanics are the ones section 3.3 adjudicates for the flag byte
(live-instance bit 0, authored blocker bit 1, never-seen bit 2, placer nibble
bits 3..6); the consolidated contract adds the height threshold of 10 and the
`nodrawundergray` gating of the memory accept. The draw-versus-marker order is:
the feature pass **clears** the never-seen marker per cell before evaluating; a
skipped hidden feature leaves it set; tall features re-mark their own cells
unconditionally; then drawing is decided by the memory-or-LOS gate.

Fog presentation afterwards draws a hard 32-pixel overlay from the visibility
grid, leaving unexplored cells dark (section 3.3). The feature passes themselves
are the authors of the per-cell never-seen bit.

#### 5.1.6 Animation and the burning lifecycle

- **Static (`animating=0`)** — normal and shadow are the first frame of their
  respective sequences. No cursor is stepped. This matches every tree/shrub
  and metal rock on Great Divide.
- **Animated (`animating=1`)** — the loader pre-wires two 12-byte cursors
  (normal and shadow) from the idle sequences and the global tick advances
  both via the single-step helper each sim tick. Geothermal vents on Great
  Divide are of this family and therefore cycle independently of view. Ship
  burned-death and reclaim sequences are forced non-looping at load so the
  shipped 46–282-tick finite lifetimes are honored.
- **Burning** — ignition selects the burn and burn-shadow sequences with a
  one-shot countdown derived from `sparktime`. The burn animation is advanced
  every tick until its cursor clears, at which point the cell is cleared and
  the `featureburnt` successor is stamped if one exists. The countdown is the
  established one-shot `simulationRandom(sparktime / 2) + (sparktime / 2)`
  (one Park-Miller draw of `sparktime/2` plus `sparktime/2`), owned by
  document 05's fire contract (`[05 "Feature burning"]`, mirrored in `[06]`
  weapon firestarter handling).
- **Clipping and transparency** — GAF drawing composes subframes in table
  order at the same pen, each at its own signed offsets, clipped **only**
  against the destination surface's clip rectangle ([R-COMP-01 §2]); the
  parent frame's dimensions bound nothing, so a child may extend beyond the
  parent's nominal rectangle unless the destination is itself a parent-sized
  raster. Later opaque pixels overwrite earlier ones; skip commands are the
  sole transparency mechanism. Shadow drawing may select the translucent
  blitter when `shadtrans` is set.

#### 5.1.7 Great Divide reference world

Header `0x2000` canonical, 160×256 cells (2,560×4,096 map pixels), sea level
45, 2,820 tiles, 17-entry table. OTA is schema-only; no added features. Table:
six tree variants (`Tree1..Tree6`), three shrubs (`Shrub1..3`), two pre-burnt
trees (`Tree1Dead`, `Tree2Dead`), four metal deposits (`RockMetal`,
`RockMetal1..3`), one geothermal vent (`Geothermal`), one hurt rock fragment
(`Rock1a`, exercising a 4×3 footprint). Definitions live in `features/green/`
(green-world). No initially-placed feature carries a 3DO `object`; all are
sprite-class (`filename` present) except later corpses.

#### The tile pass, exactly [R-COMP-01 §1]

**Established (direct-static).** Inputs: the camera origin `(camX, camZ)` in
world pixels; the viewport origin `(vpLeft, vpTop)` and size `(W, H)` of
§4.1; the tile-index grid (16-bit indices, row stride `cellWidth / 2`, §2.2)
and the tile set (`index × 1024` bytes per 32 × 32 tile). The composer has
already set the target's clip rectangle to the viewport.

```
tx0 = floorDiv(camX, 32) ; rx = camX − 32·tx0          // residue 0..31, floor for negative cameras
tz0 = floorDiv(camZ, 32) ; rz = camZ − 32·tz0
nx  = floorDiv(W + rx, 32) ; remX = (W + rx) − 32·nx ; if remX ≠ 0 : nx += 1
nz  = floorDiv(H + rz, 32) ; remZ = (H + rz) − 32·nz ; if remZ ≠ 0 : nz += 1
```

Three passes, in order:

1. **Partial columns** (when `rx ≠ 0` or `remX ≠ 0`): for every row `r` in
   `0 .. nz−1`, the tile `(tx0, tz0 + r)` is drawn at `(vpLeft − rx, vpTop − rz
   + 32r)` when `rx ≠ 0`, and the tile `(tx0 + nx − 1, tz0 + r)` at `(vpLeft +
   32·nx − rx − 32, vpTop − rz + 32r)` when `remX ≠ 0`, each as a 32 × 32
   pseudo-frame (zero offsets, no key, uncompressed) through the **raw**
   clipped blitter of [R-COMP-01 §2] — the viewport clip rectangle leaves the
   right `32 − rx` columns of the left tile and the left `remX` columns of the
   right tile.
2. **Partial rows** (when `rz ≠ 0` or `remZ ≠ 0`): for every column `c` in
   `0 .. nx−1`, the tile `(tx0 + c, tz0)` at `(vpLeft − rx + 32c, vpTop − rz)`
   when `rz ≠ 0`, and `(tx0 + c, tz0 + nz − 1)` at `(…, vpTop + 32·nz − rz −
   32)` when `remZ ≠ 0`, the same way. The corner tiles are therefore drawn
   twice.
3. **Interior**: drop the partial edge tiles (`rx ≠ 0` → first column `tx0 +
   1`, x origin `vpLeft + 32 − rx`, `nx −= 1`; `rz ≠ 0` likewise for rows;
   `remX ≠ 0` → `nx −= 1`; `remZ ≠ 0` → `nz −= 1`) and copy every remaining
   tile with the unclipped 32 × 32 block copy at `(x0 + 32c, y0 + 32r)` — on
   the screen surface this locks the surface per tile and unlocks it after
   each copy.

**Edges.** Terrain has no key colour: tile pixel 0 is opaque. The pass reads
the tile-index grid with **no bounds test**; it relies on the camera clamp
(document 07) keeping `tx0 .. tx0 + nx − 1` and `tz0 .. tz0 + nz − 1` inside
the grid. A tile is never clipped against the map — only against the viewport
— so a camera the clamp lets reach the map edge shows whatever the grid holds
there ([R-TERR-01 §2] void strips are ordinary indices).

**No map-bounds clip.** There is no map-bounds clip anywhere in the tile
pass: the only clipping is the viewport clip rectangle applied to the edge
tiles by the raw blitter's shell, and the interior copy is not clipped at
all.

#### Feature draw order and clipping, and the water line on 3DO wrecks [R-RAST-01 §6]

This section states the composer's feature passes exactly. Everything is
**Established (direct-static)** unless marked.

**The window.** Both feature passes and the unit buckets share one window of
plot cells, computed once per frame from the camera:

```
rowFirst = trunc(camZ / 16) - 16          camZ, camX in map pixels; trunc toward zero
colFirst = trunc(camX / 16) - 10
rows     = the bucket-row count;  cols = the bucket-column count     (viewport-derived, [R-RAST-01 §6-A])
rows/cols are reduced by any part of the window that falls below 0, and the
window is clipped so that rowFirst + rows <= mapHeightCells - 1 and
colFirst + cols <= mapWidthCells - 1 (the last row and column are excluded)
```

**Pass 1 — short features (between strips 2 and 3).** Row-major over the
window, each cell: clear the never-seen bit; when the cell's feature word is
live (`< 0xFFFB`), read the definition: if its `height` is **below 10** the
feature is drawn now, subject to the gate; otherwise the never-seen bit is
set and the feature is deferred. The gate is: definition without
`nodrawundergray`, **or** the plot cell's placer nibble equals the local
player's slot, **or** the two-corner LOS predicate of §5.1.5 passes.

**Pass 2 — mode-`1` units and tall features (between strips 4 and 5).** For
each window row in order: first every unit in that row's bucket whose
flags-word mode mirror is `1` (`[04 R-MOV-01 §8]`; structures included,
[R-RAST-01 §7]) — §7 gives the bucket rule — then,
column-major across the row, every cell whose never-seen
bit is set (a deferred tall feature), under the same gate as pass 1.
Consequences an implementer must keep: a tall feature paints **over** the
mode-`1` units of its own row and of every earlier row; a short feature
paints **under** every unit; a mode-`1` unit paints over the short features
and over tall features in rows above it; and airborne aircraft and units
attached to a carrier paint over all features because they are drawn later
(§7).

**Per feature (the per-cell dispatcher).** The anchor is §5.1.4's formula.
Then:

- **live instance** (cell bit 0): a 3DO definition fills the engine's
  feature pseudo-unit — model pointer, position and the slot's orientation
  words — and hands it to the ordinary per-unit present of §7. Its retained
  owner slot selects player zero's colour for team faces (§3 above); a sprite
  definition blits the instance's shadow cursor frame (only when the
  instance's shadow bit and the feature-shadow option bit are set) and then
  its normal cursor frame, both through the opaque blitter and both at the
  anchor;
- **static** (no instance): shadow first — `seqnameshad`'s frame 0, or the
  animated shadow cursor's current frame when `animating` — through the
  tinted blitter when `shadtrans` is set and the opaque blitter otherwise,
  gated on the feature-shadow option bit and on the entry existing; then the
  body — `seqname`'s frame 0, or the animated cursor's current frame —
  through the tinted blitter when `animtrans` is set, opaque otherwise.

The sprite path's clipping is the standard inclusive-rectangle intersection
of the frame against the surface clip ([03 §4.1], [fmt gaf]); a frame
entirely outside is dropped whole. The 3DO path clips at the composition
image's single blit ([R-REN-03A §9]) and, inside the image, at the image's
own bounds ([R-RAST-01 §1] step 2).

**The water line on a 3DO wreck.** A 3DO feature is presented as a unit, so
[R-REN-03A §8]'s waterline pass runs on it with these fixed inputs: the
pseudo-unit's Y is the instance's current height — the sinking integration
of [05 R-FEAT-01 §13] moves it below the surface — and its sonar-contact bit
is set permanently at construction ([R-RAST-01 §4]), so the submerged part is
**always recoloured through the `BLUE TABLE`** (every pixel with
`key <= (seaLevel - hi16(Y)) + 50`) and never erased, for every viewer. It
has no `Digger`, so no digger erase. Its structure-class bit is set and its
definition ordinal is `0`, so it takes the structure shadow branch with the
sea-level test: **a wreck whose `hi16(Y)` is below sea level casts no
shadow** ([R-RAST-01 §4]). It has a key plane (the pseudo-unit's `ZBuffer`
mirror bit is set unconditionally, [R-REN-03A §2]).

**Fog and LOS are not applied at raster time.** The feature passes decide
*whether* to draw from the gate above; nothing in the sprite or model
rasterizers reads the visibility grids. The fog overlay of §3.3 is composed
after strip 9 and darkens features, units, shadows and projectiles alike
([R-SEL-02A]).

#### The feature window's two counts, and the gate has no fog term [R-RAST-01 §6-A]

Both are **Established (direct-static)**.

**The window's row and column counts are viewport-derived, not map-derived.**
They are fields of
the frame-window descriptor, written once during map load from the battle
viewport's pixel dimensions:

```text
viewCols = trunc(viewportWidthPixels  / 16)     truncation toward zero
viewRows = trunc(viewportHeightPixels / 16)
cols     = viewCols + 12
rows     = viewRows + 32
```

The descriptor's row-pointer array is then allocated at `rows * cols` dwords,
its per-row pointer array at `rows` dwords and its per-row count array at
`rows` words. Nothing rescales them per frame. Combined with §6's origin
(`trunc(camZ/16) - 16`, `trunc(camX/16) - 10`), the window gives sixteen rows
of margin above the viewport and sixteen below, ten columns left and two
right. Against the 512x416 battle rectangle inset at (128,32) that is 160 map
pixels of slack left of the drawn area, 17 right, 256 above and 241 below: the
right and bottom margins are deliberately tight, and a feature anchored more
than seventeen pixels past the right edge is dropped by the window even when
its sprite would have reached back onto the screen. That asymmetry is retail's
and is not a defect to correct.

The camera-to-cell conversions in the window setup and in the unit bucket key
divide by sixteen with a sign correction that **truncates toward zero**, not
floor: the dividend is biased by fifteen when negative before the arithmetic
shift. The unit bucket key is `trunc((unitZ - camZ)/16) + 16` and is admitted
on `0 <= row < rows` against the *unclipped* row count, as §7 states.

**The gate contains no visibility term of its own.** There is no explored
precondition. The per-cell loop of pass 1 is, in order: clear the never-seen bit unconditionally;
skip when the cell's feature word is not live (`>= 0xFFFB`); read the
definition; if `height >= 10` set the never-seen bit and defer to pass 2;
otherwise draw when the definition's `nodrawundergray` bit is clear, else draw
when the plot cell's placer nibble equals the local player's slot, else draw
when the two-corner LOS predicate returns nonzero, else skip. The loop reads
neither fog channel and neither visibility grid. §6's "fog and LOS are not
applied at raster time" is exact; what §5.1.5 describes is the overlay's
effect on the finished frame, not a branch in the pass.

The practical consequence, and the one a reimplementation must keep: a feature
whose cell has never been seen is still composed, and the 32-pixel fog blocks
laid down after strip 9 are the only thing that hides it. Where a tall sprite
straddles the boundary the covered part goes black and the rest stays visible —
treetops stand out of the unexplored dark — and no feature ever appears or
disappears as a whole when a visibility tile changes state. Gating each
feature on its anchor cell's fog tile is wrong: that edge does not line up
with the fog blocks.


### 5.2 Units and 3DO models

Units are bucketed by world-Z plot row relative to the camera
([R-RAST-01 §7]) so that the software renderer can process them in a
deterministic order. Visibility gates are applied
before drawing hidden units. A visible model traverses the 3DO sibling/child
tree, resolves fixed-point piece transforms, triangulates primitives, applies
player/team palette selection, and optionally renders a shadow pass.

The unit catalog’s case-insensitive sort gives stable model/piece indices. COB
piece animation updates state before rendering. Per drawn unit the renderer
compares its cached orientation triple against the unit’s bank, heading, and
pitch; when ANY axis differs by more than 7 angle units it refreshes the cache
and schedules a rebuild. A dirty frame resets affected subtrees from pristine
model vertices and reapplies transforms ancestor-after-descendant (composition
contract in section 2.4). The unit’s bank, heading, and pitch fold into the
ROOT piece’s Z, Y, and X angle slots respectively, composing as the outermost
factor of the chain. Unit position never enters piece math: pieces transform
around the model origin, and position enters only the final screen placement.
Projectile models reuse the identical rotation helper — yaw feeds the Y slot,
pitch the X slot, each with a constant negative half-circle (180-degree)
authored model-facing offset — and the `propeller` variant substitutes its
spin angle for the roll word, as follows.

**The model-projectile angle block and the propeller variant (Established,
direct-static).** A model projectile is drawn from a **three-word angle block**, and the drawer
rotates every vertex by those three words in a fixed order — each step rotates
one coordinate pair in place, so the pair named is the one that turns:

| Block word | Coordinate pair rotated | Axis | Named |
|---|---|---|---|
| 0 | (X, Y) | Z | roll / bank |
| 2 | (Y after word 0, Z) | X | pitch |
| 1 | (X after word 0, Z after word 2) | Y | yaw / heading |

The block is therefore ordered `{roll, yaw, pitch}` onto the `{Z, Y, X}` slots —
the same bank→Z, heading→Y, pitch→X assignment the unit root fold uses — and is
applied Z, then X, then Y. Only words 1 and 2 receive the negative half-circle
model-facing offset; word 0 is used verbatim. [06 R-WFX-01 §4] states the block
as `{roll word, yaw − 0x8000, pitch − 0x8000}`, and its render type 6 draws
`{roll, yaw, pitch}` with no offsets at all — both consistent with this
ordering.

**The `propeller` weapon flag substitutes the record's spinning propeller angle
for word 0 — the roll slot — with no offset.** Three further facts constrain any
reimplementation:

- the substitution applies to the model's **child piece only**. The parent model
  is drawn first, from the unsubstituted block, so a propeller weapon's body
  keeps whatever roll the record holds and does not spin;
- the child is drawn at all only when the model has one **and** the current tick
  is strictly less than the record's expiry tick;
- the angle is advanced by the simulation, a fixed 1,024 units per tick, before
  any family motion runs ([06 §6.2]); the draw path only reads it.

Nothing in the draw path chooses between two rotation *sources* — the flag picks
which value goes into word 0 and nothing else ([06 R-WFX-01 §4]). The roll
word for a non-propeller model projectile is the record's
first orientation word, which only the meteor family accumulates, so an ordinary
missile draws with whatever roll its pool slot last held ([06 R-WFX-01 §4]
records that consequence as a Supported inference with a writer census as its
decider).

Texture mapping is corner-index affine 16.16 (direct-static): quads map index order `0→(0,0) 1→(1,0) 2→(1,1) 3→(0,1)` through the edge-table scanline mappers (ten-dword edge records) with per-edge `(dx<<16)/dy`, per-scanline `(uR-uL)/width` and `rowStep=(rowR-rowL)/width`, sampling `SHD[row*256+texel]` per pixel. (Textured faces are quads only and the flat filler takes any vertex count, [03 §2.4.1] item 2; the exact edge, span and rounding rules of both fillers are [R-RAST-01 §1].) No stored UVs, no perspective divide (bounded-negative), the default corners are `w-1/h-1` of the selected frame, nearest sample. No model span writer tests the sampled texel against a transparent or colour-key index ([R-REN-03A §5]); model transparency is carried by the composition image's own background index, which the final blit keys against. Face row is `trunc(dot(N,L)*5.0)&0x1F` with `L=(-0.8,1,0.25)`; a cleared render-piece shade bit (`DONT_SHADE`) pins the identity row `15`; the gouraud row interpolates as `row delta/width` (direct-static); the flat path takes the same row through `SHD[row*256+color]`. The `SHD` steps above belong to the shaded piece renderer only, which retail selects on `BMcode=0` plus the `Shading` display option ([R-RND-02A]); the unshaded renderer maps textured faces with the same affine mapper and no `SHD` lookup. Row `0x0F` is identity, rows `0..14` darken, `16..31` brighten (see §4.3.2).

#### Nanoframe reveal [R-P0-19-N]

An unfinished unit is composed exactly like a finished one, into the unit's own
offscreen indexed image, and then recoloured in place before the image is
blitted. Everything below is **Established (direct-static)**.

**The height key.** The key plane the reveal reads is the unit composition
image's ordinary depth plane; its full contract, including which units get one
at all, is [R-REN-03A §2]. In brief:
`key = hi16(vertexY) + 50 + (Digger ? 75 : 0)`, where `vertexY` is the
vertex's whole-world-unit height above the unit origin — the whole height,
not half of it. The span writers interpolate it in 16.16 across each scanline
and narrow it to a byte; a pixel is admitted only when the stored key is less
than or equal to the incoming one, so the highest face at each pixel wins and
ties go to the later-drawn face. [R-REN-03A §8] is what the raised base is
for.

**The reveal.** With `p = trunc(remaining × 255)` from the construction
remaining fraction (`1` at request, `0` at completion, so `p` counts down), the
pass derives a sweep line `t` and a four-deep band `[max(t-4,0), t)` beneath it,
all in byte arithmetic, and assigns each composed pixel one of three verdicts by
where its height key falls: **below** the band, **inside** it, or **at or above**
the line. A verdict is either a palette index, *erase* (write the image's
background index, so the pixel does not appear), or *keep* (leave the composed
texture or flat colour). Five stages, in build order:

| `p` | sweep line `t` | below band | in band | at/above line |
|---|---|---|---|---|
| `> 235` | `(p-235)×255/20` | erase | pulse A | erase |
| `200 < p ≤ 235` | `(p-200)×255/35` | erase | pulse A | erase |
| `115 < p ≤ 200` | `(115-p)×255/85 − 1` | pulse A | pulse B | erase |
| `30 < p ≤ 115` | `(30-p)×255/85 − 1` | keep | pulse B | pulse A |
| `≤ 30` | `p×255/30` | keep | pulse A | keep |

Divisions truncate toward zero and the line is consumed as a byte, so the two
negative-line stages wrap into an ascending line. The visible result is: an
empty body swept twice by a bright line, then a solid green fill rising from the
model's base, then the texture rising from the base with solid green still above
it, then the finished texture swept once more.

**The pulses.** Two colours ping-pong across the sixteen-entry green ramp based
at `0xa0`: `pulse A` from `(unitID ^ 5) + tick×33/30` and `pulse B` from
`(unitID ^ 9) + tick×57/30`, each folded as `value & 0x10 ? 0xaf - (value & 0xf)
: 0xa0 + (value & 0xf)`. `unitID` is the unit's own sixteen-bit identifier — the
one its diagnostic text formats — so two adjacent nanoframes do not pulse
together.

**The outline.** After the recolour, every primitive of every visible piece is
overdrawn in `pulse B` at its per-scanline extremes — two pixels per polygon
row — with the load-time selection primitive the one exclusion, the same
primitive the raster pass skips. The overdraw is key-tested whenever the
image has a key plane, and it is a pass over the composition image, never the
framebuffer ([R-COMP-01 §3]). At the start of construction the body is
entirely erased and only the outline remains, which is why a nanoframe reads
as a pulsing wireframe.

**Not the reveal.** The construction fraction also forces the mobile image-cache
path and suppresses one shadow branch. Neither changes the soft/hard draw
classification or the bucket key.

#### The nanoframe outline is the edge walk's row extremes, and it is key-tested [R-COMP-01 §3]

The overdraw of [R-P0-19-N] is not a polyline and it is depth-tested whenever
the image has a key plane. **Established (direct-static):**

- Pieces are walked **last to first**; only pieces with the draw bit set take
  part. Per piece the primitives are taken in stored order from index 1 when
  the model declares a selection primitive (the load-time swap of §2.4 put it
  at index 0) and from index 0 otherwise — the exclusion the paragraph states.
- Each vertex projects as `sx = hi16(x) + originX`, `sy = hi16(−z) −
  (hi16(y) >> 1) + originY` (the composition image's origin pair,
  [R-RAST-01 §2]), with the key `hi16(y) + 50 + (Digger ? 75 : 0)` — the
  whole height, exactly [R-P0-19-N]'s key, not halved.
- The primitive's vertices (closed with a copy of the first) go through an
  edge walk of the [R-RAST-01 §1] form — extrema, left chain toward the
  previous index, right chain toward the next, `x = x0 × 65536 + 0xFFFF` and
  `key = k0 × 65536` stepped by `(Δ × 65536) / (y1 − y0)` (truncating) — over
  rows `[minY, maxY)`; on each row where `xr − xl > 0` **strictly**, exactly
  **two pixels** are written: at `xl` and at `xr`. Nothing else on the row.
  So a near-horizontal edge is traced only at its ends, a one-pixel row is
  skipped, and a row the winding cull produces (`xr ≤ xl`) draws nothing: the
  "wireframe" is the polygon's per-scanline extremes.
- **Key test.** Without a key plane both writes are unconditional. With one
  ([R-REN-03A §2]'s gate), each endpoint is written only when `storedKey ≤
  trunc(interpolatedKey)` and the key is stored — the same admission as the
  span writers. Since an edge's key equals its own face's key, edges on the
  topmost face pass by equality and edges hidden behind a higher face fail:
  the outline shows through the body only where the body was erased.
- The colour is `pulse B`, unchanged; the recolour verdicts of [R-P0-19-N]
  are as stated there (pixels equal to the transparent index are skipped).

**The target image, and where the pass sits (Established, direct-static).**
The reveal and the outline are one pass over a
**composition image**, never over the framebuffer, and which image depends on
whose nanoframe it is:

- a unit drawn on its own — a structure under construction — runs it over the
  **staging image** of [R-REN-03A §4], immediately after the cached body is
  copied in and before the live-piece pass, the attached-child composites, and
  the waterline and digger erases, so those erases remove outline pixels
  below their thresholds exactly as they remove body pixels;
- an attached child — a factory's product on its plate, carried cargo — runs
  it over the child's **own two-plane image**, after the child is composed and
  before that image is composited into the carrier's staging image under the
  key test of [R-REN-03A §4] step 2. A carrier's geometry therefore occludes
  the product's wireframe wherever it occludes the product's body, and a child
  composited later covers the carrier's own outline where its keys win.

An outline pixel stores its key beside its colour, which is what lets every
later key test see it; the outline is not a framebuffer overdraw, and a
carrier can occlude it.

#### Unit draw order: the Z-row buckets, the two unit passes, and where shadows and tints happen [R-RAST-01 §7]

The bucket key is the unit's **world Z in 16-pixel plot rows relative to the
camera**, not a screen coordinate and not the sheared Y; the pass split is
the unit record's flags-word **mode mirror** ([04 R-MOV-01 §8]); and the
deferred features are the **tall** ones (`height >= 10`), shadow or not.
Everything below is **Established (direct-static)**.

**The bucket build (once per frame, before strip 0).** The composer walks the
**on-screen unit list** — the list the health-bar pass also reads
([R-FX-01 §6]): units inside the viewport rectangle that the visibility
predicate admits for the viewing player — in list order (ascending unit
slot) and appends each to bucket

```
row = trunc((hi16(unitZ) - camZ) / 16) + 16       trunc toward zero; camZ in map pixels
```

when `0 <= row < rows`; a unit outside that range is not drawn this frame.
Appends are stable, so within a row the draw order is ascending unit slot.
Note the asymmetry with §6's window: the feature row is the absolute cell row
minus `trunc(camZ / 16) - 16`, the unit row is `trunc((z - camZ) / 16) + 16`,
and the two differ by one when `camZ` is not a multiple of 16 — reproduce
both expressions rather than one shared cell index.

**Pass A — mode-`1` units, interleaved with tall features (between strips 4
and 5).** For each window row in order, each bucketed unit whose **flags-word
mode mirror** is `1` — and only when the composer's render-mode argument is
nonzero: (i) when the unit's **selected** bit is set and the diagnostic
bit permits (set unconditionally at settings load), the selected-unit
footprint quad of [R-WATER-01 §1] is drawn — there is no water-wake
rectangle; (ii)
when the unit has a model draw record, the per-unit present runs. Then that
row's deferred tall features ([R-RAST-01 §6]).

**Pass B — everything else (after strip 7, i.e. after projectiles and the
fixed effect pool).** Every row in order, every bucketed unit whose mode
mirror is **not** `1`, with the same two steps.

**The pass predicate is the unit record's mode mirror, and a structure's
mirror is `1`.** The composer never touches a mover. Both passes load the
**unit record's own flags word** and test its low two bits, which is the
*mode mirror* of [04 R-MOV-01 §8], not the mover's copy; a unit with no mover
(every building — the allocator constructs a mover only for `bmcode 1`,
[04 R-FAC-02 §5]) still has a mirror, and it is `1`: unit creation writes the
low two bits to `01` before any class test, and the spawn wrapper then
overwrites them with its own mode argument, which **every spawn call site in
the image passes as the literal `1`** — the `BuildingBuild` order handler
that lays a nanoframe, the mobile `Build` handlers, the map-start placer, the
commander respawn, the campaign placer. The mover constructor writes `1` as
well, so the two words agree for a mobile unit. Nothing ever writes a
different mirror for a unit that owns no mover, so a building's mirror is `1`
from the tick it is laid as a nanoframe until it dies — which is what
[04 R-FAC-02 §5] states ("stays `1` for a factory's whole life") and what
makes a factory pass its own mirror as the placement validator's mode
argument ([04 §10] `BuildingBuild`).

**The two passes, exactly — Established (direct-static).**

* **Pass A** is every bucketed unit whose mirror is `1`: ground and sea
  movers, landed aircraft, **and every structure, complete or still a
  nanoframe**. A building does not change pass when it completes.
* **Pass B** is every bucketed unit whose mirror is not `1`: airborne aircraft
  (`2`), the attached/parked mode (`0` — cargo inside a transport and an
  aircraft parked on a pad, [04 R-AIR-01 §3]), and the save-installed `3`.
* **The mirror is written at creation, not by the mover.** A reimplementation
  must seed it in its own unit-creation service, before any class test and
  independently of whether the unit will ever own a mover; deriving it from a
  mover record installed later leaves a unit sorted into the wrong pass for
  the frames between its creation and that installation, which for a building
  nanoframe is the whole of its most visible life.
* Structures do **not** paint over grounded units, projectiles or the effect
  strips; they are painted in Z-row order among the grounded units, so a
  mobile unit in a later row paints over the building in front of it. What
  paints over everything world-drawn is the airborne set, which is the point
  of a second pass.
* **The nanolathe spray therefore paints over the unit being built.** Strip 6
  ([R-STRIP-01 §1]) is drawn after pass A and before pass B, so its particles
  land over every structure, every nanoframe and every grounded builder, and
  under airborne aircraft and units riding a transport. A construction spray
  hidden behind the building it is completing is a defect, not the retail
  order.

Pass A's per-unit work is itself inside the render-mode gate. A zero
render-mode argument suppresses strips 6 and 7, the projectile pass, the fixed
effect pool, pass A's per-unit work (both the selected-footprint quad and the
per-unit present), pass B in full, the health-bar/label walk and strip 9.
Strips 0–5 and 8, the terrain tile pass, the feature pass and pass A's
interleaved tall features still draw — matching the numbered composer order of
§1. Only the movie-capture frame passes zero; the ordinary frame and the
large-screenshot tile renderer both pass a nonzero argument, so a large
screenshot is a full frame.

**The per-unit present.** For the unit and then each attached child that is
not carried piece-less ([04 R-UNIT-06 §3]): if
any of bank/heading/pitch differs from the cached triple by more than 7
angle units, the cache is refreshed and the piece tree is marked for a
rebuild from pristine vertices ([03 §5.2]); a pending rebuild reapplies the
piece chain. Then the draw entry decides whether the cached image is rebuilt
([R-REN-03A §4]) and runs the present of [R-REN-03A §9] — shadow first
([R-RAST-01 §4] gives the three branches), body, live pieces, children,
waterline, blit. Per unit, therefore, **the shadow is always drawn
immediately before its own body**, never in a separate shadow pass; and a
shadow is never drawn for a unit the list excludes.

**Tinted bodies.** The body blit goes through the **tinted blitter** — the
`ALP` blend against what is already on the ground, [R-REN-03D §4] — instead
of the opaque one when the unit's activation byte has the **cloaked** bit
([R-VIS-01 §6]) or when the display-mode byte that the film/HUD-hide key
family clears ([07]) is nonzero; otherwise the opaque blitter keys out the
image's transparent index and writes everything else. Nothing is masked by
fog or LOS at this point: exclusion from the list is the only visibility
effect, and the fog overlay is applied later to the whole surface
([R-SEL-02A]).

**Established (direct-static): cloak is selected at the image commit.** In the
keyed branch, attached children first join the carrier staging image by the
ordinary key comparison. The carrier's cloak state then chooses the one final
staging-image blit; a child's cloak state does not change that keyed copy. In
the keyless branch, the cached image uses the same cloak-dependent blit, then
live pieces and attached children draw through the ordinary direct polygon
writer. Those later polygons do not inherit the cached-image tint. Shadow
selection and blending are unchanged by cloak. A change of cloak state needs
no cached-image rebuild: the present reads the current instance state on each
image commit.

**RNG.** No draw from either stream anywhere in the bucket build, the two
passes, the present, or the fillers ([R-RAST-01 §1]).

#### The per-unit present is a no-op for a unit that has a carrier [R-RAST-01 §7-A]

§7's bucket build applies **no carrier filter** — a carried unit is enumerated
on the on-screen unit list, bucketed on its own world-Z row and walked by pass
B like any other mode-`0` record — and yet retail never paints a carried unit
twice. **Established (direct-static):** the per-unit present's entire body is
guarded by a single test on the unit record's **carrier link**. A unit that has
a carrier returns immediately: before the orientation-cache comparison, before
the piece-chain rebuild, before its own attached-children walk and before the
draw entry of [R-REN-03A §4]. Everything a carried unit contributes to the
frame therefore arrives through the children step of its **carrier's** present,
composed into its own two-plane image and composited into the carrier's staging
image under the key test ([R-REN-03A §4] step 2).

Consequences an implementer must keep:

- **The per-pixel occlusion between a carrier and its cargo is final.** There
  is no later unconditional blit of the cargo to undo the key test. A
  reimplementation that presents a carried unit from its own bucket entry as
  well hands the last word to whichever of the two the in-row rule enumerates
  second — ascending unit slot — so a transport built before the unit it lifts
  gets its cargo painted over its hull and one built after it does not. That
  asymmetry is the tell; retail has neither case.
- **The carrier link alone is the test; the hang piece is not part of it.** A
  unit carried on the no-piece sentinel is skipped by its own present (it has a
  carrier) *and* skipped by its carrier's children walk (§7,
  [04 R-UNIT-06 §3]), so it is drawn nowhere at all.
- **A carried unit whose carrier is not on the on-screen list is not drawn.**
  Nothing else presents it, and the present does not check whether the carrier
  was reached this frame.
- **The selected-unit footprint quad is not inside the present.** Both passes
  draw the quad ([R-WATER-01 §1]) and then call the present, so a selected unit
  riding a transport still gets its quad while its model comes from the
  carrier.

### 5.3 Projected shadows and feature shadows

Options distinguish master shadows, feature shadows, vehicle shadows, and a
separate dithered-fog option. Unit definitions provide a `noshadow`-like control
and shadow-capability flags. The shadow GAF entry is used for feature/sprite
shadows; model shadows are projected through a separate ground pass.

The composer staging: the feature pass runs between strips 0–2 and strips
3–4, followed by a second interleaved pass over the Z-row buckets mixing the
mode-mirror-`1` units and the deferred tall features ([R-RAST-01 §6],
[R-RAST-01 §7]); per feature the shadow blit precedes the normal blit at the
same anchor (section 5.1.3). Model shadows do not use `SHD`, a stencil, or
dither; their final blend uses `ALP`. **Shadow presentation is
established:**

- **Option bits.** One options word (engine root state) carries six visual
  bits: bit 1 Anti_Alias (the structure-body supersample, not a shadow pass),
  bit 2 Shadows master, bit 3 VehicleShadows (unit model shadows), bit 4
  FeatureShadows (feature/sprite shadows — read directly by the feature
  shadow gate), bit 5 Shading (selects shaded structure bodies), bit 6
  DitheredFog (not read by the model-shadow branches). The bulk INI key writes
  bit 4 then fans out bit 4→bit 3→bit 2 so one toggle makes all three shadow bits equal;
  the per-category keys set only their own bit, so feature and vehicle
  shadows toggle independently.
- **Model-shadow branch and suppression gates.** After the master bit and
  `noshadow` gate, Diggers and ordinary mobile subjects use the flattened-body
  silhouette path and additionally require vehicle shadows and reject
  `canhover` and `floater`. Structures use the dedicated rerasterized and
  RLE-cached path without those mobile gates. The exact three-branch contract,
  including the Digger, waterline and ordinal-0 wreck erasures, is
  [R-REN-03D §1].
- **Projection.** The Digger and mobile branches reuse body-image geometry;
  the structure branch rasterizes through the quarter-height ground-shadow
  projection of [R-REN-03D §2]. All three are placed at
  `screenX = trunc(worldX - camX) + 133` and
  `screenY = trunc(worldZ - camZ) - (terrainHeight >> 1) + 32`, five pixels
  right of the body and sheared by ground height. Feature cells sample the
  terrain height under the anchor: the four plot height bytes (the anchor
  cell, its +X neighbour, its +Z neighbour and the +X/+Z diagonal — the same
  four §5.1.4 gives) are averaged with `>> 3` (the sum of four bytes divided
  by 8 — the half-height shear at map-pixel scale) and subtracted into the
  screen Y. The average is computed once per feature cell and shears the body
  blit and the shadow alike, not by the shadow path on its own.
- **Shadow raster families.** (A) GAF sprite shadows (features) blit the shadow
  frame through the opaque or the tinted blitter (the tinted path selected by
  the definition's translucent flag and gated on the window's alpha-blend
  capability [R-REN-03D §4]), clipped by
  an inclusive-rect intersect. (B) Digger and mobile model shadows flatten the
  finished body silhouette to palette index 0. (C) Structure model shadows
  rerasterize every face directly at index 0, punch out the body, and cache the
  result as RLE. Both model families blit through `ALP` before the body; see
  [R-REN-03D]. `ALP` is used both by the anti-alias downscale (three lookups
  per output pixel) and by the final model-shadow blitter; no shadow family
  builds a doubled stencil, seeds a dither pattern, or darkens through an
  `SHD` row (the doubled image is the anti-alias supersample of
  [R-REN-03A §6], the `0x01010101` fill is the composition image's background
  prefill of [R-REN-03A §1], the depth compare is the body key test of
  [R-REN-03A §2], and the submerged-hull tint reads the `BLUE TABLE` of
  [R-REN-03A §8]).
- **Order.** Per bucket row the shadow is drawn before the body in both unit
  passes, and per feature the shadow GAF precedes the
  body GAF; all shadow work sits between the terrain tiles and the units/
  features, and the fog overlay (after all strips) covers shadows like all
  world drawing. The feature-memory marker makes a tall feature's shadow
  persist independent of current line of sight.

**No water flag, and no aircraft-height term (Established).**

*There is no water flag.* The only water-dependent shadow behaviour is the
mobile silhouette branch's **waterline erase**, which is computed per draw
rather than authored: with `t = seaLevel − trunc(unitY)`, a subject with `t > 0` is partly
submerged, and every pixel whose height key is at or below `t + 50` is erased
before the tinted blit, so only the above-water hull casts a shadow
([R-REN-03D §1] branch 2). No definition key gates it; the only authored inputs
are the ones already listed in the branch gates. The body image runs the same
threshold shape in its own waterline pass, where the local player's own units
are blue-tinted instead of erased ([R-REN-03A §8]). The Digger branch's erase is
the constant `50 + 75` rather than this computed threshold, and the two are
separate erasures on separate branches, not one parameterized pass.

*Aircraft height enters nothing.* No shadow gate and no shadow placement reads
the unit's own height. What makes a flying unit's shadow behave like one falls
out of the placement above: the shadow's shear term is the terrain height
beneath the subject (`− terrainHeight >> 1`), while the body's is the unit's own
height (`− unitY >> 1`), so the two separate vertically on screen exactly as the
unit climbs, and rejoin when it lands. The silhouette is a 1:1 copy of the body
image, so the shadow neither shrinks, softens, nor fades with altitude, and
there is no altitude cut-off above which it stops being drawn. The mobile
branch's `canhover` and `floater` rejections are hover and float gates, not
flight gates; nothing in the three branches tests a flight capability at all.

#### 5.3.1 Feature shadow selection and blit order

**Established.** The feature-shadow preference directly gates sprite shadows;
unit master shadows and `Shading` do not add gates. Each admitted sprite feature
may emit its shadow before its body at the same anchor, with the standard
clipping of [R-RAST-01 §6]. The dispatcher distinguishes two sources:

- Without a live event record, the definition selects the shadow and body
  sequences. Static definitions use frame zero; animated definitions use their
  pre-wired rest cursors. `shadtrans` chooses the shadow's tinted or opaque
  blitter, and `animtrans` independently chooses the body's blitter.
- With a live sprite event record, its own shadow-enabled state and the
  feature-shadow preference admit the shadow cursor. Both that shadow cursor
  and the following body cursor use the opaque blitter, irrespective of the
  definition's translucent flags.

The tinted blitter uses the startup alpha-blend capability and the `ALP`
lookup of [R-REN-03D §4]; it does not select a shadow-specific `SHD` row.
Great Divide's resting trees, rocks and vent all carry `shadtrans=1`, so
those static/rest-cursor shadows take the tinted route. The previous blanket
statement that the definition flag also chooses the live-event blitter was
incorrect; [R-RAST-01 §6] owns that branch distinction.

### 5.4 Projectiles and laser beams

The projectile pool is a 300-record array. Simulation walks the entry-captured
active span once per sub-tick, marks retired records dead, and performs stable
tail compaction before presentation consumes the resulting pool. Records
appended during the scan are not simulated until the next projectile phase but
are visible to that tail compaction. Document 06 owns the complete lifetime and
burst-scheduling contract.

**Draw gate.** The renderer skips inactive records; each active record passes
one visibility test BEFORE rendertype dispatch: when the mode word enables
per-owner current-coverage byte grids, the projected half-resolution cell must
hold a nonzero current-sight byte in the local player’s grid; otherwise the
same cell goes through the one-point word-grid test at the local player’s bit.
The gate evaluates once per record — not per presentation case.

**Rendertype dispatch.** Dispatch selects presentation from the weapon
definition’s rendertype byte; all eight cases are established:

- 0 — line from the current endpoint to the tail endpoint (the beam geometry
  below). Colors are the definition’s primary and secondary color bytes
  remapped through the live GUI-to-display semantic map of §4.3.1; a zero
  authored secondary byte draws one one-pixel stroke. Otherwise both strokes
  use endpoints ordered along the major axis, with the offset secondary first
  and the unshifted primary on top; the exact offsets and major-axis tie rule
  are established in [06 R-WFX-01 §4].
- 1 — the ground shadow sprite (frame 0 of the `fx` bank's `shadow` entry at
  the record's cached floor height, [R-FX-01 §2]), then the definition’s
  model oriented from the projectile record; the model's child piece is
  drawn whenever the model has one and the current tick is strictly before
  the record’s expiry deadline, with the `propeller` flag substituting the
  spinning propeller angle for the roll word (§5.2).
- 2 — the startup-generated 22×22 displacement lens [R-FX-01 §4] drawn at the projected point; a failed
  draw-buffer admission executes an immediate return that ABORTS THE ENTIRE
  PROJECTILE RENDERER, not just this record — the only control-flow exit
  spanning later records.
- 3 — common base sprite plus definition model through a distinct orientation
  path with a separately built angle block.
- 4 — the definition’s `color` byte picks one of five global GAF sequences
  (0 `cannonshell`, 1 `plasmasm`, 2 `plasmamd`, 3 `ultrashell`, 4
  `plasmasm`); 255 (−1) suppresses the branch entirely and 5..254 draw
  nothing; frame = `(currentTick - spawnTick) mod frameCount`.
- 5 — one fixed global sequence; frame = `frameCount -
  ((expiryTick - currentTick) * frameCount) / (16-bit definition lifetime
  field)`, lifetime-scaled, drawn only while `0 <= frame < frameCount`.
- 6 — common base sprite plus definition model using the orientation stored
  verbatim in the projectile record.
- 7 — two randomized segmented-line passes. Segment count is the truncated
  16.16 endpoint span divided by the literal constant 327680 (`5 << 16`) —
  `trunc(dist / 5)`, five whole world units per segment — skipped when zero;
  each generated point receives integer per-axis jitter of
  `rand() * 11 / 0x8000 - 5` applied to X, height, and Z before each
  sub-segment projects through the standard line path.

Beam geometry is direct evidence:

- project the head and tail with the orthographic formula above;
- if `color2` is zero, draw one one-pixel Bresenham line;
- otherwise draw two parallel one-pixel lines, using color2 as the outer stroke
  and color as the inner stroke;
- do not anti-alias, alpha-blend, perspective-scale, or widen with distance.

The beam simulation keeps the tail fixed while the head advances through its
configured duration, then advances both ends to maintain a constant-length
beam until expiry. Collision/damage is tested at the head on each live tick,
not continuously along the line. A collision queues land, water, or lava impact
effects and sound, then removes the projectile. Expiry without collision simply
removes it; no impact GAF, sound, area damage, or screen shake is generated.

Model missiles use 3DO rendering and a projected shadow. Homing turns by a
per-tick turn-rate limit. The recovered target-loss branch supports an
inference that a missile coasts toward its last known point rather than
immediately reacquiring, but exact dead-target validation and reacquisition are
open. Trail smoke cadence: a trail emitter keeps an additive next-
emission deadline, so a delayed emitter preserves any owed puffs and a
**zero-delay definition emits one puff every tick**; burst parents never emit
trail smoke, and expiry without collision leaves one final trail-style puff for
timer families without the burn-blow flag.

#### The projectile presentation hand-offs [R-FX-01 §2]

[06 R-WFX-01 §4] owns the per-case arithmetic and the stock-author census;
this section states the presentation hand-offs of the eight-case list above.
**Established (direct-static)** throughout; the CRT/simulation stream
attribution is [06 R-WFX-01 §6].

- **The ground shadow (cases 1, 3, 4 and 6).** The base sprite is frame 0 of
  the `fx` bank's **`shadow`** entry ([R-FX-01 §1]), drawn at `(Xword − viewX
  + 128, (Zword − floor/2) − viewZ + 32)` where `floor` is the record's
  **cached average floor height** (doc 06 §8.1), not the projectile's own Y.
- **The child piece (case 1)** is drawn whenever the model **has** one and
  `currentTick < expiry` (strict). The `propeller` flag only substitutes the
  record's spinning propeller angle for the first word of the angle block; it
  does not choose between two rotation sources.
- **The case-4 selector** is the authored **`color`** byte ([06 R-WFX-01
  §1]): 0 `cannonshell`, 1 `plasmasm`, 2 `plasmamd`, 3 `ultrashell`, 4
  `plasmasm`; 255 (−1) suppresses the case; 5..254 draw nothing.
- **The case-7 segment count.** The span is the truncated 16.16 length of the
  tail→head vector, so `327680 = 5 << 16` and the count is `trunc(dist / 5)`
  **whole world units per segment, in map space**. The per-axis jitter
  `rand·11/0x8000 − 5` is in whole world units added to the point's high
  word, three CRT draws per generated point, `2·n` points, `6·n` draws per
  lightning record per **rendered frame** (the renderer runs per present,
  not per tick).
- **The case-2 entry** is the 22×22 **lens frame** built at startup beside
  the calculated explosion tables ([06 R-WFX-01 §2]); its blitter is
  [R-FX-01 §4] below.

**The render-transform refresh cache (as doc 03's statement).** Every
consumer of a unit's cached piece world transforms — the effect opcode's
origin lookup [04 R-COB-03 §6] is the traced one — calls one refresh helper
first. The helper recomputes the transforms only when at least one of the
unit's three orientation words differs from the cached copy by **more than
7**, absolute value in the 65,536-per-circle domain (`|a − cached| > 7`,
strict, per word, any of three). Below that tolerance the cached transforms
(≈0.04° stale at most) are reused, so effect origins computed from piece
vertices may lag the true orientation by up to that much. **Established.**

### 5.5 Effects, flashes, nanolathe, and cursors

Impact, smoke, wake, construction, and sequence events append to effect strips
that later rendering reads and compacts. Reclaim/capture nanolathe work emits
one segment every two ticks, while build-assist work emits two segments per
tick. Effect strips evict their oldest record whenever the pre-insert count
exceeds 400, holding at most 401 in steady state (section 1). Explosion flash
discs are precomputed once per
sequence: the per-frame cadence is the frame reference's second word — a 32-bit
tick count stored at the frame-reference array entry (frame base plus index
times eight, plus four) — consumed by the standard countdown cursor, so flash
and explosion animation timing is simulation-tick countdown ticks like every
other sequence family. Build/reclaim direction uses the builder and target
positions. The literal `6` the nano producers pass is the **strip
selector**, not a colour (doc 05: "selector 6 and strip 6 are one number");
the record carries no colour field and has no fade — it is a particle emitter
whose particles carry their own colours and lifetimes.

#### The nanolathe spray [R-P0-19-P]

**Established (direct-static).** A nano segment
record is an emitter, not a line. It is constructed from a source **point** (the
`QueryNanoPiece` world position, passed as a degenerate box) and a target
**box** (the target's world bounding box: the target's position plus the two
bounding-corner triples its definition stores next to the model top). Both boxes
are immediately narrowed, per axis, to the span between their `4/11` and `7/11`
interpolants and stored as origin plus extent — so a particle's landing point is
drawn from the middle three elevenths of the target's box, and that narrowed box
is what gives the spray its cone.

Every tick, a record spawns **five particles**, each costing **six CRT draws** —
three to pick a point in the source box and three to pick a point in the target
box, each as `origin + rand()×extent/0x8000`. The draws come from the **CRT
stream**, never the simulation stream, so nano presentation cannot perturb
lockstep. It does move the CRT stream's *position*: a record whose five
particles are all discarded as zero-length has an empty list at its next
update, so it is destroyed before its second spawn tick and that spawn's
thirty draws are never spent — and the wind interval, meteor scheduler and
victory-timer arm inherit the shift (`[01 §7.5]`). A particle's lifetime is `trunc(distance/4)` ticks, taken as a
signed sixteen-bit count from a floating-point distance: it travels four whole
world units per tick, and a zero-length hop is discarded before the particle is
written. Its colour is `0xa0 | nibble`, the nibble starting at `1 + (spawn index
mod 7)` and advancing by one every tick, wrapping seven back to one — a shimmer
up the green ramp `0xa1..0xa7`, never `0xa0`.

The record's own spawn window closes one tick after creation, so each accepted
work step contributes **ten particles over two ticks**; continuous construction
therefore holds two live records and ten new particles per tick. Per tick the
record's update advances each particle, drops the ones whose expiry tick has
passed, and the record itself is destroyed once its particle list empties.
The spray record is the strip-6 container object of [R-STRIP-01 §2]: a
pooled strip object whose update advances, expires, and refills its internal
particle list, evicted under the common 401-record rule.

Each particle draws at its world position through the ordinary projection,
gated by the local player's coverage at its own projected tile — the same
one-point gate the projectile path uses. The draw fills the rectangle from the
particle's pixel to one pixel right and down, and the rectangle filler is
**inclusive on both edges** (its span width is `right - left + 1` and it runs
`bottom - top + 1` rows; the clipper's reject tests are inclusive to match), so
a particle's mark is **two by two**, not one pixel. At one pixel the spray reads
as a thin dotted line rather than the dense cone retail draws — the four-fold
difference in coverage is what makes it look like a spray at all. The particle
record carries one further field, set to `0x100` at spawn and read by nothing
observed; its purpose is `TODO(question)`.

**Where the spray lands in the frame.** Strip 6 is drawn after the first unit
pass and before the second, so the particles paint **over** the unit being
built — a nanoframe, a finished structure and a grounded builder alike — and
under airborne aircraft and units riding a carrier ([R-RAST-01 §7]: the pass
split is on the unit record's mode mirror, and a structure's mirror is `1`,
so a structure is in the *first* pass). A spray hidden behind the building
it is completing is a defect.

**Nanolathe presentation pipeline [R-P0-19]:** Construction and reclaim work
producers route their nano events through the beam-family strip-6 identity and
mark the segment geometry authoritative (source = `QueryNanoPiece` world
position, target = product/footprint anchor). The fixed effect pool preserves
the strip destination, so the client's strip-6 nanolathe draw branch fires
instead of skipping the beam. One segment is emitted per accepted work step
(mobile/factory construction and reclaim), matching the per-path cadence of
[R-P0-06 §1]; build assist's two-segment cadence is separate.

**The nano source point's coordinate space [R-P0-19-P].** The source end of a
nano record is "the piece's world point", and the sense of Z matters by
twice the piece's authored depth offset — tens of whole world units for the
stock two-emitter builders — which the strip-6 particle draw carries
straight into `sy = (Zword − Yword/2) − viewZ`. The model pass narrows a
model-relative vertex as `Zn = hi16(−vz)` while a unit's own position enters
the blit unnegated — model space is mirrored in Z against world space
([R-RAST-01 §2], §2.4) — and the particle pass projects a plain world triple
with no such mirror ([R-FX-01 §3]'s common sub-record projection). The world
point handed to the record is `unit + (offsetX, offsetY, −offsetZ)` of the
composed piece offset, not `unit + offset`: **Established** at the
submission site by the piece locator of [R-RAST-01 §8], which serves every
consumer that turns a queried piece into a world point — this spray source,
the weapon muzzle of [06 §4.1], the carrier attach point and the factory
build plate — so the spray leaves the nano piece the model pass draws.

The cursor is software-drawn. Cursor GAF entries are loaded into a table;
`GetCursorPos` and configured hotspots determine placement. The renderer saves
and restores dirty cursor rectangles, changes cursor icon/mode for move, attack,
repair, patrol, build, and other order states, and uses the same logical-to-
physical palette mapping. GUI queue lines/icons are also software-drawn.

#### The strip object families, strip by strip [R-FX-01 §3]

[R-STRIP-01 §1–§3] gives the producer census, the container lifecycle, the
sub-record strides and the draw census. This section pins the per-family
arithmetic the census did not itemize: the two families that had none
(the flame-stream trail and the impact sprinkle), the identity of every effect
class the emit-sfx opcode and the debris draw reach (the "effect families
themselves" hand-off of [04 R-COB-03 §6] and the class-7 hand-off of
[04 R-COB-04 §4]), and the puff parameters of the remaining producers. Every
claim is **Established (direct-static)** unless marked; every random draw is
from the **CRT** stream — the simulation stream is never touched.

**Common mechanics.** A container's base init sets `deadline = tick +
lifetime`. The spawn-due predicate is `nextSpawn ≤ deadline && nextSpawn ≤
tick` (both inclusive); every family here sets `nextSpawn = tick + 1` after a
spawn and spawns once from its init. The per-tick update (phase 11 of [01
§4.4]) advances every sub-record in order, removes the ones whose expiry test
passes by stable compaction, then spawns if due. The removal verdict the
dispatcher tests before the update is per class ([R-STRIP-01 §2]): "the list
is empty" for the flame, trail, sprinkle and nano families, "list empty and
deadline passed" for the strips-5/9 smoke puffer, and constant false for the
vent (the two smoke classes are tabulated below). Sub-record draws
project `sx = Xword − viewX + 128`, `sy = (Zword − Yword/2) − viewZ + 32`
(16-bit truncated). Except for geothermal steam, they pass the one-point coverage gate at tile `(Xword >> 5,
(Zword − Yword/2) >> 5)` — the viewing player's byte grid when mode bit 1 is
set, else the word-grid bit — with an off-map tile failing the gate. The
"root flag byte that disables all strip allocation" ([R-STRIP-01 §1]) has **twenty readers and no writer anywhere in the image**: it is a
zero-initialized static, the disable branch is dead in retail, and the pool
gate is only the pool itself. **Established (reference census).**

| Strip | Family | Producer events and per-site parameters |
|---|---|---|
| 2 | impact sprinkle | COB `emit-sfx` types 2 and 3 (piece vertex 0 → vertex 1, spacing **16** / **8**, colour flag 1) and types 4 and 5 (vertex 1 → vertex 0, spacing 16 / 8, flag 1) [04 R-COB-03 §6] |
| 4 | smoke puff (the vent's class, [R-FX-02 §3]) | the geothermal steam producer of [05 R-ECO-02 §3]: init `(point, spawnInterval 5, frameHold 0 → 7, lifetime 150)` |
| 5 | teleport flame segments; burning-feature smoke | [R-LAYER §4]; [R-STRIP-01 §1] |
| 6 | nanolathe emitter | [R-P0-19-P] |
| 7 | flame-stream trail; impact sprinkle | `emit-sfx` type 0 (VTOL: vertex 0 → vertex 1, lifetime **6**) and type 1 (thrust: lifetime **7**), both hold 1; `emit-sfx` type `0x103` (sub-bubble: sprinkle from the piece's point toward `(X, seaLevel << 16, Z)`, spacing 8, colour flag **0**) |
| 9 | smoke puff; flame-stream trail | every weapon puff ([06 R-WFX-01 §5]); `emit-sfx` `0x101` white = puff init `(0, 1, 0, 0, 0)` on `smoke 1` and `0x102` black = `(0, 1, 0, 0, 1)` on `smoke 2`; the debris draw's smoke puff `(0, 1, 0, 0, 0)` per rendered frame under engine bit 1 [04 R-COB-04 §2]; the debris draw's **fire particle** (flame-stream trail class, below) per rendered frame under engine bit 0; the corpse column [R-LAYER §3] |

**The "class 7 with parameter 15" flash of [04 R-COB-04 §4]** is not a flash:
it is the smoke puff emitter with init `(point, frameCap 0, spawnInterval 7,
frameHold 0 → 7, lifetime 15, selector 0 → smoke 1)` on strip 9 — one puff at
spawn and one every seventh tick while `nextSpawn ≤ spawn + 15`, three puffs
in all, each playing all twelve `smoke 1` frames at hold 7 with the CRT
countdown of [06 R-WFX-01 §5]. It is the land-dust puff every above-sea
explosion emits. The calculated frame is the ramp `0x4f..0x6e` with
transparency by radius ([06 R-WFX-01 §2]).

**Smoke-puff family** (strip 4's geothermal steam, strip 9's weapon puffs and
the emit-sfx smoke points, strip 5's burning-feature smoke). Its 32-byte
sub-record carries the bound entry, a position, an animation frame, a hold
countdown, and nothing else — no velocity. The per-tick update is three adds
against the **raw** 16.16 position words, and the scales are what keep a puff
where it was made:

```
x += windX · 8          ; windX/windZ are the published wind words [R-WIND-01]
z += windZ · 8
y += authoredGravity · S    ; the map's `gravity` key, before any per-tick conversion;
                            ; S = 4 for the strips-5/9 smoke puffer, 16 for the strip-4 vent steam
```

All three are added to the raw fixed-point words, not to whole world units, so
a wind word of forty moves a puff by `320/65536` of a world unit per tick and
the gravity word of a stock map lifts a strips-5/9 puff by `112·4/65536` and a
vent steam puff by `112·16/65536`. A puff drifts a
world unit or two over its whole life and rises slowly; it does not travel.
The vertical term's scale global is the same authored gravity word the
projectile conversion divides by 900 (`[03 §2.2]` C4), read here unconverted.
The gravity shift is per class: two (× 4) in the strips-5/9 smoke puffer that
every weapon, burning-feature and `emit-sfx` producer uses, four (× 16) in
the strip-4 vent-steam class; both wind terms shift by three (× 8) in both
classes ([06 R-WFX-01 §5]). **Established** (direct static, both update
routines).

The animation clock counts the hold down; on zero it advances the frame and
redraws the next hold as half to full of the authored value, one CRT draw per
advance ([R-STRIP-01 §3]). A puff's own life is `crtRand·(frameCount − 3)/0x8000
+ 2` frames of the bound entry, so it needs the entry's frame count, which the
container stores at init. The strip-5 burning-feature producer's jitter is
applied to the container's world X and world height before this update sees
the puff; Z is the footprint centre ([05 R-FEAT-01 §16]).

**Flame-stream trail family** (emit-sfx types 0/1 on strip 7; the debris fire
particle on strip 9). Container: source `A`, target `B`, per-axis step
`((B − A) · trunc(65536 / lifetime)) >> 16` (64-bit product, arithmetic
shift — slightly short of `(B − A)/lifetime`: the factor is 10922/65536 for
lifetime 6, 9362/65536 for 7), segment hold = the leading argument (1 at every
site), `deadline = tick + lifetime`, and the vector's capacity grows by
`deadline − tick + 1` segments when the spawn needs room. Spawn — at init and
then once per tick while `nextSpawn ≤ deadline`, so `lifetime + 1` segments
in all — writes one 60-byte segment: the `flamestream` entry, position = a
fresh copy of `A` (every segment starts at the source, not at the previous
segment), target `B`, the step, `lastFrame = frameCount − 1` (19 in stock
`fx.gaf`), frame 0, phase 0, hold, and `expiry = deadline`. No random draw.
Update per segment: `pos += step`; `phase = (phase + 1) mod hold`; when the
phase wraps, `frame = (frame + 1) mod lastFrame` — the entry's **last frame is
never shown** and with hold 1 the frame advances every tick. Expiry: `expiry <
tick` (strict), so every segment of one container lives through the deadline
tick and all of them, then the container, go on the tick after it. Draw: the
ordinary frame blitter with frame `frame` at the projected position (the
frame's authored placement offsets apply). Net effect for `emit-sfx` type 0:
a train of up to seven flame sprites marching from the piece's first vertex
toward its second at one sixth of the span per tick, a new one born each tick,
all extinguished together seven ticks after the opcode ran; type 1 the same
over seven ticks with eight sprites.

The **debris fire particle** (engine bit 0, [04 R-COB-04 §2]) is this class
with `A = B =` the piece's position jittered per axis by `crtRand · 3 / 0x8000
− 1` whole units (three CRT draws) and `lifetime = crtRand · 3 / 0x8000 + 1`
(1..3; a fourth draw), hold 1, strip 9: a stationary flame sprite whose
container lays `lifetime + 1` coincident segments over as many ticks. The
four draws are spent **before** the pool is consulted, so a dropped particle
still costs them; and because the debris draw runs per rendered frame, one
container is created per frame per burning piece, all overlapping.

**Impact sprinkle family** (strip 2 for emit-sfx 2–5, strip 7 for `0x103`).
Container init `(A, B, spacing, lifetime 1, colourFlag)`: `deadline = tick +
1`, so the window closes after one tick and the container spawns exactly
twice (init, and the next tick's update) — the two puffs of [R-STRIP-01 §1].
Per-axis step: `len = trunc(sqrt(dx² + dy² + dz²))` in double over the raw
16.16 deltas (so `len` is the span in 16.16 units), then `step = ((B − A) ·
trunc(0x80000000 / len)) >> 16` — one **half world unit per tick** along the
A→B direction, the reciprocal truncated. Edge: `A == B` gives `len = 0` and an
integer divide fault (the sub-bubble type reaches it when the piece is exactly
at sea level; a piece whose vertices 0 and 1 coincide reaches it for types
2–5). Spawn writes one 68-byte puff: the `smoke 1` entry (carried, **never
drawn** — this family fills rectangles), position = `A` plus per-axis jitter
`crtRand · 7 / 0x8000 − 3` whole units (three CRT draws), a copy of `B`, the
step, the ramp ends `0x61` and `0x67`, `colour = flag ? 0x61 : 0x67`, `dir =
flag ? +1 : −1`, phase 0, `spacing`, and `expiry = tick + spacing · 6` (96
ticks at spacing 16, 48 at 8). Update: `pos += step`; `phase = (phase + 1) mod
spacing`; when it wraps `colour += dir`, then `colour > 0x67 → 0x61` and
`colour < 0x61 → 0x67` — the emit-sfx puffs climb the seven-entry ramp
`0x61..0x67` one step per `spacing` ticks and wrap to its bottom, the
sub-bubble puff descends it and wraps to its top. Expiry ([R-WATER-01 §1]):
the puff **survives** only while `tick ≤ expiry`
**and** the bilinear terrain height under it ([R-TERR-01 §4]; −1 off-map) is
strictly below the sea-level byte; it is removed the tick it reaches land
(height `≥ seaLevel`) or expires — off-map (−1) counts as water. This is what
makes the family a *wake*: it lives on water and dies on the shore. Draw: the coverage gate, then a
two-by-two rectangle (the inclusive filler of [R-P0-19-P]) written with the
raw palette index `colour` — like the nano ramp, this byte is **not** passed
through the logical-to-physical remap that beam and lightning colours use.
Three CRT draws per puff, six per container.

**Smoke puff family** (strips 5 and 9): [06 R-WFX-01 §5] is the arithmetic
(init parameters, the `hold − 2` start countdown, the `hold/2 + rand·(hold/2)`
redraw, the wind ×8 and gravity ×4 drift, removal at the last frame); the
strip-9 producer parameters are itemized there and in the table above. The
selector byte chooses the `smoke 1` (0) or `smoke 2` (nonzero) entry; the
puff draws the selected frame through the ordinary frame blitter after its
own coverage gate.

**The two smoke classes (Established: both class vtables read out and every
overridden slot decompiled; the vent's behaviour corroborated by an
eighteen-second retail capture of a geothermal vent on a green map).** The
strips-5/9 smoke puffer and the geothermal vent's class of [R-FX-02 §3] are
**two classes with two object vtables**, not one class reached from two
producers. Their lifecycle, art selection and draw admission differ as follows:

| | strips-5/9 smoke puffer | geothermal vent |
|---|---|---|
| removal verdict | sub-record list empty **and** stored deadline `< tick` | `return 0` — constant false |
| spawn gate | next-spawn `<=` deadline **and** next-spawn `<=` tick | next-spawn `<=` tick |
| vertical drift | gravity word **× 4** | gravity word **× 16** |
| blitted entry | selected by an init flag between the two smoke entries | the first smoke entry, bound directly |
| draw admission | per-puff one-point coverage gate | no coverage gate |

**Established (direct-static, both class draw dispatches and their draw walks):**
weapon trail, muzzle, impact and land-dust smoke uses the strips-5/9 puffer's
gate at each puff's own current position, independently of the launcher or
projectile's visibility. The tile is `(Xword >> 5, (Zword − (Yword >> 1)) >> 5)`;
both shifts are arithmetic. An off-map tile fails. With current sight enabled,
the viewing player's coverage byte must be nonzero; otherwise its mapping-word
bit must be set. Geothermal steam alone skips this test. Explosion art and its
calculated flash also skip coverage admission [06 R-WFX-01 §2]. All these draws
precede the fog overlay (§3.3): explored fog can retain explosion pixels under
its gray or dither treatment, while fully unexplored cells cover them. The
geothermal exception must not be applied to weapon smoke.

Everything else is identical, instruction for instruction: the two wind terms
(both × 8), the countdown and its `hold/2 + crtRand × (hold/2) / 0x8000`
redraw, the retirement compare `frame < lastFrame` made on every visit (a
sub-record survives while `frame < lastFrame`, signed, and is compacted out
otherwise), the single-puff spawn with its one draw for `crtRand × (frameCount
− 1 − 2) / 0x8000 + 2`, and `nextSpawn = tick + interval` written
unconditionally at the end of the spawn. The record layouts differ only by
the selector word the puffer carries and the vent does not, which shifts the
puffer's position triple one word later.

The stored deadline (`tick + lifetime`) is read by the spawn's capacity
reservation in both classes — the sub-record vector grows by `(deadline −
tick + interval) / interval` records when that count is positive, and the
guard covers only the vector resize; the puff-append below it is
unconditional — so the deadline never gates a spawn *directly* in either
class. What gates the puffer's spawn is its own gate's first term, which the
vent's class drops. For the strips-5/9 puffer the stored deadline therefore
**is** a lifetime: every weapon-side site passes lifetime 0 — the trail
puff, `endsmoke` at impact, `startsmoke` at the muzzle, and both emit-sfx
smoke points — which leaves the deadline at the creation tick; the gate then
refuses the second spawn and the verdict retires the container on the first
tick after its one puff is gone. That is what makes these producers
one-shots. The two parameterised sites are the only ones with a real window:
the above-sea explosion's land dust (interval 7, lifetime 15 — three puffs)
and the land wreck's column (interval 15, lifetime 900; a wreck that sinks
emits nothing at all, [R-LAYER §3]). For the vent the
deadline is only an allocation hint: its container is never removed by the
sweep — the 401-record eviction in the producer is the only thing that
removes one — so a geothermal vent steams for the whole battle, and because
a puff's whole life is a few tens of ticks the drift terms move it a
fraction of a world unit before it retires, which is why the plume looks
anchored even in a gale. Applying the vent's virtuals to the puffer would
turn every muzzle, impact and trail container into an immortal
one-puff-per-tick emitter and park strip 9 at its 401-object bound within
twenty seconds of a small firefight; retail shows nothing of the kind.

**Strip-5 flame segments** ([R-LAYER §4]) and the **nanolathe emitter**
([R-P0-19-P]) are stated at those anchors; the nano particle's `0x100` word
is bounded-negative over that class's particle advance and particle draw
(neither reads it) and remains **Unknown** (tail).

#### The strip object pool and the base object [R-FX-02 §1]

**Established** (direct-static). Supplements [R-STRIP-01 §1]'s "one
shared fixed pool".

The strip pool is a **LIFO free list of fixed-size slots**: a slot array, a
pointer stack of free slots, a capacity and a stack top. *Take* returns null
when the top has reached the capacity, else pops the next pointer and
advances the top; *return* pushes the pointer back one below the top. *Take*
ignores the size its caller hands it and yields a whole slot or null. Every
producer then zeroes **exactly its own class's record** before running the
family constructor — 13, 14, 17, 18 or 19 dwords by family. 13 dwords is the
**smallest** of those, not the largest; the largest is the strip-6 nano
family's 19 dwords, which is the slot size itself. Because the pool recycles
slots LIFO, zeroing a fixed 13 dwords for every family would leave a 19-dword
container holding the previous tenant's values in its last six dwords.
The base constructor stores the vtable and
zeroes the deadline word; the base *init* sets `deadline = tick +
lifetime`; the pooled **destructor** re-installs the base vtable and, when
its delete flag bit 0 is set (as the sweep's eviction and removal always
pass), returns the slot to the pool. The 400-object eviction of
[R-STRIP-01 §1] destroys the **front** object of the strip (the oldest) with
that flag and shifts the vector down. The pool's capacity is **1000 slots of
76 bytes**, fixed for the life of the process ([R-FX-02 §4]): the growth
routine has exactly one caller, the pool's constructor, so the pool never
grows again after start-up.

The sweep ([01 §4.4] phase 11) runs strips 0 … 9 in order and, per object in
insertion order, the removal-verdict virtual before the update virtual; a
true verdict runs the destructor with flag 1 and compacts the vector by one.
The composer's per-strip walk calls each object's draw virtual with the
target image, in insertion order. The COB `emit-sfx` cases 0–5 of
[04 R-COB-03 §6] are the producers of [R-STRIP-01 §1] / [R-FX-01 §3]: types
0 and 1 build the strip-7 flame-stream trail with `(hold 1, lifetime 6)` and
`(hold 1, lifetime 7)`; types 2–5 build the strip-2 sprinkle with spacing
16, 8, 16, 8 and the endpoints swapped for 4 and 5.

#### The strip-5 flame family, exactly: segment life is `floor(span / 5)` ticks, not an interval [R-FX-02 §2]

**Established** (instruction-level read of the family init, spawn, step,
expiry and draw). The segment's expiry word is a segment life, not the
10-tick re-lay interval.

**Init** `(A, B, lifetime)`: `deadline = tick + lifetime`; copy `A`, `B`;
`Δ = B − A` per axis (16.16); `L = sqrt(Δx² + Δy² + Δz²)` in floating point
over the raw 16.16 deltas, truncated to an integer `n`; then

```
segLife = high16( (n << 16) / 0x50000 )        ; 64-bit shift and division
        = floor( n / (5 × 65536) )             ; whole world units of span, divided by 5
step    = Δ / segLife  per axis                 ; signed 32-bit division, truncating
```

so each segment crosses the whole span in `segLife` ticks at **five world
units per tick**. Edge: a span shorter than five world units gives `segLife
= 0` and the per-axis division **faults** (retail contract; teleport
destinations closer than that to a unit reach it). The init then spawns once.

**Spawn** (also the update's re-lay): grow the segment vector's capacity to
`size + (deadline − tick + 10) / 10` when that is larger (signed division;
no growth when the quotient is not positive), then append one 52-byte
segment: the flame-stream GAF entry, position = a copy of `A`, target `B`,
`step`, `frameCount − 1`, start frame `crtRand × (frameCount − 1) / 0x8000`
(one CRT draw), and `expiry = tick + segLife`; then `nextSpawn = tick + 10`.
The spawn-due predicate is the common `nextSpawn ≤ deadline && nextSpawn ≤
tick` of [R-FX-01 §3], so the teleport call's 30-tick container lays a
segment at ticks 0, 10, 20 and 30 — four in all.

**Step per segment:** `pos += step` (three axes); `frame = (frame + 1) mod
(frameCount − 1)` — advancing every tick and never showing the entry's last
frame; the carry the step function returns is ignored. **Expiry:** removed
when `expiry < tick` (strict) by stable compaction; the container's verdict
is "no segments". **Draw:** the one-point coverage gate of [R-FX-01 §3] at
tile `(high16(X) >> 5, (high16(Z) − high16(Y)/2) >> 5)`, then the frame at
`(high16(X) − viewX + 128, high16(Z) − high16(Y)/2 − viewZ + 32)` through the
**tinted blitter** of [R-COMP-01 §2] / [R-REN-03D §4] — the `ALP`-blend
family, not the opaque keyed blitter. **Established (direct-static):** its gate
is the window's alpha-blend table capability, enabled by application startup
independently of the `Shading` preference [R-REN-03D §4]. Turning `Shading` off
does not make these flame, trail or smoke sprites disappear.

#### The 32-byte smoke-puff family, exactly; the geothermal steam is this class on strip 4 [R-FX-02 §3]

**Established** (instruction-level read of the constructor, the
three-argument init, spawn, update and draw). This is the **vent's** class;
[R-FX-01 §3] tabulates its differences from the strips-5/9 smoke
puffer.

**Constructor:** base constructor, then the smoke vtable, creation tick,
empty puff vector. **Init** `(point, interval, hold, lifetime)`: `deadline =
tick + lifetime`; store the point; `interval`; `lastFrameBase = frameCount −
1` of the bank's first smoke entry ([R-FX-01 §1]); `hold`, or **7 when 0**;
then spawn once. The geothermal producer of [05 R-ECO-02 §3] takes a pool
slot, zeroes it, constructs this class and calls init with `(vent point,
interval 5, hold 0 → 7, lifetime 150)` under the 400-cap eviction, appending
to **strip 4**; the literals' roles are therefore settled. Its update is the
smoke update of [06 R-WFX-01 §5] (wind × 8 drift, gravity × 16 rise,
countdown, frame advance, removal at the last frame).

**Spawn:** grow capacity to `size + (deadline − tick + interval) /
interval` when larger; append one 32-byte puff: the smoke entry, position =
the point, **`lastFrame = crtRand × (lastFrameBase − 2) / 0x8000 + 2`** (one
CRT draw; a random last frame in `2 … lastFrameBase − 1`, so puffs of one
producer vanish after differing frame counts), `frame = 0`, `hold`, and
`countdown = hold` — the first countdown is **not** random; then `nextSpawn
= tick + interval`. With `interval 5` the steam vent lays a puff every fifth
tick for as long as the container lives — which, because this class's
removal verdict is constant false ([R-FX-01 §3]), is until the strip evicts
it; `lifetime 150` is only the capacity reservation's hint, not a bound. One
container per producer tick of [05 R-ECO-02 §3].

**Draw:** for every puff, `(high16(X) − viewX + 128, high16(Z) −
high16(Y)/2 − viewZ + 32)`, frame `frame` of the entry, through the tinted
blitter — **with no coverage gate**: this family's draw walk tests nothing
before blitting, unlike the flame and sprinkle families. The puff record has
the random draw in the last-frame word and the hold in the countdown word,
and no gate in the draw.

#### The strip pool's capacity, and what a dropped object costs [R-FX-02 §4]

**Established** (direct static: the pool's constructor, its single
growth routine and that routine's single caller, the take and return entries,
the family destructors, the battle-exit teardown, and the static-initialization
table entry that runs the constructor).

**When and how the pool is built.** The slot pool of [R-FX-02 §1] is
constructed **once per process**, by a routine registered in the executable's
static-initialization table — it runs with the other C++ static constructors
before the program's main body, and registers its own teardown on the C
runtime's exit list. It is not rebuilt at battle entry: the ten-descriptor
strip table is allocated at battle entry and freed at battle exit
([R-CORE-01 §4.4.1]), but the slot pool outlives every battle. The
constructor asks for **1000 slots of 76 bytes** — 76 is the largest container
record (19 dwords; each producer zeroes its own class's count, 13 through 19
dwords, [R-FX-02 §1]). The pool's growth routine reallocates the
free-pointer stack,
allocates the new slots as one block and pushes them; it has exactly **one
caller, the constructor**, and the *take* entry has no growth path — so 1000
is the hard capacity for the whole process life, not an initial size.

**Return.** Every path that destroys a strip object passes the delete flag
whose bit 0 returns the slot: the phase-11 sweep's removal, the 400-cap
eviction, and the battle-exit teardown that walks all ten strips. A new battle
therefore always starts with all 1000 slots free; nothing leaks across
battles.

**Exhaustion.** *Take* returns null once 1000 containers are live across **all
ten strips together** (the per-strip bound of 401 is a separate, later test).
A producer that gets null constructs nothing, runs no family init, appends
nothing and returns silently — the "silently drops the object" of
[R-STRIP-01 §1]. What a dropped object costs in CRT draws depends on where
the draws sit:

* draws inside the family's init or spawn are **not** spent — the nano
  emitter's thirty, the smoke puff's one, the sprinkle's three and the flame
  segment's one ([R-STRIP-01 §3]) all live inside the init the producer never
  calls;
* draws taken at the call site **before** the producer are spent regardless:
  the burning-feature emission's two jitter draws ([05 R-FEAT-01 §16]) and
  the debris fire particle's four ([R-FX-01 §3], which already records that
  its draws precede the pool).

The strip-disable byte is tested before the pool is consulted; it is the dead
constant zero of [R-FX-01 §3].

**Reachability.** The ten per-strip bounds sum to 4010, so exhaustion is
reachable in principle; the nanolathe family (seventeen sites, one container
per emitter spawn) and the strip-9 smoke family are the likeliest drivers.
Whether stock play reaches 1000 live containers is **Unknown** — no capture
has been examined for it; decider: a strip-object count over a retail
recording of a large late-game battle.

**Implementation rule.** Keep one global live-container count across all ten
strips, capped at 1000. A producer whose count is at the cap drops its object
after spending only the draws its own call site makes; every destruction path
(sweep removal, 401 eviction, battle exit) decrements the count. The
per-strip 401 bound of [R-STRIP-01 §1] is applied after, and independently of,
this test.

#### Every producer call site by strip literal; strips 0, 1, 3 and 8 have no producer [R-FX-02 §5]

**Established** (direct static, exhaustive over two independent
enumerations: every code reference to each of the thirteen producer routines
— thirty-nine call sites — with the strip literal read from each site's own
argument pushes; and every instruction in the image that reads or writes the
strip-table root word — nineteen). This verifies [R-STRIP-01 §1]'s "none —
no producer exists anywhere in the image" rows by a second, independent
method.

| Strip | Live call sites | Producer routine(s) and event |
|---|---|---|
| 0 | **0** | — |
| 1 | **0** | — |
| 2 | 4 | the sprinkle producer with colour flag 1, from the four vector `emit-sfx` cases 2–5 ([R-FX-01 §3]) |
| 3 | **0** | — |
| 4 | 1 | the vent-steam producer, from the feature stamp's geothermal tail ([R-FX-02 §3]) |
| 5 | 2 | the strip-5 flame producer from the teleport handler ([R-LAYER §4]); the white smoke-puff producer from the burning-feature walk ([05 R-FEAT-01 §16]) |
| 6 | **17** | the two nanolathe producers — the one whose source is a single point, ten sites; the one whose target is a single point, seven — from the construction, repair and reclaim order handlers ([R-P0-19-P]) |
| 7 | 3 | the trail producer from `emit-sfx` types 0 and 1 (two); the sprinkle producer with colour flag 0 from the sub-bubble type (one) |
| 8 | **0** | — |
| 9 | 12 | the parameterised smoke producer (land dust, wreck column: two); the white smoke-puff producer (five: the debris draw, the impact dispatcher, two projectile-phase sites, `emit-sfx` white); the black smoke-puff producer (`emit-sfx` black: one); the `startsmoke` producer (three weapon-fire sites); the debris fire-particle producer (one) |

Every one of the thirty-nine sites pushes its strip index as a literal; no
site computes one. The four producer routines that exist in the image but are
never referenced — a nanolathe, a trail and a sprinkle variant that take the
strip table as a hidden object argument, and a `smoke 2` puff variant with a
parameterised interval and lifetime — have **no reference of any kind**, code
or data, so they cannot be reached through a function pointer either. The
nineteen readers of the root word are: the battle-entry allocator (its
writer), the battle-exit teardown, the phase-11 sweep, the composer's draw
dispatcher, one unreferenced routine (dead), and the thirteen producers
(twelve live, plus the dead `smoke 2` variant). Nothing else touches the
table.

**Consequence.** Strips 0, 1, 3 and 8 hold no object in any retail session —
skirmish, campaign or otherwise — and the composer's unconditional walk over
them ([03 §1]) is a no-op in retail. This is a static closure and needs no
retail observation. Nanolathe may keep the four barrier calls as empty
passes or omit them; what it must never do is attach a producer to any of
the four.

#### The strip-object implementation rules, one line each [R-FX-02 §6]

The strip-object rules an implementation needs, one line each, with the
section that owns the arithmetic. **Established** unless marked.

| Question | Rule | Owner |
|---|---|---|
| shared pool capacity, behaviour when full | 1000 containers across all ten strips, process-lifetime; a full pool drops the object silently after only the call-site draws | [R-FX-02 §4] |
| start-frame mapping to a GAF frame index | there is no reduction step — each family's frame word is already an index into its bound entry: the smoke puff starts at 0 in `smoke 1`/`smoke 2`; the strip-5 flame segment starts at `crtRand × (frameCount − 1) / 0x8000` (in `0 … frameCount − 2`) of `flamestream`; the trail segment starts at 0 of `flamestream`; the nano particle and the sprinkle puff draw no frame at all (filled rectangles) | [R-FX-02 §2], [R-FX-02 §3], [R-FX-01 §3] |
| per-site lifetimes: sprinkle | container window closes one tick after creation (two puffs); each puff's tick deadline is `spacing × 6` (96 at spacing 16, 48 at 8), **and** it dies the tick the terrain under it is at or above sea level — a wake that lives only over water | [R-FX-01 §3], [R-WATER-01 §1] |
| per-site lifetimes: smoke | the puff has **no tick deadline**: it retires when its frame cursor reaches its own drawn last frame; the container's lifetime is the producer's literal (0 for every weapon-side, burning-feature and `emit-sfx` site — one-shot; 15 for land dust; 900 for the wreck column; the vent is unbounded) | [06 R-WFX-01 §5], [05 R-FEAT-01 §16], [R-FX-01 §3] |
| per-site lifetimes: flame stream | container 30 ticks (the teleport site); each segment lives `segLife = floor(spanUnits / 5)` ticks, laid every 10 ticks — four segments per container | [R-FX-02 §2] |
| sprinkle animation walk | `phase = (phase + 1) mod spacing`; on wrap `colour += dir` (`dir = +1` for flag 1, `−1` for flag 0), wrapping `> 0x67 → 0x61` and `< 0x61 → 0x67`; no draws | [R-FX-01 §3] |
| flame segment frame wrap | `frame = (frame + 1) mod (frameCount − 1)` every tick — the entry's last frame is never shown | [R-FX-02 §2] |
| trail segment animation counter | `phase = (phase + 1) mod hold`; on wrap `frame = (frame + 1) mod (frameCount − 1)`; hold is 1 at every site, so the frame advances every tick; no draws | [R-FX-01 §3] |
| sprinkle drawn byte / field map | the puff carries both ramp ends (`0x61`, `0x67`), the current colour (`flag ? 0x61 : 0x67` at spawn) and the direction word; the draw writes the current colour raw into a two-by-two rectangle; the `smoke 1` entry the puff also carries is never drawn | [R-FX-01 §3] |
| sprinkle per-tick velocity | `len = trunc(sqrt(dx² + dy² + dz²))` in floating point over the raw 16.16 deltas, `step = ((B − A) · trunc(0x80000000 / len)) >> 16` per axis — half a world unit per tick along A→B; `A == B` faults. The second point is the piece's transformed vertex 1 (vertex 0 for the swapped types 4/5) | [R-FX-01 §3], [04 R-COB-03 §6] |
| flame segment travel law | `step = (B − A) / segLife` per axis, signed truncating — five world units per tick; a span under five units faults | [R-FX-02 §2] |
| smoke default frame hold | 7 when the producer passes 0 | [R-STRIP-01 §1], [R-FX-02 §3] |

#### The flash and lens blitters, and which families do not use the countdown cursor [R-FX-01 §4]

**The flash blitter (Established, direct-static).** The explosion pool's first
draw walk ([06 R-WFX-01 §2]) hands every record's calculated (secondary) frame
to a dedicated blitter that is neither the ordinary frame blitter nor a fill.
It requires the window's light-table capability, enabled at startup independently
of display preferences [R-REN-03D §4], and clips the frame rectangle `(x − x_offset, y − y_offset,
+width − 1, +height − 1)` against the destination's clip rectangle
(inclusive), and then, for every source pixel that is not the frame's
transparent key (`0xff` for calculated frames), writes

```
dst = LHT[(src − 0x4f) · 256 + dst]
```

— the `LHT` brightening table of §4.3.1, row = the calculated disc's palette
index minus `0x4f` (`0x4f..0x6e` → rows 0..31), column = the pixel already on
screen. So the calculated disc is not painted; it **brightens what is under
it**, weakest (row 0, near identity) at the fuzzy rim and strongest (row 31)
at the centre index `0x6e`, and the named explosion art then composes over it
in the second walk. Raw and RLE frames take the same expression (the RLE path
applies it per literal and per repeat run and skips transparent runs);
composed frames recurse per subframe. The blit has no coverage gate, matching
[06 R-WFX-01 §2]. **Established.**

**The lens generator and blitter (Established, direct-static).** Startup
constructs one shared 22×22 signed-16 displacement map, anchored at `(11,11)`,
with strength 8. It is generated data, not a GAF-bank entry. For each cell
`(x,y)`, let `dx=x−11`, `dy=y−11`, and `r=sqrt(dx²+dy²)` in floating point.
When `trunc(r) >= 5`, store sentinel 32000. Otherwise set
`factor=(11−r)/8`, `sourceX=11+trunc(dx/factor)`,
`sourceY=11+trunc(dy/factor)`, and store the relative displacement
`sourceY*22+sourceX−(y*22+x)`. Each coordinate conversion truncates toward zero.
The map has no age, yaw, intensity or random input; it moves with the projected
projectile position and resamples the background on each draw.

After ordinary projectile visibility admission, render type 2 tests its
projected center against the viewport inclusively. A failed center test returns
from the whole projectile renderer: earlier output remains and later records
are skipped. An admitted lens captures the full 22×22 background block at
center minus `(11,11)` before constructing any output. Capture itself is not
clipped. For cell `i`, a nonsentinel displacement reads
`captured[i+displacement[i]]`; a sentinel writes the transparent key. The final
ordinary keyed blit clips output to the viewport and skips sampled bytes equal
to that key. A later lens observes all earlier drawing, including earlier lenses.
There is no light-table or alpha-table capability gate.

**Established finite-map check:** Every nonidentity sample points inward from
its destination toward the admitted center. Computing only visible output
cells from a pre-command snapshot therefore preserves changed pixels without
reproducing unused out-of-bounds capture reads. Binary64 and high-precision
independent evaluations give identical integer entries for the complete map.

**Unknown — lens transparent key.** The constructor does not initialize this
byte, and the bounded lifetime writer/reader census finds no later assignment.
Ordinary retail allocation is not zero-filled [01 §6]. Thus neither 255 nor zero
is an established universal key. Observing actual startup allocation contents
would settle a specific session's key, not a universal value. Nanolathe may use
zero under the deterministic host initialization policy in SPEC_CONFLICTS SC18,
while retaining this question at the code site. The consumer's sampled-key
comparison is established independently of the unknown initial value.

**Which presentation families do not use the authored countdown cursor.**
Every sequence family of §4.4 — feature and model textures (phases 6 and 7),
the fixed explosion pool ([06 R-WFX-01 §2]; hold word 2 for calculated
frames), the strip-5/9 smoke puffs (a CRT-drawn per-frame countdown seeded
from the producer's hold, not the file's), and the cursor (the wall-clock
delta stepper, [R-FX-01 §5]) — is a countdown of some kind. Three families are
**not**: the flame-stream trail and impact sprinkle of [R-FX-01 §3] (a modulo
phase counter per segment, hold from the producer, the file's hold words
unread); render type 4 (`(tick − creation) mod frames`, hold words unread) and
render type 5 (lifetime-scaled, [06 R-WFX-01 §4]); and the order-queue icons
of [R-FX-01 §5] (`tick / (2 · hold0) mod frames`). Their cadences are stated
at those anchors; none of them reads a frame's own hold word except the queue
icon, which reads only frame 0's. **Established.**

#### Cursors: table, selection data path, hotspot, cadence, and save-under [R-FX-01 §5]

**The table (Established).** The `cursors` bank ([R-FX-01 §1]) is bound into
one contiguous table of sequence pointers indexed 1..21, in this order:
1 `cursorattack`, 2 `cursorairstrike`, 3 `cursortoofar`, 4 `cursorcapture`,
5 `cursordefend`, 6 `cursorrepair`, 7 `cursorpatrol`, 8 `cursorpickup`,
9 `cursorteleport`, 10 `cursorrevive`, 11 `cursorreclamate`, 12 `cursorload`,
13 `cursorunload`, 14 `cursormove`, 15 `cursorselect`, 16 `cursorfindsite`,
17 `cursorred`, 18 `cursorgrn`, 19 `cursornormal`, 20 `cursorhourglass`,
21 `pathicon`. Index 0 is the word before the first slot, which the binder
never writes and the session-block clear leaves zero: a selector that yields 0
binds a null sequence, which is undefined behaviour (below), not "no cursor
art". The stock file's twenty-second entry, `cursorprotect` (8 frames), is
bound by nothing. **Established.**

**Index 0, a missing name and a frameless entry are undefined.** The binding
happens; the outcome is not a blank pointer. The sequence binder of §4.4 reads the entry's
frame count — for its start-index clamp — **before** it tests the entry
pointer, and its null arm then reads the loop byte through the same null
pointer, so binding a null sequence faults on the first read. The same holds
for a named entry the bank lookup does not find: the lookup returns null on a
miss and the slot stays null. An entry that exists with **zero frames** binds
frame 0 (the clamp sends every start index to 0 when the count is not greater
than it) and reads its hold word from beyond the empty frame table. The index
setter has no range check either: it stores the byte and binds the word at
that offset of the handle array, so an index above 21 would bind the globals
that follow the array as if they were sequences. None of this is reachable
with stock content and stock producers — the shape chooser starts at 19 and
takes minima over 1..20, the hourglass sites force 20 and the reset forces 19
— so retail defines **no** behaviour for a missing, frameless or out-of-range
cursor. A clone that validates every named entry at load, or ignores such an
index write, is choosing, not cloning; both choices are outside anything
retail can show. **Established (direct-static; the read-before-test order
verified in the instruction stream).**

**Selection data path (Established; the state machine itself is doc 07's).**
The battle interface keeps one byte, *the current cursor index*, and a
setter that is a no-op when the requested index equals it; on a change it
stores the index, binds the mouse object's playback cursor to the table entry
at frame 0 (the §4.4 binder: clamp, load frame 0's hold, copy the loop byte),
sets the mouse object's "animated cursor" bit, and hands the frame-0 header to
the window layer. The producers of the index: the order-cursor resolver, which
starts from 19 (`cursornormal`) and takes the **minimum** over every selected
unit of that unit's per-order cursor index (lower index wins: attack over
move over select), returning 15 (`cursorselect`) for the build-placement
special case; the hourglass sites, which force 20 while the interface waits;
and the interface reset, which forces 19. The order descriptor table (doc 04
§3.1; stride-25 records) carries **one byte per order state naming the cursor
index** — the same byte the queue-marker draw reads (below), so cursor and
queue icon for an order are one authored choice. Which order state maps to
which index is doc 07's contract; the byte's existence and both readers are
established here.

**Hotspot and placement (Established).** A cursor's hotspot is the current
frame's `x_offset`/`y_offset` pair (`[fmt gaf]` frame header +4/+6): the frame
is drawn with its top-left at `(mouseX − x_offset, mouseY − y_offset)`, where
the mouse position is the OS cursor position read at draw time. There is no
other hotspot metadata anywhere — no table, no per-entry record, no authored
key; the placement offsets *are* the hotspots, which is why the arrow
(`cursornormal`, 10×20) authors `(0, 0)` and the crosshair cursors author
their centres. `cursorfindsite` authors `(−15, −3)` — its art sits down-right
of the pointer. **Established (direct-static; stock offsets by asset census).**

**Cadence (Established).** The mouse object's update computes `delta =
clockNow − clockLast` where `clockNow = GetTickCount() · tickRate / 1000`
with `tickRate` the 30-per-second rate global — the "30-unit-per-second scaled
wall-clock delta" of §4.4 — and, while the animated-cursor bit is set, feeds
the delta to the multi-frame signed-delta stepper; if the frame index changed
it re-sends the new frame header to the window layer. Hold words are therefore
**thirtieths of a wall-clock second**, unaffected by game speed or pause.
Stock holds (asset census, `cursors.gaf`): attack 5 (10 frames), airstrike 1
(16), toofar 3 (2), capture 2 (13), defend 4 (16), repair 8 (12), patrol 9
(14), pickup 2 (24), teleport 10 (46), revive 3 (18), reclamate 3 (11), load 3
(16), unload 10 (16), move 8 (8), select 6 (2), findsite 10 (2), red / grn /
normal 10 (1 frame each — never stepped), hourglass 5 (8), pathicon 3 (1).
Every stock cursor entry's loop byte is 1 (looping).

**Save-under and presentation (Established, direct-static).** The window
layer owns the drawn cursor: the current frame header, three scratch surfaces
— each holding the fixed 1600-byte pixel block allocated once with the layer
([01 §5.1]) and re-described in place on every redraw as `width × height` with
pitch `width` from that frame header, never reallocated — and the last drawn
origin. Each cursor present, in order: read the OS cursor position; compute the new origin (position minus
hotspot); capture the framebuffer under the new rectangle into the *fresh
background* surface; copy the *old background* surface into it where the two
rectangles overlap (so the fresh capture holds true background, not the old
cursor image); build the *composition* surface as the fresh background with
the cursor frame blitted at its hotspot; restore the old background to the
screen at the old origin and blit the composition at the new one; keep the
fresh background as the next old background; and present **both** dirty
rectangles. A first present (no old rectangle) does the capture and the blit
only. The fullscreen path instead redraws the frame directly on the primary
after restoring the saved rectangle, and only while a hide counter is at or
below zero. None of this draws from any RNG. GUI queue lines and icons are
software-drawn in the same frame composer, not through this layer.

**Order-queue icons and the path dots (Established, direct-static).** The
queue marker for an order draws frame `((tick / (2 · hold0)) mod frames)` of
the cursor entry named by the order state's cursor byte at the order's target
point, where `hold0` is that entry's **frame-0** hold word (a zero byte means
no icon) and `tick` is the simulation tick counter — so queue icons animate
at half the speed the same entry runs as a cursor, and stop when the game is
paused, unlike the cursor. Along each queue line the composer also lays
`pathicon` dots every 48 world units, the first at `((tick − issueTick) mod
30) · 48 / 30` units from the segment start (so the dots slide toward the
target 1.6 units per tick and repeat every 30 ticks), only when the segment is
at least one world unit long; each dot steps the entry's frame index by one
(stock `pathicon` has one frame).

#### `damagebars` and the health-bar raster [R-FX-01 §6]

**The option (Established, direct-static).** `damagebars` is a value under the
game's registry key, read at settings load into **bit 0 of the display-options
word**: present → the bit takes the value's low bit; absent → the bit is
cleared and the default is written back to the registry. It is written out
with the other options, and toggled at runtime by a battle key command (the
key dispatcher is doc 07's) that flips the bit and immediately rewrites the
settings.

**Where the bars draw (Established).** The frame composer builds an
*on-screen unit list* every frame: the units whose definition box, projected,
intersects the viewport and that are either owned by or visible to the
viewing player. Between the strip-8 walk and the strip-9 walk ([03 §1]) it
walks that list and, for each unit that has either the bit set or a nonzero
group number: computes `sx = Xword − viewX + 128` and `sy0 = Zword − Yword/2
− viewZ`; when the bit is set **and the unit belongs to the viewing player**
draws the health bar at `(sx, sy0 + 42)` — ten pixels below the unit's
anchor row — and, when it also has a group number, the digit `'0' + group`
as text at `(sx, sy0 + 46)` in the default colour. Other players' units never
get a bar, whatever the option; strip-9 smoke therefore composes **over** the
bars and the projectile/explosion passes before them. There is no
line-of-sight test beyond the list's own admission.

**The raster (Established, direct-static).** Given the unit's signed 16-bit current health `hp` and its
definition's 32-bit `maxdamage`:

```
if hp <= 0: draw nothing
outer  = [sx−17 .. sx+17] × [y−2 .. y+2]        filled with dcb[0]
w      = (hp << 5) / maxdamage                  (unsigned, truncating)
inner  = [sx−16 .. sx−16+w] × [y−1 .. y+1]      filled with
             dcb[10] if hp > 2·(maxdamage/3)     (maxdamage/3 unsigned, truncating)
             dcb[14] if hp > maxdamage/3
             dcb[12] otherwise
```

Both fills are the inclusive rectangle filler of [R-P0-19-P], so the outer
bar is 35 × 5 pixels and the inner fill `w + 1` × 3 — full health fills 33
pixels and leaves a one-pixel border each side; a unit at 1 of 3000 still
shows a one-pixel fill; the comparisons are signed and strict. `dcb[n]` is
entry `n` of the active logical-to-physical table, the same entries [07 §6]
names for the thresholds. Death (`hp ≤ 0`) hides the bar the same frame.

#### R-FX-01 §6A — the group digit's "default colour" is entry 15

§6 above says the group digit is drawn "in the default colour", and
[R-FONT-01 §6] establishes that the FNT drawer takes the display context's
foreground/background bytes and that *each painter installs them itself* —
so "the default colour" names a piece of renderer state. The entry that state
holds when the composer reaches the label walk is **colour-map entry 15**.

**Established (direct-static).** The digit's own text call passes the string
and the pen and installs nothing. The install belongs to the frame composer,
one screenful earlier in the same pass: after the top strip's own repaint block
and before the first strip walk, the composer selects the **local player's side
font** and calls the pair setter once with (foreground = `dcb[15]`, background =
the skip colour read back from the context), and neither the bar-and-digit walk
nor any of the strip drawers it runs between that install and the walk calls
the setter again. So the digit inherits entry 15, the same entry [R-HUD-03 §4]
names for the battle text the composer draws on either side of it. The health
bar itself is unaffected — its four fills carry their own `dcb` entries as
arguments (§6) and never consult the text context.

Confidence: **Established** for the install, its position in the pass and the
digit call's lack of one; **bounded negative** for the absence of a second
install, over the strip drawers the composer invokes directly between them.

**Bounded alternatives.** The digit does not take whatever the *previous
frame's* last painter left (this composer installs its own foreground every
frame), and it does not take the resource counters' entries (those are
installed inside the strip-repaint block, which runs before this install and
only on frames where the resource snapshot changed).

#### `NoShake`, the named scratch surfaces, and the residuals [R-FX-01 §7]

**`NoShake` (Established, direct-static).** The token is an entry of the
in-battle **typed-command registry** — the table the battle setup registers
beside `Radar`, `fog`, `Contour`, `ScrollSpeed`, `IFace`, `Give` and `CDPlay`,
consulted by the text-entry line's command parser (doc 07 owns the line). Its
handler toggles **bit 4 of the session preference word**, which is exactly the
"preference bit `0x10`" §5.6 names as the shake request's early return. No
authored setting drives it; it is a per-session typed toggle, not persisted.

**Where the bit is cleared.** The preference word is the same word that holds the persisted `clock` bit
and the other registry-backed display preferences, and it survives battle
entry — the battle-entry orchestrator ([08 R-ENTRY-01]) never touches it.
The bit's writers, exhaustively over the recovered function set, are two:
the `NoShake` handler (toggle) and the **registry preference loader**, which
clears bit 4 unconditionally every time it runs — at process start and
again when the `SINGLE` screen's `Skirmish` button reloads the preferences
([07 R-FE-01 §1] phase 7 substate 0xb). It reads no registry key for the
bit, and the settings writer persists nothing for it. So the toggle is off
when the process starts and whenever a skirmish is set up from the menu,
but a toggle made in one battle is still in force in a later battle of the
same process that did not pass through that reload (a campaign mission
entered from the briefing chain). The impact dispatcher's request helper
tests the bit and returns with every accumulator untouched when it is set;
a second, direct-store request form carrying the same test has no caller.
**Established** (direct static; bounded negative over the word's writer
census).

**The named presentation scratch surfaces (lane question).** The three
mouse save-under rectangles are the old-background, fresh-background and
composition surfaces of [R-FX-01 §5]. The "blue table" is the 256-byte `BLUE
TABLE` slot of §4.3's five-table roster — the submerged tint of [R-REN-03D],
already closed there. The "lens frame" is render type 2's displacement map,
[R-FX-01 §4]. The **mouse-event buffer is closed (Established)**: the same
cursor-layer constructor that builds the three save-under surfaces allocates
it, `count × 24` bytes under its own `MOUSE EVENTS` tag, with the record count
(twenty) supplied by the display creator and stored beside the pointer — so it
is the twenty-record mouse-event ring of [01 §5.1], and doc 07 owns only what
reads it. The "loaded surface wrapper" was not traced in this pass:
**Unknown** (owner §4.1; decider: static trace of its allocation site).

**Residuals closed by citation.** The "derivation of the second effect vertex
used by the swapped pair" ([R-STRIP-01 §1] `TODO(question)`) is [04 R-COB-03
§6]: vector effect types read vertices **0 and 1** of the piece's transformed
vertex list; types 4 and 5 simply pass them in the other order. The
"physical units of the case-7 segmentation constant" is [R-FX-01 §2]. The
strip-disable byte is [R-FX-01 §3]. The nano particle's `0x100` word is
**still Unknown** (bounded-negative over its class's advance and draw;
decider: static trace of any remaining reader, or acceptance as an unused
initializer if a full-image reference census over the sub-record offset
finds none).

### 5.6 Screen shake

Screen shake is presentation, but it is driven from the authoritative impact
dispatcher, so every peer requests the same shakes from the same weapon data.

**Request.** If the preference bit `0x10` is set, the request returns with state
untouched; that bit is the typed `NoShake` toggle, not an authored setting
([R-FX-01 §7]). Otherwise
if inactive the two amplitude accumulators are cleared to zero while the
duration accumulator is left unchanged. The new duration is
`trunc((duration + authoredDuration)/2)` with signed truncation toward zero,
`remaining` is set to `duration`, the signed magnitudes are added into the two
amplitude accumulators, and if `duration > 0` active is set. There is no queue,
maximum, or distance falloff. A first request with a zeroed duration state
therefore runs for half its authored duration; later activations blend against
the stale duration left by the prior shake. The sole observed direct caller is
the authoritative impact dispatcher; it passes the same authored magnitude on
both axes.

**Consume**, once per simulation tick, from the camera update that runs after
the projectile phase — so a shake requested during the projectile phase jitters
in the **same** tick:

```
if not active: return
if remaining <= 0: active = false; return
sx = amplitudeX * remaining / duration      (signed, truncating)
sy = amplitudeY * remaining / duration
cameraX += rand() * sx / 0x8000 - (sx / 2)     (sx / 2: signed, truncating)
cameraY += rand() * sy / 0x8000 - (sy / 2)     (sy / 2: signed, truncating)
remaining -= 1
```

The half term is the compiler's signed truncating-halving idiom (add the
sign mask, then arithmetic-shift right by one), which is `s / 2` rounding
toward zero — not `abs(s) >> 1`, which differs by one when the amplitude term
is negative and odd; for odd negative displacement terms the noise band is
therefore asymmetric by one unit. **Established** (direct instruction read).

Three consequences matter:

1. The envelope is a **linear decay with uniform white noise**, not a sinusoid
   and not an exponential. The `-s/2` term (signed truncating) centres each
   axis.
2. The two draws come from the **CRT presentation random stream, not the
   simulation stream**. Shake costs exactly two presentation draws per active
   tick, none on the expiry tick, and never touches lockstep state.
3. The jitter is added **in place into the global camera origin** — the same
   integers every renderer subtracts to get screen coordinates. There is no
   separate render-time offset. So the world, fog, selection rectangle, and
   cursor mapping all shake together; and **the displacement is permanent**:
   when a shake ends, the camera rests at a random-walk offset from where it
   started and nothing restores it.

The camera clamp that runs immediately afterwards holds both axes inside the
map, so shake cannot push the view off-map, and the follow camera's
glide-halfway-to-target behavior damps it while tracking a unit.

### 5.6.1 CRD-006 follow-camera producer census [R-CRD-006 §2]

This section states the follow-camera producer and lifetime paths that are
established by the bounded retail census. The
phase-10 consumer and its priority remain the contract in [07 §10] and
[01 §4.4]: in-flight camera motion wins over a followed projectile, which wins
over a valid tracked object. A selected point is converted to a desired origin
by subtracting half the viewport span; for a ground object, the vertical point
also includes the object's half-height shear. The desired origin is clamped
before the current origin takes its bounded signed half-step. Follow runs
before shake, and shake mutates only the current origin before the final clamp.

**Established fact — in-flight camera-motion producers.** In-flight motion is
created when the currently followed projectile reaches one of the ordinary
projectile finalization paths: impact or expiry, completion of a burst/anchor
retirement, or purge of projectiles owned by a unit being removed. The producer
copies the projectile's final current/head world point into the three-component
16.16 camera anchor, loads the signed remaining count from the projectile's
authored camera-transition duration field, clears the followed-projectile
selection, and then marks the projectile for removal. Owner purge performs the
same hand-off before its immediate compaction. The battle-entry centering path
sets current and desired origins together; the bounded camera-setter census
found no separate anchor-only producer. Confidence is Established for these
listed producers and Unknown for any producer outside that bounded caller
census.

The count is consumed only when the in-flight choice wins phase-10 selection.
For a positive count, one invocation chooses the fixed anchor and decrements
the count by one; a count of zero no longer wins priority, so the next
invocation may select the followed projectile or tracked object. A zero
transition duration therefore does not create an additional positive-count
anchor interval. This is distinct from the target's own projectile lifetime
and from the projectile pool's captured-count traversal [06 §5.2][06 §7.1].

**Established fact — followed-projectile clear and compaction.** Finalization
and owner-purge paths clear the followed-projectile selection when they create
the in-flight anchor. Stable projectile compaction runs at the projectile
phase tail and after owner purge. When a surviving followed projectile moves
to a new pool slot, compaction rewrites the selection to that survivor, so the
identity is not confused with the old slot. The recovered compaction path has
no independent removal rule that can be relied on to clear a dead selection;
the observed lifecycle clears it in the retirement or purge path before
compaction. Whether a dead followed record can reach compaction without that
prior clear, and whether a defensive clear is present in an unrecovered
compaction branch, is Unknown. Nanolathe must not substitute a generic pool
policy for this unresolved retail edge. [06 §5.2][08 "Account inventory"]

**Unknown — followed-projectile assignment.** The phase-10 consumer requires
an explicit nullable followed-projectile reference and reads that record's
current/head point. It does not use the projectile's target unit, target
projectile, shooter, or cruise target point as a surrogate. The bounded writer
census recovered the finalization clears and compaction repair above, but did
not recover the assignment store. The implementation seam must therefore
provide an explicit set/clear operation (or event) owned by the gameplay path
that chooses a projectile; it must remain uncalled until that producer is
established. TODO(CRD-006): settle the assignment writer with a whole-image
reference census or a retail input/gameplay trace before implementing a
surrogate selection.

**Established fact — tracked-object validation and clear.** If no higher
priority target is active, phase 10 reads the tracked object's current world
point only while its live/valid state is present. If the reference is
invalid, retail clears the tracked selection and the dependent followed and
in-flight state, then performs no target step from that invalid reference.
The tracked point uses the same desired-origin construction and pre-step
clamp described above. Projectile compaction does not repair a tracked-object
reference: the two identities are separate, and only projectile handles
participate in projectile compaction.

**Unknown — tracked-object assignment.** No current Nanolathe session or
camera type owns this nullable reference, and the bounded retail census did
not recover a direct assignment producer. Order-marker rendering and unit
selection are not evidence of camera tracking and must not be wired as a
surrogate. The public seam is a nullable stable object handle plus an explicit
set/clear operation and a validity/current-point query. TODO(CRD-006): identify
the retail tracking command or producer before connecting that seam.

**Established fact — producer/order boundary.** Host-frame direct scrolling
remains a separate writer: it samples edge/keyboard intent and applies one
bounded signed delta in host order. Phase 10 does not consume held arrows,
pointer edges, the scroll setting, or the host raw delta. A phase-10 pass first
selects and validates a follow producer, consumes an in-flight count when
selected, constructs and clamps the desired origin, and steps current; shake
then consumes its presentation random draws and mutates current in place,
followed by the final camera clamp/view invalidation. The exact outer-frame
interleaving between host scrolling and this phase callback is not established
by the producer census. [07 §10][01 §4.4][03 §5.6]

**Implementation-ready probes.** These probes isolate the closed lifecycle
without assuming an assignment source that remains Unknown:

| Probe | Required result |
| --- | --- |
| Followed projectile retires by impact/expiry or burst completion | Its final current/head point becomes the in-flight anchor; the followed reference clears; the authored transition count is loaded; the record is then removed. |
| Followed projectile is removed by owner purge | The same anchor/count/clear hand-off occurs before the purge compaction. |
| A surviving followed projectile moves left during stable compaction | The followed handle identifies the moved survivor, not the old slot. |
| Retirement has already cleared the followed reference before compaction | No followed-projectile identity remains; do not require compaction to invent one. |
| In-flight count `2`, with both other target families present | Three phase calls select the anchor for counts `2`, `1`, then allow the next priority target after the count reaches `0`; no other target may preempt the anchor. |
| Invalid tracked object with no higher-priority target | Tracking and dependent follow state clear; that invocation does not step toward the invalid point. |
| Target point `(x,y,z)` and viewport `(w,h)` | Desired X is `x - w/2`; desired Z is `z - y/2 - h/2`; clamp desired before applying the signed half-step. |
| Current-to-desired deltas `0, ±1, ±2, ±319, ±320, ±321` | Movement is `0, 0, ±1, ±159, ±160, ±320`, with signed truncation toward zero. |
| One host pass plus `0`, `1`, or `5` phase passes | The host delta is applied once; follow/shake run once per phase pass; held input is never consumed by phase 10. |

The assignment rows are intentionally blocked by their TODOs. A future
implementation test may inject an explicit followed-projectile or tracked
object handle through the public seam, but must label that injection as a
test seam rather than claiming that it is retail's producer.

### 5.7 Construction and water wakes

The construction/nanolathe path uses target footprint and builder position to
build short line segments. There is no wake rectangle: wakes are the
script-emitted strip-2 sprinkles of [R-WATER-01 §1], and they are not a water
surface simulation.

#### There is no wake rectangle: wakes are the script-emitted strip-2 sprinkles, and the rectangle is the selection frame [R-WATER-01 §1]

**Established** (direct static trace of the composer's post-fog
rectangle, of the per-unit overlay the composer keys on the status word, of
the sprinkle container and puff, and of the emit-sfx dispatch; the survival
branch was read at the instruction level).

**The composer's post-fog rectangle is the selection outline.** The
composer's post-fog rectangle pair is the **box-selection outline of
[R-SEL-02A]**: its two world endpoints are the drag corners, its gate is the
drag-active bit of the selection mode word, its outer colour is logical
entry 15 (6 or 4 while the armed latch is MOBILEBUILD) and its inner colour
entry 0. Nothing about it reads a mover, a medium, a wake state, or the sea
level. The per-unit status bit the composer tests in Pass A and Pass B
([R-RAST-01 §7]) is the **selected** bit — the same bit `Ctrl+A` select-all
sets ([07 R-CAM-01 §2]) — and the pass it enables is the selected-unit
footprint quad below, not a wake.

**What a wake is.** A wake is the *impact sprinkle* family of [R-FX-01 §3]:
`emit-sfx` types 2–5 from the unit's COB script ([04 R-COB-03 §6]), spawned
from the emitting piece's vertex 0 toward its vertex 1 (types 4/5 reverse the
two), on strip 2 — which the composer draws in stage 2 (§1), i.e. **under
every feature and unit**. The engine has no wake renderer of its own, no
per-mover cadence, and no medium test at spawn: whether a hovercraft or ship
shows a wake is entirely the script's decision (shipped hover scripts gate on
the medium bands of [04 §9.2]). The container, puff, jitter, half-unit step,
seven-entry colour ramp `0x61..0x67`, `spacing · 6` lifetime and 2×2 raw-index
rectangle are [R-FX-01 §3]'s arithmetic and are not restated here.

**The survival test.** The puff **survives** exactly while `tick ≤ expiry`
**and** `height(pos) < seaLevel` (strict; the bilinear query of
[R-TERR-01 §4], which returns −1 off-map). Both exits — `tick > expiry` and
`height ≥ seaLevel` — return the "erase" verdict, which the caller acts on
when non-zero; only the fall-through returns "keep". So the marks live only
over water (and off-map, where −1 is below any sea level) and die on the tick
they drift onto land at or above the water plane. A clone that inverts the
rule draws wakes on land and never on water.

**The selected-unit footprint quad.** For every
bucketed unit whose selected bit is set, in the same Pass A / Pass B slot and
before the model present, the composer draws a four-line quad:

1. Bounds: the definition's model, **root piece only** (the recursive walk is
   invoked with recursion off), min and max per axis over the root piece's
   vertices plus the root piece offset, both accumulators **initialised to
   0** so the model origin is always inside the box; a piece with fewer than
   three vertices contributes nothing (so single-vertex `wake` pieces never
   widen it). Call the results `A` (min) and `B` (max).
2. Corners, in model space, all on the plane `y = A.y`:
   `(A.x, A.y, A.z)`, `(B.x, A.y, A.z)`, `(B.x, A.y, B.z)`, `(A.x, A.y, B.z)`.
3. Each corner is rotated by the unit's orientation triple (the ordinary
   piece rotation of [R-RAST-01 §2]), then projected with the world-object
   projection: `sx = hi16(rx + ux − camX·65536) + 128`,
   `sy = hi16((uz − camZ·65536) − rz) − (hi16(ry + uy) >> 1) + 32` (`u` = unit
   position, `r` = rotated corner; the rotated Z is subtracted, the 3DO
   handedness flip of [R-RAST-01 §2]).
4. Four one-pixel Bresenham lines corner→corner→…→corner, colour = the
   physical byte the logical-to-physical map holds for **logical entry 10**
   (resolved once, the same map beams use).
5. Gate: a bit of the diagnostic-overlay byte that the settings reader sets
   **unconditionally** at start-up (bits 2 and 3 of that byte are always set
   after settings load; bit 1 mirrors a registry value) — so the quad is
   always on in retail; nothing clears it.

This quad is drawn under the model (it precedes the present in the same
slot) and is not clipped by fog (Pass A/B run before the fog composite).
Document 07 owns selection semantics; this section owns only the drawing.

**Construction segments.** The "short line segments" of the paragraph above
are the nanolathe emitter of [R-P0-19-P] (strip 6); nothing else in the
construction path draws lines.

## 6. Water, lava, and media-dependent impacts

Strict retail presentation has no independently allocated animated water mesh.
Water is terrain tile art plus wakes, splashes, impact GAFs, underwater height/
visibility behavior, and palette-tinted quads. Sea level is a world/simulation
threshold, not a renderer-only decoration.

Each weapon definition can provide land explosion art, water explosion art, lava
explosion art, and corresponding sound aliases. Impact selection tests map type
and sea-level/height. Missing optional GAF lookups leave the pointer empty and
the impact visual absent without crashing. Water weapons and water damage are
separate definition flags from the visual media choice.

The absence of a water mesh is high-confidence within the current bounded
rendering invocation census, not proof that every un-decompiled helper lacks one.

### Water and lava, exactly: what exists, what does not, and the blue table [R-WATER-01 §2]

**Established** unless marked; each negative is a reader census over
the complete decompile export (the binder's holders, the mission fields, and
the runtime map-global words), not a bounded sample.

**1. There are no animated water or lava tiles.** The tile blitter reads no
tick, no frame counter and no phase; a cell's tile index is read once from
the TNT map data and blitted as authored ([R-TERR-01 §1]). The TNT header's
"TileAnims" count and pointer are the **feature records** ([fmt tnt]); no
tile-animation structure exists in the format or the loader. There is no
terrain palette cycling ([§4.3]: the only palette-install-time work is the
gray and blue tables). Water and lava motion in retail is entirely tile art
plus the effects below. **Established (bounded negative over the tile
blitter, the map loader and the palette install; no candidate remains).**

**2. Underwater presentation is a key-plane clip, not a tint pass.** The
complete contract is [R-REN-03A §8] with the ownership rule of
[R-RAST-01 §4]. The pieces that section leaves implicit:

- The per-pixel value it compares is the **height key** of the model raster:
  each vertex carries `key = hi16(modelY) + 50` (`+ 75` more when the
  definition authors `Digger`), interpolated per span into the image's key
  plane by the polygon raster ([R-RAST-01 §1]; in the shadow-doubled path the
  vertex Y is halved first).
- The threshold is `t + 50 [+ 75]` with `t = seaLevel − hi16(unitY)`
  (arithmetic shift, so a unit a fraction below an integer height rounds
  down); the waterline pass runs only when `t > 0`.
- Both helpers compare **inclusively**: a pixel is erased or recoloured when
  `key ≤ threshold`, i.e. when its model height is at or below the water
  plane. The recolour helper additionally skips pixels already equal to the
  image's transparent index; the erase helper sets them to that index.
- Order: waterline (erase or blue) first, then the Digger erase at `key ≤
  125`, then the body blit. The shadow silhouettes get the erase only
  ([R-RAST-01 §4] branches 1–2).

Features (3DO wrecks) take the blue path always ([R-RAST-01 §6]).

**3. The blue table.** The 256-byte blue table is allocated beside the gray table when
the palette window's blue-table feature bit is set, and is **built from
`PALETTE.PAL`** by the same nearest-colour search the gray table uses
(§4.3.3), at session initialisation, once:

- For each palette index `i` with RGB `(r, g, b)`: target colour
  `(r >> 1, g >> 1, (b >> 1) + 50)`. (The builder guards the blue channel
  with `(b >> 1) + 60 < 256 else 255`; for `b ≤ 255` the guard is always
  true, so the `255` arm is unreachable and the target is exactly as
  written.)
- Candidate scan: the palette's indices are first permuted by a **selection
  sort ascending on `r + g + b`** with a strict `<` compare (for `i` in
  `0..255`, for `j > i`: if `sum[j] < sum[i]` swap both the sum and the index
  — an unstable but fully deterministic order; reproduce the loop, not a
  library sort). The scan walks that permutation, skipping entries whose sum
  is below `S − 40` and **stopping** at the first whose sum exceeds `S + 40`
  (`S` = the target's `r + g + b`), keeping the strictly smaller squared RGB
  distance `dr² + dg² + db²` (first wins ties). The result is the palette
  index at the best position of the permutation.
- If no candidate fell inside the window, the position used is the scan's
  exit position (the first entry past the window, or 0 after a complete
  walk) — the same fallback quirk as the gray table.
- `blueTable[i]` = that index. The table is read by exactly one consumer,
  the submerged-hull recolour of item 2. A second 256-byte copy-in routine
  for the same slot exists in the image with **no callers** (dead).

Whether the feature bit that enables allocation can ever be clear in a
retail session was not traced (**Unknown**; decider: trace the palette
window's flag-word initialiser). Every observed path that reaches the
recolour helper has the table allocated; a clone should allocate it
unconditionally.

**4. Splashes: who makes one.** The image binds two water-entry sprites,
`h2oboom2` and `lavasplash` ([R-FX-01 §1]). Their **complete** reader census:

| Producer | When | Art | Gate |
|---|---|---|---|
| debris pool tick and debris draw ([04 R-COB-04 §2]) | a fragment's Y falls to or below `seaLevel << 16` while the terrain under it is below sea level | `lavasplash` when the mission's `lavaworld` is non-zero, else `h2oboom2`, as a fixed-pool sprite at the fragment position | the fragment's splash flag, **and** mission `nosealeveltrigger == 0` (the "opaque liquid mode" flag of [06 §8.2]) |
| projectile water crossing | [06 §7.3] — the weapon's own `waterexplosiongaf`/`lavaexplosiongaf` pair ([06 R-WFX-01 §3]), never these two sprites | — | — |

Nothing else reads either sprite. In particular:

- **Units entering or leaving water make no engine splash** and play no
  engine sound; a script may `emit-sfx` whatever it likes. **Established
  (reader census).**
- **A unit dying over water makes no splash**: the death finaliser stamps
  the corpse with the sinking velocity of [05 "Feature sinking and water
  interaction"] and **suppresses** the 900-tick smoke column that a land
  death emits ([R-LAYER §3]); the two container producers an earlier trail
  labelled "water splash" are that smoke column, and both of its call sites
  fire only above sea level.
- Timing: the debris splash is spawned in the tick the fragment crosses the
  plane (the check is on the post-integration position), and the fragment is
  retired in the same tick; there is no delay and no timer.

**5. Lava: the complete `lavaworld` reader census.** The mission's
`lavaworld` integer is read by exactly five sites: (i) the OTA parser; (ii)
the weapon-definition loader, which fills the single water-or-lava art holder
from the lava pair instead of the water pair ([06 R-WFX-01 §3]); (iii) the
debris splash art choice above; (iv) the load-time **lava flood** of
[R-TERR-01 §2] (every cell whose height byte is `≤ seaLevel` and whose
feature slot is empty or fringe becomes void); (v) the commander-respawn
candidate rejection of [08 R-SKIR-01 §3] (`height ≤ seaLevel` rejects). There
is **no lava damage rule**: the only medium damage in the engine is the
water-damage packet of [04 §9.2], driven by the mission's `waterdoesdamage`
and `waterdamage` and by `height ≤ seaLevel`, which a lava map authors like
any water map. Nothing tints, animates or recolours lava. **Established
(reader census).**

**6. Wind and tide have no presentation consumers.** The runtime wind range
and tidal word written once at map load ([R-TERR-01 §6]) are read by exactly
four sites: the wind jitter of [05 R-PROD-01 §3] and the tidal settlement of
[05 R-PROD-01 §4] (the two generators' income), and the AI's per-player work
and classifier ([05 R-PROD-01 §1]). The smoke drift of [06 R-WFX-01 §5] reads
the jitter's *current* wind vector, not the range. The mission fields
themselves (`minwindspeed`, `maxwindspeed`, `tidalstrength`) are also read by
the front-end map-description screen (`Wind Speed` text; documents 07 and 08) and by
nothing else. There is no weather system: no rain, snow, cloud, lightning or
storm producer exists in the image; the meteor shower is a mission-scripted
projectile schedule owned by [08 "Meteor showers"]. **Established (reader
census).**

**7. `TRACKTYPE` / `TRACKMODE`** are the CD-audio track fields of
[R-AUD-01 §4]; they have nothing to do with vehicle tracks or wakes. Not
re-traced here.

## 7. Fonts, text, GUI, and input-owned presentation

### 7.1 FNT software glyphs

The FNT path loads a bitmap font, measures strings, clips/truncates to a width,
and rasterizes glyph rows directly into the indexed framebuffer. Glyph bits are
1-bit, most-significant-bit first; set bits write the current text color. Newline
is byte value 10. The active font, the foreground/background colour pair and
the transparent-colour sentinel are software-renderer state.

The third colour field is the *skip* colour: a palette index that the glyph
rasterizer refuses to write ([R-FONT-01 §4]). There is no shadow flag
anywhere in the text primitives; every shadow or outline seen in retail is a
caller drawing the string more than once at pixel offsets. There is no
kerning.

The renderer does not use GDI `TextOut` for gameplay text. GDI remains
present for window/palette presentation; whether any shell dialog draws text
through GDI is still open (see the Unknown list at the end of §7.1).

#### The FNT record as the executable reads it [R-FONT-01 §1]

**Established, direct-static.** An FNT file is loaded as raw bytes through the
ordinary VFS file loader and used in place; no parse or conversion step
exists. Every text primitive reads the same four single bytes at the head of
the record, followed by the offset table:

| byte | meaning as read | retail values (25 unique `.fnt`) |
|---|---|---|
| 0 | glyph height in rows; every glyph of the font has this many rows | 9–17 |
| 1 | never read by any routine | always 0 |
| 2 | **vertical offset**, a *signed* byte: glyph rows are placed starting at `penY − offset` | 1, 2 or 3 |
| 3 | **first character code**: the offset table is indexed by `code − first` | always 0 |

The table at byte 4 holds one little-endian 16-bit entry per code from
`first` upward; an entry of 0 means "no glyph". Nothing bounds the table on
the high side — the routines index `(code − first) & 0xFFFF` for any byte
`≥ first` — so a font whose first code is 0 must carry 256 entries, which is
what every retail font does. A glyph record is one byte of **advance**
followed by `advance × height` bits packed MSB-first with no row padding
(`[fmt fnt]` owns the layout). There is no per-glyph height, no bearing, no
baseline table and no kerning table: the pen moves by exactly the advance
byte and the only vertical datum is header byte 2.

#### The width measurer [R-FONT-01 §2]

**Established, direct-static.** `width(font, s)` walks `s` until a NUL or a
byte 10 (newline) and sums the advance byte of each glyph that exists:

```
w = 0
for each byte c of s, stopping at 0 or 10:
    if c >= first and table[c - first] != 0:
        w += advance(c)
return w
```

Bytes below `first` and bytes with a zero table entry contribute nothing and
do not stop the walk. A null font or a null string measures 0. The same loop
is inlined verbatim in the drawer, the centred-text helper and the outlined
text helper; there is no cache. Newline terminates measurement, so a
multi-line string measures its first line only.

#### The string drawer: truncate, then whole-rectangle clip [R-FONT-01 §3]

**Established, direct-static.** `draw(dst, s, x, y, maxW)` runs in this order:

1. **Measure** `w = width(activeFont, s)` as in §2.
2. **Truncate.** When `maxW != −1` **and** `maxW < w` (strict — a string
   exactly `maxW` wide is kept whole), the string is copied through a
   bounded copy of at most 299 bytes into a 300-byte stack buffer, and then
   trailing bytes are removed one at a time — each removal followed by a
   full re-measure — while `maxW < w` still holds. The loop also stops when
   the buffer is empty. Nothing is appended (no ellipsis), and the caller's
   string is not modified. A string longer than 299 bytes is cut to 299
   before the width loop begins. Doc 07 §7 already states truncate-before-clip;
   the arithmetic is as here.
3. **Form the rectangle** `[x, y, x + w, y + height]`, where `height` is
   header byte 0 of the active font and `w` is the (possibly truncated)
   width. Note that the rectangle uses `x + w` and `y + height` — one past
   the last column and row — and ignores header byte 2 entirely.
4. **Clip test.** The destination's clip rectangle `[L, T, R, B]` (inclusive
   last column and row, [R-RAST-01 §1]) is copied and the text rectangle is
   accepted only when it lies **wholly** inside it, every comparison
   inclusive: `L ≤ x ≤ R`, `L ≤ x + w ≤ R`, `T ≤ y ≤ B`, `T ≤ y + height ≤ B`.
   Any failure drops the whole string; there is no partial clipping and no
   per-glyph or per-pixel clipping anywhere in the FNT path.
5. **Rasterize** (§4) with the active font, the context foreground,
   background and skip colours, unclipped.

**Edge behaviour that follows (Established).** Because the rectangle's right
and bottom are exclusive coordinates tested against inclusive bounds, text
whose last column would land exactly on the clip's last column is rejected —
one column of slack is required on the right and one row at the bottom.
Because the rectangle ignores the vertical offset, a font with offset `k`
draws its top `k` rows *above* `y`, outside the tested rectangle; a string
with `y == T` therefore writes `k` rows above the clip top. The retail
offsets are 1–3, so the overrun is at most three rows.

**Null destination (Established).** A null destination pointer makes the
drawer lock the default presentation surface, draw through the same test
against that surface's clip rectangle, and unlock; if no surface can be
locked nothing is drawn.

#### The glyph rasterizer, the skip colour, and how shadows are made [R-FONT-01 §4]

**Pen and row placement (Established, direct-static).** The rasterizer takes
the surface base and stride, the font, the string, `(x, y)`, and three colour
bytes `fg`, `bg`, `skip`. The first destination byte is
`base + (y − offset) × stride + x` with `offset` the signed header byte 2;
the string is walked with the same NUL/newline stop and the same
`first`/table lookup as §2. For each present glyph it reads `advance × height`
bits, one glyph row per surface row, most-significant bit first within each
source byte and with the bit stream continuing across rows without padding.
A set bit selects `fg`, a clear bit selects `bg`; the selected colour is
written **unless it equals `skip`**, in which case the destination byte is
left alone. After each row the destination advances by `stride − advance`;
after the last row the pen advances by `advance` and the next glyph starts
at the same top row. Absent glyphs neither advance nor draw.

**Colour state (Established).** The three bytes live in the display
context beside the active font. The setter for the pair treats `−1` as
"keep"; the skip colour has its own setter and getter. The context
constructor does not initialise any of the three (they start at whatever
the allocation held — zero in practice). Frontend initialisation installs
**skip = 254** once, and every observed caller sets the pair by reading the
skip colour and passing it as the background, so retail text is drawn with
`bg == skip`: clear bits write nothing and the glyph is transparent. The
value 254 is the sentinel doc 07 §7 calls the "transparent palette sentinel";
it is a plain palette index, and a foreground of 254 would also be invisible.
The ninth argument of the rasterizer is this skip colour, not a shadow
toggle.

**Shadows and outlines (Established).** They are caller compositions of the
plain drawer:

* The GUI label painter ([R-FONT-01 §6]) with the label's shadow attribute
  bit draws the string first in the window's colour-table entry 0 at
  `(penX + 1, penY + 3)`, then in the label's foreground at `(penX, penY)`.
* The end-of-battle report's `Click to continue` prompt uses the outlined
  helper: `penX = trunc((surfaceWidth − w) / 2)` (arithmetic shift, so a
  negative difference rounds toward −∞), then the string is drawn four times
  in the outline colour at `(penX − 1, y)`, `(penX + 1, y)`, `(penX, y − 1)`,
  `(penX, y + 1)`, then once in the fill colour at `(penX, y)`; each pass
  sets the background to the current skip colour so only set bits write. The
  outline colour is the active palette's entry for logical index 0 and the
  fill colour that for index 15, both through the logical-to-physical map
  ([03 §4]). A plain centred helper with the same `penX` and one pass exists
  but has no callers.

#### Which fonts are loaded, by whom, and which routine draws which family [R-FONT-01 §5]

**Startup preloads (Established, direct-static).** Before the shell opens,
`fonts/COMIX` and `fonts/SMLFONT` are loaded into two global slots. A null
load result of either raises the fatal modal (message box, then process exit)
with the composed path as the message — there is no fallback font. Both are
freed together at shutdown. The GUI window record is then given `fonts` as
its font directory, `COMIX` as its window FNT, the two GAF fonts
`anims/hattfont12.gaf` (slot 0) and `anims/hattfont11.gaf` (slot 1) through
the GAF-font loader (§6; a missing GAF font is a null slot, not fatal), skip
colour 254, and finally `COMIX` is made the active FNT.

**Font selection sites (Established).** The active-FNT setter ignores a null
handle. Its callers, by the handle they pass:

| handle | who selects it | what it draws |
|---|---|---|
| the window FNT (`COMIX`) | 21 sites: every GUI screen painter restores it after a per-gadget font, the shell entry, the report/end-mission screens | shell text drawn through the FNT fallback of the GAF-font path (§6) and the label/button painters when the gadget's `fontnumber` selects no font record |
| a per-gadget FNT | the GUI label, button, list and text-input painters, the shared select-font-by-gadget helper and its callers (the text-input focus paths, the text-region wrapper, the message box) | the FNT of the **font record** (kind 7) the gadget's `fontnumber` selects from the same window — the n-th kind-7 record counting from 0, so `fontnumber` 0 is the window's first record (the walk is below); that record's FNT is loaded at GUI parse from the window's font directory plus the record's `filename` through the same raw file loader (missing file → null handle → the setter ignores it and the previously active font stays) |
| the local player's **side font** (`font=` of `sidedata.tdf`, [02 §6], one handle per side record) | the battle frame composer (twice) and the unit-panel painter | every HUD number and string drawn with the FNT drawer in battle — resource counters, `FRATE`, the unit-panel readout, the group digit of [R-FX-01 §6] |
| `SMLFONT` | the developer terrain pass inside the frame composer (two sites) | mode 1's `G` markers on acceptable path-search terminal cells, and mode 3's decimal per-cell metal values (§3.12); neither is a minimap label |
| `COMIX` directly | the main-menu screen and the front-end state machine; in battle, the frame composer's diagnostic overlay, the unit-state and unit-builder probes, the unit panel's debug readout, and the status footer | `FRATE`, `Release`, `MODE`, `Game Time` and the profile labels; the probe dumps; the footer, which then draws through the GAF-font path of §6 |

**The per-gadget font walk (Established).** The four gadget painters that
draw text — label, button, listbox and text input — each open with the same
walk the shared select-font-by-gadget helper of [07 R-WGT-01 §6] performs,
inlined: a counter starts at 0; every kind-7 record of the window, in gadget
order, compares the counter against the painted gadget's `fontnumber` (read
as a signed byte) and either matches — its FNT becomes the active font — or
advances the counter. A `fontnumber` of 0 therefore selects the window's
**first** font record; there is no "names a font gadget" gate, flag or
non-zero test. When no record matches (the number is at or past the record
count, or is 128..255 and reads negative), the common font — the window FNT,
`COMIX` — is selected instead and the walk reports −1. Only the **label**
painter keeps that report: with a match it draws through the FNT drawer
directly, otherwise through the GAF pen. The button, listbox and text-input
painters discard it and draw every string through the GAF pen regardless, so
the FNT they selected is reached only on the pen's null-slot fallback (§6).
On a stock side page — one kind-7 record, `armbutt`/`corbutt`, and every
button authoring `fontnumber` 0 — each product button therefore makes
`armbutt`/`corbutt` the active FNT and then draws its caption in the slot's
GAF font all the same; the record would show only with `hattfont12.gaf`
missing. On `MSNBRIEF`, whose records are `smlfont`, `armfont`, `corefont`,
the buttons author `fontnumber` 9 (no record: common font) and the `MOREBAR`
label authors 0, so its caption is `smlfont` through the FNT drawer.

**Which routine draws which family (Established).** Battle HUD text goes
through the FNT drawer directly with the side font, except the diagnostic
overlay and probes above (`COMIX`) and the status footer (GAF-font path with
`COMIX` as its fallback). Shell text goes through
the GAF-font trio of §6, which prefers the window's GAF font and falls back
to the active FNT only when that slot is null; of the gadget painters, only
the label painter uses the FNT drawer directly, and only for a label whose
`fontnumber` selected a font record (the walk above). The list
and label painters switch the window's GAF font to slot 1 (`hattfont11`) for
their text and restore slot 0 afterwards, and so does the button painter when
the button's small-font attribute bit `0x8000` is set; a button without that
bit draws its caption with slot 0 (`hattfont12`), which is what a side page's
build count does (below). The kind-13 score-bar painter of §6 selects slot 1
the same way the list and label painters do. GDI text is not used by any of
these paths.

**The side-page build count (Established; asset census).** There is no
build-count routine that selects a font: the count-label writer stores the
number in the product button's own text slot ([07 R-P0-11 §2]) and the GUI
button painter draws it as that button's caption (§6, "Side-page build
count"), so it takes the button rule — the window's current slot, slot 0
(`hattfont12`), unless the button authors `0x8000`. No count-bearing product
button authors it: an asset census over the reference install's `guis/*.gui`
finds all 488 kind-1 buttons carrying `commonattribs` 4 or 8 authoring
`attribs = 32` and a 64x64 rectangle, and none of them `0x8000`. A side-page
build count therefore draws in `hattfont12`, not `hattfont11` — and not in
the page's own `armbutt`/`corbutt` record either, which the button's walk
selects as the active FNT (above) but the GAF pen never consults while the
slot holds a font.

#### The GAF-font pen: measure, metric, draw, wrap, and the gadget painters [R-FONT-01 §6]

Doc 07 §4 records the GAF-font loader's baseline rule (the capital-I
frame's height is subtracted from every frame's Y offset once at load) and
the blitter placement `penX − XOffset, penY − normalizedYOffset`. This section
records the pen arithmetic.

**Measure (Established, direct-static).** With a GAF font in the window's
current slot, `gafWidth(s)` sums, for every byte of `s` until NUL (newline
does **not** stop it), the width of the frame at index `byte`, adding nothing
for a byte whose frame index is out of range. With a null slot it is
`width(activeFont, s)` of §2.

**Line metric (Established).** `metric = height(frame['I']) + 2` with a GAF
font, else header byte 0 of the active FNT. This is the "capital-I height
plus two" of doc 07 §5.

**Draw (Established).** `gafDraw(dst, s, x, y, maxW, mode)`; with a null
slot it calls the FNT drawer with `maxW = −1` (the caller's width limit is
**dropped** on the fallback path). Otherwise, for each byte `b` until NUL:

```
if b < 0x20: skip (no advance, no draw)          -- control bytes, incl. 10 and 13
f = frame[b]; if none: skip (no advance)
if maxW != -1 and width(f) > maxW: stop           -- strict: a glyph exactly maxW wide still draws
if b != 0x20: blit f at (x, y) with mode
if maxW != -1: maxW -= width(f)
x += width(f)
```

The space frame advances but is never blitted. `mode == 0` selects the plain
frame blitter (opaque bytes copied, no remap — doc 07 §4's "GAF-font bytes
are copied directly"). A non-zero `mode` selects the keyed blitter, which
for a compressed frame remaps every source byte through row `mode` of the
32-row light table ([R-P0-19-N], [R-FX-01 §4]) — the table the
light-level remapper of doc 07 §4 uses — so `mode` is a light-table row
index, not a palette index. The GUI label painter passes the label's
authored foreground field as `mode`; the button painter always passes 0.
**Established — raw and composite modes differ.** A raw leaf treats `mode`
as its transparency key and writes `LHT[source × 256 + destination]` for
each other byte; it does not offset the table by `mode`. No source-row clamp
is applied, so authored indices beyond the table's rows are unsafe input.
A composite sends every child through ALP tint, independently of the child
selector, after the parent light-table gate; the child alpha capability gate
also applies. A single flattened raster cannot express these distinct paths.

**Word wrap (Established, direct-static).** `gafWrap(dst, s, x, y, maxW,
maxH, mode)` returns the pen Y after the last line. It splits on the space
byte and on byte 13 (carriage return; byte 10 is a control byte the drawer
skips), fits greedily, and modifies the string in place only transiently:

```
start = 0; lastBreak = 0; i = 0; L = strlen(s)
loop:
  advance i to the next ' ' or '\r' at or after i, or to L
  w = gafWidth(s[start .. i))                     -- the whole candidate line
  if maxW < w:                                     -- strict; a line exactly maxW wide fits
      line = s[start .. lastBreak); i = lastBreak  -- back up to the previous break
  elif s[i] == '\r' or i == L:
      line = s[start .. i)
  else:
      lastBreak = i; i += 1; continue              -- keep accumulating words
  gafDraw(dst, line, x, y, maxW, mode)
  y += 2 + metric; maxH -= 2 + metric
  if s[i] == NUL: return y
  start = lastBreak = i + 1                         -- skip the break byte
  if maxH < 1: return y
```

Consequences: the line pitch is `metric + 2` (fourteen pixels for
`hattfont12`); a first word wider than `maxW` produces an empty line, and
the walk then resumes one byte later, so an over-long first word loses its
leading bytes one per line until the remainder fits; a line that fits
exactly is not broken; `maxH` is checked only after a line is drawn, so at
least one line is always drawn.

**Label painter (Established).** For a label gadget with rectangle
`(gx, gy, w, h)` (the panel itself has `gx = gy = 0`), `right = gx + w − 1`,
`bottom = gy + h − 1`, `tw` the text width by the active family:

* an authored `x` of `−1` is replaced once by `trunc((panelWidth − tw) / 2)`;
* attribute bit 4 (right): `penX = gx + w − tw`; else bit 2 (centre):
  `penX = gx + trunc(w / 2) − trunc(tw / 2)` (two separate truncations); else
  `penX = gx`; `penY = gy` in every case;
* with a font record selected by the painter's opening walk (§5; the label's
  `fontnumber` counted the window's kind-7 records from 0, so 0 is the first
  record) — the FNT path: optional shadow as in §4, then the FNT drawer at
  `(penX, penY)` with `maxW = −1`;
* otherwise (GAF path): when `2 × metric < h − 1` the wrapper is used with
  `maxW = w`, `maxH = h`; else the single-line drawer with `maxW = w`.

**Button painter (Established).** The painter opens with the same font walk
as the label painter (§5) and makes the selected record — or the common
font — the active FNT, but it does not keep the walk's result: every caption
run below goes through the GAF pen, so the selected FNT draws only on the
pen's null-slot fallback. With `s = 1` when the
gadget's `stages` field is non-zero, else 0:

* `penY = gy + trunc((h − 1 − metric) / 2) + s` (C division, truncation
  toward zero);
* left attribute (bit 1): `penX = gx + 3 + s`; else right (bit 4):
  `penX = max(gx, right − 3 − tw)`; else centred:
  `penX = gx + trunc((right − tw − gx) / 2) + s + 1`; in every case
  `maxW = w` and `mode = 0`;
* the build-attribute variant (bit 0x20, when neither left nor right nor
  centre is set) keeps the centred `penX` and uses `penY = bottom − 4 −
  metric + s`, then draws the caption as three runs — the prefix in the
  button colour, the quickkey letter in map entry 10, the remainder in the
  button colour again, each placed after the measured width of the previous
  run ([07 R-WGT-01 §3]).

The pressed/held state never moves the pen — only `stages` does — which is
what doc 07 §5 states.

**The FNT foreground each painter installs (Established).** The FNT drawer of §3 draws in the display context's
foreground/background/skip bytes (§4), and nothing installs them on the
painter's behalf: each gadget painter runs the pair setter itself, directly
ahead of its text call, reading the skip colour back as the background (so
clear bits stay transparent) and choosing the foreground per kind. The GAF
path runs the very same setter call and then never reads the result — the
GAF pen colours from the frame bytes or the `mode` row — so the two paths
differ only in what consumes the installed byte, never in what is installed.
"Map entry *n*" below means entry *n* of the window's GUIPAL-to-display map,
the 256-byte nearest-colour lookup of [07 "Retail palette contract"] and
[03 §4.3]; "raw" means the byte is written as a physical palette index
without that lookup.

* **Button (kind 1).** Foreground = map entry `colorf` when `stages` is
  zero, map entry **0** when `stages` is non-zero; installed once before the
  caption and again before each run of a split caption (the quickkey runs,
  [07 R-WGT-01 §3]; the accelerator run of the build-attribute branch uses
  map entry 10 between them). The builder zeroes `colorf` at open and the
  flash decay returns it to 0 ([07 R-WGT-01 §1][07 R-WGT-01 §12]), so a
  button that is not mid-flash draws an FNT caption in map entry 0 whatever
  the file authored (stock content authors 15 on most buttons and 0 on the
  rest; the authored value is never read). A flashing stage-less button
  draws its FNT caption in map entry `colorf` for as long as the word is
  non-zero — a different entry each tick as it decays — where the GAF path
  lightens the glyphs through light-table row `colorf` instead; this is the
  one place the two paths visibly diverge.
* **Label (kind 5).** Foreground = the gadget's `colorf` word **raw** — a
  physical palette index, not a map entry. The shadow pass (attribute bit
  8, FNT path only) is drawn first in map entry 0. A label reaches the FNT
  drawer two ways — directly when its `fontnumber` selects a kind-7 record
  (§5's walk: the n-th record counting from 0, so 0 is the first), and
  through the GAF pen's null-slot fallback when no record matches and the
  window holds no GAF font — and the same raw `colorf` is installed ahead of
  both. Because the builder zeroes the word, an authored label
  whose window draws with an FNT and whose screen never sets its colour is
  drawn in palette index 0; the screens that build labels at run time (the
  message box, the briefing pager) write the word themselves
  ([07 R-FE-02 §5]). Two further label facts: a non-zero `colorb` first
  fills the label rectangle with map entry `colorb` (every stock label
  authors 0), and a label with a quickkey draws that letter's underline —
  the one-pixel row `penY + metric − 1` under the letter — in map entry 2.
* **Picture box (kind 12).** Draws no text at all and installs no colour:
  the painter blits frame 0 (plain when `colorf < 1`, keyed through
  light-table row `colorf` otherwise) and darkens the rectangle by 28 steps
  when its grey bit is set. A caption on a picture box has no retail
  counterpart.
* **Text input and listbox** (for contrast): map entry `colorf`, installed
  when the input takes focus ([07 R-WGT-01 §6]) and per row by the list
  painter ([07 R-WGT-01 §4]).
* **Side-page build count.** The count the count-label writer stores in a
  product button's own text slot ([07 R-P0-11 §2]) is drawn by the button
  painter above, so it takes the button rule: product buttons are kind 1 and
  almost all author `stages = 0` (587 of the 614 build-attribute buttons in
  the reference install; the rest are the stockpile and paged toys), so the
  number is drawn in map entry `colorf`, which is map entry 0 unless the
  button is mid-flash.

**Kind-13 score-bar painter (Established).** The score-bar painter of
[07 R-HUD-03 §11] (the end-of-mission bars) writes the window record's
current-GAF-font word from **slot 1** (`hattfont11`) as its first statement
and restores it from slot 0 as its last — the same two writes the list and
label painters make. Between them it draws the bevel (the map-entry 0/17/20
frame of [07 R-FE-02 §4]), two fills taking the gadget's `colorb` (inner
background) and `colorf` (progress) bytes raw, and (when `showNumber` is
set) the decimal count through the GAF pen at `(gx + trunc(w/2) −
trunc(tw/2), gy + trunc(h/2) − trunc(metric/2))` with no width limit and
mode 0, measuring the width and the capital-I metric against slot 1. It
installs no foreground: the decimal is drawn with the context bytes exactly
as the previously painted gadget left them. So the end-of-mission bar's
number is `hattfont11`, one point smaller than the `hattfont12` a plain
button caption draws in. The side-page build count is a button caption
(above) and never passes through this routine.

#### Code page and character mapping [R-FONT-01 §7]

**Established.** Both families map a string byte to a glyph by its raw
value: the FNT path by `byte − first` into the offset table, the GAF path by
`byte` as a frame index. No text primitive translates bytes, folds case, or
consults a code page; bytes 128–255 draw whatever glyph the font carries at
that position (the retail 222/223-glyph fonts carry Windows-1252 shapes
there, [fmt fnt]). Whether any string *producer* translates before drawing
is a doc 07 question (its translation-table path) and is listed there.

#### Unknown — §7.1, open items only

- Whether any shell dialog draws text through GDI rather than the FNT/GAF
  paths · decider: import-table and reference census of `TextOut`/`DrawText`
  callers.
- The keyed GAF blitter's uncompressed-frame path: whether the `mode` byte
  selects the same light-table row as the compressed path, or is used as a
  colour key · decider: static trace of the raw keyed writer (it is a
  hand-written span routine; [R-P0-19-P] owns its compressed sibling).
- Malformed `hattfont` (no frame at the `I` index): the loader subtracts an
  uninitialised stack value from every frame's Y offset, so the outcome is
  undefined by construction; whether a stock or third-party asset ever
  triggers it · decider: asset census over installed GAF fonts. Doc 07 §5
  and §7 carry the matching `TODO(question)`.

### 7.2 GUI and HUD

GUI parsing consumes side-data and GAF panels, with a gadget/control structure
and a 640-by-480 shell coordinate system. Battle chrome uses panel-top,
panel-side, panel-bottom, integer GAFs, and per-side data. The HUD has selection,
resource, health, order queue, minimap, and chat/end-game components; these are
software compositions over the indexed framebuffer.

Input is polled through `GetAsyncKeyState`, `GetKeyState`, cursor position/focus
queries, and `SetCursorPos`. There is no DirectInput or raw-input contract.
Clipboard operations use `OpenClipboard`, `GetClipboardData`, and
`GlobalLock`. Edge scrolling compares cursor position with viewport borders and
changes integer camera scroll by configured speed. The complete key-token map,
repeat rate, focus-loss behavior, and all gadget hit-testing are not settled.

## 8. Audio backends and event arbitration

### 8.1 Backend selection

Startup chooses among three established paths ([R-AUD-01 §1]):

1. DirectSound via `DirectSoundCreate`, cooperative level, a primary buffer,
   and a PCM format of 11,025 Hz, 16-bit, stereo;
2. Win32 `PlaySoundA` when `UseWindowsSound` is selected. This path is used
   for legacy/frontend or configured Windows sounds and forces DirectSound
   off; and
3. **silence** when DirectSound is disabled or unavailable — there is no
   waveOut playback backend (the executable imports no
   `waveOutOpen`/`waveOutWrite`; WinMM is used only for volume and MCI), and
   every play gate tests the no-DirectSound flag and returns without playing.

Initialization failure handling is exact: a `DSERR_NODRIVER` failure sets
the no-DirectSound flag silently; any other DirectSound failure shows the
message box `Error:  Sound system initialization failed.` (two spaces after
the colon). `NoDirectSound` and `UseWindowsSound` are `totala.ini`
`[Preferences]` integers, not registry keys; the latter sets the
no-DirectSound flag as well, so every cue thereafter plays through the
Windows sound API. CD audio initialization is always attempted independently
of the sound-system result.

DS3D buffers *are* used — every real sample is a `0x92` static buffer with
`CTRL3D`, live for the session and duplicated on demand — and the positional
helper of section 8.3 sets a DS3D position and the minimum/maximum distances
whenever Sound Mode is `3D`; in `Mono` mode a two-level in-view/off-screen
attenuation applies instead. No listener is ever configured. The `{size
0x14, flags 0x82, bytes}` descriptor with size `rate · channels ·
bytesPerSample · 2` belongs to the streaming path of [R-AUD-02 §1].

### 8.2 WAV decoding and cache

**Established — container detection and sample bytes.** The fixed-signature
classifier, DIGI sample-rate conversion and complete payload, RIFF first-chunk
selection and unpadded stride, and raw fallback are owned by `[fmt wav]`,
`[02 §7]` and `[02 R-MALF-01 §10]`. DIGI retains its complete PCM
payload without discarding any sample prefix. RIFF's authored format tag, byte rate and alignment are
not playback inputs: retail derives alignment from the declared channels and
bit width. A partial final PCM frame is not played.

**Established — failure at playback.** Unreadable samples and failed container
reads return a null sample and silence. Nanolathe's checked parser and supported
PCM adapter use explicit errors at their loading boundary; the optional audio
caller suppresses playback on failure. Rejecting unsupported codecs, invalid
PCM widths/rates and unsafe allocations is a host-safety policy, not retail
format-tag validation `[fmt wav "Nanolathe parser and playback policy"]`.
**Established — wrapper boundary.** Static/transient creation passes the
selected length directly to DirectSound, including zero for empty raw input,
without a local rejection or repair. Failed creation, lock, short read or
unlock yields null. Streaming capacity is derived from two seconds of PCM,
independently of payload length; empty raw input uses ordinary capacity and
EOF silence fill if the device request succeeds. **Unknown:** the target
DirectSound implementation's acceptance of zero-byte static buffers and
unusual PCM parameters. A backend inspection or manual observation is needed;
another wrapper trace will not settle it `[02 R-MALF-01 §10]`. Nanolathe
retains its explicit empty-input rejection.

**Allocation modes.** Each decoded sample is dispatched by a mode chosen by
the *caller*, never by an authored field ([R-AUD-01 §1]): mode 0 creates the
static DirectSound buffer every registered alias gets; mode 1 is
load-and-play for unit voice lines and the options preview; mode 2 is the
streaming path of mission narration and glamour sounds ([R-AUD-02 §1]). The
three dispatch sites and the fail-to-silence rule are established.

**Caching.** Samples are cached at the alias level: one static buffer per
alias, retained for the life of the session and duplicated on demand for up
to four simultaneous instances ([R-AUD-01 §1]), with no eviction beyond the
alias cap of 8.3; there is no pool or LRU. Nanolathe's alias cache likewise
retains entries until explicitly released; it has no FIFO capacity eviction.

### 8.3 Sound categories and eight-slot arbitration

The sound-category catalog's authored grammar, its fixed event list, and its
variant-gathering rule are specified in document 02. Each category record is
352 bytes: a name of up to 64 bytes followed by 24 event rows of twelve bytes
each, indexed by event slot, where slot zero is an unused sentinel and slots 1
through 23 are the events. A row holds a variant count and two parallel arrays
of 64-byte strings: the sound alias and its speech caption.

**Static slot table.** Alongside the categories the executable carries one
static record per slot, holding the slot index, a **priority**, a **cooldown
multiplier** (the window is `multiplier × 30` frames), the authored key name, a default speech caption, and a mutable *next-allowed
frame* cache. Priorities and cooldowns are per slot and global across every
unit, not per category:

| Slot | Priority | Cooldown | Key | Default speech |
|---:|---:|---:|---|---|
| 1 | 10 | 0 | `select` | — |
| 2 | 9 | 20 | `underattack` | Under Attack |
| 3 | 4 | 2 | `activate` | — |
| 4 | 4 | 2 | `deactivate` | — |
| 5 | 5 | 1 | `ok` | — |
| 6 | 3 | 4 | `arrived` | Arrived |
| 7 | 8 | 1 | `cant` | Cannot Comply |
| 8 | 3 | 3 | `unitcomplete` | Nanolathe Complete |
| 9 | 4 | 2 | `build` | — |
| 10 | 3 | 1 | `repair` | — |
| 11 | 2 | 1 | `working` | — |
| 12 | 7 | 1 | `load` | — |
| 13 | 7 | 1 | `unload` | — |
| 14 | 7 | 1 | `cloak` | Cloaked |
| 15 | 7 | 1 | `uncloak` | Visible |
| 16 | 4 | 1 | `capture` | — |
| 17 | 10 | 0 | `count5` | five |
| 18 | 10 | 0 | `count4` | four |
| 19 | 10 | 0 | `count3` | three |
| 20 | 10 | 0 | `count2` | two |
| 21 | 10 | 0 | `count1` | one |
| 22 | 10 | 0 | `count0` | zero |
| 23 | 10 | 0 | `canceldestruct` | Self destruct terminated |

**Queue.** Cues do not play directly. They enter an eight-entry queue whose
header carries a count and a base time; each entry holds the slot, the frame it
was enqueued, the unit, an optional override speech line, and the slot's
priority.

**Insert(slot, unit, text):**

1. If the current frame is before the slot's next-allowed frame, drop the cue.
2. If any queued entry already holds that slot, drop the cue — duplicate slots
   never queue twice.
3. If the queue is full, resolve the last entry **silently**, printing its
   speech line but playing no sound, free its text, and shift it out.
4. Insert so the queue stays sorted by descending priority, placing the new
   entry *after* entries of equal priority, which makes equal priorities
   first-in-first-out.

**Resolve(entry, audible, showText):**

1. Look up the acting unit's sound category, then that category's row for the
   entry's slot.
2. Draw the variant uniformly: `idx = (uint64)rand15 × count ÷ 0x8000`
   (32768), truncating toward zero, where `rand15` is the Microsoft CRT stream
   (`x' = x × 214013 + 2531011`) sampled at bits 16..30. The draw happens on
   **every** resolve — including silent ones — before any gate, so the CRT
   stream advances with queue pops, not with audible successes. Count zero
   produces no pick and the cue is silent.
3. If audible, the crowding gate `10 - audioThreshold < priority` (signed byte
   compare) passes, the row has at least one variant, the audible flag is set,
   and sound-flags bit 6 is set, play the chosen alias and set the slot's
   next-allowed frame to `frame + cooldownMult × 30`. The dispatch layer
   additionally requires the master gates — effects volume nonzero,
   sound-flags bits 0..2 nonzero, DirectSound enabled. The re-arm happens
   **only on an audible pass** — never on a silent resolve — and the codec
   performs no empty-path check, so the re-arm happens even when the resolved
   path is empty.
4. If showing text and the gate `10 - speechThreshold < priority` (signed byte
   compare) passes, take the entry's override line or the row's caption for
   the chosen variant, and print it as `"%s: %s"` prefixed by the unit name
   when the unit is still alive (its chat-enable latch bit set) and the
   caption is non-empty. Otherwise nothing prints.

When the second reentry flag (the honk/sing flag) is set, the alias is
replaced by a fixed cue: `sing` unless `(frame / 30) & 7 == 0`, then `honk`.
The writer is the `Sing` interface option [07 R-CAM-01 §7] ([R-AUD-01 §3]).

**Drain**, once per rendered frame and outside the simulation: if the queue is
empty do nothing; if the current frame is within 30 frames of the base time,
resolve the head silently; otherwise resolve the head audibly and reset the
base time. Then free the head's text and shift it out.

Consequences a reimplementation must preserve:

- **At most one voice is audible per 30 frames.** The sorted eight-deep queue
  decides which pending cue wins that window.
- A cue popped inside the window still prints its speech line but is silent.
- Full-queue eviction resolves the *last* entry silently rather than dropping
  it or playing it immediately.
- Cooldowns are per slot and global across all units, and re-arm only on actual
  audible playback, never on a silent resolve.
- Priorities come solely from the static slot table. The two crowding
  thresholds are separate menu-scaled bytes, one for audio and one for speech.

**Variant gathering.** For each slot the loader reads the bare key and then the
numbered keys `key1`, `key2`, ... until a number is missing. Each present key's
alias string (up to 64 bytes) and its parallel `key` + `text` caption (up to 64
bytes) are appended to the row's two arrays and the count increments. The
numbered loop runs **regardless of whether the bare key is present** — a
divergence from the spec letter in document 02, which gates on the bare key
first. The stock corpus ships no bare forms at all for the core cues (120
`select1`, 76 `ok1`, 76 `cant1`, 63 `arrived1`), and retail still counts one
variant for each via the numbered path; a strict bare-gate would mute them.
Nanolathe therefore gathers numbered keys regardless of the bare key's presence
(SC7, see `docs/SPEC_CONFLICTS.md`). The reference install's `sound.tdf`
declares 120 categories. When a variant's authored caption is absent, the
caption falls back to the slot's static default speech caption.

**Alias registration.** Aliases are registered into a flat, session-lifetime
table of 256 slots, each holding a 32-byte name and a 32-byte path — both
written with a 32-byte bounded copy, both tables laid out at a 32-byte stride,
with a parallel array of one sample handle per slot; an authored path longer
than 32 characters truncates. The registry is deduplicated (a new alias whose
name matches an existing entry, case-insensitive, up to 32 bytes, returns the
existing identity); the cap is 255 entries, and the 256th registration is
rejected without eviction and
returns the zero identity; each alias is probe-loaded at registration time
through the VFS and the WAV decode path (8.2) with the `sounds/` prefix and the
canonical candidate tries; the resulting handle, name, and path are stored and
the count increments even if the probe yields nothing. A name lookup miss
returns the sentinel id 0xFFFF, which silences the cue. A 33-byte authored
alias truncates to its stored 32 bytes; lookups stay case-insensitive over the
32-byte field. **Precedence is VFS mount order — first provider wins** (loose
directory, then GP3/CCX, then UFO/HPI, then CD-ROM); the archive flag does not
alter precedence, and a duplicate alias on a later mount is suppressed by the
**name** compare above, because an ordinary registration carries a name and
dedups on it alone. The case-insensitive canonical full-path compare exists
but runs **only** on the anonymous branch — the one taken when the alias-name
argument is null, which also stores the empty string as the slot's name.
A separate global alias loader
enumerates the children of `gamedata/allsound` and registers each child's
`sound` key as an alias through the same registry. Unit definitions map their
category names to category identities, with numeric fallback when a name is
absent.

**Crowding thresholds.** The two thresholds are separate bytes, both scaled by
5 from the menu gauge (range 0..10): one for audio (audible gate) and one for
speech (text gate). The gate is the signed compare `10 − threshold < priority`.
They are not per family, per alliance, or per player — the 24 slots share the
two global thresholds, and "by family" only describes how priorities group
(countdown slots 17..23 at priority 10 versus load/arrived at 7/3). Example
arithmetic: `select` (priority 10) always passes — at threshold 10 the gate is
`0 < 10`, true — while `working` (priority 2) fails at threshold 5 (`5 < 2`,
false). `underattack` (priority 9, 600-frame cooldown) and `working` (priority
2, 30-frame cooldown) exhibit different windows under the same threshold.

**Producer census.** The direct-caller census over the bounded corpus is
closed:

- **Positional/weapon** cues: six sites — the weapon root start, the water and
  land impact variants, the burst clone, the named-alias forward, and the
  network replay.
- **Named positional** (feature ignition): one site.
- **Underattack**: one site, on the damage path — emitted once per
  non-paralyzer normal damage event to a unit owned by the **view slot**
  ([R-AUD-01 §7], which owns the sink's gate and distinguishes the view slot
  from the local human's slot), gated on selection state, fixed category 2.
- **Unit voice**: 82 sites across orders, AI, and selection — category mapping:
  1 select, 2 underattack, 3/4 activate/deactivate, 5 ok (about thirty
  order-acceptance sites, gated on a runtime status bit 0x2000), 6 arrived,
  7 cant (construction failures), 8 unitcomplete, 9 build start, 10 repair,
  11 working, 12/13 load/unload, 14/15 cloak/uncloak (state-bit gated),
  17..22 countdown, 23 canceldestruct (self-destruct tick).
- **Bounded absence:** no producer exists on the unit sinking/removal path.

Global master and effects volume is observable through the wave and auxiliary
volume calls. The helper that plays positional world cues adds two established
presentation behaviors on top of that mixer. First, **audience gating**: the
source position is floor-divided by 65,536 (sign-corrected) to a plot cell; an
off-map cell is silent locally. The mode word's bit 1 then selects the
visibility source — the local viewing player's explored-memory byte grid
(nonzero byte visible, tested with the half-height shear and the grid bounds)
or the LOS word mask tested at the bit `1 << (localSlot & 31)`. The local
player slot only — no ally OR anywhere. Named feature cues are forwarded
through the same gate. Second, **placement**: when the device's 3-D flag is
set (Sound Mode `3D`) the helper computes the viewport-relative pan vector
with the half-height shear:

```
dx = pixelX − ((viewW/2) << 4) − viewLeft
dy = viewTop + ((viewH/2) << 4) + (pixelY >> 1) − pixelZ
```

(the 16.16 position is narrowed to its signed pixel component first), stores
(dx, 0, dy) as the buffer's DS3D position and sets the DS3D minimum and
maximum distances ([R-AUD-01 §1] has the distances and the attenuation
curve). With the flag clear (Sound Mode `Mono`) attenuation is **binary,
never a curve**: in-view sources play at −585 in the DirectSound
centibel-style volume encoding, off-screen sources at −1585 — exactly the
same two levels, with the viewport border inclusive (on the border is
in-view). Off-screen events are quieter, never discarded; a source at the
viewport center yields a zero pan vector (mixed as centered) at the same
in-view level.

**Random-stream contract.** Audio uses the CRT stream only — never the
simulation Park-Miller stream. Variant picks and CD random picks share the CRT
stream, so their interleaving matters: a CD random track consumes one draw that
would otherwise be a variant pick (presentation-level; must not leak into
simulation RNG). Silent resolves still draw, so the draw count is deterministic
with queue pops, not audible successes. The only sim-stream audio-adjacent draw
is the self-destruct detonation delay (0..15 frames), which is Park-Miller and
fires only on destruction — the two streams are distinct.

Projectile start, hit, and water sounds are queued synchronously with
projectile creation and collision events. A failed projectile reservation emits
no start event. Collision ordering is: screen shake, hit or water sound,
optional end smoke, selected land or water animation, then damage and area
effect; the visual and audio event is therefore created before the final damage
mutation in the observed path.

Positional world cues can also be emitted as a network packet carrying the alias
identity and position. A nonzero broadcast flag emits a packet carrying the
opcode, sub-opcode, alias id, and three signed 32-bit position words (18 bytes
total). The bounded direct-caller census shows every in-game
gameplay producer — weapon start and burst, impact, and named-feature ignition
forwarding — passes `broadcast = 0` and arises independently from lockstep
simulation with local audience gating; the inbound dispatcher replays the packet
with broadcast cleared and does not echo — a zero flag byte re-enters the
positional path, a nonzero flag byte replays through the backend-only path;
either way the broadcast flag is cleared before replay, so every peer still
passes its own local gates. Observed nonzero-broadcast callers are
frontend and options paths. Only dynamically or externally reached callers
outside the bounded direct-invocation census remain unknown for broadcast behavior.

### 8.4 Music and CD/MCI

Music uses WinMM MCI strings for `cdaudio` open, close, stop, status, play, and
pause.

**Open and probe.** CD initialization opens the MCI `cdaudio` device once. The
open failure path is established: on a failed `open cdaudio`, the engine
enumerates windows and retries the open with the enumeration-supplied window
handle; a second failure disables CD playback. On success it issues
`stop cdaudio`, sets the time format to milliseconds (a failure here stops and
closes the device and disables CD playback), and queries the track count with
`status cdaudio number of tracks`, parsed as a decimal integer. A failed count
query leaves the count at zero and the per-frame tick idles. The probe also
builds the 100-entry track-category table with the four repeating categories
`(trackIndex mod 4) + 1` (Red Book CDs carry at most 99 tracks), initializes
the current/next track and mode/status fields, and registers the
`MM_MCINOTIFY` handler on the engine's notification window.

**Play modes and track transitions.** The CD tick runs once per frame and
returns early when the track count is zero or playback is paused. It polls
`status cdaudio mode` and compares the reply exactly against `playing` — a
failed poll is treated as not-playing and triggers a transition. There are
exactly five modes:

| Mode | Behavior |
|---|---|
| 0 idle | no play; a detected stop resets state |
| 1 sequential | advance `next` by one, wrapping modulo the track count (`(track mod numTracks) + 1`), and play |
| 2 random | play `random_draw mod numTracks + 1` |
| 3 single | play the requested track (a requested track of zero stops playback) |
| 4 category-shuffle | stop and reset, then scan up to `(draw & 15 + 1) × numTracks` candidate tracks forward with wrap for the `max(1, draw & 15)`-th whose category equals the desired category; none found → stop |

The modes are the `TRACKMODE` choices `Play All|Random|Repeat|Custom` (1..4)
with 0 = idle; the "category" is the per-track `TRACKTYPE`
(`Building|Battle|Victory|Defeat|Unused`), persisted per disc as the registry
`CDLISTS` list. Mode 4 is a category-filtered forward scan, not a general
shuffle, and no history of recent tracks influences a pick ([R-AUD-01 §4],
[R-AUD-01 §8]).

The play primitive deduplicates a request for the track already playing,
applies the CD volume, and issues `play cdaudio from %i` — appending ` to
<physical + 1>` when the physical track (the logical track plus the
data-track offset) is below the track count — plus ` notify`, with the
notification window handle. An MCI error at play leaves
the status at playing; the next poll detects the failure and retries. After
any transition the CD volume is re-applied and status is set to playing.

**Pause, notify, volume, and mission media.**

- **Pause/resume** is UI-driven: pausing issues `pause cdaudio` and sets the
  paused status (the tick then holds, no transitions); resuming queries the
  position with `status cdaudio position/track %i` and re-issues play from the
  saved position.
- **Volume restoration:** CD volume is applied through the auxiliary volume
  control, wave audio through the waveOut volume control; the sampled system
  levels are restored unconditionally at shutdown, and `RestoreVolume` gates
  a registry round-trip of the player's levels ([R-AUD-01 §2]).
- **Mission media are not CD tracks.** The mission fields `brief`, `narration`,
  `glamoursound`, and similar are separate WAV aliases under paths like
  `camps/briefs/`, dispatched through the same decode path of 8.2. CD tracks
  are Red Book audio, independent of mission type; the campaign front-end
  reissues stop/close/open on mode changes via the CD re-init path.
- CD enable/disable is a configuration mask whose bit 0 gates the play
  primitive; the front tick re-applies it.

The missing-CD failure chain above (window-enumeration retry, then give up;
time-format failure stops and closes; track-count failure idles the tick) and
the `MM_MCINOTIFY` handler registration are established; history persistence
is [R-AUD-01 §4] (`CDLISTS`).

#### The sound device: bring-up, sample buffers, the 32-voice mixer, and the 3-D model [R-AUD-01 §1]

Everything in this section is presentation-only: no field it describes is read
by a simulation phase [03 §1].

**Established fact — bring-up order and the two INI switches.** The audio
start routine runs once at session start, after the sound device object has
been constructed and *before* the registry settings are loaded (the
construction samples the system mixer levels; see [R-AUD-01 §2]). It reads two
integers from the `[Preferences]` section of `totala.ini` in the executable's
directory (`GetPrivateProfileInt`, default 0 — these are **not** registry
values): `NoDirectSound` (nonzero → the no-DirectSound flag) and
`UseWindowsSound` (nonzero → the Windows-sound flag, which also sets the
no-DirectSound flag). With DirectSound allowed it then:

1. `DirectSoundCreate` on the default device; failure → step 4.
2. `SetCooperativeLevel(hwnd, DSSCL_PRIORITY)`; failure → step 4.
3. Creates the primary buffer (descriptor flags = primary-buffer only) and
   sets its format to PCM **11,025 Hz, 16-bit, stereo** (block align
   `((bits + 7) >> 3) · channels`, average bytes `rate · blockAlign`); failure
   → step 4. Success records the rate, bits, and channel count on the device
   and returns success.
4. Failure path: if the failing `HRESULT` is **`DSERR_NODRIVER`** the
   device's *no driver* flag is set; the device is torn down; then the caller
   tests that flag — set → the no-DirectSound flag is set silently (the game
   runs without sound effects); clear → the message box
   `Error:  Sound system initialization failed.` (two spaces after the colon,
   verbatim) is shown and the flags are left as they were.

CD initialization ([R-AUD-01 §4]) then runs regardless of the outcome, and the
eight-entry voice queue of §8.3 is allocated (count 0, base time 0, window
30, and a second constant 150 that nothing reads — bounded negative: it is
initialised and never consumed).

**No waveOut backend (Established).** The tested code is `DSERR_NODRIVER`
(device absent), not `DSERR_ALLOCATED`; and there is **no waveOut playback
backend at all** — the
executable imports no `waveOutOpen`/`waveOutWrite`. WinMM is used only for
volume (`waveOutGetVolume`/`waveOutSetVolume`, `auxGetVolume`/`auxSetVolume`)
and for MCI. The three backends of §8.1 are therefore: DirectSound; the
Win32 `PlaySound` path when `UseWindowsSound` is set; and **silence** (every
play gate tests the no-DirectSound flag and returns without playing).

**Established fact — the device record's defaults.** The constructor sets:
3-D flag off; DS3D minimum distance `1.0`; DS3D maximum distance `1.0e20`
(float); **voice limit 8**; active-voice count 0; play sequence counter 1;
thirty-two voice slots (buffer pointer, sequence number, loop flag) cleared;
eight transient-sample slots cleared. It then counts the waveOut devices,
finds the first auxiliary device whose capability technology is CD audio
(index kept; −1 if none), and samples the current waveOut volume (first
device that answers, low 16 bits) and the CD-aux volume (low 16 bits), each
−1 when unavailable.

**Established fact — sample loading modes.** The WAV loader of §8.2 is entered in one of three modes,
and the mode is chosen by the *caller*, never by an authored field:

| Mode | Caller | What it does |
|---|---|---|
| 0 | alias registration (§8.3 "Alias registration"), i.e. every `sound.tdf`/`allsound` alias and every weapon/feature sound | decodes and creates one **static** secondary buffer; returns a 16-byte sample record `{buffer, 0, 0, 0}` (tagged `Digital Audio Sample`) whose four slots hold up to four instances of the same buffer |
| 1 | the unit voice-cue resolver (§8.3 step 3) and the sound-options `TEST` button | decodes into a fresh static buffer and plays it **immediately** through the mixer, holding it in one of **8 transient slots** until it finishes; if all 8 transient slots are occupied the cue is dropped (returns 0). The loader runs the reaper first (below), so a slot whose buffer has stopped is freed before the test |
| 2 | the stream opener, entered through a timer callback ([R-AUD-02 §1]) | a streaming buffer (flags static + volume, size `rate · channels · bytesPerSample · 2`, half-buffer refill with silence fill) — the mission narration and glamour-sound path |

So every alias-registered sound is a preloaded static buffer, while every
**unit voice line is re-read from the VFS and decoded on each play** (mode 1,
under `sounds\` with the canonical candidate tries) — the alias cache of
§8.2 is never consulted for voices. Under `UseWindowsSound`, mode 0 instead
loads the file image into memory and mode 1 hands the file path to
`PlaySound`.

**Established fact — the static buffer descriptor.** For mode 0/1 the
descriptor is `{size 0x14, flags = STATIC | CTRL3D | CTRLVOLUME (0x92),
bytes = decoded payload size, reserved 0, format}` with format PCM tag 1,
the decoded channels/rate/bits, block align `((bits + 7) >> 3) · channels`,
average bytes `rate · blockAlign`, `cbSize` 18. The buffer is locked for its
whole length, the payload is read straight from the (already positioned)
file into it, and unlocked; a short read or any failing call releases the
buffer and yields a null sample (silence). The FPU control word is forced to
a fixed precision around `CreateSoundBuffer` (a DirectSound-era library
precaution; no arithmetic depends on it).

The `0x82` descriptor is the *streaming* buffer of mode 2 ([R-AUD-02 §1]);
real samples are `0x92` (with `CTRL3D`), live for the session, and are
**duplicated** on demand (below), not recreated.

**Established fact — the voice mixer (`Play(sample, volume, pan)`).** Every
non-CD sound goes through one routine. In order:

1. **Exclusive loop check.** If the *loop* flag is raised for this call (only
   the by-name cue path of [R-AUD-01 §5] raises it) and any of the 32 voice
   slots holds a voice whose loop flag is set, return 0 — a second looping
   cue never starts while one is playing.
2. **Voice-limit steal.** While `activeVoices ≥ voiceLimit` (the
   `MixingBuffers` setting, default 8, [R-AUD-01 §2]): pick the first slot
   whose voice is non-null and *not* loop-flagged, then the slot with the
   **smallest sequence number** among such slots (oldest start), `Stop` it,
   clear the slot, decrement the count. Loop-flagged voices are never
   stolen. (Edge: if every slot is loop-flagged the search runs off the end
   of the table — unreachable, since step 1 admits at most one loop voice.)
3. Null sample → return 0.
4. **Instance choice** over the sample's four slots: a failed playback-status
   query aborts admission. A slot that holds a buffer whose `GetStatus`
   reports *not playing* is reused as is; otherwise
   its play cursor is read and the instance with the **largest play cursor**
   (strictly greater wins; ties keep the earlier slot) is remembered; a null
   slot is remembered as `lastNull`. If no idle instance was found: when
   `lastNull > 0`, `DuplicateSoundBuffer(slot 0)` into `slot lastNull`
   (failure → return 0); when all four are busy, the remembered
   furthest-along instance is **restarted** (`SetCurrentPosition(0)`). So a
   sample plays at most **four** times simultaneously and the fifth request
   steals the one closest to finishing. The all-busy branch ignores this
   first reset's result and continues to step 5, even on failure. The later
   step-6 reset is separate and its failure aborts before volume, playback
   and tracking. The play-cursor query result in the choice scan is also
   ignored; these return-handling distinctions are Established.
5. **3-D setup.** `QueryInterface(IID_IDirectSound3DBuffer)` on the chosen
   instance (every real sample has `CTRL3D`, so this succeeds; it is skipped
   silently if not). If the device's 3-D flag is set **and** a pan vector
   was passed: `SetPosition(pan.x, pan.y, pan.z)` as floats, `SetMinDistance
   (device.minDist)`, `SetMaxDistance(device.maxDist)`, `SetMode(NORMAL)`;
   otherwise `SetMode(DISABLE)`. The interface is released. **No listener
   is ever configured** — the `IDirectSound3DListener` IID exists in the
   image with no reference — so the listener sits at the DirectSound default
   (origin, facing +Z, up +Y, distance factor 1, rolloff factor 1, Doppler
   1; bounded negative over the whole image).
6. `SetCurrentPosition(0)`; `SetVolume(volume)`; `Play(0, 0, looping = loop
   flag)`. Any failure returns 0.
7. Register the instance in the first empty voice slot with
   `sequence = ++counter` and the loop flag; `activeVoices++`. If the 32 slots
   are all taken the voice still plays but is untracked (return 1).

**Reaper.** Before a transient load (mode 1) the device walks the 8
transient samples and the 32 voice slots, freeing/clearing every entry whose
buffer reports *not playing* (or whose status query fails). The application pump also reaps every ≥100 ms of wall clock
([R-AUD-02 §2]); there is no per-tick reaper, and voice slots are otherwise
reclaimed only by the steal of step 2. **Stop-all** (`MODE` set to `Off`, movie start, battle exit) stops
every voice slot's buffer and clears the table.

**Established fact — what each producer passes.** Volumes are DirectSound
attenuations in hundredths of a decibel:

| Producer | Volume | Pan | Loop |
|---|---|---|---|
| by-id / by-name cue (HUD clicks, front end, `Options`, campaign cues) | −585 | none | only through the loop wrapper ([R-AUD-01 §5]) |
| unit voice line (mode 1) | −585 | none | no |
| options `TEST` preview (`sounds\explode.wav`, mode 1) | −585 | none | no |
| positional world cue, 3-D flag **set** | −585 | `(dx, 0, dy)` below | no |
| positional world cue, 3-D flag clear, source inside the inclusive viewport rectangle | −585 | none | no |
| positional world cue, 3-D flag clear, source outside | −1585 | none | no |

**Established fact — the 3-D placement.** With the 3-D flag set (Sound Mode
`3D`, [R-AUD-01 §2]) the positional helper of §8.3 computes, in **pixels**
(the position's signed 16-bit pixel components, the view extents in 16-pixel
units, signed truncating `/2`):

```
dx = px − viewLeft − trunc(viewW / 2) · 16
dy = viewTop + trunc(viewH / 2) · 16 + (py >> 1) − pz
```

stores the vector `(dx, 0, dy)`, and sets the device distances to
`minDist = trunc((viewH + viewW) / 2) · 16` and `maxDist = (mapW + mapH) · 16`
(map size in 16-pixel cells) before calling the mixer at −585. Because the
listener is at the origin facing +Z, `dx` pans left/right and `dy` is
"forward": a source at the viewport centre is centred and at full level; a
source farther than `minDist` from the centre attenuates per DirectSound's
default inverse-distance rolloff (−6 dB per doubling of distance beyond
`minDist`, rolloff factor 1, held constant beyond `maxDist`). That curve is
the library's documented default, not engine arithmetic; Nanolathe may
implement it directly.

The buffers are DS3D buffers, the vector is a DS3D **position**, and the
two floats are the DS3D **minimum and maximum distance**. The binary
−585/−1585 levels apply **only when the 3-D flag is clear** (Sound Mode
`Mono`); in `3D` mode the level is always −585 and distance attenuation is
DirectSound's. The 3-D flag is the user's Sound Mode, not a hardware probe.

**Established fact — the `PlaySound` backend.** With `UseWindowsSound`, a
by-id cue plays its in-memory image with `SND_ASYNC | SND_MEMORY` (plus
`SND_LOOP` for the loop path, which also remembers the image so the drain
of §8.3 can re-issue it with `SND_NOSTOP`); positional cues lose their
position and gates (they route to the by-id path); voice lines and the
`TEST` preview play by file name; stop-all purges the current sound. Only
one `PlaySound` can be audible at a time — a library property.

**Timing.** All of this runs on the presentation side of the host frame
[07 R-CAM-01 §1]: cue producers call the mixer synchronously from wherever
they run (weapon fire and impact inside the sub-tick, voice lines from the
drain), and nothing is deferred except through the §8.3 voice queue.

#### The audio preferences: registry names, bit map, and consumers [R-AUD-01 §2]

**Established fact — the packed sound-flags byte** (the second packed word of
[02 §3]), each value read from `Software\Cavedog Entertainment\Total
Annihilation` at session start with the listed default when absent:

| Bits | Registry value | Default | Written by | Read by |
|---|---|---|---|---|
| 0–2 | `Sound Mode` | 1 | settings load; `MODE` gadget of the sound options (`Off|Mono|3D` = 0|1|2); `RESTORE` (1) | every play gate (`≠ 0` required, §8.3); value `2` sets the device 3-D flag, any other value clears it ([R-AUD-01 §1]) |
| 3 | `RestoreVolume` | 0 | settings load only (no gadget in the traced screens) | settings load and save (below) |
| 4 | `ackfx` | 1 | settings load; `RESTORE`/`UNDO` | **nothing** — bounded negative over the complete decompiled corpus |
| 5 | `buildfx` | 1 | same | **nothing** — bounded negative, as above |
| 6 | `speechfx` | 1 | settings load; the `SPEECH` gadget writes `bit6 = (gauge ≠ 0)` | the voice-cue resolver's **audible** gate only (§8.3 step 3): with the bit clear no unit voice line plays; captions are unaffected |

So `ackfx` and `buildfx` are persisted and displayed but gate no sound in
this executable. Nanolathe carries them as inert settings.

**Established fact — the other audio settings.**

| Registry value | Default | Store | Consumers |
|---|---|---|---|
| `fxvol` | 27 | effects gauge `FXVOL`, slider maximum **64** | every play gate (`≠ 0`); applied as `waveOutSetVolume(dev, (v << 10) · 0x10001)` on **every** waveOut device, clamped to `0..0xFFFF` per channel — i.e. the *system* wave mixer, not per-buffer attenuation |
| `musicvol` | 32 | music gauge `MUSICVOL`, slider maximum **64** | `auxSetVolume(cdAux, (v << 10) · 0x10001)` and the CD object's base volume ([R-AUD-01 §4]) |
| `MixingBuffers` | 8 | device voice limit | the mixer's steal loop ([R-AUD-01 §1]) |
| `musicmode` | 1 (bit 0) | CD enable | `NOTRAK` gadget (`Off|On`); the CD play primitive and the MUSIC screen enables ([R-AUD-01 §4]) |
| `cdmode` | 4 | CD play mode 1..4 | `TRACKMODE` gadget (`Play All|Random|Repeat|Custom` = stage + 1); the CD tick ([R-AUD-01 §4]) |
| `WaveOutVolume`, `CDAudioVolume` | — | raw mixer levels | only when `RestoreVolume` bit 3 is set: applied at load, captured at save |

Both gauges are kind-4 sliders whose runtime maximum the page opener writes
into the record as the literal **64**, so a knob at the end of travel reads 64
and the level `v << 10` is 65,536 — one past the 16-bit mixer word, which the
clamp above saturates. Both scale by `<< 10`: `27 << 10 = 27,648` and
`32 << 10 = 32,768` of 65,535. The `SPEECH` gauge (`Off|Medium|Full`) writes
**both** halves of its store in one arm: bit 6 takes `stage ≠ 0` and `unitchat`
takes `stage × 5`, the acknowledgement voice level of [07 R-CAM-01 §7], so the
gate `10 − level < priority` admits nothing at `Off`, priorities ≥ 6 at
`Medium`, and everything at `Full`. The screen opens the gauge at
`speechfx ? unitchat ÷ 5 : 0`.

`RESTORE` on the sound screen sets `fxvol` 27, bits 4–6, Sound Mode 1 (3-D
off), voice level 10; `UNDO` restores the entry snapshot. Changing `MODE` to
`Off` stops every voice; changing it to `Mono` while not in a battle re-issues
the front-end `BGM` loop ([R-AUD-01 §5]). The `TEST` button plays
`sounds\explode.wav` (mode 1, −585, no pan) under the ordinary gates and plays
**no** options cue — it is the one control on the four pages that the family's
cue column of [07 R-FE-01 §6] does not cover.

**Established — the sound screen's enable state.** Both the opener and the
`MODE` arm re-apply it: the `VOLTEXT` caption's active byte takes
`(soundflags & 7) ≠ 0`, and `FXVOL`, `TEST` and `SPEECH` take the greyed/lock
word `(soundflags & 7) == 0`. So with Sound Mode `Off` the volume caption
disappears and the gauge, the test button and the speech gauge are inert and
drawn darkened ([07 R-WGT-01 §3], [07 R-WGT-01 §5]).

**Established fact — `RestoreVolume` and the two restore points.** The
device constructor samples the system waveOut and CD-aux levels **before**
the registry is read. At session shutdown those sampled levels are written
back unconditionally (the CD level only when no fade is in progress) — the
game leaves the mixer as it found it. `RestoreVolume` governs something
else: with bit 3 set, `WaveOutVolume`/`CDAudioVolume` are read at load and
pushed to the devices, and at save the *current* device levels are written
to those two values — persisting the player's mixer levels across sessions.
With the bit clear the two values are neither read nor written.

#### The unit voice pick and its gates [R-AUD-01 §3]

§8.3 states the queue, the variant draw, and the crowding gates; this
section is the producer side.

**Established fact — producer gate.** A unit voice request (any of the 82
sites, slot 1..23) is enqueued only when the unit's owner is the **local
viewing player**, the unit's status word carries the **live** bit (bit 28)
and its **death-pending** bit (bit 14 — the `0x4000` §8.3 mentions for the
`ok` sites) is clear: the same two gates the caption raiser applies
([07 R-HUD-03 §14.1], [R-AUD-01 §7]; [04 R-ORD-01 §12] and
[04 R-SPEC-01 §12] name the two bits).
The caption passed is the caller's override or the
slot's default caption run through the localisation table. Selection
(`select`) is slot 1: cooldown 0, priority 10 — it always passes the
crowding gate and is limited only by the 30-frame window and the
duplicate-slot rule (a second `select` while one is queued is dropped).

**Established fact — the draw.** The variant index is one CRT draw per
resolve, silent or audible (§8.3). The producer draws nothing; the audible
play reloads the WAV (mode 1, [R-AUD-01 §1]) and may be dropped when the 8
transient slots are all busy, in which case the cooldown is **still**
re-armed (the re-arm follows the gate, not the play result).

**The honk/sing flag.** The writer is the `Sing` interface option
[07 R-CAM-01 §7]. While it is set the audible alias is `sing`, or `honk` when
`(frame / 30) & 7 == 0` (frame = the global tick counter, truncating
division).

**Established fact — no per-tick throttle.** Beyond the 30-frame voice
window and the mixer's voice limit ([R-AUD-01 §1]) there is no per-tick cap
on cues: a weapon salvo of *n* shots issues *n* positional plays in the same
sub-tick, and the mixer steals the oldest non-looping voices to fit them.

#### CD audio: modes, categories, disc identity, transitions, and the MUSIC screen [R-AUD-01 §4]

§8.4 states the open/probe and failure chain; this section states the
modes, categories, disc identity and transitions.

**Established fact — the CD object.** Fields, with their sources: *play
mode* (`cdmode`, 1..4, and 0 = idle); *audio track count* (below);
*requested track* (Repeat mode); *current/next track*; *status* 0 stopped /
1 playing / 2 paused; *disc serial*; *category byte per track index 0..99*,
initialised to `(i mod 4) + 1`; *desired category*; *enabled* (`musicmode`
bit 0); *data-track offset*; *fade step*; *base volume*. Categories are
0..4 and the MUSIC screen labels them `Building | Battle | Victory | Defeat
| Unused` (`TRACKTYPE` gadget text; a static table of the same five slots
holds the names `NOTRAK`, `NORMTRAK`, `RANDTRAK`, `REPTTRAK`, `SPECTRAK`,
which nothing reads — bounded negative; an unreferenced vocabulary).

**Established fact — track count and the data track.** On open and on every
media-arrival notification: the first drive whose type is CD-ROM (scanning
`A:`..`Z:`) is asked for its **volume serial number** (`GetVolumeInformation`),
stored as the disc identity; `status cdaudio number of tracks` is parsed as
a decimal; then `set cdaudio time format tmsf` and `status cdaudio type
track 1` — if the reply is exactly `audio` the data-track offset is 0,
otherwise (any other reply, or a failed query) the offset is 1 and the
count is decremented (floored at 0). So `count` is the number of **audio**
tracks and logical track *t* is physical track `t + offset`. A nonzero
count sets the next track to 1; a zero count makes the tick idle (no disc,
no audio tracks, or no MCI).

**Established fact — the per-disc category list (`CDLISTS`).** A 20-entry
ring, keyed by the disc serial, persists the category bytes: registry
binary value `CDLISTS` (2,720 bytes = 20 × 136; entry = 32 unused bytes,
serial, 100 category bytes), read at session init (zeroed when absent) and
written on disc eject and at shutdown. On (re)identification: a serial hit
moves its entry to the front and copies its list into the object (`count`
bytes into tracks 1..count); a miss builds a **default list** of seven
`Battle` (1) bytes followed by zeros (`Building`), inserts it at the front
with the serial, and applies it to the object **only** when the audio count
is exactly 16 and track 1 is not `audio` (the retail disc's shape: one data
track and sixteen audio tracks) — any other unknown disc keeps the object's
current list (the `(i mod 4) + 1` cycle at first run, or the previous
disc's). `TRACKTYPE` edits write the object's list; the ring is refreshed
from the object at eject/shutdown.

**Packaged MP3 media.** **Established:** the reference GOG Commander Pack
installation supplies `music/0.mp3` through `music/17.mp3`, and the executable
routes MCI string commands through the packaged `win32.dll` audio adapter.
For a play command naming physical track *p*, that adapter opens
`music/<p>.mp3`: the filename uses the physical track number directly, with
no subtraction or renumbering. The packaged executable also has an added
track-count path that supplies the decimal count 17 before the ordinary
first-track-type adjustment. This added path is outside the original count
query behavior described above; MP3 directory length is not the CD count.

**Established:** combining that filename rule with the retail disc shape
(one data track followed by sixteen audio tracks) gives this media contract:
logical audio tracks `1..16` map to `music/2.mp3` through `music/17.mp3`,
respectively, by `filename number = logical track + 1`. The logical count is
16 and the data-track offset is 1 for this disc shape. The corresponding
fresh-disc default category list is seven `Battle` tracks followed by nine
`Building` tracks, under the category-list miss conditions above. In file
terms, that means `2.mp3..8.mp3` for Battle and `9.mp3..17.mp3` for Building.
Full-file comparison of the reference installation establishes that `0.mp3`
is identical to `9.mp3`, and `1.mp3` is identical to `10.mp3`; the sixteen
files `2.mp3..17.mp3` are distinct. The two lower-numbered duplicates do not
add logical audio tracks to that disc-shaped mapping. The development
installation's numbered files match the packaged files byte for byte.

**Established limitation / Unknown:** the packaged adapter's status-command
path reports success without filling the caller's reply buffer, including
first-track type and playback-mode requests. Consequently static evidence
does not establish a platform-independent first-track reply or a reliable
live device status for a GOG launch. The count 17 becomes sixteen audio
tracks only when the ordinary first-track probe does not return exactly
`audio`; an exact `audio` reply would retain seventeen and offset zero.
Likewise, a persisted `CDLISTS` hit replaces the fresh-disc defaults. A
missing CD-ROM serial remains zero and can match a zero-initialized list
entry, so seven Battle followed by nine Building must not be described as
an unconditional observed GOG startup state. The mapping above defines the
retail disc-shaped media, independently of those adapter response defects.
A manual retail observation with the response buffers and category-list
provenance identified would settle the packaged launch's actual state.

**Established fact — the MUSIC screen (`MUSIC.GUI` / `MUSICRT.GUI`).**
Gadgets and effects: `NOTRAK` (`Off|On`) toggles `musicmode` and calls
enable/disable (disable = stop and reset); `TRACKMODE` (`Play All|Random|
Repeat|Custom`) sets `cdmode = stage + 1` and applies it — `Repeat` copies
the selected track into *requested*, `Custom` shows `TRACKTYPE` for the
selected track; `TRACKTYPE` writes the selected track's category; `TRACKNUM`
shows the selected track as `%d`, or `NO DISC` when the selection is 0, and
is disabled when `musicmode` is off (a typed number re-syncs the selection
to the object's current track); `CDPLAY` plays the selected track; `CDNEXT`
/ `CDPREV` step the selection with wrap over `1..count` and, when playing,
switch immediately (else only move *next*); `CDSTOP` stops/resets and
re-selects track 1; `MUSICVOL` is the gauge above; `RESTORE` sets
`musicvol` 32, `cdmode` 4, and turns `musicmode` on (running the tick if it
was off); `UNDO` restores the entry snapshot (volume, list, mode, enable,
requested). `TRACKTYPE` is active only when `musicmode` is on and the mode
is `Custom`; the gauge, the four transport buttons and `TRACKMODE` are
disabled when `musicmode` is off — the gauge through the kind-4 lock word and
the buttons through the grey word, which is why two different helpers write
them ([07 R-WGT-01 §5], [07 R-WGT-01 §13]). Closing the screen runs the tick
when in battle, otherwise stops and resets.

The options root greys its own `MUSIC` button when the CD object's open flag is
zero — no drive, or `open cdaudio` having failed twice. A drive holding no
audio disc still opens: that case reaches the screen, which then shows
`NO DISC` ([07 R-FE-01 §6]).

**Established fact — the play primitive `PlayTrack(t)`.** Disabled → report
success and do nothing. `t = 0` → run the tick instead. Poll `status
cdaudio mode`; if the reply is `playing` and `t` is the current track →
success (dedupe). Otherwise `next = t`, `physical = t + offset`, apply the
base volume (fade-aware, below), `set cdaudio time format tmsf` (failure →
false), then `play cdaudio from <physical>` + (` to <physical + 1>` only
when `physical < count`) + ` notify`, sent with the engine window as the
notify target, then `set cdaudio time format milliseconds`; return whether
the play command succeeded. Consequence: the last audio track (and, on a
disc with a data track, the second-to-last) plays with no end bound —
through to the end of the disc.

**Established fact — the tick and its callers.** The ordinary CD tick is
explicit, not an unconditional part of the per-host-frame sound service.
Callers are CD play-zero, music/options actions, category-transition branches,
fade and delay completion, and front-end/battle transitions that request
playback. A successful media-completion notification invokes it only when
controller status is playing and a fresh mode query does not report playing;
a failed query counts as not playing. Other notification outcomes do not
invoke it. Timer service remains part of busy-pump housekeeping
[01 R-PLAT-02 §4]. The tick itself has no fade/delay guard:


1. `count == 0` → return.
2. `desired == 4` (`Unused`) → `stop cdaudio`, `next = 1` (or 0 when no
   tracks), status 0, fade cleared, timers cancelled; return. **Category 4
   means silence** — it is what every front-end screen requests
   ([R-AUD-01 §5]).
3. status 2 (paused) → return.
4. `desired ∈ {2, 3}` (`Victory`/`Defeat`) always takes the category branch
   (step 6) regardless of play mode; nothing in the executable requests
   them (bounded negative over all callers) — the two labels are inert.
5. Otherwise by play mode:
   * `0` idle: if status ≠ 0, set it 0; if the drive still reports
     `playing`, stop and reset.
   * `1` Play All: not `playing` → `next = next < 1 ? 1 : next + 1`;
     `PlayTrack(next)`; **then** if `next > count`, `next = 1` — the wrap
     is applied after the play, so the frame after the last track issues a
     play of `count + 1` (which the primitive sends as
     `play cdaudio from count+1+offset` with no end bound — an out-of-range
     track the MCI device refuses; the failure leaves status 1 and the next
     poll re-triggers with `next = 1`).
   * `2` Random: not `playing` → `PlayTrack(rand mod count + 1)`.
   * `3` Repeat: not `playing` **or** `next ≠ requested` → `requested = 1`
     when it was 0; `PlayTrack(requested)`.
   * `4` Custom → step 6.
6. Category branch: `u = rand & 15` (drawn **before** the poll, so the draw
   happens every tick in this branch even while a matching track plays).
   If `playing` and `category[next] == desired` → step 7. Else scan forward
   from `next` for up to `(u + 1) · count` steps, wrapping `count → 1`; the
   `max(1, u)`-th track whose category equals `desired` is played
   ([R-AUD-01 §8]; the `(u+1)·count` budget guarantees a hit whenever at
   least one matches); none → stop and reset (status 0, `next = 1`, fade step zero and both
   timers cancelled), then step 7.
7. Tail: force-apply the base volume, bypassing the nonzero-step gauge guard;
   status = 1. An explicit tick can therefore restore volume during a fade or delay.

The "not `playing`" test is the exact string compare of the `status cdaudio
mode` reply against `playing`; a failed query counts as not playing. The CRT
draws (`rand mod count + 1`, `rand & 15`) are on the presentation stream
and interleave with the variant picks of §8.3 ([R-AUD-01 §6]).

**Established fact — changing the desired category (`SetDesired(n)`).** If
unchanged, nothing. Otherwise the current/next track is remembered under the
outgoing category when that category is nonnegative, then `desired = n`.
There is no upper bound on that history write, and no recovered reader of its
five defined entries. Outgoing categories above four therefore cause an
out-of-range memory write; this unsupported input effect has no bounded
history interpretation. The new signed desired value is not clamped.

If the play mode is not `Custom` and `n` is neither 2 nor 3, return. Otherwise
copy base volume to fade level, then:

* If outgoing category was 4: cancel the repeating fade timer, then the delay
  timer, marking each absent; attempt an ordinary base-volume application;
  run the ordinary tick immediately.
* Else, if the repeating fade timer is registered: cancel it, then cancel the
  delay timer; run the ordinary tick immediately. This branch does **not**
  clear the stored fade step.
* Else set `step = -trunc(base / 18)` and register a repeating period-2 fade
  timer. An existing delay timer is not cancelled by this branch.

Each fade callback **adds** the negative step to fade level. If the result is
positive it force-applies that level. Otherwise it cancels the repeating
registration, sets both fade level and step to zero, and force-applies zero.
For desired category 0 (Building), it then registers the period-120 delay;
for other categories it runs the ordinary tick immediately. The delay
callback cancels the registration identified by the stored delay ID before
running the tick. Timer registration itself services existing timers first;
slot order and nested service are specified in [01 R-PLAT-02 §4].

**Established fact — volume state.** Initial raw base volume is the selected
CD auxiliary device's queried low 16-bit channel value, or signed `-1` if no
device is selected or the query fails. Gauge calls pass their signed integer
shifted left ten bits. An ordinary volume call returns without updating base
or output while `step != 0`; otherwise it clamps the signed level to
`0..65535`, stores base, and applies both channels. A forced call bypasses the
step guard and does not change base. Thus gauges are accepted during the
Building delay (step has been cleared), but may remain blocked after a fade
registration is cancelled (step retained). Base levels 1..17 yield step zero,
so a positive fade level does not decrease. No minimum step is substituted.

**Established fact — pause/resume and notifications.** The in-battle options
menu (`ARMOPT.GUI`) pauses the CD on open and resumes on close. Pause:
`pause cdaudio`, status 2. Resume (only when enabled and status ≠ 0):
`status cdaudio current track` → *t*; the command is `play cdaudio` +
(when `t < count`: ` from ` + the reply of `status cdaudio position` + ` to `
+ the reply of `status cdaudio position track %i`) + ` notify`; status 1.
The notify window receives `MM_MCINOTIFY` (successful completion, while
status is 1 → poll; not `playing` → tick, preserving status until the tick
runs its own ordinary branch and fresh device query) and `WM_DEVICECHANGE` (any → stop
and reset; media arrival → recount tracks and re-run the disc
identification, which reloads the category list and restarts per the tick).
The application loop also handles eject/insert around the CD object: on
eject the desired category is saved and the device closed; on insert it is
reopened, enable/mode re-applied, the saved category restored, and the
identification run.

**Established fact — without a CD.** No CD-ROM drive → no serial (0) and
the count query fails → count 0 → the tick idles forever; `open cdaudio`
failing twice disables the object (`enabled` is never consulted, the `*obj`
open flag is 0 and every entry point returns). The MUSIC screen then shows
`NO DISC` and `musicmode` toggles have no audible effect. No message is
shown.

#### Music selection: the battle intensity chooser and the front-end loop [R-AUD-01 §5]

**Established fact — who requests which category.** Exhaustive caller census
of `SetDesired`:

| Site | Category |
|---|---|
| session init | 0 (`Building`) |
| battle start (the battle-entry routine, before the first frame) | 0 |
| the battle intensity chooser (below) | 0 or 1 |
| main menu build, every front-end return, battle exit / results | 4 (`Unused` = silence) |
| disc re-insert | the category saved at eject |

Nothing requests 2 or 3; `Victory`/`Defeat` tracks are never chosen.

**Established fact — the intensity chooser** (host frame, scaled wall-clock,
in battle only, skipped once the battle-over latch is set unless the exit bit
is also set). A fresh process starts with evaluation count and clock stamp
zero, and retained last-want −1. Battle setup clears the thirty buckets and
current index only; the other chooser fields survive battle transitions.

**Damage producer, Established:** An accepted packet for a live, not-dying
victim contributes +1 after damage reaction and cause/provenance stores when
its non-null reconstructed attacker record exists and either that record's
owner or the victim's current owner equals the local player. There is no amount
test: zero damage, paralyzer and no-reaction packets qualify. Healing and
null-attacker environmental damage do not.

**Death producer, Established:** Ordinary and Cargo deaths enter the full
accounting branch directly. Reclaim enters it only when the victim's stored
attacker side is nonneutral and differs from the victim owner. All three require
the victim player record to exist. At that branch's tail, +5 is added when the stored attacker side equals
the local player. This is attacker provenance, not victim ownership; completion,
commander status and current attacker liveness do not gate the increment.
Other death causes do not enter this branch.

**Evaluation, Established:** Admission uses unsigned
`now > uint32(stamp+30)`, not signed elapsed time. On admission increment the
signed evaluation count. Once `evals > 10`, sum all thirty buckets, walking
backward from the bucket immediately before current; the five-bucket sum uses
the first five visited buckets and excludes current. Start `want` from retained
last-want, then:

- If the music controller's desired category is Building (0),
  `(sum30 > 50 or sum5 > 30)`, and the local player's unsigned 16-bit unit
  count is `> 30`, set want to Battle (1).
- Otherwise, if desired is Battle (1), `sum30 < 10`, `sum5 == 0`, and
  `evals > 60`, set want to Building (0).
- Otherwise retain the previous want.

Sums, evaluation-count comparisons and thresholds are signed and strict.
A want different from retained last-want calls `SetDesired(want)`, stores the
new last-want and clears the evaluation count. Finally advance the current
bucket modulo thirty, clear the new bucket, and sample the scaled clock again
for the stamp. Activity counters and chooser state never feed simulation.

**Established cross-battle consequence:** The full writer census finds no
other last-want, evaluation-count or stamp reset. If a battle exits with
last-want Battle (1), the next battle's explicit Building request does not
clear it. A later hot evaluation computes Battle again, equal to retained
last-want, and therefore does not request that transition. This quirk must not
be repaired by an inferred reset in a retail-compatible chooser.

**Established fact — the front-end loop (`BGM`).** Building the main menu
plays the alias `BGM` (stock `allsound.tdf`: `drone2`) through the **loop
wrapper** — the mixer's exclusive looping voice of [R-AUD-01 §1] (or
`PlaySound` with `SND_LOOP`) — and then requests CD category 4, which stops
the CD (with a fade if a category branch was active). The loop is never
stolen; it ends only at stop-all (battle entry via the movie/loading path,
battle exit, or `MODE` → `Off`) and is re-issued when the sound `MODE`
lands on `Mono` outside a battle (the exclusive check makes the re-issue a
no-op while it still plays). There is no "intense" switch in the front end
and no menu-versus-battle CD category: the front end is CD-silent.

#### Random draws and the throttle summary [R-AUD-01 §6]

**Established fact.** Every audio draw is on the **CRT** stream
(`x' = x × 214013 + 2531011`, bits 16..30): the variant pick of §8.3 (one per
resolve), CD `Random` mode (`rand mod count + 1`, once per tick that finds
the drive not playing), and the CD category branch (`rand & 15`, **once per
tick** while that branch is active, even when nothing changes). No audio
path draws from the simulation stream; the only sim-stream draw §8.3 names
(self-destruct delay) is not audio. Nanolathe's audio must not touch the
Park-Miller stream, and its CRT consumption is presentation-only — the
interleaving of CD and variant draws is not reproducible against retail
(it depends on wall-clock frame count) and is not a determinism concern.

**Throttles, complete list:** the 30-frame voice window (§8.3), the per-slot
cooldowns (§8.3), the 8-entry voice queue, the mixer's `MixingBuffers`
voice limit (steal oldest), the four-instances-per-sample cap (steal
furthest-along), the 8 transient slots for voice lines (drop), and the
single exclusive loop. Nothing counts cues per tick.

#### The engine status-cue sink: codes are slots, the raise-site gate, and what else reacts [R-AUD-01 §7]

**Established** (instruction-level read of the raise helper and its
two siblings, the queue's insert, every reader of the queue, and the unit
edge machine of [05 R-ECO-01 §8]). The four codes the engine raises from the
edge machine — 3 `activate`, 4 `deactivate`, 14 `cloak`, 15 `uncloak` — and
the order handlers' 82 voice sites use the same sink.

**The codes are slot indices.** The integer the edge machine passes as a
"status code" is the index into the static slot table of §8.3, record by
record: code 14 *is* slot 14 `cloak` (priority 7, cooldown 1 × 30, default
caption `Cloaked`), code 15 slot 15 `uncloak` (`Visible`), codes 3 and 4
slots 3/4 `activate`/`deactivate` (priority 4, cooldown 2 × 30, no default
caption). There is no translation table between the two numberings.

**One sink for every producer.** The unit voice sites, the edge machine and
the under-attack path all reach the same raise helper, `raise(unit, code,
overrideText)`, which does exactly three things:

1. *Gate.* The unit's owner slot must equal the **view slot** — the slot whose
   side the HUD presents, a global distinct from the local human's slot (the
   two are equal in single player; an observer may change the view slot) —
   the unit's status word must carry the live bit (bit 28) and must not carry
   the death-pending bit (bit 14; [04 R-SPEC-01 §12] names it) — the same pair
   the voice producer of [R-AUD-01 §3] tests. When the gate
   fails the helper returns having touched nothing: no queue write, no
   allocation, no draw. Remote and computer players' units therefore never
   enter the queue, and a dying unit's edges are silent.
2. *Text.* The override text, or when the caller passes none the slot's
   static default caption; either is passed through the localisation lookup
   (a sorted-table search; the identity when no table is loaded). The edge
   machine passes no override for any of its four codes, so 14 and 15 print
   `<unit name>: Cloaked` / `<unit name>: Visible` when §8.3's speech gate
   passes, and 3 and 4 print nothing (empty caption, §8.3 Resolve step 4).
3. *Insert*, exactly §8.3 **Insert**, timed against the **global tick
   counter**: cooldown (the slot's next-allowed frame `<=` the tick),
   duplicate-slot drop, full-queue silent resolve of the last entry, sorted
   insert after equal priorities. The entry copies the caption into its own
   allocation.

Two sibling raisers share the gate and add one global flag test each (one
requires it set and has no caller in the image; the other requires it clear
and is the under-attack path's — §8.3's "gated on selection state"). The
edge machine and the order handlers use the plain helper.

**Synchronous, from inside the simulation.** The raise runs in the caller's
own phase — the economy settlement for 14/15 ([05 R-ECO-01 §9]),
construction admission and the work handlers for 3/4 — and the insert is
immediate. The queue has exactly one other simulation-side interaction: the
**unit-teardown purge** — when a unit is removed, every queued entry whose
unit is that unit is dropped (text freed, queue compacted) before the record
goes away. No simulation phase reads a queued entry back; the readers are
the once-per-rendered-frame drain of §8.3 (the presentation pump), the purge,
and the battle-start / shutdown clear-alls. A clone that keeps presentation
behind the publication boundary ([03 §2.4], I6) may therefore carry a raise
as a committed-tick event of `(code, unit)` and perform the insert at the
boundary, provided (i) the gate's three per-unit facts are evaluated at the
raise, (ii) each committed tick's events are applied exactly once in raise
order — the insert's ordering rule and cooldown test depend on order and on
the tick, not on wall-clock — and (iii) a unit removed in the same tick drops
its pending entries. The observable difference from retail's synchronous
insert is nil: nothing in the simulation observes the queue.

**Random draws: none on the raise path.** The gate, the localisation lookup
and the insert draw from neither stream. The only draw anywhere in the cue
path is the CRT variant pick at resolve time — once per pop, audible or
silent (§8.3 Resolve step 2, [R-AUD-01 §6]). Wiring the four codes adds no
simulation-stream draw and moves the CRT stream only at drain time, as every
other cue already does.

**What else reacts on the edges, complete.** On the instance cloaked bit's
rising edge, after the cue: the observer notice — every order record whose
target reference is registered on the cloaking unit receives pending bit
`0x10000` ("target cloaked", [04 R-ORD-01 §6]); this is simulation-visible
and doc 04 owns it; the falling edge sends no notice. On the activation bit's
edges the COB `Activate` / `Deactivate` callback is raised **before** the cue
([05 R-ECO-01 §8]). After any edge: the interface-panel dirty flag is set when
the unit's owner is the **local** slot (not the view slot) and the unit's
status word carries the bit the panel keys on — presentation only — and, for
control bytes 1 and 2, the four-byte network event (doc 08). Nothing else:
no visibility-grid or fog write, no statistic, no score, no positional sound.
Bounded absence, re-confirmed: no cue is raised by the death or removal path
(§8.3); the teardown purge is the only removal-side touch.

**Consequences.** An `init_cloaked`
unit owned by the view slot raises 14 on its first paid pass ([05 R-ECO-01
§9] arm (d)) and, if the speech gate passes, prints `Cloaked`; an unpayable
or not-due pass on a hidden unit raises 15; a pass that leaves the byte
unchanged raises nothing; a computer player's cloaking units raise nothing;
a second 14 while one is queued is dropped; and the CRT stream is untouched
until the drain resolves the entry.

#### The streamed narration path: mode 2 is live, its delay timer, half-buffer refill, and stop [R-AUD-02 §1]

Everything here is presentation-only and runs on the application pump; no
simulation phase reads any of it [03 §1].

**Mode 2 is live (Established).** The WAV loader's mode 2 is entered
through a **timer callback** — a code pointer, not a call, which a
call-graph census does not see — and is the path every mission narration
and end-of-mission glamour sound takes. The one wrapper that takes a
caller-supplied volume without the timer has no reference of any kind in
the image and is dead (bounded negative over call edges, data references
and code-pointer dwords).

**Established fact — who streams.** Exactly two sounds are streamed, both
through one gate that returns without playing when the no-DirectSound flag
is set (so under `NoDirectSound` or `UseWindowsSound` there is **no**
narration and no glamour sound — the `PlaySound` backend of [R-AUD-01 §1]
is never offered them):

| Producer | Path | When |
|---|---|---|
| briefing screen open, and the briefing text loader (`MSNBRIEF.GUI`, [08 R-CAMP-01 §2]) | mission resource slot 3 (`narration`) | unless the session is in the live-battle state |
| results sequence state 6 ([08 R-CAMP-01 §6]) | mission resource slot 8 (`glamoursound`) | once per results pass (a latch) |

Both pass **delay 60** and **volume 0**. The delay is in the scaled
presentation-tick units of the timer table (`GetTickCount × rate / 1000`,
[07 R-CAM-01 §1]) — two seconds at the default rate — and the volume is the
DirectSound attenuation (`0` = full scale), *not* the −585 of ordinary cues.

**Established fact — start.** `StartStream(path, volume, delay)` copies the
path into a single global path buffer, stores the volume in a global, arms a
timer-table slot with period `delay` whose callback is the stream opener,
and records the slot on the device (the device field the constructor
initialises to −1, [R-AUD-01 §1]). It does **not** cancel a timer already
armed: a second start inside the delay overwrites the recorded slot and the
path buffer, the first timer fires at its own deadline, its callback
cancels the *recorded* (second) slot, and opens the stream with the
buffer's *current* (second) path. Net effect: one stream, the later path,
the earlier deadline. When the timer fires the callback cancels the slot
(timer slots otherwise repeat) and runs the WAV loader of §8.2 in mode 2
with the global path and volume; the container rules (DIGI, RIFF, raw) are
exactly §8.2's, so a streamed file may be any of the three kinds.

**Established fact — the stream buffer.** The opener first cancels any
pending timer and tears down an existing stream (stop, release, close its
file). With `bps = trunc(bits / 8)` (signed truncating division) it creates
one DirectSound secondary buffer `{size 0x14, flags = STATIC | CTRLVOLUME
(0x82), bytes = channels · rate · bps · 2, format PCM(channels, rate, bits,
blockAlign = channels · bps, avgBytes = rate · channels · bps, cbSize 18)}`
— two halves of one second each. It stores the bit depth, the fill offset
(0), an end marker (−1), and the open file handle (positioned at the
payload start by the loader); fills the first half (below); `SetVolume
(volume)`; `Play(0, 0, LOOPING)`. Any failing call tears the stream down
and yields silence; nothing is retried. The buffer loops, but the poll
(below) stops it before the loop matters.

**Established fact — refill.** `Refill()` locks `half` bytes at the fill
offset (a failing lock tears down). While the end marker is −1:
`n = min(half, fileSize − filePos)` bytes are read from the file; if
`n < half` the end marker is set to `fillOffset + n` and the rest of the
half is filled with silence — `0x80` bytes for 8-bit, `0` for 16-bit, and
**unfilled** (stale bytes) for any other bit depth. Once the end marker is
set every later refill fills its whole half with silence. Unlock; the fill
offset toggles between 0 and `half`.

**Established fact — poll and stop.** The application pump's media
keepalive ([01 R-PLAT-01 §1]; the reaper of [R-AUD-02 §2]) polls the stream
when one exists, at most once per 100 ms of wall clock. The poll reads the
DirectSound **write** cursor `w` (not the play cursor) and, with `next` the
fill offset:

* end marker −1: `next == 0` → refill when `half ≤ w`; `next == half` →
  refill when `w < half` — i.e. a half is rewritten once the write cursor
  has moved into the other half;
* end marker `e ≥ 0`: `next == 0` → **stop** unless (`half ≤ e` or
  `w < half`); `next == half` → **stop** unless (`e < half` or `half ≤ w`);
  otherwise apply the refill test above. So the stream stops once the write
  cursor has left the half that holds the end of the audio — the silent
  tail already queued behind it is at most one half-buffer (one second) and
  never a wrap onto the loop.

`StopStream` (the `Start`/`PrevMenu` buttons, `SHUTUP` at stage 0, a key or
click on the glamour screen, and the results-sequence exit) cancels a
pending timer, stops and releases the buffer, and closes the file; a stop
with nothing pending is a no-op. `SHUTUP` is a toggle: its stage after the
click is tested — `0` stops, any other stage starts the narration again
(with the same 60-unit delay).

**Edges (Established).** A missing or undecodable narration file: the
loader returns without creating a buffer, the timer has already been
cancelled, and nothing plays — no diagnostic. A stream started while the
transient/voice slots are busy is unaffected: it uses none of the 32 voice
slots or 8 transient slots and is never stolen ([R-AUD-01 §1]). Nothing
sets the stream buffer's own volume after the start (bounded negative: the
only `SetVolume` on it is the opener's).

**Draws and timing.** No random draws. The 60-unit delay and the ≥100 ms
poll are wall-clock; nothing here is visible to the simulation or the
save file.

#### The reaper runs from the pump [R-AUD-02 §2]

**Established.** The reaper (walk the 8 transient samples and the 32 voice
slots, freeing or clearing every entry whose buffer reports *not playing*
or whose status query fails, then poll the stream) is called by the
application pump
on every busy iteration in which at least 100 ms of wall clock have passed
since the previous call — the "media keepalive" walk that [01 R-PLAT-01 §1]
records with its slot counts (eight, then thirty-two) and its ≥100 ms gate,
whose "keepalive virtual" is `IDirectSoundBuffer::GetStatus`. So a voice
slot is reclaimed by a later eligible busy-pump iteration. Between reaper
calls, a completed buffer can still occupy a counted voice slot; the mixer
steal ([R-AUD-01 §1] step 2) does not run a status sweep of its own. The
`MixingBuffers` comparison uses the tracked count maintained by admission,
stealing and reaping, not an immediate recount of playing buffers. The timing
comparison admits an elapsed integer-millisecond difference of 100 and rejects
99; this corrects the earlier off-by-one description. Busy-pump scheduling can
delay the call further, so this is not a guaranteed wall-clock cleanup deadline. The
reaper still also runs before every transient (mode 1) load. The constant
helper that makes `UseWindowsSound` force the no-DirectSound flag simply
returns 1 ([R-AUD-01 §1]).

#### The music tick's two category overrides, their precedence, and the category scan's pick index [R-AUD-01 §8]

The order of [R-AUD-01 §4]'s tick steps is the order of the tests in the
tick, and an implementation whose desired category can be set independently
of the play mode must reproduce steps 2 and 4. Two precisions follow.

**Established — the two overrides and their precedence.** After the
`count == 0` return and **before** the paused test:

1. **Silence (`desired == 4`, `Unused`).** `stop cdaudio`; `next` becomes
   `1` when the count is non-zero, else `0`; status 0; fade level 0; both
   fade timers cancelled; return. Because this precedes the paused test, a
   **paused** CD is stopped when a screen requests silence — the resume of
   the pause/resume pair then has nothing to resume (status is 0, and the
   resume path runs only when status ≠ 0).
2. Then the paused test (status 2 → return).
3. **Victory / Defeat (`desired ∈ {2, 3}`).** The category branch (step 6
   of §4) runs **regardless of the play mode** — including mode 0 (idle),
   where the ordinary dispatch would only stop a still-playing drive. The
   category branch's own gate then decides: a `playing` drive whose `next`
   track already carries the desired category is left alone and only the
   tail runs; anything else scans and either plays a matching track or
   stops.
4. Otherwise the play-mode switch of §4 step 5, with `Custom` (mode 4)
   entering the same category branch.

So the precedence is: count, then silence, then paused, then Victory/Defeat,
then the play mode. Nothing in the executable requests categories 2 or 3
(§4's bounded negative stands), so the second override is inert in retail
content; the first is exercised by every front-end screen ([R-AUD-01 §5]).

**The scan's pick index.** The scan plays the **`max(1, u)`-th** matching
track: the counter is decremented on each match
and the scan stops when it reaches zero or below, so `u = 0` and `u = 1`
both select the first match, `u = 2` the second, and `u = 15` the
fifteenth. The budget of `(u + 1) · count` steps is unchanged and still
guarantees a hit whenever at least one track matches. (With sixteen equally
likely values of `u`, the first matching track is chosen twice as often as
any other — a retail quirk, reproduce it.) Two draws are still consumed
nowhere else in the branch: `u` is the only draw, taken before the poll.

**Established — a dead argument.** After a successful pick the tick also
counts how many consecutive tracks after the pick (up to `count`) share the
desired category and passes that count as a second argument to the play
primitive; the primitive accepts two stack arguments and never reads the
second (the other call sites pass unrelated registers there). It has no
effect on the `play cdaudio from … to …` command, whose bound is
`physical + 1` as §4 states.


## 9. Smacker cinematics and movie capture

**Established.** The cinematic player resolves five authored Smacker files
and owns a separate message pump until playback ends. Its menu entry,
Shift-repeat latch, character-key skip, errors, sound-device binding and
return to the shell are [08 R-OOS-01 §4]. The decoder's open, decode,
next-frame, wait and close entry points have been identified in the supplied
Smacker library; the file layout and decoded content are [fmt zrb].

**Established — display size and placement.** The library honours the file's
vertical display mode before the player reads its height. The authored
alternate-scanline mode doubles the reported height and writes each decoded
row two destination rows apart, retaining the intervening rows. The player
clears the display before entry, copies at horizontal origin zero and at
`floor((480 − displayHeight) / 2)`, and requests no additional copy scaling.
The available `1.zrb` through `4.zrb` are encoded at 640×240 with this mode;
their display extent is 640×480 with alternating cleared rows. `5.zrb` is
640×304 without that mode and begins at vertical coordinate 88. Replacing
the alternate rows with duplicated image rows would change the authored
presentation. Palette changes update all 256 display RGB entries before the
corresponding decode step. Byte-level vertical-mode flags belong to [fmt zrb].

**Established — cadence boundary.** With no message pending, the player asks
the library whether a frame is due; it decodes and advances one sequential
frame only while its window has focus. The final frame is decoded before
completion. Movie cadence is independent of the simulation's 30 Hz clock.
The library uses selected audio-track playback progress when sound is active
and a file-period wall clock otherwise. All five available movies specify a
33.33 ms frame period. The executable does not skip directly to a computed
frame index when late.

**Unknown.** Exact audio-cursor calibration, delayed-host resynchronization
with active sound, and sound behavior across focus loss need the remaining
library timing and audio-backend trace. A replacement elapsed-time scheduler
must identify these differences as implementation policy until settled.
Native window restoration and close-message handling beyond the established
Alt+F4 path also remain unresolved; menu restoration is established above.

**Established — capture boundary.** Configuration contains `PlayMovie`,
`nomovie`, and `Movie Output Rate`. The separate capture path writes names
`MOVIE%03i` under the configured image output directory; a wall-clock
dispatcher gates it at a 30 Hz base divided by the output-rate setting. The
capture is the image's **only** caller that passes a zero render-mode
argument, so a captured frame carries the reduced world layer set of
[R-RAST-01 §7] — no strips 6/7, projectiles, fixed effects, per-unit pass A
work, pass B, health bars, labels or strip 9. **Unknown:** capture encoding,
numbering and failure behavior remain outside the requested Intro playback
implementation.

## 10. Established facts, supported inference, and confidence

### Established facts

- Terrain cells are 16-pixel, tiles are indexed 32-by-32, and world positions
  are 16.16 fixed-point with the half-height orthographic projection described
  above.
- TNT maps, 13-byte plot cells, 3DO sibling/child hierarchies, GAF RLE/subframe
  composition, FNT software glyphs, and indexed GDI/DirectDraw presentation are
  directly evidenced.
- LOS is quantized to 32-pixel visibility tiles, with a word mask and a
  reference-count byte-grid mode; radar surfaces are separate. Sprite-mask
  sight quantizes `floor(radius/32) - 5` while terrain-ray sight divides by 32
  without the offset; ray admission requires a strictly steeper
  height-relative slope (equality fails), and the terrain word’s high byte
  controls horizon replacement.
- The gameplay visibility gate is established: owner-identity bypass, cloak
  early-out, underwater rejection with the status-field exemption bit, then a
  four-point hull diamond over half-height-sheared tiles; weapon placement
  inlines an equivalent test, and ally bits are never merged.
- The frame composer’s ten-strip numeric order with render-mode gates,
  removal-before-update strip lifecycle, oldest-first eviction past 400
  records (steady bound 401), and the separate 300-record effect pool with
  same-invocation compaction are established. The producer census is complete
  for every strip ([R-STRIP-01 §1]): strips 2, 4, 5, 6, 7, and 9 receive
  objects (strip 4's one producer is the geothermal steam of
  [05 R-ECO-02 §3]); strips 0, 1, 3, and 8 have no producer anywhere in the
  image and are always empty. The strip objects are pooled container
  records over internal fixed-stride particle/segment lists
  ([R-STRIP-01 §2]), and the phase-11 sweep's object-internal CRT draws are
  enumerated in [R-STRIP-01 §3]. The wind direction pair's axis assignment is
  closed ([R-WIND-01]): first word = −2·speed·sin(heading) on X, second word
  = −2·speed·cos(heading) on Z.
- The LOS terrain-height word contract is established end to end: built once
  per map load from raw plot heights with the weighted shear aggregation
  (low = max, high = min, blend by thirds, sea clamp, per-column carry),
  never rebuilt mid-battle, read by the ray raster's strict two-byte horizon
  test; the fog/minimap overlay cache (bit 3 of the mode word) is the only
  lazily rebuilt grid.
- All eight projectile rendertype presentations are established, including the
  whole-renderer abort on case-2 admission failure and randomized segmented
  lines for case 7.
- Fog presents through a lazily rebuilt two-byte-per-cell cache with the
  channel rules of section 3.3; plot-flag semantics (instance-present,
  no-build, never-explored marker, placer nibble) are adjudicated;
  post-load visibility publication is synchronous with unit reconstruction.
  Fringe-anchor partition is placement order (sequential rectangle stamps,
  last-stamp-wins; orphaned raw fringe vanishes).
- Shadow presentation is established: GAF feature shadows plus the Digger,
  ordinary-mobile silhouette, and structure-rerasterization model branches;
  their option and `noshadow` gates; the model branches' index-0 `ALP` blend,
  five-pixel offset, terrain-height shear, and shadow-before-body order. Model
  shadows use no stencil, dither, or `SHD` row. The minimap's viewport marker
  is a one-pixel rectangle outline in colour-map entry 14 (`[R-MM-01 §1]`);
  the five-pixel crosshair of §3.12 is the world composer's film-mode
  diagnostic.
- Piece transforms compose as ordered in-place rotate-then-translate passes in
  Z, X, Y chronological order using floating-point trigonometry, with
  bank/heading/pitch injected into the root piece’s Z/Y/X slots; there is no
  matrix stack and no interpolation.
- Fog sprite/model culling is hard and binary; `ALP` is not used by the
  observed LOS edge, and `DitheredFog` is not a model-shadow input.
- Beam geometry is one or two one-pixel Bresenham lines with fixed orthographic
  projection; collision damage is at the beam head.
- Water/lava impacts select per-weapon GAF and sound media; no independent
  retail water mesh is proven.
- DirectSound, PlaySound, eight-slot sound arbitration, MCI CD audio, and
  Smacker cinematic/capture paths are present; there is no waveOut playback
  backend.

### Supported inference

- The staged effect strips act as a software painter’s pipeline in which later
  opaque layers overwrite earlier indexed pixels, rather than as a depth-buffer
  renderer.
- The byte-grid reference count exists to make overlapping sight circles safe
  to remove one source at a time; the separate word bitset is a compact,
  mode-dependent owner coverage/mapping grid whose universal meaning remains
  open.
- The sound ring is a crowd-control policy intended to keep important events
  audible on a small number of legacy channels, not a general mixer.
- The separate cinematic message loop and DirectDraw setup are a compatibility
  mode that temporarily owns presentation; they are not ordinary game-frame
  rendering.

### Confidence limits

- The semantic naming of individual strips remains medium (the numeric strip
  order and the complete producer census are established, [R-STRIP-01 §1];
  the names used there come from each family's asset bindings, not from any
  engine-side label). GAF frame-duration interpretation for the
  simulation-tick and wall-clock countdown cursors is established (section 4.4);
  sequence-flag naming and families not shown to use that cursor remain medium.
- In-map void rendering versus out-of-map clipped regions is less certain than
  the hard cull; the exact distinction and the persistent backbuffer behavior
  are probe-pending (a capture at the map edge settles which pixels the
  backbuffer retains beyond the play rect).
- Audio buffer flags, category cooldown units, channel field meanings, CD
  notification behavior, and Smacker ordinal names are incomplete.
- The current absence of a water mesh and 3D audio is a bounded negative census,
  not a theorem over unrecovered functions.

## Missing and unknown

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it. Findings that closed an item live in the
body — most under `R-<id>` headings — and are not restated here.

### World and visibility

- What the LOS table-by-index accessor reads for group 0 (`sightdistance <
  32`; unreachable from stock content, whose smallest authored value is 55)
  · [R-COMP-02 §1] · decider: the tagged allocator's block-header layout, or
  a retail capture of such a unit under a mod.
- Reader for plot flag bit 7, and whether any unexported code writes
  placer-nibble values into it · §2.2 · static trace over the unrecovered
  regions. Marked `TODO(T23)`.
- Dense-pack rule: whether a footprint overlapping a live anchor cell is
  rejected or silently overwrites · §2.2 · manual retail observation
  (dense-pack fringe map probe). Marked `TODO(question)`.
- Whether the fog cache's `1 = NW` corner-to-bit assignment holds · §3.3
  [R-RR16-A] · manual retail observation (asymmetric fog GAF probe). Supported
  inference today.
- Whether the sensor phase's allied-vision gate reads the option word by
  mistake: the bit it tests has no writer in the recovered image, while the
  same bit number in the adjacent rule word is the written defeated/observer
  flag · §3.4 `[R-VIS-01 §7]` · static trace over the unrecovered regions and
  the lobby option parser first; failing that, manual retail observation
  (two-human-ally skirmish probe in which only one ally has radar coverage of
  a third player's unit). Until it lands, the bounded behavior — no allied
  sensor sharing on any channel — is what Nanolathe implements.
- What the 450-tick allied radar/sensor share command carries on the receiving
  side; nothing in the recovered visibility or sensor path consumes an
  incoming share · doc 05 "Sensor sharing", doc 08 · static trace of the
  command's receive handler.
- Legacy terrain header slot 12 and attribute bytes 1, 3, 4, 5, 7: no reader
  in the loader · §2.2 `[R-TERR-01 §1]` · static trace over the unrecovered
  regions; inert until one is found.
- Edge behavior for unexplored in-map void cells, map border clipping, and
  whether the backbuffer retains stale bytes beyond the play rect · §2.2, §4.1
  · manual retail observation (map-edge capture probe).
- Whether the final minimap surfaces are ever freed; bounded-negative in the
  traced corpus · §3.6 · static trace. Marked `TODO(T23)`.
- How ground scorch marks and craters are authored — a direct map/tile edit
  versus another store · §3.7 · static trace. Marked `TODO(question)`.
- Whether the radar picture's left/right orientation within a row is mirrored
  on screen · §3.7 · manual retail observation (asymmetric-palette probe with
  row-first, column-first, and diagonal orderings). Marked `TODO(question)`.
- Minimap marker blit site · §3.9 · static trace. The layer ordering
  (contacts overwrite markers) is supported inference. Marked `TODO(question)`.
### Renderer

- **Unknown:** render-type-2 lens transparent key left by heap history; its
  constructor and bounded lifetime contain no initializing write. Observe the
  startup allocation to settle a particular session. The host uses SC18
  zero-initialization with a code-site question [R-FX-01 §4].

- The unit of the terrain-record word that sizes the composition memory
  cache (read as the map height in 16.16; `n = trunc(v / 2^20) + 1`) ·
  [R-COMP-02 §7] · the terrain loader's writer of that word.
- Which effect bodies consult the base container's inclusive `finished`
  query, and on which side of the tick increment · [R-COMP-02 §7] · caller
  census of the table slot.
- Which stock GAF sub-frames set the alternate-blitter flag that routes a
  sub-frame through the tinted blitter · [R-COMP-01 §2] · asset census over
  every sub-frame header settles it.
- A stable colour for mode-1 search arrows whose original status is neither
  exactly open nor exactly closed · §3.12 · the draw has no colour assignment
  for those admitted statuses; the prior cell and stack history settle an
  individual frame, not a portable default.
- Whether the `Send/Receive K/s` text prints the rate or `0.0` ·
  [R-COMP-01 §5] · a retail multiplayer capture for the text.
- Windowed/fullscreen mode transitions, DirectDraw surface flags, palette-loss
  recovery, and the exact blit/flip error policy; lost-surface recovery at the
  blit wrappers is established · §4.2 · static trace.
- Hidden-panel viewport subrect expansion in both backends; bounded-negative
  for a second subrect writer · §4.1, §4.2 · static trace. Marked `TODO(T23)`.
- Semantic strip naming beyond the GAF/asset bindings of [R-STRIP-01 §2]
  · §1 · static trace.
- Whether any code outside the established load-time swap and bubble sort
  smooths 3DO pieces · §2.4 · static trace.
- Pitch/bank naming in the model transform · §2.4 [R-COMP-01 §4] · static
  trace.
- Which authored axis is a unit's nose at heading zero (the sense of a
  composed piece offset is Established, [R-RAST-01 §8]; this is the
  asset-side half) · §2.4 · a probe of child translation signs at headings
  0/90/180/270.
- Whether any retail-reachable configuration attaches a `BMcode = 0` child
  unit · [R-REN-03A] · asset census. Marked `TODO(question)`.
- The selection clip rectangle's left value outside a captured panel/mode
  state, and whether any per-selected-unit plate pass, primary-selection
  treatment, or cursor use of the authored selection polygon exists ·
  [R-SEL-02A] · a mode/panel capture of the descriptor at the selection call;
  a retail capture with two selected units and points on each projected hull
  edge.
- Whether stock play ever reaches the strip pool's 1000 live containers ·
  [R-FX-02 §4] · a strip-object count over a retail recording of a large
  late-game battle.
- Whether the unit catalog's ordinal `0` is a reserved null slot (the
  loader's sort walk starts at ordinal 1; the feature pseudo-unit records
  ordinal `0`) — if a stock unit type could occupy it, that type's structure
  shadow would be suppressed below sea level · [R-RAST-01 §4] · static trace
  of the catalog allocator and every ordinal writer.
- Whether retail carries any name for the display-mode byte that forces every
  unit body through the tinted blitter. Its role, its complete three-site
  writer set, its `0,1,2,3,4,0` cycle and the absence of any authored,
  session, mission or map source are settled in §3.12 "Debug display mode";
  [R-RAST-01 §7] owns the body-blend reader. Only the name is open, and no
  static trace can settle it · §3.12 · manual retail observation of a
  developer build or of film mode's own text.
- The identical-model six-variant shading matrix predicted by [R-RND-02A] has
  not been run · §5 · manual retail observation.
- The "loaded surface wrapper" named beside the cursor save-under surfaces ·
  [R-FX-01 §7] · static trace of its allocation site. (The mouse-event buffer
  named with it is closed in [R-FX-01 §7].)
- The follow-camera assignment writer, and the retail tracking command or
  producer behind it · §5.6.1 · static trace over a whole-image reference
  census, or manual retail observation. Marked `TODO(CRD-006)` at two sites.
- Any shell path that uses GDI text directly · §7.1 [R-FONT-01] · import
  and reference census of `TextOut`/`DrawText` callers.
- Malformed `hattfont` with no frame at the `I` index (undefined by
  construction) — whether any installed asset triggers it · §7.1
  [R-FONT-01 §6] · asset census over GAF fonts.
- Input repeat, focus and activation rules, key-token translation, cursor
  capture, gadget hit-testing, and complete HUD/minimap palette composition
  · doc 07 · static trace.

### Projectiles and effects

- The roll word of the projectile angle block for non-meteor models — drawn
  from whatever the pool slot last held · §5.4, [06 R-WFX-01 §4] · writer
  census of that word (doc 06 owns the record).
- Purpose of the nanolathe particle word set to `0x100` at spawn; read by
  neither the particle advance nor the particle draw · §5.5 [R-P0-19-P],
  [R-FX-01 §7] · full-image reference census over the sub-record offset.
  Marked `TODO(question)`.
- Beam fixed-point scale and lifetime edge cases, collision ordering at map
  borders, and line-colour remap initialization · §5.4 · static trace.
- Missile target invalidation and reacquisition · §5.4 · static trace (doc
  06 owns the homing contract).

### Audio and music

* **Unknown:** the packaged GOG adapter's actual first-track reply and initial
  category list on a given platform, because its status path leaves the
  reply buffer untouched and disc serial/list state can override defaults ·
  [R-AUD-01 §4 "Packaged MP3 media"] · manual retail observation identifying
  the response buffer and category-list provenance.

- The speech-*text* threshold writer among the sound-options gadgets (the
  audio threshold's writer is established) · §8.3, [R-AUD-01 §2] · static
  trace of the `SOUNDSRT` handler (doc 07 owns the screen).
- Whether dynamically or externally reached callers outside the bounded
  direct-invocation census can drive the 18-byte sound broadcast packet in
  game · §8.3 · static trace over the unrecovered regions.
- Exact PCM conversion for every legacy WAV variant beyond the DIGI and raw
  rules of §8.2 · §8.2 · asset census of the non-RIFF files.
- Exact left/right gain curve after DirectSound receives the established 3-D
  vector · [R-AUD-01 §1] · platform/library documentation or a controlled
  stereo capture. **Implementation reconciliation (Unknown):** Nanolathe's
  PCM backend currently uses a linear-X balance approximation after applying
  the established planar distance factor; that host policy is not labelled a
  retail stereo law. Marked `TODO(question)` at the conversion boundary.
- Device behavior when the derived 3-D minimum or maximum distance is zero,
  negative, or has maximum below minimum · [R-AUD-01 §1] · a malformed-view
  trace against the library. **Implementation reconciliation (Unknown):** the
  host keeps the base level for nonpositive bounds and clamps a reversed range
  to its minimum; neither policy is a claimed retail outcome. Marked
  `TODO(question)` at the placement boundary.

### Video and capture

**Implementation reconciliation.** The reference install now supplies all
five original movies under `Data`, so absence of the Intro file no longer
blocks codec research and validation. The authored container, image and audio
contracts belong to [fmt zrb]. The independently authored `formats/zrb` decoder has been verified against all
five movies, and the main-menu Intro player uses their image, palette and
audio data. Portable windowed output, joint audio/video pause on focus loss
and uncalibrated device playback-position scheduling are implementation
policies documented in DESIGN_INTERFACE_HUD_INPUT §3.9; they do not settle
the remaining proprietary audio timing questions below.

**Established.** The movie sequencer's observable behavior is recorded in
[08 R-OOS-01 §4], with display size, scanline mode and cadence in §9. The
repeat latch has a live main-menu writer, and sound is gated by an available
audio device rather than by the full-screen flag. These correct earlier
interpretations; the menu's separate full-screen requirement remains.

**Unknown:**

- Exact audio-cursor calibration, delayed-host resynchronization with active
  sound and focus-loss audio behavior · §9 · library/backend timing trace.
- Native window restoration and quit/close handling beyond Alt+F4, and movie
  message-loop ownership of the main renderer lock · §9 · static trace.
- `Movie Output Rate` exact units, capture frame numbering, file format and
  encoder, capture failure behavior, and whether the cursor and interface
  layers reach the capture surface · §9 · static trace. The **world** content
  of a captured frame is settled in [R-RAST-01 §7].
