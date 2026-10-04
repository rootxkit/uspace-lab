package basemap

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

// The table of spec v3 §4.1, the specification's own vectors.
func TestZxyToIDSpecTable(t *testing.T) {
	cases := []struct {
		z    uint8
		x, y uint32
		id   uint64
	}{
		{0, 0, 0, 0}, {1, 0, 0, 1}, {1, 0, 1, 2}, {1, 1, 1, 3}, {1, 1, 0, 4}, {2, 0, 0, 5},
		{12, 3423, 1763, 19078479},
	}
	for _, c := range cases {
		if got := ZxyToID(c.z, c.x, c.y); got != c.id {
			t.Errorf("ZxyToID(%d,%d,%d) = %d, spec says %d", c.z, c.x, c.y, got, c.id)
		}
		z, x, y := IDToZxy(c.id)
		if z != c.z || x != c.x || y != c.y {
			t.Errorf("IDToZxy(%d) = %d/%d/%d, spec says %d/%d/%d", c.id, z, x, y, c.z, c.x, c.y)
		}
	}
}

func TestTileIDRoundTripAndOrder(t *testing.T) {
	for z := uint8(0); z <= 7; z++ {
		n := uint32(1) << z
		seen := map[uint64]bool{}
		first := (uint64(1)<<(2*uint64(z)) - 1) / 3
		for x := uint32(0); x < n; x++ {
			for y := uint32(0); y < n; y++ {
				id := ZxyToID(z, x, y)
				if id < first || id >= first+uint64(n)*uint64(n) {
					t.Fatalf("z%d: id %d outside the zoom's range", z, id)
				}
				if seen[id] {
					t.Fatalf("z%d: id %d twice", z, id)
				}
				seen[id] = true
				if gz, gx, gy := IDToZxy(id); gz != z || gx != x || gy != y {
					t.Fatalf("round trip %d/%d/%d -> %d -> %d/%d/%d", z, x, y, id, gz, gx, gy)
				}
			}
		}
	}
	// Deep zooms too: the city tiles are z15.
	for _, c := range [][3]uint32{{15, 20450, 12110}, {18, 163000, 97000}} {
		id := ZxyToID(uint8(c[0]), c[1], c[2])
		if z, x, y := IDToZxy(id); uint32(z) != c[0] || x != c[1] || y != c[2] {
			t.Errorf("round trip %v -> %d -> %d/%d/%d", c, id, z, x, y)
		}
	}
}

func TestTileAtIsInsideTileBox(t *testing.T) {
	for _, p := range [][2]float64{{44.80, 41.70}, {41.64, 41.64}, {-122.4, 37.8}, {0, 0}} {
		for z := uint8(0); z <= 15; z++ {
			x, y := TileAt(z, p[0], p[1])
			b := TileBox(z, x, y)
			if p[0] < b.MinLon() || p[0] > b.MaxLon() || p[1] < b.MinLat() || p[1] > b.MaxLat() {
				t.Errorf("z%d %v: tile %d/%d box %v does not contain it", z, p, x, y, b)
			}
		}
	}
	// The spec vector's tile, by geography: z12 3423/1763 is in Japan.
	b := TileBox(12, 3423, 1763)
	if b.MinLon() < 120 || b.MaxLon() > 121 || b.MinLat() < 24 || b.MaxLat() > 25 {
		t.Errorf("TileBox(12,3423,1763) = %v", b)
	}
}

