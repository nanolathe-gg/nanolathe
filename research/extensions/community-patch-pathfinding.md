# Community 3.9.x patch line: pathfinding and movement changes

## Evidence scope and sources

This document owns one question: what the community "unofficial patch" line for
Total Annihilation — the TA Universe *TA Unofficial Patch* v3.9.x and the engine
DLL line that continues it — **documents** about pathfinding and unit movement.
It records documentation and authored configuration only. No patch binary was
disassembled, decompiled or string-mined for it, and no third-party source was
read or copied. It establishes nothing about retail Total Annihilation, which
stays owned by [retail-executable-spec](../retail-executable-spec/README.md),
and it approves no Nanolathe behavior.

Per-package material stays with its owning document:
[ProTA](prota-engine.md), [Escalation](taesc-engine.md),
[TA Zero](ta-zero-engine.md), [the shared draw DLL](draw-engine-interface.md),
[the recorder DLLs](ta-demo-recorder.md). This document cites them rather than
restating their findings.

**Established — primary sources, retrieved 2026-09-21.**

| Source | Identity | What it is |
|---|---|---|
| `Total Annihilation v3.9.02 Beta Patch Readme.txt`, SHA-256 `db4e5647 23b668eb ecf6f54f 7d84b56b 30bb3573 088a9fe8 8518e90f bdd64da4` | dated "October 2, 2013"; from the Internet Archive item `ta3902b_patch` (`https://archive.org/download/ta3902b_patch/Total%20Annihilation%20v3.9.02%20Beta%20Patch%20Readme.txt`) | the v3.9.02 release readme: feature list and the version history for v3.9.02 and v3.9.01 |
| TA Universe release thread 43735 (`https://www.tauniverse.com/forum/showthread.php?t=43735`, read through the Internet Archive snapshot of 2016-08-10) | opening post reproduces the same readme text | corroborates the readme as the publisher's own text |
| TA Universe release thread 42656 (`https://www.tauniverse.com/forum/showthread.php?t=42656`, Internet Archive snapshot of 2016-02-28) | opening post dated 15 April 2012, headed "Total Annihilation v3.9.01 Beta Patch (April 15, 2012)" | the v3.9.01 readme as published |
| `https://files.tauniverse.com/files/ta/unofficial-patch/` | `TA_Patch_3902.exe` and `TA_Patch_Resources.exe`, both timestamped 03-Oct-2013 | the publisher's own download index: the newest release it offers is v3.9.02 |
| `ProTA4.8/ProTA.ini`, `TA_Zero_Alpha_5/TAZero.ini`, Escalation Gold 10.2.0 `TAESC.ini` (local packages; see the per-package documents for their artifact identity) | shipped preference files | the `AISearchMapEntries` setting and its documented comment block |
| `ProTA4.8/ProTA 4.8 changelog.txt`, "Engine notes" | reproduces the "TA engine v2025.8.29" release notes | the continuing engine line's dated change list |
| `https://github.com/tanvanman/TADR` release notes (46 releases read, `v2024.3.2` … `dev-dcff5dd`, newest 2026-09-20) | the maintainers' own dated release notes for the recorder/engine DLL pair; repository license is **NOASSERTION** (no license granted) | dated change list and a per-package feature matrix |
| `https://github.com/Skirmisher/TA-Patch-Installers` (`Patch/notes.txt`, `Patch/TODO`, `README`, retrieved 2026-09-21; repository carries **no license**) | the patch installer project's own notes | names the superseded standalone "TA Pathfinding Fix" and an unreleased version string `3.9.3` |

Because the last two repositories grant no license, only their published
documentation text was read; no source file was read, copied or translated, and
no claim in this document rests on implementation source.

## The releases this line comprises

**Established — documented.** Two releases of the v3.9.x line were published:
**v3.9.01 Beta** (15 April 2012) and **v3.9.02 Beta** (2 October 2013). The
readme describes the line as "a beta version of the TA Unofficial Patch, a
comprehensive update to Total Annihilation intended to replace v3.1 as the
de-facto version", states that it "does not change the game balancing in any
way; it only adds new features and fixes technical issues", and says the
v3.9.xx betas lead "up to v4.0, which is intended to be the first non-beta
release". In our words: the line advertises itself as a compatibility and
technical-limits update, explicitly not a gameplay change, and no v4.0 was
published on the publisher's download index.

