package economy

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func TestRetailUnitAccountRestoresAllAccumulators(t *testing.T) {
	s := &Service{}
	data := make([]byte, retailUnitAccountSize)
	values := []float32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	for i, value := range values {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	if err := RetailUnitAccount(s, pool.Handle(7), data); err != nil {
		t.Fatal(err)
	}
	if got := s.UnitBuckets(7); got[Energy].Production != 1 || s.unitBuckets[7].Archived[Energy].Requested != 6 || got[Metal].Production != 7 || s.unitBuckets[7].Archived[Metal].Requested != 12 {
		t.Fatalf("restored account = %+v, want all twelve values", got)
	}
}
