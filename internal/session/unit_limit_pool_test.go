// WU-19-118: the unit pool is sized by the session's per-player unit limit,
// never by the catalog's definition count [05 R-SHARE-01 §7].
package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestSkirmishConfigUnitLimitDefaults locks the configured limit's
// missing-value default and the verbatim copy every stage after the start-up
// read makes of it [08 R-SKIR-01 §6][08 R-SESS-01 §9]. Zero is the absent
// sentinel: the legal range starts at 20, so no stored choice can collide
// with it. Nanolathe's expanded startup range is locked in internal/settings.
func TestSkirmishConfigUnitLimitDefaults(t *testing.T) {
	for _, tc := range []struct {
		in, want int
	}{
		{0, SkirmishDefaultUnitLimit},
		{-5, -5},
		{19, 19},
		{20, 20},
		{250, 250},
		{500, 500},
		{501, 501},
		{100000, 100000},
	} {
		if got := unitLimitOrDefault(tc.in); got != tc.want {
			t.Errorf("unitLimitOrDefault(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}

	// Both normalization entry points install the same value, and both are
	// idempotent.
	var applied SkirmishConfig
	applied.MapName = "m"
	applied.ApplyDefaults()
	applied.ApplyDefaults()
	if applied.UnitLimit != SkirmishDefaultUnitLimit {
		t.Fatalf("ApplyDefaults UnitLimit = %d, want %d", applied.UnitLimit, SkirmishDefaultUnitLimit)
	}

	// DirectSkirmishConfig is the shape both the displayless runner and the
	// headless command compose, so the default has to survive it.
	direct := DirectSkirmishConfig("m")
	if direct.UnitLimit != SkirmishDefaultUnitLimit {
		t.Fatalf("DirectSkirmishConfig UnitLimit = %d, want %d", direct.UnitLimit, SkirmishDefaultUnitLimit)
	}

	// Past the start-up read the word is copied verbatim: neither
	// normalization entry point nor the session copy re-applies the clamp,
	// so a restored `Summary.maxunits` outside 20..500 reaches the next
	// battle as it stands [08 R-SESS-01 §9].
	carried := DirectSkirmishConfig("m")
	carried.UnitLimit = 9000
	if err := carried.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if carried.UnitLimit != 9000 {
		t.Fatalf("Normalize UnitLimit = %d, want the verbatim 9000", carried.UnitLimit)
	}
	carried.ApplyDefaults()
	if carried.UnitLimit != 9000 {
		t.Fatalf("ApplyDefaults UnitLimit = %d, want the verbatim 9000", carried.UnitLimit)
	}
	if got := sessionUnitLimit(&Session{Skirmish: carried}); got != 9000 {
		t.Fatalf("sessionUnitLimit = %d, want the verbatim 9000", got)
	}
	if got := sessionUnitLimit(&Session{}); got != SkirmishDefaultUnitLimit {
		t.Fatalf("sessionUnitLimit(absent) = %d, want %d", got, SkirmishDefaultUnitLimit)
	}
}

// TestSkirmishPoolSizedByUnitLimit is the sizing contract itself: `limit × 10
// + 1` records with exactly `limit` per slot, whatever the catalog holds
// [05 R-SHARE-01 §7]. The stock catalog's definition count is deliberately
// asserted to differ from the limit, because the two used to be the same
// number and a regression would otherwise pass unnoticed.
func TestSkirmishPoolSizedByUnitLimit(t *testing.T) {
	root := aiE2ERetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount retail install: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	cfg := DirectSkirmishConfig("ashap plateau")
	cfg.Gameplay = gameplay.Strict31
	cfg.RNGSimSeed = aiE2ESeed
	cfg.RNGCrtSeed = aiE2ESeed
	sess, err := NewSkirmishWithProgress(fs, nil, cfg, nil)
	if err != nil {
		t.Fatalf("compose skirmish: %v", err)
	}
	limit := SkirmishDefaultUnitLimit
	if got := sess.Units.TotalRecords(); got != limit*10+1 {
		t.Fatalf("TotalRecords = %d, want %d", got, limit*10+1)
	}
	if defs := len(sess.Catalog.Units); defs == limit {
		t.Fatalf("catalog definition count %d equals the limit, so this test cannot tell the two apart", defs)
	}
	for player := 0; player < 10; player++ {
		start, end, ok := sess.Units.SliceForPlayer(player)
		if !ok {
			t.Fatalf("player %d: no slice", player)
		}
		if start != limit*player+1 || end != limit*(player+1) {
			t.Fatalf("player %d slice = %d..%d, want %d..%d", player, start, end, limit*player+1, limit*(player+1))
		}
	}
	// The one word every later consumer reads — the AI's half-capacity term
	// [08 R-AI-01 §13] and the path scheduler's tiering — must describe the
	// slice that was actually allocated.
	if got := sessionUnitLimit(sess); int(got) != limit {
		t.Fatalf("sessionUnitLimit = %d, want %d", got, limit)
	}
}

// TestCampaignUnitLimitIsMissionMaxUnits locks the campaign source: the OTA
// `maxunits` key, whose missing-value default is 200 — not the skirmish
// preference [08 R-SKIR-01 §6].
func TestCampaignUnitLimitIsMissionMaxUnits(t *testing.T) {
	if got := campaignUnitLimit(nil); got != 200 {
		t.Fatalf("campaignUnitLimit(nil) = %d, want the missing-key default 200", got)
	}
}
