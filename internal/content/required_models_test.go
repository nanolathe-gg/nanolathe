package content

import (
	"errors"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// authoredRequiredModel3DO builds the small checked 3DO fixtures used here.
// They are authored in the test and encoded through our format writer; no
// retail bytes enter the catalog tests.
func authoredRequiredModel3DO(t *testing.T, vertexY int32) string {
	t.Helper()
	data, err := formats.EncodeThreeDO(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{{
		Version: 1, Name: "root", Selection: -1, Parent: -1, FirstChild: -1, NextSibling: -1,
		Vertices: []formats.ThreeDOVertex{{Y: vertexY}},
	}}})
	if err != nil {
		t.Fatalf("EncodeThreeDO: %v", err)
	}
	return string(data)
}

func TestRequiredModelsRejectEveryNamedFamily(t *testing.T) {
	tests := []struct {
		name     string
		units    map[string]*UnitDef
		weapons  map[string]*WeaponDef
		features map[string]*FeatureDef
	}{
		{name: "unit", units: map[string]*UnitDef{"late": {ObjectName: "late_unit"}}},
		{name: "weapon", weapons: map[string]*WeaponDef{"weapon": {Model: "late_weapon"}}},
		{name: "feature", features: map[string]*FeatureDef{"feature": {Object: "late_feature"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequiredModels(newFixtureFS(t), tt.units, tt.weapons, tt.features)
			want := "nanolathe: required authored resource: logical path objects3d/late_" + tt.name + ".3do, providers searched [], expected valid 3DO model"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("validateRequiredModels error = %v, want diagnostic containing %q", err, want)
			}
		})
	}
}

func TestRequiredModelsFirstFailureUsesSortedLogicalPath(t *testing.T) {
	err := validateRequiredModels(newFixtureFS(t),
		map[string]*UnitDef{"later-buildable": {ObjectName: "zebra"}},
		map[string]*WeaponDef{"weapon": {Model: "zebra"}},
		map[string]*FeatureDef{"feature": {Object: "alpha"}},
	)
	if err == nil || !strings.Contains(err.Error(), "logical path objects3d/alpha.3do") {
		t.Fatalf("first required-model failure = %v, want alpha path", err)
	}
}

func TestRequiredModelsRejectReadAndParseFailuresWithProvider(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		fs := unreadableModelFS{fixtureFS: newFixtureFS(t, fixtureFile{path: "objects3d/weapon.3do", data: authoredRequiredModel3DO(t, 0)})}
		err := validateRequiredModels(fs, nil, map[string]*WeaponDef{"weapon": {Model: "weapon"}}, nil)
		for _, want := range []string{"logical path objects3d/weapon.3do", "providers searched [unknown]", "expected valid 3DO model", "model read failed"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("read failure = %v, want diagnostic containing %q", err, want)
			}
		}
	})
	t.Run("parse", func(t *testing.T) {
		fs := newFixtureFS(t, fixtureFile{path: "objects3d/feature.3do", data: "not a 3DO"})
		err := validateRequiredModels(fs, nil, nil, map[string]*FeatureDef{"feature": {Object: "feature"}})
		for _, want := range []string{"logical path objects3d/feature.3do", "providers searched [unknown]", "expected valid 3DO model", "3do: root object is truncated"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("parse failure = %v, want diagnostic containing %q", err, want)
			}
		}
	})
}

type unreadableModelFS struct{ *fixtureFS }

func (f unreadableModelFS) ReadFileLimit(name string, max int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "objects3d/") {
		return nil, errors.New("fixture: model read failed")
	}
	return f.fixtureFS.ReadFileLimit(name, max)
}

func TestRequiredModelsShareGeometryAndKeepZeroAsValidTop(t *testing.T) {
	fs := &countingModelFS{fixtureFS: newFixtureFS(t, fixtureFile{path: "objects3d/shared.3do", data: authoredRequiredModel3DO(t, -1<<16)})}
	units := map[string]*UnitDef{
		"late": {ObjectName: "shared"},
	}
	weapons := map[string]*WeaponDef{
		"weapon": {Model: "shared"},
		"empty":  {Model: ""},
	}
	features := map[string]*FeatureDef{
		"feature": {Object: "shared", SeqName: "optional-sequence"},
		"empty":   {Object: "", SeqNameDie: "optional-death-sequence"},
	}
	if err := validateRequiredModels(fs, units, weapons, features); err != nil {
		t.Fatalf("validateRequiredModels: %v", err)
	}
	if got := units["late"].ModelTopFixed; got != 0 {
		t.Fatalf("below-origin valid model top = %d, want 0", got)
	}
	if got := units["late"].ModelTop; got != 0 {
		t.Fatalf("below-origin whole model top = %d, want 0", got)
	}
	if fs.modelReads != 1 {
		t.Fatalf("shared model was read %d times, want one cached parse", fs.modelReads)
	}
}

