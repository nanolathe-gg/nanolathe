package aikit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/tactics"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utility"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// With no configured parameters the game builds the default util+tac:
// the tuned utility weights, the default variety (a style and a personality
// drawn per game, with jitter) and every tactics component on, tempered by
// the strategy's personality.
func TestTheUnconfiguredPlayBrainIsUnchanged(t *testing.T) {
	st, ec, pr := utility.Policies(utility.DefaultParams())
	tp := tactics.DefaultParams()
	tp.Temper = armyTemper{st}
	before := core.New("util+tac", st, ec, tactics.New(tp), pr)
	if got := controllerBrain(""); !reflect.DeepEqual(got, before) {
		t.Fatalf("the unconfigured play brain differs from the default util+tac:\n got %#v\nwant %#v", got, before)
	}
	// A configured one differs, so the comparison can see a change.
	if reflect.DeepEqual(controllerBrain("style=eco"), before) {
		t.Fatal("style=eco built the default brain")
	}
}

// ValidateParams is the strict check a configuration passes before a
// battle: an unknown key, a value a layer would clamp or ignore, and a
// malformed text are all refused, and a set that mixes the three layers'
// keys is accepted.
func TestValidateParamsRejectsWhatTheBrainWouldNotRead(t *testing.T) {
	if _, err := ValidateParamsText("style=eco,jitter=0,w_army=120,tower_time=1,micro=0,hn=5,pv=main,em=-40,air=0"); err != nil {
		t.Fatalf("a valid set was refused: %v", err)
	}
	if _, err := ValidateParamsText("personality=turtle,trait_towers=-40,trait_aggression=100"); err != nil {
		t.Fatalf("a valid personality was refused: %v", err)
	}
	if _, err := ValidateParamsText("personality=off,trait_raids=-100"); err != nil {
		t.Fatalf("a pinned trait without a personality was refused: %v", err)
	}
	for _, bad := range []string{
		"w_amry=120",        // unknown key
		"skill=90",          // an arena persona key: the lobby difficulty picks the persona
		"label=x",           // an arena label
		"w_army=401",        // outside the utility range (ParseParams would clamp)
		"w_army=x",          // not an integer
		"air=+0",            // not plainly spelled: tactics would read it as on
		"w_army=0120",       // not plainly spelled
		"style=rush",        // not a style
		"jitter=2",          // a variety switch is 0 or 1
		"micro=false",       // a tactics switch is 0 or 1
		"hn=0",              // tactics ignores a harass size below one
		"pv=all",            // pv takes main
		"style=eco,,x=1",    // malformed text
		"personality=rush",  // not a personality
		"personality=0",     // the draw is turned off with the word off
		"trait_towers=101",  // outside -100..100
		"trait_towers=+5",   // not plainly spelled
		"trait_tower=5",     // unknown key
		"trait_towers=high", // not an integer
	} {
		if _, err := ValidateParamsText(bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
}

// The strict vocabulary is exactly the keys the layers read. Each listed
// variety and tactics key changes what its layer's reader returns, and a
// scan of the two readers finds no key the list lacks, so a key added to a
// layer fails here until ValidateParams names it.
func TestTheVocabularyIsWhatTheLayersRead(t *testing.T) {
	varietySample := map[string]string{"style": "eco", "jitter": "0", "att_curve": "0", "att_share": "50", "att_floor": "500", "att_lag": "0", "att_build": "0", "tower_time": "1", "wide_base": "0",
		"open_reclaim": "0", "open_reclaim_hi": "400", "open_reclaim_end": "10", "open_army": "0", "open_fam": "1", "open_follow": "1",
		"personality": "off"}
	for _, k := range utility.TraitKeys {
		varietySample[k] = "40"
	}
	for _, k := range varietyKeys {
		v, err := utility.VarietyFrom(map[string]string{k: varietySample[k]})
		if err != nil || v == utility.DefaultVariety() {
			t.Fatalf("variety key %s=%s is not read (%v)", k, varietySample[k], err)
		}
	}
	for _, k := range tacticsKeys {
		value := "0"
		switch {
		case k.name == "pv":
			value = "main"
		case k.knob:
			value = "7"
		}
		if reflect.DeepEqual(tactics.ParamsFrom(map[string]string{k.name: value}), tactics.DefaultParams()) {
			t.Fatalf("tactics key %s=%s is not read", k.name, value)
		}
	}
	listed := slices.Collect(func(yield func(string) bool) {
		for _, k := range varietyKeys {
			yield(k)
		}
		for _, k := range tacticsKeys {
			yield(k.name)
		}
	})
	for _, found := range readerKeys(t, "utility", "VarietyFrom", "random") {
		if !slices.Contains(listed, found) {
			t.Errorf("utility.VarietyFrom reads %q, which ValidateParams does not name", found)
		}
	}
	for _, found := range readerKeys(t, "tactics", "ParamsFrom", "false", "main") {
		if !slices.Contains(listed, found) {
			t.Errorf("tactics.ParamsFrom reads %q, which ValidateParams does not name", found)
		}
	}
}

// readerKeys lists the key-shaped string literals in one function of a
// brain package: the keys it reads, less the named values it compares with.
func readerKeys(t *testing.T, pkg, fn string, values ...string) []string {
	t.Helper()
	dir := filepath.Join("..", "..", "internal", "aikit", "brains", pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := regexp.MustCompile(`^[a-z][a-z_]*$`)
	var out []string
	fset := token.NewFileSet()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			decl, ok := d.(*ast.FuncDecl)
			if !ok || decl.Recv != nil || decl.Name.Name != fn {
				continue
			}
			ast.Inspect(decl.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err == nil && key.MatchString(s) && !slices.Contains(values, s) {
					out = append(out, s)
				}
				return true
			})
		}
	}
	if len(out) == 0 {
		t.Fatalf("found no %s.%s to scan", pkg, fn)
	}
	return out
}

// The army is tempered by the strategy it is built with: the game and
// the arena hand the tactics army the utility strategy's drawn personality
// (utility README §13.15), so its aggression and raid appetite reach the
// margins and the raid sizes.
func TestTheArmyIsTemperedByItsStrategy(t *testing.T) {
	ut, err := NewUtilTac(map[string]string{"personality": "rusher"})
	if err != nil {
		t.Fatal(err)
	}
	if tp, ok := ut.Army.P.Temper.(armyTemper); !ok || tp.st != ut.Strategy {
		t.Fatalf("the army's temper is %#v, want the brain's own strategy", ut.Army.P.Temper)
	}
	// Before the strategy has drawn, its temper is neutral.
	if got := (armyTemper{ut.Strategy}).ArmyTemper(); got != (tactics.Temper{Engage: 0, RaidPct: 100, HarassPct: 100}) {
		t.Fatalf("an undrawn personality tempers the army: %+v", got)
	}
}
