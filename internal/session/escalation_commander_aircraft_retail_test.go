//go:build retail

package session

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func loadEscalationCommanderAircraft(t *testing.T) (vfs.FSOps, *content.Catalog, content.Limits, profiles.Profile) {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("NANOLATHE_MOD_ROOTS_ESCALATION"))
	if value == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_ESCALATION is unset: Gold 10.2.0 commander/aircraft test needs authored content")
	}
	roots := []string{testsupport.RetailRoot(t)}
	for _, root := range strings.Split(value, string(os.PathListSeparator)) {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountGameDirectories(roots); err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Resolve(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "escalation" {
		t.Fatalf("profile = %s", profile.Name)
	}
	view := profile.Layout().Apply(fs)
	limits := content.LimitsFromProfile(profile.Limits)
	cat, err := content.CompileWithOptions(view, content.Options{Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	return view, cat, limits, profile
}

func newEscalationCommanderAircraft(t *testing.T, fs vfs.FSOps, cat *content.Catalog, limits content.Limits, profile profiles.Profile) *Session {
	t.Helper()
	cfg := DirectSkirmishConfig("ashap plateau")
	cfg.ApplyDefaults()
	cfg.Gameplay = gameplay.Modern
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
	limit := 100
	s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{ContentLimits: limits, CommunitySources: CommunitySources{Content: profile.GameplaySources(), Player: community.Overrides{UnitLimit: &limit}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range s.AI {
		if a != nil {
			for j := range a.Deadlines {
				a.Deadlines[j] = ^uint32(0)
			}
		}
	}
	return s
}

// These are authored-program acceptance checks, not substitute commander or
// aircraft mechanics [research/extensions/escalation-commander-aircraft.md].
func TestEscalationCommanderResearch(t *testing.T) {
	fs, cat, limits, profile := loadEscalationCommanderAircraft(t)
	for _, tc := range []struct{ commander, tech, other string }{{"ARMCOM", "ARMTECH", "CORTECH"}, {"CORCOM", "CORTECH", "ARMTECH"}} {
		t.Run(tc.commander, func(t *testing.T) {
			s := newEscalationCommanderAircraft(t, fs, cat, limits, profile)
			c := placeCompleteRetailUnit(t, s, tc.commander, 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
			s.Econ.Players[0].Allies[1], s.Econ.Players[1].Allies[0] = true, true
			placeCompleteRetailUnit(t, s, tc.other, 0, numeric.FixedFromInt(1400), numeric.FixedFromInt(1400))
			placeCompleteRetailUnit(t, s, tc.tech, 1, numeric.FixedFromInt(1600), numeric.FixedFromInt(1400))
			def, _ := cat.Unit(tc.tech)
			h, err := s.Units.CreateNanoframe(def, 0, numeric.FixedFromInt(1200), s.World.HeightAt(numeric.FixedFromInt(1200), numeric.FixedFromInt(1200)), numeric.FixedFromInt(1200))
			if err != nil {
				t.Fatal(err)
			}
			unfinished := s.Units.Unit(h)
			if unfinished == nil || unfinished.Remaining == 0 {
				t.Fatal("research fixture was not an unfinished frame")
			}
			stepEscalationCA(t, s, 180)
			if !unfinished.Alive || unfinished.Remaining == 0 {
				t.Fatal("unfinished research did not remain available to the commander's scan")
			}
			if escalationCAPiece(t, c, "pelvis2") || !escalationCAPiece(t, c, "pelvis") {
				t.Fatal("wrong-faction, allied or unfinished research upgraded the commander")
			}

			// The ordinary first hit consumes kinetic armor; a subsequent hit during
			// its authored five-second interval is unarmored.
			assertEscalationCAHit(t, s, c, 25)
			stepEscalationCA(t, s, 2)
			if c.Armored {
				t.Fatal("unupgraded commander retained kinetic armor after a hit")
			}
			assertEscalationCAHit(t, s, c, 100)
			stepEscalationCA(t, s, 180)
			if !c.Armored {
				t.Fatal("unupgraded kinetic armor did not recover")
			}

			// An ordinary attack reaches the actual AimSecondary wait. A completed
			// own research building releases that same authored weapon gate.
			s.Econ.Players[0].Allies[1], s.Econ.Players[1].Allies[0] = false, false
			target := placeCompleteRetailUnit(t, s, "CORFUS", 1, numeric.FixedFromInt(600), numeric.FixedFromInt(800))
			s.Econ.Players[0].Stock[economy.Energy] = 10000
			if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{c.Handle}, Code: 3, Target: target.Handle, Position: orders.ResolvePos{X: target.X, Y: target.Y, Z: target.Z}}}); err != nil {
				t.Fatal(err)
			}
			stepEscalationCA(t, s, 60)
			if c.SlotAt(1).Reload != 0 || c.SlotAt(1).Aim.Ready {
				t.Fatal("unupgraded secondary weapon passed its script gate")
			}
			first := placeCompleteRetailUnit(t, s, tc.tech, 0, numeric.FixedFromInt(1200), numeric.FixedFromInt(1600))
			fired := false
			for i := 0; i < 240; i++ {
				stepEscalationCA(t, s, 1)
				if c.SlotAt(1).Reload > 0 {
					fired = true
					break
				}
			}
			if !fired || !escalationCAPiece(t, c, "pelvis2") || escalationCAPiece(t, c, "pelvis") {
				t.Fatal("completed own research did not switch geometry and release the secondary weapon")
			}
			if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{c.Handle}}}); err != nil {
				t.Fatal(err)
			}
			assertEscalationCAHit(t, s, c, 25)
			stepEscalationCA(t, s, 2)
			if !c.Armored {
				t.Fatal("upgraded commander lost armor to the baseline kinetic-hit branch")
			}

			// Two research sources maintain the same upgraded branch. Removing only
			// one preserves it; removing the final source restores the basic model.
			second := placeCompleteRetailUnit(t, s, tc.tech, 0, numeric.FixedFromInt(1400), numeric.FixedFromInt(1200))
			stepEscalationCA(t, s, 180)
			reclaimEscalationCA(t, s, first)
			stepEscalationCA(t, s, 180)
			if !escalationCAPiece(t, c, "pelvis2") {
				t.Fatal("removing one research source removed the remaining upgrade")
			}
			reclaimEscalationCA(t, s, second)
			stepEscalationCA(t, s, 180)
			if escalationCAPiece(t, c, "pelvis2") || !escalationCAPiece(t, c, "pelvis") {
				t.Fatal("removing the final own research source did not restore the base model")
			}
			if diags := c.Script.Diagnostics(); len(diags) != 0 {
				t.Fatalf("commander script diagnostics: %v", diags)
			}
		})
	}
}

