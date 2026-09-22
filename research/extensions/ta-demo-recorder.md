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
- **Script-port extensions ("COB Extensions")**: the recorder hooks the
  executable's script-port reader and answers the extended ports `32` and
  `69`–`75` for unit scripts (unit-id iteration range, own id, target owner,
  target build progress, relation and visibility tests, and the reader's kill
  count); retail ports pass through. The hook is installed after a
  byte-signature check of the host executable and has no off switch. All
  inspected builds (3.9.2.0 and 3.9.2.416) implement the same port set with
  the same semantics; see [Extended script ports](script-ports.md).
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
