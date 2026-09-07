package session

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
)

type fingerprintProvider struct {
	request path.Request
	taken   bool
}

func (*fingerprintProvider) PlayerCount() int         { return 1 }
func (*fingerprintProvider) UnitLimit() int32         { return 1 }
func (*fingerprintProvider) Eligible(player int) bool { return player == 0 }
func (p *fingerprintProvider) Poll(player int) (path.Request, path.PollResult) {
	if player != 0 || p.taken {
		return path.Request{}, path.PollNoUnit
	}
	p.taken = true
	return p.request, path.PollRequest
}

func fingerprintFixture(t *testing.T, done bool) (*Session, pool.Handle, *fingerprintProvider) {
	t.Helper()
	w := newSessionFixtureWorld(1, nil)
	h, err := w.Create(&content.UnitDef{UnitName: "fingerprint", MaxDamage: 100}, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := movement.NewSystem(nil, movement.Profile{}, nil)
	m.BindWorld(w)
	m.Collisions[h] = &movement.CollisionState{ID: int(h), X: 12}
	p := &fingerprintProvider{request: path.Request{Unit: h, Player: 0, Goal: path.PointGoal(path.Cell{X: 7}, 0)}}
	m.Scheduler = path.NewScheduler(func(path.Request, int32, int) path.WorkResult {
		if done {
			return path.WorkResult{Done: true, Pops: 1, Points: []path.Point{{X: 7}}}
		}
		// Exhaust the allowance in one slice while retaining an active request.
		return path.WorkResult{Pops: 1 << 20}
	}, nil)
	m.Scheduler.SetCandidateProvider(p)
	m.Scheduler.SetPlayerCount(1)
	m.Scheduler.SetUnitLimit(1)
	s := &Session{Clock: &clock.State{}, Units: w, Econ: &economy.Service{}, Movement: m}
	s.SeedSessionRNG(7, 11)
	return s, h, p
}

func readFingerprint(t *testing.T, s *Session) string {
	t.Helper()
	got, err := s.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, PartialStateFingerprintVersion+":") {
		t.Fatalf("fingerprint has no schema prefix: %q", got)
	}
	return got
}

func TestPartialFingerprintIgnoresRecordedMovementDiagnostics(t *testing.T) {
	for _, done := range []bool{false, true} {
		t.Run(map[bool]string{false: "active request", true: "completed request"}[done], func(t *testing.T) {
			a, _, _ := fingerprintFixture(t, done)
			b, h, _ := fingerprintFixture(t, done)
			b.EnableParityTrace([]pool.Handle{h})
			for _, s := range []*Session{a, b} {
				s.Movement.Scheduler.Tick(1)
				s.Movement.EndTick(1)
			}
			if len(b.Movement.CollisionHistory(h)) == 0 {
				t.Fatal("fixture failed to record a collision")
			}
			if _, result := b.Movement.Scheduler.TraceFor(h); result == nil {
				t.Fatal("fixture failed to record a path result")
			}
			want := readFingerprint(t, a)
			assertUnchanged := func() {
				t.Helper()
				if got := readFingerprint(t, b); got != want {
					t.Fatalf("diagnostics changed fingerprint: %s / %s", want, got)
				}
			}
			assertUnchanged()
			b.SetParityTraceLimit(0)
			b.Movement.EndTick(2)
			if !b.ParityTraceDropped() {
				t.Fatal("zero-limit fixture failed to report a dropped collision")
			}
			assertUnchanged()
			b.ResetParityTrace()
			assertUnchanged()
			assertUnchanged() // repeated observation remains pure
		})
	}
}

func TestPartialFingerprintIncludesLiveCollisionAndActiveGoal(t *testing.T) {
	for _, kind := range []string{"collision", "active goal"} {
		t.Run(kind, func(t *testing.T) {
			a, _, _ := fingerprintFixture(t, false)
			b, h, provider := fingerprintFixture(t, false)
			if kind == "collision" {
				b.Movement.Collisions[h].X++
			} else {
				provider.request.Goal = path.PointGoal(path.Cell{X: 8}, 0)
			}
			a.Movement.Scheduler.Tick(1)
			b.Movement.Scheduler.Tick(1)
			if readFingerprint(t, a) == readFingerprint(t, b) {
				t.Fatal("included live state did not affect fingerprint")
			}
		})
	}
}
