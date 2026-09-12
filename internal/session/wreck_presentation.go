package session

import "github.com/nanolathe-gg/nanolathe/internal/features"

// This is the Enhanced prototype's presentation retention policy, not a
// retail feature lifetime: keep death history for 30 seconds at 30 ticks/s.
// The cooling effect ends earlier; nothing authoritative reads this history.
const wreckPresentationLifetimeTicks uint32 = 900

// Only successful death placements enter this staging table. Exact object
// identity prevents a replacement at the same anchor from inheriting heat.
// The separate ordered slice makes retirement independent of map iteration.
type wreckPresentation struct {
	births    map[*features.Instance]uint32
	instances []*features.Instance
}

func (s *Session) noteWreckBirth(inst *features.Instance) {
	if inst == nil {
		return
	}
	p := &s.ensurePublicationState().wrecks
	if p.births == nil {
		p.births = make(map[*features.Instance]uint32)
	}
	if _, known := p.births[inst]; known {
		return
	}
	tick := uint32(0)
	if s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	p.births[inst] = tick
	p.instances = append(p.instances, inst)
}

// Prune before publication even when snapshots are disabled, releasing dead,
// reclaimed, replaced and restored objects without retaining live sim state
// on the renderer side of the committed-frame boundary [I6].
func (p *wreckPresentation) prune(service *features.Service, tick uint32) {
	n := 0
	for _, inst := range p.instances {
		if service == nil || service.InstanceAt(inst.CX, inst.CZ) != inst || tick-p.births[inst] >= wreckPresentationLifetimeTicks {
			delete(p.births, inst)
			continue
		}
		p.instances[n] = inst
		n++
	}
	clear(p.instances[n:])
	p.instances = p.instances[:n]
}
