// Package parity contains opt-in, deterministic evidence capture for parity
// investigations. It is a passive value recorder: it does not run simulation
// work, sample clocks, or own random streams [01 §4.4][I6].
package parity

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// Handle is one unit slot's identity in a capture.
type Handle struct {
	Slot          uint16 `json:"slot"`
	DefinitionKey string `json:"definition_key"`
	Owner         uint8  `json:"owner"`
	Alive         bool   `json:"alive"`
}

// Economy is one unit's economy record in a capture: its metal and energy
// buckets and the extraction state that feeds them.
type Economy struct {
	Slot             uint16  `json:"slot"`
	DefinitionKey    string  `json:"definition_key"`
	SpotMetal        float32 `json:"spot_metal"`
	Activated        bool    `json:"activated"`
	Remaining        float32 `json:"remaining"`
	MetalProduction  float32 `json:"metal_production"`
	MetalAccepted    float32 `json:"metal_accepted"`
	MetalRequested   float32 `json:"metal_requested"`
	MetalCarry       float32 `json:"metal_carry"`
	EnergyProduction float32 `json:"energy_production"`
	EnergyAccepted   float32 `json:"energy_accepted"`
	EnergyRequested  float32 `json:"energy_requested"`
	EnergyCarry      float32 `json:"energy_carry"`
}

// Callback is one script callback observed during a capture.
type Callback struct {
	Tick   uint32 `json:"tick"`
	Source uint16 `json:"source"`
	Target uint16 `json:"target"`
	Kind   string `json:"kind"`
	Value  int32  `json:"value"`
}

// Movement is one unit's position, goal and request state at a tick.
type Movement struct {
	Tick             uint32   `json:"tick"`
	Slot             uint16   `json:"slot"`
	X                int64    `json:"x"`
	Z                int64    `json:"z"`
	GoalX            int64    `json:"goal_x"`
	GoalZ            int64    `json:"goal_z"`
	RouteStatus      string   `json:"route_status"`
	Collision        string   `json:"collision"`
	CurrentRoute     []int32  `json:"current_route,omitempty"`
	NextRoute        []int32  `json:"next_route,omitempty"`
	Invalidated      bool     `json:"invalidated"`
	RequestState     string   `json:"request_state,omitempty"`
	CollisionHistory []string `json:"collision_history,omitempty"`
}

// Winner is one pick-buffer decision: the candidate written, the heights
// compared and whether the write was admitted.
type Winner struct {
	Tick           uint32 `json:"tick"`
	X              int32  `json:"x"`
	Y              int32  `json:"y"`
	Unit           uint16 `json:"unit"`
	Piece          int32  `json:"piece"`
	PrimitiveIndex int32  `json:"primitive_index"`
	Candidate      uint32 `json:"candidate"`
	IncomingHeight uint8  `json:"incoming_height"`
	StoredHeight   uint8  `json:"stored_height"`
	Admitted       bool   `json:"admitted"`
	ActualWinner   bool   `json:"actual_winner"`
	Color          uint8  `json:"color"`
	Texture        string `json:"texture,omitempty"`
	ShadeRow       int16  `json:"shade_row"`
	Primitive      string `json:"primitive"`
	Winner         string `json:"winner"`
}

