package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/units"
)

func TestDescriptorTable(t *testing.T) {
	tbl := Table()
	if len(tbl) != 68 {
		t.Fatalf("table len %d, want 68", len(tbl))
	}
	if tbl[0].Name != "" {
		t.Fatalf("index 0 name %q, want empty sentinel", tbl[0].Name)
	}
	// Spot-check five IDs against [04 §3.1] table.
	checks := map[string]int{
		"Move_Ground":  -1, // we don't hardcode index, just lookup
		"Attack_Chase": -1,
		"SelfDestruct": -1,
		"Patrol":       -1,
		"Wait":         -1,
	}
	for name := range checks {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("Lookup %q returned 0 sentinel", name)
		}
		if DescriptorFor(id).Name != name {
			t.Fatalf("DescriptorFor Lookup %q got %q", name, DescriptorFor(id).Name)
		}
	}
	// The table is sorted with the C runtime's case-insensitive compare, the
	// same comparator Lookup searches with [04 R-STANCE-01 §9].
	for i := 1; i < len(tbl); i++ {
		if foldCompare(tbl[i-1].Name, tbl[i].Name) >= 0 {
			t.Fatalf("table not sorted at %d: %q >= %q", i-1, tbl[i-1].Name, tbl[i].Name)
		}
	}
	// Gate mask spot-check
	if DescriptorFor(Lookup("Move_Ground")).StaticGate != 0x402 {
		t.Fatalf("Move_Ground gate %x, want 0x402", DescriptorFor(Lookup("Move_Ground")).StaticGate)
	}
	if DescriptorFor(Lookup("SelfDestruct")).StaticGate != 0x40040 {
		t.Fatalf("SelfDestruct gate %x, want 0x40040", DescriptorFor(Lookup("SelfDestruct")).StaticGate)
	}
}

// wantRow is one transcribed descriptor row [04 §3.1][R-DOC04-C].
type wantRow struct {
	name  string
	label string
	class uint8
	ack   uint8
	gate  uint32
	pres  PresentationHelper
}

