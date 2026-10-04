import AxeBuilder from "@axe-core/playwright";
import type { Browser } from "@playwright/test";

// The results file cmd/conformance folds in (conformance/a11y,
// format conformance-axe/v1). Nothing here judges: it records what axe
// reported for each page, or that the page did not load.
export const FORMAT = "conformance-axe/v1";

export interface Violation {
  id: string;
  impact: string;
  nodes: number;
}

export interface PageResult {
  url: string;
  status: number;
  error?: string;
  passes: number;
  violations: Violation[];
}

export interface Results {
  format: string;
  axe_version: string;
  standard: string;
  tags: string[];
  pages: PageResult[];
}

// The policy's accessibility bar (conformance/policy.yaml, pending GCAA
// L-Q10), handed over by the Makefile; these defaults are its values.
export function policyTags(): string[] {
  const v = process.env.CONFORMANCE_AXE_TAGS ?? "wcag2a wcag2aa wcag21a wcag21aa wcag22aa";
  return v.split(/[\s,]+/).filter((t) => t !== "");
}

export function policyStandard(): string {
  return process.env.CONFORMANCE_AXE_STANDARD ?? "WCAG 2.2 AA";
}

// collect loads each page in its own context and runs axe with tags.
export async function collect(browser: Browser, urls: string[], tags: string[]): Promise<Results> {
  const out: Results = { format: FORMAT, axe_version: "", standard: policyStandard(), tags, pages: [] };
  for (const url of urls) {
    const context = await browser.newContext();
    const page = await context.newPage();
    const result: PageResult = { url, status: 0, passes: 0, violations: [] };
    try {
      const resp = await page.goto(url, { waitUntil: "load" });
      result.status = resp?.status() ?? 0;
      const axe = await new AxeBuilder({ page }).withTags(tags).analyze();
      out.axe_version = axe.testEngine.version;
      result.passes = axe.passes.length;
      result.violations = axe.violations
        .map((v) => ({ id: v.id, impact: v.impact ?? "unknown", nodes: v.nodes.length }))
        .sort((a, b) => a.id.localeCompare(b.id));
    } catch (err) {
      result.error = err instanceof Error ? err.message.split("\n")[0] : String(err);
    } finally {
      await context.close();
    }
    out.pages.push(result);
  }
  return out;
}
