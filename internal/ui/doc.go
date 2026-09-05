// Package ui owns the mutable screen-level state of the authored panels: the
// front-end panel (focus, list contents and scroll positions, text fields) and
// the battle state the desktop binary and its tests share.
//
// It holds state, not pixels: internal/gui supplies the authored layout and
// internal/client draws it [07 §5] [07 §11].
package ui
