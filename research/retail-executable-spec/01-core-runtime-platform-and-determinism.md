# Retail executable contract: core runtime, platform, and determinism

This document records behavior established from static analysis of the retail
`TotalA.exe`. It is a clean-room behavioral contract, not a source translation.
It deliberately omits executable addresses, data-image offsets, and raw
decompiler names. The phrases **established**, **supported inference**, and
**unknown** distinguish what the analysis proves from what remains a useful but
unverified interpretation.

The evidence is the executable's own control and data flow, its PE import
table, and its embedded string vocabulary. Nothing here is taken from another
engine, a capture, or a reimplementation.

## 1. Runtime boundary and design categories

The executable is a 32-bit PE Windows program built with the Microsoft C
runtime conventions of its era. The runtime contract naturally divides into:

1. process singleton, startup, window creation, message dispatch, and orderly
   shutdown;
2. a wall-clock budget that decides how many fixed simulation steps run;
3. one authoritative simulation thread, with a conditional diagnostic helper
   thread that is disabled in normal startup, and thread-local C-runtime state;
4. fixed pools and linked queues whose immediate reuse and iteration order are
   part of deterministic behavior;
5. two random streams and a process-wide x87 floating-point environment;
6. Win32 diagnostics, configuration, error, and compatibility paths; and
7. optional multiplayer transport and media services that are polled by the
   same application pump.

### Established platform surface

The imports include USER32, KERNEL32, GDI32, ADVAPI32, SHELL32, WINMM, DirectDraw,
DirectSound, DirectPlay extensions, and Smacker. There are no imports for
sockets, OpenGL, Direct3D, Vulkan, DirectInput, modern raw-input, modern audio
mixers, or a general worker-pool runtime. The application therefore expects a
Win32 window/message environment and treats old multimedia APIs as optional
backends.

The import list also establishes compatibility behavior that an implementation
must account for even when the normal game path does not exercise it:

- `OutputDebugStringA`, `DebugBreak`, `RaiseException`, `FatalAppExitA`, and
  `SetUnhandledExceptionFilter` support diagnostics and fatal reporting.
- `IsBadReadPtr`, `IsBadWritePtr`, and `IsBadCodePtr` are imported for defensive
  pointer checks. Their exact guarded regions are not fully mapped.
- `VirtualProtect`, `VirtualQuery`, `CreateFileMappingA`, and `MapViewOfFile`
  are imported. The current notes do not prove which subsystem owns each use.
- `SetPriorityClass` and `GetPriorityClass` are present; no stable gameplay
  policy has been recovered from them.
- USER32 dialog, timer, hot-key, clipboard, focus, cursor, and key-state APIs
  are present. They are part of the shell and input contract, not evidence of a
  separate input thread.
- locale, environment, file-time, console-handler, device-control, and
  module/procedure lookup calls support legacy installation and diagnostics.

These imports are direct evidence of available platform dependencies. An
implementation should not infer that every import is on the normal battle path.

## 2. Process startup, singleton, window, and message loop

### 2.1 Startup sequence

The established startup sequence for the normal process entry is:

1. Call the diagnostic initializer with a constant argument that installs the
   unhandled-exception filter and the FPU setup but disables the helper thread
   (the helper thread is created only when bit 3 of that argument is clear; the
   normal path sets that bit, so no helper thread is created).
2. Perform the named-semaphore singleton check. The game attempts to open an
   existing semaphore named `Total Annihilation` with full access; if it exists,
   startup takes the immediate singleton/error path and returns without creating
   a window. Otherwise it creates a semaphore with initial and maximum count 1.
3. Seed the per-thread CRT/TLS random stream.
4. Parse the command line, including capturing a bare language token when
   present.
5. Create the display context and the application window (register class, create
   window, initialize the chosen GDI or DirectDraw output).
6. Install the fixed 30-unit scaled timebase.
7. Mount the content providers described in the VFS section. Window creation
   therefore precedes content mounting.
8. Resolve language (command-line token, then registry value, then English
   fallback) and load the translation table.
9. Read the remaining registry/profile configuration and select display, sound,
   music, and game-speed options, temporarily adjusting the Windows AudioCD
   shell registry value when needed and remembering state to restore it on exit.
10. Enter the application pump and game-mode dispatcher.

The Park–Miller simulation random stream is **not** seeded during this process
startup; it is seeded at battle entry from `QueryPerformanceCounter` (battle
entry also reseeds the CRT stream there; §7.2).

The process title and registered class both identify the game as “Total
Annihilation”. The default display dimensions are 640 by 480. The startup notes
show the class uses the standard application icon/cursor resources and creates
the window before output initialization finishes.

Evidence: the startup call chain, the archive mount sequence, the PE import
table, and the startup/configuration string vocabulary. Confidence: high for the
singleton, title/class, default size, CWD, window-before-mount order, CRT seed
before command-line, and timebase after window creation; medium for the exact
ordering of all media initialization and registry restoration.

### 2.2 Window and display creation

The window setup uses `GlobalMemoryStatus` and `SystemParametersInfoA` before
registration. The latter calls use two legacy action values (`0x5E` and `0x5D`);
their symbolic OS names remain unknown but the numeric call contract is
established: at startup one call uses action `0x5E` with `uiParam` 0 storing
into the display context and flags 1, and a second call uses action `0x5D`
with `uiParam` 0, null pointer, flags 1; shutdown restores with action `0x5D`
using the value saved from the first call. `GetTickCount` timestamps are taken
around setup for diagnostics and frame accounting.

The registered class is created with style `CS_DBLCLKS` (8), a window procedure
supplied by the game, the standard application icon, and a standard cursor
resource. The created window uses extended style `WS_EX_APPWINDOW` (`0x00040000`)
and style `WS_POPUP | WS_VISIBLE | WS_SYSMENU` (`0x90080000`), positioned with
`CW_USEDEFAULT` for origin and the configured width/height. It has no
`WS_OVERLAPPED`, `WS_CAPTION`, or `WS_THICKFRAME` bits.

The retail window procedure dispatches at least the following messages and
forwards all others to `DefWindowProcA`: `WM_CREATE`, `WM_DESTROY`,
`WM_ACTIVATE`, `WM_CLOSE`, `WM_KEYDOWN`, `WM_CHAR`, `WM_SYSKEYDOWN`,
`WM_SYSCOMMAND`, mouse move/button/double-click messages, `WM_DEVICECHANGE`,
`WM_QUERYNEWPALETTE`, `WM_PALETTECHANGED`, and custom message `0x3B9`.
`WM_PAINT`, `WM_SIZE`, `WM_TIMER`, focus, display-change, and power messages
are among those that take the default tail.

Display creation is described in detail in document 03. The runtime contract
here is that the chosen display path must provide an 8-bit indexed framebuffer,
palette installation, and a present operation that can be called from the main
thread while holding the renderer’s global “MAIN” synchronization region.

### 2.3 Main pump and shutdown

The normal application loop combines message dispatch, audio polling, and game
mode work:

- It checks for queued messages with `PeekMessageA`.
- A queued message, or a mode that is not waiting on multiplayer work, enters a
  blocking `GetMessageA`/`TranslateMessage`/`DispatchMessage` sequence. Quit
  messages terminate this loop.
- When there is no immediately queued message and the windowed/network
  condition holds (display-mode flag set or networked session), the pump runs
  its housekeeping helper and then, when at least 99 milliseconds
  have elapsed since the previous one, exactly one media-keepalive call that
  walks the audio/media channel arrays (eight then thirty-two object slots,
  invoking each live object's keepalive virtual and clearing dead slots; the
  "keepalive" reading of that virtual is an inference — the walk itself, its
  slot counts, and its ≥99 ms gate are established). That
  ≥99 ms gate drives only this keepalive. The network/game dispatcher itself
  runs every busy iteration; it tail-dispatches the session callback, which
  evaluates the fixed wall-clock budget each time. The loop does not use a
  33-millisecond `Sleep` to drive simulation. A second, distinct keepalive is
  the network control message: on the networked zero-budget path the
  dispatcher sends one control byte every 60 scaled units (two seconds),
  gated on its own stamp. The two cadences must not be conflated: the ≥99 ms
  gate belongs to the pump's media walk, the 60-unit gate to the network
  control send.
