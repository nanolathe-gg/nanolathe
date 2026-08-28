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

The runtime has one Ebitengine window path. `internal/session` owns the
authoritative tick and publishes bounded unit/order, projectile, effect,
economy, construction, HUD, and fog state directly into the committed
`internal/frame.Buffer`; `internal/client` reads that current committed frame
and never writes simulation state. Presentation samples the committed tick as
published: there is no interpolation between ticks [03 §2.4] [I6].

Save support is the retail HAPIBANK account format. Campaign-continuation
metadata is exposed, while in-battle restoration returns an explicit
unsupported result until the remaining retail account bodies are implemented;
there is no Nanolathe-authored continuation format.

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