// Crop is a named screenshot window. Coordinates are capture metadata only;
// they do not participate in simulation or rendering decisions. BoundsError
// is retained in the artifact so a clipped or invalid crop cannot disappear
// silently.
type Crop struct {
	Name        string `json:"name"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	W           int    `json:"w"`
	H           int    `json:"h"`
	Hash        string `json:"hash"`
	Format      string `json:"format"`
	BoundsError string `json:"bounds_error,omitempty"`
	Pixels      []byte `json:"-"`
}

// Capture is one labelled frame of evidence: the authoritative and
// framebuffer hashes plus whichever record families the caller supplied.
type Capture struct {
	Label              string     `json:"label"`
	Width              int        `json:"width"`
	Height             int        `json:"height"`
	Tick               uint32     `json:"tick"`
	AuthoritativeHash  string     `json:"authoritative_hash"`
	FramebufferHash    string     `json:"framebuffer_hash"`
	Handles            []Handle   `json:"handles,omitempty"`
	Economy            []Economy  `json:"economy,omitempty"`
	Callbacks          []Callback `json:"callbacks,omitempty"`
	Movement           []Movement `json:"movement,omitempty"`
	Winners            []Winner   `json:"winners,omitempty"`
	Crops              []Crop     `json:"crops,omitempty"`
	Residuals          []string   `json:"residuals,omitempty"`
	FrameDump          string     `json:"frame_dump,omitempty"`
	IndexedFramebuffer []byte     `json:"-"`
	RGBAFramebuffer    []byte     `json:"-"`
	IndexedFormat      string     `json:"indexed_format,omitempty"`
	RGBAFormat         string     `json:"rgba_format,omitempty"`
}

// Bundle is a run's evidence: the build and content identity it was taken
// against, and the captures in the order they were taken.
type Bundle struct {
	WorkID      string    `json:"work_id"`
	MainSHA     string    `json:"main_sha"`
	ContentHash string    `json:"content_hash"`
	Map         string    `json:"map"`
	Seed        uint32    `json:"seed"`
	Captures    []Capture `json:"captures"`
}

// Input is what a caller hands Bundle.Capture. The framebuffers and record
// slices are copied, so the caller may reuse its own buffers.
type Input struct {
	Label              string
	Tick               uint32
	Authoritative      any
	Frame              any
	IndexedFramebuffer []byte
	RGBAFramebuffer    []byte
	Handles            []Handle
	Economy            []Economy
	Callbacks          []Callback
	Movement           []Movement
	Winners            []Winner
	Crops              []Crop
	Residuals          []string
	Width              int
	Height             int
}

// NewBundle starts an empty evidence bundle for one run.
func NewBundle(workID, mainSHA, contentHash, mapName string, seed uint32) *Bundle {
	return &Bundle{WorkID: workID, MainSHA: mainSHA, ContentHash: contentHash, Map: mapName, Seed: seed}
}

func framebufferBytes(width, height, bytesPerPixel int) (int, error) {
	if width < 0 || height < 0 {
		return 0, fmt.Errorf("invalid framebuffer dimensions %dx%d", width, height)
	}
	if width == 0 || height == 0 {
		if width != 0 || height != 0 {
			return 0, fmt.Errorf("incomplete framebuffer dimensions %dx%d", width, height)
		}
		return 0, nil
	}
	if width > math.MaxInt/height {
		return 0, fmt.Errorf("framebuffer dimensions overflow %dx%d", width, height)
	}
	n := width * height
	if n > math.MaxInt/bytesPerPixel {
		return 0, fmt.Errorf("framebuffer dimensions overflow %dx%d", width, height)
	}
	return n * bytesPerPixel, nil
}

func validateFramebuffers(in Input) error {
	if len(in.IndexedFramebuffer) > 0 && len(in.RGBAFramebuffer) > 0 && in.Width == 0 && in.Height == 0 {
		return fmt.Errorf("framebuffer bytes require exact dimensions")
	}
	indexedN, err := framebufferBytes(in.Width, in.Height, 1)
	if err != nil {
		return err
	}
	rgbaN, err := framebufferBytes(in.Width, in.Height, 4)
	if err != nil {
		return err
	}
	if in.Width == 0 && in.Height == 0 {
		if len(in.IndexedFramebuffer) != 0 || len(in.RGBAFramebuffer) != 0 {
			return fmt.Errorf("framebuffer bytes require exact dimensions")
		}
		return nil
	}
	if len(in.IndexedFramebuffer) == 0 && len(in.RGBAFramebuffer) == 0 {
		return fmt.Errorf("missing framebuffer: dimensions %dx%d", in.Width, in.Height)
	}
	if len(in.IndexedFramebuffer) != 0 && len(in.IndexedFramebuffer) != indexedN {
		return fmt.Errorf("malformed indexed framebuffer: dimensions %dx%d, bytes %d", in.Width, in.Height, len(in.IndexedFramebuffer))
	}
	if len(in.RGBAFramebuffer) != 0 && len(in.RGBAFramebuffer) != rgbaN {
		return fmt.Errorf("malformed RGBA framebuffer: dimensions %dx%d, bytes %d", in.Width, in.Height, len(in.RGBAFramebuffer))
	}
	return nil
}

func cloneMovement(in []Movement) []Movement {
	out := append([]Movement(nil), in...)
	for i := range out {
		out[i].CurrentRoute = append([]int32(nil), in[i].CurrentRoute...)
		out[i].NextRoute = append([]int32(nil), in[i].NextRoute...)
		out[i].CollisionHistory = append([]string(nil), in[i].CollisionHistory...)
	}
	return out
}

func cloneCrops(in []Crop) []Crop {
	out := append([]Crop(nil), in...)
	for i := range out {
		out[i].Pixels = append([]byte(nil), in[i].Pixels...)
	}
	return out
}

// Capture appends one labelled capture, copying every buffer the caller
// supplied and hashing the framebuffers.
func (b *Bundle) Capture(in Input) error {
	if b == nil {
		return fmt.Errorf("parity: nil bundle")
	}
	if err := validateFramebuffers(in); err != nil {
		return fmt.Errorf("parity: %w", err)
	}
	authHash, err := StableHash(in.Authoritative)
	if err != nil {
		return fmt.Errorf("parity: authoritative hash: %w", err)
	}
	frameDump, err := StableJSON(in.Frame)
	if err != nil {
		return fmt.Errorf("parity: frame dump: %w", err)
	}
	c := Capture{
		Label:              in.Label,
		Tick:               in.Tick,
		Width:              in.Width,
		Height:             in.Height,
		AuthoritativeHash:  authHash,
		FramebufferHash:    FramebufferHash(in.RGBAFramebuffer),
		Handles:            append([]Handle(nil), in.Handles...),
		Economy:            append([]Economy(nil), in.Economy...),
		Callbacks:          append([]Callback(nil), in.Callbacks...),
		Movement:           cloneMovement(in.Movement),
		Winners:            append([]Winner(nil), in.Winners...),
		Crops:              cloneCrops(in.Crops),
		Residuals:          append([]string(nil), in.Residuals...),
		FrameDump:          frameDump,
		IndexedFramebuffer: append([]byte(nil), in.IndexedFramebuffer...),
		RGBAFramebuffer:    append([]byte(nil), in.RGBAFramebuffer...),
	}
	if len(in.IndexedFramebuffer) > 0 {
		c.IndexedFormat = "gray8"
	}
	if len(in.RGBAFramebuffer) > 0 {
		c.RGBAFormat = "rgba8"
	}
	if len(in.RGBAFramebuffer) == 0 {
		c.FramebufferHash = FramebufferHash(in.IndexedFramebuffer)
	}
	for i := range c.Crops {
		if c.Crops[i].Hash == "" {
			c.Crops[i].Hash = FramebufferHash(c.Crops[i].Pixels)
		}
		if c.Crops[i].Format == "" {
			c.Crops[i].Format = "rgba8-row-major"
		}
	}
	b.Captures = append(b.Captures, c)
	return nil
}

func evidenceName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return false
	}
	for _, r := range name {
		if !(r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return false
		}
	}
	return true
}

func captureStem(dir string, tick uint32, ordinal int, seen map[uint32]int) string {
	n := seen[tick]
	seen[tick] = n + 1
	stem := filepath.Join(dir, fmt.Sprintf("frame-%06d", tick))
	if n > 0 {
		stem = filepath.Join(dir, fmt.Sprintf("frame-%06d-%03d", tick, ordinal))
	}
	return stem
}

type evidenceFile struct {
	name string
	data []byte
}

func addEvidenceFile(files *[]evidenceFile, names map[string]struct{}, name string, data []byte) error {
	key := strings.ToLower(filepath.Clean(name))
	if _, exists := names[key]; exists {
		return fmt.Errorf("parity: duplicate evidence output path %q", name)
	}
	names[key] = struct{}{}
	*files = append(*files, evidenceFile{name: name, data: data})
	return nil
}

func (b *Bundle) outputPlan() ([]evidenceFile, error) {
	if b == nil {
		return nil, fmt.Errorf("parity: nil bundle")
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	files := make([]evidenceFile, 0, 1+len(b.Captures)*3)
	names := make(map[string]struct{}, cap(files))
	if err := addEvidenceFile(&files, names, "bundle.json", append(data, '\n')); err != nil {
		return nil, err
	}
	seenTicks := make(map[uint32]int, len(b.Captures))
	for i := range b.Captures {
		c := &b.Captures[i]
		stem := captureStem("", c.Tick, i, seenTicks)
		fd, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := addEvidenceFile(&files, names, stem+".json", append(fd, '\n')); err != nil {
			return nil, err
		}
		if len(c.IndexedFramebuffer) > 0 {
			expected, err := framebufferBytes(c.Width, c.Height, 1)
			if err != nil || expected != len(c.IndexedFramebuffer) {
				return nil, fmt.Errorf("parity: malformed indexed output at tick %d", c.Tick)
			}
			if err := addEvidenceFile(&files, names, stem+".indexed", c.IndexedFramebuffer); err != nil {
				return nil, err
			}
		}
		for _, crop := range c.Crops {
			if !evidenceName(crop.Name) {
				return nil, fmt.Errorf("parity: invalid crop name %q", crop.Name)
			}
			if len(crop.Pixels) == 0 {
				continue
			}
			if err := addEvidenceFile(&files, names, stem+"-"+crop.Name+".crop", crop.Pixels); err != nil {
				return nil, err
			}
		}
		if len(c.RGBAFramebuffer) > 0 {
			expected, err := framebufferBytes(c.Width, c.Height, 4)
			if err != nil || expected != len(c.RGBAFramebuffer) || c.Width <= 0 || c.Height <= 0 {
				return nil, fmt.Errorf("parity: malformed RGBA output at tick %d", c.Tick)
			}
			img := image.NewRGBA(image.Rect(0, 0, c.Width, c.Height))
			copy(img.Pix, c.RGBAFramebuffer)
			var pngData bytes.Buffer
			err = png.Encode(&pngData, img)
			if err != nil {
				return nil, err
			}
			if err := addEvidenceFile(&files, names, stem+".png", pngData.Bytes()); err != nil {
				return nil, err
			}
		}
	}
	return files, nil
}

// WriteDir writes the bundle and its framebuffer dumps into a directory.
func (b *Bundle) WriteDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("parity: empty evidence directory")
	}
	files, err := b.outputPlan()
	if err != nil {
		return err
	}
	// Validate all output paths before creating or writing anything. Evidence
	// is never silently replaced, including on case-insensitive filesystems.
	for _, file := range files {
		if _, err := os.Lstat(filepath.Join(dir, file.name)); err == nil {
			return fmt.Errorf("parity: evidence output already exists %q", file.name)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("parity: inspect evidence output %q: %w", file.name, err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file.name), file.data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// FramebufferHash is the evidence digest of a framebuffer: the first sixteen
// bytes of its SHA-256, hex encoded.
func FramebufferHash(pixels []byte) string {
	s := sha256.Sum256(pixels)
	return hex.EncodeToString(s[:16])
}

// CropRGBA copies a named RGBA window. If the requested rectangle is invalid
// or outside the framebuffer, BoundsError records that fact while retaining
// any non-empty clipped intersection. Callers that need an error return can
// use CropRGBAExact.
func CropRGBA(name string, rgba []byte, width, height, x, y, w, h int) Crop {
	c, _ := CropRGBAExact(name, rgba, width, height, x, y, w, h)
	return c
}

// CropRGBAExact copies a named RGBA window and returns the error a bad
// rectangle produced. CropRGBA is this without the error return.
func CropRGBAExact(name string, rgba []byte, width, height, x, y, w, h int) (Crop, error) {
	c := Crop{Name: name, X: x, Y: y, W: w, H: h, Format: "rgba8-row-major"}
	expected, dimErr := framebufferBytes(width, height, 4)
	if dimErr != nil {
		c.BoundsError = dimErr.Error()
		return c, dimErr
	}
	if expected != len(rgba) || width <= 0 || height <= 0 {
		err := fmt.Errorf("invalid RGBA framebuffer dimensions %dx%d, bytes %d", width, height, len(rgba))
		c.BoundsError = err.Error()
		return c, err
	}
	if w <= 0 || h <= 0 {
		err := fmt.Errorf("invalid crop dimensions %dx%d", w, h)
		c.BoundsError = err.Error()
		return c, err
	}
	// Use subtraction-based checks so x+w and y+h cannot overflow.
	x0, y0 := x, y
	x1, y1 := 0, 0
	if x > math.MaxInt-w {
		x1 = math.MaxInt
	} else {
		x1 = x + w
	}
	if y > math.MaxInt-h {
		y1 = math.MaxInt
	} else {
		y1 = y + h
	}
	if x < 0 || y < 0 || x1 > width || y1 > height {
		c.BoundsError = fmt.Sprintf("crop bounds (%d,%d)+(%d,%d) exceed framebuffer %dx%d", x, y, w, h, width, height)
	}
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > width {
		x1 = width
	}
	if y1 > height {
		y1 = height
	}
	if x0 >= x1 || y0 >= y1 {
		if c.BoundsError == "" {
			c.BoundsError = "crop has no intersection with framebuffer"
		}
		return c, fmt.Errorf("%s", c.BoundsError)
	}
	c.Pixels = make([]byte, 0, (x1-x0)*(y1-y0)*4)
	for row := y0; row < y1; row++ {
		c.Pixels = append(c.Pixels, rgba[(row*width+x0)*4:(row*width+x1)*4]...)
	}
	c.Hash = FramebufferHash(c.Pixels)
	if c.BoundsError != "" {
		return c, fmt.Errorf("%s", c.BoundsError)
	}
	return c, nil
}

// StableJSON is the human-readable frame dump. encoding/json emits struct
// fields in declaration order and sorts map keys.
func StableJSON(v any) (string, error) {
	if v == nil {
		return "null", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// StableHash encodes exported value structure canonically. Maps are sorted by
// their encoded keys, so diagnostic hashes never inherit Go map iteration
// order [I1]. Pointer addresses are used only for cycle detection and never
// enter the encoded bytes.
func StableHash(v any) (string, error) {
	b, err := canonical(reflect.ValueOf(v), make(map[visit]bool))
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:16]), nil
}

type visit struct {
	typ  reflect.Type
	kind reflect.Kind
	ptr  uintptr
}

func canonical(v reflect.Value, seen map[visit]bool) ([]byte, error) {
	if !v.IsValid() {
		return []byte{'n'}, nil
	}
	if v.Kind() == reflect.Interface {
		var w bytes.Buffer
		w.WriteString(v.Type().String())
		w.WriteByte('@')
		if v.IsNil() {
			w.WriteByte('n')
			return w.Bytes(), nil
		}
		out, err := canonical(v.Elem(), seen)
		if err != nil {
			return nil, err
		}
		w.Write(out)
		return w.Bytes(), nil
	}
	if v.Kind() == reflect.Pointer {
		var w bytes.Buffer
		w.WriteString(v.Type().String())
		w.WriteByte('@')
		if v.IsNil() {
			w.WriteByte('n')
			return w.Bytes(), nil
		}
		key := visit{typ: v.Type(), kind: v.Kind(), ptr: v.Pointer()}
		if seen[key] {
			return nil, fmt.Errorf("cyclic value at %s", v.Type())
		}
		seen[key] = true
		out, err := canonical(v.Elem(), seen)
		delete(seen, key)
		if err != nil {
			return nil, err
		}
		w.Write(out)
		return w.Bytes(), nil
	}
	var w bytes.Buffer
	w.WriteString(v.Type().String())
	w.WriteByte(':')
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			w.WriteByte('1')
		} else {
			w.WriteByte('0')
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		_ = binary.Write(&w, binary.LittleEndian, v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		_ = binary.Write(&w, binary.LittleEndian, v.Uint())
	case reflect.Float32:
		f := float32(v.Float())
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return nil, fmt.Errorf("unsupported non-finite float32 at %s", v.Type())
		}
		_ = binary.Write(&w, binary.LittleEndian, math.Float32bits(f))
	case reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("unsupported non-finite float64 at %s", v.Type())
		}
		_ = binary.Write(&w, binary.LittleEndian, math.Float64bits(f))
	case reflect.String:
		_ = binary.Write(&w, binary.LittleEndian, uint64(len(v.String())))
		w.WriteString(v.String())
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				w.WriteByte('n')
				return w.Bytes(), nil
			}
			key := visit{typ: v.Type(), kind: v.Kind(), ptr: uintptr(v.UnsafePointer())}
			if seen[key] {
				return nil, fmt.Errorf("cyclic value at %s", v.Type())
			}
			seen[key] = true
			defer delete(seen, key)
		}
		_ = binary.Write(&w, binary.LittleEndian, uint64(v.Len()))
		for i := 0; i < v.Len(); i++ {
			b, err := canonical(v.Index(i), seen)
			if err != nil {
				return nil, err
			}
			w.Write(b)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath != "" {
				continue
			}
			b, err := canonical(v.Field(i), seen)
			if err != nil {
				return nil, err
			}
			w.Write(b)
		}
	case reflect.Map:
		if v.IsNil() {
			w.WriteByte('n')
			return w.Bytes(), nil
		}
		key := visit{typ: v.Type(), kind: v.Kind(), ptr: uintptr(v.UnsafePointer())}
		if seen[key] {
			return nil, fmt.Errorf("cyclic value at %s", v.Type())
		}
		seen[key] = true
		defer delete(seen, key)
		type entry struct{ key, value []byte }
		entries := make([]entry, 0, v.Len())
		for _, k := range v.MapKeys() {
			kb, err := canonical(k, seen)
			if err != nil {
				return nil, err
			}
			vb, err := canonical(v.MapIndex(k), seen)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry{key: kb, value: vb})
		}
		sort.Slice(entries, func(i, j int) bool {
			if c := bytes.Compare(entries[i].key, entries[j].key); c != 0 {
				return c < 0
			}
			return bytes.Compare(entries[i].value, entries[j].value) < 0
		})
		_ = binary.Write(&w, binary.LittleEndian, uint64(len(entries)))
		for _, e := range entries {
			w.Write(e.key)
			w.Write(e.value)
		}
	default:
		return nil, fmt.Errorf("unsupported value kind %s (%s)", v.Kind(), v.Type())
	}
	return w.Bytes(), nil
}
