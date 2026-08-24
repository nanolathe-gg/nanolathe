package formats

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/nanolathe/nanolathe/vfs"
)

// ItemKind distinguishes a key/value assignment from a nested section.
type ItemKind uint8

const (
	Assignment ItemKind = iota
	NestedSection
)

// Item preserves source order and duplicate records. Callers that need a
// particular historical duplicate policy can choose FirstValue, LastValue,
// or Values instead of losing that information in a map.
type Item struct {
	Kind        ItemKind
	Key         string
	Value       string
	Section     *Section
	Line        int
	Column      int
	OriginalKey string
}

// Section is a lossless TDF section. The document root is an unnamed section;
// ordinary sections retain their original spelling and source location.
//
// Key lookups do not scan Items directly: they binary-search a resolved view
// built once per section [02 §4]. The resolution applies retail's duplicate
// policy — an identical spelling replaces the value in place (last write
// wins, one entry); a case-variant spelling coexists as a second distinct
// entry ordered by (case-insensitive fold, original bytes) — and typed
// lookups return the lower-bound entry, the first variant of the run
// (MEDIUM confidence: the mechanism is directly visible; accessor return
// among variants still needs black-box confirmation).
type Section struct {
	Name         string
	OriginalName string
	Line         int
	Column       int
	Items        []Item

	// resolved is the sorted, duplicate-resolved assignment vector. It is
	// built eagerly by the parser and lazily (single-threaded) for
	// hand-constructed sections; Items never change after either point.
	resolvedBuilt bool
	resolved      []Item
}

// Document is the parsed TDF syntax tree.
type Document struct {
	Root *Section
}

// TDFLimits bounds the amount of syntax-tree work performed for one
// untrusted document. The limits are intentionally generous for the retail
// corpus while keeping malformed or hostile input finite.
type TDFLimits struct {
	MaxBytes      int
	MaxDepth      int
	MaxItems      int
	MaxValueBytes int
	MaxNameBytes  int
}

func DefaultTDFLimits() TDFLimits {
	return TDFLimits{
		MaxBytes: 16 << 20, MaxDepth: 256, MaxItems: 1 << 18,
		MaxValueBytes: 4 << 20, MaxNameBytes: 1 << 20,
	}
}

func ParseTDF(data []byte) (*Document, error) {
	return ParseTDFWithLimits(data, DefaultTDFLimits())
}

func ParseTDFWithLimits(data []byte, limits TDFLimits) (*Document, error) {
	if limits.MaxBytes <= 0 || limits.MaxDepth <= 0 || limits.MaxItems <= 0 || limits.MaxValueBytes <= 0 || limits.MaxNameBytes <= 0 {
		return nil, fmt.Errorf("tdf: invalid parse limits")
	}
	if len(data) > limits.MaxBytes {
		return nil, fmt.Errorf("tdf: document size %d exceeds limit %d", len(data), limits.MaxBytes)
	}
	// Comments are blanked to spaces before the grammar runs, preserving every
	// character offset [02 §4]. The parser's own inline comment handling stays
	// as a safety net for callers that pass pre-blanked text.
	p := tdfParser{data: blankComments(data), line: 1, column: 1, limits: limits}
	root := &Section{}
	if err := p.parseBlock(root, false); err != nil {
		return nil, err
	}
	resolveDocument(root)
	return &Document{Root: root}, nil
}

// resolveDocument builds the resolved key view for every section in the tree,
// root first, then nested sections depth-first. Called once after a parse.
func resolveDocument(s *Section) {
	if s == nil {
		return
	}
	s.ensureResolved()
	for _, item := range s.Items {
		if item.Kind == NestedSection {
			resolveDocument(item.Section)
		}
	}
}

// ensureResolved builds the section's sorted assignment vector when it has
// not been built yet. See the Section type comment for the policy.
func (s *Section) ensureResolved() {
	if s.resolvedBuilt {
		return
	}
	type entry struct {
		item Item
		src  int // source position for last-wins among identical spellings
	}
	entries := make([]entry, 0, len(s.Items))
	for i, item := range s.Items {
		if item.Kind == Assignment {
			entries = append(entries, entry{item: item, src: i})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		fi, fj := foldName(entries[i].item.Key), foldName(entries[j].item.Key)
		if fi != fj {
			return fi < fj
		}
		oi, oj := entries[i].item.OriginalKey, entries[j].item.OriginalKey
		if oi != oj {
			return oi < oj
		}
		return entries[i].src < entries[j].src
	})
	// Collapse identical spellings to their last source occurrence; distinct
	// case variants each survive as one sorted entry [02 §4].
	s.resolved = s.resolved[:0]
	for k := 0; k < len(entries); k++ {
		last := k
		for last+1 < len(entries) &&
			foldName(entries[last+1].item.Key) == foldName(entries[k].item.Key) &&
			entries[last+1].item.OriginalKey == entries[k].item.OriginalKey {
			last++
		}
		s.resolved = append(s.resolved, entries[last].item)
		k = last
	}
	s.resolvedBuilt = true
}

