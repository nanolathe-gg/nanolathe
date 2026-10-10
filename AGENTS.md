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

### Intentional Modern gameplay

The user explicitly authorizes **Modern** gameplay (default) alongside opt-in
**Strict 3.1**. Retail research defines the strict baseline. Approved Modern
rules are intentional departures, not parity defects: **do not remove them
merely because retail behaves differently**.

Every new intentional gameplay departure must be selected through the existing
central `gameplay.Mode`, disabled by Strict 3.1, and documented as **Nanolathe
Modern policy** in the owning design document. Record the strict behavior,
modern behavior, boundaries and tests there; do not rewrite retail research to
claim the new policy is historical behavior. Tests must preserve both the
Modern contract and the Strict bypass, including RNG and resource effects.
This is an explicit exception to rule 1 for approved policy, not permission to
invent unresolved retail mechanics. Renderer and host preferences retain their
separate controls.

Use the existing interfaces and `session.RuleSet` registry described in
[DESIGN_GAMEPLAY_RULES](docs/DESIGN_GAMEPLAY_RULES.md), especially §9, for
future gameplay work. Extend the owning interface and both reserved defaults
when a new decision is needed; justify a new owning-package seam only when
none fits, and compose it through the same `RuleSet`. Do not add a second
registry, capability-selection system, or scattered gameplay booleans.
Load-time content profiles remain separate from gameplay selection. Research
an extension before proposing its contract; evidence that a patch implements
a behavior is not authorization to enable that behavior in Nanolathe.

**Mutators are a mode-independent exception (user-authorized
2026-09-23).** A mutator is a global multiplier applied to the per-battle
catalog clone at battle entry. It applies in every mode, **Strict 3.1
included**, because it transforms content rather than rules. It adds no seam,
no RNG and no per-tick state, and it is owned by
[DESIGN_MODS_MUTATORS](docs/DESIGN_MODS_MUTATORS.md) §6. Strict 3.1 *with no
mutators* is the retail baseline, and every fingerprint lock runs with none.
Anything data cannot express is not a mutator and follows the rules above.

**Survival is a second mode-independent exception (user-authorized
2026-09-23).** It is a scenario, not a rule: a skirmish session with a
commanderless attacker slot and a wave director that creates units through
the ordinary allocator and gives them ordinary orders. The survivors share
sight, radar and income as one side. It adds no seam, runs in every mode, and
exists only in a Survival session, so no other session or fingerprint lock
changes. Survival battles cannot be saved. It is owned by
[DESIGN_SURVIVAL](docs/DESIGN_SURVIVAL.md).

