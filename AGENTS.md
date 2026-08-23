# Nanolathe — Agent Instructions

You are building **Nanolathe** (the Nanolathe Engine), an MIT-licensed modern reimplementation of the Total Annihilation engine.

## Stack

* Rendering / graphics / audio / window: `https://github.com/KaijuEngine/kaiju` (Go, via `kaijuengine.com` module replace `../kaiju/src` locally).
* Sim is authoritative, fixed-step, deterministic. Renderer is presentation-only and interpolates between sim ticks for smooth modern motion.
* Original assets are in `~/TotalAnnihilation` — use for development/testing, never commit. Everything data-driven must be — do not invent data or behavior the assets already define.

## Research sources (do not duplicate, reference them)

* `research/formats/*.md` — TA file format specs (HPI family, TDF, FBI, 3DO, GAF, TNT, etc.). Byte layouts, conversions, defaults. Source of truth for *how to read bytes*.
* `research/retail-executable-spec/*.md` (8 category docs + `README.md` + `GAP_ANALYSIS.md` / `GAP-ANALYSIS.md`) — clean-room retail `TotalA.exe` (`8e74a1dffa1f5988624c52048f5b20cd`) behavioral contracts. Source of truth for *what retail does*. Three levels: **Established → Supported inference → Unknown**. Unknown stays as explicit placeholder — do not invent.
* This repo's `PHASES.md` + `docs/PLAN_*.md` — *how we will build Nanolathe* (package layout, phase graph, invariants, exit criteria). They point at the two research dirs above; they do not restate arithmetic.

If specs disagree, `research/retail-executable-spec` wins.

## Scope

* **Implement:** skirmish and mission/campaign (single-player). Economy, construction, movement+pathfinding, visibility/LOS, weapons/projectiles/damage, COB VM, features/fire, AI (skirmish planner), GUI/HUD, camera/minimap, audio, effects, maps, save/load for single-player.
* **Not now:** networking / multiplayer / DirectPlay, replay beyond optional debug recorder, competitive desync hashes.
* No GPL code. MIT only.

## Style

* Keep tests light or skip them — expect churn, this is greenfield. A small deterministic fixture that locks a retail contract is worth more than coverage.
* Keep the codebase simple and fast. No over-engineering, no scattered compatibility flags, no `if retail` forks — there is one profile: `retail-3.1`. Use standard library types/interfaces when possible.
* Prefer immutable compiled definitions + mutable instances. Preserve provenance (logical path, provider, mount order) for diagnostics.
* Fixed-point world is `16.16` (`internal/sim/numeric.Fixed`), angles `uint16` `0..65535` per circle. Truncate toward zero where retail does (`__ftol`). Do not use `float64` for authoritative state except where retail explicitly stores `float32` (wind scalar, resource stock).
* One global simulation RNG (Park-Miller, `16807`, `0x7fffffff`, seed `(QPC ^ 0x66e29572)|1`) + one CRT RNG (`*214013+2531011`) — call order is deterministic. Do not add per-entity SplitMix streams for sim ticks.

## Architecture (summary, see PHASES.md)

```
vfs        — overlay of loose dirs + HPI-family archives (HAPI, cipher, SQSH)
formats    — lossless parsers (TDF blanking comment offsets, GAF/TNT/3DO reloc)
content    — compiled catalogs (units/weapons/features/movement/side/sound/maps) with defaults+conversions
retail/clock, retail/rng, retail/pool, retail/kernel — 30 Hz tick, budget clamp 0..5, phase graph, fixed pools (slot 0=null)
retail/world (terrain, features, occupancy) → retail/visibility → retail/units+orders+cob → retail/movement → retail/economy → retail/combat → retail/ai → snapshot
client     — Kaiju renderer interpolates Previous→Current at render fraction, palette/SHD lookup, fog presentation separate from LOS mask
```

Determinism rules: stable iteration (player `0..9`, unit pool asc, `projectile capture count at entry`), no generation-tagged handles for sim IDs, no hidden `map` iteration, no wall-clock in sim.

## Workflow

* Work from `PHASES.md` reading order (core → content → world → visibility → units/orders/cob → movement → economy → combat → AI/missions → GUI → audio/render → integration). Do not implement later phases before earlier invariants are green.
* Reference `research/retail-executable-spec/README.md:78` reading order.
* There is **no Oracle** in this repo (no `tools/probe`, no retail lock). For retail validation use `~/TotalAnnihilation` assets and a manual retail install if needed, but do not automate the retail executable. Future probes will be independently authored scenarios under `probes/` that are data-driven.
* Use `go vet` / `go test ./...` lightly. Do not add heavy test harnesses.

## What to copy vs rewrite

* Copied as library: `vfs/`, `formats/`, `internal/sim/numeric/`, `internal/sim/rng/` (the `RetailSimulation`/`RetailCRT` parts). These are already retail-correct and MIT-safe to reuse. Adapt imports to `github.com/nanolathe/nanolathe`.
* Reference only: the rest of `openta-go`'s `internal/sim/*` (`kernel`, `economy`, `combat`, `movement`, `visibility`, `construction`, `script`) — behavior is modern, not retail. Read for ideas, do not transplant. Reimplement per `retail-executable-spec`.
* Kaiju: depend via `go.mod` replace, do not vendor. Renderer interpolation idea is kept.

## Deliverables per phase

Each `PLAN_*.md` lists: goal, research inputs, Go packages/files, contracts to implement, explicit unknowns to keep as `TODO(retail: ...)`, and exit criteria (manual play test or tiny fixture). Mark `GAP_ANALYSIS.md` tasks `[x]` only after code merges, not after spec reading.
