package orders

// PreserveBuildToggle answers the generic keyboard-toggle hook, not a gadget
// name match (community-patch-engine.md CP-CON-4). The low status byte is the
// source operand; group clearing and callback firing remain host work.
func (StrictRules) PreserveBuildToggle(prepared bool, status uint8, enabled bool) bool {
	return false
}

func (CommunityRules) PreserveBuildToggle(prepared bool, status uint8, enabled bool) bool {
	return enabled && prepared && status != 0
}