type countingModelFS struct {
	*fixtureFS
	modelReads int
}

func (f *countingModelFS) ReadFileLimit(name string, max int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "objects3d/") {
		f.modelReads++
	}
	return f.fixtureFS.ReadFileLimit(name, max)
}

// Compilation rejects only faces that participate in the ordering divide.
// Admission must enforce this before publishing any unit heights [02 "Model
// archive (3DO)"], even when the malformed model belongs to another family.
func TestRequiredModelsCheckCompilationBeforePublication(t *testing.T) {
	for _, tt := range []struct {
		name      string
		selection int32
		faces     int
		empty     int
		want      string
	}{
		{"invalid selection", 4, 3, -1, "selection primitive 4"},
		{"compared empty", -1, 3, 1, "zero vertex indexes"},
		{"source zero moved into comparison", 2, 3, 0, "zero vertex indexes"},
		{"selected empty excluded", 2, 3, 2, ""},
		{"two faces never compared", -1, 2, 1, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			obj := formats.ThreeDOObject{Version: 1, Name: "root", Selection: tt.selection, Parent: -1, FirstChild: -1, NextSibling: -1,
				Vertices: []formats.ThreeDOVertex{{Y: 1 << 16}}}
			for i := 0; i < tt.faces; i++ {
				face := formats.ThreeDOPrimitive{VertexIndices: []uint16{0}}
				if i == tt.empty {
					face.VertexIndices = nil
				}
				obj.Primitives = append(obj.Primitives, face)
			}
			data, err := formats.EncodeThreeDO(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{obj}})
			if err != nil {
				t.Fatal(err)
			}
			fs := newFixtureFS(t,
				fixtureFile{path: "objects3d/a_unit.3do", data: authoredRequiredModel3DO(t, 5<<16)},
				fixtureFile{path: "objects3d/z_weapon.3do", data: string(data)})
			unit := &UnitDef{ObjectName: "a_unit", ModelTop: 9, ModelTopFixed: 9 << 16}
			err = validateRequiredModels(fs, map[string]*UnitDef{"unit": unit}, map[string]*WeaponDef{"weapon": {Model: "z_weapon"}}, nil)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if unit.ModelTop != 5 {
					t.Fatalf("valid admission did not publish height: %d", unit.ModelTop)
				}
				return
			}
			for _, want := range []string{tt.want, "logical path objects3d/z_weapon.3do", "providers searched [unknown]", "expected valid 3DO model"} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want %q", err, want)
				}
			}
			if unit.ModelTop != 9 || unit.ModelTopFixed != 9<<16 {
				t.Fatal("failed admission published partial heights")
			}
		})
	}
}

func TestRequiredUnitModelKeepsEmptyBasename(t *testing.T) {
	u := &UnitDef{}
	if err := validateRequiredModels(newFixtureFS(t), map[string]*UnitDef{"": u}, nil, nil); err == nil || !strings.Contains(err.Error(), "objects3d/.3do") {
		t.Fatalf("empty unit model diagnostic = %v", err)
	}
	fs := newFixtureFS(t, fixtureFile{path: "objects3d/.3do", data: authoredRequiredModel3DO(t, 3<<16)})
	if err := validateRequiredModels(fs, map[string]*UnitDef{"": u}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if u.ModelTopFixed != 3<<16 {
		t.Fatalf("empty basename lost model height: %d", u.ModelTopFixed)
	}
	_, index := buildModelRecordCatalog([]*UnitDef{u})
	cat := &Catalog{Units: map[string]*UnitDef{"": u}, modelIndex: index}
	if name, _, ok := cat.ModelForUnit(""); !ok || name != "" {
		t.Fatal("empty basename lost catalog model binding")
	}
}
