package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Clone expiry reads the ordinary creator's stored planar distance, not range,
// the current target/muzzle, or the root's deadline [06 §4.3][06 §6.3].
func TestBurstCloneUsesStoredDistanceAndScalarSpeed(t *testing.T) {
	var s Service
	w := &content.WeaponDef{ID: 1, LineOfSight: true, Range: 400, WeaponVelocity: int32(numeric.FixedFromInt(10)), Burst: 1, BurstRate: 5}
	h, ok := TryFire(&s, &Slot{Weapon: w}, 0, Target{Kind: TargetPoint, X: numeric.FixedFromInt(60), Y: numeric.FixedFromInt(200), Z: numeric.FixedFromInt(80)}, 20, FirePorts{})
	if !ok {
		t.Fatal("root creation failed")
	}
	p := &s.Records[int(h)-1]
	if p.ExpiryTick != 60 || p.StoredPlanarDistance != numeric.FixedFromInt(100) {
		t.Fatalf("root expiry/distance = %d/%d, want 60/100 world units", p.ExpiryTick, p.StoredPlanarDistance)
	}
	p.TargetPos = Vec3{X: numeric.FixedFromInt(900)}
	if n := s.AdvanceBursts(25, nil, func(int32) (*content.WeaponDef, bool) { return w, true }, func(pool.Handle, int16) (Vec3, bool) { return Vec3{X: numeric.FixedFromInt(50)}, true }); n != 1 {
		t.Fatalf("clones = %d", n)
	}
	clone := s.Records[1]
	if clone.ExpiryTick != 36 || clone.StoredPlanarDistance != numeric.FixedFromInt(100) {
		t.Fatalf("clone expiry/distance = %d/%d, want 36/100 world units", clone.ExpiryTick, clone.StoredPlanarDistance)
	}
}

// The timer takes precedence, otherwise both operands and the sum are unsigned
// words. Random decay still adjusts an expiry that wrapped to zero [06 §4.3].
func TestBurstCloneExpiryWordArithmetic(t *testing.T) {
	cases := []struct {
		name            string
		now, timer      uint32
		distance, speed numeric.Fixed
		decay           int32
		want            uint32
	}{
		{"timer bypasses zero speed", 10, 7, 0, 0, 0, 17},
		{"stored scalar speed", 10, 0, numeric.FixedFromInt(100), numeric.FixedFromInt(5), 0, 33},
		{"unsigned numerator", 1, 0, -numeric.FixedFromInt(16) - 1, numeric.FixedFromInt(1), 0, 65536},
		{"numerator addition wraps", 1, 0, numeric.Fixed(-1), numeric.FixedFromInt(1), 0, 16},
		{"unsigned scalar speed", 1, 0, 0, numeric.Fixed(-1), 0, 1},
		{"deadline wraps", ^uint32(0) - 4, 0, numeric.FixedFromInt(100), numeric.FixedFromInt(10), 0, 6},
		{"wrapped zero still decays", ^uint32(0) - 10, 0, numeric.FixedFromInt(100), numeric.FixedFromInt(10), 100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s Service
			s.Reserve()
			s.Records[0] = Projectile{WeaponID: 1, BurstRemaining: 1, ExpiryTick: 999, StoredPlanarDistance: tc.distance, Speed: tc.speed}
			w := &content.WeaponDef{ID: 1, WeaponTimer: int32(tc.timer), RandomDecay: tc.decay}
			r := rng.NewSimulation(1)
			expectedRNG := r
			want := tc.want
			if tc.decay != 0 {
				jitter := int32(expectedRNG.Uint32n(uint32(tc.decay))) - tc.decay/2
				if jitter == 0 {
					t.Fatal("fixture requires nonzero decay")
				}
				want += uint32(jitter)
			}
			s.AdvanceBursts(tc.now, &r, func(int32) (*content.WeaponDef, bool) { return w, true }, nil)
			if got := s.Records[1].ExpiryTick; got != want {
				t.Fatalf("expiry = %d, want %d", got, want)
			}
			if r != expectedRNG {
				t.Fatalf("random stream changed beyond decay")
			}
		})
	}
}

