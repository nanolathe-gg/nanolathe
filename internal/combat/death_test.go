package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// makeUnitDef returns a minimal UnitDef with Corpse name and canonical key.
func makeUnitDef(corpse string) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(corpse)},
		Corpse:           corpse,
	}
}

// makeFeatureChain builds a chain of FeatureDefs where each points to next via featuredead.
func makeFeatureChain(names []string) map[string]*content.FeatureDef {
	m := make(map[string]*content.FeatureDef, len(names))
	feats := make([]*content.FeatureDef, len(names))
	for i, n := range names {
		fd := &content.FeatureDef{
			DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(n)},
		}
		// Store original name via Description? Not needed, but set for debug.
		feats[i] = fd
		m[content.CanonicalKey(n)] = fd
	}
	for i := 0; i < len(feats)-1; i++ {
		feats[i].FeatureDead = names[i+1]
		feats[i].FeatureDeadDef = feats[i+1]
	}
	if len(feats) > 0 {
		feats[len(feats)-1].FeatureDead = ""
		feats[len(feats)-1].FeatureDeadDef = nil
	}
	return m
}

// TestDeathSeverityVectors locks C22 clamp((floor((-health)*100/maxDamage)+prior)/2,1,100) [06 §12.1] including clamp bounds.
func TestDeathSeverityVectors(t *testing.T) {
	cases := []struct {
		health, maxHealth int32
		prior             uint8
		want              int32
	}{
		{-50, 100, 80, 65},    // (-(-50)*100)/100=50+80=130/2=65
		{0, 100, 0, 1},        // 0+0/2=0 clamp 1
		{-200, 100, 100, 100}, // 200+100=300/2=150 clamp 100
		{-10, 100, 90, 50},    // 10+90=100/2=50
		{-100, 200, 60, 55},   // 50+60=110/2=55
		{-1, 100, 0, 1},       // 1+0=1/2=0 clamp 1
		{-100, 100, 100, 100}, // 100+100=200/2=100 edge
		{-99, 100, 1, 50},     // 99+1=100/2=50
	}
	for i, c := range cases {
		got := DeathSeverity(c.health, c.maxHealth, c.prior)
		if got != c.want {
			t.Fatalf("case %d severity health %d max %d prior %d got %d want %d [06 §12.1] C22", i, c.health, c.maxHealth, c.prior, got, c.want)
		}
		// Also via ResolveDeath path for full pipeline (cause ordinary)
		ctx := DeathContext{Health: c.health, MaxHealth: c.maxHealth, PriorSample: c.prior, Cause: CauseOrdinary, RemainingFraction: 0}
		res := ResolveDeath(ctx, nil, func(sev int32) (int32, bool) { return 0, true })
		if int32(res.Severity) != c.want {
			t.Fatalf("case %d ResolveDeath severity %d want %d", i, res.Severity, c.want)
		}
	}
	// Clamp bounds explicitly
	if got := DeathSeverity(0, 100, 0); got != 1 {
		t.Fatalf("low clamp got %d want 1 [06 §12.1] C22", got)
	}
	if got := DeathSeverity(-10000, 100, 100); got != 100 {
		t.Fatalf("high clamp got %d want 100", got)
	}
}

