package features

import "sort"

type DebugFeature struct {
	CellIndex                            int
	Definition                           string
	CX, CZ                               int
	DamageAccumulator                    uint16
	ReclaimProgress                      int32
	Burning, Animating, Sinking, Settled bool
	BurnCountdown                        int32
	X, Y, Z, VY                          int64
	Status                               uint8
}

// DebugSnapshot sorts a private key copy, avoiding the live cached-key refresh.
func (s *Service) DebugSnapshot() []DebugFeature {
	if s == nil {
		return nil
	}
	keys := make([]int, 0, len(s.instances))
	for k := range s.instances {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]DebugFeature, 0, len(keys))
	for _, k := range keys {
		f := s.instances[k]
		if f == nil {
			continue
		}
		d := DebugFeature{CellIndex: k, CX: f.CX, CZ: f.CZ, DamageAccumulator: f.DamageAccumulator, ReclaimProgress: f.ReclaimProgress, Burning: f.IsBurning, Animating: f.IsAnimating, Sinking: f.IsSinking, Settled: f.Settled, BurnCountdown: f.BurnCountdown, X: f.X.Raw(), Y: f.Y.Raw(), Z: f.Z.Raw(), VY: f.Vy.Raw(), Status: f.Status}
		if f.Def != nil {
			d.Definition = f.Def.CanonicalKey
		}
		out = append(out, d)
	}
	return out
}
