package combat

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// Meteor constants per [06 §6.5] C17.
const (
	// MeteorHeightUnits is the fixed spawn height above the map plane.
	MeteorHeightUnits = 1350
	// MeteorFallVelocityUnits is the fixed vertical speed.
	MeteorFallVelocityUnits = -15
	// MeteorFlightTicks is the horizontal interpolation budget that coincides with vertical arrival.
	MeteorFlightTicks = 90
)

// Fixed-point representations per [I2].
var (
	MeteorHeightFixed       = numeric.Fixed(int64(MeteorHeightUnits) * 65536)       // 1350 wu [06 §6.5] = 0xF0000*90
	MeteorFallVelocityFixed = numeric.Fixed(int64(MeteorFallVelocityUnits) * 65536) // -15 wu/tick = -0xF0000 [06 §6.5]
)

// MeteorDelay converts the single-precision source density at working precision,
// then retains the low word of signed-64 truncation [06 §6.5][01 R-DET-01 §1].
// There is no single-precision store of the quotient. Zero and non-finite
// conversion results retain zero; default-block resolution belongs to the caller.
func MeteorDelay(density float64) int32 {
	return numeric.TruncateFloat64ToLow32(30 / float64(float32(density)))
}

// MeteorDurationTicks preserves the authored single-precision source and the
// working-precision multiply before integer conversion [06 §6.5][01 R-DET-01 §1].
func MeteorDurationTicks(duration float64) int32 {
	return numeric.TruncateFloat64ToLow32(float64(float32(duration)) * 30)
}

// MeteorIntervalTicks uses the same source/store boundaries as duration
// [06 §6.5][01 R-DET-01 §1].
func MeteorIntervalTicks(interval float64) int32 {
	return numeric.TruncateFloat64ToLow32(float64(float32(interval)) * 30)
}

// EffectiveMeteorRadius returns the effective radius substituting the
// gamedata/METEOR.TDF default when the OTA radius is literal zero per [06 §6.5] C17.
// Zero OTA parameters substitute the corresponding METEOR.TDF default and enable scheduling.
func EffectiveMeteorRadius(otaRadius int32, defaults *content.MeteorDefaults) int32 {
	if otaRadius != 0 {
		return otaRadius
	}
	if defaults != nil {
		return defaults.MeteorRadius
	}
	return 0
}

// EffectiveMeteorDensity returns the effective density with zero-OTA fallback per [06 §6.5].
func EffectiveMeteorDensity(otaDensity float64, defaults *content.MeteorDefaults) float64 {
	if otaDensity != 0 {
		return otaDensity
	}
	if defaults != nil {
		return defaults.MeteorDensity
	}
	return 0
}

// EffectiveMeteorDuration returns the effective duration with zero-OTA fallback.
func EffectiveMeteorDuration(otaDuration float64, defaults *content.MeteorDefaults) float64 {
	if otaDuration != 0 {
		return otaDuration
	}
	if defaults != nil {
		return defaults.MeteorDuration
	}
	return 0
}

// EffectiveMeteorInterval returns the effective interval with zero-OTA fallback.
func EffectiveMeteorInterval(otaInterval float64, defaults *content.MeteorDefaults) float64 {
	if otaInterval != 0 {
		return otaInterval
	}
	if defaults != nil {
		return defaults.MeteorInterval
	}
	return 0
}

// ResolveMeteorWeapon implements shower resolution per [06 §6.5]:
// an empty weapon name disables meteor scheduling; an unresolved name or a
// resolved weapon lacking the meteor flag falls back to weapon RECORD 0 of
// the ID-indexed table — the definition authoring ID=0, stock [noweapon]
// [06 R-DMG-01 §5] — instead of disabling (refinement of 2026-09-02). The
// table is addressed by the authored ID, so there is no "smallest ID" or
// "first loaded" rule; the smallest-ID fallback that stood here was never
// retail. When the catalog has no ID-0 record retail addresses the
// zero-filled slot, which has no counterpart here: the shower is then
// disabled, the one divergence, unreachable from stock content.
// Returns nil when disabled.
func ResolveMeteorWeapon(name string, weapons map[string]*content.WeaponDef) *content.WeaponDef {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	ck := content.CanonicalKey(name)
	if w, ok := weapons[ck]; ok && w.Meteor {
		return w
	}
	if w, ok := content.WeaponByID(weapons, 0); ok {
		return w // record 0 [06 §6.5]
	}
	return nil
}