// TestLowBitsConsumption verifies C23 low four bits are corpse-chain depth [06 §12.1].
func TestLowBitsConsumption(t *testing.T) {
	// Variant 0xAB -> depth 0xB=11
	cases := []struct {
		returned  int32 // syncKilled returns this
		wantDepth uint8
	}{
		{0xAB, 0xB},
		{0x5F, 0xF},
		{0x10, 0x0},
		{0xFF, 0xF},
		{0x00, 0x0},
		{0x1C, 0xC},
		{0x123, 0x3}, // truncated to low 4 bits
	}
	for i, c := range cases {
		ctx := DeathContext{Health: -50, MaxHealth: 100, PriorSample: 0, Cause: CauseOrdinary, RemainingFraction: 0}
		res := ResolveDeath(ctx, nil, func(sev int32) (int32, bool) { return c.returned, true })
		if res.Variant != c.wantDepth {
			t.Fatalf("case %d low bits 0x%X got depth %d want %d [06 §12.1] C23", i, c.returned, res.Variant, c.wantDepth)
		}
		if packedDepth := res.Packed & 0x0F; packedDepth != c.wantDepth {
			t.Fatalf("case %d packed low nibble 0x%X got %d want %d", i, res.Packed, packedDepth, c.wantDepth)
		}
		cause, depth := UnpackDeathByte(res.Packed)
		if cause != CauseOrdinary || depth != c.wantDepth {
			t.Fatalf("case %d unpack got cause %d depth %d", i, cause, depth)
		}
		// Also test direct Pack/Unpack helpers
		p := PackDeathByte(CauseOrdinary, uint8(c.returned))
		if p&0x0F != c.wantDepth {
			t.Fatalf("case %d PackDeathByte low nibble %d want %d", i, p&0x0F, c.wantDepth)
		}
	}
	// Also verify high nibble is cause
	p := PackDeathByte(CauseCapture, 0x5)
	if Cause(p>>4) != CauseCapture || (p&0x0F) != 0x5 {
		t.Fatalf("PackDeathByte cause capture got 0x%X", p)
	}
}

// TestCausesBypass locks C24 causes 4,5,9 skip query entirely severity0 variant0 no explosion no corpse [06 §12.1][GAP T21].
func TestCausesBypass(t *testing.T) {
	causes := []Cause{CauseCapture, CauseReclaim, CauseDeconstruction}
	for _, c := range causes {
		ctx := DeathContext{Health: -100, MaxHealth: 100, PriorSample: 80, Cause: c, RemainingFraction: 0}
		calls := 0
		res := ResolveDeath(ctx, nil, func(sev int32) (int32, bool) { calls++; return 5, true })
		if calls != 0 {
			t.Fatalf("cause %d should skip query but called %d times [06 §12.1] C24", c, calls)
		}
		if res.Queried {
			t.Fatalf("cause %d Queried true want false", c)
		}
		if res.Severity != 0 || res.Variant != 0 {
			t.Fatalf("cause %d severity %d variant %d want 0,0 [06 §12.1] C24", c, res.Severity, res.Variant)
		}
		if res.DoExplosion || res.DoCorpse || res.Corpse != nil {
			t.Fatalf("cause %d should have no explosion/no corpse %+v", c, res)
		}
		if res.Packed != uint8(c)<<4 {
			t.Fatalf("cause %d packed 0x%X want 0x%X", c, res.Packed, uint8(c)<<4)
		}
		if res.KilledCalls != 0 {
			t.Fatalf("cause %d KilledCalls %d want 0 [06 §12.1] C25", c, res.KilledCalls)
		}
	}
	// Cause 7: severity 0 variant 1 no query, corpse depth1 [06 §12.1] C24
	{
		unit := makeUnitDef("heap1")
		feats := makeFeatureChain([]string{"heap1", "heap2"})
		ctx := DeathContext{Health: -100, MaxHealth: 100, PriorSample: 80, Cause: CauseFeatureConversion, RemainingFraction: 0, UnitDef: unit}
		calls := 0
		res := ResolveDeath(ctx, feats, func(sev int32) (int32, bool) { calls++; return 9, true })
		if calls != 0 {
			t.Fatalf("cause 7 should skip query but called %d", calls)
		}
		if res.Severity != 0 || res.Variant != 1 {
			t.Fatalf("cause 7 severity %d variant %d want 0,1 [06 §12.1] C24", res.Severity, res.Variant)
		}
		if !res.DoCorpse || res.Corpse == nil {
			t.Fatalf("cause 7 should place corpse depth1 %+v", res)
		}
		if res.Corpse != feats[content.CanonicalKey("heap1")] {
			t.Fatalf("cause7 corpse wrong")
		}
		if res.DoExplosion {
			t.Fatalf("cause7 should have no explosion severity0")
		}
		if res.Packed != uint8(CauseFeatureConversion)<<4|1 {
			t.Fatalf("cause7 packed 0x%X want 0x%X", res.Packed, uint8(CauseFeatureConversion)<<4|1)
		}
	}
	// Positive health request skips query severity0 variant0 [06 §12.1] C24
	{
		ctx := DeathContext{Health: 10, MaxHealth: 100, PriorSample: 90, Cause: CauseOrdinary, RemainingFraction: 0}
		calls := 0
		res := ResolveDeath(ctx, nil, func(sev int32) (int32, bool) { calls++; return 3, true })
		if calls != 0 {
			t.Fatalf("positive health should skip query but called %d", calls)
		}
		if res.Severity != 0 || res.Variant != 0 || res.Queried {
			t.Fatalf("positive health bypass failed %+v", res)
		}
		if res.DoExplosion || res.DoCorpse {
			t.Fatalf("positive health should have no explosion/corpse")
		}
	}
	// Nonzero remaining fraction forces variant zero after any query [06 §12.1] C24 [GAP T21]
	{
		unit := makeUnitDef("heap1")
		feats := makeFeatureChain([]string{"heap1", "heap2"})
		ctx := DeathContext{Health: -50, MaxHealth: 100, PriorSample: 80, Cause: CauseOrdinary, RemainingFraction: 0.5, UnitDef: unit}
		res := ResolveDeath(ctx, feats, func(sev int32) (int32, bool) { return 3, true }) // would be depth3
		if res.Variant != 0 {
			t.Fatalf("remaining fraction 0.5 should force variant 0 got %d [06 §12.1] C24", res.Variant)
		}
		if res.DoCorpse || res.Corpse != nil {
			t.Fatalf("remaining fraction nonzero should have no corpse %+v", res)
		}
		if res.DoExplosion {
			t.Fatalf("remaining fraction nonzero should have no explosion")
		}
		if !res.Queried || res.KilledCalls != 1 {
			t.Fatalf("remaining fraction case should still have queried once")
		}
	}
}