// Only ordinary creation writes the stored distance [06 §6.1]. A ballistic
// burn-blow deadline computes a separate distance without storing this field.
func TestProjectileCreatorsWriteOrRetainPlanarDistance(t *testing.T) {
	cases := []struct {
		name string
		w    content.WeaponDef
		want numeric.Fixed
	}{
		{"ordinary", content.WeaponDef{LineOfSight: true}, 5},
		{"ordinary truncation", content.WeaponDef{LineOfSight: true}, 5},
		{"ballistic", content.WeaponDef{Ballistic: true}, 777},
		{"burn blow ballistic", content.WeaponDef{Ballistic: true, BurnBlow: true}, 777},
		{"vertical", content.WeaponDef{VLaunch: true}, 777},
		{"dropped", content.WeaponDef{Dropped: true}, 777},
		{"meteor", content.WeaponDef{Meteor: true}, 777},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s Service
			recycleSlotOne(t, &s, Projectile{StoredPlanarDistance: 777})
			s.Reserve()
			p := &s.Records[0]
			if p.StoredPlanarDistance != 777 {
				t.Fatal("reservation cleared distance")
			}
			w := tc.w
			w.WeaponVelocity = int32(numeric.FixedFromInt(10))
			target := Vec3{X: 3, Y: numeric.FixedFromInt(100), Z: 4}
			if tc.name == "ordinary truncation" {
				target.X = 4
			}
			InitProjectile(p, &w, 10, Vec3{}, target, 0, 0, 0, nil, 0, 0, 0, 0)
			if p.StoredPlanarDistance != tc.want {
				t.Fatalf("distance = %d, want %d", p.StoredPlanarDistance, tc.want)
			}
			InitCommon(p, 11, Vec3{}, &Vec3{}, 0, 0, 0, 0, &w)
			if p.StoredPlanarDistance != tc.want {
				t.Fatal("common initializer rewrote distance")
			}
			if w.Ballistic || w.VLaunch {
				p.BurstRemaining = 1
				p.BurstDeadline = 20
				s.AdvanceBursts(20, nil, func(int32) (*content.WeaponDef, bool) { return &w, true }, nil)
				if clone := s.Records[1]; clone.StoredPlanarDistance != 777 || clone.ExpiryTick != 21 {
					t.Fatalf("nonordinary clone lost retained lifetime input: distance=%d expiry=%d", clone.StoredPlanarDistance, clone.ExpiryTick)
				}
			}
		})
	}
}

