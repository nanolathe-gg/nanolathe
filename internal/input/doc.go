// Package input is the platform-neutral key and mouse vocabulary the interface
// is written against.
//
// It names keys and buttons, records the per-frame edge and held state the HUD
// and the front end read, and carries the command latch the pointer pass sets.
// Nothing here touches a window or a device: the platform adapter fills these
// values in and the presentation layer consumes them [07 §9].
package input
