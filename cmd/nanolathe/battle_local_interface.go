package main

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// The battle host's local interface state (DESIGN_MULTIPLAYER §7.3, §16.2
// M2-C6).
//
// Selection, the visited bits, build pages, BigBrother and the held Shift are
// the client's, in one hud.LocalInterface keyed by allocation reference. The
// host is its only writer:
//
//   - interface input applies to it at once, in input order, through
//     submitHumanCommand, which is also where every order's units are
//     resolved from it: an order, Stop, self-destruct, stance or cloak press
//     and a group assignment carry the selection as explicit handles when they
//     are sent, so a selection made earlier in an input batch is the one a
//     later command in the batch acts on;
//   - every completed tick advances it from that tick's facts, which the frame
//     buffer retains independently of its slot rotation, so a catch-up batch
//     whose newest publication alone is presented still advances it across
//     every tick (advanceLocalInterface, called as each publication is
//     observed);
//   - the presentation-only sections of the committed frames it presents are
//     composed from it (composeLocalInterface), so every reader of the
//     selection, the selected bit, the page field and the command page reads
//     the local state.
//
// Across a retail save/load the selection and the pages travel in the status
// words, as retail saved them: a save writes this state into the saved words
// (retailLocalInterface), and the loaded battle — a new session with a new
// reference namespace, entered through a new battleSession — seeds a fresh
// LocalInterface from the restored words (attachLocalInterface). Builders
// show the page field the loaded status words carry.
//
// The facts reach the host through the frame buffer's bounded queue
// (frame.Buffer.DrainInterfaceFacts). This host drains it at every step, so it
// holds one catch-up batch; should it ever overflow, the ticks it refused are
// lost to the local state, and advanceLocalInterface resynchronises from the
// first frame it observes at or after the last of them
// (hud.LocalInterface.ResyncAfterGap).

// attachLocalInterface gives a battle a fresh local interface state, seeded
// once from the status words of the battle's committed frame — the selection
// a loaded battle's file saved — and asks its session for the tick facts that
// advance it.
func (b *battleSession) attachLocalInterface() {
	if b == nil {
		return
	}
	b.local = hud.NewLocalInterface()
	b.localFacts = b.localFacts[:0]
	b.localEvents = hud.InterfaceEvents{}
	b.localGap = false
	b.localDropped = 0
	if b.sess != nil && b.sess.Snapshot != nil {
		b.localDropped, _ = b.sess.Snapshot.InterfaceFactsDropped()
		b.local.AdoptStatusWords(b.sess.Snapshot.Current())
	}
	if b.sess != nil {
		b.sess.SetLocalInterfaceFacts(true)
	}
}

// retailLocalInterface is this client's selection and the page fields local
// input set, by allocation reference, for a retail save to write into the
// status words (session.RetailLocalInterface). The retained facts are applied
// first, so the save writes the state of the tick it saves.
func (b *battleSession) retailLocalInterface() *session.RetailLocalInterface {
	if b == nil || b.local == nil {
		return nil
	}
	b.syncLocalInterface()
	out := &session.RetailLocalInterface{Selected: b.local.SelectedRefs()}
	for _, p := range b.local.PageFields() {
		out.Pages = append(out.Pages, session.RetailPageField{Ref: p.Ref, Flags: p.Flags})
	}
	return out
}

// localState returns the battle's local interface state, attaching one to a
// battle composed without the production entry path (tests, previews).
func (b *battleSession) localState() *hud.LocalInterface {
	if b == nil {
		return nil
	}
	if b.local == nil {
		b.attachLocalInterface()
	}
	return b.local
}

