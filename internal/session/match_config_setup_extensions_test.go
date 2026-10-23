package session

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Typed choices and raw known records resolve to one value. Opaque optional
// metadata keeps its bytes alongside both required gameplay records.
func TestSetupExtensionsShareTheVersion2Tail(t *testing.T) {
	r := validMatchRequest(t)
	r.RoomContent = []MatchContentEntry{{Kind: MatchContentBaseArchive, Name: "totala1.hpi", Size: 42}}
	base := encodeMatchForTest(t, resolveMatch(t, r))
	r.Seats[0].Start = StartPoint{Set: true, X: 3, Z: 4}
	r.AlliedSight = MatchAlliedSightShared
	r.Extensions = []MatchConfigExtension{{ID: 32, Data: []byte("annotation")}}
	c := resolveMatch(t, r)
	tail := extensionRecordForTest(17, 1, []byte{1, 3, 4, 0, 0})
	tail = append(tail, extensionRecordForTest(18, 1, []byte{2})...)
	tail = append(tail, extensionRecordForTest(32, 0, []byte("annotation"))...)
	tail = append(tail, 0)
	payload := withMatchTailForTest(base, tail)
	if !bytes.Equal(encodeMatchForTest(t, c), payload) {
		t.Fatal("gameplay and metadata records did not follow room content in ID order")
	}
	back, err := DecodeMatchConfig(payload)
	if err != nil || back.Digest() != c.Digest() || !reflect.DeepEqual(back.Request(), c.Request()) {
		t.Fatalf("typed choices or opaque metadata did not round-trip: %v", err)
	}
	q := back.Request()
	q.Seats[0].Start = StartPoint{}
	q.AlliedSight = MatchAlliedSightVersion1
	q.Extensions = nil
	if got := encodeMatchForTest(t, resolveMatch(t, q)); !bytes.Equal(got, base) {
		t.Fatal("clearing typed choices left stale gameplay extension records")
	}
	q = validMatchRequest(t)
	q.RoomContent = r.RoomContent
	q.Extensions = []MatchConfigExtension{{ID: 17, Required: true, Data: []byte{1, 3, 4, 0, 0}}, {ID: 18, Required: true, Data: []byte{2}}, {ID: 32, Data: []byte("annotation")}}
	if got := resolveMatch(t, q); got.Digest() != c.Digest() || !reflect.DeepEqual(got.Request(), c.Request()) {
		t.Fatal("raw known records did not project into the typed fields")
	}
	q.Seats[0].Start = StartPoint{Set: true, X: 9, Z: 4}
	if _, err := ResolveMatchConfig(q); err == nil {
		t.Fatal("conflicting typed and raw choices resolved")
	}
	q = back.Request()
	q.Extensions[0].Data[0] ^= 1
	if resolveMatch(t, q).Digest() == c.Digest() || !bytes.Equal(encodeMatchForTest(t, back), payload) {
		t.Fatal("metadata mutation changed frozen storage or failed to change identity")
	}
}

