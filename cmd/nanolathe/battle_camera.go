package main

// Battle camera: where it starts, what it follows, and the scroll setting
// that drives it [07 §10] [07 R-CAM-01].

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/units"
)

// centerBattleStartCamera is the retail battle-start camera placement: the one
// camera writer between the world rebuild's camera reset and the first composed
// frame, and a *jump* rather than a glide [07 R-CAM-01 §12 "Battle-start
// placement"]. It branches on the session kind, exactly as retail does.
//
// Campaign (kind 1): the first start-position special with stored number 0 —
// the special authored as StartPos1 — is centred in the battle viewport. A
// mission with no such special keeps the world-rebuild reset position, and
// there is no diagnostic [08 "Campaign camera"]. The campaign spawner creates
// units straight from the mission's placement records and no commander, so
// nothing here looks for one.
//
// Skirmish (kind 2): retail's per-slot stamp helper resolves the slot's
// StartPos, creates the side's commander there, and centres the camera on the
// local player's commander [08 "Resource grant" and the stamp paragraph above
// it]. It never searches for a commander by name — it centres on the unit it
// just created — so the commander identity used here is the only one retail
// has: the definition name equals the Commander name on the owner's side
// record [08 R-SKIR-01 §3]. (Until this commit the search was a `"com"` suffix
// test on the unit name, which found no unit at all in Arm campaign mission 1 —
// it fields ARMFAV, ARMPW, ARMFLASH, ARMSTUMP, ARMROCK, ARMHAM and ARMGATE and
// no commander — leaving the camera at the reset origin and the world viewport
// black, and which would equally match any unit whose name merely ends in
// those letters.)
//
// Both branches centre through Camera.JumpToBattleViewCenter, which halves the
// battle viewport rather than the framebuffer and converts retail's camera
// origin into this build's [07 R-CAM-01 §12][03 §4.1]. Neither shears: both
// battle-start writers call the jump with `x = stampX − trunc(viewWidth/2)`
// and `z = stampZ − trunc(viewHeight/2)` and never read the Y word. The
// `(z − y/2)` shear of §12 belongs to the unit-position *glide* conversion
// alone [07 R-CAM-01 §14 "the battle-start jump has no height shear"], which
// is what glideToUnit applies and this path does not — the reading this file
// already had, now established rather than chosen.
//
// A watcher slot takes neither branch: see watcherBattleStartCamera.
func centerBattleStartCamera(sess *session.Session, cam *camera.Camera) {
	if sess == nil || cam == nil {
		return
	}
	if sessionLocalIsWatcher(sess) {
		watcherBattleStartCamera(cam)
		return
	}
	if sess.Mission != nil && sess.Mission.Type == mission.TypeCampaign {
		if start, ok := campaignStartPosition(sess.Mission); ok {
			cam.JumpToBattleViewCenter(int32(start.X), int32(start.Z))
		}
		// No special → keep the reset position, emit nothing
		// [08 "Campaign camera"].
		return
	}
	if u, ok := localCommanderUnit(sess); ok {
		cam.JumpToBattleViewCenter(int32(u.X>>16), int32(u.Z>>16))
	}
}

// sessionLocalIsWatcher reports whether the local slot carries the lobby
// record's watcher bit. The observer byte skirmish setup writes is the same
// exclusion everywhere else it is spent, so it is ORed in here exactly as the
// score panel's row filter does [07 R-HUD-04 §1].
func sessionLocalIsWatcher(sess *session.Session) bool {
	if sess == nil || sess.Econ == nil {
		return false
	}
	slot := int(sess.LocalOwner)
	if slot < 0 || slot >= len(sess.Econ.Players) {
		return false
	}
	p := sess.Econ.Players[slot]
	return p.Watcher || p.IsObserver
}

func watcherBattleStartCamera(cam *camera.Camera) {
	if cam == nil {
		return
	}
	viewW, viewH := cam.BattleView()
	cam.JumpToBattleViewCenter(2*(viewW/2), 2*(viewH/2))
}