**Established — documented.** The patch installer project records a version
string `3.9.3` in a registration command and a "patch readme" cleanup
checklist, with no accompanying change list. In our words: a `3.9.3` was
prepared in the installer tooling; no release notes for it exist among the
sources above, so nothing about its content is established.

**Unknown — post-3.9.02 patch releases.** Whether any "3.9.3"/"4.0" TA Patch
release exists outside the publisher's index. What would settle it: a readme or
release-note artifact identified by version, hosted or mirrored by the
publisher.

**Established — the line continues as a dated engine-DLL line.** ProTA 4.8's
preference file is headed "ProTA 4.8 settings (TA v2025.8.29)" and quotes "TA
v2025.8.29 default" values beside "TA v3.1 default" values; its changelog's
Engine notes say "Updated to new TA engine v2025.8.29" and reproduce dated
release notes from `v2025.4.24` through `v2025.08.29`. In our words: after
v3.9.02 the line's engine component kept being developed and versioned by date,
and the packages built on it inherit those versions. The per-package identity of
each DLL build stays with [ProTA](prota-engine.md),
[Escalation](taesc-engine.md) and [TA Zero](ta-zero-engine.md).

## The one documented pathfinding change: the cycle budget

**Established — documented, v3.9.01 and v3.9.02.** Both readmes carry the same
single line under "Engine Improvements":

> "Pathfinding cycles have been increased from 1333 to 66650, dramatically
> increasing pathfinding quality when there are large numbers of units in-game
> (pathfinding no longer degrades as unit count increases)."

In our words: one numeric budget that the authors call "pathfinding cycles" is
raised fifty-fold from its retail value, and the authors claim the effect is
that path quality stops falling off as the unit population grows. The same
sentence, verbatim, is what Escalation's readme reproduces
([Escalation](taesc-engine.md) owns that package's copy).

**Established — documented, two independent mechanisms.** The v3.9.01 version
history lists both:

> "Changed default pathfinding cycles in exe to 66650 (separate from
> tdraw.dll/TA.ini implementation)."

and, separately, "Added pathfinding adjuster." The same release's settings
section says options "can now be adjusted via TA.ini, including new features
such as pathfinding cycles and special effects limit as well as registry
overrides". In our words: the release both changes the value the executable
starts with and ships a DLL-side adjuster that writes the value from a
preference file, and the authors regard these as two separate implementations
of the same change. This matches what the packages show: TA Zero's optional
"Fix 10" renderer patches the search constructor while its base build does not
([TA Zero](ta-zero-engine.md)), and all three inspected DLL builds read the
preference key ([shared draw DLL](draw-engine-interface.md)).

**Established — documented, the standalone predecessor.** Both readmes list
"TA Pathfinding Fix" among the third-party software the patch "updates,
replaces, or otherwise makes obsolete", and the installer project repeats it in
its cleanup checklist. In our words: a standalone tool that changed the same
budget circulated before the patch, and the patch subsumes it. Its own
documentation was not located.

**Established — documented, attribution.** Escalation's readme credits "xpoy
for awesome ddraw dll based updates including megamap, pathfinding, and custom
ini configurations". In our words: the DLL-side pathfinding adjuster is
credited to the same author as the megamap and preference work.

### The user-visible setting

**Established — authored configuration.** The setting is named
`AISearchMapEntries` in the `[Preferences]` section of the shipped preference
file, and every inspected package documents it with the same comment block:

> "; Pathfinding cycles
> ; Setting too low (such as TA v3.1 default) ruins pathfinding but setting
> extremely high lowers fps
> ; TA v3.1 default is 1333"

followed by that package's own default. Observed defaults: **66650** in
ProTA 4.8 (whose comment also states "TA v2025.8.29 default is 66650"),
TA Zero Alpha 5 and Escalation Gold 10.2.0. In our words: one integer
preference, documented as the "pathfinding cycles" count, with retail's 1333
named as the value that "ruins pathfinding" and the authors' own warning that
raising it far enough costs frame rate. The key's membership in the DLL
preference census is owned by [the shared draw DLL](draw-engine-interface.md);
the per-package defaults are owned by the three package documents.

