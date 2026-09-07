package content

import "testing"

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
