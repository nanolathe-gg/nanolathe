package content

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// SkirmishAsset is one file or named resource required by a skirmish bundle.
// Entry is used for named GAF resources; an empty Entry identifies the whole
// file. Provider is retained so a diagnostic can identify the winning VFS
// provider without exposing a host path [PLAN 01 C13].
type SkirmishAsset struct {
	Kind     string
	Logical  string
	Entry    string
	Provider string
}

// SkirmishDiagnostic is an actionable preflight result. Missing or malformed
// required content is fatal; optional/absent authored references are reported
// only when they are internally inconsistent.
type SkirmishDiagnostic struct {
	Code    string
	Fatal   bool
	Kind    string
	Logical string
	Entry   string
	Message string
}

// SkirmishManifest is the stable result of PreflightSkirmish. Assets and
// Diagnostics are sorted by canonical identity; the chain fields retain the
// authored build-menu order that selected each unit.
type SkirmishManifest struct {
	MapKey      string
	Side        int
	SideKey     string
	AIProfile   string
	Commander   string
	Solar       string
	Mex         string
	KbotLab     string
	LabProduct  string
	Assets      []SkirmishAsset
	Diagnostics []SkirmishDiagnostic
	Hash        string
}

// Fatal reports whether the manifest contains a required-content failure.
func (m *SkirmishManifest) Fatal() bool {
	if m == nil {
		return true
	}
	for _, d := range m.Diagnostics {
		if d.Fatal {
			return true
		}
	}
	return false
}

// Error returns one deterministic summary for callers that want a fail-fast
// gate while retaining the full manifest diagnostics.
func (m *SkirmishManifest) Error() error {
	if m == nil || !m.Fatal() {
		return nil
	}
	parts := make([]string, 0)
	for _, d := range m.Diagnostics {
		if d.Fatal {
			parts = append(parts, d.Message)
		}
	}
	return fmt.Errorf("nanolathe: skirmish preflight: %s", strings.Join(parts, "; "))
}

