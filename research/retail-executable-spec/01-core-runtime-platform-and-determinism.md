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
3. one authoritative simulation thread, a short-lived loading thread that runs
   battle entry, a cursor-redraw thread, a conditional diagnostic helper
   thread that is disabled in normal startup, and thread-local C-runtime state
   (§5.1 [R-PLAT-01 §4]);
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
entry seeds the separate loading-thread CRT block; the main-thread CRT
continues from startup [R-PLAT-01 §7]).

The process title and registered class both identify the game as “Total
Annihilation”. The default display dimensions are 640 by 480. The startup notes
show the class uses the standard application icon/cursor resources and creates
the window before output initialization finishes.

Evidence: the startup call chain, the archive mount sequence, the PE import
table, and the startup/configuration string vocabulary. Confidence: high for the
singleton, title/class, default size, CWD, window-before-mount order, CRT seed
before command-line, and timebase after window creation; medium for the exact
ordering of all media initialization and registry restoration.

### Process static initialisation and the engine block [R-PLAT-02 §1]

The evidence is the C-runtime initializer table, the game constructors it
names, and the engine-block allocator called by the process entry.

**Static initialisers (Established).** Before the process entry runs, the C
runtime walks a seventeen-entry table of game constructors in table order.
Every constructor ends by registering its matching destructor with `atexit`,
so these globals are torn down by the runtime at process exit, in reverse
registration order, after the pump has returned and after the game-state and
display teardown of [R-PLAT-02 §2]. The thirteen constructors owned by this
document (the other four are a network buffer object, a runtime red-black tree
helper, and a surface-setup record — lanes 08, 07 and 03) do the following,
in table order:

1. construct the fixed effect pool's static object (its tick is doc 04
   `R-COB-04 §4/§5`);
2. construct an empty vector for the map/resource identity list of the lobby
   screen (doc 07);
3. construct the **order descriptor table** as an empty vector — the four
   template batches of [04 §3.1] append to it at initialisation;
4. fill the 32-record movement-class scratch table with the template priors:
   maximum water depth 10000, minimum water depth −10000, all four slope
   bytes 255, every other field zero ([02 R-CONTENT-01] owns the record);
5. and 6. allocate two identical network receive objects, each a 28,000-byte
   buffer plus three 2,800-byte buffers (out of scope, [08 R-OOS-01]);
7. set bit 0 of a C-runtime option word (its only reader is the runtime);
8. allocate a pool of 1,000 records of 76 bytes for the effects family (doc
   03);
9. construct an empty vector for the vismask shape list of the LOS raster
   ([03 R-VIS-01 §2]);
10. construct an empty vector of the movement class and model catalog
    ([02 R-P0-03]);
11. construct a zeroed three-word movement-class record (doc 02);
12. zero the developer console's two spawn pointer words ([R-PLAT-01 §9]);
13. construct an empty vector consumed by the `+` command handlers
    ([07 R-CAM-01 §6]).

Five of these are the same "empty vector" constructor: three zero words
(begin, end, capacity) and a tag byte that is written from an **uninitialised
register** — the tag has no reader in the recovered image (bounded negative),
so nothing observable depends on it.

**The engine block (Established).** The process entry allocates the engine
block after installing the allocation-failure hook and before the singleton
test:

```
skew      = (GetTickCount() mod 1000) × 7          ; 0 … 6993 bytes
size      = skew + 0x3924D
allocation = alloc(size), zero-filled by the caller  ; the allocator does not fill [R-PLAT-01 §5]
engineBase = allocation + skew
```

The block therefore floats at a wall-clock-dependent offset inside a
correspondingly larger allocation; the raw allocation pointer is not stored
anywhere, so the skew has no behavioural effect beyond the placement. The
entry then stores the build-stamp strings `Jul 30 1998` and `11:16:36` in the
block, runs the per-player network-record initialiser over the eleven player
records (out of scope), and zeroes ten catalog count/pointer words. A null
allocation would leave a null engine base, but the hook never returns, so the
path is unreachable. Nothing in the recovered image frees the block.