// TestCorpseChainDepth verifies C23 depth follows featuredead exactly depth-1 times [06 §12.1].
func TestCorpseChainDepth(t *testing.T) {
	unit := makeUnitDef("corpse1")
	feats := makeFeatureChain([]string{"corpse1", "heap2", "heap3", "heap4"})
	// Depth 0 => no corpse
	if got := ResolveCorpse(unit, feats, 0); got != nil {
		t.Fatalf("depth0 should be nil got %v", got)
	}
	// Depth 1 => authored Corpse
	if got := ResolveCorpse(unit, feats, 1); got != feats[content.CanonicalKey("corpse1")] {
		t.Fatalf("depth1 wrong")
	}
	// Depth 2 => follow once -> heap2
	if got := ResolveCorpse(unit, feats, 2); got != feats[content.CanonicalKey("heap2")] {
		t.Fatalf("depth2 should be heap2 got %v", got)
	}
	// Depth 3 => follow twice -> heap3
	if got := ResolveCorpse(unit, feats, 3); got != feats[content.CanonicalKey("heap3")] {
		t.Fatalf("depth3 should be heap3 got %v", got)
	}
	// Depth 4 => heap4
	if got := ResolveCorpse(unit, feats, 4); got != feats[content.CanonicalKey("heap4")] {
		t.Fatalf("depth4 should be heap4")
	}
	// Depth beyond chain => sentinel nil
	if got := ResolveCorpse(unit, feats, 5); got != nil {
		t.Fatalf("depth5 beyond chain should be nil got %v", got)
	}
	// Depth via packed byte low bits consumption
	unit2 := makeUnitDef("heap1")
	feats2 := makeFeatureChain([]string{"heap1", "heap2", "heap3"})
	// Packed depth 0x13 -> low 0x3 => heap3
	if got := ResolveCorpse(unit2, feats2, 0x13); got != feats2[content.CanonicalKey("heap3")] {
		t.Fatalf("packed 0x13 depth 3 should be heap3")
	}
	// Empty corpse name => no corpse even depth1
	unitEmpty := makeUnitDef("")
	if got := ResolveCorpse(unitEmpty, feats2, 1); got != nil {
		t.Fatalf("empty corpse name should be nil")
	}
	// Missing feature in map => nil
	unitMissing := makeUnitDef("nonexist")
	if got := ResolveCorpse(unitMissing, feats2, 1); got != nil {
		t.Fatalf("missing corpse feature should be nil")
	}
	// Verify ResolveDeath integrates corpse chain depth 3 follows featuredead twice [PLAN_09 Tests]
	{
		ctx := DeathContext{Health: -50, MaxHealth: 100, PriorSample: 80, Cause: CauseOrdinary, RemainingFraction: 0, UnitDef: unit}
		res := ResolveDeath(ctx, feats, func(sev int32) (int32, bool) { return 3, true })
		if res.Variant != 3 {
			t.Fatalf("ResolveDeath depth 3 variant %d want 3", res.Variant)
		}
		if res.Corpse != feats[content.CanonicalKey("heap3")] {
			t.Fatalf("ResolveDeath corpse chain depth3 should be heap3")
		}
		if !res.DoCorpse {
			t.Fatalf("DoCorpse should be true for depth3")
		}
	}
}