// advanceLocalInterface applies, in tick order, every retained tick's facts up
// to and including tick, and returns the notices they raised merged with any
// stashed earlier. Facts of later ticks wait for their own publication. resync
// is the readiness of the observed frame at tick, used when facts were lost.
func (b *battleSession) advanceLocalInterface(tick uint32, resync func(pool.UnitRef) bool) hud.InterfaceEvents {
	if b == nil {
		return hud.InterfaceEvents{}
	}
	ev := b.localEvents
	b.localEvents = hud.InterfaceEvents{}
	if b.local == nil {
		return ev
	}
	if b.sess != nil && b.sess.Snapshot != nil {
		b.localDrain = b.sess.Snapshot.DrainInterfaceFacts(b.localDrain)
		b.localFacts = append(b.localFacts, b.localDrain...)
		// A changed dropped count means the queue refused the facts of every
		// tick after the ones this drain returned, up to newest: a gap the
		// local state must resynchronise over once it has applied the facts
		// before it (frame.retainedInterfaceFactsCapacity).
		if dropped, newest := b.sess.Snapshot.InterfaceFactsDropped(); dropped != b.localDropped {
			b.localDropped = dropped
			b.localGap, b.localGapTick = true, newest
		}
	}
	n := 0
	for _, f := range b.localFacts {
		if f.Tick > tick {
			break
		}
		if b.localGap && f.Tick > b.localGapTick && resync != nil {
			// Facts after the gap: resynchronise before applying them.
			b.local.ResyncAfterGap(resync)
			b.localGap = false
		}
		ev.Merge(b.local.Advance(f))
		n++
	}
	clear(b.localFacts[:n])
	b.localFacts = append(b.localFacts[:0], b.localFacts[n:]...)
	if b.localGap && tick >= b.localGapTick && resync != nil {
		// Every fact before the gap is applied and the observed frame is at
		// or after its last tick: the newest evidence there is.
		b.local.ResyncAfterGap(resync)
		b.localGap = false
	}
	// Notices input raised since the last tick (a BigBrother disable) ride
	// with this publication, as the former session's did.
	ev.Merge(b.local.TakeEvents())
	return ev
}

// observeLocalInterface is the per-publication step: it advances the local
// state through cur's tick, forgets references cur shows dead or reused, and
// recomposes cur. A frame is observed after every earlier one, so a reference
// absent from it is gone for good.
func (b *battleSession) observeLocalInterface(cur *frame.Frame) hud.InterfaceEvents {
	if b == nil || cur == nil {
		return hud.InterfaceEvents{}
	}
	ev := b.advanceLocalInterface(cur.Tick, b.frameReadyRef(cur))
	if b.local != nil {
		b.local.ObserveTick(cur.Tick)
		b.local.Prune(frameLiveRef(cur))
		b.composeLocalInterface(cur)
	}
	return ev
}

// frameLiveRef admits the references f publishes as live units.
func frameLiveRef(f *frame.Frame) func(pool.UnitRef) bool {
	return func(r pool.UnitRef) bool {
		v, ok := snapshotUnitByHandle(f, r.Handle)
		return ok && v.AllocationSerial == r.Serial
	}
}

// frameReadyRef admits the references f publishes as live units that are
// ready there, by the published form of the readiness predicate
// (selectionReadyView): the evidence a resynchronisation after lost facts
// keeps a selected unit on.
func (b *battleSession) frameReadyRef(f *frame.Frame) func(pool.UnitRef) bool {
	return func(r pool.UnitRef) bool {
		v, ok := snapshotUnitByHandle(f, r.Handle)
		return ok && v.AllocationSerial == r.Serial && b.selectionReadyView(f, v)
	}
}

// composeLocalInterface writes the local state's presentation-only sections
// onto f: the selection, the selected and page bits, the unit contacts'
// selection words, logo overrides and the command page. A frame already
// composed at the current epoch is left alone. It runs on the host's
// goroutine for a frame no writer can reach: the committed frame while the
// simulation is quiescent, or a pinned one [I6].
func (b *battleSession) composeLocalInterface(f *frame.Frame) {
	if b == nil || b.local == nil || f == nil {
		return
	}
	if !b.local.ComposeSelection(f) {
		return
	}
	var products func(string) []string
	if b.sess != nil {
		products = b.sess.CommandPageProducts
	}
	hud.ComposeCommandPage(&f.CommandPage, f, b.cat, products)
}

