// The download-menu placement compiler.

package content

import (
	"errors"
	"fmt"
	"path"
	"sort"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// DownloadMenuPlacement is one safely represented download-menu item in
// deterministic union-enumeration and section order [02 R-CAT-01 §8].
// Builder and Product retain bounded authored names and report independently
// whether they resolved. Menu and Button retain the authored bytes; Button is
// a slot, so sparse generated pages stay sparse.
//
// Retail MENU is one greater than the visible build-page number: MENU=2 is
// visible page 1, MENU=3 is visible page 2 [fmt tdf][07 R-HUD-03 §6].
type DownloadMenuPlacement struct {
	Builder         string
	Menu            uint8
	Button          uint8
	Product         string
	BuilderResolved bool
	ProductResolved bool
	Provenance      Provenance
	FileOrder       int
	ItemOrder       int
}

// VisiblePage converts the authored MENU byte to the builder's visible page
// number [fmt tdf][07 R-HUD-03 §6]. Malformed MENU=0 consequently returns
// -1; the catalog preserves that authored byte rather than inventing a page.
func (p DownloadMenuPlacement) VisiblePage() int { return int(p.Menu) - 1 }

// CompileDownloadMenus union-enumerates download/*.tdf and retains every safely
// representable item [02 R-CAT-01 §8]. Resolution flags keep its three
// independent passes distinct: a builder-only resolution raises page count, a
// product-only resolution can participate in first-item enforcement, and both
// are required to append a product. An absent string is represented as empty;
// retail's allocator residue has no meaningful safe Go equivalent.
func CompileDownloadMenus(fs vfs.FSOps, units map[string]*UnitDef) ([]DownloadMenuPlacement, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	var entries []vfs.EntryInfo
	var err error
	if ordered, ok := fs.(interface {
		RetailReadDir(string) ([]vfs.EntryInfo, error)
	}); ok {
		entries, err = ordered.RetailReadDir("download")
	} else {
		// Synthetic FSOps may not expose the concrete retail enumerator. Their
		// supplied order is already their enumeration contract; preserve it.
		entries, err = fs.ReadDir("download")
	}
	if err != nil {
		if errors.Is(err, vfs.ErrNotFound) {
			// An install without the optional family has no placements.
			return nil, nil
		}
		return nil, fmt.Errorf("content: download: %w", err)
	}
	placements := make([]DownloadMenuPlacement, 0, len(entries))
	fileOrder := 0
	for _, entry := range entries {
		if entry.IsDir || asciiFoldContent(path.Ext(entry.Path)) != ".tdf" {
			continue
		}
		data, readErr := fs.ReadFileLimit(entry.Path, 1<<20)
		if readErr != nil {
			return nil, fmt.Errorf("content: %s: %w", entry.Path, readErr)
		}
		doc, parseErr := formats.ParseTDF(data)
		if parseErr != nil {
			return nil, formats.WithTDFContext(fs, parseErr, entry.Path)
		}
		prov := ProvenanceFrom(entry)
		for itemOrder, section := range doc.Root.Sections() {
			builderName, _ := section.StringValue("UNITMENU", "")
			productName, _ := section.StringValue("UNITNAME", "")
			builderName = boundedString(trimTDFSemantic(builderName), 31)
			productName = boundedString(trimTDFSemantic(productName), 31)
			builder := units[CanonicalKey(builderName)]
			product := units[CanonicalKey(productName)]
			if builder != nil {
				builderName = builder.UnitName
			}
			if product != nil {
				productName = product.UnitName
			}
			placements = append(placements, DownloadMenuPlacement{
				Builder:         builderName,
				Menu:            uint8(section.IntValue("MENU", 0)),
				Button:          uint8(section.IntValue("BUTTON", 0)),
				Product:         productName,
				BuilderResolved: builder != nil,
				ProductResolved: product != nil,
				Provenance:      prov,
				FileOrder:       fileOrder,
				ItemOrder:       itemOrder,
			})
		}
		fileOrder++
	}
	return placements, nil
}

// ApplyDownloadMenus raises page counts and appends resolved products to each
// builder's authoritative product list in placement order [02 R-CAT-01 §8].
// Retail attempts the append while the prior count is at most 30. A Go slice
// safely retains that possible 31st logical entry without reproducing the
// adjacent-memory overwrite of retail's 60-byte allocation.
func ApplyDownloadMenus(units map[string]*UnitDef, menus map[string]*BuildMenuPage, placements []DownloadMenuPlacement) []string {
	if len(units) == 0 {
		return nil
	}
	if menus != nil {
		ensureBuilderMenus(units, menus)
	}
	var firstProducts []string
	for _, placement := range placements {
		if placement.ItemOrder == 0 && placement.ProductResolved {
			firstProducts = append(firstProducts, placement.Product)
		}
		if !placement.BuilderResolved {
			continue
		}
		builder := units[CanonicalKey(placement.Builder)]
		if builder == nil {
			continue
		}
		if int32(placement.Menu) > builder.BuildPageCount {
			builder.BuildPageCount = int32(placement.Menu)
		}
		menu := menus[builder.CanonicalKey]
		if !placement.ProductResolved || !builder.Builder || menu == nil || len(menu.Buttons) > 30 {
			continue
		}
		menu.Buttons = append(menu.Buttons, placement.Product)
	}
	if menus != nil {
		for _, key := range sortedBuildMenuKeys(menus) {
			hashBuildMenu(menus[key])
		}
	}
	return EnforceDownloadable(units, firstProducts)
}

func ensureBuilderMenus(units map[string]*UnitDef, menus map[string]*BuildMenuPage) {
	keys := make([]string, 0, len(units))
	for key := range units {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		u := units[key]
		if u == nil || !u.Builder || menus[key] != nil {
			continue
		}
		menus[key] = &BuildMenuPage{
			DefinitionHeader: DefinitionHeader{CanonicalKey: key, Provenance: u.Provenance},
			Builder:          u.UnitName,
		}
	}
}
