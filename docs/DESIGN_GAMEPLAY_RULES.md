# Design — Gameplay rules

`internal/gameplay` and `internal/session/rules.go`, plus one `rules.go` in
each package that owns a gameplay decision. How Nanolathe's two gameplay
policies — the retail baseline **Strict 3.1** and the default **Modern** —
reach the algorithms that behave differently under them.

This document owns the *mechanism*: the seam list, who may implement a seam,
what an implementation may cost, when a set may be bound, and what a save
knows about it. It owns no policy. Every Modern rule keeps its contract, its
retail baseline, its boundaries and its own verification in the design
document that owns the affected subsystem, and this document only says which
seam carries it.

This is one of the design documents listed by [ARCHITECTURE.md](ARCHITECTURE.md);
[INVARIANTS.md](INVARIANTS.md) I11 is the rule every diff is reviewed against.

## 1. Shape

`gameplay.Mode` is the vocabulary, and a word has three forms: `modern` (the
default, and the zero value), `strict-3.1`, and the name of any rule set this
build registered (§8). It is what the settings file stores and what
`--gameplay` parses. `Session.Gameplay` is narrower: it always holds one of
the two reserved words, because that is the answer everything asking
strict-versus-modern needs, and the selected set's own name stays on the bound
set.

What a word *selects* is a `session.RuleSet`: one implementation per seam,
named, bound once. After binding, every decision site holds a concrete
implementation and no simulation code reads the mode word.

```go
type RuleSet struct {
    Name         string
    Base         gameplay.Mode // the reserved set this one derives from; zero is Modern
    Combat       combat.Rules
    Orders       orders.Rules
    Construction construction.Rules
    UnitLimit    UnitLimitRules
    Path         path.Kernel
    Planner      ai.Planner
}
```