// finishLocalInterfaceStep runs at the end of each host step, while the
// simulation is quiescent. It composes every publication the buffer still
// holds, so whichever joined tick the next presentation pins already carries
// the current local state, and names the developer diagnostics' movement
// subject — the first selected unit — for the ticks this step released.
func (b *battleSession) finishLocalInterfaceStep() {
	if b == nil || b.local == nil || b.sess == nil || b.sess.Snapshot == nil {
		return
	}
	b.sess.Snapshot.PublicationsSince(0, b.composeLocalInterface)
	var subject pool.Handle
	if selected := b.selectedHandlesInSlotOrder(); len(selected) != 0 {
		subject = selected[0]
	}
	b.sess.SetDeveloperMovementSubject(subject)
}

// localRef names the local player's unit in slot h of f, or reports none.
func (b *battleSession) localRef(f *frame.Frame, h pool.Handle) (pool.UnitRef, frame.UnitView, bool) {
	v, ok := snapshotUnitByHandle(f, h)
	if !ok || v.Slot == 0 || b.sess == nil || v.Owner != b.sess.LocalOwner {
		return pool.UnitRef{}, frame.UnitView{}, false
	}
	return pool.UnitRef{Handle: v.Slot, Serial: v.AllocationSerial}, v, true
}

// localRefs resolves handles, in order and keeping repeats, to the local
// player's units in the current frame, as the former session admitted a
// selection command's handles (alive and owned by the local player).
func (b *battleSession) localRefs(handles []pool.Handle) []pool.UnitRef {
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	out := make([]pool.UnitRef, 0, len(handles))
	for _, h := range handles {
		if r, _, ok := b.localRef(f, h); ok {
			out = append(out, r)
		}
	}
	return out
}

// submitHumanCommand is the host's one path for a typed interface command.
// Local interface kinds apply to the local state; every other kind is sent to
// the session with its selection-derived units resolved from the local
// selection now, at submission (DESIGN_MULTIPLAYER §7.3). The sequence is the
// session's command receipt; local kinds have none.
func (b *battleSession) submitHumanCommand(c session.HumanCommand) (uint64, error) {
	if b == nil || b.sess == nil {
		return 0, fmt.Errorf("nanolathe: battle command not enqueued: no session")
	}
	if session.LocalInterfaceKind(c.Kind) {
		b.applyLocalInterfaceCommand(c)
		return 0, nil
	}
	if b.playback != nil && c.Kind != session.HumanNoShake && c.Kind != session.HumanSetLogo {
		// A playback plays the recorded commands and takes none of its own.
		return 0, errReplayPlaybackCommand
	}
	local := b.localState()
	online := b.onlineBattle()
	switch c.Kind {
	case session.HumanOrder:
		if len(c.Order.Handles) == 0 {
			c.Order.Handles = b.selectedHandlesInSlotOrder()
		}
	case session.HumanStop:
		if len(c.Stop.Handles) == 0 {
			c.Stop.Handles = b.selectedHandlesInSlotOrder()
		}
	case session.HumanSelfDestruct:
		if len(c.SelfDestruct.Handles) == 0 {
			c.SelfDestruct.Handles = b.selectedHandlesInSlotOrder()
		}
	case session.HumanStance:
		if len(c.Stance.Handles) == 0 {
			c.Stance.Handles = b.selectedHandlesInSlotOrder()
		}
	case session.HumanCloak:
		if len(c.Cloak.Handles) == 0 {
			c.Cloak.Handles = b.selectedHandlesInSlotOrder()
		}
	case session.HumanGroupAssign:
		// Assignment is a seat command carrying the group's complete new
		// membership: the selection when Ctrl+digit is pressed.
		c.Group.Handles = b.selectedHandlesInSlotOrder()
	case session.HumanNoShake:
		if online {
			// Online the shake driver always runs; this client only declines
			// to apply the published offset (§7.1).
			local.ToggleNoShake()
			return 0, nil
		}
	case session.HumanSetLogo:
		if online {
			// Online `+Logo` is a presentation override on this client; the
			// player record keeps its configured logo (§7.1).
			local.SetLogoOverride(c.SetLogo.Player, c.SetLogo.Logo)
			b.composeCurrentFrame()
			return 0, nil
		}
	}
	if online {
		seq, err := b.submitLocalMultiplayer(c)
		if err != nil {
			b.onlineNotice(err.Error())
		}
		return seq, err
	}
	due := uint32(1)
	if b.sess.Clock != nil {
		due = b.sess.Clock.GlobalTick + 1
	}
	seq, err := b.sess.EnqueueHumanCommandWithSequence(c)
	if err == nil && c.Kind == session.HumanGroupAssign {
		// A recall before this assignment is published sees the membership
		// it produces, as phase 1 applied both in order.
		local.NoteGroupAssign(c.Group.Group, b.localRefs(c.Group.Handles), due)
	}
	return seq, err
}

