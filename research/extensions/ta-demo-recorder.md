# TA Demo Recorder session DLLs

## Evidence scope and sources

This document records the command set and capabilities of the DirectPlay
replacement DLLs shipped by the three inspected mod packages, and the version
boundaries between them. It does not establish retail behavior and approves no
Nanolathe behavior; the packages' own engine features live in
[ProTA](prota-engine.md), [Escalation](taesc-engine.md) and
[TA Zero](ta-zero-engine.md).

**Established — artifact identity.** The inspected DLLs are the recorder
component of the community "TA Unofficial Patch" line, versioned by their own
version resource, with debug strings naming a TADR source tree. Inspected
files and SHA-256:

| File | Package | Version resource | SHA-256 |
|---|---|---|---|
| `tplayx.dll` | ProTA 4.8 | 3.9.2.416 | `b641a7c97389088f93e7270425a24a1b6d0c442a112cd6be47a46c2773efdacf` |
| `eplayx.dll` | Escalation Gold 10.2.0 | 3.9.2.416 | `29e9f66d93e788dbb2cee0cb3c9015094c7cdbe1de38a665be3b5fdc9842967b` |
| `zplayx.dll` | TA Zero Base (2013) | 3.9.2.0 | `44f2d45fefe294110ac5ac7a4dccbc5c1f27f002414b1fb2886b4ec69b092aae` |

ProTA additionally ships `dplayx.dll`, a DirectPlay entry-point forwarder
whose exports are named `tplayx.<entry>` and which loads and validates the
engine DLL; it is not itself the recorder. An early recorder build (self-named
version 0.99) was present in the inspection baseline; it is not shipped by any
of the three packages and appears only in the version-history comparison.