func TestSetupExtensionsRefuseOptionalOrMalformedGameplay(t *testing.T) {
	base := encodeMatchForTest(t, resolveMatch(t, validMatchRequest(t)))
	tooLarge := append([]byte{1}, uvarintBytes(StartPointLimit)...)
	tooLarge = append(tooLarge, 4, 0, 0)
	for _, tc := range []struct {
		name  string
		id    uint64
		flags byte
		data  []byte
	}{
		{"optional starts", 17, 0, []byte{1, 3, 4, 0, 0}},
		{"optional sight", 18, 0, []byte{2}},
		{"all Auto", 17, 1, []byte{0, 0, 0}},
		{"short starts", 17, 1, []byte{1, 3, 4}},
		{"extra starts", 17, 1, []byte{1, 3, 4, 0, 0, 0}},
		{"start flag", 17, 1, []byte{2, 0, 0}},
		{"coordinate bound", 17, 1, tooLarge},
		{"short sight", 18, 1, nil},
		{"extra sight", 18, 1, []byte{2, 0}},
		{"default sight", 18, 1, []byte{0}},
		{"unknown sight", 18, 1, []byte{3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := withMatchTailForTest(base, append(extensionRecordForTest(tc.id, tc.flags, tc.data), 0))
			if c, err := DecodeMatchConfig(payload); err == nil || c.Digest() != ([32]byte{}) {
				t.Fatalf("malformed gameplay extension accepted: %v", err)
			}
		})
	}
	// Typed records count toward the same 128-record bound as opaque metadata.
	r := validMatchRequest(t)
	r.AlliedSight = MatchAlliedSightShared
	for i := range 128 {
		r.Extensions = append(r.Extensions, MatchConfigExtension{ID: uint16(32 + i)})
	}
	if _, err := ResolveMatchConfig(r); err == nil {
		t.Fatal("129 records including a gameplay choice resolved")
	}
	for _, extension := range []MatchConfigExtension{
		{ID: 17, Required: true, Data: []byte{1, 3, 4, 0, 0}},
		{ID: 18, Required: true, Data: []byte{1}},
	} {
		survival := validSurvivalRequest(t)
		survival.Extensions = []MatchConfigExtension{extension}
		if _, err := ResolveMatchConfig(survival); err == nil {
			t.Fatal("Survival accepted a skirmish gameplay-choice extension")
		}
	}
}

// Builder speed has its own step list, which field 11's frozen step indices
// cannot spell, so it travels as required extension 19: one u8 index on
// content.BuilderSpeedSteps, omitted for the identity
// (docs/DESIGN_MODS_MUTATORS.md "Builder speed"). A configuration without it
// keeps its exact bytes.
func TestBuilderSpeedTravelsAsExtension19(t *testing.T) {
	r := validMatchRequest(t)
	base := encodeMatchForTest(t, resolveMatch(t, r))
	r.Mutators.BuilderSpeed = content.Factor{Num: 1, Den: 1}
	if got := encodeMatchForTest(t, resolveMatch(t, r)); !bytes.Equal(got, base) {
		t.Fatal("the identity spelled 1/1 wrote an extension")
	}
	if resolveMatch(t, r).Request().Mutators.BuilderSpeed != (content.Factor{}) {
		t.Fatal("the identity did not resolve to the zero factor")
	}
	r.Mutators.BuilderSpeed = content.Factor{Num: 5, Den: 1}
	c := resolveMatch(t, r)
	payload := withMatchTailForTest(base, append(extensionRecordForTest(19, 1, []byte{3}), 0))
	if !bytes.Equal(encodeMatchForTest(t, c), payload) {
		t.Fatal("Builder speed did not follow field 16 as extension 19")
	}
	back, err := DecodeMatchConfig(payload)
	if err != nil || back.Digest() != c.Digest() || back.Request().Mutators.BuilderSpeed != (content.Factor{Num: 5, Den: 1}) {
		t.Fatalf("Builder speed did not round-trip: %v", err)
	}
	q := validMatchRequest(t)
	q.Extensions = []MatchConfigExtension{{ID: 19, Required: true, Data: []byte{3}}}
	if got := resolveMatch(t, q); got.Digest() != c.Digest() || !reflect.DeepEqual(got.Request(), c.Request()) {
		t.Fatal("the raw record did not project into the typed factor")
	}
	q.Mutators.BuilderSpeed = content.Factor{Num: 3, Den: 1}
	if _, err := ResolveMatchConfig(q); err == nil {
		t.Fatal("conflicting typed and raw Builder speed resolved")
	}
	for _, bad := range []content.Factor{{Num: 4, Den: 1}, {Num: 1, Den: 2}} {
		q = validMatchRequest(t)
		q.Mutators.BuilderSpeed = bad
		if _, err := ResolveMatchConfig(q); err == nil {
			t.Fatalf("Builder speed %+v off its step list resolved", bad)
		}
	}
	if _, err := resolveMatchConfig(r, 1); err == nil {
		t.Fatal("a version 1 configuration carried Builder speed")
	}
	// A mutator applies in Survival as in skirmish.
	survival := validSurvivalRequest(t)
	survival.Mutators.BuilderSpeed = content.Factor{Num: 20, Den: 1}
	if _, err := ResolveMatchConfig(survival); err != nil {
		t.Fatalf("Survival refused Builder speed: %v", err)
	}
	for _, tc := range []struct {
		name  string
		flags byte
		data  []byte
	}{
		{"optional", 0, []byte{3}},
		{"identity index", 1, []byte{0}},
		{"index past the list", 1, []byte{6}},
		{"short", 1, nil},
		{"extra", 1, []byte{3, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := withMatchTailForTest(base, append(extensionRecordForTest(19, tc.flags, tc.data), 0))
			if c, err := DecodeMatchConfig(payload); err == nil || c.Digest() != ([32]byte{}) {
				t.Fatalf("malformed Builder speed extension accepted: %v", err)
			}
		})
	}
}

