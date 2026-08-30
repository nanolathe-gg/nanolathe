// Retail-assets end-to-end check for the playtest report "after building one
// aircraft the plant cannot build another". The two probes folded into
// PLAN_17 section 0 row 3 drove construction.QueueFactoryBuild directly and
// were green; this one drives the *presentation dispatch* instead — the
// committed frame's CommandPage builder, the same validation
// battle_dispatch.go:DispatchFactoryBuildDelta applies to it, and the
// session.HumanFactoryBuild command it enqueues [07 §9]. Skipped when
// ~/TotalAnnihilation is absent.
package session

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// fr4Session mounts the retail install, compiles the catalog and drives a
// two-player skirmish into battle, or skips.
func fr4Session(t *testing.T) (*Session, *content.Catalog, func()) {
	t.Helper()
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		if h, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(h, "TotalAnnihilation")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "totala1.hpi")); err != nil {
		t.Skip("retail assets not present at ~/TotalAnnihilation")
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount retail: %v", err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("catalog compile: %v", err)
	}
	keys := make([]string, 0, len(cat.Maps))
	for k := range cat.Maps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mapKey := ""
	for _, k := range keys {
		for _, sch := range cat.Maps[k].Schemas {
			if len(sch.Type) >= 7 && sch.Type[:7] == "Network" {
				mapKey = k
				break
			}
		}
		if mapKey != "" {
			break
		}
	}
	if mapKey == "" {
		t.Skip("no Network map in catalog")
	}
	cfg := SkirmishConfig{MapName: mapKey, NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0
	cfg.Players[1].Controller = 1
	sess, err := NewSkirmishWithFS(fs, cat, cfg)
	if err != nil {
		t.Fatalf("skirmish: %v", err)
	}
	driver := int32(1 << 20)
	step := func() { sess.Step(driver); driver++ }
	for tick := 0; tick < 60 && sess.State != StateBattle; tick++ {
		step()
	}
	if sess.State != StateBattle {
		t.Fatal("shell did not enter battle")
	}
	return sess, cat, step
}

// fr4Enrich keeps the local player's stock far above any admission threshold
// so that the economy stall of PLAN_17 row 7 cannot be mistaken for the
// production defect under test.
func fr4Enrich(sess *Session) {
	if sess.Econ == nil {
		return
	}
	p := &sess.Econ.Players[int(sess.LocalOwner)]
	if p.Capacity[0] < 100000 {
		p.Capacity[0] = 100000
	}
	if p.Capacity[1] < 100000 {
		p.Capacity[1] = 100000
	}
	p.Stock[0] = 50000
	p.Stock[1] = 50000
}

func fr4CountDone(sess *Session, key string, owner int) int {
	n := 0
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Def != nil && u.Def.CanonicalKey == key &&
			int(u.Owner) == owner && u.Remaining <= 0 && u.Flags&construction.FlagCompleted != 0 {
			n++
		}
	}
	return n
}

// fr4SnapshotUnit mirrors battle_dispatch.go:snapshotUnitByHandle: the
// committed frame is the only unit identity the dispatch may consult [I6].
func fr4SnapshotUnit(f *frame.Frame, h pool.Handle) (frame.UnitView, bool) {
	if f == nil {
		return frame.UnitView{}, false
	}
	for i := range f.Units {
		if f.Units[i].Slot == h {
			return f.Units[i], true
		}
	}
	return frame.UnitView{}, false
}

// fr4DispatchFactoryBuild is battle_dispatch.go:DispatchFactoryBuildDelta with
// the same three gates in the same order: the committed CommandPage names the
// builder, the committed view must exist and belong to the local player, and
// its compiled definition must carry the builder flag [07 §9]. The returned
// string names the gate that rejected, so a reproduction says which one.
func fr4DispatchFactoryBuild(sess *Session, cat *content.Catalog, product string, count int) (pool.Handle, string) {
	f := sess.Snapshot.Current()
	if f == nil {
		return 0, "no current snapshot"
	}
	builder := f.CommandPage.Builder
	if builder == 0 {
		return 0, "CommandPage.Builder is zero"
	}
	v, found := fr4SnapshotUnit(f, builder)
	if !found {
		return 0, "builder handle absent from committed units"
	}
	if v.Owner != sess.LocalOwner {
		return 0, "builder view owner is not the local player"
	}
	def, ok := cat.Unit(v.DefName)
	if !ok || def == nil || !def.Builder {
		return 0, "builder view definition is not a builder"
	}
	if err := sess.EnqueueHumanCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{
		Builder: builder, Product: product, Count: count,
	}}); err != nil {
		return 0, "enqueue: " + err.Error()
	}
	return builder, ""
}

