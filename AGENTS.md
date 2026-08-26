# Nanolathe — Agent Instructions

You are building **Nanolathe**, an MIT-licensed reimplementation of the Total
Annihilation engine. Retail behavior comes from clean-room reverse engineering
of the retail `TotalA.exe`; game content comes from the original assets
(HPI archives, TDF text, GAF/TNT/3DO graphics, SHD palettes, WAV/OGG audio).
Everything is data-driven — never invent data or behavior the assets or the
retail engine already define.

---

## The four rules

These override everything else. If a rule conflicts with a plan, a work unit,
or your own judgement, the rule wins.

**1. Never invent behavior.** Do not invent data or behavior that is not
defined by the retail executable or the original assets. When a question is
unresolved: leave `TODO(question): <what is unknown, and what would settle it>`
at the exact site in code, record it in the relevant research doc, and say so
in your report. An honest gap is a finished work unit; a plausible guess is a
defect that outlives you — it is indistinguishable from a traced contract once
merged. Anything recorded as **Supported inference** is a standing invitation
to trace it; if your work depends on one, verify it first (inferences here
have been found inverted, not merely imprecise).

**2. Never undo another agent's work.** Never `git reset`, rebase, revert,
amend, or force-push commits on `main`; never discard, stash, or check out over
another agent's uncommitted changes in any worktree. Fix wrong things
**forward** with a new commit that explains what changed and why. If you find a
dirty working tree that is not yours, leave it alone and say so. Work is
concurrent: treat every file you did not write this session as another agent's
live work.

**3. Clone the executable's behavior, don't copy its code.** Understanding
retail requires disassembly; shipping retail's expression of that understanding
does not. Raw analysis — disassembly, decompiler output, addresses, register
traces, generated symbol names — lives only in `/tmp/ta-decompile`, never in
the repo. Clean-room description — what the algorithm *does*, in your own
words — is what enters `research/`, code comments, and commit messages. You
must perform the translation step.

**4. Everyone works in a worktree.** Every agent does its work in its own git
worktree, never in the shared `main` checkout. See **Worktrees** below.

---

## Clean-room discipline

Applies to everything committed: `research/`, code comments, commit messages,
test names, reports.

**Never commit:** memory addresses or offsets into the executable (a bare
`004NNNNN`, a global at `0x5NNNNN`, `def+0xNNN`); decompiler-generated names
(`FUN_004NNNNN`, `DAT_005NNNNN`, `local_N`, `uVarN`, `iVarN`); disassembly or
decompiler output in any quantity; register-level narration; structure field
offsets expressed as executable layout.

**Do write:** what the algorithm computes, in plain technical language, with
the arithmetic spelled out in terms Nanolathe can implement; named concepts
instead of addresses (*the raw-frame blitter*, *the LOS height word builder*);
data layout in terms of the **file format** (`research/formats` owns byte
offsets in files — that is authored data, not executable code); a confidence
level on every claim — **Established**, **Supported inference**, or
**Unknown**.

If you cannot describe a behavior without an address, you have not finished
understanding it. Keep the address-level trail in `/tmp/ta-decompile/notes/`
so a later agent can re-derive the finding without re-doing the search.

---

## Research

`research/` is a curated reference, not a notebook.

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

**Adding a finding:** edit the owning category doc in place — the default and
the only option. A correction must state what the previous text said and why
it was wrong, so the reversal is auditable. Closed gap findings are written
inline under a heading carrying the `R-<id>` anchor code cites (old tokens like
`[R-P0-01]`, `[04 §5.4]`, `[GAP T15]` must stay findable). File-format details
go in `research/formats/<format>.md`. Never create new directories or notes
files. There are **no committed gap-analysis files**: open questions live as
`TODO(T23)` / `TODO(T25)` / `TODO(question)` markers in code and as **Unknown**
items in the category docs' "Missing and unknown" lists.

**Citations.** By document and section, never by line number: `[04 §7.2]` for
numbered docs, `[05 "Two-stage settlement algorithm"]` for docs 05 and 08
(unnumbered headings), `[fmt tnt]` for a format doc, `[R-P0-18-A §1]` for an
inline addendum section whose heading retains that exact anchor.

**Precedence:** `research/retail-executable-spec` wins for **behavior**;
`research/formats` wins for **byte layout**. They do not compete.

**Other reference docs** (these describe *our* build, not retail):

- `PHASES.md` + `docs/PLAN_*.md` — package layout, phase graph, contracts,
  exit criteria. They point at research; they do not restate arithmetic.
- `docs/INVARIANTS.md` — the fourteen rules every diff is reviewed against.
- `docs/SPEC_CONFLICTS.md` — where the reference install disproves the spec.
  Read before "fixing" anything it lists.
- `docs/WORK_UNITS.md` — flat dispatch index of every work unit.

---

## Worktrees

Create one before you touch a file, and do all work there:

```
git worktree add ../nanolathe-wt-<task-slug> -b <task-slug> main
```

Commit as you go — uncommitted work in a shared checkout has been destroyed by
concurrent agents before. Land work **as it finishes**: get your worktree green
(`go build ./...`, `go vet`, `go test ./...`), merge `main` into your branch
and resolve conflicts there (never resolve by discarding the other side — if
you cannot reconcile a conflict, stop and report), then re-verify. If `main`
moved, repeat. A top-level maintainer may then merge the reviewed branch into
`main`; a dispatched sub-agent stops after committing its verified branch and
reporting it, and never merges its own work.

