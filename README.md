# Nanolathe

Nanolathe is an MIT-licensed, clean-room reimplementation of the Total
Annihilation engine for single-player skirmish and campaign play. Original game
assets are mounted at runtime and are never committed to this repository.

## Start here

The repository keeps each kind of guidance in one place:

* [`AGENTS.md`](AGENTS.md) — contribution rules, clean-room discipline,
  worktrees, dispatch, review, and landing.
* [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — package map, dependency
  graph, the authoritative tick, what runs today, verification, and the
  citation routing every token in the tree resolves through.
* The ten design documents, one per engine area — how it is built in Go, its
  contracts, and which research owns each behavior:
  [`DESIGN_RUNTIME_DETERMINISM`](docs/DESIGN_RUNTIME_DETERMINISM.md),
  [`DESIGN_CONTENT_VFS`](docs/DESIGN_CONTENT_VFS.md),
  [`DESIGN_WORLD_VISIBILITY`](docs/DESIGN_WORLD_VISIBILITY.md),
  [`DESIGN_UNITS_ORDERS_COB`](docs/DESIGN_UNITS_ORDERS_COB.md),
  [`DESIGN_MOVEMENT_PATH`](docs/DESIGN_MOVEMENT_PATH.md),
  [`DESIGN_ECONOMY_CONSTRUCTION`](docs/DESIGN_ECONOMY_CONSTRUCTION.md),
  [`DESIGN_WEAPONS_PROJECTILES`](docs/DESIGN_WEAPONS_PROJECTILES.md),
  [`DESIGN_INTERFACE_HUD_INPUT`](docs/DESIGN_INTERFACE_HUD_INPUT.md),
  [`DESIGN_SESSIONS_AI_SAVE`](docs/DESIGN_SESSIONS_AI_SAVE.md),
  [`DESIGN_PRESENTATION_CLIENT`](docs/DESIGN_PRESENTATION_CLIENT.md).
* [`docs/INVARIANTS.md`](docs/INVARIANTS.md) — cross-cutting implementation
  rules every change must preserve.
* [`docs/SPEC_CONFLICTS.md`](docs/SPEC_CONFLICTS.md) — audited cases where a
  retail install corrected an older written contract.
* [`docs/REMASTER.md`](docs/REMASTER.md) — the remaster authoring kit and the
  art override the `--remaster` flag mounts.
* [`research/retail-executable-spec/README.md`](research/retail-executable-spec/README.md)
  — research reading order, category index, evidence language, deciders,
  writing rules, citation convention, and gap disposition.
* [`research/formats/README.md`](research/formats/README.md) — file-format
  reference. Format documents own byte layout; the executable specification
  owns runtime behavior.

A design document is a description of the build, not a checklist: it states
the contracts the packages implement and, in its last section, what is not
implemented and what is still open. Research states what retail does; the
design documents state how this tree does it.

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
outside the repository in `$HOME/ta-decompile`. See `AGENTS.md` before changing
research or authoritative behavior.
