package economy

import "testing"

func TestInitializeUnitEconomyCreatesSaveableZeroAccountAndResetsReuse(t *testing.T) {
	s := &Service{}
	if !s.InitializeUnitEconomy(297) {
		t.Fatal("initialize account")
	}
	image, err := s.RetailUnitAccountImage(297)
	if err != nil {
		t.Fatalf("new unit account image: %v", err)
	}
	for i, word := range image {
		if word != 0 {
			t.Fatalf("new unit account byte %d = %d, want zero", i, word)
		}
	}
	s.unitBuckets[297] = UnitEconomy{
		Buckets:  [2]Bucket{{Production: 7}, {Requested: 9}},
		Archived: [2]ArchivedBucket{{Production: 3}, {Requested: 4}},
	}
	if !s.InitializeUnitEconomy(297) {
		t.Fatal("reset reused account")
	}
	if got := s.unitBuckets[297]; got != (UnitEconomy{}) {
		t.Fatalf("reused account = %#v, want zero account", got)
	}
}

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
