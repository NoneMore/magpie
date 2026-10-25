// No browser dependency: keep fork-only Agent settings decoupled from
// app.js's upstream-owned list and prove the registration contract.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");

const assets = path.resolve(__dirname, "../assets");
class Element {
  constructor(tag = "div") {
    this.tagName = tag;
    this.id = "";
    this.dataset = {};
    this.children = [];
    this.parentElement = null;
  }
  append(...children) {
    for (const child of children) {
      if (child.parentElement) child.parentElement.children = child.parentElement.children.filter(x => x !== child);
      this.children.push(child);
      child.parentElement = this;
    }
  }
  insertBefore(child, before) {
    if (child.parentElement) child.parentElement.children = child.parentElement.children.filter(x => x !== child);
    const at = this.children.indexOf(before);
    if (at < 0) throw new Error("missing anchor");
    this.children.splice(at, 0, child);
    child.parentElement = this;
  }
  replaceChildren(...children) {
    this.children = [];
    this.append(...children);
  }
}
function fixture() {
  const host = new Element();
  host.id = "agentPrefRows";
  const row = new Element();
  row.id = "codexAgentsV1Row";
  host.append(row);
  const walk = (node, id) => node.id === id ? node : node.children.map(c => walk(c, id)).find(Boolean);
  const document = {
    createElement: (tag) => new Element(tag),
    getElementById: (id) => walk(host, id),
  };
  const ctx = vm.createContext({ window: {}, document, I18N: {}, console });
  vm.runInContext(fs.readFileSync(path.join(assets, "kernel.js"), "utf8"), ctx, { filename: "kernel.js" });
  ctx.magpie = ctx.window.magpie;
  vm.runInContext(fs.readFileSync(path.join(assets, "fork/codex-caller-prompt.js"), "utf8"), ctx, { filename: "codex-caller-prompt.js" });
  return { document, host, magpie: ctx.magpie, I18N: ctx.I18N };
}

test("fork agent preference merges before upstream options and preserves others", () => {
  const { magpie, host, document } = fixture();
  const upstream = ["codexAgentsV1Row", "codexSubagentModelRow", "fullContextRow"];
  assert.deepEqual(Array.from(magpie.agentPrefs.rows("codex", upstream)),
    ["codexCallerPromptRow", ...upstream]);
  assert.deepEqual(upstream, ["codexAgentsV1Row", "codexSubagentModelRow", "fullContextRow"]);
  assert.equal(magpie.agentPrefs.rows("claude", null), null);
  assert.deepEqual(host.children.map(x => x.id), ["codexCallerPromptRow", "codexAgentsV1Row"]);
  assert.equal(document.getElementById("codexCallerPromptRow").children.length, 2);
  assert.equal(document.getElementById("codexCallerPromptSegs").id, "codexCallerPromptSegs");
});

test("fork option follows backend state and posts correct boolean toggles", () => {
  const { magpie, document } = fixture();
  const calls = [];
  const options = {
    segmented: (choices, chosen, pick) => ({ choices, chosen, pick }),
    translate: (s) => s,
    save: (path, body) => calls.push({ path, body }),
  };
  magpie.agentPrefs.render({ codexCallerPromptOn: true }, options);
  const control = document.getElementById("codexCallerPromptSegs");
  assert.equal(control.children[0].chosen, "on");
  control.children[0].pick("off");
  magpie.agentPrefs.render({ codexCallerPromptOn: false }, options);
  assert.equal(control.children[0].chosen, "off");
  control.children[0].pick("on");
  // The UI handler runs inside a VM realm: normalize the payload's realm
  // before comparing its value (objects from two realms have distinct prototypes).
  assert.deepEqual(JSON.parse(JSON.stringify(calls)), [
    { path: "settings/codex-caller-prompt", body: { on: false } },
    { path: "settings/codex-caller-prompt", body: { on: true } },
  ]);
});

test("registration is protected from duplicates, and translations stay isolated", () => {
  const { magpie, I18N, document } = fixture();
  assert.throws(() => magpie.agentPrefs.register("codex", {
    id: "codexCallerPromptRow", controlId: "newControl", setting: "a", endpoint: "settings/a",
  }), /duplicated/);
  for (const lang of ["zh", "ja", "de"]) {
    assert.ok(I18N[lang]["Caller-owned Codex instructions (experimental)"]);
  }
  assert.ok(document.getElementById("codexCallerPromptRow"));
  magpie.start();
  assert.throws(() => magpie.agentPrefs.register("codex", {
    id: "lateRow", controlId: "lateControl", setting: "a", endpoint: "settings/a",
  }), /after start/);
});

test("app.js depends on registry instead of hardcoding the fork row", () => {
  const app = fs.readFileSync(path.join(assets, "app.js"), "utf8");
  const html = fs.readFileSync(path.join(assets, "index.html"), "utf8");
  assert.match(app, /magpie\.agentPrefs\.rows\(/);
  assert.match(app, /magpie\.agentPrefs\.render\(/);
  assert.doesNotMatch(app, /codexCallerPromptRow|codexCallerPromptSegs/);
  assert.doesNotMatch(html, /id="codexCallerPromptRow"/);
  assert.match(html, /src="fork\/codex-caller-prompt\.js"/);
  assert.ok(html.indexOf('src="fork/codex-caller-prompt.js"') < html.indexOf('src="app.js"'));
});
