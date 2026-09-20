# Announcement reel

`announce.json` is a 42-second, 720p/60 FPS trailer. It is meant to sell the
feeling of returning to Total Annihilation, then give viewers one next step:
**nanolathe.gg**. Technical implementation detail belongs on the website.

```
tools/film films/announce.json /tmp/nanolathe-announce.mp4 --score --fade-out 0.5
```

For the 1080p upload master, add `--height 1080`; the close shots already sit
at the 2× zoom ceiling, so they show about half again as much battlefield as
the 720p cut rather than the same framing at more pixels.

The capture uses installed Total Annihilation content; no retail assets, frames,
video or music are bundled here. The font atlases carry their OFL license in
`internal/film/assets`. The optional electronic score is original procedural
sound design under the project's MIT license, not the Total Annihilation score
or recorded gameplay audio. The footage is staged in the real engine with the
modern renderer; the on-screen end card identifies the independent project,
development status and original-game asset requirement.

| Time | Scene | Shows |
| --- | --- | --- |
| 0–10 s | Greenhaven, skirmish opening | Whole-view tile reveal, commander drop, red-hot cooling, first extractor; title and music rise together |
| 10–14.5 s | Great Divide, 250 a side plus air | Strategic icons, then a zoom into the full battle |
| 14.5–18.5 s | Coast To Coast, fleets plus air off a beach | Shoreline waves, reflections, soft aircraft shadows |
| 18.5–22 s | Metal Heck, armor plus air | Dynamic light, bloom, glow |
| 22–25.5 s | Lava Run, heavy units | Heat shimmer, blast distortion, glowing wrecks |
| 25.5–29 s | Comet Catcher, heavy units | Debris, scorch marks, craters |
| 29–32.5 s | Ice Scream, builders and factories | Nanolathe construction; open source and modding |
| 32.5–36 s | Gasbag Forests, kbots plus air | Windows, macOS and Linux |
| 36–42 s | Painted Desert, 220 a side | Pull back to strategic icons; name and website |

Every shot is its own map, roster and formation; no scene is reused. Feature
shots run 3.5–4.5 s so a caption can be read and the picture still looked at.

White condensed headlines, restrained amber accents and readable mixed-case
supporting text leave the central action visible. Cuts land on the score's
half-second beat grid. The hook, platform card and final call to action use a contrast scrim;
the pale lunar battlefield gets a lighter one to keep its copy readable. The 720p capture canvas brings units and effects closer at the renderer’s
2× detail limit; larger output dimensions widen the world viewport rather than
simply increasing pixel density. The final four seconds deliberately hold one
address instead of introducing another feature.

To iterate, render a short preview with `--film-frames 120`. For later shots,
make a temporary copy of the JSON outside the repository containing that shot
and its scene override, then inspect the resulting frames. Review a contact
sheet at every cut and at full-resolution title holds, and watch the full
encoded video before publishing. Capture settings and `--film` behavior are
documented in [FILM_CAPTURE](../docs/FILM_CAPTURE.md).
