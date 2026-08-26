# Nanolathe — Agent Instructions

You are building **Nanolathe** (the Nanolathe Engine), an MIT-licensed modern reimplementation of the Total Annihilation engine.

The engine is based on reverse engineering of the retail TotalA.exe executable to determine engine behaviors and reimplement them in a clean-room fashion. The engine is data-driven, using the original Total Annihilation assets (HPI archives, TDF text files, GAF/TNT/3DO graphics, SHD palette files, WAV/OGG audio) as the source of truth for game content.

Important: do not invent data or behavior that is not explicitly defined in the retail executable or the original assets. If a question is unresolved, leave a `TODO(...)` placeholder in code and document it in your report so we can research further. Do not guess or make up behavior.

You may use sub-agents for research, implementation, reviews, etc. Agents should use their own git worktree and merge back to `main` when the work is finished, resolving any conflicts. Never undo or overwrite another agent's work without consent.

## Stack

- Use modern Go for implementation. Use standard library interfaces when possible.
- Rendering / graphics / audio / window: use [ebitengine](https://github.com/hajimehoshi/ebiten)
- Original assets are in `~/TotalAnnihilation` — use for development/testing, never commit. Everything data-driven must be — do not invent data or behavior the assets or retail engine already define.

## Research sources (do not duplicate, reference them)

- `research/formats/*.md` — TA file format specs (HPI family, TDF, FBI, 3DO, GAF, TNT, etc.). Byte layouts, conversions, defaults. Source of truth for _how to read bytes_.
- `research/retail-executable-spec/*.md` (8 category docs + `README.md` + `GAP_ANALYSIS.md` / `GAP-ANALYSIS.md`) — clean-room retail `TotalA.exe` (`8e74a1dffa1f5988624c52048f5b20cd`) behavioral contracts. Source of truth for _what retail does_. Three levels: **Established → Supported inference → Unknown**. Unknown stays as explicit placeholder — do not invent.
- This repo's `PHASES.md` + `docs/PLAN_*.md` — _how we will build Nanolathe_ (package layout, phase graph, contracts, exit criteria). They point at the two research dirs above; they do not restate arithmetic.
- `docs/ORCHESTRATION.md` — orchestration playbook: how a phase is cut into work units, what a sub-agent is told, the review checklist, the ownership map.
- `docs/INVARIANTS.md` — the fourteen rules every diff is reviewed against.
- `docs/SPEC_CONFLICTS.md` — where the reference install disproves the spec. Read before "fixing" anything it lists.
- `docs/WORK_UNITS.md` — flat dispatch index of every work unit.

Precedence: `research/retail-executable-spec` wins for **behavior**;
`research/formats` wins for **byte layout**. They do not compete — a format
question the executable spec calls blocked (the compressed GAF decoder, for
instance) is usually fully specified in `research/formats` and often already
implemented in `formats/`.

For gaps, use `/tmp/ta-decompile` to decompile and analyze the retail executable behavior. Never commit decompiled code. Document findings in `./research` with general technical language describing algorithms, behaviors, etc.

Cite research by document and section, never by line number:
`[04 §7.2]` for numbered docs, `[05 "Two-stage settlement algorithm"]` for docs
05 and 08 (unnumbered headings), `[fmt tnt]` for a format doc, `[GAP T15]` for a
gap task.

## Scope

- **Implement:** skirmish and mission/campaign (single-player). Economy, construction, movement+pathfinding, visibility/LOS, weapons/projectiles/damage, COB VM, features/fire, AI (skirmish planner), GUI/HUD, camera/minimap, audio, effects, maps, save/load for single-player.
- **Not now:** networking / multiplayer / DirectPlay, replay beyond optional debug recorder, competitive desync hashes.
- No GPL code. MIT only.

## Style

- Keep tests light and fast or skip them — expect churn, this is greenfield. A small deterministic fixture that locks a retail contract is worth more than coverage.
- Keep the codebase simple and fast. No over-engineering, no scattered compatibility flags, no compatibility forks — there is one behavior. Use standard library types/interfaces when possible.
- Prefer immutable compiled definitions + mutable instances. Preserve provenance (logical path, provider, mount order) for diagnostics.
- Fixed-point world is `16.16` (`internal/sim/numeric.Fixed`), angles `uint16` `0..65535` per circle. Truncate toward zero where retail does (`__ftol`). Do not use `float64` for authoritative state except where retail does — the exhaustive allowlist is in `docs/INVARIANTS.md` I2 (resource stock and ledger carry, wind scalar, the clock budget product, the ballistic discriminant, model draw trig).
- One global simulation RNG (Park-Miller, `16807`, `0x7fffffff`, seed `(QPC ^ 0x66e29572)|1`) + one CRT RNG (`*214013+2531011`) — call order is deterministic. Do not add per-entity SplitMix streams for sim ticks.

## Architecture (summary, see PHASES.md)

```
vfs        — overlay of loose dirs + HPI-family archives (HAPI, cipher, SQSH)
formats    — lossless parsers (TDF blanking comment offsets, GAF/TNT/3DO reloc)
content    — compiled catalogs (units/weapons/features/movement/side/sound/maps) with defaults+conversions
clock, rng, pool, kernel — 30 Hz tick, budget clamp 0..5, phase graph, fixed pools (slot 0=null)
world (terrain, features, occupancy) → visibility → units+orders+cob → movement → economy → combat → ai → snapshot
client     — Kaiju renderer interpolates Previous→Current at render fraction, palette/SHD lookup, fog presentation separate from LOS mask
```

## Workflow

- Work from `PHASES.md`: phase table, dependency graph, exit criteria, playable gates. Do not implement a later phase before its dependencies are green.
- Dispatching implementation work: read `docs/ORCHESTRATION.md` first. One work unit owns one set of files; the plan's Public API block is the contract between units.
- Reference `research/retail-executable-spec/README.md` for the research reading order.
- There is **no Oracle** in this repo (no `tools/probe`, no retail lock). For retail validation use `~/TotalAnnihilation` assets and a manual retail install if needed, but do not automate the retail executable. Future probes will be independently authored scenarios under `probes/` that are data-driven.
- Use `go vet` / `go test ./...` lightly. Do not add heavy test harnesses.
