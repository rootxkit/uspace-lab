# Backlog

Ranked table of open items. Source is not recorded in the list this was built from.

| # | Severity (high/medium/low) | Item | Where (file:line if given) | Source (the review that found it) |
|---|---|---|---|---|
| 1 | medium | The 5000-run hang against a dead Docker VM is undiagnosed; a load run never ends once its stack stops answering. | - | not recorded |
| 2 | medium | Flaky test TestOutageFaultsAreSampledAndCounted: its 0.2 s window misses under -race. | - | not recorded |
| 3 | medium | 14 chaos rows fail (findings L1–L10, F9, F10 in the findings records); the DP cache bound needs a run longer than 25 h; conflict/constraint/Art. 13(2) notices are never triggered in the lab. | - | not recorded |
| 4 | low | results/load/README.md transcription mismatches: F3548 max 0.97 vs 1.008, raise p99 0.049 vs 0.051. | results/load/README.md | not recorded |
| 5 | low | Mojibake "Â§" in chaos.json. | location changed (chaos.json is now results/20261005-chaos/, results/20261014T215032Z-chaos/, results/20261014T232332Z-chaos/; none contains "Â§" now) | not recorded |
| 6 | low | BacklogLocations are credited as accounted with no load-side presence test. | - | not recorded |
| 7 | low | USSP-self-ended intents can't be told apart in the report. | - | not recorded |
| 8 | low | The conformance reference target writes aud as an array; C3 stays open until a uss_qualifier run accepts the tokens. | - | not recorded |
