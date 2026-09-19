package content

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
)

// RetailDefinitionDomain is the unit-definition ID domain retail compiles
// into: sixteen 32-bit membership words, so IDs 0..511, of which 0 is the
// catalog's null sentinel and 1..511 are usable [R-P0-03] [02 §5].
const RetailDefinitionDomain = CategoryMaskWords * 32

// RetailWeaponSlots is the retail weapon record table size: a fixed table of
// 256 records, each stamped once with its own slot number 0..255
// [02 "Weapon record"] [02 §5 R-CONTENT-02].
const RetailWeaponSlots = 256

// MaxDefinitionDomain is the largest domain this build can represent. The
// runtime definition identity a unit carries is a 16-bit field in the unit
// pool and in the committed frame views, so ID 65535 is the last one that
// survives the trip to presentation; a domain of 65536 makes 1..65535 usable.
// A profile asking for more is refused rather than silently truncated.
const MaxDefinitionDomain = 65536

// RetailTNTBytes and RetailLOSBytes are the host read caps a retail install is
// sized for: the largest map terrain file a map census will pull into memory
// when it cannot read a byte range out of the provider, and the largest
// battle-table file the LOS and meteor compilers will read. They are host
// policy, not retail table sizes — the retail loader's own storage for these
// files is sized by what the file declares, never by a byte budget — so
// raising them admits larger authored content without changing how any of it
// is read [03 R-COMP-02 §1] [fmt tnt].
const (
	RetailTNTBytes int64 = 16 << 20
	RetailLOSBytes int64 = 1 << 20
)

// Limits are the table sizes a catalog compile enforces. They are content
// policy, not gameplay: a limit changes which authored content is admitted,
// never what an admitted definition does in a tick. Retail's values are the
// baseline; a content profile may raise them, which is the user-authorised
// content policy documented in docs/DESIGN_CONTENT_VFS.md §5 "Content
// profiles" and docs/INVARIANTS.md I11.
//
// Units is the size of the unit-definition ID domain including the null
// sentinel at 0, so a domain of N admits N-1 definitions. Weapons is the size
// of the weapon record table, so slots 0..Weapons-1 exist.
//
// TNTBytes and LOSBytes are read caps in bytes, not table sizes: TNTBytes
// bounds a whole-file map terrain read and LOSBytes bounds each of the two
// battle-table reads, gamedata/los.tdf and gamedata/meteor.tdf.
type Limits struct {
	Units    int
	Weapons  int
	TNTBytes int64
	LOSBytes int64
}

// RetailLimits is the retail baseline: the 512-bit category domain, the
// 256-record weapon table, and the read caps a retail install is sized for.
// Compile uses it, and so does every compile whose
// options leave the counts unset, so a caller that resolved no content profile
// keeps retail admission unchanged.
func RetailLimits() Limits {
	return Limits{
		Units:    RetailDefinitionDomain,
		Weapons:  RetailWeaponSlots,
		TNTBytes: RetailTNTBytes,
		LOSBytes: RetailLOSBytes,
	}
}

// LimitsFromProfile converts a content profile's limits into the subset the
// catalog compile enforces. A profile that leaves a count unset keeps the
// retail value, so a partial profile is a partial override rather than a
// catalog with no domain at all. The host limits the profile also carries —
// the per-player unit limit and the pathfinding step allowance — belong to
// their own consumers and are not read here.
func LimitsFromProfile(p profiles.Limits) Limits {
	limits := RetailLimits()
	if p.Units > 0 {
		limits.Units = p.Units
	}
	if p.Weapons > 0 {
		limits.Weapons = p.Weapons
	}
	if p.TNTBytes > 0 {
		limits.TNTBytes = p.TNTBytes
	}
	if p.LOSBytes > 0 {
		limits.LOSBytes = p.LOSBytes
	}
	return limits
}

// normalize fills an unset Limits with the retail baseline and refuses a
// domain this build cannot represent. It runs once per compile, before any
// record is admitted, so a bad profile is reported instead of producing a
// catalog whose identities do not survive publication.
func (l Limits) normalize() (Limits, error) {
	if l.Units <= 0 {
		l.Units = RetailDefinitionDomain
	}
	if l.Weapons <= 0 {
		l.Weapons = RetailWeaponSlots
	}
	if l.TNTBytes <= 0 {
		l.TNTBytes = RetailTNTBytes
	}
	if l.LOSBytes <= 0 {
		l.LOSBytes = RetailLOSBytes
	}
	if l.Units > MaxDefinitionDomain {
		return Limits{}, fmt.Errorf("nanolathe: content profile asks for a larger unit-definition domain than this build represents: logical path <content-profile>, providers searched [content profile limits], expected a domain of at most %d, got %d", MaxDefinitionDomain, l.Units)
	}
	return l, nil
}

// definitionCapacity is the number of usable definition IDs, the null
// sentinel at 0 excluded.
func (l Limits) definitionCapacity() int { return l.Units - 1 }
