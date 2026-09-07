package units

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// TestForEachPlayerSliceLiveLaterSlotMutation locks the owner-slice iterator's
// live cursor semantics: a later freed slot can be immediately reused and an
// allocation farther ahead is visible on this traversal [04 §1.1][P0-16 §3.2].
func TestForEachPlayerSliceLiveLaterSlotMutation(t *testing.T) {
	w := newFixtureWorld(3, nil)
	def := &content.UnitDef{UnitName: "owner-iteration", MaxDamage: 1, Limit: -1}
	h1, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	var got []pool.Handle
	w.ForEachPlayerSliceLive(0, func(u *Unit) {
		got = append(got, u.Handle)
		if u.Handle != h1 {
			return
		}
		w.FreeImmediate(h2)
		reused, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatalf("reuse later slot: %v", err)
		}
		if reused != h2 {
			t.Fatalf("reused handle = %d, want %d", reused, h2)
		}
		later, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatalf("allocate later slot: %v", err)
		}
		if later <= reused {
			t.Fatalf("later handle = %d, want above reused handle %d", later, reused)
		}
	})

	if want := []pool.Handle{h1, h2, h2 + 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("live owner-slice visits = %v, want %v", got, want)
	}
}

// TestForEachPlayerSliceLiveKeepsIterSlicedRawAdmission ensures this iterator
// does not add World.Unit's pool.Alive validation to IterSliced's established
// raw record admission. This intentionally inconsistent fixture models only
// the boundary the two public traversals promise to share.
func TestForEachPlayerSliceLiveKeepsIterSlicedRawAdmission(t *testing.T) {
	w := newFixtureWorld(1, nil)
	def := &content.UnitDef{UnitName: "owner-raw-live", MaxDamage: 1, Limit: -1}
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.pool.Free(h) // leave the raw record intact; World.Unit would reject it.

	var got []pool.Handle
	w.ForEachPlayerSliceLive(0, func(u *Unit) { got = append(got, u.Handle) })
	if want := []pool.Handle{h}; !reflect.DeepEqual(got, want) {
		t.Fatalf("raw-live owner-slice visits = %v, want %v", got, want)
	}
}
