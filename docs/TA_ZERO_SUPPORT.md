# Running TA Zero

Nanolathe supports TA Zero Alpha 5's three-faction skirmish content with TA
Zero Base and Map Pack 1f. The target package is the 24 December 2024 Alpha 5
release over the Base download listed as 14 December 2019. The official
[download listing](https://zero.tauniverse.com/ta-zero/) still names Alpha 5
and Map Pack 1f as current (checked 26 September 2026 UTC).

**Support is experimental; full gameplay parity remains incomplete.** The source audit
confirmed a gameplay mismatch in passive self-repair and several missing host
features. The verified contracts and remaining work below distinguish usable
content from the behavior of Zero's historical engine.

## Install and select

Obtain Base, Alpha 5 and Map Pack 1f from the
[TA Zero downloads page](https://zero.tauniverse.com/ta-zero/). Keep the
original Total Annihilation installation separate. Expansion archives supply
additional maps when installed.

For a library installation, combine the packages in one folder in this order:

1. Start with the `TA Zero` folder inside Base.
2. Extract Alpha 5 into that folder.
3. Extract Map Pack 1f into that folder.
4. Drop the combined folder onto Nanolathe's main menu, or run
   `nanolathe --install-mod "/path/to/TA Zero"`.
5. Select the installed package in **Mods & Mutators**.

The package is detected through its content and receives the `zero` profile.
A folder without Nanolathe metadata appears as a Local mod. Nanolathe ignores
Windows executables and DLLs; it uses its own engine. Base's
`TA_Features_2013.ccx`, `ZIcon` and `tamus`, Alpha 5's `TAZ31.gp3`, and the
map pack's `TA_Zero_Maps.ufo` provide the content for this composition.
Do not install Alpha 5 alone as a separate library mod: the package depends
on Base's shared features and resources.

The verified Base/Alpha 5/1f composition has the same winning content bytes
and compiled catalog when mounted as separate roots or combined into one
folder. This is specific to these releases; future package combinations must
repeat the winner/hash check
([DESIGN_MODS_MUTATORS §5.5](DESIGN_MODS_MUTATORS.md#55-the-hosted-zip-contract)).

Separate roots also work without installing into the library:

```sh
./nanolathe --root "/path/to/Total Annihilation" \
  --root "/path/to/TA Zero Base/TA Zero" \
  --root "/path/to/TA Zero Alpha 5" \
  --root "/path/to/TA Zero Map Pack 1f" \
  --content-profile zero --gameplay community-3.9 \
  --save-dir "/path/to/zero-saves"
```

Use a 1024×768 or larger window for the authored faction interfaces. The
existing fitted sidebar also makes all build buttons reachable at smaller
heights. GoK, Arm and Core are selectable in that authored order.

## Gameplay and saves

A library installation requires **Community 3.9** or **Modern**. The existing
Community `tazero` table supplies extension support, including the script
ports used by AI factories; Modern adds Nanolathe's documented policies.
That table follows the pinned current community source, whose behavior differs
from the historical renderer and recorder in Base. It is not a claim that
Nanolathe executes or exactly reproduces those Windows patches.

Strict 3.1 continues to disable Community features. A manually mounted stack
can select Strict for diagnosis, but it is not the supported Zero setup:
extended script ports then read the strict answers. Content loading never
silently switches a rule set. The per-player limit from the Zero table is
1500; the existing settings and gameplay-feature overrides remain available.

Nanolathe's own saves retain the selected library mod through their sidecar.
For manual roots, restore with the same roots, profile and gameplay mode.
Historical `.zsv` files and recorded multiplayer matches are not supported
interchange formats. Single-player skirmish is the tested Zero session;
retail campaign files remaining visible in an overlay do not establish a
Zero campaign conversion.

## Artwork, settings and music

The content profile selects Zero's main, single-player and loading backgrounds
and `LogoZ.gaf` team-colour bank. All three faction HUDs and builder pages
come from the mounted side and GUI definitions. Ten-frame team textures follow
the player's colour; they do not animate through other player colours.
Large effect banks, including `ModFX.gaf`, load timing and geometry first, then
decode the requested frames through bounded CPU and GPU caches.

On first selection, the optional **Recommended settings** offer applies only
settings documented in Alpha 5's `TAZero.ini` and the author's controls page:
double-click selection, group digits, megamap navigation and flashing, sensor
thresholds, player-dot palette, 3D sound, 128 sound voices, random music and
ten skirmish rows. It also offers the **Zero selection** scheme: Ctrl+B/F
cycle idle builders/factories, Ctrl+S selects armed units on screen, and
W/B/Y filter drag selection. The independent **100 batch** option
enables Ctrl+Shift to add/remove 100 factory products; Alt keeps its existing
batch of 20, and stockpile buttons keep their ordinary counts.
**Keep mine** preserves the current settings. Existing installations can use
**Options → Orders**, choose **Select: Zero** and **100 batch: On**, then
**OK**; an already accepted preset is not silently reapplied.
Selection predicates and modifier
precedence are the explicit host policies in
[the interface design](DESIGN_INTERFACE_HUD_INPUT.md#313-optional-community-selection-controls),
not a claim of complete historical hotkey equivalence.

The megamap's custom icons are found in Base's `ZIcon/iconcfg.ini`. The `tamus`
folder supplies music through the existing portable MP3 backend. Physical
files `2.mp3` through `17.mp3` are the sixteen playable tracks; the bonus intro
`1.mp3` is excluded. No Windows music DLL is loaded.

## Verification and remaining boundaries

Optional installed-content checks use an ordered, platform-separated root
list (`:` on macOS/Linux, `;` on Windows):

```sh
NANOLATHE_RETAIL_ASSETS="/path/to/Total Annihilation" \
NANOLATHE_MOD_ROOTS_ZERO="/path/to/Base/TA Zero:/path/to/Alpha 5:/path/to/Map Pack 1f" \
  tools/check-retail
```

The Zero acceptance checks exercise all three commanders and scripts,
commanded combat, construction across a Nanolathe save/restore, idle
factory direction commands, the AI factories' script-port reads, and authored
weather schemas. Frontend checks cover the three-way side selector, resource
selection and team colours. The effects audit decoded every frame in `ModFX`,
`ModFX2` and `ModFX3` while checking resident cache bounds; the optional package
test also locks admission and bounded residency for a large frame in each
`ModFX` entry. The earlier bounded audit also loaded and ticked
all fifteen map-pack maps and checked that Direct leaves a busy factory
unchanged. These checks do not establish full-match AI strength
or a visual comparison against the historical Windows engine.

The follow-up audit uses the released BOS sources, compiled COB programs,
unit/weapon definitions, all eight AI profiles and the map pack, plus the
pinned MIT TADR source and its history. The entire combined catalog compiles
without donor substitutions, and all 269 compiled unit scripts load. This is
parser coverage; the following runtime checks are deliberately narrower.

| Surface | Evidence and acceptance boundary |
|---|---|
| Core plasma shields | All 13 definitions: ordinary damage, impact callback, recharge and relevant activation/movement/factory states. Bounded closed/open perimeter projectile contact for both generators. |
| Adaptive armour | All four definitions: first versus adapted hit, expiry and Raider's stowed posture. |
| GoK void shields | Source census of all 69 shield-bearing definitions; runtime representatives of distinct formula families. Commander VSOC reserve, one root payment across its burst, infantry stun, expiry and save continuation; both extended-generator contact boundaries. |
| Anti-air handoff | All 16 relevant primary/tertiary scripts; preserves the three floating-turret exceptions actually shipped. |
| Maps and economy | Weather impact kinds, actual Crystal Gorge successor chain, invisible metal deposits and geothermal admission; all 15 maps previously loaded and ticked. |
| Aircraft and AI | Authored human/AI transport differences, bounded load/unload and construction, and Scramble's AirBattle profile. This is not a full-match AI-quality certification. |
| Controls | Optional Zero selection/filter scheme and factory hundred-unit batches; existing pages, icons, palette and sensors remain independently scoped. |

Known remaining gaps:

- **Passive self-repair is incorrect for Zero.** Its custom rates are not the
  retail eight-tick caller. For example, authored `HealTime=3` currently
  produces zero healing. Matching licensed patch source or bounded manual
  observations must settle the exact cadence, energy/stall and construction
  gates before a replacement is implemented. Enabling the unrelated current
  Community repair module does not resolve it.
- **Historical engine equivalence is unproven.** Current TADR's named Zero
  build postdates Alpha 5. The legacy AI threshold, build-point changes and
  older recorder boundary cases still lack matching source. Full per-unit
  projectile interactions and visual comparisons are not established by the
  callback tests.
- **Some documented controls remain absent:** X line/surround placement with
  wheel spacing, local whiteboard and factory-to-product control-group
  inheritance. Historical megamap key/radius differences remain separately
  unresolved. Developer shortcuts and screenshot formats are Nanolathe's.
- **The release has unresolved content references:** two model textures
  (`armcolormeta4_1`, `goksphere2_5`), the `GoKPlatform1` sound category,
  AI tokens `GOKT1GUNHSIP` and `GOKT2CONTANK_AI`, and documented Ctrl+J
  membership. No guessed replacements are supplied. `SoundLava` still has
  no established engine reader.

The [owning source reference](../research/extensions/ta-zero-engine.md#current-release-and-source-completeness-audit)
records exact evidence, script/documentation disagreements and what would
settle each unresolved contract. Networking, historical `.zsv`/replay
interchange and Windows DLL installation are outside this single-player target.
