package economy

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// gateForTest is the request side of the cloak gate as the session binds it:
// the cloak-REQUESTED status bit set, and the shared reveal/cloak-suppression
// deadline due [05 R-ECO-01 §9][03 R-VIS-01 §6]. The decloak-forced term lives
// on the session's sensor status word and is exercised there.
func gateForTest(tick *uint32) func(*units.Unit) bool {
	return func(u *units.Unit) bool {
		return u != nil && u.IsCloaked && *tick >= u.RevealDeadline
	}
}

// TestInitCloakedUnitIsVisibleUntilItsFirstPaidPass locks the split between the
// two cloak bits [05 R-ECO-01 §9][03 R-VIS-01 §6]. `init_cloaked` seeds the
// REQUEST at construction; only a settlement pass the owner actually paid for
// sets the INSTANCE bit that visibility and targeting read. So the unit is
// visible when it is placed, hidden after its first paid pass, and visible
// again on the first pass its owner cannot pay — while never dropping the
// request.
func TestInitCloakedUnitIsVisibleUntilItsFirstPaidPass(t *testing.T) {
	var svc Service
	svc.Players[0].Stock[Energy] = 10
	w := units.NewSliced(10, nil)
	def := economyFixtureDef(&content.UnitDef{InitCloaked: true, CloakCost: 6})
	def.MaxDamage = 100
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	if !u.IsCloaked {
		t.Fatal("the constructor did not seed the cloak-REQUESTED bit from init_cloaked [05 R-ECO-01 §9]")
	}
	if u.Hidden {
		t.Fatal("a freshly placed init_cloaked unit is hidden before any pass has paid for it [05 R-ECO-01 §9]")
	}

	tick := uint32(0)
	svc.CloakDue = gateForTest(&tick)
	getCost := func(u *units.Unit) float32 { return u.CloakCost() }

	// Pass 1: affordable — 6 of the 10 in stock.
	ApplyCloakDebits(&svc, w, 0, getCost, nil, nil)
	if !u.Hidden {
		t.Fatal("a paid pass did not set the instance cloaked bit [05 R-ECO-01 §9]")
	}
	if svc.Players[0].Stock[Energy] != 4 {
		t.Fatalf("stock after the paid pass = %v want 4", svc.Players[0].Stock[Energy])
	}

	// Pass 2: 4 in stock against an integerized cost of 6 — no partial
	// payment, and the unit shows again while still requesting cloak.
	ApplyCloakDebits(&svc, w, 0, getCost, nil, nil)
	if u.Hidden {
		t.Fatal("an unpayable pass left the unit hidden [05 \"Cloak debit\"] step 6")
	}
	if !u.IsCloaked {
		t.Fatal("an unpayable pass cleared the cloak REQUEST; only Cloak_Off does that [04 R-ORD-01 §2]")
	}
	if svc.Players[0].Stock[Energy] != 4 {
		t.Fatalf("an unpayable pass moved stock: %v want 4", svc.Players[0].Stock[Energy])
	}
}

// TestCloakOffClearsTheRequestAndTheNextPassClearsTheInstanceBit locks the
// order of the two writes. `Cloak_Off` clears state-word bit 11 and nothing
// else [04 R-ORD-01 §2]; the unit stays hidden until its owner's next
// settlement pass finds the gate no longer due and takes the clearing arm of
// the transition — the arm [04 R-ORD-01 §5] requires when it says a builder
// with cloak requested "stays visible" while its reveal stamp is running.
func TestCloakOffClearsTheRequestAndTheNextPassClearsTheInstanceBit(t *testing.T) {
	var svc Service
	svc.Players[0].Stock[Energy] = 100
	w := units.NewSliced(10, nil)
	def := economyFixtureDef(&content.UnitDef{InitCloaked: true, CloakCost: 6})
	def.MaxDamage = 100
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)

	tick := uint32(0)
	svc.CloakDue = gateForTest(&tick)
	getCost := func(u *units.Unit) float32 { return u.CloakCost() }
	ApplyCloakDebits(&svc, w, 0, getCost, nil, nil)
	if !u.Hidden {
		t.Fatal("setup: the paid pass did not hide the unit")
	}

	u.SetCloaked(false)
	if u.IsCloaked {
		t.Fatal("Cloak_Off did not clear the cloak REQUEST [04 R-ORD-01 §2]")
	}
	if !u.Hidden {
		t.Fatal("Cloak_Off cleared the instance bit itself; the handler writes bit 11 and nothing else [04 R-ORD-01 §2]")
	}

	before := svc.Players[0].Stock[Energy]
	ApplyCloakDebits(&svc, w, 0, getCost, nil, nil)
	if u.Hidden {
		t.Fatal("the pass after Cloak_Off did not clear the instance cloaked bit [05 R-ECO-01 §9]")
	}
	if svc.Players[0].Stock[Energy] != before {
		t.Fatalf("a unit with no cloak request was charged: stock %v want %v", svc.Players[0].Stock[Energy], before)
	}

	// The same arm covers the reveal stamp: request it again, pay for it, then
	// stamp a deadline in the future and watch the next pass show the unit
	// without touching the request [04 R-ORD-01 §5][03 R-VIS-01 §6].
	u.SetCloaked(true)
	ApplyCloakDebits(&svc, w, 0, getCost, nil, nil)
	if !u.Hidden {
		t.Fatal("setup: re-requested cloak did not take on a paid pass")
	}
	u.RevealDeadline = tick + 300
	before = svc.Players[0].Stock[Energy]
	ApplyCloakDebits(&svc, w, 0, getCost, nil, nil)
	if u.Hidden {
		t.Fatal("a pass inside the reveal stamp left the unit hidden [04 R-ORD-01 §5]")
	}
	if !u.IsCloaked {
		t.Fatal("a pass inside the reveal stamp cleared the cloak REQUEST")
	}
	if svc.Players[0].Stock[Energy] != before {
		t.Fatalf("a unit inside its reveal stamp was charged: stock %v want %v", svc.Players[0].Stock[Energy], before)
	}
}
