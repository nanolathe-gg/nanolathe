package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestHumanFactoryCommandRetainsPermanentQueueRejection(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, Movement: map[string]*content.MovementClass{}}
	factoryDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armlab"},
		UnitName:         "armlab",
		Builder:          true,
		Script:           fixtureCOBProgram(),
	}
	broken := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "broken"},
		UnitName:         "broken",
		BMCode:           1,
		FootprintX:       1,
		FootprintZ:       1,
	}
	cat.Units[factoryDef.CanonicalKey] = factoryDef
	cat.Units[broken.CanonicalKey] = broken
	w := units.NewSliced(4, cat)
	h, err := w.Create(factoryDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	s.applyHumanCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{
		Builder: h, Product: broken.UnitName, Count: 1,
	}}, 7)
	if s.Build == nil {
		t.Fatal("factory command did not bind construction service")
	}
	trace := s.Build.CommandDiagnostics()
	if len(trace) != 1 {
		t.Fatalf("command trace=%+v, want one permanent rejection", trace)
	}
	if trace[0].Tick != 7 || trace[0].Builder != h || trace[0].Product != "broken" || trace[0].Reason == "" {
		t.Fatalf("command trace=%+v, missing identity/reason", trace)
	}
	if q := orders.QueueForUnit(w.Unit(h)); q != nil && q.LenPrimary() != 0 {
		t.Fatalf("rejected product entered queue: %+v", q.Primary())
	}
}