// applyLocalInterfaceCommand applies one local interface kind to the local
// state and recomposes the current frame, so a later command in the same
// input batch reads the selection, page and command page this one produced.
func (b *battleSession) applyLocalInterfaceCommand(c session.HumanCommand) {
	local := b.localState()
	switch c.Kind {
	case session.HumanSelectionReplace:
		local.ReplaceSelection(b.localRefs(c.Selection.Handles))
	case session.HumanSelectionToggle:
		local.ToggleSelection(b.localRefs(c.Selection.Handles))
	case session.HumanSelectionClear:
		local.ClearSelection()
	case session.HumanBuildPage:
		b.applyLocalBuildPage(c.BuildPage)
	case session.HumanGroupRecall:
		b.applyLocalGroupRecall(c.Group)
	case session.HumanBigBrother:
		local.ToggleBigBrother()
		if b.sess.Clock != nil && b.sess.Clock.Paused {
			// No tick will carry the notice; the former session delivered it
			// at the paused-input boundary's republication [07 R-CAM-01 §12].
			b.applyLocalInterfaceEvents(local.TakeEvents())
		}
	case session.HumanShiftState:
		local.SetShiftHeld(c.ShiftHeld)
	}
	b.composeCurrentFrame()
}

// composeCurrentFrame recomposes the committed frame host-step readers read.
func (b *battleSession) composeCurrentFrame() {
	if b == nil || b.sess == nil || b.sess.Snapshot == nil {
		return
	}
	b.composeLocalInterface(b.sess.Snapshot.Current())
}

// applyLocalBuildPage is a page key or gadget: the single selected builder's
// page moves under the retail identity and page-count guard [07 §9]. Which
// pages exist is the definition's page-count byte and nothing else
// [07 R-HUD-03 §6][02 R-CAT-01 §5 step 5]; a unit mixed with another selected
// one has aggregate command state, not a page [07 §9].
func (b *battleSession) applyLocalBuildPage(c session.HumanBuildPageCommand) {
	f, ok := b.currentSnapshot()
	if !ok || b.cat == nil {
		return
	}
	selected := b.selectedHandlesInSlotOrder()
	if len(selected) != 1 || selected[0] != c.Builder {
		return
	}
	ref, v, ok := b.localRef(f, c.Builder)
	if !ok {
		return
	}
	def, ok := b.cat.Unit(v.DefName)
	if !ok || def == nil {
		return
	}
	pageCount := hud.BuilderPageCount(def)
	if pageCount == 0 {
		return
	}
	defID, ok := b.cat.UnitDefIndex(def.CanonicalKey)
	if !ok || defID == 0 || defID > 0xffff {
		return
	}
	b.localState().SetBuildPage(ref, v.Flags, uint16(defID), c.Page, pageCount)
}

