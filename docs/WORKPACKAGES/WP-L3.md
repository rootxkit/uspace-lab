# WP-L3: `basemap-bundle`

Branch `feat/WP-L3-basemap-bundle`. Needed by ui WP-3 (the Storybook
extract), cisp WP-10 (the public map) and every console (decision record
M38). Owns `basemap/` (`build.sh`, `verify.sh`, `budget.txt`,
`README.md`) and the `basemap` job of `release.yml`. Depends on nothing
in this repo. Consumers: `uspace-ui` (the `/basemap/` layout and the
fontstack name), `docs/deploy/PLAN.md` WP-D1 (the shared volume).

## Read first

1. `docs/PLAN.md §1` D7, `§7.1` L-Q8 (size budget: a default, not a
   decision).
2. `docs/decisions/2026-10-02-cross-plan.md`: M38, ui Q4 and Q8 in
   §2.2 (Georgia-wide extract with city-level zooms for Tbilisi,
   Kutaisi, Batumi and Poti, z ≤ 12 elsewhere, about 1 GB; Noto Sans +
   Noto Sans Georgian; the lab builds, the deployment serves from one
   shared read-only volume).
3. `uspace-ui` plan: D6, D7, `§6.3` (the bundle layout:
   `/basemap/basemap.pmtiles`, `/basemap/SOURCE.json` with `bounds` and
   `osm_data_as_of`, `/basemap/fonts/{fontstack}/{range}.pbf`,
   `/basemap/sprites/v4/{light,dark}.*`), `§3` `mapFontstack`,
   `BasemapInfo`, WP-3 (the Storybook extract: Tbilisi centre, a few
   MB, committed there).
4. Predecessor `utm/infra/basemap/fetch_basemap.sh` and
   `docs/runbooks/p1-12-basemap.md` (the reference: Protomaps daily
   build, `pmtiles extract --bbox`, the basemaps-assets fonts and
   sprites, `SOURCE.json`; the 2026-09-28 result: 333 MB for Georgia at
   z 0–15).
5. Protomaps docs at the time of building: `pmtiles extract` options
   (`--bbox`, `--maxzoom`, and whether per-region max zoom needs several
   extracts merged or a second file), the glyph build tool for custom
   font stacks (`font-maker` or the current equivalent) and the Noto
   Sans Georgian source. Verify, do not assume (E-04).
6. Spec `06 §4` (consoles must not reach a third-party tile or font
   server), LESSONS E-02 (a bundle that loads is a visible state; a
   missing glyph range is checked, not assumed).

## What to build

- `basemap/build.sh OUT_DIR [BUILD]`: bbox and zoom policy from
  `basemap/regions.yaml` (Georgia bounds; the four city boxes with their
  max zoom; the countrywide max zoom), never from the script (INV-03).
  Produces `basemap.pmtiles` (one file; if the tool cannot vary max
  zoom by region in one extract, document the merge it does and pin
  the tool version), `fonts/<fontstack>/<range>.pbf` built from Noto
  Sans and Noto Sans Georgian so that every Georgian block (Mkhedruli
  U+10D0–10FF, Mtavruli U+1C90–1CBF, Nuskhuri U+2D00–2D2F, Asomtavruli
  U+10A0–10CF) has glyphs, `sprites/v4/`, and `SOURCE.json` (`source`,
  `build`, `bounds`, `regions`, `osm_data_as_of`, `fonts` with the font
  versions and licence, `assets` commit, `fetched_at`, `licence`).
- `basemap/verify.sh OUT_DIR`: total size under `budget.txt` (default
  1 GB; fails over it), the four Georgian ranges present in the glyph
  PBFs (decode the range files and assert the code points, not the
  file names), a tile exists at the city max zoom inside each city box
  and none above z 12 outside them, `SOURCE.json` complete, and a
  range request against a local `file_server` returns 206.
- `basemap/storybook.sh`: the Tbilisi-only extract (a few MB) with the
  same fonts, for `uspace-ui/stories/basemap/`; its size printed.
- `release.yml` job `basemap`: on a tag `basemap-vYYYYMMDD`, builds,
  verifies and uploads the bundle as a release asset with its SHA-256;
  the release notes carry `SOURCE.json`. Not on every push (the
  download is large; CI stays lean).
- `basemap/README.md`: how to build, how long it takes, the measured
  size, how the droplet gets it (WP-D1 pulls the release asset into the
  shared volume), how to refresh and why the date matters (an offline
  map has no other way to say it is stale).

## Done when

- [ ] A bundle built on the authoring machine: measured size, build
  time and the `verify.sh` output pasted into the PR (E-05: tool
  versions in `SOURCE.json`).
- [ ] `verify.sh` fails when a Georgian range is removed from the fonts
  and when the budget is exceeded (prove both with a throwaway run and
  paste the output, E-01).
- [ ] The Storybook extract handed to `uspace-ui` WP-3 (PR there, or
  the file attached to an issue) and its Georgian labels seen in a
  story.
- [ ] One release `basemap-v<date>` published with the asset and its
  hash; `docs/deploy/PLAN.md` WP-D1 told which URL to pull.

## Commits

`feat(basemap): build the Georgia PMTiles bundle with Georgian glyphs from a region policy   [WP-L3]`,
`feat(basemap): verify size, glyph ranges, zoom policy and range requests   [WP-L3]`,
`feat(basemap): build the Tbilisi-only Storybook extract   [WP-L3]`,
`ci: publish the basemap bundle as a release asset on basemap tags   [WP-L3]`,
`docs(basemap): record the first build and how to refresh it   [WP-L3]`.
