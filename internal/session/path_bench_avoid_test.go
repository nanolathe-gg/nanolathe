//go:build pathbench && retail

package session

import (
	"fmt"
	"testing"
)

// Same-owner opposing traffic: the friendly counterpart of the traffic/choke
// family, where the other side is a different, non-allied owner.
func init() {
	pbRegister(
		pbCase{"avoid/headon_friendly", "avoid", "Two same-owner groups swap sides in open ground", []int{16, 64}, 900, pbHeadonFriendly},
		pbCase{"avoid/choke_wide_friendly", "avoid", "Same-owner opposing groups through an eight-cell aperture", []int{16, 64}, 900, pbChokeWideFriendly},
		pbCase{"avoid/choke_two_friendly", "avoid", "Same-owner opposing groups through a four-cell aperture", []int{16, 64}, 1200, pbChokeTwoFriendly},
		pbCase{"avoid/mixed_headon_friendly", "avoid", "Mixed same-owner profiles swap sides in open ground", []int{32}, 1100, pbMixedHeadonFriendly},
		pbCase{"avoid/through_idle_army", "avoid", "A moving group crosses an idle friendly army block", []int{16, 64}, 900, pbThroughIdleArmy},
		pbCase{"avoid/headon_enemy", "avoid", "Two hostile owners' groups swap sides in open ground", []int{16, 64}, 900, pbHeadonEnemy},
		pbCase{"avoid/open_big_a", "avoid", "Large open group, start offset A", []int{128, 192, 256}, 700, func(t *testing.T, r string, n int) *pbScene { return pbOpenBig(t, r, n, 8, 8, 102, 48, 16) }},
		pbCase{"avoid/open_big_b", "avoid", "Large open group, start offset B", []int{128, 192, 256}, 700, func(t *testing.T, r string, n int) *pbScene { return pbOpenBig(t, r, n, 10, 20, 100, 60, 12) }},
		pbCase{"avoid/open_big_c", "avoid", "Large open group, diagonal", []int{128, 192, 256}, 700, func(t *testing.T, r string, n int) *pbScene { return pbOpenBig(t, r, n, 8, 60, 96, 20, 20) }},
	)
}

func pbFriendlyOpposing(t *testing.T, rules string, size int, gap0, gap1 int32, walls bool) *pbScene {
	t.Helper()
	pbCheckSize(t, size)
	ter := pbTerrain(t, 128, 96, 20, 0)
	if walls {
		pbWall(ter, 63, 0, 65, gap0)
		pbWall(ter, 63, gap1, 65, 96)
	}
	sc := pbNew(t, rules, ter)
	left := pbGroundGrid(t, sc, "armflea", "east", 0, size/2, 12, 42, 8, 2)
	right := pbGroundGrid(t, sc, "armflea", "west", 0, size-size/2, 100, 42, 8, 2)
	pbMove(t, sc, left, 110, 48, false, false)
	pbMove(t, sc, right, 14, 48, false, false)
	if walls {
		sc.Regions = append(sc.Regions, pbRegion{"aperture", 63, gap0, 65, gap1})
	}
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("owner0 both; walls=%t aperture z=[%d,%d); left (12,42) -> (110,48), right (100,42) -> (14,48); size=%d", walls, gap0, gap1, size))
	return sc
}

func pbHeadonFriendly(t *testing.T, rules string, size int) *pbScene {
	return pbFriendlyOpposing(t, rules, size, 0, 0, false)
}
func pbChokeWideFriendly(t *testing.T, rules string, size int) *pbScene {
	return pbFriendlyOpposing(t, rules, size, 44, 52, true)
}
func pbChokeTwoFriendly(t *testing.T, rules string, size int) *pbScene {
	return pbFriendlyOpposing(t, rules, size, 46, 50, true)
}

func pbMixedHeadonFriendly(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	keys := []string{"armflea", "armflash", "armstump", "armck"}
	var left, right []*pbActor
	for i := 0; i < size/2; i++ {
		left = append(left, pbAdd(t, sc, keys[i%4], "east", 0, 10+int32(i%4)*4, 36+int32(i/4)*4))
		right = append(right, pbAdd(t, sc, keys[(i+2)%4], "west", 0, 104+int32(i%4)*4, 36+int32(i/4)*4))
	}
	pbMove(t, sc, left, 110, 48, false, false)
	pbMove(t, sc, right, 14, 48, false, false)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("owner0 mixed flea/flash/stump/ck, stride4 cols4; left (10,36)->(110,48), right (104,36)->(14,48); size=%d", size))
	return sc
}

func pbThroughIdleArmy(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	for z := int32(34); z <= 60; z += 2 {
		for x := int32(56); x <= 70; x += 2 {
			pbAdd(t, sc, "armflea", "idle_army", 0, x, z)
		}
	}
	movers := pbGroundGrid(t, sc, "armflea", "crossing", 0, size, 10, 40, 8, 2)
	pbMove(t, sc, movers, 110, 48, false, false)
	sc.Regions = append(sc.Regions, pbRegion{"army", 56, 34, 72, 62})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("112 idle owner0 fleas x=56..70,z=34..60 stride2; %d movers (10,40) -> (110,48)", size))
	return sc
}

func pbOpenBig(t *testing.T, rules string, size int, x0, z0, gx, gz, cols int32) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	a := pbGroundGrid(t, sc, "armflea", "big", 0, size, x0, z0, cols, 2)
	pbMove(t, sc, a, gx, gz, false, false)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("open big start (%d,%d) cols=%d -> (%d,%d) count=%d", x0, z0, cols, gx, gz, size))
	return sc
}

// pbHeadonEnemy is the open head-on case between owners 0 and 1 with their
// alliance rows cleared, so no friendly rule may apply between the groups.
func pbHeadonEnemy(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	for i := range sc.S.Econ.Players {
		for j := range sc.S.Econ.Players[i].Allies {
			sc.S.Econ.Players[i].Allies[j] = i == j
		}
	}
	left := pbGroundGrid(t, sc, "armflea", "east", 0, size/2, 12, 42, 8, 2)
	right := pbGroundGrid(t, sc, "armflea", "west", 1, size-size/2, 100, 42, 8, 2)
	pbMove(t, sc, left, 110, 48, false, false)
	pbScriptedOwnerMove(t, sc, right, 14, 48)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("owners 0 and 1 hostile (alliance rows cleared); left (12,42)->(110,48), right (100,42)->(14,48); size=%d", size))
	return sc
}
