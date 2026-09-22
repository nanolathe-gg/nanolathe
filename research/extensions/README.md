# Non-retail extension reference

## Scope and index

This directory is the authorized home for independently worded contracts for
non-retail patches and extensions. It is a curated reference, not a notebook,
a list of desired features, or evidence about retail Total Annihilation.

Add one owning document per extension or coherent extension family when there
is sourced research to record, and link it from this index with its subject
and source version. Update that document in place as evidence improves; do
not add session notes or gap-analysis files. Each contract belongs under a
stable named heading and keeps its unresolved questions beside it.

| Reference | Scope and source version |
|---|---|
| [Extended build menus](build-menus.md) | TA Zero Alpha 5 authored placements, repeated membership records, and documented twelve-slot sidebar; patch membership semantics remain unknown. |
| [Non-retail weapon target keys](weapon-target-keys.md) | `nottoair`, `nottounderwater` and `surfacefire` as authored and described by TA: Escalation Gold 10.2.0, and `toaironly` as authored by ProTA 4.8; TA Zero Alpha 5 uses none. Documented intent, authored census and the inspected Escalation parser/acquisition evidence — the Gold-era admission predicate and `toaironly` remain unknown; the current community-patch line's predicates are settled from source in the document's final section. |
| [ProTA 4.8 engine package](prota-engine.md) | Historical engine package versus current source profile; renamed authored directories, twelve-slot interface, palette overrides, campaigns/AI/music requirements and the retail script-port/opcode census. |
| [TA: Escalation Gold 10.2.0 engine package](taesc-engine.md) | The patched-executable + engine-DLL package: documented engine changes, the executable patch inventory, the extension-key registry (veterancy, preview pieces, rotations) with the authored key census, and the recorder-provided script-port surface. |
| [TA Zero Alpha 5 engine package](ta-zero-engine.md) | The patched executable and renamed data trees/registry/save format, the executable patch inventory, the documented controls and commands, and the small asset extension surface (ports 70/74, `UnitControl`, `SoundLava`). |
| [TA Demo Recorder session DLLs](ta-demo-recorder.md) | Historical command/version boundaries plus current-source script integration: callback arguments, immediate/deferred starts, optional 64 slots, map-script scheduling and persistence limits. |
| [Extended script ports](script-ports.md) | Pinned current-recorder getter/setter dispatch through port 400: arguments, locality/playback gates, stateful commands, spawning/search arithmetic, definition edits, effects and map commands. The older packages' authored eight-port subset remains separately scoped; port `75` is controller locality, not visibility. |
| [Community patch engine behavior](community-patch-engine.md) | The TADR `tdraw.dll` line (MIT source, pinned commit `dcff5dd`, seven build profiles): compatibility target definition, profile matrix, configuration surface, behavior contracts for limits, simulation fixes, weapon/unit keys, construction (including click snap), environment, spawns, and session protocol, verified claim by claim against the source on 2026-09-21; recorder distribution 2026.9.9, draw DLL self-version 2026.8.6. |
| [Mod engine-package compatibility](mod-engine-compatibility.md) | Package architecture, input collisions, contract ownership, and the 2026-09-22 authored rendering audit: strict admission, large effect-bank limits, missing references and bounded model captures. Full support remains unverified. |
| [Community patch pathfinding](community-patch-pathfinding.md) | The TA Unofficial Patch line (v3.9.01 of 2012, v3.9.02 of 2013) and the dated engine builds that continue it: the documented "pathfinding cycles" raise and its `AISearchMapEntries` setting, the movement-adjacent construction-unit notes of the 2024–2026 builds, and the census establishing that the 3.9.x documentation records a limit raise and nothing else. |
| [Shared draw-DLL interface](draw-engine-interface.md) | The renderer family's megamap, whiteboard and selection interface: keys, zoom, icon configuration, ring thresholds, chat-stream markers, drag-filter masks, and the per-build preference-key differences. |
| [Community patch rendering](community-patch-rendering.md) | MIT TADR revision `dcff5dd`: shade fallback, team-coloured construction effects, reload/cargo bars, preview geometry and scan, rotation artwork, minimap palette/fog/features, contact admission and visual acceptance cases. Older package DLL equivalence remains unknown. |

## Evidence policy

For each behavioral claim, record the extension name, exact source version or
revision, primary source URL or reproducible artifact identity, relevant
section or symbol, evidence scope and confidence. Do not treat one release's
contract as proof about every release. Conflicting versions remain distinct
until evidence resolves their applicability.

Primary patch documentation, authored content and appropriately licensed
source may support an extension claim. Check source provenance and license
before using source material; this project remains MIT-only and excludes GPL
code. Describe algorithms independently rather than copying or translating
another implementation. Source availability does not waive clean-room rules.
Documentation establishes what a release documents; content establishes what
it authors; source establishes what the inspected implementation does. None
by itself establishes behavior outside that scope, or retail behavior.

Do not disassemble or decompile third-party patches. Extension investigation
uses the sources above and bounded manual observations; the retail static
analysis workflow does not extend to patch binaries. Several documents retain
behavior decoded from shipped patch binaries under earlier practice, marked
with an **Evidence provenance** note: that material is clean-worded background,
its observations stand as recorded, but no new contract may be closed by that
method. Where an appropriately licensed source now settles the same ground
(the community-patch line's is MIT), the source is the settling evidence.

Use these confidence labels on every claim:

- **Established** — a cited primary source directly supports the bounded
  claim for the identified version. State whether the evidence is documented,
  authored or implemented behavior, and retain any limits of that evidence.
- **Supported inference** — evidence favors the claim but leaves a caller,
  condition, arithmetic step or version boundary unresolved. Name the missing
  evidence and verify it before implementing dependent behavior.
- **Unknown** — evidence cannot yet settle the contract. Name the question and
  what would settle it; do not supply a plausible mechanic or constant.

Secondary summaries and community recollections can direct investigation;
they do not establish arithmetic or close a contract. A manual observation
must record its setup, extension version and observed result, without claiming
more than that observation supports. Do not automate the retail executable.

All committed material follows [AGENTS.md](../../AGENTS.md). Executable
addresses, disassembly, decompiler output, generated symbols and executable
structure layouts must not enter this reference. Keeping such material outside
the repository is not permission to analyze third-party patch binaries.
File-format layouts belong in
[research/formats](../formats/README.md), with their version boundaries stated.
No patch binaries, copied retail assets or third-party implementation code
belong in this directory.

## From evidence to implementation

Keep retail baseline citations in
[retail-executable-spec](../retail-executable-spec/README.md); extension sources
must never be used to fill a retail gap or relabel non-retail behavior as
historical retail behavior. Cite extension documents by name and heading,
with a Markdown link, rather than using retail section identifiers.

Research does not approve adoption. An explicitly approved Nanolathe Modern
policy belongs in its owning `docs/DESIGN_*.md`, with the strict baseline,
extension evidence, intended Nanolathe behavior, boundaries and tests stated
separately. Follow [DESIGN_GAMEPLAY_RULES §9](../../docs/DESIGN_GAMEPLAY_RULES.md#9-extending-the-existing-mechanism)
to use or extend the existing rules interfaces and registry. Load-time content
profiles remain separate from gameplay selection. Strict 3.1 must bypass an
approved departure, with its RNG and resource behavior preserved. Existing
user authorization carries forward: implementing and extending interfaces for
an already authorized mechanic does not require approval again.

If evidence is missing, keep an **Unknown** in the owning extension document
and a `TODO(question)` at any dependent code site, and report what would settle
it. Authorization to research extensions does not authorize new mechanics,
a new selection framework, stateful rule objects, or save-format changes.
