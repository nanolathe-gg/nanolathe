// WU-19-115: playtest report 3, the settling half. Five movers sent to one
// rally point must settle: the leader arrives and retires, and the four that
// cannot occupy the goal cell idle in place, re-arming their record every
// 30-59 ticks without restarting their engines ([04 R-EGRESS-01],
// [04 R-ORDER-02 §1]). Skips when ~/TotalAnnihilation is absent.
package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// wu19115Rally builds the shared scenario: five `armflash` on one row, all
// ordered to a single reachable cell well clear of their start. It returns the
// session, the step function, the handles and the goal cell.
func wu19115Rally(t *testing.T) (*Session, func(), []pool.Handle, path.Cell) {
	t.Helper()
	sess, step, com := wu19107Battle(t)
	local := sess.LocalOwner

	def, ok := sess.Catalog.Unit(content.CanonicalKey("armflash"))
	if !ok {
		t.Skip("armflash absent from the reference install")
	}
	baseCX := world.WorldToCell(com.X) + 6
	baseCZ := world.WorldToCell(com.Z)
	var made []pool.Handle
	for i := int32(0); i < 5; i++ {
		h, err := sess.Units.Create(def, uint8(local), world.CellToWorld(baseCX+2*i), 0, world.CellToWorld(baseCZ))
		if err != nil {
			t.Fatalf("create mover %d: %v", i, err)
		}
		sess.Movement.EnsureUnit(sess.Units.Unit(h))
		made = append(made, h)
	}
	var goal path.Cell
	found := false
	for r := int32(18); r < 40 && !found; r++ {
		c := path.Cell{X: baseCX, Z: baseCZ + r}
		if sess.Movement.IsGoalCellPassable(made[0], c) {
			goal, found = c, true
		}
	}
	if !found {
		t.Skip("no reachable rally cell on this start position")
	}
	if err := sess.EnqueueHumanCommand(HumanCommand{
		Kind: HumanOrder,
		Order: HumanOrderCommand{
			Handles:  made,
			Code:     2,
			Position: orders.ResolvePos{X: world.CellToWorld(goal.X), Z: world.CellToWorld(goal.Z)},
		},
	}); err != nil {
		t.Fatalf("issue rally move: %v", err)
	}
	return sess, step, made, goal
}

// wu19115Sample is one mover's settling record over the observation window.
type wu19115Sample struct {
	restarts  int // 0 -> nonzero speed transitions after the mover first stops
	moves     int // ticks on which the committed position changed
	running   int // ticks on which the mover's speed word was nonzero
	firstStop int // tick of the first stop, -1 while still moving
}

// wu19115Observe steps the session for the given number of ticks, recording per
// unit how often its engine restarts and how often it moves. The engine-restart
// count is the observable behind the report ("they keep shifting around and
// playing sounds"): every 0 -> nonzero speed transition is a fresh
// StartMoving/MoveRate cue pair [04 §5.2].
func wu19115Observe(sess *Session, step func(), made []pool.Handle, ticks int) map[pool.Handle]*wu19115Sample {
	out := make(map[pool.Handle]*wu19115Sample, len(made))
	type prev struct {
		speed int64
		x, z  int64
	}
	last := make(map[pool.Handle]*prev, len(made))
	for _, h := range made {
		u := sess.Units.Unit(h)
		// A mover already at rest when the window opens has "stopped" for the
		// purposes of the restart count; otherwise a unit that settles before
		// tick 0 of the window would have every later restart discounted.
		stop := -1
		if u.Move.Speed == 0 {
			stop = 0
		}
		out[h] = &wu19115Sample{firstStop: stop}
		last[h] = &prev{speed: int64(u.Move.Speed), x: int64(u.X), z: int64(u.Z)}
	}
	for i := 0; i < ticks; i++ {
		step()
		for _, h := range made {
			u := sess.Units.Unit(h)
			if u == nil || !u.Alive {
				continue
			}
			s, p := out[h], last[h]
			speed := int64(u.Move.Speed)
			if speed == 0 && p.speed != 0 && s.firstStop < 0 {
				s.firstStop = i
			}
			if speed != 0 && p.speed == 0 && s.firstStop >= 0 {
				s.restarts++
			}
			if speed != 0 {
				s.running++
			}
			if int64(u.X) != p.x || int64(u.Z) != p.z {
				s.moves++
			}
			p.speed, p.x, p.z = speed, int64(u.X), int64(u.Z)
		}
	}
	return out
}