// PreflightSkirmish resolves and validates the morning skirmish content
// bundle for mapName and side ordinal. The catalog is expected to have been
// compiled from the same VFS; all file bytes are read through fs and no retail
// bytes are copied into the manifest.
//
// A non-nil manifest is returned for content failures so callers can surface
// every missing resource at once. Invalid API arguments return an error before
// a manifest can be constructed.
func PreflightSkirmish(fs vfs.FSOps, catalog *Catalog, mapName string, side int) (*SkirmishManifest, error) {
	if fs == nil {
		return nil, fmt.Errorf("nanolathe: skirmish preflight: nil VFS")
	}
	if catalog == nil {
		return nil, fmt.Errorf("nanolathe: skirmish preflight: nil catalog")
	}
	if side < 0 || side >= len(catalog.Sides) {
		return nil, fmt.Errorf("nanolathe: skirmish preflight: side %d outside compiled side range 0..%d", side, len(catalog.Sides)-1)
	}
	mapKey := CanonicalKey(mapName)
	mh, ok := catalog.Maps[mapKey]
	if !ok || mh == nil {
		return nil, fmt.Errorf("nanolathe: skirmish preflight: map %q is not in the compiled catalog", mapName)
	}

	p := &skirmishPreflight{fs: fs, catalog: catalog, manifest: &SkirmishManifest{
		MapKey:  mapKey,
		Side:    side,
		SideKey: fmt.Sprintf("SIDE%d", side),
	}}
	p.file("map.ota", mh.LogicalOTA, true)
	p.file("map.tnt", mh.LogicalTNT, true)
	p.paletteBundle()
	p.file("cursor.gaf", "anims/cursors.gaf", true)
	p.file("gui.common", "anims/commongui.gaf", true)

	sd := catalog.Sides[side]
	p.file("side.gui", p.sideGUIPath(sd), true)
	p.file("side.font", p.namedFile("font", sd.Font, "fonts", "guis"), true)
	p.file("side.fontgui", p.namedFile("fontgui", sd.FontGUI, "guis", "fonts"), true)
	intGAF := p.gafFile("side.intgaf", sd.IntGAF, true)
	if intGAF != "" {
		for _, entry := range []string{"PANELTOP", "PANELSIDE", "PANELBOT"} {
			p.gafEntry("side.panel", intGAF, entry, true)
		}
	}

	commander := p.requireUnit("commander", sd.Commander)
	if commander == nil {
		return p.finish(), p.manifest.Error()
	}
	p.manifest.Commander = commander.CanonicalKey
	p.unit("commander", commander, true)
	commanderMenu := p.requireMenu("commander.buildmenu", commander.CanonicalKey)
	if commanderMenu == nil {
		return p.finish(), p.manifest.Error()
	}
	// These three definitions are selected from the authored commander page;
	// no canonical unit name is assumed [02 "Build-menu catalog keys"].
	solar := p.selectSolar(commanderMenu)
	mex := p.selectMex(commanderMenu)
	lab := p.selectFactory(commanderMenu)
	if solar == nil {
		p.fatal("build-chain", "", "solar", "commander page has no solar candidate")
	} else {
		p.manifest.Solar = solar.CanonicalKey
		p.unit("solar", solar, true)
	}
	if mex == nil {
		p.fatal("build-chain", "", "mex", "commander page has no metal-extractor candidate")
	} else {
		p.manifest.Mex = mex.CanonicalKey
		p.unit("mex", mex, true)
	}
	if lab == nil {
		p.fatal("build-chain", "", "kbot lab", "commander page has no builder page producing a kbot")
		return p.finish(), p.manifest.Error()
	}
	p.manifest.KbotLab = lab.CanonicalKey
	p.unit("kbot-lab", lab, true)
	labMenu := p.requireMenu("kbot-lab.buildmenu", lab.CanonicalKey)
	if labMenu == nil {
		return p.finish(), p.manifest.Error()
	}
	product := p.selectLabProduct(labMenu)
	if product == nil {
		p.fatal("build-chain", "", "lab product", "kbot lab page has no mobile kbot product")
	} else {
		p.manifest.LabProduct = product.CanonicalKey
		p.unit("lab-product", product, true)
	}

	p.mapAI(mh)
	result := p.finish()
	return result, result.Error()
}

type skirmishPreflight struct {
	fs       vfs.FSOps
	catalog  *Catalog
	manifest *SkirmishManifest
	assets   map[string]SkirmishAsset
	diags    map[string]SkirmishDiagnostic
	models   map[string]*model.Model
	cobs     map[string]*cob.Program
	gafs     map[string]*formats.GAF
}

func (p *skirmishPreflight) fatal(kind, logical, entry, message string) {
	p.diag(SkirmishDiagnostic{Code: "missing-required", Fatal: true, Kind: kind, Logical: logical, Entry: entry, Message: fmt.Sprintf("%s: logical path %s, expected %s", message, logical, entry)})
}

func (p *skirmishPreflight) diag(d SkirmishDiagnostic) {
	key := strings.Join([]string{d.Code, d.Kind, d.Logical, d.Entry, d.Message}, "\x00")
	if p.diags == nil {
		p.diags = make(map[string]SkirmishDiagnostic)
	}
	p.diags[key] = d
}

func (p *skirmishPreflight) file(kind, logical string, required bool) bool {
	logical = asciiFoldContent(logical)
	if logical == "" {
		if required {
			p.fatal(kind, logical, logical, "required file reference is empty")
		}
		return false
	}
	info, err := p.fs.Stat(logical)
	if err != nil || info.IsDir {
		if required {
			p.fatal(kind, logical, logical, "required file is unavailable")
		}
		return false
	}
	if p.assets == nil {
		p.assets = make(map[string]SkirmishAsset)
	}
	asset := SkirmishAsset{Kind: kind, Logical: logical, Provider: info.Source.ProviderID()}
	p.assets[strings.Join([]string{kind, logical}, "\x00")] = asset
	return true
}

