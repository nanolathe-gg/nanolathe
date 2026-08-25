# Features: From Map Bytes to Shadows

> Implementation-oriented companion to `research/features/feature_rendering.md`.
> This file tells an engineer what to do: where data comes from, which assets
> are bound, where in the frame features draw, how reclaimability is decided,
> and how footprint blocking is consumed by navigation and footing.

## Summary

- A feature is a catalog entry stamped at a plot anchor with a
  rectangle-shaped footprint. All sources (TNT Table, OTA mission features,
  death corpses, burn/reclaim/damage successors, future reproduction) converge
  on one stamp/teardown API.
- Great Divide exercises all the interesting cases except 3DO corpses: many
  metal deposits, forests and grass/shrubs, a few hurt rocks, and geothermal
  vents with their shadows.

## 1  Where they come from

### 1.1  Catalog

`features/<group>/*.tdf` (177 files, 17 groups). Each top-level section names
a type; the compiler reads the vocabulary in
`research/retail-executable-spec/02` Feature record and stores a 0x100-byte
logical entry. Successor fields `featuredead / featurereclamate / featureburnt`
are linked in a later pass and sentinel `0xFFFF` means "no successor". Failure
to link emits `Record "%s" missing from feature files`.

For rendering the relevant keys are:

- Visual selector: `object` xor `filename`. One of them wins; `object` present
  means a 3DO model, `filename` present means a sprite sheet + sequence names.
  They do not coexist.
- Sequences: `seqname` + `seqnameshad` (idle shadow), `seqnameburn` +
  `seqnameburnshad`, `seqnamedie` + `seqnamedieshad`, `seqnamereclamate`
  + `seqnamereclamateshad`. A `burnweapon` and `sparktime` (`*30` ticks)
  drive the flame weapon.
- Geometry: `footprintx`/`footprintz`, `height`.
- Behaviour: `animating`, `animtrans`, `shadtrans`, `flamable`, `geothermal`,
  `blocking`, `reclaimable`, `autoreclaimable`, `indestructible`,
  `nodrawundergray`.
- Economy: `metal`, `energy`, `damage`.

### 1.2  Stamp / teardown — the only writer

`world.Terrain` holds the runtime plot: `W*H` cells, 13 bytes each (`0xD`
stride). Offsets that matter to placement:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The stamper (`FeatureService.PlaceAt` in `internal/features/service.go`) does:

1. Clip `anchorX+footX ≤ W` and same for Z, else fail.
2. Stall pass: for each covered cell try to tear down any overlapping occupant
   with honor-indestructible gating.
3. Assign the anchor feature word, poison fringe cells with `0xFFFE` and signed
   deltas, stamp the placer nibble (`map` → `10`, corpses → owner slot), and
   notify occupancy.

Teardown clears the anchor to `0xFFFF`, each fringe to `0xFFFF`/`0xFF` and
`flags&0xFE`, then notifies occupancy. No other writer touches these bytes.

### 1.3  Sources

| Source | What it does | Great Divide |
|---|---|---|
| TNT attribute map | Four-byte on-disk cell (`height`, `feature:u16`, `unk0`) → 13-byte plot. Table size `TileAnims*132` names. | 17-entry table, 2,552 real cells |
| OTA `Number of * Features` | Stamps `8/10/26`-byte records after mapping through a 0x80-per-name `Feature Type Names` vector. | none |
| Deaths | FBI `Corpse=` + `featuredead` chain depth | later during play |
| Burn / reclaim / damage successors | Atomic remove+re-stamp at same anchor via the stamper; sinking wrecks inherit velocity. | dynamics |
| Reproduction | One cell per sim tick via descending global cursor; stock inert (`reproduce=0` corpus-wide). | disabled stock |

### 1.4  Great Divide audit

- Map: TNT `0x2000` canonical, 160×256 cells = 2,560×4,096 pix, sea 45, `17`
  table entries. OTA is schema-only.
- Table contents dumped from the TNT:
  `Tree1..Tree6`, `Shrub1..3`, `Tree1Dead/Tree2Dead`, `RockMetal/3/1/2`,
  `Geothermal`, `Rock1a`. Definitions resolve to `features/green/*.tdf`.
