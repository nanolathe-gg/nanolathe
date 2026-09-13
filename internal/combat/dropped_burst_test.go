package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// The selected creator owns the burst write, even when several family flags
// are authored. Dropped alone retains the common initializer's zero count;
// vertical launch takes precedence and copies the count [06 §4.3][06 §6.2].
func TestDroppedBurstStateFollowsCreationFamily(t *testing.T) {
	for _, tc := range []struct {
		name     string
		vertical bool
		want     int32
	}{
		{name: "dropped", want: 0},
		{name: "vertical before dropped", vertical: true, want: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s Service
			w := &content.WeaponDef{ID: 1, Dropped: true, VLaunch: tc.vertical, Burst: 5, BurstRate: 1}
			h, ok := TryFire(&s, &Slot{Weapon: w}, 0, Target{Kind: TargetPoint}, 7, FirePorts{})
			if !ok {
				t.Fatal("release failed")
			}
			if got := s.Records[int(h)-1].BurstRemaining; got != tc.want {
				t.Fatalf("remaining burst = %d, want %d for selected creator [06 §4.3]", got, tc.want)
			}
		})
	}
}