`StrictRuleSet()` and `ModernRuleSet()` are the two reserved sets;
`RuleSetForMode` resolves a selection word to a set and `Session.BindRules`
projects each field onto the service that asks it — `Combat.Rules`,
`Build.Rules`, `Build.OrderBinding.Rules`, `Movement.Kernel`, and every
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
| `combat.Rules` | `internal/combat` | terrain admission ([DESIGN_WEAPONS_PROJECTILES §2.3.1](DESIGN_WEAPONS_PROJECTILES.md#231-modern-terrain-admission)) and the launch-gate half of Hold Fire (§2.6.1) |
| `orders.Rules` | `internal/orders` | [Hold Fire](DESIGN_UNITS_ORDERS_COB.md#modern-hold-fire) at a combat join, the deferred bomber leash ([DESIGN_MOVEMENT_PATH §3.4.1](DESIGN_MOVEMENT_PATH.md#341-modern-bomber-pass-completion)), and the three guard-assistance legs |
| `construction.Rules` | `internal/construction` | [factory-exit](DESIGN_ECONOMY_CONSTRUCTION.md#modern-factory-exit-yielding) and [construction-site](DESIGN_ECONOMY_CONSTRUCTION.md#modern-construction-site-yielding) clearance |
| `session.UnitLimitRules` | `internal/session` | [Modern save unit limits](DESIGN_SESSIONS_AI_SAVE.md#modern-save-unit-limits) |
| `path.Kernel` | `internal/path` | the search a route request is opened with ("The path search kernel" below); both reserved sets bind `path.RetailKernel` |
| `ai.Planner` | `internal/ai` | the computer player's per-tick think step ("The computer player's think step" below); both reserved sets bind `ai.RetailPlanner` |

The last two rows are the **whole-subsystem** seams: each replaces an
algorithm rather than answering a question inside one, and both now exist.
Neither has a Modern implementation — both reserved sets bind the retail one —
because replacing either is a behaviour change with its own contract rather
than a selection. Both are request-granularity replacements (§4), and the two
sections below state each one's boundary: a kernel may not change *when* a
route publishes, because publication timing is ordering behaviour owned by the
tick, and a planner may not draw from the simulation stream in a different
order, because that call order is the whole future of the battle.

### The path search kernel

`path.Kernel` has one method: it opens one request's resumable search, and
`path.Search` is that search behind an interface. `path.RetailKernel` is the
retail ray-and-A\* search, it is zero size, and **both reserved sets bind
it** — there is no Modern kernel, because no approved Modern policy changes
how a route is found. `Session.BindRules` projects the field onto
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
battle; a planner that draws differently is a Modern gameplay policy needing
its own contract, so **both reserved sets bind `RetailPlanner`** — there is no
Modern planner today, and Strict 3.1 could never bind one. A set assembled
outside `internal/ai` composes the retail step by calling
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

1. **Every implementation is zero size or used by pointer.** A zero-size
   value converts to an interface without allocating; a pointer to
   session-lifetime state boxes nothing per call. A non-empty value
   implementation is a defect.
2. **No call site builds a closure per call.** A method takes the concrete
   state its answer needs — the unit, the order record, the resolved fire
   attempt as a pointer to the caller's stack, the tick — and never a closure
   or a slice it retains.
3. **A method is asked at the same site the mode projection was read.**
   Introducing a seam moves no logic; it only changes who answers. That is
   what makes an identity fingerprint the correct gate for a seam unit.
4. **A Strict answer draws no randomness and writes no state.** Strict is the
   retail baseline, so it cannot move a shared stream. A Modern answer that
   draws receives the stream explicitly.
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

Two consequences are worth stating because they are observable:

- Modern transient state that is not in the save is simply absent after a
  load, whichever set is bound. Staged movement clearance routes are the
  existing example.
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
| Dispatch through a bound set allocates nothing in either set | `session.TestBoundRuleDispatchDoesNotAllocate` |
| A switch is honoured at the command boundary and not before | `session.TestGameplayChangesAtCommandBoundary` |
| New and already composed queues observe the same set | `session.TestModernOrderPolicyComposition` |
| Per-package dispatch allocates nothing, and the Strict answers are the retail ones | `combat.TestRulesDispatchDoesNotAllocate`, `combat.TestStrictRulesAnswerAsRetail`, `orders.TestRuleDispatchDoesNotAllocate`, `orders.TestAbsentRulesAnswerStrictWithoutDrawing`, `construction.TestStrictClearanceDispatchAllocatesNothing` |
| The unit-limit seam's own policy, bounds and Strict bypass | `session.TestModernSaveUnitLimitSelection`, `session.TestModernSaveLoadsAcrossUnitLimits` |
| The registry refuses a reserved or duplicate name, builds once, and completes a set from its base | `session.TestRegisterRuleSetRefusesReservedAndDuplicateNames`, `session.TestLookupRuleSetBuildsOnceAndCompletesFromItsBase` |
| A cached set is shared by every session, so no implementation holds state | `session.TestCachedRuleSetImplementationsHoldNoState` |
| Selection by name binds the whole set, carries its base as the session's word, and reports an unknown name | `session.TestSetRulesSelectsByNameAndReportsAnUnknownOne`, `session.TestRuleSetNamesListTheReservedSetsFirst`, `session.TestBaseModeOfReducesASelectionToAReservedWord` |
| Binding is idempotent and a per-allocation re-projection keeps a selected set | `session.TestUnitCreationKeepsTheSelectedRuleSet`, `session.TestRebindRulesSelectsOnlyWhenNothingIsBound`, `example.TestSelectingTheExampleSetSurvivesALiveComposition` |
| A registered name is selectable through the vocabulary, and an unselectable word is rejected with the name list | `session.TestGameplayVocabularyKnowsTheRegisteredNames`, `gameplay.TestARegisteredNameParsesAndSurvivesNormalization`, `gameplay.TestAnUnselectableWordIsRejectedWithTheSelectableNames`, `gameplay.TestWithoutARegistryOnlyTheReservedWordsAreSelectable` |
| A set composed outside `internal/` registers, overrides one answer and inherits its base | `example.TestTheExampleSetIsRegisteredAndSelectable`, `example.TestTheExampleSetOverridesOneAnswerAndInheritsModern`, `example.TestSelectingTheExampleSetProjectsTheOverride` |
| Only a command imports the mod list | `architecture.TestOnlyCommandsImportTheModList` |
| Both reserved sets bind the retail search kernel, and the composer projects it onto the movement system | `session.TestReservedRuleSetsBindTheRetailSearchKernel`, `session.TestCompositionProjectsTheSearchKernelOntoMovement` |
| The retail kernel opens the retail search, is asked once per request, and its dispatch adds no allocation | `path.TestRetailKernelOpensTheRetailSearch`, `path.TestAKernelIsAskedOncePerRequest`, `path.TestRetailKernelDispatchAddsNoAllocation`, `movement.TestSearchFuncOpensItsSearchThroughTheBoundKernel`, `movement.TestAnUnboundKernelIsRetailAndCostsNothing` |
| Both rule sets' fingerprints are locked to constants | `headless.TestStrictFingerprintIsLocked`, `headless.TestModernFingerprintIsLocked` |
| The think step reaches every computer player, both reserved sets bind the retail step, and the dispatch allocates nothing | `session.TestBindRulesProjectsThePlannerOntoEveryComputerPlayer` |
| A nil planner is the retail step, a bound one answers in its place, and neither dispatch allocates | `ai.TestANilPlannerRunsTheRetailStep`, `ai.TestABoundPlannerAnswersTheStepInPlaceOfRetail`, `ai.TestPlannerDispatchDoesNotAllocate` |

Each Modern policy keeps its own behaviour tests in the package that owns it;
those are listed by the owning design document, not here.

Beyond the tests, a seam unit is gated on **identity**: the 6000-tick headless
fingerprint and the simulation-cost benchmark's initial, warm and final
fingerprints must be unchanged in *both* modes, because introducing a seam is
supposed to move no logic (§3 rule 3). A unit that changes a fingerprint is
either a policy change — which belongs in the owning design document with its
own contract — or a defect.

## 8. Selection by name, the registry and `mods/`

A build can select more than the two reserved sets. `session.RegisterRuleSet`
adds a named set, `session.LookupRuleSet` resolves one, and
`session.RuleSetNames` lists what this build can select — the two reserved
names first, the default before the retail baseline, then the registered names
sorted, so a diagnostic and a host menu agree on one order.

**The registry is a map consulted when a set is selected, and never in a
tick.** Selection happens at composition and at the phase-1 command boundary;
everything after that holds the bound set ([INVARIANTS.md](INVARIANTS.md) I1).
A lookup builds its set at most once and keeps it, so the same built set is
handed to every session in the process — which is exactly why §3's rule that
an implementation is zero size or a pointer to *session-independent* state is
checked by a test rather than trusted.

Registration is a **build-time** act, so it panics rather than reporting:

- a reserved name is refused, because every retail fingerprint and every
  Modern contract is written against those two sets;
- a duplicate name is refused, because selection would otherwise depend on
  link order.

A registered set states **only the seams it changes**. The registry names the
set after its registered name, reduces its declared `Base` to a reserved word,
and fills every unstated seam from that base set — not from the owning
packages' own nil fallbacks, which answer Strict 3.1 and would silently
contradict a Modern-based set.

`Base` is the set's answer to every strict-versus-modern question asked outside
the seams: the save unit-limit policy, the developer spawn gate, what the
session reports and persists. A session carries its bound set's base in
`Session.Gameplay`, so logic that only knows the two reserved behaviors needs
no knowledge of the registry, and the developer spawn gate keeps reading that
word unchanged.

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

- The retail-shaped options panel is a two-stage control. It shows the
  reserved set a selection derives from (`session.BaseModeOf`), and toggling
  it selects a reserved set — replacing a third-party selection. Selecting a
  set by name is a command-line or settings-file choice.
- A session reports and persists its base, so a third-party set's name appears
  in neither the headless report nor a save (§6). The set a run used is a
  property of the invocation, not of the saved battle.
