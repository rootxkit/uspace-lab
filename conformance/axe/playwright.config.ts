import { defineConfig, devices } from "@playwright/test";

// The axe run of the conformance suite (conformance/axe/README.md).
// tests/runner.spec.ts tests the runner itself against fixture pages;
// tests/pages.spec.ts runs it over CONFORMANCE_PAGES. Both are bounded:
// a page that does not load within the navigation timeout is recorded
// as not loaded, never retried.
export default defineConfig({
  testDir: "tests",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  timeout: 120_000,
  reporter: [["list"]],
  use: {
    navigationTimeout: 30_000,
    actionTimeout: 10_000,
    ignoreHTTPSErrors: process.env.CONFORMANCE_AXE_IGNORE_TLS === "1",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
