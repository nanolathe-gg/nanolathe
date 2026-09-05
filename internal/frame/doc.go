// Package frame is the committed simulation-to-presentation boundary.
//
// A Frame is assembled by the single simulation writer and becomes immutable
// when Buffer.Publish succeeds.  Presentation samples the frame committed for
// the current tick; it does not interpolate between ticks [03 §2.4].  The
// buffer deliberately does not retain a previous frame or clone on publish.
//
// The caller must finish reading Buffer.Current before the next BeginWrite.
// BeginWrite reuses the slot that is not committed, so retaining a pointer to
// an older frame across the next write is a data race.  This is the explicit
// single simulation-writer/presentation-reader lifetime contract; this package
// does not add speculative locking or a third historical slot.
package frame
