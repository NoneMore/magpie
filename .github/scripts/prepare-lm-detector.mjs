// Build-time-only fetch. Release EXEs embed the two assets: end users do
// not need Node, npm, or a network connection to run the bundled plugin.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFile, copyFile, mkdir, stat, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const plugin = path.join(root, "plugins", "lm-detector");
// Verified npm lmfpd data assets paired with the Go SDK v0.2.0.
const version = "0.0.1792416845676";
const expectedSHA256 = Object.freeze({
  "unified_bank.json": "c53d2e446a2c099fa584736fa54356dbdec23cae49130fa504bfb8844466b53a",
  "shared_detector.bin": "9117330f225a525d39d75db2616e90657e6b5d9ea1af46770389530de0a418fe",
});
const npm = process.platform === "win32" ? "npm.cmd" : "npm";
execFileSync(npm, ["install", "--ignore-scripts", "--no-audit", "--no-fund",
  "--no-save", "--no-package-lock", "lmfpd@" + version], {cwd: plugin, stdio: "inherit", shell: process.platform === "win32"});
const source = path.join(plugin, "node_modules", "lmfpd");
const manifest = JSON.parse(await readFile(path.join(source, "package.json"), "utf8"));
if (manifest.version !== version) throw Error("Unexpected lmfpd version: " + manifest.version);
const dest = path.join(plugin, "data");
await mkdir(dest, {recursive: true});
for (const name of ["unified_bank.json", "shared_detector.bin"]) {
  const from = path.join(source, "data", name);
  const size = (await stat(from)).size;
  if (size < 1024 * 1024) throw Error("lmfpd reference asset is unexpectedly small: " + name);
  const digest = createHash("sha256").update(await readFile(from)).digest("hex");
  if (digest !== expectedSHA256[name]) {
    throw Error("LM Detector reference SHA256 mismatch for " + name + ": " + digest);
  }
  console.log("Verified reference SHA256 " + name + ": " + digest);
  await copyFile(from, path.join(dest, name));
  console.log("Bundled " + name + " (" + size + " bytes) from lmfpd@" + version);
}
// Go embed cannot traverse into a different package. Stage a generated copy
// beside the internal/plugin bootstrap rather than making core import plugins/.
const embedded = path.join(root, "internal", "plugin", "bundled-lm-detector", "runtime");
await rm(embedded, {recursive: true, force: true});
await mkdir(embedded, {recursive: true});
// The native binary must not embed the superseded Bun executor, Page or
// vendored TypeScript algorithms. Keep them only as source-level regression tests.
await mkdir(path.join(embedded, "data"), {recursive: true});
for (const name of ["unified_bank.json", "shared_detector.bin"]) {
  await copyFile(path.join(dest, name), path.join(embedded, "data", name));
}
console.log("Prepared data-only offline LM Detector Go embed payload.");
