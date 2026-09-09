package orders

// The save record's queue word uses retail's static-mask-copy bit positions.
// Node.Flags deliberately uses local values, so the two representations must
// be translated at the save boundary [04 R-ORD-01 §13][08 R-SAVE-ORDER-01].
const (
	retailFlagActive              uint32 = 0x1000
	retailFlagCaptionPending      uint32 = 0x2000
	retailFlagAutoOp              uint32 = 0x4000
	retailFlagTombstone           uint32 = 0x10000
	retailFlagRetryMark           uint32 = 0x800000
	retailFlagStopBuildingPending uint32 = 0x400000
)

const retailLocalFlagWireMask = retailFlagActive |
	retailFlagCaptionPending |
	retailFlagAutoOp |
	retailFlagTombstone |
	retailFlagRetryMark |
	retailFlagStopBuildingPending

// retailQueueFlags encodes the semantic runtime state into the retail queue
// word. It leaves every static-mask bit that Nanolathe does not model as a
// local flag intact, including producer and cached-position bits, so a
// constructor-mutated static mask survives the save boundary.
func retailQueueFlags(n *Node) uint32 {
	if n == nil {
		return 0
	}
	word := n.StaticGate &^ retailLocalFlagWireMask
	if n.Flags&FlagActive != 0 {
		word |= retailFlagActive
	}
	if n.CaptionPending {
		word |= retailFlagCaptionPending
	}
	if n.Flags&FlagAutoOp != 0 {
		word |= retailFlagAutoOp
	}
	if n.Flags&FlagTombstone != 0 {
		word |= retailFlagTombstone
	}
	if n.Flags&FlagRetryMark != 0 {
		word |= retailFlagRetryMark
	}
	if n.Flags&FlagStopBuildingPending != 0 {
		word |= retailFlagStopBuildingPending
	}
	return word
}

// restoreRetailQueueFlags separates the canonical wire word into the local
// representation. Purge survivorship is derived from its retained static bit,
// because that property is not a second runtime bit [04 R-MOV-03 §6].
func restoreRetailQueueFlags(word uint32) (staticGate, flags uint32, captionPending bool) {
	staticGate = word &^ retailLocalFlagWireMask
	if staticGate&staticPurgeSurvivor != 0 {
		flags |= FlagPurgeSurvivor
	}
	if word&retailFlagActive != 0 {
		flags |= FlagActive
	}
	if word&retailFlagAutoOp != 0 {
		flags |= FlagAutoOp
	}
	if word&retailFlagTombstone != 0 {
		flags |= FlagTombstone
	}
	if word&retailFlagRetryMark != 0 {
		flags |= FlagRetryMark
	}
	if word&retailFlagStopBuildingPending != 0 {
		flags |= FlagStopBuildingPending
	}
	return staticGate, flags, word&retailFlagCaptionPending != 0
}