// campaignStartPosition returns the start-position special the campaign camera
// jumps to: the first special in authored record order that is a start position
// and whose stored number is 0. mission.Special.ID is that stored number — the
// authored suffix minus one, so both StartPos1 and StartPos0 store 0
// [08 "Campaign camera"] [08 R-TRIG-01 §9] [fmt ota]. It held the authored
// label until WU-19-205, when the decoder was corrected; the comparison here
// moved with it (review finding R10).
// Record order is the OTA enumeration order and is deliberately not sorted:
// "first" is a scan, not a minimum.
func campaignStartPosition(m *mission.Mission) (mission.Special, bool) {
	if m == nil {
		return mission.Special{}, false
	}
	for _, sp := range m.Specials {
		if sp.Kind == 1 && sp.ID == 0 {
			return sp, true
		}
	}
	return mission.Special{}, false
}

// localCommanderUnit finds the local player's commander using retail's only
// commander identity: the unit's definition name equals the Commander name on
// its owner's side record [08 R-SKIR-01 §3]. Scan order is unit-pool record
// order, the order retail's own sweeps use.
func localCommanderUnit(sess *session.Session) (*units.Unit, bool) {
	if sess == nil || sess.Units == nil || sess.Catalog == nil {
		return nil, false
	}
	owner := int(sess.LocalOwner)
	// The PLAYER RECORD's side, not the setup row's: the setup row is the
	// pre-battle mirror and a load restores nothing into it but the rule words
	// and the map name, so a restored battle reads side 0 for every slot
	// [08 R-SKIR-01 §2] "Save persistence".
	side, ok := sess.SideForOwner(owner)
	if !ok {
		return nil, false
	}
	if side < 0 || side >= len(sess.Catalog.Sides) || sess.Catalog.Sides[side] == nil {
		return nil, false
	}
	name := strings.TrimSpace(sess.Catalog.Sides[side].Commander)
	if name == "" {
		return nil, false
	}
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive || u.Def == nil || int(u.Owner) != owner {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(u.Def.UnitName), name) {
			return u, true
		}
	}
	return nil, false
}

// applyCommittedShake transfers the cumulative phase-10 displacement from the
// current committed frame to the one camera used by input and rendering. It
// lives with battle camera ownership rather than Client so repainting the same
// frame cannot mutate camera state or consume another random value [03 §5.6].
func (b *battleSession) applyCommittedShake() {
	if b == nil || b.cam == nil || b.sess == nil || b.sess.Snapshot == nil {
		return
	}
	cur := b.sess.Snapshot.Current()
	if cur == nil {
		return
	}
	dx := cur.ShakeOffsetX - b.appliedShakeX
	dy := cur.ShakeOffsetY - b.appliedShakeY
	if dx != 0 || dy != 0 {
		b.cam.Pan(dx, dy)
	}
	b.appliedShakeX = cur.ShakeOffsetX
	b.appliedShakeY = cur.ShakeOffsetY
}

// scrollSetting returns the persisted scroll speed byte [02 "Settings"] [07 §10] C2.
// It is presentation-only and never touches sim [I6].
//
// The value is cached on b.scrollSpeedByte rather than re-read from disk on
// every call: the camera pan block in the per-frame input path (~handleInput)
// calls this once per host frame, and settings.Load is a full file open plus
// JSON parse [WU-19-114, reported by WU-19-109's profile: 8.8% of the frame
// loop]. primeScrollSetting fills the cache once at battle entry the way
// applyDamageBarsSetting(loadedSettings()) already primes the damage-bars bit;
// the lazy fallback here only fires for a battleSession built without going
// through that entry path (unit tests construct battleSession{} directly).
func (b *battleSession) scrollSetting() byte { // [07 §10] [02 "Settings"]
	if b == nil {
		return byte(settings.DefaultScrollSpeed)
	}
	if b.scrollSpeedByte == 0 {
		b.scrollSpeedByte = b.readScrollSetting()
	}
	return b.scrollSpeedByte
}

