# Nanolathe

Nanolathe is an MIT-licensed, clean-room reimplementation of the Total
Annihilation engine for single-player skirmish and campaign play. Original game
assets are mounted at runtime and are never committed to this repository.

## Start here

The repository keeps each kind of guidance in one place:

* [`AGENTS.md`](AGENTS.md) — contribution rules, clean-room discipline,
  worktrees, dispatch, review, and landing.
* [`PHASES.md`](PHASES.md) — build dependency graph, package ownership, and
  falsifiable phase gates.
* [`docs/WORK_UNITS.md`](docs/WORK_UNITS.md) — flat dispatch index and safe
  parallel/serialized groups. The owning phase plan remains authoritative.
* [`docs/INVARIANTS.md`](docs/INVARIANTS.md) — cross-cutting implementation
  rules every change must preserve.
* [`docs/SPEC_CONFLICTS.md`](docs/SPEC_CONFLICTS.md) — audited cases where a
  retail install corrected an older written contract.
* [`research/retail-executable-spec/README.md`](research/retail-executable-spec/README.md)
  — research reading order, category index, citation convention, and gap
  disposition.
* [`research/formats/README.md`](research/formats/README.md) — file-format
  reference. Format documents own byte layout; the executable specification
  owns runtime behavior.

`docs/PLAN_*.md` files are active implementation contracts and gate checklists.
An unchecked box identifies a verification requirement; it does not by itself
prove that the underlying code is absent. Dispatch follows the dependencies
and prior gates named in `PHASES.md`.

## Current boundary

The repository has one Ebitengine presentation path and a headless authoritative
session. The session publishes bounded unit/order, projectile, effect, economy,
construction, HUD, and fog state in one committed frame; presentation reads
that frame and never writes simulation state. Windowed checks require working
display services and a local retail asset root; headless checks remain useful
for deterministic behavior and data loading but do not exercise the window.

Exact-retail gaps remain explicit as `TODO(T23)`, `TODO(T25)`, or
`TODO(question)` at their implementation site and under the relevant research
document's Unknown section. Do not replace them with plausible defaults.

## Build and check

Go and the original assets are the only development inputs. Point
`NANOLATHE_TA_ROOT` at a local retail installation for asset-backed tests.

```sh
go build ./...
go vet ./...
go test ./...
```

Use an isolated writable `GOCACHE` when the host environment requires it. Some
tests intentionally skip without retail assets; a release or gate report must
state whether an asset-backed run actually executed.

## Clean-room contribution rule

Committed prose describes what retail does in plain technical language with a
confidence level and a document/section citation. Executable addresses,
decompiler output, generated names, register narration, and raw analysis stay
outside the repository in `/tmp/ta-decompile`. See `AGENTS.md` before changing
research or authoritative behavior.
