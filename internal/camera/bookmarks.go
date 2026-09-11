package camera

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// BookmarkSlots is the number of camera bookmark slots retail keeps in the
// camera block: four origins with a valid byte each [07 R-CAM-01 §12].
const BookmarkSlots = 4

// Bookmark is one stored camera origin with its valid byte. The valid byte has
// no reader in the recall path — its only reader is the save/restore census —
// so a recall of an unwritten slot loads whatever the zero-initialised slot
// holds [07 R-CAM-01 §12].
type Bookmark struct {
	Origin Origin
	Valid  bool
}

// FollowState is the retail camera block's follow triple plus the bookmarks
// [07 R-CAM-01 §12]. Retail also holds a hold count with its frozen anchor and
// a followed-projectile reference; both belong to the projectile hold of
// [06 §7.3] and are not part of this unit.
//
// Desired is written by a *glide* (F3's message jump and the `n` unit cycle):
// only the desired origin moves, and the per-frame follow step closes the gap
// at the phase-10 rate. Tracked is written by `t`/`T` and Ctrl+C; while it is
// nonzero the follow step recomputes the desired origin from that unit every
// frame.
type FollowState struct {
	Desired   Origin
	Gliding   bool
	Tracked   pool.Handle
	Bookmarks [BookmarkSlots]Bookmark

	// latched is Nanolathe's own sequencing aid, not a retail structure
	// member: it is the tracked object captured by LatchTracked, before this
	// build's hotkey dispatch can change Tracked, so the follow application
	// that runs later in the same presentation frame still acts on the
	// object retail's phase 10 would have seen (see LatchTracked) [07
	// R-CAM-01 §12].
	latched        pool.Handle
	latchedDesired Origin
	latchedGliding bool
}

// SetTracked latches the follow camera's tracked object. Retail's `t`/`T` and
// Ctrl+C do not move the camera themselves: the first follow step after them
// begins the glide [07 R-CAM-01 §12].
func (c *Camera) SetTracked(h pool.Handle) {
	if c == nil {
		return
	}
	c.Follow.Tracked = h
	c.Follow.Gliding = false
}

// Tracked reports the current tracked object, zero when none.
func (c *Camera) Tracked() pool.Handle {
	if c == nil {
		return 0
	}
	return c.Follow.Tracked
}

// ClearFollow cancels the follow triple. It is what every writer marked "yes"
// in [07 R-CAM-01 §12]'s table does: the scroll pass, the minimap latch and
// drag-scroll entry, and a bookmark recall.
func (c *Camera) ClearFollow() {
	if c == nil {
		return
	}
	c.Follow.Tracked = 0
	c.Follow.Gliding = false
}

// GlideTo writes only the desired origin, clamped, leaving the current origin
// to the follow step — retail's "glide" [07 R-CAM-01 §12]. The point is a
// camera origin, not a world point; callers recentre first. The n/F3 writers
// preserve tracking, so a tracked object can replace this desired origin on
// the next phase-10 pass [07 R-CAM-01 §12].
func (c *Camera) GlideTo(x, z int32) {
	if c == nil {
		return
	}
	spanW, spanH := c.BattleView()
	leadX, _, leadZ, _ := c.clampInsets()
	c.Follow.Desired = Origin{
		X: clampAxis(x, c.MapW, spanW, leadX),
		Z: clampAxis(z, c.MapH, spanH, leadZ),
	}
	c.Follow.Gliding = true
}

// StepGlide advances a glide that no tracked object owns. It applies the same
// phase-10 bounded half-step FollowTo uses and reports whether the origin moved
// [07 R-CAM-01 §12][01 §4.4].
//
// The glide ends when the step stops moving the origin rather than at an
// equality test: the half-step is a truncating divide, so a one-pixel gap
// closes to zero movement, not to zero distance. That one pixel is stepAxis's
// arithmetic, shared with the tracked-object follow, and is not introduced
// here.
func (c *Camera) StepGlide() bool {
	if c == nil || !c.Follow.Gliding {
		return false
	}
	x, z := c.X, c.Z
	c.X = stepAxis(c.X, c.Follow.Desired.X)
	c.Z = stepAxis(c.Z, c.Follow.Desired.Z)
	if c.X == x && c.Z == z {
		c.Follow.Gliding = false
		return false
	}
	return true
}

// StoreBookmark copies the *current* origin into slot 0..3 and sets its valid
// byte [07 R-CAM-01 §12]. Out-of-range slots store nothing.
func (c *Camera) StoreBookmark(slot int) bool {
	if c == nil || slot < 0 || slot >= BookmarkSlots {
		return false
	}
	c.Follow.Bookmarks[slot] = Bookmark{Origin: Origin{X: c.X, Z: c.Z}, Valid: true}
	return true
}

// RecallBookmark jumps to slot 0..3 unconditionally — the valid byte is not
// consulted here, so an unwritten slot recalls its zero origin — then clamps
// and clears the follow triple [07 R-CAM-01 §12].
func (c *Camera) RecallBookmark(slot int) bool {
	if c == nil || slot < 0 || slot >= BookmarkSlots {
		return false
	}
	mark := c.Follow.Bookmarks[slot]
	c.ClearFollow()
	c.JumpTo(mark.Origin.X, mark.Origin.Z)
	return true
}
