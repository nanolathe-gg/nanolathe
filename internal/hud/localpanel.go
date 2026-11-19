package hud

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The battle panel's retained aggregate fields (DESIGN_INTERFACE_HUD_INPUT
// "Panel refresh and write-back").
//
// Retail keeps the four selection-aggregate fields the panel stages its
// MOVEORD, FIREORD, CLOAK and ONOFF gadgets from in interface words of its
// own, and recomputes them only when something raises the panel's
// refresh-needed flag [07 R-HUD-03 §13]. A press writes the value it just sent
// back into its field [04 R-STANCE-01 §2] step 3, and that value stays until
// the next refresh. For the stance and on/off fields the next refresh folds
// the same value once the order has applied. The cloak fold does not compare,
// so a selection of two or more cloak-capable units folds to 2 every time. It
// is the write-back, not a refresh, that lets a second press on such a
// selection cloak it.
//
// The flag's raisers modelled here are the ones the host can observe
// [07 R-HUD-03 §13]:
//
//   - a selection gesture that selects or toggles at least one unit, and a
//     group recall, whether or not the membership changes (MarkPanelStale);
//   - any other change of the composed selection;
//   - local page navigation (SetBuildPage);
//   - a change of a selected unit's committed activation;
//   - a selected unit finishing construction.
//
// A battle starts with the flag up. A loaded battle enters through a fresh
// LocalInterface, which is retail's load trigger. A replay playback raises it
// at every publication, because the recorded seat's own presses, whose
// write-backs this client never saw, change the fields it shows.
//
// TODO(question): retail's build-order handlers also raise the flag for a
// selected builder when construction starts, terminates or completes, and a
// completion raises it when the builder rather than the product is selected.
// Some handlers raise it on arms that keep the order running as well. The
// activation edge also raises it when the builder's StartBuilding/
// StopBuilding bit changes. Neither is published to the host, so a selected
// builder's build transitions leave the retained fields alone here.
// Publishing the activation byte's building bit, and classifying which
// handler arms run every tick, would settle it [07 R-HUD-03 §13].

// panelWords is the retained copy of the four fields and the state that
// decides when it is refreshed.
type panelWords struct {
	// fresh is false while the refresh-needed flag is up.
	fresh bool
	move  uint8
	fire  uint8
	cloak uint8
	onoff uint8
	// selection is the composed selection the last refresh folded.
	selection []pool.UnitRef
	// watch is each selected unit's trigger state at the last observed
	// publication, ascending by reference; spare is the buffer the next
	// observation fills.
	watch []panelWatch
	spare []panelWatch
	// scratch holds the composed selection while RetainPanel compares it.
	scratch []pool.UnitRef
}

// panelWatch is the part of one selected unit's committed state whose change
// raises the refresh-needed flag.
type panelWatch struct {
	ref       pool.UnitRef
	activated bool
	complete  bool
}

// markPanelStale raises the refresh-needed flag.
func (l *LocalInterface) markPanelStale() {
	l.panel.fresh = false
}

// MarkPanelStale raises the refresh-needed flag for a selection gesture, a
// replay publication or a takeover. Retail's click, drag-box and select-all
// producers raise it whenever the gesture selects or toggles a unit, and its
// group selections always do, whether or not the membership changes, so
// reselecting the same units refreshes the panel too [07 R-HUD-03 §13].
func (l *LocalInterface) MarkPanelStale() {
	if l == nil {
		return
	}
	l.markPanelStale()
	l.touch()
}

// ObservePanel compares each selected unit's committed activation and
// construction state in f with the previous observed publication, and raises
// the refresh-needed flag when any of them changed [07 R-HUD-03 §13]. The
// host calls it once per publication, in tick order, before composing f.
func (l *LocalInterface) ObservePanel(f *frame.Frame) {
	if l == nil || f == nil {
		return
	}
	prev := l.panel.watch
	next := l.panel.spare[:0]
	// f.Units is in ascending slot order and a slot holds one unit, so next
	// is built ascending by reference.
	for i := range f.Units {
		v := &f.Units[i]
		ref := pool.UnitRef{Handle: v.Slot, Serial: v.AllocationSerial}
		if !l.Selected(ref) {
			continue
		}
		w := panelWatch{ref: ref, activated: v.Activated, complete: v.BuildRemaining == 0}
		if j, found := slices.BinarySearchFunc(prev, ref, func(p panelWatch, r pool.UnitRef) int { return refLess(p.ref, r) }); found {
			if prev[j] != w && l.panel.fresh {
				l.markPanelStale()
				l.touch()
			}
		}
		next = append(next, w)
	}
	l.panel.watch, l.panel.spare = next, prev
}

// RetainPanel applies the retained fields to f's command page, just folded
// from f's composed selection. While the refresh-needed flag is up, or the
// composed selection differs from the one the retained fields were folded
// from, the fold refreshes them. Otherwise the retained values, write-backs
// included, replace the fold's [07 R-HUD-03 §13][04 R-STANCE-01 §2].
func (l *LocalInterface) RetainPanel(f *frame.Frame) {
	if l == nil || f == nil {
		return
	}
	p := &l.panel
	composed := p.scratch[:0]
	for i := range f.Units {
		v := &f.Units[i]
		if f.Selection.Contains(v.Slot) {
			composed = append(composed, pool.UnitRef{Handle: v.Slot, Serial: v.AllocationSerial})
		}
	}
	p.scratch = composed
	page := &f.CommandPage
	if !p.fresh || !slices.Equal(p.selection, composed) {
		p.fresh = true
		p.move, p.fire = page.MoveStance, page.FireStance
		p.cloak, p.onoff = page.CloakState, page.OnOffState
		p.selection = append(p.selection[:0], composed...)
		return
	}
	page.MoveStance, page.FireStance = p.move, p.fire
	page.CloakState, page.OnOffState = p.cloak, p.onoff
}

// WriteBackStance is the stance arm's step 3: the new value goes into the
// retained field [04 R-STANCE-01 §2].
func (l *LocalInterface) WriteBackStance(fire bool, value uint8) {
	if l == nil {
		return
	}
	if fire {
		l.panel.fire = value
	} else {
		l.panel.move = value
	}
	l.touch()
}

// WriteBackCloak is the cloak arm's write-back: 1 after `Cloak_On`, 0 after
// `Cloak_Off` [04 R-STANCE-01 §2].
func (l *LocalInterface) WriteBackCloak(cloaked bool) {
	if l == nil {
		return
	}
	l.panel.cloak = boolPair(cloaked)
	l.touch()
}

// WriteBackOnOff is the on/off arm's write-back: 1 after Activate, 0 after
// Deactivate [04 R-STANCE-01 §2].
func (l *LocalInterface) WriteBackOnOff(on bool) {
	if l == nil {
		return
	}
	l.panel.onoff = boolPair(on)
	l.touch()
}

func boolPair(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
