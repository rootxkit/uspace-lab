package basemap

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
)

// The PMTiles v3 layout, from the specification
// (github.com/protomaps/PMTiles spec/v3/spec.md §3.1). Pinned by
// TestHeaderMatchesReferenceTool against archives go-pmtiles wrote.
const (
	headerLen      = 127
	rootDirMaxLen  = 16384 // header plus root directory (§2)
	compressNone   = 1
	compressGzip   = 2
	maxDirEntries  = 1 << 24
	maxDirBytes    = 64 << 20
	maxMetaBytes   = 16 << 20
	maxLeafDepth   = 4
	pmtilesVersion = 3
)

// Header is the part of the PMTiles v3 header the checks read.
type Header struct {
	RootOffset, RootLength         uint64
	MetadataOffset, MetadataLength uint64
	LeafOffset, LeafLength         uint64
	TileDataOffset, TileDataLength uint64
	AddressedTiles                 uint64
	TileEntries                    uint64
	TileContents                   uint64
	Clustered                      bool
	InternalCompression            uint8
	TileCompression                uint8
	TileType                       uint8
	MinZoom, MaxZoom               uint8
	Bounds                         Box
	CenterZoom                     uint8
	CenterLon, CenterLat           float64
}

// ParseHeader decodes the 127-byte header.
func ParseHeader(b []byte) (Header, error) {
	if len(b) < headerLen {
		return Header{}, fmt.Errorf("pmtiles header: %d bytes, want %d", len(b), headerLen)
	}
	if string(b[0:7]) != "PMTiles" {
		return Header{}, errors.New("pmtiles header: no PMTiles magic number")
	}
	if b[7] != pmtilesVersion {
		return Header{}, fmt.Errorf("pmtiles header: version %d, want %d", b[7], pmtilesVersion)
	}
	u64 := func(off int) uint64 { return binary.LittleEndian.Uint64(b[off : off+8]) }
	e7 := func(off int) float64 { return float64(int32(binary.LittleEndian.Uint32(b[off:off+4]))) / 1e7 }
	h := Header{
		RootOffset:          u64(8),
		RootLength:          u64(16),
		MetadataOffset:      u64(24),
		MetadataLength:      u64(32),
		LeafOffset:          u64(40),
		LeafLength:          u64(48),
		TileDataOffset:      u64(56),
		TileDataLength:      u64(64),
		AddressedTiles:      u64(72),
		TileEntries:         u64(80),
		TileContents:        u64(88),
		Clustered:           b[96] == 1,
		InternalCompression: b[97],
		TileCompression:     b[98],
		TileType:            b[99],
		MinZoom:             b[100],
		MaxZoom:             b[101],
		Bounds:              Box{e7(102), e7(106), e7(110), e7(114)},
		CenterZoom:          b[118],
		CenterLon:           e7(119),
		CenterLat:           e7(123),
	}
	return h, nil
}

// Entry is one directory entry (§4.1). RunLength 0 is a leaf directory.
type Entry struct {
	TileID    uint64
	Offset    uint64
	Length    uint64
	RunLength uint32
}

// parseDirectory decodes an uncompressed directory (§4.2).
func parseDirectory(b []byte) ([]Entry, error) {
	r := bytes.NewReader(b)
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, fmt.Errorf("directory: entry count: %w", err)
	}
	if n == 0 || n > maxDirEntries {
		return nil, fmt.Errorf("directory: %d entries", n)
	}
	es := make([]Entry, n)
	var last uint64
	for i := range es {
		d, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("directory: tile id %d: %w", i, err)
		}
		last += d
		es[i].TileID = last
	}
	for i := range es {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("directory: run length %d: %w", i, err)
		}
		if v > math.MaxUint32 {
			return nil, fmt.Errorf("directory: run length %d: %d", i, v)
		}
		es[i].RunLength = uint32(v)
	}
	for i := range es {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("directory: length %d: %w", i, err)
		}
		if v == 0 {
			return nil, fmt.Errorf("directory: length %d is 0 (must be > 0)", i)
		}
		es[i].Length = v
	}
	for i := range es {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("directory: offset %d: %w", i, err)
		}
		if v == 0 && i > 0 {
			es[i].Offset = es[i-1].Offset + es[i-1].Length
		} else {
			if v == 0 {
				return nil, errors.New("directory: first offset encoded as contiguous")
			}
			es[i].Offset = v - 1
		}
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("directory: %d trailing bytes", r.Len())
	}
	return es, nil
}

