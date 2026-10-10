package settings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/community"
)

// Settings layers (docs/DESIGN_MODS_MUTATORS.md §4.6). The settings a battle
// runs under are built in layers: the player's base settings (the file's own
// top-level block, which the original game uses as it is), then a mod's
// recommendations from its config file, then the player's own changes for
// that mod. Each layer above the base is a patch: a partial settings document
// in the file's own JSON shape, where objects merge key by key and every
// other value replaces. Only the mod-scoped paths below may appear in a patch;
// window, audio volume, mod and mutator choices are the player's alone. Unit
// restrictions are mod-scoped because they name one content set's units
// (§15.9).

// ModScoped lists the settings paths a mod may recommend and the player keeps
// per mod. A path names a key or a whole subtree.
var ModScoped = []string{
	"gameplay", "gameplayFeatures", "unitLimit",
	"presentation",
	"display.glow", "display.glowStrength",
	"switchAlt", "interfaceType", "clock",
	"audio.soundMode", "audio.mixingBuffers", "audio.cdMode",
	"keyBindings",
	"skirmish.numPlayers",
	"restrictions",
}

// atomicPaths compare and replace as a whole: a merge can neither drop a
// key from one of these maps nor clear one of their optional fields, so a
// layer that differs states them entire.
var atomicPaths = []string{"gameplayFeatures", "keyBindings", "restrictions"}

// Preset is a named, saved set of mod-scoped settings the player can apply to
// any mod (docs/DESIGN_MODS_MUTATORS.md §4.6).
type Preset struct {
	Name     string          `json:"name"`
	Settings json.RawMessage `json:"settings"`
}

// Layer applies the patches over base in order and normalizes the result.
// base is not modified.
func Layer(base Settings, patches ...json.RawMessage) (Settings, error) {
	data, err := json.Marshal(base)
	if err != nil {
		return base, err
	}
	var out Settings
	if err := json.Unmarshal(data, &out); err != nil {
		return base, err
	}
	for _, p := range patches {
		if len(p) == 0 {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(p, &doc); err != nil {
			return base, fmt.Errorf("settings: layer: %w", err)
		}
		// An atomic subtree in the patch replaces the whole field.
		if _, ok := doc["gameplayFeatures"]; ok {
			out.GameplayFeatures = community.Overrides{}
		}
		if _, ok := doc["keyBindings"]; ok {
			out.KeyBindings = KeyBindings{}
		}
		// A layer that states a restriction set replaces the set below it
		// whole, so a mod's set never merges with the base set
		// (docs/DESIGN_MODS_MUTATORS.md §15.9).
		if _, ok := doc["restrictions"]; ok {
			out.Restrictions = nil
		}
		if err := json.Unmarshal(p, &out); err != nil {
			return base, fmt.Errorf("settings: layer: %w", err)
		}
	}
	out.Normalize()
	return out, nil
}

// BaseLayer is the mod-scoped part of a complete settings block: the patch
// that puts that block's own mod-scoped values back over the live settings
// when a mod runs, so the file's base block keeps the original game's values
// (docs/DESIGN_MODS_MUTATORS.md §4.6). It is Restrict over the block's JSON,
// except that the restriction set is stated even when it is empty: the file
// omits an empty set, and a patch that omitted it would leave the running
// mod's set in the base block, which would then restrict the original game
// (§15.9).
func BaseLayer(s Settings) (json.RawMessage, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	patch, err := Restrict(data, ModScoped)
	if err != nil || len(s.Restrictions) != 0 {
		return patch, err
	}
	doc := map[string]any{}
	if len(patch) != 0 {
		if err := json.Unmarshal(patch, &doc); err != nil {
			return nil, err
		}
	}
	doc["restrictions"] = map[string]any{}
	return json.Marshal(doc)
}

// Diff is the patch that turns baseline into effective on the given paths:
// every leaf under them that differs, and every atomic subtree whole. Nil
// when nothing differs.
func Diff(effective, baseline Settings, paths []string) (json.RawMessage, error) {
	e, err := toDoc(effective)
	if err != nil {
		return nil, err
	}
	b, err := toDoc(baseline)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, path := range paths {
		diffInto(out, path, getPath(e, path), getPath(b, path))
	}
	if len(out) == 0 {
		return nil, nil
	}
	return json.Marshal(out)
}

func diffInto(out map[string]any, path string, ev, bv any) {
	em, eok := ev.(map[string]any)
	bm, bok := bv.(map[string]any)
	if eok && bok && !slices.Contains(atomicPaths, path) {
		keys := map[string]bool{}
		for k := range em {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		for _, k := range sortedKeys(keys) {
			diffInto(out, path+"."+k, em[k], bm[k])
		}
		return
	}
	if reflect.DeepEqual(ev, bv) {
		return
	}
	if ev == nil {
		// An omitted field is its zero value: an atomic object is empty, any
		// other omitted field here is a number.
		if slices.Contains(atomicPaths, path) {
			ev = map[string]any{}
		} else {
			ev = 0.0
		}
	}
	setPath(out, path, ev)
}

// Restrict migrates retired keys, then keeps only the parts of patch that lie
// on the given paths.
func Restrict(patch json.RawMessage, paths []string) (json.RawMessage, error) {
	if len(patch) == 0 {
		return nil, nil
	}
	doc, err := layerDoc(patch)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, path := range paths {
		if v := getPath(doc, path); v != nil {
			setPath(out, path, v)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return json.Marshal(out)
}

// Merge lays patch b over patch a: objects merge key by key, other values
// replace. Retired keys migrate in each layer before precedence is chosen.
// Either may be empty.
func Merge(a, b json.RawMessage) (json.RawMessage, error) {
	if len(a) == 0 && len(b) == 0 {
		return nil, nil
	}
	da, err := layerDoc(a)
	if err != nil {
		return nil, err
	}
	db, err := layerDoc(b)
	if err != nil {
		return nil, err
	}
	if da == nil {
		da = map[string]any{}
	}
	mergeInto(da, db)
	return json.Marshal(da)
}

func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeInto(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

// Without migrates retired keys, then removes the given paths, and everything
// under them, from a patch.
func Without(patch json.RawMessage, paths []string) (json.RawMessage, error) {
	if len(patch) == 0 {
		return nil, nil
	}
	doc, err := layerDoc(patch)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		parts := strings.Split(path, ".")
		cur := doc
		for _, part := range parts[:len(parts)-1] {
			next, ok := cur[part].(map[string]any)
			if !ok {
				cur = nil
				break
			}
			cur = next
		}
		if cur != nil {
			delete(cur, parts[len(parts)-1])
		}
	}
	if len(doc) == 0 {
		return nil, nil
	}
	return json.Marshal(doc)
}

func layerDoc(patch json.RawMessage) (map[string]any, error) {
	if len(patch) == 0 {
		return nil, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(patch, &doc); err != nil {
		return nil, err
	}
	if pres, ok := doc["presentation"].(map[string]any); ok {
		migrateUIScale(pres)
	}
	return doc, nil
}

func toDoc(s Settings) (map[string]any, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	return doc, json.Unmarshal(data, &doc)
}

func getPath(doc map[string]any, path string) any {
	var cur any = doc
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

func setPath(doc map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	cur := doc
	for _, part := range parts[:len(parts)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
