package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Each axis rejects burn-blow before mutation; yaw precedes pitch [06 §6.7].
func TestGuidanceFailurePreservesFailingAxis(t *testing.T) {
	point := Vec3{X: numeric.FixedFromInt(100)}
	yaw := YawFromDelta(point.X, point.Z)
	pitch := PitchFromDelta(point.X, point.Y, point.Z)
	for _, tc := range []struct {
		name                 string
		yawError, pitchError uint16
		burn, fail           bool
		yawStep, pitchStep   uint16
	}{
		{"yaw fails", 27001, 100, true, true, 0, 0},
		{"pitch fails after yaw", 100, 27001, true, true, 7, 0},
		{"threshold admitted", 27000, 27000, true, false, 7, 7},
		{"burn blow disabled", 27001, 27001, false, false, 7, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Projectile{Yaw: yaw - numeric.Angle(tc.yawError), Pitch: pitch - numeric.Angle(tc.pitchError)}
			oldYaw, oldPitch := p.Yaw, p.Pitch
			failed := steerToward(&p, &content.WeaponDef{BurnBlow: tc.burn, TurnRate: 7}, point)
			if failed != tc.fail || p.Yaw != oldYaw+numeric.Angle(tc.yawStep) || p.Pitch != oldPitch+numeric.Angle(tc.pitchStep) {
				t.Fatalf("failed=%v yaw step=%d pitch step=%d", failed, uint16(p.Yaw-oldYaw), uint16(p.Pitch-oldPitch))
			}
		})
	}
}
