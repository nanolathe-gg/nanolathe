// Mutators: global multipliers applied to a per-battle catalog clone
// (docs/DESIGN_MODS_MUTATORS.md §6). A mutator is a transform of compiled
// content, not a gameplay rule: it adds no seam, draws no random number, keeps
// no per-tick state and is never consulted inside a tick, so it applies in
// every gameplay mode, Strict 3.1 included (INVARIANTS I11). Strict 3.1 with
// no mutators is the retail baseline.

package content

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Factor is an exact rational multiplier from the fixed step list
// (docs/DESIGN_MODS_MUTATORS.md §6.4). The zero value is the identity.
type Factor struct{ Num, Den uint8 }

// MutatorSteps is the closed step list, in ascending order. Settings, flags
// and sidecars refuse any other value, which keeps the test matrix finite and
// every product exact (docs/DESIGN_MODS_MUTATORS.md §6.4, P4).
var MutatorSteps = []Factor{{1, 4}, {1, 2}, {3, 4}, {1, 1}, {3, 2}, {2, 1}, {3, 1}, {4, 1}}

// ParseFactor reads one step in its canonical decimal spelling: "0.25",
// "0.5", "0.75", "1", "1.5", "2", "3" or "4". Any other text, including an
// equal value spelled differently ("1/2", ".5", "2.0"), is an error.
func ParseFactor(s string) (Factor, error) {
	text := strings.TrimSpace(s)
	for _, step := range MutatorSteps {
		if step.String() == text {
			return step, nil
		}
	}
	return Factor{}, fmt.Errorf("content: mutator factor %q is not one of %s", s, stepSpellings())
}

func stepSpellings() string {
	names := make([]string, len(MutatorSteps))
	for i, step := range MutatorSteps {
		names[i] = step.String()
	}
	return strings.Join(names, ", ")
}

// IsIdentity reports whether the factor changes nothing: the zero value or 1/1.
func (f Factor) IsIdentity() bool { return f == (Factor{}) || f == (Factor{1, 1}) }

// String is the canonical spelling ParseFactor reads, a decimal because most
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

// Next is the following step, clamped at the list's last step. A factor that
// is not on the list moves to the first step above its value; the zero value
// cycles as 1/1.
func (f Factor) Next() Factor {
	if f.IsIdentity() || f.Den == 0 {
		f = Factor{1, 1}
	}
	for _, step := range MutatorSteps {
		// step > f, compared by cross-multiplication.
		if uint16(step.Num)*uint16(f.Den) > uint16(f.Num)*uint16(step.Den) {
			return step
		}
	}
	return MutatorSteps[len(MutatorSteps)-1]
}

// Prev is the preceding step, clamped at the list's first step. A factor that
// is not on the list moves to the last step below its value; the zero value
// cycles as 1/1.
func (f Factor) Prev() Factor {
	if f.IsIdentity() || f.Den == 0 {
		f = Factor{1, 1}
	}
	for i := len(MutatorSteps) - 1; i >= 0; i-- {
		// step < f, compared by cross-multiplication.
		if step := MutatorSteps[i]; uint16(step.Num)*uint16(f.Den) < uint16(f.Num)*uint16(step.Den) {
			return step
		}
	}
	return MutatorSteps[0]
}

// inverse is 1/f. Every step's inverse is itself exact: the identity maps to
// the identity and Num/Den to Den/Num.
func (f Factor) inverse() Factor { return Factor{Num: f.Den, Den: f.Num} }