// readScrollSetting resolves the scrollspeed byte without caching. A battle
// composed with a frontend shell attached reads the shell's own copy —
// attachSettings already loaded it once at process startup [02 "Settings"],
// so this costs no I/O at all — and a shell-less battle (tests, --shot) falls
// back to a direct settings.Load, matching the one-read-per-battle behaviour
// the entry-point priming gives the windowed path.
func (b *battleSession) readScrollSetting() byte {
	var ss int
	if b != nil && b.shell != nil {
		ss = b.shell.scrollSpeed
	} else {
		s, _ := settings.Load()
		ss = s.ScrollSpeed
	}
	if ss <= 0 || ss > 255 {
		ss = settings.DefaultScrollSpeed
	}
	return byte(ss)
}

// primeScrollSetting caches the scrollspeed byte once at battle entry
// [WU-19-114]. Retail's in-battle ARMOPT modal chain (options -> exit ->
// confirm [07 "Tab options menu and manual exit"]) has no live settings
// editor — it only routes to save/load/main-menu/exit — so nothing inside a
// running battle can change the persisted scrollspeed; the frontend's own
// settings screens are reachable only before a battle exists (or after one
// ends, since returning to the main menu ends the battleSession), and each
// new battle re-primes the cache from composeBattleEntryDetached. Callable
// more than once if that ever changes; it always re-reads rather than
// trusting the existing cache.
func (b *battleSession) primeScrollSetting() {
	if b == nil {
		return
	}
	b.scrollSpeedByte = b.readScrollSetting()
}

// refreshScrollDelta advances the scroll pass's clock and returns this host
// frame's raw delta [07 §10].
//
// Retail's scroll pass does not measure the frame in milliseconds: it reads
// the delta word the tick-budget step stored earlier in the same host frame,
// which is this frame's scaled reading minus the previous frame's, and the
// scaled reading is floor(hostMillis*30/1000) — thirtieths of a second, the
// simulation timebase of [01 §4.1]. Feeding milliseconds here multiplies the
// scroll rate by thirty at the source and then pins every frame to the 128-pixel
// cap, which is defect PT3-11
// [07 §10].
//
// Because the reading is integral, most frames at 60 Hz return 0 and scroll
// nothing; the sustained rate is scrollByte*30 map pixels per second at any
// frame rate.
//
// The paused branch below looks like the bug this function fixes and is not:
// it is what retail does, established from the image and written up in
// [07 §10 "The scroll pass while paused"]. In single-player the pump gates the
// budget call behind the pause test, and the delta word has exactly one writer
// — that budget step — so while paused neither the delta nor the anchor is
// refreshed. The scroll pass itself is gated only on the in-battle options
// window, never on pause, so it keeps running and keeps re-multiplying the
// frozen delta. Retail therefore scrolls a paused camera at scrollByte map
// pixels per host frame when the pause landed on a frame whose delta was 1,
// and not at all when it landed on a frame whose delta was 0. Do not "fix"
// this into a zero: paused camera movement is a feature (the player looks
// around a frozen battle), and its rate is retail's.
//
// Leaving the anchor alone across the pause is the same contract: the first
// unpaused frame spends the whole pause in one delta and takes a single
// 128-pixel capped step, the scroll-pass twin of the single-player unpause
// burst of [01 §4.3].
func (b *battleSession) refreshScrollDelta() int32 { // [07 §10]
	if b == nil {
		return 0
	}
	if b.sess != nil && b.sess.Clock != nil && b.sess.Clock.Paused {
		// Established, not inferred: neither delta nor anchor moves while
		// single-player is paused [07 §10 "The scroll pass while paused"].
		// Multiplayer differs — its budget runs every iteration — but this is a
		// single-player engine.
		return b.scrollDelta
	}
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	now := clock.ScaledNow(b.millisSource.Millis32())
	if !b.scrollAnchorSet {
		// Retail's anchor is already tracking wall-clock when the battle mode
		// takes over, so its first battle frame sees an ordinary one-frame
		// delta. Seeding here reproduces that instead of charging the whole
		// pre-battle uptime to the first frame.
		b.scrollAnchor, b.scrollAnchorSet, b.scrollDelta = now, true, 0
		return 0
	}
	b.scrollDelta = now - b.scrollAnchor // signed; a wrapped host counter reverses one frame [07 §10]
	b.scrollAnchor = now
	return b.scrollDelta
}

