package combat

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func compactionFixture(count, deadEvery int) Service {
	var s Service
	for i := 0; i < count; i++ {
		s.Reserve()
		s.Records[i].WeaponID = int32(i + 1)
		s.Records[i].OldMarker = -1
		s.Records[i].TargetProjectile = pool.Handle((i+count/2)%count + 1)
		if deadEvery > 0 && i%deadEvery == 0 {
			s.MarkDead(pool.Handle(i + 1))
		}
	}
	return s
}

func BenchmarkCompactionScratch(b *testing.B) {
	for _, count := range []int{0, 100, 300} {
		for _, every := range []int{0, 10, 2, 1} {
			b.Run(fmt.Sprintf("records%d/deadEvery%d", count, every), func(b *testing.B) {
				base := compactionFixture(count, every)
				var s Service
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					s.Slots = base.Slots
					copy(s.Records[:], base.Records[:])
					follow := pool.Handle(count)
					b.StartTimer()
					s.Compact(&follow)
				}
			})
		}
	}
}

// Every dead subset crosses unmoved/moved sources, removed/live targets,
// invalid raw links and follow-camera retirement. The untouched tail retains
// its pre-copy records with refreshed old markers [06 §5.2].
func TestCompactionAllDeadSubsetsAndTail(t *testing.T) {
	const count = 6
	for mask := 0; mask < 1<<count; mask++ {
		for _, link := range []pool.Handle{0, 1, 2, 3, 4, 5, 6, 300, 301, 65535} {
			s := compactionFixture(count, 0)
			for i := 0; i < count; i++ {
				s.Records[i].TargetProjectile = link
				if mask&(1<<i) != 0 {
					s.MarkDead(pool.Handle(i + 1))
				}
			}
			s.Records[count].WeaponID = 999
			expected := s.Records
			var survivors []int
			for i := 0; i < count; i++ {
				expected[i].OldMarker = int16(i)
				if mask&(1<<i) == 0 {
					survivors = append(survivors, i)
				}
			}
			marked := expected
			wantFollow := pool.Handle(0)
			for dest, old := range survivors {
				expected[dest] = marked[old]
				if old == count-1 {
					wantFollow = pool.Handle(dest + 1)
				}
				if dest != old && link != 0 {
					for target, oldTarget := range survivors {
						if pool.Handle(oldTarget+1) == link {
							expected[dest].TargetProjectile = pool.Handle(target + 1)
							break
						}
					}
				}
			}
			follow := pool.Handle(count)
			s.Compact(&follow)
			if s.Count() != len(survivors) || follow != wantFollow || !reflect.DeepEqual(s.Records, expected) {
				t.Fatalf("mask=%06b link=%d count=%d/%d follow=%d/%d", mask, link, s.Count(), len(survivors), follow, wantFollow)
			}
		}
	}
}
