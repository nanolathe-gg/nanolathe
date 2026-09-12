# Nanolathe — Agent Instructions

**Nanolathe** is an MIT-licensed reimplementation of the Total Annihilation
engine. Behavior comes from clean-room analysis of retail `TotalA.exe`; content
comes from the original assets. Everything is data-driven—never invent what the
executable or assets already define.

---

## The four rules

These override plans, work units, and agent judgement.

**1. Never invent behavior.** For unresolved questions, leave
`TODO(question): <unknown, and what would settle it>` at the code site, record
the gap in the owning research doc, and report it. An honest gap is complete;
a plausible guess is a defect. Verify any **Supported inference** before work
depends on it—inferences here have been found inverted.

**2. Never undo another agent's work.** Never reset, rebase, revert, amend, or
force-push `main`; never discard, stash, or overwrite another agent's changes.
Fix forward with an explanatory commit. Leave foreign dirty work alone and
report it; treat files you did not write this session as live concurrent work.

**3. Clone behavior, not code.** Raw disassembly, decompiler output, addresses,
register traces, and generated names stay in `$HOME/ta-decompile`. Translate
that analysis into an independently worded description of what the algorithm
does before anything enters `research/`, code comments, or commits.

**4. Everyone works in a worktree.** Never edit the shared `main` checkout.

---

## Clean-room discipline

Applies to everything committed: `research/`, code comments, commit messages,
test names, reports.

**Never commit:** executable addresses/offsets; decompiler-generated names;
disassembly or decompiler output; register narration; executable structure
layouts.

**Do write:** implementable plain-language algorithms and arithmetic; named
concepts rather than addresses; file offsets only as authored format layouts in
`research/formats`; and **Established**, **Supported inference**, or **Unknown**
confidence for every claim.

If a behavior cannot be described without an address, analysis is unfinished.
Keep the address trail in `$HOME/ta-decompile/notes/` for reproducibility.

---

## Research

`research/` is a curated reference, not a notebook:

```
research/
  formats/                  one doc per file format — byte layouts, defaults,
                            conversions. Source of truth for HOW TO READ BYTES.
  retail-executable-spec/   behavioral contracts. Source of truth for WHAT
                            RETAIL DOES.
    README.md               index, reading order, evidence language
    01..08-*.md             eight category docs, each owning one feature area
                            exhaustively: 01 runtime/determinism, 02 content/
                            vfs/formats, 03 world/visibility/rendering/audio,
                            04 units/orders/scripts/movement, 05 economy/
                            construction/features, 06 weapons/projectiles/
                            damage, 07 interface/input/camera/front-end,
                            08 sessions/campaign/AI/save/replay
```

**Adding a finding:** edit the owning category document in place; corrections
replace old text, with the change explained in the commit. Put closed gaps inline
under the `R-<id>` heading cited by code, preserving old anchors such as `[R-P0-01]`,
`[04 §5.4]`, and `[GAP T15]`. Format details belong in
`research/formats/<format>.md`. Do not create research notes, new directories,
or gap-analysis files. Open questions belong in code `TODO(T23)` / `TODO(T25)` /
`TODO(question)` markers and the category doc's **Unknown** list.

**Citations.** By document and section, never by line number: `[04 §7.2]` for
numbered docs, `[05 "Two-stage settlement algorithm"]` for docs 05 and 08
(unnumbered headings), `[fmt tnt]` for a format doc, `[R-P0-18-A §1]` for an
inline addendum section whose heading retains that exact anchor.

**Precedence:** `research/retail-executable-spec` owns behavior;
`research/formats` owns byte layout.

**Our implementation docs** (not retail evidence):

- `docs/ARCHITECTURE.md` — packages, authoritative tick, verification, citations.
- `docs/DESIGN_*.md` — Go design by engine area; point to research without
  restating its arithmetic.
- `docs/INVARIANTS.md` — the fourteen rules every diff is reviewed against.
- `docs/SPEC_CONFLICTS.md` — where the reference install disproves the spec.
  Read before "fixing" anything it lists.

---

## Worktrees

Create one before editing:

```
git worktree add ../nanolathe-wt-<task-slug> -b <task-slug> main
```

Within the requested scope, create the worktree, implement, run local checks,
fix failures caused by the change, and commit without asking for permission at
each step. Complete the applicable verification and review below before handing
work back. Report any remaining gap and what would settle it; do not describe
blocked behavior as implemented. A blocker report names the exact instruction
or missing evidence and the affected work. Continue independent work.

Commit as you go. Before landing, merge `main` into the branch and reconcile
both sides of conflicts; if that cannot be done without discarding another
agent's work or inventing behavior, report the conflict. Verify the integrated
branch as described below. If `main` moves, integrate it and re-run the gates.
A top-level maintainer may merge reviewed work; a sub-agent commits, reports,
and stops. Re-run the required gates after landing.

After landing, remove only your worktree and branch, then prune. Never bulk-remove
worktrees; an unmerged one may be live.

### Verification

