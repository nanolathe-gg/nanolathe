// Package gui loads the authored .GUI window files into the control kinds the
// front end and the battle HUD draw and hit-test.
//
// A window is one COMMON block plus its gadgets; every gadget carries the
// authored rectangle, attributes and association the layout is made of. This
// package parses and answers questions about that data; it draws nothing and
// owns no screen state [07 §5] [fmt gui].
package gui