// IsMeteorEnabled reports whether meteor scheduling is enabled per [06 §6.5]:
// only an empty weapon name disables; literal-zero radius/density/duration/interval
// remain enabled via defaults.
func IsMeteorEnabled(weaponName string) bool {
	return strings.TrimSpace(weaponName) != ""
}

// MeteorTarget picks the shower target cell per [06 §6.5] [R-CORE-01 §4.4.1].
// It consumes two CRT draws [01 §7.2] [06 §6.5] I4: the map DEPTH draw first,
// then the map WIDTH draw, as crtRand * dimension /0x8000. Results are
// clamped to [0, dimension-1] when dimension >0; dimension <=0 yields 0.
// Depth-then-width order is behavior.
func MeteorTarget(crt *rng.CRT, mapWidth, mapHeight int32) (targetX, targetZ int32) {
	// [06 §6.5] scheduling-side draws: four draws per evaluation even when disabled.
	// This helper consumes two of them (targetZ, targetX). Caller must consume the
	// remaining two via MeteorOrigin to complete the four.
	if crt == nil {
		return 0, 0
	}
	targetZ = int32(crt.Rand()) * mapHeight / 0x8000 // [06 §6.5] first draw
	targetX = int32(crt.Rand()) * mapWidth / 0x8000  // [06 §6.5] second draw
	return targetX, targetZ
}

// MeteorOrigin computes the entry cell north of the target per [06 §6.5].
// It consumes two CRT draws: originZ offset (crt*10/0x8000 -15) giving -15..-6,
// originX offset (crt*30/0x8000 -15) giving -15..+14.
// The origin is ALWAYS 6-15 cells north of the target.
func MeteorOrigin(crt *rng.CRT, targetX, targetZ int32) (originX, originZ int32) {
	if crt == nil {
		return targetX, targetZ
	}
	dz := int32(crt.Rand())*10/0x8000 - 15 // [06 §6.5] -15..-6
	dx := int32(crt.Rand())*30/0x8000 - 15 // [06 §6.5] -15..+14
	originZ = targetZ + dz
	originX = targetX + dx
	return originX, originZ
}

// MeteorSchedule consumes the full four scheduling-side CRT draws per [06 §6.5]
// [R-CORE-01 §4.4.1]: targetZ, targetX, originZ offset, originX offset.
// The caller consumes them on every DUE evaluation (next-strike deadline
// passed, non-strict) even when the storm is disabled (I4).
// Returns target and origin cells.
func MeteorSchedule(crt *rng.CRT, mapWidth, mapHeight int32) (targetX, targetZ, originX, originZ int32) {
	targetX, targetZ = MeteorTarget(crt, mapWidth, mapHeight)
	originX, originZ = MeteorOrigin(crt, targetX, targetZ)
	return targetX, targetZ, originX, originZ
}

// MeteorRadiusAndAngle consumes the two per-hit CRT draws per [06 §6.5]:
// effective radius = crtRand * radius /0x8000 and angle = crtRand *2.
// The radius is the authored effective radius (already substituted via defaults).
func MeteorRadiusAndAngle(crt *rng.CRT, radius int32) (effRadius int32, angle uint16) {
	if crt == nil {
		return 0, 0
	}
	effRadius = int32(crt.Rand()) * radius / 0x8000 // [06 §6.5]
	angle = uint16(int32(crt.Rand()) * 2)           // [06 §6.5] 16-bit angle domain step 2
	return effRadius, angle
}

// MeteorLateralOffset computes the sine-table entry spread per [06 §6.5] [04 §5.1].
// Helpers hold round(8192*sin) per 512-word turn; the helper returns
// round(magnitude*sin) via (table*mag+4096)>>13.
// Offsets are bounded by the radius value.
func MeteorLateralOffset(crt *rng.CRT, radius int32) (offX, offZ numeric.Fixed) {
	effRadius, angle := MeteorRadiusAndAngle(crt, radius)
	mag := int32(int64(effRadius) << 16) // fixed magnitude: effRadius world units *65536
	sin := numeric.Sin(numeric.Angle(angle))
	cos := numeric.Cos(numeric.Angle(angle))
	offX = numeric.Fixed(int64(numeric.MulRound(sin, mag)))
	offZ = numeric.Fixed(int64(numeric.MulRound(cos, mag)))
	return offX, offZ
}

