package hud

import (
	"slices"
	"sync/atomic"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// LocalInterface is the client's local interface state (DESIGN_MULTIPLAYER
// §7.3, §16.2 M2-C6): the selection, the two visited bits, each builder's
// build page, BigBrother with the held Shift that pauses it, and the online
// presentation preferences that are not commands (the local logo override and
// NoShake). None of it is simulation state and no phase reads it; it never
// enters a fingerprint, a digest or the command stream.
//
// Retail keeps the selection bit, the visited bits and the page field in each
// unit's status word [07 §9][07 R-CAM-01 §12]. Here every entry is keyed by
// allocation reference, so a unit created in a reused slot inherits neither a
// selection nor a build page (§7.2, M2-C6).
//
// The state advances in two ways. Interface input applies at once, in input
// order, so a selection made earlier in an input batch is the selection a
// later order in the same batch resolves its units from. Ticks apply through
// Advance, one frame.InterfaceFacts per completed tick in tick order, which
// replays the two in-tick rules retail runs on the status word: the unit
// sweep's readiness clear [04 R-MOV-03 §1 step 7] and BigBrother's sweep-tail
// cycle [07 R-CAM-01 §12].
//
// Design reading (equivalence with the session-held selection it replaces):
// retail and the former session applied interface input at phase 1 of the
// next tick and the in-tick rules after it. A host consumes every completed
// tick's facts before it handles the next input batch — the synchronous host
// observes each publication inside its step, and the asynchronous one joins
// and drains the batch before input — so every tick a local input precedes is
// a tick the former session would also have applied that input before. The
// state therefore takes the same values, tick for tick; it only becomes
// visible a tick sooner, at the input rather than at the next publication.
//
// Design reading (retail save/load): the status word a retail save writes is
// retail's, selected bit, visited bits and page field included — the packed
// word carries flags bits 0..11 and 14..25 [08 R-SAVE-02 §6] — and this state
// is seeded from it. At save time the host hands its selection and the page
// fields local input set to the save path (session.RetailLocalInterface),
// which writes them into the detached status words where the session used to
// carry them; the live words are never written. A load builds a new session
// with a new allocation-reference namespace (§7.4.4, M2-C1), so the host
// starts a fresh LocalInterface and seeds it once from the restored words
// (AdoptStatusWords): the selection by the new references, and each
// builder's page by PageFlags's fallback to the restored page field. A
// battle with no host writes and restores each word as the session holds it.
//
// TODO(question): the visited set is not written or seeded. The `n` cycle and
// the select click name two visited bits (0x40 and 0x80) without saying which
// one a visit sets or which one the cycle tests [07 R-CAM-01 §2][07 R-CAM-01 §14], so
// mapping this one-set state onto them would be invented; a trace of the `n`
// handler's bit writes and tests would settle it. A restored word keeps
// whatever visited bits the file carried. None of this state is in any digest.
type LocalInterface struct {
	selected []pool.UnitRef // ascending by handle, then serial
	visited  []pool.UnitRef // ascending, as selected
	pages    []localPage    // ascending by reference
	bb       localBigBrother
	pending  InterfaceEvents
	assigns  []pendingGroupAssign
	logos    [10]logoOverride
	noShake  bool
	panel    panelWords
	epoch    uint64
}

// InterfaceEvents are BigBrother's three per-tick notices to the camera and
// the unit-info window, raised by local state rather than published
// [04 R-MOV-03 §1][07 R-CAM-01 §12].
type InterfaceEvents struct {
	Cycle, ResetVisited, CancelFollow bool
}

// Merge folds o's notices into e: notices of several ticks consumed together
// are delivered once.
func (e *InterfaceEvents) Merge(o InterfaceEvents) {
	e.Cycle = e.Cycle || o.Cycle
	e.ResetVisited = e.ResetVisited || o.ResetVisited
	e.CancelFollow = e.CancelFollow || o.CancelFollow
}

// localBigBrother is the selector's retained state. Fresh zero state is
// equivalent to retail's disabled retained counter: enabling always
// overwrites the counter before any read [07 R-CAM-01 §12].
type localBigBrother struct {
	enabled   bool
	countdown int16
	shiftHeld bool
}

// localPage is one builder's page field as local input last set it, in the
// status word's own layout (bit 22 the page-shown indicator, bits 23..25 the
// page number) [07 §9].
type localPage struct {
	ref   pool.UnitRef
	flags uint32
}

// pendingGroupAssign is a group assignment this client sent and has not yet
// seen applied: a local recall overlays it on the published group numbers so
// a recall that follows an assignment in the same batch sees the membership
// the assignment produces, as phase 1 applied both in order.
type pendingGroupAssign struct {
	group   uint8
	members []pool.UnitRef
	due     uint32
}

type logoOverride struct {
	set  bool
	logo uint8
}

// PageFieldMask is the status word's build-page field: the page-shown bit and
// the three page-number bits [07 §9].
const PageFieldMask = PagePagedBit | PageBitsMask

// bigBrotherPeriod is the cycle's period in ticks after a cycle fires
// [07 R-CAM-01 §12].
const bigBrotherPeriod = 90

// localEpochs numbers every change of every LocalInterface, so an epoch names
// one composed state across battles and a frame composed by one instance is
// never mistaken for another's.
var localEpochs atomic.Uint64

// NewLocalInterface returns the empty local state of a newly entered battle.
func NewLocalInterface() *LocalInterface {
	return &LocalInterface{epoch: localEpochs.Add(1)}
}

// Epoch changes whenever something composed onto a frame changes: the
// selection, a page or a logo override.
func (l *LocalInterface) Epoch() uint64 {
	if l == nil {
		return 0
	}
	return l.epoch
}

func (l *LocalInterface) touch() { l.epoch = localEpochs.Add(1) }

func refLess(a, b pool.UnitRef) int {
	if a.Handle != b.Handle {
		return int(a.Handle) - int(b.Handle)
	}
	switch {
	case a.Serial < b.Serial:
		return -1
	case a.Serial > b.Serial:
		return 1
	}
	return 0
}

func refIndex(set []pool.UnitRef, r pool.UnitRef) (int, bool) {
	return slices.BinarySearchFunc(set, r, refLess)
}

// Selected reports whether r is selected.
func (l *LocalInterface) Selected(r pool.UnitRef) bool {
	if l == nil {
		return false
	}
	_, found := refIndex(l.selected, r)
	return found
}

// SelectedRefs returns a copy of the selection in ascending handle order.
func (l *LocalInterface) SelectedRefs() []pool.UnitRef {
	if l == nil {
		return nil
	}
	return slices.Clone(l.selected)
}

// SelectionCount is the number of selected references, live or not.
func (l *LocalInterface) SelectionCount() int {
	if l == nil {
		return 0
	}
	return len(l.selected)
}

// ReplaceSelection is the replace gesture: the selection becomes exactly refs
// [07 §9]. The caller passes the local player's units only, as the former
// session admitted.
func (l *LocalInterface) ReplaceSelection(refs []pool.UnitRef) {
	if l == nil {
		return
	}
	next := slices.Clone(refs)
	slices.SortFunc(next, refLess)
	next = slices.Compact(next)
	if slices.Equal(next, l.selected) {
		return
	}
	l.selected = next
	l.touch()
}

// ToggleSelection flips each reference's membership in order, so a reference
// listed twice ends where it began, as the bit toggle did [07 §9].
func (l *LocalInterface) ToggleSelection(refs []pool.UnitRef) {
	if l == nil || len(refs) == 0 {
		return
	}
	for _, r := range refs {
		if i, found := refIndex(l.selected, r); found {
			l.selected = slices.Delete(l.selected, i, i+1)
		} else {
			l.selected = slices.Insert(l.selected, i, r)
		}
	}
	l.touch()
}

// ClearSelection deselects everything.
func (l *LocalInterface) ClearSelection() {
	if l == nil || len(l.selected) == 0 {
		return
	}
	l.selected = l.selected[:0]
	l.touch()
}

func (l *LocalInterface) deselect(r pool.UnitRef) bool {
	i, found := refIndex(l.selected, r)
	if found {
		l.selected = slices.Delete(l.selected, i, i+1)
	}
	return found
}

// Visited reports whether the `n` unit cycle has visited r since the last
// reset [07 R-CAM-01 §2].
func (l *LocalInterface) Visited(r pool.UnitRef) bool {
	if l == nil {
		return false
	}
	_, found := refIndex(l.visited, r)
	return found
}

// MarkVisited records that the `n` cycle visited r.
func (l *LocalInterface) MarkVisited(r pool.UnitRef) {
	if l == nil {
		return
	}
	if i, found := refIndex(l.visited, r); !found {
		l.visited = slices.Insert(l.visited, i, r)
	}
}

// ClearVisited forgets every visit.
func (l *LocalInterface) ClearVisited() {
	if l != nil {
		l.visited = l.visited[:0]
	}
}

// PageFlags is r's page field: the value local input last set, or else the
// field the committed status word carries, which unit creation seeds — page 1
// shown for a definition with two or more page windows — and a load restores
// [07 R-HUD-04 §4 "First build page"]. published is the unit's committed
// status word; only its page field is read.
func (l *LocalInterface) PageFlags(r pool.UnitRef, published uint32) uint32 {
	if l != nil {
		if i, found := slices.BinarySearchFunc(l.pages, r, func(p localPage, r pool.UnitRef) int { return refLess(p.ref, r) }); found {
			return l.pages[i].flags
		}
	}
	return published & PageFieldMask
}

// PageField is one builder's page field as local input last set it, in the
// status word's own layout.
type PageField struct {
	Ref   pool.UnitRef
	Flags uint32
}

// PageFields returns the page fields local input has set, ascending by
// reference: what a save writes into those builders' status words. Every
// other builder's field is the one its status word already carries.
func (l *LocalInterface) PageFields() []PageField {
	if l == nil {
		return nil
	}
	out := make([]PageField, len(l.pages))
	for i, p := range l.pages {
		out[i] = PageField{Ref: p.ref, Flags: p.flags}
	}
	return out
}

// HasPage reports whether local input has set r's page.
func (l *LocalInterface) HasPage(r pool.UnitRef) bool {
	if l == nil {
		return false
	}
	_, found := slices.BinarySearchFunc(l.pages, r, func(p localPage, r pool.UnitRef) int { return refLess(p.ref, r) })
	return found
}

// SetBuildPage moves builder's page with the retail identity, page-count
// guard and clamp (SetBuildPage) and reports whether the page field changed
// [07 §9][07 R-HUD-03 §6]. The caller has checked that builder is the single
// selected unit, whose command page this is. published is its committed
// status word; defID its catalog definition id; pageCount its definition's
// page-count byte.
func (l *LocalInterface) SetBuildPage(builder pool.UnitRef, published uint32, defID uint16, page, pageCount int) bool {
	if l == nil {
		return false
	}
	view := SelectUnit{Flags: l.PageFlags(builder, published), DefID: defID}
	if !SetBuildPage(&view, page, pageCount, nil) {
		return false
	}
	flags := view.Flags & PageFieldMask
	i, found := slices.BinarySearchFunc(l.pages, builder, func(p localPage, r pool.UnitRef) int { return refLess(p.ref, r) })
	if found {
		l.pages[i].flags = flags
	} else {
		l.pages = slices.Insert(l.pages, i, localPage{ref: builder, flags: flags})
	}
	// Page navigation raises the panel's refresh-needed flag [07 R-HUD-03 §13].
	l.markPanelStale()
	l.touch()
	return true
}

// GroupUnit is one of the local player's live units as digit recall scans it,
// in ascending pool order [07 §9]: its committed status word (for the CTRL_F
// key flag), its published group number and its catalog definition id.
type GroupUnit struct {
	Ref   pool.UnitRef
	Flags uint32
	Group uint8
	DefID uint16
}

// NoteGroupAssign records a group assignment this client has sent, due at
// tick due, until the host observes that tick (ObserveTick).
func (l *LocalInterface) NoteGroupAssign(group int, members []pool.UnitRef, due uint32) {
	if l == nil || group < 1 || group > 9 {
		return
	}
	l.assigns = append(l.assigns, pendingGroupAssign{group: uint8(group), members: slices.Clone(members), due: due})
}

// ObserveTick drops the pending assignments a publication of tick has
// applied; from then on its group numbers carry them.
func (l *LocalInterface) ObserveTick(tick uint32) {
	if l == nil {
		return
	}
	l.assigns = slices.DeleteFunc(l.assigns, func(a pendingGroupAssign) bool { return a.due <= tick })
}

// RecallGroup is digit recall: the scanner of [07 §9] over the local player's
// units, Preserve the Shift-held toggle/preserve argument and mask the
// authored CTRL_F filter (empty for no filter). It changes only the local
// selection; group numbers stay the seat's (DESIGN_MULTIPLAYER §7.1). Pending
// assignments are applied to the published numbers first, in the order they
// were sent, with the assignment scanner's rule.
func (l *LocalInterface) RecallGroup(units []GroupUnit, group int, preserve bool, mask content.CategoryMask) {
	if l == nil || group < 1 || group > 9 {
		return
	}
	views := make([]*SelectUnit, len(units))
	for i, u := range units {
		flags := u.Flags &^ SelectionFlag
		if l.Selected(u.Ref) {
			flags |= SelectionFlag
		}
		views[i] = &SelectUnit{Flags: flags, Group: u.Group, DefID: u.DefID}
	}
	for _, a := range l.assigns {
		// AssignGroup's scanner rule over the assignment's own membership:
		// members take the group, every other scanned unit carrying it loses it.
		for i, u := range units {
			v := views[i]
			if v.DefID == 0 {
				continue
			}
			if slices.Contains(a.members, u.Ref) {
				v.Group = a.group
			} else if v.Group == a.group {
				v.Group = 0
			}
		}
	}
	filter := &mask
	if mask.IsZero() {
		// No CTRL_F mask producer is part of the current frame. Absent filter
		// state is no filter; the authored mask producer remains an explicit
		// TODO rather than a guessed category mask [07 §9].
		filter = nil
	}
	changed, _ := RecallGroup(views, group, preserve, filter, nil)
	if !changed {
		return
	}
	for i, u := range units {
		if views[i].Flags&SelectionFlag != 0 {
			if j, found := refIndex(l.selected, u.Ref); !found {
				l.selected = slices.Insert(l.selected, j, u.Ref)
			}
		} else {
			l.deselect(u.Ref)
		}
	}
	l.touch()
}

// ToggleBigBrother is `+BigBrother`: enabling arms the first cycle for the
// next tick; disabling keeps the counter and asks the camera to drop its
// follow [07 R-CAM-01 §12].
func (l *LocalInterface) ToggleBigBrother() {
	if l == nil {
		return
	}
	l.bb.enabled = !l.bb.enabled
	if l.bb.enabled {
		l.bb.countdown = 1
	} else {
		l.pending.CancelFollow = true
	}
}

// SetShiftHeld records the held Shift that pauses BigBrother's countdown
// [07 R-CAM-01 §12].
func (l *LocalInterface) SetShiftHeld(held bool) {
	if l != nil {
		l.bb.shiftHeld = held
	}
}

// TakeEvents returns and clears the notices input raised since the last
// Advance. A host delivers them with the next tick, as the former session
// did; at the paused-input boundary, where no tick runs, it takes them here.
func (l *LocalInterface) TakeEvents() InterfaceEvents {
	if l == nil {
		return InterfaceEvents{}
	}
	ev := l.pending
	l.pending = InterfaceEvents{}
	return ev
}

// Advance applies one completed tick's facts and returns the tick's notices,
// including any input raised since the last one. Facts must be applied once
// each, in tick order (frame.Buffer.DrainInterfaceFacts). Invalid facts — a
// republication's — advance nothing.
func (l *LocalInterface) Advance(f frame.InterfaceFacts) InterfaceEvents {
	if l == nil {
		return InterfaceEvents{}
	}
	ev := l.TakeEvents()
	if !f.Valid {
		return ev
	}
	l.ObserveTick(f.Tick)
	// Phase 2 step 7: a selected unit that is not ready at its own visit
	// leaves the selection, whatever it is by the end of the tick
	// [04 R-MOV-03 §1].
	changed := false
	for _, r := range f.Unready {
		if l.deselect(r) {
			changed = true
		}
	}
	// The sweep tail: BigBrother's countdown and cycle, after the clear
	// [04 R-MOV-03 §1][07 R-CAM-01 §12].
	b := &l.bb
	if b.enabled && !b.shiftHeld {
		b.countdown--
		if b.countdown < 1 {
			b.countdown = bigBrotherPeriod
			ev.Cycle = true
			if l.cycle(f.Ready, &ev) {
				changed = true
			}
		}
	}
	if changed {
		l.touch()
	}
	return ev
}

// cycle is the selector's walk over the sweep tail's ready units in slice
// order: the first ready unit, the first selected ready unit and the ready
// unit after it. Finding a selected ready unit clears every selection and
// both visited bits — retail clears them on every record, whoever owns it —
// and the unit after it, or the first ready unit when none follows, becomes
// selected [07 R-CAM-01 §12].
func (l *LocalInterface) cycle(ready []pool.UnitRef, ev *InterfaceEvents) bool {
	var first, next pool.UnitRef
	haveFirst, haveNext, selectedSeen, changed := false, false, false, false
	for _, r := range ready {
		if !haveFirst {
			first, haveFirst = r, true
		}
		if selectedSeen {
			next, haveNext = r, true
			break
		}
		if !l.Selected(r) {
			continue
		}
		selectedSeen = true
		if len(l.selected) != 0 {
			l.selected = l.selected[:0]
			changed = true
		}
		l.visited = l.visited[:0]
		// The consumer also closes the command panel; page fields are not
		// changed by this request.
		ev.ResetVisited = true
	}
	if !haveNext && haveFirst {
		next, haveNext = first, true
	}
	if haveNext {
		if i, found := refIndex(l.selected, next); !found {
			l.selected = slices.Insert(l.selected, i, next)
			changed = true
		}
	}
	return changed
}

// Prune forgets every reference live does not admit — units that died or
// whose slot now holds another allocation — so the state never grows with
// the battle's dead.
func (l *LocalInterface) Prune(live func(pool.UnitRef) bool) {
	if l == nil || live == nil {
		return
	}
	n := len(l.selected)
	l.selected = slices.DeleteFunc(l.selected, func(r pool.UnitRef) bool { return !live(r) })
	if len(l.selected) != n {
		l.touch()
	}
	l.visited = slices.DeleteFunc(l.visited, func(r pool.UnitRef) bool { return !live(r) })
	l.pages = slices.DeleteFunc(l.pages, func(p localPage) bool { return !live(p.ref) })
}

// AdoptStatusWords seeds the selection, once, from the status words of a
// battle's first publication: every unit whose word carries the selected bit
// becomes selected under its allocation reference. Only a retail save's
// restore writes that bit, so for a loaded battle this is the selection the
// file saved, and for any other battle it adopts nothing [08 R-SAVE-02 §6].
// The page field needs no seeding: PageFlags reads it from the word. f must
// be uncomposed — a composed frame's bits are some local state's, not the
// session's — and AdoptStatusWords reports whether it was.
func (l *LocalInterface) AdoptStatusWords(f *frame.Frame) bool {
	if l == nil || f == nil || f.Selection.Composed != 0 {
		return false
	}
	var refs []pool.UnitRef
	for i := range f.Units {
		v := &f.Units[i]
		if v.Slot != 0 && v.Flags&SelectionFlag != 0 {
			refs = append(refs, pool.UnitRef{Handle: v.Slot, Serial: v.AllocationSerial})
		}
	}
	if len(refs) != 0 {
		l.selected = append(l.selected[:0], refs...)
		slices.SortFunc(l.selected, refLess)
		l.touch()
	}
	return true
}

// ResyncAfterGap recovers from lost ticks: the facts of some ticks never
// reached this state (frame.Buffer.InterfaceFactsDropped). What they would
// have told is unknown — which selected units the sweep found unready at a
// visit, and whether BigBrother cycled in them, with the selection change,
// the visited reset and the camera notices a cycle raises, and so where its
// countdown stands. The selection is resynchronised from the newest committed
// frame at or after the gap, the one evidence left: a selected unit ready
// there (ready reports it, by the published form of the readiness predicate)
// stays selected and every other leaves. A unit unready only inside the gap
// therefore stays selected, and the cycles the gap held are skipped rather
// than replayed: BigBrother's countdown resumes where the last applied tick
// left it. Visited units and pages are not tick state and are kept. It is a
// Nanolathe recovery with no retail counterpart; a host that drains every
// step never needs it.
func (l *LocalInterface) ResyncAfterGap(ready func(pool.UnitRef) bool) {
	if l == nil || ready == nil {
		return
	}
	n := len(l.selected)
	l.selected = slices.DeleteFunc(l.selected, func(r pool.UnitRef) bool { return !ready(r) })
	if len(l.selected) != n {
		l.touch()
	}
}

// SetLogoOverride is online `+Logo n p`: a presentation override on this
// client only. The player record keeps its configured logo (§7.1).
func (l *LocalInterface) SetLogoOverride(player int, logo uint8) {
	if l == nil || player < 0 || player >= len(l.logos) {
		return
	}
	l.logos[player] = logoOverride{set: true, logo: logo}
	l.touch()
}

// LogoOverride returns player's local logo override, if one is set.
func (l *LocalInterface) LogoOverride(player int) (uint8, bool) {
	if l == nil || player < 0 || player >= len(l.logos) || !l.logos[player].set {
		return 0, false
	}
	return l.logos[player].logo, true
}

// ToggleNoShake is online `+NoShake`: the shake driver keeps running in the
// simulation, with its draws, and this client declines to apply the
// published offset (§7.1). Single-player keeps the authoritative toggle.
func (l *LocalInterface) ToggleNoShake() {
	if l != nil {
		l.noShake = !l.noShake
	}
}

// NoShake reports the online local shake preference.
func (l *LocalInterface) NoShake() bool { return l != nil && l.noShake }