// researchRows is the full 68-row transcription in the documented sorted
// order: the empty sentinel at index 0, then the 67 named commands exactly as
// tabulated [04 §3.1]. The presentation identities follow the [R-DOC04-C]
// audit resolution of the four helper identities per record.
//
// Seven rows sit where [04 R-STANCE-01 §9] places them rather than where
// §3.1's own table does: identities 6-10 are Attack_Chase, Attack_Kamikaze,
// Attack_NoMove, AttackSpecial, AttackUType and 12-13 are BuildingBuild,
// BuildWeapon. §3.1 recomputed its order from a case-sensitive byte sort; the
// registration routine was later traced sorting with the case-insensitive
// compare, under which the underscore sorts below every letter. Per-row
// payloads are untouched — only the order of those seven names moved.
var researchRows = []wantRow{
	{"", "", 0x00, 0, 0x0000000, HelperNone},
	{"Activate", "Activate", 0x00, 19, 0x10060, HelperNone},
	{"AirStrike", "Airstrike", 0x08, 2, 0x600, HelperGoalResolveAck},
	{"AirToAir", "Engaging target", 0x08, 1, 0x200, HelperGoalResolveAck},
	{"AirToGround", "Engaging target", 0x08, 1, 0x200, HelperGoalResolveAck},
	{"AirToGroundHover", "Engaging target", 0x08, 1, 0x200, HelperGoalResolveAck},
	{"Attack_Chase", "Attacking", 0x08, 1, 0x280, HelperGoalResolveAck},
	{"Attack_Kamikaze", "Attacking", 0x08, 1, 0x600, HelperGoalResolveAck},
	{"Attack_NoMove", "Attacking", 0x08, 1, 0x280, HelperGoalResolveAck},
	{"AttackSpecial", "Annihilating", 0x08, 1, 0x680, HelperGoalResolveAck},
	{"AttackUType", "Attacking", 0x00, 19, 0x4, HelperNone},
	{"BeCarried", "Being transported", 0x00, 19, 0x24, HelperNone},
	{"BuildingBuild", "Nanolathing", 0x00, 19, 0x10010c, HelperNone},
	{"BuildWeapon", "Nanolathing", 0x00, 19, 0xc0140, HelperNone},
	{"Capture", "Capturing", 0x08, 4, 0x200, HelperGoalResolveAck},
	{"Cloak_Off", "Decloaking", 0x00, 19, 0x10060, HelperNone},
	{"Cloak_On", "Cloaking", 0x00, 19, 0x10060, HelperNone},
	{"Deactivate", "Deactivate", 0x00, 19, 0x10060, HelperNone},
	{"Follow_Ground", "Guarding", 0x12, 5, 0x200, HelperGoalResolveAckPathMarkers},
	{"GetBuilt", "Under construction", 0x00, 19, 0x224, HelperNone},
	{"Ground_Pickup", "Loading", 0x08, 12, 0x200, HelperGoalResolveAck},
	{"Ground_Unload", "Unloading", 0x08, 13, 0x400, HelperGoalResolveAck},
	{"Guard_NoMove", "Ready", 0x00, 19, 0x20, HelperNone},
	{"HelpBuild", "Nanolathing", 0x18, 6, 0x100208, HelperGoalResolveAck},
	{"MakeSelectable", "Unit is available", 0x00, 19, 0x4, HelperNone},
	{"MobileBuild", "Nanolathing", 0x13, 0, 0x100508, HelperBuildFootprint},
	{"Move_Ground", "Moving", 0x12, 14, 0x402, HelperGoalResolveAckPathMarkers},
	{"Paralyze", "Paralyzed", 0x00, 19, 0x24, HelperNone},
	{"Park", "Parking", 0x00, 14, 0x0, HelperNone},
	{"Patrol", "Patrolling", 0x12, 7, 0x412, HelperGoalResolveAckPathMarkers},
	{"QMove", "Ready with orders", 0x02, 14, 0x400, HelperGoalResolveAckPathMarkers},
	{"QPatrol", "Ready with orders", 0x02, 7, 0x400, HelperGoalResolveAckPathMarkers},
	{"Reclaim", "Reclaiming", 0x12, 11, 0x100800, HelperGoalResolveAckPathMarkers},
	{"ReclaimUnit", "Reclaiming", 0x12, 11, 0x100200, HelperGoalResolveAckPathMarkers},
	{"RepairPatrol", "Repair patrol", 0x12, 7, 0x412, HelperGoalResolveAckPathMarkers},
	{"RepairUnit", "Repairing", 0x12, 6, 0x100200, HelperGoalResolveAckPathMarkers},
	{"RepairUnitNoMove", "Repairing", 0x18, 6, 0x200, HelperGoalResolveAck},
	{"Resurrect", "Resurrecting", 0x12, 11, 0x200, HelperGoalResolveAckPathMarkers},
	{"SelfDestruct", "SELF DESTRUCT ENGAGED", 0x00, 19, 0x40040, HelperNone},
	{"SelfDestructFG", "SELF DESTRUCT ENGAGED", 0x00, 19, 0x0, HelperNone},
	{"SelfRepair", "Repairing", 0x00, 19, 0x1000204, HelperNone},
	{"Standby", "Standby", 0x10, 15, 0x20000, HelperNone},
	{"Standby_Mine", "Standby", 0x10, 15, 0x1020000, HelperNone},
	{"Standing_FireOrder", "Acknowledged", 0x00, 19, 0x10060, HelperNone},
	{"Standing_MoveOrder", "Acknowledged", 0x00, 19, 0x10060, HelperNone},
	{"Stop", "Stopping", 0x00, 19, 0x0, HelperNone},
	{"Suppress", "Suppressing fire", 0x08, 1, 0x410, HelperGoalResolveAck},
	{"Teleport", "Teleporting", 0x08, 9, 0x600, HelperGoalResolveAck},
	{"VTOL_Evade", "Evading", 0x00, 19, 0x0, HelperNone},
	{"VTOL_Follow", "Guarding", 0x02, 5, 0x200, HelperGoalResolveAckPathMarkers},
	{"VTOL_GetRepaired", "Under repair", 0x00, 19, 0x200, HelperNone},
	{"VTOL_HelpBuild", "Nanolathing", 0x08, 6, 0x100208, HelperGoalResolveAck},
	{"VTOL_LandIfCan", "Seeking to land", 0x00, 19, 0x400, HelperNone},
	{"VTOL_Landing", "Landing", 0x08, 14, 0x600, HelperGoalResolveAck},
	{"VTOL_MobileBuild", "Nanolathing", 0x03, 0, 0x100508, HelperBuildFootprint},
	{"VTOL_Move", "Moving", 0x02, 14, 0x402, HelperGoalResolveAckPathMarkers},
	{"VTOL_Patrol", "Patrolling", 0x02, 7, 0x412, HelperGoalResolveAckPathMarkers},
	{"VTOL_Pickup", "Loading", 0x08, 8, 0x200, HelperGoalResolveAck},
	{"VTOL_Reclaim", "Reclaiming", 0x02, 11, 0x100800, HelperGoalResolveAckPathMarkers},
	{"VTOL_ReclaimUnit", "Reclaiming", 0x02, 11, 0x100200, HelperGoalResolveAckPathMarkers},
	{"VTOL_RepairPatrol", "Repair patrol", 0x02, 7, 0x412, HelperGoalResolveAckPathMarkers},
	{"VTOL_RepairUnit", "Repairing", 0x02, 6, 0x100200, HelperGoalResolveAckPathMarkers},
	{"VTOL_SeekAttack", "Seeking to attack", 0x00, 19, 0x600, HelperNone},
	{"VTOL_SeekGuard", "Seeking to guard", 0x00, 19, 0x600, HelperNone},
	{"VTOL_Standby", "Standby", 0x00, 15, 0x20000, HelperNone},
	{"VTOL_Unload", "Unloading", 0x08, 9, 0x400, HelperGoalResolveAck},
	{"Wait", "Waiting", 0x00, 19, 0x4, HelperNone},
	{"WaitForAttack", "Waiting for attack", 0x00, 19, 0x204, HelperNone},
}