// valid reports whether the factor is the identity or one of MutatorSteps.
func (f Factor) valid() bool {
	if f.IsIdentity() {
		return true
	}
	for _, step := range MutatorSteps {
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
	if v <= 0 || f.IsIdentity() {
		return v
	}
	num, den := int64(f.Num), int64(f.Den)
	r := (v*num + den/2) / den
	if r < 1 {
		r = 1
	}
	if r > limit {
		r = limit
	}
	return r
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
}

// MutatorInfo describes one mutator for a front end, so a screen builds its
// rows from data instead of naming fields. Group is a short heading. Label
// and Description are player-facing and ASCII only, because the retail fonts
// have no multiplication sign.
type MutatorInfo struct {
	Key, Label, Group, Description string
}

// mutatorField is one row of the closed set: its descriptor and its field.
type mutatorField struct {
	MutatorInfo
	get func(*Mutators) *Factor
}

// mutatorFields lists the set in the design table's order, grouped, which is
// the order Describe and MutatorCatalog present
// (docs/DESIGN_MODS_MUTATORS.md §6.5).
var mutatorFields = []mutatorField{
	{MutatorInfo{"buildSpeed", "Build speed", "Economy", "Divides every unit's build time; total cost is unchanged."}, func(m *Mutators) *Factor { return &m.BuildSpeed }},
	{MutatorInfo{"buildCost", "Build cost", "Economy", "Multiplies every unit's metal and energy cost and the value of its wreck."}, func(m *Mutators) *Factor { return &m.BuildCost }},
	{MutatorInfo{"health", "Health", "Combat", "Multiplies every unit's hit points."}, func(m *Mutators) *Factor { return &m.Health }},
	{MutatorInfo{"damage", "Damage", "Combat", "Multiplies the damage of every weapon and explosion."}, func(m *Mutators) *Factor { return &m.Damage }},
	{MutatorInfo{"areaOfEffect", "Blast size", "Combat", "Multiplies every blast radius; direct hits are unchanged."}, func(m *Mutators) *Factor { return &m.AreaOfEffect }},
	{MutatorInfo{"sight", "Sight", "Vision", "Multiplies every unit's line of sight."}, func(m *Mutators) *Factor { return &m.Sight }},
	{MutatorInfo{"radar", "Radar", "Vision", "Multiplies radar, sonar and jamming ranges."}, func(m *Mutators) *Factor { return &m.Radar }},
}

// mutatorFieldsByKey is mutatorFields in ascending key order, the canonical
// order of String and of every key walk (I1).
var mutatorFieldsByKey = func() []mutatorField {
	out := append([]mutatorField(nil), mutatorFields...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}()

// MutatorCatalog returns the descriptors in the design table's order, grouped:
// Economy (build speed, build cost), Combat (health, damage, blast size),
// Vision (sight, radar). The slice is a copy.
func MutatorCatalog() []MutatorInfo {
	out := make([]MutatorInfo, len(mutatorFields))
	for i, field := range mutatorFields {
		out[i] = field.MutatorInfo
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
// spelling. Keys are walked in sorted order, so the first error reported is
// deterministic (I1). An unknown key or a factor off the step list is an error
// naming it. An identity factor parses to the zero value.
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
		factor, err := ParseFactor(values[key])
		if err != nil {
			return Mutators{}, fmt.Errorf("content: mutator %s factor %q is not one of %s", key, values[key], stepSpellings())
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

// SetFactor stores f for key. It refuses an unknown key and a factor off the
// step list; an identity factor is stored as the zero value, so a set edited
// back to 1 compares equal to the zero set.
func (m *Mutators) SetFactor(key string, f Factor) error {
	field, ok := mutatorFieldByKey(key)
	if !ok {
		return fmt.Errorf("content: unknown mutator %q, expected one of %s", key, mutatorKeys())
	}
	if !f.valid() {
		return fmt.Errorf("content: mutator %s factor %d/%d is not one of %s", key, f.Num, f.Den, stepSpellings())
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
			out[field.Key] = f.String()
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
// joined by commas, e.g. "buildCost=0.5,buildSpeed=2"; "" when zero. It is
// the value reports print and the catalog identity hashes.
func (m Mutators) String() string {
	parts := make([]string, 0, len(mutatorFieldsByKey))
	for _, field := range mutatorFieldsByKey {
		if f := *field.get(&m); !f.IsIdentity() {
			parts = append(parts, field.Key+"="+f.String())
		}
	}
	return strings.Join(parts, ",")
}

// Describe is the player-facing list, one line per active mutator in the
// design table's order, e.g. {"Build speed ×2", "Build cost ×0.5"}. It is
// empty when the set is zero.
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
// Damage, Sight and Radar mutators exist. Adding Blast size (areaOfEffect)
// later did not change the meaning of any existing set string, since no
// string written before it names that key, so the tag stayed 2.
const mutatorIdentityTag = "mutators/2"

// Digest is a stable identity of the set: a hash of mutatorIdentityTag and the
// canonical String. Field order cannot move it, because String walks keys in
// sorted order.
func (m Mutators) Digest() string {
	return HashDefinition([]byte(mutatorIdentityTag + "\n" + m.String() + "\n"))
}

// validate rejects a factor that is neither the identity nor on the step list.
func (m Mutators) validate() error {
	for _, field := range mutatorFieldsByKey {
		if f := *field.get(&m); !f.valid() {
			return fmt.Errorf("content: mutator %s factor %d/%d is not one of %s", field.Key, f.Num, f.Den, stepSpellings())
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
// store. The sight-shape index and the terrain-ray group both clamp at their
// table's last entry, so large factors saturate for long-sighted units
// [03 §3.2]. The fire-at-will opportunity scan, the standing-move leash and
// the patrol and VTOL work scans also read sightdistance and widen with it
// [04 R-STANCE-01 §3][04 R-STANCE-01 §4][04 R-ORD-01 §4][04 R-ORD-01 §7].
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
// No compile-time value is derived from any mutated field, so nothing else is
// recomputed: the unit, weapon and feature compilers assign them and only fold
// them into per-definition hashes, which stay identities of the authored
// records (§6.6); the LOS tables and sight shapes are compiled from their own
// files. With a zero set nothing changes, Hash included. Otherwise Hash
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
	}
	if !m.BuildCost.IsIdentity() {
		for _, f := range c.corpseChainFeatures(units) {
			f.Metal = int32(m.BuildCost.scale(int64(f.Metal), mutatorFeaturePoolLimit))
			f.Energy = int32(m.BuildCost.scale(int64(f.Energy), mutatorFeaturePoolLimit))
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
	// The mutated identity composes the base identity with the set's digest,
	// which hashes the identity tag and the canonical String (§6.6).
	c.Hash = HashDefinition([]byte("catalog+mutators\n" + c.Hash + "\n" + m.Digest() + "\n"))
	return nil
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
// returned untouched, bit for bit.
func (f Factor) scaleCost(v float32) float32 {
	if !(v > 0) {
		return v
	}
	return float32(f.scale(int64(v), mutatorUnitCostLimit))
}

// checkIntegralCost refuses a positive cost that the unit compiler could not
// have stored: the compiler converts a 32-bit integer, so every positive cost
// it produces is integral and at most 2^31.
func checkIntegralCost(u *UnitDef, key string, v float32) error {
	if !(v > 0) {
		return nil
	}
	if v > float32(1<<31) || float32(int64(v)) != v {
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
