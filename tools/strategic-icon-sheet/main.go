// strategic-icon-sheet compiles the winning retail catalog and exports a
// reproducible audit with the exact generated GPU mask art. Output belongs
// outside the repository (DESIGN_GPU_RENDERER §18.6).
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type sheetRow struct {
	Entry      client.StrategicIconAuditEntry
	Group, Art string
	Samples    []template.URL
}
type sheetData struct {
	Revision     int
	Constructors int
	Rows         []sheetRow
	Collisions   []string
	Unresolved   []string
}

var dark = color.RGBA{30, 42, 35, 255}
var bright = color.RGBA{203, 194, 143, 255}
var team = color.RGBA{75, 215, 240, 255}
var halo = color.RGBA{255, 240, 135, 255}

func main() {
	root := flag.String("root", filepath.Join(os.Getenv("HOME"), "TotalAnnihilation"), "retail asset directory")
	out := flag.String("out", "", "output directory outside the repository (required)")
	flag.Parse()
	if *out == "" {
		fatal(fmt.Errorf("provide -out for review artifacts"))
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(*root); err != nil {
		fatal(err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		fatal(err)
	}
	icons := client.NewStrategicIconCatalog(cat)
	entries := icons.Audit()
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		ak, bk := artKey(a), artKey(b)
		if ak != bk {
			return ak < bk
		}
		return a.Definition < b.Definition
	})
	data := sheetData{Revision: client.StrategicIconRevision}
	groups := make(map[string][]string)
	unique := []client.StrategicIconAuditEntry{}
	for _, e := range entries {
		key := artKey(e)
		if len(groups[key]) == 0 {
			unique = append(unique, e)
		}
		groups[key] = append(groups[key], e.Definition)
		row := sheetRow{Entry: e, Group: e.Descriptor.Family + " / " + e.Descriptor.Role, Art: key}
		for _, selected := range []bool{false, true} {
			for _, size := range []int{16, 20, 24} {
				bg := dark
				if selected {
					bg = bright
				}
				img := client.StrategicIconPreview(e.Descriptor, size, team, halo, bg, selected)
				var b bytes.Buffer
				if err := png.Encode(&b, img); err != nil {
					fatal(err)
				}
				row.Samples = append(row.Samples, template.URL("data:image/png;base64,"+base64.StdEncoding.EncodeToString(b.Bytes())))
			}
		}
		data.Rows = append(data.Rows, row)
		if len(e.Descriptor.Unresolved) > 0 {
			data.Unresolved = append(data.Unresolved, e.Definition+": "+strings.Join(e.Descriptor.Unresolved, "; "))
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(groups[key]) > 1 {
			data.Collisions = append(data.Collisions, key+": "+strings.Join(groups[key], ", "))
		}
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		fatal(err)
	}
	writePNG(filepath.Join(*out, "catalog.png"), renderSheet(entries, false))
	writePNG(filepath.Join(*out, "vocabulary.png"), renderSheet(unique, true))
	constructors := []client.StrategicIconAuditEntry{}
	for _, e := range entries {
		if e.Descriptor.Role == "construction" {
			constructors = append(constructors, e)
		}
	}
	data.Constructors = len(constructors)
	if data.Constructors > 0 {
		writePNG(filepath.Join(*out, "constructors.png"), renderSheet(constructors, false))
	}
	jf, err := os.Create(filepath.Join(*out, "audit.json"))
	if err != nil {
		fatal(err)
	}
	enc := json.NewEncoder(jf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		fatal(err)
	}
	if err := jf.Close(); err != nil {
		fatal(err)
	}
	hf, err := os.Create(filepath.Join(*out, "index.html"))
	if err != nil {
		fatal(err)
	}
	if err := template.Must(template.New("sheet").Parse(sheetHTML)).Execute(hf, data); err != nil {
		fatal(err)
	}
	if err := hf.Close(); err != nil {
		fatal(err)
	}
	fmt.Printf("%d definitions, %d shared symbols, %d entries with unresolved details\n%s\n", len(entries), len(unique), len(data.Unresolved), filepath.Join(*out, "index.html"))
}
func fatal(err error) {
	logicalPath := "strategic-icon-sheet"
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		logicalPath = pathErr.Path
	}
	fmt.Fprintf(os.Stderr, "nanolathe: strategic icon sheet failed (%v): logical path %q, providers searched [%s], expected generated strategic icon review artifacts\n", err, logicalPath, flag.Lookup("root").Value.String())
	os.Exit(1)
}
func artKey(e client.StrategicIconAuditEntry) string {
	d := e.Descriptor
	dots := 0
	if !d.CommanderAppearance && d.Level >= 2 {
		dots = min(d.Level, 3)
	}
	return fmt.Sprintf("%s/%s/%s/ticks%d", d.Family, d.Role, d.Subtype, dots)
}
func writePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		fatal(err)
	}
	if err := f.Close(); err != nil {
		fatal(err)
	}
}
func renderSheet(entries []client.StrategicIconAuditEntry, vocabulary bool) *image.RGBA {
	const columns = 5
	const w = 240
	const h = 106
	img := image.NewRGBA(image.Rect(0, 0, columns*w, ((len(entries)+columns-1)/columns)*h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{15, 21, 26, 255}}, image.Point{}, draw.Src)
	for i, e := range entries {
		x, y := (i%columns)*w, (i/columns)*h
		label := e.Definition
		if vocabulary {
			label = e.Descriptor.Family + " " + e.Descriptor.Role
		}
		textAt(img, x+8, y+7, label, color.RGBA{231, 239, 244, 255})
		sub := e.Descriptor.Family + "/" + e.Descriptor.Role
		if e.Descriptor.Role == "construction" && e.Descriptor.Subtype != "" {
			sub = e.Descriptor.Family + "/" + e.Descriptor.Subtype
		}
		if vocabulary {
			sub = e.Descriptor.Subtype
		}
		sub += fmt.Sprintf(" L%d", e.Descriptor.Level)
		textAt(img, x+8, y+18, sub, color.RGBA{145, 164, 175, 255})
		for row, selected := range []bool{false, true} {
			bg := dark
			if selected {
				bg = bright
			}
			draw.Draw(img, image.Rect(x+5, y+31+row*34, x+w-5, y+63+row*34), &image.Uniform{bg}, image.Point{}, draw.Src)
			for col, size := range []int{16, 20, 24} {
				icon := client.StrategicIconPreview(e.Descriptor, size, team, halo, bg, selected)
				left, top := x+12+col*75, y+35+row*34
				draw.Draw(img, image.Rect(left, top, left+size, top+size), icon, image.Point{}, draw.Src)
				textAt(img, left+29, top+8, fmt.Sprint(size), color.RGBA{105, 120, 130, 255})
			}
		}
	}
	return img
}