func decompress(b []byte, c uint8, limit int64) ([]byte, error) {
	switch c {
	case compressNone:
		return b, nil
	case compressGzip:
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		out, err := io.ReadAll(io.LimitReader(zr, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(out)) > limit {
			return nil, fmt.Errorf("decompressed section over %d bytes", limit)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("internal compression %d not supported (want gzip or none)", c)
	}
}

// Archive is a local PMTiles file opened for its directory.
type Archive struct {
	Header   Header
	Metadata map[string]any
	f        *os.File
	size     int64
}

// OpenArchive reads the header and the metadata of a local archive.
func OpenArchive(path string) (*Archive, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	a := &Archive{f: f}
	if err := a.init(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

func (a *Archive) init() error {
	st, err := a.f.Stat()
	if err != nil {
		return err
	}
	a.size = st.Size()
	hb := make([]byte, headerLen)
	if _, err := io.ReadFull(a.f, hb); err != nil {
		return fmt.Errorf("pmtiles header: %w", err)
	}
	if a.Header, err = ParseHeader(hb); err != nil {
		return err
	}
	h := a.Header
	if h.RootOffset+h.RootLength > rootDirMaxLen {
		return fmt.Errorf("root directory ends at byte %d, beyond the first %d", h.RootOffset+h.RootLength, rootDirMaxLen)
	}
	mb, err := a.section(h.MetadataOffset, h.MetadataLength)
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	if mb, err = decompress(mb, h.InternalCompression, maxMetaBytes); err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	if err := json.Unmarshal(mb, &a.Metadata); err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	return nil
}

// Close closes the file.
func (a *Archive) Close() error { return a.f.Close() }

func (a *Archive) section(off, n uint64) ([]byte, error) {
	if n > maxDirBytes || off > uint64(a.size) || n > uint64(a.size)-off {
		return nil, fmt.Errorf("section %d+%d outside the %d-byte file", off, n, a.size)
	}
	b := make([]byte, n)
	if _, err := a.f.ReadAt(b, int64(off)); err != nil {
		return nil, err
	}
	return b, nil
}

func (a *Archive) directory(off, n uint64) ([]Entry, error) {
	b, err := a.section(off, n)
	if err != nil {
		return nil, err
	}
	if b, err = decompress(b, a.Header.InternalCompression, maxDirBytes); err != nil {
		return nil, err
	}
	return parseDirectory(b)
}

// Tiles calls fn for every tile entry (run lengths not expanded), root
// and leaf directories followed in order. Each entry's offset is
// checked against the tile data section.
func (a *Archive) Tiles(fn func(Entry) error) error {
	root, err := a.directory(a.Header.RootOffset, a.Header.RootLength)
	if err != nil {
		return fmt.Errorf("root directory: %w", err)
	}
	return a.walk(root, 0, fn)
}

func (a *Archive) walk(es []Entry, depth int, fn func(Entry) error) error {
	if depth > maxLeafDepth {
		return fmt.Errorf("leaf directories nested deeper than %d", maxLeafDepth)
	}
	h := a.Header
	for _, e := range es {
		if e.RunLength == 0 {
			if e.Offset > h.LeafLength || e.Length > h.LeafLength-e.Offset {
				return fmt.Errorf("leaf directory at %d+%d outside the leaf section", e.Offset, e.Length)
			}
			leaf, err := a.directory(h.LeafOffset+e.Offset, e.Length)
			if err != nil {
				return fmt.Errorf("leaf directory at %d: %w", e.Offset, err)
			}
			if err := a.walk(leaf, depth+1, fn); err != nil {
				return err
			}
			continue
		}
		if e.Offset > h.TileDataLength || e.Length > h.TileDataLength-e.Offset {
			return fmt.Errorf("tile %d at %d+%d outside the tile data section", e.TileID, e.Offset, e.Length)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// ZxyToID is the Hilbert tile id of §4.1: the tiles of every lower zoom
// counted, then the position on zoom z's Hilbert curve.
func ZxyToID(z uint8, x, y uint32) uint64 {
	acc := (uint64(1)<<(2*uint64(z)) - 1) / 3
	n := uint32(1) << z
	var d uint64
	for s := n / 2; s > 0; s /= 2 {
		var rx, ry uint32
		if x&s > 0 {
			rx = 1
		}
		if y&s > 0 {
			ry = 1
		}
		d += uint64(s) * uint64(s) * uint64((3*rx)^ry)
		x, y = hilbertRotate(n, x, y, rx, ry)
	}
	return acc + d
}

// IDToZxy inverts ZxyToID.
func IDToZxy(id uint64) (z uint8, x, y uint32) {
	z = uint8((bits.Len64(3*id+1) - 1) / 2)
	t := id - (uint64(1)<<(2*uint64(z))-1)/3
	n := uint32(1) << z
	for s := uint32(1); s < n; s *= 2 {
		rx := uint32(1 & (t / 2))
		ry := uint32(1 & (t ^ uint64(rx)))
		x, y = hilbertRotate(s, x, y, rx, ry)
		x += s * rx
		y += s * ry
		t /= 4
	}
	return z, x, y
}

func hilbertRotate(n, x, y, rx, ry uint32) (uint32, uint32) {
	if ry == 0 {
		if rx == 1 {
			x = n - 1 - x
			y = n - 1 - y
		}
		return y, x
	}
	return x, y
}

// TileBox is the WGS84 extent of a Web Mercator tile.
func TileBox(z uint8, x, y uint32) Box {
	n := math.Exp2(float64(z))
	lon := func(x float64) float64 { return x/n*360 - 180 }
	lat := func(y float64) float64 { return math.Atan(math.Sinh(math.Pi*(1-2*y/n))) * 180 / math.Pi }
	return Box{lon(float64(x)), lat(float64(y) + 1), lon(float64(x) + 1), lat(float64(y))}
}

// TileAt is the tile of zoom z that contains the point.
func TileAt(z uint8, lon, lat float64) (x, y uint32) {
	n := math.Exp2(float64(z))
	fx := (lon + 180) / 360 * n
	r := lat * math.Pi / 180
	fy := (1 - math.Asinh(math.Tan(r))/math.Pi) / 2 * n
	clamp := func(v float64) uint32 { return uint32(math.Min(math.Max(math.Floor(v), 0), n-1)) }
	return clamp(fx), clamp(fy)
}
