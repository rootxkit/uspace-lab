package basemap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// The glyph PBF schema as font-maker writes it (main.cpp, do_range and
// do_codepoint; the MapLibre glyphs.proto): glyphs{1: fontstack},
// fontstack{1: name, 2: range, 3: glyph}, glyph{1: id, 2: bitmap,
// 3: width, 4: height, 5: left, 6: top, 7: advance}.
const (
	fieldGlyphsStack  = 1
	fieldStackName    = 1
	fieldStackRange   = 2
	fieldStackGlyph   = 3
	fieldGlyphID      = 1
	fieldGlyphBitmap  = 2
	fieldGlyphWidth   = 3
	fieldGlyphHeight  = 4
	wireVarint        = 0
	wireFixed64       = 1
	wireBytes         = 2
	wireFixed32       = 5
	glyphRangeSize    = 256
	maxGlyphFileBytes = 8 << 20
)

// Glyph is one decoded glyph: its code point and whether it draws.
type Glyph struct {
	ID        rune
	Width     uint32
	Height    uint32
	BitmapLen int
}

// Draws reports whether the glyph has an SDF bitmap to draw.
func (g Glyph) Draws() bool { return g.Width > 0 && g.Height > 0 && g.BitmapLen > 0 }

// GlyphRange is one decoded {range}.pbf file.
type GlyphRange struct {
	Name   string // the fontstack name inside the file
	Range  string // "start-end" inside the file
	Glyphs map[rune]Glyph
}

type pbField struct {
	num  uint64
	wire uint64
	v    uint64
	b    []byte
}

func pbFields(b []byte) ([]pbField, error) {
	var out []pbField
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errors.New("protobuf: bad field key")
		}
		b = b[n:]
		f := pbField{num: key >> 3, wire: key & 7}
		switch f.wire {
		case wireVarint:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, fmt.Errorf("protobuf: field %d: bad varint", f.num)
			}
			f.v, b = v, b[n:]
		case wireBytes:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return nil, fmt.Errorf("protobuf: field %d: bad length", f.num)
			}
			f.b, b = b[n:n+int(l)], b[n+int(l):]
		case wireFixed64:
			if len(b) < 8 {
				return nil, fmt.Errorf("protobuf: field %d: short fixed64", f.num)
			}
			b = b[8:]
		case wireFixed32:
			if len(b) < 4 {
				return nil, fmt.Errorf("protobuf: field %d: short fixed32", f.num)
			}
			b = b[4:]
		default:
			return nil, fmt.Errorf("protobuf: field %d: wire type %d", f.num, f.wire)
		}
		out = append(out, f)
	}
	return out, nil
}

// DecodeGlyphRange decodes a glyph PBF holding one fontstack.
func DecodeGlyphRange(b []byte) (*GlyphRange, error) {
	top, err := pbFields(b)
	if err != nil {
		return nil, err
	}
	var stacks [][]byte
	for _, f := range top {
		if f.num == fieldGlyphsStack && f.wire == wireBytes {
			stacks = append(stacks, f.b)
		}
	}
	if len(stacks) != 1 {
		return nil, fmt.Errorf("glyphs: %d fontstacks, want 1", len(stacks))
	}
	fs, err := pbFields(stacks[0])
	if err != nil {
		return nil, err
	}
	gr := &GlyphRange{Glyphs: map[rune]Glyph{}}
	for _, f := range fs {
		switch {
		case f.num == fieldStackName && f.wire == wireBytes:
			gr.Name = string(f.b)
		case f.num == fieldStackRange && f.wire == wireBytes:
			gr.Range = string(f.b)
		case f.num == fieldStackGlyph && f.wire == wireBytes:
			g, err := decodeGlyph(f.b)
			if err != nil {
				return nil, err
			}
			gr.Glyphs[g.ID] = g
		}
	}
	return gr, nil
}

