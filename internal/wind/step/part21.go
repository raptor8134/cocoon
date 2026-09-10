// Package step reads ISO 10303-21 ("STEP Part 21") physical files.
//
// This is deliberately NOT a general STEP implementation. It parses the Part 21
// *syntax* -- which is small -- into an addressable table of entity instances,
// and leaves interpretation to the caller. A mandrel needs about fifteen of the
// thousands of entity types defined across the STEP application protocols, so
// carrying a full CAD kernel to reach them is a poor trade.
//
// Why not a library:
//
//   - occt-import-js and friends TESSELLATE. They return triangle meshes, which
//     throws away the analytic surfaces (radius, axis) that made STEP worth
//     choosing over STL in the first place.
//   - Full OpenCascade builds for wasm run from several to tens of megabytes,
//     against a 1.3 MB core here.
//   - STEPcode and the other complete implementations are C/C++, and Go's wasm
//     target does not support cgo, so they cannot be linked in at all.
//
// The Part 21 grammar handled here:
//
//	#12 = CARTESIAN_POINT ( 'name', ( 0.0, 1.0, 2.0 ) ) ;
//	#13 = ( NAMED_UNIT(*) SI_UNIT(.MILLI., .METRE.) )   -- complex instance
//
// with values being: integer, real, string, enumeration, reference, list,
// unset ($) and derived (*).
package step

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Value is one parameter of an entity instance.
type Value struct {
	Kind Kind
	Num  float64 // Int, Real
	Str  string  // Str, Enum, and the type name of a typed value
	Ref  int     // Ref: the #id referred to
	List []Value // List, or a typed value's single payload
}

// Kind discriminates a Value.
type Kind uint8

const (
	Unset   Kind = iota // $
	Derived             // *
	Int
	Real
	Str
	Enum // .TRUE. / .MILLI. etc, stored without the dots
	Ref  // #123
	List
	Typed // NAME(value) appearing as a parameter
)

// Entity is one instance from the DATA section.
//
// A complex instance (several types sharing one id) keeps every type in Types,
// with Params holding each type's parameters in the same order.
type Entity struct {
	ID     int
	Type   string   // first type name; the common case
	Types  []string // all type names, for complex instances
	Params []Value
}

// File is a parsed Part 21 document.
type File struct {
	// Header holds the entities from the HEADER section, keyed by type
	// (FILE_DESCRIPTION, FILE_NAME, FILE_SCHEMA).
	Header map[string]Entity

	// Entities maps #id to instance.
	Entities map[int]Entity

	// byType indexes ids by entity type for the common "find all X" query.
	byType map[string][]int
}

// Get resolves a reference, reporting whether it exists.
func (f *File) Get(id int) (Entity, bool) {
	e, ok := f.Entities[id]
	return e, ok
}

// OfType returns every entity of the given type, in file order.
func (f *File) OfType(name string) []Entity {
	ids := f.byType[strings.ToUpper(name)]
	out := make([]Entity, 0, len(ids))
	for _, id := range ids {
		out = append(out, f.Entities[id])
	}
	return out
}

// Schema returns the FILE_SCHEMA string, e.g. "AP214" or "AP242".
func (f *File) Schema() string {
	e, ok := f.Header["FILE_SCHEMA"]
	if !ok || len(e.Params) == 0 {
		return ""
	}
	if e.Params[0].Kind == List && len(e.Params[0].List) > 0 {
		return e.Params[0].List[0].Str
	}
	return e.Params[0].Str
}

// Parse reads a Part 21 file.
//
// Errors are returned for structural problems only. Unknown entity types are
// kept verbatim rather than rejected: a file may legitimately carry types this
// package has never heard of, and refusing to read it would be unhelpful.
func Parse(r io.Reader) (*File, error) {
	src, err := io.ReadAll(bufio.NewReader(r))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	p := &parser{s: string(src)}
	return p.parseFile()
}

type parser struct {
	s string
	i int
}