// paletteBundle shares the live loader's recovery policy, so a recoverable
// PCX installation is not rejected by an existence-only PAL preflight [02 R-MALF-01 §9].
func (p *skirmishPreflight) paletteBundle() {
	if _, err := palette.Load(p.fs); err != nil {
		p.fatal("palette", "palettes/palette.pal", "", err.Error())
		return
	}
	recovered := false
	for _, stem := range []string{"palette", "guipal"} {
		name := "palettes/" + stem + ".pal"
		info, err := p.fs.Stat(name)
		if err != nil || info.Size == 0 {
			name = "palettes/" + stem + ".pcx"
			recovered = true
		}
		p.file("palette", name, true)
	}
	if !recovered {
		for _, ext := range []string{"alp", "lht", "shd"} {
			name := "palettes/palette." + ext
			if info, err := p.fs.Stat(name); err == nil && info.Size != 0 {
				p.file("palette", name, true)
			}
		}
	}
}

func (p *skirmishPreflight) namedFile(kind, name string, dirs ...string) string {
	name = trimTDFSemantic(name)
	if name == "" {
		return ""
	}
	base := name
	if strings.Contains(base, "/") {
		return base
	}
	for _, dir := range dirs {
		path := asciiFoldContent(strings.TrimSuffix(dir, "/") + "/" + base)
		if !strings.Contains(path[strings.LastIndex(path, "/"):], ".") {
			path += ".fnt"
		}
		if _, err := p.fs.Stat(path); err == nil {
			return path
		}
	}
	// Return the first data-derived candidate so the resulting diagnostic says
	// exactly which lookup was attempted; the file() call emits the failure.
	path := asciiFoldContent(strings.TrimSuffix(dirs[0], "/") + "/" + base)
	if !strings.Contains(path[strings.LastIndex(path, "/"):], ".") {
		path += ".fnt"
	}
	return path
}

func (p *skirmishPreflight) sideGUIPath(sd *SideDef) string {
	if sd == nil {
		return ""
	}
	prefix := CanonicalKey(sd.NamePrefix)
	if prefix == "" {
		prefix = CanonicalKey(sd.Commander)
		if len(prefix) > 3 {
			prefix = prefix[:3]
		}
	}
	return "guis/" + prefix + "main.gui"
}

func (p *skirmishPreflight) gafFile(kind, value string, required bool) string {
	value = trimTDFSemantic(value)
	if value == "" {
		if required {
			p.fatal(kind, "", value, "required GAF reference is empty")
		}
		return ""
	}
	path := value
	if !strings.Contains(path, "/") {
		path = "anims/" + path
	}
	if !strings.HasSuffix(asciiFoldContent(path), ".gaf") {
		path += ".gaf"
	}
	path = asciiFoldContent(path)
	if !p.file(kind, path, required) {
		return ""
	}
	if p.gafs == nil {
		p.gafs = make(map[string]*formats.GAF)
	}
	if _, ok := p.gafs[path]; !ok {
		gaf, err := formats.LoadGAFFile(p.fs, path)
		if err != nil {
			p.diag(SkirmishDiagnostic{Code: "malformed-gaf", Fatal: required, Kind: kind, Logical: path, Message: fmt.Sprintf("cannot parse GAF: %v", err)})
			return ""
		}
		p.gafs[path] = gaf
	}
	return path
}

func (p *skirmishPreflight) gafEntry(kind, gafPath, entry string, required bool) bool {
	if gafPath == "" {
		return false
	}
	gaf := p.gafs[gafPath]
	if gaf == nil {
		return false
	}
	if _, ok := gaf.Find(entry); !ok {
		p.diag(SkirmishDiagnostic{Code: "missing-gaf-entry", Fatal: required, Kind: kind, Logical: gafPath, Entry: entry, Message: fmt.Sprintf("GAF entry is unavailable: logical path %s, expected %s", gafPath, entry)})
		return false
	}
	if asset, ok := p.assets[strings.Join([]string{kind, gafPath}, "\x00")]; ok {
		asset.Entry = entry
		p.assets[strings.Join([]string{kind, gafPath, entry}, "\x00")] = asset
	}
	return true
}

