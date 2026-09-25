# Running ProTA

Nanolathe runs the ProTA 4.8 content package over an original Total
Annihilation install. No patched executable or Windows DLL is loaded: the
package's engine changes that the
[extension reference](../research/extensions/prota-engine.md) establishes are
reimplemented as switches, listed below.

## Installing

Drop the ProTA 4.8 zip or folder onto the Nanolathe window at the main menu,
then choose it on the **Mods & Mutators** screen. A package without Nanolathe
metadata installs as a *Local* mod with the `prota` content profile detected
([DESIGN_MODS_MUTATORS §4.5](DESIGN_MODS_MUTATORS.md#45-manual-installs)).
The desktop command's `--install-mod <path>` does the same from a shell, and
`--mod <id>` selects an installed mod for one run.

A manual stack of roots still works, and takes precedence over the saved mod
choice:

```sh
go build -o nanolathe ./cmd/nanolathe
./nanolathe --root "/path/to/Total Annihilation" \
  --root "/path/to/ProTA4.8" --content-profile prota \
  --gameplay community-3.9 --save-dir "/path/to/prota-saves"
```

Keep the base game and expansion archives in the first root: the package
overlays their maps and campaigns. The content profile selects ProTA's renamed
directories and content limits.

Load saved battles with the same mod, or the same roots, profile and gameplay
selection. A save made with a library mod records it and switches to it on
load ([DESIGN_MODS_MUTATORS §7](DESIGN_MODS_MUTATORS.md#7-the-save-sidecar)).

## Gameplay

ProTA requires **Community 3.9** or **Modern**: while it is selected the
gameplay option skips Strict 3.1, and a command line naming `--mod` and
Strict 3.1 together is rejected. Both select the Community `prota` feature table, which
follows the pinned current source
([extension reference](../research/extensions/prota-engine.md#current-source-profile-is-a-separate-target));
Modern adds Nanolathe's documented Modern policies on top. The table sets the
unit limit to 1500, which the loading screen shows as *Unit limit 1500 (set by …)*,
naming the source.

The `prota` content profile also turns on nine switches for the historical 4.8
package's engine changes
([DESIGN_COMMUNITY_PATCH §4.7](DESIGN_COMMUNITY_PATCH.md#47-prota-48-package-behaviours)).
They are off in every shipped table, so no other content sees them:

| Switch | Effect |
|---|---|
| `aiDifficultyIncome` | the computer player's production and feature-reclaim income is ×0.5 / ×1 / ×4 on Easy / Medium / Hard |
| `aiStockpileProducts` | the computer player queues nuke and anti-nuke rounds from ProTA's stockpile producers |
| `targetLockRelease` | a weapon drops an autonomous unit target it can no longer hit, and looks for another |
| `aiApplianceEnergy` | the computer player's low-energy task picks appliances by energy use |
| `aiBuilderStopThreshold` | a computer builder stops placing buildings at ten builders, not five |
| `workingWeaponsAutonomous` | a unit that is assisting, repairing, reclaiming or capturing keeps firing at targets of its own |
| `attackSingleSlotTake` | an attack order takes one weapon, so the unit's other weapons keep picking targets |
| `mapFeatureOwnerEleven` | walls and other tall map features stay drawn under fog once explored |
| `resurrectionTextFix` | the failed-resurrection message is spelled *Resurrection failed* |

The switches can be overridden one at a time through the settings file's
`gameplayFeatures` or `--gameplay-feature name=value`.

## Recommended settings

The first time ProTA is selected — from the Mods & Mutators screen, by
`--mod`, by loading a save, or as a manual `--content-profile prota` stack —
Nanolathe offers ProTA's recommended settings once. The window lists each
setting with its new value and your current one; **Apply** writes them and
**Keep mine** leaves yours. Either answer is remembered. Every row is an
ordinary option you can change later
([the full table](DESIGN_MODS_MUTATORS.md#43-selection-and-precedence)):

- **Controls:** idle-unit keys, double-click selection, queued-order drag, and
  digits recalling groups (`SwitchAlt`).
- **HUD:** Community counters, reload bars, veterancy labels, group digits,
  the wind and tide readout, the game clock and the victory cue.
- **Megamap:** the megamap overview with ProTA's wheel, flash, ring and
  dot-colour preferences.
- **Audio:** 3D sound, 128 voices, and random music.
- **Skirmish:** all ten player rows shown.

## Controls

With **Idle keys** on (Options → Orders):

| Input | Nanolathe action |
|---|---|
| Ctrl+B | Select the next idle mobile builder and centre the view. |
| Ctrl+F | Select the next idle factory and centre the view. |
| Ctrl+S | Select on-screen units in the authored `CTRL_W` category that cannot fly. |
| Ctrl+Shift+B/F | Keep the ordinary additive authored-category selection. |

**2-click** selects every on-screen unit of the clicked type, with either
mouse button. **Digits: Groups** (Options → Orders, or the `+switchalt` chat
command) makes 1–9 recall unit groups and Alt+digit select a build page;
**Pages**, the retail default, is the other way round. Build shortcuts come from the
unit's authored GUI page and appear on its buttons.

The [historical 4.8 selection audit](../research/extensions/prota-engine.md#shipped-selection-and-hotkey-audit)
records the same key assignments but older idle-cycle and prepared-order
behaviour; Nanolathe implements the current source.

## Megamap

With the overview set to **Megamap** (the recommended setting), Tab shows a
full-screen map of the battle instead of opening the options, and F2 still
opens them. Wheel back also shows it, and wheel forward returns to the battle
centred on the pointer. On the megamap you can box-select, give orders, and
place buildings; icons come from ProTA's `Icon/iconcfg.ini` in the player's dot
colours, with radar, jammer and anti-nuke rings for selected units and a flash
for units under attack. The simulation keeps running while it is shown.
[DESIGN_INTERFACE_HUD_INPUT §3.15](DESIGN_INTERFACE_HUD_INPUT.md#315-optional-megamap)
has the full input table. With the overview set to **Zoom**, Tab keeps
Nanolathe's ordinary zoom-out overview.

## Strategic icons

ProTA's own `Icon/iconcfg.ini` is found automatically in the mounted mod, or
in a manual stack's roots, last root first. To use a different configuration,
set `presentation.strategicIconConfig` in Nanolathe's settings to a package
directory, its `Icon` directory, or an exact `iconcfg.ini` path; merge the
field into the existing settings rather than replacing them:

```json
{
  "presentation": {
    "strategicIconConfig": "/path/to/ProTA4.8"
  }
}
```

A directory must hold exactly one configuration at its root, `Icon/iconcfg.ini`
or `ZIcon/iconcfg.ini` (case-insensitive names). A missing or ambiguous
configuration reports a diagnostic and keeps Nanolathe's generated icons
([DESIGN_GPU_RENDERER §18.7](DESIGN_GPU_RENDERER.md#187-optional-community-icon-configuration)).

## Victory sound

With **Victory cue** on (Options → HUD, or the recommended settings), a win
plays the `Victory Condition` sound when the result appears, in skirmish,
Survival and campaign alike, as ProTA 4.8 does
([DESIGN_INTERFACE_HUD_INPUT §3.16](DESIGN_INTERFACE_HUD_INPUT.md#316-optional-victory-cue)).

## Music

Nanolathe reads MP3 and PCM WAV tracks from a mounted `music` folder,
including the original installation's optional soundtrack. This is the
directory ProTA 4.8's bundled `wgmus.ini` names; Nanolathe does not import
that INI or run WGMUS. Playback uses Nanolathe's portable backend and its
[documented track ordering](DESIGN_PRESENTATION_CLIENT.md#5-divergences). The
ProTA package contains no soundtrack. Options → Music controls playback and
volume; **Random** matches ProTA 4.8's `CDMode=2`, and the recommended
settings choose it.

The shipped executable uses its bundled `WIN32.dll` music proxy, which plays
in Windows file enumeration order and does not fall back to CD when its folder
is empty; see
[the extension reference](../research/extensions/prota-engine.md#versioned-music-documentation).

## Verification

Set the mod root explicitly when running asset tests; the retail gate
otherwise skips optional mod coverage:

```sh
NANOLATHE_RETAIL_ASSETS="/path/to/Total Annihilation" \
NANOLATHE_MOD_ROOTS_PROTA="/path/to/ProTA4.8" tools/check-retail
```

The [package acceptance requirements](../research/extensions/prota-engine.md#package-acceptance-cases)
distinguish authored content, implemented behaviour and remaining historical
unknowns. Loading a catalog alone is not evidence of campaign, rendering or AI
parity.

The focused [session check](DESIGN_SESSIONS_AI_SAVE.md#27-prota-48-package-acceptance-boundary)
exercises commanded combat, construction across save/load, directional-yard
queue admission and original/Core Contingency mission entry, and checks the
campaign restrictions, map providers, wind overlay and successor lookup. It
does not complete campaign objectives. A bounded campaign GUI probe submitted
CORSOLAR through the build button and placement path, while ARMAPEX stayed
absent from the restricted catalog; a forced-victory fixture verified the
result screen and continuation to mission 2's briefing. Neither establishes a
natural campaign playthrough.

Further ProTA-gated checks cover each package switch on and off, the
recommended-settings offer, the megamap with ProTA's icons, and the Core nuke
silo (CORSILO) and amphibious kbot (CORAMPH). Both declare one trailing script
piece their model lacks; retail's piece linking tolerates that
([04 R-COB-01 §4]), and Nanolathe links the same way, so both can be built.

The [presentation checks](DESIGN_INTERFACE_HUD_INPUT.md) cover both faction
menus, portraits, palette assets and custom icon loading in Classic and Modern
rendering. A skirmish fixture verified ProTA's own team-logo colours on unit
trim through both renderers.

With the AI switches on, a bounded Community 3.9 skirmish showed the Hard
computer player's income at four times Medium's, and a computer player that
completed a CORFMD queued and held an anti-nuke round. This checks the
switches take effect; it is not a comparison with the historical engine or an
assessment of AI strength.

## Remaining gaps

- **Weapons while building a new structure.** Retail stops a mobile builder's
  weapons picking targets while it places a new building, and ProTA 4.8
  reverses that. Nanolathe never stops them, under any rule set, so ProTA
  already behaves as intended here but Strict 3.1 differs from retail.
- **Megamap details** that the research does not record are Nanolathe's
  choices: the terrain picture, what a neutral order does, and build
  placement from the megamap. The `Megamap*Color` overrides, the order and
  selection overlay and the whiteboard marker strip are not implemented, and
  the allied resource bars do not use the dot colours.
- **Victory cue:** the engine checks something before it draws either end
  title, and the research does not describe it; Nanolathe plays the cue on
  every shown win.
- **Directional shipyards and mobile anti-nukes.** The Core east- and
  west-facing shipyards (`CORSYE`, `CORSYW`) build from their buttons, but the
  computer player does not use them, and the mobile anti-nukes (`ARMSCAB`,
  `CORMABM`) are not stocked by it. ProTA 4.8 behaves the same way; neither is
  a Nanolathe gap.