// lookupResolved binary-searches the resolved vector for the lower bound of
// the fold run of key and returns that entry — the first variant in
// case-insensitive sort order [02 §4].
func (s *Section) lookupResolved(key string) (Item, bool) {
	s.ensureResolved()
	fold := foldName(key)
	lo := sort.Search(len(s.resolved), func(i int) bool {
		return foldName(s.resolved[i].Key) >= fold
	})
	if lo < len(s.resolved) && foldName(s.resolved[lo].Key) == fold {
		return s.resolved[lo], true
	}
	return Item{}, false
}

func LoadTDF(fs vfs.FSOps, name string) (*Document, error) {
	data, err := readVFSWithLimit(fs, name, int64(DefaultTDFLimits().MaxBytes))
	if err != nil {
		return nil, err
	}
	return ParseTDF(data)
}

func foldName(name string) string { return strings.ToLower(name) }

func (s *Section) Sections() []*Section {
	result := make([]*Section, 0)
	for i := range s.Items {
		if s.Items[i].Kind == NestedSection {
			result = append(result, s.Items[i].Section)
		}
	}
	return result
}

func (s *Section) Assignments() []Item {
	result := make([]Item, 0)
	for _, item := range s.Items {
		if item.Kind == Assignment {
			result = append(result, item)
		}
	}
	return result
}

// Values returns the values of every distinct key spelling matching key, in
// resolved order (case-insensitive fold, then original bytes). Identical
// duplicate spellings collapsed to their last value per [02 §4].
func (s *Section) Values(key string) []string {
	s.ensureResolved()
	key = foldName(key)
	result := make([]string, 0)
	for _, item := range s.resolved {
		if foldName(item.Key) == key {
			result = append(result, item.Value)
		}
	}
	return result
}

// FirstValue returns the lower-bound entry of key's fold run — the first
// variant in case-insensitive sort order [02 §4]. Identical duplicate
// spellings have already collapsed to their last value.
func (s *Section) FirstValue(key string) (string, bool) {
	item, ok := s.lookupResolved(key)
	if !ok {
		return "", false
	}
	return item.Value, true
}

// LastValue returns the upper-bound entry of key's fold run — the last
// variant in resolved order. For keys authored with a single spelling (the
// entire stock corpus) this equals FirstValue.
func (s *Section) LastValue(key string) (string, bool) {
	s.ensureResolved()
	fold := foldName(key)
	hi := -1
	for i, item := range s.resolved {
		if foldName(item.Key) == fold {
			hi = i
			continue
		}
		if hi >= 0 {
			break
		}
	}
	if hi < 0 {
		return "", false
	}
	return s.resolved[hi].Value, true
}

func (s *Section) Section(name string) *Section {
	name = foldName(name)
	for i := range s.Items {
		item := s.Items[i]
		if item.Kind == NestedSection && foldName(item.Section.Name) == name {
			return item.Section
		}
	}
	return nil
}

func (s *Section) SectionsNamed(name string) []*Section {
	name = foldName(name)
	result := make([]*Section, 0)
	for _, item := range s.Items {
		if item.Kind == NestedSection && foldName(item.Section.Name) == name {
			result = append(result, item.Section)
		}
	}
	return result
}

// Int, Float and Bool share the typed family's key location: the resolved
// lower-bound entry [02 §4]. They return (value, found, error) with the
// conversion applied.
func (s *Section) Int(key string) (int64, bool, error) {
	value, ok := s.FirstValue(key)
	if !ok {
		return 0, false, nil
	}
	return int64(ParseTDFInteger(value)), true, nil
}

func (s *Section) Float(key string) (float64, bool, error) {
	value, ok := s.FirstValue(key)
	if !ok {
		return 0, false, nil
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, true, fmt.Errorf("tdf: %s=%q: %w", key, value, err)
	}
	return n, true, nil
}

func (s *Section) Bool(key string) (bool, bool, error) {
	value, ok := s.FirstValue(key)
	if !ok {
		return false, false, nil
	}
	return ParseTDFInteger(value) != 0, true, nil
}

