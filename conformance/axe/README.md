# axe run (informative)

Accessibility of the public pages a target names (docs/PLAN.md §7.1
L-Q10: WCAG 2.2 AA assumed, pending the ministry and GCAA). Playwright
loads each page and runs axe with the policy's tags
(`conformance/policy.yaml` `accessibility.axe_tags`); the results are
folded into the report as A11Y-PUBLIC, which is informative: reported,
never failing a run.

```
make conformance-axe-test       # the runner's own test, both ways
CONFORMANCE_PAGES="https://cisp.example/ https://cisp.example/?lang=ka" make conformance-axe
CONFORMANCE_AXE=conformance/axe/axe-results.json make conformance TARGET=cisp
```

`tests/runner.spec.ts` serves fixture pages from memory: clean pages in
English and Georgian are recorded with no violation, a page with an
image without `alt` is recorded with `image-alt`, and a page that does
not load is recorded as not loaded (never as clean).

`CONFORMANCE_AXE_IGNORE_TLS=1` accepts the lab CA's certificates without
trusting them system-wide.
