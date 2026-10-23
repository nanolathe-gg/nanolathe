package content

// The simulation-content manifest and its digest (DESIGN_MULTIPLAYER §8.7,
// §8.8). These are Nanolathe protocol values, not retail findings: the hash
// domain, the family numbers, the presence states and the encodings below are
// the published contract two clients compare before a battle may start.
//
// Every SemanticDigest is computed from the value the battle consumes — the
// prepared catalog's definitions, the programs and parsed models later unit
// creation binds, the compiled animation table, the map, AI and extension
// inputs as captured — never from Go memory layout, provider metadata or a
// file's name. Local provenance (provider, host path, byte size) is reported
// separately and excluded, so identical inputs on two installs agree.

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/model"
)

// Manifest families, in the published order (DESIGN_MULTIPLAYER §8.7).
const (
	SimulationFamilyCatalog   uint8 = 1
	SimulationFamilyCOB       uint8 = 2
	SimulationFamilyModel     uint8 = 3
	SimulationFamilySimArt    uint8 = 4
	SimulationFamilyMap       uint8 = 5
	SimulationFamilyAI        uint8 = 6
	SimulationFamilyExtension uint8 = 7
)

// Presence states of a manifest entry (DESIGN_MULTIPLAYER §8.7).
const (
	// SimulationInputAbsent is a defined absence: the owning loader found
	// nothing it could use, and that answer is part of the battle.
	SimulationInputAbsent uint8 = 0
	// SimulationInputPresent is an input the owning loader read and used.
	SimulationInputPresent uint8 = 1
	// SimulationInputFallback is the owning loader's own fallback: the named
	// input was absent and the loader used FallbackKey instead.
	SimulationInputFallback uint8 = 2
)

// simulationContentDomain is the literal hash domain of the content identity.
const simulationContentDomain = "nanolathe/sim-content/1"

// simulationInputKeyLimit is the text(1024) bound on Key and FallbackKey.
const simulationInputKeyLimit = 1024

// SimulationInput is one typed manifest record. Key names the input within
// its family; Ordinal keeps record identity where names repeat (a unit
// record's position in the immutable record order, a weapon's slot, a side's
// ordinal); FallbackKey is set only for SimulationInputFallback.
type SimulationInput struct {
	Family         uint8
	Presence       uint8
	Key            string
	FallbackKey    string
	Ordinal        uint32
	SemanticDigest [32]byte
}

// SimulationSourceFile is one file behind a manifest entry, as this install
// supplied it. It is diagnostic provenance and never part of the identity.
type SimulationSourceFile struct {
	LogicalPath  string
	ProviderID   string
	ProviderType string
	Size         int64
}

// SimulationInputProvenance names the local files behind one manifest entry.
type SimulationInputProvenance struct {
	Family  uint8
	Key     string
	Ordinal uint32
	Files   []SimulationSourceFile
}

// lessSimulationInput is the manifest's order: family, then ordinal, then key
// bytes.
func lessSimulationInput(a, b SimulationInput) bool {
	if a.Family != b.Family {
		return a.Family < b.Family
	}
	if a.Ordinal != b.Ordinal {
		return a.Ordinal < b.Ordinal
	}
	return a.Key < b.Key
}

func sortSimulationInputs(entries []SimulationInput) {
	sort.SliceStable(entries, func(i, j int) bool { return lessSimulationInput(entries[i], entries[j]) })
}

// encodeSimulationManifest is the positional encoding the digest covers:
// entry count u32, then each entry {Family u8, Key text(1024), Ordinal u32,
// Presence u8, SemanticDigest digest} followed by FallbackKey text(1024) when
// Presence is the fallback state. Integers are the shortest unsigned varint
// and text is a u32 byte length then the bytes (DESIGN_MULTIPLAYER §7.4.1,
// §8.6). The entries must already be validated and sorted.
func encodeSimulationManifest(entries []SimulationInput) []byte {
	out := make([]byte, 0, 16+len(entries)*64)
	out = binary.AppendUvarint(out, uint64(len(entries)))
	for _, e := range entries {
		out = append(out, e.Family)
		out = binary.AppendUvarint(out, uint64(len(e.Key)))
		out = append(out, e.Key...)
		out = binary.AppendUvarint(out, uint64(e.Ordinal))
		out = append(out, e.Presence)
		out = append(out, e.SemanticDigest[:]...)
		if e.Presence == SimulationInputFallback {
			out = binary.AppendUvarint(out, uint64(len(e.FallbackKey)))
			out = append(out, e.FallbackKey...)
		}
	}
	return out
}

