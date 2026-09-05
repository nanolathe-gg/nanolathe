// Package client is the window and frame loop.
//
// Presentation samples committed state with no interpolation, matching
// retail's draw path [03 §2.4]. The sim never reads
// wall-clock time, input state, camera, or renderer state (I6). Client owns the
// update boundary but never advances a clock or mutates sim state itself; the
// injected Step callback owns those effects (PLAN_03 C15/C16, PLAN_04A C9).
//
// The presentation backend is a pure-Go software framebuffer: composeIndexed
// produces the indexed image, the palette expands it to RGBA once per frame,
// and Ebitengine uploads and presents it. No sim package imports this package
// (I6).
package client
