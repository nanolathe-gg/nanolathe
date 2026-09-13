# Gameplay review follow-up

User authorization: complete PC-03, PC-09, FE-10, PC-01, PC-11 and CV-01/02.
Starting main: `eb2a333`. This is task state, not retail evidence.

## Units and ownership

| Unit | Worktree branch | Status |
|---|---|---|
| Download candidate rejection CV-01/02 | fix/gameplay-content | integrated; worker and independent fast/retail gates passed |
| Audio cadence and music PC-03/09 | fix/gameplay-audio | integrated b0afaa91; worker and independent fast/retail gates passed |
| Beam palette and mind-gun lens PC-01/11 | fix/gameplay-visual | integrated 8f889621; worker and independent fast/retail/Metal gates passed |
| Skirmish difficulty coupling FE-10 | fix/gameplay-20260913 | committed 02b341b and 2b5b5627; screen-entry and callback regressions passed |

Root owns this plan and shared research/design documents. Workers have exclusive
source ownership recorded in their dispatches; upstream changes require an
explicit ownership extension. All edits happen in isolated worktrees.

## Public contracts

- Archive discovery rejects only established invalid candidates, retains
  provenance diagnostics, and continues later candidates. Explicit archive
  mounting and required payload failures retain their error behavior.
- Map catalog discovery distinguishes missing browser metadata from fatal
  syntax/read/required terrain failures. Never fabricate zero metadata.
- Audio event delivery remains independent of acknowledgement queue service.
  Queue cadence requires an explicit host policy independent of pause and game
  speed. Retail arbitration arithmetic remains separately documented.
- Music intensity uses established damage/death counts and presentation clock
  thresholds. The shell retains and pumps the outgoing music owner during fade.
- Visual changes depend on verified palette table identity and lens arithmetic;
  correct the owning research before implementation. Unresolved bytes or unsafe
  reads are explicit questions or host safety policies, never guessed behavior.
- SKIRMISH entry and Difficulty update both established selectors. Do not infer coupling
  for unrelated registry or NEWGAME writes.

## Verification

Baseline fast/retail/device gates passed at starting main in the previous queue.
Review each worker diff and at least two arithmetic-heavy cited contracts;
inspect clean-room and invariants. Run affected checks, tools/check,
tools/check-retail, applicable design/device gates, actual visual inspection and
sequential matching classic/modern live benchmarks. Keep artifacts outside the
repository. Integrate main, recheck if it moves, land, repeat gates, and remove
only this task's worktrees/branches.

## Evidence and completion

- Content commit `6de8a30` independently reviewed, with rejection classification,
  mount dedup and parsed-map boundaries checked against owning research. Root
  fast/retail logs: `/private/tmp/nanolathe-gameplay-content-review-*.log`.
- Static evidence corrected beam palette identity, lens generation/sampling,
  music participant/death predicates, bucket order, comparison signedness and
  chooser lifetime. Owning research commits `f4c6040`, `f0bcd66`; explicit lens
  renderer policy `7085942`, precision boundary `0d22bff`.
- Lens key is a verified uninitialized retail field; SC18 deterministic zero
  is the host policy, with a specific code/research question. Modern compares
  RGB key colour because its enhanced composite no longer carries index identity.

- Root reviewed each source diff, including prior-bucket-first music sums,
  strict signed/unsigned thresholds, cause-specific death entry, lens truncation
  and relative sampling. New code retains deterministic simulation boundaries.
- Independent Metal fixture passed. Actual classic/modern native and 2x PNGs
  are byte-identical; key skipping, overlapping lenses, prior/later lines and
  viewport edges were inspected. Artifacts: `/private/tmp/nanolathe-gameplay-lens-shots`
  and `/private/tmp/nanolathe-gameplay-lens-review`.
- Integrated concurrent Intro, startup-logo, campaign-ending and credits work
  through main `819ff210`. Combined fast/retail gates passed on `70f05fb2`:
  `/private/tmp/nanolathe-gameplay-movies-check.log` and
  `/private/tmp/nanolathe-gameplay-movies-retail.log`.
- Integrated installation discovery through main `cb0c53bf` in `068a1e26`.
  Resolved the archive-loop overlap by retaining the new root priority offset
  together with candidate rejection and successful-mount accounting.
- All seven items are implemented and independently reviewed. Final integrated
  fast, retail and Metal gates passed on `068a1e26`. Logs:
  `/private/tmp/nanolathe-gameplay-roots-check.log`,
  `/private/tmp/nanolathe-gameplay-roots-retail.log` and
  `/private/tmp/nanolathe-gameplay-roots-device.log`.
- The final merge revision, post-landing gate results and cleanup record are
  recorded in `/private/tmp/nanolathe-gameplay-verification/landing.json`.

## Visual and performance evidence

Artifacts: `/private/tmp/nanolathe-gameplay-verification`. The metadata records
source revisions, binary hashes, frozen settings and commands. Baseline is
`819ff210`; candidate is `70f05fb2`. Subsequent integration changes startup root
selection, not the measured battle or renderer paths.

All 15 sequential runs passed: matching classic and modern Great Divide battles
at 1x, 1.5x and 2x; the displayless simulation pair; and the frozen lens probe.
Every live pair had identical scene metadata and all 180 frame censuses. All
12 battle captures were visually inspected: armies, factory nanoframes, burning
features, projectiles, HUD/minimap and fog remain present. Acknowledgement text
changes with the corrected queue cadence.

| Renderer / zoom | Baseline median / p95 draw work (ms) | Candidate median / p95 (ms) |
|---|---:|---:|
| Classic 1x | 15.977 / 18.378 | 15.991 / 18.254 |
| Classic 1.5x | 22.063 / 26.352 | 22.218 / 26.279 |
| Classic 2x | 21.993 / 28.176 | 22.301 / 27.940 |
| Modern 1x, repeat 1 | 10.659 / 11.692 | 10.207 / 11.324 |
| Modern 1x, repeat 2, reversed order | 10.298 / 11.387 | 10.546 / 11.737 |
| Modern 1.5x | 8.582 / 21.137 | 8.472 / 9.568 |
| Modern 2x | 8.044 / 11.976 | 7.978 / 10.002 |

The initial modern 1x pair had a candidate tail spike; two sequential repeats,
including reversed order, did not reproduce it. Repeat metadata and all frame
censuses also match. These short host measurements show no consistent regression;
timing variation is not evidence of a speedup.

The simulation pair has equal initial/warm/final **partial** fingerprints, equal
censuses, and equal simulation/CRT draw counts (91,086 / 2,456,545). Mean tick
cost was 2.631 versus 1.868 ms; allocations were 159,355 versus 159,340 bytes per
tick. The partial fingerprint is not a claim of exhaustive state equivalence.

The authored five-lens frozen probe passed and its native/2x captures were
inspected. Median CPU submission was 0.043 / 0.143 ms; median Draw-to-Draw interval
was 8.328 / 8.333 ms. Intervals are host callback timing, not GPU timestamps.
Stock MINDGUN has no stock unit reference, so the authored fixture deliberately
exercises its projectile. Audio lifetime/fade behavior was checked through the
shell and fake device tests; native listening was not performed.