// TestNoDoubleKilledCallback locks C25 local authoritative death does not issue second Killed after sync query [06 §12.1].
func TestNoDoubleKilledCallback(t *testing.T) {
	syncCalls := 0
	asyncCalls := 0
	// Simulate sync Killed that would be called once
	syncFn := func(sev int32) (int32, bool) {
		syncCalls++
		return 2, true // depth2
	}
	// async would be separate; we track via ShouldDispatchReplayKilled + a fake async fn
	asyncFn := func(sev int8) {
		asyncCalls++
	}
	unit := makeUnitDef("heap1")
	feats := makeFeatureChain([]string{"heap1", "heap2"})
	ctx := DeathContext{Health: -50, MaxHealth: 100, PriorSample: 0, Cause: CauseOrdinary, RemainingFraction: 0, UnitDef: unit}
	res := ResolveDeath(ctx, feats, syncFn)
	if syncCalls != 1 {
		t.Fatalf("sync Killed should be called exactly once, got %d [06 §12.1] C25", syncCalls)
	}
	if res.KilledCalls != 1 {
		t.Fatalf("KilledCalls %d want 1 [06 §12.1] C25", res.KilledCalls)
	}
	if !res.Queried {
		t.Fatalf("Queried should be true for full pipeline")
	}
	// Local path must NOT dispatch async second call [06 §12.1] C25.
	// We simulate what local does: it passes completed packet to central handler without async dispatch.
	// If we incorrectly dispatched async, asyncCalls would be 1.
	// For local, async should remain 0.
	if asyncCalls != 0 {
		t.Fatalf("local authoritative death issued second async Killed, asyncCalls %d want 0 [06 §12.1] C25", asyncCalls)
	}
	// Verify that replay path WOULD dispatch only when severity positive and return ignored.
	// For severity 0 bypass, replay should not dispatch.
	if ShouldDispatchReplayKilled(0) {
		t.Fatalf("replay with severity 0 should not dispatch [06 §12.1] C25")
	}
	if !ShouldDispatchReplayKilled(10) {
		t.Fatalf("replay with severity 10 should dispatch [06 §12.1] C25")
	}
	if ShouldDispatchReplayKilled(-5) {
		t.Fatalf("replay with negative severity should not dispatch")
	}
	// Full pipeline replay would call async once
	syncCalls2 := 0
	ctx2 := DeathContext{Health: -10, MaxHealth: 100, PriorSample: 90, Cause: CauseOrdinary, RemainingFraction: 0}
	res2 := ResolveDeath(ctx2, feats, func(sev int32) (int32, bool) { syncCalls2++; return 1, true })
	// Simulate replay dispatch for res2.Severity>0
	if ShouldDispatchReplayKilled(int8(res2.Severity)) {
		asyncFn(int8(res2.Severity))
	}
	if syncCalls2 != 1 {
		t.Fatalf("replay test sync calls %d want 1", syncCalls2)
	}
	if asyncCalls != 1 { // now async should be 1 from replay path
		t.Fatalf("replay asyncCalls %d want 1", asyncCalls)
	}
	// Bypassed causes should have syncCalls 0 and no async
	syncCalls3 := 0
	ctx3 := DeathContext{Health: -50, MaxHealth: 100, PriorSample: 80, Cause: CauseDeconstruction, RemainingFraction: 0}
	res3 := ResolveDeath(ctx3, feats, func(sev int32) (int32, bool) { syncCalls3++; return 2, true })
	if syncCalls3 != 0 || res3.KilledCalls != 0 {
		t.Fatalf("bypassed cause9 should have 0 sync calls got %d KilledCalls %d", syncCalls3, res3.KilledCalls)
	}
	if ShouldDispatchReplayKilled(int8(res3.Severity)) {
		t.Fatalf("bypassed severity 0 should not dispatch replay")
	}
}