// A small independently authored bitmap alphabet labels the PNG contact sheet.
// The HTML carries the full Unicode authored names and all evidence.
var letters = map[rune]string{
	'A': "01110 10001 10001 11111 10001 10001 10001", 'B': "11110 10001 10001 11110 10001 10001 11110", 'C': "01111 10000 10000 10000 10000 10000 01111", 'D': "11110 10001 10001 10001 10001 10001 11110", 'E': "11111 10000 10000 11110 10000 10000 11111", 'F': "11111 10000 10000 11110 10000 10000 10000", 'G': "01111 10000 10000 10111 10001 10001 01111", 'H': "10001 10001 10001 11111 10001 10001 10001", 'I': "111 010 010 010 010 010 111", 'J': "00111 00010 00010 00010 00010 10010 01100", 'K': "10001 10010 10100 11000 10100 10010 10001", 'L': "10000 10000 10000 10000 10000 10000 11111", 'M': "10001 11011 10101 10101 10001 10001 10001", 'N': "10001 11001 11001 10101 10011 10011 10001", 'O': "01110 10001 10001 10001 10001 10001 01110", 'P': "11110 10001 10001 11110 10000 10000 10000", 'Q': "01110 10001 10001 10001 10101 10010 01101", 'R': "11110 10001 10001 11110 10100 10010 10001", 'S': "01111 10000 10000 01110 00001 00001 11110", 'T': "11111 00100 00100 00100 00100 00100 00100", 'U': "10001 10001 10001 10001 10001 10001 01110", 'V': "10001 10001 10001 10001 10001 01010 00100", 'W': "10001 10001 10001 10101 10101 11011 10001", 'X': "10001 10001 01010 00100 01010 10001 10001", 'Y': "10001 10001 01010 00100 00100 00100 00100", 'Z': "11111 00001 00010 00100 01000 10000 11111", '0': "01110 10001 10011 10101 11001 10001 01110", '1': "010 110 010 010 010 010 111", '2': "01110 10001 00001 00010 00100 01000 11111", '3': "11110 00001 00001 01110 00001 00001 11110", '4': "00010 00110 01010 10010 11111 00010 00010", '5': "11111 10000 10000 11110 00001 00001 11110", '6': "01110 10000 10000 11110 10001 10001 01110", '7': "11111 00001 00010 00100 01000 01000 01000", '8': "01110 10001 10001 01110 10001 10001 01110", '9': "01110 10001 10001 01111 00001 00001 01110", '/': "00001 00010 00010 00100 01000 01000 10000", '-': "000 000 000 111 000 000 000",
}

