package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func isFeatureFixtureDef(name string) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
		UnitName:         name,
		MaxDamage:        100,
		Limit:            -1,
		IsFeature:        true,
		Corpse:           "dragonteeth_dead",
	}
}

// TestFinishedIsFeatureCreationStampsCause7 locks site 1 of the two cause-7
// writers [06 R-DMG-01 §12]: the unit creator, when asked for a FINISHED unit
// whose definition carries `isfeature`, stores damage kind 7 directly and
// raises the death latch, so the ordinary death sweep converts the unit into
// its authored corpse feature. The gate is the `isfeature` bit alone — the
// definition here has full health and its corpse need not resolve.
//
// Stock content reaches this through mission placement of the dragon's teeth
// and the floating forts, which the entry path creates already built.
func TestFinishedIsFeatureCreationStampsCause7(t *testing.T) {
	def := isFeatureFixtureDef("fixturedrag")
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)

	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("finished isfeature create: %v", err)
	}
	u := w.Unit(h)
	if !u.Dying {
		t.Fatal("a finished isfeature creation left the death latch clear [06 R-DMG-01 §12]")
	}
	if u.LastDamageCause != damageKindFeatureConversion {
		t.Fatalf("finished isfeature creation stamped kind %d, want 7 [06 §12.1]", u.LastDamageCause)
	}
	if u.DeathCause != DeathKilled {
		t.Fatalf("death label = %d, want DeathKilled, the label the build-completion writer uses", u.DeathCause)
	}
	// Cause 7's attacker is not a packet field, so the creator's neutral-side
	// seed stands and no attacker link is recorded [06 §12.1][06 R-WPN-04 §2].
	if u.EngagementTarget != 0 {
		t.Fatalf("recorded attacker = %d, want the null handle [06 §12.1]", u.EngagementTarget)
	}
	if u.LastDamageSide != NeutralAttackerSide {
		t.Fatalf("attacker-side snapshot = %d, want the neutral seed %d", u.LastDamageSide, NeutralAttackerSide)
	}
}

// TestNanoframeIsFeatureCreationIsNotLatched is the negative half. The block is
// selected by the creator's finished argument, so a frame creation skips it
// entirely and converts only when the build-completion transition — the other
// cause-7 writer — stamps the kind on it [06 R-DMG-01 §12]. A frame that
// latched at creation would die on its first sweep and never be built.
//
// An already-built creation whose definition does NOT carry `isfeature` is the
// other side of the same gate: no health, corpse-flag or corpse-resolution
// condition exists, and no other definition bit selects the arm.
func TestNanoframeIsFeatureCreationIsNotLatched(t *testing.T) {
	def := isFeatureFixtureDef("fixturedrag")
	plain := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("fixturetank")},
		UnitName:         "fixturetank",
		MaxDamage:        100,
		Limit:            -1,
		Corpse:           "tank_dead",
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		def.CanonicalKey:   def,
		plain.CanonicalKey: plain,
	}}
	w := newFixtureWorld(4, cat)

	frame, err := w.CreateNanoframe(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("isfeature nanoframe create: %v", err)
	}
	if u := w.Unit(frame); u.Dying || u.LastDamageCause != damageKindNone {
		t.Fatalf("nanoframe latched=%v kind=%d, want an unlatched frame with no recorded kind [06 R-DMG-01 §12]", u.Dying, u.LastDamageCause)
	}

	built, err := w.Create(plain, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("finished ordinary create: %v", err)
	}
	if u := w.Unit(built); u.Dying || u.LastDamageCause != damageKindNone {
		t.Fatalf("ordinary finished unit latched=%v kind=%d, want neither [06 R-DMG-01 §12]", u.Dying, u.LastDamageCause)
	}
}
