# Announcement reel

`announce.json` is a 24-second, 720p/60 FPS trailer. It is meant to sell the
feeling of returning to Total Annihilation, then give viewers one next step:
**nanolathe.gg**. Technical implementation detail belongs on the website.

```
tools/film films/announce.json /tmp/nanolathe-announce.mp4 --score --fade-out 0.5
```

The capture uses installed Total Annihilation content; no retail assets, frames,
video or music are bundled here. The font atlases carry their OFL license in
`internal/film/assets`. The optional electronic score is original procedural
sound design under the project's MIT license, not the Total Annihilation score
or recorded gameplay audio. The footage is staged in the real engine with the
modern renderer; the on-screen end card identifies the independent project,
development status and original-game asset requirement.

| Time | Scene | Purpose |
| --- | --- | --- |
| 0–3 s | Great Divide, combined-arms firefight | Recognizable action and the return of Total Annihilation |
| 3–5.5 s | Comet Catcher, combined arms | Introduce Nanolathe and its open-source engine |
| 5.5–8.5 s | Coast to Coast, aircraft | A different palette and modern water reflections |
| 8.5–11 s | Great Divide, close firefight | Battlefield light, bloom and heat distortion |
| 11–13.5 s | Comet Catcher, later close firefight | Explosive light, flying debris and scorched ground |
| 13.5–16 s | Great Divide, factory line | Open source and modding |
| 16–19 s | Greenhaven, combined arms | Windows, macOS and Linux |
| 19–24 s | Great Divide, wide pullback | Name, website and invitation to try/contribute |

White condensed headlines, restrained amber accents and readable mixed-case
supporting text leave the central action visible. Cuts land on the score's
half-second beat grid. The hook, platform card and final call to action use a contrast scrim;
the pale lunar battlefield gets a lighter one to keep its copy readable. The 720p capture canvas brings units and effects closer at the renderer’s
2× detail limit; larger output dimensions widen the world viewport rather than
simply increasing pixel density. The final five seconds deliberately hold one
address instead of introducing another feature.

To iterate, render a short preview with `--film-frames 120`. For later shots,
make a temporary copy of the JSON outside the repository containing that shot
and its scene override, then inspect the resulting frames. Review a contact
sheet at every cut and at full-resolution title holds, and watch the full
encoded video before publishing. Capture settings and `--film` behavior are
documented in [FILM_CAPTURE](../docs/FILM_CAPTURE.md).
