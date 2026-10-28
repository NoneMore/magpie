// Native LM Detector: Provider-row action, explicit model selection, live
// progress, and stale-results protection in the real Chromium/WebKit page.
// No user settings, network Gateway credentials, or paid models are used.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const providers = ["missing", "direct"].map((id) => ({
  id, name: id, icon: "generic", host: "example.test",
  chat: "https://example.test/v1", responses: "", anthropic: "",
  models: [], agents: [], fallback: [], headers: {}, key: {set: false},
  keyList: [], ready: true,
}));
const data = (name, probability) => ({
  prediction_name: name, evidence: {reason: name + " was ranked first"},
  results: [{display_name: name, probability}],
});
const round = (n, name) => ({
  round: n, analysis: data(name, .9), error: "",
  samples: [{index: 1, state: "complete", count: 300, expectedCount: 300}],
});

function handler(operations) {
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (value) => route.fulfill({json: value});
    if (url.pathname === "/boot.js") {
      return route.fulfill({contentType: "text/javascript",
        body: 'window.bootPrefs={lang:"en",theme:"light",web:false,featuresOff:[]};'});
    }
    if (url.pathname === "/wails/runtime.js") {
      return route.fulfill({contentType: "text/javascript", body: "export const Window={};"});
    }
    if (url.pathname === "/api/state") {
      return json({agents: [], profiles: [], settings: {lang: "en", theme: "light"}});
    }
    if (url.pathname === "/api/providers") {
      return json({providers, presets: [], excluded: [],
        gateway: {running: true, window: true}, plugins: []});
    }
    if (url.pathname === "/api/groups") return json({groups: [], pools: []});
    if (url.pathname === "/api/agents/cli") return json({agents: {}, pending: false});
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/plugin-pages") return json([]);
    if (url.pathname === "/api/plugin-panels") return json([]);
    if (url.pathname === "/api/plugins") return json({plugins: []});
    if (url.pathname === "/api/plugins/listings") return json({listings: []});
    if (url.pathname === "/api/plugins/market") return json({listings: [],state:{plugins:[]}});
    if (url.pathname === "/api/lm-detector/models") return json({models: ["direct/model"]});
    if (url.pathname === "/api/lm-detector/start") {
      const payload = req.postDataJSON();
      operations.started.push(payload);
      const jobId = "job-" + operations.started.length;
      return json({jobId, model: payload.model, state: "running",
        round: 1, repeat: payload.repeat, count: payload.count, completed: 0,
        rounds: [], samples: [{index: 1, state: "queued", count: 0, expectedCount: 300}]});
    }
    if (url.pathname === "/api/lm-detector/status") {
      const jobId = req.postDataJSON().jobId;
      if (jobId === "job-1") return json({
        jobId, state: "done", round: 2, repeat: 2, count: 1, completed: 2,
        samples: [{index: 1, state: "complete", count: 300, expectedCount: 300,
          firstByteMs: 120, durationMs: 2500, startedAt: Date.now()-2500}],
        rounds: [round(1, "Earlier model"), round(2, "Later model")],
      });
      return json({jobId, state: "running", round: 1, repeat: 2,
        count: 1, completed: 0, rounds: [],
        samples: [{index: 1, state: "streaming", count: 100,
          expectedCount: 300, firstByteMs: 100, startedAt: Date.now()-2500}]});
    }
    if (url.pathname === "/api/lm-detector/cancel") {
      return json({jobId: "job-2", state: "cancelled", round: 1, repeat: 2,
        count: 1, completed: 0, rounds: [], samples: []});
    }
    if (url.pathname.startsWith("/api/")) return json({});
    if (url.host !== "magpie.test") return route.fulfill({status: 404,body:""});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = {".html":"text/html", ".js":"text/javascript",
      ".css":"text/css", ".svg":"image/svg+xml", ".png":"image/png"}[path.extname(file)];
    try { return route.fulfill({body: await fs.readFile(file), contentType}); }
    catch { return route.fulfill({status: 404, body:""}); }
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": native detector row actions and per-round results", async (t) => {
    const browser = await (engine === "webkit"
      ? webkit.launch() : chromium.launch({channel:"chromium"}));
    t.after(() => browser.close());
    const context = await browser.newContext({viewport:{width:1100,height:820}});
    t.after(() => context.close());
    const page = await context.newPage();
    page.setDefaultTimeout(10000);
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    const operations = {started: []};
    await page.route("**/*", handler(operations));
    await page.goto("http://magpie.test/");

    async function providerAction(id) {
      await page.evaluate(() => show("providers"));
      const row = page.locator('#providers > .row.provider[data-id="' + id + '"]');
      await row.locator(".name").click({button:"right"});
      await page.locator(".pop.row-menu").waitFor();
      const action = page.locator(".pop.row-menu .rm-item", {hasText:"Detect model fingerprint"});
      assert.equal(await action.count(), 1, "native action appears in provider menu");
      await action.click();
      await page.waitForFunction(() => view === "lm-detector");
    }

    await providerAction("missing");
    const detector = page.locator("#view-lm-detector");
    const run = detector.locator('[data-fpd="run"]');
    await detector.locator('[data-fpd="message"]').getByText("No fingerprintable Gateway models.").waitFor();
    assert.equal(await run.isDisabled(), true, "never choose another provider for a paid request");
    assert.deepEqual(operations.started, []);

    await providerAction("direct");
    await run.waitFor();
    assert.equal(await run.isEnabled(), true, "explicit provider with direct model is selectable");
    await detector.locator('[data-fpd="repeat"]').fill("2");
    await detector.locator('[data-fpd="count"]').fill("1");
    await run.click();
    await page.waitForFunction(() => document.querySelector('#view-lm-detector [data-fpd="winner"]')?.textContent === "Later model");
    assert.equal(operations.started[0].model, "direct/model");
    const selector = detector.locator('[data-fpd="round"]');
    assert.equal(await selector.isVisible(), true, "completed rounds can be replayed");
    await selector.selectOption("1");
    assert.equal(await detector.locator('[data-fpd="winner"]').innerText(), "Earlier model");

    await run.click();
    await page.waitForFunction(() => document.querySelector('#view-lm-detector [data-fpd="samples"]')?.textContent.includes("streaming"));
    assert.equal(await detector.locator('[data-fpd="winner"]').innerText(), "", "new job clears old verdict");
    assert.equal(await detector.locator('[data-fpd="results"] .fpd-result').count(), 0);
    assert.equal(await detector.locator('[data-fpd="samples"]').innerText().then(s => s.includes("TTFT")), true);
    assert.equal(operations.started[1].model, "direct/model");
    await detector.locator('[data-fpd="cancel"]').click();
    assert.deepEqual(errors, [], "no uncaught browser errors");
  });
}
