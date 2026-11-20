package hud

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/content"
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
//   - a change of a selected unit's committed activation or building bit;
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
// host sees the StartBuilding/StopBuilding bit, but the order handlers'
// other refresh requests are not published. Classify the handler arms that
// run every tick before publishing those requests [07 R-HUD-03 §13].

// panelWords is the retained copy of the four fields and the state that
// decides when it is refreshed.
type panelWords struct {
	// fresh is false while the refresh-needed flag is up.
	fresh bool
	move  uint8
	fire  uint8
	cloak uint8
	onoff uint8
	// watch holds the composed selection and its trigger state in the last
	// frame the host examined. Both buffers are in ascending slot order.
	watch []panelWatch
	spare []panelWatch
}

// panelWatch is the part of one selected unit's committed state whose change
// raises the refresh-needed flag.
type panelWatch struct {
	ref       pool.UnitRef
	activated bool
	building  bool
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

// RefreshPanel examines f after ComposeSelection. The host calls it in
// publication order and after local interface input. The input call starts
// watching newly selected units before the first tick that can change them.
// It reports whether a trigger refreshed the words [07 R-HUD-03 §13].
func (l *LocalInterface) RefreshPanel(f *frame.Frame, cat *content.Catalog) bool {
	p := &l.panel
	next := p.spare[:0]
	if f.Selection.Count != 0 {
		for i := range f.Units {
			v := &f.Units[i]
			if v.Flags&SelectionFlag != 0 {
				next = append(next, panelWatch{
					ref:       pool.UnitRef{Handle: v.Slot, Serial: v.AllocationSerial},
					activated: v.Activated,
					building:  v.BuildingState,
					complete:  v.BuildRemaining == 0,
				})
			}
		}
	}
	refresh := !p.fresh || !slices.EqualFunc(p.watch, next, func(before, after panelWatch) bool {
		return before.ref == after.ref && before.activated == after.activated &&
			before.building == after.building && (before.complete || !after.complete)
	})
	p.watch, p.spare = next, p.watch
	if !refresh {
		return false
	}

	var page frame.CommandPageView
	foldSelectionAggregate(&page, f, cat)
	p.fresh = true
	p.move, p.fire = page.MoveStance, page.FireStance
	p.cloak, p.onoff = page.CloakState, page.OnOffState
	l.touch()
	return true
}

// panelMatchesSelection reports whether f can use the retained words. An
// older publication may lack a selected unit or show another in its slot.
func (l *LocalInterface) panelMatchesSelection(f *frame.Frame) bool {
	p := &l.panel
	if !p.fresh || int(f.Selection.Count) != len(p.watch) {
		return false
	}
	i := 0
	for _, w := range p.watch {
		for i < len(f.Units) && f.Units[i].Slot < w.ref.Handle {
			i++
		}
		if i == len(f.Units) {
			return false
		}
		if v := &f.Units[i]; v.Slot != w.ref.Handle || v.AllocationSerial != w.ref.Serial || v.Flags&SelectionFlag == 0 {
			return false
		}
	}
	return true
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
