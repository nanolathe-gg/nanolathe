# Diagnostic capture

Press **Ctrl+Shift+F11** in a battle to pause and write a diagnostic bundle.
The shortcut also works while paused or in a battle modal. Each key-down edge
creates a unique directory under `~/Nanolathe/diagnostics`; holding the keys
does not repeat. The status line and standard error report the directory and
any incomplete result. Resume with the normal pause or menu controls.

This is a Nanolathe development feature, not a retail binding. Capture consumes
the triggering input sample before gameplay dispatch, joins background recording,
and pauses through the existing scheduling bridge. It neither steps simulation
nor draws either authoritative RNG. Disk errors also leave the game paused.
Writing is synchronous on the game/window owner, so a large battle can take a
few seconds. OS memory tools each have a five-second timeout.

`manifest.json` uses schema version 1. It records collection times, capture
duration, process ID, map, authoritative and committed ticks, original and
resulting pause truth, each file's bytes/error, and explicit omissions. The
committed frame can still report its old pause value. A diagnostic bundle is a
partial observation, **not a restartable save**. `Complete` means the requested
files were written successfully; it does not erase the documented omissions.

| File | Contents |
| --- | --- |
| `runtime.json` | Initial Go memory statistics, scalar runtime metrics, goroutine count, GOMAXPROCS and heap sampling rate |
| `heap.pprof`, `allocs.pprof` | Sampled live heap and cumulative allocation profiles, collected before engine snapshot allocations; no forced GC |
| `goroutine.pprof`, `threadcreate.pprof`, `goroutines.txt` | Go goroutine/thread-creation profiles and full goroutine stacks |
| `process.json` | Current PID/PPID, executable path, Go build settings and revision if embedded; no environment or command arguments |
| `process-memory.txt`, `virtual-memory.txt` | On macOS, current-process `ps` memory counters and `vmmap -summary`; failures retained individually |
| `process-status.txt`, `process-smaps_rollup.txt` | Linux `/proc/self` memory diagnostics |
| `file-descriptors.json` | Approximate current descriptor count; no descriptor targets or file contents |
| `session.json` | Exported clock state, RNG words, result/pending result, phase/staged-event/pending-command diagnostics, economy/player/alliance state |
| `units.jsonl` | One detached unit per line in the world's existing player/slot traversal order: slot and existing publication identity, definition, owner, position, health/build, movement, weapons, cargo links, queues and COB runtime |
| `movement.json` | Existing detached movement/path trace projection, including routes and available request/results |
| `features.json`, `projectiles.json` | Explicit runtime projections, excluding definition/assets and terrain data |
| `construction.json`, `ai.json` | Builder/product links and AI scheduling/group/rally projections |
| `client.json`, `battle.json` | Camera/zoom, viewport/input/modal state, interpolation, recorder counters, cache sizes, main and individual worker model scratch, cached body/shadow storage |
| `renderer.json`, `last-frame.png` | Device counters, paused-world reuse counts and retained image size, and one readback of the retained last composition; no additional render pass |

Units use raw signed 16.16 fixed point and 65536 angle units per circle. A
publication identity of zero means this exact unit occupant has not been
published. Pool slots can be reused; their stale relationships are preserved,
not repaired by the capture. COB data includes thread status, PC, complete
32-word physical argument/local/expression window, sleep/wait/signal state,
execution identities, return values, pending callback records, completion-receiver presence, statics,
piece pose/animation and program names/checksum; it excludes bytecode and
callback closures. Existing order snapshot bounds and truncation flags apply;
route geometry is separately in `movement.json`.

Important limits are also repeated in the manifest. Private clock bits,
mission/visibility internals, path search heaps,
feature animation cursors/event ordering, combat target/pending-aim registries,
construction work beyond links and AI per-definition vectors are omitted.
Historical callback events exist only if tracing was already enabled. No save
projection is attempted because transient state and callbacks do not have a
complete faithful restore contract.

Storage estimates describe explicitly listed backing arrays and logical RGBA
images. They exclude maps, pointed-to objects, allocator overhead, Ebitengine
internal textures and driver allocations. GPU arena offsets reset after
execution and can rewind during growth; they are **not last-frame peaks**.
Client worker scratch is listed separately because each recorder owns its own
arenas. Retained obsolete references and raw-versus-clipped outline row spans
are not measured. These counters alone do not establish the cause of high RSS.
The adapter currently does not retain the exact presented tick; it explicitly
marks that identity unavailable rather than substituting the committed tick.

Profiles are sampled; stacks and runtime state have independent timestamps.
No CPU recording, forced GC or large raw heap dump is enabled. Block/mutex
histories are omitted because this capture did not enable their sampling.
