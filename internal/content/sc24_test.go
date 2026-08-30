package content

import (
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

// providerFixtureFS gives the same authored bytes an explicit provider
// identity. It lets SC24 lock the content policy at the compiler boundary
// without coupling simulation identity to archive-vs-loose provenance.
type providerFixtureFS struct {
	*fixtureFS
	provider vfs.Provenance
}

func (f *providerFixtureFS) ReadDir(name string) ([]vfs.EntryInfo, error) {
	entries, err := f.fixtureFS.ReadDir(name)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Source = f.provider
		entries[i].Source.LogicalPath = entries[i].Path
	}
	return entries, nil
}

func (f *providerFixtureFS) Stat(name string) (vfs.EntryInfo, error) {
	info, err := f.fixtureFS.Stat(name)
	if err != nil {
		return vfs.EntryInfo{}, err
	}
	info.Source = f.provider
	info.Source.LogicalPath = info.Path
	return info, nil
}

func TestSC24ArchiveAndLooseDefinitionsHaveIdenticalSimulationIdentity(t *testing.T) {
	files := []fixtureFile{{
		path: "units/sc24.fbi",
		data: `[UNITINFO]
{
 unitname=sc24;
 Germanname=Archivdefinition;
 name=LooseFallback;
 description=shared;
 bmcode=1;
 movementclass=Kbot;
 canmove=1;
}
`,
	}}
	archive := &providerFixtureFS{
		fixtureFS: newFixtureFS(t, files...),
		provider:  vfs.Provenance{ProviderType: "hpi", SourcePath: "rev31.gp3", MountOrder: 1},
	}
	loose := &providerFixtureFS{
		fixtureFS: newFixtureFS(t, files...),
		provider:  vfs.Provenance{ProviderType: "directory", SourcePath: "/content/units/sc24.fbi", MountRoot: "/content", MountOrder: 2},
	}

	archiveUnits, err := CompileUnitsWithLanguage(archive, "German")
	if err != nil {
		t.Fatalf("archive compile: %v", err)
	}
	looseUnits, err := CompileUnitsWithLanguage(loose, "German")
	if err != nil {
		t.Fatalf("loose compile: %v", err)
	}
	archiveDef := archiveUnits["sc24"]
	looseDef := looseUnits["sc24"]
	if archiveDef == nil || looseDef == nil {
		t.Fatalf("archive and loose definitions must both be accepted: archive=%v loose=%v", archiveDef != nil, looseDef != nil)
	}
	if archiveDef.Name != "Archivdefinition" || looseDef.Name != archiveDef.Name {
		t.Fatalf("language-prefixed name mismatch: archive=%q loose=%q", archiveDef.Name, looseDef.Name)
	}
	if archiveDef.Hash != looseDef.Hash {
		t.Fatalf("simulation identity depends on provider origin: archive=%s loose=%s", archiveDef.Hash, looseDef.Hash)
	}
	if archiveDef.Provenance.ProviderID == looseDef.Provenance.ProviderID {
		t.Fatalf("fixture providers did not remain distinguishable: %q", archiveDef.Provenance.ProviderID)
	}
}
