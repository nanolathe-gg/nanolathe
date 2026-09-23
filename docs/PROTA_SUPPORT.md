# Running ProTA

Use the ProTA 4.8 content package over an original Total Annihilation install:

```sh
go build -o nanolathe ./cmd/nanolathe
./nanolathe --root "/path/to/Total Annihilation" \
  --root "/path/to/ProTA4.8" --content-profile prota \
  --gameplay community-3.9 --save-dir "/path/to/prota-saves"
```

The second root supplies `ProTA.gp3` and its companion artwork. Keep the base
game and expansion archives in the first root: the package overlays their
maps and campaigns. No patched executable or Windows DLL is loaded by
Nanolathe.

The content profile selects ProTA's renamed directories and content limits.
Community 3.9 selects the supported Community rules; Modern adds Nanolathe's
documented Modern policies. Strict 3.1 ignores the Community feature table.
The current Community `prota` table follows the pinned source described in
[the extension reference](../research/extensions/prota-engine.md#current-source-profile-is-a-separate-target).
It does not establish exact equivalence with every change in the historical
2025 engine bundle.

## Strategic icons

Set `presentation.strategicIconConfig` in Nanolathe's settings to the ProTA
package directory, its `Icon` directory, or the exact `Icon/iconcfg.ini` path.
For example, merge this field into the existing settings rather than replacing
other preferences:

```json
{
  "presentation": {
    "strategicIconConfig": "/path/to/ProTA4.8"
  }
}
```

Directory selection requires exactly one configuration at the directory root,
`Icon/iconcfg.ini`, or `ZIcon/iconcfg.ini` (case-insensitive names). An explicit
INI path takes precedence over directory discovery. Missing or ambiguous
configurations report a diagnostic and keep generated icons available.

## Selection controls

Options → Orders exposes **Idle keys** and **2-click**. Both are off by default
and are independent of the gameplay and renderer selections. Enable Idle keys
for these current Community-source controls:

| Input | Nanolathe action |
|---|---|
| Ctrl+B | Select the next idle mobile builder and centre the view. |
| Ctrl+F | Select the next idle factory and centre the view. |
| Ctrl+S | Select on-screen units in the authored `CTRL_W` category that cannot fly. |
| Ctrl+Shift+B/F | Keep the ordinary additive authored-category selection. |

Enable 2-click for on-screen same-type selection with either mouse button.
Build shortcuts still come from the unit's authored GUI page and appear on its
buttons. These controls implement the pinned current-source contracts. The
[historical 4.8 selection audit](../research/extensions/prota-engine.md#shipped-selection-and-hotkey-audit)
also establishes those key assignments, but records older idle-cycle and
prepared-order behavior that differs from the current source.

## Music

Nanolathe already reads MP3 and PCM WAV tracks from a mounted `music` folder,
including the original installation's optional soundtrack. This matches the
directory named by ProTA 4.8's bundled `wgmus.ini`; Nanolathe does not import
that INI or execute WGMUS. Playback uses Nanolathe's portable backend and its
[documented track ordering](DESIGN_PRESENTATION_CLIENT.md#5-divergences).
The ProTA package itself contains no soundtrack. Options → Music controls
playback and volume after entering a battle. Choose **Random** there to match
ProTA 4.8's documented `CDMode=2` preference (`audio.cdMode: 2` in Nanolathe's
settings). Nanolathe does not automatically import `ProTA.ini` preferences.

The shipped executable uses its bundled `WIN32.dll` music proxy. That proxy
uses Windows file enumeration order without a separate sort and does not
fall back to CD or another backend when its music folder is empty. These
version-specific findings are recorded in
[the extension reference](../research/extensions/prota-engine.md#versioned-music-documentation).

## Verification

Set the mod root explicitly when running asset tests; the ordinary retail
gate otherwise skips optional mod coverage:

```sh
NANOLATHE_RETAIL_ASSETS="/path/to/Total Annihilation" \
NANOLATHE_MOD_ROOTS_PROTA="/path/to/ProTA4.8" tools/check-retail
```

The [package acceptance requirements](../research/extensions/prota-engine.md#package-acceptance-cases)
distinguish authored content, implemented current-source behavior and remaining
historical unknowns. Loading a catalog alone is not evidence of campaign,
rendering or AI parity.

The focused [session check](DESIGN_SESSIONS_AI_SAVE.md#27-prota-48-package-acceptance-boundary)
exercises commanded combat, construction across save/load, directional-yard
queue admission and original/Core Contingency mission entry. It checks the
campaign restrictions, map providers, wind overlay and successor lookup.
It does not complete campaign objectives. A separate bounded campaign GUI
probe submitted CORSOLAR through the build button and placement path into the
authoritative queue, while ARMAPEX remained absent from the restricted catalog
and active controls. That probe explicitly made the commander selectable:
Core mission 1 authors a 3,600-second wait before its MakeSelectable order, and
the normal opening was observed for only 3,000 ticks. A separate forced-victory
fixture verified the result screen and continuation to mission 2's briefing.
Neither fixture establishes a natural campaign playthrough.

The [presentation checks](DESIGN_INTERFACE_HUD_INPUT.md) cover both
faction menus, portraits, palette assets and custom icon loading; captures
also exercise Classic and Modern rendering.

An explicit skirmish fixture additionally verified live pink and slate unit
trim through both renderers using ProTA's own team-logo palette choices. The
ordinary direct-map capture path uses default player colors, so its output
alone does not verify a saved color selection.

Load saved battles with the same content roots, profile and gameplay selection.
The retail save bank relies on those host choices when reconstructing a session.

A bounded run with the identified 4.8 archive, Community 3.9, Comet Catcher,
medium difficulty and seed 7 reached the normal post-battle loss at tick
57660. The computer created 107 units and first issued an attack at tick
46800, then destroyed the idle human player's commander. This checks ordinary
AI construction, attack and result progression; it is not a comparison with
the historical patched engine or an assessment of AI strength.

## Remaining compatibility gaps

The regular Core east- and west-facing shipyards (`CORSYE`, `CORSYW`) have
working human build buttons: the package check clicks each yard's authored
constructor-ship button into its factory queue. Their CANBUILD lists remain
empty because the archive names those sections `CORSYNE` and `CORSYNW`.
Historical AI use of those lists remains unverified; the human GUI does not
require an alias.

The shipped 4.8 loader's computer-player production and feature-reclaim factors
are now established as Easy/Medium/Hard `0.5/1/4`, applied per contribution.
Unit reclaim and other refund paths retain retail factors. Its construction
task stops capture-capable builders placing buildings at ten build units, but
admits reposition/repair patrol at five, leaving an overlap from five to nine.
These are implementation gaps: Nanolathe retains its retail behavior at these
sites, and the Community profile name does not enable the historical changes.

The shipped low-energy task also selects building-class appliances with
authored energy use of at least 32 for ordinary finite values, and its stockpile
task submits one product only while both order paths are empty. These contracts
are established but not implemented as historical ProTA behavior; in particular,
the queue rule differs from the later source's completed-ammunition guard. See
[the AI audit](../research/extensions/prota-engine.md#ai-and-economy-evidence-audit)
for exact admission, arithmetic and ordering boundaries, and
[remaining unknowns](../research/extensions/prota-engine.md#unknown).
