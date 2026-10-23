// Mutators: global multipliers applied to a per-battle catalog clone
// (docs/DESIGN_MODS_MUTATORS.md §6). A mutator is a transform of compiled
// content, not a gameplay rule: it adds no seam, draws no random number, keeps
// no per-tick state and is never consulted inside a tick, so it applies in
// every gameplay mode, Strict 3.1 included (INVARIANTS I11). Strict 3.1 with
// no mutators is the retail baseline. Builder speed and Smart makers are the
// two the maintainer accepted whose recorded value engine code reads (§6.7,
// §6.8).

package content

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Factor is an exact rational multiplier from the fixed step list
// (docs/DESIGN_MODS_MUTATORS.md §6.4). The zero value is the identity.
type Factor struct{ Num, Den uint8 }

// MutatorSteps is the shared closed step list, in ascending order, which
// every mutator but Builder speed uses. Settings, flags and sidecars refuse
// any other value, which keeps the test matrix finite and every product exact
// (docs/DESIGN_MODS_MUTATORS.md §6.4, P4).
var MutatorSteps = []Factor{{1, 4}, {1, 2}, {3, 4}, {1, 1}, {3, 2}, {2, 1}, {3, 1}, {4, 1}}

// BuilderSpeedSteps is Builder speed's own closed step list, in ascending
// order: whole multiples only, with the identity first, because its factor
// multiplies script animation rates and divides sleeps
// (docs/DESIGN_MODS_MUTATORS.md §6.4, "Builder speed").
var BuilderSpeedSteps = []Factor{{1, 1}, {2, 1}, {3, 1}, {5, 1}, {8, 1}, {20, 1}}

// SwitchSteps is the closed list of a mutator that is switched on or off
// rather than scaled: off, the identity, then on. Its steps are spelled "off"
// and "on" (MutatorInfo.Spell), and the on step's value carries no
// arithmetic; Smart makers is the one such mutator
// (docs/DESIGN_MODS_MUTATORS.md §6.4, "Smart makers"). A list is a switch
// when it equals this one, so a scaled mutator must not use the steps ×1 and
// ×2 alone; TestMutatorCatalogDescribesTheClosedSet fails if one does.
var SwitchSteps = []Factor{{1, 1}, {2, 1}}

// SwitchOn is a switch mutator's on step, SwitchSteps[1]. The online
// configuration's Smart makers record and the simulation manifest name it.
func SwitchOn() Factor { return SwitchSteps[1] }

// isSwitchList reports whether steps is SwitchSteps, compared by value
// because MutatorCatalog hands out copies.
func isSwitchList(steps []Factor) bool {
	return len(steps) == len(SwitchSteps) && steps[0] == SwitchSteps[0] && steps[1] == SwitchSteps[1]
}

// spellIn is f's canonical spelling on steps: "off" or "on" on a switch's
// list, and the factor's decimal on every other list.
func spellIn(steps []Factor, f Factor) string {
	if isSwitchList(steps) {
		if f.IsIdentity() {
			return "off"
		}
		return "on"
	}
	return f.String()
}

// parseFactorIn reads one step of steps in its canonical spelling:
// "0.25", "0.5", "0.75", "1", "1.5", "2", "3" or "4" on the shared list,
// "2", "3", "5", "8" or "20" on Builder speed's, and "off" or "on" on a
// switch's. Any other text, including an equal value spelled differently
// ("1/2", ".5", "2.0", "On"), is an error.
func parseFactorIn(s string, steps []Factor) (Factor, error) {
	text := strings.TrimSpace(s)
	for _, step := range steps {
		if spellIn(steps, step) == text {
			return step, nil
		}
	}
	return Factor{}, fmt.Errorf("content: mutator factor %q is not one of %s", s, stepSpellings(steps))
}

func stepSpellings(steps []Factor) string {
	names := make([]string, len(steps))
	for i, step := range steps {
		names[i] = spellIn(steps, step)
	}
	return strings.Join(names, ", ")
}

// IsIdentity reports whether the factor changes nothing: the zero value or 1/1.
func (f Factor) IsIdentity() bool { return f == (Factor{}) || f == (Factor{1, 1}) }

// String is the canonical spelling parseFactorIn reads, a decimal because most
// players read "1.5" more easily than "3/2": "2", "0.5", "0.75", and "1" for
// the identity. Every step is a whole number of quarters, so every step has
// an exact decimal; a factor off the list with no short exact decimal keeps
// the fraction form.
func (f Factor) String() string {
	if f.IsIdentity() {
		return "1"
	}
	if f.Den == 0 {
		return fmt.Sprintf("%d/%d", f.Num, f.Den)
	}
	num, den := int(f.Num), int(f.Den)
	text := strconv.Itoa(num / den)
	rest := num % den
	if rest == 0 {
		return text
	}
	text += "."
	for digits := 0; rest != 0; digits++ {
		if digits == 4 {
			return fmt.Sprintf("%d/%d", f.Num, f.Den)
		}
		rest *= 10
		text += strconv.Itoa(rest / den)
		rest %= den
	}
	return text
}

// Label is the player-facing spelling: "×2", "×0.5", "×1".
func (f Factor) Label() string { return "×" + f.String() }

// NextIn is the following step of steps, an ascending list such as a
// MutatorInfo's Steps, clamped at its last step. A factor that is not on
// the list moves to the first step above its value; the zero value cycles as
// 1/1.
func (f Factor) NextIn(steps []Factor) Factor {
	if len(steps) == 0 {
		return f
	}
	if f.IsIdentity() || f.Den == 0 {
		f = Factor{1, 1}
	}
	for _, step := range steps {
		// step > f, compared by cross-multiplication.
		if uint16(step.Num)*uint16(f.Den) > uint16(f.Num)*uint16(step.Den) {
			return step
		}
	}
	return steps[len(steps)-1]
}

// PrevIn is the preceding step of steps, clamped at its first step. A factor
// that is not on the list moves to the last step below its value; the zero
// value cycles as 1/1.
func (f Factor) PrevIn(steps []Factor) Factor {
	if len(steps) == 0 {
		return f
	}
	if f.IsIdentity() || f.Den == 0 {
		f = Factor{1, 1}
	}
	for i := len(steps) - 1; i >= 0; i-- {
		// step < f, compared by cross-multiplication.
		if step := steps[i]; uint16(step.Num)*uint16(f.Den) < uint16(f.Num)*uint16(step.Den) {
			return step
		}
	}
	return steps[0]
}

// inverse is 1/f. Every step's inverse is itself exact: the identity maps to
// the identity and Num/Den to Den/Num.
func (f Factor) inverse() Factor { return Factor{Num: f.Den, Den: f.Num} }

// validIn reports whether the factor is the identity or one of steps.
func (f Factor) validIn(steps []Factor) bool {
	if f.IsIdentity() {
		return true
	}
	for _, step := range steps {
		if f == step {
			return true
		}
	}
	return false
}

// scale applies the factor to one stored integer v with store maximum limit
// (docs/DESIGN_MODS_MUTATORS.md §6.4). v <= 0 is unchanged: zero is the absent
// value for these keys and a negative value is a malformed input with its own
// retail arms [05 R-WORK-01 §11], so a mutator neither creates nor moves one.
// v > 0 becomes max(1, (v·Num + ⌊Den/2⌋) div Den) in 64-bit integers — round
// half up — then saturates at limit. Saturating rather than wrapping means a
// mutator never introduces a wrap.
func (f Factor) scale(v, limit int64) int64 {
	if f.IsIdentity() {
		return v
	}
	return f.ratio().scale(v, limit)
}

