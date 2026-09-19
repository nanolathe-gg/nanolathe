# Non-retail extension reference

## Scope and index

This directory is the authorized home for independently worded contracts for
non-retail patches and extensions. It is a curated reference, not a notebook,
a list of desired features, or evidence about retail Total Annihilation.

Add one owning document per extension or coherent extension family when there
is sourced research to record, and link it from this index with its subject
and source version. Update that document in place as evidence improves; do
not add session notes or gap-analysis files. Each contract belongs under a
stable named heading and keeps its unresolved questions beside it.

| Reference | Scope and source version |
|---|---|
| [Extended build menus](build-menus.md) | TA Zero Alpha 5 authored placements, repeated membership records, and documented twelve-slot sidebar; patch membership semantics remain unknown. |
| [Non-retail weapon target keys](weapon-target-keys.md) | `nottoair`, `nottounderwater` and `surfacefire` as authored and described by TA: Escalation Gold 10.2.0, and `toaironly` as authored by ProTA 4.8; TA Zero Alpha 5 uses none. Documented intent and authored census only — no patch admission algorithm is established, and `toaironly` remains unknown. |

## Evidence policy

For each behavioral claim, record the extension name, exact source version or
revision, primary source URL or reproducible artifact identity, relevant
section or symbol, evidence scope and confidence. Do not treat one release's
contract as proof about every release. Conflicting versions remain distinct
until evidence resolves their applicability.

Primary patch documentation, authored content and appropriately licensed
source may support an extension claim. Check source provenance and license
before using source material; this project remains MIT-only and excludes GPL
code. Describe algorithms independently rather than copying or translating
another implementation. Source availability does not waive clean-room rules.
Documentation establishes what a release documents; content establishes what
it authors; source establishes what the inspected implementation does. None
by itself establishes behavior outside that scope, or retail behavior.

Do not disassemble or decompile third-party patches. Extension investigation
uses the sources above and bounded manual observations; the retail static
analysis workflow does not extend to patch binaries.

Use these confidence labels on every claim:

- **Established** — a cited primary source directly supports the bounded
  claim for the identified version. State whether the evidence is documented,
  authored or implemented behavior, and retain any limits of that evidence.
- **Supported inference** — evidence favors the claim but leaves a caller,
  condition, arithmetic step or version boundary unresolved. Name the missing
  evidence and verify it before implementing dependent behavior.
- **Unknown** — evidence cannot yet settle the contract. Name the question and
  what would settle it; do not supply a plausible mechanic or constant.

Secondary summaries and community recollections can direct investigation;
they do not establish arithmetic or close a contract. A manual observation
must record its setup, extension version and observed result, without claiming
more than that observation supports. Do not automate the retail executable.

All committed material follows [AGENTS.md](../../AGENTS.md). Executable
addresses, disassembly, decompiler output, generated symbols and executable
structure layouts must not enter this reference. Keeping such material outside
the repository is not permission to analyze third-party patch binaries.
File-format layouts belong in
[research/formats](../formats/README.md), with their version boundaries stated.
No patch binaries, copied retail assets or third-party implementation code
belong in this directory.

## From evidence to implementation

Keep retail baseline citations in
[retail-executable-spec](../retail-executable-spec/README.md); extension sources
must never be used to fill a retail gap or relabel non-retail behavior as
historical retail behavior. Cite extension documents by name and heading,
with a Markdown link, rather than using retail section identifiers.

Research does not approve adoption. An explicitly approved Nanolathe Modern
policy belongs in its owning `docs/DESIGN_*.md`, with the strict baseline,
extension evidence, intended Nanolathe behavior, boundaries and tests stated
separately. Follow [DESIGN_GAMEPLAY_RULES §9](../../docs/DESIGN_GAMEPLAY_RULES.md#9-extending-the-existing-mechanism)
to use or extend the existing rules interfaces and registry. Load-time content
profiles remain separate from gameplay selection. Strict 3.1 must bypass an
approved departure, with its RNG and resource behavior preserved. Existing
user authorization carries forward: implementing and extending interfaces for
an already authorized mechanic does not require approval again.

If evidence is missing, keep an **Unknown** in the owning extension document
and a `TODO(question)` at any dependent code site, and report what would settle
it. Authorization to research extensions does not authorize new mechanics,
a new selection framework, stateful rule objects, or save-format changes.