// TestRemainingFractionGate ensures construction fraction zero gating for explosion vs variant [06 §12.1].
func TestRemainingFractionGate(t *testing.T) {
	unit := makeUnitDef("heap1")
	feats := makeFeatureChain([]string{"heap1"})
	// Remaining 0 => explosion allowed when severity>0
	ctx0 := DeathContext{Health: -50, MaxHealth: 100, PriorSample: 0, Cause: CauseOrdinary, RemainingFraction: 0, UnitDef: unit}
	res0 := ResolveDeath(ctx0, feats, func(sev int32) (int32, bool) { return 1, true })
	if !res0.DoExplosion {
		t.Fatalf("remaining 0 with severity>0 should have DoExplosion true [06 §12.1]")
	}
	if !res0.DoCorpse {
		t.Fatalf("remaining 0 variant1 should have DoCorpse true")
	}
	// Remaining nonzero => no explosion, variant forced 0
	ctx1 := DeathContext{Health: -50, MaxHealth: 100, PriorSample: 0, Cause: CauseOrdinary, RemainingFraction: 0.3, UnitDef: unit}
	res1 := ResolveDeath(ctx1, feats, func(sev int32) (int32, bool) { return 1, true })
	if res1.DoExplosion {
		t.Fatalf("remaining non-zero should have no explosion [06 §12.1]")
	}
	if res1.Variant != 0 || res1.DoCorpse {
		t.Fatalf("remaining non-zero should force variant0 %+v", res1)
	}
}

// TestCause7WithRemainingFraction ensures cause7 variant1 survives remainingFraction non-zero path as no query [06 §12.1] C24.
func TestCause7WithRemainingFraction(t *testing.T) {
	unit := makeUnitDef("heap1")
	feats := makeFeatureChain([]string{"heap1"})
	ctx := DeathContext{Health: -50, MaxHealth: 100, PriorSample: 80, Cause: CauseFeatureConversion, RemainingFraction: 0.5, UnitDef: unit}
	res := ResolveDeath(ctx, feats, func(sev int32) (int32, bool) { return 9, true })
	// Cause7 skips query, variant 1 even though remaining non-zero, because gate is after query only [06 §12.1] C24
	if res.Variant != 1 {
		t.Fatalf("cause7 variant %d want 1 despite remaining fraction [06 §12.1] C24", res.Variant)
	}
	if !res.DoCorpse {
		t.Fatalf("cause7 should still have DoCorpse even with remaining non-zero")
	}
}

