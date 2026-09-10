package combat

type DebugProjectile struct {
	Slot              int
	PublishedIdentity uint64
	State             Projectile
}

// Projectile consists solely of scalar/fixed-array runtime values.
func (s *Service) DebugSnapshot() []DebugProjectile {
	if s == nil {
		return nil
	}
	out := make([]DebugProjectile, 0, s.Slots.Count())
	for i := 0; i < s.Slots.Count(); i++ {
		out = append(out, DebugProjectile{Slot: i, PublishedIdentity: s.presentationIDs[i], State: s.Records[i]})
	}
	return out
}
