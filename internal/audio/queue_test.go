package audio

import (
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
)

func categoryFixture() *Category {
	c := &Category{Name: "testcat"}
	// Provide rows for used slots
	c.Rows[1].Variants = []string{"sel_a", "sel_b"}
	c.Rows[1].Captions = []string{"", ""}
	c.Rows[2].Variants = []string{"warn"}
	c.Rows[2].Captions = []string{""}
	c.Rows[3].Variants = []string{"act"}
	c.Rows[5].Variants = []string{"ok"}
	c.Rows[6].Variants = []string{"arr"}
	c.Rows[7].Variants = []string{"cant"}
	c.Rows[8].Variants = []string{"complete"}
	c.Rows[9].Variants = []string{"build"}
	c.Rows[11].Variants = []string{"working"}
	c.Rows[12].Variants = []string{"load"}
	c.Rows[13].Variants = []string{"unload"}
	c.Rows[14].Variants = []string{"cloak"}
	c.Rows[15].Variants = []string{"uncloak"}
	c.Rows[16].Variants = []string{"cap"}
	return c
}

func TestSlotTableStatic(t *testing.T) {
	want := [24]slotInfo{
		0:  {"", "", 0, 0},
		1:  {"select", "", 10, 0},
		2:  {"underattack", "Under Attack", 9, 20},
		3:  {"activate", "", 4, 2},
		4:  {"deactivate", "", 4, 2},
		5:  {"ok", "", 5, 1},
		6:  {"arrived", "Arrived", 3, 4},
		7:  {"cant", "Cannot Comply", 8, 1},
		8:  {"unitcomplete", "Nanolathe Complete", 3, 3},
		9:  {"build", "", 4, 2},
		10: {"repair", "", 3, 1},
		11: {"working", "", 2, 1},
		12: {"load", "", 7, 1},
		13: {"unload", "", 7, 1},
		14: {"cloak", "Cloaked", 7, 1},
		15: {"uncloak", "Visible", 7, 1},
		16: {"capture", "", 4, 1},
		17: {"count5", "five", 10, 0},
		18: {"count4", "four", 10, 0},
		19: {"count3", "three", 10, 0},
		20: {"count2", "two", 10, 0},
		21: {"count1", "one", 10, 0},
		22: {"count0", "zero", 10, 0},
		23: {"canceldestruct", "Self destruct terminated", 10, 0},
	}
	if len(slotTable) != len(want) {
		t.Fatalf("slot table len %d want %d", len(slotTable), len(want))
	}
	for i := range want {
		if slotTable[i] != want[i] {
			t.Fatalf("slot %d = %+v want %+v", i, slotTable[i], want[i])
		}
	}
}

func TestCategoryIdentity(t *testing.T) {
	var c Category
	if len(c.Rows) != 24 {
		t.Fatalf("rows len %d want 24", len(c.Rows))
	}
	c.Name = strings.Repeat("x", 64)
	if len(c.Name) != 64 {
		t.Fatal("name should support 64 bytes")
	}
	c.Rows[1].Variants = []string{"a", "b"}
	c.Rows[1].Captions = []string{"cap_a", "cap_b"}
	if len(c.Rows[1].Variants) != 2 || c.Rows[1].Captions[1] != "cap_b" {
		t.Fatal("row variants/captions parallel arrays not preserved")
	}
}

func TestInsertCooldownDrop(t *testing.T) {
	q := NewQueue()
	cat := categoryFixture()
	h := pool.Handle(1)
	q.Register(h, cat, "Peewee", true)
	var plays []string
	q.OnPlay(func(alias string, slot Slot, unit pool.Handle) { plays = append(plays, alias) })
	// Insert underattack at 90
	if !q.InsertAt(90, 2, h, "") {
		t.Fatal("first underattack insert rejected")
	}
	q.Drain(90) // audible, arms cooldown 20*30=600 -> nextAllowed 690
	if len(plays) != 1 {
		t.Fatalf("plays %v want 1", plays)
	}
	plays = nil
	// Before cooldown should drop
	if q.InsertAt(600, 2, h, "") {
		t.Fatal("cooldown should gate insert at 600")
	}
	if q.Count != 0 {
		t.Fatalf("count %d want 0 after drain", q.Count)
	}
	// At exactly 690 should pass
	if !q.InsertAt(690, 2, h, "") {
		t.Fatal("cooldown should lift at 690")
	}
	// duplicate slot should drop while queued
	if q.InsertAt(690, 2, h, "") {
		t.Fatal("duplicate slot should be dropped")
	}
}