func (p *skirmishPreflight) requireUnit(kind, key string) *UnitDef {
	key = CanonicalKey(key)
	u, ok := p.catalog.Units[key]
	if !ok || u == nil {
		p.diag(SkirmishDiagnostic{Code: "missing-definition", Fatal: true, Kind: kind, Entry: key, Message: fmt.Sprintf("unit definition is unavailable: catalog key %s", key)})
		return nil
	}
	return u
}

func (p *skirmishPreflight) requireMenu(kind, key string) *BuildMenuPage {
	menu, ok := p.catalog.BuildMenus[CanonicalKey(key)]
	if !ok || menu == nil || len(menu.Buttons) == 0 {
		p.diag(SkirmishDiagnostic{Code: "missing-build-menu", Fatal: true, Kind: kind, Entry: CanonicalKey(key), Message: fmt.Sprintf("build-menu page is unavailable: catalog key %s", CanonicalKey(key))})
		return nil
	}
	return menu
}

func (p *skirmishPreflight) selectSolar(menu *BuildMenuPage) *UnitDef {
	for _, key := range menu.Buttons {
		u := p.catalog.Units[CanonicalKey(key)]
		if u == nil {
			continue
		}
		// Solar is the first authored ENERGY-category producer that is neither
		// wind, tidal, radar, nor storage; this keeps selection data-driven
		// across ARM/CORE pages [fmt fbi].
		category := CanonicalKey(u.Category)
		if strings.Contains(category, "energy") && !strings.Contains(category, "storage") && u.WindGenerator == 0 && u.TidalGenerator == 0 {
			return u
		}
	}
	return nil
}

func (p *skirmishPreflight) selectMex(menu *BuildMenuPage) *UnitDef {
	for _, key := range menu.Buttons {
		u := p.catalog.Units[CanonicalKey(key)]
		if u != nil && (u.ExtractsMetal > 0 || u.MakesMetal != 0) {
			return u
		}
	}
	return nil
}

func (p *skirmishPreflight) selectFactory(menu *BuildMenuPage) *UnitDef {
	for _, key := range menu.Buttons {
		u := p.catalog.Units[CanonicalKey(key)]
		if u == nil || !u.Builder {
			continue
		}
		if child := p.catalog.BuildMenus[u.CanonicalKey]; child != nil {
			for _, product := range child.Buttons {
				candidate := p.catalog.Units[CanonicalKey(product)]
				if candidate != nil && candidate.CanMove && strings.Contains(CanonicalKey(candidate.Category), "kbot") {
					return u
				}
			}
		}
	}
	return nil
}

func (p *skirmishPreflight) selectLabProduct(menu *BuildMenuPage) *UnitDef {
	for _, key := range menu.Buttons {
		u := p.catalog.Units[CanonicalKey(key)]
		if u != nil && u.CanMove && !u.Builder && strings.Contains(CanonicalKey(u.Category), "kbot") {
			return u
		}
	}
	return nil
}

