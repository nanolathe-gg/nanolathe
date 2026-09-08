# Licensing and provenance

Nanolathe's original code and documentation are licensed under [MIT](LICENSE).
That grant does not cover rights in Total Annihilation game content, extracted
assets, or derivatives of those assets. The development remaster examples need
separate disposition before publication; see [the review](docs/PUBLICATION.md).

## Go dependencies

Dependencies retain their own licenses. This is an inventory of the modules
required by `go.mod` at the publication review, checked against their locally
cached license files. They are downloaded by Go, not vendored into this tree.

| Module | Version | License |
| --- | --- | --- |
| github.com/hajimehoshi/ebiten/v2 | v2.10.0-rc.3 | Apache-2.0, with bundled component notices |
| github.com/ebitengine/gomobile | v0.0.0-20260820040257-d11f821a26a6 | BSD-3-Clause |
| github.com/ebitengine/hideconsole | v1.0.0 | Apache-2.0 |
| github.com/ebitengine/oto/v3 | v3.5.0 | Apache-2.0 |
| github.com/ebitengine/purego | v0.11.0 | Apache-2.0 |
| github.com/jfreymuth/pulse | v0.1.3 | MIT |
| golang.org/x/sync | v0.22.0 | BSD-3-Clause |
| golang.org/x/sys | v0.47.0 | BSD-3-Clause |

This inventory is not a replacement for license texts accompanying a binary.
For each binary release, collect the licenses and notices for the actual linked
modules and bundled native components, including
[Ebitengine's component notices](https://github.com/hajimehoshi/ebiten/blob/v2.10.0-rc.3/NOTICE.md).
Recheck when dependencies or build targets change.

## Contribution provenance

Contributions must be independently authored and suitable for distribution
under the project license. Preserve any required third-party attribution and
identify external sources when submitting a change. Do not submit extracted
game art, retail source code, decompiler output, or code copied from GPL projects.
See [AGENTS.md](AGENTS.md) for the research and implementation rules.

Repository inspection cannot certify that every line is independently authored.
The publication review records the evidence checked and outstanding questions.
