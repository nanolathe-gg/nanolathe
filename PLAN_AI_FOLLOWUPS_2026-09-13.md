# AI gameplay follow-ups

User authorization: address SC-10, verify SC-11, investigate rough-terrain and
water skirmishes, and exercise recovery after losses. Baseline main `d23e1693`;
its post-landing fast and retail gates passed in the previous task.

## Ownership and public contracts

- Root (`fix/ai-followups`): AI placement and matching fixtures, shared design
  and research documents, terrain probes, this task state, review and landing.
- Menu worker (`fix/ai-menu`): selection.go and selection tests only. Invalid
  menu products must not abort remaining candidates; a valid catalog type with
  missing initialized strategic data remains an error. Existing selector API,
  candidate order, side filtering and RNG contracts remain.
- Recovery worker (`audit/ai-recovery`): temporary authored probe only, reporting
  normal death-path perturbations and subsequent queue/build/attack activity.
  Requests exclusive source ownership if investigation proves a defect.

The shared docs have one owner. No behavior is tuned to force a game outcome.
Static retail evidence owns behavior; bounded host policy for malformed content
must remain explicitly identified. Probe source and outputs stay outside the
repository under `/private/tmp/nanolathe-ai-followups-artifacts`.

## Work and verification

All four investigations are complete; the two confirmed mod-content fixes are
implemented and independently reviewed. Terrain and recovery probes found no
additional confirmed implementation defect in the exercised cases.

### Mod content

SC-10 skips unresolved catalog-bound menu products individually, before profile
or vector reads and without a reservoir draw. The authored compile-to-selection
fixture retains valid later candidates, excludes unresolved download additions,
and preserves the separate failure for a valid type lacking strategic state.
Existing [08 R-AI-02 §2] and [02 R-CAT-01 §5] already establish invalid-type
rejection; no research correction was needed.

SC-11 removes AI's extra empty-yard refusal. An actual compiled authored archive
fixture exercises absent, empty and whitespace yard text through scatter and
extractor selection. Each behaves like the occupied remainder used by the
shared parser. [fmt fbi] already identifies this as bounded host policy and
retail's exhausted-source result as Unknown. World design W10 incorrectly called
it an all-open retail default; that wording is corrected. No stock definition's
behavior is intentionally changed.

Both new regressions fail against their original production implementations and
pass with the fixes. Worker/root and independent reviewer fast/retail gates
passed; the combined branch's fast/retail gates also passed.

### Terrain scenarios

Ordinary medium-difficulty skirmishes, seeds one and seven, began with a
54,000-tick diagnostic bound. The four nonterminal cases were extended to
108,000 ticks. All eight original-bound full headless reports are identical
before and after the mod fixes, including RNG and fingerprints.

| Map | Seed 1 AI victory tick | Seed 7 AI victory tick |
|---|---:|---:|
| Acid Foursome | 36,570 | 47,190 |
| Aqua Verdigris | 34,650 | 45,510 |
| Brilliant Cut Lake | 90,090 | 84,330 |
| Two Continents | Not terminal by 108,000 | Not terminal by 108,000 |

Two Continents is a characterized limitation, not a fixed map. At seed seven,
hovercraft have active routes to the enemy vicinity and move west. At tick
108,300 the wave merge sheds four advancing hovercraft into regroup; their
attack orders are replaced by moves east toward the remaining land-dominated
wave. Subsequent positions confirm the return motion. This follows the
established distance-based merge and paired regroup [08 R-P0-04 §3]
[08 R-AI-01 §5]. No transport or separate hover tactic is invented.

Seed seven's four aircraft follow the established small-group local patrol.
Seed one's earlier air attack submissions were outside the captured execution
windows; snapshots cannot establish their entire damage history. No specific
air-execution mismatch was identified. A future investigation would need a
per-tick queue/weapon/projectile trace around its first air attack, rather than
assuming that a final full-health commander was never hit.

### Recovery scenarios

Six fresh Great Divide medium seed-seven runs: baseline plus five interventions
through normal Combat.AcceptDamage and next-tick death/cleanup paths. The
commander and some recovery capability were retained. All five cases resumed
ordinary production and later attacked; these are observations, not a universal
recovery requirement.

| Intervention | Observed recovery | First attack tick |
|---|---|---:|
| Completed factory loss at 12,000 | Replacement lab completes 17,875 | 38,100 |
| Constructor loss at 12,000 | New vehicle constructor 16,115 and kbot constructor 16,365 | 24,300 |
| Thirteen income structures lost at 12,000 | New extractor 26,010, solar 32,880, wind 34,240 | 52,500 |
| Commander damaged during active build at 6,340 | Stops, respects deadline 6,650, new build order 6,840 | 23,700 |
| Unfinished factory lost at 12,000 | Builder reselects at 12,150, replacement factory completes 15,940 | 21,900 |

The baseline first attack/victory 44,400/48,150 matches the preceding task. No
new recovery policy or production priorities were added. Exact reports, commands,
probe sources and event logs live in the external terrain and recovery artifact
directories. Performance and post-landing verification are recorded below.


### Performance and landing

The sequential authoritative-tick benchmark has matching setup metadata,
catalog, initial/warm/final partial fingerprints, both RNG totals and every
census row. Median tick time was 1.281 → 1.266 ms, p95 2.009 → 2.032 ms and
maximum 10.347 → 8.175 ms. Both close with 433 live units, 232 moving units,
10 builds and 6,343 features. This is the documented simulation-cost gate for
an authoritative-tick-only change; presentation code is unchanged.

Root reviewed both production diffs, fixture sources and research contracts,
including candidate limits, reservoir draw order and placement helper/radius
ordering. Clean-room and invariant scans found no added violations. The external
`SUMMARY.md` links the gate, scenario and benchmark logs; post-landing results
use `post-check.log` and `post-retail.log`. Cleanup is limited to the three
worktrees created for this task and their corresponding branches.
