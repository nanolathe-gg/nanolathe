// Command modinventory mounts a set of content roots, compiles the catalog,
// and reports what a content mod asks of the engine: definition counts,
// unknown TDF keys (new against a baseline), unresolved cross-references,
// script names the baseline never uses, and limit pressure. It is a
// development probe, not a shipped tool; output is plain text.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/install"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type rootList []string

func (r *rootList) String() string     { return strings.Join(*r, ",") }
func (r *rootList) Set(s string) error { *r = append(*r, s); return nil }

type inventory struct {
	label              string
	catalog            *content.Catalog
	compileErr         error
	unitUnknown        map[string][]string // key -> unit names
	weaponUnknown      map[string][]string
	featUnknown        map[string][]string
	scriptNames        map[string]int // COB script (function) name -> number of programs defining it
	scriptFail         []string
	portReads          map[int32]map[string]bool // engine read port id -> unit names
	portWrites         map[int32]map[string]bool
	unknownOps         map[uint32]int // opcode word (masked) -> occurrences across programs
	unknownOpUnits     map[uint32]map[string]bool
	unresolved         map[string][]string // kind -> "unit -> ref"
	topDirs            []string
	stubbed            []string       // resources the catalog required but the roots lack; replaced by a donor so compile can continue
	gate               map[string]int // "copyright | version" -> FBI count, before the retail version/copyright gate
	fbiTotal           int
	fbiUnitInfoMissing int
	gamedata           []string
	providers          []string
}

func mount(roots []string) (*vfs.FS, error) {
	resolved, err := install.Resolve(roots)
	if err != nil {
		return nil, err
	}
	fs := vfs.New()
	if err := fs.MountGameDirectories(resolved); err != nil {
		fs.Close()
		return nil, err
	}
	return fs, nil
}

var logicalPathRE = regexp.MustCompile(`logical path ([^,]+),`)