- Audio/CD status is polled from this same application activity. Media playback
  is not proven to have a general-purpose audio worker owned by the game.

Lobby, synchronization, and commander-placement barriers may call `Sleep(50)`
while waiting. This sleep is a wait policy for a barrier, not the simulation
clock. The same distinction applies to any modal or cinematic message loop.

Shutdown behavior is only partially recovered. The AudioCD registry adjustment
is explicitly restored; display, sound, archive handles, semaphore lifetime,
and worker termination are not all traced to a single finalizer. A clean
implementation should make each resource’s ownership explicit and preserve
the observed failure paths rather than assuming process termination is the
only cleanup.

## 3. Configuration, installation, and compatibility runtime

### 3.1 Registry and profile sources

The registry helper creates or opens a per-application key under the current
user's `Software\\Cavedog Entertainment` branch. Document 02 carries the
complete value-name list, the two subkeys, and every installed default; note
that the helper *creates* the key path even on a read, and that a missing value
is written back with its default at startup.

The executable also refers to an INI path ending in `totala.ini` and uses
`GetPrivateProfileIntA`, but the INI imports are confined to the diagnostics
helpers — no INI read exists on the startup, front-end, or battle
configuration path (bounded by the WinMain body, the front-end router, and
the settings loader). The registry settings loader runs at front-end entry
with default-and-write-back for every value; the command-line parser runs in
WinMain before display initialization and sets its own switch bits and scalar
slots. Precedence for the overlapping scalars is therefore defaults, then
registry, then command line; the exact interaction of the two scalar command-
line slots with the registry values is not individually mapped
(`TODO(question)`). Language precedence is command line, then registry, then
English fallback.

Startup temporarily mutates a machine AudioCD registry shell value, then
restores the prior value. This is a compatibility side effect, not a game
state setting; it should be isolated from deterministic simulation state.

Evidence: the clean-room account is
the main-loop call chain plus the configuration string vocabulary. The behavior is
high confidence for key names, registry API family, and the narrowed
defaults < registry < command-line precedence; medium only for the two
unmapped scalar command-line slots.

### 3.2 Virtual filesystem boundary

The content layer maintains an ordered provider array. A loose host-file open
is attempted first. If it fails, providers are searched linearly and the first
matching provider supplies the file. Startup mounts, in append order: the
revision package `rev<name>.GP3` with keep-open flag 1; every `*.CCX` with flag
1; every `*.UFO` with flag 0; up to ten successfully mounted local `*.HPI` with
flag 0 (the eleventh successful local HPI is not mounted); then `*.hpi` on each
enumerated `DRIVE_CDROM` with flag 0. The module directory is made the initial
current directory before this work. The keep-open flag does not affect search
precedence; it controls whether the archive handle remains open after validation
(flag 1 keeps open, flag 0 closes and lazily reopens on next open). A separate
validation pass temporarily reopens closed providers to test availability and
prunes failures, closing successful validation opens again.

HPI loading validates the HAPI header/footer, decodes the rolling-XOR directory,
and supports stored and compressed blocks through a bounded 64-KiB decompression
window. Save writing is a separate direct `CreateFileA`/`WriteFile`/`CloseHandle`
path; no memory mapping is established for the observed save writer.

The archive contract is specified in document 02. Case-insensitive path
comparison is established. Path splitting uses backslash only, forward slash is
an ordinary name byte, `.` and `..` have no special traversal meaning, and
within one directory the last duplicate entry wins scanned backwards. Same-
extension enumeration order is host `FindFirstFileA` order, which retail does not
sort and is therefore not a portable executable-defined order.

## 4. Fixed-step clock, speed, pause, and lag

### 4.1 Timebase

The authoritative simulation quantum is 30 ticks per nominal second. A boot
helper converts `GetTickCount()` milliseconds to an integer scaled time by
computing:

```
scaledNow = floor(GetTickCountMilliseconds * 30 / 1000)
```

`QueryPerformanceCounter` is not the tick driver. Its established startup use
is to seed the simulation random stream. `GetTickCount` is the wall-clock input
to the budget and is also used for profiling. The 30-Hz interpretation is
confirmed by the timebase initialization, the one-second cadence constants, and
multiple tick modulo/capture paths.

### 4.2 Budget algorithm

Each budget sample keeps an integer scaled-time anchor, a signed raw delta, a
floating fractional carry, and a requested/active speed pair. Let `delta` be
the signed difference between this sample’s `scaledNow` and the prior anchor.
Let `activeSpeed` be an integer from 1 through 20. The normal speed multiplier
is `activeSpeed * 0.1`, with 10 therefore being nominal speed.

For multiplayer lag control, compute the distance from the current global tick
to the oldest qualifying remote-progress value found by scanning the active
remote participant slots (the scanned dword also admits an
earliest-pending-order reading of the same code; document 08 records the
ambiguity). Below 900 ticks of lag there is no throttle. At 900 or more, cap
lag at 3600 and multiply speed by:

```
max(0.01, (3600 - min(lag, 3600)) / 2700)
```

The resulting raw budget is:

```
raw = double(delta) * effectiveSpeed + double(fractionalCarry)
```

Convert `raw` to an integer by truncation toward zero, store the remainder
`raw - truncated` as a 32-bit float carry, then clamp the runnable count to
the inclusive range 0 through 5. A raw count of 6 or more is therefore capped
at 5; the excess integer work is not queued as a separate backlog. Pause
interacts differently with the single-player and multiplayer dispatch paths
(section 4.3).

The direct notes establish that the carry is a float, the product is computed
in x87 double precision, and conversion is the compiler’s truncating `__ftol`
path. Relying on a platform-default rounded integer conversion is incorrect.
The signed interpretation of a wrapped `GetTickCount` delta is also observed:
large wrap deltas become negative and then clamp to zero rather than being
repaired as elapsed time.

### 4.3 Speed state and adaptation

The user/network request is clamped to 1..20 by the common setter; the
keyboard speed keys reach the full 1..20 range (speed-up is skipped when the
requested value is already 20, speed-down when it is 1). The effective speed
is the value used in the budget. The budget's hysteresis counter decrements
on normal (<6) work and increments on capped (>=6) work:

- after more than 100 normal observations, effective speed may rise one step
  toward the requested value;
- after more than 10 capped observations, effective speed may fall one step,
  with a lower bound of 1;
- the counter resets after a step.

The common setter can set requested and active values together, while the budget
can subsequently regulate active speed under sustained load. A pending-speed
flag records active/requested disagreement. In multiplayer, pause and speed
packets are handled by the peer dispatcher; the receive case for the
pause/speed packet is established: a sub-type byte of zero updates the pause
bit from the value byte, any other sub-type applies the speed through the
common setter with the rebroadcast flag cleared — recipients apply without
rebroadcasting. The send-side byte layout of the pause packet remains a
network-framing residual (`TODO(question)`), while the speed packet send is
the common setter's own broadcast of `{type, sub-type, speed}`.

Pause asymmetry between the dispatch paths is established. The dispatcher
branches on the session's network flag:

- **Single player** short-circuits the budget call behind the pause test. The
  scaled-time anchor, raw delta, and fractional carry all stall while paused,
  and wall-clock advances unseen. Unpause therefore produces one capped burst:
  the huge raw delta clamps to five runnable ticks, and the fractional carry
  keeps only the sub-1.0 remainder of the pre-clamp value, so most paused time
  is lost.
