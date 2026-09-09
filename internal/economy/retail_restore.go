package economy

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

const retailUnitAccountSize = 48

// RetailUnitAccount restores the twelve single-precision accumulators in the
// u%04xacc image. The account is two consecutive six-word resource halves:
// production, requested, accepted, carry, archived production, archived
// requested [08 R-SAVE-02 §7; 05 "Unit instance economy state"].
func RetailUnitAccount(s *Service, h pool.Handle, data []byte) error {
	if s == nil {
		return fmt.Errorf("economy: retail restore: nil service")
	}
	if h == 0 {
		return fmt.Errorf("economy: retail restore: null unit handle")
	}
	if len(data) != retailUnitAccountSize {
		return fmt.Errorf("economy: retail restore: account size %d, want 48", len(data))
	}
	s.ensureUnitBuckets(h)
	var buckets [2]Bucket
	var archived [2]ArchivedBucket
	// Wire order is Energy then Metal, unlike the runtime enum (Metal=0,
	// Energy=1) [05 "Unit instance economy state"].
	for wire, resource := range [...]Res{Energy, Metal} {
		base := wire * 24
		buckets[resource] = Bucket{
			Production: binaryFloat32(data[base:]),
			Requested:  binaryFloat32(data[base+4:]),
			Accepted:   binaryFloat32(data[base+8:]),
			Carry:      binaryFloat32(data[base+12:]),
		}
		archived[resource] = ArchivedBucket{
			Production: binaryFloat32(data[base+16:]),
			Requested:  binaryFloat32(data[base+20:]),
		}
	}
	if !s.RestoreUnitEconomy(h, buckets, archived) {
		return fmt.Errorf("economy: retail restore: unit handle %d outside ledger", h)
	}
	return nil
}

func binaryFloat32(data []byte) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data))
}