// skip advances past whitespace and comments. Part 21 comments are /* ... */
// and may appear anywhere whitespace may.
func (p *parser) skip() {
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			p.i++
			continue
		}
		if c == '/' && p.i+1 < len(p.s) && p.s[p.i+1] == '*' {
			end := strings.Index(p.s[p.i+2:], "*/")
			if end < 0 {
				p.i = len(p.s)
				return
			}
			p.i += 2 + end + 2
			continue
		}
		return
	}
}

func (p *parser) parseFile() (*File, error) {
	f := &File{
		Header:   map[string]Entity{},
		Entities: map[int]Entity{},
		byType:   map[string][]int{},
	}

	// The leading ISO-10303-21; marker is required but carries no data.
	p.skip()
	if !strings.HasPrefix(p.s[p.i:], "ISO-10303-21") {
		return nil, fmt.Errorf("not a STEP Part 21 file (missing ISO-10303-21 header)")
	}

	inHeader := false
	for {
		p.skip()
		if p.i >= len(p.s) {
			break
		}
		rest := p.s[p.i:]
		switch {
		case strings.HasPrefix(rest, "HEADER;"):
			p.i += len("HEADER;")
			inHeader = true
			continue
		case strings.HasPrefix(rest, "ENDSEC;"):
			p.i += len("ENDSEC;")
			inHeader = false
			continue
		case strings.HasPrefix(rest, "DATA"):
			// DATA; or DATA(...);
			p.i += len("DATA")
			p.skipToSemicolon()
			continue
		case strings.HasPrefix(rest, "END-ISO-10303-21"):
			p.i = len(p.s)
			continue
		case strings.HasPrefix(rest, "ISO-10303-21"):
			p.i += len("ISO-10303-21")
			p.skipToSemicolon()
			continue
		}

		if inHeader {
			e, err := p.parseHeaderEntity()
			if err != nil {
				return nil, err
			}
			f.Header[e.Type] = e
			continue
		}

		e, err := p.parseInstance()
		if err != nil {
			return nil, err
		}
		f.Entities[e.ID] = e
		for _, t := range e.Types {
			f.byType[t] = append(f.byType[t], e.ID)
		}
	}
	return f, nil
}

func (p *parser) skipToSemicolon() {
	for p.i < len(p.s) && p.s[p.i] != ';' {
		p.i++
	}
	if p.i < len(p.s) {
		p.i++
	}
}

// parseHeaderEntity reads NAME(params); with no leading #id.
func (p *parser) parseHeaderEntity() (Entity, error) {
	name := p.parseName()
	if name == "" {
		p.skipToSemicolon()
		return Entity{}, nil
	}
	params, err := p.parseParams()
	if err != nil {
		return Entity{}, err
	}
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == ';' {
		p.i++
	}
	return Entity{Type: name, Types: []string{name}, Params: params}, nil
}

// parseInstance reads "#id = ..." through its terminating semicolon.
func (p *parser) parseInstance() (Entity, error) {
	p.skip()
	if p.i >= len(p.s) || p.s[p.i] != '#' {
		return Entity{}, fmt.Errorf("expected entity id at offset %d", p.i)
	}
	p.i++
	id, err := p.parseInt()
	if err != nil {
		return Entity{}, fmt.Errorf("bad entity id at offset %d: %w", p.i, err)
	}
	p.skip()
	if p.i >= len(p.s) || p.s[p.i] != '=' {
		return Entity{}, fmt.Errorf("expected '=' after #%d", id)
	}
	p.i++
	p.skip()

	e := Entity{ID: id}

	// A complex instance is a parenthesised run of typed records.
	if p.i < len(p.s) && p.s[p.i] == '(' {
		p.i++
		for {
			p.skip()
			if p.i >= len(p.s) || p.s[p.i] == ')' {
				p.i++
				break
			}
			name := p.parseName()
			if name == "" {
				return Entity{}, fmt.Errorf("bad complex instance #%d", id)
			}
			params, err := p.parseParams()
			if err != nil {
				return Entity{}, err
			}
			e.Types = append(e.Types, name)
			e.Params = append(e.Params, params...)
		}
	} else {
		name := p.parseName()
		if name == "" {
			return Entity{}, fmt.Errorf("expected type name for #%d", id)
		}
		params, err := p.parseParams()
		if err != nil {
			return Entity{}, err
		}
		e.Types = []string{name}
		e.Params = params
	}

	if len(e.Types) > 0 {
		e.Type = e.Types[0]
	}
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == ';' {
		p.i++
	}
	return e, nil
}