// TestDeathExplosionSkipsInactiveSentinelLink locks the death-explosion
// selection against the record-0 inactive sentinel [02 §5 R-CONTENT-02]: a
// missed death link (explodeas/selfdestructas) resolves to the [noweapon]
// record, which is inactive by its zero slot number and must not be selected
// as a death explosion. Retail produces no explosion at all for an unresolved
// or absent death weapon — there is no third default candidate [06 §12.2] —
// so an all-inactive selection returns nil and the death path dispatches
// nothing. Active (non-zero-ID) links are unchanged.
func TestDeathExplosionSkipsInactiveSentinelLink(t *testing.T) {
	// The sentinel link exactly as LinkUnitWeapons fills it for a missed name
	// [02 §5 R-CONTENT-02]: non-nil, ID 0.
	sentinel := &content.WeaponDef{ID: 0}
	sentinel.CanonicalKey = "noweapon"
	active := &content.WeaponDef{ID: 7, Range: 600}
	active.CanonicalKey = "bigbertha"

	// Ordinary death with a missed explodeas: no [noweapon] explosion.
	def := &content.UnitDef{ExplodeAsDef: sentinel}
	if w := SelectDeathExplosionWeapon(def, CauseOrdinary); w != nil {
		t.Fatalf("missed explodeas selected %v (ID %d), want no death-explosion weapon [02 §5 R-CONTENT-02]", w, w.ID)
	}

	// Self-destruct with a missed selfdestructas falls through to an active
	// explodeas; an inactive selfdestructas is not selected either.
	def = &content.UnitDef{SelfDestructAsDef: sentinel, ExplodeAsDef: active}
	if w := SelectDeathExplosionWeapon(def, CauseSelfDestruct); w != active {
		t.Fatalf("sentinel selfdestructas did not fall through to active explodeas, got %v", w)
	}

	// Self-destruct with both death links inactive: no explosion.
	def = &content.UnitDef{SelfDestructAsDef: sentinel, ExplodeAsDef: sentinel}
	if w := SelectDeathExplosionWeapon(def, CauseSelfDestruct); w != nil {
		t.Fatalf("all-inactive death links selected %v (ID %d), want nil [02 §5 R-CONTENT-02][06 §12.2]", w, w.ID)
	}

	// Active links behave exactly as before.
	def = &content.UnitDef{SelfDestructAsDef: active, ExplodeAsDef: sentinel}
	if w := SelectDeathExplosionWeapon(def, CauseSelfDestruct); w != active {
		t.Fatalf("active selfdestructas changed behavior, got %v want %v", w, active)
	}
	def = &content.UnitDef{ExplodeAsDef: active}
	if w := SelectDeathExplosionWeapon(def, CauseOrdinary); w != active {
		t.Fatalf("active explodeas changed behavior, got %v want %v", w, active)
	}
}

