# Total Annihilation File Formats

This directory is a standalone, self-contained reference for every file format
used by Total Annihilation (Cavedog Entertainment, 1997) and its expansions.
It exists so that Nanolathe development does not depend on external web pages,
which have a habit of disappearing. Everything here has been cross-checked
against real retail game data (the `totala*.hpi`, `*.ccx`, and `rev31.gp3`
archives). Nanolathe's parsers are conformance consumers of this reference,
not evidence for retail behavior.

Each of the fourteen documents follows the same template:

1. **Overview** — what the format is, what it stores, where it is used.
2. **Format at a glance** — a high-level diagram of the layout.
3. **Reference** — exhaustive byte-level documentation with worked examples
   taken from real retail data. Some documents split this across
   named sections (`Syntax` and `Schema families` in [tdf.md](tdf.md), the
   container/VM/instruction-set split in [cob.md](cob.md)), and a few add a
   "How the engine loads it" section for loader-visible edges.
4. **Unknowns and caveats** — anything we are not sure about, called out
   explicitly rather than guessed, each stated as the unknown plus what would
   settle it.
5. **Sources** — where the information originally came from.

**What this directory owns.** Byte layout in *files*: offsets, field sizes,
types, authored defaults and the conversions applied when a value is read.
That is authored data, so file offsets belong here. What the engine *does*
with a value it has read is owned by `research/retail-executable-spec`; where
a document here touches behavior it cites the owning section rather than
restating the arithmetic. Citations into this directory are by document —
`[fmt tnt]` resolves to `tnt.md` — and nothing inside a document is anchored,
so headings are free to change.

**One voice.** Each document states one thing. A finding that replaces an
earlier one replaces the text it corrects instead of arguing with it; git
history is the audit trail. Claims carry a confidence level —
**Established**, **Supported inference**, or **Unknown** — and warnings about
readings the community documents differently are written as warnings about
those readings, not as this directory's own history.

## The formats

| Document | Extensions | Kind | Role |
| --- | --- | --- | --- |
| [hpi.md](hpi.md) | `.hpi` `.ufo` `.ccx` `.gp3` | binary archive | Compressed, lightly encrypted archive container holding all game data |
| [3do.md](3do.md) | `.3do` | binary | 3D models for units, corpses, features, and projectiles |
| [cob.md](cob.md) | `.cob` (`.bos` source) | binary bytecode | Compiled unit animation scripts run by the game's script VM |
| [gaf.md](gaf.md) | `.gaf` | binary | Indexed-color image/animation containers (textures, UI art, explosions, cursors, build pics) |
| [gui.md](gui.md) | `.gui` | text (TDF syntax) | Menu / screen layout definitions (gadgets) |
| [pal.md](pal.md) | `.pal` `.alp` `.lht` `.shd` | binary | The 256-color game palette and derived lookup tables |
| [tdf.md](tdf.md) | `.tdf` | text | The general text-definition syntax and the gamedata/feature/weapon/download schemas |
| [fbi.md](fbi.md) | `.fbi` | text (TDF syntax) | Unit definitions — one file per unit |
| [ota.md](ota.md) | `.ota` | text (TDF syntax) | Map/mission metadata paired with a TNT, including the mission scripting mini-language |
| [tnt.md](tnt.md) | `.tnt` `.sct` | binary | Runtime map terrain (tile graphics, tile placement, height/feature grid, minimap) and the editor's section source (small tile grid, preview, opaque metadata) |
| [fnt.md](fnt.md) | `.fnt` | binary | 1-bit bitmap fonts used by the GUI |
| [pcx.md](pcx.md) | `.pcx` | binary (standard) | Unit info pictures and full-screen images (standard ZSoft PCX) |
| [wav.md](wav.md) | `.wav` | binary (RIFF plus legacy containers) | Sound effects and voice |
| [tad.md](tad.md) | `.tad` | binary | Community TA Demo Recorder match recordings (network-traffic logs; not a Cavedog format) |

## Shared conventions

Unless a document says otherwise, all of the following hold everywhere:

- **Endianness.** Every multi-byte integer in every binary format is
  **little-endian**. No TA format mixes byte orders.
