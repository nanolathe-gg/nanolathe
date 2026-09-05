// Package ebitenapp is the concrete Ebitengine platform adapter: window and
// game-loop lifecycle, device input polling, and framebuffer upload.
//
// It is the only package that imports Ebitengine. internal/client composes an
// indexed framebuffer and expands it through the palette without touching a
// device, so the authoritative packages and the pure presentation packages
// keep a dependency closure that stands up with no display [I6].
package ebitenapp