// compileTolerant retries the catalog compile, supplying a donor file of the
// same extension for each missing required resource so one packaging gap does
// not hide the rest of the inventory. Every substitution is recorded.
func compileTolerant(inv *inventory, roots []string) (*vfs.FS, *content.Catalog, error) {
	overlay, err := os.MkdirTemp("", "modinventory-overlay-")
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for attempt := 0; attempt < 400; attempt++ {
		fs, err := mount(append(append([]string{}, roots...), overlay))
		if err != nil {
			return nil, nil, fmt.Errorf("mount: %w", err)
		}
		cat, err := content.Compile(wrap(fs))
		if err == nil {
			return fs, cat, nil
		}
		m := logicalPathRE.FindStringSubmatch(err.Error())
		if m == nil || seen[m[1]] {
			fs.Close()
			return nil, nil, err
		}
		logical := m[1]
		seen[logical] = true
		inv.stubbed = append(inv.stubbed, logical)
		dir, base := "", logical
		if i := strings.LastIndex(logical, "/"); i >= 0 {
			dir, base = logical[:i], logical[i+1:]
		}
		ext := strings.ToLower(base[strings.LastIndex(base, "."):])
		var donor []byte
		if entries, derr := fs.ReadDir(dir); derr == nil {
			for _, e := range entries {
				if !e.IsDir && strings.HasSuffix(strings.ToLower(e.Name), ext) && !strings.EqualFold(e.Name, base) {
					if data, rerr := fs.ReadFileLimit(dir+"/"+e.Name, 8<<20); rerr == nil {
						donor = data
						break
					}
				}
			}
		}
		fs.Close()
		target := overlay + "/" + logical
		if err := os.MkdirAll(target[:strings.LastIndex(target, "/")], 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(target, donor, 0o644); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, fmt.Errorf("gave up after 400 substitutions")
}

// mappedFS renames top-level content directories the way a patched
// executable does (e.g. units -> unitsE). The map is keyed by the retail name
// in lower case; every path whose first segment matches is redirected.
type mappedFS struct {
	inner vfs.FSOps
	m     map[string]string
}

func (f mappedFS) rewrite(name string) string {
	first, rest := name, ""
	if i := strings.IndexByte(name, '/'); i >= 0 {
		first, rest = name[:i], name[i:]
	}
	if to, ok := f.m[strings.ToLower(first)]; ok {
		return to + rest
	}
	return name
}

// unrewrite maps a provider path under a renamed directory back to the
// retail-named path the catalog expects to see in entry metadata.
func (f mappedFS) unrewrite(path string) string {
	first, rest := path, ""
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first, rest = path[:i], path[i:]
	}
	for retail, modded := range f.m {
		if strings.EqualFold(first, modded) {
			return retail + rest
		}
	}
	return path
}

func (f mappedFS) Open(name string) (vfs.File, error) { return f.inner.Open(f.rewrite(name)) }
func (f mappedFS) ReadFileLimit(name string, max int64) ([]byte, error) {
	return f.inner.ReadFileLimit(f.rewrite(name), max)
}
func (f mappedFS) ReadDir(name string) ([]vfs.EntryInfo, error) {
	entries, err := f.inner.ReadDir(f.rewrite(name))
	for i := range entries {
		entries[i].Path = f.unrewrite(entries[i].Path)
	}
	return entries, err
}
func (f mappedFS) Stat(name string) (vfs.EntryInfo, error) {
	info, err := f.inner.Stat(f.rewrite(name))
	info.Path = f.unrewrite(info.Path)
	return info, err
}
func (f mappedFS) CacheStamp(name string) (string, error) { return f.inner.CacheStamp(f.rewrite(name)) }

var dirMap = map[string]string{}

func wrap(fs *vfs.FS) vfs.FSOps {
	if len(dirMap) == 0 {
		return fs
	}
	return mappedFS{inner: fs, m: dirMap}
}

// opLen is the instruction length in words for each retail opcode, keyed by
// the dispatch-masked word. Anything absent is an opcode our VM does not
// execute; the scan resynchronises one word later and counts it.
var opLen = map[uint32]int{
	0x10001000: 3, 0x10002000: 3, 0x10003000: 3, 0x10004000: 3, 0x10005000: 2, 0x10006000: 2, 0x10007000: 2, 0x10008000: 2,
	0x10009000: 2, 0x1000a000: 2, 0x1000b000: 3, 0x1000c000: 3, 0x1000d000: 2, 0x1000e000: 2, 0x1000f000: 2, 0x10011000: 3,
	0x10012000: 3, 0x10013000: 1, 0x10021000: 2, 0x10022000: 1, 0x10023000: 2, 0x10024000: 1, 0x10031000: 1, 0x10032000: 1,
	0x10033000: 1, 0x10034000: 1, 0x10035000: 1, 0x10036000: 1, 0x10037000: 1, 0x10038000: 1, 0x10041000: 1, 0x10042000: 1,
	0x10043000: 1, 0x10044000: 1, 0x10045000: 1, 0x10051000: 1, 0x10052000: 1, 0x10053000: 1, 0x10054000: 1, 0x10055000: 1,
	0x10056000: 1, 0x10057000: 1, 0x10058000: 1, 0x10059000: 1, 0x1005a000: 1, 0x10061000: 3, 0x10062000: 3, 0x10063000: 3,
	0x10064000: 2, 0x10065000: 1, 0x10066000: 2, 0x10067000: 1, 0x10068000: 1, 0x10071000: 2, 0x10082000: 1, 0x10083000: 1,
	0x10084000: 1,
}

// scanPorts walks a program linearly and records the engine port identifiers
// its get/set instructions use, taken from the push-constant that supplies
// the identifier (the deepest of the five pushes for the five-argument read,
// the second-deepest of two for a write). Identifiers above 20 are outside
// the retail port range and therefore patched-engine extensions.
func scanPorts(inv *inventory, unit string, code []uint32) {
	const dispatchMask = 0x100FF000
	var pushed []int64 // recent push-constant operands; -1 for a non-constant push
	note := func(m map[int32]map[string]bool, id int64) {
		if id < 0 || id > 1<<20 {
			return
		}
		if m[int32(id)] == nil {
			m[int32(id)] = map[string]bool{}
		}
		m[int32(id)][unit] = true
	}
	for pc := 0; pc < len(code); {
		word := code[pc]
		op := word & dispatchMask
		n, ok := opLen[op]
		if !ok {
			inv.unknownOps[op]++
			if inv.unknownOpUnits[op] == nil {
				inv.unknownOpUnits[op] = map[string]bool{}
			}
			inv.unknownOpUnits[op][unit] = true
			pc++
			pushed = pushed[:0]
			continue
		}
		switch op {
		case 0x10021000:
			if pc+1 < len(code) && word&7 == 1 {
				pushed = append(pushed, int64(int32(code[pc+1])))
			} else {
				pushed = append(pushed, -1)
			}
			if len(pushed) > 8 {
				pushed = pushed[len(pushed)-8:]
			}
		case 0x10042000:
			if len(pushed) >= 1 {
				note(inv.portReads, pushed[len(pushed)-1])
			}
			pushed = pushed[:0]
		case 0x10043000:
			if len(pushed) >= 5 {
				note(inv.portReads, pushed[len(pushed)-5])
			}
			pushed = pushed[:0]
		case 0x10082000:
			if len(pushed) >= 2 {
				note(inv.portWrites, pushed[len(pushed)-2])
			}
			pushed = pushed[:0]
		default:
			if op != 0x10022000 && op != 0x10023000 && op != 0x10024000 {
				// arithmetic between pushes keeps the identifier push in place
				// only for the simple forms; anything else resets the window.
				if op < 0x10031000 || op > 0x1005a000 {
					pushed = pushed[:0]
				}
			}
		}
		pc += n
	}
}

var opName = map[uint32]string{
	0x10001000: "move", 0x10002000: "turn", 0x10003000: "spin", 0x10004000: "stop-spin", 0x10005000: "show", 0x10006000: "hide",
	0x10007000: "cache", 0x10008000: "dont-cache", 0x10009000: "legacy-effect", 0x1000a000: "dont-shadow", 0x1000b000: "move-now",
	0x1000c000: "turn-now", 0x1000d000: "shade", 0x1000e000: "dont-shade", 0x1000f000: "emit-sfx", 0x10011000: "wait-for-turn",
	0x10012000: "wait-for-move", 0x10013000: "sleep", 0x10021000: "push", 0x10022000: "alloc-local", 0x10023000: "pop",
	0x10024000: "discard", 0x10031000: "add", 0x10032000: "sub", 0x10033000: "mul", 0x10034000: "div", 0x10035000: "and",
	0x10036000: "or", 0x10037000: "xor", 0x10038000: "not", 0x10041000: "random", 0x10042000: "GET", 0x10043000: "GET5",
	0x10044000: "cargo-contains", 0x10045000: "carrier", 0x10051000: "lt", 0x10052000: "le", 0x10053000: "gt", 0x10054000: "ge",
	0x10055000: "eq", 0x10056000: "ne", 0x10057000: "land", 0x10058000: "lor", 0x10059000: "lxor", 0x1005a000: "lnot",
	0x10061000: "start-script", 0x10062000: "call-script", 0x10063000: "pop-n", 0x10064000: "jump", 0x10065000: "return",
	0x10066000: "jump-if-false", 0x10067000: "signal", 0x10068000: "set-signal-mask", 0x10071000: "explode", 0x10082000: "SET",
	0x10083000: "attach", 0x10084000: "detach",
}

// dumpScript prints one script's instructions from its entry until the next
// script entry (or the end of code), decoding operands by opcode length.
func dumpScript(prog *cob.Program, script string) {
	start, ok := prog.Scripts[script]
	if !ok {
		fmt.Fprintln(os.Stderr, "no such script; have:", keysSorted(prog.Scripts))
		os.Exit(1)
	}
	end := len(prog.Code)
	for _, s := range prog.Scripts {
		if s > start && s < end {
			end = s
		}
	}
	byID := map[int]string{}
	for name, idx := range prog.Scripts {
		for id, off := range prog.ScriptsByID {
			if off == idx {
				byID[id] = name
			}
		}
	}
	for pc := start; pc < end; {
		word := prog.Code[pc]
		op := word & 0x100FF000
		n, ok := opLen[op]
		name := opName[op]
		if !ok {
			fmt.Printf("%6d  ???? 0x%08x\n", pc, word)
			pc++
			continue
		}
		args := ""
		for i := 1; i < n && pc+i < len(prog.Code); i++ {
			v := int32(prog.Code[pc+i])
			args += fmt.Sprintf(" %d", v)
			if op == 0x10061000 || op == 0x10062000 {
				if i == 1 {
					args += "(" + byID[int(v)] + ")"
				}
			}
			if (op == 0x10001000 || op == 0x10002000 || op == 0x10003000 || op == 0x10004000 || op == 0x1000b000 || op == 0x1000c000 || op == 0x10011000 || op == 0x10012000 || op == 0x10071000 || op == 0x1000f000 || op == 0x10005000 || op == 0x10006000) && i == 1 && int(v) < len(prog.Pieces) && v >= 0 {
				args += "(" + prog.Pieces[v] + ")"
			}
		}
		if op == 0x10021000 {
			name += fmt.Sprintf("[%d]", word&7)
		}
		fmt.Printf("%6d  %-14s%s\n", pc, name, args)
		pc += n
	}
}

func listDir(fs vfs.FSOps, dir string) []string {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, strings.ToLower(e.Name))
	}
	sort.Strings(out)
	return out
}