// followCommander latches the follow camera onto the last own unit in the
// `Commander` category set [07 R-CAM-01 §2][07 R-CAM-01 §12].
func (b *battleSession) followCommander() {
	if b == nil || b.cam == nil {
		return
	}
	mask, ok := b.categoryMask("Commander")
	if !ok || mask.IsZero() {
		return
	}
	f, found := b.currentSnapshot()
	if !found {
		return
	}
	var last pool.Handle
	for i := range f.Units {
		v := f.Units[i]
		if b.sess == nil || v.Owner != b.sess.LocalOwner || v.Slot == 0 {
			continue
		}
		if b.inCategory(v, mask) {
			last = v.Slot
		}
	}
	if last != 0 {
		b.cam.SetTracked(last)
	}
}

// onScreenUnit reports whether a committed unit projects inside the battle
// viewport — the on-screen list Ctrl+S selects from and the `n` cycle marks
// visited [07 R-CAM-01 §2][07 §8].
func (b *battleSession) onScreenUnit(v frame.UnitView) bool {
	if b == nil || b.cam == nil {
		return false
	}
	sx, sy := b.cam.WorldToScreen(v.X, v.Y, v.Z)
	w, h := b.cam.BattleView()
	sx -= camera.OriginX
	sy -= camera.OriginY
	return sx >= 0 && sy >= 0 && sx < w && sy < h
}

// cycleFollowTarget is `t` / `T`: the tracked object becomes the next (or, with
// Shift, the previous) selected unit after the current tracked object in
// unit-slot order, wrapping; with nothing selected it becomes null
// [07 R-CAM-01 §2][07 R-CAM-01 §12].
func (b *battleSession) cycleFollowTarget(previous bool) {
	if b == nil || b.cam == nil {
		return
	}
	sel := b.selectedHandlesInSlotOrder()
	if len(sel) == 0 {
		b.cam.SetTracked(0)
		return
	}
	current := b.cam.Tracked()
	idx := -1
	for i, h := range sel {
		if h == current {
			idx = i
			break
		}
	}
	var next pool.Handle
	switch {
	case idx < 0 && previous:
		next = sel[len(sel)-1]
	case idx < 0:
		next = sel[0]
	case previous:
		next = sel[(idx+len(sel)-1)%len(sel)]
	default:
		next = sel[(idx+1)%len(sel)]
	}
	b.cam.SetTracked(next)
}

// cycleNextUnvisitedUnit is `n`: find the next own unit this cycle has not
// visited, glide the camera to it, record it as the current unit, and mark it
// and every on-screen own unit visited. When every unit has been visited the
// visited set is cleared and the cycle restarts. It does not change the
// selection [07 R-CAM-01 §2][07 R-CAM-01 §12].
func (b *battleSession) cycleNextUnvisitedUnit() {
	f, ok := b.currentSnapshot()
	if !ok || b.cam == nil || b.sess == nil {
		return
	}
	pick, found := b.firstUnvisitedOwnUnit(f)
	if !found {
		b.visitedUnits = nil
		pick, found = b.firstUnvisitedOwnUnit(f)
	}
	if !found {
		return
	}
	b.glideToUnit(pick)
	b.currentUnit = pick.Slot
	b.markVisited(pick.Slot)
	for i := range f.Units {
		v := f.Units[i]
		if v.Slot != 0 && v.Owner == b.sess.LocalOwner && b.onScreenUnit(v) {
			b.markVisited(v.Slot)
		}
	}
}