- All initially-placed features are sprite-class (`filename=trees/rocks/geotherm`).
  `Tree*`/`Shrub*` 1×1, height 40/4; `RockMetal*` 3×3 height 4;
  `Geothermal` 1×1 animating vent; `Rock1a` 4×3. The corpses path will add
  `object=armdrag / cordrag` walls later.

## 2  Which assets get loaded and how

### 2.1  Dispatch at draw time

```text
if object present (filename absent):
    // 3DO path — early-stamped corpses/walls
    copy model pointer + world position into FeatureUnit
    dispatch through model rasterizer (flags always opaque)
else:
    // sprite path — everything on Great Divide at load
    resolve filename -> GAF set
    resolve seqname / seqnameshad -> handles
    if animating:
        advance cursors each sim tick, draw via cursor deref
    else:
        draw first frame of sequence (static)
    shadtrans=1 => translucent shadow blit, else opaque
    animtrans=1 => translucent normal, else opaque
```

Missing sequence → null handle → per-cell dispatch returns without drawing;
never crashes.

### 2.2  What the TDF resolves to on Great Divide

| Feature | Asset wires |
|---|---|
| `Tree1` | `filename=trees`, `seqname=leaf1`, `seqnameshad=leafshad`, `seqnameburn=leafyburn01`, shadow translucent (`shadtrans=1`), normal opaque, reclaim energy 250, `featureburnt=Tree1Dead`, residue `smudge01` |
| `Shrub*` | same `trees` GAF with `shrubs1..3`, no reclaim shadow, energy 20, `shruburn` |
| `RockMetal*` | `filename=rocks`, `seqname=rockmetal{1..4}`, `shadtrans=1`, indestructible, `metal 86..223`, non-reclaimable |
| `Geothermal` | `filename=geotherm`, `seqname=geotherm`, `animating=1`, geothermal flag, indestructible |
| `Rock1a` | `filename=rockshurt`, `seqname=rock1a`, `shadtrans=1`, `damage=2000`, chain to `Rock1b→rockgone` |

## 3  Where they fall in the render steps

### 3.1  Composer staging

Features are **outside** the strip model:

```
tiles
strips 0,1,2
feature-pass-1 (row-major, owns unexplored bit)
strips 3,4
Y-bucket build + interleaved soft-unit / feature-pass-2
strip 5
gated( mode!=0 ): strip6 + projectiles + fixed-effects(300) + strip7 + hard units
strip 8 always
labels(for owner==local) + strip9 gated + overlays gated
fog (hard 32px tiles, after world, before selection)
selection rect + interface
```

Great Divide has no projectiles/effects at load, but its features still run in
the two feature passes above.

### 3.2  Screen placement

World positions live in `16.16` fixed. The dispatch computes an inclusive
anchor rectangle then clips it:

```
screenX = footX*8 + (cellX+8)*16 - cameraX
screenY = footZ*8 - (h0+h1+h2+h3)/8 + (cellZ+2)*16 - cameraZ
```

Heights are terrain bytes from the footprint's four corner cells; `>>3` is
half the bilinear average, matching the unit half-height shear
`screenY = worldZ - worldY/2 - camZ + 32`. Footprint halving centers `3×3`
rocks over their anchor.

Inside the dispatch:

- **Shadow** (optional): if the options word enables shadowing and the
  feature declares a shadow sequence (`seqnameshad` or the animated runtime
  copy) and `shadtrans` gates the translucent path, the shadow frame blits
  first at the same anchor.
- **Normal**: for static features the first frame blits; for animated features
  the cursor-dereferenced frame blits; burning instances blit from the
  burning-slot cursors. The blitter composes subframes, skips transparent
  holes, and clips to an inclusive `right = left + w -1` rectangle; entirely
  off-screen features are culled whole without allocating.

Painter order is `Y`-sorted: pass-2 walks bucket rows ascending and draws
features whose corrected Y matches the row in enumeration order, so tree
crowns correctly interleave with units and never use a depth buffer.

### 3.3  Visibility / fog / memory — what gets drawn

Short features (`height < 10`) participate in a memory test before LOS:

- If the definition lacks `nodrawundergray`, or the plot's placer nibble
  (`(flags>>3)&0xF`, `10` for map-authored) equals the local player slot, the
  feature draws irrespective of LOS.
- Otherwise LOS must pass the four-corner predicate at `cell+4`.

