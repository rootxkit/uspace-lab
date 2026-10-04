// Package basemap builds and checks the self-hosted basemap bundle
// (docs/WORKPACKAGES/WP-L3.md, decision record M38): the region policy
// read from basemap/regions.yaml (no coordinate or zoom in code,
// INV-03), the pinned inputs of basemap/inputs.yaml fetched and checked
// against their SHA-256, a reader for the PMTiles v3 directory (spec
// v3, pinned against archives the reference tool wrote), a decoder for
// MapLibre glyph PBFs, SOURCE.json, and the checks verify.sh runs:
// size budget, the four Georgian blocks decoded from the glyphs, the
// zoom policy over every tile, SOURCE.json completeness and a range
// request answered with 206.
//
// The tiles themselves are cut and merged by the pinned go-pmtiles CLI
// and the glyphs rendered by the pinned font-maker; this package plans
// that work and checks the result, it does not write archives.
package basemap