func TestEscalationAtlasStackAndRecovery(t *testing.T) {
	fs, cat, limits, profile := loadEscalationCommanderAircraft(t)
	s := newEscalationCommanderAircraft(t, fs, cat, limits, profile)
	a := placeCompleteRetailUnit(t, s, "ARMATLAS", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
	b := placeCompleteRetailUnit(t, s, "ARMATLAS", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
	for _, u := range []*units.Unit{a, b} {
		moveEscalationCA(t, s, u, 1100, 600)
	}
	stepEscalationCA(t, s, 120)
	for _, u := range []*units.Unit{a, b} {
		if u.Armored || !escalationCAPiece(t, u, "stacker") {
			t.Fatal("coincident Atlas transports did not enter the authored stack penalty")
		}
		assertEscalationCAHit(t, s, u, 100)
	}
	moveEscalationCA(t, s, b, 600, 1100)
	stepEscalationCA(t, s, 300)
	for _, u := range []*units.Unit{a, b} {
		if !u.Armored || escalationCAPiece(t, u, "stacker") {
			t.Fatal("separated Atlas did not regain armor at its next scan")
		}
		assertEscalationCAHit(t, s, u, 25)
		if diags := u.Script.Diagnostics(); len(diags) != 0 {
			t.Fatalf("Atlas script diagnostics: %v", diags)
		}
	}
}

func TestEscalationAtlasOffMapCargoAndSave(t *testing.T) {
	fs, cat, limits, profile := loadEscalationCommanderAircraft(t)
	s := newEscalationCommanderAircraft(t, fs, cat, limits, profile)
	a := placeCompleteRetailUnit(t, s, "ARMATLAS", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
	cargo := placeCompleteRetailUnit(t, s, "ARMFLASH", 0, numeric.FixedFromInt(650), numeric.FixedFromInt(600))
	setEscalationCAStance(t, s, a, 0)
	stepEscalationCA(t, s, 300)
	if cargo.Attachment.Carrier != a.Handle {
		t.Fatal("Atlas did not load the nearby own unit through its authored automatic mode")
	}
	moveEscalationCA(t, s, a, -200, 600)
	stepEscalationCA(t, s, 600)
	if a.X >= 0 || cargo.Attachment.Carrier != a.Handle {
		t.Fatal("Atlas did not carry its cargo outside the map")
	}
	moveEscalationCA(t, s, a, 600, 600)
	stepEscalationCA(t, s, 300)
	if a.X <= numeric.FixedFromInt(500) || !escalationCAPiece(t, a, "naughty") {
		t.Fatal("returning Atlas did not retain the authored penalty indicator")
	}
	// The selection command requires an on-map unit. Set unload mode once
	// back inside; the existing penalty must still prevent the script's drop.
	setEscalationCAStance(t, s, a, 1)
	stepEscalationCA(t, s, 30)
	if cargo.Attachment.Carrier != a.Handle || !escalationCAPiece(t, a, "naughty") {
		t.Fatal("cargo unloaded before off-map penalty recovery")
	}

	summary := RetailBattleSummary(s, "Escalation aircraft acceptance", "0", s.Skirmish.UnitLimit)
	inputs, err := s.RetailBattleSaveInputs(summary, save.Camera{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "AIRCRAFT.SAV")
	if err := s.WriteRetailSave(path, inputs); err != nil {
		t.Fatal(err)
	}
	limit := 100
	loaded, err := LoadRetailSavePath(path, RetailLoadDeps{FS: fs, Catalog: cat, ContentLimits: limits, CommunitySources: CommunitySources{Content: profile.GameplaySources(), Player: community.Overrides{UnitLimit: &limit}}, Gameplay: gameplay.Modern, SimSeed: 7, CRTSeed: 7, UnitLimit: s.Skirmish.UnitLimit})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Battle == nil || loaded.Battle.Session == nil {
		t.Fatal("aircraft save did not restore a battle")
	}
	dst := loaded.Battle.Session
	restored := dst.Units.Unit(loaded.Battle.StableUnit[uint16(a.Handle)])
	restoredCargo := dst.Units.Unit(loaded.Battle.StableUnit[uint16(cargo.Handle)])
	if restored == nil || restoredCargo == nil || restoredCargo.Attachment.Carrier != restored.Handle {
		t.Fatal("restored Atlas lost its cargo")
	}
	before, err := cob.RetailScriptImage(a.Script)
	if err != nil {
		t.Fatal(err)
	}
	after, err := cob.RetailScriptImage(restored.Script)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("active off-map penalty script changed across save/load")
	}
	heldWithIndicator := false
	for i := 0; i < 900; i++ {
		stepEscalationCA(t, dst, 1)
		indicator := escalationCAPiece(t, restored, "naughty")
		if indicator {
			heldWithIndicator = true
			if restoredCargo.Attachment.Carrier != restored.Handle {
				t.Fatal("restored Atlas unloaded during its active penalty")
			}
		}
		if restoredCargo.Attachment.Carrier == 0 {
			if !heldWithIndicator || indicator {
				t.Fatal("cargo release did not follow the visible penalty's recovery")
			}
			if diags := restored.Script.Diagnostics(); len(diags) != 0 {
				t.Fatalf("restored Atlas script diagnostics: %v", diags)
			}
			return
		}
	}
	t.Fatal("restored Atlas did not unload after off-map penalty recovery")
}

func stepEscalationCA(t *testing.T, s *Session, ticks int) {
	t.Helper()
	advanced := 0
	for dispatch := 0; advanced < ticks && dispatch < ticks+8; dispatch++ {
		before := s.Clock.GlobalTick
		s.Step(s.Clock.ScaledAnchor + 1)
		advanced += int(s.Clock.GlobalTick - before)
	}
	if advanced != ticks {
		t.Fatalf("advanced %d commander/aircraft ticks, want %d", advanced, ticks)
	}
}
func escalationCAPiece(t *testing.T, u *units.Unit, name string) bool {
	t.Helper()
	for i, n := range u.Script.Program().Pieces {
		if n == name {
			return u.Script.RenderPieceFlags()[i]&1 != 0
		}
	}
	t.Fatalf("%s has no authored piece %s", u.Def.UnitName, name)
	return false
}
func assertEscalationCAHit(t *testing.T, s *Session, u *units.Unit, want uint16) {
	t.Helper()
	before := u.Health
	r := s.acceptDamage(s.Clock.GlobalTick, combat.DamageInput{Victim: u.Handle, Nominal: 100, Kind: combat.KindOrdinary})
	if !r.Accepted || r.Amount != want || u.Health != before-int32(want) {
		t.Fatalf("%s ordinary hit=%+v health %d -> %d, want %d", u.Def.UnitName, r, before, u.Health, want)
	}
}
func reclaimEscalationCA(t *testing.T, s *Session, u *units.Unit) {
	t.Helper()
	if r := s.acceptDamage(s.Clock.GlobalTick, combat.DamageInput{Victim: u.Handle, Nominal: 30000, Kind: 5}); !r.DeathLatched {
		t.Fatal("research reclaim did not latch removal")
	}
}
func moveEscalationCA(t *testing.T, s *Session, u *units.Unit, x, z int64) {
	t.Helper()
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{u.Handle}, Code: 2, AssignedPosition: true, Position: orders.ResolvePos{X: numeric.FixedFromInt(x), Z: numeric.FixedFromInt(z), InterfaceType: orders.InterfaceTypeRightClick}}}); err != nil {
		t.Fatal(err)
	}
}
func setEscalationCAStance(t *testing.T, s *Session, u *units.Unit, value int32) {
	t.Helper()
	for _, cmd := range []HumanCommand{{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{u.Handle}}}, {Kind: HumanStance, Stance: HumanStanceCommand{Fire: true, Value: value}}} {
		if err := s.EnqueueHumanCommand(cmd); err != nil {
			t.Fatal(err)
		}
	}
}
