package session

import "github.com/nanolathe-gg/nanolathe/internal/units"

// Presentation caches must retire when a new unit occupies the same slot,
// including replacement between two publications [03 R-COMP-01 §4][I6].
// This is our publication bookkeeping; object references are compared locally
// and only the assigned integer identity crosses into the immutable frame.
type publishedUnitIdentity struct {
	unit *units.Unit
	id   uint64
	seen bool
}

func (p *publicationState) beginUnitIdentities() {
	for i := range p.unitIdentities {
		p.unitIdentities[i].seen = false
	}
}

func (p *publicationState) unitIdentity(u *units.Unit) uint64 {
	if u == nil || u.Handle == 0 {
		return 0
	}
	index := int(u.Handle)
	if index >= len(p.unitIdentities) {
		p.unitIdentities = append(p.unitIdentities, make([]publishedUnitIdentity, index+1-len(p.unitIdentities))...)
	}
	entry := &p.unitIdentities[index]
	if entry.unit != u {
		p.nextUnitIdentity++
		if p.nextUnitIdentity == 0 {
			p.nextUnitIdentity++
		}
		entry.unit, entry.id = u, p.nextUnitIdentity
	}
	entry.seen = true
	return entry.id
}

func (p *publicationState) finishUnitIdentities() {
	for i := range p.unitIdentities {
		if !p.unitIdentities[i].seen {
			// Do not retain retired scripts/models through an empty slot.
			p.unitIdentities[i] = publishedUnitIdentity{}
		}
	}
}
