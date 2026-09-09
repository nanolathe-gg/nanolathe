// The feature compiler.

package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// FeatureDef is a compiled feature definition [02 "Feature record"].
// DefinitionHeader must be the first field per catalog convention [02 §5].
type FeatureDef struct {
	DefinitionHeader
	// Description — string, default empty [02 "Feature record"].
	Description string // Description string empty [02 "Feature record"]
	// Footprint in cells [02 "Feature record"].
	FootprintX int32 // footprintx integer default 0 [02 "Feature record"]
	FootprintZ int32 // footprintz integer default 0 [02 "Feature record"]
	Height     int32 // height integer default 0 [02 "Feature record"]
	// Asset names [02 "Feature record"].
	Object   string // object string empty — 3DO model name [02 "Feature record"]
	Filename string // filename string empty — sprite source, used when no model [02 "Feature record"]
	// Animation sequences [02 "Feature record"].
	SeqName              string // seqname string empty [02 "Feature record"]
	SeqNameShad          string // seqnameshad string empty [02 "Feature record"]
	SeqNameBurn          string // seqnameburn string empty [02 "Feature record"]
	SeqNameBurnShad      string // seqnameburnshad string empty [02 "Feature record"]
	SeqNameDie           string // seqnamedie string empty [02 "Feature record"]
	SeqNameDieShad       string // seqnamedieshad string empty [02 "Feature record"]
	SeqNameReclamate     string // seqnamereclamate string empty [02 "Feature record"]
	SeqNameReclamateShad string // seqnamereclamateshad string empty [02 "Feature record"]
	// Economy / combat [02 "Feature record"].
	Metal  int32 // metal integer default 0 [02 "Feature record"]
	Energy int32 // energy integer default 0 [02 "Feature record"]
	Damage int32 // damage integer default 0 [02 "Feature record"]
	// Fire / regrowth [02 "Feature record"] [03 §5.1.2].
	SpreadChance  int32  // spreadchance integer default 0 [02 "Feature record"]
	Reproduce     int32  // reproduce integer default 0 [02 "Feature record"] [GAP T14]
	ReproduceArea int32  // reproducearea integer default 0 [02 "Feature record"] [GAP T14]
	SparkTime     int32  // sparktime floating default 0.0 *30 truncated ticks [02 "Feature record"]
	BurnWeapon    string // burnweapon string empty [02 "Feature record"]
	// Animation flags [02 "Feature record"].
	Animating int32 // animating integer default 0 [02 "Feature record"]
	AnimTrans int32 // animtrans integer default 0 [02 "Feature record"]
	ShadTrans int32 // shadtrans integer default 0 [02 "Feature record"]
	// Behaviour flags [02 "Feature record"].
	Flamable        bool // flamable integer 0 — spelled with one m [02 "Feature record"]
	Geothermal      bool // geothermal integer 0 — has no registry, enforced by footprint validator [05 "Geothermal requirement"] [02 "Feature record"]
	Blocking        bool // blocking integer 0 [02 "Feature record"]
	Reclaimable     bool // reclaimable integer 0 [02 "Feature record"]
	Autoreclaimable bool // autoreclaimable integer 1 — the only feature flag that defaults on [02 "Feature record"]
	Indestructible  bool // indestructible integer 0 [02 "Feature record"]
	NoDisplayInfo   bool // nodisplayinfo integer 0 [02 "Feature record"]
	NoDrawUnderGray bool // nodrawundergray integer 0 [02 "Feature record"]

	// Successor hops — authored names, empty means no successor [02 "Feature record"].
	// Second pass resolves these to catalog identities [GAP T14].
	FeatureDead      string // featuredead string empty — next feature when destroyed [02 "Feature record"]
	FeatureReclamate string // featurereclamate string empty — feature left after reclaiming [02 "Feature record"]
	FeatureBurnt     string // featureburnt string empty — burnt successor [02 "Feature record"]

	// Resolved successor pointers, nil if no successor.
	// Populated by LinkFeatureSuccessors; fatal if authored name missing [GAP T14] C9.
	FeatureDeadDef      *FeatureDef
	FeatureReclamateDef *FeatureDef
	FeatureBurntDef     *FeatureDef

	// Unknown retains inert parsed keys so a later phase can consume without re-parsing [02 §5] C14.
	Unknown map[string]string
}

