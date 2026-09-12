[![Nanolathe — Open-source 2.5D RTS engine](https://nanolathe.gg/brand/readme-header.png)](https://nanolathe.gg/)

# Nanolathe

Nanolathe is an independent, open-source **2.5D real-time strategy engine in Go**.
Its current focus is a clean-room reimplementation of Total Annihilation for
single-player skirmish and campaign play, with documented game formats and an
experimental GPU renderer. It is under active development, with incomplete
behavior and compatibility gaps.

[Official website](https://nanolathe.gg/) · [Get started](https://nanolathe.gg/get-started/) · [Documentation](https://nanolathe.gg/docs/) · [Contributors and AI agents](#contributors-and-ai-agents)

The engine reads content from your own local Total Annihilation installation.
Nanolathe's original code is [MIT licensed](LICENSE); the license does not grant
rights to the original game or retail-derived artwork. Retail-derived remaster
exports are excluded from this curated copy. See the
[publication review](docs/PUBLICATION.md) for the history policy and remaining
provenance questions.

## Run

Install Go 1.25 or newer and provide a local retail installation. Linux desktop
builds also need a C toolchain and graphics/audio development headers; the
[Linux dependency script](.github/scripts/install-linux-deps.sh) lists the
packages used by CI on Ubuntu.

```sh
git clone https://github.com/nanolathe-gg/nanolathe.git
cd nanolathe
go build -o nanolathe ./cmd/nanolathe
./nanolathe --root "$HOME/TotalAnnihilation"
```

The default launches the front end with the modern GPU renderer and a 60 FPS
cap. Options → Nanolathe selects Classic / Modern and 30 / 60 / 120 FPS; OK saves
the choices for future runs. F10 switches renderers during a match and saves
that choice. Classic retains its 30 FPS presentation cadence. Use `./nanolathe --help` for map,
rendering, and diagnostic options. The separate `cmd/nanolathe-headless`
command supports displayless simulation runs; see
[architecture and verification](docs/ARCHITECTURE.md).

Desktop fullscreen is available with `--fullscreen`; Alt+Enter toggles it
from menus or battle and saves the preference. Use `--fullscreen=false` to
start windowed regardless of the saved preference. The selected display size
applies to the window throughout menus, loading, battle and results; menu art
scales proportionally from its authored 640×480 canvas. The resolution slider
also offers 1280×720, 1600×900, and 1920×1080. Resolution changes apply when
you confirm Options with OK; dragging the slider leaves the window stable. Fullscreen scales to
the desktop without changing the monitor resolution. On macOS, fullscreen
entered through the green window button must be exited through that native
control.

For music, copy the GOG installation's `music` folder into the same retail
root. Nanolathe plays its MP3 soundtrack on Windows, Linux and macOS; no
conversion is needed. Music starts when a battle begins. Options → Music
controls volume, playback mode and track selection. The main menu retains its
retail ambient loop.

Multiplayer is outside the current scope. For implemented contracts and known
gaps, read the design document for the relevant engine area.

## Contributors and AI agents

Read [AGENTS.md](AGENTS.md) before making changes; it is the authoritative
contribution guide for people and coding agents. Work in an isolated worktree,
preserve concurrent work, and follow its verification and landing rules.

Start with the architecture, invariants, and the design document for the area
you are changing. Follow their research citations before implementing behavior;
record unanswered questions explicitly instead of guessing.

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
and never writes simulation state. Classic presentation samples the committed
tick as published, with no interpolation [03 §2.4] [I6]. The experimental modern
renderer has its own [presentation policy](docs/DESIGN_GPU_RENDERER.md), including
interpolation that leaves authoritative simulation unchanged.

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
