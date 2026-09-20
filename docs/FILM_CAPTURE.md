# Film capture

`--film` composes a scripted sequence of presented frames offline and writes
them as PNGs or as a raw stream an encoder reads. It is how promotional and
demonstration footage is made: a film script names a scene, the shots that look
at it, the camera move inside each shot and the titles drawn over them.

```
tools/film films/announce.json /tmp/announce.mp4 --score --fade-out 0.5
```

`tools/film` builds the binary, streams packed RGBA on stdout and hands it to
ffmpeg. No intermediate frame files are written. The encoder writes a temporary
MP4 beside the destination and replaces the destination only after capture and
encoding both succeed; a failed render preserves an existing output. Python 3,
Go, ffmpeg and retail assets are required. Keep the output outside the repository.
The optional score uses a temporary WAV; omit `--score` for a silent render.

The example is a 24-second announcement: eight shots across four maps, with
separate armor and aircraft compositions, short feature captions, platform
support, modding and a five-second website card. See [the edit notes](../films/README.md).

To inspect frames instead of encoding them:

```
go run ./cmd/nanolathe --film films/announce.json --film-out /tmp/frames
go run ./cmd/nanolathe --film films/announce.json --film-out /tmp/frames --film-frames 30
```

`--film-frames` stops early, which is how a framing or a title is checked
without paying for the whole sequence.

## Why it is not a screen recording

A capture is not paced. A frame that takes half a second to compose still lands
on its own slot in the sequence, so the result is correct at its scripted rate
even when the scene is far too heavy to play at that rate. Three things follow:

* **Resolution is free of the display.** The frame is read back from the
  composed surface, not from a window, so 1440p or 4K costs time and nothing
  else.
* **The rate is exact.** The authoritative simulation stays at 30 Hz
  [01 §4.1]. A capture rate is a whole multiple of it, and each presented frame
  is composed at its own exact blend fraction `k/n` — never a wall-clock
  sample. This is the Enhanced interpolation path the modern window already
  uses (DESIGN_GPU_RENDERER §13.5); the film supplies the fraction instead of
  the host clock.
* **It is reproducible.** Two runs of one script produce byte-identical
  frames, so a shot can be re-rendered after a tuning change and cut into the
  same timeline.

Sprite and animation frame indices and the nanoframe reveal are never
interpolated (§13.5), so effects still step at 30 Hz inside a 60 FPS sequence,
exactly as they do in the window.

## Script format

A script is JSON. Unknown fields are rejected — a silently ignored camera key
is a whole wasted render — and validation reports every problem at once.

```json
{
  "width": 1920, "height": 1080, "fps": 60,
  "clean": true, "letterbox": 0.055, "messages": false,
  "scene": {"kind": "battle", "map": "Great Divide", "seed": 7,
            "pre_ticks": 240, "per_side": 160, "buildings": 16,
            "factories": true, "fog": false},
  "shots": [{
    "name": "opening", "ticks": 180,
    "camera": [{"tick": 0, "x": 0, "z": 40, "zoom": 0.62},
               {"tick": 180, "x": 0, "z": 0, "zoom": 0.82, "ease": "inout"}],
    "text": [{"at": 20, "ticks": 140, "style": "title", "anim": "wipe",
              "in": 22, "out": 24, "lines": ["NANOLATHE"]}]
  }]
}
```

| Field | Meaning |
| --- | --- |
| `width`, `height` | written frame size; even sides, for the usual 4:2:0 encoders |
| `fps` | capture rate; a multiple of 30, so one tick covers a whole number of frames |
| `clean` | write the world viewport only, with no battle interface (below) |
| `letterbox` | bar height as a fraction of the frame, each bar, clamped to a quarter |
| `messages` | keep the battle message column; a film silences it by default |
| `shots[].ticks` | the shot's length in simulation ticks; shots run back to back |

All times are simulation ticks. All sizes and anchors are fractions of the
frame, never pixels, so one script composes the same picture at 720p and 4K.

## Scenes

The scene is a capture fixture: it places units directly rather than opening a
normal skirmish, and its composition and spacing are film choices, not retail
rules. It deliberately does not share the benchmark's stager, whose composition
is pinned by the performance baselines it feeds
([BATTLE_BENCHMARK](BATTLE_BENCHMARK.md)).

* `battle` — two armies of `per_side` armed mobile units facing each other
  across a flat stretch the fixture searches for, `buildings` rear structures
  per side, and factory production when `factories` is set. Every mobile unit
  is ordered across the gap, so combat, pathfinding, COB and construction all
  run production code.
* `skirmish` — an ordinary fresh battle, framed on the viewing player's own
  start. Quieter footage: a base, not a front.

The top-level `scene` starts the film. A shot may carry its own complete
`scene` object to load a fresh map/session at that cut; shots without one
continue the current session. Each override gets independent defaults, not
values inherited from the previous scene. The first shot's override, if any,
replaces the top-level scene before rendering. The capture stays inside one
Ebitengine loop and retires the previous client's workers and map-bound GPU
resources before composing the new scene.

`roster` selects a capture composition: `mixed` (the original combined force),
`armor` (armed ground vehicles) or `air` (authored fighters/bombers). Air starts
through the existing airborne creator and cruise-altitude interfaces, uses the
ordinary flight order, and has no rear buildings or factory production. This
is staged footage, not a new gameplay rule or a standard skirmish opening.

An optional `anchor: [x, z]` gives an authored world-pixel centre for the fixture;
without it, the battle fixture searches for a large dry area. Anchors and staged
positions must fit the map. This allows an aerial battle over a coast without
asking the dry-land search to find an ocean. The announcement uses
`Coast to Coast`, anchor `[2450, 1000]`, for its reflected aircraft pass.