- **Multiplayer** evaluates the budget every iteration even while paused, so
  the anchor tracks wall-clock. The integer result is discarded — zero ticks
  run — and only the sub-1.0 fractional remainder survives, so no burst occurs
  on unpause. While paused with a zero budget, the engine drains the network,
  optionally debug-renders, and services the periodic control keepalive; only
  the simulation subsystem sequence stops.

Movie capture and the screenshot hotkey both reset the scaled-time anchor to
the current scaled clock after performing their capture, so capture cadences
do not accumulate as elapsed gameplay time.

### 4.4 Tick phase order

**Established fact:** When the runnable count is nonzero, each sub-tick
increments the global tick before any phase runs [P0-09]. The established
twelve-phase order is:

1. multiplayer frame and network drain (multiplayer only) — drains the
   future-frame window and can apply resource-transfer packets before unit work;
2. per-unit sweep over players in ascending order and units in ascending pool
   order, with per-unit micro-order: general unit update, then weapon update
   (reload, target acquisition, aim latch, spawn), then COB drain of eight
   threads in ascending order plus one piece-interpolation pass, then primary
   order-queue pump (head-blocking, restart-from-head), then secondary queue pump
   (skip not-due, front-to-back), then movement integration (immediate wake-flag
   starts for StartMoving, StopMoving, MoveRate and setSFXoccupy), then slot-end
   death handling with synchronous Killed query and finalization [P0-09];
3. projectile integration and collision — captures active count at entry; a
   zero-burst root spawned in phase 2 is inside the span and can move and collide
   the same tick, while burst clones appended during iteration are outside and
   wait for the next tick [P0-09]; the phase tail runs the projectile-pool
   compactor, which reads the current (post-append) count and removes dead
   records in place preserving survivor order (see §6.1);
4. general effects and feature motion — side-list sweep (updating entries
   removed when their update returns zero), then the effect pool: position
   integration with gravity and wind terms, map-bound bounce or removal,
   per-effect animation-strip cursor advance, and an order-preserving in-place
   compaction of finished records;
5. per-player orders, path, economy, and occupancy work — outer loop players
   0 through 9 in ascending order; for each eligible player: the per-player AI
   coordinator tick (runs every tick for every eligible player, with a 30-tick
   internal cadence and ten deadline-gated vtable subtask objects) before the
   occupancy re-stamp sweep, then the settlement deadline compare, then
   deadline `tick + 30` single add (catch-up via consecutive ticks), then the
   nine-step settlement pass when the gate chain passes [P0-09];
6. feature lifecycle and reclaim or death processing (burn, wind probes,
   successor hops; reclaim credits become visible at the next settlement);
7. sequence and effect-strip advancement — the global animation-sequence
   cursor list (frame counter, remaining duration, loop flag per cursor; each
   cursor advances with the same step used per-effect in phase 4); this is not
   a line-of-sight or occupancy scan;
8. wind change — when due: interval draw `((CRT*10)/0x8000 + 5) * 30` ticks,
   new speed `simRand(maxWind − minWind) + minWind`, new heading
   `simRand(0x10000)` truncated to 16 bits (drawn only when the speed is
   nonzero), the direction vector pair computed as −2 × the fixed-point trig
   of the heading with the speed as the magnitude, the published ratio
   `speed / 5000` clamped at exactly 1.0, and the change flag;
9. meteor-shower strike scheduler — when the next-strike deadline passes,
    the strike window and following strike are re-armed from the mission's
    duration and interval (each authored in seconds and converted to ticks),
    a strike target is drawn anywhere on the map and a spawn origin offset
    from it (four CRT draws, consumed even when the storm is disabled), then
    per hit a radius and an angle draw (CRT) launch one invisible meteor
    projectile into the shared projectile pool (append at tail; silent drop
    when the pool is full), broadcast as a network packet when networked;
    this state family is the meteor shower — full arithmetic and the
    parameter sources in §4.4.1 [R-CORE-01] and doc 06 §6.5;
10. camera/scroll position update — the camera steps toward its scroll target
     (clamped at ±320 per tick, half-step when closer), the camera shake
     driver adds a CRT-drawn jitter while a shake is active (exactly two CRT
     draws per active tick, linear-decay envelope; details in §4.4.1
     [R-CORE-01]), and the view is refreshed;
11. ten object-list update sweeps — the ten effect strips of the rendering
    contract (doc 03 "Strip storage and lifecycle"): ten vectors of
    vtable-backed objects in a table allocated at battle entry; per strip in
    ascending index and per object in insertion order, a removal verdict
    virtual is evaluated before the update virtual: a positive verdict
    destroys the object (destructor invoked with argument 1) and removes it
    with stable in-place compaction, a zero verdict runs the update virtual
    and keeps the object; empty strips are skipped without touching any
    global (details in §4.4.1 [R-CORE-01]);
12. an every-eight-sub-tick cadence flip.

At the tail of every sub-tick, when networked and the transport flag is set,
the engine runs resource sharing (60-tick and 450-tick cadences) and flushes
the packet transport. This sharing block is inside the sub-tick loop, after
phase 12 — an earlier reading placed it after the loop. After the loop the
executor runs three empty barrier functions, then a 30-entry deadline-ring
slide (head advances when the head record's deadline has passed; the ring
sits beside the network receive queue and is most plausibly the receive-frame
window — supported inference, `TODO(question)` for the record owner), then
the missile/interceptor pending-list compaction (expired records invoke their
expiry callback and are removed in place). The projectile-pool compactor does
**not** run here; it runs at the projectile-phase tail (phase 3) and from the
unit-owner projectile purge (see §6.1).

**Established fact — event visibility across phases [P0-09]:**

- A newly created unit is visible to later same-tick phases when its
  player and slot lie ahead of the current scan position; a position already
  visited waits for the next tick. Free slot reuse is immediate via the lowest
  free scan with no generation counter, so later same-tick readers that test the
  alive flag correctly skip a freed slot.
- Ownership transfer is synchronous and is visible to later readers in the same
  tick when the new owner's player block has not yet been visited in the
  settlement loop.
- Build-complete — `remaining` reaching zero — publishes the product's GetBuilt
  order and builder links before the next trigger poll; victory checks are polled
  locally every 30 ticks and need the next poll to observe the product.
- Kill damage sets a dying latch but defers final pool clearing to slot-end
  death handling; a victim remains observable through settlement
  of the current tick and is removed before the next unit sweep.
- Stable iteration is by ascending player and ascending slot; there is no hidden
  map iteration and no generation-tagged handles for simulation identities
  [P0-09][P1-14].

### 4.4.1 R-CORE-01 closure — phase 9 identity, phase 10 shake arithmetic, phase 11 object family, and the visibility publication seam [R-CORE-01]

**Phase 9 is the meteor shower, not a "wind field" (correction).** The
previous text (here and in §7.3) called phase 9 the "wind-field update"
producing a "wind-field projectile". That family label is wrong: the phase's
state block is saved and restored under the section name **"Meteor"** with the
keys `Enabled`, `Active`, `Next Strike Time`, `Time Strike Ends`,
`Next Hit Time`, `Origin X/Z`, `Target X/Z`; its configuration is written by
the mission/OTA loader from the `MeteorWeapon`, `MeteorRadius`,
`MeteorDensity`, `MeteorDuration`, and `MeteorInterval` keys; and its
mechanics match doc 06 §6.5 exactly. The earlier "wind-field" label was an
inference from the phase's position after the wind change; the save-section
vocabulary and the OTA key chain disprove it. Established.

**Phase 9 mechanics (Established).** All draws are CRT; the simulation stream
is never touched by this phase. When the next-strike deadline passes (a
non-strict comparison against the global tick):

1. The active flag is set; the strike end is re-armed at
   `tick + trunc(duration × 30)`; the next strike at
   `strikeEnd + trunc(interval × 30)` — the duration and interval are the
   mission's authored seconds converted to ticks once at load; the per-hit
   spacing is `trunc(30 / density)` ticks and also serves as the initial
   next-strike value written at battle entry.