// The header and the directories as go-pmtiles wrote them
// (testdata/README.md records what its `show` printed and the offsets
// Python's struct read independently).
func TestHeaderMatchesReferenceTool(t *testing.T) {
	a, err := OpenArchive("testdata/fixture.pmtiles")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Header
	want := Header{
		RootOffset: 127, RootLength: 62, MetadataOffset: 189, MetadataLength: 1178,
		LeafOffset: 1367, LeafLength: 0, TileDataOffset: 1367, TileDataLength: 792,
		AddressedTiles: 9, TileEntries: 6, TileContents: 5, Clustered: true,
		InternalCompression: compressGzip, TileCompression: compressGzip, TileType: 1,
		MinZoom: 11, MaxZoom: 14, Bounds: Box{35, 43, 35.06, 43.04},
		CenterZoom: 11, CenterLon: 35.03, CenterLat: 43.0199999,
	}
	if h != want {
		t.Errorf("header\n got %+v\nwant %+v", h, want)
	}
	if got := a.Metadata[osmReplicationKey]; got != "2026-10-03T04:00:00Z" {
		t.Errorf("metadata %s = %v", osmReplicationKey, got)
	}
	entries, addressed := 0, 0
	contents := map[uint64]bool{}
	var lastID uint64
	err = a.Tiles(func(e Entry) error {
		if entries > 0 && e.TileID <= lastID {
			t.Errorf("tile ids not ascending: %d after %d", e.TileID, lastID)
		}
		lastID = e.TileID
		entries++
		addressed += int(e.RunLength)
		contents[e.Offset] = true
		z, _, _ := IDToZxy(e.TileID)
		if z < h.MinZoom || z > h.MaxZoom {
			t.Errorf("tile %d at z%d outside z%d-%d", e.TileID, z, h.MinZoom, h.MaxZoom)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if uint64(entries) != h.TileEntries || uint64(addressed) != h.AddressedTiles || uint64(len(contents)) != h.TileContents {
		t.Errorf("directory: %d entries, %d addressed, %d contents; the header (go-pmtiles) says %d, %d, %d",
			entries, addressed, len(contents), h.TileEntries, h.AddressedTiles, h.TileContents)
	}
}

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func uvarints(vs ...uint64) []byte {
	var out []byte
	for _, v := range vs {
		out = binary.AppendUvarint(out, v)
	}
	return out
}

func TestParseDirectoryContiguousOffsets(t *testing.T) {
	// Spec §4.2: three entries, ids 5, 42, 69; the second offset is
	// "contiguous" (encoded 0), the third explicit (offset 1000 -> 1001).
	b := uvarints(3, 5, 37, 27, 1, 1, 0, 10, 20, 30, 1, 0, 1001)
	es, err := parseDirectory(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{5, 0, 10, 1}, {42, 10, 20, 1}, {69, 1000, 30, 0}}
	for i := range want {
		if es[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, es[i], want[i])
		}
	}
	for name, bad := range map[string][]byte{
		"no entries":        uvarints(0),
		"zero length":       uvarints(1, 5, 1, 0, 1),
		"first contiguous":  uvarints(1, 5, 1, 10, 0),
		"trailing bytes":    append(uvarints(1, 5, 1, 10, 1), 7),
		"truncated offsets": uvarints(2, 5, 1, 1, 1, 10, 10, 1),
	} {
		if _, err := parseDirectory(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOpenArchiveRefusesBrokenFiles(t *testing.T) {
	good, err := os.ReadFile("testdata/fixture.pmtiles")
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(f func(b []byte) []byte) string {
		b := f(append([]byte(nil), good...))
		p := t.TempDir() + "/x.pmtiles"
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]struct {
		f    func(b []byte) []byte
		want string
	}{
		"magic":   {func(b []byte) []byte { b[0] = 'X'; return b }, "magic"},
		"version": {func(b []byte) []byte { b[7] = 2; return b }, "version"},
		"short":   {func(b []byte) []byte { return b[:100] }, "header"},
		"root beyond": {func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[8:], rootDirMaxLen)
			return b
		}, "root directory"},
		"metadata outside": {func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[32:], 1<<20)
			return b
		}, "metadata"},
		"compression": {func(b []byte) []byte { b[97] = 3; return b }, "compression"},
	}
	for name, c := range cases {
		_, err := OpenArchive(mutate(c.f))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want it to mention %q", name, err, c.want)
		}
	}
	// A tile entry pointing past the tile data section.
	p := mutate(func(b []byte) []byte {
		binary.LittleEndian.PutUint64(b[64:], 10) // tile data length 10
		return b
	})
	a, err := OpenArchive(p)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Tiles(func(Entry) error { return nil }); err == nil || !strings.Contains(err.Error(), "outside the tile data") {
		t.Errorf("tile outside the data section: %v", err)
	}
}

func TestDecompressLimit(t *testing.T) {
	if _, err := decompress(gz(t, make([]byte, 100)), compressGzip, 50); err == nil {
		t.Error("a section over the limit was accepted")
	}
	b, err := decompress(gz(t, []byte("abc")), compressGzip, 50)
	if err != nil || string(b) != "abc" {
		t.Errorf("gzip: %q %v", b, err)
	}
	if b, err := decompress([]byte("abc"), compressNone, 50); err != nil || string(b) != "abc" {
		t.Errorf("none: %q %v", b, err)
	}
}