func exists(fs vfs.FSOps, path string) bool {
	info, err := fs.Stat(path)
	return err == nil && !info.IsDir
}

func build(label string, roots []string) *inventory {
	inv := &inventory{label: label,
		unitUnknown: map[string][]string{}, weaponUnknown: map[string][]string{}, featUnknown: map[string][]string{},
		scriptNames: map[string]int{}, unresolved: map[string][]string{},
		portReads: map[int32]map[string]bool{}, portWrites: map[int32]map[string]bool{}, unknownOps: map[uint32]int{}, unknownOpUnits: map[uint32]map[string]bool{}}
	rawFS, cat, err := compileTolerant(inv, roots)
	if err != nil {
		inv.compileErr = err
		return inv
	}
	defer rawFS.Close()
	fs := wrap(rawFS)
	for _, p := range rawFS.Providers() {
		inv.providers = append(inv.providers, fmt.Sprintf("%s[%s,%d files]", p.ID[strings.LastIndex(p.ID, "/")+1:], p.Type, p.Files))
	}
	inv.topDirs = listDir(fs, "")
	inv.gamedata = listDir(fs, "gamedata")
	inv.catalog = cat
	inv.gate = map[string]int{}
	if entries, derr := fs.ReadDir("units"); derr == nil {
		for _, e := range entries {
			if e.IsDir || !strings.HasSuffix(strings.ToLower(e.Name), ".fbi") {
				continue
			}
			inv.fbiTotal++
			data, rerr := fs.ReadFileLimit("units/"+e.Name, 1<<20)
			if rerr != nil {
				continue
			}
			doc, perr := formats.ParseTDF(data)
			if perr != nil {
				inv.gate["<unparseable>"]++
				continue
			}
			sec := doc.Root.Section("UNITINFO")
			if sec == nil {
				inv.fbiUnitInfoMissing++
				continue
			}
			cp, _ := sec.StringValue("copyright", "")
			inv.gate[fmt.Sprintf("%q | version %v", cp, sec.FloatValue("version", 0))]++
		}
	}

	add := func(kind, from, ref string) {
		inv.unresolved[kind] = append(inv.unresolved[kind], from+" -> "+ref)
	}
	weaponOK := func(name string) bool {
		if strings.TrimSpace(name) == "" {
			return true
		}
		_, ok := cat.Weapons[content.CanonicalKey(name)]
		return ok
	}
	names := make([]string, 0, len(cat.Units))
	for k := range cat.Units {
		names = append(names, k)
	}
	sort.Strings(names)
	loader := cob.NewCachedLoader()
	for _, k := range names {
		u := cat.Units[k]
		if u == nil || u.DiscoveryOnly {
			continue
		}
		for _, key := range u.UnknownKeysSorted() {
			inv.unitUnknown[key] = append(inv.unitUnknown[key], u.UnitName)
		}
		for _, w := range []struct{ f, v string }{{"weapon1", u.Weapon1}, {"weapon2", u.Weapon2}, {"weapon3", u.Weapon3}, {"explodeas", u.ExplodeAs}, {"selfdestructas", u.SelfDestructAs}} {
			if !weaponOK(w.v) {
				add("unit "+w.f+" -> weapon", u.UnitName, w.v)
			}
		}
		if c := strings.TrimSpace(u.Corpse); c != "" {
			if _, ok := cat.Features[content.CanonicalKey(c)]; !ok {
				add("unit corpse -> feature", u.UnitName, c)
			}
		}
		if m := strings.TrimSpace(u.MovementClass); m != "" {
			if _, ok := cat.Movement[content.CanonicalKey(m)]; !ok {
				add("unit movementclass", u.UnitName, m)
			}
		}
		if s := strings.TrimSpace(u.SoundCategory); s != "" {
			if _, ok := cat.Sounds[content.CanonicalKey(s)]; !ok {
				add("unit soundcategory", u.UnitName, s)
			}
		}
		if o := strings.TrimSpace(u.ObjectName); o != "" && !exists(fs, "objects3d/"+content.CanonicalKey(o)+".3do") {
			add("unit objectname -> 3do", u.UnitName, o)
		}
		prog, found, err := loader.Load(fs, u.UnitName)
		switch {
		case err != nil:
			inv.scriptFail = append(inv.scriptFail, u.UnitName+": "+err.Error())
		case !found:
			inv.scriptFail = append(inv.scriptFail, u.UnitName+": no script")
		default:
			for name := range prog.Scripts {
				inv.scriptNames[name]++
			}
			scanPorts(inv, u.UnitName, prog.Code)
		}
	}
	wnames := make([]string, 0, len(cat.Weapons))
	for k := range cat.Weapons {
		wnames = append(wnames, k)
	}
	sort.Strings(wnames)
	for _, k := range wnames {
		w := cat.Weapons[k]
		for _, key := range w.UnknownKeysSorted() {
			inv.weaponUnknown[key] = append(inv.weaponUnknown[key], k)
		}
		if m := strings.TrimSpace(w.Model); m != "" && !exists(fs, "objects3d/"+content.CanonicalKey(m)+".3do") {
			add("weapon model -> 3do", k, m)
		}
	}
	fnames := make([]string, 0, len(cat.Features))
	for k := range cat.Features {
		fnames = append(fnames, k)
	}
	sort.Strings(fnames)
	for _, k := range fnames {
		f := cat.Features[k]
		for _, key := range f.UnknownKeysSorted() {
			inv.featUnknown[key] = append(inv.featUnknown[key], k)
		}
		if d := strings.TrimSpace(f.FeatureDead); d != "" && f.FeatureDeadDef == nil {
			add("feature featuredead", k, d)
		}
		if d := strings.TrimSpace(f.FeatureReclamate); d != "" && f.FeatureReclamateDef == nil {
			add("feature featurereclamate", k, d)
		}
	}
	for _, s := range cat.Sides {
		if s == nil {
			continue
		}
		if c := strings.TrimSpace(s.Commander); c != "" {
			if _, ok := cat.Units[content.CanonicalKey(c)]; !ok {
				add("side commander -> unit", s.Name, c)
			}
		}
	}
	return inv
}

