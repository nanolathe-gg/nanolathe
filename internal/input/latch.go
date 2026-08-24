// Latch constants are the armed-order latch byte values [GAP T22].
//
// The latch byte holds values that are literally the order-dispatcher switch
// keys (a 14-entry table indexed by value minus one) [07 §9]. They decide
// authorization while the cursor index decides shape; the two coincide
// numerically only by table offset and must not be conflated [07 §8].
package input

// Latch is the armed-order latch byte [GAP T22][07 §9].
type Latch byte

const (
	// LatchNormal is the idle/normal latch (also the generic immediate-order
	// table) [GAP T22].
	LatchNormal Latch = 1

	// LatchMove arms a MOVE order [GAP T22].
	LatchMove Latch = 2

	// LatchAttack arms an ATTACK order [GAP T22].
	LatchAttack Latch = 3

	// LatchBlast arms a BLAST (attack-special) order [GAP T22].
	LatchBlast Latch = 4

	// LatchUnload arms an UNLOAD order [GAP T22].
	LatchUnload Latch = 5

	// LatchPickup arms a PICKUP/LOAD order [GAP T22].
	LatchPickup Latch = 6

	// LatchFollow arms a FOLLOW/GUARD (defend) order [GAP T22].
	LatchFollow Latch = 7

	// LatchRepair arms a REPAIR/HELPBUILD order [GAP T22].
	LatchRepair Latch = 8

	// LatchPatrol arms a PATROL order [GAP T22].
	LatchPatrol Latch = 9

	// 0xA is unused in the latch table [GAP T22][07 §9].

	// LatchTeleport arms a TELEPORT order [GAP T22]. Armed by other reviewed
	// paths, not by the order-button dispatcher [07 §9].
	LatchTeleport Latch = 0xB

	// LatchReclaim arms a RECLAIM/RESURRECT order [GAP T22].
	LatchReclaim Latch = 0xC

	// LatchCapture arms a CAPTURE order [GAP T22].
	LatchCapture Latch = 0xD

	// LatchMobileBuild arms a MOBILEBUILD order [GAP T22]. Armed by other
	// reviewed paths, not by the order-button dispatcher [07 §9].
	LatchMobileBuild Latch = 0xE
)

// Aliases for the latch values that have multiple order-family names [07 §9].
const (
	LatchLoad             = LatchPickup  // LOAD alias for PICKUP [07 §9]
	LatchGuard            = LatchFollow  // GUARD alias for FOLLOW [07 §9]
	LatchDefend           = LatchFollow  // DEFEND alias for FOLLOW [07 §9]
	LatchHelpBuild        = LatchRepair  // HELPBUILD alias for REPAIR [07 §9]
	LatchResurrect        = LatchReclaim // RESURRECT alias for RECLAIM [GAP T22]
	LatchReclaimResurrect = LatchReclaim
)

// IsValid reports whether l corresponds to a defined latch value [GAP T22].
func (l Latch) IsValid() bool {
	switch l {
	case LatchNormal, LatchMove, LatchAttack, LatchBlast, LatchUnload, LatchPickup, LatchFollow, LatchRepair, LatchPatrol, LatchTeleport, LatchReclaim, LatchCapture, LatchMobileBuild:
		return true
	default:
		return false
	}
}

// String returns the latch name for diagnostics.
func (l Latch) String() string {
	switch l {
	case LatchNormal:
		return "Normal"
	case LatchMove:
		return "Move"
	case LatchAttack:
		return "Attack"
	case LatchBlast:
		return "Blast"
	case LatchUnload:
		return "Unload"
	case LatchPickup:
		return "Pickup"
	case LatchFollow:
		return "Follow"
	case LatchRepair:
		return "Repair"
	case LatchPatrol:
		return "Patrol"
	case LatchTeleport:
		return "Teleport"
	case LatchReclaim:
		return "Reclaim"
	case LatchCapture:
		return "Capture"
	case LatchMobileBuild:
		return "MobileBuild"
	default:
		return "Unknown"
	}
}