**Unknown — the documented range and the patch's own `TA.ini` text.** No source
above states a minimum, maximum or validated range for `AISearchMapEntries`,
and the v3.9.02 `TA.ini` as shipped was not read (only the derived package INIs
were). Community posts quoting other values (for example 77777 or 90050) are
recollection and establish nothing. What would settle it: the `TA.ini` shipped
inside `TA_Patch_3902.exe`, read as text.

**Unknown — which engine field the adjuster writes.** The readmes name a value
and its retail default; they do not name the field. Establishing the write site
would require inspecting the patch binary, which the
[evidence policy](README.md#evidence-policy) forbids. The numeric coincidence
with our retail research is treated as inference below, not as fact.

## What the 3.9.x documentation does not say

**Established — by census of the v3.9.01 and v3.9.02 readmes.** Apart from the
cycle-budget line, the two readmes contain no statement about pathfinding or
movement behavior. A search of both for *path*, *stuck*, *wedge*, *repath*,
*formation*, *collision*, *transport*, *landing* and *move* returns only: the
cycle line; the two-mechanism version-history line; "Added pathfinding
adjuster"; the superseded "TA Pathfinding Fix" entry; unrelated uses of
"cycles" for the `CTRL+F`/`CTRL+B`/`CTRL+\` selection and camera cycling; and
packaging lines about moving files between the two installers.

In particular, nothing in the 3.9.x documentation addresses **units wedging or
getting stuck**, **repath cadence**, **group movement or formations**,
**pathing through unexplored ground or fog**, **blocked-unit handling**,
**transport or aircraft landing**, **builder or factory exit pathing**,
**collision or pushing**, or **hover and ship pathing**.

**The honest summary for the 3.9.x line: the documentation establishes a limit
raise and nothing else.** Everything else attributed to these patches in
community discussion is recollection, and recollection establishes nothing
here.

## Movement-adjacent behavior in the continuing engine line

These are **not** 3.9.x changes. They are dated changes to the engine DLL that
the packages now ship, recorded here because they are the only documented
movement-behavior changes anywhere in this line.

**Established — documented, dated release notes (ProTA 4.8 "Engine notes" and
the maintainers' release notes).**

- `v2025.5.18`: "Improved behaviour of con units when guarding a factory - they
  stay put after finishing a build", introduced behind a feature flag that
  `v2025.6.3` made opt-out and `v2025.7.12` changed to **opt-in**. In our
  words: a guarding construction unit is kept at its position across the end of
  a build instead of moving, and the maintainers treat it as an opt-in
  deviation rather than a default.
- `v2025.7.12`: "Allow user to queue build orders underneath their own mobile
  units; and auto kickout of units that are under a build order", plus
  "User configurable con unit patrol and guard settings" exposed in the
  in-game options menu. `v2025.08.19` restricted the kickout to the ordering
  player's own units. In our words: a build order may be placed on ground
  occupied by the player's own mobile units, and those units are ordered away
  when the order takes effect.
- `v2025.8.1`: "Preview build square to yellow if requires unit kickout" — the
  placement preview signals that clearance will be needed.
- `v2026.5.28`: "Fix intermittent bug where construction unit tries to sit
  within footprint of rotated structure". In our words: a defect fix in where a
  builder positions itself relative to a structure footprint, in a build with
  non-retail structure rotation.
- The maintainers' per-package feature matrix (2026-08-31) lists
  "Construction units stay put while guarding", "Construction units
  reclaim-only / assist-only while patrolling" and "Auto-kickout of
  construction units blocking a builder" as enabled for ProTA, Escalation,
  Mayhem, TA Zero and BTA, and **not** for unmodified OTA. That matrix has no
  pathfinding row at all.

The preference strings that expose the guard and patrol behavior are already
recorded by [ProTA](prota-engine.md); this document adds only their dated
provenance.

**Established — by census of those release notes.** Across all 46 published
release notes of the continuing line, no entry describes a change to the path
search, its budget, its admission cadence, its route publication, or pathing
through unexplored ground.

## Content-level mitigations in packages built on this line

Recorded for completeness, because they are how the package authors say they
addressed pathing complaints without touching the search. Each is **Established
— authored content, documented in that package's own changelog**, and each
belongs to its package, not to the patch line.

- ProTA 4.4: "Added rotated versions of shipyards to improve pathfinding of
  exiting ships." In our words: the exit problem was addressed by authoring
  additional building orientations, not by changing exit logic.
- Escalation Gold 10.2.0: "Reclassed all 8x8 Movement classes to 7x7 to
  optimize large T2 and T3 unit pathfinding", "Fixed multiple underwater mobile
  unit corpses blocking ship/sub pathfinding", and a documented reduction of
  full-wreck value justified in part by wrecks degrading pathing on choke maps.
  In our words: footprint sizing and wreckage density were tuned as pathing
  levers.

## Relation to the Nanolathe retail research

The retail mechanism our research states, for comparison only:

- The path scheduler's **per-scheduler-call total step allowance is 1333**,
  compiled in, and the only writer in a shipped session is the developer
  console `Search` command — no registry key, INI file, TDF key or command-line
  switch reaches it
  ([04 R-PATH-01 §10](../retail-executable-spec/04-units-orders-scripts-and-movement.md#the-heuristic-base-is-compiled-in-the-search-console-command-is-its-only-writer-r-path-01-10)).
- That allowance is divided by player count into per-player step accumulators
  each tick; admitting one unit's request charges 100, an idle probe charges 1,
  and each expansion iteration is capped at 100 heap pops; a global counter
  replenishes every 150th scheduler call
  ([04 §7.3](../retail-executable-spec/04-units-orders-scripts-and-movement.md#73-scheduler-budget-publication-and-route-storage),
  [04 R-PATH-01 §6](../retail-executable-spec/04-units-orders-scripts-and-movement.md#the-per-player-quantum-is-the-heuristic-weight-not-a-work-slice-r-path-01-6)).
- The per-player **quantum is the heuristic weight**, not a work slice: it is
  rewritten at each replenish as the compiled base times 6, 3 or 1 according to
  `serviceCount / divisor`, where the **divisor is the session's per-player unit
  limit**
  ([04 R-PATH-01 §6](../retail-executable-spec/04-units-orders-scripts-and-movement.md#the-per-player-quantum-is-the-heuristic-weight-not-a-work-slice-r-path-01-6)).
- A blocked or route-exhausted mover re-requests at most once per 60 ticks;
  publication is full-or-empty, never a partial prefix
  ([04 §7.3](../retail-executable-spec/04-units-orders-scripts-and-movement.md#73-scheduler-budget-publication-and-route-storage)).
- The search treats a block the requesting player has not explored as
  **passable**
  ([04 R-PATH-01 §2](../retail-executable-spec/04-units-orders-scripts-and-movement.md#the-coarse-word-the-search-tests-is-mapping-memory-not-a-building-mask-r-path-01-2)).

**Supported inference — the patch's "1333" is our step allowance.** The value
the patch names as the retail default is the value our research establishes for
the per-scheduler-call step allowance, under a setting name the authors gloss as
"pathfinding cycles", and our research finds no other retail path constant equal
to 1333. Missing evidence: the patch does not name the field, and confirming the
write site would require binary inspection the evidence policy forbids. An
alternative that the documentation cannot exclude is that the adjuster writes a
different budget with the same seed value. Do not implement anything that
depends on the identification without further evidence.

**Supported inference — raising the unit limit also moves a path constant.**
The same releases raise the per-player unit limit from retail's 250 to a
configured 1500. In our research the unit limit is the **divisor** of the
heuristic-weight tier, so a session configured with a larger unit limit keeps
the tier — and therefore the heuristic weight — at its lowest tier index for
far longer. Missing evidence: whether the patch's real-time unit-limit write
reaches the same lobby field that our research says is copied at battle setup.
This is a coupling to be aware of when reasoning about the authors' quality
claim, not a documented patch change.

**Established — nothing in this line bears on the unexplored-ground wedge.**
Nanolathe's found defect — a search that admits unexplored ground while the
commit validator rejects the step, so the mover re-requests the same rejected
route forever — has **no counterpart anywhere in the 3.9.x documentation, the
continuing engine line's 46 release notes, or the packages' engine sections**.
The community line's answer to "units path badly" is, in its own documentation,
entirely a budget raise. It offers no evidence for or against teaching a mover
the cell that rejected it.

## Bearing on Nanolathe Modern

Candidates only. None is approved, and no constant or contract is proposed here.
An approved departure belongs in its owning `docs/DESIGN_*.md` under
[DESIGN_GAMEPLAY_RULES §9](../../docs/DESIGN_GAMEPLAY_RULES.md#9-extending-the-existing-mechanism).

1. **A larger path step allowance is a limit change, not a behavior change.**
   Our scheduler already carries the allowance as the documented retail constant
   and charges exactly what the search reports. Evidence still needed before any
   Modern value could be chosen: that the community value corresponds to our
   field at all (the inference above), and a Nanolathe-side measurement of what
   a raise buys and costs — the authors' own INI warns that "extremely high
   lowers fps", and our budget is spent under a different renderer and tick
   cost. A borrowed constant with no measurement behind it would be an invented
   constant.
2. **Unit-limit-coupled heuristic weighting deserves a probe, not a policy.**
   If the tier divisor is the unit limit, then Nanolathe sessions with large
   unit limits already sit in a different weighting regime than retail
   skirmishes did. Evidence still needed: a Nanolathe measurement of route
   quality and search cost across tiers; this is our own research question, and
   the patch line contributes only the observation that both values were raised
   together.
3. **Construction-site clearance has an independent precedent.** The continuing
   line's "auto kickout of units that are under a build order", its
   placement-preview signal, and its restriction to the ordering player's own
   units are documented for 2025 builds. Nanolathe's
   [construction-site clearance](../../docs/DESIGN_ECONOMY_CONSTRUCTION.md#modern-construction-site-yielding)
   policy is already approved on its own terms; this is corroboration that
   another implementation reached a similar shape, plus two details worth
   weighing — ownership restriction and a placement-preview signal. Evidence
   still needed for either detail: none from this line beyond the quoted notes,
   which do not state the selection rule, the order issued, or what happens when
   the unit cannot move.
4. **Guarding builders holding position across a build completion** is
   documented, opt-in, and user-configurable in the continuing line. It is
   adjacent to Nanolathe's approved
   [factory-exit yielding](../../docs/DESIGN_ECONOMY_CONSTRUCTION.md#modern-factory-exit-yielding).
   Evidence still needed before it could be considered: what "stay put" means
   for an already-issued order, how it interacts with the guard order's own
   repath, and whether it changes resource or RNG behavior. The release notes
   state an intent, not a rule.
5. **Content-level levers are cheap and reversible.** Footprint sizing, building
   orientation variants and wreck density were the package authors' actual
   pathing remedies. These are content decisions in our model, not gameplay-mode
   departures, and need no Modern rule.

Nothing here supports a Modern rule about unexplored-ground pathing. That work
stands or falls on Nanolathe's own retail research and measurements.

## Unknown

- **The documented range of `AISearchMapEntries`.** No source states a minimum
  or maximum. Settled by the patch's own `TA.ini`, read as text.
- **The v3.9.02 `TA.ini` text.** Only the three derived package INIs were read;
  the patch's own preference file and its comment block were not.
- **Which engine field the adjuster writes.** Not documented; not establishable
  under the extension evidence policy.
- **Any post-3.9.02 TA Patch release for unmodified TA.** The publisher's index
  stops at v3.9.02; the engine line continued under date versions inside mod
  packages, and whether a standalone patch release carries them is unestablished.
- **The standalone "TA Pathfinding Fix".** Named as superseded by both readmes;
  its own documentation, author and value were not located.
- **Whether the "no longer degrades as unit count increases" claim was
  measured.** The readmes assert it; no source shows a measurement.