// ParseTDFInteger reproduces retail's 32-bit decimal atoi conversion. It
// accepts an optional sign and a decimal digit prefix, ignores trailing bytes,
// returns zero when no digit prefix exists, and wraps with 32-bit arithmetic.
// It deliberately does not recognize hexadecimal prefixes.
func ParseTDFInteger(value string) int32 {
	i := 0
	for i < len(value) && isTDFIntegerSpace(value[i]) {
		i++
	}
	negative := false
	if i < len(value) && (value[i] == '+' || value[i] == '-') {
		negative = value[i] == '-'
		i++
	}
	var magnitude uint32
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		magnitude = magnitude*10 + uint32(value[i]-'0')
		i++
	}
	result := int32(magnitude)
	if negative {
		result = -result
	}
	return result
}

func isTDFIntegerSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

type tdfParser struct {
	data   []byte
	pos    int
	line   int
	column int
	depth  int
	items  int
	limits TDFLimits
}

func (p *tdfParser) parseBlock(section *Section, untilClose bool) error {
	for {
		p.skipSpaceAndComments()
		if p.pos >= len(p.data) {
			if untilClose {
				return p.diag(DiagNextBlock, section.OriginalName, "")
			}
			return nil
		}
		if p.data[p.pos] == '}' {
			if !untilClose {
				return p.diag(DiagNextBlock, section.OriginalName, "")
			}
			p.advance()
			return nil
		}
		if p.data[p.pos] == '[' {
			child, err := p.parseSection()
			if err != nil {
				return err
			}
			if err := p.appendItem(section, Item{Kind: NestedSection, Section: child, Line: child.Line, Column: child.Column}); err != nil {
				return err
			}
			continue
		}
		item, err := p.parseAssignment()
		if err != nil {
			return err
		}
		if err := p.appendItem(section, item); err != nil {
			return err
		}
	}
}

func (p *tdfParser) appendItem(section *Section, item Item) error {
	if p.items >= p.limits.MaxItems {
		return p.errorf("document item count exceeds limit %d", p.limits.MaxItems)
	}
	p.items++
	section.Items = append(section.Items, item)
	return nil
}

func (p *tdfParser) parseSection() (*Section, error) {
	if p.depth >= p.limits.MaxDepth {
		return nil, p.errorf("section nesting exceeds limit %d", p.limits.MaxDepth)
	}
	line, column := p.line, p.column
	p.advance() // '['
	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] != ']' {
		if p.data[p.pos] == '\n' || p.data[p.pos] == '\r' {
			return nil, p.diag(DiagClosingBracket, strings.TrimSpace(string(p.data[start:p.pos])), "")
		}
		p.advance()
	}
	if p.pos >= len(p.data) {
		return nil, p.diag(DiagClosingBracket, strings.TrimSpace(string(p.data[start:p.pos])), "")
	}
	original := strings.TrimSpace(string(p.data[start:p.pos]))
	if p.pos-start > p.limits.MaxNameBytes {
		return nil, p.errorf("section name exceeds limit %d", p.limits.MaxNameBytes)
	}
	p.advance() // ']'
	p.skipSpaceAndComments()
	if p.pos >= len(p.data) || p.data[p.pos] != '{' {
		return nil, p.diag(DiagOpeningBrace, original, "")
	}
	p.advance()
	section := &Section{Name: foldName(original), OriginalName: original, Line: line, Column: column}
	p.depth++
	if err := p.parseBlock(section, true); err != nil {
		p.depth--
		return nil, err
	}
	p.depth--
	// Retail TDF files commonly terminate nested sections as `};`.
	// The semicolon belongs to the section terminator, not an assignment.
	if p.pos < len(p.data) && p.data[p.pos] == ';' {
		p.advance()
	}
	return section, nil
}

func (p *tdfParser) parseAssignment() (Item, error) {
	line, column := p.line, p.column
	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] != '=' {
		if p.data[p.pos] == '[' || p.data[p.pos] == '}' || p.data[p.pos] == ';' {
			return Item{}, p.diag(DiagEqualsNotFound, strings.TrimSpace(string(p.data[start:p.pos])), "")
		}
		p.advance()
	}
	if p.pos >= len(p.data) {
		return Item{}, p.diag(DiagEqualsNotFound, strings.TrimSpace(string(p.data[start:p.pos])), "")
	}
	originalKey := strings.TrimSpace(string(p.data[start:p.pos]))
	if p.pos-start > p.limits.MaxNameBytes {
		return Item{}, p.errorf("assignment key exceeds limit %d", p.limits.MaxNameBytes)
	}
	if originalKey == "" {
		return Item{}, p.errorf("empty assignment key")
	}
	p.advance() // '='
	value, err := p.readValue()
	if err != nil {
		return Item{}, err
	}
	return Item{Kind: Assignment, Key: foldName(originalKey), OriginalKey: originalKey, Value: value, Line: line, Column: column}, nil
}