**Two housekeeping registrations at entry (Established).** Guarded by a once
bit, the entry registers an **empty** `atexit` handler (a no-op; it exists so
the registration happens exactly once). The entry itself runs inside the
runtime's structured-exception frame (the wrapper that calls the game's
`WinMain`); that frame is the C runtime's own — the game's crash reporting is
the unhandled-exception filter of [R-PLAT-01 §8].

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
- When there is no immediately queued message and the window is active or the
  session is networked, the pump runs its housekeeping helper and then, when
  at least 100 milliseconds
  have elapsed since the previous one, exactly one media-keepalive call that
  walks the audio/media channel arrays (eight then thirty-two object slots,
  invoking each live object's keepalive virtual and clearing dead slots).
  That walk is the audio reaper — eight transient plus thirty-two voice slots,
  then the narration-stream poll ([03 R-AUD-02 §2], Established). That
  ≥100 ms gate drives only this keepalive. The network/game dispatcher itself
  runs every busy iteration; it tail-dispatches the session callback, which
  evaluates the fixed wall-clock budget each time. The loop does not use a
  33-millisecond `Sleep` to drive simulation. A second, distinct keepalive is
  the network control message: on the networked zero-budget path the
  dispatcher sends one control byte every 60 scaled units (two seconds),
  gated on its own stamp. The two cadences must not be conflated: the ≥100 ms
  gate belongs to the pump's media walk, the 60-unit gate to the network
  control send.
- Audio/CD status is polled from this same application activity. Media playback
  is not proven to have a general-purpose audio worker owned by the game.

The word the pump tests for the busy path is the **window activation word**
that the window procedure writes on `WM_ACTIVATE` (low word of `wParam`
nonzero → 1, else 0), not a display-mode flag. The busy path runs while the
window is active or the session is networked; an inactive single-player window
blocks in `GetMessageA` and neither the budget nor the housekeeping runs. The
full pump is [R-PLAT-01 §1] below.

Lobby, synchronization, and commander-placement barriers may call `Sleep(50)`
while waiting. This sleep is a wait policy for a barrier, not the simulation
clock. The same distinction applies to any modal or cinematic message loop.

Shutdown behavior is only partially recovered. The AudioCD registry adjustment
is explicitly restored; display, sound, archive handles, semaphore lifetime,
and worker termination are not all traced to a single finalizer. A clean
implementation should make each resource’s ownership explicit and preserve
the observed failure paths rather than assuming process termination is the
only cleanup.

### The application pump, activation gating, and the battle host pump [R-PLAT-01 §1]

The evidence is a direct read of the process entry, the window procedure, the
pump housekeeping helper and the battle host pump.

**Startup order, exactly (Established; refines §2.1).** Diagnostic init with
argument 8 (filter and FPU setup on, helper thread off — §9 [R-PLAT-01 §8]);
install the allocation-failure hook ([R-PLAT-01 §5]); the singleton semaphore
test; CRT `srand` from the time-of-day helper (this seeds the **main
thread's** CRT block — the only seed that block ever receives, see
[R-PLAT-01 §4]); command-line parse (a parse failure returns exit code 1 —
[R-PLAT-01 §2]); display defaults 640×480 and the display flags word (see
below); window creation (failure returns 0); the 30-unit timebase; mounts;
language resolution; translation table; audio device; settings; the AudioCD
shell swap; then the pump. On exit: when the **quit-requested bit** is set,
stop the cursor thread, release its surfaces and run the whole game-state
teardown; restore the AudioCD value; tear down the display ([R-PLAT-02 §2]).

**The display flags word.** Startup sets bit 0 of the display flags word to
the complement of the display mode chosen by `-D`/`-Df` (bit 0 = `~mode & 1`,
so the default mode 3 and the `-D` mode 3 both clear it and `-Df` mode 2 sets
it) and ORs in the constant `0x3F2` (bits 1, 4, 5, 6, 7, 8, 9). Bit 9 is
copied by the window creator into the "cursor thread" bit of the display
object, so the cursor thread is **always** created in retail ([R-PLAT-01 §4]).

**Pump iteration (Established).** Each iteration:

1. *Focus-driven audio suspend/resume.* If the activation word is 0 and the
   audio object exists: save the CD play lists to the registry, remember the
   current volume, suspend the device, and set a "suspended" latch. If the
   activation word is nonzero, the audio device pointer is null, and the
   latch is set: re-create the device, re-register the completion callback,
   re-apply the two audio preference bits, restore the remembered volume,
   re-read the CD lists, clear the latch. Focus loss therefore silences and
   releases the audio device; focus gain rebuilds it.
2. `PeekMessageA` (no remove). If a message is queued, **or** the activation
   word is 0 and the session is not networked, fall into the blocking
   `GetMessageA`/`TranslateMessage`/`DispatchMessageA` (a `WM_QUIT` return
   ends the pump).
3. Otherwise run the housekeeping helper (next paragraph), then if
   `GetTickCount() − lastKeepalive > 99` (signed) run the media keepalive
   walk once and stamp.

**Housekeeping helper (Established; the input side is doc 07's).** In order:
peek the key ring head — the developer-console token when the developer bit is
set pops it and restores the display; the F2 token when the ESC-menu bit is
set pops it, closes the menu, clears the bit and, unless the session kind is
multiplayer, clears the pause bit; the screenshot token pops it, creates the
`screenshots` directory beside the install, captures, and **resets the
scaled-time anchor to the current scaled clock** (the reset §4.3 records) —
these three peeks are [07 R-CAM-01 §1]'s "input ordering"; pop one button
record (or copy the motion slot — [R-PLAT-01 §6]); run the sound service;
copy the record into the canonical pointer record; then call the current
mode's frame function unless the display object's **quit-requested** bit is
set ([R-PLAT-02 §2]; it is set only by the quit-request routine).

**Battle host pump (Established; the mode frame function while in battle).**

1. Profile bookkeeping: the nine phase counters are summed into a total (at
   least 1), copied to the display copy, zeroed, and the frame stamp taken —
   diagnostics only.
2. *Single player* (session kind 1 or 2): if the ESC-menu bit is set, skip
   straight to step 5 — the budget is not evaluated, no tick runs, no host
   frame is drawn, so the scaled-time anchor stalls exactly as it does under
   pause and the menu's closing produces the same one capped burst (§4.3).
   Otherwise, if the pause bit is clear, evaluate the budget (§4.2); then if
   the pending count is nonzero, run the sub-ticks (§4.4).
3. *Multiplayer* (kind 3): evaluate the budget every iteration; with a zero
   pending count while paused, drain the network and, when the control-stamp
   deadline has passed, send the one-byte control keepalive and advance the
   stamp by 60 scaled units; with a nonzero pending count run the sub-ticks in
   networked mode, then the three empty barrier calls.
4. If the ESC-menu bit is clear: the host frame (input, dispatch, and the
   interface — [07 R-CAM-01 §1]), then the presentation update.
5. Drag/selection state bits, the HUD composer, and movie capture: while a
   movie series is armed and its next-frame deadline is at or below the
   scaled clock, capture one frame, advance the deadline by
   `30 / framesPerSecond` scaled units, and reset the scaled-time anchor
   ([07 R-CAM-01 §8]).

**Budget re-read (Established, confirms §4.2 and §4.3 unchanged).** The lag
scan qualifies a remote slot when its record exists, its control byte is 3, a
progress word is nonzero, and its frame word is below the global tick; the
minimum frame word gives `lag = tick − min`. The pending-speed bit is set when
the active speed is below the requested one. Nothing in the re-read changes
the arithmetic already recorded.

### The quit request, the exit path, and the shutdown sequence [R-PLAT-02 §2]

The evidence is the quit-request routine, the window creator, the exit tail of
the process entry, the game-state teardown and the display teardown.

**Which bit the pump and the exit test (Established).** The housekeeping
helper's gate on the mode frame function and the exit tail's gate on the
game-state teardown read the same bit of the display object's flags word: the
**quit-requested bit**, written by exactly one routine (below). The
cursor-thread mark is the *adjacent lower* bit, which the window creator
copies from bit 9 of the display-flags configuration word (the cursor thread
is always created, [R-PLAT-01 §4]). The window creator also clears the
quit-requested bit, so it starts clear in every run.

**The quit request (Established).** Every process quit goes through one
routine (the front end's `EXIT` [07 R-FE-01 §3] through the router, the
front-end lobby-launch failure path, the battle teardown's exit step
[07 R-FE-02 §3], and the out-of-scope DirectPlay session path all reach
it). It:

1. sets the quit-requested bit;
2. when the display's full-screen bit is set, restores the desktop display
   mode (the surface-release routine with argument 0);
3. when a message was supplied, shows it in a modal box parented to the game
   window and titled with the application title;
4. posts `WM_DESTROY` to the game window.

From step 1 on, the housekeeping helper no longer calls the mode frame
function, so no host frame, budget evaluation or sub-tick runs while the
posted message drains; the window procedure's `WM_DESTROY` handling calls
`PostQuitMessage(0)` and the pump ends on the resulting `WM_QUIT`.

**The exit tail (Established).** After the pump returns:

1. **Only when the quit-requested bit is set:** stop the cursor thread
   ([R-PLAT-01 §4]) and run the game-state teardown below. A window
   destroyed without a quit request (an external close) skips both and leaves
   every game object to process teardown.
2. Restore the `AudioCD` shell registry value saved at startup and write the
   application's `cdshell` registry value.
3. Run the display teardown below.
4. Return the `WM_QUIT` message's `wParam` as the process exit code.

**Game-state teardown, in order (Established; the internals are each owning
document's).**

1. Read every CD play list back from the audio object into the `CDLISTS`
   array and write it to the registry ([03 R-AUD-01 §4]).
2. Flush the bitmap cache: for each of its ten entries that holds a surface,
   release the surface, free the data, null the entry, and null any reference
   to it held by the current-background word or by the open window's
   background pointer (doc 07 owns the cache).
3. Free the interface object's two side buffers and its record buffer.
4. Free the common GAF, the side fonts and the two preloaded fonts
   ([03 R-FONT-01 §5]).
5. Tear down the sound catalog: for every sound category, the 24 pairs of
   per-category buffers whose count is positive; the category array; then
   every loaded sample — released through DirectSound, or plain-freed when
   the Windows-sound flag is set ([03 R-AUD-01 §1]); then the remaining
   audio and effect teardown routines of doc 03.
6. Free the `OFFSCREEN` surface, clear the renderer's target, and run the
   DirectDraw surface-restore routine ([03 §4.1], [03 §4.2]).
7. Empty the order descriptor table by resetting its end pointer to its base
   (the storage is not freed; [04 §3.1]).
8. Free the setup record ([08 R-ENTRY-01 §2]).
9. Unlock and free the unit-definition table and zero its count ([02 §3]).
10. Close the network object: free the send buffer; when the session is
    networked, flush packet pacing, close the transport unless the online
    score-reporting callback is installed, and clear the networked bit (all
    but the free are out of scope, [08 R-OOS-01]).
11. Destroy the mission/session record ([07 R-FE-02 §3]) and free the
    front-end map list.

**Display teardown, in order (Established).** Stop the cursor thread (a
second, idempotent call); free every font object in the display's font list —
close its file handle, free its data, free the record — then the list; free
the lookup tables the display flags say were allocated ([R-PLAT-02 §3] — in
retail all five); release the four DirectDraw surfaces and then the
DirectDraw object through their COM `Release` entries; delete the memory DC,
the DIB section and the logical palette; restore `SystemParametersInfoA`
action `0x5D` with the value saved at window creation (§2.2).

### Window-creation residue: defaults, the lookup tables, the recorded directory, the custom-message callback [R-PLAT-02 §3]

The evidence is the display-defaults routine, the window creator and its
helpers, and the callback setter.

**Display defaults (Established; refines [R-PLAT-01 §1]).** Before the entry
computes the display-flags configuration word it calls the defaults routine,
which clears bits 0–9 of that word (so the `0x3F2` OR of [R-PLAT-01 §1]
starts from a clean low half), sets width 640 and height 480, the client
rectangle `(0, 0)–(639, 479)`, and a floating scale word of exactly 1.0.

**What the window creator allocates (Established).** Besides the class,
window, key ring and cursor thread already recorded: the total physical
memory from `GlobalMemoryStatus`; a `GetTickCount` stamp; the display
object's flags word rebuilt as *bit 0 set, bits 2–9 copied from bits 1–8 of
the configuration word, bit 10 from configuration bit 9* (the cursor-thread
mark), bit 11 (quit-requested) clear; the **motion slot** initialised by
copying six dwords from a three-dword zero record — its last three fields
(scaled tick, message, double-click) therefore start as uninitialised stack
contents until the first `WM_MOUSEMOVE` ([07 §2]); the current directory of
the current drive, recorded as a 256-byte string in the display object (its
only reader is the out-of-scope network path); and the lookup tables, each
allocated through the labelled wrapper only when its flag bit is set — in
retail every bit is set:

| Table label | Size (bytes) | Content owner |
|---|---|---|
| `SHADE_TABLE` | 0x2000 | [03 §4.3] |
| `ALPHA_TABLE` | 0x10000 | [03 §4.3] |
| `GRAY_TABLE` | 0x100 | [03 §4.3] |
| two further tables (allocators are doc 03's rows) | — | [03 §4.3] |

None is filled by the allocator ([R-PLAT-01 §5]); doc 03 owns the fill.
The failure path — class registration, window creation or output
initialisation failing — releases the DirectDraw surfaces and object, deletes
the DC and GDI objects, shows the modal `Error:  Environment Initialization
Failed!` (two spaces, as authored) titled with the application title,
destroys the window and returns 0, which the entry returns as the exit code.

**The custom message and its callback (Established).** The window procedure
routes the custom message `0x3B9` (§2.2) to a callback pointer held in the
display object. The pointer's only writer is a one-line setter whose only
caller is the audio device re-creation of [R-PLAT-01 §1] step 1 — the
DirectSound completion callback ([03 R-AUD-01 §1]). Nothing else uses the
message.

**The `-C` exception frame (Established; refines [R-PLAT-01 §2]).** The
online-configuration load of the `-C` switch runs under the parser's own
structured-exception frame. A fault inside the online library's loader is
caught by that frame's handler, which sets the restricted-config flag and
resumes the parser at the next token — a failing `online.dll` therefore
cannot crash startup.

**A frame-counter word that is never written (Established, bounded).** The
developer probe overlays read a display-object word through an accessor and
display it as the frame counter. Its only setter has no caller in the
recovered image, and the display object is static zero-initialised data, so
the overlays show a constant 0 in retail.

## 3. Configuration, installation, and compatibility runtime

### 3.1 Registry and profile sources

The registry helper creates or opens a per-application key under the current
user's `Software\\Cavedog Entertainment` branch. Document 02 carries the
complete value-name list, the two subkeys, and every installed default; note
that the helper *creates* the key path even on a read, and that a missing value
is written back with its default at startup.

The executable also refers to an INI path ending in `totala.ini` and uses
`GetPrivateProfileIntA`. The integer-profile accessor (`<module
directory>\totala.ini`, section `[Preferences]`) has exactly two callers, both
on the startup path: the settings loader reads `UnitLimit` (default 250,
clamped to 20..500; doc 02 R-CONTENT-03 owns it) and the sound initializer
reads `NoDirectSound` and `UseWindowsSound` ([R-PLAT-01 §2]).

The registry settings loader runs at front-end entry with
default-and-write-back for every value; the command-line parser runs in
WinMain before display initialization and sets its own switch bits and scalar
slots. Precedence for the overlapping scalars is therefore defaults, then
registry, then command line. The scalar command-line slots have **no**
registry twins: `-T` (peer timeout) and `-E` (an unread value) write
engine-block words that no registry value writes, and `-P` writes the packet
pacing block ([R-PLAT-01 §2]). Language precedence is command line,
then registry, then English fallback.

Startup temporarily mutates a machine AudioCD registry shell value, then
restores the prior value. This is a compatibility side effect, not a game
state setting; it should be isolated from deterministic simulation state.

Evidence: the clean-room account is
the main-loop call chain plus the configuration string vocabulary. The behavior is
high confidence for key names, registry API family, and the narrowed
defaults < registry < command-line precedence; medium only for the two
unmapped scalar command-line slots.

### The command-line census and the profile-file reads [R-PLAT-01 §2]

The evidence is the game's parser, the debug library's option scanner, and
every reader of each written slot (bounded to the recovered image).

**Tokenizer.** The command line is split on space and tab. Before parsing,
the peer timeout word is preset to 30, the `-E` word to 0, and the
"restricted-config" flag to 0. A token not beginning with `-` or `/` is copied
to the language slot (the last such token wins). A token the debug library
recognises (prefix match, case-insensitive, against its nineteen switches —
below) is skipped by the game parser. Otherwise the second character selects
the switch (case-insensitive). Any other letter is ignored.

| Switch | Value form | Effect (Established) | Reader |
|---|---|---|---|
| `-B<word>` / `-B <word>` | word | `lock` sets bit 0 of the lobby-option word. Every other recognised word — `deathends`, `deathplays`, `deathmatch`, `fixedloc`, `mapping`, `circlos`, `truelos`, `permlos`, `cheating`, `watching` — is compared and **writes nothing** on either outcome | the ALLIES/SHARE gadget enable and the lobby record toggle read bit 0 |
| `-C<file>` | name | online library present and its version above 2 → load the named online configuration into the online record (0x150 bytes); then sets the restricted-config flag | `1.zrb` list load is skipped when the flag is set |
| `-D` / `-Df` | — | display mode 3, or 2 when the third character is `f`/`F` | startup only: bit 0 of the display flags word (`~mode & 1`) |
| `-E<n>` | integer | `atoi`; a leading `-` yields −1; outside 0..100 → 0 | **none** in the recovered image (retained-and-inert) |
| `-F` | — | sets the fixed-drive flag | the CD-drive scan uses a fixed drive letter instead of enumerating |
| `-H[<name>]` | name or next token (unless it starts with `-`) | host flag ← 1; name copied (at most 63 bytes) | lobby/host front-end screens |
| `-L` | — | clears one word | **none** (inert) |
| `-N<k>[:<name>]` | integer, optional name | `k` = `atoi`; when `k == 1` and a `:` follows, the name is stored; `k` in 1..4 stored as the network kind; then the restricted-config flag | as `-C` |
| `-P<n>` | integer | packet pacing: `n < 0` disables; `n == 0` → interval 200 ms; else `n` clamped to 2..30 and interval `1000 / n` ms; eleven slots hold `(interval × 30 + 999) / 1000` scaled units | network transport (out of scope) |
| `-R` | rest of line | registers the application with DirectPlay (`dsetup.dll`) using the title, the executable path and the fixed GUID; on failure beeps and shows `DirectPlay registration failed.`; **the parser then returns 0 and the process exits with code 1** on both outcomes | — |
| `-S` | — | "no DirectSound" flag | sound init |
| `-T<n>` | integer | `atoi`; outside 30..300 → 30; stored as the peer timeout in seconds | the peer scanner drops a peer when `timeout × 30` scaled units have elapsed since its last packet (unsigned compare) |
| `-W` | — | "Windows sound" flag and the "no DirectSound" flag | sound init |

**Sound backend selection.** The sound initializer ORs the `-S`/`-W` flags
with the profile-file booleans `NoDirectSound` and `UseWindowsSound`
(`totala.ini` `[Preferences]`, default 0). The Windows-sound flag selects the
waveform probe path; otherwise DirectSound is initialised, and failure raises
the modal `Error:  Sound system initialization failed.` (two spaces, as
authored; doc 03 owns the audio contract).

**Debug-library switches (Established).** The diagnostics library scans
`GetCommandLineA()` itself, matching a switch only when it is followed by
whitespace, `=`, or the end of the line; booleans take an enable string and a
disable string, values take `=<decimal>` or `=0x<hex>`. The nineteen
recognised switches and their defaults:

| Switch | Default | What it does |
|---|---|---|
| `-memfussy` / `-memnofussy` / `-memfrontalign` | off | tracked `VirtualAlloc` allocator ([R-PLAT-01 §5]) |
| `-memset=<v>` / `-memnoset` | off; pattern `0xDEADBEEF` | fill every allocation with the pattern |
| `-gonzo` | on | affects only the tracked allocator's bookkeeping |
| `-fpufussy` / `-fpunofussy` | **off** | see below |
| `-enableimagehlp` / `-disableimagehlp` | **on** | symbolised stack walk in the crash report ([R-PLAT-01 §8]) |
| `-enableimagehlplines` / `-disableimagehlplines`, `-dprinton` / `-dprintoff` / `-dprintfile`, `-saveresources` | — | listed in the switch table; no reader in the recovered image |
| `-memorystatus`, `-performancestatus` | off | read only inside the helper-thread body, which normal startup never creates |
| `-debughelper[=n]` | off | `LoadLibrary("DebugHelper.dll")` and call its `DebugFunc1(n)`; failures print a diagnostic |

**`-fpufussy`, exactly (Established; refines §8).** The FPU setup helper
**always runs** from the diagnostic initializer (its gate bit is clear in the
normal argument); the switch changes the helper's *argument*, not whether it
runs. It calls the C-runtime control-word setter with the *invalid* and
*zero-divide* exception-mask bits: with the switch off (default) it sets both
mask bits (`set(0x18, mask 0x18)` in the runtime's abstract encoding — both
exceptions stay masked, which is the C-runtime default, so the control word is
unchanged); with the switch on it clears both (`set(0, mask 0x18)`), so
invalid operations and divisions by zero trap. Precision and rounding are
never touched ([R-DET-01 §3]).

### 3.2 Virtual filesystem boundary

The content layer maintains an ordered provider array. A loose host-file open
is attempted first. If it fails, providers are searched linearly and the first
matching provider supplies the file. The module directory is made the initial
current directory before mounting. Document 02 §2 owns the complete mount
order, per-invocation local-HPI budget, duplicate suppression, keep-open
semantics, and validation pass. The ten-entry budget applies to newly mounted
local HPIs in **one** mount invocation, and the mount driver invokes that path
repeatedly; there is no global ten-archive limit ([02 §2]).

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

### The scaled-clock timer table [R-PLAT-02 §4]

The evidence is the timebase installer, the timer service in the housekeeping
helper, and the registration and removal routines' callers.

**Shape.** Ten slots of four words: callback, argument, period, remaining.
A slot is armed when its period is non-negative; the timebase installer
(the `30` of §4.1) clears the table and the last-service stamp.

**Service (Established).** Once per busy pump iteration, inside the
housekeeping helper of [R-PLAT-01 §1] — after the sound service and before
the mode frame function — the service computes

```
elapsed     = scaledNow − lastService          ; scaledNow as §4.1, GetTickCount × 30 / 1000
lastService = scaledNow'                        ; a second GetTickCount read
for each armed slot:
    remaining −= elapsed
    if remaining < 1:                           ; i.e. ≤ 0, signed
        callback(argument)
        remaining = period
```

so a period-`p` timer fires once every `p` scaled units at the granularity
of the pump (a late pump fires it once, not repeatedly — the deficit is not
carried), and a one-shot timer is one that removes itself from its callback.
The two `GetTickCount` reads can differ by a millisecond; the difference is
lost, not accumulated. The service runs only on the busy path, so timers
stall while an inactive single-player window blocks in `GetMessageA`.

**Registrants (Established, bounded to the recovered image).** The only
callers of the registration routine are the CD-audio fade timers of
[03 R-AUD-01 §4] (the repeating period-2 fade step and the one-shot period-120
pause), and the removal routine's callers are all in the same audio module.
No simulation state is touched by the table or by any registrant.

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

**Established.** Save `raw` as binary64, then compute `floored = floor(raw)`
with the runtime wrapper of `[R-DET-01 §3]`. Convert that floating result
through the signed-64 truncation helper, retaining its signed low 32 bits
`[R-DET-01 §1]`. Store `raw - floored` as a 32-bit float carry, independently
of any wrapping in the integer word, then clamp that word to the inclusive
range 0 through 5. A retained count of 6 or more is therefore capped
at 5; the excess integer work is not queued as a separate backlog. Pause
interacts differently with the single-player and multiplayer dispatch paths
(section 4.3).

The carry's single-precision store can round a remainder just below one to
exactly one. For a negative fractional raw budget the floor is more negative
than truncation toward zero, leaving a positive carry. The compiler's
truncating conversion happens only after that floor wrapper; it does not
replace the floor. Relying on a platform-default integer cast is incorrect.
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
rebroadcasting. The send-side layout of the pause packet is
`{0x19, 0, newPauseBit}` and the speed packet is the common setter's own
broadcast of `{0x19, 1, speed}` — both in [R-PLAT-01 §3] below.

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

### Pause-send framing and the speed clamp [R-PLAT-01 §3]

**The pause toggle (Established).** The pause token (`0xF8`, the Pause key —
[07 §2]) reaches the battle hotkey dispatcher, which:

1. toggles bit 0 of the scheduler's pause/lag/pending word (the pause bit,
   offset `0x1A` of the saved block in §7.3 "Scheduler persistence") — the
   local state changes **before** any send;
2. builds a three-byte packet `{0x19, 0, pauseBit}` — type `0x19`, sub-kind
   0, then the **new** value of the bit (0 or 1);
3. hands it to the broadcast helper with the local player's id (the first
   slot whose record exists and whose control byte is 1 or 2; −1 when none).

**The broadcast helper (Established).** It looks the sender up; the sender
must be live with control byte 1 or 2 and a clear "dropped" byte, else it
returns 0. Then: **if the session is not networked it returns 1 without
sending anything** — the single-player pause is purely the local bit flip.
Networked, it sends through the transport (one send when the transport is in
broadcast mode; otherwise once per distinct remote peer id over the
control-byte-3 slots). The single-player boundary for every other packet the
local path still builds — and the direct helper's return of 0 when not
networked — is [08 R-OOS-01 §1]; the `-N` restricted-config flag also
suppresses the start-up cinematic ([08 R-OOS-01 §4]).

**The receive side** ([01 §4.3], established earlier): sub-kind 0 copies the
value byte into the pause bit; any other sub-kind passes the value to the
common speed setter with the rebroadcast flag clear.

**The speed clamp and sub-kind 1 (Established; owned by [07 R-CAM-01 §3]).**
The common setter clamps the request to `1..20` with signed compares (`> 20 →
20`, then `< 1 → 1`), writes both the requested and active 16-bit words, and
— when its send flag is set — emits `{0x19, 1, speed}` through the same
broadcast helper, so it too is a no-op in single player. The hotkeys compute
`target ± 1` and pass the send flag; a request of 21 or 0 is therefore
clamped, not refused (the hotkey's own refusal at 20 / 1 is the dispatcher's
pre-test, [07 R-CAM-01 §2]).

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
the packet transport. This sharing block is **inside** the sub-tick loop,
after phase 12. After the loop the executor runs a three-step tail: three
empty barrier functions; the 30-entry in-battle message-ring retire (the
text-scroll ring doc 07 owns — [R-PLAT-02 §8]; the retire is the one
[R-PLAT-02 §7] describes); then the expiry pass over the temporary-sight
("eyeball") observer list of 36-byte records, whose expired entries invoke
their expiry callback and are removed in place ([R-PLAT-02 §5]). The
projectile-pool compactor does **not** run here; it runs at the
projectile-phase tail (phase 3) and from the unit-owner projectile purge
(see §6.1).

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

### 4.4.1 Phase 9 identity, phase 10 shake arithmetic, phase 11 object family, and the visibility publication seam [R-CORE-01]

**Phase 9 is the meteor shower (Established).** The phase's state block is
saved and restored under the section name **"Meteor"** with the keys
`Enabled`, `Active`, `Next Strike Time`, `Time Strike Ends`, `Next Hit Time`,
`Origin X/Z`, `Target X/Z`; its configuration is written by the mission/OTA
loader from the `MeteorWeapon`, `MeteorRadius`, `MeteorDensity`,
`MeteorDuration`, and `MeteorInterval` keys; and its mechanics match doc 06
§6.5 exactly. It is not a wind-field update, despite sitting immediately after
the wind change in the phase order.

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

**Phase 10 shake (Established).** A shake request arrives from the authoritative impact dispatcher with one
amplitude value applied to both axes and one duration taken from the
impacting weapon's definition. If no shake is active the two amplitude
accumulators are cleared; the new duration is
`trunc((requested + current) / 2)` blended with any current duration, the
remaining counter is set to it, the amplitudes accumulate, and the active
flag is set when the duration is positive. An options bit can make requests
return untouched: it is bit 4 of the session preference word, whose only
toggle is the typed `NoShake` command ([03 R-FX-01 §7], [07 §11 "Mask 1"]);
no `.ini`/registry key and no option-panel control drives it. Each sub-tick
with an active shake and a positive counter
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

**Phase 11 object family (Established).** The virtual the sweep evaluates
first is a **removal verdict, evaluated before the update work** — a
**positive** verdict destroys the object (calling its destructor entry with
argument 1) and removes it with stable left compaction, while a **zero**
verdict runs a second virtual (the update work) and keeps the object. The
polarity matters, and it agrees with doc 03's strip lifecycle ("destroying and
stably compacting on a positive verdict"). The family is **the ten effect
strips of the rendering contract**: the table holds ten vector descriptors
(each a tag byte, a zeroed word, and begin/end pointers), allocated at battle
entry and
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

#### Phase 12 radar blink cadence [R-CORE-03][CRD-008]

**State and ownership (Established).** Phase 12 owns two pieces of radar
presentation state. `radarBlinkCountdown` is a signed 16-bit countdown, whose
normal values are 7 through 0. `radarBlinkPhase` is bit 0 of a 16-bit radar
dirty/status word; the other bits in that word are independent mapped/final
surface and camera-dirty flags and are not part of this cadence contract. The
state is presentation-owned radar state, not authoritative world, economy, or
gameplay state. Its mutation is nevertheless scheduled by the authoritative
tick executor, so catch-up stepping advances it in the same deterministic
sub-tick order as the rest of the phase graph. It consumes no RNG draw.

**Reset and exact predicate (Established).** Radar surface setup at battle
entry sets `radarBlinkCountdown` to exactly 7 and clears `radarBlinkPhase` to
0, preserving unrelated status bits. This setup is also part of the battle
entry path used while loading a saved session. Therefore the state immediately
after battle initialization, before global tick 1, is countdown 7 and phase 0.
For every runnable sub-tick, after the global tick has been incremented and
after phases 1 through 11, phase 12 applies this exact predicate:

1. If `radarBlinkCountdown > 0` (strictly), subtract one and leave the phase
   bit unchanged.
2. Otherwise, set the countdown to exactly 7 and toggle only bit 0 of the
   radar status word.

The countdown is therefore a seven-to-zero countdown, followed by a reload
and phase toggle on the next phase-12 invocation. The predicate is on this
owned countdown, not on `globalTick % 8`; the global tick labels the boundary
but does not replace the countdown. The ordinary reset cycle never reaches a
negative value.

**Producer and consumer census (Established, bounded to the recovered retail
image).** The battle-entry radar setup is the producer that initializes the
countdown and phase bit. Phase 12 is the only recurring producer: it decrements
the countdown or reloads it and toggles bit 0. Other writers of the same status
word update unrelated dirty bits and do not participate in the blink cadence.
The minimap contact renderer consumes the phase bit for regular unit blips:
when a contact's per-unit blink-suppression byte is nonzero, the contact is
drawn only when the phase bit is set (a zero suppression byte draws it in both
phases). The dashed interceptor-ring renderer also consumes the bit as the
dash-parity seed. No simulation, economy, order, visibility-mask, or RNG path
reads this bit. No other bit-0 reader or writer was found in the recovered
image; that negative census does not claim coverage of unrecovered code.

**Save/load treatment (Established).** The phase-12 countdown and blink bit
are absent from the bounded save-writer census. The save may carry radar image
and Mapping data, but those products do not serialize this transient cadence
state. Loading re-enters battle-entry orchestration, which resets the state to
countdown 7 and phase 0; the loader does not resume the pre-save cadence from a
saved countdown or phase bit. This is separate from the scheduler's saved
global tick and from the radar surface rebuild/dirty handling described in doc
03.

**Ordering and publication (Established).** Phase 12 runs once per runnable
sub-tick after the phase-11 strip sweep. The per-sub-tick transport/resource
sharing and packet flush run after phase 12. Nanolathe cleanup/result handling
and snapshot publication follow that sharing block, so a committed snapshot
observes the post-phase-12 blink bit. The executor's three-step tail — the
three empty barriers, the message-ring retire and the temporary-sight expiry
pass ([R-PLAT-02 §7]) — runs only after all
runnable sub-ticks and cannot interpose a pre-flip publication. The host-frame
renderer samples the committed presentation state; it does not own or advance
the cadence.

**Boundary probes (Established).** “Before” and “after” below refer to the
phase-12 invocation for the displayed global tick. Tick 0 is the post-battle-
entry state and has no phase-12 invocation yet.

| Global tick | Countdown before | Phase before | Countdown after | Phase after |
|---:|---:|---:|---:|---:|
| 0 (battle entry) | — | — | 7 | 0 |
| 1 | 7 | 0 | 6 | 0 |
| 7 | 1 | 0 | 0 | 0 |
| 8 | 0 | 0 | 7 | 1 |
| 9 | 7 | 1 | 6 | 1 |
| 15 | 1 | 1 | 0 | 1 |
| 16 | 0 | 1 | 7 | 0 |

The vectors make the strict comparison and the reset-before-toggle ordering
observable: ticks 8 and 16 toggle, while ticks 7 and 15 only reach zero.

### The post-loop list is the temporary-sight list; the stub census; the control byte; the start barrier [R-PLAT-02 §5]

The evidence is the sub-tick executor's tail, the list's allocator, its single
producer and its expiry pass, the empty routines the executor and the pump
call, the control-byte sender, and the start-barrier routine's sole gate.

**The post-loop list (Established).** The list the post-loop pass compacts is
a temporary-sight observer list, not a missile or interceptor structure:

- The block is allocated at battle entry under the label `EYEBALL_MEMORY`
  as **20 records of 36 bytes** (720 bytes) and freed at battle exit; the
  count starts at 0.
- Each record is a **self-contained LOS observer** of [03 R-VIS-01 §2]: the
  owning player record, a pointer to the record's own inline coverage-tile
  pair, the sight distance as a signed 16-bit value, the height byte, a
  pointer to the record's own inline coverage byte, the world X, Y (raised to
  `(SeaLevel + 1) << 16` when lower) and Z, and an **expiry tick**. It is a
  temporary sight source that is not a unit — an "eyeball".
- The expiry callback is the throttled LOS refresh of [03 R-VIS-01 §2]
  itself, invoked with the record; what the refresh publishes or removes for
  an expiring record is doc 03's contract.
- **The producer is the central unit-death handler**: it appends when the
  victim is owned by the local slot, LOS mode bit 1 is set (`Circular` or
  `True`) and the count is below 20 (silently dropped at 20). The handler of
  a received network death packet *is* that same central death handler, and
  the local death path calls it directly after building the death record
  networking would send, so the list is populated in **every** session kind
  and the post-loop expiry pass does real work ([08 R-SESS-01 §3]); the pass
  itself is stated in [03 R-COMP-02 §2].

The pass itself, for completeness (Established): for every record whose
expiry is **strictly below** the global tick (unsigned), call the refresh
with the record; then, if any expired, find the first expired record and
copy every later record whose expiry is at or above the tick down over it
(re-pointing the record's two self-pointers), and set the count to the
survivors. The "any expired" flag is an uninitialised local when nothing
expired, so the compaction may run with nothing to remove; it then changes
nothing.

**The stub census (Established).** These routines exist in the recovered
image, are called on the paths named, and do nothing:

| Caller | Stubs |
|---|---|
| process entry, after the audio device | one empty routine |
| process entry, before the timebase | one routine returning 0 |
| sub-tick executor, after the loop | the "three empty barrier functions" of §4.4 — three empty routines, before the message-ring retire |
| battle host pump, networked branch (§4.3 / [R-PLAT-01 §1] step 3) | three routines returning 1, then the start barrier, then three empty routines |
| display teardown, first call | one empty routine |

None reads or writes any word; a re-implementation omits them.

**The control keepalive byte (Established; the value [R-PLAT-01 §1] left
unnamed).** The one-byte control packet is **type 6**. The sender writes the
byte into the engine's send buffer and, with a zero target, hands it to the
broadcast helper (which returns without sending in single player —
[R-PLAT-01 §3]); with a nonzero target, to the direct helper for that peer.
Besides the paused zero-budget path, the loading state's frame sends it once
per frame to every local player while the loading thread runs and then
drains the receive path; both are no-ops off the network.

**The start barrier is multiplayer-only (Established).** Bit 2 of the pump's
state word, which gates the start-position assignment/synchronisation routine
in both the pump and the loading state's frame, is set at exactly one site:
the battle-entry orchestrator, only when the session kind is 3 (network).
The routine — which shuffles start slots with CRT draws on the main thread
and exchanges packets — therefore never runs in campaign or skirmish and is
out of scope ([08 R-OOS-01]); its main-thread CRT draws do not enter the
single-player stream census of [R-PLAT-01 §7].

### The sub-tick executor's tail runs unconditionally, including on a zero-runnable pump [R-PLAT-02 §7]

**Established.** The post-loop tail is not inside the loop and is not guarded
by the runnable count: a pump that advances zero sub-ticks — a paused session,
a budget that rounded to nothing, a frame that arrived early — still falls
through the loop and runs the same tail.

The tail, in order, is:

1. the **three empty barrier routines** of [R-PLAT-02 §5]'s stub census — they
   read and write nothing, and a re-implementation omits them;
2. the **text-scroll retire** — the in-battle message ring of 30 entries
   (doc 07's object; [R-PLAT-02 §8]). At most one entry is retired per call:
   the entry at the display index is retired when the tick it was posted, plus
   `(the text-scroll interface option + 1) × 30` ticks, is below the current
   tick. That option is `TXTSCROL`, in seconds, default 10
   ([07 R-CAM-01 §7]), so the term is that many seconds converted to ticks.
   This is presentation state;
3. the **temporary-sight expiry pass** over the "eyeball" observer list of
   [R-PLAT-02 §5]: every record whose expiry tick is **strictly below** the
   global tick (unsigned compare) invokes the throttled LOS refresh, followed
   by the in-place compaction that section describes.

**What a zero-runnable pump changes, exactly.** Nothing in simulation state.
The global tick is not incremented — only the loop body's own increment, ahead
of phase 1, does that — so the expiry comparison is evaluated against the same
tick the
previous pump already compacted for, and it finds nothing new to expire. The
barrier routines are empty. The only thing that can still move is the
text-scroll retire, which may advance one entry per pump because its
condition is a function of the tick and not of the loop having run; that is
presentation, outside the simulation's committed state.

For an implementation the contract is therefore: run the tail on every pump,
runnable sub-ticks or none, and rely on the tail's own predicates rather than
on a "did we tick" flag. The zero-tick non-mutation property holds because
each step is separately inert, not because the tail is skipped.

### The post-loop ring is the in-battle message ring, and the tail has three steps [R-PLAT-02 §8]

**Established** by direct read of the executor tail, the retire, the poster,
the two index resets and the two drawers.

- **The ring is the in-battle message ring** — the text-scroll ring doc 07
  owns ([07 R-CAM-01 §7] `textscroll`, [07 R-HUD-03 §14.3], [07 R-HUD-03
  §14.4]). Its 30 records of 72 bytes are each a 64-byte text line with a
  forced terminator, the **global tick at which the line was posted**, the
  source unit's id (16-bit), a silence byte (`'\n'` suppresses the
  `MessageArrived` cue) and a class nibble. Two 16-bit indices address it: a
  **producer index** the poster advances and a **display index** the retire
  advances, both modulo 30.
- **Advancing the ring and retiring a line are one routine.** The executor's
  tail is: the three empty barrier routines; the message-ring retire; the
  temporary-sight expiry pass. There is no fourth call. The retire is exactly
  the rule [R-PLAT-02 §7] states: when the ring is non-empty (producer ≠
  display) and `postTick + (textscroll + 1) × 30 < currentTick` (unsigned) for
  the line at the display index, the display index advances one slot, wrapping
  at 30; at most one line per call.
- **What indexes it.** The poster ([07 R-HUD-03 §14.4]'s producers — chat,
  order acknowledgements, elimination lines, cheat and save cues) writes at
  the producer index and stamps the current tick; the retire and the two
  drawers read from the display index toward the producer. Both indices are
  reset to zero by three routines: the skirmish/multiplayer battle-entry
  path, one further set-up routine, and the battle state's own exit path;
  the F12 key clears them in play ([07 R-CAM-01 §2]).
- **No network path touches it.** The send helper, the receive dispatch and
  the receive-frame window never read or write the ring or its indices
  (bounded negative over the recovered export).

The ring is presentation state, retired once per host pump whether or not a
sub-tick ran ([R-PLAT-02 §7]), and is never serialized; no simulation state
depends on it.

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
four-byte `rand` state in the historical Microsoft layout. Each block is seeded
from local/system time and time-zone conversion at one-second resolution:
the **main thread's** at process startup and never again, the **loading
thread's** by the battle-entry orchestrator running on it, so the two blocks
have independent histories (§7.2, [08 R-ENTRY-01 §10]). Each
thread obtains the block lazily with `TlsGetValue`; missing
state is allocated, initialized, and installed with `TlsSetValue`. There is no
worker *pool*; the thread census below finds a loading thread that runs the
whole battle-entry orchestrator and a cursor-redraw thread that always exists
([R-PLAT-01 §4]).

### The thread census: the loading thread and the cursor thread [R-PLAT-01 §4]

The evidence is every caller of the engine's thread starter and of the
C-runtime `_beginthread` beneath it.

**The thread starter.** `_beginthread(fn, stackSize, arg)`: the runtime
`calloc`s a fresh 116-byte per-thread block (so its `rand` state starts at
**1**, the runtime's documented initial seed, until something seeds it),
creates the thread suspended and resumes it. Three call sites exist in the
recovered image; one (a second cursor-thread starter) has no caller.

**1. The loading thread (Established).** The front-end state machine's
loading state starts a thread (default stack) whose body is a structured-
exception frame around the **battle-entry orchestrator** — the orchestrator's
only caller. Failure to create it raises the fatal modal
`Unable to start the loading thread!` ([08 R-ENTRY-01 §1] owns the state
machine). Consequences:

- The orchestrator's CRT `srand` writes the **loading thread's** TLS block —
  a block that did not exist before the thread started. The **main thread's**
  CRT block is seeded exactly once, at process startup, and is never reseeded.
  Every CRT draw the tick makes (wind interval, meteor scheduler, victory
  timer, camera shake, strips, sound variants, the elimination line) runs on
  the main thread — the battle host pump is the main thread's mode frame
  function — and therefore **continues the front-end's stream**, including
  every menu, briefing and sound-variant draw made since process start. The
  worker's draws (the skirmish slot shuffle, the explosion-frame builder) come
  from the freshly seeded thread block and die with the thread. The
  per-thread consumer census is [R-PLAT-01 §7]; doc 08's statement of the same
  fact is [08 R-ENTRY-01 §2].
- The simulation stream is a process global, so its battle-entry seed is
  thread-independent; nothing above changes §7.1.
- The main thread keeps pumping messages while the loading thread runs; the
  loading state's frame function waits for the orchestrator's completion flag
  ([08 R-ENTRY-01 §1]). No gameplay phase runs on the loading thread.

**2. The cursor thread (Established).** The window creator copies bit 9 of
the display flags word into the display object's cursor-thread bit; startup
always sets bit 9 ([R-PLAT-01 §1]), so retail always creates this thread
(stack 0x8000 bytes, `THREAD_PRIORITY_HIGHEST`) together with the twenty-record
button ring and three save-under surfaces of 0x640 bytes. Its loop: acquire
the display lock (exchange the owner marker with the four-byte value
"MOUS", event wait on contention), redraw the cursor when the "cursor
dirty/visible" word is set (a `GetCursorPos` read and blit), release, then
sleep until 33 ms after the iteration started (minimum 1 ms) — a
presentation-only 30 Hz cursor updater. Shutdown sets its stop word and polls
up to 21 × 100 ms for the thread to acknowledge before freeing the surfaces
and the ring. It touches no game state and draws no random numbers.

**3. The diagnostic helper thread** — never created in normal startup (§5.1);
its body sets `THREAD_PRIORITY_ABOVE_NORMAL` while it opens the
`-memorystatus` / `-performancestatus` dialogs (each only when its switch is
present), restores the previous priority, then runs a `GetMessage` loop with
a dialog filter.

**Bounded negative.** No other `CreateThread`/`_beginthread` caller exists in
the recovered game code; the remaining creators are library (Smacker, the
runtime).

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
take human-readable subsystem labels, and callers initialize the blocks they
receive; the label is dropped before the allocation and the allocator itself
**does not** zero or pattern-fill unless the `-memset` diagnostic switch is
present ([R-PLAT-01 §5]). Vectors are sometimes grown by reallocation.
Immediate slot reuse and linear scan order are observable and therefore
deterministic.

### 6.1 Established fixed pools

The evidence for the projectile rows is a read of all projectile count
writers, allocation sequences, the projectile-phase loop, and the compaction
routine: allocation is append and compaction is stable at the tail. The
following contracts are Established.

| Pool | Capacity/record contract | Allocation and retirement |
| --- | --- | --- |
| Unit instances | 280-byte records; capacity is a game value derived from setup multiplied by ten plus one, yielding roughly two thousand to five thousand stock slots rather than the 500 folklore; the pool is sliced per player by sorted player order, each slice holding as many records as there are definition types, with slot zero reserved as null; allocation scans the owning player's slice for the lowest free flag and reuses it immediately, and an alive mask marks a live slot; per-definition limits are enforced by a flag and a value of minus one meaning unlimited, counted by scanning the slice; the canonical allocator is the sole allocation site for every creation path and the reconstructor validates a forced slot against slice bounds and occupancy, with every limit, slice-full, out-of-bounds, or occupied case returning a null handle and consuming no RNG; freeing clears alive masks, heaps, order queues, and attachments but retains the stored slot index; saving uses forced-slot reconstruction and a stale 16-bit packet that validates only slot nonzero and alive, so it aliases a reused occupant silently |
| Projectiles | Exactly 300 records, 107 bytes each | Allocation appends at the active-span tail. Retirement sets a dead flag without changing the count. Stable compaction runs at the projectile-phase tail every sub-tick (reading the current post-append count), and again immediately after the unit-owner projectile purge when a unit dies; it removes dead records, preserves survivor order, and repairs the affected projectile and follow-camera links. The post-loop pass is a different structure (see §6.2). |
| Feature definitions | Each type has a 128-byte copy; type table records use a 256-byte stride | Preallocated at map/catalog load; type IDs are stable for the loaded catalog. |
| Live features | A 48-byte live record plus a 13-byte plot cell per map attribute cell | Plot cells point to feature anchors; removal returns the cell to the free sentinel and releases the live record. Map-row order is deterministic. |
| COB threads | Eight 164-byte thread records per unit | Lowest clear thread-mask bit is selected. Ending/sleeping a thread clears its active bit; the scan is fixed order. |
| Construction nodes | A 86-byte node; factories use separate tail/head links selected by a flag | Nodes append to a per-factory chain, coalesce matching build types where applicable, and are freed on cancellation/completion. |
| Effect/sequence strips | Variable vectors of segment records drawn from one process-lifetime pool of **1000 slots × 76 bytes**, built by a static constructor and never grown | Append in event order; a compaction/drain pass moves/removes old entries, and each strip evicts its oldest object when its pre-insert count exceeds 400. Every producer call site is enumerated by strip literal in doc 03 [R-FX-02 §5] (strips 0, 1, 3 and 8 have none). |

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
- The post-loop ring is the in-battle **message ring**: a 30-entry circular
  window of 72-byte records, each a 64-byte text line plus its post tick,
  source unit, silence byte and class nibble. After the tick body, while the
  head entry's deadline — its post tick plus `(textscroll + 1) × 30` ticks,
  `textscroll` being the interface option in seconds — has passed, the head
  advances one slot with wraparound.
  It is presentation state, no network path touches it, and it is not a
  generic timer queue ([R-PLAT-02 §8]).
- A separate post-loop pass compacts the temporary-sight observer list of
  [R-PLAT-02 §5] — 36-byte records with expiry fields — invoking each expired
  record's expiry callback and removing it in place; its producer is the
  central unit-death handler, reached directly in single player
  ([08 R-SESS-01 §3]). It is distinct from the projectile-pool compactor of
  §6.1.
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
the exact user-facing action is not uniform. General heap failure is the
allocation-failure hook of [R-PLAT-01 §5]: an `ErrorLog.txt` line, a modal,
and process termination. Vector growth failure and archive decompression
failure are not exhaustively traced.

### The tagged allocator, its fill policy, and its failure path [R-PLAT-01 §5]

The evidence is the two allocation wrappers, the shared body, the C-runtime
`malloc` beneath it, and the allocation-failure hook.

**One allocator.** The labelled wrapper (`alloc(label, size)`) and the plain
wrapper (`alloc(size)`) both reach the same body; the label is **discarded**
before the body runs (it is not stored in or beside the block). The body:

1. enters a process-wide critical section (lazily initialised);
2. when `-memfussy` is present, takes the tracked path: page-aligned
   `VirtualAlloc` (commit granularity 0x1000, read/write), front or back
   alignment, a fill pattern, and a record in a tracking table — diagnostics
   only, never the retail default;
3. otherwise calls the C-runtime `malloc`: the request is rounded up to a
   multiple of 16; requests at or below the runtime's small-block threshold go
   to the runtime's small-block heap under its own lock, larger ones to
   `HeapAlloc(crtHeap, 0, size)` — **no zero flag**, and the small-block heap
   does not clear either. The runtime heap is the backing store for large
   requests, but the tag never reaches it;
4. on success updates two byte/count statistics; on a null result calls the
   allocation-failure hook (if installed) and retries while the hook remains
   installed — in retail the hook never returns (below), so the retry loop is
   unreachable;
5. when `-memset` is present, fills the block with the `-memset=` value
   (default pattern `0xDEADBEEF`); **by default the block's contents are
   whatever the heap held**.

`free` mirrors it: tracked blocks go to the tracking table, others to the
runtime `free` after the statistics update. The runtime `calloc` (which does
zero) has no game caller — its users are the runtime and the Smacker library.

**Verdicts requested by other documents.** Doc 03's plot-memory question
(`PLOT_MEMORY`, 13 bytes per cell; [03 R-TERR-01]) and doc 04's script-state
pools (`Object States`, `Static Varibles`; [04 R-COB-04]) all go through the
labelled wrapper, so **the allocator does not zero them**. The plot loader's
own initialisation loop writes, per 13-byte cell: bytes 0–3 to zero, byte 7
to the loaded per-map value, the two-byte field at offset 8 to `0xFFFF` (the
"no feature" sentinel), and clears bits 0–1 of the flags byte at offset 12.
Bytes 4, 5, 6, 10 and 11, and bits 2–7 of byte 12, keep whatever the heap
held until a later writer sets them — so doc 03's "bytes 5 and 6 of the last
column and row" are **undefined heap contents in retail**, not zero. Doc 03
owns what that means for its sector-grid sweep and lava flood.

**The allocation-failure hook (Established).** Installed by the process entry
before the singleton test. On a null allocation it appends
`Out of memory!\r\nYour hard disk may be full\r\n` to `ErrorLog.txt` beside
the module ([R-PLAT-01 §8] owns the file), shows the same text in a modal
titled `Total Annihilation` (`MB_ICONHAND | MB_SYSTEMMODAL | MB_SETFOREGROUND`,
flags `0x41010`), raises `SIGABRT`, and — should the signal return — calls
`exit(3)`. Heap exhaustion therefore always terminates the process; no
fallback path exists.

### Input queue capacities and overflow [R-PLAT-01 §6]

The record formats and the producer-side refusal are [07 §2]'s, restated here
only for the capacities and the consumer side.

| Queue | Capacity | Producer full | Consumer empty |
|---|---|---|---|
| Key-token ring | 30 slots (installed at window creation), one reserved → 29 pending | refused, both indices unchanged | pop returns token 0 ("no input"); peek likewise |
| Button-record ring | 20 records of 24 bytes (installed with the cursor thread) | refused, both indices unchanged | pop copies the motion slot instead and returns 0 |

Both rings are drained by the main thread's housekeeping helper once per pump
iteration ([R-PLAT-01 §1]); the window procedure produces on the same thread
inside `DispatchMessageA`, so there is no cross-thread producer — the cursor
thread only reads the pointer position. Neither ring is saved. Overflow of the
**simulation** queue families (order chains, path requests, the network
window) is each owning document's contract and is unchanged by this closure.

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
unsigned remainder modulo the bound. The bound test is a **signed** 32-bit
compare: a bound below 2 — which includes every bound whose top bit is set —
returns zero **without advancing the state**. Callers pass computed bounds
such as `max − min` or a candidate count that can legitimately be 0 or 1, so
the no-advance property is load-bearing for every draw census in this
document (see [R-DET-01 §4]). The Lehmer step itself is computed in wrapping 32-bit
arithmetic as `s × 16807 − (s ÷ 127773) × 2147483647`, which is the Schrage
form and never leaves the signed range, so the "add the modulus when
nonpositive" branch is the only correction needed. Startup seeds this stream from the sum of the low and high parts
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
resolution. Battle entry seeds the loading thread's separate block from the
same time-of-day helper: the seed helper has exactly two call sites in the recovered image,
process startup and the battle-entry orchestrator. The battle-entry seed
writes the **loading thread's** block ([R-PLAT-01 §4]): battle entry runs on a
thread the loading state creates, so its `srand` seeds that thread's fresh
block, while the main thread's block — the one every tick-side draw reads —
keeps the state it has carried since process startup.
The stream supplies the meteor-shower draws, the
wind-change interval jitter, and UI/media variants, and is also consumed by
the camera-shake driver inside the tick (two draws per shake step while a
shake is active); it is not the simulation Park–Miller stream. The wind tick
consumes both streams for different outputs, making the separation
observable. The complete consumer census — every call site of the CRT draw in
the image, including the sound-variant picker, the elimination-message picker,
the victory-timer arm, the effect strips, the lightning renderer and the
battle-entry explosion frames ([06 R-WFX-01 §6]: drawn per battle on the
loading worker's block, not at process start) — is §7.6 [R-DET-01 §5].

**The widening sampler (Established).** Every CRT sample whose bound may
exceed 32,767 goes through one inlined sampler:

1. set the mask to `0x7FFF` and take **exactly one** draw, `rand() & 0x7FFF`,
   as the result;
2. while the mask is below the bound — and while the mask is not already all
   ones, which is a guard tested before each pass — shift the mask left 15 bits
   and OR `0x7FFF` into it, and shift the **result** left 15 bits and OR
   `0x7FFF` into it as well;
3. return the result modulo the bound, through an **unsigned** divide.

The widening loop ORs the same constant into the result that it ORs into the
mask; it never takes a second draw. So **one draw is consumed per call at every
bound**, and above 32,767 the sample's low fifteen bits are always all ones —
only its top bits vary with the stream. A bound of 1 still consumes its draw
and yields 0; a bound of 0 reaches the divide and faults.

Both of the sampler's two inline sites carry the identical shape (both are
Fisher–Yates shuffles whose bound is the growing prefix length, so the
widening loop is not reached on any stock array); the reading is confirmed at
instruction level, not from decompiler output alone.

### 7.3 Sampling, wind draws, and save implications

Placement draws X then Y from the global simulation stream. Wind consumption
spans both streams and is fully recovered:

- At briefing-screen entry the CRT stream draws
  `rand() % (maxWind − minWind + 1) + minWind` from the mission's parsed
  minimum/maximum bounds and then `rand() & 0x3F`. These are **front-end
  display state only** — the two values are stored in briefing-screen globals
  whose only readers are the briefing/front-end region itself, and no
  battle-side reader exists in the recovered image. The battle's actual
  initial wind is drawn by the wind-change routine at tick 1 (see below).
  The `& 0x3F` value is an **update countdown**, not a direction: the briefing
  screen's per-update routine decrements it, and when it reaches
  zero (`< 1`) it drifts the displayed speed by `−2 + rand() % 5` (−2..+2),
  clamps the result to `[minWind, maxWind]`, and re-arms the countdown with
  `rand() % 63` (0..62). Its only readers are the entry routine and that
  per-update routine; nothing converts it to an angle, and the briefing
  screen has **no wind heading at all** — the display is a speed alone. The
  entry routine draws exactly twice; it arms no interval deadline. Established
  (the cadence of the per-update routine — once per briefing-screen update
  call — is what a screen redraw is; its frame rate is not traced).
  **The remainder is signed, and a malformed range is not clamped
  (Established).** Both the entry draw and the per-update drift use a signed
  32-bit divide, not an unsigned one: the span
  `max − min + 1` is formed as a signed subtract and the draw is sign-extended
  before the divide, so the remainder takes the sign of the draw and is never
  negative. Nothing in either routine tests the bounds for sanity. A mission
  authoring `maxwindspeed` **below** `minwindspeed` therefore gives a negative
  span, and the entry value lands at or **above** `minWind` — it is the
  per-update clamp that then pulls the display down, because its low clamp runs
  first and its high clamp second, so the second wins and the speed sticks at
  `maxWind` from the first countdown expiry onward. The one exception is
  `maxwindspeed == minwindspeed − 1`, where the span is exactly zero and the
  divide faults the process. A reimplementation must not substitute an unsigned
  modulo (it would give a completely different value for a malformed range) and
  must decide its own policy for the zero span.
- In simulation, the wind change falls due when the global tick passes the
  wind deadline (a strict comparison); the next deadline advances by
  `((CRT draw * 10) / 0x8000 + 5) * 30` ticks — five through fourteen seconds
  quantized to 30-tick units, using 64-bit multiply/divide, drawn **before**
  the new speed and heading.
- When due, the new speed is a bounded simulation-stream draw
  `simRand(maxWind − minWind) + minWind` — and the §7.1 bound test applies, so
  a map whose `maxwindspeed − minwindspeed` is below 2 (an equal or inverted
  pair included) consumes **no** simulation draw and pins the speed at
  `minWind` ([05 R-PROD-01 §3]). The new heading is a simulation draw
  of `simRand(0x10000)` truncated to 16 bits, taken only when the speed is
  nonzero; the direction vector pair is computed as **−2 × the fixed-point
  trig** of the heading.
  Battle entry itself initializes the wind by zeroing the deadline and
  calling the wind-change routine — but with the global tick still zero the
  strict gate does not fire, so that call consumes **no draws**; the first
  wind chain runs inside the first sub-tick, after the executor has
  incremented the global tick.
- The published float ratio is `(float)speed / (float)5000` — the denominator
  is a fixed constant written once at battle entry — stored as a 32-bit float
  and clamped from above at exactly 1.0 (overflow stores the float bit
  pattern for 1.0).
- The second deadline gates the **meteor-shower strike
  scheduler** (see §4.4.1 [R-CORE-01] and doc 06 §6.5). Its draws are all CRT
  and in a fixed order: four scheduling draws
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

#### Battle RNG seeding and a chronological draw census [R-CORE-02]

**Seeding (Established).** At battle entry the orchestrator seeds the simulation
stream and the loading-thread CRT block before setup: simulation is seeded from
the `QueryPerformanceCounter` sample (low part plus high part, XOR the fixed
constant, forced odd), and the loading-thread CRT is seeded from the time-of-day
helper (local time with time-zone and daylight handling, one-second
resolution). The CRT seed helper has exactly two call sites — process startup
and battle entry — and the simulation seed setter exactly one (battle entry).

The simulation seed lands in the process global, so every simulation draw made
before battle entry is wiped and pre-battle consumption cannot influence
battle determinism. The CRT stream behaves differently: the `srand` at battle
entry writes the **loading thread's** TLS block ([R-PLAT-01 §4],
[08 R-ENTRY-01 §2]), while the main thread's CRT state is seeded once at
process start and every tick-side CRT draw continues it, so front-end CRT
consumption **does** shift the battle's wind-interval, meteor and
victory-timer draws. Only the worker-side draws (skirmish shuffle, explosion
frames) start from the battle-entry seed.

**Chronological draw census (process start through early battle ticks):**

| When | Stream | Draws | Consumer |
|---|---|---|---|
| Process startup | CRT (main thread) | 0 (seed only) | main-thread TLS state ← time-of-day helper — the only seed that block ever receives |
| Menu/front-end screens | CRT (main thread) | unbounded (variant paths) | UI/media random variants; not censused exhaustively; **these draws carry into the battle** ([R-PLAT-01 §4]) |
| Briefing-screen entry | CRT | 2 | wind display: speed `% (max−min+1) + min`, then the display-jitter countdown `& 0x3F` (not a direction; §7.3) — front-end display globals, no battle-side reader; each later briefing update whose countdown expires draws 2 more (speed drift `% 5`, countdown `% 63`) |
| Battle entry (loading thread) | both | 0 (reseeds only) | simulation ← QPC sum; **loading-thread** CRT ← time-of-day; global tick ← 0 |
| Battle entry, skirmish setup | CRT (loading thread) | count−1 (Fisher-Yates swap draws), plus one 50/50 gate draw when fewer than three qualifying players | player-slot assignment shuffle (skirmish start positions; skipped entirely when a saved game is being loaded); consumed from the worker's block, which dies with the thread |
| Battle entry, networked setup | sim | 2 per placed commander (one per axis of the start point) | commander start placement |
| Battle entry, campaign/mission setup | sim | For each successful common allocation: buildangle-bounded heading invocation (bound <2 returns zero without advancing), then one full-domain initialization draw; per-definition-limit/pool refusal returns before both draws (0) | common unit initializer; a successful mission allocation then has its initialized heading overwritten by the authored placement angle |
| Battle entry, wind initialization | — | 0 | the wind-change routine is called with a zeroed deadline while the global tick is still zero; the strict gate does not fire |
| First sub-tick (global tick 1) | CRT, then sim | 1 CRT (interval), then 1 sim (speed), then 1 sim (heading) only when speed ≠ 0 | phase 8 wind change, now due (deadline 0 < tick 1) |
| Sub-tick when a strike is due | CRT | 4 scheduling draws, + 2 per meteor hit (radius, then angle) | phase 9 meteor shower |
| Sub-tick with an active shake | CRT | 2 | phase 10 camera shake |
| Sub-tick with non-empty strips | object-internal | none at the dispatcher level | phase 11 strip sweep |

Front-end draws between startup and battle entry are real CRT consumption on
the main thread's block, which battle entry does **not** reseed; they advance
the state the tick-side CRT consumers (wind interval, meteor, victory timer)
will read ([R-PLAT-01 §4], [R-PLAT-01 §7]).

**Save/load (Established).** Loading re-enters the
battle-entry orchestrator, so the simulation stream and the loading thread's
CRT block are reseeded unconditionally **before** the saved state is read (the
main thread's CRT block is not; [R-PLAT-01 §4]). The CRT source is the same
time-of-day helper as at startup, not the C library `time()` directly, and the
reseed is a consequence of load re-running the battle-entry path rather than a
separate loader step. The first post-load
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

| Offset | Size | Field |
|---:|---:|---|
| 0x00 | 4 | scaled wall-clock anchor |
| 0x04 | 4 | pending ticks to run |
| 0x08 | 4 | last scaled delta |
| 0x0C | 4 | fractional carry (`float32`) |
| 0x10 | 4 | global simulation tick |
| 0x14 | 2 | requested speed |
| 0x16 | 2 | active speed |
| 0x18 | 2 | slew counter |
| 0x1A | 2 | pause/lag/pending bits |

RNG state (Park–Miller process-wide and CRT TLS state) is outside this block
and is absent from the bounded save-writer graph. Load re-enters the battle-entry
orchestrator, reseeding simulation and the loading-thread CRT block; the
main-thread CRT continues its current process history [R-CORE-02]
[R-PLAT-01 §7]. A resumed game therefore does not restore the pre-save random
sequence bit-identically. The meteor-scheduling state, by contrast, is saved
and restored in its own box. Replay formats are not covered by this save-box
contract.

### 7.4 A wall-clock leak into authoritative state

The evidence is the ground-mover trace of `[04 §8.1 R-MOV-01 §5]`.
Sections 7.1 and 7.2 establish the two deterministic streams and section 4.1
the tick counter. Those are not the whole determinism boundary: one
authoritative write is driven by a **wall-clock** counter instead.

**Established.** The engine keeps an animation counter formed as
`GetTickCount()` multiplied by a configured rate field and divided by 1000. It
is read all over the presentation layer, which is unremarkable. It is also read
inside the simulation's post-move terrain conform, in the hover-bob branch that
runs for every unit whose definition sets `canhover`: the counter's low five
bits select the phase of a per-corner cosine offset of at most two height
units, and the four perturbed corner heights are then averaged into the unit's
**integer height word** — authoritative state that document 04's medium-band
classifier, below-water half-speed branch and water damage all compare against
([04 §9.1], [04 §9.2], [04 §8.1 R-MOV-01 §5]). The counter is sampled once per
corner, four times per hovering unit per tick.

This is the only clock or non-tick input anywhere in the mover chain: a bounded
census of the mover tick, ground steering, speed update, position and occupancy
commit, movement-rate classifier, band classifier, post-move correction,
terrain conform and route-follower service finds no other, and no simulation-
or CRT-stream draw at all. It does not make retail's single-player behavior
non-reproducible in any way the two streams already bound — it makes a hovering
unit's committed height a function of elapsed real time.

**Established — the rate field.** The rate word has exactly one writer, the
boot-time timebase installer, which stores **30** into it once before any
battle exists; it is not a session option, a registry key or anything a battle
rewrites. The counter is therefore `floor(GetTickCount() · 30 / 1000)` — the
same 30-per-second wall-clock scale §4.1's budget uses, read raw with no start
offset and masked to its low five bits. The per-unit phase it is combined with
is the allocator's full-domain simulation draw, stored once at unit creation
([04 R-MOV-01 §5c]).

**Established — the perturbation does cross a threshold.** Two of the three
readers named above — the below-water half-speed branch and the water damage —
exempt `canhover` outright, so the exposure is the medium-band classifier
alone; but that classifier's band-2 test is an equality against sea level, and
10 of the 13 `canhover` definitions in this install author `waterline` 0,
which puts them exactly on it. The perturbed word therefore alternates those
units between bands 2 and 1 with elapsed real time, by exhaustive enumeration
in `[04 R-MOV-01 §5b]`. The leak is script-visible, not merely theoretical.

### 7.5 The per-phase random draw table [R-DET-01 §4]

**Established** by a whole-image census: every call site of the simulation
sampler (143 static sites in 59 functions) and of the CRT draw (66 sites in 29
functions) was read and placed in the phase order of §4.4. This is the one
table every lane's "draws N" claim is checked against; the sections cited in
the right-hand column own the surrounding arithmetic and are not restated
here. The cross-check against them is [R-DET-01 §6].

**Conventions.** `sim(b)` is the bounded simulation draw of §7.1 — remember
that `b < 2` (signed) returns 0 **without advancing**; `crt()` is one CRT
draw of §7.2 (0..32767). Draws are listed in execution order within a row.
"Per visit" means per handler invocation of the owning unit's order in the
phase-2 pump; a handler that returns before the draw consumes nothing.

#### Before the first tick

| When | Stream | Draws | Consumer and anchor |
|---|---|---|---|
| Battle entry, world rebuild (loading thread) | CRT (loading thread) | 391,606 — one draw per generated pixel, `trunc((crt()·10)/32768)`, over three strips (doc 06's count; the strips were not recounted here) | procedural explosion frames [06 R-WFX-01 §6]; the builder's only caller is the orchestrator on the loading thread, whose block is discarded with the thread ([R-PLAT-01 §4]) |
| Front end | CRT (main thread) | unbounded | main-menu spark shimmer (`crt() mod 640` and companions), briefing wind display (2), sound variants, CD track choice — see [R-DET-01 §5]. **Not wiped**: the main-thread block is never reseeded ([R-PLAT-01 §4]) |
| Battle entry | sim; loading-thread CRT | reseed only | [R-CORE-02] |
| Battle entry, skirmish | CRT (loading thread) | `count − 1` swap draws, plus one 50/50 gate when fewer than three qualifying players | slot shuffle [08 R-SKIR-01 §2] |
| Battle entry, networked | sim | 2 per placed commander: `sim(mapWidth − 160)`, `sim(mapDepth − 160)` (world units; the result is offset by 80) | commander placement [R-CORE-02] |
| Every unit allocation (mission spawn, factory completion, builder completion, AI, respawn) | sim | `sim(buildangle)` then `sim(65536)` — the first is skipped without advancing when `buildangle < 2` | common unit initializer [04 §2] |
| AI player setup | sim | eight, in order: `sim(10)`, `sim(3)`, then two draws bounded by the two region widths just formed from those results, then `sim(20)`, `sim(3)`, then two draws bounded by the second pair of widths | strategic-state constructor [08 R-AI-01] |
| Battle entry, wind init | — | 0 | the gate does not fire at tick 0 [R-CORE-02] |

#### Inside the sub-tick, by phase

| Phase | Stream | Draws | Consumer and anchor |
|---|---|---|---|
| 1 network drain | sim | `sim(sparkTicks ÷ 2)` per fire-start packet applied (multiplayer only; the same ignition routine is reached in single player from the impact path, phase 3) | feature ignition [05 R-FEAT-01 §9] |
| 2 unit sweep — general update, damage reaction | sim | `sim(300)` once per damage event on an armed or kamikaze unit with a live target | [04 R-SPEC-01 §1, §9] |
| 2 — weapon update, acquisition | sim | `sim(candidateCount)` for the swap-remove sampler (capped at 50 candidates), then `sim(scoreA + scoreB)` for the two-bucket choice | ordinary acquisition [06 §3.2] |
| 2 — weapon update, turret executor at fire time | sim | `sim(spread)`, `sim(spread)` — only when the spread width is non-zero; a width of exactly 1 passes the test and draws nothing | [06 R-WPN-03 §4] |
| 2 — weapon update, line-of-sight executor | — | 0 | [06 R-WPN-03 §2] |
| 2 — COB drain | sim | `random` opcode: `sim(high − low + 1)`; `explode` opcode: `sim(3000)`×3, `sim(40)`, `sim(10)`, `sim(40)` unless the bitmap-only flag is set | [04 R-COB-01 §2] |
| 2 — primary and secondary order pumps | sim | `sim(15)` once per pump visit that lands on disposition case 3 (the wait dispositions) | [04 R-P0-01] |
| 2 — order handlers (ground) | sim | `Wait` `sim(30)` (+150 ticks); `AttackUType` `sim(90)` then `sim(x ÷ 2)`; `SelfDestruct` `sim(15)` when the countdown reaches zero; `Patrol` `sim(30)`; `Suppress` `sim(d ÷ 3)`; `RepairUnit` `sim(30)` (+30) in the out-of-range retry; `Follow_Ground` `sim(65536)`; `Reclaim`/`Resurrect` approach `sim(featureHeight)`; `RepairPatrol` — **unit scan/pick**: one ordered candidate gather and one bounded `sim(count)` pick, followed only when the handler reaches feature pairing by three energy-list draws and then three metal-list draws (each triple conditional on a nonempty sampled list) | [04 R-ORD-01 §2–§5], [05 R-WORK-01 §8], [R-DET-01 §6] |
| 2 — order handlers (air) | sim | `VTOL_Standby` `sim(30)`, then `sim(65536)` bearing and `sim(32)` radius (+8), then `sim(15)` (+30) delay; `VTOL_SeekAttack`/`VTOL_SeekGuard`/`VTOL_Follow` `sim(65536)` bearing, `sim(count)` for the damaged-retreat pad, `sim(8192)` orbit angle only when the interrupt bits are set, `sim(30)` deadline; `VTOL_Patrol` `sim(count)`; `AirStrike`-family case bodies `sim(16384)`; `VTOL_Evade` `sim(2)`; further air case bodies `sim(128)`, `sim(2)`, `sim(30)` | [04 R-AIR-01 §7, §8], [04 §10.3] |
| 2 — movement integration, route follower, path search | — | 0 | [04 R-MOV-01], [04 R-PATH-01 §11] |
| 2 — death handling | CRT | 1 when the victim's owner's live-unit count reaches zero: skirmish `crt() mod 3`, multiplayer `crt() & 7` (announcement line choice; presentation text, but the draw is inside the tick) | [08 R-CAMP-01 §9], [R-DET-01 §6] |
| 3 projectiles — burst clone spawn | sim | `sim(randomdecay)` when non-zero, then `sim(sprayangle)` when non-zero, per clone allocated | [06 §4.3] |
| 3 — impact on a flammable feature | sim | `sim(sparkTicks ÷ 2)` per ignition (countdown = draw + half) | [05 R-FEAT-01 §9] |
| 3 — meteor motion | — | 0 | [06 §6.5] |
| 4 effects — shatter | sim | per fragment created: `sim(160)`×3, `sim(1600)`×3, `sim(200)`×2 (eight) | [04 R-COB-04 §3] |
| 5 per-player — target-registry cadence gate | sim | `sim(30)` once per side every 30 ticks (zero also runs the strategic refresh) | [06 §3.1] |
| 5 — victory timer arm | CRT | `9000 + (crt()·9000) ÷ 32768` ticks, once, when the timer is unarmed | [08 R-TRIG-01 §6] |
| 5 — commander respawn (commander-death rule 2) | sim | per trial `sim(W − 2·(W ÷ 10))`, `sim(D − 2·(D ÷ 10))`, up to 9999 trials | [08 R-SKIR-01 §3] |
| 5 — AI tasks | sim | build roulette `sim(cumulative)` per positive-score candidate; extractor selector `sim(255)`; scatter per trial `sim(radius)`, `sim(65536)`, then the two map-fraction draws; construction repositioning `sim(65536)` per branch taken; explore `sim(900)` then `sim(2)` and the leg/map-edge draws; rally `sim(150)`, `sim(10)`, `sim(65536)`, `sim(incumbentScore)`, `sim(challengerScore)`; eco toggle `sim(5)` per candidate maker; the AI's own order dispatch reaches the acquisition draws of phase 2 | [08 R-AI-01 §2–§8], [05 R-PROD-01 §5, §6] |
| 6 features | sim | reproduction: `sim(100)` gate, then `sim(b)`, `sim(b)` with `b` the definition's reproduction-range byte; burn spread: `sim(100)` per neighbouring cell tested against the flammability byte (strict `<`) | [05 R-FEAT-01 §10–§13] |
| 6 features | CRT | 3 per burning-feature smoke emission, in order: horizontal jitter, vertical jitter, then the puff's last frame inside the producer (the producer's constructor draw is taken at the phase-6 call, not in phase 11) | [05 R-FEAT-01 §10, §16] |
| 7 sequences | — | 0 | [R-CORE-01] |
| 8 wind, when due | CRT, then sim | `crt()` (interval), `sim(max − min)` (0 without advance when `max − min < 2`), `sim(65536)` only when the rolled speed is non-zero | [05 R-PROD-01 §3] |
| 9 meteor, when due | CRT | 4, then 2 per hit | [R-CORE-01], [06 §6.5] |
| 10 camera shake, while active | CRT | 2 | [R-CORE-01] |
| 11 effect strips | CRT | per live object per tick: segment layers 1; particle emitters 5 objects × 6; smoke sprinkles 3 or 1; the nanolathe and beam families 1 | [03 R-STRIP-01 §1–§3] |
| 12 cadence flip | — | 0 | |

**What is sim-visible (Established).** Every simulation-stream draw above
writes authoritative state; every CRT draw above writes presentation state
or a deadline that only presentation reads, with **three exceptions that are
authoritative and CRT-fed**: the wind interval (phase 8), the meteor
scheduler and its projectiles (phase 9), and the skirmish victory-timer arm
(phase 5). A clone that reproduces the simulation stream exactly but not the
CRT stream reproduces every unit, order, projectile, feature and economy
outcome except wind timing, meteor strikes and the victory-timer instant.

### 7.6 The CRT stream's other consumers, exhaustively [R-DET-01 §5]

**Established.** The CRT seed helper has exactly two callers (process
startup and battle entry, [R-CORE-02]). The CRT draw has 29 calling
functions; the ones not already placed in the tick table above are:

| Consumer | Draws | Note |
|---|---|---|
| procedural explosion frames | one per generated pixel, on the loading thread at battle entry | [06 R-WFX-01 §6]; the block is discarded with the thread ([R-PLAT-01 §4]) |
| main-menu spark shimmer | several per spark per frame (`crt() mod 640` position, lifetime, drift) | front end only |
| briefing wind display | 2 | [R-CORE-02] |
| sound-variant picker | `(crt() · variantCount) ÷ 32768` per play, gated by the sound-category record's variant count and the options flags | doc 03 §8 / doc 02 sound category |
| CD audio track choice | 2 sites | random track mode |
| minimap/radar preparation | 1 | presentation |
| fire-effect spawn from debris | 4 (three −1..+1 position jitters and one more) | [04 R-COB-04 §2] |
| lightning renderer | per rendered frame, two passes, three draws per point: `(crt()·11) ÷ 32768 − 5` on each axis | [06 R-WFX-01 §6]; 6 per point per frame, matching the lane |
| multiplayer host lobby shuffle | as the skirmish shuffle | out of scope |
| a packet-path gate `(crt()·101) ÷ 32768 ≤ setting` | 1 | its callers are dead or multiplayer-only; out of scope |
| a second meteor-scheduler body without the per-hit loop | 4 | unreferenced — dead code |

**Established — the statement of what is sim-visible.** The CRT stream is
per-thread state seeded from wall-clock time; nothing in the save box
restores it ([R-CORE-02]), and the block the tick reads is the main thread's,
seeded at process start and advanced by every front-end draw since
([R-PLAT-01 §4], [R-PLAT-01 §7]). Its only authoritative consumers are the three
named in §7.5. Everything else it feeds is presentation, front end, audio,
or a message-string choice. Nanolathe therefore needs a CRT-compatible
stream only for those three consumers' *positions in the tick*, not for the
front end.

### Which thread's CRT block each consumer reads [R-PLAT-01 §7]

**Established** by walking each of the 29 CRT-draw callers of §7.6 up to its
thread root.

| Thread | Consumers | Seed history of the block they read |
|---|---|---|
| Main thread (pump → mode frame function → battle host pump → tick, host frame, composer) | camera shake, feature fire effects, meteor scheduler, victory-timer arm, wind interval, the eleven strip families, the sound-variant picker, the elimination-line pickers (skirmish `mod 3`, multiplayer `& 7`), fire-effect spawn, lightning renderer, minimap preparation, the spark shimmer, briefing wind display, CD track choice | seeded **once** at process startup from the time-of-day helper; never reseeded; advanced by every front-end and battle draw in program order |
| Loading thread (battle-entry orchestrator) | the skirmish slot shuffle and its gate draw; the explosion-frame builder (391,606) | fresh block (`rand` state 1 at thread start) seeded by the orchestrator's `srand` from the time-of-day helper; discarded with the thread |
| Cursor thread, helper thread | none | — |

**Consequence for Nanolathe.** A CRT-compatible stream for the three
authoritative consumers of §7.5 must be seeded once at process start and
advanced by every main-thread front-end draw to match retail bit-for-bit;
since that history is wall-clock-seeded and unbounded, exact retail parity of
wind timing, meteor strikes and the victory-timer instant is unattainable in
any case, and the useful contract is the *position* of each draw in the tick,
as §7.6 already concluded. The worker-side draws need a separate stream
seeded at battle entry.

**The elimination announcement (Established).** The skirmish
elimination line is chosen by `crt() mod 3` inside a helper reached from the
unit-death handler (phase 2, slot-end death handling) when the victim's owner
has no live units left; the helper formats `"%s %s"` from the translated line
and posts a class-4 status message with the player's colour byte. It is a
main-thread CRT draw inside the tick ([R-DET-01 §4], [08 R-CAMP-01 §9]).

### 7.7 Lane draw claims checked against the census [R-DET-01 §6]

**Established.** The census of §7.5 was compared, draw expression by draw
expression and in order, against the sections that own each consumer. These
agree with it: [04 R-COB-01 §2] (random/explode), [04 R-COB-04 §3] (eight per
fragment), [04 R-PATH-01 §11] and [04 R-MOV-01] (none), [04 R-AIR-01 §7, §8]
including the conditional `8192` orbit draw, [05 R-PROD-01 §3, §8] (wind),
[05 R-FEAT-01 §9, §11] (ignition bound `sparkTicks ÷ 2`, burn `sim(100)`),
[06 §3.1] (gate 30), [06 §3.2], [06 §4.3] (randomdecay before sprayangle),
[06 R-WPN-03 §4], [06 R-WFX-01 §6] (lightning 6 per point per frame; the
battle-entry explosion-frame site and its per-pixel rule), [08 R-AI-01] (constructor order 10, 3, widths, 20,
3, widths; task bounds), [08 R-SKIR-01 §2, §3], [08 R-TRIG-01 §6],
[08 R-CAMP-01 §9] (the skirmish `mod 3` draw is inside the tick on the death
path).

Where a lane's own wording disagreed with the census, the census row of §7.5
is this document's statement: the `RepairPatrol` gather is one ordered
candidate gather and one bounded pick, with three energy-list and three
metal-list draws only once the handler reaches feature pairing; and the
multiplayer elimination path draws `crt() & 7` from an eight-entry table that
is live, not dead data.

## 8. x87 floating point and integer conversion

The executable uses x87 arithmetic; no SSE simulation path is established.
The default control word is the Microsoft/CRT 53-bit precision, round-to-nearest,
masked-exception environment. The FPU setup helper always runs at startup;
with `-fpufussy` absent it re-masks the invalid and zero-divide exceptions
(no change from the runtime default), with it present it unmasks them; the
switch changes the helper's argument, not whether it runs ([R-PLAT-01 §2]).

Authoritative code mixes integer/fixed-point, 32-bit float, and 64-bit double:

- counters, flags, pool indices, and most map dimensions are integer;
- persistent world positions use signed 16.16 fixed-point words; selected
  resource totals and intermediate calculations use double as specified by
  their owning contracts ([04 §8.1], [05 "Two-stage settlement algorithm"]);
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
operations). The two values are not the same field and must not be read into
one.

### The float→int conversion census [R-DET-01 §1]

**Established** by a whole-image scan of every x87 integer-store instruction
and every call of the truncating helper.

**The helper, exactly.** The compiler's truncating helper saves the control
word, ORs the round-control field to *toward zero* (both bits set), loads the
modified word, performs one **64-bit signed** integer store, restores the
saved word, and returns the 64-bit result in the register pair. Every game
caller keeps only the low 32 bits (a plain `int` cast), and several keep less:
the weapon-definition time fields are stored as 16-bit words, the metal
seeding byte and the palette bytes as 8 bits. Consequences an implementer
must reproduce: the conversion truncates toward zero for both signs; a
magnitude between 2³¹ and 2⁶³ does **not** saturate — it wraps through the low
32 bits; a magnitude at or beyond 2⁶³ (or a NaN) produces the x87 indefinite
integer, whose low 32 bits are zero. No caller range-checks first.

**Site census.** 283 static call sites in 120 functions. The authoritative
ones — reached from the tick, from battle entry, or from a definition parser
whose output the tick reads — are listed by subsystem; each row names the
converted quantity as far as the lane's section states it, and the lane
anchor that owns the arithmetic. Presentation-only clusters are summarised.

| Subsystem | Sites | Converted quantity (all truncate; width in parentheses when narrower than 32 bits) | Owning anchor |
|---|---:|---|---|
| Economy: settlement pass | 1 | the per-tick settlement term on the deadline-due branch | [05 R-ECO-01 §2], [05 R-PROD-01 §2] |
| Construction step | 2 | the two truncated energy/metal terms | [05 R-WORK-01 §1] |
| Repair step | 2 | the two truncated terms, then the ≥1 clamp | [05 R-WORK-01 §3] |
| Work-amount seed | 1 | `max(1, trunc(...))` | [04 R-ORD-01 §10], [05 R-WORK-01 §4] |
| MobileBuild, HelpBuild, VTOL work fragments | 3 | a build-time-derived count (`buildtime ÷ 30`) and two work terms | [05 R-WORK-01] |
| Reclaim / Resurrect approach | 2 | the feature-height term | [05 R-WORK-01 §5, §7] |
| Capture | 1 | capture timer `((kills ÷ 5 + 10) · t · 10) ÷ 100` | [05 "Capture"], [06 R-DMG-01 §2] |
| Order handlers, ground: Attack_Chase, MobileBuild, RepairUnit and its fragments, BuildingBuild, a target-distance helper | 16 | the **two-argument distance** (`hypot`) of the unit→target world offset for the leash and reach tests, and of the two footprint pairs | [04 R-ORD-01 §3, §5] |
| Order handlers, air: AirStrike, AirToGround, AirToGroundHover, VTOL_RepairUnit and their case bodies | 17 | `hypot` to target or goal; one lead term; one 16-bit field term | [04 R-AIR-01 §8], [04 R-ORD-01 §7] |
| Ground steering | 2 | `hypot` to the waypoint and to the goal | [04 R-MOV-01] |
| Flight integrator | 6 | the speed-scale terms (float × 2⁻¹⁶) and three position terms | [04 R-AIR-01 §1, §2] |
| Route-follower install | 2 | `hypot` for the terminal-cell and half-distance tests | [04 R-PATH-01 §8] |
| Group centroid | 2 | `sumX ÷ n`, `sumZ ÷ n` | [04 R-STANCE-01 §5] |
| Per-unit tick and weapon helpers | 3 | one float field when non-zero; one packet term; one weapon-update term — **value not named** | doc 04 §6, [06 R-DMG-01 §2] |
| Unit creation | 1 | value not named | [04 R-CB-01 §4] |
| Weapons: impact dispatch, area impact, impact helper | 4 | value not named (the impact arithmetic is [06 §9.2, §9.3]) | [06 §9.2], [06 §9.3] |
| Weapons: aim-cone test (45° = 0.7853981633974475 rad), projectile cruise waypoint, projectile and weapon-helper `hypot` sites, line-of-sight executor | 8 | `hypot` of the aim offset; the cruise waypoint height | [06 R-WPN-03 §2, §3] |
| Target-registry rebuild | 3 | centroid `sum ÷ n` on three axes | [06 §3.1] |
| AI: strategic constructor, score table, weight clamp (`weight · value` then clamp ≥ 0), candidate score, construction task, extractor placement, distance helper | 21 | score and region terms | [08 R-AI-01 §3, §8, §12] |
| Tick budget clamp | 1 | the string-to-double budget value | §4.2 |
| Battle entry: starting stock, metal seeding (8-bit per footprint cell), gravity install, meteor parameters (seconds × 30), mission spawner coordinates (`%f` fields) | 20 | as named | [05 R-ECO-01 §4], [05 R-FEAT-01 §7], [03 R-TERR-01 §1, §6], [R-CORE-01], doc 08 |
| Definition parsers (enumerated below) | 22 | as named | [06 R-WFX-01 §1], [06 R-DMG-01 §1], [05 R-PROD-01 §1], [05 R-FEAT-01 §1], [02 R-CONTENT-01, R-CONTENT-02], [fmt tdf] |
| Presentation and front end (HUD resource bar, renderer, model bounds, range rings, palette bytes (8-bit), audio tables (clamped to 255), option sliders, score and statistics screens, window placement, startup explosion frames) | ~120 | not authoritative | docs 03, 07, 08 |

**Bounded negative (Established).** No other float→int conversion mechanism
exists in game code: there is no `fisttp`, no 16-bit integer store, and no
inlined copy of the helper.

#### Definition parsers

Twenty-two of the sites are in definition parsers. Each ultimately uses the
signed-64 truncation helper, and several store narrower than 32 bits — a width
an implementation must reproduce, because the wrap is observable. The catalog
`Version` pair first passes each binary64 operand through the separate
toward-negative-infinity wrapper described in §3:

- weapon TDF: velocity, start velocity and acceleration are **32-bit**;
  `reloadtime`, `weapontimer`, `turnrate`, `burstrate`, `duration`,
  `randomdecay`, `smokedelay`, `flighttime`, `holdtime` and their companions
  are **16-bit** ([06 R-WFX-01 §1], [06 R-DMG-01 §1]);
  The velocity and acceleration readers multiply once by their stored binary64
  scale constants `65536/30` and `65536/900`, respectively; multiplying by
  65536 and then dividing is not the same floating-point operation order.
- FBI: one key ([05 R-PROD-01 §1]);
- feature TDF: `sparktime × 30`, **16-bit** ([05 R-FEAT-01 §1]);
- the catalog `Version` pair, `int(floor(v))` and
  `int(floor((v − int(floor(v))) · 10))`
  ([02 R-CONTENT-01, R-CONTENT-02]);
- the 3DO table, and the TDF float-getter's integer form ([fmt tdf]).

### The two round-to-nearest sites [R-DET-01 §2]

**Established.** Exactly two game routines store an x87 value to an integer
directly, under the default control word (round to nearest, ties to even),
into a **32-bit** slot:

1. **The bearing helper.** `bearing(a, b) = round(atan2(a, b) · 65536 ÷ 2π)`;
   the multiplier is a double constant (10430.378…), the arctangent is the
   x87 partial-arctangent at extended precision, the result is a 32-bit
   integer that callers mask to the 16-bit heading domain. Sixteen callers:
   the ground steering, the per-unit tick, the shared order-handler bearing,
   the turret and line-of-sight slot executors, the weapon impact dispatch,
   the projectile tick, four weapon-update helpers, the battle-entry unit
   placement, one multiplayer helper and one dead helper.
2. **The vector-rotate helper.** Given a 16-bit angle `a` and a pair of
   32-bit integers `(x, y)`: when `a = 0` nothing is written; otherwise
   `θ = a · 2π ÷ 65536` (double constant 9.5874e−05), `x' = round(x·cos θ −
   y·sin θ)`, `y' = round(x·sin θ + y·cos θ)`, both products formed at extended
   precision. Callers: the ground steering, the per-unit tick, the effect
   draw pass, and three dead helpers.

A third direct integer store lives in the C runtime's floating-point
exception raiser (it stores an out-of-range constant to raise *invalid*); it
is library code with no game caller.

### Control-word mutations reachable from the simulation [R-DET-01 §3]

**Established (bounded by a whole-image instruction scan: 53 control-word
load/store instructions).** The control word is written by:

* the truncating helper (save, set toward-zero, restore) — §8 above;
* the C-runtime `_control87`-style mask helper, whose ten callers are all
  library: the **two-argument distance helper** (`hypot`) sets precision to
  64-bit with all exceptions masked on entry and **restores the caller's word
  before returning**; its result passes through a 64-bit double memory slot
  before the return, so the caller — and the truncating helper after it —
  sees a double, not an 80-bit value; the binary64 floor wrapper used by the
  content catalog loader (`Version`) and the tick-budget clamp (save and
  restore inside each call); the printf-family float formatter; the runtime's
  own reset and math-error helpers;
* an audio-decoder reset pair around the WAV decoder;
* nothing else: one apparent site is a jump-table data word mis-decoded as an
  instruction, and the remainder lie in the unrecovered runtime region between
  the mask helper and the power/exponential family.

Non-default control words are therefore reachable from the simulation **only
transiently inside a runtime call**
(`hypot`, the binary64 floor wrapper), and every such call
restores the word before returning. No game routine changes precision or
rounding and leaves it changed; the default (53-bit, nearest) holds outside
those helpers, and the truncating helper remains the only rounding-mode change
at a game-visible integer store. The optional `-fpufussy`/`-fpunofussy`
switches of §8 are unaffected by this census.

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
front-end/game-mode state machine is
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

### The exception filter, `ErrorLog.txt`, and out-of-memory [R-PLAT-01 §8]

The evidence is the diagnostic initializer, the filter body, the symbol helper
and the allocation-failure hook.

**What is on by default (Established).** The filter is installed on the normal
path (the initializer's argument 8 leaves the filter bit clear) and the
symbolised stack walk is **enabled by default** (`-disableimagehlp` turns it
off); only `DebugHelper.dll` is switch-gated. No minidump is written —
`MiniDumpWriteDump` is not imported.

**The filter (Established).** `SetUnhandledExceptionFilter` installs a
trampoline to the report writer. The writer:

1. takes a re-entry guard (a second fault inside the writer returns at once);
2. runs the stack walker: when the imagehlp option is on it lazily loads
   `IMAGEHLP.DLL` and resolves `SymSetOptions`, `SymInitialize`,
   `SymCleanup`, `StackWalk`, `SymFunctionTableAccess`, `SymGetModuleBase`,
   `SymGetSymFromAddr`, `SymGetLineFromAddr` and `UnDecorateSymbolName`; the
   symbol search path is the executable's directory plus the `windir`
   environment value; a missing DLL or export degrades to an unsymbolised
   frame list;
3. opens `ErrorLog.txt` in the module's directory (`CreateFileA`,
   `GENERIC_WRITE`, `OPEN_ALWAYS`, then seek to end — the file accumulates
   across crashes);
4. maps the exception code through a 24-entry name table (the standard NT
   status names; anything else is `Unknown exception type`) and writes the
   header lines immediately — `%s caused an %s in\n` and
   `module %s at %04x:%08lx.\n` — so a fault during the rest of the report
   still leaves the module and address on disk;
5. formats into a buffer: `Exception handler called in %s. ` (the application
   name), the symbolised faulting line when available, the access-violation
   extras when the code is `0xC0000005` with two parameters (read/write
   attempt and the address, plus a read-only-page probe), the eight general
   registers as `Registers:` and four lines, `Bytes at CS:EIP:` followed by
   sixteen `%02x` bytes, the walker's frame text, the debug registers
   `Dr0`–`Dr7`, `ContextFlags`, and the x87 control/status/tag words;
6. normalises line endings to CR-LF, writes the whole buffer, closes the file,
   calls `SymCleanup`, and **returns `EXCEPTION_CONTINUE_SEARCH` (0)** — the
   process then dies through the operating system's default handler; the
   filter never resumes, never shows its own dialog, and never restores the
   display mode.

**Out of memory (Established).** The allocation-failure hook of
[R-PLAT-01 §5]: `Out of memory!\r\nYour hard disk may be full\r\n` appended to
the same `ErrorLog.txt`, a system-modal message box titled
`Total Annihilation`, `SIGABRT`, then `exit(3)`.

**Other writers of `ErrorLog.txt`.** An assertion writer (message plus a
blank line, then a breakpoint) exists but has **no caller** in the recovered
image. The generic fatal modal (message box then exit code 1 — [08 R-ENTRY-01
§1]) does not write the file.

### The developer console: `DebugBreak`, `debugdat` scripts, and the `~` key [R-PLAT-01 §9]

The console vocabulary itself is [07 R-CAM-01 §6] and the developer bit's
writers are [07 R-CAM-01 §9] (the registry `Games` value at settings load, and
the five-word `Now` phrase).

**`DebugBreak [1|2|3]`** — requires the developer bit **and** film mode:

- `1`: allocate 32 MiB (`0x2000000` bytes) through the plain wrapper in an
  endless loop; `2`: the same through the labelled wrapper with the tag
  `FORCE OUT OF MEMORY` — both end in the allocation-failure hook of
  [R-PLAT-01 §5] (log line, modal, abort);
- `3`: a **deliberate integer divide by zero** (the constant 1 divided by
  `1 >> 1`), which raises the integer-divide exception and exercises the
  filter of [R-PLAT-01 §8]; the `exit` call that follows it in the code is
  unreachable;
- any other argument (or none): when the display is full-screen, restore the
  desktop mode and sleep 500 ms, then `DebugBreak()`.

**Script fallback of the `+` command (Established).** When a `+<word>`
command matches no built-in and no unit-definition wildcard (the default
handler's spawn path, [07 R-CAM-01 §6]), the handler opens
`debugdat\<word>.txt` through the VFS (binary read), loads it whole (an
allocation tagged with the file's base name), and runs the text through the
line runner: each line up to `\n` — **including a final unterminated line**
— is tokenised and dispatched through the same `+` dispatcher with every
handler mask enabled, so a script can invoke handlers the console masks out;
the developer's spawn pointer words are saved before and restored after the
run. The tokeniser treats a trailing `\r` of a CR-LF line as whitespace and
strips it ([R-PLAT-02 §6]). No `debugdat` directory ships with the retail
install, so the path is inert in stock configurations.

**The `~` key.** With the developer bit set, the housekeeping helper pops the
`0x7E` token before the dispatcher sees it and restores the desktop display
mode ([R-PLAT-01 §1]); without the bit the token reaches the battle dispatcher
as one of the "label every unit" toggles ([07 R-CAM-01 §2]).

### The directive tokeniser and the screenshot writer [R-PLAT-02 §6]

The evidence is the tokeniser, its argument-substitution and reset helpers,
the line runner, the screenshot routine and the PCX encoder.

**The directive tokeniser (Established).** One tokeniser serves the developer console's `+`
commands ([07 R-CAM-01 §6]), the `debugdat` script runner ([R-PLAT-01 §9])
and the AI profile parser ([08 R-AI-01 §12]). Given a text span (or a
NUL-terminated string when no end is given):

1. reset the argument count to 0;
2. skip characters for which the C-runtime `isspace` is true — space, tab,
   **carriage return**, line feed, vertical tab and form feed — so a trailing
   `\r` of a CR-LF line is whitespace and never reaches a token: **the
   tokeniser strips it**;
3. stop at the end of the span or at `#` (comment to end of line);
4. record the argument's start when fewer than **20** arguments are
   registered (a 21st and later argument is copied into the text buffer but
   not registered — the count stays 20);
5. copy characters until whitespace, `#`, the end, or the **126-byte** text
   buffer is exhausted, then NUL-terminate; the next argument continues
   after the terminator.

**`%N` substitution (Established).** After tokenising, an
argument whose first character is `%` and whose remainder converts (`atoi`)
to an index `N` with `0 ≤ N < callerArgumentCount` is replaced by the
caller's argument `N` (a pointer copy into the callee's argument list); any
other `%` argument is left as written. The `debugdat` line runner applies it
with the invoking command's arguments, so a script line `+spawn %1` receives
the console command's first argument.

**The line runner (Established; refines [R-PLAT-01 §9]).** It splits on
`\n` only; the line passed to the tokeniser excludes the `\n`; a final
unterminated line is processed; the result is the OR of every line's
dispatcher result.

**The screenshot writer (Established; used by the `0xD6` hotkey of
[R-PLAT-01 §1] and the movie series of [07 R-CAM-01 §8]).** The routine
takes a directory and a name prefix (the hotkey passes the screenshot
directory and `FRAM`; the movie series its `MOVIE%03i` directory):

1. If the display's *frame-presented* word is clear (it is set by the
   present routine and cleared by the surface-restore routine), return 0
   without writing — nothing has been composed to capture.
2. Enumerate `<dir><sep><prefix>*.pcx` (`<sep>` is `\` when the directory
   is non-empty, otherwise empty); for every match convert the characters
   after the prefix with `atoi` and keep the maximum; the new file is
   `<dir><sep><prefix>%04i.pcx` with `maximum + 1` (so the first capture is
   `…0001.pcx`).
3. Convert the display's 256-entry palette (four bytes per entry) to 768
   RGB bytes, then encode the frame-buffer record (width, height, pixels)
   as PCX and write it through the VFS handle layer's loose-file open
   (mode `a+b`) and write helper — the helper refuses archive-backed handles
   and returns −1, which fails the size check below.
4. Any write whose byte count differs from the request closes the file and
   returns 0 (a partial file is left on disk); success returns 1.

**The PCX encoder (Established; the reader is [fmt pcx]).** Header of 128
bytes: manufacturer 10, version 5, encoding 1, 8 bits per pixel, `xmin 0`,
`ymin 0`, `xmax = width − 1`, `ymax = height − 1`, both DPI words 0, the
48-byte EGA palette field = the **first 48 bytes of the converted
palette**, reserved 0, planes 1, bytes per line = `width`, palette-info 0,
the rest zero. Each scanline is run-length encoded left to right: a run of
`n` equal bytes is emitted as chunks of at most 63 — each chunk `0xC0 | len`
followed by the value; a single byte below `0xC0` is emitted literally; a
single byte at or above `0xC0` (both top bits set) is emitted as a run of one
(`0xC1`, value). No row padding is emitted. After the last row: the
byte `0x0C` and the 768-byte palette.

## 10. Established facts, supported inference, and unresolved boundaries

### Established facts

- Win32 process/window/message-pump architecture with a named singleton;
  second-instance `OpenSemaphoreA` with an existing object returns immediately
  with `-1` and no window activation or handoff.
- 30-Hz scaled `GetTickCount` budget, carry, truncation, zero-to-five cap, and
  50-ms barrier waits that are not the tick driver; the ≥100 ms pump gate drives
  exactly one media-keepalive call while the budget itself is evaluated every
  busy pump iteration, and the networked zero-budget path sends a separate
  one-byte control keepalive every 60 scaled units.
- Single-player pause stalls the budget anchor (one capped burst on unpause);
  multiplayer pause discards the integer budget while keeping the fractional
  remainder (no burst). Movie capture and the screenshot hotkey reset the
  scaled-time anchor.
- The phase order and global-tick increment position listed above.
- Main-thread simulation; a loading thread runs battle entry and a cursor
  thread always exists, both presentation/setup-only; the conditional
  diagnostic helper thread is disabled by normal startup. The main thread's
  CRT block is seeded once at process startup and never reseeded; battle
  entry seeds the Park–Miller global and the loading thread's own CRT block
  ([R-PLAT-01 §4], §7).
- The pause packet is `{0x19, 0, newPauseBit}` sent after the local flip, the
  speed packet `{0x19, 1, speed}` from the 1..20 clamp; both are no-ops in
  single player ([R-PLAT-01 §3]).
- The allocator drops its label and never zero-fills by default; heap
  exhaustion logs to `ErrorLog.txt`, shows a system-modal box, and terminates
  ([R-PLAT-01 §5], §8). The crash filter writes `ErrorLog.txt` on the normal
  path and returns continue-search ([R-PLAT-01 §8]).
- Input rings: 30 key slots (29 usable), 20 button records; producer refusal
  when full, "nothing" on empty ([R-PLAT-01 §6]).
- Fixed unit/projectile/feature/COB/construction pools and documented queue
  capacities/order where the ledger is explicit.
- One global Park–Miller stream, one CRT TLS stream, x87 53-bit default, and
  truncating `__ftol` conversion. Battle entry seeds simulation and the separate
  loading-thread CRT; the main-thread CRT continues from process startup
  ([R-PLAT-01 §7]). Wind draws span simulation and main-thread CRT with the
  exact arithmetic recovered (interval jitter, bounded speed draw, 16-bit
  heading draw, −2 vector factor, ratio over the fixed denominator 5000
  clamped at exactly 1.0) while the meteor shower consumes only the CRT
  stream (§7.3 [R-CORE-02]).
- Registry/profile/legacy multimedia compatibility surface, loose-file-first
  lookup, and ordered archive-provider behavior with exact window styles
  (`WS_EX_APPWINDOW`, `WS_POPUP|WS_VISIBLE|WS_SYSMENU`, `CS_DBLCLKS`) and popup
  semantics.
- 28-byte `Players/GameTime` scheduler block is saved/restored with the field
  layout above; RNG state is not saved. Load reseeds simulation and the
  loading-thread CRT, while main-thread CRT history continues [R-CORE-02].
- Seventeen static initialisers run before the entry; the engine block
  floats at a wall-clock skew of `(GetTickCount mod 1000) × 7` bytes; one
  quit-request routine sets the quit bit and posts `WM_DESTROY`; the
  game-state teardown runs only on a requested quit ([R-PLAT-02 §1], §2).
- A ten-slot scaled-clock timer table is serviced once per busy pump
  iteration; only the CD-audio fades register in it ([R-PLAT-02 §4]).
- The post-loop expiry list is the 20 × 36-byte temporary-sight ("eyeball")
  observer list, fed by the central unit-death handler and therefore populated
  in every session kind; the control keepalive byte is type 6; the start
  barrier is network-only ([R-PLAT-02 §5]).
- The post-loop ring is the in-battle message ring doc 07 owns, retired by the
  `textscroll` rule; the executor's tail has three steps
  ([R-PLAT-02 §7], [R-PLAT-02 §8]).
- Peer state synchronization is push-and-overwrite: the quarter-second scanner
  pushes counters and resource totals, receivers copy every field with no
  comparison, threshold or abort, and the only gate is the echo-flood check,
  so divergence handling is notification (kick/chat) rather than repair or
  abort-on-mismatch (§9; the manual sync-error chat handler alone is inferred).
- The directive tokeniser splits on C `isspace` (so CR-LF is safe), keeps
  20 arguments in 126 bytes, and substitutes `%N`; screenshots are
  `<prefix>%04i.pcx` with the highest existing number plus one, RLE-encoded
  with 63-byte runs ([R-PLAT-02 §6]).

### Supported inference

- The MAIN ownership region is primarily a renderer/present lock, while the
  recursive owner-ID region protects archive decompression and related shared
  buffers.
- Active speed is a load regulator that can move away from a user request under
  sustained frame/tick pressure, then recover slowly.
- Multiplayer tick advancement is soft-paced by oldest remote progress rather
  than synchronized per tick by a barrier.
- Most fixed pools are intentionally chosen to make insertion/retirement order
  deterministic, not merely as performance optimizations.

### Confidence limits

- Function-boundary recovery is incomplete, so every absence claim in this
  document is bounded by the current import/decompile census rather than
  proved over every byte of the image.
- Network and replay coverage remains incomplete. No timer queue exists on the
  tick path; the post-loop message ring of [R-PLAT-02 §8] is presentation
  state and is not serialized; the network future-frame window, its overflow
  policy and any replay format are outside what this document establishes.
- The scaled-clock factor (30) and the default x87 control word `0x27F` occupy
  different fields; conflating them misreads both §4.1 and §8.

## Missing and unknown

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it. Everything this document establishes is
stated in the body, not here.

### Process and platform

- Symbolic OS meaning of the two `SystemParametersInfoA` actions `0x5E`/`0x5D`;
  the numeric call sequence and restore semantics are established · §2.2 ·
  static trace.
- Meaning of the window style and ex-style bits outside the established
  `0x90080000` / `0x00040000` / `CS_DBLCLKS` values and the client-area
  adjustment · §2.2 · static trace.
- Shutdown ordering after an exceptional failure: the crash filter returns
  continue-search without releasing anything ([R-PLAT-01 §8]), so the
  singleton semaphore, display mode and audio device are left to the operating
  system's process teardown. Whether the semaphore's kernel object outlives a
  crashed process long enough to block an immediate relaunch is an OS
  question, not an executable one · §2.3 · manual test on the reference
  install.
- What the throttled LOS refresh publishes or removes when the post-loop pass
  hands it an **expiring** temporary-sight record · §4.4 [R-PLAT-02 §5],
  doc 03 [R-VIS-01 §2] · static trace.
- Whether a writer of the display object's frame-presented word exists
  beyond the two found (present sets it, surface restore clears it) · §2.2
  [R-PLAT-02 §6] · static trace over the unrecovered regions.
- The cursor thread's redraw internals (what the 33 ms redraw blits, and the
  three save-under surfaces' roles) — presentation only · §5.1 [R-PLAT-01 §4],
  doc 07 · static trace.
- The purpose of the ten `-B` words that the parser compares and never acts on
  (`deathends` … `watching`); they are inert in retail and have no
  implementation impact · §3.1 [R-PLAT-01 §2] · none needed.
- TLS destructor and `DeleteCriticalSection` callsites: the thread and lock
  census is bounded by the recovered function window, and these sites fall
  outside it · §5.1, §5.2 · static trace over the unrecovered regions.
- Complete consumer sets for `VirtualProtect`, `VirtualQuery`, file mappings,
  device control, the console handler, environment, locale, and the module
  loader; each facility has exactly one recovered wrapper site, but its callers
  are not enumerated · §3.2 · static trace.

### Clock, network, and determinism

- Network future-frame overflow policy, retransmission wrap, and late-join
  resynchronization · §4.3 · static trace. Multiplayer-only; recorded so the
  spec stays exhaustive.
- Whether network transport state and any replay format are serialized; the
  RNG stream is established as not saved and the scheduler block as saved
  · §7.3 · static trace.
- Positions of the camera-shake magnitude and duration fields in doc 06's
  compiled weapon-record field map; the behavior, the authored keys
  `shakemagnitude` / `shakeduration` and the duration × 30 compile-time
  conversion are established · §4.4.1, doc 06 · static trace.
- Whether the briefing wind-display globals have any reader outside the
  front-end region; bounded absence in the recovered image only · §7.3 ·
  static trace over the unrecovered regions.
- Which battle-entry set-up routine, besides the skirmish/multiplayer entry
  path and the battle state's exit path, resets the in-battle message ring's
  two indices · §4.4 [R-PLAT-02 §8], doc 07 · static trace of the ring's
  index writers.
- Whether the networked-mode commander placement loop also runs when a saved
  networked game is loaded; the loop is not gated on the save box · §4.4 ·
  static trace. Multiplayer-only.
- The converted quantity at the truncation sites [R-DET-01 §1] marks as
  "value not named": a few order-handler, unit-creation and weapon-helper
  sites whose operand the owning document does not spell out; the site list
  itself is complete · §8, docs 04 and 06 · static trace of the x87 stack at
  each site.
- The exact per-object CRT draw count of the effect-strip objects and of the
  fire-effect spawn's fourth draw; doc 03 owns the object bodies, this census
  records the sites · §7.5, §7.6 [R-DET-01 §5], doc 03 · static trace.
- Whether the 391,606 battle-entry CRT draws of the procedural explosion
  frames ([06 R-WFX-01 §6]) are exact; the site and the one-draw-per-pixel
  rule are established, the three strips were not recounted · §7.5 · static
  recount of the strip parameter sets.

### Memory and queues

- The runtime's small-block threshold value (the size at or below which the
  C-runtime `malloc` serves from its small-block heap rather than
  `HeapAlloc`); it changes nothing observable because neither path fills
  · §6 [R-PLAT-01 §5] · read of the runtime's one-time threshold writer.
- Failure side effects of the specialized projectile allocators other than the
  meteor spawner, whose placement and silent-drop behavior are established
  · §6.3 · static trace.
- Per-strip ownership registration for effect strips outside the nanolathe,
  beam, and smoke families, and the mission-object, path-debt, and audio-node
  layouts · §6.1, doc 03 · static trace.
- Overflow and linked-list cycle defence of the **simulation** queue families
  (order chains, path requests, the network window), and whether same-tick
  inserts are drained immediately or deferred, per family · §6.2, §6.3,
  docs 04 and 08 · static trace.
- Remaining save box-level field maps beyond the order/task nodes, feature
  records, stockpile state, meteor globals and player economy stock already
  established as serialized · §7.3 "Scheduler persistence", doc 08 · static
  trace.

### Configuration and I/O

- Same-archive duplicate-name resolution beyond what document 02 establishes,
  and archive enumeration order within one wildcard group (host
  `FindFirstFileA` order, not sorted) · §3.2, doc 02 · asset census against the
  reference install.
- Save header and version compatibility rules, and all replay chunk semantics
  · §7.3, doc 08 · static trace.

### Error and diagnostics

- Which remaining failures select a fallback and which terminate the process;
  the established set is singleton failure (silent `-1`), display-init failure
  ("Environment Initialization Failed!" then cleanup), graded file-mapping
  codes 1/2/3, heap exhaustion (log, modal, abort — [R-PLAT-01 §5]), the
  `-R` registration path (exit code 1 on both outcomes — [R-PLAT-01 §2]),
  loading-thread creation failure (modal, exit code 1), and silent
  input-queue drop · §9 · static trace.
- The `DebugHelper.dll` protocol beyond `DebugFunc1(n)` (no such library ships;
  its interface is defined by a file that does not exist in retail) · §9
  [R-PLAT-01 §2] · none possible from the executable.
- How the integrity-breach UI maps to disconnect state, and the parse of the
  front-end `.zrb` list files, which lies in unrecovered code · §9 · static
  trace over the unrecovered regions.