// ratio is an exact product of factors held in 64-bit integers. Builder speed
// composes with Unit speed as k_u²·k_b, up to 16·20, which a Factor's 8-bit
// numerator cannot hold, so that product is formed here and never stored
// (docs/DESIGN_MODS_MUTATORS.md §6.4).
type ratio struct{ num, den int64 }

// ratio is the factor as an exact ratio; the zero value is 1/1.
func (f Factor) ratio() ratio {
	if f.IsIdentity() {
		return ratio{1, 1}
	}
	return ratio{int64(f.Num), int64(f.Den)}
}

func (r ratio) times(o ratio) ratio { return ratio{r.num * o.num, r.den * o.den} }

func (r ratio) isIdentity() bool { return r.num == r.den }

// scale is §6.4's rule for the exact ratio: v <= 0 unchanged, otherwise
// max(1, (v·num + ⌊den/2⌋) div den), round half up, saturated at limit. Every
// operand is at most 2^31·320, well inside 64 bits.
func (r ratio) scale(v, limit int64) int64 {
	if v <= 0 || r.isIdentity() {
		return v
	}
	out := (v*r.num + r.den/2) / r.den
	if out < 1 {
		out = 1
	}
	if out > limit {
		out = limit
	}
	return out
}

// Mutators is the closed set of global multipliers one battle runs under. The
// zero value changes nothing. A new mutator is a new field, a row in
// mutatorFields and its tests, not a plugin (docs/DESIGN_MODS_MUTATORS.md §6.2).
type Mutators struct {
	// BuildSpeed makes every builder k× faster by scaling each unit's
	// buildtime by the inverse factor 1/k. P1 revised: inverse buildtime,
	// because the worker quantum is workertime/30 in integers
	// [05 R-WORK-01 §1] — scaling workertime would quantize every builder's
	// speed unevenly and stop builders whose scaled workertime fell below
	// thirty. WorkerTime is left alone. See ApplyMutators for what follows
	// from buildtime.
	BuildSpeed Factor
	// BuildCost scales unit buildcostmetal and buildcostenergy, and the metal
	// and energy of every feature reachable from a unit's corpse chain (P2).
	BuildCost Factor
	// Health scales unit maxdamage.
	Health Factor
	// Damage scales every weapon's default damage and each per-unit damage
	// override, death and self-destruct explosions included.
	Damage Factor
	// AreaOfEffect scales every splash weapon's areaofeffect, and so its blast
	// radius, death and self-destruct explosions included. A direct-hit weapon
	// (areaofeffect 16 or less) is left alone, and a splash weapon never
	// scales into that class.
	AreaOfEffect Factor
	// Sight scales unit sightdistance.
	Sight Factor
	// Radar scales unit radardistance and sonardistance, and the two jamming
	// distances by the same factor (P3).
	Radar Factor
	// Income multiplies what every unit produces beyond its own upkeep:
	// energymake's surplus over a positive energyuse, metalmake,
	// extractsmetal, windgenerator, tidalgenerator and the negative-energyuse
	// production arm. Upkeep, storage, makesmetal and costs are left alone.
	Income Factor
	// Salvage scales the metal and energy of every feature definition that is
	// not indestructible: wrecks, heaps, rocks and trees. A corpse-chain
	// feature takes Build cost and Salvage as one product.
	Salvage Factor
	// FireRate makes every weapon fire k× as often by scaling its reloadtime
	// by the inverse factor 1/k, as Build speed does buildtime. Cost per shot
	// is unchanged.
	FireRate Factor
	// UnitSpeed makes every unit move and turn k× as fast along the same
	// paths: maxvelocity, turnrate and the two movement-rate thresholds by k,
	// acceleration by k², and brakerate by k² on the ground and by k in the
	// air, where the flight integrator reads it as a speed.
	UnitSpeed Factor
	// BuilderSpeed makes every builder k× as agile and its build animations
	// k× as fast, from its own step list (BuilderSpeedSteps): a mobile
	// builder's turnrate by k, and a ground mobile builder's acceleration and
	// brakerate by k, leaving top speed alone; the factor is recorded on every
	// builder definition (UnitDef.BuilderSpeed), where its script VM and its
	// mobile-build approach read it. Shown as "Builder speed".
	BuilderSpeed Factor
	// SmartMakers is a switch on SwitchSteps, not a multiplier: when on,
	// every metal-maker definition records UnitDef.SmartMaker, and after each
	// of a human player's settlement passes the economy switches that
	// player's finished makers on above 80% of energy storage and off below
	// 40%, as many as the measured surplus or shortfall needs, most efficient
	// first on and least efficient first off. Shown as "Smart makers"
	// (docs/DESIGN_MODS_MUTATORS.md "Smart makers").
	SmartMakers Factor
}

// MutatorInfo describes one mutator for a front end, so a screen builds its
// rows from data instead of naming fields. Group is a short heading. Label
// and Description are player-facing and ASCII only, because the retail fonts
// have no multiplication sign. Steps is the mutator's closed step list in
// ascending order, the identity included.
type MutatorInfo struct {
	Key, Label, Group, Description string
	Steps                          []Factor
}

// IsSwitch reports whether the mutator is switched on and off (SwitchSteps)
// rather than scaled.
func (i MutatorInfo) IsSwitch() bool { return isSwitchList(i.Steps) }

// Spell is f's canonical spelling on the mutator's list, the text settings,
// flags and ParseMutators read: "2", "0.5", or "on" and "off" for a switch.
func (i MutatorInfo) Spell(f Factor) string { return spellIn(i.Steps, f) }

// Value is the player-facing value: "×2", or "On" and "Off" for a switch.
func (i MutatorInfo) Value(f Factor) string {
	if i.IsSwitch() {
		return switchValue(f)
	}
	return f.Label()
}

// ValueASCII is Value spelled for the retail fonts, which have no
// multiplication sign: "x2", or "On" and "Off".
func (i MutatorInfo) ValueASCII(f Factor) string {
	if i.IsSwitch() {
		return switchValue(f)
	}
	return "x" + f.String()
}

func switchValue(f Factor) string {
	if f.IsIdentity() {
		return "Off"
	}
	return "On"
}

// mutatorField is one row of the closed set: its descriptor and its field.
type mutatorField struct {
	MutatorInfo
	get func(*Mutators) *Factor
}

// steps is the row's closed step list.
func (f mutatorField) steps() []Factor { return f.Steps }

