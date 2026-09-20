// Package film composes offline capture sequences: the film script that names
// a scene, its shots and their camera moves, and the overlay pass that draws
// animated titles onto a composed frame.
//
// Everything here is presentation. The package reads no session state, draws
// no RNG and never reaches authoritative state [I6]; the capture route in
// cmd/nanolathe owns the Ebitengine loop, the client and the device, and hands
// this package the readback buffer after the frame is composed.
//
// The overlay's type is our own stroke font rather than a retail GAF font
// (docs/FILM_CAPTURE.md "Titles"): promotional titles are Nanolathe's own
// wording and should not be set in an authored retail face, and a stroke
// outline stays crisp at any capture resolution.
package film