// MeteorEntryPos computes the spawn position per [06 §6.5].
// Each meteor spawns at exactly 1350 world units height and at origin
// offset by the lateral sine spread. It consumes the two per-hit draws.
func MeteorEntryPos(crt *rng.CRT, originX, originZ int32, radius int32) (posX, posY, posZ numeric.Fixed) {
	offX, offZ := MeteorLateralOffset(crt, radius)
	posX = numeric.Fixed(int64(originX)<<20) - offX // [06 §6.5] origin<<20 minus spread
	posZ = numeric.Fixed(int64(originZ)<<20) - offZ
	posY = MeteorHeightFixed // [06 §6.5] exactly 1350 wu
	return posX, posY, posZ
}

// MeteorVelocity computes horizontal velocity per [06 §6.5]:
// vel = trunc(((target - origin)<<20)/90) with truncation toward zero [01 §8] I3.
// Vertical velocity is fixed at -15 wu/tick and supplied separately.
func MeteorVelocity(targetX, originX, targetZ, originZ int32) (velX, velZ numeric.Fixed) {
	dx := int64(targetX-originX) << 20 // cell delta scaled to fixed world
	dz := int64(targetZ-originZ) << 20
	vx := dx / MeteorFlightTicks // trunc toward zero, Go division matches [01 §8]
	vz := dz / MeteorFlightTicks
	return numeric.Fixed(vx), numeric.Fixed(vz)
}

// MeteorAngularSteps returns per-tick visual orientation increments per
// [06 §6.5]: the record's ROLL word (the render block's first orientation
// word) advances by (high16(velX)<<8) and its PITCH word by (high16(velZ)<<8),
// wrapping modulo 2^16. The [GAP T21] reconcile is closed (2026-09-02): the
// meteor tick reads the high halves of the velocity words themselves every
// tick; there is no stored angular-rate field. Presentation only, never
// motion.
func MeteorAngularSteps(velX, velZ numeric.Fixed) (rollStep, pitchStep uint16) {
	rollStep = uint16(uint16(velX.Raw()>>16) << 8)
	pitchStep = uint16(uint16(velZ.Raw()>>16) << 8)
	return rollStep, pitchStep
}

// SpawnMeteor appends one meteor projectile through the shared pool per [06 §6.5].
// Geometry is derived from CRT draws; the common initializer uses the no-shooter
// neutral side path so meteor explosions credit nobody [06 §6.5].
// Pool-full silently drops the individual meteor: the strike timer is assumed
// to have already advanced by the caller and there is no retry (I4).
// Returns the handle and true on success, false on pool-full or missing weapon.
func SpawnMeteor(svc *Service, crt *rng.CRT, tick uint32, weapon *content.WeaponDef, targetX, targetZ, originX, originZ int32, radius int32) (pool.Handle, bool) {
	if svc == nil || crt == nil || weapon == nil {
		// Still consume per-hit draws even on invalid args (I4) — caller should have
		// consumed scheduling draws before this. We consume radius/angle here.
		if crt != nil {
			_, _ = MeteorRadiusAndAngle(crt, radius)
		}
		return 0, false
	}
	posX, posY, posZ := MeteorEntryPos(crt, originX, originZ, radius) // consumes 2 draws
	velX, velZ := MeteorVelocity(targetX, originX, targetZ, originZ)
	vel := Vec3{X: velX, Y: MeteorFallVelocityFixed, Z: velZ}
	pos := Vec3{X: posX, Y: posY, Z: posZ}
	h, ok := svc.Reserve()
	if !ok {
		return 0, false
	}
	p := &svc.Records[int(h)-1]
	InitMeteor(p, weapon, tick, pos, vel) // [06 §6.1] [06 §6.5] null-shooter path, neutral side
	// The null-shooter path gives meteors the NEUTRAL side byte 10, so meteor
	// explosions credit nobody [06 §6.5]; side 0 is a real side.
	p.ShooterSide = NeutralSide
	return h, true
}
