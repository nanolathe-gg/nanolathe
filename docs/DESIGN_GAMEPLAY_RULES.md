# Design — Gameplay rules

`internal/gameplay` and `internal/session/rules.go`, plus one `rules.go` in
each package that owns a gameplay decision. How Nanolathe's three reserved
gameplay policies — the retail baseline **Strict 3.1**, the compatibility
layer **Community 3.9**, and the default **Modern** — reach the algorithms
that behave differently under them.

This document owns the *mechanism*: the seam list, who may implement a seam,
what an implementation may cost, when a set may be bound, and what a save
knows about it. It owns no policy. Community contracts live in
[DESIGN_COMMUNITY_PATCH](DESIGN_COMMUNITY_PATCH.md); every Modern rule keeps
its contract, retail baseline, boundaries and verification in the design
document that owns the affected subsystem. This document only says which seam
carries each answer.

This is one of the design documents listed by [ARCHITECTURE.md](ARCHITECTURE.md);
[INVARIANTS.md](INVARIANTS.md) I11 is the rule every diff is reviewed against.

## 1. Shape

`gameplay.Mode` is the vocabulary, and a word has two forms: one of the three
reserved words — `modern` (the default and zero value), `community-3.9`, or
`strict-3.1` — or the name of any rule set this build registered (§8). It is
what the settings file stores and what `--gameplay` parses.
`Session.Gameplay` is narrower: it always holds one of the three reserved
words, because that is the base answer for code outside a seam, and the
selected set's own name stays on the bound set.

What a word *selects* is a `session.RuleSet`: one implementation per seam,
named, bound once. After binding, every decision site holds a concrete
implementation and no simulation code reads the mode word.

```go
type RuleSet struct {
    Name         string
    Base         gameplay.Mode // the reserved set this one derives from; zero is Modern
    Combat       combat.Rules
    Visibility   visibility.Rules
    Orders       orders.Rules
    Construction construction.Rules
    UnitLimit    UnitLimitRules
    ScriptPorts  ScriptPortRules
    Movement     movement.Rules
    Path         path.Kernel
    Planner      ai.Planner
}
```

The set also carries a `community.Overrides` declaration in `Features`.
Composition resolves content, registered declarations, player settings and
command-line overrides in that order. `Session.Community` holds the immutable
result; service rebinding copies that result without resolving it again.
Selection at a command boundary resolves the new set before changing the
session, so an invalid declaration leaves the current selection intact.

`StrictRuleSet()`, `CommunityRuleSet()` and `ModernRuleSet()` are the three
reserved sets in derivation order. Each package's `CommunityRules` embeds its
`StrictRules`, and `ModernRules` embeds `CommunityRules`; a layer overrides
only the answers its policy changes.
`RuleSetForMode` resolves a selection word to a set and `Session.BindRules`
projects each field onto the service that asks it — `Combat.Rules`,
`Vis.Rules`, `Build.Rules`, `Build.OrderBinding.Rules`, `Movement.Rules`, `Movement.Kernel`, and every
computer player's `Planner` — and onto every queue binding composed
afterwards. `Session.SetRules(name)` is the selection entry point
that can report an unknown name; `SetGameplay` is the same selection for a
word already known to be selectable, and it is what the phase-1 command
boundary calls.

A whole set is bound at once. There is no partial binding and no per-seam
override on a bound session: a caller that wants one different answer
registers a set that composes the shipped implementations (§8), so a session
can never hold half of one policy and half of another. **Binding is
idempotent**, which is what lets the composer re-project after every unit it
allocates; `Session.RebindRules` is that re-projection, and it re-projects
what is bound rather than re-deriving a set from the mode word, because the
word is the bound set's *base* and deriving from it would silently replace a
selected third-party set with its base set.

A service or binding whose seam was never set answers as Strict 3.1. That
fallback exists for fixtures and for a queue reconstructed by a restore, not
as a second way to select a policy: a composed session always binds.

## 2. The seams

