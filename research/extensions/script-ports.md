# Extended script ports

## Evidence scope and sources

This document records the engine ports that the shipped mod packages expose
beyond retail's twenty, their semantics, and their cross-package behavior. It
does not establish retail behavior and approves no Nanolathe behavior. The
retail port set is owned by
[04 R-COB-03](../retail-executable-spec/04-units-orders-scripts-and-movement.md).

**Established — retail's port set is closed at twenty.** The retail read
switch rejects identifiers above nineteen and reads zero for them; identifiers
`0` and `21` and above read zero, and writing them does nothing but set the
script-touched marker ([04 R-COB-03 §1](../retail-executable-spec/04-units-orders-scripts-and-movement.md)).
Every port in this document is added by a shipped extension DLL.

**Established — the extended ports are a TA Demo Recorder feature.** The
recorder DLLs ([TA Demo Recorder session DLLs](ta-demo-recorder.md)) install a
handler over the executable's per-unit script-port reader. The handler passes
retail ports `1`–`20` through, resolves the argument needed by port `73`,
and otherwise serves ports `32` and `69`–`75`. The recorder reports the
handler among its feature names ("COB_extensions"). The same handler exists,
with the same port set and the same semantics, in all three inspected builds:

| Build | Version | Ports | Semantics |
|---|---|---|---|
| Escalation `eplayx.dll` | 3.9.2.416 | 32, 69–75 | baseline |
| ProTA `tplayx.dll` | 3.9.2.416 | 32, 69–75 | identical to Escalation (whole extension region byte-equal) |
| TA Zero `zplayx.dll` | 3.9.2.0 | 32, 69–75 | same arms, state fields and values for 32, 69, 70 and 72–75; port 71 differs in route (see below) |

**Established — installation is gated but always on.** Each build installs
the hook only after a byte-signature check of the host executable (the
expected pattern matches all four inspected executables: retail, Escalation,
ProTA and TA Zero), and the handler's enable byte is initialised to 1 in the
file and never written. There is no per-player or per-game setting and no
recorder command that toggles it; the recorder's status commands only report
it.

**Established — read-only.** No inspected script writes an extended port, and
the handler replaces the read path only; the write opcodes for these numbers
have no arms.

## Port table

**Established mechanics.** The value each port returns was determined from
the handler's arithmetic; where the meaning of an engine field or table is
itself not established, the confidence notes say so.

| Port | Form | Value |
|---|---|---|
| `32` | one-argument read | 100 × the reading unit's kill count (the same kill field retail veterancy counts). Read only by Arm and Core commanders. Why the ×100 scale is used is **Unknown**. |
| `69` | one-argument read | Constant `1`: the first unit id of an iteration range. |
| `70` | one-argument read | The end of that unit-id iteration range: `10 ×` a 16-bit game-state field; which of two adjacent fields is read depends on a game-state flag. The fields' identities are **Unknown**. |
| `71` | one-argument read | The reading unit's own id. The 3.9.2.416 builds return the stored identifier field's low 16 bits; the 3.9.2.0 build derives the unit's array index from its pointer instead. The two agree when the stored field holds the array index, which is a **Supported inference** and not established. |
| `72` | five-argument read | Owner/player index of the unit whose id is passed as the first argument. |
| `73` | five-argument read | The passed unit's build-percent-left, using the retail port-`17` semantics (0 when complete, otherwise a 0–100 remaining value). The recorder resolves the unit itself before calling the retail body. |
| `74` | five-argument read | Relation test: `1` when the passed unit's owner is in the reading unit's per-player relation byte table, `0` otherwise. Scripts use `== 1` as a "may I interact with this unit" gate. Whether the table means alliance, known-unit or something else is a **Supported inference** (it is the table the engine populates per player). |
| `75` | five-argument read | Visibility class of the passed unit to the reading player: `1` visible, `2` radar-detected (treated as `1` by the handler), `0` hidden. Scripts combine it with their own id to show or hide shield/effect pieces. Whether "visible" is line of sight or a broader viewer test is a **Supported inference**. |

## Authored use

**Established — census.** Reads by package (units whose scripts read the port
at least once):

| Port | Escalation | ProTA | TA Zero |
|---|---|---|---|
| `32` | 2 (commanders only) | — | — |
| `69` | 289 | — | — |
| `70` | 289 | — | 19 |
| `71` | 254 | — | — |
| `72` | 45 | — | — |
| `73` | 260 | — | — |
| `74` | 243 | — | 13 |
| `75` | 44 | — | — |

Escalation's typical pattern is to read `69`, `70` and `71` once to obtain the
unit-id iteration range and its own id, then to call `72`–`75` on candidate
ids inside a loop while its detection state machine runs. A factory's
third-weapon aim callback reads `71` and then the five-argument `75` on its own
id as a viewer test before showing its direction-indicator piece. TA Zero's
scripts read only `70` and `74`, from construction aircraft, dropships,
factories and their AI variants, alongside retail ports (`4`, `7`, `9`, `12`,
`17`) while setting up build plates and activation state; the AI factory
variants use the two reads to sample unit ids and compare owners when choosing
their build-plate side. ProTA authors no extended-port reads.

**Established — no collision.** Ports `70` and `74` are read by both
Escalation and TA Zero scripts, and the two recorder builds implement them
identically; the numbers are a shared interface, not two competing meanings.
Cross-package compatibility therefore depends only on the recorder build's
version (both inspected 3.9.2.x builds agree), not on the mod.

## Unknown

- **Unknown — the unit-id iteration fields.** Ports `69`/`70` expose a unit-id
  range whose bounds come from an engine state field; which field, and what
  the range enumerates (all units, the local player's, mission units), is not
  established. A writer trace or a runtime dump of the value would settle it.
- **Unknown — the relation table's meaning.** Whether port `74` answers
  "allied", "known" or another relation.
- **Unknown — the visibility class's exact test.** Whether port `75`'s `1`
  means line of sight, radar contact or a broader engine visibility.
- **Unknown — the ×100 kill scale on port `32`.** No consumer needs it in the
  inspected content; the scaling may serve a later feature.
- **Unknown — runtime removal.** No command was found that uninstalls the
  handler; whether the recorder's status commands can restore the original
  reader is not established.
