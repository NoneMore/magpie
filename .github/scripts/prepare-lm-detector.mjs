// Build-time-only fetch. Release EXEs embed the two assets: end users do
// not need Node, npm, or a network connection to run the bundled plugin.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFile, copyFile, mkdir, stat, cp, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const plugin = path.join(root, "plugins", "lm-detector");
const version = "0.0.1792416845676";
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
  console.log("Reference SHA256 " + name + ": " + digest);
  await copyFile(from, path.join(dest, name));
  console.log("Bundled " + name + " (" + size + " bytes) from lmfpd@" + version);
}
// Go embed cannot traverse into a different package. Stage a generated copy
// beside the internal/plugin bootstrap rather than making core import plugins/.
const embedded = path.join(root, "internal", "plugin", "bundled-lm-detector", "runtime");
await rm(embedded, {recursive: true, force: true});
await mkdir(embedded, {recursive: true});
for (const file of ["index.js", "package.json"]) {
  await copyFile(path.join(plugin, file), path.join(embedded, file));
}
for (const directory of ["page", "shared", "data"]) {
  await cp(path.join(plugin, directory), path.join(embedded, directory), {recursive: true});
}
console.log("Prepared offline LM Detector Go embed payload.");