// A generated map's recipe is required extension 20 in skirmish and
// Survival alike: it round-trips as typed bytes, raw records project into
// it, and an optional, empty or conflicting record is refused
// (docs/DESIGN_MAP_GENERATOR.md §10).
func TestGeneratedMapIsRequiredExtension20(t *testing.T) {
	recipe := []byte(`{"format":1,"code":"test_world-2-0-6-1-1-1-b-v1"}`)
	r := validMatchRequest(t)
	base := encodeMatchForTest(t, resolveMatch(t, r))
	r.GeneratedMap = recipe
	c := resolveMatch(t, r)
	payload := withMatchTailForTest(base, append(extensionRecordForTest(20, 1, recipe), 0))
	if !bytes.Equal(encodeMatchForTest(t, c), payload) {
		t.Fatal("the recipe is not required extension 20 after room content")
	}
	back, err := DecodeMatchConfig(payload)
	if err != nil || back.Digest() != c.Digest() || !bytes.Equal(back.Request().GeneratedMap, recipe) {
		t.Fatalf("the recipe did not round-trip: %v", err)
	}
	if c.Digest() == resolveMatch(t, validMatchRequest(t)).Digest() {
		t.Fatal("the recipe does not change the configuration's identity")
	}
	q := validMatchRequest(t)
	q.Extensions = []MatchConfigExtension{{ID: 20, Required: true, Data: recipe}}
	if got := resolveMatch(t, q); got.Digest() != c.Digest() {
		t.Fatal("a raw record did not project into the typed recipe")
	}
	q.GeneratedMap = []byte(`{"format":1}`)
	if _, err := ResolveMatchConfig(q); err == nil {
		t.Fatal("conflicting typed and raw recipes resolved")
	}
	for _, tc := range []struct {
		name  string
		flags byte
		data  []byte
	}{{"optional", 0, recipe}, {"empty", 1, nil}} {
		if _, err := DecodeMatchConfig(withMatchTailForTest(base, append(extensionRecordForTest(20, tc.flags, tc.data), 0))); err == nil {
			t.Fatalf("%s extension 20 accepted", tc.name)
		}
	}
	survival := validSurvivalRequest(t)
	survival.GeneratedMap = recipe
	if _, err := ResolveMatchConfig(survival); err != nil {
		t.Fatalf("Survival on a generated map: %v", err)
	}
	// Beside Builder speed (19) the recipe follows in ascending ID order.
	both := validMatchRequest(t)
	both.GeneratedMap = recipe
	both.Mutators.BuilderSpeed = content.Factor{Num: 5, Den: 1}
	back, err = DecodeMatchConfig(encodeMatchForTest(t, resolveMatch(t, both)))
	if err != nil || !bytes.Equal(back.Request().GeneratedMap, recipe) || back.Request().Mutators.BuilderSpeed != both.Mutators.BuilderSpeed {
		t.Fatalf("Builder speed and a generated map did not round-trip together: %v", err)
	}
}

