# Running TA Zero

Nanolathe supports TA Zero Alpha 5's three-faction skirmish content with TA
Zero Base and Map Pack 1f. The target package is the 24 December 2024 Alpha 5
release over the 13 December 2019 Base. Historical engine parity remains
incomplete; the boundaries below distinguish content support from legacy
engine behavior.

## Install and select

Obtain Base, Alpha 5 and Map Pack 1f from the
[TA Zero downloads page](https://zero.tauniverse.com/downloads/). Keep the
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
settings documented in Alpha 5's `TAZero.ini`: double-click selection, group
digits, megamap wheel navigation and flashing, separate sensor thresholds,
Zero's player-dot palette, 3D sound, 128 sound voices, random music and ten
skirmish rows. **Keep mine** preserves the current settings. Other host options
stay as chosen; this is not a complete emulation of Zero's historical hotkey
handler.

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

Remaining compatibility questions are retained in the
[TA Zero engine reference](../research/extensions/ta-zero-engine.md#unknown):
historical recorder boundary cases, the legacy AI profile threshold and
build-point changes, and complete per-unit shield/VSOC behavior need matching
licensed source or bounded manual evidence. No generic shield mechanic or
unsettled historical arithmetic is substituted. The authored `SoundLava` key
has no established reader. Two model texture names remain absent from the
inspected distribution (`armcolormeta4_1` and `goksphere2_5`); the owning
[rendering audit](../research/extensions/mod-engine-compatibility.md#authored-rendering-audit)
distinguishes visible surfaces from script-hidden emitter pieces.