// applyLocalGroupRecall is digit recall over the local player's live units in
// the current frame, in ascending slot order, as the group scanner walks them
// [07 §9]. Only the local selection changes.
func (b *battleSession) applyLocalGroupRecall(c session.HumanGroupCommand) {
	f, ok := b.currentSnapshot()
	if !ok || b.cat == nil || b.sess == nil {
		return
	}
	units := make([]hud.GroupUnit, 0, len(f.Units))
	for i := range f.Units {
		v := &f.Units[i]
		if v.Slot == 0 || v.Owner != b.sess.LocalOwner {
			continue
		}
		defID, ok := b.cat.UnitDefIndex(v.DefName)
		if !ok || defID == 0 || defID > 0xffff {
			continue
		}
		units = append(units, hud.GroupUnit{Ref: pool.UnitRef{Handle: v.Slot, Serial: v.AllocationSerial}, Flags: v.Flags, Group: v.Group, DefID: uint16(defID)})
	}
	b.localState().RecallGroup(units, c.Group, c.Preserve, c.Mask)
}

// selectedHandlesInSlotOrder is the local selection's live units in the
// current frame, in slot order: the units an order sent now acts on.
func (b *battleSession) selectedHandlesInSlotOrder() []pool.Handle {
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	if b.local == nil {
		// A battle that never attached local state (a hand-built fixture)
		// keeps whatever selection its frame was built with.
		out := make([]pool.Handle, 0, len(f.Selection.Handles))
		for i := range f.Units {
			if containsHandle(f.Selection.Handles, f.Units[i].Slot) {
				out = append(out, f.Units[i].Slot)
			}
		}
		return out
	}
	out := make([]pool.Handle, 0, b.local.SelectionCount())
	for i := range f.Units {
		v := &f.Units[i]
		if b.sess != nil && v.Owner == b.sess.LocalOwner && b.local.Selected(pool.UnitRef{Handle: v.Slot, Serial: v.AllocationSerial}) {
			out = append(out, v.Slot)
		}
	}
	return out
}

// applyLocalInterfaceEvents gives BigBrother's notices to the camera and the
// unit-info window, in the order the former publication delivered them:
// cancel follow, reset the visited set, then cycle [04 R-MOV-03 §1]
// [07 R-CAM-01 §12].
func (b *battleSession) applyLocalInterfaceEvents(ev hud.InterfaceEvents) {
	if b == nil || b.cam == nil {
		return
	}
	if ev.CancelFollow {
		b.cam.ClearFollow()
		b.cam.LatchTracked()
	}
	if ev.ResetVisited {
		// The local state already forgot the visited units with the cycle.
		// TODO(question): map the force-zero page-close deferral bits and current
		// page owner to this shell before replacing its existing unit-info-only
		// close [07 R-HUD-04 §3].
		if closeUnitInfo() {
			b.developer.quickkeysDisabled = false
		}
	}
	if ev.Cycle {
		b.cycleFollowTargetFrom(false, b.cam.LatchedTracked())
		b.cam.LatchTracked()
	}
}

// syncLocalInterface applies every retained tick's facts that no publication
// observer consumed — a host or test that stepped the session itself — and
// keeps their notices for the next observation, then recomposes the current
// frame. In the window every publication is observed as it lands, so this
// finds nothing to do.
func (b *battleSession) syncLocalInterface() {
	if b == nil || b.local == nil || b.sess == nil || b.sess.Snapshot == nil {
		return
	}
	cur := b.sess.Snapshot.Current()
	if cur == nil {
		return
	}
	ev := b.advanceLocalInterface(cur.Tick, b.frameReadyRef(cur))
	b.local.ObserveTick(cur.Tick)
	b.localEvents.Merge(ev)
	b.local.Prune(frameLiveRef(cur))
	b.composeLocalInterface(cur)
}
