package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"testing"
)

func TestModernInstalledTrajectoryAdmissionRetail(t *testing.T) {
	catalog, _ := retailcat.Shared(t)
	for _, name := range []string{"armbrtha", "corint", "corrl", "armrl", "armjeth", "corcrash", "armrock"} {
		t.Run(name, func(t *testing.T) {
			unit := catalog.Units[name]
			if unit == nil || unit.Weapon1Def == nil {
				t.Fatal("installed primary weapon missing")
			}
			_, terrain := newContactFixture(t)
			terrain.PlotAt(2, 1).SetMinHeight(64)
			terrain.PlotAt(3, 1).SetMinHeight(64)
			muzzle, aim := modernTerrainPoints()
			muzzle.X = numeric.FixedFromInt(31)
			launch := Slot{Weapon: unit.Weapon1Def, DesiredYaw: retailYawFromGo(16384), DesiredPitch: 8192}
			if got := modernTerrainAdmission(launch, muzzle, aim, 10, terrain, nil, &world.Wind{}); got != terrainShotBlocked {
				t.Fatalf("installed nearby ridge admission=%v", got)
			}
		})
	}
}
