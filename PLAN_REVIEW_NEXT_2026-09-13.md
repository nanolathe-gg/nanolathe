# Remaining gameplay review queue

User authorization: address CV-03, UO-02, WP-07 and PC-08. Starting main:
`ea82acdb`. This file records task state, not retail evidence.

## Ownership and public contracts

- Root (`fix/review-next`): script catalog admission, preflight/creation/restore
  verification, standing-order COB ports, content and units design documents,
  owning research in categories 02/04, this plan, integration and landing.
- Stockpile (`fix/review-stockpile`): completion scheduling investigation;
  `internal/combat/stockpile.go`, its tests, `internal/orders/zbuildweapon.go`
  and its tests if needed; weapons design and category 06 research. Keep the
  existing TickStockpile return interface and economy admission ownership.
- Wreck (`fix/review-wreck`): stock LOGOS face census and pseudo-unit selector
  investigation; feature model rendering and tests in internal/client;
  presentation design and category 03 research. Keep frame and renderer APIs;
  report any upstream ownership need before editing outside the assigned files.

Independent workers start after baseline gates. Research must establish the
stockpile completion return and feature colour selector before implementation.
If direct static investigation cannot settle a question, record the exact
Unknown and its decider at the code and owning research sites; never guess.

CV-03 retains uncreatable definitions with actionable catalog warnings. A
required missing script refuses preflight or creation/restore before allocating
a live unit. Existing valid compiled programs and their provenance remain
immutable. This is a user-authorized change to the host refusal boundary, not a
claim that retail creates functioning scriptless units. UO-02 reads the current
two-bit stance fields; port writes retain their existing marker-only behavior.

## Verification and progress

Baseline fast/retail gates passed. All four changes are implemented and reviewed:

- CV-03: missing or unusable programs retain warnings and winning provenance;
  required preflight and all creation variants refuse them. Authored catalog
  fixtures verify refusal before allocation/RNG, and a broken Core commander
  override leaves Arm preflight usable while refusing Core. Review also closed
  empty-code preflight admission.
- UO-02: real COB reads see current two-bit standing move/fire fields. Writes
  keep the existing script-touched marker without changing either field.
- WP-07: direct static evidence establishes immediate completion restart at all
  build times. Tests exercise next-round costs, rejected admission, completed
  progress held at the ammunition cap, and the live secondary pump. Normal
  tick-end saves cannot capture unsettled completed progress between phases.
- PC-08: direct static evidence establishes player slot zero's current colour
  for feature LOGOS faces. Both recording paths use that committed row. The
  stock census found three affected quads on `corkrog_dead`. Paired classic and
  real Metal captures show restored panels at four headings; missing/out-of-range
  selectors remain absent, and modern retains no software image.

Each worker's fast/retail gates passed again under independent root review.
The combined branch includes concurrent main `3b6768cf` (menu-only changes),
with integrated fast/retail/device gates passed at `cb11f72f`. Baseline binaries
were rebuilt at that main revision. Six sequential benchmark runs passed:

| Milliseconds, baseline → candidate | Median | p95 | Maximum |
|---|---:|---:|---:|
| Simulation tick | 1.633 → 1.653 | 2.735 → 2.872 | 9.716 → 12.720 |
| Classic battle draw work | 15.958 → 16.025 | 18.736 → 18.726 | 19.464 → 20.147 |
| Modern battle draw work | 10.520 → 10.406 | 11.382 → 11.561 | 11.880 → 21.010 |

The simulation's setup, catalog, partial fingerprints, RNG totals and complete
census match. Both native battle pairs have identical scene metadata, every
gameplay census row and final captures. Captures were inspected; eight active
factories, 178–204 moving units, 2,542–2,546 features and 9–19 burning features
prove the workload. Modern reports no overflow. Median/p95 costs remain similar;
one candidate modern draw has a longer maximum, whose cause is not established
by this paired sample. The stock wreck correction has separate focused captures.
Artifacts live under `/private/tmp/nanolathe-review-next-verification` and
`/private/tmp/nanolathe-review-wreck-artifacts`; no retail bytes are committed.
The local verification SUMMARY.md records exact commands, hashes and metadata.
Landing verification uses `/private/tmp/nanolathe-review-next-post-check.log`,
`-post-retail.log` and `-post-device.log`. Cleanup is limited to these three
worktrees and branches; foreign worktrees remain untouched.

## Divergences

The authorized CV-03 host boundary preserves unusable definitions for discovery
and diagnoses required use instead of reproducing retail's creation-time fault.
It never substitutes an empty VM. The owning policy is DESIGN_CONTENT_VFS §3.4
C9 and DESIGN_UNITS_ORDERS_COB §2.1. PC-08 retains the existing absent-input
omission when no committed player-zero row exists; it invents no retail colour.