func keysSorted[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sample(v []string, n int) string {
	if len(v) <= n {
		return strings.Join(v, ", ")
	}
	return strings.Join(v[:n], ", ") + fmt.Sprintf(", … (+%d)", len(v)-n)
}

func reportUnknown(title string, mod, base map[string][]string) {
	fmt.Printf("\n## %s\n", title)
	fmt.Printf("%-28s %6s %6s  %s\n", "key", "mod", "base", "sample definitions")
	for _, k := range keysSorted(mod) {
		b := len(base[k])
		m := len(mod[k])
		mark := "  "
		if b == 0 {
			mark = "NEW"
		}
		fmt.Printf("%-28s %6d %6d %s %s\n", k, m, b, mark, sample(mod[k], 4))
	}
}

func main() {
	var baseRoots, modRoots rootList
	flag.Var(&baseRoots, "base", "baseline content root (repeatable, load order)")
	flag.Var(&modRoots, "root", "mod content root appended after the baseline (repeatable, load order)")
	label := flag.String("label", "mod", "label for the report")
	tree := flag.Bool("tree", false, "only print per-directory file counts of the -root roots mounted alone")
	dump := flag.String("dump", "", "UNIT:SCRIPT — print a linear listing of one script from the mod roots (after -dirmap), then exit")
	dirmap := flag.String("dirmap", "", "retail=modded directory renames, comma separated (e.g. units=unitsE,weapons=weaponE); applied to the mod build only")
	flag.Parse()
	if *tree {
		fs, err := mount(modRoots)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer fs.Close()
		var walk func(dir string, depth int)
		walk = func(dir string, depth int) {
			entries, err := fs.ReadDir(dir)
			if err != nil {
				return
			}
			files, exts := 0, map[string]int{}
			for _, e := range entries {
				if !e.IsDir {
					files++
					n := strings.ToLower(e.Name)
					if i := strings.LastIndex(n, "."); i >= 0 {
						exts[n[i:]]++
					}
				}
			}
			var ex []string
			for _, k := range keysSorted(exts) {
				ex = append(ex, fmt.Sprintf("%s=%d", k, exts[k]))
			}
			fmt.Printf("%s%s/ %d files %s\n", strings.Repeat("  ", depth), dir, files, strings.Join(ex, " "))
			if depth >= 3 {
				return
			}
			for _, e := range entries {
				if e.IsDir {
					sub := e.Name
					if dir != "" {
						sub = dir + "/" + e.Name
					}
					walk(sub, depth+1)
				}
			}
		}
		walk("", 0)
		return
	}

	base := build("baseline", baseRoots)
	if base.compileErr != nil {
		fmt.Fprintln(os.Stderr, "baseline:", base.compileErr)
		os.Exit(1)
	}
	all := append(append(rootList{}, baseRoots...), modRoots...)
	for _, pair := range strings.Split(*dirmap, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(pair), "="); ok {
			dirMap[strings.ToLower(k)] = v
		}
	}
	if *dump != "" {
		unit, script, _ := strings.Cut(*dump, ":")
		fs, err := mount(all)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer fs.Close()
		prog, found, err := cob.NewCachedLoader().Load(wrap(fs), unit)
		if err != nil || !found {
			fmt.Fprintln(os.Stderr, "script:", err, found)
			os.Exit(1)
		}
		dumpScript(prog, script)
		return
	}
	mod := build(*label, all)

	fmt.Printf("# Inventory: %s\n", *label)
	fmt.Printf("roots: %s\n", strings.Join(all, " | "))
	fmt.Printf("providers (%d): %s\n", len(mod.providers), strings.Join(mod.providers, ", "))
	fmt.Printf("top-level dirs: %s\n", strings.Join(mod.topDirs, ", "))
	if mod.compileErr != nil {
		fmt.Printf("\n**CATALOG COMPILE FAILED**: %v\n", mod.compileErr)
		os.Exit(2)
	}
	c, b := mod.catalog, base.catalog
	fmt.Printf("\n## Counts (mod / baseline)\n")
	fmt.Printf("units %d / %d\nweapons %d / %d\nfeatures %d / %d\nmovement classes %d / %d\nsound categories %d / %d\nmaps %d / %d\nai profiles %d / %d\nbuild menus %d / %d\ndownload placements %d / %d\n",
		len(c.Units), len(b.Units), len(c.Weapons), len(b.Weapons), len(c.Features), len(b.Features), len(c.Movement), len(b.Movement), len(c.Sounds), len(b.Sounds), len(c.Maps), len(b.Maps), len(c.AIProfiles), len(b.AIProfiles), len(c.BuildMenus), len(b.BuildMenus), len(c.DownloadPlacements), len(b.DownloadPlacements))
	var sides []string
	for _, s := range c.Sides {
		if s != nil {
			sides = append(sides, fmt.Sprintf("%s(commander=%s)", s.Name, s.Commander))
		}
	}
	fmt.Printf("sides: %s\n", strings.Join(sides, ", "))

	// gamedata files new against baseline.
	baseGD := map[string]bool{}
	for _, g := range base.gamedata {
		baseGD[g] = true
	}
	var newGD []string
	for _, g := range mod.gamedata {
		if !baseGD[g] {
			newGD = append(newGD, g)
		}
	}
	fmt.Printf("gamedata files new vs baseline: %s\n", strings.Join(newGD, ", "))

	// Limit pressure.
	var teleporters, builders, flyers, maxSight, maxRadar, maxSonar, maxJam, maxFootX, maxFootZ, maxTransportCap, maxWeaponsPerUnit int
	var overSight []string
	for _, u := range c.Units {
		if u == nil || u.DiscoveryOnly {
			continue
		}
		if u.Teleporter {
			teleporters++
		}
		if u.Builder {
			builders++
		}
		if u.CanFly {
			flyers++
		}
		if int(u.SightDistance) > maxSight {
			maxSight = int(u.SightDistance)
		}
		if int(u.RadarDistance) > maxRadar {
			maxRadar = int(u.RadarDistance)
		}
		if int(u.SonarDistance) > maxSonar {
			maxSonar = int(u.SonarDistance)
		}
		if int(u.RadarDistanceJam) > maxJam {
			maxJam = int(u.RadarDistanceJam)
		}
		if int(u.FootprintX) > maxFootX {
			maxFootX = int(u.FootprintX)
		}
		if int(u.FootprintZ) > maxFootZ {
			maxFootZ = int(u.FootprintZ)
		}
		if int(u.TransportCapacity) > maxTransportCap {
			maxTransportCap = int(u.TransportCapacity)
		}
		n := 0
		for _, w := range []string{u.Weapon1, u.Weapon2, u.Weapon3} {
			if strings.TrimSpace(w) != "" {
				n++
			}
		}
		if n > maxWeaponsPerUnit {
			maxWeaponsPerUnit = n
		}
		if u.SightDistance > 1000 {
			overSight = append(overSight, fmt.Sprintf("%s=%d", u.UnitName, u.SightDistance))
		}
	}
	sort.Strings(overSight)
	fmt.Printf("\n## Retail unit admission gate (copyright string and version, over every units/*.fbi)\n")
	fmt.Printf("fbi files %d, without UNITINFO %d, compiled %d\n", mod.fbiTotal, mod.fbiUnitInfoMissing, len(c.Units))
	for _, k := range keysSorted(mod.gate) {
		fmt.Printf("%6d  %s\n", mod.gate[k], k)
	}
	fmt.Printf("\n## Catalog warnings (%d)\n%s\n", len(c.Warnings), sample(c.Warnings, 10))
	fmt.Printf("\n## Limit pressure\n")
	fmt.Printf("unit definitions %d (retail table 512)\nweapon definitions %d (retail table 256)\nteleporter=1 units %d\nbuilders %d, flyers %d\nmax sightdistance %d, radar %d, sonar %d, radarjam %d\nsight>1000: %s\nmax footprint %dx%d, max transportcapacity %d, max weapons/unit %d\n",
		len(c.Units), len(c.Weapons), teleporters, builders, flyers, maxSight, maxRadar, maxSonar, maxJam, sample(overSight, 12), maxFootX, maxFootZ, maxTransportCap, maxWeaponsPerUnit)

	reportUnknown("Unknown unit (FBI) keys", mod.unitUnknown, base.unitUnknown)
	reportUnknown("Unknown weapon (TDF) keys", mod.weaponUnknown, base.weaponUnknown)
	reportUnknown("Unknown feature (TDF) keys", mod.featUnknown, base.featUnknown)

	fmt.Printf("\n## Required resources missing from the roots (donor-substituted to continue) (%d)\n%s\n", len(mod.stubbed), sample(mod.stubbed, 40))
	fmt.Printf("\n## Unresolved references\n")
	for _, k := range keysSorted(mod.unresolved) {
		v := mod.unresolved[k]
		sort.Strings(v)
		fmt.Printf("%s (%d): %s\n", k, len(v), sample(v, 8))
	}
	fmt.Printf("\n## Script load failures (%d)\n%s\n", len(mod.scriptFail), sample(mod.scriptFail, 15))

	fmt.Printf("\n## Engine ports read by scripts (mod ids / baseline ids); ids above 20 are outside the retail range\n")
	printPorts := func(mod, base map[int32]map[string]bool) {
		ids := make([]int, 0, len(mod))
		for id := range mod {
			ids = append(ids, int(id))
		}
		sort.Ints(ids)
		for _, id := range ids {
			units := keysSorted(mod[int32(id)])
			mark := "   "
			if id > 20 {
				mark = "EXT"
			}
			b := 0
			if base != nil {
				b = len(base[int32(id)])
			}
			fmt.Printf("%s %5d  units %4d (base %4d)  %s\n", mark, id, len(units), b, sample(units, 5))
		}
	}
	printPorts(mod.portReads, base.portReads)
	fmt.Printf("\n## Engine ports written by scripts\n")
	printPorts(mod.portWrites, base.portWrites)
	fmt.Printf("\n## Opcode words our VM does not execute (masked op -> occurrences)\n")
	for _, k := range func() []int {
		ks := make([]int, 0)
		for op := range mod.unknownOps {
			ks = append(ks, int(op))
		}
		sort.Ints(ks)
		return ks
	}() {
		fmt.Printf("0x%08x %d (base %d) %s\n", k, mod.unknownOps[uint32(k)], base.unknownOps[uint32(k)], sample(keysSorted(mod.unknownOpUnits[uint32(k)]), 12))
	}
	fmt.Printf("\n## COB script names absent from baseline scripts\n")
	type nc struct {
		n string
		c int
	}
	var novel []nc
	for n, cnt := range mod.scriptNames {
		if base.scriptNames[n] == 0 {
			novel = append(novel, nc{n, cnt})
		}
	}
	sort.Slice(novel, func(i, j int) bool {
		if novel[i].c != novel[j].c {
			return novel[i].c > novel[j].c
		}
		return novel[i].n < novel[j].n
	})
	for _, e := range novel {
		if e.c >= 2 {
			fmt.Printf("%-32s %d\n", e.n, e.c)
		}
	}
	single := 0
	for _, e := range novel {
		if e.c < 2 {
			single++
		}
	}
	fmt.Printf("(+%d names used by a single script)\n", single)
}