// UnknownKeysSorted returns inert keys sorted for hash stability (I1).
func (f *FeatureDef) UnknownKeysSorted() []string {
	if f.Unknown == nil {
		return nil
	}
	keys := make([]string, 0, len(f.Unknown))
	for k := range f.Unknown {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		li, lj := CanonicalKey(keys[i]), CanonicalKey(keys[j])
		if li != lj {
			return li < lj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// knownFeatureKeys is the set of lower-cased keys that have a typed reader [02 "Feature record"].
// Anything not in this set is retained in Unknown (C14).
var knownFeatureKeys = map[string]struct{}{
	"description": {}, "footprintx": {}, "footprintz": {}, "height": {},
	"object": {}, "filename": {}, "seqname": {}, "seqnameshad": {},
	"seqnameburn": {}, "seqnameburnshad": {}, "seqnamedie": {}, "seqnamedieshad": {},
	"seqnamereclamate": {}, "seqnamereclamateshad": {},
	"metal": {}, "energy": {}, "damage": {},
	"spreadchance": {}, "reproduce": {}, "reproducearea": {}, "sparktime": {}, "burnweapon": {},
	"animating": {}, "animtrans": {}, "shadtrans": {},
	"flamable": {}, "geothermal": {}, "blocking": {}, "reclaimable": {}, "autoreclaimable": {},
	"indestructible": {}, "nodisplayinfo": {}, "nodrawundergray": {},
	"featuredead": {}, "featurereclamate": {}, "featureburnt": {},
}

// compileFeatureSection compiles a single feature section into a FeatureDef.
// It uses typed accessors only from formats/tdf_typed.go [02 §4].
func compileFeatureSection(section *formats.Section, featureName string, prov Provenance) *FeatureDef {
	// Identity / asset strings.
	description, _ := section.StringValue("Description", "")
	object, _ := section.StringValue("object", "")
	filename, _ := section.StringValue("filename", "")
	seqname, _ := section.StringValue("seqname", "")
	seqnameshad, _ := section.StringValue("seqnameshad", "")
	seqnameburn, _ := section.StringValue("seqnameburn", "")
	seqnameburnshad, _ := section.StringValue("seqnameburnshad", "")
	seqnamedie, _ := section.StringValue("seqnamedie", "")
	seqnamedieshad, _ := section.StringValue("seqnamedieshad", "")
	seqnamereclamate, _ := section.StringValue("seqnamereclamate", "")
	seqnamereclamateshad, _ := section.StringValue("seqnamereclamateshad", "")
	burnweapon, _ := section.StringValue("burnweapon", "")
	description = boundedString(description, 19)
	object = boundedString(object, 255)
	filename = boundedString(filename, 255)
	seqname = boundedString(seqname, 255)
	seqnameshad = boundedString(seqnameshad, 255)
	seqnameburn = boundedString(seqnameburn, 255)
	seqnameburnshad = boundedString(seqnameburnshad, 255)
	seqnamedie = boundedString(seqnamedie, 255)
	seqnamedieshad = boundedString(seqnamedieshad, 255)
	seqnamereclamate = boundedString(seqnamereclamate, 255)
	seqnamereclamateshad = boundedString(seqnamereclamateshad, 255)
	burnweapon = boundedString(burnweapon, 255)

	// Numeric scalars — integer accessor default 0 [02 "Feature record"].
	footprintx := section.IntValue("footprintx", 0)
	footprintz := section.IntValue("footprintz", 0)
	height := section.IntValue("height", 0)
	metal := section.IntValue("metal", 0)
	energy := section.IntValue("energy", 0)
	damage := section.IntValue("damage", 0)
	spreadchance := section.IntValue("spreadchance", 0)
	reproduce := section.IntValue("reproduce", 0)
	reproducearea := section.IntValue("reproducearea", 0)
	animating := section.IntValue("animating", 0) & 1
	animtrans := section.IntValue("animtrans", 0) & 1
	shadtrans := section.IntValue("shadtrans", 0) & 1

	// sparktime is the signed 16-bit seconds-to-ticks store [05 R-FEAT-01 §1].
	sparktimeFloat := section.FloatValue("sparktime", 0)
	sparktime := int32(int16(numeric.TruncateFloat64ToLow32(sparktimeFloat * 30)))

	// Retired (WU-19-143): this used to probe `resurrectspread` then
	// `jitterspread` as a guess ladder for "the retail FBI/TDF key spelling
	// for the feature resurrection spread byte" (an accepted-blocked marker).
	// Both guesses
	// are dead: the feature parser's exhaustive key census reads no such key
	// at all [05 R-FEAT-01 §1], and a full census of every stock feature
	// section (177 files, 1645 sections) confirms neither spelling nor any
	// other `*spread*` key is ever authored — the census's only `*spread*`
	// hit is the unrelated, already-modeled `spreadchance`. There is no
	// separate authored spread/jitter byte to find: the resurrection order's
	// sole simulation-RNG draw is bounded by the feature's ordinary `height`
	// byte (already read above) during the order's approach phase, and it is
	// an approach-point draw, not a placement draw — the earlier "placement
	// jitter" framing named the wrong phase entirely
	// [05 "Resurrection"][05 R-WORK-01 §7]. Nanolathe therefore carries no
	// `ResurrectSpread` field; `Height` is the byte that mattered all along.

	// Stored flag bits retain only the parsed low bit; autoreclaimable defaults
	// to one [02 R-KEYS-01 §5][05 R-FEAT-01 §1].
	flamable := storedFlag(section, "flamable", false)
	geothermal := storedFlag(section, "geothermal", false)
	blocking := storedFlag(section, "blocking", false)
	reclaimable := storedFlag(section, "reclaimable", false)
	autoreclaimable := storedFlag(section, "autoreclaimable", true)
	indestructible := storedFlag(section, "indestructible", false)
	nodisplayinfo := storedFlag(section, "nodisplayinfo", false)
	nodrawundergray := storedFlag(section, "nodrawundergray", false)

	// Successor hops — string default empty [02 "Feature record"] [GAP T14].
	//
	// Retail reads only `featurereclamate`. The misspelling
	// `featurereclamamate` (authored ten times in features/acid/acidplants.tdf,
	// with zero correct spellings) therefore leaves those records without a
	// reclaim successor in retail, and we reproduce that: the typo key is
	// retained as an inert Unknown entry only. Accepting it as an alias would
	// invent successors retail does not have (I11).
	featuredead, _ := section.StringValue("featuredead", "")
	featurereclamate, _ := section.StringValue("featurereclamate", "")
	featureburnt, _ := section.StringValue("featureburnt", "")

	// Unknown inert keys retained per C14 [02 §5]. This includes the
	// `featurereclamamate` typo, which is data retail ignores.
	unknown := make(map[string]string)
	for _, item := range section.Items {
		if item.Kind != formats.Assignment {
			continue
		}
		fold := CanonicalKey(item.Key)
		if _, ok := knownFeatureKeys[fold]; ok {
			continue
		}
		unknown[item.OriginalKey] = item.Value
	}
	if len(unknown) == 0 {
		unknown = nil
	}

	fd := &FeatureDef{
		DefinitionHeader: DefinitionHeader{
			CanonicalKey: CanonicalKey(featureName),
			Provenance:   prov,
		},
		Description:          description,
		FootprintX:           footprintx,
		FootprintZ:           footprintz,
		Height:               height,
		Object:               object,
		Filename:             filename,
		SeqName:              seqname,
		SeqNameShad:          seqnameshad,
		SeqNameBurn:          seqnameburn,
		SeqNameBurnShad:      seqnameburnshad,
		SeqNameDie:           seqnamedie,
		SeqNameDieShad:       seqnamedieshad,
		SeqNameReclamate:     seqnamereclamate,
		SeqNameReclamateShad: seqnamereclamateshad,
		Metal:                metal,
		Energy:               energy,
		Damage:               damage,
		SpreadChance:         spreadchance,
		Reproduce:            reproduce,
		ReproduceArea:        reproducearea,
		SparkTime:            sparktime,
		BurnWeapon:           burnweapon,
		Animating:            animating,
		AnimTrans:            animtrans,
		ShadTrans:            shadtrans,
		Flamable:             flamable,
		Geothermal:           geothermal,
		Blocking:             blocking,
		Reclaimable:          reclaimable,
		Autoreclaimable:      autoreclaimable,
		Indestructible:       indestructible,
		NoDisplayInfo:        nodisplayinfo,
		NoDrawUnderGray:      nodrawundergray,
		FeatureDead:          trimTDFSemantic(featuredead),
		FeatureReclamate:     trimTDFSemantic(featurereclamate),
		FeatureBurnt:         trimTDFSemantic(featureburnt),
		Unknown:              unknown,
	}

	// Hash over canonical bytes including defaults, independent of map iteration (I1) [02 §5] C12.
	var b strings.Builder
	fmt.Fprintf(&b, "%s|", fd.CanonicalKey)
	fmt.Fprintf(&b, "%s|%d|%d|%d|", fd.Description, fd.FootprintX, fd.FootprintZ, fd.Height)
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|", fd.Object, fd.Filename, fd.SeqName, fd.SeqNameShad, fd.SeqNameBurn, fd.SeqNameBurnShad, fd.SeqNameDie, fd.SeqNameDieShad, fd.SeqNameReclamate, fd.SeqNameReclamateShad)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|", fd.Metal, fd.Energy, fd.Damage, fd.SpreadChance, fd.Reproduce, fd.ReproduceArea)
	fmt.Fprintf(&b, "%d|%s|%d|%d|%d|", fd.SparkTime, fd.BurnWeapon, fd.Animating, fd.AnimTrans, fd.ShadTrans)
	// Flags as 0/1 in fixed order.
	flags := []bool{fd.Flamable, fd.Geothermal, fd.Blocking, fd.Reclaimable, fd.Autoreclaimable, fd.Indestructible, fd.NoDisplayInfo, fd.NoDrawUnderGray}
	for _, f := range flags {
		if f {
			b.WriteString("1|")
		} else {
			b.WriteString("0|")
		}
	}
	fmt.Fprintf(&b, "%s|%s|%s|", fd.FeatureDead, fd.FeatureReclamate, fd.FeatureBurnt)
	b.WriteString("unknown|")
	for _, k := range fd.UnknownKeysSorted() {
		fmt.Fprintf(&b, "%s=%s|", k, fd.Unknown[k])
	}
	fd.Hash = HashDefinition([]byte(b.String()))
	return fd
}

// LinkFeatureSuccessors resolves featuredead/featurereclamate/featureburnt hops.
// It is the second pass of the two-stage catalog construction [02 §5].
// Missing links are fatal with the verbatim message `Record "%s" missing from feature files` [GAP T14] C9.
func LinkFeatureSuccessors(features map[string]*FeatureDef) error {
	// Sort keys for deterministic error ordering (I1).
	keys := make([]string, 0, len(features))
	for k := range features {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fd := features[k]
		if trimTDFSemantic(fd.FeatureDead) != "" {
			ck := CanonicalKey(fd.FeatureDead)
			if target, ok := features[ck]; ok {
				fd.FeatureDeadDef = target
			} else {
				//lint:ignore ST1005 retail diagnostic text, reproduced verbatim [02 "Feature record"]
				return fmt.Errorf(`Record "%s" missing from feature files`, fd.FeatureDead)
			}
		}
		if trimTDFSemantic(fd.FeatureReclamate) != "" {
			ck := CanonicalKey(fd.FeatureReclamate)
			if target, ok := features[ck]; ok {
				fd.FeatureReclamateDef = target
			} else {
				//lint:ignore ST1005 retail diagnostic text, reproduced verbatim [02 "Feature record"]
				return fmt.Errorf(`Record "%s" missing from feature files`, fd.FeatureReclamate)
			}
		}
		if trimTDFSemantic(fd.FeatureBurnt) != "" {
			ck := CanonicalKey(fd.FeatureBurnt)
			if target, ok := features[ck]; ok {
				fd.FeatureBurntDef = target
			} else {
				//lint:ignore ST1005 retail diagnostic text, reproduced verbatim [02 "Feature record"]
				return fmt.Errorf(`Record "%s" missing from feature files`, fd.FeatureBurnt)
			}
		}
	}
	return nil
}

// CompileFeatures compiles features from the VFS. Discovery is recursive over
// features/<group>/*.tdf (17 group directories in retail) [PLAN 02 Discovery].
// It returns a map keyed by CanonicalKey(feature name) [02 §5].
// The helper uses only typed accessors from formats/tdf_typed.go [02 §4].
func CompileFeatures(fs vfs.FSOps) (map[string]*FeatureDef, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	result := make(map[string]*FeatureDef)

	// Recursive discovery — features is a directory with subdirs [PLAN 02 Discovery].
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := fs.ReadDir(dir)
		if err != nil {
			// Missing features directory is not silent; caller decides fatal vs empty.
			// For the top-level features dir, propagate error.
			return err
		}
		// ReadDir already sorts by Path [vfs.ReadDir], so iteration is stable (I1).
		for _, e := range entries {
			if e.IsDir {
				if err := walk(e.Path); err != nil {
					return err
				}
				continue
			}
			// Filter by extension — only *.tdf are definitions.
			if !strings.HasSuffix(asciiFoldContent(e.Path), ".tdf") {
				continue
			}
			data, err := fs.ReadFileLimit(e.Path, 1<<20)
			if err != nil {
				continue
			}
			prov := ProvenanceFrom(e)
			doc, err := formats.ParseTDF(data)
			if err != nil {
				return formats.WithTDFContext(fs, err, e.Path)
			}
			for _, section := range doc.Root.Sections() {
				name := trimTDFSemantic(section.OriginalName)
				if name == "" {
					name = trimTDFSemantic(section.Name)
				}
				if name == "" {
					continue
				}
				fd := compileFeatureSection(section, name, prov)
				key := CanonicalKey(name)
				// Duplicate section names retain the first parsed record [02
				// "Feature record"]. ReadDir and section order are deterministic.
				if _, exists := result[key]; !exists {
					result[key] = fd
				}
			}
		}
		return nil
	}

	if err := walk("features"); err != nil {
		return nil, fmt.Errorf("content: features: %w", err)
	}

	// Second pass: resolve successor hops, fatal on missing [GAP T14] C9.
	if err := LinkFeatureSuccessors(result); err != nil {
		return nil, err
	}
	return result, nil
}