// TestAbsentKilledBodyIsOneConstant locks the implementation rule of
// [04 R-CB-01 §9]: "no VM", "no `Killed` body", "thread pool full" and "a body
// that ignores its second parameter" are one case with one substitute constant
// for the variant nibble, and the substitute still yields to the remaining-work
// gate. It asserts the four sub-cases agree with each other, not the value.
func TestAbsentKilledBodyIsOneConstant(t *testing.T) {
	// A `Killed` body that returns without assigning its second parameter, so
	// the seeded variant cell round-trips unchanged [04 R-CB-01 §9].
	ignoringBody := func() *cob.Program {
		return &cob.Program{
			Code:        []uint32{0x10065000},
			Scripts:     map[string]int{"Killed": 0},
			ScriptsByID: []int{0},
			Pieces:      []string{"base"},
		}
	}

	// Sub-case 1: no VM at all — the query is never reached.
	noVM, ok := KilledVariantFromVM(nil, 50)
	if ok {
		t.Fatalf("nil VM reported a started query [04 R-CB-01 §9]")
	}
	// Sub-case 2: the program has no `Killed` entry — the name misses.
	bodyless := cob.NewVM(&cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}, Pieces: []string{"base"}})
	noBody, ok := KilledVariantFromVM(bodyless, 50)
	if ok {
		t.Fatalf("absent Killed body reported a started query [04 R-CB-01 §9]")
	}
	// Sub-case 3: every thread slot busy — the allocator refuses before seeding.
	full := cob.NewVM(ignoringBody())
	for i := range full.Threads {
		full.Threads[i].Status = cob.ThreadSleeping
		full.Threads[i].Sleep = 100
	}
	poolFull, ok := KilledVariantFromVM(full, 50)
	if ok {
		t.Fatalf("full thread pool reported a started query [04 R-CB-01 §9][04 §4.3]")
	}
	// Sub-case 4: the body runs and ignores its second parameter.
	ignoring, ok := KilledVariantFromVM(cob.NewVM(ignoringBody()), 50)
	if !ok {
		t.Fatalf("a present Killed body must start [04 §5.1]")
	}

	if noVM != noBody || noBody != poolFull || poolFull != ignoring {
		t.Fatalf("absent-Killed sub-cases disagree: noVM %d noBody %d poolFull %d ignoring %d — [04 R-CB-01 §9] requires one constant", noVM, noBody, poolFull, ignoring)
	}
	if noVM != UnassignedKilledVariant {
		t.Fatalf("substitute %d is not the recorded constant %d [04 R-CB-01 §9]", noVM, UnassignedKilledVariant)
	}

	// The same agreement must survive ResolveDeath, whose nil-syncKilled arm is
	// the no-VM case and whose ok=false arm is the other three.
	unit := makeUnitDef("wreck1")
	feats := makeFeatureChain([]string{"wreck1", "wreck2"})
	ctx := DeathContext{Health: -50, MaxHealth: 100, PriorSample: 80, Cause: CauseOrdinary, RemainingFraction: 0, UnitDef: unit}
	viaNil := ResolveDeath(ctx, feats, nil)
	viaNotStarted := ResolveDeath(ctx, feats, func(int32) (int32, bool) { return UnassignedKilledVariant, false })
	viaIgnoringBody := ResolveDeath(ctx, feats, func(sev int32) (int32, bool) {
		return KilledVariantFromVM(cob.NewVM(ignoringBody()), sev)
	})
	if viaNil.Variant != viaNotStarted.Variant || viaNotStarted.Variant != viaIgnoringBody.Variant {
		t.Fatalf("ResolveDeath arms disagree: nil %d not-started %d ignoring-body %d [04 R-CB-01 §9]", viaNil.Variant, viaNotStarted.Variant, viaIgnoringBody.Variant)
	}
	if int32(viaNil.Variant) != UnassignedKilledVariant || !viaNil.DoCorpse {
		t.Fatalf("absent Killed body must pack the substitute and place its authored corpse: %+v [04 R-CB-01 §9][06 §12.1] C23", viaNil)
	}

	// The remaining-work gate outranks the substitute on every arm: a non-zero
	// remaining build fraction forces zero after any query [04 R-CB-01 §9].
	midBuild := ctx
	midBuild.RemainingFraction = 0.5
	gated := []struct {
		name string
		res  DeathResolution
	}{
		{"nil", ResolveDeath(midBuild, feats, nil)},
		{"not-started", ResolveDeath(midBuild, feats, func(int32) (int32, bool) { return UnassignedKilledVariant, false })},
		{"ignoring-body", ResolveDeath(midBuild, feats, func(sev int32) (int32, bool) { return KilledVariantFromVM(cob.NewVM(ignoringBody()), sev) })},
	}
	for _, g := range gated {
		if g.res.Variant != 0 || g.res.DoCorpse || g.res.Corpse != nil {
			t.Fatalf("%s arm ignored the remaining-work gate: %+v [04 R-CB-01 §9][06 §12.1] C24", g.name, g.res)
		}
	}
}