func (b *battleSession) firstUnvisitedOwnUnit(f *frame.Frame) (frame.UnitView, bool) {
	for i := range f.Units {
		v := f.Units[i]
		if v.Slot == 0 || b.sess == nil || v.Owner != b.sess.LocalOwner {
			continue
		}
		if b.visitedUnits[v.Slot] {
			continue
		}
		return v, true
	}
	return frame.UnitView{}, false
}

func (b *battleSession) markVisited(h pool.Handle) {
	if b.visitedUnits == nil {
		b.visitedUnits = make(map[pool.Handle]bool)
	}
	b.visitedUnits[h] = true
}

// glideToUnit writes the desired origin from a unit position through the one
// conversion of [07 R-CAM-01 §12]: the map-pixel X and the height-sheared Z,
// recentred on the battle viewport.
func (b *battleSession) glideToUnit(v frame.UnitView) {
	if b.cam == nil {
		return
	}
	x := radarMapPixel(v.X)
	y := radarMapPixel(v.Y)
	z := radarMapPixel(v.Z)
	ox, oz := b.cam.BattleViewCenterOrigin(x, z-y/2)
	b.cam.GlideTo(ox, oz)
}

// glideToMessageSource is F3 [07 R-CAM-01 §14 "F3's leading clear is a
// different bit from the visited bit"]:
//
//  1. clear bit 0x20 on every one of the thirty message-ring records;
//  2. scan from the display index toward the producer index, wrapping at 30 —
//     oldest displayed message first — for a record whose source unit id is
//     nonzero, whose bit 0x10 is clear and whose unit is alive; on a hit set
//     both bits and glide to that unit;
//  3. if the scan finds nothing, clear bit 0x10 on every record and scan once
//     more.
//
// The leading clear and the scan's test are different bits, so "not yet
// visited" is a real test and the retry runs exactly when every live-source
// message has been visited. This site used to clear the bit the scan tests,
// which made both the test and the retry arm dead.
//
// The glide reads the unit's map-pixel X/Z words directly and subtracts the
// half viewport without the height shear [07 R-CAM-01 §12].
func (b *battleSession) glideToMessageSource() {
	ring := b.messageRing()
	if ring == nil || b.cam == nil {
		return
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	alive := func(h pool.Handle) bool {
		v, found := snapshotUnitByHandle(f, h)
		return found && v.Slot != 0
	}
	ring.ClearJumped()
	src, found := ring.NextUnvisitedSource(alive)
	if !found {
		ring.ClearVisited()
		src, found = ring.NextUnvisitedSource(alive)
	}
	if !found {
		return
	}
	v, ok := snapshotUnitByHandle(f, src)
	if !ok {
		return
	}
	ox, oz := b.cam.BattleViewCenterOrigin(radarMapPixel(v.X), radarMapPixel(v.Z))
	b.cam.GlideTo(ox, oz)
}

// stepFollowCamera is the follow half of phase 10 [01 §4.4][07 R-CAM-01 §12]:
// a live tracked object recomputes the desired origin every pass and the
// current origin closes the gap; a tracked object that has died clears the
// follow triple; with no tracked object a glide left by `n` or F3 continues.
func (b *battleSession) stepFollowCamera() {
	if b == nil || b.cam == nil {
		return
	}
	tracked := b.cam.Tracked()
	if tracked == 0 {
		b.cam.StepGlide()
		return
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	v, found := snapshotUnitByHandle(f, tracked)
	if !found || v.Slot == 0 {
		b.cam.ClearFollow()
		return
	}
	b.cam.FollowTo(camera.TargetPoint{
		X: radarMapPixel(v.X),
		Y: radarMapPixel(v.Y),
		Z: radarMapPixel(v.Z),
	})
	b.cam.Clamp()
}
