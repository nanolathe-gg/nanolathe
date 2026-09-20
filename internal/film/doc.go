// Package film composes offline capture sequences: the film script that names
// scenes, their shots and camera moves, and the overlay pass that draws
// animated titles onto a composed frame.
//
// Everything here is presentation. The package reads no session state, draws
// no RNG and never reaches authoritative state [I6]; the capture route in
// cmd/nanolathe owns the Ebitengine loop, the client and the device, and hands
// this package the readback buffer after the frame is composed.
//
// Overlay typography uses licensed display and body glyph atlases, independent
// of retail GAF fonts (docs/FILM_CAPTURE.md "Titles"). Promotional wording and
// its typography are capture presentation, never game content or behavior.
package film
