package utility

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// Every trait row names a real parameter, archetype names cannot be
// mistaken for the switch's words, every centre is a trait value, and every
// trait has a word for each lean.
func TestPersonalityTablesAreConsistent(t *testing.T) {
	for _, m := range traitParams {
		if paramIndex(m.name) < 0 {
			t.Errorf("trait %s: unknown parameter %q", TraitKeys[m.trait], m.name)
		}
	}
	seen := map[string]bool{}
	for _, a := range Personalities {
		if a.Name == persOff || a.Name == persRandom || a.Name == "" || a.Name == "none" || a.Name == "steady" || seen[a.Name] {
			t.Errorf("archetype name %q is reserved or repeated", a.Name)
		}
		seen[a.Name] = true
		for i, c := range a.centre {
			if c < -traitMax || c > traitMax {
				t.Errorf("%s: %s centre %d", a.Name, TraitKeys[i], c)
			}
		}
	}
	for i, k := range TraitKeys {
		if !strings.HasPrefix(k, "trait_") || traitName(i) == "" || traitWords[i][0] == "" || traitWords[i][1] == "" {
			t.Errorf("trait key %q or its words", k)
		}
	}
	if 2*traitHalf != traitMax {
		t.Errorf("a default draw spans ±%d, want the whole ±%d", 2*traitHalf, traitMax)
	}
}

// begin runs the whole Init draw for one variety.
func drawVariety(seed uint32, v Variety) (*variety, Params, aikit.Rand) {
	r := aikit.PlayerRand(seed, 1)
	k := &aikit.Kit{Persona: aikit.PersonaHard, Rand: &r}
	vr := &variety{v: v}
	p := DefaultParams()
	vr.begin(k, &p)
	return vr, p, r
}

// The personality draws after the style, lead and jitter draws: turning it
// off leaves those as they were, and the drawn personality is exactly what
// a draw from the position the others leave gives. The default plays the
// balanced style; a drawn style's draws are left as they were too.
func TestPersonalityDrawsAfterTheOthers(t *testing.T) {
	names := map[string]bool{}
	var lean, strong int
	for seed := uint32(301); seed < 401; seed++ {
		for _, style := range []string{"balanced", "random"} {
			on := DefaultVariety()
			on.Style = style
			off := on
			off.Personality = persOff
			vOn, pOn, _ := drawVariety(seed, on)
			vOff, pOff, rOff := drawVariety(seed, off)
			if vOn.style != vOff.style || vOn.lead != vOff.lead || vOn.waveJit != vOff.waveJit {
				t.Fatalf("seed %d: the personality moved an earlier draw", seed)
			}
			if style == "balanced" && vOn.style != 0 {
				t.Fatalf("seed %d: the default played style %d", seed, vOn.style)
			}
			if vOff.pers != (personality{}) {
				t.Fatalf("seed %d: personality off drew %+v", seed, vOff.pers)
			}
			// Continue the off run's generator with the personality draw.
			var again personality
			k := &aikit.Kit{Persona: aikit.PersonaHard, Rand: &rOff}
			p := pOff
			again.draw(k, &on, &p)
			if again != vOn.pers || p != pOn {
				t.Fatalf("seed %d: the personality is not the draw that follows the others", seed)
			}
			if style != "balanced" {
				continue
			}
			names[vOn.pers.name()] = true
			for i, tr := range vOn.pers.trait {
				if tr < -traitMax || tr > traitMax {
					t.Fatalf("seed %d: %s = %d", seed, TraitKeys[i], tr)
				}
				if tr >= 30 || tr <= -30 {
					lean++
				}
				if tr >= 80 || tr <= -80 {
					strong++
				}
			}
		}
	}
	// Triangular on ±100: |t| ≥ 30 in 49% of draws, ≥ 80 in 4%.
	if lean < 300 || lean > 500 || strong > 70 {
		t.Errorf("of 800 drawn traits %d lean 30 or more and %d lean 80 or more", lean, strong)
	}
	if len(names) < 8 {
		t.Errorf("100 seeds drew %d personality names", len(names))
	}
}

// The same seed and slot draw the same personality, so a game's
// personality is reproducible from its seed.
func TestPersonalityReplays(t *testing.T) {
	for seed := uint32(1); seed < 21; seed++ {
		a, pa, ra := drawVariety(seed, DefaultVariety())
		b, pb, rb := drawVariety(seed, DefaultVariety())
		if a.pers != b.pers || pa != pb || ra != rb {
			t.Fatalf("seed %d did not replay", seed)
		}
	}
}

