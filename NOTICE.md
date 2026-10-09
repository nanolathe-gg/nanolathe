# Licensing and provenance

Nanolathe's original code and documentation are licensed under [MIT](LICENSE).
That grant does not cover rights in Total Annihilation game content, extracted
assets, or derivatives of those assets. Generated remaster artwork is excluded from this curated copy; see
[the review](docs/PUBLICATION.md) for provenance limits.

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
| github.com/hajimehoshi/go-mp3 | v0.3.4 | Apache-2.0 |
| github.com/jfreymuth/pulse | v0.1.3 | MIT |
| golang.org/x/image | v0.45.0 | BSD-3-Clause |
| golang.org/x/sync | v0.22.0 | BSD-3-Clause |
| golang.org/x/sys | v0.47.0 | BSD-3-Clause |
| golang.org/x/text | v0.41.0 | BSD-3-Clause |

## Bundled font

`internal/upscale/chrome/font/SairaCondensed-800.ttf` is an instance of Saira
(condensed width, weight 800) by The Saira Project Authors, cut from its
variable font with fontTools. It is licensed under the
[SIL Open Font License 1.1](internal/upscale/chrome/font/OFL.txt), not MIT,
and is embedded in the binary to draw the remastered interface captions.

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

## Adapted Go mathematics routines

`internal/sim/numeric/radians.go` adapts Go 1.27.1's `math/sin.go`,
`tan.go`, `atan.go`, `atan2.go`, `asin.go` and `trig_reduce.go`. Architecture
dispatch is removed and multiplication is explicitly rounded to binary64.
The Go and Cephes source notices are retained in that file. The Go license:

```text
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```