func decodeGlyph(b []byte) (Glyph, error) {
	fs, err := pbFields(b)
	if err != nil {
		return Glyph{}, err
	}
	var g Glyph
	hasID := false
	for _, f := range fs {
		switch {
		case f.num == fieldGlyphID && f.wire == wireVarint:
			if f.v > unicode.MaxRune {
				return Glyph{}, fmt.Errorf("glyph: id %d beyond Unicode", f.v)
			}
			g.ID, hasID = rune(f.v), true
		case f.num == fieldGlyphBitmap && f.wire == wireBytes:
			g.BitmapLen = len(f.b)
		case f.num == fieldGlyphWidth && f.wire == wireVarint:
			g.Width = uint32(f.v)
		case f.num == fieldGlyphHeight && f.wire == wireVarint:
			g.Height = uint32(f.v)
		}
	}
	if !hasID {
		return Glyph{}, errors.New("glyph: no id")
	}
	return g, nil
}

// RangeFile is the {range}.pbf name holding code point r.
func RangeFile(r rune) string {
	start := int(r) / glyphRangeSize * glyphRangeSize
	return fmt.Sprintf("%d-%d.pbf", start, start+glyphRangeSize-1)
}

// ReadGlyphRange reads and decodes fonts/<stack>/<range>.pbf and checks
// that the file says what its path says.
func ReadGlyphRange(fontsDir, stack, file string) (*GlyphRange, error) {
	p := filepath.Join(fontsDir, stack, file)
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxGlyphFileBytes {
		return nil, fmt.Errorf("%s: %d bytes", p, st.Size())
	}
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return nil, err
	}
	gr, err := DecodeGlyphRange(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if gr.Name != stack {
		return nil, fmt.Errorf("%s: fontstack %q inside, want %q", p, gr.Name, stack)
	}
	if want := strings.TrimSuffix(file, ".pbf"); gr.Range != want {
		return nil, fmt.Errorf("%s: range %q inside, want %q", p, gr.Range, want)
	}
	lo, hi, err := parseRange(gr.Range)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	for id := range gr.Glyphs {
		if id < lo || id > hi {
			return nil, fmt.Errorf("%s: glyph U+%04X outside the range %s", p, id, gr.Range)
		}
	}
	return gr, nil
}

func parseRange(s string) (lo, hi rune, err error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("range %q: not start-end", s)
	}
	l, err1 := strconv.ParseUint(a, 10, 21)
	h, err2 := strconv.ParseUint(b, 10, 21)
	if err1 != nil || err2 != nil || h < l {
		return 0, 0, fmt.Errorf("range %q: not start-end", s)
	}
	return rune(l), rune(h), nil
}

// Block is a Unicode block the glyphs must cover.
type Block struct {
	Name     string
	First    rune
	Last     rune
	Required *unicode.RangeTable // the code points of the block that must draw
}

// GeorgianBlocks are the four Georgian blocks of WP-L3 and ui D7. The
// code points that must draw are those of the block that Go's Unicode
// tables give the Georgian script (reserved code points have no glyph to
// draw), so the list is derived, not typed.
var GeorgianBlocks = []Block{
	{Name: "Asomtavruli", First: 0x10A0, Last: 0x10CF, Required: unicode.Georgian},
	{Name: "Mkhedruli", First: 0x10D0, Last: 0x10FF, Required: unicode.Georgian},
	{Name: "Mtavruli", First: 0x1C90, Last: 0x1CBF, Required: unicode.Georgian},
	{Name: "Nuskhuri", First: 0x2D00, Last: 0x2D2F, Required: unicode.Georgian},
}

// BasicLatin is checked too: a fontstack that lost its Latin font would
// draw no English label.
var BasicLatin = Block{Name: "Basic Latin letters", First: 'A', Last: 'z', Required: unicode.Letter}

// RequiredRunes lists the code points of the block that must draw.
func (b Block) RequiredRunes() []rune {
	var rs []rune
	for r := b.First; r <= b.Last; r++ {
		if unicode.Is(b.Required, r) {
			rs = append(rs, r)
		}
	}
	return rs
}

// CheckBlock decodes the range files covering the block from
// fonts/<stack>/ and returns the code points that do not draw.
func CheckBlock(fontsDir, stack string, b Block) (missing []rune, err error) {
	files := map[string]*GlyphRange{}
	for _, r := range b.RequiredRunes() {
		name := RangeFile(r)
		gr, ok := files[name]
		if !ok {
			gr, err = ReadGlyphRange(fontsDir, stack, name)
			if err != nil {
				return nil, err
			}
			files[name] = gr
		}
		if g, ok := gr.Glyphs[r]; !ok || !g.Draws() {
			missing = append(missing, r)
		}
	}
	return missing, nil
}
