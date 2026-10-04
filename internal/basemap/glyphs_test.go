package basemap

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pbBytes and pbVarint encode the glyph schema the way font-maker's
// protozero writer does (field key, then value).
func pbBytes(field int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(field)<<3|wireBytes)
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}

func pbVarint(field int, v uint64) []byte {
	out := binary.AppendUvarint(nil, uint64(field)<<3|wireVarint)
	return binary.AppendUvarint(out, v)
}

func pbSint(field int, v int64) []byte {
	return pbVarint(field, uint64((v<<1)^(v>>63)))
}

// encodeRange writes one range file holding the given code points, each
// with a small bitmap, in font-maker's field order (3, 4, 5, 1, 6, 7, 2).
func encodeRange(stack, rng string, runes []rune) []byte {
	var fs []byte
	fs = append(fs, pbBytes(fieldStackName, []byte(stack))...)
	fs = append(fs, pbBytes(fieldStackRange, []byte(rng))...)
	for _, r := range runes {
		var g []byte
		g = append(g, pbVarint(3, 10)...)
		g = append(g, pbVarint(4, 12)...)
		g = append(g, pbSint(5, -1)...)
		g = append(g, pbVarint(1, uint64(r))...)
		g = append(g, pbSint(6, -20)...)
		g = append(g, pbVarint(7, 11)...)
		g = append(g, pbBytes(2, make([]byte, 16*18))...)
		fs = append(fs, pbBytes(fieldStackGlyph, g)...)
	}
	return pbBytes(fieldGlyphsStack, fs)
}