2. Scheduling draws, consumed on every due evaluation even when the storm is
   disabled: strike target = one draw scaled by the map depth then one by the
   map width; spawn origin = target plus a depth-axis offset
   `(draw × 10) / 0x8000 − 15` (always −15 through −6) and a width-axis
   offset `(draw × 30) / 0x8000 − 15` (−15 through +14).
3. While the storm is active, each hit (first on the strike's opening tick,
   then every per-hit spacing) draws a radius `(draw × radiusValue) / 0x8000`
   converted to fixed-point and an angle `draw × 2` (always even, one full
   16-bit turn), resolves the lateral scatter through the fixed-point sine and
   cosine tables, and spawns one meteor: spawn height 1350 world units, drop
   velocity −15 per tick, horizontal velocity
   `trunc(((target − origin) << 20) / 90)` per axis. The spawner appends at
   the projectile pool tail and silently drops the individual meteor when the
   pool is full (no retry, no event; the hit timer has already advanced).
   When networked it broadcasts the field data as a packet.

**Phase 10 shake (Established; extends the previous one-line description).**
A shake request arrives from the authoritative impact dispatcher with one
amplitude value applied to both axes and one duration taken from the
impacting weapon's definition. If no shake is active the two amplitude
accumulators are cleared; the new duration is
`trunc((requested + current) / 2)` blended with any current duration, the
remaining counter is set to it, the amplitudes accumulate, and the active
flag is set when the duration is positive. An options bit can make requests
return untouched. Each sub-tick with an active shake and a positive counter
consumes **exactly two CRT draws** (one per axis) and steps the camera
origin by

```
sx = amplitudeX * remaining / duration      (signed, truncating)
offsetX = rand() * sx / 0x8000 − sx / 2     (signed truncating division)
```

so the envelope decays linearly with the remaining counter. The tick after
the counter reaches zero clears the active flag and consumes **no draws**.
The jitter lands in the authoritative camera origin; the final view clamp
holds it inside the map.

**Phase 11 object family (correction + closure).** The previous text said
"each object's update virtual runs, and objects returning zero are destroyed
and removed" and left the family unidentified. Both halves are superseded by
direct reads of the sweep: the evaluated virtual is a **removal verdict
evaluated before the update work** — a **positive** verdict destroys (calling
the object's destructor entry with argument 1) and removes the object with
stable left compaction, while a **zero** verdict runs a second virtual (the
update work) and keeps the object. The polarity matters: doc 03's strip
lifecycle ("destroying and stably compacting on a positive verdict") is the
correct reading and always was; the inverted phrasing came from an earlier
note. Established. The family is closed as **the ten effect strips of the
rendering contract**: the table holds ten vector descriptors (each a tag
byte, a zeroed word, and begin/end pointers), allocated at battle entry and
freed at battle exit with every object destroyed; producers append at the
vector end chosen by a literal strip index (doc 03's producer census), with
the pre-insert count above 400 destroying the oldest object first; strips are
swept in ascending index and objects in insertion order. A presentation-side
helper also walks whole strips to invoke a per-object notification entry.
The sweep consumes no random draws and changes no globals when every strip is
empty. The per-object update internals are doc 03's territory.

**Visibility publication seam (Established, phase placement).** The per-unit
coverage writers live inside phase 5 itself: the phase runs the path
scheduler first, then per player in ascending order dispatches orders and
per-player work, then sweeps that player's unit slice stamping coverage for
each unit flagged in-game — the stamp is dirty-checked, re-rasterizing a
unit's coverage only when its stored stamp cell or sight range changed (so a
unit that moved in phase 2 is re-stamped in the same tick's phase 5, and an
unchanged unit writes nothing). The bulk wipe-and-rebuild runs at battle
entry and, inside phase 5, only in the commander spawn/defeat branches —
never per tick. There is no visibility pass in phase 12, the cadence flip,
or the post-loop tail: the phase-5 sweep is the final publisher, and phases
6 and later read the same-tick updated coverage for every player already
processed (ascending order).

## 5. Threads, TLS, locks, and synchronization

### 5.1 Threads

The normal simulation, message pump, rendering, input, and game-state mutation
run on the main thread. The executable contains a conditional diagnostic helper
thread implementation with a requested stack size of 8,000 bytes whose body uses
`GetMessage` and runs diagnostic initialization; it is not proven to execute
gameplay phases. **Normal startup disables this thread**: the diagnostic
initializer is called with a constant argument whose bit 3 is set, and the helper
thread is created only when that bit is clear, so the normal path creates no
helper thread. The exception filter (gated by bit 1) and FPU setup (gated by
bit 2) remain enabled in that same call.

The C-runtime thread-local block is 116 bytes. It stores thread identity and a
four-byte `rand` state in the historical Microsoft layout (seed established at
startup from local/system time and time-zone conversion at one-second
resolution, and reseeded at battle entry from the same helper; §7.2). Each
thread obtains the block lazily with `TlsGetValue`; missing
state is allocated, initialized, and installed with `TlsSetValue`. No gameplay
worker pool is established by the current call census.

### 5.2 Locking

The main renderer uses a process-wide lock/ownership region whose owner marker
contains the value “MAIN”. Acquisition exchanges the owner marker and may wait
on an event; release restores the marker and signals the event. This is distinct
from the separately evidenced named singleton object. A second global recursive
lock compresses owner identity to a process/thread token with
`InterlockedExchange`, waits on contention, and protects
decompression-window/tree state.

Heap and rendering helpers also use `InitializeCriticalSection`,
`EnterCriticalSection`, `LeaveCriticalSection`, and `DeleteCriticalSection`.
The exact ownership graph among archive, palette, and surface locks is not
settled. Avoid assuming all locks are recursive or that the renderer lock is
the only presentation serialization.

## 6. Allocation, pools, object lifetime, and queues

The behavior is pool-oriented rather than garbage-collected. Allocation helpers
tag blocks with human-readable subsystem labels, zero or initialize them, and
sometimes grow a vector by reallocation. Immediate slot reuse and linear scan
order are observable and therefore deterministic.

### 6.1 Established fixed pools

A direct reread of
all projectile count writers, allocation sequences, the projectile-phase loop,
and the compaction routine resolves the older projectile-lifecycle conflict in
favor of append allocation and stable tail compaction. The following contracts
are established:

| Pool | Capacity/record contract | Allocation and retirement |
| --- | --- | --- |
| Unit instances | 280-byte records; capacity is a game value derived from setup multiplied by ten plus one, yielding roughly two thousand to five thousand stock slots rather than the 500 folklore; the pool is sliced per player by sorted player order, each slice holding as many records as there are definition types, with slot zero reserved as null; allocation scans the owning player's slice for the lowest free flag and reuses it immediately, and an alive mask marks a live slot; per-definition limits are enforced by a flag and a value of minus one meaning unlimited, counted by scanning the slice; the canonical allocator is the sole allocation site for every creation path and the reconstructor validates a forced slot against slice bounds and occupancy, with every limit, slice-full, out-of-bounds, or occupied case returning a null handle and consuming no RNG; freeing clears alive masks, heaps, order queues, and attachments but retains the stored slot index; saving uses forced-slot reconstruction and a stale 16-bit packet that validates only slot nonzero and alive, so it aliases a reused occupant silently |
| Projectiles | Exactly 300 records, 107 bytes each | Allocation appends at the active-span tail. Retirement sets a dead flag without changing the count. Stable compaction runs at the projectile-phase tail every sub-tick (reading the current post-append count), and again immediately after the unit-owner projectile purge when a unit dies; it removes dead records, preserves survivor order, and repairs the affected projectile and follow-camera links. The post-loop pass is a different structure (see §6.2). |
| Feature definitions | Each type has a 128-byte copy; type table records use a 256-byte stride | Preallocated at map/catalog load; type IDs are stable for the loaded catalog. |
| Live features | A 48-byte live record plus a 13-byte plot cell per map attribute cell | Plot cells point to feature anchors; removal returns the cell to the free sentinel and releases the live record. Map-row order is deterministic. |
| COB threads | Eight 164-byte thread records per unit | Lowest clear thread-mask bit is selected. Ending/sleeping a thread clears its active bit; the scan is fixed order. |
| Construction nodes | A 86-byte node; factories use separate tail/head links selected by a flag | Nodes append to a per-factory chain, coalesce matching build types where applicable, and are freed on cancellation/completion. |
| Effect/sequence strips | Variable vectors of segment records, with a global cap of about 400 for the nanolathe/effect family | Append in event order; a compaction/drain pass moves/removes old entries. Exact ownership of every strip is not yet proven. |

The unit maximum is the game value described above, not a universal
500 constant. The 300-projectile capacity, packed record size, append
allocation, deferred retirement, and compaction behavior are direct
observations. Freeing a unit clears its alive masks, heaps, queues, and
attachments but leaves its stored slot index intact; the reconstructor and
save path use that forced slot for stable identity, and stale references that
hold only the 16-bit slot with a nonzero and alive check will silently alias a
later occupant after reuse.

### 6.2 Established queues

- Per-unit local order nodes are 86 bytes and attach to a per-unit chain. A
  ten-slot delayed order ring is used by the order path. Same-type/target
  duplicates can replace or coalesce; cancellation splices and frees nodes.
  Orders are serialized inside unit records rather than as a separate saved
  queue.
- The network future-frame window is 30 frames. A sequence outside the accepted
  window, a duplicate below the base, or an unauthorized host frame is dropped.
  The outbound packet queue is 1024 entries and per-peer future storage is 512
  entries; a datagram can carry about 1,066 bytes. Network queues are not saved.
- Path requests are per-player linked queues. A search drains at most 100 nodes
  per pass; a 150-tick deadline is also recorded. Duplicate goals can overwrite
  an existing request.
- The post-loop ring is a 30-entry circular window of 72-byte deadline
  records: after the tick body, while the head entry's deadline (record value
  plus the current window offset times thirty ticks) has passed, the head
  advances one slot with wraparound. The ring sits beside the network receive
  queue and matches the 30-frame future window, so it is most plausibly the
  receive-frame window (supported inference); it is not a generic timer
  queue. A separate post-loop pass compacts the missile/interceptor pending
  list (24-byte-stride records with deadline fields), invoking each expired
  record's expiry callback and removing it in place — this is the "deferred
  compaction" of earlier notes, distinct from the projectile-pool compactor
  of §6.1.