- During iteration, run checks for the affected contracts and packages. For
  documentation edits, check the diff and links; run `go test ./internal/docs`
  when changing citations that its resolver checks. Broaden or repeat checks
  only for new changes, failures, unresolved concerns, or the landing gates.
- Before landing, run `tools/check` and `tools/check-retail`, the fast and
  integration gates defined in `docs/ARCHITECTURE.md` §6, plus the applicable
  design gate. Use these scripts instead of duplicating their commands; they
  control asset selection, tracked-file formatting, and test concurrency.
  Missing retail assets block the integration gate; report it as unrun.
- Include visual inspection and the performance checks below when applicable.
  Reviewers run the required checks themselves in the assigned worktree.

---

## Dispatching and reviewing sub-agents

Delegate bounded units when parallelism or specialist review improves throughput
or quality. Do not delegate a small one-file change or work whose coordination
cost exceeds doing it directly. One sub-agent owns one unit and one set of files;
the plan's Public API block is the contract between units. Every agent that may
edit files works in its own worktree; read-only reviewers inspect the assigned
worktree. Dispatch only when dependencies and earlier phase gates are green.
`docs/ARCHITECTURE.md` owns package boundaries; a dispatch names its files, and
concurrent units never name the same file.

### Context and orchestration efficiency

- Default independent units to fresh context (`fork_turns="none"`) when the
  dispatch can fully encode their contract. Otherwise inherit only the smallest
  number of recent turns needed; never inherit full history merely for convenience.
  Every dispatch must be self-contained and cite required files and sections.
- Keep the orchestrator focused on decomposition, dependencies, review, and
  landing. Record behavioral findings in the owning research document,
  implementation decisions in the owning design document, and transient progress
  in an explicitly owned project plan or task state. At major phase boundaries,
  write a concise handoff and compact or restart only after active units are
  committed, reviewed, and no longer need coordination.
- Parallelism saves elapsed time, not model usage. Dispatch only independent,
  ready work; avoid nested delegation unless the dispatch explicitly authorizes
  it. Wait for completion events instead of repeatedly polling unchanged agents.

A dispatch brief is exactly this shape:

```
Implement <unit> per docs/DESIGN_MOVEMENT_PATH.md §3 (contracts C4-C9).

Read first: AGENTS.md, docs/INVARIANTS.md, docs/DESIGN_MOVEMENT_PATH.md,
then research sections [04 §7.2] and [04 §7.3].

Worktree: .claude/worktrees/wu-07-3 on branch wu-07-3, branched from main.
Files you own (create/modify only these): internal/path/search.go, internal/path/search_test.go
Files you may read but must not modify: internal/world/*, internal/movement/profile.go
Public API you must satisfy: the Search block in the design document's package section.
Done when: contracts C4-C9 hold, `go test ./internal/path` passes, and the applicable Verification gates pass.
Unknowns: follow "When research does not answer" below within your file ownership; report investigation needs outside it and continue independent work.
```

Do **not** paste research documents into a dispatch—cite sections and let the
agent read them. Parallel work requires **exclusive file ownership**, **API
first** (a unit may add to its own package's API, never change another's), **no
cross-package refactors** (report upstream needs), and **one unit, one commit**.

**Review before merge.** Sub-agent summaries are not evidence; the orchestrator
is accountable for the diff it lands:

1. Read the diff, not the summary (`git diff main...HEAD`).
2. Follow Verification above, including the required checks after landing.
3. Verify two of the most arithmetic-heavy contracts against their cited
   research section — constants, order of operations, comparison strictness.
   This is where wrong constants get caught.
4. Grep the diff for clean-room violations before merging — offending text
   reads like rigour.
5. If it is visual, look at it: `--shot` renders headless; a screenshot is
   evidence, an assertion that it should look right is not.
6. Send corrections to the same agent while its context is useful; name the
   contract, citation, and observed behavior. After two unsuccessful rounds,
   rescope or split the unit.
7. Check `docs/INVARIANTS.md`: no `map` range in sim-visible paths, no
   `float64` outside the I2 allowlist, no `time.Now()` in sim packages, no new
   module dependencies, unknowns are `TODO(...)` markers, research the unit
   added follows "Adding a finding" above.

**When a unit lands badly:** fix it **forward** with a new commit that says
what changed and why. Never revert/reset/rebase/amend on `main`. If the whole
unit needs to come out, stop and escalate rather than deciding that yourself.

**Test policy.** Keep tests light and fast; asset-dependent tests skip when
retail assets are absent in the fast tier. The integration gate requires them.
A test exists to lock a retail contract that is easy to regress silently — an
ordering, a truncation, a comparison strictness — not for coverage. A test
that encodes an **inference** must say so. Assert relationships and hashes,
not censuses. Fixtures go in `testdata/` and are authored by us — never copied
retail bytes.

**Diagnostics.** Retail diagnostic text is reproduced **verbatim** where the
spec quotes it. Our own errors follow one shape:

```
nanolathe: <what failed>: logical path <path>, providers searched [<a>, <b>], expected <product>
```

Never log inside a sim tick path; return errors or record them on a diagnostic
sink the presentation layer drains.