func textAt(img *image.RGBA, x, y int, s string, c color.RGBA) {
	for _, r := range strings.ToUpper(s) {
		rows := strings.Fields(letters[r])
		if len(rows) == 0 {
			x += 4
			continue
		}
		for dy, row := range rows {
			for dx, bit := range row {
				if bit == '1' {
					img.SetRGBA(x+dx, y+dy, c)
				}
			}
		}
		x += len(rows[0]) + 1
	}
}

const sheetHTML = `<!doctype html><html lang="en"><meta charset="utf-8"><title>Nanolathe strategic icon audit</title><style>
body{background:#11191f;color:#e5edf2;font:15px system-ui;margin:32px;line-height:1.5}h1{font-size:30px}h2{margin-top:40px}a{color:#66d8e6}.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(320px,1fr));gap:14px}.card{border:1px solid #344752;border-radius:9px;padding:16px;background:#1a262e}.name{font-weight:650;font-size:18px}.meta{font-size:12px;color:#a9bdc9}details{margin:12px 0;font-size:12px}summary{cursor:pointer}.samples{display:flex;align-items:center;gap:22px;padding:8px;margin-top:8px;background:#1e2a23;min-height:24px}.samples.light{background:#cbc28f}.unresolved{color:#f7cb83}li{margin:4px 0}code{overflow-wrap:anywhere}
</style><h1>Strategic icon catalog</h1><p>Vocabulary revision {{.Revision}} · {{len .Rows}} winning definitions. Independently authored Enhanced presentation symbols. Geometry uses the exact GPU atlas masks and bilinear sampling. Each row: 16 / 20 / 24 px; dark terrain unselected, bright terrain selected. Cyan is a sample owner color, pale gold is a sample selection palette color. Level 2 uses two light ticks with dark keylines across the original bottom border; level 3 uses three. Border thickness, body size and inner glyph are unchanged. Levels use the minimum factory count on a build path from a true commander, with authored numeric Category tokens as fallback when unreachable. The audit preserves both inputs. Levels 0/1 have no marks; larger authored levels cap at three ticks. Commanders and decoys share one crown without level marks. All factories share one factory symbol; combat units show the first active weapon slot. Shared art identifies family and capability, not exact unit identity. Visible nanoframes are excluded from strategic icons and icon picking. Builder and factory progress remains available through the existing selection UI. Sensor-only contacts do not disclose construction state.</p><p>{{if .Constructors}}<a href="constructors.png">Constructor comparison PNG</a> · {{end}}<a href="vocabulary.png">Vocabulary PNG</a> · <a href="catalog.png">Full catalog PNG</a> · <a href="audit.json">Machine-readable audit</a></p>
<h2>Definitions grouped by family, role and weapon glyph</h2><div class="grid">{{range .Rows}}<article class="card"><div class="name">{{.Entry.Definition}} · {{.Entry.Name}}</div><div>{{.Entry.Description}}</div><div class="meta">{{.Entry.Side}} · {{.Art}}</div><div class="samples">{{range $i,$s:=.Samples}}{{if lt $i 3}}<img src="{{$s}}">{{end}}{{end}}</div><div class="samples light">{{range $i,$s:=.Samples}}{{if ge $i 3}}<img src="{{$s}}">{{end}}{{end}}</div><details><summary>Evidence and provenance</summary><code>{{.Entry.LogicalPath}} · {{.Entry.Provider}} · mount {{.Entry.MountOrder}}<br>hash {{.Entry.Hash}}<br>Category: {{.Entry.Category}}<br>TEDClass: {{.Entry.TEDClass}}</code><ul>{{range .Entry.Descriptor.Evidence}}<li>{{.}}</li>{{end}}</ul><p>Capabilities: {{range .Entry.Descriptor.Capabilities}}{{.}}; {{end}}</p></details>{{if .Entry.Descriptor.Unresolved}}<details class="unresolved"><summary>Unresolved / conservative fallback</summary><ul>{{range .Entry.Descriptor.Unresolved}}<li>{{.}}</li>{{end}}</ul></details>{{end}}</article>{{end}}</div>
<h2>Shared-symbol collisions</h2><p>Intentional sharing is listed for review; exact identity remains available only through existing permitted hover information.</p><ul>{{range .Collisions}}<li><code>{{.}}</code></li>{{end}}</ul><h2>Unresolved evidence</h2><ul>{{range .Unresolved}}<li>{{.}}</li>{{end}}</ul></html>`