- Delayed status events use deadlines of `globalTick + 30 + random(300 or
  900)`, with the choice depending on the event family.
- Audio arbitration uses an eight-slot channel ring, described in document 03.

The fixed iteration order is: units and features by ascending slot or map
index, players by player index, and linked queues by chain order. Projectile
iteration is linear over the packed active span. The projectile phase captures
the span count once at entry, so records appended during that scan are not
updated until the next projectile phase. Its tail compactor reads the current
global count and therefore includes those newly appended records when removing
dead entries. A zero-burst projectile created by the preceding unit phase is in
the captured span and is eligible to move and collide in its creation tick,
subject to its family-specific launch and expiry state. A burst clone created
during projectile iteration cannot move until the following tick.

### 6.3 Failure and exhaustion behavior

The direct evidence establishes graceful failure for a failed HPI or media
resource, a failed optional GAF lookup, and a failed projectile reservation.
Projectile allocation failure emits no corresponding start sound/COB/fire
event. Network buffer exhaustion can produce a fatal receive/allocation path;
the exact user-facing action is not uniform. General heap failure, vector growth
failure, and archive decompression failure are not exhaustively traced.

## 7. Deterministic random streams

### 7.1 Simulation stream

The authoritative simulation stream is one process-wide 31-bit Park–Miller
state. The update uses Schrage constants:

- multiplier 16,807;
- modulus 2,147,483,647;
- quotient 127,773; and
- remainder 2,836.

For a positive bound of at least 2, update the state with the Lehmer recurrence,
add the modulus when the intermediate value is nonpositive, then return the
unsigned remainder modulo the bound. Bounds below 2 return zero without a
useful draw. Startup seeds this stream from the sum of the low and high parts
of `QueryPerformanceCounter`, XORed with a fixed constant and forced odd; the
seed setter has exactly one call site in the recovered image — the
battle-entry orchestrator — so neither startup, loading, nor any packet
handler reseeds it elsewhere. A single state is shared by placement, wind,
effects, combat, and other callers; call order, not entity identity, isolates
consumers.

### 7.2 Microsoft CRT stream

The second stream is per-thread and uses the classic Microsoft recurrence:

```
state = state * 214013 + 2531011
return (state >> 16) & 32767
```

The state is the four-byte field in the 116-byte TLS block. Startup seeds it
from local/system time and time-zone conversion, at effectively one-second
resolution, and **battle entry reseeds it again** from the same time-of-day
helper: the seed helper has exactly two call sites in the recovered image,
process startup and the battle-entry orchestrator. The battle-entry seed
writes the calling thread's block — the main thread whose state every
gameplay draw reads — so battle entry references and replaces the same
stream state; no copy of "process CRT state" is taken, and with the normal
startup creating no helper thread the main thread's block is the only
battle-relevant CRT stream. The stream supplies the meteor-shower draws, the
wind-change interval jitter, and UI/media variants, and is also consumed by
the camera-shake driver inside the tick (two draws per shake step while a
shake is active); it is not the simulation Park–Miller stream. The wind tick
consumes both streams for different outputs, making the separation
observable.

Sampling bounds above 32,767 use a chunk-concatenation loop before the final
modulo: starting with mask and result both `0x7FFF`, while the mask is below
the needed bound, shift both left by 15 bits, OR the mask with `0x7FFF` again,
and OR another fresh `rand() & 0x7FFF` draw into the result; the final result
modulo the needed bound is the sample. This identical inlined helper appears
wherever a CRT draw needs a wider range.

### 7.3 Sampling, wind draws, and save implications

Placement draws X then Y from the global simulation stream. Wind consumption
spans both streams and is fully recovered:

- (Corrected) At briefing-screen entry the CRT stream draws
  `rand() % (maxWind − minWind + 1) + minWind` from the mission's parsed
  minimum/maximum bounds and then `rand() & 0x3F`. The earlier text presented
  these as the battle's initial wind values; they are **front-end display
  state only** — the two values are stored in briefing-screen globals whose
  only readers are the briefing/front-end region itself, and no battle-side
  reader exists in the recovered image. The battle's actual initial wind is
  drawn by the wind-change routine at tick 1 (see below).
- In simulation, the wind change falls due when the global tick passes the
  wind deadline (a strict comparison); the next deadline advances by
  `((CRT draw * 10) / 0x8000 + 5) * 30` ticks — five through fourteen seconds
  quantized to 30-tick units, using 64-bit multiply/divide, drawn **before**
  the new speed and heading.
- When due, the new speed is a bounded simulation-stream draw
  `simRand(maxWind − minWind) + minWind`. The new heading is a simulation draw
  of `simRand(0x10000)` truncated to 16 bits, taken only when the speed is
  nonzero; the direction vector pair is computed as **−2 × the fixed-point
  trig** of the heading (the −2 factor was omitted from earlier revisions).
  Battle entry itself initializes the wind by zeroing the deadline and
  calling the wind-change routine — but with the global tick still zero the
  strict gate does not fire, so that call consumes **no draws**; the first
  wind chain runs inside the first sub-tick, after the executor has
  incremented the global tick.
- The published float ratio is `(float)speed / (float)5000` — the denominator
  is a fixed constant written once at battle entry — stored as a 32-bit float
  and clamped from above at exactly 1.0 (overflow stores the float bit
  pattern for 1.0).