// Triggered copies publish through the same event seam as the root, after the
// copy and before expiry and random work, without replaying callbacks [06 §4.3]
// [06 R-WFX-01 §3]. Allocation failure consumes neither sound nor random draws.
func TestBurstSoundTriggerPublicationOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		trigger, full bool
		sounds        int
	}{{"triggered", true, false, 2}, {"flag clear", false, false, 1}, {"pool full", true, true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var s Service
			w := &content.WeaponDef{ID: 1, LineOfSight: true, Range: 400, WeaponVelocity: int32(numeric.FixedFromInt(10)), Burst: 2, BurstRate: 5, SoundStart: "burst-start", SoundTrigger: tc.trigger, RandomDecay: 100, SprayAngle: 100}
			r := rng.NewSimulation(9)
			spy := &FireSpy{}
			muzzle := Vec3{X: numeric.FixedFromInt(30)}
			refreshed := Vec3{X: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(50)}
			var events []Event
			s.Events = func(e Event) {
				events = append(events, e)
				if len(events) == 2 {
					clone := s.Records[1]
					if s.Count() != 2 || clone.CreationTick != 5 || clone.ExpiryTick != 40 || clone.BurstRemaining != 1 || r.Draws() != 0 {
						t.Fatalf("sound ran outside copy-before-expiry/random boundary: clone=%+v draws=%d", clone, r.Draws())
					}
				}
			}
			shooter := &units.Unit{Handle: 7}
			shooter.Move.Heading = 0xc000 // fixed muzzle faces the target on +X
			_, ok := TryFire(&s, &Slot{Weapon: w}, 0, Target{Kind: TargetPoint, X: numeric.FixedFromInt(130)}, 0, FirePorts{RNG: &r, Spy: spy, Shooter: shooter, Events: &combatFireEvents{svc: &s, shooter: 7, pos: muzzle}})
			if !ok {
				t.Fatal("root failed")
			}
			callbacks := len(spy.Events)
			if tc.full {
				for s.Count() < ProjectileCapacity {
					s.Reserve()
				}
			}
			before := r
			s.AdvanceBursts(5, &r, func(int32) (*content.WeaponDef, bool) { return w, true }, func(pool.Handle, int16) (Vec3, bool) { return refreshed, true })
			if len(events) != tc.sounds {
				t.Fatalf("sounds = %d, want %d", len(events), tc.sounds)
			}
			if len(spy.Events) != callbacks {
				t.Fatal("clone reran root callbacks")
			}
			if len(events) == 2 {
				want := Event{Kind: EventStartSound, Tick: 5, Source: 7, Position: refreshed, Sound: w.SoundStart}
				if events[1] != want {
					t.Fatalf("clone event = %+v, want %+v", events[1], want)
				}
			}
			if !tc.full {
				before.Uint32n(100)
				before.Uint32n(100)
			}
			if r != before {
				t.Fatal("unexpected random stream change")
			}
		})
	}
}

// A timerless zero-speed clone faults after allocation/copy/sound, before any
// random draw, rather than substituting an invented lifetime [06 §4.3].
func TestBurstZeroSpeedFaultKeepsPriorSideEffects(t *testing.T) {
	var s Service
	s.Reserve()
	s.Records[0] = Projectile{WeaponID: 1, BurstRemaining: 2, ExpiryTick: 99}
	w := &content.WeaponDef{ID: 1, SoundTrigger: true, SoundStart: "start", RandomDecay: 10}
	r := rng.NewSimulation(1)
	sounds := 0
	s.Events = func(Event) { sounds++ }
	defer func() {
		if recover() == nil {
			t.Fatal("zero-speed divide did not fault")
		}
		if s.Count() != 2 || sounds != 1 || r.Draws() != 0 || s.Records[0].BurstRemaining != 1 || s.Records[1].BurstRemaining != 1 {
			t.Fatalf("fault lost prior side effects: count=%d sound=%d draws=%d", s.Count(), sounds, r.Draws())
		}
	}()
	s.AdvanceBursts(1, &r, func(int32) (*content.WeaponDef, bool) { return w, true }, nil)
}

// Reusing the stored planar distance keeps the creator's signed-short pitch
// operands, including fractional truncation and the whole-word sign boundary
// [06 §6.3][06 §3.3].
func TestOrdinaryStoredDistancePreservesPitchNarrowing(t *testing.T) {
	w := &content.WeaponDef{LineOfSight: true, WeaponVelocity: int32(numeric.FixedFromInt(10))}
	for _, target := range []Vec3{
		{X: numeric.FixedFromInt(3), Y: -numeric.FixedFromInt(2) - 1, Z: numeric.FixedFromInt(4) + 1},
		{X: numeric.FixedFromInt(32768), Y: numeric.FixedFromInt(1), Z: 1},
		{X: 1, Y: 1, Z: 1},
	} {
		var p Projectile
		InitOrdinary(&p, w, 0, Vec3{}, target, 0)
		if want := PitchFromDelta(target.X, target.Y, target.Z); p.Pitch != want {
			t.Fatalf("target=%+v pitch=%d want=%d", target, p.Pitch, want)
		}
	}
}
