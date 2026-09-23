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

## Music

Nanolathe already reads MP3 and PCM WAV tracks from a mounted `music` folder,
including the original installation's optional soundtrack. This matches the
directory named by ProTA 4.8's bundled `wgmus.ini`; Nanolathe does not import
that INI or execute WGMUS. Playback uses Nanolathe's portable backend and its
[documented track ordering](DESIGN_PRESENTATION_CLIENT.md#5-divergences).
The ProTA package itself contains no soundtrack. Options → Music controls
playback and volume after entering a battle.

The historical bundle's choice between its two music DLLs, fallback behavior
and exact track ordering remain unresolved in
[the extension reference](../research/extensions/prota-engine.md#unknown).

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

The regular Core east- and west-facing shipyards (`CORSYE`, `CORSYW`) currently
have no build products. The 4.8 archive names their build lists `CORSYNE` and
`CORSYNW`. An upstream content correction or evidence of the historical alias
is needed; Nanolathe does not guess that relationship. The other directional
yard definitions retain their matching authored membership.

The 4.8 documentation names different computer-player income/reclaim factors,
broader low-energy shutdown and a higher commander construction threshold.
Their exact arithmetic and runtime boundaries are not established by the
audited readable source. Nanolathe retains its retail behavior at these sites;
the Community profile name does not enable those documented changes.

The historical stockpile/nuke implementation, complete hotkey assignment table
and music backend also remain unverified. Their evidence requirements are
recorded in [ProTA's unknowns](../research/extensions/prota-engine.md#unknown).
