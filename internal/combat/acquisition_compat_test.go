package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// SlotAcquisitionAdmits is the Strict-compatible free form retained for tools
// and fixtures that have no combat service. A composed session binds the
// service method above so Community feature state reaches the shared gate.
func SlotAcquisitionAdmits(u *units.Unit, idx int, cand *units.Unit, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog) bool {
	return (*Service)(nil).SlotAcquisitionAdmits(u, idx, cand, w, vis, terrain, econ, catalog)
}