// A pinned trait takes its pin and leaves every other trait, and every
// later draw, as drawn; without jitter nothing is drawn: the default plays
// every trait neutral and a named archetype its centres.
func TestPinnedTraitsAndNamedArchetypes(t *testing.T) {
	free, _, rFree := drawVariety(77, DefaultVariety())
	pin := DefaultVariety()
	pin.Traits[TraitTowers], pin.Pinned = -100, 1<<TraitTowers
	pinned, p, rPin := drawVariety(77, pin)
	for i := range free.pers.trait {
		want := free.pers.trait[i]
		if Trait(i) == TraitTowers {
			want = -100
		}
		if pinned.pers.trait[i] != want {
			t.Errorf("%s: %d, want %d", TraitKeys[i], pinned.pers.trait[i], want)
		}
	}
	if rFree != rPin {
		t.Error("pinning a trait changed the number of draws")
	}
	if p.WDefense >= DefaultParams().WDefense {
		t.Errorf("towers pinned at -100 left w_defense at %d", p.WDefense)
	}

	still := DefaultVariety()
	still.Jitter = false
	vd, pd, rd := drawVariety(5, still)
	if fresh := aikit.PlayerRand(5, 1); rd != fresh || vd.pers.trait != [numTraits]int32{} || pd != DefaultParams() || vd.pers.name() != "steady" {
		t.Errorf("the default without jitter drew (%v) or leaned: %+v", rd != fresh, vd.pers)
	}

	named := Variety{Style: "balanced", Personality: "turtle"}
	vr, _, r := drawVariety(5, named)
	if fresh := aikit.PlayerRand(5, 1); r != fresh {
		t.Error("a named archetype without jitter drew from the generator")
	}
	if vr.pers.trait != Personalities[personalityIndex("turtle")].centre || vr.pers.name() != "turtle" {
		t.Errorf("turtle played %+v", vr.pers)
	}
	named.Jitter = true
	vj, _, _ := drawVariety(5, named)
	for i, tr := range vj.pers.trait {
		if c := Personalities[personalityIndex("turtle")].centre[i]; tr < c-traitNoise || tr > c+traitNoise {
			t.Errorf("turtle with jitter: %s %d, centre %d", TraitKeys[i], tr, c)
		}
	}

	// Pins act without a personality too.
	solo := Variety{Style: "balanced"}
	solo.Traits[TraitAggression], solo.Pinned = 100, 1<<TraitAggression
	vs, ps, _ := drawVariety(5, solo)
	if vs.pers.name() != "none" || vs.pers.trait[TraitAggression] != 100 || ps != DefaultParams() {
		t.Errorf("a pinned aggression without a personality: %+v, Params moved %v", vs.pers, ps != DefaultParams())
	}
}

// Each trait moves its parameters to its row's percent at the extremes and
// nothing at 0; aggression and raids reach the attack value and the army.
func TestTraitExtremes(t *testing.T) {
	d := DefaultParams()
	at := func(tr Trait, v int32) (Params, *Strategy) {
		vr := Variety{Style: "balanced"}
		vr.Traits[tr], vr.Pinned = v, 1<<tr
		st := &Strategy{s: &shared{}}
		st.vr.v = vr
		_, p, _ := drawVariety(9, vr)
		st.vr.pers.trait[tr] = v
		return p, st
	}
	if p, _ := at(TraitTowers, 100); p.WDefense != d.WDefense*150/100 {
		t.Errorf("towers +100: w_defense %d", p.WDefense)
	}
	if p, _ := at(TraitTowers, -100); p.WDefense != d.WDefense*60/100 {
		t.Errorf("towers -100: w_defense %d", p.WDefense)
	}
	if p, _ := at(TraitTech, 100); p.TechTime >= d.TechTime || p.WTech <= d.WTech {
		t.Errorf("tech +100: tech_time %d w_tech %d, want earlier and more", p.TechTime, p.WTech)
	}
	if p, _ := at(TraitAir, 50); p.WAir != d.WAir*120/100 {
		t.Errorf("air +50: w_air %d, want halfway to 140%%", p.WAir)
	}
	if _, st := at(TraitAggression, 100); st.vr.pers.launchPct() != aggrLaunchHi || st.ArmyTemper().Engage != aggrEngage {
		t.Errorf("aggression +100: launch %d%%, temper %+v", st.vr.pers.launchPct(), st.ArmyTemper())
	}
	if _, st := at(TraitAggression, -100); st.vr.pers.launchPct() != aggrLaunchLo || st.ArmyTemper().Engage != -aggrEngage {
		t.Errorf("aggression -100: launch %d%%, temper %+v", st.vr.pers.launchPct(), st.ArmyTemper())
	}
	if _, st := at(TraitRaids, 100); st.ArmyTemper() != (ArmyTemper{0, raidSplitHi, raidHarassHi}) {
		t.Errorf("raids +100: %+v", st.ArmyTemper())
	}
	if p, st := at(TraitScouting, 0); p != d || st.ArmyTemper() != (ArmyTemper{0, 100, 100}) || st.vr.pers.launchPct() != 100 {
		t.Error("a neutral trait moved something")
	}
}

