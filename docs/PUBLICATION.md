# Curated publication candidate

Prepared 2026-09-08 in an independent repository copy. The original development
repository and its history were not rewritten. No remote is configured and
nothing has been published.

## History

The original 5,903 commits were grouped into adjacent integration snapshots
along their first-parent ancestry. The original root, bootstrap, early phase
boundaries, date changes and nearby subsystem transitions guided the groups;
no group contains more than ten first-parent steps. Redundant sanitized
snapshots were omitted. Publication follow-ups update the documentation and make a test helper
portable, giving 293 commits in this curated candidate.

The original merge graph sometimes follows a topic branch before merging other
work. These commits are curated integration snapshots, not a reconstruction of
an original linear mainline. Authors and source endpoint author dates are
retained; the curator's committer identity and date identify the rewrite.
Intermediate snapshots are not individually certified releases. Their commit
messages describe the aggregate areas and selected constituent changes.

A private audit outside this repository records source-to-curated mappings,
path exclusions and affected text versions. It is not part of the public Git
history. Do not merge original development refs into this repository.

## Removed throughout the retained history

- Generated retail-derived remaster textures and model OBJ/MTL/JSON exports;
  extracted source art, previews and packed outputs. Recipes and notes remain.
- Accidentally tracked build outputs, including the headless engine and map
  upscaler executables.
- Transient reviews, plans, orchestration reports, scratch diagnostics and
  superseded standalone research notes whose successors are the category docs.
- Raw-analysis comments and prose identified by the review filters, including
  executable-address/generated-symbol forms, register narration and runtime
  layout descriptions. Historical analysis strings in Go metadata and test
  diagnostics were redacted as well. Explicit omission markers preserve the fact that the
  removed material has not been replaced by independently worded evidence.
- Retail byte-dump examples and stock-derived COB/BOS script examples in the
  format references. Authored file schemas are retained in the final reference. The latest Go
  production executable tokens are unchanged from the original source endpoint.
  Two gadget-field comments were clarified, and a source-checking test helper
  now locates the repository through its module file rather than a personal path.

These removals concern publication scope. They are not claims that every
excluded file infringed a right. The retained original source is MIT licensed;
the license does not grant rights in retail inputs or derivatives.

## Remaining limits

- Community-format and “OpenTA” attributions remain unresolved. They were
  preserved rather than silently renamed. Author confirmation or source review
  is still needed to establish independent authorship or redistribution rights.
- Pattern-based redaction and targeted review cannot prove absence of copied
  expression or every possible raw-analysis form. Omitted passages are explicit
  documentation gaps, not newly researched behavioral conclusions.
- The retained authored fixtures and generators were distinguished from retail
  exports. No exhaustive external-code similarity comparison was performed.
- Binary releases need the actual dependency license texts and bundled native
  component notices described in [NOTICE.md](../NOTICE.md).
- Rewriting this copy cannot remove objects from the original repository,
  existing clones, hosting caches or other people's copies.

## Validation

The preparation audit compares retained latest Go sources with the original
source endpoint, checks excluded-path reachability and scans retained blobs and
messages. Formatting, `go build ./...`, `go vet ./...` and `go test ./...` passed on
macOS arm64 with Go 1.25.0. Snapshots 80 and 200 also passed `go build ./...`;
other intermediate snapshots were not individually tested. Detailed outcomes
are recorded in the accompanying private audit report. No retail-asset or live
battle benchmark result is implied by ordinary build and unit-test checks.
