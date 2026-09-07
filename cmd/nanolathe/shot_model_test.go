package main

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

func TestResolveModelPreviewDefinitionUsesUnitDisplayProperties(t *testing.T) {
	catalog := &content.Catalog{Units: map[string]*content.UnitDef{
		"fixture": {UnitName: "fixture", ObjectName: "fixture3do", BMCode: 0, ZBuffer: true},
	}}
	got, err := resolveModelPreviewDefinition(catalog, "FIXTURE.3DO")
	if err != nil {
		t.Fatalf("resolve unit key: %v", err)
	}
	if got.ObjectName != "fixture3do" || !got.Structure || !got.ZBuffer {
		t.Fatalf("resolved display properties = %+v", got)
	}
}

func TestResolveModelPreviewDefinitionRejectsUnclassifiedObject(t *testing.T) {
	catalog := &content.Catalog{Units: map[string]*content.UnitDef{
		"fixture": {UnitName: "fixture", ObjectName: "fixture3do", BMCode: 1},
	}}
	_, err := resolveModelPreviewDefinition(catalog, "unlisted.3do")
	if err == nil || !strings.Contains(err.Error(), "expected a unit key") {
		t.Fatalf("unclassified object error = %v", err)
	}
}

func TestResolveModelPreviewDefinitionRejectsConflictingObjectProperties(t *testing.T) {
	catalog := &content.Catalog{Units: map[string]*content.UnitDef{
		"mobile":    {UnitName: "mobile", ObjectName: "shared", BMCode: 1},
		"structure": {UnitName: "structure", ObjectName: "shared", BMCode: 0},
	}}
	_, err := resolveModelPreviewDefinition(catalog, "shared")
	if err == nil || !strings.Contains(err.Error(), "conflicting unit display properties") {
		t.Fatalf("conflicting object error = %v", err)
	}
}