// The launch share follows aggression: an aggressive personality launches
// at a smaller army, a patient one at a larger.
func TestAggressionMovesTheLaunchValue(t *testing.T) {
	base := attackAt("balanced", aikit.PersonaHard, Variety{}, 12)
	hot := Variety{}
	hot.Traits[TraitAggression], hot.Pinned = 100, 1<<TraitAggression
	cold := Variety{}
	cold.Traits[TraitAggression], cold.Pinned = -100, 1<<TraitAggression
	h := attackAtPers("balanced", aikit.PersonaHard, hot, 12)
	c := attackAtPers("balanced", aikit.PersonaHard, cold, 12)
	if !(h < base && base < c) {
		t.Errorf("minute 12 launch values: aggressive %d, neutral %d, patient %d", h, base, c)
	}
}

// attackAtPers is attackAt with the variety's personality drawn first.
func attackAtPers(style string, per aikit.Persona, v Variety, minute int64) int64 {
	var vr variety
	vr.v = v
	vr.style = styleIndex(style)
	r := aikit.PlayerRand(1, 0)
	p := DefaultParams()
	vr.pers.draw(&aikit.Kit{Persona: per, Rand: &r}, &v, &p)
	s := &shared{k: &aikit.Kit{Persona: per}, tick: uint32(minute * 1800), minutes: int32(minute)}
	return vr.attackValue(s, &p)
}

// VarietyFrom reads the personality switch and the trait pins and refuses
// what the strategy would not play.
func TestVarietyFromPersonality(t *testing.T) {
	v, err := VarietyFrom(map[string]string{"personality": "raider", "trait_heavy": "-30", "trait_air": "100"})
	if err != nil || v.Personality != "raider" || v.Pinned != 1<<TraitHeavy|1<<TraitAir || v.Traits[TraitHeavy] != -30 || v.Traits[TraitAir] != 100 {
		t.Fatalf("got %+v, %v", v, err)
	}
	if v, _ := VarietyFrom(nil); v.Personality != persRandom || v.Pinned != 0 || v.Style != "balanced" || !v.Jitter {
		t.Errorf("default %+v: want the balanced style, jitter, a drawn personality and no pins", v)
	}
	if v, _ := VarietyFrom(map[string]string{"personality": "off"}); v.Personality != persOff {
		t.Errorf("personality=off read as %q", v.Personality)
	}
	for _, kv := range []map[string]string{
		{"personality": "rush"}, {"personality": ""}, {"trait_towers": "101"}, {"trait_towers": "-101"}, {"trait_towers": "x"},
	} {
		if _, err := VarietyFrom(kv); err == nil {
			t.Errorf("%v accepted", kv)
		}
	}
	var st Strategy
	if err := st.SetVariety(Variety{Personality: "rush"}); err == nil {
		t.Error("SetVariety accepted an unknown personality")
	}
}

// The personality is named in the report and the explain notes with every
// trait value: a named archetype by its name, a drawn one by its strongest
// lean.
func TestPersonalityReportAndNote(t *testing.T) {
	vr, _, _ := drawVariety(11, Variety{Style: "balanced", Personality: "rusher"})
	got := map[string]int64{}
	vr.pers.report(func(name string, v int64) { got[name] = v })
	if got["personality_mode"] != persNamed || got["personality_rusher"] != 1 || got["trait_aggression"] != 70 {
		t.Errorf("report %v", got)
	}
	note := vr.pers.String()
	if !strings.HasPrefix(note, "personality rusher (configured) aggression +70 raids +40 towers -50") || !strings.Contains(note, "scouting +20") {
		t.Errorf("note %q", note)
	}
	for _, c := range []struct {
		trait [numTraits]int32
		name  string
	}{
		{[numTraits]int32{29, -29, 0, 10}, "steady"},
		{[numTraits]int32{30, -29}, "aggressive"},
		{[numTraits]int32{20, -45, 44}, "quiet"},
		{[numTraits]int32{0, 0, 0, 0, 0, 0, 0, -60}, "unscouted"},
		{[numTraits]int32{-50, 50}, "patient"},
	} {
		d := personality{mode: persDrawn, trait: c.trait}
		if n := d.name(); n != c.name {
			t.Errorf("drawn %v is named %q, want %q", c.trait, n, c.name)
		}
	}
	var none personality
	got = map[string]int64{}
	none.report(func(name string, v int64) { got[name] = v })
	if got["personality_mode"] != persNone || got["personality_none"] != 1 {
		t.Errorf("no personality reported %v", got)
	}
}
