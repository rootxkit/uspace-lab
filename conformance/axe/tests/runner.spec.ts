import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { test, expect } from "@playwright/test";
import { collect, FORMAT, policyTags } from "./axe";

// The runner's own test, both ways (LESSONS E-01): a clean page is
// recorded with no violation, a page with a known defect with that
// rule, and a page that does not load as not loaded, never as clean.
// The pages are served from memory; the lang attributes cover the two
// languages every console ships (en, ka).
const pages: Record<string, string> = {
  "/clean-en": `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Public map</title></head>
<body><main><h1>Public map</h1><p>U-space zones over Georgia.</p></main></body></html>`,
  "/clean-ka": `<!doctype html><html lang="ka"><head><meta charset="utf-8"><title>საჯარო რუკა</title></head>
<body><main><h1>საჯარო რუკა</h1><p>U-space ზონები საქართველოს თავზე.</p></main></body></html>`,
  "/image-without-alt": `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Defect</title></head>
<body><main><h1>Defect</h1><img src="/pixel.png"></main></body></html>`,
};

let server: Server;
let base = "";

test.beforeAll(async () => {
  server = createServer((req, res) => {
    const body = pages[req.url ?? ""];
    if (req.url === "/pixel.png") {
      res.writeHead(204);
      res.end();
      return;
    }
    if (body === undefined) {
      res.writeHead(404, { "Content-Type": "text/plain" });
      res.end("not found");
      return;
    }
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    res.end(body);
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
});

test.afterAll(async () => {
  await new Promise<void>((resolve) => server.close(() => resolve()));
});

test("clean pages in both languages are recorded with no violation", async ({ browser }) => {
  const r = await collect(browser, [`${base}/clean-en`, `${base}/clean-ka`], policyTags());
  expect(r.format).toBe(FORMAT);
  expect(r.axe_version).not.toBe("");
  for (const p of r.pages) {
    expect(p.error).toBeUndefined();
    expect(p.status).toBe(200);
    expect(p.violations).toEqual([]);
    expect(p.passes).toBeGreaterThan(0);
  }
});

test("a page with an image without alt is recorded with image-alt", async ({ browser }) => {
  const r = await collect(browser, [`${base}/image-without-alt`], policyTags());
  expect(r.pages[0].violations.map((v) => v.id)).toContain("image-alt");
});

test("a page that does not load is recorded as not loaded", async ({ browser }) => {
  const r = await collect(browser, ["http://127.0.0.1:9/unreachable"], policyTags());
  expect(r.pages[0].error).toBeTruthy();
  expect(r.pages[0].violations).toEqual([]);
});
