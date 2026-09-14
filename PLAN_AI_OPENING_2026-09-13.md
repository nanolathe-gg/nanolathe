# AI opening investigation

User authorization: investigate and fix the gameplay AI issues behind the slow
Great Divide opening. Starting main: `42d5e1c2`. This is implementation task
state, not evidence of retail behavior.

## Ownership and contracts

Root owns `internal/ai/manager.go`, `internal/ai/groups.go`, the matching group
tests, the owning category 08 research correction, and this plan. The wave audit
agent reads these files and the private static corpus without modifying the
repository. Existing manager, group-writer, order, and economy APIs remain.

The wave audit establishes the post-transfer coordinate calculation in
[08 R-P0-04 §3]. Retain signed 32-bit sums across shedding, subtract the member
occupying the removed slot after swap-delete, and divide by the new count even
when it is one. Peer admission holds that centroid and count fixed. Signed
distance products and sums retain their 32-bit width. No threshold or profile
weight is tuned to improve the diagnostic outcome.

## Findings

Normal headless skirmishes use stock assets, medium difficulty, and both seeds
set to seven. Ashap Plateau wins at tick 34,650. Great Divide has no attack by
tick 36,000, but extending the same run wins at tick 48,330 (26m51s). These are
single reproducible scenarios, not a claim that every seed or map completes.

The stopped Great Divide session at tick 36,000 has four ground wave members,
zero regroup members, and a clear engagement latch. The established first-wave
threshold is six. Its army consists of an AK, Weasel, Storm and Crasher; most
other created units are economy, constructors and base defenses. Stock is 5.60
metal against the established 25-metal candidate gate. At tick 24,000 it is
12.73 metal, and at tick 30,000 it is 163.27 with negative net metal income.
The resource-pressure score can reserve the entire candidate mix for economy.
These observations establish a slow, resource-constrained opening, not a
permanent factory or attack latch failure. The regrouping mismatch is a separate
confirmed defect and is not established as the cause of this opening's delay.

Snapshotting at host-pump boundaries preserved the exact original tick-36,000
partial fingerprint. Diagnostic snapshots, authored probe source, logs and
performance artifacts are kept outside the repository under
`/private/tmp/nanolathe-ai-opening-artifacts`.

## Verification

Baseline `tools/check` and `tools/check-retail` passed. Focused regression cases
cover a non-final removal that changes reinforcement admission, a final removal,
the division after shrinking to one member, and signed distance overflow.
Independent review found no issues and ran both required gates successfully.
Root's integrated `tools/check` and `tools/check-retail` also passed. The new
regression fails against baseline source in the non-final removal, single
survivor and signed-distance cases, then passes against the corrected source.

Both normal skirmishes complete after the fix. Great Divide first attacks at
44,400 both before and after; victory changes from 48,330 to 48,150. Ashap Plateau
first attacks at 27,300 both before and after; victory changes from 34,650 to
37,530. The correction changes later troop behavior, with no general claim of
stronger or faster AI.

The sequential simulation-cost benchmark passed with matching scene metadata,
catalog, initial state and every census row. Median tick time was 1.378 → 1.439
ms, p95 2.169 → 2.192 ms, maximum 8.539 → 8.390 ms. At window close both runs
have 433 live units, 232 moving units, 185 attack orders, 10 builds and 6,343
features. This is the authoritative-tick-only performance gate; no presentation
code changes. All post-landing gate results are recorded in the external
`post-check.log` and `post-retail.log`. Cleanup is limited to this branch and
worktree.
