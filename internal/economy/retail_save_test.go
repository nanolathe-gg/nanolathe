package economy

import "testing"

func TestRetailUnitAccountImageRoundTrip(t *testing.T) {
	s := &Service{}
	buckets := [2]Bucket{{Production: 7, Requested: 8, Accepted: 9, Carry: 10}, {Production: 1, Requested: 2, Accepted: 3, Carry: 4}}
	archived := [2]ArchivedBucket{{Production: 11, Requested: 12}, {Production: 5, Requested: 6}}
	s.RestoreUnitEconomy(17, buckets, archived)
	image, err := s.RetailUnitAccountImage(17)
	if err != nil {
		t.Fatal(err)
	}
	var restored Service
	if err := RetailUnitAccount(&restored, 3, image); err != nil {
		t.Fatal(err)
	}
	if got := restored.unitBuckets[3]; got.Buckets != buckets || got.Archived != archived {
		t.Fatalf("account round trip = %#v", got)
	}
}