Remove your worktree when it lands (`git worktree remove`, `git branch -d`,
`git worktree prune`). Do **not** bulk-remove worktrees you did not create —
an unmerged one may be another agent's live work.

---

## Dispatching and reviewing sub-agents

Delegate anything non-trivial — research sweeps, implementation units, reviews.
One sub-agent owns one unit and one set of files; the plan's Public API block
is the contract between units. Every sub-agent works in its own worktree.
Dispatch only when the unit's dependencies and earlier phase gates are green.
`PHASES.md` owns package boundaries and phase dependencies;
`docs/WORK_UNITS.md` owns safe parallel groups and required serialization.

A dispatch brief is exactly this shape:

```
Implement WU-07-3 from docs/PLAN_07_MOVEMENT_PATHFINDING.md.

Read first: AGENTS.md, docs/INVARIANTS.md, docs/PLAN_07_MOVEMENT_PATHFINDING.md,
then research sections [04 §7.2] and [04 §7.3].

Worktree: .claude/worktrees/wu-07-3 on branch wu-07-3, branched from main.
Files you own (create/modify only these): internal/path/search.go, internal/path/search_test.go
Files you may read but must not modify: internal/world/*, internal/movement/profile.go
Public API you must satisfy: the Search block in the plan's "Public API" section.
Done when: contracts C4-C9 hold, `go test ./internal/path` passes, `go vet ./...` clean.
Unknowns: if research does not answer a question, stop and report it — do not invent a constant.
```

Do **not** paste research documents into a sub-agent prompt — cite sections
and let it read them. Rules that make parallel dispatch safe: **exclusive file
ownership** (two units never list the same file); **API first** (a unit may add
to its own package's API, never change another package's); **no cross-package
refactors** (report upstream needs to the orchestrator); **one unit, one
commit**.

**Review before merge.** Sub-agent output is not trusted by default; you are
accountable for what you merge. The reviewer is the only thing standing between
an invented constant and permanent, plausible-looking wrongness:

1. Read the diff, not the summary (`git diff main...HEAD`). Sub-agents report
   confidently about code that does not compile.
2. Run the checks yourself: `go build ./... && go vet ./... && gofmt -l . &&
   go test ./...`, plus the plan's gate command. Re-run the gate **after**
   merging, not only in the worktree.
3. Verify two of the most arithmetic-heavy contracts against their cited
   research section — constants, order of operations, comparison strictness.
   This is where wrong constants get caught.
4. Grep the diff for clean-room violations before merging — offending text
   reads like rigour.
5. If it is visual, look at it: `--shot` renders headless; a screenshot is
   evidence, an assertion that it should look right is not.
6. Feed corrections back into the **same** agent — it still has the context.
   Name the contract, the citation, and the observed behavior. Two rounds of
   feedback is normal; a third means the unit was scoped wrong — split it.
7. Check against `docs/INVARIANTS.md`: no `map` range in sim-visible paths, no
   `float64` outside the I2 allowlist, no `time.Now()` in sim packages, no new
   module dependencies, unknowns are `TODO(...)` markers, research the unit
   added follows "Adding a finding" above.

**When a unit lands badly:** fix it **forward** with a new commit that says
what changed and why. Never revert/reset/rebase/amend on `main`. If the whole
unit needs to come out, stop and escalate rather than deciding that yourself.

**Test policy.** Keep tests light and fast; skip when retail assets are absent.
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
genuine gap, analyze the executable in `/tmp/ta-decompile`, then write the
finding up clean-room in the owning doc — the translation step is part of the
work, not a formatting chore. Otherwise stop and report. Inventing a constant
is the one unrecoverable failure mode.

---

## Stack, scope, style

- Modern Go, standard library first. Rendering/graphics/audio/window:
  [ebitengine](https://github.com/hajimehoshi/ebiten). Original assets live in
  `~/TotalAnnihilation` — use for development/testing, never commit.
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

## Architecture (summary, see PHASES.md)

```
vfs        — overlay of loose dirs + HPI-family archives (HAPI, cipher, SQSH)
formats    — lossless parsers (TDF blanking comment offsets, GAF/TNT/3DO reloc)
content    — compiled catalogs (units/weapons/features/movement/side/sound/maps) with defaults+conversions
clock, rng, pool, kernel — 30 Hz tick, budget clamp 0..5, phase graph, fixed pools (slot 0=null)
world (terrain, features, occupancy) → visibility → units+orders+cob → movement → economy → combat → ai → snapshot
client     — Ebitengine window loop presents a software framebuffer, interpolates Previous→Current at render fraction, palette/SHD lookup, fog presentation separate from LOS mask
```

## Workflow

- Work from `PHASES.md`: phase table, dependency graph, exit criteria, playable
  gates. Do not implement a later phase before its dependencies are green.
- Reference `research/retail-executable-spec/README.md` for the reading order.
- There is **no Oracle** in this repo. For retail validation use
  `~/TotalAnnihilation` assets and a manual retail install if needed, but do
  not automate the retail executable. Future probes live under `probes/` as
  independently authored, data-driven scenarios.
- Verify visually when the change is visual. A screenshot from `--shot` beats
  an assertion that it should look right.
- Use `go vet` / `go test ./...` lightly. Do not add heavy test harnesses.
