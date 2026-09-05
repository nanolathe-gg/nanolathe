// Package settings persists the frontend preferences that survive a restart:
// the last skirmish setup, the last map, per-slot side/colour/ally/resources,
// and the campaign difficulty.
//
// Retail keeps exactly this set in the registry under
// HKEY_CURRENT_USER\Software\Cavedog Entertainment\Total Annihilation, with the
// per-slot Player%dController/Side/Color/AllyGroup/Metal/Energy values in the
// nested "Total Annihilation\Skirmish" key. The startup reader loads the whole
// block once and installs a default for every value it does not find; the
// writer rewrites the whole block [02 "Settings"][07 §10].
//
// One value here is not a registry value: the per-player unit limit lives in
// the profile file's `[Preferences]` section instead [02 "Unit limit"]. It is
// persisted all the same, and the two stores collapse into the one JSON file
// below.
//
// Nanolathe keeps the value set, the defaults, and the read-once/write-whole
// shape, and swaps the registry for one JSON file. Only the preferences retail
// actually persists are stored here — audio mixing and the networking identity
// fields are retail values Nanolathe has no owner for yet and are deliberately
// absent rather than written as invented defaults.
//
// The display block (`DisplaymodeWidth`/`DisplaymodeHeight`) and the visual
// option values (`Anti-Alias`, `Shadows`, `FeatureShadows`, `VehicleShadows`,
// `Shading`, `Gamma`) are here because the options screen's `VISUALS` page is
// their only writer [07 R-FE-01 §6][07 R-FE-01 §11]; their missing-value
// defaults are the registry loader's [02 R-KEYS-01 §5].
package settings