**When research does not answer:** grep the category docs and `research/formats`
first. If it is a T23/T25 item, write `TODO(T23)` / `TODO(T25)` with the chosen
placeholder behavior and a one-line justification, and keep going. If it is a
genuine gap, analyze the executable in `$HOME/ta-decompile`, then write the
finding up clean-room in the owning doc before implementing dependent behavior.
Sub-agents investigate and edit only within their assigned ownership; report
upstream research or API needs to the orchestrator. If the gap cannot be settled,
record it under rule 1 and report the missing evidence. The gap blocks behavior
that depends on it; continue independent work. Inventing a constant is the one
unrecoverable failure mode.

---

## Stack, scope, style

- Modern Go, standard library first. Rendering/graphics/audio/window:
  [ebitengine](https://github.com/hajimehoshi/ebiten). Original assets live in
  `~/TotalAnnihilation` — use for development/testing, never commit.
  Ebitengine ships its own agent skills at
  [`hajimehoshi/ebiten/skills`](https://github.com/hajimehoshi/ebiten/tree/main/skills)
  — `run-ebitengine-app-headless` and `writing-kage-shaders`. Read the
  relevant one before headless-running the window build or writing a Kage
  shader (both come up in the GPU renderer work,
  [docs/DESIGN_GPU_RENDERER.md](docs/DESIGN_GPU_RENDERER.md)).
- **Implement:** skirmish and mission/campaign (single-player): economy,
  construction, movement+pathfinding, visibility/LOS, weapons/projectiles/
  damage, COB VM, features/fire, AI (skirmish planner), GUI/HUD,
  camera/minimap, audio, effects, maps, save/load for single-player.
  **Not now:** networking/multiplayer, replay beyond an optional debug
  recorder, competitive desync hashes. No GPL code — MIT only.
- Keep the codebase simple and fast. No over-engineering, no scattered
  compatibility flags — there is one behavior. Prefer immutable compiled
  definitions + mutable instances; preserve provenance (logical path,
  provider, mount order) for diagnostics.
- Fixed-point world is `16.16` (`internal/sim/numeric.Fixed`), angles `uint16`
  `0..65535` per circle. Truncate toward zero where retail does (`__ftol`).
  Do not use `float64` for authoritative state except where retail does — the
  exhaustive allowlist is in `docs/INVARIANTS.md` I2.
- One global simulation RNG (Park-Miller, `16807`, `0x7fffffff`, seed
  `(QPC ^ 0x66e29572)|1`) + one CRT RNG (`*214013+2531011`) — call order is
  deterministic. Do not add per-entity SplitMix streams.
- Comments explain *why* and cite the contract by research section. They never
  carry executable addresses.

## Architecture (summary, see docs/ARCHITECTURE.md)

```
vfs        — overlay of loose dirs + HPI-family archives (HAPI, cipher, SQSH)
formats    — lossless parsers (TDF blanking comment offsets, GAF/TNT/3DO reloc)
content    — compiled catalogs (units/weapons/features/movement/side/sound/maps) with defaults+conversions
clock, rng, pool — 30 Hz tick, budget clamp 0..5, phase graph, fixed pools (slot 0=null)
session    — authoritative sub-tick: the twelve-phase order, one owner per RNG stream, publication boundary
frame      — committed tick-end copies; presentation samples the committed tick with no interpolation [03 §2.4][I6]
world (terrain, features, occupancy) → visibility → units+orders+cob → movement → economy → combat → ai
client     — Ebitengine window loop presents a software framebuffer from the committed frame, palette/SHD lookup, fog presentation separate from LOS mask
```

## Workflow

- Read `docs/ARCHITECTURE.md` for package boundaries and dependencies. For
  behavior changes, read the owning `docs/DESIGN_*.md`, relevant invariants,
  and cited research sections; use `research/retail-executable-spec/README.md`
  to locate the evidence. Read benchmark docs for the performance checks below.
  A wording-only correction needs the affected text and its context.
- There is **no Oracle** in this repo. For retail validation use
  `~/TotalAnnihilation` assets and a manual retail install if needed, but do
  not automate the retail executable. Future probes live under `probes/` as
  independently authored, data-driven scenarios.
- Follow Verification above. Do not add heavy test harnesses.

## Live battle performance regression check

For simulation, movement, construction, model/effect rendering or renderer
storage changes, use the opt-in [live battle benchmark](docs/BATTLE_BENCHMARK.md)
with both `classic` and `modern` renderers when retail assets and a display are
available. It exercises moving armies and factory construction. Inspect the
feature census and captures as well as frame times; keep artifacts outside the
repository. Run benchmarks sequentially and compare matching scene metadata.

For a change to the authoritative tick alone, the displayless
[simulation-cost benchmark](docs/SIM_BENCHMARK.md) (`tools/sim-bench`) measures
ticks with no window, renderer or audio device: three 250-unit computer armies
fighting on one map, with per-phase attribution, a census that proves the
workload, and CPU and allocation profiles of the measured window. It shares the
same host lock, so it never runs beside the windowed benchmark.
