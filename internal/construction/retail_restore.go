package construction

import "github.com/nanolathe-gg/nanolathe/internal/orders"

// RestoreBuilderLinks rebuilds Nanolathe's progress index after all saved
// queues have been installed. The producer's order target is the source;
// retail has no separate auxiliary builder link to serialize. Carrier and
// GetBuilt references have different lifetimes and cannot replace that target
// [08 R-SAVE-02 §11][05 "Build request and factory queue behavior"].
func (s *Service) RestoreBuilderLinks() {
	if s == nil {
		return
	}
	clear(s.builderLinks)
	if s.World == nil {
		return
	}
	for _, builder := range s.World.Iter() {
		q := orders.QueueOfUnit(builder)
		if q == nil {
			continue
		}
		for _, segment := range [][]*orders.Node{q.Primary(), q.Secondary()} {
			for _, node := range segment {
				if node == nil || !isBuildOrderID(node.ID) || node.Target == 0 {
					continue
				}
				product := s.World.Unit(node.Target)
				if product != nil && product.Alive {
					s.SetBuilderLink(product.Handle, builder.Handle)
				}
			}
		}
	}
}
