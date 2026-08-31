package content

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

// topologyFixtureFS gives every authored file an explicit provider identity.
// This keeps the production archive-only gate testable without broadening the
// ordinary fixture VFS into a second overlay implementation.
type topologyFixtureFS struct {
	*fixtureFS
	providers map[string]vfs.Provenance
}

func (f *topologyFixtureFS) ReadDir(name string) ([]vfs.EntryInfo, error) {
	entries, err := f.fixtureFS.ReadDir(name)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Source = f.providers[strings.ToLower(entries[i].Path)]
		entries[i].Source.LogicalPath = entries[i].Path
	}
	return entries, nil
}

// shadowProviderFS models the important overlay edge: ReadDir exposes the
// loose winner, while the optional mount surface can still open the archived
// copy of that same logical path. Mount 0 is loose and mount 1 is archive,
// matching the real overlay's precedence order.
type shadowProviderFS struct {
	*fixtureFS
	archive map[string]string
}

func (f *shadowProviderFS) ReadDir(name string) ([]vfs.EntryInfo, error) {
	entries, err := f.fixtureFS.ReadDir(name)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Source = vfs.Provenance{
			LogicalPath:  entries[i].Path,
			ProviderType: "directory",
			SourcePath:   entries[i].Path,
		}
	}
	return entries, nil
}

func (f *shadowProviderFS) MountCount() int { return 2 }

func (f *shadowProviderFS) OpenMount(index int, name string) (vfs.File, error) {
	path := strings.ToLower(name)
	if index == 0 {
		data, ok := f.files[path]
		if !ok {
			return nil, vfs.ErrNotFound
		}
		return newBytesFile([]byte(data), vfs.EntryInfo{
			Path: path, Size: int64(len(data)),
			Source: vfs.Provenance{LogicalPath: path, ProviderType: "directory", SourcePath: path},
		}), nil
	}
	if index == 1 {
		data, ok := f.archive[path]
		if !ok {
			return nil, vfs.ErrNotFound
		}
		return newBytesFile([]byte(data), vfs.EntryInfo{
			Path: path, Size: int64(len(data)),
			Source: vfs.Provenance{LogicalPath: path, ProviderType: "hpi", SourcePath: "totala1.hpi"},
		}), nil
	}
	return nil, vfs.ErrNotFound
}

func TestSC24ArchiveDefinitionsWinAndLooseDefinitionsAreUnobservable(t *testing.T) {
	archiveUnit := `[UNITINFO]
{
 unitname=SC24;
 name=ArchiveUnit;
}
`
	looseUnit := `[UNITINFO]
{
 unitname=SC24;
 name=LooseUnit;
}
`
	archiveWeapon := `[SC24WEAPON]
{
 ID=24;
 name=ArchiveWeapon;
 range=240;
}
`
	looseWeapon := `[SC24WEAPON]
{
 ID=24;
 name=LooseWeapon;
 range=999;
}
`
	fs := &topologyFixtureFS{
		fixtureFS: newFixtureFS(t,
			fixtureFile{path: "units/archive.fbi", data: archiveUnit},
			fixtureFile{path: "units/loose.fbi", data: looseUnit},
			fixtureFile{path: "weapons/archive.tdf", data: archiveWeapon},
			fixtureFile{path: "weapons/loose.tdf", data: looseWeapon},
		),
		providers: map[string]vfs.Provenance{
			"units/archive.fbi":   {ProviderType: "hpi", SourcePath: "totala1.hpi"},
			"units/loose.fbi":     {ProviderType: "directory", SourcePath: "units/loose.fbi"},
			"weapons/archive.tdf": {ProviderType: "hpi", SourcePath: "totala1.hpi"},
			"weapons/loose.tdf":   {ProviderType: "directory", SourcePath: "weapons/loose.tdf"},
		},
	}

	units, err := CompileUnits(fs)
	if err != nil {
		t.Fatalf("compile units: %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("compiled units = %d, want only the archived definition", len(units))
	}
	if got := units["sc24"]; got == nil || got.Name != "ArchiveUnit" {
		t.Fatalf("SC24 unit = %#v, want archived definition", got)
	}

	weapons, duplicates, err := CompileWeaponsWithDuplicates(fs)
	if err != nil {
		t.Fatalf("compile weapons: %v", err)
	}
	if len(weapons) != 1 {
		t.Fatalf("compiled weapons = %d, want only the archived definition", len(weapons))
	}
	if got := weapons["sc24weapon"]; got == nil || got.Name != "ArchiveWeapon" || got.Range != 240 {
		t.Fatalf("SC24 weapon = %#v, want archived definition", got)
	}
	if len(duplicates) != 0 {
		t.Fatalf("loose weapon created duplicate diagnostics: %v", duplicates)
	}
}

func TestSC24ShadowedLooseWinnerRecoversArchivedUnitAndWeapon(t *testing.T) {
	looseUnit := `[UNITINFO]
{
 unitname=SHADOWED;
 name=LooseUnit;
}
`
	archiveUnit := `[UNITINFO]
{
 unitname=SHADOWED;
 name=ArchiveUnit;
}
`
	looseWeapon := `[SHADOWEDWEAPON]
{
 ID=25;
 name=LooseWeapon;
 range=999;
}
`
	archiveWeapon := `[SHADOWEDWEAPON]
{
 ID=25;
 name=ArchiveWeapon;
 range=250;
}
`
	pathUnit := "units/shadowed.fbi"
	pathWeapon := "weapons/shadowed.tdf"
	fs := &shadowProviderFS{
		fixtureFS: newFixtureFS(t,
			fixtureFile{path: pathUnit, data: looseUnit},
			fixtureFile{path: pathWeapon, data: looseWeapon},
		),
		archive: map[string]string{pathUnit: archiveUnit, pathWeapon: archiveWeapon},
	}

	units, err := CompileUnits(fs)
	if err != nil {
		t.Fatalf("compile shadowed unit: %v", err)
	}
	if got := units["shadowed"]; got == nil || got.Name != "ArchiveUnit" || got.Provenance.ProviderID != "totala1.hpi" {
		t.Fatalf("shadowed unit = %#v, want archived bytes and provenance", got)
	}

	weapons, duplicates, err := CompileWeaponsWithDuplicates(fs)
	if err != nil {
		t.Fatalf("compile shadowed weapon: %v", err)
	}
	if got := weapons["shadowedweapon"]; got == nil || got.Name != "ArchiveWeapon" || got.Range != 250 || got.Provenance.ProviderID != "totala1.hpi" {
		t.Fatalf("shadowed weapon = %#v, want archived bytes and provenance", got)
	}
	if len(duplicates) != 0 {
		t.Fatalf("shadowed loose weapon created duplicate diagnostics: %v", duplicates)
	}
}
