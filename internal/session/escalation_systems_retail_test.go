//go:build retail

package session

import (
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func loadEscalationSystems(t *testing.T) (vfs.FSOps, *content.Catalog, content.Limits, profiles.Profile) {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("NANOLATHE_MOD_ROOTS_ESCALATION"))
	if value == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_ESCALATION is unset: Gold 10.2.0 system test needs authored content")
	}
	roots := append([]string{testsupport.RetailRoot(t)}, strings.Split(value, string(os.PathListSeparator))...)
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

func escalationSystemSession(t *testing.T) *Session {
	fs, cat, limits, profile := loadEscalationSystems(t)
	cfg := DirectSkirmishConfig("expanded confluence")
	cfg.ApplyDefaults()
	cfg.NumPlayers = 3
	cfg.Players[2] = cfg.Players[1]
	cfg.Gameplay = gameplay.Modern
	cfg.RNGSimSeed = 7
	cfg.RNGCrtSeed = 7
	limit := 100
	s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{ContentLimits: limits, CommunitySources: CommunitySources{Content: profile.GameplaySources(), Player: community.Overrides{UnitLimit: &limit}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range s.AI {
		if a != nil {
			for j := range a.Deadlines {
				a.Deadlines[j] = 1 << 30
			}
		}
	}
	return s
}
func TestEscalationBuildingUpgrade(t *testing.T) {
	s := escalationSystemSession(t)
	f := placeCompleteRetailUnit(t, s, "ARMFUS", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
	advanceProTATicks(t, s, 300)
	if f.Armored {
		t.Fatal("unupgraded isolated fusion is already armored")
	}
	if err := construction.QueueFactoryBuild(f, "ARMFUS_UPGRADE", 1, s.Catalog); err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < 16000; tick++ {
		p := &s.Econ.Players[0]
		p.Stock = [2]float32{1e6, 1e6}
		p.Capacity = [2]float32{1e6, 1e6}
		advanceProTATicks(t, s, 1)
		if f.Armored {
			if len(f.Attachment.Cargo) != 1 {
				t.Fatalf("upgrade not retained as authored cargo: %+v", f.Attachment)
			}
			upgrade := s.Units.Unit(f.Attachment.Cargo[0])
			if upgrade == nil || upgrade.Def.CanonicalKey != "armfus_upgrade" || upgrade.Remaining != 0 || upgrade.Attachment.Carrier != f.Handle {
				t.Fatal("parent retained an incomplete or incorrect upgrade")
			}
			if f.InBuildStance {
				t.Fatal("upgrade did not close the parent build stance")
			}
			for _, u := range []*units.Unit{f, upgrade} {
				if diagnostics := u.GetScript().Diagnostics(); len(diagnostics) != 0 {
					t.Fatal(diagnostics)
				}
			}
			return
		}
	}
	t.Fatalf("fusion upgrade never applied: armor=%v queue=%+v diagnostics=%v", f.Armored, orders.QueueForUnit(f).Head(), f.ScriptState.Binding.VM.Diagnostics())
}
func TestEscalationTeleporter(t *testing.T) {
	s := escalationSystemSession(t)
	source := placeCompleteRetailUnit(t, s, "ARMGATE", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
	dest := placeCompleteRetailUnit(t, s, "ARMGATE", 0, numeric.FixedFromInt(1200), numeric.FixedFromInt(600))
	cargo := placeCompleteRetailUnit(t, s, "ARMFLASH", 0, numeric.FixedFromInt(660), numeric.FixedFromInt(600))
	advanceProTATicks(t, s, 90)
	err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{dest.Handle}, Code: int(input.LatchAttack), Position: orders.ResolvePos{X: source.X, Y: source.Y, Z: source.Z}}})
	if err != nil {
		t.Fatal(err)
	}
	jumped := false
	for tick := 0; tick < 900; tick++ {
		previousX := cargo.X
		s.Econ.Players[0].Stock[economy.Energy] = 1e6
		advanceProTATicks(t, s, 1)
		jumped = jumped || cargo.X-previousX > numeric.FixedFromInt(300)
		if cargo.X > numeric.FixedFromInt(1000) && cargo.Attachment.Carrier == 0 {
			if !jumped || !cargo.Alive || cargo.Owner != dest.Owner || cargo.X <= (source.X+dest.X)/2 {
				t.Fatalf("teleported cargo was not released near receiver: position=%d,%d attachment=%+v", cargo.X.Int(), cargo.Z.Int(), cargo.Attachment)
			}
			if diagnostics := dest.GetScript().Diagnostics(); len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			return
		}
	}
	t.Fatalf("did not teleport: cargo=%d,%d dest activation=%v slot=%+v diagnostics=%v", cargo.X>>16, cargo.Z>>16, dest.Activated, dest.SlotAt(0), dest.ScriptState.Binding.VM.Diagnostics())
}

// Gold's carrier scripts assign weights by model height, not a generic unit
// count. Four Stumpies and four Flashes exactly fill ARMTHOVR's authored 7200.
// [research/extensions/escalation-script-systems.md]
func TestEscalationAutomaticSurfaceTransport(t *testing.T) {
	s := escalationSystemSession(t)
	carrier := placeCompleteRetailUnit(t, s, "ARMTHOVR", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
	cargo := make([]*units.Unit, 0, 8)
	for i, position := range [][2]int64{{550, 550}, {600, 500}, {650, 550}, {700, 600}, {650, 650}, {600, 700}, {550, 650}, {500, 600}} {
		key := "ARMSTUMP"
		if i >= 4 {
			key = "ARMFLASH"
		}
		cargo = append(cargo, placeCompleteRetailUnit(t, s, key, 0, numeric.FixedFromInt(position[0]), numeric.FixedFromInt(position[1])))
	}
	ally := placeCompleteRetailUnit(t, s, "ARMFLASH", 1, numeric.FixedFromInt(500), numeric.FixedFromInt(650))
	s.Econ.Players[0].Allies[1] = true
	s.Econ.Players[1].Allies[0] = true
	setEscalationTransportMode(t, s, carrier.Handle, 0)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: carrier.Handle, Activate: true}}); err != nil {
		t.Fatal(err)
	}
	allCarriedBy := func(h pool.Handle) bool {
		for _, u := range cargo {
			if u.Attachment.Carrier != h {
				return false
			}
		}
		return true
	}
	for tick := 0; tick < 2400 && !allCarriedBy(carrier.Handle); tick++ {
		advanceProTATicks(t, s, 1)
	}
	if !allCarriedBy(carrier.Handle) || len(carrier.Attachment.Cargo) != len(cargo) {
		t.Fatalf("mixed load failed: cargo=%v diagnostics=%v", carrier.Attachment.Cargo, carrier.GetScript().Diagnostics())
	}
	excess := placeCompleteRetailUnit(t, s, "ARMFLASH", 0, numeric.FixedFromInt(500), numeric.FixedFromInt(550))
	advanceProTATicks(t, s, 120)
	if excess.Attachment.Carrier != 0 || ally.Attachment.Carrier != 0 {
		t.Fatal("full transport accepted excess capacity or another owner's unit")
	}
	setEscalationTransportMode(t, s, carrier.Handle, 1)
	for tick := 0; tick < 2400 && !allCarriedBy(0); tick++ {
		advanceProTATicks(t, s, 1)
	}
	if !allCarriedBy(0) || len(carrier.Attachment.Cargo) != 0 {
		t.Fatalf("mixed unload failed: cargo=%v diagnostics=%v", carrier.Attachment.Cargo, carrier.GetScript().Diagnostics())
	}
	if diagnostics := carrier.GetScript().Diagnostics(); len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
}

func setEscalationTransportMode(t *testing.T, s *Session, h pool.Handle, value int32) {
	t.Helper()
	for _, cmd := range []HumanCommand{{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{h}}}, {Kind: HumanStance, Stance: HumanStanceCommand{Fire: true, Value: value}}} {
		if err := s.EnqueueHumanCommand(cmd); err != nil {
			t.Fatal(err)
		}
	}
}
