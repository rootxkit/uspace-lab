import { writeFileSync } from "node:fs";
import { test, expect } from "@playwright/test";
import { collect, policyTags } from "./axe";

// The axe run over the target's public pages: CONFORMANCE_PAGES (space
// separated URLs) in, CONFORMANCE_AXE_OUT (default axe-results.json)
// out. It fails only when it cannot write its results; what axe found is
// for cmd/conformance to fold in as informative (A11Y-PUBLIC).
test("axe over the target's public pages", async ({ browser }) => {
  const urls = (process.env.CONFORMANCE_PAGES ?? "").split(/\s+/).filter((u) => u !== "");
  test.skip(urls.length === 0, "CONFORMANCE_PAGES names no page");
  const results = await collect(browser, urls, policyTags());
  const out = process.env.CONFORMANCE_AXE_OUT ?? "axe-results.json";
  writeFileSync(out, JSON.stringify(results, null, 2) + "\n");
  for (const p of results.pages) {
    console.log(`${p.url}: status ${p.status}, ${p.violations.length} violations${p.error ? ", error " + p.error : ""}`);
  }
  expect(results.pages).toHaveLength(urls.length);
});
