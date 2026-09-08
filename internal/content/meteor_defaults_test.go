package content

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestCompileMeteorDefersPresentDefaultValidation(t *testing.T) {
	valid := func(body string) *MeteorDefaults {
		t.Helper()
		md, err := CompileMeteor(newFixtureFS(t, fixtureFile{path: "gamedata/meteor.tdf", data: body}))
		if err != nil {
			t.Fatalf("CompileMeteor: %v", err)
		}
		return md
	}

	missing := valid(`[Other] { MeteorWeapon=meteor; }`)
	if missing.DefaultPresent || missing.DefaultValid {
		t.Fatalf("missing Default = %+v, want absent semantic default", missing)
	}
	if missing.MeteorWeapon != "" {
		t.Fatalf("root assignments became default data: %+v", missing)
	}

	absentWeapon := valid(`[Default] { MeteorRadius=300; MeteorDensity=2; MeteorDuration=5; MeteorInterval=60; }`)
	if !absentWeapon.DefaultPresent || absentWeapon.DefaultValid {
		t.Fatalf("missing default weapon presence/validity = %+v, want present invalid", absentWeapon)
	}

	emptyWeapon := valid(`[Default] { MeteorWeapon=; MeteorRadius=300; MeteorDensity=2; MeteorDuration=5; MeteorInterval=60; }`)
	if !emptyWeapon.DefaultPresent || !emptyWeapon.DefaultValid || emptyWeapon.MeteorWeapon != "" {
		t.Fatalf("empty present weapon should be valid: %+v", emptyWeapon)
	}
	if missing.Hash == emptyWeapon.Hash {
		t.Fatalf("meteor hash lost Default presence: absent=%q present=%q", missing.Hash, emptyWeapon.Hash)
	}

	zero := valid(`[Default] { MeteorWeapon=meteor; MeteorRadius=0; MeteorDensity=2; MeteorDuration=5; MeteorInterval=60; }`)
	if !zero.DefaultPresent || zero.DefaultValid {
		t.Fatalf("zero default numeric should be deferred-invalid: %+v", zero)
	}

	lowA := valid(`[Default] { MeteorWeapon=meteor; MeteorRadius=1; MeteorDensity=1e-20; MeteorDuration=1; MeteorInterval=1; }`)
	lowB := valid(`[Default] { MeteorWeapon=meteor; MeteorRadius=1; MeteorDensity=1.0000001e-20; MeteorDuration=1; MeteorInterval=1; }`)
	if math.Float32bits(lowA.MeteorDensity) == math.Float32bits(lowB.MeteorDensity) {
		t.Fatalf("fixture must retain distinct source float32 values")
	}
	if lowA.Hash == lowB.Hash {
		t.Fatalf("meteor hash collapsed distinct narrow float32 values: %q", lowA.Hash)
	}
}

type meteorReadFailureFS struct{ *fixtureFS }

func (f meteorReadFailureFS) ReadFileLimit(string, int64) ([]byte, error) {
	return nil, errors.New("provider read failed")
}

func TestCompileMeteorRejectsProviderFailure(t *testing.T) {
	_, err := CompileMeteor(meteorReadFailureFS{newFixtureFS(t)})
	if err == nil || !strings.Contains(err.Error(), "nanolathe: required authored resource: logical path gamedata/meteor.tdf, providers searched [], expected retail METEOR.TDF default table: provider read failed") {
		t.Fatalf("CompileMeteor provider failure = %v", err)
	}
}