- **Integer types.** Documents use `u8`/`u16`/`u32` for unsigned and
  `i8`/`i16`/`i32` for signed integers of the given bit width. The original
  1990s notes call 32-bit fields `long` and 16-bit fields `short`.
- **Offsets are absolute.** Binary formats here are pointer-heavy: structures
  reference each other with `u32` file offsets measured from the start of the
  file (for HPI, from the start of the archive). Sections therefore do not
  need to be contiguous or in any particular order, and real files often
  interleave them.
- **Strings** are NUL-terminated ASCII unless stated otherwise. Name lookup
  in the engine is case-insensitive; files freely mix cases
  (`ARMFLAK.FBI` vs `armflash_dead.tdf`).
- **Fixed point.** 3D model coordinates and most script motion scalars are
  signed 16.16 fixed point: the value in "world units" is the stored integer
  divided by 65536.
- **Angles.** The game represents a full circle as 65536 angular units
  (BOS source writes `<degrees>`, compiled as `degrees * 65536 / 360`,
  i.e. ~182.04 per degree).
- **Coordinate system.** X is east (increases rightward on the map), Y is up,
  Z is south (increases downward on the map). Models are authored Y-up with
  +Z as the unit's forward direction. One map "pixel" (the unit of OTA
  positions and weapon ranges) corresponds to one texel of the map tile
  graphics; the height/attribute grid is 16 pixels per cell and the tile grid
  is 32 pixels per cell.
- **Indexed color.** All game art is 8-bit indexed into the single shared
  256-entry palette ([pal.md](pal.md)). No format stores RGB pixels.

## Where files live

Inside the archives ([hpi.md](hpi.md)) content is organized into a fixed
directory layout. The engine merges all mounted archives into one virtual
file system; loose directories next to the executable override archive
content, and among archives the extension decides precedence
(GP3 > CCX > UFO > HPI).

```
anims/          GAF: UI art, build-menu "gadget" pics, cursors, explosions
bitmaps/        PCX: backgrounds, logos
camps/          Campaign/mission data (briefings in camps/briefs, useonly lists)
download/       TDF: build-menu placement for add-on units
features/       TDF: map features and unit corpses (subdirs per world type)
fonts/          FNT: bitmap fonts
gamedata/       TDF: global tables (sidedata, moveinfo, sound, allsound, ...)
guis/           GUI: menu layouts
maps/           OTA + TNT map pairs
objects3d/      3DO models
palettes/       PAL/ALP/LHT/SHD palette data
scripts/        COB compiled unit scripts (sometimes BOS source too)
sounds/         WAV sound effects
textures/       GAF model textures
unitpics/       PCX unit portraits
units/          FBI unit definitions
weapons/        TDF weapon definitions
```

## Provenance

The byte-level information here originates from community reverse-engineering
documents written 1998–2003 (the "TA Design Guide" at
`units.tauniverse.com/tutorials/tadesign/` and the format notes it links),
verified and extended by direct inspection of retail data and, where stated,
bounded static analysis of the retail executable. Every document lists its
sources as original web URLs; those sites may disappear (the Wayback Machine at `web.archive.org`
holds captures), which is exactly why these documents are written to stand
alone — nothing in them requires the originals. Hex dumps labelled with an
archive path are real bytes from the retail game files.

One further source appears in [fbi.md](fbi.md), [tdf.md](tdf.md) and
[ota.md](ota.md): a whole-string census of the retail executable's data
segment. TDF-family lookup is by key pointer, so a key that has no literal
string anywhere in the image cannot be read by the original engine, however
often the shipped content authors it or however confidently the community
documented it. Several long-standing keys turn out to be inert that way —
`TEDClass`, `SteeringMode`, `NoAutoFire`, unprefixed `BadTargetCategory`,
`hitdensity`, `aimrate`, `SolarStrength`, `MohoMetal` — and a couple of keys
turn out to exist that no shipped file uses, such as the unit key
`armoredstate`. This is evidence about the data segment only. It settles which
keys can matter; it never settles what the engine does with the ones that can.
