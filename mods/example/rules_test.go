package example

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Importing this package registers its set, so the name is selectable by
// `--gameplay` and by the settings key, and the vocabulary knows it.
func TestTheExampleSetIsRegisteredAndSelectable(t *testing.T) {
	set, ok := session.LookupRuleSet(Name)
	if !ok {
		t.Fatalf("%q is not registered", Name)
	}
	if set.Name != Name {
		t.Fatalf("set name %q, want %q", set.Name, Name)
	}
	if set.Base != gameplay.Modern {
		t.Fatalf("declared base %q, want %q", set.Base, gameplay.Modern)
	}
	mode, err := gameplay.Parse(Name)
	if err != nil || string(mode) != Name {
		t.Fatalf("Parse(%q) = %q, %v; the registry is not installed in the vocabulary", Name, mode, err)
	}
}

// The set changes one answer of one package and inherits the rest of Modern,
// which is what makes an override cheap: the other three seams are the shipped
// Modern implementations, not the packages' Strict fallbacks.
func TestTheExampleSetOverridesOneAnswerAndInheritsModern(t *testing.T) {
	set, ok := session.LookupRuleSet(Name)
	if !ok {
		t.Fatalf("%q is not registered", Name)
	}
	modern := session.ModernRuleSet()
	if _, isModern := set.Orders.(*orders.ModernRules); isModern {
		t.Fatal("the order seam is Modern's own implementation; the override did not reach the built set")
	}
	if _, isStrict := set.Combat.(combat.StrictRules); isStrict {
		t.Fatal("an unstated seam fell back to Strict instead of the declared base")
	}
	if _, isStrict := set.Construction.(construction.StrictRules); isStrict {
		t.Fatal("an unstated seam fell back to Strict instead of the declared base")
	}
	if !set.UnitLimit.RecordsLiveUnitLimit() || !set.UnitLimit.RestoresSavedUnitLimit() {
		t.Fatal("the unit-limit seam does not answer as the declared base does")
	}
	// A unit holding fire: Modern suppresses the combat join, this set does not.
	held := &units.Unit{}
	held.Flags &^= units.StandingFieldMask << units.StandingFireShift
	if !modern.Orders.HoldsFire(held) {
		t.Fatal("the fixture unit is not holding fire; the comparison below proves nothing")
	}
	if set.Orders.HoldsFire(held) {
		t.Fatalf("%q still suppresses a combat join for a held unit", Name)
	}
	// The combat seam is untouched, so the launch gate keeps Modern's answer
	// for a slot the unit owns itself.
	if !set.Combat.HoldsFire(held, false) {
		t.Fatal("the combat seam lost Modern's Hold Fire answer; only the order seam is overridden")
	}
}

// Selecting the set by name binds it to a session and projects the override
// onto the services that ask the question, and the session carries the set's
// base as its mode word so strict-versus-modern logic still has an answer.
func TestSelectingTheExampleSetProjectsTheOverride(t *testing.T) {
	s := &session.Session{Gameplay: gameplay.Strict31, Combat: &combat.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}}
	if err := s.SetRules(Name); err != nil {
		t.Fatalf("SetRules(%q): %v", Name, err)
	}
	if s.Rules.Name != Name {
		t.Fatalf("bound set %q, want %q", s.Rules.Name, Name)
	}
	if s.Gameplay != gameplay.Modern {
		t.Fatalf("session mode word %q, want the set's base %q", s.Gameplay, gameplay.Modern)
	}
	if s.Build.OrderBinding.Rules != s.Rules.Orders {
		t.Fatal("the order seam did not reach the queue binding")
	}
	held := &units.Unit{}
	held.Flags &^= units.StandingFieldMask << units.StandingFireShift
	if s.Build.OrderBinding.Rules.HoldsFire(held) {
		t.Fatal("the queue binding answers with Modern's Hold Fire, not the selected set's")
	}
	// Re-projection is what the composer performs after every allocation, and
	// it must keep the selected set rather than re-derive one from the word.
	selected := s.Rules.Orders
	s.RebindRules()
	if s.Rules.Name != Name || s.Rules.Orders != selected || s.Build.OrderBinding.Rules != selected {
		t.Fatalf("re-projection replaced the selected set with %q", s.Rules.Name)
	}
}

// The end-to-end selection path, the one a `--gameplay example` run takes: a
// composed skirmish carrying the name, advanced far enough that the composer
// allocates units and lazily builds the services it projects a set onto. The
// selected set must still be bound afterwards — a per-allocation step that
// re-derived a set from the session's mode word would have replaced it with
// the reserved Modern set, and nothing in a fingerprint would say so.
//
// Asset-gated: it composes a real map, so the synthetic tier skips it.
func TestSelectingTheExampleSetSurvivesALiveComposition(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("nanolathe: mounting install failed: logical path %s, providers searched [], expected a readable Total Annihilation install: %v", root, err)
	}
	t.Cleanup(func() { fs.Close() })
	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind:           headless.ScenarioDirectOTA,
		Map:            "ashap plateau",
		Gameplay:       gameplay.Mode(Name),
		LocalOwner:     -1,
		SimulationSeed: 7,
		CRTSeed:        7,
		FS:             fs,
	})
	if err != nil {
		t.Skipf("nanolathe: skirmish composition failed: logical path %s, providers searched [], expected a mounted skirmish map: %v", "ashap plateau", err)
	}
	sess := composed.Session
	if sess.Rules.Name != Name {
		t.Fatalf("composition bound %q, want the selected set %q", sess.Rules.Name, Name)
	}
	selected := sess.Rules.Orders
	scaled := sess.Clock.ScaledAnchor
	for sess.Clock.GlobalTick < 300 {
		scaled += 5
		sess.Step(scaled)
	}
	alive := 0
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive {
			alive++
		}
	}
	if alive == 0 {
		t.Fatal("no unit was allocated, so the per-allocation projection never ran")
	}
	if sess.Rules.Name != Name || sess.Rules.Orders != selected {
		t.Fatalf("after %d allocations the bound set is %q", alive, sess.Rules.Name)
	}
	if sess.Build.OrderBinding.Rules != selected {
		t.Fatal("the queue binding the composer retains carries another set's order rules")
	}
}
