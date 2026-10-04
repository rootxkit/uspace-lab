# basemap: the self-hosted map bundle

WP-L3 (`docs/WORKPACKAGES/WP-L3.md`, decision record M38). Every console
draws its map from one bundle served at `/basemap/` by the deployment's
Caddy from a shared read-only volume; no console asks a third-party tile
or font server for anything (spec `06 §4`). This directory builds that
bundle, checks it, and publishes it as a release asset of this repo.

| File | What |
|---|---|
| `regions.yaml` | the policy: Georgia's bounds to `max_zoom` 12, Tbilisi, Kutaisi, Batumi and Poti to 15, the Storybook box. Every coordinate and zoom lives here (INV-03). |
| `inputs.yaml` | every downloaded file pinned by commit and SHA-256, with its licence and source: Noto Sans and Noto Sans Georgian TTFs, the OFL, the Protomaps v4 sprites and their MIT licence; the fontstacks; where the Protomaps builds are listed. |
| `tools.env` | the tool pins: go-pmtiles, font-maker (by commit), the Caddy image verify.sh serves with. |
| `budget.txt` | the size budgets: 1 GB for the bundle (owner's L-Q8 default), 10 MB for the Storybook extract. |
| `build.sh OUT_DIR [BUILD]` | builds the bundle. |
| `verify.sh OUT_DIR` | checks it; exits non-zero on any failure. |
| `storybook.sh OUT_DIR [BUILD]` | the Tbilisi-only extract for uspace-ui, built and verified the same way. |
| `package.sh OUT_DIR ASSET.tar.gz` | the reproducible release asset and its `.sha256`. |
| `tools.sh DIR` | installs go-pmtiles and compiles font-maker (build.sh calls it). |

The Go half is `cmd/basemap` (`internal/basemap`): it plans the extracts,
fetches and checks the inputs, writes `SOURCE.json` and runs the checks.

## The bundle

The layout uspace-ui reads (its PLAN §6.3, `BasemapConfig`):

```
basemap.pmtiles                     one PMTiles v3 archive, read with range requests
SOURCE.json                         bounds, osm_data_as_of, and where every byte came from
fonts/<fontstack>/<range>.pbf       "Noto Sans Regular" (the kit's mapFontstack), "Noto Sans Medium", "Noto Sans Italic"
fonts/OFL.txt                       the font licence
sprites/v4/{light,dark}{,@2x}.{json,png}, sprites/v4/LICENSE.md
```

**Tiles.** A Protomaps daily build (OpenStreetMap, ODbL) cut with
`go-pmtiles extract`. The tool takes one max zoom per run, so the policy
is several extracts: the country from zoom 0 to 12 over its bounds, then
one extract per zoom above 12 over a GeoJSON MultiPolygon of the city
boxes that reach that zoom. They are disjoint by zoom, which is what
`pmtiles merge` requires; the merged file is checked with `pmtiles
verify`. A tile is kept when it overlaps a box, so the edges of a city
box are rounded out to whole tiles.

**Glyphs.** font-maker draws each fontstack from Noto Sans first and Noto
Sans Georgian for what Noto Sans lacks, which is how basemaps-assets
makes its own; the pins are in `inputs.yaml`. Noto Sans Georgian has no
italic, so the Italic stack (water and park labels) uses its Regular:
basemaps-assets' Italic stack has no Georgian at all, which leaves
river names in Georgian blank. Only Latin, Greek, Cyrillic and Georgian
draw: a label in Armenian or Arabic script loses those characters.

**SOURCE.json** carries `source`, `build`, `upstream` (the planet file's
size and BLAKE3 from the builds list: a 138 GB file is not re-hashed
here), `bounds` (TileJSON order), `regions`, `osm_data_as_of`,
`archive` (its SHA-256 and tiles per zoom), `fonts` (each font's version
read from its name table, SHA-256 and licence), `assets`, `inputs`,
`tools` (go-pmtiles, font-maker, Go and this repo's commit), `fetched_at`
and `licence`.

## Build

On Linux with Go, git, curl, cmake, clang and the FreeType and Boost
headers (`apt-get install cmake clang libfreetype-dev libboost-dev`):

```sh
basemap/build.sh local/basemap 20261003    # or make basemap BUILD=20261003
basemap/verify.sh local/basemap            # or make basemap-verify
```

On Windows or macOS, run build.sh in the Go image and verify.sh on the
host (it needs `caddy` or Docker for the range request):

```sh
docker run --rm -v "$PWD:/src" -v "$PWD/local:/out" -w /src \
  golang:1.27.1-bookworm bash -c \
  'apt-get update -qq && apt-get install -y -qq cmake clang libfreetype-dev libboost-dev && basemap/build.sh /out/basemap 20261003'
```

The first run compiles font-maker (about two and a half minutes) into
`basemap/.tools/`; later runs reuse it.

## First build, 2026-10-04

Protomaps build `20261003` (OSM data as of 2026-10-03T04:00:00Z),
go-pmtiles v1.31.2, font-maker `714ccaea67d3`, Go 1.27.1, this repo at
`16963b479d17`, built from a fresh clone in `golang:1.27.1-bookworm` on
the authoring machine and verified on the host:

| | |
|---|---|
| Build time | 49 s with the tools already compiled (the four extracts about 30 s); compiling font-maker the first time adds about two and a half minutes |
| Downloaded | 81 MB of tiles (57 + 3.6 + 6.7 + 14 MB for the four extracts) and 1.4 MB of fonts and sprites |
| Bundle | 81,854,246 bytes (81.9 MB): `basemap.pmtiles` 77,834,384, `fonts/` 3,904,292, `sprites/` 104,817, `SOURCE.json` 10,753 |
| Release asset | `basemap-20261003.tar.gz`, 80,201,664 bytes; packing the same bundle twice gave the same SHA-256 |
| Tiles | 6,859: z0-12 4,473 over Georgia, z13 134, z14 474, z15 1,778 (Tbilisi 1,073, Kutaisi 272, Batumi 289, Poti 144) |
| Storybook extract | 7,363,449 bytes (7.4 MB): 39 tiles, 3.3 MB of `basemap.pmtiles`, the same fonts and sprites |

An earlier build of the same date from the working tree gave a
`basemap.pmtiles` with the same SHA-256
(`f7656ba8df4c298b69257154d7b97a46d07880af007f09431c990f40218df39a`):
the extract is deterministic for a build date; only `SOURCE.json`
(`fetched_at`, the commit) and so the asset's hash differ.

The predecessor's Georgia extract at zoom 0-15 everywhere was 333 MB;
the city policy keeps the bundle at about a quarter of that and well
under the 1 GB budget, so the droplet's disk is not the constraint.

`verify.sh` on it:

```
ok   size within budget
       81854246 bytes (81.9 MB), budget 1000000000 bytes (1000.0 MB)
ok   glyphs: the four Georgian blocks and Basic Latin decoded from every fontstack
       Noto Sans Regular: Asomtavruli 40/40, Mkhedruli 47/47, Mtavruli 46/46, Nuskhuri 40/40, Basic Latin letters 52/52
       Noto Sans Medium: Asomtavruli 40/40, Mkhedruli 47/47, Mtavruli 46/46, Nuskhuri 40/40, Basic Latin letters 52/52
       Noto Sans Italic: Asomtavruli 40/40, Mkhedruli 47/47, Mtavruli 46/46, Nuskhuri 40/40, Basic Latin letters 52/52
ok   tiles: zoom policy over every tile
       6859 tiles (z0:1 z1:1 z2:1 z3:2 z4:2 z5:2 z6:2 z7:6 z8:18 z9:66 z10:231 z11:861 z12:3280 z13:134 z14:474 z15:1778)
       tbilisi: 1073 tiles at z15, centre tile 15/20466/12196 present
       kutaisi: 272 tiles at z15, centre tile 15/20269/12132 present
       batumi: 289 tiles at z15, centre tile 15/20173/12209 present
       poti: 144 tiles at z15, centre tile 15/20177/12146 present
ok   SOURCE.json complete and consistent with the files
       build 20261003, OSM data as of 2026-10-03T04:00:00Z, 15 inputs (10 shipped files hashed), archive sha256 f7656ba8df4c298b69257154d7b97a46d07880af007f09431c990f40218df39a
ok   range request answered with 206 by the file server
       GET http://127.0.0.1:18780/basemap.pmtiles Range: bytes=0-126 -> 206 Partial Content, Content-Range "bytes 0-126/77834384"
verify: 5 checks, 0 failed
```

The earlier build (same archive) with `fonts/Noto Sans Medium/7168-7423.pbf`
deleted:

```
FAIL glyphs: the four Georgian blocks and Basic Latin decoded from every fontstack
       ...
       Noto Sans Medium: Mtavruli: GetFileAttributesEx ...\fonts\Noto Sans Medium\7168-7423.pbf: The system cannot find the file specified.
verify: 5 checks, 1 failed
exit 1
```

and with a 50 MB budget:

```
FAIL size within budget
       81854213 bytes (81.9 MB), budget 50000000 bytes (50.0 MB)
       over budget by 31854213 bytes
verify: 5 checks, 1 failed
exit 1
```

A glyph removed from inside a range file (rather than the file) fails
with the code points named, e.g. `Mtavruli (U+1C90-1CBF): 2 of 46 code
points do not draw: U+1C90 U+1CBF`; `internal/basemap` tests that, and
CI runs verify.sh itself over a fixture both ways (`basemap.yml`).

## How the droplet gets it

`release.yml` builds, verifies and packs the bundle on a tag
`basemap-vYYYYMMDD` (the date is the Protomaps build) and publishes:

```
https://github.com/rootxkit/uspace-lab/releases/download/basemap-vYYYYMMDD/basemap-YYYYMMDD.tar.gz
https://github.com/rootxkit/uspace-lab/releases/download/basemap-vYYYYMMDD/basemap-YYYYMMDD.tar.gz.sha256
https://github.com/rootxkit/uspace-lab/releases/download/basemap-vYYYYMMDD/SOURCE.json
```

The repository is public, so no token is needed. uspace-deploy WP-D1
(`basemap/pull.sh`) downloads the asset, checks it with `sha256sum -c`
against the `.sha256` (or a hash pinned in its own config), unpacks it
into a new directory of the shared volume, swaps it in atomically and
prints `SOURCE.json`. The archive holds the bundle's files at its root.
A release is never replaced: a rebuild would change `fetched_at` and so
the hash under a URL someone has pinned. Refresh with a new date.

## Refreshing, and why the date matters

An offline map has no other way to say it is stale: the date in the
attribution line (`osm_data_as_of`) is the only sign that a road built
last month is missing. To refresh, tag the newest Protomaps build
(`build-metadata.protomaps.dev/builds.json` lists them; old ones are
pruned upstream, so a date more than a few weeks old may be gone):

```sh
git tag basemap-v20261003 && git push origin basemap-v20261003
```

then point uspace-deploy at the new URL and hash. Changing a city box or
a zoom is an edit to `regions.yaml`; changing a font or sprite is a new
pin in `inputs.yaml` (the fetch refuses a file whose hash or font
version differs from its pin); bumping a tool is `tools.env`. Each is
recorded in the next `SOURCE.json`.

## Not here

Terrain (Copernicus GLO-30) and the geoid are fetched by the scenarios
that need them (L-Q7); nothing about them is built or released here.