// TestFactoryRepeatThroughDispatchRetail queues three products one at a time
// through the presentation dispatch, waiting for each to finish and clear the
// pad, exactly as a player clicking the build button three times does.
func TestFactoryRepeatThroughDispatchRetail(t *testing.T) {
	sess, cat, step := fr4Session(t)
	local := int(sess.LocalOwner)
	rich := func() { step(); fr4Enrich(sess) }
	for i := 0; i < 50; i++ {
		rich()
	}

	var com *units.Unit
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Def != nil && u.Def.Commander && int(u.Owner) == local {
			com = u
			break
		}
	}
	if com == nil {
		t.Fatal("no human commander")
	}

	// The commander builds an aircraft plant. Ring offsets absorb spawn-side
	// variance across maps, as the existing production e2e test does.
	const plantKey = "armap"
	ck := content.CanonicalKey(plantKey)
	var plant *units.Unit
	for _, off := range []int32{24, -24, 32, -32, 16, 40, -40} {
		if err := construction.QueueMobileBuild(com, plantKey, com.X+cellsToWorld(off), com.Z, 1, cat); err != nil {
			t.Fatalf("queue %s: %v", plantKey, err)
		}
		plant = waitCompletedUnit(t, sess, rich, 12000, func(u *units.Unit) bool {
			return u.Def.CanonicalKey == ck && int(u.Owner) == local
		})
		if plant != nil {
			break
		}
		if q := orders.QueueForUnit(com); q != nil {
			q.PurgeUnprotected()
		}
	}
	if plant == nil {
		t.Fatalf("aircraft plant never completed; msgs=%v", sess.Build.Messages())
	}
	t.Logf("plant complete: handle=%d", plant.Handle)

	// Select it the way a click does: the authoritative selection command, then
	// a step so the frame publishes the command page [07 §9][I6].
	if err := sess.EnqueueHumanCommand(HumanCommand{
		Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{plant.Handle}},
	}); err != nil {
		t.Fatalf("select: %v", err)
	}
	for i := 0; i < 4; i++ {
		rich()
	}

	f := sess.Snapshot.Current()
	if f == nil {
		t.Fatal("no committed frame after selection")
	}
	if f.CommandPage.Builder != plant.Handle {
		t.Fatalf("command page builder = %d, want the plant %d", f.CommandPage.Builder, plant.Handle)
	}
	// The product identity a click uses is the committed page key, never a
	// live catalog lookup [07 §9].
	productKey := ""
	for _, key := range f.CommandPage.ProductKeys {
		if d, ok := cat.Unit(key); ok && d != nil && d.BMCode {
			productKey = d.CanonicalKey
			break
		}
	}
	if productKey == "" {
		t.Fatalf("no queueable product on the committed page %v", f.CommandPage.ProductKeys)
	}
	t.Logf("product=%s page=%v", productKey, f.CommandPage.ProductKeys)

	// While the plant stays selected it must remain the committed command
	// page's builder: if the page handle were to drop, the build rail would
	// lose its buttons and every later click would be rejected, which is the
	// shape of the report under test [07 §9].
	flips := 0
	firstFlip := -1
	stepNo := 0
	watch := func() {
		rich()
		stepNo++
		if cur := sess.Snapshot.Current(); cur != nil && cur.CommandPage.Builder != plant.Handle {
			flips++
			if firstFlip < 0 {
				firstFlip = stepNo
			}
		}
	}

	// Rounds 2 and 3 differ deliberately: round 2 is clicked after the product
	// has had time to clear the pad, round 3 is clicked the instant the product
	// completes, with the pad still occupied.
	for round := 1; round <= 3; round++ {
		builder, why := fr4DispatchFactoryBuild(sess, cat, productKey, 1)
		if why != "" {
			cur := sess.Snapshot.Current()
			t.Fatalf("round %d: dispatch rejected: %s (page builder=%d selection=%d alive=%v owner=%d)",
				round, why, cur.CommandPage.Builder, cur.Selection.Count, plant.Alive, plant.Owner)
		}
		if builder != plant.Handle {
			t.Fatalf("round %d: dispatch resolved builder %d, want %d", round, builder, plant.Handle)
		}
		want := round
		done := false
		for i := 0; i < 20000; i++ {
			watch()
			if fr4CountDone(sess, productKey, local) >= want {
				t.Logf("round %d: %s #%d completed after %d steps", round, productKey, want, i)
				done = true
				break
			}
		}
		if !done {
			q := orders.QueueForUnit(plant)
			head, qlen := "", 0
			if q != nil {
				qlen = q.LenPrimary()
				if qlen > 0 {
					head = q.Primary()[0].BuildDefKey
				}
			}
			t.Fatalf("round %d: only %d produced; plant queue len=%d head=%q msgs=%v",
				round, fr4CountDone(sess, productKey, local), qlen, head, sess.Build.Messages())
		}
		if round == 1 {
			// Let the finished product clear the pad before the next click.
			for i := 0; i < 400; i++ {
				watch()
			}
		}
	}
	if flips != 0 {
		t.Fatalf("committed command page lost the selected plant on %d steps, first at step %d", flips, firstFlip)
	}
	t.Logf("command page named the plant on all %d observed steps", stepNo)
}