- (Corrected) The second deadline gates the **meteor-shower strike
  scheduler** (see §4.4.1 [R-CORE-01] and doc 06 §6.5), not a wind-field
  update. Its draws are all CRT and in a fixed order: four scheduling draws
  on every due evaluation even when the storm is disabled (map-depth-scaled
  then map-width-scaled target draws, then a depth-axis and a width-axis
  origin offset), then per hit a radius draw and an angle draw
  (`draw × 2`, always even) before each invisible meteor projectile is
  reserved from the shared projectile pool at the append tail — when the pool
  is full the reservation fails silently (no retry, no event) and the network
  variant broadcasts the field data as a packet.

Random sound variants use the CRT path. There is no recovered per-player or
per-weapon RNG state, explicit peer RNG synchronization, or complete saved RNG
snapshot. Save/load therefore has an unresolved determinism risk if a resumed
match is expected to continue the exact pre-save stream.

The fixed-step scheduler block is **saved** (see below). RNG state is not part
of that block.

#### R-CORE-02 closure — battle RNG seeding and a chronological draw census [R-CORE-02]

**Seeding (Established).** At battle entry the orchestrator seeds both
streams before any battle setup runs: the simulation stream is seeded from
the `QueryPerformanceCounter` sample (low part plus high part, XOR the fixed
constant, forced odd), and the CRT stream is reseeded from the time-of-day
helper (local time with time-zone and daylight handling, one-second
resolution). The CRT seed helper has exactly two call sites — process startup
and battle entry — and the simulation seed setter exactly one (battle entry).
Both writes land in the calling (main) thread's state: the CRT write replaces
the same TLS block every gameplay draw reads; nothing is copied. Because both
seeds are taken fresh at battle entry, **every draw made before battle entry
is wiped from the streams' state** — pre-battle consumption cannot influence
battle determinism (only the seed instants can).

**Chronological draw census (process start through early battle ticks):**

| When | Stream | Draws | Consumer |
|---|---|---|---|
| Process startup | CRT | 0 (seed only) | TLS stream state ← time-of-day helper |
| Menu/front-end screens | CRT | unbounded (variant paths) | UI/media random variants; not censused exhaustively |
| Briefing-screen entry | CRT | 2 | wind display: speed `% (max−min+1) + min`, then direction `& 0x3F` — front-end display globals, no battle-side reader |
| Battle entry | both | 0 (reseeds only) | simulation ← QPC sum; CRT ← time-of-day; global tick ← 0 |
| Battle entry, skirmish setup | CRT | count−1 (Fisher-Yates swap draws), plus one 50/50 gate draw when fewer than three qualifying players | player-slot assignment shuffle (skirmish start positions; skipped entirely when a saved game is being loaded) |
| Battle entry, networked setup | sim | 2 per placed commander (one per axis of the start point) | commander start placement |
| Battle entry, campaign/mission setup | sim | 2 per placed unit (X then Y) | initial-mission unit creation |
| Battle entry, wind initialization | — | 0 | the wind-change routine is called with a zeroed deadline while the global tick is still zero; the strict gate does not fire |
| First sub-tick (global tick 1) | CRT, then sim | 1 CRT (interval), then 1 sim (speed), then 1 sim (heading) only when speed ≠ 0 | phase 8 wind change, now due (deadline 0 < tick 1) |
| Sub-tick when a strike is due | CRT | 4 scheduling draws, + 2 per meteor hit (radius, then angle) | phase 9 meteor shower |
| Sub-tick with an active shake | CRT | 2 | phase 10 camera shake |
| Sub-tick with non-empty strips | object-internal | none at the dispatcher level | phase 11 strip sweep |

Front-end draws between startup and battle entry are real CRT consumption
but carry no battle consequence because battle entry reseeds both streams;
they matter only for reproducing front-end behavior itself (for example the
briefing wind display).

**Save/load (Established; wording corrected).** Loading re-enters the
battle-entry orchestrator, so both streams are reseeded unconditionally
**before** the saved state is read: the earlier wording "reseeded from
`QueryPerformanceCounter` and `time(NULL)`" named the mechanism imprecisely —
the CRT source is the same time-of-day helper as at startup, not the C
library `time()` directly, and the reseed is a consequence of load re-running
the battle-entry path rather than a separate loader step. The first post-load
draws are the battle-entry setup draws appropriate to the mode (the
skirmish shuffle is skipped when a save is present), then the tick-1 wind
chain — the wind deadline is re-zeroed unconditionally by battle entry, so
the wind is always redrawn on the first loaded tick — and any meteor hit due
at once if the restored meteor box re-arms a due strike. The meteor
scheduling box itself is restored from the save (its keys are listed in
§4.4.1); the streams are not.

#### Scheduler persistence

The retail save writes a `Players/GameTime` binary box of exactly 28 bytes
copied from the scheduler block, and the loader requests 28 bytes and proceeds
only when 28 bytes are returned (a larger on-disk box is tolerated — the
request copies the first 28 bytes and ignores the remainder; a shorter box
fails the scheduler restore without partial application). The layout is:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

RNG state (Park–Miller process-wide and CRT TLS state) is outside this block
and is absent from the bounded save-writer graph; on load both streams are
reseeded because load re-enters the battle-entry orchestrator (see
§7.3 [R-CORE-02]), so a resumed game does not continue the pre-save random
sequence bit-identically. The meteor-scheduling state, by contrast, is saved
and restored in its own box. Replay formats are not covered by this save-box
contract.

## 8. x87 floating point and integer conversion

The executable uses x87 arithmetic; no SSE simulation path is established.
The default control word is the Microsoft/CRT 53-bit precision, round-to-nearest,
masked-exception environment. Optional `-fpufussy`/`-fpunofussy` diagnostics can
change whether the setup helper runs, but normal startup does not intentionally
change the default control word.

Authoritative code mixes integer/fixed-point, 32-bit float, and 64-bit double:

- counters, flags, pool indices, and most map dimensions are integer;
- persistent world positions and selected totals are double;
- per-unit contributors, published ratios, and many definition values are
  float;
- double-to-float stores round at the store boundary, so extended-register
  precision must not be silently retained across a stored float;
- angle and position helpers use fixed-point integer tables where documented by
  the rendering contract.

The compiler `__ftol` helper snapshots the control word, sets round-control to
truncate toward zero for the integer store, performs a signed 64-bit x87 store,
and restores the original control word. Integer casts in simulation that are
fed by this helper must truncate, not floor or nearest-round. Overflow produces
the x87 indefinite integer; callers generally do not perform an explicit range
check.

Short-lived helpers around DirectSound save/restore the x87 control word while
masking exceptions. Trigonometric helpers may temporarily set a canonical
precision/mask pattern and restore it. No stable simulation policy was found
that globally changes precision per phase.

A recurring confusion must be resolved explicitly: the scaled-clock factor
(30) is a plain integer field of the display context, written once by the
timebase installer and read by the scaled clock and by the window procedure's
input timestamps. The value 0x27F is the default x87 control word asserted by
the C-runtime floating-point helpers (they check-and-restore it before FP
operations). The two values are not the same field; earlier corpus notes read
both into one display-context field, which is wrong.

## 9. Diagnostics, anti-tamper, and error paths

Startup can load `DebugHelper.dll` when the debug-helper switch is present,
install an unhandled-exception filter unless disabled by a mask, initialize the
FPU unless disabled, and start the diagnostic helper thread only when its
enable bit is clear. Normal startup enables the filter and FPU but disables the
helper thread; normal battle entry therefore uses the path that installs the
filter and FPU setup without creating the helper thread.

The executable emits debug strings and can break, raise, or show fatal dialogs.
The network path includes an executable-integrity-breach message and can remove
a peer or disconnect the match after an integrity violation.

