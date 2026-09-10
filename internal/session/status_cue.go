package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The engine status-cue sink [03 R-AUD-01 §7].
//
// The unit edge machine raises four codes — 3 `activate`, 4 `deactivate`,
// 14 `cloak`, 15 `uncloak` — and the codes ARE the slot indices of the static
// table of [03 §8.3]; there is no translation between the two numberings. The
// order handlers' voice sites reach the same sink through the presentation
// adapter's Status port in composition.go.
//
// The raise helper does exactly three things and, when its gate fails, returns
// having touched nothing — no queue write, no allocation, no draw. It draws
// from neither RNG stream: the only draw anywhere in the cue path is the CRT
// variant pick at resolve time, once per pop, on the presentation side
// [03 §8.3][I4].

// bindStatusCueSinks installs the raise seam on every live unit. It runs at
// RegisterAll and again from the creation hook, so a unit placed before the
// hooks were bound and a unit produced by a factory reach the same sink
// [03 R-AUD-01 §7].
func (s *Session) bindStatusCueSinks() {
	if s == nil || s.Units == nil {
		return
	}
	for _, u := range s.Units.Iter() { // pool slot ascending (I1)
		if u == nil {
			continue
		}
		u.SetStatusCueSink(s.raiseStatusCue)
	}
}

// raiseStatusCue is the raise helper of [03 R-AUD-01 §7], reached from the unit
// edge machine's four codes.
//
// It runs synchronously in the caller's own simulation phase — the economy
// settlement for 14/15 [05 R-ECO-01 §9], construction admission and the work
// handlers for 3/4. §7 permits a clone that keeps presentation behind the
// publication boundary to carry the raise as a committed-tick event of
// (code, unit) and perform the §8.3 Insert at that boundary, provided the
// gate's three per-unit facts are evaluated at the raise, each committed tick's
// events are applied exactly once in raise order, and a unit removed in the
// same tick drops its pending entries. That is what this does: the gate and the
// caption are resolved here, the event carries (code, unit, caption) in raise
// order, the client's drain performs the Insert against the same global tick
// counter, and purgeStatusCues below is the teardown purge [03 §2.4][I6].
func (s *Session) raiseStatusCue(u *units.Unit, code uint8) {
	if s == nil || u == nil {
		return
	}
	// Step 1, the gate. The unit's owner slot must equal the view slot, its
	// status word must carry the alive bit and must not carry the death latch
	// [03 R-AUD-01 §7][04 R-SPEC-01 §12]. Unit.Alive/Dying are this model's
	// live/death state. Units outside the viewing slot never enter the queue,
	// and a dying unit's edges are silent.
	if u.Handle != 0 && u.Owner == s.ViewingOwner && u.Alive && !u.Dying {
		// Step 2, the text. The edge machine passes no override for any of its
		// four codes, so the slot's static default caption is used: `Cloaked`
		// for 14, `Visible` for 15, and empty for 3 and 4, which therefore
		// print nothing [03 R-AUD-01 §7][03 §8.3]. There is no localisation
		// table loaded in this build, and §7 makes the lookup the identity in
		// that case.
		_, caption, _, _, ok := audio.SlotStatic(audio.Slot(code))
		if ok && s.publication != nil && s.publication.events != nil {
			tick := uint32(0)
			if s.Clock != nil {
				tick = s.Clock.GlobalTick
			}
			// Step 3, the insert, timed against the GLOBAL tick counter — the
			// cooldown, the duplicate-slot drop, the full-queue silent resolve
			// and the sorted insert all live in the queue the presentation edge
			// drains this event into [03 §8.3].
			s.publication.events.EmitStatus(frame.Event{
				Tick: tick, Source: u.Handle, StatusKind: code,
				StatusText: caption, StatusClass: 1,
			})
		}
	}
	// What else reacts, after the cue: the instance cloaked bit's RISING edge
	// raises pending bit `0x10000` on every order record observing the unit.
	// This one is simulation-visible and is NOT gated on the view slot — a
	// computer player's records observing a cloaking unit wake exactly the same
	// way [03 R-AUD-01 §7][04 R-ORD-01 §6]. The falling edge sends no notice.
	if code == units.StatusCueCloak {
		s.noticeTargetCloaked(u)
	}
	// The remaining reaction §7 lists after any edge is the interface-panel
	// dirty flag, set when the unit's owner is the LOCAL slot (not the view
	// slot). It is presentation only and this build keeps no session-side
	// battle-interface dirty word for it (hud.InterfaceDirtyBit has no session
	// consumer), so there is nothing here to set. The four-byte network event
	// for control bytes 1 and 2 is out of scope (no networking).
}

// noticeTargetCloaked delivers the rising-edge notice to each observing order,
// including references bound by a running handler [04 R-ORD-01 §6].
func (s *Session) noticeTargetCloaked(victim *units.Unit) {
	if s == nil || victim == nil {
		return
	}
	orders.TargetCloaked(s.Units, victim.Handle)
}

// purgeStatusCues is the unit-teardown purge [03 R-AUD-01 §7]: when a unit is
// removed, every queued entry whose unit is that unit is dropped before the
// record goes away. The queue has exactly one other simulation-side
// interaction, and this is it — no simulation phase reads a queued entry back.
//
// This build's queued entries for the tick under construction are the staged
// status events of the committed-frame window, so the purge clears their slot:
// slot 0 is §8.3's unused sentinel, and the presentation edge's insert refuses
// it, which is the drop. Entries published on an EARLIER tick have already left
// the session and sit in the presentation queue, which this package does not
// own; a unit removed after its cue was published therefore keeps its sound but
// still prints no caption, because the caption gate re-reads the unit's live
// state at resolve time [03 §8.3 Resolve step 4].
func (s *Session) purgeStatusCues(h pool.Handle) {
	if s == nil || h == 0 || s.publication == nil || s.publication.events == nil {
		return
	}
	staged := s.publication.events.StagingEvents()
	for i := range staged {
		if staged[i].Kind != frame.KindStatus || staged[i].Source != h {
			continue
		}
		staged[i].StatusKind = 0
		staged[i].StatusText = ""
	}
}
