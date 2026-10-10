package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
)

// matchTestCommunity is the mainline table with its unit-limit override
// removed, so a test can vary the configuration's unit limit on its own.
func matchTestCommunity(t testing.TB) community.Features {
	t.Helper()
	zero := 0
	f, err := community.Resolve(false, community.Overrides{UnitLimit: &zero})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func matchTestID(b byte) MatchParticipantID {
	var id MatchParticipantID
	for i := range id {
		id[i] = b
	}
	return id
}

// validMatchRequest is a Modern online skirmish with two humans, a computer
// added by the first and a mod selected, in which every field of §8.6 holds
// a value that can change on its own.
func validMatchRequest(t testing.TB) MatchConfigRequest {
	t.Helper()
	guard := orders.DefaultBuilderOptions()
	guard.Guard[0] = orders.GuardStay
	return MatchConfigRequest{
		SessionKind: MatchOnlineSkirmish,
		RuleName:    ModernRuleSetName,
		RuleBase:    MatchBaseModern,
		MapName:     "Great Divide",
		MapSchema:   0,
		Seats: []MatchSeat{
			{Role: MatchRoleHuman, Side: 0, Color: 0, AllyGroup: 0, Nickname: "Ann", Metal: 1000, Energy: 1000,
				Participant: matchTestID(1), HostSeat: MatchHostNone, BuilderOptions: guard},
			{Role: MatchRoleHuman, Side: 1, Color: 1, AllyGroup: 1, Nickname: "Ben", Metal: 1000, Energy: 1000,
				Participant: matchTestID(2), HostSeat: MatchHostNone, BuilderOptions: orders.DefaultBuilderOptions()},
			{Role: MatchRoleComputer, Side: 1, Color: 2, AllyGroup: 2, Nickname: "", Metal: 1000, Energy: 1000,
				HostSeat: 0, ComputerKind: ai.ControllerModern, Difficulty: 1, BuilderOptions: orders.DefaultBuilderOptions(),
				AIParams: []AIParam{{Key: "jitter", Value: "0"}, {Key: "style", Value: "eco"}}},
		},
		Location: 1, CommanderDeath: 1, Mapping: 1, LineOfSight: 1, LOSType: 1,
		UnitLimit:      1000,
		SimulationSeed: 7,
		CRTSeed:        11,
		Mod:            MatchMod{ID: "prota", Version: "4.8", Archive: sha256.Sum256([]byte("prota archive"))},
		ContentProfile: MatchContentProfile{Name: "prota", Directories: []MatchDirectory{{From: "gamedata", To: "gamedatp"}, {From: "weapons", To: "weaponp"}},
			Units: 16000, Weapons: 16000, TNTBytes: 64 << 20, LOSBytes: 8 << 20},
		Community:        matchTestCommunity(t),
		UnitRestrictions: []MatchUnitRestriction{{DefinitionID: 3, Unit: "armcom", Limit: 1}, {DefinitionID: 40, Unit: "corkrog", Limit: 0}},
		CheatsAllowed:    false,
		WatchingAllowed:  false,
		PlayerView:       MatchView{MinimumScale: 256, MaximumScale: 2048, FullMap: true},
		SpectatorView:    MatchView{MinimumScale: 64, MaximumScale: 2048, FullMap: true},
		ReplayView:       MatchView{MinimumScale: 128, MaximumScale: 1024, FullMap: false},
		Policies: MatchPolicies{Revision: 1, Scheduling: 1, Pacing: 1, Drop: 1, Audience: 1,
			RejoinGraceMilliseconds: 60000, SpectatorDelayMilliseconds: 120000, ReplayReleaseDelayMilliseconds: 300000},
	}
}

// validSurvivalRequest is an online Survival battle: two human survivors and
// a computer on one team, and the attacker last.
func validSurvivalRequest(t testing.TB) MatchConfigRequest {
	t.Helper()
	r := validMatchRequest(t)
	r.SessionKind = MatchOnlineSurvival
	r.SurvivalPace = survival.PaceRelaxed
	r.Seats[0].AllyGroup, r.Seats[1].AllyGroup, r.Seats[2].AllyGroup = 2, 2, 2
	r.Seats = append(r.Seats, MatchSeat{Role: MatchRoleSurvivalAttacker, Side: 1, Color: 3, AllyGroup: 5,
		HostSeat: MatchHostNone, BuilderOptions: orders.DefaultBuilderOptions()})
	for i := range r.Seats {
		r.Seats[i].SharedVictory = matchTeamOfTwo(r.Seats, i)
	}
	return r
}

func resolveMatch(t testing.TB, r MatchConfigRequest) EffectiveMatchConfig {
	t.Helper()
	c, err := ResolveMatchConfig(r)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return c
}

// The configuration round-trips through its encoding: the decoded value has
// the same digest and the same request, and resolving its request again
// changes nothing.
func TestMatchConfigRoundTrip(t *testing.T) {
	for _, r := range []MatchConfigRequest{validMatchRequest(t), validSurvivalRequest(t)} {
		c := resolveMatch(t, r)
		payload, err := EncodeMatchConfig(c)
		if err != nil {
			t.Fatal(err)
		}
		back, err := DecodeMatchConfig(payload)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if back.Digest() != c.Digest() {
			t.Fatal("the decoded configuration has another digest")
		}
		if !reflect.DeepEqual(back.Request(), c.Request()) {
			t.Fatalf("the decoded request differs:\n got %+v\nwant %+v", back.Request(), c.Request())
		}
		again := resolveMatch(t, c.Request())
		if again.Digest() != c.Digest() {
			t.Fatal("resolving the resolved request changed it")
		}
		// The identity is SHA-256 of the literal domain, no NUL, then the
		// encoding.
		want := sha256.Sum256(append([]byte("nanolathe/match-config/1"), payload...))
		if c.Digest() != want {
			t.Fatal("the digest is not SHA-256 of the domain and the encoding")
		}
	}
}

// The resolved value is a frozen copy: changing the request afterwards, or
// the copy Request returns, cannot reach it.
func TestMatchConfigOwnsItsStorage(t *testing.T) {
	r := validMatchRequest(t)
	c := resolveMatch(t, r)
	digest := c.Digest()
	r.Seats[2].AIParams[0].Value = "1"
	r.ContentProfile.Directories[0].To = "other"
	r.UnitRestrictions[0].Limit = 9
	copied := c.Request()
	copied.Seats[2].AIParams[1].Value = "tower"
	copied.Seats[0].Nickname = "Zed"
	if c.Digest() != digest {
		t.Fatal("a caller's slice reached the frozen configuration")
	}
	payload, _ := EncodeMatchConfig(c)
	payload[0] ^= 0xff
	if again, _ := EncodeMatchConfig(c); again[0] == payload[0] {
		t.Fatal("EncodeMatchConfig returned the frozen bytes themselves")
	}
}

// The zero value is no configuration: it has no encoding and a zero digest.
func TestZeroMatchConfigIsNoConfiguration(t *testing.T) {
	var c EffectiveMatchConfig
	if _, err := EncodeMatchConfig(c); err == nil {
		t.Fatal("the zero configuration encoded")
	}
	if c.Digest() != ([32]byte{}) {
		t.Fatal("the zero configuration has a digest")
	}
}

// matchMutation changes one effective field of a valid request.
type matchMutation struct {
	name   string
	change func(*MatchConfigRequest)
}

// matchOneFieldMutations covers every field of §8.6, and so every row of
// the §16.2 effective-input inventory the request represents, one effective
// change at a time. Where the format couples two fields — a rule set and its
// base, a team and its shared-victory bits — the mutation changes the one
// effective choice and keeps the configuration valid.
func matchOneFieldMutations() []matchMutation {
	recount := func(r *MatchConfigRequest) {
		for i := range r.Seats {
			r.Seats[i].SharedVictory = matchTeamOfTwo(r.Seats, i)
		}
	}
	m := []matchMutation{
		{"rule set", func(r *MatchConfigRequest) { r.RuleName, r.RuleBase = CommunityRuleSetName, MatchBaseCommunity39 }},
		{"map name", func(r *MatchConfigRequest) { r.MapName = "Lava Run" }},
		{"map schema", func(r *MatchConfigRequest) { r.MapSchema = 1 }},
		{"seat count", func(r *MatchConfigRequest) {
			r.Seats = append(r.Seats, MatchSeat{Role: MatchRoleHuman, Side: 0, Color: 4, AllyGroup: 3, Nickname: "Cy",
				Metal: 1000, Energy: 1000, Participant: matchTestID(3), HostSeat: MatchHostNone, BuilderOptions: orders.DefaultBuilderOptions()})
		}},
		{"seat order", func(r *MatchConfigRequest) {
			r.Seats[0], r.Seats[1] = r.Seats[1], r.Seats[0]
			r.Seats[2].HostSeat = 1
		}},
		{"seat role", func(r *MatchConfigRequest) {
			r.WatchingAllowed = true
			r.Seats[1].Role = MatchRoleWatcher
			r.Seats[1].BuilderOptions = orders.DefaultBuilderOptions()
		}},
		{"side", func(r *MatchConfigRequest) { r.Seats[0].Side = 1 }},
		{"colour", func(r *MatchConfigRequest) { r.Seats[0].Color = 5 }},
		{"team", func(r *MatchConfigRequest) { r.Seats[0].AllyGroup = 4 }},
		{"team with shared victory", func(r *MatchConfigRequest) { r.Seats[2].AllyGroup = 0; recount(r) }},
		{"nickname", func(r *MatchConfigRequest) { r.Seats[0].Nickname = "Anne" }},
		{"metal", func(r *MatchConfigRequest) { r.Seats[0].Metal = 999 }},
		{"energy explicit zero", func(r *MatchConfigRequest) { r.Seats[0].Energy = 0 }},
		{"participant", func(r *MatchConfigRequest) { r.Seats[1].Participant = matchTestID(9) }},
		{"host seat", func(r *MatchConfigRequest) { r.Seats[2].HostSeat = 1 }},
		{"computer kind", func(r *MatchConfigRequest) { r.Seats[2].ComputerKind = ai.ControllerClassic }},
		{"computer difficulty", func(r *MatchConfigRequest) { r.Seats[2].Difficulty = 2 }},
		{"builder options", func(r *MatchConfigRequest) { r.Seats[0].BuilderOptions.Patrol[2] = orders.PatrolAssistOnly }},
		{"AI parameter value", func(r *MatchConfigRequest) { r.Seats[2].AIParams[0].Value = "1" }},
		{"AI parameter added", func(r *MatchConfigRequest) {
			r.Seats[2].AIParams = append(r.Seats[2].AIParams, AIParam{Key: "w_army", Value: "120"})
		}},
		{"AI parameters none", func(r *MatchConfigRequest) { r.Seats[2].AIParams = nil }},
		{"location", func(r *MatchConfigRequest) { r.Location = 0 }},
		{"commander death", func(r *MatchConfigRequest) { r.CommanderDeath = 0 }},
		{"mapping", func(r *MatchConfigRequest) { r.Mapping = 0 }},
		{"line of sight", func(r *MatchConfigRequest) { r.LineOfSight = 0 }},
		{"LOS type", func(r *MatchConfigRequest) { r.LOSType = 0 }},
		{"unit limit", func(r *MatchConfigRequest) { r.UnitLimit = 1001 }},
		{"simulation seed", func(r *MatchConfigRequest) { r.SimulationSeed = 0 }},
		{"CRT seed", func(r *MatchConfigRequest) { r.CRTSeed = 0 }},
		{"mod id", func(r *MatchConfigRequest) { r.Mod.ID = "escalation" }},
		{"mod version", func(r *MatchConfigRequest) { r.Mod.Version = "4.9" }},
		{"mod archive", func(r *MatchConfigRequest) { r.Mod.Archive[31] ^= 1 }},
		{"base content", func(r *MatchConfigRequest) { r.Mod = MatchMod{} }},
		{"content profile name", func(r *MatchConfigRequest) { r.ContentProfile.Name = "retail" }},
		{"content directory row", func(r *MatchConfigRequest) {
			r.ContentProfile.Directories = append(r.ContentProfile.Directories, MatchDirectory{From: "weapons2", To: "weapp2"})
		}},
		{"content directory target", func(r *MatchConfigRequest) { r.ContentProfile.Directories[0].To = "gamedat2" }},
		{"content units", func(r *MatchConfigRequest) { r.ContentProfile.Units = 512 }},
		{"content weapons", func(r *MatchConfigRequest) { r.ContentProfile.Weapons = 256 }},
		{"content TNT bytes", func(r *MatchConfigRequest) { r.ContentProfile.TNTBytes = 16 << 20 }},
		{"content LOS bytes", func(r *MatchConfigRequest) { r.ContentProfile.LOSBytes = 1 << 20 }},
		{"community switch", func(r *MatchConfigRequest) { r.Community.Veterancy = !r.Community.Veterancy }},
		{"community repair rate", func(r *MatchConfigRequest) { r.Community.RepairRate.RepairMultiplier = 2 }},
		{"community unit limit", func(r *MatchConfigRequest) { r.Community.UnitLimit = 1000 }},
		{"restriction added", func(r *MatchConfigRequest) {
			r.UnitRestrictions = append(r.UnitRestrictions, MatchUnitRestriction{DefinitionID: 41, Unit: "corgol", Limit: 100})
		}},
		{"restriction limit", func(r *MatchConfigRequest) { r.UnitRestrictions[0].Limit = 2 }},
		{"restriction definition", func(r *MatchConfigRequest) { r.UnitRestrictions[0].DefinitionID = 4 }},
		{"unrestricted", func(r *MatchConfigRequest) { r.UnitRestrictions = nil }},
		{"cheats", func(r *MatchConfigRequest) { r.CheatsAllowed = true }},
		{"watching", func(r *MatchConfigRequest) { r.WatchingAllowed = true }},
		{"player view minimum", func(r *MatchConfigRequest) { r.PlayerView.MinimumScale = 1024 }},
		{"player view maximum", func(r *MatchConfigRequest) { r.PlayerView.MaximumScale = 1024 }},
		{"player view full map", func(r *MatchConfigRequest) { r.PlayerView.FullMap = false }},
		{"spectator view minimum", func(r *MatchConfigRequest) { r.SpectatorView.MinimumScale = 65 }},
		{"spectator view maximum", func(r *MatchConfigRequest) { r.SpectatorView.MaximumScale = 2047 }},
		{"spectator view full map", func(r *MatchConfigRequest) { r.SpectatorView.FullMap = false }},
		{"replay view minimum", func(r *MatchConfigRequest) { r.ReplayView.MinimumScale = 129 }},
		{"replay view maximum", func(r *MatchConfigRequest) { r.ReplayView.MaximumScale = 2048 }},
		{"replay view full map", func(r *MatchConfigRequest) { r.ReplayView.FullMap = true }},
		{"rejoin grace", func(r *MatchConfigRequest) { r.Policies.RejoinGraceMilliseconds = 0 }},
		{"spectator delay", func(r *MatchConfigRequest) { r.Policies.SpectatorDelayMilliseconds = 1 }},
		{"replay release delay", func(r *MatchConfigRequest) { r.Policies.ReplayReleaseDelayMilliseconds = 2 }},
	}
	// Each of the eleven mutators on its own.
	for i, name := range []string{"build speed", "build cost", "health", "damage", "area of effect", "sight", "radar", "income", "salvage", "fire rate", "unit speed"} {
		m = append(m, matchMutation{"mutator " + name, func(r *MatchConfigRequest) {
			*matchMutatorFields(&r.Mutators)[i] = content.Factor{Num: 2, Den: 1}
		}})
	}
	return m
}

// M2-C9: every effective difference changes the identity. Each one-field
// change resolves, round-trips, and has a digest unlike the base and unlike
// every other change.
func TestMatchConfigEveryEffectiveFieldChangesIdentity(t *testing.T) {
	base := resolveMatch(t, validMatchRequest(t)).Digest()
	seen := map[[32]byte]string{base: "base"}
	for _, m := range matchOneFieldMutations() {
		r := validMatchRequest(t)
		m.change(&r)
		c, err := ResolveMatchConfig(r)
		if err != nil {
			t.Errorf("%s: the changed configuration does not resolve: %v", m.name, err)
			continue
		}
		payload, _ := EncodeMatchConfig(c)
		if back, err := DecodeMatchConfig(payload); err != nil || back.Digest() != c.Digest() {
			t.Errorf("%s: the changed configuration does not round-trip: %v", m.name, err)
		}
		d := c.Digest()
		if other, dup := seen[d]; dup {
			t.Errorf("%s: same identity as %s", m.name, other)
		}
		seen[d] = m.name
	}
	// The Survival switches, against a Survival base.
	survivalBase := resolveMatch(t, validSurvivalRequest(t)).Digest()
	if survivalBase == base {
		t.Fatal("a Survival battle has the skirmish's identity")
	}
	for _, m := range []matchMutation{
		{"survival pace", func(r *MatchConfigRequest) { r.SurvivalPace = survival.PaceRelentless }},
		{"survival no air", func(r *MatchConfigRequest) { r.SurvivalNoAir = true }},
		{"survival no naval", func(r *MatchConfigRequest) { r.SurvivalNoNaval = true }},
	} {
		r := validSurvivalRequest(t)
		m.change(&r)
		if d := resolveMatch(t, r).Digest(); d == survivalBase {
			t.Errorf("%s: the identity did not change", m.name)
		}
	}
}

// §6.6 and §15 Q28: changing only one computer seat's difficulty is a
// configuration mismatch, and there is no battle-wide difficulty word — not
// in the request, and not in the encoding, where a byte inserted after the
// five rule words is refused.
func TestMatchConfigCarriesOnlyPerSeatDifficulty(t *testing.T) {
	r := validMatchRequest(t)
	r.Seats = append(r.Seats, MatchSeat{Role: MatchRoleComputer, Side: 0, Color: 3, AllyGroup: 3, Metal: 1000, Energy: 1000,
		HostSeat: 1, Difficulty: 1, BuilderOptions: orders.DefaultBuilderOptions()})
	base := resolveMatch(t, r)
	for seat := 2; seat <= 3; seat++ {
		changed := r.clone()
		changed.Seats[seat].Difficulty = 0
		if resolveMatch(t, changed).Digest() == base.Digest() {
			t.Fatalf("changing seat %d's difficulty kept the identity", seat)
		}
	}
	if _, ok := reflect.TypeOf(MatchConfigRequest{}).FieldByName("Difficulty"); ok {
		t.Fatal("the request carries a battle-wide difficulty word")
	}
	// The five rule words are Location, CommanderDeath, Mapping,
	// LineOfSight and LOSType, followed by the unit limit. Splice a sixth
	// byte between them.
	payload, _ := EncodeMatchConfig(base)
	at := bytes.Index(payload, []byte{1, 1, 1, 1, 1, 0xe8, 0x07}) // the rule words, then 1000
	if at < 0 {
		t.Fatal("cannot find the rule words in the encoding")
	}
	spliced := append(append(append([]byte(nil), payload[:at+5]...), 1), payload[at+5:]...)
	if _, err := DecodeMatchConfig(spliced); err == nil {
		t.Fatal("a configuration with a battle-wide difficulty word decoded")
	}
	// A difficulty on a seat no reader consults is malformed too.
	human := r.clone()
	human.Seats[0].Difficulty = 1
	if _, err := ResolveMatchConfig(human); err == nil {
		t.Fatal("a human seat carried a difficulty")
	}
}

// M2-C9: equivalent spellings resolve to one value. The identity factor's
// two spellings are one value for every mutator.
func TestMatchConfigEquivalentMutatorSpellings(t *testing.T) {
	zero := validMatchRequest(t)
	one := validMatchRequest(t)
	for _, f := range matchMutatorFields(&one.Mutators) {
		*f = content.Factor{Num: 1, Den: 1}
	}
	a, b := resolveMatch(t, zero), resolveMatch(t, one)
	if a.Digest() != b.Digest() {
		t.Fatal("1/1 and the zero factor resolve to different identities")
	}
	if !reflect.DeepEqual(a.Request(), b.Request()) {
		t.Fatal("1/1 and the zero factor resolve to different values")
	}
	// An empty collection and none are one value too.
	empty, none := validMatchRequest(t), validMatchRequest(t)
	empty.Seats[2].AIParams, empty.ContentProfile.Directories, empty.UnitRestrictions = []AIParam{}, []MatchDirectory{}, []MatchUnitRestriction{}
	none.Seats[2].AIParams, none.ContentProfile.Directories, none.UnitRestrictions = nil, nil, nil
	if e, n := resolveMatch(t, empty), resolveMatch(t, none); e.Digest() != n.Digest() || !reflect.DeepEqual(e.Request(), n.Request()) {
		t.Fatal("empty collections and absent ones resolve to different values")
	}
	// Each index names the factor of the step table, in order.
	payload, _ := EncodeMatchConfig(a)
	ones := bytes.Repeat([]byte{3}, 11)
	if !bytes.Contains(payload, ones) {
		t.Fatal("the identity mutators do not encode as eleven index 3 bytes")
	}
	want := []content.Factor{{Num: 1, Den: 4}, {Num: 1, Den: 2}, {Num: 3, Den: 4}, {}, {Num: 3, Den: 2}, {Num: 2, Den: 1}, {Num: 3, Den: 1}, {Num: 4, Den: 1}}
	for i, f := range want {
		got, ok := matchMutatorFactor(uint8(i))
		if !ok || got != f {
			t.Fatalf("step index %d is %v, want %v", i, got, f)
		}
	}
	if _, ok := matchMutatorFactor(8); ok {
		t.Fatal("step index 8 names a factor")
	}
}

// Every bound and closed value of §8.6 refuses what it does not admit,
// without allocating a configuration.
func TestMatchConfigRefusesInvalidFields(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	cases := []matchMutation{
		{"session kind 0", func(r *MatchConfigRequest) { r.SessionKind = 0 }},
		{"session kind 3", func(r *MatchConfigRequest) { r.SessionKind = 3 }},
		{"unknown rule set", func(r *MatchConfigRequest) { r.RuleName = "modern-ai" }},
		{"rule set not canonical", func(r *MatchConfigRequest) { r.RuleName = "Modern" }},
		{"rule base disagrees", func(r *MatchConfigRequest) { r.RuleBase = MatchBaseCommunity39 }},
		{"rule base unknown", func(r *MatchConfigRequest) { r.RuleBase = 4 }},
		{"empty map", func(r *MatchConfigRequest) { r.MapName = "" }},
		{"spaced map", func(r *MatchConfigRequest) { r.MapName = " Great Divide" }},
		{"long map", func(r *MatchConfigRequest) { r.MapName = long(1025) }},
		{"one seat", func(r *MatchConfigRequest) { r.Seats = r.Seats[:1] }},
		{"eleven seats", func(r *MatchConfigRequest) {
			for len(r.Seats) < 11 {
				r.Seats = append(r.Seats, MatchSeat{Role: MatchRoleComputer, AllyGroup: 5, HostSeat: 1, BuilderOptions: orders.DefaultBuilderOptions()})
			}
		}},
		{"no human", func(r *MatchConfigRequest) {
			r.WatchingAllowed = true
			r.Seats[0].Role, r.Seats[1].Role = MatchRoleWatcher, MatchRoleWatcher
			r.Seats[0].BuilderOptions = orders.DefaultBuilderOptions()
			r.Seats = r.Seats[:2]
		}},
		{"watcher without permission", func(r *MatchConfigRequest) {
			r.Seats[1].Role = MatchRoleWatcher
		}},
		{"role 5", func(r *MatchConfigRequest) { r.Seats[1].Role = 5 }},
		{"colour 10", func(r *MatchConfigRequest) { r.Seats[0].Color = 10 }},
		{"ally group 6", func(r *MatchConfigRequest) { r.Seats[0].AllyGroup = 6 }},
		{"long nickname", func(r *MatchConfigRequest) { r.Seats[0].Nickname = long(17) }},
		{"invalid nickname", func(r *MatchConfigRequest) { r.Seats[0].Nickname = "\xe2\x82" }},
		{"NUL nickname", func(r *MatchConfigRequest) { r.Seats[0].Nickname = "a\x00b" }},
		{"negative metal", func(r *MatchConfigRequest) { r.Seats[0].Metal = -1 }},
		{"negative energy", func(r *MatchConfigRequest) { r.Seats[0].Energy = -1 }},
		{"human without identity", func(r *MatchConfigRequest) { r.Seats[0].Participant = MatchParticipantID{} }},
		{"duplicate identity", func(r *MatchConfigRequest) { r.Seats[1].Participant = r.Seats[0].Participant }},
		{"computer with identity", func(r *MatchConfigRequest) { r.Seats[2].Participant = matchTestID(5) }},
		{"human with host", func(r *MatchConfigRequest) { r.Seats[0].HostSeat = 0 }},
		{"human with computer kind", func(r *MatchConfigRequest) { r.Seats[0].ComputerKind = ai.ControllerModern }},
		{"human with AI parameters", func(r *MatchConfigRequest) { r.Seats[0].AIParams = []AIParam{{Key: "jitter", Value: "0"}} }},
		{"computer hosted by itself", func(r *MatchConfigRequest) { r.Seats[2].HostSeat = 2 }},
		{"computer hosted by no seat", func(r *MatchConfigRequest) { r.Seats[2].HostSeat = 9 }},
		{"computer with no host", func(r *MatchConfigRequest) { r.Seats[2].HostSeat = MatchHostNone }},
		{"computer kind 2", func(r *MatchConfigRequest) { r.Seats[2].ComputerKind = 2 }},
		{"difficulty 3", func(r *MatchConfigRequest) { r.Seats[2].Difficulty = 3 }},
		{"computer builder options", func(r *MatchConfigRequest) { r.Seats[2].BuilderOptions.Guard[1] = orders.GuardStay }},
		{"builder option 3", func(r *MatchConfigRequest) { r.Seats[0].BuilderOptions.Patrol[0] = 3 }},
		{"unsorted AI parameters", func(r *MatchConfigRequest) {
			r.Seats[2].AIParams[0], r.Seats[2].AIParams[1] = r.Seats[2].AIParams[1], r.Seats[2].AIParams[0]
		}},
		{"repeated AI parameter", func(r *MatchConfigRequest) { r.Seats[2].AIParams[1] = r.Seats[2].AIParams[0] }},
		{"unknown AI parameter", func(r *MatchConfigRequest) { r.Seats[2].AIParams[0].Key = "skill" }},
		{"AI parameter out of range", func(r *MatchConfigRequest) { r.Seats[2].AIParams[0] = AIParam{Key: "jitter", Value: "2"} }},
		{"AI parameter with a separator", func(r *MatchConfigRequest) { r.Seats[2].AIParams[1].Value = "e,co" }},
		{"AI parameter too long", func(r *MatchConfigRequest) { r.Seats[2].AIParams[1].Value = long(33) }},
		{"too many AI parameters", func(r *MatchConfigRequest) {
			r.Seats[2].AIParams = make([]AIParam, 129)
		}},
		{"strict second computer", func(r *MatchConfigRequest) {
			r.RuleName, r.RuleBase, r.Community = StrictRuleSetName, MatchBaseStrict31, community.Features{}
			r.Seats = append(r.Seats, MatchSeat{Role: MatchRoleComputer, AllyGroup: 5, HostSeat: 0, BuilderOptions: orders.DefaultBuilderOptions()})
		}},
		{"deathmatch computer", func(r *MatchConfigRequest) { r.CommanderDeath = uint8(CommanderDeathDeathmatch) }},
		{"attacker in a skirmish", func(r *MatchConfigRequest) {
			r.Seats[2] = MatchSeat{Role: MatchRoleSurvivalAttacker, AllyGroup: 5, HostSeat: MatchHostNone, BuilderOptions: orders.DefaultBuilderOptions()}
		}},
		{"one team holds every seat", func(r *MatchConfigRequest) {
			for i := range r.Seats {
				r.Seats[i].AllyGroup = 3
				r.Seats[i].SharedVictory = true
			}
		}},
		{"shared victory alone", func(r *MatchConfigRequest) { r.Seats[0].SharedVictory = true }},
		{"shared victory missing", func(r *MatchConfigRequest) { r.Seats[2].AllyGroup = 0 }},
		{"location 2", func(r *MatchConfigRequest) { r.Location = 2 }},
		{"commander death 3", func(r *MatchConfigRequest) { r.CommanderDeath = 3 }},
		{"mapping 2", func(r *MatchConfigRequest) { r.Mapping = 2 }},
		{"line of sight 2", func(r *MatchConfigRequest) { r.LineOfSight = 2 }},
		{"LOS type 2", func(r *MatchConfigRequest) { r.LOSType = 2 }},
		{"unit limit 19", func(r *MatchConfigRequest) { r.UnitLimit = 19 }},
		{"unit limit 3277", func(r *MatchConfigRequest) { r.UnitLimit = 3277 }},
		{"unit limit unlike the table's", func(r *MatchConfigRequest) { r.Community.UnitLimit = 1500 }},
		{"survival switches in a skirmish", func(r *MatchConfigRequest) { r.SurvivalNoAir = true }},
		{"survival pace in a skirmish", func(r *MatchConfigRequest) { r.SurvivalPace = survival.PaceRelaxed }},
		{"mod without archive", func(r *MatchConfigRequest) { r.Mod.Archive = [32]byte{} }},
		{"mod without version", func(r *MatchConfigRequest) { r.Mod.Version = "" }},
		{"long mod id", func(r *MatchConfigRequest) { r.Mod.ID = long(256) }},
		{"profile without name", func(r *MatchConfigRequest) { r.ContentProfile.Name = "" }},
		{"identity directory row", func(r *MatchConfigRequest) { r.ContentProfile.Directories[0].To = "gamedata" }},
		{"unsorted directory rows", func(r *MatchConfigRequest) {
			d := r.ContentProfile.Directories
			d[0], d[1] = d[1], d[0]
		}},
		{"capital directory key", func(r *MatchConfigRequest) { r.ContentProfile.Directories[0].From = "Gamedata" }},
		{"capital directory target", func(r *MatchConfigRequest) { r.ContentProfile.Directories[0].To = "GamedatP" }},
		{"nested directory", func(r *MatchConfigRequest) { r.ContentProfile.Directories[0].To = "a/b" }},
		{"65 directory rows", func(r *MatchConfigRequest) {
			r.ContentProfile.Directories = nil
			for i := 0; i < 65; i++ {
				r.ContentProfile.Directories = append(r.ContentProfile.Directories, MatchDirectory{From: "d" + strings.Repeat("x", i), To: "t"})
			}
		}},
		{"zero units", func(r *MatchConfigRequest) { r.ContentProfile.Units = 0 }},
		{"units over the domain", func(r *MatchConfigRequest) { r.ContentProfile.Units = 65537 }},
		{"zero weapons", func(r *MatchConfigRequest) { r.ContentProfile.Weapons = 0 }},
		{"weapons over int32", func(r *MatchConfigRequest) { r.ContentProfile.Weapons = 1 << 31 }},
		{"zero TNT bytes", func(r *MatchConfigRequest) { r.ContentProfile.TNTBytes = 0 }},
		{"LOS bytes over int64", func(r *MatchConfigRequest) { r.ContentProfile.LOSBytes = 1 << 63 }},
		{"zero table outside Strict", func(r *MatchConfigRequest) { r.Community = community.Features{} }},
		{"table out of bounds", func(r *MatchConfigRequest) { r.Community.RepairRate.SelfHealMultiplier = 101 }},
		{"snap radius over its maximum", func(r *MatchConfigRequest) { r.Community.MexSnapRadius = r.Community.MexSnapRadiusMax + 1 }},
		{"table under Strict", func(r *MatchConfigRequest) {
			r.RuleName, r.RuleBase = StrictRuleSetName, MatchBaseStrict31
		}},
		{"mutator off the step list", func(r *MatchConfigRequest) { r.Mutators.Damage = content.Factor{Num: 5, Den: 1} }},
		{"restriction definition zero", func(r *MatchConfigRequest) { r.UnitRestrictions[0].DefinitionID = 0 }},
		{"unsorted restrictions", func(r *MatchConfigRequest) { r.UnitRestrictions[1].DefinitionID = 3 }},
		{"restriction limit 101", func(r *MatchConfigRequest) { r.UnitRestrictions[0].Limit = 101 }},
		{"restriction key not canonical", func(r *MatchConfigRequest) { r.UnitRestrictions[0].Unit = "ARMCOM" }},
		{"restriction key empty", func(r *MatchConfigRequest) { r.UnitRestrictions[0].Unit = "" }},
		{"view minimum 63", func(r *MatchConfigRequest) { r.PlayerView.MinimumScale = 63 }},
		{"view maximum 2049", func(r *MatchConfigRequest) { r.SpectatorView.MaximumScale = 2049 }},
		{"view minimum over maximum", func(r *MatchConfigRequest) { r.ReplayView.MinimumScale = 1025 }},
		{"policy revision 2", func(r *MatchConfigRequest) { r.Policies.Revision = 2 }},
		{"scheduling 2", func(r *MatchConfigRequest) { r.Policies.Scheduling = 2 }},
		{"pacing 0", func(r *MatchConfigRequest) { r.Policies.Pacing = 0 }},
		{"drop 2", func(r *MatchConfigRequest) { r.Policies.Drop = 2 }},
		{"audience 2", func(r *MatchConfigRequest) { r.Policies.Audience = 2 }},
	}
	for _, m := range cases {
		r := validMatchRequest(t)
		m.change(&r)
		if c, err := ResolveMatchConfig(r); err == nil {
			t.Errorf("%s: accepted", m.name)
		} else if c.Digest() != ([32]byte{}) {
			t.Errorf("%s: a refused configuration returned a value", m.name)
		}
	}
	// The Survival layout's own invariants.
	for _, m := range []matchMutation{
		{"attacker not last", func(r *MatchConfigRequest) { r.Seats[2], r.Seats[3] = r.Seats[3], r.Seats[2] }},
		{"no attacker", func(r *MatchConfigRequest) {
			r.Seats = r.Seats[:3]
		}},
		{"attacker allied", func(r *MatchConfigRequest) { r.Seats[3].AllyGroup = 2 }},
		{"attacker with resources", func(r *MatchConfigRequest) { r.Seats[3].Metal = 1000 }},
		{"attacker with identity", func(r *MatchConfigRequest) { r.Seats[3].Participant = matchTestID(7) }},
		{"survivors split", func(r *MatchConfigRequest) {
			r.Seats[2].AllyGroup = 3
			for i := range r.Seats {
				r.Seats[i].SharedVictory = matchTeamOfTwo(r.Seats, i)
			}
		}},
		{"pace 3", func(r *MatchConfigRequest) { r.SurvivalPace = 3 }},
	} {
		r := validSurvivalRequest(t)
		m.change(&r)
		if _, err := ResolveMatchConfig(r); err == nil {
			t.Errorf("survival %s: accepted", m.name)
		}
	}
}

// §6.6 Q23: one computer per human under Strict 3.1; Modern and Community
// admit several within the available seats.
func TestMatchConfigComputerSeatCap(t *testing.T) {
	twoComputers := func(rule string, base MatchRuleBase, f community.Features) MatchConfigRequest {
		r := validMatchRequest(t)
		r.RuleName, r.RuleBase, r.Community = rule, base, f
		r.Seats = append(r.Seats, MatchSeat{Role: MatchRoleComputer, AllyGroup: 5, Metal: 1000, Energy: 1000, HostSeat: 0, BuilderOptions: orders.DefaultBuilderOptions()})
		return r
	}
	if _, err := ResolveMatchConfig(twoComputers(StrictRuleSetName, MatchBaseStrict31, community.Features{})); err == nil {
		t.Fatal("Strict 3.1 admitted a second computer for one human")
	}
	strictTwoHosts := twoComputers(StrictRuleSetName, MatchBaseStrict31, community.Features{})
	strictTwoHosts.Seats[3].HostSeat = 1
	resolveMatch(t, strictTwoHosts)
	resolveMatch(t, twoComputers(ModernRuleSetName, MatchBaseModern, matchTestCommunity(t)))
	resolveMatch(t, twoComputers(CommunityRuleSetName, MatchBaseCommunity39, matchTestCommunity(t)))
	// Ten seats are the most there are.
	full := validMatchRequest(t)
	for len(full.Seats) < SkirmishMaxPlayers {
		full.Seats = append(full.Seats, MatchSeat{Role: MatchRoleComputer, AllyGroup: 5, HostSeat: 0, BuilderOptions: orders.DefaultBuilderOptions()})
	}
	resolveMatch(t, full)
	full.Seats = append(full.Seats, MatchSeat{Role: MatchRoleComputer, AllyGroup: 5, HostSeat: 0, BuilderOptions: orders.DefaultBuilderOptions()})
	if _, err := ResolveMatchConfig(full); err == nil {
		t.Fatal("an eleventh seat was admitted")
	}
	// The cap is the bound set's seat seam, not its base: a Strict-based set
	// composing two seats per human admits what Strict 3.1 refuses, and a
	// Modern-based set composing Strict's answer refuses what Modern admits.
	resolveMatch(t, twoComputers(matchTestTwoSeatsSet, MatchBaseStrict31, community.Features{}))
	threeComputers := twoComputers(matchTestTwoSeatsSet, MatchBaseStrict31, community.Features{})
	threeComputers.Seats = append(threeComputers.Seats, MatchSeat{Role: MatchRoleComputer, AllyGroup: 5, HostSeat: 0, BuilderOptions: orders.DefaultBuilderOptions()})
	if _, err := ResolveMatchConfig(threeComputers); err == nil {
		t.Fatal("a set allowing two computers per human admitted three")
	}
	if _, err := ResolveMatchConfig(twoComputers(matchTestOneSeatSet, MatchBaseModern, matchTestCommunity(t))); err == nil {
		t.Fatal("a Modern-based set composing one computer per human admitted two")
	}
}

// Two registered sets that compose another seat answer than their base's.
const (
	matchTestTwoSeatsSet = "session-test-two-seats"
	matchTestOneSeatSet  = "session-test-one-seat"
)

// matchTestTwoSeats lets one human add two computers; its other answers are
// its Strict 3.1 base's.
type matchTestTwoSeats struct{ StrictSeats }

func (matchTestTwoSeats) ComputerSeatsPerHuman() int { return 2 }

func init() {
	RegisterRuleSet(matchTestTwoSeatsSet, func() RuleSet { return RuleSet{Base: gameplay.Strict31, Seats: matchTestTwoSeats{}} })
	RegisterRuleSet(matchTestOneSeatSet, func() RuleSet { return RuleSet{Base: gameplay.Modern, Seats: StrictSeats{}} })
}

// A Strict 3.1 configuration carries exactly the zero Community table, and
// its encoding is the zero table's canonical JSON, whose digest is the one
// community.Features.Digest reports.
func TestMatchConfigCommunityTable(t *testing.T) {
	r := validMatchRequest(t)
	r.RuleName, r.RuleBase, r.Community = StrictRuleSetName, MatchBaseStrict31, community.Features{}
	payload, _ := EncodeMatchConfig(resolveMatch(t, r))
	zero, _ := matchCommunityJSONForTest(community.Features{})
	if !bytes.Contains(payload, zero) {
		t.Fatal("the Strict table is not the zero table's canonical JSON")
	}
	sum := sha256.Sum256(zero)
	if hex.EncodeToString(sum[:]) != (community.Features{}).Digest() {
		t.Fatal("the encoded table is not the bytes Features.Digest hashes")
	}
}

func matchCommunityJSONForTest(f community.Features) ([]byte, error) {
	set, _ := LookupRuleSet(ModernRuleSetName)
	if f == (community.Features{}) {
		set, _ = LookupRuleSet(StrictRuleSetName)
	}
	return matchCommunityJSON(set, f)
}
