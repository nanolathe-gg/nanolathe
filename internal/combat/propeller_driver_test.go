package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestTickProjectilesPropellerAdvanceIsCommonEntryWork uses the live driver,
// including its family expiry branches. The authored advance happens before
// those branches and only for propeller records [06 §6.1][06 §7.1].
func TestTickProjectilesPropellerAdvanceIsCommonEntryWork(t *testing.T) {
	types := []struct {
		name      string
		configure func(*content.WeaponDef)
		expiry    uint32
	}{
		{"direct-expired", func(w *content.WeaponDef) { w.LineOfSight = true }, 1},
		{"direct-live", func(w *content.WeaponDef) { w.LineOfSight = true }, 2},
		{"ballistic-expired", func(w *content.WeaponDef) { w.Ballistic, w.WeaponTimer = true, 1 }, 1},
		{"ballistic-live", func(w *content.WeaponDef) { w.Ballistic, w.WeaponTimer = true, 1 }, 2},
		{"dropped-no-expiry", func(w *content.WeaponDef) { w.Dropped = true }, 1},
		{"selfprop-expired", func(w *content.WeaponDef) { w.SelfProp = true }, 1},
	}
	for _, family := range types {
		t.Run(family.name, func(t *testing.T) {
			for _, propeller := range []bool{false, true} {
				t.Run(map[bool]string{false: "ordinary", true: "propeller"}[propeller], func(t *testing.T) {
					var svc Service
					weapon := &content.WeaponDef{ID: 201, Propeller: propeller}
					family.configure(weapon)
					h, ok := svc.Reserve()
					if !ok {
						t.Fatal("reserve projectile")
					}
					p := &svc.Records[int(h)-1]
					p.WeaponID = weapon.ID
					p.PropellerYaw = numeric.Angle(0xfe00)
					p.Roll = numeric.Angle(0x1234)
					p.ExpiryTick = family.expiry
					svc.TickProjectiles(1, nil, nil, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
					want := numeric.Angle(0xfe00)
					if propeller {
						want = want.Add(1024)
					}
					if got := svc.Records[int(h)-1].PropellerYaw; got != want {
						t.Fatalf("propeller angle after tick = %#04x, want %#04x", uint16(got), uint16(want))
					}
				})
			}
		})
	}
}

// TestTickProjectilesMeteorKeepsVelocityDerivedAccumulators verifies that the
// common authored propeller update does not replace meteor's two independent
// velocity-derived orientation steps [06 §6.1][06 §6.5].
func TestTickProjectilesMeteorKeepsVelocityDerivedAccumulators(t *testing.T) {
	for _, propeller := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "propeller"}[propeller], func(t *testing.T) {
			var svc Service
			weapon := &content.WeaponDef{ID: 202, Meteor: true, Propeller: propeller}
			h, ok := svc.Reserve()
			if !ok {
				t.Fatal("reserve meteor")
			}
			p := &svc.Records[int(h)-1]
			p.WeaponID = weapon.ID
			p.PropellerYaw = numeric.Angle(0xfe00)
			p.Roll = numeric.Angle(0xfe00)
			p.MeteorPitch = numeric.Angle(0x0100)
			p.Velocity = Vec3{X: numeric.FixedFromInt(0x12), Z: numeric.FixedFromInt(-1)}
			roll, pitch := MeteorAngularSteps(p.Velocity.X, p.Velocity.Z)
			svc.TickProjectiles(1, nil, nil, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
			wantRoll := numeric.Angle(uint16(int32(numeric.Angle(0xfe00)) + int32(int16(roll))))
			if got := svc.Records[int(h)-1].Roll; got != wantRoll {
				t.Fatalf("meteor roll = %#04x, want %#04x", uint16(got), uint16(wantRoll))
			}
			wantPropeller := numeric.Angle(0xfe00)
			if propeller {
				wantPropeller = wantPropeller.Add(1024)
			}
			if got := svc.Records[int(h)-1].PropellerYaw; got != wantPropeller {
				t.Fatalf("meteor propeller angle = %#04x, want %#04x", uint16(got), uint16(wantPropeller))
			}
			wantPitch := numeric.Angle(uint16(int32(numeric.Angle(0x0100)) + int32(int16(pitch))))
			if got := svc.Records[int(h)-1].MeteorPitch; got != wantPitch {
				t.Fatalf("meteor pitch = %#04x, want %#04x", uint16(got), uint16(wantPitch))
			}
		})
	}
}
