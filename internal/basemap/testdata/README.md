# basemap test fixtures

Written by the reference tool, not by this package, so that the PMTiles
reader is pinned against an independent writer (E-03). Open sea in the
Black Sea: the tiles are a few dozen bytes each.

| File | Made with |
|---|---|
| `fixture-country.pmtiles` | `go-pmtiles extract https://build.protomaps.com/20261003.pmtiles fixture-country.pmtiles --bbox=35.00,43.00,35.06,43.04 --minzoom=11 --maxzoom=12` |
| `fixture.pmtiles` | `fixture-country.pmtiles` merged with two extracts over the city box `35.02,43.01,35.04,43.03` (`--region` a MultiPolygon of that box, `--minzoom=13 --maxzoom=13`, then `14`): `go-pmtiles merge fixture-country.pmtiles c13.pmtiles c14.pmtiles fixture.pmtiles` |
| `regions.yaml` | the policy those extracts follow (country to z12, the city box to z14) |

go-pmtiles v1.31.2. `go-pmtiles show fixture.pmtiles` printed: min zoom
11, max zoom 14, bounds 35,43 to 35.06,43.04, center 35.03,43.02 z11,
9 addressed tiles, 6 tile entries, 5 tile contents, clustered, gzip
internal and tile compression, OSM replication time
2026-10-03T04:00:00Z. The header offsets read independently with
Python's `struct.unpack_from('<11Q', b, 8)`: 127, 62, 189, 1178, 1367,
0, 1367, 792, 9, 6, 5.

Map data © OpenStreetMap contributors, ODbL 1.0.