// TestRallyMoversSettleRetail is the acceptance test for playtest report 3:
// "units moving to a rally point never settle down; they keep shifting around
// and playing sounds seemingly forever".
//
// The unreachable-goal contract is [04 R-EGRESS-01]: the followers of a shared
// destination "re-arm every 30 ticks against a goal they cannot occupy and idle
// in place". Idling in place is not the same as re-accelerating on every
// re-arm: [04 R-PATH-01 §8] step 5.3 suppresses the goal installer's synthetic
// straight line for a record whose retiring flag is set, and [05 R-EGRESS-02]
// names that flag as the pump's code-9 completion flag, which every re-armed
// record carries. A mover therefore gets at most one synthetic straight line
// per record — the one installed before the pump has declared it complete.
//
// The second assertion is the orbit case. A point goal whose cell is occupied
// is still reachable as far as the search is concerned: the ray's write-once
// tolerance makes every opened cell whose scaled heuristic is at or below it an
// acceptable terminal, so A* legitimately publishes a route ending short of the
// goal ([04 R-PATH-01 §1] "Arrival tolerance uses a write-once threshold").
// The follower has no special case for that: it prunes to the terminal, drops
// below two points, clears has-waypoint and brakes ([04 R-MOV-01 §3]), arms
// wants-repath, and re-requests at most once per 60 ticks ([04 R-MOV-01 §7]).
// Once the goal cannot be improved on, the request publishes empty, which is
// the `0x40` that re-arms the record ([04 R-PATH-01 §4] step 9,
// [04 R-ORDER-02 §1]). The steady state is therefore SILENT: no motion, no
// engine, only the record's own 30-59-tick re-arm. Two engine restarts per
// second was the synthetic line reasserting itself on every one of those
// re-arms, not a separate defect.
func TestRallyMoversSettleRetail(t *testing.T) {
	sess, step, made, _ := wu19115Rally(t)
	// Let the column form and the stragglers find their slots: the leader
	// arrives, the rest close up behind it around the occupied goal cell.
	for i := 0; i < 3000; i++ {
		step()
	}
	got := wu19115Observe(sess, step, made, 3000)
	for _, h := range made {
		s := got[h]
		u := sess.Units.Unit(h)
		if u == nil || !u.Alive {
			continue
		}
		if s.restarts > 1 {
			t.Errorf("mover %d restarted its engine %d times after settling; a re-armed record's "+
				"goal install must not resynthesize a straight line at a goal it cannot occupy "+
				"[04 R-PATH-01 §8 step 5.3][05 R-EGRESS-02][04 R-EGRESS-01]", h, s.restarts)
		}
		// One hundred seconds of settled time. The tolerance is two seconds of
		// motion, not zero, so a single late re-slot does not fail the test;
		// the defect this guards against ran the engine on 68 of these ticks
		// and never stopped doing so.
		if s.running > 60 {
			t.Errorf("mover %d ran its engine on %d of 3000 settled ticks (%d position changes); a "+
				"blocked follower idles in place [04 R-EGRESS-01][04 R-MOV-01 §3]", h, s.running, s.moves)
		}
	}
}

// The work unit's before/after measurement harness, TestRallyMoversSettleReport,
// lived here. It asserted nothing — it re-ran wu19115Rally for 6000 ticks and
// logged per-mover restart/move/running counts — and the numbers it was written
// to produce are now the assertions in TestRallyMoversSettleRetail above.