func TestInsertDuplicateDrop(t *testing.T) {
	q := NewQueue()
	cat := categoryFixture()
	q.Register(1, cat, "U", true)
	q.Register(2, cat, "U", true)
	if !q.InsertAt(100, 5, 1, "") {
		t.Fatal("first ok insert failed")
	}
	if q.InsertAt(100, 5, 2, "") {
		t.Fatal("duplicate slot should be dropped")
	}
	if q.Count != 1 {
		t.Fatalf("count %d want 1", q.Count)
	}
}

func TestInsertOrdering(t *testing.T) {
	q := NewQueue()
	_ = categoryFixture()
	// Deactivate pri4, Activate pri4, Build pri4 -> equal priorities FIFO
	// But we test descending: cant pri8, load pri7, ok pri5, build pri4, working pri2
	// Insert in random order, check sorted
	// Use slots: 7(cant 8),12(load7),5(ok5),9(build4),11(working2)
	// Insert low then high, queue should be high first
	q = NewQueue()
	if !q.InsertAt(0, 11, 1, "") {
		t.Fatal("insert working")
	}
	if !q.InsertAt(0, 7, 1, "") {
		t.Fatal("insert cant")
	}
	if !q.InsertAt(0, 5, 1, "") {
		t.Fatal("insert ok")
	}
	if q.Count != 3 {
		t.Fatalf("count %d", q.Count)
	}
	// Expect order: cant(8), ok(5), working(2) — because load not inserted yet
	if q.Entries[0].Slot != 7 || q.Entries[1].Slot != 5 || q.Entries[2].Slot != 11 {
		t.Fatalf("order %v want cant,ok,working", []Slot{q.Entries[0].Slot, q.Entries[1].Slot, q.Entries[2].Slot})
	}
	// Equal priority FIFO: insert deactivate, activate, build all pri4 in that order
	q2 := NewQueue()
	if !q2.InsertAt(0, 4, 1, "") {
		t.Fatal("deactivate")
	}
	if !q2.InsertAt(0, 3, 1, "") {
		t.Fatal("activate")
	}
	if !q2.InsertAt(0, 9, 1, "") {
		t.Fatal("build")
	}
	if q2.Entries[0].Slot != 4 || q2.Entries[1].Slot != 3 || q2.Entries[2].Slot != 9 {
		t.Fatalf("equal FIFO order %v want 4,3,9", []Slot{q2.Entries[0].Slot, q2.Entries[1].Slot, q2.Entries[2].Slot})
	}
}

func TestAudioInsertOrdering(t *testing.T) {
	q := NewQueue()
	cat := categoryFixture()
	q.Register(1, cat, "Peewee", true)
	// equal priorities inserted out of key order must stay FIFO
	for _, slot := range []Slot{4, 3, 9} {
		if !q.InsertAt(3000, slot, 1, "") {
			t.Fatalf("insert %d", slot)
		}
	}
	// Drain to verify order preserved through resolves? Check entries directly
	if q.Entries[0].Slot != 4 || q.Entries[1].Slot != 3 || q.Entries[2].Slot != 9 {
		t.Fatalf("FIFO after equals failed")
	}
}

func TestFullQueueSilentEviction(t *testing.T) {
	q := NewQueue()
	cat := categoryFixture()
	q.Register(1, cat, "Peewee", true)
	var plays []string
	var lines []string
	q.OnPlay(func(alias string, slot Slot, unit pool.Handle) { plays = append(plays, alias) })
	q.OnSpeech(func(line string) { lines = append(lines, line) })
	q.Seed(42)
	// Fill 8 entries with distinct slots
	slots := []Slot{1, 2, 7, 12, 13, 14, 15, 6} // 8 distinct, priorities 10,9,8,7,7,7,7,3
	for _, s := range slots {
		if !q.InsertAt(5000, s, 1, "") {
			t.Fatalf("fill slot %d rejected", s)
		}
	}
	if q.Count != 8 {
		t.Fatalf("count %d want 8", q.Count)
	}
	// Last entry should be lowest priority (arrived pri3 or similar). Determine last
	lastSlot := q.Entries[7].Slot
	// Insert 9th distinct slot (working pri2 or build pri4 etc). Use working 11 pri2 - lowest, will be inserted at end, but eviction removes last before insert
	// To test eviction, use a new slot not in queue: e.g., 5 (ok pri5) or 3
	// Choose capture 16 pri4 — will insert in middle, but eviction still removes last (lowest priority)
	plays = nil
	lines = nil
	if !q.InsertAt(5000, 16, 1, "") {
		t.Fatal("evicting insert failed")
	}
	if q.Count != 8 {
		t.Fatalf("count after eviction %d want 8", q.Count)
	}
	if len(plays) != 0 {
		t.Fatalf("eviction played audibly %v", plays)
	}
	// Evicted last's speech should have flushed
	found := false
	for _, l := range lines {
		if strings.Contains(l, "Arrived") || strings.Contains(l, "Working") || l != "" {
			// last slot speech is from slotTable speech or caption
			// slot 6 arrived has speech Arrived, so expect that
			if l == "Peewee: Arrived" {
				found = true
			}
		}
	}
	// The last before eviction was slot 6 arrived (pri3) if we filled as above, but slot set includes 6 as last after sorting
	// Verify that at least one speech flushed
	if !found && len(lines) == 0 {
		t.Fatalf("evicted entry speech not flushed lines=%v lastSlot=%d", lines, lastSlot)
	}
	// Head should still be highest priority select
	if q.Entries[0].Slot != 1 {
		t.Fatalf("head %d want 1", q.Entries[0].Slot)
	}
}