**Established — source availability and a version boundary.** The recorder
source is now MIT-licensed (`src/Recorder` in
[the TADR repository](community-patch-engine.md)) and builds the current
distribution, whose version is date-based (`2026.9.9`). The inspected
`3.9.2.416` and `3.9.2.0` binaries predate that source revision: the repository
history contains `3.9.2.2` → `3.9.2.437` in the Delphi project version series
and does not contain `3.9.2.416` at all, so the version resources of the
shipped binaries do not map onto a repository revision. Claims below about the
recorder surface remain claims about the identified binaries; the
source-established port semantics for the current line are owned by
[Extended script ports](script-ports.md), and the current `mods.ini` location
is `%LOCALAPPDATA%\TADR\mods.ini` with `MOD<id>` sections (the inspected
2013-era binary's demos-subfolder location may differ).

**Established — what the DLLs are.** Each DLL exports the DirectPlay entry
points under a renamed file, so the game's DirectPlay calls resolve to the
recorder. The recorder registers its user-facing commands with the game's
console at load; every command and help sentence below was read from those
registrations, and the help text of a command shared across builds is
byte-identical in all three.

**Established — the recorder surface is the same in every package** (with the
version boundaries below). Commands are entered in the chat box with a
leading dot. Feature families:

- **Recording and replay**: `.record`, `.recordstatus` (query/set), `.stoplog`,
  `.onlyunits`/`.unitsonly` (experimental reduced-packet recording),
  `.3dta` (toggle the 3D replayer).
- **Chat and stats logs**: `.createtxt` (timestamped chat log); newer builds
  add a stats log.
- **Unit take-over**: `.take`, `.takecmd` (including the dropped player's
  commander), `.give`/`.stopgive` (grant or bar a player the ability to take).
- **Pause and readiness votes**: `.autopause`, `.votego`, `.voteready` with
  alias `.ready`; newer builds add vote-count messages and `.forcego`
  (host force-clicks watchers).
- **Shared intelligence**: `.sharelos` (mutual allied line-of-sight and radar),
  `.sharemappos` (ally-visible camera rectangle), `.lockon` (follow an ally's
  camera in a replay).
- **Start-of-game tools**: `.cmdwarp` (paused single-shot commander placement),
  `.base`/`.dobase`/`.baseoff` (host-defined prebuilt base).
- **Lag workarounds**: `.fixfacexps` (three-second protection for units under
  construction in factories), `.protectdt` (dragon's-teeth protection),
  `.fixall`/`.fixon`/`.fixoff`; the `.report` indicator shows which are on.
- **Interface upgrade**: `.ehaon`/`.ehaoff`/`.ehareport` control recorder-side
  interface features — the idle-construction-unit finder and the hundred-unit
  build queue — for all players in the game.
- **Script-port extensions ("COB Extensions")**: the shared content census
  uses `32` and `69`–`75` (kill count, ids, owner, build progress, alliance and
  controller locality). The pinned source implements a much larger getter
  and setter interface, including stateful commands and visual controls;
  [Extended script ports](script-ports.md) owns the current-source contract.
  The source's broader surface and callback extensions must not be projected
  onto older shipped DLLs without release-specific evidence.
- **Reporting**: `.report`, `.status`, `.date`, `.time`, `.units`, `.players`,
  `.ehareport`, `.hookreport`; `.reportmod` publishes each player's configured
  mod name/version from `mods.ini` (newer builds only).
- **Utility and hosting**: `.help`, `.about`, `.randmap` and, in newer builds,
  `.randmapex`/`.rm` (random map from the host's installed maps), `.fakewatch`
  (chat while watching), `.forcecd` (in-memory no-CD toggle), `.panic`,
  `.crash`, `.yankspank` (easter egg).
- **Integrity and environment notices**: a version check that the host game is
  TA 3.1; detection and reporting of cheat tools (newer builds scan process
  memory); "uses TA Unofficial Patch" recognition; "broken recorder"
  detection for old builds; crash-report instructions that ask for
  `errorlog.txt`.

**Established — what the recorder does not provide.** The DLLs contain no
whiteboard, screenshot/poster, allied-resource-bar or CRC/hash/report
machinery. Those features belong to the mod engine DLLs (for example
Escalation's and ProTA's `.exereport`-family reporting and whiteboard). Their
presence in the same install must not be attributed to the recorder.

## Version boundaries

**Established.** The 2013 build (`zplayx.dll`, version 3.9.2.0) registers the
same command set as the 3.9.2.416 builds except that it lacks `.reportmod`,
`.forcego` and `.randmapex`/`.rm`; it also lacks stats logging, INI-file
settings reading, the process-memory cheat scan and the HUD animation-name
table, and it uniquely implements a battle-room text-scroll hook. The
3.9.2.416 builds additionally carry vote-count messages, a demo file name
prefix, and the HUD animation names. The early 0.99 build has a small
command set (including a packet-loss test and a DLL self-integrity check)
that no later build has.

**Established — ProTA and Escalation ship the same recorder build.** The two
files differ in only 70 bytes: an identity tag used by mod reporting, the
registry key (`Software\ProTA\TA Demo` vs `Software\TA Esc\TA Demo`), three
small code conditionals, and version-resource flags. The feature set, command
table, help text and imports are identical.

## Compatibility checks

**Established.** Recorder compatibility is name/version based: a game must be
TA 3.1; `.report`/`.status`/`.ehareport` query other players' recorder
presence and version; and the newer builds can announce the local mod with
`.reportmod` as ordinary chat lines ("*** <player> uses <mod name>
(ID: <id>)", or "OTA or backward compatibility mode" when no mod is
configured). No checksum, file hash or executable-verification mechanism
exists in the recorder. The recorder also warns when other players' builds
cannot handle more than 500 units per side. Version differences are tolerated,
with the newer commands simply unavailable to old clients.

**Established — mods.ini.** The recorder keeps `mods.ini` in a `TADR`
subfolder of the demos directory. Each per-mod section is named `MOD` followed
by the decimal mod identifier and carries ID, Name, Version, registry name,
path and a weapon-identifier-patch flag; the recorder writes its own entry at
startup and falls back to "Unknown" when a name or version is missing. The
game's own INI separately holds the current mod record, its preferences and a
`[Colors]` section (below).

**Established — the interface upgrade and its colour slots.** The 3.9.2.416
builds define a 28-member colour-slot enumeration and read each member's name
as an INI key under `[Colors]`: selection box, health bar by condition,
build-queue boxes (selected and unselected), the six loading-progress stages,
main-menu dots plus a disable switch, under-construction surface and outline
highlights, and a construction-particle pair (base colour plus colour list).
The recorder stores those values but never reads them back, and its
"interface upgrade" controls (`.ehaon`, `.ehaoff`, `.tahookoff`,
`.ehareport`) gate the idle-construction-unit finder and the hundred-unit
queue and report an external companion program's presence. The colour values
are therefore consumed outside this DLL (**Supported inference**), by that
companion or another component.

## Current-source script integration

**Established — scope and registration.** This section records source behavior
at TADR commit `dcff5ddeb6bd1030e3f452c0f16e5f005850f62f`, inspected on
2026-09-22. It does not extend the earlier binary observations to callbacks or
ports that those observations did not cover. The
[`Plugins` registration path](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/Plugins.pas)
registers the extended port handler for every mod identity, while registering
extended script callbacks, map scripting, extra GAF sequences and the
unit-action/GUI-variant consumers only when configured mod id is greater than
1. Each plugin also requires the supported host-version check. Presence of a
port handler is therefore insufficient to establish that all its backing
consumers were installed. Registration's two installation phases are timing
choices, not disabled-plugin flags.

**Established — complete interface owner.** The current recorder handles the
grouped port interface from 21 through 400 described in
[Extended script ports](script-ports.md#current-source-dispatch-contract).
It includes player economy/visibility, health and cloak, creation and killing,
search result arrays, order inspection/issuance, definition copies/edits,
speech/sound/animation, other-unit script calls, shared data, map queries and
map-mission commands. Many are getter-shaped commands. Selected mutating
getters are suppressed during playback; others, including healing, giving,
shared-data writes, template edits and mission commands, are not. Setter
permissions divide into unrestricted, local human/AI, and local human/AI plus
not-playback groups. The eight ports found in the package census are a subset
of this source implementation, not a limit on recorder capability.

### Additional callbacks and call arguments

**Established — callback calls.** The
[`ScriptCallsExtend` plugin](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/ScriptCallsExtend.pas)
uses existing COB invocation machinery; it does not add a script opcode. Its
active call sites provide these arguments and scheduling options:

| Callback | Arguments supplied by the source | Start mode |
|---|---|---|
| `AimPrimary`, `AimSecondary`, `AimTertiary` | Extend the aim call to three arguments: existing heading/pitch plus target unit id; ground targeting passes zero id. The ballistic path supplies zero for both angle arguments and the same unit-id-or-zero third argument. | Existing aim invocation |
| `WeaponHit` | Weapon id, source hit-test result 0/1, projectile current X and Z in raw fixed point. Requires attacker and attacker script state. The wrapper does not reduce X/Z to integer map coordinates. | Immediate |
| `TookDamage` | Damage type, damage amount, attacker id; requires the target's alive state and no death latch at this hook. | Deferred |
| `SetNewMaxReloadTime` | Weapon selector `130`–`132` and new reload-time word. | Deferred |
| `ConfirmedKill` | Death-type argument sent to the unit entering the death-finalization path, not to its credited killer. The call is inserted before the original death handling. | Immediate |
| `ConfirmVTOLTransport` | Loading flag, piece, transported unit id. Loading uses 1 and the load-order piece; unloading uses 0 and piece 0. Both unit references must be present. | Immediate |

The generic
[`TAUnit.CobStartScript` helper](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/TAMem/TA_MemUnits.pas)
counts supplied argument values, fills omitted ones with zero, and does
nothing when the unit or its script state is absent. Matching the source
binding to the retail adapters establishes that the wrapper's “guaranteed”
option selects immediate execution: after allocation, all active slots run
with zero delta and one zero-delta piece pass follows. False selects deferred
execution; neither guarantees a free slot ([04 §4.2](../retail-executable-spec/04-units-orders-scripts-and-movement.md)).
The death callback recipient matches the retail finalization entry
([04 §5.1](../retail-executable-spec/04-units-orders-scripts-and-movement.md));
the damage guard matches its alive/death-latch predicates. The
commented-out expansion of `HitByWeapon` is not registered; no extra damage
argument to that existing callback is established by this source.

**Unknown — full callback trigger semantics.** The source settles callback
names, payloads and invocation options, but the patched engine call sites
still need matching to retail behavioral evidence to establish the full
`WeaponHit` test and the damage helper's preceding admission/scaling before
`TookDamage`. Their payloads are established, but complete event-delivery
contracts still require that caller match; no binary-patch
analysis is authorized by this reference.

### Script capacity and map-script scheduling

**Established — optional 64 slots.** The
[`MaxScriptSlots` plugin](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/MaxScriptSlots.pas)
raises unit script capacity from eight to 64 slots only when mod id is greater
than 1 and `[Preferences] IncScriptSlotsLimit` is true. The
[`INI reader`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/IniOptions.pas)
defaults the setting to false. The replacements cover allocation,
initialization, start admission, dispatcher loops and the unit-loop script
runner. The
[`SaveGame` companion](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/SaveGame.pas)
uses the same two gates for the matching save/load changes. This establishes
a capacity change; it is not evidence for a different COB instruction budget,
`SLEEP` unit or ordering policy.

**Established — map COB entry.** The mod-only
[`MapExtensions` plugin](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/Recorder/plugins/MapExtensions.pas)
derives a `.cob` path from the loaded map's terrain pathname. When the file
exists, it creates a separate script host using a copy of catalog unit type 1
with that COB file substituted and the local player as owner. For non-menu
game types it starts `MapMission` with two arguments: game type
(1 campaign, 2 skirmish, 3 multiplayer) and difficulty (0 easy, 1 medium,
2 hard), using immediate execution. The host later reports id 65535. The
start precedes that id assignment, so the initial synchronous drain cannot
rely on the final id having been assigned. A continuing update hook invokes
the engine script runner for this host when script state exists; it is not merely a one-shot startup call. The
precise placement of that hook within an authoritative tick still needs a
retail phase match.

**Established — map command tables.** If a companion `.tdf` file exists, the
loader reads `sounds`, `features`, `unitsmissions`, and `textmessages`
sections. Sound/feature/initial-mission lists contain section **keys** in the
reader's returned order. The text list instead contains values looked up by
those keys. Script ports use zero-based list indices; most callers do not
check an index or even the table's presence. Map initialization clears mouse
lock and fade. Extension cleanup releases those lists, removes the map-script
host and clears shared data/search arrays. See
[map and mission ports](script-ports.md#map-mission-and-arithmetic-ports) for
individual commands and their remaining delegate questions.

**Unknown — authored map-table contract and persistence.** The source tells
which file/section strings are requested, but not which of the inspected
packages actually ships compatible map scripts or what ordering an author
expects from the INI-style section reader. A versioned authored-file census
would settle that boundary. Save/load restores the explicitly submitted
shared-data prefix and result-array families; map-host scheduling and all
private-definition/forced-height/visual state must not be assumed persistent
without tracing their save/load consumers. A compatible Nanolathe save design
remains a separate decision.

## Unknown

- **Unknown — the nanolathe particle overlay.** The colour-slot names include a
  construction-particle base colour and colour list, but no shipped recorder
  code draws anything with them; whether the companion interface-upgrade
  program implements that overlay is not established. A bounded observation,
  or finding the reader of the `[Colors]` section in that program, would
  settle it.
- **Unknown — the remaining per-build code conditionals.** One ProTA/Escalation
  difference writes a per-player flag whose meaning is untraced; two
  cheat-report comparisons differ in strictness. Their user-visible effect is
  not established.
- **Unknown — the per-build identity fragment.** The identity tags differ per
  build and are used in version/log formatting in the same area as mod
  reporting; whether they name the mod or a file suffix is not established.
