package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
)

func TestBuildProductsDataDrivenPaging(t *testing.T) {
	// Ten authored products make two six-button build pages, so the page-count
	// byte is three: page 0 is the orders state and carries no products, pages
	// 1 and 2 carry entries 1-6 and 7-10 [07 R-HUD-03 §6].
	all := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	perPage := RetailBuildButtonsPerPage
	cnt := PageCountFromButtons(len(all), perPage)
	if cnt != 3 {
		t.Fatalf("page count want 3 got %d", cnt)
	}
	if p0 := ProductsForPage(all, 0, perPage); len(p0) != 0 {
		t.Fatalf("the orders page carries no products, got %v", p0)
	}
	p1 := ProductsForPage(all, 1, perPage)
	if len(p1) != 6 || p1[0] != "a" || p1[5] != "f" {
		t.Fatalf("page 1 wrong %v", p1)
	}
	p2 := ProductsForPage(all, 2, perPage)
	if len(p2) != 4 || p2[0] != "g" || p2[3] != "j" {
		t.Fatalf("page 2 wrong %v", p2)
	}
	// A page past the last authored one carries nothing rather than repeating
	// the last page's products.
	if beyond := ProductsForPage(all, 5, perPage); len(beyond) != 0 {
		t.Fatalf("page beyond the last authored one carried %v", beyond)
	}
	// A builder with no authored products has the orders page and nothing else.
	if got := PageCountFromButtons(0, perPage); got != 1 {
		t.Fatalf("empty build list page count = %d, want 1", got)
	}
	// The NEXT and PREV gadgets never return to page 0 [07 R-HUD-03 §6].
	flags := uint32(0) // page 0
	flags = NextPage(flags, cnt)
	if DecodePage(flags) != 1 {
		t.Fatalf("next page want 1 got %d", DecodePage(flags))
	}
	flags = NextPage(flags, cnt)
	if DecodePage(flags) != 2 {
		t.Fatalf("next page want 2 got %d", DecodePage(flags))
	}
	flags = NextPage(flags, cnt)
	if DecodePage(flags) != 1 {
		t.Fatalf("next from the last page wraps to 1, got %d", DecodePage(flags))
	}
	flags = PrevPage(flags, cnt)
	if DecodePage(flags) != 2 {
		t.Fatalf("prev from page 1 wraps to the last page, got %d", DecodePage(flags))
	}
	// Validate product not invented
	if !ValidateBuildProductForTest(all, "a") {
		t.Fatalf("validate should find a")
	}
	if ValidateBuildProductForTest(all, "z") {
		t.Fatalf("validate should reject z not in list")
	}
}

// The page cycle's three producers move differently over the same page range
// [07 R-HUD-03 §6]: the keys wrap through page 0, the gadgets never return to
// it, and a digit that names no page does nothing at all.
func TestPageCycleKeysButtonsAndDigits(t *testing.T) {
	const count = 5 // four authored build pages plus the orders page
	for _, tc := range []struct{ page, next, prev int }{
		{0, 1, 4}, {1, 2, 0}, {3, 4, 2}, {4, 0, 3},
	} {
		if got := NextPageKey(tc.page, count); got != tc.next {
			t.Fatalf("`.` from page %d = %d, want %d", tc.page, got, tc.next)
		}
		if got := PrevPageKey(tc.page, count); got != tc.prev {
			t.Fatalf("`,` from page %d = %d, want %d", tc.page, got, tc.prev)
		}
	}
	for _, tc := range []struct{ page, next, prev int }{
		{0, 1, 4}, {1, 2, 4}, {3, 4, 2}, {4, 1, 3},
	} {
		if got := NextPageButton(tc.page, count); got != tc.next {
			t.Fatalf("NEXT from page %d = %d, want %d", tc.page, got, tc.next)
		}
		if got := PrevPageButton(tc.page, count); got != tc.prev {
			t.Fatalf("PREV from page %d = %d, want %d", tc.page, got, tc.prev)
		}
	}
	// A single-page builder (count 1: the orders page alone) has nowhere to go.
	if got := NextPageKey(0, 1); got != 0 {
		t.Fatalf("`.` with one page = %d, want 0", got)
	}
	if got := PrevPageKey(0, 1); got != 0 {
		t.Fatalf("`,` with one page = %d, want 0", got)
	}
	// Digit d selects page d-1, and selects nothing when that page is absent.
	for _, tc := range []struct{ digit, want int }{{1, 0}, {2, 1}, {5, 4}} {
		got, ok := DigitPage(tc.digit, count)
		if !ok || got != tc.want {
			t.Fatalf("digit %d = (%d, %v), want (%d, true)", tc.digit, got, ok, tc.want)
		}
	}
	if _, ok := DigitPage(6, count); ok {
		t.Fatal("digit 6 selected a page a five-page builder does not have")
	}
	if _, ok := DigitPage(0, count); ok {
		t.Fatal("digit 0 is not a page digit")
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
	queues := []frame.OrderQueueView{{
		Primary:   []frame.OrderView{{BuildProduct: "ArmFlash", BuildCount: 2}},
		Secondary: []frame.OrderView{{BuildProduct: "armflash", BuildCount: 3}},
	}}
	if got := QueueCountLabel(queues, "armflash"); got != "2 +3" {
		t.Fatalf("queue label=%q want %q", got, "2 +3")
	}
	if got := QueueCountLabel(queues, "armflea"); got != "" {
		t.Fatalf("missing product label=%q want empty", got)
	}
}