// TestDescriptorTableMatchesResearch locks the full row-for-row transcription
// of the descriptor table against [04 §3.1] and the [R-DOC04-C] presentation
// resolution. Any divergence is a transcription bug or an intentional
// correction that must come with a research-doc change first.
func TestDescriptorTableMatchesResearch(t *testing.T) {
	tbl := Table()
	if len(researchRows) != 68 {
		t.Fatalf("transcription has %d rows, want 68", len(researchRows))
	}
	if len(tbl) != len(researchRows) {
		t.Fatalf("table len %d, want %d", len(tbl), len(researchRows))
	}
	for i, w := range researchRows {
		d := tbl[i]
		if d.Name != w.name {
			t.Fatalf("row %d name %q, want %q", i, d.Name, w.name)
		}
		if d.StateLabel != w.label {
			t.Fatalf("row %d (%s) label %q, want %q", i, w.name, d.StateLabel, w.label)
		}
		if d.Class != w.class {
			t.Fatalf("row %d (%s) class %#x, want %#x", i, w.name, d.Class, w.class)
		}
		if d.AckGroup != w.ack {
			t.Fatalf("row %d (%s) ack group %d, want %d", i, w.name, d.AckGroup, w.ack)
		}
		if d.StaticGate != w.gate {
			t.Fatalf("row %d (%s) gate mask %#x, want %#x", i, w.name, d.StaticGate, w.gate)
		}
		if d.Presentation != w.pres {
			t.Fatalf("row %d (%s) presentation %d, want %d", i, w.name, d.Presentation, w.pres)
		}
	}
}