// Smart makers is a switch, which field 11's frozen step indices cannot
// spell, so it travels as required extension 21 with the single byte 1,
// omitted when off (docs/DESIGN_MODS_MUTATORS.md "Smart makers"). A
// configuration without it keeps its exact bytes, and with Builder speed the
// two records follow field 16 in ID order.
func TestSmartMakersTravelsAsExtension21(t *testing.T) {
	r := validMatchRequest(t)
	base := encodeMatchForTest(t, resolveMatch(t, r))
	r.Mutators.SmartMakers = content.Factor{Num: 1, Den: 1}
	if got := encodeMatchForTest(t, resolveMatch(t, r)); !bytes.Equal(got, base) {
		t.Fatal("off spelled 1/1 wrote an extension")
	}
	if resolveMatch(t, r).Request().Mutators.SmartMakers != (content.Factor{}) {
		t.Fatal("off did not resolve to the zero factor")
	}
	r.Mutators.SmartMakers = content.SwitchOn()
	c := resolveMatch(t, r)
	payload := withMatchTailForTest(base, append(extensionRecordForTest(21, 1, []byte{1}), 0))
	if !bytes.Equal(encodeMatchForTest(t, c), payload) {
		t.Fatal("Smart makers did not follow field 16 as extension 21")
	}
	back, err := DecodeMatchConfig(payload)
	if err != nil || back.Digest() != c.Digest() || back.Request().Mutators.SmartMakers != content.SwitchOn() {
		t.Fatalf("Smart makers did not round-trip: %v", err)
	}
	q := validMatchRequest(t)
	q.Extensions = []MatchConfigExtension{{ID: 21, Required: true, Data: []byte{1}}}
	if got := resolveMatch(t, q); got.Digest() != c.Digest() || !reflect.DeepEqual(got.Request(), c.Request()) {
		t.Fatal("the raw record did not project into the typed switch")
	}
	// Both spellings must agree.
	q.Mutators.SmartMakers = content.SwitchOn()
	if got := resolveMatch(t, q); got.Digest() != c.Digest() {
		t.Fatal("an agreeing typed switch and raw record did not resolve to one value")
	}
	q.Mutators.SmartMakers = content.Factor{Num: 3, Den: 1}
	if _, err := ResolveMatchConfig(q); err == nil {
		t.Fatal("a typed Smart makers step off its switch beside the raw record resolved")
	}
	r.Mutators.BuilderSpeed = content.Factor{Num: 5, Den: 1}
	both := withMatchTailForTest(base, append(append(extensionRecordForTest(19, 1, []byte{3}), extensionRecordForTest(21, 1, []byte{1})...), 0))
	if !bytes.Equal(encodeMatchForTest(t, resolveMatch(t, r)), both) {
		t.Fatal("Builder speed and Smart makers did not follow field 16 in ID order")
	}
	q = validMatchRequest(t)
	q.Mutators.SmartMakers = content.Factor{Num: 3, Den: 1}
	if _, err := ResolveMatchConfig(q); err == nil {
		t.Fatal("a Smart makers step off its switch resolved")
	}
	q.Mutators.SmartMakers = content.SwitchOn()
	if _, err := resolveMatchConfig(q, 1); err == nil {
		t.Fatal("a version 1 configuration carried Smart makers")
	}
	survival := validSurvivalRequest(t)
	survival.Mutators.SmartMakers = content.SwitchOn()
	if _, err := ResolveMatchConfig(survival); err != nil {
		t.Fatalf("Survival refused Smart makers: %v", err)
	}
	for _, tc := range []struct {
		name  string
		flags byte
		data  []byte
	}{
		{"optional", 0, []byte{1}},
		{"explicit off", 1, []byte{0}},
		{"not one", 1, []byte{2}},
		{"short", 1, nil},
		{"extra", 1, []byte{1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := withMatchTailForTest(base, append(extensionRecordForTest(21, tc.flags, tc.data), 0))
			if c, err := DecodeMatchConfig(payload); err == nil || c.Digest() != ([32]byte{}) {
				t.Fatalf("malformed Smart makers extension accepted: %v", err)
			}
		})
	}
}