// mutatorFields lists the set in the design table's order, grouped, which is
// the order Describe and MutatorCatalog present
// (docs/DESIGN_MODS_MUTATORS.md §6.5).
var mutatorFields = []mutatorField{
	{MutatorInfo{"buildSpeed", "Build speed", "Economy", "Divides every unit's build time; total cost is unchanged.", MutatorSteps}, func(m *Mutators) *Factor { return &m.BuildSpeed }},
	{MutatorInfo{"buildCost", "Build cost", "Economy", "Multiplies unit costs and the value of their wrecks.", MutatorSteps}, func(m *Mutators) *Factor { return &m.BuildCost }},
	{MutatorInfo{"income", "Income", "Economy", "Multiplies production beyond each unit's upkeep.", MutatorSteps}, func(m *Mutators) *Factor { return &m.Income }},
	{MutatorInfo{"salvage", "Salvage", "Economy", "Multiplies the value of wrecks, rocks and trees.", MutatorSteps}, func(m *Mutators) *Factor { return &m.Salvage }},
	{MutatorInfo{"smartMakers", "Smart makers", "Economy", "Your metal makers switch on at 80 percent energy, off below 40 percent.", SwitchSteps}, func(m *Mutators) *Factor { return &m.SmartMakers }},
	{MutatorInfo{"health", "Health", "Combat", "Multiplies every unit's hit points.", MutatorSteps}, func(m *Mutators) *Factor { return &m.Health }},
	{MutatorInfo{"damage", "Damage", "Combat", "Multiplies the damage of every weapon and explosion.", MutatorSteps}, func(m *Mutators) *Factor { return &m.Damage }},
	{MutatorInfo{"areaOfEffect", "Blast size", "Combat", "Multiplies every blast radius; direct hits are unchanged.", MutatorSteps}, func(m *Mutators) *Factor { return &m.AreaOfEffect }},
	{MutatorInfo{"fireRate", "Fire rate", "Combat", "Divides every reload time; cost per shot is unchanged.", MutatorSteps}, func(m *Mutators) *Factor { return &m.FireRate }},
	{MutatorInfo{"unitSpeed", "Unit speed", "Movement", "Multiplies how fast every unit moves and turns.", MutatorSteps}, func(m *Mutators) *Factor { return &m.UnitSpeed }},
	{MutatorInfo{"builderSpeed", "Builder speed", "Builders", "Builders animate, turn and accelerate faster and start work in range.", BuilderSpeedSteps}, func(m *Mutators) *Factor { return &m.BuilderSpeed }},
	{MutatorInfo{"sight", "Sight", "Vision", "Multiplies every unit's line of sight.", MutatorSteps}, func(m *Mutators) *Factor { return &m.Sight }},
	{MutatorInfo{"radar", "Radar", "Vision", "Multiplies radar, sonar and jamming ranges.", MutatorSteps}, func(m *Mutators) *Factor { return &m.Radar }},
}