// TestDescriptorBatchRegistration locks the four static batches (23/22/22/1)
// to their compiled registration order [R-DOC04-C]; buildTable re-sorts after
// every batch, so this order does not change identities — it is the
// transcription contract itself [04 §3.1] C4.
func TestDescriptorBatchRegistration(t *testing.T) {
	batches := [][]Descriptor{batch1, batch2, batch3, batch4}
	wantLens := []int{23, 22, 22, 1}
	for i, names := range [][]string{
		{"Stop", "Attack_NoMove", "Activate", "Deactivate", "Cloak_On", "Cloak_Off",
			"Standing_MoveOrder", "Standing_FireOrder", "BuildingBuild", "BuildWeapon",
			"SelfDestruct", "SelfDestructFG", "Paralyze", "GetBuilt", "BeCarried",
			"MakeSelectable", "Wait", "WaitForAttack", "AttackUType", "Guard_NoMove",
			"SelfRepair", "QMove", "QPatrol"},
		{"Standby", "Standby_Mine", "Move_Ground", "Follow_Ground", "Suppress",
			"Attack_Chase", "Attack_Kamikaze", "AttackSpecial", "Park", "Patrol",
			"Ground_Pickup", "Ground_Unload", "Teleport", "MobileBuild", "HelpBuild",
			"RepairPatrol", "RepairUnit", "Capture", "Resurrect", "Reclaim",
			"ReclaimUnit", "RepairUnitNoMove"},
		{"VTOL_Standby", "VTOL_Move", "VTOL_Landing", "VTOL_Pickup", "VTOL_Unload",
			"VTOL_Follow", "VTOL_Patrol", "AirStrike", "AirToAir", "AirToGround",
			"AirToGroundHover", "VTOL_MobileBuild", "VTOL_HelpBuild", "VTOL_RepairPatrol",
			"VTOL_RepairUnit", "VTOL_Reclaim", "VTOL_ReclaimUnit", "VTOL_Evade",
			"VTOL_SeekAttack", "VTOL_SeekGuard", "VTOL_GetRepaired", "VTOL_LandIfCan"},
		{""},
	} {
		if len(batches[i]) != wantLens[i] {
			t.Fatalf("batch %d has %d records, want %d", i+1, len(batches[i]), wantLens[i])
		}
		if len(names) != wantLens[i] {
			t.Fatalf("transcription batch %d has %d names, want %d", i+1, len(names), wantLens[i])
		}
		for j, name := range names {
			if batches[i][j].Name != name {
				t.Fatalf("batch %d record %d name %q, want %q", i+1, j, batches[i][j].Name, name)
			}
		}
	}
}

// TestPresentationHelperIdentities locks the [R-DOC04-C] helper-identity
// relationships: the build-footprint marker is carried by MobileBuild and
// VTOL_MobileBuild only, and the per-record resolutions that the family names
// alone do not pin down (AttackUType none despite its name; AirStrike and
// VTOL_Landing in the acknowledgement family; RepairUnitNoMove acknowledged
// without path markers while RepairUnit carries them; the queued move/patrol
// variants carry path markers).
func TestPresentationHelperIdentities(t *testing.T) {
	var footprint []string
	for _, d := range Table() {
		if d.Presentation == HelperBuildFootprint {
			footprint = append(footprint, d.Name)
		}
	}
	if len(footprint) != 2 || footprint[0] != "MobileBuild" || footprint[1] != "VTOL_MobileBuild" {
		t.Fatalf("build-footprint helpers %v, want [MobileBuild VTOL_MobileBuild] only [R-DOC04-C]", footprint)
	}
	spots := []struct {
		name string
		want PresentationHelper
	}{
		{"AttackUType", HelperNone},
		{"AirStrike", HelperGoalResolveAck},
		{"VTOL_Landing", HelperGoalResolveAck},
		{"RepairUnitNoMove", HelperGoalResolveAck},
		{"RepairUnit", HelperGoalResolveAckPathMarkers},
		{"QMove", HelperGoalResolveAckPathMarkers},
		{"QPatrol", HelperGoalResolveAckPathMarkers},
	}
	for _, s := range spots {
		if got := DescriptorFor(Lookup(s.name)).Presentation; got != s.want {
			t.Fatalf("%s presentation %d, want %d", s.name, got, s.want)
		}
	}
}