func (p *parser) parseName() string {
	p.skip()
	start := p.i
	for p.i < len(p.s) {
		c := p.s[p.i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			p.i++
			continue
		}
		break
	}
	return strings.ToUpper(p.s[start:p.i])
}

func (p *parser) parseInt() (int, error) {
	p.skip()
	start := p.i
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	if start == p.i {
		return 0, fmt.Errorf("expected integer")
	}
	return strconv.Atoi(p.s[start:p.i])
}

// parseParams reads a parenthesised, comma-separated parameter list.
func (p *parser) parseParams() ([]Value, error) {
	p.skip()
	if p.i >= len(p.s) || p.s[p.i] != '(' {
		return nil, nil // a type with no parameter list
	}
	p.i++
	var out []Value
	for {
		p.skip()
		if p.i >= len(p.s) {
			return nil, fmt.Errorf("unterminated parameter list")
		}
		if p.s[p.i] == ')' {
			p.i++
			return out, nil
		}
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.skip()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
		}
	}
}

func (p *parser) parseValue() (Value, error) {
	p.skip()
	if p.i >= len(p.s) {
		return Value{}, fmt.Errorf("unexpected end of file in value")
	}
	switch c := p.s[p.i]; {
	case c == '$':
		p.i++
		return Value{Kind: Unset}, nil
	case c == '*':
		p.i++
		return Value{Kind: Derived}, nil
	case c == '#':
		p.i++
		id, err := p.parseInt()
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: Ref, Ref: id}, nil
	case c == '\'':
		s, err := p.parseString()
		return Value{Kind: Str, Str: s}, err
	case c == '.':
		p.i++
		start := p.i
		for p.i < len(p.s) && p.s[p.i] != '.' {
			p.i++
		}
		v := Value{Kind: Enum, Str: p.s[start:p.i]}
		if p.i < len(p.s) {
			p.i++
		}
		return v, nil
	case c == '(':
		list, err := p.parseParams()
		return Value{Kind: List, List: list}, err
	case c == '-' || c == '+' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	default:
		// A typed parameter: NAME(value).
		name := p.parseName()
		if name == "" {
			return Value{}, fmt.Errorf("unexpected character %q at offset %d", c, p.i)
		}
		inner, err := p.parseParams()
		return Value{Kind: Typed, Str: name, List: inner}, err
	}
}

func (p *parser) parseString() (string, error) {
	p.i++ // opening quote
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '\'' {
			// A doubled quote is an escaped quote.
			if p.i+1 < len(p.s) && p.s[p.i+1] == '\'' {
				b.WriteByte('\'')
				p.i += 2
				continue
			}
			p.i++
			return b.String(), nil
		}
		b.WriteByte(c)
		p.i++
	}
	return "", fmt.Errorf("unterminated string")
}

func (p *parser) parseNumber() (Value, error) {
	start := p.i
	if p.s[p.i] == '-' || p.s[p.i] == '+' {
		p.i++
	}
	isReal := false
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c >= '0' && c <= '9' {
			p.i++
			continue
		}
		if c == '.' || c == 'e' || c == 'E' {
			isReal = true
			p.i++
			continue
		}
		if (c == '-' || c == '+') && (p.s[p.i-1] == 'e' || p.s[p.i-1] == 'E') {
			p.i++
			continue
		}
		break
	}
	text := p.s[start:p.i]
	n, err := strconv.ParseFloat(strings.TrimSuffix(text, "."), 64)
	if err != nil {
		return Value{}, fmt.Errorf("bad number %q: %w", text, err)
	}
	if isReal {
		return Value{Kind: Real, Num: n}, nil
	}
	return Value{Kind: Int, Num: n}, nil
}