func TestAudioOneVoicePer30(t *testing.T) {
	q := NewQueue()
	cat := categoryFixture()
	q.Register(1, cat, "Peewee", true)
	var plays []string
	var lines []string
	q.OnPlay(func(alias string, slot Slot, unit pool.Handle) { plays = append(plays, alias) })
	q.OnSpeech(func(line string) { lines = append(lines, line) })
	q.Seed(123)
	order := []Slot{1, 2, 7, 12}
	for _, s := range order {
		if !q.InsertAt(5000, s, 1, "") {
			t.Fatalf("insert %d", s)
		}
	}
	// Queue sorted descending: select10, underattack9, cant8, load7
	q.Drain(5000) // audible select
	q.Drain(5000) // same window silent underattack
	q.Drain(5010) // still inside silent cant
	q.Drain(5031) // 5031 >= 5030 audible load
	if len(plays) != 2 {
		t.Fatalf("plays %v want 2 audibles", plays)
	}
	if plays[0] != "sel_a" && plays[0] != "sel_b" {
		t.Fatalf("first play %q want sel", plays[0])
	}
	if plays[1] != "load" {
		t.Fatalf("second play %q want load", plays[1])
	}
	silentMap := map[string]bool{}
	for _, l := range lines {
		silentMap[l] = true
	}
	if !silentMap["Peewee: Under Attack"] {
		t.Fatalf("missing silent underattack speech %v", lines)
	}
	if !silentMap["Peewee: Cannot Comply"] {
		t.Fatalf("missing silent cant speech %v", lines)
	}
}

func TestQueueSignedGaugeGateAndBitSix(t *testing.T) {
	q := NewQueue()
	q.Register(1, categoryFixture(), "Unit", true)
	q.ConfigureThresholdGauges(10, 10)
	q.ConfigureBackendGates(1, 0x40, true) // audible bit set, master bits absent
	var plays int
	q.OnPlay(func(string, Slot, pool.Handle) { plays++ })
	if !q.InsertAt(100, 11, 1, "") {
		t.Fatal("insert working")
	}
	q.Drain(100)
	if plays != 0 {
		t.Fatal("bit 6 must gate audible dispatch")
	}
	q.ConfigureBackendGates(1, 0x47, true)
	if q.InsertAt(120, 11, 1, "") {
		t.Fatal("audible gate should re-arm cooldown despite blocked dispatch")
	}
	if !q.InsertAt(130, 11, 1, "") {
		t.Fatal("cooldown should expire at frame 130")
	}
	q.Drain(130)
	if plays != 1 {
		t.Fatal("signed threshold at gauge 10 should pass priority 2")
	}
}

func TestQueueDeadUnitDoesNotPrintSpeech(t *testing.T) {
	q := NewQueue()
	q.Register(1, categoryFixture(), "Unit", false)
	var lines []string
	q.OnSpeech(func(line string) { lines = append(lines, line) })
	if !q.InsertAt(100, 2, 1, "override") {
		t.Fatal("insert underattack")
	}
	q.Drain(100)
	if len(lines) != 0 {
		t.Fatalf("dead unit speech=%v", lines)
	}
}