Peer state synchronization is push-and-overwrite, not compare-and-react: a
background scanner polls roughly every quarter second and pushes each remote
participant's counters and resource totals to peers (an immediate echo path
re-emits the push toward its originator), and receivers copy every field into
local state with no comparison, no threshold, and no abort — the sole gate is
an anti-spam check on echo flooding. Divergence surfaces only indirectly,
through the kick/chat flows: the integrity-breach notice posted repeatedly as
chat before disconnect cleanup, disconnect notices, and a manual sync-error
chat command (handler inferred).

An internal code-checksum routine returns zero unconditionally in retail,
leaving its guarded code-segment-checksum-error diagnostic branch dead;
self-checks run only when switching front-end states, never per tick. The
front-end/game-mode state machine (the "orchestrator" of earlier notes) is
recovered: it switches between game modes over a router state byte with
sub-states, runs the checksum self-check between every state transition, and
loads the numbered `1.zrb`..`5.zrb` list files from the `Data` directory at
specific states; what the list machinery does with the loaded bytes (mission
list parsing) remains in unrecovered code.

Profiling stores per-phase `GetTickCount` deltas, frame-rate counters, and a
rolling sample history. Those counters are diagnostics and must not feed the
authoritative tick state. Performance-counter strings such as “Returns
Predicted” are present in the binary but have no proven normal-path gameplay
consumer.

Error dialogs cover window registration/output failure, sound initialization
failure, movie setup/open/pixel-format failure, and archive/resource failure.
The exact distinction between recoverable fallback and fatal termination is
subsystem-specific.

## 10. Established facts, supported inference, and unresolved boundaries

### Established facts

- Win32 process/window/message-pump architecture with a named singleton;
  second-instance `OpenSemaphoreA` with an existing object returns immediately
  with `-1` and no window activation or handoff.
- 30-Hz scaled `GetTickCount` budget, carry, truncation, zero-to-five cap, and
  50-ms barrier waits that are not the tick driver; the ≥99 ms pump gate drives
  exactly one media-keepalive call while the budget itself is evaluated every
  busy pump iteration, and the networked zero-budget path sends a separate
  one-byte control keepalive every 60 scaled units.
- Single-player pause stalls the budget anchor (one capped burst on unpause);
  multiplayer pause discards the integer budget while keeping the fractional
  remainder (no burst). Movie capture and the screenshot hotkey reset the
  scaled-time anchor.
- The phase order and global-tick increment position listed above.
- Main-thread simulation; conditional diagnostic helper thread exists but normal
  startup disables it; TLS CRT state seeded at process startup and Park–Miller
  state seeded at battle entry.
- Fixed unit/projectile/feature/COB/construction pools and documented queue
  capacities/order where the ledger is explicit.
- One global Park–Miller stream, one CRT TLS stream, x87 53-bit default, and
  truncating `__ftol` conversion; both streams are seeded at battle entry
  (simulation from the performance counter, CRT from the time-of-day helper;
  single call sites per §7.1/§7.2), and wind draws span both streams with the
  exact arithmetic recovered (interval jitter, bounded speed draw, 16-bit
  heading draw, −2 vector factor, ratio over the fixed denominator 5000
  clamped at exactly 1.0) while the meteor shower consumes only the CRT
  stream (§7.3 [R-CORE-02]).
- Registry/profile/legacy multimedia compatibility surface, loose-file-first
  lookup, and ordered archive-provider behavior with exact window styles
  (`WS_EX_APPWINDOW`, `WS_POPUP|WS_VISIBLE|WS_SYSMENU`, `CS_DBLCLKS`) and popup
  semantics.
- 28-byte `Players/GameTime` scheduler block is saved/restored with the field
  layout above; RNG state is not saved and is reseeded on load.

### Supported inference

- The MAIN ownership region is primarily a renderer/present lock, while the
  recursive owner-ID region protects archive decompression and related shared
  buffers.
- Active speed is a load regulator that can move away from a user request under
  sustained frame/tick pressure, then recover slowly.
- Network divergence handling is overwrite-plus-notification (kick/chat
  flows), not automatic repair or abort-on-mismatch; multiplayer tick
  advancement is soft-paced by oldest remote progress rather than synchronized
  per tick by a barrier.
- Most fixed pools are intentionally chosen to make insertion/retirement order
  deterministic, not merely as performance optimizations.

### Important contradictions or confidence limits

- Some older notes called the sequence phase LOS and the ten-vtable pass a
  renderer; current corrected notes retract both labels.
- Phase 10 was previously labelled "ledger and death cleanup"; it is the
  camera/scroll position update with camera shake (a CRT consumer). The dying
  latch and finalization belong to phase 2's slot-end death handling.
- Phase 11 was previously kept as a `TODO(T23)` no-op registration point, then
  described as "a real per-object update sweep over ten vtable-backed object
  lists (removal and destruction on zero return)" with the object family
  unidentified. Both refinements are superseded: the sweep evaluates a
  removal verdict **before** the update work and destroys on a **positive**
  verdict (the zero-return-destruction wording was inverted), and the family
  is now identified as the ten effect strips of doc 03's rendering contract
  (§4.4.1 [R-CORE-01]).
- Phase 9 was described as a "wind-field update" spawning a "wind-field
  projectile". The family label is retracted: the state block is the meteor
  shower — saved under the section name "Meteor", configured from the
  mission's `Meteor*` keys, and mechanically identical to doc 06 §6.5
  (§4.4.1 [R-CORE-01]). The mechanical descriptions (deadlines, draws, pool
  append, silent drop) were correct and stand.
- §7.3 previously presented the briefing-screen wind draws as the battle's
  initial wind values "before the simulation consumes either value". They
  are front-end display state with no battle-side reader; the battle's
  initial wind is drawn by phase 8 at tick 1, and battle entry's own
  wind-change call consumes nothing because its deadline gate is strict and
  both sides are zero (§7.3 [R-CORE-02]).
- The AI coordinator dispatch before the settlement deadline (phase 5) was a
  supported inference from a shared entry; it is now direct: the per-player
  coordinator tick runs every tick for every eligible player ahead of the
  deadline compare, with a 30-tick internal cadence.
- Projectile-pool compaction runs at the projectile-phase tail (and on the
  unit-owner projectile purge), not in the post-loop tail; the post-loop
  compactor is the missile/interceptor pending list, and the post-loop "timer
  dispatch" is a 30-entry deadline-ring slide beside the network receive
  queue (owner inferred).
- The keyboard speed range is the full 1..20, not 2..19.
- The multiplayer sharing block runs at the tail of every sub-tick (inside
  the loop), not after it.
- The display-context field at the scaled-clock factor offset holds 30; the
  0x27F control word belongs to the C-runtime FP helpers and is a different
  site.
- The scheduler anchor, raw delta, and float carry were previously interchanged;
  the current wall-clock note is authoritative for their roles.
- Earlier revisions described the window as overlapped-style and left style bits
  open; the popup style `0x90080000`/`0x00040000` and `CS_DBLCLKS` are now
  established, as is the WndProc dispatch list above.
- Earlier revisions stated normal startup creates a watchdog thread; the thread
  implementation is conditional and disabled in the normal path.
- Earlier revisions listed scheduler persistence as unknown; the 28-byte
  `Players/GameTime` block is now established as saved, while RNG persistence
  remains absent and replay coverage remains separate (the post-loop deadline
  ring is the network frame window, not a timer queue).
- Earlier revisions described periodic peer hash packets as abort-on-mismatch
  checks; receivers provably copy pushed state without any comparison, and the
  internal code-checksum stub makes its error branch unreachable in retail.
  The earlier hedge that left single-player/multiplayer pause carry behavior
  open is also closed by the asymmetry described in section 4.3.
- Function-boundary recovery is incomplete, so absence claims are bounded by
  the current import/decompile census rather than proof over every byte.
- Network and replay save coverage remains incomplete (no timer queue exists
  on the tick path; the post-loop deadline ring is not serialized).

## Missing and unknown

The following are intentionally left as implementation TODOs rather than
invented behavior.

### Process and platform

