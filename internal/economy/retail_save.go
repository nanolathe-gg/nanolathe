package economy

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// RetailUnitAccountImage returns the detached 48-byte u%04xacc image. Wire
// resource order is Energy then Metal [08 R-SAVE-02 §7; 05 "Unit instance
// economy state"].
func (s *Service) RetailUnitAccountImage(h pool.Handle) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("economy: retail save: nil service")
	}
	if h == 0 || int(h) >= len(s.unitBuckets) {
		return nil, fmt.Errorf("economy: retail save: unit handle %d has no account", h)
	}
	data := make([]byte, retailUnitAccountSize)
	for wire, resource := range [...]Res{Energy, Metal} {
		base := wire * 24
		bucket := s.unitBuckets[h].Buckets[resource]
		archived := s.unitBuckets[h].Archived[resource]
		values := [...]float32{bucket.Production, bucket.Requested, bucket.Accepted, bucket.Carry, archived.Production, archived.Requested}
		for i, value := range values {
			binary.LittleEndian.PutUint32(data[base+i*4:], math.Float32bits(value))
		}
	}
	return data, nil
}