// simulationManifestDigest is SHA-256 of the literal domain followed by the
// encoding, with no separator and no trailing NUL.
func simulationManifestDigest(entries []SimulationInput) [32]byte {
	h := sha256.New()
	h.Write([]byte(simulationContentDomain))
	h.Write(encodeSimulationManifest(entries))
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// semanticDigest builds one entry's SemanticDigest. Each family encoder starts
// with its own versioned domain, so two families can never produce one digest
// from the same fields. Encodings follow §7.4.1: unsigned varints, zigzag
// signed varints, length-prefixed text, IEEE bits for authored floats.
type semanticDigest struct {
	h       hash.Hash
	scratch [binary.MaxVarintLen64]byte
	buffer  [512]byte
	used    int
}

func newSemanticDigest(domain string) *semanticDigest {
	d := &semanticDigest{h: sha256.New()}
	d.text(domain)
	return d
}

// Compile-time identities are revalidated by checkpoints. Batch primitive
// writes without changing their framing (DESIGN_MULTIPLAYER §16.3.81).
func (d *semanticDigest) flush() {
	if d.used != 0 {
		d.h.Write(d.buffer[:d.used])
		d.used = 0
	}
}
func (d *semanticDigest) write(p []byte) {
	for len(p) != 0 {
		n := copy(d.buffer[d.used:], p)
		d.used += n
		p = p[n:]
		if d.used == len(d.buffer) {
			d.flush()
		}
	}
}
func (d *semanticDigest) u8(v uint8) { d.scratch[0] = v; d.write(d.scratch[:1]) }
func (d *semanticDigest) boolean(v bool) {
	if v {
		d.u8(1)
	} else {
		d.u8(0)
	}
}
func (d *semanticDigest) u64(v uint64) { d.write(d.scratch[:binary.PutUvarint(d.scratch[:], v)]) }
func (d *semanticDigest) u32(v uint32) { d.u64(uint64(v)) }
func (d *semanticDigest) s64(v int64)  { d.write(d.scratch[:binary.PutVarint(d.scratch[:], v)]) }
func (d *semanticDigest) s32(v int32)  { d.s64(int64(v)) }
func (d *semanticDigest) text(s string) {
	d.u64(uint64(len(s)))
	for len(s) != 0 {
		n := copy(d.buffer[d.used:], s)
		d.used += n
		s = s[n:]
		if d.used == len(d.buffer) {
			d.flush()
		}
	}
}
func (d *semanticDigest) f32(v float32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
	d.write(b[:])
}
func (d *semanticDigest) f64(v float64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
	d.write(b[:])
}
func (d *semanticDigest) digest(v [32]byte) { d.write(v[:]) }
func (d *semanticDigest) texts(values []string) {
	d.u64(uint64(len(values)))
	for _, v := range values {
		d.text(v)
	}
}
func (d *semanticDigest) sum() [32]byte {
	d.flush()
	var out [32]byte
	copy(out[:], d.h.Sum(d.buffer[:0]))
	return out
}

// Catalog family -----------------------------------------------------------

// catalogEntries is the catalog family: every compiled definition the
// simulation reads, in its semantic order. It deliberately leaves out what the
// simulation never reads — sound categories and aliases (audio presentation),
// every installed map but the selected one (the map family carries that), the
// discovery-time AI profile table (the AI family carries the selected profile
// as the planner reads it), the model-name index, warnings and Manifest
// (diagnostics) — and the regression Catalog.Hash, which mixes in provider
// provenance that differs between installs.
func catalogEntries(c *Catalog) []SimulationInput {
	var out []SimulationInput
	add := func(key string, ordinal uint32, digest [32]byte) {
		out = append(out, SimulationInput{Family: SimulationFamilyCatalog, Presence: SimulationInputPresent, Key: key, Ordinal: ordinal, SemanticDigest: digest})
	}
	if c.SurvivalRoster != nil {
		domain := "nanolathe/sim-content/catalog/survival-roster/1"
		if c.SurvivalRoster.IncludeBuildTree {
			domain = "nanolathe/sim-content/catalog/survival-roster-with-build-tree/1"
		}
		d := newSemanticDigest(domain)
		for _, e := range c.SurvivalRoster.Units {
			d.text(e.Unit)
			d.s64(int64(e.Tier))
		}
		add("survival-roster", 0, d.sum())
	}
	limits := newSemanticDigest("nanolathe/sim-content/catalog/limits/1")
	limits.s64(int64(c.Limits.Units))
	limits.s64(int64(c.Limits.Weapons))
	limits.s64(c.Limits.TNTBytes)
	limits.s64(c.Limits.LOSBytes)
	add("limits", 0, limits.sum())

	for i, u := range c.unitRecordView() {
		if u == nil {
			continue
		}
		add("unit/"+u.CanonicalKey, uint32(i+1), unitSemanticDigest(u))
	}
	for _, key := range sortedKeys(c.Weapons) {
		w := c.Weapons[key]
		if w == nil {
			continue
		}
		ordinal := uint32(0)
		if w.ID >= 0 {
			ordinal = uint32(w.ID)
		}
		add("weapon/"+key, ordinal, weaponSemanticDigest(w))
	}
	for _, key := range sortedKeys(c.Features) {
		if f := c.Features[key]; f != nil {
			add("feature/"+key, 0, featureSemanticDigest(f))
		}
	}
	for _, key := range sortedKeys(c.Movement) {
		if m := c.Movement[key]; m != nil {
			d := newSemanticDigest("nanolathe/sim-content/catalog/movement/1")
			d.text(m.Hash)
			add("movement/"+key, 0, d.sum())
		}
	}
	for i, s := range c.Sides {
		d := newSemanticDigest("nanolathe/sim-content/catalog/side/1")
		key := "side/"
		if s != nil {
			key += s.CanonicalKey
			d.boolean(true)
			d.s64(int64(s.Index))
			d.text(s.Hash)
		} else {
			d.boolean(false)
		}
		add(key, uint32(i), d.sum())
	}
	if c.Categories != nil {
		for i, e := range c.Categories.entries {
			d := newSemanticDigest("nanolathe/sim-content/catalog/category/1")
			d.text(e.Name)
			for _, word := range e.Membership.canonicalWords32(CategoryMaskWords) {
				d.u32(word)
			}
			add("category/"+e.Name, uint32(i), d.sum())
		}
	}
	for _, key := range sortedKeys(c.BuildMenus) {
		p := c.BuildMenus[key]
		if p == nil {
			continue
		}
		d := newSemanticDigest("nanolathe/sim-content/catalog/buildmenu/1")
		d.text(p.Hash)
		d.text(p.Builder)
		d.texts(p.Buttons)
		d.texts(p.AuthoredButtons)
		d.s64(int64(p.BaseButtonCount))
		add("buildmenu/"+key, 0, d.sum())
	}
	for i, p := range c.DownloadPlacements {
		d := newSemanticDigest("nanolathe/sim-content/catalog/download/1")
		d.text(p.Builder)
		d.u8(p.Menu)
		d.u8(p.Button)
		d.text(p.Product)
		d.boolean(p.BuilderResolved)
		d.boolean(p.ProductResolved)
		// The logical path is portable; provider and mount order are not.
		d.text(p.Provenance.LogicalPath)
		d.s64(int64(p.FileOrder))
		d.s64(int64(p.ItemOrder))
		add("download", uint32(i), d.sum())
	}
	if c.LOS != nil {
		d := newSemanticDigest("nanolathe/sim-content/catalog/los/1")
		d.text(c.LOS.Hash)
		d.s32(c.LOS.NumTables)
		d.u64(uint64(len(c.LOS.Tables)))
		add("los", 0, d.sum())
	} else {
		out = append(out, SimulationInput{Family: SimulationFamilyCatalog, Presence: SimulationInputAbsent, Key: "los"})
	}
	if m := c.Meteor; m != nil {
		d := newSemanticDigest("nanolathe/sim-content/catalog/meteor/1")
		d.text(m.Hash)
		d.boolean(m.DefaultPresent)
		d.boolean(m.DefaultValid)
		d.text(m.MeteorWeapon)
		d.s32(m.MeteorRadius)
		d.f32(m.MeteorDensity)
		d.f32(m.MeteorDuration)
		d.f32(m.MeteorInterval)
		add("meteor", 0, d.sum())
	} else {
		out = append(out, SimulationInput{Family: SimulationFamilyCatalog, Presence: SimulationInputAbsent, Key: "meteor"})
	}
	if s := c.Sight; s != nil {
		d := newSemanticDigest("nanolathe/sim-content/catalog/sightshapes/1")
		d.u64(uint64(len(s.Shapes)))
		for _, shape := range s.Shapes {
			d.s32(shape.W)
			d.s32(shape.H)
			d.s32(shape.AnchorX)
			d.s32(shape.AnchorY)
			d.u64(uint64(len(shape.Opaque)))
			for _, covered := range shape.Opaque {
				d.boolean(covered)
			}
		}
		add("sightshapes", 0, d.sum())
	} else {
		out = append(out, SimulationInput{Family: SimulationFamilyCatalog, Presence: SimulationInputAbsent, Key: "sightshapes"})
	}
	return out
}

// unitSemanticDigest covers a prepared unit record. The canonical record
// string (writeUnitCanonical) is recomputed here rather than taken from the
// stored Hash, so a mutator's effective values are what is digested; the
// fields that string deliberately omits because they come from other assets or
// from linking are added explicitly. The compiled program is the COB family's.
func unitSemanticDigest(u *UnitDef) [32]byte {
	d := newSemanticDigest("nanolathe/sim-content/catalog/unit/1")
	d.text(string(writeUnitCanonical(u)))
	d.s32(u.ModelTop)
	d.s32(u.ModelTopFixed)
	d.s32(u.BuildPageCount)
	d.boolean(u.HasPageZeroGUI)
	d.boolean(u.LimitEnabled)
	d.s32(u.Limit)
	d.s32(u.UnitLimit)
	// The canonical string carries the non-retail extension keys only when
	// they were authored, to keep retail definition hashes; their compiled
	// values, defaults included, are what the rules read, so they are encoded
	// here unconditionally.
	d.u8(uint8(u.Rotations))
	d.u64(uint64(len(u.VeterancyThresholds)))
	for _, threshold := range u.VeterancyThresholds {
		d.u32(threshold)
	}
	d.s32(u.VeterancyAccuracyBuffRate)
	d.texts([]string{u.TransportedExplodeAs, u.TransportedSelfDestructAs,
		u.PreviewPieces, u.PreviewPiecesS, u.PreviewPiecesE, u.PreviewPiecesN, u.PreviewPiecesW, u.PreviewObject3D})
	d.boolean(u.PreviewFaceOpponent)
	// Builder speed's recorded factor drives the builder's script timing and
	// approach. It is encoded only when present, so every definition prepared
	// without it keeps its digest (docs/DESIGN_MODS_MUTATORS.md "Builder
	// speed").
	if !u.BuilderSpeed.IsIdentity() {
		d.text("builderSpeed")
		d.u8(u.BuilderSpeed.Num)
		d.u8(u.BuilderSpeed.Den)
	}
	// Smart makers' mark drives the economy's maker switching, and likewise is
	// encoded only when present (docs/DESIGN_MODS_MUTATORS.md "Smart makers").
	if u.SmartMaker {
		d.text("smartMaker")
		d.boolean(true)
	}
	for _, link := range [...]*WeaponDef{
		u.Weapon1Def, u.Weapon2Def, u.Weapon3Def, u.ExplodeAsDef, u.SelfDestructAsDef,
		u.TransportedExplodeAsDef, u.TransportedSelfDestructAsDef,
	} {
		if link == nil {
			d.boolean(false)
			continue
		}
		d.boolean(true)
		d.text(link.CanonicalKey)
		d.s32(link.ID)
	}
	return d.sum()
}

// weaponSemanticDigest covers a prepared weapon record: its authored identity
// (the compiled canonical record's Hash, which preparation never rewrites)
// plus every field that preparation may change after compilation — the
// Community reload transform and the Damage, Blast size and Fire rate
// mutators — and the definition-side active byte.
func weaponSemanticDigest(w *WeaponDef) [32]byte {
	d := newSemanticDigest("nanolathe/sim-content/catalog/weapon/1")
	d.text(w.Hash)
	d.s32(w.ID)
	d.s32(w.ReloadTime)
	d.s32(w.AreaOfEffect)
	d.s32(w.DamageDefault)
	keys := w.DamageKeysSorted()
	d.u64(uint64(len(keys)))
	for _, key := range keys {
		d.text(key)
		d.s32(w.Damage[key])
	}
	d.u8(w.ActiveByte())
	d.boolean(w.activeByteRestored)
	return d.sum()
}

// featureSemanticDigest covers a prepared feature record: its authored
// identity plus the two pools the Build cost and Salvage mutators rewrite and
// the resolved successor links.
func featureSemanticDigest(f *FeatureDef) [32]byte {
	d := newSemanticDigest("nanolathe/sim-content/catalog/feature/1")
	d.text(f.Hash)
	d.s32(f.Metal)
	d.s32(f.Energy)
	for _, next := range [...]*FeatureDef{f.FeatureDeadDef, f.FeatureReclamateDef, f.FeatureBurntDef} {
		if next == nil {
			d.boolean(false)
			continue
		}
		d.boolean(true)
		d.text(next.CanonicalKey)
	}
	return d.sum()
}

// programSemanticDigest covers one compiled COB program: code words, entry
// points by name and in table order, piece names, static storage and the
// content checksum the loader computed.
func programSemanticDigest(p *cob.Program) [32]byte {
	d := newSemanticDigest("nanolathe/sim-content/cob/1")
	d.u64(uint64(len(p.Code)))
	for _, word := range p.Code {
		d.u32(word)
	}
	names := make([]string, 0, len(p.Scripts))
	for name := range p.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	d.u64(uint64(len(names)))
	for _, name := range names {
		d.text(name)
		d.s64(int64(p.Scripts[name]))
	}
	d.texts(p.Pieces)
	d.s64(int64(p.Statics))
	d.u64(uint64(len(p.ScriptsByID)))
	for _, index := range p.ScriptsByID {
		d.s64(int64(index))
	}
	d.u32(p.SourceChecksum)
	return d.sum()
}

// modelSemanticDigest covers one parsed model as unit creation binds it — the
// piece hierarchy, piece origins, vertices and primitives after load-time
// compilation — together with the model-top height the definition loader
// derives from the same file [02 R-CAT-01 §7].
func modelSemanticDigest(m *model.Model, top int32) [32]byte {
	d := newSemanticDigest("nanolathe/sim-content/model/1")
	d.text(m.Name)
	d.s64(int64(m.Root))
	d.u64(uint64(len(m.Pieces)))
	for _, p := range m.Pieces {
		d.text(p.Name)
		d.s64(int64(p.Parent))
		d.u64(uint64(len(p.Children)))
		for _, child := range p.Children {
			d.s64(int64(child))
		}
		for _, v := range p.Translate {
			d.s64(int64(v))
		}
		d.u64(uint64(len(p.Vertices)))
		for _, vertex := range p.Vertices {
			for _, v := range vertex {
				d.s64(int64(v))
			}
		}
		d.u64(uint64(len(p.Primitives)))
		for _, prim := range p.Primitives {
			d.u32(prim.ColorIndex)
			d.u64(uint64(len(prim.VertexIndices)))
			for _, index := range prim.VertexIndices {
				d.u32(uint32(index))
			}
			d.text(prim.TextureName)
			d.s32(prim.IsColored)
			d.s32(prim.SourceIndex)
		}
		d.boolean(p.Selection)
	}
	d.s32(top)
	return d.sum()
}

// SimArt family -------------------------------------------------------------

// simArtEntries is the animation family: each effect bank's entries and their
// frame holds, each bank that could not compile as a defined miss, and each
// feature event sequence's ordered frame geometry and delays, or its compiled
// miss. It is computed from the table's contents alone, so a table compiled at
// battle entry and an equal precompiled one agree.
func simArtEntries(a *SimArt) []SimulationInput {
	var out []SimulationInput
	if a == nil {
		return out
	}
	byBank := make(map[string][]string)
	for key := range a.effects {
		bank, _, _ := strings.Cut(key, "|")
		byBank[bank] = append(byBank[bank], key)
	}
	for _, bank := range sortedKeys(byBank) {
		keys := byBank[bank]
		sort.Strings(keys)
		d := newSemanticDigest("nanolathe/sim-content/simart/bank/1")
		d.text(bank)
		d.u64(uint64(len(keys)))
		for _, key := range keys {
			_, entry, _ := strings.Cut(key, "|")
			d.text(entry)
			holds := a.effects[key]
			d.u64(uint64(len(holds)))
			for _, hold := range holds {
				d.s32(hold)
			}
		}
		out = append(out, SimulationInput{Family: SimulationFamilySimArt, Presence: SimulationInputPresent, Key: "bank/" + bank, SemanticDigest: d.sum()})
	}
	missing := make(map[string]struct{})
	for _, diag := range a.diagnostics {
		if _, loaded := byBank[diag.Bank]; !loaded {
			missing[diag.Bank] = struct{}{}
		}
	}
	for _, bank := range sortedKeys(missing) {
		out = append(out, SimulationInput{Family: SimulationFamilySimArt, Presence: SimulationInputAbsent, Key: "bank/" + bank})
	}
	for _, key := range sortedKeys(a.sequences) {
		info := a.sequences[key]
		if info == nil {
			out = append(out, SimulationInput{Family: SimulationFamilySimArt, Presence: SimulationInputAbsent, Key: "sequence/" + key})
			continue
		}
		d := newSemanticDigest("nanolathe/sim-content/simart/sequence/1")
		d.s32(info.visits)
		d.u64(uint64(len(info.frames)))
		for _, f := range info.frames {
			d.s32(f.w)
			d.s32(f.h)
			d.s32(f.xoff)
			d.s32(f.yoff)
			d.s32(f.delay)
		}
		out = append(out, SimulationInput{Family: SimulationFamilySimArt, Presence: SimulationInputPresent, Key: "sequence/" + key, SemanticDigest: d.sum()})
	}
	return out
}

// Extension family ------------------------------------------------------------

// mutatorStepIndex maps a factor to its index on the published step list —
// ¼, ½, ¾, 1, 1½, 2, 3, 4 as 0..7 — with the zero value and 1/1 both the
// identity, 3 (DESIGN_MULTIPLAYER §8.6 field 11, DESIGN_MODS_MUTATORS §6.4).
// A factor off the list reports false; the prepared catalog already refused
// such a set, so a battle never reaches that branch.
func mutatorStepIndex(f Factor) (uint8, bool) {
	if f.IsIdentity() {
		f = Factor{Num: 1, Den: 1}
	}
	for i, step := range MutatorSteps {
		if f == step {
			return uint8(i), true
		}
	}
	return 0, false
}

// mutatorVector is the eleven shared-list step indices in the published
// field order, then the tail the manifest appends after them: nothing when
// neither Builder speed nor Smart makers is set; Builder speed's index on its
// own list (BuilderSpeedStepIndex) when only it is; and that index, 0 for the
// identity, followed by 1 when Smart makers is on. The tail's length tells the
// three apart, and every digest made without the two, before or since they
// existed, is unchanged.
func mutatorVector(m Mutators) ([11]uint8, []uint8, bool) {
	var out [11]uint8
	for i, f := range [...]Factor{
		m.BuildSpeed, m.BuildCost, m.Health, m.Damage, m.AreaOfEffect, m.Sight,
		m.Radar, m.Income, m.Salvage, m.FireRate, m.UnitSpeed,
	} {
		step, ok := mutatorStepIndex(f)
		if !ok {
			return out, nil, false
		}
		out[i] = step
	}
	builder, ok := BuilderSpeedStepIndex(m.BuilderSpeed)
	if !ok || !m.SmartMakers.validIn(SwitchSteps) {
		return out, nil, false
	}
	switch {
	case !m.SmartMakers.IsIdentity():
		return out, []uint8{builder, 1}, true
	case builder != 0:
		return out, []uint8{builder}, true
	}
	return out, nil, true
}

// BuilderSpeedStepIndex maps a Builder speed factor to its index on
// BuilderSpeedSteps — 1, 2, 3, 5, 8, 20 as 0..5 — with the zero value and 1/1
// both the identity, 0. The online configuration's Builder speed record and
// the simulation manifest share it (docs/DESIGN_MODS_MUTATORS.md "Builder
// speed"). A factor off the list reports false.
func BuilderSpeedStepIndex(f Factor) (uint8, bool) {
	if f.IsIdentity() {
		return 0, true
	}
	for i, step := range BuilderSpeedSteps {
		if f == step {
			return uint8(i), true
		}
	}
	return 0, false
}

// BuilderSpeedStep is the factor at index on BuilderSpeedSteps, the zero
// value for the identity at 0; ok is false off the list.
func BuilderSpeedStep(index uint8) (Factor, bool) {
	if int(index) >= len(BuilderSpeedSteps) {
		return Factor{}, false
	}
	if index == 0 {
		return Factor{}, true
	}
	return BuilderSpeedSteps[index], true
}

// sortedKeys returns a map's keys in byte order (I1).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