// writeGlyphs writes the range files of a fontstack covering every
// required code point of the Georgian blocks and Basic Latin, except
// those in drop.
func writeGlyphs(t *testing.T, fontsDir, stack string, drop map[rune]bool) {
	t.Helper()
	byFile := map[string][]rune{}
	blocks := append(append([]Block{}, GeorgianBlocks...), BasicLatin)
	for _, b := range blocks {
		for _, r := range b.RequiredRunes() {
			if !drop[r] {
				byFile[RangeFile(r)] = append(byFile[RangeFile(r)], r)
			}
		}
	}
	// A range file always exists for every block, as font-maker writes all
	// 256 ranges; an empty one holds no glyph.
	for _, b := range blocks {
		if _, ok := byFile[RangeFile(b.First)]; !ok {
			byFile[RangeFile(b.First)] = nil
		}
	}
	dir := filepath.Join(fontsDir, stack)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, rs := range byFile {
		b := encodeRange(stack, strings.TrimSuffix(name, ".pbf"), rs)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The code points that must draw, from Go's Unicode tables, pinned so
// that a table change is seen: reserved code points excluded, U+10FB
// (Georgian paragraph separator, script Common) excluded.
func TestGeorgianRequiredRunes(t *testing.T) {
	want := map[string]int{"Asomtavruli": 40, "Mkhedruli": 47, "Mtavruli": 46, "Nuskhuri": 40}
	for _, b := range GeorgianBlocks {
		rs := b.RequiredRunes()
		if len(rs) != want[b.Name] {
			t.Errorf("%s: %d required code points, want %d", b.Name, len(rs), want[b.Name])
		}
		for _, r := range rs {
			if r < b.First || r > b.Last {
				t.Errorf("%s: U+%04X outside the block", b.Name, r)
			}
		}
	}
	if n := len(BasicLatin.RequiredRunes()); n != 52 {
		t.Errorf("Basic Latin letters: %d, want 52", n)
	}
}

func TestRangeFile(t *testing.T) {
	for r, want := range map[rune]string{0x41: "0-255.pbf", 0x10D0: "4096-4351.pbf", 0x1C90: "7168-7423.pbf", 0x2D00: "11520-11775.pbf"} {
		if got := RangeFile(r); got != want {
			t.Errorf("RangeFile(U+%04X) = %s, want %s", r, got, want)
		}
	}
}

// A range file font-maker wrote (testdata/README.md): the decoder is
// pinned against the reference writer, not only against encodeRange.
func TestDecodeFontMakerOutput(t *testing.T) {
	gr, err := ReadGlyphRange("testdata/fonts", "Noto Sans Regular", "11520-11775.pbf")
	if err != nil {
		t.Fatal(err)
	}
	if gr.Name != "Noto Sans Regular" || gr.Range != "11520-11775" {
		t.Errorf("name %q range %q", gr.Name, gr.Range)
	}
	missing, err := CheckBlock("testdata/fonts", "Noto Sans Regular", GeorgianBlocks[3])
	if err != nil || len(missing) != 0 {
		t.Errorf("Nuskhuri from Noto Sans Georgian: missing %U, %v", missing, err)
	}
	g := gr.Glyphs[0x2D00]
	// font-maker renders at 24 px with a 3 px buffer: the SDF bitmap is
	// (width+6) x (height+6) bytes.
	if int(g.Width+6)*int(g.Height+6) != g.BitmapLen {
		t.Errorf("U+2D00: %dx%d with a %d-byte bitmap", g.Width, g.Height, g.BitmapLen)
	}
}

func TestDecodeGlyphRange(t *testing.T) {
	b := encodeRange("Noto Sans Regular", "11520-11775", []rune{0x2D00, 0x2D01})
	gr, err := DecodeGlyphRange(b)
	if err != nil {
		t.Fatal(err)
	}
	if gr.Name != "Noto Sans Regular" || gr.Range != "11520-11775" || len(gr.Glyphs) != 2 {
		t.Fatalf("%+v", gr)
	}
	g := gr.Glyphs[0x2D00]
	if !g.Draws() || g.Width != 10 || g.Height != 12 || g.BitmapLen != 288 {
		t.Errorf("glyph %+v", g)
	}
	// A glyph without a bitmap (a space, or an empty outline) does not draw.
	noBitmap := pbBytes(fieldGlyphsStack, append(append(pbBytes(1, []byte("s")), pbBytes(2, []byte("0-255"))...),
		pbBytes(3, append(pbVarint(1, 32), pbVarint(3, 0)...))...))
	gr, err = DecodeGlyphRange(noBitmap)
	if err != nil {
		t.Fatal(err)
	}
	if gr.Glyphs[32].Draws() {
		t.Error("a glyph with no bitmap draws")
	}
	for name, bad := range map[string][]byte{
		"two stacks": append(b, b...),
		"truncated":  b[:len(b)-3],
		"no id":      pbBytes(1, pbBytes(3, pbVarint(3, 1))),
	} {
		if _, err := DecodeGlyphRange(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCheckBlockPresenceAndAbsence(t *testing.T) {
	fonts := t.TempDir()
	writeGlyphs(t, fonts, "Full", nil)
	for _, b := range append(append([]Block{}, GeorgianBlocks...), BasicLatin) {
		missing, err := CheckBlock(fonts, "Full", b)
		if err != nil || len(missing) != 0 {
			t.Errorf("Full: %s: missing %v, err %v", b.Name, missing, err)
		}
	}
	writeGlyphs(t, fonts, "NoNuskhuri", map[rune]bool{0x2D00: true, 0x2D2D: true})
	missing, err := CheckBlock(fonts, "NoNuskhuri", GeorgianBlocks[3])
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(missing) != fmt.Sprint([]rune{0x2D00, 0x2D2D}) {
		t.Errorf("missing %U, want U+2D00 U+2D2D", missing)
	}
	// The range file itself removed: an error, not a silent pass.
	if err := os.Remove(filepath.Join(fonts, "Full", "11520-11775.pbf")); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckBlock(fonts, "Full", GeorgianBlocks[3]); err == nil {
		t.Error("a missing range file passed")
	}
}

func TestReadGlyphRangeChecksWhatTheFileSays(t *testing.T) {
	dir := t.TempDir()
	stack := filepath.Join(dir, "S")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(stack, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A Nuskhuri file renamed to the Mkhedruli range: the name is not
	// trusted, the range inside is.
	write("4096-4351.pbf", encodeRange("S", "11520-11775", []rune{0x2D00}))
	if _, err := ReadGlyphRange(dir, "S", "4096-4351.pbf"); err == nil || !strings.Contains(err.Error(), "range") {
		t.Errorf("renamed range file: %v", err)
	}
	write("0-255.pbf", encodeRange("Other", "0-255", []rune{'A'}))
	if _, err := ReadGlyphRange(dir, "S", "0-255.pbf"); err == nil || !strings.Contains(err.Error(), "fontstack") {
		t.Errorf("another fontstack's file: %v", err)
	}
	write("256-511.pbf", encodeRange("S", "256-511", []rune{'A'}))
	if _, err := ReadGlyphRange(dir, "S", "256-511.pbf"); err == nil || !strings.Contains(err.Error(), "outside the range") {
		t.Errorf("a glyph outside its range: %v", err)
	}
	write("512-767.pbf", encodeRange("S", "512-767", []rune{600}))
	if _, err := ReadGlyphRange(dir, "S", "512-767.pbf"); err != nil {
		t.Errorf("a good file: %v", err)
	}
}
