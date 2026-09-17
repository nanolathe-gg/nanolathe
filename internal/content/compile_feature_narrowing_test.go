package content

import (
	"strconv"
	"testing"
)

func TestFeatureSparkTimeNarrowingUsesSignedWord(t *testing.T) {
	doc := mustParseTDF(t, `[edge]
{
sparktime=2184.6;
}
`)
	feature := compileFeatureSection(doc.Root.Sections()[0], "edge", Provenance{})
	if feature.SparkTime != 2 {
		t.Fatalf("sparktime = %d, want 2 after signed-64 low word then int16 store", feature.SparkTime)
	}
}

// TestFeatureReclaimYieldMasksToSixteenBits locks the mask retail applies to
// the integer conversion before the single-precision yield store: the stored
// value is float(uint16(value)), so a six-figure authored metal wraps and a
// negative becomes a large positive [05 "Feature catalog and placement"].
// The four authored values below are the stock sections that depend on it.
func TestFeatureReclaimYieldMasksToSixteenBits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		authored int32
		want     int32
	}{
		{"armgate_dead", 171172, 40100},
		{"armgate_heap", 85586, 20050},
		{"corgate_dead", 157988, 26916},
		{"corgate_heap", 78994, 13458},
		{"negative", -1, 65535},
		{"in range", 4000, 4000},
		{"exact wrap", 65536, 0},
	} {
		doc := mustParseTDF(t, "[w]\n{\nmetal="+itoa(tc.authored)+";\nenergy="+itoa(tc.authored)+";\n}\n")
		feature := compileFeatureSection(doc.Root.Sections()[0], "w", Provenance{})
		if feature.Metal != tc.want || feature.Energy != tc.want {
			t.Fatalf("%s: authored %d compiled to metal=%d energy=%d, want %d", tc.name, tc.authored, feature.Metal, feature.Energy, tc.want)
		}
	}
}

func itoa(v int32) string {
	return strconv.FormatInt(int64(v), 10)
}

// TestFeatureRecordStoreWidths locks the record's narrow stores and each
// field's reader extension [02 R-KEYS-01 §5][05 R-FEAT-01 §1]. `height` is the
// simulation-visible one: it bounds the reclaim/resurrect approach-point draw,
// and 27 stock decorations author values above 255 (the four below are stock
// authored values with their retail bytes).
func TestFeatureRecordStoreWidths(t *testing.T) {
	doc := mustParseTDF(t, `[w]
{
footprintx=70000;
footprintz=-1;
height=490;
damage=-1;
spreadchance=300;
reproduce=-1;
reproducearea=256;
}
`)
	feature := compileFeatureSection(doc.Root.Sections()[0], "w", Provenance{})
	for _, tc := range []struct {
		key  string
		got  int32
		want int32
	}{
		{"footprintx", feature.FootprintX, 4464},    // signed 16-bit: 70000 & 0xFFFF = 4464
		{"footprintz", feature.FootprintZ, -1},      // signed 16-bit: stays negative
		{"height", feature.Height, 234},             // unsigned byte: 490 & 0xFF
		{"damage", feature.Damage, 65535},           // unsigned 16-bit
		{"spreadchance", feature.SpreadChance, 44},  // unsigned byte: 300 & 0xFF
		{"reproduce", feature.Reproduce, 255},       // unsigned byte
		{"reproducearea", feature.ReproduceArea, 0}, // unsigned byte: 256 wraps to 0
	} {
		if tc.got != tc.want {
			t.Fatalf("%s = %d, want %d", tc.key, tc.got, tc.want)
		}
	}
}

// TestFeatureHeightStockBytes pins the stock authored heights R20 censused
// against the bytes retail stores, because the draw bound — not just the field
// — changes with them [05 "Resurrection"][05 R-WORK-01 §7].
func TestFeatureHeightStockBytes(t *testing.T) {
	for _, tc := range []struct{ authored, want int32 }{
		{490, 234}, {460, 204}, {420, 164}, {385, 129}, {260, 4}, {250, 250},
	} {
		doc := mustParseTDF(t, "[w]\n{\nheight="+itoa(tc.authored)+";\n}\n")
		feature := compileFeatureSection(doc.Root.Sections()[0], "w", Provenance{})
		if feature.Height != tc.want {
			t.Fatalf("height %d compiled to %d, want the stored byte %d", tc.authored, feature.Height, tc.want)
		}
	}
}
