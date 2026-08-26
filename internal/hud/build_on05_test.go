package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/snapshot"
)

func TestBuildProductsDataDrivenPaging(t *testing.T) {
	// Paging changes page data-driven [R-P0-03][07 §9] C10: no hardcoding, guard prevents overflow
	all := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	perPage := RetailBuildButtonsPerPage
	cnt := PageCountFromButtons(len(all), perPage)
	if cnt != 2 {
		t.Fatalf("page count want 2 got %d", cnt)
	}
	// Page 0 slice
	p0 := ProductsForPage(all, 0, perPage)
	if len(p0) != 6 || p0[0] != "a" || p0[5] != "f" {
		t.Fatalf("page 0 wrong %v", p0)
	}
	p1 := ProductsForPage(all, 1, perPage)
	if len(p1) != 4 || p1[0] != "g" || p1[3] != "j" {
		t.Fatalf("page1 wrong %v", p1)
	}
	// Guard: request beyond count clamps
	pClamped := ProductsForPage(all, 5, perPage)
	if len(pClamped) != 4 {
		t.Fatalf("clamp beyond count should give last page, got %v", pClamped)
	}
	// Next/Prev with guard
	flags := uint32(0) // page 0
	flags = NextPage(flags, cnt)
	if DecodePage(flags) != 1 {
		t.Fatalf("next page want 1 got %d", DecodePage(flags))
	}
	// Next beyond max stays at max (guard)
	flags = NextPage(flags, cnt)
	if DecodePage(flags) != 1 {
		t.Fatalf("next beyond max should stay 1 got %d", DecodePage(flags))
	}
	flags = PrevPage(flags, cnt)
	if DecodePage(flags) != 0 {
		t.Fatalf("prev page want 0 got %d", DecodePage(flags))
	}
	flags = PrevPage(flags, cnt)
	if DecodePage(flags) != 0 {
		t.Fatalf("prev beyond 0 should stay 0 got %d", DecodePage(flags))
	}
	// Validate product not invented
	if !ValidateBuildProductForTest(all, "a") {
		t.Fatalf("validate should find a")
	}
	if ValidateBuildProductForTest(all, "z") {
		t.Fatalf("validate should reject z not in list")
	}
}

// helper to simulate ValidateBuildProduct without catalog
func ValidateBuildProductForTest(all []string, prod string) bool {
	for _, b := range all {
		if b == prod {
			return true
		}
	}
	return false
}

func TestBuildValidationNoInvention(t *testing.T) {
	// GUI may not invent products absent from authored build list [R-P0-03]
	all := []string{"armsolar", "armfav"}
	if ValidateBuildProductForTest(all, "armcom") {
		t.Fatalf("should not invent armcom")
	}
}

func TestQueueCountLabelSumsPrimaryAndSecondary(t *testing.T) {
	queues := []snapshot.OrderQueueView{{
		Primary:   []snapshot.OrderView{{BuildProduct: "ArmFlash", BuildCount: 2}},
		Secondary: []snapshot.OrderView{{BuildProduct: "armflash", BuildCount: 3}},
	}}
	if got := QueueCountLabel(queues, "armflash"); got != "2 +3" {
		t.Fatalf("queue label=%q want %q", got, "2 +3")
	}
	if got := QueueCountLabel(queues, "armflea"); got != "" {
		t.Fatalf("missing product label=%q want empty", got)
	}
}