func TestMakeSelectableHandler(t *testing.T) {
	u := &units.Unit{Flags: units.ClassifierEligibleStatus | 0x8000}
	id := Lookup("MakeSelectable")
	if id == 0 || DescriptorFor(id).Handler == nil {
		t.Fatal("MakeSelectable handler is not registered")
	}
	if got := DescriptorFor(id).Handler(u, &Node{}, 0, 0); got != Code(5) {
		t.Fatalf("MakeSelectable returned %d, want 5", got)
	}
	if u.Flags != units.ClassifierEligibleStatus {
		t.Fatalf("MakeSelectable flags %08x, want %08x", u.Flags, units.ClassifierEligibleStatus)
	}
}

// TestLookupIsCaseInsensitive locks the search contract of
// [04 R-STANCE-01 §9]: callers need not match the table's spelling. The
// interface transmits STANDING_FIREORDER and STANDING_MOVEORDER in upper case
// and they must resolve to the descriptors named Standing_FireOrder and
// Standing_MoveOrder.
//
// This also covers the regression the ordering correction fixed. While the
// table was sorted case-sensitively, a case-insensitive lower_bound could not
// find Attack_Chase at all — the chase attack of [04 §3.5] resolved to the
// reject sentinel.
func TestLookupIsCaseInsensitive(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"STANDING_FIREORDER", "Standing_FireOrder"},
		{"STANDING_MOVEORDER", "Standing_MoveOrder"},
		{"attack_chase", "Attack_Chase"},
		{"ATTACK_CHASE", "Attack_Chase"},
		{"Attack_Chase", "Attack_Chase"},
		{"attackspecial", "AttackSpecial"},
		{"BUILDINGBUILD", "BuildingBuild"},
		{"buildweapon", "BuildWeapon"},
		{"move_ground", "Move_Ground"},
	} {
		id := Lookup(tc.query)
		if id == 0 {
			t.Errorf("Lookup(%q) returned the reject sentinel", tc.query)
			continue
		}
		if got := DescriptorFor(id).Name; got != tc.want {
			t.Errorf("Lookup(%q) resolved to %q, want %q", tc.query, got, tc.want)
		}
	}
	// A name in no batch still rejects, and the empty name is the sentinel.
	if id := Lookup("NotACommand"); id != 0 {
		t.Errorf("Lookup of an unknown name returned %d, want the reject sentinel", id)
	}
	if id := Lookup(""); id != 0 {
		t.Errorf("Lookup(\"\") returned %d, want index 0", id)
	}
}

// TestCorrectedDescriptorIdentities pins the seven identities that moved when
// the sort comparator was corrected [04 R-STANCE-01 §9], and the two anchors
// the correction states do not move.
func TestCorrectedDescriptorIdentities(t *testing.T) {
	for id, want := range map[ID]string{
		6:  "Attack_Chase",
		7:  "Attack_Kamikaze",
		8:  "Attack_NoMove",
		9:  "AttackSpecial",
		10: "AttackUType",
		12: "BuildingBuild",
		13: "BuildWeapon",
		// Unmoved anchors: the empty name stays the reject sentinel and
		// GetBuilt stays at 0x13 under either order.
		0:    "",
		0x13: "GetBuilt",
	} {
		if got := DescriptorFor(id).Name; got != want {
			t.Errorf("identity %d is %q, want %q", id, got, want)
		}
	}
}