func (p *skirmishPreflight) unit(kind string, u *UnitDef, required bool) {
	if u == nil {
		return
	}
	p.modelAsset(kind+".3do", requiredUnitModelPath(u.ObjectName), required)
	if u.MovementClass != "" {
		if _, ok := p.catalog.Movement[CanonicalKey(u.MovementClass)]; !ok {
			p.diag(SkirmishDiagnostic{Code: "missing-definition", Fatal: required, Kind: kind + ".movement", Entry: u.MovementClass, Message: fmt.Sprintf("movement class is unavailable: catalog key %s", CanonicalKey(u.MovementClass))})
		}
	}
	p.script(kind, u, required)
	weaponRefs := []struct {
		name string
		ref  string
		def  *WeaponDef
	}{
		{"weapon1", u.Weapon1, u.Weapon1Def},
		{"weapon2", u.Weapon2, u.Weapon2Def},
		{"weapon3", u.Weapon3, u.Weapon3Def},
		{"explodeas", u.ExplodeAs, u.ExplodeAsDef},
		{"selfdestructas", u.SelfDestructAs, u.SelfDestructAsDef},
	}
	for _, link := range weaponRefs {
		if trimTDFSemantic(link.ref) != "" && link.def == nil {
			p.diag(SkirmishDiagnostic{Code: "missing-definition", Fatal: required, Kind: kind + "." + link.name, Entry: link.ref, Message: fmt.Sprintf("weapon definition is unavailable: catalog key %s", CanonicalKey(link.ref))})
		}
	}
	for _, link := range weaponRefs {
		weapon := link.def
		if weapon != nil {
			p.weapon(kind+".weapon", weapon, required)
		}
	}
	if u.Corpse != "" {
		p.feature(kind+".corpse", u.Corpse, required, make(map[string]bool))
	}
}

func (p *skirmishPreflight) modelAsset(kind, name string, required bool) {
	path := trimTDFSemantic(name)
	if !strings.Contains(path, "/") {
		path = "objects3d/" + path
	}
	if !strings.HasSuffix(asciiFoldContent(path), ".3do") {
		path += ".3do"
	}
	path = asciiFoldContent(path)
	if !p.file(kind, path, required) {
		return
	}
	if p.models == nil {
		p.models = make(map[string]*model.Model)
	}
	m := p.models[path]
	if m == nil {
		loaded, err := model.Load(p.fs, path)
		if err != nil {
			p.diag(SkirmishDiagnostic{Code: "malformed-3do", Fatal: required, Kind: kind, Logical: path, Message: fmt.Sprintf("cannot parse model: %v", err)})
			return
		}
		m = loaded
		p.models[path] = m
	}
}

func (p *skirmishPreflight) script(kind string, u *UnitDef, required bool) {
	path := vfs.ResourcePath("scripts", CanonicalKey(u.UnitName), "cob")
	program, found, err := cob.LoadFromFS(p.fs, u.UnitName)
	if err != nil {
		p.diag(SkirmishDiagnostic{Code: "malformed-cob", Fatal: required, Kind: kind + ".cob", Logical: path, Message: fmt.Sprintf("cannot parse COB: %v", err)})
		return
	}
	if !found || program == nil {
		p.fatal(kind+".cob", path, u.UnitName, "required COB is unavailable")
		return
	}
	p.file(kind+".cob", path, required)
	for _, entry := range []string{"Create"} {
		if !hasScript(program, entry) {
			p.diag(SkirmishDiagnostic{Code: "missing-callback", Fatal: required, Kind: kind + ".cob", Logical: path, Entry: entry, Message: fmt.Sprintf("required COB entry point is unavailable: logical path %s, expected %s", path, entry)})
		}
	}
	if !IsWeaponInactive(u.Weapon1Def) {
		for _, entry := range []string{"QueryPrimary", "AimFromPrimary", "AimPrimary", "FirePrimary"} {
			if !hasScript(program, entry) {
				p.diag(SkirmishDiagnostic{Code: "missing-callback", Fatal: required, Kind: kind + ".cob", Logical: path, Entry: entry, Message: fmt.Sprintf("required COB entry point is unavailable: logical path %s, expected %s", path, entry)})
			}
		}
	}
	if u.Builder && u.BMCode == 0 {
		for _, entry := range []string{"QueryNanoPiece", "QueryBuildInfo"} {
			if !hasScript(program, entry) {
				p.diag(SkirmishDiagnostic{Code: "missing-callback", Fatal: required, Kind: kind + ".cob", Logical: path, Entry: entry, Message: fmt.Sprintf("required COB entry point is unavailable: logical path %s, expected %s", path, entry)})
			}
		}
	}
	// Verify the full COB piece table against the already parsed 3DO hierarchy
	// [04 §4.1] [fmt cob] [03 §2.4].
	if p.models != nil {
		modelPath := "objects3d/" + CanonicalKey(u.ObjectName) + ".3do"
		if m := p.models[modelPath]; m != nil {
			pieces := make(map[string]struct{}, len(m.Pieces))
			for _, piece := range m.Pieces {
				pieces[CanonicalKey(piece.Name)] = struct{}{}
			}
			for _, piece := range program.Pieces {
				if _, ok := pieces[CanonicalKey(piece)]; !ok {
					p.diag(SkirmishDiagnostic{Code: "piece-map", Fatal: required, Kind: kind + ".piece", Logical: path, Entry: piece, Message: fmt.Sprintf("COB piece is absent from 3DO hierarchy: logical path %s, expected %s", path, piece)})
				}
			}
		}
	}
	if p.cobs == nil {
		p.cobs = make(map[string]*cob.Program)
	}
	p.cobs[path] = program
}

