package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// applyAndObserve runs the pending commands for one tick and observes the
// publication as the host does at each completed tick (applyPublishedCamera).
func applyAndObserve(t *testing.T, b *battleSession) {
	t.Helper()
	applyPendingBattleCommands(b)
	if b.sess == nil || b.sess.Snapshot == nil {
		t.Fatal("no snapshot")
	}
	b.observeLocalInterface(b.sess.Snapshot.Current())
}

// pressCloak presses CLOAK and reports the direction it sent and the pair the
// panel shows afterwards.
func pressCloak(t *testing.T, b *battleSession) (cloak bool, shown uint8) {
	t.Helper()
	b.toggleCloakSelected()
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanCloak {
		t.Fatalf("CLOAK press enqueued %+v, want one cloak command", pending)
	}
	cloak = pending[0].Cloak.Cloak
	f, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("no snapshot after the press")
	}
	return cloak, f.CommandPage.CloakState
}

// Two cloak-capable units always fold to the generic CLOAK ORDERS pair 2,
// whether or not they agree [07 R-HUD-03 §13]. The press writes the pair it
// sent back into the panel's retained field, and neither the press nor the
// cloak order raises the refresh-needed flag. So the first press decloaks and
// shows VISIBLE, and the second cloaks both [04 R-STANCE-01 §2]. When the
// panel refolded at every publication, every press sent Cloak_Off and the
// selection could never be cloaked from the panel (issue #203).
func TestCloakPressOnTwoCloakableUnitsAlternates(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	a := placeUnit(b, "armcons", numeric.Fixed(6*65536), numeric.Fixed(6*65536))
	c := placeUnit(b, "armcons", numeric.Fixed(12*65536), numeric.Fixed(12*65536))
	a.Def.CloakCost, c.Def.CloakCost = 100, 100
	a.IsCloaked = true
	replaceSelectionForTest(t, b, a, c)
	b.observeLocalInterface(b.sess.Snapshot.Current())
	if f, _ := b.currentSnapshot(); f.CommandPage.CloakState != 2 {
		t.Fatalf("two cloak-capable units fold to %d, want 2", f.CommandPage.CloakState)
	}

	if cloak, shown := pressCloak(t, b); cloak || shown != 0 {
		t.Fatalf("first press sent Cloak=%v showing %d, want Cloak_Off showing 0 (VISIBLE)", cloak, shown)
	}
	applyAndObserve(t, b)
	if a.IsCloaked || c.IsCloaked {
		t.Fatal("Cloak_Off left a unit cloaked")
	}
	if f, _ := b.currentSnapshot(); f.CommandPage.CloakState != 0 {
		t.Fatalf("after Cloak_Off applied the panel shows %d, want the written-back 0", f.CommandPage.CloakState)
	}

	if cloak, shown := pressCloak(t, b); !cloak || shown != 1 {
		t.Fatalf("second press sent Cloak=%v showing %d, want Cloak_On showing 1 (CLOAKED)", cloak, shown)
	}
	applyAndObserve(t, b)
	if !a.IsCloaked || !c.IsCloaked {
		t.Fatal("Cloak_On did not cloak both units")
	}

	// Reselecting the same two units is a selection gesture: it refreshes
	// the field from the fold, which is 2 again.
	replaceSelectionForTest(t, b, a, c)
	if f, _ := b.currentSnapshot(); f.CommandPage.CloakState != 2 {
		t.Fatalf("reselection shows %d, want the refolded 2", f.CommandPage.CloakState)
	}
}

// A selected unit's activation change raises the refresh-needed flag; a
// cloak change alone does not [07 R-HUD-03 §13].
func TestPanelRefreshFollowsActivationNotCloak(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	a := placeUnit(b, "armcons", numeric.Fixed(6*65536), numeric.Fixed(6*65536))
	c := placeUnit(b, "armcons", numeric.Fixed(12*65536), numeric.Fixed(12*65536))
	a.Def.CloakCost, c.Def.CloakCost = 100, 100
	a.Def.OnOffable = true
	a.Activated, c.Activated = true, true
	replaceSelectionForTest(t, b, a, c)
	b.observeLocalInterface(b.sess.Snapshot.Current())
	if cloak, _ := pressCloak(t, b); cloak {
		t.Fatal("press on pair 2 sent Cloak_On")
	}
	applyAndObserve(t, b)

	// A cloak change that is not a trigger leaves the written-back 0.
	a.IsCloaked = true
	applyAndObserve(t, b)
	if f, _ := b.currentSnapshot(); f.CommandPage.CloakState != 0 {
		t.Fatalf("a cloak change refreshed the panel to %d, want the retained 0", f.CommandPage.CloakState)
	}

	// An activation change is a trigger: the next composition refolds.
	c.Activated = false
	applyAndObserve(t, b)
	f, _ := b.currentSnapshot()
	if f.CommandPage.CloakState != 2 || f.CommandPage.OnOffState != 2 {
		t.Fatalf("after an activation change cloak=%d onoff=%d, want the refolded 2 and 2", f.CommandPage.CloakState, f.CommandPage.OnOffState)
	}
}