func (p *tdfParser) readValue() (string, error) {
	var value strings.Builder
	for p.pos < len(p.data) {
		// A number of retail TDF/FBI tables omit the semicolon before the
		// next assignment. Newline is not a general value terminator, so only
		// accept this compatibility form when the following non-space bytes
		// unambiguously begin another key, section, or closing brace.
		if p.data[p.pos] == '\n' || p.data[p.pos] == '\r' {
			if p.implicitValueBoundary(p.pos) {
				return strings.TrimSpace(value.String()), nil
			}
		}
		if p.data[p.pos] == ';' {
			p.advance()
			return value.String(), nil
		}
		if p.data[p.pos] == '/' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '/' {
			p.advance()
			p.advance()
			for p.pos < len(p.data) && p.data[p.pos] != '\n' {
				p.advance()
			}
			value.WriteByte(' ')
			continue
		}
		if p.data[p.pos] == '/' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '*' {
			p.advance()
			p.advance()
			for p.pos < len(p.data) {
				if p.data[p.pos] == '*' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '/' {
					p.advance()
					p.advance()
					break
				}
				p.advance()
			}
			if p.pos >= len(p.data) && (len(p.data) < 2 || string(p.data[len(p.data)-2:]) != "*/") {
				return "", p.errorf("unterminated block comment in value")
			}
			value.WriteByte(' ')
			continue
		}
		if value.Len() >= p.limits.MaxValueBytes {
			return "", p.errorf("assignment value exceeds limit %d", p.limits.MaxValueBytes)
		}
		value.WriteByte(p.data[p.pos])
		p.advance()
	}
	return "", p.diag(DiagSemicolonMissing, "", strings.TrimSpace(value.String()))
}

func (p *tdfParser) implicitValueBoundary(pos int) bool {
	i := pos
	for i < len(p.data) && (p.data[i] == '\n' || p.data[i] == '\r' || p.data[i] == ' ' || p.data[i] == '\t') {
		i++
	}
	if i >= len(p.data) || p.data[i] == '}' || p.data[i] == '[' {
		return true
	}
	start := i
	for i < len(p.data) && (p.data[i] == '_' || p.data[i] == '-' || p.data[i] >= '0' && p.data[i] <= '9' || p.data[i] >= 'A' && p.data[i] <= 'Z' || p.data[i] >= 'a' && p.data[i] <= 'z') {
		i++
	}
	for i < len(p.data) && (p.data[i] == ' ' || p.data[i] == '\t') {
		i++
	}
	return i > start && i < len(p.data) && p.data[i] == '='
}

func (p *tdfParser) skipSpaceAndComments() {
	for p.pos < len(p.data) {
		if unicode.IsSpace(rune(p.data[p.pos])) {
			p.advance()
			continue
		}
		if p.data[p.pos] == '/' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '/' {
			p.advance()
			p.advance()
			for p.pos < len(p.data) && p.data[p.pos] != '\n' {
				p.advance()
			}
			continue
		}
		if p.data[p.pos] == '/' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '*' {
			p.advance()
			p.advance()
			closed := false
			for p.pos < len(p.data) {
				if p.data[p.pos] == '*' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '/' {
					p.advance()
					p.advance()
					closed = true
					break
				}
				p.advance()
			}
			if !closed {
				return
			}
			continue
		}
		break
	}
}

func (p *tdfParser) advance() {
	if p.pos >= len(p.data) {
		return
	}
	if p.data[p.pos] == '\n' {
		p.line++
		p.column = 1
	} else {
		p.column++
	}
	p.pos++
}

func (p *tdfParser) errorf(format string, args ...any) error {
	return fmt.Errorf("tdf: line %d column %d: %s", p.line, p.column, fmt.Sprintf(format, args...))
}

// diag returns one of retail's five verbatim parse diagnostics [02 §4],
// carrying the position and the partial name/value the tokenizer had scanned.
func (p *tdfParser) diag(d ParseDiagnostic, name, value string) error {
	return &ParseError{
		Diagnostic: d, Name: name, Value: value,
		Offset: p.pos, Line: p.line, Column: p.column,
	}
}