func hasScript(program *cob.Program, name string) bool {
	if program == nil {
		return false
	}
	for script := range program.Scripts {
		if asciiFoldContent(script) == asciiFoldContent(name) {
			return true
		}
	}
	return false
}

func (p *skirmishPreflight) weapon(kind string, w *WeaponDef, required bool) {
	if w == nil {
		return
	}
	if w.Model != "" {
		p.modelAsset(kind+".model", w.Model, required)
	}
	for _, pair := range [][2]string{{w.ExplosionGaf, w.ExplosionArt}, {w.WaterExplosionGaf, w.WaterExplosionArt}, {w.LavaExplosionGaf, w.LavaExplosionArt}} {
		if trimTDFSemantic(pair[0]) == "" && trimTDFSemantic(pair[1]) == "" {
			continue
		}
		if trimTDFSemantic(pair[0]) == "" || trimTDFSemantic(pair[1]) == "" {
			p.diag(SkirmishDiagnostic{Code: "incomplete-reference", Fatal: required, Kind: kind + ".explosion", Entry: w.CanonicalKey, Message: fmt.Sprintf("weapon explosion reference has only one half: weapon %s", w.CanonicalKey)})
			continue
		}
		gaf := p.gafFile(kind+".explosion", pair[0], required)
		p.gafEntry(kind+".explosion", gaf, pair[1], required)
	}
}

func (p *skirmishPreflight) featureGAF(kind, entry string, required bool) bool {
	entry = trimTDFSemantic(entry)
	if entry == "" {
		return false
	}
	// Feature sequence fields are authored entry names, not file names. Scan
	// the deterministic anims directory and bind the first GAF containing the
	// authored entry; this preserves the retail resource identity without
	// inventing a shared filename.
	entries, err := p.fs.ReadDir("anims")
	if err == nil {
		for _, info := range entries {
			path := asciiFoldContent(info.Path)
			if info.IsDir || !strings.HasSuffix(path, ".gaf") {
				continue
			}
			gaf := p.gafFile(kind, path, false)
			if gaf == "" {
				continue
			}
			if _, ok := p.gafs[gaf].Find(entry); ok {
				p.gafEntry(kind, gaf, entry, required)
				return true
			}
		}
	}
	p.diag(SkirmishDiagnostic{Code: "missing-gaf-entry", Fatal: required, Kind: kind, Entry: entry, Message: fmt.Sprintf("feature sequence entry is unavailable: expected %s in the anims GAF set", entry)})
	return false
}