| Seam | Owner | Carries |
|---|---|---|
| `combat.Rules` | `internal/combat` | terrain admission ([DESIGN_WEAPONS_PROJECTILES §2.3.1](DESIGN_WEAPONS_PROJECTILES.md#231-modern-terrain-admission)) the launch-gate half of Hold Fire (§2.6.1), and Modern threat targeting/incoming-fire coordination |
| `orders.Rules` | `internal/orders` | [Hold Fire](DESIGN_UNITS_ORDERS_COB.md#modern-hold-fire) at a combat join, the deferred bomber leash ([DESIGN_MOVEMENT_PATH §3.4.1](DESIGN_MOVEMENT_PATH.md#341-modern-bomber-pass-completion)), the three guard-assistance legs, Modern danger response/protected work, [crowded arrival](DESIGN_UNITS_ORDERS_COB.md#modern-crowded-arrival), and which records may finish as [unreachable moves](DESIGN_UNITS_ORDERS_COB.md#modern-unreachable-moves) |
| `construction.Rules` | `internal/construction` | [factory-exit](DESIGN_ECONOMY_CONSTRUCTION.md#modern-factory-exit-yielding) and [construction-site](DESIGN_ECONOMY_CONSTRUCTION.md#modern-construction-site-yielding) clearance; [authored build membership](DESIGN_ECONOMY_CONSTRUCTION.md#modern-authored-build-membership) |
| `visibility.Rules` | `internal/visibility` | Community allied-jammer suppression and aircraft border visibility (DESIGN_COMMUNITY_PATCH §4.4); no prior seam owned per-viewer sensor decisions |
| `session.ScriptPortRules` | `internal/session` | Community recorder ports 32 and 69–75 (DESIGN_COMMUNITY_PATCH §4.5) |
| `session.UnitLimitRules` | `internal/session` | [Modern save unit limits](DESIGN_SESSIONS_AI_SAVE.md#modern-save-unit-limits) |
| `movement.Rules` | `internal/movement` | [learned terrain](DESIGN_MOVEMENT_PATH.md#modern-learned-terrain): a ground mover rejected by static ground teaches its owner, and the owner's next search reads what it learned; [re-route staggering](DESIGN_MOVEMENT_PATH.md#modern-re-route-staggering): a 0–7 tick offset on the 60-tick re-route throttle; [group-order spreading](DESIGN_MOVEMENT_PATH.md#modern-group-order-spreading): a same-tick group's first requests admitted over three ticks, nearest first; [bounded path work](DESIGN_MOVEMENT_PATH.md#modern-bounded-path-work): carried search work capped at four shares and a futile polling sweep ends the player's call; [group destination slots](DESIGN_INTERFACE_HUD_INPUT.md#modern-group-destination-slots): each actor of an ordinary group move gets its own free destination footprint; [allied pass-through](DESIGN_MOVEMENT_PATH.md#modern-allied-pass-through): head-on friendly movers pass through each other mid-route; [unreachable moves](DESIGN_MOVEMENT_PATH.md#modern-unreachable-moves): a goal certified sealed by a static re-run of the setup ray finishes its eligible move at the frontier after a 90-tick dwell and a closing probe; [jam release](DESIGN_MOVEMENT_PATH.md#modern-jam-release): a ground mover friendly units have blocked for 30 ticks ignores friendly ground occupants other than same-way movers for 90 ticks and plans over the static view |
| `path.Kernel` | `internal/path` | the search a route request is opened with ("The path search kernel" below); Strict 3.1 and Community bind `path.RetailKernel`, Modern binds `path.StraightenKernel` ([route straightening](DESIGN_MOVEMENT_PATH.md#modern-route-straightening)) |
| `ai.Planner` | `internal/ai` | the computer player's per-tick think step ("The computer player's think step" below); all three reserved sets bind `ai.RetailPlanner` |

The last two rows are the **whole-subsystem** seams: each replaces an
algorithm rather than answering a question inside one, and both now exist.
Neither has a Community or Modern implementation — all three reserved sets
bind the retail one —
because replacing either is a behaviour change with its own contract rather
than a selection. Both are request-granularity replacements (§4), and the two
sections below state each one's boundary: a kernel may not change *when* a
route publishes, because publication timing is ordering behaviour owned by the
tick, and a planner may not draw from the simulation stream in a different
order, because that call order is the whole future of the battle.

### The path search kernel

`path.Kernel` has one method: it opens one request's resumable search, and
`path.Search` is that search behind an interface. `path.RetailKernel` is the
retail ray-and-A\* search, it is zero size, and **Strict 3.1 and Community bind
it**. Modern binds `path.StraightenKernel`, the same retail search whose
finished route has its sawtooth turns removed before publication
([Modern route straightening](DESIGN_MOVEMENT_PATH.md#modern-route-straightening));
it publishes on exactly the slice the retail search finishes on. ([Modern learned terrain](DESIGN_MOVEMENT_PATH.md#modern-learned-terrain)
changes one *input* the retail search reads, through `movement.Rules`; the
search is the same.) `Session.BindRules` projects the field onto
`movement.System.Kernel`; a system with nothing bound searches as retail does,
which is the fallback a fixture and a restored system rely on.

The boundary is what makes this a rule-set seam rather than a rewrite hatch.
A kernel decides **how** a route is found. It decides nothing about **when**
one appears, because that is ordering behaviour the tick owns and every kernel
shares:

- the scheduler's single active request and its admission cursor,
- the per-player step allowance and heuristic weight,
- the full-or-empty publication boundary — a budget slice publishes nothing,
- route consumption by the follower, and the notification bits a request
  raises on its order record.

A kernel that wanted to change any of those would be a movement contract
change with its own design section and its own fingerprint, not a rule-set
selection. `path.SearchFunc` stays what the scheduler calls; the kernel sits
inside it, at the one point that used to name the retail search directly.

### The computer player's think step

`ai.Planner` is the per-player *think step*, not a replacement computer
player. The session's before-deadline hook dispatches one step per computer
player per tick through `ai.Manager.Tick`, and the bound planner answers what
to do with that manager's state on that tick:

```go
type Planner interface {
    Step(m *Manager, tick uint32, w *units.World, econ *economy.Service)
}
type RetailPlanner struct{} // the retail step, unchanged
```

The split is deliberate. The session reads the retail manager's state deeply —
the strategic record, the ten task deadlines, the nine group vectors, the
session bindings, and everything a save writes and a restore rebuilds — so the
`*ai.Manager` stays, owning its bookkeeping, its save and its restore, and only
the decision is replaceable. `Manager.Tick` is the seam every caller reaches,
so a fixture and the composed session dispatch the same way, and the retail
gates stay inside the step so no caller can skip them.

A replacement answers the same step from the same manager. It may draw from the
simulation stream only through the manager's own accessor and only in the order
the retail step draws, because that call order is the whole future of the
battle; a planner that draws differently is a gameplay policy needing its own
contract, so **all three reserved sets bind `RetailPlanner`** — there is no
Community or Modern planner today, and Strict 3.1 could never bind one. A set
assembled outside `internal/ai` composes the retail step by calling
`ai.RetailPlanner{}.Step`; the retail body itself stays unexported.

The manager's field is the binding point: nil is the retail step, so a fixture
and a manager a restore rebuilt run the executable's behaviour rather than none
at all. `Session.BindRules` projects the bound set onto every computer player
the session already owns, and `initializeBattleAI` takes the same field from
the bound set for a manager composed later, including on the restore path; both
directions agree, which is what keeps rebinding idempotent (§1).

**Out of scope, deliberately:** a replacement planner with state of its own.
That would need its own save and restore contract, its own place in the
per-player settlement walk, and a decision about what a save written by one
planner means to another. Nothing here provides that: a planner is asked a
question and answers it from the retail manager's state.

### The package seams are in-package extension points

A Modern implementation lives in the package whose algorithm asks the
question, beside the Strict one, because its body calls unexported kernels:
the launch preview and the terrain sampler in `internal/combat`, the standing
flag masks and queue helpers in `internal/orders`, the occupancy planes and
the bounded cardinal search in `internal/construction`. Moving those bodies
into a sibling package would mean exporting the kernels, which widens the
package API for no gain. A rule set assembled elsewhere therefore composes
shipped implementations — it may embed `ModernRules` and override one
method — rather than reimplementing a policy from outside.

Each package keeps its own `StrictRules`, so the retail path never reaches a
Modern type and no retail call site imports a Modern package.

## 3. Allocation rules

These are the reason the seam is cheap enough to sit in the tick at all, and
they are checked by tests rather than trusted:

1. **Every cached implementation holds no state.** A zero-size value converts
   to an interface without allocating; a pointer is allowed only when its
   pointee is also zero size. The registry shares implementations across
   sessions, so pointers to mutable session scratch are not permitted.
   `TestCachedRuleSetImplementationsHoldNoState` checks the pointee as well as
   the value; the allocation test alone is not a state-ownership check.
   Mutable state belongs to the supplied service, unit, manager or request
   (for example the `path.Search` returned by a stateless kernel). A rule
   must not hide mutable state in globals or retain request arguments.
   Supporting stateful implementations later requires a deliberate lifecycle,
   isolation, switching and save/restore design (§9), not weakening the guard.
2. **No call site builds a closure per call.** A method takes the concrete
   state its answer needs — the unit, the order record, the resolved fire
   attempt as a pointer to caller-owned reusable storage, the tick — and never a closure
   or a slice it retains.
3. **A method is asked at the same site the mode projection was read.**
   Introducing a seam moves no logic; it only changes who answers. That is
   what makes an identity fingerprint the correct gate for a seam unit.
4. **Strict policy answers add no randomness or state writes.** The policy
   methods in `combat.Rules`, `orders.Rules`, `construction.Rules` and
   `UnitLimitRules` and `movement.Rules` preserve the retail path without extra work. The whole
   subsystem seams still execute retail search and planner behavior, including
   their established state writes and RNG consumption; they are not no-ops.
   An approved Modern change documents its RNG and resource effects and uses
   only the owning service's supplied stream, never a new private stream.
5. **Binding happens outside a tick.** Composition and the phase-1 command
   boundary are the only two places a set may be bound, so no phase selects an
   implementation ([INVARIANTS.md](INVARIANTS.md) I1).

## 4. Granularity

An interface call **per request** is fine: one fire attempt, one blocked
construction attempt, one path search, one planner tick. An interface call
**per node** is not: neighbour expansion, a per-cell occupancy probe, a
per-pixel or per-sample step. A policy that would need a per-node decision is
redesigned as a per-request one — a precomputed mask, a decision cached for
the request — or it is not a rule-set seam.

`path.Goal` already sits inside the search loop. It predates this document,
it is not a gameplay seam, and it is not a precedent for adding one. The
search kernel above is the worked example of the rule in the other direction:
one whole subsystem, one interface question per request, and the per-node work
entirely inside the object that question returns.

If a seam ever shows in a profile, two measures come before redesign: cache
the concrete decision once per tick where the answer cannot change within the
tick, or devirtualize the dominant type with a profile-guided build. Neither
is planned; both are cheaper than reintroducing per-package mode booleans.

## 5. Switch timing

A rule-set switch is honoured **only at the phase-1 command boundary**. The
front end enqueues the change as a human command, the command boundary binds
the new set, and the tick that follows runs entirely under it. Presentation
never rebinds a service directly.

A switch does not unwind state a Modern rule already staged. Movement
clearance hints and guard retry deadlines created under Modern run out under
their own existing expiry rules after a switch to Strict; Strict simply stops
creating new ones. There is deliberately no detach or rollback step: an
unwind would be new behaviour in both directions and would need its own
contract and its own tests. A player who wants a clean Strict run starts one.

Future extensions must state which in-flight work keeps its original
implementation and which subsequent decisions use the newly bound set. They
must also specify residual orders, deadlines, request storage and resource
commitments, and test switches in both directions. The current rebind has no
extension-specific migration hook; do not assume it cancels or converts work.

## 6. Save interaction

A save records **no rule-set name**. The retail bank's box vocabulary is
fixed and Nanolathe adds no metadata area of its own, so there is nowhere to
store `RuleSet.Name` without inventing a box, and this design does not change
retail save bytes. The gameplay word lives in the settings file, not in the
save.

Consequently **a loaded game runs under the session's current rule set.** The
load path carries the caller's mode word, binds the matching set during
composition, and the restored battle continues under it. The code site
carries a `TODO(question)` naming what is missing: a decision on a
Nanolathe-side save metadata area — a sidecar file, or an agreed additional
box — which is a save-format question rather than a retail one.

**Planned change.** [DESIGN_MODS_MUTATORS §7](DESIGN_MODS_MUTATORS.md#7-the-save-sidecar)
settles that question with a Nanolathe sidecar file beside the bank. The
sidecar records the bound set, the Community sources, the unit limit, the mod
and the mutators, and a load restores them. Until that design is implemented,
this section describes the code. A save without a sidecar keeps this
behaviour afterwards.

A future extension that needs persistent identity or private state must first
settle the metadata format, versioning, missing-set behavior and restoration
contract in the owning save design. The current omission is a limitation,
not permission to invent save bytes or infer a set from loaded content.

Two consequences are worth stating because they are observable:

- Modern transient state that is not in the save is simply absent after a
  load, whichever set is bound. Staged movement clearance routes and the
  [learned-terrain grid](DESIGN_MOVEMENT_PATH.md#modern-learned-terrain) are
  the existing examples.
- The unit-limit seam is asked on both sides of a save. The writer asks
  whether the summary records the live session limit; the loader asks whether
  a present, nonzero saved limit may size the pool. Both answers, their
  bounds and the Strict baseline belong to
  [DESIGN_SESSIONS_AI_SAVE](DESIGN_SESSIONS_AI_SAVE.md#modern-save-unit-limits).
  The load question is asked from the mode word the staging caller supplies,
  because no session exists yet at that point.

## 7. Verification

| What | Test |
|---|---|
| Every bound implementation is zero size or a pointer, and no seam is left unbound | `session.TestRuleSetImplementationsAreZeroSizeOrPointers` |
| The reserved sets carry the mode vocabulary and the unit-limit policy each mode's contract states | `session.TestReservedRuleSetsMatchTheModeVocabulary` |
| Binding reaches the combat service, the construction service and an already composed queue binding | `session.TestBindRulesProjectsEverySeam` |
| Dispatch through a bound set allocates nothing in any reserved set | `session.TestBoundRuleDispatchDoesNotAllocate` |
| A switch is honoured at the command boundary and not before | `session.TestGameplayChangesAtCommandBoundary` |
| New and already composed queues observe the same set | `session.TestModernOrderPolicyComposition` |
| Per-package dispatch allocates nothing, and the Strict answers are the retail ones | `combat.TestRulesDispatchDoesNotAllocate`, `combat.TestStrictRulesAnswerAsRetail`, `orders.TestRuleDispatchDoesNotAllocate`, `orders.TestAbsentRulesAnswerStrictWithoutDrawing`, `construction.TestStrictClearanceDispatchAllocatesNothing` |
| The unit-limit seam's own policy, bounds and Strict bypass | `session.TestModernSaveUnitLimitSelection`, `session.TestModernSaveLoadsAcrossUnitLimits` |
| The registry refuses a reserved or duplicate name, builds once, and completes a set from any reserved base | `session.TestRegisterRuleSetRefusesReservedAndDuplicateNames`, `session.TestLookupRuleSetBuildsOnceAndCompletesFromItsBase`, `session.TestCompleteRuleSetFillsFromCommunityBase` |
| A cached set is shared by every session, so no implementation holds state | `session.TestCachedRuleSetImplementationsHoldNoState` |
| Selection by name binds the whole set, carries its base as the session's word, and reports an unknown name | `session.TestSetRulesSelectsByNameAndReportsAnUnknownOne`, `session.TestRuleSetNamesListTheReservedSetsFirst`, `session.TestBaseModeOfReducesASelectionToAReservedWord` |
| Binding is idempotent and a per-allocation re-projection keeps a selected set | `session.TestUnitCreationKeepsTheSelectedRuleSet`, `session.TestRebindRulesSelectsOnlyWhenNothingIsBound`, `example.TestSelectingTheExampleSetSurvivesALiveComposition` |
| A registered name is selectable through the vocabulary, and an unselectable word is rejected with the name list | `session.TestGameplayVocabularyKnowsTheRegisteredNames`, `gameplay.TestARegisteredNameParsesAndSurvivesNormalization`, `gameplay.TestAnUnselectableWordIsRejectedWithTheSelectableNames`, `gameplay.TestWithoutARegistryOnlyTheReservedWordsAreSelectable` |
| A set composed outside `internal/` registers, overrides one answer and inherits its base | `example.TestTheExampleSetIsRegisteredAndSelectable`, `example.TestTheExampleSetOverridesOneAnswerAndInheritsModern`, `example.TestSelectingTheExampleSetProjectsTheOverride` |
| Only a command imports the mod list | `architecture.TestOnlyCommandsImportTheModList` |
| The movement policy seam reaches the movement system, dispatches without allocating, and answers Strict when unbound | `session.TestBindRulesProjectsEverySeam`, `session.TestCompositionProjectsTheSearchKernelOntoMovement`, `movement.TestMovementRulesDispatchDoesNotAllocate`, `movement.TestStrictTerrainRejectionLearnsNothing` |
| Strict and Community bind the retail search kernel, Modern binds route straightening, and the composer projects the kernel onto the movement system | `session.TestReservedRuleSetsBindTheirSearchKernels`, `path.TestStraightenRemovesSawtoothTurns`, `path.TestStraightenKeepsBlockedAndDiagonalTurns`, `session.TestCompositionProjectsTheSearchKernelOntoMovement` |
| The retail kernel opens the retail search, is asked once per request, and its dispatch adds no allocation | `path.TestRetailKernelOpensTheRetailSearch`, `path.TestAKernelIsAskedOncePerRequest`, `path.TestRetailKernelDispatchAddsNoAllocation`, `movement.TestSearchFuncOpensItsSearchThroughTheBoundKernel`, `movement.TestAnUnboundKernelIsRetailAndCostsNothing` |
| All three reserved sets' fingerprints are locked to constants | `headless.TestStrictFingerprintIsLocked`, `headless.TestCommunityFingerprintIsLocked`, `headless.TestModernFingerprintIsLocked` |
| The think step reaches every computer player, all reserved sets bind the retail step, and the dispatch allocates nothing | `session.TestBindRulesProjectsThePlannerOntoEveryComputerPlayer` |
| A nil planner is the retail step, a bound one answers in its place, and neither dispatch allocates | `ai.TestANilPlannerRunsTheRetailStep`, `ai.TestABoundPlannerAnswersTheStepInPlaceOfRetail`, `ai.TestPlannerDispatchDoesNotAllocate` |

Each Modern policy keeps its own behaviour tests in the package that owns it;
those are listed by the owning design document, not here.

Beyond the tests, a seam unit is gated on **identity**: the 6000-tick headless
fingerprint and the simulation-cost benchmark's initial, warm and final
fingerprints must be unchanged in every reserved mode, because introducing a
seam is supposed to move no logic (§3 rule 3). A unit that changes a
fingerprint is either a policy change — which belongs in the owning design
document with its own contract — or a defect.

## 8. Selection by name, the registry and `mods/`

A build can select more than the three reserved sets. `session.RegisterRuleSet`
adds a named set, `session.LookupRuleSet` resolves one, and
`session.RuleSetNames` lists what this build can select — the three reserved
names first from the default down through its derivation layers, then the
registered names sorted, so a diagnostic and a host menu agree on one order.

**The registry is a map consulted when a set is selected, and never in a
tick.** Selection happens at composition and at the phase-1 command boundary;
everything after that holds the bound set ([INVARIANTS.md](INVARIANTS.md) I1).
A lookup builds its set at most once and keeps it, so the same built set is
handed to every session in the process — which is exactly why §3's rule that
an implementation and any pointee are zero size is checked by a test rather
than trusted. These cached objects cannot own session scratch.

Registration is a **build-time** act, so it panics rather than reporting:

- a reserved name is refused, because the fingerprints and policy contracts
  are written against those three sets;
- a duplicate name is refused, because selection would otherwise depend on
  link order.

A registered set states **only the seams it changes**. The registry names the
set after its registered name, reduces its declared `Base` to a reserved word,
and fills every unstated seam from that base set — not from the owning
packages' own nil fallbacks, which answer Strict 3.1 and would silently
contradict a Modern-based set.

`Base` is the set's answer to reserved-layer questions outside the seams, such
as the options stage and the headless report's `gameplay` field.
The save unit-limit decisions themselves use `UnitLimitRules`, so a named
set may override those answers independently of its base. A session carries
its bound set's base in `Session.Gameplay`, so logic that only knows the three
reserved behaviors needs no knowledge of the registry.

### The mod list

```
mods/
  all.go            blank imports of every shipped set; both commands import this
  example/rules.go  RegisterRuleSet("example", build) from an init
```

`mods` is the list of sets a build links, and **only a command imports it**.
Nothing under `internal/` may: a simulation package that reached a mod would
make its behavior depend on which sets happen to be linked, and a set that
composes that package's own implementations would close the loop into an
import cycle. The direction is cmd → mods → session → simulation, and
`internal/architecture.TestOnlyCommandsImportTheModList` enforces it.

`mods/example` is the worked example, kept compiling as the proof that a set
can be composed entirely from outside `internal/`: it embeds the shipped
`orders.ModernRules`, shadows one promoted method — Hold Fire at the order
seam answers as retail — and leaves every other seam unstated. Go's `plugin`
package is deliberately not used: a compiled-in list needs no matching
toolchain, works on every host, and keeps one binary's behavior reproducible
from its source.

### Selecting one

`--gameplay <name>` on both commands and the `gameplay` settings key accept
any selectable name, and reject anything else with the name list in the
diagnostic. The vocabulary is a leaf package and cannot import the registry,
so the session installs a view of it (`gameplay.UseNameRegistry`) from an init
before any word is parsed; a build with no registry installed — a test binary
of a package below the session — knows only the reserved words.

`Mode.Normalize` therefore canonicalizes a *selection*: a reserved word and a
registered name survive, and a word this build cannot select becomes Modern.
That is what lets a settings file, a host option and a session constructor
pass a name through untouched while a typo or a file written by a build that
linked other sets still starts under the default instead of failing.

Two consequences are worth stating because they are observable:

- The retail-shaped options panel is a three-stage control in derivation order:
  Strict 3.1, Community 3.9, Modern. It shows the reserved set a selection
  derives from (`session.BaseModeOf`), and cycling it selects a reserved set —
  replacing a third-party selection. Selecting a set by name is a command-line
  or settings-file choice.
- Headless and simulation-cost reports include `rules` for the bound set
  name; session debug captures also include `rules`. The headless report
  exposes the session base separately as `gameplay`. A retail save still
  carries no rule-set name (§6). Reports identify a run's selection but do not
  make loading a save restore that selection.

## 9. Extending the existing mechanism

Future gameplay work uses the interfaces in §2 and the registry in §8. This
section is the implementation workflow, not approval for new mechanics.
Existing user authorization carries forward: implement an already authorized
mechanic and extend its owning interfaces autonomously within that scope.
Documenting its contract and tests is implementation work, not a requirement
to ask for the same approval again.

1. **Establish the contract and owner.** Read the owning subsystem design,
   retail baseline and any versioned extension evidence in
   [research/extensions](../research/extensions/README.md). Separate established
   source behavior, unresolved questions and an explicitly approved Nanolathe
   Modern policy. Name the trigger, strict answer, new answer, affected state,
   ordering, RNG/resource effects and boundaries before implementing it.
2. **Use the existing interface.** Compose a shipped implementation when it
   already answers the question. Add a method to the owning `combat.Rules`,
   `orders.Rules`, `construction.Rules`, `movement.Rules` or
   `session.UnitLimitRules` when the
   package needs a new decision. Implement Strict, Community and Modern
   defaults in their derivation chain, with each layer promoting the lower
   answer where no departure is approved.
   Keep unbound fixtures' retail behavior. For a replacement search or think
   step, use `path.Kernel` or `ai.Planner` within their existing boundaries.
3. **Add a seam only for a missing owner or boundary.** Explain in the owning
   design why the existing interfaces cannot express the contract. Put the
   narrow interface in that algorithm's package and compose its field in the
   same `session.RuleSet`: all reserved constructors, base completion,
   binding/rebinding and later-created/restored owners must agree. Do not
   introduce another registry, capability-selection system, per-unit policy
   selector or collection of compatibility booleans. Keep selection at
   composition or the phase-1 command boundary and dispatch at request
   granularity (§4).
4. **Keep state with its owner.** Cached rule objects and pointees remain zero
   size (§3). New state in an existing owner needs documented initialization,
   lifetime, cleanup, save/restore and switch behavior. If a rule object itself
   must hold state, first design per-session construction and isolation,
   rebind idempotence, disposal, in-flight work and save identity/versioning.
   The current registry is not a per-session factory and supplies none of
   those facilities; no such redesign is authorized by this guidance.
5. **Verify the contract.** Test the extension behavior and the Strict bypass,
   including RNG draws and resource effects; retain allocation, composition,
   restoration and registry guards. Test any added state across rebinding and
   switches, plus independent sessions when state isolation matters. A seam
   refactor preserves all reserved modes' fingerprints (§7); an approved behavior
   change explains its expected differences in the owning design. Run the
   applicable verification and performance gates from
   [ARCHITECTURE](ARCHITECTURE.md#6-verification) and AGENTS.md.

### Content profiles are a separate input

`internal/content/profiles` selects load-time directory layout and content
limits through `vfs.Layout`; see [DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md).
It does not select `session.RuleSet`. The bounded exception is the Community
profile declaration of DESIGN_COMMUNITY_PATCH §3.2: content may name one
closed feature table, which composition resolves only after the host has
selected Community 3.9 or Modern. That declaration never selects the gameplay
mode, and Strict 3.1 ignores all of its overrides. A patch or content pack may
require both a load-time profile and separately authorized gameplay support;
record those two requirements independently. A detected marker, install name,
file path or asset-provider identity must not become a hidden runtime gameplay
selector. Keep renderer and host preferences under their existing controls as
well.

### Decisions still required for future extensions

The existing mechanism deliberately does not settle stateful rule lifecycles,
new migration/cancellation semantics on switching, or persistent rule-set
identity and compatibility across saves. These require concrete extension
requirements, an approved design and tests before dependent behavior can be
implemented. Record unresolved behavior as `TODO(question)` at its code site
and in its owning research contract; record Nanolathe design decisions in the
owning design document. The existence of a seam or an extension reference does
not authorize changing any reserved set's behavior.

### Modern combat prototype composition

The user-authorized combat prototype extends the existing `combat.Rules` and
`orders.Rules` selections. Its owning contracts are
[weapons and targeting](DESIGN_WEAPONS_PROJECTILES.md#modern-threat-targeting-and-incoming-fire) and
[danger response and protected work](DESIGN_UNITS_ORDERS_COB.md#modern-danger-response).
The session forwards combat danger observations only when the victim's owner
currently sees a live hostile attacker. This uses authoritative visibility,
not the local viewer or published frame. Orders revalidate that contact while
responding. Accepted hostile impacts can additionally supply an anonymous
incoming bearing from observed projectile motion (or the packet's local impact
direction when horizontal motion is unavailable). The existing combat and
orders rule interfaces carry this cue; Strict hooks are inert. It supports
local withdrawal without conveying a hidden attacker identity or position.

The existing phase-2 unit visit asks orders for its danger response immediately
before the ordinary order pump, after the normal weapon update and COB drain.
That visit also maintains proven automatic ground/guard target ownership even
without a danger notice; direct and unknown attack provenance stays protected.
Thus an observation never runs a nested order pump or a second weapon visit.
Strict's response method is inert. The movement-owned local-corridor query is
composed on the same queue binding, and the existing path kernel and scheduler
remain unchanged. There is no additional rule registry or per-unit mode flag.
