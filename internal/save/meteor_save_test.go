package save

import "testing"

func TestMeteorSnapshot_RoundTrip(t *testing.T) {
	st := &StateV1{
		Version:      StateV1VersionConst,
		CatalogHash:  "cat",
		ManifestHash: "man",
		Meteor: MeteorSnapshot{
			Enabled:       true,
			Active:        true,
			NextStrike:    100,
			StrikeEnds:    250,
			NextHit:       110,
			OriginX:       10,
			OriginZ:       20,
			TargetX:       30,
			TargetZ:       40,
			WeaponName:    "meteor",
			Density:       2.5,
			Radius:        300,
			DurationTicks: 150,
			IntervalTicks: 1800,
			PerHitDelay:   12,
			Initialized:   true,
		},
	}
	data := MarshalStateV1(st)
	dec, err := UnmarshalStateV1(data, "cat", "man")
	if err != nil {
		t.Fatalf("UnmarshalStateV1 meteor: %v", err)
	}
	if dec.Meteor.Enabled != st.Meteor.Enabled || dec.Meteor.Active != st.Meteor.Active || dec.Meteor.NextStrike != st.Meteor.NextStrike || dec.Meteor.WeaponName != st.Meteor.WeaponName || dec.Meteor.Radius != st.Meteor.Radius || dec.Meteor.Density != st.Meteor.Density {
		t.Fatalf("meteor snapshot round-trip mismatch got %+v want %+v", dec.Meteor, st.Meteor)
	}
	if dec.Version != StateV1VersionConst {
		t.Fatalf("version mismatch got %d want %d", dec.Version, StateV1VersionConst)
	}
}

func TestMeteorSnapshot_BackwardCompat(t *testing.T) {
	// Marshal version 7 (without meteor) should unmarshal as version 7 with zero meteor
	st7 := &StateV1{
		Version:      StateV1Version7,
		CatalogHash:  "cat",
		ManifestHash: "man",
	}
	data7 := MarshalStateV1(st7)
	dec, err := UnmarshalStateV1(data7, "cat", "man")
	if err != nil {
		t.Fatalf("Unmarshal v7: %v", err)
	}
	if dec.Meteor.Enabled || dec.Meteor.Active || dec.Meteor.Initialized {
		t.Fatalf("v7 should have zero meteor, got %+v", dec.Meteor)
	}
}