A scene's explicit `seed` wins over the command-line seed; when neither supplies
one, film capture uses seed 1. `pre_ticks` advances each new scene before its
first frame is written. `fog` keeps
the viewing player's fog; a film reveals the map by default, through the same
path as the `+nowisee` developer command, because a film shows the battle
rather than one side's knowledge of it.

## Camera and cuts

Camera keys are world pixels **relative to the scene anchor** — the fixture's
own centre — unless a key sets `"space": "world"`. `zoom` is the factor the
window calls 1× (DESIGN_GPU_RENDERER §16.2); it is clamped to the map's own
floor and to 2×. `ease` names the curve used to arrive at that key:
`inout` (the default), `linear`, `in` or `out`. Position interpolates linearly
and zoom geometrically, so equal ticks cover equal magnification ratios and a
push reads as steady rather than self-decelerating.

The camera is evaluated **once per tick**, never per presented frame. Enhanced
samples the camera origin at the authoritative step and blends the two samples
itself (§13.5), which is what makes a pan sub-pixel smooth at 60 or 120 FPS;
moving it per frame would fight that blend instead of feeding it.

Shots cut hard. At a cut the capture collapses the camera blend onto the
incoming shot (`SnapCameraBlend`), so the outgoing framing is not a move the
new shot slides out of.

A shot whose keys rise above 1× makes the capture synthesize the detail view's
2× art at load time (§14.1, §14.4); a film that stays at or below native skips
it, because no frame could show one of those pixels.

## Clean captures

The battle interface covers the composed surface's leftmost 128 columns and its
top and bottom 32 rows [03 §4.1]. A clean capture composes a surface that much
larger and writes only the world viewport out of it, so it gets interface-free
footage at exactly the requested size without a second viewport contract in the
camera. `film.ChromeInsetX/Y` and `camera.OriginX/Y` are held equal by a test.

The message column draws inside the world viewport, so it cannot be cropped
away; a film silences it at the ring instead, by configuring one authored line
[07 R-HUD-03 §14.3]. Set `"messages": true` to keep it.

Drop `"clean"` to capture the interface as the player sees it. Both belong in a
reel: the clean frames are the beauty shots, the full frames are the proof that
it is a game.

## Titles

Overlay text is drawn onto the composed frame after readback. Titles and lower
thirds use **Barlow Condensed ExtraBold**; subtitles and captions use **Barlow
Medium**. Both preserve mixed case. These are independently licensed OFL faces,
not retail GAF fonts. The bundled high-resolution coverage atlases and metrics
need no host font installation or new Go module. Provenance, pinned source
hashes, the license and regeneration instructions are in
[`internal/film/assets`](../internal/film/assets/README.md).

| `style` | Where |
| --- | --- |
| `title` | centred, large |
| `subtitle` | centred, below the title |
| `lower` | left, lower third; `"rule": true` draws a rule above it |
| `caption` | centred, near the bottom |

| `anim` | How it arrives |
| --- | --- |
| `fade` | opacity only |
| `rise` | opacity and a short slide up |
| `wipe` | a reveal column sweeping left to right |
| `type` | characters appear one at a time |

`at` and `ticks` are the cue's start and length within its shot; `in` and `out`
are the animation and fade lengths. `size` overrides the style's cap height as
a fraction of frame height, `x`/`y` its anchor, `align` its alignment and
`color` its face colour. `font: "display"` or `"body"` overrides the style's face. A subtle
shadow and edge halo keep type legible without the old heavy stroke outlines.
An optional `scrim` between 0 and 1 darkens the entire frame by that opacity,
following the cue's fade envelope. Use it on only the first cue in a shot to
avoid stacking scrims or dimming earlier text; the end card uses it to give the
website a quiet background.

An unknown style or animation is a script error rather than a default: a title
that quietly does not appear costs a whole re-render to notice.

To review the typography, write the specimen sheet and look at it:

```
FILM_SPECIMEN=/tmp/face.png go test ./internal/film -run TestFontSpecimen -count=1
```

## Sound and encoding

`tools/film --score` adds an original deterministic electronic score generated
by `tools/film-score`, using Python's standard library. It contains no sampled
music or retail game audio: a 120 BPM pulse, minor synth ostinato, cut-aligned
impacts and transition swells, with a quieter resolving chord under the final
shot. The source is MIT licensed with the rest of Nanolathe. It follows shot
boundaries automatically; omit the flag when taking the footage into an editor
with another score. This is editorial sound design, not a gameplay-audio capture.

`--fade-out 0.5` fades the last half-second of the picture to black. Its timing
uses the actual requested capture duration, including `--film-frames` limits.
Output is H.264, 4:2:0, with fast-start metadata; scored output adds stereo AAC.
The generated score fades to silence at the end of the full script.

The exporter checks both child processes. A producer which fails after writing
valid frames must not be mistaken for success just because ffmpeg can encode
that short stream. Run the pipeline regression checks with:

```
python3 -m unittest discover -s tools -p film_test.py
```

## Cost

The announcement writes 1,440 frames at 1280×720/60 FPS. Cost depends on the
maps, unit counts, title sizes and detail-art cache; each fresh scene pays its
own loading and warmup cost. Offline capture is not a real-time performance
claim. It takes no benchmark host lock and must not run alongside a benchmark.

## What it does not do yet

* **No gameplay audio mixdown.** Capture composes frames only; the optional
  procedural score is added by the encoder. The audio backend's device seam
  could support a tick-timestamped PCM mixdown, but it does not exist.
* **No saved-game scenes.** `--load-save` reaches the battle through the
  windowed frontend, so a film cannot yet start from a save.
* **No follow camera.** The camera takes scripted keys, not a unit to track.