func TestDrainOutsideSim(t *testing.T) {
	data, err := os.ReadFile("queue.go")
	if err != nil {
		t.Fatalf("read queue.go: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "rng.Sim") || strings.Contains(s, "Global.Sim") || strings.Contains(s, "Simulation") {
		t.Fatalf("queue.go must not use simulation RNG (C19), found Sim reference")
	}
	if strings.Contains(s, "math/rand") {
		t.Fatalf("queue.go must not import math/rand")
	}
	// Ensure CRT path exists
	if !strings.Contains(s, "214013") {
		t.Fatalf("queue.go should contain CRT LCG 214013 per I4/C19")
	}
}

func TestNoSimImport(t *testing.T) {
	data, err := os.ReadFile("queue.go")
	if err != nil {
		t.Skip("no queue.go")
	}
	if strings.Contains(string(data), "\"github.com/nanolathe/nanolathe/internal/sim/rng\"") {
		// If it imports rng, ensure it doesn't use Sim
		if strings.Contains(string(data), ".Sim") {
			t.Fatal("must not use rng.Sim")
		}
	}
}

func TestResolveVariantCRT(t *testing.T) {
	cat := categoryFixture()
	// Ensure variant draw uses CRT not Sim by checking determinism
	q1 := NewQueue()
	q2 := NewQueue()
	q1.Register(1, cat, "U", true)
	q2.Register(1, cat, "U", true)
	q1.Seed(777)
	q2.Seed(777)
	var p1, p2 []string
	q1.OnPlay(func(a string, s Slot, u pool.Handle) { p1 = append(p1, a) })
	q2.OnPlay(func(a string, s Slot, u pool.Handle) { p2 = append(p2, a) })
	for i := 0; i < 5; i++ {
		q1.InsertAt(uint32(i*100), 1, 1, "")
		q1.Drain(uint32(i*100 + 30))
	}
	for i := 0; i < 5; i++ {
		q2.InsertAt(uint32(i*100), 1, 1, "")
		q2.Drain(uint32(i*100 + 30))
	}
	if len(p1) != len(p2) {
		t.Fatalf("variant determinism failed %d vs %d: %v vs %v", len(p1), len(p2), p1, p2)
	}
	for i := range p1 {
		if p1[i] != p2[i] {
			t.Fatalf("variant %d diverged %q vs %q", i, p1[i], p2[i])
		}
	}
	// Ensure silent resolves also consume CRT
	q3 := NewQueue()
	q3.Register(1, cat, "U", true)
	q3.Seed(42)
	q3.Configure(0, 0, true, true) // threshold 0 blocks all (10-0=10 < priority false for max 10)
	// This will make audible gate fail, so drains silent but still draws
	for i := 0; i < 10; i++ {
		q3.InsertAt(uint32(i*10), 1, 1, "")
		q3.Drain(uint32(i*10 + 30))
	}
	// Draw count should be 10 even though no audible plays
	if q3.crtState == 42 {
		t.Fatal("CRT state not advanced on silent resolves")
	}
}

func TestDrainEmptyNoOp(t *testing.T) {
	q := NewQueue()
	q.Drain(100) // should not panic
	if q.Count != 0 || q.BaseTime != 0 {
		t.Fatalf("empty drain changed state")
	}
}

func TestInsertUsesDescendingPriority(t *testing.T) {
	q := NewQueue()
	// Insert low priority first
	if !q.InsertAt(0, 11, 1, "") {
		t.Fatal("working")
	}
	if !q.InsertAt(0, 7, 1, "") {
		t.Fatal("cant")
	}
	if q.Entries[0].Slot != 7 {
		t.Fatalf("high priority not first")
	}
}

func TestQueueCountAndBaseTime(t *testing.T) {
	q := &Queue{}
	if q.Count != 0 {
		t.Fatal("new queue count not 0")
	}
	q.BaseTime = 100
	if q.BaseTime != 100 {
		t.Fatal("BaseTime")
	}
}

func TestInsertTextOverride(t *testing.T) {
	q := NewQueue()
	cat := categoryFixture()
	q.Register(1, cat, "Hero", true)
	var lines []string
	q.OnSpeech(func(s string) { lines = append(lines, s) })
	q.Seed(1)
	if !q.InsertAt(0, 1, 1, "Custom Line") {
		t.Fatal("insert")
	}
	q.Drain(1000)
	if len(lines) != 1 || lines[0] != "Hero: Custom Line" {
		t.Fatalf("override speech %v", lines)
	}
}

func TestCrowdGate(t *testing.T) {
	q := NewQueue()
	cat := categoryFixture()
	q.Register(1, cat, "Peewee", true)
	q.Configure(3, 3, true, true) // 10-3=7 < priority passes if priority >7
	var plays []string
	q.OnPlay(func(a string, s Slot, u pool.Handle) { plays = append(plays, a) })
	// load pri7 should be blocked (7 not >7)
	if !q.InsertAt(2000, 12, 1, "") {
		t.Fatal("load insert")
	}
	q.Drain(2000)
	if len(plays) != 0 {
		t.Fatalf("crowd gate failed, plays %v", plays)
	}
	// cant pri8 should pass
	if !q.InsertAt(2035, 7, 1, "") {
		t.Fatal("cant insert")
	}
	q.Drain(2035)
	if len(plays) != 1 || plays[0] != "cant" {
		t.Fatalf("crowd cant %v", plays)
	}
}