- Symbolic OS meaning of the two `SystemParametersInfoA` actions (`0x5E`/`0x5D`);
  the numeric call sequence and restore semantics are established above.
- Exact meanings of any remaining window style/ex-style bits outside the
  established `0x90080000`/`0x00040000`/`CS_DBLCLKS` values and client-area
  adjustment.
- Per-message parameter semantics beyond the case list above are closed by the
  dispatch-table note: activation flag byte, close-hook call, key translation
  helpers, input-event timestamp arithmetic, device-change/custom-message
  handler indirection, and palette-realize branches are all traced; no
  `WM_TIMER`/`SetTimer` usage exists in the window procedure.
- Singleton semaphore release and full shutdown ordering after exceptional
  failures (second-instance path returns `-1` with no handoff or activation).
- Watchdog/conditional helper thread purpose is closed: a debug-helper dialog
  message loop with a transient priority boost, no simulation-global access;
  normal startup creates no thread. Termination signal is the `WM_QUIT` of its
  own message loop.
- Every `CreateThread`, TLS destructor, thread priority, and critical-section
  callsite not covered by the current notes — the bounded census (3 thread
  creation sites, 1 TLS allocation, 8 critical-section initializations,
  12 enter/leave pairs) is in the thread note; the destructor and
  `DeleteCriticalSection` sites remain outside the recovered window.
- Owners and lifetime of `VirtualProtect`, `VirtualQuery`, file mappings,
  device-control, console-handler, environment, locale, and module-loader
  calls — the bounded site census is in the platform note (each facility has
  exactly one recovered wrapper site plus its consumers).

### Clock, network, and determinism

- Complete pause/speed packet framing and host-permission rules: the receive
  side is established (sub-type byte selects pause-bit update or speed apply
  without rebroadcast) and the speed send is the common setter's broadcast;
  the pause send's byte layout remains a network-framing residual.
- Network future-frame overflow policy, retransmission wrap, and all late-join/
  resynchronization behavior.
- RNG state persistence (established as not saved; reseeded because load
  re-enters battle entry, §7.3 [R-CORE-02]) versus
  scheduler block persistence (established as saved above); network state and
  replay formats remain separate unknowns; the per-tick deadline ring of the
  post-loop tail is not serialized (its account would appear in the save
  writer's fixed account list, which contains none).
- Phase 11's ten object lists are closed (§4.4.1 [R-CORE-01]): the family is
  the ten effect strips of the rendering contract (doc 03 "Strip storage and
  lifecycle"), the sweep evaluates a removal verdict before the update work
  and destroys on a positive verdict, and the three post-loop barrier calls
  decompile to empty bodies (no hidden work). The remaining strip questions —
  producers for strips 0, 1, 3, 5, and 8, and per-object update internals —
  live in doc 03's account.
- The compiled weapon-record fields that carry the camera-shake magnitude and
  duration into the impact dispatcher's shake request are established
  behaviorally (§4.4.1 [R-CORE-01]); their positions in doc 06's compiled
  weapon-record field map remain unmapped (`TODO(question)`). The authored
  keys (`shakemagnitude`/`shakeduration`) and the duration × 30 compile-time
  conversion are already established in [fmt tdf] and doc 06.
- Whether the briefing wind display globals have any reader outside the
  briefing/front-end region (bounded absence: none in the recovered image; a
  dataflow census over unrecovered regions would settle it).
- Whether the networked-mode commander placement loop also runs when a saved
  networked game is loaded (the loop is not gated on the save box; only
  affects multiplayer, which is out of scope).
- Complete list of authoritative `__ftol` callers and any non-default x87
  control-word mutation reachable from simulation; the scaled-clock factor
  field is resolved as 30 (not a control word).

### Memory and queues

- The allocator’s backing implementation is narrowed: `HeapCreate`/`HeapAlloc`
  wrappers over the process heap with tagged blocks; fixed pools are
  zero-filled by their initializers, transient allocations are not; wrappers
  return 0 on failure and callers either propagate the null or show a
  message box and quit. Arena boundaries beyond the pool initializers remain
  open.
- The universal unit-pool maximum is closed: physical cap = catalog unit-
  definition count × 10 + 1 with per-player slices, slot 0 null, lowest-free
  immediate reuse, no generation counter (bounded-negative over the
  decompiled corpus); stale handles alias later occupants.
- Failure side effects of specialized projectile allocators beyond the meteor
  spawner, whose placement is closed (it appends to the shared projectile pool
  from phase 9 — the meteor-shower strike scheduler — and silently drops when
  the pool is full, with the hit timer already advanced so the slot is not
  retried). The earlier "wind-field projectile" name for that spawner is
  retracted; it is the meteor spawner (§4.4.1 [R-CORE-01]).
- Effect-strip layouts are largely typed — ten fixed strips drawn in barrier
  order, fed by one shared segment pool capped at 400 entries with oldest-first
  FIFO eviction and a per-tick compaction pass; what remains open is per-strip
  ownership registration for effects outside the nanolathe/beam/smoke families,
  plus mission-object, path-debt, and audio-node layouts (the "timer" layout of
  earlier revisions is resolved as the network deadline ring).
- Queue overflow, linked-list cycle defense, and whether all same-tick inserts
  are drained immediately or deferred by queue family.
- Save serialization is narrowed but not complete: order/task nodes serialize
  across both queue segments including their duration credit and wake state
  (their save/load pair is traced); feature saves are a plotmap census into
  three typed record families with counts — no free-list serialization exists;
  stockpile production state (slot byte, queue rounds, progress) survives;
  meteor shower globals persist in their own save block; player economy stock,
  counters, and capacities are raw single-precision bits live and therefore
  serialize bit-exact. Still open: remaining box-level field maps.

### Configuration and I/O

- Registry versus INI versus command-line precedence is narrowed: INI reads
  are confined to diagnostics helpers (none on the startup/front-end/battle
  config path); registry values load at front-end entry with
  default-and-write-back; command-line switches are parsed in WinMain before
  display init. Precedence for overlapping scalars: defaults, then registry,
  then command line; the two scalar command-line slots are not individually
  mapped against their registry twins.
- Archive enumeration order within one wildcard group (host `FindFirstFileA`
  order, not sorted) and CD-drive behavior: every `DRIVE_CDROM` drive is
  scanned in drive-letter order and contributes its archives to the mount
  table in that order; there is no pick-one CD selection.
  Same-archive duplicate-name resolution and backslash-only slash/traversal
  rules are established in document 02 and §3.2.
- Exact save header/version compatibility and all replay chunk semantics; the
  normal-save scheduler block is established above.
- Executable-directory fallback: `GetModuleFileNameA(NULL, 256-byte buffer)`
  then `SetCurrentDirectoryA` on its directory component establishes the
  content CWD independent of the launch directory; return values are not
  checked, so truncation/failure behavior is not a deliberate fallback and a
  safe implementation may fail with a diagnostic instead of reproducing unsafe
  reads.

### Error and diagnostics

- Which failures merely select a fallback and which terminate the process:
  singleton failure exits `-1` silently; display init failure shows
  "Environment Initialization Failed!" and cleans up; file-mapping failures
  carry graded codes 1/2/3; heap failures propagate null or message-box-and-
  quit; input-queue full is a silent drop.
- Exception-filter reporting and minidump/debug-helper protocol: the filter is
  installed once at startup unless masked; the debug-helper DLL and imagehlp
  minidump machinery engage only with the enable switches.
- How the integrity-breach UI maps to disconnect state (the breach packet is
  type `0x27`; posts the translated chat notice repeatedly, then kick and
  disconnect cleanup); the front-end state machine's `.zrb` list files are
  loaded at specific states and the checksum self-check runs between every
  state transition, but the list-file parse itself is in unrecovered code.
  Anti-tamper coverage is otherwise closed: the code-checksum stub returns
  zero (dead branch) and self-checks run only on front-end state switches,
  never per tick.