**The Modern AI computer player is the third mode-independent exception
(user-authorized 2026-09-25).** Each computer player is Classic (its rule
set's own planner) or Modern (the util+tac brain), chosen per player in
skirmish and Survival in every gameplay mode, Strict 3.1 included, and
nothing else chooses it: no rule set selects the Modern AI (the `modern-ai`
set was retired; `--ai-player all=modern` marks every computer player). A
Modern player plays under the bound set's rules but is paid in full in every
mode, since its difficulty is its persona
([Modern AI full income](docs/DESIGN_ECONOMY_CONSTRUCTION.md#modern-ai-full-income));
a Classic player keeps its set's income. It chooses who decides for a
player, not a rule. On 2026-09-24 the user approved for it what the retail
planner may not do — a private random generator per computer player,
controller state of its own, and thinking on a background goroutine. Strict
3.1 *with only Classic players* is the retail baseline, and every fingerprint
lock runs Classic. The policy is owned by
[DESIGN_SESSIONS_AI_SAVE](docs/DESIGN_SESSIONS_AI_SAVE.md#modern-ai-computer-player);
its seam, lifecycle and determinism rules by
[DESIGN_GAMEPLAY_RULES](docs/DESIGN_GAMEPLAY_RULES.md#the-modern-ai-controller).

**Multiplayer is the fourth mode-independent exception (user-authorized
2026-10-01).** A multiplayer battle is a session kind, not a rule: relayed
deterministic lockstep, in which every client runs the whole simulation and
only commands travel. It is offered for skirmish and Survival in every
gameplay mode, Strict 3.1 included. Retail multiplayer has no single outcome
to match, so "Strict 3.1 online" is defined by owner-machine equivalence —
each seat's work runs as that seat's own retail machine would have run it —
and it neither reproduces retail's transport nor interoperates with it.
The online policies apply in every mode and are Nanolathe's, not retail's:
a command that changes the world needs the lobby's cheat permission even
where retail leaves it ungated; online battles run at normal speed with no
pause in the first releases; a room may restrict views for all its
players; and teammates in an online skirmish share sight and radar
(user-authorized 2026-10-09). Follow-up policies approved 2026-10-02 use one shared directed
alliance matrix, canonical per-player explored histories with only the
entering human/hosted-computer reset scope, and request-tick map sharing.
Computer difficulty is explicit per seat; computers are excluded from
Deathmatch initially. Strict keeps one computer per human; Modern/Community
allow multiple within available seats through the existing `RuleSet`.
Departure policies approved 2026-10-02: a disconnected seat stays idle until
the connected, still-playing humans pass a removal vote — no connected seat
can be voted out, resignation needs no vote, and no timer removes a seat —
and at a human's final removal Strict removes its hosted computers with it as
retail does, while Modern/Community keep them running through the existing
`RuleSet`. The same day the maintainer approved the protocol details the
design had proposed (vote window and cooldown, cumulative grace, client
sequence numbers, per-command work limits, the online `Give` range, no
battle-wide difficulty word), decided that a finally removed seat is absent
from the end-condition sweeps while the perspective its computers borrow
keeps updating, and that a Strict resignation deletes the leaver's units
silently as retail's menu quit does on the other machines.
These policies and their tests are owned by DESIGN_MULTIPLAYER §6.6–§6.7,
§11.1, Q22–Q29 and §19 O23; they do not change single-player. Nothing a relay, a lobby or a
host supplies may change what a tick
computes, and a single-seat battle is untouched: every fingerprint lock runs
single-player. It is owned by
[DESIGN_MULTIPLAYER](docs/DESIGN_MULTIPLAYER.md), which stages the work in
milestones (§16). Build them in that order except for the maintainer-approved
bounded increments brought forward ahead of full M3 platform acceptance and M4
replays, each preserving the existing simulation contracts: the 2026-10-07
two-client play-test slice (§16.4), the first hosted relay the same day (§16.5)
and the first online lobby on 2026-10-08 (§16.6). The maintainer had all three
landed on main on 2026-10-08. On 2026-10-09 the maintainer approved computer
players in online rooms, added by the room's host (§6.6), with one more
Nanolathe Modern policy through the existing `SeatRules` seam: under Modern
and Community a hosted computer keeps playing, with normal sight, after its
host human is defeated, while Strict 3.1 keeps retail's one countdown per
machine (Q30); and local replays, recorded for every skirmish and Survival
battle and played back from a Replays screen (§10).

**Unit restrictions are the fifth mode-independent exception (user-authorized
2026-10-05).** A restriction is retail's multiplayer unit-restriction count,
offered for skirmish and Survival in every gameplay mode, Strict 3.1
included, and edited in the unit viewer from a card beside the mutators. A
count of 0 removes the definition from the per-battle catalog clone at battle
entry, as retail's battle-entry compile removes a cleared record; a count of
1–100 caps each player's records of it at the allocator's existing
per-definition test, nanoframes included. It is applied before the mutators,
adds no seam, no RNG and no per-tick state, and campaign missions keep their
own authored unit lists. Everyone obeys, the Survival wave attacker included.
Classic computer players keep retail's behaviour — a capped product is
refused at creation and retried 300 ticks later — while the Modern AI does
not choose a unit once its own records have reached the cap. Definitions
authored `norestrict` can never be restricted, and nothing is seeded: an
empty set leaves the catalog, its hash and every identity untouched, `wacky`
content included. Strict 3.1 *with no restrictions* is the retail baseline,
and every fingerprint lock runs with none. The online lobby carries the
host's set as battle-configuration field 12 (user-authorized 2026-10-08,
DESIGN_MULTIPLAYER §16.6). It is owned by
[DESIGN_MODS_MUTATORS](docs/DESIGN_MODS_MUTATORS.md) §15.

Current policies: [terrain admission](docs/DESIGN_WEAPONS_PROJECTILES.md#231-modern-terrain-admission),
[Hold Fire](docs/DESIGN_UNITS_ORDERS_COB.md#modern-hold-fire), and
[factory-exit yielding](docs/DESIGN_ECONOMY_CONSTRUCTION.md#modern-factory-exit-yielding), and
[construction-site clearance](docs/DESIGN_ECONOMY_CONSTRUCTION.md#modern-construction-site-yielding), and
[authored build membership](docs/DESIGN_ECONOMY_CONSTRUCTION.md#modern-authored-build-membership), and
[learned terrain](docs/DESIGN_MOVEMENT_PATH.md#modern-learned-terrain), and
[group-order spreading](docs/DESIGN_MOVEMENT_PATH.md#modern-group-order-spreading), and
[bounded path work](docs/DESIGN_MOVEMENT_PATH.md#modern-bounded-path-work), and
[group destination slots](docs/DESIGN_INTERFACE_HUD_INPUT.md#modern-group-destination-slots), and
[unreachable moves](docs/DESIGN_MOVEMENT_PATH.md#modern-unreachable-moves), and
[route straightening](docs/DESIGN_MOVEMENT_PATH.md#modern-route-straightening), and
[wedge escape](docs/DESIGN_MOVEMENT_PATH.md#modern-wedge-escape), and
[traffic](docs/DESIGN_MOVEMENT_PATH.md#modern-traffic) (user-authorized
2026-09-29: friendly units never share cells) with its
[steering](docs/DESIGN_MOVEMENT_PATH.md#modern-steering),
[route smoothing](docs/DESIGN_MOVEMENT_PATH.md#modern-route-smoothing),
[search weight](docs/DESIGN_MOVEMENT_PATH.md#modern-search-weight),
[prompt re-routing](docs/DESIGN_MOVEMENT_PATH.md#modern-prompt-re-routing),
[route claims](docs/DESIGN_MOVEMENT_PATH.md#modern-route-claims),
[arrival places](docs/DESIGN_MOVEMENT_PATH.md#modern-arrival-places) and
[routes through friends](docs/DESIGN_MOVEMENT_PATH.md#modern-routes-through-friends), and
[wave air targets](docs/DESIGN_SESSIONS_AI_SAVE.md#modern-wave-air-targets), and
[Modern AI move retention](docs/DESIGN_UNITS_ORDERS_COB.md#modern-ai-move-retention), and
[repair-pad queue](docs/DESIGN_MOVEMENT_PATH.md#modern-repair-pad-queue), and
[infection](docs/DESIGN_UNITS_ORDERS_COB.md#modern-infection) (user-authorized
2026-10-05) with its
[Survival infection hunters and infector waves](docs/DESIGN_SURVIVAL.md#modern-infection-hunters),
[infector target preference](docs/DESIGN_WEAPONS_PROJECTILES.md#modern-infector-target-preference)
and [Modern AI infector focus](docs/DESIGN_SESSIONS_AI_SAVE.md#modern-ai-infector-focus), and
[Modern AI restriction caps](docs/DESIGN_SESSIONS_AI_SAVE.md#modern-ai-restriction-caps).
Retired from Modern by its traffic policy, and kept only as the pathfinding
laboratory's baseline (`movement.OverlapRules`): re-route staggering, allied
pass-through, jam release and pocket release. Do not restore them as parity
or regression fixes.
See also [INVARIANTS.md I11](docs/INVARIANTS.md#i11--retail-baseline-and-modern-gameplay).

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
  extensions/               non-retail extension contracts and evidence policy;
                            see extensions/README.md. Never retail evidence.
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

**Adding a retail finding:** edit the owning category document in place;
corrections replace old text, with the change explained in the commit. Put closed gaps inline
under the `R-<id>` heading cited by code, preserving old anchors such as `[R-P0-01]`,
`[04 §5.4]`, and `[GAP T15]`. Format details belong in
`research/formats/<format>.md`. Except for the authorized extension reference
below, do not create research notes, new directories, or gap-analysis files.
Open questions belong in code `TODO(T23)` / `TODO(T25)` / `TODO(question)`
markers and the category doc's **Unknown** list.

**Non-retail extension research:** `research/extensions/` is explicitly
authorized for curated extension contracts, under its
[README](research/extensions/README.md) evidence policy. Primary patch
documentation, authored content, and appropriately licensed source can support
extension claims, with source version, scope and confidence recorded. Describe
behavior independently; do not disassemble third-party patches. Use the
extension evidence policy rather than the retail executable-analysis workflow.
Do not promote extension evidence into the retail specification or treat this
directory as approval for new mechanics.

**Citations.** By document and section, never by line number: `[04 §7.2]` for
numbered docs, `[05 "Two-stage settlement algorithm"]` for docs 05 and 08
(unnumbered headings), `[fmt tnt]` for a format doc, `[R-P0-18-A §1]` for an
inline addendum section whose heading retains that exact anchor.

**Precedence:** `research/retail-executable-spec` owns retail behavior;
`research/formats` owns byte layout; `research/extensions` owns sourced
non-retail extension behavior only. Explicitly approved Modern gameplay
departures are owned by their design contracts, as described above.

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
  Reviewers run affected contract checks themselves in the assigned worktree;
  the landing owner runs the whole-tree gates once on the integrated candidate.
  Use `tools/check-retail --full` for changes affecting long-match AI, termination,
  or extended campaign/save sequences; ordinary landings use the short retail tier.
  Retail assets are a fixed reference install: reuse the normal test cache and
  use `tools/check-retail --fresh` for an uncached diagnostic run. After changing
  assets in place, clear old results once with `go clean -testcache`.
  See `docs/ARCHITECTURE.md` §6 for budgets and host coordination.

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
2. Run affected contract checks; the landing owner runs the whole-tree gates
   before and after landing, per Verification above.
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
   added follows the applicable research rules above.

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
genuine retail gap, analyze the retail executable in `$HOME/ta-decompile`, then
write the finding up clean-room in the owning doc before implementing dependent
behavior. For non-retail extension gaps, follow `research/extensions/README.md`:
use primary documentation, authored content, appropriately licensed source or
manual observations; do not disassemble third-party patches.
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
  camera/minimap, audio, effects, maps, save/load for single-player; and
  multiplayer skirmish and Survival with replays, by relayed lockstep, in the
  order [DESIGN_MULTIPLAYER](docs/DESIGN_MULTIPLAYER.md) §16 stages it
  (user-authorized 2026-10-01).
  **Not now:** retail's network transport or cross-play with any other
  engine, co-op campaign, multiplayer saves, and ranked play before its own
  gate. No GPL code — MIT only.
- Keep the codebase simple and fast. No over-engineering, no scattered
  compatibility flags. The user-authorized central Modern / Strict 3.1 gameplay
  policy is the exception; see docs/DESIGN_WEAPONS_PROJECTILES.md §2.3.1. Prefer immutable compiled
  definitions + mutable instances; preserve provenance (logical path,
  provider, mount order) for diagnostics.
- Fixed-point world is `16.16` (`internal/sim/numeric.Fixed`), angles `uint16`
  `0..65535` per circle. Truncate toward zero where retail does (`__ftol`).
  Do not use `float64` for authoritative state except where retail does — the
  exhaustive allowlist is in `docs/INVARIANTS.md` I2.
- One global simulation RNG (Park-Miller, `16807`, `0x7fffffff`, seed
  `(QPC ^ 0x66e29572)|1`) + one CRT RNG (`*214013+2531011`) — call order is
  deterministic. Do not add per-entity SplitMix streams. The one exception
  is the Modern AI controller's private per-player generator, which never
  draws from or seeds either stream
  ([INVARIANTS.md I4](docs/INVARIANTS.md#i4--two-rng-streams-call-order-is-behavior),
  "Modern AI exception").
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
  For gameplay extensions, also read `docs/DESIGN_GAMEPLAY_RULES.md` §9 and
  `research/extensions/README.md`; reuse or extend the existing interfaces.
  A wording-only correction needs the affected text and its context.
- There is **no Oracle** in this repo. For retail validation use
  `~/TotalAnnihilation` assets and a manual retail install if needed, but do
  not automate the retail executable. Future probes live under `probes/` as
  independently authored, data-driven scenarios.
- Follow Verification above. Do not add heavy test harnesses.

## Live battle performance regression check

For simulation, movement, construction, model/effect rendering or renderer
storage changes, use the opt-in [live battle benchmark](docs/BATTLE_BENCHMARK.md)
with both `classic` and `modern` renderers for presentation-affecting changes
when retail assets and a display are available. For authoritative-tick-only
changes, use the displayless simulation benchmark instead of also running both
windowed renderers. It exercises moving armies and factory construction. Inspect the
feature census and captures as well as frame times; keep artifacts outside the
repository. Run benchmarks sequentially and compare matching scene metadata.

For a change to the authoritative tick alone, the displayless
[simulation-cost benchmark](docs/SIM_BENCHMARK.md) (`tools/sim-bench`) measures
ticks with no window, renderer or audio device: three 250-unit computer armies
fighting on one map, with per-phase attribution, a census that proves the
workload, and CPU and allocation profiles of the measured window. It shares the
same host lock, so it never runs beside the windowed benchmark.

Benchmarks take the benchmark lock one at a time; verification gates take
`tools/host-run --gate` slots and never wait for a benchmark. A focused test,
probe or research sweep takes no lock; never wrap one in `tools/host-run`,
which would stall every agent's benchmark for its whole duration
([ARCHITECTURE §6](docs/ARCHITECTURE.md#6-verification)).

For pathfinding and movement-policy changes, use the opt-in
[path benchmark](docs/PATH_BENCHMARK.md) (`tools/path-bench`): authored
scenarios from single units to 1,500-unit waves under any registered rule set,
with arrival outcomes, per-tick thread CPU time and deterministic hashes, plus
rule sets that switch off one Modern pathfinding policy at a time. Compare
candidates by alternating runs of the two builds on a shared host.
