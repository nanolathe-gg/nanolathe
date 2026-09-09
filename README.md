# Nanolathe

Nanolathe is an experimental reimplementation of the Total Annihilation engine
in Go, targeting single-player skirmish and campaign play. It is under active
development, with incomplete behavior and compatibility gaps.

The engine reads content from your own local Total Annihilation installation.
Nanolathe's original code is [MIT licensed](LICENSE); the license does not grant
rights to the original game or retail-derived artwork. Retail-derived remaster exports are excluded from this curated copy. See the
[publication review](docs/PUBLICATION.md) for the history policy and remaining
provenance questions.

## Run

Install Go 1.25 or newer and provide a local retail installation. Linux desktop
builds also need a C toolchain and graphics/audio development headers; the
[Linux dependency script](.github/scripts/install-linux-deps.sh) lists the
packages used by CI on Ubuntu.

```sh
go build -o nanolathe ./cmd/nanolathe
./nanolathe --root "$HOME/TotalAnnihilation"
```

The default launches the classic front end. Use `./nanolathe --help` for map,
rendering, and diagnostic options. The separate `cmd/nanolathe-headless`
command supports displayless simulation runs; see
[architecture and verification](docs/ARCHITECTURE.md).

Multiplayer is outside the current scope. For implemented contracts and known
gaps, read the design document for the relevant engine area.

## Start here

The repository keeps each kind of guidance in one place:

* [`AGENTS.md`](AGENTS.md) — contribution rules, clean-room discipline,
  worktrees, dispatch, review, and landing.
* [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — package map, dependency
  graph, the authoritative tick, what runs today, verification, and the
  citation routing every token in the tree resolves through.
* The eleven design documents, one per engine area — how it is built in Go, its
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
  [`DESIGN_PRESENTATION_CLIENT`](docs/DESIGN_PRESENTATION_CLIENT.md),
  [`DESIGN_GPU_RENDERER`](docs/DESIGN_GPU_RENDERER.md).
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

Save/load uses the retail HAPIBANK account format, including in-battle
restoration. This is still a compatibility work in progress; the
[session design](docs/DESIGN_SESSIONS_AI_SAVE.md) describes the implemented
accounts and remaining gaps.

Exact-retail gaps remain explicit as `TODO(T23)`, `TODO(T25)`, or
`TODO(question)` at their implementation site and under the relevant research
document's Unknown section. Do not replace them with plausible defaults.

## Build and check

With neither `NANOLATHE_RETAIL_ASSETS` nor `NANOLATHE_TA_ROOT` set, these
checks use authored fixtures and skip tests that require retail assets. Desktop
packages require the native build prerequisites above.

```sh
go build ./...
go vet ./...
go test ./...
```

`./tools/check` runs the same checks plus formatting and explicitly disables
retail tests, even if your shell has asset variables set.

For the separate asset-backed integration gate, including retail-tagged tests:

```sh
NANOLATHE_RETAIL_ASSETS="$HOME/TotalAnnihilation" ./tools/check-retail
```

Use an isolated writable `GOCACHE` when the host environment requires it.
Report whether an asset-backed run actually executed when sharing test results.

## Clean-room contribution rule

Committed prose describes what retail does in plain technical language with a
confidence level and a document/section citation. Executable addresses,
decompiler output, generated names, register narration, and raw analysis stay
outside the repository in `$HOME/ta-decompile`. See `AGENTS.md` before changing
research or authoritative behavior.

## About this history

This repository presents curated integration snapshots of the original
private development history. Adjacent changes have been combined in ancestry
order; development-only artifacts and raw-analysis material were removed.
Intermediate snapshots represent work in progress, not individually verified
releases. The final source was checked separately. See the
[publication review](docs/PUBLICATION.md) for scope and limitations.
