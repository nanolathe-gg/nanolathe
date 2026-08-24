# Spec conflicts observed against the reference install

`research/retail-executable-spec` is the behavior authority, but it was derived
from static analysis of one executable and a few of its statements are
contradicted by a real, working retail install. This file records those, with the
evidence, so nobody "fixes" the code back toward the spec later.

Reference install: `~/TotalAnnihilation` — base + Core Contingency + Battle
Tactics + patch 3.1. Probed through this repo's `vfs` package.

---

## SC1 — There is no ten-archive cap we can honor

**Spec** `[02 §2]`: "Local `*.HPI` with flag 0, up to ten successfully mounted
archives (the eleventh successful local HPI is not mounted)."

**Observed:** the install has **13** local HPI archives — `tactics1..8.hpi`,
`totala1..4.hpi`, `worlds.hpi` — plus five `.ccx`, eleven `.ufo`, and one
`.gp3`, and it plays. A ten-archive cap must drop three of them. In our lexical
tier order it would drop `totala3.hpi`, `totala4.hpi` (152 `camps/` files and
100 maps) and `worlds.hpi` (837 map `sections/`), which would remove the
campaign and most maps. Under any other enumeration order it drops a different,
equally load-bearing three.

Any install with base + both expansions + a handful of downloadable units
exceeds ten. The cap as stated cannot be a general truth about how retail
resolves content.

**Decision:** mount every local HPI. If more than ten are present, emit one
diagnostic naming them, so the discrepancy stays visible. The spec's cap is
recorded here rather than implemented.

**Falsifies:** the earlier PLAN_01 contract C2. Do not reintroduce it.

---

## SC2 — `GAMEDATA.TDF` does not exist

**Spec** `[02 §1]`: "The known hard requirements include `MOVEINFO.TDF` and
`GAMEDATA.TDF`."

**Observed:** no file named `gamedata.tdf` exists anywhere in the 31 mounted
providers. `gamedata/` contains exactly 13 files: `allsound.tdf`,
`buildinfo.tdf`, `category.tdf`, `help.tdf`, `los.tdf`, `meteor.tdf`,
`moveinfo.tdf`, `sidedata.tdf`, `sound.tdf`, `translate.tdf`, `unitview.tdf`,
`version.tdf`, `weapons.tdf`.

The most likely reading is that the spec means the `gamedata\` **directory**,
whose absence is indeed fatal.

**Decision:** `MOVEINFO.TDF` and `SIDEDATA.TDF` missing are fatal; a missing
`gamedata/` directory is fatal; a missing `GAMEDATA.TDF` is **not**, because
making it fatal would refuse to boot on a genuine retail install.

---

## SC3 — Intra-tier ordering barely matters, and we can measure it

Retail resolves same-tier archives in `FindFirstFileA` order, which `[02 §2]`
itself calls "not a portable executable-defined order". We sort lexically inside
a tier (PLAN_01 divergence D1).

**Observed:** 323 logical paths exist in more than one same-tier provider —
231 `ccdata.ccx` vs `btdata.ccx`, 85 `totala2.hpi` vs `totala1.hpi`, 7 others.
Of those 323, **only 2 differ in size**: `anims/armhp1.gaf` (75,272 vs 68,136
bytes, ccdata vs btdata) and `installres/install.inf` (not game content).

So the divergence has essentially one observable consequence in the whole
install, and the manifest names the winner. Acceptable; keep the deterministic
lexical rule.

---

## SC4 — `vfs.EntryInfo.Name` is a base name, not a path

Not a spec conflict but the same class of trap. `EntryInfo.Path` is the logical
path; `EntryInfo.Name` is the base name; `FS.Entries()` returns one entry **per
mount** and is not deduplicated by logical path. Code that groups by `Name`, or
that treats `Entries()` as the unique file set, silently produces nonsense (a
first pass at the probe above "found" 8,246 unique paths and top-level
directories named `battleroom.pcx`).

`FS.Manifest()` (PLAN_01 WU-01-2) is the deduplicated view; use it.

---

## Reproducing these measurements

Every number above came from a throwaway program against the real install. To
rerun (adjust for whatever the `vfs` API looks like by then):

```go
fs := vfs.New()
fs.MountGameDirectory(filepath.Join(os.Getenv("HOME"), "TotalAnnihilation"))
fmt.Println("mounts:", fs.MountCount())
for _, e := range fs.Entries() {          // one record PER MOUNT, see SC4
    if e.IsDir { continue }
    if srcs := fs.Sources(e.Path); len(srcs) > 1 {
        // srcs[0] is the winner; compare Source.Priority for same-tier peers
    }
}
```

Keep such programs in a scratch directory, not in the repo — the reusable form
is PLAN_01's `WU-01-5` coverage test.

## How to add to this file

One section per conflict: what the spec says, what was observed and how, the
decision, and which contract it changes. Evidence must be reproducible — a probe
against `~/TotalAnnihilation` that another agent can rerun.