// mutatorFieldsByKey is mutatorFields in ascending key order, the canonical
// order of String and of every key walk (I1).
var mutatorFieldsByKey = func() []mutatorField {
	out := append([]mutatorField(nil), mutatorFields...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}()

// MutatorCatalog returns the descriptors in the design table's order, grouped:
// Economy (build speed, build cost, income, salvage, smart makers), Combat (health, damage,
// blast size, fire rate), Movement (unit speed), Builders (builder speed),
// Vision (sight, radar). The slice and each step list are copies.
func MutatorCatalog() []MutatorInfo {
	out := make([]MutatorInfo, len(mutatorFields))
	for i, field := range mutatorFields {
		out[i] = field.MutatorInfo
		out[i].Steps = append([]Factor(nil), field.Steps...)
	}
	return out
}

func mutatorKeys() string {
	keys := make([]string, len(mutatorFieldsByKey))
	for i, field := range mutatorFieldsByKey {
		keys[i] = field.Key
	}
	return strings.Join(keys, ", ")
}

// ParseMutators reads the settings and flag form, key to canonical factor
// spelling ("on" and "off" for a switch). Keys are walked in sorted order, so the first error reported is
// deterministic (I1). An unknown key or a factor off that key's step list is
// an error naming it. An identity factor parses to the zero value.
func ParseMutators(values map[string]string) (Mutators, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var m Mutators
	for _, key := range keys {
		field, ok := mutatorFieldByKey(key)
		if !ok {
			return Mutators{}, fmt.Errorf("content: unknown mutator %q, expected one of %s", key, mutatorKeys())
		}
		factor, err := parseFactorIn(values[key], field.steps())
		if err != nil {
			return Mutators{}, fmt.Errorf("content: mutator %s factor %q is not one of %s", key, values[key], stepSpellings(field.steps()))
		}
		if factor.IsIdentity() {
			factor = Factor{}
		}
		*field.get(&m) = factor
	}
	return m, nil
}

func mutatorFieldByKey(key string) (mutatorField, bool) {
	for _, field := range mutatorFieldsByKey {
		if field.Key == key {
			return field, true
		}
	}
	return mutatorField{}, false
}

// Factor returns the factor stored for key, and false for an unknown key.
func (m Mutators) Factor(key string) (Factor, bool) {
	field, ok := mutatorFieldByKey(key)
	if !ok {
		return Factor{}, false
	}
	return *field.get(&m), true
}

// SetFactor stores f for key. It refuses an unknown key and a factor off that
// key's step list; an identity factor is stored as the zero value, so a set
// edited back to 1 compares equal to the zero set.
func (m *Mutators) SetFactor(key string, f Factor) error {
	field, ok := mutatorFieldByKey(key)
	if !ok {
		return fmt.Errorf("content: unknown mutator %q, expected one of %s", key, mutatorKeys())
	}
	if !f.validIn(field.steps()) {
		return fmt.Errorf("content: mutator %s factor %d/%d is not one of %s", key, f.Num, f.Den, stepSpellings(field.steps()))
	}
	if f.IsIdentity() {
		f = Factor{}
	}
	*field.get(m) = f
	return nil
}

// Map is the inverse of ParseMutators. Identity factors are omitted, so the
// zero value maps to an empty map.
func (m Mutators) Map() map[string]string {
	out := make(map[string]string)
	for _, field := range mutatorFieldsByKey {
		if f := *field.get(&m); !f.IsIdentity() {
			out[field.Key] = field.Spell(f)
		}
	}
	return out
}

// IsZero reports whether every factor is the identity, so applying the set
// would change nothing.
func (m Mutators) IsZero() bool {
	for _, field := range mutatorFields {
		if !field.get(&m).IsIdentity() {
			return false
		}
	}
	return true
}

// String is the canonical form, "key=factor" pairs in ascending key order
// joined by commas, e.g. "buildCost=0.5,buildSpeed=2,smartMakers=on"; ""
// when zero. It is the value reports print and the catalog identity hashes.
func (m Mutators) String() string {
	parts := make([]string, 0, len(mutatorFieldsByKey))
	for _, field := range mutatorFieldsByKey {
		if f := *field.get(&m); !f.IsIdentity() {
			parts = append(parts, field.Key+"="+field.Spell(f))
		}
	}
	return strings.Join(parts, ",")
}

// Describe is the player-facing list, one line per active mutator in the
// design table's order, e.g. {"Build speed ×2", "Build cost ×0.5", "Smart
// makers"}: a switch that is on is named alone. It is empty when the set is
// zero.
func (m Mutators) Describe() []string {
	return m.describe(Factor.Label)
}

// DescribeASCII is Describe spelled for the retail fonts, which have no
// multiplication sign: {"Build speed x2", "Build cost x0.5"}.
func (m Mutators) DescribeASCII() []string {
	return m.describe(func(f Factor) string { return "x" + f.String() })
}

func (m Mutators) describe(label func(Factor) string) []string {
	var out []string
	for _, field := range mutatorFields {
		if f := *field.get(&m); !f.IsIdentity() {
			if field.IsSwitch() {
				out = append(out, field.Label)
				continue
			}
			out = append(out, field.Label+" "+label(f))
		}
	}
	return out
}

// mutatorIdentityTag versions the meaning of the transform. It is hashed into
// Digest and so into every mutated catalog identity, and it must change
// whenever any mutator's transform changes meaning, so that a record written
// under the old transform can never be taken for the new one. Tag 2: Build
// speed divides buildtime instead of multiplying workertime, and the Health,
// Damage, Sight and Radar mutators exist. Adding Blast size (areaOfEffect),
// and later Income, Salvage, Fire rate and Unit speed, did not change the
// meaning of any existing set string, since no string written before them
// names their keys, so the tag stayed 2.
// Tag 3: Sight above identity extends the per-battle visibility inputs.
// Builder speed, added under tag 3, likewise left it: it has a key of its own
// that no earlier string names, and with it absent every other transform —
// Unit speed's motion fields included — is unchanged. Smart makers left it for
// the same reason: its key is new and it changes no other transform.
const mutatorIdentityTag = "mutators/3"

// Digest is a stable identity of the set: a hash of mutatorIdentityTag and the
// canonical String. Field order cannot move it, because String walks keys in
// sorted order.
func (m Mutators) Digest() string {
	return HashDefinition([]byte(mutatorIdentityTag + "\n" + m.String() + "\n"))
}

// validate rejects a factor that is neither the identity nor on its key's
// step list.
func (m Mutators) validate() error {
	for _, field := range mutatorFieldsByKey {
		if f := *field.get(&m); !f.validIn(field.steps()) {
			return fmt.Errorf("content: mutator %s factor %d/%d is not one of %s", field.Key, f.Num, f.Den, stepSpellings(field.steps()))
		}
	}
	return nil
}

// Store maxima of the mutated fields (docs/DESIGN_MODS_MUTATORS.md §6.4). A
// mutator never introduces a wrap, measured at the narrowest store the value
// reaches: for most fields that is the definition's own store, but hit points
// and damage are carried downstream in signed 16-bit words, so those two cap
// at 32,767 whatever their definition store holds.
const (
	// buildtime is a 32-bit store [02 R-KEYS-01 §5].
	mutatorBuildTimeLimit = math.MaxInt32
	// maxdamage is a 32-bit definition store [02 R-KEYS-01 §5], but it seeds
	// the unit's live health, a signed 16-bit word [04 §4.4][04 §5.1], which
	// cannot hold more than 32,767.
	mutatorMaxDamageLimit = math.MaxInt16
	// Feature metal and energy are masked to sixteen bits before their
	// single-precision store [02 R-KEYS-01 §5][05 R-FEAT-01 §1].
	mutatorFeaturePoolLimit = math.MaxUint16
	// buildcostmetal and buildcostenergy are read as 32-bit integers and
	// stored as single floats [02 R-KEYS-01 §5]. The design names no separate
	// maximum for them; saturating at the authored integer's own ceiling keeps
	// a scaled cost inside the domain authored content can reach.
	mutatorUnitCostLimit = math.MaxInt32
	// A weapon's default damage is an unsigned 16-bit definition store and
	// each per-unit override a signed 32-bit one [06 R-DMG-01 §1]
	// [02 R-KEYS-01 §5], but every hit is packed into a signed 16-bit packet
	// amount modulo 65,536 [06 §9.2], which cannot carry more than 32,767.
	mutatorDefaultDamageLimit  = math.MaxInt16
	mutatorOverrideDamageLimit = math.MaxInt16
	// sightdistance and the four radar, sonar and jamming distances are
	// signed 16-bit stores [02 R-KEYS-01 §5].
	mutatorDistanceLimit = math.MaxInt16
	// areaofeffect is an unsigned 16-bit store, and every blast reader
	// zero-extends it [02 R-KEYS-01 §5][06 §9.3].
	mutatorAreaLimit = math.MaxUint16
	// reloadtime is an unsigned 16-bit store that every reader zero-extends
	// [02 "Weapon record"][06 §4.2][06 R-WPN-05 §2].
	mutatorReloadLimit = math.MaxUint16
	// maxvelocity, acceleration, brakerate and the two movement-rate
	// thresholds are 32-bit 16.16 stores [02 R-KEYS-01 §5].
	mutatorMotionLimit = math.MaxInt32
	// turnrate is an unsigned 16-bit store that every reader zero-extends
	// [02 R-KEYS-01 §5][04 R-MOV-01 §1].
	mutatorTurnRateLimit = math.MaxUint16
	// A projectile that meets a unit with areaofeffect at or below 16 damages
	// that unit alone and skips the area sweep [06 §9.1]; Modern's reliable
	// direct-fire class uses the same bound. The Area of effect mutator keeps
	// every weapon on its own side of it.
	mutatorDirectHitArea = 16
)

// ApplyMutators transforms this catalog in place (docs/DESIGN_MODS_MUTATORS.md
// §6.4–§6.6). Like RestrictToCreatable, it is applied only to a per-battle
// clone, never to a shared compiled catalog, and only once per clone. Every
// mutator starts from the stored value: v <= 0 is left alone, since zero is
// the absent value and a negative value is a malformed input with its own
// retail arms [05 R-WORK-01 §11]; v > 0 becomes max(1, (v·Num + ⌊Den/2⌋) div
// Den) in 64-bit integers, saturating at the narrowest store the value
// reaches — the definition's own store, or for hit points and damage the
// signed 16-bit words they are carried in — so a mutator never introduces a
// wrap. Products formed later are not guarded: the retail arithmetic that
// reads a mutated value stays retail.
//
// Build speed k scales every unit's BuildTime by the inverse factor:
// v > 0 becomes max(1, (v·Den + ⌊Num/2⌋) div Num), saturating at the 32-bit
// store. P1 revised: inverse buildtime, because the worker quantum is
// workertime/30 in integers [05 R-WORK-01 §1]. What follows from buildtime:
//
//   - Construction progress is worker/buildtime per visit and spend is cost
//     times progress, so a build takes 1/k of the visits at the same total
//     cost and the same total health gain [05 R-WORK-01 §1].
//   - Resurrection waits buildtime·0.3/quantum ticks, so it is about 1/k as
//     long [05 R-WORK-01 §7].
//   - The abandoned-frame reverse arm steps by 11·buildtime/buildcostenergy
//     divided by buildtime, so decay does not depend on buildtime
//     [05 R-WORK-01 §9], apart from rounding. Its 11·buildtime product is a
//     32-bit integer.
//   - Retail repair is unaffected: both of its terms divide by buildtime but
//     are clamped to exactly one whenever positive, so every visit heals one
//     point for one energy [05 R-WORK-01 §3]. Where the Community repair rate
//     is enabled, its heal divides by buildtime, so repair and self-heal there
//     heal about k times as much per visit, and its per-visit energy divides
//     by buildtime too, with a minimum of one (community-patch-engine.md
//     CP-DMG-4).
//   - Unit reclaim and capture read no buildtime and are unaffected
//     [05 R-WORK-01 §4][05 R-WORK-01 §6].
//
// Build cost scales every unit's BuildCostMetal and BuildCostEnergy, and the
// Metal and Energy of every feature definition reachable from any unit's
// Corpse through the FeatureDead successor chain, each definition once; a
// feature no unit's corpse chain reaches is left alone (P2).
//
// Health scales every unit's MaxDamage, saturating at 32,767: the unit's live
// health is a signed 16-bit word seeded from maxdamage [04 §4.4][04 §5.1],
// so a larger maxdamage would seed a health the word cannot hold and the
// first hit would wrap it. A unit already near that ceiling gains less than
// k: the stock Krogoth (29,918) reaches it from ×1.5.
//
//   - Retail repair and self-heal restore exactly one point per accepted
//     visit [05 R-WORK-01 §3], so repairing to full takes about k times as
//     many visits and, at one energy per visit, about k times the energy.
//   - Construction's health gain telescopes to maxdamage, so a finished
//     frame is at the scaled full health [05 R-WORK-01 §1].
//   - The unit-reclaim pulse is proportional to the target's maxdamage, so
//     reclaim takes about as long as before; its 32-bit product grows k-fold
//     [05 R-WORK-01 §4].
//   - The capture timer at full health does not depend on maxdamage
//     [05 R-WORK-01 §6].
//
// Damage scales every weapon record's DamageDefault and each DAMAGE override,
// death, self-destruct, burn and meteor weapons included, walking overrides
// through DamageKeysSorted (I1). The default is scaled from its stored
// unsigned 16-bit value [06 R-DMG-01 §1]; the default and the overrides all
// saturate at 32,767, because every hit is packed into a signed 16-bit packet
// amount modulo 65,536 [06 §9.2] and a larger value would wrap there. Feature
// hit points are not scaled, so features fall faster. Health and Damage at one
// factor roughly cancel unit against unit, apart from truncation and the caps.
//
//   - The per-recipient amount skips the armored damage modifier once it
//     reaches 30,000 [06 §9.2]. The stock disintegrators (the D-gun) author
//     30,000 and already skip it at ×1; under any factor above one they cap at
//     32,767, so the mutator never wraps their hit and they keep killing.
//   - Attacker veterancy multiplies the amount by up to 1.3 before it is
//     packed [06 §9.2], so a veteran's capped hit can still pass 32,767
//     downstream, as the stock 30,000 D-gun already can from its second
//     veterancy tier. That product is retail arithmetic and is not guarded.
//
// Sight scales every unit's SightDistance, saturating at the signed 16-bit
// store. Above identity it also extends the per-battle raster inputs so the
// original table caps do not swallow the increase. Original reachable data
// and the raster arithmetic remain unchanged [03 §3.2][03 R-COMP-02 §1]; the
// generated content is Nanolathe policy (docs/DESIGN_MODS_MUTATORS.md §6.5).
// The fire-at-will opportunity scan, the standing-move leash and the patrol
// and VTOL work scans also read sightdistance and widen with it at every
// factor [04 R-STANCE-01 §3][04 R-STANCE-01 §4][04 R-ORD-01 §4]
// [04 R-ORD-01 §7].
//
// Radar scales every unit's RadarDistance, SonarDistance, RadarDistanceJam and
// SonarDistanceJam, saturating at the signed 16-bit store. All four are plain
// radii of the one radius visitor [03 R-VIS-01 §5], so scaling them together
// keeps detection and jamming in proportion. The emitter's radar elevation
// bonus is not scaled, and the search stays bounded by the larger of the
// scaled radar and sonar distances [03 R-VIS-01 §4]. mincloakdistance is not a
// radar distance and is left alone.
//
// Area of effect scales every weapon record's areaofeffect above 16 from its
// stored unsigned 16-bit value, with a floor of 17 and saturation at 65,535,
// death, self-destruct, burn and meteor weapons included. At or below 16 the
// value marks a direct-hit weapon, whose hit on a unit skips the area sweep
// [06 §9.1], and it is left alone, so a laser never gains splash and a small
// splash weapon never loses it at ×0.25. The blast radius is the stored area
// halved [06 §9.3], so every blast reaches k times as far.
//
//   - The falloff reads distance over radius [06 §9.3], so a recipient at the
//     same fraction of the scaled radius takes the same share of the damage.
//   - The broad phase walks (radius/16)+1 cells each way [06 §9.3], so a blast
//     visits about k² times the cells. The stock largest, 950, is 3,800 at ×4.
//   - The area sweep remembers at most twenty units, and a unit met again once
//     the memory is full is processed again [06 §9.3]. A wider blast meets more
//     units, so a large unit spanning several cells can be hit more than once
//     sooner.
//   - An interceptor's catch test compares against its unhalved area squared,
//     a signed 32-bit product [06 R-WPN-05 §10], so interceptors catch within
//     k times the distance. The square wraps from 46,341; the stock
//     interceptors author 96.
//   - The kamikaze pulse ring and the area-of-effect range ring read the same
//     field and widen with it [04 R-SPEC-01 §1][07 R-P0-11 §3]. Explosion art is
//     not scaled.
//
// Income scales each unit's production in the single-precision store retail
// keeps these keys in [02 R-KEYS-01 §5][05 R-PROD-01 §1] (scaleStoredFloat):
// metalmake, extractsmetal, windgenerator, tidalgenerator, the magnitude of a
// negative energyuse, and energymake's surplus over a positive energyuse
// (scaleSurplus). Every settlement contribution is the field times something
// the mutator leaves alone, so output scales by k [05 R-ECO-01 §2].
//
//   - A negative energyuse is not a malformed value here but the production
//     arm the stock solar collectors author (−20): its negation is added to
//     production [05 R-ECO-01 §2]. Scaling the magnitude keeps the sign and the
//     arm.
//   - 126 stock units author energymake equal to energyuse, paying for their
//     own radar, jammer or movement upkeep. Scaling energymake alone would turn
//     them into net drains below ×1 and into generators above it, so only the
//     surplus scales: a fusion plant (no upkeep) makes exactly k times as much,
//     and a self-powered radar stays neutral while its upkeep is charged. The
//     upkeep is charged only while a building is activated or a mobile unit is
//     activated or moving, and energymake is paid whenever the unit is
//     complete [05 R-ECO-01 §2], so a switched-off radar or a parked mobile
//     unit keeps its unscaled make at every factor.
//   - Positive energyuse, makesmetal, storage and costs are unchanged. A metal
//     maker keeps its authored conversion [05 R-PROD-01 §5]; more energy runs
//     more makers. Storage fills sooner, and production beyond it is wasted at
//     the settlement clamp [05 R-ECO-01 §6].
//   - The computer players' difficulty discount multiplies each contribution
//     and so composes with Income [05 R-ECO-01 §3]. An extractor samples its
//     rate at creation [05 R-PROD-01 §6], and a save carries that rate.
//
// Salvage scales the Metal and Energy of every feature definition that is not
// indestructible, with the Build cost limits; a corpse-chain feature takes the
// product of Build cost and Salvage, rounded once (times). Map features
// resolve to these definitions when the terrain loads, after battle entry has
// applied the mutators.
//
//   - The deposit pass writes the low byte of an indestructible definition's
//     metal into the extraction grid [05 R-FEAT-01 §7]; scaling it would wrap
//     that byte (250×2 is 244), so indestructible definitions are left alone.
//     No stock indestructible definition is reclaimable.
//   - Feature reclaim counts down trunc(15 + (energy+metal)/2) work at a fixed
//     rate and pays the whole pool on the removing visit
//     [05 R-WORK-01 §5], so a builder earns at about the same rate for about k
//     times as long.
//   - The pools are read at payout, by repair patrol's reclaim scan and its
//     inclusive fits-under-storage test [05 R-FEAT-01 §6][04 R-ORD-01 §4], so
//     a larger pool is passed over sooner when storage is nearly full, and by
//     the HUD footer [07 R-HUD-03 §3]. Resurrection reads no pool
//     [05 R-WORK-01 §7].
//
// Fire rate k scales every weapon's ReloadTime by the inverse factor, from its
// stored unsigned 16-bit value, saturating at that store; zero stays zero,
// and the minimum of one means the mutator never creates a free reload
// [06 R-WPN-05 §2]. The slot fires when its countdown reaches zero
// [06 §4.2], so a weapon fires about k times as often, up to once per tick per
// slot; short stock reloads round unevenly at ×4 (5 ticks becomes 1).
//
//   - energypershot and metalpershot are billed per shot [06 §4.2], so a
//     weapon's drain grows k-fold at the same cost per shot.
//   - A stockpile round advances five progress per visit up to reloadtime and
//     bills cost in proportion to progress [06 §11.1], so rounds finish in
//     about 1/k of the time at the same total cost, demanding k times as much
//     per visit.
//   - burstrate spaces the shots within one burst on its own schedule
//     [06 §4.3] and is left alone; a burst longer than the scaled reload
//     overlaps the next.
//   - The veteran reload is a percentage of reloadtime in integers [06 §4.2],
//     so the bonus flattens at the smallest reloads. SetMaxReloadTime hands
//     scripts the scaled value [06 R-WPN-05 §3].
//
// Unit speed k scales every unit's MaxVelocity, TurnRate and the MoveRate1
// and MoveRate2 thresholds by k, and Acceleration by k² (the exact product
// k·k, rounded once). BrakeRate scales by k² for a unit that does not fly and
// by k for one that does; the mover chooses the ground or flight integrator
// on the definition's canfly bit alone [04 R-MOV-01 §1]. Motion then plays k
// times as fast along paths of the same shape:
//
//   - The ground mover accelerates only while the lookahead target is beyond
//     twice its turning distance |err|·speed/turnrate and the point two ahead
//     is beyond its braking distance speed²/(2·brakerate)
//     [04 R-MOV-01 §4]. Both distances are unchanged, as is the distance to
//     reach top speed, speed²/(2·acceleration); the time to reach it is 1/k.
//   - The flight integrator compares horizontal speed with brakerate itself
//     and turns the excess toward the heading [04 §10.1], so brakerate is a
//     speed there and scales by k. The loss term acceleration/maxvelocity of
//     its decay grows k-fold, and so does its approach speed sqrt(2·a·d), so
//     the terminal speed and every approach are k times as fast.
//     The decay reaches zero when acceleration/maxvelocity reaches 1/k; the
//     stock largest ratio is 0.042, so ×4 keeps it above 0.83.
//   - The movement-rate tiers compare speed with the two thresholds
//     [04 R-MOV-01 §6], so a unit changes tier at the same fraction of its
//     speed. No COB port can set a speed [04 R-COB-03 §1].
//   - Fixed distances do not scale: the waypoint capture radius, the
//     lookahead and the air service radii. A unit whose step outgrows the
//     capture radius can pass a waypoint and loop back to it, and the
//     collision validator tests only the proposed rectangle
//     [04 R-COLL-01 §1]. Tick cadences (repath, air order deadlines) and every
//     weapon's speed and turn rate are unchanged, so fast units outrun more
//     fire. cruisealt and the bank and pitch gains are left alone.
//
// Builder speed k, from its own step list, applies to every definition that
// authors builder — commanders, mobile builders, factories and nano towers —
// and to no other (scaleMotion). A builder the allocator gives a mover gains
// turnrate ×k, and a ground one acceleration and brakerate ×k as well, so it
// reaches its unchanged top speed and turns in 1/k of the time; a flying one
// gains turnrate only, since the flight integrator turns its acceleration and
// brakerate into speeds [04 §10.1]. With Unit speed each field is scaled once
// by the exact product, formed in 64 bits. Every builder records k in
// BuilderSpeed. This is the first mutated value that engine code reads rather
// than retail arithmetic: the session hands it to the builder's script VM,
// which runs the build-animation functions k× faster, and a mobile ground
// builder starts placement once retail's reach test passes instead of walking
// to the footprint border (docs/DESIGN_MODS_MUTATORS.md "Builder speed").
//
// Smart makers is a switch, not a scale: it changes no authored field, and
// every metal maker a player can switch (SmartMakerCandidate) records
// SmartMaker. The economy reads that mark after a human player's settlement
// pass to switch the player's makers with its energy
// (docs/DESIGN_MODS_MUTATORS.md "Smart makers").
//
// The only compile-time values derived from a mutated field are the moverate1
// and moverate2 defaults, twice maxvelocity; Unit speed scales them by the
// same k, so they stay twice the scaled velocity to within one 16.16 unit.
// The unit, weapon and feature compilers otherwise assign the mutated fields
// and fold them into per-definition hashes, which remain identities of the
// authored records (§6.6). Sight above identity additionally extends the LOS
// tables and sight shapes under §6.5. With a zero set nothing changes, Hash
// included. Otherwise Hash
// becomes a hash of the base Hash and Digest — that is, of the identity tag,
// the base Hash and String. A set with a factor off the step list, or a unit
// cost that is not an integral 32-bit store, is refused before anything is
// written.
func (c *Catalog) ApplyMutators(m Mutators) error {
	if err := m.validate(); err != nil {
		return err
	}
	if c == nil || m.IsZero() {
		return nil
	}
	units := c.mutatorUnits()
	if !m.BuildCost.IsIdentity() {
		for _, u := range units {
			if err := checkIntegralCost(u, "buildcostmetal", u.BuildCostMetal); err != nil {
				return err
			}
			if err := checkIntegralCost(u, "buildcostenergy", u.BuildCostEnergy); err != nil {
				return err
			}
		}
	}
	for _, u := range units {
		if !m.BuildSpeed.IsIdentity() {
			u.BuildTime = int32(m.BuildSpeed.inverse().scale(int64(u.BuildTime), mutatorBuildTimeLimit))
		}
		if !m.BuildCost.IsIdentity() {
			u.BuildCostMetal = m.BuildCost.scaleCost(u.BuildCostMetal)
			u.BuildCostEnergy = m.BuildCost.scaleCost(u.BuildCostEnergy)
		}
		if !m.Health.IsIdentity() {
			u.MaxDamage = int32(m.Health.scale(int64(u.MaxDamage), mutatorMaxDamageLimit))
		}
		if !m.Sight.IsIdentity() {
			u.SightDistance = int32(m.Sight.scale(int64(u.SightDistance), mutatorDistanceLimit))
		}
		if !m.Radar.IsIdentity() {
			u.RadarDistance = int32(m.Radar.scale(int64(u.RadarDistance), mutatorDistanceLimit))
			u.SonarDistance = int32(m.Radar.scale(int64(u.SonarDistance), mutatorDistanceLimit))
			u.RadarDistanceJam = int32(m.Radar.scale(int64(u.RadarDistanceJam), mutatorDistanceLimit))
			u.SonarDistanceJam = int32(m.Radar.scale(int64(u.SonarDistanceJam), mutatorDistanceLimit))
		}
		if !m.UnitSpeed.IsIdentity() || (u.Builder && !m.BuilderSpeed.IsIdentity()) {
			scaleMotion(u, m.UnitSpeed, m.BuilderSpeed)
		}
		if !m.SmartMakers.IsIdentity() && SmartMakerCandidate(u) {
			u.SmartMaker = true
		}
	}
	if m.Sight.Num > m.Sight.Den {
		c.extendMutatorSight(units)
	}
	if !m.Income.IsIdentity() {
		for _, u := range units {
			// The surplus reads the authored upkeep, which Income leaves alone.
			u.EnergyMake = m.Income.scaleSurplus(u.EnergyMake, u.EnergyUse)
			if u.EnergyUse < 0 {
				u.EnergyUse = -m.Income.scaleStoredFloat(-u.EnergyUse)
			}
			u.MetalMake = m.Income.scaleStoredFloat(u.MetalMake)
			u.ExtractsMetal = m.Income.scaleStoredFloat(u.ExtractsMetal)
			u.WindGenerator = m.Income.scaleStoredFloat(u.WindGenerator)
			u.TidalGenerator = m.Income.scaleStoredFloat(u.TidalGenerator)
		}
	}
	if !m.BuildCost.IsIdentity() || !m.Salvage.IsIdentity() {
		var chain map[*FeatureDef]bool
		if !m.BuildCost.IsIdentity() {
			chain = make(map[*FeatureDef]bool)
			for _, f := range c.corpseChainFeatures(units) {
				chain[f] = true
			}
		}
		for _, f := range c.mutatorFeatures() {
			k := Factor{1, 1}
			if chain[f] {
				k = m.BuildCost
			}
			if !f.Indestructible {
				k = k.times(m.Salvage)
			}
			if k.IsIdentity() {
				continue
			}
			f.Metal = int32(k.scale(int64(f.Metal), mutatorFeaturePoolLimit))
			f.Energy = int32(k.scale(int64(f.Energy), mutatorFeaturePoolLimit))
		}
	}
	if !m.Damage.IsIdentity() {
		for _, w := range c.mutatorWeapons() {
			// The default starts from its stored unsigned 16-bit value, which
			// is what every damage reader sees [06 R-DMG-01 §1].
			if stored := int64(uint16(w.DamageDefault)); stored > 0 {
				w.DamageDefault = int32(m.Damage.scale(stored, mutatorDefaultDamageLimit))
			}
			seen := make(map[string]bool, len(w.Damage))
			for _, key := range w.DamageKeysSorted() {
				if seen[key] {
					continue
				}
				seen[key] = true
				w.Damage[key] = int32(m.Damage.scale(int64(w.Damage[key]), mutatorOverrideDamageLimit))
			}
		}
	}
	if !m.AreaOfEffect.IsIdentity() {
		for _, w := range c.mutatorWeapons() {
			w.AreaOfEffect = m.AreaOfEffect.scaleArea(w.AreaOfEffect)
		}
	}
	if !m.FireRate.IsIdentity() {
		for _, w := range c.mutatorWeapons() {
			// The reload starts from its stored unsigned 16-bit value, which is
			// what every reader sees [06 §4.2].
			if stored := int64(uint16(w.ReloadTime)); stored > 0 {
				w.ReloadTime = int32(m.FireRate.inverse().scale(stored, mutatorReloadLimit))
			}
		}
	}
	c.Hash = MutatedHash(c.Hash, m)
	return nil
}

// MutatedHash is the identity ApplyMutators gives a catalog whose identity is
// base: base itself for the zero set, otherwise the base identity composed
// with the set's digest, which hashes the identity tag and the canonical
// String (§6.6). A save load compares a save's recorded identity with the one
// its recorded set gives the restored content, when Smart makers runs as the
// player selects rather than as recorded (docs/DESIGN_MODS_MUTATORS.md §7.3).
func MutatedHash(base string, m Mutators) string {
	if m.IsZero() {
		return base
	}
	return HashDefinition([]byte("catalog+mutators\n" + base + "\n" + m.Digest() + "\n"))
}

// scaleMotion applies Unit speed k_u and Builder speed k_b to one unit's
// motion fields, each field scaled once by the exact product and saturated
// (docs/DESIGN_MODS_MUTATORS.md §6.4, "Builder speed"):
//
//   - Unit speed: maxvelocity and the two movement-rate thresholds by k_u,
//     turnrate by k_u, acceleration by k_u², brakerate by k_u² on the ground
//     and by k_u in the air, where the flight integrator reads it as a speed
//     [04 R-MOV-01 §1][04 §10.1].
//   - Builder speed applies only to a definition authoring builder, and its
//     agility only to one the allocator gives a mover, bmcode 1
//     [04 R-FAC-02 §5]: turnrate by k_b, and for a ground builder
//     acceleration and brakerate by k_b too. Top speed is untouched. A flying
//     builder's acceleration and brakerate feed the flight integrator's speed
//     terms [04 §10.1], so scaling them would change its top speed; it gains
//     turnrate only.
//
// Every builder, mobile or not, records k_b in BuilderSpeed for its script VM
// and its mobile-build approach.
func scaleMotion(u *UnitDef, unitSpeed, builderSpeed Factor) {
	ku := unitSpeed.ratio()
	kb := ratio{1, 1}
	if u.Builder && !builderSpeed.IsIdentity() {
		u.BuilderSpeed = builderSpeed
		if u.BMCode == 1 {
			kb = builderSpeed.ratio()
		}
	}
	squared := ku.times(ku)
	turn, accel, brake := ku.times(kb), squared, squared
	if u.CanFly {
		brake = ku
	} else {
		accel, brake = squared.times(kb), squared.times(kb)
	}
	u.MaxVelocity = int32(ku.scale(int64(u.MaxVelocity), mutatorMotionLimit))
	u.Acceleration = int32(accel.scale(int64(u.Acceleration), mutatorMotionLimit))
	u.BrakeRate = int32(brake.scale(int64(u.BrakeRate), mutatorMotionLimit))
	u.MoveRate1 = int32(ku.scale(int64(u.MoveRate1), mutatorMotionLimit))
	u.MoveRate2 = int32(ku.scale(int64(u.MoveRate2), mutatorMotionLimit))
	if stored := int64(uint16(u.TurnRate)); stored > 0 {
		u.TurnRate = int32(turn.scale(stored, mutatorTurnRateLimit))
	}
}

// SmartMakerCandidate reports whether Smart makers manages a definition: a
// metal maker the settlement runs through its maker arm and a player can
// switch. That is a building (bmcode zero) whose makesmetal byte is non-zero
// with no extractsmetal ahead of it in the generator chain, whose upkeep is a
// positive energyuse, and which authors onoffable, the handlers' own gate
// [05 R-ECO-01 §2][04 R-ORD-01 §2]. The stock metal makers, Moho and floating
// makers included, all qualify, and no other stock definition does
// (docs/DESIGN_MODS_MUTATORS.md "Smart makers").
func SmartMakerCandidate(u *UnitDef) bool {
	return u != nil && u.BMCode == 0 && uint8(u.MakesMetal) != 0 && !(u.ExtractsMetal > 0) &&
		float32(u.EnergyUse) > 0 && u.OnOffable
}

// mutatorWeapons returns every weapon record once, in sorted catalog-name
// order (I1). Every unit link, including death and self-destruct weapons,
// points into this map [02 §5 R-CONTENT-02].
func (c *Catalog) mutatorWeapons() []*WeaponDef {
	keys := make([]string, 0, len(c.Weapons))
	for key := range c.Weapons {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := make(map[*WeaponDef]bool, len(keys))
	out := make([]*WeaponDef, 0, len(keys))
	for _, key := range keys {
		if w := c.Weapons[key]; w != nil && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// times is the product of two factors, each normalized from the zero value to
// 1/1 first. It is exact: every step's numerator and denominator is at most
// four, so the product's are at most sixteen. The product is off the step
// list and is never stored in a Mutators; it only composes two mutators that
// scale the same field, so the field is rounded once.
func (f Factor) times(g Factor) Factor {
	if f.IsIdentity() {
		f = Factor{1, 1}
	}
	if g.IsIdentity() {
		g = Factor{1, 1}
	}
	return Factor{Num: f.Num * g.Num, Den: f.Den * g.Den}
}

// float is the factor as a float64. Every step is a whole number of quarters,
// so the value is exact.
func (f Factor) float() float64 {
	if f.IsIdentity() {
		return 1
	}
	return float64(f.Num) / float64(f.Den)
}

// scaleStoredFloat scales a floating unit value in the single-precision store
// retail keeps it in [02 R-KEYS-01 §5][05 R-PROD-01 §1]: the stored single
// times k, rounded once to single, and kept in the definition's float64 field
// the way the compiler keeps an authored value (INVARIANTS I2). A single's
// 24-bit significand times a quarter step fits a double exactly, so the one
// rounding is the narrowing, and every consumer that narrows the field sees
// the same value as one that reads it whole. A value that is not positive,
// NaN included, is returned untouched; a product beyond the single range
// saturates at the largest single, so the mutator never creates an infinity.
func (f Factor) scaleStoredFloat(v float64) float64 {
	if !(v > 0) || f.IsIdentity() {
		return v
	}
	return narrowScaled(float64(float32(v)) * f.float())
}

// scaleSurplus scales what energymake produces beyond the same unit's positive
// energyuse, leaving the part that pays for its own upkeep alone: with s_m and
// s_e the stored singles, s_m > s_e becomes s_e + k·(s_m − s_e), rounded once
// to single. The difference and product are formed in doubles, and the
// explicit conversion of the product keeps it from being fused into the sum,
// so the result is the same on every platform. A unit whose energymake does
// not exceed its upkeep — the self-powered radars, jammers and mobile units
// that author the two equal — is returned untouched, as is a value that is not
// positive; with no positive upkeep the rule is scaleStoredFloat's.
func (f Factor) scaleSurplus(produced, upkeep float64) float64 {
	if !(produced > 0) || f.IsIdentity() {
		return produced
	}
	storedMake, storedUse := float64(float32(produced)), 0.0
	if upkeep > 0 {
		storedUse = float64(float32(upkeep))
	}
	if !(storedMake > storedUse) {
		return produced
	}
	surplus := float64((storedMake - storedUse) * f.float())
	return narrowScaled(storedUse + surplus)
}

// narrowScaled rounds a scaled double to the single store, saturating at the
// largest finite single.
func narrowScaled(v float64) float64 {
	if v > math.MaxFloat32 {
		return math.MaxFloat32
	}
	return float64(float32(v))
}

// scaleArea scales a stored areaofeffect, keeping it on its side of the
// direct-hit bound: 16 or less is returned untouched, and a larger value never
// scales below 17.
func (f Factor) scaleArea(area int32) int32 {
	stored := int64(uint16(area))
	if stored <= mutatorDirectHitArea {
		return area
	}
	return int32(max(mutatorDirectHitArea+1, f.scale(stored, mutatorAreaLimit)))
}

// scaleCost multiplies a unit cost as the integer it was authored as and
// stores the result as a single float, which is exact below 2^24
// (docs/DESIGN_MODS_MUTATORS.md §6.4). A cost that is not positive is
// returned untouched, bit for bit. checkIntegralCost has already bounded a
// positive cost to at most 2^31, where the defined signed-64 conversion is the
// raw one; it is used so no catalog value rests on Go's processor-dependent
// out-of-range conversion (I2).
func (f Factor) scaleCost(v float32) float32 {
	if !(v > 0) {
		return v
	}
	return float32(f.scale(numeric.TruncateFloat64ToInt64(float64(v)), mutatorUnitCostLimit))
}

// checkIntegralCost refuses a positive cost that the unit compiler could not
// have stored: the compiler converts a 32-bit integer, so every positive cost
// it produces is integral and at most 2^31.
func checkIntegralCost(u *UnitDef, key string, v float32) error {
	if !(v > 0) {
		return nil
	}
	if v > float32(1<<31) || float32(numeric.TruncateFloat64ToInt64(float64(v))) != v {
		return fmt.Errorf("content: mutator build cost: unit %q %s %v is not an integral 32-bit store", u.UnitName, key, v)
	}
	return nil
}

// mutatorUnits returns every unit definition once: the retained records in ID
// order, then any name-index entry the records do not hold, in sorted key
// order (I1).
func (c *Catalog) mutatorUnits() []*UnitDef {
	records := c.unitRecordView()
	seen := make(map[*UnitDef]bool, len(records))
	out := make([]*UnitDef, 0, len(records))
	for _, u := range records {
		if u != nil && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, key := range c.SortedUnitKeys() {
		if u := c.Units[key]; u != nil && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// mutatorFeatures returns every feature definition once, in sorted
// catalog-name order (I1). Map-placed features resolve into this map when the
// terrain loads, after battle entry has applied the mutators, so wrecks, rocks
// and trees are all here.
func (c *Catalog) mutatorFeatures() []*FeatureDef {
	keys := make([]string, 0, len(c.Features))
	for key := range c.Features {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := make(map[*FeatureDef]bool, len(keys))
	out := make([]*FeatureDef, 0, len(keys))
	for _, key := range keys {
		if f := c.Features[key]; f != nil && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// corpseChainFeatures returns each feature definition reachable from a unit's
// corpse through FeatureDead successors, once. The corpse name resolves the
// way the death path resolves it — the catalog's feature map under the
// canonical key [06 §12.1] — and each successor is looked up by name in this
// catalog's own map, so a stale successor pointer can never lead outside the
// clone. The seen set also ends a cyclic chain.
func (c *Catalog) corpseChainFeatures(units []*UnitDef) []*FeatureDef {
	if len(c.Features) == 0 {
		return nil
	}
	seen := make(map[*FeatureDef]bool)
	var out []*FeatureDef
	for _, u := range units {
		key := CanonicalKey(u.Corpse)
		if key == "" {
			continue
		}
		for f := c.Features[key]; f != nil && !seen[f]; {
			seen[f] = true
			out = append(out, f)
			next := CanonicalKey(f.FeatureDead)
			if next == "" {
				break
			}
			f = c.Features[next]
		}
	}
	return out
}