Tall features (`≥10`) set the plot's never-seen bit (`+0xC|0x04`) even when
LOS would have admitted them, making them keep a foggy halo. Their draw call
is otherwise gated by the same memory-or-LOS rule.

Great Divide's trees/shrubs/rocks are all `height 40/20/4`; the tall path
activates for the `40`/`20`-high foliage/rocks.

### 3.4  Shadows

Shadow existence is two-gated:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

## 4  Which are reclaimable vs not

### 4.1  Flags that matter

| Flag | Default | Meaning |
|---|---|---|
| `reclaimable` | 0 | order system gate: only `1` accepts the reclaim work state |
| `autoreclaimable` | 1 | stuffer gate: implicit footprint collision clears the cell only when honored |
| `indestructible` | 0 | damage + teardown gate: `1` makes weapon damage ignored and teardown skip |
| `blocking` | 0 | path/yard gate: `1` makes the footprint impassable for generic placement |
| `geothermal` | 0 | yard gate: required by `G` control byte |

Economy yield (reclaim) is detached from extractor economy:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### 4.2  Great Divide table

- **Reclaimable + blocking + flamable:** `Tree1..Tree6`, `Shrub1..3`,
  `Rock1a` (and their hurt chain). 250/20/100 yields, damagable.
- **Reclaim successor chain:** `Tree* → Smudge01..03`, `Shrub*→Smudge*`,
  `Rock1a→Rock1b→rockgone*`. Smudges are `1×1` non-reclaimable, non-blocking;
  the chain is `featuredead` on lethal damage and `featurereclamate` on
  successful reclaim, sharing sizes where documented.
- **RockMetal deposits:** `3×3` `RockMetal*`, `indestructible=1`,
  `reclaimable=0`, not blocking, height 4. Traversable. Never reclaimable nor
  damageable; They persist for the whole session.
- **Vents:** `Geothermal`, `animating=1`, `geothermal=1`, `indestructible=1`.
  Not reclaimable; only used for footprint test.

### 4.3  Burning vs reclaim interaction

`flamable=1` trees/shrubs ignite via the burn-anim sequence. While a burning
timer is active (`sparktime*30/2` centroid jittered via simulation RNG) the
feature rejects reclaim until the animation cursor clears and the
`featureburnt` swap fires (e.g. `Tree1 → Tree1Dead`). After the swap the
burnt remnant may itself be reclaimable per its definition.

## 5  How they impact pathfinding and building placement before and after reclaim

### 5.1  Blocking substrate

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

- **Before reclaim on Great Divide:** forests (trees/shrubs/`Rock1a`)
  partition the pass; metal deposits and vents are traversable; building a
  `o`-typed structure that covers a tree is yard-rejected.
- **After reclaim:** teardown clears `0xFFFF` and fringe `0xFF`, successor
  smudges are `1×1` non-blocking. A reclaimed tree corridor becomes
  traversable and buildable; reclaimed/degenerated rocks free their `4×3`
  blocks. Vents and metal deposits never clear.

### 5.2  YardMap validation

The building-class validator walks ten control bytes row-major over the
footprint. Anchors are reconstructed by resolving fringe `0xFFFE` through its
signed deltas before classifying the feature reference (`empty`, `real`,
`blocked` for voids/ threshold or out-of-range/ fringe-orphan). Tested bits:

- `bit0` enemy visibility occupant,
- `bits1-2` mobile occupiers vs `self`,
- `bit3` slope, `bit4` height,
- `bit5` require blocking-feature-free,
- `bit6` require not non-reclaimable-typed,
- `bit7` geothermal required.

`'.' 0x00`, `'C' 0x35`, `'G' 0x8f` (geothermal), `'O' 0x2b`, `'Y' 0x31`,
`'c' 0x2d`, `'f' 0x6f`, `'o' 0x2f`, `'w' 0x37`, `'y' 0x29`.

`G=0x8f` carries `bits7|3|2|1|0` and **lacks** `bit5`, so a geothermal plant
covers a vent. `o=0x2f` lacks `bit7` but carries `bit5`, so it is rejected
over any blocking foliage. Geothermal validation is read-only; placing a
plant leaves the vent stamp behind, and destroying the plant re-exposes the
same vent cell without a restore step.

### 5.3  Great Divide walkthrough

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