func (p *skirmishPreflight) feature(kind, key string, required bool, seen map[string]bool) {
	key = CanonicalKey(key)
	if key == "" || seen[key] {
		return
	}
	seen[key] = true
	f, ok := p.catalog.Features[key]
	if !ok || f == nil {
		p.diag(SkirmishDiagnostic{Code: "missing-definition", Fatal: required, Kind: kind, Entry: key, Message: fmt.Sprintf("feature definition is unavailable: catalog key %s", key)})
		return
	}
	if f.Object != "" {
		p.modelAsset(kind+".3do", f.Object, required)
	}
	for _, pair := range [][2]string{{f.SeqName, "sequence"}, {f.SeqNameShad, "shadow sequence"}, {f.SeqNameBurn, "burn sequence"}, {f.SeqNameBurnShad, "burn shadow sequence"}, {f.SeqNameDie, "death sequence"}, {f.SeqNameDieShad, "death shadow sequence"}, {f.SeqNameReclamate, "reclaim sequence"}, {f.SeqNameReclamateShad, "reclaim shadow sequence"}} {
		if trimTDFSemantic(pair[0]) == "" {
			continue
		}
		p.featureGAF(kind+"."+pair[1], pair[0], required)
	}
	for _, next := range []string{f.FeatureDead, f.FeatureReclamate, f.FeatureBurnt} {
		if trimTDFSemantic(next) != "" {
			p.feature(kind+".successor", next, required, seen)
		}
	}
}

func (p *skirmishPreflight) mapAI(mh *MapHeader) {
	profile := ""
	for _, schema := range mh.Schemas {
		if trimTDFSemantic(schema.AIProfile) != "" {
			profile = trimTDFSemantic(schema.AIProfile)
			break
		}
	}
	if profile == "" {
		profile = "default"
	}
	p.manifest.AIProfile = CanonicalKey(profile)
	if _, ok := p.catalog.AIProfiles[p.manifest.AIProfile]; !ok {
		p.diag(SkirmishDiagnostic{Code: "missing-definition", Fatal: true, Kind: "ai.profile", Entry: p.manifest.AIProfile, Message: fmt.Sprintf("AI profile is unavailable: catalog key %s", p.manifest.AIProfile)})
	}
	p.file("ai.profile", "ai/"+p.manifest.AIProfile+".txt", true)
}

func (p *skirmishPreflight) finish() *SkirmishManifest {
	p.manifest.Assets = p.manifest.Assets[:0]
	for _, asset := range p.assets {
		p.manifest.Assets = append(p.manifest.Assets, asset)
	}
	sort.SliceStable(p.manifest.Assets, func(i, j int) bool {
		a, b := p.manifest.Assets[i], p.manifest.Assets[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Logical != b.Logical {
			return a.Logical < b.Logical
		}
		return a.Entry < b.Entry
	})
	p.manifest.Diagnostics = p.manifest.Diagnostics[:0]
	for _, diag := range p.diags {
		p.manifest.Diagnostics = append(p.manifest.Diagnostics, diag)
	}
	sort.SliceStable(p.manifest.Diagnostics, func(i, j int) bool {
		a, b := p.manifest.Diagnostics[i], p.manifest.Diagnostics[j]
		if a.Fatal != b.Fatal {
			return a.Fatal
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Logical != b.Logical {
			return a.Logical < b.Logical
		}
		if a.Entry != b.Entry {
			return a.Entry < b.Entry
		}
		return a.Message < b.Message
	})
	p.manifest.Hash = skirmishManifestHash(p.manifest)
	return p.manifest
}

func skirmishManifestHash(m *SkirmishManifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%d|%s|%s|%s|%s|%s|%s|%s|%s|", m.MapKey, m.Side, m.SideKey, m.AIProfile, m.Commander, m.Solar, m.Mex, m.KbotLab, m.LabProduct, "")
	for _, a := range m.Assets {
		fmt.Fprintf(&b, "a:%s|%s|%s|%s|", a.Kind, a.Logical, a.Entry, a.Provider)
	}
	for _, d := range m.Diagnostics {
		fmt.Fprintf(&b, "d:%s|%t|%s|%s|%s|%s|", d.Code, d.Fatal, d.Kind, d.Logical, d.Entry, d.Message)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
